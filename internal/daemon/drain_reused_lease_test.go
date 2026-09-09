package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent is EVIDENCE for the open carried
// defect SP08-D2. It asserts the CURRENT at-least-once outcome, not the desired one, and it must
// be inverted (or replaced by the observer-level assertion named below) by the fix that makes
// captureSubagent and supersession idempotent under a reused lease.
//
// The window it pins is the one the code promises to tolerate (ingest.go dispatch: "Restart does
// not retain this set, so handlers must tolerate at-least-once delivery"): a delivery the live
// path LEASED and whose handler RAN — the sidecar is durable, the observer's reference writes
// landed — but whose acknowledgement never reached the journal because the process died between
// publication and commitDelivery (ingest.go, stage 3). The next daemon starts with an empty
// seenSet, finds the WAL copy the dying process never drained (and the client's fallback copy,
// spooled because the one-byte transport ACK never left that process), re-takes the SAME lease
// (delivery_lease.go lease is idempotent for a known delivery) and dispatches it once more under
// the SAME ObservationID (observer.WithObservation). Reusing the identity is correct and must
// stay; the defect is that the second dispatch is not absorbed.
//
// The cut is placed where the crash is: the ingest worker's journal resolver answers the lease at
// Accept but refuses the frontier write, the shape TestCrashCutBetweenReferenceAndFrontierRedelivers
// (delivery_publication_test.go) gives the drain-side worker. Withholding the acknowledgement
// this way, rather than failing the handler, is deliberate: a handler that returns an error is the
// SP05-D1 retry (TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending,
// TestDrainRejectedResponseIsRetryableAfterRestart) and has no side effects to absorb, and
// deleting the ack line afterwards changes nothing the open journal reads (acknowledged answers
// from its in-memory acks; checkAckFile refuses a file edited behind the handle). Only "handler
// committed, frontier did not" is the window.
//
// The observing handler here is the daemon-level counting stub, so the honest evidence at this
// level is the drain fact: the handler runs twice under one reused lease, and both copies reach
// it as one delivery. The two observer sites that fail to absorb that second run are:
//
//   - (P2) observer/stop.go captureSubagent mints SubagentCaptureID(e.SessionID, st.Turn) from
//     the fresh process's per-session turn counter (0 on restart) instead of the reused
//     observation identity, so a second SubagentStop capture blob and index/tool_use.jsonl
//     record appear for one event (x09's phantom subagent_<session>_0).
//   - (P3) the read-supersede path (observer/supersede.go detectSupersession, reached from
//     tooluse.go, marking through Store.MarkSuperseded) lets a replayed read whose content is
//     OLDER supersede records appended AFTER it.
//
// The observer-level assertion for those two sites belongs to the fix. This file must not be
// confused with TestDrainDoesNotRedeliverAnAcknowledgedClientCopy (F4-P1), whose delivery IS on
// the frontier and must not be dispatched at all; here the frontier is genuinely empty and the
// redelivery is legitimate. TestCrashCutBetweenReferenceAndFrontierRedelivers already pins that
// the drain's own retry reuses the identity and mints one sidecar; its assertions are not
// repeated, this test adds the live-path cut and the second invocation itself.
//
// The pin is not vacuous: a seenSet that already holds the key as completed (what "the set
// survived the restart" would look like) takes the drain's completed branch and never dispatches,
// and the second-invocation assertion below is the one that fails. Note that the crash-cut leaves
// the key out of the set anyway (seenSet.finish drops an unacknowledged key), so a same-process
// retry redelivers too; the restart shape is used because it is the one x09 produced.
func TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent(t *testing.T) {
	root := t.TempDir()
	dd, calls, ids := newIdentityRecordingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	token := testDeliveryToken('9')
	req := observeRequest(token, "sess-reused-lease", `{"hook_event_name":"PostToolUse"}`)

	// The crash window: Accept's lease resolves the journal and succeeds; the worker's
	// commitDelivery resolves it again, after the handler ran, and finds it gone. There is no
	// daemon-side QOMPACK_FAULT seam (spawn.go strips it), so the cut is the dependency itself.
	cut := true
	dd.ing.journal = func() (*deliveryJournal, error) {
		j, err := dd.deliveryJournal()
		if err != nil || !cut {
			return j, err
		}
		if _, leased := j.leases[token]; leased {
			return nil, deliveryJournalError() // the frontier write, and only it, is cut
		}
		return j, nil
	}
	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	lease, leased := journal.leases[token]
	require.True(t, leased, "fixture: the live path took the lease")
	require.False(t, journal.acknowledged(token), "fixture: the frontier was not committed")
	require.Equal(t, 1, calls(), "fixture: the handler ran once before the crash")
	require.Equal(t, []core.ObservationID{lease.ObservationID}, ids(),
		"fixture: the handler saw the leased identity")
	require.Len(t, sidecarFiles(t, root), 1, "fixture: stage 1 is durable")

	// Restart. The dying process never drained its WAL copy, and the hook client, whose transport
	// ACK never arrived, spooled its own copy. The seen set is process memory; the lease is not.
	cut = false
	require.NoError(t, dd.ing.Close())
	wals, err := filepath.Glob(paths.Long(filepath.Join(paths.Of(root).Spool, "wal-*.ndjson")))
	require.NoError(t, err)
	require.NotEmpty(t, wals, "fixture: the WAL copy survived the crash")
	const copyName = "client-00007.ndjson"
	writeSpoolLine(t, root, copyName, req)
	dd.ing.seen = newSeenSet(seenCapacity)
	dd.drain.Load().cfg.Seen = dd.ing.seen

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)

	// The defect half, pinned as it stands: the handler is invoked a SECOND time under the reused
	// lease. Nothing in the daemon absorbs this (the frontier was empty, so it must not); the
	// absorption is the observer's job at the two sites named above, and there it does not
	// happen. When the fix lands, this count stays 2 and the assertion that moves is the
	// observer's: one SubagentStop capture id per observation, no supersede mark from a replay.
	require.Equal(t, 2, calls(),
		"SP08-D2 evidence: the at-least-once redelivery runs the handler again under the same "+
			"ObservationID (ingest.go dispatch: handlers must tolerate at-least-once delivery)")

	// The correct half: one identity, one delivery, one sidecar, one frontier record.
	require.Equal(t, []core.ObservationID{lease.ObservationID, lease.ObservationID}, ids(),
		"a redelivery under a crash-cut frontier keeps the identity it was first assigned")
	require.Equal(t, 1, n, "the WAL copy is dispatched; the client copy is the same delivery")
	require.Len(t, sidecarFiles(t, root), 1, "the identity is reused, not re-minted")
	require.True(t, journal.acknowledged(token), "the redelivery reaches the frontier")
	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(root).Spool, copyName)),
		"the client copy is released once its offset is past the frontier")
	require.True(t, dd.DrainGaps().Complete, "a redelivery is not a gap")
}

// newIdentityRecordingDaemon is newObservingDaemon with a handler that also records the
// observation identity each invocation carried on its context (observer.ObservationFrom), so a
// test can tell a reused lease from a re-minted one at the handler rather than by counting
// sidecars alone.
func newIdentityRecordingDaemon(t *testing.T, root string) (*daemon, func() int, func() []core.ObservationID) {
	t.Helper()
	var seen []core.ObservationID
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(ctx context.Context, _ hookio.Event) error {
			seen = append(seen, observer.ObservationFrom(ctx))
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
	return dd, func() int { return len(seen) }, func() []core.ObservationID {
		return append([]core.ObservationID(nil), seen...)
	}
}

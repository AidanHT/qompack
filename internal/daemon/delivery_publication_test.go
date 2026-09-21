package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// admittedCapture is what the hook client attaches to a permitted delivery.
func admittedCapture(payload string) *hookio.Capture {
	return &hookio.Capture{
		Version: core.EvidenceVersion, Bytes: []byte(payload), SourceFormat: "application/json",
		PolicyVersion: "redact-json/v1", HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityExact, Outcome: core.OutcomeOK,
		SourceBytes: len(payload), HostFields: []string{"hook_event_name", "session_id"},
	}
}

func observeRequest(nonce, session, payload string) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: core.SessionID(session), TS: core.UnixMilli(epoch.UnixMilli()),
		Event:   &hookio.Event{HookEventName: "PostToolUse", SessionID: core.SessionID(session)},
		Capture: admittedCapture(payload), Nonce: nonce,
	}
}

// TestDeliveryJournal_AcknowledgementIsTheCommittedFrontier pins the third publication stage: an
// assignment is not a publication, an acknowledgement names a real assignment, and a redelivery of
// an already-acknowledged token appends nothing.
func TestDeliveryJournal_AcknowledgementIsTheCommittedFrontier(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	ctx := context.Background()
	token := testDeliveryToken('a')

	require.False(t, journal.acknowledged(token), "an unleased delivery is never acknowledged")
	require.ErrorIs(t, journal.acknowledge(ctx, token, "unassigned", core.Hash{}), core.ErrContract,
		"a frontier record may not name an assignment that does not exist")

	lease, err := journal.lease(ctx, token, "session", testDeliveryRequest("x"))
	require.NoError(t, err)
	require.False(t, journal.acknowledged(token), "a lease alone is not a publication")

	require.ErrorIs(t, journal.acknowledge(ctx, token, "wrong-identity", core.Hash{}), core.ErrContract)
	require.False(t, journal.acknowledged(token))

	require.NoError(t, journal.acknowledge(ctx, token, lease.ObservationID, core.Hash{}))
	require.True(t, journal.acknowledged(token))

	before, err := os.ReadFile(filepath.Join(paths.Of(journalRoot(journal)).State, deliveryAckFile))
	require.NoError(t, err)
	require.NoError(t, journal.acknowledge(ctx, token, lease.ObservationID, core.Hash{}),
		"acknowledging twice is the retry case and must be idempotent")
	after, err := os.ReadFile(filepath.Join(paths.Of(journalRoot(journal)).State, deliveryAckFile))
	require.NoError(t, err)
	require.Equal(t, before, after, "an idempotent acknowledgement appends nothing")
}

// TestDeliveryJournal_AcknowledgementSurvivesReopen is crash cut 3: the frontier was committed and
// the process died. A restart must still read the delivery as published.
func TestDeliveryJournal_AcknowledgementSurvivesReopen(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	ctx := context.Background()
	token := testDeliveryToken('b')
	lease, err := journal.lease(ctx, token, "session", testDeliveryRequest("x"))
	require.NoError(t, err)
	require.NoError(t, journal.acknowledge(ctx, token, lease.ObservationID, core.Hash{}))
	require.NoError(t, lock.Release())

	next, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = next.Release() })
	recovered, err := next.openDeliveryJournal()
	require.NoError(t, err)
	require.True(t, recovered.acknowledged(token), "a committed frontier must survive restart")

	replayed, err := recovered.lease(ctx, token, "session", testDeliveryRequest("x"))
	require.NoError(t, err)
	require.Equal(t, lease.ObservationID, replayed.ObservationID,
		"a redelivered token keeps the identity it was first assigned")
}

// TestDeliveryJournal_AcknowledgementAheadOfItsLeaseIsRefused is crash cut 2 inverted: an
// acknowledgement whose assignment did not survive is a frontier ahead of its own evidence, and the
// journal must refuse to reopen rather than present it as published.
func TestDeliveryJournal_AcknowledgementAheadOfItsLeaseIsRefused(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	ctx := context.Background()
	token := testDeliveryToken('c')
	lease, err := journal.lease(ctx, token, "session", testDeliveryRequest("x"))
	require.NoError(t, err)
	require.NoError(t, journal.acknowledge(ctx, token, lease.ObservationID, core.Hash{}))
	require.NoError(t, lock.Release())

	// Remove the assignment, keep the frontier.
	state := paths.Of(root).State
	require.NoError(t, os.WriteFile(filepath.Join(state, deliveryLeaseFile), nil, 0o600))
	seed, err := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Chain: deliveryChainSeed})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(state, deliveryPositionFile), seed, 0o600))

	next, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = next.Release() })
	_, err = next.openDeliveryJournal()
	require.ErrorIs(t, err, core.ErrDegraded)
}

func journalRoot(j *deliveryJournal) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(j.path)))
}

// TestDispatchOpDeniesBeforeAnythingIsPersisted covers invariant 1 at the live entry point: a
// delivery the policy refuses reaches no WAL, no sidecar and no handler.
func TestDispatchOpDeniesBeforeAnythingIsPersisted(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)

	req := observeRequest(testDeliveryToken('d'), "sess-denied", `{"hook_event_name":"PostToolUse"}`)
	req.Capture.Outcome, req.Capture.Fidelity, req.Capture.Bytes = core.OutcomeDenied, core.FidelityUnknown, nil

	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK, "a denial is a decision, not a transport failure the client should retry")
	require.Contains(t, string(resp.Data), string(core.OutcomeDenied))
	require.Equal(t, 0, calls())

	entries, _ := os.ReadDir(paths.Of(root).Spool)
	require.Empty(t, entries, "a denied delivery must not reach the WAL")
}

// TestDispatchOpRefusesAnUndecidableCapture: a capture NO policy decided is a gap, not a
// publication.
//
// This test used to construct an OVERSIZE record — Outcome=Unavailable, Fidelity=Truncated,
// CaptureError=Oversize — and assert it was refused. That fixture was wrong, and the assertion
// built on it encoded the data-loss defect rather than a requirement: an oversize verdict IS a
// decision, taken by a policy that ran, over a payload that arrived, and it is the only record
// that will ever say so. It is now covered by
// TestDispatchOpPersistsADegradedCaptureAsEvidence, which asserts the opposite outcome for the
// opposite reason. What remains here is the genuinely undecidable case: a record whose capture
// error names THIS PROCESS's inability to classify anything (no compiled policy), which is a hole
// in the record and not a fact about the delivery.
func TestDispatchOpRefusesAnUndecidableCapture(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)

	req := observeRequest(testDeliveryToken('e'), "sess-degraded", "")
	req.Capture.Outcome, req.Capture.Fidelity = core.OutcomeUnavailable, core.FidelityUnknown
	req.Capture.CaptureError, req.Capture.Bytes = core.CaptureErrorPolicy, nil

	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK)
	require.Contains(t, string(resp.Data), string(core.OutcomeUnavailable))
	require.Equal(t, 0, calls(), "publication is blocked when the capture is not admissible")
	entries, _ := os.ReadDir(paths.Of(root).Spool)
	require.Empty(t, entries)
}

// TestDispatchOpPersistsADegradedCaptureAsEvidence is BLOCKER 1's daemon-UP half: the record the
// hook client mints for an over-budget payload — FidelityTruncated / CaptureErrorOversize /
// OutcomeUnavailable, a bounded permitted prefix, the real source size, and NO Event — must be
// persisted as evidence by the resident daemon, exactly as the spool path persists it.
//
// Before the fix admitDelivery mapped it to Failed, dispatchOp returned before any route, and the
// whole record was discarded while the client was told OK — a delivery that left no WAL line, no
// sidecar and no trace anywhere, on the one path the existing tests for that fix did not drive.
func TestDispatchOpPersistsADegradedCaptureAsEvidence(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	token := testDeliveryToken('7')
	req := observeRequest(token, "sess-oversize", `{"hook_ev`)
	req.Capture.Bytes = nil // V6: the hook drops prefixes whose scope cannot be proved.
	req.Event = nil         // hookio derived none, and none may be invented from a payload it refused
	req.Capture.Outcome, req.Capture.Fidelity = core.OutcomeUnavailable, core.FidelityTruncated
	req.Capture.CaptureError, req.Capture.Truncated = core.CaptureErrorOversize, true
	req.Capture.SourceBytes = 4 << 20

	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	sc := readOnlySidecar(t, root)
	require.Equal(t, core.OutcomeUnavailable, sc.Outcome, "the sidecar records the decision as taken")
	require.Equal(t, core.FidelityTruncated, sc.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, sc.CaptureError)
	require.True(t, sc.Truncated)
	require.Equal(t, 4<<20, sc.SourceBytes, "the observed delivery size survives even when the bytes do not")
	require.Empty(t, sc.Bytes, "scope-unprovable bytes are withheld; classification is the evidence")

	require.Equal(t, 0, calls(),
		"a capture with no derived Event publishes evidence, never a synthetic observation")

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(token),
		"the evidence is durable, so the delivery is accounted for rather than redelivered forever")
}

// TestDispatchOpObservesAnOversizePayloadAtTheShippedDefault is BLOCKER 1's regression half, and
// it is about the SHIPPED configuration rather than a lowered budget.
//
// runtime.hotPath.maxPayloadBytes defaults to 1 MiB, whose capture limit is the 4 MiB hard cap, so
// hookio admits a ~400 KB PostToolUse payload with OutcomeOK and a complete Event. ipc.WithCapture
// then finds the permitted bytes over CaptureFrameBudget (393,216 B) and downgrades the CAPTURE
// half to unavailable/oversize — while leaving the Event exactly where it was. Before the fix the
// daemon read that downgrade as a refusal of the whole delivery and the tool use was never
// observed at all: no RecordToolUse, no sidecar, no file-version history, for any ordinary read of
// a 400 KB file.
func TestDispatchOpObservesAnOversizePayloadAtTheShippedDefault(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	const payloadBytes = 400 * 1000
	permitted := `{"hook_event_name":"PostToolUse","tool_response":"` +
		strings.Repeat("x", payloadBytes) + `"}`
	require.Greater(t, len(permitted), ipc.CaptureFrameBudget,
		"the fixture must actually cross the frame budget or it proves nothing")

	base := observeRequest(testDeliveryToken('8'), "sess-shipped", "")
	admitted := *admittedCapture(permitted)
	req := ipc.WithCapture(ipc.Request{
		Op: base.Op, Session: base.Session, TS: base.TS, Event: base.Event, Nonce: base.Nonce,
	}, admitted)

	require.NotNil(t, req.Event, "WithCapture must never take the observation away")
	require.Equal(t, core.OutcomeUnavailable, req.Capture.Outcome)

	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	require.Equal(t, 1, calls(), "the tool use is observed; only the evidence half was degraded")
	sc := readOnlySidecar(t, root)
	require.Equal(t, core.OutcomeUnavailable, sc.Outcome)
	require.Equal(t, len(permitted), sc.SourceBytes,
		"the record still measures what the host delivered")
}

// TestDispatchOpAdmitsARequestThatCarriesNoDecision is the direct-IPC-caller case: no capture at
// all, so the daemon must decide before persisting — and must never claim exact fidelity for a
// payload it reconstructed from an already-decoded Event.
func TestDispatchOpAdmitsARequestThatCarriesNoDecision(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	v := dd.admitDelivery(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-bare", TS: core.UnixMilli(epoch.UnixMilli()),
		Event: &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-bare"},
	})
	require.False(t, v.Denied)
	require.False(t, v.Failed)
	require.NotNil(t, v.Request.Capture)
	require.Equal(t, core.OutcomeOK, v.Request.Capture.Outcome)
	require.NotEqual(t, core.FidelityExact, v.Request.Capture.Fidelity,
		"a reconstruction is never the host's literal delivery")
	require.NotEmpty(t, v.Request.Capture.HostFields)
}

// TestAdmissionNeverRestoresRemovedContent: a decision that already redacted the payload is taken
// as given. Re-running a policy could only work from the derived Event, whose fields still hold the
// original text, and that would put back exactly what the first policy removed.
func TestAdmissionNeverRestoresRemovedContent(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	const secret = "tok_live_012345678901234567890123"
	req := observeRequest(testDeliveryToken('f'), "sess-redacted", `{"prompt":"[REDACTED]"}`)
	req.Capture.Fidelity, req.Capture.Redacted = core.FidelityRedacted, true
	req.Event.Prompt = secret

	v := dd.admitDelivery(req)
	require.False(t, v.Failed)
	require.NotContains(t, string(v.Request.Capture.Bytes), secret)
	require.True(t, v.Request.Capture.Redacted)
}

// TestEqualContentDeliveriesStayDistinct is invariant 2: two deliveries with byte-identical
// payloads are two deliveries, and both are observed.
func TestEqualContentDeliveriesStayDistinct(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	payload := `{"hook_event_name":"PostToolUse"}`
	first := observeRequest(testDeliveryToken('1'), "sess-dup", payload)
	second := observeRequest(testDeliveryToken('2'), "sess-dup", payload)

	require.True(t, dd.dispatchOp(context.Background(), first).OK)
	require.True(t, dd.dispatchOp(context.Background(), second).OK)
	drainRing(t, dd)
	require.Equal(t, 2, calls(), "equal bytes from distinct deliveries must both be observed")

	sidecars := sidecarFiles(t, root)
	require.Len(t, sidecars, 2, "two deliveries produce two capture sidecars")
}

// TestRedeliveryOfOneNonceIsObservedOnce is the other half of invariant 2: one delivery sent twice
// keeps one identity and is published once.
func TestRedeliveryOfOneNonceIsObservedOnce(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	req := observeRequest(testDeliveryToken('3'), "sess-retry", `{"hook_event_name":"PostToolUse"}`)
	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)
	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	require.Equal(t, 1, calls(), "one delivery is published once, however many times it arrives")
	require.Len(t, sidecarFiles(t, root), 1)
}

// TestPublicationOrderIsObjectReferenceFrontier walks the three stages and asserts the frontier is
// reached only after both writes, and never when the reference write fails.
func TestPublicationOrderIsObjectReferenceFrontier(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemonWithResult(t, root, func() error { return context.Canceled })
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	token := testDeliveryToken('4')
	req := observeRequest(token, "sess-order", `{"hook_event_name":"PostToolUse"}`)
	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	// Stage 1 completed: the capture is durable.
	require.Len(t, sidecarFiles(t, root), 1)
	// Stage 3 did not: the reference write failed, so nothing is published.
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, journal.acknowledged(token),
		"a failed reference write must block the committed frontier")

	sc := readOnlySidecar(t, root)
	require.False(t, sc.Published, "a durable capture with no reference is not a published handle")
	require.Equal(t, core.OutcomeOK, sc.Outcome)
}

// TestDrainAdmitsAndAcknowledgesInheritedRecords covers the spool entry point end to end: an
// inherited record with no capture is admitted here, leased, published, and acknowledged, and its
// offset advances only then.
func TestDrainAdmitsAndAcknowledgesInheritedRecords(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	token := testDeliveryToken('5')
	req := observeRequest(token, "sess-spool", `{"hook_event_name":"PostToolUse"}`)
	req.Capture = nil // an inherited record: nothing decided it before the transport
	writeSpoolLine(t, root, "client-00001.ndjson", req)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls())

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(token), "a drained publication reaches the frontier")

	gaps := dd.DrainGaps()
	require.True(t, gaps.Observed)
	require.True(t, gaps.Complete, "a fully replayed spool has no gaps: %+v", gaps.Gaps)
}

// TestDrainReportsGapsInsteadOfSilentCompleteness is the M2-02 producer side: a corrupt line is a
// hole in the record, and the drain says so rather than reporting a complete replay.
func TestDrainReportsGapsInsteadOfSilentCompleteness(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	require.Equal(t, DrainGapState{}, dd.DrainGaps(), "no replay yet is unknown, not complete")

	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, "client-00002.ndjson")),
		[]byte("{not json}\n"), 0o600))

	_, err := dd.Drain(context.Background())
	require.NoError(t, err)

	gaps := dd.DrainGaps()
	require.True(t, gaps.Observed)
	require.False(t, gaps.Complete)
	require.Len(t, gaps.Gaps, 1)
	require.Equal(t, DrainGapCorruptLine, gaps.Gaps[0].Kind)
	require.Equal(t, 1, gaps.Gaps[0].Count)
}

// TestSessionEndRecordsRecoveryNeeded is T20-M1-05: an interrupted SessionEnd leaves a state that
// says so, and a completed one clears it.
func TestSessionEndRecordsRecoveryNeeded(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Empty(t, sr.Sessions)

	// An interrupted flush: the drained-flush path never runs the replay that finishes it.
	resp := dd.flushRoute(context.Background(), ipc.Request{
		Op: ipc.OpFlush, Session: "sess-end",
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: "sess-end"},
	}, false)
	require.True(t, resp.OK)

	sr, err = LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, core.SessionID("sess-end"),
		"SessionEnd must record a recovery-needed state rather than finalize unacknowledged work")
	require.Equal(t, recoveryStageSketches, sr.Sessions["sess-end"].Stage)

	// The full flush runs the replay and finishes.
	resp = dd.flushRoute(context.Background(), ipc.Request{
		Op: ipc.OpFlush, Session: "sess-end",
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: "sess-end"},
	}, true)
	require.True(t, resp.OK)
	sr, err = LoadSessionRecovery(root)
	require.NoError(t, err)
	require.NotContains(t, sr.Sessions, core.SessionID("sess-end"))
}

// TestBoundedQueueDropsRatherThanBlocks pins the admission bound the ring already enforces: a full
// ring never blocks Accept, and the WAL still holds every line.
func TestBoundedQueueDropsRatherThanBlocks(t *testing.T) {
	root := t.TempDir()
	ing := newIngest(root, testConfig(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })

	for i := 0; i < ringCapacity+8; i++ {
		require.NoError(t, ing.Accept(ipc.Request{Op: ipc.OpObserveTool, Session: "sess-full"},
			[]byte(`{"op":"observe.tool","s":"sess-full"}`)))
	}
	require.Len(t, ing.ring, ringCapacity, "the queue is bounded and does not grow")

	wal, err := os.ReadFile(paths.Long(walPath(paths.Of(root).Spool, "sess-full", 0)))
	require.NoError(t, err)
	require.Equal(t, ringCapacity+8, strings.Count(string(wal), "\n"),
		"every accepted line is durable even when the queue refused it")
}

// ---------------------------------------------------------------------------
// Fixtures. There is no daemon-side QOMPACK_FAULT seam: spawn.go strips that variable from a
// spawned daemon deliberately, and every cut this file needs is a file-state cut a fixture can
// produce directly and deterministically — the style delivery_lease_failure_test.go and
// drain_recovery_test.go already use.

func newObservingDaemon(t *testing.T, root string) (*daemon, func() int) {
	t.Helper()
	return newObservingDaemonWithResult(t, root, func() error { return nil })
}

func newObservingDaemonWithResult(t *testing.T, root string, result func() error) (*daemon, func() int) {
	t.Helper()
	calls := 0
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(context.Context, hookio.Event) error {
			calls++
			return result()
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
	return dd, func() int { return calls }
}

// lockFor gives the daemon the singleton lock its delivery journal lives under, the way Run would.
func lockFor(t *testing.T, dd *daemon, root string) *Lock {
	t.Helper()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	dd.startMu.Lock()
	dd.lock = lock
	dd.startMu.Unlock()
	return lock
}

// drainRing runs the queued jobs synchronously, which is what the worker pool would do.
func drainRing(t *testing.T, dd *daemon) {
	t.Helper()
	for {
		select {
		case j := <-dd.ing.ring:
			dd.ing.dispatch(context.Background(), dd.runIngested, j)
		default:
			return
		}
	}
}

func writeSpoolLine(t *testing.T, root, name string, req ipc.Request) {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, name)), append(line, '\n'), 0o600))
}

func sidecarFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	base := filepath.Join(paths.Of(root).Records, "captures")
	_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func readOnlySidecar(t *testing.T, root string) store.CaptureSidecar {
	t.Helper()
	files := sidecarFiles(t, root)
	require.Len(t, files, 1)
	b, err := os.ReadFile(files[0])
	require.NoError(t, err)
	var sc store.CaptureSidecar
	require.NoError(t, json.Unmarshal(b, &sc))
	return sc
}

// TestCrashCutBetweenReferenceAndFrontierRedelivers is crash cut 2: the reference write completed
// and the process died before the frontier record was committed. The spool offset must NOT have
// advanced, the delivery must reappear, and a second pass must publish it under the SAME identity.
func TestCrashCutBetweenReferenceAndFrontierRedelivers(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	// The cut: the lease succeeds, the acknowledgement's journal lookup does not. There is no
	// daemon-side QOMPACK_FAULT seam — spawn.go strips that variable deliberately — so the cut is
	// produced by the dependency itself, the way the delivery-lease and drain-recovery fixtures do.
	cut := true
	journal := func() (*deliveryJournal, error) {
		j, err := dd.deliveryJournal()
		if err != nil || !cut {
			return j, err
		}
		if _, leased := j.leases[testDeliveryToken('6')]; leased {
			return nil, deliveryJournalError() // the frontier write, and only it, is cut
		}
		return j, nil
	}
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: journal, IsLive: dd.sessionIsLive,
	}))

	token := testDeliveryToken('6')
	writeSpoolLine(t, root, "client-00003.ndjson", observeRequest(token, "sess-cut", `{"hook_event_name":"PostToolUse"}`))

	_, err := dd.Drain(context.Background())
	require.Error(t, err, "an uncommitted frontier is not a completed drain")
	require.Equal(t, 1, calls(), "the reference write ran")

	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, "client-00003.ndjson")))
	require.NoError(t, statErr, "the record that would let this be retried must not be released")
	st, err := dd.drain.Load().loadState()
	require.NoError(t, err)
	require.Zero(t, st["client-00003.ndjson"].Offset, "the offset advances only past a committed frontier")

	gaps := dd.DrainGaps()
	require.False(t, gaps.Complete)
	require.Contains(t, gapKinds(gaps), DrainGapUnacknowledged)

	// Restart: the same nonce takes back the same identity, and this time the frontier commits.
	cut = false
	dd.ing.seen = newSeenSet(seenCapacity)
	dd.drain.Load().cfg.Seen = dd.ing.seen
	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	real, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, real.acknowledged(token))
	require.Len(t, sidecarFiles(t, root), 1,
		"a redelivery reuses its identity rather than minting a second observation")
}

func gapKinds(st DrainGapState) []DrainGapKind {
	out := make([]DrainGapKind, 0, len(st.Gaps))
	for _, g := range st.Gaps {
		out = append(out, g.Kind)
	}
	return out
}

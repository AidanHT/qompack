package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestDrainDoesNotRedeliverAnAcknowledgedClientCopy pins the acknowledged frontier's contract at
// the drain: a delivery the frontier already names "advances a spool offset without republishing
// anything" (delivery_lease.go, acknowledge), whichever copy of it the drain reads.
//
// The shape is the one V5-VERIFY's x09 run produced (F4-P1/P2/P3). The live path leases,
// publishes and acknowledges a delivery. The hook client, whose one-byte transport ACK was lost
// AFTER that acknowledgement (ipc.awaitACK -> spoolAndReturn, at the shipped AckDeadlineMs),
// appends the same request to its client spool (spool/client-*.ndjson). The next daemon starts with
// an empty seenSet and drains that copy. Before the fix, drain.go consulted the frontier only inside the
// seenSet's completed branch, so the copy was dispatched through fresh handlers: the observer ran
// a second time for a delivery the frontier held, and the effects the redelivery-tolerant handlers
// do not absorb (a SubagentStop capture under a fresh turn's id, supersede marks against records
// newer than the replayed content) landed in the index.
//
// It pins the frontier check in drain.go's read loop: a leased line the frontier already names
// keeps its blob pending and advances the offset without reaching dispatchPending, whether or not
// the seen set (process memory) remembers it. It asserts at the drain, where the contract is
// written; a fix placed in the handlers instead (ingest.go dispatch: "handlers must tolerate
// at-least-once delivery") would make the call count the wrong witness and move the assertion to
// the observer's outputs.
func TestDrainDoesNotRedeliverAnAcknowledgedClientCopy(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	token := testDeliveryToken('7')
	req := observeRequest(token, "sess-acked-copy", `{"hook_event_name":"PostToolUse"}`)
	require.True(t, dd.dispatchOp(context.Background(), req).OK)
	drainRing(t, dd)

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(token), "fixture: the live path committed the frontier")
	require.Equal(t, 1, calls(), "fixture: the live path published once")
	require.Len(t, sidecarFiles(t, root), 1)

	// The process ends with its WAL copy already drained (the state x09 observed before its
	// flush: every delivery leased and acknowledged, the WAL consumed), leaving the client's
	// fallback copy of the acknowledged delivery as the only spool content. Then a restart: the
	// seen set is process memory, the frontier is not.
	require.NoError(t, dd.ing.Close())
	wals, err := filepath.Glob(paths.Long(filepath.Join(paths.Of(root).Spool, "wal-*.ndjson")))
	require.NoError(t, err)
	require.NotEmpty(t, wals, "fixture: the live path wrote its WAL copy")
	for _, wal := range wals {
		require.NoError(t, os.Remove(wal))
	}
	const copyName = "client-00007.ndjson"
	writeSpoolLine(t, root, copyName, req)
	dd.ing.seen = newSeenSet(seenCapacity)
	dd.drain.Load().cfg.Seen = dd.ing.seen

	_, err = dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, calls(),
		"a spool copy of an acknowledged delivery must advance the offset without republishing: "+
			"the restarted drain dispatched it again (delivery_lease.go acknowledge; F4-P1)")
	require.Len(t, sidecarFiles(t, root), 1, "the identity is reused, not re-minted")
	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(root).Spool, copyName)),
		"the consumed copy is released once its offset is past the frontier")
	require.True(t, dd.DrainGaps().Complete, "skipping an acknowledged copy is not a gap")
}

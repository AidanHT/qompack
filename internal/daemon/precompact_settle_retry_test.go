package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes is the w16d-sealrow regression,
// the daemon half of internal/cli TestPreCompactInSpoolSubmodeSealsTheSpooledReads's local failure
// under -covermode=atomic. A hook's ACK deadline lapsed on the session's first Read, so its hook
// spooled a copy while the daemon was still taking the live line, and the session's next Read went
// to the same client spool behind it. The daemon leases the live Read only after the PreCompact's
// settle has looked at the session's leases (here: during the settle's first look at the spool), and
// it is still publishing it when the settle replays the spool. The replay must leave the copy to the
// live publication and the next Read behind it (the ordering gate), so it publishes nothing; the
// settle must then wait for the live publication and replay again, inside its bound, and the seal
// must see both Reads published and name nothing. Before the fix the settle replayed once and sealed
// at once, naming both Reads as unreplayed with nearly all of its bound unused.
func TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dr := newDrainer(dd.drainConfig())
	dd.drain.Store(dr)
	const sess core.SessionID = "sess-precompact-live-copy-ahead"
	first := liveOrderTool(dd, root, sess, 1)
	next := liveOrderTool(dd, root, sess, 2)
	run, open := settleGate(dd, first.Nonce)
	liveOrderWorkers(t, dd, 2, run)
	t.Cleanup(open)

	writeHookSpool(t, root, "client-7070.ndjson", first, next) // the late ACK's copy, then the next Read
	probe := bindSealProbe(dd, first.Nonce, next.Nonce)

	// The live line is accepted once the settle has taken its look at the leases: inside its first
	// read of the spool. The lane then publishes it, held by the gate.
	var once sync.Once
	var acceptErr error
	dd.spoolHeads.read = func(_ context.Context, path string) ([]byte, error) {
		once.Do(func() {
			line, err := ipc.EncodeRequest(first)
			if err == nil {
				err = dd.ing.Accept(first, line)
			}
			acceptErr = err
		})
		return paths.ReadFileShared(path)
	}

	// The live publication finishes only once the settle's replay has deferred the next Read behind
	// it and ended its pass (the pass holds the drainer's mutex throughout).
	go func() {
		liveOrderPollUntil(liveOrderBound, func() bool {
			if dd.m.Counter(counterDrainOrderingDeferred).Value() == 0 || !dr.mu.TryLock() {
				return false
			}
			dr.mu.Unlock()
			return true
		})
		open()
	}()

	pre := checkpointRequest(dd, sess, "nonce-precompact-live-copy-ahead")
	pre.TS = next.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.NoError(t, acceptErr, "fixture sanity: the live Read was accepted during the settle's first look")
	require.Positive(t, dd.m.Counter(counterDrainOrderingDeferred).Value(),
		"fixture sanity: the settle's replay met the next Read behind the live one")
	require.Equal(t, 1, probe.calls)
	require.True(t, probe.published[first.Nonce], "the seal waited for the live Read's publication")
	require.True(t, probe.published[next.Nonce],
		"the settle replayed the spool again once the live Read ahead of the next one had published")
	require.Empty(t, probe.drops, "nothing was left unreplayed")
}

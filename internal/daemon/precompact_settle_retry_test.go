package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
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

// TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty is the w16d-sealrow review's
// second finding: a client spool the settle's looks cannot read, for any reason but its being gone
// (a sharing violation, an anti-virus lock, an I/O error), may hold the session's captures. The seal
// must say it could not read the file, as it does for a file the bound left unread, and must not
// take it as holding nothing of the session's: that sealed without the session's Read and without a
// word in the drop report. A file gone since the listing (a drain released it) still has nothing to
// name.
func TestPreCompactSettle_ASpoolItCannotReadIsCountedNotTakenAsEmpty(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-unreadable"
	own := liveOrderTool(dd, root, sess, 1)
	const base = "client-7171.ndjson"
	writeHookSpool(t, root, base, own)
	dd.spoolHeads.read = func(_ context.Context, path string) ([]byte, error) {
		if filepath.Base(path) == base {
			return nil, &os.PathError{Op: "open", Path: path, Err: errInjectedSpoolLock}
		}
		return paths.ReadFileShared(path)
	}
	probe := bindSealProbe(dd, own.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-unreadable")
	pre.TS = own.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.False(t, probe.published[own.Nonce], "fixture sanity: nothing could replay the unreadable spool")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, 0, 0, 0, 0, 0) +
			fmt.Sprintf(unreadSpoolsClauseFormat, 1),
	}}, probe.drops, "the seal says a spool went unread rather than silently counting nothing")
	require.Contains(t, probe.drops[0].Detail, "or failed to read",
		"the clause says the file failed to read, not only that the bound ended first (wave 16f)")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactSettle).Value(),
		"a settle that could not tell counts as one")

	// Control: a spool gone since the listing has nothing to name, and is not counted.
	listed := listClientSpools(root)
	require.Len(t, listed, 1)
	require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(root).Spool, base))))
	dd.spoolHeads.read = nil
	lines, ok, read := dd.spoolHeads.heads(context.Background(), root, listed[0])
	require.True(t, read)
	require.True(t, ok, "a spool removed since the listing is not unread: it has nothing to name")
	require.Empty(t, lines)
}

// errInjectedSpoolLock is the read error the unreadable-spool row injects: neither the file's absence
// nor a context's end.
var errInjectedSpoolLock = errors.New("injected: the client spool is locked by another process")

package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// D53(c): the PreCompact route seals only after the session's client-spooled captures are replayed,
// bounded inside B-E, and names what the bound left (precompact_settle.go). The end-to-end row, with
// the shipped hook clients in spool submode and the real checkpoint and rehydration, is internal/cli
// TestPreCompactInSpoolSubmodeSealsTheSpooledReads; these rows pin the route's own behaviour.

// sealProbe is a PreCompact seam that records, at the moment the route calls it, the drop entries the
// settle handed over and whether each watched delivery was already published.
type sealProbe struct {
	calls     int
	drops     []checkpoint.DropEntry
	published map[string]bool
}

func bindSealProbe(dd *daemon, watch ...string) *sealProbe {
	p := &sealProbe{published: map[string]bool{}}
	dd.svc.PreCompact = func(ctx context.Context, _ hookio.Event) (hookio.Output, error) {
		p.calls++
		p.drops = sealDrops(ctx)
		for _, nonce := range watch {
			p.published[nonce] = spoolWatchPublished(dd, nonce)
		}
		return hookio.Empty(), nil
	}
	return p
}

// TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal: the daemon has moved to spool submode (its own
// transition, so state.bin says spool and every hook spools), the session's newest Read sits only in
// a client spool, and nothing else drains: no watcher runs here. The seal must see it published.
func TestPreCompactSettle_ReplaysTheSpoolBeforeTheSeal(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-spooled"

	require.True(t, dd.dispatchOp(context.Background(), spD3Prompt(dd, root, sess, orderNonce(0), "p0")).OK)
	dd.applyHotPathTransition(ToSpool)
	require.Equal(t, ipc.HotSpool, dd.registry.HotMode(), "fixture sanity: the daemon is in spool submode")

	spooled := liveOrderTool(dd, root, sess, 1)
	writeHookSpool(t, root, "client-6161.ndjson", spooled)
	probe := bindSealProbe(dd, spooled.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-spooled")
	pre.TS = spooled.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.True(t, probe.published[spooled.Nonce],
		"the PreCompact route must replay the session's client spool before it seals")
	require.Empty(t, probe.drops, "nothing was left unreplayed")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactSettle).Value())
	require.Zero(t, dd.m.Counter(counterPrecompactUnreplayed).Value())
}

// TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed: a spooled Read whose publication cannot finish
// inside the settle's bound (the slow disk, made deterministic: its replay waits until its context
// ends) does not hold the seal past the bound, and the seal names it. A Read the session made after
// the PreCompact fired is not this compaction's, and is not named.
func TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-precompact-bound"
	slow := liveOrderTool(dd, root, sess, 1)
	later := liveOrderTool(dd, root, sess, 2)
	cfg := dd.drainConfig()
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Nonce == slow.Nonce {
			<-ctx.Done() // the disk that never finishes inside the bound
			return ipc.Response{Err: ctx.Err().Error()}
		}
		return dd.drainDispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.applyHotPathTransition(ToSpool)

	writeHookSpool(t, root, "client-6262.ndjson", slow)
	later.TS = slow.TS + 10
	writeHookSpool(t, root, "client-6363.ndjson", later)
	probe := bindSealProbe(dd, slow.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-bound")
	pre.TS = slow.TS + 5 // fired between the two Reads
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls, "the route seals once the bound has expired")
	require.False(t, probe.published[slow.Nonce], "fixture sanity: the slow Read was not replayed")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: checkpoint.DropKindUnreplayedCapture, ID: string(slow.Event.ToolUseID), Detail: unreplayedDetail,
	}}, probe.drops, "the seal names the capture the bound left unreplayed, and only that one")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactUnreplayed).Value())
}

// TestPreCompactSettle_AHealthySessionPaysNothing: a session with nothing spooled and every leased
// arrival published seals at once: no wait, no drain pass, no drop entry. settleFast is the whole
// cost, one spool listing and two in-memory lookups, which the row logs.
func TestPreCompactSettle_AHealthySessionPaysNothing(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-healthy"

	tool := liveOrderTool(dd, root, sess, 1)
	require.True(t, dd.dispatchOp(context.Background(), tool).OK)
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, tool.Nonce) },
		liveOrderBound, liveOrderTick, "fixture sanity: the live Read publishes")
	probe := bindSealProbe(dd)

	// Hold the drain's mutex for the whole PreCompact: a settle that tried to drain would wait out
	// its whole bound behind it and count itself.
	dr := dd.drain.Load()
	dr.mu.Lock()
	defer dr.mu.Unlock()
	require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, sess, "nonce-healthy")).OK)

	require.Equal(t, 1, probe.calls)
	require.Empty(t, probe.drops)
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value(),
		"a healthy session's PreCompact found nothing to settle and did not wait")

	// What that costs, for the record (no wall-clock assertion: the machine may be loaded).
	const rounds = 1000
	start := time.Now()
	for range rounds {
		require.Nil(t, dd.settleBeforeSeal(context.Background(), sess, 0))
	}
	t.Logf("healthy settle: %s per PreCompact over %d rounds", time.Since(start)/rounds, rounds)
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value())
}

// TestPreCompactSettle_AReplayedPreCompactDoesNotSettle: a PreCompact a drain replays reaches the route
// while that drain holds the drain's mutex; the route must not try to drain again (it would wait out
// its bound behind its own caller), and the compaction it announced is already over.
func TestPreCompactSettle_AReplayedPreCompactDoesNotSettle(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	const sess core.SessionID = "sess-precompact-replayed"
	writeHookSpool(t, root, "client-6464.ndjson", liveOrderTool(dd, root, sess, 1))
	probe := bindSealProbe(dd)

	require.True(t, dd.drainDispatch(context.Background(), checkpointRequest(dd, sess, "nonce-replayed")).OK)
	require.Equal(t, 1, probe.calls)
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value())
}

// TestPrecompactSettleBound_IsWhatBELeavesTheSeal pins the bound's derivation: B-E less the seal's
// own window, and none at all when B-E leaves the seal nothing more.
func TestPrecompactSettleBound_IsWhatBELeavesTheSeal(t *testing.T) {
	cfg := testConfig()
	require.Equal(t, time.Duration(cfg.Runtime.Budgets.CheckpointFinalizeMs)*time.Millisecond-checkpoint.MaxPreCompactWindow,
		precompactSettleBound(cfg))
	require.Equal(t, 500*time.Millisecond, precompactSettleBound(cfg), "2000 ms B-E less the seal's 1500 ms")
	cfg.Runtime.Budgets.CheckpointFinalizeMs = int(checkpoint.MaxPreCompactWindow / time.Millisecond)
	require.Zero(t, precompactSettleBound(cfg))
	cfg.Runtime.Budgets.CheckpointFinalizeMs = 1
	require.Zero(t, precompactSettleBound(cfg))
}

// TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex: the settle's pass waits for a pass holding
// the drain's mutex no longer than its own context allows, and reads nothing when it gives up.
func TestDrainer_DrainClientSpoolsWithinGivesUpOnABusyMutex(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(dd.drainConfig())
	writeHookSpool(t, root, "client-6565.ndjson", liveOrderTool(dd, root, "sess-busy", 1))
	dr.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := dr.DrainClientSpoolsWithin(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, n)
	dr.mu.Unlock()
	require.False(t, spoolWatchGone(root, "client-6565.ndjson"), "nothing was consumed")
	// The abandoned Lock completes and releases once the holder is done: the mutex is usable again.
	require.Eventually(t, func() bool {
		if dr.mu.TryLock() {
			dr.mu.Unlock()
			return true
		}
		return false
	}, liveOrderBound, liveOrderTick)
}

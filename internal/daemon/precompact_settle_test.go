package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/tokens"
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
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(slow.Event.ToolUseID)},
	}, probe.drops, "the seal names the capture the bound left unreplayed, and only that one")
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
	n, err := dr.DrainClientSpoolsWithin(ctx, nil)
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

// TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult: the drop report for what the bound
// left is one counted summary and one line per tool result, by tool_use_id, newest first while the
// names fit their allowance; a prompt and a Stop are counted, having no id the model could ask for.
func TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult(t *testing.T) {
	tool := func(id string, ts core.UnixMilli) pendingCapture {
		return pendingCapture{op: ipc.OpObserveTool, toolUseID: core.ToolUseID(id), ts: ts}
	}
	left := []pendingCapture{
		tool("toolu_a", 1),
		{op: ipc.OpObservePrompt, ts: 2},
		tool("toolu_b", 3),
		{op: ipc.OpObserveStop, ts: 4},
	}
	est := tokens.New(testConfig(), "")
	got := unreplayedDrops(left, unreplayedNamesAllowance(testConfig()), est)
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 4, 2, 1, 1, 2)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: "toolu_b"},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: "toolu_a"},
	}, got)
	require.Contains(t, got[0].Detail, "4 capture(s) of this session (2 tool result(s), 1 prompt(s), 1 other)")
	require.Contains(t, got[0].Detail, "the newest 2 tool result(s) are named")
	require.Contains(t, got[0].Detail, "nothing is lost")

	// No allowance names none, and the summary still counts them all.
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 4, 2, 1, 1, 0)},
	}, unreplayedDrops(left, 0, est))
}

// TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain: a client spool another
// session left is not this session's to settle. A healthy session's PreCompact must neither count a
// settle nor wait for the drain (held here for the whole PreCompact), and the other spool stays.
func TestPreCompactSettle_AnotherSessionsSpoolCostsAHealthySessionNoDrain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-healthy-beside"
	writeHookSpool(t, root, "client-7070.ndjson", liveOrderTool(dd, root, "sess-precompact-other", 1))

	tool := liveOrderTool(dd, root, sess, 2)
	require.True(t, dd.dispatchOp(context.Background(), tool).OK)
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, tool.Nonce) },
		liveOrderBound, liveOrderTick, "fixture sanity: the live Read publishes")
	probe := bindSealProbe(dd)

	dr := dd.drain.Load()
	dr.mu.Lock()
	defer dr.mu.Unlock()
	pre := checkpointRequest(dd, sess, "nonce-healthy-beside")
	pre.TS = tool.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.Empty(t, probe.drops)
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value(),
		"another session's spool is no reason for this session's PreCompact to settle")
	require.False(t, spoolWatchGone(root, "client-7070.ndjson"), "the other session's spool is left to the drains")

	// What the scan of the other session's spool costs, for the record (no wall-clock assertion).
	const rounds = 200
	start := time.Now()
	for range rounds {
		require.Nil(t, dd.settleBeforeSeal(context.Background(), sess, pre.TS))
	}
	t.Logf("healthy settle beside another session's spool: %s per PreCompact over %d rounds",
		time.Since(start)/rounds, rounds)
}

// TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers: another session's older spool
// whose replay cannot finish (the slow disk) must not spend this session's bound: the settle replays
// the compacting session's own spools, and its Read is sealed.
func TestPreCompactSettle_ReplaysThisSessionsSpoolBeforeOlderOnesOfOthers(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess, other core.SessionID = "sess-precompact-own", "sess-precompact-older"
	cfg := dd.drainConfig()
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if resolveEvent(req).SessionID == other {
			<-ctx.Done()
			return ipc.Response{Err: ctx.Err().Error()}
		}
		return dd.drainDispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.applyHotPathTransition(ToSpool)

	older := liveOrderTool(dd, root, other, 1)
	own := liveOrderTool(dd, root, sess, 2)
	own.TS = older.TS + 10
	writeHookSpool(t, root, "client-7171.ndjson", older)
	writeHookSpool(t, root, "client-7272.ndjson", own)
	probe := bindSealProbe(dd, own.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-own")
	pre.TS = own.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.True(t, probe.published[own.Nonce], "the compacting session's own spooled Read is replayed first")
	require.Empty(t, probe.drops)
}

// TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare: a few hundred spooled Reads, one
// hook process each as in spool submode, on a disk too slow to replay any of them inside the bound.
// The summary counts every one; only the newest are named, within their share of
// checkpoint.budgetTokens, so a checkpoint whose pointers fit the rest of its budget keeps them all
// (Truncate measures the drop report with the document). Named without that share, the same report
// costs the checkpoint its pointers.
func TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sess core.SessionID = "sess-precompact-backlog"
	const backlog = 300
	cfg := dd.drainConfig()
	cfg.Dispatch = func(ctx context.Context, _ ipc.Request) ipc.Response {
		<-ctx.Done() // the disk that never finishes a line inside the bound
		return ipc.Response{Err: ctx.Err().Error()}
	}
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.applyHotPathTransition(ToSpool)

	reqs := make([]ipc.Request, backlog)
	for i := range reqs {
		reqs[i] = liveOrderTool(dd, root, sess, i+1)
		writeHookSpool(t, root, fmt.Sprintf("client-%d.ndjson", 80000+i), reqs[i])
	}
	probe := bindSealProbe(dd)
	pre := checkpointRequest(dd, sess, "nonce-precompact-backlog")
	pre.TS = reqs[backlog-1].TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.NotEmpty(t, probe.drops)
	named := probe.drops[1:]
	k := len(named)
	require.Equal(t, checkpoint.DropEntry{
		Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, backlog, backlog, 0, 0, k),
	}, probe.drops[0], "the summary counts the whole backlog exactly")
	require.Positive(t, k, "the newest tool results are named")
	require.Less(t, k, backlog, "fixture sanity: the backlog is larger than the share can name")
	for i, e := range named {
		require.Equal(t, checkpoint.DropKindUnreplayedToolResult, e.Kind)
		require.Equal(t, string(reqs[backlog-1-i].Event.ToolUseID), e.ID, "named newest first")
	}
	require.Equal(t, int64(backlog), dd.m.Counter(counterPrecompactUnreplayed).Value())

	tcfg := dd.currentCfg()
	est := tokens.New(tcfg, "")
	budget := core.Tokens(tcfg.Checkpoint.BudgetTokens)
	share := unreplayedNamesAllowance(tcfg)
	t.Logf("named the newest %d of %d spooled tool results within %d of %d tokens", k, backlog, share, budget)
	require.LessOrEqual(t, checkpointCost(est, checkpoint.Checkpoint{Dropped: probe.drops})-
		checkpointCost(est, checkpoint.Checkpoint{Dropped: probe.drops[:1]}), share,
		"the names stay inside their share of checkpoint.budgetTokens")

	// A checkpoint whose pointers fit its budget less that share, with the summary, keeps every pointer.
	cp := checkpoint.Checkpoint{Dropped: probe.drops[:1]}
	for i := 0; checkpointCost(est, cp) < budget-share-unreplayedPointerCost(est); i++ {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_kept_%04d", i)), Summary: "Read src/kept.go",
		})
	}
	require.NotEmpty(t, cp.Pointers.Tools)
	cp.Dropped = probe.drops
	_, cut := checkpoint.Truncate(cp, budget, tcfg.Checkpoint.Tiers, est)
	require.Empty(t, cut, "the bounded report costs the checkpoint none of its pointers")

	unbounded := unreplayedDrops(capturesOf(reqs), budget*budget, est)
	require.Len(t, unbounded, backlog+1, "fixture sanity: every tool result named")
	cp.Dropped = unbounded
	_, cut = checkpoint.Truncate(cp, budget, tcfg.Checkpoint.Tiers, est)
	require.NotEmpty(t, cut, "fixture sanity: naming the whole backlog would have cut the pointers")
}

// checkpointCost measures c as Truncate does: its canonical bytes, priced as JSON.
func checkpointCost(est tokens.Estimator, c checkpoint.Checkpoint) core.Tokens {
	b, err := checkpoint.Marshal(c)
	if err != nil {
		panic(err)
	}
	return est.Estimate(b, tokens.ClassJSON)
}

// unreplayedPointerCost is one fixture tool pointer's cost, so the fill above stops below its target.
func unreplayedPointerCost(est tokens.Estimator) core.Tokens {
	one := checkpoint.Checkpoint{Pointers: checkpoint.Pointers{Tools: []checkpoint.ToolPointer{{
		ToolUseID: "toolu_kept_0000", Summary: "Read src/kept.go",
	}}}}
	return checkpointCost(est, one) - checkpointCost(est, checkpoint.Checkpoint{})
}

// capturesOf is what the settle knows of reqs.
func capturesOf(reqs []ipc.Request) []pendingCapture {
	out := make([]pendingCapture, len(reqs))
	for i, req := range reqs {
		out[i] = captureOf(req)
	}
	return out
}

// TestSpoolLineHead_ReadsWhatDecodeRequestReads: the settle's scan decodes only a spooled request's
// head. It must read the same op, session, time, nonce and tool_use_id that the full decode and
// resolveEvent give, for an event that names its session and for one that leaves it to the request.
func TestSpoolLineHead_ReadsWhatDecodeRequestReads(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	tool := liveOrderTool(dd, root, "sess-head", 3)
	prompt := ipc.Request{
		Op: ipc.OpObservePrompt, Session: "sess-head-req", TS: 42, Nonce: orderNonce(9),
		Event: &hookio.Event{HookEventName: "UserPromptSubmit", Prompt: "hello"},
	}
	stop := ipc.Request{Op: ipc.OpObserveStop, Session: "sess-head-stop", TS: 43}
	for _, req := range []ipc.Request{tool, prompt, stop} {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		full, err := ipc.DecodeRequest(line)
		require.NoError(t, err)
		head, err := decodeSpoolLineHead(line)
		require.NoError(t, err)
		require.Equal(t, resolveEvent(full).SessionID, head.session())
		require.Equal(t, captureOf(full), head.capture())
	}
}

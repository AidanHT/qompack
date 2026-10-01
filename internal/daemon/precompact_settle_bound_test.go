package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// V6 close-out D55, wave 16b: every look the PreCompact settle takes at the client spools counts
// against its bound, a look reuses what an earlier one read (the spool index, spool_heads.go), and a
// healthy session beside other sessions' backlog pays the listing once that backlog is indexed. The
// names in the drop report are priced by the seal's own, calibrated estimator. These rows count
// files read (counterPrecompactSpoolReads), never wall time.

// settleSpoolReads is how many client spool files the PreCompact settles have read so far.
func settleSpoolReads(dd *daemon) int64 { return dd.m.Counter(counterPrecompactSpoolReads).Value() }

// TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists: three hundred client
// spools of other sessions sit beside a healthy session (its Read published, nothing of it spooled).
// Its first PreCompact reads each of them once, inside the bound, to learn that none is its own; every
// PreCompact after it takes their heads from the index and reads no file at all: the listing is the
// whole cost. Neither settles, waits or drains (the drain's mutex is held throughout).
func TestPreCompactSettle_AHealthySessionReadsOthersBacklogOnceThenOnlyLists(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-healthy-backlog"
	const backlog = 300
	for i := range backlog {
		other := core.SessionID(fmt.Sprintf("sess-precompact-other-%03d", i))
		writeHookSpool(t, root, fmt.Sprintf("client-%d.ndjson", 90000+i), liveOrderTool(dd, root, other, 100+i))
	}
	tool := liveOrderTool(dd, root, sess, 1)
	require.True(t, dd.dispatchOp(context.Background(), tool).OK)
	require.Eventually(t, func() bool { return spoolWatchPublished(dd, tool.Nonce) },
		liveOrderBound, liveOrderTick, "fixture sanity: the live Read publishes")
	probe := bindSealProbe(dd)

	dr := dd.drain.Load()
	dr.mu.Lock()
	defer dr.mu.Unlock()
	compact := func(nonce string) time.Duration {
		pre := checkpointRequest(dd, sess, nonce)
		pre.TS = tool.TS + 1
		start := time.Now()
		require.True(t, dd.dispatchOp(context.Background(), pre).OK)
		return time.Since(start)
	}

	cold := compact("nonce-healthy-backlog-cold")
	require.Equal(t, int64(backlog), settleSpoolReads(dd),
		"the first PreCompact beside the backlog reads each spool once, to learn none holds this session's captures")
	warm := compact("nonce-healthy-backlog-warm")
	require.Equal(t, int64(backlog), settleSpoolReads(dd),
		"a later PreCompact reads no spool file: the index holds them all at their listed size and time")
	require.Equal(t, 2, probe.calls)
	require.Empty(t, probe.drops)
	require.Zero(t, dd.m.Counter(counterPrecompactSettle).Value(),
		"another session's backlog is no reason for this session's PreCompact to settle")

	const rounds = 200
	start := time.Now()
	for range rounds {
		require.Nil(t, dd.settleBeforeSeal(context.Background(), sess, tool.TS+1))
	}
	require.Equal(t, int64(backlog), settleSpoolReads(dd), "no settle after the first read a file")
	// For the record only (no wall-clock assertion: the machine may be loaded).
	t.Logf("healthy PreCompact beside %d other sessions' spools: route %s cold, %s warm; settle %s per "+
		"PreCompact over %d warm rounds", backlog, cold, warm, time.Since(start)/rounds, rounds)
}

// TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo: with B-E no larger than the seal's own
// window the settle has no bound at all, and its looks read no spool file: the session's spooled Read
// is in a file the index does not hold, so the seal counts the file as unread instead of naming what
// it cannot see. Once the index holds the file, the same zero bound names the Read from memory.
func TestPreCompactSettle_ALookPastTheBoundReadsNothingAndSaysSo(t *testing.T) {
	dd, root := settleTestDaemon(t, 0)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-no-bound"
	own := liveOrderTool(dd, root, sess, 1)
	writeHookSpool(t, root, "client-7373.ndjson", own)
	probe := bindSealProbe(dd, own.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-no-bound")
	pre.TS = own.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)
	require.Zero(t, settleSpoolReads(dd), "no look reads a spool file once the bound has passed")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, 0, 0, 0, 0, 0) +
			fmt.Sprintf(unreadSpoolsClauseFormat, 1),
	}}, probe.drops, "the seal says a spool went unread rather than silently counting nothing")
	require.False(t, probe.published[own.Nonce], "fixture sanity: nothing was replayed")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactSettle).Value(),
		"a settle that could not tell counts as one")

	// An earlier look with time to read it (the watcher's, or a PreCompact with a bound) indexes it.
	listed := listClientSpools(root)
	require.Len(t, listed, 1)
	_, ok := dd.spoolHeads.heads(context.Background(), root, listed[0])
	require.True(t, ok)
	pre = checkpointRequest(dd, sess, "nonce-precompact-no-bound-indexed")
	pre.TS = own.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)
	require.Zero(t, settleSpoolReads(dd))
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(own.Event.ToolUseID)},
	}, probe.drops, "with the file indexed, the seal names the Read without reading anything")
}

// TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools: the settle's last look keeps the first
// look's result. It reads again only the spools the first look named and the ones listed since; a
// spool the first look found nothing of this session's in is not read again, however it changed.
// While the session's own spool is replayed, a new spool appears holding another of its Reads, fired
// before the PreCompact, and another session's spool grows. The settle reads three files in all: the
// two of the first look and the new one, whose Read the seal names (the replay did not include it).
func TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	const sess, other core.SessionID = "sess-precompact-last-look", "sess-precompact-last-look-other"
	own := liveOrderTool(dd, root, sess, 1)
	theirs := liveOrderTool(dd, root, other, 2)
	late := liveOrderTool(dd, root, sess, 3)
	late.TS = own.TS + 1
	theirsToo := liveOrderTool(dd, root, other, 4)
	cfg := dd.drainConfig()
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Nonce == own.Nonce {
			// While the first look's own spool is being replayed: a spool listed after that look, and
			// another session's spool that look already read grows.
			writeHookSpool(t, root, "client-7676.ndjson", late)
			appendHookSpool(t, root, "client-7575.ndjson", theirsToo)
		}
		return dd.drainDispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	writeHookSpool(t, root, "client-7474.ndjson", own)
	writeHookSpool(t, root, "client-7575.ndjson", theirs)
	probe := bindSealProbe(dd, own.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-last-look")
	pre.TS = late.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.True(t, probe.published[own.Nonce], "the first look's own spool was replayed")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(late.Event.ToolUseID)},
	}, probe.drops, "the last look found the session's Read in the spool listed after the first look")
	require.Equal(t, int64(3), settleSpoolReads(dd),
		"two files in the first look, then only the new one: the named one is replayed and released, "+
			"and the other session's grown spool is not read again")
}

// appendHookSpool appends reqs to the client spool base as a later hook with a reused pid does.
func appendHookSpool(t *testing.T, root, base string, reqs ...ipc.Request) {
	t.Helper()
	f, err := os.OpenFile(paths.Long(filepath.Join(paths.Of(root).Spool, base)), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	for _, req := range reqs {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		_, err = f.Write(line)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
}

// TestPreCompactSettle_NamesFitTheirShareUnderTheSealsCalibratedEstimator: the seal prices the
// unreplayed report's names with the estimator its Truncate measures the document with, the
// project-calibrated one, not the identity. On a project calibrated above 1 (here the configured
// maximum), the names the real seam seals cost at most their share of checkpoint.budgetTokens as
// that estimator measures them. Priced with the identity, as before, the same report names more and
// overruns the share.
func TestPreCompactSettle_NamesFitTheirShareUnderTheSealsCalibratedEstimator(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)
	est := f.src.Tokens
	// Observed counts at the configured maximum ratio, more times than the estimator's warm-up needs.
	const observations, estimated = 20, 1000
	for range observations {
		est.Calibrate(core.Tokens(f.cfg.Runtime.Tokens.CalibrationMax*estimated), estimated)
	}
	require.InDelta(t, f.cfg.Runtime.Tokens.CalibrationMax, est.Factor(), 1e-9,
		"fixture sanity: the project is calibrated at the configured maximum, above the identity")
	require.Greater(t, est.Factor(), 1.0)

	o := &Options{ProjectRoot: f.root, Cfg: f.cfg, Log: logging.Nop(), Clock: f.clk}
	BindCheckpoint(o, f.cfg, f.w, f.src)
	var s Services
	for _, bind := range o.binds {
		bind(&s)
	}
	const backlog = 300
	left := make([]pendingCapture, backlog)
	for i := range left {
		left[i] = pendingCapture{
			op: ipc.OpObserveTool, toolUseID: core.ToolUseID(fmt.Sprintf("toolu_calibrated_%04d", i)),
			ts: core.UnixMilli(i + 1),
		}
	}
	report := &sealReport{left: left, allowance: unreplayedNamesAllowance(f.cfg)}
	_, err := s.PreCompact(withSealReport(f.ctx(), report), hookio.Event{
		HookEventName: "PreCompact", SessionID: cpSession, Trigger: "auto", CWD: f.root,
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(f.l, core.CheckpointSeq(1))))
	require.NoError(t, err)
	sealed, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	var got []checkpoint.DropEntry
	for _, e := range sealed.Dropped {
		if e.Kind == checkpoint.DropKindUnreplayedCapture || e.Kind == checkpoint.DropKindUnreplayedToolResult {
			got = append(got, e)
		}
	}
	require.Equal(t, report.drops(est), got, "the seal priced the names with the draft's own estimator")
	require.Greater(t, len(got), 1, "fixture sanity: some tool results are named")

	share := unreplayedNamesAllowance(f.cfg)
	namesCost := func(drops []checkpoint.DropEntry) core.Tokens {
		return checkpointCost(est, checkpoint.Checkpoint{Dropped: drops}) -
			checkpointCost(est, checkpoint.Checkpoint{Dropped: drops[:1]})
	}
	require.LessOrEqual(t, namesCost(got), share,
		"the sealed names stay inside their share as the seal's calibrated estimator measures them")

	identity := report.drops(tokens.New(f.cfg, ""))
	require.Greater(t, len(identity), len(got), "fixture sanity: the identity names more")
	require.Greater(t, namesCost(identity), share,
		"priced with the identity, the names overrun their share as the seal measures them")
}

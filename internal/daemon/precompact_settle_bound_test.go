package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	_, ok, _ := dd.spoolHeads.heads(context.Background(), root, listed[0])
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

// coldBacklogBound is the settle's bound in TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound:
// long enough that, with the first look held until just past half of it, the replay of the session's
// one spooled Read fits in what is left on a loaded machine (it is one line, and the row fails only
// if that replay takes more than 45 % of it).
const coldBacklogBound = 3 * time.Second

// coldBacklogLeft is how much of coldBacklogBound the row's slow read leaves: just under half, so the
// first look takes more than half of the bound and still leaves time for the replay.
const coldBacklogLeft = coldBacklogBound * 45 / 100

// TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound (wave 16b review): twenty client
// spools of other sessions, none indexed yet, are listed before the session's own spool, and the
// first look's read of the last of them is slow (a read seam holds it until less than half of the
// bound is left). The first look still leaves time, so the session's own Read is replayed before the
// seal and nothing is left over: the waits and the replay run to the bound's own deadline. Held back
// by the first look's whole duration again, as they were, they would have had no time at all, and a
// cold backlog of other sessions' spools would have cost this session its replay. The last look reads
// nothing: the own spool is released by the replay, and the others it does not look at again.
func TestPreCompactSettle_AColdBacklogLeavesTheReplayTheRestOfTheBound(t *testing.T) {
	dd, root := settleTestDaemon(t, coldBacklogBound)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	const sess core.SessionID = "sess-precompact-cold-backlog"
	const backlog = 20
	for i := range backlog {
		other := core.SessionID(fmt.Sprintf("sess-precompact-cold-other-%02d", i))
		writeHookSpool(t, root, fmt.Sprintf("client-%d.ndjson", 10000+i), liveOrderTool(dd, root, other, 100+i))
	}
	own := liveOrderTool(dd, root, sess, 1)
	writeHookSpool(t, root, "client-9999.ndjson", own) // listed after the backlog
	slow := fmt.Sprintf("client-%d.ndjson", 10000+backlog-1)
	var left atomic.Int64 // what the slow read left of the bound
	dd.spoolHeads.read = func(ctx context.Context, path string) ([]byte, error) {
		if dl, ok := ctx.Deadline(); ok && filepath.Base(path) == slow {
			hold := time.NewTimer(time.Until(dl) - coldBacklogLeft)
			select {
			case <-hold.C:
			case <-ctx.Done():
			}
			hold.Stop()
			left.Store(int64(time.Until(dl)))
		}
		return paths.ReadFileShared(path)
	}
	probe := bindSealProbe(dd, own.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-cold-backlog")
	pre.TS = own.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Positive(t, left.Load(), "fixture sanity: the first look ended before the bound")
	require.Less(t, time.Duration(left.Load()), coldBacklogBound/2,
		"fixture sanity: the first look took more than half of the bound")
	require.True(t, probe.published[own.Nonce],
		"the first look left time, so the session's own spool is replayed before the seal")
	require.Empty(t, probe.drops, "nothing of the session was left unreplayed")
	require.Equal(t, int64(backlog+1), settleSpoolReads(dd),
		"the first look reads every file once; the last look reads none")
}

// TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks (wave 16c, the w16b-settle review's
// first nit): precompact_settle_spool_reads counts the client spool files the settle's own looks
// read, not every read the spool index made while the settle ran. Here the client-spool watcher's
// indexClientSpools runs on its own goroutine in the middle of the settle's first look (a read seam
// starts it when the settle reads the one listed spool and waits for it to finish, so the overlap is
// certain and no clock decides it), and reads a spool a hook wrote after the settle's listing. The
// index reads two files in all; the settle read one, and counts one.
func TestPreCompactSettle_CountsOnlyTheSpoolReadsOfItsOwnLooks(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	const other core.SessionID = "sess-precompact-reads-other"
	const listed, written = "client-7272.ndjson", "client-7373.ndjson"
	writeHookSpool(t, root, listed, liveOrderTool(dd, root, other, 1))
	later := liveOrderTool(dd, root, other, 2)
	var once sync.Once
	dd.spoolHeads.read = func(_ context.Context, path string) ([]byte, error) {
		if filepath.Base(path) == listed {
			once.Do(func() {
				writeHookSpool(t, root, written, later)
				done := make(chan struct{})
				go func() {
					defer close(done)
					dd.indexClientSpools(context.Background(), map[string]bool{written: true})
				}()
				<-done
			})
		}
		return paths.ReadFileShared(path)
	}

	require.Nil(t, dd.settleBeforeSeal(context.Background(), "sess-precompact-reads-healthy", 0))
	require.Equal(t, int64(2), dd.spoolHeads.reads.Load(),
		"fixture sanity: the watcher's pass read the new spool while the settle ran")
	require.Equal(t, int64(1), settleSpoolReads(dd), "the settle counts the one file its own look read")
}

// TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated (wave 16c, the w16b-settle
// review's second nit): the spool index keys a file version on its size and modification time, and a
// drain can release client-<pid>.ndjson after which a hook with the same pid writes a new capture
// under the same name, at the same size and, on a filesystem with coarse timestamps, the same time.
// The daemon's own removal drops the index entry, so the settle reads the recreated file again and
// names the capture it holds. Served from the released file's heads, the settle found only a
// published Read and sealed with nothing named: the capture was silently missing from the report.
func TestPreCompactSettle_ReadsASpoolTheDrainReleasedAndAHookRecreated(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const sess core.SessionID = "sess-precompact-recreated"
	const base = "client-7474.ndjson"
	first := liveOrderTool(dd, root, sess, 1)
	second := liveOrderTool(dd, root, sess, 2) // the same length as first: the reused pid's next Read
	cfg := dd.drainConfig()
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Nonce == second.Nonce {
			<-ctx.Done() // the disk that never finishes inside the bound
			return ipc.Response{Err: ctx.Err().Error()}
		}
		return dd.drainDispatch(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.applyHotPathTransition(ToSpool)

	path := paths.Long(filepath.Join(paths.Of(root).Spool, base))
	writeHookSpool(t, root, base, first)
	was, err := os.Stat(path)
	require.NoError(t, err)
	dd.indexClientSpools(ctx, map[string]bool{base: true})
	require.True(t, spoolIndexed(dd, base), "fixture sanity: the watcher indexed the spool")

	_, err = dd.Drain(ctx)
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, first.Nonce), "fixture sanity: the drain published the Read")
	require.True(t, spoolWatchGone(root, base), "fixture sanity: the drain released the spool")

	writeHookSpool(t, root, base, second)
	require.NoError(t, os.Chtimes(path, was.ModTime(), was.ModTime()))
	now, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, was.Size(), now.Size(), "fixture sanity: the recreated spool has the same size")
	require.True(t, was.ModTime().Equal(now.ModTime()), "fixture sanity: and the same modification time")

	probe := bindSealProbe(dd, second.Nonce)
	pre := checkpointRequest(dd, sess, "nonce-precompact-recreated")
	pre.TS = second.TS + 1
	require.True(t, dd.dispatchOp(ctx, pre).OK)

	require.Equal(t, int64(1), settleSpoolReads(dd), "the settle reads the recreated spool again")
	require.False(t, probe.published[second.Nonce], "fixture sanity: its replay could not finish in the bound")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(second.Event.ToolUseID)},
	}, probe.drops, "the seal names the recreated spool's Read")
}

// TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered (wave 16c): a look that read a
// client spool while the drain removed it may have read the removed file. The index does not keep
// what it read, so a file recreated under the name at the same size and time cannot be served the
// removed file's heads. The read seam calls removed in the middle of the read, as the drain would.
func TestSpoolHeadIndex_AReadTheDrainsRemovalOverlapsIsNotRemembered(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const base = "client-7575.ndjson"
	writeHookSpool(t, root, base, liveOrderTool(dd, root, "sess-spool-index-removal", 1))
	listed := listClientSpools(root)
	require.Len(t, listed, 1)
	dd.spoolHeads.read = func(_ context.Context, path string) ([]byte, error) {
		b, err := paths.ReadFileShared(path)
		dd.spoolHeads.removing(filepath.Base(path))()
		return b, err
	}

	lines, ok, read := dd.spoolHeads.heads(context.Background(), root, listed[0])
	require.True(t, ok)
	require.True(t, read)
	require.Len(t, lines, 1, "the look still gets what it read")
	require.False(t, spoolIndexed(dd, base), "but the index does not remember it")

	dd.spoolHeads.read = nil
	_, ok, read = dd.spoolHeads.heads(context.Background(), root, listed[0])
	require.True(t, ok)
	require.True(t, read, "the next look reads the file again")
	require.True(t, spoolIndexed(dd, base), "control: a read no removal overlaps is remembered")
}

// TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain (wave 16c, the settle2
// review): the drain's removal of a released client spool and the index's forgetting of it are one
// step to every look. Here a hook with the reused pid writes the name again, at the same size and
// modification time, in the instant after the drain's unlink returned and before the drain went on
// (the drainer's removeSpool seam does it, and looks, inside the removal), and a look made then
// reads the new file: it is not served the removed one's heads. Neither is the next look after it,
// and a look that read the old file just before the unlink did not put its heads back either.
// Dropping the entry only after the unlink returned left that instant open.
func TestSpoolHeadIndex_ASpoolRecreatedRightAfterTheDrainsUnlinkIsReadAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const sess core.SessionID = "sess-spool-index-unlink"
	const base = "client-7676.ndjson"
	first := liveOrderTool(dd, root, sess, 1)
	second := liveOrderTool(dd, root, sess, 2) // the same length as first: the reused pid's next Read
	path := paths.Long(filepath.Join(paths.Of(root).Spool, base))
	writeHookSpool(t, root, base, first)
	was, err := os.Stat(path)
	require.NoError(t, err)
	dd.indexClientSpools(ctx, map[string]bool{base: true})
	require.True(t, spoolIndexed(dd, base), "fixture sanity: the watcher indexed the spool")

	type look struct {
		lines    []spoolHeadLine
		ok, read bool
	}
	var before, during []look
	dr := newDrainer(dd.drainConfig())
	dr.removeSpool = func(p string, drained int64) (bool, error) {
		if filepath.Base(p) == base { // a look at the released file just before its unlink
			for _, l := range listClientSpools(root) {
				if l.base == base {
					lines, ok, read := dd.spoolHeads.heads(ctx, root, l)
					before = append(before, look{lines, ok, read})
				}
			}
		}
		removed, err := removeIfUnchanged(p, drained)
		if !removed || filepath.Base(p) != base {
			return removed, err
		}
		writeHookSpool(t, root, base, second)
		require.NoError(t, os.Chtimes(path, was.ModTime(), was.ModTime()))
		for _, l := range listClientSpools(root) {
			if l.base == base {
				require.Equal(t, was.Size(), l.size, "fixture sanity: the recreated spool has the same size")
				require.True(t, was.ModTime().Equal(l.mod), "fixture sanity: and the same modification time")
				lines, ok, read := dd.spoolHeads.heads(ctx, root, l)
				during = append(during, look{lines, ok, read})
			}
		}
		return removed, err
	}
	dd.drain.Store(dr)
	liveOrderWorkers(t, dd, 2, dd.runIngested)

	_, err = dd.Drain(ctx)
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, first.Nonce), "fixture sanity: the drain published the first Read")
	require.Len(t, before, 1, "fixture sanity: the seam looked at the released spool before its unlink")
	require.True(t, before[0].read, "the removal dropped the entry before the unlink")
	require.Len(t, before[0].lines, 1)
	require.Equal(t, first.Nonce, before[0].lines[0].c.nonce, "fixture sanity: that look read the released file")
	require.Len(t, during, 1, "fixture sanity: the seam recreated the spool and looked once")
	require.True(t, during[0].ok)
	require.True(t, during[0].read, "the look right after the unlink reads the recreated spool")
	require.Len(t, during[0].lines, 1)
	require.Equal(t, second.Nonce, during[0].lines[0].c.nonce, "and names its capture, not the removed one's")

	l := listClientSpools(root)
	require.Len(t, l, 1, "fixture sanity: the pass left the recreated spool for a later one")
	lines, ok, read := dd.spoolHeads.heads(ctx, root, l[0])
	require.True(t, ok)
	require.True(t, read, "nothing a look read inside the removal was remembered")
	require.Len(t, lines, 1)
	require.Equal(t, second.Nonce, lines[0].c.nonce, "the next look names the recreated spool's capture")
	_, _, read = dd.spoolHeads.heads(ctx, root, l[0])
	require.False(t, read, "control: once the removal returned, the index remembers the file again")
}

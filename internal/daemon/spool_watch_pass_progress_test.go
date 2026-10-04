package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// spoolWatchLookEveryTick drives the watcher's look (lookAtClientSpools, which watchClientSpools runs)
// on the look's injected clock: one look per interval from now on, every look kicked, until looks
// looks after the first have run or enough reports true. It returns when each pass ran, on that
// clock, from the first look.
func spoolWatchLookEveryTick(dd *daemon, looks int, enough func(passedAt []time.Duration) bool) []time.Duration {
	ctx := context.Background()
	entries := map[string]*spoolWatchEntry{}
	start := time.Now()
	var passedAt []time.Duration
	for i := 0; i <= looks; i++ {
		now := start.Add(time.Duration(i) * dd.spool.every)
		before := dd.m.Counter(counterSpoolWatchDrains).Value()
		dd.lookAtClientSpools(ctx, entries, true, now)
		if dd.m.Counter(counterSpoolWatchDrains).Value() > before {
			passedAt = append(passedAt, now.Sub(start))
		}
		if enough(passedAt) {
			break
		}
	}
	return passedAt
}

// TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff (V6 close-out C1.13,
// D31): a client spool written as a hook writes it, whose head waits on an arrival nothing will
// publish, with a line behind it that a pass can consume (another session's delivery, which a reused
// pid's hook appended), on a host where every pass outlasts its budget: every admission of the waiting
// head is held past idleRunBudget (slowWaitingHead). The first pass publishes the line behind the head,
// and its budget then ends it inside the spool, which is therefore due again at the next look
// (TestSpoolWatch_APassItsBudgetCutShortDoesNotBackOffTheSpoolsItLeft). Every pass after it only
// consumes that line again, which is no progress and must not let the budget end the pass: the pass
// finishes the spool, finds it unconsumable, and the spool keeps the doubling wait. Before the fix each
// of those passes stopped inside the spool, and the watcher passed it, and every client spool with it,
// at every look for the whole horizon.
func TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadKeepsTheBackoff(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	blocked := blockedSpoolHead(t, dd, root, "sess-spool-progress-stuck", 0)
	cfg := dd.drainConfig()
	admit, held := slowWaitingHead(cfg.Admit, blocked.Nonce)
	cfg.Admit = admit
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.spool.every, dd.spool.horizon = spoolWatchTick, 50*spoolWatchTick
	behind := liveOrderTool(dd, root, "sess-spool-progress-other", 7)
	writeHookSpool(t, root, "client-6363.ndjson", blocked, behind)

	// The first look finds the spool new. The first pass, a look later, publishes the line behind the
	// head and stops on its budget in the spool, so the next look passes it again; that pass makes no
	// progress, finishes the spool, and the next is due after spoolRetryAfter's wait for a second pass.
	want := []time.Duration{spoolWatchTick, 2 * spoolWatchTick, 2*spoolWatchTick + spoolRetryAfter(spoolWatchTick, 2)}
	passedAt := spoolWatchLookEveryTick(dd, 7, func(passedAt []time.Duration) bool { return len(passedAt) > len(want) })
	require.Equal(t, int32(len(passedAt)), held.Load(),
		"fixture: every pass's admission of the waiting head was held past its budget (passes at %v)", passedAt)
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first pass published the line behind the head")
	require.Equal(t, want, passedAt,
		"a pass that only consumed again the line behind the blocked head made no progress: it finishes the "+
			"spool, which keeps the doubling wait instead of being passed at every look")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-6363.ndjson"),
		"nothing of it was lost: it stays for a drain that can publish it")
}

// TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt (V6
// close-out C1.13, D31): the blocked spool of the row above comes first in host order, and a later
// spool holds a fresh capture of a third session. A watcher pass passes every client spool in host
// order, so on a host where every pass outlasts its budget (at the waiting head here, slowWaitingHead),
// each pass after the first stopped in the blocked spool on the line it only consumed again, no
// budgeted pass ever reached the later spool, and its capture waited for a session end, Stop or a
// restart. D31's budgeted pass consumes a line when it can: the pass that makes no progress in the
// blocked spool goes on and publishes the later capture.
func TestSpoolWatch_ABlockedSpoolWithAConsumedLineBehindItsHeadDoesNotStarveTheSpoolsAfterIt(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	blocked := blockedSpoolHead(t, dd, root, "sess-spool-starve-stuck", 0)
	cfg := dd.drainConfig()
	admit, held := slowWaitingHead(cfg.Admit, blocked.Nonce)
	cfg.Admit = admit
	dd.drain.Store(newDrainer(cfg))
	liveOrderWorkers(t, dd, 2, dd.runIngested)
	dd.spool.every, dd.spool.horizon = spoolWatchTick, 50*spoolWatchTick
	behind := liveOrderTool(dd, root, "sess-spool-starve-other", 7)
	writeHookSpool(t, root, "client-6363.ndjson", blocked, behind)
	fresh := liveOrderTool(dd, root, "sess-spool-starve-later", 50)
	writeHookSpool(t, root, "client-6364.ndjson", fresh)

	passedAt := spoolWatchLookEveryTick(dd, 3, func([]time.Duration) bool { return spoolWatchPublished(dd, fresh.Nonce) })
	require.Equal(t, int32(len(passedAt)), held.Load(),
		"fixture: every pass's admission of the waiting head was held past its budget (passes at %v)", passedAt)
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first pass published the line behind the head")
	require.True(t, spoolWatchPublished(dd, fresh.Nonce),
		"the later spool's fresh capture is published by a budgeted pass that made no progress in the "+
			"blocked spool before it (passes at %v)", passedAt)
	require.Len(t, passedAt, 2,
		"the pass after the one that published the line behind the head publishes it (passes at %v)", passedAt)
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-6363.ndjson"),
		"nothing of the blocked spool was lost: it stays for a drain that can publish it")
}

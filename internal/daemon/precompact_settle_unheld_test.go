package daemon

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// D73(b): a PreCompact seal names every capture of its session the settle leaves (D55: 'captures it
// leaves are named in the drop report and replayed later'), wherever the daemon holds it. The settle
// looked for them in two places only, the session's lane and the hook client spools, so a leased
// arrival held in neither was left out of the drop report without a word: one the ring dropped
// because it was full, one the lanes refused because the session's lane was at its bound, one still
// waiting in the ring for a worker, and one a daemon before this one accepted and never published.
// Each is durable in the daemon's WAL and leased in the delivery journal, so a later drain does
// replay it; the checkpoint just did not say it was missing. These rows pin that each is named, and
// that each is then replayed.

// TestPreCompactSettle_NamesALeasedReadTheRingDropped: the ring was full when the Read was accepted
// (here: a ring no worker receives from, so Accept takes its full-ring branch), so only the WAL and
// the journal hold it. Nothing drains before the seal.
func TestPreCompactSettle_NamesALeasedReadTheRingDropped(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	dd.ing.ring = make(chan job) // no worker receives: Accept drops the job, as from a full ring
	const sess core.SessionID = "sess-precompact-ring-dropped"
	tool := liveOrderTool(dd, root, sess, 1)
	acceptPrompt(t, dd, tool)
	require.Equal(t, int64(1), dd.m.Counter(counterL0RingFull).Value(), "fixture sanity: the ring dropped the Read")
	probe := bindSealProbe(dd, tool.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-ring-dropped")
	pre.TS = tool.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.False(t, probe.published[tool.Nonce], "fixture sanity: only the WAL held the Read when the seal ran")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(tool.Event.ToolUseID)},
	}, probe.drops, "the seal names the Read the ring dropped")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactUnreplayed).Value())

	requireReplayedLater(t, dd, tool.Nonce)
}

// TestPreCompactSettle_NamesALeasedReadTheLanesRefused: the session's lane holds one job and is
// publishing the first Read (held by the gate); the second Read arrives while it does, the lanes
// refuse it, and only the WAL and the journal hold it. The row ends the settle (settleCut) once the
// settle has begun, so the seal is made with the first Read still publishing and the second refused.
func TestPreCompactSettle_NamesALeasedReadTheLanesRefused(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	ctx, cut := settleCut(t)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	laneTestSetLanes(dd, laneCapacity, 1) // the session's lane holds one job
	const sess core.SessionID = "sess-precompact-lane-refused"
	first := liveOrderTool(dd, root, sess, 1)
	refused := liveOrderTool(dd, root, sess, 2)
	run, open, held := settleGate(dd, first.Nonce)
	liveOrderWorkers(t, dd, 2, run)
	t.Cleanup(open)

	acceptPrompt(t, dd, first)
	require.True(t, heldWithin(held, liveOrderBound), "fixture sanity: the lane is publishing the first Read")
	acceptPrompt(t, dd, refused)
	require.True(t, liveOrderPollUntil(liveOrderBound, func() bool {
		return dd.m.Counter(counterOrderingLaneFull).Value() == 1
	}), "fixture sanity: the lanes refused the second Read")
	probe := bindSealProbe(dd, first.Nonce, refused.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-lane-refused")
	pre.TS = refused.TS + 1
	done := make(chan ipc.Response, 1)
	go func() { done <- dd.dispatchOp(ctx, pre) }()
	require.True(t, liveOrderPollUntil(liveOrderBound, func() bool { return settleStarted(dd) }),
		"fixture sanity: the settle began")
	cut() // the bound ends with the first Read publishing and the second refused
	require.True(t, (<-done).OK)

	require.Equal(t, 1, probe.calls)
	require.False(t, probe.published[first.Nonce], "fixture sanity: the held Read did not publish")
	require.False(t, probe.published[refused.Nonce], "fixture sanity: nothing published the refused Read")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 2, 2, 0, 0, 2)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(refused.Event.ToolUseID)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(first.Event.ToolUseID)},
	}, probe.drops, "the seal names the Read its lane holds and the one the lanes refused, newest first")

	open()
	require.True(t, liveOrderPollUntil(liveOrderBound, func() bool { return spoolWatchPublished(dd, first.Nonce) }),
		"fixture sanity: the held Read publishes once the gate opens")
	requireReplayedLater(t, dd, refused.Nonce)
}

// TestPreCompactSettle_NamesALeasedReadStillInTheRing: every worker is busy (here: none has started),
// so the accepted Read waits in the ring and no lane holds it yet. The seal names it, and the worker
// pool publishes it once it runs.
func TestPreCompactSettle_NamesALeasedReadStillInTheRing(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	const sess core.SessionID = "sess-precompact-in-ring"
	tool := liveOrderTool(dd, root, sess, 1)
	acceptPrompt(t, dd, tool)
	require.Len(t, dd.ing.ring, 1, "fixture sanity: the Read waits in the ring")
	probe := bindSealProbe(dd, tool.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-in-ring")
	pre.TS = tool.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.False(t, probe.published[tool.Nonce], "fixture sanity: no worker had taken the Read")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: checkpoint.DropKindUnreplayedCapture, Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 1, 0, 0, 1)},
		{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(tool.Event.ToolUseID)},
	}, probe.drops, "the seal names the Read still in the ring")

	liveOrderWorkers(t, dd, 2, dd.runIngested)
	require.True(t, liveOrderPollUntil(liveOrderBound, func() bool { return spoolWatchPublished(dd, tool.Nonce) }),
		"the worker pool publishes the Read the seal named")
	require.False(t, unheldRecords(dd.ing.lanes, tool.Nonce), "the record forgets a job its lane took")
}

// TestPreCompactSettle_CountsALeasedArrivalOnlyTheJournalKnows: a daemon before this one accepted the
// Read (its WAL line and its lease are durable) and stopped before publishing it, so this daemon holds
// no request for it in memory, only the lease; the drain it runs replays the WAL. (The row makes that
// state with Accept's durable half, makeDurable, which is exactly what such a daemon leaves.) The seal
// cannot say what the capture was, but it counts it, and says it knows it only by its lease.
func TestPreCompactSettle_CountsALeasedArrivalOnlyTheJournalKnows(t *testing.T) {
	dd, root := settleTestDaemon(t, liveOrderBound)
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	const sess core.SessionID = "sess-precompact-journal-only"
	tool := liveOrderTool(dd, root, sess, 1)
	line, err := ipc.EncodeRequest(tool)
	require.NoError(t, err)
	j, err := dd.ing.makeDurable(tool, bytes.TrimSuffix(line, []byte{'\n'}))
	require.NoError(t, err)
	require.True(t, j.leased, "fixture sanity: the Read is leased")
	probe := bindSealProbe(dd, tool.Nonce)

	pre := checkpointRequest(dd, sess, "nonce-precompact-journal-only")
	pre.TS = tool.TS + 1
	require.True(t, dd.dispatchOp(context.Background(), pre).OK)

	require.Equal(t, 1, probe.calls)
	require.False(t, probe.published[tool.Nonce], "fixture sanity: only the WAL held the Read when the seal ran")
	require.Equal(t, []checkpoint.DropEntry{{
		Kind: checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, 1, 0, 0, 1, 0) +
			fmt.Sprintf(unknownKindClauseFormat, 1),
	}}, probe.drops, "the seal counts the capture only its lease names, and says it cannot tell its kind")
	require.Equal(t, int64(1), dd.m.Counter(counterPrecompactUnreplayed).Value())

	requireReplayedLater(t, dd, tool.Nonce)
}

// requireReplayedLater asserts the second half of D55's rule for a capture a seal named: a drain,
// here run by the row as the lanes' requested drain or the idle drain would run it, replays it from
// the WAL. The pass's release then drops it from the record of leased jobs no lane holds.
func requireReplayedLater(t *testing.T, dd *daemon, nonce string) {
	t.Helper()
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, nonce), "the drain replays the capture the seal named")
	require.False(t, unheldRecords(dd.ing.lanes, nonce), "the record forgets a job the drain published")
}

// unheldRecords reports whether ls's record of leased jobs no lane holds has delivery.
func unheldRecords(ls *dispatchLanes, delivery string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, m := range ls.unheld {
		if _, ok := m[delivery]; ok {
			return true
		}
	}
	return false
}

// TestDispatchLanes_RecordsTheLeasedJobsNoLaneHolds pins the record's bookkeeping without a daemon
// (delivery_unheld.go): Accept's record leaves it once a lane holds the job; a job the lanes refuse,
// evict or hand back to the WAL enters it; an unleased job never does; and it holds at most
// unheldCapacity jobs.
func TestDispatchLanes_RecordsTheLeasedJobsNoLaneHolds(t *testing.T) {
	const sess core.SessionID = "s"
	mk := func(arrival uint64) job {
		return job{
			req: ipc.Request{
				Op: ipc.OpObserveTool, Session: sess, TS: core.UnixMilli(arrival), Nonce: orderNonce(int(arrival)),
				Event: &hookio.Event{ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%d", arrival))},
			},
			leased: true, lease: deliveryLease{Session: sess, ArrivalSeq: arrival, Delivery: orderNonce(int(arrival))},
		}
	}
	recorded := func(ls *dispatchLanes) []uint64 {
		var out []uint64
		for _, u := range ls.unheldOf(sess) {
			out = append(out, u.lease.ArrivalSeq)
		}
		return out
	}
	ls := newDispatchLanes(8, 2)

	ls.noteUnheld(mk(1)) // Accept records a leased job before it offers it to the ring
	require.Equal(t, []uint64{1}, recorded(ls))
	require.Equal(t, captureOf(mk(1).req), ls.unheldOf(sess)[0].capture, "the record keeps what names the job")
	own, _, _ := ls.join(mk(1))
	require.True(t, own)
	require.Empty(t, recorded(ls), "a job its lane holds leaves the record")

	ls.noteUnheld(mk(3))
	_, _, _ = ls.join(mk(3))
	ls.noteUnheld(mk(2))
	_, full, _ := ls.join(mk(2)) // the lane is at its bound: 2 takes 3's place, and 3 is refused
	require.True(t, full)
	require.Equal(t, []uint64{3}, recorded(ls), "the job the lane evicts enters the record")

	ls.noteUnheld(mk(4))
	_, full, _ = ls.join(mk(4))
	require.True(t, full)
	require.Equal(t, []uint64{3, 4}, recorded(ls), "a job the lanes refuse stays in the record")
	_, _, _ = ls.join(mk(2))
	require.Equal(t, []uint64{3, 4}, recorded(ls), "a duplicate of a held job changes nothing")

	ls.park(sess)
	require.Equal(t, 2, ls.forget(sess))
	require.Equal(t, []uint64{1, 2, 3, 4}, recorded(ls), "the jobs a flush hands back to the WAL enter the record")

	ls.forgetUnheld(sess, []string{orderNonce(1), orderNonce(3)})
	require.Equal(t, []uint64{2, 4}, recorded(ls))

	unleased := mk(5)
	unleased.leased = false
	ls.noteUnheld(unleased)
	require.Equal(t, []uint64{2, 4}, recorded(ls), "an unleased job is never recorded")

	// The bound: past unheldCapacity jobs, none is recorded (the journal still counts them).
	big := newDispatchLanes(8, 2)
	for a := uint64(1); a <= unheldCapacity+1; a++ {
		big.noteUnheld(mk(a))
	}
	require.Equal(t, unheldCapacity, big.unheldN)
	require.False(t, unheldRecords(big, orderNonce(unheldCapacity+1)), "the job past the bound is not recorded")
}

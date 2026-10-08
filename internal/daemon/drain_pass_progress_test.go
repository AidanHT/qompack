package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.13 and D31, candidate 8. A line a pass consumes out of order, behind a head that
// waits on an earlier arrival of its session, is remembered only for the rest of that pass: the
// consumed front cannot roll over it while the head waits. Every later pass reads it and consumes it
// again, absorbing what the pass that published or retired it left, and a corrupt or refused line the
// same way. Each of those consumptions counted as the pass consuming a line, which is what lets a
// spent pass budget end a pass (withPassBudget). On a host slow enough that a pass reached such a line
// with its budget spent, every pass therefore stopped inside that spool having made no progress: the
// watcher passed it at every look instead of backing off, no budgeted pass reached a spool after it in
// pass order, and a requested pass asked for the next one after each of them. Nothing was lost; the
// captures waited for an unbudgeted drain. Only progress counts now: the consumed front advancing, or
// a line the pass itself published or retired.

// slowWaitingHead is admit holding every admission of the delivery that carries nonce past a whole pass
// budget, in real time, before deciding it, as a loaded host can hold the scope check every admission
// makes. The delivery is a spool's waiting head (blockedSpoolHead), which every pass reads and admits
// again, so every pass has spent its budget by the time it reads any line behind that head. It returns
// how many admissions it has held.
//
// It used to hold every sync of a client spool instead, but a pass no longer syncs a spool it has synced
// before and finds unchanged (spoolMemo), so only the first pass over a blocked spool would have spent
// its budget there: the passes after it, the subject of these rows, would not.
func slowWaitingHead(admit func(ipc.Request) admissionVerdict, nonce string,
) (func(ipc.Request) admissionVerdict, *atomic.Int32) {
	var held atomic.Int32
	return func(req ipc.Request) admissionVerdict {
		if req.Nonce == nonce {
			held.Add(1)
			timer := time.NewTimer(idleRunBudget + spoolWatchTick)
			defer timer.Stop()
			<-timer.C
		}
		return admit(req)
	}, &held
}

// blockedSpoolHead returns arrival 1 of sess, a delivery that can never pass the ordering gate: its
// arrival 0 is leased and then lost, so nothing will ever publish it. The two arrivals carry
// orderNonce(first) and orderNonce(first+1).
func blockedSpoolHead(t *testing.T, dd *daemon, root string, sess core.SessionID, first int) ipc.Request {
	t.Helper()
	_, ok := dd.ing.leaseDelivery(context.Background(), spD3Prompt(dd, root, sess, orderNonce(first), "lost"))
	require.True(t, ok)
	return spD3Prompt(dd, root, sess, orderNonce(first+1), "blocked")
}

// hookSpoolLine is req as a hook's spool writer appends it: its encoding, which ends its own line.
func hookSpoolLine(t *testing.T, req ipc.Request) []byte {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	return line
}

// writeRawSpool writes lines, each ending in its own newline, back to back as the client spool base.
func writeRawSpool(t *testing.T, root, base string, lines ...[]byte) {
	t.Helper()
	var buf []byte
	for _, line := range lines {
		buf = append(buf, line...)
	}
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, base)), buf, 0o600))
}

// denyNonce is admit with the policy denying the delivery that carries nonce.
func denyNonce(admit func(ipc.Request) admissionVerdict, nonce string) func(ipc.Request) admissionVerdict {
	return func(req ipc.Request) admissionVerdict {
		if req.Nonce == nonce {
			return admissionVerdict{Request: req, Denied: true, Reason: "test policy denial"}
		}
		return admit(req)
	}
}

// denyNonceAtDispatch is admit with the policy changing its mind about the delivery that carries nonce
// between a pass reading its line and dispatching it: it admits that delivery's first admission, the
// one the read loop makes, and denies every later one, such as dispatchPending's. A leased line the
// pass reads is then retired at dispatch (errReplayDenied), not by the read loop's denial branch. It
// returns how many admissions of that delivery it has answered.
func denyNonceAtDispatch(admit func(ipc.Request) admissionVerdict, nonce string,
) (func(ipc.Request) admissionVerdict, *atomic.Int32) {
	var calls atomic.Int32
	return func(req ipc.Request) admissionVerdict {
		if req.Nonce == nonce && calls.Add(1) > 1 {
			return admissionVerdict{Request: req, Denied: true, Reason: "test policy denial at dispatch"}
		}
		return admit(req)
	}, &calls
}

// publishLiveFirst is admit with one side effect: the first time a pass reads the delivery that
// carries nonce, the ingest first publishes that delivery's own queued job, as a worker would. Its live
// copy then reaches the committed frontier while the pass is reading the spool that holds its spooled
// copy, between the pass deferring a later arrival of its session and reading that copy.
func publishLiveFirst(t *testing.T, dd *daemon, admit func(ipc.Request) admissionVerdict, nonce string,
) func(ipc.Request) admissionVerdict {
	var once sync.Once
	return func(req ipc.Request) admissionVerdict {
		if req.Nonce == nonce {
			once.Do(func() {
				select {
				case job := <-dd.ing.ring:
					dd.ing.dispatch(context.Background(), dd.runIngested, job)
				default:
					t.Error("fixture: the live job is not queued")
				}
			})
		}
		return admit(req)
	}
}

// TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass: a pass whose budget
// is spent before it starts meets a spool whose head waits on an arrival nothing publishes, with a line
// behind that head an earlier pass already consumed: a delivery it published (a 0.3.0 hook that
// reused the pid appended it to the legacy name), one it retired by a policy denial, or a corrupt
// line. Consuming that line again is no progress, so it must not let the budget end the pass (D31: a
// pass is cut by its budget only once it has consumed a line). The pass goes on to the next spool in
// host order and publishes the fresh capture there, and that publication is what ends it.
func TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass(t *testing.T) {
	for _, behind := range []string{"a delivery a pass published", "a delivery a pass retired", "a corrupt line"} {
		t.Run(behind, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := lineDeadlineDrainConfig(dd)
			blocked := blockedSpoolHead(t, dd, root, "sess-reconsume-stuck", 0)
			other := liveOrderTool(dd, root, "sess-reconsume-other", 7)
			line := hookSpoolLine(t, other)
			consumed := func() bool { return spoolWatchPublished(dd, other.Nonce) }
			switch behind {
			case "a delivery a pass retired":
				lease, ok := dd.ing.leaseDelivery(ctx, other)
				require.True(t, ok)
				cfg.Admit = denyNonce(cfg.Admit, other.Nonce)
				consumed = func() bool {
					j, err := dd.deliveryJournal()
					require.NoError(t, err)
					denied, err := j.terminalDenied(lease)
					return err == nil && denied
				}
			case "a corrupt line":
				line = []byte("not a request\n")
				consumed = func() bool { return dd.m.Counter(counterDrainFileError).Value() > 0 }
			}
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			writeRawSpool(t, root, "client-8801.ndjson", hookSpoolLine(t, blocked), line)

			_, err := dr.DrainClientSpools(ctx) // no budget: it consumes the line behind the head
			require.NoError(t, err)
			require.True(t, consumed(), "fixture: an earlier pass consumed %s behind the blocked head", behind)

			fresh := liveOrderTool(dd, root, "sess-reconsume-later", 50)
			writeHookSpool(t, root, "client-8802.ndjson", fresh)
			n, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
			require.True(t, spoolWatchPublished(dd, fresh.Nonce),
				"a pass that only consumed again %s behind the blocked head made no progress, so its spent "+
					"budget must not end it there: it publishes the next spool's capture (err %v)", behind, err)
			require.Equal(t, 1, n, "the next spool's capture is the one line the pass published")
			require.ErrorIs(t, err, errPassBudgetSpent, "and that publication ended the pass, its budget spent")
			require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
			require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8801.ndjson"),
				"nothing of the blocked spool is lost: it stays for a drain that can publish it")
		})
	}
}

// TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass: what does count as a
// pass consuming a line behind a blocked head is a line the pass itself publishes or retires, although
// the consumed front cannot move over it. Once it has one, a spent budget ends the pass (D31: it runs
// past its budget by at most that line), and the fresh capture in the next spool waits for the next
// pass. Five ways in. The line behind the head is published as it is read; it is retired by a policy
// denial as it is read (the read loop's denial branch); or the policy admits it as it is read and
// denies it at dispatch, which retires it without publishing it (processOne's changed without
// dispatched). Or it waits on its own session's earlier arrival, whose live copy publishes while the
// pass reads on, and the pass's look-ahead publishes it, or retires it at dispatch, once the pass has
// consumed the spooled copy of that arrival. Consuming that copy is no progress (its live copy is
// published), so it must not end the pass before the look-ahead's publication or retirement.
func TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass(t *testing.T) {
	retiredAt := func(t *testing.T, dd *daemon, lease deliveryLease) func() bool {
		return func() bool {
			j, err := dd.deliveryJournal()
			require.NoError(t, err)
			denied, err := j.terminalDenied(lease)
			return err == nil && denied
		}
	}
	for _, how := range []string{
		"published as it is read", "retired as it is read", "retired as it is dispatched",
		"published by the look-ahead", "retired by the look-ahead at dispatch",
	} {
		t.Run(how, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := lineDeadlineDrainConfig(dd)
			blocked := blockedSpoolHead(t, dd, root, "sess-progress-stuck", 0)
			lines := [][]byte{hookSpoolLine(t, blocked)}
			var consumed func() bool
			switch how {
			case "published as it is read":
				other := liveOrderTool(dd, root, "sess-progress-other", 7)
				lines = append(lines, hookSpoolLine(t, other))
				consumed = func() bool { return spoolWatchPublished(dd, other.Nonce) }
			case "retired as it is read":
				other := liveOrderTool(dd, root, "sess-progress-other", 7)
				lease, ok := dd.ing.leaseDelivery(ctx, other)
				require.True(t, ok)
				cfg.Admit = denyNonce(cfg.Admit, other.Nonce)
				lines = append(lines, hookSpoolLine(t, other))
				consumed = retiredAt(t, dd, lease)
			case "retired as it is dispatched":
				other := liveOrderTool(dd, root, "sess-progress-other", 7)
				lease, ok := dd.ing.leaseDelivery(ctx, other)
				require.True(t, ok)
				admit, admissions := denyNonceAtDispatch(cfg.Admit, other.Nonce)
				cfg.Admit = admit
				lines = append(lines, hookSpoolLine(t, other))
				consumed = func() bool {
					require.Equal(t, int32(2), admissions.Load(),
						"fixture: the policy admitted the line as it was read and denied it at dispatch")
					require.False(t, spoolWatchPublished(dd, other.Nonce), "fixture: it was retired, not published")
					return retiredAt(t, dd, lease)()
				}
			case "published by the look-ahead", "retired by the look-ahead at dispatch":
				const sess core.SessionID = "sess-progress-ahead"
				p0 := spD3Prompt(dd, root, sess, orderNonce(10), "p0")
				acceptPrompt(t, dd, p0) // leased, arrival 0 of sess; its job waits for a worker, and none runs
				p1 := liveOrderTool(dd, root, sess, 1)
				// p1 waits on p0; then the hook's spooled copy of p0, whose live copy publishes as the pass
				// reaches it.
				lines = append(lines, hookSpoolLine(t, p1), hookSpoolLine(t, p0))
				cfg.Admit = publishLiveFirst(t, dd, cfg.Admit, p0.Nonce)
				consumed = func() bool {
					require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: p0's live copy published")
					return spoolWatchPublished(dd, p1.Nonce)
				}
				if how == "retired by the look-ahead at dispatch" {
					lease, ok := dd.ing.leaseDelivery(ctx, p1) // arrival 1 of sess, after p0
					require.True(t, ok)
					admit, admissions := denyNonceAtDispatch(cfg.Admit, p1.Nonce)
					cfg.Admit = admit
					consumed = func() bool {
						require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: p0's live copy published")
						require.Equal(t, int32(2), admissions.Load(),
							"fixture: the policy admitted p1 as it was read and denied it at the look-ahead's dispatch")
						require.False(t, spoolWatchPublished(dd, p1.Nonce), "fixture: p1 was retired, not published")
						return retiredAt(t, dd, lease)()
					}
				}
			}
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			writeRawSpool(t, root, "client-8811.ndjson", lines...)
			fresh := liveOrderTool(dd, root, "sess-progress-later", 50)
			writeHookSpool(t, root, "client-8812.ndjson", fresh)

			_, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
			require.True(t, consumed(), "the pass consumed the line behind the blocked head: %s (err %v)", how, err)
			require.ErrorIs(t, err, errPassBudgetSpent, "and that line ended the pass, its budget spent")
			require.False(t, spoolWatchPublished(dd, fresh.Nonce),
				"the pass started no line after it: the next spool's capture waits for the next pass")
			require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
		})
	}
}

// TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced: the consumed front advancing is
// progress too, and once a pass whose budget is spent has made it, the pass starts no other line (D31).
// Here the spool's head is a corrupt line, a hook's torn write: the pass consumes it, which advances
// the front past it for good, and must stop before the fresh capture behind it. Nothing the pass
// publishes stands between the two lines, so the read loop's own check before each line is the one
// that stops it. A prototype of the C1.13 fix dropped that check, and no row noticed.
func TestDrainClientSpools_ASpentPassStartsNoLineAfterItsFrontAdvanced(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(lineDeadlineDrainConfig(dd))
	dd.drain.Store(dr)
	torn := []byte("not a request\n")
	fresh := liveOrderTool(dd, root, "sess-front-advanced", 1)
	writeRawSpool(t, root, "client-8841.ndjson", torn, hookSpoolLine(t, fresh))

	n, err := dr.DrainClientSpools(withPassBudget(context.Background(), 0))
	require.ErrorIs(t, err, errPassBudgetSpent, "the pass consumed the torn line, and its spent budget then ended it")
	require.False(t, spoolWatchPublished(dd, fresh.Nonce), "the pass started no line after the one it consumed")
	require.Zero(t, n, "the pass published nothing")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.NotNil(t, st["client-8841.ndjson"])
	require.Equal(t, int64(len(torn)), st["client-8841.ndjson"].Offset,
		"fixture: consuming the torn line advanced the spool's consumed front past it")

	_, err = dr.DrainClientSpools(context.Background())
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, fresh.Nonce), "control: the next pass publishes the capture")
}

// TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead is the same defect in a
// drain the ingest's lanes ask for (drainOnRequest), which runs under the same pass budget and asks for
// the next pass itself when its budget ended one (passLeftWork). Every pass outlasts its budget at the
// blocked head here (slowWaitingHead). The first pass publishes the line behind the blocked head and
// stops on its budget after that progress, so it asks again. The next only consumes that line again:
// it must finish the spool and must not ask again. Before the fix it stopped on its budget and asked
// again after every such pass, for as long as the host stayed slow. Once a fresh capture lands in a
// later spool, the next requested pass reaches it past the blocked spool and publishes it.
func TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	blocked := blockedSpoolHead(t, dd, root, "sess-requested-reconsume-stuck", 0)
	cfg := lineDeadlineDrainConfig(dd)
	admit, held := slowWaitingHead(cfg.Admit, blocked.Nonce)
	cfg.Admit = admit
	dd.drain.Store(newDrainer(cfg))
	behind := liveOrderTool(dd, root, "sess-requested-reconsume-other", 7)
	writeHookSpool(t, root, "client-8821.ndjson", blocked, behind)
	ctx := context.Background()

	dd.requestedDrainPass(ctx)
	require.Equal(t, int32(1), held.Load(), "fixture: the pass's admission of the blocked head was held past its budget")
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first pass published the line behind the head")
	require.Equal(t, 1, len(dd.ing.drainKick), "fixture: its budget ended it after that progress, so it asks again")
	<-dd.ing.drainKick // the requester takes it, as drainOnRequest does

	dd.requestedDrainPass(ctx)
	require.Equal(t, int32(2), held.Load(), "fixture: so was the next pass's, which spent its budget there too")
	require.Zero(t, len(dd.ing.drainKick),
		"a pass that only consumed again the line an earlier pass published made no progress: its budget must "+
			"not end it in the blocked spool, and it must not ask for another pass")

	fresh := liveOrderTool(dd, root, "sess-requested-reconsume-later", 50)
	writeHookSpool(t, root, "client-8822.ndjson", fresh)
	dd.requestedDrainPass(ctx)
	require.True(t, spoolWatchPublished(dd, fresh.Nonce),
		"the next requested pass went past the blocked spool and published the later spool's capture")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8821.ndjson"),
		"nothing of the blocked spool is lost: it stays for a drain that can publish it")
}

// TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead is the same defect in the
// idle drain, which RunOnce runs under a pass budget too (registerPaced). Every pass outlasts its
// budget at the blocked head here (slowWaitingHead). The first idle pass publishes the line behind
// the blocked head and its budget ends it there, before the next spool. The second only consumes that
// line again, and must go on to publish the next spool's capture. Before the fix every idle pass
// stopped where the first did.
func TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	blocked := blockedSpoolHead(t, dd, root, "sess-idle-reconsume-stuck", 0)
	cfg := lineDeadlineDrainConfig(dd)
	admit, held := slowWaitingHead(cfg.Admit, blocked.Nonce)
	cfg.Admit = admit
	dd.drain.Store(newDrainer(cfg))
	behind := liveOrderTool(dd, root, "sess-idle-reconsume-other", 7)
	writeHookSpool(t, root, "client-8831.ndjson", blocked, behind)
	fresh := liveOrderTool(dd, root, "sess-idle-reconsume-later", 50)
	writeHookSpool(t, root, "client-8832.ndjson", fresh)
	ctx := context.Background()

	ran, err := dd.idle.RunOnce(ctx, idleRunBudget)
	require.NoError(t, err)
	require.Contains(t, ran, idleTaskDrain, "fixture: the idle pass ran the drain")
	require.Equal(t, int32(1), held.Load(), "fixture: the pass's admission of the blocked head was held past its budget")
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first idle pass published the line behind the head")
	require.False(t, spoolWatchPublished(dd, fresh.Nonce), "fixture: and its budget ended it there, before the next spool")

	ran, err = dd.idle.RunOnce(ctx, idleRunBudget)
	require.NoError(t, err)
	require.Contains(t, ran, idleTaskDrain, "fixture: the idle pass ran the drain")
	require.Equal(t, int32(2), held.Load(), "fixture: so was the second pass's, which spent its budget there too")
	require.True(t, spoolWatchPublished(dd, fresh.Nonce),
		"an idle pass that only consumed again the line behind the blocked head went on and published the "+
			"next spool's capture")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8831.ndjson"),
		"nothing of the blocked spool is lost: it stays for a drain that can publish it")
}

// TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress: once a pass whose budget is
// spent has made progress, it starts no other line (D31), and that holds inside the look-ahead too.
// The progress releases the deferred lines of its session, and the look-ahead's re-attempt (drainFile's
// reattempt) would publish every one of them; its two checks of the budget, before each round and before
// each line, are what stop it. Removing both left every row green (the pre-freeze audit's mutation MX).
// Two ways in, behind a head that waits on an arrival nothing publishes. The read loop publishes x,
// arrival 0 of a session whose arrivals 1 and 2 come before it in the spool and wait for it: the pass
// stops before the re-attempt reaches either. Or the pass consumes the spooled copy of p0, whose live
// copy publishes as the pass reads it, so p1 and p2, waiting behind it, are released: consuming that
// copy is no progress, so the re-attempt publishes p1, and that progress must stop it before p2.
func TestDrainClientSpools_ASpentPassStartsNoLookAheadLineAfterItsProgress(t *testing.T) {
	for _, how := range []string{"after a line the read loop published", "after a line the look-ahead published"} {
		t.Run(how, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := lineDeadlineDrainConfig(dd)
			const sess core.SessionID = "sess-lookahead-spent"
			blocked := blockedSpoolHead(t, dd, root, "sess-lookahead-stuck", 0)
			var progress, first, second ipc.Request // the line that is progress, and the two it releases
			var lines [][]byte
			switch how {
			case "after a line the read loop published":
				progress = liveOrderTool(dd, root, sess, 0)
				first, second = liveOrderTool(dd, root, sess, 1), liveOrderTool(dd, root, sess, 2)
				for _, req := range []ipc.Request{progress, first, second} { // leased in arrival order
					_, ok := dd.ing.leaseDelivery(ctx, req)
					require.True(t, ok)
				}
				lines = [][]byte{hookSpoolLine(t, first), hookSpoolLine(t, second), hookSpoolLine(t, progress)}
			case "after a line the look-ahead published":
				p0 := spD3Prompt(dd, root, sess, orderNonce(10), "p0")
				acceptPrompt(t, dd, p0) // leased, arrival 0 of sess; its job waits for a worker, and none runs
				progress = liveOrderTool(dd, root, sess, 1)
				second = liveOrderTool(dd, root, sess, 2)
				first = p0 // released by its live copy, whose spooled copy the pass consumes without progress
				cfg.Admit = publishLiveFirst(t, dd, cfg.Admit, p0.Nonce)
				lines = [][]byte{hookSpoolLine(t, progress), hookSpoolLine(t, second), hookSpoolLine(t, p0)}
			}
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			writeRawSpool(t, root, "client-8891.ndjson", append([][]byte{hookSpoolLine(t, blocked)}, lines...)...)

			_, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
			require.True(t, spoolWatchPublished(dd, progress.Nonce), "fixture: the pass made its progress (err %v)", err)
			require.ErrorIs(t, err, errPassBudgetSpent, "and that progress ended the pass, its budget spent")
			if how == "after a line the read loop published" {
				require.False(t, spoolWatchPublished(dd, first.Nonce),
					"the spent pass started no look-ahead line after its progress")
			} else {
				require.True(t, spoolWatchPublished(dd, first.Nonce), "fixture: p0's live copy published")
			}
			require.False(t, spoolWatchPublished(dd, second.Nonce),
				"the spent pass started no look-ahead line after its progress")

			_, err = dr.DrainClientSpools(ctx)
			require.NoError(t, err)
			require.True(t, spoolWatchPublished(dd, second.Nonce), "control: the next pass publishes what it left")
			require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
		})
	}
}

// TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass: a deferred line the look-ahead finds
// acknowledged, because its live copy published while the pass read on, is absorbed, not published or
// retired by the pass, so it is no progress, and a spent pass goes on to the next spool. Here p0 and p1
// were both accepted live, and their spooled copies wait behind a head nothing publishes, p1's ahead of
// p0's. Both live copies publish as the pass reads p0's copy: the pass absorbs p0's copy, and the
// look-ahead then absorbs p1's. The fresh capture in the next spool is the pass's progress. No row
// pinned the re-attempt's condition (the pre-freeze audit's mutation M8b, which counted every look-ahead
// consumption as progress, left every row green).
func TestDrainClientSpools_ALookAheadAbsorptionDoesNotEndASpentPass(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := lineDeadlineDrainConfig(dd)
	const sess core.SessionID = "sess-absorb-ahead"
	blocked := blockedSpoolHead(t, dd, root, "sess-absorb-stuck", 0)
	p0 := spD3Prompt(dd, root, sess, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // arrival 0 of sess
	p1 := liveOrderTool(dd, root, sess, 1)
	acceptPrompt(t, dd, p1) // arrival 1; both jobs wait for a worker, and none runs
	cfg.Admit = publishQueuedFirst(t, dd, cfg.Admit, p0.Nonce, 2)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	writeHookSpool(t, root, "client-8895.ndjson", blocked, p1, p0)
	fresh := liveOrderTool(dd, root, "sess-absorb-later", 50)
	writeHookSpool(t, root, "client-8896.ndjson", fresh)

	n, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
	require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: p0's live copy published")
	require.True(t, spoolWatchPublished(dd, p1.Nonce), "fixture: p1's live copy published")
	require.True(t, spoolWatchPublished(dd, fresh.Nonce),
		"absorbing p1's copy in the look-ahead was no progress, so the spent pass went on to the next spool (err %v)", err)
	require.Equal(t, 1, n, "the fresh capture is the one line the pass published")
	require.ErrorIs(t, err, errPassBudgetSpent, "and that publication ended the pass")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
}

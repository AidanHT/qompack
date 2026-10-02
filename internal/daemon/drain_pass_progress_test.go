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

// slowSpoolSyncs holds every sync dr makes of a client spool (drainer.syncFile) past a whole pass
// budget before the real sync, as a deep fsync queue on a slow host can. A budgeted pass has then spent
// its budget by the time it reads the first line of any spool it had to sync. It returns how many syncs
// it has held.
func slowSpoolSyncs(dr *drainer) *atomic.Int32 {
	sync := dr.syncFile
	var held atomic.Int32
	dr.syncFile = func(path string) error {
		if isClientSpoolName(filepath.Base(path)) {
			held.Add(1)
			timer := time.NewTimer(idleRunBudget + spoolWatchTick)
			defer timer.Stop()
			<-timer.C
		}
		return sync(path)
	}
	return &held
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
// behind that head an earlier pass already consumed: a delivery it published (a reused pid's hook
// appended it), one it retired by a policy denial, or a corrupt line. Consuming that line again is no
// progress, so it must not let the budget end the pass (D31: a pass is cut by its budget only once it
// has consumed a line). The pass goes on to the next spool in host order and publishes the fresh
// capture there, and that publication is what ends it.
func TestDrainClientSpools_ReconsumingALineBehindABlockedHeadDoesNotEndASpentPass(t *testing.T) {
	for _, behind := range []string{"a delivery a pass published", "a delivery a pass retired", "a corrupt line"} {
		t.Run(behind, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := dd.drainConfig()
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
// pass. Three ways in: the line behind the head is published as it is read; it is retired by a policy
// denial as it is read; or it waits on its own session's earlier arrival, whose live copy publishes
// while the pass reads on, and the pass's look-ahead publishes it once the pass has consumed the
// spooled copy of that arrival. Consuming that copy is no progress (its live copy is published), so it
// must not end the pass before the look-ahead's publication.
func TestDrainClientSpools_ALinePublishedOrRetiredBehindABlockedHeadEndsASpentPass(t *testing.T) {
	for _, how := range []string{"published as it is read", "retired as it is read", "published by the look-ahead"} {
		t.Run(how, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := dd.drainConfig()
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
				consumed = func() bool {
					j, err := dd.deliveryJournal()
					require.NoError(t, err)
					denied, err := j.terminalDenied(lease)
					return err == nil && denied
				}
			case "published by the look-ahead":
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
	dr := newDrainer(dd.drainConfig())
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
// the next pass itself when its budget ended one (passLeftWork). Every client spool's sync outlasts the
// budget here. The first pass publishes the line behind the blocked head and stops on its budget after
// that progress, so it asks again. The next only consumes that line again: it must finish the spool
// and must not ask again. Before the fix it stopped on its budget and asked again after every such
// pass, for as long as the host stayed slow. Once a fresh capture lands in a later spool, the next
// requested pass reaches it past the blocked spool and publishes it.
func TestDrain_ARequestedPassIsNotEndedByReconsumingALineBehindABlockedHead(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(dd.drainConfig())
	held := slowSpoolSyncs(dr)
	dd.drain.Store(dr)
	blocked := blockedSpoolHead(t, dd, root, "sess-requested-reconsume-stuck", 0)
	behind := liveOrderTool(dd, root, "sess-requested-reconsume-other", 7)
	writeHookSpool(t, root, "client-8821.ndjson", blocked, behind)
	ctx := context.Background()

	dd.requestedDrainPass(ctx)
	require.Positive(t, held.Load(), "fixture: the pass's sync of the spool was held past its budget")
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first pass published the line behind the head")
	require.Equal(t, 1, len(dd.ing.drainKick), "fixture: its budget ended it after that progress, so it asks again")
	<-dd.ing.drainKick // the requester takes it, as drainOnRequest does

	dd.requestedDrainPass(ctx)
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
// idle drain, which RunOnce runs under a pass budget too (registerPaced). Every client spool's sync
// outlasts the budget here. The first idle pass publishes the line behind the blocked head and its
// budget ends it there, before the next spool. The second only consumes that line again, and must go
// on to publish the next spool's capture. Before the fix every idle pass stopped where the first did.
func TestIdleDrain_AnIdlePassIsNotEndedByReconsumingALineBehindABlockedHead(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(dd.drainConfig())
	held := slowSpoolSyncs(dr)
	dd.drain.Store(dr)
	blocked := blockedSpoolHead(t, dd, root, "sess-idle-reconsume-stuck", 0)
	behind := liveOrderTool(dd, root, "sess-idle-reconsume-other", 7)
	writeHookSpool(t, root, "client-8831.ndjson", blocked, behind)
	fresh := liveOrderTool(dd, root, "sess-idle-reconsume-later", 50)
	writeHookSpool(t, root, "client-8832.ndjson", fresh)
	ctx := context.Background()

	ran, err := dd.idle.RunOnce(ctx, idleRunBudget)
	require.NoError(t, err)
	require.Contains(t, ran, idleTaskDrain, "fixture: the idle pass ran the drain")
	require.Positive(t, held.Load(), "fixture: the pass's sync of the spool was held past its budget")
	require.True(t, spoolWatchPublished(dd, behind.Nonce), "fixture: the first idle pass published the line behind the head")
	require.False(t, spoolWatchPublished(dd, fresh.Nonce), "fixture: and its budget ended it there, before the next spool")

	ran, err = dd.idle.RunOnce(ctx, idleRunBudget)
	require.NoError(t, err)
	require.Contains(t, ran, idleTaskDrain, "fixture: the idle pass ran the drain")
	require.True(t, spoolWatchPublished(dd, fresh.Nonce),
		"an idle pass that only consumed again the line behind the blocked head went on and published the "+
			"next spool's capture")
	require.False(t, spoolWatchPublished(dd, blocked.Nonce), "control: the blocked head never publishes")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-8831.ndjson"),
		"nothing of the blocked spool is lost: it stays for a drain that can publish it")
}

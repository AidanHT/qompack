package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.15. Claude Code gives a plugin's SessionEnd hooks one SHARED 1.5 s budget and
// cancels a hook still running when it runs out; a timeout set on a plugin-provided hook does not
// raise it (plans/sdd/V6-closeout/packaging/evidence/live-s{1,2}-*/stderr.txt: "SessionEnd hook
// [... qompack.exe flush] failed: Hook cancelled", in both live sessions). The flush route did the
// whole of the session's end — settle, SessionEnd, marker, sketches, drain — before it answered.
//
// The flush is now answered once it is DURABLE — its line in the session's WAL and leased, what an
// observe event's ACK promises — and the session is ended on a goroutine of its own, still ordered
// after every earlier arrival of the session (settleSession). A flush the host cancels, or a daemon
// that dies before the end runs, loses nothing: the durable line is replayed by the next drain.

// flushAsyncRequest is the SessionEnd hook's request as the hook client now sends it: fire-and-forget
// (an ACK, no reply), with the delivery nonce every hook carries.
func flushAsyncRequest(dd *daemon, root string, sess core.SessionID, nonce string) ipc.Request {
	return ipc.Request{
		Op: ipc.OpFlush, Session: sess, TS: core.NowMilli(dd.clk), Nonce: nonce,
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: sess, CWD: root},
	}
}

// heldSessionEnd wraps a daemon's SessionEnd seam: every call is counted, signals entered, and waits
// for release (or for its context to end) before it runs the real SessionEnd.
type heldSessionEnd struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func holdSessionEnd(dd *daemon) *heldSessionEnd {
	h := &heldSessionEnd{entered: make(chan struct{}, 8), release: make(chan struct{})}
	real := dd.svc.SessionEnd
	dd.svc.SessionEnd = func(ctx context.Context, e hookio.Event) error {
		h.calls.Add(1)
		h.entered <- struct{}{}
		select {
		case <-h.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return real(ctx, e)
	}
	return h
}

func (h *heldSessionEnd) open() { h.once.Do(func() { close(h.release) }) }

// flushAsyncDaemon is laneTestDaemon with its drainer installed and a held SessionEnd seam. The
// cleanup releases the seam and joins every session end still running before the store, the WAL
// handles and the lock are closed by the cleanups registered earlier.
func flushAsyncDaemon(t *testing.T) (*daemon, *heldSessionEnd, string) {
	t.Helper()
	dd, _, root := laneTestDaemon(t)
	dd.drain.Store(newDrainer(dd.drainConfig()))
	hold := holdSessionEnd(dd)
	t.Cleanup(func() {
		hold.open()
		ctx, cancel := context.WithTimeout(context.Background(), liveOrderBound)
		defer cancel()
		dd.awaitSessionEnds(ctx)
	})
	return dd, hold, root
}

// flushAsyncAwait waits for every session end the daemon started to finish.
func flushAsyncAwait(t *testing.T, dd *daemon) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveOrderBound)
	defer cancel()
	require.True(t, dd.awaitSessionEnds(ctx), "an accepted session end never finished")
}

// flushAsyncDispatch sends req on its own goroutine and returns its answer, failing the test if the
// answer does not come while the session's SessionEnd is still held.
func flushAsyncDispatch(t *testing.T, dd *daemon, req ipc.Request) ipc.Response {
	t.Helper()
	answered := make(chan ipc.Response, 1)
	go func() { answered <- dd.dispatchOp(context.Background(), req) }()
	select {
	case resp := <-answered:
		return resp
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the flush did not answer while its SessionEnd was still running: the hook "+
			"would outlive the host's 1.5 s SessionEnd budget and be cancelled")
		return ipc.Response{}
	}
}

// flushAsyncWALHasFlush reports whether sess's WAL segment holds a flush line carrying nonce.
func flushAsyncWALHasFlush(t *testing.T, root string, sess core.SessionID, nonce string) bool {
	t.Helper()
	f, err := os.Open(paths.Long(walPath(paths.Of(root).Spool, sess, 0)))
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), ipc.MaxLineBytes+1)
	for sc.Scan() {
		req, derr := ipc.DecodeRequest(sc.Bytes())
		if derr == nil && req.Op == ipc.OpFlush && req.Nonce == nonce {
			return true
		}
	}
	require.NoError(t, sc.Err())
	return false
}

// TestFlush_AnswersOnceDurableNotAfterSessionEnd is the C1.15 regression: the flush answers while
// SessionEnd is still running — before, the answer waited for the whole end — and by the time it has
// answered its request is durable: in the session's WAL, and leased. The session is then ended once,
// the flush reaches the committed frontier, and its recovery marker is cleared.
func TestFlush_AnswersOnceDurableNotAfterSessionEnd(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	const sess core.SessionID = "sess-flush-async"
	req := flushAsyncRequest(dd, root, sess, orderNonce(40))

	resp := flushAsyncDispatch(t, dd, req)
	require.True(t, resp.OK, "a durably accepted flush is acknowledged: %q", resp.Err)

	require.True(t, flushAsyncWALHasFlush(t, root, sess, req.Nonce),
		"the answer promises durability: the flush line is in the session's WAL before it goes out")
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	lease, held, err := j.leaseHeld(req.Nonce)
	require.NoError(t, err)
	require.True(t, held, "the flush is leased before it is acknowledged, as an observe event is")
	require.False(t, j.acknowledged(lease.Delivery), "nothing is published before the session has ended")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess,
		"an acknowledged flush whose end has not finished is recorded as needing recovery before the answer")

	hold.open()
	flushAsyncAwait(t, dd)
	require.Equal(t, int32(1), hold.calls.Load(), "the session is ended exactly once")
	require.True(t, j.acknowledged(lease.Delivery), "the ended flush reaches the committed frontier")
	sr, err = LoadSessionRecovery(root)
	require.NoError(t, err)
	require.NotContains(t, sr.Sessions, sess, "a finished session end leaves no recovery marker")
}

// TestFlush_AReplyCallerStillWaitsForTheSessionEnd: a Reply flush — an older hook client, an
// operator, a test — asks for the end's own answer, so it still gets it only once the session has
// ended. Only the fire-and-forget hook stopped waiting.
func TestFlush_AReplyCallerStillWaitsForTheSessionEnd(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	const sess core.SessionID = "sess-flush-reply"
	req := flushAsyncRequest(dd, root, sess, orderNonce(41))
	req.Reply = true

	answered := make(chan ipc.Response, 1)
	go func() { answered <- dd.dispatchOp(context.Background(), req) }()
	select {
	case <-hold.entered:
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the session end never reached SessionEnd")
	}
	select {
	case resp := <-answered:
		require.FailNow(t, "a Reply flush answered while its SessionEnd was still held", "%+v", resp)
	default:
	}
	hold.open()
	select {
	case resp := <-answered:
		require.True(t, resp.OK, resp.Err)
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the Reply flush never answered after its SessionEnd finished")
	}
}

// TestFlush_AnAcceptedFlushSurvivesACrashBeforeTheEnd: the daemon dies after acknowledging the flush
// and before ending the session (SessionEnd is held, then the process is gone). What is on disk at
// that moment is all a restarted daemon has, and its startup drain replays the flush from it.
func TestFlush_AnAcceptedFlushSurvivesACrashBeforeTheEnd(t *testing.T) {
	dd, _, root := flushAsyncDaemon(t)
	const sess core.SessionID = "sess-flush-crash"
	resp := flushAsyncDispatch(t, dd, flushAsyncRequest(dd, root, sess, orderNonce(42)))
	require.True(t, resp.OK, resp.Err)

	// The crash: only the spool as it stands now survives, into the next daemon's project.
	restarted := t.TempDir()
	spool := paths.Of(restarted).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.NoError(t, err)
	for _, e := range entries {
		b, rerr := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Spool, e.Name())))
		require.NoError(t, rerr)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, e.Name())), b, 0o600))
	}

	var replayed []ipc.Op
	dr := newDrainer(DrainConfig{Root: restarted, Clock: dd.clk, Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		replayed = append(replayed, r.Op)
		return ipc.Response{OK: true}
	}})
	_, err = dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, []ipc.Op{ipc.OpFlush}, replayed, "the restarted daemon's drain replays the accepted flush")
}

// TestFlush_TheHookSpooledCopyOfAnAcceptedFlushEndsTheSessionOnce: a hook whose ACK arrived too late
// spools the same request (same nonce) it already delivered. The drain absorbs that copy through the
// flush's own lease and frontier record; it must not end the session a second time.
func TestFlush_TheHookSpooledCopyOfAnAcceptedFlushEndsTheSessionOnce(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	const sess core.SessionID = "sess-flush-duplicate"
	req := flushAsyncRequest(dd, root, sess, orderNonce(43))

	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK, resp.Err)
	flushAsyncAwait(t, dd)
	require.Equal(t, int32(1), hold.calls.Load())

	writeSpoolLines(t, root, "client-4242.ndjson", req)
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), hold.calls.Load(), "the spooled copy of an ended flush must not end the session again")
	require.NoFileExists(t, filepath.Join(paths.Of(root).Spool, "client-4242.ndjson"), "the absorbed copy is released")
}

// TestFlush_ADrainedFlushSettlesUpToItsOwnArrival: a flush a drain replays (a restart, or a hook that
// fell back to its client spool) is itself a leased arrival of its session. Its settle must wait only
// for the arrivals BEFORE it: counting its own, still unacknowledged while it runs, made every drained
// flush report that SessionEnd ran ahead of its session (l0_flush_unsettled) when nothing had.
func TestFlush_ADrainedFlushSettlesUpToItsOwnArrival(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	const sess core.SessionID = "sess-flush-drained"
	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, orderNonce(44), "p0"))
	drainRing(t, dd)

	writeSpoolLines(t, root, "client-4343.ndjson", flushAsyncRequest(dd, root, sess, orderNonce(45)))
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), hold.calls.Load(), "the drained flush ended the session")
	require.Zero(t, dd.m.Counter(counterFlushUnsettled).Value(),
		"every arrival before the drained flush was published, so its SessionEnd ran after all of them")
}

// TestStop_FinishesAnAcceptedSessionEndBeforeItReturns: a session end still running when Stop begins
// gets its own bounded window to finish, and Stop does not return until it has — nothing it writes
// races the store's close, and nothing of the flush is left undone by a clean shutdown.
func TestStop_FinishesAnAcceptedSessionEndBeforeItReturns(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	const sess core.SessionID = "sess-flush-stop"
	req := flushAsyncRequest(dd, root, sess, orderNonce(46))
	resp := flushAsyncDispatch(t, dd, req)
	require.True(t, resp.OK, resp.Err)
	select {
	case <-hold.entered:
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the session end never reached SessionEnd")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(context.Background()) }()
	select {
	case <-stopped:
		require.FailNow(t, "Stop returned while an accepted session end was still running")
	case <-time.After(stopJoinProbe):
	}
	hold.open()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(liveOrderBound):
		require.FailNow(t, "Stop never returned after the session end finished")
	}
	require.Equal(t, int32(1), hold.calls.Load(), "the session was ended exactly once")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.NotContains(t, sr.Sessions, sess, "the session end finished before Stop returned")
}

// TestSessionRecovery_ConcurrentEndsLoseNoMarker: the recovery-needed set is one file every session
// end rewrites whole (markRecoveryNeeded, clearRecoveryNeeded: read, change one entry, write). Since
// C1.15 the ends run on goroutines of their own, beside the flush route that marks the next one, so
// two sessions' ends overlap as a matter of course. Each rewrite must see the one before it: a lost
// update either resurrects a cleared session — a finished end reported as needing recovery for good —
// or drops a marker for an end still running.
func TestSessionRecovery_ConcurrentEndsLoseNoMarker(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	const sessions = 24
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := core.SessionID(fmt.Sprintf("sess-recovery-%02d", i))
			dd.markRecoveryNeeded(sess, recoveryStageBegin, 0)
			dd.markRecoveryNeeded(sess, recoveryStageSessionEnd, 0)
			if i%2 == 0 {
				dd.clearRecoveryNeeded(sess)
			}
		}()
	}
	wg.Wait()

	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	var want []core.SessionID
	for i := 1; i < sessions; i += 2 {
		want = append(want, core.SessionID(fmt.Sprintf("sess-recovery-%02d", i)))
	}
	got := make([]core.SessionID, 0, len(sr.Sessions))
	for s := range sr.Sessions {
		got = append(got, s)
	}
	require.ElementsMatch(t, want, got,
		"exactly the sessions whose ends did not finish are marked: no cleared one resurrected, no marker lost")
}

// TestFlush_AFlushAcknowledgedDuringShutdownIsMarkedForRecovery: a flush that arrives once Stop has
// begun joining the session ends is still made durable and acknowledged, but no end is started for it
// in this process — Stop's own drain or the next daemon's replays its line. Until that happens the
// session must be on record as needing recovery, as it is for every acknowledged flush whose end has
// not finished: the marker is written before the answer, not by the end.
func TestFlush_AFlushAcknowledgedDuringShutdownIsMarkedForRecovery(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	dd.ends.close() // Stop has begun joining the session ends
	const sess core.SessionID = "sess-flush-at-stop"
	req := flushAsyncRequest(dd, root, sess, orderNonce(47))

	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK, "the flush is durable, so it is acknowledged: %q", resp.Err)
	require.True(t, flushAsyncWALHasFlush(t, root, sess, req.Nonce), "its line is in the WAL for the next drain")
	require.Zero(t, hold.calls.Load(), "no session end is started once Stop is joining them")
	require.Equal(t, int64(1), dd.m.Counter(counterSessionEndRefused).Value())
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess,
		"an acknowledged flush whose end has not run is on record as needing recovery")
	require.Equal(t, recoveryStageBegin, sr.Sessions[sess].Stage)
}

// TestStop_IsNotHeldBehindASessionEndsDrain: a session end's last step is a drain, and a drain holds
// the drainer's mutex for its whole pass. Stop joins Run's own goroutines (runWG) before it joins the
// session ends, and two of those goroutines — the drains the lanes ask for and the client-spool
// watcher — take that mutex without watching any context. So one of them waiting on an end's drain
// held Stop's join until the drain finished on its own: its session end is cancelled only once Stop
// reaches them, and a drain line is bounded only by its own drainLineDeadline, line after line. Stop
// must give the ends their grace from the moment it begins, so the drain it waits behind is cancelled
// once the grace is over, and Stop stays inside its bound however long the drain had left to run.
func TestStop_IsNotHeldBehindASessionEndsDrain(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	dd.sessionEndGrace = stopJoinProbe

	// Another session's spooled line whose publication holds the drain until the drain's context ends.
	// Once a cancellation has reached it — the grace is over — it publishes at once, as a handler does.
	const stuck core.SessionID = "sess-stop-held-by-drain"
	entered := make(chan struct{}, 8)
	released := make(chan struct{})
	var releaseOnce sync.Once
	cfg := dd.drainConfig()
	real := cfg.Dispatch
	cfg.Dispatch = func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Session == stuck {
			select {
			case <-released:
			default:
				entered <- struct{}{}
				<-ctx.Done()
				if errors.Is(ctx.Err(), context.Canceled) {
					releaseOnce.Do(func() { close(released) })
				}
				return ipc.Response{Err: ctx.Err().Error()}
			}
		}
		return real(ctx, req)
	}
	dd.drain.Store(newDrainer(cfg))
	writeSpoolLines(t, root, "client-9191.ndjson", spD3Prompt(dd, root, stuck, orderNonce(60), "p0"))

	const sess core.SessionID = "sess-stop-drain-end"
	resp := flushAsyncDispatch(t, dd, flushAsyncRequest(dd, root, sess, orderNonce(61)))
	require.True(t, resp.OK, resp.Err)
	select {
	case <-entered: // the session end's final drain is publishing the stuck line, holding the mutex
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the session end's final drain never reached the spooled line")
	}

	// Run's client-spool watcher, a runWG member under Run's context, waiting for its own pass.
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	dd.runCancelMu.Lock()
	dd.runCancel = runCancel
	dd.runCancelMu.Unlock()
	dd.goRun(func() { _, _ = dd.drain.Load().DrainClientSpools(runCtx) })

	// The grace, the abandon window, and room for a loaded machine: well short of the drain line's
	// own deadline, which is all that ended the wait before.
	bound := dd.sessionEndGrace + sessionEndAbandonAfter + 2*time.Second
	require.Less(t, bound, drainLineDeadline, "precondition: the bound tells the two outcomes apart")
	began := time.Now()
	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(liveOrderBound):
		require.FailNow(t, "Stop never returned")
	}
	took := time.Since(began)
	require.Less(t, took, bound,
		"Stop waited %s behind a session end's drain: the grace must run from Stop's start, not after runWG", took)
}

// TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget: a flush only a hook's client spool
// holds — its dial failed while the daemon was serving — is replayed by a drain, and the drains that
// run while the daemon serves are budgeted: the client-spool watcher's pass, the drains the lanes ask
// for and the idle drain get idleRunBudget, and any one line drainLineDeadline. Ended inside the pass,
// the session's settle and SessionEnd got whatever was left of that budget, and a SessionEnd longer
// than that could never finish through those drains. The flush must be ended as an acknowledged live
// one is — on its own goroutine, with no budget but the end's own — and acknowledged only once its
// SessionEnd has really run.
func TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	dd.drainsEndSessions.Store(true) // serving: Run sets it once its startup drain is done
	var finished atomic.Int32
	held := dd.svc.SessionEnd
	dd.svc.SessionEnd = func(ctx context.Context, e hookio.Event) error {
		err := held(ctx, e)
		if err == nil {
			finished.Add(1)
		}
		return err
	}

	const sess core.SessionID = "sess-flush-pass-budget"
	req := flushAsyncRequest(dd, root, sess, orderNonce(62))
	writeSpoolLines(t, root, "client-6262.ndjson", req) // the flush only its hook's client spool holds

	// A client-spool pass, as the watcher runs one, with its budget shortened.
	pass, cancel := context.WithTimeout(context.Background(), stopJoinProbe)
	defer cancel()
	_, _ = dd.drain.Load().DrainClientSpools(pass)
	select {
	case <-hold.entered:
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the replayed flush never reached SessionEnd")
	}
	<-pass.Done() // the pass's budget is spent while SessionEnd is still running

	hold.open()
	flushAsyncAwait(t, dd)
	require.Equal(t, int32(1), finished.Load(),
		"the replayed flush's SessionEnd ran to completion, once, and was not cut off at the pass's budget")
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	lease, leased, err := j.leaseHeld(req.Nonce)
	require.NoError(t, err)
	require.True(t, leased)
	require.True(t, j.acknowledged(lease.Delivery), "the ended flush reaches the committed frontier")
	require.Eventually(t, func() bool { return spoolWatchGone(root, "client-6262.ndjson") },
		liveOrderBound, liveOrderTick, "the absorbed client spool is released")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.NotContains(t, sr.Sessions, sess, "a finished session end leaves no recovery marker")
}

// TestFlushRoute_AReplayedSessionEndCutShortIsNotAcknowledged: a flush a drain replays inline — the
// startup drain, Stop's drain — is acknowledged by that drain when the route answers OK. A session end
// its context cut short, before or during SessionEnd, has not ended the session, and must not answer
// OK: a drain whose pass outlived the line's own drainLineDeadline, the startup drain's, acknowledged
// it, and SessionEnd never ran for that session.
func TestFlushRoute_AReplayedSessionEndCutShortIsNotAcknowledged(t *testing.T) {
	dd, _, root := flushAsyncDaemon(t) // SessionEnd stays held, as a slow one would be
	const sess core.SessionID = "sess-flush-cut-short"
	ctx, cancel := context.WithTimeout(context.Background(), stopJoinProbe)
	defer cancel()
	resp := dd.flushRoute(ctx, flushAsyncRequest(dd, root, sess, orderNonce(63)), false)
	require.False(t, resp.OK, "a session end its context cut short answered OK, so its drain would acknowledge it")
	require.NotEmpty(t, resp.Err)
}

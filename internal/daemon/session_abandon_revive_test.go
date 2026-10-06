package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// UAT-09 O-1 (candidate 7, plans/sdd/V6-closeout/live/rerun-c7/UAT-09/notes.txt): with
// runtime.daemon.idleExitSeconds at 30, the day log said "daemon: ending abandoned session; no
// SessionEnd arrived" (silentMs=30099) while the host session was still live, and the lane recorded
// that SessionEnd never reached the daemon. The store says otherwise: the last turn's
// UserPromptSubmit was followed by a 34.5 s reply with no tool call, so no hook reached the daemon
// for longer than the window; its Stop then revived the session, and its SessionEnd ran the
// observer's end of session a second later (index/segments.jsonl closes segment 2 with the
// observer's feature set, gap_seconds 1.021, and the day log's "observer: gc" line, which only
// OnSessionEnd writes, follows at 19:25:51.205).
//
// This row replays that timeline against the daemon's own handlers and idle-exit decision on a fake
// clock, so the disposition rests on the code and not on a reading of the evidence: the silence
// sweep ends the quiet session, the next hook revives it, the flush ends it through SessionEnd
// exactly once, and the daemon's idle exit then follows one window later.
func TestIdleExit_AnAbandonedSessionRevivedByItsStopStillEndsThroughSessionEnd(t *testing.T) {
	const (
		sess core.SessionID = "sess-long-final-turn"
		// The UAT-09 lane's QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS.
		idleExitSeconds = 30
	)
	window := idleExitSeconds * time.Second
	clk := newFakeClock(epoch)
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, func(o *Options) {
		o.Clock = clk
		o.Cfg.Runtime.Daemon.IdleExitSeconds = idleExitSeconds
	})
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	liveOrderWorkers(t, dd, 1, dd.runIngested)

	var ends atomic.Int32
	real := dd.svc.SessionEnd
	require.NotNil(t, real, "the wired observer binds the SessionEnd seam")
	dd.svc.SessionEnd = func(ctx context.Context, e hookio.Event) error {
		if e.SessionID == sess {
			ends.Add(1)
		}
		return real(ctx, e)
	}

	ctx := context.Background()
	hook := func(op ipc.Op, name string, nonce int) ipc.Request {
		return ipc.Request{
			Op: op, Session: sess, TS: core.NowMilli(clk), Nonce: orderNonce(nonce),
			Event: &hookio.Event{HookEventName: name, SessionID: sess, CWD: root, Prompt: "quote the block"},
		}
	}
	state := func() SessionState {
		t.Helper()
		// Snapshot copies under the registry's lock; Get's shared pointer would race the workers.
		for _, s := range dd.registry.Snapshot() {
			if s.ID == sess {
				return s
			}
		}
		require.FailNow(t, "the session is not tracked")
		return SessionState{}
	}
	var zeroLiveSince time.Time

	// SessionStart registered the session; the last turn's prompt is its last hook for a while.
	dd.registry.Ensure(&hookio.Event{SessionID: sess, CWD: root}, core.NowMilli(clk))
	prompt := hook(ipc.OpObservePrompt, "UserPromptSubmit", 1)
	prompt.Reply = true
	require.True(t, dd.dispatchOp(ctx, prompt).OK)
	lastPrompt := core.NowMilli(clk)

	// The reply sends no hook. One idle tick past the window, the sweep ends the session for silence
	// (the WARN the lane saw, silentMs=30099); the zero-live countdown starts on the same tick.
	clk.Advance(window + 99*time.Millisecond)
	require.False(t, dd.idleExitDue(core.NowMilli(clk), &zeroLiveSince), "the countdown has only started")
	require.Zero(t, dd.registry.Live(), "the silence sweep ended the quiet session")
	abandoned := state()
	require.True(t, abandoned.abandoned, "ended for silence, not by its own SessionEnd")
	require.Equal(t, int64(30_099), int64(abandoned.EndedTS-lastPrompt))

	// The turn's Stop arrives inside the countdown and revives the session.
	clk.Advance(4400 * time.Millisecond)
	require.True(t, dd.dispatchOp(ctx, hook(ipc.OpObserveStop, "Stop", 2)).OK)
	require.Equal(t, 1, dd.registry.Live(), "the Stop proves the session live again")
	require.False(t, dd.idleExitDue(core.NowMilli(clk), &zeroLiveSince), "a live session holds the daemon")
	require.True(t, zeroLiveSince.IsZero(), "the revival cleared the countdown")

	// SessionEnd one second later: the flush ends the session through the observer's SessionEnd.
	clk.Advance(1021 * time.Millisecond)
	flush := hook(ipc.OpFlush, "SessionEnd", 3)
	flush.Reply = true // the end's own answer, so the row needs no wait of its own
	resp := dd.dispatchOp(ctx, flush)
	require.True(t, resp.OK, "the flush ends the revived session: %q", resp.Err)
	require.Equal(t, int32(1), ends.Load(), "SessionEnd reached the observer exactly once")
	ended := state()
	require.False(t, ended.Live)
	require.False(t, ended.abandoned, "the session is ended by its own SessionEnd, not by the sweep")
	require.Equal(t, core.NowMilli(clk), ended.EndedTS, "the end is the flush's, not the sweep's")

	// With no live session left, the daemon exits one window later, as the lane's daemon did.
	require.False(t, dd.idleExitDue(core.NowMilli(clk), &zeroLiveSince))
	clk.Advance(window)
	require.True(t, dd.idleExitDue(core.NowMilli(clk), &zeroLiveSince), "the idle exit follows one window later")
}

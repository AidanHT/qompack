package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// liveWALIdleExitSeconds is the idle-exit window the idle-exit tests configure. Its value is
// immaterial — the fake clock is advanced in fractions of it — so it is simply a round minute.
const liveWALIdleExitSeconds = 60

// TestIdleExitWaitsForAnUnregisteredSessionThatKeepsSendingTraffic pins M-2 for a daemon that never
// saw the session's SessionStart: Run's idle tick exited after one window with Live()==0 while the
// session kept sending hot-path traffic, and the next hook paid a cold start. The countdown must
// stay unarmed for as long as the traffic continues, and must still run out once the session goes
// silent past the window (EndAbandoned) or ends (SessionEnd).
func TestIdleExitWaitsForAnUnregisteredSessionThatKeepsSendingTraffic(t *testing.T) {
	t.Parallel()
	const window = liveWALIdleExitSeconds * time.Second
	for _, ending := range []string{"abandoned", "ended"} {
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-busy-after-restart")
			dd, clk := liveWALDaemon(t, func(o *Options) { o.Cfg.Runtime.Daemon.IdleExitSeconds = liveWALIdleExitSeconds })
			ctx := context.Background()
			var zeroLiveSince time.Time
			tick := func() bool { return dd.idleExitDue(core.NowMilli(clk), &zeroLiveSince) }

			// Three whole windows of continuous traffic: a delivery and an idle tick every half window.
			const halfWindows = 6
			for i := 0; i < halfWindows; i++ {
				require.True(t, dd.dispatchOp(ctx, liveWALTool(dd, sess)).OK)
				require.False(t, tick(), "tick %d: a session still sending traffic must not look idle", i)
				clk.Advance(window / 2)
			}

			switch ending {
			case "abandoned":
				clk.Advance(window / 2) // a whole window of silence since the last delivery
				require.False(t, tick(), "abandoning the silent session only starts the countdown")
				require.False(t, dd.registry.IsLive(sess), "silent past the window, the session is abandoned")
			case "ended":
				require.True(t, dd.dispatchOp(ctx, liveWALSessionEnd(dd, sess)).OK)
				require.False(t, tick(), "SessionEnd only starts the countdown")
			}
			clk.Advance(window)
			require.True(t, tick(), "one window with no live session: the daemon exits")
		})
	}
}

// breachState is the part of breachDetector a reset would move.
type breachState struct{ n, breaches, clean int }

func breachSnapshot(b *breachDetector) breachState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return breachState{n: b.n, breaches: b.breaches, clean: b.clean}
}

// TestTrafficRegistrationLeavesHotSubmodeAndBreachDetectorAlone: a session that reaches the
// registry through its traffic has already sent requests — it may be the very session that breached
// — so registering it must not do what a brand-new session's SessionStart does (reset the
// daemon-wide hot submode to sync, reset the breach detector's ring). Nor may its SessionStart, when
// one does arrive later (a compaction's, say): the session is not new.
func TestTrafficRegistrationLeavesHotSubmodeAndBreachDetectorAlone(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-breached-before-restart")
	dd, _ := liveWALDaemon(t, nil)
	ctx := context.Background()

	dd.registry.SetHotMode(ipc.HotSpool, "breach")
	for i := 0; i < sampleWindow-1; i++ {
		tr, closed := dd.breach.Observe(20 * time.Millisecond)
		require.Equal(t, NoTransition, tr)
		require.False(t, closed)
	}
	before := breachSnapshot(dd.breach)
	require.Equal(t, sampleWindow-1, before.n)

	req := liveWALTool(dd, sess)
	req.Reply = true // a reply request is answered, not NAKed, in the spool submode
	require.True(t, dd.dispatchOp(ctx, req).OK)
	require.True(t, dd.registry.IsLive(sess), "hot-path traffic proves the session live")
	require.Equal(t, ipc.HotSpool, dd.registry.HotMode(), "registering by traffic must not reset the hot submode")
	require.Equal(t, "breach", dd.registry.HotReason())
	require.Equal(t, before, breachSnapshot(dd.breach), "registering by traffic must not reset the breach detector")

	require.True(t, dd.dispatchOp(ctx, liveWALSessionStart(dd, sess)).OK)
	require.Equal(t, ipc.HotSpool, dd.registry.HotMode(), "a known session's SessionStart must not reset the hot submode")
	require.Equal(t, before, breachSnapshot(dd.breach), "a known session's SessionStart must not reset the breach detector")
}

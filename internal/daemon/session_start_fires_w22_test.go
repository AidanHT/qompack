package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// Audit 2's status findings #8 and #10 (wave 22, D67): two ways session_start.fires reached
// SevCritical and degraded a healthy project to passive recording on a store where every hook fired,
// each pinned with the genuine failure it must still report.
//
//   - #8: sessions opened at once on a project with no run/marker.json yet. An absent marker was
//     counted while the session it would come from was still running, so a third window failed.
//   - #10: a startup replayed from a spool after its own session's PreCompact found its own marker
//     and counted an absence.

// abandonAll ends every live session the way the idle tick does when a session goes silent for a
// whole window with no SessionEnd: registry bookkeeping only, no terminal-hook marker.
func abandonAll(dd *daemon) {
	dd.registry.EndAbandoned(core.NowMilli(dd.clk)+core.UnixMilli(time.Hour.Milliseconds()), 0)
}

// sessionEndRequest is a SessionEnd of sess fired at ts, answered once the end has run.
func sessionEndRequest(dd *daemon, sess core.SessionID, ts core.UnixMilli) ipc.Request {
	return ipc.Request{
		Op: ipc.OpFlush, Session: sess, Reply: true, TS: ts,
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: sess, CWD: dd.root},
	}
}

// requireFiresNotFailing requires session_start.fires to hold or wait, never fail, and the project to
// stay in full mode.
func requireFiresNotFailing(t *testing.T, dd *daemon, step string) contract.Result {
	t.Helper()
	r := reportOf(t, dd, contract.CSessionStartFires)
	require.True(t, r.OK, "%s: session_start.fires must not fail (observed %q)", step, r.Observed)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(), "%s: nothing broke a contract", step)
	return r
}

// TestSessionStartFires_ConcurrentFreshStartsStayFull is #8: three windows opened on a project that
// has never had a terminal hook, none of them ended or compacted. No session had a terminal hook
// due, so no absence is counted; then each of two of them compacts, and every reading holds.
func TestSessionStartFires_ConcurrentFreshStartsStayFull(t *testing.T) {
	dd := replayProbeDaemon(t)
	for _, s := range []core.SessionID{"sess-a", "sess-b", "sess-c"} {
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, s, "startup", "", "n-"+string(s))).OK)
		r := requireFiresNotFailing(t, dd, string(s)+" startup")
		require.NotEqual(t, contract.StandingPending, contract.StandingOf(r),
			"%s startup: no session has ended, so no marker was due (observed %q)", s, r.Observed)
		require.Zero(t, history(t, dd).StartsWithoutMarker, "%s startup counts no absence", s)
	}
	for _, s := range []core.SessionID{"sess-a", "sess-b"} {
		require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, s, "p-"+string(s))).OK)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, s, "compact", "", "c-"+string(s))).OK)
		r := requireFiresNotFailing(t, dd, string(s)+" compaction")
		require.Equal(t, contract.StandingHolding, contract.StandingOf(r),
			"%s compaction: its own restart holds, so the banner reads 0 pending (observed %q)", s, r.Observed)
		require.Equal(t, contract.StandingHolding, contract.StandingOf(statusRow(t, dd, contract.CSessionStartFires)),
			"%s compaction: status reads the same row", s)
	}
}

// TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails pins the other direction of #8: a
// session that is no longer running and left no marker is a real absence. Two in a row fail at
// critical severity and degrade the project, whether the daemon saw the session go silent (the idle
// tick's abandonment) or restarted and forgot it.
func TestSessionStartFires_AbsenceAfterAnEndedSessionStillFails(t *testing.T) {
	t.Run("abandoned", func(t *testing.T) {
		dd := replayProbeDaemon(t)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-a", "startup", "", "n-a")).OK)
		abandonAll(dd)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-b", "startup", "", "n-b")).OK)
		r := reportOf(t, dd, contract.CSessionStartFires)
		require.Equal(t, "marker-absent-once", r.Observed, "a ended with no marker: one absence")
		require.Equal(t, contract.StandingPending, contract.StandingOf(r))
		abandonAll(dd)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-c", "startup", "", "n-c")).OK)
		r = reportOf(t, dd, contract.CSessionStartFires)
		require.False(t, r.OK, "two consecutive sessions ended without a marker")
		require.Equal(t, contract.SevCritical, r.Severity)
		require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode())
	})
	t.Run("daemon restarted", func(t *testing.T) {
		root := t.TempDir()
		dd := replayProbeDaemonAt(t, root)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-a", "startup", "", "n-a")).OK)
		dd = replayProbeDaemonAt(t, root)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-b", "startup", "", "n-b")).OK)
		require.Equal(t, "marker-absent-once", reportOf(t, dd, contract.CSessionStartFires).Observed)
		dd = replayProbeDaemonAt(t, root)
		require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-c", "startup", "", "n-c")).OK)
		r := reportOf(t, dd, contract.CSessionStartFires)
		require.False(t, r.OK)
		require.Equal(t, contract.SevCritical, r.Severity)
		require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode())
	})
}

// TestSessionStartFires_ReplayedStartupAfterItsOwnPreCompact is #10, the audit's sequence: a
// startup whose live reply missed the client's deadline is replayed after its own session's
// PreCompact rewrote run/marker.json, while another session started in between. Twice is the
// critical failure the audit reached on a healthy store.
func TestSessionStartFires_ReplayedStartupAfterItsOwnPreCompact(t *testing.T) {
	dd := replayProbeDaemon(t)
	h := history(t, dd)
	h.SessionCount, h.LastSessionID = 1, "sess-z"
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(dd.root), h))
	require.NoError(t, contract.WriteMarker(dd.root, "sess-z", 1))

	a := startRequest(dd, "sess-a", "startup", "", "n-a")
	b := startRequest(dd, "sess-b", "startup", "", "n-b")
	a.TS, b.TS = a.TS-2000, b.TS-1000 // fired before every terminal hook below
	require.True(t, dd.dispatchOp(context.Background(), a).OK)
	require.True(t, dd.dispatchOp(context.Background(), b).OK)
	require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, "sess-a", "p-a")).OK)
	require.True(t, dd.drainDispatch(context.Background(), a).OK)
	requireFiresNotFailing(t, dd, "a's startup replayed")
	require.Zero(t, history(t, dd).StartsWithoutMarker, "a's own later PreCompact is no absence")
	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, "sess-a", "compact", "", "c-a")).OK)
	require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, "sess-b", "p-b")).OK)
	require.True(t, dd.drainDispatch(context.Background(), b).OK)
	requireFiresNotFailing(t, dd, "b's startup replayed")
	require.Zero(t, history(t, dd).StartsWithoutMarker, "b's own later PreCompact is no absence")
}

// TestSessionStartFires_ReplayedStartupAfterAnEndedSessionsMarkerWasOverwritten is #10 where the
// session the history last saw has ended: its SessionEnd wrote its marker, and the replayed session's
// own PreCompact then overwrote it. The replayed startup's own marker is newer than the startup, so
// it was written by that session's own later terminal hook and is no absence.
func TestSessionStartFires_ReplayedStartupAfterAnEndedSessionsMarkerWasOverwritten(t *testing.T) {
	dd := replayProbeDaemon(t)
	h := history(t, dd)
	h.SessionCount, h.LastSessionID = 1, "sess-z"
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(dd.root), h))
	require.NoError(t, contract.WriteMarker(dd.root, "sess-z", 1))

	a := startRequest(dd, "sess-a", "startup", "", "n-a")
	a.TS -= 3000
	require.True(t, dd.dispatchOp(context.Background(), a).OK)
	b := startRequest(dd, "sess-b", "startup", "", "n-b")
	b.TS -= 2000
	require.True(t, dd.dispatchOp(context.Background(), b).OK)
	resp := dd.dispatchOp(context.Background(), sessionEndRequest(dd, "sess-b", b.TS+500))
	require.True(t, resp.OK, resp.Err)
	require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, "sess-a", "p-a")).OK)
	require.True(t, dd.drainDispatch(context.Background(), a).OK)
	r := requireFiresNotFailing(t, dd, "a's startup replayed")
	require.NotEqual(t, contract.StandingPending, contract.StandingOf(r), "observed %q", r.Observed)
	require.Zero(t, history(t, dd).StartsWithoutMarker)
}

package contract_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// Audit 2's findings #8, #9 and #10 at the assertions themselves (wave 22). The daemon-level rows,
// which drive the same sequences through the session.start, checkpoint, prompt and flush routes,
// are in internal/daemon/session_start_fires_w22_test.go.

// liveSet is an Env.SessionLive over a fixed set of running sessions.
func liveSet(live ...core.SessionID) func(core.SessionID) bool {
	return func(s core.SessionID) bool {
		for _, l := range live {
			if l == s {
				return true
			}
		}
		return false
	}
}

// startFiresWith runs session_start.fires once for a SessionStart of sess with the given source, the
// way the daemon's session.start does, with the caller's live sessions and the start's hook time.
func startFiresWith(t *testing.T, root string, h *contract.SessionHistory, sess core.SessionID, source string,
	live func(core.SessionID) bool, startTS core.UnixMilli,
) contract.Result {
	t.Helper()
	r := assertionByID(t, contract.CSessionStartFires).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, SessionLive: live, StartTS: startTS,
		Event: hookio.Event{HookEventName: "SessionStart", SessionID: sess, Source: source},
	})
	h.SessionCount++
	return r
}

// TestSessionStartFires_NoAbsenceWhileThePriorSessionRuns is #8: on a project that has never had a
// terminal hook, a start whose predecessor is still running counts nothing — that session's marker
// is not due — reads prior-session-live (nothing to judge, never pending), and leaves LastSessionID
// naming the running session, whose terminal hook the next start still awaits.
func TestSessionStartFires_NoAbsenceWhileThePriorSessionRuns(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{}
	r := startFiresWith(t, root, h, "sess-a", "startup", liveSet("sess-a"), 0)
	require.Equal(t, "first-session", r.Observed)

	for _, s := range []core.SessionID{"sess-b", "sess-c"} {
		r = startFiresWith(t, root, h, s, "startup", liveSet("sess-a", "sess-b", "sess-c"), 0)
		require.True(t, r.OK, "%s", s)
		require.Equal(t, "prior-session-live", r.Observed, "%s", s)
		require.Equal(t, contract.StandingIdle, contract.StandingOf(r), "%s: nothing to judge, not pending", s)
		require.Zero(t, h.StartsWithoutMarker, "%s counts no absence", s)
		require.Equal(t, core.SessionID("sess-a"), h.LastSessionID, "%s: the running session is still awaited", s)
	}

	// The awaited session ends with its marker: the next start finds it.
	require.NoError(t, contract.WriteMarker(root, "sess-a", 1))
	r = startFiresWith(t, root, h, "sess-d", "startup", liveSet("sess-b", "sess-c", "sess-d"), 0)
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, core.SessionID("sess-d"), h.LastSessionID)
}

// TestSessionStartFires_AbsenceOnceThePriorSessionEndedStillCounts pins the other direction: once
// the awaited session is no longer running and left no marker, the absence counts, and two
// consecutive ones fail at critical severity.
func TestSessionStartFires_AbsenceOnceThePriorSessionEndedStillCounts(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{}
	startFiresWith(t, root, h, "sess-a", "startup", liveSet("sess-a"), 0)
	r := startFiresWith(t, root, h, "sess-b", "startup", liveSet("sess-a", "sess-b"), 0)
	require.Equal(t, "prior-session-live", r.Observed)

	// a ends without a marker while b runs: the next start counts a's absence.
	r = startFiresWith(t, root, h, "sess-c", "startup", liveSet("sess-b", "sess-c"), 0)
	require.True(t, r.OK)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, contract.StandingPending, contract.StandingOf(r))
	require.Equal(t, 1, h.StartsWithoutMarker)
	require.Equal(t, core.SessionID("sess-c"), h.LastSessionID)

	// While c runs, a start keeps the counted absence pending and adds nothing.
	r = startFiresWith(t, root, h, "sess-d", "startup", liveSet("sess-b", "sess-c", "sess-d"), 0)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, 1, h.StartsWithoutMarker)

	// c ends without a marker too: two consecutive sessions, a failure.
	r = startFiresWith(t, root, h, "sess-e", "startup", liveSet("sess-b", "sess-d", "sess-e"), 0)
	require.False(t, r.OK)
	require.Equal(t, contract.SevCritical, r.Severity)
	require.Equal(t, 2, h.StartsWithoutMarker)

	// A failure stays a failure while the session it now awaits runs.
	r = startFiresWith(t, root, h, "sess-f", "startup", liveSet("sess-e", "sess-f"), 0)
	require.False(t, r.OK)
	require.Equal(t, contract.StandingFailing, contract.StandingOf(r))
}

// TestSessionStartFires_OwnMarkerWrittenAfterTheStartIsARestart is #10: a startup evaluated after
// its own session's terminal hook — replayed from a spool — finds a marker naming itself that was
// written after the host fired it. That is the session's own later hook, no absence, whatever
// LastSessionID names. A marker naming the session written BEFORE the start fired, or one the start
// time cannot place (unknown StartTS), still counts.
func TestSessionStartFires_OwnMarkerWrittenAfterTheStartIsARestart(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	for _, source := range []string{"startup", "clear"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			h := &contract.SessionHistory{SessionCount: 3, LastSessionID: "sess-b"}
			require.NoError(t, contract.WriteMarker(root, "sess-a", 5000))
			r := startFiresWith(t, root, h, "sess-a", source, nil, 4000)
			require.True(t, r.OK)
			require.Equal(t, "same-session-restart", r.Observed)
			require.Equal(t, contract.StandingHolding, contract.StandingOf(r))
			require.Zero(t, h.StartsWithoutMarker)
			require.Equal(t, core.SessionID("sess-b"), h.LastSessionID, "a restart moves neither field")

			for _, startTS := range []core.UnixMilli{6000, 0} {
				h2 := &contract.SessionHistory{SessionCount: 3, LastSessionID: "sess-b"}
				r = startFiresWith(t, root, h2, "sess-a", source, nil, startTS)
				require.Equal(t, "marker-absent-once", r.Observed, "start time %d", startTS)
				require.Equal(t, 1, h2.StartsWithoutMarker, "start time %d", startTS)
			}
		})
	}
}

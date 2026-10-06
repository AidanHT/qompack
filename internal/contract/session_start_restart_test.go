package contract_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// sessionRestartObserved is what session_start.fires reports for a session's own restart, with no
// absence counted: a start whose marker that same session's own terminal hook wrote, either of the
// session the history last saw or a compaction's SessionStart(source=compact) or a --resume of any
// session (F-C48-1; the two-window case, w20 status audit).
const sessionRestartObserved = "same-session-restart"

// startFires runs session_start.fires once for a SessionStart of sess with the given source, the
// way the daemon's session.start does (one RunAll per start, then SessionCount++).
func startFires(t *testing.T, root string, h *contract.SessionHistory, sess core.SessionID, source string) contract.Result {
	t.Helper()
	r := assertionByID(t, contract.CSessionStartFires).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h,
		Event: hookio.Event{HookEventName: "SessionStart", SessionID: sess, Source: source},
	})
	h.SessionCount++
	return r
}

// TestSessionStartFires_CompactOfSameSessionHolds is F-C48-1 (candidate 7 live lane, C4.8): a session
// that started on the previous session's marker, then compacted — PreCompact, a terminal hook,
// rewrote run/marker.json to name this session — and received SessionStart(source=compact) read
// marker-absent-once, so `qompack status` counted a pending row on a healthy store whose history held
// starts_without_marker 0. The compaction's start is this session's own restart, not an absent
// marker: it holds, it counts nothing toward the two-consecutive-sessions failure, and the next
// session still reads the marker the compaction left.
func TestSessionStartFires_CompactOfSameSessionHolds(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 1, LastSessionID: "sess-a"}

	require.NoError(t, contract.WriteMarker(root, "sess-a", 1)) // session a's SessionEnd
	r := startFires(t, root, h, "sess-b", "startup")
	require.True(t, r.OK)
	require.Equal(t, "marker-found", r.Observed)

	for i := range 2 { // two compactions in one session: each is the same restart
		require.NoError(t, contract.WriteMarker(root, "sess-b", core.UnixMilli(2+i))) // PreCompact
		r = startFires(t, root, h, "sess-b", "compact")
		require.True(t, r.OK)
		require.Equal(t, sessionRestartObserved, r.Observed, "compaction %d", i+1)
		require.Equal(t, contract.StandingHolding, contract.StandingOf(r),
			"a same-session compaction must read holding, never pending (troubleshooting.md §1's 0 pending)")
		require.Equal(t, 0, h.StartsWithoutMarker, "a same-session compaction must count nothing")
		require.Equal(t, core.SessionID("sess-b"), h.LastSessionID)
	}

	// The next session reads the marker the compaction left.
	r = startFires(t, root, h, "sess-c", "startup")
	require.True(t, r.OK)
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, 0, h.StartsWithoutMarker)
}

// TestSessionStartFires_CompactOfFirstSessionHolds: the very first session a project has ever had
// reads first-session at its startup; its own compaction is the same restart and holds too.
func TestSessionStartFires_CompactOfFirstSessionHolds(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{}

	r := startFires(t, root, h, "sess-a", "startup")
	require.Equal(t, "first-session", r.Observed)

	require.NoError(t, contract.WriteMarker(root, "sess-a", 1)) // PreCompact
	r = startFires(t, root, h, "sess-a", "compact")
	require.True(t, r.OK)
	require.Equal(t, sessionRestartObserved, r.Observed)
	require.Equal(t, contract.StandingHolding, contract.StandingOf(r))
	require.Equal(t, 0, h.StartsWithoutMarker)
}

// TestSessionStartFires_ResumeOfSameSessionHolds: a --resume that keeps the session id starts after
// that session's own SessionEnd wrote the marker, with no other session in between. It is the same
// session's restart, not an absent marker.
func TestSessionStartFires_ResumeOfSameSessionHolds(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 4, LastSessionID: "sess-a"}

	require.NoError(t, contract.WriteMarker(root, "sess-a", 1))
	r := startFires(t, root, h, "sess-b", "startup")
	require.Equal(t, "marker-found", r.Observed)

	require.NoError(t, contract.WriteMarker(root, "sess-b", 2)) // session b's SessionEnd
	r = startFires(t, root, h, "sess-b", "resume")
	require.True(t, r.OK)
	require.Equal(t, sessionRestartObserved, r.Observed)
	require.Equal(t, contract.StandingHolding, contract.StandingOf(r))
	require.Equal(t, 0, h.StartsWithoutMarker)
	require.Equal(t, core.SessionID("sess-b"), h.LastSessionID)
}

// TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour pins that the restart reading takes nothing
// from a real absence: a new session with no marker from a prior terminal hook is
// marker-absent-once and pending; the session's own later restart leaves that counted absence where
// it was (the next start still decides it); and two consecutive sessions without a marker fail, a
// failure the failing session's own restart does not turn into a holding row.
func TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 2, LastSessionID: "sess-a"}

	// Session b: no marker at all, a genuine absence.
	r := startFires(t, root, h, "sess-b", "startup")
	require.True(t, r.OK)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, contract.StandingPending, contract.StandingOf(r))
	require.Equal(t, 1, h.StartsWithoutMarker)

	// Session b compacts: its own restart. The absence its startup counted is still the one the next
	// start decides, so the row stays pending and the count is untouched.
	require.NoError(t, contract.WriteMarker(root, "sess-b", 1))
	r = startFires(t, root, h, "sess-b", "compact")
	require.True(t, r.OK)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, contract.StandingPending, contract.StandingOf(r))
	require.Equal(t, 1, h.StartsWithoutMarker)

	// A same-session start with no marker at all counts nothing more either.
	h2 := &contract.SessionHistory{SessionCount: 2, LastSessionID: "sess-a"}
	root2 := t.TempDir()
	r = startFires(t, root2, h2, "sess-b", "startup")
	require.Equal(t, "marker-absent-once", r.Observed)
	r = startFires(t, root2, h2, "sess-b", "compact")
	require.True(t, r.OK)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, 1, h2.StartsWithoutMarker)

	// Session c: no marker again — two consecutive sessions, a failure.
	r = startFires(t, root2, h2, "sess-c", "startup")
	require.False(t, r.OK)
	require.Equal(t, contract.SevCritical, r.Severity)
	require.Equal(t, 2, h2.StartsWithoutMarker)

	// Session c's own compaction does not turn the failure into a holding row.
	require.NoError(t, contract.WriteMarker(root2, "sess-c", 2))
	r = startFires(t, root2, h2, "sess-c", "compact")
	require.False(t, r.OK)
	require.Equal(t, contract.StandingFailing, contract.StandingOf(r))
	require.Equal(t, 2, h2.StartsWithoutMarker)
}

// TestSessionStartFires_OverlappingSessionsRestartsHold is the two-window case (w20 status audit):
// two sessions open in one project share run/marker.json and state/history.json, so the session
// that started second is the one History.LastSessionID names. The OTHER session's compaction, or a
// --resume of it, still finds the marker its own PreCompact or SessionEnd just wrote. That is its
// own restart: it holds, counts nothing, and leaves LastSessionID alone. Before the fix the first
// session's compaction counted an absence (pending) and the second's made it two (SevCritical, the
// project degraded on a store where every hook fired).
func TestSessionStartFires_OverlappingSessionsRestartsHold(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 1, LastSessionID: "sess-z"}
	require.NoError(t, contract.WriteMarker(root, "sess-z", 1)) // session z's SessionEnd

	r := startFires(t, root, h, "sess-a", "startup")
	require.Equal(t, "marker-found", r.Observed)
	r = startFires(t, root, h, "sess-b", "startup") // a second window, while a is still open
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, core.SessionID("sess-b"), h.LastSessionID)

	steps := []struct {
		sess   core.SessionID
		source string
	}{
		{"sess-a", "compact"}, // a, not the last-started session, compacts
		{"sess-b", "compact"}, // then b compacts
		{"sess-a", "compact"}, // interleaved again
		{"sess-a", "resume"},  // a's SessionEnd, then a --resume keeping its id
		{"sess-b", "resume"},
	}
	for i, s := range steps {
		require.NoError(t, contract.WriteMarker(root, s.sess, core.UnixMilli(2+i)))
		r = startFires(t, root, h, s.sess, s.source)
		require.True(t, r.OK, "step %d (%s %s)", i+1, s.sess, s.source)
		require.Equal(t, sessionRestartObserved, r.Observed, "step %d (%s %s)", i+1, s.sess, s.source)
		require.Equal(t, contract.StandingHolding, contract.StandingOf(r), "step %d", i+1)
		require.Equal(t, 0, h.StartsWithoutMarker, "step %d: an own restart counts nothing", i+1)
	}
	require.Equal(t, core.SessionID("sess-b"), h.LastSessionID,
		"a restart is no new session: LastSessionID still names the last-started one")

	// The next new session reads the marker the restarts left.
	r = startFires(t, root, h, "sess-c", "startup")
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, 0, h.StartsWithoutMarker)
}

// TestSessionStartFires_OverlappingRestartKeepsACountedAbsence: the other session's own restart takes
// nothing from an absence a start counted. The row stays marker-absent-once (pending) at a count of
// one and failing at two; the restart neither adds to the count nor clears it, and the next new
// session's start still decides it.
func TestSessionStartFires_OverlappingRestartKeepsACountedAbsence(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 2, LastSessionID: "sess-a"}

	// Session b starts beside a with no marker at all: a genuine absence.
	r := startFires(t, root, h, "sess-b", "startup")
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, 1, h.StartsWithoutMarker)

	// Session a compacts: its own restart, but the absence b's start counted is still undecided.
	require.NoError(t, contract.WriteMarker(root, "sess-a", 1))
	r = startFires(t, root, h, "sess-a", "compact")
	require.True(t, r.OK)
	require.Equal(t, "marker-absent-once", r.Observed)
	require.Equal(t, contract.StandingPending, contract.StandingOf(r))
	require.Equal(t, 1, h.StartsWithoutMarker)
	require.Equal(t, core.SessionID("sess-b"), h.LastSessionID)

	// At a count of two the other session's restart stays failing.
	h2 := &contract.SessionHistory{SessionCount: 3, LastSessionID: "sess-b", StartsWithoutMarker: 2}
	r = startFires(t, root, h2, "sess-a", "compact")
	require.False(t, r.OK)
	require.Equal(t, contract.StandingFailing, contract.StandingOf(r))
	require.Equal(t, 2, h2.StartsWithoutMarker)

	// The next new session finds a's marker and decides the absence.
	r = startFires(t, root, h, "sess-c", "startup")
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, 0, h.StartsWithoutMarker)
}

// TestSessionStartFires_FreshStartNamingItselfStillCounts: only a compact or resume start is a
// restart of a session the history did not last see. A startup or clear carries a session id the
// host has just minted, so a marker that already names it is no proof any prior terminal hook
// fired; it counts as an absence, as it always did.
func TestSessionStartFires_FreshStartNamingItselfStillCounts(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	for _, source := range []string{"startup", "clear"} {
		root := t.TempDir()
		h := &contract.SessionHistory{SessionCount: 2, LastSessionID: "sess-a"}
		require.NoError(t, contract.WriteMarker(root, "sess-b", 1))
		r := startFires(t, root, h, "sess-b", source)
		require.True(t, r.OK, source)
		require.Equal(t, "marker-absent-once", r.Observed, source)
		require.Equal(t, 1, h.StartsWithoutMarker, source)
		require.Equal(t, core.SessionID("sess-b"), h.LastSessionID, source)
	}
}

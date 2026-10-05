package contract

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// Audit 2's #9, wave 22 fix round 2: the record of a session going on after a PreCompact (a start, a
// prompt or its SessionEnd) lives in state/history.json, so a PreCompact a restarted daemon replays
// after those hooks still arms its obligation already lapsed. The daemon-level rows are
// TestCheckpointReplay_ThePreCompactsSessionWentOnBeforeARestart and its pin,
// TestCheckpointReplay_NonCompactStartAfterARestartedReplayStillFails.

// TestSessionHistory_WentOnSurvivesASaveAndLapsesALaterArming: a hook of the session fired after the
// PreCompact, recorded and saved before the PreCompact is armed, lapses the new obligation once the
// history is loaded again. One fired before the PreCompact, and another session's, lapse nothing.
func TestSessionHistory_WentOnSurvivesASaveAndLapsesALaterArming(t *testing.T) {
	const sess, other = core.SessionID("sess-a"), core.SessionID("sess-b")
	p := filepath.Join(t.TempDir(), "history.json")

	h := &SessionHistory{}
	require.True(t, h.NoteWentOn(sess, 2000))
	require.True(t, h.NoteWentOn(other, 5000))
	require.NoError(t, SaveHistory(p, h))

	h = LoadHistory(p)
	require.Equal(t, core.UnixMilli(2000), h.WentOn[sess], "the record survives a save and a load")
	h.ArmCompactStart(sess, 1000)
	require.True(t, h.AwaitingCompactStart)
	require.Equal(t, sess, h.LastPrecompactSession)
	require.Equal(t, core.UnixMilli(1000), h.LastPrecompactTS)
	require.True(t, h.CompactStartLapsed, "the session went on after the PreCompact was fired")

	h.ArmCompactStart(sess, 3000)
	require.False(t, h.CompactStartLapsed, "a hook fired before the PreCompact lapses nothing")
	require.NotContains(t, h.WentOn, sess, "a record no later than the armed PreCompact is dropped")
	require.Equal(t, core.UnixMilli(5000), h.WentOn[other], "a later one is kept")

	h.ArmCompactStart(sess, 4000)
	require.False(t, h.CompactStartLapsed, "another session's hook lapses nothing")
	h.ArmCompactStart(other, 6000)
	require.Nil(t, h.WentOn, "every record is from before the latest PreCompact")
}

// TestSessionHistory_NoteWentOnKeepsOnlyWhatALaterArmingCanRead: NoteWentOn records only the latest
// hook time per session, only one later than the PreCompact the history records, and reports a change
// only when it made one, so the routes save the history only then.
func TestSessionHistory_NoteWentOnKeepsOnlyWhatALaterArmingCanRead(t *testing.T) {
	h := &SessionHistory{LastPrecompactTS: 1000}
	require.False(t, h.NoteWentOn("sess-a", 1000), "no later than the recorded PreCompact")
	require.False(t, h.NoteWentOn("sess-a", 0), "no time")
	require.False(t, h.NoteWentOn("", 2000), "no session")
	require.Nil(t, h.WentOn)

	require.True(t, h.NoteWentOn("sess-a", 3000))
	require.False(t, h.NoteWentOn("sess-a", 2000), "an earlier hook delivered late")
	require.False(t, h.NoteWentOn("sess-a", 3000), "the same hook again")
	require.Equal(t, core.UnixMilli(3000), h.WentOn["sess-a"])

	var nilH *SessionHistory
	require.False(t, nilH.NoteWentOn("sess-a", 3000))
	nilH.ArmCompactStart("sess-a", 3000)
}

// TestSessionHistory_WentOnIsBounded: the record holds at most maxWentOnSessions sessions, those
// that went on latest, however many sessions a project runs without a compaction, and a hand-edited
// history is clamped on load.
func TestSessionHistory_WentOnIsBounded(t *testing.T) {
	h := &SessionHistory{}
	for i := 1; i <= maxWentOnSessions+4; i++ {
		h.NoteWentOn(core.SessionID(fmt.Sprintf("sess-%02d", i)), core.UnixMilli(i*1000))
	}
	require.Len(t, h.WentOn, maxWentOnSessions)
	for i := 1; i <= 4; i++ {
		require.NotContains(t, h.WentOn, core.SessionID(fmt.Sprintf("sess-%02d", i)), "the earliest go first")
	}
	require.Contains(t, h.WentOn, core.SessionID(fmt.Sprintf("sess-%02d", maxWentOnSessions+4)))

	big := map[core.SessionID]core.UnixMilli{}
	for i := 0; i < 3*maxWentOnSessions; i++ {
		big[core.SessionID(fmt.Sprintf("s-%03d", i))] = 7000 // one time: ties go by session id
	}
	p := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, SaveHistory(p, &SessionHistory{WentOn: big}))
	loaded := LoadHistory(p)
	require.Len(t, loaded.WentOn, maxWentOnSessions)
	require.Contains(t, loaded.WentOn, core.SessionID(fmt.Sprintf("s-%03d", 3*maxWentOnSessions-1)),
		"at one time the largest session ids stay")
	require.NotContains(t, loaded.WentOn, core.SessionID("s-000"))
}

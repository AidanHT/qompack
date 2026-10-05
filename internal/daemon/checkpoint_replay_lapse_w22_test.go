package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// Audit 2's #9, wave 22 fix round 1: a PreCompact replayed from a hook's spool must arm
// session_start.source_compact as of the moment the host fired it, whatever the daemon handled in
// the meantime. The hook spools the very request it sent (same nonce, same TS) when the reply
// misses its deadline, although the daemon may have handled it live, and a drain replays that copy
// later. Re-arming from it cleared the lapse a later prompt or SessionEnd had recorded, so a
// --resume after the cancelled compaction failed at critical severity again. A PreCompact the daemon
// never saw live, replayed after its session went on, armed with no lapse at all, for the same
// false critical.

// lapseFixture is one session's startup and the PreCompact request the rows replay. The startup
// is fired a second before the PreCompact, so SessionRegistry.StartedSince does not mistake it for
// the start the PreCompact announced.
func lapseFixture(t *testing.T, dd *daemon, sess core.SessionID, nonce string) ipc.Request {
	t.Helper()
	pre := checkpointRequest(dd, sess, nonce)
	start := startRequest(dd, sess, "startup", "", "n-start-"+string(sess))
	start.TS = pre.TS - 1000
	require.True(t, dd.dispatchOp(context.Background(), start).OK)
	return pre
}

// goOn delivers a hook of sess that the host fired at at and that shows the session went on: a
// prompt (the prompt scan runIngested runs) or its SessionEnd.
func goOn(t *testing.T, dd *daemon, sess core.SessionID, how string, at core.UnixMilli) {
	t.Helper()
	switch how {
	case "prompt":
		promptScan(dd, sess, "", at)
	case "session end":
		resp := dd.dispatchOp(context.Background(), sessionEndRequest(dd, sess, at))
		require.True(t, resp.OK, resp.Err)
	default:
		t.Fatalf("goOn: unknown hook %q", how)
	}
}

// resumeAt delivers a live --resume of sess fired at at and returns its source_compact reading and
// the answer's banner.
func resumeAt(t *testing.T, dd *daemon, sess core.SessionID, at core.UnixMilli) (contract.Result, string) {
	t.Helper()
	resume := startRequest(dd, sess, "resume", "", "n-resume-"+string(sess))
	resume.TS = at
	resp := dd.dispatchOp(context.Background(), resume)
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	return reportOf(t, dd, contract.CSessionStartSourceCompact), resp.Output.SystemMessage
}

func requireNoHostFailure(t *testing.T, dd *daemon, r contract.Result, banner string) {
	t.Helper()
	require.True(t, r.OK, "a cancelled compaction is no host failure (observed %q)", r.Observed)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode())
	require.Empty(t, banner, "no banner blames the host")
}

func requireHostFailure(t *testing.T, dd *daemon, r contract.Result) {
	t.Helper()
	require.False(t, r.OK, "a resume right after PreCompact is no compaction")
	require.Equal(t, contract.SevCritical, r.Severity)
	require.Equal(t, "compact", r.Expected)
	require.Equal(t, "resume", r.Observed)
	require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode())
}

// TestCheckpointReplay_ACopyAfterTheSessionWentOnKeepsItsLapse is the verifier's probe: the live
// PreCompact armed the obligation, the compaction was cancelled, the session prompted or ended, and
// then a drain replayed the hook's spooled copy of the same PreCompact. The copy is a PreCompact
// already recorded and changes nothing, so the --resume that follows stays full. The copy is replayed
// by a restarted daemon too, which has no memory of the session.
func TestCheckpointReplay_ACopyAfterTheSessionWentOnKeepsItsLapse(t *testing.T) {
	const sess = core.SessionID("sess-copy-after-lapse")
	cases := []struct {
		name, how string
		restart   bool
	}{
		{"prompt", "prompt", false},
		{"session end", "session end", false},
		{"prompt, then a daemon restart", "prompt", true},
		{"session end, then a daemon restart", "session end", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dd := replayProbeDaemonAt(t, root)
			pre := lapseFixture(t, dd, sess, "n-pre")
			require.True(t, dd.dispatchOp(context.Background(), pre).OK)
			goOn(t, dd, sess, tc.how, pre.TS+1000)
			require.True(t, history(t, dd).CompactStartLapsed, "fixture: the %s records the lapse", tc.how)
			if tc.restart {
				dd = replayProbeDaemonAt(t, root)
			}

			require.True(t, dd.drainDispatch(context.Background(), pre).OK)
			h := history(t, dd)
			assert.True(t, h.AwaitingCompactStart)
			assert.True(t, h.CompactStartLapsed, "a copy of the PreCompact already recorded keeps its lapse")
			assert.Equal(t, pre.TS, h.LastPrecompactTS)

			r, banner := resumeAt(t, dd, sess, pre.TS+2000)
			requireNoHostFailure(t, dd, r, banner)
		})
	}
}

// TestCheckpointReplay_ANeverSeenPreCompactAfterTheSessionWentOnIsLapsed is the same class for a
// PreCompact the daemon never handled live: the hook could not deliver it, the compaction was
// cancelled, the session's prompt or SessionEnd reached the daemon, and only then did a drain (a
// SessionEnd's own final drain, or the spool watcher's) replay the PreCompact. It arms the obligation
// already lapsed, as it would have read had it arrived first.
func TestCheckpointReplay_ANeverSeenPreCompactAfterTheSessionWentOnIsLapsed(t *testing.T) {
	const sess = core.SessionID("sess-unseen-after-lapse")
	for _, how := range []string{"prompt", "session end"} {
		t.Run(how, func(t *testing.T) {
			dd := replayProbeDaemon(t)
			pre := lapseFixture(t, dd, sess, "n-pre")
			goOn(t, dd, sess, how, pre.TS+1000)

			require.True(t, dd.drainDispatch(context.Background(), pre).OK)
			h := history(t, dd)
			assert.True(t, h.AwaitingCompactStart, "the replay still records the PreCompact")
			assert.Equal(t, sess, h.LastPrecompactSession)
			assert.True(t, h.CompactStartLapsed, "the session went on after the PreCompact was fired")

			r, banner := resumeAt(t, dd, sess, pre.TS+2000)
			requireNoHostFailure(t, dd, r, banner)
		})
	}
}

// TestCheckpointReplay_AnOlderPreCompactLeavesTheNewerObligation: a replayed PreCompact older than
// the one history already records, of another session, is no newer obligation and must not take the
// newer one's place. Taking it lost the newer session's genuine failure (its non-compact start read
// precompact-pending-for-another-session), and after a restart it pinned the older session's finished
// compaction on its next --resume at critical severity.
func TestCheckpointReplay_AnOlderPreCompactLeavesTheNewerObligation(t *testing.T) {
	const a, b = core.SessionID("sess-older"), core.SessionID("sess-newer")

	t.Run("the newer session's genuine failure still fails", func(t *testing.T) {
		dd := replayProbeDaemon(t)
		preA := lapseFixture(t, dd, a, "n-pre-a")
		preB := lapseFixture(t, dd, b, "n-pre-b")
		preB.TS = preA.TS + 1000
		require.True(t, dd.dispatchOp(context.Background(), preA).OK)
		require.True(t, dd.dispatchOp(context.Background(), preB).OK)

		require.True(t, dd.drainDispatch(context.Background(), preA).OK)
		assert.Equal(t, b, history(t, dd).LastPrecompactSession, "the newer obligation keeps its place")

		r, _ := resumeAt(t, dd, b, preB.TS+1000)
		requireHostFailure(t, dd, r)
	})

	t.Run("across a restart, the older session's resume is no failure", func(t *testing.T) {
		root := t.TempDir()
		dd := replayProbeDaemonAt(t, root)
		preA := lapseFixture(t, dd, a, "n-pre-a")
		preB := lapseFixture(t, dd, b, "n-pre-b")
		preB.TS = preA.TS + 2000
		require.True(t, dd.dispatchOp(context.Background(), preA).OK)
		compact := startRequest(dd, a, "compact", "", "n-compact-a")
		compact.TS = preA.TS + 1000
		require.True(t, dd.dispatchOp(context.Background(), compact).OK)
		require.True(t, dd.dispatchOp(context.Background(), preB).OK)
		dd = replayProbeDaemonAt(t, root)

		require.True(t, dd.drainDispatch(context.Background(), preA).OK)
		assert.Equal(t, b, history(t, dd).LastPrecompactSession, "the newer obligation keeps its place")

		r, banner := resumeAt(t, dd, a, preB.TS+1000)
		requireNoHostFailure(t, dd, r, banner)
	})
}

// TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails pins the other
// direction: a replayed PreCompact followed by a non-compact start of its session with nothing of
// that session in between is still the host breaking the contract. That holds for a copy of a
// PreCompact handled live, for one never seen live, and when the only hook of the session the daemon
// handled first was fired before the PreCompact, or was another session's.
func TestCheckpointReplay_NonCompactStartRightAfterAReplayedPreCompactStillFails(t *testing.T) {
	const sess, other = core.SessionID("sess-replayed-wrong-source"), core.SessionID("sess-bystander")
	cases := []struct {
		name   string
		before func(t *testing.T, dd *daemon, pre ipc.Request)
	}{
		{"a copy of the live PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			require.True(t, dd.dispatchOp(context.Background(), pre).OK)
		}},
		{"a PreCompact never seen live", func(*testing.T, *daemon, ipc.Request) {}},
		{"a prompt fired before the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			goOn(t, dd, sess, "prompt", pre.TS-500)
		}},
		{"a SessionEnd fired before the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			end := sessionEndRequest(dd, sess, pre.TS-500)
			require.True(t, dd.drainDispatch(context.Background(), end).OK)
		}},
		{"another session's prompt after the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			goOn(t, dd, other, "prompt", pre.TS+500)
		}},
		{"another session's SessionEnd after the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			goOn(t, dd, other, "session end", pre.TS+500)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dd := replayProbeDaemon(t)
			pre := lapseFixture(t, dd, sess, "n-pre")
			tc.before(t, dd, pre)

			require.True(t, dd.drainDispatch(context.Background(), pre).OK)
			h := history(t, dd)
			require.True(t, h.AwaitingCompactStart)
			require.False(t, h.CompactStartLapsed, "nothing of the session followed its PreCompact")

			r, _ := resumeAt(t, dd, sess, pre.TS+1000)
			requireHostFailure(t, dd, r)
		})
	}
}

// TestCheckpointReplay_ThePreCompactsSessionWentOnBeforeARestart is #9 across a daemon restart (wave
// 22 fix round 2, the verifier's probe): the daemon never handled the PreCompact live (its hook could
// not deliver it), the compaction was cancelled, and the session's prompt or SessionEnd was handled
// live; then the daemon died before a drain replayed the PreCompact. The restarted daemon replays it
// with no memory of what the session did, so the record of a session going on after a PreCompact is
// kept in state/history.json, beside the PreCompact it is compared with. The same holds for a start
// of the session fired after the PreCompact and handled before the restart (its compact start, when
// the compaction did complete): it already judged the obligation, so the session's next start, a
// --resume, has nothing to judge. And a PreCompact handled live only after its own compact start —
// one held up while the host went on — is the same order of arrival without a replay.
func TestCheckpointReplay_ThePreCompactsSessionWentOnBeforeARestart(t *testing.T) {
	const sess = core.SessionID("sess-went-on-before-restart")
	for _, how := range []string{"prompt", "session end", "compact start"} {
		t.Run(how, func(t *testing.T) {
			root := t.TempDir()
			dd := replayProbeDaemonAt(t, root)
			pre := lapseFixture(t, dd, sess, "n-pre")
			if how == "compact start" {
				compact := startRequest(dd, sess, "compact", "", "n-compact")
				compact.TS = pre.TS + 1000
				require.True(t, dd.dispatchOp(context.Background(), compact).OK)
			} else {
				goOn(t, dd, sess, how, pre.TS+1000)
			}
			dd = replayProbeDaemonAt(t, root)

			require.True(t, dd.drainDispatch(context.Background(), pre).OK)
			h := history(t, dd)
			assert.True(t, h.CompactStartLapsed || !h.AwaitingCompactStart,
				"the session went on after the PreCompact was fired (awaiting=%v lapsed=%v)",
				h.AwaitingCompactStart, h.CompactStartLapsed)

			r, banner := resumeAt(t, dd, sess, pre.TS+2000)
			requireNoHostFailure(t, dd, r, banner)
		})
	}
	t.Run("a live PreCompact handled after its compact start", func(t *testing.T) {
		dd := replayProbeDaemon(t)
		pre := lapseFixture(t, dd, sess, "n-pre")
		compact := startRequest(dd, sess, "compact", "", "n-compact")
		compact.TS = pre.TS + 1000
		require.True(t, dd.dispatchOp(context.Background(), compact).OK)
		require.True(t, dd.dispatchOp(context.Background(), pre).OK)

		r, banner := resumeAt(t, dd, sess, pre.TS+2000)
		requireNoHostFailure(t, dd, r, banner)
	})
}

// TestCheckpointReplay_NonCompactStartAfterARestartedReplayStillFails pins the other direction across
// a restart: what the restarted daemon reads from state/history.json must not lapse an obligation
// nothing of the session followed. A PreCompact replayed by a restarted daemon, then a non-compact
// start of its session with nothing of that session in between, still fails at critical severity:
// when the session's last hook before the restart was fired before the PreCompact, or when only
// another session went on after it.
func TestCheckpointReplay_NonCompactStartAfterARestartedReplayStillFails(t *testing.T) {
	const sess, other = core.SessionID("sess-restarted-wrong-source"), core.SessionID("sess-bystander")
	cases := []struct {
		name   string
		before func(t *testing.T, dd *daemon, pre ipc.Request)
	}{
		{"nothing but its startup", func(*testing.T, *daemon, ipc.Request) {}},
		{"a prompt fired before the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			goOn(t, dd, sess, "prompt", pre.TS-500)
		}},
		{"another session's prompt after the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			goOn(t, dd, other, "prompt", pre.TS+500)
		}},
		{"another session's start after the PreCompact", func(t *testing.T, dd *daemon, pre ipc.Request) {
			start := startRequest(dd, other, "startup", "", "n-other")
			start.TS = pre.TS + 500
			require.True(t, dd.dispatchOp(context.Background(), start).OK)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dd := replayProbeDaemonAt(t, root)
			pre := lapseFixture(t, dd, sess, "n-pre")
			tc.before(t, dd, pre)
			dd = replayProbeDaemonAt(t, root)

			require.True(t, dd.drainDispatch(context.Background(), pre).OK)
			h := history(t, dd)
			require.True(t, h.AwaitingCompactStart)
			require.False(t, h.CompactStartLapsed, "nothing of the session followed its PreCompact")

			r, _ := resumeAt(t, dd, sess, pre.TS+1000)
			requireHostFailure(t, dd, r)
		})
	}
}

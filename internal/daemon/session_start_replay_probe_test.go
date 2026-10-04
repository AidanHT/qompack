package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// A request reaches the drain from a hook client's spool only when no answer from the daemon reached
// that hook in time: the daemon was down or not yet listening, or its reply missed the client's
// deadline. The hook then answered the host without the daemon, so nothing the replay puts into an
// answer can reach the host. The §12.1 hook.additional_context_delivered probe is the sharpest case:
// a replayed session.start that mints a sentinel mints one nobody received, the next two prompts
// cannot find it, and the next session start degrades the project to passive recording, blaming the
// host for a reply Qompack itself lost (w2-hookout, runs/diag-spooled-sessionstart-probe-windows.log).
// These rows pin what a replay may and may not do.

// TestSessionStartReplay_MintsNoProbeAndAnswersNothing: the replay does the start's durable
// bookkeeping — the session is registered, the contract run is recorded and counted — but mints no
// probe and puts nothing into its answer.
func TestSessionStartReplay_MintsNoProbeAndAnswersNothing(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-replayed-start")
	transcript := replayTranscript(t, dd.root)

	resp := dd.drainDispatch(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-replayed"))
	require.True(t, resp.OK, "the replay is acknowledged, so the drain consumes the spooled line")
	require.Empty(t, additionalContext(resp.Output), "a replay's answer reaches no host, so it carries nothing")
	if resp.Output != nil {
		require.Empty(t, resp.Output.SystemMessage)
	}

	h := history(t, dd)
	require.Empty(t, h.Sentinel.Token, "a replay mints no probe: the answer that would carry it reaches no host")
	require.Equal(t, 1, h.SessionCount, "the replay still counts the host's SessionStart")
	require.Equal(t, sess, h.LastSessionID, "the replay still runs the contract (session_start.fires records it)")
	_, known := dd.registry.Get(sess)
	require.True(t, known, "the replay still registers the session")
	_, err := os.Stat(paths.Long(contract.ObservationLedgerPath(dd.root)))
	require.NoError(t, err, "the replay still records the run's capability observations")
}

// TestSessionStartReplay_NeverDegradesTheProjectTwoPromptsLater is the routed defect end to end at
// the route level: a spooled startup SessionStart replayed by the drain, then the session's two
// prompts, then its next start. Before the fix the replay minted a probe the host never received,
// both prompts missed it, and the next start degraded the project to passive recording.
func TestSessionStartReplay_NeverDegradesTheProjectTwoPromptsLater(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-spooled-start")
	transcript := replayTranscript(t, dd.root)

	dd.drainDispatch(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-spooled"))
	for range 2 {
		promptScan(dd, sess, transcript, core.NowMilli(dd.clk))
	}
	require.Less(t, history(t, dd).Sentinel.Chances, 2, "no probe the host never saw may run out its chances")

	resp := dd.dispatchOp(context.Background(), startRequest(dd, sess, "resume", transcript, "nonce-next"))
	require.True(t, resp.OK)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(),
		"a replayed SessionStart must never degrade the project: the host was never sent its probe")
	require.NotNil(t, resp.Output)
	require.Empty(t, resp.Output.SystemMessage, "no degrade banner blames the host")
	require.True(t, hasProbeLine(additionalContext(resp.Output)), "the live start mints and delivers its own probe")
}

// TestSessionStartReplay_WithdrawsTheProbeItsLostAnswerCarried: a live start whose reply missed the
// client's deadline minted a probe into an answer the host never got, and the client spooled the
// request. Its replay — the same delivery, so the same nonce — is the daemon's proof that the answer
// was lost, and withdraws that probe, so the session's prompts cannot run out its chances. A replay of
// any OTHER request leaves the current probe alone: that one's answer may well have been delivered.
func TestSessionStartReplay_WithdrawsTheProbeItsLostAnswerCarried(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-late-reply")
	transcript := replayTranscript(t, dd.root)
	late := startRequest(dd, sess, "startup", transcript, "nonce-late")

	live := dd.dispatchOp(context.Background(), late)
	require.True(t, hasProbeLine(additionalContext(live.Output)), "fixture: the live answer carried a probe")
	minted := history(t, dd).Sentinel.Token
	require.NotEmpty(t, minted)
	promptScan(dd, sess, transcript, core.NowMilli(dd.clk))
	require.Equal(t, 1, history(t, dd).Sentinel.Chances, "fixture: the first prompt missed the undelivered probe")

	// An earlier start of the same session, spooled and replayed only now: another request, so its
	// replay proves nothing about the live answer's delivery.
	other := startRequest(dd, sess, "startup", transcript, "nonce-other")
	dd.drainDispatch(context.Background(), other)
	require.Equal(t, minted, history(t, dd).Sentinel.Token, "a replay of another request withdraws nothing")

	dd.drainDispatch(context.Background(), late)
	h := history(t, dd)
	require.Empty(t, h.Sentinel.Token, "the replay of the request whose answer was lost withdraws its probe")
	require.Zero(t, h.Sentinel.Chances, "and the chances that probe never had")

	promptScan(dd, sess, transcript, core.NowMilli(dd.clk))
	dd.dispatchOp(context.Background(), startRequest(dd, sess, "resume", transcript, "nonce-next"))
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(), "a lost answer's probe must never degrade the project")
}

// seedPendingCompact makes the next non-compact start of sess fail session_start.source_compact at
// critical severity, which degrades the daemon — the banner's trigger (degrade_banner_test.go).
func seedPendingCompact(t *testing.T, dd *daemon, sess core.SessionID) {
	t.Helper()
	h := history(t, dd)
	h.SessionCount = 1
	h.AwaitingCompactStart = true
	h.LastPrecompactSession = sess
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(dd.root), h))
}

// TestSessionStartReplay_LeavesTheDegradeBannerToTheNextLiveStart: the §12.1 banner is shown once, on
// the start that degrades the project. A replay that degrades it has no host to show it to, so the
// banner is owed to the next live start — before the fix the replay spent it and the user never saw it.
func TestSessionStartReplay_LeavesTheDegradeBannerToTheNextLiveStart(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-degrading-replay")
	seedPendingCompact(t, dd, sess)

	replay := dd.drainDispatch(context.Background(), startRequest(dd, sess, "startup", "", "nonce-degrading"))
	require.True(t, replay.OK)
	require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode(), "fixture: the replayed start degrades")
	if replay.Output != nil {
		require.Empty(t, replay.Output.SystemMessage, "a replay's banner would reach no host")
	}

	live := dd.dispatchOp(context.Background(), startRequest(dd, "sess-next", "startup", "", "nonce-next"))
	require.NotNil(t, live.Output)
	require.Contains(t, live.Output.SystemMessage, "Qompack: degraded to passive recording",
		"the next live start owes the user the banner the replay could not deliver")

	again := dd.dispatchOp(context.Background(), startRequest(dd, "sess-after", "startup", "", "nonce-after"))
	require.NotNil(t, again.Output)
	require.Empty(t, again.Output.SystemMessage, "once shown, the banner is not repeated")
}

// TestSessionStartReplay_OwesTheBannerItsLostAnswerCarried: a live start that degraded the project put
// the banner into an answer the host never got; the replay of that same request owes it to the next
// live start.
func TestSessionStartReplay_OwesTheBannerItsLostAnswerCarried(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-lost-banner")
	seedPendingCompact(t, dd, sess)
	lost := startRequest(dd, sess, "startup", "", "nonce-lost-banner")

	first := dd.dispatchOp(context.Background(), lost)
	require.NotNil(t, first.Output)
	require.Contains(t, first.Output.SystemMessage, "degraded to passive recording", "fixture: the lost answer carried it")

	dd.drainDispatch(context.Background(), lost)

	// The session ends without a terminal hook, so the next start fails session_start.fires too (a
	// second absence) and the project is still degraded when the banner is owed. Before wave 22 the
	// next start failed it while this session was still running, which was audit 2's #8 false
	// critical; the replay's own clean run would otherwise begin the restore.
	abandonAll(dd)
	next := dd.dispatchOp(context.Background(), startRequest(dd, "sess-next", "startup", "", "nonce-next"))
	require.NotNil(t, next.Output)
	require.Contains(t, next.Output.SystemMessage, "Qompack: degraded to passive recording",
		"the banner whose answer was lost is owed to the next live start")
}

// TestSessionStartReplay_AnEarlierSessionsProbeIsNotMissedByTheReplayedSession: a replayed start mints
// no probe, so the probe an EARLIER live start minted stays current — one the host did deliver, to
// that earlier session's transcript. The replayed session's own prompts scan their own transcript,
// which never had it: counting their misses would degrade the project two prompts later exactly as
// the undelivered probe did, whenever the earlier session ended before any prompt of its own found it.
func TestSessionStartReplay_AnEarlierSessionsProbeIsNotMissedByTheReplayedSession(t *testing.T) {
	dd := replayProbeDaemon(t)
	const earlier, replayed = core.SessionID("sess-earlier-live"), core.SessionID("sess-later-spooled")
	earlierTranscript := replayTranscript(t, dd.root)
	transcript := filepath.Join(dd.root, "later-transcript.jsonl")
	require.NoError(t, os.WriteFile(paths.Long(transcript), []byte(`{"type":"user"}`+"\n"), 0o600))

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, earlier, "startup", earlierTranscript, "nonce-earlier")).OK)
	require.NotEmpty(t, history(t, dd).Sentinel.Token, "fixture: the earlier live start minted and delivered a probe")

	dd.drainDispatch(context.Background(), startRequest(dd, replayed, "startup", transcript, "nonce-spooled"))
	for range 2 {
		promptScan(dd, replayed, transcript, core.NowMilli(dd.clk)+1)
	}
	require.Less(t, history(t, dd).Sentinel.Chances, 2, "no prompt of another session may run out the probe's chances")

	resp := dd.dispatchOp(context.Background(), startRequest(dd, replayed, "resume", transcript, "nonce-next"))
	require.True(t, resp.OK)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(),
		"a replayed SessionStart must never degrade the project through an earlier session's probe")
	require.NotNil(t, resp.Output)
	require.Empty(t, resp.Output.SystemMessage)
}

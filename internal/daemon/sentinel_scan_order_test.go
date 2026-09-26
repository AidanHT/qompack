package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The §12.1 hook.additional_context_delivered probe counts a prompt whose transcript scan misses the
// current sentinel as one of its two chances. A prompt replayed from a spool, deferred to a drain, or
// sent from another window can be older than the sentinel it is scanned for, and a miss by a prompt
// the sentinel did not yet exist for is no evidence the host failed to deliver it.

// replayProbeDaemon is a daemon whose producer set declares hook.additional_context_delivered — the
// assertion the stale probe fails — by binding a Rehydrate seam, which is what declares it in
// production (DeclareProducers). The seam answers a compact start with nothing; the rows that start
// one are about the contract, not the rehydration.
//
// Deliberately NOT parallel (every caller): New declares producers into the process-wide set.
func replayProbeDaemon(t *testing.T) *daemon {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	root := t.TempDir()
	o := NewOptions(root, testConfig())
	o.Log = logging.Nop()
	o.Bind(func(s *Services) {
		s.Rehydrate = func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	require.True(t, contract.HasProducer(contract.CAdditionalContext), "fixture: the probe's assertion must be live")
	return dd
}

// replayTranscript writes a transcript that carries no probe, as a host's does when the SessionStart
// answer that would have carried one never reached it.
func replayTranscript(t *testing.T, root string) string {
	t.Helper()
	p := filepath.Join(root, "transcript.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"read the auth module"}}` + "\n"
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(line), 0o600))
	return p
}

// startRequest is the session.start request a host's SessionStart becomes, with the hook's delivery
// nonce and its first-statement timestamp, as the hook client builds it.
func startRequest(dd *daemon, sess core.SessionID, source, transcript, nonce string) ipc.Request {
	ev := &hookio.Event{
		HookEventName: "SessionStart", SessionID: sess, CWD: dd.root, Source: source, TranscriptPath: transcript,
	}
	return ipc.Request{
		Op: ipc.OpSessionStart, Session: sess, Reply: true, Event: ev, Nonce: nonce, TS: core.NowMilli(dd.clk),
	}
}

// promptScan runs the UserPromptSubmit half of the probe for one prompt of sess, as runIngested does.
func promptScan(dd *daemon, sess core.SessionID, transcript string, ts core.UnixMilli) {
	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, CWD: dd.root, TranscriptPath: transcript}
	dd.scanSentinelForPrompt(ev, ts)
}

func history(t *testing.T, dd *daemon) *contract.SessionHistory {
	t.Helper()
	return contract.LoadHistory(contract.HistoryPath(dd.root))
}

// TestSentinelScan_APromptFromBeforeTheMintIsNoMiss: a prompt submitted before the probe was minted
// (a replayed or deferred one, or another window's) cannot have been a chance to find it, so its miss
// is not counted; its find still is, since the transcript has the probe either way.
func TestSentinelScan_APromptFromBeforeTheMintIsNoMiss(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-scan-order")
	transcript := replayTranscript(t, dd.root)
	before := core.NowMilli(dd.clk) - 1000

	dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-mint"))
	h := history(t, dd)
	require.NotEmpty(t, h.Sentinel.Token)
	mintedAt := h.Sentinel.MintedAt

	promptScan(dd, sess, transcript, before)
	promptScan(dd, sess, transcript, before)
	require.Zero(t, history(t, dd).Sentinel.Chances, "prompts from before the mint are no chances to find it")

	promptScan(dd, sess, transcript, mintedAt+1)
	require.Equal(t, 1, history(t, dd).Sentinel.Chances, "a prompt after the mint still counts")

	f, err := os.OpenFile(paths.Long(transcript), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"system","content":"` + h.Sentinel.Token + `"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	promptScan(dd, sess, transcript, before)
	require.True(t, history(t, dd).Sentinel.Observed, "a find is evidence whenever the prompt was sent")
}

// TestSentinelScan_APromptOfAnotherSessionIsNoMiss: the probe is minted into one session's answer,
// so only that session's transcript can carry it. A prompt of another session — a second window, or
// a session whose own start was replayed from a spool and minted nothing, which leaves an earlier
// session's probe current — scans a transcript the probe was never sent to, so its miss is no chance
// to find it. Its find still counts: a resumed session's transcript can carry an earlier one's probe.
func TestSentinelScan_APromptOfAnotherSessionIsNoMiss(t *testing.T) {
	dd := replayProbeDaemon(t)
	const minted, other = core.SessionID("sess-probe-owner"), core.SessionID("sess-other-window")
	transcript := replayTranscript(t, dd.root)
	otherTranscript := filepath.Join(dd.root, "other-transcript.jsonl")
	require.NoError(t, os.WriteFile(paths.Long(otherTranscript), []byte(`{"type":"user"}`+"\n"), 0o600))

	dd.dispatchOp(context.Background(), startRequest(dd, minted, "startup", transcript, "nonce-owner"))
	h := history(t, dd)
	require.Equal(t, minted, h.Sentinel.Session, "fixture: the probe belongs to the session that started")
	after := h.Sentinel.MintedAt + 1

	promptScan(dd, other, otherTranscript, after)
	promptScan(dd, other, otherTranscript, after)
	require.Zero(t, history(t, dd).Sentinel.Chances, "another session's prompts are no chances to find this probe")

	promptScan(dd, minted, transcript, after)
	require.Equal(t, 1, history(t, dd).Sentinel.Chances, "the session it was minted for still counts its miss")

	f, err := os.OpenFile(paths.Long(otherTranscript), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"system","content":"` + h.Sentinel.Token + `"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	promptScan(dd, other, otherTranscript, after)
	require.True(t, history(t, dd).Sentinel.Observed, "a find is evidence in whichever transcript it is made")
}

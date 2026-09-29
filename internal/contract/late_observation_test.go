package contract_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// Two §12.1 observables arrive after the SessionStart that evaluates them: the host connects the
// MCP server beside the first start, not before it, and in -p mode it creates the transcript only
// after SessionStart:startup. The Phase 4 live lane (install D4, retrieval D7, C45-1) saw both read
// FAILING in healthy sessions. A start that is too early to see them reports them pending; they
// fail only when a later start shows they never came.

// startEnv is the Env one SessionStart of sess evaluates the assertions under.
func startEnv(h *contract.SessionHistory, sess core.SessionID, source, transcript string) contract.Env {
	return contract.Env{
		Clock: newFakeClock(), History: h,
		Event: hookio.Event{
			HookEventName: "SessionStart", SessionID: sess, CWD: "/tmp/project", Source: source,
			TranscriptPath: transcript,
		},
	}
}

// TestMCPServerRegistered_TheFirstSessionIsPending: the start that opens a project's first session
// runs before the host has connected the MCP server, so it has nothing to observe yet.
func TestMCPServerRegistered_TheFirstSessionIsPending(t *testing.T) {
	contract.DeclareProducer(contract.CMCPRegistered)
	t.Cleanup(contract.ResetProducers)
	a := assertionByID(t, contract.CMCPRegistered)
	h := &contract.SessionHistory{}

	r := a.Check(context.Background(), startEnv(h, "sess-1", "startup", ""))
	require.True(t, r.OK, "the first session's start cannot have seen a handshake yet: %+v", r)
	require.Equal(t, "initialize-pending", r.Observed)

	r = a.Check(context.Background(), startEnv(h, "sess-1", "compact", ""))
	require.True(t, r.OK, "a later start of the same session is still that session's window: %+v", r)
	require.Equal(t, "initialize-pending", r.Observed)
}

// TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext: a whole session that had a prompt
// and ended with no handshake reaching the daemon is the real failure, reported by the next start
// once that session is over. While it is still live, another window's start leaves it pending.
func TestMCPServerRegistered_ASessionWithoutAHandshakeFailsTheNext(t *testing.T) {
	contract.DeclareProducer(contract.CMCPRegistered)
	t.Cleanup(contract.ResetProducers)
	a := assertionByID(t, contract.CMCPRegistered)
	h := &contract.SessionHistory{}
	live := liveSessions{"sess-1": true}

	require.True(t, a.Check(context.Background(), live.env(h, "sess-1", "startup", "")).OK)
	require.True(t, h.NotePrompt("sess-1"), "the awaited session's prompt is recorded")
	r := a.Check(context.Background(), live.env(h, "sess-2", "startup", ""))
	require.True(t, r.OK, "the first session is still running: its handshake may yet come: %+v", r)
	require.Equal(t, "initialize-pending", r.Observed)

	live["sess-1"] = false
	r = a.Check(context.Background(), live.env(h, "sess-3", "startup", ""))
	require.False(t, r.OK, "a session with a turn ended with no handshake: the server is not registered")
	require.Equal(t, "initialize-not-received", r.Observed)
	require.Equal(t, contract.SevInfo, r.Severity)

	h.MCPInitialized = true
	r = a.Check(context.Background(), live.env(h, "sess-4", "startup", ""))
	require.True(t, r.OK)
	require.Equal(t, "initialize-received", r.Observed)
}

// liveSessions is a fake Env.SessionLive: the sessions the daemon's registry would report live.
type liveSessions map[core.SessionID]bool

// env is startEnv with l bound as the Env's SessionLive.
func (l liveSessions) env(h *contract.SessionHistory, sess core.SessionID, source, transcript string) contract.Env {
	e := startEnv(h, sess, source, transcript)
	e.SessionLive = func(id core.SessionID) bool { return l[id] }
	return e
}

// TestTranscriptReadable_NotYetWrittenAtStartupIsPending: in -p mode the host writes the transcript's
// first line after SessionStart:startup, so its absence then is not a failure.
func TestTranscriptReadable_NotYetWrittenAtStartupIsPending(t *testing.T) {
	declareWave1(t)
	a := assertionByID(t, contract.CTranscriptReadable)
	h := &contract.SessionHistory{}
	transcript := filepath.Join(t.TempDir(), "sess-1.jsonl")

	r := a.Check(context.Background(), startEnv(h, "sess-1", "startup", transcript))
	require.True(t, r.OK, "a transcript the host has not created yet is pending: %+v", r)
	require.Equal(t, "transcript-pending", r.Observed)

	require.NoError(t, os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0o600))
	r = a.Check(context.Background(), startEnv(h, "sess-2", "startup", filepath.Join(t.TempDir(), "sess-2.jsonl")))
	require.True(t, r.OK, "the earlier transcript appeared, and this start's own is pending: %+v", r)
	require.Equal(t, "transcript-pending", r.Observed)
}

// TestTranscriptReadable_ATranscriptThatNeverAppearedFails: a pending transcript whose session had a
// prompt, ended, and still left no file never appeared, which is the failure the row exists for.
// While that session is live, another window's start keeps it pending.
func TestTranscriptReadable_ATranscriptThatNeverAppearedFails(t *testing.T) {
	declareWave1(t)
	a := assertionByID(t, contract.CTranscriptReadable)
	h := &contract.SessionHistory{}
	dir := t.TempDir()
	missing := filepath.Join(dir, "sess-1.jsonl")
	present := filepath.Join(dir, "sess-2.jsonl")
	require.NoError(t, os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o600))
	live := liveSessions{"sess-1": true}

	require.True(t, a.Check(context.Background(), live.env(h, "sess-1", "startup", missing)).OK)
	require.True(t, h.NotePrompt("sess-1"), "the awaited session's prompt is recorded")
	r := a.Check(context.Background(), live.env(h, "sess-2", "startup", present))
	require.True(t, r.OK, "the first session is still running: %+v", r)
	require.Equal(t, "transcript readable", r.Observed)

	live["sess-1"] = false
	r = a.Check(context.Background(), live.env(h, "sess-3", "startup", present))
	require.False(t, r.OK, "the first session's transcript never appeared")
	require.Equal(t, "an earlier session's transcript_path never appeared", r.Observed)
	require.Equal(t, contract.SevWarn, r.Severity)

	r = a.Check(context.Background(), live.env(h, "sess-4", "startup", present))
	require.True(t, r.OK, "the failure is reported once, then the record moves on: %+v", r)
	require.Equal(t, "transcript readable", r.Observed)
}

// TestTranscriptReadable_ALiveSessionsTranscriptThatAppearsLaterIsForgotten: the awaited session's
// transcript appears after another window started; the next start forgets it without a failure.
func TestTranscriptReadable_ALiveSessionsTranscriptThatAppearsLaterIsForgotten(t *testing.T) {
	declareWave1(t)
	a := assertionByID(t, contract.CTranscriptReadable)
	h := &contract.SessionHistory{}
	dir := t.TempDir()
	first := filepath.Join(dir, "sess-1.jsonl")
	present := filepath.Join(dir, "sess-2.jsonl")
	require.NoError(t, os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o600))
	live := liveSessions{"sess-1": true, "sess-2": true}

	require.True(t, a.Check(context.Background(), live.env(h, "sess-1", "startup", first)).OK)
	require.True(t, a.Check(context.Background(), live.env(h, "sess-2", "startup", present)).OK)
	require.Equal(t, first, h.TranscriptAwaitPath, "a live session's await is carried forward")

	h.NotePrompt("sess-1")
	require.NoError(t, os.WriteFile(first, []byte(`{"type":"user"}`+"\n"), 0o600))
	live["sess-1"] = false
	r := a.Check(context.Background(), live.env(h, "sess-3", "startup", present))
	require.True(t, r.OK, "the transcript appeared: %+v", r)
	require.Empty(t, h.TranscriptAwaitPath)
}

// TestTranscriptReadable_AMissingTranscriptAtCompactFails: a compaction starts from a transcript the
// host has been writing all session, so its absence then is a failure at once.
func TestTranscriptReadable_AMissingTranscriptAtCompactFails(t *testing.T) {
	declareWave1(t)
	a := assertionByID(t, contract.CTranscriptReadable)
	r := a.Check(context.Background(),
		startEnv(&contract.SessionHistory{}, "sess-1", "compact", filepath.Join(t.TempDir(), "gone.jsonl")))
	require.False(t, r.OK)
	require.Equal(t, "transcript_path does not exist", r.Observed)
}

// TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending is the w13-diag review's
// finding: a second session that starts while the first has not had a turn yet (two terminals
// opened in a row) must not fail the first session's handshake. Nothing proves that session's host
// should have connected the server by then.
func TestMCPServerRegistered_ASecondWindowBeforeTheFirstHasTurnedIsPending(t *testing.T) {
	contract.DeclareProducer(contract.CMCPRegistered)
	t.Cleanup(contract.ResetProducers)
	a := assertionByID(t, contract.CMCPRegistered)
	h := &contract.SessionHistory{}

	require.True(t, a.Check(context.Background(), startEnv(h, "sess-1", "startup", "")).OK)
	r := a.Check(context.Background(), startEnv(h, "sess-2", "startup", ""))
	require.True(t, r.OK, "the first session has had no turn, so its handshake is not overdue: %+v", r)
	require.Equal(t, "initialize-pending", r.Observed)
}

// TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending is the same finding for the
// transcript: the host creates a session's transcript with its first prompt, so a session that has
// not had one yet — or that ended without one — has no transcript due, and another session's start
// must not report it as one that never appeared.
func TestTranscriptReadable_ASecondWindowBeforeTheFirstPromptIsPending(t *testing.T) {
	declareWave1(t)
	a := assertionByID(t, contract.CTranscriptReadable)
	h := &contract.SessionHistory{}
	dir := t.TempDir()
	unwritten := filepath.Join(dir, "sess-1.jsonl")
	present := filepath.Join(dir, "sess-2.jsonl")
	require.NoError(t, os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o600))

	require.True(t, a.Check(context.Background(), startEnv(h, "sess-1", "startup", unwritten)).OK)
	r := a.Check(context.Background(), startEnv(h, "sess-2", "startup", present))
	require.True(t, r.OK, "the first session has had no prompt, so no transcript is due: %+v", r)
	require.Equal(t, "transcript readable", r.Observed)
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// The transcript the elimination prompt's event names. The assistant line AFTER the user's prompt
// is the agent's reply to it, already written when a late replay runs: the approach must still be
// the turn the user rejected, not the reply.
const (
	stmtSession   = core.SessionID("sess-user-statement")
	stmtPath      = "src/db.ts"
	stmtApproach  = "I'll widen the pool timeout in src/db.ts."
	stmtPrompt    = "That didn't work, the pool is still saturated."
	stmtLaterText = "Let me disable pooling instead."
)

func stmtTranscript(t *testing.T) string {
	t.Helper()
	line := func(role string, content any) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "message": map[string]any{"role": role, "content": content},
		})
		require.NoError(t, err)
		return string(b) + "\n"
	}
	text := func(s string) []map[string]string { return []map[string]string{{"type": "text", "text": s}} }
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	body := line("assistant", text(stmtApproach+"\nThen rerun the tests.")) +
		line("user", stmtPrompt) +
		line("assistant", text(stmtLaterText))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// userStatementRecords returns the session's active user-statement eliminations.
func userStatementRecords(t *testing.T, led negknow.Ledger) []negknow.Record {
	t.Helper()
	ctx := negknow.WithCaller(context.Background(), negknow.Caller{Session: stmtSession})
	recs, err := led.Active(ctx, negknow.ScopeSession)
	require.NoError(t, err)
	var out []negknow.Record
	for _, r := range recs {
		if r.Source == negknow.SourceUserStatement {
			out = append(out, r)
		}
	}
	return out
}

// TestWireObserver_UserStatementBecomesElimination drives the production path end to end: a
// captured prompt that states an elimination becomes a ledger record already_tried answers
// active, a benign prompt records nothing (and opens no ledger), and a redelivery of the
// elimination prompt under its lease adds no second record.
func TestWireObserver_UserStatementBecomesElimination(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root) // the delivery journal that leases the prompt needs the daemon lock
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		if l := o.LedgerHandle(); l != nil {
			_ = l.Close()
		}
	})
	ctx := context.Background()
	transcript := stmtTranscript(t)

	abs := filepath.Join(root, filepath.FromSlash(stmtPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("export const pool = new Pool({ timeout: 60 });\n"), 0o600))
	in, err := json.Marshal(map[string]string{"file_path": abs, "old_string": "30", "new_string": "60"})
	require.NoError(t, err)
	// edit observes an Edit of stmtPath: the target a statement in the NEXT prompt is placed on.
	edit := func(id core.ToolUseID) {
		t.Helper()
		require.NoError(t, dd.svc.ObserveTool(ctx, hookio.Event{
			HookEventName: "PostToolUse", SessionID: stmtSession, CWD: root, ToolName: "Edit",
			ToolUseID: id, ToolInput: in,
			ToolResponse: json.RawMessage(`{"content":"The file has been updated."}`),
		}))
	}

	// A benign prompt: captured, but nothing for the ledger, which is not even opened.
	_, err = dd.svc.ObservePrompt(observer.WithPromptCaptureOnly(ctx), hookio.Event{
		HookEventName: "UserPromptSubmit", SessionID: stmtSession, CWD: root,
		Prompt: "looks good, carry on", TranscriptPath: transcript,
	})
	require.NoError(t, err)
	require.Nil(t, o.LedgerHandle(), "a prompt stating no elimination must not open the ledger")

	edit("toolu_stmt_edit")

	// The elimination prompt, through the real delivery path: WAL line, lease, worker capture.
	req := ipc.Request{
		Op: ipc.OpObservePrompt, Session: stmtSession, Reply: true, TS: core.NowMilli(dd.clk),
		Nonce: testDeliveryToken('d'),
		Event: &hookio.Event{
			HookEventName: "UserPromptSubmit", SessionID: stmtSession, CWD: root,
			Prompt: stmtPrompt, TranscriptPath: transcript,
		},
		Capture: admittedCapture(`{"hook_event_name":"UserPromptSubmit"}`),
	}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, dd.ing.Accept(req, line))
	require.Equal(t, dispatchSettled, dd.ing.dispatch(ctx, dd.runIngested, <-dd.ing.ring))

	led := o.LedgerHandle()
	require.NotNil(t, led, "a captured elimination statement must open the ledger")
	recs := userStatementRecords(t, led)
	require.Len(t, recs, 1, "one user-stated elimination")
	require.Equal(t, stmtPath, recs[0].Target)
	require.Equal(t, stmtApproach, recs[0].Approach,
		"the approach is the first line of the assistant turn BEFORE the prompt, not the reply after it")
	require.True(t, strings.HasPrefix(recs[0].Reason, "user stated: "), "reason %q", recs[0].Reason)

	callerCtx := negknow.WithCaller(ctx, negknow.Caller{Session: stmtSession})
	ans, err := led.Query(callerCtx, stmtPath, stmtApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State, "already_tried must answer the stated elimination active")

	// A redelivery of the same prompt under its leased observation identity is absorbed by the
	// observer and must not mint a second record.
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	lease, ok := journal.leases[req.Nonce]
	require.True(t, ok, "fixture sanity: the prompt delivery was leased")
	require.True(t, dd.runIngested(observer.WithObservation(ctx, lease.ObservationID), req).OK)
	require.Len(t, userStatementRecords(t, led), 1, "a redelivered prompt must not duplicate the record")

	// And a second, unleased capture of the identical statement, after another edit of the same
	// file, is deduplicated by the ledger.
	edit("toolu_stmt_edit_2")
	_, err = dd.svc.ObservePrompt(observer.WithPromptCaptureOnly(ctx), *req.Event)
	require.NoError(t, err)
	require.Len(t, userStatementRecords(t, led), 1, "an identical statement must not duplicate the record")
}

// TestStatementApproach pins the approach shaping: first non-empty line, trimmed, cut on a rune
// boundary.
func TestStatementApproach(t *testing.T) {
	require.Equal(t, "", statementApproach(""))
	require.Equal(t, "first", statementApproach("\n  \n  first  \nsecond"))
	long := strings.Repeat("é", maxStatementApproachBytes) // two bytes per rune
	got := statementApproach(long)
	require.LessOrEqual(t, len(got), maxStatementApproachBytes)
	require.True(t, strings.HasPrefix(long, got))
	require.Equal(t, maxStatementApproachBytes, len(got), "an even cut lands on a rune start")
}

// TestIngestUserStatement_RedactsBeforeTheLedger pins that a secret in an elimination prompt, or
// in the assistant turn it rejects, reaches the append-only ledger only as a placeholder: the
// production ledger is opened with no Deps.Redact of its own.
func TestIngestUserStatement_RedactsBeforeTheLedger(t *testing.T) {
	root := t.TempDir()
	_, _, o := wireTestDaemon(t, root, nil)
	t.Cleanup(func() {
		if l := o.LedgerHandle(); l != nil {
			_ = l.Close()
		}
	})
	const key = "AKIA" + "IOSFODNN7EXAMPLE"
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	b, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"role": "assistant", "content": []map[string]string{{"type": "text", "text": "I'll set aws_access_key_id = " + key}},
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(transcript, append(b, '\n'), 0o600))

	ingestUserStatement(context.Background(), o.OpenLedger, NewLiveRedactor(o.CurrentCfg), logging.Nop(),
		observer.PromptCapture{
			Session: stmtSession, Turn: 1, LastEditPath: stmtPath, TranscriptPath: transcript,
			Prompt: "that didn't work, aws_access_key_id = " + key + " still fails",
		})

	led := o.LedgerHandle()
	require.NotNil(t, led, "the statement must have opened the ledger")
	recs := userStatementRecords(t, led)
	require.Len(t, recs, 1)
	require.NotContains(t, recs[0].Reason, key)
	require.Contains(t, recs[0].Reason, "«redacted:")
	require.NotContains(t, recs[0].Approach, key)
	require.Contains(t, recs[0].Approach, "«redacted:")
	raw, err := os.ReadFile(filepath.Join(paths.Of(root).Records, "eliminations.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), key, "the append-only log must not hold the secret")
}

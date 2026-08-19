package eval_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// transcriptFixtures is the directory holding the recorded-transcript fixtures.
func transcriptFixtures(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "fixtures", "transcripts")
}

// oneFixture copies a single fixture into a fresh directory, so an import sees exactly it.
func oneFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(transcriptFixtures(t), name))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o600))
	return dir
}

// importOne runs an import of one fixture into a temp destination and returns the report plus the
// sessions that landed.
func importOne(t *testing.T, fixture string, redact bool) (eval.ImportReport, []eval.Session) {
	t.Helper()
	to := t.TempDir()
	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, fixture), To: to, Redact: redact,
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	sessions, err := eval.New(eval.Options{}).Load(to)
	require.NoError(t, err)
	return rep, sessions
}

// TestImport_MapsToolUseAndResult: a tool_result record is not a turn of its own — it is the
// answer to a tool_use, and attaching it by tool_use_id is what makes the result's tokens count
// toward the block that produced them.
func TestImport_MapsToolUseAndResult(t *testing.T) {
	rep, sessions := importOne(t, "basic.jsonl", true)

	require.Equal(t, 1, rep.Files)
	require.Equal(t, 1, rep.Sessions)
	require.Len(t, sessions, 1)

	s := sessions[0]
	require.Len(t, s.Turns, 4, "six records, two of which are tool results attached to earlier calls")
	require.Equal(t, rep.Turns, len(s.Turns))

	var calls []eval.ToolCall
	for _, turn := range s.Turns {
		calls = append(calls, turn.ToolCalls...)
	}
	require.Len(t, calls, 2)
	require.Equal(t, core.ToolUseID("toolu_01"), calls[0].ID)
	require.Equal(t, "Read", calls[0].Name)
	require.Equal(t, []string{"src/auth/handler.go"}, calls[0].Paths)
	require.Contains(t, string(calls[0].Result), "package auth")
	require.Contains(t, string(calls[1].Result), "applied 1 edit")
}

// TestImport_TokensFromUsage: the recorded usage is a better number than len/4, so it is used when
// present.
func TestImport_TokensFromUsage(t *testing.T) {
	_, sessions := importOne(t, "basic.jsonl", true)

	assistant := sessions[0].Turns[1]
	require.Equal(t, "assistant", assistant.Role)
	require.Equal(t, core.Tokens(140), assistant.Tokens)

	user := sessions[0].Turns[0]
	require.Positive(t, user.Tokens, "a user turn has no usage block, so len/4 stands in")
}

// TestImport_CompactBoundaryBecomesCompactionAt is what makes an imported session replayable at
// all: without the boundary there is nothing to fork at.
func TestImport_CompactBoundaryBecomesCompactionAt(t *testing.T) {
	_, sessions := importOne(t, "compact-boundary.jsonl", true)

	require.Equal(t, []core.TurnIndex{3}, sessions[0].CompactionAt)
	require.Len(t, sessions[0].Turns, 5)
}

// TestImport_UnknownRecordSkippedNotFatal: transcripts carry record types this importer has never
// heard of, and refusing the whole file over one of them would make the recorded tier unusable.
func TestImport_UnknownRecordSkippedNotFatal(t *testing.T) {
	rep, sessions := importOne(t, "unknown-record.jsonl", true)

	require.Len(t, sessions[0].Turns, 2)
	require.Equal(t, []string{"nonsense"}, rep.Skipped)
}

// TestImport_MarksImportedSessionsAsRecorded: an imported session is not synthetic, and the
// corpus tier a number was computed over must never be guessable.
func TestImport_MarksImportedSessionsAsRecorded(t *testing.T) {
	_, sessions := importOne(t, "basic.jsonl", true)

	require.False(t, sessions[0].Synthetic)
	require.Equal(t, "recorded", sessions[0].Meta["corpusTier"])
	require.Equal(t, "basic", sessions[0].ID)
}

// TestImport_RefusesDestinationInsideRepo is the mechanical form of "real sessions never
// committed": the importer will not write where git could pick the files up.
func TestImport_RefusesDestinationInsideRepo(t *testing.T) {
	inside := filepath.Join(moduleRoot(t), "testdata", "sessions", "recorded")

	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, "basic.jsonl"), To: inside, Redact: true,
		Getenv: func(string) string { return "" },
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "repository")
	require.Equal(t, 0, rep.Sessions)

	entries, readErr := os.ReadDir(inside)
	if readErr == nil {
		for _, e := range entries {
			require.NotEqual(t, ".json", filepath.Ext(e.Name()), "nothing may be written there")
		}
	}
}

// TestImport_RequiresSessionsDir: with no destination and no environment variable there is nowhere
// safe to put user transcripts, and guessing would be the wrong answer.
func TestImport_RequiresSessionsDir(t *testing.T) {
	_, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, "basic.jsonl"), Redact: true,
		Getenv: func(string) string { return "" },
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "QOMPACK_SESSIONS_DIR")
}

// TestImport_UsesSessionsDirFromEnv: the variable is read through the injected Getenv, never
// through a bare os.Getenv, so these tests never mutate real process environment and can run in
// parallel with everything else.
func TestImport_UsesSessionsDirFromEnv(t *testing.T) {
	to := t.TempDir()

	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, "basic.jsonl"), Redact: true,
		Getenv: func(k string) string {
			if k == "QOMPACK_SESSIONS_DIR" {
				return to
			}
			return ""
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, rep.Sessions)

	entries, err := os.ReadDir(to)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestImport_LimitStopsEarly keeps a huge transcript directory from being imported wholesale.
func TestImport_LimitStopsEarly(t *testing.T) {
	from := t.TempDir()
	for _, name := range []string{"basic.jsonl", "compact-boundary.jsonl", "unknown-record.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(transcriptFixtures(t), name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(from, name), raw, 0o600))
	}

	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: from, To: t.TempDir(), Limit: 2, Redact: true,
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Equal(t, 2, rep.Sessions)
}

// ── Redact ──────────────────────────────────────────────────────────────────────────────────

// fixtureSecrets are the literal secrets planted in secrets.jsonl, one per redaction rule.
var fixtureSecrets = []string{
	`C:\Users\alice`,
	"alice@example.com",
	"BEGIN RSA PRIVATE KEY",
	"@@SEC_GH_PAT_ALPHA@@",
	"hunter2secret",
	"abcdefghijklmnop0123",
	"@@SEC_JWT@@",
	"admin:s3cr3tpw@",
}

// sessionText flattens everything Redact is supposed to reach.
func sessionText(s eval.Session) string {
	var b strings.Builder
	for _, turn := range s.Turns {
		b.WriteString(turn.Text)
		b.WriteString("\n")
		for _, tc := range turn.ToolCalls {
			b.Write(tc.Args)
			b.Write(tc.Result)
			b.WriteString(strings.Join(tc.Paths, " "))
			b.WriteString("\n")
		}
	}
	keys := make([]string, 0, len(s.Meta))
	for k := range s.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(s.Meta[k])
		b.WriteString("\n")
	}
	return b.String()
}

// TestRedact_AllEightRules: recorded transcripts carry user code, prompts, home paths and
// secrets, so every rule has to actually fire on a fixture that plants one instance of each.
func TestRedact_AllEightRules(t *testing.T) {
	to := t.TempDir()
	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, "secrets.jsonl"), To: to, Redact: true,
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Equal(t, 8, rep.RedactedSpans, "one span per rule")

	sessions, err := eval.New(eval.Options{}).Load(to)
	require.NoError(t, err)
	text := sessionText(sessions[0])

	for _, secret := range fixtureSecrets {
		require.NotContains(t, text, secret)
	}
	for _, marker := range []string{"<HOME>", "<EMAIL>", "<KEY>", "<TOKEN>", "<REDACTED>", "<JWT>"} {
		require.Contains(t, text, marker)
	}
}

// TestRedact_NoRedactLeavesSecretsIntact proves the flag does something, so the second
// environment variable guarding it is guarding a real capability.
func TestRedact_NoRedactLeavesSecretsIntact(t *testing.T) {
	to := t.TempDir()
	rep, err := eval.Import(context.Background(), eval.ImportOptions{
		From: oneFixture(t, "secrets.jsonl"), To: to, Redact: false,
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Equal(t, 0, rep.RedactedSpans)

	sessions, err := eval.New(eval.Options{}).Load(to)
	require.NoError(t, err)
	require.Contains(t, sessionText(sessions[0]), "alice@example.com")
}

// TestRedact_Idempotent: redacting twice must be indistinguishable from redacting once, or a
// re-import would keep mangling already-clean text.
func TestRedact_Idempotent(t *testing.T) {
	_, sessions := importOne(t, "secrets.jsonl", false)

	once, n1 := eval.Redact(sessions[0])
	twice, _ := eval.Redact(once)

	require.Positive(t, n1)
	require.Equal(t, once, twice)
}

// redactText runs one string through Redact and returns the scrubbed text and the span count, so
// a rule-level case does not have to build a transcript fixture to state what it means.
func redactText(t *testing.T, in string) (string, int) {
	t.Helper()
	out, n := eval.Redact(eval.Session{
		ID:    "redact-text",
		Turns: []eval.Turn{{Index: 0, Role: "user", Text: in, Tokens: 1}},
	})
	return out.Turns[0].Text, n
}

// TestRedact_URLCredentialsRejectsAMarkerUsername pins the minimized input from fuzz seed
// 4ca9f371d73b9950, where two rules interacted: the e-mail rule leaves "<EMAIL>" exactly where a
// URL user name sits, and the URL-credentials rule used to accept that marker as a user name, so
// a second pass rewrote "a://<EMAIL>:0@" into "a://<REDACTED>@".
//
// It pins the output rather than only re-asserting idempotency, because both spellings are stable
// on their own — the bug was which of the two this string settles on, and a rule change that
// silently swapped them would still redact, just not as the ordering above intends.
func TestRedact_URLCredentialsRejectsAMarkerUsername(t *testing.T) {
	const input = "a://0@0.0:0@"

	once, first := redactText(t, input)
	require.Equal(t, "a://<EMAIL>:0@", once)
	require.Equal(t, 1, first, "one span: the e-mail rule, and nothing after it")

	twice, second := redactText(t, once)
	require.Equal(t, once, twice)
	require.Zero(t, second, "an already-redacted string has no spans left to replace")
}

// TestRedact_PreservesTurnCountAndTokens: redaction changes what a session says, never its shape,
// because the shape is what the replay numbers are computed from.
func TestRedact_PreservesTurnCountAndTokens(t *testing.T) {
	_, sessions := importOne(t, "secrets.jsonl", false)
	before := sessions[0]

	after, _ := eval.Redact(before)

	require.Len(t, after.Turns, len(before.Turns))
	for i := range before.Turns {
		require.Equal(t, before.Turns[i].Role, after.Turns[i].Role)
		require.Equal(t, before.Turns[i].Tokens, after.Turns[i].Tokens)
		require.Equal(t, before.Turns[i].Index, after.Turns[i].Index)
		require.Len(t, after.Turns[i].ToolCalls, len(before.Turns[i].ToolCalls))
	}
	require.Equal(t, before.CompactionAt, after.CompactionAt)
}

// TestRedact_DoesNotMutateItsInput: Redact returns a session, so the caller's copy has to survive
// unchanged or a --no-redact re-run would silently see scrubbed input.
func TestRedact_DoesNotMutateItsInput(t *testing.T) {
	_, sessions := importOne(t, "secrets.jsonl", false)
	original := sessionText(sessions[0])

	_, _ = eval.Redact(sessions[0])

	require.Equal(t, original, sessionText(sessions[0]))
}

// ── qompack eval import ─────────────────────────────────────────────────────────────────────

// TestImportCommand_PrintsReport: the command's whole output is one JSON object, so a script can
// consume it.
func TestImportCommand_PrintsReport(t *testing.T) {
	var out bytes.Buffer
	code := eval.ImportCommand(
		[]string{"--from", oneFixture(t, "basic.jsonl"), "--to", t.TempDir()},
		&out, config.Env{Getenv: func(string) string { return "" }})

	require.Equal(t, 0, code)

	var rep eval.ImportReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &rep))
	require.Equal(t, 1, rep.Sessions)
}

// TestImportCommand_NoRedactRequiresEnv: an accidental unredacted import is a loss-of-privacy
// event, not a convenience, so the flag alone is not enough.
func TestImportCommand_NoRedactRequiresEnv(t *testing.T) {
	var out bytes.Buffer
	code := eval.ImportCommand(
		[]string{"--from", oneFixture(t, "secrets.jsonl"), "--to", t.TempDir(), "--no-redact"},
		&out, config.Env{Getenv: func(string) string { return "" }})

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "QOMPACK_EVAL_ALLOW_UNREDACTED")
}

// TestImportCommand_NoRedactWithEnvSucceeds completes the pair: the guard is a guard, not a ban.
func TestImportCommand_NoRedactWithEnvSucceeds(t *testing.T) {
	var out bytes.Buffer
	code := eval.ImportCommand(
		[]string{"--from", oneFixture(t, "secrets.jsonl"), "--to", t.TempDir(), "--no-redact"},
		&out, config.Env{Getenv: func(k string) string {
			if k == "QOMPACK_EVAL_ALLOW_UNREDACTED" {
				return "1"
			}
			return ""
		}})

	require.Equal(t, 0, code)
}

// TestImportCommand_ErrorsAreReportedNotPanicked keeps a bad flag from taking the process down.
func TestImportCommand_ErrorsAreReportedNotPanicked(t *testing.T) {
	var out bytes.Buffer
	code := eval.ImportCommand([]string{"--nonsense"}, &out,
		config.Env{Getenv: func(string) string { return "" }})
	require.Equal(t, 1, code)
}

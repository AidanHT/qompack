package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// V6-AUTH-1 on the hook client, before the spool/WAL. scopeGuardCapture is what keeps out-of-project
// file bytes off the transport spool entirely — the guarantee the daemon gate alone cannot make,
// because the raw line touches the spool before admission runs. A refused capture is reduced to a
// byte-free record: a PROVEN escape is denied, an UNPROVABLE target is unavailable (never a false
// absence). A degraded delivery's already-redacted retained bytes are scoped too; only a complete,
// unambiguous, in-scope object may be kept.

func okCapture(bytesJSON string) hookio.Capture {
	return hookio.Capture{
		Version: core.EvidenceVersion, Bytes: []byte(bytesJSON), SourceFormat: "application/json",
		PolicyVersion: "redact-json/v1", HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityExact, Outcome: core.OutcomeOK, SourceBytes: len(bytesJSON),
	}
}

func TestAdmitHookCapture_RedactionCannotLaunderOriginalScope(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	cfg, err := json.Marshal(map[string]any{"runtime": map[string]any{"redact": map[string]any{"patterns": []string{regexp.QuoteMeta(outside)}}}})
	require.NoError(t, err)
	writeAdmissionConfig(t, root, string(cfg))
	raw, err := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": outside}, "tool_response": "sensitive"})
	require.NoError(t, err)
	env := Env{Getenv: noEnv, HomeDir: t.TempDir(), Clock: testClock()}
	capture, ev, _, err := admitHookCapture(env, root, hookInput{Raw: raw})
	require.NoError(t, err)
	require.Equal(t, core.OutcomeDenied, capture.Outcome)
	require.Empty(t, capture.Bytes)
	require.Equal(t, hookio.Event{}, ev)
}

func TestAdmitHookCapture_DuplicateOriginalPathIsUnavailable(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	raw := []byte(`{"tool_name":"Read","tool_input":{"file_path":"a.txt","file_path":"b.txt"},"tool_response":"content"}`)
	env := Env{Getenv: noEnv, HomeDir: t.TempDir(), Clock: testClock()}
	capture, ev, _, err := admitHookCapture(env, root, hookInput{Raw: raw})
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, core.OutcomeUnavailable, capture.Outcome)
	require.Empty(t, capture.Bytes)
	require.Equal(t, hookio.Event{}, ev)
}

func readEvent(t *testing.T, filePath string) hookio.Event {
	t.Helper()
	in, err := json.Marshal(map[string]any{"file_path": filePath})
	require.NoError(t, err)
	return hookio.Event{HookEventName: "PostToolUse", ToolName: "Read", ToolInput: in}
}

func degradedCapture(bytesJSON string) hookio.Capture {
	c := hookio.Capture{
		Version: core.EvidenceVersion, SourceFormat: "application/json",
		PolicyVersion: "redact-json/v1", HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityTruncated, Outcome: core.OutcomeUnavailable,
		CaptureError: core.CaptureErrorOversize, Truncated: true, SourceBytes: 5 << 20,
	}
	if bytesJSON != "" {
		c.Bytes = []byte(bytesJSON)
	}
	return c
}

func TestScopeGuard_OKOutOfProjectReadDenied(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")

	got := scopeGuardCapture(root, okCapture(`{"tool_name":"Read","tool_response":"SENSITIVE"}`), readEvent(t, outside))

	require.Equal(t, core.OutcomeDenied, got.Outcome, "a proven escape is denied before it can be spooled")
	require.Empty(t, got.Bytes, "no servable bytes may reach the spool")
	require.Equal(t, core.FidelityUnknown, got.Fidelity)
	require.True(t, got.Recorded(), "the denial still travels as an auditable record")
}

func TestScopeGuard_OKDroppedPathReadUnavailable(t *testing.T) {
	root := t.TempDir()
	ev := hookio.Event{HookEventName: "PostToolUse", ToolName: "Read", ToolInput: json.RawMessage(`{}`)}

	got := scopeGuardCapture(root, okCapture(`{"tool_name":"Read"}`), ev)

	require.Equal(t, core.OutcomeUnavailable, got.Outcome, "a path that cannot be proven inside is unavailable, not denied")
	require.Empty(t, got.Bytes)
}

func TestScopeGuard_OKInProjectReadUntouched(t *testing.T) {
	root := t.TempDir()
	got := scopeGuardCapture(root, okCapture(`{"tool_name":"Read","tool_response":"content"}`), readEvent(t, "src/a.go"))
	require.Equal(t, core.OutcomeOK, got.Outcome, "a legitimate in-project read is untouched")
	require.NotEmpty(t, got.Bytes)
}

func TestScopeGuard_PathlessBashOKUntouched(t *testing.T) {
	root := t.TempDir()
	ev := hookio.Event{HookEventName: "PostToolUse", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)}
	got := scopeGuardCapture(root, okCapture(`{"tool_name":"Bash"}`), ev)
	require.Equal(t, core.OutcomeOK, got.Outcome, "a pathless producer is preserved")
	require.NotEmpty(t, got.Bytes)
}

func TestScopeGuard_DegradedTruncatedBytesDropped(t *testing.T) {
	root := t.TempDir()
	// A degraded delivery's retained prefix is not a complete object; it cannot prove its nature, so
	// its bytes are dropped while the classification (Outcome/CaptureError) and SourceBytes stand.
	for _, b := range []string{
		`{"tool_name":"Read","tool_input":{"file_path":"` + filepath.Join(t.TempDir(), "x") + `"`, // out-of-project, cut
		`{"tool_name":"Read","tool_input":{"file_pa`,                                              // cut before path
		`{"tool_name":"Bash","tool_input":{"command":"go test`,                                    // pathless, cut
	} {
		got := scopeGuardCapture(root, degradedCapture(b), hookio.Event{})
		require.Empty(t, got.Bytes, "an unprovable prefix must not retain opaque bytes")
		require.Equal(t, core.OutcomeUnavailable, got.Outcome, "the degraded classification is preserved")
		require.Equal(t, 5<<20, got.SourceBytes, "SourceBytes is preserved as the trace")
	}
}

func TestScopeGuard_DegradedCompletePathlessObjectKept(t *testing.T) {
	root := t.TempDir()
	// The only bytes a degraded record may keep: a single, complete, unambiguous, in-scope object —
	// here a fully-formed pathless Bash payload. Its metadata is provable and unambiguous.
	got := scopeGuardCapture(root, degradedCapture(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`), hookio.Event{})
	require.NotEmpty(t, got.Bytes, "a provably pathless complete object may be retained as evidence")
	require.Equal(t, core.OutcomeUnavailable, got.Outcome)
}

func TestScopeGuard_DegradedCompleteOutOfProjectObjectDropped(t *testing.T) {
	root := t.TempDir()
	b := `{"tool_name":"Read","tool_input":{"file_path":"` + filepath.Join(t.TempDir(), "x") + `"}}`
	got := scopeGuardCapture(root, degradedCapture(b), hookio.Event{})
	require.Empty(t, got.Bytes, "a complete but out-of-project object is dropped")
}

func TestScopeGuard_UnrecordedCaptureUntouched(t *testing.T) {
	// A zero capture — admission never ran — has nothing to guard and must pass through unchanged,
	// or the hook client would publish a classification it never made.
	got := scopeGuardCapture(t.TempDir(), hookio.Capture{}, hookio.Event{})
	require.False(t, got.Recorded())
}

// TestAdmitHookCapture_OutOfProjectReadIsByteFreeWithNoEvent proves the real admission path — not
// just the guard in isolation — returns a byte-free capture AND a cleared Event, so ipc.WithCapture
// cannot compose a spool line that carries the out-of-project path forward.
func TestAdmitHookCapture_OutOfProjectReadIsByteFreeWithNoEvent(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	raw, err := json.Marshal(map[string]any{
		"cwd": root, "session_id": "scope-session", "hook_event_name": "PostToolUse",
		"tool_name": "Read", "tool_input": map[string]any{"file_path": outside},
		"tool_response": map[string]any{"content": "SENSITIVE OUT-OF-PROJECT BYTES"},
	})
	require.NoError(t, err)

	env := Env{Getenv: noEnv, HomeDir: t.TempDir(), Stdin: bytes.NewReader(raw), Clock: testClock()}
	capture, ev, cfg, aerr := admitHookCapture(env, root, hookInput{Raw: raw})
	require.NoError(t, aerr)
	require.NotEqual(t, "off", cfg.Runtime.Mode)

	require.Equal(t, core.OutcomeDenied, capture.Outcome, "the out-of-project delivery is refused at admission")
	require.Empty(t, capture.Bytes, "no out-of-project bytes are carried toward the spool")
	require.Equal(t, hookio.Event{}, ev, "the Event is cleared so composition cannot spool the path")
	// hookclient composes evp = &ev only when the capture is OutcomeOK; a denied capture therefore
	// travels with a nil Event and, now, a cleared ev — so neither the bytes nor the path survive.
}

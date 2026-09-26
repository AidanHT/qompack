package hookio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

func TestCaptureScope_NullLifecycleInputAndAmbiguousNestedEdit(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, ScopeAllow, CaptureScope(root, "", json.RawMessage(`null`)).Verdict)
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "Read", json.RawMessage(`null`)).Verdict)
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "MultiEdit", json.RawMessage(`{"edits":[{"file_path":"../outside","file_path":"inside"}]}`)).Verdict)
}

// These tests pin the V6-AUTH-1 path-scope trust boundary at its single source of truth. Every
// caller (hook client, daemon admission, observer) reads its answer from here, so the containment
// rules — lexical escape, physical junction/symlink swap, every path in a multi-edit, and the
// fragment case where only a truncated prefix is available — are asserted once, against real paths.

func scopeInput(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestCaptureScope_InsidePositiveCarriesPrimaryKey(t *testing.T) {
	root := t.TempDir()
	sc := CaptureScope(root, "Read", scopeInput(t, map[string]any{"file_path": "src/auth.go"}))
	require.Equal(t, ScopeAllow, sc.Verdict)

	want, err := paths.Norm(root, "src/auth.go")
	require.NoError(t, err)
	require.Equal(t, paths.Key(want), sc.PrimaryKey, "the primary key is read from the one place containment was decided")
}

func TestCaptureScope_AbsolutePathOutsideProjectRefused(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt") // a sibling temp dir, never under root
	sc := CaptureScope(root, "Read", scopeInput(t, map[string]any{"file_path": outside}))
	require.Equal(t, ScopeOutOfProject, sc.Verdict)
	require.Empty(t, sc.PrimaryKey, "a refused capture carries no key")
}

func TestCaptureScope_TraversalOutsideProjectRefused(t *testing.T) {
	root := t.TempDir()
	sc := CaptureScope(root, "Edit", scopeInput(t, map[string]any{"file_path": "../../etc/passwd"}))
	require.Equal(t, ScopeOutOfProject, sc.Verdict)
}

func TestCaptureScope_ChecksEveryPathNotJustTheFirst(t *testing.T) {
	root := t.TempDir()
	// A MultiEdit whose top-level file_path is in-project but whose SECOND edit escapes must be
	// refused on the second — the bug the old "raw[0] only" observer path had.
	in := scopeInput(t, map[string]any{
		"file_path": "in_project.go",
		"edits": []map[string]any{
			{"file_path": "also_inside.go"},
			{"file_path": "../../outside.go"},
		},
	})
	sc := CaptureScope(root, "MultiEdit", in)
	require.Equal(t, ScopeOutOfProject, sc.Verdict)
}

func TestCaptureScope_PathlessProducersAllowed(t *testing.T) {
	root := t.TempDir()
	for _, tool := range []string{"Bash", "PowerShell", "UserPromptSubmit", "SubagentStop", "mcp__qompack__record_eliminated"} {
		sc := CaptureScope(root, tool, scopeInput(t, map[string]any{"command": "go test ./..."}))
		require.Equal(t, ScopeAllow, sc.Verdict, "%s is pathless by nature and must be preserved", tool)
		require.Empty(t, sc.PrimaryKey)
	}
}

func TestCaptureScope_UnknownPathlessProducerIsArchiveData(t *testing.T) {
	root := t.TempDir()
	// An unknown producer with no structured path is archive data governed by redaction. It is NOT
	// refused at capture — gaining or losing RETRIEVAL authority for it is a separate boundary.
	sc := CaptureScope(root, "SomeFutureTool", scopeInput(t, map[string]any{"foo": "bar"}))
	require.Equal(t, ScopeAllow, sc.Verdict)
}

func TestCaptureScope_FileProducerWithDroppedPathIsUnprovable(t *testing.T) {
	root := t.TempDir()
	// A Read that names NO structured path: the dropped-path case. It cannot be proven in scope, so
	// it fails closed. It is kept distinct from an out-of-project escape only so they count apart.
	sc := CaptureScope(root, "Read", json.RawMessage(`{}`))
	require.Equal(t, ScopeUnprovable, sc.Verdict)
	// A dropped path cannot be proven inside: it refuses everywhere (recorded as unavailable, not a
	// proven escape and not a false absence).
	require.True(t, sc.Verdict.Refuses())
}

func TestCaptureScope_MalformedToolInputFailsClosed(t *testing.T) {
	root := t.TempDir()
	// Truncated / non-object / not-even-JSON tool input cannot be proven exhaustive → unprovable.
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "Read", json.RawMessage(`{"file_path":`)).Verdict)
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "Bash", json.RawMessage(`[1,2]`)).Verdict)
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "Bash", json.RawMessage(`not json`)).Verdict)
}

func TestCaptureScope_DuplicateStructuredKeyIsAmbiguousAndRefused(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	// json.Unmarshal would silently take the last file_path; a duplicate is ambiguity we refuse to
	// resolve, so an inside/outside pair cannot be laundered by ordering.
	dup := json.RawMessage(`{"file_path":"in.go","file_path":` + string(mustJSON(t, outside)) + `}`)
	require.Equal(t, ScopeUnprovable, CaptureScope(root, "Read", dup).Verdict)
}

func TestCaptureScope_JunctionSwapDefeatsLexicalContainment(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "link")
	// A symlink where the host allows one, and otherwise the NTFS junction this test is named for,
	// which an unprivileged Windows process can create.
	if err := makeDirLink(link, target); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	// "link/secret.txt" is lexically inside root, so paths.Norm accepts it — the exact case where a
	// gate built on Norm alone has nothing to refuse. paths.ResolvesInside walks the link to its
	// out-of-project target and refuses, which is the junction-swap defense the boundary needs.
	require.False(t, paths.ResolvesInside(root, "link/secret.txt"))
	sc := CaptureScope(root, "Read", scopeInput(t, map[string]any{"file_path": "link/secret.txt"}))
	require.Equal(t, ScopeOutOfProject, sc.Verdict)
}

func TestCaptureScopeRaw_CompleteObjectDecidedOnMerits(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")

	full := scopeRawPayload(t, "Read", map[string]any{"file_path": outside}, "<file contents>")
	require.Equal(t, ScopeOutOfProject, CaptureScopeRaw(root, full).Verdict, "a complete out-of-project payload is proven outside")

	in := scopeRawPayload(t, "Read", map[string]any{"file_path": "src/a.go"}, "<contents>")
	require.Equal(t, ScopeAllow, CaptureScopeRaw(root, in).Verdict, "a complete in-project payload is allowed")

	bash := scopeRawPayload(t, "Bash", map[string]any{"command": "go test ./..."}, "<stdout>")
	require.Equal(t, ScopeAllow, CaptureScopeRaw(root, bash).Verdict, "a complete pathless payload is allowed")
}

func TestCaptureScopeRaw_TruncatedPrefixCannotProveNature(t *testing.T) {
	root := t.TempDir()
	// Field order is not a JSON contract: a prefix cut anywhere — even after tool_name/tool_input,
	// even for a pathless producer — cannot prove there is no later path field or shadowing
	// duplicate. Every truncation is Unprovable, so no opaque prefix bytes pass forward.
	inside := scopeRawPayload(t, "Read", map[string]any{"file_path": "src/a.go"}, "<contents>")
	outside := scopeRawPayload(t, "Read", map[string]any{"file_path": filepath.Join(t.TempDir(), "x")}, "<contents>")
	bash := scopeRawPayload(t, "Bash", map[string]any{"command": "go test"}, "<stdout>")
	for _, full := range [][]byte{inside, outside, bash} {
		require.Equal(t, ScopeUnprovable, CaptureScopeRaw(root, cutAfterToolInput(full)).Verdict)
	}
	// tool_input cut before its path is readable: also unprovable.
	require.Equal(t, ScopeUnprovable, CaptureScopeRaw(root, []byte(`{"tool_name":"Read","tool_input":{"file_pa`)).Verdict)
}

func TestCaptureScopeRaw_ResponseFirstPrefixCannotProveNature(t *testing.T) {
	root := t.TempDir()
	// tool_response first, tool_name/tool_input after — and the whole thing cut mid-response. The
	// prefix can prove nothing about the producer or its path, so it must be Unprovable, not passed.
	prefix := []byte(`{"tool_response":"<a very long response that is cut before the`)
	require.Equal(t, ScopeUnprovable, CaptureScopeRaw(root, prefix).Verdict)
}

func TestCaptureScopeRaw_DuplicateOrTrailingRefused(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, ScopeUnprovable,
		CaptureScopeRaw(root, []byte(`{"tool_name":"Bash","tool_name":"Read","tool_input":{}}`)).Verdict,
		"a duplicate tool_name is ambiguous")
	require.Equal(t, ScopeUnprovable,
		CaptureScopeRaw(root, []byte(`{"tool_name":"Bash","tool_input":{"command":"x"}} trailing`)).Verdict,
		"trailing bytes after a complete object are not a single clean payload")
}

// scopeRawPayload builds a hook payload with tool_name, tool_input and a trailing tool_response, in
// that field order, so a cut after tool_input mirrors a real oversize prefix.
func scopeRawPayload(t *testing.T, tool string, input map[string]any, response string) []byte {
	t.Helper()
	inputJSON, err := json.Marshal(input)
	require.NoError(t, err)
	respJSON, err := json.Marshal(response)
	require.NoError(t, err)
	return []byte(`{"tool_name":` + string(mustJSON(t, tool)) + `,"tool_input":` + string(inputJSON) +
		`,"tool_response":` + string(respJSON) + `}`)
}

// cutAfterToolInput truncates a scopeRawPayload immediately after its tool_input value, dropping the
// trailing tool_response and the closing brace — the shape of a bounded prefix of an oversize
// payload.
func cutAfterToolInput(raw []byte) []byte {
	marker := []byte(`,"tool_response":`)
	for i := 0; i+len(marker) <= len(raw); i++ {
		if string(raw[i:i+len(marker)]) == string(marker) {
			return raw[:i]
		}
	}
	return raw
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

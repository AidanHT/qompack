package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// C1.9 / V6-HOST-1 reproduction, written before the fix and kept as the regression. Each test
// states the defect as the behaviour the retrieval layer must have; both were red on the unfixed
// tree (plans/sdd/V6-closeout/hostperm/runs/01-red-before-fix.log, where they ran under their
// original names TestHostDenyRed_ExpandServesAPathTheHostNowDenies and
// TestReReadHashFormRed_JunctionEscapeIsServedThroughAnotherPath).

const (
	redSecretPath   = "config/secret.env"
	redSecretMarker = "V6-HOST-1-DENIED-ARCHIVE-3f9a0c71"
	redOkPath       = "docs/ok.md"
)

// redWriteProjectSettings writes <root>/.claude/settings.json.
func redWriteProjectSettings(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, "settings.json")), []byte(body), 0o600))
}

// TestV6Host_ArchivedPathUnderANewDenyRuleIsRefused: a file captured legitimately, then placed
// under a project permissions.deny Read rule, must not be re-served from the archive.
func TestV6Host_ArchivedPathUnderANewDenyRuleIsRefused(t *testing.T) {
	f := newFixture(t)
	root, id := f.putAndRecord(t, "Read", redSecretPath, redSecretMarker+"\n", 1)
	redWriteProjectSettings(t, f.Root, `{"permissions":{"deny":["Read(./config/secret.env)"]}}`)

	for name, args := range map[string]map[string]any{
		"tool_use_id": {"tool_use_id": string(id), "full": true},
		"root_hash":   {"hash": root.String(), "full": true},
	} {
		t.Run(name, func(t *testing.T) {
			text := responseText(f.call(t, ToolExpand, args))
			require.NotContains(t, text, redSecretMarker, "expand re-served a host-denied path")
			var d deniedBody
			require.NoError(t, json.Unmarshal([]byte(text), &d))
			require.True(t, d.Denied, "the refusal must be explicit: %s", text)
			require.Equal(t, hostDeniedReason, d.Reason, "the refusal names the host's rules, not containment")
		})
	}
	text := responseText(f.call(t, ToolReRead, map[string]any{"path": redSecretPath, "full": true}))
	require.NotContains(t, text, redSecretMarker, "re_read re-served a host-denied path")
	var d deniedBody
	require.NoError(t, json.Unmarshal([]byte(text), &d))
	require.Equal(t, denied(hostDeniedReason), d)
}

// TestV6Auth_ReReadHashFormChecksTheHashOrigins is the V6-AUTH residual this task found while
// enumerating retrieval forms: re_read's `at: sha256:<hash>` form checks the PATH it
// was given, never the origins of the HASH, so the content of a path that fails authorization
// today is served under any other path that passes it.
func TestV6Auth_ReReadHashFormChecksTheHashOrigins(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)
	f.putAndRecord(t, "Read", redOkPath, "ok\n", 2)

	outside := t.TempDir()
	dir := filepath.Dir(filepath.Join(f.Root, filepath.FromSlash(spanAuthPath)))
	require.NoError(t, os.RemoveAll(paths.Long(dir)))
	if err := makeDirLink(dir, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(dir)) })

	body := responseText(f.call(t, ToolReRead, map[string]any{
		"path": redOkPath, "at": root.Hash.String(), "full": true,
	}))
	var served contentBody
	require.NoError(t, json.Unmarshal([]byte(body), &served))
	require.False(t, served.Found && served.Content == text,
		"re_read served an escaping path's archive (%d bytes) through another path", len(served.Content))
	var d deniedBody
	require.NoError(t, json.Unmarshal([]byte(body), &d))
	require.True(t, d.Denied, "the hash form must refuse explicitly (found=%v, %d content bytes)",
		served.Found, len(served.Content))
	require.Equal(t, authorizedDenialReason, d.Reason, "this is containment's refusal, not the host's")
}

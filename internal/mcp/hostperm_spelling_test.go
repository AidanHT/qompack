package mcp

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// hpAliases are spellings Windows resolves to config/secret.env: the Win32 layer strips a final
// segment's trailing dots and spaces and a directory segment's single trailing dot, and
// paths.Norm's EvalSymlinks returns the canonical name, whose history key is the one served. On a
// POSIX filesystem each names a different file that was never captured.
var hpAliases = []string{
	"config/secret.env.",
	"config/secret.env ",
	"config/secret.env...",
	"config/secret.env. . ",
	"config./secret.env",
}

// TestHostPolicy_AnAliasSpellingOfADeniedPathIsRefused is review finding 1/4 of C1.9: the host
// check judged only the caller's spelling while re_read served the canonical file's history, so
// on Windows a trailing dot or space walked past a deny rule on the file's real name.
func TestHostPolicy_AnAliasSpellingOfADeniedPathIsRefused(t *testing.T) {
	f := newHPFixture(t,
		withConfig(func(c *config.Config) { c.Retrieval.EphemeralResults = false }),
		withFiles(map[string]string{hpSecret: hpSecretBody}))
	at := core.NowMilli(f.Clock).Time().Add(time.Hour).Format(time.RFC3339)
	control := responseText(f.call(t, ToolReRead, map[string]any{"path": hpAliases[0], "full": true}))
	if runtime.GOOS == "windows" {
		require.Contains(t, control, hpSecretMark, "control: the alias serves the secret before a rule exists")
	}

	f.writeSettings(t, projectSettings, denyRules("Read(./config/secret.env)"))
	for _, alias := range hpAliases {
		for form, args := range map[string]map[string]any{
			"latest":       {"path": alias, "full": true},
			"line_anchor":  {"path": alias + ":1"},
			"at_turn":      {"path": alias, "at": "turn:1", "full": true},
			"at_timestamp": {"path": alias, "at": at, "full": true},
		} {
			t.Run(form+"/"+alias, func(t *testing.T) {
				text := responseText(f.call(t, ToolReRead, args))
				if runtime.GOOS == "windows" {
					requireRefused(t, text, denied(hostDeniedReason))
					return
				}
				require.NotContains(t, text, hpSecretMark, "on POSIX the alias names an uncaptured file")
			})
		}
	}

	t.Run("dropped withholds a pointer spelled as an alias", func(t *testing.T) {
		f.Drops.Entries = []checkpoint.DropEntry{
			{Kind: "file_pointer", ID: hpAliases[0], Detail: "truncated at budget; re_read(path) still resolves"},
			{Kind: "file_pointer", ID: hpOK, Detail: "truncated at budget; re_read(path) still resolves"},
		}
		resp := f.callOK(t, ToolDropped, map[string]any{}, nil)
		var got droppedBody
		require.NoError(t, json.Unmarshal([]byte(responseText(resp)), &got))
		if runtime.GOOS == "windows" {
			require.Equal(t, f.Drops.Entries[1:], got.Drops, "the alias points into denied archive")
			require.Equal(t, 1, got.Denied)
			return
		}
		require.Equal(t, f.Drops.Entries, got.Drops, "on POSIX the alias was never captured")
	})
}

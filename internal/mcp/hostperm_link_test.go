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

// TestHostPolicy_ALinkSpellingIsJudgedToo is the other half of the host's symlink rule: a deny rule
// applies when EITHER the link's own path or its target matches. paths.Norm adopts a link's
// in-project target, so a check built on the normalized path alone would never see the spelling
// the rule names.
func TestHostPolicy_ALinkSpellingIsJudgedToo(t *testing.T) {
	f := newFixture(t, withFiles(map[string]string{"real/key.pem": "k\n"}))
	link := filepath.Join(f.Root, "lnk")
	if err := makeDirLink(link, filepath.Join(f.Root, "real")); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(link)) })
	_, id := f.putAndRecord(t, "Read", "lnk/key.pem", "LINK-SPELLED-KEY\n", 1)
	hp := &hpFixture{fixture: f}
	hp.writeSettings(t, projectSettings, denyRules("Read(./lnk/**)"))
	text := responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(id)}))
	require.NotContains(t, text, "LINK-SPELLED-KEY")
	var d deniedBody
	require.NoError(t, json.Unmarshal([]byte(text), &d))
	require.Equal(t, denied(hostDeniedReason), d, "a rule on the link's own spelling applies too")
}

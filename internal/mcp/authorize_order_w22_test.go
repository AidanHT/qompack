package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// TestAuthorizeHash_OneReasonOnEveryRead is audit 2's #13 at the MCP surface (D53(a)): one archived
// object, first captured by a pathless shell call, then read through a path the host's rules deny
// and through a path outside the project. Its verdict was always withheld, but the reason named
// whichever refused origin the store's map iteration gave first, so two reads of unchanged state
// disagreed. Every read now gives one reason.
func TestAuthorizeHash_OneReasonOnEveryRead(t *testing.T) {
	f := &hpFixture{fixture: newFixture(t, withConfig(func(c *config.Config) { c.Retrieval.EphemeralResults = false }))}
	const body = "one archived object, two refused origins W22-ORIGIN-ORDER\n"
	root, _ := f.store(t, "Bash", "", body, 1, false)
	for i, path := range []string{hpSecret, "../outside/x.txt", hpOK, "src/other.go"} {
		require.NoError(t, f.Store.RecordToolUse(context.Background(), store.ToolUseRecord{
			ID: core.ToolUseID(fmt.Sprintf("tu-w22-%d", i)), Session: testSession, Turn: core.TurnIndex(2 + i),
			TS: core.NowMilli(f.Clock), Tool: "Read", Root: root, Path: path, Bytes: int64(len(body)),
		}))
	}
	f.writeSettings(t, projectSettings, denyRules("Read(./"+hpSecret+")"))

	reasons := map[string]int{}
	for range 200 {
		text := responseText(f.call(t, ToolExpand, map[string]any{"hash": root.String(), "full": true}))
		require.NotContains(t, text, "W22-ORIGIN-ORDER", "a refused origin withholds the content")
		var d deniedBody
		require.NoError(t, json.Unmarshal([]byte(text), &d), text)
		reasons[d.Reason]++
	}
	require.Len(t, reasons, 1, "one reason for unchanged state, read 200 times: %v", reasons)
}

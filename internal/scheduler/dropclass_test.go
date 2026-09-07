package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDropClassOf_CompactableTable: Qompack.md §2.2's compactable tool set, in both the spec's
// spelling and the wire names Claude Code actually sends.
func TestDropClassOf_CompactableTable(t *testing.T) {
	for _, tool := range []string{
		"FileRead", "Read",
		"Bash", "PowerShell",
		"Grep", "Glob",
		"WebSearch", "WebFetch",
		"FileEdit", "Edit", "MultiEdit",
		"FileWrite", "Write",
	} {
		require.Equal(t, DropOrdinary, DropClassOf(tool, false, false), tool)
	}
}

// TestDropClassOf_PreservedTools: "AgentTool and MCP results are preserved", and anything
// unlisted is not droppable either.
func TestDropClassOf_PreservedTools(t *testing.T) {
	for _, tool := range []string{
		"Task", "Agent", "AgentTool",
		"mcp__qompack__recall", "mcp__other__x",
		"", "NotebookEdit",
	} {
		require.Equal(t, DropNone, DropClassOf(tool, false, false), "%q", tool)
	}
}

// TestDropClassOf_EphemeralBeatsEverything: retrieved content is born ephemeral (§8.7) and is
// the first eviction candidate whatever tool produced it.
func TestDropClassOf_EphemeralBeatsEverything(t *testing.T) {
	require.Equal(t, DropEphemeral, DropClassOf("mcp__qompack__recall", true, false))
	require.Equal(t, DropEphemeral, DropClassOf("mcp__qompack__recall", true, true), "ephemeral outranks superseded")
	require.Equal(t, DropEphemeral, DropClassOf("Task", true, false))
	require.Equal(t, DropEphemeral, DropClassOf("Read", true, false))
	require.Equal(t, DropEphemeral, DropClassOf("", true, false))
}

// TestDropClassOf_SupersededBeatsToolClass: supersession is a stronger statement than the tool
// class (§8.1 item 3), so a superseded Task or MCP result is still droppable.
func TestDropClassOf_SupersededBeatsToolClass(t *testing.T) {
	require.Equal(t, DropSuperseded, DropClassOf("Task", false, true))
	require.Equal(t, DropSuperseded, DropClassOf("mcp__other__x", false, true))
	require.Equal(t, DropSuperseded, DropClassOf("Read", false, true), "superseded outranks ordinary")
	require.Equal(t, DropSuperseded, DropClassOf("NotebookEdit", false, true))
}

// TestDropClassOf_CaseInsensitive: comparison is case-folded and whitespace-trimmed.
func TestDropClassOf_CaseInsensitive(t *testing.T) {
	require.Equal(t, DropOrdinary, DropClassOf("BASH", false, false))
	require.Equal(t, DropOrdinary, DropClassOf(" grep ", false, false))
	require.Equal(t, DropOrdinary, DropClassOf("\tWebFetch\n", false, false))
	require.Equal(t, DropNone, DropClassOf("MCP__Other__X", false, false), "the mcp__ prefix is matched after folding too")
	require.Equal(t, DropNone, DropClassOf(" TASK ", false, false))
}

// TestEvictionRank_Order: ephemeral > superseded > ordinary > none, matching §8.7's "ahead of
// ordinary tool results".
func TestEvictionRank_Order(t *testing.T) {
	require.Equal(t, 3, DropEphemeral.EvictionRank())
	require.Equal(t, 2, DropSuperseded.EvictionRank())
	require.Equal(t, 1, DropOrdinary.EvictionRank())
	require.Equal(t, 0, DropNone.EvictionRank())
	require.Greater(t, DropEphemeral.EvictionRank(), DropSuperseded.EvictionRank())
	require.Greater(t, DropSuperseded.EvictionRank(), DropOrdinary.EvictionRank())
	require.Greater(t, DropOrdinary.EvictionRank(), DropNone.EvictionRank())
}

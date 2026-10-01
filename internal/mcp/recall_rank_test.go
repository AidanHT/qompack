package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The host names a plugin's MCP tool mcp__plugin_<plugin segment>_<server>__<tool>. Every install
// observed so far came from a marketplace entry named `qompack`, so the segment was `qompack`. A
// release installs from an entry named qompack-<os>-<arch> (the host lists it as
// qompack-windows-amd64@qompack, packaging/evidence/c7.5-marketplace/entry-name-probe.txt), and the
// segment the host derives from such an entry has never been observed (audit F2, V6 close-out
// D53(e)). Recall must rank the host's capture of a call to a Qompack tool last whichever segment
// the host uses, and must not mistake any other tool for one.

// TestIsRetrievalSelfRecord_AnyPluginSegment covers both entry spellings, in the forms a host could
// plausibly derive from `qompack-windows-amd64` (kept, or with its hyphens folded to underscores),
// for every one of the eight tools.
func TestIsRetrievalSelfRecord_AnyPluginSegment(t *testing.T) {
	segments := []string{
		"qompack",               // a local or source install from an entry named qompack (observed)
		"qompack-windows-amd64", // a release entry, the name kept as the host lists it
		"qompack_windows_amd64", // the same entry with its hyphens folded
		"qompack-linux-arm64",
	}
	for _, seg := range segments {
		for _, tool := range ToolNames() {
			name := "mcp__plugin_" + seg + "_" + ServerName + "__" + tool
			require.True(t, isRetrievalSelfRecord(name), "%q is the host's capture of a Qompack tool call", name)
		}
	}
	// The server's own ephemeral record of an answer is unchanged.
	require.True(t, isRetrievalSelfRecord(mcpToolPrefix+ToolReRead))
}

// TestIsRetrievalSelfRecord_Negatives is the other half: a tool that is not one of Qompack's
// eight, or a tool of a server that is not Qompack's, is an ordinary capture and keeps its rank.
func TestIsRetrievalSelfRecord_Negatives(t *testing.T) {
	for _, name := range []string{
		"FileRead",
		"Bash",
		// Another plugin's server and tool.
		"mcp__plugin_github_github__search_code",
		// Another plugin's server that happens to have a tool named like one of Qompack's.
		"mcp__plugin_notes_notes__recall",
		"mcp__plugin_qompack-windows-amd64_other__recall",
		// Qompack's server segment, but a tool Qompack does not have.
		"mcp__plugin_qompack_qompack__recall_all",
		"mcp__plugin_qompack_qompack__deploy",
		"mcp__plugin_qompack_qompack__",
		"mcp__plugin_qompack-windows-amd64_qompack__recallx",
		// A server segment that only ends in "qompack".
		"mcp__plugin_tools_notqompack__recall",
		// No plugin segment at all.
		"mcp__plugin__qompack__recall",
		"mcp__plugin_qompack__recall",
		// A segment that hides a second server boundary.
		"mcp__plugin_evil__x_qompack__recall",
		// A standalone (non-plugin) server of another name.
		"mcp__notes__recall",
	} {
		require.False(t, isRetrievalSelfRecord(name), "%q is not a Qompack retrieval self-record", name)
	}
}

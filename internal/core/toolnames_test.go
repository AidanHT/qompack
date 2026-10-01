package core_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// TestCutHostPluginTool pins the host's plugin tool name, mcp__plugin_<entry>_<server>__<tool>, for
// every entry spelling a release could produce, and the names that only resemble it.
func TestCutHostPluginTool(t *testing.T) {
	for name, want := range map[string]string{
		"mcp__plugin_qompack_qompack__recall":               "recall",
		"mcp__plugin_qompack-windows-amd64_qompack__expand": "expand",
		"mcp__plugin_qompack_linux_arm64_qompack__re_read":  "re_read",
		"mcp__plugin_a__qompack__why":                       "why", // the entry "a_" holds no "__"
	} {
		got, ok := core.CutHostPluginTool(name, "qompack")
		require.True(t, ok, "%q", name)
		require.Equal(t, want, got, "%q", name)
	}
	for _, name := range []string{
		"recall",
		"mcp__qompack__recall",
		"mcp__plugin_github_github__search_code",
		"mcp__plugin_qompack-windows-amd64_other__recall",
		"mcp__plugin_tools_notqompack__recall",
		"mcp__plugin__qompack__recall",
		"mcp__plugin_qompack__recall",
		"mcp__plugin_evil__x_qompack__recall",
		"mcp__plugin_qompack_qompack__",
		"mcp__plugin_qompack_qompack",
	} {
		_, ok := core.CutHostPluginTool(name, "qompack")
		require.False(t, ok, "%q is not a tool of the qompack plugin server", name)
	}
}

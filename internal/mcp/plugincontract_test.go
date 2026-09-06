package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The half of the §7.5 plugin-bundle contract that belongs to THIS package.
//
// `plugin-validate` asserts the bundle's shape from tools/devtool, where it can see
// internal/pluginmanifest; the count it checks against is a literal 8, deliberately, so that a
// generator which quietly stopped emitting a tool fails rather than agreeing with itself. That
// literal is only meaningful if the published tool set really is eight, and THAT is a fact about
// internal/mcp — which is why it is asserted here as well as there, from the opposite side.
//
// The V4/V5 verification plans run this by name against ./internal/mcp; devtool's runpatterns
// sub-check fails the build if the name is not here, because a `-run` pattern that matches nothing
// still prints ok and exits 0, and a verification row that cannot fail verifies nothing.

// pluginBundleToolCount is the tool count tools/devtool/pluginvalidate.go pins as wantMCPTools.
//
// Written out rather than imported: tools/devtool is a composition root and nothing may import it
// (§3.2), and in any case a check that read the other side's constant could only ever agree with
// itself. Two independent statements of 8 that must match is the point.
const pluginBundleToolCount = 8

// TestPluginValidateSeesEightTools asserts the published tool set is exactly the eight names
// §8.7 fixes, in the order it fixes them.
func TestPluginValidateSeesEightTools(t *testing.T) {
	t.Parallel()

	names := ToolNames()
	require.Len(t, names, pluginBundleToolCount,
		"plugin-validate pins wantMCPTools = %d; ToolNames() reports %d — one of the two is wrong",
		pluginBundleToolCount, len(names))
	require.Equal(t, []string{
		"recall", "expand", "re_read", "already_tried",
		"record_eliminated", "timeline", "why", "dropped",
	}, names, "the §8.7 table's names and order are the published contract")
}

// TestEveryPublishedToolIsRegisteredOnARealServer asserts the count survives registration.
//
// ToolNames() is a list; what a host receives is whatever Register accepted. A tool whose schema
// failed to compile would be refused by Register and silently absent from tools/list while
// ToolNames() went on claiming eight, so the two are checked against each other rather than each
// against a literal.
func TestEveryPublishedToolIsRegisteredOnARealServer(t *testing.T) {
	t.Parallel()

	got := toolNamesOf(newToolServer(t).Tools())
	require.Equal(t, ToolNames(), got,
		"every name ToolNames() publishes must survive Register onto a real server")
}

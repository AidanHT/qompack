package pluginmanifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// TestBinaryRelPath_IsBinaryRefWithoutThePlaceholder pins the one fact the mod's bridge needs: the
// host's process API expands no ${CLAUDE_PLUGIN_ROOT}, so the mod joins this relative path onto
// the plugin root itself, and it must name the same executable the hooks launch.
func TestBinaryRelPath_IsBinaryRefWithoutThePlaceholder(t *testing.T) {
	require.Equal(t, "bin/qompack", pluginmanifest.BinaryRelPath("linux"))
	require.Equal(t, "bin/qompack", pluginmanifest.BinaryRelPath("darwin"))
	require.Equal(t, "bin/qompack.exe", pluginmanifest.BinaryRelPath("windows"))
	for _, goos := range []string{"linux", "darwin", "windows"} {
		require.Equal(t, "${CLAUDE_PLUGIN_ROOT}/"+pluginmanifest.BinaryRelPath(goos), pluginmanifest.BinaryRef(goos))
	}
}

func TestModTargetSource_RendersOneTarget(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		src := string(pluginmanifest.ModTargetSource(goos))
		require.Contains(t, src, "export const TARGET_GOOS = '"+goos+"'\n")
		require.Contains(t, src, "export const BINARY_REL_PATH = '"+pluginmanifest.BinaryRelPath(goos)+"'\n")
		require.True(t, strings.HasPrefix(src, "// Code generated"), "the file says it is generated")
		require.True(t, strings.HasSuffix(src, "\n") && !strings.HasSuffix(src, "\n\n"), "one final newline")
		require.NotContains(t, src, "\r")
		require.NotContains(t, src, "${", "nothing in the mod's sources relies on host placeholder expansion")
	}
	require.NotEqual(t, pluginmanifest.ModTargetSource("linux"), pluginmanifest.ModTargetSource("windows"))
}

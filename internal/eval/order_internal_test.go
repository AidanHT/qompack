package eval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKeysOfBool_ListsKeysInSortedOrder: the command-check error names the allowed commands from
// keysOfBool, so the list must not take the map's iteration order (D53(a), w20 status audit nit).
// The production set holds one command today; the row pins the order for any set that grows.
func TestKeysOfBool_ListsKeysInSortedOrder(t *testing.T) {
	m := map[string]bool{"go": true, "make": true, "bazel": true, "cargo": true, "npm": true}
	for range 50 {
		require.Equal(t, []string{"bazel", "cargo", "go", "make", "npm"}, keysOfBool(m))
	}
	require.Equal(t, []string{"go"}, keysOfBool(liveCheckCommands))
}

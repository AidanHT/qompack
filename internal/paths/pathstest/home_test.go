package pathstest_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack pins the whole contract in one sequence,
// because IsolateHome changes process-wide state and only one isolation may be live at a time.
func TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("USERPROFILE", realHome)
	t.Setenv("QOMPACK_HOME", filepath.Join(realHome, ".qompack"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(realHome, ".claude"))

	restore, err := pathstest.IsolateHome()
	require.NoError(t, err)
	restored := false
	defer func() {
		if !restored {
			restore()
		}
	}()

	home := pathstest.Home()
	require.NotEmpty(t, home)
	require.NotEqual(t, realHome, home)
	require.DirExists(t, home)
	for _, k := range []string{"HOME", "USERPROFILE"} {
		require.Equal(t, home, os.Getenv(k), "%s must name the isolated home", k)
	}
	got, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, home, got, "os.UserHomeDir, which the product's home lookups end in, must answer the isolated home")
	for _, k := range []string{"QOMPACK_HOME", "CLAUDE_CONFIG_DIR"} {
		_, set := os.LookupEnv(k)
		require.False(t, set, "%s names a user-global location ahead of the home and must be unset", k)
	}
	for _, k := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		v := os.Getenv(k)
		require.NotEmpty(t, v, "%s must be pinned so a child `go build` keeps the real toolchain caches", k)
		require.False(t, strings.HasPrefix(v, home), "%s must not have moved into the isolated home: %s", k, v)
	}
	env := pathstest.Environ()
	require.Contains(t, env, "HOME="+home)
	require.False(t, slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "QOMPACK_HOME=") }))

	_, err = pathstest.IsolateHome()
	require.Error(t, err, "a second isolation while one is live must be refused")

	restore()
	restored = true
	require.Equal(t, realHome, os.Getenv("HOME"))
	require.Equal(t, realHome, os.Getenv("USERPROFILE"))
	require.Equal(t, filepath.Join(realHome, ".qompack"), os.Getenv("QOMPACK_HOME"))
	require.Equal(t, filepath.Join(realHome, ".claude"), os.Getenv("CLAUDE_CONFIG_DIR"))
	require.NoDirExists(t, home, "the isolated home is removed")
	require.Empty(t, pathstest.Home())
	require.Nil(t, pathstest.Environ())
}

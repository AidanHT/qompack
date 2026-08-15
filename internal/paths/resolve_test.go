package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// getenvFunc returns a paths.Resolve-compatible getenv backed by a plain map, so tests never
// touch the real process environment.
func getenvFunc(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestResolve_EnvWins(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tmp, ".git"), 0o755))

	want, err := filepath.Abs("/other")
	require.NoError(t, err)

	got, err := paths.Resolve(getenvFunc(map[string]string{"QOMPACK_PROJECT_ROOT": "/other"}), tmp)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolve_WalksToGitDir(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tmp, ".git"), 0o755))
	deep := filepath.Join(tmp, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	got, err := paths.Resolve(getenvFunc(nil), deep)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(tmp), filepath.Clean(got))
}

func TestResolve_GitFileWorktree(t *testing.T) {
	tmp := t.TempDir()
	gitFile := filepath.Join(tmp, ".git")
	require.NoError(t, os.WriteFile(gitFile, []byte("gitdir: ../elsewhere/.git/worktrees/foo\n"), 0o644))
	sub := filepath.Join(tmp, "a")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	got, err := paths.Resolve(getenvFunc(nil), sub)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(tmp), filepath.Clean(got))
}

func TestResolve_FallsBackToCWD(t *testing.T) {
	tmp := t.TempDir() // a fresh temp dir; nothing under it (or above it, by construction of the
	// OS temp root used across this whole test binary) ever gets a .git marker.
	sub := filepath.Join(tmp, "a")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	got, err := paths.Resolve(getenvFunc(nil), sub)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(sub), filepath.Clean(got))
}

func TestResolve_EmptyCWDWithNoEnv(t *testing.T) {
	_, err := paths.Resolve(getenvFunc(nil), "")
	require.Error(t, err)
}

// TestResolve_PropagatesAbsFailure drives Resolve's own filepath.Abs failure path via a payload
// CWD containing a NUL byte, which no Windows path may contain.
func TestResolve_PropagatesAbsFailure(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: this exercises the windows-specific Abs failure path")
	}
	_, err := paths.Resolve(getenvFunc(nil), "a\x00b")
	require.Error(t, err)
}

func TestGlobal_JoinsHomeAndDotDir(t *testing.T) {
	home := t.TempDir()
	require.Equal(t, filepath.Join(home, ".qompack"), paths.Global(home))
}

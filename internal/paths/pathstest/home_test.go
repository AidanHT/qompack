package pathstest_test

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestIsolateHome_RunsNoGoCommandWhenTheToolchainIsPinned pins that IsolateHome asks the go command
// for the toolchain's locations only when one is not already set.
//
// A test process started by another isolated test process inherits all four pinned, and the go
// command is not a passive reader of the home it runs under: on Linux it writes its telemetry
// counters under os.UserConfigDir ($HOME/.config/go/telemetry). test/guards' sentinel starts a
// child with the home pointing at a fake real home, so a go command run there wrote into it and
// the sentinel failed on Linux. A fake `go` on PATH records whether it ran; the control run, with
// one location unset, proves it is the command IsolateHome would find.
func TestIsolateHome_RunsNoGoCommandWhenTheToolchainIsPinned(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("USERPROFILE", realHome)
	bin := t.TempDir()
	marker := filepath.Join(bin, "ran")
	if runtime.GOOS == "windows" {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "go.bat"),
			[]byte("@echo off\r\necho ran> \""+marker+"\"\r\necho {}\r\n"), 0o700))
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "go"),
			[]byte("#!/bin/sh\necho ran > '"+marker+"'\necho '{}'\n"), 0o700))
	}
	t.Setenv("PATH", bin)
	pinned := map[string]string{
		"GOPATH": filepath.Join(realHome, "gopath"), "GOMODCACHE": filepath.Join(realHome, "modcache"),
		"GOCACHE": filepath.Join(realHome, "gocache"), "GOENV": filepath.Join(realHome, "goenv"),
	}
	for k, v := range pinned {
		t.Setenv(k, v)
	}

	restore, err := pathstest.IsolateHome()
	require.NoError(t, err)
	restore()
	require.NoFileExists(t, marker, "every toolchain location was already pinned, so no go command may run")
	for k, v := range pinned {
		require.Equal(t, v, os.Getenv(k), "%s must be left as it was", k)
	}

	require.NoError(t, os.Unsetenv("GOENV")) // t.Setenv above restores it at the end
	restore, err = pathstest.IsolateHome()
	require.NoError(t, err)
	restore()
	require.FileExists(t, marker, "control: with GOENV unset IsolateHome must ask the go command on PATH")
}

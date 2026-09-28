package pathstest_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
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

// TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder pins that the one `go env` IsolateHome
// runs writes no Go telemetry into the home it runs under.
//
// That `go env` runs before the home moves, so under the caller's home. With no telemetry mode file
// there the go command's mode is "local": it creates a counter file under
// os.UserConfigDir()/go/telemetry/local and, holding no fresh upload token, starts a detached
// telemetry sidecar that keeps writing there after `go env` has returned. When that home is a
// directory the test process removes — the fake real home of
// TestIsolateHome_RedirectsEveryHomeLookupAndPutsItBack — its t.TempDir cleanup raced the sidecar
// and failed with "directory not empty" in the Linux non-root gate. The row points the user config
// dir into a fake real home, gives that home a go env file whose GOMODCACHE only the go command can
// report (the control that it ran), and requires that no telemetry directory appears there.
func TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("USERPROFILE", realHome)
	t.Setenv("XDG_CONFIG_HOME", "")                                    // Linux: the config dir follows HOME
	t.Setenv("APPDATA", filepath.Join(realHome, "AppData", "Roaming")) // Windows: the config dir is APPDATA
	cfg, err := os.UserConfigDir()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(cfg, realHome), "control: the user config dir must lie in the fake real home: %s", cfg)

	fromEnvFile := filepath.Join(realHome, "modcache-from-go-env-file")
	require.NoError(t, os.MkdirAll(filepath.Join(cfg, "go"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cfg, "go", "env"), []byte("GOMODCACHE="+fromEnvFile+"\n"), 0o600))
	for _, k := range []string{"GOENV", "GOMODCACHE"} {
		t.Setenv(k, "") // restored at the end; unset now so the go command resolves them
		require.NoError(t, os.Unsetenv(k))
	}

	restore, err := pathstest.IsolateHome()
	require.NoError(t, err)
	pinned := os.Getenv("GOMODCACHE")
	restore()
	require.Equal(t, fromEnvFile, pinned, "control: GOMODCACHE must be the go command's answer from the go env file")
	require.NoDirExists(t, filepath.Join(cfg, "go", "telemetry"),
		"the `go env` IsolateHome runs wrote Go telemetry into the home it ran under")
}

// TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry pins that a go command a test runs
// in the isolated environment finds Go telemetry off and writes nothing into the isolated home,
// which the restore function removes: a detached telemetry sidecar writing there would race that
// removal. Where the user config dir follows the home (Linux with no XDG_CONFIG_HOME, macOS) the
// helper leaves exactly one file there, the telemetry mode file; where it does not (Windows'
// APPDATA) the go command's telemetry goes to the user's own config dir, which no test removes, and
// the isolated home stays empty.
func TestIsolateHome_GoCommandsUnderTheIsolatedHomeLeaveNoTelemetry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "") // Linux: the config dir follows the (isolated) home
	restore, err := pathstest.IsolateHome()
	require.NoError(t, err)
	defer restore()
	home := pathstest.Home()
	cfg, err := os.UserConfigDir()
	require.NoError(t, err)
	var want []string
	inHome := strings.HasPrefix(cfg, home)
	if inHome {
		want = []string{filepath.Join(cfg, "go", "telemetry", "mode")}
	}

	cmd := exec.CommandContext(context.Background(), "go", "env", "GOTELEMETRY")
	cmd.Env = pathstest.Environ()
	out, err := cmd.Output()
	require.NoError(t, err)
	if inHome {
		require.Equal(t, "off", strings.TrimSpace(string(out)), "the go command must read the isolated home's mode file")
	}

	var files []string
	require.NoError(t, filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	}))
	require.Equal(t, want, files, "a go command run under the isolated home left files there")
}

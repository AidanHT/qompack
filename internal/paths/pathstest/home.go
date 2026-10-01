// Package pathstest keeps a test process away from the user's real home directory.
//
// Qompack keeps a user-global layer under <home>/.qompack (paths.Global): config.json, the token
// estimator's calibration.json, a fallback log directory, and on Windows the staged daemon copies
// under bin/. It also reads Claude Code's own settings under <home>/.claude. Product code finds
// that home through the environment: os.UserHomeDir (USERPROFILE on Windows, HOME elsewhere),
// HOME and USERPROFILE directly, QOMPACK_HOME for the calibration file, and CLAUDE_CONFIG_DIR for
// Claude Code's settings. A test process that inherits the developer's real values reads their
// config and calibration, so its result depends on the machine it runs on, and it can write into
// them: a test's calibration samples would overwrite the user's real factor. ~/.qompack is in the
// product write set (00-ARCHITECTURE.md §13 invariant 7) for the product serving the user, never
// for a test, and nothing may write under ~/.claude at all. After a live UAT the machine running
// the suite has exactly such files.
//
// Main is the TestMain body of every test package whose test binary links a package that resolves
// the home. test/guards' TestGuard_EveryHomeReachingTestPackageIsolatesHome finds those packages
// from the import graph and fails for any that does not call it, and
// TestGuard_IsolatedTestsNeverReadAPoisonedRealHome proves the isolation against a fake real home
// holding a poisoned config.json, calibration.json and Claude Code settings file.
//
// WithoutBackupPrivileges is the other process-level difference a filesystem test meets: an account
// whose token enables the backup and restore privileges, as the hosted Windows runner's does, opens
// past an access-control entry a fixture writes (privileges_windows.go).
//
// The package imports nothing from this module, so the in-package tests of any package can use it
// without an import cycle. internal/testutil cannot offer that: it imports store, config and most of
// the tree, so the in-package tests of those packages cannot import it.
package pathstest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// homeVars are the variables IsolateHome points at the isolated home: os.UserHomeDir reads
// USERPROFILE on Windows and HOME elsewhere, and internal/cli and internal/paths read both.
var homeVars = []string{"HOME", "USERPROFILE"}

// overrideVars are the variables that name a user-global location directly, ahead of the home, and
// so are unset: QOMPACK_HOME is the calibration file's directory (tokens.DefaultCalibPath) and
// CLAUDE_CONFIG_DIR is Claude Code's settings directory (internal/hostperm). Unset, both fall back
// to the isolated home.
var overrideVars = []string{"QOMPACK_HOME", "CLAUDE_CONFIG_DIR"}

// toolchainVars are the Go toolchain's own locations. Unset, the go command derives them from the
// home — GOPATH (and with it the module cache) from HOME or USERPROFILE on every platform, the build
// cache and the go env file from HOME on Linux — so a test that runs `go build` under an isolated
// home would find an empty module cache and a cold build cache. IsolateHome pins each one that is
// unset to the value the go command resolves before the home moves. They name the toolchain's
// caches, not anything of Qompack's.
var toolchainVars = []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"}

// goEnvTimeout bounds the one `go env` IsolateHome runs to learn the toolchain's locations. Test
// harness sizing, not a product bound: `go env` answers in well under a second from a warm
// toolchain, and a minute leaves room for a toolchain switch on a loaded host. When it is exceeded
// or fails, the defaults the go command documents are used instead.
const goEnvTimeout = time.Minute

// Go telemetry. The go command keeps telemetry under os.UserConfigDir()/go/telemetry, and its mode is
// the mode file there (`go help telemetry`; golang.org/x/telemetry/internal/telemetry.Dir.Mode in
// the go1.26.6 toolchain). With no mode file the mode is "local": every go command opens a counter
// file under local/ and, holding no upload token fresh within the day, starts a detached telemetry
// sidecar that keeps writing local/ after the go command has returned. A go command run under a home
// the test process later removes, the isolated home or a test's fake real home, therefore races
// that removal ("unlinkat .../.config/go/telemetry/local: directory not empty" in the Linux gate).
// With the mode "off" the go command opens no counter file and starts no sidecar.
const (
	// telemetryModeOff is the mode file content that turns Go telemetry off, as `go telemetry off`
	// writes it less the date it appends, which the go command does not need to read the mode.
	telemetryModeOff = "off"
	// testTelemetryDirVar is the go command's own override of its telemetry directory
	// (cmd/internal/telemetry and cmd/internal/telemetry/counter read it, go1.23 onward). goEnv
	// points it at a directory holding an "off" mode file: the only way to turn telemetry off for one
	// go command without writing into the caller's own config dir. It is a hook of the go command's
	// tests, not a documented setting, so TestIsolateHome_GoEnvLeavesNoTelemetryInTheHomeItRunsUnder
	// fails if a toolchain stops honouring it.
	testTelemetryDirVar = "TEST_TELEMETRY_DIR"
)

// exitIsolationFailed is Main's exit code when the home cannot be isolated: the tests are not run
// at all rather than run against the real home. It is the code `go test` uses for a build or setup
// failure, distinct from a test failure's 1.
const exitIsolationFailed = 2

var (
	mu       sync.Mutex
	home     string
	environ  []string
	restored bool
)

// Main runs m's tests with the process's home isolated (IsolateHome) and returns the exit code for
// os.Exit. A TestMain with nothing else to do is
//
//	func TestMain(m *testing.M) { os.Exit(pathstest.Main(m)) }
//
// after runs in order once the tests have finished, while the home is still isolated, for a TestMain
// that has its own cleanup. When the home cannot be isolated Main runs no test and returns
// exitIsolationFailed. When a test left HOME or USERPROFILE pointing somewhere else at the end of
// the run, isolation did not hold for the tests after it, and Main fails the run.
func Main(m *testing.M, after ...func()) int {
	restore, err := IsolateHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pathstest: %v; no test was run against the real home\n", err)
		return exitIsolationFailed
	}
	code := m.Run()
	for _, f := range after {
		f()
	}
	if err := stillIsolated(); err != nil {
		fmt.Fprintf(os.Stderr, "pathstest: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	restore()
	return code
}

// IsolateHome points HOME and USERPROFILE at a new, empty temporary directory, unsets QOMPACK_HOME
// and CLAUDE_CONFIG_DIR, and pins the Go toolchain's locations first (toolchainVars), all for the
// whole process. Where the user config dir then lies in the isolated home it turns Go telemetry off
// there (turnOffGoTelemetry), so no go command a test runs can race the directory's removal. It
// returns a function that puts every variable back and removes the directory.
//
// It changes the process environment, so it belongs in TestMain before m.Run, never in a test that
// may run in parallel with another.
func IsolateHome() (func(), error) {
	mu.Lock()
	defer mu.Unlock()
	if home != "" {
		return nil, errors.New("the home is already isolated")
	}

	touched := slices.Concat(toolchainVars, homeVars, overrideVars)
	saved := make(map[string]*string, len(touched))
	for _, k := range touched {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = &v
		} else {
			saved[k] = nil
		}
	}
	restoreEnv := func() {
		for k, v := range saved {
			if v == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *v)
			}
		}
	}

	if err := pinToolchain(); err != nil {
		restoreEnv()
		return nil, err
	}
	dir, err := os.MkdirTemp("", "qompack-test-home-")
	if err != nil {
		restoreEnv()
		return nil, fmt.Errorf("creating an isolated home: %w", err)
	}
	for _, k := range homeVars {
		if err := os.Setenv(k, dir); err != nil {
			restoreEnv()
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("setting %s: %w", k, err)
		}
	}
	for _, k := range overrideVars {
		if err := os.Unsetenv(k); err != nil {
			restoreEnv()
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("unsetting %s: %w", k, err)
		}
	}
	if got, err := os.UserHomeDir(); err != nil || got != dir {
		restoreEnv()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("os.UserHomeDir answers %q (%v) after isolation, not %q", got, err, dir)
	}
	if err := turnOffGoTelemetry(dir); err != nil {
		restoreEnv()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	home, environ, restored = dir, os.Environ(), false
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if restored {
			return
		}
		restored = true
		restoreEnv()
		// Best effort: a detached daemon a test failed to stop may still hold a file in it.
		_ = os.RemoveAll(dir)
		home, environ = "", nil
	}, nil
}

// Home returns the isolated home IsolateHome created, or "" when the process is not isolated.
func Home() string {
	mu.Lock()
	defer mu.Unlock()
	return home
}

// Environ returns a copy of the process environment as IsolateHome left it: the isolated home, the
// pinned toolchain, and none of the changes a test makes afterwards with t.Setenv. A child process
// that must not inherit the calling test's own project or home — a `go build`, or a harness that
// should resolve exactly what CI resolves — starts from this. It is nil when the process is not
// isolated.
func Environ() []string {
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(environ)
}

// stillIsolated reports an error when HOME or USERPROFILE no longer names the isolated home.
func stillIsolated() error {
	mu.Lock()
	defer mu.Unlock()
	for _, k := range homeVars {
		if v := os.Getenv(k); v != home {
			return fmt.Errorf("%s is %q at the end of the run, not the isolated home %q: a test changed "+
				"it without restoring it, and every test after it ran against that directory", k, v, home)
		}
	}
	return nil
}

// pinToolchain sets each unset toolchainVars entry to the value the go command resolves now, while
// the real home is still in place. The go command itself is asked first, because a go env file can
// move any of them; the documented defaults are the fallback when it cannot answer.
//
// When all of them are already set there is nothing to pin, and the go command is not run at all.
// That is the case for a test binary started by another isolated test process, which inherits them
// pinned, and it matters because a go command is not a passive reader of the home it runs under:
// test/guards' sentinel starts such a child with the home pointing at a fake real home and fails on
// any write there (TestIsolateHome_RunsNoGoCommandWhenTheToolchainIsPinned). When it does run, goEnv
// turns its Go telemetry off, the write it would otherwise make there.
func pinToolchain() error {
	if !slices.ContainsFunc(toolchainVars, func(k string) bool { _, set := os.LookupEnv(k); return !set }) {
		return nil
	}
	resolved, err := goEnv()
	if err != nil {
		resolved, err = toolchainDefaults()
		if err != nil {
			return err
		}
	}
	for _, k := range toolchainVars {
		if _, set := os.LookupEnv(k); set || resolved[k] == "" {
			continue
		}
		if err := os.Setenv(k, resolved[k]); err != nil {
			return fmt.Errorf("pinning %s: %w", k, err)
		}
	}
	return nil
}

// goEnv asks the go command on PATH for toolchainVars. The names are spelled out as constants in
// the argument list, in toolchainVars' order, so the command line carries no variable input.
//
// It runs under the caller's home, before the home moves, and must not write Go telemetry there:
// that home may be one the test process removes. Its telemetry directory is a throwaway one holding
// an "off" mode file (testTelemetryDirVar), so it opens no counter file and starts no sidecar, and
// nothing can still be writing the throwaway directory when it is removed.
func goEnv() (map[string]string, error) {
	telemetryDir, err := os.MkdirTemp("", "qompack-go-telemetry-off-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(telemetryDir) }()
	if err := writeTelemetryModeOff(telemetryDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), goEnvTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "env", "-json", "GOPATH", "GOMODCACHE", "GOCACHE", "GOENV")
	cmd.Env = append(os.Environ(), testTelemetryDirVar+"="+telemetryDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	resolved := map[string]string{}
	if err := json.Unmarshal(out, &resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// toolchainDefaults is the go command's documented default for each of toolchainVars, computed
// from the environment as it is now.
func toolchainDefaults() (map[string]string, error) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving the Go toolchain's locations: %w", err)
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(realHome, "go")
	}
	resolved := map[string]string{
		"GOPATH":     gopath,
		"GOMODCACHE": filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod"),
	}
	if d, err := os.UserCacheDir(); err == nil {
		resolved["GOCACHE"] = filepath.Join(d, "go-build")
	}
	if d, err := os.UserConfigDir(); err == nil {
		resolved["GOENV"] = filepath.Join(d, "go", "env")
	}
	return resolved, nil
}

// turnOffGoTelemetry writes an "off" Go telemetry mode file where a go command run in the isolated
// environment reads it, os.UserConfigDir()/go/telemetry/mode, when that lies in the isolated home:
// on Linux with no XDG_CONFIG_HOME ($HOME/.config) and on macOS ($HOME/Library/Application Support).
// Where the config dir lies outside it (APPDATA on Windows, a set XDG_CONFIG_HOME) it is the user's
// own directory, which the helper must not write and no test removes, so nothing is done there; with
// no config dir at all the go command treats telemetry as off already.
func turnOffGoTelemetry(isolated string) error {
	if cfg, err := os.UserConfigDir(); err == nil && within(isolated, cfg) {
		return writeTelemetryModeOff(filepath.Join(cfg, "go", "telemetry"))
	}
	return nil
}

// within reports whether path is dir or lies under it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// writeTelemetryModeOff writes the "off" mode file into the Go telemetry directory dir.
func writeTelemetryModeOff(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("turning Go telemetry off: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(telemetryModeOff), 0o600); err != nil {
		return fmt.Errorf("turning Go telemetry off: %w", err)
	}
	return nil
}

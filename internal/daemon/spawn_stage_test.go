package daemon

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// C1.17: on Windows the daemon runs from a verified copy of the binary under the per-user data
// directory, never from the plugin directory it would otherwise keep from being removed
// (spawn_stage.go). stageBinary itself is platform-independent, so every row here runs everywhere.

// fakeSelf writes a stand-in executable with random contents and returns its path. The extension
// follows the platform, as the real binary's does.
func fakeSelf(t *testing.T) string {
	t.Helper()
	name := "qompack"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(t.TempDir(), "plugin", "bin", name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	b := make([]byte, 64<<10)
	_, err := rand.Read(b)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, b, 0o700))
	return p
}

// requireSameBytes fails unless a and b have identical contents.
func requireSameBytes(t *testing.T, a, b string) {
	t.Helper()
	x, err := os.ReadFile(a)
	require.NoError(t, err)
	y, err := os.ReadFile(b)
	require.NoError(t, err)
	require.Equal(t, x, y, "%s and %s differ", a, b)
}

// unseal clears the read-only mode a staged copy is sealed with, so a test can alter or remove it.
func unseal(t *testing.T, p string) {
	t.Helper()
	require.NoError(t, os.Chmod(p, 0o600))
}

// TestStageBinary_CopiesOnceUnderTheContentAddress: the copy lands at
// <home>/.qompack/bin/<sha256>/qompack[.exe], byte for byte, and a second spawn reuses it.
func TestStageBinary_CopiesOnceUnderTheContentAddress(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	sum, err := fileSHA256(self)
	require.NoError(t, err)

	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(paths.Global(home), stagedBinDir, sum, stagedBinaryName+filepath.Ext(self)), staged)
	requireSameBytes(t, self, staged)
	fi, err := os.Lstat(staged)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())
	if runtime.GOOS != "windows" {
		require.Zero(t, fi.Mode().Perm()&0o077, "a staged copy is its owner's alone: %v", fi.Mode())
	}

	again, err := stageBinary(self, home)
	require.NoError(t, err)
	require.Equal(t, staged, again)
	fi2, err := os.Lstat(again)
	require.NoError(t, err)
	require.Equal(t, fi.ModTime(), fi2.ModTime(), "a verified copy is reused, not rewritten")

	entries, err := os.ReadDir(filepath.Dir(staged))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary copy is left behind")
}

// TestStageBinary_ReplacesATamperedCopy: bytes filed under a version's hash that do not hash to it
// are never run; a verified copy replaces them.
func TestStageBinary_ReplacesATamperedCopy(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	staged, err := stageBinary(self, home)
	require.NoError(t, err)

	unseal(t, staged)
	require.NoError(t, os.WriteFile(staged, []byte("not the daemon"), 0o700))
	require.Error(t, verifyStaged(staged, filepath.Base(filepath.Dir(staged))))

	again, err := stageBinary(self, home)
	require.NoError(t, err)
	require.Equal(t, staged, again)
	requireSameBytes(t, self, again)
}

// TestStageBinary_ReplacesANonFileAtTheTarget: something other than a regular file standing where
// the copy belongs — here a directory; on Unix a symbolic link too (spawn_stage_unix_test.go) — is
// removed and never executed.
func TestStageBinary_ReplacesANonFileAtTheTarget(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	sum, err := fileSHA256(self)
	require.NoError(t, err)
	target := filepath.Join(paths.Global(home), stagedBinDir, sum, stagedBinaryName+filepath.Ext(self))
	require.NoError(t, os.MkdirAll(target, 0o700))

	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	require.Equal(t, target, staged)
	requireSameBytes(t, self, staged)
}

// TestStageBinary_RefusesWithoutAHome: with no home directory there is nowhere Qompack may write a
// copy (§3.3), so staging fails and the caller runs the plugin binary.
func TestStageBinary_RefusesWithoutAHome(t *testing.T) {
	t.Parallel()
	_, err := stageBinary(fakeSelf(t), "")
	require.Error(t, err)
}

// TestStageBinary_UnreadableSourceFails: a binary that cannot be read cannot be staged.
func TestStageBinary_UnreadableSourceFails(t *testing.T) {
	t.Parallel()
	_, err := stageBinary(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	require.Error(t, err)
}

// TestStageBinary_PrunesOtherVersions: staging a version removes the copies of other versions that
// nothing is running, and touches nothing in the directory that stageBinary did not create.
func TestStageBinary_PrunesOtherVersions(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	binRoot := filepath.Join(paths.Global(home), stagedBinDir)
	old := filepath.Join(binRoot, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, os.MkdirAll(old, 0o700))
	oldBin := filepath.Join(old, stagedBinaryName+filepath.Ext(self))
	require.NoError(t, os.WriteFile(oldBin, []byte("an older version"), 0o500))
	foreign := filepath.Join(binRoot, "not-a-version")
	require.NoError(t, os.MkdirAll(foreign, 0o700))

	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	require.NoDirExists(t, old, "an idle older version is pruned")
	require.DirExists(t, foreign, "only directories stageBinary names are ever pruned")
	require.FileExists(t, staged)
}

// TestStageBinary_ConcurrentSpawnersAgree: hooks racing to start the same project's (or different
// projects') daemon all end on one verified copy.
func TestStageBinary_ConcurrentSpawnersAgree(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	const spawners = 8
	var wg sync.WaitGroup
	got := make([]string, spawners)
	errs := make([]error, spawners)
	for i := 0; i < spawners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = stageBinary(self, home)
		}()
	}
	wg.Wait()
	for i := 0; i < spawners; i++ {
		require.NoError(t, errs[i], "spawner %d", i)
		require.Equal(t, got[0], got[i])
	}
	requireSameBytes(t, self, got[0])
	entries, err := os.ReadDir(filepath.Dir(got[0]))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no spawner's temporary copy is left behind")
}

// TestDaemonProgram_StagesOnlyWhereEnabled: the program SpawnDetached starts is the staged copy
// where staging is enabled (Windows) and the hook runs from inside CLAUDE_PLUGIN_ROOT; the binary
// itself elsewhere, and when it is not a plugin's (a build tree, a test's temporary directory, which
// then writes nothing into the home); and the plugin binary — with the failure reported — when
// staging fails.
func TestDaemonProgram_StagesOnlyWhereEnabled(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	pluginRoot := filepath.Dir(filepath.Dir(self))

	got, err := daemonProgram(self, home, pluginRoot, false)
	require.NoError(t, err)
	require.Equal(t, self, got)

	for _, notAPlugin := range []string{"", t.TempDir()} {
		got, err = daemonProgram(self, home, notAPlugin, true)
		require.NoError(t, err)
		require.Equal(t, self, got, "a binary outside CLAUDE_PLUGIN_ROOT (%q) pins no plugin directory", notAPlugin)
	}
	require.NoDirExists(t, filepath.Join(paths.Global(home), stagedBinDir), "and nothing is written into the home")

	got, err = daemonProgram(self, home, pluginRoot, true)
	require.NoError(t, err)
	require.NotEqual(t, self, got)
	requireSameBytes(t, self, got)

	got, err = daemonProgram(self, "", pluginRoot, true)
	require.Error(t, err, "a staging failure is reported")
	require.Equal(t, self, got, "and the plugin binary is started instead")
}

// markPluginRoot gives the directory above self's bin/ the manifest a host plugin directory has.
func markPluginRoot(t *testing.T, self string) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(self))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, pluginManifestRel), []byte(`{"name":"qompack"}`), 0o600))
	return root
}

// TestDaemonProgram_StagesAPluginLayoutWithoutTheVariable: not every process that can start the
// daemon carries CLAUDE_PLUGIN_ROOT — `qompack mcp`, launched from .mcp.json, spawns it lazily, and
// so does a slash command's `qompack` — so a binary laid out as a plugin's, bin/qompack[.exe] with
// .claude-plugin/plugin.json beside bin/, is staged with the variable unset too. A bin/ without the
// manifest is not a plugin's.
func TestDaemonProgram_StagesAPluginLayoutWithoutTheVariable(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()

	got, err := daemonProgram(self, home, "", true)
	require.NoError(t, err)
	require.Equal(t, self, got, "a bin/ with no plugin manifest beside it is not a plugin directory")
	require.NoDirExists(t, filepath.Join(paths.Global(home), stagedBinDir))

	markPluginRoot(t, self)
	got, err = daemonProgram(self, home, "", true)
	require.NoError(t, err)
	require.NotEqual(t, self, got, "a plugin's binary is staged whether or not CLAUDE_PLUGIN_ROOT is set")
	requireSameBytes(t, self, got)

	got, err = daemonProgram(self, home, "", false)
	require.NoError(t, err)
	require.Equal(t, self, got, "and never where the platform does not stage")
}

// TestDaemonWorkingDir: a daemon started from a staged copy runs in the copy's own directory, never
// the directory the spawning hook ran in — on Windows a process's working directory cannot be
// removed either, and the hook may run inside the plugin's own directory. A daemon started from
// the hook's own binary inherits the hook's working directory. Neither is ever the project root,
// which can be past MAX_PATH, where Windows cannot start a process in it at all (test/platform
// TestPlatform_ProjectRootShapes/long failed that way while this was the project root).
func TestDaemonWorkingDir(t *testing.T) {
	t.Parallel()
	self := filepath.Join("plugin", "bin", "qompack.exe")
	staged := filepath.Join("home", ".qompack", "bin", "abc", "qompack.exe")
	require.Equal(t, filepath.Dir(staged), daemonWorkingDir(self, staged))
	require.Empty(t, daemonWorkingDir(self, self), "an unstaged daemon inherits the hook's working directory")

	cmd := buildSpawnCommand(staged, "/proj", nil)
	require.Empty(t, cmd.Dir, "buildSpawnCommand leaves the working directory to spawnDetached")
	require.Equal(t, staged, cmd.Args[0])
}

// TestRunningFromPluginRoot: the daemon's self-check (Run) fires only for an executable inside
// CLAUDE_PLUGIN_ROOT on a platform that stages.
func TestRunningFromPluginRoot(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "plugin")
	inside := filepath.Join(root, "bin", "qompack.exe")
	outside := filepath.Join(t.TempDir(), ".qompack", "bin", "abc", "qompack.exe")
	sibling := root + "-other" + string(filepath.Separator) + "qompack.exe"

	require.True(t, runningFromPluginRoot(inside, root, true))
	require.False(t, runningFromPluginRoot(outside, root, true))
	require.False(t, runningFromPluginRoot(sibling, root, true), "a sibling directory sharing the prefix is not inside")
	require.False(t, runningFromPluginRoot(inside, "", true), "no plugin root, nothing to pin")
	require.False(t, runningFromPluginRoot(inside, root, false), "nothing is pinned where the kernel allows the unlink")

	// A plugin's layout is recognised without the variable, as daemonProgram recognises it.
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o700))
	markPluginRoot(t, inside)
	require.True(t, runningFromPluginRoot(inside, "", true), "a plugin's layout pins its directory, variable or not")
	require.False(t, runningFromPluginRoot(outside, "", true), "a staged copy is not a plugin's layout")
}

// TestRun_ReportsRunningFromThePluginDirectory: a daemon that finds itself running from inside
// CLAUDE_PLUGIN_ROOT where staging was due (Windows) says so Loud — the one visible trace of a
// staging failure that fell back to the plugin binary, which then pins the plugin directory. Where
// the kernel allows the unlink it has nothing to report. The test binary stands in for the plugin
// binary: its own directory is named as the plugin root.
//
// Not parallel: t.Setenv.
func TestRun_ReportsRunningFromThePluginDirectory(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	t.Setenv(pluginRootEnv, filepath.Dir(exe))
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	log := newRecordingLogger()
	d, err := New(Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: log, Clock: core.SystemClock()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	addr, err := ipc.Resolve(d.(*daemon).root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, ensureRunningDialTimeout) },
		stopCleanupBound, 20*time.Millisecond, "the daemon never came up")
	cancel()
	require.NoError(t, <-errCh)

	reported := false
	for _, m := range log.msgs(logLoud) {
		if strings.Contains(m, "running from inside the plugin directory") {
			reported = true
		}
	}
	require.Equal(t, stagingEnabled, reported,
		"the plugin-directory Loud fires exactly where staging was due (stagingEnabled=%v)", stagingEnabled)
}

// BenchmarkStageBinary measures what staging costs the process that spawns the daemon on Windows
// (C1.17): "cold" is a version's first spawn in a home (hash self, copy, fsync, verify the copy),
// "warm" every later one (hash self, verify the existing copy). Both run synchronously inside
// session-start's EnsureRunning and a hook's lazy spawn, before the daemon is even started. The test
// binary stands in for qompack.exe and is larger than it, so the hashing and copying measured here
// are an upper bound. Beside the mean it reports the per-op p50 and p99.
func BenchmarkStageBinary(b *testing.B) {
	self, err := os.Executable()
	if err != nil {
		b.Fatal(err)
	}
	fi, err := os.Stat(self)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("staging %s (%d bytes)", self, fi.Size())

	run := func(b *testing.B, home func(i int) string) {
		took := make([]time.Duration, 0, b.N)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h := home(i)
			start := time.Now()
			if _, err := stageBinary(self, h); err != nil {
				b.Fatal(err)
			}
			took = append(took, time.Since(start))
		}
		b.StopTimer()
		slices.Sort(took)
		pct := func(p int) float64 { return benchMillis(took[(len(took)-1)*p/100]) }
		b.ReportMetric(pct(50), "p50-ms")
		b.ReportMetric(pct(99), "p99-ms")
	}
	b.Run("cold", func(b *testing.B) {
		base := b.TempDir()
		run(b, func(i int) string { return filepath.Join(base, strconv.Itoa(i)) })
	})
	b.Run("warm", func(b *testing.B) {
		home := b.TempDir()
		if _, err := stageBinary(self, home); err != nil {
			b.Fatal(err)
		}
		run(b, func(int) string { return home })
	})
}

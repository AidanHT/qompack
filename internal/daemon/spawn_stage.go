package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

// Where a detached daemon runs from (C1.17).
//
// The resident daemon outlives the Claude Code session that started it: it serves every session in
// the project and exits only after runtime.daemon.idleExitSeconds with no live session. On Windows a
// running executable cannot be deleted, and neither can the directory holding it, so a daemon
// started from the plugin's own bin/qompack.exe kept the plugin directory from being removed: in
// the packaging lane's live session 2 the host's cleanup of a --plugin-dir zip extraction stopped
// half way (plugin.json and .mcp.json gone, bin/ and everything after it left behind), and the same
// lock would break a plugin update or uninstall, which removes the old version's directory.
//
// So on Windows a process running the plugin's own binary starts the daemon from a copy of the
// binary under the per-user data directory instead, one of the two places Qompack writes
// (<home>/.qompack, §3.3): <home>/.qompack/bin/<sha256>/qompack.exe, content-addressed by the
// binary's own SHA-256. The plugin's binary is recognised two ways (pluginDirOf): inside
// CLAUDE_PLUGIN_ROOT, which the host sets for every plugin hook, or by the plugin's layout —
// bin/qompack.exe with .claude-plugin/plugin.json beside bin/ — because not every process that can
// start the daemon is a hook: `qompack mcp`, which the host launches from .mcp.json, spawns it
// lazily, and nothing guarantees that process the variable (the manifest substitutes the
// placeholder into the command line, not into an environment variable). The copy is made once per
// version and verified on every spawn — a regular file, not a link or reparse point, whose bytes
// hash to the name it is filed under and to the spawning process's own executable — before
// anything executes it; a copy that fails verification is replaced, never run. The plugin
// directory is then held only by short-lived hook processes and the session's own MCP server, both
// of which end with the session.
//
// Elsewhere the kernel lets a running executable's file and directory be unlinked or replaced, so
// removing or updating the plugin never waits on the daemon, and the daemon runs from the plugin
// binary as it always has. A binary run from anywhere but a plugin directory — a build tree, a
// test's temporary directory, an operator's own copy — pins nothing a host will remove and is run
// as it is, which also keeps a test that runs a bare build from writing into the real user's home.
// Staging failure is not fatal anywhere: the daemon is started from the plugin binary instead, and
// it reports that itself when it starts (runningFromPluginRoot).

// pluginRootEnv is the variable Claude Code sets to the plugin's installed directory for every
// process it starts from the plugin; a daemon inherits it from the hook that spawned it.
const pluginRootEnv = "CLAUDE_PLUGIN_ROOT"

// pluginBinDirName is the directory under a plugin's root its executables live in: the manifests
// name ${CLAUDE_PLUGIN_ROOT}/bin/qompack (internal/pluginmanifest).
const pluginBinDirName = "bin"

// pluginManifestRel is where a Claude Code plugin keeps its manifest, relative to the plugin's
// root. A bundle always carries it (tools/devtool bundle), and it is what makes a directory a
// plugin to the host.
var pluginManifestRel = filepath.Join(".claude-plugin", "plugin.json")

// stagedBinDir is the directory under <home>/.qompack the staged copies live in.
const stagedBinDir = "bin"

// stagedBinaryName is the file name of every staged copy, whatever the source file was called.
const stagedBinaryName = "qompack"

// stagingEnabled reports whether SpawnDetached runs the daemon from a staged copy. Only Windows
// needs it (see above). The functions that act on it take it as a parameter, so a test exercises
// either path on any platform without writing a package variable other tests read.
const stagingEnabled = runtime.GOOS == "windows"

// stageTempPattern names a copy being written; stageBinary removes its own on every failure path.
const stageTempPattern = ".stage-*"

// errStagedMismatch is a staged copy whose bytes are not what its name says.
var errStagedMismatch = errors.New("daemon: staged binary does not match its content address")

// stageBinary returns the path of a verified copy of self under paths.Global(home)/bin, making the
// copy when it is missing or fails verification. It returns an error, and leaves nothing half
// written behind, when home is empty, self cannot be read, or the copy cannot be made and verified;
// the caller then runs self.
//
// Two spawners racing on one version both end on the same verified file: each writes its own
// temporary copy and renames it into place, and a rename that loses to an existing file falls back
// to verifying that file. A copy some other process is executing cannot be replaced on Windows,
// but it verified when it was written and is left alone unless it no longer does.
func stageBinary(self, home string) (string, error) {
	if home == "" {
		return "", errors.New("daemon: no home directory to stage the daemon binary under")
	}
	sum, err := fileSHA256(self)
	if err != nil {
		return "", fmt.Errorf("daemon: hashing %s: %w", self, err)
	}
	binRoot := filepath.Join(paths.Global(home), stagedBinDir)
	dir := filepath.Join(binRoot, sum)
	target := filepath.Join(dir, stagedBinaryName+filepath.Ext(self))

	switch verr := verifyStaged(target, sum); {
	case verr == nil:
		return target, nil
	case !errors.Is(verr, fs.ErrNotExist):
		// Something is filed under this version's name that is not this version — altered bytes,
		// or a link standing where the file should be. It is removed before a verified copy takes
		// its place, and never executed. The copy is sealed read-only, which Windows will not
		// remove or replace until the bit is cleared.
		_ = os.Chmod(paths.Long(target), 0o600)
		if rerr := os.Remove(paths.Long(target)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return "", fmt.Errorf("daemon: removing %s, which failed verification (%w): %w", target, verr, rerr)
		}
	}
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return "", fmt.Errorf("daemon: creating %s: %w", dir, err)
	}
	if err := copyStaged(self, dir, target, sum); err != nil {
		return "", err
	}
	pruneStaged(binRoot, sum)
	return target, nil
}

// copyStaged writes self into a temporary file in dir, checks the copy hashes to sum, and renames it
// to target. A rename refused because target now exists (another spawner won, or an older copy is
// running) is accepted only if target verifies.
func copyStaged(self, dir, target, sum string) (err error) {
	src, err := os.Open(paths.Long(self))
	if err != nil {
		return fmt.Errorf("daemon: opening %s: %w", self, err)
	}
	defer func() { _ = src.Close() }()

	tmp, err := os.CreateTemp(paths.Long(dir), stageTempPattern)
	if err != nil {
		return fmt.Errorf("daemon: staging in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), src)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("daemon: copying %s: %w", self, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		// self changed between the hash that named the directory and the copy: an update landing
		// under a spawning hook. Nothing is filed under a name its bytes do not have.
		err = fmt.Errorf("%w: %s changed while it was being copied", errStagedMismatch, self)
		return err
	}
	// Owner read and execute only: nothing needs to write a staged copy again, and on Windows the
	// mode sets the read-only attribute, which keeps a stray writer out as well.
	if err = os.Chmod(tmpPath, 0o500); err != nil {
		return fmt.Errorf("daemon: sealing %s: %w", tmpPath, err)
	}
	if rerr := os.Rename(tmpPath, paths.Long(target)); rerr != nil {
		if verr := verifyStaged(target, sum); verr != nil {
			err = fmt.Errorf("daemon: installing %s: %w (and the existing file: %w)", target, rerr, verr)
			return err
		}
		_ = os.Chmod(tmpPath, 0o600) // read-only files cannot be removed on Windows
		_ = os.Remove(tmpPath)
	}
	return verifyStaged(target, sum)
}

// verifyStaged checks that target is a regular file — not a symbolic link, junction or other
// reparse point, which Lstat reports as something other than a regular file — whose contents hash
// to sum.
func verifyStaged(target, sum string) error {
	fi, err := os.Lstat(paths.Long(target))
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("daemon: staged binary %s is not a regular file (%s)", target, fi.Mode().Type())
	}
	got, err := fileSHA256(target)
	if err != nil {
		return err
	}
	if got != sum {
		return fmt.Errorf("%w: %s", errStagedMismatch, target)
	}
	return nil
}

// pruneStaged removes every staged version under binRoot other than keep, best effort. A version a
// running daemon is executing cannot be removed on Windows and simply stays until a later spawn
// finds it idle; nothing else in binRoot is touched.
func pruneStaged(binRoot, keep string) {
	entries, err := os.ReadDir(paths.Long(binRoot))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == keep || !e.IsDir() || !isSHA256Hex(name) {
			continue
		}
		dir := filepath.Join(binRoot, name)
		_ = filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, werr error) error {
			if werr == nil && !d.IsDir() {
				_ = os.Chmod(p, 0o600) // clear the read-only bit the copy was sealed with
			}
			return nil
		})
		_ = os.RemoveAll(paths.Long(dir))
	}
}

// isSHA256Hex reports whether s is a lower-case hex SHA-256, the only directory name stageBinary
// creates.
func isSHA256Hex(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// fileSHA256 is the lower-case hex SHA-256 of the file at p.
//
// It reads through paths.OpenShared, whose handle shares delete as well as read and write. On
// Windows a plain os.Open does not, so it is refused with ERROR_SHARING_VIOLATION while any other
// handle holds p with DELETE access — and the spawner whose rename has just installed a staged copy
// holds the renamed file exactly that way until MoveFileEx closes its handle. A spawner that lost
// that rename and verified the winner's copy through os.Open inside that window read a correct
// copy as a failed install (TestStageBinary_VerifiesACopyItsRenamerStillHolds). Sharing delete
// takes nothing from verification: the hash is still of the bytes the handle reads.
func fileSHA256(p string) (string, error) {
	f, err := paths.OpenShared(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// daemonProgram is the executable SpawnDetached starts for a process running self: a staged copy
// when stage is set (stagingEnabled) and self is a plugin's binary (pluginDirOf, with envRoot the
// CLAUDE_PLUGIN_ROOT it sees) — the one case in which the daemon would otherwise pin a plugin
// directory — and staging succeeds; self otherwise. The error is the staging failure, for a caller
// with a logger to report; it never prevents the spawn.
func daemonProgram(self, home, envRoot string, stage bool) (string, error) {
	if !stage || pluginDirOf(self, envRoot) == "" {
		return self, nil
	}
	staged, err := stageBinary(self, home)
	if err != nil {
		return self, err
	}
	return staged, nil
}

// daemonWorkingDir is the working directory SpawnDetached gives a daemon it starts from program for
// a hook running self. A process's working directory cannot be removed on Windows either, and the
// daemon outlives the hook, so a staged copy runs in its own directory: the running executable
// already holds that directory, so the working directory pins nothing more, whichever directory
// the hook ran in — the plugin's own included. A daemon started from self inherits the hook's
// working directory, as it always has ("").
//
// It is never the project root. CreateProcess takes the working directory without the \\?\
// prefix that lifts MAX_PATH, so a project root past MAX_PATH cannot be a Windows process's
// working directory, and the spawn would fail outright (test/platform
// TestPlatform_ProjectRootShapes/long).
func daemonWorkingDir(self, program string) string {
	if program == self {
		return ""
	}
	return filepath.Dir(program)
}

// runningFromPluginRoot reports whether exe is a plugin's binary (pluginDirOf, with envRoot the
// CLAUDE_PLUGIN_ROOT the daemon inherited from whatever spawned it) where that pins the plugin
// directory — stage is stagingEnabled: staging was expected and did not happen, and the plugin
// cannot be removed or updated while this daemon runs. Run reports it Loud.
func runningFromPluginRoot(exe, envRoot string, stage bool) bool {
	return stage && pluginDirOf(exe, envRoot) != ""
}

// pluginDirOf returns the plugin directory exe is running from, or "" when it runs from none:
// envRoot (CLAUDE_PLUGIN_ROOT) when exe lies inside it, and otherwise the directory above exe's own
// when that is laid out as a plugin — exe in bin/, .claude-plugin/plugin.json beside bin/. The
// second test covers a process that does not carry the variable (spawn_stage.go's header); a bin/
// with no manifest beside it, such as a build tree's, is not a plugin's.
func pluginDirOf(exe, envRoot string) string {
	if insideDir(exe, envRoot) {
		return envRoot
	}
	if exe == "" {
		return ""
	}
	bin := filepath.Dir(filepath.Clean(exe))
	if !strings.EqualFold(filepath.Base(bin), pluginBinDirName) {
		return ""
	}
	root := filepath.Dir(bin)
	fi, err := os.Stat(paths.Long(filepath.Join(root, pluginManifestRel)))
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return root
}

// insideDir reports whether p lies inside dir: by the cleaned paths as spelled, or failing that by
// the paths with links resolved, so a plugin root reached through a link, a junction or a short
// (8.3) name is still recognized. An empty p or dir is inside nothing.
func insideDir(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	if within(p, dir) {
		return true
	}
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rd, err := filepath.EvalSymlinks(dir)
	return err == nil && within(rp, rd)
}

// within is insideDir's lexical test.
func within(p, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

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
// So on Windows the daemon runs from a copy of the binary under the per-user data directory, one of
// the two places Qompack writes (<home>/.qompack, §3.3): <home>/.qompack/bin/<sha256>/qompack.exe,
// content-addressed by the binary's own SHA-256. The copy is made once per version and verified on
// every spawn — a regular file, not a link or reparse point, whose bytes hash to the name it is
// filed under and to the running hook's own executable — before anything executes it; a copy that
// fails verification is replaced, never run. The plugin directory is then held only by short-lived
// hook processes and the session's own MCP server, both of which end with the session.
//
// Elsewhere the kernel lets a running executable's file and directory be unlinked or replaced, so
// removing or updating the plugin never waits on the daemon, and the daemon runs from the plugin
// binary as it always has. Staging failure is not fatal anywhere: the daemon is started from the
// plugin binary instead, and it reports that itself when it starts (runningFromPluginRoot).

// pluginRootEnv is the variable Claude Code sets to the plugin's installed directory for every
// process it starts from the plugin; a daemon inherits it from the hook that spawned it.
const pluginRootEnv = "CLAUDE_PLUGIN_ROOT"

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
func fileSHA256(p string) (string, error) {
	f, err := os.Open(paths.Long(p))
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

// daemonProgram is the executable SpawnDetached starts for a hook running self: the staged copy
// when stage is set (stagingEnabled) and staging succeeds, otherwise self. The error is the staging
// failure, for a caller with a logger to report; it never prevents the spawn.
func daemonProgram(self, home string, stage bool) (string, error) {
	if !stage {
		return self, nil
	}
	staged, err := stageBinary(self, home)
	if err != nil {
		return self, err
	}
	return staged, nil
}

// runningFromPluginRoot reports whether exe lies inside pluginRoot (CLAUDE_PLUGIN_ROOT, which the
// daemon inherits from the hook that spawned it) where that pins the plugin directory — stage is
// stagingEnabled: staging was expected and did not happen, and the plugin cannot be removed or
// updated while this daemon runs. Run reports it Loud.
func runningFromPluginRoot(exe, pluginRoot string, stage bool) bool {
	if !stage || exe == "" || pluginRoot == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(pluginRoot), filepath.Clean(exe))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

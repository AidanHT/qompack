package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The operator's backup and integrity commands on a project whose .qompack path is past Windows'
// legacy MAX_PATH (C1.7, V6-RECOVERY-2). Before the fix, `qompack backup create` failed there with
// "Rel: can't make \\?\... relative to ...": the backup walk handed its \\?\-prefixed spelling to a
// no-follow check anchored at the unprefixed project root (w3-paths runs/15, runs/16). The rows are
// platform-neutral; off Windows paths.Long is the identity.

// cliDeepRootMinLen is past MAX_PATH (260), so the project root, its .qompack and everything under
// them are beyond it and every path the commands touch takes paths.Long's \\?\ spelling.
const cliDeepRootMinLen = 270

// cliDeepSegment builds that depth; it names itself so a failure shows at once why a path is long.
const cliDeepSegment = "a-deliberately-long-directory-name-to-pass-max-path"

// cliDeepDir returns a directory below base whose path is at least minLen characters, creating it.
func cliDeepDir(t *testing.T, base string, minLen int) string {
	t.Helper()
	dir := base
	for len(dir) < minLen {
		dir = filepath.Join(dir, cliDeepSegment)
	}
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	return dir
}

// isolateUserGlobal points every home-directory lookup a store open makes (tokens.DefaultCalibPath)
// at a fresh directory, so these rows never read or write the real ~/.qompack.
func isolateUserGlobal(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestBackupCLI_CreateVerifyRestoreBeyondMaxPath(t *testing.T) {
	isolateUserGlobal(t)
	p := seedFsckProjectAt(t, cliDeepDir(t, filepath.Join(t.TempDir(), "project"), cliDeepRootMinLen))
	require.GreaterOrEqual(t, len(p.Layot.Dot), cliDeepRootMinLen, "fixture sanity: the store must sit past MAX_PATH")

	code, output, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "deep", "--json")
	require.Equal(t, ExitOK, code, "backup create past MAX_PATH: %s %s", output, stderr)
	var created backupReport
	require.NoError(t, json.Unmarshal([]byte(output), &created))
	require.NotNil(t, created.Manifest)
	require.True(t, created.Manifest.Consistent)
	require.NotEmpty(t, created.Manifest.Files)

	code, output, stderr = fsckDispatch(t, "backup", "verify", "--project", p.Root, "--id", "deep", "--json")
	require.Equal(t, ExitOK, code, "backup verify past MAX_PATH: %s %s", output, stderr)

	dest := cliDeepDir(t, filepath.Join(t.TempDir(), "restored"), cliDeepRootMinLen)
	code, output, stderr = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "deep",
		"--destination", dest, "--json")
	require.Equal(t, ExitOK, code, "backup restore past MAX_PATH: %s %s", output, stderr)
	var restored backupReport
	require.NoError(t, json.Unmarshal([]byte(output), &restored))
	require.NotNil(t, restored.Restore)
	require.Positive(t, restored.Restore.ContentRootsProven)
	require.NotNil(t, restored.Integrity, "restore runs fsck over the destination")
	require.Equal(t, ExitOK, restored.Integrity.Exit, "the restored deep project must pass fsck: %s", output)
	require.DirExists(t, paths.Long(paths.Of(dest).Dot))
}

// TestFsckCLI_PassesBeyondMaxPath runs the whole fsck scan, every walk and every check of it, over
// the seeded project placed past MAX_PATH: a clean project must still come back clean.
func TestFsckCLI_PassesBeyondMaxPath(t *testing.T) {
	isolateUserGlobal(t)
	p := seedFsckProjectAt(t, cliDeepDir(t, filepath.Join(t.TempDir(), "project"), cliDeepRootMinLen))

	code, doc, stderr := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "fsck past MAX_PATH must pass a clean project: %s\n%v", stderr, doc)
}

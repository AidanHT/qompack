//go:build windows

package daemon

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/paths/pathstest"
)

// Windows rows for stageBinary under concurrent spawners (C1.17). Opening a file is not a passive
// act on Windows: each handle names the access it takes and the access it lets others take, and a
// second open that does not share what an existing handle holds fails with
// ERROR_SHARING_VIOLATION. Two spawners racing on one version showed it: the one whose rename
// installed the copy still held the renamed file with DELETE access (the handle MoveFileEx keeps
// on the file it has just renamed, until it closes it), and a losing spawner verifying that file
// through a plain os.Open, which does not share delete, was refused, so a correct copy read as a
// failed install. A spawner whose first check was refused that way also removed the correct copy
// as if it had been tampered with, and a third spawner's final check then found nothing there.
// TestStageBinary_ConcurrentSpawnersAgree failed so in 2 of 1000 runs on a loaded host, and in 20
// of 1500 with eight busy processes added (plans/sdd/V6-closeout/w8-stagerace/runs).

// holdStaged opens p the way share names, with access, and closes the handle when the test ends.
func holdStaged(t *testing.T, p string, access, share uint32) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(paths.Long(p))
	require.NoError(t, err)
	h, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err, "holding %s", p)
	t.Cleanup(func() { _ = windows.CloseHandle(h) })
}

// holdLikeARenamer holds p as the spawner whose rename just installed it does: DELETE access,
// sharing read, write and delete.
func holdLikeARenamer(t *testing.T, p string) {
	t.Helper()
	holdStaged(t, p, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE)
}

// requireSameStagedFile fails unless p is still the very file (not a replacement with equal bytes)
// before names, and still sealed read-only.
func requireSameStagedFile(t *testing.T, before os.FileInfo, p string) {
	t.Helper()
	now, err := os.Lstat(p)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, now), "%s was removed and written again", p)
	require.Zero(t, now.Mode().Perm()&0o200, "%s lost its read-only seal", p)
}

// TestStageBinary_VerifiesACopyItsRenamerStillHolds: a spawner whose rename loses to a copy another
// spawner has just installed verifies that copy and uses it, even while the installer's rename
// handle is still open. It is the failure TestStageBinary_ConcurrentSpawnersAgree hit, made
// deterministic: "rename .stage-* -> qompack.exe: Access is denied (and the existing file: open
// ... The process cannot access the file because it is being used by another process)".
func TestStageBinary_VerifiesACopyItsRenamerStillHolds(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	before, err := os.Lstat(staged)
	require.NoError(t, err)
	holdLikeARenamer(t, staged)

	// The losing spawner's install step: its rename fails, and the existing copy decides.
	require.NoError(t, copyStaged(self, filepath.Dir(staged), staged, filepath.Base(filepath.Dir(staged))))
	entries, err := os.ReadDir(filepath.Dir(staged))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the losing spawner's temporary copy is removed")

	// A spawner arriving while the handle is open reuses the copy; it neither fails nor replaces it.
	again, err := stageBinary(self, home)
	require.NoError(t, err)
	require.Equal(t, staged, again)
	requireSameStagedFile(t, before, staged)
}

// TestStageBinary_StartsACopyItsRenamerStillHolds: the other half of the race — a spawner that has
// verified a copy starts it even while the spawner whose rename installed it still holds the file
// with DELETE access. The image open CreateProcess makes shares delete, so the renamer's handle
// cannot turn a verified copy into a failed start; only verification was ever refused. The test
// binary stands in for the daemon (it runs no tests and exits 0).
func TestStageBinary_StartsACopyItsRenamerStillHolds(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	require.NoError(t, err)
	staged, err := stageBinary(exe, t.TempDir())
	require.NoError(t, err)
	holdLikeARenamer(t, staged)

	out, err := exec.Command(staged, "-test.run=^$").CombinedOutput()
	require.NoError(t, err, "starting the verified copy: %s", out)
}

// TestStageBinary_NeverRemovesACopyHeldOpen: a copy another handle holds without sharing read —
// which refuses even a delete-sharing reader with ERROR_SHARING_VIOLATION for as long as it is open
// — has not been shown to be the wrong bytes, so it is neither run nor removed: staging fails, the
// spawn falls back to the plugin binary (daemonProgram), and the copy stays for the next spawn.
// Removing it could pull a correct copy out from under a spawner that verified it and is about to
// start it.
//
// The re-stage runs without the backup and restore privileges (pathstest.WithoutBackupPrivileges):
// the hosted runner's elevated account holds them enabled, and paths.OpenShared opens with backup
// semantics, so there the fixture's refusal did not reach stageBinary (nightly 36820740318). The
// precondition says so loudly if a token still gets past it.
func TestStageBinary_NeverRemovesACopyHeldOpen(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	before, err := os.Lstat(staged)
	require.NoError(t, err)
	holdStaged(t, staged, windows.GENERIC_READ, windows.FILE_SHARE_DELETE)

	pathstest.WithoutBackupPrivileges(t, func() {
		f, perr := paths.OpenShared(staged)
		if perr == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, perr, windows.ERROR_SHARING_VIOLATION,
			"precondition: a handle that does not share read refuses this token's read (enabled: %v)",
			pathstest.EnabledBypassPrivileges(t))

		_, err = stageBinary(self, home)
	})
	require.ErrorIs(t, err, windows.ERROR_SHARING_VIOLATION, "a copy that cannot be verified is never returned to be run")
	requireSameStagedFile(t, before, staged)
}

// TestStageBinary_RestagesACopyItCanNeverRead: only a copy held open is kept unread. One refused for
// a reason that does not pass — here an access-control entry denying the file's data to everyone —
// is refused as surely by every other spawner, so none can have verified it and none is about to
// start it; keeping it would send every later spawn to the plugin binary for good, the D10 hazard
// staging exists to remove. It is removed and staged again, as it was before w8-stagerace.
//
// The deny entry binds every token but one holding the backup privilege enabled, which an open with
// backup semantics (paths.OpenShared) uses to read past it, as the hosted runner's elevated account
// did (nightly 36820740318); the row therefore reads without that privilege.
func TestStageBinary_RestagesACopyItCanNeverRead(t *testing.T) {
	t.Parallel()
	self, home := fakeSelf(t), t.TempDir()
	staged, err := stageBinary(self, home)
	require.NoError(t, err)
	denyReadingData(t, staged)

	var again string
	pathstest.WithoutBackupPrivileges(t, func() {
		f, perr := paths.OpenShared(staged)
		if perr == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, perr, fs.ErrPermission, "precondition: the copy's data cannot be read (enabled: %v)",
			pathstest.EnabledBypassPrivileges(t))

		again, err = stageBinary(self, home)
	})
	require.NoError(t, err, "an unreadable copy is replaced, not a reason to run the plugin binary")
	require.Equal(t, staged, again)
	require.NoError(t, verifyStaged(again, filepath.Base(filepath.Dir(again))))
	requireSameBytes(t, self, again)
}

// denyReadingData replaces p's access-control list with one that denies FILE_READ_DATA to everyone
// and allows everything else, so p's attributes can still be read and p can still be removed.
func denyReadingData(t *testing.T, p string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;0x1;;;WD)(A;;FA;;;WD)")
	require.NoError(t, err)
	dacl, _, err := sd.DACL()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
}

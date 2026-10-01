package paths_test

// Error branches of the durable writers that a POSIX host reaches and that no other row provoked
// until the C3.6 Linux coverage floor measured them (w16b-cover). Each case uses a failure every user
// can stage — a regular file where a directory is expected, a value JSON cannot encode, a directory
// that is not empty — so the row means the same thing for root and for an ordinary user.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestBarriers_DirAndFileBarrierIssueTheirSyncThroughTheSeam pins the two exported one-shot barriers
// store and negknow use for trees and handles of their own: each issues exactly the sync its seam
// names, and with no seam the real sync, which succeeds on a real directory and file.
func TestBarriers_DirAndFileBarrierIssueTheirSyncThroughTheSeam(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "handle.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	log := &barrierLog{}
	b := log.barriers()
	require.NoError(t, b.DirBarrier(dir))
	require.NoError(t, b.FileBarrier(f))
	require.Equal(t, []string{"dir:" + filepath.Base(dir), "file:handle.jsonl"}, log.steps)

	failing := &barrierLog{failAt: 1}
	require.ErrorIs(t, failing.barriers().DirBarrier(dir), errBarrier, "a failed directory sync is reported")
	failing = &barrierLog{failAt: 1}
	require.ErrorIs(t, failing.barriers().FileBarrier(f), errBarrier, "a failed file sync is reported")

	require.NoError(t, paths.Barriers{}.DirBarrier(dir), "the default directory barrier syncs a real directory")
	require.NoError(t, paths.Barriers{}.FileBarrier(f), "the default file barrier syncs a real handle")
}

// TestBarriersMkdirAll_UnderARegularFileFailsWithoutSyncing: a path whose ancestor is a regular file
// cannot be created, and MkdirAll reports that without issuing any barrier — there is no new entry
// to make durable — and without touching the file in the way.
func TestBarriersMkdirAll_UnderARegularFileFailsWithoutSyncing(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("kept"), 0o600))

	log := &barrierLog{}
	err := log.barriers().MkdirAll(filepath.Join(blocker, "sub", "leaf"), 0o700)
	require.Error(t, err, "a directory cannot be made beneath a regular file")
	require.Empty(t, log.steps, "nothing was created, so nothing may be synced")
	got, rerr := os.ReadFile(blocker)
	require.NoError(t, rerr)
	require.Equal(t, "kept", string(got))
}

// TestAppendJSONLDurable_RefusesAnUnencodableValueBeforeTouchingTheLog: a value JSON cannot encode is
// refused before the log is opened, so no empty log is created and no barrier runs.
func TestAppendJSONLDurable_RefusesAnUnencodableValueBeforeTouchingTheLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.jsonl")

	log := &barrierLog{}
	require.Error(t, log.barriers().AppendJSONLDurable(p, map[string]any{"ch": make(chan int)}))
	require.NoFileExists(t, p, "an unencodable record must not create the log")
	require.Empty(t, log.steps)
}

// TestRestoreLog_ReportsAnUninspectableDestination: a destination beneath a regular file is neither
// "already restored" (os.ErrExist) nor restorable, and RestoreLog says so instead of creating anything.
func TestRestoreLog_ReportsAnUninspectableDestination(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "checkpoints")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))

	err := paths.RestoreLog(filepath.Join(blocker, "MANIFEST.jsonl"), []byte("{}\n"))
	require.Error(t, err)
	require.NotErrorIs(t, err, os.ErrExist, "an uninspectable path is not an existing restore")
}

// TestTerminatePartialTail_LeavesAnUnreadableTailAlone: a path whose last byte cannot be read — here a
// directory, which stats with a size but fails the read — is left alone: no terminator is written and
// no error is returned, because the diagnostic read must never cost the append it guards.
func TestTerminatePartialTail_LeavesAnUnreadableTailAlone(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "entry"), []byte("x"), 0o600))

	w := &recordingWriter{}
	require.NoError(t, paths.TerminatePartialTail(w, sub))
	require.Empty(t, w.got, "nothing may be written when the tail could not be read")
}

// TestEnsureLayout_RefusesToGrowALayoutItCannotUnmark: a marked layout that is missing a directory is
// unmarked before the directory is made, so a failed sync never leaves a marker vouching for a name
// that is not durable. When the marker cannot be removed — here it is a non-empty directory —
// EnsureLayout stops there: it reports the unmark and creates nothing.
func TestEnsureLayout_RefusesToGrowALayoutItCannotUnmark(t *testing.T) {
	l := paths.Of(t.TempDir())
	require.NoError(t, paths.EnsureLayout(l))

	gitignore := filepath.Join(l.Dot, ".gitignore")
	require.NoError(t, os.Remove(gitignore))
	require.NoError(t, os.Mkdir(gitignore, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(gitignore, "held"), []byte("x"), 0o600))
	require.NoError(t, os.Remove(l.Backup))

	err := paths.EnsureLayout(l)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmark", "the error must name the step that failed")
	require.NoDirExists(t, l.Backup, "no directory may be created while the layout is still marked")
}

// recordingWriter records what TerminatePartialTail writes.
type recordingWriter struct{ got []byte }

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.got = append(w.got, p...)
	return len(p), nil
}

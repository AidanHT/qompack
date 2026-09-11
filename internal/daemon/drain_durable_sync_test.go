package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The tests below pin what syncSpoolFileWith itself does, rather than what a drainer's syncFile seam
// returns (SP20-D1, F6 review 2). Every other failed-sync test injects at that seam, one level above
// the open, the fsync and the close, so each of those three could fail silently while the whole
// package passed: the error branches carry the non-held half of the F6 rule, and the open's flags
// carry "creating nothing".

// TestDrainConsumesNothingOfAFileWhoseFsyncFailed: the fsync the drain issues on its own handle is the
// only thing that makes a client spool, or a segment no longer held, durable. When it fails, nothing
// of the file may be consumed — otherwise a machine crash that takes the pages it could not flush
// leaves the leases, dispatches and offset the pass made of them: the orphan lease the design rejects
// (X1). Every other failed-sync test replaces syncFile wholesale, so a syncSpoolFileWith that returned
// nil after a failed fsync passed all of them and the rest of the package too (review 2, S1), and
// under it a failed sync both consumed and leased the file.
func TestDrainConsumesNothingOfAFileWhoseFsyncFailed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	const name = "client-9191.ndjson"
	path := filepath.Join(spool, name)
	line := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-fsync-fail", TS: 1}).line
	require.NoError(t, os.WriteFile(paths.Long(path), line, 0o600))

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
	})
	errFsync := errors.New("drain: injected fsync fault on the drain's own handle")
	failing := true
	fsyncs := 0
	dr.syncHandle = func(f *os.File) error { // the fsync itself fails, not the seam above it
		fsyncs++
		if failing {
			return errFsync
		}
		return f.Sync()
	}

	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, errFsync, "the fsync's own error reaches the pass")
	require.Zero(t, n)
	require.Empty(t, got, "nothing of the file is dispatched")
	require.Equal(t, 1, fsyncs, "the pass issued the fsync that failed")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st[name], "nothing of the file is consumed")
	require.FileExists(t, path, "and the file is left for a later pass")
	require.Equal(t, DrainGap{File: name, Kind: DrainGapUnsynced, Count: 1, Reason: "spool bytes could not be made durable"},
		gapOfKind(t, dr.GapState(), DrainGapUnsynced))
	require.False(t, dr.GapState().Complete)

	failing = false
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "once the fsync returns nil, the next pass takes the line")
	require.Equal(t, []core.UnixMilli{1}, got, "the delivery is dispatched exactly once")
	require.Equal(t, 2, fsyncs, "each pass over the file issues its own fsync")
	require.NoFileExists(t, path, "fully drained, the file is removed")
}

// TestDrainConsumesNothingWhenItCannotCloseTheHandleItSynced: syncSpoolFileWith returns the Close of
// the handle it opened, so a Close that fails after an fsync that returned nil leaves the file
// unconsumed. The fsync is not the last word on a write handle: a close can still report the error of
// a flush the filesystem deferred, and the pass cannot tell which bytes that error covers. Nothing
// pinned this branch, so a syncSpoolFileWith that dropped the Close error passed the whole package
// (review 2, S7).
func TestDrainConsumesNothingWhenItCannotCloseTheHandleItSynced(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	const name = "client-9090.ndjson"
	path := filepath.Join(spool, name)
	line := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-close-fail", TS: 1}).line
	require.NoError(t, os.WriteFile(paths.Long(path), line, 0o600))

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
	})
	failing := true
	synced := 0
	dr.syncHandle = func(f *os.File) error {
		require.NoError(t, f.Sync(), "the fsync itself succeeds: this pins the close that follows it")
		synced++
		if failing {
			// Closing the handle here makes the drain's own Close of it fail, the way a filesystem
			// reporting a deferred flush at close does, with the fsync already returned nil.
			require.NoError(t, f.Close())
		}
		return nil
	}

	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, os.ErrClosed, "the failed close reaches the pass, though the fsync returned nil")
	require.Zero(t, n)
	require.Empty(t, got, "nothing of the file is dispatched")
	require.Equal(t, 1, synced)
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st[name], "nothing of the file is consumed")
	require.FileExists(t, path)
	require.Equal(t, DrainGap{File: name, Kind: DrainGapUnsynced, Count: 1, Reason: "spool bytes could not be made durable"},
		gapOfKind(t, dr.GapState(), DrainGapUnsynced))

	failing = false
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "once the handle closes cleanly, the next pass takes the line")
	require.Equal(t, []core.UnixMilli{1}, got, "the delivery is dispatched exactly once")
	require.NoFileExists(t, path)
}

// TestDrainSyncNeverCreatesASpoolFileThatIsGone: the drain's sync opens the spool file for appending
// and creates nothing. A file can disappear between the pass's stat and its sync — only an external
// delete does that, since the drain is the sole in-process remover and is serialized under its mutex —
// and an open with O_CREATE would put an empty file back under the same name. The pass would then
// record the stat's size for a zero-byte file, and validateProgress refuses progress past a file's
// size, so every later Drain would fail for the whole spool, including files that are perfectly well.
// Nothing pinned the flags, and the mutant that creates survived the whole package (review 2, S2).
func TestDrainSyncNeverCreatesASpoolFileThatIsGone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	const goneName, keptName = "client-9292.ndjson", "client-9393.ndjson"
	gonePath, keptPath := filepath.Join(spool, goneName), filepath.Join(spool, keptName)
	line := func(ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-gone", TS: ts}).line
	}
	k1, k2 := line(2), line(3)
	half := len(k2) / 2
	require.NoError(t, os.WriteFile(paths.Long(gonePath), line(1), 0o600))
	// A trailing half line leaves the second file with work for the pass after the one that loses
	// the first, which is where a wedge of the whole spool would show.
	require.NoError(t, os.WriteFile(paths.Long(keptPath), joinLines(k1, k2[:half]), 0o600))

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
	})
	deleted := false
	dr.syncFile = func(p string) error { // the seam keeps the real open, flags and all
		if filepath.Base(p) == goneName && !deleted {
			deleted = true
			require.NoError(t, os.Remove(paths.Long(p)), "fixture: the file goes between the pass's stat and its sync")
		}
		return syncSpoolFileWith(p, dr.syncHandle)
	}

	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, os.ErrNotExist, "the sync opens the file, it does not create it")
	require.True(t, deleted, "fixture: the pass reached the vanished file's sync")
	require.Equal(t, 1, n, "the other file drains")
	require.Equal(t, []core.UnixMilli{2}, got)
	require.NoFileExists(t, gonePath, "no empty file takes the name")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st[goneName], "and no size is recorded for it")

	w, err := paths.AppendOnly(keptPath)
	require.NoError(t, err)
	_, err = w.Write(k2[half:])
	require.NoError(t, err)
	require.NoError(t, w.Close())

	n, err = dr.Drain(ctx)
	require.NoError(t, err, "no file's recorded size names bytes that are gone, so the spool is not wedged")
	require.Equal(t, 1, n, "the next pass takes the line completed since")
	require.Equal(t, []core.UnixMilli{2, 3}, got, "every line exactly once, in order")
	require.NoFileExists(t, keptPath, "fully drained, the file is removed")
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// writeSpoolFile writes n NDJSON request lines to <root>/.qompack/spool/<name>, in the exact shape
// ipc.EncodeRequest produces, and returns the full path.
func writeSpoolFile(t *testing.T, root, name string, n int) string {
	t.Helper()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, name)

	f, err := os.Create(p) //nolint:gosec // test fixture, path built from t.TempDir()
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	for i := 0; i < n; i++ {
		line, err := ipc.EncodeRequest(ipc.Request{
			Op: ipc.OpObserveTool, Session: "sess-1", TS: core.UnixMilli(i),
		})
		require.NoError(t, err)
		_, err = f.Write(line)
		require.NoError(t, err)
	}
	return p
}

// countingDispatch returns a Dispatch func that increments a counter for every call and records
// every dispatched request's TS, so a test can assert both the count and (when relevant) ordering.
func countingDispatch() (dispatch func(context.Context, ipc.Request) ipc.Response, count *int64) {
	var n int64
	return func(context.Context, ipc.Request) ipc.Response {
		atomic.AddInt64(&n, 1)
		return ipc.Response{OK: true}
	}, &n
}

// TestDrainPersistsPerFileBeforeMovingOn pins I-4: state/drain.json is persisted per file, on EOF,
// before the file is removed — not only once at the very end of Drain — so a crash between one
// file's completion and the next file's processing never loses the completed file's recorded
// offset. Proven by reading state/drain.json back from disk from inside the Dispatch callback, at
// the first line of the file after the one being checked, well before Drain itself has returned.
//
// A file the drain keeps (a live session's WAL) must show its completion there. It is read back at
// the first line of the very next file, because no other save runs between the kept file's EOF and
// that point: only the per-file EOF save can have put the completion on disk. Read back any later,
// it would pass without that save, since a removed file's forget-save (removeCompletedFile)
// persists the whole in-memory state, the kept file's completion included.
//
// A file the drain removes must show its removal instead: gone from the spool and forgotten in
// state/drain.json, persisted before the unlink. Reading a removed file's {Done} entry back, as this
// test once did for client-1.ndjson, was reading the stale entry that outlived its file until the
// next save — the entry a later file under the same name inherited.
func TestDrainPersistsPerFileBeforeMovingOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSpoolFile(t, root, "wal-sess-1.ndjson", 2) // kept: sess-1 stays live
	removed := writeSpoolFile(t, root, "client-1.ndjson", 2)
	writeSpoolFile(t, root, "client-2.ndjson", 2)

	clk := newFakeClock(epoch)
	var calls int
	var sawKeptFileDoneOnDiskAtTheNextFile, sawRemovedFileForgottenOnDiskEarly bool
	var dr *drainer
	dr = newDrainer(DrainConfig{
		Root: root, Clock: clk,
		IsLive: func(sess core.SessionID) bool { return sess == "sess-1" },
		Dispatch: func(context.Context, ipc.Request) ipc.Response {
			calls++
			switch calls {
			case 3: // the first line of client-1.ndjson, the file right after the kept one
				st, stateErr := dr.loadState()
				require.NoError(t, stateErr)
				fs, ok := st["wal-sess-1.ndjson"]
				sawKeptFileDoneOnDiskAtTheNextFile = ok && fs.Done
			case 5: // the first line of client-2.ndjson, the file right after the removed one
				st, stateErr := dr.loadState()
				require.NoError(t, stateErr)
				_, named := st["client-1.ndjson"]
				_, statErr := os.Stat(removed)
				sawRemovedFileForgottenOnDiskEarly = !named && os.IsNotExist(statErr)
			}
			return ipc.Response{OK: true}
		},
	})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.True(t, sawKeptFileDoneOnDiskAtTheNextFile,
		"wal-sess-1.ndjson's completion must be on disk before client-1.ndjson starts, not only at the end of Drain")
	require.True(t, sawRemovedFileForgottenOnDiskEarly,
		"client-1.ndjson's removal must be on disk before client-2.ndjson starts: gone, and forgotten in drain.json")
}

// TestDrainPersistsAFileBeforeCleaningUpItsBlobs pins the ORDER of the per-file EOF save and the
// cleanup; TestDrainPersistsPerFileBeforeMovingOn pins only that the save exists. drainFileState's
// contract is that PendingBlobs is persisted with the acknowledged offset before any deletion is
// attempted. Saved after the cleanup instead, a crash between a blob's removal and that save leaves
// the old offset on disk with the blob gone: the next pass reads the line again, and an unleased
// line whose blob is gone wedges its file on every pass. The cleanup is made to fail from inside the
// blob line's own dispatch, after its blob was read, by putting a non-empty directory where the blob
// was, a removal both platforms refuse. A failed cleanup returns before anything else the file's pass
// does, so whatever is on disk when the next file starts was saved before the cleanup was tried.
func TestDrainPersistsAFileBeforeCleaningUpItsBlobs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	payload := []byte(`{"externalized":true}`)
	const blobName = "blob-7272-0.bin"
	blobPath := filepath.Join(spool, blobName)
	require.NoError(t, os.WriteFile(paths.Long(blobPath), payload, 0o600))
	ref, err := json.Marshal(blobRef{Blob: blobName, Bytes: len(payload), Field: drainBlobToolResponse})
	require.NoError(t, err)
	line, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-kept", Event: &hookio.Event{}, Raw: ref,
	})
	require.NoError(t, err)
	kept := filepath.Join(spool, "wal-sess-kept.ndjson")
	require.NoError(t, os.WriteFile(paths.Long(kept), line, 0o600))
	writeSpoolFile(t, root, "client-7373.ndjson", 1) // the next file the listing offers
	size := spoolFileSize(t, kept)

	var atNextFile drainState
	dr := newDrainer(DrainConfig{
		Root: root, Clock: newFakeClock(epoch),
		IsLive: func(core.SessionID) bool { return true }, // kept: no forget-save can persist the entry instead
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			if r.Session == "sess-kept" {
				require.NoError(t, os.Remove(paths.Long(blobPath))) // already read for this dispatch
				require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(blobPath, "keep")), 0o700))
			} else if atNextFile == nil {
				atNextFile = diskDrainState(t, root)
			}
			return ipc.Response{OK: true}
		},
	})
	n, err := dr.Drain(context.Background())
	require.Error(t, err, "fixture: the blob could not be removed")
	require.Equal(t, 2, n, "the blob line and the next file's line")
	require.NotNil(t, atNextFile, "fixture: the pass reached the next file")
	require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true, PendingBlobs: []string{blobName}},
		atNextFile[filepath.Base(kept)],
		"the consumed offset and its cleanup intent must be on disk before the cleanup is tried")
}

// TestDrainIsIdempotent pins that a fully-drained file is not re-processed by a second Drain call:
// the file is deleted after the first pass, so SpoolFiles never offers it again.
func TestDrainIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSpoolFile(t, root, "client-1.ndjson", 10)

	dispatch, count := countingDispatch()
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: dispatch})

	n1, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10, n1)

	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	require.Empty(t, files, "the drained file must be deleted after the first pass")

	n2, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, n2)

	require.EqualValues(t, 10, atomic.LoadInt64(count), "10 dispatches total, not 20")
}

// TestDrainResumesAfterCancel pins resumability: a Drain cancelled partway through persists its
// offset, and a second call finishes the remainder — total dispatches across both calls equals the
// line count exactly once.
func TestDrainResumesAfterCancel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const total = 1000
	const cutoff = 200
	writeSpoolFile(t, root, "client-1.ndjson", total)

	var n int64
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	dispatch := func(context.Context, ipc.Request) ipc.Response {
		mu.Lock()
		n++
		if n == cutoff {
			cancel()
		}
		mu.Unlock()
		return ipc.Response{OK: true}
	}

	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: dispatch})

	n1, err := dr.Drain(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, cutoff, n1)

	n2, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, total-cutoff, n2)

	require.Equal(t, total, n1+n2)
}

// TestDrainResolvesBlobs pins blob restoration: a spooled line externalizing Event.ToolResponse
// into a side blob file must reach the handler with the field restored, and the blob file must be
// removed afterward.
func TestDrainResolvesBlobs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))

	blobBytes := []byte(`{"huge":"tool output"}`)
	blobName := "blob-1-1.bin"
	require.NoError(t, os.WriteFile(filepath.Join(dir, blobName), blobBytes, 0o600))

	ref, err := json.Marshal(struct {
		Blob  string `json:"blob"`
		Bytes int    `json:"bytes"`
		Field string `json:"field"`
	}{Blob: blobName, Bytes: len(blobBytes), Field: "e.tool_response"})
	require.NoError(t, err)

	req := ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-1",
		Event: &hookio.Event{ToolResponse: nil},
		Raw:   ref,
	}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client-1.ndjson"), line, 0o600))

	var got ipc.Request
	dispatch := func(_ context.Context, r ipc.Request) ipc.Response {
		got = r
		return ipc.Response{OK: true}
	}
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: dispatch})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.NotNil(t, got.Event)
	require.JSONEq(t, string(blobBytes), string(got.Event.ToolResponse))
	require.Empty(t, got.Raw, "the blob descriptor must be cleared once resolved")

	_, statErr := os.Stat(filepath.Join(dir, blobName))
	require.True(t, os.IsNotExist(statErr), "the blob file must be deleted once resolved")
}

// TestDrainSurvivesCorruptLine pins that one malformed line does not abort the file: every valid
// line around it still dispatches, the corruption is counted, and the file is still deleted once
// fully read.
func TestDrainSurvivesCorruptLine(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, "client-1.ndjson")

	f, err := os.Create(p) //nolint:gosec // test fixture
	require.NoError(t, err)
	writeValid := func(ts int) {
		line, eerr := ipc.EncodeRequest(ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", TS: core.UnixMilli(ts)})
		require.NoError(t, eerr)
		_, werr := f.Write(line)
		require.NoError(t, werr)
	}
	writeValid(1)
	writeValid(2)
	writeValid(3)
	_, err = f.WriteString("{{{\n")
	require.NoError(t, err)
	writeValid(4)
	require.NoError(t, f.Close())

	clk := newFakeClock(epoch)
	m := obs.New(clk)
	dispatch, count := countingDispatch()
	dr := newDrainer(DrainConfig{Root: root, Clock: clk, Metrics: m, Dispatch: dispatch})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.EqualValues(t, 4, atomic.LoadInt64(count))
	require.Equal(t, int64(1), m.Counter(counterDrainFileError).Value(), "the corrupt line must be counted exactly once")

	_, statErr := os.Stat(p)
	require.True(t, os.IsNotExist(statErr), "the file must still be deleted despite the corrupt line")
}

// TestDrainKeepsLiveSessionWAL pins that a wal-<session>.ndjson file for a still-live session is
// fully drained (offset-marked) but not deleted, and is skipped on a subsequent no-op Drain.
func TestDrainKeepsLiveSessionWAL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	p := writeSpoolFile(t, root, "wal-sess-1.ndjson", 3)

	dispatch, count := countingDispatch()
	dr := newDrainer(DrainConfig{
		Root: root, Clock: newFakeClock(epoch), Dispatch: dispatch,
		IsLive: func(sess core.SessionID) bool { return sess == "sess-1" },
	})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.FileExists(t, p, "a live session's WAL must not be deleted")

	// A second drain with nothing new appended must not re-dispatch anything (Done + size match).
	n2, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, n2)
	require.EqualValues(t, 3, atomic.LoadInt64(count))
}

// TestDrainNotesAStaleProgressOnceThenClears is the owning-package half of F4-7: a
// state/drain.json that no longer matches the spool Louds once across two Drain calls
// (noteWedged), and the flag clears on the first pass that gets through validateProgress
// so a later wedge is announced again.
func TestDrainNotesAStaleProgressOnceThenClears(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	present := writeSpoolFile(t, root, "wal-sess-1.ndjson", 1)
	size := spoolFileSize(t, present)
	writeStaleDrainProgress(t, root, filepath.Base(present), size+1)

	log := newRecordingLogger()
	dr := newDrainer(DrainConfig{
		Root: root, Log: log, Clock: newFakeClock(epoch),
		IsLive:   func(sess core.SessionID) bool { return sess == "sess-1" },
		Dispatch: func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
	})

	_, err := dr.Drain(context.Background())
	require.Error(t, err)
	_, err = dr.Drain(context.Background())
	require.Error(t, err)
	require.Equal(t, 1, log.count(logLoud), "noteWedged is once per wedge, not once per pass")
	require.Contains(t, log.msgs(logLoud)[0], "state/drain.json")

	writeMatchingDrainProgress(t, root, filepath.Base(present), size)
	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, log.count(logLoud), "a pass that validates must not Loud again")

	writeStaleDrainProgress(t, root, filepath.Base(present), size+1)
	_, err = dr.Drain(context.Background())
	require.Error(t, err)
	require.Equal(t, 2, log.count(logLoud), "clearing the flag lets a later wedge speak")
}

func writeStaleDrainProgress(t *testing.T, root, base string, size int64) {
	t.Helper()
	writeDrainProgress(t, root, drainState{base: {Size: size, Offset: size, Done: true}})
}

func writeMatchingDrainProgress(t *testing.T, root, base string, size int64) {
	t.Helper()
	writeDrainProgress(t, root, drainState{base: {Size: size, Offset: 0}})
}

func writeDrainProgress(t *testing.T, root string, st drainState) {
	t.Helper()
	state, err := json.Marshal(st)
	require.NoError(t, err)
	p := drainStatePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), state, 0o600))
}

// TestWalSessionID pins the filename parser rotation-suffix handling.
func TestWalSessionID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base   string
		wantID core.SessionID
		wantOK bool
	}{
		{"wal-sess-1.ndjson", "sess-1", true},
		{"wal-sess-1.3.ndjson", "sess-1", true},
		{"client-123.ndjson", "", false},
		{"wal-.ndjson", "", false},
	}
	for _, c := range cases {
		id, ok := walSessionID(c.base)
		require.Equal(t, c.wantOK, ok, c.base)
		if c.wantOK {
			require.Equal(t, c.wantID, id, c.base)
		}
	}
}

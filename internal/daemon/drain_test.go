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
// offset. Proven by reading state/drain.json back from disk from inside the Dispatch callback for
// the SECOND file's first line: the first file's completion must already be visible on disk by
// then, well before Drain itself has returned.
func TestDrainPersistsPerFileBeforeMovingOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSpoolFile(t, root, "client-1.ndjson", 2)
	writeSpoolFile(t, root, "client-2.ndjson", 2)

	clk := newFakeClock(epoch)
	var calls int
	var sawFirstFileDoneOnDiskEarly bool
	var dr *drainer
	dr = newDrainer(DrainConfig{Root: root, Clock: clk, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		calls++
		if calls == 3 { // the first line of the second file
			st := dr.loadState()
			fs, ok := st["client-1.ndjson"]
			sawFirstFileDoneOnDiskEarly = ok && fs.Done
		}
		return ipc.Response{OK: true}
	}})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.True(t, sawFirstFileDoneOnDiskEarly,
		"client-1.ndjson's completion must be on disk before client-2.ndjson starts, not only at the end of Drain")
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

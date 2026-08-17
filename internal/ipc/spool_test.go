package ipc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// This file is white-box (package ipc, not ipc_test) because
// TestSpoolWriteFailureDropsAndLoudsOnce needs newSpool's log/metrics-wired constructor, which
// NewSpool's shipped two-argument signature (dir only) has no way to expose to an external caller.

// loudCountingLogger counts Loud calls; every other Logger method is a no-op. It exists so a
// white-box test can assert "exactly one Loud line" without depending on logging's file-backed
// sink.
type loudCountingLogger struct {
	mu sync.Mutex
	n  int
}

func (l *loudCountingLogger) With(...any) logging.Logger { return l }
func (l *loudCountingLogger) Debug(string, ...any)       {}
func (l *loudCountingLogger) Info(string, ...any)        {}
func (l *loudCountingLogger) Warn(string, ...any)        {}
func (l *loudCountingLogger) Error(string, ...any)       {}

func (l *loudCountingLogger) Loud(string, ...any) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *loudCountingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func TestNewSpool_ConstructsWithoutTouchingTheFilesystem(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSpool(dir)
	require.NoError(t, err)
	require.NotNil(t, s)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "constructing a spool must not create a file")
}

// TestSpoolAppendOnly is the table's row: pre-create the spool file with content, Append, and
// assert the original bytes are intact with the new line appended after them — paths.AppendOnly
// is the only opener this package's write path ever reaches, and it refuses O_TRUNC outright.
func TestSpoolAppendOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpool(dir)
	require.NoError(t, err)
	t.Cleanup(func() { closeSpool(t, s) })

	preexisting := `{"op":"observe.tool","s":"pre","t":1}` + "\n"
	require.NoError(t, os.MkdirAll(dir, dirPerm))
	require.NoError(t, os.WriteFile(s.Path(), []byte(preexisting), 0o600))

	require.NoError(t, s.Append(Request{Op: OpObserveTool, Session: core.SessionID("new"), TS: 2}))

	got, err := os.ReadFile(s.Path())
	require.NoError(t, err)
	require.True(t, len(got) > len(preexisting))
	require.Equal(t, preexisting, string(got[:len(preexisting)]), "the pre-existing line must survive byte-identical")
}

// TestSpoolAppend_WritesExactlyOneNewlinePerRecord is the C-1 regression test: EncodeRequest
// already returns a '\n'-terminated line, so writeLocked must write it as-is. A test that strips
// empty lines before counting (as ipctest's own readers do) would never catch a doubled newline,
// so this one counts raw bytes.
func TestSpoolAppend_WritesExactlyOneNewlinePerRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpool(dir)
	require.NoError(t, err)
	t.Cleanup(func() { closeSpool(t, s) })

	const n = 5
	for i := 0; i < n; i++ {
		require.NoError(t, s.Append(Request{Op: OpObserveTool, Session: "s", TS: core.UnixMilli(i)}))
	}

	raw, err := os.ReadFile(s.Path())
	require.NoError(t, err)
	require.Equal(t, n, bytes.Count(raw, []byte("\n")), "exactly one newline per record — no blank lines")
	require.NotContains(t, string(raw), "\n\n", "a spooled file must never contain a blank line")
}

// TestSpoolWriteFailureDropsAndLoudsOnce is the table's row: a spool whose backing file cannot be
// written drops every event, counts every one of them as dropped, but Louds exactly once across
// 10 failing Append calls.
func TestSpoolWriteFailureDropsAndLoudsOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, fmt.Sprintf("%s%d%s", spoolFilePrefix, os.Getpid(), spoolFileExt))
	require.NoError(t, os.WriteFile(p, nil, 0o600))
	require.NoError(t, os.Chmod(p, 0o400))
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	lg := &loudCountingLogger{}
	reg := obs.New(core.SystemClock())
	s := newSpool(dir, lg, reg)
	t.Cleanup(func() { _ = s.Close() })

	for i := 0; i < 10; i++ {
		err := s.Append(Request{Op: OpObserveTool, Session: core.SessionID("s"), TS: core.UnixMilli(i)})
		require.NoError(t, err, "a spool write failure must never propagate to the caller")
	}

	require.EqualValues(t, 10, reg.Counter(counterL0Dropped).Value())
	require.Equal(t, 1, lg.count(), "exactly one Loud line for 10 dropped events")
}

// TestSpoolCap asserts Append refuses once the spool file would exceed spoolMaxBytes: the write
// fails internally (counted as a drop, per the same §12.3 path as any other write failure) rather
// than growing the file without bound.
func TestSpoolCap(t *testing.T) {
	dir := t.TempDir()
	lg := &loudCountingLogger{}
	reg := obs.New(core.SystemClock())
	s := newSpool(dir, lg, reg)
	t.Cleanup(func() { _ = s.Close() })

	// One small write opens the real file, so the forced byte count below reflects a spool that
	// legitimately grew to the cap through ordinary Appends rather than an untouched fresh file.
	require.NoError(t, s.Append(Request{Op: OpObserveTool, Session: "s", TS: 0}))

	s.mu.Lock()
	s.bytes = spoolMaxBytes
	s.mu.Unlock()

	require.NoError(t, s.Append(Request{Op: OpObserveTool, Session: "s", TS: 1}))
	require.EqualValues(t, 1, reg.Counter(counterL0Dropped).Value())
	require.Equal(t, 1, lg.count())
}

// closeSpool releases s's backing file handle, if any, so TempDir's cleanup can remove it —
// necessary on Windows, where an open file cannot be unlinked. SpoolWriter itself has no Close
// method (SP-01 shipped it without one); the concrete *spool type does, reached here exactly as
// Client.Close reaches it, through a type assertion.
func closeSpool(t *testing.T, s SpoolWriter) {
	t.Helper()
	if cl, ok := s.(interface{ Close() error }); ok {
		_ = cl.Close()
	}
}

func TestExternalizeThreshold_IsTheSmallerOfMaxPayloadAndMaxLine(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.HotPath.MaxPayloadBytes = 4096
	require.Equal(t, 4096, ExternalizeThreshold(cfg))

	cfg.Runtime.HotPath.MaxPayloadBytes = MaxLineBytes * 2
	require.Equal(t, MaxLineBytes, ExternalizeThreshold(cfg))
}

// TestSpoolFiles_SortsWALBeforeClient asserts the daemon's drain order: WAL segments first, this
// package's own client spools after, each family lexically sorted.
func TestSpoolFiles_SortsWALBeforeClient(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"client-200.ndjson", "wal-b.ndjson", "client-100.ndjson", "wal-a.ndjson", "ignored.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}

	got, err := SpoolFiles(dir)
	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(dir, "wal-a.ndjson"),
		filepath.Join(dir, "wal-b.ndjson"),
		filepath.Join(dir, "client-100.ndjson"),
		filepath.Join(dir, "client-200.ndjson"),
	}, got)
}

// TestSpoolFiles_MissingDirIsEmpty asserts a project that never spooled anything reports no files,
// not an error.
func TestSpoolFiles_MissingDirIsEmpty(t *testing.T) {
	got, err := SpoolFiles(filepath.Join(t.TempDir(), "never-created"))
	require.NoError(t, err)
	require.Empty(t, got)
}

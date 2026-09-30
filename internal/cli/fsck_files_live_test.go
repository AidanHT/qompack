package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// index/files.json while a daemon runs (V6 close-out D49, item 4; UAT-04 O-UAT04-2 of the candidate 4
// live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-04/notes.txt).
//
// The view is DERIVED: the store appends every file version to index/files.jsonl as it is observed
// and regenerates index/files.json only when it flushes, which the daemon does at a session's end
// (the flush route) and when it stops (Store.Close). Between those two points a running daemon's view
// is absent — in a project that has never flushed — or behind its log, and that is the store working
// as designed, not damage. UAT-04 ran fsck with the daemon up after the planted unparseable config
// had made the hooks refuse the session's end, so nothing had flushed, and fsck exited 1 on
// "index/files.json is absent while its log carries 4 path(s); the view is derived and --repair
// regenerates it" — a repair it refuses while a daemon holds the lock. Any session in progress in a
// fresh project got the same answer before its first end. The daemon's own stop wrote the view
// later (the evidence's index_files.json was generated at the idle exit).
//
// With no daemon running the project is quiet, nothing will flush, and a view behind its log is the
// residue of a crash: that stays a defect --repair regenerates.

// openUnflushedFilesStore opens a store over a fresh project and records one file version the way the
// observer does for a Read, without the flush that would materialize index/files.json. The store is
// left open, as a running daemon holds it; closing it would flush.
func openUnflushedFilesStore(t *testing.T, root string, path string) store.Store {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(root, ".git")), 0o700))
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	ctx := context.Background()
	s, err := store.Open(root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	res, err := s.PutBytes(ctx, []byte("notes the session read\n"), store.PutOptions{Tool: "Read", Path: path})
	require.NoError(t, err)
	require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
		ID: core.ToolUseID("tu-live-" + path), Session: core.SessionID("s-live"), Turn: 1,
		TS: 1, Tool: "Read", Root: res.Root.Hash, Path: path, Bytes: res.Root.RawBytes,
	}))
	require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
		TS: 1, Root: res.Root.Hash, Turn: 1, Bytes: res.Root.RawBytes,
	}))
	return s
}

// TestFsck_ALiveDaemonsUnmaterializedFilesViewIsNotADefect is UAT-04's state: a daemon running over
// a project whose file versions it has not yet flushed into the view.
func TestFsck_ALiveDaemonsUnmaterializedFilesViewIsNotADefect(t *testing.T) {
	// Not parallel: it takes the project's singleton lock and binds its endpoint.
	root := t.TempDir()
	openUnflushedFilesStore(t, root, "notes.md")
	_, err := os.Stat(paths.Long(filepath.Join(paths.Of(root).Index, "files.json")))
	require.True(t, os.IsNotExist(err), "the precondition: nothing has materialized the view yet")
	serveFakeDaemon(t, root)

	_, doc, errw := fsckJSON(t, root)
	require.Equal(t, true, doc["daemon_running"], "stderr=%s", errw)
	row := fsckRequireRow(t, doc, "index.files")
	require.Equal(t, true, row["ok"],
		"a running daemon's view that has not been materialized yet is not a defect: %s", fsckDetail(row))
	require.Contains(t, fsckDetail(row), "materializes it at its next flush",
		"the row still says the view lags and when it catches up")
}

// TestFsck_ALiveDaemonsLaggingFilesViewIsNotADefect is the same state one flush later: the view
// exists, and the daemon has observed a newer version of a file since.
func TestFsck_ALiveDaemonsLaggingFilesViewIsNotADefect(t *testing.T) {
	// Not parallel: it takes the project's singleton lock and binds its endpoint.
	p := seedFsckProject(t)
	ctx := context.Background()
	s, err := store.Open(p.Root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	res, err := s.PutBytes(ctx, []byte("package main\n\nfunc main() { run() }\n"),
		store.PutOptions{Tool: "Read", Path: "src/main.go"})
	require.NoError(t, err)
	require.NoError(t, s.AppendFileVersion(ctx, "src/main.go", store.FileVersion{
		TS: 2, Root: res.Root.Hash, Turn: 2, Bytes: res.Root.RawBytes,
	}))
	serveFakeDaemon(t, p.Root)

	_, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, true, doc["daemon_running"], "stderr=%s", errw)
	row := fsckRequireRow(t, doc, "index.files")
	require.Equal(t, true, row["ok"], "a view behind a live daemon's log is not a defect: %s", fsckDetail(row))
	require.Contains(t, fsckDetail(row), "materializes it at its next flush")
}

// TestFsck_AQuietProjectsUnmaterializedFilesViewStaysADefect keeps the check's strength where it
// belongs: with no daemon running nothing will flush, so a view behind its log is crash residue.
func TestFsck_AQuietProjectsUnmaterializedFilesViewStaysADefect(t *testing.T) {
	root := t.TempDir()
	openUnflushedFilesStore(t, root, "notes.md")

	code, doc, _ := fsckJSON(t, root)
	require.Equal(t, false, doc["daemon_running"])
	row := fsckRequireRow(t, doc, "index.files")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "index/files.json is absent while its log carries 1 path(s)")
	require.Equal(t, ExitError, code)
}

// TestFsck_ALiveDaemonsViewThatContradictsItsLogStaysADefect: a running daemon excuses a view that is
// BEHIND its log, never one that disagrees with it. A path the log never mentions cannot be lag.
func TestFsck_ALiveDaemonsViewThatContradictsItsLogStaysADefect(t *testing.T) {
	// Not parallel: it takes the project's singleton lock and binds its endpoint.
	p := seedFsckProject(t)
	view := filepath.Join(p.Layot.Index, "files.json")
	b, err := os.ReadFile(paths.Long(view))
	require.NoError(t, err)
	doc := string(b)
	require.Contains(t, doc, `"src/main.go"`)
	forged := []byte(strings.Replace(doc, `"src/main.go"`, `"src/never-logged.go"`, 1))
	require.NoError(t, os.WriteFile(paths.Long(view), forged, 0o600))
	serveFakeDaemon(t, p.Root)

	_, got, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, got, "index.files")
	require.Equal(t, false, row["ok"], "a view that records what the log never mentions is not lag")
	require.Contains(t, fsckDetail(row), "src/never-logged.go")
}

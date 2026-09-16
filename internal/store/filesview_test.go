package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// index/files.json's exported readers and regenerator (SP-17 R5-1).
//
// These are the functions `qompack fsck` reads and repairs the view through, and the reason they
// exist is that it used to do both through a private copy of the shape, the version constant and
// storeKey. So the tests below are about the two properties that copy could not have: the exported
// replay agrees with the one the store performs on open, and the exported regenerator writes the
// document a Flush would have written.

// appendFilesLogLine appends one raw line to index/files.jsonl. The store must be CLOSED first:
// it holds an append handle on that file while it is open.
func appendFilesLogLine(t *testing.T, l paths.Layout, line string) {
	t.Helper()
	f, err := os.OpenFile(paths.Long(filepath.Join(l.Index, filesLogFile)),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, werr := f.WriteString(line + "\n")
	require.NoError(t, werr)
	require.NoError(t, f.Close())
}

// seedFileVersions records two versions of one path, under two spellings of it, and one version of
// another, then flushes and closes. The flushed view and its log agree, which is the state a
// diagnostic meets on a project that was shut down cleanly.
func seedFileVersions(t *testing.T) (*testProject, paths.Layout) {
	t.Helper()
	tp := newTestStore(t)
	ctx := context.Background()
	for _, v := range []struct {
		path string
		fv   FileVersion
	}{
		{"./src/a.go", FileVersion{TS: 10, Root: core.Hash{1}, Turn: 1, Bytes: 11}},
		{"src/a.go", FileVersion{TS: 20, Root: core.Hash{2}, Turn: 2, Bytes: 12}},
		{"docs/b.md", FileVersion{TS: 30, Root: core.Hash{3}, Turn: 3, Bytes: 13}},
	} {
		require.NoError(t, tp.Store.AppendFileVersion(ctx, v.path, v.fv))
	}
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())
	return tp, paths.Of(tp.Root)
}

// TestReplayFilesLog_IsTheReplayTheStoreItselfPerforms is the anti-drift claim the exported reader
// is here to make good on.
//
// fsck compares index/files.json against index/files.jsonl, and the comparison is only meaningful
// if it replays the log the way the store does — same acceptance rule, same key normalization, same
// ordering. It used to reimplement all three. This asserts the exported replay produces exactly
// what the store's own FileHistory answers after an open, path for path and version for version,
// INCLUDING the two spellings of one path collapsing to one key and the out-of-order append coming
// back ascending by timestamp.
func TestReplayFilesLog_IsTheReplayTheStoreItselfPerforms(t *testing.T) {
	tp, l := seedFileVersions(t)
	// One version recorded out of timestamp order, which is what a hook that saw two reads in the
	// wrong order leaves behind. Only a replay puts it back in place.
	appendFilesLogLine(t, l, `{"v":1,"path":"src/a.go","ts":5,"turn":1,"root":`+
		`"sha256:0000000000000000000000000000000000000000000000000000000000000005","bytes":10}`)

	hist, defects, err := ReplayFilesLog(l)
	require.NoError(t, err)
	require.Empty(t, defects)
	require.Len(t, hist, 2, "the two spellings of src/a.go are one path")

	reopened := openOver(t, tp.project)
	ctx := context.Background()
	for _, p := range []string{"src/a.go", "docs/b.md"} {
		want, herr := reopened.Store.FileHistory(ctx, p)
		require.NoError(t, herr)
		require.Equal(t, want, hist[storeKey(p)], "replayed history of %s differs from the store's", p)
	}
	got := hist[storeKey("src/a.go")]
	require.Len(t, got, 3)
	require.Equal(t, []core.UnixMilli{5, 10, 20}, []core.UnixMilli{got[0].TS, got[1].TS, got[2].TS},
		"versions come back ascending by timestamp whatever order they were appended in")
}

// TestReplayFilesLog_NamesAnUnparseableLineAndKeepsTheRest.
//
// A truncated tail from a crash must not make the rest of the log unreadable, and fsck has to be
// able to tell an operator WHICH line it could not read — the line number is the whole difference
// between "your file-version index has a bad line" and a report someone can act on. The number is
// the FILE's, counted from 1, not an index into the lines that happened to parse.
func TestReplayFilesLog_NamesAnUnparseableLineAndKeepsTheRest(t *testing.T) {
	_, l := seedFileVersions(t)
	appendFilesLogLine(t, l, `{"v":1,"path":"src/c.go","ts":`)
	appendFilesLogLine(t, l, `{"v":1,"path":"src/c.go","ts":40,"turn":4,"root":`+
		`"sha256:0000000000000000000000000000000000000000000000000000000000000004","bytes":14}`)

	hist, defects, err := ReplayFilesLog(l)
	require.NoError(t, err)
	require.Len(t, defects, 1)
	require.Equal(t, 4, defects[0].Line, "the fourth line of the file is the bad one")
	require.NotEmpty(t, defects[0].Why)
	require.Len(t, hist[storeKey("src/c.go")], 1,
		"the good line after the bad one is still replayed")
}

// TestReplayFilesLog_SkipsWhatThisBuildDoesNotReadWithoutCallingItCorrupt.
//
// A record at a future schema version, or one with no path, is not damage: it is a line this build
// has no reader for. Reporting it as a defect would make `qompack fsck` print corruption for a
// project a newer binary wrote and then "repair" the view by dropping the record, so the two cases
// are separated here, at the acceptance rule, exactly as loadFiles separates them.
func TestReplayFilesLog_SkipsWhatThisBuildDoesNotReadWithoutCallingItCorrupt(t *testing.T) {
	_, l := seedFileVersions(t)
	future := `"sha256:0000000000000000000000000000000000000000000000000000000000000009"`
	appendFilesLogLine(t, l, `{"v":2,"path":"src/future.go","ts":50,"turn":5,"root":`+future+`,"bytes":15}`)
	appendFilesLogLine(t, l, `{"v":1,"path":"","ts":60,"turn":6,"root":`+future+`,"bytes":16}`)
	appendFilesLogLine(t, l, ``)

	hist, defects, err := ReplayFilesLog(l)
	require.NoError(t, err)
	require.Empty(t, defects, "an unread record and a blank line are not defects")
	require.NotContains(t, hist, storeKey("src/future.go"))
	require.Len(t, hist, 2, "only the two paths this build reads are replayed")
}

// TestReplayFilesLog_AMissingLogIsAnEmptyHistory. A project that has never recorded a file version
// has no log, and a diagnostic must read that as "no versions" rather than as a failure — fsck
// opens projects that were never used with Qompack at all.
func TestReplayFilesLog_AMissingLogIsAnEmptyHistory(t *testing.T) {
	l := paths.Of(newProject(t).Root)

	hist, defects, err := ReplayFilesLog(l)
	require.NoError(t, err)
	require.Empty(t, defects)
	require.NotNil(t, hist)
	require.Empty(t, hist)
}

// TestReadFilesView_TellsAnAbsentViewFromAnUnparseableOne. The two are different defects with
// different repairs: an absent view over an empty log is not a defect at all, while a view that is
// present and will not parse is one whatever the log says. A reader that collapsed them would let
// --repair create a file in a project that had nothing wrong with it.
func TestReadFilesView_TellsAnAbsentViewFromAnUnparseableOne(t *testing.T) {
	_, l := seedFileVersions(t)
	view := filepath.Join(l.Index, filesViewNam)

	got, present, err := ReadFilesView(l)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, FilesViewVersion, got.Version)
	require.Len(t, got.Files, 2)

	require.NoError(t, os.WriteFile(paths.Long(view), []byte("{not json"), 0o600))
	_, present, err = ReadFilesView(l)
	require.True(t, present, "a view that will not parse is still a view that is there")
	require.ErrorContains(t, err, "does not parse")

	require.NoError(t, os.Remove(paths.Long(view)))
	_, present, err = ReadFilesView(l)
	require.NoError(t, err, "an absent view is not a read failure")
	require.False(t, present)

	require.NoError(t, os.MkdirAll(paths.Long(view), 0o700))
	_, present, err = ReadFilesView(l)
	require.Error(t, err, "a view path that is not a file is a read failure")
	require.True(t, present, "an unreadable view is still a view that is there")
}

// TestRegenerateFilesView_WritesOnlyWhenTheViewAndItsLogDisagree.
//
// --repair's whole claim is that its surface is no wider than the defect surface: a view that
// already describes its log is not rewritten, and an absent view over an empty log is not created.
// Both no-ops are load-bearing — the first keeps a repair from touching a clean project's files at
// all, and the second keeps it from bringing a file into existence in a project that never had one.
func TestRegenerateFilesView_WritesOnlyWhenTheViewAndItsLogDisagree(t *testing.T) {
	_, l := seedFileVersions(t)
	view := filepath.Join(l.Index, filesViewNam)
	clk := newFakeClock()

	before, err := os.ReadFile(paths.Long(view))
	require.NoError(t, err)
	wrote, err := RegenerateFilesView(l, clk)
	require.NoError(t, err)
	require.False(t, wrote, "the flushed view already describes its log")
	after, err := os.ReadFile(paths.Long(view))
	require.NoError(t, err)
	require.Equal(t, before, after)

	empty := paths.Of(newProject(t).Root)
	wrote, err = RegenerateFilesView(empty, clk)
	require.NoError(t, err)
	require.False(t, wrote, "an absent view over an empty log is not a defect")
	_, statErr := os.Stat(paths.Long(filepath.Join(empty.Index, filesViewNam)))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the repair created the view it was not asked for")
}

// TestRegenerateFilesView_ReplacesEveryViewThatDoesNotDescribeItsLog walks the three shapes fsck
// meets: a view that lost a path, one that will not parse at all, and one that is missing while the
// log holds records. Each is replaced by the log's own projection, because the LOG is the truth —
// a crash between the last append and the next Flush leaves the view behind, and rebuilding it from
// the log is what makes that survivable.
func TestRegenerateFilesView_ReplacesEveryViewThatDoesNotDescribeItsLog(t *testing.T) {
	for _, c := range []struct {
		name string
		put  func(t *testing.T, view string)
	}{
		{"a view that lost a path", func(t *testing.T, view string) {
			require.NoError(t, os.WriteFile(paths.Long(view), []byte(`{"version":1,"files":{}}`), 0o600))
		}},
		{"a view that will not parse", func(t *testing.T, view string) {
			require.NoError(t, os.WriteFile(paths.Long(view), []byte("{not json"), 0o600))
		}},
		{"no view at all", func(t *testing.T, view string) {
			require.NoError(t, os.Remove(paths.Long(view)))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, l := seedFileVersions(t)
			view := filepath.Join(l.Index, filesViewNam)
			c.put(t, view)

			wrote, err := RegenerateFilesView(l, newFakeClock())
			require.NoError(t, err)
			require.True(t, wrote)

			got, present, err := ReadFilesView(l)
			require.NoError(t, err)
			require.True(t, present)
			require.Equal(t, FilesViewVersion, got.Version)

			log, _, err := ReplayFilesLog(l)
			require.NoError(t, err)
			require.Equal(t, log, got.Files, "the regenerated view is not the log's projection")

			again, err := RegenerateFilesView(l, newFakeClock())
			require.NoError(t, err)
			require.False(t, again, "a regenerated view still disagrees with its log")
		})
	}
}

// TestRegenerateFilesView_WritesTheDocumentAFlushWouldHave is the property that makes one exported
// regenerator worth more than a correct copy of it.
//
// fsck --repair and Flush now reach index/files.json through the same writer, so given the same log
// and the same instant they produce the same BYTES — key order, version field and all. A copy could
// have been correct about the version and still written a differently ordered or differently shaped
// document that an older reader refuses.
func TestRegenerateFilesView_WritesTheDocumentAFlushWouldHave(t *testing.T) {
	tp, l := seedFileVersions(t)
	view := filepath.Join(l.Index, filesViewNam)

	flushed, err := os.ReadFile(paths.Long(view))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(view), []byte(`{"version":1,"files":{}}`), 0o600))

	// tp.Clock is the clock the store flushed with and has not been advanced since.
	wrote, err := RegenerateFilesView(l, tp.Clock)
	require.NoError(t, err)
	require.True(t, wrote)

	regenerated, err := os.ReadFile(paths.Long(view))
	require.NoError(t, err)
	require.Equal(t, string(flushed), string(regenerated))

	var doc FilesView
	require.NoError(t, json.Unmarshal(regenerated, &doc))
	require.Equal(t, core.NowMilli(tp.Clock), doc.Generated)
}

// TestReplayFilesLog_TreatsAnOverLongLineAsMalformedContent.
//
// A line past the scanner's bound stops the replay where it stands. That is malformed CONTENT, not
// a failure to read the file, and the difference matters: reporting it as an error would make fsck
// give up on a project whose earlier records are perfectly good, and every one of those records is
// a file version something else depends on.
func TestReplayFilesLog_TreatsAnOverLongLineAsMalformedContent(t *testing.T) {
	_, l := seedFileVersions(t)
	appendFilesLogLine(t, l, `{"v":1,"path":"`+strings.Repeat("x", scannerMaxBuf+1)+`"}`)

	hist, defects, err := ReplayFilesLog(l)
	require.NoError(t, err, "an over-long line is content, not a read failure")
	require.NotEmpty(t, defects, "and it is reported rather than passed over in silence")
	require.Len(t, hist, 2, "the records before it are still replayed")
}

// TestRegenerateFilesView_ReportsAViewPathItCanNeitherReadNorReplace.
//
// The one thing a repair must never do is report a repair it did not perform. When the view's path
// is occupied by something that is not a file, the read fails (which is NOT the same as an absent
// view — that case is a no-op), the write fails too, and both come back as an error with wrote
// false, which is what makes fsck print "unchanged" instead of a fresh digest.
func TestRegenerateFilesView_ReportsAViewPathItCanNeitherReadNorReplace(t *testing.T) {
	_, l := seedFileVersions(t)
	view := filepath.Join(l.Index, filesViewNam)
	require.NoError(t, os.Remove(paths.Long(view)))
	require.NoError(t, os.MkdirAll(paths.Long(view), 0o700))

	_, present, err := ReadFilesView(l)
	require.Error(t, err, "a view path that is not a file is a read failure")
	require.True(t, present, "an unreadable view is still a view that is there")

	wrote, err := RegenerateFilesView(l, newFakeClock())
	require.Error(t, err)
	require.False(t, wrote, "a regeneration that could not write must not report that it did")
}

// TestFilesViewAgrees_ComparesPathsAndEntriesRatherThanCounts. Equal counts are not agreement: a
// view holding the same NUMBER of paths as the log, or the same number of versions of one path, can
// still describe different content, and a comparison that stopped at the counts would leave that
// view in place as though the log had been regenerated from it.
func TestFilesViewAgrees_ComparesPathsAndEntriesRatherThanCounts(t *testing.T) {
	t.Parallel()

	v1 := FileVersion{TS: 1, Root: core.Hash{1}, Turn: 1, Bytes: 1}
	v2 := FileVersion{TS: 2, Root: core.Hash{2}, Turn: 2, Bytes: 2}
	log := map[string][]FileVersion{"a": {v1}, "b": {v2}}
	view := func(files map[string][]FileVersion) FilesView {
		return FilesView{Version: FilesViewVersion, Files: files}
	}

	require.True(t, filesViewAgrees(view(map[string][]FileVersion{"a": {v1}, "b": {v2}}), log))
	require.False(t, filesViewAgrees(view(map[string][]FileVersion{"a": {v1}, "c": {v2}}), log),
		"two paths and two paths, but not the same two")
	require.False(t, filesViewAgrees(view(map[string][]FileVersion{"a": {v1}, "b": {v1}}), log),
		"the same paths carrying a different version")
	require.False(t, filesViewAgrees(view(map[string][]FileVersion{"a": {v1, v2}, "b": {v2}}), log),
		"one path carrying an extra version")
	require.False(t, filesViewAgrees(
		FilesView{Version: FilesViewVersion + 1, Files: map[string][]FileVersion{"a": {v1}, "b": {v2}}}, log),
		"a view this build does not read never agrees")
}

// TestWriteFilesView_WritesAnEmptyObjectRatherThanNull. A project with no recorded file version
// still gets a readable view: JSON null would make every reader's map nil and turn "no versions" into
// a decode-time special case each of them has to remember.
func TestWriteFilesView_WritesAnEmptyObjectRatherThanNull(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), filesViewNam)
	require.NoError(t, writeFilesView(p, FilesView{Version: FilesViewVersion}))

	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Contains(t, string(b), `"files":{}`)
	var back FilesView
	require.NoError(t, json.Unmarshal(b, &back))
	require.NotNil(t, back.Files)
	require.Empty(t, back.Files)
}

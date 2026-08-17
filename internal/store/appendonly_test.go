package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// storeJSONLFiles is every append-only log this package writes. index/files.json is deliberately
// NOT in this list: it is a materialized VIEW over files.jsonl, regenerated wholesale by Flush
// through paths.WriteAtomic, and treating it as a log would assert an invariant it does not have.
var storeJSONLFiles = []string{rootsFile, toolUseFile, filesLogFile, sessionsFile, segmentsFile}

// TestAppendOnlyGuard_StoreFiles asserts the append-only property that this package's index logs
// ACTUALLY have — which is not the one the subplan's test table describes.
//
// The subplan says to "attempt truncation of every *.jsonl this package writes" and expect every
// attempt to fail. That assertion cannot be made honestly. paths.IsProtected — the guard behind
// paths.OpenFile — covers exactly three locations: sketches/tried.bloom, everything under
// checkpoints/, and everything under pins/. index/ is not among them, so a truncating
// paths.OpenFile on index/roots.jsonl SUCCEEDS. Writing a test that asserted otherwise would
// either fail or, worse, have to be weakened into something that passes while proving nothing.
//
// What is true, and is what §7.4 actually buys here, is that these files are only ever WRITTEN
// through paths.AppendOnly — which refuses any path that is not *.jsonl/*.ndjson/*.log and opens
// O_APPEND only — and that no code path in this package ever truncates or rewrites one. So the
// observable property is growth-only: whatever bytes a log held at one moment are still there,
// unchanged and at the same offsets, after any later mutation. That is asserted below against real
// mutations: a supersession, a second file version, a segment close and encode, and a re-Flush,
// every one of which is a "change" that a lesser design would have implemented as a rewrite.
func TestAppendOnlyGuard_StoreFiles(t *testing.T) {
	t.Run("append_only_door_refuses_foreign_extensions", func(t *testing.T) {
		tp := newTestStore(t)
		index := paths.Of(tp.Root).Index

		// The materialized view is not a log and must not be reachable through the append door.
		_, err := paths.AppendOnly(filepath.Join(index, filesViewNam))
		require.ErrorIs(t, err, core.ErrAppendOnly,
			"index/files.json is a derived view; AppendOnly must refuse it")

		for _, name := range storeJSONLFiles {
			w, oerr := paths.AppendOnly(filepath.Join(index, name))
			require.NoError(t, oerr, "%s is a log and must be openable through the append door", name)
			require.NoError(t, w.Close())
		}
	})

	t.Run("records_are_never_rewritten", func(t *testing.T) {
		tp := newTestStore(t)
		ctx := context.Background()
		clk := tp.Clock
		const path = "src/appendonly.ts"

		// Establish content in all five logs.
		first, err := tp.Store.PutBytes(ctx, []byte("append-only: first revision\n"), PutOptions{Tool: "FileRead", Path: path})
		require.NoError(t, err)
		clk.Advance(goldenTick)

		argsA, previewA := ArgsDigest(json.RawMessage(`{"file_path":"src/appendonly.ts"}`))
		idA := core.ToolUseID("toolu_01APPENDONLYAAAAAAAAAAAA")
		idB := core.ToolUseID("toolu_01APPENDONLYBBBBBBBBBBBB")
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: idA, Session: "sess-appendonly", Turn: 1, TS: core.UnixMilli(clk.Now().UnixMilli()),
			Tool: "FileRead", ArgsDigest: argsA, ArgsPreview: previewA,
			Root: first.Root.Hash, Path: path, Bytes: first.Root.RawBytes, Tokens: first.Root.Tokens,
		}))
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: idB, Session: "sess-appendonly", Turn: 2, TS: core.UnixMilli(clk.Now().UnixMilli()),
			Tool: "FileRead", ArgsDigest: argsA, ArgsPreview: previewA,
			Root: first.Root.Hash, Path: path, Bytes: first.Root.RawBytes, Tokens: first.Root.Tokens,
		}))
		require.NoError(t, tp.Store.AppendFileVersion(ctx, path, FileVersion{
			TS: core.UnixMilli(clk.Now().UnixMilli()), Root: first.Root.Hash, Turn: 1, Bytes: first.Root.RawBytes,
		}))
		segID, err := tp.Store.Segments().Open(ctx, Segment{Session: "sess-appendonly", StartTurn: 0})
		require.NoError(t, err)
		require.NoError(t, tp.Store.Flush(ctx))

		before := readIndexBytes(t, tp, storeJSONLFiles)
		require.NotEmpty(t, before[rootsFile], "fixture sanity: roots.jsonl must have content to protect")
		require.NotEmpty(t, before[sessionsFile], "fixture sanity: sessions.jsonl must have content to protect")

		// Every one of these is a MUTATION of already-recorded state.
		clk.Advance(goldenTick)
		require.NoError(t, tp.Store.MarkSuperseded(ctx, idA, idB))

		second, err := tp.Store.PutBytes(ctx, []byte("append-only: second revision, longer\n"), PutOptions{Tool: "FileRead", Path: path})
		require.NoError(t, err)
		require.NoError(t, tp.Store.AppendFileVersion(ctx, path, FileVersion{
			TS: core.UnixMilli(clk.Now().UnixMilli()), Root: second.Root.Hash, Turn: 3, Bytes: second.Root.RawBytes,
		}))

		require.NoError(t, tp.Store.Segments().Close(ctx, segID, 4, map[string]float64{"tokens": 12}))
		require.NoError(t, tp.Store.Segments().MarkEncoded(ctx, []core.SegmentID{segID}, 1))

		// A GC that actually collects appends tombstones rather than editing root lines.
		_, err = tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
		require.NoError(t, err)
		require.NoError(t, tp.Store.Flush(ctx))

		after := readIndexBytes(t, tp, storeJSONLFiles)
		for _, name := range storeJSONLFiles {
			require.True(t, bytes.HasPrefix(after[name], before[name]),
				"%s was rewritten: the bytes it held before the mutations are no longer its exact prefix.\n"+
					"append-only means a change is APPENDED as a new record — a supersede line, a tombstone, "+
					"an encode record — never edited into the line that is already there (§7.4)", name)
			require.GreaterOrEqual(t, len(after[name]), len(before[name]), "%s shrank", name)
		}

		// The supersession and the GC tombstone must be visible as appended records, so this is a
		// statement about the mechanism and not merely about file length.
		require.Contains(t, string(after[toolUseFile]), `"op":"supersede"`,
			"supersession must be recorded as an appended mutation record")
		require.Contains(t, string(after[segmentsFile]), `"op":"encode"`,
			"encoding must be recorded as an appended record")
	})

	t.Run("view_is_regenerated_not_appended", func(t *testing.T) {
		// The counterpart assertion: files.json is NOT append-only, and this pins that so the two
		// are never confused. It is derived state, rewritten atomically from the log.
		tp := newTestStore(t)
		ctx := context.Background()
		const path = "src/view.ts"

		res, err := tp.Store.PutBytes(ctx, []byte("view v1\n"), PutOptions{Tool: "FileRead", Path: path})
		require.NoError(t, err)
		require.NoError(t, tp.Store.AppendFileVersion(ctx, path, FileVersion{
			TS: 1, Root: res.Root.Hash, Turn: 1, Bytes: res.Root.RawBytes,
		}))
		require.NoError(t, tp.Store.Flush(ctx))
		firstView := readFileAt(t, filepath.Join(paths.Of(tp.Root).Index, filesViewNam))

		res2, err := tp.Store.PutBytes(ctx, []byte("view v2\n"), PutOptions{Tool: "FileRead", Path: path})
		require.NoError(t, err)
		require.NoError(t, tp.Store.AppendFileVersion(ctx, path, FileVersion{
			TS: 2, Root: res2.Root.Hash, Turn: 2, Bytes: res2.Root.RawBytes,
		}))
		require.NoError(t, tp.Store.Flush(ctx))
		secondView := readFileAt(t, filepath.Join(paths.Of(tp.Root).Index, filesViewNam))

		require.NotEqual(t, string(firstView), string(secondView), "the view must reflect the new version")
		require.False(t, bytes.HasPrefix(secondView, firstView),
			"files.json is a regenerated view, not an append-only log — if this ever becomes a prefix "+
				"relationship, the view has been turned into a log and its invariants have changed")
	})
}

// readIndexBytes reads each named index file whole.
func readIndexBytes(t *testing.T, tp *testProject, names []string) map[string][]byte {
	t.Helper()
	index := paths.Of(tp.Root).Index
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		b, err := os.ReadFile(paths.Long(filepath.Join(index, name)))
		if os.IsNotExist(err) {
			out[name] = nil
			continue
		}
		require.NoError(t, err)
		out[name] = b
	}
	return out
}

// readFileAt reads one file whole through the long-path form.
func readFileAt(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	return b
}

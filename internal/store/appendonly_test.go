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

// storeLog is one log this package writes, located by the layout directory that holds it.
type storeLog struct {
	dir  func(paths.Layout) string
	name string
}

func indexDir(l paths.Layout) string   { return l.Index }
func migrateDir(l paths.Layout) string { return l.Migrate }

// indexLogs are the index logs FSStore itself appends to, every one through paths.AppendOnly.
var indexLogs = []storeLog{
	{indexDir, rootsFile},
	{indexDir, toolUseFile},
	{indexDir, filesLogFile},
	{indexDir, sessionsFile},
	{indexDir, segmentsFile},
}

// migrateLogs are the migration logs Migrator appends to, every one through paths.AppendJSONL:
// the import frontier (importOne, migrate.go), the rollback rehearsals (RehearseRollback,
// backup.go) and the new-format writes (RecordNewFormatWrite, migrate.go).
var migrateLogs = []storeLog{
	{migrateDir, importMappingFile}, {migrateDir, rollbackDrillFile}, {migrateDir, newFormatFile},
}

// storeJSONLFiles is every log this package writes that is append-only BY CONTRACT: its only
// writer goes through paths.AppendOnly (directly, or via paths.AppendJSONL) and no code path in
// this package ever truncates, compacts or rewrites it. Everything else this package writes under
// a .jsonl name, or reads from one, is deliberately NOT in this list:
//
//   - index/files.json is a materialized VIEW over files.jsonl, regenerated wholesale by Flush
//     (materializeFilesJSON, files.go) through paths.WriteAtomic. Treating it as a log would
//     assert an invariant it does not have; view_is_regenerated_not_appended pins that.
//   - state/retention-roots.jsonl is appended by AppendRetentionRoot but COMPACTED by
//     CompactRetentionRoots (lifecycle.go), which rewrites it through paths.WriteAtomic as the set
//     of claims it makes. The TestCompactRetentionRoots_* tests pin that contract.
//   - state/demand.jsonl is appended by DemandLog.Record but COMPACTED by CompactDemandLog
//     (phase7maint.go), which replaces it by a verified staging-file rename. The
//     TestCompactDemandLog_* tests pin that contract.
//   - state/delivery-leases.jsonl and state/delivery-acks.jsonl are internal/daemon's journals
//     (delivery_lease.go); records/eliminations.jsonl belongs to internal/negknow (log.go);
//     pins/invariants.jsonl belongs to internal/pins (store.go); records/evidence.jsonl has no
//     writer in this tree at all. This package only READS them, as GC retention roots
//     (gcRootFiles, gcrun.go), so their append-only property is their owners' to guard, not this
//     test's.
var storeJSONLFiles = append(append([]storeLog{}, indexLogs...), migrateLogs...)

// TestAppendOnlyGuard_StoreFiles asserts the append-only property that this package's logs
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
		l := paths.Of(tp.Root)

		// The materialized view is not a log and must not be reachable through the append door.
		_, err := paths.AppendOnly(filepath.Join(l.Index, filesViewNam))
		require.ErrorIs(t, err, core.ErrAppendOnly,
			"index/files.json is a derived view; AppendOnly must refuse it")

		for _, lg := range storeJSONLFiles {
			require.NoError(t, os.MkdirAll(paths.Long(lg.dir(l)), 0o700))
			w, oerr := paths.AppendOnly(filepath.Join(lg.dir(l), lg.name))
			require.NoError(t, oerr, "%s is a log and must be openable through the append door", lg.name)
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

		before := readLogBytes(t, tp.Root, indexLogs)
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

		after := readLogBytes(t, tp.Root, indexLogs)
		requireGrowthOnly(t, indexLogs, before, after)

		// The supersession and the GC tombstone must be visible as appended records, so this is a
		// statement about the mechanism and not merely about file length.
		require.Contains(t, string(after[toolUseFile]), `"op":"supersede"`,
			"supersession must be recorded as an appended mutation record")
		require.Contains(t, string(after[segmentsFile]), `"op":"encode"`,
			"encoding must be recorded as an appended record")
	})

	t.Run("migration_logs_are_never_rewritten", func(t *testing.T) {
		// The same growth-only property for the three migration logs, against the mutations a
		// lesser design would have implemented as a rewrite: resuming an interrupted import
		// (rebuilding the frontier from the cursor), a second new-format write (editing the
		// "first" line), and a second rollback rehearsal (replacing the drill record).
		tp := newTestStore(t)
		ctx := context.Background()
		src := legacySource(3)
		src.failAt = 3 // hands out two records, then fails
		m := newMigrator(t, tp, src)
		stop := func(context.Context) error { return nil }

		// mapping.jsonl: an interrupted import commits two frontier lines...
		_, err := m.Import(ctx)
		require.Error(t, err, "fixture sanity: the interrupted import must fail")
		before := readLogBytes(t, tp.Root, migrateLogs)
		require.NotEmpty(t, before[importMappingFile], "fixture sanity: mapping.jsonl must have content to protect")

		// ...and resuming it must APPEND the third rather than rebuild the frontier.
		src.failAt, src.handed = 0, 0
		_, err = m.Import(ctx)
		require.NoError(t, err)

		// rollback.jsonl and newformat.jsonl: one rehearsal and the first new-format write.
		_, err = m.TakeBackup(ctx, "pre-cutover")
		require.NoError(t, err)
		pre, err := m.RehearseRollback(ctx, RollbackOptions{
			Phase: RollbackBeforeFirstNewWrite, BackupID: "pre-cutover",
			RestoreRoot: filepath.Join(t.TempDir(), "restore-pre"), StopWriters: stop,
		})
		require.NoError(t, err)
		require.True(t, pre.OK, "fixture sanity: refusal: %s", pre.Refusal)
		_, err = m.Cutover(ctx, CutoverOptions{BackupID: "pre-cutover", StopLegacyWriter: stop})
		require.NoError(t, err)
		first, err := tp.Store.PutBytes(ctx, []byte("new-format: first write\n"), PutOptions{Tool: "FileRead", Path: "src/new1.ts"})
		require.NoError(t, err)
		firstWrite, err := m.RecordNewFormatWrite(ctx, first.Root.Hash, "")
		require.NoError(t, err)
		require.True(t, firstWrite.First, "fixture sanity: this is the first new-format write")
		established := readLogBytes(t, tp.Root, migrateLogs)
		before[rollbackDrillFile], before[newFormatFile] = established[rollbackDrillFile], established[newFormatFile]
		require.NotEmpty(t, before[rollbackDrillFile], "fixture sanity: rollback.jsonl must have content to protect")
		require.NotEmpty(t, before[newFormatFile], "fixture sanity: newformat.jsonl must have content to protect")

		// The second write ends the "first" distinction, and the second rehearsal is the after
		// phase against the actual new artifact: both are changes to state the logs already
		// record, and both must land as appended lines.
		second, err := tp.Store.PutBytes(ctx, []byte("new-format: second write\n"), PutOptions{Tool: "FileRead", Path: "src/new2.ts"})
		require.NoError(t, err)
		secondWrite, err := m.RecordNewFormatWrite(ctx, second.Root.Hash, "")
		require.NoError(t, err)
		require.False(t, secondWrite.First)
		post, err := m.RehearseRollback(ctx, RollbackOptions{
			Phase: RollbackAfterFirstNewWrite, BackupID: "pre-cutover",
			RestoreRoot: filepath.Join(t.TempDir(), "restore-post"), StopWriters: stop,
		})
		require.NoError(t, err)
		require.True(t, post.OK, "fixture sanity: refusal: %s", post.Refusal)

		after := readLogBytes(t, tp.Root, migrateLogs)
		requireGrowthOnly(t, migrateLogs, before, after)

		// As above: the mutations must be visible as appended records, not merely as growth.
		require.Len(t, mappingLines(t, tp.Root), 3, "the resumed import appends exactly the missing frontier line")
		require.Contains(t, string(after[newFormatFile]), `"first":false`,
			"a later new-format write must be recorded as its own appended line")
		require.Contains(t, string(after[rollbackDrillFile]), `"phase":"`+string(RollbackAfterFirstNewWrite)+`"`,
			"the after-phase rehearsal must be recorded as an appended drill record")
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

// requireGrowthOnly asserts that every log's earlier bytes are the exact prefix of its later ones.
func requireGrowthOnly(t *testing.T, logs []storeLog, before, after map[string][]byte) {
	t.Helper()
	for _, lg := range logs {
		require.True(t, bytes.HasPrefix(after[lg.name], before[lg.name]),
			"%s was rewritten: the bytes it held before the mutations are no longer its exact prefix.\n"+
				"append-only means a change is APPENDED as a new record — a supersede line, a tombstone, "+
				"an encode record — never edited into the line that is already there (§7.4)", lg.name)
		require.GreaterOrEqual(t, len(after[lg.name]), len(before[lg.name]), "%s shrank", lg.name)
	}
}

// readLogBytes reads each named log whole, keyed by its basename; a log that does not exist yet
// reads as nil.
func readLogBytes(t *testing.T, root string, logs []storeLog) map[string][]byte {
	t.Helper()
	l := paths.Of(root)
	out := make(map[string][]byte, len(logs))
	for _, lg := range logs {
		b, err := os.ReadFile(paths.Long(filepath.Join(lg.dir(l), lg.name)))
		if os.IsNotExist(err) {
			out[lg.name] = nil
			continue
		}
		require.NoError(t, err)
		out[lg.name] = b
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

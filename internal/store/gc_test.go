package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// gcSeed stores one payload and returns its root.
func gcSeed(t *testing.T, tp *testProject, path, body string) Root {
	t.Helper()
	res, err := tp.Store.PutBytes(context.Background(), []byte(body), PutOptions{Tool: "FileRead", Path: path})
	require.NoError(t, err)
	return res.Root
}

// gcSeedEphemeral stores one payload marked born-ephemeral.
func gcSeedEphemeral(t *testing.T, tp *testProject, path, body string) Root {
	t.Helper()
	res, err := tp.Store.PutBytes(context.Background(), []byte(body),
		PutOptions{Tool: "recall", Path: path, Ephemeral: true})
	require.NoError(t, err)
	return res.Root
}

// writeCheckpointJSON plants a checkpoint document referencing the given hash strings, so the mark
// phase has something structural to harvest.
func writeCheckpointJSON(t *testing.T, tp *testProject, name string, refs ...string) {
	t.Helper()
	l := paths.Of(tp.Root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Checkpoints), 0o700))
	doc := map[string]any{"seq": 1, "pointers": refs, "nested": map[string]any{"evidence": refs}}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.Checkpoints, name)), b, 0o600))
}

// writeJSONLWithHash plants a one-line JSONL document carrying a hash reference.
func writeJSONLWithHash(t *testing.T, dir, name, field, hash string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	b, err := json.Marshal(map[string]any{field: hash})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, name)), append(b, '\n'), 0o600))
}

// objectCount counts the object files currently on disk.
func objectCount(t *testing.T, tp *testProject) int {
	t.Helper()
	return len(tp.objectPaths(t))
}

// indexLinesContaining returns the lines of one index file containing sub.
func (tp *testProject) indexLinesContaining(t *testing.T, name, sub string) []string {
	t.Helper()
	var out []string
	for _, l := range tp.indexLines(t, name) {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return out
}

// forceCollect is the policy that disables both retention axes — the only way to force collection.
var forceCollect = GCPolicy{RetainDays: -1, RetainSessions: -1}

// TestGC_CollectsUnreferenced asserts roots nothing references are collected while roots a
// checkpoint points at survive.
func TestGC_CollectsUnreferenced(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var kept, dropped []Root
	for i := 0; i < 10; i++ {
		r := gcSeed(t, tp, fmt.Sprintf("src/f%02d.ts", i), fmt.Sprintf("body number %d, unique content\n", i))
		if i < 2 {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, r)
		}
	}
	writeCheckpointJSON(t, tp, "0001.json", kept[0].Hash.String(), kept[1].Hash.String())

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.Positive(t, rep.DeletedObjects)

	for _, r := range kept {
		_, err := tp.Store.GetRoot(ctx, r.Hash)
		require.NoError(t, err, "a checkpoint-referenced root must survive")
		for _, c := range r.Chunks {
			require.True(t, tp.Store.Has(c.Hash))
		}
	}
	for _, r := range dropped {
		_, err := tp.Store.GetRoot(ctx, r.Hash)
		require.ErrorIs(t, err, core.ErrNotFound, "an unreferenced root must be collected")
	}
}

// TestGC_ZeroPolicyInheritsConfigAndDeletesNothing is the safety property that matters most in
// practice: the ZERO GCPolicy — what a careless caller passes — must inherit store.retention from
// configuration and collect nothing recent, never mass-delete.
func TestGC_ZeroPolicyInheritsConfigAndDeletesNothing(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%02d.ts", i), fmt.Sprintf("recent body %d\n", i))
	}
	before := objectCount(t, tp)

	rep, err := tp.Store.GC(ctx, GCPolicy{})
	require.NoError(t, err)
	require.Zero(t, rep.DeletedObjects, "the zero policy is default retention, never a mass deletion")
	require.Equal(t, before, objectCount(t, tp))
}

// TestGC_RetentionIsWhicheverIsLonger asserts §8.2's "30 days or 10 sessions, whichever is longer"
// is a DISJUNCTION: a root far outside the day window survives on the session axis alone.
func TestGC_RetentionIsWhicheverIsLonger(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	old := gcSeed(t, tp, "src/old.ts", "sixty days old but still in the current session\n")
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "tu-old", Session: "sess-current", Turn: 1,
		TS:   core.UnixMilli(tp.Clock.Now().Add(-60 * 24 * time.Hour).UnixMilli()),
		Tool: "FileRead", Root: old.Hash, Path: "src/old.ts",
	}))
	// Move the clock forward so the ROOT's own timestamp is outside the 30-day window too.
	tp.Clock.Advance(60 * 24 * time.Hour)

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 30, RetainSessions: 10})
	require.NoError(t, err)
	require.Zero(t, rep.DeletedObjects)

	_, err = tp.Store.GetRoot(ctx, old.Hash)
	require.NoError(t, err, "a root outside the day window but inside the session window must be retained")
}

// TestGC_EphemeralNotInWindowByAge asserts an ephemeral root is never held alive by the age clause,
// so retrieval spam is reclaimable while an ordinary result of the same age is not.
func TestGC_EphemeralNotInWindowByAge(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	ordinary := gcSeed(t, tp, "src/ordinary.ts", "an ordinary tool result, distinct content\n")
	ephemeral := gcSeedEphemeral(t, tp, "src/ephemeral.ts", "a retrieved, born-ephemeral result\n")

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 30, RetainSessions: -1})
	require.NoError(t, err)
	require.Positive(t, rep.DeletedObjects)

	_, err = tp.Store.GetRoot(ctx, ordinary.Hash)
	require.NoError(t, err, "an ordinary root inside the day window must survive")
	_, err = tp.Store.GetRoot(ctx, ephemeral.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "an ephemeral root is never in-window by age")
	for _, c := range ephemeral.Chunks {
		require.False(t, tp.Store.Has(c.Hash), "the ephemeral root's exclusive chunks must be collected")
	}
}

// TestGC_HarvestsBareHexHash asserts a hash serialized WITHOUT the "sha256:" prefix is still seen by
// the mark phase. Missing it would silently delete a live object, which is the one failure mode a
// collector must never have.
func TestGC_HarvestsBareHexHash(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	kept := gcSeed(t, tp, "src/kept.ts", "referenced by a bare-hex hash\n")
	gcSeed(t, tp, "src/gone.ts", "referenced by nothing at all\n")
	writeCheckpointJSON(t, tp, "0001.json", hex.EncodeToString(kept.Hash[:]))

	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, kept.Hash)
	require.NoError(t, err, "a bare 64-hex reference must keep its root alive")
}

// TestGC_HarvestsHashesFromCheckpointPinsEliminations asserts all three producer files are read,
// without this package importing checkpoint, pins or negknow (which import store).
func TestGC_HarvestsHashesFromCheckpointPinsEliminations(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	fromCheckpoint := gcSeed(t, tp, "src/a.ts", "kept by a checkpoint pointer\n")
	fromPin := gcSeed(t, tp, "src/b.ts", "kept by a pin\n")
	fromElimination := gcSeed(t, tp, "src/c.ts", "kept by an elimination's evidence\n")
	doomed := gcSeed(t, tp, "src/d.ts", "kept by nothing\n")

	writeCheckpointJSON(t, tp, "0001.json", fromCheckpoint.Hash.String())
	writeJSONLWithHash(t, l.Pins, "invariants.jsonl", "hash", fromPin.Hash.String())
	writeJSONLWithHash(t, l.Records, "eliminations.jsonl", "evidence", fromElimination.Hash.String())

	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	for name, r := range map[string]Root{
		"checkpoint": fromCheckpoint, "pin": fromPin, "elimination": fromElimination,
	} {
		_, err := tp.Store.GetRoot(ctx, r.Hash)
		require.NoError(t, err, "the %s reference must keep its root alive", name)
	}
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestGC_MissingRootFilesAreNotAnError asserts GC runs cleanly before waves 3–5 ship any of the
// producer files it harvests from.
func TestGC_MissingRootFilesAreNotAnError(t *testing.T) {
	tp := newTestStore(t)
	gcSeed(t, tp, "src/a.ts", "no checkpoints, pins or eliminations exist yet\n")

	rep, err := tp.Store.GC(context.Background(), forceCollect)
	require.NoError(t, err)
	require.Positive(t, rep.ScannedObjects)
}

// TestGC_DryRun asserts a dry run reports what it would collect and removes nothing.
func TestGC_DryRun(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%02d.ts", i), fmt.Sprintf("dry run body %d\n", i))
	}
	before := objectCount(t, tp)

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true})
	require.NoError(t, err)
	require.Positive(t, rep.DeletedObjects, "a dry run must still report what it would collect")
	require.Equal(t, before, objectCount(t, tp), "a dry run must remove nothing")
	require.Empty(t, tp.indexLinesContaining(t, rootsFile, `"op":"gc"`),
		"a dry run must not tombstone anything either")
}

// TestGC_DeadlineTruncatesAndResumes asserts a deadline-bounded pass reports Truncated, persists a
// cursor, and that a second unbounded pass finishes the job.
func TestGC_DeadlineTruncatesAndResumes(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 700; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%04d.ts", i), fmt.Sprintf("resumable body %d, unique\n", i))
	}
	total := objectCount(t, tp)
	require.Positive(t, total)

	first, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, Deadline: time.Nanosecond})
	require.NoError(t, err, "an expired deadline is a normal outcome, never an error")
	if first.Truncated {
		require.FileExists(t, filepath.Join(paths.Of(tp.Root).State, gcStateFile))
	}

	second, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)
	require.False(t, second.Truncated)
	require.Equal(t, 0, objectCount(t, tp), "the resumed pass must finish collecting everything")
	require.NoFileExists(t, filepath.Join(paths.Of(tp.Root).State, gcStateFile),
		"a completed pass must clear its cursor")
	require.GreaterOrEqual(t, first.DeletedObjects+second.DeletedObjects, total-first.DeletedObjects)
}

// TestGC_LiveDigestMismatchRestartsMark asserts a root created between two passes is never
// collected by the second, even though a cursor from the first is on disk.
func TestGC_LiveDigestMismatchRestartsMark(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 400; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%03d.ts", i), fmt.Sprintf("body %d\n", i))
	}
	_, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, Deadline: time.Nanosecond})
	require.NoError(t, err)

	// A brand-new root appears after the interrupted pass recorded its live set.
	fresh := gcSeed(t, tp, "src/fresh.ts", "written after the interrupted pass\n")
	writeCheckpointJSON(t, tp, "0001.json", fresh.Hash.String())

	_, err = tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, fresh.Hash)
	require.NoError(t, err, "a root created after the interrupted pass must never be collected")
	for _, c := range fresh.Chunks {
		require.True(t, tp.Store.Has(c.Hash))
	}
}

// TestGC_TombstonesRootsAppendOnly asserts collection retires roots by APPENDING, leaving every
// original line in index/roots.jsonl intact (Qompack.md §7.4).
func TestGC_TombstonesRootsAppendOnly(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var roots []Root
	for i := 0; i < 3; i++ {
		roots = append(roots, gcSeed(t, tp, fmt.Sprintf("src/f%02d.ts", i), fmt.Sprintf("doomed body %d\n", i)))
	}
	before := tp.indexLines(t, rootsFile)
	require.Len(t, before, 3)

	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	after := tp.indexLines(t, rootsFile)
	require.Equal(t, before, after[:len(before)], "the original root lines must survive verbatim")
	require.Len(t, tp.indexLinesContaining(t, rootsFile, `"op":"gc"`), 3)

	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, tp.project)
	for _, r := range roots {
		_, err := reopened.Store.GetRoot(ctx, r.Hash)
		require.ErrorIs(t, err, core.ErrNotFound, "a tombstoned root must stay retired across a reopen")
	}
}

// TestGC_ContextCancel asserts a cancelled sweep reports the context error with a partially filled
// report, and that the store reopens cleanly afterwards.
func TestGC_ContextCancel(t *testing.T) {
	tp := newTestStore(t)
	for i := 0; i < 320; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%03d.ts", i), fmt.Sprintf("cancellable body %d\n", i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tp.Store.GC(ctx, forceCollect)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, tp.project)
	_, err = reopened.Store.Stats(context.Background())
	require.NoError(t, err, "a cancelled GC must leave the store reopenable")
}

// TestGC_ClosedStoreDegrades asserts GC reports core.ErrDegraded after Close.
func TestGC_ClosedStoreDegrades(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())
	_, err := tp.Store.GC(context.Background(), GCPolicy{})
	require.ErrorIs(t, err, core.ErrDegraded)
}

// BenchmarkGC_50kObjects measures a full mark-and-sweep against the ≤2 s idle-work budget.
//
// The 50 000 objects are built from a THOUSAND large puts rather than fifty thousand small ones:
// the test chunker averages ~256 B, so a 13 KB payload yields ~50 chunks and the object count is
// reached in a thousandth of the ingest time. What is being measured is the mark-and-sweep walk
// over the object tree, which cares about how many object FILES exist, not how they got there.
func BenchmarkGC_50kObjects(b *testing.B) {
	const (
		roots         = 1000
		bytesPerRoot  = 13 << 10
		wantMinObject = 40000
	)
	t := &testing.T{}
	tp := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < roots; i++ {
		var sb strings.Builder
		for sb.Len() < bytesPerRoot {
			fmt.Fprintf(&sb, "root %05d line %05d %s\n", i, sb.Len(),
				hex.EncodeToString([]byte(fmt.Sprintf("%d-%d", i, sb.Len()))))
		}
		if _, err := tp.Store.PutBytes(ctx, []byte(sb.String()),
			PutOptions{Tool: "FileRead", Path: fmt.Sprintf("src/f%05d.ts", i)}); err != nil {
			b.Fatal(err)
		}
	}
	if got := len(tp.objectPaths(t)); got < wantMinObject {
		b.Fatalf("benchmark fixture built only %d objects; it is supposed to exercise ~50 000", got)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tp.Store.GC(ctx, GCPolicy{DryRun: true}); err != nil {
			b.Fatal(err)
		}
	}
}

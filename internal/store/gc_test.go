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

// gcResumeSeeds is how many roots the resume tests plant. It must exceed gcCheckEvery, or the
// sweep never reaches a deadline check and truncation cannot happen at all — which is exactly the
// hole the old version of TestGC_DeadlineTruncatesAndResumes had, having guarded everything it
// wanted to prove behind `if first.Truncated`.
const gcResumeSeeds = 700

// gcResumeKept is how many of those roots a checkpoint holds live, so that "the two passes collect
// the same set as one pass" is a statement about a SET rather than about "everything".
const gcResumeKept = 50

// seedGCCorpus plants gcResumeSeeds deterministic roots and pins the first gcResumeKept of them
// with a checkpoint. Two calls produce byte-identical object trees, which is what lets one be
// collected in two bounded passes and the other in one unbounded pass and the results compared.
func seedGCCorpus(t *testing.T) *testProject {
	t.Helper()
	tp := newTestStore(t)
	refs := make([]string, 0, gcResumeKept)
	for i := 0; i < gcResumeSeeds; i++ {
		r := gcSeed(t, tp, fmt.Sprintf("src/f%04d.ts", i), fmt.Sprintf("resumable body %d, unique\n", i))
		if i < gcResumeKept {
			refs = append(refs, r.Hash.String())
		}
	}
	writeCheckpointJSON(t, tp, "0001.json", refs...)
	return tp
}

// TestGC_DeadlineTruncatesAndResumes asserts a deadline-bounded pass really truncates, persists a
// cursor, stops at the first deadline check, and that resuming from that cursor collects exactly
// the set one unbounded pass collects.
//
// It used to guard the truncation half behind `if first.Truncated` and close with
// `first.Deleted+second.Deleted >= total-first.Deleted`, which is satisfied by
// `0 >= 0`: a GC that truncated nothing and deleted nothing passed it. Truncation is now forced
// structurally rather than hoped for — gcResumeSeeds exceeds gcCheckEvery, so an already-expired
// deadline is guaranteed to be seen — and the resume property is checked against a CONTROL store
// carrying the identical corpus, collected in one pass.
func TestGC_DeadlineTruncatesAndResumes(t *testing.T) {
	ctx := context.Background()
	resumed, control := seedGCCorpus(t), seedGCCorpus(t)

	before := resumed.objectPaths(t)
	require.Equal(t, before, control.objectPaths(t),
		"fixture sanity: the two corpora must be identical, or comparing their outcomes proves nothing")
	require.Greater(t, len(before), gcCheckEvery,
		"fixture sanity: the sweep must walk past at least one deadline check for truncation to be reachable")

	cursor := filepath.Join(paths.Of(resumed.Root).State, gcStateFile)

	first, err := resumed.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, Deadline: time.Nanosecond})
	require.NoError(t, err, "an expired deadline is a normal outcome, never an error")
	require.True(t, first.Truncated,
		"a deadline that has already expired, over %d objects, must truncate", len(before))
	require.FileExists(t, cursor, "a truncated pass must leave a cursor to resume from")
	require.Positive(t, first.DeletedObjects, "a truncated pass must still have done real work")
	require.Less(t, first.ScannedObjects, gcCheckEvery,
		"an already-expired deadline must stop the sweep at its FIRST check, not one interval later")
	require.Less(t, len(resumed.objectPaths(t)), len(before), "objects must actually have been removed")
	require.Greater(t, len(resumed.objectPaths(t)), gcResumeKept, "and work must remain for the resumed pass")

	second, err := resumed.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)
	require.False(t, second.Truncated)
	require.NoFileExists(t, cursor, "a completed pass must clear its cursor")

	one, err := control.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)
	require.False(t, one.Truncated)

	// The union property, on the three quantities that can differ: the surviving object SET, the
	// cumulative report, and the roots the checkpoint holds. A resumed report is cumulative — GC
	// seeds it from the persisted cursor's counters — so the two-pass totals are directly
	// comparable with the one-pass ones, and an object swept twice or missed once breaks them.
	require.Equal(t, control.objectPaths(t), resumed.objectPaths(t),
		"two bounded passes must leave exactly the object set one unbounded pass leaves")
	require.Len(t, resumed.objectPaths(t), gcResumeKept, "and the checkpoint-held roots must be what survives")
	require.Equal(t, one.DeletedObjects, second.DeletedObjects,
		"the resumed pass's cumulative deletion count must equal the unbounded pass's")
	require.Equal(t, one.ScannedObjects, second.ScannedObjects,
		"and so must the cumulative scan count: an object swept twice would show up here")
	require.Equal(t, one.BytesFreed, second.BytesFreed)

	for i := 0; i < gcResumeKept; i++ {
		res, perr := resumed.Store.PutBytes(ctx, []byte(fmt.Sprintf("resumable body %d, unique\n", i)),
			PutOptions{Tool: "FileRead", Path: fmt.Sprintf("src/f%04d.ts", i)})
		require.NoError(t, perr)
		require.Zero(t, res.Novel, "a checkpoint-held root's chunks must have survived both passes intact")
	}
}

// gcDeadlineOvershoot is how far past its deadline a truncating sweep may run.
//
// V2-SP06-20 states "deadline honoured ±50 ms". The bound is not a wall-clock guess: the sweep
// consults the deadline once every gcCheckEvery objects, so the overshoot is the cost of finishing
// the batch in progress plus persisting the cursor, and nothing else. Measured over eight trials
// on an idle Windows development host, 1 500 objects: 6.1, 9.5, 12.3, 16.7, 18.5, 18.9, 20.7 and
// 24.4 ms, against a full unbounded sweep of 116–146 ms. The CI Linux runner's open() is roughly
// ten times cheaper (§2.6a ⑥), so this is the pessimistic platform.
const gcDeadlineOvershoot = 50 * time.Millisecond

// gcOvershootCeiling is the point past which no amount of host slowness excuses the overshoot.
//
// The assertion below relaxes gcDeadlineOvershoot on a slow host, because the overshoot is one
// check interval and a check interval costs what 256 object visits cost there — the SAME 1 500
// objects swept while the rest of this package's suite was running measured 31.3 ms against 6–24
// on an idle machine, and a wall-clock bound that fails for that reason is a bound nobody trusts.
// The relaxation is capped so it cannot swallow a real regression: if two check intervals cost
// more than this, gcCheckEvery is too coarse for the 2 s idle budget the deadline exists to fit
// inside, and the constant needs revisiting rather than the assertion.
const gcOvershootCeiling = 250 * time.Millisecond

// gcOvershootSeeds is large enough that half of a full sweep lands well inside the sweep rather
// than inside the mark phase, which is what makes the measurement below one of check granularity
// rather than one of setup cost.
const gcOvershootSeeds = 1500

// TestGC_DeadlineOvershootIsBoundedByTheCheckInterval asserts V2-SP06-20's ±50 ms.
//
// The deadline is CALIBRATED rather than fixed: an unbounded dry run over the same tree is timed
// first and half of that becomes the budget. A fixed short deadline would expire before the sweep
// began, and the overshoot would then measure the mark phase, not the check interval. It runs dry
// on purpose for the same reason — GCPolicy.Deadline is consulted only inside sweep, so a pass
// that also tombstones several hundred dead roots spends unbounded time before the first check,
// and folding that in would measure a different thing. (That tombstoning is unbounded by the
// deadline is a real, separate gap; it is recorded rather than papered over here.)
func TestGC_DeadlineOvershootIsBoundedByTheCheckInterval(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < gcOvershootSeeds; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%04d.ts", i), fmt.Sprintf("overshoot body %d, unique\n", i))
	}
	require.Greater(t, objectCount(t, tp), gcCheckEvery*4,
		"fixture sanity: the sweep must cross several deadline checks")

	full, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true})
	require.NoError(t, err)
	require.False(t, full.Truncated, "the calibration pass must complete")
	require.Positive(t, full.ScannedObjects)
	budget := full.Duration / 2

	start := time.Now()
	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true, Deadline: budget})
	overshoot := time.Since(start) - budget
	require.NoError(t, err)
	require.True(t, rep.Truncated, "half of a measured full sweep must not be enough to finish it")
	require.Greater(t, rep.ScannedObjects, gcCheckEvery,
		"calibration check: the deadline must expire INSIDE the sweep, or this measures the mark phase")

	// One check interval, priced on THIS host from the calibration pass. The limit is the §2.6 row's
	// 50 ms, or two intervals when a single interval is already close to it — see gcOvershootCeiling.
	interval := full.Duration / time.Duration(full.ScannedObjects) * gcCheckEvery
	limit := gcDeadlineOvershoot
	if twoIntervals := 2 * interval; twoIntervals > limit {
		limit = twoIntervals
	}
	t.Logf("full sweep %v over %d objects; check interval %v; budget %v; overshoot %v after %d objects; limit %v",
		full.Duration, full.ScannedObjects, interval, budget, overshoot, rep.ScannedObjects, limit)

	require.LessOrEqual(t, limit, gcOvershootCeiling,
		"two deadline checks cost %v on this host, so gcCheckEvery (%d) is too coarse to honour a "+
			"deadline inside the 2 s idle budget; revisit the constant, not this assertion", 2*interval, gcCheckEvery)
	require.LessOrEqual(t, overshoot, limit,
		"V2-SP06-20: a truncating sweep must return within %v of its deadline; it ran %v over after "+
			"scanning %d objects at a check interval of %d (%v)",
		limit, overshoot, rep.ScannedObjects, gcCheckEvery, interval)
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
// the granular chunker averages ~256 B, so a 13 KB payload yields ~50 chunks and the object count
// is reached in a thousandth of the ingest time. What is being measured is the mark-and-sweep walk
// over the object tree, which cares about how many object FILES exist, not how they got there —
// which is why this is one of the two places still entitled to a chunker double. Reaching 50 000
// objects through the real 4 KiB-average chunker would mean ingesting ~200 MB of unique content per
// benchmark setup, measuring the ingest path rather than the collector.
func BenchmarkGC_50kObjects(b *testing.B) {
	const (
		roots         = 1000
		bytesPerRoot  = 13 << 10
		wantMinObject = 40000
	)
	t := &testing.T{}
	tp := newTestStore(t, withGranularChunker())
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

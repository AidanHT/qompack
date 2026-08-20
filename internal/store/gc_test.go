package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// gcOvershootSeeds is the fixture size, and it is a whole number of gcCheckEvery intervals on
// purpose.
//
// Two things must be true for the calibrated budget below to land where the measurement needs it:
// after the sweep's FIRST deadline check, or the overshoot measures the mark phase, and before its
// LAST, or the sweep finishes and never truncates at all. The checks fall at objects 256, 512, …,
// so in units of the judged sweep those two points are gcCheckEvery/N and
// floor(N/gcCheckEvery)*gcCheckEvery/N. A budget of half a calibrated sweep therefore tolerates
// the judged pass running up to N/(2*gcCheckEvery) times SLOWER than calibration, and up to
// N/(2*floor(N/gcCheckEvery)*gcCheckEvery) — 2x, once N is a whole number of intervals — FASTER.
//
// 1 500 was neither large enough nor a whole number of intervals: its last 220 objects (14.7 % of
// the sweep) fell past the final check, which cut the fast-side tolerance from 2x to 1.71x, and the
// slow side stood at 2.93x. Twelve whole intervals buy 2x fast and 6x slow instead, and both ends
// widen on EVERY draw even after the estimator change below, which lowers the number they are
// multiples of. Eight consecutive unbounded passes over the identical tree, three trials on a
// Windows development host, put the fastest of four at 0.74x, 0.88x and 0.95x of the first, so 2x
// of the fastest is at most 0.5x of the single first sample the shipped test priced from (against
// its 0.586x), and 6x of the fastest is at least 4.4x of it (against its 2.93x) — the slow bracket
// widens by half again even in the draw where the first sample was the slowest one going. Eight
// intervals would only have broken even there, which is why this is twelve.
//
// The size is worth its cost only because BOTH ends get used. Quiet, the same three trials put the
// judged pass between 0.81x and 1.31x of the fastest-of-four calibration — nowhere near either
// bracket. Busy, the ratio leaves the band at both ends, and neither end is hypothetical. The fast
// end is the CI failure, and it reproduces off CI: five runs of the SHIPPED test on a Windows host
// whose sweep had slowed fourfold over a working session failed twice, both times on "half of a
// measured full sweep must not be enough to finish it", while nineteen runs of the fastest-of-four
// rule on that same host, at this fixture size and at 2 048, never once reached it. The slow end
// is the same estimator seen from the other side, and it showed up under an I/O
// co-load storm spanning both passes: two of five runs of the SHIPPED test truncated at the very
// first check instead, 255 objects, its 2.93x bracket. Seeding is linear in the fixture — the same
// per-object cost at 1 500, 1 536, 2 048 and 3 072 seeds, measured, with no penalty for the larger
// tree — so the whole price of the wider band is 1 572 more seeds.
const gcOvershootSeeds = 12 * gcCheckEvery

// gcCalibrationPasses is how many unbounded sweeps price the budget below, of which the FASTEST
// wins.
//
// One sample is not a measurement of this host; it is a measurement of this host's next few tens
// of milliseconds. A sweep can only be pushed SLOWER than what the machine costs — by the
// co-scheduled package binaries `devtool cover` runs, by the first walk of a tree the seeding loop
// has only just finished writing, by one scheduler slice lost to another runnable goroutine —
// never faster, so the minimum over several samples is the closest estimate of what the judged
// pass will cost and every slower sample is noise of known sign. That is the same estimator change
// item 18 of plans/V2-report.md §0 made to TestSliceLatencyBudget (median-of-20 to fastest-of-20,
// c18edb0), for the same reason and in the same direction.
const gcCalibrationPasses = 4

// gcCalibratedSweep is the deadline budget's estimator: of the unbounded passes measured over the
// tree, the FASTEST one prices the budget. See gcCalibrationPasses for why the minimum, and
// TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass for the arithmetic it has to
// satisfy, asserted without a clock.
func gcCalibratedSweep(passes []GCReport) GCReport {
	fastest := passes[0]
	for _, p := range passes[1:] {
		if p.Duration < fastest.Duration {
			fastest = p
		}
	}
	return fastest
}

// TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass is the host-independent half of the
// overshoot test below: the same estimator and the same check-schedule arithmetic, over pass
// durations supplied rather than measured, so the property is asserted on a host too fast, too
// slow or too loaded to demonstrate it with a clock.
//
// CI run 32298432254 (ubuntu, cover job, 2a5c31c) failed the overshoot test at its PRECONDITION —
// "half of a measured full sweep must not be enough to finish it" — with the whole test, 1 500
// seeds included, taking 0.70 s. It left no timings behind, because the shipped test logged them
// only after the assertion it failed on. What it does establish is the shape: the judged pass
// swept the whole tree inside a budget of half the one calibration sample, so that sample was
// worth more than twice the sweep it priced. This test fixes that shape in numbers, asserts the
// shipped rule fails on it, and asserts the estimator and the fixture absorb it — including how
// far wrong the estimate may be in EACH direction, which is what the fixture size buys.
func TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass(t *testing.T) {
	require.Zero(t, gcOvershootSeeds%gcCheckEvery,
		"the fixture must be a whole number of check intervals, or its tail is swept unchecked; "+
			"see gcOvershootSeeds")

	// A quiet fast host's sweep, and one sample a transient stretched past twice it. The outlier
	// is first because that is where the shipped rule read its only sample.
	const sweep = 15 * time.Millisecond
	const inflated = 40 * time.Millisecond
	passes := []GCReport{{Duration: inflated, ScannedObjects: gcOvershootSeeds}}
	for i := 0; i < gcCalibrationPasses-1; i++ {
		passes = append(passes, GCReport{
			Duration:       sweep + time.Duration(i)*time.Millisecond,
			ScannedObjects: gcOvershootSeeds,
		})
	}
	require.Len(t, passes, gcCalibrationPasses)

	budget := gcCalibratedSweep(passes).Duration / 2

	// checks returns where a judged sweep costing d consults its deadline: the earliest point a
	// budget may expire at without measuring the mark phase instead of check granularity, and the
	// latest point it may expire at and still truncate anything. sweep consults the deadline after
	// every gcCheckEvery objects, so those are the (gcCheckEvery-1)th and the last object.
	checks := func(d time.Duration) (first, last time.Duration) {
		return d * (gcCheckEvery - 1) / gcOvershootSeeds, d * (gcOvershootSeeds - 1) / gcOvershootSeeds
	}

	_, shippedLast := checks(sweep)
	require.Greater(t, passes[0].Duration/2, shippedLast,
		"premise: half the transient-inflated sample outlasts the whole judged sweep, which is the "+
			"shape CI run 32298432254 failed on — if this stops holding, the numbers above no "+
			"longer reproduce the defect and this test is asserting nothing")

	// How far the estimate may be wrong in either direction before a bracket fails. The slow side
	// is gcOvershootSeeds/(2*gcCheckEvery) and the fast side is 2x, and both come from the fixture
	// being twelve whole check intervals rather than 1 500 objects.
	for _, tc := range []struct {
		name   string
		judged time.Duration
	}{
		{"judged pass as fast as the fastest calibration", sweep},
		{"judged pass twice as slow", 2 * sweep},
		{"judged pass at the slow bracket", sweep * (gcOvershootSeeds / gcCheckEvery) / 2},
		{"judged pass nearly twice as fast", sweep * 55 / 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, last := checks(tc.judged)
			require.Greater(t, budget, first,
				"the deadline would be gone by the sweep's first check, so the overshoot would "+
					"measure the mark phase")
			require.Less(t, budget, last,
				"the sweep would outlive the deadline's last check, so nothing would truncate and "+
					"the deadline would go unasserted")
		})
	}
}

// TestGC_DeadlineOvershootIsBoundedByTheCheckInterval asserts V2-SP06-20's ±50 ms.
//
// The deadline is CALIBRATED rather than fixed: unbounded dry runs over the same tree are timed
// first and half of the fastest becomes the budget. A fixed short deadline would expire before the
// sweep began, and the overshoot would then measure the mark phase, not the check interval. They
// run dry on purpose for the same reason — GCPolicy.Deadline is consulted only inside sweep, so a
// pass that also tombstones several hundred dead roots spends unbounded time before the first
// check, and folding that in would measure a different thing. (That tombstoning is unbounded by
// the deadline is a real, separate gap; it is recorded rather than papered over here.)
//
// Calibrating from ONE pass is what CI run 32298432254 failed on, at the precondition rather than
// at the bound: the ubuntu cover job priced a budget from a calibration pass that cost more than
// twice what the pass it bounded cost, so the judged sweep finished inside its deadline and
// "half of a measured full sweep must not be enough to finish it" failed on a correct product.
// That is item 25's defect seen from the other side. There, load ROSE between the two passes and
// the judged pass overshot an allowance priced when the host was quicker (33e82b5, which prices
// the allowance from whichever pass ran slower — kept below, and untouched). Here the host was
// quicker for the pass being JUDGED than for the one doing the pricing, and the same single-sample
// estimator under-delivered in the mirror direction. Both are the genus items 18, 22 and 25 name:
// a self-scaling wall-clock check whose scaling under-delivers its documented intent. The budget
// is now priced from the fastest of gcCalibrationPasses samples, which is unbiased by transient
// load, and the fixture is sized so that a mispricing has to be far larger than any ratio measured
// on this fixture before either bracket around the budget can fail.
func TestGC_DeadlineOvershootIsBoundedByTheCheckInterval(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < gcOvershootSeeds; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%04d.ts", i), fmt.Sprintf("overshoot body %d, unique\n", i))
	}
	objects := objectCount(t, tp)
	require.Greater(t, objects, gcCheckEvery*4,
		"fixture sanity: the sweep must cross several deadline checks")
	require.Zero(t, objects%gcCheckEvery,
		"fixture sanity: %d objects is %d whole check intervals plus %d objects, and that remainder "+
			"is swept with NO deadline check at all — see gcOvershootSeeds",
		objects, objects/gcCheckEvery, objects%gcCheckEvery)

	// The fastest of several passes over the identical tree, not the first one: see
	// gcCalibrationPasses. The discarded passes are not waste — the first walk of a freshly
	// written tree is exactly the sample a single-pass calibration is stuck with.
	var passes []GCReport
	var slowest time.Duration
	for i := 0; i < gcCalibrationPasses; i++ {
		pass, perr := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true})
		require.NoError(t, perr)
		require.False(t, pass.Truncated, "the calibration pass must complete")
		require.Equal(t, objects, pass.ScannedObjects,
			"every calibration pass must sweep the whole tree, or it prices a different sweep")
		passes = append(passes, pass)
		if pass.Duration > slowest {
			slowest = pass.Duration
		}
	}
	fastest := gcCalibratedSweep(passes)
	budget := fastest.Duration / 2
	require.Positive(t, budget,
		"a zero budget is not a deadline: GC arms one only for a POSITIVE GCPolicy.Deadline, so a "+
			"calibration too short to measure would leave the sweep below unbounded and truncating "+
			"for no reason — %d objects swept in %v", objects, fastest.Duration)

	start := time.Now()
	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true, Deadline: budget})
	overshoot := time.Since(start) - budget
	require.NoError(t, err)
	require.True(t, rep.Truncated,
		"half of a measured full sweep must not be enough to finish it: the judged pass swept all "+
			"%d objects inside a %v budget, so it ran more than twice as fast as the fastest of %d "+
			"calibration passes (%v…%v) over the same tree moments earlier",
		objects, budget, gcCalibrationPasses, fastest.Duration, slowest)
	require.Greater(t, rep.ScannedObjects, gcCheckEvery,
		"calibration check: the deadline must expire INSIDE the sweep, or this measures the mark "+
			"phase; the %v budget was gone by the sweep's first check, so the judged pass ran more "+
			"than %dx slower than the fastest of %d calibration passes (%v…%v)",
		budget, objects/(2*gcCheckEvery), gcCalibrationPasses, fastest.Duration, slowest)

	// One check interval, priced on THIS host — from BOTH passes, and the slower rate wins. The
	// limit is the §2.6 row's 50 ms, or two intervals when a single interval is already close to
	// it — see gcOvershootCeiling.
	//
	// Calibration alone under-prices the interval whenever load rises between the two passes,
	// and that is not hypothetical: V2-VERIFY's final whole-tree cover run measured the
	// calibration pass at 41.5 ms/interval and the truncating pass — minutes of co-scheduled
	// coverage-instrumented packages later — at 68.8 ms/interval, so a sweep that stopped
	// correctly at the very next check still overshot the stale 83.0 ms limit by 1.5 ms. Pricing
	// the interval from the judged pass too keeps the assertion about check GRANULARITY (the
	// §2.6 row's actual subject) rather than about scheduler weather between two measurements;
	// the fixed gcOvershootCeiling above still caps the total relaxation, so a genuinely
	// too-coarse gcCheckEvery fails regardless of which pass priced it.
	//
	// The calibration side of that maximum is now the FASTEST calibration pass rather than the
	// first one, which can only lower it: the allowance is at most what it was before this test
	// grew its extra calibration passes, never more. Item 25's protection is the judged-pass term,
	// and that is untouched — a pass slowed by co-load still prices its own interval and still
	// wins the maximum.
	interval := fastest.Duration / time.Duration(fastest.ScannedObjects) * gcCheckEvery
	if measured := (budget + overshoot) / time.Duration(rep.ScannedObjects) * gcCheckEvery; measured > interval {
		interval = measured
	}
	limit := gcDeadlineOvershoot
	if twoIntervals := 2 * interval; twoIntervals > limit {
		limit = twoIntervals
	}
	t.Logf("calibration %v…%v over %d objects (fastest of %d); check interval %v; budget %v; "+
		"overshoot %v after %d objects; limit %v",
		fastest.Duration, slowest, fastest.ScannedObjects, gcCalibrationPasses, interval, budget,
		overshoot, rep.ScannedObjects, limit)

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

// TestGC_TombstoneRetiresOnlyMarkTimeDead pins the snapshot semantics V2-VERIFY's §4.7 authoring
// found missing: the tombstone phase may retire only roots that were dead AT MARK TIME. Before
// the fix, tombstoneDeadRoots re-read s.rootIndex and computed dead = index − live itself, so a
// root published between mark's snapshot and the tombstone phase — in the index, not in the live
// set — was retired seconds after it was written. mark now fixes the dead set under the same read
// lock as the live set, and this test drives exactly that interleaving: mark, then a write in the
// window, then tombstone.
func TestGC_TombstoneRetiresOnlyMarkTimeDead(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	oldRoot := gcSeed(t, tp, "src/old.txt", "content the pass legitimately retires")

	// Force-collect retention: at the snapshot, nothing is live and the old root is dead.
	_, liveRoots, dead := tp.Store.mark(-1, -1)
	require.Empty(t, liveRoots, "force-collect must find no live roots")
	require.Contains(t, dead, oldRoot.Hash, "the pre-existing root must be dead at the snapshot")

	// The window: a root published after mark's snapshot, before the tombstone phase runs.
	fresh := gcSeed(t, tp, "src/fresh.txt", "content written between mark and tombstone")

	require.NoError(t, tp.Store.tombstoneDeadRoots(ctx, dead))

	_, err := tp.Store.GetRoot(ctx, fresh.Hash)
	require.NoError(t, err, "a root written after mark's snapshot must survive that pass's tombstone phase")
	_, err = tp.Store.GetRoot(ctx, oldRoot.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "the mark-time dead root must still be retired")
}

// TestSweep_SparesObjectsWrittenAfterThePassStarted pins the sweep half of the same window: an
// object written after the pass started cannot be in the live set no matter how live it is,
// because the live set predates it, so the sweep must spare it for the next pass rather than
// delete it. Both mtimes are set explicitly, so the test is exact in both directions: the
// pass-aged object is deleted, the younger-than-the-pass object survives.
func TestSweep_SparesObjectsWrittenAfterThePassStarted(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	gcSeed(t, tp, "src/a.txt", "first object set, written before the pass")
	before := gcObjectPaths(t, tp)
	require.NotEmpty(t, before)

	gcSeed(t, tp, "src/b.txt", "second object set, standing in for a write during the pass")
	all := gcObjectPaths(t, tp)
	young := make([]string, 0, len(all))
	for _, p := range all {
		if !slicesContains(before, p) {
			young = append(young, p)
		}
	}
	require.NotEmpty(t, young, "the second put must add objects of its own")

	started := tp.Clock.Now()
	for _, p := range before {
		require.NoError(t, os.Chtimes(p, started.Add(-time.Hour), started.Add(-time.Hour)))
	}
	for _, p := range young {
		require.NoError(t, os.Chtimes(p, started.Add(time.Hour), started.Add(time.Hour)))
	}

	var rep GCReport
	_, truncated, err := tp.Store.sweep(ctx, sweepArgs{
		live:    map[core.Hash]struct{}{},
		started: started,
		rep:     &rep,
	})
	require.NoError(t, err)
	require.False(t, truncated)

	for _, p := range before {
		_, statErr := os.Stat(p)
		require.True(t, os.IsNotExist(statErr), "pass-aged dead object %s must be deleted", p)
	}
	for _, p := range young {
		_, statErr := os.Stat(p)
		require.NoError(t, statErr, "object younger than the pass %s must be spared", p)
	}
	require.Equal(t, len(before), rep.DeletedObjects, "the report counts only what was actually removed")
}

// gcObjectPaths lists every object file currently on disk, OS-native, sorted.
func gcObjectPaths(t *testing.T, tp *testProject) []string {
	t.Helper()
	var out []string
	base := paths.Long(tp.Store.l.Objects)
	require.NoError(t, filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, p)
		return nil
	}))
	sort.Strings(out)
	return out
}

func slicesContains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

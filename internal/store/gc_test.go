package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
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
// Both roots are ALSO recorded as tool_use entries, which is what production does — a retrieval
// result reaches the store through a recorded tool use like any other. Until the 2026-08-22 audit
// the mark phase read the age clause on that path without consulting Eph, so this test passed only
// because it seeded roots nothing referenced; with the records present, the ephemeral root was
// age-live and the property this test names was void on the path that carries every real one.
func TestGC_EphemeralNotInWindowByAge(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	ordinary := gcSeed(t, tp, "src/ordinary.ts", "an ordinary tool result, distinct content\n")
	ephemeral := gcSeedEphemeral(t, tp, "src/ephemeral.ts", "a retrieved, born-ephemeral result\n")

	now := core.UnixMilli(tp.Clock.Now().UnixMilli())
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "tu-ordinary", Session: "sess-a", Turn: 1, TS: now,
		Tool: "FileRead", Root: ordinary.Hash, Path: "src/ordinary.ts",
	}))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "tu-ephemeral", Session: "sess-a", Turn: 2, TS: now,
		Tool: "recall", Root: ephemeral.Hash, Path: "src/ephemeral.ts",
	}))

	// RetainSessions is off, so the session clause cannot mask the age clause: §8.2 keeps an
	// ephemeral root alive by the session clause or an explicit reference, and this run has neither.
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

	// The budget is priced from the mark phase rather than fixed at a nanosecond, and that is
	// load-bearing since the mark phase became deadline-bounded too (see
	// TestGC_MarkPhaseHonoursTheDeadline). A nanosecond is spent before the pass starts, so the
	// pass truncates in MARK, sweeps nothing and writes no cursor — a correct outcome, and not the
	// one this test is about.
	//
	// What this test needs is a budget that survives the mark phase and is long gone by the
	// sweep's first check. Those two costs differ by construction and in the same direction on
	// every host: the mark walks in-memory indexes and reads three small files, while reaching the
	// sweep's first check is 256 filesystem visits and deletions. So the budget is measured, not
	// guessed — from the CONTROL store, which carries the identical corpus and which mark, being
	// read-only, leaves untouched — and expressed as a multiple of what it measured, so it scales
	// with the host instead of encoding one host's speed.
	markStart := time.Now()
	_, _, _, markTruncated, merr := control.Store.mark(ctx, -1, -1, time.Time{})
	require.NoError(t, merr)
	require.False(t, markTruncated, "an unbounded mark must not truncate")
	budget := 4 * time.Since(markStart)

	first, err := resumed.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, Deadline: budget})
	require.NoError(t, err, "an expired deadline is a normal outcome, never an error")
	require.Positive(t, first.ScannedObjects,
		"this pass must reach the sweep: a mark-phase truncation collects nothing and leaves no "+
			"cursor, so every assertion below would be measuring the wrong phase")
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
// Two things must be true for the priced budget below to land where the measurement needs it: it
// must expire after the sweep's FIRST deadline check, or the overshoot measures the mark phase,
// and before its LAST, or the sweep finishes and never truncates at all. The checks fall at
// objects 256, 512, …, so every usable budget lives in the window between the first check and the
// last, and the RATIO of those two is the whole multiplicative room the measurement has. That
// ratio is at most N/(gcCheckEvery-1) — 12.05x here — on a host holding still, reached only when
// N is a whole number of intervals and the pass spends nothing before its first object.
// gcSweepWindow prices it at run time rather than assuming it, and then floors it. Both ends are
// fastest-of-gcCalibrationPasses samples taken at different moments, and 255 object visits catch a
// quiet gap between two bursts of co-load that 3 072 cannot, so the RAW ratio reads above 12.05x
// under load rather than below it: eighteen runs on linux/amd64 (tmpfs) under heavy bursty co-load
// measured 13.82x…127.81x, median 46.63x, every one of them above the structural ceiling.
// gcSweepWindow.floored puts that right from Full's own arithmetic; with it, thirty runs under
// lighter co-load measured 7.83x…12.05x, median 11.68x, and four quiet runs on windows/amd64
// (NTFS) measured 9.66x…11.10x, where the floor never binds at all. What is left over after the
// floor is a budget re-measured rather than trusted; see gcOvershootAttempts.
//
// 1 500 was neither large enough nor a whole number of intervals: its last 220 objects (14.7 % of
// the sweep) fell past the final check, so the window ran from object 255 to object 1 280 and was
// 5.02x wide, while the window it PRICED ran to 1 500 and read 5.88x — the gap between the two
// being exactly the unchecked tail. Twelve whole intervals put the last check on the last object,
// so the usable window (255 to 3 071) and the priced one (255 to a 3 072-object pass) agree at
// 12.05x, and both widen on EVERY draw even after the estimator change below, which lowers the
// number both ends are multiples of. Eight consecutive unbounded passes over the identical tree,
// three trials on a Windows development host, put the fastest of four at 0.74x, 0.88x and 0.95x of
// the first, so the window priced from the fastest of four sits where a single first sample would
// have put it only in the luckiest draw. Eight intervals would have bought 8.03x, which is why
// this is twelve.
//
// The size is worth its cost only because BOTH ends get used, and neither is hypothetical. The
// fast end is CI run 32298432254 and §16.1 item 11, and it reproduces off CI: five runs of the
// pre-70f91bf test on a Windows host whose sweep had slowed fourfold over a working session failed
// twice, both times on "half of a measured full sweep must not be enough to finish it", while
// nineteen runs of the fastest-of-four rule on that same host, at this fixture size and at 2 048,
// never once reached it. The slow end is the same estimator seen from the other side: two of five
// runs of that same test truncated at the very first check under an I/O co-load storm spanning
// both passes, and CI run 32319171399 did the same on ubuntu. Seeding is linear in the fixture —
// the same per-object cost at 1 500, 1 536, 2 048 and 3 072 seeds, measured, with no penalty for
// the larger tree — so the whole price of the wider window is 1 572 more seeds.
//
// Widening it further is not the answer to a noisy host, and the arithmetic says why: the window
// grows LINEARLY in the fixture (and so in the test's running time), while the tolerance it buys
// grows as its square root. Going from 12x to a 100x window — enough to absorb a 10x mispricing
// from a single sample — would take 25 600 seeds, and 3 072 already puts the whole test at 19–21 s
// on the Windows host measured above, of which the eight calibration passes are 2.2 s and the rest
// is seeding. Absorbing a spread larger than sqrt(12) is gcOvershootAttempts' job.
const gcOvershootSeeds = 12 * gcCheckEvery

// gcCalibrationPasses is how many passes of each kind price the window below, of which the FASTEST
// of each kind wins.
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

// gcOvershootAttempts is how many budgets the overshoot measurement may try before it gives up and
// reports the host as unmeasurable.
//
// Placing the deadline inside the sweep is a PRECONDITION on the measurement setup, not a property
// of the product: it is priced from passes that ran before the pass being judged, and a runner
// whose sweep cost moves several-fold between adjacent passes can move it by more than the whole
// window is wide. CI run 32319171399 priced 62.361718 ms from a fastest-of-four calibration
// spanning 124.723436 ms…411.945468 ms — a 3.3x spread on one host, in one test, seconds apart —
// and the judged pass was slower still, so the budget was gone by the sweep's first check at
// object 256 of 3 072. Reporting that as a broken bound conflates "the product broke its bound"
// with "this host is too noisy to price the bound", which are different findings.
//
// A miss is therefore re-measured rather than reported, and it is not retried blindly: a pass that
// stopped at the first check has MEASURED how long this host now takes to reach that check, and a
// pass that finished has MEASURED a whole sweep, so each miss re-prices the window from its own
// timing (gcSweepWindow.next) and the next attempt is centred on the host as it is now. If the
// host holds still, one miss learns a step change of ANY size exactly and the second attempt
// lands — asserted without a clock in TestGC_DeadlineBudgetRepricesAfterAMissedWindow. Five
// attempts are therefore not five hopeful draws; they are four chances for the host to change its
// mind again by more than sqrt(12.05) = 3.47x between ADJACENT passes, which the 3.3x spread above
// did not do even across four.
//
// Measured on a real linux/amd64 kernel (WSL2 5.15, tmpfs), the test under taskset and GOMAXPROCS
// while a fixed pool of spinners bursts on and off across every core: fifty pairs of runs
// alternating the pre-fix test and this loop under ONE continuous load, so both saw the same
// weather — thirty pairs on three cores against ninety spinners at a 0.3 s/0.5 s duty cycle, then
// twenty on two cores against 250 at 0.4 s/0.4 s. The pre-fix test failed this bracket five times,
// each with CI run 32319171399's message verbatim, and the ±50 ms bound three more; this loop
// failed neither. Nine of the fifty needed a re-price to get there, seven landing on attempt 2 and
// two on attempt 3, and one exhausted all five and said so — which is the whole point: on that
// host, at that moment, the bound could not be priced, and reporting that is not the same as
// reporting a collector that broke it.
//
// Five is not a number one attempt could have been tuned into: the slack those runs measured
// was 2.80x…3.47x, capped there by the floor, against calibration spreads that reached 10.32x.
//
// A miss is never allowed to pass silently, and it is never allowed to be a product defect wearing
// a host's clothes. Every attempt asserts that a truncating pass outlived its budget and that a
// completed one did not outlive its last check; exhausting the attempts fails with every attempt's
// budget, elapsed time and object count.
const gcOvershootAttempts = 5

// gcCalibratedSweep is the Full end of the window below: of the unbounded passes measured over the
// tree, the FASTEST one. See gcCalibrationPasses for why the minimum, and
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

// gcSweepWindow is the pair of measurements that bracket every deadline the overshoot measurement
// can use on this host.
//
// FirstCheck is what a pass costs when it stops at the sweep's FIRST deadline check: the mark
// phase, the live-set write, gcCheckEvery-1 object visits and the cursor write. A budget at or
// below it expires before the sweep has visited anything worth measuring, and the overshoot then
// measures the mark phase rather than check granularity. Full is what an unbounded pass over the
// same tree costs; a budget at or above it never truncates and the deadline goes unasserted.
//
// Both ends are MEASURED rather than derived from the fixture size, because the pass does real
// work before its first object — a mark phase and a paths.WriteAtomic of the live set, which is
// two fsyncs — and that prefix is inside the deadline, as is the cursor write a truncated pass
// ends with. Four quiet runs on windows/amd64 put FirstCheck at 46.2, 48.7, 52.4 and 49.5 ms
// against whole passes of 484.3, 470.8, 529.0 and 549.3 ms — 0.7–2.1 % of a whole pass more than
// the 255/3072 of it those 255 object visits account for. Over tmpfs on linux/amd64 the same
// excess is within noise of zero. It is small on both, but it is not zero and it is not a
// constant, and a budget derived as a fraction of a whole pass spends it before the sweep starts
// without ever naming it.
type gcSweepWindow struct {
	FirstCheck time.Duration
	Full       time.Duration
}

// floored raises a measured FirstCheck to the least it could be at Full's own speed.
//
// The two ends are measured by different passes at different moments, and the SHORT one catches a
// quiet gap far more often than the long one does: 255 object visits fit between two bursts of
// co-load where 3 072 cannot, so the fastest-of-gcCalibrationPasses minimum sits much closer to
// the floor for the short pass than for the long one. Left alone that reads as a WIDER window than
// the check schedule can offer — bursty linux/amd64 runs measured 35x…128x against a structural
// ceiling of 12.05x — and a budget placed at the geometric centre of an over-wide window is placed
// far too early, which is the same first-check miss the retry exists to catch, arriving by a route
// the retry cannot see.
//
// The floor is Full's own arithmetic and needs no assumption about the host. The deadline is
// consulted at objects gcCheckEvery, 2*gcCheckEvery, …, before each is counted, so the first check
// falls after gcCheckEvery-1 of the `objects` visits Full paid for; a pass at Full's speed
// therefore cannot reach it in less than that fraction of Full, and reaches it later still if it
// does anything at all before its first object. Raising the measured end to that floor can only
// NARROW the window, so it can never push a budget past the last check and can never turn a
// truncating pass into a completing one; what it removes is room the check schedule does not have.
func (w gcSweepWindow) floored(objects int) gcSweepWindow {
	// Rounded UP, so that the ratio the floor leaves behind is never a nanosecond wider than the
	// schedule offers — the property TestGC_DeadlineWindowNeverClaimsMoreRoomThanTheCheckSchedule
	// asserts as an inequality rather than as an approximation.
	n := time.Duration(objects)
	if floor := (w.Full*(gcCheckEvery-1) + n - 1) / n; w.FirstCheck < floor {
		w.FirstCheck = floor
	}
	return w
}

// budget is the deadline to arm: the GEOMETRIC mean of the window's two ends.
//
// The arithmetic mean is the wrong centre for this. Both ends move MULTIPLICATIVELY when the host
// changes speed — a pass that is s times slower reaches its first check s times later and finishes
// s times later — so what a mispricing has to be measured against is a ratio, and the quantity to
// maximise is the smaller of budget/FirstCheck and Full/budget. Those meet at sqrt(FirstCheck*Full).
// Half a whole pass, the rule this replaces, sits well off that centre: on a 12.05x window it
// tolerates 6.02x slower but only 2.00x faster, and both ends of that lopsided band have now failed
// CI once each — the fast end in §16.1 item 11, the slow end in run 32319171399.
func (w gcSweepWindow) budget() time.Duration {
	return time.Duration(math.Sqrt(float64(w.FirstCheck) * float64(w.Full)))
}

// slack is how far wrong this window may be, multiplicatively and in EITHER direction, before the
// budget it prices misses the sweep. It is sqrt(Full/FirstCheck) by construction — 3.47x on a
// 12.05x window, which the floor above makes a ceiling rather than a typical value: 2.80x…3.47x
// with a median of 3.42x over the thirty measured runs above.
// CI run 32319171399's four calibration passes alone spanned 3.3x, which is why one attempt at the
// best possible placement is still not enough on its own; see gcOvershootAttempts.
func (w gcSweepWindow) slack() float64 {
	return math.Sqrt(float64(w.Full) / float64(w.FirstCheck))
}

// next returns the window to price the NEXT attempt from, given what an attempt that missed did.
//
// The attempt measured one of this window's two ends directly: a pass that stopped at the sweep's
// first check is a fresh FirstCheck sample, and a pass that finished is a fresh Full sample. The
// other end moves with it, because a host that is s times slower is s times slower on the mark
// phase and on the sweep alike — exactly true for a uniform slowdown, and the loop re-measures
// either way. So if the host holds still, the re-priced budget is not a guess: it is the geometric
// centre of the window the next pass actually has, whatever the size of the step that was missed.
func (w gcSweepWindow) next(elapsed time.Duration, stoppedAtFirstCheck bool) gcSweepWindow {
	was := w.Full
	if stoppedAtFirstCheck {
		was = w.FirstCheck
	}
	f := float64(elapsed) / float64(was)
	return gcSweepWindow{
		FirstCheck: time.Duration(float64(w.FirstCheck) * f),
		Full:       time.Duration(float64(w.Full) * f),
	}
}

// gcSweepModel is the sweep's deadline schedule in closed form, so the claims the window makes can
// be asserted on a host too fast, too slow or too loaded to demonstrate them with a clock.
//
// It mirrors FSStore.sweep (internal/store/gcrun.go): `seen` counts object visits, the deadline is
// consulted when seen%gcCheckEvery == 0 and BEFORE that visit is counted, so a pass truncating at
// the jth check reports j*gcCheckEvery-1 scanned objects, and a pass whose fixture is a whole
// number of intervals has its last check on its last object. Prefix is everything the pass does
// before its first object (mark, live-set write); Save is the cursor write a truncated pass ends
// with, which is outside the sweep and inside exactly ONE of the two clocks this file reads.
//
// GC takes rep.Duration = time.Since(started) at gcrun.go:117 and calls saveGCState only
// afterwards, at :124, so GCReport.Duration -- what the calibration passes below read --
// EXCLUDES Save, while the wall-clock time.Since(start) the attempt loop wraps around the whole
// GC call includes it. run() models the second. A window priced from this model is therefore
// narrower than the one calibration actually measures on the same host: 9.29x against 10.55x
// with the constants below. That error is conservative in the only direction that matters --
// the model claims LESS room than the host has, so a schedule it accepts is one the host also
// accepts -- but it is why the two elapsed times are not interchangeable, and why a future
// change here must say which clock it means.
type gcSweepModel struct {
	Prefix, Rate, Save time.Duration
	Objects            int
}

// run reports what a pass under this model does with the given deadline budget.
//
// A non-positive budget is an UNBOUNDED pass, not an instantly expired one: FSStore.GC arms a
// deadline only for a positive GCPolicy.Deadline, so the model has to say the same thing.
func (m gcSweepModel) run(budget time.Duration) (scanned int, truncated bool, elapsed time.Duration) {
	if budget > 0 {
		for seen := gcCheckEvery; seen <= m.Objects; seen += gcCheckEvery {
			at := m.Prefix + time.Duration(seen-1)*m.Rate
			if at > budget {
				return seen - 1, true, at + m.Save
			}
		}
	}
	return m.Objects, false, m.Prefix + time.Duration(m.Objects)*m.Rate
}

// window is what TestGC_DeadlineOvershootIsBoundedByTheCheckInterval's calibration measures on a
// host behaving like this model: an already-expired deadline for one end, an unbounded pass for
// the other.
func (m gcSweepModel) window() gcSweepWindow {
	_, _, stop := m.run(time.Nanosecond)
	_, _, full := m.run(0)
	return gcSweepWindow{FirstCheck: stop, Full: full}.floored(m.Objects)
}

// scaled returns the same host running f times slower (f > 1) or faster (f < 1).
func (m gcSweepModel) scaled(f float64) gcSweepModel {
	return gcSweepModel{
		Prefix:  time.Duration(float64(m.Prefix) * f),
		Rate:    time.Duration(float64(m.Rate) * f),
		Save:    time.Duration(float64(m.Save) * f),
		Objects: m.Objects,
	}
}

// gcSimulateAttempts runs the attempt loop of TestGC_DeadlineOvershootIsBoundedByTheCheckInterval
// against modelled hosts, returning the 1-based attempt whose deadline landed inside the sweep, or
// 0 if none did. hosts[i] is the host the ith attempt runs on, so a host that keeps changing its
// mind is expressed by varying the slice.
//
// Past the landing test there are only two outcomes left — the pass finished, or it truncated at
// the first check — so `truncated` IS gcSweepWindow.next's stoppedAtFirstCheck there, exactly as
// in the loop this mirrors.
func gcSimulateAttempts(win gcSweepWindow, hosts []gcSweepModel) (landedOn int, scanned []int) {
	for i, h := range hosts {
		got, truncated, elapsed := h.run(win.budget())
		scanned = append(scanned, got)
		if truncated && got > gcCheckEvery {
			return i + 1, scanned
		}
		win = win.next(elapsed, truncated)
	}
	return 0, scanned
}

// gcQuietModel is a host whose numbers reproduce the phase split measured on a Windows development
// host: a whole pass of 15.56 ms over gcOvershootSeeds objects, 1.3 % of it spent before the first
// object and as much again on the cursor write, which puts its window at 9.29x — just under the
// 9.66x…11.10x four quiet runs of the real fixture measured there. It is deliberately a whole pass
// an order of magnitude faster than that host's 470…550 ms: the model has to say the same things
// about placement at any speed, and running it at a speed no real pass here has is one more way of
// checking that it does.
var gcQuietModel = gcSweepModel{
	Prefix:  200 * time.Microsecond,
	Rate:    5 * time.Microsecond,
	Save:    200 * time.Microsecond,
	Objects: gcOvershootSeeds,
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
// single-sample rule fails on it, and asserts the fastest-of-gcCalibrationPasses estimator absorbs
// it — including how far wrong the estimate may then be in EACH direction.
func TestGC_DeadlineBudgetSurvivesATransientlySlowCalibrationPass(t *testing.T) {
	require.Zero(t, gcOvershootSeeds%gcCheckEvery,
		"the fixture must be a whole number of check intervals, or its tail is swept unchecked; "+
			"see gcOvershootSeeds")

	// A quiet fast host, and one calibration sample a transient stretched past twice it. The
	// outlier is first because that is where a single-sample calibration reads its only sample.
	quiet := gcQuietModel
	inflated := quiet.scaled(2.6)
	_, _, quietFull := quiet.run(0)
	_, _, inflatedFull := inflated.run(0)
	passes := []GCReport{{Duration: inflatedFull, ScannedObjects: gcOvershootSeeds}}
	for i := 0; i < gcCalibrationPasses-1; i++ {
		passes = append(passes, GCReport{
			Duration:       quietFull + time.Duration(i)*time.Millisecond,
			ScannedObjects: gcOvershootSeeds,
		})
	}
	require.Len(t, passes, gcCalibrationPasses)
	require.Equal(t, quietFull, gcCalibratedSweep(passes).Duration,
		"the estimator must discard the transient and keep the fastest sample")

	// Premise: the single sample the pre-70f91bf rule priced from outlasts the whole judged sweep,
	// which is the shape CI run 32298432254 failed on. If this stops holding, the numbers above no
	// longer reproduce the defect and this test is asserting nothing.
	_, singleTruncated, _ := quiet.run(passes[0].Duration / 2)
	require.False(t, singleTruncated,
		"premise: half the transient-inflated sample (%v) must be enough to sweep a quiet pass "+
			"(%v) to the end, so the judged pass would not truncate at all",
		passes[0].Duration/2, quietFull)

	// The estimator absorbs it: priced from the fastest sample and placed at the window's
	// geometric centre, the deadline lands inside the sweep for every judged pass within slack of
	// the calibrated host — and the slack is symmetric, which half a whole pass never was.
	win := quiet.window()
	require.Equal(t, quietFull, win.Full, "the window's Full end is the fastest calibration pass")
	require.InDelta(t, math.Sqrt(float64(win.Full)/float64(win.FirstCheck)), win.slack(), 0.001)

	// The PLACEMENT earns its keep on its own, before any retry. Half a whole pass has 2.00x of
	// fast-side room on a 12.05x window and less once the pass spends anything before its first
	// object, so a judged pass a little over twice as fast as the calibration finishes inside it
	// and truncates nothing — §16.1 item 11's failure. The geometric centre still lands on it,
	// because it trades the slow side's surplus for the fast side's shortfall.
	brisk := quiet.scaled(0.45)
	_, halfTruncated, _ := brisk.run(quietFull / 2)
	require.False(t, halfTruncated,
		"premise: half a whole pass (%v) is enough for a 2.2x-faster sweep to reach its last check, "+
			"so that rule asserts nothing here", quietFull/2)
	briskScanned, briskTruncated, _ := brisk.run(win.budget())
	require.True(t, briskTruncated,
		"the window's centre (%v) must still truncate a 2.2x-faster sweep", win.budget())
	require.Greater(t, briskScanned, gcCheckEvery,
		"and must not be gone by its first check either")

	for _, tc := range []struct {
		name  string
		scale float64
	}{
		{"judged pass as fast as the fastest calibration", 1},
		{"judged pass twice as slow", 2},
		{"judged pass at the slow edge of the slack", win.slack() * 0.99},
		{"judged pass at the fast edge of the slack", 1 / (win.slack() * 0.99)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanned, truncated, _ := quiet.scaled(tc.scale).run(win.budget())
			require.True(t, truncated,
				"the sweep would outlive the deadline's last check, so nothing would truncate and "+
					"the deadline would go unasserted")
			require.Greater(t, scanned, gcCheckEvery,
				"the deadline would be gone by the sweep's first check, so the overshoot would "+
					"measure the mark phase")
		})
	}
}

// TestGC_DeadlineWindowNeverClaimsMoreRoomThanTheCheckSchedule is the host-independent half of
// gcSweepWindow.floored: the check schedule caps how far apart the window's two ends can be, and a
// measurement that reads wider than the cap is the short calibration pass's luck, not the host's.
//
// It is the one direction the retry below cannot rescue on its own. A budget priced from an
// over-wide window is placed too EARLY, so the first attempt truncates at the sweep's first check;
// the retry then re-prices from that pass and lands, but it has spent an attempt on a mispricing
// arithmetic could have removed before the first pass ran. Under bursty co-load on linux/amd64 the
// effect was not marginal: 255 object visits fit between two bursts where 3 072 could not, and all
// eighteen windows measured there read wider than the 12.05x this fixture's check schedule can
// offer — 13.82x…127.81x, median 46.63x. Twenty pairs of runs at that load with the floor in
// place fixed the ±50 ms bound as well as this bracket: without it five of twenty overshot a
// limit priced from a judged pass only three intervals long, with it none did.
func TestGC_DeadlineWindowNeverClaimsMoreRoomThanTheCheckSchedule(t *testing.T) {
	// The cap, from the schedule alone: the deadline is consulted at gcCheckEvery, 2*gcCheckEvery,
	// … and BEFORE that object is counted, so the first check falls after gcCheckEvery-1 of the
	// fixture's objects, and — the fixture being a whole number of intervals — the last falls on
	// its last one.
	const schedule = float64(gcOvershootSeeds) / float64(gcCheckEvery-1)
	require.InDelta(t, 12.05, schedule, 0.01,
		"the fixture's check schedule offers 12.05x; see gcOvershootSeeds")

	// A window measured on a host holding still is already inside it, and the floor must leave it
	// exactly alone — including the prefix it measured, which is the whole reason both ends are
	// measured rather than derived.
	quiet := gcQuietModel.window()
	require.Less(t, float64(quiet.Full)/float64(quiet.FirstCheck), schedule,
		"premise: a quiet host's own window is inside what the schedule offers")
	require.Equal(t, quiet, quiet.floored(gcOvershootSeeds),
		"a window the check schedule can offer must survive the floor untouched")

	// The bursty-host shape, at the widest of the five measured above: the SHORT pass caught a
	// quiet gap and the long one did not.
	lucky := gcSweepWindow{FirstCheck: quiet.Full * 100 / 12781, Full: quiet.Full}
	require.Greater(t, float64(lucky.Full)/float64(lucky.FirstCheck), schedule,
		"premise: the measurement must read wider than the schedule offers, or there is nothing to floor")
	floored := lucky.floored(gcOvershootSeeds)
	require.LessOrEqual(t, float64(floored.Full)/float64(floored.FirstCheck), schedule,
		"the floored window must be inside what the schedule offers")
	require.Equal(t, lucky.Full, floored.Full,
		"the floor moves the measured end only: a whole pass is a whole pass")
	require.Greater(t, floored.FirstCheck, lucky.FirstCheck,
		"and it moves that end UP, so the window can only narrow")
	require.Less(t, floored.budget(), floored.Full,
		"a narrower window cannot push the budget past the last check")

	// And it is not cosmetic. The over-wide window's centre is gone before the judged pass reaches
	// its first check — the miss the retry exists for, arriving before the retry can see it —
	// while the floored window's centre lands inside the sweep on the FIRST attempt.
	scanned, truncated, _ := gcQuietModel.run(lucky.budget())
	require.True(t, truncated, "premise: the unfloored budget (%v) must still truncate", lucky.budget())
	require.LessOrEqual(t, scanned, gcCheckEvery,
		"premise: the unfloored budget must be gone by the sweep's first check, or this test is "+
			"asserting nothing; it scanned %d", scanned)
	scanned, truncated, _ = gcQuietModel.run(floored.budget())
	require.True(t, truncated, "the floored budget (%v) must still truncate", floored.budget())
	require.Greater(t, scanned, gcCheckEvery,
		"the floored budget must land INSIDE the sweep on the first attempt, not at its first check")

	// The floor is a lower bound on the truth, never an estimate of it: it may raise a measured end
	// and never lower one, so no reading of any size can come out of it wider than it went in.
	for _, first := range []time.Duration{
		time.Nanosecond, quiet.Full / 1000, quiet.FirstCheck, quiet.Full / 3, quiet.Full,
	} {
		w := gcSweepWindow{FirstCheck: first, Full: quiet.Full}
		got := w.floored(gcOvershootSeeds)
		require.GreaterOrEqual(t, got.FirstCheck, w.FirstCheck,
			"the floor may raise the measured end, never lower it")
		require.LessOrEqual(t, got.slack(), w.slack(),
			"so the slack it reports can only shrink — a floor that widened a window would be "+
				"claiming room the check schedule does not have")
	}
}

// TestGC_DeadlineBudgetRepricesAfterAMissedWindow is the host-independent half of the RETRY: it
// fixes CI run 32319171399's numbers in a model, shows that no single-attempt placement of the
// budget survives them, and shows that one re-priced attempt does.
//
// The run failed the overshoot test's other bracket — "the deadline must expire INSIDE the sweep"
// — with a fastest-of-four calibration spanning 124.723436 ms…411.945468 ms over 3 072 objects and
// a judged pass slower than all four. That is not a bound the product broke; it is a budget priced
// on a host that then changed speed, and the difference is what this test pins down.
func TestGC_DeadlineBudgetRepricesAfterAMissedWindow(t *testing.T) {
	// CI run 32319171399's fastest calibration pass, over the same gcOvershootSeeds objects. How
	// that pass split between the mark phase, the sweep and the cursor write is not in the CI
	// output, so the split is gcQuietModel's — measured on a real host — scaled up to it.
	const ciFastestPass = 124723436 * time.Nanosecond
	_, _, quietFull := gcQuietModel.run(0)
	ci := gcQuietModel.scaled(float64(ciFastestPass) / float64(quietFull))
	win := ci.window()
	_, _, full := ci.run(0)
	require.InDelta(t, float64(ciFastestPass), float64(full), float64(time.Millisecond),
		"premise: the modelled host must cost what CI's fastest calibration pass cost")

	// The judged pass was more than 6x slower — that is what the shipped message computed from
	// gcOvershootSeeds/(2*gcCheckEvery) and reported as fact. Both the rule that failed and the
	// best possible single-attempt placement miss it, and saying so is the point: no placement of
	// one budget survives a host that moves this far, so the fix cannot be a better placement.
	judged := ci.scaled(6.1)
	halfAPass, _, _ := judged.run(full / 2)
	require.Equal(t, gcCheckEvery-1, halfAPass,
		"premise: half a calibrated pass is gone by the judged sweep's first check, which is the "+
			"255-of-3072 CI run 32319171399 reported")
	centred, _, _ := judged.run(win.budget())
	require.Equal(t, gcCheckEvery-1, centred,
		"premise: the geometric centre of the window misses it too — 6.1x is past the %.2fx slack "+
			"a %.2fx-wide window can offer, so this is not a placement problem",
		win.slack(), float64(win.Full)/float64(win.FirstCheck))

	// One re-priced attempt lands, and it lands for a step change of any size in either
	// direction: the missing pass measured the end of the window it fell off, so the next budget
	// is the geometric centre of the window the host actually has.
	for _, scale := range []float64{0.02, 0.1, 0.5, 0.9, 1, 1.1, 2, 6.1, 25, 100} {
		hosts := make([]gcSweepModel, gcOvershootAttempts)
		for i := range hosts {
			hosts[i] = ci.scaled(scale)
		}
		landedOn, scanned := gcSimulateAttempts(win, hosts)
		require.True(t, landedOn == 1 || landedOn == 2,
			"a host %vx off the calibration must be measured, and by the second attempt at the "+
				"latest, because the first attempt's own timing prices the second: landed on "+
				"attempt %d of %d, scanned %v", scale, landedOn, gcOvershootAttempts, scanned)
	}

	// What defeats the loop is the host changing its mind by more than the slack between ADJACENT
	// attempts, every time — so the give-up path is reachable, and it is a statement about the
	// host rather than about the deadline. Each attempt here alternates far past the slack in the
	// direction the previous miss just corrected for.
	alternating := make([]gcSweepModel, gcOvershootAttempts)
	for i := range alternating {
		if i%2 == 0 {
			alternating[i] = ci.scaled(math.Pow(win.slack(), 2))
		} else {
			alternating[i] = ci.scaled(1 / math.Pow(win.slack(), 2))
		}
	}
	landedOn, scanned := gcSimulateAttempts(win, alternating)
	require.Zero(t, landedOn,
		"a host that swings by slack² between every adjacent pass must exhaust the attempts, so "+
			"that the loop cannot be mistaken for an unconditional pass; scanned %v", scanned)
}

// TestGC_DeadlineOvershootIsBoundedByTheCheckInterval asserts V2-SP06-20's ±50 ms.
//
// The deadline is CALIBRATED rather than fixed: passes over the same tree are timed first and the
// budget is placed inside the window they measure. A fixed short deadline would expire before the
// sweep began, and the overshoot would then measure the mark phase, not the check interval. They
// run dry on purpose for the same reason — GCPolicy.Deadline is consulted only inside sweep, so a
// pass that also tombstones several hundred dead roots spends unbounded time before the first
// check, and folding that in would measure a different thing. (That tombstoning is unbounded by
// the deadline is a real, separate gap; it is recorded rather than papered over here.)
//
// Three runs have now failed this measurement for a reason that was not the collector's, once in
// each of the three ways it can be got wrong, and each fix is still standing:
//
//   - Run 32298432254 priced the budget from ONE calibration pass that cost more than twice the
//     pass it bounded, so the judged sweep finished inside its deadline. Fixed by pricing from the
//     fastest of gcCalibrationPasses samples (70f91bf), which is unbiased by transient load.
//   - The V2-VERIFY cover run (§0 item 25) overshot an allowance priced when the host was quicker.
//     Fixed by pricing the check interval from whichever pass ran slower (33e82b5) — kept below,
//     and untouched: the judged-pass term is what protects it and it still wins the maximum.
//   - Run 32319171399 priced 62.361718 ms from a calibration spanning 124.723436 ms…411.945468 ms
//     and the judged pass was slower than all four, so the budget was gone by the sweep's first
//     check. No placement of a single budget survives that (see
//     TestGC_DeadlineBudgetRepricesAfterAMissedWindow), because the spread is wider than the
//     window; the budget is now placed at the window's geometric centre, floored so the window
//     cannot claim room the check schedule does not have (gcSweepWindow.floored), and RE-PRICED
//     from a missing pass's own timing, up to gcOvershootAttempts times.
//
// All three are the genus items 18, 22 and 25 of plans/V2-report.md §0 name: a self-scaling
// wall-clock check whose scaling under-delivers its documented intent. Nothing asserted about the
// PRODUCT is relaxed to buy that, and every attempt asserts it, landing or not: a truncated sweep
// stops exactly ON a deadline check and only after outliving its budget, a sweep that did not
// truncate swept the whole tree and did not outlive its last check, and the landing attempt's
// overshoot is bounded by one check interval exactly as before. The first of those is new here,
// and it is what keeps a retry from being a second chance for the collector rather than for the
// host: a deadline that fires before it is due fails on the attempt that did it, whatever the
// attempt count.
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

	// Every pass below must sweep the WHOLE tree. A truncated pass leaves a cursor, the live set
	// never changes here so its digest always matches, and the next pass would therefore resume
	// from it — sweeping only the tail and seeding its ScannedObjects from the previous pass's
	// counters, which are the two numbers every assertion below reads.
	cursor := filepath.Join(paths.Of(tp.Root).State, gcStateFile)
	dropCursor := func() {
		t.Helper()
		if err := os.Remove(paths.Long(cursor)); err != nil {
			require.ErrorIs(t, err, fs.ErrNotExist,
				"the cursor must be gone before the next pass, or that pass resumes instead of sweeping")
		}
		require.NoFileExists(t, cursor)
	}

	// Calibration prices BOTH ends of the window, interleaved so the two come from the same
	// stretch of host weather, and the FASTEST of each kind wins (see gcCalibrationPasses). The
	// discarded passes are not waste — the first walk of a freshly written tree is exactly the
	// sample a single-pass calibration is stuck with.
	var passes []GCReport
	var slowest, firstCheck time.Duration
	for i := 0; i < gcCalibrationPasses; i++ {
		dropCursor()
		full, ferr := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, DryRun: true})
		require.NoError(t, ferr)
		require.False(t, full.Truncated, "the calibration pass must complete")
		require.Equal(t, objects, full.ScannedObjects,
			"every calibration pass must sweep the whole tree, or it prices a different sweep")
		passes = append(passes, full)
		if full.Duration > slowest {
			slowest = full.Duration
		}

		dropCursor()
		// This end of the window is "a pass that reaches the sweep and stops at its FIRST check",
		// which is Prefix + one check interval in gcSweepModel's terms. It is measured by running
		// the two phases GC runs, in the order GC runs them, rather than by handing GC an expired
		// deadline: since the mark phase became deadline-bounded too — it streams every checkpoint,
		// pin and elimination file and was previously unbounded, which the sweep's own comment
		// denied — an already-expired GCPolicy.Deadline correctly stops the pass in mark with
		// nothing swept at all (TestGC_MarkPhaseHonoursTheDeadline pins that), which prices the
		// wrong phase for this window.
		stopStart := time.Now()
		liveChunks, _, _, markTruncated, merr := tp.Store.mark(ctx, -1, -1, time.Time{})
		require.NoError(t, merr)
		require.False(t, markTruncated, "an unbounded mark must not truncate")
		require.NoError(t, tp.Store.writeLiveSet(liveChunks))
		var stopRep GCReport
		_, stopTruncated, serr := tp.Store.sweep(ctx, sweepArgs{
			live:     liveChunks,
			dryRun:   true,
			deadline: time.Now().Add(-time.Nanosecond),
			started:  stopStart,
			rep:      &stopRep,
		})
		stopDuration := time.Since(stopStart)
		require.NoError(t, serr, "an expired deadline is a normal outcome, never an error")
		require.True(t, stopTruncated,
			"an already-expired deadline, over %d objects, must truncate", objects)
		require.Equal(t, gcCheckEvery-1, stopRep.ScannedObjects,
			"an already-expired deadline must stop the sweep at its FIRST check: the deadline is "+
				"consulted once every %d objects and before that object is counted, so this pass "+
				"can only ever report %d", gcCheckEvery, gcCheckEvery-1)
		if firstCheck == 0 || stopDuration < firstCheck {
			firstCheck = stopDuration
		}
	}
	fastest := gcCalibratedSweep(passes)
	// .floored keeps the window inside what the check schedule can actually offer: the two ends
	// are measured by different passes, and the short one is far likelier to have caught a quiet
	// gap. See gcSweepWindow.floored.
	cal := gcSweepWindow{FirstCheck: firstCheck, Full: fastest.Duration}.floored(objects)
	require.Positive(t, cal.FirstCheck,
		"a pass that stops at the sweep's first check must be measurable at all; %d objects, "+
			"whole pass %v", objects, cal.Full)
	require.Greater(t, cal.Full, 2*cal.FirstCheck,
		"host sanity: a whole pass costs %v and merely reaching the sweep's FIRST check costs %v, "+
			"so there is no room either side of any budget that could expire inside the sweep. On "+
			"this host the pass spends most of its time somewhere other than visiting objects — "+
			"the mark phase and the live-set write before the sweep, the cursor write after it, "+
			"two fsyncs each — and that is a statement about the host, not about the collector; "+
			"see gcSweepWindow", cal.Full, cal.FirstCheck)

	// limitFor prices one check interval on THIS host — from BOTH passes, and the slower rate
	// wins. The limit is the §2.6 row's 50 ms, or two intervals when a single interval is already
	// close to it — see gcOvershootCeiling.
	//
	// Calibration alone under-prices the interval whenever load rises between the two passes, and
	// that is not hypothetical: V2-VERIFY's final whole-tree cover run measured the calibration
	// pass at 41.5 ms/interval and the truncating pass — minutes of co-scheduled
	// coverage-instrumented packages later — at 68.8 ms/interval, so a sweep that stopped
	// correctly at the very next check still overshot the stale 83.0 ms limit by 1.5 ms. Pricing
	// the interval from the judged pass too keeps the assertion about check GRANULARITY (the §2.6
	// row's actual subject) rather than about scheduler weather between two measurements; the
	// fixed gcOvershootCeiling still caps the total relaxation, so a genuinely too-coarse
	// gcCheckEvery fails regardless of which pass priced it.
	//
	// The calibration side of that maximum is the FASTEST calibration pass rather than the first
	// one, which can only lower it. Item 25's protection is the judged-pass term, and that is
	// untouched — a pass slowed by co-load still prices its own interval and still wins the
	// maximum.
	limitFor := func(budget, overshoot time.Duration, scanned int) (interval, limit time.Duration) {
		interval = fastest.Duration / time.Duration(fastest.ScannedObjects) * gcCheckEvery
		if measured := (budget + overshoot) / time.Duration(scanned) * gcCheckEvery; measured > interval {
			interval = measured
		}
		limit = gcDeadlineOvershoot
		if two := 2 * interval; two > limit {
			limit = two
		}
		return interval, limit
	}

	type attempt struct {
		budget, elapsed time.Duration
		scanned         int
		truncated       bool
	}
	var tries []attempt
	var judged GCReport
	var judgedBudget, judgedOvershoot time.Duration
	landed := false
	win := cal
	for len(tries) < gcOvershootAttempts && !landed {
		budget := win.budget()
		require.Positive(t, budget,
			"a zero budget is not a deadline: GC arms one only for a POSITIVE GCPolicy.Deadline, so "+
				"a calibration too short to measure would leave the sweep below unbounded and "+
				"truncating for no reason — %d objects swept in %v", objects, fastest.Duration)

		dropCursor()
		start := time.Now()
		rep, gerr := tp.Store.GC(ctx, GCPolicy{
			RetainDays: -1, RetainSessions: -1, DryRun: true, Deadline: budget,
		})
		elapsed := time.Since(start)
		require.NoError(t, gerr)
		tries = append(tries, attempt{
			budget: budget, elapsed: elapsed, scanned: rep.ScannedObjects, truncated: rep.Truncated,
		})

		if !rep.Truncated {
			// The pass swept the whole tree, so no check ever saw an expired deadline. That is a
			// correct outcome for a budget priced too generously — and the one shape it must
			// never hide is a deadline that is not consulted at all, which returns a whole sweep
			// late rather than one object late. The fixture is a whole number of intervals, so
			// the last check falls ON the last object and nothing but that object and the walk's
			// teardown separates it from the return.
			require.Equal(t, objects, rep.ScannedObjects,
				"a pass that did not truncate must have swept the whole tree")
			_, limit := limitFor(budget, elapsed-budget, rep.ScannedObjects)
			require.LessOrEqual(t, elapsed-budget, limit,
				"V2-SP06-20: this pass swept all %d objects instead of truncating, which is correct "+
					"only if it never saw its %v deadline expire — its last check falls on its last "+
					"object, so it may return at most that object late, not %v late",
				objects, budget, elapsed-budget)
			win = win.next(elapsed, rep.Truncated)
			continue
		}

		if rep.ScannedObjects == 0 {
			// The budget was gone before the sweep ran at all, so the mark phase truncated the
			// pass and nothing was swept or collected. That is a correct outcome and a mispriced
			// setup, not a broken bound — the same case as the first-check one below, one phase
			// earlier — so it is re-priced from what this pass measured and retried.
			win = win.next(elapsed, rep.Truncated)
			continue
		}

		require.Zero(t, (rep.ScannedObjects+1)%gcCheckEvery,
			"a truncated sweep must stop exactly ON a deadline check: the sweep consults the "+
				"deadline once every %d objects and before that object is counted, so it can only "+
				"ever report a multiple of %d minus one; it reported %d",
			gcCheckEvery, gcCheckEvery, rep.ScannedObjects)

		// A truncating pass must have OUTLIVED its budget, and that holds on any host at any
		// speed: GC arms the deadline at started+Deadline with a `started` taken after the timer
		// above, sweep truncates only once time.Now() is already past it, and `elapsed` brackets
		// both, so elapsed > budget is arithmetic rather than weather.
		//
		// It is what tells a mispriced setup apart from a deadline that fires early, and the
		// attempt loop must never confuse the two: a pass that stopped at the first check because
		// the host slowed down ran LONGER than its budget, while one that stopped there because
		// the deadline was armed wrong, compared against the wrong clock, or consulted before the
		// pass began ran SHORTER. Only the first is re-priced and retried below; the second fails
		// here, on the attempt that did it, whether that attempt landed inside the sweep or not.
		require.Greater(t, elapsed, budget,
			"a sweep truncated on a deadline it had not yet reached: %d of %d objects in %v, "+
				"against a %v budget armed at the start of the same call. The sweep truncates only "+
				"once time.Now() is past started+Deadline, and this call returned before that "+
				"instant, so the deadline it stopped on was not the one it was given — that is the "+
				"collector, not the host",
			rep.ScannedObjects, objects, elapsed, budget)

		if rep.ScannedObjects <= gcCheckEvery {
			// The budget was gone by the sweep's first check, so the overshoot would measure the
			// mark phase. That is a mispriced setup, not a broken bound: re-price from what this
			// pass just measured and try again (see gcOvershootAttempts).
			win = win.next(elapsed, rep.Truncated)
			continue
		}

		judged, judgedBudget, judgedOvershoot = rep, budget, elapsed-budget
		landed = true
	}

	var trail strings.Builder
	truncatedSomething := false
	for i, a := range tries {
		truncatedSomething = truncatedSomething || a.truncated
		fmt.Fprintf(&trail, "\n\tattempt %d: budget %v → %v elapsed, %d/%d objects, truncated %v",
			i+1, a.budget, a.elapsed, a.scanned, objects, a.truncated)
	}

	// A host too noisy to price and a deadline that never fires both end up here, and they are
	// told apart without a clock. Every completed attempt re-prices the budget DOWN by exactly the
	// factor by which the pass beat it, so against a collector that honours its deadline the
	// second attempt truncates whatever the host's speed — see
	// TestGC_DeadlineBudgetRepricesAfterAMissedWindow. Not one truncation in gcOvershootAttempts
	// budgets is therefore not weather.
	require.True(t, truncatedSomething || landed,
		"no attempt truncated at all, at budgets re-priced down from %v across %d tries: a "+
			"mispriced budget misses in different directions as it is re-priced, but a deadline "+
			"that is never consulted sweeps to the end however small the budget gets, and this is "+
			"the second shape:%s", cal.budget(), gcOvershootAttempts, trail.String())

	require.True(t, landed,
		"no attempt placed the deadline inside the sweep, so this host could not be measured — a "+
			"finding about the HOST, not about the deadline, and the two are not the same. "+
			"Calibration: whole pass %v…%v (fastest of %d), a %.2fx spread; reaching the sweep's "+
			"first check %v, measured %v; so the window is %.2fx wide and one attempt tolerates "+
			"being %.2fx off in either direction. Every attempt re-priced from the previous "+
			"attempt's own timing, so defeating all %d takes a sweep cost that moves further than "+
			"that between ADJACENT passes, every time:%s",
		fastest.Duration, slowest, gcCalibrationPasses, float64(slowest)/float64(fastest.Duration),
		cal.FirstCheck, firstCheck,
		float64(cal.Full)/float64(cal.FirstCheck), cal.slack(), gcOvershootAttempts, trail.String())

	interval, limit := limitFor(judgedBudget, judgedOvershoot, judged.ScannedObjects)
	t.Logf("calibration %v…%v over %d objects (fastest of %d); first check %v (measured %v); "+
		"window %.2fx, slack %.2fx; check interval %v; budget %v; overshoot %v after %d objects on "+
		"attempt %d of %d; limit %v",
		fastest.Duration, slowest, fastest.ScannedObjects, gcCalibrationPasses, cal.FirstCheck,
		firstCheck, float64(cal.Full)/float64(cal.FirstCheck), cal.slack(), interval, judgedBudget,
		judgedOvershoot, judged.ScannedObjects, len(tries), gcOvershootAttempts, limit)

	require.LessOrEqual(t, limit, gcOvershootCeiling,
		"two deadline checks cost %v on this host, so gcCheckEvery (%d) is too coarse to honour a "+
			"deadline inside the 2 s idle budget; revisit the constant, not this assertion", 2*interval, gcCheckEvery)
	require.LessOrEqual(t, judgedOvershoot, limit,
		"V2-SP06-20: a truncating sweep must return within %v of its deadline; it ran %v over after "+
			"scanning %d objects at a check interval of %d (%v)",
		limit, judgedOvershoot, judged.ScannedObjects, gcCheckEvery, interval)
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
	_, liveRoots, dead, truncated, err := tp.Store.mark(ctx, -1, -1, time.Time{})
	require.NoError(t, err)
	require.False(t, truncated, "an unbounded mark must not report truncation")
	require.Empty(t, liveRoots, "force-collect must find no live roots")
	require.Contains(t, dead, oldRoot.Hash, "the pre-existing root must be dead at the snapshot")

	// The window: a root published after mark's snapshot, before the tombstone phase runs.
	fresh := gcSeed(t, tp, "src/fresh.txt", "content written between mark and tombstone")

	require.NoError(t, tp.Store.tombstoneDeadRoots(ctx, dead))

	_, err = tp.Store.GetRoot(ctx, fresh.Hash)
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

// TestGC_MarkPhaseHonoursTheDeadline pins the bound the mark phase did not have until the
// 2026-08-22 audit, and the sweep's own comment claimed it did ("both this loop and the mark phase
// check the deadline and the ctx").
//
// The phase streams every checkpoint, pin and elimination file token by token and walks every index
// in memory; its cost grows with the project's whole history, and an idle task that granted it 2 s
// had no way to get out of it. An already-expired deadline must therefore stop the pass IN mark —
// with nothing tombstoned, nothing swept, and no cursor left behind, because a live set the phase
// never finished computing is one that would look mostly dead to a sweep.
func TestGC_MarkPhaseHonoursTheDeadline(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	// Enough roots that the mark phase crosses several of its own checks.
	for i := 0; i < gcCheckEvery*3; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/m%04d.ts", i), fmt.Sprintf("mark budget body %d, unique\n", i))
	}
	before := objectCount(t, tp)

	// The phase bound itself, asserted directly, because it is the only way to state it without a
	// race: GCPolicy.Deadline is a duration from the pass's start, so the shortest expiry the
	// public API can express is one nanosecond, and whether that has elapsed by the mark's first
	// check — 256 items in, microseconds of map iteration — depends on the host's clock
	// granularity rather than on the collector. Measured on a Windows host: mark truncates on
	// three runs in five and completes within one tick on the other two.
	_, _, _, truncated, err := tp.Store.mark(ctx, -1, -1, time.Now().Add(-time.Second))
	require.NoError(t, err, "an expired deadline is a normal outcome of idle work, never an error")
	require.True(t, truncated, "an expired deadline must truncate the mark phase")

	// And the consequence at the GC level, over whichever of the two phases the budget ran out in.
	rep, err := tp.Store.GC(ctx, GCPolicy{
		RetainDays: -1, RetainSessions: -1, Deadline: time.Nanosecond,
	})
	require.NoError(t, err)
	require.True(t, rep.Truncated, "an expired deadline must truncate the pass")

	if rep.ScannedObjects == 0 {
		require.Zero(t, rep.DeletedObjects)
		require.Equal(t, before, objectCount(t, tp),
			"a pass truncated in mark must collect nothing: every reference it never reached looks dead")
		require.NoFileExists(t, filepath.Join(paths.Of(tp.Root).State, gcStateFile),
			"a truncated mark must leave no cursor, or the next pass resumes a sweep against a live "+
				"set that was never finished")
		return
	}
	// The mark completed inside one clock tick and the sweep truncated instead, against a live set
	// that IS complete. That is the sweep's own bound, and it must still stop exactly on a check.
	require.Zero(t, (rep.ScannedObjects+1)%gcCheckEvery,
		"a truncated sweep stops on a deadline check, so it can only report a multiple of %d minus "+
			"one; it reported %d", gcCheckEvery, rep.ScannedObjects)
}

// TestGC_MarkPhaseHonoursCancellation is the ctx half: a cancelled caller gets an error, not a
// silently truncated pass, because the two are different events (a shutdown against a spent budget).
func TestGC_MarkPhaseHonoursCancellation(t *testing.T) {
	tp := newTestStore(t)
	for i := 0; i < gcCheckEvery*3; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/c%04d.ts", i), fmt.Sprintf("cancel body %d, unique\n", i))
	}
	before := objectCount(t, tp)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// mark is called directly as well as through GC, because GC checks ctx once on entry and would
	// return before mark ran at all — which would leave the phase's OWN cancellation path, the one
	// that matters for a pass cancelled while it is already streaming files, unexercised.
	_, _, _, truncated, err := tp.Store.mark(ctx, -1, -1, time.Time{})
	require.ErrorIs(t, err, context.Canceled, "the mark phase must observe its caller's cancellation")
	require.False(t, truncated, "a cancelled caller is an error, not a spent budget; the two differ")

	_, err = tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, before, objectCount(t, tp), "a cancelled pass must collect nothing")
}

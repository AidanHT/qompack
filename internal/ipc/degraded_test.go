package ipc

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// This file is what enforces budget B-G (internal/obs/budgets.go): the synchronous spool append a
// hook pays inside Client.Send when the daemon cannot take the event. B-G itself is reported only
// — no production evaluator can ever see one of its samples, for the structural reason its
// Budgets() entry gives — so this gate is the only thing standing on that path, not a second
// opinion beside a runtime check. It is white-box (package ipc) because the calibration below has
// to perform the same filesystem work the spool does with the same permission constant, and
// dirPerm is unexported.
//
// Why the gate is rate-graded rather than wall-clock. What this path costs is a cold
// create-and-append, so it is the host's disk that is being timed, not this package. Measured
// quiet on this repo's own Windows host (NTFS), at exactly the settings below, one run's batches
// ran 1.58 ms per append in the fastest and 7.45 ms in the slowest with the product byte-identical,
// and across a 20-run session of 200 batches the figure spanned 0.75 ms to 1.92 ms — the same code
// on the same disk, hours apart; bc44d2a measured the same call at p99 282 ms and max 541 ms on a
// co-loaded runner, and a busy loop on this host drove per-append wall clock to 26 ms. Any fixed
// millisecond bound tight enough to catch a regression there is a bound the runner decides. So the
// platform's own cost is priced in the SAME run, against the SAME volume, and the product is graded
// as a multiple of it — the pattern internal/store/gc_test.go's gcCalibratedSweep established
// (077b759) and the reasoning test/bench/hotpath/report.go's budgetIDBECPU applies to B-E.
//
// CPU time, the other co-load-immune clock in this repo, is the wrong instrument here: the work is
// disk-bound, and time spent waiting on the filesystem burns no CPU at all, so a regression that
// added an fsync per byte would be invisible to it.

// degradedGateBatch is how many cold appends one timed span performs, on each side of the
// comparison. It is a batch and not a single call because a single call is below this host's clock
// resolution: time.Now()'s smallest resolvable gap on the Windows host measured here is 541 us at
// the median (309 us fastest, 2.86 ms slowest), against a cold append that costs about 1 ms — so
// per-call samples quantise, and a 300-call run of them produced exact zeros and therefore
// infinite ratios. degradedGateClockTicks below turns that from a comment into an assertion.
const degradedGateBatch = 64

// degradedGateTrials is how many times each side is measured. Both ends of the comparison are
// reduced by min(), and the two sides are interleaved so that any weather passing over the host
// lands on both: under sustained co-load every trial is slow on both sides and the ratio holds,
// while intermittent load leaves at least one quiet trial on each side. A minimum is also the one
// statistic a real regression cannot dodge — extra work per call inflates the fastest sample
// exactly as much as the slowest.
//
// 10 rather than 6, and the difference was measured rather than guessed. The two minima are taken
// independently, so the reduced ratio is inflated whenever the calibration side happens on a lull
// the product side does not get, and under a busy loop pegging all 22 cores that happens often
// enough to matter: twelve runs at 6 trials reached 3.382x, twelve at 10 trials reached 2.590x.
// Four extra trials cost about 0.9 s per run and buy back most of the co-load tail.
const degradedGateTrials = 10

// degradedAppendFactor is how many times the platform's own cost the degraded append may take.
//
// The product side legitimately does a little more than the calibration — it JSON-encodes the
// request, takes the spool's mutex, stats the file to resume its byte count, records the B-G
// sample, and builds the client around all of it — so the honest nominal sits just over 1x.
// Measured at exactly these settings — degradedGateBatch=64, degradedGateTrials=10 — on four
// deliberately different environments:
//
//	Windows, NTFS, quiet        0.890x - 1.143x   over 20 runs (0.75-1.92 ms per append)
//	Windows, NTFS, `-race`       1.058x - 1.237x  over  5 runs (1.07-1.27 ms per append)
//	Linux, tmpfs                1.135x - 1.252x   over  5 runs (16.0-17.5 us per append)
//	Windows, all 22 cores busy  0.550x - 2.590x   over 12 runs
//
// The tmpfs row is adversarial by design: the faster the filesystem, the larger a share of the
// ratio the product's own CPU work becomes, and tmpfs is as fast as a filesystem gets. The co-load
// row is where the noise actually lives, and it is the row that sets this constant — 2.590x is the
// worst any environment produced, so 6x leaves 2.3x of headroom over it.
//
// What this band does and does not catch, measured at these settings rather than assumed. The
// grade is a ratio, so a regression is caught in proportion to how much it multiplies the work the
// calibration already does — and the calibration is dominated by creating a directory and a file,
// about 1 ms of NTFS, against which a few extra syscalls barely register:
//
//	4 extra f.Sync() per append   8.611x  9.327x  13.775x   FAILS, 3 of 3 — this is the target
//	1 extra f.Sync() per append   4.910x  4.585x   5.211x   passes: missed
//	1 write syscall per byte      4.205x  3.897x   3.776x   passes: missed
//
// So the honest claim is a band, not a floor: the gate reliably catches a regression that costs
// several times the whole cold append — an fsync loop, an fsync per byte on a journalled
// filesystem, a quadratic re-encode, a lock convoy — and does not catch one that adds a single
// constant-cost syscall. Tightening past about 3x would catch those too and would also have failed
// on this very host under co-load, where a correct product graded 3.382x at 6 trials. That
// trade-off is the reason the number is 6 and the reason this table is here.
// TestDegradedAppendGrade_JudgesARegressionAndSparesASlowDisk holds both edges of the arithmetic
// without needing a host to demonstrate either.
const degradedAppendFactor = 6

// degradedGateClockTicks is how many of the host clock's own smallest resolvable gaps a timed span
// must cover before it may be graded. It is the premise the batch above exists to satisfy, checked
// rather than assumed: a span measured near the clock's resolution carries a quantisation error
// comparable to the quantity itself, and a ratio of two such spans is noise. 32 caps that error at
// roughly 3%, which is nothing beside a 6x band.
const degradedGateClockTicks = 32

// degradedGateRequest is the representative event every append in this file writes: an ordinary
// PostToolUse observation, the op the degraded path carries in overwhelming volume. Its encoded
// size is asserted by the gate rather than assumed, so a future change to Request's shape that
// made the line an order of magnitude bigger cannot silently re-price the measurement.
func degradedGateRequest() Request {
	return Request{
		Op:      OpObserveTool,
		Session: core.SessionID("sess-degraded"),
		TS:      core.UnixMilli(1730000000000),
		Event: &hookio.Event{
			HookEventName: "PostToolUse",
			ToolName:      "Read",
			ToolUseID:     core.ToolUseID("tu_1"),
			ToolInput:     []byte(`{"file_path":"src/auth.ts"}`),
		},
	}
}

// clockTick reports this host's smallest resolvable gap between two time.Now() readings, as the
// median of n spins. The median rather than the minimum or maximum: the minimum can catch a tick
// boundary at the instant it turns over and read short, and the maximum is a descheduling, not a
// property of the clock.
func clockTick(t *testing.T, n int) time.Duration {
	t.Helper()
	gaps := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		var d time.Duration
		for d == 0 {
			d = time.Since(start)
		}
		gaps = append(gaps, d)
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

// platformAppendBatch is the calibration: n times, it does exactly the filesystem work one cold
// spool append is made of and nothing else — create the directory, open the file for append, write
// the line, close it — and returns how long the whole batch took. It goes through paths.AppendOnly
// and dirPerm, the same door and the same mode the spool uses, so the ratio it prices is the work
// the spool adds on top of the platform rather than a difference between two ways of opening a
// file.
func platformAppendBatch(t *testing.T, dir string, line []byte, n int) time.Duration {
	t.Helper()
	start := time.Now()
	for i := 0; i < n; i++ {
		unit := filepath.Join(dir, fmt.Sprintf("u%03d", i))
		if err := os.MkdirAll(paths.Long(unit), dirPerm); err != nil {
			t.Fatalf("calibration mkdir: %v", err)
		}
		w, err := paths.AppendOnly(filepath.Join(unit, spoolFilePrefix+"0"+spoolFileExt))
		if err != nil {
			t.Fatalf("calibration open: %v", err)
		}
		if _, err := w.Write(line); err != nil {
			t.Fatalf("calibration write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("calibration close: %v", err)
		}
	}
	return time.Since(start)
}

// degradedSendBatch is the measurement: n times, it builds the client one hook process builds and
// drives one Send down the degraded path, against a fresh spool directory each time so every
// append is cold exactly as a first-hook-in-a-cold-project's is. The branch taken is Send step 2,
// the operator-disabled daemon — the one degraded route that reaches the append without also
// spending a dial timeout, so the span times the append and not the network stack. Every other
// route into spoolAndReturn lands on the identical call.
//
// Client construction and Close are inside the span deliberately: a hook process pays both, once,
// around the single Send it makes, and Close is the counterpart of the calibration's own close.
func degradedSendBatch(t *testing.T, dir string, addr Addr, req Request, n int, reg obs.Registry) time.Duration {
	t.Helper()
	st := State{Mode: contract.ModeFull, ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: false}

	start := time.Now()
	for i := 0; i < n; i++ {
		sp, err := NewSpool(filepath.Join(dir, fmt.Sprintf("u%03d", i)))
		if err != nil {
			t.Fatalf("spool: %v", err)
		}
		c := NewClientWithOptions(addr, sp, logging.Nop(), reg, ClientOptions{State: st})
		if _, err := c.Send(context.Background(), req, testDeadline); err != nil {
			t.Fatalf("Send must never return an error: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	return time.Since(start)
}

// degradedAppendGrade reports the degraded append's cost as a multiple of the platform's own, for
// two spans of equal length measured on the same volume in the same run. A calibration that
// measured zero is reported as an infinite ratio rather than a division by zero: the caller
// refuses that case on the clock premise before it ever gets here, and a silent NaN would be a
// gate that passes on no measurement at all.
func degradedAppendGrade(platform, degraded time.Duration) float64 {
	if platform <= 0 {
		return math.Inf(1)
	}
	return float64(degraded) / float64(platform)
}

// TestDegradedAppendGrade_JudgesARegressionAndSparesASlowDisk is the standing red for the gate
// below, and it needs no host to produce either edge: the same arithmetic that grades the live
// measurement is fed spans that state, in numbers, what B-G is and is not for.
//
// A disk ten times slower than another host's moves both spans together and changes nothing — that
// is the whole point of grading a rate. A product that does ten times the work per append on the
// same disk fails, and so does a calibration too small to have been measured at all.
func TestDegradedAppendGrade_JudgesARegressionAndSparesASlowDisk(t *testing.T) {
	const fast = 20 * time.Millisecond
	const slow = 100 * fast // the same batch on a disk two orders of magnitude slower

	require.LessOrEqual(t, degradedAppendGrade(fast, fast), float64(degradedAppendFactor),
		"a product that costs what the platform costs must pass")
	require.LessOrEqual(t, degradedAppendGrade(slow, slow), float64(degradedAppendFactor),
		"the same product on a far slower disk must pass identically: the gate grades a rate")
	require.LessOrEqual(t, degradedAppendGrade(fast, degradedAppendFactor*fast),
		float64(degradedAppendFactor), "the factor itself is inside the band, not outside it")

	require.Greater(t, degradedAppendGrade(fast, (degradedAppendFactor+1)*fast),
		float64(degradedAppendFactor), "one step past the factor must fail")
	require.Greater(t, degradedAppendGrade(slow, 10*slow), float64(degradedAppendFactor),
		"ten times the work per append fails on a slow disk too, which a millisecond bound could not tell apart")
	require.Greater(t, degradedAppendGrade(0, fast), float64(degradedAppendFactor),
		"a calibration that measured nothing must fail the gate, never divide by zero into a pass")
}

// TestDegradedSpoolAppendIsRateGradedAgainstThePlatform is B-G's gate: the degraded path's own
// cost, priced against what the same filesystem work costs without it, on the same volume in the
// same run.
//
// This is the assertion bc44d2a left behind. That commit correctly stopped folding the spool append
// into a deadline-derived wall-clock bound — the append is not deadline-governed and that bound was
// measuring the runner's disk — but subtracting it left the path with nothing checking it at all.
func TestDegradedSpoolAppendIsRateGradedAgainstThePlatform(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	req := degradedGateRequest()
	line, err := EncodeRequest(req)
	require.NoError(t, err)
	require.Less(t, len(line), MaxLineBytes,
		"premise: the representative event must be an ordinary line, not one the spool would refuse")

	reg := obs.New(core.SystemClock())
	var platform, degraded time.Duration
	for i := 0; i < degradedGateTrials; i++ {
		p := platformAppendBatch(t, filepath.Join(root, fmt.Sprintf("cal%02d", i)), line, degradedGateBatch)
		d := degradedSendBatch(t, filepath.Join(root, fmt.Sprintf("run%02d", i)), addr, req, degradedGateBatch, reg)
		t.Logf("trial %d: platform %v (%v/append), degraded %v (%v/append), grade %.3fx",
			i, p, p/degradedGateBatch, d, d/degradedGateBatch, degradedAppendGrade(p, d))
		if i == 0 || p < platform {
			platform = p
		}
		if i == 0 || d < degraded {
			degraded = d
		}
	}

	// Premise: both spans must be far enough above the clock's own resolution to be a measurement.
	// Checked, not assumed — a single append on this host is only two or three ticks wide, which is
	// what makes the batch necessary in the first place.
	floor := degradedGateClockTicks * clockTick(t, degradedGateBatch)
	require.Greater(t, platform, floor,
		"calibration span %v is not %dx this host's clock resolution (%v): the batch is too small to grade",
		platform, degradedGateClockTicks, floor/degradedGateClockTicks)
	require.Greater(t, degraded, floor,
		"measured span %v is not %dx this host's clock resolution (%v): the batch is too small to grade",
		degraded, degradedGateClockTicks, floor/degradedGateClockTicks)

	// Premise: every Send took the degraded route and landed on exactly one append, so the span
	// covers the appends it claims to and nothing paid for a dial.
	want := int64(degradedGateTrials * degradedGateBatch)
	snap := reg.Snapshot()
	require.Equal(t, want, snap.Counters[counterL0Spooled], "every Send must have spooled exactly once")
	require.Zero(t, snap.Counters[counterL0Dropped], "no append may have been refused")

	grade := degradedAppendGrade(platform, degraded)
	t.Logf("B-G rate grade: fastest platform batch %v (%v/append), fastest degraded batch %v (%v/append), %.3fx of %dx allowed (limit %v, %d appends of %d bytes)",
		platform, platform/degradedGateBatch, degraded, degraded/degradedGateBatch,
		grade, degradedAppendFactor, budgetLimit(t, obs.BG), want, len(line))

	require.LessOrEqualf(t, grade, float64(degradedAppendFactor),
		"the degraded spool append cost %.3fx what the same create-and-append costs on this volume "+
			"(fastest of %d batches of %d: platform %v, degraded %v). Budget B-G allows %dx. A slow or "+
			"loaded disk cannot produce this — it moves both spans — so this is work the append itself "+
			"gained: an added sync, a re-encode, or contention on the spool's mutex",
		grade, degradedGateTrials, degradedGateBatch, platform, degraded, degradedAppendFactor)
}

// TestDegradedSendRecordsIntoBGsHistogram pins the wiring B-G needs to be a budget rather than a
// name: every degraded Send must leave a sample in the histogram obs.Budgets() says B-G reads, so
// CheckBudgets has something to check. It asserts the series by looking the name up from the budget
// table, never by respelling it, and it does not time anything — the population is exact.
func TestDegradedSendRecordsIntoBGsHistogram(t *testing.T) {
	const sends = 5
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	bg := budgetLimitAndHist(t, obs.BG)
	require.Equal(t, bg.Hist, histDegraded,
		"the client must record into the series the budget table names, not one of its own")

	reg := obs.New(core.SystemClock())
	_ = degradedSendBatch(t, filepath.Join(root, "spools"), addr, degradedGateRequest(), sends, reg)

	snap := reg.Snapshot()
	require.EqualValues(t, sends, snap.Hists[bg.Hist].N,
		"every degraded Send must observe its append into %s", bg.Hist)
	require.EqualValues(t, sends, snap.Counters[counterL0Spooled])

	// A hook whose Registry is nil must still spool: the measurement is an observation, never a
	// precondition for durability.
	sp, err := NewSpool(filepath.Join(root, "nometrics"))
	require.NoError(t, err)
	c := NewClientWithOptions(addr, sp, logging.Nop(), nil, ClientOptions{
		State: State{Mode: contract.ModeFull, ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: false},
	})
	res, err := c.Send(context.Background(), degradedGateRequest(), testDeadline)
	require.NoError(t, err)
	require.False(t, res.OK, "a spooled event is honourably not-OK, never an error")
	require.NoError(t, c.Close())
	files, err := SpoolFiles(filepath.Join(root, "nometrics"))
	require.NoError(t, err)
	require.Len(t, files, 1, "the event must be on disk even with no Registry to time the write")
}

// budgetLimitAndHist returns the Budget obs.Budgets() declares for id, failing the test if there is
// none — the same indirection test/bench/hotpath and internal/daemon use, so no test in this
// package restates a budget's histogram name or limit.
func budgetLimitAndHist(t *testing.T, id obs.BudgetID) obs.Budget {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("obs.Budgets() does not declare %s", id)
	return obs.Budget{}
}

// budgetLimit is budgetLimitAndHist's limit against the shipped defaults, for log lines that want
// to name the budget the graded number lives under.
func budgetLimit(t *testing.T, id obs.BudgetID) time.Duration {
	t.Helper()
	return budgetLimitAndHist(t, id).Limit(config.Defaults())
}

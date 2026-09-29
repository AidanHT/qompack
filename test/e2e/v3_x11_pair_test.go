package e2e

// X11's paired comparison (coordinator decision D42, plans/V6-CLOSEOUT-CHECKLIST.md, 2026-09-29):
// the pure half of TestV3_HotPathUnchangedWithLedgerResident's "the ledger must not move the hot
// path" judgement, and the tree copy that gives the no-ledger run the same corpus.
//
// The judged quantity is hook_controlled_observed, recvTS - req.TS, read from the harness's own
// "daemon-observed hook_controlled_observed" note (test/bench/hotpath/measure.go, buildNotes): the
// part of B-A before the handler runs, so the only part a resident ledger could move without also
// moving B-B, and the one part whose definition has not changed since V2. It is never read from
// the wall-clock spawn samples.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// x11ObservedNote matches the harness's hook_controlled_observed note. The opening mirrors
// buildNotes' format string verbatim up to n; the harness is package main and cannot be imported,
// so a drift in its prose makes x11ReadObserved refuse the artifact rather than skip the gate.
var x11ObservedNote = regexp.MustCompile(`^daemon-observed hook_controlled_observed \(recvTS-reqTS, ` +
	`strict lower bound, no hotPathTailAllowance\): p50=(\d+\.\d+)ms p99=(\d+\.\d+)ms n=(\d+) `)

// x11ObservedNotePrefix is the note's fixed opening, used only to spot a note the regexp no longer
// reads (so the refusal can say which note it was).
const x11ObservedNotePrefix = "daemon-observed hook_controlled_observed"

// x11Observed is one run's hook_controlled_observed row, in whole microseconds: the note renders
// milliseconds with three decimals, so a microsecond is its exact resolution.
type x11Observed struct {
	p50us, p99us int64
	n            int
}

// x11ParseMs converts a note's "%.3f" millisecond figure to whole microseconds.
func x11ParseMs(s string) (int64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("unreadable millisecond figure %q: %w", s, err)
	}
	return int64(math.Round(v * float64(time.Millisecond/time.Microsecond))), nil
}

// x11ReadObserved returns the artifact's hook_controlled_observed row, refusing an artifact that
// carries no such note, carries it twice, or carries one it cannot read. The harness writes the
// note only when the daemon's histogram holds at least one sample, so a missing note is a run whose
// comparison has nothing to compare, never a pass.
func x11ReadObserved(rep x11BenchReport) (x11Observed, error) {
	var out x11Observed
	found := false
	for _, note := range rep.Notes {
		m := x11ObservedNote.FindStringSubmatch(note)
		if m == nil {
			if len(note) >= len(x11ObservedNotePrefix) && note[:len(x11ObservedNotePrefix)] == x11ObservedNotePrefix {
				return out, fmt.Errorf("the bench artifact carries a hook_controlled_observed note this test "+
					"cannot read (has the harness's buildNotes format changed?): %q", note)
			}
			continue
		}
		if found {
			return out, fmt.Errorf("the bench artifact states hook_controlled_observed twice: %q", rep.Notes)
		}
		found = true
		p50, err := x11ParseMs(m[1])
		if err != nil {
			return out, err
		}
		p99, err := x11ParseMs(m[2])
		if err != nil {
			return out, err
		}
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return out, fmt.Errorf("unreadable sample count %q: %w", m[3], err)
		}
		if n <= 0 || p50 <= 0 || p99 < p50 {
			return out, fmt.Errorf("the hook_controlled_observed note is not a histogram summary "+
				"(p50=%dus p99=%dus n=%d): %q", p50, p99, n, note)
		}
		out = x11Observed{p50us: p50, p99us: p99, n: n}
	}
	if !found {
		return out, fmt.Errorf("the bench artifact carries no daemon-observed hook_controlled_observed note, "+
			"so the paired comparison has no number to judge; notes: %q", rep.Notes)
	}
	return out, nil
}

// errX11LedgerRegressed marks x11LedgerPairVerdict's one judgement failure, so a co-loaded run can
// report it without also forgiving an unreadable artifact.
var errX11LedgerRegressed = errors.New("X11: the resident ledger moved the hot path")

// errX11PairIncomparable marks a pair whose two hook_controlled_observed populations differ in
// size. An isolated run fails it; a co-loaded run, where a spool deferral is a reported outcome
// (D39), reports it as a pair it could not compare.
var errX11PairIncomparable = errors.New("X11: the two runs' hook_controlled_observed populations differ in size")

// x11DeliveryLedgerNotePrefix is the opening of the harness's delivery-ledger note, verbatim from
// buildNotes (test/bench/hotpath/measure.go), which writes the note only when the run deferred at
// least one hot-path request to the client spool.
const x11DeliveryLedgerNotePrefix = "delivery ledger: "

// x11DeferralNote returns the artifact's delivery-ledger note, if it carries one. A run with that
// note moved hooks to the client spool part of the way through, so its daemon-side populations lack
// the requests that never reached the daemon: an isolated X11 run refuses it, the way
// test/integration's hotpathJudgeSpool refuses any spool transition in an isolated run.
func x11DeferralNote(rep x11BenchReport) (string, bool) {
	for _, note := range rep.Notes {
		if strings.HasPrefix(note, x11DeliveryLedgerNotePrefix) {
			return note, true
		}
	}
	return "", false
}

// x11Pair is what x11LedgerPairVerdict compared, for the test's log.
type x11Pair struct {
	base, ledger x11Observed
	ceilingUS    float64
}

// x11LedgerPairVerdict is D42's judgement: the ledger run's hook_controlled_observed p50 may be at
// most x11RegressionFactor times the paired no-ledger run's, taken on the same host in the same
// test. The factor is the whole rule, exactly as D42 records it ("no new number"). The quantity is
// quantized: recvTS and req.TS are whole milliseconds, so every p50 is a whole number of the obs
// histogram's 1.024 ms buckets, and below four of them 25% is less than one bucket, so there a
// one-bucket move fails the pair (Linux's p50 is one bucket on every recorded run).
//
// It returns an error wrapping errX11PairIncomparable when the two notes' sample counts differ,
// one wrapping errX11LedgerRegressed when the ledger p50 is over the ceiling, and a plain error
// when either artifact's note is missing or unreadable.
func x11LedgerPairVerdict(base, ledger x11BenchReport) (x11Pair, error) {
	var out x11Pair
	b, err := x11ReadObserved(base)
	if err != nil {
		return out, fmt.Errorf("the no-ledger run: %w", err)
	}
	l, err := x11ReadObserved(ledger)
	if err != nil {
		return out, fmt.Errorf("the ledger-resident run: %w", err)
	}
	out = x11Pair{base: b, ledger: l, ceilingUS: x11RegressionFactor * float64(b.p50us)}
	if b.n != l.n {
		return out, fmt.Errorf("%w: n=%d without the ledger, n=%d with it — a p50 over fewer samples is a p50 "+
			"over part of the run (a spool deferral or rejected samples), so the two are not compared",
			errX11PairIncomparable, b.n, l.n)
	}
	if float64(l.p50us) > out.ceilingUS {
		return out, fmt.Errorf("%w: hook_controlled_observed p50 %.3fms with the 5 000-entry ledger resident "+
			"is over %.3fms, the ceiling from the paired no-ledger run's %.3fms (x%.2f)", errX11LedgerRegressed,
			x11MsOf(l.p50us), x11MsOf(int64(out.ceilingUS)), x11MsOf(b.p50us), x11RegressionFactor)
	}
	return out, nil
}

// x11MsOf renders whole microseconds as milliseconds.
func x11MsOf(us int64) float64 {
	return float64(us) / float64(time.Millisecond/time.Microsecond)
}

// x11CopyProject copies the project tree src to dst, which must not exist yet, so the no-ledger
// run measures the same bytes the ledger run's corpus holds. The one directory left out is
// .qompack/logs: it holds the setup's own log, still open in p.Log, and is not resident state; the
// harness's EnsureLayout recreates it. Anything but a regular file or a directory is refused, since
// the fixture writes neither links nor devices and a copy that silently dropped one would not be
// the same project.
func x11CopyProject(src, dst string) error {
	logs := filepath.Clean(paths.Of(src).Logs)
	return filepath.WalkDir(paths.Long(src), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(paths.Long(src), p)
		if err != nil {
			return err
		}
		if filepath.Join(src, rel) == logs {
			return fs.SkipDir
		}
		target := paths.Long(filepath.Join(dst, rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return x11CopyFile(p, target, info.Mode().Perm())
		default:
			return fmt.Errorf("x11CopyProject: %s is neither a regular file nor a directory (%s)", p, info.Mode())
		}
	})
}

// x11CopyFile copies one regular file, keeping its permission bits.
func x11CopyFile(src, dst string, perm fs.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// ── fixtures ─────────────────────────────────────────────────────────────────────────────────────

// x11ObservedNoteFixture renders a note exactly as buildNotes does (its format string, verbatim).
func x11ObservedNoteFixture(p50, p99 float64, n int) string {
	return fmt.Sprintf("daemon-observed hook_controlled_observed (recvTS-reqTS, strict lower bound, no "+
		"hotPathTailAllowance): p50=%.3fms p99=%.3fms n=%d — informational cross-check against the gated "+
		"B-A row above, which adds the tail allowance", p50, p99, n)
}

// x11ReportWithNotes is a bench artifact carrying only notes, the one field the comparison reads.
func x11ReportWithNotes(notes ...string) x11BenchReport {
	return x11BenchReport{Notes: append([]string{"B-C not measured in wave 1: the processing seams are stubs"}, notes...)}
}

// TestV3_X11LedgerPairVerdict pins x11LedgerPairVerdict on fixtures: RED when the ledger run's p50
// is over the paired ceiling, GREEN at or under it, the factor alone deciding at every scale, and a
// refusal, never a pass, when either artifact lacks a readable note.
func TestV3_X11LedgerPairVerdict(t *testing.T) {
	const n = 2064
	note := func(p50, p99 float64) x11BenchReport { return x11ReportWithNotes(x11ObservedNoteFixture(p50, p99, n)) }

	for _, tc := range []struct {
		name         string
		base, ledger float64
		regressed    bool
	}{
		{"equal p50 is green", 4.096, 4.096, false},
		{"ledger faster is green", 5.120, 4.096, false},
		{"exactly 1.25x is green", 4.096, 5.120, false},
		{"over 1.25x is red", 4.096, 6.144, true},
		{"well over is red", 6.144, 30.720, true},
		{"one bucket above a one-bucket p50 is red", 1.024, 2.048, true},
		{"two buckets above a one-bucket p50 is red", 1.024, 3.072, true},
		{"one bucket above a three-bucket p50 is red", 3.072, 4.096, true},
		{"a zero-millisecond p50 leaves no room for one bucket", 0.001, 1.024, true},
		{"above sixteen buckets within the factor is green", 18.432, 22.528, false},
		{"above sixteen buckets over the factor is red", 18.432, 24.576, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pair, err := x11LedgerPairVerdict(note(tc.base, 2*tc.base), note(tc.ledger, 2*tc.ledger))
			if tc.regressed {
				require.ErrorIs(t, err, errX11LedgerRegressed, "ledger p50 %.3f vs base %.3f", tc.ledger, tc.base)
				return
			}
			require.NoError(t, err)
			require.LessOrEqual(t, float64(pair.ledger.p50us), pair.ceilingUS)
			require.Equal(t, n, pair.base.n)
		})
	}

	t.Run("refusals", func(t *testing.T) {
		good := note(4.096, 7.168)
		for _, rc := range []struct {
			name         string
			base, ledger x11BenchReport
		}{
			{"no note in the no-ledger run", x11ReportWithNotes(), good},
			{"no note in the ledger run", good, x11ReportWithNotes()},
			{"the note twice", good, x11ReportWithNotes(x11ObservedNoteFixture(4.096, 7.168, n),
				x11ObservedNoteFixture(4.096, 7.168, n))},
			{"a note in a drifted format", good, x11ReportWithNotes(
				"daemon-observed hook_controlled_observed (recvTS-reqTS): p50=4.096ms p99=7.168ms n=2064")},
			{"a p99 below its p50", good, note(4.096, 1.024)},
		} {
			t.Run(rc.name, func(t *testing.T) {
				_, err := x11LedgerPairVerdict(rc.base, rc.ledger)
				require.Error(t, err, "an artifact without a readable note must be refused, not passed")
				require.NotErrorIs(t, err, errX11LedgerRegressed,
					"a refusal is not a verdict: co-loaded runs report verdicts but must still fail refusals")
				require.NotErrorIs(t, err, errX11PairIncomparable,
					"an unreadable artifact is not an unequal population: co-loaded runs must still fail it")
			})
		}
	})

	// A run that lost samples to the client spool, or to the daemon's invalid-sample filter, judges
	// its p50 over a shorter population than its twin's; a p50 over a prefix that ended at a spool
	// transition is the fast part of the run. That pair is refused as incomparable whatever the two
	// p50s say, and never as a verdict.
	t.Run("unequal populations", func(t *testing.T) {
		for _, uc := range []struct {
			name             string
			baseN, ledgerN   int
			basePs, ledgerPs float64
		}{
			{"a shorter ledger run with an equal p50", n, n - 564, 4.096, 4.096},
			{"a shorter no-ledger run with an equal p50", n - 1, n, 4.096, 4.096},
			{"a shorter ledger run over the ceiling", n, 1500, 4.096, 30.720},
		} {
			t.Run(uc.name, func(t *testing.T) {
				_, err := x11LedgerPairVerdict(
					x11ReportWithNotes(x11ObservedNoteFixture(uc.basePs, 2*uc.basePs, uc.baseN)),
					x11ReportWithNotes(x11ObservedNoteFixture(uc.ledgerPs, 2*uc.ledgerPs, uc.ledgerN)))
				require.ErrorIs(t, err, errX11PairIncomparable, "n=%d against n=%d", uc.baseN, uc.ledgerN)
				require.NotErrorIs(t, err, errX11LedgerRegressed, "an incomparable pair has no verdict")
			})
		}
	})
}

// TestV3_X11DeferralNoteIsFound pins x11DeferralNote, which an isolated X11 run uses to refuse a
// run that deferred any hot-path request to the client spool: the harness's delivery-ledger note is
// found exactly when buildNotes wrote it, and a clean artifact carries none.
func TestV3_X11DeferralNoteIsFound(t *testing.T) {
	const ledgerNote = "delivery ledger: 2064 hot-path requests sent, 1500 delivered live to the daemon, " +
		"564 DEFERRED to the client spool and 0 lost. A deferral is §8.1/§12.2's documented path"
	got, ok := x11DeferralNote(x11ReportWithNotes(x11ObservedNoteFixture(4.096, 8.192, 1500), ledgerNote))
	require.True(t, ok, "a run whose artifact carries the delivery ledger deferred to the spool")
	require.Equal(t, ledgerNote, got)

	_, ok = x11DeferralNote(x11ReportWithNotes(x11ObservedNoteFixture(4.096, 8.192, 2064),
		"B-A's gated n=2064 includes 64 in-process warm-up observe.tool requests"))
	require.False(t, ok, "a clean run's artifact carries no delivery ledger")
}

// TestV3_X11CopyProjectIsExactButForLogs pins the twin X11's no-ledger run measures: every file
// and directory of the project, bytes and all, except .qompack/logs; and a destination that
// already holds the file is refused rather than overwritten.
func TestV3_X11CopyProjectIsExactButForLogs(t *testing.T) {
	src := filepath.Join(t.TempDir(), "project")
	l := paths.Of(src)
	files := map[string]string{
		filepath.Join(l.Index, "tool_uses.jsonl"):  "{\"id\":1}\n",
		filepath.Join(l.Objects, "ab", "cdef"):     "object bytes",
		filepath.Join(l.DAG, "deps.jsonl"):         "{\"edge\":1}\n",
		filepath.Join(src, "src", "svc", "x11.ts"): "// source",
	}
	for p, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	require.NoError(t, os.MkdirAll(l.Logs, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(l.Logs, "qompack.log"), []byte("setup log"), 0o600))
	require.NoError(t, os.MkdirAll(l.Spool, 0o700), "an empty directory is copied too")

	dst := filepath.Join(t.TempDir(), "twin")
	require.NoError(t, x11CopyProject(src, dst))
	for p, body := range files {
		rel, err := filepath.Rel(src, p)
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(dst, rel))
		require.NoError(t, err, "%s must be copied", rel)
		require.Equal(t, body, string(got), "%s must be copied byte for byte", rel)
	}
	require.DirExists(t, paths.Of(dst).Spool)
	require.NoDirExists(t, paths.Of(dst).Logs, "the setup's log is not resident state and is not copied")

	require.Error(t, x11CopyProject(src, dst), "copying over an existing twin must fail, not merge")
}

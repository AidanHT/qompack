package main

import (
	"strings"
	"testing"
)

// benchstatCSVFixture is a trimmed but byte-faithful sample of `benchstat -format csv old new`
// output: bare configuration lines, a two-line header per unit table, data rows, and a geomean
// summary. It carries one row per case the gate has to get right.
const benchstatCSVFixture = `goos: linux
goarch: amd64
pkg: github.com/qompack/qompack/internal/sketch
cpu: Intel(R) Core(TM) Ultra 7 155H
,old.txt,,new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
BloomAdd-22,1.5e-07,3%,1.68e-07,4%,+12.00%,p=0.000 n=10
CMSAdd-22,1e-07,3%,1.3e-07,4%,+30.00%,p=0.000 n=10
HLLAdd-22,1e-07,3%,1.02e-07,4%,~,p=0.481 n=10
MinHash100KiB-22,0.0025,3%,0.002,4%,-20.00%,p=0.000 n=10
geomean,1.2e-07,,1.3e-07,,+8.33%,
,old.txt,,new.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
BloomAdd-22,4,0%,5,0%,+25.00%,p=0.000 n=10
geomean,4,,5,,+25.00%,

pkg: github.com/qompack/qompack/internal/paths
,old.txt,,new.txt,,,
,B/s,CI,B/s,CI,vs base,P
PathsWriteAtomic_4KB-22,1080000,3%,756000,4%,-30.00%,p=0.000 n=10
PathsWriteAtomic_64KB-22,1080000,3%,1404000,4%,+30.00%,p=0.000 n=10
geomean,1080000,,1030000,,-4.63%,
`

// TestParseBenchstatCSV_ClassifiesEachUnitDirection is the core of the gate: benchstat's `vs base`
// is the raw change of the metric and says nothing about whether the change is good, so every
// assertion here is about the SIGN after this parser has applied the unit's direction.
func TestParseBenchstatCSV_ClassifiesEachUnitDirection(t *testing.T) {
	cmp, err := parseBenchstatCSV(strings.NewReader(benchstatCSVFixture))
	if err != nil {
		t.Fatalf("parseBenchstatCSV: %v", err)
	}
	if len(cmp.unknownUnits) != 0 {
		t.Fatalf("fixture uses only classified units, got unknown: %v", cmp.unknownUnits)
	}

	// Seven data rows across the three tables; geomean rows are summaries and are not benchmarks.
	if cmp.rows != 7 {
		t.Fatalf("want 7 compared rows (geomean excluded), got %d", cmp.rows)
	}
	// Six of them moved significantly; HLLAdd's "~" is benchstat saying it cannot tell.
	if len(cmp.deltas) != 6 {
		t.Fatalf("want 6 significant deltas, got %d: %v", len(cmp.deltas), cmp.deltas)
	}

	got := map[string]float64{}
	for _, d := range cmp.deltas {
		got[d.name+" "+d.unit] = d.pct
	}
	want := map[string]float64{
		// sec/op is lower-is-better: benchstat's sign is already "positive means worse".
		"BloomAdd-22 sec/op":      12,
		"CMSAdd-22 sec/op":        30,
		"MinHash100KiB-22 sec/op": -20,
		"BloomAdd-22 allocs/op":   25,
		// B/s is higher-is-better, so the sign inverts: a 30% DROP in throughput is 30% worse, and
		// a 30% rise is an improvement. Reading these two naively would gate the improvement and
		// pass the regression.
		"PathsWriteAtomic_4KB-22 B/s":  30,
		"PathsWriteAtomic_64KB-22 B/s": -30,
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing delta for %q; got %v", k, got)
			continue
		}
		if g != w {
			t.Errorf("%s: want %+.2f%% worse, got %+.2f%%", k, w, g)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected extra deltas: got %v, want keys %v", got, want)
	}
}

// TestParseBenchstatCSV_ThresholdBands checks the two §7 bands against the fixture, because the
// boundary is strictly greater-than on both: exactly 25.00% worse is a warning, not a failure.
func TestParseBenchstatCSV_ThresholdBands(t *testing.T) {
	cmp, err := parseBenchstatCSV(strings.NewReader(benchstatCSVFixture))
	if err != nil {
		t.Fatalf("parseBenchstatCSV: %v", err)
	}

	var warn, fail []string
	for _, d := range cmp.deltas {
		switch {
		case d.pct > benchFailPct:
			fail = append(fail, d.name+" "+d.unit)
		case d.pct > benchWarnPct:
			warn = append(warn, d.name+" "+d.unit)
		}
	}

	wantWarn := map[string]bool{"BloomAdd-22 sec/op": true, "BloomAdd-22 allocs/op": true}
	wantFail := map[string]bool{"CMSAdd-22 sec/op": true, "PathsWriteAtomic_4KB-22 B/s": true}

	if len(warn) != len(wantWarn) {
		t.Errorf("want %d warnings (+12%% sec/op and the +25%% allocs/op that is NOT over the fail "+
			"threshold), got %v", len(wantWarn), warn)
	}
	for _, w := range warn {
		if !wantWarn[w] {
			t.Errorf("unexpected warning: %s", w)
		}
	}
	if len(fail) != len(wantFail) {
		t.Errorf("want %d failures, got %v", len(wantFail), fail)
	}
	for _, f := range fail {
		if !wantFail[f] {
			t.Errorf("unexpected failure: %s", f)
		}
	}
}

// TestParseBenchstatCSV_UnknownUnitIsReported is the loud-degradation case. A unit this file does
// not classify cannot be scored in either direction without guessing, and both guesses disable or
// invert the gate silently, so it is surfaced for a human to classify.
func TestParseBenchstatCSV_UnknownUnitIsReported(t *testing.T) {
	const in = `pkg: github.com/qompack/qompack/internal/eval
,old.txt,,new.txt,,,
,tokens/sess,CI,tokens/sess,CI,vs base,P
Recall-22,100,3%,80,4%,-20.00%,p=0.000 n=10
`
	cmp, err := parseBenchstatCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseBenchstatCSV: %v", err)
	}
	if len(cmp.deltas) != 0 {
		t.Errorf("an unclassified unit must not be scored, got %v", cmp.deltas)
	}
	if len(cmp.unknownUnits) != 1 || cmp.unknownUnits[0] != "tokens/sess" {
		t.Fatalf("want the unknown unit reported by name, got %v", cmp.unknownUnits)
	}
	if cmp.rows != 1 {
		t.Errorf("the row was still compared and must be counted, got rows=%d", cmp.rows)
	}
}

// TestParseBenchstatCSV_NoSharedBenchmarksIsNotAPass covers the dead-gate case that motivated the
// task: benchstat given two files with nothing in common prints tables with no comparison column
// at all and exits 0. rows == 0 is what lets taskBenchCompare tell that apart from "the two runs
// agree", which is the same distinction V2-MERGE-03 is about for a lost baseline block.
func TestParseBenchstatCSV_NoSharedBenchmarksIsNotAPass(t *testing.T) {
	const in = `pkg: github.com/qompack/qompack/internal/sketch
,old.txt,
,sec/op,CI
BloomAdd-22,1.5e-07,3%
geomean,1.5e-07,
`
	cmp, err := parseBenchstatCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseBenchstatCSV: %v", err)
	}
	if cmp.rows != 0 {
		t.Fatalf("a single-file table has no comparison column and must count zero rows, got %d", cmp.rows)
	}
}

// TestParseBenchstatDelta covers the cell grammar directly, including the "~" that benchstat
// writes when the two distributions are indistinguishable.
func TestParseBenchstatDelta(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"+12.34%", 12.34, true},
		{"-5.00%", -5, true},
		{" +0.00% ", 0, true},
		{"~", 0, false},
		{"", 0, false},
		{"p=0.481 n=10", 0, false},
	}
	for _, c := range cases {
		got, ok := parseBenchstatDelta(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseBenchstatDelta(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestBenchComparePaths_DefaultsToTheCommittedBaseline pins the one-argument form, which is the
// shape §7's rule is actually run in.
func TestBenchComparePaths_DefaultsToTheCommittedBaseline(t *testing.T) {
	oldRoot := root
	root = testModuleRoot(t)
	defer func() { root = oldRoot }()

	oldPath, newPath, err := benchComparePaths([]string{defaultBenchBaseline})
	if err != nil {
		t.Fatalf("bench-compare with one argument: %v", err)
	}
	if oldPath != defaultBenchBaseline || newPath != defaultBenchBaseline {
		t.Fatalf("want both sides resolved, got old=%q new=%q", oldPath, newPath)
	}

	if _, _, err := benchComparePaths(nil); err == nil {
		t.Error("bench-compare with no arguments should be a usage error")
	}
	if _, _, err := benchComparePaths([]string{defaultBenchBaseline, "no-such-file.txt"}); err == nil {
		t.Error("bench-compare should reject a file that does not exist rather than compare nothing")
	}
}

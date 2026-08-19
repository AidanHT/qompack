package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// defaultBenchBaseline is the committed micro-benchmark baseline 00-ARCHITECTURE.md §7 compares
// against, and the default "old" side of a comparison.
const defaultBenchBaseline = "testdata/bench-baseline.txt"

// benchWarnPct and benchFailPct are 00-ARCHITECTURE.md §7's two thresholds, verbatim: "a >10%
// regression on any micro-benchmark posts a warning, a >25% regression fails". They are the whole
// content of the gate and are deliberately the only numbers in this file.
const (
	benchWarnPct = 10.0
	benchFailPct = 25.0
)

// benchUnitWorseWhenHigher classifies every benchmark unit this repository actually reports, so
// "worse" is a fact rather than an assumption about the sign of a delta.
//
// benchstat's `vs base` column is the raw percentage change of the metric and carries no opinion
// about direction, so a throughput benchmark that got 30% FASTER reports +30% in exactly the shape
// a 30% slowdown does. internal/paths' BenchmarkPathsWriteAtomic_4KB calls b.SetBytes and is
// reported in MB/s (normalised to B/s), so this is not a hypothetical inversion — reading the sign
// naively would gate improvements and wave regressions through on every b.SetBytes benchmark in
// the tree.
//
// A unit that is absent from this map is a hard failure rather than a guess in either direction.
// Guessing "lower is better" would silently invert the gate for a new throughput metric; guessing
// the row is unrankable would silently drop it. Both are the dead-gate failure mode §2.0 exists to
// catch, so the tool stops and asks for the unit to be classified here.
var benchUnitWorseWhenHigher = map[string]bool{
	"sec/op":    true,
	"ns/op":     true,
	"B/op":      true,
	"allocs/op": true,
	"B/s":       false,
	"MB/s":      false,
}

// benchDelta is one benchstat comparison cell: a single benchmark, in a single unit, with the
// change expressed so that a POSITIVE pct always means worse regardless of the unit's direction.
type benchDelta struct {
	pkg  string
	name string
	unit string
	pct  float64
}

// String renders a delta the way the job log should read it, signed in the "worse is positive"
// convention rather than benchstat's raw one.
func (d benchDelta) String() string {
	return fmt.Sprintf("%+.1f%% worse  %s  %s [%s]", d.pct, d.name, d.unit, d.pkg)
}

// taskBenchCompare is `devtool bench-compare [old] [new]`: it runs the pinned benchstat over two
// `go test -bench` output files and applies 00-ARCHITECTURE.md §7's regression rule to the result.
//
// This task exists because §7's rule had no executable form anywhere in the repository. benchstat
// was pinned in tools/pinned/go.mod, blank-imported by tools/pinned/tools.go, and named by
// benchstatPkg in tools/devtool/util.go — a constant with zero callers. No devtool task and no CI
// job ran it, and bench-gate runs bench-hotpath only. That is worse than a missing tool: the pin
// and the constant both read as though the gate is wired, so four subplans in a row recorded
// baseline rows against a comparison nobody ever performed (V2-VERIFY §2.3a item 3).
//
// Producing the "new" file is deliberately NOT this task's job — `devtool bench` already does it,
// and folding a multi-minute benchmark run into the comparator would make the gate too expensive
// to run at the moment it is useful:
//
//	go run ./tools/devtool bench > v2-bench.txt
//	go run ./tools/devtool bench-compare v2-bench.txt
//
// A benchmark benchstat reports as "~" is not gated. That is benchstat's own significance test
// saying the two distributions are not distinguishable at the configured confidence, and §7's
// percentages have no meaning across a difference that is noise. Nothing is silently dropped: the
// comparison count is printed, and benchstat's stderr — which is where it names a benchmark
// present in one file and missing from the other — is passed through verbatim.
func taskBenchCompare(args []string) error {
	oldPath, newPath, err := benchComparePaths(args)
	if err != nil {
		return err
	}

	stdout, stderr, runErr := pinnedCapture(benchstatPkg, "-format", "csv", oldPath, newPath)
	if len(stderr) > 0 {
		fmt.Print(string(stderr))
		if !strings.HasSuffix(string(stderr), "\n") {
			fmt.Println()
		}
	}
	if runErr != nil {
		return fmt.Errorf("bench-compare: benchstat %s %s: %w", oldPath, newPath, runErr)
	}

	cmp, err := parseBenchstatCSV(strings.NewReader(string(stdout)))
	if err != nil {
		return fmt.Errorf("bench-compare: %w", err)
	}
	if len(cmp.unknownUnits) > 0 {
		return fmt.Errorf(
			"bench-compare: benchstat reported unit(s) %s, which tools/devtool/benchcompare.go's "+
				"benchUnitWorseWhenHigher does not classify. Add each one with its direction — a "+
				"guess would invert or disable §7's gate for every benchmark reporting it",
			strings.Join(cmp.unknownUnits, ", "))
	}
	if cmp.rows == 0 {
		return fmt.Errorf(
			"bench-compare: benchstat produced no comparable rows from %s and %s. Either the two "+
				"files share no benchmark names, or one of them is not `go test -bench` output; "+
				"an empty comparison is not a pass (00-ARCHITECTURE.md §7)", oldPath, newPath)
	}

	var warnings, failures []benchDelta
	for _, d := range cmp.deltas {
		switch {
		case d.pct > benchFailPct:
			failures = append(failures, d)
		case d.pct > benchWarnPct:
			warnings = append(warnings, d)
		}
	}
	sortBenchDeltasWorstFirst(warnings)
	sortBenchDeltasWorstFirst(failures)

	fmt.Printf("bench-compare: %s -> %s, %d measurement(s) compared, %d with a significant change\n",
		oldPath, newPath, cmp.rows, len(cmp.deltas))

	for _, d := range warnings {
		fmt.Printf("  WARN  (>%.0f%%, 00-ARCHITECTURE.md §7) %s\n", benchWarnPct, d)
	}
	for _, d := range failures {
		fmt.Printf("  FAIL  (>%.0f%%, 00-ARCHITECTURE.md §7) %s\n", benchFailPct, d)
	}

	if len(failures) > 0 {
		return fmt.Errorf(
			"bench-compare: %d measurement(s) more than %.0f%% worse than %s, plus %d in the %.0f-%.0f%% warning band",
			len(failures), benchFailPct, oldPath, len(warnings), benchWarnPct, benchFailPct)
	}
	if len(warnings) > 0 {
		// Exit 0, loudly. §7 makes the 10% band a warning that must be EXPLAINED in the completion
		// report rather than a build failure, so the job stays green and the reader is told why.
		fmt.Printf("bench-compare: %d warning(s) over %.0f%% and none over %.0f%%; explain each in the report\n",
			len(warnings), benchWarnPct, benchFailPct)
		return nil
	}
	fmt.Printf("bench-compare: OK, nothing more than %.0f%% worse than %s\n", benchWarnPct, oldPath)
	return nil
}

// benchComparePaths resolves the task's positional arguments to two readable files. With one
// argument the old side defaults to the committed baseline, which is the common case; with two,
// both sides are explicit, which is what makes the task usable for comparing any two runs.
func benchComparePaths(args []string) (oldPath, newPath string, err error) {
	switch len(args) {
	case 1:
		oldPath, newPath = defaultBenchBaseline, args[0]
	case 2:
		oldPath, newPath = args[0], args[1]
	default:
		return "", "", errors.Join(errUsage, errors.New(
			"usage: devtool bench-compare [<old>] <new>   (old defaults to "+defaultBenchBaseline+")"))
	}
	for _, p := range []string{oldPath, newPath} {
		if _, statErr := os.Stat(filepath.Join(root, p)); statErr != nil {
			return "", "", fmt.Errorf("bench-compare: %s: %w", p, statErr)
		}
	}
	return oldPath, newPath, nil
}

// vsBaseColumn is the header cell benchstat's CSV writes over its comparison column.
const vsBaseColumn = "vs base"

// benchComparison is what one benchstat run yields.
//
// rows counts every measurement benchstat actually compared, "~" included, and deltas holds only
// the subset that moved significantly. Keeping them apart is what separates "the two runs agree"
// from "these two files have no benchmark in common": the first is a pass with an empty deltas and
// a non-zero rows, the second is a dead gate reporting green and must fail.
type benchComparison struct {
	deltas       []benchDelta
	rows         int
	unknownUnits []string
}

// parseBenchstatCSV reads `benchstat -format csv` output into a benchComparison.
//
// The format is CSV tables separated by bare configuration lines, so the parser is a small state
// machine rather than a single csv.ReadAll:
//
//	pkg: github.com/qompack/qompack/internal/sketch   <- 1 field, sets the current package
//	,old.txt#0,,new.txt#1,,,                          <- file header, ignored
//	,sec/op,CI,sec/op,CI,vs base,P                    <- unit header, locates the delta column
//	BloomAdd-22,1.67e-07,3%,1.9e-07,4%,+13.77%,p=0.000 n=10
//	geomean,...                                       <- a summary, not a benchmark
//
// FieldsPerRecord is -1 because the configuration lines are genuinely one field wide and a table's
// rows are seven; treating that as a malformed file would reject benchstat's normal output.
func parseBenchstatCSV(r io.Reader) (benchComparison, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	var (
		out      benchComparison
		pkg      = "(unknown package)"
		unit     string
		deltaCol = -1
		unknown  = map[string]bool{}
	)
	for {
		rec, readErr := cr.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return benchComparison{}, fmt.Errorf("parsing benchstat CSV output: %w", readErr)
		}

		// A configuration line ("goos: linux", "pkg: …", "cpu: …"). It ends the table above it.
		if len(rec) == 1 {
			if after, found := strings.CutPrefix(rec[0], "pkg: "); found {
				pkg = strings.TrimSpace(after)
			}
			unit, deltaCol = "", -1
			continue
		}

		// A header row: benchstat leaves the name column empty on both of them. The one naming the
		// comparison column also names the unit, in the column right after the (empty) name.
		if rec[0] == "" {
			if col := indexOf(rec, vsBaseColumn); col >= 0 && len(rec) > 1 {
				unit, deltaCol = strings.TrimSpace(rec[1]), col
			}
			continue
		}

		if unit == "" || deltaCol < 0 || deltaCol >= len(rec) || rec[0] == "geomean" {
			continue
		}
		out.rows++

		worseWhenHigher, known := benchUnitWorseWhenHigher[unit]
		if !known {
			unknown[unit] = true
			continue
		}
		pct, ok := parseBenchstatDelta(rec[deltaCol])
		if !ok {
			continue // "~": benchstat cannot distinguish the two distributions
		}
		if !worseWhenHigher {
			pct = -pct
		}
		out.deltas = append(out.deltas, benchDelta{pkg: pkg, name: rec[0], unit: unit, pct: pct})
	}

	for u := range unknown {
		out.unknownUnits = append(out.unknownUnits, u)
	}
	sort.Strings(out.unknownUnits)
	return out, nil
}

// parseBenchstatDelta reads one `vs base` cell. It reports ok == false for "~" (benchstat's marker
// for a difference indistinguishable from noise) and for anything else it cannot read as a signed
// percentage, which is the conservative answer: a cell this does not understand is left out of the
// gate and shows up in the printed comparison count instead of being silently scored as zero.
func parseBenchstatDelta(cell string) (pct float64, ok bool) {
	s := strings.TrimSpace(cell)
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimPrefix(s, "+")
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// indexOf returns the position of want in rec, or -1.
func indexOf(rec []string, want string) int {
	for i, f := range rec {
		if strings.TrimSpace(f) == want {
			return i
		}
	}
	return -1
}

// sortBenchDeltasWorstFirst orders a report list by severity, then by name and unit so the output
// is deterministic across runs on the same data.
func sortBenchDeltasWorstFirst(ds []benchDelta) {
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].pct != ds[j].pct {
			return ds[i].pct > ds[j].pct
		}
		if ds[i].name != ds[j].name {
			return ds[i].name < ds[j].name
		}
		return ds[i].unit < ds[j].unit
	})
}

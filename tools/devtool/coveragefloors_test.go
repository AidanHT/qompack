package main

import (
	"strings"
	"testing"
)

// The fixtures here are the real shapes, copied from the documents that carried them: the prose
// checklist item V2-SP-03 uses, the two-package item V4-SP-10 uses, and 00-ARCHITECTURE.md §6.4's
// group table. The pins numbers are the pre-fix ones on purpose — 90 in the plan against 75 in
// OWNERS.tsv is the divergence this check exists to have caught.

func TestParsePlanCoverageFloors_ProseChecklistItem(t *testing.T) {
	doc := "- [ ] Line coverage for " + bt + "internal/sketch" + bt + " ≥ **90 %** (00-ARCHITECTURE §6.4 floor).\n"
	got := parsePlanCoverageFloors("plans/X.md", doc)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1: %+v", len(got), got)
	}
	if got[0].pkg != "sketch" || got[0].pct != 90 || got[0].line != 1 {
		t.Errorf("claim = %+v, want sketch/90 on line 1", got[0])
	}
}

func TestParsePlanCoverageFloors_TwoPackagesOnOneLine(t *testing.T) {
	doc := "- [ ] Line coverage for " + bt + "internal/checkpoint" + bt + " ≥ **90%** and " +
		bt + "internal/pins" + bt + " ≥ **90%** (§6.4 puts " + bt + "checkpoint" + bt + " in the 90% group).\n"
	got := parsePlanCoverageFloors("plans/V4-SP-10.md", doc)
	if len(got) != 2 {
		t.Fatalf("got %d claims, want 2: %+v", len(got), got)
	}
	if got[0].pkg != "checkpoint" || got[0].pct != 90 {
		t.Errorf("first claim = %+v, want checkpoint/90", got[0])
	}
	if got[1].pkg != "pins" || got[1].pct != 90 {
		t.Errorf("second claim = %+v, want pins/90", got[1])
	}
}

func TestParsePlanCoverageFloors_CommaSeparatedRunSharesOneFloor(t *testing.T) {
	doc := "| " + bt + "internal/ipc" + bt + ", " + bt + "internal/daemon" + bt + ", " +
		bt + "internal/contract" + bt + " each ≥ **75 %** |\n"
	got := parsePlanCoverageFloors("plans/X.md", doc)
	if len(got) != 3 {
		t.Fatalf("got %d claims, want one per package in the run: %+v", len(got), got)
	}
	for _, c := range got {
		if c.pct != 75 {
			t.Errorf("%s claimed %d%%, want the run's shared 75%%", c.pkg, c.pct)
		}
	}
}

func TestParsePlanCoverageFloors_ReadsTheArchitectureGroupTable(t *testing.T) {
	doc := "| Package group | Line coverage floor |\n" +
		"|---|---|\n" +
		"| " + bt + "config" + bt + ", " + bt + "store" + bt + ", " + bt + "pins" + bt + " | **90%** |\n" +
		"| " + bt + "dag" + bt + ", " + bt + "eval" + bt + " | **85%** |\n"
	got := parsePlanCoverageFloors("plans/00-ARCHITECTURE.md", doc)
	if len(got) != 5 {
		t.Fatalf("got %d claims, want 5 (three in the 90 row, two in the 85 row): %+v", len(got), got)
	}
	want := map[string]int{"config": 90, "store": 90, "pins": 90, "dag": 85, "eval": 85}
	for _, c := range got {
		if want[c.pkg] != c.pct {
			t.Errorf("%s claimed %d%%, want %d%%", c.pkg, c.pct, want[c.pkg])
		}
	}
}

func TestParsePlanCoverageFloors_IgnoresRowsThatAreNotFloors(t *testing.T) {
	// A measured result, a latency budget and an ordinary two-column table all live in these
	// documents in quantity. None of them is a coverage-floor claim, and reading one as a claim
	// would fail a correct document.
	doc := "| V2-SP02-20 | coverage ≥ 85 % | PASS | 90.9 % |\n" +
		"Measured at the tip: " + bt + "eval" + bt + " 90.9 % ≥ 85.\n" +
		"| " + bt + "store" + bt + " — Put cold | **3 ms** |\n" +
		"L0 hook budget is **50%** of the frame.\n"
	if got := parsePlanCoverageFloors("plans/X.md", doc); len(got) != 0 {
		t.Errorf("got %d claims from lines that assert no floor: %+v", len(got), got)
	}
}

func TestCoverageFloorProblems_CatchesThePinsDivergence(t *testing.T) {
	// plans/OWNERS.tsv said 75; five plan sites said 90. `devtool cover` reads OWNERS.tsv, so every
	// one of those checklist items could be ticked by a package sitting at 75.1 %.
	claims := []coverageFloorClaim{
		{file: "plans/V4-SP-10-checkpointer-l4.md", line: 1523, pkg: "checkpoint", pct: 90},
		{file: "plans/V4-SP-10-checkpointer-l4.md", line: 1523, pkg: "pins", pct: 90},
	}
	floorOf := map[string]int{"checkpoint": 90, "pins": 75}

	problems := coverageFloorProblems(claims, floorOf)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want exactly the pins one: %v", len(problems), problems)
	}
	for _, want := range []string{"V4-SP-10-checkpointer-l4.md:1523", "`pins`", "90%", "75%"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q does not name %q", problems[0], want)
		}
	}
}

func TestCoverageFloorProblems_UnknownPackageIsItsOwnFailure(t *testing.T) {
	// §6.4: "a plan that asserts a floor OWNERS.tsv does not carry asserts nothing."
	problems := coverageFloorProblems(
		[]coverageFloorClaim{{file: "plans/X.md", line: 7, pkg: "nosuchpkg", pct: 90}},
		map[string]int{"store": 90})
	if len(problems) != 1 || !strings.Contains(problems[0], "no row for that package") {
		t.Errorf("problems = %v, want one naming the missing OWNERS.tsv row", problems)
	}
}

func TestCoverageFloorProblems_AgreementIsSilent(t *testing.T) {
	claims := []coverageFloorClaim{
		{file: "plans/X.md", line: 1, pkg: "sketch", pct: 90},
		{file: "plans/X.md", line: 2, pkg: "eval", pct: 85},
	}
	if problems := coverageFloorProblems(claims, map[string]int{"sketch": 90, "eval": 85}); len(problems) != 0 {
		t.Errorf("problems = %v, want none when every claim matches OWNERS.tsv", problems)
	}
}

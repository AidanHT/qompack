package main

import (
	"strings"
	"testing"
)

// TestClassifySkips_AcceptsCompliantMessages asserts that a Rule-W1 skip in a package owned by
// someone other than SP-01 produces zero problems, and that a reasoned platform skip is accepted
// as a notice rather than a hard failure.
func TestClassifySkips_AcceptsCompliantMessages(t *testing.T) {
	owners := []ownerRow{
		{Package: "core", Owner: "SP-01", Floor: 75, Probe: "-"},
		{Package: "store", Owner: "SP-06", Floor: 90, Probe: "PutBytes"},
	}
	events := []testEvent{
		{
			Action: "output", Package: modulePath + "/internal/store/storetest", Test: "TestStoreSuite/behaviour",
			Output: "    store_test.go:10: " + ruleW1Msg + "\n",
		},
		{Action: "skip", Package: modulePath + "/internal/store/storetest", Test: "TestStoreSuite/behaviour"},

		{
			Action: "output", Package: modulePath + "/internal/core", Test: "TestSomething",
			Output: "    x_test.go:3: " + platformPrefix + "the \\\\?\\ prefix form is windows-specific\n",
		},
		{Action: "skip", Package: modulePath + "/internal/core", Test: "TestSomething"},
	}

	problems, notices, counts := classifySkips(events, owners)

	if counts[modulePath+"/internal/store/storetest"] != 1 || counts[modulePath+"/internal/core"] != 1 {
		t.Fatalf("unexpected counts: %v", counts)
	}
	if len(problems) != 0 {
		t.Fatalf("want zero hard problems, got %d: %v", len(problems), problems)
	}
	if len(notices) != 1 {
		t.Fatalf("want exactly one notice (the platform skip in core), got %d: %v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "internal/core") || !strings.Contains(notices[0], "platform-gated") {
		t.Fatalf("unexpected notice: %v", notices[0])
	}
}

// TestClassifySkips_RejectsUnclassifiedReason is the Definition-of-Done item 5 gate: a skip whose
// reason matches none of the three permitted messages is a hard failure, not a notice. This is
// what stops a skipped test from quietly standing in for missing work.
func TestClassifySkips_RejectsUnclassifiedReason(t *testing.T) {
	owners := []ownerRow{{Package: "core", Owner: "SP-01", Floor: 75, Probe: "-"}}
	events := []testEvent{
		{
			Action: "output", Package: modulePath + "/internal/core", Test: "TestSomething",
			Output: "    x_test.go:3: skipped for an unrelated reason\n",
		},
		{Action: "skip", Package: modulePath + "/internal/core", Test: "TestSomething"},
	}

	problems, _, _ := classifySkips(events, owners)
	if len(problems) != 1 {
		t.Fatalf("want exactly one problem for an unclassified skip reason, got %d: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "none of the three permitted messages") {
		t.Fatalf("unexpected problem text: %v", problems[0])
	}
}

// TestClassifySkips_RejectsBarePlatformPrefix asserts the reason after "platform: " is mandatory.
// A bare prefix would let any skip launder itself through the platform category.
func TestClassifySkips_RejectsBarePlatformPrefix(t *testing.T) {
	owners := []ownerRow{{Package: "core", Owner: "SP-01", Floor: 75, Probe: "-"}}
	events := []testEvent{
		{
			Action: "output", Package: modulePath + "/internal/core", Test: "TestBare",
			Output: "    x_test.go:3: " + platformPrefix + "   \n",
		},
		{Action: "skip", Package: modulePath + "/internal/core", Test: "TestBare"},
	}

	problems, _, _ := classifySkips(events, owners)
	if len(problems) != 1 {
		t.Fatalf("a bare platform prefix must be rejected, got %d problems: %v", len(problems), problems)
	}
}

// TestClassifySkips_BlocksW1ForSP01OwnedPackage asserts that a Rule-W-1 skip inside a package
// plans/OWNERS.tsv assigns to SP-01 is a problem (SP-01 must ship the real behaviour, not a
// stub), while the identical skip in a package owned by a different subplan is not.
func TestClassifySkips_BlocksW1ForSP01OwnedPackage(t *testing.T) {
	owners := []ownerRow{
		{Package: "core", Owner: "SP-01", Floor: 75, Probe: "-"},
		{Package: "store", Owner: "SP-06", Floor: 90, Probe: "PutBytes"},
	}
	events := []testEvent{
		{
			Action: "output", Package: modulePath + "/internal/core/coretest", Test: "TestCoreSuite/behaviour",
			Output: "    core_test.go:5: " + ruleW1Msg + "\n",
		},
		{Action: "skip", Package: modulePath + "/internal/core/coretest", Test: "TestCoreSuite/behaviour"},

		{
			Action: "output", Package: modulePath + "/internal/store/storetest", Test: "TestStoreSuite/behaviour",
			Output: "    store_test.go:9: " + ruleW1Msg + "\n",
		},
		{Action: "skip", Package: modulePath + "/internal/store/storetest", Test: "TestStoreSuite/behaviour"},
	}

	problems, _, _ := classifySkips(events, owners)
	if len(problems) != 1 {
		t.Fatalf("want exactly one problem (core is SP-01-owned), got %d: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "internal/core/coretest") {
		t.Fatalf("problem should name the SP-01-owned package: %v", problems[0])
	}
}

// TestClassifySkips_AcceptsRuleW2 asserts a Rule-W-2 skip (contract fixture not yet recorded) is
// never itself a problem, regardless of which package it's in.
func TestClassifySkips_AcceptsRuleW2(t *testing.T) {
	owners := []ownerRow{{Package: "core", Owner: "SP-01", Floor: 75, Probe: "-"}}
	events := []testEvent{
		{
			Action: "output", Package: modulePath + "/internal/core", Test: "TestUsesFixture",
			Output: "    x_test.go:1: " + ruleW2Msg + "\n",
		},
		{Action: "skip", Package: modulePath + "/internal/core", Test: "TestUsesFixture"},
	}
	problems, notices, counts := classifySkips(events, owners)
	if len(problems) != 0 {
		t.Fatalf("want zero problems for a Rule-W2 skip, got: %v", problems)
	}
	if len(notices) != 0 {
		t.Fatalf("want zero notices for a Rule-W2 skip, got: %v", notices)
	}
	if counts[modulePath+"/internal/core"] != 1 {
		t.Fatalf("unexpected counts: %v", counts)
	}
}

// TestTimedOutPackages_ReportsAKilledBinary asserts that a package whose test binary was killed
// for running past -timeout is reported. This is the case stubskips cannot treat like any other
// red suite: the events after the kill never arrive, so the skips they would have carried are
// absent, and an absent skip is indistinguishable from a compliant one.
func TestTimedOutPackages_ReportsAKilledBinary(t *testing.T) {
	events := []testEvent{
		{Action: "output", Package: modulePath + "/test/integration", Test: "TestHotPath",
			Output: "panic: test timed out after 30m0s\n"},
		{Action: "output", Package: modulePath + "/internal/store", Test: "TestPut",
			Output: "*** Test killed with quit: ran too long\n"},
		{Action: "output", Package: modulePath + "/internal/core", Test: "TestFine",
			Output: "ok\n"},
	}

	got := timedOutPackages(events)
	want := []string{modulePath + "/internal/store", modulePath + "/test/integration"}
	if len(got) != len(want) {
		t.Fatalf("want %d timed-out package(s) %v, got %d: %v", len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("timedOutPackages must be sorted and deduplicated; want %v, got %v", want, got)
		}
	}
}

// TestTimedOutPackages_IgnoresAnOrdinarilyFailingSuite is the other half of the contract: a red
// test is NOT a timeout. stubskips deliberately ignores the exit status because the `test` task
// already reports failures, and a failing test still reports every skip around it — so the timeout
// detector must not quietly turn stubskips into a second test gate.
func TestTimedOutPackages_IgnoresAnOrdinarilyFailingSuite(t *testing.T) {
	events := []testEvent{
		{Action: "output", Package: modulePath + "/internal/core", Test: "TestBoom",
			Output: "    x_test.go:9: expected 3, got 4\n"},
		{Action: "output", Package: modulePath + "/internal/core", Test: "TestBoom",
			Output: "--- FAIL: TestBoom (0.00s)\n"},
		{Action: "output", Package: modulePath + "/internal/core", Test: "",
			Output: "FAIL\tgithub.com/qompack/qompack/internal/core\t0.01s\n"},
		{Action: "fail", Package: modulePath + "/internal/core", Test: "TestBoom"},
	}

	if got := timedOutPackages(events); len(got) != 0 {
		t.Fatalf("a failing suite is not a timeout; want none, got %v", got)
	}
}

func TestParseTestEvents(t *testing.T) {
	stream := `{"Action":"run","Package":"p","Test":"T"}
{"Action":"output","Package":"p","Test":"T","Output":"line1\n"}
{"Action":"skip","Package":"p","Test":"T"}
`
	events, err := parseTestEvents([]byte(stream))
	if err != nil {
		t.Fatalf("parseTestEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d: %v", len(events), events)
	}
	if events[2].Action != "skip" || events[2].Test != "T" {
		t.Fatalf("unexpected last event: %+v", events[2])
	}
}

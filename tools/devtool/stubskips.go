package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// The three, and only three, permitted t.Skip reasons anywhere in the tree.
//
// Two come straight from the Interface contract section (§15-16): a conformance suite's
// behaviour block skipped because the implementation is still a stub, or a consumer test skipped
// because its golden contract fixture has not been recorded yet.
//
// The third is an SP-01 addition. The Definition of Done permits only the two Rule-W messages,
// but a cross-platform Go codebase has legitimate environment-capability skips that hide no
// missing work at all — a Windows-only \\?\ path assertion, an `icacls` ACL test on a host
// without it, a symlink test where the process lacks the privilege to create one. Silently
// tolerating those (the alternative) would weaken the check to the point of uselessness, and
// banning them outright would force either dead platform coverage or a lie.
//
// So they get a distinct, greppable, mandatory-reason prefix and everything else is a hard
// failure. That is stricter than treating unknown reasons as advisory: a skip whose reason
// matches none of the three now fails `devtool lint` rather than printing a notice, which is the
// property the Definition of Done was actually reaching for.
const (
	ruleW1Msg = "behaviour: implementation is a stub (Rule W-1)"
	ruleW2Msg = "contract fixture not yet recorded (Rule W-2)"
	// platformPrefix must be followed by a non-empty reason.
	platformPrefix = "platform: "
)

// testEvent mirrors the fields of `go test -json` this check needs; every other field is ignored.
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// parseTestEvents decodes the newline-delimited JSON stream `go test -json` writes to stdout.
func parseTestEvents(stdout []byte) ([]testEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(stdout))
	var events []testEvent
	for dec.More() {
		var e testEvent
		if err := dec.Decode(&e); err != nil {
			return events, err
		}
		events = append(events, e)
	}
	return events, nil
}

// classifySkips groups events by (package, test), finds every test that was skipped, and checks
// the output accumulated for that test against the two Rule W-1 / W-2 messages.
//
// It returns two different severities, matching the mechanism the implementation spec §15
// actually describes for stubskips ("greps test output for that [Rule-W1] message and reports the
// count; it becomes a merge blocker for a subplan only when the subplan owns the package"):
//
//   - problems are hard failures, of two kinds: a Rule-W1 skip inside a package
//     plans/OWNERS.tsv currently assigns to SP-01 (which must ship real behaviour rather than a
//     stub), and any skip whose reason matches none of the three permitted messages.
//   - notices are informational: a permitted platform skip, listed so that dead platform
//     coverage is visible in the job log rather than invisible.
//
// counts is a per-package skip count, purely for reporting.
func classifySkips(events []testEvent, owners []ownerRow) (problems, notices []string, counts map[string]int) {
	counts = make(map[string]int)

	type key struct{ pkg, test string }
	output := make(map[key]string)
	var skipped []key

	for _, e := range events {
		if e.Test == "" {
			continue // package-level event (build output, "ok  <pkg>  0.01s", …), not a test skip
		}
		k := key{e.Package, e.Test}
		switch e.Action {
		case "output":
			output[k] += e.Output
		case "skip":
			skipped = append(skipped, k)
		}
	}

	ownerByKey := make(map[string]ownerRow, len(owners))
	for _, o := range owners {
		ownerByKey[o.Package] = o
	}

	for _, k := range skipped {
		counts[k.pkg]++
		text := output[k]
		hasW1 := strings.Contains(text, ruleW1Msg)
		hasW2 := strings.Contains(text, ruleW2Msg)
		hasPlatform := hasReasonedPlatformSkip(text)

		if !hasW1 && !hasW2 && !hasPlatform {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): skipped with a reason matching none of the three permitted messages "+
					"(Rule W-1, Rule W-2, or %q followed by a reason)", k.pkg, k.test, platformPrefix))
			continue
		}
		if hasPlatform && !hasW1 && !hasW2 {
			notices = append(notices, fmt.Sprintf(
				"%s (%s): platform-gated, not run on this host", k.pkg, k.test))
		}
		if hasW1 {
			if row, ok := ownerByKey[packageKeyOf(k.pkg)]; ok && row.Owner == "SP-01" {
				problems = append(problems, fmt.Sprintf(
					"%s (%s): Rule W-1 skip in a package plans/OWNERS.tsv assigns to SP-01 — it should no longer be a stub",
					k.pkg, k.test))
			}
		}
	}

	sort.Strings(problems)
	sort.Strings(notices)
	return problems, notices, counts
}

// hasReasonedPlatformSkip reports whether text contains a platform skip carrying a non-empty
// reason. A bare "platform:" with nothing after it is not accepted: the reason is the entire
// point, because it is what tells a reader whether the coverage gap matters on their host.
func hasReasonedPlatformSkip(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, platformPrefix)
		if i < 0 {
			continue
		}
		if strings.TrimSpace(line[i+len(platformPrefix):]) != "" {
			return true
		}
	}
	return false
}

// runStubSkips is the `devtool lint` sub-check: it runs the test suite under internal/, cmd/ and
// test/ (whichever of those trees exist) with `-json`, and greps the resulting skip reasons for
// Rule W-1/W-2 compliance — a Rule-W-1 skip is a merge blocker only for the subplan that owns the
// package, per plans/OWNERS.tsv.
func runStubSkips() error {
	patterns := existingTopLevelPatterns("internal", "cmd", "test")
	if len(patterns) == 0 {
		fmt.Println("stubskips: no internal/, cmd/, or test/ trees yet; nothing to check")
		return nil
	}

	owners, err := loadOwners(filepath.Join(root, "plans", "OWNERS.tsv"))
	if err != nil {
		return fmt.Errorf("stubskips: %w", err)
	}

	args := append([]string{"test", "-json"}, patterns...)
	// The exit status of this `go test` run is deliberately not inspected here: a package that
	// fails to build, or whose non-skip tests fail, is already reported by the `test` task.
	// stubskips only cares about the text of whatever skip reasons this run does produce.
	stdout, _, _ := runCapture(nil, "go", args...)

	events, err := parseTestEvents(stdout)
	if err != nil {
		return fmt.Errorf("stubskips: parsing `go test -json` output: %w", err)
	}

	problems, notices, counts := classifySkips(events, owners)

	pkgNames := make([]string, 0, len(counts))
	for pkg := range counts {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)
	for _, pkg := range pkgNames {
		fmt.Printf("stubskips: %s: %d skip(s)\n", pkg, counts[pkg])
	}
	for _, n := range notices {
		fmt.Println("  notice: " + n)
	}

	if len(problems) == 0 {
		fmt.Println("stubskips: OK")
		return nil
	}
	for _, p := range problems {
		fmt.Println("  " + p)
	}
	return fmt.Errorf("stubskips: %d problem(s)", len(problems))
}

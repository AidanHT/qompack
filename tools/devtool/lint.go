package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// lintSubcheck is one step of `devtool lint`.
type lintSubcheck struct {
	name string
	run  func() error
}

// lintSubchecks runs in exactly this order (implementation spec §3): golangci-lint, then the four
// in-repo checks against the import/dependency graph, then the two source-scanning checks.
var lintSubchecks = []lintSubcheck{
	{"golangci-lint", runGolangciLintCheck},
	{"nomagic", runNomagicCheck},
	{"importgraph", runImportGraph},
	{"testdeps", runTestDeps},
	{"bindeps", runBinDeps},
	{"sleepcheck", runSleepCheck},
	{"stubskips", runStubSkips},
}

// runGolangciLintCheck runs the pinned golangci-lint with the committed .golangci.yml.
func runGolangciLintCheck() error {
	return pinnedRunInherit(golangciLintPkg, "run", "./...")
}

// runNomagicCheck runs the in-repo nomagic analysis pass over the whole module.
func runNomagicCheck() error {
	return goInherit("run", "./tools/lint/nomagic", "./...")
}

// taskLint runs every lint sub-check in order and fails if any of them fails. --only=a,b,c
// restricts the run to a comma-separated subset of sub-check names (the security CI job uses this
// to run just importgraph, testdeps and bindeps).
func taskLint(args []string) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	only := fs.String("only", "", "comma-separated list of sub-checks to run (default: all)")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}

	var wanted map[string]bool
	if *only != "" {
		wanted = make(map[string]bool)
		for _, name := range strings.Split(*only, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			wanted[name] = true
		}
		if err := validateOnlyNames(wanted); err != nil {
			return errors.Join(errUsage, err)
		}
	}

	var failed []string
	for _, sc := range lintSubchecks {
		if wanted != nil && !wanted[sc.name] {
			continue
		}
		fmt.Printf("== devtool lint: %s ==\n", sc.name)
		if err := sc.run(); err != nil {
			fmt.Printf("FAIL %s: %v\n", sc.name, err)
			failed = append(failed, sc.name)
			continue
		}
		fmt.Printf("PASS %s\n", sc.name)
	}

	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("lint: failed sub-check(s): %s", strings.Join(failed, ", "))
}

// validateOnlyNames rejects an --only value naming a sub-check that does not exist, so a typo
// fails loudly instead of silently running nothing.
func validateOnlyNames(wanted map[string]bool) error {
	known := make(map[string]bool, len(lintSubchecks))
	for _, sc := range lintSubchecks {
		known[sc.name] = true
	}
	for name := range wanted {
		if !known[name] {
			return fmt.Errorf("--only: unknown sub-check %q", name)
		}
	}
	return nil
}

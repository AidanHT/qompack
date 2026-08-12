package main

import (
	"fmt"
	"sort"
	"strings"
)

// testOnlyModules are the "test-only" dependencies of 00-ARCHITECTURE.md §2.5: production
// (non-_test.go) source may never import them.
var testOnlyModules = []string{
	"github.com/stretchr/testify",
	"github.com/google/go-cmp",
	"pgregory.net/rapid",
}

// checkTestDeps applies the §2.5 rule to pkgs' direct, non-test imports (`.Imports` — go list's
// own definition of that field already excludes anything only referenced from a _test.go file)
// and returns one violation string per offending import, sorted for deterministic output.
func checkTestDeps(pkgs []pkgInfo) []string {
	var violations []string
	for _, p := range pkgs {
		if testDepsExempt(p.ImportPath) {
			continue
		}
		for _, imp := range p.Imports {
			for _, forbidden := range testOnlyModules {
				if imp == forbidden || strings.HasPrefix(imp, forbidden+"/") {
					violations = append(violations, fmt.Sprintf(
						"%s imports test-only dependency %s outside a _test.go file (00-ARCHITECTURE.md §2.5)",
						p.ImportPath, imp))
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

// testDepsExempt reports whether importPath legitimately exports test helpers and so may import
// the test-only modules from non-_test.go source: internal/testutil, every internal/<pkg>/<pkg>test
// conformance subpackage, and anything under test/.
func testDepsExempt(importPath string) bool {
	rel := strings.TrimPrefix(importPath, modulePath+"/")
	if rel == importPath {
		return false
	}
	if rel == "internal/testutil" || strings.HasPrefix(rel, "internal/testutil/") {
		return true
	}
	if strings.HasPrefix(rel, "test/") {
		return true
	}
	if stripped, ok := strings.CutPrefix(rel, "internal/"); ok {
		parts := strings.Split(stripped, "/")
		if len(parts) == 2 && parts[1] == parts[0]+"test" {
			return true
		}
	}
	return false
}

// runTestDeps is the `devtool lint` sub-check for checkTestDeps, run over the whole root module
// (tools/pinned is a separate module and is never part of `./...`).
func runTestDeps() error {
	pkgs, err := goListJSON("./...")
	if err != nil {
		return fmt.Errorf("testdeps: %w", err)
	}
	violations := checkTestDeps(pkgs)
	if len(violations) == 0 {
		fmt.Printf("testdeps: OK (%d package(s) checked)\n", len(pkgs))
		return nil
	}
	for _, v := range violations {
		fmt.Println("  " + v)
	}
	return fmt.Errorf("testdeps: %d violation(s)", len(violations))
}

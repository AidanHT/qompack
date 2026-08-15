package main

import (
	"strings"
	"testing"
)

// TestImportGraph_RejectsViolation feeds checkImportGraph a synthetic package list containing
// store -> negknow, which 00-ARCHITECTURE.md §3.2 explicitly forbids ("store must not import
// negknow — hence ChangedSince takes []core.Dep, not []negknow.Dep"), and asserts it is rejected
// with a message naming both packages and §3.2.
func TestImportGraph_RejectsViolation(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/core"},
		{ImportPath: modulePath + "/internal/dag"},
		{ImportPath: modulePath + "/internal/sketch"},
		{ImportPath: modulePath + "/internal/store", Imports: []string{
			modulePath + "/internal/negknow",
		}},
		{ImportPath: modulePath + "/internal/negknow", Imports: []string{
			modulePath + "/internal/sketch",
			modulePath + "/internal/store",
			modulePath + "/internal/dag",
		}},
	}

	violations := checkImportGraph(pkgs)
	if len(violations) == 0 {
		t.Fatalf("expected at least one violation for store -> negknow, got none")
	}

	var found bool
	for _, v := range violations {
		if strings.Contains(v, modulePath+"/internal/store") &&
			strings.Contains(v, modulePath+"/internal/negknow") &&
			strings.Contains(v, "§3.2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a violation naming store, negknow and §3.2; got: %v", violations)
	}
}

// TestImportGraph_AcceptsCleanSyntheticGraph is a positive control for
// TestImportGraph_RejectsViolation: the same shape of package list, but with negknow's real
// (allowed) dependencies only, must report zero violations.
func TestImportGraph_AcceptsCleanSyntheticGraph(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/core"},
		{ImportPath: modulePath + "/internal/dag"},
		{ImportPath: modulePath + "/internal/sketch"},
		{ImportPath: modulePath + "/internal/store", Imports: []string{
			modulePath + "/internal/chunk",
			modulePath + "/internal/tokens",
			modulePath + "/internal/core",
		}},
		{ImportPath: modulePath + "/internal/chunk"},
		{ImportPath: modulePath + "/internal/tokens"},
		{ImportPath: modulePath + "/internal/negknow", Imports: []string{
			modulePath + "/internal/sketch",
			modulePath + "/internal/store",
			modulePath + "/internal/dag",
		}},
		// a conformance suite may import its own package, testutil and core (rule c).
		{ImportPath: modulePath + "/internal/store/storetest", Imports: []string{
			modulePath + "/internal/store",
			modulePath + "/internal/testutil",
			modulePath + "/internal/core",
		}},
		{ImportPath: modulePath + "/internal/testutil", Imports: []string{
			modulePath + "/internal/store",
		}},
	}

	violations := checkImportGraph(pkgs)
	if len(violations) != 0 {
		t.Fatalf("expected zero violations for a clean, §3.2-compliant graph, got: %v", violations)
	}
}

// TestImportGraph_RejectsUndeclaredPackage exercises rule (d): a package on disk absent from both
// `allow` and `compositionRoots` is itself an error.
func TestImportGraph_RejectsUndeclaredPackage(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/nonexistent"},
	}
	violations := checkImportGraph(pkgs)
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation for an undeclared package, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "nonexistent") || !strings.Contains(violations[0], "§3.2") {
		t.Fatalf("violation should name the package and §3.2: %v", violations[0])
	}
}

// TestImportGraph_RejectsImportOfCompositionRoot asserts that a normal (non-root) package
// importing a composition root is rejected, even though the root itself is a "recognized"
// package.
func TestImportGraph_RejectsImportOfCompositionRoot(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/store", Imports: []string{
			modulePath + "/internal/daemon",
		}},
		{ImportPath: modulePath + "/internal/daemon"},
	}
	violations := checkImportGraph(pkgs)
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "composition root") {
		t.Fatalf("violation should call out the composition root rule: %v", violations[0])
	}
}

// TestImportGraph_AcceptsRealRepo runs importgraph against the actual repository (whatever
// subset of internal/, cmd/, and test/ currently exists) and must pass. Before wave 1 lands, that
// subset is just internal/core, which is foundation with no imports — trivially clean.
func TestImportGraph_AcceptsRealRepo(t *testing.T) {
	oldRoot := root
	root = testModuleRoot(t)
	defer func() { root = oldRoot }()

	if err := runImportGraph(); err != nil {
		t.Fatalf("importgraph failed against the real repository: %v", err)
	}
}

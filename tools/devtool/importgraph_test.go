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

// TestImportGraph_RejectsCompositionRootInTestImports is the V2-MERGE-22 regression: an
// in-package _test.go file importing a composition root is a violation, and testutil gets no
// exception there. This is the exact shape that broke the wave-1 merge — internal/dag's in-package
// tests imported internal/testutil, and dag -> testutil -> cli -> daemon -> dag closed the moment
// SP-05 gave daemon a dag dependency.
func TestImportGraph_RejectsCompositionRootInTestImports(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/dag", TestImports: []string{
			modulePath + "/internal/testutil",
		}},
		{ImportPath: modulePath + "/internal/testutil"},
	}
	violations := checkImportGraph(pkgs)
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], ".TestImports") ||
		!strings.Contains(violations[0], "composition root") {
		t.Fatalf("violation should name .TestImports and the composition-root rule: %v", violations[0])
	}
}

// TestImportGraph_AcceptsTestutilInXTestImports pins the one carve-out: an x_test package is
// compiled separately from the package under test, so it cannot close a cycle back through it, and
// internal/testutil exists to be imported by tests. canon, chunk, eval, sketch and store all do
// this on the real tree, so a rule without this exception would fail five packages that are right.
func TestImportGraph_AcceptsTestutilInXTestImports(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/chunk", XTestImports: []string{
			modulePath + "/internal/testutil",
			modulePath + "/internal/core",
		}},
		{ImportPath: modulePath + "/internal/testutil"},
		{ImportPath: modulePath + "/internal/core"},
	}
	if violations := checkImportGraph(pkgs); len(violations) != 0 {
		t.Fatalf("x_test files may import internal/testutil; got: %v", violations)
	}
}

// TestImportGraph_RejectsOtherRootsInXTestImports is the negative control for the carve-out: it is
// testutil specifically, not composition roots in general. An x_test file importing internal/cli
// or internal/daemon is the same violation it would be in production source.
func TestImportGraph_RejectsOtherRootsInXTestImports(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/chunk", XTestImports: []string{
			modulePath + "/internal/daemon",
		}},
		{ImportPath: modulePath + "/internal/daemon"},
	}
	violations := checkImportGraph(pkgs)
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], ".XTestImports") ||
		!strings.Contains(violations[0], "composition root") {
		t.Fatalf("violation should name .XTestImports and the composition-root rule: %v", violations[0])
	}
}

// TestImportGraph_CompositionRootTestsMayImportAnything keeps the rule one-directional. A
// composition root may import anything, and that has to hold for its tests too: internal/cli names
// daemon, contract and ipc in its own .TestImports on the real tree.
func TestImportGraph_CompositionRootTestsMayImportAnything(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/cli", TestImports: []string{
			modulePath + "/internal/daemon",
			modulePath + "/internal/contract",
			modulePath + "/internal/ipc",
		}},
		{ImportPath: modulePath + "/internal/daemon"},
		{ImportPath: modulePath + "/internal/contract"},
		{ImportPath: modulePath + "/internal/ipc"},
	}
	if violations := checkImportGraph(pkgs); len(violations) != 0 {
		t.Fatalf("a composition root's tests may import anything; got: %v", violations)
	}
}

// TestImportGraph_TestImportsIgnoreTheLayerTable records a deliberate limit: only the
// composition-root half of §3.2 is applied to test imports. The layer table governs what a package
// IS, and a test legitimately reaches across it to build a fixture — internal/store's own tests
// import canon, chunk, redact, sketch, symbols and tokens, which its allow-set happens to permit,
// but internal/dag's import go/ast and internal/eval's import paths. Widening this to the full
// table is a separate decision with its own blast radius, not a free tightening.
func TestImportGraph_TestImportsIgnoreTheLayerTable(t *testing.T) {
	pkgs := []pkgInfo{
		// sketch's allow-set is {core, paths, config, logging, obs} — dag is not in it.
		{ImportPath: modulePath + "/internal/sketch", TestImports: []string{
			modulePath + "/internal/dag",
		}},
		{ImportPath: modulePath + "/internal/dag"},
	}
	if violations := checkImportGraph(pkgs); len(violations) != 0 {
		t.Fatalf("test imports are checked against composition roots only; got: %v", violations)
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

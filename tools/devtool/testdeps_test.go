package main

import (
	"strings"
	"testing"
)

// TestTestDeps_RejectsProductionTestify asserts that a production package importing
// stretchr/testify (outside a _test.go file) is rejected.
func TestTestDeps_RejectsProductionTestify(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/store", Imports: []string{
			"github.com/stretchr/testify/require",
		}},
	}
	violations := checkTestDeps(pkgs)
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "testify") {
		t.Fatalf("violation should name testify: %v", violations[0])
	}
}

// TestTestDeps_RejectsGoCmpAndRapidToo covers the other two test-only modules named in
// 00-ARCHITECTURE.md §2.5.
func TestTestDeps_RejectsGoCmpAndRapidToo(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/dag", Imports: []string{"github.com/google/go-cmp/cmp"}},
		{ImportPath: modulePath + "/internal/chunk", Imports: []string{"pgregory.net/rapid"}},
	}
	violations := checkTestDeps(pkgs)
	if len(violations) != 2 {
		t.Fatalf("want exactly two violations, got %d: %v", len(violations), violations)
	}
}

// TestTestDeps_ExemptsHelperPackages is the positive control: testutil, a <pkg>test conformance
// subpackage, and anything under test/ may import the test-only modules from non-_test.go source.
func TestTestDeps_ExemptsHelperPackages(t *testing.T) {
	pkgs := []pkgInfo{
		{ImportPath: modulePath + "/internal/testutil", Imports: []string{"github.com/stretchr/testify/require"}},
		{ImportPath: modulePath + "/internal/store/storetest", Imports: []string{"github.com/google/go-cmp/cmp"}},
		{ImportPath: modulePath + "/test/e2e", Imports: []string{"pgregory.net/rapid"}},
	}
	violations := checkTestDeps(pkgs)
	if len(violations) != 0 {
		t.Fatalf("want zero violations for exempt helper packages, got: %v", violations)
	}
}

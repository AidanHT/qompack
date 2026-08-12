package main

import "testing"

// testModuleRoot returns this module's root directory for tests that need the package-level
// `root` variable populated without going through main/run. Tests that use it must set `root`
// themselves (root = testModuleRoot(t)) and restore the previous value afterward, since `root` is
// shared, mutable package state and devtool's tests do not run in parallel.
func testModuleRoot(t *testing.T) string {
	t.Helper()
	r, err := findModuleRoot()
	if err != nil {
		t.Fatalf("findModuleRoot: %v", err)
	}
	return r
}

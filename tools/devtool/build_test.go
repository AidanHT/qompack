package main

import (
	"path/filepath"
	"testing"
)

// TestBuildOutputPath pins the host-platform output name across every GOOS the release matrix
// builds for. The bug this guards is narrow and was invisible on Linux and macOS: `build` wrote
// bin/qompack unconditionally, so on Windows it produced a file with no extension that the shell
// refuses to execute, and every step that then ran the binary failed complaining the command did
// not exist rather than pointing at the build.
func TestBuildOutputPath(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "qompack.exe",
		"linux":   "qompack",
		"darwin":  "qompack",
	} {
		if got, expect := buildOutputPath(goos), filepath.Join("bin", want); got != expect {
			t.Errorf("buildOutputPath(%q) = %q, want %q", goos, got, expect)
		}
	}
}

// TestBuildOutputPath_AgreesWithBuildAll keeps the two build tasks from drifting apart again:
// build-all already suffixed correctly, which is exactly why the single-platform gap went
// unnoticed. Both must derive the suffix from the same helper.
func TestBuildOutputPath_AgreesWithBuildAll(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		want := "qompack" + exeSuffix(goos)
		if got := filepath.Base(buildOutputPath(goos)); got != want {
			t.Errorf("build and build-all disagree for %q: %q vs %q", goos, got, want)
		}
	}
}

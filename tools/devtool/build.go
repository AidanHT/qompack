package main

import (
	"path/filepath"
	"runtime"
)

// taskBuild builds the single static binary for the host platform:
// CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X …core.Version=$(version)" -o bin/qompack ./cmd/qompack
//
// The output name carries exeSuffix for the same reason taskBuildAll does. `go build -o bin/qompack`
// on Windows produces a file with no extension, which the shell will not execute — so every
// downstream step that runs bin/qompack (`devtool test-e2e`, the plugin harness, a developer
// following the README) fails on Windows only, with an error about the command not existing rather
// than about the build.
func taskBuild(args []string) error {
	version := resolveVersion()
	out := buildOutputPath(runtime.GOOS)
	return goInheritEnv(map[string]string{"CGO_ENABLED": "0"},
		"build", "-trimpath", "-ldflags", versionLdflags(version), "-o", out, "./cmd/qompack")
}

// buildOutputPath is the host-platform binary path `build` writes. Split out from taskBuild so the
// platform suffix is assertable without shelling out to a real compile.
func buildOutputPath(goos string) string {
	return filepath.Join("bin", "qompack"+exeSuffix(goos))
}

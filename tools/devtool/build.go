package main

import "path/filepath"

// taskBuild builds the single static binary for the host platform:
// CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X …core.Version=$(version)" -o bin/qompack ./cmd/qompack
func taskBuild(args []string) error {
	version := resolveVersion()
	out := filepath.Join("bin", "qompack")
	return goInheritEnv(map[string]string{"CGO_ENABLED": "0"},
		"build", "-trimpath", "-ldflags", versionLdflags(version), "-o", out, "./cmd/qompack")
}

package main

import (
	"fmt"
	"path/filepath"
)

// taskBuildAll cross-builds the six 00-ARCHITECTURE.md §2.6 release targets into
// dist/qompack-<os>-<arch>[.exe] (releaseTargets is shared with bindeps, which checks the same
// six platforms).
func taskBuildAll(args []string) error {
	version := resolveVersion()
	ldflags := versionLdflags(version)
	for _, tgt := range releaseTargets {
		out := filepath.Join("dist", fmt.Sprintf("qompack-%s-%s%s", tgt.GOOS, tgt.GOARCH, exeSuffix(tgt.GOOS)))
		env := map[string]string{"CGO_ENABLED": "0", "GOOS": tgt.GOOS, "GOARCH": tgt.GOARCH}
		if err := goInheritEnv(env, "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/qompack"); err != nil {
			return fmt.Errorf("build-all: %s/%s: %w", tgt.GOOS, tgt.GOARCH, err)
		}
	}
	return nil
}

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
		if err := goBuildRelease(tgt.GOOS, tgt.GOARCH, out, ldflags); err != nil {
			return fmt.Errorf("build-all: %s/%s: %w", tgt.GOOS, tgt.GOARCH, err)
		}
	}
	return nil
}

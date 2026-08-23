package guards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// QOMPACK_FAULT is the §12.3 fault-injection switch: it makes a hook client take a degraded path on
// purpose so the degradation can be tested end to end. That makes it the one environment variable in
// this repository that must never be readable from anywhere a user's real session could reach it,
// and it is confined to exactly two non-test files.
//
//   - internal/cli/fault.go READS it. One reader, so what the switch can do is bounded by one file.
//   - internal/daemon/spawn.go STRIPS it from a spawned daemon's environment. Without that, a
//     developer's injected fault would be inherited by the resident daemon and outlive the command
//     that asked for it — the switch would stop being per-invocation.
//
// SP-05's plan claimed a CI grep enforced this. There is no such grep, and there never was: the
// 2026-08-22 plan audit found the claim and the plan now states the invariant as a two-file one.
// This test is the enforcement the claim described.
//
// Test files are unrestricted by design. internal/cli/fault_test.go, internal/daemon/spawn_test.go
// and test/e2e/faultinject_test.go all name the variable, and a guard that forbade that would forbid
// testing the switch at all.
const faultEnvVar = "QOMPACK_FAULT"

// faultEnvAllowed is the closed set of non-test files permitted to name it, module-relative.
var faultEnvAllowed = map[string]bool{
	"internal/cli/fault.go":    true,
	"internal/daemon/spawn.go": true,
}

// TestGuard_FaultEnvIsConfinedToTwoFiles is the enforcement itself.
func TestGuard_FaultEnvIsConfinedToTwoFiles(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	found := map[string]bool{}
	scanned := 0

	for _, dir := range []string{"internal", "cmd", "tools", "test"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if base := filepath.Base(p); base == "testdata" || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scanned++
			b, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			if !strings.Contains(string(b), faultEnvVar) {
				return nil
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			found[filepath.ToSlash(rel)] = true
			return nil
		})
		require.NoError(t, err)
	}

	require.NotZero(t, scanned, "the walk found no non-test Go files — the guard would vacuously pass")

	for f := range found {
		require.True(t, faultEnvAllowed[f],
			"%s names %s. The fault switch has exactly one reader (internal/cli/fault.go) and one "+
				"stripper (internal/daemon/spawn.go); a third site widens what an injected fault can "+
				"reach, and nothing else in the build would say so",
			f, faultEnvVar)
	}
	for f := range faultEnvAllowed {
		require.True(t, found[f],
			"%s no longer names %s. If the reader moved, move this allowlist with it; if the daemon "+
				"stopped stripping the variable, an injected fault now outlives the command that "+
				"asked for it and is inherited by every hook the resident daemon serves",
			f, faultEnvVar)
	}
}

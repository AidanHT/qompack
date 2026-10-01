package e2e

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestInstallArtifactDir_DoesNotOutliveItsTest pins that a non-collecting run's per-test install
// record directory is forgotten when the test that created it ends (test/security's
// TestHarness_TempArtifactDirDoesNotOutliveItsTest, the same defect).
//
// The directory is that test's t.TempDir, which the testing package removes at the test's end. A
// cache keyed by t.Name() that kept it would hand the next test of the same name, every repeat
// under `go test -count=2`, a directory that no longer exists, and the repeat would then fail
// writing its record after the case itself had passed (test/fault, test/platform and test/release
// did exactly that in run 36816905394, job 110223848442).
func TestInstallArtifactDir_DoesNotOutliveItsTest(t *testing.T) {
	t.Setenv(installArtifactsEnv, "") // the non-collecting path is the one that caches

	var name, dir string
	t.Run("writes_a_record", func(t *testing.T) {
		name, dir = t.Name(), installArtifactDir(t)
		require.DirExists(t, dir)
		require.Equal(t, dir, installArtifactDir(t), "one test's records share one directory")
	})

	_, err := os.Stat(paths.Long(dir))
	require.True(t, os.IsNotExist(err), "the testing package removes a finished test's TempDir (%v)", err)

	installTempDirMu.Lock()
	stale, cached := installTempDirs[name]
	installTempDirMu.Unlock()
	require.False(t, cached,
		"installArtifactDir still maps %s to %s after that test ended; its next run would write there", name, stale)
}

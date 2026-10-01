package release

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestHarness_TempArtifactDirDoesNotOutliveItsTest pins that a non-collecting run's per-test record
// directory is forgotten when the test that created it ends (test/security's row of the same name,
// the same defect).
//
// The directory is that test's t.TempDir, which the testing package removes at the test's end. A
// cache keyed by t.Name() that kept it handed the next test of the same name, every repeat under
// `go test -count=2` (ci.yml's Windows leg), a directory that no longer exists, and the repeat
// then failed writing its record with "The system cannot find the path specified" after the case
// itself had passed (run 36816905394, job 110223848442).
func TestHarness_TempArtifactDirDoesNotOutliveItsTest(t *testing.T) {
	t.Setenv(artifactsEnvKey, "") // the non-collecting path is the one that caches

	var name, dir string
	t.Run("writes_a_record", func(t *testing.T) {
		name, dir = t.Name(), artifactDir(t)
		require.DirExists(t, dir)
		require.Equal(t, dir, artifactDir(t), "one test's records share one directory")
	})

	_, err := os.Stat(paths.Long(dir))
	require.True(t, os.IsNotExist(err), "the testing package removes a finished test's TempDir (%v)", err)

	tempArtifactMu.Lock()
	stale, cached := tempArtifactDirs[name]
	tempArtifactMu.Unlock()
	require.False(t, cached,
		"artifactDir still maps %s to %s after that test ended; its next run would write there", name, stale)
}

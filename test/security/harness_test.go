package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// fileVersionFixtureRel is a display path with upper-case letters in it, the shape
// TestSecurity_ArchivedTextIsDataNeverAnInstruction reads (docs/NOTES.md). An all-lower-case path
// cannot tell a case-folded key from the path it was derived from, which is how the predicate
// below passed on every Linux run and on the lower-case src/auth.ts case while failing
// deterministically on Windows.
const fileVersionFixtureRel = "docs/NOTES.md"

// fileVersionLine is one index/files.jsonl line recording key, in the store's own shape
// (internal/store/files.go fileVersionRec).
func fileVersionLine(key string) []byte {
	return fmt.Appendf(nil,
		`{"v":1,"path":%q,"ts":1790362806763,"turn":0,"root":"sha256:%064d","bytes":1672}`+"\n",
		key, 0)
}

// TestHarness_FileVersionPredicateReadsTheObserversKey pins requireFileVersion's predicate to the key
// the observer records a Read under, rather than to the display path the case hands it.
//
// The observer appends a Read's §8.2 file version under hookio.CaptureScope's PrimaryKey, which is
// paths.Key of paths.Norm — case-folded on Windows and macOS (§4). The first arm derives that key
// from the producer itself, so it fails wherever the host folds and the predicate does not. The
// second arm pins both fold settings through paths.KeyFold, so a predicate that ignores the fold
// fails on every platform, Linux included.
func TestHarness_FileVersionPredicateReadsTheObserversKey(t *testing.T) {
	t.Run("the_observers_key_on_this_host", func(t *testing.T) {
		root := t.TempDir()
		full := filepath.Join(root, filepath.FromSlash(fileVersionFixtureRel))
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(full)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(full), []byte("notes\n"), 0o600))

		input, err := json.Marshal(map[string]string{"file_path": fileVersionFixtureRel})
		require.NoError(t, err)
		scope := hookio.CaptureScope(root, "Read", input)
		require.Equal(t, hookio.ScopeAllow, scope.Verdict, "the fixture path must be in scope")
		require.NotEmpty(t, scope.PrimaryKey)

		image := fileVersionLine(scope.PrimaryKey)
		require.True(t, fileVersionNames(image, fileVersionFixtureRel),
			"a version the observer recorded under %q must satisfy the wait for %q",
			scope.PrimaryKey, fileVersionFixtureRel)
		require.False(t, fileVersionNames(image, "docs/OTHER.md"),
			"a version of a different path must not satisfy the wait")
	})

	for _, fold := range []bool{true, false} {
		t.Run(fmt.Sprintf("fold_%v", fold), func(t *testing.T) {
			image := fileVersionLine(paths.KeyFold(fileVersionFixtureRel, fold))
			require.True(t, fileVersionNamesFold(image, fileVersionFixtureRel, fold),
				"the key %q must satisfy the wait for %q",
				paths.KeyFold(fileVersionFixtureRel, fold), fileVersionFixtureRel)
			require.False(t, fileVersionNamesFold(image, "docs/OTHER.md", fold),
				"a version of a different path must not satisfy the wait")
		})
	}
}

// TestHarness_TempArtifactDirDoesNotOutliveItsTest pins that a non-collecting run's per-test record
// directory is forgotten when the test that created it ends.
//
// The directory is that test's t.TempDir, which the testing package removes at the test's end. A
// cache keyed by t.Name() that kept it would hand the next test of the same name — every repeat
// under `go test -count=N` — a directory that no longer exists, and that test's writeRecord would
// then fail with "The system cannot find the path specified" after the case itself had passed.
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

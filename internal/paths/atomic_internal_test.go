package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenameWithRetry_LeavesADirectoryDestinationUntouched pins the retry's precondition: the
// chmod-and-retry is for a read-only regular file at the destination and nothing else. On POSIX
// os.Chmod(dir, 0o600) strips a directory's execute bits, so a failed replace over a non-empty
// directory used to leave that directory untraversable and its contents unreadable — the first
// Linux and macOS CI run found ReplacePinsView doing exactly that to the old evidence
// (TestReplacePinsView_DestinationFailurePreservesEvidenceAndCleansStaging). Windows directories
// ignore the bit, which is why the test beside it never saw it.
func TestRenameWithRetry_LeavesADirectoryDestinationUntouched(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "dest")
	inside := filepath.Join(dst, "kept.txt")
	require.NoError(t, os.MkdirAll(dst, 0o700))
	require.NoError(t, os.WriteFile(inside, []byte("evidence"), 0o600))
	before, err := os.Lstat(dst)
	require.NoError(t, err)

	tmp := filepath.Join(root, "staged.tmp")
	require.NoError(t, os.WriteFile(tmp, []byte("replacement"), 0o600))

	require.Error(t, renameWithRetry(tmp, dst), "a file cannot replace a non-empty directory")

	after, err := os.Lstat(dst)
	require.NoError(t, err)
	require.True(t, after.IsDir())
	require.Equal(t, before.Mode().Perm(), after.Mode().Perm(),
		"a failed replace must not change the destination directory's mode")
	got, err := os.ReadFile(inside)
	require.NoError(t, err, "the directory's contents must still be readable after the failed replace")
	require.Equal(t, "evidence", string(got))
}

// TestTmpDirFor_IsTheOwningStoresTmpOrBesideTheTarget pins where WriteAtomic stages for every shape
// of target under a stand-in home that holds the user-global layer: in the .qompack/tmp of the store
// whose .qompack is on the target's path, and beside the target when there is none. The home's
// store owns only what is under it; the real home is never touched.
func TestTmpDirFor_IsTheOwningStoresTmpOrBesideTheTarget(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(Global(home), 0o700))
	project := filepath.Join(home, "src", "project")
	l := Of(project)
	require.NoError(t, EnsureLayout(l))
	bare := Of(filepath.Join(home, "src", "bare"))
	corpus := filepath.Join(home, "corpus")

	cases := []struct{ name, p, want string }{
		{"a project store file", filepath.Join(l.State, "store.json"), l.Tmp},
		{"a project store file deep in the tree", filepath.Join(l.Objects, "ab", "cd", "x.zst"), l.Tmp},
		{"the user-global layer's own file", filepath.Join(Global(home), "calibration.json"), Of(home).Tmp},
		{"a file beside a project store", filepath.Join(project, "README.md"), project},
		{"a store file of a project with no store yet", filepath.Join(bare.Run, "state.bin"), bare.Run},
		{"a file in no store", filepath.Join(corpus, "session.json"), corpus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, ok := rootOf(tc.p)
			require.Equal(t, tc.want, tmpDirFor(tc.p, root, ok))
		})
	}
}

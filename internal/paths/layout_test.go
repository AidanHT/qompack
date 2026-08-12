package paths_test

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// newLayout materializes a fresh, fully created .qompack tree under a new t.TempDir() and
// returns its Layout. It is the shared fixture every other test file in this package uses.
func newLayout(t *testing.T) paths.Layout {
	t.Helper()
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(l))
	return l
}

func TestEnsureLayout_CreatesAllDirsAndSelfIgnore(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(l))

	// The 16 MkdirAll targets fixed by paths/layout.go, plus eval/ itself, which eval/replay and
	// eval/opt imply as their parent: 17 directories in total.
	dirs := []string{
		l.Objects, l.Index, l.Sketches, l.DAG, l.Grammar, l.Checkpoints, l.Pins,
		filepath.Join(l.Eval, "replay"), filepath.Join(l.Eval, "opt"), l.Eval,
		l.Records, l.State, l.Run, l.Spool, l.Logs, l.Metrics, l.Tmp,
	}
	require.Len(t, dirs, 17)
	for _, d := range dirs {
		fi, err := os.Stat(d)
		require.NoError(t, err, "expected directory to exist: %s", d)
		require.True(t, fi.IsDir(), "expected a directory: %s", d)
	}

	b, err := os.ReadFile(filepath.Join(l.Dot, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*\n", string(b))
}

func TestEnsureLayout_IdempotentAndNeverRewritesGitignore(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(l))
	require.NoError(t, paths.EnsureLayout(l))

	b, err := os.ReadFile(filepath.Join(l.Dot, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*\n", string(b))
}

func TestEnsureLayout_MkdirFailsWhenPathIsAFile(t *testing.T) {
	root := t.TempDir()
	dot := filepath.Join(root, ".qompack")
	require.NoError(t, os.MkdirAll(dot, 0o700))
	// "objects" is supposed to become a directory; pre-create it as a plain file so MkdirAll
	// must fail instead of silently succeeding.
	require.NoError(t, os.WriteFile(filepath.Join(dot, "objects"), []byte("not a directory"), 0o600))

	l := paths.Of(root)
	err := paths.EnsureLayout(l)
	require.Error(t, err)
}

// TestEnsureLayout_WriteAtomicFailureIsPropagated drives EnsureLayout's own WriteAtomic-failure
// path using icacls (a standard Windows tool, invoked via os/exec) to deny "add file" permission
// on l.Dot after every directory already exists but before .gitignore is written. The mkdir loop
// only needs to Stat each already-existing directory, which a write-only deny does not block; the
// rename that finishes WriteAtomic's write of .gitignore does need to create a new entry in
// l.Dot, and that is exactly what is denied.
func TestEnsureLayout_WriteAtomicFailureIsPropagated(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: icacls is windows-specific")
	}
	u, err := user.Current()
	if err != nil {
		t.Skipf("platform: could not determine current user: %v", err)
	}

	l := newLayout(t)
	require.NoError(t, os.Remove(filepath.Join(l.Dot, ".gitignore")))

	if out, denyErr := exec.Command("icacls", l.Dot, "/deny", u.Username+":(WD)").CombinedOutput(); denyErr != nil {
		t.Skipf("platform: icacls deny unavailable in this environment: %v: %s", denyErr, out)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("icacls", l.Dot, "/remove:d", u.Username).CombinedOutput()
	})

	err = paths.EnsureLayout(l)
	require.Error(t, err)
}

func TestOf_FieldsAreUnderDot(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)

	require.Equal(t, root, l.Root)
	require.Equal(t, filepath.Join(root, ".qompack"), l.Dot)
	for name, got := range map[string]string{
		"Objects": l.Objects, "Index": l.Index, "Sketches": l.Sketches, "DAG": l.DAG,
		"Grammar": l.Grammar, "Checkpoints": l.Checkpoints, "Pins": l.Pins, "Eval": l.Eval,
		"Records": l.Records, "State": l.State, "Run": l.Run, "Spool": l.Spool,
		"Logs": l.Logs, "Metrics": l.Metrics, "Tmp": l.Tmp,
	} {
		require.True(t, filepath.Dir(got) == l.Dot, "%s should be a direct child of Dot, got %s", name, got)
	}
}

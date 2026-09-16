package paths_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// makeDirLink installs a directory link at linkPath pointing at target, by whatever mechanism this
// host allows: a real symlink where privilege permits, and otherwise an NTFS junction, which needs
// none. Both shapes matter — a junction is what a directory swap actually looks like on Windows, and
// filepath.EvalSymlinks does not follow one.
func makeDirLink(linkPath, target string) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// cmd's own `mklink` is the only mechanism the standard library does not expose, and it is a
	// builtin rather than an executable, hence `cmd /c`.
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput() //nolint:gosec // G204: fixed subcommand over this test's own temp directories
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", linkPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// makeRelativeDirLink is makeDirLink for a target stored relative to the link's parent, so
// resolveLinks' relative-target join is what the second hop exercises.
func makeRelativeDirLink(linkPath, relativeTarget string) error {
	if err := os.Symlink(relativeTarget, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", filepath.Base(linkPath), relativeTarget) //nolint:gosec // G204: fixed subcommand over this test's own temp directories
	cmd.Dir = filepath.Dir(linkPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s %s (cwd %s): %w: %s", filepath.Base(linkPath), relativeTarget, cmd.Dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// TestResolvesInside_RefusesAParentReplacedByALinkOutside is finding S-1's own unit.
//
// Norm answers "src/auth.ts" for this path — cleanly, and by design, because adopting the outside
// resolution is the smuggling it refuses. ResolvesInside is the second question, and it must say no.
func TestResolvesInside_RefusesAParentReplacedByALinkOutside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "auth.ts"), []byte("outside\n"), 0o600))

	src := filepath.Join(root, "src")
	if err := makeDirLink(src, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(src) })

	// Norm still normalises it, which is the point of the finding.
	norm, err := paths.Norm(root, "src/auth.ts")
	require.NoError(t, err, "Norm is the store-key normaliser and still answers")
	require.Equal(t, "src/auth.ts", norm)

	require.False(t, paths.ResolvesInside(root, "src/auth.ts"),
		"a path whose parent now points outside the project must not be authorized")
	require.False(t, paths.ResolvesInside(root, "src"),
		"the link itself resolves outside too")
	require.False(t, paths.ResolvesInside(root, "src/deeper/missing.ts"),
		"a path that does not exist below the link is refused on the link")
}

// TestResolvesInside_AllowsOrdinaryPaths keeps the gate from refusing what it must allow: ordinary
// in-project paths, paths whose file has been deleted, and the root itself.
func TestResolvesInside_AllowsOrdinaryPaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "auth.ts"), []byte("x\n"), 0o600))

	for _, p := range []string{
		"src/auth.ts",
		"src/never-existed.ts",
		"does/not/exist/at/all.ts",
		"src",
		".",
	} {
		require.True(t, paths.ResolvesInside(root, p), "%q must be authorized", p)
	}
	require.True(t, paths.ResolvesInside(root, filepath.Join(root, "src", "auth.ts")),
		"an absolute in-project path must be authorized")
}

// TestResolvesInside_RefusesLexicalEscapesAndEmptyInputs: a lexical escape needs no disk access, and
// an empty root or path is not something a gate may say yes to.
func TestResolvesInside_RefusesLexicalEscapesAndEmptyInputs(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	require.False(t, paths.ResolvesInside(root, "../escape.ts"))
	require.False(t, paths.ResolvesInside(root, "../../etc/passwd"))
	require.False(t, paths.ResolvesInside(root, outside))
	require.False(t, paths.ResolvesInside("", "src/auth.ts"))
	require.False(t, paths.ResolvesInside(root, ""))
}

// TestResolvesInside_AllowsALinkThatStaysInside: the check is about WHERE a link lands, not about
// links being present. A link inside the project to another place inside the project is authorized.
func TestResolvesInside_AllowsALinkThatStaysInside(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real", "auth.ts"), []byte("x\n"), 0o600))

	link := filepath.Join(root, "linked")
	if err := makeDirLink(link, filepath.Join(root, "real")); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	require.True(t, paths.ResolvesInside(root, "linked/auth.ts"),
		"a link that stays inside the project is not an escape")
}

// TestResolvesInside_RefusesAChainedLinkThroughAnotherLink is the two-junction S-1 bypass:
// proj\other points at a directory outside the project and proj\sub points at proj\other\x.
// Following the second link as a whole left a lexically-inside path whose bytes live outside.
// A relative-target variant is the same shape with the second link stored relative to its parent.
func TestResolvesInside_RefusesAChainedLinkThroughAnotherLink(t *testing.T) {
	t.Run("absolute two-junction", func(t *testing.T) {
		root, secretRel := chainedOutsideFixture(t, false)
		norm, err := paths.Norm(root, secretRel)
		require.NoError(t, err, "Norm is the store-key normaliser and still answers")
		require.Equal(t, "sub/secret.txt", norm)
		require.False(t, paths.ResolvesInside(root, secretRel),
			"a path that reaches outside through a second link must not be authorized")
	})
	t.Run("relative-target", func(t *testing.T) {
		root, secretRel := chainedOutsideFixture(t, true)
		norm, err := paths.Norm(root, secretRel)
		require.NoError(t, err, "Norm is unchanged")
		require.Equal(t, "sub/secret.txt", norm)
		require.False(t, paths.ResolvesInside(root, secretRel),
			"a relative link whose target walks through another link must not be authorized")
	})
}

// chainedOutsideFixture builds proj\other → outside and proj\sub → other\x (absolute or
// relative), with outside\x\secret.txt holding bytes that a live read would serve.
func chainedOutsideFixture(t *testing.T, relative bool) (root, secretRel string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "proj")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x", "secret.txt"), []byte("outside bytes\n"), 0o600))

	other := filepath.Join(root, "other")
	if err := makeDirLink(other, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(other) })

	sub := filepath.Join(root, "sub")
	var linkErr error
	if relative {
		// Relative to the link's parent so Readlink returns a non-absolute target.
		// mklink /J otherwise resolves the target against the process cwd.
		linkErr = makeRelativeDirLink(sub, filepath.Join("other", "x"))
	} else {
		linkErr = makeDirLink(sub, filepath.Join(other, "x"))
	}
	if linkErr != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + linkErr.Error())
	}
	t.Cleanup(func() { _ = os.Remove(sub) })

	got, err := os.ReadFile(filepath.Join(sub, "secret.txt"))
	require.NoError(t, err, "fixture sanity: the chain must serve the outside bytes")
	require.Equal(t, []byte("outside bytes\n"), got)
	return root, "sub/secret.txt"
}

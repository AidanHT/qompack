package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/paths"
)

func TestNorm_RejectsEscape(t *testing.T) {
	tmp := t.TempDir()

	cases := map[string]string{
		"leading dotdot":       filepath.Join("..", "outside"),
		"dotdot inside an abs": filepath.Join(tmp, "..", "x"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := paths.Norm(tmp, in)
			require.Error(t, err)
			require.Contains(t, err.Error(), "escapes project root")
		})
	}
}

func TestNorm_RejectsExistingPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir() // a real, existing sibling directory: forces EvalSymlinks to succeed
	// and inside() to be evaluated, rather than short-circuiting on a missing path.
	_, err := paths.Norm(root, other)
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes project root")
}

func TestNorm_EmptyProjectRoot(t *testing.T) {
	_, err := paths.Norm("", "a")
	require.Error(t, err)
}

func TestNorm_RootItself(t *testing.T) {
	root := t.TempDir()
	got, err := paths.Norm(root, ".")
	require.NoError(t, err)
	require.Equal(t, ".", got)
}

// TestNorm_RejectsDifferentVolume drives Norm's own filepath.Rel failure path: two absolute
// paths on different Windows volumes can never be made relative to one another, lexically,
// regardless of whether either side exists on disk.
func TestNorm_RejectsDifferentVolume(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: drive volumes are windows-specific")
	}
	root := t.TempDir() // e.g. C:\...
	other := `D:\somewhere\file.txt`

	_, err := paths.Norm(root, other)
	require.Error(t, err)
}

// TestNorm_LeavesUNCVolumeMismatchUnresolved drives inside()'s own filepath.Rel failure path.
// The admin share form of root (\\localhost\C$\...) names the exact same files as root itself,
// so filepath.EvalSymlinks succeeds on it — inside() actually runs — but its volume spelling can
// never be made relative to a drive-letter root lexically, so inside() must return false via its
// own error branch rather than via a lexical ".." prefix.
func TestNorm_LeavesUNCVolumeMismatchUnresolved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: UNC admin shares are windows-specific")
	}
	root := t.TempDir()
	vol := filepath.VolumeName(root) // e.g. "C:"
	if len(vol) != 2 || vol[1] != ':' {
		t.Skip("platform: root has no drive-letter volume to mirror via an admin share")
	}
	drive := strings.TrimSuffix(vol, ":")
	uncEquivalent := `\\localhost\` + drive + `$` + strings.TrimPrefix(root, vol)

	if _, err := os.Stat(uncEquivalent); err != nil {
		t.Skipf("platform: admin share unavailable in this environment: %v", err)
	}

	_, err := paths.Norm(root, uncEquivalent)
	require.Error(t, err)
}

// TestNorm_LeavesLongPathPrefixVolumeMismatchUnresolved drives the same inside() failure path as
// TestNorm_LeavesUNCVolumeMismatchUnresolved, but through the \\?\ long-path prefix form instead
// of an admin share: it names exactly the same local files as root, needs no network stack or
// special share configuration, and is available on every Windows installation, so it is the more
// robust of the two routes to this branch.
func TestNorm_LeavesLongPathPrefixVolumeMismatchUnresolved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: the \\\\?\\ prefix form is windows-specific")
	}
	root := t.TempDir()
	prefixed := `\\?\` + root

	_, err := paths.Norm(root, prefixed)
	require.Error(t, err)
}

func TestNorm_ForwardSlashRelative(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "a", "b.ts") // native separators: backslashes on Windows
	got, err := paths.Norm(tmp, in)
	require.NoError(t, err)
	require.Equal(t, "a/b.ts", got)
	require.NotContains(t, got, `\`)
}

func TestNorm_ResolvesExistingPathInsideRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	got, err := paths.Norm(root, filepath.Join("a", "b"))
	require.NoError(t, err)
	require.Equal(t, "a/b", got)
}

func TestNorm_ResolvesSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("platform: symlinks unavailable in this environment: %v", err)
	}

	got, err := paths.Norm(root, "link")
	require.NoError(t, err)
	require.Equal(t, "real", got)
}

func TestNorm_LeavesSymlinkOutsideRootUnresolved(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	real := filepath.Join(outside, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("platform: symlinks unavailable in this environment: %v", err)
	}

	got, err := paths.Norm(root, "link")
	require.NoError(t, err)
	require.Equal(t, "link", got, "a symlink pointing outside root must not be followed")
}

func TestKeyFold(t *testing.T) {
	require.Equal(t, "src/foo.ts", paths.KeyFold("Src/Foo.TS", true))
	require.Equal(t, "Src/Foo.TS", paths.KeyFold("Src/Foo.TS", false))
}

func TestKey_UsesDefaultFold(t *testing.T) {
	require.Equal(t, paths.KeyFold("Src/Foo.TS", paths.DefaultFold()), paths.Key("Src/Foo.TS"))
}

// TestNorm_Property checks, for any relative path built from safe segments, that Norm is
// idempotent and that its output never contains a backslash or "..".
func TestNorm_Property(t *testing.T) {
	root := t.TempDir()
	segment := rapid.StringMatching(`[a-zA-Z0-9_]{1,12}`)

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(rt, "n")
		segs := make([]string, n)
		for i := range segs {
			segs[i] = segment.Draw(rt, "seg")
		}
		p := strings.Join(segs, "/")

		n1, err := paths.Norm(root, p)
		require.NoError(rt, err)
		require.NotContains(rt, n1, `\`)
		require.NotContains(rt, n1, "..")

		n2, err := paths.Norm(root, n1)
		require.NoError(rt, err)
		require.Equal(rt, n1, n2, "Norm must be idempotent")
		require.NotContains(rt, n2, `\`)
		require.NotContains(rt, n2, "..")
	})
}

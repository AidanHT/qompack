package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// maxPathLegacy is Windows' legacy MAX_PATH. The long-path fixture must exceed it before the temp
// root is even prepended, which is what makes it a real test of paths.Long's \\?\ prefixing.
const maxPathLegacy = 260

// TestWindowsHostileFiles_AllCreatable asserts every one of §6.2's five path classes can be
// created inside a real project, and that the case-colliding pair produces whichever outcome this
// host's filesystem actually produces — asserted explicitly rather than fought.
func TestWindowsHostileFiles_AllCreatable(t *testing.T) {
	files := WindowsHostileFiles()
	p := NewProject(t, WithFiles(files))

	t.Run("a path with spaces", func(t *testing.T) {
		require.Equal(t, files[SpacePathFile], readProjectFile(t, p.Root, SpacePathFile))
	})

	t.Run("a path over 260 characters", func(t *testing.T) {
		require.Greater(t, len(LongPathFile), maxPathLegacy,
			"the fixture must clear MAX_PATH on its own, before any temp root is prepended")
		require.Equal(t, longPathDepth, strings.Count(LongPathFile, "/"),
			"twelve nested directories")
		require.Equal(t, files[LongPathFile], readProjectFile(t, p.Root, LongPathFile))
	})

	t.Run("a CRLF file survives byte for byte", func(t *testing.T) {
		got := readProjectFile(t, p.Root, CRLFFile)
		require.Equal(t, files[CRLFFile], got)
		require.Contains(t, got, "\r\n", "the CRLF fixture must not be normalized on the way to disk")
	})

	t.Run("a read-only file", func(t *testing.T) {
		full := filepath.Join(p.Root, filepath.FromSlash(ReadOnlyFile))
		require.Equal(t, files[ReadOnlyFile], readProjectFile(t, p.Root, ReadOnlyFile))

		require.NoError(t, os.Chmod(paths.Long(full), 0o444))
		// Restore a writable mode before cleanup so removing the temp tree cannot be blocked by
		// the very attribute this case set.
		t.Cleanup(func() { _ = os.Chmod(paths.Long(full), filePerm) })

		f, err := os.OpenFile(paths.Long(full), os.O_WRONLY, 0)
		if err == nil {
			_ = f.Close()
			t.Fatal("a 0444 file must not open for writing")
		}
		require.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("a case-colliding pair", func(t *testing.T) {
		entries, err := os.ReadDir(filepath.Join(p.Root, "src"))
		require.NoError(t, err)

		var collided []string
		for _, e := range entries {
			if strings.EqualFold(e.Name(), "foo.ts") {
				collided = append(collided, e.Name())
			}
		}

		switch len(collided) {
		case 1:
			// Case-insensitive filesystem (the Windows and macOS default). The two fixture entries
			// collapsed into one file, and writeFiles' sorted order means the LAST writer won:
			// "src/foo.ts" sorts after "src/Foo.ts", so the surviving content is the lower-case
			// one. Both spellings must read back that same content.
			require.Equal(t, files[LowerCaseFile], readProjectFile(t, p.Root, LowerCaseFile))
			require.Equal(t, files[LowerCaseFile], readProjectFile(t, p.Root, UpperCaseFile),
				"on a case-insensitive filesystem both spellings name the one surviving file")
			require.False(t, paths.KeyFold(UpperCaseFile, true) != paths.KeyFold(LowerCaseFile, true),
				"a folded key must make the two spellings identical, which is why DefaultFold is true here")
		case 2:
			// Case-sensitive filesystem: two genuinely distinct files.
			require.Equal(t, files[UpperCaseFile], readProjectFile(t, p.Root, UpperCaseFile))
			require.Equal(t, files[LowerCaseFile], readProjectFile(t, p.Root, LowerCaseFile))
			require.NotEqual(t, paths.KeyFold(UpperCaseFile, false), paths.KeyFold(LowerCaseFile, false))
		default:
			t.Fatalf("a case-colliding pair must produce one file or two, got %d: %v", len(collided), collided)
		}
	})
}

// TestWindowsHostileFiles_ShapeIsStable pins the fixture set itself: five classes across six
// entries, because the case-colliding class needs two.
func TestWindowsHostileFiles_ShapeIsStable(t *testing.T) {
	files := WindowsHostileFiles()
	require.Len(t, files, 6)
	for _, name := range []string{SpacePathFile, LongPathFile, UpperCaseFile, LowerCaseFile, CRLFFile, ReadOnlyFile} {
		require.Contains(t, files, name)
		require.NotEmpty(t, files[name])
	}
	require.Contains(t, SpacePathFile, " ")

	for _, dir := range strings.Split(filepath.ToSlash(LongPathFile), "/")[:longPathDepth] {
		require.Len(t, dir, longPathDirLen)
	}
	require.Len(t, filepath.Base(LongPathFile), longPathNameLen)
}

// readProjectFile reads a project-relative path through paths.Long, which is what lets the
// >260-character fixture be read at all on Windows.
func readProjectFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(root, filepath.FromSlash(rel))))
	require.NoError(t, err, "fixture %s must be creatable and readable", rel)
	return string(b)
}

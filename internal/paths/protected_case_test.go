package paths_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// The §7.4 guard is textual: it decides from a path's spelling whether the path names a protected
// location. On a filesystem that folds case, CHECKPOINTS\0001.json and checkpoints\0001.json are the
// same file, so a guard that compares spellings exactly lets a case variant replace a sealed
// checkpoint or truncate the pins log (w3-paths runs/fix/03, runs/fix/04, both on Windows). The
// coordinator's default (V6 close-out, 2026-09-26) is to fold case wherever paths.DefaultFold does:
// Windows, whose filesystems fold, and darwin conservatively, where the default APFS volume folds and
// a case-sensitive volume only makes the guard refuse a little more than it must.
//
// These rows are platform-neutral: off the folding platforms a case variant names a different file
// and must stay writable, which is the other half of the same rule.

func TestIsProtected_CaseVariantsFollowThePlatformsFold(t *testing.T) {
	l := newLayout(t)
	fold := paths.DefaultFold()

	cases := []struct {
		name string
		p    string
	}{
		{"an upper-case checkpoints directory", filepath.Join(l.Dot, "CHECKPOINTS", "0001.json")},
		{"a mixed-case checkpoints directory", filepath.Join(l.Dot, "Checkpoints", "MANIFEST.jsonl")},
		{"an upper-case pins directory", filepath.Join(l.Dot, "PINS", "invariants.jsonl")},
		{"a mixed-case bloom name", filepath.Join(l.Sketches, "Tried.Bloom")},
		{"a mixed-case sketches directory", filepath.Join(l.Dot, "Sketches", "tried.bloom")},
		{"an upper-case store directory", filepath.Join(l.Root, ".QOMPACK", "pins", "invariants.jsonl")},
		{"every element folded at once", filepath.Join(l.Root, ".Qompack", "SKETCHES", "TRIED.BLOOM")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, fold, paths.IsProtected(l.Root, tc.p),
				"on a platform that folds case (DefaultFold=%v) %s names a protected file", fold, tc.p)
		})
	}

	// The root itself is compared the same way, so a caller naming the project in another case
	// still finds its protected files.
	t.Run("the project root spelled in another case", func(t *testing.T) {
		upperRoot := swapCase(l.Root)
		if upperRoot == l.Root {
			t.Fatalf("fixture sanity: %q has no letter to change the case of", l.Root)
		}
		require.Equal(t, fold, paths.IsProtected(upperRoot, filepath.Join(l.Checkpoints, "0001.json")))
		require.Equal(t, fold, paths.IsProtected(l.Root, filepath.Join(upperRoot, ".qompack", "pins", "x")))
	})
}

// TestIsProtected_FoldingRefusesNoOrdinaryPath keeps the fold from widening what counts as protected
// beyond case: names that merely contain a protected name, or sit beside one, stay writable.
func TestIsProtected_FoldingRefusesNoOrdinaryPath(t *testing.T) {
	l := newLayout(t)
	for _, p := range []string{
		filepath.Join(l.Dot, "CHECKPOINTSX", "0001.json"),
		filepath.Join(l.Dot, "XPINS", "invariants.jsonl"),
		filepath.Join(l.Sketches, "TRIED.BLOOM.1.bak"),
		filepath.Join(l.Sketches, "Touch.cms"),
		filepath.Join(l.Dot, "STATE", "bocd.json"),
		filepath.Join(l.Root, "PINS", "invariants.jsonl"),
	} {
		require.False(t, paths.IsProtected(l.Root, p), "%s is not a protected location in any case", p)
	}
}

// swapCase flips the case of every ASCII letter in s.
func swapCase(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = c - 'a' + 'A'
		case c >= 'A' && c <= 'Z':
			b[i] = c - 'A' + 'a'
		}
	}
	return string(b)
}

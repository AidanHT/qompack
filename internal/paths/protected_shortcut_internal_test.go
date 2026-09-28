package paths

import (
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// OpenFile skips its protected-root walk when mayBeProtected(p) is false. That is only sound if
// mayBeProtected never answers false for a path IsProtected would refuse under SOME root. These
// tests hold it to that, over the named §7.4 locations and over generated paths built from the
// protected names, the layout names, dot elements, mixed separators and case variants.

func TestMayBeProtected_EveryProtectedLocationTakesTheWalk(t *testing.T) {
	root := t.TempDir()
	l := Of(root)
	for _, p := range []string{
		filepath.Join(l.Sketches, "tried.bloom"),
		filepath.Join(l.Checkpoints, "0001.json"),
		ManifestPath(l),
		filepath.Join(l.Pins, "invariants.jsonl"),
		filepath.Join(l.Pins, "nested", "deeper.jsonl"),
		filepath.Join(l.State, "..", "checkpoints", "x.json"),
		l.Dot + "/pins/view.json",
	} {
		require.True(t, IsProtected(root, p), "fixture sanity: %s must be protected", p)
		require.True(t, mayBeProtected(p), "%s is protected, so OpenFile must not skip its walk", p)
	}
}

func TestMayBeProtected_OrdinaryStorePathsSkipTheWalk(t *testing.T) {
	// mayBeProtected reads only the string, so a synthetic root is enough, and one whose own text
	// contains no protected name is what makes the negative claim about the paths under it.
	l := Of(filepath.Join(string(filepath.Separator), "srv", "project"))
	require.False(t, mayBeProtected(l.Root))
	for _, p := range []string{
		filepath.Join(l.Tmp, "obj-0123456789ab"),
		filepath.Join(l.Objects, "ab", "cd", strings.Repeat("0f", 32)+".zst"),
		filepath.Join(l.Index, "roots.jsonl"),
		filepath.Join(l.State, "pending", "abcdef.json"),
	} {
		require.False(t, mayBeProtected(p), "%s cannot be protected under any root", p)
	}
}

func TestMayBeProtected_NeverHidesAProtectedPath(t *testing.T) {
	base := t.TempDir()
	roots := []string{base, filepath.Join(base, "proj"), filepath.Join(base, "pins-project"), "rel"}
	elems := []string{
		".qompack", ".QOMPACK", "checkpoints", "Checkpoints", "pins", "PINS", "sketches",
		"tried.bloom", "tried.bloom.1.bak", "objects", "state", "tmp", "x", "..", ".", "",
		"pinsx", "xcheckpoints", "0001.json",
		// Case variants of every protected name, and the NTFS stream spellings of the bloom, which
		// IsProtected folds and strips on the platforms where they name the protected file.
		"CHECKPOINTS", "Pins", "SKETCHES", "Sketches", "TRIED.BLOOM", "Tried.Bloom",
		"tried.bloom::$DATA", "TRIED.BLOOM:x", "pins:x",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20000; i++ {
		root := roots[rng.IntN(len(roots))]
		n := 1 + rng.IntN(6)
		var b strings.Builder
		b.WriteString(root)
		for j := 0; j < n; j++ {
			if rng.IntN(4) == 0 {
				b.WriteByte('/')
			} else {
				b.WriteByte(filepath.Separator)
			}
			b.WriteString(elems[rng.IntN(len(elems))])
		}
		p := b.String()
		for _, r := range roots {
			if IsProtected(r, p) {
				require.True(t, mayBeProtected(p), "IsProtected(%q, %q) but mayBeProtected said no", r, p)
			}
		}
	}
}

// TestMayBeProtected_AllocatesNothingForAnASCIIPath keeps OpenFile's fast path what it was before the
// guard folded case: a text test with no allocation. Folding compares in place for an ASCII path;
// only a non-ASCII path on a folding platform pays for a folded copy.
func TestMayBeProtected_AllocatesNothingForAnASCIIPath(t *testing.T) {
	l := Of(filepath.Join(string(filepath.Separator), "Srv", "Project"))
	for _, p := range []string{
		filepath.Join(l.Objects, "ab", "cd", strings.Repeat("0F", 32)+".zst"),
		filepath.Join(l.Dot, "PINS", "invariants.jsonl"),
	} {
		allocs := testing.AllocsPerRun(100, func() { _ = mayBeProtected(p) })
		require.Zero(t, allocs, "mayBeProtected(%q) allocated", p)
	}
}

// TestIsProtected_FoldCoversTheFilesystemsUnicodeCaseMappings pins the three non-ASCII spellings
// foldKey exists for: NTFS upcases the dotless ı and the long ſ to I and S, so pıns and pinſ name
// the pins directory there, and APFS folds the Kelvin sign to k. Where DefaultFold holds each is
// protected, and the text test agrees, through its non-ASCII path.
func TestIsProtected_FoldCoversTheFilesystemsUnicodeCaseMappings(t *testing.T) {
	root := t.TempDir()
	l := Of(root)
	for _, p := range []string{
		filepath.Join(l.Dot, "p\u0131ns", "invariants.jsonl"),
		filepath.Join(l.Dot, "pin\u017f", "invariants.jsonl"),
		filepath.Join(l.Dot, "chec\u212apoints", "0001.json"),
	} {
		require.Equal(t, DefaultFold(), IsProtected(root, p), "%q", p)
		if IsProtected(root, p) {
			require.True(t, mayBeProtected(p), "%q is protected, so OpenFile must not skip its walk", p)
		}
	}
}

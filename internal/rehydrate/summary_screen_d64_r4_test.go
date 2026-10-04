package rehydrate

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// Wave 19g's final verify of D64 (ADR 0011 §23). The project root was found in a text by a regular
// expression compiled with `(?i)`, and containment compared a path with the root through
// filepath.Rel; on Windows both fold case by Unicode's simple folding, which pairs `k` with the
// Kelvin sign (U+212A), `s` with the long s (U+017F) and `å` with the Angstrom sign (U+212B), while
// NTFS keeps each of those a directory beside the one spelled with the letter. So a summary, a file
// pointer or a drop reason naming such a sibling was read as the project and shown. The root is now
// compared with an ASCII letter's case folded where the platform's paths fold, and nothing else
// folded. Each row below is red on 71e5133d on Windows.

// unicodeCaseRoots are root segments holding a letter that Unicode's simple case folding pairs with
// another code point, each beside its sibling spelled with that code point, which NTFS keeps a
// separate directory: `kate` beside the same name with the Kelvin sign (U+212A) for its `k`, `sam`
// beside one with the long s (U+017F) for its `s`, and `åsa` beside one with the Angstrom sign
// (U+212B) for its `å`.
var unicodeCaseRoots = []struct{ seg, sib string }{
	{"kate", "\u212Aate"},
	{"sam", "\u017Fam"},
	{"\u00E5sa", "\u212Bsa"},
}

// caseSummaries are the summaries a session leaves that name a file under root: a NotebookEdit's
// path-named value, a Read's one-word file_path, two commands, and the NotebookEdit's value cut by
// the store past the end of root, alone and as the second of two path-named values.
func caseSummaries(t *testing.T, root string) []string {
	t.Helper()
	nb := filepath.Join(root, "nb", "a.ipynb")
	kept := nb[:len(nb)-len("ipynb")]
	return []string{
		notebookPreview(t, nb),
		filepath.Join(root, "src", "main.go"),
		"git -C " + root + " status",
		"cat " + filepath.Join(root, "src", "main.go"),
		cutJSONValue(t, "notebook_path", nb, kept),
		cutSecondPath(t, nb, kept),
	}
}

// TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt: a root spelled with the Kelvin sign,
// the long s or the Angstrom sign where the project's root has `k`, `s` or `å` names a directory
// beside the project on NTFS, so every summary that names a file under it is withheld, uncut, as
// free text, or cut past the end of the root. The same summaries spelled with the real root's ASCII
// letters in the other case name the project where the platform's paths fold (Windows and macOS)
// and are shown, and name a directory beside the project elsewhere.
func TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	for _, rc := range unicodeCaseRoots {
		t.Run(rc.seg, func(t *testing.T) {
			root := previewRoot(rc.seg, "proj")
			upper := asciiUpper(root)
			require.NotEqual(t, root, upper, "fixture: the variant differs from the root")
			// Each set is built on its own: a path the build withholds teaches the screen its names
			// (knownText), so the sibling's `main.go` would withhold a command naming the project's.
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, nil,
				caseSummaries(t, previewRoot(rc.sib, "proj")), []string{rc.sib})
			if fold {
				requireScreened(t, root, hostRules(root, uat12Rules...), nil, caseSummaries(t, upper), nil, nil)
			} else {
				requireScreened(t, root, hostRules(root, uat12Rules...), nil, nil, caseSummaries(t, upper), nil)
			}
		})
	}
}

// TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons: the same sibling
// is outside the project for a file pointer, which points by hash alone, and for a drop entry's
// reason, which is redacted; a file pointer under the real root spelled in another ASCII case is
// shown where the platform's paths fold.
func TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	for _, rc := range unicodeCaseRoots {
		t.Run(rc.seg, func(t *testing.T) {
			root := previewRoot(rc.seg, "proj")
			sib, upper := previewRoot(rc.sib, "proj"), asciiUpper(root)
			sibFile, upperFile := filepath.Join(sib, "src", "util.go"), filepath.Join(upper, "src", "util.go")
			gitdir := "checkpoint: git index unsupported: reading index: open " + filepath.Join(sib, ".git", "index") + ": gone"
			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{
				{Path: sibFile, Hash: hashOf("sib"), Why: "referenced"},
				{Path: upperFile, Hash: hashOf("upper"), Why: "referenced"},
			}
			cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: gitdir})
			d := uat05Deps(t, cp)
			d.HostPaths = hostRules(root, uat12Rules...)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
			require.NotContains(t, section6, sibFile, "a file pointer beside the project is withheld")
			require.Contains(t, section6, hashOf("sib").String(), "a withheld file pointer still points by hash")
			if fold {
				require.Contains(t, section6, "- "+upperFile+" "+hashOf("upper").String(),
					"the root in another ASCII case is the project")
			} else {
				require.NotContains(t, res.Text, upperFile, "the root in another ASCII case is a directory beside the project")
			}
			var details []string
			for _, e := range res.Dropped {
				if e.Kind == "pointer_git_unavailable" {
					details = append(details, e.Detail)
				}
			}
			require.Len(t, details, 1, "the git drop is still reported: %v", res.Dropped)
			require.NotContains(t, details[0], ".git", "the sibling's gitdir is outside the project")
			requireNoLeak(t, res, []string{rc.sib})
		})
	}
}

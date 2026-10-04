package rehydrate

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/paths"
)

// Wave 19g's final verify of D64 (ADR 0011 §23). The project root was found in a text by a regular
// expression compiled with `(?i)`, and containment compared a path with the root through
// filepath.Rel; on Windows both fold case by Unicode's simple folding, which pairs `k` with the
// Kelvin sign (U+212A), `s` with the long s (U+017F) and `å` with the Angstrom sign (U+212B), while
// NTFS keeps each of those a directory beside the one spelled with the letter. So a summary, a file
// pointer or a drop reason naming such a sibling was read as the project and shown. The root is now
// compared with an ASCII letter's case folded where the platform's paths fold, and nothing else
// folded. And a rule anchored outside the project was matched against the root in screen form, which
// deletes ' " ` \ ^, so a rule whose `?`, negated class or escape stood for one of them refused
// project files at the host while the screen never learned its literal; the root is now matched as
// the host matches it. Each row below is red on 71e5133d on Windows, and the rule row in a Linux
// build too; the Kelvin rule row pins the host's own case rule.

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

// hostSpec is root as a rule anchored at the filesystem root spells it (`//c/q/proj` on Windows,
// `//q/proj` elsewhere), with its segment seg spelled as pat.
func hostSpec(t *testing.T, root, seg, pat string) string {
	t.Helper()
	p := filepath.ToSlash(root)
	if vol := filepath.VolumeName(root); vol != "" {
		p = "/" + strings.ToLower(vol[:1]) + p[len(vol):]
	}
	require.Contains(t, p, "/"+seg+"/", "fixture: %q holds the segment %q", p, seg)
	return "/" + strings.Replace(p, "/"+seg+"/", "/"+pat+"/", 1)
}

// keyRule stands in for one Read deny rule, spec, that refuses every `.key` file in root's project
// (`//<root>/**/*.key`, with one of the root's segments spelled as a pattern) where on is set, and
// refuses nothing otherwise (the rule does not match the root on this platform).
func keyRule(root, spec string, on bool) HostPaths {
	return func() HostRules {
		return HostRules{Patterns: []string{spec}, Refuses: func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			rel, err := filepath.Rel(root, filepath.Clean(p))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return false
			}
			return on && strings.HasSuffix(paths.Key(rel), ".key")
		}}
	}
}

// TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt: a rule anchored outside the project that
// refuses project files through the path below the root (`//<root>/**/*.key`) adds that part's
// literal (`.key`) to the screen (rootCover). The host matches the rule's raw segments against the
// root's with path.Match, its case folded where the platform's paths fold, but the screen matched
// them in screen form, which deletes ' " ` \ ^ from both: a rule that spells a root segment with a `?`
// for one of those (`o?brien` for `o'brien`), with a negated class whose `^` the screen form deleted
// (`[^x]brien` for `obrien`), or with an escape (`o\'brien`) refused the project's `.key` files at
// the host and never matched the root in the screen, so `cat certs/server.key` was shown. The root
// is matched as the host matches it. A rule spelled with the Kelvin sign for the root's `k` matches
// it where the host folds case, by Unicode, as the host's own rule reads it (a pin: the host's rules,
// unlike a path's spelling of the root, fold by Unicode, so the screen must too).
func TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	type row struct {
		name, seg, pat string
		on             bool
	}
	rows := []row{
		{"a question mark for an apostrophe", "o'brien", "o?brien", true},
		{"a question mark for a backtick", "a`b", "a?b", true},
		{"a question mark for a caret", "a^b", "a?b", true},
		{"a negated class", "obrien", "[^x]brien", true},
		{"an escaped apostrophe", "o'brien", `o\'brien`, true},
		{"the Kelvin sign for k", "kate", "\u212Aate", fold},
	}
	if fold {
		rows = append(rows, row{"a question mark in another case", "o'brien", "O?BRIEN", true})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			root := previewRoot(tc.seg, "proj")
			spec := hostSpec(t, root, tc.seg, tc.pat) + "/**/*.key"
			shown := []string{"cat src/main.go", "go vet ./src/main.go"}
			withheld := []string{"cat certs/server.key", "openssl x509 -in certs/server.key"}
			if !tc.on {
				shown, withheld = append(shown, withheld...), nil
			}
			requireScreened(t, root, keyRule(root, spec, tc.on), nil, shown, withheld, nil)
		})
	}
}

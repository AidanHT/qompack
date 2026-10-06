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

// Wave 19h's verify of the root's ASCII-only case fold (ADR 0011 §23, the two-fold rule). c16b21d5
// made every comparison of a path with the root fold an ASCII letter's case alone, which is right for
// a judgement that SHOWS (a root spelled with the Kelvin sign is a directory beside the project on
// NTFS), but it narrowed what the build LEARNS too. A path the build withholds teaches the screen its
// project-relative names (note, recordedPath), so that a glob, a selector or a text that names or
// selects it is withheld later. A recorded path under the root spelled with another case of a
// non-ASCII letter is the project's own path to the host, which lower-cases both a rule and a path
// (internal/hostperm), and to filepath.Rel's reading; under the ASCII-only fold it was outside the
// project, so its relative names went unlearned and `private/d*` was shown beside a withheld pointer
// to the denied private/deny.txt. A judgement that learns or withholds now reads the root both ways
// and learns under the union: a path the broad reading places in the project and the host refuses
// teaches its project-relative names. Each row is red on eca33155 on Windows for the spellings the
// host folds, and green on 71e5133d except for the dotted capital I, which only the host's
// lower-casing pairs with `i`.

// unicodeCaseSpellings are root segments beside a spelling of the same name that some reading of
// the root folds onto it, and whether the host's rules fold it there (hostFolds: internal/hostperm
// lower-cases a path and a rule by Unicode, strings.ToLower). The Latin capital A with ring above
// (U+00C5) is folded onto `å` by NTFS, Unicode's simple folding and the host; the Kelvin sign
// (U+212A) and the Angstrom sign (U+212B) by Unicode's simple folding and the host, not NTFS; the
// long s (U+017F) by Unicode's simple folding alone, so the host, judging the recorded spelling,
// refuses nothing under it and it teaches no project-relative name; and the capital I with dot above
// (U+0130) by the host alone. hostFoldRules judges the recorded spelling alone, as the daemon's
// adapter does while the root resolves to itself; under a root that resolves elsewhere the adapter
// also judges the broad reading's place below the resolved root, so it refuses the long s's spelling
// wherever the host refuses the project's file (ADR 0011 §23).
var unicodeCaseSpellings = []struct {
	name, seg, variant string
	hostFolds          bool
}{
	{"U+00C5 for å", "\u00E5sa", "\u00C5sa", true},
	{"U+212A for k", "kate", "\u212Aate", true},
	{"U+017F for s", "sam", "\u017Fam", false},
	{"U+212B for å", "\u00E5sa", "\u212Bsa", true},
	{"U+0130 for i", "iris", "\u0130ris", true},
}

// hostFoldRules stands in for the host's Read deny rules on exact project files, `./<file>`, read as
// internal/hostperm reads them: a path, relative or absolute, is refused when it and the rule's file
// under root are equal once both are lower-cased where the platform's paths fold (paths.Key, which
// hostperm's strings.ToLower is).
func hostFoldRules(root string, patterns ...string) HostPaths {
	refuses := func(p string) bool {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		key := paths.Key(filepath.ToSlash(filepath.Clean(p)))
		for _, pt := range patterns {
			if key == paths.Key(filepath.ToSlash(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pt, "./"))))) {
				return true
			}
		}
		return false
	}
	return func() HostRules { return HostRules{Refuses: refuses, Patterns: patterns} }
}

// requireLearned builds root's payload under the host rules patterns, with the recorded file pointers
// files and the recorded summaries recorded (each withheld), and requires each of globs to be withheld
// when learned is set and shown otherwise; no spelling in leaks reaches the payload.
func requireLearned(t *testing.T, root string, patterns, files, recorded, globs, leaks []string, learned bool) {
	t.Helper()
	withheld := append([]string(nil), recorded...)
	var shown []string
	if learned {
		withheld = append(withheld, globs...)
	} else {
		shown = globs
	}
	requireScreened(t, root, hostFoldRules(root, patterns...), files, shown, withheld, leaks)
}

// foldsPaths reports whether this platform's paths fold case (Windows and macOS).
func foldsPaths() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames: a file pointer recorded
// under the root spelled with another case of a non-ASCII letter (`C:\q\Åsa\proj\private\deny.txt`
// for a root `C:\q\åsa\proj`) is withheld, and where the host refuses it as the project's denied
// private/deny.txt it teaches the build that project-relative path, so a glob that selects it is
// withheld. Where paths do not fold the recorded path is another directory's, which the host does not
// refuse, and the globs are shown.
func TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames(t *testing.T) {
	for _, rc := range unicodeCaseSpellings {
		t.Run(rc.name, func(t *testing.T) {
			root := previewRoot(rc.seg, "proj")
			pointer := filepath.Join(previewRoot(rc.variant, "proj"), "private", "deny.txt")
			requireLearned(t, root, []string{"./private/deny.txt"}, []string{pointer}, nil,
				[]string{"private/d*", "private/de?y.txt"}, []string{rc.variant}, foldsPaths() && rc.hostFolds)
		})
	}
}

// TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames: a
// NotebookEdit's path-named value recorded under a root with a space, spelled with another case of a
// non-ASCII letter (`C:\q\Åsa berg\proj\private\deny.txt` for a root `C:\q\åsa berg\proj`), is
// withheld, and where the host refuses it teaches the build the project-relative path it names:
// recordedPath holds the root in it under the broad reading too, so the root's own space does not
// leave the value unlearned, and a glob that selects private/deny.txt is withheld.
func TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames(t *testing.T) {
	for _, rc := range unicodeCaseSpellings {
		t.Run(rc.name, func(t *testing.T) {
			root := previewRoot(rc.seg+" berg", "proj")
			recorded := notebookPreview(t, filepath.Join(previewRoot(rc.variant+" berg", "proj"), "private", "deny.txt"))
			requireLearned(t, root, []string{"./private/deny.txt"}, nil, []string{recorded},
				[]string{"private/d*", "private/de?y.txt"}, []string{rc.variant}, foldsPaths() && rc.hostFolds)
		})
	}
}

// TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath: a file pointer
// to the denied private/id recorded under the root spelled with another case of a non-ASCII letter
// teaches the build no basename (`id` is too short to screen free text by) but, where the host
// refuses it, its project-relative path, which a glob selects: `private/i?` is withheld.
func TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath(t *testing.T) {
	for _, rc := range unicodeCaseSpellings {
		t.Run(rc.name, func(t *testing.T) {
			root := previewRoot(rc.seg, "proj")
			pointer := filepath.Join(previewRoot(rc.variant, "proj"), "private", "id")
			requireLearned(t, root, []string{"./private/id"}, []string{pointer}, nil,
				[]string{"private/i?"}, []string{rc.variant}, foldsPaths() && rc.hostFolds)
		})
	}
}

// TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted: a drop
// entry's reason that names the project's denied private/deny.txt through the root spelled with
// another case of a non-ASCII letter, glued to the text around it (`(C:\q\Åsa\proj\private\deny.txt)`,
// `gitdir='…'`), is redacted. The reason screen finds a withheld path's whole spelling where a path
// starts, after the root held as one mark; since c16b21d5 the root was held as its own spelling folds
// ASCII letters alone, so the variant was no root and the reason was shown. It withholds, so it now
// holds the root under the broad reading too (holdRootBroad), whichever letter the variant spells.
// Where paths do not fold the variant is a directory beside the project, and a reason that glues an
// outside path to other text is not read as one on any revision (the reason screen reads an outside
// path as a whitespace-delimited token or as an operation's path), so the row runs where paths fold.
func TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted(t *testing.T) {
	if !foldsPaths() {
		t.Skip("platform: where paths do not fold the variant is a directory beside the project; see the comment")
	}
	for _, rc := range unicodeCaseSpellings {
		t.Run(rc.name, func(t *testing.T) {
			root := previewRoot(rc.seg, "proj")
			denied := filepath.Join(previewRoot(rc.variant, "proj"), "private", "deny.txt")
			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
			cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: .git file names gitdir (" + denied + ")"},
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: gitdir='" + denied + "'"})
			d := uat05Deps(t, cp)
			d.HostPaths = hostFoldRules(root, "./private/deny.txt")
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			var details []string
			for _, e := range res.Dropped {
				if e.Kind == "pointer_git_unavailable" {
					details = append(details, e.Detail)
				}
			}
			require.Len(t, details, 2, "the git drops are still reported: %v", res.Dropped)
			requireNoLeak(t, res, []string{rc.variant, "deny.txt"})
		})
	}
}

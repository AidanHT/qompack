package rehydrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Coordinator decision D64's rulings on wave 19f's open items (ADR 0011 §23): a cut path-named value
// is the project root only when it is the start of the root's own spelling, byte for byte; a
// character that Windows' ANSI best-fit conversion turns into ASCII punctuation is outside the
// free-text whitelist and the root unit on every platform; and a root with a word that starts with
// `-` after one of its spaces has no root unit. The cut, best-fit and dash rows are red on 895d41f4;
// the learning row pins what the rulings leave as it is.

// bestFitANSI are the code points the free-text whitelist read as letters, marks or digits that
// Windows' ANSI best-fit conversion (WideCharToMultiByte without WC_NO_BEST_FIT_CHARS) maps to one
// ASCII character other than a letter or a digit, in at least one of Windows' ANSI code pages,
// measured by TestWhitelist_NoANSIBestFitToPunctuationIsSafe on Windows: U+01C0 to `|`, U+01C3 to
// `!`, U+02B9, U+02BC and U+02C8 to `'`, U+02BA to `"`, U+02C6 and U+02C7 to `^`, U+02CB to `'` or
// a backtick, U+02CD to `_`, U+0300 to `'` or a backtick, U+0302 to `^`, U+0303 to `~`, U+030E to
// `"`, and U+0331 and U+0332 to `_` (code pages 1250, 1252 and 1254 for most; 1254 alone for
// U+02C7).
var bestFitANSI = []rune{
	0x01C0, 0x01C3, 0x02B9, 0x02BA, 0x02BC, 0x02C6, 0x02C7, 0x02C8, 0x02CB, 0x02CD,
	0x0300, 0x0302, 0x0303, 0x030E, 0x0331, 0x0332,
}

// bestFitRunes are the code points the rulings take out of the whitelist and the root unit: every
// code point of the Spacing Modifier Letters block (U+02B0 to U+02FF), where most of the best-fit
// mappings to ASCII punctuation lie, and the measured ones outside it (bestFitANSI).
func bestFitRunes() []rune {
	var rs []rune
	for r := rune(0x02B0); r <= 0x02FF; r++ {
		rs = append(rs, r)
	}
	for _, r := range bestFitANSI {
		if r < 0x02B0 || r > 0x02FF {
			rs = append(rs, r)
		}
	}
	return rs
}

// jsonBody is s as the body of a JSON string, as the store's canonical JSON escapes it.
func jsonBody(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(s))
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}

// cutJSONValue is the store's cut preview of a NotebookEdit-shaped call whose path-named argument
// name holds value, cut right after kept, a start of value: the JSON-escaped kept survives the cut,
// and nothing of value after it.
func cutJSONValue(t *testing.T, name, value, kept string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(value, kept), "fixture: %q starts %q", kept, value)
	esc, keep := jsonBody(t, value), jsonBody(t, kept)
	require.True(t, strings.HasPrefix(esc, keep), "fixture: the escaped prefix")
	head, mid := `{"cell_id":"c1","new_source":"`, `","`+name+`":"`
	pad := previewWidth - len(previewEllipsis) - len(head) - len(mid) - len(keep)
	require.Positive(t, pad, "fixture: the value starts inside the preview")
	s := storeCut(head + strings.Repeat("x", pad) + mid + esc + `"}`)
	require.Equal(t, head+strings.Repeat("x", pad)+mid+keep+previewEllipsis, s, "fixture: the store's cut")
	return s
}

// cutSecondPath is the store's cut preview of a call whose `paths` array holds a project file and
// then value, cut right after kept: two path-named values, which ask the host nothing.
func cutSecondPath(t *testing.T, value, kept string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(value, kept), "fixture: %q starts %q", kept, value)
	esc, keep := jsonBody(t, value), jsonBody(t, kept)
	head, mid := `{"paths":["src/`, `.go","`
	pad := previewWidth - len(previewEllipsis) - len(head) - len(mid) - len(keep)
	require.Positive(t, pad, "fixture: the value starts inside the preview")
	s := storeCut(head + strings.Repeat("x", pad) + mid + esc + `"]}`)
	require.Equal(t, head+strings.Repeat("x", pad)+mid+keep+previewEllipsis, s, "fixture: the store's cut")
	return s
}

// asciiUpper is s with its ASCII letters in upper case and every other byte as it is.
func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling is D64's ruling on wave 19f's final verify: a
// path-named JSON value the store's cut fell inside was the project (rootPrefix) when it was a start
// of the root's spelling compared in screen form, which deletes ' " ` \ ^, folds whitespace runs,
// reads `\` as `/`, collapses a repeated separator and folds case by Unicode, so a cut value that
// differed from the root only there was shown, though it names a directory beside the project
// (`<q>/John'athan`, `<q>/John  Smith`, `<q>/obrien` beside a root `<q>/o'brien`, the Kelvin sign
// beside a root `kate`, which NTFS does not fold). The value is now compared with the root's own
// spelling byte for byte: the cleaned root in the platform's separators or, on Windows, in `/`
// throughout, ASCII letters' case folded only on Windows and macOS, nothing deleted and no
// whitespace folded; any other value is judged by the directory it spells, outside the project. A
// repeated separator (`<q>//Johnathan`), which names the same directory, is over-withheld.
func TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling(t *testing.T) {
	sep := string(filepath.Separator)
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	// within is value cut right after the first occurrence of mark and the n bytes after it.
	within := func(value, mark string, n int) string {
		i := strings.Index(value, mark)
		require.GreaterOrEqual(t, i, 0, "fixture: %q holds %q", value, mark)
		return value[:i+len(mark)+n]
	}
	t.Run("Johnathan", func(t *testing.T) {
		root := previewRoot("Johnathan", "proj")
		value := filepath.Join(root, "nb", "a.ipynb")
		kept := within(value, "John", 2)
		shown := []string{cutJSONValue(t, "notebook_path", value, kept), cutSecondPath(t, value, kept)}
		if runtime.GOOS == "windows" {
			shown = append(shown, cutJSONValue(t, "notebook_path", filepath.ToSlash(value), filepath.ToSlash(kept)))
		}
		var withheld []string
		if fold {
			shown = append(shown, cutJSONValue(t, "notebook_path", asciiUpper(value), asciiUpper(kept)))
		} else {
			withheld = append(withheld, cutJSONValue(t, "notebook_path", asciiUpper(value), asciiUpper(kept)))
		}
		marks := []string{"'", "`", "^"}
		if runtime.GOOS != "windows" {
			marks = append(marks, `"`, `\`)
		}
		for _, c := range marks {
			sib := filepath.Join(previewRoot("John"+c+"athan", "proj"), "nb", "a.ipynb")
			withheld = append(withheld,
				cutJSONValue(t, "notebook_path", sib, within(sib, "John"+c, 2)),
				cutSecondPath(t, sib, within(sib, "John"+c, 2)))
		}
		doubled := strings.Replace(value, sep+"Johnathan", sep+sep+"Johnathan", 1)
		withheld = append(withheld, cutJSONValue(t, "notebook_path", doubled, within(doubled, "John", 2)))
		if runtime.GOOS != "windows" {
			// A POSIX name may hold a backslash: `/q\Johnathan` is the entry `q\Johnathan` of `/`.
			bs := strings.Replace(value, "/Johnathan", `\Johnathan`, 1)
			withheld = append(withheld, cutJSONValue(t, "notebook_path", bs, within(bs, "John", 2)))
		}
		requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown, withheld,
			[]string{"John'at", "John`at", "John^at"})
	})
	t.Run("John Smith", func(t *testing.T) {
		root := previewRoot("John Smith", "proj")
		value := filepath.Join(root, "nb", "a.ipynb")
		shown := []string{cutJSONValue(t, "notebook_path", value, within(value, "John S", 1))}
		sibs := []string{"John  Smith"}
		if runtime.GOOS != "windows" {
			sibs = append(sibs, "John\tSmith")
		}
		var withheld []string
		for _, s := range sibs {
			sib := filepath.Join(previewRoot(s, "proj"), "nb", "a.ipynb")
			withheld = append(withheld,
				cutJSONValue(t, "notebook_path", sib, within(sib, s[:len(s)-4], 1)),
				cutSecondPath(t, sib, within(sib, s[:len(s)-4], 1)))
		}
		requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown, withheld, []string{"John  S"})
	})
	t.Run("o'brien", func(t *testing.T) {
		root := previewRoot("o'brien", "proj")
		value := filepath.Join(root, "nb", "a.ipynb")
		sib := filepath.Join(previewRoot("obrien", "proj"), "nb", "a.ipynb")
		requireScreened(t, root, hostRules(root, uat12Rules...), nil,
			[]string{cutJSONValue(t, "notebook_path", value, within(value, "o'b", 2))},
			[]string{
				cutJSONValue(t, "notebook_path", sib, within(sib, "ob", 2)),
				cutSecondPath(t, sib, within(sib, "ob", 2)),
			},
			[]string{"obri"})
	})
	t.Run("kate", func(t *testing.T) {
		root := previewRoot("kate", "proj")
		value := filepath.Join(root, "nb", "a.ipynb")
		sib := filepath.Join(previewRoot("\u212Aate", "proj"), "nb", "a.ipynb")
		requireScreened(t, root, hostRules(root, uat12Rules...), nil,
			[]string{cutJSONValue(t, "notebook_path", value, within(value, "ka", 1))},
			[]string{cutJSONValue(t, "notebook_path", sib, within(sib, "\u212Aa", 1))},
			[]string{"\u212Aat"})
	})
}

// TestBuild_ABestFitCharacterIsOutsideTheWhitelist is D64's ruling on Windows' best-fit conversion:
// a program that takes its command line through the ANSI code page (a C program's argv,
// GetCommandLineA) receives each character that code page cannot hold as its best fit, and some
// best fits are ASCII punctuation (U+02BA is `"`, U+02B9, U+02BC and U+02C8 are `'`, U+0303 is `~`),
// so a word the whitelist read as letters reached such a program with a quote, an escape or a home
// in it. Every code point of the Spacing Modifier Letters block, and each measured best fit to ASCII
// punctuation outside it (bestFitANSI), is now unsafe on every platform: in a word, a quoted run, an
// http(s) URL and a JSON string value alike. A letter or mark whose best fit is a letter, or only an
// OEM code page's punctuation (U+0301, which code pages 437 and 862 turn into `'`), stays safe.
func TestBuild_ABestFitCharacterIsOutsideTheWhitelist(t *testing.T) {
	root := previewRoot("proj")
	for _, r := range bestFitRunes() {
		s := string(r)
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, nil,
				[]string{
					"cat docs/a" + s + "b.md",
					`cat "docs/a` + s + `b c.md"`,
					"curl https://x.example/a" + s + "b",
					`{"query":"a` + s + `b"}`,
				},
				nil)
		})
	}
	t.Run("letters", func(t *testing.T) {
		requireScreened(t, root, hostRules(root, uat12Rules...), nil,
			[]string{
				"cat docs/café.md", "cat docs/cafe\u0301.md", "cat docs/Ελληνικά.md", `cat "docs/naïve c.md"`,
				"curl https://x.example/café", `{"query":"naïve résumé"}`,
			},
			nil, nil)
	})
}

// TestBuild_ABestFitRootHoldsNoRootUnit is the same ruling for the root unit: a root holding a code
// point the whitelist no longer reads as a letter (bestFitRunes) has no unit, so a summary spelling it
// is judged as the free text it is and withheld, while a path-named JSON value under it, which reaches
// no shell, is still shown.
func TestBuild_ABestFitRootHoldsNoRootUnit(t *testing.T) {
	for _, r := range bestFitRunes() {
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			root := previewRoot("a"+string(r)+"b", "proj")
			requireScreened(t, root, hostRules(root, uat12Rules...), nil,
				[]string{notebookPreview(t, filepath.Join(root, "src", "x.ipynb"))},
				append(rootSummaries(root), "cd "+root+" && cat private/deny.txt"),
				[]string{"deny.txt"})
		})
	}
}

// TestBuild_ARootWithADashWordHoldsNoRootUnit is D64's ruling on the round-2 verify's open shape: a
// word of the root that starts with `-` after one of its spaces binds as a parameter of a PowerShell
// cmdlet or advanced function (`g C:\q\John -Force\proj` sets `-Force`), so a root with one, a lone
// `-` among them (`OneDrive - Contoso`), has no unit. A `-` inside a word, or starting a segment after
// a separator, keeps it.
func TestBuild_ARootWithADashWordHoldsNoRootUnit(t *testing.T) {
	for _, seg := range []string{"John -Force", "a -b", "OneDrive - Contoso", "x --y", "a -"} {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			requireScreened(t, root, hostRules(root, uat12Rules...), nil,
				[]string{notebookPreview(t, filepath.Join(root, "src", "x.ipynb"))},
				append(rootSummaries(root), "cd "+root+" && cat private/deny.txt", filepath.Join(root+"2", "x.txt")),
				[]string{"deny.txt"})
		})
	}
	for _, seg := range []string{"John Smith-Jones", "-x", "a-b c"} {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, rootSummaries(root),
				[]string{"cd " + root + " && cat private/deny.txt", filepath.Join(root+"2", "x.txt")},
				[]string{"deny.txt"})
		})
	}
}

// TestBuild_AWithheldPathIsLearnedUnderEveryRoot pins D64's ratification that recordedPath keeps
// holding the project root whatever its spelling: learning a withheld path's names only ever
// withholds more, so a withheld one-word Read under a root with no unit (an apostrophe, a run of
// spaces, a word that starts with `-`, a best-fit character) still teaches the screen its basename,
// and a free text naming that file alone is withheld.
func TestBuild_AWithheldPathIsLearnedUnderEveryRoot(t *testing.T) {
	for _, seg := range []string{"John O'Brien", "a  b", "John -Force", "a \u02BAb", "John Smith"} {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			requireScreened(t, root, hostRules(root, "./secrets/**"), nil, []string{"go test ./..."},
				[]string{filepath.Join(root, "secrets", "token.txt"), "type token.txt"},
				[]string{"token.txt"})
		})
	}
}

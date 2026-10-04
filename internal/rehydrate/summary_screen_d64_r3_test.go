package rehydrate

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Coordinator decision D64's rulings on wave 19f's open items (ADR 0011 §23): a cut path-named value
// is the project root only when it is the start of the root's own spelling, byte for byte. The row
// is red on 895d41f4.

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

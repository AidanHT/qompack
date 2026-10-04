package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// asciiUpperPath is p with its ASCII letters in upper case and every other byte as it is.
func asciiUpperPath(p string) string {
	b := []byte(p)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// cutNotebookPreview is the store's preview of a NotebookEdit of value with a cell padded so that the
// store's cut falls right after kept, a start of value.
func cutNotebookPreview(t *testing.T, value, kept string) string {
	t.Helper()
	head, mid, esc := `{"new_source":"`, `","notebook_path":"`, strings.ReplaceAll(kept, `\`, `\\`)
	pad := 120 - len("…") - len(head) - len(mid) - len(esc)
	require.Positive(t, pad, "fixture: the value starts inside the preview")
	raw, err := json.Marshal(map[string]any{"new_source": strings.Repeat("x", pad), "notebook_path": value})
	require.NoError(t, err)
	_, preview := store.ArgsDigest(raw)
	require.Equal(t, head+strings.Repeat("x", pad)+mid+esc+"…", preview, "fixture: the store's cut")
	return preview
}

// TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt is wave 19g's final verify
// of D64 through the real host rules and the store's own previews: a directory spelled with the
// Kelvin sign (U+212A), the long s (U+017F) or the Angstrom sign (U+212B) where the project's root
// has `k`, `s` or `å` is another directory on NTFS, but the root's spelling and containment folded
// case by Unicode, so a Read, a NotebookEdit, two commands and a NotebookEdit cut past the end of the
// root naming a file in it were shown. Each is withheld; the same calls spelled with the real root's
// ASCII letters in the other case are shown where the platform's paths fold, through the adapter's
// judgement of the root's resolved spelling too.
func TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	for _, rc := range []struct{ seg, sib string }{
		{"kate", "\u212Aate"}, {"sam", "\u017Fam"}, {"\u00E5sa", "\u212Bsa"},
	} {
		t.Run(rc.seg, func(t *testing.T) {
			root := uat12Project(t, rc.seg, "proj")
			sib := filepath.Join(filepath.Dir(filepath.Dir(root)), rc.sib, "proj")
			writeProjectFile(t, sib, "src/main.go")
			writeProjectFile(t, sib, "nb/a.ipynb")
			rootInfo, err := os.Stat(paths.Long(root))
			require.NoError(t, err)
			sibInfo, err := os.Stat(paths.Long(sib))
			require.NoError(t, err)
			require.False(t, os.SameFile(rootInfo, sibInfo), "fixture: the sibling is another directory")

			calls := func(r string) []string {
				nb := filepath.Join(r, "nb", "a.ipynb")
				return []string{
					storePreview(t, map[string]string{"file_path": filepath.Join(r, "src", "main.go")}),
					storePreview(t, map[string]string{"notebook_path": nb}),
					storePreview(t, map[string]string{"command": "cat " + filepath.Join(r, "src", "main.go")}),
					storePreview(t, map[string]string{"command": "git -C " + r + " status"}),
					cutNotebookPreview(t, nb, nb[:len(nb)-len("ipynb")]),
				}
			}
			res := requireToolSummaries(t, root, nil, calls(sib))
			require.NotContains(t, res.Text, rc.sib)
			upper := asciiUpperPath(root)
			if fold {
				requireToolSummaries(t, root, calls(upper), nil)
			} else {
				requireToolSummaries(t, root, nil, calls(upper))
			}
		})
	}
}

// TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt is the rule half of wave 19g's
// final verify through the real host rules: a project deny rule spelled from the filesystem root
// refuses every `.key` file of the project, its root segment spelled with a `?` for the root's
// apostrophe, a negated class, or an escaped apostrophe. The host refuses `certs/server.key`, but the
// screen matched the rule against the root in screen form, which deletes the apostrophe and the
// caret, found it covered nothing, and showed `cat certs/server.key`. It is withheld.
func TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt(t *testing.T) {
	for _, tc := range []struct{ seg, pat string }{
		{"o'brien", "o?brien"}, {"obrien", "[^x]brien"}, {"o'brien", `o\\'brien`},
	} {
		t.Run(tc.pat, func(t *testing.T) {
			root := shortProjectDir(t, tc.seg, "proj")
			posix := filepath.ToSlash(root)
			if vol := filepath.VolumeName(root); vol != "" {
				posix = "/" + strings.ToLower(vol[:1]) + posix[len(vol):]
			}
			spec := strings.Replace(posix, "/"+tc.seg+"/", "/"+tc.pat+"/", 1)
			require.NotEqual(t, posix, spec, "fixture: the rule spells the root's segment as a pattern")
			writeProjectSettings(t, root, `{"permissions":{"deny":["Read(/`+spec+`/**/*.key)"]}}`)
			writeProjectFile(t, root, "certs/server.key")
			writeProjectFile(t, root, "src/main.go")
			refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
			require.NotNil(t, refuses)
			require.True(t, refuses("certs/server.key"), "fixture: the host refuses the project's .key files")
			require.False(t, refuses("src/main.go"), "fixture: and nothing else")

			bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
			res := requireToolSummaries(t, root,
				[]string{bash("cat src/main.go"), bash("go vet ./src/main.go")},
				[]string{bash("cat certs/server.key"), bash("openssl x509 -in certs/server.key")})
			require.NotContains(t, res.Text, "server.key")
		})
	}
}

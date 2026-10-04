package daemon

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
)

// Wave 22's daemon rows over audit 2's rehydrate findings (coordinator decisions D66 and D67).

// TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece is audit 2's finding
// 26 through the real adapter, the real host rules and the store's own previews: a path-named JSON
// value holding a list whose later piece names a path outside the project was judged as one relative
// path, which the host, joining it under the root, refused nothing about, and section 6 showed it.
// Each piece is judged for a path outside the project; a single project path with a space in it, in
// a plain project and in one whose own path has a space, is still shown. Wave 22's verify found a
// path after a quote and a climb after an option's `=` or an `@` still shown (eca33155 showed them
// too): each is judged where a reader starts the path, and the names a project's paths hold are
// still shown.
func TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := uat12Project(t, elem...)
			out := filepath.Join(shortProjectDir(t, "outside"), "x.txt")
			v := func(key string, value any) string { return storePreviewOf(t, map[string]any{key: value}) }
			res := requireToolSummaries(t, root,
				[]string{
					v("file", "src/my notes.txt"),
					v("paths", []string{"docs/design notes.md", "src/main.go"}),
					v("files", "src/main.go src/util.go"),
					v("notebook_path", filepath.Join(root, "nb", "my notes.ipynb")),
					v("paths", "src/main.go "+filepath.Join(root, "docs", "b.md")),
					v("paths", []string{"app/(auth)/page.tsx", "pages/[slug].tsx", "src/routes/+page.svelte"}),
					v("paths", `"src/main.go" "src/util.go"`),
					v("notebook_path", `"`+filepath.Join(root, "nb", "a b.ipynb")+`"`),
					v("paths", "src/main.go --out=docs/b.md"),
				},
				[]string{
					v("paths", "src/main.go,"+out),
					v("paths", "src/main.go "+out),
					v("file", "src/main.go /etc/passwd"),
					v("paths", "src/main.go ~/.ssh/id_rsa"),
					v("paths", "src/main.go $HOME/.aws/credentials"),
					v("paths", []string{"src/main.go", "b " + out}),
					v("paths", "src/main.go C:secret.txt"),
					v("paths", "src/main.go Temp:secret.txt"),
					v("paths", "src/main.go;"+out),
					v("paths", "src/main.go:/etc/passwd"),
					v("path", "Temp:secret.txt"),
					v("paths", `src/main.go "/etc/passwd"`),
					v("paths", `src/main.go '~/.ssh/id_rsa'`),
					v("paths", []string{"src/main.go", `"/etc/passwd"`}),
					v("path", `"/etc/passwd"`),
					v("paths", `src/main.go "`+out+`"`),
					v("paths", "src/main.go (/etc/passwd)"),
					v("paths", "src/main.go --out=../../outside/x.txt"),
					v("paths", "src/main.go @../outside/x.txt"),
					v("paths", "src/main.go:../outside/x.txt"),
					v("path", "--out=../outside/x.txt"),
				})
			for _, leak := range []string{filepath.Join("outside", "x.txt"), "passwd", "id_rsa", "credentials", "secret.txt", "../outside"} {
				require.NotContains(t, res.Text, leak)
			}
		})
	}
}

// longestTempBase is the longest temporary directory a hosted runner hands this platform's tests,
// with os.MkdirTemp's longest suffix (a uint32's ten digits) under it: on macOS the TMPDIR
// /var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T (48 characters, read from hosted logs, ci
// 36981590450 and nightly 36981711009), which every POSIX row is held to; on Windows windows-latest's
// TEMP in its long form, C:\Users\runneradmin\AppData\Local\Temp, where JSON's doubled backslash is
// the tighter limit. audit 2's finding 36: shortProjectDir spelled a macOS root from TMPDIR, and the
// Docker fixture's preview, about 113 characters plus the suffix, passed the store's 120-character
// width only for a suffix of seven digits or fewer.
func longestTempBase() string {
	if runtime.GOOS == "windows" {
		return `C:\Users\runneradmin\AppData\Local\Temp\q4294967295`
	}
	return "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/q4294967295"
}

// TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory is finding 36's regression
// row: the UAT-12 rows' previews that spell the project root, built under the longest temporary
// directory a hosted runner spells, are each uncut, so a row cannot pass on a short local temporary
// directory and fail on a hosted runner. storePreview and storePreviewOf hold every other row's
// previews to the same directory.
func TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory(t *testing.T) {
	root := filepath.Join(longestTempBase(), "John Smith", "proj")
	shown, withheld := commonIdiomPreviews(root)
	us, uw := usefulSummaryPreviews(root)
	for _, args := range append(append(append(shown, withheld...), us...), uw...) {
		raw, err := json.Marshal(args)
		require.NoError(t, err)
		_, preview := store.ArgsDigest(raw)
		require.False(t, strings.HasSuffix(preview, "…"), "the store cuts the preview of %v", args)
	}
}

package daemon

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Wave 22's daemon rows over audit 2's rehydrate findings (coordinator decisions D66 and D67).

// TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece is audit 2's finding
// 26 through the real adapter, the real host rules and the store's own previews: a path-named JSON
// value holding a list whose later piece names a path outside the project was judged as one relative
// path, which the host, joining it under the root, refused nothing about, and section 6 showed it.
// Each piece is judged for a path outside the project; a single project path with a space in it, in
// a plain project and in one whose own path has a space, is still shown.
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
				})
			for _, leak := range []string{filepath.Join("outside", "x.txt"), "passwd", "id_rsa", "credentials", "secret.txt"} {
				require.NotContains(t, res.Text, leak)
			}
		})
	}
}

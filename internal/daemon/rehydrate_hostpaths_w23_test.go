package daemon

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

// Wave 23's daemon rows over candidate 8's diff verify (coordinator decisions D66 and D67(l)).

// TestRehydrateHostPaths_AWithheldNameAfterAGluingCharacterIsWithheld is candidate 8's diff verify,
// findings 8 and 9, through the real adapter, the real host rules and the store's own previews. The
// host judges a path-named value that holds several paths as one path and refuses nothing, so the
// rule-literal and withheld-name screen alone judges its in-project pieces.
//
// Finding 8: a `.env` file pointer the host refuses teaches the build the name `.env`, and with
// UAT-12's `Read(./.env)` the rule's literal names it too; a NUL between `README.md` and `.env`,
// which the store's preview keeps as \u0000, was dropped by the screen's spelling and glued the
// two into `README.md.env`, so section 6 showed it (77374c3c).
//
// Finding 9: a directory link lnk into secrets/, which `Read(./secrets/**)` refuses through the
// link, is a path no rule's literal spells; the build learns lnk/token.txt from its file pointer. A
// value whose first piece is the root and a path in one separator style was screened by the rules'
// literals alone, as the one exact path the host judged, though the host judged the whole value and
// its later piece lnk/token.txt was never asked about, so section 6 showed it (77374c3c). A single
// rooted path, and a list of project paths, are still shown.
//
// The same verify found the gluing in free text: a string of a canonical-JSON preview that no
// path-named argument holds (an MCP tool's `query`) is judged by the whitelist, and a NUL in it,
// which the store's preview keeps as an escape, was dropped before the whitelist read it, so
// `x<NUL>/etc/passwd` was the one relative word `x/etc/passwd` and was shown (77374c3c).
func TestRehydrateHostPaths_AWithheldNameAfterAGluingCharacterIsWithheld(t *testing.T) {
	t.Run("learned .env", func(t *testing.T) {
		root := uat12Project(t, "proj")
		v := func(value string) string { return storePreviewOf(t, map[string]any{"paths": value}) }
		res := requireToolSummariesBeside(t, root, []string{".env"},
			[]string{v("README.md\x00.env.example"), v("README.md\x00src/main.go")},
			[]string{v("README.md\x00.env"), v("README.md\u00a0.env"), v("README.md\u02ba.env")})
		require.NotContains(t, res.Text, `.env"`)
	})
	t.Run("link", func(t *testing.T) {
		root := shortProjectDir(t, "proj")
		writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./secrets/**)"]}}`)
		writeProjectFile(t, root, "secrets/token.txt")
		writeProjectFile(t, root, "src/main.go")
		writeProjectFile(t, root, "src/util.go")
		require.NoError(t, makeDirLink(filepath.Join(root, "lnk"), filepath.Join(root, "secrets")))
		refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
		require.NotNil(t, refuses)
		require.True(t, refuses(filepath.Join("lnk", "token.txt")), "fixture: the host refuses the file through the link")
		main := filepath.Join(root, "src", "main.go")
		require.False(t, refuses(main+" "+filepath.Join("lnk", "token.txt")),
			"fixture: the host, asked about the whole value, refuses nothing")

		v := func(value string) string { return storePreviewOf(t, map[string]any{"paths": value}) }
		res := requireToolSummariesBeside(t, root, []string{filepath.Join("lnk", "token.txt")},
			[]string{v(main), v(main + " " + filepath.Join("src", "util.go"))},
			[]string{
				v(main + " " + filepath.Join("lnk", "token.txt")),
				v(main + ";" + filepath.Join("lnk", "token.txt")),
				v(main + "\x00" + filepath.Join("lnk", "token.txt")),
			})
		require.NotContains(t, res.Text, "token.txt")
	})
	t.Run("free text", func(t *testing.T) {
		root := uat12Project(t, "proj")
		v := func(value string) string { return storePreviewOf(t, map[string]any{"query": value}) }
		res := requireToolSummaries(t, root, []string{v("foo\x00bar")},
			[]string{v("x\x00/etc/passwd"), v("x\x1f~/.ssh/id_rsa"), v("src/main.go\x00.env"), v("cat src/main.go\x00secrets/token.txt")})
		for _, leak := range []string{"passwd", "id_rsa", `.env"`, "token.txt"} {
			require.NotContains(t, res.Text, leak)
		}
	})
}

package daemon

import (
	"fmt"
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

// TestRehydrateHostPaths_ARefusedNameHoldingAPieceCharacterIsWithheld is fix round 1's review of
// candidate 8's diff verify, finding 8, through the real adapter, the real host rules and the store's
// own previews, in a plain project and in one whose own path has a space. The round screened a
// path-named value as the store spells it and in a name form where every character a piece starts or
// ends at is a space; a refused name that itself holds such a character (an apostrophe, an en dash,
// a comma) was whole in neither reading when a NUL, an NBSP, U+2028 or a zero-width space glued it to
// the piece before it or after it, and section 6 showed the exact path the host refuses (shown at
// 77374c3c too). So did a name the build learned as withheld that holds one (`bob's keys.txt`, from
// a file pointer the host refuses through a link). The minor half: a glob piece after an ASCII space,
// a `:` or a parenthesis, and a rooted glob after a NUL under a root with a space in it, were judged
// only as the whole value, which selects nothing, and shown, though the glob selects lnk/token.txt,
// a path the build learned as withheld. A name that only begins like the refused one, a list of
// project paths and a glob that selects only project paths are still shown.
func TestRehydrateHostPaths_ARefusedNameHoldingAPieceCharacterIsWithheld(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			t.Run("literal", func(t *testing.T) {
				root := shortProjectDir(t, elem...)
				names := []string{"o'brien.env", "q3\u2013secrets.xlsx", "a,b.env"}
				writeProjectSettings(t, root,
					`{"permissions":{"deny":["Read(./o'brien.env)","Read(./q3\u2013secrets.xlsx)","Read(./a,b.env)"]}}`)
				for _, f := range append([]string{"src/main.go", "src/util.go"}, names...) {
					writeProjectFile(t, root, f)
				}
				refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
				require.NotNil(t, refuses)
				for _, n := range names {
					require.True(t, refuses(n), "fixture: the host refuses %s", n)
					require.False(t, refuses("src/main.go\x00"+n), "fixture: the host, asked about the whole value, refuses nothing")
				}
				v := func(value any) string { return storePreviewOf(t, map[string]any{"paths": value}) }
				for i, s := range []string{
					v("src/main.go\x00o'brien.env"),
					v("src/main.go\u00a0o'brien.env"),
					v("src/main.go\u2028o'brien.env"),
					v("src/main.go\u200bo'brien.env"),
					v("o'brien.env\x00src/main.go"),
					v("o'brien.env\u00a0src/main.go"),
					v("src/main.go\x00q3\u2013secrets.xlsx"),
					v("src/main.go\u00a0q3\u2013secrets.xlsx"),
					v("src/main.go\x00a,b.env"),
					v([]string{"docs/b.md", "src/main.go\x00o'brien.env"}),
					v("src/main.go o'brien.env"),
					v("o'brien.env"),
				} {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						res := requireToolSummaries(t, root, nil, []string{s})
						for _, leak := range []string{`o'brien.env"`, `o'brien.env\u`, "secrets.xlsx", "a,b.env"} {
							require.NotContains(t, res.Text, leak)
						}
					})
				}
				requireToolSummaries(t, root, []string{
					v("src/main.go\x00src/util.go"),
					v("src/main.go\x00o'brien.env.example"),
					v("src/main.go\x00q3\u2013report.xlsx"),
					v("src/main.go\u00a0docs/o'brien.md"),
				}, nil)
			})
			t.Run("learned", func(t *testing.T) {
				root := shortProjectDir(t, elem...)
				writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./secrets/**)"]}}`)
				writeProjectFile(t, root, "secrets/bob's keys.txt")
				writeProjectFile(t, root, "src/main.go")
				require.NoError(t, makeDirLink(filepath.Join(root, "lnk"), filepath.Join(root, "secrets")))
				pointer := filepath.Join("lnk", "bob's keys.txt")
				refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
				require.NotNil(t, refuses)
				require.True(t, refuses(pointer), "fixture: the host refuses the file through the link")
				v := func(value string) string { return storePreviewOf(t, map[string]any{"paths": value}) }
				for i, s := range []string{
					v("README.md\x00bob's keys.txt"),
					v("README.md\u00a0bob's keys.txt"),
					v("README.md\u2028bob's keys.txt"),
					v("bob's keys.txt\x00README.md"),
					v("README.md bob's keys.txt"),
				} {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						res := requireToolSummariesBeside(t, root, []string{pointer}, nil, []string{s})
						require.NotContains(t, res.Text, "keys.txt")
					})
				}
				requireToolSummariesBeside(t, root, []string{pointer}, []string{v("README.md\x00src/main.go")}, nil)
			})
			t.Run("glob", func(t *testing.T) {
				root := shortProjectDir(t, elem...)
				writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./secrets/**)"]}}`)
				for _, f := range []string{"secrets/token.txt", "src/main.go", "src/util.go"} {
					writeProjectFile(t, root, f)
				}
				require.NoError(t, makeDirLink(filepath.Join(root, "lnk"), filepath.Join(root, "secrets")))
				pointer := filepath.Join("lnk", "token.txt")
				refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
				require.NotNil(t, refuses)
				require.True(t, refuses(pointer), "fixture: the host refuses the file through the link")
				main := filepath.Join(root, "src", "main.go")
				g := filepath.Join("lnk", "tok*")
				v := func(value string) string { return storePreviewOf(t, map[string]any{"paths": value}) }
				for i, s := range []string{
					v("src/main.go " + g),
					v("src/main.go:" + g),
					v("src/main.go (" + g + ")"),
					v(main + " " + g),
					v("src/main.go\x00" + filepath.Join(root, "lnk", "*.txt")),
					v(g),
					v("src/main.go\x00" + g),
					v("src/main.go," + g),
				} {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						res := requireToolSummariesBeside(t, root, []string{pointer}, nil, []string{s})
						for _, leak := range []string{"tok*", "*.txt", "token.txt"} {
							require.NotContains(t, res.Text, leak)
						}
					})
				}
				requireToolSummariesBeside(t, root, []string{pointer}, []string{
					v("src/main.go " + filepath.Join("src", "*.go")),
					v(main + " " + filepath.Join("src", "*.go")),
				}, nil)
			})
		})
	}
}

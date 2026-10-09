package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/rehydrate"
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
				// Fix round 2's review: a one-word plain preview gluing the second path on with `+`,
				// where free text starts a path (cmd.exe's copy) and a value's piece reads a name's
				// character, was screened by the rules' literals alone.
				storePreview(t, map[string]string{"file_path": main + "+" + filepath.Join("lnk", "token.txt")}),
			})
		require.NotContains(t, res.Text, "token.txt")
		requireToolSummariesBeside(t, root, []string{filepath.Join("lnk", "token.txt")},
			[]string{storePreview(t, map[string]string{"file_path": filepath.Join(root, "src", "a+b.go")})}, nil)
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

// TestRehydrateHostPaths_APathNamedValueNamingAPathBuiltAtRunTimeIsWithheld is fix round 2's review
// of candidate 8's diff verify (its minor finding, identical at 77374c3c) through the real adapter,
// the real host rules and the store's own previews, in a plain project and in one whose own path has
// a space. A path-named value skips the free-text whitelist, and containment read only `$NAME`, `~`,
// `%VAR%` and `!VAR!` at a path start as rooted, so a brace list (`{a,b}`, which bash, zsh and fish
// expand) and a command substitution (`$(…)`, a backtick pair) were never resolved, and section 6
// showed `~{,x}/.ssh/id_rsa` (`~/.ssh/id_rsa`), `$(pwd)/../other` (a sibling of the project),
// `.{env,x}` (the `.env` UAT-12's `Read(./.env)` refuses) and `lnk/{token.txt,x}` (lnk a link into a
// refused secrets/, learned from a file pointer, which only the host's judgement of the alternative
// refuses). A brace list of project paths, a brace with no list in it and a `$` inside a name are
// still shown.
func TestRehydrateHostPaths_APathNamedValueNamingAPathBuiltAtRunTimeIsWithheld(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			t.Run("rules", func(t *testing.T) {
				root := uat12Project(t, elem...)
				v := func(k string, value any) string { return storePreviewOf(t, map[string]any{k: value}) }
				for i, s := range []string{
					v("paths", "~{,x}/.ssh/id_rsa"),
					v("paths", "{,x}/etc/passwd"),
					v("paths", ".{.,x}/outside/secret.txt"),
					v("directory", "$(pwd)/../other"),
					v("cwd", "$(echo ~)/.ssh"),
					v("notebook_path", "$(pwd)/../other/x.ipynb"),
					v("paths", ".{env,x}"),
					v("directory", "`pwd`-old/x.txt"),
					v("cwd", "${!x}/y"),
					v("paths", "src/main.go $(pwd)/../other"),
					v("paths", "..$(pwd)"),
					v("paths", []string{"src/main.go", "{,x}/etc/passwd"}),
					v("paths", ".{d..f}nv"),
					v("paths", "{a,{.,x}.}/outside/secret.txt"),
				} {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						res := requireToolSummaries(t, root, nil, []string{s})
						for _, leak := range []string{"passwd", "id_rsa", "secret.txt", "../other", "-old"} {
							require.NotContains(t, res.Text, leak)
						}
					})
				}
				requireToolSummaries(t, root, []string{
					v("paths", "src/{main,util}.go"),
					v("paths", "**/*.{go,md}"),
					v("file", "templates/{{name}}/x.txt"),
					v("file", "build/classes/Outer$Inner.class"),
					v("file", "app/routes/users.$userId.tsx"),
				}, nil)
			})
			t.Run("link", func(t *testing.T) {
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
				v := func(value string) string { return storePreviewOf(t, map[string]any{"files": value}) }
				// No file pointer teaches the build a name here: only the host's judgement of each
				// alternative, through the link, refuses one.
				for i, value := range []string{
					filepath.Join("{lnk,src}", "token.txt"),
					filepath.Join("{src,lnk}", "token.txt"),
					"{lnk,src}/token.txt",
				} {
					require.False(t, refuses(value), "fixture: the host, asked about the whole value, refuses nothing")
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						res := requireToolSummaries(t, root, nil, []string{v(value)})
						require.NotContains(t, res.Text, "token.txt")
					})
				}
				// Learned from a file pointer, the name withholds a list's later piece too.
				res := requireToolSummariesBeside(t, root, []string{pointer}, nil,
					[]string{v("src/main.go " + filepath.Join("{lnk,src}", "token.txt"))})
				require.NotContains(t, res.Text, "token.txt")
				requireToolSummariesBeside(t, root, []string{pointer}, []string{v(filepath.Join("src", "{main,util}.go"))}, nil)
			})
		})
	}
}

// TestRehydrateHostPaths_APathNamedValueNamingACmdVariableOrAShellsTildeIsWithheld is fix round 3's
// review of candidate 8's diff verify (its first two minor findings, identical at 77374c3c) through
// the real adapter, the real host rules and the store's own previews, in a plain project and in one
// whose own path has a space. Round 3 started a path at a `$` after a segment's run of dots
// (`..$HOME/x`), but not at cmd.exe's `%VAR%` or delayed `!VAR!` there, which containment reads as
// rooted where a path starts, nor at a batch file's parameters; and containment read a `~` as rooted
// only before a user name, a `-` or a digit. So section 6 showed `..%HOMEPATH%\.ssh\id_rsa`
// (`..\Users\u\.ssh\id_rsa` to cmd.exe), `%~dp0..\x`, `~+/../other/x.txt` (`$PWD/../other/x.txt` to
// bash and zsh), `~+1/.ssh/id_rsa` and zsh's `~$USER/.ssh/id_rsa`. Each is withheld; a `%` or a `!`
// inside a name, a name that holds a `~`, and a Word lock file are still shown.
func TestRehydrateHostPaths_APathNamedValueNamingACmdVariableOrAShellsTildeIsWithheld(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := uat12Project(t, elem...)
			v := func(k, value string) string { return storePreviewOf(t, map[string]any{k: value}) }
			for i, s := range []string{
				v("file", `..%HOMEPATH%\.ssh\id_rsa`),
				v("paths", "..%HOMEPATH%/x.txt"),
				v("paths", `..!HOMEPATH!\x`),
				v("paths", `src/main.go ..%HOMEPATH%\.ssh`),
				v("file", "..!HOMEPATH!/.ssh/id_rsa"),
				v("paths", `--dir=..%HOMEPATH%\x`),
				v("file", `%~dp0..\x`),
				v("file", `%1\x`),
				v("file", "~+/../other/x.txt"),
				v("directory", "~+/.."),
				v("paths", "src/main.go ~+/../other"),
				v("file", "~+1/.ssh/id_rsa"),
				v("file", "~$USER/.ssh/id_rsa"),
				v("directory", "~$USER"),
				v("file", "~[proj]/../other/x.txt"),
			} {
				t.Run(fmt.Sprint(i), func(t *testing.T) {
					res := requireToolSummaries(t, root, nil, []string{s})
					for _, leak := range []string{"id_rsa", "HOMEPATH", "dp0", "../other", "$USER"} {
						require.NotContains(t, res.Text, leak)
					}
				})
			}
			requireToolSummaries(t, root, []string{
				v("file", "docs/50%off.md"),
				v("file", "docs/%HOMEPATH%.md"),
				v("file", "src/a!b.go"),
				v("file", "notes/v1..%2.txt"),
				v("file", "~$report.docx"),
				v("file", "docs/~+notes.md"),
			}, nil)
		})
	}
}

// TestRehydrateHostPaths_BraceListAlternativesCostABoundedNumberOfHostJudgements is fix round 3's
// review of candidate 8's diff verify (its third minor finding) through the real adapter and the
// store's own previews: the review's hundred tool pointers whose one path-named value each holds a
// brace list of 51 alternatives asked the host 5200 times (round 3 judges each alternative), and took
// a build from 53 ms to seconds. While a Read rule is in force the build judges at most
// braceJudgementsBound fresh alternatives by the host, and withholds every braced value past them
// unjudged (fail closed), so thirty such pointers (about as many lines as section 6 holds at the
// default budget) cost the bound on top of what thirty unbraced values do.
func TestRehydrateHostPaths_BraceListAlternativesCostABoundedNumberOfHostJudgements(t *testing.T) {
	const braceJudgementsBound = 64 // ADR 0011 §23 item 10
	const n = 30
	alts := strings.Split("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNO", "")
	root := uat12Project(t, "proj")
	// build reports how many of the n summaries section 6 shows and leaves out (missing: their lines
	// did not fit), and how many host judgements the build made.
	build := func(braced bool) (shown, missing, calls int) {
		var tools []checkpoint.ToolPointer
		for i := 0; i < n; i++ {
			v := fmt.Sprintf("d%02d/a", i)
			if braced {
				v = fmt.Sprintf("d%02d/{%s}", i, strings.Join(alts, ","))
			}
			s := storePreviewOf(t, map[string]any{"paths": v})
			tools = append(tools, checkpoint.ToolPointer{
				ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%03d", i)), Hash: core.Hash(sha256.Sum256([]byte(s))), Summary: s,
			})
		}
		hp := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())
		counting := func() rehydrate.HostRules {
			h := hp()
			refuses := h.Refuses
			require.NotNil(t, refuses)
			h.Refuses = func(p string) bool { calls++; return refuses(p) }
			return h
		}
		res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), rehydrate.Deps{HostPaths: counting})
		require.NoError(t, err)
		for _, tp := range tools {
			line := "- expand(tool_use_id=\"" + string(tp.ToolUseID) + "\") " + tp.Hash.String() + " — "
			switch {
			case strings.Contains(res.Text, line+tp.Summary+"\n"):
				shown++
			case strings.Contains(res.Text, line+"(summary withheld)\n"):
			default:
				missing++
			}
		}
		return shown, missing, calls
	}
	shown, _, unbraced := build(false)
	require.Equal(t, n, shown, "fixture: every unbraced value is shown")
	shown, missing, calls := build(true)
	require.Equal(t, unbraced+braceJudgementsBound, calls,
		"the alternatives cost the bound's host judgements, on top of each value's own")
	require.Zero(t, missing, "fixture: section 6 holds every pointer")
	require.Equal(t, braceJudgementsBound/len(alts), shown, "the lists the bound covers are judged and shown")
}

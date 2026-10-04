package rehydrate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// The D63 review's second round (ADR 0011 §23): a glob class that respells `..`, an outside file's
// basename withholding the project's own namesake, a `file:` URL in a path-named value, a drop reason
// in a project whose root has a space, and the whitelist extensions that recover everyday summaries
// with a completeness argument each (a Glob preview of a directory under the root, a brace glob, an
// operator glued to a word, parentheses inside a quoted run, an apostrophe between letters, a single-
// quoted run, a single `%`). Each row is red on the review's worktree (f708c693).

// jsonPath is p as a canonical-JSON preview spells it, its backslashes escaped.
func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

// TestBuild_AGlobClassNeverRespellsAParentDirectory: containment cleaned a glob as a literal path, and
// the escaping check looked only for a literal `..`, so a structured glob that spells `..` with a
// class, a `?` or an escape (`[.][.]`, `.[.]`, `?.`, `.?`, `[.]\.`), or with an explicit leading dot
// and a star (`.*`, which a shell without globskipdots matches against `..`), was shown though it
// names a path outside the project; and path.Match lets a class match `/`, so `[/]etc[/]passwd` is
// /etc/passwd to Qompack's own matcher. A class of one character is read as that character and a
// class that may match a separator as one. A star alone never produces `..` (no directory walker
// yields it, and a shell's `*` skips a leading dot), so `*/x` and `*.*` stay inside, and a Next.js
// catch-all route `[...slug]` is one character of a class, not a parent.
func TestBuild_AGlobClassNeverRespellsAParentDirectory(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"**/*.go", "*/main.go", "src/[ab]*.go", "*.*",
			"app/[...slug]/page.tsx", "app/[[...slug]]/page.tsx",
			`{"paths":["src/*.go","docs/*.md"]}`,
		},
		[]string{
			"[.][.]/outside/x.key",
			".[.]/outside/x.key",
			"[.][.]/[.][.]/sibling/secret.key",
			"?./outside/x.key",
			".?/outside/x.key",
			".*/outside/x.key",
			"[.][.]/.ssh/id_rsa",
			`[.]\./outside/x.key`,
			`{"paths":["[.][.]/outside/secret.key"]}`,
			`{"paths":["?./outside/secret.key","src/a.go"]}`,
			`{"path":".[.]/outside"}`,
			"[/]etc[/]passwd",
			"[~]/.ssh/id_rsa",
			"x[/]..[/]..[/]y",
			"private/de[n]y.txt",
			`{"paths":["[/]etc[/]passwd","src/a.go"]}`,
			`{"paths":["src/a.go","private/de[n]y.txt"]}`,
			cutJSONArg(t, "file_path", "[.][.]/outside/secret.key", len("[.][.]/outside/sec")),
		},
		[]string{"x.key", "secret.key", "sibling", "id_rsa"})
}

// TestBuild_AnOutsideNamesakeNeverWithholdsAProjectPath: a one-word structured value had to pass the
// whole name screen, whose withheld names include the basename of every path the build withholds,
// outside ones too, so a Read of a dependency's README.md, a global settings.json or ~/.kube/config
// withheld every Read of the project's own README.md, settings.json or config/…, in a build with no
// rules at all. A rooted value spelled in one separator style is the exact path the host judged, so
// it is screened by the rules' literals alone; free text naming the basename stays withheld (D61's
// accepted over-withholding, ADR 0011 §23 item 2), and the host is asked about each project path once.
func TestBuild_AnOutsideNamesakeNeverWithholdsAProjectPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		elem  []string
		rules []string
	}{
		{"no rules", []string{"proj"}, nil},
		{"UAT-12", []string{"proj"}, uat12Rules},
		{"UAT-12, a root with a space", []string{"John Smith", "proj"}, uat12Rules},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := previewRoot(tc.elem...)
			home := previewRoot("home")
			outside := []string{
				filepath.Join(home, "go", "pkg", "mod", "cobra@v1.8.0", "README.md"),
				filepath.Join(home, ".claude", "settings.json"),
				filepath.Join(home, ".cargo", "registry", "x", "lib.rs"),
				filepath.Join(home, ".kube", "config"),
				filepath.Join(home, "elsewhere", "go.mod"),
				`{"file_path":"` + jsonPath(filepath.Join(home, "npm", "package.json")) + `"}`,
			}
			project := []string{
				filepath.Join(root, "README.md"),
				filepath.Join(root, ".claude", "settings.json"),
				filepath.Join(root, "src", "lib.rs"),
				filepath.Join(root, "config", "database.yml"),
				filepath.Join(root, "go.mod"),
				`{"file_path":"` + jsonPath(filepath.Join(root, "package.json")) + `"}`,
			}
			asked := map[string]int{}
			inner := hostRules(root, tc.rules...)
			hp := func() HostRules {
				h := inner()
				refuses := h.Refuses
				h.Refuses = func(p string) bool { asked[p]++; return refuses(p) }
				return h
			}
			requireScreened(t, root, hp, nil, project, append(outside, "git diff README.md"),
				[]string{"cobra@", ".cargo", ".kube", "elsewhere", "npm"})
			want := map[string]int{}
			for _, p := range project[:len(project)-1] {
				want[p] = 1
			}
			want[filepath.Join(root, "package.json")] = 1
			require.Equal(t, want, asked, "each project path is judged once, and no outside path reaches the host")
		})
	}
}

// TestBuild_ARuleLiteralFromAGlobRunKeepsItsOpenEnd: a structured value's names were read as whole
// names, which is exact for a rule's literal segment (`deny.txt`) but not for the literal run that
// starts a glob segment (`secret` for `./secret*`, `.env` for `**/.env*`): every name that begins
// with it is refused. Several path-named values ask the host nothing, and a value whose backslash a
// POSIX shell reads as an escape names another path than the host judged, so `secrets.txt`,
// `.env.local` and `se\crets.txt` were shown under rules that refuse them. Such a literal keeps its
// open end everywhere; a whole literal still reads whole names (`deny.txt.bak` is another file).
func TestBuild_ARuleLiteralFromAGlobRunKeepsItsOpenEnd(t *testing.T) {
	root := previewRoot("proj")
	fwd := filepath.ToSlash(root)
	requireScreened(t, root, hostRules(root, "./secret*", "**/.env*", "./private/deny.txt"), nil,
		[]string{
			`{"paths":["src/a.go","docs/b.md"]}`,
			`{"paths":["mysecret.txt","src/a.go"]}`,
			`{"paths":["private/deny.txt.bak","src/a.go"]}`,
			filepath.Join(root, "docs", "env.md"),
		},
		[]string{
			`{"paths":["secrets.txt","src/a.go"]}`,
			`{"paths":["src/a.go",".env.local"]}`,
			`{"paths":["config/.envrc","src/a.go"]}`,
			fwd + `/se\crets.txt`,
			fwd + `/.e\nv.local`,
		},
		[]string{"secrets.txt", ".env.local", ".envrc", "crets.txt", "nv.local"})
}

// TestBuild_ARootLedGlobPreviewIsJudgedAsOneGlob: the store previews a Glob call with a path as the
// path, a space and the pattern, so every Glob of a directory under the root was free text holding a
// `*`, and withheld; so was a brace glob. A two-word preview whose first word is the root, or the root
// and a safe relative directory, and whose second is a safe glob is judged as one structured glob: the
// directory by the host once, the joined glob and the glob alone (a script's argument, read from the
// working directory) by what they select, and the text by the rules' literals and the withheld names.
// One level of `{a,b}` is expanded and each alternative judged as the glob it makes, every one of
// which must be a glob; a sequence (`{1..3}`), a nested brace or a concrete alternative is withheld.
func TestBuild_ARootLedGlobPreviewIsJudgedAsOneGlob(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			requireScreened(t, root, hostRules(root, uat12Rules...), []string{"private/deny.txt"},
				[]string{
					filepath.Join(root, "internal") + " **/*_test.go",
					root + " **/package.json",
					filepath.Join(root, "src") + " *.{go,md}",
					"**/*.{ts,tsx}",
					"src/**/*.{test,spec}.ts",
				},
				[]string{
					root + " **/.env*",
					filepath.Join(root, "private") + " deny*",
					filepath.Join(root, "private") + " *",
					root + " secrets/*",
					root + " ../*",
					root + " [.][.]/*",
					filepath.Join(root, "secrets") + " *.go",
					"**/*.{go,env}",
					"{src,..}/x.go",
					"{1..3}/x.go",
					"src/{a,{b,c}}.go",
					"src/{a,b/x.go",
					// A concrete path among a brace list's alternatives would reach no host judgement.
					"src/{a,b}.go",
					"{docs/a.md,**/*.go}",
				},
				[]string{"deny.txt", "token.txt"})
		})
	}
}

// TestBuild_AnOperatorGluedToAWordSplitsIt: a `;`, `|`, `&&` or `||` glued to a word made the token
// unsafe, so `TODO|FIXME`, `…2>&1; tail …` and PowerShell's `Set-Location <root>; …` were withheld.
// Outside a quoted run and a URL such a token is split at those operators and each piece judged as a
// token of its own, every piece start a path start; the root's spelling before a `|`, or before a `;`
// that ends its token, is the root. A single `&` stays outside the whitelist, and a `..` glued to a
// word stays withheld wherever it stands (cmd.exe reads `cd..` after `if`, `else` or `do` too, where
// no token position tells it is a command).
func TestBuild_AnOperatorGluedToAWordSplitsIt(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"TODO|FIXME",
			"go test ./... > test.log 2>&1; tail -n 50 test.log",
			"Set-Location " + root + "; go test ./...",
			"go build ./...;echo ok",
			"make lint||exit 1",
			"cat docs/guide.md&&ls",
			"go test ./... 2>&1|tail -n 5",
		},
		[]string{
			"ls;cat private/deny.txt",
			"x|/etc/passwd",
			"a;~/.ssh/id_rsa",
			root + ";cd ..",
			"GOPATH=" + root + `;C:\elsewhere`,
			"go build;cd.. && dir",
			"if exist x cd.. && type notes.txt",
			"a&b",
			"a|&/etc/passwd",
			"git log|grep .env",
			"a&&&/etc/passwd",
		},
		[]string{"deny.txt", "passwd", "id_rsa", "elsewhere"})
}

// TestBuild_ParenthesesInsideAQuotedRunAreJudged: a parenthesis anywhere made a token unsafe, so a
// conventional commit message (`"feat(api): …"`) was withheld. Inside a double-quoted run, where no
// shell gives a parenthesis meaning, `(` and `)` are allowed: each starts a path (a nested shell's
// subshell, PowerShell's subexpression) and bounds a `..`, the run's words are read again with the
// parentheses removed (a nested zsh reads `de(n)y.txt` as the group `deny.txt`), and an extglob `+(` or
// `@(` stays unsafe. A Next.js route group (`app/(auth)/…`) stays withheld: PowerShell starts an
// argument after `)`, so `/login/page.tsx` reads as a path from the drive's root.
func TestBuild_ParenthesesInsideAQuotedRunAreJudged(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			`git commit -m "feat(api): add users endpoint"`,
			`gh pr create --title "fix(x): y"`,
			`git commit -m "chore(deps): bump go to 1.24"`,
			`echo "done (all tests pass)"`,
		},
		[]string{
			`cat "(/etc/passwd)"`,
			`cat "(~/.ssh/id_rsa)"`,
			`cat "x(~/.ssh/id_rsa)"`,
			`cat "a(..)/x"`,
			`cat "(.)(.)/x"`,
			`cat "private/de(n)y.txt"`,
			`cat "x+(n)"`,
			`cat "@(deny).txt"`,
			`cat "app/(auth)/login/page.tsx"`,
			filepath.Join(root, "app", "(auth)", "login", "page.tsx"),
			`python3 -c "import sys; print(sys.version)"`,
		},
		[]string{"passwd", "id_rsa", "(n)y"})
}

// TestBuild_AnApostropheBetweenLettersAndASingleQuotedRunAreJudged: a single quote anywhere made a
// token unsafe, so a recall query, a reason or a prompt with a contraction or a possessive, and a
// POSIX command's single-quoted argument, were withheld. An apostrophe between two letters is
// allowed: its quoted span is literal and the screen reads the text without the quote, a path may
// start after it, and no root, separator or `..` can border it. A single-quoted run at a token start,
// whose content holds no quote, backtick, `$` or backslash (a POSIX shell keeps a backslash there), is
// judged as a double-quoted run's content is. Anything else with a quote stays unsafe.
func TestBuild_AnApostropheBetweenLettersAndASingleQuotedRunAreJudged(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, append([]string{"./private/my secret.txt"}, uat12Rules...)...), nil,
		[]string{
			`{"query":"what's new in the gate"}`,
			`{"reason":"superseded by the user's correction"}`,
			`{"query":"why does git's rebase drop empty commits"}`,
			`sed -n '1,50p' src/main.go`,
			`jq '.dependencies' package.json`,
			`ssh host 'systemctl restart app'`,
			`git commit -m 'fix the bug'`,
			`cd '` + root + `' && make`,
		},
		[]string{
			`cat de'n'y.txt`,
			`cat m'y s'ecret.txt`,
			`cat '/etc/passwd'`,
			`cat x'/etc/passwd'`,
			`cat '~/.ssh/id_rsa'`,
			`bash -c 'cat $HOME/.ssh/id_rsa'`,
			`cat '..'/x`,
			`find . -name '*.env'`,
			`cat '` + root + ` old/x.txt'`,
			`cat 'a\b'`,
			`echo it\'s`,
			`cat x'C:secret.t'xt`,
			`echo don''t`,
		},
		[]string{"passwd", "id_rsa", "secret.t", "old/x.txt"})
}

// TestBuild_AFileURLInAPathNamedValueIsOutsideTheProject: deleting D61's outsideIn removed the only
// check that read a `file:` URL in a structured value, so containment read `file:///etc/passwd` as a
// project path whose first segment is `file:`, the host refused nothing, and the value was shown.
// Containment now reads a `file:` scheme as outside the project, as free text already did, at no host
// judgement.
func TestBuild_AFileURLInAPathNamedValueIsOutsideTheProject(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{`{"paths":["src/a.go","docs/b.md"]}`},
		[]string{
			`{"directory":"file:///home/u/other"}`,
			`{"paths":["file:///etc/passwd","src/a.go"]}`,
			`{"cell_id":"c1","notebook_path":"file:///home/u/nb.ipynb"}`,
			`{"args":["status"],"cwd":"file:///C:/Users/someone/secret"}`,
			`{"file":"file://fileserver/share/payroll.xlsx"}`,
			cutJSONArg(t, "file_path", "file:///home/u/secret/notes.txt", len("file:///home/u/secret/no")),
		},
		[]string{"passwd", "payroll", "someone", "nb.ipynb", "/home/u"})
}

// TestBuild_AReasonInAProjectWithASpaceShowsItsOwnPaths: the drop-reason screen split a reason on
// whitespace without holding the root together, so in a project whose root has a space the root's
// first piece read as an absolute path outside the project, and every reason naming a project path
// was redacted. With the root no longer marked, a withheld project path named absolutely was not
// found either, and a sibling of the root named as a whole part of the error chain, with no
// operation before it, was read as the root and a relative word; both were shown in a root with no
// space. The root is held together, and a part of the chain is judged whole as an operation's path is.
func TestBuild_AReasonInAProjectWithASpaceShowsItsOwnPaths(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			index := "checkpoint: git index unsupported: reading index: open " + filepath.Join(root, ".git", "index") + ": gone"
			restore := "rule " + filepath.Join(root, ".claude", "rules", "a.md") + " skipped; restore: re_read(path=docs/x.md)"
			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
			cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: index},
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: restore},
				checkpoint.DropEntry{
					Kind:   "pointer_git_unavailable",
					Detail: "fatal: not a git repository: " + filepath.Join(root+" main", ".git", "worktrees", "wt"),
				},
				checkpoint.DropEntry{
					Kind:   "pointer_git_unavailable",
					Detail: "rules: read " + filepath.Join(root, "private", "deny.txt") + ": Access is denied.",
				})
			d := uat05Deps(t, cp)
			d.HostPaths = hostRules(root, uat12Rules...)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireNoLeak(t, res, []string{"proj main", "worktrees", "deny.txt"})
			var details []string
			for _, e := range res.Dropped {
				if e.Kind == "pointer_git_unavailable" {
					details = append(details, e.Detail)
				}
			}
			require.Len(t, details, 4, "every git drop is still reported: %v", res.Dropped)
			require.Contains(t, details, index, "a project path is shown")
			require.Contains(t, details, restore, "the restore call beside a project path is kept")
			require.Contains(t, details, "fatal: not a git repository: "+withheldDropID)
			require.Contains(t, details, "rules: "+withheldDropID+": Access is denied.")
		})
	}
}

// TestBuild_ASinglePercentIsShownAndAPairIsNot: `%` was never safe, so `git log --pretty=format:%h`
// and `date +%s` were withheld. A percent-escape needs `%` and two hex digits, and a cmd.exe variable
// two `%`, so one `%` that two hex digits do not follow is inert in every shell and decoder; two or
// more, or one before two hex digits (or before the store's cut, which may hide them), stay unsafe.
func TestBuild_ASinglePercentIsShownAndAPairIsNot(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"git log --format=%h -n 3",
			"date +%s",
			`echo "100% done"`,
			"echo 100%",
		},
		[]string{
			`git log --format="%h %s"`,
			`type %USERPROFILE%\x.txt`,
			"curl https://x.example/a%2Fb",
			"cat private%2Fdeny.txt",
			"cat %5Cetc%5Cpasswd",
			"cat %2e%2e/x",
			"cat a%2e",
			cutAfter(t, "curl https://x.example/", "%2"),
			cutAfter(t, "date +", "%"),
			// Criterion change (wave 19d final verify): `--pretty=format` before a `:` is a name PowerShell
			// accepts for a drive (providerPath), so git's `format:` spelling is over-withheld.
			"git log --pretty=format:%h -n 3",
		},
		[]string{"passwd", "USERPROFILE"})
}

// TestBuild_AGlobPreviewAndAnOperatorPieceCostOneJudgementEach pins the cost of the new shapes: a
// root-led Glob preview asks the host about its directory once, and the pieces of a token split at an
// operator, an apostrophe, a single-quoted run and a single `%` ask it nothing.
func TestBuild_AGlobPreviewAndAnOperatorPieceCostOneJudgementEach(t *testing.T) {
	root := previewRoot("proj")
	cp := ckUAT05()
	cp.Pointers.Files = nil
	summaries := []string{
		filepath.Join(root, "internal") + " **/*_test.go",
		"TODO|FIXME", "go build ./...;echo ok", `{"query":"what's new"}`, `sed -n '1,50p' src/main.go`,
		"date +%s", `git commit -m "feat(api): x"`,
	}
	cp.Pointers.Tools = nil
	for i, s := range summaries {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%02d", i)), Hash: hashOf(s), Summary: s,
		})
	}
	asked := map[string]int{}
	inner := hostRules(root, uat12Rules...)
	d := uat05Deps(t, cp)
	d.HostPaths = func() HostRules {
		h := inner()
		refuses := h.Refuses
		h.Refuses = func(p string) bool { asked[p]++; return refuses(p) }
		return h
	}
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root
	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	require.NotContains(t, res.Text, withheldSummary, "every summary here names nothing private")
	// The one-word `TODO|FIXME` is a structured value (a Grep pattern), judged whole once.
	require.Equal(t, map[string]int{filepath.Join(root, "internal"): 1, "TODO|FIXME": 1}, asked)
}

// TestBuild_AZshOrPowerShellExpansionIsWithheld: three characters D63(2) lists as safe expand in a
// shell the whitelist must cover. zsh replaces a word that starts with `=` by the command's path
// (EQUALS, on by default: `=python` is /usr/bin/python), and after `:` in an assignment; with
// EXTENDED_GLOB a `#` after a character repeats it (`de#ny.txt` matches deny.txt, `(#i)` folds case);
// and PowerShell splats a variable from a whole `@name` argument (`@env:HOME`). A `=` that starts a
// name at a token's start or after a delimiter, a `#` anywhere but in a token that starts with one
// (which every shell reads as a comment), and a whole `@name` token are unsafe. A `#` in a URL is a
// fragment, which can glob only below a directory named `http:` in the project, as `?` can.
func TestBuild_AZshOrPowerShellExpansionIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"npm i @types/node", "# just a comment", "git log --grep=fix -n 3", "go test -run=TestX ./...",
			"https://example.com/docs#install", "curl -s https://example.com/a#b",
			`git commit -m "fix #42"`,
			`{"sql":"SELECT id FROM users WHERE active = 1"}`,
			`{"plan":"## Plan\n1. Fix the gate"}`,
		},
		[]string{
			"cat de#ny.txt",
			"cat x#deny.txt",
			`zsh -o extendedglob -c "cat (#i)DENY.txt"`,
			`zsh -c "cat (=python)"`,
			`bash -c "echo x(~)"`,
			"cat =python",
			"X=a:=python make",
			"cat a==b",
			"Get-Content @env:HOME",
			"cmd @args",
			// PowerShell splats its automatic variables too (`@HOME`), so a Grep for an annotation is
			// over-withheld.
			"src/test/java @Test",
		},
		[]string{"#ny", "python", "@args", "@env"})
}

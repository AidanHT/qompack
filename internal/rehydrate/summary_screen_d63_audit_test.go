package rehydrate

import (
	"path/filepath"
	"testing"
)

// The audit of the D63 whitelist's extensions (ADR 0011 §23 item 7): each extension of D63(2)'s list
// is pinned by an adversarial row that would show a path if its completeness argument failed. The rows
// for the apostrophe, the quoted root before an operator and the single `%` are red on f3196046, whose
// extensions those arguments could not complete; the others pin arguments that hold.

// TestBuild_ARootLedGlobPreviewNamesOnlyWhatTheHostJudged pins the root-led Glob preview: its
// directory is read by the Glob tool exactly as the host judged it (a shell would run it as a
// command, which lists nothing), and its pattern, read from the directory and alone, may climb or
// start outside the project in no reading, brace and class included.
func TestBuild_ARootLedGlobPreviewNamesOnlyWhatTheHostJudged(t *testing.T) {
	root := previewRoot("proj")
	src := filepath.Join(root, "src")
	requireScreened(t, root, hostRules(root, "./private/**", "./.env"), []string{"private/deny.txt"},
		[]string{src + " **/*.go", root + " **/package.json", src + " *.{go,md}"},
		[]string{
			src + " ../*", src + " /etc/*", src + " ~/*", src + " C:*", src + " Temp:*", src + " #/etc/*",
			src + " [.][.]/*", src + " x[/]..[/]*", src + " *.{go,/etc/passwd}", root + " {..,src}/*",
			filepath.ToSlash(root) + `/priv\ate *`, root + " private/*", root + " **/de*.txt",
		},
		[]string{"passwd", "deny.txt"})
}

// TestBuild_ABraceGlobNeverRespellsAnOutsidePath pins the brace glob: one level of `{a,b}` is read
// as bash, zsh, fish and a glob library expand it, and its braces and commas as path starts
// (PowerShell opens a script block at `{` and splits at `,`), so no alternative climbs, starts
// outside the project, spells a rule's literal or selects a withheld path in either backslash
// reading; a sequence, a nested or unbalanced brace and a concrete alternative are withheld.
func TestBuild_ABraceGlobNeverRespellsAnOutsidePath(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/**", "./.env"), []string{"private/deny.txt"},
		[]string{"**/*.{ts,tsx}", "src/**/*.{test,spec}.ts"},
		[]string{
			"{..,src}/*.go", ".{.,x}/*.go", `{.\.,x}/*.go`, "*{,/}etc/*", "{/etc,src}/*", "{~,x}/*",
			"{C:,x}*", "{Temp:,x}*", "{[.][.],x}/*", "src/{a,b}/../*.go", "{private,src}/*.txt",
			"*.{go,env}", `{a\,b,c}*`, "{a,b}{c,d}*", "{1..3}*",
		},
		[]string{"passwd", "deny.txt"})
}

// TestBuild_AGluedOperatorPieceIsJudgedAsAToken pins the split at a glued `;`, `|`, `&&` or `||`:
// every piece is a token, so every piece start is a path start, and the name screen reads the text
// whole, so a program that keeps the token whole (cmd.exe hands an external program `a;b`) reads no
// name the screen has not read.
func TestBuild_AGluedOperatorPieceIsJudgedAsAToken(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"TODO|FIXME", "go build ./...;echo ok", "make lint||exit 1"},
		[]string{
			"a|Temp:secret.txt", "a&&C:secret.txt", "a||#/etc/passwd", "go build;cat .env", "x|..",
			"x;../y", "a|=python", "a;~user/x", "TODO|de'n'y.txt",
			"x||" + filepath.ToSlash(root) + `\zzsib`,
		},
		[]string{"secret.txt", "passwd", "python", "zzsib"})
}

// TestBuild_AQuotedRunsParenthesesNeverHideAPath pins the parentheses inside a quoted run: a path
// may start after each, a `..` touching one climbs, and the run's words are read again without them.
func TestBuild_AQuotedRunsParenthesesNeverHideAPath(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{`git commit -m "feat(api): add users endpoint"`, `echo "done (all tests pass)"`},
		[]string{
			`cat "(Temp:secret.txt)"`, `cat "x(C:secret.txt)"`, `cat "a)(/etc/passwd"`, `cat "(..)"`,
			`cat "x(.)(.)/y"`, `cat "(=python)"`, `cat "x+(deny).txt"`, `cat "private/de(n)y.txt"`,
		},
		[]string{"secret.txt", "passwd", "python", "(n)y"})
}

// TestBuild_AnApostropheSpanNeverJoinsTheRootToASibling: an apostrophe between letters opens a quoted
// span that a POSIX shell and PowerShell close only at the next apostrophe, whatever lies between, so
// the spaces and operators inside it are literal and join tokens the screen judged one by one into
// one word. Only the root's mark is judged by what follows it (the root followed by a space, `|` or
// `;` ends there), so inside such a span the root after a path start and a space or an operator named
// a sibling of the root (`--o=<root> old` is `--o=/q/proj old`). A text whose apostrophe stands
// outside a quoted run holds no root; an apostrophe inside a double-quoted run is literal there.
func TestBuild_AnApostropheSpanNeverJoinsTheRootToASibling(t *testing.T) {
	root := previewRoot("proj")
	fwd := filepath.ToSlash(root)
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			`{"query":"what's new in the gate"}`,
			"cd " + fwd + ` && git commit -m "don't panic"`,
			"echo it's done",
		},
		[]string{
			"echo it's x --o=" + fwd + " zzsib y'z",
			"cat it's," + fwd + " zzsib y'z",
			"echo don't --dir=" + fwd + " && zzsib y'z",
			"echo it's -C" + fwd + "|zzsib y'z",
			"echo it's " + fwd + ";zzsib y'z",
		},
		[]string{"zzsib"})
}

// TestBuild_AQuotedRootBeforeAnOperatorIsASiblingAtAPathStart: inside a quoted run the root followed
// by a space and a shell operator was never a sibling, for a nested shell's command line (`bash -c "cd
// <root> && make"`); but a program that is no shell takes the run as one argument, and when the root
// stands where that argument's path starts (the run's start, after a delimiter or a short option) the
// operator is part of a sibling's name (`cat "<root> && make"` opens `/q/proj && make`). The operator
// is the end of the root only when the root is a word of its own after the run's first word.
func TestBuild_AQuotedRootBeforeAnOperatorIsASiblingAtAPathStart(t *testing.T) {
	root := previewRoot("proj")
	fwd := filepath.ToSlash(root)
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{`bash -c "cd ` + fwd + ` && go test ./..."`, `sh -c "cd ` + fwd + ` || exit 1"`},
		[]string{
			`cat "` + fwd + ` && zzsib"`,
			`cat '` + fwd + ` | zzsib'`,
			`cat "--dir=` + fwd + ` && zzsib"`,
			`cat "-C` + fwd + ` ; zzsib"`,
			`cat "a, ` + fwd + ` && zzsib"`,
			`cat "--dir= ` + fwd + ` | zzsib"`,
		},
		[]string{"zzsib"})
}

// TestBuild_ASinglePercentIsNoEscapeOrParameter: a single `%` was a name character unless two hex
// digits followed it, but `%u` and four hex digits is an escape (IIS's and JavaScript's unescape read
// `%u002e` as `.`), and a digit, `*` or `~` after it is a cmd.exe batch parameter (`%1`, `%*`,
// `%~dp0`, the batch file's own directory). Each makes the `%` unsafe, and so does a store cut within
// the five bytes after it.
func TestBuild_ASinglePercentIsNoEscapeOrParameter(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"date +%s", "echo 100%", `echo "100% done"`, "git log --format=%h -n 3"},
		[]string{
			"cat %u002e./x", "cat %U002E./x", "cd %~dp0 && type secret.txt", "type %1", "echo %*",
			cutAfter(t, "curl https://x.example/", "%u00"),
		},
		[]string{"secret.txt"})
}

// TestBuild_ANullDeviceTokenIsAFixedSpelling pins the null device's tokens: each is a whole token in
// a fixed spelling, so a longer token that begins with one is judged as any other.
func TestBuild_ANullDeviceTokenIsAFixedSpelling(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"ls src/ 2>/dev/null", "go test ./... >nul", "cat /dev/stdin"},
		[]string{
			"cat /dev/null/../../etc/passwd", ">/dev/nullx", "2>/dev/null/x", "cat /dev/stdin2",
			"2>/dev/null;cat /etc/passwd", "cat /dev/fd/3",
		},
		[]string{"passwd"})
}

// TestBuild_ACommentTokenAndAnEqualsRunExpandNothing pins the `#` that starts a token and the run of
// `=` with no name after it: neither expands in any shell, and a path after the `#` is judged.
func TestBuild_ACommentTokenAndAnEqualsRunExpandNothing(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"# TODO later", "echo x==", "git log --grep=fix -n 3"},
		[]string{"cat x#deny.txt", "#~/.ssh/id_rsa", "#..", "cat =python", "cat a==b", "echo #=/etc/passwd"},
		[]string{"id_rsa", "python", "passwd"})
}

// TestBuild_TheRootBeforeAPipeOrSemicolonIsTheRoot pins the root's spelling before a `|`, and before
// a `;` that ends its token: every shell ends the word there (cmd.exe hands a program `<root>;`, the
// root's own spelling and a `;`); before any other character it is a sibling's name.
func TestBuild_TheRootBeforeAPipeOrSemicolonIsTheRoot(t *testing.T) {
	root := previewRoot("proj")
	fwd := filepath.ToSlash(root)
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"Set-Location " + root + "; go test ./...", "ls " + fwd + "|wc -l"},
		[]string{
			fwd + ";cd ..", "cat " + fwd + ";zzsib", `cat "` + fwd + `|zzsib"`, `cat '` + fwd + `;zzsib'`,
			"cat " + fwd + "&zzsib", "cat " + fwd + "#zzsib",
		},
		[]string{"zzsib"})
}

// TestBuild_AJSONKeyIsScreenedAsAString pins the claim that a canonical-JSON preview's keys are judged:
// D63(1) judges a preview by its decoded strings, and a key is one, screened as free text as every
// string but a path-named value is, so a key that spells a path is withheld as a value would be.
func TestBuild_AJSONKeyIsScreenedAsAString(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{`{"query":"retry backoff"}`, `{"k":5,"query":"x"}`},
		[]string{
			`{"private/deny.txt":"x"}`, `{"/etc/passwd":1}`, `{"Temp:secret.txt":true}`, `{"a":{"..":1}}`,
			`{"x":["~/.ssh/id_rsa"]}`,
		},
		[]string{"deny.txt", "passwd", "secret.txt", "id_rsa"})
}

// TestBuild_ADotDotRangeAndAGoPatternNeverClimb pins `..` between two name characters (a revision
// range) and `...` (a Go package pattern): no shell, program or Win32 path normalization climbs
// through either, while a `..` that touches a token's end, a separator, the root or a delimiter does.
func TestBuild_ADotDotRangeAndAGoPatternNeverClimb(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"git log main..feature", "git diff HEAD~3..HEAD", "go test ./...",
			"git diff origin/main...HEAD",
		},
		[]string{
			"git diff main..", `cat x..\y`, "cd..", "cat a,..", "cat #..", `cat "(..)"`, "x=..", "cat @..",
			"git -C.. log", "x..|y", "cat x/..",
		},
		nil)
}

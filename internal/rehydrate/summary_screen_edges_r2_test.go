package rehydrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/rules"
)

// The D61 screen's edges as the w19c round-2 review found them (ADR 0011 §23 items 6 to 10): an
// instruction file under a denied directory restored with its body, a sibling of the root behind a
// glued quote, an apostrophe in the root read as an open quote, a regular expression read as an
// absolute path and noted as a withheld name, a relative path glued to a flag, a rule literal matched
// inside another word, a command run from the root judged whole by the host, and a path-named array
// judged once per element.

// TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored: item 6a handed the rule scanner every
// file pointer, the withheld ones included, so a nested CLAUDE.md above a withheld pointer was
// restored, its path in the heading and its body in the payload, and named with its restore call in
// section 7 and dropped() when it did not fit; nothing judged a rule file's own path, so a path rule
// the host refuses was restored too. The scanner is handed only the pointers section 6 may show,
// and a rule file the host refuses, or outside the project, is neither rendered nor named.
func TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"},
		{Path: "src/a.go", Hash: hashOf("a"), Why: "referenced"},
	}
	leaks := []string{"private/CLAUDE.md", "ZZ-NESTED-BODY", "secret.md", "ZZ-SECRET-RULE", "private/deny.txt", "outside.md"}
	for _, tc := range []struct {
		name   string
		budget core.Tokens
	}{{"everything fits", maxBudget()}, {"the instructions are dropped", 260}} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &fakeScanner{
				pathScoped: []rules.Rule{
					{Path: ".claude/rules/secret.md", Globs: []string{"src/**"}, Body: "ZZ-SECRET-RULE"},
					{Path: ".claude/rules/go.md", Globs: []string{"src/**"}, Body: "Go rule body."},
					{Path: "../outside.md", Globs: []string{"src/**"}, Body: "ZZ-OUTSIDE-RULE"},
				},
				nested: []rules.Rule{
					{Path: "private/CLAUDE.md", Body: "ZZ-NESTED-BODY", Nested: true},
					{Path: "src/CLAUDE.md", Body: "Nested guidance for src.", Nested: true},
				},
			}
			d := uat05Deps(t, cp)
			d.HostPaths = hostRules(root, "./private/**", "./.claude/rules/secret.md")
			d.Rules = sc
			r := requestFor(t, cp, tc.budget)
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireNoLeak(t, res, leaks)
			require.NotContains(t, res.Text, "ZZ-OUTSIDE-RULE")
			for _, given := range sc.pointers {
				require.Equal(t, []string{"src/a.go"}, given, "the scanner is handed only the pointers section 6 shows")
			}
			if tc.budget == maxBudget() {
				require.Contains(t, res.Text, "Go rule body.", "an allowed path rule is restored")
				require.Contains(t, res.Text, "Nested guidance for src.", "an allowed nested CLAUDE.md is restored")
				return
			}
			_, ok := dropForKind(res.Dropped, dropKindNestedClaudeMD, "src/CLAUDE.md")
			require.True(t, ok, "fixture: the allowed nested CLAUDE.md did not fit: %v", res.Dropped)
		})
	}
}

// TestBuild_AQuoteGluedAfterTheRootStillNamesASibling: a quote right after the root's spelling was
// read as the end of the root's word, but a quote that opens a stretch starting with a space joins
// that stretch to the same shell word, a sibling of the root (`<root>" old"/notes.txt` is
// `<root> old/notes.txt`), and it was shown. A quote that closes the root's argument is still the
// end of the root.
func TestBuild_AQuoteGluedAfterTheRootStillNamesASibling(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{`cat "` + root + `"/notes.txt`, `cd "` + root + `" && make`, `cat "` + root + `" "old/notes.txt"`},
		[]string{
			`cat ` + root + `" old"/notes.txt`,
			`cat ` + root + `' old'/notes.txt`,
			`cat "` + root + `"" old/notes.txt"`,
			`cat ` + root + `" old/notes.txt"`,
		},
		nil)
}

// TestBuild_AnApostropheInTheRootIsNotAnOpenQuote: the quote readers were handed the text up to the
// end of the root's spelling, the root's own apostrophe included, which every reader took for an
// open quote, so in a project under `o'brien` or `John's projects` every summary where the root is
// followed by a space was withheld as a sibling: the store's Grep and Glob previews, `cd <root> &&
// go test ./...` and `git -C <root> status`, and the POSIX-escaped `o\'brien`. The quote state is
// read without the root's own characters; a sibling stays withheld.
func TestBuild_AnApostropheInTheRootIsNotAnOpenQuote(t *testing.T) {
	for _, elem := range [][]string{{"o'brien", "proj"}, {"John's projects", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			slash := strings.ReplaceAll(root, `\`, "/")
			escaped := strings.NewReplacer("'", `\'`, " ", `\ `).Replace(slash)
			sep := string(filepath.Separator)
			requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
				[]string{
					root + " TODO",
					root + " **/*.go",
					slash + " TODO",
					root + " err != nil",
					"cd " + root + " && go test ./...",
					"cd " + slash + " && git log --oneline -n 5",
					"git -C " + root + " status --short",
					"cd " + escaped + " && go test ./...",
					`git -C "` + root + `" status`,
				},
				[]string{
					`cat "` + root + ` old` + sep + `x.txt"`,
					`cat ` + root + `" old"` + sep + `x.txt`,
					root + "2" + sep + "x.txt",
					"cd " + root + " && cat private/deny.txt",
				},
				[]string{"deny.txt"})
		})
	}
}

// TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName: a Grep pattern led by a backslash
// (`\bConfigLoader\b`, `\s`) was read as a rooted path outside the project, withheld, and noted as a
// withheld path whose "basename", a regex fragment (`b`, `s`, `(`), then withheld every free text in
// the build holding it where a name starts (`go build ./...`, `git status`); a `^` removed as a cmd
// escape turned `^\s*func\b` into one; and a one-letter basename of any withheld path did the same.
// A backslash-led word that reads as a regular expression (regexLike) is no path, a caret before a
// separator is kept, a value is noted only when it is one word, and a basename shorter than
// minCutPrefix is not noted. A drive-less Windows path, and a POSIX path, outside the project stay
// withheld.
func TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), []string{previewRoot("other", "b")},
		[]string{
			`\bConfigLoader\b`, `\w+Error\b`, `\s`, `\.Evaluate\(`, filepath.Join(root, "internal") + ` \bretryBackoff\b`,
			`^\s*//\s*TODO`, `^\s*func\b`, `src \d+\.\d+\.\d+`, `\s+$`, `grep -E "\bfoo\b" -r src/`,
			"go build ./...", "git branch -a", "git status", "ls src/", "echo (done)", "npm run build", "bash scripts/x.sh",
		},
		[]string{`type \Users\me\.ssh\id_rsa`, `\Users\me\.aws\credentials`, "cat /etc/passwd"},
		[]string{"id_rsa", "credentials", "passwd"})
}

// TestBuild_ARelativePathGluedToAFlagThatLeavesTheProjectIsWithheld: a path glued to a short option
// started only at a separator, the root or a drive, never at `.`, so `-C../x` was judged as one word
// whose first segment is `-C..`, and a sibling worktree or a directory above the project was shown.
// A revision range (`main..feature`) is not a path.
func TestBuild_ARelativePathGluedToAFlagThatLeavesTheProjectIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{
			"git log main..feature", "git diff HEAD~3..HEAD", "git diff origin/main...HEAD", "go test -run=TestX ./...",
			"gcc -I./include a.c", "go build -o./bin/app .",
		},
		[]string{
			"git -C../qompack-develop log -1", "gcc -I../openssl/include a.c", "tar -C../backup -xf a.tar",
			"go build -o../bin/app .", "git -C.. status",
		},
		[]string{"qompack-develop", "openssl", "backup"})
}

// TestBuild_ARuleLiteralIsMatchedOnlyWhereANameStarts: a rule's literal was matched anywhere inside a
// text, so `git fetch` held `etc` under `Read(//etc/**)` and `tsconfig.json` held `config` under
// `Read(~/.kube/config)`. A literal taken from a whole segment is spelled at a segment's start in
// every path the rule refuses, so it is matched where a name starts (or glued to a short option),
// as a withheld name is; a literal taken from inside a glob run (`.env` for `**/*.env`) may begin
// inside a name and is still matched anywhere.
func TestBuild_ARuleLiteralIsMatchedOnlyWhereANameStarts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		root            string
		rules           []string
		shown, withheld []string
	}{
		{
			"etc", previewRoot("fetcher"),
			[]string{"//etc/**"},
			[]string{
				"git fetch origin", "git fetch --all --prune", "npm run fetch-data", "cat sketch.md",
				"go get github.com/stretchr/testify",
			},
			[]string{"cat /etc/passwd"},
		},
		{
			"kube config", previewRoot("proj"),
			[]string{"~/.kube/config"},
			[]string{"cat tsconfig.json", "go vet ./xconfig/..."},
			[]string{"cat ~/.kube/config"},
		},
		{
			"env file", previewRoot("proj"),
			[]string{"./.env"},
			[]string{"echo process.env.NODE_ENV", "cat src.env"},
			[]string{"cat ./.env", "cat x/.env", "cat .env.local", "tar -C.env x", "type .\\.env"},
		},
		{
			"env glob", previewRoot("proj"),
			[]string{"**/*.env"},
			nil,
			[]string{"cat prod.env", "echo process.env.NODE_ENV"},
		},
		{
			"secrets", previewRoot("proj"),
			[]string{"./secrets/**"},
			[]string{"cat mysecrets.md", "go test ./internal/nosecrets/..."},
			[]string{"cat secrets/token.txt", "tar -Csecrets -cf x.tar .", "kubectl get secrets"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireScreened(t, tc.root, hostRules(tc.root, tc.rules...), nil, tc.shown, tc.withheld, nil)
		})
	}
}

// TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath: a summary that starts at the root and
// goes on below it with a space went to the host whole, so a command run from the project root
// (`<root>\tools\lint.ps1 --since HEAD~1`) or a Grep preview of path then pattern cost a host
// judgement of its arguments, which the host refuses on Windows as an 8.3 name it cannot resolve,
// and the refused command was noted as a withheld path whose fragments withheld other summaries in
// the build (`git diff src HEAD~1`). The host judges the stretch from the root through the last word
// that holds a separator, once; a multi-word value is never noted.
func TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath(t *testing.T) {
	root := previewRoot("proj")
	lint := filepath.Join(root, "tools", "lint.ps1")
	src := filepath.Join(root, "src")
	run := filepath.Join(root, "scripts", "build.sh")
	linked := filepath.Join(root, "linked", "go.sh")
	asked := map[string]int{}
	hp := func() HostRules {
		return HostRules{Patterns: []string{"./private/**"}, Refuses: func(p string) bool {
			asked[p]++
			rel, err := filepath.Rel(root, p)
			// `linked` stands for a link into private/, and a `~1` word for an 8.3 name the host cannot
			// resolve: only the host knows either.
			return strings.Contains(p, "~1") || strings.Contains(p, "~2") ||
				(err == nil && (strings.HasPrefix(rel, "linked") || strings.HasPrefix(rel, "private")))
		}}
	}
	requireScreened(t, root, hp, nil,
		[]string{
			lint + " --since HEAD~1", src + " HEAD~1", run + " --fast -n 3 && echo ok",
			"git diff src HEAD~1", "pwsh tools/lint.ps1 --since HEAD~2 -Fix", "git diff HEAD~1", "go test ./... --since ~2 days",
		},
		[]string{linked + " --since HEAD~1"},
		nil)
	require.Equal(t, map[string]int{lint: 1, src: 1, run: 1, linked: 1}, asked,
		"the host judges each rooted summary's path once, never its arguments")
}

// TestBuild_AJSONPreviewCostsAtMostOneHostJudgement: each element of a path-named array, and each of
// several path-named arguments, went to the host, so one summary could cost a judgement per value
// (about thirty in 120 bytes). A JSON preview with exactly one path-named value is judged through
// it; one with more is screened as free text alone, as a multi-word relative Grep preview is.
func TestBuild_AJSONPreviewCostsAtMostOneHostJudgement(t *testing.T) {
	root := previewRoot("proj")
	asked := map[string]int{}
	inner := hostRules(root, "./private/deny.txt")
	hp := func() HostRules {
		h := inner()
		refuses := h.Refuses
		h.Refuses = func(p string) bool { asked[p]++; return refuses(p) }
		return h
	}
	requireScreened(t, root, hp, nil,
		[]string{
			`{"paths":["s/a1.go","s/b1.go","s/c1.go","s/d1.go","s/e1.go","s/f1.go"]}`,
			`{"dest_path":"b.go","source_path":"a.go"}`,
			`{"paths":["src/one.go"]}`,
		},
		[]string{
			`{"paths":["src/a.go","private/deny.txt"]}`,
			`{"paths":["src/a.go","../outside/x.txt"]}`,
		},
		[]string{"deny.txt", "outside/x.txt"})
	require.Equal(t, map[string]int{"src/one.go": 1}, asked,
		"a JSON preview costs one host judgement only through its one path-named value")
}

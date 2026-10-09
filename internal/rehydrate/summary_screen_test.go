package rehydrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
)

// The tool-summary gate as coordinator decision D63 rules it (C4.6, D50; ADR 0011 §23 items 5 to 10).
// Rounds 1 and 2 tried to find every path inside arbitrary text, and D61's screen tried to undo every
// shell's quoting; each closed some spellings and opened others. A structured summary, the store's
// preview of one path argument, is judged whole as a file pointer is. Free text costs no host
// judgement and is shown only when a whitelist proves every token safe, no token names an absolute or
// escaping path, and the text names no Read rule's literal and no path the build withholds.

// hostRules stands in for the host's Read deny rules written as these patterns: `./a/b.txt`
// refuses that project file and `./dir/**` everything under the project directory dir, in any
// spelling of the path, relative or absolute, case-folded where the platform folds paths.
func hostRules(root string, patterns ...string) HostPaths {
	refuses := func(p string) bool {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		rel, err := filepath.Rel(root, filepath.Clean(p))
		if err != nil {
			return false
		}
		rel = paths.Key(filepath.ToSlash(rel))
		for _, pt := range patterns {
			spec := paths.Key(strings.TrimPrefix(pt, "./"))
			if dir, ok := strings.CutSuffix(spec, "/**"); ok {
				if rel == dir || strings.HasPrefix(rel, dir+"/") {
					return true
				}
				continue
			}
			if rel == spec {
				return true
			}
		}
		return false
	}
	return func() HostRules { return HostRules{Refuses: refuses, Patterns: patterns} }
}

// requireScreened builds a payload whose checkpoint records files and one tool pointer for each
// summary in shown and withheld, judged against hp, and requires section 6 to show each of shown
// as recorded and to withhold each of withheld, with none of leaks anywhere in the payload or the
// drop report.
func requireScreened(t *testing.T, root string, hp HostPaths, files, shown, withheld, leaks []string) Result {
	t.Helper()
	for _, s := range append(append([]string(nil), shown...), withheld...) {
		require.LessOrEqual(t, len(s), previewWidth, "fixture: %q is wider than a store preview", s)
	}
	cp := ckUAT05()
	cp.Pointers.Files = nil
	for _, f := range files {
		cp.Pointers.Files = append(cp.Pointers.Files, checkpoint.FilePointer{Path: f, Hash: hashOf("file " + f), Why: "referenced"})
	}
	cp.Pointers.Tools = nil
	for i, s := range shown {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_ok_%d", i)), Hash: hashOf("ok" + s), Summary: s,
		})
	}
	for i, s := range withheld {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_no_%d", i)), Hash: hashOf("no" + s), Summary: s,
		})
	}
	d := uat05Deps(t, cp)
	d.HostPaths = hp
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, leaks)

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for i, s := range shown {
		require.Contains(t, section6, pointerLine(toolCall(core.ToolUseID(fmt.Sprintf("toolu_ok_%d", i))), hashOf("ok"+s), s),
			"%q names no withheld path, so it is shown as recorded", s)
	}
	for i, s := range withheld {
		require.Contains(t, section6, fmt.Sprintf("- expand(tool_use_id=\"toolu_no_%d\") %s — %s", i, hashOf("no"+s), withheldSummary),
			"%q names a withheld path", s)
	}
	return res
}

// TestBuild_NestedShellsNeverShowADeniedPath, under D63, no longer models what each shell's quoting
// means: a command that runs another shell (`powershell -Command`, `bash -c`, `sh -c`, `wsl -e`)
// quotes the inner command with single quotes, backticks or escapes, and every such token is outside
// the whitelist (a quote inside a run, a backtick, a doubled `\`, a single `&` glued to a word), so
// the whole summary is withheld — whether it names a denied file or an allowed one (accepted
// over-withholding, ADR 0011 §23). A `&&` glued to a word splits it (operatorPieces), so `cat
// docs/guide.md&&ls` is shown, as it was before D63. The privacy guarantee holds: no denied path
// reaches section 6.
func TestBuild_NestedShellsNeverShowADeniedPath(t *testing.T) {
	root := previewRoot("proj")
	commands := func(dir, john, secret, paren, plain string) []string {
		return []string{
			`powershell -Command "Get-Content '` + dir + `/` + john + `'"`,
			`pwsh -NoProfile -c "gc '` + dir + `/` + secret + `'"`,
			`bash -c "cat '` + dir + `/` + secret + `'"`,
			`sh -c "cat \"` + dir + `/` + paren + `\""`,
			`wsl -e bash -c "cat '` + dir + `/` + strings.ReplaceAll(john, "'", `'\''`) + `'"`,
			`bash -c "cat ` + dir + `/` + strings.ReplaceAll(secret, " ", `\\ `) + `"`,
			`cmd /c type "` + dir + `\` + paren + `"`,
			`echo it\'s && cat "` + dir + `/` + paren + `"`,
			`cat ` + dir + `/` + plain + `&&ls`,
			`type ` + dir + `\` + plain + `&echo done`,
		}
	}
	denied := []string{"private/John's notes.txt", "private/my secret.txt", "private/deny (1).txt", "private/deny.txt"}
	docs := commands("docs", "John's notes.md", "my notes.md", "draft (1).md", "guide.md")
	shown := []string{docs[8]}
	withheld := append(append(docs[:8:8], docs[9:]...),
		commands("private", "John's notes.txt", "my secret.txt", "deny (1).txt", "deny.txt")...)
	requireScreened(t, root, hostRules(root, prefixed("./", denied)...), nil, shown, withheld,
		[]string{"John''s notes.txt", "my secret.txt", `my\\ secret.txt`, "deny (1).txt", "deny.txt"})
}

// prefixed is each of s with p before it.
func prefixed(p string, s []string) []string {
	out := make([]string, len(s))
	for i, e := range s {
		out[i] = p + e
	}
	return out
}

// TestBuild_ADotRelativeWithheldPathInFreeTextIsWithheld is the round-2 verifier's second finding:
// a withheld path the build records, written `./`-relative in free text with a space in it, was
// shown. The search for recorded paths needed a word boundary before the path, and `/` is not one.
func TestBuild_ADotRelativeWithheldPathInFreeTextIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	deny := []string{"private/my secret.txt"}
	requireScreened(t, root, hostRules(root, prefixed("./", deny)...), deny,
		[]string{`cp ./docs/my notes.md backup/`, `{"query":"open ./docs/my notes.md now"}`},
		[]string{
			`cp ./private/my secret.txt backup/`,
			`{"query":"open ./private/my secret.txt now"}`,
			`copy .\private\my secret.txt backup\`,
		},
		[]string{"my secret.txt"})
}

// TestBuild_ADirectoryRuleNeverPoisonsAnUnrelatedSummary is the round-2 verifier's third finding.
// Round 2 noted every word and span of a summary that the host refused as a known withheld path,
// with each of its path-segment suffixes, so under a directory rule `cat private/main.go` made
// main.go known, and every other summary naming a main.go or the word config was withheld. The
// paths the build knows to withhold now come from file pointers, path-keyed checkpoint drops and
// structured summaries only, never from a fragment of free text.
func TestBuild_ADirectoryRuleNeverPoisonsAnUnrelatedSummary(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/**"), nil,
		[]string{"go vet main.go", "grep -rn config src/", `{"query":"load config"}`, "go build ./cmd/main.go"},
		[]string{"cat private/main.go", "cat private/config", "ls private"},
		[]string{"private/main.go", "private/config"})
}

// TestBuild_FileURLsAndDriveRelativePathsAreWithheld: a file:// URL (D63(3) names it outright) and a
// drive-relative Windows path (`D:secret.txt`, the file secret.txt in drive D's current directory)
// name paths outside the project, and are withheld. Under D63 a file URL is withheld even when it
// names a path inside the project (over-withholding, since `file://` is never a whitelist-safe URL);
// an http(s) URL is shown.
func TestBuild_FileURLsAndDriveRelativePathsAreWithheld(t *testing.T) {
	root := previewRoot("proj")
	slash := strings.TrimPrefix(filepath.ToSlash(root), "/")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{
			"https://example.com/docs/index.html",
			"curl -s https://example.com/api/v1/items?page=2",
		},
		[]string{
			"file:///etc/passwd",
			"curl -s file:///etc/hosts",
			"file://fileserver/share/payroll.xlsx",
			`{"url":"file:///C:/Users/someone/secret.txt"}`,
			"D:secret.txt",
			"type D:secret.txt",
			`Get-Content "D:notes\secret.txt"`,
			"file:///" + slash + "/src/main.go",
		},
		[]string{"passwd", "/etc/hosts", "payroll", "secret.txt"})
}

// TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfADeniedPath is the round-2 verifier's eighth
// finding: the store cuts a preview at 120 bytes with `…`, and when the cut falls inside a denied
// path the summary ends in a prefix of it, which no exact-file rule refuses. A truncated summary is
// withheld when its last word is a prefix, three characters or longer, of a rule's literal, of a
// withheld path, or of a rule pattern's path. A cut word that is a prefix of none of them is shown.
func TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfADeniedPath(t *testing.T) {
	root := previewRoot("proj")
	pad := "git status --short && git diff --stat && git log --oneline -n 3 && go vet ./... && echo ok"
	cut := func(tail string) string {
		s := pad + " " + tail
		require.LessOrEqual(t, len(s), previewWidth-len("…"), "fixture: %q", s)
		return s + "…"
	}
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{cut("go test ./internal/reh"), cut("cat docs/gu"), cut("cat de")},
		[]string{
			cut("cat private/den"),
			cut("cat den"),
			cut("cat ./priv"),
			filepath.Join(root, "private", "den") + "…",
			`{"query":"open private/deny.t…`,
		},
		[]string{"private/den", "cat den", "priv…"})
}

// TestBuild_ADropReasonNeverShowsAnOutsideOrWithheldPath is the round-2 verifier's sixth finding,
// and the rest of D61(3): section 7 and dropped() never show, in a drop entry's reason, an absolute
// path outside the project or a path the build withholds. pointer_git_unavailable carried the git
// error verbatim, which in a linked worktree names its gitdir outside the project; a rule or skill
// scan error can name a directory above the project; and a path-scoped rule's drop named the file
// pointer it matched, a withheld one included. The path is redacted and the error kind kept.
func TestBuild_ADropReasonNeverShowsAnOutsideOrWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	outside := filepath.Join(filepath.Dir(root), "elsewhere")
	gitdir := filepath.Join(outside, "main", ".git", "worktrees", "proj")

	// The real checkpointer's pointer_git_unavailable for a linked worktree whose gitdir has no index.
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o600))
	validated, err := checkpoint.ValidatePointers(context.Background(), root,
		checkpoint.Pointers{Files: []checkpoint.FilePointer{{Path: "a.txt", Hash: hashOf("a")}}})
	require.NoError(t, err)
	gitDrop, ok := dropForKind(validated, "pointer_git_unavailable", "")
	require.True(t, ok, "fixture: the checkpointer reports the missing index: %v", validated)
	require.Contains(t, gitDrop.Detail, filepath.Join("main", ".git"), "fixture: the git error names the gitdir")

	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...), gitDrop,
		checkpoint.DropEntry{
			Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: reading index: open " +
				filepath.Join(gitdir, "index") + ": The system cannot find the file specified.",
		})
	d := uat05Deps(t, cp)
	d.HostPaths = hostRules(root, "./private/**")
	d.Rules = &fakeScanner{
		pathScoped: []rules.Rule{{Path: ".claude/rules/private.md", Globs: []string{"private/**"}, Body: "Never print these files."}},
		nestedErr:  errors.New("rules: read " + filepath.Join(outside, "CLAUDE.md") + ": Access is denied."),
	}
	d.Skills = &fakeIndexer{err: errors.New("skills: list " + filepath.Join(outside, ".claude", "skills") + ": Access is denied.")}
	r := requestFor(t, cp, core.Tokens(400))
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	_, ok = dropForKind(res.Dropped, "path_rule", ".claude/rules/private.md")
	require.True(t, ok, "fixture: the path-scoped rule did not fit: %v", res.Dropped)
	requireNoLeak(t, res, []string{
		outside, strings.ReplaceAll(outside, `\`, `\\`), "elsewhere", "worktrees", "deny.txt", "private/",
	})
	var gitKinds int
	for _, e := range res.Dropped {
		if e.Kind == "pointer_git_unavailable" {
			gitKinds++
			require.Contains(t, e.Detail, "git index unsupported", "the error kind is kept: %+v", e)
		}
	}
	require.Equal(t, 2, gitKinds, "every git drop is still reported: %v", res.Dropped)
	nested, ok := dropForKind(res.Dropped, "nested_claude_md", "scan")
	require.True(t, ok, "the scan error is still reported: %v", res.Dropped)
	require.True(t, strings.HasPrefix(nested.Detail, "rules: "), "the error kind is kept: %+v", nested)
	require.Contains(t, nested.Detail, "Access is denied.", "the system's error is kept: %+v", nested)
}

// TestBuild_UsefulSummariesAreShownUnderTheUAT12Rules is D61's usefulness row: under UAT-12's rules
// (Read(./private/deny.txt), Read(./.env), Read(./secrets/**)), in a project whose root has a space
// in it, the summaries a session leaves every day are shown, and the ones naming a denied path are
// not.
func TestBuild_UsefulSummariesAreShownUnderTheUAT12Rules(t *testing.T) {
	root := previewRoot("John Smith", "proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt", "./.env", "./secrets/**"), []string{"src/main.go"},
		[]string{
			"cd " + root + " && go test ./...",
			"git diff HEAD~1",
			"git log --oneline HEAD~3..HEAD",
			"git -C " + root + " status",
			"npm test",
			"go test -run TestX ./internal/...",
			"grep -rn TODO src/",
			`git commit -m "fix the bug"`,
			`{"query":"path:src/main.go"}`,
			`{"query":"path:src/main.go retry"}`,
			`{"url":"https://example.com/a"}`,
			"https://example.com/search?a=1&b=2",
			`{"description":"run the tests","prompt":"go test ./... and report the failures"}`,
			filepath.Join(root, "src", "main.go"),
			filepath.Join(root, "src", "main.go") + " TODO",
			`cat "` + filepath.Join(root, "src", "main.go") + `"`,
			"ls src/ 2>/dev/null",
			"café/sub/x.txt",
		},
		[]string{"cat .env", "cat secrets/token.txt", `{"query":"path:private/deny.txt"}`, "ls " + filepath.Join(root, "secrets")},
		[]string{"deny.txt", "token.txt"})
}

// TestBuild_ARuleOverTheWholeProjectWithholdsEveryFreeText: a rule anchored above the project
// (`~/**` with the project under the home directory, or the project's parent spelled from the
// filesystem root) refuses every project path, so no literal of it tells a free-text summary that
// names one apart from one that names none, and every free-text summary is withheld.
func TestBuild_ARuleOverTheWholeProjectWithholdsEveryFreeText(t *testing.T) {
	root := previewRoot("proj")
	parent := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(root)), "/")
	if runtime.GOOS == "windows" {
		parent = "/" + strings.ToLower(parent[1:2]) + parent[3:]
	}
	for _, pattern := range []string{"~/**", "/" + parent + "/**"} {
		t.Run(pattern, func(t *testing.T) {
			all := func() HostRules {
				return HostRules{Refuses: func(string) bool { return true }, Patterns: []string{"./private/**", pattern}}
			}
			requireScreened(t, root, all, nil, nil,
				[]string{"npm test", "go test ./...", `{"query":"retry logic"}`}, nil)
		})
	}
}

// screenLiteral is what screenBy reads from one rule specifier, ruleSegments and then literalOf (the
// gate itself has no such wrapper): the part of the specifier that every path the rule refuses spells
// (D61(2)(a)): its last segment when that has no glob syntax (`deny.txt`, `.env`, `John's
// notes.txt`); else the nearest all-literal segment before it (`secrets` for `./secrets/**`,
// `private` for `./private/*.txt`); else the longest literal run of the last segment (`.env` for
// `**/*.env`, `.pem` for `*.pem`). It is in screen form, and "" when the specifier has no literal
// part at all (`Read`, `./**`, `~/**`), which refuses everything below its anchor. anchored reports a
// specifier measured from outside the project (`//`, `/`, `~`, a drive, `..`), whose literal may lie
// in the project's own path.
func screenLiteral(spec string) (lit string, anchored bool) {
	segs, anchored := ruleSegments(spec)
	lit, _ = literalOf(segs)
	return lit, anchored
}

// TestScreenLiteral_IsTheRulePatternsLiteralPart pins D61(2)(a)'s screen literal: a rule's last
// segment when it has no glob syntax, else the nearest all-literal segment before it, else the
// longest literal run of the last segment, in screen form; nothing for a rule with no literal part,
// and anchored for one measured from outside the project.
func TestScreenLiteral_IsTheRulePatternsLiteralPart(t *testing.T) {
	for _, tc := range []struct {
		spec, lit string
		anchored  bool
	}{
		{"./private/deny.txt", "deny.txt", false},
		{".env", ".env", false},
		{"./.env", ".env", false},
		{"./private/John's notes.txt", paths.Key("Johns notes.txt"), false},
		{"./private/my  secret.txt", "my secret.txt", false},
		{"./secrets/**", "secrets", false},
		{"secrets/", "secrets", false},
		{"./private/*.txt", "private", false},
		{"**/*.env", ".env", false},
		{"*.pem", ".pem", false},
		{"**/.env*", ".env", false},
		{"./a/[sS]ecret*.key", "a", false},
		{"**/[sS]ecret*.key", "ecret", false},
		{"./**", "", false},
		{"**", "", false},
		{"", "", false},
		{"~/.ssh/**", ".ssh", true},
		{"~", "", true},
		{"~/**", "", true},
		{"//etc/shadow", "shadow", true},
		{"//c/Users/me/**", "me", true},
		{`C:\Users\me\secret.txt`, "secret.txt", true},
		{"/build/keys/**", "keys", true},
		{"../shared/creds.json", "creds.json", true},
	} {
		lit, anchored := screenLiteral(tc.spec)
		require.Equal(t, tc.lit, lit, "the literal of %q", tc.spec)
		require.Equal(t, tc.anchored, anchored, "whether %q is anchored outside the project", tc.spec)
	}
}

// TestBuild_FreeTextAsksTheHostNothing is D63's cost rule inside the package (ADR 0011 §23 item 10):
// a build asks the host about each file pointer, path-keyed checkpoint drop and structured summary
// once (a cut one by its directory), and about no free text, whatever its shape: a one-word URL with a
// query string and a cut value among several path-named values are free text too.
func TestBuild_FreeTextAsksTheHostNothing(t *testing.T) {
	root := previewRoot("proj")
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "src/a.go", Hash: hashOf("a"), Why: "referenced"},
		{Path: "private/deny.txt", Hash: hashOf("d"), Why: "referenced"},
	}
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{Kind: "pointer_missing", ID: "src/gone.go", Detail: "file no longer exists in the working tree"})
	structured := []string{
		filepath.Join(root, "src", "b.go"), "**/*.go", "src", `{"notebook_path":"nb/x.ipynb","cell_id":"c1"}`,
		filepath.Join(root, "src", "a.go"),
		// A cut path-named value that is its preview's only one: its directory is judged once.
		cutJSONArg(t, "file_path", "docs/guide.md", len("docs/gui")),
	}
	free := []string{
		"git log --oneline -n 5 --stat src/a.go docs/guide.md and grep for TODO in the diff then stop",
		"cd " + root + " && go test ./...", "git diff HEAD~1", `{"query":"path:src/a.go retry"}`,
		`{"description":"run tests","prompt":"go test ./... then report failures"}`,
		"curl -s https://example.com/api/v1/items?page=2", "file:///etc/passwd", "D:secret.txt",
		// A one-word URL with a query string, and a cut value among several path-named values.
		"https://example.com/a?b=c", "https://example.com/search?a=1&b=2",
		cutPathArray(t, []string{"mod1/aaaa.go", "mod1/bbbb.go", "mod1/cccc.go"}, "mod7/hhhh.go", len("mod7/hh")),
	}
	cp.Pointers.Tools = nil
	for i, s := range append(append([]string(nil), structured...), free...) {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%02d", i)), Hash: hashOf(s), Summary: s,
		})
	}
	asked := map[string]int{}
	inner := hostRules(root, "./private/deny.txt")
	d := uat05Deps(t, cp)
	d.HostPaths = func() HostRules {
		h := inner()
		refuses := h.Refuses
		h.Refuses = func(p string) bool { asked[p]++; return refuses(p) }
		return h
	}
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	_, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	want := map[string]int{
		"src/a.go": 1, "private/deny.txt": 1, "src/gone.go": 1, // file pointers and the path-keyed drop
		filepath.Join(root, "src", "b.go"): 1, "**/*.go": 1, "src": 1, "nb/x.ipynb": 1, // structured
		filepath.Join(root, "src", "a.go"): 1, "docs": 1,
	}
	require.Equal(t, want, asked, "each path is judged once per build, and no free text is judged")
}

// TestBuild_AOneWordArgumentIsJudgedByItsShape: a one-word summary is judged whole as the one file a
// Read, Write or Edit names AND must pass the free-text whitelist (D63). So a word glued from a
// command (`path:…`, `cat<…`) is screened, and a relative Grep argument spelling a withheld file's
// name or a rule's literal is withheld. `<root>/docs/deny.md` is shown: the rule's literal is
// `deny.txt`, which a `.md` sibling does not hold. Criterion change (D63): the whitelist's name screen
// cannot tell the project's own `<root>/README.md` from the denied `private/README.md`, whose rule's
// literal is `README.md`, so the absolute path is withheld too.
func TestBuild_AOneWordArgumentIsJudgedByItsShape(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/README.md", "./private/deny.txt"), []string{"private/README.md"},
		[]string{filepath.Join(root, "docs", "deny.md"), "docs/guide.md", "TODO"},
		[]string{
			filepath.Join(root, "README.md"), "README.md", "deny.txt", "path:private/deny.txt",
			"cat<private/deny.txt", "x=private/deny.txt",
		},
		[]string{"private/deny.txt", "private/README.md"})
}

// TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason pins the reason gate's word boundary
// (D61(3)), found by TestBuild_NeverExceedsTheHostCeiling: a structured summary `/W`, a path outside
// the project, is withheld and its basename is a withheld name. Matched inside other words, it
// redacted every drop reason holding the letter w, the tier-1 overflow's `OVERFLOW: … whole …`
// among them, which then no longer read as an overflow and no longer named its record. A reason is
// redacted only where it names the withheld path as a word or a path segment.
func TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason(t *testing.T) {
	cp := ckUAT05()
	cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{ToolUseID: "toolu_w", Hash: hashOf("w"), Summary: "/W"})
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: reading index: open /W: denied"})
	res, err := Build(context.Background(), requestFor(t, cp, core.Tokens(150)), uat05Deps(t, cp))
	require.NoError(t, err)
	inv, ok := dropForKind(res.Dropped, ItemInvariants.String(), "tier1")
	require.True(t, ok, "fixture: the pin is a tier-1 overflow: %v", res.Dropped)
	require.True(t, strings.HasPrefix(inv.Detail, "OVERFLOW: pinned invariant "+uat05PinID+" "), "%+v", inv)
	require.True(t, Overflowed(res.Dropped))
	git, ok := dropForKind(res.Dropped, "pointer_git_unavailable", "")
	require.True(t, ok, "%v", res.Dropped)
	require.Equal(t, "checkpoint: git index unsupported: reading index: "+withheldDropID+": denied", git.Detail,
		"a reason naming the withheld path as a word is redacted, its error kind kept")
}

// TestBuild_AWithheldAnchorPoisonsNoFreeText: a structured summary that is only an anchor (`~`, a
// bare drive `D:`, `$HOME`) is withheld as outside the project, but it names no file, so it adds no
// name to the free-text screen; were `~` a withheld name, every `HEAD~1` would be withheld.
//
// Criterion change (D63): `echo $HOMEPAGE is set` is no longer a shown row, because under the
// whitelist any `$` at a token start makes the summary unsafe (a variable cannot be proven inert), so
// it is withheld for its `$`, not as poisoning. The `~`, `D:` and `$HOME` anchors stay withheld and
// still poison nothing (`git diff HEAD~1` is shown).
func TestBuild_AWithheldAnchorPoisonsNoFreeText(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{"git diff HEAD~1", "git commit -m added: tests"},
		[]string{"~", "D:", "$HOME", "echo $HOMEPAGE is set"},
		nil)
}

package rehydrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// Privacy in argument summaries (owner decision D50, C4.6; UAT-12 F1 on candidate 7,
// plans/sdd/V6-closeout/live/rerun-c7/UAT-12/sessionB/injected-2-SessionStart-compact.txt line 36).
// With the deny rule Read(./private/deny.txt) in force, section 6 showed
//
//	- tool_use toolu_017m9djGav7vcngkniobgwim sha256:f29d6a64… — {"query":"path:private/deny.txt"}
//
// the argument summary of a recall call whose path: selector names the denied file. The pointer gate
// judged the piece "path:private/deny.txt" as a path, and no host rule refuses that spelling. A path
// reaches a summary in more shapes than a bare token: behind a selector prefix, as the value of a
// path-style argument with neither a dot nor a separator in it, and as a glob that selects it.

// previewWidth is the width of the store's preview of a call's arguments (store.argsPreviewMax),
// which every summary a checkpoint records stays within: the store cuts a longer one.
const previewWidth = 120

// previewRoot is an absolute project root short enough that the absolute summaries a row builds
// under it stay within previewWidth, as real previews do. Build reads no files, so it need not exist.
func previewRoot(elem ...string) string {
	vol := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	return filepath.Join(append([]string{vol, "q"}, elem...)...)
}

// denyFiles stands in for host permissions.deny Read rules on exact files (the UAT-12 rule was
// Read(./private/deny.txt)): it refuses each named project file, in any spelling of its path, and
// nothing else — not its directory and not a sibling. It hands the build each rule's pattern,
// Read(./<file>).
func denyFiles(root string, rels ...string) HostPaths {
	denied := make(map[string]bool, len(rels))
	patterns := make([]string, 0, len(rels))
	for _, r := range rels {
		denied[filepath.Clean(filepath.Join(root, filepath.FromSlash(r)))] = true
		patterns = append(patterns, "./"+r)
	}
	return func() HostRules {
		return HostRules{Patterns: patterns, Refuses: func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			return denied[filepath.Clean(p)]
		}}
	}
}

func TestBuild_ArgumentSummariesNeverShowAWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	outside := filepath.Join(filepath.Dir(root), "outside", "outside.txt")
	jsonOutside := strings.ReplaceAll(outside, `\`, `\\`)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		// Session A of UAT-12 read the file before the deny rule existed, so the checkpoint points at it.
		{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"},
		{Path: "reports.py", Hash: hashOf("reports"), Why: "referenced"},
	}
	withheldTools := []checkpoint.ToolPointer{
		// The live shape: recall's path: selector naming the denied file.
		{ToolUseID: "toolu_selector", Hash: hashOf("s1"), Summary: `{"query":"path:private/deny.txt"}`},
		// The same selector quoted, as JSON escapes it.
		{ToolUseID: "toolu_quoted", Hash: hashOf("s2"), Summary: `{"query":"path:\"private/deny.txt\" salary"}`},
		// A path-style argument whose value has neither a dot nor a separator.
		{ToolUseID: "toolu_notebook", Hash: hashOf("s3"), Summary: `{"notebook_path":"credentials"}`},
		// The store's identifying-argument preview of a Read or a path argument: the value alone.
		{ToolUseID: "toolu_bare", Hash: hashOf("s4"), Summary: "credentials"},
		// Globs that select the denied file.
		{ToolUseID: "toolu_glob", Hash: hashOf("s5"), Summary: "private/deny.*"},
		{ToolUseID: "toolu_globany", Hash: hashOf("s6"), Summary: "**/deny.txt"},
		{ToolUseID: "toolu_globsel", Hash: hashOf("s7"), Summary: `{"query":"path:private/*.txt"}`},
		// Out of the project, behind a selector and as a glob.
		{ToolUseID: "toolu_selabs", Hash: hashOf("s8"), Summary: `{"query":"path:` + jsonOutside + `"}`},
		{ToolUseID: "toolu_selesc", Hash: hashOf("s9"), Summary: `{"query":"path:../outside/*.txt"}`},
	}
	allowedTools := []checkpoint.ToolPointer{
		{ToolUseID: "toolu_ok_selector", Hash: hashOf("a1"), Summary: `{"query":"path:reports.py"}`},
		{ToolUseID: "toolu_ok_query", Hash: hashOf("a2"), Summary: `{"query":"LUPINE-7731"}`},
		{ToolUseID: "toolu_ok_glob", Hash: hashOf("a3"), Summary: "**/*.py"},
		{ToolUseID: "toolu_ok_dir", Hash: hashOf("a4"), Summary: `{"path":"src"}`},
		{ToolUseID: "toolu_ok_url", Hash: hashOf("a5"), Summary: "https://example.com/docs/index.html"},
		{ToolUseID: "toolu_ok_cmd", Hash: hashOf("a6"), Summary: "cat data/meta.txt"},
	}
	cp.Pointers.Tools = append(append([]checkpoint.ToolPointer(nil), withheldTools...), allowedTools...)

	d := uat05Deps(t, cp)
	d.HostPaths = denyFiles(root, "private/deny.txt", "credentials")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, []string{"deny.txt", "deny.*", "credentials", "outside.txt", "outside/", jsonOutside})

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for _, tp := range withheldTools {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+withheldSummary,
			"a summary naming a withheld path is withheld, and the pointer still points by id and hash")
	}
	for _, tp := range allowedTools {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+tp.Summary,
			"a summary that names no withheld path is shown as recorded")
	}
}

// TestBuild_ArgumentSummariesFailClosedWithoutHostRules: when the host's rules cannot be established,
// a path behind a selector is withheld like every other path (re_read fails closed the same way).
// Criterion change (coordinator decision D61(2)(d)): so is every free-text summary. Round 2 showed
// `{"query":"LUPINE-7731"}` because no word of it looked like a path; free text is now screened by
// the rules' literals, which an unavailable policy does not supply, so no free text can be told
// apart from one naming a refused file, and none is shown.
func TestBuild_ArgumentSummariesFailClosedWithoutHostRules(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_selector", Hash: hashOf("s1"), Summary: `{"query":"path:Makefile"}`},
		{ToolUseID: "toolu_query", Hash: hashOf("s2"), Summary: `{"query":"LUPINE-7731"}`},
	}
	d := uat05Deps(t, cp)
	d.HostPaths = func() HostRules { return HostRules{} }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.NotContains(t, section6, "Makefile", "a selector's value is a path, dot or none")
	require.Contains(t, section6, "- tool_use toolu_selector "+hashOf("s1").String()+" — "+withheldSummary)
	require.Contains(t, section6, "- tool_use toolu_query "+hashOf("s2").String()+" — "+withheldSummary,
		"without the rules' literals no free text can be screened")
	require.NotContains(t, section6, "LUPINE-7731")
}

// TestBuild_JoinedArgumentPreviewsNeverShowAWithheldPath covers the summaries a built-in tool's
// call leaves. The store's preview of one is not JSON: it is the values of file_path, path,
// pattern, command and url joined by spaces (store.argsPreview), so nothing names the argument and
// no token boundary marks where a path ends. A denied path with a space in it split into tokens no
// rule refuses; recall's plain path: selector selects by path-segment suffix
// (store.pathSelector.weight), so a basename names the file; Glob's preview is its directory then
// its pattern; and a path with no dot or separator beside another argument was never judged
// (w19-rehydrate review).
func TestBuild_JoinedArgumentPreviewsNeverShowAWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"},
		{Path: "reports.py", Hash: hashOf("reports"), Why: "referenced"},
	}
	withheldTools := []checkpoint.ToolPointer{
		// A Read of a denied path with a space in it: the preview is the file_path value alone.
		{ToolUseID: "toolu_spaced", Hash: hashOf("j1"), Summary: "private/my secret.txt"},
		// Grep's path, then its pattern.
		{ToolUseID: "toolu_spacedgrep", Hash: hashOf("j2"), Summary: "private/my secret.txt apikey"},
		// A command quoting it.
		{ToolUseID: "toolu_spacedcmd", Hash: hashOf("j3"), Summary: `cat "private/my secret.txt"`},
		// recall's plain selector, spelled by basename.
		{ToolUseID: "toolu_basename", Hash: hashOf("j4"), Summary: `{"query":"path:deny.txt"}`},
		// Glob's directory then pattern: a file the build records, and one Qompack never recorded.
		{ToolUseID: "toolu_globpair", Hash: hashOf("j5"), Summary: "private deny.txt"},
		{ToolUseID: "toolu_globunrecorded", Hash: hashOf("j6"), Summary: "vault keys.txt"},
		// A path with no dot or separator, beside another argument.
		{ToolUseID: "toolu_grepbare", Hash: hashOf("j7"), Summary: "credentials apikey"},
		{ToolUseID: "toolu_catbare", Hash: hashOf("j8"), Summary: "cat credentials"},
	}
	allowedTools := []checkpoint.ToolPointer{
		// A search inside the denied file's directory names the directory, which no rule refuses.
		{ToolUseID: "toolu_ok_grepdir", Hash: hashOf("k1"), Summary: "private salary"},
		{ToolUseID: "toolu_ok_cmd", Hash: hashOf("k2"), Summary: "cat data/meta.txt"},
		{ToolUseID: "toolu_ok_selector", Hash: hashOf("k3"), Summary: `{"query":"path:reports.py"}`},
		{ToolUseID: "toolu_ok_grep", Hash: hashOf("k4"), Summary: "src TODO"},
		{ToolUseID: "toolu_ok_spaced", Hash: hashOf("k5"), Summary: "docs/my notes.md"},
	}
	cp.Pointers.Tools = append(append([]checkpoint.ToolPointer(nil), withheldTools...), allowedTools...)

	d := uat05Deps(t, cp)
	d.HostPaths = denyFiles(root, "private/deny.txt", "private/my secret.txt", "vault/keys.txt", "credentials")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, []string{"deny.txt", "secret.txt", "keys.txt", "credentials"})

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for _, tp := range withheldTools {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+withheldSummary,
			"a summary naming a withheld path is withheld, and the pointer still points by id and hash")
	}
	for _, tp := range allowedTools {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+tp.Summary,
			"a summary that names no withheld path is shown as recorded")
	}
}

// TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames: recall's selector and Glob's pattern match at
// any depth, so `path:README.md` selects a denied private/README.md and is withheld. Criterion change
// (D63): a one-word summary must also pass the free-text whitelist, whose (4) screen cannot tell the
// project's own `<root>/README.md` from the denied `private/README.md` — they share the rule's
// literal `README.md` — so the absolute path is withheld too (accepted over-withholding, ADR 0011
// §23). A rooted path whose basename is not a rule literal (`<root>/docs/guide.md`) is still shown.
func TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/README.md", Hash: hashOf("pr"), Why: "referenced"}}
	rootReadme := filepath.Join(root, "README.md")
	guide := filepath.Join(root, "docs", "guide.md")
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_abs", Hash: hashOf("abs"), Summary: rootReadme},
		{ToolUseID: "toolu_guide", Hash: hashOf("guide"), Summary: guide},
		{ToolUseID: "toolu_sel", Hash: hashOf("sel"), Summary: `{"query":"path:README.md"}`},
	}
	d := uat05Deps(t, cp)
	d.HostPaths = denyFiles(root, "private/README.md")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.Contains(t, section6, "- tool_use toolu_abs "+hashOf("abs").String()+" — "+withheldSummary)
	require.Contains(t, section6, "- tool_use toolu_guide "+hashOf("guide").String()+" — "+guide)
	require.Contains(t, section6, "- tool_use toolu_sel "+hashOf("sel").String()+" — "+withheldSummary)
}

// TestBuild_DelimiterCharactersInADeniedPathNeverShowIt is the w19 verifier's V2. The store's
// preview of a Read, Write or Edit is the file_path value alone, and Grep's is its path then its
// pattern (store.argsPreview), so a denied path holding a parenthesis, an apostrophe, a comma or an
// equals sign is the whole preview or its first words, and a command quotes it whole. Round 1 read
// paths only inside segments it split at those very characters, so it never judged the path whole
// and section 6 showed it, while the file pointer for the same path was withheld.
// A denied path holding a delimiter is withheld whatever the delimiter. Under D63 an allowed path of
// the same shape is shown ONLY when the delimiter is whitelist-safe (`,` and `=`): a parenthesis, an
// apostrophe or a space makes every token unsafe, so the allowed path is withheld too (accepted
// over-withholding, ADR 0011 §23). The denied path is withheld either way, by the rule's literal
// (`a,b.txt`, `k=v.txt`) or by the unsafe character.
func TestBuild_DelimiterCharactersInADeniedPathNeverShowIt(t *testing.T) {
	for _, tc := range []struct {
		denied, allowed, leak string
		// unquoted: the path needs no quoting in a command, so it may be an option's value there.
		unquoted bool
		// allowedShown: the delimiter is whitelist-safe, so the allowed path of the same shape is shown.
		allowedShown bool
	}{
		{"private/deny (1).txt", "docs/draft (1).md", "deny (1)", false, false},
		{"private/deny(2).txt", "docs/draft(2).md", "deny(2)", true, false},
		{"private/John's notes.txt", "docs/John's notes.md", "John's notes.txt", false, false},
		{"private/a,b.txt", "docs/a,b.md", "a,b.txt", true, true},
		{"private/k=v.txt", "docs/k=v.md", "k=v.txt", true, true},
	} {
		t.Run(tc.denied, func(t *testing.T) {
			root := previewRoot("proj")
			spellings := func(rel string) []string {
				abs := filepath.Join(root, filepath.FromSlash(rel))
				out := []string{rel, abs, rel + " apikey", abs + " apikey", `cat "` + rel + `"`, `cat "` + abs + `"`}
				if tc.unquoted {
					out = append(out, "sort --output="+rel+" data.txt")
				}
				for _, s := range out {
					require.LessOrEqual(t, len(s), previewWidth, "fixture: %q is wider than a store preview", s)
				}
				return out
			}
			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{{Path: tc.denied, Hash: hashOf("deny"), Why: "referenced"}}
			var withheld, allowed []checkpoint.ToolPointer
			for i, s := range spellings(tc.denied) {
				withheld = append(withheld, checkpoint.ToolPointer{
					ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_denied_%d", i)), Hash: hashOf("d" + s), Summary: s,
				})
			}
			for i, s := range spellings(tc.allowed) {
				allowed = append(allowed, checkpoint.ToolPointer{
					ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_ok_%d", i)), Hash: hashOf("a" + s), Summary: s,
				})
			}
			cp.Pointers.Tools = append(append([]checkpoint.ToolPointer(nil), withheld...), allowed...)

			d := uat05Deps(t, cp)
			d.HostPaths = denyFiles(root, tc.denied)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireInsideTheHostCeiling(t, res, cp.Session)
			requireNoLeak(t, res, []string{tc.leak})

			section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
			for _, tp := range withheld {
				require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+withheldSummary,
					"a summary naming the denied path %q is withheld", tp.Summary)
			}
			for _, tp := range allowed {
				if tc.allowedShown {
					require.Contains(t, section6, pointerLine("tool_use "+string(tp.ToolUseID), tp.Hash, tp.Summary),
						"a safe-delimiter allowed path is shown as recorded")
				} else {
					require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+withheldSummary,
						"an allowed path with an unsafe delimiter is withheld too (over-withholding)")
				}
			}
		})
	}
}

// TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths is round 1's open issue, ruled on in
// D60: in a project whose own path has a space in it (C:\Users\John Smith\proj), every absolute
// summary was withheld, because its first word, cut at that space, is an absolute path outside the
// project. The project's own absolute paths are shown, in every shape the store's preview takes,
// while a denied one, one outside the project, and one that merely starts like the project's path
// are still withheld.
func TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths(t *testing.T) {
	base := previewRoot()
	root := filepath.Join(base, "John Smith", "proj")
	src := filepath.Join(root, "src", "main.go")
	deny := filepath.Join(root, "private", "deny.txt")
	sibling := filepath.Join(base, "John Smith", "other", "x.txt")
	shown := []string{
		src,
		strings.ReplaceAll(src, `\`, "/"),
		src + " TODO",
		`cat "` + src + `"`,
		`{"notebook_path":"` + strings.ReplaceAll(src, `\`, `\\`) + `"}`,
	}
	withheld := []string{
		deny,
		deny + " apikey",
		filepath.Join(root, "private") + " deny.txt",
		`cat "` + deny + `"`,
		sibling,
		`cat "` + sibling + `"`,
		filepath.Join(base, "John Smith", "proj2", "x.txt"),
		"ls " + filepath.Join(base, "John"),
		// Criterion change (D63): a glob in free text is never shown, though this one's directory is the
		// project's own src.
		filepath.Join(root, "src") + " *.go",
	}
	for _, s := range append(append([]string(nil), shown...), withheld...) {
		require.LessOrEqual(t, len(s), previewWidth, "fixture: %q is wider than a store preview", s)
	}
	cp := ckUAT05()
	cp.Pointers.Files = nil
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
	d.HostPaths = denyFiles(root, "private/deny.txt")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, []string{"deny.txt", filepath.Join("John Smith", "other"), "proj2", "ls " + filepath.Join(base, "John")})

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for i, s := range shown {
		require.Contains(t, section6, pointerLine(fmt.Sprintf("tool_use toolu_ok_%d", i), hashOf("ok"+s), s),
			"an absolute path inside the project is shown although the project's path has a space")
	}
	for i, s := range withheld {
		require.Contains(t, section6, fmt.Sprintf("- tool_use toolu_no_%d %s — %s", i, hashOf("no"+s), withheldSummary),
			"%q names a denied path or one outside the project", s)
	}
}

// requireSummaries builds a payload whose checkpoint records files and one tool pointer for each
// summary in shown and withheld, judged against exact-file host rules on deny, and requires
// section 6 to show each of shown as recorded and to withhold each of withheld, with none of leaks
// anywhere in the payload or the drop report.
func requireSummaries(t *testing.T, root string, deny, files, shown, withheld, leaks []string) {
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
	d.HostPaths = denyFiles(root, deny...)
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, leaks)

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for i, s := range shown {
		require.Contains(t, section6, pointerLine(fmt.Sprintf("tool_use toolu_ok_%d", i), hashOf("ok"+s), s),
			"%q names no withheld path, so it is shown as recorded", s)
	}
	for i, s := range withheld {
		require.Contains(t, section6, fmt.Sprintf("- tool_use toolu_no_%d %s — %s", i, hashOf("no"+s), withheldSummary),
			"%q names a withheld path", s)
	}
}

// TestBuild_TheProjectRootFollowedByMoreWordsIsShown is the w19 round-2 review's first finding.
// The store's preview of a Grep or a Glob whose path is the project root is the root, a space and
// the pattern, and a command names the root as one of its words (`cd <root> && go test ./...`,
// `git -C <root> status`). Read whole, a stretch that starts at the root and runs on into the next
// word is the root's own name with more after it, a sibling of the root outside the project, so
// every such summary was withheld, in every project, with or without host rules, while D60 rules
// that the project's own absolute paths are shown. Each word is judged on its own, and the root is
// in the project. A quoted path, a path-named argument and a word that runs past the root into a
// sibling are still judged, and withheld, as the paths outside the project they are.
func TestBuild_TheProjectRootFollowedByMoreWordsIsShown(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			slash := strings.ReplaceAll(root, `\`, "/")
			sep := string(filepath.Separator)
			shown := []string{
				root + " TODO",
				slash + " TODO",
				root + " func main",
				"cd " + root + " && go test ./...",
				"cd " + slash + " && git log --oneline -n 5",
				"git -C " + root + " status --short",
				"go test ./... && cd " + root,
			}
			withheld := []string{
				root + "2" + sep + "x.txt",
				`cat "` + root + " old" + sep + `x.txt"`,
				`{"file_path":"` + strings.ReplaceAll(root+" old"+sep+"x.txt", `\`, `\\`) + `"}`,
				"cd " + root + " && cat private/deny.txt",
				root + sep + "private deny.txt",
				// D63 over-withholds a glob pattern in free text, though the directory is the project root.
				root + " **/*.go",
			}
			requireSummaries(t, root, []string{"private/deny.txt"}, nil, shown, withheld,
				[]string{"deny.txt", "proj2", "old" + sep + "x.txt", strings.ReplaceAll("old"+sep+"x.txt", `\`, `\\`)})
		})
	}
}

// TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath is the w19 round-2 review's second finding.
// A Bash or PowerShell command names a path the way its shell reads it: PowerShell doubles an
// apostrophe inside a single-quoted string and escapes with a backtick, a POSIX shell escapes a
// space, an apostrophe or a parenthesis with a backslash and honours `\"` inside double quotes, and
// `&&` ends a word without a space. Each is withheld relative and absolute.
//
// Criterion change (D63): the same shapes naming allowed files are withheld too, since each holds an
// apostrophe, a backtick, a parenthesis, a quote inside a run, an `&` glued to a word or a backslash
// that ends a token (an escaped space), none of which the whitelist proves inert.
func TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath(t *testing.T) {
	root := previewRoot("proj")
	slash := strings.ReplaceAll(root, `\`, "/")
	relative := func(dir, ext string) []string {
		return []string{
			`Get-Content -LiteralPath '` + dir + `/John''s notes` + ext + `'`,
			`Get-Content ` + dir + "/my` secret" + ext,
			`cat ` + dir + `/John\'s\ notes` + ext,
			`cat ` + dir + `/my\ secret` + ext,
			`cat ` + dir + `/deny\(2\)` + ext,
			`echo "a\"b" && cat "` + dir + `/deny (1)` + ext + `"`,
			`grep -c "it\"s" "` + dir + `/a,b` + ext + `"`,
			`wc -l ` + dir + `/a,b` + ext + `&&echo`,
			`cat ` + dir + `/plain` + ext + `&&ls`,
		}
	}
	absolute := func(dir, ext string) []string {
		return []string{
			`Get-Content '` + filepath.Join(root, dir, "John''s notes"+ext) + `'`,
			"Get-Content " + slash + "/" + dir + "/my` secret" + ext,
			`cat ` + slash + `/` + dir + `/John\'s\ notes` + ext,
			`cat ` + slash + `/` + dir + `/my\ secret` + ext,
			`echo "a\"b" && cat "` + slash + `/` + dir + `/deny (1)` + ext + `"`,
			`grep -c "it\"s" "` + slash + `/` + dir + `/a,b` + ext + `"`,
		}
	}
	denied := []string{
		"private/John's notes.txt", "private/my secret.txt", "private/deny(2).txt", "private/deny (1).txt",
		"private/a,b.txt", "private/plain.txt",
	}
	leaks := []string{
		"John''s notes.txt", `John\'s\ notes.txt`, "my` secret.txt", `my\ secret.txt`, `deny\(2\).txt`,
		"deny (1).txt", "a,b.txt", "plain.txt",
	}
	// No file pointer records these paths: each denied shape is withheld by the host's rule literal on
	// the path, not by a path the build already knows.
	for _, tc := range []struct {
		name   string
		shapes func(dir, ext string) []string
	}{{"relative", relative}, {"absolute", absolute}} {
		t.Run(tc.name, func(t *testing.T) {
			withheld := append(tc.shapes("docs", ".md"), tc.shapes("private", ".txt")...)
			requireSummaries(t, root, denied, nil, nil, withheld, leaks)
		})
	}
}

// TestBuild_AKnownWithheldPathIsFoundAnywhereInAText is the w19 round-2 review's third finding: a
// path with a space that no producer delimits, in the middle of a free-text argument or a command,
// is read word by word, and no word spells it. Every path the build withholds and records is
// looked for in each text whole, wherever its spaces fall; a path Qompack never recorded stays a
// documented limit (ADR 0011 §23.2).
//
// Criterion change (coordinator decision D61(2)(a)-(b)): `{"query":"not my secret.txt.bak at
// all"}` was shown, because the withheld name was followed by a further `.bak`. Free text is now
// withheld when it holds a rule's literal or a withheld path's basename where a name starts
// (namedAt); what follows the name is not judged, so `my secret.txt.bak` holds `my secret.txt`. D61
// accepts that a text which merely mentions the name is withheld (ADR 0011 §23.2), and the row now
// requires it.
func TestBuild_AKnownWithheldPathIsFoundAnywhereInAText(t *testing.T) {
	root := previewRoot("proj")
	slash := strings.ReplaceAll(root, `\`, "/")
	withheld := []string{
		`{"query":"find private/my secret.txt usages"}`,
		`(cat private/my secret.txt)`,
		`cp private/my secret.txt backup/`,
		`{"query":"where is my secret.txt used"}`,
		`cp ` + slash + `/private/my secret.txt backup/`,
		`{"query":"see @private/deny.txt first"}`,
		`{"query":"not my secret.txt.bak at all"}`,
		`{"query":"PRIVATE/MY SECRET.TXT and more"}`,
	}
	shown := []string{
		`{"query":"find docs/my notes.md usages"}`,
		`cp docs/my notes.md backup/`,
		`{"query":"see @docs/guide.md first"}`,
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		// A path folds case only where the platform's filesystem does (paths.Key).
		shown = append(shown, withheld[len(withheld)-1])
		withheld = withheld[:len(withheld)-1]
	}
	deny := []string{"private/my secret.txt", "private/deny.txt"}
	requireSummaries(t, root, deny, deny, shown, withheld, []string{"my secret.txt usages", "deny.txt first"})
}

// TestBuild_APathSelectorIsJudgedByWhatItSelects is the w19 round-2 review's fourth finding.
// recall's plain path: selector selects a record by equality, by path-segment suffix and by
// containment, and a glob selector by path.Match against the whole key and against each of its
// path-segment suffixes (store.pathSelector.weight). The gate judged a plain selector by equality
// and suffix alone and a glob as rules.Match anchors it, so a selector that selects a withheld file
// by containment or by a glob suffix was shown, spelling most of the path.
func TestBuild_APathSelectorIsJudgedByWhatItSelects(t *testing.T) {
	root := previewRoot("proj")
	withheld := []string{
		`{"query":"path:private/deny.tx"}`,
		`{"query":"path:rivate/deny.txt"}`,
		`{"query":"path:deny"}`,
		`{"query":"path:keep/*.txt"}`,
		`{"query":"path:keep/a.* retry"}`,
		// Criterion change (D63): a glob selector in free text holds `*`, which makes the token unsafe,
		// so it is withheld even when it selects nothing withheld (over-withholding of globs).
		`{"query":"path:src/*.go"}`,
		`{"query":"path:docs/*.md symbol:deny"}`,
	}
	shown := []string{
		`{"query":"path:reports"}`,
	}
	deny := []string{"private/deny.txt", "private/keep/a.txt"}
	requireSummaries(t, root, deny, append([]string{"reports.py"}, deny...), shown, withheld,
		[]string{"deny.tx", "rivate", "path:deny", "keep/"})
}

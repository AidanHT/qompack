package rehydrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
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

// denyFiles stands in for host permissions.deny Read rules on exact files (the UAT-12 rule was
// Read(./private/deny.txt)): it refuses each named project file, in any spelling of its path, and
// nothing else — not its directory and not a sibling.
func denyFiles(root string, rels ...string) func() func(string) bool {
	denied := make(map[string]bool, len(rels))
	for _, r := range rels {
		denied[filepath.Clean(filepath.Join(root, filepath.FromSlash(r)))] = true
	}
	return func() func(string) bool {
		return func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			return denied[filepath.Clean(p)]
		}
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
func TestBuild_ArgumentSummariesFailClosedWithoutHostRules(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_selector", Hash: hashOf("s1"), Summary: `{"query":"path:Makefile"}`},
		{ToolUseID: "toolu_query", Hash: hashOf("s2"), Summary: `{"query":"LUPINE-7731"}`},
	}
	d := uat05Deps(t, cp)
	d.HostPaths = func() func(string) bool { return nil }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.NotContains(t, section6, "Makefile", "a selector's value is a path, dot or none")
	require.Contains(t, section6, "- tool_use toolu_selector "+hashOf("s1").String()+" — "+withheldSummary)
	require.Contains(t, section6, `{"query":"LUPINE-7731"}`, "a summary that names no path is not a path")
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

// TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames: a suffix names a withheld file only when it
// is relative. recall's selector and Glob's pattern match at any depth, so `path:README.md` selects
// a denied private/README.md; the store's preview of a Read is the absolute path of one file, and
// the project's own README.md is not withheld because a file of the same name is.
func TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/README.md", Hash: hashOf("pr"), Why: "referenced"}}
	rootReadme := filepath.Join(root, "README.md")
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_abs", Hash: hashOf("abs"), Summary: rootReadme},
		{ToolUseID: "toolu_sel", Hash: hashOf("sel"), Summary: `{"query":"path:README.md"}`},
	}
	d := uat05Deps(t, cp)
	d.HostPaths = denyFiles(root, "private/README.md")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.Contains(t, section6, "- tool_use toolu_abs "+hashOf("abs").String()+" — "+rootReadme)
	require.Contains(t, section6, "- tool_use toolu_sel "+hashOf("sel").String()+" — "+withheldSummary)
}

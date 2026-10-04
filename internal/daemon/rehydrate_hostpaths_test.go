package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/store"
)

// The rehydration's section 6 follows the host rules re_read follows (owner decision D50, C4.6,
// UAT-12 F4): the daemon hands rehydrate the host's current Read rules through Deps.HostPaths. These
// rows pin the adapter over a hermetic policy (mcpOpHostPolicy: no home, no environment, no managed
// source) with a project deny rule, and that WireRehydrator installs it.

func writeProjectSettings(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, "settings.json")), []byte(body), 0o600))
}

func TestRehydrateHostPaths_RefusesWhatTheHostDenies(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/**)"],"ask":["Read(./ask/**)"]}}`)

	refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
	require.NotNil(t, refuses)
	require.True(t, refuses("private/deny.txt"), "a deny rule refuses the relative spelling")
	require.True(t, refuses(filepath.Join(root, "private", "deny.txt")), "and the absolute one")
	require.True(t, refuses("ask/q.txt"), "an ask rule refuses too: an archived rehydration cannot ask")
	require.False(t, refuses("reports.py"))
}

func TestRehydrateHostPaths_UnreadableRulesFailClosed(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":`)

	require.Nil(t, rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses,
		"rules that cannot be established withhold every path, as re_read does")
}

// TestRehydrateHostPaths_HandsTheBuildTheRulePatterns pins what the free-text screen reads
// (coordinator decision D61, ADR 0011 §23.5): every Read deny and ask rule's specifier, as the
// settings spell it, from the same snapshot Refuses judges against; none when no rule is in force,
// and none, with no Refuses, when the rules cannot be established.
func TestRehydrateHostPaths_HandsTheBuildTheRulePatterns(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./.env)","Read(./secrets/**)"],"ask":["Read(**/*.pem)"]}}`)
	got := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())()
	require.NotNil(t, got.Refuses)
	require.Equal(t, []string{"./private/deny.txt", "./.env", "./secrets/**", "**/*.pem"}, got.Patterns)

	empty := t.TempDir()
	writeProjectSettings(t, empty, `{"permissions":{"deny":["Edit(./src/**)"]}}`)
	got = rehydrateHostPaths(mcpOpHostPolicy(t, empty), empty, logging.Nop())()
	require.NotNil(t, got.Refuses)
	require.Empty(t, got.Patterns, "no Read rule: nothing to screen by")

	broken := t.TempDir()
	writeProjectSettings(t, broken, `{"permissions":`)
	got = rehydrateHostPaths(mcpOpHostPolicy(t, broken), broken, logging.Nop())()
	require.Nil(t, got.Refuses)
	require.Empty(t, got.Patterns)
}

func TestWireRehydrator_InstallsTheHostPathRules(t *testing.T) {
	root := t.TempDir()
	o := Options{ProjectRoot: root, Cfg: testConfig(), HostPolicy: mcpOpHostPolicy(t, root)}
	svc, ok := WireRehydrator(&o).(*rehydrateService)
	require.True(t, ok)
	require.NotNil(t, svc.o.Deps.HostPaths, "section 6 must be judged against the host's rules")
}

// TestRehydrateHostPaths_ASelectorNamingADeniedFileIsWithheld is UAT-12 F1 on candidate 7 through the
// adapter and the real host rules: the project deny rule the live run wrote, Read(./private/deny.txt),
// and the recall call whose path: selector named that file. Section 6 showed its argument summary,
// {"query":"path:private/deny.txt"}; it is withheld, and the pointer still points by id and hash.
func TestRehydrateHostPaths_ASelectorNamingADeniedFileIsWithheld(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/deny.txt)"]}}`)

	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("2a4952b4-730f-41f8-954b-696d176baba8")
	cp.Seq = core.CheckpointSeq(3)
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{
			ToolUseID: "toolu_017m9djGav7vcngkniobgwim", Hash: core.Hash(sha256.Sum256([]byte("selector"))),
			Summary: `{"query":"path:private/deny.txt"}`,
		},
		{
			ToolUseID: "toolu_0151dDKt7HtDjJ4EY3WA5cbU", Hash: core.Hash(sha256.Sum256([]byte("marker"))),
			Summary: `{"query":"ORCHID-DENY-8842"}`,
		},
	}
	req := rehydrate.Request{
		Session: cp.Session, Source: "compact", ProjectRoot: root, Checkpoint: cp, Cfg: testConfig(),
		Ref: checkpoint.Ref{Seq: cp.Seq, Path: filepath.Join(root, ".qompack", "checkpoints", "0003.json")},
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), req, deps)
	require.NoError(t, err)
	require.NotContains(t, res.Text, "deny.txt", "the payload shows the denied path")
	require.Contains(t, res.Text, "- tool_use toolu_017m9djGav7vcngkniobgwim "+
		core.Hash(sha256.Sum256([]byte("selector"))).String()+" — summary withheld")
	require.Contains(t, res.Text, `{"query":"ORCHID-DENY-8842"}`, "a summary that names no path is shown")
}

// TestRehydrateHostPaths_EverySpellingOfADeniedFileIsWithheld is the same deny rule through the real
// host rules, for the spellings the review found still shown after the selector fix: recall's plain
// path: selector by basename (it selects by path-segment suffix), Glob's directory-then-pattern
// preview, and the checkpointer's own drop entry for the pointer (a gitignored file is untracked).
func TestRehydrateHostPaths_EverySpellingOfADeniedFileIsWithheld(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/deny.txt)"]}}`)

	denied := core.Hash(sha256.Sum256([]byte("denied")))
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("2a4952b4-730f-41f8-954b-696d176baba8")
	cp.Seq = core.CheckpointSeq(3)
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: denied, Why: "referenced"}}
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_basename", Hash: core.Hash(sha256.Sum256([]byte("b"))), Summary: `{"query":"path:deny.txt"}`},
		{ToolUseID: "toolu_glob", Hash: core.Hash(sha256.Sum256([]byte("g"))), Summary: "private deny.txt"},
		{ToolUseID: "toolu_marker", Hash: core.Hash(sha256.Sum256([]byte("m"))), Summary: `{"query":"ORCHID-DENY-8842"}`},
	}
	cp.Dropped = []checkpoint.DropEntry{{Kind: "pointer_untracked", ID: "private/deny.txt", Detail: "not tracked by git"}}
	req := rehydrate.Request{
		Session: cp.Session, Source: "compact", ProjectRoot: root, Checkpoint: cp, Cfg: testConfig(),
		Ref: checkpoint.Ref{Seq: cp.Seq, Path: filepath.Join(root, ".qompack", "checkpoints", "0003.json")},
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), req, deps)
	require.NoError(t, err)
	require.NotContains(t, res.Text, "deny.txt", "the payload shows the denied path")
	for _, e := range res.Dropped {
		require.NotContains(t, e.ID+" "+e.Detail, "deny.txt", "the drop report shows the denied path: %+v", e)
	}
	for _, id := range []string{"toolu_basename", "toolu_glob"} {
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — summary withheld", res.Text)
	}
	require.Contains(t, res.Text, "- pointer_untracked "+denied.String()+" — not tracked by git")
	require.Contains(t, res.Text, `{"query":"ORCHID-DENY-8842"}`, "a summary that names no path is shown")
}

// writeProjectFile creates rel (and its directories) under root.
func writeProjectFile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("x\n"), 0o600))
}

// toolPointerRequest is a compact rehydration request for root whose checkpoint holds tools.
func toolPointerRequest(root string, tools []checkpoint.ToolPointer) rehydrate.Request {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("2a4952b4-730f-41f8-954b-696d176baba8")
	cp.Seq = core.CheckpointSeq(3)
	cp.Pointers.Tools = tools
	return rehydrate.Request{
		Session: cp.Session, Source: "compact", ProjectRoot: root, Checkpoint: cp, Cfg: testConfig(),
		Ref: checkpoint.Ref{Seq: cp.Seq, Path: filepath.Join(root, ".qompack", "checkpoints", "0003.json")},
	}
}

// The cost rows' fixture (the w19 verifier's V1 probe, re-derived for coordinator decision D61): a
// project with the verifier's three Read deny rules and rehydrateCostPointers free-text summaries,
// beside rehydrateCostFiles file pointers and rehydrateCostReads structured summaries (the store's
// preview of a Read: its file_path alone).
const (
	rehydrateCostPointers = 80
	rehydrateCostWords    = 17
	// rehydrateCostFiles and rehydrateCostReads are the file pointers and the structured summaries
	// each cost build carries: enough that the bound below is not met by a build that judges nothing.
	rehydrateCostFiles = 10
	rehydrateCostReads = 10
	// rehydrateCostPreviewMax is the width of the store's preview (store.argsPreviewMax), which
	// every fixture preview stays within, as a real one does.
	rehydrateCostPreviewMax = 120
)

// rehydrateCostPreview is the fixture's i-th Bash preview.
func rehydrateCostPreview(i int) string {
	return fmt.Sprintf("git log --oneline -n %d --stat src/mod%d/file%d.go docs/guide%d.md and grep for TODO in the diff then stop",
		i, i, i, i)
}

// costProject is a project for the cost rows: short enough that a preview naming its root fits the
// store's preview width uncut (shortProjectDir), its root canonical (filepath.EvalSymlinks) so that
// the adapter judges each path once rather than once more under a second spelling of the root,
// with the verifier's three Read deny rules and their files.
func costProject(t *testing.T) string {
	t.Helper()
	dir := shortProjectDir(t, "proj")
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	writeProjectSettings(t, root,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./.env)","Read(./secrets/**)"]}}`)
	for _, f := range []string{"private/deny.txt", ".env", "secrets/token.txt"} {
		writeProjectFile(t, root, f)
	}
	return root
}

// costPointers is one tool pointer for each of previews, each within the store's preview width.
func costPointers(t *testing.T, previews []string) []checkpoint.ToolPointer {
	t.Helper()
	tools := make([]checkpoint.ToolPointer, 0, len(previews))
	for i, s := range previews {
		require.LessOrEqual(t, len(s), rehydrateCostPreviewMax, "fixture: %q", s)
		tools = append(tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_cost_%03d", i+1)),
			Hash:      core.Hash(sha256.Sum256([]byte(s))), Summary: s,
		})
	}
	return tools
}

// costBuild builds the rehydration of root's checkpoint, with the cost fixture's file pointers and
// structured summaries beside the free-text previews, through the real adapter and the real host
// rules, and returns its result, how many host judgements (Refuses calls) the build made, and the
// paths it judged.
func costBuild(t *testing.T, root string, previews []string) (rehydrate.Result, int, []string) {
	t.Helper()
	var reads []string
	files := make([]checkpoint.FilePointer, 0, rehydrateCostFiles)
	for i := 1; i <= rehydrateCostFiles; i++ {
		rel := fmt.Sprintf("pkg/f%d.go", i)
		writeProjectFile(t, root, rel)
		files = append(files, checkpoint.FilePointer{Path: rel, Hash: core.Hash(sha256.Sum256([]byte(rel))), Why: "referenced"})
	}
	for i := 1; i <= rehydrateCostReads; i++ {
		rel := fmt.Sprintf("pkg/r%d.go", i)
		writeProjectFile(t, root, rel)
		reads = append(reads, storePreview(t, map[string]string{"file_path": filepath.Join(root, filepath.FromSlash(rel))}))
	}
	req := toolPointerRequest(root, costPointers(t, append(reads, previews...)))
	req.Checkpoint.Pointers.Files = files

	judgements := 0
	var judged []string
	hp := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())
	deps := rehydrate.Deps{HostPaths: func() rehydrate.HostRules {
		rules := hp()
		require.NotNil(t, rules.Refuses, "fixture: the host's rules are established")
		refuses := rules.Refuses
		rules.Refuses = func(p string) bool {
			judgements++
			judged = append(judged, p)
			return refuses(p)
		}
		return rules
	}}
	start := time.Now()
	res, err := rehydrate.Build(context.Background(), req, deps)
	require.NoError(t, err)
	t.Logf("%d free-text summaries, %d structured, %d file pointers: %d host judgements in %v (logged, not judged)",
		len(previews), len(reads), len(files), judgements, time.Since(start))
	for _, s := range reads {
		require.Contains(t, res.Text, " — "+s+"\n", "fixture: an in-project Read preview is shown")
	}
	return res, judgements, judged
}

// maxCostJudgements bounds a cost build's host judgements (D61(4)): one for each file pointer and
// one for each structured summary, every path judged once per build, and none for free text.
const maxCostJudgements = rehydrateCostFiles + rehydrateCostReads

// TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers is the cost row,
// re-derived for coordinator decision D61(4) through the real adapter and the real host rules.
// Round 1 sent every run of words of every summary to the host's judgement (80 Bash previews took
// 16 s on the w19 verifier's idle machine), and round 2 every word, prefix, suffix and join (4w-2 per
// summary) through an evaluator that still judged a canonical-JSON or URL piece on disk. Free text
// now costs no host judgement at all, in any shape: Bash previews of seventeen words, canonical JSON
// and URLs (the round-2 cost review's first gap), commands spelling the project root absolutely
// (its second), and path-named JSON arrays of several values (the w19c round-2 review's). A
// summary that starts at the root and goes on below it with a space may be a Read of a path with a
// space in it, so it is a structured summary and costs one judgement: of its path only, the stretch
// from the root through its last word that holds a separator, never its arguments (the round-2
// review's commands run from the root, whose `HEAD~N` the host refuses on Windows as an 8.3 name).
// A build judges exactly its file pointers and its structured summaries, once each. The pass
// criterion is the count and the paths judged; the wall time is logged, never judged.
func TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers(t *testing.T) {
	root := costProject(t)
	slash := strings.ReplaceAll(root, `\`, "/")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	for _, tc := range []struct {
		name    string
		preview func(i int) string
		// rooted is how many of the previews are structured summaries that start at the root.
		rooted int
	}{
		{"Bash", func(i int) string {
			s := rehydrateCostPreview(i)
			require.Len(t, strings.Fields(s), rehydrateCostWords, "fixture: %q", s)
			return s
		}, 0},
		{"canonical JSON and URLs", func(i int) string {
			switch i % 4 {
			case 0:
				return storePreview(t, map[string]string{
					"description": fmt.Sprintf("run tests %d", i),
					"prompt":      fmt.Sprintf("go test ./mod%d/... then report failures in file%d.go", i, i),
				})
			case 1:
				return storePreview(t, map[string]string{"query": fmt.Sprintf("path:src/mod%d/file%d.go TODO retry", i, i)})
			case 2:
				return bash(fmt.Sprintf("curl -s https://example.com/api/v%d/items?page=%d | jq .items > out%d.json", i, i, i))
			}
			return storePreview(t, map[string]string{"url": fmt.Sprintf("https://example.com/docs/v%d/guide.html", i)})
		}, 0},
		{"absolute paths", func(i int) string {
			switch i % 3 {
			case 0:
				return bash(fmt.Sprintf("cd %s && go test ./mod%d/... -run TestFile%d", slash, i, i))
			case 1:
				return bash(fmt.Sprintf("git -C %s log --oneline -n %d", root, i))
			}
			return bash(fmt.Sprintf("diff %s src/b%d.go", filepath.Join(root, "src", fmt.Sprintf("a%d.go", i)), i))
		}, 0},
		{"path-named arrays", func(i int) string {
			var values []string
			for _, c := range "abcdef" {
				values = append(values, fmt.Sprintf("s/%c%d.go", c, i))
			}
			return storePreviewOf(t, map[string]any{"paths": values})
		}, 0},
		{"commands run from the root", func(i int) string {
			return bash(fmt.Sprintf("%s --since HEAD~%d && echo ok", filepath.Join(root, "tools", fmt.Sprintf("lint%d.ps1", i)), i))
		}, rehydrateCostPointers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previews := make([]string, 0, rehydrateCostPointers)
			for i := 1; i <= rehydrateCostPointers; i++ {
				previews = append(previews, tc.preview(i))
			}
			res, judgements, judged := costBuild(t, root, previews)
			require.Equal(t, maxCostJudgements+tc.rooted, judgements,
				"a build judges each file pointer and structured summary once, and no free text")
			for _, p := range judged {
				require.NotContains(t, p, " ", "a command's arguments never reach the host")
			}
			require.NotContains(t, res.Text, "summary withheld", "no fixture preview names a denied path")
			require.Contains(t, res.Text, " — "+previews[0]+"\n", "fixture: the previews reach section 6")
		})
	}
}

// TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules is D61's usefulness row through
// the real host rules: UAT-12's three deny rules in a project whose path has a space in it. The
// summaries a session leaves every day are shown, the round-2 verifier's fourth finding among them:
// on Windows a git revision word (`HEAD~1`) is 8.3-shaped, and judged as a path under any Read rule
// it was refused (hostperm's fail-closed unresolved short name), so every such command was withheld;
// and `cd <root> && …` and `git -C <root> …` were read as siblings of the root. The summaries that
// name a denied file are withheld.
func TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules(t *testing.T) {
	root := shortProjectDir(t, "John Smith", "proj")
	writeProjectSettings(t, root,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./.env)","Read(./secrets/**)"]}}`)
	for _, f := range []string{"private/deny.txt", ".env", "secrets/token.txt", "src/main.go"} {
		writeProjectFile(t, root, f)
	}
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	res := requireToolSummaries(t, root,
		[]string{
			bash("cd " + root + " && go test ./..."),
			bash("git diff HEAD~1"),
			bash("git log --oneline HEAD~3..HEAD"),
			bash("git -C " + root + " status"),
			bash("npm test"),
			bash("go test -run TestX ./internal/..."),
			storePreview(t, map[string]string{"query": "path:src/main.go retry"}),
			storePreview(t, map[string]string{"description": "run the tests", "prompt": "go test ./... and report the failures"}),
			bash("grep -rn TODO src/"),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "src", "main.go")}),
		},
		[]string{
			bash("cat .env"),
			bash("cat secrets/token.txt"),
			storePreview(t, map[string]string{"query": "path:private/deny.txt"}),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "private", "deny.txt")}),
		})
	require.NotContains(t, res.Text, "deny.txt")
	require.NotContains(t, res.Text, "token.txt")
}

// shortProjectDir is a fresh project directory short enough that the previews a row builds of
// absolute paths under it fit the store's preview width uncut, as a project's often do. t.TempDir
// spells the test's name into the path, which alone can exceed it.
func shortProjectDir(t *testing.T, elem ...string) string {
	t.Helper()
	base, err := os.MkdirTemp("", "q")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(paths.Long(base)) })
	return filepath.Join(append([]string{base}, elem...)...)
}

// storePreview is the summary the checkpointer records for a call with these arguments: the store's
// own preview (store.ArgsDigest), which the row requires to be uncut.
func storePreview(t *testing.T, args map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	_, preview := store.ArgsDigest(raw)
	require.NotContains(t, preview, "…", "fixture: the store cut the preview of %v", args)
	return preview
}

// TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld is the w19 verifier's V2 through the
// real host rules and the store's own previews: an exact Read deny rule on each of five files whose
// names hold a space and a parenthesis, a parenthesis, an apostrophe, a comma or an equals sign. A
// Read's preview is the path alone and Grep's is the path then the pattern; both are withheld,
// relative and absolute, while the same calls naming an allowed file are shown.
func TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld(t *testing.T) {
	root := shortProjectDir(t)
	denied := []string{
		"private/deny (1).txt", "private/deny(2).txt", "private/John's notes.txt", "private/a,b.txt", "private/k=v.txt",
	}
	allowed := []string{"docs/draft (1).md", "docs/draft(2).md", "docs/John's notes.md", "docs/a,b.md", "docs/k=v.md"}
	var rules []string
	for _, f := range denied {
		rules = append(rules, `"Read(./`+f+`)"`)
		writeProjectFile(t, root, f)
	}
	for _, f := range allowed {
		writeProjectFile(t, root, f)
	}
	writeProjectSettings(t, root, `{"permissions":{"deny":[`+strings.Join(rules, ",")+`]}}`)

	var tools []checkpoint.ToolPointer
	add := func(prefix string, i int, s string) {
		tools = append(tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("%s_%02d", prefix, i)), Hash: core.Hash(sha256.Sum256([]byte(prefix + s))),
			Summary: s,
		})
	}
	previews := func(rel string) []string {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		return []string{
			storePreview(t, map[string]string{"file_path": rel}),
			storePreview(t, map[string]string{"file_path": abs}),
			storePreview(t, map[string]string{"path": rel, "pattern": "apikey"}),
			storePreview(t, map[string]string{"path": abs, "pattern": "apikey"}),
		}
	}
	for i, f := range denied {
		for j, s := range previews(f) {
			add("toolu_denied", i*10+j, s)
		}
	}
	for i, f := range allowed {
		for j, s := range previews(f) {
			add("toolu_ok", i*10+j, s)
		}
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	require.NoError(t, err)
	for _, leak := range []string{"deny (1)", "deny(2)", "John's notes.txt", "a,b.txt", "k=v.txt"} {
		require.NotContains(t, res.Text, leak, "the payload shows a denied path")
	}
	for _, tp := range tools {
		if strings.HasPrefix(string(tp.ToolUseID), "toolu_denied") {
			require.Regexp(t, "- tool_use "+string(tp.ToolUseID)+" sha256:[0-9a-f]+ — summary withheld", res.Text)
			continue
		}
		require.NotRegexp(t, "- tool_use "+string(tp.ToolUseID)+" sha256:[0-9a-f]+ — summary withheld", res.Text,
			"%q names an allowed file", tp.Summary)
	}
}

// TestRehydrateHostPaths_AProjectPathWithASpaceShowsItsOwnPaths is D60's ruling on round 1's open
// issue, through the real host rules: in a project whose own path has a space in it, its absolute
// paths are shown, and a denied one or one outside the project is still withheld.
func TestRehydrateHostPaths_AProjectPathWithASpaceShowsItsOwnPaths(t *testing.T) {
	base := shortProjectDir(t)
	root := filepath.Join(base, "John Smith", "proj")
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/deny.txt)"]}}`)
	writeProjectFile(t, root, "private/deny.txt")
	writeProjectFile(t, root, "src/main.go")
	src := filepath.Join(root, "src", "main.go")
	deny := filepath.Join(root, "private", "deny.txt")
	tools := []checkpoint.ToolPointer{
		{
			ToolUseID: "toolu_ok_read", Hash: core.Hash(sha256.Sum256([]byte("r"))),
			Summary: storePreview(t, map[string]string{"file_path": src}),
		},
		{
			ToolUseID: "toolu_ok_grep", Hash: core.Hash(sha256.Sum256([]byte("g"))),
			Summary: storePreview(t, map[string]string{"path": src, "pattern": "TODO"}),
		},
		{
			ToolUseID: "toolu_no_deny", Hash: core.Hash(sha256.Sum256([]byte("d"))),
			Summary: storePreview(t, map[string]string{"file_path": deny}),
		},
		{
			ToolUseID: "toolu_no_glob", Hash: core.Hash(sha256.Sum256([]byte("j"))),
			Summary: storePreview(t, map[string]string{"path": filepath.Join(root, "private"), "pattern": "deny.txt"}),
		},
		{
			ToolUseID: "toolu_no_sibling", Hash: core.Hash(sha256.Sum256([]byte("s"))),
			Summary: storePreview(t, map[string]string{"file_path": filepath.Join(base, "John Smith", "other", "x.txt")}),
		},
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	require.NoError(t, err)
	for _, id := range []string{"toolu_ok_read", "toolu_ok_grep"} {
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — [^s]", res.Text, "the project's own path is shown")
		require.NotRegexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — summary withheld", res.Text)
	}
	for _, id := range []string{"toolu_no_deny", "toolu_no_glob", "toolu_no_sibling"} {
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — summary withheld", res.Text)
	}
	require.NotContains(t, res.Text, "deny.txt")
}

// requireToolSummaries builds root's rehydration through the real adapter, with one tool pointer
// for each preview in shown and withheld, and requires section 6 to show each of shown as recorded
// and to withhold each of withheld.
func requireToolSummaries(t *testing.T, root string, shown, withheld []string) rehydrate.Result {
	t.Helper()
	var tools []checkpoint.ToolPointer
	for i, s := range shown {
		tools = append(tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_ok_%02d", i)), Hash: core.Hash(sha256.Sum256([]byte("ok" + s))), Summary: s,
		})
	}
	for i, s := range withheld {
		tools = append(tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_no_%02d", i)), Hash: core.Hash(sha256.Sum256([]byte("no" + s))), Summary: s,
		})
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}
	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	require.NoError(t, err)
	for _, tp := range tools {
		line := "- tool_use " + string(tp.ToolUseID) + " " + tp.Hash.String() + " — "
		if strings.HasPrefix(string(tp.ToolUseID), "toolu_no_") {
			require.Contains(t, res.Text, line+"summary withheld", "%q names a withheld path", tp.Summary)
			continue
		}
		require.Contains(t, res.Text, line+tp.Summary+"\n", "%q names no withheld path, so it is shown", tp.Summary)
	}
	return res
}

// TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown is the w19 round-2 review's first
// finding through the real host rules and the store's own previews, in a plain project path and in
// one with a space in it: Grep and Glob with the project root as their path, `cd <root> && …` and
// `git -C <root> …` were withheld, because a stretch read from the root on into the next word is a
// sibling of the root. They are shown; a denied file, a quoted sibling and a sibling of one word
// are still withheld.
func TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := shortProjectDir(t, elem...)
			writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/deny.txt)"]}}`)
			writeProjectFile(t, root, "private/deny.txt")
			writeProjectFile(t, root, "src/main.go")
			bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
			shown := []string{
				storePreview(t, map[string]string{"path": root, "pattern": "TODO"}),
				storePreview(t, map[string]string{"path": root, "pattern": "**/*.go"}),
				bash("cd " + root + " && go test ./..."),
				bash("git -C " + root + " status --short"),
			}
			withheld := []string{
				bash("cd " + root + " && cat private/deny.txt"),
				storePreview(t, map[string]string{"file_path": filepath.Join(root+"2", "x.txt")}),
				bash(`cat "` + filepath.Join(root+" old", "x.txt") + `"`),
			}
			res := requireToolSummaries(t, root, shown, withheld)
			require.NotContains(t, res.Text, "deny.txt")
		})
	}
}

// TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld is the w19 round-2 review's second
// finding through the real host rules: exact Read deny rules on a file with an apostrophe and a
// space in its name and on one with a space, named the way PowerShell and a POSIX shell escape
// them. Each is withheld, and the same commands naming allowed files are shown.
func TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld(t *testing.T) {
	root := shortProjectDir(t, "proj")
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/John's notes.txt)","Read(./private/my secret.txt)"]}}`)
	for _, f := range []string{"private/John's notes.txt", "private/my secret.txt", "docs/John's notes.md", "docs/my secret.md"} {
		writeProjectFile(t, root, f)
	}
	commands := func(dir, ext string) []string {
		var out []string
		for _, cmd := range []string{
			`Get-Content '` + dir + `/John''s notes` + ext + `'`,
			`Get-Content ` + dir + "/my` secret" + ext,
			`cat ` + dir + `/John\'s\ notes` + ext,
			`cat ` + dir + `/my\ secret` + ext,
		} {
			out = append(out, storePreview(t, map[string]string{"command": cmd}))
		}
		return out
	}
	res := requireToolSummaries(t, root, commands("docs", ".md"), commands("private", ".txt"))
	require.NotContains(t, res.Text, "notes.txt")
	require.NotContains(t, res.Text, "secret.txt")
}

// storePreviewOf is storePreview for arguments of any JSON shape (an array value among them).
func storePreviewOf(t *testing.T, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	_, preview := store.ArgsDigest(raw)
	require.NotContains(t, preview, "…", "fixture: the store cut the preview of %v", args)
	return preview
}

// uat12Project is a project under shortProjectDir(elem...) with UAT-12's three Read deny rules, their
// files and src/main.go.
func uat12Project(t *testing.T, elem ...string) string {
	t.Helper()
	root := shortProjectDir(t, elem...)
	writeProjectSettings(t, root,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./.env)","Read(./secrets/**)"]}}`)
	for _, f := range []string{"private/deny.txt", ".env", "secrets/token.txt", "src/main.go"} {
		writeProjectFile(t, root, f)
	}
	return root
}

// TestRehydrateHostPaths_APathArgumentHoldingMoreThanAPathIsWithheld is the w19c round-1 review's
// path-named-argument finding through the real host rules: a value of a path-named JSON argument
// that holds more than one path (a list, a line locator, a glob) went to the host as one whole path,
// which refuses nothing, and section 6 showed the denied name in it. On Linux and macOS hostperm
// cuts no `:10` stream suffix, so `{"file":"private/deny.txt:10"}` was shown there too. Each is now
// screened as free text as well; the same shapes naming allowed files are shown.
func TestRehydrateHostPaths_APathArgumentHoldingMoreThanAPathIsWithheld(t *testing.T) {
	root := uat12Project(t, "proj")
	res := requireToolSummaries(t, root,
		[]string{
			storePreviewOf(t, map[string]any{"files": "src/main.go src/util.go"}),
			storePreviewOf(t, map[string]any{"paths": []string{"src/*.go"}}),
			storePreviewOf(t, map[string]any{"relative_path": "src/main.go#L4"}),
			storePreviewOf(t, map[string]any{"file": "src/main.go:10"}),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "src", "main.go")}),
		},
		[]string{
			storePreviewOf(t, map[string]any{"files": "src/main.go private/deny.txt"}),
			storePreviewOf(t, map[string]any{"paths": []string{"**/deny.txt"}}),
			storePreviewOf(t, map[string]any{"relative_path": "private/deny.txt#L4"}),
			storePreviewOf(t, map[string]any{"file": "private/deny.txt:10"}),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "private", "deny.txt") + "#L4"}),
		})
	require.NotContains(t, res.Text, "deny.txt")
}

// TestRehydrateHostPaths_ARootedPathWithASpaceIsJudgedByTheHost is the review's link finding through
// the real host rules: with a Read deny rule on private/**, a directory link `my docs` into private/
// made the Read preview `<root>/my docs/x2.txt` several words, screened as free text alone, and the
// host, which resolves links, never judged it. It is judged whole now, as the one-word `mydocs` link
// always was; a real directory with a space is shown.
func TestRehydrateHostPaths_ARootedPathWithASpaceIsJudgedByTheHost(t *testing.T) {
	root := shortProjectDir(t, "proj")
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/**)"]}}`)
	writeProjectFile(t, root, "private/x1.txt")
	writeProjectFile(t, root, "private/x2.txt")
	writeProjectFile(t, root, "my notes/x.txt")
	require.NoError(t, makeDirLink(filepath.Join(root, "mydocs"), filepath.Join(root, "private")))
	require.NoError(t, makeDirLink(filepath.Join(root, "my docs"), filepath.Join(root, "private")))
	res := requireToolSummaries(t, root,
		[]string{storePreview(t, map[string]string{"file_path": filepath.Join(root, "my notes", "x.txt")})},
		[]string{
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "mydocs", "x1.txt")}),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "my docs", "x2.txt")}),
		})
	require.NotContains(t, res.Text, "x2.txt")
}

// TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules extends D61's usefulness row with the
// review's everyday idioms, through the real host rules in a project whose path has a space in it: a
// search for a comment marker, a redirect to /dev/null and a Docker bind mount of the project are
// shown. The review's leaks are withheld: an absolute path glued to a flag, PowerShell's environment
// variables, and a home directory's variable ending a word.
func TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules(t *testing.T) {
	root := uat12Project(t, "John Smith", "proj")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	res := requireToolSummaries(t, root,
		[]string{
			bash(`grep -rn "// TODO" internal/`),
			bash(`rg -n "//nolint" internal/`),
			bash("ls -la src/ 2>/dev/null || true"),
			bash("go build ./... >/dev/null && echo ok"),
			bash(`docker run -v "` + root + `:/src" -w /src img go test ./...`),
		},
		[]string{
			bash("git -C/home/u/other status"),
			bash(`Get-Content $env:USERPROFILE\.aws\credentials`),
			bash("cd $HOME && cat .ssh/id_rsa"),
			bash(`7z x -oD:\stash a.zip`),
		})
	for _, leak := range []string{"/home/u", "credentials", "id_rsa", "stash"} {
		require.NotContains(t, res.Text, leak)
	}
}

// TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules extends D61's
// usefulness row with the w19c round-2 review's findings, through the real host rules in a project
// whose path has a space in it. A command run from the project root, and a Grep preview of a
// directory below the root then a revision word, went to the host whole, which on Windows refuses
// `HEAD~1` as an 8.3 name it cannot resolve, and the refused summary was noted as a withheld path
// whose fragments withheld `git diff src HEAD~1` beside it; a Grep pattern led by a backslash was
// read as a rooted path outside the project, withheld, and noted as a withheld name (`b`) that
// withheld `go build ./...`. Each is shown; a command run from a denied directory is withheld.
func TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules(t *testing.T) {
	root := uat12Project(t, "John Smith", "proj")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	res := requireToolSummaries(t, root,
		[]string{
			bash(filepath.Join(root, "tools", "lint.ps1") + " --since HEAD~1"),
			storePreview(t, map[string]string{"path": filepath.Join(root, "src"), "pattern": "HEAD~1"}),
			bash("git diff src HEAD~1"),
			bash("pwsh tools/lint.ps1 --since HEAD~2 -Fix"),
			bash(filepath.Join(root, "scripts", "run.sh") + " --fast -n 3 && echo ok"),
			storePreview(t, map[string]string{"pattern": `\bConfigLoader\b`}),
			storePreview(t, map[string]string{"pattern": `^\s*func\b`}),
			storePreview(t, map[string]string{"path": filepath.Join(root, "internal"), "pattern": `\bretryBackoff\b`}),
			bash("go build ./..."),
			bash("git status"),
		},
		[]string{
			bash(filepath.Join(root, "secrets", "rotate.sh") + " --since HEAD~1"),
			bash("cat .env"),
		})
	require.NotContains(t, res.Text, "rotate.sh")
}

// TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote is the w19c round-2 review's
// apostrophe finding through the real host rules and the store's own previews: in a project under
// `o'brien`, the root's own apostrophe was read as an open quote, so Grep and Glob with the root as
// their path, `cd <root> && …` and `git -C <root> …` were withheld as siblings of the root. They are
// shown; a denied file and a quoted sibling are still withheld.
func TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote(t *testing.T) {
	root := uat12Project(t, "o'brien", "proj")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	res := requireToolSummaries(t, root,
		[]string{
			storePreview(t, map[string]string{"path": root, "pattern": "TODO"}),
			storePreview(t, map[string]string{"path": root, "pattern": "**/*.go"}),
			bash("cd " + root + " && go test ./..."),
			bash("git -C " + root + " status --short"),
		},
		[]string{
			bash("cd " + root + " && cat private/deny.txt"),
			bash(`cat "` + filepath.Join(root+" old", "x.txt") + `"`),
		})
	require.NotContains(t, res.Text, "deny.txt")
}

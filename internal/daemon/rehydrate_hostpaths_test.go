package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hostperm"
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

	refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())()
	require.NotNil(t, refuses)
	require.True(t, refuses("private/deny.txt"), "a deny rule refuses the relative spelling")
	require.True(t, refuses(filepath.Join(root, "private", "deny.txt")), "and the absolute one")
	require.True(t, refuses("ask/q.txt"), "an ask rule refuses too: an archived rehydration cannot ask")
	require.False(t, refuses("reports.py"))
}

func TestRehydrateHostPaths_UnreadableRulesFailClosed(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":`)

	require.Nil(t, rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())(),
		"rules that cannot be established withhold every path, as re_read does")
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

// The cost row's fixture (the w19 verifier's V1 probe): N Bash previews of rehydrateCostWords words,
// each distinct, each naming two real project files, in a project with three Read deny rules.
const (
	rehydrateCostPointers = 80
	rehydrateCostWords    = 17
	// rehydrateCostPreviewMax is the width of the store's preview (store.argsPreviewMax), which
	// every fixture preview stays within, as a real one does.
	rehydrateCostPreviewMax = 120
	// maxJudgementsPerSummary bounds the host judgements one fixture preview may cost. Section 6's
	// gate reads a summary's pieces from the shapes its producer writes (ADR 0011 §23.5): the whole
	// summary, each word, each proper prefix and suffix of its words, and each directory/pattern
	// join at a space. A preview of w words with no quote, escape, delimiter, selector or assignment
	// has at most 1 + w + 2(w-1) + (w-1) = 4w-2 of them (its shell readings yield its own words, and
	// the search for known paths asks the host nothing), each judged at most once per build: 66 at
	// w=17. 4bad4cef also judged every run of two or more consecutive words, w(w-1)/2 = 136 more,
	// and with the joins and the words that is 169 pieces, and the verifier measured 134 judgements
	// per summary once the words the summaries share were judged once.
	maxJudgementsPerSummary = 4*rehydrateCostWords - 2
	// maxDiskJudgementsPerSummary bounds the judgements of one fixture preview that reach the disk:
	// none. The build's hostperm.Evaluator judges a piece on disk only when a walk down the
	// directories it names meets an entry that could respell it (a link or another reparse point,
	// an entry named by its 8.3 alias, a segment the Win32 layer trims to an existing name, or a
	// non-ASCII name a listed name may equal). In the fixture no entry is any of those: a piece
	// either stops naming entries at some segment (`git log …` names nothing in the project) or
	// names plain entries throughout (`src/mod1/file1.go`). 4bad4cef judged every piece on disk.
	maxDiskJudgementsPerSummary = 0
	// linkedDiskJudgementsPerSummary is the disk judgements of one preview in the linked-directory
	// variant, where both file words name entries through the link lib (to src): a piece is judged
	// on disk when its walk meets lib, and the pieces whose first segment is lib are the two file
	// words and the two suffixes of the preview's words that start at them (each prefix, join and
	// the whole preview starts at `git`). So 2 file words x {the word, the suffix from it} = 4.
	linkedDiskJudgementsPerSummary = 2 * 2
)

// rehydrateCostPreview is the fixture's i-th Bash preview.
func rehydrateCostPreview(i int) string {
	return fmt.Sprintf("git log --oneline -n %d --stat src/mod%d/file%d.go docs/guide%d.md and grep for TODO in the diff then stop",
		i, i, i, i)
}

// costProject is a project for the cost rows: short enough that a preview naming its root fits the
// store's preview width uncut (shortProjectDir), its root canonical (filepath.EvalSymlinks) so that
// the adapter judges each piece once rather than once more under a second spelling of the root,
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
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_cost_%02d", i+1)),
			Hash:      core.Hash(sha256.Sum256([]byte(s))), Summary: s,
		})
	}
	return tools
}

// costBuild builds the rehydration of tools in root through the real adapter and the real host
// rules, and returns its result, the host judgements section 6 asked for, how many of them were of
// a piece holding a non-ASCII rune, how many reached the disk, and the build's wall time.
func costBuild(t *testing.T, root string, tools []checkpoint.ToolPointer) (
	res rehydrate.Result, judgements, wide, disk int, elapsed time.Duration,
) {
	t.Helper()
	var ev *hostperm.Evaluator
	hp := rehydrateHostPathsObserved(mcpOpHostPolicy(t, root), root, logging.Nop(),
		func(e *hostperm.Evaluator) { ev = e })
	deps := rehydrate.Deps{HostPaths: func() func(string) bool {
		refuses := hp()
		require.NotNil(t, refuses, "fixture: the host's rules are established")
		return func(p string) bool {
			judgements++
			if strings.IndexFunc(p, func(r rune) bool { return r >= utf8.RuneSelf }) >= 0 {
				wide++
			}
			return refuses(p)
		}
	}}
	start := time.Now()
	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	elapsed = time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, ev, "the build judged through one evaluator")
	t.Logf("%d summaries: %d host judgements, %d on disk, %v", len(tools), judgements, ev.DiskEvaluations(), elapsed)
	return res, judgements, wide, ev.DiskEvaluations(), elapsed
}

// TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords is the w19 verifier's V1 through the
// real adapter and the real host rules. Round 1 sent every run of consecutive words of every tool
// summary to the host's judgement, and the adapter's judgement does on-disk work for each (the
// path's full and long names and a Readlink of every component), so a project with any Read deny
// or ask rule paid seconds for a few dozen Bash pointers: 80 previews took 16 s on the verifier's
// idle machine, three times compactAnswerBudget, and the session would have received the deferred
// note instead of its rehydration. The pass criterion is the number of judgements and of disk
// judgements; the wall clock is a sanity bound only.
func TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords(t *testing.T) {
	root := costProject(t)
	previews := make([]string, 0, rehydrateCostPointers)
	for i := 1; i <= rehydrateCostPointers; i++ {
		writeProjectFile(t, root, fmt.Sprintf("src/mod%d/file%d.go", i, i))
		writeProjectFile(t, root, fmt.Sprintf("docs/guide%d.md", i))
		s := rehydrateCostPreview(i)
		require.Len(t, strings.Fields(s), rehydrateCostWords, "fixture: %q", s)
		previews = append(previews, s)
	}

	res, judgements, _, disk, elapsed := costBuild(t, root, costPointers(t, previews))
	require.LessOrEqual(t, judgements, rehydrateCostPointers*maxJudgementsPerSummary,
		"section 6 judged %d pieces of %d summaries of %d words: more than %d per summary",
		judgements, rehydrateCostPointers, rehydrateCostWords, maxJudgementsPerSummary)
	require.LessOrEqual(t, disk, rehydrateCostPointers*maxDiskJudgementsPerSummary,
		"%d of the build's judgements reached the disk", disk)
	require.NotContains(t, res.Text, "summary withheld", "no fixture preview names a denied path")
	require.Contains(t, res.Text, " — git log --oneline -n ", "fixture: the previews reach section 6")
	// A sanity bound, not the pass criterion: the whole build inside the compaction's answer budget.
	require.Less(t, elapsed, compactAnswerBudget(), "the build would have been answered with the deferred note")
}

// TestRehydrateHostPaths_APieceThroughALinkIsJudgedOnDisk is the cost row's fixture with both file
// words naming entries through a directory link (lib, to src), which the w19 round-2 review found
// the row's disk bound had not been derived for. A piece whose walk meets the link is judged on
// disk, exactly the pieces whose first segment is lib: linkedDiskJudgementsPerSummary per preview,
// no more, and each still judged right (the files are allowed, so nothing is withheld).
func TestRehydrateHostPaths_APieceThroughALinkIsJudgedOnDisk(t *testing.T) {
	root := costProject(t)
	previews := make([]string, 0, rehydrateCostPointers)
	for i := 1; i <= rehydrateCostPointers; i++ {
		writeProjectFile(t, root, fmt.Sprintf("src/mod%d/file%d.go", i, i))
		writeProjectFile(t, root, fmt.Sprintf("src/guide%d.md", i))
		s := fmt.Sprintf("git log --oneline -n %d --stat lib/mod%d/file%d.go lib/guide%d.md and grep for TODO in the diff then stop",
			i, i, i, i)
		require.Len(t, strings.Fields(s), rehydrateCostWords, "fixture: %q", s)
		previews = append(previews, s)
	}
	require.NoError(t, makeDirLink(filepath.Join(root, "lib"), filepath.Join(root, "src")))
	t.Cleanup(func() { _ = os.Remove(paths.Long(filepath.Join(root, "lib"))) })

	res, judgements, _, disk, elapsed := costBuild(t, root, costPointers(t, previews))
	require.LessOrEqual(t, judgements, rehydrateCostPointers*maxJudgementsPerSummary)
	require.Equal(t, rehydrateCostPointers*linkedDiskJudgementsPerSummary, disk,
		"a piece is judged on disk exactly when its walk meets the link")
	require.NotContains(t, res.Text, "summary withheld", "no fixture preview names a denied path")
	require.Less(t, elapsed, compactAnswerBudget(), "the build would have been answered with the deferred note")
}

// TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk is the w19 round-2 review's
// finding that the round-2 evaluator kept the disk-free judgement for colon-free ASCII pieces only.
// A piece holding a colon (a stream, a drive spelled mid-command, a URL, `TODO:`, every word of a
// canonical-JSON preview) or a non-ASCII rune (an em dash, any non-English text) went to the full
// on-disk evaluation, about 2-3 judgements per word, so a few dozen MCP, Task, curl or commit
// previews cost seconds again. Each shape below, 80 distinct previews of it as the store records
// them, is judged with no disk judgement at all (maxDiskJudgementsPerSummary: the fixture names no
// link, alias or entry a trailing dot or space trims to, and no listed name is non-ASCII), and none
// is withheld: they name no denied path, and the ones naming the project root are its own paths.
// On macOS, whose filesystems also equate Unicode normalizations, a piece holding a non-ASCII rune
// is judged on disk by design, so there the disk judgements are exactly those pieces.
func TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk(t *testing.T) {
	root := costProject(t)
	slash := strings.ReplaceAll(root, `\`, "/")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	for _, tc := range []struct {
		name    string
		preview func(i int) string
	}{
		{"Task JSON", func(i int) string {
			return storePreview(t, map[string]string{
				"description": fmt.Sprintf("run tests %d", i),
				"prompt":      fmt.Sprintf("go test ./mod%d/... then report failures in file%d.go", i, i),
			})
		}},
		{"recall JSON", func(i int) string {
			return storePreview(t, map[string]string{"query": fmt.Sprintf("path:src/mod%d/file%d.go TODO retry", i, i)})
		}},
		{"URL", func(i int) string {
			return bash(fmt.Sprintf("curl -s https://example.com/api/v%d/items?page=%d | jq .items > out%d.json", i, i, i))
		}},
		{"colons", func(i int) string {
			return bash(fmt.Sprintf("git commit -m 'fix(mod%d): handle file%d.go errors: TODO: retry'", i, i))
		}},
		{"em dash", func(i int) string {
			return bash(fmt.Sprintf("git commit -m 'fix %d — never cut the root of mod%d'", i, i))
		}},
		{"Cyrillic", func(i int) string { return bash(fmt.Sprintf("echo 'Привет мир %d' > docs/guide%d.md", i, i)) }},
		{"absolute root, forward slashes", func(i int) string {
			return bash(fmt.Sprintf("cd %s && go test ./mod%d/... -run TestFile%d", slash, i, i))
		}},
		{"absolute root", func(i int) string { return bash(fmt.Sprintf("cd %s && git log --oneline -n %d", root, i)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previews := make([]string, 0, rehydrateCostPointers)
			for i := 1; i <= rehydrateCostPointers; i++ {
				previews = append(previews, tc.preview(i))
			}
			res, _, wide, disk, elapsed := costBuild(t, root, costPointers(t, previews))
			want := rehydrateCostPointers * maxDiskJudgementsPerSummary
			if runtime.GOOS == "darwin" {
				want += wide
			}
			require.Equal(t, want, disk, "%d of the build's judgements reached the disk", disk)
			require.NotContains(t, res.Text, "summary withheld", "no fixture preview names a denied path")
			require.Less(t, elapsed, compactAnswerBudget(), "the build would have been answered with the deferred note")
		})
	}
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

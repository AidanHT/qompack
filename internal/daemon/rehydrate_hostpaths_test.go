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
	// join at a space. A preview of w words with no quote, delimiter, selector or assignment has at
	// most 1 + w + 2(w-1) + (w-1) = 4w-2 of them, each judged at most once per build: 66 at w=17.
	// 4bad4cef also judged every run of two or more consecutive words, w(w-1)/2 = 136 more, and with
	// the joins and the words that is 169 pieces, and the verifier measured 134 judgements per
	// summary once the words the summaries share were judged once.
	maxJudgementsPerSummary = 4*rehydrateCostWords - 2
	// maxDiskJudgementsPerSummary bounds the judgements of one fixture preview that reach the disk.
	// The build's hostperm.Evaluator judges a piece on disk only when the walk down the directories
	// it names meets an entry that could respell it (a link, a reparse point, an 8.3 alias, or a
	// name the operating system rewrites); a piece that stops naming entries at some segment, or
	// names plain entries throughout, needs no disk. Only a preview's two file words name existing
	// entries throughout, so even where they were links a preview could not cost more than two.
	// The fixture holds no link, and the row logs the actual count, which is zero; 4bad4cef judged
	// every piece on disk.
	maxDiskJudgementsPerSummary = 2
)

// rehydrateCostPreview is the fixture's i-th Bash preview.
func rehydrateCostPreview(i int) string {
	return fmt.Sprintf("git log --oneline -n %d --stat src/mod%d/file%d.go docs/guide%d.md and grep for TODO in the diff then stop",
		i, i, i, i)
}

// TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords is the w19 verifier's V1 through the
// real adapter and the real host rules. Round 1 sent every run of consecutive words of every tool
// summary to the host's judgement, and the adapter's judgement does on-disk work for each (the
// path's full and long names and a Readlink of every component), so a project with any Read deny
// or ask rule paid seconds for a few dozen Bash pointers: 80 previews took 16 s on the verifier's
// idle machine, three times compactAnswerBudget, and the session would have received the deferred
// note instead of its rehydration. The pass criterion is the number of judgements; the wall clock
// is a sanity bound only.
func TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./.env)","Read(./secrets/**)"]}}`)
	for _, f := range []string{"private/deny.txt", ".env", "secrets/token.txt"} {
		writeProjectFile(t, root, f)
	}
	tools := make([]checkpoint.ToolPointer, 0, rehydrateCostPointers)
	for i := 1; i <= rehydrateCostPointers; i++ {
		writeProjectFile(t, root, fmt.Sprintf("src/mod%d/file%d.go", i, i))
		writeProjectFile(t, root, fmt.Sprintf("docs/guide%d.md", i))
		s := rehydrateCostPreview(i)
		require.Len(t, strings.Fields(s), rehydrateCostWords, "fixture: %q", s)
		require.LessOrEqual(t, len(s), rehydrateCostPreviewMax, "fixture: %q", s)
		tools = append(tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_cost_%02d", i)),
			Hash:      core.Hash(sha256.Sum256([]byte(s))), Summary: s,
		})
	}

	var ev *hostperm.Evaluator
	hp := rehydrateHostPathsObserved(mcpOpHostPolicy(t, root), root, logging.Nop(),
		func(e *hostperm.Evaluator) { ev = e })
	judgements := 0
	deps := rehydrate.Deps{HostPaths: func() func(string) bool {
		refuses := hp()
		require.NotNil(t, refuses, "fixture: the host's rules are established")
		return func(p string) bool { judgements++; return refuses(p) }
	}}

	start := time.Now()
	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	elapsed := time.Since(start)
	require.NoError(t, err)

	require.NotNil(t, ev, "the build judged through one evaluator")
	t.Logf("%d summaries: %d host judgements, %d on disk, %v", rehydrateCostPointers, judgements,
		ev.DiskEvaluations(), elapsed)
	require.LessOrEqual(t, judgements, rehydrateCostPointers*maxJudgementsPerSummary,
		"section 6 judged %d pieces of %d summaries of %d words: more than %d per summary",
		judgements, rehydrateCostPointers, rehydrateCostWords, maxJudgementsPerSummary)
	require.LessOrEqual(t, ev.DiskEvaluations(), rehydrateCostPointers*maxDiskJudgementsPerSummary,
		"%d of the build's judgements reached the disk", ev.DiskEvaluations())
	require.NotContains(t, res.Text, "summary withheld", "no fixture preview names a denied path")
	require.Contains(t, res.Text, " — git log --oneline -n ", "fixture: the previews reach section 6")
	// A sanity bound, not the pass criterion: the whole build inside the compaction's answer budget.
	require.Less(t, elapsed, compactAnswerBudget(), "the build would have been answered with the deferred note")
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

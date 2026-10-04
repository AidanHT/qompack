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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
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
// (coordinator decisions D61 and D63, ADR 0011 §23 item 7): every Read deny and ask rule's
// specifier, as the settings spell it, from the same snapshot Refuses judges against; none when no
// rule is in force, and none, with no Refuses, when the rules cannot be established.
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
		core.Hash(sha256.Sum256([]byte("selector"))).String()+" — (summary withheld)\n")
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
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — \\(summary withheld\\)\n", res.Text)
	}
	require.Contains(t, res.Text, "- pointer_untracked "+denied.String()+" — not tracked by git")
	require.Contains(t, res.Text, `{"query":"ORCHID-DENY-8842"}`, "a summary that names no path is shown")
}

// writeProjectText creates rel (and its directories) under root with body.
func writeProjectText(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
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

// The cost rows' fixture (the w19 verifier's V1 probe, re-derived for coordinator decisions D61 and
// D63): a project with the verifier's three Read deny rules and rehydrateCostPointers free-text
// summaries, beside rehydrateCostFiles file pointers and rehydrateCostReads structured summaries
// (the store's preview of a Read: its file_path alone).
const (
	rehydrateCostPointers = 80
	rehydrateCostWords    = 17
	// rehydrateCostFiles and rehydrateCostReads are the file pointers and the structured summaries
	// each cost build carries: enough that the bound below is not met by a build that judges nothing.
	rehydrateCostFiles = 10
	rehydrateCostReads = 10
	// rehydrateCostInstructions is how many `paths:` rule files and skills the instruction variant's
	// project holds: enough that a build judging none of them, or each twice, fails the bound.
	rehydrateCostInstructions = 5
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
// paths it judged. With instructions > 0 the project also holds that many `paths:` rule files
// scoped to the file pointers and that many skills, which items 6a and 6b restore through the real
// rule scanner and skill indexer.
func costBuild(t *testing.T, root string, previews []string, instructions, drops int) (rehydrate.Result, int, []string) {
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
	for i := 0; i < drops; i++ {
		// The checkpointer's budget cut names each file pointer it cuts (truncate.go cutFilePointers),
		// so a long session's checkpoint carries a drop like this for every file it touched.
		req.Checkpoint.Dropped = append(req.Checkpoint.Dropped, checkpoint.DropEntry{
			Kind: "file_pointer", ID: fmt.Sprintf("vendor/m%d/z%d.go", i%50, i),
			Detail: "truncated at budget; re_read(path) still resolves",
		})
	}

	judgements := 0
	var judged []string
	hp := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())
	var deps rehydrate.Deps
	if instructions > 0 {
		for i := 1; i <= instructions; i++ {
			writeProjectText(t, root, fmt.Sprintf(".claude/rules/k%d.md", i),
				fmt.Sprintf("---\npaths:\n  - \"pkg/**\"\n---\nRule %d body.\n", i))
			writeProjectText(t, root, fmt.Sprintf(".claude/skills/s%d/SKILL.md", i),
				fmt.Sprintf("---\nname: s%d\ndescription: skill %d\n---\nSkill %d body.\n", i, i, i))
		}
		deps.Rules, deps.Skills = rules.New(), skills.New()
	}
	deps.HostPaths = func() rehydrate.HostRules {
		rules := hp()
		require.NotNil(t, rules.Refuses, "fixture: the host's rules are established")
		refuses := rules.Refuses
		rules.Refuses = func(p string) bool {
			judgements++
			judged = append(judged, p)
			return refuses(p)
		}
		return rules
	}
	start := time.Now()
	res, err := rehydrate.Build(context.Background(), req, deps)
	require.NoError(t, err)
	t.Logf("%d previews, %d Read previews, %d file pointers, %d path-keyed drops: %d host judgements in %v (logged, not judged)",
		len(previews), len(reads), len(files), drops, judgements, time.Since(start))
	for _, s := range reads {
		require.Contains(t, res.Text, " — "+s+"\n", "fixture: an in-project Read preview is shown")
	}
	return res, judgements, judged
}

// maxCostJudgements bounds a cost build's host judgements (D63, ADR 0011 §23 item 10): one for each
// file pointer and one for each structured summary, every path judged once per build, and none for
// free text.
const maxCostJudgements = rehydrateCostFiles + rehydrateCostReads

// rehydrateCostDrops is how many path-keyed checkpoint drops the drops variant carries (audit 2's
// finding 28: on eca33155 the build judged every one, about 2.5 s through this adapter on Windows),
// and costDropJudgements how many of them a build judges at most (ADR 0011 §23 item 10).
const (
	rehydrateCostDrops = 1000
	costDropJudgements = 64
)

// TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers is the cost row,
// re-derived for coordinator decisions D61(4) and D63 through the real adapter and the real host
// rules. Round 1 sent every run of words of every summary to the host's judgement (80 Bash previews took
// 16 s on the w19 verifier's idle machine), and round 2 every word, prefix, suffix and join (4w-2 per
// summary) through an evaluator that still judged a canonical-JSON or URL piece on disk. Free text
// now costs no host judgement at all, in any shape: Bash previews of seventeen words, canonical JSON
// and URLs (the round-2 cost review's first gap), commands spelling the project root absolutely
// (its second), path-named JSON arrays of several values (the w19c round-2 review's), and such
// arrays cut by the store inside a value (the D63 review's: a cut value among several is judged by
// containment and the screen, never by the host). A summary that starts at the root and goes on
// below it with a space may be a Read of a path with a space in it, so it is a structured summary
// and costs one judgement: of its path part, the stretch
// from the root through its last word that holds a separator, which in this fixture's commands is
// the script's path alone (the round-2 review's commands run from the root, whose `HEAD~N` the host
// refuses on Windows as an 8.3 name; a later argument holding a separator would reach the host too).
// The rule files and skills items 6a and 6b restore cost one judgement each (the round-3 review). A
// build judges exactly its file pointers, its structured summaries and those files, once each. The
// pass criterion is the count and the paths judged; the wall time is logged, never judged.
func TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers(t *testing.T) {
	root := costProject(t)
	slash := strings.ReplaceAll(root, `\`, "/")
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	for _, tc := range []struct {
		name    string
		preview func(i int) string
		// rooted is how many of the previews are structured summaries that start at the root.
		rooted int
		// instructions is how many `paths:` rule files and skills the project holds (costBuild): each
		// rule file item 6a would restore and each skill file item 6b would index is judged once.
		instructions int
		// drops is how many path-keyed checkpoint drops the checkpoint carries (costBuild): at most
		// costDropJudgements of them are judged, whatever their number (audit 2's finding 28).
		drops int
	}{
		{"Bash", func(i int) string {
			s := rehydrateCostPreview(i)
			require.Len(t, strings.Fields(s), rehydrateCostWords, "fixture: %q", s)
			return s
		}, 0, 0, 0},
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
		}, 0, 0, 0},
		{"absolute paths", func(i int) string {
			switch i % 3 {
			case 0:
				return bash(fmt.Sprintf("cd %s && go test ./mod%d/... -run TestFile%d", slash, i, i))
			case 1:
				return bash(fmt.Sprintf("git -C %s log --oneline -n %d", root, i))
			}
			return bash(fmt.Sprintf("diff %s src/b%d.go", filepath.Join(root, "src", fmt.Sprintf("a%d.go", i)), i))
		}, 0, 0, 0},
		{"path-named arrays", func(i int) string {
			var values []string
			for _, c := range "abcdef" {
				values = append(values, fmt.Sprintf("s/%c%d.go", c, i))
			}
			return storePreviewOf(t, map[string]any{"paths": values})
		}, 0, 0, 0},
		{"cut path-named arrays", func(i int) string {
			var values []string
			for _, c := range "abcdefghijkl" {
				values = append(values, fmt.Sprintf("mod%d/%c%c%c%c.go", i, c, c, c, c))
			}
			raw, err := json.Marshal(map[string]any{"paths": values})
			require.NoError(t, err)
			_, preview := store.ArgsDigest(raw)
			require.True(t, strings.HasSuffix(preview, "…"), "fixture: the store cuts %q", preview)
			return preview
		}, 0, 0, 0},
		{"commands run from the root", func(i int) string {
			return bash(fmt.Sprintf("%s --since HEAD~%d && echo ok", filepath.Join(root, "tools", fmt.Sprintf("lint%d.ps1", i)), i))
		}, rehydrateCostPointers, 0, 0},
		{"instruction and skill files", rehydrateCostPreview, 0, rehydrateCostInstructions, 0},
		{"path-keyed checkpoint drops", rehydrateCostPreview, 0, 0, rehydrateCostDrops},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previews := make([]string, 0, rehydrateCostPointers)
			for i := 1; i <= rehydrateCostPointers; i++ {
				previews = append(previews, tc.preview(i))
			}
			res, judgements, judged := costBuild(t, root, previews, tc.instructions, tc.drops)
			require.Equal(t, maxCostJudgements+tc.rooted+2*tc.instructions+min(tc.drops, costDropJudgements), judgements,
				"a build judges each file pointer, structured summary, rule file and skill file once, a bounded "+
					"number of path-keyed checkpoint drops, and no free text")
			for _, p := range judged {
				require.NotContains(t, p, " ", "no fixture command's arguments reach the host")
			}
			// The count's 2*instructions term is exactly the rule files and the skill files, once each,
			// in the spelling items 6a and 6b hand the judge: a build that dropped 6b's judgements and
			// judged 6a's twice (under a second spelling, or past the memo) meets the count but not this.
			instr := map[string]int{}
			for _, p := range judged {
				if strings.Contains(filepath.ToSlash(p), ".claude/") {
					instr[filepath.ToSlash(p)]++
				}
			}
			want := map[string]int{}
			for i := 1; i <= tc.instructions; i++ {
				want[fmt.Sprintf(".claude/rules/k%d.md", i)] = 1
				want[fmt.Sprintf(".claude/skills/s%d/SKILL.md", i)] = 1
			}
			require.Equal(t, want, instr, "items 6a and 6b judge each rule file and each skill file once: %v", judged)
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
	shown, withheld := usefulSummaryPreviews(root)
	res := requireToolSummaries(t, root, storePreviews(t, shown), storePreviews(t, withheld))
	require.NotContains(t, res.Text, "deny.txt")
	require.NotContains(t, res.Text, "token.txt")
}

// usefulSummaryPreviews are TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules's calls
// in a project at root, as the arguments the store previews: those section 6 shows, and those it
// withholds. TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory builds them
// under the longest temporary directory a hosted runner spells.
func usefulSummaryPreviews(root string) (shown, withheld []map[string]any) {
	bash := func(cmd string) map[string]any { return map[string]any{"command": cmd} }
	shown = []map[string]any{
		bash("cd " + root + " && go test ./..."),
		bash("git diff HEAD~1"),
		bash("git log --oneline HEAD~3..HEAD"),
		bash("git -C " + root + " status"),
		bash("npm test"),
		bash("go test -run TestX ./internal/..."),
		bash(`git commit -m "fix the bug"`),
		{"query": "path:src/main.go"},
		{"query": "path:src/main.go retry"},
		{"url": "https://example.com/a"},
		{"url": "https://example.com/search?a=1&b=2", "prompt": "list the results"},
		{"description": "run the tests", "prompt": "go test ./... and report the failures"},
		bash("grep -rn TODO src/"),
		{"file_path": filepath.Join(root, "src", "main.go")},
		{"path": filepath.Join(root, "src"), "pattern": "**/*_test.go"},
		{"pattern": "**/*.{ts,tsx}"},
		{"pattern": "TODO|FIXME"},
		bash("go test ./... > test.log 2>&1; tail -n 50 test.log"),
		bash(`git commit -m "feat(api): add users endpoint"`),
		bash(`sed -n '1,50p' src/main.go`),
		bash("git log --format=%h -n 3"),
		{"query": "what's the owner's ruling"},
	}
	withheld = []map[string]any{
		bash("cat .env"),
		bash("cat secrets/token.txt"),
		{"query": "path:private/deny.txt"},
		{"file_path": filepath.Join(root, "private", "deny.txt")},
		{"path": filepath.Join(root, "secrets"), "pattern": "*.txt"},
		{"pattern": "**/*.{go,env}"},
		bash(`cat 'secrets/token.txt'`),
		{"paths": []string{"file:///etc/passwd", "src/main.go"}},
		// Criterion change (wave 19d final verify): `--pretty=format` before a `:` is a name PowerShell
		// accepts for a drive, so git's `format:` spelling is over-withheld; `--format=%h` is shown.
		bash("git log --pretty=format:%h -n 3"),
	}
	return shown, withheld
}

// storePreviews is storePreviewOf of each of args.
func storePreviews(t *testing.T, args []map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, storePreviewOf(t, a))
	}
	return out
}

// TestRehydrateHostPaths_AFileURLInAPathNamedValueIsOutsideTheProject is the D63 review's file-URL
// finding through the real adapter: a `file:` URL in a path-named value was read by containment as a
// project path whose first segment is `file:`, and the host, handed it as a path, refused nothing. It
// is outside the project, as free text already was, and asks the host nothing.
func TestRehydrateHostPaths_AFileURLInAPathNamedValueIsOutsideTheProject(t *testing.T) {
	root := uat12Project(t, "proj")
	res := requireToolSummaries(t, root,
		[]string{storePreviewOf(t, map[string]any{"paths": []string{"src/main.go", "docs/b.md"}})},
		[]string{
			storePreviewOf(t, map[string]any{"directory": "file:///home/u/other"}),
			storePreviewOf(t, map[string]any{"paths": []string{"file:///etc/passwd", "src/a.go"}}),
			storePreviewOf(t, map[string]any{"cell_id": "c1", "notebook_path": "file:///home/u/nb.ipynb"}),
			storePreviewOf(t, map[string]any{"args": []string{"status"}, "cwd": "file:///C:/Users/someone/secret"}),
			storePreviewOf(t, map[string]any{"file": "file://fileserver/share/payroll.xlsx"}),
		})
	for _, leak := range []string{"passwd", "payroll", "someone", "nb.ipynb", "/home/u"} {
		require.NotContains(t, res.Text, leak)
	}
}

// TestRehydrateHostPaths_AnOutsideNamesakeNeverWithholdsAProjectPath is the D63 review's namesake
// finding through the real adapter, in a project whose path has a space in it: a Read outside the
// project (a dependency's README.md, a global settings.json, ~/.kube/config) withheld every Read of
// the project's own file of the same name. A rooted Read spelled in one separator style is the exact
// path the host judges, so only the rules' literals screen it.
func TestRehydrateHostPaths_AnOutsideNamesakeNeverWithholdsAProjectPath(t *testing.T) {
	root := uat12Project(t, "John Smith", "proj")
	home := shortProjectDir(t, "home")
	read := func(p string) string { return storePreview(t, map[string]string{"file_path": p}) }
	res := requireToolSummaries(t, root,
		[]string{
			read(filepath.Join(root, "README.md")),
			read(filepath.Join(root, ".claude", "settings.json")),
			read(filepath.Join(root, "src", "lib.rs")),
			read(filepath.Join(root, "config", "database.yml")),
			read(filepath.Join(root, "go.mod")),
		},
		[]string{
			read(filepath.Join(home, "mod", "cobra@v1.8.0", "README.md")),
			read(filepath.Join(home, ".claude", "settings.json")),
			read(filepath.Join(home, ".cargo", "x", "lib.rs")),
			read(filepath.Join(home, ".kube", "config")),
			read(filepath.Join(home, "elsewhere", "go.mod")),
			read(filepath.Join(root, "private", "deny.txt")),
		})
	for _, leak := range []string{"cobra@", ".cargo", ".kube", "elsewhere", "deny.txt"} {
		require.NotContains(t, res.Text, leak)
	}
}

// shortProjectDir is a fresh project directory short enough that the previews a row builds of
// absolute paths under it fit the store's preview width uncut, as a project's often do. t.TempDir
// spells the test's name into the path, which alone can exceed it. Off Windows it is made in /tmp,
// not in TMPDIR: macOS hands a test a 48-character TMPDIR, under which a root-spelling preview the
// row requires uncut was cut (audit 2's finding 36); storePreview holds every preview to the longest
// temporary directory a hosted runner spells all the same (longestTempBase).
func shortProjectDir(t *testing.T, elem ...string) string {
	t.Helper()
	dir := ""
	if runtime.GOOS != "windows" {
		dir = "/tmp"
	}
	base, err := os.MkdirTemp(dir, "q")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(paths.Long(base)) })
	if runtime.GOOS == "windows" {
		// A hosted Windows runner spells its temporary directory with an 8.3 name
		// (C:\Users\RUNNER~1\AppData\Local\Temp), and a root holding a `~` has no root unit (D64(1)),
		// so the base is spelled by its long names, as a session's working directory is.
		long, err := filepath.EvalSymlinks(base)
		require.NoError(t, err)
		base = long
	}
	projectBases.Lock()
	projectBases.dirs = append(projectBases.dirs, base)
	projectBases.Unlock()
	return filepath.Join(append([]string{base}, elem...)...)
}

// projectBases are the directories shortProjectDir made, which storePreview and storePreviewOf
// replace with longestTempBase to hold each preview to a hosted runner's temporary directory.
var projectBases struct {
	sync.Mutex
	dirs []string
}

// storePreview is the summary the checkpointer records for a call with these arguments: the store's
// own preview (store.ArgsDigest), which the row requires to be uncut, here and under the longest
// temporary directory a hosted runner spells (requireUncutOnAHostedRunner).
func storePreview(t *testing.T, args map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	_, preview := store.ArgsDigest(raw)
	require.NotContains(t, preview, "…", "fixture: the store cut the preview of %v", args)
	vals := make(map[string]any, len(args))
	for k, v := range args {
		vals[k] = v
	}
	requireUncutOnAHostedRunner(t, vals)
	return preview
}

// requireUncutOnAHostedRunner requires the store's preview of args to be uncut with every directory
// shortProjectDir made, in either slash style, spelled as longestTempBase instead (audit 2's finding
// 36): a row's previews fit the store's width on the runner that tests it, not only on a machine with
// a short temporary directory.
func requireUncutOnAHostedRunner(t *testing.T, args map[string]any) {
	t.Helper()
	projectBases.Lock()
	dirs := append([]string(nil), projectBases.dirs...)
	projectBases.Unlock()
	long := longestTempBase()
	var swap func(v any) any
	swap = func(v any) any {
		switch x := v.(type) {
		case string:
			for _, d := range dirs {
				x = strings.ReplaceAll(x, d, long)
				x = strings.ReplaceAll(x, filepath.ToSlash(d), filepath.ToSlash(long))
			}
			return x
		case []string:
			out := make([]string, len(x))
			for i, s := range x {
				out[i], _ = swap(s).(string)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = swap(e)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = swap(e)
			}
			return out
		}
		return v
	}
	raw, err := json.Marshal(swap(args))
	require.NoError(t, err)
	_, preview := store.ArgsDigest(raw)
	require.NotContains(t, preview, "…",
		"fixture: under a hosted runner's temporary directory (%s) the store cuts the preview of %v", long, args)
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
	// The apostrophe between letters, the comma and the equals sign (allowed indices 2, 3 and 4) are
	// whitelist-safe; an unquoted parenthesis (indices 0 and 1) makes the allowed path withheld too
	// under D63.
	allowedSafe := map[int]bool{2: true, 3: true, 4: true}
	for i, f := range allowed {
		prefix := "toolu_okno"
		if allowedSafe[i] {
			prefix = "toolu_okshow"
		}
		for j, s := range previews(f) {
			add(prefix, i*10+j, s)
		}
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), toolPointerRequest(root, tools), deps)
	require.NoError(t, err)
	for _, leak := range []string{"deny (1)", "deny(2)", "John's notes.txt", "a,b.txt", "k=v.txt"} {
		require.NotContains(t, res.Text, leak, "the payload shows a denied path")
	}
	for _, tp := range tools {
		withheld := "- tool_use " + string(tp.ToolUseID) + " sha256:[0-9a-f]+ — \\(summary withheld\\)\n"
		if strings.HasPrefix(string(tp.ToolUseID), "toolu_okshow") {
			require.NotRegexp(t, withheld, res.Text, "%q names an allowed file with a safe delimiter", tp.Summary)
			continue
		}
		require.Regexp(t, withheld, res.Text, "%q is a denied path or an allowed path with an unsafe delimiter", tp.Summary)
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
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — [^s(]", res.Text, "the project's own path is shown")
		require.NotRegexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — \\(summary withheld\\)", res.Text)
	}
	for _, id := range []string{"toolu_no_deny", "toolu_no_glob", "toolu_no_sibling"} {
		require.Regexp(t, "- tool_use "+id+" sha256:[0-9a-f]+ — \\(summary withheld\\)\n", res.Text)
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
			require.Contains(t, res.Text, line+"(summary withheld)\n", "%q names a withheld path", tp.Summary)
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
				// A Glob preview of the root is one structured glob (ADR 0011 §23 item 6).
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
	// Criterion change (D63): a single quote, a doubled quote, a backtick and a backslash that ends a
	// token (an escaped space) are unsafe, so every shape is over-withheld for the allowed file too.
	res := requireToolSummaries(t, root, nil, append(commands("docs", ".md"), commands("private", ".txt")...))
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
	requireUncutOnAHostedRunner(t, args)
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
// redirect to /dev/null is shown (a whole null-device token, D63's extension). The review's leaks are
// withheld: an absolute path glued to a flag, PowerShell's environment variables, and a home
// directory's variable ending a word.
func TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules(t *testing.T) {
	root := uat12Project(t, "John Smith", "proj")
	shown, withheld := commonIdiomPreviews(root)
	res := requireToolSummaries(t, root, storePreviews(t, shown), storePreviews(t, withheld))
	for _, leak := range []string{"/home/u", "credentials", "id_rsa", "stash"} {
		require.NotContains(t, res.Text, leak)
	}
}

// commonIdiomPreviews are TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules's calls in a
// project at root, as the arguments the store previews: those section 6 shows, and those it
// withholds. TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory builds them
// under the longest temporary directory a hosted runner spells.
func commonIdiomPreviews(root string) (shown, withheld []map[string]any) {
	bash := func(cmd string) map[string]any { return map[string]any{"command": cmd} }
	shown = []map[string]any{
		bash("ls -la src/ 2>/dev/null || true"),
		bash("go build ./... >/dev/null && echo ok"),
	}
	withheld = []map[string]any{
		// Criterion change (D63): a comment marker (`//`) and a Docker bind mount (`:/src`) read as
		// absolute paths, so they are over-withheld along with the leaks. The bind mount's command is
		// short enough to stay uncut under a hosted runner's temporary directory (audit 2's finding
		// 36); `:/src` alone withholds it, as `-w /src` did.
		bash(`grep -rn "// TODO" internal/`),
		bash(`rg -n "//nolint" internal/`),
		bash(`docker run -v "` + root + `:/src" img`),
		bash("git -C/home/u/other status"),
		bash(`Get-Content $env:USERPROFILE\.aws\credentials`),
		bash("cd $HOME && cat .ssh/id_rsa"),
		bash(`7z x -oD:\stash a.zip`),
	}
	return shown, withheld
}

// TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules extends D61's
// usefulness row with the w19c round-2 review's findings, through the real host rules in a project
// whose path has a space in it. A command run from the project root, and a Grep preview of a
// directory below the root then a revision word, went to the host whole, which on Windows refuses
// `HEAD~1` as an 8.3 name it cannot resolve, and the refused summary was noted as a withheld path
// whose fragments withheld `git diff src HEAD~1` beside it; a Grep pattern led by a backslash was
// read as a rooted path outside the project, withheld, and noted as a withheld name (`b`) that
// withheld `go build ./...`. Each command is shown. Under D63 no text is read as a regular
// expression: one led by `\` or `^` is withheld, and noted as nothing, so `go build ./...` is still
// shown; a command run from a denied directory is withheld.
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
			bash("go build ./..."),
			bash("git status"),
		},
		[]string{
			bash(filepath.Join(root, "secrets", "rotate.sh") + " --since HEAD~1"),
			bash("cat .env"),
			// Criterion change (D63): a backslash-led or `^`-anchored regular expression is over-withheld (a
			// leading `\` or `^` is unsafe); a rooted command is still judged through its script path only.
			storePreview(t, map[string]string{"pattern": `\bConfigLoader\b`}),
			storePreview(t, map[string]string{"pattern": `^\s*func\b`}),
			storePreview(t, map[string]string{"path": filepath.Join(root, "internal"), "pattern": `\bretryBackoff\b`}),
		})
	require.NotContains(t, res.Text, "rotate.sh")
}

// TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit (renamed from
// TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote by D64) is coordinator decision
// D64(1) through the real host rules and the store's own previews: the root's spelling was held
// together as one unit whatever it held, so in a project under `o'brien`, `a;b`, `a,b`, `a$b` or
// `a+b` the Grep and Glob previews of the root, `cd <root> && …` and `git -C <root> …` were shown,
// though a shell splits or reinterprets the root at its own `'`, `;`, `,`, `$` or `+` (cmd.exe's copy
// starts its next source there) and reads another path. A root holding a character outside the
// unit's set has no unit, so each is withheld (criterion change for `o'brien`, whose rows were shown
// since the w19c round-2 review); a path-named JSON value is still judged as the structured value it
// is, and a root of letters, digits, `-`, `_` and `.` keeps its unit. Since the round-2 verify of
// D64 a root with an `@` has none either (PowerShell splats a word of the root that is a whole
// `@name`, so `git -C C:\q\John @Work status` hands git `C:\q\John` and the splatted array), and the
// control root `a@b-c.d`, shown until then, is withheld (criterion change).
func TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit(t *testing.T) {
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	rootSummaries := func(root string) []string {
		return []string{
			storePreview(t, map[string]string{"path": root, "pattern": "TODO"}),
			storePreview(t, map[string]string{"path": root, "pattern": "**/*.go"}),
			bash("cd " + root + " && go test ./..."),
			bash("git -C " + root + " status --short"),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "src", "main.go")}),
		}
	}
	for _, seg := range []string{"o'brien", "a;b", "a,b", "a$b", "a+b", "a@b-c.d", "John @Work"} {
		t.Run(seg, func(t *testing.T) {
			root := uat12Project(t, seg, "proj")
			res := requireToolSummaries(t, root,
				[]string{storePreviewOf(t, map[string]any{"notebook_path": filepath.Join(root, "src", "x.ipynb")})},
				append(rootSummaries(root),
					bash("cd "+root+" && cat private/deny.txt"),
					bash(`cat "`+filepath.Join(root+" old", "x.txt")+`"`),
				))
			require.NotContains(t, res.Text, "deny.txt")
		})
	}
	t.Run("a-b_c.d", func(t *testing.T) {
		root := uat12Project(t, "a-b_c.d", "proj")
		res := requireToolSummaries(t, root, rootSummaries(root),
			[]string{bash("cd " + root + " && cat private/deny.txt")})
		require.NotContains(t, res.Text, "deny.txt")
	})
}

// TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit is D64's rulings on wave 19f's open items
// through the real host rules and the store's own previews: a root holding a code point that
// Windows' ANSI best-fit conversion turns into ASCII punctuation (U+02BA into `"`, U+02BC into `'`,
// U+0303 into `~`), or a word that starts with `-` after one of its spaces (PowerShell binds
// `-Force` as a parameter, and a lone `-` too is withheld), has no root unit, so the root's Grep and
// Glob previews, `cd <root> && …`, `git -C <root> …` and a one-word Read under it are withheld,
// while a path-named JSON value is still shown. A `-` inside a word keeps the unit.
func TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit(t *testing.T) {
	bash := func(cmd string) string { return storePreview(t, map[string]string{"command": cmd}) }
	rootSummaries := func(root string) []string {
		return []string{
			storePreview(t, map[string]string{"path": root, "pattern": "TODO"}),
			storePreview(t, map[string]string{"path": root, "pattern": "**/*.go"}),
			bash("cd " + root + " && go test ./..."),
			bash("git -C " + root + " status --short"),
			storePreview(t, map[string]string{"file_path": filepath.Join(root, "src", "main.go")}),
		}
	}
	for _, seg := range []string{"a\u02BAb", "a\u02BCb", "a\u0303b", "John -Force", "OneDrive - Contoso"} {
		t.Run(seg, func(t *testing.T) {
			root := uat12Project(t, seg, "proj")
			res := requireToolSummaries(t, root,
				[]string{storePreviewOf(t, map[string]any{"notebook_path": filepath.Join(root, "src", "x.ipynb")})},
				append(rootSummaries(root), bash("cd "+root+" && cat private/deny.txt")))
			require.NotContains(t, res.Text, "deny.txt")
		})
	}
	t.Run("John Smith-Jones", func(t *testing.T) {
		root := uat12Project(t, "John Smith-Jones", "proj")
		res := requireToolSummaries(t, root, rootSummaries(root),
			[]string{bash("cd " + root + " && cat private/deny.txt")})
		require.NotContains(t, res.Text, "deny.txt")
	})
}

// TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling is D64's ruling on wave 19f's final
// verify through the real host rules and the store's own cut: a NotebookEdit's notebook_path sorts
// after its new_source, so a long cell cuts the path. A cut inside the root's own spelling is the
// project and is shown, while one inside a directory beside it whose name differs from the root's
// only by an apostrophe or a caret (`John'athan`, `John^athan` beside `Johnathan`), which the screen
// form deletes, names a path outside the project and is withheld.
func TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling(t *testing.T) {
	root := uat12Project(t, "Johnathan", "proj")
	base := filepath.Dir(filepath.Dir(root))
	// cut is the store's preview of a NotebookEdit of value with a cell padded so that the store's
	// cut falls right after kept, a start of value.
	cut := func(value, kept string) string {
		head, mid, esc := `{"new_source":"`, `","notebook_path":"`, strings.ReplaceAll(kept, `\`, `\\`)
		pad := 120 - len("…") - len(head) - len(mid) - len(esc)
		require.Positive(t, pad, "fixture: the value starts inside the preview")
		raw, err := json.Marshal(map[string]any{"new_source": strings.Repeat("x", pad), "notebook_path": value})
		require.NoError(t, err)
		_, preview := store.ArgsDigest(raw)
		require.Equal(t, head+strings.Repeat("x", pad)+mid+esc+"…", preview, "fixture: the store's cut")
		return preview
	}
	within := func(value, mark string) string { return value[:strings.Index(value, mark)+len(mark)+2] }
	own := filepath.Join(root, "nb", "a.ipynb")
	var withheld []string
	for _, seg := range []string{"John'athan", "John^athan"} {
		sib := filepath.Join(base, seg, "proj", "nb", "a.ipynb")
		withheld = append(withheld, cut(sib, within(sib, seg[:5])))
	}
	res := requireToolSummaries(t, root, []string{cut(own, within(own, "John"))}, withheld)
	require.NotContains(t, res.Text, "John'at")
	require.NotContains(t, res.Text, "John^at")
}

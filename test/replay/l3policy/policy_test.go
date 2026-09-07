package l3policy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/test/replay/l3policy"
	"github.com/stretchr/testify/require"
)

// This file is the l3policy half of the plan's Phase 4 test tables (plans/V4-SP-12-scheduler-l3.md,
// "test/replay/l3policy/policy_test.go"). The pause-model anchors read their coefficients from
// eval.DefaultLatencyModel(); nothing here restates 3000 or 150 as a model constant — the two
// expected values below are the arithmetic the plan states, not a second copy of the model.

const (
	policyName   = "qompack-l3"
	corpusRel    = "../../../testdata/sessions/synthetic"
	turnStepMS   = 45_000
	idleGapMS    = 3_600_000
	msPerSecond  = 1000.0
	sessionStart = core.UnixMilli(1_700_000_000_000)
)

// loadCorpus reads the committed 24-session synthetic corpus the way the driver does.
func loadCorpus(t *testing.T) (eval.Harness, []eval.Session, config.Config) {
	t.Helper()
	cfg := config.Defaults()
	h := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()})
	sessions, err := h.Load(filepath.FromSlash(corpusRel))
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	return h, sessions, cfg
}

func TestPolicy_NameIsQompackL3(t *testing.T) {
	require.Equal(t, policyName, l3policy.New(config.Defaults()).Name())

	// init() registers the same constructor under the same name, so the driver's --policies
	// flag can reach it by name.
	p, ok := eval.PolicyByName(policyName, config.Defaults())
	require.True(t, ok, "l3policy must register itself with eval.RegisterPolicy in init()")
	require.Equal(t, policyName, p.Name())
}

func TestPolicy_Deterministic(t *testing.T) {
	ctx := context.Background()
	h, sessions, cfg := loadCorpus(t)
	s := sessions[0]
	opts := eval.ReplayOptions{Deterministic: true, Seed: 1}

	r1, err := h.Replay(ctx, s, l3policy.New(cfg), opts)
	require.NoError(t, err)
	r2, err := h.Replay(ctx, s, l3policy.New(cfg), opts)
	require.NoError(t, err)

	b1, err := json.Marshal(r1)
	require.NoError(t, err)
	b2, err := json.Marshal(r2)
	require.NoError(t, err)
	require.Equal(t, string(b1), string(b2), "two replays of %s with Seed 1 must be byte-identical", s.ID)
	require.Equal(t, r1.Keeps, r2.Keeps)
	require.Equal(t, r1.PauseMS, r2.PauseMS)
	require.Equal(t, r1.ResidualSpan, r2.ResidualSpan)
	require.NotEmpty(t, r1.Keeps)

	// The KeepSet itself, called directly, is byte-identical too: no clock, no randomness.
	at := s.CompactionAt[0]
	k1, err := l3policy.New(cfg).KeepSet(ctx, s, at, eval.DefaultKeepBudget)
	require.NoError(t, err)
	k2, err := l3policy.New(cfg).KeepSet(ctx, s, at, eval.DefaultKeepBudget)
	require.NoError(t, err)
	kb1, err := json.Marshal(k1)
	require.NoError(t, err)
	kb2, err := json.Marshal(k2)
	require.NoError(t, err)
	require.Equal(t, string(kb1), string(kb2))
}

// shapeSession is a session with one unmistakable task boundary: a long steady run of file reads
// in one package, then a 3600-second idle gap into a run of test calls on paths the first task
// never touched, and finally five tool calls — three droppable (FileRead, Bash, Grep) and two
// preserved (Task, an MCP result) — before the compaction point at the very end. The changepoint
// the detector declares lands in the second task, so all five tail calls sit after P. The tail's
// two reads re-touch files of the first task, so their file blocks are prefix blocks: the row
// then reads exactly as the plan states it. tailPaths names paths first touched after the
// boundary, for the pointer-tier test.
func shapeSession(tailPaths bool) (s eval.Session, droppable, preserved []core.ToolUseID) {
	ts := sessionStart
	next := func() core.UnixMilli { ts += turnStepMS; return ts }
	var turns []eval.Turn
	add := func(role, text string, tokens core.Tokens, calls ...eval.ToolCall) {
		turns = append(turns, eval.Turn{
			Index: core.TurnIndex(len(turns)), Role: role, TS: ts, Text: text,
			ToolCalls: calls, Tokens: tokens,
		})
		next()
	}
	call := func(id, name string, tokens int, paths ...string) eval.ToolCall {
		return eval.ToolCall{
			ID: core.ToolUseID(id), Name: name, Paths: paths,
			Result: json.RawMessage(fmt.Sprintf(`{"tokens":%d}`, tokens)),
		}
	}

	// Task A: 48 turns of steady reads across five files of one package.
	for i := range 48 {
		if i%8 == 0 {
			add("user", fmt.Sprintf("next: step %d", i), 80)
			continue
		}
		add("assistant", "", 400, call(fmt.Sprintf("tu_a%02d", i), "FileRead", 1200,
			fmt.Sprintf("src/a/file%d.go", i%5)))
	}
	// The idle gap, then task B: twelve test runs on four new paths.
	ts += idleGapMS - turnStepMS
	add("user", "now something else entirely", 120)
	for i := range 12 {
		add("assistant", "", 500, call(fmt.Sprintf("tu_b%02d", i), "Test", 900,
			fmt.Sprintf("src/b/thing%d_test.go", i%4)))
	}
	// The five tail calls: three droppable, two preserved.
	readPath, grepPath := "src/a/file1.go", "src/a/file2.go"
	if tailPaths {
		readPath, grepPath = "src/b/main.go", "src/b/util.go"
	}
	add("assistant", "", 300, call("tu_tail_read", "FileRead", 900, readPath))
	add("assistant", "", 300, call("tu_tail_bash", "Bash", 700))
	add("assistant", "", 300, call("tu_tail_grep", "Grep", 500, grepPath))
	add("assistant", "", 300, call("tu_tail_task", "Task", 2000))
	add("assistant", "", 300, call("tu_tail_mcp", "mcp__github__search", 800))

	s = eval.Session{
		ID: "shape", Turns: turns, Synthetic: true,
		CompactionAt: []core.TurnIndex{core.TurnIndex(len(turns))},
	}
	droppable = []core.ToolUseID{"tu_tail_read", "tu_tail_bash", "tu_tail_grep"}
	preserved = []core.ToolUseID{"tu_tail_task", "tu_tail_mcp"}
	return s, droppable, preserved
}

// keepSetOf runs the policy on s at its single compaction point and indexes the result.
func keepSetOf(t *testing.T, s eval.Session, budget core.Tokens) (eval.KeepSet, []eval.Block, map[string]bool) {
	t.Helper()
	at := s.CompactionAt[0]
	ks, err := l3policy.New(config.Defaults()).KeepSet(context.Background(), s, at, budget)
	require.NoError(t, err)
	blocks := eval.Blocks(s, at)
	byID := make(map[string]eval.Block, len(blocks))
	for _, b := range blocks {
		byID[b.ID] = b
	}
	kept := make(map[string]bool, len(ks.IDs))
	for _, id := range ks.IDs {
		require.Contains(t, byID, id, "KeepSet.IDs must only name blocks of the prefix")
		require.False(t, kept[id], "KeepSet.IDs must not repeat %s", id)
		kept[id] = true
	}
	return ks, blocks, kept
}

// TestPolicy_KeepSetShape is a SUPERSET check of the plan's row, not an exact keep-set: it
// asserts that everything before P is kept, that the three droppable tail results are absent,
// that the two preserved ones are present, and that Tokens prices the retained tail. It does not
// assert that only those survive — the twelve Test results between P and the tail are DropNone
// and are retained too (§2.2), and the pointer tier may add file blocks
// (TestPolicy_PointerTierRetainsRecentFiles) — so a keep-set that retains more than the row names
// still passes, by design.
func TestPolicy_KeepSetShape(t *testing.T) {
	s, droppable, preserved := shapeSession(false)
	ks, blocks, kept := keepSetOf(t, s, eval.DefaultKeepBudget)

	// P is a real boundary: after the first task, before the five tail calls.
	firstTail := toolBlock(t, blocks, droppable[0])
	require.Greater(t, ks.P, 0, "the detector must have declared the task boundary")
	require.LessOrEqual(t, ks.P, firstTail.Pos, "P must precede every tail call")

	// Everything before P is kept — the cached prefix is untouched.
	for _, b := range blocks {
		if b.Pos < ks.P {
			require.True(t, kept[b.ID], "block %s at pos %d < P=%d must be kept", b.ID, b.Pos, ks.P)
		}
	}
	// After P: the three droppable results are gone, the two preserved ones remain.
	for _, id := range droppable {
		require.False(t, kept[toolBlock(t, blocks, id).ID], "droppable %s must be absent", id)
	}
	for _, id := range preserved {
		require.True(t, kept[toolBlock(t, blocks, id).ID], "preserved %s must be kept", id)
	}
	// Tokens is exactly the sum over what is retained after P — the rehydrated tail; the cached
	// prefix costs nothing to keep.
	var sum core.Tokens
	for _, b := range blocks {
		if kept[b.ID] && b.Pos >= ks.P {
			sum += b.Tokens
		}
	}
	require.Equal(t, sum, ks.Tokens)
}

// TestPolicy_PointerTierRetainsRecentFiles pins the §8.5 tier-3 model: a droppable read after P
// is dropped, but the file block of a path first touched after P survives as a pointer while the
// budget allows — and not at all under a zero budget.
func TestPolicy_PointerTierRetainsRecentFiles(t *testing.T) {
	s, droppable, _ := shapeSession(true)

	ks, blocks, kept := keepSetOf(t, s, eval.DefaultKeepBudget)
	require.Greater(t, ks.P, 0)
	read := toolBlock(t, blocks, droppable[0])
	require.False(t, kept[read.ID], "the read's result is droppable")
	main := fileBlock(t, blocks, "src/b/main.go")
	require.GreaterOrEqual(t, main.Pos, ks.P, "precondition: the file is first touched after P")
	require.True(t, kept[main.ID], "its file block survives as a pointer under the default budget")

	ks0, blocks0, kept0 := keepSetOf(t, s, 0)
	require.Equal(t, ks.P, ks0.P, "the budget never moves the cut")
	require.False(t, kept0[fileBlock(t, blocks0, "src/b/main.go").ID], "no pointer fits a zero budget")
}

// toolBlock finds the tool-result block for one tool-use id without knowing eval's ID grammar:
// Blocks emits one BlockToolResult per ToolCall, in turn order, and the block's Turn names the
// turn whose ToolCalls it came from.
func toolBlock(t *testing.T, blocks []eval.Block, id core.ToolUseID) eval.Block {
	t.Helper()
	for _, b := range blocks {
		if b.Kind == eval.BlockToolResult && strings.HasSuffix(b.ID, string(id)) {
			return b
		}
	}
	t.Fatalf("no tool-result block for %s", id)
	return eval.Block{}
}

// fileBlock finds the file block for one path by the key Blocks recorded on it.
func fileBlock(t *testing.T, blocks []eval.Block, path string) eval.Block {
	t.Helper()
	for _, b := range blocks {
		if b.Kind == eval.BlockFile && len(b.Paths) > 0 && strings.HasSuffix(b.Paths[0], path) {
			return b
		}
	}
	t.Fatalf("no file block for %s", path)
	return eval.Block{}
}

func TestPolicy_DoesNotImportDaemon(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	require.NoError(t, err, string(out))
	deps := strings.Fields(string(out))
	require.Contains(t, deps, "github.com/qompack/qompack/internal/scheduler")
	require.NotContains(t, deps, "github.com/qompack/qompack/internal/daemon",
		"00-ARCHITECTURE §3.2: nothing imports a composition root")
}

func TestPolicy_PauseModelAnchors(t *testing.T) {
	lat := eval.DefaultLatencyModel()
	require.True(t, lat.Modelled, "deterministic replay models the pause; it never measures it")
	pause := func(residual float64) float64 {
		return lat.PauseBaseMS + lat.PausePerKResidualMS*residual/msPerSecond
	}
	// The stock anchor (§2.5's ~150K residual) sits inside §6.7's observed 15–40 s band; the
	// frontier-advanced anchor is §8.5's 10–20K case. 0.15 ms per residual token, not 0.12.
	require.InDelta(t, 25_500, pause(150_000), 0)
	require.InDelta(t, 6_000, pause(20_000), 0)
}

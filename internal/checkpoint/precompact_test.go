package checkpoint_test

// PreCompact's behaviour suite: the six rows of plans/V4-SP-10-checkpointer-l4.md §15. It reuses
// writer_test.go's fx fixture, so every backend below is real.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// hookTimeout is the plugin manifest's declared PreCompact timeout. It is passed IN rather than
// read inside the package, which is what keeps the value out of internal/checkpoint entirely and
// the nomagic pass green -- so the tests supply it the same way the daemon does.
const hookTimeout = 20 * time.Second

// precompactDebug mirrors .qompack/state/precompact.json. It lives here rather than being exported
// because nothing in internal/contract reads that file: it is a debug artifact, and a test is the
// only thing that should be asserting on its shape.
type precompactDebug struct {
	Seq               core.CheckpointSeq `json:"seq"`
	Sentinel          string             `json:"sentinel"`
	EmittedAt         core.UnixMilli     `json:"emitted_at"`
	WallMs            int64              `json:"wall_ms"`
	TimeoutMs         int64              `json:"timeout_ms"`
	InstructionsBytes int                `json:"instructions_bytes"`
	Frontier          core.TurnIndex     `json:"frontier"`
	SpanInstruction   bool               `json:"span_instruction"`
}

func (f *fx) precompactInput() checkpoint.PreCompactInput {
	now := f.p.Clock.Now()
	return checkpoint.PreCompactInput{
		Session:     f.sess,
		Trigger:     "auto",
		Now:         now,
		Deadline:    now.Add(hookTimeout),
		HookTimeout: hookTimeout,
		Budget:      core.Tokens(f.p.Cfg.Checkpoint.BudgetTokens),
		Cfg:         f.p.Cfg.Checkpoint,
		Cache:       checkpoint.CacheInfo{TTLState: "unknown"},
	}
}

func (f *fx) readPrecompactDebug(t *testing.T) precompactDebug {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(f.p.Root).State, "precompact.json")))
	require.NoError(t, err)
	var d precompactDebug
	require.NoError(t, json.Unmarshal(raw, &d))
	return d
}

// seedForPreCompact builds a session with two closed, encoded segments so the frontier is
// non-zero and the span paragraph has a real turn to name.
func seedForPreCompact(t *testing.T, f *fx) {
	t.Helper()
	f.prompt(0, "Fix the intermittent 500s on POST /api/session/refresh.", true)
	f.tool("toolu_pc_0001", 1, "Read", "src/auth.ts", "export function refreshToken() {}", false)
	f.tool("toolu_pc_0002", 5, "Bash", "", "pool acquisition timeout after 30s", false)
	f.closedSeg(1, 0, 3)
	f.closedSeg(2, 4, 9)
	d := f.begin()
	f.advance(d, 1, 2)
}

func TestPreCompactSealsACheckpointAndReturnsInstructions(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)

	require.Equal(t, core.CheckpointSeq(1), res.Ref.Seq)
	require.NotEmpty(t, res.Instructions, "PreCompact's whole output to the host is this string")
	require.FileExists(t, paths.Long(res.Ref.Path))

	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the sealed checkpoint is indexed, not just written")

	// The first line is what the contract monitor's custom_instructions_accepted probe is built
	// from, so it must be a real sentence rather than a heading or a blank.
	first := strings.SplitN(res.Instructions, "\n", 2)[0]
	require.GreaterOrEqual(t, len([]rune(first)), 24,
		"paragraph 1 must clear the contract's minimum phrase length; got %q", first)
	require.NotContains(t, res.Instructions, "\\",
		"the checkpoint path is rendered forward-slashed on every platform")
}

func TestPreCompactInstructionsCarryTheIncrementalSpan(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)
	in := f.precompactInput()
	in.Cfg.IncrementalSpanInstruction = true

	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)

	// This paragraph IS the O1 optimization: without it the host re-summarizes the whole session
	// on every compaction, and the frontier the writer maintained buys nothing.
	require.Greater(t, int(res.Ref.Frontier), 0, "fixture sanity: the frontier advanced")
	require.Contains(t, res.Instructions, "0001.json",
		"the span paragraph names the checkpoint that already covers the prefix")

	dbg := f.readPrecompactDebug(t)
	require.True(t, dbg.SpanInstruction)
	require.Equal(t, res.Ref.Frontier, dbg.Frontier)
}

func TestPreCompactOmitsTheSpanWhenDisabled(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)
	in := f.precompactInput()
	in.Cfg.IncrementalSpanInstruction = false

	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	require.NotEmpty(t, res.Instructions, "the standing instruction is emitted either way")

	dbg := f.readPrecompactDebug(t)
	require.False(t, dbg.SpanInstruction,
		"config governs the span paragraph; the debug artifact records which build emitted what")
}

func TestPreCompactWritesTheDebugArtifact(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)

	dbg := f.readPrecompactDebug(t)
	require.Equal(t, res.Ref.Seq, dbg.Seq)
	require.Equal(t, checkpoint.SentinelPhrase, dbg.Sentinel)
	require.Equal(t, len(res.Instructions), dbg.InstructionsBytes)
	require.GreaterOrEqual(t, dbg.WallMs, int64(0))
	// timeout_ms is the value the DAEMON supplied, echoed back. Asserting it against the input
	// rather than against a literal is what keeps the hook timeout owned by one place.
	require.Equal(t, hookTimeout.Milliseconds(), dbg.TimeoutMs)
}

func TestPreCompactFinalizesEvenWhenTheDeadlineHasPassed(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	in := f.precompactInput()
	// A host that hands us a deadline already in the past must still get a written checkpoint:
	// §12's PreCompact-timeout row says finalize as-is, not give up. The floor inside preCompact
	// is what makes that true, and without it the context would be born cancelled.
	in.Deadline = in.Now.Add(-time.Second)

	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err, "an expired deadline must not cost us the artifact")
	require.Equal(t, core.CheckpointSeq(1), res.Ref.Seq)
	require.FileExists(t, paths.Long(res.Ref.Path))
}

func TestPreCompactOnAColdSessionBeginsAndSeals(t *testing.T) {
	f := newFx(t)
	// No Begin, no Advance: compaction fired before any idle window ran. The cold path has to open
	// a draft and catch up itself, because the alternative is a session with no checkpoint at all.
	f.prompt(0, "Investigate the pool exhaustion.", true)
	f.tool("toolu_pc_cold", 1, "Read", "src/pool.ts", "export function acquire() {}", false)
	f.closedSeg(1, 0, 4)
	require.Empty(t, f.w.OpenDrafts(), "fixture sanity: no draft is open yet")
	// The daemon publishes the seams at bind time; without them the cold path has no SourceSet.
	f.w.SetSources(f.src)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	require.True(t, res.NewDraft, "the cold path reports that it had to open the draft itself")
	require.Equal(t, core.CheckpointSeq(1), res.Ref.Seq)
	require.FileExists(t, paths.Long(res.Ref.Path))
}

// TestPreCompactDerivesItsBudgetFromTheCallersDeadline walks the four shapes a caller's deadline
// can take, because one of them used to produce the OPPOSITE of what the code intended.
//
// The budget is Deadline.Sub(Now) - finalizeGuard, clamped into [minFinalizeWindow, maxPreCompact].
// The clamp order is fine; the SUBTRACTION was the bug. A zero time.Time is the furthest-past
// instant there is, so Deadline.Sub(Now) saturates at the minimum duration for a caller that left
// Deadline unset — and subtracting finalizeGuard from the minimum duration WRAPS POSITIVE, so the
// upper clamp then handed that caller the most lenient budget in the table. The comment promised
// the exact opposite: a deadline in the past floors at minFinalizeWindow.
//
// The budget is read back through the context Finalize hands to pins.Materialize, which is the last
// thing it does with it. The thresholds are deliberately far apart — the floor is 250 ms and the cap
// is 1.5 s — so the assertion is about which clamp fired, not about timing precision.
func TestPreCompactDerivesItsBudgetFromTheCallersDeadline(t *testing.T) {
	// midpoint separates "floored" from "capped" with more than half a second of slack on each
	// side, so a stalled machine cannot flip the answer.
	const midpoint = 875 * time.Millisecond

	for _, tc := range []struct {
		name    string
		offset  time.Duration
		zero    bool
		lenient bool
	}{
		{name: "zero_deadline", zero: true, lenient: false},
		{name: "deadline_in_the_past", offset: -time.Second, lenient: false},
		{name: "deadline_equals_now", offset: 0, lenient: false},
		{name: "deadline_well_in_the_future", offset: hookTimeout, lenient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			seedForPreCompact(t, f)

			in := f.precompactInput()
			if tc.zero {
				in.Deadline = time.Time{}
			} else {
				in.Deadline = in.Now.Add(tc.offset)
			}

			// §12's PreCompact-timeout row says finalize as-is: every shape still gets an artifact.
			res, err := f.w.PreCompact(f.ctx(), in)
			require.NoError(t, err)
			require.FileExists(t, paths.Long(res.Ref.Path))

			require.True(t, f.pins.hasDeadline, "PreCompact must install a deadline of its own")
			remaining := time.Until(f.pins.ctxDeadline)
			if tc.lenient {
				require.Greater(t, remaining, midpoint,
					"a caller with real time left gets the capped budget; got %v", remaining)
			} else {
				require.LessOrEqual(t, remaining, midpoint,
					"a deadline that is absent, past or already here must take the FLOOR, "+
						"never the most lenient budget in the table; got %v", remaining)
			}
		})
	}
}

// countingSegments records how many MarkEncoded batches were written, which is one per Advance
// call: it is how this file tells "one batched Advance" from "one Advance per segment".
type countingSegments struct {
	store.SegmentLog
	marks int
}

func (c *countingSegments) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	c.marks++
	return c.SegmentLog.MarkEncoded(ctx, ids, seq)
}

// narrativeSegmentOrder reads the segment ids out of a checkpoint's narrative, in the order the
// lines were appended — which is the order Advance encoded them in.
func narrativeSegmentOrder(t *testing.T, narrative string) []int {
	t.Helper()
	var out []int
	for _, line := range strings.Split(narrative, "\n") {
		var id int
		if _, err := fmt.Sscanf(line, "seg %d turns", &id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// TestColdPreCompactCatchesUpInOneBatchOldestFirst pins both halves of the cold path's shape.
//
// One batch, because every Advance does a full-graph scan: nodesInRange calls dag.NodesAfter(0),
// which allocates and copies every node in the graph under the index lock and then sorts the copy.
// Calling Advance once per segment made the hook path O(unencoded segments x |V|), and the time
// check between calls could not stop a scan already in flight — nodesInRange's own doc comment says
// "never inside a loop", and Advance documents "Per batch there is exactly ONE full-graph scan".
//
// Oldest first, because the frontier only advances over a CONTIGUOUS encoded prefix. Encoding
// newest-first leaves a gap behind every segment it encodes, so the frontier stays at 0 whenever
// the budget stops the walk early — and the O1 span paragraph, which is the entire reason the cold
// path bothers catching up, is then suppressed.
func TestColdPreCompactCatchesUpInOneBatchOldestFirst(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Investigate the pool exhaustion.", true)
	for k := 1; k <= 4; k++ {
		start := core.TurnIndex(10*k - 9)
		f.tool(fmt.Sprintf("toolu_cold_%d", k), start+1, "Read", fmt.Sprintf("src/p%d.ts", k), "body", false)
		f.closedSeg(core.SegmentID(k), start, core.TurnIndex(10*k))
	}
	require.Empty(t, f.w.OpenDrafts(), "fixture sanity: compaction fired before any idle window")

	counted := &countingSegments{SegmentLog: f.src.Segments}
	cold := f.src
	cold.Segments = counted
	f.w.SetSources(cold)

	in := f.precompactInput()
	in.Cfg.IncrementalSpanInstruction = true
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	require.True(t, res.NewDraft)

	require.Equal(t, 1, counted.marks,
		"the cold path catches up in ONE batched Advance, not one full-graph scan per segment")

	raw, err := os.ReadFile(paths.Long(res.Ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4}, narrativeSegmentOrder(t, cp.Narrative),
		"segments are encoded oldest first, so the frontier advances over a contiguous prefix")

	require.Equal(t, core.TurnIndex(40), res.Ref.Frontier)
	require.Contains(t, res.Instructions, "through turn 40",
		"and the O1 span paragraph — the reason the cold path catches up at all — has a turn to name")
}

func TestPreCompactReportsTruncationButNotPointerDrops(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	in := f.precompactInput()
	// A budget of 1 forces the walk to exhaust both cuttable tiers, so the sealed artifact carries
	// truncation drops.
	in.Budget = core.Tokens(1)

	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)
	require.True(t, res.Truncated, "a budget of 1 cannot fit tier 1; that is truncation")
	require.NotEmpty(t, res.Drops)
}

func TestPreCompactExtraDropsAndOpenQuestionsReachTheArtifact(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)

	in := f.precompactInput()
	// ExtraDrops and OpenQuestions are how SP-12 and SP-14 contribute to a checkpoint without
	// editing this package, so the pass-through is part of the contract rather than a convenience.
	in.ExtraDrops = []checkpoint.DropEntry{{
		Kind: "path_rule", ID: "api-conventions.md", Detail: "no pointer matched its globs",
	}}
	in.OpenQuestions = []string{"Is staging running the same pgbouncer build as production?"}
	in.CurrentWork = &checkpoint.CurrentWork{
		Goal: "Eliminate pool exhaustion.", NextStep: "Extract the IdP call from the transaction.",
	}

	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(res.Ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	require.Equal(t, "Eliminate pool exhaustion.", cp.CurrentWork.Goal)
	require.Contains(t, cp.OpenQuestions, "Is staging running the same pgbouncer build as production?")
	var kinds []string
	for _, d := range cp.Dropped {
		kinds = append(kinds, d.Kind)
	}
	require.Contains(t, kinds, "path_rule")
}

// TestPreCompactNamesTheContiguousFrontierInTheSpan is the consumer half of the frontier's claim.
//
// store.SegmentLog.Frontier is the EndTurn of the last CONSECUTIVELY encoded segment, and the O1
// span paragraph turns that number into an instruction: do not re-summarize anything through turn
// N. Advancing to the maximum EndTurn of whatever a batch happened to encode misstates it whenever
// a gap exists — here segments 1 and 3 are encoded and segment 2 is not, so turns 11-20 are in
// neither the checkpoint nor the summary, and a maximum frontier would have told the summarizer in
// writing to drop them.
//
// The frontier arithmetic itself is asserted in writer_test.go
// (TestAdvanceKeepsTheFrontierContiguousAcrossAGap); this row asserts what the instruction says.
func TestPreCompactNamesTheContiguousFrontierInTheSpan(t *testing.T) {
	f := newFx(t)
	f.prompt(1, "Trace the pool exhaustion end to end.", true)
	for k := 1; k <= 3; k++ {
		start := core.TurnIndex(10*k - 9)
		f.tool(fmt.Sprintf("tu_%d", k), start+1, "Read", fmt.Sprintf("src/f%d.ts", k), "body", false)
		f.closedSeg(core.SegmentID(k), start, core.TurnIndex(10*k))
	}

	d := f.begin()
	require.Equal(t, core.TurnIndex(10), f.advance(d, 1, 3),
		"fixture sanity: coverage ends where segment 1 ends")

	in := f.precompactInput()
	in.Cfg.IncrementalSpanInstruction = true
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(t, err)

	require.Equal(t, core.TurnIndex(10), res.Ref.Frontier)
	require.Contains(t, res.Instructions, "through turn 10",
		"the span paragraph names the contiguous frontier")
	require.NotContains(t, res.Instructions, "turn 30",
		"naming segment 3's EndTurn would tell the summarizer to drop turns 11-20 entirely")
}

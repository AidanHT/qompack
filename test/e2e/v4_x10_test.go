// V4 §4.10 — `why` and `dropped` answer from REAL producers, and report unavailable rather than
// erroring when their producer is absent.
//
// Wired: internal/checkpoint's ExtractDecisions — the only producer of a core.DecisionID — running
// inside a real Finalize, read back through the real checkpoint.Reader by the real MCP `why`
// handler; and internal/rehydrate's real Build, whose drop report the real Reporter persists and
// the real MCP `dropped` handler returns.
//
// SUPERSEDED CLAUSE (reconciliation map §4.10): unit F (a9a9e1d) added the ToolDeps checkpoint
// gate. With no checkpoint reader and no drop reporter both tools must report UNAVAILABLE, not
// error — that gated arm is the negative control below.
package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
)

// x10v4Session is this row's session identity.
const x10v4Session = core.SessionID("sess-e2e-v4-x10")

// The elimination whose record is what ExtractDecisions derives a real Decision from (§9 source b).
const (
	x10v4Target   = "src/auth/refresh.ts:rotate"
	x10v4Approach = "retry the rotation twice before failing"
	x10v4Reason   = "the second rotation invalidates the first token, so the retry cannot succeed"
)

// TestV4_WhyAndDroppedAnswerFromRealProducers is V4-VERIFY §4.10.
//
// The negative control is the last arm: a server composed WITHOUT the checkpoint reader and
// WITHOUT the drop reporter must answer unavailable on both tools — an explicit state the model can
// read — rather than raising a protocol error or, worse, an empty success that reads as "there is
// no such decision" and "nothing was dropped".
func TestV4_WhyAndDroppedAnswerFromRealProducers(t *testing.T) {
	ctx := context.Background()
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x10v4Session, "why is the refresh rotation still failing"), env)
	r.SeedTurns(t, x10v4Session, "v4x10", 4)
	v4EnsureLedger(t, r, x10v4Session)

	// A real elimination: §9 source (b) is what turns it into a real Decision at Finalize.
	m, ok := r.Opts.LedgerHandle().(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a Maintainer")
	_, _, err := m.IngestMCP(ctx, negknow.MCPArgs{
		Target: x10v4Target, Approach: x10v4Approach, Reason: x10v4Reason,
		Scope: string(negknow.ScopeProject),
	})
	require.NoError(t, err)

	// Real closed, encoded segments: ExtractDecisions scans from the draft's own turn cut, so a
	// draft that covers nothing has nothing to derive a decision from.
	cpCloseObserverSegment(t, r.Segs, x10v4Session)
	for i := range 3 {
		cpCloseSegment(t, r.Segs, x10v4Session, core.TurnIndex(i*10), core.TurnIndex(i*10+9))
	}
	require.Contains(t, r.RunIdle(t), "advance_frontier")

	_, instr := r.PreCompact(t, x10v4Session)
	require.NotEmpty(t, instr, "the PreCompact must have sealed a checkpoint to read decisions from")

	// ── The decisions a REAL ExtractDecisions run created, read back through the real reader ─────
	rd, err := checkpoint.OpenReader(p.Root, p.Log, r.Opts.Metrics)
	require.NoError(t, err)
	refs, err := rd.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, refs, "the sealed artifact must be listable")

	var decisions []checkpoint.Decision
	var fromSeq core.CheckpointSeq
	for _, ref := range refs {
		cp, _, readErr := rd.Get(ctx, ref.Seq)
		require.NoError(t, readErr)
		if len(cp.Decisions) > 0 {
			decisions, fromSeq = cp.Decisions, ref.Seq
			break
		}
	}
	require.NotEmpty(t, decisions,
		"ExtractDecisions must have derived at least one decision from the recorded elimination — "+
			"it is the only producer of a core.DecisionID, and `why` has nothing to find without it")

	srv := v4Server(t, v4ToolDeps(t, r))

	var why struct {
		Found         bool               `json:"found"`
		DecisionID    string             `json:"decision_id"`
		What          string             `json:"what"`
		Why           string             `json:"why"`
		CheckpointSeq core.CheckpointSeq `json:"checkpoint_seq"`
	}
	res := v4Call(t, srv, x10v4Session, mcp.ToolWhy,
		map[string]any{"decision_id": string(decisions[0].ID)}, &why)
	require.False(t, res.IsError, "a decision that exists must not come back as a tool error")
	require.True(t, why.Found,
		"`why` must find the decision a real ExtractDecisions run created: %+v", decisions[0])
	require.Equal(t, string(decisions[0].ID), why.DecisionID,
		"the answer must be about the decision that was asked for")
	require.Equal(t, decisions[0].What, why.What, "the answer must carry the artifact's own text")
	require.Equal(t, fromSeq, why.CheckpointSeq,
		"`why` must name the checkpoint it actually read the decision out of")

	// ── The drop report the REAL rehydrator wrote ────────────────────────────────────────────────
	ac := r.CompactStart(t, x10v4Session)
	require.NotEmpty(t, ac, "the rehydration whose drop report `dropped` returns")
	require.Contains(t, ac, "## 7. No longer in context",
		"item 7 IS the drop report reaching the model: %s", ac)

	var droppedBody struct {
		Drops     []checkpoint.DropEntry `json:"drops"`
		Count     int                    `json:"count"`
		Available *bool                  `json:"available"`
		Reason    string                 `json:"reason"`
	}
	v4Call(t, srv, x10v4Session, mcp.ToolDropped, map[string]any{}, &droppedBody)
	require.Nil(t, droppedBody.Available,
		"a wired drop reporter must not report itself unavailable: %+v", droppedBody)

	fromReporter, err := v4DropReporter{rep: r.DropReporter()}.CurrentDrops(ctx, x10v4Session)
	require.NoError(t, err)
	require.Equal(t, len(fromReporter), droppedBody.Count,
		"`dropped` must return the drop report the real Build actually wrote, entry for entry: "+
			"reporter=%+v tool=%+v", fromReporter, droppedBody.Drops)

	// ── NEGATIVE CONTROL: the gated arm — no checkpoint reader, no drop reporter ─────────────────
	gated := v4Server(t, mcp.ToolDeps{
		Store: r.Opts.Store, Cfg: p.Cfg, ProjectRoot: p.Root,
		// Checkpoints and Rehydrator deliberately absent.
	})

	var gatedWhy struct {
		Found     bool   `json:"found"`
		Available *bool  `json:"available"`
		Reason    string `json:"reason"`
	}
	gatedRes := v4Call(t, gated, x10v4Session, mcp.ToolWhy,
		map[string]any{"decision_id": string(decisions[0].ID)}, &gatedWhy)
	require.False(t, gatedRes.IsError,
		"NEGATIVE CONTROL: an absent producer is an UNAVAILABLE state, never a tool error")
	require.NotNil(t, gatedWhy.Available,
		"`why` with no checkpoint reader must say so explicitly: %+v", gatedWhy)
	require.False(t, *gatedWhy.Available)
	require.NotEmpty(t, gatedWhy.Reason, "an unavailable answer must name what is missing")
	require.False(t, gatedWhy.Found,
		"and it must not be reported as a decision that does not exist — that is a false absence")

	var gatedDropped struct {
		Drops     []checkpoint.DropEntry `json:"drops"`
		Count     int                    `json:"count"`
		Available *bool                  `json:"available"`
		Reason    string                 `json:"reason"`
	}
	gatedDropRes := v4Call(t, gated, x10v4Session, mcp.ToolDropped, map[string]any{}, &gatedDropped)
	require.False(t, gatedDropRes.IsError, "the same for `dropped`: unavailable, not an error")
	require.NotNil(t, gatedDropped.Available, "%+v", gatedDropped)
	require.False(t, *gatedDropped.Available)
	require.NotEmpty(t, gatedDropped.Reason)
	require.Empty(t, gatedDropped.Drops,
		"an unavailable drop report is empty AND explained — never empty and silent")
}

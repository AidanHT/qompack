// V4 §4.2 — the scheduler decides to write BEFORE the host's own auto-compact threshold, and the
// checkpoint cadence consumer seals at that point.
//
// Wired: real hook events through the real binary into the real observer and the real store; a real
// internal/daemon scheduler.Runtime built by the shipped NewSchedulerRuntime over that store and
// DAG, resolving the real window ladder; internal/scheduler's real Evaluate over a simulated
// context-growth trace; and the real act.checkpoint_cadence idle consumer in
// internal/daemon/wire_checkpoint.go.
//
// The "stock threshold" is the HOST's, from Qompack.md §2.5:
// effectiveWindow − HostAutoCompactBuffer. It is read from internal/scheduler's own exported host
// constants, never written as a literal here, so a change to either side moves both.
package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/testutil"
)

// x2v4Session is this row's session identity.
const x2v4Session = core.SessionID("sess-e2e-v4-x02")

// x2v4CadenceSegments is comfortably past §8.5's eight-segment cadence threshold, which is what
// makes act.checkpoint_cadence seal rather than merely run.
const x2v4CadenceSegments = 10

// x2v4SegmentTurn is the inclusive turn width of each segment the cadence arm closes.
const x2v4SegmentTurn = 5

// x2v4TraceCeilingPct is where the simulated growth trace stops, as a fraction of the effective
// window: past the shipped hard ceiling (which is effective − 13 000 − 20 000, i.e. ~81 %) so the
// firing position is a property of the ladder, and short of the window itself so the negative
// control can raise the soft floor ABOVE the trace's maximum, which is what that control means.
const x2v4TraceCeilingPct = 0.90

// x2v4ControlSoftFloorPct sits strictly above x2v4TraceCeilingPct: the negative control's ladder.
const x2v4ControlSoftFloorPct = 0.95

// x2v4Trace is the simulated context-growth trace, in tokens.
func x2v4Trace(effective core.Tokens) []core.Tokens {
	const steps = 120
	top := int(float64(effective) * x2v4TraceCeilingPct)
	out := make([]core.Tokens, 0, steps)
	for i := 1; i <= steps; i++ {
		out = append(out, core.Tokens(top*i/steps))
	}
	return out
}

// x2v4Candidates is the cut-point ladder the trace is evaluated against.
//
// It is DATA, not a stubbed collaborator: Evaluate is a pure function whose Candidates field the
// Runtime assembles from the DAG and the store, and this row is about the composite trigger's
// threshold arithmetic rather than about candidate assembly (which arm 1's real Runtime exercises).
// Every candidate sits on a round boundary so eligible() never has to relax, and reclaimable grows
// with position so chooseP always has a legal cut to name.
func x2v4Candidates(effective core.Tokens) []scheduler.Candidate {
	const n = 8
	out := make([]scheduler.Candidate, 0, n)
	for i := 1; i <= n; i++ {
		pos := int(effective) * i / (n * 2)
		out = append(out, scheduler.Candidate{
			Pos:               pos,
			Turn:              core.TurnIndex(i),
			SegmentID:         core.SegmentID(i),
			RoundBoundary:     true,
			ReclaimableTokens: core.Tokens(pos / 2),
			Coupling:          1,
		})
	}
	return out
}

// x2v4FirstFiring runs the REAL Evaluate at every point of the trace and returns the first
// ContextTokens position at which it decides to compact, or -1 when it never does.
func x2v4FirstFiring(t *testing.T, cfg config.SchedulerCfg, effective core.Tokens, trace []core.Tokens) (core.Tokens, scheduler.Decision) {
	t.Helper()
	cands := x2v4Candidates(effective)
	for _, n := range trace {
		d := scheduler.Evaluate(scheduler.Inputs{
			Now:             core.UnixMilli(testutil.Epoch.UnixMilli()),
			ContextTokens:   n,
			EffectiveWindow: effective,
			MaxOutputTokens: scheduler.HostDefaultMaxOutput,
			Candidates:      cands,
			Cfg:             cfg,
		})
		if d.ShouldCompact {
			return n, d
		}
	}
	return -1, scheduler.Decision{}
}

// TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold is V4-VERIFY §4.2.
//
// The negative control is the third arm: with the ladder raised above the trace's maximum the
// scheduler must not fire anywhere on the same trace. Without it, "fires below the host threshold"
// would be satisfied by a scheduler that fires at every point, including zero.
func TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x2v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x2v4Session, "grow the context and checkpoint before the host does"), env)
	r.SeedTurns(t, x2v4Session, "v4x02", 8)
	// Arm 4 below is about the §8.5 IDLE cadence, and the idle tasks seed tier 1 from
	// eliminations — so this row must be a daemon that has compacted before it can advance a
	// frontier at all. See OpenLedgerByCompacting: the ledger is lazy by design, and a
	// compact-start opens it without sealing anything Arm 4 would then have to explain away.
	r.OpenLedgerByCompacting(t, x2v4Session)

	// ── Arm 1: a REAL Runtime over the real store and DAG resolves the real ladder ───────────────
	rt, err := daemon.NewSchedulerRuntime(daemon.SchedulerRuntimeOptions{
		ProjectRoot: p.Root,
		Session:     x2v4Session,
		Cfg:         p.Cfg,
		Clock:       core.SystemClock(),
		Log:         p.Log,
		Metrics:     r.Opts.Metrics,
		Store:       r.Opts.Store,
		Graph:       r.Opts.Graph,
		LedgerFn:    r.LedgerFn(),
		Checkpoints: r.W,
		Sources:     r.Src,
		Getenv:      func(string) string { return "" },
	})
	require.NoError(t, err, "the shipped scheduler Runtime must compose over the real store and DAG")

	live, err := rt.Evaluate(ctx)
	require.NoError(t, err)
	require.Positive(t, live.HardCeilingTokens,
		"a real Runtime must resolve a real window ladder, not the error_no_window branch: %v", live.Breakdown)

	stock := live.HardCeilingTokens + core.Tokens(p.Cfg.Scheduler.HardCeilingMargin) + scheduler.HostAutoCompactBuffer
	hostThreshold := stock - scheduler.HostAutoCompactBuffer
	require.Less(t, live.HardCeilingTokens, hostThreshold,
		"the hard ceiling — the position past which the composite trigger fires unconditionally — "+
			"must sit strictly below the host's own auto-compact threshold, by the configured margin")
	require.Less(t, live.SoftFloorTokens, live.HardCeilingTokens,
		"the ladder must be ordered: nothing fires below the soft floor")

	// ── Arm 2: the real Evaluate over a simulated growth trace ───────────────────────────────────
	effective := scheduler.EffectiveWindow(scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput)
	require.Positive(t, effective)
	stockThreshold := effective - scheduler.HostAutoCompactBuffer

	at, decision := x2v4FirstFiring(t, p.Cfg.Scheduler, effective, x2v4Trace(effective))
	require.NotEqual(t, core.Tokens(-1), at,
		"the scheduler must fire somewhere on a trace that climbs to the whole effective window")
	require.Less(t, at, stockThreshold,
		"the decision to write must land strictly below the host's stock auto-compact threshold "+
			"(effectiveWindow %d − %d): fired at %d", int(effective), int(scheduler.HostAutoCompactBuffer), int(at))
	require.NotEmpty(t, decision.Reasons, "a firing decision must name the terms that fired it")

	// Under SHIPPED defaults the Young-Daly clause contributes nothing: MeasuredDeltaSeconds is
	// null, so the clause is unmeasured and the firing is attributable to the supported terms only.
	require.Zero(t, decision.YoungDalySeconds,
		"no Young-Daly interval may be computed under shipped defaults: %v", decision.Breakdown)
	offMarkers := 0
	for _, k := range []string{"young_daly_disabled", "young_daly_delta_unmeasured", "young_daly_no_baseline"} {
		if decision.Breakdown[k] == 1 {
			offMarkers++
		}
	}
	require.Positive(t, offMarkers,
		"the Breakdown must record WHY the Young-Daly clause is off under shipped defaults: %v",
		decision.Breakdown)

	// ── Arm 3, the NEGATIVE CONTROL: raise the ladder above the trace's maximum ──────────────────
	raised := p.Cfg.Scheduler
	raised.SoftFloorPct = x2v4ControlSoftFloorPct
	raised.HardCeilingMargin = int(effective)
	never, _ := x2v4FirstFiring(t, raised, effective, x2v4Trace(effective))
	require.Equal(t, core.Tokens(-1), never,
		"NEGATIVE CONTROL: with the soft floor above the trace's maximum the scheduler must not "+
			"fire anywhere on the same trace — it fired at %d instead", int(never))

	// ── Arm 4: the real cadence consumer seals at that point ─────────────────────────────────────
	//
	// act.checkpoint_cadence is the §8.5 consumer of the decision to write. It is asserted by its
	// EFFECT — a sealed artifact — because that is the only thing the decision is for.
	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"nothing may be sealed before the cadence threshold is crossed")

	cpCloseObserverSegment(t, r.Segs, x2v4Session)
	for i := range x2v4CadenceSegments {
		start := core.TurnIndex(i * x2v4SegmentTurn)
		cpCloseSegment(t, r.Segs, x2v4Session, start, start+x2v4SegmentTurn-1)
	}

	ran := r.RunIdle(t)
	require.Contains(t, ran, "act.checkpoint_cadence",
		"the acting cadence task must have run in ModeFull; ran=%v", ran)
	// A second pass: advance_frontier folds the segments into the draft on the pass that observes
	// them, and the cadence consumer judges the draft it finds at the START of its own pass.
	r.RunIdle(t)

	require.NotEmpty(t, cpCheckpointArtifacts(t, p.Root),
		"with %d encoded segments past §8.5's eight-segment cadence threshold, the cadence consumer "+
			"must have sealed a draft without any PreCompact hook arriving", x2v4CadenceSegments)
	x4RequireManifestVerifies(t, p.Root)
}

package scheduler

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// TriggerReason names one of the composite trigger's five named conditions (Qompack.md §8.4:
// should_compact = tokens > soft_floor AND (at_changepoint OR elapsed > young_daly_interval OR
// tokens > hard_ceiling OR idle_gap > ttl)). SP-12 owns the logic that decides which of these are
// true; SP-01 fixes their wire values now so every consumer agrees on the spelling before that
// logic exists.
type TriggerReason string

// The five composite-trigger conditions. All five, not just the four disjuncts, can appear in
// Decision.Reasons: soft_floor is the AND-gate and the other four are the OR-clause, but every
// condition that evaluates true is reported so that /qompack:status and the eval harness can show
// why (or why not) a Decision fired.
const (
	// TriggerSoftFloor is true when ContextTokens has risen above Cfg.SoftFloorPct of
	// EffectiveWindow — the AND-gate: compaction never fires below this floor, so the plugin acts
	// well before Claude Code's own auto-compact threshold (Qompack.md §8.4).
	TriggerSoftFloor TriggerReason = "soft_floor"
	// TriggerChangepoint is true when Changepoint.AtChangepoint is true: BOCD has detected a task
	// boundary, the cheapest place to pay the rewrite cost.
	TriggerChangepoint TriggerReason = "changepoint"
	// TriggerYoungDaly is true when the elapsed time since the last significant cache write has
	// reached the Young-Daly optimal interval I* = sqrt(2*delta*M) (see YoungDaly).
	TriggerYoungDaly TriggerReason = "young_daly"
	// TriggerHardCeiling is true when ContextTokens has reached EffectiveWindow minus
	// Cfg.HardCeilingMargin: one turn's headroom below Claude Code's own threshold, so the plugin
	// always gets to checkpoint first.
	TriggerHardCeiling TriggerReason = "hard_ceiling"
	// TriggerIdleColdCache is true during an idle gap once the prompt cache is provably cold: the
	// idle gap has exceeded the cache TTL, so the rewrite the next message forces is already
	// unavoidable and a deep cut now is nearly free.
	TriggerIdleColdCache TriggerReason = "idle_cold_cache"
)

// TTLState reports where the prompt cache sits on its sliding TTL (00-ARCHITECTURE.md §5.4).
type TTLState string

// The four TTL states.
const (
	// TTLWarm means the cache is inside its TTL: a write now would be wasted.
	TTLWarm TTLState = "warm"
	// TTLExpiring means the TTL is close enough to expiry that a write should be considered.
	TTLExpiring TTLState = "expiring"
	// TTLCold means the TTL has already lapsed: the next call pays a full cache write regardless,
	// so a deep cut is nearly free (Qompack.md §5.4).
	TTLCold TTLState = "cold"
	// TTLUnknown means the scheduler has not observed enough API-call history to classify the
	// cache state yet — for example, at the very start of a session.
	TTLUnknown TTLState = "unknown"
)

// Urgency classifies how strongly a Decision recommends acting on it.
type Urgency uint8

// The three urgency levels.
const (
	// UrgencyNone means ShouldCompact is false: no trigger fired.
	UrgencyNone Urgency = iota
	// UrgencyAdvisory means a soft signal fired (for example TriggerChangepoint alone):
	// compaction is worthwhile but not urgent.
	UrgencyAdvisory
	// UrgencyNow means a hard signal fired (TriggerHardCeiling, or TriggerSoftFloor combined with
	// TriggerIdleColdCache): compaction should happen before the next tool call.
	UrgencyNow
)

// BackgroundTask names one piece of idle-time (O3) work the scheduler may ask the daemon's
// IdleController to run (00-ARCHITECTURE.md §8.4).
type BackgroundTask string

// The six background tasks.
const (
	// BackgroundAdvanceFrontier advances the checkpoint frontier over newly closed segments (O1).
	BackgroundAdvanceFrontier BackgroundTask = "advance_frontier"
	// BackgroundGC runs the store's reference-counted garbage collector.
	BackgroundGC BackgroundTask = "gc"
	// BackgroundPrecomputeSlice refreshes the dependence-DAG slice cache ahead of the next
	// compaction.
	BackgroundPrecomputeSlice BackgroundTask = "precompute_slice"
	// BackgroundRefreshDelta recomputes analyzer delta-scores that have gone stale.
	BackgroundRefreshDelta BackgroundTask = "refresh_delta"
	// BackgroundRebuildBloom rebuilds sketches/tried.bloom from records/eliminations.jsonl.
	BackgroundRebuildBloom BackgroundTask = "rebuild_bloom"
	// BackgroundCompactDAG compacts the on-disk dependence-DAG log.
	BackgroundCompactDAG BackgroundTask = "compact_dag"
)

// Features is one observation fed to the BOCD changepoint Detector (Qompack.md §6.6,
// 00-ARCHITECTURE.md §5.13).
type Features struct {
	// PathJaccard is the Jaccard distance over recently touched file paths: locality.
	PathJaccard float64
	// ToolShift measures the shift in the tool-type distribution between adjacent windows.
	ToolShift float64
	// LexicalCohesion is a TextTiling-style lexical cohesion score across the recent turns.
	LexicalCohesion float64
	// GapSeconds is the wall-clock gap since the previous turn.
	GapSeconds float64
	// TodoTransition measures how much the todo-list state changed.
	TodoTransition float64
}

// ChangepointState is the BOCD run-length posterior at one point in the session.
type ChangepointState struct {
	// RunLength is the most likely run length since the last changepoint.
	RunLength int
	// ProbChangepoint is the posterior probability that the current step is itself a changepoint.
	ProbChangepoint float64
	// AtChangepoint is the thresholded decision derived from ProbChangepoint.
	AtChangepoint bool
	// Posterior is the pruned run-length posterior distribution.
	Posterior []float64
}

// Candidate is one position the scheduler could choose as the compaction point p (Qompack.md
// §8.4: candidates = changepoint boundaries intersected with API-round boundaries).
type Candidate struct {
	// Pos is the candidate's token position.
	Pos int
	// Turn is the candidate's turn index.
	Turn core.TurnIndex
	// SegmentID is the segment the candidate falls in.
	SegmentID core.SegmentID
	// RoundBoundary reports whether Pos lands on an API-round boundary.
	RoundBoundary bool
	// ReclaimableTokens estimates how many tokens compacting at Pos would reclaim.
	ReclaimableTokens core.Tokens
	// Coupling is dag.CrossingEdges(Pos): how many dependence edges straddle this position. It is
	// assembled by the Runtime implementation (00-ARCHITECTURE.md §5.13), never by this package.
	Coupling int
}

// Inputs is the complete, explicit snapshot Evaluate decides from. Every field the composite
// trigger needs is here so that Evaluate can remain a pure function (00-ARCHITECTURE.md §5.13).
type Inputs struct {
	// Now is the current wall-clock time, sampled by the caller — Evaluate never reads a clock.
	Now core.UnixMilli
	// ContextTokens is the current size of the context window.
	ContextTokens core.Tokens
	// EffectiveWindow is the model's context window minus the reserved output budget
	// (00-ARCHITECTURE.md §2.5).
	EffectiveWindow core.Tokens
	// MaxOutputTokens is the model's maximum output token budget.
	MaxOutputTokens core.Tokens
	// LastAPICallTS is the timestamp of the last API round trip: the sliding-TTL idle model (E1),
	// deliberately NOT the same as LastCacheWriteTS.
	LastAPICallTS core.UnixMilli
	// LastCacheWriteTS is the timestamp of the last prompt-cache write.
	LastCacheWriteTS core.UnixMilli
	// BurnRateTokensPerMin is the recent token consumption rate.
	BurnRateTokensPerMin float64
	// MeasuredDeltaSeconds is delta for the Young-Daly formula; nil (config null) means "measure
	// at runtime" rather than a measured zero.
	MeasuredDeltaSeconds *float64
	// Changepoint is the BOCD state at Now.
	Changepoint ChangepointState
	// Candidates is the set of compaction points the scheduler may choose among.
	Candidates []Candidate
	// FrontierTurn is the last turn already covered by a finalized checkpoint.
	FrontierTurn core.TurnIndex
	// ResidualTokens is the token span between FrontierTurn and Now (O5).
	ResidualTokens core.Tokens
	// ExpectedRemainingReads feeds the ski-rental cache-write decision (Phase 7).
	ExpectedRemainingReads float64
	// Cfg is the scheduler configuration section in force for this evaluation.
	Cfg config.SchedulerCfg
}

// Decision is Evaluate's output: whether to compact, at which candidate, and why.
type Decision struct {
	// ShouldCompact is the composite trigger's final answer.
	ShouldCompact bool
	// Reasons lists every TriggerReason condition that evaluated true, in evaluation order (see
	// the TriggerReason constants).
	Reasons []TriggerReason
	// P is the chosen compaction candidate.
	P Candidate
	// PScore is P's selection score.
	PScore float64
	// Breakdown names the terms behind PScore ("reclaimable", "rewrite", "distortion", …) for
	// /qompack:status and the eval harness.
	Breakdown map[string]float64
	// Urgency classifies how strongly this Decision recommends acting now.
	Urgency Urgency
	// TTL is the prompt cache's TTL state at Now.
	TTL TTLState
	// YoungDalySeconds is the Young-Daly optimal checkpoint interval computed for this Decision.
	YoungDalySeconds float64
	// Background lists O3 work the idle controller may run.
	Background []BackgroundTask
	// SoftFloorTokens is Cfg.SoftFloorPct of EffectiveWindow, in tokens.
	SoftFloorTokens core.Tokens
	// HardCeilingTokens is EffectiveWindow minus Cfg.HardCeilingMargin, in tokens.
	HardCeilingTokens core.Tokens
}

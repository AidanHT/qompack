package eval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// Turn is one logged turn of a session: a user message, an assistant message, or a tool result,
// in the order the session actually produced them.
type Turn struct {
	// Index is this turn's 0-based position in Session.Turns.
	Index core.TurnIndex
	// Role names who produced this turn ("user", "assistant", "tool", …).
	Role string
	// TS is this turn's timestamp.
	TS core.UnixMilli
	// Text is this turn's text content.
	Text string
	// ToolCalls lists every tool call this turn made.
	ToolCalls []ToolCall
	// Tokens is this turn's estimated token cost.
	Tokens core.Tokens
}

// ToolCall is one tool invocation within a Turn.
type ToolCall struct {
	// ID is the tool call's identifier.
	ID core.ToolUseID
	// Name is the tool's name.
	Name string
	// Args is the tool call's raw argument payload.
	Args json.RawMessage
	// Result is the tool call's raw result payload.
	Result json.RawMessage
	// Paths lists every file path this tool call touched.
	Paths []string
}

// Session is one logged (or synthesized) conversation, replayed by the Harness against one or
// more Policies.
type Session struct {
	// ID identifies the session.
	ID string
	// Turns is the session's full turn sequence.
	Turns []Turn
	// CompactionAt lists the turn indices at which the real session compacted.
	CompactionAt []core.TurnIndex
	// Meta carries free-form session metadata (project, model, …).
	Meta map[string]string
	// Synthetic reports whether this Session came from Synthesize rather than a recording.
	Synthetic bool
}

// Policy decides what survives a compaction. The eval harness scores every registered Policy
// against the Belady-optimal keep-set to compute FractionOfOPT (§11.1's primary metric).
type Policy interface {
	// Name identifies this policy in a Report.
	Name() string
	// KeepSet decides what survives a compaction at turn at under budget.
	KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}

// KeepSet is one compaction decision: which items survive, and at what cost.
type KeepSet struct {
	// IDs lists the identifiers of every item kept.
	IDs []string
	// Tokens is the total token cost of the kept items.
	Tokens core.Tokens
	// P is the chosen compaction point (see scheduler.Candidate.Pos).
	P int
}

// Run is one full replay of a Policy against a Session: every action taken and every compaction
// decision made, in order.
type Run struct {
	// Policy is the replayed Policy's Name().
	Policy string
	// Session is the replayed Session's ID.
	Session string
	// Branch is "uncompacted" or "compacted".
	Branch string
	// Actions is the full action sequence the replay took.
	Actions []Action
	// Keeps is every KeepSet decision the replay made, in order.
	Keeps []KeepSet
	// PauseMS is the compaction pause, in milliseconds, for each compaction event.
	PauseMS []int
	// ResidualSpan is the residual span, in tokens, after each compaction event.
	ResidualSpan []core.Tokens
	// FirstTurnAfterMS is the latency to the first turn after each compaction event.
	FirstTurnAfterMS []int
}

// Action is one step of a Run: a turn, the tool it used, the paths it touched, and the replay
// harness's decision at that point.
type Action struct {
	// Turn is the turn index this action occurred at.
	Turn core.TurnIndex
	// Tool is the tool used, if any.
	Tool string
	// Paths lists every file path this action touched.
	Paths []string
	// Decision describes what the harness decided at this action.
	Decision string
}

// Divergence measures how far a compacted Run drifted from its uncompacted baseline.
type Divergence struct {
	// FirstDivergenceTurn is the first turn index at which the two runs disagree (§11.2).
	FirstDivergenceTurn int
	// FileSetJaccard is the Jaccard similarity of the two runs' touched-file sets.
	FileSetJaccard float64
	// ToolEditDistance is the edit distance between the two runs' tool-call sequences.
	ToolEditDistance int
	// SameDecision reports whether the two runs reached the same final decision.
	SameDecision bool
	// DecisionPreservation is the fraction of decisions preserved across the two runs.
	DecisionPreservation float64
	// RedundantReads counts reads the compacted run repeated that the uncompacted run did not.
	RedundantReads int
	// ReAttempts counts approaches the compacted run retried that were already eliminated.
	ReAttempts int
}

// Score is one Policy's evaluated performance on one Session.
type Score struct {
	// FractionOfOPT is the primary metric (§11.1): this policy's value as a fraction of the
	// Belady-optimal keep-set's value.
	FractionOfOPT float64
	// Divergence measures drift from the uncompacted baseline.
	Divergence Divergence
	// RewriteTokens is the total rewrite cost this policy incurred (Σ w·(n − p_min)).
	RewriteTokens int
	// RehydrationTokens is the total token cost this policy's rehydrations incurred.
	RehydrationTokens core.Tokens
	// RetrievalHitRate is the fraction of retrieval calls that found what they were looking for.
	RetrievalHitRate float64
	// CompactionPauseMS is the distribution of compaction pauses, in milliseconds.
	CompactionPauseMS Percentiles
	// ResidualSpan is the distribution of residual spans, in tokens.
	ResidualSpan Percentiles
	// FirstTurnAfterMS is the distribution of first-turn-after latencies, in milliseconds.
	FirstTurnAfterMS Percentiles
}

// Percentiles summarizes a distribution at four points.
type Percentiles struct {
	P50, P95, P99, Max float64
}

// Harness is the L7 replay-and-evaluation seam (00-ARCHITECTURE.md §5.18). SP-01 ships every
// method as a stub; SP-02 owns the real implementation.
type Harness interface {
	// Load reads every session file under dir.
	Load(dir string) ([]Session, error)
	// Replay re-applies p's keep-set decisions against s and returns the resulting Run.
	Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
	// Compare computes the Divergence between an uncompacted and a compacted Run of the same
	// session.
	Compare(uncompacted, compacted Run) Divergence
	// Belady computes the optimal keep-set for a compaction at turn at under budget: the ceiling
	// every Policy is scored against.
	Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
	// ScoreRun scores a completed Run against the Belady-optimal keep-set for each of its
	// compaction events.
	ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
	// Report aggregates per-policy scores across sessions into the replay-gate report.
	Report(ctx context.Context, scores map[string][]Score) (Report, error)
}

// ReplayOptions configures one Replay call.
type ReplayOptions struct {
	// K is the number of forked continuation turns to consider (deterministic mode: bounded by
	// the logged session's own length).
	K int
	// Seed makes a Replay reproducible: the same Session, Policy and Seed must produce a
	// byte-identical Run.
	Seed int64
	// Budget is the token budget each compaction decision is made under.
	Budget core.Tokens
	// Deterministic selects deterministic (logged-action-sequence) mode when true, and live
	// (real-model-fork) mode when false. CI only ever sets this true.
	Deterministic bool
}

// Report aggregates every Policy's Score across every replayed Session, and is what the CI
// replay gate reads.
type Report struct {
	// Policies maps each Policy's Name() to its aggregate Score.
	Policies map[string]Score
	// Baseline names the Policy every Regression is measured against.
	Baseline string
	// Regressions lists every metric that moved beyond the §11.3 2% rule.
	Regressions []Regression
	// Sessions is the number of sessions this Report was computed over.
	Sessions int
	// GeneratedAt is when this Report was produced.
	GeneratedAt time.Time
}

// Regression is one metric that moved beyond the §11.3 "no metric may regress by more than 2%"
// rule.
type Regression struct {
	// Metric names the regressed metric.
	Metric string
	// Policy names the Policy the regression was observed on.
	Policy string
	// Baseline is the metric's baseline value.
	Baseline float64
	// Observed is the metric's observed value.
	Observed float64
	// DeltaPct is the percentage change from Baseline to Observed.
	DeltaPct float64
	// Allowed reports whether this regression is within the §11.3 tolerance.
	Allowed bool
}

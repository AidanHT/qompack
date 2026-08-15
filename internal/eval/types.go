package eval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Turn is one logged turn of a session: a user message, an assistant message, or a tool result,
// in the order the session actually produced them.
type Turn struct {
	// Index is this turn's 0-based position in Session.Turns.
	Index core.TurnIndex `json:"index"`
	// Role names who produced this turn ("user", "assistant", "tool", …).
	Role string `json:"role"`
	// TS is this turn's timestamp.
	TS core.UnixMilli `json:"ts"`
	// Text is this turn's text content.
	Text string `json:"text"`
	// ToolCalls lists every tool call this turn made.
	ToolCalls []ToolCall `json:"toolCalls"`
	// Tokens is this turn's estimated token cost.
	Tokens core.Tokens `json:"tokens"`
}

// ToolCall is one tool invocation within a Turn.
type ToolCall struct {
	// ID is the tool call's identifier.
	ID core.ToolUseID `json:"id"`
	// Name is the tool's name.
	Name string `json:"name"`
	// Args is the tool call's raw argument payload.
	Args json.RawMessage `json:"args"`
	// Result is the tool call's raw result payload.
	Result json.RawMessage `json:"result"`
	// Paths lists every file path this tool call touched.
	Paths []string `json:"paths"`
}

// Session is one logged (or synthesized) conversation, replayed by the Harness against one or
// more Policies.
type Session struct {
	// ID identifies the session.
	ID string `json:"id"`
	// Turns is the session's full turn sequence.
	Turns []Turn `json:"turns"`
	// CompactionAt lists the turn indices at which the real session compacted.
	CompactionAt []core.TurnIndex `json:"compactionAt"`
	// Meta carries free-form session metadata (project, model, …).
	Meta map[string]string `json:"meta"`
	// Synthetic reports whether this Session came from Synthesize rather than a recording.
	Synthetic bool `json:"synthetic"`
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
	IDs []string `json:"ids"`
	// Tokens is the total token cost of the kept items.
	Tokens core.Tokens `json:"tokens"`
	// P is the chosen compaction point (see scheduler.Candidate.Pos).
	P int `json:"p"`
}

// Run is one full replay of a Policy against a Session: every action taken and every compaction
// decision made, in order.
//
// The first eight fields are 00-ARCHITECTURE.md §5.18's field set, unchanged. The five after them
// are SP-02 additions, appended because Compare and ScoreRun are handed a Run and no Session and
// therefore cannot otherwise know the compaction turns, the horizon, the per-event demand sets or
// n_i — without which the two most important functions in the package are unimplementable as
// specified. Appending is not a §0 amendment: nothing §5.18 lists is renamed, retyped, reordered
// or removed, and Run is constructed and read entirely inside internal/eval and test/replay.
type Run struct {
	// Policy is the replayed Policy's Name().
	Policy string `json:"policy"`
	// Session is the replayed Session's ID.
	Session string `json:"session"`
	// Branch is "uncompacted" or "compacted".
	Branch string `json:"branch"`
	// Actions is the full action sequence the replay took.
	Actions []Action `json:"actions"`
	// Keeps is every KeepSet decision the replay made, in order.
	Keeps []KeepSet `json:"keeps"`
	// PauseMS is the compaction pause, in milliseconds, for each compaction event.
	PauseMS []int `json:"pauseMs"`
	// ResidualSpan is the residual span, in tokens, after each compaction event.
	ResidualSpan []core.Tokens `json:"residualSpan"`
	// FirstTurnAfterMS is the latency to the first turn after each compaction event.
	FirstTurnAfterMS []int `json:"firstTurnAfterMs"`

	// At is s.CompactionAt in ascending order: At[i] is compaction event i's turn. At, Demands,
	// PrefixTokens and Keeps are parallel — index i names the same event in all four.
	At []core.TurnIndex `json:"at"`
	// Demands[i] is Demands(s, At[i], min(At[i]+Horizon, len(s.Turns))): what the session went on
	// to need after event i. ScoreRun scores both the policy's and OPT's keep-set against this
	// exact set rather than recomputing it, so the two sides are always graded on the same demand.
	Demands [][]Demand `json:"demands"`
	// PrefixTokens[i] is n_i: the position-advancing tokens in Blocks(s, At[i]), which is the n of
	// §5.2's cost = w·(n − p_min).
	PrefixTokens []core.Tokens `json:"prefixTokens"`
	// FirstCompactionTurn is At[0], or -1 when the session never compacted. Compare reads it from
	// its compacted argument, so a baseline branch needs no horizon of its own.
	FirstCompactionTurn core.TurnIndex `json:"firstCompactionTurn"`
	// Horizon is the effective K this run was built with (§4.2's "next K actions").
	Horizon int `json:"horizon"`
}

// Action is one step of a Run: a turn, the tool it used, the paths it touched, and the replay
// harness's decision at that point.
type Action struct {
	// Turn is the turn index this action occurred at.
	Turn core.TurnIndex `json:"turn"`
	// Tool is the tool used, if any.
	Tool string `json:"tool"`
	// Paths lists every file path this action touched.
	Paths []string `json:"paths"`
	// Decision describes what the harness decided at this action.
	Decision string `json:"decision"`
}

// Divergence measures how far a compacted Run drifted from its uncompacted baseline: §4.2's five
// bullets, which together are the empirical estimator of D.
type Divergence struct {
	// FirstDivergenceTurn is how many turns after the compaction the two branches first disagree,
	// and equals the horizon K when they never do. It is DirHigherBetter, so "never diverged" must
	// be the largest value the metric can take; -1 is never emitted.
	FirstDivergenceTurn int `json:"firstDivergenceTurn"`
	// FileSetJaccard is the Jaccard similarity of the two runs' touched-file sets.
	FileSetJaccard float64 `json:"fileSetJaccard"`
	// ToolEditDistance is the edit distance between the two runs' tool-call sequences.
	ToolEditDistance int `json:"toolEditDistance"`
	// SameDecision reports whether the two runs reached the same final decision.
	SameDecision bool `json:"sameDecision"`
	// DecisionPreservation is the fraction of decisions preserved across the two runs.
	DecisionPreservation float64 `json:"decisionPreservation"`
	// RedundantReads counts reads the compacted run repeated that the uncompacted run did not. It
	// may be negative, which means the policy prevented re-reads and is an improvement.
	RedundantReads int `json:"redundantReads"`
	// ReAttempts counts approaches the compacted run retried that were already eliminated.
	ReAttempts int `json:"reAttempts"`
}

// Score is one Policy's evaluated performance on one Session.
type Score struct {
	// FractionOfOPT is the primary metric (§11.1): this policy's value as a fraction of the
	// Belady-optimal keep-set's value.
	FractionOfOPT float64 `json:"fractionOfOpt"`
	// Divergence measures drift from the uncompacted baseline.
	Divergence Divergence `json:"divergence"`
	// RewriteTokens is the total rewrite cost this policy incurred (Σ w·(n − p_min)).
	RewriteTokens int `json:"rewriteTokens"`
	// RehydrationTokens is the total token cost this policy's rehydrations incurred.
	RehydrationTokens core.Tokens `json:"rehydrationTokens"`
	// RetrievalHitRate is the fraction of retrieval calls that found what they were looking for.
	RetrievalHitRate float64 `json:"retrievalHitRate"`
	// CompactionPauseMS is the distribution of compaction pauses, in milliseconds.
	CompactionPauseMS Percentiles `json:"compactionPauseMs"`
	// ResidualSpan is the distribution of residual spans, in tokens.
	ResidualSpan Percentiles `json:"residualSpan"`
	// FirstTurnAfterMS is the distribution of first-turn-after latencies, in milliseconds.
	FirstTurnAfterMS Percentiles `json:"firstTurnAfterMs"`
}

// Percentiles summarizes a distribution at four points.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// Harness is the L7 replay-and-evaluation seam (00-ARCHITECTURE.md §5.18).
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
//
// Report carries exactly §5.18's five fields. Everything the driver additionally discloses — the
// corpus tier and hash, the "latency": "modelled" tag, budget violations, no-demand sessions, the
// breakpoint plan, the growth result — lives on test/replay's DriverReport envelope instead, so
// this type stays the shape §5.18 fixed.
type Report struct {
	// Policies maps each Policy's Name() to its aggregate Score.
	Policies map[string]Score `json:"policies"`
	// Baseline names the Policy every Regression is measured against.
	Baseline string `json:"baseline"`
	// Regressions lists every metric that moved beyond the §11.3 2% rule. Report always leaves it
	// empty: comparison needs a previous report, which eval is never given, so filling it is the
	// gate's job.
	Regressions []Regression `json:"regressions"`
	// Sessions is the number of sessions this Report was computed over.
	Sessions int `json:"sessions"`
	// GeneratedAt is when this Report was produced.
	GeneratedAt time.Time `json:"generatedAt"`
}

// Regression is one metric that moved beyond the §11.3 "no metric may regress by more than 2%"
// rule.
type Regression struct {
	// Metric names the regressed metric.
	Metric string `json:"metric"`
	// Policy names the Policy the regression was observed on.
	Policy string `json:"policy"`
	// Baseline is the metric's baseline value.
	Baseline float64 `json:"baseline"`
	// Observed is the metric's observed value.
	Observed float64 `json:"observed"`
	// DeltaPct is the percentage change from Baseline to Observed.
	DeltaPct float64 `json:"deltaPct"`
	// Allowed reports whether this regression carries a matching sign-off trailer.
	Allowed bool `json:"allowed"`
}

// ── SP-02 additions ─────────────────────────────────────────────────────────────────────────
//
// Everything below is new in SP-02. eval is SP-02's package, so adding to it is not an amendment:
// nothing §5.18 declares changes.

// Options configures a Harness constructed by New. It is not part of §5.18's normative type set —
// Harness is the seam, not its constructor — so extending it is not an amendment.
//
// Every member's zero value is replaced by a safe default in New, so eval.New(eval.Options{}) is
// always valid and a test never has to pre-fill anything it does not care about.
type Options struct {
	// Cfg is the configuration in force for the harness's replay and scoring decisions. Zero →
	// config.Defaults(). It is where w and r come from at every use site (D11, §11.6).
	Cfg config.Config
	// Log receives the Loud diagnostics a dishonest number would otherwise hide: a policy that
	// overran its budget, a missing OPT entry. Zero → logging.Nop().
	Log logging.Logger
	// Metrics records eval.belady.ms, eval.replay.ms, eval.score.ms and eval.report.ms. Zero → a
	// registry over Clock. This is the only use this package makes of obs.
	Metrics obs.Registry
	// Clock stamps Report.GeneratedAt. Zero → core.SystemClock().
	Clock core.Clock
	// Latency is the model that turns a residual span into a pause. Zero → DefaultLatencyModel(),
	// detected by Latency.Modelled == false, which the default never is.
	Latency LatencyModel
}

// harness is the concrete Harness. It is a pointer receiver throughout because ScoreRun
// accumulates raw latency samples into pool, which Report then reduces — the one piece of state
// in the package, and the reason Report is a deterministic function of the ScoreRun calls that
// preceded it rather than of a Score's already-reduced Percentiles.
type harness struct {
	cfg     config.Config
	log     logging.Logger
	metrics obs.Registry
	clock   core.Clock
	lat     LatencyModel
	live    LiveRunner
	pool    map[string]latencyPool
}

// latencyPool is one policy's raw, unreduced latency samples, concatenated across every session
// ScoreRun has seen. An average of P95s is not a P95, so Report recomputes from these.
type latencyPool struct {
	pause     []float64
	residual  []float64
	firstTurn []float64
}

// LiveRunner is the §6.3-tier-3 seam: the thing that would re-execute a fork against a real
// model. It is nil in every build SP-02 ships, and Replay refuses rather than silently degrading
// to deterministic mode, so a CI run can never be mistaken for a model-backed one. SP-17's
// pre-release run supplies an implementation; no model call is written in this subplan.
type LiveRunner interface {
	// Fork re-executes the k actions following at, given what the compaction kept.
	Fork(ctx context.Context, s Session, at core.TurnIndex, keep KeepSet, k int) ([]Action, error)
}

// LatencyModel turns a residual span and a rehydration size into the three §11.2 latency metrics.
//
// It is a model, not a measurement: nothing in deterministic mode calls an API, so there is no
// wall-clock to report. Modelled is therefore always true here, and the driver stamps
// "latency": "modelled" on every report that carries these numbers. The coefficients are
// calibrated against two sentences of the design and a unit test names them both: at the §2.5
// stock residual of 167_000 tokens the model yields 28_050 ms, inside §6.7's "~15–40s" band; at
// the §8.5 post-O5 residual of 10–20K it yields 4.5–6.0 s.
type LatencyModel struct {
	// PauseBaseMS is the fixed cost of a summarization round-trip.
	PauseBaseMS float64
	// PausePerKResidualMS is the marginal pause cost per 1_000 residual tokens.
	PausePerKResidualMS float64
	// FirstTurnBaseMS is the fixed cost of the turn following a compaction.
	FirstTurnBaseMS float64
	// FirstTurnPerKRehydrMS is the marginal first-turn cost per 1_000 rehydrated tokens.
	FirstTurnPerKRehydrMS float64
	// Modelled records that these numbers were computed, never observed. It is what distinguishes
	// a configured LatencyModel from the zero value, and it is never false in a built harness.
	Modelled bool
}

// Turn roles. A turn that is neither is treated as an assistant turn, because every non-user
// turn occupies the assistant side of the context window regardless of what produced it.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// Run.Branch values.
const (
	branchUncompacted = "uncompacted"
	branchCompacted   = "compacted"
)

// DefaultKeepBudget is the equal token budget every policy decides under, and is §2.3's
// maxTokens: the host's own preservation cap.
const DefaultKeepBudget core.Tokens = 40000

// DefaultHorizonK is §4.2's K: how many turns after a compaction divergence is measured over.
const DefaultHorizonK int = 20

// BlockKind classifies a Block by what it is, which is what decides whether it advances a
// position (see Blocks).
type BlockKind uint8

// The six block kinds. The first two are prefix content — the bytes that actually occupy the
// context window. The last four are derived retention units extracted from content already
// counted, which is why they never advance a position.
const (
	// BlockToolResult is one tool call's result payload.
	BlockToolResult BlockKind = iota
	// BlockUserPrompt is one user turn's message.
	BlockUserPrompt
	// BlockAssistant is one assistant turn's message.
	BlockAssistant
	// BlockFile is a file's content, aliasing the tool result that produced it.
	BlockFile
	// BlockElimination is one recorded "this approach does not work".
	BlockElimination
	// BlockDecision is one recorded decision.
	BlockDecision
)

// Block is one keepable unit of a session's prefix at a compaction point.
//
// Pos and Tokens are deliberately not the same accumulator. Pos measures the original prefix and
// advances only on BlockUserPrompt / BlockAssistant / BlockToolResult, so p_min is always a real
// position in it and nothing is counted into a position twice. Tokens is what restoring this
// block costs, derived or not, so a policy that keeps both a tool result and the file block it
// produced legitimately pays twice: the budget is a retention budget, not a prefix measurement.
type Block struct {
	// ID is this block's stable identifier (see the ID grammar in blocks.go).
	ID string `json:"id"`
	// Kind classifies the block.
	Kind BlockKind `json:"kind"`
	// Turn is the turn this block came from.
	Turn core.TurnIndex `json:"turn"`
	// Pos is this block's token offset into the original prefix.
	Pos int `json:"pos"`
	// Tokens is what keeping this block costs.
	Tokens core.Tokens `json:"tokens"`
	// Paths lists the file paths this block concerns.
	Paths []string `json:"paths,omitempty"`
	// Symbols lists the symbols this block concerns.
	Symbols []string `json:"symbols,omitempty"`
	// Ephemeral marks a block whose value expires with the turn that produced it.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// DemandKind classifies why a block was needed after a compaction.
type DemandKind uint8

// The four demand kinds.
const (
	// DemandFileContent is a re-touch of a file whose content was already in the prefix.
	DemandFileContent DemandKind = iota
	// DemandToolResult is a reference to a tool result by its tool_use_id.
	DemandToolResult
	// DemandDecision is a reference to a decision minted before the compaction.
	DemandDecision
	// DemandElimination is a re-attempt of an approach already eliminated.
	DemandElimination
)

// Demand is one "this block was needed after the compaction" event. Demands are the only notion
// of need in this package: FractionOfOPT counts them, and both the policy's keep-set and OPT's
// are scored against the same set.
type Demand struct {
	// Turn is the turn that needed the block.
	Turn core.TurnIndex `json:"turn"`
	// BlockID names the block that was needed.
	BlockID string `json:"blockId"`
	// Kind classifies the need.
	Kind DemandKind `json:"kind"`
}

// OPTDetail is what BeladyDetail knows about the keep-set it just computed, and that
// Harness.Belady discards.
type OPTDetail struct {
	// Value is the number of demands the OPT keep-set satisfies: the denominator of FractionOfOPT.
	Value int `json:"value"`
	// Exact is false when the DP was too large and the 1/2-approximation fallback ran, which the
	// gate reports as a WARN naming the session.
	Exact bool `json:"exact"`
	// Candidates is how many blocks survived the value == 0 pruning.
	Candidates int `json:"candidates"`
	// BudgetTokens echoes the budget the instance was solved under.
	BudgetTokens core.Tokens `json:"budgetTokens"`
	// DPCells is the size of the table the exact solver would need.
	DPCells int `json:"dpCells"`
}

// BeladyOptions tunes the retrospective OPT computation.
type BeladyOptions struct {
	// K is the horizon demands are collected over.
	K int
	// Granularity is the token quantum weights are scaled by. Rounding is upward, so the returned
	// keep-set never exceeds the true budget.
	Granularity core.Tokens
	// MaxDPCells caps the exact solver; beyond it the 1/2-approximation runs instead.
	MaxDPCells int
}

// BreakpointPlan is the §5.6 optimal cache_control breakpoint placement.
//
// It is measurement-and-port material, never a feature: §5.6 and §12 both state that Claude Code
// manages its own cache markers and a plugin cannot move them. Note always carries that sentence
// so a number from this type can never read as something Qompack does.
type BreakpointPlan struct {
	// Positions is the chosen marker set, ascending. It may be shorter than Markers.
	Positions []int `json:"positions"`
	// CachedReads is value(B) of the chosen marker set.
	CachedReads int64 `json:"cachedReads"`
	// Markers is the marker budget the caller asked for.
	Markers int `json:"markers"`
	// Candidates is how many candidate positions survived the 256-cap stride.
	Candidates int `json:"candidates"`
	// Note is always NotPluginActionable.
	Note string `json:"note"`
}

// NotPluginActionable is the disclaimer every BreakpointPlan carries and the gate prints on the
// line above the number.
const NotPluginActionable = "measurement only: §5.6/§12 — Claude Code manages its own " +
	"cache_control markers; a plugin cannot place or move them"

// Direction says which way a metric is better, which is what makes the §11.3 2% rule computable
// without a per-metric special case in the gate.
type Direction uint8

const (
	// DirHigherBetter marks a metric a policy wants to maximize.
	DirHigherBetter Direction = iota
	// DirLowerBetter marks a metric a policy wants to minimize.
	DirLowerBetter
)

// NamedSpec is one row of the committed corpus table: the shape name SynthSpec has no room for,
// plus the seed and spec Synthesize takes.
type NamedSpec struct {
	// File is the basename under testdata/sessions/synthetic/.
	File string `json:"file"`
	// Shape names the failure mode this session spans.
	Shape string `json:"shape"`
	// Seed is the generator seed.
	Seed int64 `json:"seed"`
	// Spec is the generator input.
	Spec SynthSpec `json:"spec"`
}

// StatsSample is one observation of store growth, shaped exactly like the store.Stats fields the
// guardrail needs. It is declared here, rather than imported, because eval is foundation-only
// (§3.2) and store lands in a later wave: the driver is a composition root and adapts.
type StatsSample struct {
	// Turn is the session length at which this sample was taken.
	Turn core.TurnIndex `json:"turn"`
	// Objects is the number of stored objects.
	Objects int `json:"objects"`
	// Bytes is the stored size after dedup.
	Bytes int64 `json:"bytes"`
	// RawBytes is the content size before dedup.
	RawBytes int64 `json:"rawBytes"`
	// DedupRatio is RawBytes/Bytes as the store reported it.
	DedupRatio float64 `json:"dedupRatio"`
}

// GrowthResult is the verdict on §11.3's "store growth sublinear in session length after dedup".
type GrowthResult struct {
	// Exponent is α in ln(Bytes) = α·ln(RawBytes) + c.
	Exponent float64 `json:"exponent"`
	// Samples is how many usable samples the fit ran over.
	Samples int `json:"samples"`
	// RawSpan is max/min RawBytes: how much dynamic range the fit had.
	RawSpan float64 `json:"rawSpan"`
	// Sublinear is the verdict.
	Sublinear bool `json:"sublinear"`
	// Reason explains an inconclusive verdict, which the gate fails on: an unmeasurable guardrail
	// is not a passing guardrail.
	Reason string `json:"reason"`
}

// SketchHealth is the §11.4 bloom watch-for, shaped like the negknow health numbers SP-09 will
// supply through the same provider seam.
type SketchHealth struct {
	// FillRatio is how full the filter is.
	FillRatio float64 `json:"fillRatio"`
	// EstFPRate is the estimated false-positive rate. §11.4: at 1% they are safe; at 10% the agent
	// starts skipping viable approaches.
	EstFPRate float64 `json:"estFpRate"`
	// Records is how many records the filter holds.
	Records int `json:"records"`
	// Active is how many are still live.
	Active int `json:"active"`
	// Stale is how many have gone stale.
	Stale int `json:"stale"`
}

// ImportOptions configures Import.
type ImportOptions struct {
	// From is the transcript root. Empty → ~/.claude/projects.
	From string
	// To is the destination. Empty → $QOMPACK_SESSIONS_DIR; both empty is an error naming the
	// variable, and a destination inside the repository working tree is refused outright.
	To string
	// Limit caps how many sessions are imported. Zero means no limit.
	Limit int
	// Redact scrubs secrets, home paths and emails. It is on by default at the command layer, and
	// turning it off requires a second environment variable.
	Redact bool
	// Getenv is injected so a test never mutates the process environment. Nil → os.Getenv.
	Getenv func(string) string
}

// ImportReport is what Import did.
type ImportReport struct {
	// Files is how many transcript files were read.
	Files int `json:"files"`
	// Sessions is how many sessions were written.
	Sessions int `json:"sessions"`
	// Turns is how many turns those sessions hold.
	Turns int `json:"turns"`
	// RedactedSpans is how many spans Redact replaced.
	RedactedSpans int `json:"redactedSpans"`
	// Skipped names every record type that was counted and ignored rather than failing the import.
	Skipped []string `json:"skipped"`
}

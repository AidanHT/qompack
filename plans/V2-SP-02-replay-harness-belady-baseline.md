# SP-02: Phase 0 / L7: replay harness, counterfactual divergence metrics, Belady OPT, stock baseline, and the full evaluation methodology

> **Recommended model: Opus 5 · max effort**
>
> Belady-OPT under an equal *token* budget is a knapsack, not plain furthest-in-future, and every later wave is graded against the number this package emits — a subtly wrong harness silently poisons all six checkpoints, so correctness outranks cost here. Volume is moderate (1.5k lines), so `max` is affordable.

**Branch:** `feat/sp02-replay-harness-belady-baseline` (cut from `develop`) | **Wave:** 1 | **Prerequisites:** the branches of `["SP-01"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 1 (SP-03 sketches, SP-04 chunking/canon/symbols, SP-05 daemon/IPC, SP-06 store, SP-07 dag) | **Design sections:** §4.2, §5.6 (Belady breakpoint extension), §6.10, §8.8, §10 Phase 0, §11.1, §11.2, §11.3, §11.4 | **Gaps closed:** G8.1, G8.3

> **This subplan has shipped. Before verifying it, read [`V2-SP02-handoff.md`](V2-SP02-handoff.md).**
>
> Six of `V2-VERIFY` §2.2's twenty SP-02 rows do not match what shipped. Two of them **exit 0 having
> run nothing** (V2-SP02-15, V2-SP02-17) and one (V2-SP02-12) would delete working code if followed
> literally. The handoff gives the corrected command for each, plus the wave-1 coverage-floor action
> in its §4.1 that silently unmeasures SP-03 … SP-07 if it is missed.

---

## Mission

This subplan builds the measurement loop, and it is the first thing built after the foundation because `Qompack.md` §10 Phase 0 says *"Why first: you cannot tune anything without it, and every subsequent phase needs a regression signal"* and the closing note ranks it #1: *"Phase 0. Without measurement, everything else is opinion."* Qompack's whole thesis (§1.3 RC-3) is that the stock system is unmeasured. If SP-02 ships a harness that flatters the plugin, the project reproduces the exact sin it indicts. Every number this package emits must therefore be reproducible bit-for-bit from committed inputs, and every number that is *modelled* rather than *observed* must say so in its own output.

Concretely, you deliver `internal/eval` in full: a session model, a store-independent counterfactual replay harness that forks a logged session at each of its compaction points, the five divergence metrics of §4.2, retrospective Belady OPT keep-set computation (§6.10) and fraction-of-OPT scoring (§11.1) under an equal token budget, every secondary metric of §11.2 including the three latency metrics added in v1.2, the §5.6 optimal-breakpoint measurement (reported and explicitly labelled *not plugin-actionable*), the deterministic `eval.Synthesize` generator plus a committed 24-session synthetic corpus, a redacting importer for out-of-repo recorded transcripts, and the `test/replay` driver that backs the CI `replay-gate` job — the 2% no-regression rule with its sign-off trailer, the per-phase exit-criterion assertions, the sublinear-store-growth guardrail, and the §11.4 watch-for instrumentation.

`internal/eval` imports **foundation packages only** (`core`, `paths`, `config`, `logging`, `obs`) per 00-ARCHITECTURE §3.2. That is a deliberate architectural choice, not an accident: it is what lets the evaluation layer be built in the earliest parallel wave, against no sibling's output, and still supply a regression signal to every later phase. Where SP-02 needs data that only a later package can produce (`store.Stats` for the growth guardrail, `negknow.Health` for the bloom FP rate), it defines a *provider interface in `eval`* with the same field shape and consumes it from the `test/replay` driver, which is a composition root and may import anything.

**Gap closure — what in this file actually closes G8.1 and G8.3.** Both are §9-matrix rows and neither is closed by "the package exists"; each closes at a named artifact:

| Gap | §3 text | Closed by, concretely |
|---|---|---|
| **G8.1** — *"No diff, no retention score, no drop report. First signal of a bad compaction is behavioural degradation noticed several turns later."* | §9 row: "L7 replay harness; `/qompack:status`" | The **retention score** is `Score.FractionOfOPT` (§8 of the implementation spec) computed against `BeladyDetail`'s ceiling, and the **diff** is `Divergence` (`Compare`), which is the literal turn-by-turn diff between the compacted and uncompacted branches. Both become a *signal before the fact* rather than after it because `test/replay` runs them as a required CI check on every PR. (The `/qompack:status` half of the §9 row is SP-14's; the drop report is SP-11's.) |
| **G8.3** — *"No feedback on `/compact <instructions>`. The only steering lever is applied at the moment of compaction, not as a standing preference, and nothing reports whether it worked."* | §9 row: "L7 fraction-of-OPT metric" | `eval.Policy` + `RegisterPolicy` make *any* steering strategy — including a `/compact`-instruction variant — a named policy with a comparable number, and the 2% rule with its sign-off trailer turns "whether it worked" into a merge-blocking answer. `oracle` supplies the ceiling and `null` the floor, so a reported number is bounded on both sides rather than free-floating. |

A Done-checklist item asserts both rows: every gap ID in this subplan's header is traceable to a test that fails if the artifact above is absent.

**Corpus tier and the word "real" in the Phase 0 exit criterion.** §10 Phase 0 says *"reproducible across at least 20 **real** sessions"*, and the committed `testdata/baseline/phase0.json` is computed over **24 synthetic** sessions. That substitution is deliberate, is 00-ARCHITECTURE §6.3's ruling (*"This corpus is what CI runs"*, tier 2 *"Gates releases (§8), not PRs"*), and is declared in the artifact itself: `phase0.json` carries `"corpusTier": "synthetic"`. The real-session number is produced by the same driver over an imported recorded corpus (`qompack eval import` → `$QOMPACK_SESSIONS_DIR` → `go run ./test/replay --corpus $QOMPACK_SESSIONS_DIR --write-baseline testdata/baseline/phase0-recorded.json`); the recorded transcripts are never committed but the resulting *number* is, with `"corpusTier": "recorded"` and the session count. Producing it requires ≥ 20 recorded sessions on the operator's machine and is therefore a documented, scheduled step in `docs/adr/0002-replay-methodology.md`, not a CI step. Anything that reports a synthetic number as if it were a real-session number is exactly the dishonest measurement §1.3 RC-3 indicts; the `corpusTier` key exists so that can never happen silently.

**What exists when you start (post-SP-01 `develop`):** the Go 1.26 module `github.com/qompack/qompack`; `internal/core` (`Hash`, `SessionID`, `ToolUseID`, `TurnIndex`, `Tokens`, `UnixMilli`, `Clock`, `SystemClock`, `ChunkRef`, `Dep`, the sentinel errors), `internal/paths` (`Norm`, `Key`, `WriteAtomic`, `AppendOnly`, `CreateNew`), `internal/config` (the whole Appendix C schema plus the §11.5 `runtime` namespace, `Defaults`, `Load`, `Validate`, provenance), `internal/logging`, `internal/obs`, `internal/tokens` (baseline estimator), `internal/testutil` (`Project`, `FakeClock`, golden helpers), a compiling `internal/eval` stub whose every function returns `core.ErrNotImplemented`, the `evaltest` conformance suite with its behaviour tests `t.Skip`ped, the `test/e2e` scaffolding, `tools/devtool` with a registered-but-unimplemented `replay` task, and a CI pipeline whose `replay-gate` job is wired but not required.

**What exists when you finish:** `internal/eval` fully implemented with every `evaltest` skip flipped off; 24 deterministic synthetic sessions committed under `testdata/sessions/synthetic/`; `testdata/baseline/phase0.json` carrying a single reproducible fraction-of-OPT number for stock behaviour across 24 sessions (≥ the `eval.minSessions: 20` floor); `test/replay` and `devtool replay` green; and `replay-gate` promoted to a required check on `develop` at the end of wave 1.

---

## Design context (verbatim from `Qompack.md`)

Everything below is quoted verbatim. Nothing in this subplan requires opening the design document.

### §4.2 — Making distortion measurable (the whole basis of this package)

> Clean logprobs over action sequences are not available through the Messages API. The empirical estimator is, and it is the highest-leverage single component in this plan:
>
> **Counterfactual replay.** Take real logged sessions. Fork at turn *t*. Run one branch uncompacted and one compacted. Measure divergence over the next *K* actions:
>
> - Jaccard distance over the set of files touched
> - Edit distance over the tool-call sequence
> - Turn index of first divergence
> - Whether the same decision was reached
> - Redundant work: re-reads of files already read, re-attempts of eliminated approaches
>
> This is an unbiased estimate of `D`. Without it, every other idea in this document is untested intuition — which is why it is Phase 0 rather than Phase 6.

### §4.1 — the distortion being estimated

> ```
> D = KL( P(a | X) ‖ P(a | T) )
> ```
>
> How much the agent's next-action distribution moves when the transcript is swapped for the summary.

### §6.10 — Belady's OPT as evaluation ceiling

> **Closes:** G8.1, G8.3
>
> Belady's algorithm is clairvoyant and unimplementable online — but logged sessions make it computable *retrospectively*. Replay a session, observe what was actually needed after each compaction, compute the optimal keep-set. That is the upper bound no online policy can beat.
>
> Now every policy has a number: **fraction of OPT achieved.** Without it you are tuning constants by vibes; with it you can tell whether slicing beats recency by 4% or 40%.

### §5.6 — the Belady breakpoint extension (measurement only)

> > **Scope note.** The first item below — breakpoint placement — is **not plugin-actionable**: Claude Code manages its own `cache_control` markers and a plugin cannot move them. The analysis is retained because it applies verbatim if Qompack is later ported to a first-party harness on the Messages API (§2.8), and because the Belady extension makes it measurable today. It is listed in the §12 "cannot do" inventory.
>
> **Breakpoint placement as optimal stopping.** Given a growing prefix and a limited number of markers, place them to maximize expected reads before invalidation. If the hazard rate of invalidation at each position is estimable, optimal placement puts breakpoints *just before* high-hazard regions, so an edit there does not cost the stable content preceding it. Concretely: one after the system prompt, one after the stable startup block, one after the last compaction summary.
>
> **Belady extends to breakpoints.** The retrospective replay harness can compute optimal *breakpoint placement* as well as optimal keep-sets — same clairvoyant setup, different decision variable.

And from §12, "What this plugin cannot do":

> - **Cannot place or move `cache_control` breakpoints.** Claude Code manages its own cache markers, so the breakpoint-placement analysis in §5.6 is measurement-and-port material, not a plugin feature.

### §8.8 — L7 Evaluation harness

> Offline, not part of the hot path. See §11.

### §10 Phase 0 — Measurement (do this first)

> **Why first:** you cannot tune anything without it, and every subsequent phase needs a regression signal.
>
> - Session replay harness: fork logged sessions at compaction points
> - Counterfactual divergence metrics (§4.2)
> - Belady OPT computation over logged sessions (§6.10)
> - Baseline the current system: what fraction of OPT does stock compaction achieve?
>
> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

### §11.1 — Primary metric

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

### §11.2 — Secondary metrics (table verbatim)

> | Metric | Definition |
> |---|---|
> | First-divergence turn | Turns after compaction before compacted and uncompacted branches diverge |
> | File-set Jaccard | Overlap of files touched over the next K turns |
> | Redundant work rate | Re-reads of already-read files; re-attempts of eliminated approaches |
> | Decision preservation | Fraction of pre-compaction decisions still correctly recalled |
> | Rewrite tokens/session | Total `w · (n − p_min)` paid |
> | Rehydration budget | Tokens spent restoring context |
> | Retrieval hit rate | How often `expand`/`re_read` is called, and whether it prevented a re-read |
> | **Compaction pause** | Wall-clock of the summarization call; target O(delta) under frontier advancement (O5) |
> | **Residual span at compaction** | Tokens between frontier N and the compaction point — the direct driver of pause time |
> | **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### §11.3 — Guardrails

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §11.4 — Watch for

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### Constants the stock-behaviour policy must model

§2.3, Session Memory Compact preservation config:

> Preservation config: `minTokens: 10_000`, `minTextBlockMessages: 5`, `maxTokens: 40_000`. Expansion runs backwards from the last summarized message until both minimums are met, capped at `maxTokens`.

§2.4, step 7 of the Full Compact pipeline:

> ```
> 7.  Restore post-compact context:
>       • top 5 recently-read files (50K budget, 5K/file)
>       • invoked skills (25K budget, 5K/skill)
> ```

§2.5, trigger arithmetic:

> ```
> effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
> autoCompactThreshold   = effectiveContextWindow − 13_000
> ```
>
> For a 200K model: effective ≈ 180K, threshold ≈ 167K.

§2.2, on stock token estimation (the coarseness SP-06 later fixes; the harness must reproduce it for the *stock* policy only):

> Token estimation pads by 4/3 and flat-rates images and PDFs at 2,000 tokens.

### Cache cost model used by `rewrite tokens/session`

§5.1 and Appendix A:

> ```
> rebuild cost = w · (n − p)
> forfeited discount = (1 − r) · (n − p)
> ```
>
> Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing before tuning**, since the ratio drives several thresholds below.

> ```
> cost = w · (n − p_min)     where p_min = position of the earliest dropped block
> ```

§5.2, the scenario table this harness must be able to reproduce numerically:

> | Scenario | Dropped | p_min | Rewrite | Verdict |
> |---|---|---|---|---|
> | A | 60K tokens | 150K | 17K | Good |
> | B | 2K tokens | 10K | 157K | 30× the cost, 1/30 the benefit |

### Latency facts the pause model is calibrated against

§6.7:

> Map `δ` to compaction cost (tokens plus latency; the summarization call at 167K input runs ~15–40s) and `M` to expected time until forced compaction at the current burn rate.

§8.5 (O5):

> When compaction fires, the O1 instruction restricts the summarizer to turns after `N` — and that residual span is now 10–20K tokens rather than 150K, **regardless of how long the session has run**. Short novel prefill, short decode, every time.

### Appendix C — the `eval` configuration block, verbatim

> ```jsonc
>   "eval": { "replayOnPhaseGate": true, "minSessions": 20 }
> ```

and the cache multipliers this package reads rather than hardcodes (D11 / §11.6):

> ```jsonc
>     "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
> ```

### §7.4 — the eval directory in the runtime store

> ```
> └── eval/
>     ├── replay/                    # counterfactual fork logs
>     └── opt/                       # Belady keep-sets
> ```

---

## Out of scope

Implement none of the following. Each names its owner.

| Excluded | Owner |
|---|---|
| `internal/config`, `internal/core`, `internal/paths`, `internal/logging`, `internal/obs`, `internal/testutil`, the `evaltest` conformance-suite *skeleton*, the CI pipeline files, the `devtool` task registry | **SP-01** |
| Any implementation of `sketch.Bloom` (including `EstimatedFPRate`) — SP-02 only *consumes* a fill-ratio number through a provider interface | **SP-03** |
| FastCDC, canonicalizers, MinHash, `symbols` — synthetic sessions carry pre-computed token counts, never real chunking | **SP-04** |
| The daemon, IPC, hot-path budgets B-A/B-B/B-D, `bench-hotpath`, the `contract` monitor | **SP-05** |
| `store.Store`, `store.Stats`, real dedup ratios, GC, `internal/redact` (SP-02's `eval.Redact` scrubs *transcripts on import*; it is not the ingest-choke-point redactor) | **SP-06** |
| `dag.Graph`, `BackwardSlice`, `CrossingEdges` — the harness never slices | **SP-07** |
| The real observer, tombstones, supersession, the Phase 1 exit criterion (store:raw ≥ 4:1) | **SP-08** |
| `negknow.Ledger`, descriptors, staleness, bloom-as-cache. SP-02 models eliminations *inside a session log only* | **SP-09** |
| `checkpoint` schema, `ExtractDecisions`, focus instructions | **SP-10** |
| `rehydrate`, the 8-item injection, the drop report, the 8–12K budget | **SP-11** |
| `scheduler.Evaluate`, BOCD, Young–Daly, real p-selection. SP-02 defines the *scoring* of a `p`, never the choosing of one at runtime | **SP-12** |
| The MCP server and its eight tools | **SP-13** |
| `/qompack:eval` slash command, `internal/commands`, `--json` command output. SP-02 owns only the `qompack eval import` verb | **SP-14** |
| `analyzer`, submodular selection, Sequitur. Later subplans register their policies into `eval`'s registry from `test/replay`; SP-02 never implements them | **SP-15** |
| Phase 7 refinements, cross-session warm start, promotion | **SP-16** |
| Release packaging, cross-platform matrix, `fsck`/`doctor` | **SP-17** |
| `docs/user-guide.md`, `docs/cannot-do.md`. SP-02 writes exactly two docs files: `docs/adr/0002-replay-methodology.md` and `docs/adr/0003-replay-overfit-recollection.md` | **SP-18** |

**Explicitly not built, ever, by anyone:** actual `cache_control` breakpoint placement. §5.6 and §12 both state it is not plugin-actionable. SP-02 computes the optimal placement as a *number in a report*, labelled as such.

---

## Interface contract

### Consumes (exact signatures from 00-ARCHITECTURE.md)

```go
// internal/core (§4)
type Hash [32]byte
func (h Hash) String() string
func (h Hash) Short() string
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type Tokens int
type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
var ErrNotImplemented, ErrNotFound, ErrBudget error

// internal/config (§5.1, §11.1, §11.5)
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
type Env struct {
    ProjectRoot string
    HomeDir     string
    Getenv      func(string) string
    Flags       map[string]string
}
// Field paths used by this subplan (Go field = TitleCase of the Appendix C JSON key):
//   cfg.Eval.ReplayOnPhaseGate  bool     ← "eval.replayOnPhaseGate", default true
//   cfg.Eval.MinSessions        int      ← "eval.minSessions",       default 20
//   cfg.Scheduler.Cache.ReadMultiplier  float64 ← default 0.1
//   cfg.Scheduler.Cache.WriteMultiplier float64 ← default 1.25

// internal/paths (§4, §3.3)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte) error

// internal/logging, internal/obs (§5.2)
type Logger interface { With(kv ...any) Logger; Debug(string, ...any); Info(string, ...any)
                        Warn(string, ...any); Error(string, ...any); Loud(string, ...any) }
func Nop() Logger
type Registry interface { Hist(name string) Histogram; Counter(name string) Counter
                          Gauge(name string) Gauge; Snapshot() Snapshot
                          CheckBudgets(cfg config.Config) []BudgetBreach }

// internal/testutil (§6.2) — tests only
func NewProject(t *testing.T, opts ...ProjectOpt) *Project
type FakeClock struct{ /* implements core.Clock */ }

// internal/eval/evaltest (§5.22 conformance suite, shipped by SP-01 with behaviour tests
// t.Skip-ped; SP-02 flips every skip off — Rule W-1)
func RunEvalSuite(t *testing.T, factory func(t *testing.T) eval.Harness)
```

SP-01's stub constructor for the suite factory is `eval.New(eval.Options) Harness` — the same signature SP-02 keeps, so flipping the skips off requires no change on SP-01's side.

### Produces (normative — later subplans rely on these)

Every declaration in 00-ARCHITECTURE §5.18 is implemented **with exactly the listed field names and types**. Nothing listed there is renamed, retyped, reordered out of the struct, or removed, and no method is added to another subplan's interface (Rule W-3). SP-02 adds JSON struct tags to the serialized types, and **appends** five fields to `Run` (marked below) because `Compare` and `ScoreRun` are given a `Run` and no `Session`, and therefore cannot otherwise know the compaction turns, the horizon, the per-event demand sets, or `n_i` — without them the two most important functions in the package are unimplementable as specified. Appending is not an amendment under 00-ARCHITECTURE §0 (nothing in §5.18 changes) and is safe by inspection: `Run` is produced and consumed entirely inside `internal/eval` and `test/replay`, both SP-02's. Tags are the lowerCamelCase of the field name unless stated otherwise below.

```go
package eval

// ── §5.18 verbatim ──────────────────────────────────────────────────────────
type Turn struct {
    Index core.TurnIndex `json:"index"`; Role string `json:"role"`; TS core.UnixMilli `json:"ts"`
    Text string `json:"text"`; ToolCalls []ToolCall `json:"toolCalls"`; Tokens core.Tokens `json:"tokens"`
}
type ToolCall struct {
    ID     core.ToolUseID  `json:"id"`
    Name   string          `json:"name"`
    Args   json.RawMessage `json:"args"`
    Result json.RawMessage `json:"result"`
    Paths  []string        `json:"paths"`
}
type Session struct {
    ID string `json:"id"`; Turns []Turn `json:"turns"`; CompactionAt []core.TurnIndex `json:"compactionAt"`
    Meta map[string]string `json:"meta"`; Synthetic bool `json:"synthetic"`
}
type Policy interface {
    Name() string
    KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}
type KeepSet struct{ IDs []string `json:"ids"`; Tokens core.Tokens `json:"tokens"`; P int `json:"p"` }
type Run struct {
    // ── the §5.18 field set, unchanged ──
    Policy string `json:"policy"`; Session string `json:"session"`; Branch string `json:"branch"`
    Actions []Action `json:"actions"`; Keeps []KeepSet `json:"keeps"`
    PauseMS []int `json:"pauseMs"`; ResidualSpan []core.Tokens `json:"residualSpan"`
    FirstTurnAfterMS []int `json:"firstTurnAfterMs"`
    // ── SP-02 appended fields (see the paragraph above) ──
    // All four slices below are parallel to Keeps: index i is compaction event i.
    At           []core.TurnIndex `json:"at"`           // s.CompactionAt, ascending
    Demands      [][]Demand       `json:"demands"`      // Demands(s, At[i], min(At[i]+Horizon, len(s.Turns)))
    PrefixTokens []core.Tokens    `json:"prefixTokens"` // n_i: position-advancing tokens in Blocks(s, At[i])
    FirstCompactionTurn core.TurnIndex `json:"firstCompactionTurn"` // At[0]; -1 when len(At)==0
    Horizon      int              `json:"horizon"`      // the effective K this run was built with
}
type Action struct{ Turn core.TurnIndex; Tool string; Paths []string; Decision string }
type Divergence struct {
    FirstDivergenceTurn int; FileSetJaccard float64; ToolEditDistance int
    SameDecision bool; DecisionPreservation float64; RedundantReads int; ReAttempts int
}
type Score struct {
    FractionOfOPT float64; Divergence Divergence; RewriteTokens int
    RehydrationTokens core.Tokens; RetrievalHitRate float64
    CompactionPauseMS Percentiles; ResidualSpan Percentiles; FirstTurnAfterMS Percentiles
}
type Percentiles struct{ P50, P95, P99, Max float64 }
type Harness interface {
    Load(dir string) ([]Session, error)
    Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
    Compare(uncompacted, compacted Run) Divergence
    Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
    ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
    Report(ctx context.Context, scores map[string][]Score) (Report, error)
}
type ReplayOptions struct{ K int; Seed int64; Budget core.Tokens; Deterministic bool }
type Report struct {
    Policies map[string]Score; Baseline string; Regressions []Regression
    Sessions int; GeneratedAt time.Time
}
type Regression struct{ Metric, Policy string; Baseline, Observed, DeltaPct float64; Allowed bool }
func Synthesize(seed int64, spec SynthSpec) Session
type SynthSpec struct {
    Turns int; ToolMix map[string]float64
    FileRereadRate float64; TestOutputNoise float64
    Changepoints int; Eliminations int; SubagentCalls int
    DependencyChangeAt []core.TurnIndex
    CompactionAt []core.TurnIndex
}

// ── SP-02 additions (permitted: eval is SP-02's package; nothing above changes) ──
type Options struct {
    Cfg     config.Config
    Log     logging.Logger
    Metrics obs.Registry
    Clock   core.Clock
    Latency LatencyModel
}
// New fills zero-valued members with safe defaults so a test may write eval.New(eval.Options{}):
// Cfg → config.Defaults(), Log → logging.Nop(), Metrics → a no-op registry, Clock →
// core.SystemClock(), Latency → DefaultLatencyModel() (detected by Latency.Modelled == false,
// which the default never is). It registers no policies and mutates no globals.
// The returned *harness records eval.belady.ms, eval.replay.ms, eval.score.ms and
// eval.report.ms histograms on Options.Metrics — the only use this package makes of obs.
func New(o Options) Harness
func DefaultLatencyModel() LatencyModel
// BaselineRun builds the "uncompacted" branch straight from the logged turns. It is a package
// function, not a Harness method, so the §5.18 interface is reproduced exactly as specified.
func BaselineRun(s Session) Run
// LiveRunner is the §6.3-tier-3 seam. Nil in every build SP-02 ships; Replay returns
// errLiveRunnerAbsent when it is nil and live mode was requested. SP-17's pre-release run
// supplies an implementation; no model call is written in this subplan.
type LiveRunner interface {
    Fork(ctx context.Context, s Session, at core.TurnIndex, keep KeepSet, k int) ([]Action, error)
}
// SetLiveRunner is declared on Harness's concrete type and reached via a type assertion,
// so the §5.18 Harness interface gains no method (Rule W-3).
func (h *harness) SetLiveRunner(r LiveRunner)
type LatencyModel struct {
    PauseBaseMS            float64 // 3000
    PausePerKResidualMS    float64 // 150
    FirstTurnBaseMS        float64 // 800
    FirstTurnPerKRehydrMS  float64 // 90
    Modelled               bool    // always true in deterministic mode
}
const DefaultKeepBudget core.Tokens = 40000 // §2.3 maxTokens: 40_000
const DefaultHorizonK   int         = 20

type BlockKind uint8
const (
    BlockToolResult BlockKind = iota
    BlockUserPrompt
    BlockAssistant
    BlockFile
    BlockElimination
    BlockDecision
)
type Block struct {
    ID      string; Kind BlockKind; Turn core.TurnIndex; Pos int
    Tokens  core.Tokens; Paths []string; Symbols []string; Ephemeral bool
}
func Blocks(s Session, upTo core.TurnIndex) []Block

type DemandKind uint8
const (
    DemandFileContent DemandKind = iota
    DemandToolResult
    DemandDecision
    DemandElimination
)
type Demand struct{ Turn core.TurnIndex; BlockID string; Kind DemandKind }
func Demands(s Session, from, to core.TurnIndex) []Demand

type OPTDetail struct {
    Value        int     // demands satisfied by the OPT keep-set
    Exact        bool    // false → 1/2-approximation fallback was used
    Candidates   int
    BudgetTokens core.Tokens
    DPCells      int
}
type BeladyOptions struct{ K int; Granularity core.Tokens; MaxDPCells int }
func DefaultBeladyOptions() BeladyOptions // K=20, Granularity=256, MaxDPCells=20_000_000
func BeladyDetail(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens,
                  o BeladyOptions) (KeepSet, OPTDetail, error)

type BreakpointPlan struct {
    Positions   []int   `json:"positions"`   // ascending; len(Positions) may be < Markers
    CachedReads int64   `json:"cachedReads"` // value(B) of the chosen marker set
    Markers     int     `json:"markers"`     // the marker budget the caller asked for
    Candidates  int     `json:"candidates"`  // candidate positions surviving the 256-cap stride
    Note        string  `json:"note"`        // always the NotPluginActionable constant
}
const NotPluginActionable = "measurement only: §5.6/§12 — Claude Code manages its own " +
    "cache_control markers; a plugin cannot place or move them"
func BreakpointOPT(s Session, markers int) (BreakpointPlan, error)

func RegisterPolicy(name string, ctor func(config.Config) Policy)
func PolicyByName(name string, cfg config.Config) (Policy, bool)
func PolicyNames() []string
type NamedSpec struct{ File, Shape string; Seed int64; Spec SynthSpec }
func CorpusSpecs() []NamedSpec
func WriteCorpus(dir string) error   // regenerates the 24 files + CORPUS.json; single writer

func NewStockPolicy(cfg config.Config) Policy   // registered as "stock"
func NewNullPolicy(cfg config.Config) Policy    // registered as "null"
func NewOraclePolicy(cfg config.Config) Policy  // registered as "oracle"

// MetricsOf takes the config because rewrite_span_tokens and forfeited_discount_tokens are
// derived from Score.RewriteTokens using w and r, which are config keys, never literals (D11).
func MetricsOf(s Score, cfg config.Config) map[string]float64  // flat metric map, 6-dp rounded
func MetricDirection(metric string) Direction                  // DirHigherBetter | DirLowerBetter
type Direction uint8

// Providers for data owned by later waves (kept out of the import graph, §3.2)
type StatsSample struct {
    Turn core.TurnIndex `json:"turn"`; Objects int `json:"objects"`
    Bytes int64 `json:"bytes"`; RawBytes int64 `json:"rawBytes"`; DedupRatio float64 `json:"dedupRatio"`
}
type GrowthResult struct {
    Exponent float64; Samples int; RawSpan float64; Sublinear bool; Reason string
}
func CheckSublinearGrowth(samples []StatsSample) GrowthResult
type SketchHealth struct{ FillRatio, EstFPRate float64; Records, Active, Stale int }

// Importer (§6.3 tier 2)
type ImportOptions struct {
    From, To string
    Limit    int
    Redact   bool
    Getenv   func(string) string // nil → os.Getenv; injected so tests never mutate the process env
}
type ImportReport struct{ Files, Sessions, Turns, RedactedSpans int; Skipped []string }
func Import(ctx context.Context, o ImportOptions) (ImportReport, error)
func Redact(s Session) (Session, int)
func ImportCommand(args []string, out io.Writer, env config.Env) int // wired as `qompack eval import`
```

**Consumed by later subplans:** SP-14's `commands.Deps` names `Eval eval.Harness`; SP-08/SP-11/SP-12/SP-15 register their policies via `eval.RegisterPolicy` from `test/replay` and assert their phase exit criteria through the gate's phase registry (`test/replay/phases.go`, one function per phase, appended by the owning subplan).

---

## Implementation spec

All paths are repo-relative to `C:/Users/Quant/Documents/Programming/Projects/qompack`.

### 1. `internal/eval/types.go` — the data model (write this first, in the main session)

Holds every type in the Interface-contract block above plus the JSON tags, **and the unexported `harness` struct itself** — `struct{ cfg config.Config; log logging.Logger; metrics obs.Registry; clock core.Clock; lat LatencyModel; live LiveRunner; pool map[string]latencyPool }` — because three of the four subagents implement methods on it and a concurrent redefinition of the receiver is the one conflict worth pre-empting. `latencyPool` is `struct{ pause, residual, firstTurn []float64 }`. Two rules:

- `Session` marshals with `encoding/json` using `json.MarshalIndent(s, "", "  ")` plus a trailing `"\n"`. That exact form is what the corpus files on disk contain — the golden test compares bytes.
- `ToolCall.Args`/`Result` are `json.RawMessage`; the synthesizer always emits compact (`json.Marshal`) objects into them so indentation does not vary.

Block ID grammar (stable, hash-free where possible so goldens are readable):

```
tu:<tool_use_id>                 BlockToolResult
turn:<index>                     BlockUserPrompt | BlockAssistant
file:<paths.Key(path)>           BlockFile
elim:<12 hex of HashBytes("qompack.eval.elim.v1", key)>   BlockElimination
dec:<decision id>                BlockDecision
```

where the elimination key is `paths.Key(target) + "\x00" + approachClass(approach)` and

```go
// approachClass lowercases, collapses whitespace runs to a single '-', and strips
// everything outside [a-z0-9-]. "Widen  Pool Timeout!" → "widen-pool-timeout".
func approachClass(approach string) string
```

### 2. `internal/eval/blocks.go` — `Blocks` and `Demands`

`Blocks(s, upTo)` walks `s.Turns[0:upTo]` in order and emits, per turn, in this order:

1. one `BlockUserPrompt` (`role=="user"`) or `BlockAssistant` (`role=="assistant"`) with `Tokens = turn.Tokens`;
2. one `BlockToolResult` per `ToolCall` with `Tokens` = the `tokens` field of the tool call's `Result` JSON object if present, else `len(Result)/4` rounded up;
3. one `BlockFile` per distinct `paths.Key(p)` first seen in a `ToolCall` whose `Name` is `FileRead`, `Read`, `Edit`, or `Write`, with `Tokens` = the tokens of the producing tool result;
4. one `BlockElimination` per `ToolCall` named `record_eliminated`, `Tokens = 64`;
5. one `BlockDecision` per `[decision:<id>]` marker found in `turn.Text`, `Tokens = 128`.

**Positions vs. weights — the two are deliberately not the same accumulator.** Kinds 1 and 2 (`BlockUserPrompt`/`BlockAssistant` and `BlockToolResult`) are *prefix content*: they are the bytes that actually occupy the context window. Kinds 3, 4 and 5 (`BlockFile`, `BlockElimination`, `BlockDecision`) are *derived retention units* — a file block aliases the very tool result that produced it, and elimination/decision blocks are extracted from text already counted in their turn. Therefore:

- **`Pos` advances only on kinds 1 and 2.** `Pos` of the first emitted block is 0; each subsequent kind-1/kind-2 block gets the running sum of the `Tokens` of the kind-1/kind-2 blocks before it. A derived block takes the `Pos` of the block it was derived from (the producing tool result for `BlockFile` and `BlockElimination`; the containing turn block for `BlockDecision`). Nothing is counted twice into a position.
- **`n_i`, the prefix length at compaction event `i`, is the sum of `Tokens` over the kind-1/kind-2 blocks of `Blocks(s, at_i)`** — nothing else. This is the `n` of §5.2's `cost = w · (n − p_min)`, and it is why a 400-turn synthetic session lands in the same order of magnitude as the document's 167 000.
- **Knapsack weight is each block's own `Tokens`, derived or not.** Keeping a file block genuinely costs tokens to restore into the rehydrated context, so a policy that keeps both `tu:x` and `file:p` legitimately pays twice — the budget is a retention budget, not a prefix measurement. Positions measure the original prefix; weights measure what restoration costs. Conflating them was the modelling error this rule exists to prevent.

`p_min` is measured against `Pos`, so it is always a real position in the original prefix.

`Demands(s, from, to)` walks `s.Turns[from+1 : to]` and emits, deterministically ordered by `(Turn, BlockID)`:

- `DemandFileContent` for `file:<key>` for every path in every `ToolCall.Paths` of that turn whose key was already produced as a `BlockFile` at a turn `≤ from`;
- `DemandToolResult` for `tu:<id>` when `turn.Text` contains the literal `tool_use_id` string of a tool call at a turn `≤ from`;
- `DemandDecision` for `dec:<id>` when `turn.Text` contains `[decision:<id>]` for an id minted at a turn `≤ from`;
- `DemandElimination` for `elim:<k>` when the turn contains a `ToolCall` whose `(target, approachClass)` matches an elimination recorded at a turn `≤ from`. Target for a non-`record_eliminated` call is its first `Paths` entry.

Duplicate `(Turn, BlockID)` pairs are collapsed to one demand. Demands are the *only* notion of "was needed after the compaction" in this package.

### 3. `internal/eval/belady.go` — §6.10 OPT keep-sets

```go
func BeladyDetail(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens,
                  o BeladyOptions) (KeepSet, OPTDetail, error)
```

**Formulation.** Clairvoyance means the demand sequence over the horizon is known. Let `C = Blocks(s, at)` and `D = Demands(s, at, min(at+K, len(s.Turns)))`. For block `b`, `value(b) = |{d ∈ D : d.BlockID == b.ID}|` and `weight(b) = b.Tokens`. OPT is

```
maximize   Σ_{b ∈ S} value(b)
subject to Σ_{b ∈ S} weight(b) ≤ budget
```

which is exactly 0/1 knapsack. With unit weights and a slot budget it degenerates to classic Belady (keep the items demanded soonest/most), so this is the correct generalization to heterogeneous block sizes rather than a substitute for it.

**Cancellation and guards, before any allocation.** `BeladyDetail` returns `ctx.Err()` immediately if the context is already done, and re-checks `ctx.Err()` once per outer DP item (the `i` loop) so a cancelled 20 M-cell run unwinds promptly. `budget <= 0` returns an empty `KeepSet` with `Exact: true` and `Candidates: 0`, no error. `o.Granularity <= 0` is replaced by `DefaultBeladyOptions().Granularity`; `o.K <= 0` by `DefaultHorizonK`; `o.MaxDPCells <= 0` by the default.

**Exact solver.** Drop every `b` with `value(b) == 0` (they can never help; this is the pruning that makes the DP small). Scale weights: `w_i = ceil(weight(b_i) / o.Granularity)` (granularity 256 tokens; rounding *up* is conservative — the returned keep-set never exceeds the true budget). `W = floor(budget / o.Granularity)`. Any item with `w_i > W` is dropped before the DP (it can never fit). If `len(items) * (W+1) > o.MaxDPCells`, take the fallback below and set `Exact=false`; otherwise run the standard 1-D DP:

```go
words := (W + 1 + 63) / 64
dp    := make([]int32, W+1)                 // dp[j] = best value at capacity j
take  := make([]uint64, len(items)*words)   // bit (i*words*64 + j) = item i taken at cap j
for i, it := range items {
    for j := W; j >= it.w; j-- {
        if v := dp[j-it.w] + it.value; v > dp[j] {
            dp[j] = v
            take[i*words+j/64] |= 1 << uint(j%64)
        }
    }
}
// reconstruction
keep, j := make([]string, 0, len(items)), W
for i := len(items) - 1; i >= 0; i-- {
    if take[i*words+j/64]&(1<<uint(j%64)) != 0 {
        keep = append(keep, items[i].id)
        j -= items[i].w
    }
}
sort.Strings(keep)
```

Reconstruct by walking `i` downward from the last item at capacity `W`, following `take`. Ties: because the loop takes `v > dp[j]` (strict), the reconstruction is deterministic given a deterministic item order. **Item order is `sort.Slice` on `(−value, weight, ID)`** — descending value, then ascending tokens, then lexicographic ID — computed before the DP so two runs on the same session always produce the identical keep-set.

**Fallback (only when the DP is too large).** Sort by density `value/weight` descending, tiebreak `(weight asc, ID asc)`; fill greedily; then compare against the single best-value item that fits alone and return whichever total value is higher. That is the textbook 1/2-approximation. `OPTDetail.Exact = false`, and the gate emits a WARN naming the session — a corpus that trips this is a corpus that needs smaller synthetic sessions.

`KeepSet.Tokens` = Σ weights of the selected blocks (unscaled, exact). `KeepSet.P` = `min(Pos)` over blocks **not** selected, or `len(prefix tokens)` when everything is kept — this is `p_min` of §5.2, and it is what feeds rewrite-token accounting.

`Harness.Belady(ctx, s, at, budget)` calls `BeladyDetail` with `DefaultBeladyOptions()` and discards the detail.

**Performance budget:** `BenchmarkBeladyDetail_400Turns` ≤ 250 ms/op and ≤ 96 MB allocated (a 400-turn session with `budget = 40_000` gives `W = 156` and ≤ 3 000 items → ≤ 470 k cells).

### 4. `internal/eval/breakpoint.go` — the §5.6 measurement-only extension

```go
func BreakpointOPT(s Session, markers int) (BreakpointPlan, error)
```

Candidates are the token positions at turn boundaries: `cand[t] = Σ tokens of the position-advancing blocks in turns < t` (the same accumulator as `Block.Pos`, §2), deduplicated and sorted ascending, always including `0`. When there are more than 256 of them, keep the first, the last, and every `stride = ceil(len/256)`-th in between. The surviving count is reported in `BreakpointPlan.Candidates`; `markers` is echoed in `BreakpointPlan.Markers` even when fewer positions are returned, and `Plan.Note` is never used for anything but the disclaimer constant.

For every turn `t` that issues an API call (every turn with `role=="assistant"`), define `cap_t = min(n_t, e_t)` where `n_t` is the prefix length at `t` and `e_t` is the earliest edited token position since the previous assistant turn (`+∞` when no compaction happened in between; a compaction at turn `a` contributes `e = KeepSet_P(a)`, taken from the *stock* policy so the measurement is about the observed session). Then for a marker set `B`:

```
value(B) = Σ_t  max{ q ∈ B ∪ {0} : q ≤ cap_t }
```

Maximize over `|B| = markers`. `markers` is always supplied by the caller — there is no default inside the function; `test/replay` calls `BreakpointOPT(s, 4)` because four is the Messages-API `cache_control` marker budget, and that literal lives in one named constant in `test/replay/report.go` (`const cacheControlMarkers = 4`), not scattered. Before anything else the function clamps `markers` to `[0, len(cand)]`; `markers == 0` returns `BreakpointPlan{Positions: nil, CachedReads: 0, Markers: 0, Candidates: len(cand), Note: NotPluginActionable}` with no error. The value decomposes: for chosen candidates `q_1 < … < q_m`, `value = Σ_j q_j · |{t : q_j ≤ cap_t < q_{j+1}}|` with `q_{m+1} = +∞`. Sort `caps` ascending once and let `cnt(a, b)` be the number of caps in `[a, b)`, answered in `O(log C)` by binary search. Then the DP is exact:

```go
const negInf = math.MinInt64 / 4   // sentinel: "this (j, i) state is unreachable"

// g[j][i] = best value using exactly j markers, the largest of which is cand[i].
g      := make([][]int64, markers+1)
parent := make([][]int, markers+1)
for j := range g {
    g[j] = make([]int64, len(cand))
    parent[j] = make([]int, len(cand))
    for i := range g[j] { g[j][i] = negInf; parent[j][i] = -1 }
}
for i := range cand {
    g[1][i] = int64(cand[i]) * int64(cnt(cand[i], math.MaxInt))
}
for j := 2; j <= markers; j++ {
    for i := range cand {                       // cand[i] is the largest chosen
        best := int64(negInf)
        for k := 0; k < i; k++ {                // cand[k] is the second-largest
            if g[j-1][k] == negInf { continue } // fewer than j-1 candidates below i
            v := g[j-1][k] -
                 int64(cand[k])*int64(cnt(cand[i], math.MaxInt)) + // undo k's over-count
                 int64(cand[i])*int64(cnt(cand[i], math.MaxInt))
            if v > best { best = v; parent[j][i] = k }
        }
        g[j][i] = best                          // stays negInf when i < j-1
    }
}
```

The correction term is what keeps the recurrence exact: `g[j-1][k]` credited `cand[k]` to every cap `≥ cand[k]`, including those `≥ cand[i]`, which the newly-added higher marker now serves instead. `O(markers · C²)` with `C ≤ 256` → ≤ 262 k operations at `markers = 4`.

**Selecting the answer.** Take the maximum of `g[j][i]` over all `1 ≤ j ≤ markers` and all `i`, skipping `negInf` states, tie-broken by smaller `j` then smaller `i` so the result is deterministic; reconstruct the marker set by following `parent[j][i]` down to `j == 1` and reversing. Using fewer than `markers` markers is never strictly better (a marker at 0 contributes 0 and can always be added), so the max over `j` exists only to make the `i < j-1` unreachable states harmless and to keep the deterministic tie-break honest. `CachedReads` is that maximum; `Positions` is the reconstructed ascending set with any leading `0` dropped (position 0 is the implicit `q = 0` baseline and is never reported as a marker).

**Testability seam.** `BreakpointOPT` is a thin wrapper: it derives `cand` and `caps` from the session and delegates to the unexported `breakpointPlan(caps, cand []int, markers int) BreakpointPlan`, which holds the DP above. Unit tests that need exact, hand-chosen inputs (`TestBreakpointOPT_KnownOptimum`) call `breakpointPlan` directly; the property and benchmark tests call `BreakpointOPT` so the derivation is covered too.

`Plan.Note` is always the `NotPluginActionable` constant. The gate prints it verbatim on the line above the number. A unit test asserts the string is present in the driver's stdout whenever a breakpoint number is printed — the design says this analysis is measurement-and-port material and the output must never read as a plugin feature.

### 5. `internal/eval/policy.go` — the policy registry and the three built-ins

`RegisterPolicy` writes into a package-level `map[string]func(config.Config) Policy` guarded by a `sync.Mutex`; duplicate names panic at init (a programming error, never a runtime path). `PolicyNames` returns names sorted lexicographically.

**Host constants live in one file.** `internal/eval/hostconst.go` declares every number that belongs to *Claude Code*, not to Qompack, each annotated so the `nomagic` pass (§11.6) accepts it — D11 governs Qompack's own tunables, and modelling the host faithfully requires naming the host's numbers:

```go
// Constants of the system Qompack surrounds (§2.3, §2.4, §2.5). They are NOT Qompack
// tunables and must never be moved into internal/config: changing them would change what
// "stock behaviour" means, which is precisely what the Phase 0 baseline measures.
const (
    hostTopFiles            = 5      //nomagic:allow §2.4 step 7 — host restore fan-out
    hostPerFileTokens       = 5000   //nomagic:allow §2.4 step 7 — host 5K/file
    hostRestoreBudget       = 50000  //nomagic:allow §2.4 step 7 — host 50K budget
    hostSkillBudget         = 25000  //nomagic:allow §2.4 step 7 — host 25K skills
    hostPreserveMinTokens   = 10000  //nomagic:allow §2.3 minTokens
    hostPreserveMinMessages = 5      //nomagic:allow §2.3 minTextBlockMessages
    hostPreserveMaxTokens   = 40000  //nomagic:allow §2.3 maxTokens
    hostMaxOutputReserve    = 20000  //nomagic:allow §2.5 min(maxOutputTokens, 20_000)
    hostAutoCompactBuffer   = 13000  //nomagic:allow §2.5 effectiveWindow − 13_000
    hostTokenPadNumerator   = 4      //nomagic:allow §2.2 4/3 padding
    hostTokenPadDenominator = 3      //nomagic:allow §2.2 4/3 padding
)
```

**`stock`** — models Claude Code Full Compact per §2.4/§2.5 exactly:

Every stage records **what each kept block contributes to `used`** in a `contrib map[string]core.Tokens`, because stage (a) caps a file block at 5 000 tokens while stage (b) would count that same block in full. Without the map, stage (c)'s removals subtract a number that was never added and `used` drifts (it can go negative on a session with large file reads). `used` is by construction `Σ contrib`, and a unit invariant asserts exactly that at every return.

```go
func (p *stockPolicy) KeepSet(ctx context.Context, s Session, at core.TurnIndex,
                              budget core.Tokens) (KeepSet, error) {
    if err := ctx.Err(); err != nil { return KeepSet{}, err }
    blocks  := Blocks(s, at)
    contrib := map[string]core.Tokens{}          // id → what it added to used
    add := func(id string, t core.Tokens) {      // first writer wins; never double-counts
        if _, ok := contrib[id]; !ok { contrib[id] = t }
    }
    used := func() core.Tokens {
        var n core.Tokens
        for _, t := range contrib { n += t }
        return n
    }

    // (a) §2.4 step 7: top 5 recently-read files, 5K/file, 50K budget.
    //     lastNDistinctFiles returns the last n BlockFile entries by descending Turn,
    //     one per distinct paths.Key, re-sorted ascending by Turn for determinism.
    fileUsed := core.Tokens(0)
    for _, b := range lastNDistinctFiles(blocks, hostTopFiles) {
        t := min(b.Tokens, hostPerFileTokens)
        if fileUsed+t > hostRestoreBudget { break }
        add(b.ID, t); fileUsed += t
    }
    // (b) §2.3 preservation: walk backwards from `at`, keeping whole turns until BOTH
    //     minTokens=10_000 and minTextBlockMessages=5 are met, capped at maxTokens=40_000.
    //     blocksOfTurn returns only position-advancing blocks (§2): a turn's own
    //     message block and its tool-result blocks. Derived blocks ride along via (a).
    msgs, msgTokens := 0, core.Tokens(0)
    for t := int(at) - 1; t >= 0; t-- {
        turnBlocks := blocksOfTurn(blocks, core.TurnIndex(t))
        tt := sumTokens(turnBlocks)
        if msgTokens+tt > hostPreserveMaxTokens { break }
        for _, b := range turnBlocks { add(b.ID, b.Tokens) }
        msgTokens += tt
        if hasTextBlock(turnBlocks) { msgs++ }
        if msgTokens >= hostPreserveMinTokens && msgs >= hostPreserveMinMessages { break }
    }

    // (c) the harness imposes one equal budget on every policy (§11.1 "under the same
    //     token budget"). Stock has no smarter rule than dropping its oldest kept turn,
    //     so that is exactly what it does. Ties and exhaustion are explicit: oldestKeptTurn
    //     returns (turn, false) when nothing is kept, which terminates the loop even if the
    //     budget is smaller than a single block.
    for used() > budget {
        oldest, ok := oldestKeptTurn(contrib, blocks)
        if !ok { break }
        for _, b := range blocksOfTurn(blocks, oldest) { delete(contrib, b.ID) }
        for _, b := range derivedBlocksOfTurn(blocks, oldest) { delete(contrib, b.ID) }
    }
    return KeepSet{IDs: sortedKeys(contrib), Tokens: used(), P: 0}, nil
}
```

`P: 0` is not an approximation — a Full Compact rewrites the whole message array, so `p_min = 0` and the rewrite cost is `w · n`. That is the fact §5.2 is complaining about, and the baseline must show it.

**`null`** — `KeepSet{IDs: nil, Tokens: 0, P: 0}`. Its fraction-of-OPT is the floor; a policy scoring below `null` is a bug in that policy.

**`oracle`** — delegates to `BeladyDetail`. Its fraction-of-OPT must be exactly `1.0` on every session; a test asserts it, which is how the scorer proves it is self-consistent.

### 6. `internal/eval/replay.go` — `Load`, `Replay`, and the deterministic divergence model

`Load(dir)` reads `*.json` in `dir` sorted by filename (`filepath.WalkDir`, non-recursive), unmarshals each into a `Session`, and returns `core.ErrNotFound` wrapped with the directory when zero sessions are found. A file that fails to parse is a hard error naming the file — a silently skipped fixture is a silently wrong baseline. **Exactly one filename is skipped: `CORPUS.json`**, which lives in the same directory and is a manifest, not a session; it is skipped by name (not by a heuristic) and its absence in a directory that contains sessions is a WARN, never an error, so an imported recorded corpus without a manifest still loads. `json.Decoder` is used with `DisallowUnknownFields()` on session files so a schema drift in a committed fixture fails loudly instead of silently zeroing a field.

`Replay(ctx, s, p, o)`:

1. Defaults: `o.K == 0 → DefaultHorizonK`; `o.Budget == 0 → DefaultKeepBudget`; `o.Deterministic` false with `os.Getenv("QOMPACK_EVAL_LIVE") != "1"` is an error (`live mode requested without QOMPACK_EVAL_LIVE=1`). CI only ever runs `Deterministic: true`. Live mode is implemented as a documented, tested *seam*: the concrete `*harness` carries a nil-by-default `LiveRunner` field settable through `SetLiveRunner` (reached by type assertion, so the §5.18 `Harness` interface gains no method), and `Replay` returns `errLiveRunnerAbsent` when it is nil. No model call is written in this subplan — the seam exists so SP-17's pre-release run can fill it, and the error message says exactly that.
2. Build the **uncompacted** run by calling the package function `BaselineRun(s)`: `Actions` = one `Action` per turn in `s.Turns` (`Tool` = the turn's first tool-call name or `""`, `Paths` = the union of its tool calls' paths in first-seen order, `Decision` = the `[decision:<id>]` id found in the turn text or `""`). `Keeps`, `PauseMS`, `ResidualSpan`, `FirstTurnAfterMS`, `At`, `Demands`, `PrefixTokens` are empty; `Branch = "uncompacted"`; `FirstCompactionTurn` = `s.CompactionAt[0]` (or `-1` when the session has none) and `Horizon` = `DefaultHorizonK`. `Compare` always reads the horizon and the first compaction turn from its **compacted** argument, never from the baseline, so a replay run with a non-default `o.K` needs no change to `BaselineRun`.
3. Build the **compacted** run: start from the uncompacted actions. For each `at ∈ s.CompactionAt` (ascending), call `p.KeepSet(ctx, s, at, o.Budget)`, append it to `Keeps`, and then for every demand `d` in `Demands(s, at, min(at+o.K, len(s.Turns)))` whose `BlockID` is **not** in the keep-set, apply the repair rule for its kind, in demand order:

| Demand kind | Repair inserted immediately before the demanding turn's action | Counter |
|---|---|---|
| `DemandFileContent` | `Action{Turn: d.Turn, Tool: "FileRead", Paths: [path]}` | redundant read |
| `DemandToolResult` | `Action{Turn: d.Turn, Tool: "FileRead", Paths: [path of the tool use]}` (or `Tool:"Bash"` when the tool use had no path) | redundant read |
| `DemandElimination` | `Action{Turn: d.Turn, Tool: "ReAttempt", Paths: [target]}` | re-attempt |
| `DemandDecision` | no insertion; the demanding turn's `Action.Decision` is set to `""` | decision lost |

   Then fill the three latency slices for this event: `ResidualSpan[i] = residual(at)` (tokens between the frontier and `at`; the frontier is `0` for `stock` and, for any policy that reports one, the policy's `KeepSet.P`), `PauseMS[i] = round(L.PauseBaseMS + L.PausePerKResidualMS * residual/1000)`, `FirstTurnAfterMS[i] = round(L.FirstTurnBaseMS + L.FirstTurnPerKRehydrMS * KeepSet.Tokens/1000)`.
4. `Branch = "compacted"`, `Policy = p.Name()`, `Session = s.ID`, `Horizon = o.K`, `FirstCompactionTurn = s.CompactionAt[0]` (or `-1`), and — parallel to `Keeps`, one entry per compaction event, in the same ascending order — `At[i] = at`, `Demands[i] = Demands(s, at, min(at+o.K, len(s.Turns)))`, `PrefixTokens[i] = n_i` (the §2 position accumulator over `Blocks(s, at)`). These four are what make `ScoreRun` and `Compare` computable from a `Run` alone; they are filled here and nowhere else.

`Replay` returns the **compacted** run. The uncompacted branch is not a replay at all — it is the logged ground truth — so it is produced by the package-level `func BaselineRun(s Session) Run`, and `Compare` is always called as `h.Compare(eval.BaselineRun(s), compactedRun)`. `BaselineRun` is a pure, deterministic function of the session (it is a package function rather than a `Harness` method so the interface in §5.18 stays exactly as specified).

**Latency-model honesty.** `LatencyModel.Modelled` is `true` and the driver's envelope carries `"latency": "modelled"` (see §8's `DriverReport`; `eval.Report` itself has the §5.18 field set and no room for it). `DefaultLatencyModel()` returns `{3000, 150, 800, 90, true}`: at the §2.5 stock residual of 167 000 tokens the model yields `3000 + 150·167 = 28 050 ms`, inside the *"~15–40s"* band §6.7 states; at the §8.5 post-O5 residual of 10–20 K it yields 4.5–6.0 s. Those two anchors are the whole calibration and they are asserted by a unit test, so if anyone changes a coefficient the test names the design sentence they broke.

### 7. `internal/eval/divergence.go` — `Compare` (§4.2's five bullets)

```go
func (h *harness) Compare(uncompacted, compacted Run) Divergence
```

`Compare` takes `firstCompactionTurn = compacted.FirstCompactionTurn` and `K = compacted.Horizon`; the **horizon** is the action range `[firstCompactionTurn+1, firstCompactionTurn+K]` by `Action.Turn`, applied identically to both branches. When `compacted.FirstCompactionTurn < 0` (a session with no compaction) `Compare` returns the identity `Divergence` (`FirstDivergenceTurn = K`, `FileSetJaccard = 1`, `ToolEditDistance = 0`, `SameDecision = true`, `DecisionPreservation = 1`, zero counters).

- **FirstDivergenceTurn** — the smallest `i` such that `uncompacted.Actions[i]` and `compacted.Actions[i]` differ on `(Tool, Paths, Decision)`, expressed as `int(compacted.Actions[i].Turn) - int(firstCompactionTurn)`. Comparison length is `min(len(a), len(b))`; if one is longer and the shorter is a prefix of it, the divergence index is that length. **When the branches do not diverge anywhere in the horizon the value is `K`, not `-1`** — `first_divergence_turn` is a `DirHigherBetter` metric, so the "never diverged" case must be the *largest* value the metric can take or the 2% gate would score a perfect policy as the worst one. `-1` is never emitted; a unit test asserts the returned value is always in `[0, K]`.
- **FileSetJaccard** — over the paths touched in the horizon in each branch: `|A∩B| / |A∪B|`, with both-empty ⇒ `1.0`. Paths compared as `paths.Key`.
- **ToolEditDistance** — Levenshtein (unit insert/delete/substitute) over the two tool-name sequences restricted to the same horizon. Implemented with the two-row rolling array, `O(n·m)` time / `O(min(n,m))` space.
- **SameDecision** — the last non-empty `Decision` in each horizon is equal (both empty ⇒ `true`).
- **DecisionPreservation** — `|{d ∈ decisions(uncompacted horizon) : d ∈ decisions(compacted horizon)}| / |decisions(uncompacted horizon)|`; denominator 0 ⇒ `1.0`.
- **RedundantReads** — `redundant(compacted) − redundant(uncompacted)` where `redundant(run)` counts actions whose `Tool ∈ {FileRead, Read}` and whose path appears in an earlier action of the same run. May be negative; negative means the policy *prevented* re-reads and is an improvement. Documented in the metric table and in `MetricDirection` (`DirLowerBetter`).
- **ReAttempts** — same shape, counting `Tool == "ReAttempt"`.

### 8. `internal/eval/score.go` — `ScoreRun` and `Report`

```go
func (h *harness) ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
```

`ScoreRun` is a pure function of `(r, opt)` plus the harness's own `cfg` and `log`; every quantity it needs about the session travels inside `r` (`At`, `Demands`, `PrefixTokens`, `Horizon`), which is why those fields exist. It is called once per (policy, session).

- **FractionOfOPT (§11.1, primary).** Per compaction event `i` at turn `at_i = r.At[i]`: `v_i = value(r.Keeps[i], r.Demands[i])` and `o_i = value(opt[at_i], r.Demands[i])`, where `value(KS, D)` = the number of demands in `D` whose `BlockID ∈ KS.IDs`. Both sides are scored against the *same* demand set, which is the one recorded on the run. A missing `opt[at_i]` is a programming error and panics in tests / returns a zeroed `Score` with a `Loud` log in production. Session-level `FractionOfOPT = Σ v_i / Σ o_i` (micro-average, token-weighted by construction because larger events contribute more demands). `Σ o_i == 0` ⇒ `1.0` (nothing was demanded; every keep-set is optimal). Result is clamped to `[0, 1]`; a policy whose keep-set exceeds `budget` is clamped **and** logged `Loud` with the overrun, because a policy that cheats on the budget is not comparable.
- **RewriteTokens** — §11.2 defines it as the session **total** `w · (n − p_min)`, so the sum is taken first and rounded once: `RewriteTokens = round(w · Σ_i (n_i − p_min,i))`. Rounding per event and then summing would leave `rewrite_span_tokens = RewriteTokens / w` off by up to half a token per event, and a derived metric that does not invert cleanly is a metric people stop trusting. `w = cfg.Scheduler.Cache.WriteMultiplier` read from the harness's config at the use site (D11; there is no `1.25` literal anywhere in `internal/eval`), `n_i = r.PrefixTokens[i]`, `p_min,i = r.Keeps[i].P` clamped to `[0, n_i]`. Read §5.2's table carefully before writing the test: its **Rewrite** column is the *unweighted* span `n − p_min` (A: `167_000 − 150_000 = 17_000`; B: `167_000 − 10_000 = 157_000`), and its "30×" refers to the **benefit** ratio (60K dropped vs 2K dropped), not to the rewrite ratio, which is `157/17 ≈ 9.2×`. This harness reports three distinct, separately-named quantities so nobody conflates them again: `rewrite_span_tokens = n − p_min`, `rewrite_tokens = w · (n − p_min)` (A: `21_250`; B: `196_250`), and `forfeited_discount_tokens = (1 − r) · (n − p_min)` (A: `0.9 · 17_000 = 15_300`; B: `141_300`), the last two using `w` and `r` from config.
- **RehydrationTokens** — `Σ_i r.Keeps[i].Tokens`. This is §11.2's "Tokens spent restoring context".
- **RetrievalHitRate** — over the compacted run: `hits / retrievalActions`, where a retrieval action is `Tool ∈ {recall, expand, re_read, already_tried}` and it is a *hit* when its path was in the pre-compaction read set and no `FileRead`/`Read` of that path occurs in the following 5 actions. Zero retrieval actions ⇒ `0.0` (and the report records `retrieval_actions: 0` so a zero rate is never mistaken for a failure).
- **CompactionPauseMS / ResidualSpan / FirstTurnAfterMS** — `Percentiles` over `r.PauseMS`, `r.ResidualSpan`, `r.FirstTurnAfterMS`. Percentile rule: nearest-rank on the sorted slice, `idx = ceil(q·N) − 1`, clamped to `[0, N−1]`; empty slice ⇒ all-zero `Percentiles`.

`Report(ctx, scores)` aggregates `map[policyName][]Score` into `Report{Policies, Baseline, Regressions, Sessions, GeneratedAt}`. Aggregation across sessions is the arithmetic mean for ratio metrics (`FractionOfOPT`, `FileSetJaccard`, `DecisionPreservation`, `RetrievalHitRate`, `SameDecision` as 1/0, `FirstDivergenceTurn`) and the sum for count metrics (`RewriteTokens`, `RehydrationTokens`, `Divergence.RedundantReads`, `Divergence.ReAttempts`, `ToolEditDistance`). `Baseline` is `"stock"`. `Sessions` is `len(scores[Baseline])`, and `Report` errors if any other policy has a different number of scores — an unequal corpus across policies makes every comparison meaningless. `GeneratedAt = h.clock.Now().UTC()`. `Regressions` is left empty by `Report` — filling it is the gate's job (§ below), because comparison needs a *previous* report, which `eval` is never given.

**Pooled percentiles.** The three latency metrics must *not* be averaged across sessions (an average of P95s is not a P95), but a `Score` carries only the already-reduced `Percentiles`. So `ScoreRun` appends the raw `r.PauseMS`, `r.ResidualSpan` and `r.FirstTurnAfterMS` slices into an unexported per-policy pool on the harness (`h.pool[policy]`), and `Report` recomputes each `Percentiles` with the same nearest-rank rule over the pooled, sorted concatenation. This is the one piece of state in the package and it is scoped, documented and bounded: `New` returns a fresh harness with an empty pool, the driver builds exactly one harness per invocation, and `Report` is therefore a deterministic function of the sequence of `ScoreRun` calls that preceded it — a property `TestReport_PercentilesRecomputedNotAveraged` asserts directly by calling `ScoreRun` for two sessions with disjoint pause distributions and comparing against the P95 of the hand-concatenated slice. `Report` returns an error if a policy appears in `scores` with no pooled entries, so a caller that skipped `ScoreRun` gets a failure rather than silent zeros.

`MetricsOf(s Score, cfg config.Config)` produces the canonical flat map, all values `math.Round(x*1e6)/1e6`:

```
fraction_of_opt, first_divergence_turn, file_set_jaccard, tool_edit_distance,
same_decision, decision_preservation, redundant_reads, re_attempts,
rewrite_span_tokens, rewrite_tokens, forfeited_discount_tokens,
rehydration_tokens, retrieval_hit_rate,
compaction_pause_ms_p50, compaction_pause_ms_p95, residual_span_p50, residual_span_p95,
first_turn_after_ms_p50, first_turn_after_ms_p95
```

`Score.RewriteTokens` (the §5.18 field) carries `rewrite_tokens`; the other two are derived
inside `MetricsOf` as `rewrite_span_tokens = RewriteTokens / w` and
`forfeited_discount_tokens = rewrite_span_tokens · (1 − r)`, with `w` and `r` taken from
`cfg.Scheduler.Cache`. That is why `MetricsOf` takes the config.

`MetricDirection` returns `DirHigherBetter` for `fraction_of_opt, first_divergence_turn, file_set_jaccard, same_decision, decision_preservation, retrieval_hit_rate`; `DirLowerBetter` for the other thirteen keys. An unknown metric name is a `panic` in tests via a table-completeness test — every key of `MetricsOf` must have a direction and every direction entry must be a key of `MetricsOf`. The two §11.4 watch-for metrics (`bloom_fp_rate`, `bloom_fill_ratio`) are **not** `MetricsOf` keys: they come from a provider, not from a `Score`, so the gate keeps its own small extension table in `test/replay/gate.go` (`var watchForDirection = map[string]eval.Direction{"bloom_fp_rate": DirLowerBetter, "bloom_fill_ratio": DirLowerBetter}`) and a gate test asserts that table's keys are disjoint from `MetricsOf`'s. Keeping them out of `MetricsOf` is what lets the completeness test above be an exact set equality.

**The report envelope — where everything that is not a `Score` lives.** `eval.Report` has exactly the §5.18 field set, so the several per-run facts this subplan promises to disclose (`"latency": "modelled"`, `budget_violation`, `noDemands`, `retrieval_actions`, the corpus hash, the breakpoint number, the growth and sketch results) cannot and do not live on it. They live on the driver's envelope, defined once in `test/replay/report.go`:

```go
type DriverReport struct {
    Generator     string                        `json:"generator"`     // "test/replay/1"
    Corpus        string                        `json:"corpus"`
    CorpusTier    string                        `json:"corpusTier"`    // "synthetic" | "recorded"
    CorpusSHA256  string                        `json:"corpusSHA256"`
    Sessions      int                           `json:"sessions"`
    Latency       string                        `json:"latency"`       // always "modelled" in deterministic mode
    Policies      map[string]map[string]float64 `json:"policies"`      // MetricsOf per policy
    WatchFor      map[string]float64            `json:"watchFor"`      // bloom_fp_rate, bloom_fill_ratio
    RetrievalActions map[string]int             `json:"retrievalActions"` // per policy; 0 explains a 0.0 rate
    BudgetViolations []string                   `json:"budgetViolations"` // policy names that overran
    NoDemandSessions []string                   `json:"noDemandSessions"` // session ids with Σo_i == 0
    Breakpoint    eval.BreakpointPlan           `json:"breakpoint"`    // carries NotPluginActionable
    Growth        eval.GrowthResult             `json:"growth"`
    PhaseChecked  int                           `json:"phaseChecked"`
    PhaseChecksSkipped bool                     `json:"phaseChecksSkipped"`
    Report        eval.Report                   `json:"report"`        // the §5.18 object, verbatim
    Regressions   []eval.Regression             `json:"regressions"`
}
```

`--out` writes this; `--write-baseline` writes the subset that the baseline file needs (everything above except `Report.GeneratedAt`, `Regressions`, and the wall-clock, so two runs are byte-identical).

### 9. `internal/eval/synth.go` — `Synthesize` and the corpus specs

```go
func Synthesize(seed int64, spec SynthSpec) Session
```

Determinism is the entire point: *"a golden test asserts that a given seed produces a byte-identical session, so replay numbers are comparable across commits"* (00-ARCHITECTURE §6.3).

- RNG: `math/rand/v2`, explicitly `rand.New(rand.NewPCG(uint64(seed), 0x9E3779B97F4A7C15))`. Never `rand.Int()` package-level, never map iteration for anything that reaches output.
- `ToolMix` is a `map[string]float64`; the generator sorts its keys lexicographically and builds a cumulative distribution over the sorted order, so Go's randomized map iteration cannot leak in.
- Turn `i` gets `TS = TS(i-1) + gap(i)` starting from `TS(0) = 1735689600000` (2025-01-01T00:00:00Z), with `gap(i) = 45_000` ms normally and `gap(i) = 3_600_000` ms when turn `i` is a changepoint turn. That is the only idle-gap mechanism, and it is principled rather than a shape flag: `SynthSpec` (fixed by §5.18) has no idle field, and §6.6 lists inter-turn time gaps as a changepoint feature while §5.4 states cold-cache windows occur only in idle gaps — so a long think-gap at a task boundary is exactly where the design says one belongs. The "long-idle" shape is therefore the shape with the highest changepoint density.
- Roles alternate `user, assistant, assistant, …`: turn 0 is `user`, then every 6th turn is `user`, the rest `assistant`.
- Token counts: an assistant turn is `400 + rng.IntN(400)`; a tool result is drawn from a per-tool base (`FileRead 1200–4800`, `Bash 300–2000`, `Grep 200–900`, `Edit 150–400`, `Test 2000–9000`, `Task 800–2400`) and multiplied by `(1 + TestOutputNoise)` for `Test`.
- `FileRereadRate` — with that probability a `FileRead` reuses a path already read (uniform over previously read paths) instead of minting `src/pkg<a>/file<b>.go`.
- `Changepoints` — the file namespace switches to a fresh `pkg` index at `Changepoints` evenly spaced turns; the switch turn indices go into `Meta["changepoints"]` as a comma-separated list.
- `Eliminations` — that many `record_eliminated` tool calls are placed at evenly spaced turns in the first 60% of the session, each `{target: <a previously read path>, approach: synthApproaches[i % 8], reason: synthApproaches[i%8] + " fails under the pinned dependency constraint"}`, where

```go
var synthApproaches = [8]string{
    "widen pool timeout", "retry with backoff", "disable prepared statements",
    "bump connection limit", "switch to transaction mode", "cache the lookup",
    "batch the writes", "move the check upstream",
}
```
- `SubagentCalls` — that many `Task` tool calls, each followed by an assistant turn whose text contains the subagent's returned summary and the literal tool_use_id of the `Task` call (so `DemandToolResult` fires).
- `DependencyChangeAt` — at each listed turn, a `Write` to `docker-compose.yml` or `package-lock.json` (alternating), which is what SP-09's staleness path will later key on. In SP-02 it only shapes the corpus.
- `CompactionAt` is copied to `Session.CompactionAt` verbatim.
- Decisions: every 9th assistant turn appends `" [decision:dec_" + hex12(HashBytes("qompack.eval.decision", []byte(strconv.FormatInt(seed,10)+"#"+itoa(turn)))) + "]"` to its text. The hash is keyed on the **seed**, not on `Session.ID`, precisely so that naming the session afterwards (below) cannot perturb a single decision id.
- **Naming, and why `Synthesize` cannot do it alone.** `SynthSpec` is fixed by §5.18 and has **no shape field**, so `Synthesize(seed, spec)` has no way to know it is producing "read-heavy". It therefore sets `Session.ID = "synth-" + strconv.FormatInt(seed, 10)`, `Synthetic = true`, and `Meta` = `{seed, spec (the compact JSON of the spec), generator: "eval.Synthesize/1", changepoints: "<comma-separated turn indices>"}` — no `shape` key. The shape lives on SP-02's own `NamedSpec`, and the unexported

```go
func synthesizeNamed(n NamedSpec) Session   // Synthesize(n.Seed, n.Spec), then:
                                            //   s.ID = n.Shape + "-" + itoa(n.Seed)
                                            //   s.Meta["shape"] = n.Shape
```

  is the **single** path by which a corpus session is produced. `WriteCorpus`, `--regen-corpus`, and `TestSynthesize_MatchesCommittedCorpus` all call `synthesizeNamed`, so the committed bytes have exactly one producer and the golden test compares like with like.

**The 24 committed sessions.** `internal/eval/corpus.go` holds `CorpusSpecs()` and `WriteCorpus(dir)` (which marshals each session with `json.MarshalIndent(s, "", "  ")` plus a trailing newline, writes via `paths.WriteAtomic`, and then writes `CORPUS.json`), driven by this exact table:

| # | File | Shape | Seed | Turns | ToolMix | Reread | TestNoise | CP | Elim | Sub | DepChangeAt | CompactionAt |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1–3 | `read-heavy-{1,2,3}.json` | read-heavy | 1001,1002,1003 | 180 | FileRead .60, Grep .20, Edit .12, Bash .08 | 0.45 | 0.0 | 3 | 2 | 0 | — | 90 |
| 4–6 | `test-output-{1,2,3}.json` | test-output-heavy | 1011,1012,1013 | 200 | Test .45, Bash .25, Edit .18, FileRead .12 | 0.20 | 0.8 | 4 | 3 | 0 | — | 100 |
| 7–9 | `refactor-{1,2,3}.json` | refactor-across-files | 1021,1022,1023 | 240 | Edit .40, FileRead .30, Grep .18, Test .12 | 0.35 | 0.2 | 5 | 3 | 0 | — | 80, 160 |
| 10–12 | `long-idle-{1,2,3}.json` | long-idle-gap | 1031,1032,1033 | 160 | FileRead .35, Bash .30, Edit .20, Grep .15 | 0.30 | 0.1 | 8 | 2 | 0 | — | 100 |
| 13–15 | `dep-change-{1,2,3}.json` | dependency-change | 1041,1042,1043 | 220 | FileRead .35, Test .25, Edit .25, Bash .15 | 0.30 | 0.4 | 4 | 5 | 0 | 60, 140 | 90, 170 |
| 16–18 | `subagent-{1,2,3}.json` | subagent-heavy | 1051,1052,1053 | 190 | Task .30, FileRead .30, Edit .22, Bash .18 | 0.25 | 0.1 | 3 | 2 | 8 | — | 95 |
| 19–21 | `thrash-{1,2,3}.json` | thrash-loop | 1061,1062,1063 | 210 | FileRead .28, Edit .28, Test .28, Bash .16 | 0.65 | 0.5 | 2 | 4 | 0 | — | 105 |
| 22–24 | `multi-compact-{1,2,3}.json` | multi-compaction | 1071,1072,1073 | 320 | FileRead .32, Edit .26, Test .22, Bash .12, Grep .08 | 0.40 | 0.3 | 6 | 6 | 3 | 200 | 70, 140, 210, 280 |

For `thrash-loop`, the generator forces the tool cycle `FileRead → Edit → Test(fail) → FileRead` for turns 40–150, which is what SP-15's Sequitur detector will later find. `Meta` always carries `changepoints` (the comma-separated changepoint turn indices), so the idle turns are recoverable from the committed file without re-running the generator.

24 sessions ≥ `eval.minSessions: 20`, so *"the config default is satisfiable offline"* (00-ARCHITECTURE §6.3) holds with margin.

`testdata/sessions/synthetic/CORPUS.json` is the manifest: `{"generator":"eval.Synthesize/1","regeneratedAfterPhase":0,"sessions":[{"file":…,"shape":…,"seed":…,"sha256":…,"turns":…,"compactionAt":[…]}]}`, written by `devtool replay --regen-corpus`.

### 10. `internal/eval/importer.go` — recorded corpora (§6.3 tier 2)

`Import(ctx, o)` walks `o.From` (default `filepath.Join(home, ".claude", "projects")`) for `**/*.jsonl`, parses each line as a Claude Code transcript record, and maps records to `Turn`/`ToolCall`:

- a record with `"type":"user"` → a `Turn{Role:"user"}` whose `Text` is the string content (array content is joined by `"\n"` over its `text` parts);
- `"type":"assistant"` → `Turn{Role:"assistant"}`; its `message.content[]` entries of type `tool_use` become `ToolCall{ID: id, Name: name, Args: input}`; entries of type `text` concatenate into `Text`;
- `"type":"user"` records carrying `tool_result` blocks attach `Result` to the matching `ToolCall` by `tool_use_id`;
- a record whose `isCompactSummary` field is `true`, or whose text begins with the compaction boundary marker, appends its turn index to `Session.CompactionAt`;
- `Paths` are extracted from the tool input's `file_path`, `path`, `notebook_path`, and `pattern` fields, normalized by `paths.Key`;
- `Tokens` come from `message.usage.output_tokens` when present, else `len(text)/4` rounded up;
- unknown record types are counted into `ImportReport.Skipped` and never fail the import.

`Redact(s Session) (Session, int)` scrubs, in this order, everywhere in `Text`, `Args`, `Result`, `Paths`, and `Meta`:

1. absolute home paths — `C:\Users\<name>`, `/Users/<name>`, `/home/<name>` → `<HOME>`;
2. e-mail addresses (`[\w.+-]+@[\w-]+\.[\w.]+`) → `<EMAIL>`;
3. PEM private-key blocks (`-----BEGIN [A-Z ]*PRIVATE KEY-----` … `-----END [A-Z ]*PRIVATE KEY-----`) → `<KEY>`;
4. `AKIA[0-9A-Z]{16}`, `ASIA[0-9A-Z]{16}`, `ghp_[A-Za-z0-9]{36}`, `gho_[A-Za-z0-9]{36}`, `github_pat_[A-Za-z0-9_]{22,}`, `sk-ant-[A-Za-z0-9-]{20,}`, `sk-[A-Za-z0-9]{20,}` → `<TOKEN>`;
5. `(?i)(password|secret|token|api[_-]?key)\s*[:=]\s*\S+` → `$1=<REDACTED>`;
6. `Bearer\s+[A-Za-z0-9._~+/-]{16,}` → `Bearer <REDACTED>`;
7. JWTs (`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`) → `<JWT>`;
8. connection strings with credentials (`[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`) → `scheme://<REDACTED>@`.

Returns the count of replaced spans. `Redact` is **idempotent**: a fuzz target asserts `Redact(Redact(x)) == Redact(x)`.

Output goes to `o.To`, defaulting to `QOMPACK_SESSIONS_DIR`, and `Import` errors with a message naming the variable when both are empty. The variable is read through the `config.Env.Getenv` function handed to `ImportCommand` (and through an `ImportOptions.Getenv func(string) string` field defaulting to `os.Getenv` when `Import` is called directly), never through a bare `os.Getenv` — otherwise `TestImport_RequiresSessionsDir` and `TestImportCommand_NoRedactRequiresEnv` would have to mutate real process environment and could not run in parallel. Files are written with `paths.WriteAtomic` as `<sessionID>.json` using the same indented encoding as the synthetic corpus. **Nothing is ever written under `testdata/sessions/recorded/` by code** — `.gitignore` already excludes it and the importer refuses a destination inside the repo working tree (detected by walking up for `.git`), erroring loudly. That is the mechanical version of *"real sessions never committed"*.

`ImportCommand(args, out, env)` parses `--from`, `--to`, `--limit`, `--no-redact` with a `flag.FlagSet`, prints the `ImportReport` as one JSON object, and returns `0` on success, `1` on error. `--no-redact` additionally requires `QOMPACK_EVAL_ALLOW_UNREDACTED=1`, otherwise it errors — an accidental unredacted import is a data-loss-of-privacy event, not a convenience.

`internal/cli/register_eval.go` (a **new file**, owned by SP-02, so it cannot collide with SP-14's `internal/commands` work) registers the verb pair `("eval", "import") → eval.ImportCommand` in SP-01's dispatch table.

### 11. `test/replay/` — the driver and the §11.3 gate

Files: `main.go`, `gate.go`, `phases.go`, `growth.go`, `report.go`.

`devtool replay` (implemented in the new file `tools/devtool/task_replay.go`) shells to `go run ./test/replay` with the CI flags.

```
test/replay [flags]
  --corpus     dir      default testdata/sessions/synthetic
  --baseline   ref      default testdata/baseline/phase0.json  ("" disables comparison)
  --policies   csv      default "stock,null,oracle"
  --out        file     write the full DriverReport JSON (default testdata/bench-replay.json, gitignored)
  --signoff    file     file containing the PR body, for the sign-off trailer scan
  --phase      int      highest merged phase whose exit criterion must hold (default 0)
  --growth     file     StatsSample JSON for the sublinear-growth guardrail
  --sketch     file     SketchHealth JSON for the bloom FP-rate watch-for
  --regen-corpus        regenerate testdata/sessions/synthetic + CORPUS.json and exit
  --write-baseline      write --baseline from this run and exit
  --max-wall   dur      default 15m; exceeded → exit 3
  --ci                  CI mode: phase checks may not be disabled by config (see below)
```

**`--baseline` accepts two forms.** A path (the normal case, and what `--write-baseline` writes), or a **git ref**, which is what 00-ARCHITECTURE §8 spells as `devtool replay --corpus testdata/sessions/synthetic --baseline develop`. A value that contains no path separator, does not end in `.json`, and resolves via `git rev-parse --verify <v>` is treated as a ref and read with `git show <ref>:testdata/baseline/phase0.json`; anything else is a path. Both forms produce the same in-memory baseline map, so the gate logic is identical. The `git` invocation is the driver's only subprocess and lives in `test/replay/gate.go`; it is skipped entirely when the value is a path, so the unit tests never shell out.

**`eval.replayOnPhaseGate`.** The Appendix C key is read, not decorative: when `cfg.Eval.ReplayOnPhaseGate` is `false` the driver skips the `--phase` checks, prints a `Loud` warning, and sets `DriverReport.PhaseChecksSkipped = true`. Under `--ci` (which the CI job always passes) that combination is a **hard failure with exit 5** — a developer may turn the phase checks off in their own `.qompack/config.json` for a fast local loop; nobody may turn them off for a pull request. The 2% comparison, the growth guardrail and the watch-fors are unaffected by the key and always run.

**The 2% rule.** For each policy and each metric in `MetricsOf`, with `b` = baseline value and `v` = observed:

```
worse = (direction == DirHigherBetter) ? (v < b) : (v > b)
rel   = |v - b| / max(|b|, epsilon)          // epsilon = 1e-9
regression = worse && ( |b| >= absFloor ? rel > 0.02 : |v - b| > absTol )
```

`absFloor` is `1e-6`; `absTol` is `0.02` for ratio metrics (`fraction_of_opt`, `file_set_jaccard`, `decision_preservation`, `retrieval_hit_rate`, `same_decision`) and `1.0` for count metrics. `DeltaPct = (v − b) / max(|b|, epsilon) * 100`, signed.

**The sign-off trailer.** A regression is `Allowed` only when `--signoff`'s content contains a line matching

```
(?mi)^sign-off:\s*(?P<metric>[a-z0-9_]+)\s*=\s*(?P<delta>[+-]?[0-9.]+%)\s+(?P<reason>\S.*)$
```

whose `metric` equals the regressing metric name **and** whose `reason` is at least 10 characters. Any unallowed regression fails the job, printing a table of `metric | policy | baseline | observed | delta% | allowed`. The failure message ends with the exact trailer line the author would have to add — a gate that tells you how to satisfy it is a gate people use rather than route around.

**Phase exit criteria (`phases.go`).** `var phaseChecks = map[int]func(Context) error` with exactly one entry today:

```go
0: func(c Context) error {
    // §10 Phase 0: "a single number for stock behaviour, reproducible across at
    // least 20 real sessions."
    if c.Report.Sessions < c.Cfg.Eval.MinSessions {
        return fmt.Errorf("phase 0: %d sessions replayed, eval.minSessions requires %d",
            c.Report.Sessions, c.Cfg.Eval.MinSessions)
    }
    if _, ok := c.Report.Policies["stock"]; !ok {
        return errors.New(`phase 0: no "stock" policy in the report; ` +
            "the baseline number is stock behaviour by definition")
    }
    // Reproducibility: a second full replay of the same corpus must produce a
    // byte-identical canonical metric map.
    if !bytes.Equal(c.CanonicalFirst, c.CanonicalSecond) {
        return fmt.Errorf("phase 0: replay is not reproducible; first and second runs "+
            "differ:\n%s", firstDiffLine(c.CanonicalFirst, c.CanonicalSecond))
    }
    return nil
}
```

Later subplans append `1:`…`6:` to this map; the driver runs every entry `≤ --phase`. SP-02 documents that contract in `docs/adr/0002-replay-methodology.md` so SP-08 onward add a function instead of a new gate.

**Sublinear store growth (`growth.go`).** §11.3 says *"Store growth sublinear in session length after dedup"*. `StatsSample` carries both `Turn` (session length) and `RawBytes` (content produced), and the check uses **`RawBytes` as the regressor with `Turn` as a validity guard**, for a stated reason: turn count alone is a bad x-axis (one 40 MB test run and one 200-byte `Grep` are both "one turn"), while raw bytes is the quantity dedup is actually asked to beat, and raw bytes grows monotonically with turns in any real session. So `eval.CheckSublinearGrowth(samples)`:

1. sorts by `Turn` ascending and drops any sample with `RawBytes ≤ 0` or `Bytes ≤ 0`;
2. **rejects the series as `inconclusive` if `RawBytes` is not non-decreasing in `Turn`** (`Reason: "rawBytes not monotone in turn; the proxy for session length is invalid"`) — this is what keeps the substitution honest rather than convenient;
3. fits `ln(Bytes) = α·ln(RawBytes) + c` by ordinary least squares and returns `Exponent = α`, `Sublinear = α ≤ 0.95`.

It refuses to judge with fewer than 6 usable samples or a `RawSpan` (max/min `RawBytes`) below 8× (`Reason` explains, `Sublinear = false`, and the gate reports it as `inconclusive`, which fails the job — an unmeasurable guardrail is not a passing guardrail). In wave 1 the samples come from `testdata/golden/eval/growth/stats-growth.json` (W-2 fixture, created by this subplan, 8 samples with α ≈ 0.62). From SP-06 onward the driver swaps in a real provider that walks a replayed session through `store.Stats`; the fixture stays as the shape contract and the wave-2 verification re-runs the same check against the real store, per Rule W-2.

**Watch-fors (§11.4).**
- *Bloom FP rate as a first-class metric.* `--sketch` supplies `eval.SketchHealth`; the gate emits `bloom_fp_rate` and `bloom_fill_ratio` alongside the score metrics, subject to the same 2% rule, plus a hard ceiling: `EstFPRate > 0.10` fails outright with the §11.4 sentence quoted in the error (*"At 1% they are safe; at 10% the agent starts skipping viable approaches"*). Wave-1 fixture: `testdata/golden/eval/growth/health.json` with `{fillRatio: 0.18, estFPRate: 0.006}`.
- *Replay overfitting.* `CORPUS.json.regeneratedAfterPhase` is compared against `--phase`. When `--phase > regeneratedAfterPhase + 2`, the gate fails with `corpus stale: re-collect sessions under the current policy (§11.4)` and points at `docs/adr/0003-replay-overfit-recollection.md`, which specifies the protocol: re-run `--regen-corpus` with bumped seeds (`+100` per regeneration), re-import the recorded corpus, re-write `testdata/baseline/phase<N>.json`, and record the new `regeneratedAfterPhase`. This makes "re-collect sessions periodically" a mechanical schedule instead of a good intention.

**`testdata/baseline/phase0.json`** — the committed Phase-0 answer:

```json
{
  "generator": "test/replay/1",
  "corpus": "testdata/sessions/synthetic",
  "corpusTier": "synthetic",
  "corpusSHA256": "<sha256 of CORPUS.json>",
  "sessions": 24,
  "latency": "modelled",
  "policies": {
    "null":   { "<all 19 keys of MetricsOf>": 0.0 },
    "oracle": { "fraction_of_opt": 1.0, "<the other 18 keys>": 0.0 },
    "stock":  { "fraction_of_opt": 0.0,  "<the other 18 keys>": 0.0 }
  }
}
```

Each policy object holds **exactly** the 19 keys of `MetricsOf`, no more and no fewer (a gate test asserts the key sets are equal, so a new metric cannot land without a baseline for it). All floats are 6-decimal rounded; map keys are sorted by `encoding/json`. The two watch-for metrics live in a sibling `"watchFor"` object, not inside a policy, so the 19-key assertion stays exact. `Context` for the phase checks is `struct{ Report eval.Report; Driver DriverReport; Cfg config.Config; CanonicalFirst, CanonicalSecond []byte; Corpus CorpusManifest; Growth eval.GrowthResult; Sketch eval.SketchHealth }`. `CanonicalFirst`/`CanonicalSecond` are the two `json.Marshal` renderings of the sorted `map[policy]map[metric]float64` produced by replaying the corpus twice in the same process (the second replay reuses the loaded sessions but a **freshly constructed** harness, so a stateful bug in the percentile pool shows up as a diff rather than hiding). The single number Phase 0 asks for is `policies.stock.fraction_of_opt`; the placeholder `0.0` values above are replaced by the real run in commit 7 and are never hand-edited.

### 12. CI wiring

`.github/workflows/ci.yml`, `replay-gate` job — SP-01 created it as a placeholder; SP-02 fills the step body:

```yaml
      - run: go run ./tools/devtool replay
             --corpus testdata/sessions/synthetic
             --baseline testdata/baseline/phase0.json
             --phase 0
             --growth testdata/golden/eval/growth/stats-growth.json
             --sketch testdata/golden/eval/growth/health.json
             --signoff "$PR_BODY_FILE"
             --max-cpu 2m
             --ci
```

`tools/devtool/task_replay.go` forwards **every** argument after `replay` verbatim to `go run ./test/replay` and propagates the child's exit code unchanged, so the flag surface documented in §11 is the flag surface CI uses and there is no second place to keep in sync.

with a preceding step writing `${{ github.event.pull_request.body }}` to `$PR_BODY_FILE` (empty file on push events, which means no regression can be signed off on a direct push — correct). SP-02 also flips `replay-gate` to a required check on `develop` at the end of wave 1, as 00-ARCHITECTURE §8 states it *"cannot be required before SP-02 and SP-05 exist"*.

### 13. Error handling per failure mode

| Failure | Behaviour |
|---|---|
| corpus directory empty or missing | `Load` returns `fmt.Errorf("eval: no sessions in %s: %w", dir, core.ErrNotFound)`; driver exits 2 |
| a corpus file fails to parse | hard error naming file and offset; never skipped |
| policy returns a keep-set over budget | clamp for scoring, `log.Loud`, record `budget_violation: <policy>` in the report, gate fails |
| `Σ o_i == 0` for a session | `FractionOfOPT = 1.0`, session flagged `noDemands: true` in the report; the gate fails if more than 25% of the corpus is so flagged (a corpus with no demands measures nothing) |
| DP too large | 1/2-approximation, `OPTDetail.Exact = false`, gate WARN naming the session |
| `--baseline` file missing | comparison disabled with a printed WARN; the phase check still runs; exit 0 only when `--baseline ""` was explicit, else exit 2 |
| growth samples insufficient | `inconclusive` → gate fails |
| live mode requested without the env var | error before any work; never silently degrades to deterministic |
| import destination inside the repo | error; nothing written |
| `--max-wall` exceeded | driver prints elapsed time per session and exits 3 |
| `eval.replayOnPhaseGate: false` under `--ci` | `Loud`, `phaseChecksSkipped: true`, exit 5 |
| `--baseline <ref>` where `git rev-parse` fails | error naming the ref and the two accepted forms; exit 2 |
| `opt` map missing an entry for a compaction turn in `ScoreRun` | `Loud`, zeroed `Score`, gate fails; a test asserts it never happens on the corpus |
| growth samples not monotone in `Turn` | `inconclusive` → gate fails, `Reason` names the invalid proxy |

### 14. Performance budgets (SP-02-local; named so CI can cite them)

| ID | What | Budget |
|---|---|---|
| **E-1** | `devtool replay` over the 24-session corpus × 3 policies, deterministic | CPU < 120 s (gate flag `--max-cpu`; `--max-wall` stays at its 15 m default as the liveness bound — the post-V2 hardening round split cost from liveness) |
| **E-2** | `BenchmarkBeladyDetail_400Turns` | ≤ 250 ms/op |
| **E-3** | `BenchmarkSynthesize_320Turns` | ≤ 50 ms/op |
| **E-4** | `BenchmarkCompare_400Actions` (Levenshtein dominated) | ≤ 20 ms/op |
| **E-5** | `BenchmarkBreakpointOPT_256Candidates` (markers=4) | ≤ 15 ms/op |

`benchstat` baselines land in `testdata/bench-baseline.txt` per 00-ARCHITECTURE §7.

---

## Test plan (TDD)

Every test below is written **before** the code it exercises, run once to observe the failure, then made to pass. Package `eval` sits in the **85% line-coverage** group (00-ARCHITECTURE §6.4); the suite below exceeds it. `assert` is banned — use `testify/require`. Every time-dependent test takes `testutil.FakeClock`.

### `internal/eval/blocks_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestBlocks_PositionsAreCumulative` | 3-turn hand-built session, turn tokens 100/200/300, no tool calls | `Blocks(s, 3)` | 3 blocks with `Pos` = 0, 100, 300 and `Tokens` = 100, 200, 300 |
| `TestBlocks_FileBlockPerDistinctKey` | two `FileRead` calls on `src/A.ts` and `SRC/a.ts` | `Blocks(s, 2)` | exactly one `BlockFile` with `ID == "file:src/a.ts"` on Windows/macOS keying |
| `TestBlocks_DecisionMarkerExtracted` | assistant turn text `"done [decision:dec_abc123def456]"` | `Blocks` | one `BlockDecision` with `ID == "dec:dec_abc123def456"`, `Tokens == 128` |
| `TestDemands_OnlyPreCompactionBlocks` | file read at turn 2, re-read at turn 9, compaction at 5 | `Demands(s, 5, 25)` | exactly one demand `{Turn: 9, BlockID: "file:…", Kind: DemandFileContent}` |
| `TestDemands_DeduplicatesWithinTurn` | one turn touching the same path in two tool calls | `Demands` | one demand |
| `TestDemands_EliminationMatchByApproachClass` | `record_eliminated{target:"src/auth.ts", approach:"Widen  Pool Timeout"}` at turn 3; turn 12 has an `Edit` on `src/auth.ts` with args `{"approach":"widen-pool-timeout"}` | `Demands(s,5,25)` | one `DemandElimination` |
| `TestApproachClass_Normalization` (property, `rapid`) | random strings | `approachClass` | output matches `^[a-z0-9-]*$` and is idempotent |

### `internal/eval/belady_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestBelady_UnitWeightsMatchesClassicBelady` | 6 blocks, all 256 tokens, demands: b1×3, b2×2, b3×1, b4..b6×0; budget 768 (3 slots) | `BeladyDetail` | `IDs == {b1,b2,b3}` sorted, `Tokens == 768`, `OPTDetail.Value == 6`, `Exact == true` |
| `TestBelady_KnapsackBeatsGreedyDensity` | blocks of 10/6/5 tokens with 6/5/4 demands each, `BeladyOptions{Granularity: 1, K: 20, MaxDPCells: 1<<20}`, budget 11 | `BeladyDetail` | picks the `{6,5}+{5,4}` pair (value 9, weight 11), not the single `{10,6}`; `Granularity: 1` is explicit so the assertion is about the DP and not about rounding |
| `TestBelady_BudgetNeverExceeded` (property, `rapid`) | random 1–200 blocks, random budget 0–100 000 | `BeladyDetail` | `KeepSet.Tokens ≤ budget` always |
| `TestBelady_Deterministic` | `read-heavy-1.json` | `BeladyDetail` run twice | identical `IDs` slices |
| `TestBelady_ZeroValueBlocksPruned` | 500 blocks, only 3 demanded | `OPTDetail.Candidates` | `== 3` |
| `TestBelady_PMinIsEarliestDropped` | 4 blocks at `Pos` 0/100/300/600, keep `{b1,b4}` forced by demands+budget | `KeepSet.P` | `== 100` |
| `TestBelady_FallbackWhenDPTooLarge` | `MaxDPCells: 10`, 50 blocks | `BeladyDetail` | `Exact == false`, value ≥ ½ of the exact value computed with a large cap |
| `TestBelady_ContextCancelled` | cancelled `context.Context` | `BeladyDetail` | returns `ctx.Err()` before allocating the DP table |
| `BenchmarkBeladyDetail_400Turns` | `multi-compact-1.json` | — | budget **E-2**: ≤ 250 ms/op |

### `internal/eval/breakpoint_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestBreakpointOPT_KnownOptimum` | caps `{100, 100, 100, 900}`, candidates `{0,100,500,900}`, fed straight to the unexported `breakpointPlan` | `breakpointPlan(caps, cand, 1)` then `(…, 2)` | markers 1 ⇒ `Positions == [900]`, `CachedReads == 900` (beats `[100]`'s `400` and `[500]`'s `500`); markers 2 ⇒ `Positions == [100, 900]`, `CachedReads == 1200` (`100·3 + 900`) |
| `TestBreakpointOPT_MoreMarkersNeverWorse` (property) | random sessions, `m ∈ [1,6]` | — | `value(m+1) ≥ value(m)` |
| `TestBreakpointOPT_NoteIsAlwaysTheDisclaimer` | any session | — | `Plan.Note == NotPluginActionable` |
| `TestBreakpointOPT_MarkersZero` | any | `markers = 0` | `Positions` empty, `CachedReads == 0`, no error |
| `BenchmarkBreakpointOPT_256Candidates` | `multi-compact-3.json`, markers 4 | — | budget **E-5**: ≤ 15 ms/op |

### `internal/eval/policy_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestStockPolicy_TopFiveFilesFiveKEach` | session with 9 distinct `FileRead` files, each producing an 8 000-token result, budget 40 000 | the keep-set contains exactly 5 `file:` ids — the 5 most recent by turn — and their combined contribution is `5 × min(8_000, 5_000) == 25_000`; `P == 0`. Asserted on the file-block subset, not on `KeepSet.Tokens`, because stage (b) legitimately adds message and tool-result blocks on top |
| `TestStockPolicy_PreservationMinimums` | 30 assistant turns of 500 tokens each, no tool calls | walks back exactly 20 turns: `msgTokens == 10_000` (the `minTokens` floor) with `msgs == 20 ≥ 5`, under the 40 000 cap; keeping a 21st turn would be a §2.3 violation |
| `TestStockPolicy_UsedNeverGoesNegative` | 9 files × 8 000 tokens plus 30 turns × 500, budget 1 000 (forces stage (c) to evict) | `KeepSet.Tokens ≥ 0` and `== Σ contrib` at return; regression test for the double-count/over-subtract bug the `contrib` map exists to prevent |
| `TestStockPolicy_PIsZero` | any | `KeepSet.P == 0` for every session in the corpus |
| `TestNullPolicy_Empty` | any | `IDs == nil`, `Tokens == 0` |
| `TestRegisterPolicy_DuplicatePanics` | register `"stock"` twice | panics |
| `TestPolicyNames_Sorted` | — | `["null","stock"]` after commit 1; the expectation is updated to `["null","oracle","stock"]` in **commit 2**, when `belady.go`'s `init` registers the oracle |
| `TestOraclePolicy_ScoresExactlyOne` | all 24 corpus sessions | `ScoreRun` `FractionOfOPT == 1.0` exactly. **Lives in `score_test.go` and lands in commit 4**, because it needs both the oracle (commit 2) and the scorer (commit 4); the policy table lists it here for completeness |

### `internal/eval/replay_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestReplay_NullPolicyInjectsRepairForEveryDemand` | hand-built session, 4 demands after the compaction | compacted run has exactly 4 more actions than the baseline |
| `TestReplay_OracleFewerRepairsThanStock` | all 24 sessions | `len(oracle.Actions) ≤ len(stock.Actions)` for every session |
| `TestReplay_LiveModeRefusedWithoutEnv` | `Deterministic: false`, env unset | error mentioning `QOMPACK_EVAL_LIVE` |
| `TestReplay_LiveRunnerAbsent` | `Deterministic:false`, env set, nil runner | `errLiveRunnerAbsent` |
| `TestReplay_LatencyModelAnchors` | `residual = 167_000` and `residual = 15_000` | pause `28_050 ms` and `5_250 ms`; the test comment cites §6.7's "~15–40s" and §8.5's "10–20K tokens" |
| `TestReplay_DeterministicAcrossRuns` | every corpus session × 3 policies, replayed twice | `go-cmp` equal `Run`s |
| `TestReplay_HorizonRespected` | `K = 5`, compaction at 10, 100-turn session | the horizon is `Demands(s, 10, 15)` ⇒ turns 11–14; no demand at turn ≥ 15 produces a repair, and `Run.Horizon == 5` |

### `internal/eval/divergence_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestCompare_IdenticalRuns` | same run twice, `Horizon: 20` | `{FirstDivergenceTurn:20, FileSetJaccard:1, ToolEditDistance:0, SameDecision:true, DecisionPreservation:1, RedundantReads:0, ReAttempts:0}` — `FirstDivergenceTurn == Horizon`, never `-1` |
| `TestCompare_FirstDivergenceInRange` (property, `rapid`) | random run pairs | result always in `[0, Horizon]`, and `MetricDirection("first_divergence_turn") == DirHigherBetter` is consistent with "never diverged is best" |
| `TestCompare_FirstDivergenceIsRelativeToCompaction` | compaction at turn 40, first differing action at turn 43 | `FirstDivergenceTurn == 3` |
| `TestCompare_JaccardBothEmpty` | horizons with no paths | `1.0` |
| `TestCompare_JaccardHalf` | A `{a,b}`, B `{b,c}` | `1.0/3.0` |
| `TestCompare_EditDistanceKnown` | `[read,edit,test]` vs `[read,test]` | `1` |
| `TestCompare_EditDistance_Property` (`rapid`) | random sequences | symmetric, `≤ max(len)`, `0` iff equal |
| `TestCompare_RedundantReadsCanBeNegative` | compacted branch with fewer re-reads | negative value, and `MetricDirection("redundant_reads") == DirLowerBetter` |
| `TestCompare_DecisionPreservationDenominatorZero` | no decisions in the uncompacted horizon | `1.0` |
| `BenchmarkCompare_400Actions` | — | budget **E-4**: ≤ 20 ms/op |

### `internal/eval/score_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestScoreRun_FractionIsMicroAveraged` | 2 events with `(v,o) = (2,4)` and `(6,6)` | `8/10 == 0.8`, not `mean(0.5, 1.0) == 0.75` |
| `TestScoreRun_NoDemandsIsOne` | `Σo == 0` | `1.0` |
| `TestScoreRun_OverBudgetClamped` | policy keeping 90 000 tokens against a 40 000 budget | clamped to `≤1.0` and a `budget_violation` recorded |
| `TestScoreRun_RewriteTokensSection52TableA` | `n = 167_000`, `p_min = 150_000`, `w`/`r` from `config.Defaults()` | `rewrite_span_tokens == 17_000` (the doc's Rewrite column), `rewrite_tokens == 21_250`, `forfeited_discount_tokens == 15_300` |
| `TestScoreRun_RewriteTokensSection52TableB` | `n = 167_000`, `p_min = 10_000` | `rewrite_span_tokens == 157_000`, `rewrite_tokens == 196_250`, `forfeited_discount_tokens == 141_300`; the test asserts `span_B / span_A == 157.0/17.0` and carries a comment recording that §5.2's "30×" is the benefit ratio (60K vs 2K dropped), not the rewrite ratio |
| `TestScoreRun_NoHardcodedMultiplier` | `cfg.Scheduler.Cache.WriteMultiplier = 2.0` | `rewrite_tokens` doubles (proves D11 compliance at runtime, in addition to the `nomagic` pass) |
| `TestScoreRun_Percentiles_NearestRank` | `PauseMS = [10,20,30,40]` | `P50 == 20`, `P95 == 40`, `Max == 40` |
| `TestScoreRun_PercentilesEmpty` | empty slices | all zeros, no panic |
| `TestScoreRun_RetrievalHitRateZeroActions` | no retrieval actions | `0.0` |
| `TestMetricsOf_CoversEveryDirection` | — | every key of `MetricsOf` has a `MetricDirection` and vice versa |
| `TestReport_GeneratedAtUsesClock` | `FakeClock` at a fixed instant | `Report.GeneratedAt` equals it |
| `TestReport_PercentilesRecomputedNotAveraged` | `ScoreRun` called for two sessions with disjoint pause distributions, then `Report` | `P95` equals the P95 of the hand-concatenated slice, and differs from the mean of the two per-session P95s |
| `TestReport_UnequalSessionCountsError` | `stock` with 24 scores, `null` with 23 | error; no partial report |
| `TestReport_MissingPoolIsAnError` | `Report` called for a policy with no preceding `ScoreRun` | error, not silent zeros |
| `TestScoreRun_UsesRunDemandsNotRecomputed` | a `Run` whose `Demands[0]` is deliberately truncated to one entry | `FractionOfOPT` reflects that one demand — proving `ScoreRun` reads `Run.Demands` and never reaches for a `Session` it does not have |

### `internal/eval/synth_test.go`

| Test | Expected |
|---|---|
| `TestSynthesize_ByteIdenticalForSeed` | `Synthesize(1001, spec)` marshalled twice ⇒ identical bytes |
| `TestSynthesize_MatchesCommittedCorpus` | for all 24 entries of `CorpusSpecs()`, regenerating equals the committed file byte-for-byte (this is the §6.3 golden stability test) |
| `TestSynthesize_ToolMixOrderIndependent` | a spec whose `ToolMix` is built in a different insertion order produces an identical session |
| `TestSynthesize_ShapeInvariants` | read-heavy ⇒ ≥ 55% `FileRead`; test-output ⇒ ≥ 40% `Test`; thrash ⇒ the `FileRead,Edit,Test` trigram repeats ≥ 20 times; subagent ⇒ exactly `SubagentCalls` `Task` calls; dep-change ⇒ a `Write` at each `DependencyChangeAt`; multi-compact ⇒ `len(CompactionAt) == 4`; long-idle ⇒ exactly 8 inter-turn gaps of 3 600 000 ms and none in any other shape's first 3 turns |
| `TestSynthesize_EveryCompactionHasDemands` | for all 24 sessions and every `at`: `len(Demands(s, at, at+20)) > 0` **and** `BeladyDetail(…, DefaultKeepBudget, DefaultBeladyOptions()).Value > 0`. The second half is what makes the corpus measure anything: demands that no affordable block can satisfy give `Σo_i == 0`, which scores every policy `1.0` and hides all signal |
| `TestCorpus_CountAtLeastMinSessions` | `len(CorpusSpecs()) >= config.Defaults().Eval.MinSessions` ⇒ 24 ≥ 20 |
| `TestCorpus_ManifestHashesMatch` | every `CORPUS.json` sha256 matches the file on disk |
| `TestSynthesize_WriteCorpus` | no-ops and passes unless `QOMPACK_EVAL_WRITE_CORPUS=1`; with it set, calls `WriteCorpus("testdata/sessions/synthetic")`. This is the single writer of the committed corpus and the same function `--regen-corpus` calls |
| `BenchmarkSynthesize_320Turns` | budget **E-3**: ≤ 50 ms/op |

### `internal/eval/importer_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestImport_MapsToolUseAndResult` | 6-line JSONL fixture at `testdata/fixtures/transcripts/basic.jsonl` | 1 session, 4 turns, 2 tool calls with `Result` attached by `tool_use_id` |
| `TestImport_CompactBoundaryBecomesCompactionAt` | fixture with `isCompactSummary: true` at record 4 | `CompactionAt == [3]` |
| `TestImport_UnknownRecordSkippedNotFatal` | fixture with a `{"type":"nonsense"}` line | success, `Skipped` length 1 |
| `TestImport_RefusesDestinationInsideRepo` | `--to` inside the working tree | error, zero files written |
| `TestImport_RequiresSessionsDir` | no `--to`, `QOMPACK_SESSIONS_DIR` unset | error naming the variable |
| `TestRedact_AllEightRules` | a fixture string containing one instance of each rule | 8 replacements, none of the original secrets present in the output |
| `TestRedact_Idempotent` (property + fuzz `FuzzRedact`) | random bytes | `Redact(Redact(x)) == Redact(x)` |
| `TestRedact_PreservesTurnCountAndTokens` | any session | turn count, roles and `Tokens` unchanged |
| `TestImportCommand_NoRedactRequiresEnv` | `--no-redact` without the env var | exit code 1, message names `QOMPACK_EVAL_ALLOW_UNREDACTED` |

### `internal/eval/growth_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestCheckSublinearGrowth_Sublinear` | the committed fixture | `Exponent ≈ 0.62 ± 0.02`, `Sublinear == true` |
| `TestCheckSublinearGrowth_Linear` | `bytes == rawBytes` over 8 samples | `Exponent ≈ 1.0`, `Sublinear == false` |
| `TestCheckSublinearGrowth_TooFewSamples` | 3 samples | `Sublinear == false`, `Reason` mentions "at least 6" |
| `TestCheckSublinearGrowth_SpanTooSmall` | 8 samples spanning 2× | `Sublinear == false`, `Reason` mentions "8x" |

### `internal/eval/evalsuite_test.go`

Flips every `t.Skip` in SP-01's `evaltest.RunEvalSuite` off and runs it against `eval.New(...)`. It is a merge blocker if any skip remains (Rule W-1).

### `test/replay/gate_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestGate_NoRegressionPasses` | baseline == observed | exit 0, zero `Regressions` |
| `TestGate_TwoPercentBoundaryExclusive` | `fraction_of_opt` 0.500 → 0.4901 (−1.98%) and → 0.4899 (−2.02%) | first passes, second is a regression |
| `TestGate_LowerBetterMetricDirection` | `rewrite_tokens` 1000 → 1030 | regression |
| `TestGate_ImprovementNeverRegresses` | `rewrite_tokens` 1000 → 500 | not a regression |
| `TestGate_SignOffAllowsNamedMetricOnly` | body `sign-off: rewrite_tokens=+3.1% traded for a 9% first-divergence gain`, regressions in `rewrite_tokens` and `redundant_reads` | first `Allowed`, second not; exit non-zero |
| `TestGate_SignOffRejectsShortReason` | reason of 4 characters | not allowed |
| `TestGate_ZeroBaselineUsesAbsoluteTolerance` | baseline `redundant_reads: 0` (a `DirLowerBetter` count metric, so `absTol == 1.0`) → observed `2`, and separately → observed `1` | `2` is a regression (`|2−0| > 1.0`); `1` is not (`|1−0|` is not `> 1.0`). A ratio metric cannot exercise this rule from a `0.0` baseline: every ratio metric in `MetricsOf` is `DirHigherBetter`, so moving away from `0.0` is an improvement and `worse` is false before the tolerance is ever consulted |
| `TestGate_Phase0ExitCriterion` | report with 19 sessions | fails naming `eval.minSessions` |
| `TestGate_Phase0Reproducibility` | corpus replayed twice | canonical metric maps byte-equal |
| `TestGate_BloomFPCeiling` | `estFPRate: 0.11` | fails, message contains the §11.4 sentence |
| `TestGate_CorpusStaleness` | `regeneratedAfterPhase: 0`, `--phase 3` | fails pointing at ADR 0003 |
| `TestGate_GrowthInconclusiveFails` | 3-sample growth file | non-zero exit |
| `TestGate_MaxWallExceeded` | `--max-wall 1ns` | exit 3 |
| `TestGate_BreakpointDisclaimerPrinted` | any run | stdout contains `NotPluginActionable` verbatim |
| `TestGate_PhaseChecksMayNotBeDisabledInCI` | `cfg.Eval.ReplayOnPhaseGate = false`, `--ci` | exit 5, `phaseChecksSkipped: true`, message names the config key; without `--ci` the same input exits 0 with a WARN |
| `TestGate_BaselineRefForm` | `--baseline develop` with a fake `git` on `PATH` returning a known blob | resolves to the same map as the file form; an unresolvable ref exits 2 naming both accepted forms |
| `TestGate_WatchForKeysDisjointFromMetricsOf` | — | `watchForDirection` keys ∩ `MetricsOf` keys is empty, so the 19-key baseline assertion stays exact |
| `TestGate_BaselineHasExactlyNineteenKeysPerPolicy` | committed `phase0.json` | each policy object's key set equals `MetricsOf`'s key set |

### `test/replay/e2e_test.go`

`TestReplayDriver_EndToEnd` builds the driver with `go run`, runs it against the committed corpus and baseline with `--phase 0`, and asserts exit 0 plus a parseable report JSON whose `policies.stock.fraction_of_opt` equals the committed baseline exactly.

### Fixtures created by this subplan

- `testdata/sessions/synthetic/*.json` (24) + `CORPUS.json`
- `testdata/baseline/phase0.json`
- `testdata/golden/eval/growth/stats-growth.json` (8 samples, α ≈ 0.62)
- `testdata/golden/eval/growth/health.json` (`fillRatio 0.18`, `estFPRate 0.006`)
- `testdata/fixtures/transcripts/{basic,compact-boundary,unknown-record,secrets}.jsonl`
- `testdata/golden/eval/divergence/{identical,repairs,decision-loss}.json`
- `testdata/corpora/evalredact/` — fuzz seed corpus for `FuzzRedact`

---

## Commit plan

Work happens **only** on `feat/sp02-replay-harness-belady-baseline`, cut from `develop` with SP-01 already merged:

```
git checkout develop && git pull && git checkout -b feat/sp02-replay-harness-belady-baseline
```

Exactly **7 commits**. Each compiles and passes `go run ./tools/devtool test` for the packages it touches. Conventional Commits per 00-ARCHITECTURE §10; footer names the subplan, gaps and sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(eval): session model, block/demand derivation, and the policy registry`

- [ ] Write `internal/eval/blocks_test.go` and `internal/eval/policy_test.go` first; run `go test ./internal/eval/...` and confirm they **fail** (`ErrNotImplemented` / nil map).
- [ ] Add `internal/eval/types.go` (every §5.18 type with JSON tags, plus `Options`, `New`, `LatencyModel`, `DefaultLatencyModel`, `Block`, `Demand`, constants).
- [ ] Add `internal/eval/blocks.go` (`Blocks`, `Demands`, `approachClass`).
- [ ] Add `internal/eval/policy.go` (`RegisterPolicy`, `PolicyByName`, `PolicyNames`, `NewStockPolicy`, `NewNullPolicy`, `internal/eval/hostconst.go`; `NewOraclePolicy` is added in **commit 2** and registered from an `init` in `belady.go`).
- [ ] Add `internal/eval/importgraph_test.go` asserting that the transitive import closure of `internal/eval` contains no `internal/` package outside `{core, paths, config, logging, obs}`. Walk it with `go/build.Import` from the standard library — **not** `go list` via `os/exec` — so the `security` job's `os/exec` allowlist (`internal/daemon`, `internal/cli`, `tools/`) is respected and the test runs with no toolchain subprocess.
- [ ] Run: `go run ./tools/devtool fmt lint test` — all green.
- [ ] Files: `internal/eval/{types,blocks,policy,hostconst}.go`, `internal/eval/{blocks,policy,importgraph}_test.go`.

```
feat(eval): session model, block/demand derivation, and the policy registry

Blocks/Demands are the only notion of "was needed after compaction" in the
harness, so they are defined before anything that consumes them. eval imports
foundation packages only, asserted by an import-graph test, which is what lets
L7 be built in wave 1 against no sibling's output.

Refs: SP-02, G8.1, §4.2, §11.1
```

### Commit 2 — `feat(eval): retrospective Belady OPT keep-sets and the §5.6 breakpoint measurement`

- [ ] Write `internal/eval/belady_test.go` and `internal/eval/breakpoint_test.go` first; confirm failure.
- [ ] Add `internal/eval/belady.go` (knapsack DP, deterministic ordering, 1/2-approximation fallback, `OPTDetail`, `oraclePolicy` + its `init` registration).
- [ ] Add `internal/eval/breakpoint.go` (`BreakpointOPT`, `BreakpointPlan`, `NotPluginActionable`).
- [ ] Run: `go test -run 'Belady|Breakpoint' -race ./internal/eval/...` then `go test -bench 'Belady|Breakpoint' ./internal/eval/...` and record E-2 / E-5 into `testdata/bench-baseline.txt`.
- [ ] Files: `internal/eval/{belady,breakpoint}.go`, `internal/eval/{belady,breakpoint}_test.go`, `testdata/bench-baseline.txt`.

```
feat(eval): retrospective Belady OPT keep-sets and the §5.6 breakpoint measurement

OPT is the 0/1-knapsack generalization of Belady to heterogeneous block sizes;
with unit weights it degenerates to classic Belady, so it is the ceiling §6.10
asks for rather than a substitute. Breakpoint placement is computed and
reported but carries a permanent not-plugin-actionable note (§5.6, §12).

Refs: SP-02, G8.1, G8.3, §6.10, §5.6
```

### Commit 3 — `feat(eval): counterfactual replay and the §4.2 divergence metrics`

- [ ] Write `internal/eval/replay_test.go` and `internal/eval/divergence_test.go` first; confirm failure.
- [ ] Add `internal/eval/replay.go` (`Load`, `Replay`, `BaselineRun`, repair rules, the `At`/`Demands`/`PrefixTokens`/`Horizon`/`FirstCompactionTurn` fill, latency-model fill, live-mode seam).
- [ ] Add `internal/eval/divergence.go` (`Compare` and the five §4.2 bullets).
- [ ] Run: `go test -race ./internal/eval/...`; `go test -bench Compare` and record E-4.
- [ ] Files: `internal/eval/{replay,divergence}.go`, `internal/eval/{replay,divergence}_test.go`, `testdata/golden/eval/divergence/*.json`.

```
feat(eval): counterfactual replay and the §4.2 divergence metrics

Deterministic mode applies the policy to the logged action sequence and repairs
each unsatisfied demand, giving a reproducible model-free estimate of D. Live
mode is a nil-by-default seam that errors rather than silently degrading, so a
CI run can never be mistaken for a model-backed one.

Refs: SP-02, G8.1, §4.2, §11.2
```

### Commit 4 — `feat(eval): fraction-of-OPT scoring and every §11.2 secondary metric`

- [ ] Write `internal/eval/score_test.go` first, including the §5.2 table reproductions and the `WriteMultiplier = 2.0` D11 test; confirm failure.
- [ ] Add `internal/eval/score.go` (`ScoreRun`, `Report`, `MetricsOf`, `MetricDirection`, percentile helper).
- [ ] Add `internal/eval/growth.go` (`CheckSublinearGrowth`, `StatsSample`, `GrowthResult`, `SketchHealth`) and `internal/eval/growth_test.go`.
- [ ] Add the two W-2 fixtures `testdata/golden/eval/growth/{stats-growth.json,health.json}`.
- [ ] Run: `go run ./tools/devtool lint test cover` — `eval` ≥ 85%.
- [ ] Files: `internal/eval/{score,growth}.go`, `internal/eval/{score,growth}_test.go`, the two fixtures.

```
feat(eval): fraction-of-OPT scoring and every §11.2 secondary metric

FractionOfOPT is micro-averaged across a session's compaction events. Rewrite
cost reads w from scheduler.cache.writeMultiplier at the use site — there is no
1.25 in this package — and the report emits rewrite and forfeited-discount
separately so §5.2's two quantities are never conflated. Latency metrics are
modelled in deterministic mode and labelled "modelled" in the report.

Refs: SP-02, G8.1, G8.3, §11.1, §11.2, §11.3, §5.2
```

### Commit 5 — `feat(eval): deterministic synthesizer and the 24-session synthetic corpus`

- [ ] Write `internal/eval/synth_test.go` first (including `TestSynthesize_MatchesCommittedCorpus`, which fails because the corpus does not exist yet); confirm failure.
- [ ] Add `internal/eval/synth.go` and `internal/eval/corpus.go` with the 24-row spec table.
- [ ] Generate the corpus once, from the main session, with the env-guarded writer test: `QOMPACK_EVAL_WRITE_CORPUS=1 go test -run TestSynthesize_WriteCorpus ./internal/eval/`. That test is permanent (it is also how commit 7's `--regen-corpus` flag does the work — the flag calls the same `eval.WriteCorpus(dir string) error` function), and it no-ops without the environment variable so CI can never rewrite the corpus it is measuring against.
- [ ] Commit `testdata/sessions/synthetic/*.json` (24) and `CORPUS.json`.
- [ ] Run: `go test -count=2 ./internal/eval/...` (twice, to prove determinism across process runs) and `go test -bench Synthesize` recording E-3.
- [ ] Files: `internal/eval/{synth,corpus}.go`, `internal/eval/synth_test.go`, `testdata/sessions/synthetic/**`.

```
feat(eval): deterministic synthesizer and the 24-session synthetic corpus

Eight shapes x three seeds spans the failure space named in the test-fixture
policy: read-heavy, test-output-heavy, refactor, long-idle, dependency-change,
subagent-heavy, thrash-loop and multi-compaction. 24 >= the eval.minSessions
default of 20, so the config default is satisfiable entirely offline. PCG with
an explicit seed and lexicographically ordered ToolMix keys keep the output
byte-identical across platforms and Go versions.

Refs: SP-02, §10 Phase 0, §11.4
```

### Commit 6 — `feat(eval): redacting importer for recorded corpora and qompack eval import`

- [ ] Write `internal/eval/importer_test.go` and the JSONL fixtures first; confirm failure.
- [ ] Add `internal/eval/importer.go` (`Import`, `Redact`, `ImportCommand`) and `internal/eval/fuzz_test.go` (`FuzzRedact`) with its seed corpus.
- [ ] Add `internal/cli/register_eval.go` wiring `qompack eval import`.
- [ ] Run: `go test ./internal/eval/... ./internal/cli/...`; `go test -fuzz FuzzRedact -fuzztime 60s ./internal/eval/`.
- [ ] Files: `internal/eval/{importer,fuzz_test}.go`, `internal/eval/importer_test.go`, `internal/cli/register_eval.go`, `testdata/fixtures/transcripts/*.jsonl`, `testdata/corpora/evalredact/*`.

```
feat(eval): redacting importer for recorded corpora and qompack eval import

Recorded transcripts are the fidelity tier that gates releases, and they carry
user code, prompts, home paths and secrets. Redaction is on by default, the
bypass requires a second environment variable, and the importer refuses any
destination inside the repository working tree, so "never committed" is
mechanical rather than a convention.

Refs: SP-02, §11.4
```

### Commit 7 — `test(replay): replay-gate driver, 2% rule, phase-exit assertions, growth guardrail`

- [ ] Write `test/replay/gate_test.go` and `test/replay/e2e_test.go` first; confirm failure.
- [ ] Add `test/replay/{main,gate,phases,growth,report}.go`.
- [ ] Add `tools/devtool/task_replay.go` implementing the registered `replay` task.
- [ ] Generate and commit `testdata/baseline/phase0.json` via `go run ./test/replay --write-baseline`.
- [ ] Fill the `replay-gate` step body in `.github/workflows/ci.yml` and mark it a required check on `develop`.
- [ ] Add `docs/adr/0002-replay-methodology.md` (metric definitions, direction table, the phase-registry contract for later subplans, the modelled-latency disclosure) and `docs/adr/0003-replay-overfit-recollection.md` (the §11.4 re-collection protocol and its mechanical schedule).
- [ ] Run: `go run ./tools/devtool ci-local` — `verify`, `test`, `cover`, `replay` all green.
- [ ] Files: `test/replay/**`, `tools/devtool/task_replay.go`, `testdata/baseline/phase0.json`, `.github/workflows/ci.yml`, `docs/adr/000{2,3}-*.md`.

```
test(replay): replay-gate driver, 2% rule, phase-exit assertions, growth guardrail

The gate is the mechanism that turns §11.3 from a sentence into a required
check: every metric has a direction, a regression beyond 2% needs a sign-off
trailer naming that exact metric, every merged phase's exit criterion is
re-asserted on every PR, store growth must be provably sublinear, and a corpus
more than two phases old fails the build rather than quietly overfitting.

Refs: SP-02, G8.1, G8.3, §11.3, §11.4, §10 Phase 0
```

---

## Subagent strategy

This subplan is **heavy**. Partition it across four parallel subagents after the main session has landed the shared types, and keep the commit sequence strictly serial in the main session.

**Main session, before dispatching anything (do not delegate this):**

1. Cut the branch.
2. Write `internal/eval/types.go` in full — every §5.18 type with its JSON tags (including `Run`'s five appended fields), `Options`/`New`/`LatencyModel`, the unexported `harness` and `latencyPool` structs, `LiveRunner`, `Block`, `Demand`, `BlockKind`, `DemandKind`, `BreakpointPlan`, `ImportOptions`, the constants, and the `Direction` enum. Commit nothing yet; the file must exist and compile before subagents start, because all four touch it and a concurrent rewrite of the shared type file is the one merge conflict that costs more than it saves.
3. Write `internal/eval/blocks.go` and `internal/eval/policy.go` (commit 1's implementation) yourself. They are small, and everything downstream depends on the exact `Block.ID` grammar and `Pos` semantics.
4. Land commit 1.

**Subagent A — OPT (`belady.go`, `breakpoint.go`, their tests).** Given: `types.go`, `blocks.go`. Returns: the two implementation files, the two test files, and the measured E-2/E-5 benchmark numbers as text. Must not touch any other file. Integration: main session runs the tests, records the benchmarks, lands commit 2.

**Subagent B — replay and divergence (`replay.go`, `divergence.go`, their tests, the three divergence goldens).** Given: `types.go`, `blocks.go`, `policy.go`. Returns: the two implementation files, tests, goldens, and the E-4 number. Depends on nothing from A (it uses `Policy`, not `Belady`), so it runs concurrently with A. Integration: main session lands commit 3 after commit 2.

**Subagent C — scoring, growth, and the synthesizer (`score.go`, `growth.go`, `synth.go`, `corpus.go`, their tests, the two W-2 fixtures).** Given: `types.go`, `blocks.go`. Returns: four implementation files, tests, fixtures, and the E-3 number. **Does not generate the corpus files** — it returns `CorpusSpecs()` and the generator; the main session runs the generation once so the 24 committed JSON files have a single writer and their bytes are reproducible from one process. Integration: main session lands commits 4 and 5.

**Subagent D — importer and gate (`importer.go`, `fuzz_test.go`, `internal/cli/register_eval.go`, `test/replay/**`, `tools/devtool/task_replay.go`, the JSONL fixtures, the two ADRs).** Given: `types.go` and the `MetricsOf`/`MetricDirection` signatures from C's contract (main session hands D the signatures verbatim before C finishes, so D can code against them). Returns: all of the above plus the CI step body. Integration: main session lands commits 6 and 7 and generates `phase0.json` itself.

**Stays in the main session, always:** the branch and all seven commits; `types.go`; corpus generation; `testdata/baseline/phase0.json` generation; the `.github/workflows/ci.yml` edit; the final full-suite run; the self-review checklist. Subagents never run `git commit`.

**Conflict rules:** each subagent owns a disjoint file set; the only shared file is `types.go`, which is frozen before dispatch. If a subagent needs a new exported type, it returns the request to the main session rather than editing `types.go` — that is the same discipline 00-ARCHITECTURE §0's amendment rule imposes between subplans, applied one level down.

---

## Exit criteria

### Quoted verbatim from `Qompack.md`

§10 Phase 0:

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

§11.3 guardrails owned by this subplan:

> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

§11.1:

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

### Local, measurable Definition of Done

- [ ] `testdata/baseline/phase0.json` exists, is committed, carries `"corpusTier": "synthetic"`, and contains a single `policies.stock.fraction_of_opt` value computed over **24** sessions (≥ the `eval.minSessions: 20` floor).
- [ ] That number is reproducible: `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline` twice in a row produces byte-identical files, and on a machine with a different OS produces the same metric values.
- [ ] The §10 "real sessions" reading is discharged, not quietly substituted: `docs/adr/0002-replay-methodology.md` states that the committed number is synthetic-corpus, documents the exact command that produces the recorded-corpus number, and names who runs it and when (pre-release, per 00-ARCHITECTURE §6.3 tier 2).
- [ ] `oracle` scores `fraction_of_opt == 1.0` on every session. `null` scores `0.0` on every session — guaranteed, not hoped for, by `TestSynthesize_EveryCompactionHasDemands` asserting `OPTDetail.Value > 0` at every compaction — and `stock` scores strictly above `null` on all 24. If `stock` ever ties `null`, the corpus shape is wrong and the corpus is fixed, never the assertion.
- [ ] Every `t.Skip` in `evaltest.RunEvalSuite` is removed and the suite passes against `eval.New` (Rule W-1).
- [ ] `go run ./tools/devtool test` and `-race` green on Linux, macOS and Windows in CI.
- [ ] `go run ./tools/devtool cover` shows `internal/eval` ≥ **85%** (00-ARCHITECTURE §6.4).
- [ ] `gofumpt -l` empty; `golangci-lint run` clean; the in-repo `nomagic` pass clean. Concretely, none of the pass's forbidden literals — floats `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}`, ints `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` — appears in `internal/eval` or `test/replay` outside `*_test.go`, except on the `//nomagic:allow`-annotated lines of `internal/eval/hostconst.go` (which model Claude Code's constants, not Qompack's) and on the driver's limit defaults (`defaultMaxCPU = 2 * time.Minute`, `defaultMaxWall = 15 * time.Minute` — the post-V2 hardening round split the cost bound from the liveness bound; both carry their own annotations in `test/replay/main.go`), written as durations rather than as second counts.
- [ ] The import-graph test proves `internal/eval` imports no `internal/` package outside `{core, paths, config, logging, obs}`.
- [ ] Benchmarks within budget: E-1 < 120 s, E-2 ≤ 250 ms/op, E-3 ≤ 50 ms/op, E-4 ≤ 20 ms/op, E-5 ≤ 15 ms/op, all recorded in `testdata/bench-baseline.txt`.
- [ ] `replay-gate` runs on the branch, passes, and is configured as a required check on `develop`.
- [ ] Every gate failure mode has a test: 2% boundary (both sides), sign-off trailer accept/reject, phase-0 session-count, phase-0 reproducibility, bloom FP ceiling, corpus staleness, growth inconclusive, max-wall.
- [ ] `BreakpointOPT` output always carries the `NotPluginActionable` note and a test asserts the driver prints it.
- [ ] `FuzzRedact` runs 60 s clean; `Redact` is idempotent and no fixture secret survives it.
- [ ] `docs/adr/0002-replay-methodology.md` and `docs/adr/0003-replay-overfit-recollection.md` committed; the latter specifies the re-collection schedule the gate mechanically enforces.
- [ ] Exactly 7 commits on the branch, all Conventional-Commit-shaped, none carrying an attribution trailer.

---

## Done checklist

- [ ] Branch `feat/sp02-replay-harness-belady-baseline` cut from `develop` with SP-01 merged; no work landed anywhere else.
- [ ] **Spec coverage:** every quoted item in *Design context* has an implementation and a test — §4.2's five bullets (`Divergence`), §5.6 (`BreakpointOPT` + note), §6.10 (`BeladyDetail`), §8.8 (`internal/eval` offline, no hot-path import), §10 Phase 0 (all four bullets + exit criterion), §11.1 (`FractionOfOPT`), §11.2 (all ten rows in `MetricsOf`), §11.3 (see the next item), §11.4 (both watch-fors mechanized).
- [ ] **§11.3, all four guardrails accounted for, three of them owned here:** *store growth sublinear* → `CheckSublinearGrowth` + the gate's `--growth`; *no metric regresses >2% without sign-off* → the gate's 2% rule and trailer scan; *every phase gate runs the full replay suite* → `phases.go` + `--phase` + `eval.replayOnPhaseGate`. The fourth — *hook p99 < 15 ms (L0), < 2 s (L4)* — is **budgets B-A and B-E, enforced by SP-05's `bench-gate` job**, not by `test/replay`; SP-02 names it in `docs/adr/0002` and asserts nothing about it. Claiming otherwise would be the same dishonest measurement §1.3 RC-3 indicts.
- [ ] **Gap closure asserted, not assumed:** G8.1 → `Score.FractionOfOPT` + `Divergence` exist, are computed on every corpus session, and gate CI; G8.3 → `RegisterPolicy` admits a competing steering policy and the 2% rule renders a verdict on it. A test fails if either artifact is removed.
- [ ] **Placeholder scan:** `grep -rniE "TODO|TBD|FIXME|XXX|implement appropriately|handle edge cases|add tests" internal/eval test/replay tools/devtool/task_replay.go docs/adr/0002-replay-methodology.md docs/adr/0003-replay-overfit-recollection.md` returns nothing. (The plan file itself is deliberately excluded: it *names* those markers in this very line, so including it would make the check unsatisfiable.)
- [ ] **Type consistency:** every type and function in the *Interface contract* section exists with exactly that signature; `go build ./...` and `go vet ./...` clean; no §5.18 field renamed, retyped or removed; the only §5.18 struct with appended fields is `Run` (`At`, `Demands`, `PrefixTokens`, `FirstCompactionTurn`, `Horizon`), which no package outside `internal/eval` and `test/replay` constructs or reads; no method added to another subplan's interface (Rule W-3).
- [ ] **Report envelope, not a mutated `Report`:** `budget_violation`, `noDemands`, `retrieval_actions`, `latency`, `corpusTier`, `corpusSHA256`, the breakpoint plan and the growth result live on `test/replay`'s `DriverReport`; `eval.Report` still has exactly the §5.18 five fields.
- [ ] **Config, not constants:** `w` and `r` read from `cfg.Scheduler.Cache.*` at the use site; `eval.minSessions` read from `cfg.Eval.MinSessions`; `nomagic` clean.
- [ ] **Honesty check:** every latency number in the report is tagged `"latency": "modelled"`; the breakpoint number carries `NotPluginActionable`; `retrieval_hit_rate: 0.0` is accompanied by `retrieval_actions: 0`; `forfeited_discount_tokens` is reported separately from `rewrite_tokens`.
- [ ] **W-2 fixtures** committed at `testdata/golden/eval/growth/stats-growth.json` and `testdata/golden/eval/growth/health.json` — they live under `testdata/golden/eval/` rather than `testdata/golden/contracts/` because the contract directories are their owning subplan's to regenerate (V2-MERGE-18), as `test/replay/growth.go:19-22` records — and the driver's provider seam is documented so SP-06 and SP-09 can swap in the real sources without editing `internal/eval`.
- [ ] **Corpus integrity:** 24 files present, `CORPUS.json` hashes match, regeneration is byte-identical, `regeneratedAfterPhase: 0` recorded.
- [ ] **Out-of-scope respected:** no file created or modified under `internal/{store,sketch,chunk,canon,symbols,dag,negknow,checkpoint,rehydrate,scheduler,analyzer,grammar,mcp,commands,observer,daemon,ipc,contract,redact}`; the only files touched outside `internal/eval`, `test/replay`, `testdata/`, `docs/adr/` are `internal/cli/register_eval.go` (new), `tools/devtool/task_replay.go` (new), `.github/workflows/ci.yml` (one step body), and `testdata/bench-baseline.txt`.
- [ ] **`Qompack.md` unmodified** — verify with `git diff develop -- Qompack.md` returning empty.
- [ ] **Commit count verified:** `git log --oneline develop..HEAD | wc -l` == 7 (within the 5–8 rule).
- [ ] **No co-author trailers:** `git log develop..HEAD --format=%B | grep -inE "co-authored-by|signed-off-by|generated with|🤖"` returns nothing. Do not add Co-Authored-By lines or any attribution trailers to any commit message.
- [ ] Full CI green on the branch: `verify`, `test` (3 OSes), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.

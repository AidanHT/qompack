# SP-15: Phases 5 and 6 / L2 analyzer: delta-scoring, redundancy, suffix-constrained submodular greedy, Sequitur grammar compression, and thrash warnings

> **Recommended model: Fable 5 · high effort**
>
> **Second-hardest.** Sequitur's two invariants (digram uniqueness, rule utility) are famously easy to break under recursive rule elimination, and the lazy-greedy selector must satisfy the `coverage(S) − λ·redundancy(S)` identity to 1e-12 under a property test while keeping the `(1 − 1/e)` guarantee and the structural pre-`p` refusal. `high` rather than `xhigh` because the plan already writes the algorithms out.

**Branch:** `feat/sp15-analyzer-selection-and-grammar` (cut from `develop`) | **Wave:** 4 | **Prerequisites:** the branches of `SP-01`, `SP-06`, `SP-07`, `SP-08`, `SP-10`, `SP-12` already merged into `develop`, **plus the blocking `arch/store-tooluses-by-session` pre-step** (adds `store.Store.ToolUsesBySession`; see Implementation spec §4 — this branch is cut after it lands) | **Runs in parallel with:** sibling subplans of wave 4 (SP-14 slash commands, SP-16 Phase 7 refinements) | **Design sections:** §6.3, §6.5, §7.2 L2, §8.1 item 6, §8.3 (delta-scoring, slicing, submodular), §10 Phase 5, §10 Phase 6, Closing note item 3 | **Gaps closed:** G6.3

---

## Mission

This slice is layer **L2's selection machinery**. Qompack's store (L1) records everything, the DAG (L1) knows what depends on what, and the scheduler (L3) has already decided *when* to compact and *where* to cut (`p`). What is still missing is the answer to the question §5.3 of `Qompack.md` says is the only one worth asking: **"Given that I am already paying to rewrite from `p`, what else should I drop from there?"** SP-15 answers it with three cooperating pieces: a Δ-scorer that ranks blocks by how much of the *observed* continuation they explain, a redundancy detector that finds content the rest of the context already reproduces, and a submodular lazy-greedy selector that maximizes `coverage(S) − λ·redundancy(S)` under a token knapsack — constructed with `p` so that selecting anything before `p` is **structurally impossible**, not merely discouraged.

It also closes Phase 6 by shipping `internal/grammar`: an online, linear-time Sequitur implementation over the action log that maintains the two invariants (no digram appears twice; every rule is used more than once), detects high-multiplicity nonterminals as thrash, delivers a one-line warning through `UserPromptSubmit` `additionalContext`, and folds the grammar-compressed action history into the checkpoint **at compaction time** rather than rewriting anything in place — which is exactly why §5.5 classifies Sequitur as cache-**Safe**.

**What this subplan ships, stated plainly so no one has to infer it: in wave 4 the selector ships
measured, not wired.** Its only callers are the two replay policies in `test/replay`
(`NewSuffixSubmodularPolicy` and `NewPSelectionBaselinePolicy`); no production code path constructs
a `Selector`. §6.5's "this should allocate the post-compact budget instead of 'top 5 files, 5K
each'" is therefore **partially** delivered here: the allocator exists, is property-tested against
the `(1 − 1/e)` guarantee, and is scored against Belady OPT on the 24-session corpus — but the
production budget allocation stays with SP-11's rehydrator and SP-10's `Truncate`, both of which
this subplan lists as out of scope. **The production consumer splits, and only half of it has an
owner.** `checkpoint.Truncate`'s pointer ordering — where SP-16's `OrderPointers` lands — is owned
by **SP-16**. The rehydrator's 8–12K post-compact allocation (`Qompack.md:590`) is owned by
**nobody**: no subplan in waves 3–5 claims it, and the "wave-5 rehydration slice" earlier drafts of
this file pointed at does not exist — no document in `plans/` defines it and `plans/README.md`'s
wave-5 list does not contain it. It is recorded as an unowned obligation in
`plans/TRACEABILITY.md`'s unowned-obligations section, and naming its owner is a decision for the
plan set, not for this subplan. Wiring it here would in any case mean an anchored edit inside two
other subplans' files and an owner negotiation none of the three wave-4 branches has room for.
**This costs SP-15 no exit criterion:** §10 Phase 5's criterion is *"improved fraction-of-OPT at
equal budget"*, a replay-harness number, and the two policies deliver it. What follows from that,
and is repeated in the Exit criteria: **Phase 5's number is replay evidence, not live-path
behaviour.** It is the honest form of the phase gate — "the selector would do better at equal
budget, measured on a corpus" — and it is what makes the later wiring a mechanical change with a
number already attached rather than a leap of faith.

The scheduling of this subplan into wave 4 is load-bearing and must not be "optimized" earlier. Closing note 3 of `Qompack.md` states that shipping slicing or submodular selection before p-selection "would make the system measurably more expensive while looking smarter." SP-07 already shipped slice *scores* in wave 1 (legal: scores rank content inside a checkpoint or rehydration budget, which is not a prefix edit). What was withheld until now is the scattered keep-set that drives a *drop* decision. `analyzer.NewSelector` therefore refuses to construct unless `scheduler.PSelectionAvailable()` reports true, and a CI test proves the inertness.

**What exists in the repo when you start.** `internal/core`, `paths`, `config`, `logging`, `obs`, `tokens`, `contract`, `hookio`, `cli`, `pluginmanifest`, `testutil`, and the `test/e2e` scaffolding (SP-01). `internal/eval` with `Harness`, `Policy`, `Belady`, `Synthesize` and the 24-session synthetic corpus under `testdata/sessions/synthetic/` (SP-02, wave 1). `internal/sketch` with `Signature`, `Jaccard` and `IsNearDup` (SP-03, wave 1). `internal/symbols` with `Extractor` and `References` (SP-04, wave 1). `internal/store` with objects, roots, the tool_use index, file version history, segments, GC and exact token accounting (SP-06). `internal/dag` with the nine node kinds, eight edge kinds, `BackwardSlice`/`ForwardSlice` returning relevance scores, `CrossingEdges(pos)` and `NodesAfter(pos)` (SP-07). `internal/observer` writing tool-use records, supersession marks and Sequitur symbols through the shipped interfaces (SP-08). `internal/checkpoint` with the versioned schema, the incremental `Draft`, `Truncate`, `ExtractDecisions` and `FocusInstructions` (SP-10). `internal/scheduler` with BOCD, Young–Daly, the composite trigger, p-selection, droppable-block classification and `PSelectionAvailable()` (SP-12). `internal/analyzer` and `internal/grammar` exist as SP-01 stubs whose methods return `core.ErrNotImplemented` and whose conformance suites are `t.Skip`ped.

SP-02, SP-03 and SP-04 are not in this subplan's `depends_on` list because they are wave-1 branches that merged into `develop` three waves ago; they are prerequisites of the *repository state*, not of the branch cut. SP-15 consumes them read-only and changes nothing in them.

**What exists when you finish.** `internal/analyzer` fully implemented: a `DeltaScorer` interface with the cheap retrospective proxy behind it and a documented medium/expensive upgrade path; `DetectRedundancy` covering superseded reads and MinHash near-duplicates, feeding both eviction ranking and the summary-exclusion rule; a `Selector` whose constructor takes `p` and errors on any block before it, running lazy greedy with a property-tested `(1 − 1/e)` guarantee. `internal/grammar` fully implemented: online Sequitur with both invariants property-tested, a versioned CRC-checked `grammar/actions.seq` codec, thrash detection and warning delivery. `internal/checkpoint/grammar.go` folding the compressed action history into the checkpoint narrative and metadata. `test/replay` asserting the Phase 5 and Phase 6 exit criteria against the 24-session synthetic corpus. Every `t.Skip` in `analyzertest` and `grammartest` removed.

---

## Design context (verbatim from Qompack.md)

Everything quoted below is reproduced exactly; do not paraphrase these into the code comments, cite them.

### §6.3 — Grammar compression over the action sequence

> **Closes:** loop detection, G6.2, transcript bloat
>
> Sequitur is linear-time and online: it builds a hierarchical grammar enforcing two invariants — no digram appears twice, every rule is used more than once. Feed it the tool-call sequence as a symbol string.
>
> `read → edit → test → fail` repeated eleven times becomes one production plus a count. That is 90%+ reduction on the most repetitive part of the transcript, and — more valuable — **the grammar rule itself is the insight**: a nonterminal with high multiplicity is a loop the agent is stuck in. Thrash detection as a byproduct of compression. Re-Pair gives better ratios offline if streaming is not required.

### §6.5 — Submodular maximization for budget allocation

> **Closes:** G3.3, principled allocation with a guarantee
>
> Once candidate units and a coverage notion exist, "pick the best subset under a token budget" is submodular maximization under a knapsack constraint. Coverage plus redundancy penalty are both monotone submodular, so greedy achieves `(1 − 1/e) ≈ 0.63` of optimal, and lazy greedy exploits diminishing returns to skip most re-evaluations.
>
> This should allocate the post-compact budget instead of "top 5 files, 5K each." Slice membership and Δ-scores become the coverage weights.

### §7.2 — Layer diagram, L2 row

> ```
> ├─────────────────────────────────────────────────────────────────┤
> │ L2  ANALYZER              slicing · Δ-scoring · submodular ·     │
> │                           Sequitur · redundancy detection        │
> ├─────────────────────────────────────────────────────────────────┤
> ```

### §8.1 item 6 — Observer responsibility feeding this layer

> 6. **Sequitur.** Append the tool symbol to the action grammar; check for high-multiplicity nonterminals and emit a thrash warning.

and, from the same section's performance budget:

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

and, from §8.1 item 3, the summary-exclusion rule this subplan enforces:

> 3. **Redundancy detection.** If the chunk set is a superset or near-duplicate of a prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the first candidates for eviction and should never appear in a summary.

### §8.3 — Δ-scoring, Slicing, Submodular selection

> **Δ-scoring.** Retrospective proxy: for each candidate block, measure how much of the *observed* subsequent content is predictable without it. Implementation options in ascending cost:
>
> - Cheap: token-overlap and symbol-reference counting between block and continuation
> - Medium: a small local model computing conditional perplexity, Selective Context style
> - Expensive: leave-one-out with the session model (do not do this online)
>
> Start cheap. The replay harness will say whether the expensive version is worth it.
>
> **Slicing.** Backward slice from the criterion set: current todo items, files under edit, the active plan, the most recent user intent. Thin-slicing variant by default. Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.
>
> **Submodular selection.** Objective: `coverage(S) − λ · redundancy(S)` under a token knapsack. Lazy greedy. Coverage weights come from slice membership and Δ-scores. **Constrained to the suffix after `p`** (§5.3) — this is enforced in the selector's constructor, not left to the caller.

### §4.3 — the quantity the Δ proxy estimates

> ```
> Δ(c) = H(future | context \ c) − H(future | context)
> ```
>
> Blocks with `Δ ≈ 0` are dead weight regardless of how important they look to a human reader. This replaces "keep the last 5 tool results" — a recency heuristic — with an information-contribution ranking.
>
> Two computable proxies, since the future is unavailable at compaction time:
>
> - **Retrospective Δ.** At compaction time you know what happened between block *c* and now. Score *c* against the already-observed continuation. Blocks that explained the recent past tend to constrain the near future.
> - **Reconstruction / redundancy test.** Can the content be reproduced from the rest of the context? If yes, it compresses to a pointer. A `FileRead` superseded by a later diff is pure redundancy.

### §5.2 — why the suffix constraint exists

> **Most content-selection algorithms produce arbitrary subsets, and an arbitrary subset of a prefix-cached sequence is a worst-case edit.**
>
> Slicing, submodular greedy, and Δ-scoring all pick a scattered keep-set. If the earliest dropped element sits at position 12,000 of 167,000, you have selected beautifully and paid to rewrite 155,000 tokens.
>
> The governing quantity is the **earliest edit position**, not how much was dropped:
>
> ```
> cost = w · (n − p_min)     where p_min = position of the earliest dropped block
> ```

### §5.3 — the corrected objective and the two-stage flip

> ```
> minimize   Σ tokens_kept · r          (steady-state read cost)
>          + w · (n − p_min)            (one-time rewrite)
>          + c · n                       (the compaction request's own input)
>          + λ · D(keep-set)            (task damage, from §4.2)
> ```
>
> The right question is never "should I drop this block." It is:
>
> > **Given that I am already paying to rewrite from `p`, what else should I drop from there?**
>
> That flips the algorithm into two stages:
>
> 1. **Choose `p`** — a scalar optimization over a small candidate set.
> 2. **Run expensive selection only on the suffix after `p`**, where it costs nothing extra.
>
> Slicing, submodular greedy, and Δ-scoring all become cache-safe when confined to the region being rewritten anyway.

### §5.5 — cache-compatibility audit rows this subplan is bound by

> | Method | Cache status | Notes |
> |---|---|---|
> | Sequitur over action log | **Safe** | Folded into the summary at compaction time, not rewritten in place. |
> | Dynamic slicing | **Conditional** | Legal only inside the suffix after `p`. |
> | Submodular greedy | **Conditional** | Same constraint. |
> | Δ-scoring | **Conditional** | Same constraint. |

### §8.7 — the ephemeral-at-birth ranking this selector must honour

> - Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).

### §10 Phase 5 — Selection

> - Dependence DAG construction
> - Thin slicing
> - Δ-scoring (start with the cheap proxy)
> - Submodular greedy, **constrained to the suffix after p**
>
> **Exit criterion:** improved fraction-of-OPT at equal budget.

### §10 Phase 6 — Grammar and loop detection

> - Sequitur over the action log
> - High-multiplicity nonterminal → thrash warning
> - Grammar-compressed action history in the checkpoint
>
> **Exit criterion:** thrash detected before the user notices it, on replay.

### Closing note item 3

> 3. **The cache correction.** Do not ship slicing or submodular selection before p-selection. Selection quality is real, but an arbitrary subset of a cached prefix is a worst-case edit, and shipping it first would make the system measurably more expensive while looking smarter.

### §11.1, §11.3 — the metric and the guardrails

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### Appendix A — the guarantee that is property-tested

> **Submodular greedy guarantee**
> ```
> f(S_greedy) ≥ (1 − 1/e)·f(S_opt) ≈ 0.63
> ```

### Appendix C — the only configuration keys this subplan reads

> ```jsonc
>   "selection": {
>     "slicing": "thin",
>     "deltaScoring": "cheap",
>     "submodular": { "lambda": 0.4, "lazyGreedy": true }
>   },
> ```
>
> and, for the near-duplicate threshold used by redundancy detection:
>
> ```jsonc
>     "canonicalize": {
>       "enabled": true,
>       "strip": ["timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"],
>       "minhash": { "enabled": true, "permutations": 128, "nearDupThreshold": 0.9 }
>     }
> ```

### §12 — the degradation rows that bind this subplan

> | Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |

### §13 of 00-ARCHITECTURE — invariant 4, verbatim

> 4. **Nothing scattered before `p` (§5.3).** `analyzer.NewSelector` refuses blocks with `Pos < p`. Do not add a bypass.

---

## Out of scope

Each item names the sibling subplan that owns it. Do not implement any of these.

| Out of scope | Owner |
|---|---|
| `dag.BackwardSlice`, `ForwardSlice`, `CrossingEdges`, `NodesAfter`, edge/node model, `dag/deps.jsonl` persistence, thin-slicing semantics | **SP-07** |
| `scheduler.PSelectionAvailable()` itself, BOCD, Young–Daly, the composite trigger, p-selection scoring, `Candidate.ReclaimableTokens`, droppable-block *classification* (ephemeral → superseded → ordinary compactable), the sliding-TTL idle model, `BackgroundTask` scheduling | **SP-12** |
| The negative-knowledge ledger, canonical descriptors, `depends_on` staleness, `tried.bloom` rebuild, the three-way `already_tried` answer | **SP-09** |
| The checkpoint JSON schema, `version` bumps, tiering, `Truncate`, `ExtractDecisions`, `FocusInstructions`, `ValidatePointers`, `Begin`/`Advance`/`Finalize` mechanics, `pins` | **SP-10** |
| Demand-driven promotion of re-expanded hashes into the pointer tier, per-segment Bloom filters, cross-session warm start, ski-rental write policy, progressive-truncation tuning, `checkpoint/promote.go` | **SP-16** |
| `internal/observer` in its entirety — PostToolUse, UserPromptSubmit capture, tombstones, the *marking* of `SUPERSEDED`, subagent capture, task-boundary signals. **No subplan other than SP-08 writes code in `internal/observer`** (00-ARCHITECTURE §5.21) | **SP-08** |
| The MCP server, the eight tools, ephemeral tagging of retrieval results, the `Promoter`, minimal-span resolution | **SP-13** |
| Slash commands and the `/qompack:status` surface | **SP-14** |
| The replay harness itself, `eval.Harness`, `Belady`, `Synthesize`, the 24-session synthetic corpus, divergence metrics, the replay-gate driver's own baseline machinery | **SP-02** |
| `sketch.MinHash`, `Signature.Jaccard`, Bloom/CMS/HLL/Misra-Gries implementations and serialization | **SP-03** |
| `symbols.Extractor` implementation, FastCDC, canonicalizers | **SP-04** |
| `store` objects/roots/index/GC, redaction at ingest, `tokens` exact accounting | **SP-06** |
| Daemon lifecycle, IPC transport, op-routing table, `IdleController`, spool/WAL, hot-path budgets | **SP-05** (SP-15 adds exactly one new file, `internal/daemon/grammar_addendum.go`, plus one contiguous four-line guarded call in `daemon.New` immediately before the `for _, bind := range o.binds` loop; it appends to `*Options`' bind list, never registers a route, never touches a running daemon, and never reads or mutates `d.routes`. See Implementation spec §7) |
| The production consumer of the selector — the rehydrator's 8–12K allocation, and `checkpoint.Truncate`'s pointer ordering. SP-15 ships the selector measured-but-unwired; its callers are the two `test/replay` policies | **SP-16** (pointer ordering / `OrderPointers`). The 8–12K allocation is **unowned** — see the Mission and `plans/TRACEABILITY.md`'s unowned-obligations section |
| Packaging, cross-platform matrix, security audit, release pipeline | **SP-17** |
| README, user guide, troubleshooting, config reference, cannot-do list, UAT | **SP-18** |

---

## Interface contract

### Consumes (exact signatures already on `develop`; call them, do not change them)

From `internal/core` (00-ARCHITECTURE §4):

```go
type Hash [32]byte
func (h Hash) String() string
func (h Hash) Short() string
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type Tokens int
type UnixMilli int64
type ChunkRef struct{ Hash Hash; Len int }
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
var ErrNotFound = errors.New("qompack: not found")
var ErrBudget   = errors.New("qompack: budget exceeded")
```

From `internal/config` (§5.1, §11.1):

```go
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
// Config.Selection.Submodular.Lambda float64        (Appendix C default 0.4)
// Config.Selection.Submodular.LazyGreedy bool       (Appendix C default true)
// Config.Selection.Submodular.Enabled bool          (json:"-"; derived by Load from
//                                                    Runtime.Selection.SubmodularEnabled — see
//                                                    the ship-order decision below)
// Config.Selection.DeltaScoring string              (Appendix C default "cheap")
// Config.Selection.Slicing string                   (Appendix C default "thin")
// Config.Store.Canonicalize.MinHash.NearDupThreshold float64   (Appendix C default 0.9)
```

**Decision — the ship-order key exists on `develop`, and SP-15 flips its default to `true`.**
There is no `selection.submodular.enabled` key in Appendix C, and none is invented: Appendix C's
`selection.submodular` object has exactly two members (`lambda`, `lazyGreedy`) and §11.1 pins
`config.Defaults()` to that document with a golden test. What SP-01 did ship is a **separate,
derived** field outside Appendix C, and it is live on `develop` today:

```go
// internal/config/runtime.go
type RSelectionCfg struct {
    SubmodularEnabled bool `json:"submodularEnabled" doc:"ship-order gate: enable submodular selection; refused without p-selection (closing-note-3)" sec:"Closing note"`
}
// internal/config/config.go — SubmodularCfg.Enabled is json:"-", never read from a file
// internal/config/load.go   — deriveSubmodularEnabled copies Runtime.Selection.SubmodularEnabled
//                             into Selection.Submodular.Enabled after every fromMap
// internal/config/defaults.go — SubmodularEnabled: false, and the derived Enabled: false
```

`runtime.selection.submodularEnabled` therefore defaults to **false** on `develop`, and ANDing that
flag into the gate while it stays false would make this entire subplan inert under
`config.Defaults()` and under every real project config — the Phase 5 exit number would be
demonstrable only in tests that hand-build a `Config`, and the shipped plugin would improve
nothing. That is not a shippable outcome, so the gate is not left where SP-01 parked it.

**SP-15 flips the default, as an explicit declared edit, because the condition SP-01 wrote it for
is now satisfied.** The key's own doc string names the condition ("refused without p-selection"),
and SP-12 merged in wave 3, so `scheduler.PSelectionAvailable()` reports true on the branch SP-15
is cut from. Four files change, all of them listed in the Done checklist and made in commit 5:

| File | Edit |
|---|---|
| `internal/config/defaults.go` | `Runtime.Selection.SubmodularEnabled: false → true`, and the derived `Selection.Submodular.Enabled: false → true` so `Defaults()` agrees with what `Load` derives |
| `internal/config/defaults_test.go` | `require.Equal(t, config.RSelectionCfg{SubmodularEnabled: true}, rt.Selection)`; `TestDefaults_SubmodularEnabledDefaultsFalseAndHidden` becomes `TestDefaults_SubmodularEnabledDefaultsTrueAndHidden` — `require.True(t, cfg.Selection.Submodular.Enabled)`, with the `json:"-"` assertion unchanged |
| `test/guards/buildorder_test.go` | `TestGuard_SubmodularDefaultsOff` becomes `TestGuard_SubmodularEnabledOnlyAfterPSelection`: it asserts `d.Runtime.Selection.SubmodularEnabled == true` **and** `d.Selection.Submodular.Enabled == d.Runtime.Selection.SubmodularEnabled`, i.e. that the derived field still follows the runtime key. The guard is not deleted — it is re-pointed at the invariant that survives, which is the derivation, not the value |
| `docs/config-reference.md` | regenerated: `go run ./tools/devtool gen-config-docs` (the `docs` CI job runs `gen-config-docs --check`, so the row's default column must be regenerated, never hand-edited) |

**With the default flipped, the AND-gate only lets an operator opt out — and its two halves live in
two different places, on purpose.** The **ship-order** half is structural and stays in the
constructor exactly as SP-01 shipped it: `NewSelectorWithStore` consults `pAvailable()` and refuses
with `ErrPSelectionUnavailable`, and no configuration can override that. The **operator opt-out**
half lives at the call site, because that is where a loaded `config.Config` exists: every consumer
reads `cfg.Selection.Submodular.Enabled` before constructing and, when it is false, keeps the
baseline keep-set unchanged — `test/replay/policy_analyzer.go`'s `KeepSet` step 6 returns `base`
without building a selector, which is byte-identical to what the `ErrPSelectionUnavailable` branch
in step 7 already does. Two alternatives were considered and rejected: adding a `config.Config` parameter to the
§5.12-pinned constructor signature is an amendment under 00-ARCHITECTURE §0, and holding the flag
in a package-level variable the way `pAvailable` is held would put a mutable global on a path two
goroutines can reach. Neither Appendix C nor any §5 interface changes, so no amendment is needed
and none is made.

From `internal/logging` and `internal/obs` (§5.2):

```go
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    Loud(msg string, kv ...any)
}
func Nop() Logger
type Histogram interface { Observe(d time.Duration); Snapshot() HistSnapshot; Reset() }
type Registry interface { Hist(name string) Histogram; Counter(name string) Counter; /* … */ }
```

From `internal/store` (§5.8):

```go
type Root struct{ Hash core.Hash; Chunks []ChunkRef; CanonBytes, RawBytes int64; Tokens core.Tokens }
type Supersession uint8 // StatusOK, StatusSuperseded
type ToolUseRecord struct {
    ID core.ToolUseID; Session core.SessionID; Turn core.TurnIndex; TS core.UnixMilli
    Tool string; ArgsDigest core.Hash; ArgsPreview string; Root core.Hash; Path string
    Bytes int64; Tokens core.Tokens; Signature sketch.Signature
    Status Supersession; SupersededBy core.ToolUseID; Ephemeral bool; Subagent string
}
type Query struct{ Text, Path, Symbol, Tool string; Since time.Time; K int }
type Hit struct {
    Root core.Hash; ToolUseID core.ToolUseID; Path, Tool string
    TS core.UnixMilli; Score float64; Summary string; Span [2]int64
}
type Store interface {
    GetRoot(ctx context.Context, root core.Hash) (Root, error)
    Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
    ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
    ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
    ToolUsesBySession(ctx context.Context, sess core.SessionID, limit int) ([]ToolUseRecord, error)
    // ^ added by the blocking arch/store-tooluses-by-session pre-step; see Implementation spec §4.
    //   Search is deliberately NOT in SP-15's call set: it clamps K to maxK = 100 and is
    //   project-wide (store.Query has no Session field).
    // … the rest of the interface is not called by SP-15
}
```

From `internal/dag` (§5.9):

```go
type NodeKind uint8 // KindToolUse, KindToolResult, KindAssistant, KindUserPrompt,
                    // KindFile, KindSymbol, KindDecision, KindElimination, KindSegment
type NodeID string  // "<kind>:<stable-key>"
type Node struct {
    ID NodeID; Kind NodeKind; Turn core.TurnIndex; TS core.UnixMilli
    Pos int; Ref string; Root core.Hash; Tokens core.Tokens; Ephemeral bool
}
type EdgeKind uint8 // EdgeSequence, EdgeProduces, EdgeConsumes, EdgeSharedFile,
                    // EdgeSharedSymbol, EdgeSupersedes, EdgeExplains, EdgeControlOnly
type Edge struct{ From, To NodeID; Kind EdgeKind; Weight float32; Turn core.TurnIndex }
type Slice struct{ Scores map[NodeID]float32; Order []NodeID; Truncated bool; Visited int }
type SliceOptions struct {
    Thin bool; MaxDepth, MaxNodes int; Decay float32; Deadline time.Duration
}
type Graph interface {
    Node(id NodeID) (Node, bool)
    Out(id NodeID) []Edge
    In(id NodeID) []Edge
    NodesAfter(pos int) []Node
    BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error) // called only from test/replay
    // … the rest is not called by SP-15
}
```

`BackwardSlice` and `SliceOptions` are consumed **only** by `test/replay/policy_analyzer.go`
(Implementation spec §9 step 4). `internal/analyzer` itself never touches `dag.Graph`: the selector
receives an already-computed `dag.Slice` as a constructor argument, which is what keeps the package
pure and unit-testable without a graph fixture.

From `internal/sketch` (§5.7):

```go
type Signature struct{ Perms uint16; Mins []uint64 }
func (s Signature) Jaccard(o Signature) float64
func (s Signature) IsNearDup(o Signature, threshold float64) bool
```

From `internal/scheduler` (§5.13) — the ship-order guard:

```go
func PSelectionAvailable() bool
```

From `internal/checkpoint` (§5.14) — same package, SP-15 adds a file to it:

```go
type Checkpoint struct{ /* … */ Narrative string `json:"narrative"`; SketchRefs map[string]string `json:"sketch_refs"` /* … */ }
type SourceSet struct{ /* … */ Grammar grammar.Sequitur /* … */ }
type Draft struct{ /* opaque */ }
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)
```

From `internal/symbols` (§5.22b) — consumed **structurally**, never imported (see Implementation spec §2):

```go
References(b []byte, names []string) map[string]int
```

From `internal/hookio` and `internal/ipc` (§5.3, §5.4) — used only by the one new daemon file:

```go
type Output struct{ /* … */ HookSpecificOutput *HSO `json:"hookSpecificOutput,omitempty"` /* … */ }
type HSO struct{ HookEventName string; AdditionalContext string; CustomInstructions string }
type Handler func(ctx context.Context, req Request) Response
type Response struct{ OK bool; Mode contract.Mode; Hot HotPathMode; Output *hookio.Output; Err string; Data json.RawMessage }
type Op string
```

From `internal/eval` (§5.18) — used only from `test/replay`:

```go
type Turn struct {
    Index core.TurnIndex; Role string; TS core.UnixMilli
    Text string; ToolCalls []ToolCall; Tokens core.Tokens
}
type ToolCall struct{ ID core.ToolUseID; Name string; Args, Result json.RawMessage; Paths []string }
type Session struct {
    ID string; Turns []Turn; CompactionAt []core.TurnIndex
    Meta map[string]string; Synthetic bool
}
type Policy interface {
    Name() string
    KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}
type KeepSet struct{ IDs []string; Tokens core.Tokens; P int }
type Divergence struct {
    FirstDivergenceTurn int; FileSetJaccard float64; ToolEditDistance int
    SameDecision bool; DecisionPreservation float64; RedundantReads, ReAttempts int
}
type Score struct{ FractionOfOPT float64; Divergence Divergence; /* … */ }
type Harness interface{ Load(dir string) ([]Session, error); /* … */ }
```

`Session.Meta` is a free-form `map[string]string`. SP-02's synthetic generator stamps the spec
shape into `Meta["shape"]`, using the eight names 00-ARCHITECTURE §6.3 enumerates: `read-heavy`,
`test-output-heavy`, `refactor-across-files`, `long-idle-gap`, `dependency-change-mid-session`,
`subagent-heavy`, `thrash-loop`, `multi-compaction`. SP-15's Phase 5 and Phase 6 tests read that
key and **fail loudly** (`require.NotEmpty`, message naming SP-02 and this paragraph) if any of the
24 sessions lacks it. Silently skipping a phase-exit assertion because a fixture key is missing is
the exact failure mode §1.3 RC-3 indicts, so it is never permitted.

### Produces (later subplans and CI rely on exactly these)

`internal/analyzer` — the §5.12 contract, implemented, plus the additive members SP-15 owns:

```go
type Block struct {
    ID         dag.NodeID
    Pos        int
    Tokens     core.Tokens
    Kind       dag.NodeKind
    Root       core.Hash
    Ephemeral  bool
    Superseded bool
}

type DeltaMode string
const (ModeCheap DeltaMode = "cheap"; ModeMedium DeltaMode = "medium"; ModeExpensive DeltaMode = "expensive")

type Continuation struct{ Text []byte; Symbols []string; Paths []string; FromTurn core.TurnIndex }

type DeltaScorer interface {
    Mode() DeltaMode
    Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error)
}

// SymbolRefs is the one-method slice of symbols.Extractor the cheap proxy needs. It is declared
// here, not imported, because §3.2 does not list `symbols` in `analyzer`'s import set; Go
// interfaces are structural, so symbols.Extractor satisfies it with no import edge.
type SymbolRefs interface{ References(b []byte, names []string) map[string]int }

func NewCheapScorer(s store.Store) DeltaScorer
func NewCheapScorerWithSymbols(s store.Store, sym SymbolRefs, log logging.Logger) DeltaScorer

type RedundancyReport struct {
    Superseded []core.ToolUseID
    NearDups   map[core.ToolUseID][]core.ToolUseID
}
func DetectRedundancy(ctx context.Context, s store.Store, sess core.SessionID) (RedundancyReport, error)
func DetectRedundancyWithConfig(ctx context.Context, s store.Store, sess core.SessionID, cfg config.Config) (RedundancyReport, error)
func (r RedundancyReport) ExcludeFromSummary() map[core.ToolUseID]bool
func (r RedundancyReport) ApplyTo(blocks []Block) []Block
func (r RedundancyReport) SortedNearDupKeys() []core.ToolUseID
func ToolUseIDOf(id dag.NodeID) (core.ToolUseID, bool)

type Selector interface {
    P() int
    Select(ctx context.Context, budget core.Tokens) (Selection, error)
}
type Selection struct {
    Keep    []dag.NodeID
    Tokens  core.Tokens
    Value   float64
    Dropped []dag.NodeID
    Iters   int
}
func NewSelector(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
                 lambda float64, lazy bool) (Selector, error)
func NewSelectorWithStore(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
                          lambda float64, lazy bool, s store.Store) (Selector, error)

// The two structural sentinels WRAP the core errors the shipped constructor already returns, so
// every live errors.Is check keeps passing. test/guards/buildorder_test.go requires
// core.ErrBudget for a pre-p block (ungated by PSelectionAvailable) and
// internal/analyzer/selector_test.go additionally requires the formatted message to contain
// "invariant 4"; the ship-order refusal is pinned to core.ErrNotImplemented by
// TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet and TestGuard_SelectorRefusesWithoutPSelection.
var (
    ErrPSelectionUnavailable = fmt.Errorf("%w: submodular selection requires p-selection (closing note 3)",
        core.ErrNotImplemented)
    ErrBlockBeforeP = fmt.Errorf("%w: block position precedes p (suffix constraint, §5.3, §13 invariant 4)",
        core.ErrBudget)
    ErrLambdaNegative = errors.New("qompack: submodular lambda must be >= 0")
)

// SetPSelectionProbe replaces the ship-order probe and returns a restore func. Test-only:
// a CI test asserts no non-test file under internal/ or cmd/ references it.
func SetPSelectionProbe(fn func() bool) (restore func())
```

`internal/grammar` — the §5.11 contract, implemented, plus additive members:

```go
type Symbol string
type RuleID int
type Rule struct{ ID RuleID; Body []Symbol; Uses int; Expansion []Symbol; Span int }

type Sequitur interface {
    Append(s Symbol)
    Rules() []Rule
    Thrash(minUses int) []Rule
    Compressed() []Symbol
    Reset()
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func New() Sequitur
func NewWithClock(c core.Clock) Sequitur   // New() == NewWithClock(core.SystemClock())

// TurnAware is satisfied by the value New() returns. Callers that know the turn index
// type-assert to it; Append(s) is exactly AppendAt(s, lastTurn).
type TurnAware interface{ AppendAt(s Symbol, turn core.TurnIndex) }

type Warning struct{ Rule Rule; Repeats int; Message string; Turns []core.TurnIndex }
// FormatWarning is SHIPPED by SP-01 and frozen (internal/grammar/formatwarning.go). It is listed
// here because SP-15 calls it; SP-15 does not modify it, its wording, or its test. Warning.Message
// is the "short, human-readable suggestion" it interpolates — never the rendered line (§6.5).
func FormatWarning(w Warning) string

type WarnOptions struct{ MinUses, MinSpan, MaxWarnings int }
func DefaultWarnOptions() WarnOptions
func WarningsFor(g Sequitur, o WarnOptions) []Warning
func PromptAddendum(g Sequitur, o WarnOptions) string

const (
    DefaultThrashMinUses = 3
    DefaultThrashMinSpan = 2
    FormatVersion        = 1
)
func Save(path string, g Sequitur) error
func Load(path string, g Sequitur) error
```

`internal/checkpoint` — additive, no schema change, no `version` bump:

```go
type ActionRule struct{ ID string `json:"id"`; Body []string `json:"body"`; Uses int `json:"uses"`; Span int `json:"span"` }
type ThrashNote struct{ Rule string `json:"rule"`; Repeats int `json:"repeats"`; Message string `json:"message"` }
type ActionHistory struct {
    Rules      []ActionRule `json:"rules"`
    Sequence   []string     `json:"sequence"`
    Symbols    int          `json:"symbols"`
    Compressed int          `json:"compressed"`
    Thrash     []ThrashNote `json:"thrash,omitempty"`
}
func BuildActionHistory(g grammar.Sequitur, o grammar.WarnOptions) ActionHistory
func RenderActionHistory(h ActionHistory) string
func FoldActionHistoryInto(c *Checkpoint, g grammar.Sequitur, o grammar.WarnOptions)
```

`internal/daemon` — additive, one new file:

```go
func WrapObservePromptWithThrashWarning(
    inner func(ctx context.Context, e hookio.Event) (hookio.Output, error),
    g grammar.Sequitur, o grammar.WarnOptions, log logging.Logger,
) func(ctx context.Context, e hookio.Event) (hookio.Output, error)
// AttachThrashWarning takes *Options, not Daemon, and uses Options.Bind, not Options.Handle: the
// addendum decorates the Services.ObservePrompt seam so it inherits handleObservePrompt's
// mode.MayAct() gate and its 250 ms promptReplyDeadline race. A route-level override would sit
// outside both (see §7).
func AttachThrashWarning(o *Options, g grammar.Sequitur, wo grammar.WarnOptions, log logging.Logger) error
```

`test/replay` — additive, own files:

```go
func NewSuffixSubmodularPolicy(deps PolicyDeps) eval.Policy   // Name() == "analyzer-suffix-submodular"
func NewPSelectionBaselinePolicy(deps PolicyDeps) eval.Policy // Name() == "p-selection-baseline"
                                                              // (omit if SP-12 shipped one)
```

---

## Implementation spec

### 1. `internal/analyzer/doc.go` (new)

Package documentation. Responsibility: state the layer, the three cache-safety facts an implementer must not violate, and the import set.

```go
// Package analyzer is layer L2's selection machinery (Qompack.md §7.2, §8.3).
//
// It answers the only question §5.3 says is worth asking: given that a rewrite from token
// position p is already being paid for, what else should be dropped from there?
//
// Three hard rules, all of them normative:
//
//  1. Nothing scattered before p. NewSelector refuses any Block whose Pos < p
//     (00-ARCHITECTURE §13 invariant 4). There is no bypass and none may be added.
//  2. Submodular selection is inert unless scheduler.PSelectionAvailable() reports true
//     (Qompack.md closing note 3). Slice scores may be shipped and ranked at any time;
//     a scattered keep-set driving a drop decision may not.
//  3. Retrieval results are ephemeral at birth and are the FIRST eviction candidates,
//     ahead of ordinary tool results (§8.7). They enter the objective with a full
//     redundancy penalty.
//
// Import set (00-ARCHITECTURE §3.2): store, dag, sketch, scheduler, plus the foundation
// packages core, paths, config, logging, obs. `symbols` is deliberately NOT imported; the
// one method the cheap Δ proxy needs is declared locally as SymbolRefs.
package analyzer
```

### 2. `internal/analyzer/block.go` (new)

Responsibility: the shared types, the sentinel errors, the ship-order probe indirection, and `ToolUseIDOf`.

```go
package analyzer

// Block is a candidate unit of selection. Pos is the token position in the prefix, which is
// what makes the suffix constraint of §5.3 checkable in the constructor.
type Block struct {
    ID         dag.NodeID
    Pos        int
    Tokens     core.Tokens
    Kind       dag.NodeKind
    Root       core.Hash
    Ephemeral  bool
    Superseded bool
}

// ErrPSelectionUnavailable and ErrBlockBeforeP are NAMES for the two refusals SP-01's constructor
// already returns; they are not new error values. Each wraps the core sentinel the shipped code
// (and the live tests that pin it) returns today, so `errors.Is(err, core.ErrBudget)` and
// `core.IsNotImplemented(err)` keep holding while callers gain a specific symbol to branch on.
// The messages are the shipped messages: ErrBlockBeforeP keeps the phrase "invariant 4", which
// internal/analyzer/selector_test.go asserts with require.Contains.
var (
    ErrPSelectionUnavailable = fmt.Errorf("%w: submodular selection requires p-selection (closing note 3)",
        core.ErrNotImplemented)
    ErrBlockBeforeP = fmt.Errorf("%w: block position precedes p (suffix constraint, §5.3, §13 invariant 4)",
        core.ErrBudget)
    ErrLambdaNegative = errors.New("qompack: submodular lambda must be >= 0")
)

// pAvailable is an indirection over scheduler.PSelectionAvailable so the closing-note-3
// inertness guard can be exercised without removing internal/scheduler from the build.
var pAvailable = scheduler.PSelectionAvailable

// SetPSelectionProbe replaces the probe and returns a restore func. Test-only; a CI test
// asserts that no non-test file under internal/ or cmd/ references this symbol.
func SetPSelectionProbe(fn func() bool) (restore func()) {
    prev := pAvailable
    pAvailable = fn
    return func() { pAvailable = prev }
}

// ToolUseIDOf extracts the tool_use_id from a dag.NodeID of the form "<kind>:<stable-key>"
// (00-ARCHITECTURE §5.9). It returns ok == false when the id carries no ':' separator or the
// key part is empty. The kind prefix spelling belongs to SP-07 and is deliberately not matched.
func ToolUseIDOf(id dag.NodeID) (core.ToolUseID, bool) {
    s := string(id)
    i := strings.IndexByte(s, ':')
    if i <= 0 || i+1 >= len(s) {
        return "", false
    }
    return core.ToolUseID(s[i+1:]), true
}
```

**Error handling.** Every error returned from this package wraps a sentinel with `fmt.Errorf("%w: …", sentinel)` so `errors.Is` works at the call site. No function in this package panics; a nil `store.Store` is tolerated by the scorer and the selector (both degrade to the store-free path described below).

### 3. `internal/analyzer/delta.go` (new)

Responsibility: the `DeltaScorer` interface and the **cheap retrospective proxy** of §8.3, plus the documented upgrade path.

**Constructors.**

```go
func NewCheapScorer(s store.Store) DeltaScorer {
    return NewCheapScorerWithSymbols(s, nil, logging.Nop())
}

func NewCheapScorerWithSymbols(s store.Store, sym SymbolRefs, log logging.Logger) DeltaScorer {
    if log == nil { log = logging.Nop() }
    return &cheapScorer{store: s, sym: sym, log: log}
}
```

`NewCheapScorer` keeps the §5.12 signature byte-for-byte. The daemon composition root calls
`NewCheapScorerWithSymbols(st, symbols.New(), log)`; that is the only place `symbols` is imported.

**Tuning constants** (named, not literals; none of them are in the `nomagic` forbidden set):

```go
const (
    maxBlockBytes        = 1 << 18 // 256 KiB read cap per block
    maxContinuationBytes = 1 << 20 // 1 MiB read cap for the continuation
    minTokenLen          = 3
    maxTokenLen          = 64
    weightOverlapOnly    = 1.0  // symbol refs unavailable
    weightOverlapPaired  = 0.5  // symbol refs available
    weightSymbolPaired   = 0.5
)
```

**Tokenization** (`func tokenize(b []byte) map[string]int`). A token is a maximal run of
`[A-Za-z0-9_]`. ASCII-lowercase it. Reject it when: `len < minTokenLen`; `len > maxTokenLen`;
every byte is a digit; or it is in `stopwords`. `stopwords` is a package-level
`map[string]struct{}` containing exactly these **47** entries, in this order in the source
(a test asserts `len(stopwords) == 47`, so the list and the count can never drift apart):

```
the and for that this with from have has was were are not but you your its his her they
them then than into out all any can will would should could been being which when where
what who how why use using used new get set
```

**Scoring algorithm.**

```
Score(ctx, blocks, cont):
  contTok  := tokenize(cont.Text[:min(len, maxContinuationBytes)])
  contSyms := dedup(cont.Symbols)            // stable order preserved
  denomTok := len(contTok)                   // distinct continuation tokens
  out := make(map[dag.NodeID]float64, len(blocks))

  for each b in blocks (in the given order):
      if ctx.Err() != nil { return out, ctx.Err() }
      body := readBlock(b)                    // see below; nil on any failure
      if body == nil { out[b.ID] = 0; continue }

      // token-overlap term: how much of the continuation's vocabulary this block explains
      blockTok := tokenize(body)
      hit := 0
      for t := range contTok { if _, ok := blockTok[t]; ok { hit++ } }
      overlap := 0.0
      if denomTok > 0 { overlap = float64(hit) / float64(denomTok) }

      // symbol-reference term
      symScore, wOv, wSym := 0.0, weightOverlapOnly, 0.0
      if sc.sym != nil && len(contSyms) > 0 {
          refs := sc.sym.References(body, contSyms)
          named := 0
          for _, n := range contSyms { if refs[n] > 0 { named++ } }
          symScore = float64(named) / float64(len(contSyms))
          wOv, wSym = weightOverlapPaired, weightSymbolPaired
      }

      out[b.ID] = clamp01(wOv*overlap + wSym*symScore)
  return out, nil
```

`readBlock(b)`: if `sc.store == nil` or `b.Root` is the zero `core.Hash`, return nil. Otherwise
`rc, err := sc.store.Open(ctx, b.Root)`; on `errors.Is(err, core.ErrNotFound)` log at Debug and
return nil; on any other error log at Debug and return nil (a missing object must never fail a
compaction — §12 "everything else fails toward do nothing"). Read with
`io.ReadAll(io.LimitReader(rc, maxBlockBytes))`, always `defer rc.Close()`; a read error after
partial data returns the partial bytes, not nil. **Use `io.ReadAll`, not `io.ReadFull`** —
`ReadFull` over a `LimitReader` returns `io.ErrUnexpectedEOF` for every object smaller than the
cap, which is the common case and would zero every score in the corpus.

`clamp01(x)` returns `0` for `x < 0`, `1` for `x > 1`, `x` otherwise. `Mode()` returns `ModeCheap`.

**Determinism.** The result is a map, but every consumer iterates `blocks` (a slice), never the
map, so ordering is fixed. A test asserts identical output across 100 runs on the same input.

**Documented upgrade path** (a comment block in this file, required by the assignment):

```go
// Upgrade path (§8.3 "Implementation options in ascending cost").
//
//   cheap     — implemented here: token overlap + symbol-reference counting against the
//               OBSERVED continuation. No model, no network, ~O(total block bytes).
//   medium    — a small local model computing conditional perplexity, Selective Context style.
//               Adding it requires exactly one new constructor returning DeltaScorer, e.g.
//                   func NewLocalPerplexityScorer(m LocalLM, s store.Store) DeltaScorer
//               and nothing else: every consumer in this repository takes the interface, and
//               config.Selection.DeltaScoring ("cheap"|"medium"|"expensive") already selects
//               between them. No call site changes.
//   expensive — leave-one-out with the session model. §8.3 says "do not do this online"; if it
//               is ever built it belongs in internal/eval, off the hot path.
//
// Which one ships is a measurement decision, not a design decision: the replay harness
// (§10 Phase 5 exit criterion) reports fraction-of-OPT per scorer and that number decides.
```

**Performance budget.** `BenchmarkCheapScorer500` — 500 blocks × 8 KiB bodies plus a 64 KiB
continuation must complete in **< 250 ms** on the CI Linux runner. This is idle-time work
scheduled as `scheduler.BackgroundTask("refresh_delta")` (O3), never on the L0 hot path, so it
is bounded by `IdleController.RunOnce`'s budget rather than B-A.

### 4. `internal/analyzer/redundancy.go` (new)

Responsibility: `DetectRedundancy` across superseded reads and MinHash near-duplicates; the two
consumers named in §8.1 item 3 — eviction ranking and the summary-exclusion rule.

```go
type RedundancyOptions struct {
    NearDupThreshold float64 // from config.Store.Canonicalize.MinHash.NearDupThreshold
    MaxCandidates    int     // scan cap; default 5000
}

func DetectRedundancy(ctx context.Context, s store.Store, sess core.SessionID) (RedundancyReport, error) {
    return DetectRedundancyWithConfig(ctx, s, sess, config.Defaults())
}

func DetectRedundancyWithConfig(ctx context.Context, s store.Store, sess core.SessionID,
                                cfg config.Config) (RedundancyReport, error)
```

`DetectRedundancyWithConfig` builds `RedundancyOptions` from `cfg` — that is why the threshold
`0.9` never appears as a literal in this package (§11.6 `nomagic`; `0.9` is in the forbidden set).
`MaxCandidates` is `5000`, a named constant `maxRedundancyCandidates` (not in the forbidden set).

**Candidate enumeration.** `s.ToolUsesBySession(ctx, sess, o.MaxCandidates)` — the
session-scoped enumerator added by the blocking pre-step below. Records whose `Session != sess`
cannot occur; `core.ErrNotFound` on a single record is skipped with a Debug log, never propagated.

**`store.Search` must not be used here, and this is not a preference.** The shipped `Search`
clamps `K` to `maxK = 100` (`internal/store/search.go:41, :60-66`), caps the scanned candidate
set at `maxCandidates = 512` (`:114-118`), and `store.Query` carries no `Session` field
(`internal/store/query.go:11-25`), so an empty query with `K: 5000` returns **the 100 most recent
records of the whole project**, not the session — and a per-record `Session != sess` filter then
discards most of those 100. A session with more than 100 records would be silently analysed on a
truncated, cross-session sample, which is precisely the shape a redundancy detector must not
have. The same shipped behaviour is written up independently in
`plans/V4-SP-11-rehydrator-l5.md:506, :1113` and `plans/V4-SP-13-mcp-retrieval-layer.md:820`
(the sibling `K: 0` → `defaultK = 5` case); SP-15 is the fourth plan to meet it and the first to
depend on it, so it is stated here rather than rediscovered again.

> **Pre-step (blocking): `arch/store-tooluses-by-session`.** `store.Store` exposes no
> session-scoped enumerator (`internal/store/store.go:21-67`). Cut `arch/store-tooluses-by-session`
> off `develop`, add
> `ToolUsesBySession(ctx context.Context, sess core.SessionID, limit int) ([]ToolUseRecord, error)`
> to §5.8's `store.Store` block in `00-ARCHITECTURE.md` and to `internal/store/store.go`, and
> implement it on `*FSStore` alongside `ToolUsesByPath` (`internal/store/tooluseindex.go:263-265`)
> with a `bySessionTU map[core.SessionID][]core.ToolUseID` index built in `loadToolUse`, returning
> newest-first with `limit <= 0` meaning all — the same contract `ToolUsesByPath` already states.
> This is a §5 interface change, so 00-ARCHITECTURE §0's amendment rule applies in full: it is
> negotiated with SP-06's owner and **merged into `develop` before `feat/sp15-*` is cut**, not
> opened mid-branch as a contingency. A workaround inside `analyzer` is forbidden, and so is a
> direct read of `.qompack/index/tool_use.jsonl`, which would bypass the store's ownership of its
> own index.

**Group construction.** Records are grouped by `paths.Key(rec.Path)`; records with an empty `Path`
form no group and participate in neither pass. Within a group, records are sorted ascending by
`(TS, Turn, ID)`. A group holding more than `maxGroupPairs = 512` records is truncated to its most
recent 512 with a Debug log **before either pass runs**, so both the supersession pass and the
near-duplicate pass are bounded at `O(512²)` per path and the whole detector stays linear in the
number of paths.

**Superseded set** — union of two sources:

1. Records the observer already marked: `rec.Status == store.StatusSuperseded`.
2. Chunk-set supersession, recomputed here because a compaction may see records the observer
   marked after the fact. For each ordered pair `(older, newer)` in a group with `older` earlier:
   fetch `store.GetRoot` for both (memoized in a `map[core.Hash]map[core.Hash]struct{}` of
   chunk-hash sets) and mark `older` superseded when `chunks(older) ⊆ chunks(newer)`. Subset
   **including equality** is the test: an identical re-read is supersession of the earlier read,
   so proper-subset is deliberately *not* required. A root that fails to load contributes the
   empty set, and the empty set is never treated as a subset of anything — guard by skipping the
   pair when `len(chunks(older)) == 0` or `len(chunks(newer)) == 0`.

Output `Superseded` is deduplicated and sorted ascending by `core.ToolUseID` string order, so the
report is byte-stable for golden tests.

**Near-duplicate map.** Within each path group, for every ordered pair `(older, newer)` where
neither is already superseded and both have `Signature.Perms > 0`:
`if older.Signature.IsNearDup(newer.Signature, o.NearDupThreshold) { NearDups[older.ID] = append(…, newer.ID) }`.
The key is the **earlier** record (the one whose content is reproducible from the later one) and
the values are the later records that reproduce it. Each value slice is sorted ascending by
`core.ToolUseID` string order. `NearDups` is a Go map and therefore unordered; every consumer that
renders or serializes it (`ExcludeFromSummary`, the golden tests, `RenderActionHistory` never) must
first take `slices.Sorted(maps.Keys(r.NearDups))`, and a helper `func (r RedundancyReport) SortedNearDupKeys() []core.ToolUseID`
is provided so no consumer has to remember.

**The two consumers.**

```go
// ExcludeFromSummary is §8.1 item 3's rule: "Superseded reads ... should never appear in a
// summary." It returns superseded ids plus every near-duplicate KEY (the reproducible earlier
// read), never the later read that reproduces it.
func (r RedundancyReport) ExcludeFromSummary() map[core.ToolUseID]bool

// ApplyTo is the eviction-ranking feed: it sets Superseded=true on every Block whose NodeID
// resolves (via ToolUseIDOf) to an excluded tool_use_id, and returns the blocks in input order.
// It never reorders and never drops.
func (r RedundancyReport) ApplyTo(blocks []Block) []Block
```

**Performance budget.** `BenchmarkDetectRedundancy2000` — 2 000 tool-use records across 200
paths must complete in **< 300 ms**. Idle-time work (`BackgroundTask` reuse), not hot path.

### 5. `internal/analyzer/selector.go` and `internal/analyzer/greedy.go` (new)

#### 5.1 The objective, written out

Let `λ = lambda` (from `config.Selection.Submodular.Lambda`, Appendix C default `0.4`, passed in
by the caller — the literal never appears in this package).

Two named package constants carry the mix (neither is in the `nomagic` forbidden set, and neither
duplicates an Appendix C default — the only Appendix C value in this file is `λ`, which arrives as
a parameter):

```go
const (
    weightSlice = 0.5 // slice membership   (§6.5 "slice membership and Δ-scores become the
    weightDelta = 0.5 // retrospective Δ     coverage weights" — equal mix until the replay
)                     //                     harness says otherwise, §10 Phase 5)
```

For each block `b`:

```
slice(b) = float64(slice.Scores[b.ID])          , 0 when absent, clamped to [0,1]
Δ(b)     = delta[b.ID]                          , 0 when absent, clamped to [0,1]
w(b)     = weightSlice·slice(b) + weightDelta·Δ(b)   ∈ [0,1]
ρ(b)     = 1.0            if b.Superseded                        (§8.1 item 3)
         = 1.0            if b.Ephemeral                         (§8.7 first eviction candidate)
         = 0.0            otherwise
wEff(b)  = w(b) · max(0, 1 − λ·ρ(b))
U(b)     = { "c:" + hash.String() for each chunk hash of b.Root }
                                        when the store resolves b.Root and it has ≥ 1 chunk
         = { "node:" + string(b.ID) }   otherwise (synthetic singleton element)
b*(u)    = argmax over blocks b in the ground set with u ∈ U(b) of wEff(b),
           ties broken by (Pos asc, ID asc) so b*(u) is a single well-defined block
W(u)     = wEff(b*(u))
f(S)     = Σ_{u ∈ ⋃_{b∈S} U(b)}  W(u)
c(b)     = int(b.Tokens)
```

`f` is a weighted set-coverage function, therefore **monotone and submodular**, therefore the
`(1 − 1/e)` guarantee of Appendix A applies under a cardinality constraint and the
greedy-plus-best-singleton variant below is the knapsack form.

`f` is exactly `coverage(S) − λ·redundancy(S)` as §8.3 specifies, and the identity is exact rather
than approximate **because `W₀` and `ρ` are both read off the same block `b*(u)`**: define
`W₀(u) = w(b*(u))` and `ρ(u) = ρ(b*(u))`. Then `W(u) = W₀(u)·max(0, 1 − λ·ρ(u))` by construction,
and with `coverage(S) = Σ_{u∈cover(S)} W₀(u)` and `redundancy(S) = Σ_{u∈cover(S)} W₀(u)·ρ(u)` we get
`f(S) = coverage(S) − λ·redundancy(S)` on the nose, subject only to the documented clamp that a
block's effective weight floors at zero when `λ·ρ(b) > 1`. (Taking the argmax of `w` instead of the
argmax of `wEff` would *not* give an exact identity, because the two argmaxes can differ; this is
why `b*(u)` is defined once, over `wEff`, and both terms are derived from it.) The clamp only
strengthens monotonicity and is why the guarantee survives `λ > 1`. `λ < 0` is rejected with
`ErrLambdaNegative`. A property test `PropCoverageMinusLambdaRedundancy` asserts the identity
numerically to 1e-12 on random instances, so the claim is checked, not merely asserted.

**Shared coverage elements are where the redundancy of §4.3's "reconstruction test" actually
lives**: two reads of the same file share chunk hashes, so keeping both buys almost nothing. That
is the submodularity, and it is why `U(b)` is derived from the store's chunk sets rather than from
block identity.

#### 5.2 `NewSelector` — the two structural guards

```go
func NewSelector(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
                 lambda float64, lazy bool) (Selector, error) {
    return NewSelectorWithStore(p, blocks, slice, delta, lambda, lazy, nil)
}

func NewSelectorWithStore(p int, blocks []Block, slice dag.Slice, delta map[dag.NodeID]float64,
                          lambda float64, lazy bool, s store.Store) (Selector, error) {
    // Guard 1 — the suffix constraint (§5.3, 00-ARCHITECTURE §13 invariant 4).
    // This runs FIRST and the order is NORMATIVE: SP-01's own doc comment on NewSelector says so
    // ("The order matters and is normative: the Pos check runs FIRST, so a build in which both
    // conditions hold reports the invariant-4 violation rather than masking it behind the
    // ship-order one"), and two live tests pin it —
    // internal/analyzer/selector_test.go's TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck and
    // test/guards/buildorder_test.go's TestGuard_SubmodularInertWithoutPSelection, which passes a
    // pre-p block and requires core.ErrBudget with no PSelectionAvailable gate around it.
    for i := range blocks {
        if blocks[i].Pos < p {
            return nil, fmt.Errorf("%w: block %s at pos %d precedes p=%d",
                ErrBlockBeforeP, blocks[i].ID, blocks[i].Pos, p)
        }
    }
    // Guard 2 — ship order (Qompack.md closing note 3, 00-ARCHITECTURE §5.12).
    if !pAvailable() {
        return nil, ErrPSelectionUnavailable
    }
    if lambda < 0 {
        return nil, fmt.Errorf("%w: got %v", ErrLambdaNegative, lambda)
    }
    …
}
```

**Both refusals keep the shipped `core` sentinels and the shipped message text.** `ErrBlockBeforeP`
wraps `core.ErrBudget` and carries "§5.3, §13 invariant 4" in its own text, so the formatted error
still satisfies `require.ErrorIs(err, core.ErrBudget)`, still names the offending block
(`%s` on `blocks[i].ID`, matching `require.Contains(err.Error(), "tooluse:early")`), and still
contains the substring `invariant 4`. `ErrPSelectionUnavailable` wraps `core.ErrNotImplemented`,
so `core.IsNotImplemented(err)` still reports true and the message still contains `p-selection`.
SP-15 renames nothing and removes neither check: it replaces `Select` and gives the two existing
refusals exported names. Reordering the guards, dropping either `core` sentinel, or dropping the
phrase "invariant 4" would each break a live test on `develop` and is forbidden.

Guard 1 **errors**; it does not filter. `00-ARCHITECTURE §13` says "refuses", and refusing is
strictly safer than silently discarding a caller's block: a caller that passes pre-`p` blocks has
a bug in its candidate assembly and must be told, not accommodated. Blocks with `Pos == p` are
legal (the cut is *at* `p`; everything from `p` onward is being rewritten anyway), which
`TestNewSelector_PosEqualToPIsLegal` pins.

The constructor then materializes the immutable selection state, once, **in this order**:
(1) blocks are copied into a private slice sorted by `(Pos asc, ID asc)`; (2) `wEff` is computed
per block; (3) `U(b)` is resolved per block as a `[]string` of element keys sorted ascending;
(4) the coverage universe is built as `map[string]float64` of element → `W(u)` by a single pass
over the sorted block slice keeping the running maximum of `wEff` (first block wins ties, which is
exactly the `(Pos asc, ID asc)` tiebreak `b*(u)` is defined with).

**Order of `Selection.Keep` and `Selection.Dropped` is the constructor's sorted order**
`(Pos asc, ID asc)` — not the caller's argument order. Stating it once here removes the only
ambiguity in the output contract: `Keep` is emitted in sorted order regardless of the order greedy
picked the blocks, and `Dropped` is the sorted ground set minus `Keep`, also sorted. Both are
therefore byte-stable across runs and safe to golden.

Element keys are `"c:" + hash.String()` for chunk elements and `"node:" + string(b.ID)` for
synthetic ones — the `"c:"`/`"node:"` prefixes prevent a collision between the two namespaces.

When `s == nil` or `s.GetRoot` fails for a root, `U(b)` falls back to the synthetic singleton and
a Debug line is logged. In that degenerate case `f` is modular, the guarantee still holds, and
selection reduces to a value-density knapsack — an honest degradation, documented in the file.

`P()` returns `p`.

#### 5.3 `Select` — lazy greedy under a knapsack

```
Select(ctx, budget):
  if budget <= 0 { return Selection{Dropped: allIDs}, nil }

  // Phase A — cost-benefit lazy greedy.
  covered := map[string]bool{}
  value   := 0.0
  spent   := 0
  keep    := []dag.NodeID{}
  iters   := 0

  // Priority queue of (blockIndex, cachedRatio, stale) ordered by cachedRatio DESC,
  // tiebreak Pos ASC then ID ASC. Initial cachedRatio = gain({b}) / max(1, c(b)).
  pq := newHeap()
  for i, b := range sel.blocks {
      if c(b) > budget { continue }               // can never fit; permanently excluded
      g := marginal(i, covered); iters++
      pq.push(entry{i: i, ratio: g / float64(max(1, c(b)))})
  }

  for pq.Len() > 0 {
      if ctx.Err() != nil { return partial, ctx.Err() }
      e := pq.pop()
      b := sel.blocks[e.i]
      if c(b) > budget-spent { continue }         // no longer fits; drop it
      g := marginal(e.i, covered); iters++        // lazy re-evaluation
      r := g / float64(max(1, c(b)))
      if pq.Len() > 0 && r < pq.peek().ratio {
          pq.push(entry{i: e.i, ratio: r})        // still not the best; requeue
          continue
      }
      if g <= 0 { break }                          // monotone: no positive gain remains
      keep = append(keep, b.ID); spent += c(b); value += g
      for _, u := range sel.cover[e.i] { covered[u] = true }
  }

  // Phase B — best singleton (the knapsack correction; without it a single huge
  // high-value block can be starved by many cheap ones).
  // sel.blocks is already sorted by (Pos asc, ID asc), so strict `>` makes the
  // first-encountered block win every tie. No auxiliary comparator is needed or defined.
  bestI, bestV := -1, 0.0
  for i, b := range sel.blocks {
      if c(b) > budget { continue }
      v := f({b}); iters++
      if v > bestV { bestI, bestV = i, v }
  }
  if bestI >= 0 && bestV > value {
      keep, spent, value = []dag.NodeID{sel.blocks[bestI].ID}, c(sel.blocks[bestI]), bestV
  }

  sortByPosThenID(keep)                    // Keep is emitted in constructor order, see §5.2
  return Selection{Keep: keep, Tokens: core.Tokens(spent), Value: value,
                   Dropped: sortedGroundSet minus keep, Iters: iters}, nil
```

`marginal(i, covered)` is `Σ_{u ∈ U(b_i), u ∉ covered} W(u)` — one pass over a pre-sorted
`[]string`, no allocation.

When `lazy == false` (`config.Selection.Submodular.LazyGreedy == false`) the same loop runs but
every remaining block's marginal gain is recomputed on every iteration and the requeue branch is
skipped. This is the naive greedy used as the benchmark's control arm; it produces an identical
keep-set (lazy greedy is exact, not approximate — Minoux 1978) and a far larger `Iters`. A test
asserts `Keep` is identical between the two modes on 200 random instances.

`Selection.Value` is `f(Keep)`. `Selection.Iters` is the marginal-gain evaluation count and is
what the `(1 − 1/e)` sanity assertion and the lazy-vs-naive benchmark read.

**Performance budget.** `BenchmarkLazyGreedy2000` — 2 000 blocks, mean 8 chunks each, budget
12 000 tokens: **< 50 ms p99**. This runs inside the checkpoint/rehydration path whose enclosing
budget is B-E (`checkpoint_finalize` p99 < 2 s, 00-ARCHITECTURE §2.4), so 50 ms is a comfortable
sub-budget and is declared as one. `BenchmarkLazyGreedyEvaluations` asserts
`lazyIters ≤ naiveIters / 5` on the same instance.

### 6. `internal/grammar` (new package: `doc.go`, `sequitur.go`, `rules.go`, `codec.go`, `warn.go`)

`grammar` imports foundation packages only (00-ARCHITECTURE §3.2): `core`, `paths`, `config`,
`logging`, `obs`. It must **not** import `sketch`, so it carries its own header format.

#### 6.1 `sequitur.go` — data structures

```go
type symKind uint8
const (symTerminal symKind = iota; symNonTerminal; symGuard)

type sym struct {
    kind       symKind
    val        Symbol // terminal only
    rule       *rule  // nonterminal: referenced rule. guard: the owning rule.
    refIdx     int    // nonterminal only: this symbol's index in rule.refs (swap-remove bookkeeping)
    prev, next *sym
}

type rule struct {
    id    RuleID
    guard *sym   // sentinel: guard.next == first body symbol, guard.prev == last
    refs  []*sym // every nonterminal symbol currently referencing this rule; len(refs) == refCount
    n     int    // body length
}

func (r *rule) refCount() int { return len(r.refs) }

type digram struct{ a, b string }

type seq struct {
    root     *rule
    rules    map[RuleID]*rule // includes root under id 0
    index    map[digram]*sym  // digram -> the left symbol of its UNIQUE occurrence
    nextID   RuleID           // 1, 2, 3 …
    terms    int64            // terminals appended
    turns    []core.TurnIndex // turn per appended terminal, parallel to terms
    lastTurn core.TurnIndex
    created  core.UnixMilli   // stamped once at construction, written into the header
    mu       sync.Mutex       // the daemon appends from a worker; Rules()/Marshal read
}

func New() Sequitur { return NewWithClock(core.SystemClock()) }

// NewWithClock takes a Clock because the codec header carries a creation timestamp and
// 00-ARCHITECTURE §6.1 requires every package that takes time to take a core.Clock — without it
// MarshalBinary would be nondeterministic and the golden and determinism tests unwritable.
func NewWithClock(c core.Clock) Sequitur {
    q := &seq{rules: map[RuleID]*rule{}, index: map[digram]*sym{}, nextID: 1,
              created: core.UnixMilli(c.Now().UnixMilli())}
    q.root = q.newRuleWithID(0)
    return q
}
```

`Reset()` clears rules, index and turns and rebuilds an empty root; it leaves `created` alone so a
reset grammar keeps its identity in the header.

`key(s *sym) string` is `"t\x00" + string(s.val)` for a terminal and
`"r\x00" + strconv.Itoa(int(s.rule.id))` for a nonterminal. The `\x00` separator makes a terminal
literally named `r1` unambiguous against nonterminal `R1`.

#### 6.2 `sequitur.go` — the algorithm (Nevill-Manning & Witten 1997)

Both invariants are maintained *incrementally*, which is what makes it linear-time and online:

- **Digram uniqueness.** `index` holds at most one occurrence of any digram anywhere in the
  grammar. A second occurrence triggers `match`.
- **Rule utility.** A rule whose `uses` drops to 1 is inlined at its single reference site and
  deleted.

```
AppendAt(v, turn):
    lock
    if turn < 0 { turn = 0 }             // the codec encodes turns as unsigned LEB128; clamping
                                         // here means MarshalBinary can never fail on turn data
    n := &sym{kind: symTerminal, val: v}
    insertBefore(root.guard, n)          // append at the tail of root's body
    terms++; turns = append(turns, turn); lastTurn = turn
    if n.prev.kind != symGuard { check(n.prev) }
    unlock

Append(v): AppendAt(v, lastTurn)         // lastTurn starts at 0 on a fresh grammar

// check enforces digram uniqueness at the digram (s, s.next).
check(s) bool:
    if s.kind == symGuard || s.next == nil || s.next.kind == symGuard { return false }
    d := digram{key(s), key(s.next)}
    m, ok := index[d]
    if !ok { index[d] = s; return false }
    if m.next == s { return false }      // OVERLAPPING occurrence: "aa" inside "aaa" is one
                                         // digram, not two. This exception is required for
                                         // termination and is asserted by a property test.
    match(s, m)
    return true

// match: s is the new occurrence, m the previously indexed one.
match(s, m):
    if m.prev.kind == symGuard && m.next.next.kind == symGuard {
        // m and m.next are the ENTIRE body of an existing rule: reuse it.
        r := m.prev.rule
        substitute(s, r)
    } else {
        r := newRule()
        appendToRule(r, cloneOf(m))
        appendToRule(r, cloneOf(m.next))
        index[digram{key(r.guard.next), key(r.guard.next.next)}] = r.guard.next
        substitute(m, r)
        substitute(s, r)
    }
    // rule utility, checked at the one place it can be violated by construction
    if fs := r.guard.next; fs.kind == symNonTerminal && fs.rule.refCount() == 1 { expand(fs) }

// substitute replaces the two symbols (s, s.next) with one nonterminal referring to r.
substitute(s, r):
    left, right := s.prev, s.next.next
    deleteDigram(s)                                        // (s, s.next)
    if left.kind  != symGuard { deleteDigram(left) }        // (left, s)
    if right.kind != symGuard { deleteDigram(s.next) }      // (s.next, right)
    unlink(s); unlink(s.next)                               // drops refs on nonterminals
    n := &sym{kind: symNonTerminal, rule: r}
    link(left, n, right)                                    // link() appends n to r.refs
    if !check(n.prev) { check(n) }

// expand inlines a rule used exactly once and deletes it.
expand(s):
    r := s.rule
    left, right := s.prev, s.next
    first, last := r.guard.next, r.guard.prev
    deleteDigram(left)          // (left, s) when left is not a guard
    deleteDigram(s)             // (s, right) when right is not a guard
    left.next = first; first.prev = left
    last.next = right; right.prev = last
    if right.kind != symGuard { index[digram{key(last), key(right)}] = last }
    delete(rules, r.id)
    check(left); check(last)
```

`deleteDigram(s)` removes `index[digram{key(s), key(s.next)}]` **only if the stored pointer is
`s` itself** — otherwise it would evict a live occurrence recorded elsewhere.

**Reference tracking, spelled out** (this is the one place a naive Sequitur port goes wrong).
Each `rule` owns `refs []*sym`, the exact set of nonterminal symbols currently pointing at it, and
each such `sym` remembers its slot in `refIdx`:

```
link(left, n, right):   splice n between left and right
                        if n.kind == symNonTerminal {
                            n.refIdx = len(n.rule.refs); n.rule.refs = append(n.rule.refs, n)
                        }

unlink(s):              splice s out
                        if s.kind == symNonTerminal {
                            r := s.rule; last := len(r.refs)-1
                            r.refs[s.refIdx] = r.refs[last]        // swap-remove, O(1)
                            r.refs[s.refIdx].refIdx = s.refIdx
                            r.refs = r.refs[:last]
                            if len(r.refs) == 1 { expand(r.refs[0]) }   // rule-utility invariant
                        }
```

`len(r.refs)` **is** the reference count; there is no separate counter to drift, and the surviving
reference when the count falls to 1 is `r.refs[0]` — found in O(1) with no scan and no ambiguous
"last linked" back-pointer. The root rule (id 0) has `refs == nil` forever and is never expanded.
Rule ids are never reused; `nextID` increments monotonically.

**Complexity.** Every `AppendAt` performs O(1) amortized work: one insertion plus a bounded
cascade of `check`/`substitute`/`expand` steps, each of which strictly reduces the total symbol
count of the grammar. §8.1 states the requirement — "Sequitur and BOCD updates are O(1)
amortized" — and `BenchmarkSequiturAppend` enforces it at **< 20 µs/op p99** over 200 000 appends
from a 12-symbol alphabet. Appends happen daemon-side in the **asynchronous** ingest worker, i.e.
inside budget **B-C** (`l0_process` — WAL → fully chunked, stored, DAG/sketches updated, p99 < 50 ms,
soft; 00-ARCHITECTURE §2.4), never inside B-B (`l0_ingest`, which ends the moment the WAL append
returns) and never in the client. 20 µs against a 50 ms soft budget is three orders of magnitude of
headroom by design.

#### 6.3 `rules.go` — projections

```go
// Rules returns every NON-root rule ascending by ID. The root is available via Compressed().
func (q *seq) Rules() []Rule
```

For each rule: `Body` is the body with nonterminals rendered as `Symbol("R" + itoa(id))`;
`Expansion` is the fully expanded terminal sequence, memoized in a `map[RuleID][]Symbol` built by
depth-first expansion (Sequitur grammars are acyclic by construction — a rule can only reference
rules that already existed when it was created); `Span = len(Expansion)`.

**`Rule.Uses` is the occurrence multiplicity, not the reference count.** This is a deliberate,
load-bearing decision and it is the difference between a working thrash detector and a useless one.

*Why.* §6.3 says "`read → edit → test → fail` repeated eleven times becomes one production plus a
count" and "a nonterminal with high multiplicity is a loop the agent is stuck in". Sequitur builds
a *hierarchy*: eleven repetitions of a 4-cycle produce `R3 = read edit test fail` and then further
rules `R4 = R3 R3`, `R5 = R4 R4`, so `R3`'s raw reference count settles at 2 or 3 — it never
approaches 11. Reporting the reference count would make `Thrash(3)` fire on an unrepeated grammar
and report "repeated 2×" for a loop the user has watched run eleven times. The count §6.3 means is
how many times the rule's expansion actually occurs in the appended sequence.

*Definition.* `occ(root) = 1`; for every other rule `r`,
`occ(r) = Σ over rules q of occ(q) · (number of symbols in q's body referencing r)`.
Computed once per `Rules()` call by a single memoized pass in ascending rule-id order (legal
because a rule only ever references lower-id rules, so `occ` of every referrer is already known —
the same acyclicity that makes `Expansion` memoizable). `Rule.Uses = occ(r)`.

*Consequences, all of them intended.* `Uses ≥ refCount ≥ 2` for every non-root rule, so
`PropRuleUtility` ("every rule in `Rules()` has `Uses >= 2`") still holds. `Uses × Span` is exactly
the number of terminals the loop consumed, which is why `Thrash` ranks on that product.
`Warning.Repeats = Rule.Uses` renders as "repeated 11×" for an eleven-times loop.
`len(turnsForRule(id)) == Uses`. The internal `refCount()` is private, is the only thing the
digram/utility invariants are maintained against, and is never exported.

```go
// Compressed is the grammar-compressed action history: the root body with nonterminals
// rendered as "R<id>". This is what §5.5 calls "folded into the summary at compaction time".
func (q *seq) Compressed() []Symbol

// Thrash returns rules with Uses >= minUses AND Span >= DefaultThrashMinSpan, sorted by
// (Uses*Span) DESC then ID ASC. A nonterminal with high multiplicity is a loop the agent is
// stuck in (§6.3).
func (q *seq) Thrash(minUses int) []Rule

// Reset clears the grammar to a fresh single empty root, keeping no rules and no turns.
func (q *seq) Reset()
```

**A single agent loop normally yields several qualifying `Thrash` rules, not one, and that is
correct.** Sequitur is hierarchical: eleven repetitions of `read edit test fail` produce the
4-symbol rule (`Uses` 11, `Span` 4, product 44), an 8-symbol rule pairing it with itself (`Uses` 5,
`Span` 8, product 40), a 16-symbol rule above that, and the 2-symbol sub-rules `read edit` and
`test fail` (`Uses` 11, `Span` 2, product 22). All describe the same loop at different granularities.
The `(Uses*Span) DESC` sort is precisely what puts the *most informative* granularity first — the
product is the number of terminals the rule accounts for, and it is maximized at the rule that most
closely matches the true cycle — and `WarnOptions.MaxWarnings` (default 2) is what stops the user
seeing the same loop reported five times. Do not try to deduplicate rules by containment; the sort
plus the cap is the whole policy, and it is testable.

`turnsForRule(id RuleID) []core.TurnIndex`: walk the **full depth-first expansion** of the root
body left to right, maintaining a running count of terminals emitted so far; every time the walk
*enters* rule `id` — at root level or nested at any depth — record `turns[terminalCount]`, the turn
of the first terminal that occurrence covers. The result is ascending by construction, has exactly
`Rule.Uses` entries, and is **not** deduplicated (two occurrences that began on the same turn are
two occurrences and both are reported). It is empty when the grammar carries no turn data, and any
index `≥ len(turns)` is skipped rather than panicking — a grammar decoded from a truncated older
file may legitimately have fewer turns than terminals.

#### 6.4 `codec.go` — `grammar/actions.seq`, byte-for-byte

Little-endian throughout. `varuint` is unsigned LEB128.

```
offset  size   field
0       4      magic     'Q','P','K','G'   (0x51 0x50 0x4B 0x47)
4       2      ver       uint16  = FormatVersion (1)
6       2      flags     uint16  = 0
8       8      terms     uint64  total terminals appended
16      8      created   int64   core.UnixMilli
24      4      nRules    uint32  number of rules INCLUDING the root
28      4      crc32c    uint32  Castagnoli CRC over bytes[32:] only
32      …      payload
```

Payload, rules first, ascending by `RuleID` with the root (id 0) always first:

```
per rule:
  varuint  ruleID
  varuint  refs            reference count (len(rule.refs)); the root writes 0
  varuint  bodyLen
  bodyLen × symbol:
      byte 0x00  → terminal:    varuint byteLen, byteLen bytes UTF-8
      byte 0x01  → nonterminal: varuint ruleID
```

The `refs` field is the *internal reference count*, not the exported `Rule.Uses` occurrence
multiplicity — `Uses` is derived from the rule bodies on demand (§6.3) and is therefore never
persisted, so it can never disagree with the grammar it was computed from. On decode, `refs` is
cross-checked against the reference counts actually observed while re-linking the bodies; a
mismatch is a decode error (`"grammar: rule %d declares %d refs, body scan found %d"`).

Then the turn section:

```
  varuint  nTurns          (== terms)
  nTurns × varuint  turnIndex   (append order; negative turns are rejected on write)
```

`MarshalBinary` returns the whole buffer. `UnmarshalBinary` validates, in order: length ≥ 32;
magic; `ver == FormatVersion` (a different version returns
`fmt.Errorf("grammar: unsupported format version %d", v)`); CRC32C over `b[32:]`; `nRules ≥ 1`;
the root present with id 0; every nonterminal reference resolving to a declared rule; `nTurns ==
terms`. Any failure returns an error and leaves the receiver **unmodified** (decode into a
temporary and swap only on success) — a corrupt `actions.seq` must never destroy live in-memory
state. Reconstruction rebuilds the linked lists and the digram index from the decoded bodies by
re-indexing every adjacent pair; it does **not** re-run `check`, because the decoded grammar
already satisfies both invariants and re-running would be quadratic.

```go
// Save creates the parent directory (os.MkdirAll(filepath.Dir(path), 0o755)) and then calls
// paths.WriteAtomic(path, bytes). actions.seq is NOT an append-only target — 00-ARCHITECTURE §3.3
// lists only checkpoints/, pins/ and sketches/tried.bloom — so atomic replace is correct here.
func Save(path string, g Sequitur) error

// Load is os.ReadFile + UnmarshalBinary. A missing file returns core.ErrNotFound so the daemon
// can start with a fresh grammar; a corrupt file returns the decode error, and the caller logs
// Loud and calls Reset() rather than aborting the session (§12 "fails toward do nothing").
func Load(path string, g Sequitur) error
```

**Performance budget.** `BenchmarkGrammarMarshal50k` — a grammar built from 50 000 appends must
marshal **and** unmarshal in **< 20 ms** combined, and the marshalled size must be
**< 512 KiB**. A round-trip golden fixture at `testdata/golden/grammar/actions_v1.seq` pins the
byte layout across versions.

#### 6.5 `warn.go` — thrash detection and formatting

```go
const (
    DefaultThrashMinUses = 3  // a cycle seen three times is a loop, not a coincidence
    DefaultThrashMinSpan = 2  // §5.11: "whose expansion length >= 2"
    defaultMaxWarnings   = 2  // never flood a single prompt
)

func DefaultWarnOptions() WarnOptions {
    return WarnOptions{MinUses: DefaultThrashMinUses, MinSpan: DefaultThrashMinSpan,
                       MaxWarnings: defaultMaxWarnings}
}

func WarningsFor(g Sequitur, o WarnOptions) []Warning
```

`WarningsFor` calls `g.Thrash(o.MinUses)`, filters `Span >= o.MinSpan`, takes the first
`o.MaxWarnings`, and builds one `Warning` each with `Repeats = Rule.Uses`,
`Turns = turnsForRule(Rule.ID)` (empty when the grammar carries no turn data), and

```go
// thrashSuggestion is the §5.11 "short, human-readable suggestion" that grammar.Warning.Message
// is documented to carry. FormatWarning interpolates it; it is not the formatted line.
const thrashSuggestion = "consider a different approach; call already_tried before retrying"
```

`Message = thrashSuggestion` for every warning. **`Message` is the suggestion, never the rendered
line.** Setting `Message = FormatWarning(w)` would be self-referential against the shipped
formatter, which interpolates `w.Message` into its own output.

**Memoization — required, not an optimization.** `Rules()`, `Thrash()` and `turnsForRule()` all walk
the whole grammar, and `PromptAddendum` runs on the `UserPromptSubmit` reply path, which is the hot
path gated by **B-A** (`hook_controlled` p99 < 15 ms, 00-ARCHITECTURE §2.4). `seq` therefore keeps a
`dirty bool` set by `AppendAt` and `Reset` and cleared by the first projection call after a change;
`Rules()`, `Compressed()`, the `occ` map, the `Expansion` map and `turnsForRule` results are cached
behind it under the same `mu`. Steady state on a prompt with no new tool calls since the last one is
therefore a cache read costing O(number of warnings), not O(grammar). `BenchmarkWarningsFor` measures
the cold (dirty) path, which is the one that must fit the budget.

**`FormatWarning` is SP-01's, is frozen, and SP-15 does not modify it.** `internal/grammar/formatwarning.go`
already ships it as a real function — not a stub — with wording that 00-ARCHITECTURE §5.11 left
open and SP-01 deliberately closed:

```go
// internal/grammar/formatwarning.go — SHIPPED, unchanged by this subplan
func FormatWarning(w Warning) string {
    return fmt.Sprintf("[qompack] possible loop: %s repeated %d× (turns %s) — %s",
        strings.Join(symbolStrings(w.Rule.Expansion), "→"), w.Repeats, turnRange(w.Turns), w.Message)
}
```

Three exact strings are pinned unconditionally by `internal/grammar/formatwarning_test.go`
(a test whose own header notes that, unlike everything else in the package, `FormatWarning` is real
and therefore never skipped), and `plans/V1-VERIFY-foundation-and-contracts.md` L11 gates the shape
`[qompack] possible loop: A→B→C repeated N× (turns X–Y) — <message>`. `turnRange` renders an
en-dash min–max span (`42–74`, or `42` for a single turn) and the empty string for empty turns; the
symbol join is `"→"` with **no** surrounding spaces. **SP-15 neither re-words the line nor adds a
`renderTurns`: `internal/grammar/formatwarning.go` and `formatwarning_test.go` are not in this
subplan's file list, and the V1 goldens are untouched.** Everything SP-15 wanted from a re-wording
it gets from `Warning.Message`, which is exactly what that field is for.

The composed line, pinned as a golden at `testdata/golden/grammar/thrash_warning.txt`, is therefore
what `FormatWarning` produces from a `WarningsFor` output — a fixture of the composition, not a
second definition of the format:

```
[qompack] possible loop: FileRead→FileEdit→Bash→test:fail repeated 11× (turns 14–39) — consider a different approach; call already_tried before retrying
```

`PromptAddendum(g Sequitur, o WarnOptions) string` returns `""` when there are no warnings, and
otherwise `FormatWarning` applied to each warning, joined with `"\n"`. This is the exact string the
daemon appends to `hookSpecificOutput.additionalContext` on `UserPromptSubmit`.

`WarningsFor` always populates `Turns` for a grammar built through `AppendAt`, so the degenerate
empty-`turnRange` rendering (`(turns )`) is unreachable in production; `TestWarningsForPopulatesTurns`
asserts that rather than re-testing SP-01's formatter.

### 7. Thrash-warning delivery — `internal/daemon/grammar_addendum.go` (new) and four lines in `New()`

§5.21 of 00-ARCHITECTURE is normative: **"No subplan other than SP-08 writes code in
`internal/observer`."** Delivery therefore happens in the daemon, which §2.4 designs as the place
later waves wire into ("a late-bound `Services` set", "the op-routing table is data, not a
switch"). SP-15 adds exactly one new file and one four-line guarded block.

**The addendum decorates the `Services.ObservePrompt` seam through `Options.Bind`; it does not
override the `observe.prompt` route through `Options.Handle`.** This is not a stylistic choice — a
route-level override is *wrong*, for two independent reasons the shipped handler makes plain:

1. **It would bypass the §12 mode gate.** `handleObservePrompt` gates twice
   (`internal/daemon/handlers.go:376-381`, `:401-403`): it returns `hookio.Empty()` outright when
   `!mode.MayRecord()` (`ModeOff`), and it calls the seam **only** when `mode.MayAct()`, which
   `internal/contract/mode.go:53-55` makes false for `ModeDegradedPassive` and `ModeOff`. A wrapper
   sitting outside the route appends `additionalContext` after the handler has already returned its
   inert response — so Qompack would act while degraded, and act while the operator had explicitly
   switched it off. Decorating the seam inherits both gates for free, and the route-level shape
   cannot be repaired in place: `ipc.Handler` has no access to `d.monitor`.
2. **It would run outside `promptReplyDeadline`.** `callObservePromptWithDeadline`
   (`internal/daemon/handlers.go:406-435`) races the seam call against
   `promptReplyDeadline = 250 * time.Millisecond` (`:22`) in a goroutine and falls back to
   `hookio.Empty()` — *"a prompt is never blocked on the daemon"*. That race wraps
   `d.svc.ObservePrompt` and nothing else. `observe.prompt` is a hot-path op gated at B-A
   p99 < 15 ms, so grammar work run *after* the handler returns is charged to the request with no
   bound of any kind — and SP-15's own out-of-scope table hands hot-path budgets to SP-05. Inside
   the seam it is bounded by the same 250 ms race as every other prompt-path callee.

**The bind is appended at `Options` time, before `New` applies the bind list.** `New` seeds
`Services` from `Options`, then runs `for _, bind := range o.binds { bind(svc) }`
(`internal/daemon/daemon.go:222-233`) before it builds the route table — so a bind appended last
wraps whatever every earlier bind (SP-08's `ObservePrompt` in particular) installed, and reading
`s.ObservePrompt` inside the bind is the documented way to compose onto a seam another subplan
owns: *"This is the seam a wave-2/3 subplan uses to attach its own function seams … without
editing daemon internals or colliding with a sibling subplan doing the same thing."* `Services` is
a plain struct mutated once during construction and read thereafter, so there is no mutex and no
race — the same property that makes `Options.Handle` the right seam for a route makes `Bind` the
right seam for a service. Registration order is the one constraint: SP-15's `Bind` must be
appended after SP-08's, which the call site below guarantees by construction.

```go
// WrapObservePromptWithThrashWarning appends the L2 thrash warning to the UserPromptSubmit
// output's additionalContext (Qompack.md §8.1 item 6, §10 Phase 6). It decorates the
// Services.ObservePrompt seam, so it runs only when mode.MayAct() and only inside
// callObservePromptWithDeadline's 250 ms race. It wraps rather than replaces, so SP-08's observer
// semantics are untouched and internal/observer is not edited.
func WrapObservePromptWithThrashWarning(
    inner func(ctx context.Context, e hookio.Event) (hookio.Output, error),
    g grammar.Sequitur, o grammar.WarnOptions, log logging.Logger,
) func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
    return func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
        out := hookio.Empty()
        if inner != nil {
            var err error
            if out, err = inner(ctx, e); err != nil { return out, err }
        }
        if g == nil { return out, nil }
        add := grammar.PromptAddendum(g, o)
        if add == "" { return out, nil }
        if out.HookSpecificOutput == nil {
            out.HookSpecificOutput = &hookio.HSO{HookEventName: "UserPromptSubmit"}
        }
        h := out.HookSpecificOutput
        if h.AdditionalContext == "" { h.AdditionalContext = add } else { h.AdditionalContext += "\n" + add }
        log.Info("thrash warning delivered", "session", string(e.SessionID), "bytes", len(add))
        return out, nil
    }
}

// AttachThrashWarning appends the seam decoration to the Options bind list, before New applies it.
// It is a no-op returning nil when there is no grammar (waves 1–2 run with a nil Sequitur).
func AttachThrashWarning(o *Options, g grammar.Sequitur, wo grammar.WarnOptions, log logging.Logger) error
```

Its body, fully specified:

```go
func AttachThrashWarning(o *Options, g grammar.Sequitur, wo grammar.WarnOptions, log logging.Logger) error {
    if o == nil {
        return errors.New("qompack: cannot attach thrash warning to a nil Options")
    }
    if g == nil { return nil }                      // waves 1–2 run with a nil Sequitur
    if log == nil { log = logging.Nop() }

    o.Bind(func(s *Services) {
        // s.ObservePrompt is whatever every earlier Bind left there — SP-08's, normally. A nil
        // seam is legal and the wrapper handles it: the addendum is then the whole output.
        s.ObservePrompt = WrapObservePromptWithThrashWarning(s.ObservePrompt, g, wo, log)
    })
    return nil
}
```

**`Options.Handle`, `Options.Handler` and `defaultObservePromptHandler` play no part.** An earlier
draft of this section registered a wrapped `ipc.Handler` for `ipc.OpObservePrompt` on the Options
and delegated to `(*daemon).handleObservePrompt` through the `Daemon` value in the request context.
That shape is deleted: it put the addendum outside both the `mode.MayAct()` gate and the 250 ms
prompt-reply race, and its only reason for existing — needing to reach the daemon's own default
route — disappears with `Bind`, which composes onto the seam the default route already calls. One
consequence worth stating: nothing in this file now needs `(*daemon)`, `DaemonFrom` or the route
table, so `internal/daemon/grammar_addendum.go` is in package `daemon` only so that `New` can call
`AttachThrashWarning` (below) without an import cycle.

The single call site, added to `internal/daemon/daemon.go`'s `New()` **immediately before**
`for _, bind := range o.binds { bind(svc) }` (`daemon.go:232`) — which is the last moment a bind can
still be appended, and which guarantees SP-15's decoration runs after every bind a composition root
registered, SP-08's included:

```go
    // SP-15: L2 thrash warning delivery (§8.1 item 6, §10 Phase 6).
    if o.Grammar != nil {
        if err := AttachThrashWarning(&o, o.Grammar, grammar.DefaultWarnOptions(), o.Log); err != nil {
            o.Log.Warn("thrash warning not attached", "err", err)
        }
    }
    for _, bind := range o.binds {
        bind(svc)
    }
```

`New` takes `Options` by value, so `&o` is the same value whose `binds` slice the loop below reads,
and the appended bind is visible to it. The error return is non-fatal at the call site (it logs
`Warn` and continues), so a daemon SP-05 later restructures degrades to "no thrash warning", never
to a broken prompt path. Four lines, contiguous, guarded — trivially resolvable if SP-14 or SP-16
touch the same function (00-ARCHITECTURE §9: conflicts are resolved on the incoming branch, then
re-merged).

**No `routeFor` accessor, no route registration, no mutex, no `Daemon`-interface change, and no
amendment.** `d.routes` is neither read nor written by this subplan: `ipc.OpObservePrompt` keeps
SP-05's own `d.handleObservePrompt`, and the addendum is composed onto the service that handler
already calls — inside the `mode.MayAct()` gate and inside the 250 ms deadline race. That is the
whole mechanism. `go test -race ./internal/daemon/...` is the check.

### 8. `internal/checkpoint/grammar.go` (new file in SP-10's package) and 4 lines in `writer.go`

**No schema change and no `version` bump.** The compressed action history lands in fields that
already exist: `Narrative` (tier 3, "truncate first" — the correct tier for a compression
artefact) and `SketchRefs` (a `map[string]string`, so adding a key changes no JSON shape).

```go
// BuildActionHistory projects the Sequitur grammar into a checkpoint-shaped record.
func BuildActionHistory(g grammar.Sequitur, o grammar.WarnOptions) ActionHistory {
    var h ActionHistory
    if g == nil { return h }

    all := g.Rules()                          // unfiltered: needed for the exact terminal count
    span := make(map[string]int, len(all))    // "R<id>" -> Span (expanded terminal length)
    for _, r := range all { span["R"+strconv.Itoa(int(r.ID))] = r.Span }

    for _, r := range all {                   // filtered: only rules worth showing a reader
        if r.Span < o.MinSpan || r.Uses < 2 { continue }
        h.Rules = append(h.Rules, ActionRule{
            ID: "R" + strconv.Itoa(int(r.ID)), Body: symbolsToStrings(r.Body),
            Uses: r.Uses, Span: r.Span})
    }

    h.Sequence = symbolsToStrings(g.Compressed())
    h.Compressed = len(h.Sequence)
    h.Symbols = 0
    for _, s := range h.Sequence {            // EXACT expanded terminal count, not an estimate
        if n, ok := span[s]; ok { h.Symbols += n } else { h.Symbols++ }
    }

    for _, w := range grammar.WarningsFor(g, o) {
        // FormatWarning, not w.Message: the note must carry the whole rendered line (rule,
        // repeats, turn span AND suggestion), because RenderActionHistory prints it verbatim and
        // the Phase 6 exit assertion greps the narrative for it. w.Message alone is only the
        // suggestion half (§6.5).
        h.Thrash = append(h.Thrash, ThrashNote{Rule: "R" + strconv.Itoa(int(w.Rule.ID)),
            Repeats: w.Repeats, Message: grammar.FormatWarning(w)})
    }
    return h
}
```

`h.Symbols` is therefore the number of tool actions the grammar encodes and `h.Compressed` the
number of symbols after compression; their ratio is the "90%+ reduction" §6.3 claims, and
`RenderActionHistory` prints both.

`RenderActionHistory(h ActionHistory) string` produces a deterministic prose block. It returns `""`
when `h.Symbols == 0` (which covers both the zero-value history and a nil grammar). Otherwise it is
built from exactly these four format strings, joined with `"\n"`, with **no trailing newline**:

```go
header := fmt.Sprintf("Action history (grammar-compressed, Qompack.md §6.3): %d tool actions → %d symbols.",
                      h.Symbols, h.Compressed)
rule   := fmt.Sprintf("%s = %s (×%d)", r.ID, strings.Join(r.Body, " "), r.Uses)   // one per rule
seqLn  := fmt.Sprintf("sequence: %s", strings.Join(seqShown, " ")) // + " …" iff truncated
thrash := fmt.Sprintf("thrash: %s", note.Message)                  // one per ThrashNote
```

Order: header, then every rule ascending by numeric ID, then the `sequence:` line, then every
`thrash:` line in `h.Thrash` order. `seqShown` is `h.Sequence` truncated to its first
`maxRenderedSequence = 60` entries; the `" …"` suffix is appended **only** when truncation actually
occurred. Note the single space before `(×%d)` — the golden is byte-exact, so this is normative.

Worked example (this is `testdata/golden/checkpoints/action_history.txt`'s shape, with illustrative
numbers):

```
Action history (grammar-compressed, Qompack.md §6.3): 412 tool actions → 37 symbols.
R3 = FileRead FileEdit Bash test:fail (×11)
R7 = Grep FileRead (×4)
sequence: user R3 R3 R3 Grep R7 R3 …
thrash: [qompack] possible loop: FileRead→FileEdit→Bash→test:fail repeated 11× (turns 14–39) — consider a different approach; call already_tried before retrying
```

Every line is plain text; **no fenced code blocks, no leading tabs, and no line indented by four or
more spaces**, because 00-ARCHITECTURE §13 invariant 5 has a CI test that greps checkpoint goldens
for multi-line code blocks. `symbolsToStrings` must therefore also reject any symbol containing
`"\n"` or "```" by replacing it with `"?"` — tool names never contain those, but a decoded corrupt
grammar could, and invariant 5 is not allowed to fail on untrusted input.

```go
// FoldActionHistoryInto folds the compressed history into the checkpoint at compaction time —
// §5.5 classifies Sequitur as cache-Safe precisely because it is "folded into the summary at
// compaction time, not rewritten in place". It appends; it never rewrites Narrative.
func FoldActionHistoryInto(c *Checkpoint, g grammar.Sequitur, o grammar.WarnOptions) {
    h := BuildActionHistory(g, o)
    s := RenderActionHistory(h)
    if s == "" { return }
    if c.Narrative == "" { c.Narrative = s } else { c.Narrative += "\n\n" + s }
    if c.SketchRefs == nil { c.SketchRefs = map[string]string{} }
    c.SketchRefs["grammar"] = "grammar/actions.seq"
}
```

**The `writer.go` edit.** Inside `Finalize`, on the assembled `Checkpoint` value and
**immediately before the call to `Truncate`** (so the fold participates in importance-ordered
truncation and, being tier 3, is dropped first when the budget is tight — §6.9), insert:

```go
    // SP-15: grammar-compressed action history (§6.3, §5.5 "folded into the summary at
    // compaction time, not rewritten in place", §10 Phase 6).
    FoldActionHistoryInto(&c, src.Grammar, grammar.DefaultWarnOptions())
```

`src` is the `SourceSet` the draft already carries (SP-10 stores it when `Begin` is called); use
whatever local identifier `Finalize` binds it to. `FoldActionHistoryInto` tolerates a nil
`Grammar`, so no guard is needed.

**Performance budget.** `BenchmarkFoldActionHistory` — a 50 000-append grammar folds in
**< 20 ms**, a sub-budget of B-E (`checkpoint_finalize` p99 < 2 s).

### 9. `test/replay/policy_analyzer.go`, `phase5_exit_test.go`, `phase6_exit_test.go` (new)

`internal/eval` may import foundation packages only, so an `eval.Policy` backed by the analyzer
cannot live there. It lives under `test/`, which is outside `internal/` and outside the import
DAG, in files whose names no sibling subplan uses.

```go
type PolicyDeps struct {
    Store    store.Store
    Graph    dag.Graph
    Scorer   analyzer.DeltaScorer
    Cfg      config.Config    // whole config; Lambda/Lazy below are read from it by the caller
    Lambda   float64          // config.Selection.Submodular.Lambda
    Lazy     bool             // config.Selection.Submodular.LazyGreedy
    K        int              // continuation horizon in turns; 0 means use defaultContinuationK
    Baseline eval.Policy      // supplies p and the candidate set; see "the baseline" below
}

const (
    defaultContinuationK = 20 // turns of observed continuation fed to the Δ proxy
    criteriaLookbackTurns = 3 // turns before `at` whose file nodes seed the backward slice
    sliceDecay           = 0.85 // dag.SliceOptions default (00-ARCHITECTURE §5.9)
)

// NewSuffixSubmodularPolicy is Phase 5's policy: the baseline chooses p and classifies droppable
// blocks; SP-15 chooses which of the post-p blocks to keep.
func NewSuffixSubmodularPolicy(d PolicyDeps) eval.Policy
```

**The baseline — decided here so nobody has to ask.** `PolicyDeps.Baseline` is the p-selection-only
policy the Phase 5 comparison is run against. 00-ARCHITECTURE does not oblige SP-12 to ship an
`eval.Policy`, so SP-15 does not depend on one existing: `test/replay/policy_baseline_p.go` (an
SP-15 file) defines `NewPSelectionBaselinePolicy(d PolicyDeps) eval.Policy`, `Name() == "p-selection-baseline"`,
which calls `scheduler.Evaluate` to obtain `p` and then fills the budget with the post-`p` blocks in
the §8.7 eviction order — ephemeral last, then superseded, then ordinary compactable tool results,
then everything else — keeping in that order until the budget is exhausted. That is exactly "wave 3
behaviour with no analyzer", which is the control arm Phase 5 needs. If SP-12 *did* ship a policy on
`develop`, use it instead and delete `policy_baseline_p.go`; the Phase 5 assertion is unchanged
either way because it compares two `eval.Score`s, not two implementations.

`KeepSet(ctx, s, at, budget)`:
1. `base, err := d.Baseline.KeepSet(ctx, s, at, budget)` — `base.P` is `p`.
2. Assemble `[]analyzer.Block` from `d.Graph.NodesAfter(base.P)`, mapping
   `Node{ID, Pos, Tokens, Kind, Root, Ephemeral}` straight across. `Superseded` stays false here;
   step 3 sets it.
3. `rep, _ := analyzer.DetectRedundancyWithConfig(ctx, d.Store, core.SessionID(s.ID), d.Cfg)`;
   `blocks = rep.ApplyTo(blocks)`.
4. `slice, _ := d.Graph.BackwardSlice(criteriaFor(d.Graph, s, at), dag.SliceOptions{Thin: true, Decay: sliceDecay})`.
   `criteriaFor` is the §8.3 criterion set restricted to what a logged session exposes. It builds
   the criterion list **by `Node.Kind`, never by parsing the `"<kind>:<key>"` prefix** — that
   spelling belongs to SP-07 and this subplan does not match on it anywhere (see `ToolUseIDOf`).
   Concretely: call `d.Graph.NodesAfter(-1)` to enumerate every node regardless of position, then
   take (a) every node with `Kind == dag.KindFile` and `Turn ∈ [at-criteriaLookbackTurns, at]`, and
   (b) the single node with `Kind == dag.KindUserPrompt` having the greatest `Turn <= at`. Sort the
   resulting ids ascending so the slice is reproducible. If the list is empty, skip the slice
   entirely and pass the zero `dag.Slice`; `w(b)` then reduces to `weightDelta·Δ(b)`, which is a
   documented degradation, not an error.
5. `delta, _ := d.Scorer.Score(ctx, blocks, continuationOf(s, at, k))` where `k = d.K` or
   `defaultContinuationK` when `d.K == 0`, and `continuationOf` concatenates `Turn.Text` for turns
   `at+1 … at+k` (clamped to `len(s.Turns)`), collects the union of `ToolCall.Paths` into
   `Continuation.Paths`, sets `FromTurn = at+1`, and leaves `Continuation.Symbols` empty unless a
   `SymbolRefs` was supplied to the scorer. This is the **observed** continuation §4.3 calls the
   retrospective proxy.
6. **The operator opt-out, checked here because this is where the config lives.** If
   `!d.Cfg.Selection.Submodular.Enabled`, return `base` unchanged without constructing a selector —
   byte-identical to the `ErrPSelectionUnavailable` branch below. With the default flipped to
   `true` (see the Interface contract's ship-order decision), this branch is taken only when an
   operator has explicitly set `runtime.selection.submodularEnabled: false`.
7. `sel, err := analyzer.NewSelectorWithStore(base.P, blocks, slice, delta, d.Lambda, d.Lazy, d.Store)`.
   On `errors.Is(err, analyzer.ErrPSelectionUnavailable)` return `base` unchanged — the policy is
   inert, exactly as closing note 3 requires. On `errors.Is(err, analyzer.ErrBlockBeforeP)` the
   candidate assembly in step 2 is wrong; fail the test loudly rather than falling back, because a
   silent fallback would hide a violation of invariant 4.
8. `out, err := sel.Select(ctx, budget)`; return
   `eval.KeepSet{IDs: stringsOf(out.Keep), Tokens: out.Tokens, P: base.P}`.

`Name()` returns `"analyzer-suffix-submodular"`.

**Phase 5 exit assertion** (`phase5_exit_test.go`, part of the `replay-gate` job):

- Load `testdata/sessions/synthetic` (24 sessions) via `eval.Harness.Load`.
- Score the baseline policy and the analyzer policy at the **same budget**
  (`checkpoint.budgetTokens` = 12 000, from `config.Defaults()`).
- Assert every session carries `Meta["shape"]`; `require.NotEmpty` naming SP-02 if not (see the
  Interface contract's note on `eval.Session`).
- Assert `analyzerScore.FractionOfOPT > baselineScore.FractionOfOPT` across the full corpus.
- Assert `analyzerScore.FractionOfOPT − baselineScore.FractionOfOPT >= 0.02` on the subset whose
  `Meta["shape"]` is `"read-heavy"` or `"refactor-across-files"` (00-ARCHITECTURE §6.3's own spec
  names, used verbatim), where redundant re-reads give selection the most room.
- Assert §11.3's 2 % rule: no secondary metric in `eval.Score` (`Divergence.FileSetJaccard`,
  `Divergence.FirstDivergenceTurn`, `RewriteTokens`, `RehydrationTokens`) regresses by more than
  2 % against the baseline.
- Write the observed numbers to `testdata/replay-baseline/phase5.json`
  (`{"baseline_fraction_of_opt": …, "analyzer_fraction_of_opt": …, "delta": …, "sessions": 24}`)
  and assert the committed file matches to 4 decimal places, so the number is reviewable in the
  diff and cannot silently drift.

**Phase 6 exit assertion** (`phase6_exit_test.go`):

Qompack.md's criterion is "thrash detected before the user notices it, on replay". Operationalized
against the corpus with no new fixture fields:

- For each session with `Meta["shape"] == "thrash-loop"`: feed `grammar.New()` one symbol per
  tool call in turn order via `AppendAt(Symbol(tc.Name), turn)`, calling
  `grammar.WarningsFor(g, grammar.DefaultWarnOptions())` after every append. Record
  `detectTurn` = the turn of the first non-empty result. Require at least one such session in the
  corpus (`require.NotZero(thrashSessions)`), so an empty subset can never make the gate vacuous.
- Let `cycleEndTurn` be the turn of the last tool call belonging to the repeated cycle (the
  expansion of the thrash rule with the greatest `Uses × Span` at end of session), and
  `turnOfThirdOccurrence` be the third entry of `turnsForRule` for that same rule, obtained through
  the exported `Warning.Turns` of the end-of-session warning set.
- Assert `detectTurn <= turnOfThirdOccurrence` and `detectTurn <= cycleEndTurn - 5`. Detection at
  or before the third repetition, with at least five turns of loop still to run, is the
  measurable form of "before the user notices."
- For **every** session whose shape is not `"thrash-loop"` (do not hardcode how many there are —
  iterate the corpus and filter), assert `grammar.WarningsFor(g, grammar.DefaultWarnOptions())`
  is empty at end of session — a zero-false-positive requirement, without which "detected early"
  is worthless.
- Assert the checkpoint fold: build a `Checkpoint` from each thrash session's grammar via
  `checkpoint.FoldActionHistoryInto` and require `SketchRefs["grammar"] == "grammar/actions.seq"`
  and a non-empty `Narrative` containing `"[qompack] possible loop:"` — SP-01's frozen prefix
  (`internal/grammar/formatwarning.go`), which this subplan does not re-word.

---

## Test plan (TDD)

Every test below is written and run **before** the implementation it covers, inside the commit
that adds that implementation. `testify/require` only (`assert` is banned, 00-ARCHITECTURE §6.1).
Every test touching time takes `testutil.FakeClock`. Property tests use `pgregory.net/rapid`.

### `internal/grammar/sequitur_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestSequiturEmptyGrammar` | `New()` | no appends | `Rules()` empty, `Compressed()` empty, `Thrash(1)` empty |
| `TestSequiturSingleSymbol` | `New()` | `Append("a")` | `Rules()` empty, `Compressed() == ["a"]` |
| `TestSequiturNoRepetition` | `New()` | `a b c d e` | `Rules()` empty, `Compressed() == [a b c d e]` |
| `TestSequiturCreatesRuleOnRepeatedDigram` | `New()` | `a b a b` | exactly 1 rule; `Rules()[0].Body == ["a","b"]`; `Uses == 2`; `Compressed() == ["R1","R1"]` |
| `TestSequiturOverlapExceptionAAA` | `New()` | `a a a` | `Rules()` empty (the digram `aa` occurs twice but the occurrences overlap); `Compressed() == [a a a]` |
| `TestSequiturOverlapExceptionAAAA` | `New()` | `a a a a` | exactly 1 rule `R1 = a a`, `Uses == 2`, `Compressed() == ["R1","R1"]` |
| `TestSequiturHierarchy` | `New()` | `a b c a b c a b c` | a rule whose `Expansion == [a b c]` exists with `Uses == 3`; expanding `Compressed()` reproduces the input |
| `TestSequiturRuleUtilityInlines` | `New()` | `a b c d b c a b c d b c` (constructed so an intermediate rule drops to one reference) | no rule in `Rules()` has `Uses < 2`, and none has `refCount() < 2` (asserted through an internal test in the same package) |
| `TestSequiturUsesIsOccurrenceNotRefCount` | `New()` | `read edit test fail` × 11 | the rule whose `Expansion == [read edit test fail]` reports `Uses == 11`, **not** its raw reference count. Assert the reference count is strictly less than 11 as well, so the test would fail if `Uses` were ever silently reverted to `len(refs)`. This is the pin on the §6.3 decision in Implementation spec §6.3 |
| `TestSequiturThrashSelection` | `New()` | `read edit test fail` × 11 then `done` | `Thrash(3)` returns ≥ 1 rule whose `Expansion == [read edit test fail]` and `Uses == 11`; `Thrash(12)` returns empty |
| `TestSequiturThrashSpanFilter` | `New()` | `a b` × 5 then `c` | every rule `Thrash(3)` returns has `Span >= DefaultThrashMinSpan`; `WarningsFor(g, WarnOptions{MinUses:3, MinSpan:3, MaxWarnings:2})` returns empty — the only rule clearing `MinUses: 3` is `R1 = a b` with span 2, and the pair rule `R2 = R1 R1` (span 4) has `Uses == 2` and is filtered out by `MinUses` |
| `TestSequiturReset` | grammar with 3 rules | `Reset()` | `Rules()` empty, `Compressed()` empty, next `Append` starts a fresh grammar |
| `TestSequiturAppendAtRecordsTurns` | `New()` as `TurnAware` | `AppendAt("a", 5)`, `AppendAt("b", 6)`, `AppendAt("a", 7)`, `AppendAt("b", 8)` | `WarningsFor` with `MinUses:2` reports `Turns == [5, 7]` |
| `TestSequiturConcurrentAppendAndRules` | `New()`, 4 goroutines | 10 000 appends while a 5th goroutine calls `Rules()` | `-race` clean; final terminal count == 10 000 |

### `internal/grammar/invariants_property_test.go` (rapid)

| Property | Generator | Assertion |
|---|---|---|
| `PropDigramUniqueness` | alphabet of 2–6 symbols, sequence length 0–400 | across all rule bodies including the root, every digram occurs at most once, **counting non-overlapping occurrences only** (a run `aaa` contributes one `aa`) |
| `PropRuleUtility` | same | every rule in `Rules()` has `Uses >= 2` **and** `refCount() >= 2` — the exported occurrence count and the internal reference count are both checked, because only the second is the actual Sequitur invariant |
| `PropOccurrenceCountMatchesExpansion` | same | for every rule `r`, `Uses(r)` equals the number of times `Expansion(r)` occurs as a non-overlapping factor in the depth-first expansion of the root, and `len(turnsForRule(r.ID)) == Uses(r)` |
| `PropExpansionRoundTrip` | same | fully expanding `Compressed()` through `Rules()` reproduces the appended terminal sequence exactly |
| `PropAcyclic` | same | the rule reference graph is a DAG (depth-first expansion terminates with a visited-set check that never revisits a rule on its own stack) |
| `PropCompressionIsNotExpansion` | same | `len(Compressed()) <= number of appended terminals` |
| `PropDeterminism` | same, both grammars built with `NewWithClock(testutil.NewFakeClock(fixedTime))` | building the grammar twice from the same sequence yields byte-identical `MarshalBinary()` output, header included |

### `internal/grammar/codec_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestCodecRoundTrip` | grammar from `read edit test fail` × 11 | marshal → new grammar → unmarshal | `Rules()`, `Compressed()`, `Thrash(3)` all deep-equal via `go-cmp` |
| `TestCodecHeaderLayout` | same, built with `NewWithClock(testutil.NewFakeClock(time.UnixMilli(1700000000000)))` | `MarshalBinary()` | `b[0:4] == "QPKG"`; `LE16(b[4:6]) == 1`; `LE16(b[6:8]) == 0`; `LE64(b[8:16]) == 44`; `LE64(b[16:24]) == 1700000000000`; `LE32(b[24:28]) == len(Rules())+1`; `LE32(b[28:32]) == crc32.Checksum(b[32:], crc32.MakeTable(crc32.Castagnoli))` |
| `TestCodecGolden` | the **exact** 60-symbol sequence below, appended with `AppendAt(sym, turn)` where `turn` is the 0-based append index, built with `NewWithClock(testutil.NewFakeClock(time.UnixMilli(1700000000000)))` | marshal | bytes equal `testdata/golden/grammar/actions_v1.seq` |
| `TestCodecRejectsBadMagic` | golden with `b[0] = 'X'` | unmarshal | error mentioning "magic"; receiver unchanged (its `Rules()` still returns the pre-call value) |
| `TestCodecRejectsBadVersion` | golden with `ver = 99` | unmarshal | error `"grammar: unsupported format version 99"` |
| `TestCodecRejectsBadCRC` | golden with one payload byte flipped | unmarshal | error mentioning "crc"; receiver unchanged |
| `TestCodecRejectsTruncated` | golden truncated to 20 bytes, and to `len-1` | unmarshal | error in both cases, no panic |
| `TestCodecRejectsDanglingRuleRef` | hand-built payload referencing rule 99 | unmarshal | error mentioning "unknown rule 99" |
| `TestSaveLoadRoundTrip` | `testutil.NewProject` | `Save(root+"/grammar/actions.seq", g)` then `Load` into a fresh grammar | equal grammars; file exists; parent dir created |
| `TestLoadMissingFileIsNotFound` | empty temp project | `Load` a non-existent path | `errors.Is(err, core.ErrNotFound)` |
| `TestCodecSizeBudget` | 50 000 appends from a 12-symbol alphabet | marshal | `len(bytes) < 512*1024` |
| `FuzzGrammarUnmarshal` | seed corpus = the golden plus 6 mutations | arbitrary bytes | never panics; either an error or a grammar satisfying both invariants |

**The golden sequence, normative** (60 symbols; `goldenSequence` is a package-level `[]Symbol` in
`codec_test.go`, and `TestCodecGolden` asserts `len(goldenSequence) == 60` before using it):

```
 1..4    FileRead  FileEdit  Bash  test:fail
 5..8    FileRead  FileEdit  Bash  test:fail
 9..12   FileRead  FileEdit  Bash  test:fail
13..16   FileRead  FileEdit  Bash  test:fail
17..20   Grep      FileRead  Grep  FileRead
21..24   FileRead  FileEdit  Bash  test:pass
25..28   user      Glob      WebFetch  user
29..32   FileRead  FileEdit  Bash  test:fail
33..36   FileRead  FileEdit  Bash  test:fail
37..40   Grep      FileRead  Grep  FileRead
41..44   FileRead  FileEdit  Bash  test:pass
45..48   user      Glob      WebFetch  user
49..52   Grep      FileRead  Glob  WebFetch
53..56   FileRead  FileEdit  Bash  test:fail
57..60   FileRead  FileEdit  Bash  test:fail
```

Fifteen rows of four symbols each, read top to bottom and left to right, with the leading column
being the 1-based position range and not part of the data. It deliberately exercises a repeated
4-cycle (rule creation and hierarchy), a repeated 2-cycle (`Grep FileRead`), rule reuse across
non-adjacent regions, and symbols that never form a rule, so the golden covers every payload
branch of the encoder.

### `internal/grammar/warn_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestWarningsForBelowThreshold` | `read edit` × 2 | `DefaultWarnOptions()` (MinUses 3) | empty |
| `TestWarningsForAtThreshold` | `read edit` × 3 | default | exactly 1 warning, `Repeats == 3` |
| `TestWarningsForMaxWarnings` | cycle A = 4 distinct symbols × 3 (best product 12) then cycle B = 3 distinct *other* symbols × 6 (best product 18); the two alphabets are disjoint | `MaxWarnings: 1` | exactly 1 warning, and every symbol in its `Rule.Expansion` belongs to cycle B's alphabet — asserted on alphabet membership rather than on an exact rule id, because Sequitur's hierarchy makes several cycle-B rules tie at 18 and any of them is a correct answer |
| `TestWarningsForMaxWarningsTieBreak` | one 4-symbol cycle × 6, so the 4-symbol rule (product 24) and the 8-symbol pair rule (product 24) tie | `MaxWarnings: 1` | exactly 1 warning, and it is the rule with the **lower** `RuleID` — the documented `ID ASC` tiebreak; asserted identical across 20 rebuilds to prove it is not map-iteration dependent |
| `TestWarningMessageIsTheSuggestionOnly` | `read edit test fail` × 11 | `WarningsFor` | every `Warning.Message` equals `thrashSuggestion` and contains **no** `"[qompack]"` — `Message` is §5.11's "short, human-readable suggestion", the half `FormatWarning` interpolates, never the rendered line |
| `TestWarningsForPopulatesTurns` | same grammar, fed through `AppendAt` | `WarningsFor` | every `Warning.Turns` is non-empty and ascending, so SP-01's degenerate empty-`turnRange` rendering is unreachable in production |
| `TestComposedWarningGolden` | rule `FileRead FileEdit Bash test:fail`, `Uses 11`, turns `[14,19,24,29,34,39]` from `WarningsFor` | `FormatWarning(WarningsFor(g, o)[0])` | byte-equal to `testdata/golden/grammar/thrash_warning.txt` (single line, no trailing newline). This is a fixture of the composition; the formatter's own wording stays pinned by SP-01's `internal/grammar/formatwarning_test.go`, which SP-15 does not touch |
| `TestFormatWarningWordingIsSP01s` | the V1 fixture `Warning{Expansion:[Read Edit Bash], Repeats:11, Message:"consider a different approach", Turns:[42,74]}` | `FormatWarning` | exactly `"[qompack] possible loop: Read→Edit→Bash repeated 11× (turns 42–74) — consider a different approach"` — the same byte string V1-VERIFY L11 gates, restated here so a re-wording fails in SP-15's own package as well as SP-01's. The file itself is proved unmodified by the commit-2 checklist's `git diff --exit-code develop -- internal/grammar/formatwarning.go` |
| `TestPromptAddendumEmpty` | grammar with no thrash | `PromptAddendum` | `""` |
| `TestPromptAddendumJoinsWithNewline` | two thrash cycles, `MaxWarnings: 2` | `PromptAddendum` | exactly one `"\n"`, two lines each starting `"[qompack] possible loop:"` |

### `internal/grammar/bench_test.go`

- `BenchmarkSequiturAppend` — 200 000 appends, 12-symbol alphabet with a repeated 4-cycle.
  **Budget: < 20 µs/op**, and O(1) amortized is asserted by requiring ns/op at n=200 000 to be
  within 3× of ns/op at n=20 000 (the amortization claim, tested directly).
- `BenchmarkGrammarMarshal50k` — **Budget: marshal + unmarshal < 20 ms**.
- `BenchmarkWarningsFor` — 50 000-symbol grammar, cold (dirty) cache on every iteration.
  **Budget: < 5 ms.** Rationale: `PromptAddendum` runs on the `UserPromptSubmit` reply path, which
  is a `reply: true` request and therefore counts inside **B-A** (`hook_controlled`, p99 < 15 ms,
  00-ARCHITECTURE §2.4). 5 ms is declared here as an explicit sub-budget of B-A, leaving 10 ms for
  spawn, connect, framing and the observer's own work. A second benchmark
  `BenchmarkWarningsForCached` measures the clean-cache path and must be **< 50 µs**, which is the
  number that actually applies on a prompt with no intervening tool call.

### `internal/analyzer/delta_test.go`

Fixture: `testutil.NewProject`, real `store.Store`, four roots put via `PutBytes`:
`A = "func refreshToken(ctx context.Context) error { return pool.Acquire(ctx) }"`,
`B = "unrelated prose about weather and clouds"`,
`C = ""` (empty),
`D = "widening the pool timeout was ruled out because pgbouncer 1.18 ignores it in transaction mode"`.

| Test | Input | Expected |
|---|---|---|
| `TestCheapScorerMode` | `NewCheapScorer(st)` | `Mode() == ModeCheap` |
| `TestStopwordListSize` | package `stopwords` | `len(stopwords) == 47`; no entry shorter than `minTokenLen` |
| `TestCheapScorerOverlapOnly` | blocks A,B; continuation text `"refreshToken pool Acquire context"`, no symbols, `sym == nil` | `score[A] == 1.0` (all 4 distinct continuation tokens present); `score[B] == 0`; every score ∈ [0,1] |
| `TestCheapScorerSymbolTerm` | blocks A,B; continuation text `"refreshToken pool Acquire context pgbouncer transaction"` (6 distinct tokens, 4 of them in A); run twice — once with `sym == nil`, once with `Continuation.Symbols == ["refreshToken","Acquire"]` and `sym` = a stub `SymbolRefs` returning real counts | nil run: `score[A] == 4.0/6.0` (±1e-9). symbol run: `score[A] == 0.5*(4.0/6.0) + 0.5*1.0` (±1e-9), which is **strictly greater** than the nil run — this is the pairing of `weightOverlapPaired`/`weightSymbolPaired`. `score[B] == 0` in both runs |
| `TestCheapScorerRanksNegativeKnowledgeHighest` (**G6.3**) | blocks A (a reconstructible file read) and D (a ruled-out-approach rationale); continuation `"pgbouncer transaction mode timeout ruled"`; `sym == nil` | `score[D] > score[A]`. This is the operational form of §3 G6.3 — "negative knowledge is the highest-Δ content in the transcript … and it is exactly what gets dropped" — and of §9's row `G6.3 → L2 Δ-scoring prioritises it`: the elimination rationale, not the re-readable file body, wins the ranking that feeds `w(b)` |
| `TestCheapScorerEmptyContinuation` | blocks A,B; empty `Continuation` | all scores `0`, no error |
| `TestCheapScorerZeroRoot` | block with zero `core.Hash` | score `0`, no error, no store call |
| `TestCheapScorerMissingObject` | block whose Root is a random hash | score `0`, no error (`core.ErrNotFound` swallowed and Debug-logged) |
| `TestCheapScorerNilStore` | `NewCheapScorer(nil)` | all scores `0`, no panic |
| `TestCheapScorerStopwordsDropped` | continuation `"the and for that this"` only | denominator 0 → all scores `0` |
| `TestCheapScorerNumericTokensDropped` | block `"12345 67890"`, continuation `"12345"` | score `0` |
| `TestCheapScorerBlockCap` | block of 1 MiB whose distinguishing token is at byte 900 000 | score `0` (beyond `maxBlockBytes`); test documents the cap rather than fighting it |
| `TestCheapScorerDeterministic` | blocks A,B,C, 100 repeated calls | byte-identical maps via `go-cmp` every time |
| `TestCheapScorerContextCancel` | 1 000 blocks, ctx cancelled after 1 | returns `ctx.Err()` and a partial map |

### `internal/analyzer/redundancy_test.go`

Fixture: `testutil.NewProject` with a real store; helper `putRead(path, body, turn)` returning a
`store.ToolUseRecord`.

| Test | Setup | Expected |
|---|---|---|
| `TestDetectRedundancyEmpty` | empty store | zero-value report, no error |
| `TestDetectRedundancyEnumeratesWholeSession` | **150** records across 3 paths in the target session, interleaved with 150 records of a second session | the detector observes all 150 of the target session's records and none of the other session's (assert via an instrumented store wrapper counting the enumerator's returns, or by making all 150 mutually superseding). **150 is chosen to exceed `store`'s `maxK = 100`**, so this row fails against any `Search`-based enumeration and passes only against `ToolUsesBySession` — a 12-record fixture would pass either way and pin nothing |
| `TestSortedNearDupKeys` | report with 3 near-dup keys inserted in reverse order | `SortedNearDupKeys()` returns them ascending, identical across 50 runs |
| `TestDetectRedundancyObserverMarked` | one record with `Status: StatusSuperseded` | `Superseded == [that id]` |
| `TestDetectRedundancyChunkSuperset` | `src/auth.ts` read at turn 1 (100 lines), re-read at turn 5 (the same 100 lines plus 40 more) | `Superseded` contains the turn-1 id, not the turn-5 id |
| `TestDetectRedundancyIdenticalReread` | same path, byte-identical content twice | earlier id superseded |
| `TestDetectRedundancyDifferentPathsNotSuperseded` | two different paths, identical content | `Superseded` empty (the rule in §8.1 item 3 is per-path) |
| `TestDetectRedundancyNearDup` | two `go test` outputs differing in one failing test, MinHash Jaccard 0.94 | `NearDups[earlier] == [later]` |
| `TestDetectRedundancyBelowThreshold` | two outputs with Jaccard 0.60 | `NearDups` empty |
| `TestDetectRedundancyThresholdFromConfig` | Jaccard 0.85; `cfg.Store.Canonicalize.MinHash.NearDupThreshold = 0.8` | `NearDups` non-empty — proves the threshold is read from config, not hardcoded |
| `TestDetectRedundancyOtherSessionIgnored` | records from two sessions | only the requested session's ids appear |
| `TestDetectRedundancyDeterministicOrder` | 20 records, 5 runs | `Superseded` and each `NearDups` value slice byte-identical every run |
| `TestExcludeFromSummary` | report with 2 superseded + 1 near-dup key | map has exactly 3 true entries; the near-dup *value* id is absent |
| `TestApplyToSetsSuperseded` | blocks with ids `"tooluse:t1"`, `"tooluse:t2"`; report excludes `t1` | `blocks[0].Superseded == true`, `blocks[1]` unchanged, order preserved, length preserved |
| `TestToolUseIDOf` | `"tooluse:abc"`, `"file:src/a.ts"`, `"nocolon"`, `":x"`, `"x:"` | `("abc",true)`, `("src/a.ts",true)`, `("",false)`, `("",false)`, `("",false)` |
| `TestDetectRedundancyMissingRootTolerated` | record whose root object was GC'd | no error; that record is neither superseded nor a superseder |

### `internal/analyzer/selector_test.go`

**SP-01's five tests in this file stay exactly as they are** (`TestNewSelector_RefusesABlockBeforeP`,
`TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck`, `TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet`,
`TestNewSelector_PosEqualToPIsLegal`, `TestNewSelector_EmptyCandidateSetStillConsultsTheShipOrderGate`);
`TestStubSelector_SelectIsNotImplemented` is the one exception — its "Select is either the SP-01
stub or SP-15's real implementation" branch is what it was written to allow, and it keeps passing
once `Select` returns a real `Selection` with a nil error. The rows below are added alongside them.

| Test | Setup | Expected |
|---|---|---|
| `TestNewSelectorRejectsPreP` | `p = 100`; blocks at Pos 150, 99 | `errors.Is(err, ErrBlockBeforeP)` **and** `errors.Is(err, core.ErrBudget)`; error text names the block id, its pos, `p`, and contains `invariant 4` |
| `TestNewSelectorPosCheckPrecedesShipOrderCheck` | probe forced false; blocks at Pos 150 and 99 | `errors.Is(err, core.ErrBudget)` and `!core.IsNotImplemented(err)` — the shipped normative order, re-asserted against the new sentinels |
| `TestNewSelectorAcceptsPosEqualP` | `p = 100`; block at Pos 100 | no error |
| `TestNewSelectorRejectsNegativeLambda` | `lambda = -0.1` | `errors.Is(err, ErrLambdaNegative)` |
| `TestSelectorPReturnsP` | `p = 4242` | `P() == 4242` |
| `TestSelectZeroBudget` | 5 blocks, budget 0 | `Keep` empty, `Tokens == 0`, `Value == 0`, `Dropped` has all 5 in constructor order `(Pos asc, ID asc)` |
| `TestSelectRespectsBudget` | 10 blocks × 1 000 tokens, **pairwise-disjoint chunk sets and identical `w`** (so no single block can beat three via Phase B), budget 3 500 | `Tokens == 3000 <= 3500`; `len(Keep) == 3` |
| `TestSelectPrefersHighSliceAndDelta` | 3 disjoint blocks, equal cost; slice/Δ = (0.9,0.9), (0.5,0.5), (0.1,0.1); budget fits 1 | `Keep == [the first]` |
| `TestSelectPenalizesSuperseded` | 2 disjoint equal-cost blocks with equal w; one `Superseded`; λ = 0.4; budget fits 1 | the non-superseded block is kept |
| `TestSelectPenalizesEphemeral` | same shape with `Ephemeral` instead | the non-ephemeral block is kept — §8.7's "first eviction candidate" |
| `TestSelectDiminishingReturnsOnSharedChunks` | blocks X and Y sharing all chunk hashes, plus Z disjoint with slightly lower w; budget fits 2 | `Keep` contains exactly one of {X,Y} and Z — the submodularity is observable |
| `TestSelectLazyEqualsNaive` | 200 rapid-generated instances (≤ 40 blocks) | `Keep` identical between `lazy=true` and `lazy=false`; `Iters(lazy) < Iters(naive)` on ≥ 190 of them |
| `TestSelectBestSingletonWins` | 1 block worth 10.0 costing the whole budget, 6 blocks worth 0.5 each costing 1/6 budget | `Keep == [the big one]` |
| `TestSelectDeterministicTieBreak` | 4 blocks with identical w and cost | `Keep` ordered by `(Pos asc, ID asc)`, identical across 50 runs |
| `TestSelectContextCancel` | 5 000 blocks, ctx cancelled mid-loop | returns `ctx.Err()` with a partial, budget-respecting `Selection` |
| `TestSelectNilStoreFallsBackToSingletons` | store nil | every block's coverage is its own singleton; `Value == Σ wEff(kept)`; no panic |
| `TestSelectorGoldenSmallCase` | the 8-block instance in `testdata/golden/analyzer/selection_smallcase.json` | `Keep` (exact order), `Tokens` (exact) and `Value` (4 dp) match the golden; `Iters` is asserted only as `> 0 && <= 8*8` because the evaluation count is an implementation detail of the heap and must not be frozen by a golden |

### `internal/analyzer/greedy_property_test.go` (rapid)

| Property | Generator | Assertion |
|---|---|---|
| `PropGuaranteeUnitCost` | ≤ 12 blocks, all `Tokens == 1`, budget `k ∈ [1,6]`, random w ∈ [0,1], random overlapping chunk sets over a 10-element universe | brute force all `2^12` subsets of size ≤ k; assert `f(greedy) >= (1 - 1/math.E) * f(opt) - 1e-9`. This is the exact theoretical setting of Appendix A's guarantee. |
| `PropGuaranteeKnapsack` | ≤ 12 blocks, `Tokens ∈ [1,50]`, budget ∈ [10,200] | brute force all subsets fitting the budget; assert `f(greedyBestOfTwo) >= (1 - 1/math.E) * f(opt) - 1e-9` |
| `PropMonotone` | random instance | `f(S ∪ {b}) >= f(S)` for every block and every greedy prefix |
| `PropSubmodular` | random instance, random `A ⊂ B` | `f(A∪{b}) − f(A) >= f(B∪{b}) − f(B) - 1e-12` |
| `PropBudgetNeverExceeded` | random instance and budget | `Selection.Tokens <= budget` always |
| `PropNothingBeforeP` | random `p`, blocks all with `Pos >= p` | every kept id belongs to a block with `Pos >= p` (the constructor already refuses others; this asserts `Select` adds no bypass) |
| `PropCoverageMinusLambdaRedundancy` | random instance, random `λ ∈ [0, 3]`, random greedy prefix `S` | `f(S)` computed by the selector equals `coverage(S) − λ·redundancy(S)` computed independently from `W₀(u)` and `ρ(u)` read off `b*(u)`, to within 1e-12. This checks the §5.1 identity numerically instead of taking it on faith |

### `internal/analyzer/guard_test.go` — the ship-order guard (CI proof of inertness)

| Test | Setup | Expected |
|---|---|---|
| `TestNewSelectorInertWithoutPSelection` | `restore := SetPSelectionProbe(func() bool { return false }); defer restore()`, every block at `Pos >= p` | `NewSelector(0, blocks, slice, delta, 0.4, true)` returns `nil` and an error satisfying both `errors.Is(err, ErrPSelectionUnavailable)` and `core.IsNotImplemented(err)`; `NewSelectorWithStore` likewise |
| `TestNewSelectorLiveWithPSelection` | probe forced true | constructor succeeds and `Select` returns a non-empty keep-set |
| `TestPSelectionProbeDefaultsToScheduler` | no override | `pAvailable` is `scheduler.PSelectionAvailable` (compared by calling both and requiring equal results across 3 calls) |
| `TestSetPSelectionProbeRestores` | override then restore | the default behaviour returns |

### `test/e2e/analyzer_shiporder_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestPSelectionProbeIsTestOnly` | walk `internal/**` and `cmd/**` with `go/parser`, skipping `*_test.go` | zero references to `SetPSelectionProbe`; failure message names each offending file — this is the CI test 00-ARCHITECTURE §5.12 requires |
| `TestNoSelectorBypass` | `go/parser` over every non-test `.go` file under `internal/analyzer` | exactly **one** comparison of a `Block.Pos` against the constructor's `p`: a binary `<` (or `>=`) expression whose operands are a selector ending in `.Pos` and the identifier `p`, and it occurs inside `NewSelectorWithStore` in `selector.go`. A second such comparison anywhere in the package fails the test, naming file and line (invariant 4: "Do not add a bypass"). **Ordering comparisons of two blocks' `Pos` are explicitly permitted** — `bs[i].Pos < bs[j].Pos` is the `(Pos asc, ID asc)` sort §5.2 mandates, and neither operand is `p`. This is an AST check precisely because the earlier string-grep formulation (`exactly one occurrence of "Pos <"`) failed on correct code: the constructor's own sort contains that substring, so the grep would have forced an implementer to contort `sortByPosThenID` (`cmp.Compare`, a reversed `>`) purely to satisfy a text match |

### `internal/checkpoint/grammar_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestBuildActionHistoryNilGrammar` | `BuildActionHistory(nil, DefaultWarnOptions())` | zero-value `ActionHistory` |
| `TestBuildActionHistoryCounts` | grammar from `read edit test fail` × 11 + `done`, `DefaultWarnOptions()` (`MaxWarnings: 2`) | `len(Rules) >= 1`; `Compressed == len(Sequence)`; `Symbols == 45` (44 cycle terminals + `done`); `len(Thrash) == 2` — the cap, not one, because Sequitur's hierarchy yields several rules describing the same loop (see Implementation spec §6.3) and the first is the 4-symbol cycle with `Repeats == 11` |
| `TestRenderActionHistoryEmpty` | zero-value history | `""` |
| `TestRenderActionHistoryGolden` | the fixed 11× thrash grammar | byte-equal to `testdata/golden/checkpoints/action_history.txt` |
| `TestRenderActionHistoryNoCodeFences` | any history | rendered text contains no "```" and no line beginning with four spaces — 00-ARCHITECTURE §13 invariant 5 |
| `TestFoldAppendsNeverRewrites` | `Checkpoint{Narrative: "existing prose."}` | after fold, `Narrative` starts with `"existing prose."` and contains `"\n\nAction history"` |
| `TestFoldSetsSketchRef` | checkpoint with nil `SketchRefs` | `SketchRefs["grammar"] == "grammar/actions.seq"`; pre-existing keys preserved |
| `TestFoldNilGrammarIsNoOp` | `FoldActionHistoryInto(&c, nil, o)` | `c` unchanged, including a nil `SketchRefs` staying nil |
| `TestFoldDoesNotBumpVersion` | fold into a v1 checkpoint, marshal | `json.Unmarshal` shows `"version": 1` and the key set is identical to a golden v1 checkpoint's key set |
| `TestFinalizeIncludesActionHistory` | real `checkpoint.Writer` over `testutil.NewProject` with a populated `SourceSet.Grammar` | the written `0001.json`'s `narrative` contains `"Action history (grammar-compressed"` and `sketch_refs.grammar` is set |
| `TestTruncateDropsActionHistoryFirst` | fold, then `Truncate` with a budget below tier-3 cost | the action-history block is gone, tier 1 intact, a `DropEntry` recorded |

### `test/e2e/thrash_warning_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestThrashWarningReachesAdditionalContext` | real daemon built by `daemon.New` over `testutil.NewProject` with `Options.Grammar` pre-fed 11 `read edit test fail` cycles (so `New`'s own guarded `AttachThrashWarning(&o, …)` fires), contract mode `full` | the `observe.prompt` response's `hookSpecificOutput.additionalContext` contains `"[qompack] possible loop:"` and `"repeated 11×"` |
| `TestThrashWarning_NotInjectedWhenModeMayNotAct` | same daemon, contract monitor forced to `degraded-passive`, then to `off` | in **both** modes the `observe.prompt` response carries **no** `additionalContext` — the addendum inherits `handleObservePrompt`'s `mode.MayAct()` gate (`handlers.go:401-403`) because it decorates the seam. This row fails against any route-level `Options.Handle` wrapper, which is why it exists |
| `TestThrashWarning_InsidePromptDeadline` | a `grammar.Sequitur` whose `PromptAddendum` blocks 2 s | the `observe.prompt` route returns **within 1 s** with an empty `Output` — the addendum is inside `callObservePromptWithDeadline`'s 250 ms race (`handlers.go:406-435`), so unbounded grammar work cannot be charged to the B-A hot path |
| `TestThrashWarningWrapsTheDefaultSeam` | same, with **no** other `Bind` registering `Services.ObservePrompt` | the response carries the addendum alone and the daemon still ACKs — a nil inner seam is legal (`Services`' "a handler that finds a nil seam still ACKs"), and `ipc.OpObservePrompt` is still SP-05's own `d.handleObservePrompt`: assert `o.Handler(ipc.OpObservePrompt)` reports `ok == false` both before and after `AttachThrashWarning` |
| `TestThrashWarningWrapsAnEarlierBind` | an earlier `Options.Bind` setting `Services.ObservePrompt` (SP-08's shape) | that seam runs and the addendum is appended to its output — proof that `AttachThrashWarning`'s bind is appended last and reads back what earlier binds installed |
| `TestThrashWarningPreservesInnerContext` | inner handler that sets `additionalContext = "inner"` | result is `"inner\n[qompack] possible loop: …"` |
| `TestThrashWarningAbsentWhenNoThrash` | grammar with 5 distinct symbols | response is byte-identical to the undecorated seam's |
| `TestThrashWarningNilGrammarIsNoOp` | `g == nil` | `AttachThrashWarning` returns nil and appends **no** bind (`Services.ObservePrompt` is the same function value before and after `New`); the wrapper likewise returns the inner output unchanged |
| `TestThrashWarningNoRouteMutationAfterNew` | `go test -race`, 64 concurrent `observe.prompt` requests against a daemon whose grammar is being appended to | no race reported; `d.routes` is never written by this subplan and `Services` is mutated only inside `New`'s bind loop |
| `TestThrashWarningHookStillExitsZero` | the real `qompack observe prompt` binary against the warmed daemon | exit code 0 (00-ARCHITECTURE §13 invariant 6) |

### `test/replay/phase5_exit_test.go` and `phase6_exit_test.go`

Exactly the assertions enumerated in Implementation spec §9. Both run in the `replay-gate` CI job.
Fixtures: `testdata/sessions/synthetic/*.json` (SP-02, 24 sessions) and the SP-15-owned
`testdata/replay-baseline/phase5.json`.

### `internal/analyzer/bench_test.go`

- `BenchmarkCheapScorer500` — 500 blocks × 8 KiB, 64 KiB continuation. **Budget: < 250 ms.**
- `BenchmarkDetectRedundancy2000` — 2 000 records over 200 paths. **Budget: < 300 ms.**
- `BenchmarkLazyGreedy2000` — 2 000 blocks, mean 8 chunks, budget 12 000. **Budget: < 50 ms**
  (declared sub-budget of **B-E**, `checkpoint_finalize` p99 < 2 s).
- `BenchmarkLazyGreedyEvaluations` — same instance. **Budget: `lazyIters ≤ naiveIters / 5`.**

### Fixtures created by this subplan

| Path | Contents |
|---|---|
| `testdata/golden/grammar/actions_v1.seq` | the byte-exact v1 serialization of a fixed 60-symbol grammar |
| `testdata/golden/grammar/thrash_warning.txt` | the single line `FormatWarning` produces from a `WarningsFor` output — SP-01's `[qompack] possible loop:` wording with SP-15's `thrashSuggestion` as the message — no trailing newline |
| `testdata/golden/checkpoints/action_history.txt` | the rendered narrative block for the 11× thrash grammar |
| `testdata/golden/analyzer/selection_smallcase.json` | an 8-block instance (blocks, slice scores, Δ scores, λ, budget) plus expected `Keep` (exact order), `Tokens` (exact) and `Value` (4 dp). **`Iters` is deliberately not in the golden** — it is a property of the heap implementation and freezing it would turn a legal optimization into a test failure; `TestSelectorGoldenSmallCase` bounds it instead |
| `testdata/replay-baseline/phase5.json` | the recorded fraction-of-OPT baseline and analyzer numbers |
| `testdata/corpora/grammar/` | fuzz seed corpus for `FuzzGrammarUnmarshal` (golden + 6 mutations) |

---

## Commit plan

Work happens on `feat/sp15-analyzer-selection-and-grammar`, cut from `develop` **after** V4
verification is green and the branches of SP-01, SP-06, SP-07, SP-08, SP-10 and SP-12 are merged.

```
git fetch origin
git switch develop && git pull --ff-only
git switch -c feat/sp15-analyzer-selection-and-grammar
git worktree add ../qompack-sp15 feat/sp15-analyzer-selection-and-grammar   # optional, encouraged
```

Seven commits. Each compiles and passes `go run ./tools/devtool test` for the packages it touches
before it is made.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(grammar): sequitur core with digram-uniqueness and rule-utility invariants`

Body: *Online, linear-time grammar induction over the action log. The two invariants are
maintained incrementally rather than checked after the fact, which is what makes the update O(1)
amortized and therefore admissible inside the L0 ingest budget.*
Footer: `Refs: SP-15, §6.3, §8.1 item 6, §10 Phase 6`

- [ ] Write `internal/grammar/sequitur_test.go` (14 tests) and
      `internal/grammar/invariants_property_test.go` (7 properties) **first**; run
      `go test ./internal/grammar/...` and confirm every one **fails** against the SP-01 stub.
- [ ] Add `internal/grammar/doc.go`, `sequitur.go` (structures, `AppendAt`, `Append`, `check`,
      `match`, `substitute`, `expand`, `deleteDigram`, `link`, `unlink`), `rules.go`
      (`Rules`, `Compressed`, `Thrash`, `Reset`, `turnsForRule`, expansion memoization).
- [ ] Remove the `t.Skip` lines from the `grammartest` conformance suite for these methods.
- [ ] Run: `go test -race ./internal/grammar/...` — all green.
- [ ] Run: `go run ./tools/devtool fmt lint vet`.

### Commit 2 — `feat(grammar): versioned actions.seq codec, thrash detection and warnings`

Body: *On-disk stability across plugin versions is a hard requirement of the store, so the grammar
carries its own magic, version and CRC32C rather than borrowing the sketch header — `grammar` may
not import `sketch` under the §3.2 import DAG.*
Footer: `Refs: SP-15, §6.3, §7.4, §10 Phase 6`

- [ ] Write `internal/grammar/codec_test.go` (12 tests + `FuzzGrammarUnmarshal`) and
      `warn_test.go` (10 tests) **first**; confirm they fail.
- [ ] Add `internal/grammar/codec.go` and `warn.go`, including the `thrashSuggestion` constant and
      the `dirty`-flag projection cache that makes the `PromptAddendum` B-A sub-budget hold.
      **Do not touch `formatwarning.go`.**
- [ ] Generate `testdata/golden/grammar/actions_v1.seq` from the normative 60-symbol sequence,
      `testdata/golden/grammar/thrash_warning.txt` (the composed line, in SP-01's `possible loop`
      wording), and the fuzz seed corpus under `testdata/corpora/grammar/`.
- [ ] Run `git diff --exit-code develop -- internal/grammar/formatwarning.go internal/grammar/formatwarning_test.go`
      — must be empty. SP-01 froze that wording and V1-VERIFY L11 gates it.
- [ ] Add `internal/grammar/bench_test.go` and confirm the four budgets
      (`< 20 µs/op`, `< 20 ms`, `< 5 ms` cold, `< 50 µs` cached).
- [ ] Run: `go test -race ./internal/grammar/... && go test -run Fuzz -fuzz FuzzGrammarUnmarshal -fuzztime 60s ./internal/grammar/`.
- [ ] Run: `go run ./tools/devtool cover` and confirm `grammar` ≥ 90 % (floor is 75 %).

### Commit 3 — `feat(analyzer): cheap retrospective delta scorer over the observed continuation`

Body: *§8.3 says start cheap and let the replay harness decide. Token overlap plus
symbol-reference counting needs no model and no network; the medium and expensive variants are
reachable through one new constructor because every consumer takes the interface.*
Footer: `Refs: SP-15, G6.3, §4.3, §8.3, §10 Phase 5`

- [ ] Write `internal/analyzer/delta_test.go` (14 tests, including the G6.3 ranking test) **first**;
      confirm they fail.
- [ ] Add `internal/analyzer/doc.go`, `block.go` (types, sentinels, `pAvailable`,
      `SetPSelectionProbe`, `ToolUseIDOf`), `delta.go` (`SymbolRefs`, `cheapScorer`,
      `tokenize`, `stopwords`, the upgrade-path comment block).
- [ ] Add `BenchmarkCheapScorer500` and confirm **< 250 ms**.
- [ ] Remove the corresponding `t.Skip` lines in `analyzertest`.
- [ ] Run: `go test -race ./internal/analyzer/...` and `go run ./tools/devtool lint` — the
      `nomagic` pass must report zero findings (no `0.9`, `0.4`, `450`, `12000` literals).

### Commit 4 — `feat(analyzer): redundancy detection across superseded reads and near-duplicates`

Body: *Two consumers, one detector: the eviction ranking and the rule that a superseded read may
never appear in a summary. The near-duplicate threshold is read from config so the value can move
without a code change.*
Footer: `Refs: SP-15, §4.3, §8.1 item 3, §8.3`

- [ ] Write `internal/analyzer/redundancy_test.go` (16 tests) **first**; confirm they fail.
- [ ] Confirm the `arch/store-tooluses-by-session` pre-step (Implementation spec §4) is already
      merged into `develop` — `store.Store` must declare `ToolUsesBySession`. It is a prerequisite
      of this branch, not a contingency: `feat/sp15-*` is cut after it lands.
- [ ] Run `TestDetectRedundancyEnumeratesWholeSession` against the real SP-06 store **before**
      writing the rest of the file; its 150-record fixture is what proves the enumerator is
      session-scoped and unclamped.
- [ ] Add `internal/analyzer/redundancy.go` (`RedundancyOptions`, `DetectRedundancy`,
      `DetectRedundancyWithConfig`, chunk-superset detection, MinHash grouping,
      `ExcludeFromSummary`, `ApplyTo`, `SortedNearDupKeys`).
- [ ] Add `BenchmarkDetectRedundancy2000` and confirm **< 300 ms**.
- [ ] Run: `go test -race ./internal/analyzer/...`.

### Commit 5 — `feat(analyzer): suffix-constrained submodular lazy greedy with the p guard`

Body: *The selector is constructed with p and refuses any block before it, so a scattered pre-p
keep-set is a compile-and-construct-time impossibility rather than a caller discipline. It also
refuses to construct at all unless p-selection is available, which is closing note 3 enforced in
code.*
Footer: `Refs: SP-15, §5.2, §5.3, §6.5, §8.3, Closing note 3, Appendix A`

- [ ] Extend the shipped `internal/analyzer/selector_test.go` with the 17 rows tabled above —
      **keeping SP-01's five constructor tests exactly as they are** — and write
      `greedy_property_test.go` (7 properties) and `guard_test.go` (4 tests) **first**; confirm the
      new ones fail and the five old ones pass.
- [ ] Replace `internal/analyzer/selector.go`'s stub body (weights, coverage universe,
      `NewSelector`, `NewSelectorWithStore`, `P`) and add `greedy.go` (the lazy heap, `marginal`,
      Phase A, Phase B). **Both guards survive verbatim**: the `Pos < p` check first, wrapped as
      `ErrBlockBeforeP` over `core.ErrBudget` with `invariant 4` still in the message; then the
      `pAvailable()` check, wrapped as `ErrPSelectionUnavailable` over `core.ErrNotImplemented`.
- [ ] Run `go test ./internal/analyzer/ -run TestNewSelector_ && go test ./test/guards/ -run TestGuard_S`
      **before** touching anything else in the file — the five shipped constructor tests and the two
      shipped build-order guards must still pass, unmodified, after the replacement.
- [ ] Flip the submodular default: `internal/config/defaults.go` (`SubmodularEnabled: true` and the
      derived `Enabled: true`), `internal/config/defaults_test.go` (two assertions),
      `test/guards/buildorder_test.go` (`TestGuard_SubmodularDefaultsOff` →
      `TestGuard_SubmodularEnabledOnlyAfterPSelection`), then
      `go run ./tools/devtool gen-config-docs` and commit the regenerated
      `docs/config-reference.md`. Confirm with `go run ./tools/devtool gen-config-docs --check`.
- [ ] Add `testdata/golden/analyzer/selection_smallcase.json`.
- [ ] Add `test/e2e/analyzer_shiporder_test.go` (2 tests).
- [ ] Add `BenchmarkLazyGreedy2000` (**< 50 ms**, B-E sub-budget) and
      `BenchmarkLazyGreedyEvaluations` (**≤ naive/5**).
- [ ] Remove every remaining `t.Skip` in `analyzertest`.
- [ ] Run: `go test -race ./internal/analyzer/... ./test/e2e/...` and
      `go run ./tools/devtool cover` — `analyzer` ≥ 90 % (floor is 85 %).

### Commit 6 — `feat(checkpoint): fold grammar-compressed action history at compaction time`

Body: *§5.5 classifies Sequitur as cache-Safe precisely because the grammar is folded into the
summary at compaction time and nothing is rewritten in place. It lands in the tier-3 narrative so
importance-ordered truncation drops it first, and in a new file so it does not collide with the
other wave-4 checkpoint work.*
Footer: `Refs: SP-15, §5.5, §6.3, §6.9, §8.5, §10 Phase 6`

- [ ] Write `internal/checkpoint/grammar_test.go` (11 tests) and
      `test/e2e/thrash_warning_test.go` (10 tests) **first**; confirm they fail.
- [ ] Add `internal/checkpoint/grammar.go` (`ActionRule`, `ThrashNote`, `ActionHistory`,
      `BuildActionHistory`, `RenderActionHistory`, `FoldActionHistoryInto`).
- [ ] Modify `internal/checkpoint/writer.go`: insert the three-line fold immediately before the
      `Truncate` call in `Finalize`. No other change to that file.
- [ ] Add `internal/daemon/grammar_addendum.go` (`WrapObservePromptWithThrashWarning`,
      `AttachThrashWarning(o *Options, …)`). No route registration, no accessor, no mutex, no
      change to the `Daemon` interface — and no `defaultObservePromptHandler`: `Bind` composes onto
      the seam `handleObservePrompt` already calls, so nothing here reaches into `(*daemon)`.
- [ ] Modify `internal/daemon/daemon.go`: insert the four-line guarded `AttachThrashWarning(&o, …)`
      call **immediately before** the `for _, bind := range o.binds { bind(svc) }` loop in `New`.
      No other change to that file, and `d.routes` is neither read nor written.
- [ ] Add `testdata/golden/checkpoints/action_history.txt`.
- [ ] Add `BenchmarkFoldActionHistory` and confirm **< 20 ms**.
- [ ] Run: `go test -race ./internal/checkpoint/... ./internal/daemon/... ./test/e2e/...` and
      `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon`
      — B-A p99 must remain **< 15 ms** and B-E p99 **< 2 s**. The prompt path's exit-0 behaviour
      is covered by `TestThrashWarningHookStillExitsZero` rather than by the hot-path harness,
      because `UserPromptSubmit` is a warm-path hook that returns data.

### Commit 7 — `test(replay): phase 5 and phase 6 exit criteria on the synthetic corpus`

Body: *Phases 5 and 6 close on numbers, not on code review. Fraction-of-OPT at equal budget and
thrash-detected-before-user-visible are both asserted against the committed 24-session corpus and
the observed numbers are committed so a regression shows up in the diff.*
Footer: `Refs: SP-15, §10 Phase 5, §10 Phase 6, §11.1, §11.3`

- [ ] Write `test/replay/phase5_exit_test.go` and `test/replay/phase6_exit_test.go` **first**;
      confirm they fail (no policy exists yet).
- [ ] Add `test/replay/policy_analyzer.go` (`PolicyDeps`, `NewSuffixSubmodularPolicy`,
      `criteriaFor`, `continuationOf`).
- [ ] Add `test/replay/policy_baseline_p.go` (`NewPSelectionBaselinePolicy`) **unless** SP-12
      already shipped a p-selection-only `eval.Policy` on `develop`, in which case use that and
      skip this file. Record which branch was taken in the commit body.
- [ ] Add `testdata/replay-baseline/phase5.json` with the observed numbers.
- [ ] Add `docs/adr/0015-suffix-constrained-submodular-selection.md` recording: why the objective
      is a weighted coverage function rather than Lin & Bilmes' non-monotone form (the `(1 − 1/e)`
      guarantee), why `W₀(u)` and `ρ(u)` are both read off the same `b*(u)` so the
      `coverage − λ·redundancy` identity is exact, why `NewSelector` errors rather than filters,
      why `symbols` is consumed structurally, why `grammar.Rule.Uses` is the occurrence
      multiplicity rather than the reference count, why the ship-order guard stays
      `scheduler.PSelectionAvailable()` in the constructor while the operator opt-out
      (`runtime.selection.submodularEnabled`, default flipped to `true` by this subplan) is read at
      the call site rather than added to the §5.12 signature, why the selector ships
      measured-but-unwired in wave 4 with pointer ordering deferred to SP-16 and the production
      8–12K allocator left unowned (see `plans/TRACEABILITY.md`), and why the grammar fold lives
      in tier 3.
- [ ] Run: `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop`
      — the replay-gate must pass with no metric regressing more than 2 %.
- [ ] Run the full local gate: `go run ./tools/devtool ci-local`.
- [ ] Push and confirm all nine CI jobs green:
      `verify test cover crossbuild bench-gate replay-gate plugin-validate security docs`.

---

## Subagent strategy

This subplan is **heavy**. Partition it across five parallel subagents, then integrate in the main
session. The commit plan stays strictly sequential — subagents produce working trees, the main
session sequences them into the seven commits above.

**Contract for every subagent.** Each is given: this file; the "Interface contract" section
verbatim; the exact file list it owns; and the instruction that it may not create, modify, or
delete any file outside that list. Each returns: the file contents, the `go test` output for its
own packages, and a one-paragraph note of any assumption it had to make about a prerequisite
package's behaviour.

| Agent | Owns (exclusive) | Depends on | Returns |
|---|---|---|---|
| **A — Sequitur core** | `internal/grammar/doc.go`, `sequitur.go`, `rules.go`, `sequitur_test.go`, `invariants_property_test.go` | nothing outside `core` | the package plus a rapid seed that reproduces any invariant failure |
| **B — Grammar codec & warnings** | `internal/grammar/codec.go`, `warn.go`, `codec_test.go`, `warn_test.go`, `bench_test.go`, `testdata/golden/grammar/**`, `testdata/corpora/grammar/**` | Agent A's `rules.go` signatures only (given as a stub file) | the codec, the goldens, benchmark numbers against the three budgets |
| **C — Δ-scorer & redundancy** | `internal/analyzer/doc.go`, `block.go`, `delta.go`, `redundancy.go`, `delta_test.go`, `redundancy_test.go` | `store`, `dag`, `sketch`, `config` (all on `develop`) | the two files plus measured `BenchmarkCheapScorer500` / `BenchmarkDetectRedundancy2000` numbers |
| **D — Submodular selector** | `internal/analyzer/selector.go`, `greedy.go`, `selector_test.go`, `greedy_property_test.go`, `guard_test.go`, `testdata/golden/analyzer/selection_smallcase.json` | Agent C's `block.go` (given as a stub file with the exact types) | the selector, the property-test results including the observed worst-case greedy/OPT ratio, and the lazy-vs-naive evaluation counts |
| **E — Replay policy & Phase 5/6 assertions** | `test/replay/policy_analyzer.go`, `test/replay/policy_baseline_p.go`, `phase5_exit_test.go`, `phase6_exit_test.go`, `testdata/replay-baseline/phase5.json` | the public signatures of C and D (given verbatim from this file's Interface contract; **not** their implementations) | the policy, the measured fraction-of-OPT deltas per session shape, and the Phase 6 detection turns per thrash session |

**Parallelism.** A, C and E start immediately. B starts as soon as A's `rules.go` signatures are
fixed (minutes, not hours — hand B the signature block from this document rather than waiting).
D starts as soon as C's `block.go` is fixed, on the same terms. E writes its tests against the
published signatures and only runs them after C and D integrate.

**Must stay in the main session — never delegated:**

1. **The six cross-package edits.** The 3 lines in `internal/checkpoint/writer.go`, the 4 lines in
   `internal/daemon/daemon.go`'s `New`, the submodular default flip in
   `internal/config/defaults.go`, and its three follow-ons (`internal/config/defaults_test.go`,
   `test/guards/buildorder_test.go`, the regenerated `docs/config-reference.md`). These touch files
   owned by SP-10, SP-05 and SP-01; each must be made by one agent that has read the surrounding
   function or test, and together they are the only merge-conflict surface this subplan has.
2. **`internal/checkpoint/grammar.go` and `internal/daemon/grammar_addendum.go`.** Both live in
   packages another subplan owns and both need the surrounding code read before writing.
3. **Every `git commit`.** The seven-commit sequence, the conventional-commit messages, and the
   verification that no commit carries an attribution trailer.
4. **Removing `t.Skip` lines from the `analyzertest` and `grammartest` conformance suites.** These
   are SP-01's files; leaving one behind is a merge blocker (Rule W-1), so one agent owns the sweep
   and greps for `t.Skip` across both suites before the final push.
5. **Reconciling the benchmark numbers against the declared budgets** and deciding whether a
   miss is a real regression or a noisy runner. That judgement is not delegable.
6. **The `nomagic` sweep.** After integration, run `go run ./tools/devtool lint` and personally
   confirm that `0.9`, `0.4`, `12000`, `10000` and `450` appear nowhere in this subplan's non-test
   files. Subagents routinely inline config defaults; assume they did.

**Integration order in the main session.** A → B (grammar package complete, commits 1–2) →
C → D (analyzer package complete, commits 3–5) → main-session cross-package edits (commit 6) →
E (commit 7). Run `go build ./... && go vet ./...` after each merge-in, before writing the commit.

---

## Exit criteria

### Quoted verbatim from Qompack.md

**§10 Phase 5:**

> **Exit criterion:** improved fraction-of-OPT at equal budget.

**§10 Phase 6:**

> **Exit criterion:** thrash detected before the user notices it, on replay.

**§11.3 (the guardrails every phase gate runs under):**

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Appendix A (the guarantee this subplan property-tests):**

> ```
> f(S_greedy) ≥ (1 − 1/e)·f(S_opt) ≈ 0.63
> ```

### Measurable Definition of Done

**Scope note, binding on every criterion below.** Phase 5's exit number is a **replay-harness
result**, not a live-path measurement. The selector's only callers in this subplan are
`test/replay/policy_analyzer.go` and `policy_baseline_p.go`; nothing in `internal/` constructs a
`Selector`. "Improved fraction-of-OPT at equal budget" is therefore satisfied by the two policies
scoring differently on the committed 24-session corpus, and by nothing else. The production
consumer splits: `checkpoint.Truncate`'s pointer ordering is owned by SP-16, and the rehydrator's
8–12K allocation (`Qompack.md:590`) is **unowned** and carried in `plans/TRACEABILITY.md`'s
unowned-obligations section, as stated in the Mission. A reviewer must not read the criteria
below as claiming the shipped plugin allocates its post-compact budget submodularly; it does
not yet, and that is the declared shape of this wave.

- [ ] **Phase 5, operationalized.** `test/replay/phase5_exit_test.go` passes:
      `FractionOfOPT(analyzer-suffix-submodular) > FractionOfOPT(baseline)` across all 24
      synthetic sessions at an identical 12 000-token budget, with a delta ≥ 0.02 on the
      read-heavy and refactor subsets, and the numbers committed in
      `testdata/replay-baseline/phase5.json` matching to 4 decimal places.
- [ ] **Phase 6, operationalized.** `test/replay/phase6_exit_test.go` passes: on every
      `thrash-loop` session (at least one must exist) the first warning fires at or before the
      third repetition **and** at least 5 turns before the loop ends; on every session whose shape
      is not `thrash-loop` the warning set is empty at end of session.
- [ ] **The `(1 − 1/e)` guarantee** holds on every rapid-generated instance in both
      `PropGuaranteeUnitCost` and `PropGuaranteeKnapsack`, run at
      `go test -run Prop -rapid.checks=1000 ./internal/analyzer/` (1 000 brute-forced cases each;
      rapid's default is 100, so the flag is not optional).
- [ ] **The suffix constraint is structural, and the shipped guards are intact.** `NewSelector`
      returns `ErrBlockBeforeP` — which wraps `core.ErrBudget` and still contains "invariant 4" —
      for any block with `Pos < p`, and the Pos check still runs before the ship-order check.
      `go test ./test/guards/ -run TestGuard_SubmodularInertWithoutPSelection` and
      `go test ./internal/analyzer/ -run TestNewSelector_` both pass unmodified from `develop`;
      `TestNoSelectorBypass` finds exactly one `Block.Pos`-vs-`p` comparison; `PropNothingBeforeP`
      holds.
- [ ] **The ship-order guard is proven inert.** `TestNewSelectorInertWithoutPSelection` passes,
      `ErrPSelectionUnavailable` still satisfies `core.IsNotImplemented`, and
      `TestPSelectionProbeIsTestOnly` finds zero non-test references to `SetPSelectionProbe`.
- [ ] **The submodular default is on and the derivation still holds.** `config.Defaults()` reports
      `Runtime.Selection.SubmodularEnabled == true` and `Selection.Submodular.Enabled == true`;
      `go test ./internal/config/ ./test/guards/` passes with the three updated assertions; and
      `go run ./tools/devtool gen-config-docs --check` reports `docs/config-reference.md` up to
      date (its `runtime.selection.submodularEnabled` row now reads `true`).
- [ ] **FormatWarning is untouched.** `git diff --exit-code develop -- internal/grammar/formatwarning.go internal/grammar/formatwarning_test.go` is empty, and
      `go test ./internal/grammar/ -run TestFormatWarning` passes — SP-01's frozen wording and the
      V1-VERIFY L11 shape are exactly as they were.
- [ ] **Sequitur's two invariants** hold on 1 000 rapid-generated sequences
      (`go test -run Prop -rapid.checks=1000 ./internal/grammar/`: `PropDigramUniqueness`,
      `PropRuleUtility`), and `PropExpansionRoundTrip` reproduces every input exactly.
- [ ] **G6.3 is closed by something specific, not by assertion.**
      `TestCheapScorerRanksNegativeKnowledgeHighest` shows the Δ proxy ranking an elimination
      rationale above a re-readable file body, which is §9's row
      `G6.3 highest-Δ content dropped → L2 Δ-scoring prioritises it` made testable.
- [ ] **Every latency and size budget met**, measured not assumed (00-ARCHITECTURE §13
      invariant 9): `BenchmarkSequiturAppend` < 20 µs/op; `BenchmarkGrammarMarshal50k` < 20 ms and
      < 512 KiB; `BenchmarkWarningsFor` < 5 ms cold and `BenchmarkWarningsForCached` < 50 µs;
      `BenchmarkCheapScorer500` < 250 ms;
      `BenchmarkDetectRedundancy2000` < 300 ms; `BenchmarkLazyGreedy2000` < 50 ms;
      `BenchmarkLazyGreedyEvaluations` ≤ naive/5; `BenchmarkFoldActionHistory` < 20 ms.
- [ ] **No hot-path regression.** `devtool bench-hotpath --iterations 2000` reports B-A p99
      < 15 ms and B-E p99 < 2 s on ubuntu, macos and windows.
- [ ] **No checkpoint schema change.** `TestFoldDoesNotBumpVersion` passes; `"version": 1` and the
      v1 key set are unchanged; `TestRenderActionHistoryNoCodeFences` passes (invariant 5).
- [ ] **All tests green:** `go test -race ./...` on ubuntu and macos, `go test -count=2 ./...` on
      windows.
- [ ] **Lint and typecheck clean:** `gofumpt -l` empty; `golangci-lint run` clean; `go vet` clean;
      the in-repo `nomagic` pass reports zero findings; the import-graph check confirms
      `analyzer` imports only `store dag sketch scheduler` plus foundation, and `grammar` only
      foundation.
- [ ] **Coverage floors cleared:** `analyzer` ≥ 85 % (target 90 %), `grammar` ≥ 75 %
      (target 90 %), `checkpoint` ≥ 90 % maintained.
- [ ] **Conformance suites live:** zero `t.Skip` remaining in `analyzertest` and `grammartest`
      (Rule W-1 merge blocker).
- [ ] **CI green on the branch:** all nine jobs — `verify`, `test`, `cover`, `crossbuild`,
      `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] **Commit hygiene:** exactly 7 commits, every message conventional with a `Refs:` footer, and
      `git log --format=%B develop..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'`
      returns nothing.

---

## Done checklist

- [ ] Branch `feat/sp15-analyzer-selection-and-grammar` was cut from a `develop` on which SP-01,
      SP-06, SP-07, SP-08, SP-10 and SP-12 are all merged and V4 verification is green.
- [ ] **Spec coverage self-review against the "Design context" section**, one line at a time:
  - [ ] §6.3 Sequitur — online, linear-time, both invariants, high-multiplicity nonterminal as the
        insight → `sequitur.go`, `rules.go`, `warn.go`, `invariants_property_test.go`.
  - [ ] §6.5 submodular — knapsack, `(1 − 1/e)`, lazy greedy, slice + Δ as coverage weights →
        `selector.go`, `greedy.go`, `greedy_property_test.go`. §6.5's *"this should allocate the
        post-compact budget instead of 'top 5 files, 5K each'"* is delivered as a measured policy
        only. `checkpoint.Truncate`'s pointer ordering goes to SP-16; the production 8–12K
        allocator is **unowned** and tracked in `plans/TRACEABILITY.md`'s unowned-obligations
        section, as the Mission and the Exit criteria's scope note both state.
  - [ ] §7.2 L2 row — slicing (consumed from SP-07), Δ-scoring, submodular, Sequitur, redundancy
        detection: all five present or explicitly delegated in "Out of scope".
  - [ ] §8.1 item 6 — Sequitur append and thrash warning emission → `AppendAt` + the daemon
        addendum, with `internal/observer` untouched.
  - [ ] §8.1 item 3 — superseded reads never appear in a summary → `ExcludeFromSummary`.
  - [ ] §8.3 Δ-scoring — cheap = token overlap + symbol-reference counting; the medium and
        expensive tiers documented as constructor-only extensions.
  - [ ] **G6.3** (the one gap id this subplan owns) — "negative knowledge is the highest-Δ content
        in the transcript … and it is exactly what gets dropped" is closed by the Δ ranking, and
        the closure is pinned by `TestCheapScorerRanksNegativeKnowledgeHighest`, not merely
        claimed in a footer.
  - [ ] §8.3 submodular — `coverage(S) − λ·redundancy(S)`, token knapsack, lazy greedy,
        "enforced in the selector's constructor, not left to the caller".
  - [ ] §8.7 — retrieval results are the FIRST eviction candidate → `ρ(b) = 1` for `Ephemeral`.
  - [ ] §5.2/§5.3 — the earliest-edit-position argument and the two-stage flip are quoted in
        `doc.go` and enforced by `ErrBlockBeforeP`.
  - [ ] §5.5 — Sequitur "folded into the summary at compaction time, not rewritten in place" →
        `FoldActionHistoryInto` appends, never rewrites.
  - [ ] §10 Phase 5 and Phase 6 exit criteria → two replay tests with committed numbers.
  - [ ] Closing note 3 → `ErrPSelectionUnavailable` plus two CI inertness tests.
  - [ ] Appendix A `(1 − 1/e)` → two property tests, one in the exact theoretical setting.
  - [ ] Appendix C `selection.*` and `canonicalize.minhash.nearDupThreshold` read from config,
        never inlined.
- [ ] **Placeholder scan:** `grep -rniE 'TBD|TODO|FIXME|XXX|implement appropriately|add error handling|similar to|handle edge cases'`
      over every file this subplan added or modified returns nothing.
- [ ] **Type consistency with the Interface contract:** every signature in
      `internal/analyzer` and `internal/grammar` matches 00-ARCHITECTURE §5.11 and §5.12
      character for character; no §5 *interface* was changed or removed. Two shipped
      implementations inside packages SP-15 owns are deliberately replaced, both declared here:
      `NewSelector`'s stub body (its two guards preserved verbatim in order, sentinel and message —
      see §5.2) and `stubSelector.Select`. `internal/grammar/formatwarning.go` is **not** among
      them: SP-01's wording is frozen and untouched. Every other addition is a new symbol in a
      package SP-15 owns (`NewCheapScorerWithSymbols`, `NewSelectorWithStore`,
      `DetectRedundancyWithConfig`, `SortedNearDupKeys`, `SetPSelectionProbe`, `ToolUseIDOf`,
      `SymbolRefs`, `TurnAware`, `WarnOptions`, `WarningsFor`, `PromptAddendum`, `Save`, `Load`,
      `DefaultThrashMinUses`, `DefaultThrashMinSpan`, `FormatVersion`, and in `test/replay`
      `NewSuffixSubmodularPolicy` / `NewPSelectionBaselinePolicy`) — no amendment to
      00-ARCHITECTURE was required **on this branch** and none was made: §5.8's
      `ToolUsesBySession` line lands on the `arch/store-tooluses-by-session` pre-step, merged
      into `develop` before `feat/sp15-*` is cut. In particular `Rule.Uses` and
      `Selection.Keep`/`Dropped` keep their §5.11/§5.12 *types*; only their documented *semantics*
      are pinned down here, which needs no amendment.
- [ ] **Import DAG respected:** `internal/symbols` is not imported by `internal/analyzer`;
      `internal/sketch` is not imported by `internal/grammar`; the import-graph test passes.
- [ ] **Ownership respected:** zero lines written in `internal/observer`; exactly six files
      modified outside packages SP-15 owns, each edit contiguous, guarded and declared above:
      `internal/checkpoint/writer.go` (the three-line fold before `Truncate`),
      `internal/daemon/daemon.go` (the four-line guarded `AttachThrashWarning(&o, …)` block
      immediately before the `for _, bind := range o.binds` loop), `internal/config/defaults.go` (the
      submodular default flip, two fields), `internal/config/defaults_test.go` (two assertions),
      `test/guards/buildorder_test.go` (`TestGuard_SubmodularDefaultsOff` re-pointed at the
      derivation) and `docs/config-reference.md` (regenerated, never hand-edited).
      `internal/grammar/formatwarning.go` and `formatwarning_test.go` are explicitly **not** in
      that list.
- [ ] **Commit count verified: 7**, within the 5–8 range. `git rev-list --count develop..HEAD`
      returns `7`.
- [ ] **No co-author or attribution trailers** in any commit message, merge commit, tag message or
      PR body. Verified by the grep in the exit criteria and by CI's `verify` job.
- [ ] `Qompack.md` is untouched — `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] ADR `docs/adr/0015-suffix-constrained-submodular-selection.md` committed and referenced from
      the commit-7 body.

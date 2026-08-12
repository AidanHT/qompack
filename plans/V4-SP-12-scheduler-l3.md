# SP-12: L3 scheduler: composite trigger, p-selection with the cache term, BOCD changepoints, Young-Daly cadence, sliding-TTL idle model, idle background work, and frontier advancement

**Branch:** `feat/sp12-scheduler-l3` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of SP-01, SP-05, SP-06, SP-07, SP-08 already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-10 checkpointer, SP-11 rehydrator, SP-13 MCP retrieval) | **Design sections:** §5.3, §5.4, §6.6, §6.7, §7.2 L3, §8.4, §8.5 (O5 amortization), §10 Phase 4 | **Gaps closed:** G1.1, G1.2, G1.3, G1.4, G5.1, G5.2, G7.1, G7.6, G8.2

---

## Mission

This slice is layer **L3 — the scheduler**. It answers the two questions `Qompack.md` §8.4 names: **when** to compact and **where** to cut. Today Claude Code answers the first with a single token threshold (`effectiveWindow − 13_000`) and never asks the second at all. That is G1.1 (task-blind trigger), G1.3 (fixed buffer), G1.4 (blocking-limit cliff), G5.2 (the cache-cheap direction is the useless one), and G8.2 (the summarizer runs at its most degraded moment). SP-12 replaces the single constant with a composite trigger over a Bayesian changepoint posterior, a Young–Daly cadence measured at runtime, and a sliding-TTL idle model; and it replaces "no cut choice at all" with an explicit argmax over `reclaimable(p)·r − rewrite(p) − λ·segment_coupling(p)`.

The slice exists in this wave and not earlier because it needs `dag.CrossingEdges` (SP-07, wave 1) for the coupling term and the `store` tool-use index plus `SegmentLog` (SP-06, wave 1) for reclaimable-token accounting and segment lifecycle. It exists in this wave and not later because of `Qompack.md`'s closing note 3: *"Do not ship slicing or submodular selection before p-selection."* SP-15's `analyzer.NewSelector` is gated behind `scheduler.PSelectionAvailable()`, and that gate stays false until this subplan lands. SP-12 is therefore the unlock for wave 4's selection work, and the `PSelectionAvailable()` flip is a first-class deliverable, not a footnote.

**What exists in the repo when you start.** `internal/scheduler` exists as SP-01 compiling stubs: every type in 00-ARCHITECTURE §5.13 is declared, `Evaluate` returns a zero `Decision`, `NewBOCD` returns a detector whose methods return `core.ErrNotImplemented`, and `internal/scheduler/schedulertest` holds a conformance suite whose behaviour cases are `t.Skip`ped (Rule W-1). `internal/config` loads and validates the full Appendix C schema plus the §11.5 `runtime` namespace, with `youngDaly.measuredDeltaSeconds` already modelled as `*float64` so JSON `null` means measure-not-zero. `internal/daemon` (SP-05) is a working resident process with `IdleController.Register` (and its normative `act.`-prefix rule), an op-routing table, the `Options.Bind(func(*Services))` late-binding seam with its nil-tolerant function set, `daemon.Options.Sched scheduler.Runtime` already declared and tolerated as `nil`, and the B-A/B-B/B-C latency budgets instrumented. Note that SP-05 calls the `Services` seams, never `Sched` — supplying the path from L0 to L3 is SP-12's job, and it is done by decorating those seams through `Bind`. `internal/store` (SP-06) serves objects, the tool-use index, file version history and the `SegmentLog` with `MarkEncoded`. `internal/dag` (SP-07) answers `CrossingEdges(pos)`, `NodesAfter(pos)` and `BackwardSlice`. `internal/observer` (SP-08) emits `observer.Signals` and opens the session's first segment. `internal/checkpoint` (SP-10) lands in this same wave and merges **after** SP-12 in the wave order, so SP-12 develops its `checkpoint.Writer.Advance` call site against the SP-01 stub and the `testdata/golden/contracts/checkpoint/` fixtures per Rule W-2.

**What exists when you finish.** `scheduler.Evaluate` is a pure, allocation-light, fully unit-testable function of `Inputs` that returns a `Decision` carrying `ShouldCompact`, ordered `Reasons`, the chosen `Candidate` `P`, its `PScore`, a complete numeric `Breakdown` map that `/qompack:status` and `eval` both read, the `TTLState`, the Young–Daly interval, the soft-floor and hard-ceiling token counts, and the O3 `Background` task list. A `Detector` implements Bayesian online changepoint detection over the four cheap features of §6.6 with pruned, bounded-length updates and a versioned, CRC-checked binary state. The droppable-block ranking (ephemeral → superseded → ordinary compactable tool result) ships as pure, foundation-only spec in `scheduler` so the replay policy can share it. `internal/daemon` gains SP-12-owned files implementing `scheduler.Runtime`: a tap that decorates SP-05's `Services` seams through `Options.Bind` so L0 events reach L3 without editing a single SP-05 or SP-08 line, feature extraction from `observer.Signals`, the `store.ToolUseRecord` adapter onto that ranking, suffix-sum `ReclaimableTokens`, candidate assembly as changepoint boundaries ∩ API-round boundaries with a cached turn→position map and cached `CrossingEdges`, session-scoped persistence to `state/bocd.json` and `state/scheduler.json`, six registered O3 idle tasks (one of them the acting `act.advance_frontier`), and O5 frontier advancement that closes segments on changepoint/todo-completion/passing-test/commit and drives `checkpoint.Writer.Advance`. `scheduler.PSelectionAvailable()` returns true once a real Runtime is constructed. `test/replay/phase4_test.go` asserts the §10 Phase 4 exit criterion directly.

---

## Gap closure map

Every gap this subplan claims, the mechanism that closes it (per `Qompack.md` §9's traceability matrix), the artifact, and the test that proves it. A gap with no row here is not this slice's.

| Gap | §9 "closed by" | Artifact in this slice | Proof |
|---|---|---|---|
| **G1.1** task-blind trigger | L3 BOCD changepoints | `bocd.go` detector + the `at_changepoint` clause in `evaluate.go` | `TestBOCD_StepChange_DetectedWithinFiveObservations`, `TestEvaluate_Changepoint_Fires` |
| **G1.2** no agent agency | L3 soft floor + hard ceiling | `thresholds.go`: `HardCeiling = effectiveWindow − 13 000 − hardCeilingMargin`, i.e. 20 000 tokens of headroom below the host's own threshold, so the plugin always checkpoints first | `TestHardCeiling_OneTurnBelowHostThreshold`, `TestSoftFloorBelowHardCeiling_Property` |
| **G1.3** fixed 13K buffer | L3 adaptive Young–Daly interval | `youngdaly.go`: `√(2·δ·M)` with δ measured at runtime (`RecordCompactionCost`) and M from the live burn rate — no constant anywhere in the path | `TestYoungDaly_Formula`, `TestRuntime_DeltaEWMA`, `TestResolveDelta_NilMeansMeasureNotZero` |
| **G1.4** blocking-limit cliff | L3 soft floor keeps sessions far from the cliff | soft floor at 55% of the effective window (99 000 of 180 000) vs. the host's blocking limit at `effectiveWindow − 3K` = 177 000 | `TestSoftFloor_55PctOfEffectiveWindow`, `TestEvaluate_BelowSoftFloor_NoCompact` |
| **G5.1** single cut | L3 multi-segment log | segment close and roll on changepoint/todo/test/commit (`scheduler_frontier.go`) turns the session into a segment sequence, so the cut set is the segment boundary set rather than one pivot; `Candidate.SegmentID` carries it into the decision | `TestFrontier_CloseOn*`, `TestAssemble_IntersectsChangepointsAndRounds` |
| **G5.2** the cheap direction is the useless one | L3 p-selection with the cache term | `pselect.go`: `score(p) = reclaimable(p)·r − w·(n−p)·cacheFactor − λ·coupling(p)`; the cache term is what makes an early cut *choosable* when it is genuinely free rather than structurally forbidden | `TestEvaluate_ArgmaxLatestWhenWarm`, `TestEvaluate_ArgmaxDeepestWhenCold`, `TestPhase4_RewriteTokensReduced` |
| **G7.1** the summarization call is the most expensive in the session | L3 earlier, cheaper, better-targeted compaction | firing at the soft floor instead of 167K cuts the call's input size; O5 frontier advancement (`advanceFrontier`) then bounds the residual span the summarizer must cover to `maxResidualTokens` | `TestPhase4_ResidualUnderMaxResidualTokens`, `TestFrontier_AdvanceCallsWriterWithClosedUnencodedOnly` |
| **G7.6** compaction loops / >100% indicator jam | L3 keeps sessions far from the failure region | the composite trigger acts at 55% and escalates `UrgencyNow` at the hard ceiling, so the session never reaches the region where the host loops; `no_candidates` refuses to claim a compaction it cannot place rather than retrying | `TestEvaluate_HardCeiling_Fires_UrgencyNow`, `TestEvaluate_NoCandidatesAboveCeiling` |
| **G8.2** summarizer works from the worst vantage point | L3 compacts earlier, at lower context | same soft-floor mechanism: the summarization fork runs at ~99K rather than ~167K of context, and at a changepoint rather than mid-hypothesis | `TestEvaluate_Changepoint_Fires`, `TestPhase4_NoDivergenceRegression` |

G1.5 (no task-boundary signals) is closed **jointly**: SP-08 owns `observer.ExtractSignals`, SP-12 owns the translation (`FeaturesFrom`, the `todos` feature) and the consumption (segment close on todo/test/commit). It is listed on SP-08's assignment, not this one, which is why it appears in commit footers here but not in the table above.

---

## Design context (verbatim from `Qompack.md`)

Everything below is quoted verbatim. No fact needed to implement this slice lives outside this section.

### §8.4 L3 — Scheduler (the whole section)

> The scheduler answers two questions: **when** to compact and **where** to cut.
>
> **When — the composite trigger:**
>
> ```
> should_compact  =  tokens > soft_floor
>                 AND ( at_changepoint
>                       OR elapsed > young_daly_interval
>                       OR tokens > hard_ceiling
>                       OR idle_gap > ttl )     # cache provably cold → cut is free
> ```
>
> - `soft_floor` — well below the auto-compact threshold; default 55% of effective window, so the plugin acts before Claude Code's own trigger and the expensive path stays a fallback
> - `young_daly_interval = √(2 · δ · M)` where `δ` is measured compaction cost and `M` is expected time to forced compaction at the current burn rate
> - `hard_ceiling` — one turn's worth of headroom below Claude Code's threshold, so the plugin always gets to checkpoint first (closes G1.2 as far as a plugin can)
>
> **Where — p-selection:**
>
> ```
> candidates = changepoint boundaries ∩ API-round boundaries
> for p in candidates:
>     reclaimable(p) = Σ tokens of droppable blocks after p
>     rewrite(p)     = w · (n − p)          # 0 if cache cold or expiring
>     distortion(p)  = λ · segment_coupling(p)
>     score(p)       = reclaimable(p)·r − rewrite(p) − distortion(p)
> choose argmax
> ```
>
> `segment_coupling(p)` is the count of DAG edges crossing `p` — a direct, cheap measure of how much the post-`p` region depends on pre-`p` detail. Cutting where coupling is low is the operational meaning of "compact at a task boundary."
>
> **Cache-state awareness (corrected for sliding TTL).** The TTL refreshes on every hit, so the prefix does not expire during active use — only during idle gaps. The scheduler therefore tracks *time since last API call*, not time since last cache write. When an idle gap exceeds the TTL (cache provably cold), `rewrite(p) → 0` for all `p` and the scheduler prefers a deep cut it would refuse mid-burst. When the user has been idle long enough that expiry is imminent, the marginal cost of forfeiting the warm prefix approaches zero and the same preference applies. This implements the corrected bimodality in §5.4 and reuses the exact idle signal that gates Claude Code's own time-based MicroCompact.
>
> **Idle-time background work (O3).** User think-time is free compute. During detected idle, the scheduler advances the shadow checkpoint incrementally, runs store GC, precomputes backward slices from the current criterion set, and refreshes Δ-scores — so that when compaction does fire, the expensive analysis is already done and `PreCompact` only finalizes. This is also the natural moment to *perform* a deep cut: the cache is dying anyway and no user is waiting on latency.

### §5.3 The corrected objective

> ```
> minimize   Σ tokens_kept · r          (steady-state read cost)
>          + w · (n − p_min)            (one-time rewrite)
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

### §5.4 Choosing p

> Three structural facts make this cheap:
>
> - **Monotonicity.** Reclaimable tokens are non-increasing in `p`; cost is increasing. The objective is unimodal in the typical case, so a single pass finds the knee.
> - **Small candidate set.** Do not evaluate all `n` positions — only changepoint boundaries from §6.6. Twenty candidates, not 167,000. A cut at a task boundary has low distortion *and* tends to follow a long stable prefix.
> - **TTL bimodality — with a correction.** If the prefix has expired, `p = 0` is free, so the optimal policy is genuinely bimodal: **edit as late as possible, or edit when the cache is cold and rebuild everything.** The expensive region is the middle. But the TTL is *sliding*, not fixed — it refreshes on every cache hit, so an actively-used prefix never expires on its own. Cold-cache windows therefore occur only during **idle gaps**: user think-time, meetings, overnight. "Wait for expiry" operationally means "detect idle," which is precisely the signal Claude Code's own time-based MicroCompact path already keys on.
>
> The strategic consequence:
>
> > **Compaction should be scheduled against cache state, not only against token count.** A deep cut during an idle gap that outlasts the TTL is nearly free — the rewrite was going to happen on the next message regardless. The same cut mid-burst, one minute after a fresh cache write, forfeits every discounted read the warm prefix would have served. Idle-gap detection is therefore a first-class scheduler input (§8.4), and the Young–Daly interval (§6.7) takes it as a term.

### §5.1 and §5.2 — the multipliers and the governing quantity

> Prompt caching is **exact-prefix-match**. An edit at position `p` invalidates everything from `p` onward. With read multiplier `r` and write multiplier `w`:
>
> ```
> rebuild cost = w · (n − p)
> forfeited discount = (1 − r) · (n − p)
> ```
>
> Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing before tuning**, since the ratio drives several thresholds below.

> The governing quantity is the **earliest edit position**, not how much was dropped:
>
> ```
> cost = w · (n − p_min)     where p_min = position of the earliest dropped block
> ```
>
> | Scenario | Dropped | p_min | Rewrite | Verdict |
> |---|---|---|---|---|
> | A | 60K tokens | 150K | 17K | Good |
> | B | 2K tokens | 10K | 157K | 30× the cost, 1/30 the benefit |

### §6.6 Changepoint detection for when to compact

> **Closes:** G1.1, G1.5
>
> Bayesian online changepoint detection maintains a distribution over run length since the last changepoint, updated in O(1) amortized with pruning. Run it over cheap features:
>
> - File-path locality (Jaccard over recently-touched paths)
> - Tool-type distribution shift
> - Lexical cohesion (TextTiling-style)
> - Inter-turn time gaps
> - Todo-list state transitions
>
> A changepoint is a task boundary. **Compacting at one is nearly free in distortion** because the new segment does not depend on the old segment's detail. Compacting mid-segment is maximally destructive. The fixed 13K buffer has no idea which it is doing.

### §6.7 Optimal stopping / Young–Daly cadence

> **Closes:** G1.1, G1.3, G9.1
>
> HPC solved this. Given checkpoint cost `δ` and mean time between failures `M`:
>
> ```
> optimal interval = √(2 · δ · M)
> ```
>
> Map `δ` to compaction cost (tokens plus latency; the summarization call at 167K input runs ~15–40s) and `M` to expected time until forced compaction at the current burn rate.
>
> Combined with §6.6 and §5.4, the policy becomes two lines:
>
> > **Compact at the first changepoint after the Young–Daly interval has elapsed, preferring a `p` near cache expiry.**
>
> That dominates a fixed 167K trigger on all three cost terms.

### §2.5 Trigger arithmetic (the host constants the hard ceiling is measured against)

> ```
> effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
> autoCompactThreshold   = effectiveContextWindow − 13_000
> ```
>
> For a 200K model: effective ≈ 180K, threshold ≈ 167K.
>
> | State | Boundary |
> |---|---|
> | `isAboveWarningThreshold` | effectiveWindow − 20K |
> | `isAboveErrorThreshold` | effectiveWindow − 20K |
> | `isAboveAutoCompactThreshold` | effectiveWindow − 13K |
> | `isAtBlockingLimit` | effectiveWindow − 3K (manual compact required) |

### §2.2 MicroCompact — the compactable tool set (defines "ordinary compactable tool results")

> Compactable tool set:
>
> ```
> FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
> ```
>
> Only high-volume, reproducible results are targeted. AgentTool and MCP results are preserved.

### §2.6 API-round boundaries (defines the candidate intersection)

> When the compaction request itself exceeds the prompt-too-long limit, the system groups messages by API round (`groupMessagesByApiRound`, boundary on new assistant `message.id`) and drops the **oldest** groups until the gap is covered.

### §8.7 Retrieved content is born ephemeral — the droppable ranking

> - Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).

### §8.1 item 3 — supersession feeds the ranking

> **Redundancy detection.** If the chunk set is a superset or near-duplicate of a prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the first candidates for eviction and should never appear in a summary.

### §8.5 O5 — amortized compaction (frontier advancement)

> **Amortized compaction — the latency architecture (O5).** The incremental-writing rule above is not only a defense against the `PreCompact` timeout; taken seriously, it changes the asymptotics of compaction latency. Where the wall-clock goes in a stock long-session compact: prefill is mostly cache reads (thanks to the cache-key reuse in §1.2), so **decode dominates** — several thousand output tokens of 9-section summary plus scratchpad, generated autoregressively — with extended thinking stacked on top when the session has it enabled, and a slow first post-compact turn from re-prefilling up to 75K of eagerly restored context.
>
> The fix is to keep the checkpoint frontier moving continuously: every time a segment closes (changepoint, todo completion, passing test run), encode it into the checkpoint during the next idle moment, advancing frontier `N` all session long. When compaction fires, the O1 instruction restricts the summarizer to turns after `N` — and that residual span is now 10–20K tokens rather than 150K, **regardless of how long the session has run**. Short novel prefill, short decode, every time.
>
> This is precisely the stop-the-world vs. incremental garbage collection distinction. Stock compaction is a stop-the-world collector: pause, walk the entire heap, resume — pause time proportional to session size. Frontier advancement is an incremental collector: small units of encoding work interleaved with real work, so no pause is ever O(session). Per-compaction cost drops from **O(session) to O(delta)** — amortized O(1) per turn.

### §8.2 — the segment log and the DPI guard SP-12 drives

> **Segment log.** Changepoint-delimited segments, each with: start/end turn, feature summary, encoded-once flag, and checkpoint reference. **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint. It is re-encoded from the *original chunks* or not at all.

### §10 Phase 4 — the phase this slice closes

> ### Phase 4 — Scheduler
>
> - BOCD over cheap features
> - Young–Daly cadence with measured δ
> - p-selection with the cache term
> - Idle-gap detection (sliding-TTL model, §8.4) and idle-time background work (O3)
> - **Continuous checkpoint frontier advancement (O5)** — encode closed segments during idle so the residual span, and therefore the compaction pause, stays O(delta)
>
> **Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).

### §11.2 — the three latency metrics this slice drives

> | **Compaction pause** | Wall-clock of the summarization call; target O(delta) under frontier advancement (O5) |
> | **Residual span at compaction** | Tokens between frontier N and the compaction point — the direct driver of pause time |
> | **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### §11.3 Guardrails

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §12 — the two risk rows this slice owns

> | Plugin and Claude Code compaction fight each other | Medium | Soft floor well below the auto threshold; plugin acts first by design |
> | Cache multipliers change | Low | Read `r` and `w` from config, never hardcode |

### §5.6 — ski rental (the helper §5.13 places in this package)

> **Ski rental for the write decision.** Whether to pay `w` to write a cache entry is rent-or-buy under unknown horizon. Competitive ratio 2 deterministic, `e/(e−1) ≈ 1.58` randomized. Practically: write the cache when expected remaining reads exceed `w/r ≈ 12.5`. Short sessions should not be paying for cache writes at all.

### Appendix A — the formulas, verbatim

> **Cache rewrite cost**
> ```
> cost = w · (n − p_min)
> ```
>
> **Composite compaction objective**
> ```
> min  Σ tokens_kept·r  +  w·(n − p_min)  +  λ·D(keep-set)
> ```
>
> **Auto-compact threshold (current system)**
> ```
> effectiveWindow = contextWindow − min(maxOutputTokens, 20_000)
> threshold       = effectiveWindow − 13_000
> ```
>
> **Young–Daly optimal checkpoint interval**
> ```
> I* = √(2·δ·M)
> ```
>
> **Ski-rental cache-write threshold**
> ```
> write when  E[remaining reads] > w/r   (≈ 12.5 at r=0.1, w=1.25)
> ```

### Appendix C — the configuration this slice reads, verbatim

> ```jsonc
>   "scheduler": {
>     "softFloorPct": 0.55,
>     "hardCeilingMargin": 20000,
>     "youngDaly": { "enabled": true, "measuredDeltaSeconds": null },
>     "changepoint": { "hazardRate": 0.004, "features": ["paths","tools","time","todos"] },
>     "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
>     "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
>   },
>   "checkpoint": {
>     "budgetTokens": 12000,
>     "incrementalSpanInstruction": true,
>     "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
> ```
>
> ```jsonc
>   "selection": {
>     "slicing": "thin",
>     "deltaScoring": "cheap",
>     "submodular": { "lambda": 0.4, "lazyGreedy": true }
>   },
> ```

**00-ARCHITECTURE §11.2 note on the `null` δ, verbatim:**

> Numbers keep JSON semantics; `null` on `youngDaly.measuredDeltaSeconds` means "measure at runtime", not "zero", and is modelled as `*float64`.

**00-ARCHITECTURE §5.13 note on where `Runtime` lives, verbatim:**

> **Package purity and where `Runtime` lives.** Package `scheduler` imports foundation packages only (§3.2): `Evaluate` is pure, `Runtime` is an *interface*. Assembling `Inputs.Candidates` requires `dag.CrossingEdges` and the store's tool-use records, so the `Runtime` **implementation** lives in `internal/daemon` (a composition root). SP-12 owns both the `scheduler` package and that implementation file.
>
> **Who computes `Candidate.ReclaimableTokens`.** The `Runtime` implementation, not the analyzer. Droppable blocks after `p` are ranked ephemeral-first, then superseded, then ordinary compactable tool results, then everything else — the §8.7 eviction order. This must exist in wave 3 for p-selection to be meaningful; `analyzer` (wave 4) later refines *which* of them to keep, never *whether* they are droppable.

**00-ARCHITECTURE §11.6, verbatim (the no-hardcoding rule this slice is the main subject of):**

> `scheduler.cache.readMultiplier` (`r`), `writeMultiplier` (`w`), and `ttlSeconds` are read from config at every use site. There is no package-level `const r = 0.1` anywhere. The in-repo `nomagic` analysis pass fails the build on any float literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` or integer literal in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` appearing outside `internal/config/defaults.go`, `*_test.go`, and explicitly annotated `//nomagic:allow <reason>` lines. The ski-rental threshold is computed as `w/r`, never written as `12.5`.

---

## Out of scope

Implement none of the following. Each names its owning sibling subplan.

| Out of scope | Owner |
|---|---|
| `checkpoint.Writer` / `Draft` / `Begin` / `Advance` / `Finalize` internals, `checkpoint.Truncate`, `ExtractDecisions`, `FocusInstructions` (including the O1 span paragraph), the `PreCompact` hook, `state/frontier.json`, `checkpoints/MANIFEST.jsonl` | **SP-10**. SP-12 *calls* `Writer.Advance` and `Writer.Begin`; it does not implement them and does not write `state/frontier.json`. |
| Rehydration, the eight-item injection order, the drop report, `DropReporter`, `rules`, `skills`, the 8–12K budget | **SP-11** |
| The MCP server, all eight tools, ephemeral tagging *at birth* on retrieval results, `Promoter` / `promoteAfterExpansions`, budget B-F | **SP-13**. SP-12 only *reads* `store.ToolUseRecord.Ephemeral`, which SP-13/SP-08 set. |
| `/qompack:status` rendering, `--json`, the last-decision table layout, all seven slash commands, `docs/commands.md` | **SP-14**. SP-12 supplies `Decision.Breakdown`; it does not render it. |
| Δ-scoring, `DeltaScorer`, `DetectRedundancy`, submodular lazy greedy, `analyzer.NewSelector`, flipping `config.SelectionCfg.Submodular.Enabled` to `true`, Sequitur / `grammar`, thrash warnings | **SP-15**. SP-12 owns only the `PSelectionAvailable()` runtime gate. |
| Cross-session warm start (O4), seeding BOCD feature priors from past sessions, per-segment Bloom filters, demand-driven promotion, *applying* the ski-rental policy to a write decision, progressive truncation tuning | **SP-16**. SP-12 ships the `SkiRentalShouldWrite` helper because §5.13 places it in this package; nothing calls it in this slice. |
| `eval.Harness`, `Replay`, `Belady`, `Compare`, `ScoreRun`, `Synthesize`, the 24-session synthetic corpus, `Divergence`, `Score`, `Percentiles`, the replay-gate driver itself | **SP-02**. SP-12 adds one `eval.Policy` implementation and one test file under `test/replay/`. |
| `store` objects, roots, `SegmentLog` mechanics, `MarkEncoded` semantics, GC mark-and-sweep, `tokens` estimation | **SP-06** |
| `dag` edge model, persistence, `BackwardSlice` / `ForwardSlice` / `CrossingEdges` / `NodesAfter` / `Compact` implementations | **SP-07** |
| `observer.Signals`, `ExtractSignals`, `PostToolUse` / `UserPromptSubmit` / `Stop` semantics, the session's **first** `SegmentLog.Open`, addressable tombstones, supersession detection | **SP-08**. SP-12 owns every segment *close* and every subsequent *roll-open*. |
| `negknow.Ledger`, `RefreshStaleness`, `RebuildBloom` mechanics, descriptors, the three-way answer | **SP-09**. SP-12's `rebuild_bloom` idle task only *invokes* the ledger's existing methods. |
| `internal/daemon` core (registry, ingest queue, WAL, worker pool, `IdleController` implementation, the `act.` prefix rule, `Options`/`Services`/`Handle`/`Bind`/`DeclareProducers`, spool drain, idle exit), `internal/ipc`, `internal/contract`, budgets B-A/B-B/B-C/B-D | **SP-05**. SP-12 adds new files inside `internal/daemon`, *uses* the `Bind` and `IdleController.Register` seams SP-05 shipped for exactly this purpose, and adds three blocks to the `daemon` subcommand of `cmd/qompack/main.go`. It edits no existing SP-05 line and registers no op route. |
| `internal/config` schema, defaults, precedence, validation, provenance, JSON Schema, the `nomagic` pass | **SP-01** |
| Packaging, cross-platform matrix, release pipeline | **SP-17** |
| User guide, troubleshooting, config reference, UAT | **SP-18**. SP-12 writes one ADR only. |

---

## Interface contract

### Consumes (exact signatures, from 00-ARCHITECTURE §5)

```go
// internal/core (§4)
type SessionID string
type ToolUseID string
type TurnIndex int
type SegmentID int
type CheckpointSeq int
type Tokens int
type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
var ErrNotFound       = errors.New("qompack: not found")
var ErrAlreadyEncoded = errors.New("qompack: segment already encoded (DPI guard)")

// internal/paths (§3.3)
func WriteAtomic(p string, b []byte) error

// internal/config (§5.1, Appendix C)
type Config struct {
    Scheduler  SchedulerCfg  `json:"scheduler"`
    Checkpoint CheckpointCfg `json:"checkpoint"`
    Selection  SelectionCfg  `json:"selection"`
    // … other sections
}
// SchedulerCfg leaves used by this slice (names normative from Appendix C):
//   SoftFloorPct      float64
//   HardCeilingMargin int
//   YoungDaly         struct{ Enabled bool; MeasuredDeltaSeconds *float64 }
//   Changepoint       struct{ HazardRate float64; Features []string }
//   Cache             struct{ ReadMultiplier, WriteMultiplier float64; TTLSeconds int }
//   Idle              struct{ DetectAfterSeconds int; BackgroundWork, DeepCutWhenCold bool }
// CheckpointCfg leaves used: Frontier struct{ AdvanceOnSegmentClose bool; MaxResidualTokens int }
// SelectionCfg leaves used:  Submodular struct{ Lambda float64; LazyGreedy bool }

// internal/logging (§5.2)
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    Loud(msg string, kv ...any)
}

// internal/obs (§5.2)
type Registry interface {
    Hist(name string) Histogram
    Counter(name string) Counter
    Gauge(name string) Gauge
    Snapshot() Snapshot
    CheckBudgets(cfg config.Config) []BudgetBreach
}

// internal/dag (§5.9)
func (g Graph) CrossingEdges(pos int) int
func (g Graph) NodesAfter(pos int) []Node
func (g Graph) BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
func (g Graph) Compact(ctx context.Context) error
type Node struct {
    ID NodeID; Kind NodeKind; Turn core.TurnIndex; TS core.UnixMilli
    Pos int; Ref string; Root core.Hash; Tokens core.Tokens; Ephemeral bool
}

// internal/store (§5.8)
type Supersession uint8 // StatusOK, StatusSuperseded
type ToolUseRecord struct {
    ID core.ToolUseID; Session core.SessionID; Turn core.TurnIndex; TS core.UnixMilli
    Tool string; ArgsDigest core.Hash; ArgsPreview string; Root core.Hash; Path string
    Bytes int64; Tokens core.Tokens; Signature sketch.Signature
    Status Supersession; SupersededBy core.ToolUseID; Ephemeral bool; Subagent string
}
func (s Store) ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
func (s Store) Segments() SegmentLog
func (s Store) GC(ctx context.Context, p GCPolicy) (GCReport, error)
type GCPolicy struct{ RetainDays, RetainSessions int; DryRun bool; Deadline time.Duration }
type Segment struct {
    ID core.SegmentID; Session core.SessionID
    StartTurn, EndTurn core.TurnIndex; StartTS, EndTS core.UnixMilli
    Features map[string]float64; Tokens core.Tokens; EncodedOnce bool
    CheckpointSeq core.CheckpointSeq; Closed bool; BloomRef string
}
type SegmentLog interface {
    Open(ctx context.Context, s Segment) (core.SegmentID, error)
    Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
    Get(ctx context.Context, id core.SegmentID) (Segment, error)
    Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
    Current(ctx context.Context, s core.SessionID) (Segment, error)
    MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
    Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
    Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error)
}

// internal/checkpoint (§5.14) — SP-10, wave 3, merges AFTER SP-12; develop against the SP-01 stub
type Writer interface {
    Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error)
    Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error)
    Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error)
    Abort(d *Draft) error
}

// internal/negknow (§5.10)
type Ledger interface {
    RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
    RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
    Health() Health
    // … other methods
}
type Health struct{ Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool }

// internal/observer (§5.21)
type Signals struct{ TodoCompleted, TestPassed, GitCommit bool; Paths []string }

// internal/daemon (§5.4) — SP-05 seams SP-12 wires into
type Daemon interface {
    Run(ctx context.Context) error
    Registry() *SessionRegistry
    Drain(ctx context.Context) (int, error)
    Idle() IdleController
    Stop(ctx context.Context) error
}
type IdleController interface {
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}
type Options struct {
    ProjectRoot string; Cfg config.Config; Log logging.Logger
    Metrics obs.Registry; Clock core.Clock
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
}
func New(o Options) (Daemon, error)

// The two SP-05 extension seams SP-12 wires into (SP-05 plan, `internal/daemon/options.go`;
// 00-ARCHITECTURE §5.4 "Extension seams … so later waves wire in WITHOUT editing daemon
// internals"). Bind hooks run once, in registration order, at daemon start.
func (o *Options) Bind(fn func(*Services))
type Services struct {
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    ObserveTool    func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObservePrompt  func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObserveStop    func(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error) // SP-08
    SessionStart   func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08 + SP-11
    SessionEnd     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    PreCompact     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-10
    Rehydrate      func(ctx context.Context, e hookio.Event) (string, error)        // SP-11
    MCPInitialized func() bool                                                      // SP-13
    StatusExtra    func(ctx context.Context) map[string]any                         // SP-14
}

// SP-05's IdleController naming rule, NORMATIVE and easy to miss: a task whose name begins
// `act.` is an *acting* task and RunOnce SKIPS it when the contract monitor is in
// degraded-passive (00-ARCHITECTURE §12.1 "no scheduler-initiated checkpoints"). Every other
// task name is recording/maintenance work that keeps running. This is why exactly one of
// SP-12's six tasks is registered as `act.advance_frontier`.

// internal/hookio (§5.3) — the payload the wrapped seams carry
type Event struct {
    HookEventName string; SessionID core.SessionID; TranscriptPath string
    Source string; Trigger string
    ToolName string; ToolUseID core.ToolUseID
    Prompt string
    // … other fields
}

// internal/eval (§5.18) — SP-02
type Policy interface {
    Name() string
    KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}
type KeepSet struct{ IDs []string; Tokens core.Tokens; P int }
type Harness interface {
    Load(dir string) ([]Session, error)
    Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
    Compare(uncompacted, compacted Run) Divergence
    Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
    ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
    Report(ctx context.Context, scores map[string][]Score) (Report, error)
}
```

### Produces (what later subplans rely on)

Everything in 00-ARCHITECTURE §5.13, now real rather than `ErrNotImplemented`:

```go
package scheduler

type TriggerReason string
const (
    ReasonSoftFloor     TriggerReason = "soft_floor"
    ReasonChangepoint   TriggerReason = "changepoint"
    ReasonYoungDaly     TriggerReason = "young_daly"
    ReasonHardCeiling   TriggerReason = "hard_ceiling"
    ReasonIdleColdCache TriggerReason = "idle_cold_cache"
)

type TTLState string
const (
    TTLWarm     TTLState = "warm"
    TTLExpiring TTLState = "expiring"
    TTLCold     TTLState = "cold"
    TTLUnknown  TTLState = "unknown"
)

type Urgency uint8
const (
    UrgencyNone Urgency = iota
    UrgencyAdvisory
    UrgencyNow
)

type Features struct {
    PathJaccard     float64
    ToolShift       float64
    LexicalCohesion float64
    GapSeconds      float64
    TodoTransition  float64
}
type ChangepointState struct {
    RunLength       int
    ProbChangepoint float64
    AtChangepoint   bool
    Posterior       []float64
}
type Detector interface {
    Observe(f Features) ChangepointState
    State() ChangepointState
    Reset()
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func NewBOCD(hazardRate float64, features []string) Detector

type Candidate struct {
    Pos               int
    Turn              core.TurnIndex
    SegmentID         core.SegmentID
    RoundBoundary     bool
    ReclaimableTokens core.Tokens
    Coupling          int
}

type Inputs struct {
    Now                    core.UnixMilli
    ContextTokens          core.Tokens
    EffectiveWindow        core.Tokens
    MaxOutputTokens        core.Tokens
    LastAPICallTS          core.UnixMilli
    LastCacheWriteTS       core.UnixMilli
    BurnRateTokensPerMin   float64
    MeasuredDeltaSeconds   *float64
    Changepoint            ChangepointState
    Candidates             []Candidate
    FrontierTurn           core.TurnIndex
    ResidualTokens         core.Tokens
    ExpectedRemainingReads float64
    Cfg                    config.SchedulerCfg

    // ── ADDITIVE, SP-12. Both fields are new members of a struct `internal/scheduler`
    // owns; nothing outside SP-12 constructs `Inputs`, so this needs no §0 amendment.
    LastCompactionTS core.UnixMilli // §8.4 `elapsed`: base for the Young–Daly clause.
                                    // The Runtime seeds it with session start when there
                                    // has been no compaction yet. 0 ⇒ clause disabled.
    CouplingLambda   float64        // §8.4 λ in distortion(p) = λ·segment_coupling(p).
                                    // Filled from config `selection.submodular.lambda`
                                    // (Appendix C default 0.4). ≤0 ⇒ distortion term off.
}

type Decision struct {
    ShouldCompact                      bool
    Reasons                            []TriggerReason
    P                                  Candidate
    PScore                             float64
    Breakdown                          map[string]float64
    Urgency                            Urgency
    TTL                                TTLState
    YoungDalySeconds                   float64
    Background                         []BackgroundTask
    SoftFloorTokens, HardCeilingTokens core.Tokens
}
type BackgroundTask string
const (
    TaskAdvanceFrontier BackgroundTask = "advance_frontier"
    TaskGC              BackgroundTask = "gc"
    TaskPrecomputeSlice BackgroundTask = "precompute_slice"
    TaskRefreshDelta    BackgroundTask = "refresh_delta"
    TaskRebuildBloom    BackgroundTask = "rebuild_bloom"
    TaskCompactDAG      BackgroundTask = "compact_dag"
)

func Evaluate(in Inputs) Decision
func PSelectionAvailable() bool
func EnablePSelection()   // SP-12 additive: called by the Runtime constructor
func DisablePSelection()  // SP-12 additive: called on Runtime shutdown and by tests
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64
func SkiRentalShouldWrite(expectedReads, r, w float64) bool

// Host constants, all from Qompack.md §2.5:
//   effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
//   autoCompactThreshold   = effectiveContextWindow − 13_000
//   "For a 200K model: effective ≈ 180K, threshold ≈ 167K."
// They describe the HOST, not a Qompack tunable, so Appendix C has no key for any of them
// and §11.6's nomagic pass is satisfied by an explicit allowance on each.
const (
    HostAutoCompactBuffer    core.Tokens = 13_000  //nomagic:allow host constant, Qompack.md §2.5
    HostMaxOutputCap         core.Tokens = 20_000  //nomagic:allow host constant, Qompack.md §2.5
    HostDefaultContextWindow core.Tokens = 200_000 //nomagic:allow host default, Qompack.md §2.5
    HostDefaultMaxOutput     core.Tokens = 32_000  //nomagic:allow host default, Qompack.md §2.5
)

// EffectiveWindow is §2.5's first line, exported so the Runtime, /qompack:status (SP-14) and
// eval (SP-02) all derive the same number instead of three drifting copies.
func EffectiveWindow(contextWindow, maxOutputTokens core.Tokens) core.Tokens

// SoftFloor and HardCeiling are exported so /qompack:status (SP-14) and eval (SP-02)
// can render the same numbers Evaluate used without recomputing them differently.
func SoftFloor(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens
func HardCeiling(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens

// DropClass is the §8.4/§8.7 droppable-block ranking behind reclaimable(p). It lives in
// `scheduler` rather than `daemon` for two reasons: it is pure spec (the §2.2 compactable
// tool set) needing no import beyond the standard library, and `test/replay/l3policy` must be
// able to score candidates without importing `internal/daemon`, which is a composition root
// that nothing may import (00-ARCHITECTURE §3.2). `daemon.ClassifyDrop` is the three-line
// adapter from `store.ToolUseRecord` onto this function, and it is the API 00-ARCHITECTURE
// §5.13 names when it says the Runtime — not the analyzer — owns the classification.
type DropClass uint8
const (
    DropNone       DropClass = iota // not droppable: AgentTool, MCP results, anything unlisted
    DropOrdinary                    // §2.2 compactable tool set
    DropSuperseded                  // store.StatusSuperseded (§8.1 item 3)
    DropEphemeral                   // retrieval-born (§8.7) — FIRST eviction candidate
)
func DropClassOf(tool string, ephemeral, superseded bool) DropClass
func (c DropClass) EvictionRank() int // DropEphemeral=3 … DropNone=0

// ClassifyTTL is exported for SP-14's status surface and SP-02's replay policy.
func ClassifyTTL(now, lastAPICallTS core.UnixMilli, ttlSeconds int) (TTLState, float64)
// CacheFactor is the multiplier applied to rewrite(p): 1.0 warm → 0.0 cold, linear across
// the expiring band. Exported so eval can reproduce Evaluate's arithmetic exactly.
func CacheFactor(state TTLState, idleGapSeconds float64, ttlSeconds int) float64

type Runtime interface {
    Observe(ctx context.Context, f Features, at core.TurnIndex) ChangepointState
    Evaluate(ctx context.Context) (Decision, error)
    NotifyActivity(ts core.UnixMilli)
    IdleSince() (core.UnixMilli, bool)
    Persist(ctx context.Context) error
}
```

```go
package daemon // SP-12-owned files inside SP-05's composition root

// DropClass and its constants are ALIASES of the scheduler types above, so `daemon` and
// `test/replay/l3policy` classify identically by construction rather than by a parity test.
type DropClass = scheduler.DropClass
const (
    DropNone       = scheduler.DropNone
    DropOrdinary   = scheduler.DropOrdinary
    DropSuperseded = scheduler.DropSuperseded
    DropEphemeral  = scheduler.DropEphemeral
)

// ClassifyDrop is the store-aware adapter. Body, in full:
//   return scheduler.DropClassOf(rec.Tool, rec.Ephemeral, rec.Status == store.StatusSuperseded)
func ClassifyDrop(rec store.ToolUseRecord) DropClass

type SchedulerRuntimeOptions struct {
    ProjectRoot string
    Session     core.SessionID    // MAY be empty: the daemon starts before any session exists.
                                  // The runtime binds the first session id it observes.
    Cfg         config.Config
    Clock       core.Clock
    Log         logging.Logger
    Metrics     obs.Registry
    Store       store.Store
    Graph       dag.Graph
    Ledger      negknow.Ledger    // may be nil
    Checkpoints checkpoint.Writer // may be nil (SP-10 merges after SP-12)
    // Sources supplies checkpoint.SourceSet to Writer.Begin. SP-10 owns every member of that
    // struct and owns wiring this field when it merges; until then it is nil and frontier
    // advancement is inert in exactly the same way a nil Checkpoints makes it inert. SP-12
    // never constructs a SourceSet from live context — that is invariant 1, and SourceSet's
    // shape (00-ARCHITECTURE §5.14) makes the alternative uncompilable.
    Sources func() (checkpoint.SourceSet, error) // may be nil
}
func NewSchedulerRuntime(o SchedulerRuntimeOptions) (scheduler.Runtime, error)

// The concrete runtime additionally implements these three, reached by type assertion because
// scheduler.Runtime (00-ARCHITECTURE §5.13) declares none of them and SP-12 may not add a
// method to a §5 interface (Rule W-3). Adding methods to a struct SP-12 owns is allowed.
//   Close() error                          — Persist + scheduler.DisablePSelection
//   CostRecorder                           — see below
//   PrecomputedSlice() (dag.Slice, bool)   — the precompute_slice cache SP-11/SP-15 read
func CloseSchedulerRuntime(rt scheduler.Runtime) error          // nil-safe type-asserting helper
func PrecomputedSlice(rt scheduler.Runtime) (dag.Slice, bool)   // ok=false when never computed

// WrapServicesForScheduler is HOW L0 EVENTS REACH L3. SP-05's daemon calls only the
// `Services` function seams (`ObserveTool`, `ObserveStop`, …) — it never calls `Sched`
// itself — so SP-12 decorates those seams through SP-05's `Options.Bind` late-binding hook.
// Each wrapper calls the inner seam FIRST (so SP-08 has already written the tool-use record
// the tap reads), then taps the scheduler. Nil inner seams are tolerated. No SP-05 or SP-08
// file is edited.
func WrapServicesForScheduler(s *Services, rt scheduler.Runtime, o SchedulerRuntimeOptions)

// RegisterSchedulerIdleWork registers the six O3/O5 tasks on d.Idle() and binds `d` into the
// runtime (in-package, no exported seam needed) so idle tasks can reach Daemon state.
func RegisterSchedulerIdleWork(d Daemon, rt scheduler.Runtime, o SchedulerRuntimeOptions) error

// FeaturesFrom translates observer signals into scheduler features. observer must not import
// scheduler (§3.2), so the translation lives here.
func FeaturesFrom(h *FeatureHistory, sig observer.Signals, tool string, ts core.UnixMilli) scheduler.Features
type FeatureHistory struct{ /* two sliding windows of paths, tools, text, and the prior TS */ }
func NewFeatureHistory(window int) *FeatureHistory

// SchedulerSnapshot backs /qompack:status (SP-14) and the Phase 4 harness (SP-02 fixtures).
type SchedulerSnapshot struct {
    LastDecision      scheduler.Decision
    FrontierTurn      core.TurnIndex
    ResidualTokens    core.Tokens
    MaxResidualTokens core.Tokens
    DeltaSeconds      float64
    DeltaSamples      int
    BurnRateTokensPerMin float64
    ChangepointTurns  []core.TurnIndex
    IdleSinceMS       int64
}
func SchedulerSnapshotOf(rt scheduler.Runtime) (SchedulerSnapshot, bool)

// CostRecorder is implemented by the concrete runtime. It is how a measured compaction
// wall-clock reaches the Young–Daly δ EWMA. SP-10's checkpoint op and SP-14's status
// command both reach it with a type assertion; nothing may assume it is present.
type CostRecorder interface {
    RecordCompactionCost(seconds float64)
}
```

```go
package l3policy // test/replay only — the eval.Policy this slice contributes

func New(cfg config.Config) eval.Policy // Name() == "qompack-l3"
```

---

## Implementation spec

### Global rules for this subplan

1. All work happens on `feat/sp12-scheduler-l3`, cut from `develop` with SP-01, SP-05, SP-06, SP-07 and SP-08 already merged. Do not branch from a sibling wave-3 branch and do not merge one in.
2. `internal/scheduler` imports **foundation packages only** (`core`, `paths`, `config`, `logging`, `obs`) per 00-ARCHITECTURE §3.2. It must not import `store`, `dag`, `checkpoint`, `daemon`, `observer` or `negknow`. The import-graph check in CI's `verify` job enforces this; a violation is a hard build failure, not a warning.
3. Every value in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and every integer in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` is read from `config`, never written as a literal outside `*_test.go`. `w/r` is computed; `12.5` never appears. Exactly six annotated exceptions exist in this slice, and no more may be added without an ADR entry: `HostAutoCompactBuffer`, `HostMaxOutputCap`, `HostDefaultContextWindow`, `HostDefaultMaxOutput` (host constants from §2.5, which Appendix C deliberately has no key for), `maxShingles` and `maxTurnHistory` (memory bounds, not config defaults). Each carries `//nomagic:allow <reason>` on its own line, following the precedent SP-05 set with `ringCapacity`.
4. Never modify `Qompack.md`.
5. `internal/scheduler` never performs I/O, never reads the clock, and holds no package-level mutable state except the single `PSelectionAvailable` atomic flag mandated by 00-ARCHITECTURE §5.12.

### File map

| Path | Action | Responsibility |
|---|---|---|
| `internal/scheduler/doc.go` | modify | package doc: purity contract, §8.4 quote, where `Runtime` lives |
| `internal/scheduler/types.go` | modify | add the two additive `Inputs` fields; add `TriggerReason`/`TTLState`/`BackgroundTask` constants |
| `internal/scheduler/thresholds.go` | create | `HostAutoCompactBuffer`, `SoftFloor`, `HardCeiling` |
| `internal/scheduler/ttl.go` | create | `ClassifyTTL`, `CacheFactor` — the E1 sliding-TTL idle model |
| `internal/scheduler/youngdaly.go` | create | `YoungDaly`, `mtbfSeconds`, δ precedence resolution |
| `internal/scheduler/skirental.go` | create | `SkiRentalShouldWrite` |
| `internal/scheduler/dropclass.go` | create | `DropClass`, `DropClassOf`, `EvictionRank` — the §2.2 tool table as pure spec |
| `internal/scheduler/bocd.go` | create | the BOCD `Detector`: Normal-Inverse-Gamma model, pruned update, binary state |
| `internal/scheduler/pselect.go` | create | candidate filtering, `score(p)`, argmax with TTL-aware tie-break |
| `internal/scheduler/evaluate.go` | create | the composite trigger, `Breakdown` assembly, `Background` planning |
| `internal/scheduler/gate.go` | create | `PSelectionAvailable` / `EnablePSelection` / `DisablePSelection` |
| `internal/scheduler/*_test.go` | create | unit + property + benchmark tests (see Test plan) |
| `internal/scheduler/schedulertest/suite.go` | modify | flip SP-01's `t.Skip`s; add behaviour cases |
| `internal/daemon/scheduler_droppable.go` | create | `ClassifyDrop` adapter, suffix-sum `reclaimableIndex` |
| `internal/daemon/scheduler_features.go` | create | `FeatureHistory`, `FeaturesFrom` |
| `internal/daemon/scheduler_candidates.go` | create | candidate assembly, turn→`Pos` map, `CrossingEdges` cache, cap-at-32 |
| `internal/daemon/scheduler_tap.go` | create | `WrapServicesForScheduler` — the `Options.Bind` decoration that feeds L0 events to L3 |
| `internal/daemon/scheduler_runtime.go` | create | `scheduler.Runtime` implementation, δ EWMA, burn rate, `SchedulerSnapshot` |
| `internal/daemon/scheduler_state.go` | create | `state/bocd.json` + `state/scheduler.json` codecs |
| `internal/daemon/scheduler_frontier.go` | create | O5: segment close/roll, `checkpoint.Writer.Advance`, residual accounting |
| `internal/daemon/scheduler_idle.go` | create | O3: `RegisterSchedulerIdleWork`, the six tasks |
| `internal/daemon/scheduler_*_test.go` | create | unit + property + benchmark tests |
| `cmd/qompack/main.go` | modify | three added blocks in the `daemon` subcommand: construct, `Bind`, register |
| `testdata/golden/scheduler/decision-{warm,expiring,cold}.json` | create | frozen `Decision` goldens (`Breakdown` key set + ordering) |
| `testdata/bench-baseline.txt` | modify | append the seven SP-12 benchmark baselines |
| `test/e2e/scheduler_idle_test.go` | create | `TestDaemonIdleRunsSchedulerWork` against a real daemon |
| `test/replay/l3policy/policy.go` | create | the `eval.Policy` named `qompack-l3` |
| `test/replay/l3policy/policy_test.go` | create | policy unit tests (determinism, keep-set shape) |
| `test/replay/phase4_test.go` | create | the Phase 4 exit-criterion harness |
| `docs/adr/0012-scheduler-l3.md` | create | ADR: the seven decisions listed in commit 7 |

---

### `internal/scheduler/thresholds.go`

```go
package scheduler

import (
    "github.com/qompack/qompack/internal/config"
    "github.com/qompack/qompack/internal/core"
)

// Claude Code's own trigger arithmetic (Qompack.md §2.5):
//   effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
//   autoCompactThreshold   = effectiveContextWindow − 13_000
// These describe the HOST, not Qompack tunables, so Appendix C has no key for any of them.
const (
    HostAutoCompactBuffer    core.Tokens = 13_000  //nomagic:allow host constant, Qompack.md §2.5
    HostMaxOutputCap         core.Tokens = 20_000  //nomagic:allow host constant, Qompack.md §2.5
    HostDefaultContextWindow core.Tokens = 200_000 //nomagic:allow host default, Qompack.md §2.5
    HostDefaultMaxOutput     core.Tokens = 32_000  //nomagic:allow host default, Qompack.md §2.5
)

// EffectiveWindow is §2.5 line 1. Non-positive inputs return 0, which makes Evaluate
// short-circuit with error_no_window rather than invent a window.
func EffectiveWindow(contextWindow, maxOutputTokens core.Tokens) core.Tokens {
    if contextWindow <= 0 {
        return 0
    }
    cap := maxOutputTokens
    if cap < 0 {
        cap = 0
    }
    if cap > HostMaxOutputCap {
        cap = HostMaxOutputCap
    }
    if ew := contextWindow - cap; ew > 0 {
        return ew
    }
    return 0
}

// SoftFloor is Qompack.md §8.4: "default 55% of effective window".
func SoftFloor(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens {
    if effectiveWindow <= 0 || cfg.SoftFloorPct <= 0 {
        return 0
    }
    return core.Tokens(cfg.SoftFloorPct * float64(effectiveWindow))
}

// HardCeiling is Qompack.md §8.4: "one turn's worth of headroom below Claude Code's
// threshold". The host threshold is effectiveWindow − HostAutoCompactBuffer (§2.5); the
// headroom is scheduler.hardCeilingMargin (Appendix C default 20000).
// Clamped to [1, effectiveWindow] so a pathological config can never invert the ordering.
func HardCeiling(effectiveWindow core.Tokens, cfg config.SchedulerCfg) core.Tokens {
    if effectiveWindow <= 0 {
        return 0
    }
    hc := effectiveWindow - HostAutoCompactBuffer - core.Tokens(cfg.HardCeilingMargin)
    if hc < 1 {
        hc = 1
    }
    if hc > effectiveWindow {
        hc = effectiveWindow
    }
    return hc
}
```

**Worked example (asserted by test).** `EffectiveWindow(200_000, 32_000) = 200_000 − min(32_000, 20_000) = 180_000` — §2.5's own worked example, reproduced exactly. `SoftFloor = 0.55 × 180_000 = 99_000`. `HardCeiling = 180_000 − 13_000 − 20_000 = 147_000`. The host's own threshold is `167_000`, so the plugin has 20 000 tokens — comfortably one large turn — to checkpoint first. This is the arithmetic that closes G1.2, G1.3 and G1.4 as far as a plugin can.

**Where the window numbers come from — decided, not left open.** There is no host API for the model's context window in wave 3, and a scheduler that cannot resolve one is inert. The Runtime resolves `contextWindow` and `maxOutputTokens` in this order, then writes which rung fired into `Breakdown["window_source"]` (`3` = env autocompact, `2` = explicit override, `1` = host default) **after `Evaluate` returns** — `Evaluate` stays pure and sees only the resulting number — so `/qompack:status` never presents a guess as a measurement:

1. `CLAUDE_CODE_AUTO_COMPACT_WINDOW` — the documented host env var of §2.5, clamped to its documented range `[100_000, 1_000_000]`. Read once per `SessionStart` through `config.Env.Getenv`, never `os.Getenv` directly, so tests can inject it.
2. `QOMPACK_CONTEXT_WINDOW` / `QOMPACK_MAX_OUTPUT_TOKENS` — explicit overrides for CI, replay and the bench harness. These are environment variables, not config keys: this slice adds no Appendix C key and therefore cannot break the `docs` gate.
3. `HostDefaultContextWindow` / `HostDefaultMaxOutput` (200 000 / 32 000), which reproduce §2.5's worked example.

`EffectiveWindow` is then applied to whichever pair won. Rung 1 supplies a window directly, so `maxOutputTokens` for that rung is `0` and the env value *is* the effective window.

**Failure modes.** `effectiveWindow <= 0` (caller did not supply it) ⇒ both return 0 and `Evaluate` short-circuits to a no-op `Decision` with `Breakdown["error_no_window"] = 1`. `SoftFloorPct` outside `(0,1)` never reaches here: `config.Validate` already replaced it with the default and logged Loud (00-ARCHITECTURE §11.3).

---

### `internal/scheduler/ttl.go` — the E1 sliding-TTL idle model

The correction in `Qompack.md` §8.4 is precise: track **time since last API call**, never time since last cache write. `Inputs` carries both; this file reads only `LastAPICallTS`. `LastCacheWriteTS` is retained on `Inputs` for observability and for SP-16's ski-rental work and is written into `Breakdown` but never into the decision.

```go
package scheduler

import "github.com/qompack/qompack/internal/core"

// ttlExpiringFraction is the fraction of the TTL after which the prefix is treated as
// "expiring" and the marginal value of the warm cache begins to decay linearly to zero
// (Qompack.md §8.4: "idle long enough that expiry is imminent … the same preference applies").
const ttlExpiringFraction = 0.5

// ClassifyTTL returns the cache state and the observed idle gap in seconds.
//   lastAPICallTS == 0                      → TTLUnknown, gap 0
//   gap <  0.5·ttl                          → TTLWarm
//   0.5·ttl <= gap < ttl                    → TTLExpiring
//   gap >= ttl                              → TTLCold   ("cache provably cold → cut is free")
func ClassifyTTL(now, lastAPICallTS core.UnixMilli, ttlSeconds int) (TTLState, float64) {
    if lastAPICallTS <= 0 || ttlSeconds <= 0 {
        return TTLUnknown, 0
    }
    gap := float64(now-lastAPICallTS) / 1000.0
    if gap < 0 {
        gap = 0
    }
    ttl := float64(ttlSeconds)
    switch {
    case gap >= ttl:
        return TTLCold, gap
    case gap >= ttlExpiringFraction*ttl:
        return TTLExpiring, gap
    default:
        return TTLWarm, gap
    }
}

// CacheFactor multiplies rewrite(p). Warm = 1 (pay in full), cold = 0 ("rewrite(p) → 0 for
// all p"), and the expiring band ramps linearly between them so the policy is continuous
// rather than a cliff. Unknown is conservative: assume warm.
func CacheFactor(state TTLState, idleGapSeconds float64, ttlSeconds int) float64 {
    switch state {
    case TTLCold:
        return 0
    case TTLExpiring:
        ttl := float64(ttlSeconds)
        f := (ttl - idleGapSeconds) / (ttl * (1 - ttlExpiringFraction))
        if f < 0 {
            return 0
        }
        if f > 1 {
            return 1
        }
        return f
    default: // TTLWarm, TTLUnknown
        return 1
    }
}
```

**Worked example (asserted by test).** `ttlSeconds = 300` (Appendix C). gap 10 s ⇒ warm, factor 1.0. gap 150 s ⇒ expiring, factor `(300−150)/150 = 1.0`. gap 225 s ⇒ expiring, factor `(300−225)/150 = 0.5`. gap 300 s ⇒ cold, factor 0.0. gap 4 h ⇒ cold, factor 0.0. The ramp is continuous at both endpoints.

---

### `internal/scheduler/youngdaly.go`

```go
package scheduler

import (
    "math"

    "github.com/qompack/qompack/internal/config"
    "github.com/qompack/qompack/internal/core"
)

// YoungDaly is Qompack.md §6.7 / Appendix A: I* = √(2·δ·M).
// Returns 0 when either term is unknown or non-positive, and 0 means "no cadence
// constraint" — the caller must treat a zero interval as a disabled clause, never as
// "elapsed > 0 is always true".
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64 {
    if deltaSeconds <= 0 || mtbfSeconds <= 0 ||
        math.IsNaN(deltaSeconds) || math.IsNaN(mtbfSeconds) ||
        math.IsInf(deltaSeconds, 0) || math.IsInf(mtbfSeconds, 0) {
        return 0
    }
    return math.Sqrt(2 * deltaSeconds * mtbfSeconds)
}

// mtbfSeconds is M: "expected time to forced compaction at the current burn rate" (§8.4).
// Forced compaction means crossing the hard ceiling, because that is the last position at
// which the plugin still gets to checkpoint first.
func mtbfSeconds(contextTokens, hardCeiling core.Tokens, burnTokensPerMin float64) float64 {
    headroom := float64(hardCeiling - contextTokens)
    if headroom <= 0 || burnTokensPerMin <= 0 {
        return 0
    }
    return headroom / burnTokensPerMin * 60.0
}

// resolveDelta implements the δ precedence rule.
//
//   1. cfg.YoungDaly.MeasuredDeltaSeconds non-nil  → explicit operator override, wins.
//   2. in.MeasuredDeltaSeconds non-nil             → the Runtime's measurement.
//   3. both nil                                    → unknown; the clause is disabled and
//      Breakdown["young_daly_delta_unmeasured"] = 1 is set by the caller.
//
// Appendix C's `null` means "measure at runtime", not zero (00-ARCHITECTURE §11.2), so a
// nil config value is NOT a zero δ and must never collapse the interval to 0 silently.
func resolveDelta(in Inputs, cfg config.SchedulerCfg) (float64, bool) {
    if cfg.YoungDaly.MeasuredDeltaSeconds != nil && *cfg.YoungDaly.MeasuredDeltaSeconds > 0 {
        return *cfg.YoungDaly.MeasuredDeltaSeconds, true
    }
    if in.MeasuredDeltaSeconds != nil && *in.MeasuredDeltaSeconds > 0 {
        return *in.MeasuredDeltaSeconds, true
    }
    return 0, false
}
```

**Worked example (asserted by test).** δ = 20 s (a measured summarization call, mid-range of the §6.7 "15–40 s" band), context 120 000, hard ceiling 147 000, burn 900 tokens/min ⇒ `M = 27 000/900 × 60 = 1 800 s`; `I* = √(2 × 20 × 1 800) = √72 000 = 268.328…` s. So the scheduler will fire on the first changepoint roughly 4.5 minutes after the last compaction, rather than waiting for a token count.

---

### `internal/scheduler/skirental.go`

```go
package scheduler

// SkiRentalShouldWrite is Qompack.md §5.6 / Appendix A:
//   write when E[remaining reads] > w/r
// The threshold is COMPUTED from config, never written as 12.5 (00-ARCHITECTURE §11.6).
// Nothing in SP-12 calls this; SP-16 applies the policy. It lives here because
// 00-ARCHITECTURE §5.13 places the signature in this package.
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
    if r <= 0 || w <= 0 {
        return false
    }
    return expectedReads > w/r
}
```

---

### `internal/scheduler/gate.go`

```go
package scheduler

import "sync/atomic"

// pSelectionAvailable is the ship-order guard of Qompack.md's closing note 3 and
// 00-ARCHITECTURE §5.12: analyzer.NewSelector refuses to construct while it is false, so
// submodular selection over a scattered keep-set is structurally inert until a real
// scheduler Runtime exists. This is the only package-level mutable state in `scheduler`.
var pSelectionAvailable atomic.Bool

// PSelectionAvailable reports whether p-selection is live for this process.
func PSelectionAvailable() bool { return pSelectionAvailable.Load() }

// EnablePSelection is called by daemon.NewSchedulerRuntime after the runtime has verified
// it can assemble candidates (non-nil store, graph and segment log).
func EnablePSelection() { pSelectionAvailable.Store(true) }

// DisablePSelection is called on runtime shutdown and by tests. Tests MUST defer it.
func DisablePSelection() { pSelectionAvailable.Store(false) }
```

---

### `internal/scheduler/bocd.go` — Bayesian online changepoint detection

Model: Adams & MacKay (2007) run-length posterior with a constant hazard `H = hazardRate`, one **Normal-Inverse-Gamma** conjugate observation model per feature, features assumed independent so the joint predictive is the product. Predictive is Student-t. Updates are pruned so the posterior length is bounded, which is what §6.6's "O(1) amortized with pruning" means operationally: per-observation cost is `O(len × F)` with `len` bounded by a hard cap and, in practice, by the mass-bearing prefix — it does **not** grow with the number of observations.

```go
package scheduler

const (
    bocdFormatVersion uint16 = 1

    bocdMaxRunLength         = 512   // hard cap on posterior length
    bocdPruneEpsilon         = 1e-4  // tail entries below this are dropped
    bocdMinPosteriorLen      = 2     // never prune below this
    bocdMinTurnsBetweenDecls = 3     // hysteresis: no changepoint storms on a plateau
    bocdChangepointThreshold = 0.5   // declare when P(r_t = 0) exceeds this
    bocdMaxFeatures          = 8     // deserialization bound (§13: no unbounded allocation)

    // Normal-Inverse-Gamma prior. mu0 = 0.5 because every mapped feature is in [0,1]
    // (see featureVector); kappa0/alpha0/beta0 = 1 is the standard weakly-informative choice.
    bocdPriorMu    = 0.5
    bocdPriorKappa = 1.0
    bocdPriorAlpha = 1.0
    bocdPriorBeta  = 1.0

    // gapReferenceSeconds compresses an unbounded inter-turn gap into [0,1] via
    // log1p(gap)/log1p(ref). 600 s is ten minutes: a gap that long is unambiguously a
    // task boundary and saturates the feature. NewBOCD's signature is fixed by
    // 00-ARCHITECTURE §5.13 and carries no idle config, so this scale lives here.
    gapReferenceSeconds = 600.0
)

// knownFeatures maps the Appendix C `changepoint.features` names onto Features fields.
// "lexical" is supported but not in the Appendix C default list.
var knownFeatures = map[string]struct{}{
    "paths": {}, "tools": {}, "lexical": {}, "time": {}, "todos": {},
}
var defaultFeatures = []string{"paths", "tools", "time", "todos"}

type ngParams struct{ Mu, Kappa, Alpha, Beta float64 }

type bocd struct {
    hazard   float64
    features []string
    post     []float64    // post[r] = P(run length == r), normalized, len >= 1
    stats    [][]ngParams // stats[r][f]
    lgNum    []float64    // lgamma((2·alpha0 + r + 1)/2), precomputed
    lgDen    []float64    // lgamma((2·alpha0 + r)/2),     precomputed
    since    int          // turns since the last declared changepoint
    last     ChangepointState
}

func NewBOCD(hazardRate float64, features []string) Detector {
    h := hazardRate
    if !(h > 0) || h >= 1 || math.IsNaN(h) {
        h = 1.0 / float64(bocdMaxRunLength) // safe fallback; config.Validate normally prevents this
    }
    sel := make([]string, 0, len(features))
    for _, f := range features {
        if _, ok := knownFeatures[f]; ok {
            sel = append(sel, f)
        }
    }
    if len(sel) == 0 {
        sel = append(sel, defaultFeatures...)
    }
    b := &bocd{hazard: h, features: sel}
    b.buildLgammaTables()
    b.Reset()
    return b
}

func (b *bocd) buildLgammaTables() {
    n := bocdMaxRunLength + 2
    b.lgNum = make([]float64, n)
    b.lgDen = make([]float64, n)
    for r := 0; r < n; r++ {
        // A row that has absorbed r observations has alpha = alpha0 + r/2 exactly.
        alpha := bocdPriorAlpha + float64(r)/2
        nu := 2 * alpha
        b.lgNum[r], _ = math.Lgamma((nu + 1) / 2)
        b.lgDen[r], _ = math.Lgamma(nu / 2)
    }
}

func (b *bocd) priorRow() []ngParams {
    row := make([]ngParams, len(b.features))
    for i := range row {
        row[i] = ngParams{bocdPriorMu, bocdPriorKappa, bocdPriorAlpha, bocdPriorBeta}
    }
    return row
}

func (b *bocd) Reset() {
    b.post = []float64{1}
    b.stats = [][]ngParams{b.priorRow()}
    b.since = bocdMinTurnsBetweenDecls // allow the first genuine changepoint to fire
    b.last = ChangepointState{RunLength: 0, ProbChangepoint: 1, AtChangepoint: false,
        Posterior: []float64{1}}
}
```

**Feature mapping.** Every mapped component is in `[0,1]`, oriented so that **higher means more boundary-like**. Orientation does not affect BOCD (it detects distribution shift either way) but a single documented convention keeps the priors meaningful.

```go
func (b *bocd) featureVector(f Features) []float64 {
    out := make([]float64, len(b.features))
    for i, name := range b.features {
        switch name {
        case "paths":
            out[i] = clamp01(1 - f.PathJaccard)   // path NOVELTY
        case "tools":
            out[i] = clamp01(f.ToolShift)         // total-variation shift, already 0..1
        case "lexical":
            out[i] = clamp01(1 - f.LexicalCohesion)
        case "time":
            g := f.GapSeconds
            if g < 0 || math.IsNaN(g) {
                g = 0
            }
            out[i] = clamp01(math.Log1p(g) / math.Log1p(gapReferenceSeconds))
        case "todos":
            out[i] = clamp01(f.TodoTransition)
        }
    }
    return out
}

func clamp01(x float64) float64 {
    if math.IsNaN(x) || x < 0 { return 0 }
    if x > 1 { return 1 }
    return x
}
```

**The update.**

```go
// logPredictive is the Student-t posterior predictive of a Normal-Inverse-Gamma model:
//   nu = 2a, scale² = b(k+1)/(a·k)
//   log p = lgamma((nu+1)/2) − lgamma(nu/2) − ½·log(nu·π·scale²)
//           − ((nu+1)/2)·log1p((x−mu)²/(nu·scale²))
// The two lgamma terms depend only on the run index r, so they are table lookups.
func (b *bocd) logPredictive(r int, x float64, p ngParams) float64 {
    nu := 2 * p.Alpha
    scale2 := p.Beta * (p.Kappa + 1) / (p.Alpha * p.Kappa)
    d := x - p.Mu
    return b.lgNum[r] - b.lgDen[r] -
        0.5*math.Log(nu*math.Pi*scale2) -
        ((nu+1)/2)*math.Log1p(d*d/(nu*scale2))
}

// ngUpdate absorbs one observation into a Normal-Inverse-Gamma row.
func ngUpdate(p ngParams, x float64) ngParams {
    k1 := p.Kappa + 1
    d := x - p.Mu
    return ngParams{
        Mu:    (p.Kappa*p.Mu + x) / k1,
        Kappa: k1,
        Alpha: p.Alpha + 0.5,
        Beta:  p.Beta + p.Kappa*d*d/(2*k1),
    }
}

func (b *bocd) Observe(f Features) ChangepointState {
    x := b.featureVector(f)
    n := len(b.post)

    // 1. predictive probability of x under each run-length hypothesis
    pred := make([]float64, n)
    for r := 0; r < n; r++ {
        lp := 0.0
        for i := range x {
            lp += b.logPredictive(r, x[i], b.stats[r][i])
        }
        pred[r] = math.Exp(lp)
    }

    // 2. growth + changepoint messages (Adams & MacKay eq. 1–3)
    grown := make([]float64, n+1)
    cp := 0.0
    for r := 0; r < n; r++ {
        joint := b.post[r] * pred[r]
        grown[r+1] = joint * (1 - b.hazard)
        cp += joint * b.hazard
    }
    grown[0] = cp

    // 3. normalize; a degenerate posterior (all-zero / NaN / Inf) is a numerical failure and
    //    the only correct response is to restart the run-length distribution.
    sum := 0.0
    for _, v := range grown {
        sum += v
    }
    if !(sum > 0) || math.IsNaN(sum) || math.IsInf(sum, 0) {
        b.Reset()
        return b.last
    }
    for i := range grown {
        grown[i] /= sum
    }

    // 4. sufficient statistics: row r+1 absorbs x into row r; row 0 is the fresh prior.
    ns := make([][]ngParams, n+1)
    ns[0] = b.priorRow()
    for r := 0; r < n; r++ {
        row := make([]ngParams, len(x))
        for i := range x {
            row[i] = ngUpdate(b.stats[r][i], x[i])
        }
        ns[r+1] = row
    }
    b.post, b.stats = grown, ns

    // 5. prune — this is what makes the update amortized O(1) rather than O(t)
    b.prune()

    // 6. declare
    runLength := argmaxIdx(b.post)
    prob := b.post[0]
    declared := prob > bocdChangepointThreshold && b.since >= bocdMinTurnsBetweenDecls
    if declared {
        b.since = 0
    } else {
        b.since++
    }
    post := make([]float64, len(b.post))
    copy(post, b.post)
    b.last = ChangepointState{
        RunLength:       runLength,
        ProbChangepoint: prob,
        AtChangepoint:   declared,
        Posterior:       post,
    }
    return b.last
}

func (b *bocd) prune() {
    if len(b.post) > bocdMaxRunLength {
        b.post, b.stats = b.post[:bocdMaxRunLength], b.stats[:bocdMaxRunLength]
    }
    end := len(b.post)
    for end > bocdMinPosteriorLen && b.post[end-1] < bocdPruneEpsilon {
        end--
    }
    b.post, b.stats = b.post[:end], b.stats[:end]
    s := 0.0
    for _, v := range b.post {
        s += v
    }
    if s > 0 {
        for i := range b.post {
            b.post[i] /= s
        }
    }
}

// argmaxIdx returns the first index holding the maximum — deterministic on ties.
func argmaxIdx(xs []float64) int {
    best, bi := math.Inf(-1), 0
    for i, v := range xs {
        if v > best {
            best, bi = v, i
        }
    }
    return bi
}

func (b *bocd) State() ChangepointState { return b.last }
```

**Why this is O(1) amortized.** With hazard `H`, the run-length posterior concentrates around `1/H` (250 at Appendix C's `0.004`) and its tail decays geometrically, so the mass-bearing prefix that survives `bocdPruneEpsilon` is bounded independently of `t`. The hard cap `bocdMaxRunLength = 512` makes the bound unconditional. Truncation is a re-slice (O(1)); renormalization is O(len). Per-observation cost is therefore `O(min(len, 512) × F)` and never grows with session length. A test asserts `len(Posterior) <= 512` and mean length `< 64` over 5 000 observations of a piecewise-stationary series.

**Binary state format (`MarshalBinary`), byte-for-byte, little-endian throughout:**

```
offset  size                          field
0       4                             magic  'Q','P','K','B'
4       2   uint16                    version = 1
6       2   uint16                    featureCount F (1..8)
8       8   float64 (math.Float64bits) hazardRate
16      4   uint32                    runCount R (1..512)
20      4   uint32                    since   (turns since last declaration)
24      1   uint8                     declared (0|1, mirrors last.AtChangepoint)
25      3                             reserved, must be zero
28      var F × { uint8 nameLen; nameLen bytes UTF-8 }
...     8×R float64                   posterior, R entries
...     8×R×F×4 float64               stats, row-major: r outer, f inner, (Mu,Kappa,Alpha,Beta)
end−4   4   uint32                    CRC32C (Castagnoli) over bytes [0, end−4)
```

`UnmarshalBinary` validates, in order: length ≥ 32; magic; `version == 1`; `1 <= F <= 8`; `1 <= R <= 512`; every feature name is in `knownFeatures`; the declared total length matches the buffer length **exactly**; CRC32C matches. Any failure returns `fmt.Errorf("scheduler: corrupt BOCD state: %s: %w", detail, core.ErrNotFound)` and leaves the receiver untouched — the caller (`daemon`) then logs `Loud` and calls `Reset()`, exactly as `sketch.Load` does for a corrupt sketch (00-ARCHITECTURE §5.7). Bounds are checked **before** any allocation, closing the §13 "no unbounded allocation reachable from untrusted input" invariant. After a successful unmarshal, `lgNum`/`lgDen` are rebuilt and `last` is recomputed from the loaded posterior.

Maximum serialized size: `28 + 8×(1+10) + 8×512 + 8×512×8×4 = ~135 KB`. `state/bocd.json` base64-encodes it, so the file is ≤ ~180 KB.

---

### `internal/scheduler/pselect.go`

```go
package scheduler

import (
    "sort"

    "github.com/qompack/qompack/internal/config"
    "github.com/qompack/qompack/internal/core"
)

// maxScoredCandidates bounds the argmax sweep. Qompack.md §5.4: "Twenty candidates, not
// 167,000." The Runtime already caps its own assembly; this is defence in depth so a
// misbehaving caller cannot make Evaluate super-linear.
const maxScoredCandidates = 32

type scored struct {
    c          Candidate
    tail       float64 // max(0, n − Pos): the rewritten suffix length in tokens
    reclaim    float64
    rewrite    float64
    distortion float64
    score      float64
}

// eligible implements "candidates = changepoint boundaries ∩ API-round boundaries" (§8.4).
// Every Candidate the Runtime emits already sits on a changepoint boundary; RoundBoundary
// carries the second half of the intersection. If the intersection is empty we relax to the
// full set rather than refusing to cut — refusing would leave the session with no legal p
// exactly when the trigger says it must act — and record the relaxation in Breakdown.
func eligible(cands []Candidate) (out []Candidate, relaxed bool) {
    for _, c := range cands {
        if c.RoundBoundary {
            out = append(out, c)
        }
    }
    if len(out) == 0 && len(cands) > 0 {
        return append([]Candidate(nil), cands...), true
    }
    return out, false
}

// scoreCandidates implements §8.4 verbatim:
//     reclaimable(p) = Σ tokens of droppable blocks after p   (supplied by the Runtime)
//     rewrite(p)     = w · (n − p)      × cacheFactor         ("0 if cache cold or expiring")
//     distortion(p)  = λ · segment_coupling(p)
//     score(p)       = reclaimable(p)·r − rewrite(p) − distortion(p)
// r and w come from cfg. They are never literals (00-ARCHITECTURE §11.6).
func scoreCandidates(cands []Candidate, n core.Tokens, cacheFactor, lambda float64,
    cfg config.SchedulerCfg) []scored {

    r := cfg.Cache.ReadMultiplier
    w := cfg.Cache.WriteMultiplier
    out := make([]scored, 0, len(cands))
    for _, c := range cands {
        tail := float64(n) - float64(c.Pos)
        if tail < 0 {
            tail = 0
        }
        reclaim := float64(c.ReclaimableTokens) * r
        rewrite := w * tail * cacheFactor
        dist := 0.0
        if lambda > 0 {
            dist = lambda * float64(c.Coupling)
        }
        out = append(out, scored{c: c, tail: tail, reclaim: reclaim, rewrite: rewrite,
            distortion: dist, score: reclaim - rewrite - dist})
    }
    return out
}

// chooseP is the argmax. Tie-breaking encodes §5.4's bimodality explicitly:
//   • cold cache AND cfg.Idle.DeepCutWhenCold → prefer the SMALLEST Pos (deep cut is free)
//   • otherwise                                → prefer the LARGEST Pos (edit as late as possible)
// Ties are compared with an exact float equality after both scores are finite; scores are
// produced by the same arithmetic on the same machine, so exact comparison is correct here.
func chooseP(ss []scored, ttl TTLState, cfg config.SchedulerCfg) (scored, bool) {
    if len(ss) == 0 {
        return scored{}, false
    }
    deep := ttl == TTLCold && cfg.Idle.DeepCutWhenCold
    best := ss[0]
    for _, s := range ss[1:] {
        switch {
        case s.score > best.score:
            best = s
        case s.score == best.score:
            if (deep && s.c.Pos < best.c.Pos) || (!deep && s.c.Pos > best.c.Pos) {
                best = s
            }
        }
    }
    return best, true
}

// prepareCandidates sorts ascending by Pos, caps the set to the highest-Pos
// maxScoredCandidates, and reports whether reclaimable(p) violated §5.4's monotonicity
// ("Reclaimable tokens are non-increasing in p"). A violation is a Runtime bug, not a
// reason to refuse to decide, so it is surfaced in Breakdown and the sweep continues.
func prepareCandidates(cands []Candidate) (out []Candidate, nonMonotonic bool) {
    out = append([]Candidate(nil), cands...)
    sort.SliceStable(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
    for i := 1; i < len(out); i++ {
        if out[i].ReclaimableTokens > out[i-1].ReclaimableTokens {
            nonMonotonic = true
            break
        }
    }
    if len(out) > maxScoredCandidates {
        out = out[len(out)-maxScoredCandidates:]
    }
    return out, nonMonotonic
}
```

**Why the sign behaviour is correct and intended.** With Appendix C's `r = 0.1`, `w = 1.25` and a warm cache, `reclaimable(p) ≤ n − p`, so `score(p) ≤ (n−p)(r − w) = −1.15(n−p) < 0` and the argmax is the *latest* boundary. With a cold cache `cacheFactor = 0`, `rewrite` vanishes and the argmax is the *earliest* boundary with high reclaim and low coupling. That is exactly the bimodality §5.4 describes — "edit as late as possible, or edit when the cache is cold and rebuild everything" — falling out of the formula rather than being special-cased. Two tests assert both limbs. `Evaluate` never treats a negative `PScore` as a veto: the trigger decides *whether*, p-selection decides *where given that you are compacting*.

---

### `internal/scheduler/evaluate.go` — the composite trigger

```go
package scheduler

// Evaluate is a PURE function of Inputs: no I/O, no clock, no globals, no mutation of the
// argument. Calling it twice with the same Inputs returns byte-identical Decisions. This is
// what makes the whole of Qompack.md §8.4 unit-testable and replayable (00-ARCHITECTURE §5.13).
func Evaluate(in Inputs) Decision {
    cfg := in.Cfg
    d := Decision{Breakdown: make(map[string]float64, 24), TTL: TTLUnknown}

    if in.EffectiveWindow <= 0 {
        d.Breakdown["error_no_window"] = 1
        return d
    }

    // ── thresholds ────────────────────────────────────────────────────────
    soft := SoftFloor(in.EffectiveWindow, cfg)
    hard := HardCeiling(in.EffectiveWindow, cfg)
    d.SoftFloorTokens, d.HardCeilingTokens = soft, hard
    n := in.ContextTokens

    // ── cache state (E1: time since last API CALL, never last cache write) ─
    ttl, gap := ClassifyTTL(in.Now, in.LastAPICallTS, cfg.Cache.TTLSeconds)
    cf := CacheFactor(ttl, gap, cfg.Cache.TTLSeconds)
    d.TTL = ttl

    // ── Young–Daly cadence ────────────────────────────────────────────────
    var elapsed, interval float64
    delta, haveDelta := resolveDelta(in, cfg)
    m := mtbfSeconds(n, hard, in.BurnRateTokensPerMin)
    if !cfg.YoungDaly.Enabled {
        d.Breakdown["young_daly_disabled"] = 1
    } else if !haveDelta {
        d.Breakdown["young_daly_delta_unmeasured"] = 1
    } else if in.LastCompactionTS <= 0 {
        d.Breakdown["young_daly_no_baseline"] = 1
    } else {
        interval = YoungDaly(delta, m)
        elapsed = float64(in.Now-in.LastCompactionTS) / 1000.0
    }
    d.YoungDalySeconds = interval

    // ── the composite trigger, verbatim from §8.4 ─────────────────────────
    //   should_compact = tokens > soft_floor
    //                    AND ( at_changepoint
    //                          OR elapsed > young_daly_interval
    //                          OR tokens > hard_ceiling
    //                          OR idle_gap > ttl )
    aboveSoftFloor := n > soft
    atChangepoint := in.Changepoint.AtChangepoint
    youngDalyElapsed := interval > 0 && elapsed > interval
    aboveHardCeiling := n > hard
    idleColdCache := ttl == TTLCold

    if aboveSoftFloor {
        d.Reasons = append(d.Reasons, ReasonSoftFloor)
        if atChangepoint {
            d.Reasons = append(d.Reasons, ReasonChangepoint)
        }
        if youngDalyElapsed {
            d.Reasons = append(d.Reasons, ReasonYoungDaly)
        }
        if aboveHardCeiling {
            d.Reasons = append(d.Reasons, ReasonHardCeiling)
        }
        if idleColdCache {
            d.Reasons = append(d.Reasons, ReasonIdleColdCache)
        }
    }
    fired := aboveSoftFloor &&
        (atChangepoint || youngDalyElapsed || aboveHardCeiling || idleColdCache)

    // ── p-selection ───────────────────────────────────────────────────────
    cands, nonMono := prepareCandidates(in.Candidates)
    if nonMono {
        d.Breakdown["reclaimable_nonmonotonic"] = 1
    }
    elig, relaxed := eligible(cands)
    if relaxed {
        d.Breakdown["round_boundary_relaxed"] = 1
    }
    ss := scoreCandidates(elig, n, cf, in.CouplingLambda, cfg)
    best, ok := chooseP(ss, ttl, cfg)

    switch {
    case !fired:
        d.ShouldCompact = false
    case !ok:
        // The trigger fired but there is no legal cut point. Do NOT claim a compaction the
        // scheduler cannot place; escalate instead so /qompack:status shows the pressure.
        d.ShouldCompact = false
        d.Breakdown["no_candidates"] = 1
    default:
        d.ShouldCompact = true
        d.P, d.PScore = best.c, best.score
        d.Breakdown["reclaimable"] = best.reclaim
        d.Breakdown["rewrite"] = best.rewrite
        d.Breakdown["distortion"] = best.distortion
        d.Breakdown["score"] = best.score
        d.Breakdown["p"] = float64(best.c.Pos)
        d.Breakdown["p_turn"] = float64(best.c.Turn)
        d.Breakdown["p_segment"] = float64(best.c.SegmentID)
        d.Breakdown["coupling"] = float64(best.c.Coupling)
        d.Breakdown["reclaimable_tokens"] = float64(best.c.ReclaimableTokens)
        d.Breakdown["rewrite_tokens"] = best.tail * cf // clamped tail, never negative
    }

    // ── urgency ───────────────────────────────────────────────────────────
    switch {
    case aboveHardCeiling:
        d.Urgency = UrgencyNow
    case fired:
        d.Urgency = UrgencyAdvisory
    default:
        d.Urgency = UrgencyNone
    }

    // ── O3 background plan ────────────────────────────────────────────────
    d.Background = planBackground(in, ttl, soft, haveDelta)

    // ── observability: every number /qompack:status and eval need ─────────
    d.Breakdown["context_tokens"] = float64(n)
    d.Breakdown["effective_window"] = float64(in.EffectiveWindow)
    d.Breakdown["soft_floor"] = float64(soft)
    d.Breakdown["hard_ceiling"] = float64(hard)
    d.Breakdown["idle_gap_seconds"] = gap
    d.Breakdown["cache_factor"] = cf
    d.Breakdown["read_multiplier"] = cfg.Cache.ReadMultiplier
    d.Breakdown["write_multiplier"] = cfg.Cache.WriteMultiplier
    d.Breakdown["lambda"] = in.CouplingLambda
    d.Breakdown["delta_seconds"] = delta
    d.Breakdown["mtbf_seconds"] = m
    d.Breakdown["young_daly_seconds"] = interval
    d.Breakdown["elapsed_seconds"] = elapsed
    d.Breakdown["burn_rate_tokens_per_min"] = in.BurnRateTokensPerMin
    d.Breakdown["prob_changepoint"] = in.Changepoint.ProbChangepoint
    d.Breakdown["run_length"] = float64(in.Changepoint.RunLength)
    d.Breakdown["candidates"] = float64(len(ss))
    d.Breakdown["candidates_supplied"] = float64(len(in.Candidates))
    d.Breakdown["frontier_turn"] = float64(in.FrontierTurn)
    d.Breakdown["residual_tokens"] = float64(in.ResidualTokens)
    d.Breakdown["last_cache_write_age_seconds"] = ageSeconds(in.Now, in.LastCacheWriteTS)
    return d
}

func ageSeconds(now, then core.UnixMilli) float64 {
    if then <= 0 {
        return 0
    }
    a := float64(now-then) / 1000.0
    if a < 0 {
        return 0
    }
    return a
}

// planBackground is the O3 plan (§8.4 "Idle-time background work"). It is pure: the set is
// derived from Inputs alone, and the Runtime's registered tasks consult it before running.
func planBackground(in Inputs, ttl TTLState, soft core.Tokens, haveDelta bool) []BackgroundTask {
    if !in.Cfg.Idle.BackgroundWork {
        return nil
    }
    var out []BackgroundTask
    if in.ResidualTokens > 0 {
        out = append(out, TaskAdvanceFrontier) // O5 — always first: it is the latency lever
    }
    if in.ContextTokens > soft {
        out = append(out, TaskPrecomputeSlice)
    }
    if !haveDelta {
        out = append(out, TaskRefreshDelta)
    }
    if ttl == TTLCold {
        // A gap longer than the TTL is the "next idle window" §8.3 names for the bloom
        // rebuild, and the only moment at which DAG compaction and GC cost nothing.
        out = append(out, TaskRebuildBloom, TaskCompactDAG, TaskGC)
    }
    return out
}
```

**Ordering is normative.** `Reasons` is always emitted in the order `soft_floor, changepoint, young_daly, hard_ceiling, idle_cold_cache`. `Background` is always emitted in the order `advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc`. Both are asserted by test so `/qompack:status` output and replay reports are byte-stable.

**Failure modes.**

| Condition | Behaviour |
|---|---|
| `EffectiveWindow <= 0` | zero `Decision`, `Breakdown["error_no_window"]=1`, no panic |
| `Candidates` empty or all filtered out | `ShouldCompact=false`, `Breakdown["no_candidates"]=1`, `Urgency` still escalates |
| No `RoundBoundary` candidate | relax to the full set, `Breakdown["round_boundary_relaxed"]=1` |
| `CouplingLambda <= 0` | distortion term is 0; `Breakdown["lambda"]=0` records it |
| δ unknown from both sources | Young–Daly clause disabled, `Breakdown["young_daly_delta_unmeasured"]=1` |
| `LastCompactionTS == 0` | Young–Daly clause disabled, `Breakdown["young_daly_no_baseline"]=1` |
| `BurnRateTokensPerMin <= 0` or headroom ≤ 0 | `M = 0` ⇒ `interval = 0` ⇒ clause disabled |
| `LastAPICallTS == 0` | `TTLUnknown`, `CacheFactor = 1` (conservatively warm) |
| `ReclaimableTokens` non-monotone in `Pos` | flagged, sweep continues |

**Performance budget.** `BenchmarkEvaluate` with 64 supplied candidates and a 4-feature `ChangepointState`: **≤ 50 µs/op, ≤ 8 allocations/op**. `Evaluate` is off the B-A hot path entirely — it runs on the daemon's idle worker and on the `status` op — but 00-ARCHITECTURE §7 names it as a tracked micro-benchmark and `benchstat` fails the build on a >25% regression.

---

### `internal/scheduler/dropclass.go` — the droppable-block classification

This is the classification behind `reclaimable(p)`. It must exist in wave 3 for p-selection to mean anything; `analyzer` (SP-15, wave 4) later refines *which* droppable blocks to keep, never *whether* they are droppable. It lives in `scheduler` because it is pure spec — no import beyond `strings` — and because `test/replay/l3policy` must score candidates without importing the `daemon` composition root (00-ARCHITECTURE §3.2). `daemon.ClassifyDrop` is the adapter.

```go
package scheduler

import "strings"

type DropClass uint8

const (
    DropNone       DropClass = iota // not droppable
    DropOrdinary                    // §2.2 compactable tool set
    DropSuperseded                  // §8.1 item 3 "first candidates for eviction"
    DropEphemeral                   // §8.7 "the FIRST eviction candidate, ahead of ordinary"
)

// EvictionRank orders the §8.4 droppable ranking: ephemeral > superseded > ordinary > none.
func (c DropClass) EvictionRank() int { return int(c) }

// compactableTools is Qompack.md §2.2 verbatim:
//   "FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite"
// plus the wire names Claude Code actually sends for the same operations. Comparison is
// case-folded. "Only high-volume, reproducible results are targeted."
var compactableTools = map[string]struct{}{
    "fileread": {}, "read": {},
    "bash": {}, "powershell": {},
    "grep": {}, "glob": {},
    "websearch": {}, "webfetch": {},
    "fileedit": {}, "edit": {}, "multiedit": {},
    "filewrite": {}, "write": {},
}

// preservedTools is the other half of §2.2: "AgentTool and MCP results are preserved."
var preservedTools = map[string]struct{}{
    "agenttool": {}, "agent": {}, "task": {},
}

// DropClassOf assigns a droppable class. Order matters: ephemeral first, then superseded,
// then ordinary — this IS the §8.7 eviction order.
func DropClassOf(tool string, ephemeral, superseded bool) DropClass {
    if ephemeral {
        return DropEphemeral
    }
    if superseded {
        return DropSuperseded
    }
    name := strings.ToLower(strings.TrimSpace(tool))
    if strings.HasPrefix(name, "mcp__") {
        return DropNone
    }
    if _, ok := preservedTools[name]; ok {
        return DropNone
    }
    if _, ok := compactableTools[name]; ok {
        return DropOrdinary
    }
    return DropNone
}
```

**Note on ordering.** A *superseded* MCP result or a superseded `Task` result is still droppable: supersession is a stronger statement than the tool class ("Superseded reads … should never appear in a summary", §8.1 item 3). The switch order above encodes that deliberately, and a test pins it.

---

### `internal/daemon/scheduler_droppable.go` — the store adapter and the suffix-sum index

```go
package daemon

import (
    "sort"

    "github.com/qompack/qompack/internal/core"
    "github.com/qompack/qompack/internal/scheduler"
    "github.com/qompack/qompack/internal/store"
)

type DropClass = scheduler.DropClass

const (
    DropNone       = scheduler.DropNone
    DropOrdinary   = scheduler.DropOrdinary
    DropSuperseded = scheduler.DropSuperseded
    DropEphemeral  = scheduler.DropEphemeral
)

// ClassifyDrop is the whole adapter: the classification rules live in `scheduler` so
// `test/replay/l3policy` shares them without importing this composition root.
func ClassifyDrop(rec store.ToolUseRecord) DropClass {
    return scheduler.DropClassOf(rec.Tool, rec.Ephemeral, rec.Status == store.StatusSuperseded)
}
```

**Suffix-sum reclaimable index.** Computing `reclaimable(p)` per candidate by scanning all records would be `O(N × C)`. A single ascending sweep gives `O(N log N + C log N)` and makes §5.4's monotonicity structural rather than hoped-for.

```go
type dropBlock struct {
    Pos    int
    Tokens core.Tokens
    Class  DropClass
}

// reclaimableIndex answers "Σ tokens of droppable blocks after p" in O(log N).
type reclaimableIndex struct {
    pos    []int          // ascending, deduplicated is NOT required
    suffix []core.Tokens  // suffix[i] = Σ tokens of blocks[i:]
    total  core.Tokens
    counts [4]int         // per-DropClass block counts, for /qompack:status
}

func newReclaimableIndex(blocks []dropBlock) *reclaimableIndex {
    kept := blocks[:0:0]
    idx := &reclaimableIndex{}
    for _, b := range blocks {
        idx.counts[b.Class]++
        if b.Class == DropNone || b.Tokens <= 0 {
            continue
        }
        kept = append(kept, b)
    }
    sort.SliceStable(kept, func(i, j int) bool { return kept[i].Pos < kept[j].Pos })
    idx.pos = make([]int, len(kept))
    idx.suffix = make([]core.Tokens, len(kept)+1)
    for i, b := range kept {
        idx.pos[i] = b.Pos
    }
    for i := len(kept) - 1; i >= 0; i-- {
        idx.suffix[i] = idx.suffix[i+1] + kept[i].Tokens
    }
    idx.total = idx.suffix[0]
    return idx
}

// After returns Σ tokens of droppable blocks at position >= p. Non-increasing in p by
// construction, which is exactly Qompack.md §5.4's monotonicity property.
func (r *reclaimableIndex) After(p int) core.Tokens {
    i := sort.SearchInts(r.pos, p)
    return r.suffix[i]
}
```

---

### `internal/daemon/scheduler_features.go`

`observer` must not import `scheduler` (00-ARCHITECTURE §3.2), so the translation from `observer.Signals` to `scheduler.Features` lives in the composition root and is owned by SP-12.

```go
package daemon

const defaultFeatureWindow = 8 // turns per sliding window

// maxShingles bounds the lexical-cohesion allocation. 4096 is in §11.6's forbidden integer
// set, so the annotation is mandatory — it is a memory bound, not a config default.
const maxShingles = 4096 //nomagic:allow bounded-allocation cap, not an Appendix C default

type FeatureHistory struct {
    window int
    recent turnWindow // most recent `window` turns
    prior  turnWindow // the `window` turns before those
    lastTS core.UnixMilli
}
type turnWindow struct {
    paths []map[string]struct{} // paths.Key form, one set per turn
    tools []string
    text  []string
}

func NewFeatureHistory(window int) *FeatureHistory // window <= 0 ⇒ defaultFeatureWindow

// FeaturesFrom appends one turn's observation and returns the current feature vector.
// It mutates h and is not safe for concurrent use; the Runtime holds the lock.
func FeaturesFrom(h *FeatureHistory, sig observer.Signals, tool string, ts core.UnixMilli) scheduler.Features
```

Definitions, each asserted by a table test:

| Feature | Definition |
|---|---|
| `PathJaccard` | `|R ∩ P| / |R ∪ P|` where `R` is the union of `paths.Key`s over the recent window and `P` over the prior window. Empty union ⇒ `1.0` (no evidence of a shift; do not manufacture a changepoint from silence). |
| `ToolShift` | total-variation distance between the tool-name distributions of the two windows: `0.5 · Σ_t |p_t − q_t|`. Either window empty ⇒ `0.0`. Range `[0,1]` by construction. |
| `LexicalCohesion` | Jaccard over lowercased whitespace-token bigram shingles of the two windows' concatenated `ArgsPreview` text, capped at `maxShingles` per window (bounded allocation). Either side empty ⇒ `1.0`. Computed but **not enabled by default**: Appendix C's `changepoint.features` is `["paths","tools","time","todos"]`, so `NewBOCD` drops it unless an operator adds `"lexical"`. |
| `GapSeconds` | `(ts − h.lastTS)/1000`, clamped at ≥ 0; `0` on the first observation. |
| `TodoTransition` | `1.0` if `sig.TodoCompleted || sig.TestPassed || sig.GitCommit`, else `0.0`. This is the G1.5 wiring: "Todo completion, passing test runs, git commits are all natural safe points". |

Window roll: on each call, the oldest entry of `recent` moves into `prior`, and `prior` drops its oldest, so both windows hold at most `window` turns. All slices are pre-allocated to `window` and reused; `FeaturesFrom` allocates only the transient path/tool maps.

**Performance budget.** `BenchmarkFeaturesFrom` with two 8-turn windows of 12 paths each: **≤ 100 µs/op**. It runs on the daemon's async B-C path (budget 50 ms), never on B-A.

---

### `internal/daemon/scheduler_candidates.go`

```go
package daemon

const maxAssembledCandidates = 32 // §5.4 "Twenty candidates, not 167,000"

type candidateAssembler struct {
    graph    dag.Graph
    segs     store.SegmentLog
    coupling map[int]int                       // Pos → dag.CrossingEdges(Pos)
    turnPos  map[core.TurnIndex]turnAnchor     // Turn → smallest Node.Pos at that turn
    version  dag.GraphStats                    // both caches invalidate together on a change
}
type turnAnchor struct {
    Pos     int
    Segment core.SegmentID
}

// Assemble builds Inputs.Candidates: changepoint boundaries ∩ API-round boundaries (§8.4).
func (a *candidateAssembler) Assemble(
    ctx context.Context,
    sess core.SessionID,
    cpTurns []core.TurnIndex,
    rounds map[core.TurnIndex]struct{},
    idx *reclaimableIndex,
) ([]scheduler.Candidate, error)
```

Algorithm, exactly:

1. Build (or reuse) the turn→position map. `dag.Graph` exposes no by-turn lookup, so the map is built with the one call that enumerates nodes — `graph.NodesAfter(0)` — recording, per `Node.Turn`, the **smallest** `Node.Pos` seen (00-ARCHITECTURE §5.9: *"Pos int — token position in the prefix — required for p-selection"*) and the `SegmentID` resolved from `segs.Range(turn, turn)` (first match; `0` when none). The map is cached on the assembler under the same `graph.Stats()` version guard as the coupling cache, so it is built at most once per graph mutation. Then, for every recorded changepoint turn `t` in `cpTurns` (ascending), look up `posOf(t)`. Turns absent from the map are skipped and counted in `obs.Counter("sched.candidate.unresolved")`.
2. `RoundBoundary = t ∈ rounds`. The round set is maintained by the Runtime and fed by the tap (`scheduler_tap.go`): `Qompack.md` §2.6 defines an API-round boundary as a **new assistant `message.id`**, and the one hook that fires exactly once per assistant turn is `Stop`. So the `ObserveStop` wrapper — and only that wrapper — records the current turn into `rounds`. Turn 0 is seeded as a round boundary at session bind so a session that has not yet produced a `Stop` still has one legal cut point.
3. `Coupling = a.crossing(pos)`, memoized in `a.coupling`. The cache is cleared whenever `graph.Stats()` reports a changed node/edge count. Without the cache, 32 candidates × sub-millisecond BFS would blow the Runtime budget; with it, a re-`Evaluate` inside one idle tick is free.
4. `ReclaimableTokens = idx.After(pos)`.
5. Sort ascending by `Pos`; if more than `maxAssembledCandidates` remain, keep the **highest-`Pos`** 32. Rationale: a very early boundary can only win when the cache is cold, and in that regime `rewrite = 0` makes even the 32nd-latest boundary a deep cut relative to `n`; keeping the newest boundaries preserves the resolution where the decision is actually close.
6. Return.

**Failure modes.** `graph == nil` or `segs == nil` ⇒ return `nil, nil` (no candidates), and `NewSchedulerRuntime` will already have refused to enable the p-selection gate. A `CrossingEdges` panic is recovered at the assembler boundary, logged `Loud`, and the candidate is dropped rather than failing the whole evaluation.

**Performance budget.** `BenchmarkAssemble` with 2 000 tool-use records, 40 changepoint turns and a 5 000-node graph: **≤ 20 ms/op cold cache, ≤ 200 µs/op warm cache**.

---

### `internal/daemon/scheduler_tap.go` — how L0 events reach L3

SP-05's daemon routes every hook to a `Services` **function seam** (`ObserveTool`, `ObserveStop`, `ObservePrompt`, `SessionStart`, `SessionEnd`); it never calls `Services.Sched` itself, and `scheduler.Runtime` has no hook-shaped method it could call. Without an explicit tap, nothing would ever call `Observe`, `NotifyActivity` or `Persist` and the whole layer would be dead code. The tap is therefore a first-class deliverable, and it uses SP-05's own late-binding seam so that **no SP-05 or SP-08 file is edited**.

```go
package daemon

// WrapServicesForScheduler decorates the seams in place. Registered from the composition root
// as: opts.Bind(func(s *Services) { WrapServicesForScheduler(s, sched, schedOpts) })
//
// ORDERING IS LOAD-BEARING: Bind hooks run in registration order, so this Bind must be
// registered AFTER SP-08's (which sets the seams). A nil inner seam is tolerated — the tap
// still runs — so a build without SP-08 degrades to "scheduler sees timestamps only".
func WrapServicesForScheduler(s *Services, rt scheduler.Runtime, o SchedulerRuntimeOptions)
```

Per-seam behaviour. Every wrapper calls the inner seam **first**, so SP-08 has already written the `store.ToolUseRecord` the tap reads, and every wrapper swallows its own errors (a tap failure must never change a hook's result):

| Seam | Tap work | Clock |
|---|---|---|
| `SessionStart` | `rt.BindSession(e.SessionID)` — binds the id, loads `state/*.json` for that id (§ state files below), seeds turn 0 as a round boundary, `NotifyActivity(now)` | warm path, no budget concern |
| `ObserveTool` | `sig := observer.ExtractSignals(e)`; `rec, err := store.ToolUse(ctx, e.ToolUseID)`; on success `f := FeaturesFrom(hist, sig, rec.Tool, rec.TS)` then `rt.Observe(ctx, f, rec.Turn)`; `rt.NotifyActivity(rec.TS)`; fold `rec.Tokens` into the burn-rate sample; then, when `sig.TodoCompleted \|\| sig.TestPassed \|\| sig.GitCommit`, `rt.CloseSegmentOn(ctx, rec.Turn, f, cause)` with `cause` ∈ `todo`/`test`/`commit` (first true wins) — these are the non-changepoint members of §8.5's "changepoint, todo completion, passing test run", plus the git-commit safe point G1.5 names | worker pool, budget **B-C** (50 ms) — never B-A |
| `ObserveStop` | same as `ObserveTool` minus the record lookup (a `Stop` carries no `tool_use_id`), **plus** `rt.NoteAPIRound(turn)` — §2.6's "boundary on new assistant `message.id`". `turn` is the highest turn the runtime has seen | worker pool, B-C |
| `ObservePrompt` | `rt.NotifyActivity(now)` **only**. This seam is called synchronously inside SP-05's 250 ms reply deadline, so the tap does no store I/O and no BOCD update here | reply path — keep under 1 ms |
| `SessionEnd` | `rt.Persist(ctx)` then `CloseSchedulerRuntime(rt)` | `flush` op, 20 s hook timeout |

`BindSession` and `NoteAPIRound` are additive methods on the concrete `*schedRuntime` (a struct SP-12 owns), reached inside the package without a type assertion; neither appears on the `scheduler.Runtime` interface, so Rule W-3 is not touched.

**Failure modes.** `rt == nil` ⇒ `WrapServicesForScheduler` returns without touching `s` (waves without SP-12 are unaffected). `store.ToolUse` returning `core.ErrNotFound` ⇒ increment `obs.Counter("sched.tap.no_record")` and skip the BOCD update for that event; the timestamp work still happens. A panic anywhere in the tap is recovered at the wrapper boundary, logged `Loud` once per session, and the inner seam's result is returned unchanged.

**Performance budget.** `BenchmarkSchedulerTap_ObserveTool` (one store lookup + one `FeaturesFrom` + one `Observe`): **≤ 1.5 ms/op**, which leaves the B-C budget (50 ms p99) intact. A test asserts the tap is unreachable from any client-side hook path, alongside `TestSchedulerNotOnHotPath`.

---

### `internal/daemon/scheduler_runtime.go`

```go
package daemon

const (
    deltaEWMAAlpha = 0.3 // smoothing for the measured compaction cost δ
    burnEWMAAlpha  = 0.2 // smoothing for the token burn rate

    // maxTurnHistory bounds cpTurns and rounds. 4096 is in §11.6's forbidden integer set, so
    // the annotation is mandatory — it is a memory bound, not a config default.
    maxTurnHistory = 4096 //nomagic:allow bounded-history cap, not an Appendix C default
)

type schedRuntime struct {
    mu sync.Mutex

    root    string
    session core.SessionID
    cfg     config.Config
    clock   core.Clock
    log     logging.Logger
    metrics obs.Registry

    st    store.Store
    segs  store.SegmentLog
    graph dag.Graph
    ledger  negknow.Ledger                          // may be nil
    ckpt    checkpoint.Writer                       // may be nil until SP-10 merges
    sources func() (checkpoint.SourceSet, error)    // may be nil until SP-10 merges
    draft   *checkpoint.Draft                       // opened lazily by advanceFrontier

    det  scheduler.Detector
    hist *FeatureHistory
    asm  *candidateAssembler

    d Daemon // bound by RegisterSchedulerIdleWork; nil until then

    cpTurns []core.TurnIndex
    rounds  map[core.TurnIndex]struct{}
    maxTurn core.TurnIndex

    contextTokens   core.Tokens // last computed; see Evaluate below
    effectiveWindow core.Tokens // resolved once per session bind (§ thresholds.go)
    maxOutput       core.Tokens // the maxOutputTokens the same ladder resolved
    windowSource    int         // 3 env autocompact | 2 explicit override | 1 host default
    precomputed     dag.Slice   // precompute_slice cache, read via PrecomputedSlice
    precomputedOK   bool

    lastActivity     core.UnixMilli
    lastAPICallTS    core.UnixMilli
    lastCacheWriteTS core.UnixMilli
    lastCompactionTS core.UnixMilli
    sessionStartTS   core.UnixMilli

    deltaEWMA    float64
    deltaSamples int
    burnEWMA     float64
    burnSamples  int
    lastTokens   core.Tokens
    lastTokensTS core.UnixMilli

    lastDecision       scheduler.Decision
    frontier           core.TurnIndex
    residual           core.Tokens
    lastCheckpointSeq  core.CheckpointSeq // parent seq for checkpoint.Writer.Begin; 0 = none
    residualWarned     bool               // one over-budget Warn per session
    dirty              bool               // state changed since the last Persist
}

func NewSchedulerRuntime(o SchedulerRuntimeOptions) (scheduler.Runtime, error)
```

`NewSchedulerRuntime` behaviour, in order:

1. Validate: `o.Store`, `o.Graph`, `o.Clock`, `o.Log` non-nil; `o.ProjectRoot` non-empty. Any missing ⇒ return `fmt.Errorf("daemon: scheduler runtime: %s required", name)`. `o.Session` **may be empty** — the daemon is per project and starts before any session exists, so the id arrives with the first `SessionStart` and is bound then. The daemon tolerates a nil `Sched` (00-ARCHITECTURE §5.4), so an error here degrades the scheduler, never the session.
2. `det = scheduler.NewBOCD(cfg.Scheduler.Changepoint.HazardRate, cfg.Scheduler.Changepoint.Features)`.
3. `segs = o.Store.Segments()`; build `asm`, `hist`; `rounds = map[core.TurnIndex]struct{}{}`.
4. `sessionStartTS = clock.Now()` in millis.
5. If `o.Session != ""`, call `BindSession(o.Session)` immediately; otherwise defer it to the tap.
6. `scheduler.EnablePSelection()` — this is the closing-note-3 unlock. Record `obs.Counter("sched.pselection.enabled").Inc()`.
7. Return the runtime.

**`BindSession(id)`** — idempotent for the same id; called once per session by the tap. In order:

1. `r.session = id`; `rounds = {0: {}}` (turn 0 is always a legal cut point).
2. Resolve the window: `effectiveWindow`, `windowSource` per the resolution ladder in the `thresholds.go` section. Logged at INFO with the source, once per session.
3. Load `state/bocd.json` and `state/scheduler.json` if present. **State is session-scoped.** If a file's `"session"` differs from `id`, every field in it is discarded and a fresh detector is used, logged at INFO — carrying a previous session's posterior, changepoint turns, frontier or EWMAs forward would be the cross-session warm start (O4) that the Out-of-scope table assigns to SP-16. The files are per project with fixed names (00-ARCHITECTURE §3.3), so this check is what makes them safe. A same-session match is the crash-recovery case and everything is restored.
4. A decode or CRC failure logs `Loud("scheduler state unreadable, restarting detector", "path", p, "err", err)`, deletes nothing, and continues with a fresh detector — the store is untouched, so no data is lost.
5. `lastCompactionTS = sessionStartTS` when the loaded state has none, so the Young–Daly clause has a baseline from turn 0.

**`Close() error`** — `Persist`, then `scheduler.DisablePSelection()`, then release the draft with `ckpt.Abort` when one is open. Idempotent. It is **not** on the `scheduler.Runtime` interface (§5.13 declares no `Close` and Rule W-3 forbids adding one), so callers reach it through the package helper `CloseSchedulerRuntime(rt)`, which type-asserts and returns `nil` when the assertion fails. The tap calls it on `SessionEnd`; the daemon's own shutdown path calls it via the same helper.

**`Observe(ctx, f, at)`** — takes the lock; `st := det.Observe(f)`; tracks `maxTurn`; if `st.AtChangepoint` appends `at` to `cpTurns` (deduplicated, capped at `maxTurnHistory` with oldest-first eviction) and calls `r.closeSegmentLocked(ctx, at, f, "changepoint")`; increments `obs.Counter("sched.changepoint")` on declaration; sets a dirty flag so the next idle tick persists. Returns `st`. Budget: **≤ 1 ms p99** on the async B-C path.

**`NoteAPIRound(at)`** — records `at` in `rounds` (capped at `maxTurnHistory`, oldest-first). Called only from the `ObserveStop` wrapper, which is the one seam that fires once per assistant turn (§2.6). Additive method on the concrete type; not on the interface.

**`Evaluate(ctx)`** — takes the lock, assembles `Inputs` and calls `scheduler.Evaluate`:

```go
in := scheduler.Inputs{
    Now:                    now,
    ContextTokens:          r.recomputeContextTokensLocked(ctx),
    EffectiveWindow:        r.effectiveWindow,
    MaxOutputTokens:        r.maxOutput,
    LastAPICallTS:          r.lastAPICallTS,
    LastCacheWriteTS:       r.lastCacheWriteTS,
    BurnRateTokensPerMin:   r.burnEWMA,
    MeasuredDeltaSeconds:   r.deltaPtr(),          // nil until deltaSamples > 0
    Changepoint:            r.det.State(),
    Candidates:             cands,
    FrontierTurn:           r.frontier,
    ResidualTokens:         r.residual,
    ExpectedRemainingReads: 0,                     // SP-16 fills this; unused here
    Cfg:                    r.cfg.Scheduler,
    LastCompactionTS:       r.lastCompactionTS,
    CouplingLambda:         r.cfg.Selection.Submodular.Lambda,
}
d := scheduler.Evaluate(in)
r.lastDecision = d
```

- `contextTokens` = `Σ Segment.Tokens` over this session's segments, obtained with one `segs.Range(0, r.maxTurn)` call (SP-06 maintains `Segment.Tokens`; 00-ARCHITECTURE §5.8). SP-05's `SessionRegistry` deliberately is **not** the source: its `SessionState` (SP-05, `registry.go`) carries `Events`/`Dropped`/`Externalized` counters and no token total, so reading it would be reading a number that does not exist. The value is cached on `r.contextTokens` and recomputed at most once per `Evaluate`. `Range` failing or returning nothing ⇒ `0`, which simply keeps `n > soft_floor` false; that is the honest answer, not a guess.
- `effectiveWindow` = `r.effectiveWindow`, resolved once at `BindSession` by the ladder in the `thresholds.go` section (`CLAUDE_CODE_AUTO_COMPACT_WINDOW` → `QOMPACK_CONTEXT_WINDOW`/`QOMPACK_MAX_OUTPUT_TOKENS` → the §2.5 host defaults). It is never `0` after a bind, so `error_no_window` can only appear before the first `SessionStart` — which is exactly when the scheduler should decline to act. `Breakdown["window_source"]` records which rung supplied it.
- `maxOutputTokens` = the value the same ladder resolved (0 on rung 1, where the env var already names the effective window).
- `deltaPtr()` returns `nil` when `deltaSamples == 0`, honouring "null means measure at runtime, not zero".
- Budget: **≤ 25 ms p99** with 2 000 tool-use records and 32 candidates. Never called from a hook; only from the idle worker and the `status` op.

**`NotifyActivity(ts)`** — sets `lastActivity = ts`; sets `lastAPICallTS = ts` (E1: the API-call clock is what the sliding TTL keys on); updates the burn-rate EWMA from `(tokens − lastTokens)` over `(ts − lastTokensTS)` when both deltas are positive. It does **not** forward to `IdleController.Notify`: SP-05 already calls `Notify` from `registry.Touch` on every accepted request (SP-05, `registry.go`), and a second call from here would be a duplicate feeding the same controller.

**`IdleSince()`** — returns `(lastActivity, clock.Now()−lastActivity >= cfg.Scheduler.Idle.DetectAfterSeconds)`.

**δ measurement.** `RecordCompactionCost(seconds float64)` (an additive method on the concrete type, reached through `SchedulerSnapshotOf` and by the frontier code) folds a measured compaction wall-clock into `deltaEWMA`:
`deltaEWMA = deltaEWMAAlpha*seconds + (1−deltaEWMAAlpha)*deltaEWMA`, seeded on the first sample, `deltaSamples++`, `lastCompactionTS = now`. Sources of a sample, in order of preference: the elapsed time of the `checkpoint` op (PreCompact entry→exit, SP-10's B-E clock, read from `obs.Registry.Hist("checkpoint_finalize").Snapshot()`); failing that, the wall-clock between a `PreCompact` observation and the following `SessionStart(source=compact)`. Both are real measurements — the design says *"δ is measured compaction cost"*, and nothing in this slice substitutes a constant.

**`Persist(ctx)`** — writes both state files with `paths.WriteAtomic`. Budget **≤ 5 ms**. Called on every idle tick when dirty, on `SessionEnd`, and on daemon shutdown.

---

### `internal/daemon/scheduler_state.go`

`state/bocd.json`:

```json
{
  "version": 1,
  "session": "sess-7f3a",
  "updated": 1730000000000,
  "hazard_rate": 0.004,
  "features": ["paths", "tools", "time", "todos"],
  "state": "UVBLQgEABAA..."
}
```

`state` is the standard-base64 encoding of `Detector.MarshalBinary()`. On load the state is **discarded** and a fresh detector used, in three cases, each asserted by test:

- `session` differs from the bound session id — logged at `Info`. Carrying a posterior across sessions is O4 cross-session warm start, which the Out-of-scope table assigns to SP-16.
- `hazard_rate` or `features` differ from the current config — logged at `Warn` with both shapes. The model shape changed; this is the correct behaviour for a config edit mid-project.
- decode, base64 or CRC failure — logged `Loud`.

`state/scheduler.json`:

```json
{
  "version": 1,
  "session": "sess-7f3a",
  "updated": 1730000000000,
  "session_start_ts": 1729996400000,
  "last_compaction_ts": 1729999000000,
  "last_api_call_ts": 1730000000000,
  "last_cache_write_ts": 1729999985000,
  "delta_ewma_seconds": 18.4,
  "delta_samples": 3,
  "burn_ewma_tokens_per_min": 812.5,
  "burn_samples": 41,
  "effective_window": 180000,
  "window_source": 1,
  "changepoint_turns": [0, 14, 33],
  "round_turns": [0, 3, 7, 14, 19, 33],
  "frontier_turn": 33,
  "residual_tokens": 9120,
  "last_decision": {
    "should_compact": false,
    "reasons": ["soft_floor"],
    "p_pos": 148230,
    "p_turn": 33,
    "p_score": -22429.3,
    "urgency": 1,
    "ttl": "warm",
    "young_daly_seconds": 268.3,
    "soft_floor_tokens": 99000,
    "hard_ceiling_tokens": 147000,
    "breakdown": { "reclaimable": 600, "rewrite": 23012.5, "distortion": 16.8 }
  }
}
```

The document above is internally consistent and a test asserts it: `p_score = reclaimable − rewrite − distortion = 600 − 23012.5 − 16.8 = −22429.3`, and `rewrite = w·(n − p)·cacheFactor = 1.25 × (166 640 − 148 230) × 1.0 = 23 012.5`.

Both files are written under `.qompack/state/`. Neither is append-only (00-ARCHITECTURE §3.3 lists `state/` as daemon-persisted, not append-only), so `paths.WriteAtomic` is correct and `paths.AppendOnly` must **not** be used. `changepoint_turns` and `round_turns` are capped at `maxTurnHistory` entries each on write; overflow drops the oldest. Both files carry the bound `session`; a mismatch on load discards the document, per the rule above.

Decode failure on either file: log `Loud`, continue with defaults. Unknown JSON fields are ignored (forward compatibility, matching the config loader's posture).

---

### `internal/daemon/scheduler_frontier.go` — O5 continuous frontier advancement

Three responsibilities: close a segment when a boundary is observed, roll a successor open, and encode closed-and-unencoded segments into the checkpoint draft during idle so the residual span stays O(delta).

Two entry points reach `closeSegmentLocked`, and together they cover every boundary event the design names:

- `Observe` calls it with `cause = "changepoint"` when the detector declares one.
- `CloseSegmentOn(ctx, at, f, cause)` — an additive method on the concrete type, called by the tap — covers `cause` ∈ `{"todo", "test", "commit"}`. It takes the lock and delegates. Together these are §8.5's "changepoint, todo completion, passing test run" plus G1.5's git-commit safe point.

```go
// closeSegmentLocked closes the session's current segment and opens its successor.
// Cause is one of: changepoint, todo, test, commit.
// SP-08 owns the session's FIRST Open; every subsequent roll is owned here.
func (r *schedRuntime) closeSegmentLocked(
    ctx context.Context, at core.TurnIndex, f scheduler.Features, cause string) error {

    if !r.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose {
        return nil
    }
    cur, err := r.segs.Current(ctx, r.session)
    if errors.Is(err, core.ErrNotFound) {
        return nil // SP-08 has not opened one yet; nothing to close
    } else if err != nil {
        return err
    }
    if cur.Closed || at < cur.StartTurn {
        return nil
    }
    feats := map[string]float64{
        "path_jaccard": f.PathJaccard, "tool_shift": f.ToolShift,
        "lexical_cohesion": f.LexicalCohesion, "gap_seconds": f.GapSeconds,
        "todo_transition": f.TodoTransition,
        "prob_changepoint": r.det.State().ProbChangepoint,
    }
    if err := r.segs.Close(ctx, cur.ID, at, feats); err != nil {
        return err
    }
    _, err = r.segs.Open(ctx, store.Segment{
        Session: r.session, StartTurn: at + 1, StartTS: r.nowMS(),
    })
    r.metrics.Counter("sched.segment.closed." + cause).Inc()
    return err
}
```

```go
// advanceFrontier is the body of the O3-registered "act.advance_frontier" task (gated on the
// TaskAdvanceFrontier value in Decision.Background) and the O5 mechanism.
func (r *schedRuntime) advanceFrontier(ctx context.Context) error
```

Algorithm:

1. If `r.ckpt == nil` **or** `r.sources == nil` (SP-10 not yet in the build) ⇒ return `nil` after `obs.Counter("sched.frontier.no_writer").Inc()`. This is the Rule W-2 posture: SP-12 develops against the stub and the golden fixtures, and the wave-3 verification checkpoint re-runs these tests against SP-10's real writer.
2. `segs, err := r.segs.Unencoded(ctx, r.session)`; filter to `s.Closed == true`; sort ascending by `StartTurn`.
3. If empty ⇒ recompute `residual` and return.
4. Open the draft lazily: `src, err := r.sources()` then `r.draft, err = r.ckpt.Begin(ctx, r.session, r.lastCheckpointSeq, src)`. **SP-12 does not construct the `SourceSet`.** Its members include `pins.Store`, `grammar.Sequitur` and `tokens.Estimator`, none of which `daemon.Options` or `SchedulerRuntimeOptions` carries; SP-10 owns `checkpoint` and owns wiring `SchedulerRuntimeOptions.Sources` (together with `Checkpoints`) when it merges. What matters for invariant 1 is that `SourceSet` has no field that can carry live context text (00-ARCHITECTURE §5.14), so "checkpoint from a summary" stays uncompilable regardless of who builds it. A `Sources()` error is treated exactly like step 1: counter, `Warn`, return.
5. `newFrontier, err := r.ckpt.Advance(ctx, r.draft, ids)`.
   - `errors.Is(err, core.ErrAlreadyEncoded)` ⇒ this is a DPI-guard violation: log `Loud("frontier advance hit the DPI guard", "segments", ids)`, drop the offending ids from the batch, retry once with the remainder, and if it recurs abandon the draft (`r.ckpt.Abort`) and leave the frontier where it is. **Never** re-encode from a checkpoint.
   - Any other error ⇒ log `Warn`, leave the frontier, return the error to the idle controller (which records it and continues with the next task).
6. `r.frontier = newFrontier`; recompute residual; `r.dirty = true`.
7. Recompute residual as `residual = contextTokens − Σ Segment.Tokens over segments with EncodedOnce == true`, clamped at ≥ 0.
8. If `residual > cfg.Checkpoint.Frontier.MaxResidualTokens` (Appendix C: 20000) **and** there were no unencoded closed segments to encode, log once per session at `Warn` with `obs.Gauge("sched.residual_over_budget").Set(1)`. This is the honest signal that O5 is not keeping up — §11.4's "watch for" discipline applied to our own mechanism rather than hidden.

**Idempotence.** Running `advanceFrontier` twice in one idle window is a no-op on the second call because `MarkEncoded` (invoked inside SP-10's `Advance`) is idempotent for the same `CheckpointSeq`. A test asserts the second call returns `nil` and does not move the frontier.

---

### `internal/daemon/scheduler_idle.go` — O3 idle-time background work

```go
func RegisterSchedulerIdleWork(d Daemon, rt scheduler.Runtime, o SchedulerRuntimeOptions) error
```

Registers exactly six tasks on `d.Idle()` and binds `d` into the runtime (`r.d = d`, in-package). Lower priority number runs first.

**The `act.` prefix is normative, not cosmetic.** SP-05's `IdleController.RunOnce` skips any task whose registered name begins `act.` when the contract monitor is not in `ModeFull` — that is the mechanism implementing 00-ARCHITECTURE §12.1's "no scheduler-initiated checkpoints" in `degraded-passive`. Exactly one of these six *acts*: `advance_frontier` drives `checkpoint.Writer.Advance`. It is therefore registered as **`act.advance_frontier`**. The other five are recording/maintenance work that must keep running while degraded (§12.1 preserves L0/L1), so they carry no prefix. The `scheduler.BackgroundTask` **values** are unchanged — `advance_frontier` etc., exactly as 00-ARCHITECTURE §5.13 declares them — because the prefix belongs to the registration name, not to the decision vocabulary; the gate below keys on the value.

| Registered name | Prio | Body | Guard |
|---|---|---|---|
| `act.advance_frontier` | 10 | `r.advanceFrontier(ctx)` (O5) | `TaskAdvanceFrontier` in `Background` ∧ `cfg.Checkpoint.Frontier.AdvanceOnSegmentClose` |
| `precompute_slice` | 20 | `graph.BackwardSlice(criteria, SliceOptions{Thin: cfg.Selection.Slicing == "thin", Decay: 0.85, Deadline: 250ms})`, stored on the runtime as `r.precomputed`/`r.precomputedOK` and read by SP-11 and SP-15 through the exported `daemon.PrecomputedSlice(rt)` helper | in `Background` |
| `refresh_delta` | 30 | pull `obs.Hist("checkpoint_finalize").Snapshot().P50` and fold it into `deltaEWMA`; recompute the burn EWMA; `Persist` | in `Background` |
| `rebuild_bloom` | 40 | `ledger.RefreshStaleness(ctx, store)` then, if it flipped anything or `ledger.Health().NeedsResize`, `ledger.RebuildBloom(ctx)` | in `Background` ∧ `ledger != nil` ∧ `cfg.Eliminations.RebuildOnStale == "nextIdle"` |
| `compact_dag` | 50 | `graph.Compact(ctx)` | in `Background` |
| `gc` | 60 | `store.GC(ctx, GCPolicy{RetainDays: cfg.Store.Retention.Days, RetainSessions: cfg.Store.Retention.Sessions, Deadline: remaining budget})` | in `Background` |

Every task body begins with the same three guards, in this order:

```go
func (r *schedRuntime) gate(name scheduler.BackgroundTask) bool {
    if !r.cfg.Scheduler.Idle.BackgroundWork { return false }
    r.mu.Lock(); defer r.mu.Unlock()
    return slices.Contains(r.lastDecision.Background, name)
}
```

so the pure `Evaluate` decides *what* runs and the daemon decides *when*, without SP-12 editing SP-05's `IdleController`. If no `Evaluate` has run yet this session, `lastDecision.Background` is empty and every task is inert — which is the correct posture for a session that has not yet reached the soft floor.

Each task honours the `ctx` deadline the `IdleController` passes and returns promptly on cancellation. `gc` additionally derives `GCPolicy.Deadline` from that context — `if dl, ok := ctx.Deadline(); ok { p.Deadline = time.Until(dl) }`, and when the context carries no deadline, `p.Deadline = 0` meaning "unbounded, but still cancellable" — so it is resumable (00-ARCHITECTURE §5.8 GC semantics) rather than truncated.

**Criterion set for `precompute_slice`.** The `[]dag.NodeID` criteria are, in order: the current segment's `KindSegment` node, every `KindFile` node touched in the last `defaultFeatureWindow` turns, and the most recent `KindUserPrompt` node. This mirrors §8.3's "current todo items, files under edit, the active plan, the most recent user intent" as far as the DAG exposes it in wave 3.

---

### `cmd/qompack/main.go` — the only modification outside SP-12's own files

In the `daemon` subcommand, after `opts` is populated — **including after every other subplan's `opts.Bind(…)` call**, because `Bind` hooks run in registration order and the tap must decorate seams SP-08 has already set — and **before** `daemon.New(opts)`:

```go
// Block 1 — construct. Session is deliberately empty: the daemon is per project and starts
// before any session exists; the runtime binds the first id its tap sees. Sources stays nil
// until SP-10 merges and wires it alongside opts.Checkpoints.
schedOpts := daemon.SchedulerRuntimeOptions{
    ProjectRoot: opts.ProjectRoot, Cfg: opts.Cfg,
    Clock: opts.Clock, Log: opts.Log, Metrics: opts.Metrics,
    Store: opts.Store, Graph: opts.Graph, Ledger: opts.Ledger,
    Checkpoints: opts.Checkpoints,
}
sched, schedErr := daemon.NewSchedulerRuntime(schedOpts)
if schedErr != nil {
    opts.Log.Loud("scheduler runtime unavailable; L3 disabled for this daemon", "err", schedErr)
} else {
    opts.Sched = sched
    // Block 2 — tap. This is the only path by which L0 events reach L3.
    opts.Bind(func(s *daemon.Services) { daemon.WrapServicesForScheduler(s, sched, schedOpts) })
}
```

and immediately after `d, err := daemon.New(opts)` succeeds:

```go
// Block 3 — idle registration.
if opts.Sched != nil {
    if err := daemon.RegisterSchedulerIdleWork(d, opts.Sched, schedOpts); err != nil {
        opts.Log.Loud("scheduler idle work not registered", "err", err)
    }
}
```

Three added blocks (construction, tap binding, idle registration), no edits to any existing line, no changes to SP-05's daemon internals. A failure at any point degrades L3 only — the daemon, the store, and every hook keep working, which is 00-ARCHITECTURE §12.3's "everything else fails toward do nothing". `cmd/qompack/main.go` must stay under 150 LOC (00-ARCHITECTURE §3.1); if these blocks would push it over, move all three into an SP-12-owned `cmd/qompack/scheduler_wiring.go` with a single `wireScheduler(&opts)` call left in `main.go`, and record that in the ADR.

---

### `test/replay/l3policy/policy.go`

```go
package l3policy

// Imports: internal/core, internal/config, internal/eval, internal/scheduler — and NOT
// internal/daemon, which is a composition root nothing may import (00-ARCHITECTURE §3.2).
// That is precisely why the drop classification lives in `scheduler` as DropClassOf.
//
// New returns the eval.Policy named "qompack-l3". It replays a logged session through
// scheduler.Evaluate with no daemon, no store and no I/O: candidates and reclaimable tokens
// are derived from the session's own logged tool calls via scheduler.DropClassOf, and
// segment coupling is approximated by counting logged path-sharing pairs that straddle p.
func New(cfg config.Config) eval.Policy

// The modelled compaction pause (see phase4_test.go). Deterministic replay makes no model
// call, so a wall-clock pause cannot be measured; §8.5 says decode dominates and decode
// length tracks the residual span, so the pause is MODELLED linearly from it and labelled as
// modelled everywhere it is reported. Neither literal is in §11.6's forbidden sets.
const (
    pauseInterceptMS  = 3000.0 // fixed per-call overhead: prefill of the novel span + setup
    pauseMSPerToken   = 0.12   // decode cost per residual token
)
// Sanity anchors, asserted by policy_test.go: a stock 150 000-token residual models to
// 21.0 s, inside §6.7's observed "15–40 s" band; a frontier-advanced 20 000-token residual
// models to 5.4 s.
```

`KeepSet(ctx, s, at, budget)` returns `KeepSet{P: chosen.Pos, IDs: …, Tokens: …}` where `IDs` is every logged tool-call id **before** `P` plus every non-droppable id after it. `P` is what `test/replay/phase4_test.go` sums into `Score.RewriteTokens` via `w · (n − p_min)`. The policy fills `Run.ResidualSpan` from the frontier it simulates and `Run.PauseMS` from the model above. Deterministic: no clock, no randomness, `ReplayOptions.Deterministic` respected.

**Feature synthesis for replay.** The policy drives the same `scheduler.NewBOCD` detector the daemon uses, feeding it `scheduler.Features` computed from the logged turns: `PathJaccard` and `ToolShift` over two 8-turn windows of `eval.ToolCall.Paths`/`.Name` (the same definitions as `FeaturesFrom`), `GapSeconds` from `eval.Turn.TS`, `TodoTransition` from a logged `TodoWrite` completion or a `test:pass` action, and `LexicalCohesion` left at `0` since the default feature list excludes it. Token positions are the running sum of `eval.Turn.Tokens`; every assistant turn is an API-round boundary (§2.6).

---

## Test plan (TDD)

Tests are written **before** the implementation inside each commit and must be observed failing (against the SP-01 `ErrNotImplemented` stub or a missing symbol) before the implementation lands. Assertions use `testify/require` (`assert` is banned, 00-ARCHITECTURE §6.1). Property tests use `pgregory.net/rapid`. Every test that touches time uses `testutil.FakeClock`; `time.Sleep` is forbidden outside `test/bench`.

### Shared fixtures

| Fixture | Location | Content |
|---|---|---|
| `baseCfg()` | `internal/scheduler/testdata_test.go` | `config.Defaults().Scheduler` — `softFloorPct 0.55`, `hardCeilingMargin 20000`, `youngDaly{enabled:true, measuredDeltaSeconds:nil}`, `changepoint{hazardRate:0.004, features:["paths","tools","time","todos"]}`, `cache{0.1, 1.25, 300}`, `idle{120, true, true}` |
| `baseInputs()` | same | `Now: 1_700_000_000_000`, `ContextTokens: 120_000`, `EffectiveWindow: 180_000`, `MaxOutputTokens: 32_000`, `LastAPICallTS: Now−10_000`, `LastCompactionTS: Now−600_000`, `BurnRateTokensPerMin: 900`, `CouplingLambda: 0.4`, three candidates at `Pos` 40 000 / 80 000 / **118 000** with `ReclaimableTokens` 30 000 / 12 000 / 6 000, `Coupling` 210 / 84 / 42, all `RoundBoundary: true`. **Every `Pos` is strictly below `ContextTokens`** — a candidate beyond `n` is not a possible prefix position, and a fixture that contained one would let the warm-cache argmax test pass for the wrong reason (a clamped-to-zero tail rather than a genuinely cheap rewrite). Warm scores under this fixture: `−97 084` / `−48 833.6` / `−1 916.8`; cold scores: `2 916` / `1 166.4` / `583.2`. |
| `stepSeries(n, shift)` | `internal/scheduler/bocd_test.go` | `n` observations drawn deterministically (fixed LCG seed) from mean `0.2` then, after `n/2`, mean `0.2+shift` |
| `testdata/golden/contracts/checkpoint/` | repo | SP-01-generated `checkpoint.Writer` fixtures used under Rule W-2 until SP-10 merges |
| `testdata/golden/scheduler/decision-*.json` | new, SP-12 | three frozen `Decision` documents (warm/expiring/cold) used as regression goldens for `Breakdown` key completeness and ordering |
| `fakeSegmentLog` / `fakeGraph` / `fakeStore` | `internal/daemon/scheduler_testhelpers_test.go` | in-memory implementations of the three interfaces, with call recorders for `Close`, `Open`, `MarkEncoded`, `Advance`, `CrossingEdges` |

### `internal/scheduler/thresholds_test.go`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestEffectiveWindow_Section25Arithmetic` | — | `(200_000, 32_000)`; `(200_000, 8_000)`; `(200_000, 0)`; `(0, 8_000)`; `(10_000, 64_000)` | `180_000` (cap binds); `192_000`; `200_000`; `0`; `0` — never negative |
| `TestSoftFloor_55PctOfEffectiveWindow` | `baseCfg()` | `effectiveWindow = 180_000` | `99_000` |
| `TestHardCeiling_OneTurnBelowHostThreshold` | `baseCfg()` | `effectiveWindow = 180_000` | `147_000` (= 180 000 − 13 000 − 20 000); asserts `hardCeiling < 167_000`, the host threshold |
| `TestHardCeiling_ClampedWhenMarginExceedsWindow` | `cfg.HardCeilingMargin = 500_000` | `effectiveWindow = 180_000` | `1`, never ≤ 0 |
| `TestThresholds_ZeroWindow` | `baseCfg()` | `effectiveWindow = 0` | both `0` |
| `TestSoftFloorBelowHardCeiling_Property` (rapid) | `softFloorPct ∈ (0,1)`, `margin ∈ [1, 50_000]`, `window ∈ [50_000, 1_000_000]` | — | `SoftFloor < HardCeiling` whenever `window > 13_000 + margin + 1`; asserts the §12 "plugin acts first by design" ordering can never invert |

### `internal/scheduler/ttl_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestClassifyTTL_Table` | `ttl=300`; gaps 0, 10, 149, 150, 224, 299, 300, 3600 s | `warm, warm, warm, expiring, expiring, expiring, cold, cold`; boundary at exactly 150 s is `expiring`, at exactly 300 s is `cold` |
| `TestClassifyTTL_UnknownWhenNoAPICall` | `lastAPICallTS = 0` | `TTLUnknown`, gap `0` |
| `TestClassifyTTL_NegativeGapClamped` | `lastAPICallTS > now` | `TTLWarm`, gap `0` |
| `TestCacheFactor_Ramp` | `ttl=300`; states/gaps from the row above | warm ⇒ `1.0`; gap 150 ⇒ `1.0`; gap 225 ⇒ `0.5`; gap 299 ⇒ `≈0.00667`; cold ⇒ `0.0`; unknown ⇒ `1.0` |
| `TestCacheFactor_MonotoneDecreasing_Property` (rapid) | `gap ∈ [0, 2·ttl]` | factor is non-increasing in gap and always in `[0,1]` |
| `TestSlidingTTLUsesAPICallNotCacheWrite` | `LastCacheWriteTS = Now−1000` (fresh), `LastAPICallTS = Now−400_000` (stale), `ttl=300` | `Decision.TTL == TTLCold` — the E1 correction, asserted directly |

### `internal/scheduler/youngdaly_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestYoungDaly_Formula` | `δ=20`, `M=1800` | `268.3281572999748` (±1e-9) |
| `TestYoungDaly_NonPositive` | `(0,1800)`, `(20,0)`, `(-1,5)`, `(NaN,5)`, `(Inf,5)` | all `0` |
| `TestMTBF_FromBurnRate` | `context=120_000`, `hard=147_000`, `burn=900` | `1800.0` |
| `TestMTBF_ZeroWhenAtOrAboveCeiling` | `context=150_000`, `hard=147_000` | `0` |
| `TestMTBF_ZeroWhenBurnUnknown` | `burn=0` | `0` |
| `TestResolveDelta_ConfigOverridesRuntime` | cfg `= ptr(30)`, inputs `= ptr(12)` | `(30, true)` |
| `TestResolveDelta_RuntimeUsedWhenConfigNil` | cfg `= nil`, inputs `= ptr(12)` | `(12, true)` |
| `TestResolveDelta_NilMeansMeasureNotZero` | both `nil` | `(0, false)`; and `Evaluate` sets `young_daly_delta_unmeasured=1` and **never** fires `ReasonYoungDaly` — the explicit test that JSON `null` is not read as zero |

### `internal/scheduler/skirental_test.go`

`TestSkiRentalShouldWrite`: `r=0.1, w=1.25` ⇒ threshold `12.5`; `expectedReads=12` ⇒ `false`, `12.5` ⇒ `false`, `12.6` ⇒ `true`. `r=0` or `w=0` ⇒ `false`. A grep test in the same file asserts the literal `12.5` appears only inside `_test.go`.

### `internal/scheduler/bocd_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestBOCD_StationarySeries_RunLengthGrows` | `stepSeries(200, 0)` | `RunLength` at the end ≥ 100; `AtChangepoint` declared ≤ 2 times over the 200 observations |
| `TestBOCD_StepChange_DetectedWithinFiveObservations` | `stepSeries(200, 0.6)` | first `AtChangepoint == true` occurs at index `∈ [100, 105]` |
| `TestBOCD_Hysteresis_NoStorm` | a 40-observation plateau at mean `0.9` following a shift | at most one declaration in any window of `bocdMinTurnsBetweenDecls` consecutive observations |
| `TestBOCD_PosteriorNormalized_Property` (rapid) | 500 random `Features` | `|Σ Posterior − 1| < 1e-9` after every `Observe`; every entry ≥ 0 |
| `TestBOCD_PosteriorBounded` | 5 000 observations, hazard `0.004` | `len(Posterior) ≤ 512` always; mean length over the run `< 64`; per-op wall time non-increasing between the first and last 500 observations (the O(1)-amortized claim) |
| `TestBOCD_MarshalRoundTrip_Property` (rapid) | detector fed `k ∈ [0,300]` random observations | `Unmarshal(Marshal(d))` reproduces `State()` exactly and produces byte-identical `Marshal` output |
| `TestBOCD_UnmarshalRejectsCorruption` | valid buffer mutated: bad magic; `version=2`; `F=0`; `F=9`; `R=0`; `R=513`; unknown feature name; truncated by 1 byte; extended by 1 byte; flipped CRC byte | every case returns an error wrapping `core.ErrNotFound`; the receiver's `State()` is unchanged |
| `TestBOCD_UnmarshalBoundsCheckedBeforeAllocation` | header claiming `R=4_000_000_000` on a 40-byte buffer | error, and the test asserts peak alloc via `testing.AllocsPerRun` stays under 64 |
| `TestBOCD_Deterministic` | same 300-observation series into two detectors | identical `ChangepointState` at every step, including `Posterior` |
| `TestBOCD_FeatureSubsetHonoured` | `NewBOCD(0.004, []string{"time"})` | serialized `featureCount == 1`; changing only `PathJaccard` across 50 observations never declares a changepoint |
| `TestBOCD_UnknownFeaturesDropped` | `NewBOCD(0.004, []string{"paths","bogus"})` | features `["paths"]` |
| `TestBOCD_EmptyFeaturesFallsBackToDefaults` | `NewBOCD(0.004, nil)` | features `["paths","tools","time","todos"]` |
| `TestBOCD_InvalidHazardClamped` | `hazardRate` of `0`, `1`, `-1`, `NaN` | no panic; `Observe` produces a normalized posterior |
| `TestBOCD_ResetRestoresPrior` | 100 observations then `Reset()` | `len(Posterior)==1`, `RunLength==0`, `Marshal` equals a fresh detector's |
| `TestBOCD_DegeneratePosteriorSelfHeals` | inject `GapSeconds = math.Inf(1)` via a feature that underflows the predictive to 0 | `Observe` returns a valid normalized state (via `Reset`), no panic, no NaN |

### `internal/scheduler/evaluate_test.go`

| Test | Delta from `baseInputs()` | Expected |
|---|---|---|
| `TestEvaluate_BelowSoftFloor_NoCompact` | `ContextTokens = 90_000`, changepoint declared | `ShouldCompact=false`, `Reasons` empty, `Urgency=UrgencyNone` |
| `TestEvaluate_AboveSoftFloorNoClause_NoCompact` | `ContextTokens=120_000`, no changepoint, δ nil, gap 10 s | `ShouldCompact=false`, `Reasons=["soft_floor"]`, `Urgency=UrgencyNone` |
| `TestEvaluate_Changepoint_Fires` | `Changepoint.AtChangepoint=true` | `true`, `Reasons=["soft_floor","changepoint"]`, `Urgency=UrgencyAdvisory` |
| `TestEvaluate_YoungDaly_Fires` | `MeasuredDeltaSeconds=ptr(20)`, `LastCompactionTS=Now−300_000` (elapsed 300 s > 268.3 s) | `true`, `Reasons` contains `young_daly`, `YoungDalySeconds≈268.328` |
| `TestEvaluate_YoungDaly_DoesNotFireBelowInterval` | same but `LastCompactionTS=Now−200_000` | `Reasons` has no `young_daly` |
| `TestEvaluate_HardCeiling_Fires_UrgencyNow` | `ContextTokens=150_000` | `true`, `Reasons` contains `hard_ceiling`, `Urgency=UrgencyNow` |
| `TestEvaluate_IdleColdCache_Fires` | `LastAPICallTS=Now−400_000` | `true`, `Reasons` contains `idle_cold_cache`, `TTL=TTLCold` |
| `TestEvaluate_ReasonsOrderStable` | all four clauses true | `["soft_floor","changepoint","young_daly","hard_ceiling","idle_cold_cache"]` exactly |
| `TestEvaluate_ArgmaxLatestWhenWarm` | default (warm) | `P.Pos == 118_000` — the latest boundary; `PScore == −1_916.8` (±1e-9) and negative, asserted as intended |
| `TestEvaluate_ArgmaxDeepestWhenCold` | `LastAPICallTS=Now−400_000` | `P.Pos == 40_000` — the deepest boundary; `PScore == 2_916`; `Breakdown["rewrite"]==0` |
| `TestEvaluate_DeepCutWhenColdDisabled` | cold **and** `cfg.Idle.DeepCutWhenCold=false`, with all three candidates given equal `ReclaimableTokens` and `Coupling` so scores tie | `P.Pos == 118_000` (latest wins the tie) |
| `TestEvaluate_RoundBoundaryIntersection` | candidate at `Pos 80_000` has `RoundBoundary=false` | it is never chosen; `Breakdown["candidates"]==2` |
| `TestEvaluate_RoundBoundaryRelaxed` | all candidates `RoundBoundary=false` | `Breakdown["round_boundary_relaxed"]==1`, a `P` is still chosen |
| `TestEvaluate_NoCandidates` | `Candidates=nil`, changepoint declared | `ShouldCompact=false`, `Breakdown["no_candidates"]==1`, `Urgency=UrgencyAdvisory` |
| `TestEvaluate_NoCandidatesAboveCeiling` | `Candidates=nil`, `ContextTokens=150_000` | `ShouldCompact=false`, `Urgency=UrgencyNow` |
| `TestEvaluate_ScoreArithmeticExact` | one candidate: `Pos=148_230`, `Reclaimable=6_000`, `Coupling=42`; `n=150_000`; warm | `reclaimable = 6_000×0.1 = 600`; `rewrite = 1.25×1_770×1.0 = 2_212.5`; `distortion = 0.4×42 = 16.8`; `PScore = 600 − 2212.5 − 16.8 = −1629.3` (±1e-9) |
| `TestEvaluate_MultipliersReadFromConfig` | run twice with `readMultiplier` 0.1 then 0.2 | `Breakdown["reclaimable"]` exactly doubles; same for `writeMultiplier` and `rewrite` — proves §12's "read `r` and `w` from config, never hardcode" |
| `TestEvaluate_LambdaZeroDisablesDistortion` | `CouplingLambda=0` | `Breakdown["distortion"]==0` even with `Coupling=210` |
| `TestEvaluate_ZeroEffectiveWindow` | `EffectiveWindow=0` | zero `Decision`, `Breakdown` has exactly one key `error_no_window` |
| `TestEvaluate_NonMonotonicReclaimableFlagged` | candidates with increasing reclaimable in `Pos` | `Breakdown["reclaimable_nonmonotonic"]==1`, decision still produced |
| `TestEvaluate_CandidatesCappedAt32` | 100 candidates at ascending `Pos` | `Breakdown["candidates"]==32`, `Breakdown["candidates_supplied"]==100`, and the retained set is the highest-`Pos` 32 |
| `TestEvaluate_Purity_NoInputMutation` | deep-copy `Inputs`, call `Evaluate` | `go-cmp` reports no diff against the copy, including the `Candidates` slice contents |
| `TestEvaluate_Purity_Idempotent` | call twice | `go-cmp` reports no diff between the two `Decision`s |
| `TestEvaluate_BreakdownKeysComplete` | default | the key set equals the frozen golden `testdata/golden/scheduler/decision-warm.json`; a missing or extra key fails, so SP-14's status surface can never silently lose a field. `window_source` is **not** in the golden: the Runtime adds it after `Evaluate` returns, and `TestRuntime_WindowResolutionLadder` covers it |
| `TestEvaluate_BackgroundEmptyWhenDisabled` | `cfg.Idle.BackgroundWork=false` | `Background` nil |
| `TestEvaluate_BackgroundColdIncludesAllSix` | cold, `ResidualTokens=9_000`, `ContextTokens>soft`, δ nil | `["advance_frontier","precompute_slice","refresh_delta","rebuild_bloom","compact_dag","gc"]` in exactly that order |
| `TestEvaluate_BackgroundWarmIsFrontierOnly` | warm, `ResidualTokens=9_000`, δ measured, `ContextTokens < soft` | `["advance_frontier"]` |
| `TestEvaluate_NoAllocationsBeyondBudget` (`testing.AllocsPerRun`) | 64 candidates | ≤ 8 allocations per call |

### `internal/scheduler/pselect_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestEligible_KeepsOnlyRoundBoundaries` | 4 candidates, 2 with `RoundBoundary: true` | those 2, in `Pos` order; `relaxed == false` |
| `TestEligible_RelaxesWhenIntersectionEmpty` | 3 candidates, none a round boundary | all 3 returned; `relaxed == true` |
| `TestEligible_EmptyInputStaysEmpty` | `nil` | `nil`, `relaxed == false` |
| `TestPrepareCandidates_SortsAndDetectsNonMonotonic` | unsorted input, reclaimable rising with `Pos` | ascending by `Pos`; `nonMonotonic == true` |
| `TestPrepareCandidates_CapKeepsHighestPos` | 100 candidates | 32 retained, the highest-`Pos` ones, ascending |
| `TestScoreCandidates_UsesConfigMultipliers` | `r=0.2`, `w=2.0`, `cacheFactor=0.5` | `reclaim`, `rewrite` match the hand-computed values; `tail` clamped to 0 when `Pos > n` |
| `TestChooseP_TieBreakLatestWhenWarmDeepestWhenCold` | three equal scores | warm ⇒ largest `Pos`; cold with `DeepCutWhenCold` ⇒ smallest `Pos`; cold with the flag off ⇒ largest `Pos` |
| `TestChooseP_EmptyReturnsNotOK` | `nil` | `ok == false` |

### `internal/scheduler/gate_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestPSelectionAvailable_DefaultFalse` | `TestMain` records `PSelectionAvailable()` into a package var **before** calling `m.Run()`; the test asserts that recorded value | `false` — the only way to observe the process default without depending on test ordering |
| `TestEnableDisablePSelection` | `Enable` then `Disable`, each with `defer DisablePSelection()` | `true` then `false` |
| `TestPSelectionGate_ConcurrentAccess` (`-race`) | 64 goroutines reading while one writes | no race, no torn read |

### `internal/scheduler/schedulertest/suite.go`

Flip every `t.Skip` SP-01 left. `RunEvaluateSuite` gains: threshold arithmetic, the five trigger clauses, the argmax limbs, purity, and `Breakdown` completeness. `RunDetectorSuite` gains: normalization, bounded posterior, marshal round-trip, determinism. `RunRuntimeSuite` gains: `Observe` → `State` consistency, `NotifyActivity` → `IdleSince`, `Persist` round-trip. Do not rename SP-01's entry points (Rule W-3). A merge blocker check: `grep -R "t.Skip" internal/scheduler/schedulertest/` must return nothing.

### `internal/scheduler/dropclass_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestDropClassOf_CompactableTable` | `FileRead`, `Read`, `Bash`, `PowerShell`, `Grep`, `Glob`, `WebSearch`, `WebFetch`, `FileEdit`, `Edit`, `MultiEdit`, `FileWrite`, `Write` | all `DropOrdinary` |
| `TestDropClassOf_PreservedTools` | `Task`, `Agent`, `AgentTool`, `mcp__qompack__recall`, `mcp__other__x`, `""`, `"NotebookEdit"` | all `DropNone` |
| `TestDropClassOf_EphemeralBeatsEverything` | `ephemeral=true` on an `mcp__` tool | `DropEphemeral` |
| `TestDropClassOf_SupersededBeatsToolClass` | `superseded=true` on `Task` | `DropSuperseded` |
| `TestDropClassOf_CaseInsensitive` | `"BASH"`, `" grep "` | `DropOrdinary` |
| `TestEvictionRank_Order` | — | `DropEphemeral(3) > DropSuperseded(2) > DropOrdinary(1) > DropNone(0)`, matching §8.7's "ahead of ordinary tool results" |

### `internal/daemon/scheduler_droppable_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestClassifyDrop_AdaptsStoreRecord` | `ToolUseRecord{Tool:"Bash"}`; `{Ephemeral:true}`; `{Status:StatusSuperseded}` | `DropOrdinary`, `DropEphemeral`, `DropSuperseded` — proves the adapter maps all three record fields onto `scheduler.DropClassOf` |
| `TestReclaimableIndex_SuffixSums` | blocks at `Pos` 10/20/30 with 5/7/11 tokens | `After(0)=23`, `After(10)=23`, `After(11)=18`, `After(30)=11`, `After(31)=0` |
| `TestReclaimableIndex_ExcludesDropNone` | one `DropNone` block of 100 tokens | not counted in any `After` |
| `TestReclaimableIndex_MonotoneNonIncreasing_Property` (rapid) | 1–200 random blocks, 1–50 random query positions | `After(p1) >= After(p2)` whenever `p1 <= p2` — §5.4's monotonicity, proven not assumed |
| `TestReclaimableIndex_Empty` | no blocks | `After(anything)==0` |

### `internal/daemon/scheduler_features_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestFeaturesFrom_PathJaccard` | prior window paths `{a,b,c}`, recent `{b,c,d}` | `2/4 = 0.5` |
| `TestFeaturesFrom_PathJaccard_EmptyUnionIsOne` | both windows empty | `1.0` |
| `TestFeaturesFrom_ToolShiftTotalVariation` | prior `[read,read,read,read]`, recent `[bash,bash,bash,bash]` | `1.0`; identical windows ⇒ `0.0`; half-and-half ⇒ `0.5` |
| `TestFeaturesFrom_GapSeconds` | `FakeClock`, previous ts `T`, current `T+45_000` | `45.0`; first observation ⇒ `0.0`; negative ⇒ `0.0` |
| `TestFeaturesFrom_TodoTransition` | each of `TodoCompleted`, `TestPassed`, `GitCommit` individually and none | `1,1,1,0` |
| `TestFeaturesFrom_WindowRoll` | 20 observations, window 8 | `recent` holds turns 13–20, `prior` holds 5–12; no unbounded growth after 10 000 observations (`len` of every internal slice ≤ 8) |
| `TestFeaturesFrom_LexicalCohesionShingleCap` | a 200 KB preview | ≤ `maxShingles` shingles retained; runs in < 10 ms |

### `internal/daemon/scheduler_candidates_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestAssemble_IntersectsChangepointsAndRounds` | cp turns `[5,10,15]`, rounds `{5,15}` | three candidates, `RoundBoundary` true for turns 5 and 15 only |
| `TestAssemble_SkipsUnresolvableTurns` | cp turn 99 has no DAG node | skipped; `obs.Counter("sched.candidate.unresolved")` == 1 |
| `TestAssemble_CouplingFromCrossingEdges` | `fakeGraph.CrossingEdges` returns `pos/1000` | `Coupling` matches per candidate |
| `TestAssemble_CrossingEdgesCached` | assemble twice with no graph mutation | `fakeGraph` records exactly 3 `CrossingEdges` calls, not 6 |
| `TestAssemble_CacheInvalidatedOnGraphChange` | bump `fakeGraph.Stats()` counts between calls | 6 calls |
| `TestAssemble_CapAt32KeepsHighestPos` | 100 cp turns | 32 candidates, the highest-`Pos` ones, ascending |
| `TestAssemble_ReclaimableFromIndex` | index with known suffix sums | each candidate's `ReclaimableTokens == idx.After(pos)` |
| `TestAssemble_NilGraphReturnsNoCandidates` | `graph = nil` | `nil, nil`, no panic |
| `TestAssemble_RecoversCrossingEdgesPanic` | `fakeGraph` panics on one `Pos` | that candidate dropped, others returned, one `Loud` recorded |

### `internal/daemon/scheduler_runtime_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestNewSchedulerRuntime_RequiresDeps` | omit `Store`, then `Graph`, then `Clock`, then `Log` | each returns a non-nil error naming the missing dependency; `PSelectionAvailable()` stays false |
| `TestNewSchedulerRuntime_EnablesPSelectionGate` | full deps | `PSelectionAvailable()` true after construction, false after `CloseSchedulerRuntime(rt)`; test `defer scheduler.DisablePSelection()` |
| `TestNewSchedulerRuntime_EmptySessionIsLegal` | `Session: ""` | construction succeeds; `Evaluate` before any bind returns `Breakdown["error_no_window"]==1` and `ShouldCompact==false`; after `BindSession("s1")` it evaluates normally |
| `TestRuntime_WindowResolutionLadder` | `Getenv` returning, in turn: `CLAUDE_CODE_AUTO_COMPACT_WINDOW=150000`; `QOMPACK_CONTEXT_WINDOW=250000` + `QOMPACK_MAX_OUTPUT_TOKENS=8000`; nothing | `effectiveWindow` `150_000` / `242_000` / `180_000` with `Breakdown["window_source"]` `3` / `2` / `1`; a value outside `[100_000, 1_000_000]` on rung 1 is rejected and falls through |
| `TestRuntime_ContextTokensFromSegments` | `fakeSegmentLog.Range` returning segments of 10 000 + 25 000 tokens | `Breakdown["context_tokens"] == 35_000`; `Range` erroring ⇒ `0` and no panic |
| `TestRuntime_ObserveDeclaresAndRecordsChangepoint` | feed `stepSeries` features via `Observe` | `cpTurns` contains the declaring turn; `obs.Counter("sched.changepoint") == 1` |
| `TestRuntime_ObserveClosesSegmentOnChangepoint` | `fakeSegmentLog` with an open segment | `Close(id, at, feats)` then `Open(StartTurn: at+1)` recorded, in that order |
| `TestRuntime_ObserveNoOpWhenAdvanceOnSegmentCloseFalse` | `cfg.Checkpoint.Frontier.AdvanceOnSegmentClose=false` | no `Close`, no `Open` |
| `TestRuntime_EvaluateProducesDecision` | 2 000 fake tool uses, 3 cp turns | `Decision.ShouldCompact` matches a hand-computed expectation; `Breakdown` non-empty |
| `TestRuntime_EvaluateSuppliesLambdaFromSelectionConfig` | `cfg.Selection.Submodular.Lambda = 0.4` | `Breakdown["lambda"] == 0.4` |
| `TestRuntime_DeltaPtrNilUntilMeasured` | fresh runtime | `Breakdown["young_daly_delta_unmeasured"] == 1`; after `RecordCompactionCost(20)` it is absent and `delta_seconds == 20` |
| `TestRuntime_DeltaEWMA` | `RecordCompactionCost` with 20, then 30 | `deltaEWMA == 20` then `0.3*30 + 0.7*20 = 23.0` |
| `TestRuntime_BurnRateEWMA` | `NotifyActivity` twice with +9 000 tokens over 60 s | first sample `9 000` tokens/min; second folded at `α=0.2` |
| `TestRuntime_NotifyActivitySetsAPICallClock` | `FakeClock` | `IdleSince()` returns `(ts, false)` immediately, `(ts, true)` after advancing past `detectAfterSeconds` |
| `TestRuntime_PersistRoundTrip` | observe 50 features, persist, construct a second runtime on the same root and `BindSession` with **the same id** | `State()` identical; `deltaEWMA`, `burnEWMA`, `cpTurns`, `rounds`, `frontier`, `residual`, `lastDecision` all restored |
| `TestRuntime_StateDiscardedOnSessionMismatch` | persist as `"s1"`, rebind as `"s2"` | fresh detector, zero `cpTurns`, zero EWMAs, `Info` logged, no error — cross-session carry-over is SP-16's O4, not this slice's |
| `TestRuntime_StateDiscardedOnHazardChange` | persist with hazard `0.004`, reload with `0.01` | fresh detector, `Warn` logged, no error returned |
| `TestRuntime_StateDiscardedOnFeatureListChange` | persist with 4 features, reload with 2 | fresh detector |
| `TestRuntime_CorruptStateFilesSelfHeal` | write garbage into both state files | bind succeeds, two `Loud` messages recorded, detector fresh |
| `TestRuntime_ChangepointTurnsCapped` | 10 000 declarations | `len(cpTurns) == maxTurnHistory`, oldest evicted; same for `rounds` |
| `TestRuntime_NoteAPIRoundRecordsBoundary` | `NoteAPIRound(7)` | turn 7 is a `RoundBoundary` in the next `Evaluate`'s candidates; turn 0 is one from bind |
| `TestRuntime_CloseIsIdempotent` | `CloseSchedulerRuntime` twice | both return nil; `Persist` ran once per call; the open draft is aborted exactly once |
| `TestRuntime_ConcurrentObserveEvaluatePersist` (`-race`) | 8 goroutines | no race, no deadlock over 5 000 operations |

### `internal/daemon/scheduler_state_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestStateCodec_BOCDRoundTrip` | write `state/bocd.json`, read it back | base64 field decodes to byte-identical `MarshalBinary` output; `hazard_rate`, `features`, `session` preserved |
| `TestStateCodec_SchedulerRoundTrip` | the document printed in the spec above | every field round-trips; `p_score == −22429.3` and equals `reclaimable − rewrite − distortion` |
| `TestStateCodec_UnknownFieldsIgnored` | add `"future_key": 1` to both files | load succeeds, no warning escalated beyond `Debug` |
| `TestStateCodec_WritesAtomicallyNotAppendOnly` | write both files twice | `paths.WriteAtomic` used (a temp file appears in `.qompack/tmp/` and is renamed); `paths.AppendOnly` never called; the second write replaces rather than appends |
| `TestStateCodec_TurnListsCapped` | 10 000 entries in each list | exactly `maxTurnHistory` written, oldest dropped |

### `internal/daemon/scheduler_tap_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestWrapServices_CallsInnerSeamsFirst` | `Services` with recording inner seams | every inner seam ran, its `hookio.Output` and error returned unchanged, tap work observed **after** it |
| `TestWrapServices_NilInnerSeamsTolerated` | `Services{}` (all nil) | no panic; `NotifyActivity` still observed |
| `TestWrapServices_ObserveToolDrivesObserve` | `fakeStore.ToolUse` returning a record at turn 9 | `rt.Observe` called once with turn 9 and features derived from that record |
| `TestWrapServices_ObserveStopRecordsRoundBoundary` | one `ObserveStop` at turn 9 | turn 9 in `rounds`; `ObserveTool` at turn 9 does **not** add one |
| `TestWrapServices_BoundarySignalsCloseSegment` | `Signals{TodoCompleted}`, then `{TestPassed}`, then `{GitCommit}` | `CloseSegmentOn` called once per event with cause `todo`/`test`/`commit` |
| `TestWrapServices_SessionStartBindsAndSessionEndCloses` | `SessionStart` then `SessionEnd` | `BindSession` with the event's id; `Persist` then `CloseSchedulerRuntime` on end |
| `TestWrapServices_PromptSeamDoesNoStoreIO` | `ObservePrompt` | `fakeStore` records zero calls; only `NotifyActivity` observed (the 250 ms reply deadline) |
| `TestWrapServices_MissingRecordCounted` | `ToolUse` returns `core.ErrNotFound` | `obs.Counter("sched.tap.no_record") == 1`; no `Observe`; inner output still returned |
| `TestWrapServices_NilRuntimeIsNoOp` | `rt = nil` | `Services` unchanged (same function pointers) |

### `internal/daemon/scheduler_frontier_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestFrontier_CloseOnTodoCompleted` | `Signals{TodoCompleted:true}` | segment closed and rolled |
| `TestFrontier_CloseOnTestPassed` | `Signals{TestPassed:true}` | same |
| `TestFrontier_CloseOnGitCommit` | `Signals{GitCommit:true}` | same |
| `TestFrontier_NoCloseWithoutCurrentSegment` | `Current` returns `core.ErrNotFound` | no error, no `Open` — SP-08 still owns the first open |
| `TestFrontier_AdvanceCallsWriterWithClosedUnencodedOnly` | 5 segments: 2 closed+unencoded, 1 open, 2 encoded | `Advance` receives exactly the 2 closed+unencoded ids, ascending |
| `TestFrontier_AdvanceNoWriterIsNoOp` | `ckpt = nil` | returns nil; `obs.Counter("sched.frontier.no_writer") == 1` |
| `TestFrontier_AdvanceIdempotent` | call twice in one window | second call moves nothing and returns nil |
| `TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes` | writer returns `core.ErrAlreadyEncoded` for one id | one `Loud`, retry with the remainder, frontier advances only for the clean ids; a second violation aborts the draft; `MarkEncoded` never called with a checkpoint-derived source |
| `TestFrontier_ResidualRecomputed` | context 40 000, encoded segments summing 31 000 | `residual == 9 000` |
| `TestFrontier_ResidualNeverNegative` | encoded sum exceeds context | `residual == 0` |
| `TestFrontier_ResidualOverBudgetWarnsOnce` | residual 25 000 > `maxResidualTokens` 20 000, nothing to encode | exactly one `Warn` per session, gauge set to 1 |

### `internal/daemon/scheduler_idle_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestIdleTasksRegistered` | `RegisterSchedulerIdleWork` | exactly six names in priority order `act.advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc` with priorities 10…60 |
| `TestIdleActingTaskSkippedInDegradedPassive` | `IdleController` whose `mode()` reports `ModeDegradedPassive` | `RunOnce` returns the five unprefixed names and **not** `act.advance_frontier` — 00-ARCHITECTURE §12.1's "no scheduler-initiated checkpoints", enforced by SP-05's prefix rule |
| `TestIdleWorkBindsDaemon` | `RegisterSchedulerIdleWork(d, rt, o)` | `r.d == d` afterwards; calling it twice is idempotent |
| `TestIdleTaskInertWithoutEvaluate` | run tasks before any `Evaluate` | every task returns nil and performs no work |
| `TestIdleTaskGatedByDecisionBackground` | last decision `Background = ["advance_frontier"]` | only that task does work; `gc` and `compact_dag` are no-ops |
| `TestIdleTasksOffWhenBackgroundWorkFalse` | `cfg.Idle.BackgroundWork=false` | all six inert |
| `TestIdleTaskRebuildBloomSkippedWhenLedgerNil` | `Ledger=nil` | no panic, no work |
| `TestIdleTaskGCPassesRemainingDeadline` | `RunOnce` budget 500 ms | `GCPolicy.Deadline > 0` and ≤ 500 ms; `RetainDays/RetainSessions` from config |
| `TestIdleTasksHonourContextCancellation` | cancel mid-run | every task returns within 5 ms of cancellation |

### Benchmarks (each names its budget)

| Benchmark | Budget | Notes |
|---|---|---|
| `BenchmarkEvaluate_64Candidates` | **≤ 50 µs/op, ≤ 8 allocs/op** | 00-ARCHITECTURE §7 named micro-benchmark; `benchstat` fails >25% regression against `testdata/bench-baseline.txt` |
| `BenchmarkBOCDObserve_4Features` | **≤ 150 µs/op** at a full 512-entry posterior; **≤ 20 µs/op** at the steady-state pruned length | proves the §6.6 "O(1) amortized" claim is met in wall-clock, not just in asymptotics |
| `BenchmarkBOCDMarshal` | **≤ 2 ms/op** at 512 entries × 4 features | bounds the idle-tick persist cost |
| `BenchmarkFeaturesFrom` | **≤ 100 µs/op** | runs on the async B-C path (50 ms) |
| `BenchmarkAssembleCandidates_2000ToolUses` | **≤ 20 ms/op cold, ≤ 200 µs/op warm** | the `CrossingEdges` cache is the difference |
| `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates` | **≤ 25 ms/op** | never on B-A; asserted to be invoked only from the idle worker and the `status` op by an import/call-site test |
| `BenchmarkReclaimableIndexBuild_5000Blocks` | **≤ 3 ms/op** | |
| `BenchmarkSchedulerTap_ObserveTool` | **≤ 1.5 ms/op** | one store lookup + `FeaturesFrom` + `Observe`; runs on B-C (50 ms), never on B-A |

A guard test, `TestSchedulerNotOnHotPath`, asserts by call-graph inspection of `internal/cli` that no hook subcommand path reaches `scheduler.Evaluate`, `schedRuntime.Evaluate` or `WrapServicesForScheduler`, protecting budget B-A (< 15 ms p99). The tap runs daemon-side on the worker pool, which is B-C.

### `test/replay/l3policy/policy_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestPolicy_NameIsQompackL3` | — | `New(config.Defaults()).Name() == "qompack-l3"` |
| `TestPolicy_Deterministic` | the same synthetic session replayed twice with `Seed: 1` | byte-identical `KeepSet` and `Run`, including `P`, `PauseMS` and `ResidualSpan` |
| `TestPolicy_KeepSetShape` | a session with 3 droppable and 2 preserved tool calls after `P` | `IDs` = everything before `P` plus the 2 preserved; the 3 droppable are absent; `Tokens` equals their sum |
| `TestPolicy_DoesNotImportDaemon` | `go list -deps ./test/replay/l3policy` | the dependency list contains `internal/scheduler` and **not** `internal/daemon` (00-ARCHITECTURE §3.2: nothing imports a composition root) |
| `TestPolicy_PauseModelAnchors` | residual 150 000 then 20 000 | `21_000 ms` then `5_400 ms`, the §6.7 "15–40 s" band and the §8.5 "10–20K residual" case respectively |

### `test/replay/phase4_test.go` — the Phase 4 exit criterion

Runs against SP-02's committed 24-session synthetic corpus in `testdata/sessions/synthetic/` with `ReplayOptions{Deterministic: true, Seed: 1}`. Policies compared: SP-02's `stock` baseline and `l3policy.New(config.Defaults())` (`qompack-l3`).

| Test | Assertion |
|---|---|
| `TestPhase4_RewriteTokensReduced` | `Σ Score.RewriteTokens[qompack-l3] ≤ 0.80 × Σ Score.RewriteTokens[stock]` across all 24 sessions. This is the direct measurement of "measured reduction in total rewrite tokens per session". The per-session table is written to `.qompack/eval/phase4-rewrite.json` for the PR comment. |
| `TestPhase4_NoDivergenceRegression` | For `FirstDivergenceTurn`, `FileSetJaccard`, `DecisionPreservation`, `RedundantReads` and `ReAttempts`, `qompack-l3` regresses by **≤ 2%** against `stock` (§11.3). A regression beyond 2% fails unless the PR body carries a `sign-off:` trailer, matching the `replay-gate` rule. |
| `TestPhase4_ResidualSpanFlatAsSessionGrows` | Least-squares slope of `median(Score.ResidualSpan)` against session turn count over the 24 sessions is **≤ 0.02 tokens/turn**, and `median(ResidualSpan)` for the longest quartile is **≤ 1.25 ×** that of the shortest quartile. This is the amortization claim tested directly: *"median compaction pause and residual span flat as session length grows"*. |
| `TestPhase4_ResidualUnderMaxResidualTokens` | `Score.ResidualSpan.P95 ≤ config.Defaults().Checkpoint.Frontier.MaxResidualTokens` (20 000), matching §8.5's "10–20K tokens rather than 150K, regardless of how long the session has run". |
| `TestPhase4_CompactionPauseModelled` | `Score.CompactionPauseMS` is a **linear function of residual span**: for every run, `PauseMS[i] == round(pauseInterceptMS + pauseMSPerToken × ResidualSpan[i])`, asserted exactly. The modelled-ness is recorded by SP-12's own artifact — `test/replay/phase4_test.go` writes `.qompack/eval/phase4-pause.json` containing `{"pause_modelled": true, "intercept_ms": 3000, "ms_per_token": 0.12, "per_session": […]}` — and **not** by inventing a field on `eval.Report` or `eval.Score`, neither of which has one (00-ARCHITECTURE §5.18). Deterministic replay makes no model call, so reporting a "measured" pause would be dishonest measurement — precisely the sin §1.3 RC-3 indicts — and the flatness claim is carried by residual span, which is real. |
| `TestPhase4_PSelectionGateHonoured` | With `scheduler.DisablePSelection()` in effect, `l3policy` still produces a `KeepSet` (p-selection is not submodular selection) but `analyzer.NewSelector` refuses to construct — the closing-note-3 boundary asserted from both sides. |

---

## Commit plan

Seven commits, in order, on `feat/sp12-scheduler-l3` cut from `develop`. Each commit compiles and passes `go run ./tools/devtool test` for the packages it touches. Conventional Commits per 00-ARCHITECTURE §10: `<type>(<scope>): <subject>`, subject imperative and ≤ 72 chars, body explaining the decision, footer `Refs:`.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is absolute and applies to every commit, merge commit, tag message and PR body here. CI's `verify` job greps for `Co-Authored-By`, `Signed-off-by`, `Generated with` and `🤖` in the commit range and fails the build if any appear.

### Setup (before commit 1)

- [ ] `git fetch origin && git checkout develop && git pull` — confirm SP-01, SP-05, SP-06, SP-07 and SP-08 are merged (`git log --oneline --merges | head -8` shows all five).
- [ ] `git checkout -b feat/sp12-scheduler-l3`
- [ ] `go run ./tools/devtool ci-local` on the clean branch point — record that it is green *before* any SP-12 change, so a later failure is unambiguously ours.
- [ ] Optional worktree: `git worktree add ../qompack-sp12 feat/sp12-scheduler-l3`.

---

### Commit 1 — `feat(scheduler): add BOCD detector with pruned updates and versioned state`

Adds the changepoint detector of `Qompack.md` §6.6 with a Normal-Inverse-Gamma observation model, hazard-rate run-length posterior, tail pruning that bounds the update cost independently of session length, and a CRC-checked binary state so the daemon can persist it across restarts.

**Files added:** `internal/scheduler/bocd.go`, `internal/scheduler/bocd_test.go`, `internal/scheduler/testdata_test.go`.
**Files modified:** `internal/scheduler/doc.go`.

- [ ] Write `internal/scheduler/testdata_test.go` (`baseCfg`, `baseInputs`, `stepSeries`) and all 15 `bocd_test.go` cases first.
- [ ] Run `go test ./internal/scheduler/ -run TestBOCD` — **must fail** (SP-01's stub returns `core.ErrNotImplemented`).
- [ ] Implement `bocd.go`: `NewBOCD`, `featureVector`, `logPredictive`, `ngUpdate`, `Observe`, `prune`, `State`, `Reset`, `MarshalBinary`, `UnmarshalBinary`, the lgamma tables.
- [ ] `go test ./internal/scheduler/ -run TestBOCD -race` — all green.
- [ ] `go test ./internal/scheduler/ -run TestBOCD -count=2` — determinism holds across runs.
- [ ] `go run ./tools/devtool fmt lint vet`.
- [ ] Commit body: why Normal-Inverse-Gamma (conjugate ⇒ closed-form Student-t predictive ⇒ no sampling on the async path), why the lgamma table (α advances by exactly ½ per run index, so the two gamma terms are a pure function of the index), and why pruning is what makes §6.6's "O(1) amortized" real.
- [ ] Footer: `Refs: SP-12, G1.1, G1.5, §6.6, §8.4`

---

### Commit 2 — `feat(scheduler): add thresholds, sliding-TTL, Young-Daly, ski rental and drop classes`

Adds the pure scalar machinery the composite trigger composes: §2.5's `EffectiveWindow` and the 55%/hard-ceiling thresholds measured against the host's own arithmetic, the E1 sliding-TTL idle model that keys on the last API call rather than the last cache write, the `√(2·δ·M)` interval with the `*float64` null-means-measure contract, the `w/r` ski-rental helper, and the §2.2/§8.7 droppable-block ranking as foundation-only spec so `test/replay` can share it without importing a composition root.

**Files added:** `internal/scheduler/thresholds.go`, `internal/scheduler/ttl.go`, `internal/scheduler/youngdaly.go`, `internal/scheduler/skirental.go`, `internal/scheduler/dropclass.go`, and their `_test.go` peers.

- [ ] Write `thresholds_test.go`, `ttl_test.go`, `youngdaly_test.go`, `skirental_test.go`, `dropclass_test.go` first (6 + 6 + 8 + 2 + 6 cases), including `TestEffectiveWindow_Section25Arithmetic`, `TestSlidingTTLUsesAPICallNotCacheWrite` and `TestResolveDelta_NilMeansMeasureNotZero`.
- [ ] Run `go test ./internal/scheduler/` — **must fail** on undefined symbols.
- [ ] Implement the five files.
- [ ] `go test ./internal/scheduler/ -race` — green.
- [ ] `go run ./tools/devtool lint` — the `nomagic` pass must be clean; verify `12.5`, `0.55`, `20000`, `300` and `0.1`/`1.25` appear only in `_test.go`, and that all four host constants (`HostAutoCompactBuffer`, `HostMaxOutputCap`, `HostDefaultContextWindow`, `HostDefaultMaxOutput`) carry their `//nomagic:allow` annotations.
- [ ] Footer: `Refs: SP-12, G1.2, G1.3, G1.4, G5.2, §2.2, §2.5, §5.4, §6.7, §8.4, §8.7`

---

### Commit 3 — `feat(scheduler): implement the composite trigger and cache-aware p-selection`

Makes `Evaluate` real: the four-clause trigger gated on the soft floor, candidate filtering to changepoint ∩ API-round boundaries, the `reclaimable·r − rewrite − λ·coupling` argmax with the TTL-aware tie-break, the O3 background plan, the full `Breakdown`, and the `PSelectionAvailable` gate that keeps SP-15's submodular selection inert until a real runtime exists.

**Files added:** `internal/scheduler/evaluate.go`, `internal/scheduler/pselect.go`, `internal/scheduler/gate.go`, `internal/scheduler/evaluate_test.go`, `internal/scheduler/pselect_test.go`, `internal/scheduler/gate_test.go`, `testdata/golden/scheduler/decision-{warm,expiring,cold}.json`.
**Files modified:** `internal/scheduler/types.go` (the two additive `Inputs` fields and the reason/TTL/task constants), `internal/scheduler/schedulertest/suite.go` (flip the skips).

- [ ] Write all 28 `evaluate_test.go` cases, the 8 `pselect_test.go` cases and the 3 gate tests first; generate the three golden `Decision` documents by hand from the worked arithmetic in this plan (the `baseInputs()` warm/cold score tables), not by capturing implementation output.
- [ ] Run `go test ./internal/scheduler/ -run TestEvaluate` — **must fail** (stub returns a zero `Decision`).
- [ ] Add the two `Inputs` fields with the ADR-referenced comment block.
- [ ] Implement `pselect.go` then `evaluate.go` then `gate.go`.
- [ ] Flip every `t.Skip` in `internal/scheduler/schedulertest/`; `grep -R "t.Skip" internal/scheduler/schedulertest/` must return nothing.
- [ ] `go test ./internal/scheduler/... -race -count=2` — green.
- [ ] `go test ./internal/scheduler/ -run TestEvaluate_ScoreArithmeticExact -v` — confirm `−1629.3` exactly.
- [ ] `go run ./tools/devtool lint vet` — including the import-graph check proving `scheduler` still imports foundation packages only.
- [ ] Footer: `Refs: SP-12, G1.1, G5.1, G5.2, G7.1, G7.6, G8.2, §5.3, §5.4, §8.4`

---

### Commit 4 — `feat(daemon): classify droppable blocks and assemble p-selection candidates`

Adds the wave-3 half of `reclaimable(p)`: the `store.ToolUseRecord` adapter onto commit 2's ranking, an O(log N) suffix-sum index that makes §5.4's monotonicity structural, the `observer.Signals` → `scheduler.Features` translation that keeps `observer` free of a `scheduler` import, and candidate assembly with a memoized turn→`Pos` map and memoized `CrossingEdges`.

**Files added:** `internal/daemon/scheduler_droppable.go`, `internal/daemon/scheduler_features.go`, `internal/daemon/scheduler_candidates.go`, `internal/daemon/scheduler_testhelpers_test.go`, and the three `_test.go` peers (5 + 7 + 9 cases).

- [ ] Write `scheduler_droppable_test.go`, `scheduler_features_test.go`, `scheduler_candidates_test.go` and the fake `store`/`dag`/`SegmentLog` helpers first.
- [ ] Run `go test ./internal/daemon/ -run 'TestClassifyDrop|TestAssemble'` — **must fail** on undefined symbols.
- [ ] Implement the three files.
- [ ] `go test ./internal/daemon/ -race` — green, including the rapid monotonicity property.
- [ ] `go test ./internal/daemon/ -bench BenchmarkFeaturesFrom -benchtime 2000x` — confirm ≤ 100 µs/op.
- [ ] Footer: `Refs: SP-12, G1.5, G5.1, §2.2, §5.4, §8.1, §8.4, §8.7`

---

### Commit 5 — `feat(daemon): implement scheduler.Runtime with persisted BOCD and burn state`

Implements the stateful wrapper the daemon owns: session binding and the §2.5 window-resolution ladder, feature history, candidate assembly, the measured-δ and burn-rate EWMAs, session-scoped `state/bocd.json` and `state/scheduler.json` with self-healing on corruption, the `SchedulerSnapshot` SP-14 will render, the `Options.Bind` tap that is the only path by which L0 events reach L3, and the three-block wiring in the composition root.

**Files added:** `internal/daemon/scheduler_runtime.go`, `internal/daemon/scheduler_state.go`, `internal/daemon/scheduler_tap.go`, `internal/daemon/scheduler_runtime_test.go`, `internal/daemon/scheduler_state_test.go`, `internal/daemon/scheduler_tap_test.go`.
**Files modified:** `cmd/qompack/main.go` (blocks 1 and 2 of the `daemon` subcommand wiring — construct and tap; block 3, idle registration, lands with commit 6 because that is where the tasks exist).

- [ ] Write all 23 `scheduler_runtime_test.go` cases, the 5 `scheduler_state_test.go` cases and the 9 `scheduler_tap_test.go` cases first.
- [ ] Run `go test ./internal/daemon/ -run 'TestRuntime|TestStateCodec|TestWrapServices'` — **must fail**.
- [ ] Implement `scheduler_state.go`, then `scheduler_runtime.go`, then `scheduler_tap.go`, then add the `main.go` wiring — registering the `Bind` hook **after** every pre-existing `opts.Bind` call so the tap decorates seams SP-08 has already set.
- [ ] `go test ./internal/daemon/... -race -count=2` — green, including `TestRuntime_ConcurrentObserveEvaluatePersist`.
- [ ] `go build ./...` and `go run ./tools/devtool build-all` — the composition-root change must cross-compile for all six targets.
- [ ] `go run ./tools/devtool lint` — confirm the import-graph check still passes and `cmd/qompack/main.go` stays under 150 LOC.
- [ ] Footer: `Refs: SP-12, G1.3, G8.2, §8.4, 00-ARCHITECTURE §5.13`

---

### Commit 6 — `feat(daemon): add O3 idle background work and O5 frontier advancement`

Registers the six idle tasks on SP-05's `IdleController` without editing daemon internals, and implements continuous frontier advancement: segment close on changepoint/todo-completion/passing-test, `checkpoint.Writer.Advance` over closed-and-unencoded segments with a hard DPI guard, and residual-span accounting held under `maxResidualTokens`.

**Files added:** `internal/daemon/scheduler_frontier.go`, `internal/daemon/scheduler_idle.go`, `internal/daemon/scheduler_frontier_test.go`, `internal/daemon/scheduler_idle_test.go`, `test/e2e/scheduler_idle_test.go`.
**Files modified:** `cmd/qompack/main.go` (block 3: `RegisterSchedulerIdleWork` after `daemon.New`).

- [ ] Write both test files first (11 + 9 cases), including `TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes`, `TestIdleTaskGatedByDecisionBackground` and `TestIdleActingTaskSkippedInDegradedPassive`.
- [ ] Run `go test ./internal/daemon/ -run 'TestFrontier|TestIdle'` — **must fail**.
- [ ] Implement `scheduler_frontier.go` then `scheduler_idle.go`; wire `RegisterSchedulerIdleWork` into `main.go`'s post-`daemon.New` block. Register the acting task as **`act.advance_frontier`** and the other five unprefixed — SP-05's `RunOnce` uses that prefix to suppress scheduler-initiated checkpoints in `degraded-passive`.
- [ ] `go test ./internal/daemon/... -race` — green.
- [ ] `go test ./test/e2e/ -run TestDaemonIdleRunsSchedulerWork` — the daemon starts, a hook payload flows through the tap, the session goes idle, `act.advance_frontier` runs against the SP-01 `checkpoint.Writer` stub, and no `Loud` is logged beyond the expected `sched.frontier.no_writer` counter.
- [ ] Footer: `Refs: SP-12, G1.5, G7.1, §8.2, §8.4, §8.5`

---

### Commit 7 — `test(scheduler): add Phase 4 replay harness, benchmarks and the L3 ADR`

Closes Phase 4 with the exit-criterion harness, the seven micro-benchmarks and their budgets, the hot-path guard test, and the ADR recording the four decisions this slice made that a reader would otherwise have to reverse-engineer.

**Files added:** `test/replay/l3policy/policy.go`, `test/replay/l3policy/policy_test.go`, `test/replay/phase4_test.go`, `internal/scheduler/bench_test.go`, `internal/daemon/scheduler_bench_test.go`, `docs/adr/0012-scheduler-l3.md`.
**Files modified:** `testdata/bench-baseline.txt` (append the seven new benchmark baselines).

- [ ] Write `phase4_test.go` (6 cases) and `l3policy/policy_test.go` (5 cases) first against the 24-session synthetic corpus; run them — **must fail** because `l3policy` does not exist.
- [ ] Implement `l3policy/policy.go`; iterate until all six Phase 4 assertions and all five policy tests pass. It imports `internal/scheduler` and must **not** import `internal/daemon` — `TestPolicy_DoesNotImportDaemon` enforces it.
- [ ] Add the 8 benchmarks and `TestSchedulerNotOnHotPath`.
- [ ] `go run ./tools/devtool bench` then `benchstat` against `develop` — record the baselines; no benchmark may exceed its stated budget.
- [ ] `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop` — the `replay-gate` must be green with no `sign-off:` trailer required.
- [ ] Write `docs/adr/0012-scheduler-l3.md` covering all seven decisions: (1) the two additive `Inputs` fields and why they need no §0 amendment, (2) the δ precedence order and why config-non-nil wins, (3) the linear cache-factor ramp across the expiring band rather than a cliff, (4) the cap-at-32 candidate rule and why the highest-`Pos` boundaries are kept, (5) the §2.5 window-resolution ladder and why no Appendix C key was added for it, (6) why `DropClass` lives in `scheduler` and `ClassifyDrop` is a `daemon` adapter (the `test/replay` import rule of §3.2), (7) why L0 events reach L3 through `Options.Bind` seam decoration rather than a new op route or an edit to SP-05/SP-08.
- [ ] `go run ./tools/devtool ci-local` — full local pipeline green.
- [ ] Footer: `Refs: SP-12, G8.1, G8.3, §10 Phase 4, §11.2, §11.3`

---

### Before opening the PR

- [ ] `go run ./tools/devtool ci-local` green.
- [ ] `git log --format=%B develop..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'` returns nothing.
- [ ] `git log --oneline develop..HEAD | wc -l` reports exactly 7.
- [ ] `go run ./tools/devtool cover` — `scheduler` ≥ 85% (00-ARCHITECTURE §6.4).
- [ ] PR body states the Phase 4 numbers: rewrite-token reduction ratio, the five divergence deltas, and the residual-span slope.

---

## Subagent strategy

This subplan is **heavy**: two packages, twelve new source files, 173 enumerated tests and 8 benchmarks, one numerical algorithm with a serialization format, and a replay harness. Partition it across parallel subagents in four dependency-ordered rounds. **The commit plan stays strictly sequential in the main session** — subagents produce file contents and test results, they never commit, never touch git, and never open a branch.

### What must stay in the main session, always

- All `git` operations: branch creation, staging, commit messages, ordering, the seven-commit boundary.
- The two additive `Inputs` fields and every other change to `internal/scheduler/types.go`. This is the one place where two subagents could silently disagree, so exactly one actor edits it: you, before Round B starts, with the field names and comments transcribed verbatim from the Interface contract section above.
- The `Breakdown` key list and the three golden `Decision` documents. Every downstream surface (SP-14 status, SP-02 eval) keys off these; they are a contract, not an implementation detail.
- Final integration: `go build ./...`, `devtool lint`, the import-graph check, `benchstat`, `ci-local`.
- Any decision a subagent reports as ambiguous. Subagents are instructed to stop and report rather than invent — this plan contains no open decisions, so an ambiguity means a spec bug the main session resolves once, centrally.

### Round A — three subagents in parallel (no shared files)

| Subagent | Owns | Returns |
|---|---|---|
| **A1 — BOCD** | `internal/scheduler/bocd.go` + `bocd_test.go` | Full file contents; a table of the 15 test names with pass/fail; the measured `BenchmarkBOCDObserve` numbers at 512-entry and steady-state posteriors; the exact serialized byte length at 512×4 |
| **A2 — scalars** | `internal/scheduler/thresholds.go`, `ttl.go`, `youngdaly.go`, `skirental.go`, `dropclass.go` + tests | Full file contents; the worked-example table (180 000 / 99 000 / 147 000 / 268.328 / the cache-factor ramp) confirmed by test output |
| **A3 — droppable + features** | `internal/daemon/scheduler_droppable.go`, `scheduler_features.go` + tests + the fake `store`/`dag`/`SegmentLog` helpers | Full file contents; the rapid monotonicity property's pass output; `BenchmarkFeaturesFrom` and `BenchmarkReclaimableIndexBuild` numbers. **Depends on A2's `scheduler.DropClassOf`** — hand A3 that four-line signature up front; it must not redefine the tool table |

A1, A2 and A3 share no file. A3's fake helpers are the only artifact later rounds reuse, so A3 must return them as a standalone file with no dependency on A1 or A2.

**Integration point 1.** Main session lands A1 as commit 1 and A2 as commit 2, running `devtool lint vet` between them. A3's output is held for commit 4.

### Round B — two subagents in parallel

| Subagent | Owns | Returns |
|---|---|---|
| **B1 — Evaluate + p-selection** | `internal/scheduler/evaluate.go`, `pselect.go`, `gate.go` + tests + the three golden `Decision` JSON files | Full file contents; the 28-case result table; the exact `PScore` for the arithmetic test; `BenchmarkEvaluate` µs/op and allocs/op |
| **B2 — candidates** | `internal/daemon/scheduler_candidates.go` + tests | Full file contents; the `CrossingEdges` call-count evidence for the cache test; `BenchmarkAssembleCandidates` cold/warm numbers |

B1 depends on A2's symbols and on the main session having already added the two `Inputs` fields. B2 depends on A3's `reclaimableIndex` and the fakes. Neither depends on the other. Hand B1 the golden-document arithmetic from this plan explicitly so it derives the goldens from the spec rather than from its own output.

**Integration point 2.** Main session lands commit 3 (B1 + the schedulertest skip flip) then commit 4 (A3 + B2), running the full `./internal/...` test suite with `-race` between them.

### Round C — two subagents, sequential with a narrow overlap

| Subagent | Owns | Returns |
|---|---|---|
| **C1 — Runtime + state + tap** | `internal/daemon/scheduler_runtime.go`, `scheduler_state.go`, `scheduler_tap.go` + tests, and the exact `cmd/qompack/main.go` diff (blocks 1 and 2) | Full file contents; the `main.go` diff as a patch, not a rewritten file; the 23 + 5 + 9 case result tables; the two JSON state documents produced by a real round-trip |
| **C2 — frontier + idle** | `internal/daemon/scheduler_frontier.go`, `scheduler_idle.go` + tests, `test/e2e/scheduler_idle_test.go`, and the `main.go` block-3 diff | Full file contents; the six registered task names with priorities (`act.advance_frontier` first); the DPI-guard test transcript showing the batch-drop-and-retry path |

C2 needs `schedRuntime`'s field set and method receivers from C1, so give C2 C1's struct definition and method signatures **before** it starts — that is a 40-line contract, not the whole file, and it keeps the two agents from redefining the same type. Run C1 to completion first if the struct is not yet settled; otherwise run them in parallel with that contract fixed.

**Integration point 3.** Main session lands commit 5 (C1) then commit 6 (C2), running `devtool build-all` after commit 5 because it is the one that touches the composition root.

### Round D — one subagent

| Subagent | Owns | Returns |
|---|---|---|
| **D1 — Phase 4 + benchmarks + ADR** | `test/replay/l3policy/`, `test/replay/phase4_test.go`, both `bench_test.go` files, `docs/adr/0012-scheduler-l3.md` | Full file contents; the six Phase 4 assertion outcomes with the actual measured numbers (rewrite ratio, five divergence deltas, residual slope, P95 residual); the seven benchmark results against their budgets |

D1 needs the whole branch built, so it runs alone after commit 6.

**Integration point 4.** Main session lands commit 7, runs `ci-local`, updates `testdata/bench-baseline.txt`, and opens the PR.

### Instructions every subagent receives verbatim

1. Do not run `git`. Return file contents and test output; the main session commits.
2. Do not edit any file outside your listed set. If you need a symbol that does not exist, report it — do not create it in someone else's file.
3. Write tests first, run them, and include the failing output in your report before the passing output.
4. No literal from `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` or `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` outside `_test.go`.
5. `internal/scheduler` may import only `core`, `paths`, `config`, `logging`, `obs` plus the standard library. If you believe you need another import, stop and report.
6. No `TODO`, no `panic("unimplemented")`, no stubbed branches. Every path in this plan is fully specified.
7. Every test touching time uses `testutil.FakeClock`. `time.Sleep` is forbidden.

---

## Exit criteria

### The `Qompack.md` phase exit criterion, verbatim (§10 Phase 4)

> **Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).

Operationalized and enforced by `test/replay/phase4_test.go`:

- [ ] `Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock]` over the 24-session synthetic corpus.
- [ ] No divergence metric (`FirstDivergenceTurn`, `FileSetJaccard`, `DecisionPreservation`, `RedundantReads`, `ReAttempts`) regresses by more than 2% (§11.3), with no `sign-off:` trailer required.
- [ ] Least-squares slope of median residual span against session turn count ≤ 0.02 tokens/turn, and the longest-quartile median ≤ 1.25× the shortest-quartile median.
- [ ] `ResidualSpan.P95 ≤ 20 000` (`checkpoint.frontier.maxResidualTokens`).
- [ ] Compaction pause is derived linearly from residual span (`3000 ms + 0.12 ms/token`), recorded in `.qompack/eval/phase4-pause.json` with `"pause_modelled": true`, and never presented as a measured wall-clock in deterministic replay.

### The applicable §11.3 guardrails, verbatim

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

- [ ] `devtool bench-hotpath -n 2000` on linux, macos and windows: B-A p99 < 15 ms, unchanged from the pre-SP-12 baseline within noise. `TestSchedulerNotOnHotPath` proves no hook path reaches `Evaluate`.
- [ ] `replay-gate` green on the branch.

### Local criteria

- [ ] All enumerated tests green: `go test ./... -race` on linux and macos, `go test ./... -count=2` on windows.
- [ ] `grep -R "t.Skip" internal/scheduler/schedulertest/` returns nothing (Rule W-1: the owning subplan flips every skip).
- [ ] `gofumpt -l .` empty; `golangci-lint run` clean; `go vet ./...` clean; the in-repo `nomagic` pass clean.
- [ ] Import-graph check confirms `internal/scheduler` imports only `core`, `paths`, `config`, `logging`, `obs`.
- [ ] Coverage: `internal/scheduler` ≥ **85%** (00-ARCHITECTURE §6.4 group 2). The SP-12-owned files in `internal/daemon` are counted in the `daemon` group's 75% floor and must not lower it.
- [ ] Benchmarks within budget: `Evaluate` ≤ 50 µs/op and ≤ 8 allocs/op; `BOCD.Observe` ≤ 150 µs/op at full posterior and ≤ 20 µs/op at steady state; `BOCD.MarshalBinary` ≤ 2 ms/op; `FeaturesFrom` ≤ 100 µs/op; `AssembleCandidates` ≤ 20 ms cold / ≤ 200 µs warm; `Runtime.Evaluate` ≤ 25 ms/op; `reclaimableIndex` build ≤ 3 ms/op; `SchedulerTap.ObserveTool` ≤ 1.5 ms/op. `benchstat` shows no >25% regression on any pre-existing benchmark.
- [ ] `devtool build-all` cross-compiles all six release targets.
- [ ] `devtool plugin-validate` and the `docs` job unaffected (this slice adds no config key and no manifest entry).
- [ ] `scheduler.PSelectionAvailable()` returns **true** after `NewSchedulerRuntime` succeeds and **false** in a build where no runtime has been constructed — asserted from both sides, so SP-15 can rely on the gate.
- [ ] CI green on `feat/sp12-scheduler-l3`: `verify`, `test`, `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] Exactly 7 commits, no attribution trailers, every message Conventional-Commit shaped with a `Refs:` footer.

---

## Done checklist

- [ ] Branch `feat/sp12-scheduler-l3` cut from a `develop` containing merged SP-01, SP-05, SP-06, SP-07 and SP-08.
- [ ] `Qompack.md` untouched — `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] `plans/00-ARCHITECTURE.md` untouched — no §5 signature changed or removed; the only additions are two fields on `scheduler.Inputs`, a struct `internal/scheduler` owns, documented in `docs/adr/0012-scheduler-l3.md`.
- [ ] **Spec coverage self-review against the Design context section.** Walk each quoted block and point at the code that implements it: the four-clause trigger (`evaluate.go`); `soft_floor` at 55% and `hard_ceiling` one turn below the host threshold (`thresholds.go`); `candidates = changepoint ∩ API-round` (`pselect.go` `eligible` + `scheduler_candidates.go`); `reclaimable(p)` (`scheduler_droppable.go`); `rewrite(p) = w·(n−p)`, zero when cold (`pselect.go` + `ttl.go`); `distortion(p) = λ·coupling` (`pselect.go` via `dag.CrossingEdges`); `argmax` (`chooseP`); the sliding-TTL correction keyed on last API call (`ttl.go`, `Runtime.NotifyActivity`); `√(2·δ·M)` with measured δ (`youngdaly.go`, `RecordCompactionCost`); BOCD over paths/tools/time/todos with pruning (`bocd.go`); O3's four named activities (`scheduler_idle.go`); O5 segment close and frontier advance (`scheduler_frontier.go`); the §2.2 compactable tool set and the §8.7 ephemeral-first ranking (`scheduler.DropClassOf` + `daemon.ClassifyDrop`); §2.5's `effectiveContextWindow` arithmetic and the window-resolution ladder (`thresholds.go`, `BindSession`); §2.6's API-round boundary (`NoteAPIRound`, fed by the `ObserveStop` wrapper); the §8.2 encoded-once DPI guard (`advanceFrontier`'s `ErrAlreadyEncoded` path); Phase 4's exit criterion (`phase4_test.go`).
- [ ] **Event path proven end to end.** `TestWrapServices_*` plus `test/e2e/scheduler_idle_test.go` show a real hook payload reaching `Observe`, a `Stop` recording a round boundary, and an idle tick running the six tasks — the scheduler is wired, not merely written.
- [ ] `act.advance_frontier` is the only prefixed idle task, and `TestIdleActingTaskSkippedInDegradedPassive` proves it is suppressed in `degraded-passive` while the other five keep running.
- [ ] **Placeholder scan.** `grep -RniE 'TODO|FIXME|TBD|XXX|unimplemented|not implemented|handle .* appropriately' internal/scheduler internal/daemon/scheduler_* test/replay/l3policy docs/adr/0012-scheduler-l3.md` returns nothing. No function returns `core.ErrNotImplemented` in any SP-12-owned file.
- [ ] **Type consistency with the Interface contract.** Every signature in the Produces block compiles exactly as written: `Evaluate(Inputs) Decision`, `NewBOCD(float64, []string) Detector`, `YoungDaly(float64, float64) float64`, `SkiRentalShouldWrite(float64, float64, float64) bool`, `PSelectionAvailable() bool`, `EffectiveWindow`/`SoftFloor`/`HardCeiling`/`ClassifyTTL`/`CacheFactor`, `DropClassOf(string, bool, bool) DropClass`, `ClassifyDrop(store.ToolUseRecord) DropClass`, `NewSchedulerRuntime(SchedulerRuntimeOptions) (scheduler.Runtime, error)`, `RegisterSchedulerIdleWork(Daemon, scheduler.Runtime, SchedulerRuntimeOptions) error`, `WrapServicesForScheduler(*Services, scheduler.Runtime, SchedulerRuntimeOptions)`, `CloseSchedulerRuntime(scheduler.Runtime) error`, `PrecomputedSlice(scheduler.Runtime) (dag.Slice, bool)`, `FeaturesFrom(*FeatureHistory, observer.Signals, string, core.UnixMilli) scheduler.Features`. `TriggerReason` string values match §5.13 exactly: `soft_floor`, `changepoint`, `young_daly`, `hard_ceiling`, `idle_cold_cache`. `BackgroundTask` values match: `advance_frontier`, `gc`, `precompute_slice`, `refresh_delta`, `rebuild_bloom`, `compact_dag`. `TTLState` values match: `warm`, `expiring`, `cold`, `unknown`.
- [ ] No method added to another subplan's interface (Rule W-3); the `checkpoint.Writer`, `store.SegmentLog`, `dag.Graph`, `negknow.Ledger` and `daemon.IdleController` surfaces are used exactly as §5 declares them.
- [ ] Rule W-2 honoured: the `checkpoint.Writer` call sites are exercised against `testdata/golden/contracts/checkpoint/` fixtures and re-run against SP-10's real implementation at the V4 verification checkpoint.
- [ ] All 173 enumerated tests exist by name and are green; all 8 benchmarks report within their budgets; all four property tests (`SoftFloorBelowHardCeiling`, `CacheFactor_MonotoneDecreasing`, `reclaimable` monotonicity, BOCD posterior normalization) plus `TestBOCD_MarshalRoundTrip_Property` pass under `rapid`.
- [ ] `state/bocd.json` and `state/scheduler.json` round-trip, and both self-heal loudly from corruption without losing store data.
- [ ] Six idle tasks registered with the stated names and priorities; each inert until an `Evaluate` has placed it in `Decision.Background`.
- [ ] The three-block `cmd/qompack/main.go` addition is the only modification outside SP-12-owned files; `internal/daemon`'s pre-existing SP-05 files are unmodified (`git diff develop..HEAD --stat internal/daemon/` lists only `scheduler_*.go`), and `internal/observer` is untouched. The tap reaches L0 through SP-05's `Options.Bind` seam precisely so this stays true.
- [ ] Commit count verified: exactly **7** (within the mandated 5–8).
- [ ] No `Co-Authored-By`, `Signed-off-by`, `Generated with` or `🤖` in any commit message, merge message, tag or PR body.
- [ ] `docs/adr/0012-scheduler-l3.md` written and covers all seven recorded decisions.
- [ ] CI green on the branch across all nine jobs; PR body carries the Phase 4 numbers.

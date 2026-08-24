# SP-12: L3 scheduler: composite trigger, p-selection with the cache term, BOCD changepoints, Young-Daly cadence, sliding-TTL idle model, idle background work, and frontier advancement

> **Recommended model: Fable 5 · xhigh effort**
>
> **The hardest plan in the set — do not economize.** Adams–MacKay run-length posterior with Normal-Inverse-Gamma conjugates and Student-t predictives (pruned, log-gamma tabulated), Young–Daly cadence, ski rental, and a cache-aware `reclaimable·r − rewrite − λ·coupling` argmax — 2,900 lines closing nine gaps, and it gates SP-15 via `PSelectionAvailable()`. Numerical-stability bugs here fail silently rather than loudly.

**Branch:** `feat/sp12-scheduler-l3` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of SP-01, SP-05, SP-06, SP-07, SP-08 already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-10 checkpointer, SP-11 rehydrator, SP-13 MCP retrieval) | **Design sections:** §5.3, §5.4, §6.6, §6.7, §7.2 L3, §8.4, §8.5 (O5 amortization), §10 Phase 4 | **Gaps closed:** G1.1, G1.2, G1.3, G1.4, G5.1, G5.2, G7.1, G7.6, G8.2

---

## Mission

This slice is layer **L3 — the scheduler**. It answers the two questions `Qompack.md` §8.4 names: **when** to compact and **where** to cut. Today Claude Code answers the first with a single token threshold (`effectiveWindow − 13_000`) and never asks the second at all. That is G1.1 (task-blind trigger), G1.3 (fixed buffer), G1.4 (blocking-limit cliff), G5.2 (the cache-cheap direction is the useless one), and G8.2 (the summarizer runs at its most degraded moment). SP-12 replaces the single constant with a composite trigger over a Bayesian changepoint posterior, a Young–Daly cadence measured at runtime, and a sliding-TTL idle model; and it replaces "no cut choice at all" with an explicit argmax over `reclaimable(p)·r − rewrite(p) − λ·segment_coupling(p)`.

The slice exists in this wave and not earlier because it needs `dag.CrossingEdges` (SP-07, wave 1) for the coupling term and the `store` tool-use index plus `SegmentLog` (SP-06, wave 1) for reclaimable-token accounting and segment lifecycle. It exists in this wave and not later because of `Qompack.md`'s closing note 3: *"Do not ship slicing or submodular selection before p-selection."* SP-15's `analyzer.NewSelector` is gated behind `scheduler.PSelectionAvailable()`, and that gate stays false until this subplan lands. SP-12 is therefore the unlock for wave 4's selection work, and the `PSelectionAvailable()` flip is a first-class deliverable, not a footnote.

**What exists in the repo when you start.** `internal/scheduler` exists as SP-01 compiling stubs: every type in 00-ARCHITECTURE §5.13 is declared, `Evaluate` returns a zero `Decision`, `NewBOCD` returns a detector whose methods return `core.ErrNotImplemented`, and `internal/scheduler/schedulertest` holds a conformance suite whose behaviour cases are `t.Skip`ped (Rule W-1). **Four symbols in that package are already real, in `internal/scheduler/formulas.go`: `YoungDaly`, `SkiRentalShouldWrite`, `PSelectionAvailable` and the package flag behind it.** SP-12 moves them into the files below and deletes `formulas.go`; re-declaring any of them is a compile error, and the file map says so row by row. The skipped conformance cases in `schedulertest/behaviour.go` are a grader SP-12 inherits rather than writes, and flipping the skip is what puts it in force — see the `schedulertest` section of the test plan for the three fixture edits that inheritance requires. `internal/config` loads and validates the full Appendix C schema plus the §11.5 `runtime` namespace, with `youngDaly.measuredDeltaSeconds` already modelled as `*float64` so JSON `null` means measure-not-zero. `internal/daemon` (SP-05) is a working resident process with `IdleController.Register` (and its normative `act.`-prefix rule), an op-routing table, the `Options.Bind(func(*Services))` late-binding seam with its nil-tolerant function set, `daemon.Options.Sched scheduler.Runtime` already declared and tolerated as `nil`, and the B-A/B-B/B-C latency budgets instrumented. Note that SP-05 calls the `Services` seams, never `Sched` — supplying the path from L0 to L3 is SP-12's job, and it is done by decorating those seams through `Bind`. `internal/store` (SP-06) serves objects, the tool-use index, file version history and the `SegmentLog` with `MarkEncoded`. `internal/dag` (SP-07) answers `CrossingEdges(pos)`, `NodesAfter(pos)` and `BackwardSlice`. `internal/observer` (SP-08) emits `observer.Signals` and opens the session's first segment. `internal/checkpoint` (SP-10) lands in this same wave and merges **after** SP-12 in the wave order, so SP-12 develops its `checkpoint.Writer.Advance` call site against the SP-01 stub and the `testdata/golden/contracts/checkpoint/` fixtures per Rule W-2.

**What exists when you finish.** `scheduler.Evaluate` is a pure, allocation-light, fully unit-testable function of `Inputs` that returns a `Decision` carrying `ShouldCompact`, ordered `Reasons`, the chosen `Candidate` `P`, its `PScore`, a complete numeric `Breakdown` map that `/qompack:status` and `eval` both read, the `TTLState`, the Young–Daly interval, the soft-floor and hard-ceiling token counts, and the O3 `Background` task list. A `Detector` implements Bayesian online changepoint detection over the four cheap features of §6.6 with pruned, bounded-length updates and a versioned, CRC-checked binary state. The droppable-block ranking (ephemeral → superseded → ordinary compactable tool result) ships as pure, foundation-only spec in `scheduler` so the replay policy can share it. `internal/daemon` gains SP-12-owned files implementing `scheduler.Runtime`: a tap that decorates SP-05's `Services` seams through `Options.Bind` so L0 events reach L3 without editing a single SP-05 or SP-08 line, feature extraction from `observer.Signals`, the `store.ToolUseRecord` adapter onto that ranking, suffix-sum `ReclaimableTokens`, candidate assembly as changepoint boundaries ∩ API-round boundaries with a cached turn→position map (and live, uncached `CrossingEdges`, which is two binary searches), session-scoped persistence to `state/bocd.json` and `state/scheduler.json`, six registered O3 idle tasks (one of them the acting `act.advance_frontier`), and O5 frontier advancement that closes segments on changepoint/todo-completion/passing-test/commit and drives `checkpoint.Writer.Advance`. `scheduler.PSelectionAvailable()` returns true once a real Runtime is constructed. `test/replay/phase4_test.go` asserts the §10 Phase 4 exit criterion directly.

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
>                       OR idle_gap > ttl_max            # cache provably cold → cut is free
>                       OR ( regime_known                # §5.4: fire BEFORE expiry — the
>                            AND idle_gap > 0.8 · ttl )  # summarization call still reads cache
>                       OR effort_changed )              # §5.4: the key changed; prefix is gone
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
> write when  E[remaining reads] > w/r   (12.5 at r=0.1, w=1.25 — the 5-minute TTL)
>                                       (20   at r=0.1, w=2.0  — the 1-hour   TTL)
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
| `internal/daemon` core (registry, ingest queue, WAL, worker pool, `IdleController` implementation, the `act.` prefix rule, `Options`/`Services`/`Handle`/`Bind`/`DeclareProducers`, spool drain, idle exit), `internal/ipc`, `internal/contract`, budgets B-A/B-B/B-C/B-D | **SP-05**. SP-12 adds new files inside `internal/daemon`, *uses* the `Bind` and `IdleController.Register` seams SP-05 shipped for exactly this purpose, and adds three blocks to `internal/cli/daemon.go`, the composition root behind the `qompack daemon` subcommand. It edits no existing SP-05 line and registers no op route. |
| Constructing and assigning `opts.Store`, `opts.Graph` and `opts.Ledger` in `internal/cli/daemon.go` (opening the store, the DAG and the negative-knowledge ledger for the resident daemon) | **SP-11** creates the block; **SP-13** extends it and owns its final shape. `daemon.NewOptions` sets only `ProjectRoot`/`Cfg`/`Log`/`Metrics`/`Clock`/`Sketches`, and `internal/cli/daemon.go` adds only `Log`/`Metrics`/`Clock`, so on the `develop` every wave-3 branch is cut from, nothing populates the three service members SP-12's runtime requires. SP-11 merges **second** in wave 3 and its `TestE2E_AdditionalContextProducerIsDeclared` needs the block at that moment, which is why creation is SP-11's and not SP-13's (`plans/V4-SP-11-rehydrator-l5.md` prerequisite 1; `plans/V4-SP-13-mcp-retrieval-layer.md` spec §11 conforms). Either way SP-12's Block 1 **reads** those fields and must not open a second store, graph or ledger of its own. See the dependency note in the `internal/cli/daemon.go` section. |
| `internal/config` schema, defaults, precedence, validation, provenance, JSON Schema, the `nomagic` pass | **SP-01** |
| Packaging, cross-platform matrix, release pipeline | **SP-17** |
| User guide, troubleshooting, config reference, UAT | **SP-18**. SP-12 writes one ADR only. |

---

## Interface contract

### Consumes (exact signatures, from 00-ARCHITECTURE §5 except where a block names another source)

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

// The two SP-05 extension seams SP-12 wires into. These signatures are transcribed from the
// SHIPPED `internal/daemon/options.go` (lines 101–131), not from 00-ARCHITECTURE: §5.4 names
// `Services` in prose ("Extension seams … so later waves wire in WITHOUT editing daemon
// internals") but declares no struct, so the file on disk is the only authority for the nine
// seam signatures — and five of them do NOT return a hookio.Output. Bind hooks run once, in
// registration order, at daemon start.
func (o *Options) Bind(fn func(*Services))
type Services struct {
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    ObserveTool    func(ctx context.Context, e hookio.Event) error                  // SP-08 — error only
    ObservePrompt  func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObserveStop    func(ctx context.Context, e hookio.Event, subagent bool) error   // SP-08 — error only
    SessionStart   func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08 + SP-11
    SessionEnd     func(ctx context.Context, e hookio.Event) error                  // SP-08 — error only
    PreCompact     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-10
    Rehydrate      func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-11
    MCPInitialized func(ctx context.Context) bool                                   // SP-13
    StatusExtra    func(ctx context.Context) (json.RawMessage, error)               // SP-14
}
// The tap decorates exactly five of these — SessionStart, ObserveTool, ObserveStop,
// ObservePrompt, SessionEnd — so three of its five wrappers (ObserveTool, ObserveStop,
// SessionEnd) have only an `error` to pass through, and two (SessionStart, ObservePrompt)
// return `(hookio.Output, error)`. Writing a wrapper against the wrong shape does not compile,
// so the tap table and `TestWrapServices_CallsInnerSeamsFirst` below are stated per seam.

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

Everything in 00-ARCHITECTURE §5.13, now real rather than `ErrNotImplemented`. **The Go identifiers below are SP-01's shipped spellings** (`internal/scheduler/types.go`), not new names: §5.13 fixes the wire *values* but not the identifiers, so SP-01's `Trigger*`/`Background*` names are the ones in force and the shipped conformance suite already references them. SP-12 declares none of these constants a second time — writing `Reason*`/`Task*` would produce eleven duplicate constants for the same eleven wire values:

```go
package scheduler

type TriggerReason string
const ( // already declared in types.go — SP-12 uses these, adds none
    TriggerSoftFloor     TriggerReason = "soft_floor"
    TriggerChangepoint   TriggerReason = "changepoint"
    TriggerYoungDaly     TriggerReason = "young_daly"
    TriggerHardCeiling   TriggerReason = "hard_ceiling"
    TriggerIdleColdCache TriggerReason = "idle_cold_cache"
    // ADDED by SP-12. This is a new value of a §5.13 type SP-01 owns, so it is additive under
    // §5's latitude but the enumeration in 00-ARCHITECTURE §5.13 moves with it — that doc edit
    // is part of this branch, not a follow-up. It does not trip the alias gate in the Done
    // checklist, which greps for `Reason*`/`Task*` identifiers; this one keeps the `Trigger`
    // prefix the eleven shipped values use.
    TriggerCacheExpiring TriggerReason = "cache_expiring"
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

    // ── ADDITIVE, SP-12, cache-regime correction (see cacheregime.go). Same rationale:
    // `internal/scheduler` owns `Inputs` and nothing outside SP-12 constructs one.
    Regime             CacheRegime    // the (TTL, price) pair this session is actually billed at.
                                      // Zero value ⇒ Evaluate resolves the unknown-regime rung.
    LastRequestStartTS core.UnixMilli // start of the most recent API REQUEST, never the end of its
                                      // response. The TTL clock runs from the request start and
                                      // generation time counts against it, so anchoring on Stop
                                      // over-reports warmth by the whole generation. 0 ⇒ fall back
                                      // to LastAPICallTS, i.e. exactly today's behaviour.
    EffortChanged      bool           // the turn's effort level differs from the previous turn's.
                                      // Effort is part of the cache key, so this is an INSTANT
                                      // full invalidation that no wall-clock gap can reveal.
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
const ( // already declared in types.go — SP-12 uses these, adds none
    BackgroundAdvanceFrontier BackgroundTask = "advance_frontier"
    BackgroundGC              BackgroundTask = "gc"
    BackgroundPrecomputeSlice BackgroundTask = "precompute_slice"
    BackgroundRefreshDelta    BackgroundTask = "refresh_delta"
    BackgroundRebuildBloom    BackgroundTask = "rebuild_bloom"
    BackgroundCompactDAG      BackgroundTask = "compact_dag"
)

func Evaluate(in Inputs) Decision           // shipped as a zero-Decision stub in evaluate.go
func PSelectionAvailable() bool             // ALREADY SHIPS in formulas.go
func EnablePSelection()   // SP-12 additive: called by the Runtime constructor
func DisablePSelection()  // SP-12 additive: called on Runtime shutdown and by tests
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64   // ALREADY SHIPS in formulas.go
func SkiRentalShouldWrite(expectedReads, r, w float64) bool // ALREADY SHIPS in formulas.go

// Four of the symbols above are NOT new: SP-01 shipped `YoungDaly`, `SkiRentalShouldWrite`,
// `PSelectionAvailable` and the package-level `pSelectionAvailable` flag in
// `internal/scheduler/formulas.go`, and `Evaluate`/`NewBOCD` as stubs in `evaluate.go` and
// `detector.go`. SP-12 therefore MOVES them rather than declaring them a second time — see the
// file map's `formulas.go`, `evaluate.go` and `detector.go` rows, which are deletions and
// modifications, not creations. Two symbols here are genuinely new: `EnablePSelection` and
// `DisablePSelection`.

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
| `internal/scheduler/types.go` | modify | add the two additive `Inputs` fields (`LastCompactionTS`, `CouplingLambda`) — the **only** genuinely new declarations in this file. No constant is added: all five `TriggerReason`, four `TTLState`, three `Urgency` and six `BackgroundTask` constants already ship. Also correct three godocs (see below) |
| `internal/scheduler/thresholds.go` | create | `HostAutoCompactBuffer`, `SoftFloor`, `HardCeiling` |
| `internal/scheduler/cacheregime.go` | create | `CacheRegime`, `ResolveCacheRegime`, `UnknownRegime` — which TTL and which `w` this session is actually billed at |
| `internal/scheduler/ttl.go` | create | `ClassifyTTL`, `CacheFactor` — the E1 sliding-TTL idle model, keyed off the regime's two bounds |
| `internal/scheduler/formulas.go` | **delete** | SP-01 shipped `YoungDaly`, `SkiRentalShouldWrite`, `PSelectionAvailable` and `var pSelectionAvailable bool` here. Its four symbols are redistributed to `youngdaly.go`, `skirental.go` and `gate.go` below; the file is removed in the same commit so package `scheduler` never holds two declarations of any of them |
| `internal/scheduler/formulas_test.go` | **delete** | its four cases (`TestYoungDaly_Formula`, `TestSkiRental_ComputedNotLiteral`, `TestSkiRental_ThresholdTracksConfig`, `TestPSelectionAvailable_DefaultsFalse`) move — under those exact names — into `youngdaly_test.go`, `skirental_test.go` and `gate_test.go`, absorbing the new cases rather than being duplicated beside them |
| `internal/scheduler/youngdaly.go` | create | takes over `YoungDaly` from `formulas.go` (adding the NaN/Inf guard — a declared behaviour change), plus `mtbfSeconds` and δ precedence resolution |
| `internal/scheduler/skirental.go` | create | takes over `SkiRentalShouldWrite` from `formulas.go`, adding the `w <= 0` guard — a declared behaviour change |
| `internal/scheduler/dropclass.go` | create | `DropClass`, `DropClassOf`, `EvictionRank` — the §2.2 tool table as pure spec |
| `internal/scheduler/detector.go` | modify | SP-01's `NewBOCD` + `stubDetector` live here. Delete `stubDetector` and repoint `NewBOCD` at `bocd.go`'s real constructor; the `Detector` interface godoc stays. Declaring `NewBOCD` again in `bocd.go` would not compile |
| `internal/scheduler/bocd.go` | create | the BOCD `Detector` implementation: Normal-Inverse-Gamma model, pruned update, binary state. It declares the `bocd` type and its methods only — `NewBOCD` keeps its shipped home in `detector.go` |
| `internal/scheduler/pselect.go` | create | candidate filtering, `score(p)`, argmax with TTL-aware tie-break |
| `internal/scheduler/evaluate.go` | modify | SP-01's zero-`Decision` stub body is replaced in place (the file already exists): the composite trigger, `Breakdown` assembly, `Background` planning |
| `internal/scheduler/gate.go` | create | takes over `PSelectionAvailable` and the package flag from `formulas.go`, retyped `bool` → `atomic.Bool` (a declared behaviour change), and adds `EnablePSelection` / `DisablePSelection` |
| `internal/scheduler/*_test.go` | create | unit + property + benchmark tests (see Test plan) |
| `internal/scheduler/schedulertest/suite.go` | modify | flip SP-01's `t.Skip`s; add behaviour cases |
| `internal/scheduler/schedulertest/behaviour.go` | modify | two fixture edits the shipped grader's own note (behaviour.go:24–26) authorises: give `baseInputs()` a placeable candidate and a `LastCompactionTS`, and make `assumedMTBFSeconds` subtract `HostAutoCompactBuffer`. Exact edits in the `schedulertest` section of the test plan; the truth table itself is not touched |
| `internal/daemon/scheduler_droppable.go` | create | `ClassifyDrop` adapter, suffix-sum `reclaimableIndex` |
| `internal/daemon/scheduler_features.go` | create | `FeatureHistory`, `FeaturesFrom` |
| `internal/daemon/scheduler_candidates.go` | create | candidate assembly, cached turn→`Pos` map, cap-at-32 (`CrossingEdges` is called live — see below) |
| `internal/daemon/scheduler_tap.go` | create | `WrapServicesForScheduler` — the `Options.Bind` decoration that feeds L0 events to L3 |
| `internal/daemon/scheduler_runtime.go` | create | `scheduler.Runtime` implementation, δ EWMA, burn rate, `SchedulerSnapshot` |
| `internal/daemon/scheduler_state.go` | create | `state/bocd.json` + `state/scheduler.json` codecs |
| `internal/daemon/scheduler_frontier.go` | create | O5: segment close/roll, `checkpoint.Writer.Advance`, residual accounting |
| `internal/daemon/scheduler_idle.go` | create | O3: `RegisterSchedulerIdleWork`, the six tasks |
| `internal/daemon/scheduler_*_test.go` | create | unit + property + benchmark tests |
| `internal/cli/daemon.go` | modify | three added blocks in `runDaemon`, the composition root that builds `daemon.Options` and calls `daemon.New`: construct, `Bind`, register. `cmd/qompack/main.go` is 30 lines of `cli.Dispatch` and has no `daemon` subcommand, no `opts` and no `daemon.Options` — it is not touched |
| `testdata/golden/scheduler/decision-{warm,expiring,cold}.json` | create | frozen `Decision` goldens (`Breakdown` key set + ordering) |
| `testdata/bench-baseline.txt` | modify | append the eight SP-12 benchmark baselines — one per row of the benchmark table in the test plan; a benchmark with no baseline line is a benchmark `benchstat` cannot judge |
| `test/e2e/scheduler_idle_test.go` | create | `TestDaemonIdleRunsSchedulerWork` against a real daemon |
| `test/replay/l3policy/policy.go` | create | the `eval.Policy` named `qompack-l3` |
| `test/replay/l3policy/policy_test.go` | create | policy unit tests (determinism, keep-set shape) |
| `test/replay/phase4_test.go` | create | the Phase 4 exit-criterion harness |
| `docs/adr/0012-scheduler-l3.md` | create | ADR: the eleven decisions listed in commit 7 |

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

**Three godoc corrections in `internal/scheduler/types.go`, landing with this file.** SP-01's godoc describes a hard ceiling that omits the host buffer, and a Young–Daly clause keyed on the wrong clock. Both are prose-only edits — no identifier, type or value changes — and both are mandatory, because a reader implementing against them would reproduce exactly the arithmetic the shipped conformance grader gets wrong:

1. `Decision.HardCeilingTokens`: "EffectiveWindow minus Cfg.HardCeilingMargin, in tokens" → **"EffectiveWindow minus HostAutoCompactBuffer (13 000, Qompack.md §2.5) minus Cfg.HardCeilingMargin, in tokens"**.
2. `TriggerHardCeiling`: "true when ContextTokens has reached EffectiveWindow minus Cfg.HardCeilingMargin" → the same three-term expression. `config.Validate`'s own doc for `hardCeilingMargin` ("token headroom below Claude Code's own auto-compact threshold", `internal/config/config.go:74`) already agrees with §8.4 and with the arithmetic below; only `types.go` disagreed.
3. `TriggerYoungDaly`: "the elapsed time since the last significant cache write" → **"the elapsed time since the last compaction (`Inputs.LastCompactionTS`)"**. `LastCacheWriteTS` never enters the decision (see `ttl.go` below), and keying the cadence clause on it would restart the interval on every prompt-cache write.

**Worked example (asserted by test).** `EffectiveWindow(200_000, 32_000) = 200_000 − min(32_000, 20_000) = 180_000` — §2.5's own worked example, reproduced exactly. `SoftFloor = 0.55 × 180_000 = 99_000`. `HardCeiling = 180_000 − 13_000 − 20_000 = 147_000`. The host's own threshold is `167_000`, so the plugin has 20 000 tokens — comfortably one large turn — to checkpoint first. This is the arithmetic that closes G1.2, G1.3 and G1.4 as far as a plugin can.

**Where the window numbers come from — decided, not left open.** There is no host API for the model's context window in wave 3, and a scheduler that cannot resolve one is inert. The Runtime resolves `contextWindow` and `maxOutputTokens` in this order, then writes which rung fired into `Breakdown["window_source"]` (`3` = env autocompact, `2` = explicit override, `1` = host default) **after `Evaluate` returns** — `Evaluate` stays pure and sees only the resulting number — so `/qompack:status` never presents a guess as a measurement:

1. `CLAUDE_CODE_AUTO_COMPACT_WINDOW` — the documented host env var of §2.5, clamped to its documented range `[100_000, 1_000_000]`. Read once per `SessionStart` through `config.Env.Getenv`, never `os.Getenv` directly, so tests can inject it.

**Four further variables change the window Claude Code is working to, and §2.5 v1.3 names all four.**
A ladder that reads only `CLAUDE_CODE_AUTO_COMPACT_WINDOW` sizes `soft_floor` and `hard_ceiling`
against a window nobody is using — silently, because every threshold still computes and every cut is
still legal. All four are read through `config.Env.Getenv` at `SessionStart`, beside rung 1, and
each writes its own `Breakdown` key so `/qompack:status` can show which one bound:

| Variable | Effect on the ladder | `Breakdown` |
|---|---|---|
| `DISABLE_COMPACT` | **There is no host trigger to stay ahead of.** `hard_ceiling`'s entire justification — "one turn's worth of headroom below Claude Code's threshold" — is void, and `Evaluate` must stop promising headroom it no longer controls: `Urgency` is capped at `UrgencyAdvisory`, the `hard_ceiling` clause cannot raise it to `UrgencyNow`, and every trigger becomes a recommendation. It does **not** disable Qompack: L4 checkpointing and L5 rehydration are more valuable here, not less, because nothing else is bounding the window | `host_compaction_disabled = 1` |
| `CLAUDE_CODE_MAX_CONTEXT_TOKENS` | Declares the window Claude Code assumes for a gateway or unrecognized model ID. Outranks the `HostDefault*` rung and is clamped to the same `[100_000, 1_000_000]` range; it does **not** outrank an explicit `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, which sets the compaction point rather than the window | `window_source = 2.5` |
| `CLAUDE_CODE_DISABLE_1M_CONTEXT` | A natively-1M model compacts at the 200 000 boundary instead. Clamps the resolved window to 200 000 **after** every other rung, because it is a ceiling on the host's behaviour rather than a source for the number | `window_clamped_200k = 1` |
| `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT` | The host compacts only after the API rejects the conversation, so the resolved window is a guess with no enforcement behind it. Treated like `DISABLE_COMPACT` for urgency — advisory only — while the window itself still resolves normally | `host_enforcement_off = 1` |

None of these is an Appendix C key and none is a new one: they are documented host variables, read
the same way §2.5's original is. `TestResolveWindow_HostVariables` drives each in isolation and the
`DISABLE_COMPACT` + `hard_ceiling` interaction explicitly, because "the plugin acts first by design"
(§12) is a claim about a host trigger that, under that variable, does not exist.
2. `QOMPACK_CONTEXT_WINDOW` / `QOMPACK_MAX_OUTPUT_TOKENS` — explicit overrides for CI, replay and the bench harness. These are environment variables, not config keys: this slice adds no Appendix C key and therefore cannot break the `docs` gate.
3. `HostDefaultContextWindow` / `HostDefaultMaxOutput` (200 000 / 32 000), which reproduce §2.5's worked example.

`EffectiveWindow` is then applied to whichever pair won. Rung 1 supplies a window directly, so `maxOutputTokens` for that rung is `0` and the env value *is* the effective window.

**Failure modes.** `effectiveWindow <= 0` (caller did not supply it) ⇒ both return 0 and `Evaluate` short-circuits to a no-op `Decision` with `Breakdown["error_no_window"] = 1`. `SoftFloorPct` outside `(0,1)` never reaches here: `config.Validate` already replaced it with the default and logged Loud (00-ARCHITECTURE §11.3).

---

### `internal/scheduler/cacheregime.go` — which cache the session is actually running on

**Why this file exists.** `Qompack.md` §5.1 states the multipliers and then says, in bold, *"verify
against current pricing before tuning, since the ratio drives several thresholds below."* That
verification was done on 2026-08-23 against the two primary sources, and it found the ratio intact
but the **TTL and the write multiplier wrong for the deployment Qompack actually ships into**. Both
are quoted verbatim below, because every number in this file is downstream of them.

From the Claude API reference (`platform.claude.com/docs/en/build-with-claude/prompt-caching`):

> "Cache read tokens are 0.1 times the base input tokens price"
> "5-minute cache write tokens are 1.25 times the base input tokens price"
> "**1-hour cache write tokens are 2 times the base input tokens price**"

From the Claude Code reference (`code.claude.com/docs/en/prompt-caching`, *Cache lifetime*):

> "**On a Claude subscription, Claude Code requests the one-hour TTL automatically**, so the cache
> survives breaks of up to an hour."
> "On an API key, Amazon Bedrock, Google Cloud's Agent Platform, Microsoft Foundry, or Claude
> Platform on AWS, you pay the per-token rates, so the TTL stays at the cheaper five minutes by
> default. To opt into the one-hour TTL, set `ENABLE_PROMPT_CACHING_1H=1`."
> "Set `FORCE_PROMPT_CACHING_5M=1` to force the five-minute TTL regardless of authentication."

So `r = 0.1` is confirmed and unchanged. `w` is **not a scalar**: it is `1.25` at the five-minute
TTL and `2.0` at the one-hour TTL. And Appendix C's `ttlSeconds: 300` is correct for API-key auth
and **wrong by a factor of 12** for a Claude subscription, which is the majority Claude Code
deployment and the one this plugin is written for.

**What that costs, computed from this subplan's own fixture.** `baseInputs()` (see the
`evaluate_test.go` fixture table) sets `ContextTokens: 120_000` with candidates at `Pos` 40 000 /
80 000 / 118 000. `TestEvaluate_ArgmaxDeepestWhenCold` drives it with `LastAPICallTS = Now−400_000`
— a 400-second gap — and `ttlSeconds = 300`, which classifies `TTLCold`, sets `CacheFactor = 0`,
zeroes `rewrite` for every candidate, and makes `chooseP` take the deep-cut branch: `P.Pos ==
40_000`, `Breakdown["rewrite"] == 0`.

On a subscription the true TTL is 3600 s, so a 400-second gap is **warm**, not cold. The same
fixture's warm scores are `−97 084` at `Pos 40_000` and `−1 916.8` at `Pos 118_000`: the warm
objective prefers the shallow cut by roughly fifty to one, and the rewrite the cold branch treated
as free actually costs `w · (120 000 − 40 000) = 2.0 × 80 000 = 160 000` write-units against
`2.0 × 2 000 = 4 000` for the shallow cut. **A forty-fold error on precisely the decision §5.2
calls "the governing quantity".** It is also silent: nothing fails, the cut is legal, and the bill
arrives later.

**The resolution, and why it is a runtime ladder rather than a config edit.** Appendix C lives in
`Qompack.md`, which is read-only, and `TestDefaults_MatchesAppendixCVerbatim` deep-equals against
it, so `scheduler.cache.ttlSeconds` and `writeMultiplier` cannot change value or shape. They do not
need to. §11.5's `runtime` namespace is the sanctioned additive extension — *"No key here may
change the meaning or default of any Appendix C key"* — and this file resolves the regime the same
way `EffectiveWindow` already resolves the context window: a documented rung ladder read through
`config.Env.Getenv`, never `os.Getenv`, with the winning rung written into `Breakdown` so
`/qompack:status` never presents an inference as a measurement.

```go
package scheduler

// CacheRegime is the (TTL, price) pair the session is actually running under. Appendix C's
// scheduler.cache keys supply the FLOOR; this struct is what the scheduler reasons with.
//
// TTLMin and TTLMax are separate on purpose, and they are equal only when the regime is KNOWN.
// The asymmetry is the whole point — see ClassifyTTL.
type CacheRegime struct {
    TTLMinSeconds   int     // shortest TTL the session could be running under
    TTLMaxSeconds   int     // longest  TTL the session could be running under
    ReadMultiplier  float64 // r
    WriteMultiplier float64 // w — 1.25 at the 5-minute TTL, 2.0 at the 1-hour TTL
    Disabled        bool    // prompt caching turned off entirely for this model
    Source          string  // which rung fired; goes straight into Breakdown and /qompack:status
}

// ResolveCacheRegime walks the documented ladder. Highest rung wins.
//
//  4. FORCE_PROMPT_CACHING_5M=1        → KNOWN 5-minute  (300, 300, r, 1.25)   source "force_5m"
//  3. DISABLE_PROMPT_CACHING[_MODEL]=1 → KNOWN no cache  (0, 0, 1.0, 1.0)      source "disabled"
//  2. ENABLE_PROMPT_CACHING_1H=1       → KNOWN 1-hour    (3600, 3600, r, 2.0)  source "enable_1h"
//  1. nothing set                      → UNKNOWN         (cfgTTL, 3600, r, 2.0) source "unknown"
//
// Rung 4 outranks rung 2 because the Claude Code reference says FORCE_PROMPT_CACHING_5M applies
// "regardless of authentication" and names overriding an ENABLE_PROMPT_CACHING_1H in managed
// settings as its purpose. Rung 3 outranks rung 2 because a disabled cache is not a short cache.
//
// Rung 1 is the case that matters, because it is the default on every machine that has not been
// deliberately configured, and Qompack CANNOT tell subscription auth from API-key auth: no hook
// input carries it and there is no environment variable for it. So rung 1 does not guess. It
// reports a RANGE — the Appendix C floor for the lower bound, the one-hour ceiling for the upper —
// and takes the conservative multiplier of the two, which is the larger w. Charging the scheduler
// the higher write price when the regime is unknown biases it toward shallower cuts, and a
// shallower cut than optimal costs reclaim; a deeper cut than optimal costs money.
func ResolveCacheRegime(getenv func(string) string, cfg config.SchedulerCfg, model string) CacheRegime
```

**The asymmetric classification, and the claim it finally makes true.** The shipped `ClassifyTTL`
doc comment says `gap >= ttl` means *"cache provably cold → cut is free"*. Under rung 1 that word
is not earned: a 400-second gap proves nothing when the TTL might be 3600. The fix is to key the
two thresholds off the two different bounds:

```
gap <  0.5 · TTLMin   → TTLWarm       the prefix is certainly still readable
0.5·TTLMin ≤ gap < TTLMax → TTLExpiring   it MIGHT be dead; confidence decays
gap ≥ TTLMax          → TTLCold       it is dead under every regime in the range
```

When the regime is known the two bounds coincide and this is exactly the shipped behaviour, so
`TestClassifyTTL_*` keeps its `ttl = 300` assertions unchanged by passing a known 5-minute regime.
When the regime is unknown, `TTLCold` now requires a full hour of silence — and when it does fire,
the "provably cold" in the comment is a fact rather than a hope. `CacheFactor` ramps across the
widened expiring band exactly as before; only the endpoints move.

`Disabled` short-circuits everything: with no cache there is no read discount and no write premium,
so `r = w = 1`, `CacheFactor` is always 1, every token of tail costs exactly one token to resend,
and the bimodal deep-cut branch is turned off because there is no cold state to exploit.
`Breakdown["cache_disabled"] = 1` says so out loud, because a scheduler silently optimizing a cache
that does not exist is the worst of the failure modes here.

**Subagents are a different regime and must not inherit the main conversation's.** The Claude Code
reference is explicit: *"Subagents use the five-minute TTL even on a subscription, since the
automatic one-hour TTL applies to the main conversation."* Any regime resolved for a session whose
`hookio.Event` carries `agent_id` is therefore pinned to `(300, 300, r, 1.25)`, source
`"subagent_5m"`, whatever the ladder says. This costs nothing to implement and prevents the
scheduler from believing a subagent's prefix survives an hour of idle when it dies in five minutes.

---

### The two clock corrections

**(a) The TTL clock starts at the request, and generation time counts against it.** The API
reference states it without qualification:

> "The lifetime is measured from the **start of the request** that writes or reads the cache entry,
> not from the end of its response. Time spent generating a response counts against the lifetime:
> if a response takes 4 minutes to stream, a follow-up request that reuses the same cached prefix
> must start within about 1 minute of that response completing."

`NotifyActivity(ts)` currently sets `lastAPICallTS` from whichever hook fired last, and the last
hook of a turn is `Stop`, which fires **after** generation. So the gap the scheduler measures is
short by the whole generation time — on a long agentic turn, minutes. Under a five-minute TTL that
is the difference between "five minutes of headroom" and "one", and it makes the scheduler classify
a genuinely cold prefix as warm.

The fix is free, because the observer already sees the earlier events: anchor on the **start of the
most recent request**, which is the last `PostToolUse` of the turn (the tool result is what the next
request carries) or `UserPromptSubmit` when the turn used no tools — never `Stop`. `Inputs` gains
`LastRequestStartTS`; `ClassifyTTL` reads it and falls back to `LastAPICallTS` when it is zero, so a
session that predates the field still classifies exactly as it does today. The estimate is
one-sided by construction: `LastRequestStartTS ≤ true request start ≤ Stop`, so the new anchor can
only make the measured gap **larger**, never smaller, and the classifier can only become more
conservative about warmth. That one-sidedness is the property `TestTTLAnchorIsNeverLaterThanStop`
asserts, and it is why the change cannot introduce a regression in the direction that costs money.

**(b) A change of effort level empties the cache instantly, and wall-clock cannot see it.** The
Claude Code reference lists effort alongside model as part of the cache key:

> "The cache is keyed by **effort level** as well as model, so switching with `/effort` means the
> next request reads the entire conversation history with no cache hits."

This one is observable, and cheaply. Hook inputs carry `effort.level` on `PreToolUse`,
`PostToolUse`, `Stop` and `SubagentStop` — three of which SP-08 already registers — and the same
value is exported as `$CLAUDE_EFFORT`. So the observer records the effort string on every event it
already handles, and `Inputs` gains `EffortChanged bool`, set when the current turn's effort differs
from the previous turn's. When it is true the classifier returns `TTLCold` **regardless of gap**,
because the prefix is not expiring, it is gone.

This is worth more than it looks. §5.4's bimodality says the two good moments to cut are "as late as
possible" and "when the cache is cold and rebuilding is free". An effort switch manufactures the
second one instantly, at a moment the scheduler currently reads as maximally warm, and it is the
only such moment Qompack can detect. `Breakdown["cold_reason"]` distinguishes `"idle"` from
`"effort_change"` so the two are never conflated in a report.

**What Qompack cannot see, recorded so nobody looks for it.** A mid-session **model** switch and a
**fast-mode** toggle both invalidate the cache identically, and neither is observable: hook inputs
carry `model` only on `SessionStart`, and the reference notes it is not guaranteed present even
there; `fast_mode` appears in the status-line payload and in no hook input. There is no environment
variable for either. These go in the §12 "cannot do" inventory (SP-18) rather than being
approximated, because an approximation here would classify a warm prefix as cold and take the
expensive branch — the exact failure this section exists to remove.

---

### `internal/scheduler/ttl.go` — the E1 sliding-TTL idle model

The correction in `Qompack.md` §8.4 is precise: track **time since last API call**, never time since last cache write. `Inputs` carries both; this file never reads `LastCacheWriteTS`, which is retained for observability and for SP-16's ski-rental work and is written into `Breakdown` but never into the decision.

Two refinements arrive with the cache-regime work above and neither weakens that correction. The anchor is `LastRequestStartTS` when it is set, falling back to `LastAPICallTS` when it is zero — both are API-call clocks, and the first is simply the tighter of the two, because the API measures the TTL from the request's start while `LastAPICallTS` is written by whichever hook fired last. And the thresholds key off `CacheRegime` rather than a bare `ttlSeconds`, so a session that cannot identify its regime says so instead of asserting a 300-second cliff it cannot justify.

```go
package scheduler

import "github.com/qompack/qompack/internal/core"

// ttlExpiringFraction is the fraction of the TTL after which the prefix is treated as
// "expiring" and the marginal value of the warm cache begins to decay linearly to zero
// (Qompack.md §8.4: "idle long enough that expiry is imminent … the same preference applies").
const ttlExpiringFraction = 0.5

// ClassifyTTL returns the cache state and the observed idle gap in seconds.
//
// anchorTS is the start of the most recent API REQUEST, not the end of its response — see the
// clock corrections in cacheregime.go. Callers pass Inputs.LastRequestStartTS and fall back to
// LastAPICallTS only when it is zero.
//
// The two thresholds key off the two DIFFERENT bounds of the regime, and that asymmetry is what
// makes "provably cold" true rather than hopeful:
//   anchorTS == 0 or regime invalid            → TTLUnknown, gap 0
//   effortChanged                              → TTLCold at any gap (the prefix is gone, not aging)
//   gap <  0.5·TTLMin                          → TTLWarm     (readable under every regime)
//   0.5·TTLMin <= gap < TTLMax                 → TTLExpiring (readable under SOME regime)
//   gap >= TTLMax                              → TTLCold     (dead under every regime)
// When the regime is known, TTLMin == TTLMax and this reduces exactly to the shipped behaviour.
func ClassifyTTL(now, anchorTS core.UnixMilli, reg CacheRegime, effortChanged bool) (TTLState, float64) {
    if anchorTS <= 0 || reg.TTLMaxSeconds <= 0 {
        return TTLUnknown, 0
    }
    gap := float64(now-anchorTS) / 1000.0
    if gap < 0 {
        gap = 0
    }
    if effortChanged {
        return TTLCold, gap
    }
    switch {
    case gap >= float64(reg.TTLMaxSeconds):
        return TTLCold, gap
    case gap >= ttlExpiringFraction*float64(reg.TTLMinSeconds):
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

**Worked example (asserted by test), known 5-minute regime.** `TTLMin = TTLMax = 300` — the regime a `FORCE_PROMPT_CACHING_5M=1` session resolves to, and the one Appendix C's floor describes. gap 10 s ⇒ warm, factor 1.0. gap 150 s ⇒ expiring, factor `(300−150)/150 = 1.0`. gap 225 s ⇒ expiring, factor `(300−225)/150 = 0.5`. gap 300 s ⇒ cold, factor 0.0. gap 4 h ⇒ cold, factor 0.0. The ramp is continuous at both endpoints. **These are the shipped numbers and they do not move**: a known regime collapses `TTLMin` and `TTLMax` onto one value and the classifier is bit-identical to the version this replaces.

**Worked example, unknown regime — the case that changes.** `TTLMin = 300`, `TTLMax = 3600`. gap 10 s ⇒ warm. gap 150 s ⇒ expiring (the *lower* bound governs the warm→expiring edge, so Qompack stops trusting warmth exactly when it used to). gap 400 s ⇒ **expiring, not cold** — the correction, and the one that keeps `TestEvaluate_ArgmaxDeepestWhenCold`'s 40× mis-cut from happening on a subscription. gap 3600 s ⇒ cold, and now provably so. The expiring ramp is stretched across `[150, 3600]`, which is deliberate: an unknown regime should express its uncertainty as a long, slow decay in the value of the warm prefix rather than as a cliff at a number it cannot justify.

---

---

### The compaction event has its own price, and the trigger set currently ignores it

**The fact.** Compaction is not free bookkeeping that happens between requests. It **is** a
request, and the Claude Code reference prices it explicitly (`code.claude.com/docs/en/prompt-caching`,
*Compacting the conversation*):

> "To produce the summary, Claude Code sends a separate request with the same system prompt, tools,
> and history as your conversation, plus a summarization instruction appended as a final user
> message. **While the cache is warm, that request reads your prefix from the cache**, so a
> mid-session `/compact` costs a fraction of what the context size suggests and spends most of its
> time generating the summary."
>
> "**After a break longer than the cache lifetime, there is no cache left to read, so the
> summarization request reprocesses the full history as uncached input.** This is why `/compact`
> costs the most when you resume an old session."

That request shares the conversation's prefix and appends to it, so it hits the cache when the cache
is alive. Its input cost is therefore `r · n` warm and `1.0 · n` cold — a difference of
`(1 − r) · n`, which at `r = 0.1` is **`0.9 · n`**.

**Where §8.4's objective is silent.** `score(p) = reclaimable(p)·r − rewrite(p) − distortion(p)`
prices the *cut*. It has no term for the *event*. That omission is harmless for the argmax — the
event cost is identical for every candidate `p`, so it cannot reorder them — but it is not harmless
for the fire decision, which is where `TTLCold` enters as a standalone trigger:

```go
idleColdCache := ttl == TTLCold
fired := aboveSoftFloor && (atChangepoint || youngDalyElapsed || aboveHardCeiling || idleColdCache)
```

**What follows, stated carefully, because the obvious reading is wrong.** It is tempting to conclude
that firing on a cold cache is a mistake. It is not. Once the prefix is dead the comparison is
`compact now` at `1.0·n + w·s` against `keep working` at `w·n` plus a permanently larger steady
state, and with `s ≪ n` and `w > 1` the first is the cheaper of the two. The cold trigger is sound
and stays exactly as it is.

The defect is one step earlier. `TTLExpiring` is used **only** as a ramp on `CacheFactor` and never
as a trigger, so no idle-driven compaction can fire until the prefix is already dead. The scheduler
therefore pays the `0.9·n` cold-summarization premium on **every** idle-driven compaction it will
ever recommend, and it does so by construction rather than by bad luck.

Firing one band earlier removes that premium outright:

| Fired at | Summarization input | Warm prefix forfeited | Total (n = 150 000, r = 0.1) |
|---|---|---|---|
| `TTLExpiring` (prefix alive, nearly dead) | `r·n` = 15 000 | almost none — it was about to expire | **15 000** |
| `TTLCold` (prefix dead) | `1.0·n` = 150 000 | none — already gone | **150 000** |

`0.9 · n` = **135 000 base-input-token-equivalents saved per idle-driven compaction** at a 150 000-token
context, and the saving scales linearly with `n`, which is to say it is largest exactly when
compaction matters most. The forfeited-discount column is what makes the expiring band the right
place rather than merely an earlier one: §5.1 prices a premature cut at `(1 − r)·(n − p)` in
discounted reads you no longer get to use, and a prefix at `0.9 · TTL` of idle has almost no
remaining reads to lose. Cutting there gives up nearly nothing and buys the cheap summarization.
This is `Qompack.md` §5.4's own instruction — *"Compaction should be scheduled against cache state,
not only against token count"* — applied to the half of cache state the shipped trigger set skipped.

**The change.** One new trigger, additive; nothing is removed or reordered.

```go
// TriggerCacheExpiring fires while the prefix is STILL READABLE but close enough to expiry that
// its remaining discounted reads are worth less than the (1−r)·n premium a cold summarization
// pays. Gated on the regime being KNOWN: under rung 1 the expiring band spans 150 s to 3600 s and
// firing across all of it would compact sessions that are merely between turns.
cacheExpiring := ttl == TTLExpiring &&
    reg.TTLMinSeconds == reg.TTLMaxSeconds &&
    gap >= cfg.Cache.ExpiringTriggerFraction*float64(reg.TTLMaxSeconds)

fired := aboveSoftFloor &&
    (atChangepoint || youngDalyElapsed || aboveHardCeiling || idleColdCache || cacheExpiring)
```

`runtime.scheduler.cache.expiringTriggerFraction` (default `0.8`) is the new key, and it is a
`runtime` key precisely because §11.5 forbids it from touching an Appendix C default — it adds a
trigger, it does not retune `ttlSeconds`, `readMultiplier` or `writeMultiplier`. `0.8` is not tuned
against a corpus and this document does not claim it is: it is the point at which the remaining
warm window is one fifth of the TTL, chosen so that the trigger cannot fire during ordinary
between-turn pauses, and SP-16's Phase-7 work owns measuring it. Until then `Breakdown["fired_at_ttl_fraction"]`
records the gap fraction at every firing so the corpus needed to tune it accumulates from real runs.

**Two guards this must not lose.** The trigger is gated on `aboveSoftFloor` like every other, so it
cannot compact a small context merely because the cache is aging. And it is gated on a **known**
regime: firing on an unknown-regime expiring band would mean compacting on the strength of a
threshold derived from a TTL the scheduler admits it cannot identify, which is the failure mode this
whole section is removing rather than one to reintroduce one paragraph later.

---

### `internal/scheduler/youngdaly.go`

**This file takes `YoungDaly` over from `internal/scheduler/formulas.go`, which is deleted in the same commit.** Re-declaring it beside the shipped copy is a compile error, and leaving the shipped copy in place would leave `mtbfSeconds` and `resolveDelta` orphaned in a second file. One declared behaviour change comes with the move: the shipped body guards only `deltaSeconds <= 0 || mtbfSeconds <= 0`, so `YoungDaly(math.NaN(), 5)` returns `NaN` today. The body below adds the NaN/Inf guard, because a `NaN` interval silently disables the cadence clause (`interval > 0` is false for `NaN`) instead of reporting the disabled state — `TestYoungDaly_NonPositive` pins `(NaN,5)` and `(Inf,5)` to `0`. The shipped `TestYoungDaly_Formula` moves into `youngdaly_test.go` and keeps its `√(2·30·600)` assertion alongside the new `δ=20, M=1800` anchor.

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

**This file takes `SkiRentalShouldWrite` over from `internal/scheduler/formulas.go`, which is deleted in the same commit.** One declared behaviour change comes with the move: the shipped body guards only `r <= 0`, so at `w = 0` it reports `expectedReads > 0`, i.e. "always write" for a free cache write that cannot be free. The body below also refuses on `w <= 0`, and `TestSkiRentalShouldWrite` pins `r=0` and `w=0` to `false`. The shipped `TestSkiRental_ComputedNotLiteral` and `TestSkiRental_ThresholdTracksConfig` move into `skirental_test.go` unchanged — neither asserts the `w = 0` case, so nothing regresses.

```go
package scheduler

// SkiRentalShouldWrite is Qompack.md §5.6 / Appendix A:
//   write when E[remaining reads] > w/r
// The threshold is COMPUTED from config, never written as 12.5 (00-ARCHITECTURE §11.6).
// Nothing in SP-12 calls this; SP-16 applies the policy. It lives here because
// 00-ARCHITECTURE §5.13 places the signature in this package.
//
// The “≈12.5” in §5.6 and in §5.13's signature comment is the FIVE-MINUTE figure and is not the
// only one. Cache writes are 1.25× base input at the 5-minute TTL and 2× at the 1-hour TTL, so
// w/r is 12.5 under one regime and **20** under the other — a caller that hardcodes either is
// wrong 50% of the time. Pass w from CacheRegime.WriteMultiplier (see cacheregime.go), never
// from cfg.Cache.WriteMultiplier directly: the config key is Appendix C's 5-minute floor, and
// the regime is what the session is actually billed at.
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
    if r <= 0 || w <= 0 {
        return false
    }
    return expectedReads > w/r
}
```

---

### `internal/scheduler/gate.go`

**This file takes `PSelectionAvailable` and the package flag over from `internal/scheduler/formulas.go`, which is deleted in the same commit, and retypes the flag.** SP-01 shipped `var pSelectionAvailable = false` — a plain `bool` that nothing ever wrote, which was safe only because no writer existed. SP-12 adds two writers (`EnablePSelection` on construction, `DisablePSelection` on shutdown and in every test's `defer`) while `analyzer.NewSelector` reads it from arbitrary goroutines, so the retype to `atomic.Bool` is mandatory, not stylistic: `TestPSelectionGate_ConcurrentAccess` runs 64 concurrent readers against one writer under `-race` and fails on the shipped `bool`. This is a declared behaviour change to a symbol SP-01 owns; the ADR records it. The shipped `TestPSelectionAvailable_DefaultsFalse` moves into `gate_test.go` under that exact name, upgraded to the `TestMain` recording described in the test plan so it observes the process default rather than whatever the last test left behind.

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
    // Regime first: every threshold below is relative to it. A zero-value Regime means the
    // Runtime did not resolve one, which is the unknown rung, not an error.
    reg := in.Regime
    if reg.TTLMaxSeconds <= 0 {
        reg = UnknownRegime(cfg)
    }
    anchor := in.LastRequestStartTS
    if anchor <= 0 {
        anchor = in.LastAPICallTS
    }
    ttl, gap := ClassifyTTL(in.Now, anchor, reg, in.EffortChanged)
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
    //                          OR idle_gap > ttl_max
    //                          OR (regime_known AND idle_gap > 0.8·ttl)
    //                          OR effort_changed )
    aboveSoftFloor := n > soft
    atChangepoint := in.Changepoint.AtChangepoint
    youngDalyElapsed := interval > 0 && elapsed > interval
    aboveHardCeiling := n > hard
    idleColdCache := ttl == TTLCold
    // Fire one band EARLIER than expiry when the regime is known: the summarization request still
    // reads the prefix from cache there, which is (1−r)·n cheaper than the same compaction after
    // the prefix dies. Gated on a known regime because the unknown band spans 150 s–3600 s.
    cacheExpiring := ttl == TTLExpiring &&
        reg.TTLMinSeconds == reg.TTLMaxSeconds &&
        gap >= cfg.Cache.ExpiringTriggerFraction*float64(reg.TTLMaxSeconds)

    if aboveSoftFloor {
        d.Reasons = append(d.Reasons, TriggerSoftFloor)
        if atChangepoint {
            d.Reasons = append(d.Reasons, TriggerChangepoint)
        }
        if youngDalyElapsed {
            d.Reasons = append(d.Reasons, TriggerYoungDaly)
        }
        if aboveHardCeiling {
            d.Reasons = append(d.Reasons, TriggerHardCeiling)
        }
        if idleColdCache {
            d.Reasons = append(d.Reasons, TriggerIdleColdCache)
        }
        if cacheExpiring {
            d.Reasons = append(d.Reasons, TriggerCacheExpiring)
        }
    }
    fired := aboveSoftFloor &&
        (atChangepoint || youngDalyElapsed || aboveHardCeiling || idleColdCache || cacheExpiring)

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
    d.Breakdown["ttl_min_seconds"] = float64(reg.TTLMinSeconds)
    d.Breakdown["ttl_max_seconds"] = float64(reg.TTLMaxSeconds)
    d.Breakdown["regime_write_multiplier"] = reg.WriteMultiplier
    d.Breakdown["fired_at_ttl_fraction"] = gap / float64(reg.TTLMaxSeconds) // corpus for tuning 0.8
    if reg.Disabled {
        d.Breakdown["cache_disabled"] = 1
    }
    if in.EffortChanged {
        d.Breakdown["cold_reason_effort_change"] = 1
    }
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
        out = append(out, BackgroundAdvanceFrontier) // O5 — always first: it is the latency lever
    }
    if in.ContextTokens > soft {
        out = append(out, BackgroundPrecomputeSlice)
    }
    if !haveDelta {
        out = append(out, BackgroundRefreshDelta)
    }
    if ttl == TTLCold {
        // A gap longer than the TTL is the "next idle window" §8.3 names for the bloom
        // rebuild, and the only moment at which DAG compaction and GC cost nothing.
        out = append(out, BackgroundRebuildBloom, BackgroundCompactDAG, BackgroundGC)
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
    graph      dag.Graph
    segs       store.SegmentLog
    turnPos    map[core.TurnIndex]turnAnchor // Turn → smallest Node.Pos at that turn
    builtAt    core.TurnIndex                // maxTurn the map was built at; rebuild when it advances
    built      bool                          // false until the first build (turn 0 is a legal builtAt)
}
type turnAnchor struct {
    Pos     int
    Segment core.SegmentID
}

// Assemble builds Inputs.Candidates: changepoint boundaries ∩ API-round boundaries (§8.4).
// maxTurn is the highest turn the Runtime has observed; it is the turn→Pos cache's version key.
func (a *candidateAssembler) Assemble(
    ctx context.Context,
    sess core.SessionID,
    cpTurns []core.TurnIndex,
    rounds map[core.TurnIndex]struct{},
    idx *reclaimableIndex,
    maxTurn core.TurnIndex,
) ([]scheduler.Candidate, error)
```

Algorithm, exactly:

1. Build (or reuse) the turn→position map. `dag.Graph` exposes no by-turn lookup, so the map is built with the one call that enumerates nodes — `graph.NodesAfter(0)` — recording, per `Node.Turn`, the **smallest** `Node.Pos` seen (00-ARCHITECTURE §5.9: *"Pos int — token position in the prefix — required for p-selection"*) and the `SegmentID` resolved from `segs.Range(turn, turn)` (first match; `0` when none). This is the expensive half of assembly — one full node walk plus one `segs.Range` per turn — so it is the half that is cached. The cache is keyed on the Runtime's own `maxTurn`: rebuild when `!a.built || maxTurn > a.builtAt`, reuse otherwise, and drop it on `BindSession`. Comparing two `core.TurnIndex` values costs nothing, which is the point — see the note below on why `graph.Stats()` is not the key. Then, for every recorded changepoint turn `t` in `cpTurns` (ascending), look up `posOf(t)`. Turns absent from the map are skipped and counted in `obs.Counter("sched.candidate.unresolved")`. A `Pos` that moves *within* an already-recorded turn (a non-anchor `AddNode` upsert) is picked up on the next turn advance, which is at most one turn stale and never stale across a compaction, because a compaction always follows at least one new turn.
2. `RoundBoundary = t ∈ rounds`. The round set is maintained by the Runtime and fed by the tap (`scheduler_tap.go`): `Qompack.md` §2.6 defines an API-round boundary as a **new assistant `message.id`**, and the one hook that fires exactly once per assistant turn is `Stop`. So the `ObserveStop` wrapper — and only that wrapper — records the current turn into `rounds`. Turn 0 is seeded as a round boundary at session bind so a session that has not yet produced a `Stop` still has one legal cut point.
3. `Coupling = graph.CrossingEdges(pos)`, called live, once per candidate, with **no cache and no version guard**. `CrossingEdges` is not a search over the graph: it is `sort.SearchInts(g.lo, pos) − sort.SearchInts(g.hi, pos)` over two presorted slices (`internal/dag/index.go:120-134`) — O(log E), measured at **0.27 µs warm** against a 5 µs budget (`docs/adr/0007-dag-slices-are-scores-not-drop-decisions.md:167`). Thirty-two candidates therefore cost about **9 µs**, and caching that is a loss, not a saving: the only invalidation probe `dag.Graph` offers is `Stats()`, which walks every node once and every edge three times (`EdgesByKind`, plus `danglingLocked()` twice, via the `Dangling` field and `needsCompactionLocked()`) and allocates two fresh `map[string]int` per call (`internal/dag/graph.go:550-582`) — roughly 50 000 map operations on the 5 000-node / 15 000-edge graph this slice benchmarks against, to avoid 9 µs of binary searches. Worse, it would not even be correct: a non-anchor `AddNode` upsert that moves a node's `Pos` (`internal/dag/graph.go:266-280`) changes no node or edge count, so a `Stats()`-keyed cache would serve stale coupling values indefinitely.
4. `ReclaimableTokens = idx.After(pos)`.
5. Sort ascending by `Pos`; if more than `maxAssembledCandidates` remain, keep the **highest-`Pos`** 32. Rationale: a very early boundary can only win when the cache is cold, and in that regime `rewrite = 0` makes even the 32nd-latest boundary a deep cut relative to `n`; keeping the newest boundaries preserves the resolution where the decision is actually close.
6. Return.

**Failure modes.** `graph == nil` or `segs == nil` ⇒ return `nil, nil` (no candidates), and `NewSchedulerRuntime` will already have refused to enable the p-selection gate. A `CrossingEdges` panic is recovered at the assembler boundary, logged `Loud`, and the candidate is dropped rather than failing the whole evaluation.

**Performance budget.** `BenchmarkAssemble` with 2 000 tool-use records, 40 changepoint turns and a 5 000-node graph: **≤ 20 ms/op cold cache, ≤ 200 µs/op warm cache**. The cache being measured is the turn→`Pos` map alone; the ~9 µs of `CrossingEdges` calls is paid on both paths and is inside the warm budget by a factor of twenty.

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

Per-seam behaviour. Every wrapper calls the inner seam **first**, so SP-08 has already written the `store.ToolUseRecord` the tap reads, and every wrapper swallows its own errors (a tap failure must never change a hook's result). The "Inner returns" column is the shipped shape from `internal/daemon/options.go:122-130` and fixes what each wrapper must pass through: three of the five decorated seams have no `hookio.Output` at all, so a wrapper written against `(hookio.Output, error)` does not compile:

| Seam | Inner returns | Tap work | Clock |
|---|---|---|---|
| `SessionStart` | `(hookio.Output, error)` | `rt.BindSession(e.SessionID)` — binds the id, loads `state/*.json` for that id (§ state files below), seeds turn 0 as a round boundary, `NotifyActivity(now)` | warm path, no budget concern |
| `ObserveTool` | `error` | `sig := observer.ExtractSignals(e)`; `rec, err := store.ToolUse(ctx, e.ToolUseID)`; on success `f := FeaturesFrom(hist, sig, rec.Tool, rec.TS)` then `rt.Observe(ctx, f, rec.Turn)`; `rt.NotifyActivity(rec.TS)`; `rt.NoteRequestStart(rec.TS)`; `rt.NoteEffort(e)` (see below); fold `rec.Tokens` into the burn-rate sample **and into `r.openSegTokens`, the open segment's token accumulator** (see below — this is the number `closeSegmentLocked` hands to `SegmentLog.Close`); then, when `sig.TodoCompleted \|\| sig.TestPassed \|\| sig.GitCommit`, `rt.CloseSegmentOn(ctx, rec.Turn, f, cause)` with `cause` ∈ `todo`/`test`/`commit` (first true wins) — these are the non-changepoint members of §8.5's "changepoint, todo completion, passing test run", plus the git-commit safe point G1.5 names | worker pool, budget **B-C** (50 ms) — never B-A |
| `ObserveStop` | `error` | same as `ObserveTool` minus the record lookup and the token fold (a `Stop` carries no `tool_use_id` and no token count), **plus** `rt.NoteAPIRound(turn)` — §2.6's "boundary on new assistant `message.id`". `turn` is the highest turn the runtime has seen. It calls `NotifyActivity` but **not** `NoteRequestStart`: `Stop` fires after generation completes and is therefore later than the request whose cache entry it would be dating | worker pool, B-C |

**`NoteEffort(e hookio.Event)`** — reads `e.Extra["effort"]`, unmarshals `{"level":"..."}`, and sets `effortChanged = level != lastEffort` before storing it (`lastEffort == ""` on the first event is not a change). Falls back to `cfgEnv.Getenv("CLAUDE_EFFORT")` when the object is absent. **This needs no new plumbing at all**, which is why it is worth doing: `effort` is a top-level key that no `hookio.Event` struct tag claims, so it already lands in `Extra`, and SP-08's `arch/sp08-observer-seams` amendment already restores `Extra` daemon-side from `req.Raw` (the transport drops it because `Extra` is `json:"-"`). The effort signal rides the exact channel that amendment builds for the subagent name. It is worth capturing because effort is part of the cache key — *"switching with `/effort` means the next request reads the entire conversation history with no cache hits"* — so a change empties the prefix instantly at a moment every wall-clock heuristic reads as maximally warm, and §5.4 calls that the second of the two good moments to cut.
| `ObservePrompt` | `(hookio.Output, error)` | `rt.NotifyActivity(now)` and `rt.NoteRequestStart(now)` **only**. This seam is called synchronously inside SP-05's 250 ms reply deadline, so the tap does no store I/O and no BOCD update here | reply path — keep under 1 ms |
| `SessionEnd` | `error` | `rt.Persist(ctx)` then `CloseSchedulerRuntime(rt)` | `flush` op, 20 s hook timeout |

The four seams SP-12 does **not** decorate — `PreCompact`, `Rehydrate`, `MCPInitialized`, `StatusExtra` — are left exactly as `Bind` found them. `WrapServicesForScheduler` never reads or replaces them, so their signatures (including `MCPInitialized func(ctx context.Context) bool` and `StatusExtra func(ctx context.Context) (json.RawMessage, error)`) are SP-13's and SP-14's business, not this slice's.

`BindSession` and `NoteAPIRound` are additive methods on the concrete `*schedRuntime` (a struct SP-12 owns), reached inside the package without a type assertion; neither appears on the `scheduler.Runtime` interface, so Rule W-3 is not touched.

**Failure modes.** `rt == nil` ⇒ `WrapServicesForScheduler` returns without touching `s` (waves without SP-12 are unaffected). `store.ToolUse` returning `core.ErrNotFound` ⇒ increment `obs.Counter("sched.tap.no_record")` and skip the BOCD update for that event; the timestamp work still happens. A panic anywhere in the tap is recovered at the wrapper boundary, logged `Loud` once per session, and the inner seam's result is returned unchanged — which for `ObserveTool`, `ObserveStop` and `SessionEnd` means the inner `error` value and nothing else, and for `SessionStart` and `ObservePrompt` means the inner `(hookio.Output, error)` pair.

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

    openSegTokens core.Tokens // Σ rec.Tokens observed since the current segment opened; the
                              // "tokens" pseudo-feature closeSegmentLocked must pass to
                              // SegmentLog.Close, reset to 0 when the successor opens

    lastDecision       scheduler.Decision
    lastEvaluateTS     core.UnixMilli     // when lastDecision was produced; the idle-tick decision TTL
    frontierRuns       uint64             // advanceFrontier executions; the starvation counter reads it
    frontierPlannedAt  uint64             // frontierRuns as of the decision that planned an advance
    frontierSkipTicks  int                // consecutive decisions that planned an advance that never ran
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

- `contextTokens` = `Σ Segment.Tokens` over this session's **closed** segments, obtained with one `segs.Range(0, r.maxTurn)` call, **plus `r.openSegTokens`, the tokens accumulated in the still-open segment**. Both halves are required. `Segment.Tokens` is written exactly once, by `SegmentLog.Close`, so a segment that is still open contributes zero to `Range` — counting only closed segments would make `n` lag the live context by the whole open segment, which is precisely the growth the soft floor exists to catch (and, since a session's first segment closes only at the first boundary, would hold `n` at 0 for the whole opening stretch). `r.openSegTokens` is maintained by the `ObserveTool` tap (it folds `rec.Tokens` there) and reset to 0 by `closeSegmentLocked` when the successor segment opens, so the two halves partition the session's tokens without double counting. SP-05's `SessionRegistry` deliberately is **not** the source: its `SessionState` (SP-05, `registry.go`) carries `Events`/`Dropped`/`Externalized` counters and no token total, so reading it would be reading a number that does not exist. The value is cached on `r.contextTokens` and recomputed at most once per `Evaluate`. `Range` failing ⇒ the closed half is `0` and only the open accumulator is counted, logged at `Debug`; that is the honest answer, not a guess.
- `effectiveWindow` = `r.effectiveWindow`, resolved once at `BindSession` by the ladder in the `thresholds.go` section (`CLAUDE_CODE_AUTO_COMPACT_WINDOW` → `QOMPACK_CONTEXT_WINDOW`/`QOMPACK_MAX_OUTPUT_TOKENS` → the §2.5 host defaults). It is never `0` after a bind, so `error_no_window` can only appear before the first `SessionStart` — which is exactly when the scheduler should decline to act. `Breakdown["window_source"]` records which rung supplied it.
- `maxOutputTokens` = the value the same ladder resolved (0 on rung 1, where the env var already names the effective window).
- `deltaPtr()` returns `nil` when `deltaSamples == 0`, honouring "null means measure at runtime, not zero".
- Budget: **≤ 25 ms p99** with 2 000 tool-use records and 32 candidates. Never called from a hook; only from the idle worker and the `status` op.

**`NoteRequestStart(ts)`** — sets `lastRequestStartTS = ts`. Called from the `ObserveTool` and `ObservePrompt` wrappers and **deliberately not from `ObserveStop`**. That asymmetry is the whole point: the API measures the cache TTL *"from the start of the request … not from the end of its response"*, and `Stop` fires after generation, so anchoring on it over-reports warmth by the entire generation time — minutes, on a long agentic turn. A tool result is what the next request carries, so the last `PostToolUse` of a turn is the tightest anchor the hook surface can offer; `UserPromptSubmit` covers the turn that used no tools. The estimate is one-sided by construction — `lastRequestStartTS ≤ true request start ≤ Stop` — so it can only widen the measured gap and make the classifier more conservative about warmth, never less. Additive method on the concrete type; not on the interface.

**`NotifyActivity(ts)`** — sets `lastActivity = ts`; sets `lastAPICallTS = ts` (E1: the API-call clock is what the sliding TTL keys on); updates the burn-rate EWMA from `(tokens − lastTokens)` over `(ts − lastTokensTS)` when both deltas are positive. It does **not** forward to `IdleController.Notify`: SP-05 already calls `Notify` from `registry.Touch` on every accepted request (SP-05, `registry.go`), and a second call from here would be a duplicate feeding the same controller.

**`IdleSince()`** — returns `(lastActivity, clock.Now()−lastActivity >= cfg.Scheduler.Idle.DetectAfterSeconds)`.

**δ is where the compaction request's own cost belongs, and that is a decision, not an omission.**
§5.3 v1.3 adds a `c·n` term for the summarization call's *input* — `r·n` against a live prefix, `n`
against a dead one — and `Evaluate` uses it only in the fire decision, never in `score(p)`, because
it is identical for every candidate and so cannot reorder them. Its *output* is a different matter.
Since Claude Code v2.1.198 the summarization request inherits the session's extended-thinking
configuration (§2.7), so on a thinking-enabled session it also emits thinking tokens, and their
volume is **not published**. Modelling them would put a guess inside an objective whose whole claim
is that it replaces guesses with measurement.

So it is measured instead. `RecordCompactionCost` already folds the observed wall-clock of a real
compaction into `δ`, and thinking shows up there for free — a thinking-enabled session's compactions
simply take longer, `δ` rises, and `√(2·δ·M)` lengthens the interval between them, which is exactly
the response a more expensive compaction should produce. Nothing needs to know *why* δ rose.
`Breakdown["delta_seconds"]` makes it visible, and `/qompack:status` shows it beside the interval, so
a user on a thinking-enabled session can see the cost they are paying rather than having it modelled
at them.

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
  "delta_ewma_seconds": 20.0,
  "delta_samples": 3,
  "burn_ewma_tokens_per_min": 800.0,
  "burn_samples": 41,
  "effective_window": 180000,
  "window_source": 1,
  "changepoint_turns": [0, 14, 33],
  "round_turns": [0, 3, 7, 14, 19, 33],
  "frontier_turn": 33,
  "residual_tokens": 9120,
  "last_decision": {
    "should_compact": true,
    "reasons": ["soft_floor", "young_daly"],
    "p_pos": 118230,
    "p_turn": 33,
    "p_score": -5379.3,
    "urgency": 1,
    "ttl": "warm",
    "young_daly_seconds": 268.3281573,
    "soft_floor_tokens": 99000,
    "hard_ceiling_tokens": 147000,
    "breakdown": { "context_tokens": 123000, "reclaimable": 600, "rewrite": 5962.5, "distortion": 16.8 }
  }
}
```

`breakdown` persists the whole `Decision.Breakdown` map; it is abridged here to the four keys `TestStateCodec_SchedulerRoundTrip` checks the arithmetic against.

**The document above is a `Decision` that `Evaluate` could actually produce, and the test asserts both halves — trigger and score — not just the score.** Every number below is derived from fields the document itself carries:

- **Trigger.** `n = breakdown.context_tokens = 123 000` sits above `soft_floor_tokens` 99 000 and below `hard_ceiling_tokens` 147 000, so `soft_floor` is reported and `hard_ceiling` is not. `M = (147 000 − 123 000) / 800 × 60 = 1 800 s`; `I* = √(2 × 20.0 × 1 800) = 268.3281573 s`, which is `young_daly_seconds`. `elapsed = (updated − last_compaction_ts)/1000 = (1 730 000 000 000 − 1 729 999 000 000)/1000 = 1 000 s > 268.33 s`, so `young_daly` fires. The AND-gate holds and one disjunct is true, so `should_compact` is `true`; the hard ceiling was not crossed, so `urgency` is `UrgencyAdvisory` = 1. `last_api_call_ts == updated` ⇒ idle gap 0 ⇒ `ttl` `warm` ⇒ `cacheFactor = 1.0`.
- **Score.** `rewrite = w·(n − p)·cacheFactor = 1.25 × (123 000 − 118 230) × 1.0 = 5 962.5`; `distortion = λ·coupling = 0.4 × 42 = 16.8`; `reclaimable = reclaimable_tokens · r = 6 000 × 0.1 = 600`; `p_score = 600 − 5 962.5 − 16.8 = −5 379.3`.

The two halves are checked together because a fixture that satisfies the score identity while contradicting the trigger — an implied `n` above `hard_ceiling_tokens`, say — is not output any `Evaluate` could produce, and `TestStateCodec_SchedulerRoundTrip` and V4-VERIFY row V4-SP12-16 both cite this document as the consistency proof. Note also that `p_pos`, `p_score` and the `reclaimable`/`rewrite`/`distortion` breakdown keys exist **only** on a decision that fired: `Evaluate` fills them in its `default:` branch, so a persisted `should_compact: false` document carries a zero `P` and none of those three keys.

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
        // "tokens" is NOT a BOCD feature: it is the pseudo-feature store.SegmentLog.Close reads
        // Segment.Tokens from (internal/store/segments.go:25-30, splitSegFeatures at :316-332).
        // Segment.Tokens has no setter and Close runs exactly once per segment, so omitting this
        // key leaves that segment's Tokens at ZERO PERMANENTLY — the store logs a Warn once per
        // segment when it is missing, and SP-12 is the caller the store's own comment names.
        // Everything downstream is keyed off it: contextTokens, every trigger clause, residual
        // accounting, and therefore ShouldCompact itself.
        "tokens": float64(r.openSegTokens),

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
    r.openSegTokens = 0 // the successor starts empty; the tap refills it from rec.Tokens
    r.metrics.Counter("sched.segment.closed." + cause).Inc()
    return err
}
```

**Where the token count comes from.** `r.openSegTokens` is a running sum the `ObserveTool` tap maintains: on every observed tool-use record it adds `rec.Tokens` (the same value it already folds into the burn-rate sample), under the same lock. That makes the close record's `Tok` field the sum of the tool-use tokens observed while the segment was open, and `closeSegmentLocked` is the only writer of `Segment.Tokens` in the whole system. `SP-08`'s `SessionEnd` close is the one other closer in the plan set and passes the same five feature keys without `"tokens"`; that is SP-08's line to fix, not SP-12's, and the V4 verification checkpoint re-checks it — but note that if it is not fixed, the final segment of every session contributes zero to `contextTokens` after the session ends, which is harmless for the live scheduler and visible in `TestFrontier_CloseWritesRealSegmentTokens`.

```go
// advanceFrontier is the body of the O3-registered "act.advance_frontier" task (gated on the
// BackgroundAdvanceFrontier value in Decision.Background) and the O5 mechanism.
func (r *schedRuntime) advanceFrontier(ctx context.Context) error
```

Algorithm:

1. If `r.ckpt == nil` **or** `r.sources == nil` (SP-10 not yet in the build) ⇒ return `nil` after `obs.Counter("sched.frontier.no_writer").Inc()`. This is the Rule W-2 posture: SP-12 develops against the stub and the golden fixtures, and the wave-3 verification checkpoint re-runs these tests against SP-10's real writer.
2. `unencodedClosed, err := r.segs.Unencoded(ctx, r.session)`; filter to `s.Closed == true`; sort ascending by `StartTurn`. `ids` below is that slice's `Segment.ID`s.
3. If empty ⇒ recompute `residual` (step 7), run the over-budget check (step 8), and return. **The check runs on this path too**: "nothing left to encode and still over budget" is one of the two states the signal exists to report, and an early return that skipped it would make the whole of step 8 unreachable.
4. Open the draft lazily: `src, err := r.sources()` then `r.draft, err = r.ckpt.Begin(ctx, r.session, r.lastCheckpointSeq, src)`. **SP-12 does not construct the `SourceSet`.** Its members include `pins.Store`, `grammar.Sequitur` and `tokens.Estimator`, none of which `daemon.Options` or `SchedulerRuntimeOptions` carries; SP-10 owns `checkpoint` and owns wiring `SchedulerRuntimeOptions.Sources` (together with `Checkpoints`) when it merges. What matters for invariant 1 is that `SourceSet` has no field that can carry live context text (00-ARCHITECTURE §5.14), so "checkpoint from a summary" stays uncompilable regardless of who builds it. A `Sources()` error is treated exactly like step 1: counter, `Warn`, return.
5. `newFrontier, err := r.ckpt.Advance(ctx, r.draft, ids)`.
   - `errors.Is(err, core.ErrAlreadyEncoded)` ⇒ this is a DPI-guard violation: log `Loud("frontier advance hit the DPI guard", "segments", ids)`, drop the offending ids from the batch, retry once with the remainder, and if it recurs abandon the draft (`r.ckpt.Abort`) and leave the frontier where it is. **Never** re-encode from a checkpoint.
   - Any other error ⇒ log `Warn`, leave the frontier, return the error to the idle controller (which records it and continues with the next task).
6. `r.frontier = newFrontier`; recompute residual; `r.dirty = true`.
7. Recompute residual as `residual = contextTokens − Σ Segment.Tokens over segments with EncodedOnce == true`, clamped at ≥ 0. `contextTokens` here is the same closed-plus-open sum `Evaluate` uses, so a segment whose `Close` omitted the `"tokens"` feature would count as zero on both sides and quietly under-report the residual — which is the second reason `closeSegmentLocked` must always pass it.
8. If `residual > cfg.Checkpoint.Frontier.MaxResidualTokens` (Appendix C: 20000), log once per session at `Warn` with `obs.Gauge("sched.residual_over_budget").Set(1)`, carrying `"unencodedClosed", len(unencodedClosed)` and `"frontierTurn", r.frontier` in the log line. **The condition is the residual alone.** An earlier draft suppressed the warning unless the backlog was empty, which inverted the signal: a queue of closed-but-unencoded segments *is* "O5 is not keeping up", and the only case that survived the extra conjunct — frontier as far forward as it can go, one open segment alone over 20 000 tokens — is not an O5 failure at all. Carrying the backlog length in the line keeps both readings distinguishable without suppressing either. This is the honest signal that O5 is not keeping up — §11.4's "watch for" discipline applied to our own mechanism rather than hidden.

**The second failure mode: O5 never got the budget.** `act.advance_frontier` is an idle task, and `IdleController.RunOnce` gives all registered tasks one shared 2 s wall budget (`idleRunBudget`, `internal/daemon/daemon.go:34`), running them in priority order and breaking as soon as `remain <= 0` (`internal/daemon/idle.go:160-163`). SP-05's `drain` runs first at priority 10, so a long drain can leave nothing for the acting task tick after tick; in `ModeDegradedPassive` the `act.` prefix skips it by design (`idle.go:157-159`). In both cases step 8 never executes, so a residual that is silently growing looks identical to a healthy one. `obs.Counter("sched.frontier.skipped")` — specified in the `scheduler_idle.go` section below — is what separates "O5 ran and is behind" (the `Warn` above) from "O5 never got the budget" (the counter). Both are required; either alone is misleading.

**Idempotence.** Running `advanceFrontier` twice in one idle window is a no-op on the second call because `MarkEncoded` (invoked inside SP-10's `Advance`) is idempotent for the same `CheckpointSeq`. A test asserts the second call returns `nil` and does not move the frontier.

---

### `internal/daemon/scheduler_idle.go` — O3 idle-time background work

```go
func RegisterSchedulerIdleWork(d Daemon, rt scheduler.Runtime, o SchedulerRuntimeOptions) error
```

Registers exactly six tasks on `d.Idle()` and binds `d` into the runtime (`r.d = d`, in-package). Lower priority number runs first.

**The priority band is 110–160, not 10–60, and that is load-bearing.** `daemon.New` has already registered three tasks on the same controller at 10/20/30 — `drain`, `sketches`, `metrics` (`internal/daemon/daemon.go:261-263`, `:288-290`). `Register` keys replacement on **name** only (`idle.go:97-105`) and `sortLocked` is a `sort.SliceStable` on priority (`idle.go:108-110`), so SP-12's six do not replace SP-05's three: a real `Daemon` ends up with **nine** tasks. Reusing 10/20/30 would interleave SP-12's work with SP-05's under stable-sort ties (`drain, act.advance_frontier, sketches, precompute_slice, metrics, refresh_delta, …`), putting the acting task behind a drain that can consume the whole 2 s budget. The 110–160 band keeps SP-12's six contiguous, in the relative order below, after SP-05's three — which is also the order V4-VERIFY §4.12 already assumes.

**The `act.` prefix is normative, not cosmetic.** SP-05's `IdleController.RunOnce` skips any task whose registered name begins `act.` when the contract monitor is not in `ModeFull` — that is the mechanism implementing 00-ARCHITECTURE §12.1's "no scheduler-initiated checkpoints" in `degraded-passive`. Exactly one of these six *acts*: `advance_frontier` drives `checkpoint.Writer.Advance`. It is therefore registered as **`act.advance_frontier`**. The other five are recording/maintenance work that must keep running while degraded (§12.1 preserves L0/L1), so they carry no prefix. The `scheduler.BackgroundTask` **values** are unchanged — `advance_frontier` etc., exactly as 00-ARCHITECTURE §5.13 declares them — because the prefix belongs to the registration name, not to the decision vocabulary; the gate below keys on the value.

| Registered name | Prio | Body | Guard |
|---|---|---|---|
| `act.advance_frontier` | 110 | `r.advanceFrontier(ctx)` (O5) | `BackgroundAdvanceFrontier` in `Background` ∧ `cfg.Checkpoint.Frontier.AdvanceOnSegmentClose` |
| `precompute_slice` | 120 | `graph.BackwardSlice(criteria, SliceOptions{Thin: cfg.Selection.Slicing == "thin", Decay: 0.85, Deadline: 250ms})`, stored on the runtime as `r.precomputed`/`r.precomputedOK` and read by SP-11 and SP-15 through the exported `daemon.PrecomputedSlice(rt)` helper | in `Background` |
| `refresh_delta` | 130 | pull `obs.Hist("checkpoint_finalize").Snapshot().P50` and fold it into `deltaEWMA`; recompute the burn EWMA; `Persist` | in `Background` |
| `rebuild_bloom` | 140 | `ledger.RefreshStaleness(ctx, store)` then, if it flipped anything or `ledger.Health().NeedsResize`, `ledger.RebuildBloom(ctx)` | in `Background` ∧ `ledger != nil` ∧ `cfg.Eliminations.RebuildOnStale == "nextIdle"` |
| `compact_dag` | 150 | `graph.Compact(ctx)` | in `Background` |
| `gc` | 160 | `store.GC(ctx, GCPolicy{RetainDays: cfg.Store.Retention.Days, RetainSessions: cfg.Store.Retention.Sessions, Deadline: remaining budget})` | in `Background` |

**One decision per idle pass, and the starvation counter that goes with it.** Nothing in SP-05 calls `scheduler.Runtime.Evaluate` — `Run`'s idle tick calls `d.idle.RunOnce(runCtx, idleRunBudget)` and nothing else (`daemon.go:613-618`) — so if SP-12 does not produce a decision on the idle path, `lastDecision.Background` stays empty and all six tasks are inert forever. `RegisterSchedulerIdleWork` therefore wraps all six bodies in one closure, `r.idleTask(name, body)`, whose first action is `r.refreshDecision(ctx)`:

- `refreshDecision` calls `Evaluate` when `clock.Since(r.lastEvaluateTS) >= idleRunBudget` and reuses `lastDecision` otherwise. `idleRunBudget` (2 s) bounds one whole `RunOnce` pass, so every task in a pass reads the decision the first task of that pass produced, and each pass gets exactly one `Evaluate` — which is also what keeps the ≤ 25 ms `Evaluate` budget off the per-task budget.
- It is also where starvation is detected. When the decision being replaced contained `BackgroundAdvanceFrontier` and `r.frontierRuns` has not moved since that decision was produced (`r.frontierRuns == r.frontierPlannedAt`), `refreshDecision` increments `obs.Counter("sched.frontier.skipped")` and `r.frontierSkipTicks`, and sets `obs.Gauge("sched.frontier.ticks_since_advance").Set(float64(r.frontierSkipTicks))`. `advanceFrontier` increments `r.frontierRuns` on entry and the gauge resets to 0 there. That is what makes "the budget ran out before the acting task" and "degraded-passive skipped it" visible as themselves rather than as silence, and it is the counterpart to the residual `Warn` in the frontier section: the `Warn` means O5 ran and is behind, the counter means O5 never ran.

Every task body then begins with the same three guards, in this order:

```go
func (r *schedRuntime) gate(name scheduler.BackgroundTask) bool {
    if !r.cfg.Scheduler.Idle.BackgroundWork { return false }
    r.mu.Lock(); defer r.mu.Unlock()
    return slices.Contains(r.lastDecision.Background, name)
}
```

so the pure `Evaluate` decides *what* runs and the daemon decides *when*, without SP-12 editing SP-05's `IdleController`. Before any session is bound, `refreshDecision`'s `Evaluate` short-circuits on `error_no_window` and returns an empty `Background`, so every task is inert — which is the correct posture for a daemon that has not yet seen a session, and for a session that has not yet reached the soft floor.

Each task honours the `ctx` deadline the `IdleController` passes and returns promptly on cancellation. `gc` additionally derives `GCPolicy.Deadline` from that context — `if dl, ok := ctx.Deadline(); ok { p.Deadline = time.Until(dl) }`, and when the context carries no deadline, `p.Deadline = 0` meaning "unbounded, but still cancellable" — so the pass is bounded. Truncation has two meanings since `4708ebe`, and the idle budget must be sized against the harsher one: a truncated **sweep** persists a cursor and resumes on the next pass, while a truncated **mark harvest** returns `GCReport{Truncated: true}` with nothing collected and no cursor, because sweeping against an incomplete live set would delete live objects. An idle budget smaller than the harvest therefore collects nothing however often the tick fires — which is a reason to grant `gc` a real budget, not a reason to treat every pass as resumable.

**Criterion set for `precompute_slice`.** The `[]dag.NodeID` criteria are, in order: the current segment's `KindSegment` node, every `KindFile` node touched in the last `defaultFeatureWindow` turns, and the most recent `KindUserPrompt` node. This mirrors §8.3's "current todo items, files under edit, the active plan, the most recent user intent" as far as the DAG exposes it in wave 3.

---

### `internal/cli/daemon.go` — SP-12's only modification outside its own files, and a file three subplans share

**Not `cmd/qompack/main.go`.** That file is 30 lines: it builds a `cli.Env` and calls `os.Exit(cli.Dispatch(…))`. It has no `daemon` subcommand, no `opts` and no `daemon.Options`. The composition root that builds `daemon.Options` and calls `daemon.New` is `runDaemon` in `internal/cli/daemon.go` — `opts := daemon.NewOptions(root, cfg)` at line 81, `d, err := daemon.New(opts)` at line 86, the single non-test `daemon.New` call site in the repo. SP-13 targets the same file for the same reason, and so does SP-08.

**Shared-file protocol — four subplans write into the same six-line window, in one fixed order.** `runDaemon`'s window between `opts := daemon.NewOptions(root, cfg)` (line 81) and `d, err := daemon.New(opts)` (line 86) is claimed by SP-08, SP-11, SP-12 and SP-13. None of the four owns the file exclusively, and no reviewer should read any of the four plans' "only modification outside my own files" bullets as an exclusivity claim over `internal/cli/daemon.go` — each bullet scopes that subplan's **own** diff, not the file. Inside the window the order is normative, because each step reads what the one before it wrote:

1. **SP-08's observer wiring** (`V3-SP-08`, commit 6) — constructs the observer and registers its `opts.Bind`, which sets the five `Services` seams SP-12's tap decorates.
2. **SP-11's resident-set block** (`V4-SP-11` prerequisite 1) — `symbols.New`, `store.Open`, `dag.Open`, `negknow.Open`, `checkpoint.OpenReader`, the `opts.Store`/`opts.Graph`/`opts.Ledger` assignments, the two `defer Close`s and `BindRehydrate(o, svc)`, each failure logged `Loud` and degraded.
3. **SP-12's Block 1 and Block 2** — construct the runtime from those three fields, then register the tap's `Bind`. Block 1 must follow step 2 or it reads nil handles; Block 2's `Bind` must follow step 1's `Bind` or it decorates seams nobody has set.
4. **SP-13's extension** (`V4-SP-13` spec §11) — `rehydrate.NewReporter`, `mcp.NewPromoter`, then `InstallMCPOp(&opts, …)` last in the window, because it must see the finished `opts.Store`/`opts.Graph`/`opts.Ledger` and because `daemon.New` registers its own fallback `mcp` route only for ops not already registered. It binds `Services.MCPInitialized` and no seam SP-12's tap touches, so its `Bind` running after Block 2's is harmless.
5. **`daemon.New(opts)`**, then SP-12's Block 3 (idle registration) immediately after it succeeds.

Wave 3 merges SP-10 → SP-11 → SP-12 → SP-13 and same-wave branches never branch from each other (`plans/README.md:38`), so on `feat/sp12-*` steps 2 and 4 do not exist yet: SP-12 lands blocks 1–3 against a window that has SP-08's wiring and nothing else, and the degrade path below is the one that runs **on the branch**. It stops running at the merge: SP-11 is already on `develop` by then, so step 2 arrives with SP-12's rebase rather than later. Step 4 arrives when SP-13 rebases onto a `develop` that already contains steps 1–3 and **inserts its own lines after SP-11's block and immediately before `daemon.New`** rather than appending them wholesale — that insertion, not a merge conflict resolution, is what puts the file in the order above.

**Dependency, stated explicitly: Block 1 reads `opts.Store`, `opts.Graph` and `opts.Ledger`, and nothing populates them yet.** `daemon.NewOptions` sets only `ProjectRoot`/`Cfg`/`Log`/`Metrics`/`Clock`/`Sketches` (`internal/daemon/options.go:58-68`), and `runDaemon` adds only `Log`/`Metrics`/`Clock` (`internal/cli/daemon.go:82-84`). Opening the store, the DAG and the ledger in that composition root is **created by SP-11's daemon-bootstrap block and extended by SP-13**, whose plans carry the assignment; SP-12 depends on it and must not duplicate it, because two `store.Store` handles on one project root is a corruption bug, not a redundancy. On SP-12's own branch that block does not exist — same-wave branches never branch from each other (`plans/README.md:38`) — so `NewSchedulerRuntime` returns `daemon: scheduler runtime: store required` on every real daemon start, Block 1 logs `Loud` and L3 stays disabled: the degrade path below, taken on purpose and visibly, not silently. On `develop` that state is transient rather than wave-long, because SP-11 merges one place **ahead** of SP-12: by SP-12's own merge the three fields are populated, and a `Loud` store-required line in a post-merge daemon start is then a defect to chase, not the expected reading. Every SP-12 unit test constructs the runtime directly with `fakeStore`/`fakeGraph`, so the unit suite is green either way; `test/e2e/scheduler_idle_test.go` is the test that fails if this dependency is missed, and it wires a real store/graph itself rather than relying on the composition root.

Block 1 and Block 2 go in `runDaemon` after `opts` is populated — at position 3 of the shared-file protocol above, i.e. **after SP-08's `opts.Bind(…)` call**, because `Bind` hooks run in registration order and the tap must decorate seams SP-08 has already set, and after SP-13's `opts.Store`/`opts.Graph`/`opts.Ledger` assignments — and **before** `daemon.New(opts)`. The SP-08 `Bind` this must follow is the observer wiring SP-08's commit 6 adds to `runDaemon`; if that call is absent, `WrapServicesForScheduler` decorates nil seams and L3 receives nothing, so verify it is present before adding Block 2:

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

Three added blocks (construction, tap binding, idle registration), no edits to any existing line, no changes to SP-05's daemon internals. A failure at any point degrades L3 only — the daemon, the store, and every hook keep working, which is 00-ARCHITECTURE §12.3's "everything else fails toward do nothing", and `runDaemon`'s own contract that it never surfaces a non-zero exit is preserved because every path here logs `Loud` and continues. `runDaemon` is 98 lines today (`internal/cli/daemon.go:29-126`; the file itself is 126 lines), and the three blocks add about 20.

**The 150-line ceiling on this file is SP-12's own house rule, not an architecture obligation.** 00-ARCHITECTURE §3.1 states `<150 LOC` for `cmd/qompack/main.go` alone (`plans/00-ARCHITECTURE.md:308`, inside the repo tree) and imposes no per-file bound anywhere else — do not cite §3.1 for this file. SP-12 adopts the number anyway for one reason: `internal/cli/daemon.go` is the one file four subplans write into, in a fixed order, and a composition root that still fits on two screens is the only cheap way a reviewer can check that order by eye. Treat it as a trigger, not a gate. It will in fact be crossed during wave 3 — SP-08's wiring adds ~6 lines, SP-11's resident-set block ~28 and SP-13's extension ~8 on top of SP-12's ~20 — so the expected outcome is the extraction, not the exception: move all three SP-12 blocks into an SP-12-owned `internal/cli/scheduler_wiring.go` with a single `wireScheduler(&opts)` call left in `runDaemon` at position 3 of the shared-file protocol, and record that in the ADR. The extraction is SP-12's to perform whenever the file crosses the line, whichever subplan's block pushed it over.

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

// l3policy owns NO pause model and declares NO latency constants. Deterministic replay makes
// no model call, so a wall-clock pause cannot be measured; §8.5 says decode dominates and
// decode length tracks the residual span, so the harness MODELS the pause linearly from the
// residual and labels it modelled everywhere it is reported (eval.LatencyModel.Modelled). A
// second copy of those coefficients in a package that cannot apply them is exactly the
// drifting duplicate this plan forbids elsewhere.
```

`KeepSet(ctx, s, at, budget)` returns `KeepSet{P: chosen.Pos, IDs: …, Tokens: …}` where `IDs` is every logged tool-call id **before** `P` plus every non-droppable id after it. `P` is what `test/replay/phase4_test.go` sums into `Score.RewriteTokens` via `w · (n − p_min)`. Deterministic: no clock, no randomness, `ReplayOptions.Deterministic` respected.

**Who fills `Run.ResidualSpan` and `Run.PauseMS`: `eval.Replay`, not the policy.** An `eval.Policy` has exactly one method that returns anything — `KeepSet(ctx, s, at, budget) (KeepSet, error)`, where `KeepSet` is `{IDs []string; Tokens core.Tokens; P int}` — and never touches `Run`. Inside `Replay`, per compaction point, the harness computes `residual := max(prefixTokens(blocks) − keep.P, 0)`, appends it to `run.ResidualSpan`, and appends `modelledPauseMS(h.lat, residual)` to `run.PauseMS` (`internal/eval/replay.go:217-224`), where `modelledPauseMS = round(lat.PauseBaseMS + lat.PausePerKResidualMS·residual/1000)` (`replay.go:285-287`). **`l3policy` therefore influences both fields through exactly one number: its choice of `P`.** It does not simulate a frontier, and it must not pretend to: it has no store, no `SegmentLog`, no `checkpoint.Writer` and no idle worker, by design (see the import rule above). The shipped coefficients are `PauseBaseMS = 3000` and `PausePerKResidualMS = 150` — 0.15 ms per residual token — from `eval.DefaultLatencyModel()` (`internal/eval/harness.go:16-21, 54-62`); no SP-12 file restates them, `policy_test.go` and `phase4_test.go` both read them from `eval.DefaultLatencyModel()`, and that is what keeps the assertion from drifting away from the harness it is asserting about.

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
| `TestClassifyTTL_Table` | **known** 5-minute regime (`TTLMin==TTLMax==300`); gaps 0, 10, 149, 150, 224, 299, 300, 3600 s | `warm, warm, warm, expiring, expiring, expiring, cold, cold`; boundary at exactly 150 s is `expiring`, at exactly 300 s is `cold`. Unchanged from the pre-regime version — a known regime collapses both bounds and the classifier is bit-identical |
| **`TestClassifyTTL_UnknownRegimeIsNotColdAt400s`** | unknown regime (`TTLMin=300`, `TTLMax=3600`); gap 400 s | `TTLExpiring`, **not** `TTLCold`. This is the row that stops the 40× mis-cut: under the old scalar `ttl=300` this gap classified cold, zeroed `rewrite`, and sent `chooseP` down the deep-cut branch on a prefix that a subscription session still had 53 minutes of |
| **`TestClassifyTTL_UnknownRegimeColdAtMax`** | unknown regime; gaps 3599 s, 3600 s | `expiring`, then `cold` — "provably cold" now means dead under **every** regime in the range, which is what the doc comment always claimed |
| **`TestClassifyTTL_EffortChangeIsColdAtAnyGap`** | any regime; `effortChanged = true`; gap 0 s | `TTLCold`. Effort is part of the cache key, so the prefix is gone rather than aging, and no wall-clock gap can reveal it |
| `TestClassifyTTL_UnknownWhenNoAPICall` | `anchorTS = 0` | `TTLUnknown`, gap `0` |
| `TestClassifyTTL_NegativeGapClamped` | `anchorTS > now` | `TTLWarm`, gap `0` |
| **`TestTTLAnchorIsNeverLaterThanStop`** (rapid) | any turn with `UserPromptSubmit ≤ PostToolUse ≤ Stop` | the resolved anchor is `≤` the `Stop` timestamp for every ordering. One-sided by construction: the new anchor can only make the measured gap **larger**, so the classifier can only become more conservative about warmth, and the change cannot regress in the direction that costs money |
| `TestCacheFactor_Ramp` | `ttl=300`; states/gaps from the row above | warm ⇒ `1.0`; gap 150 ⇒ `1.0`; gap 225 ⇒ `0.5`; gap 299 ⇒ `≈0.00667`; cold ⇒ `0.0`; unknown ⇒ `1.0` |
| `TestCacheFactor_MonotoneDecreasing_Property` (rapid) | `gap ∈ [0, 2·ttl]` | factor is non-increasing in gap and always in `[0,1]` |
| `TestSlidingTTLUsesAPICallNotCacheWrite` | `LastCacheWriteTS = Now−1000` (fresh), `LastAPICallTS = Now−400_000` (stale), **known** 5-minute regime | `Decision.TTL == TTLCold` — the E1 correction, asserted directly. Pinning the regime to known-5m is what keeps this row asserting E1 rather than accidentally re-asserting the new range logic |

### `internal/scheduler/cacheregime_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestResolveCacheRegime_LadderOrder` | each env var alone, then `FORCE_PROMPT_CACHING_5M=1` **and** `ENABLE_PROMPT_CACHING_1H=1` together | `force_5m`, `disabled`, `enable_1h`, `unknown` from their own rungs; the conflicting pair resolves to `force_5m`, because the Claude Code reference says it applies "regardless of authentication" and names overriding a managed-settings `ENABLE_PROMPT_CACHING_1H` as its purpose |
| `TestResolveCacheRegime_WriteMultiplierTracksTTL` | `enable_1h`, then `force_5m` | `WriteMultiplier == 2.0`, then `1.25`; `ReadMultiplier == 0.1` in both. These are the two documented figures and the reason `w` cannot be a scalar |
| `TestResolveCacheRegime_UnknownTakesTheDearerW` | no env vars set | `TTLMin == cfg.Cache.TTLSeconds`, `TTLMax == 3600`, `WriteMultiplier == 2.0`. Charging the higher write price under uncertainty biases toward shallower cuts, and a shallow cut costs reclaim while a deep one costs money |
| `TestResolveCacheRegime_DisabledZeroesThePremium` | `DISABLE_PROMPT_CACHING=1`; then `DISABLE_PROMPT_CACHING_OPUS=1` with a matching and a non-matching model | `r == w == 1.0` and `Disabled` for the global var and the matching model; the non-matching model falls through to the next rung. With no cache there is no read discount and no write premium |
| `TestResolveCacheRegime_SubagentPinned5m` | `agent_id` present, `ENABLE_PROMPT_CACHING_1H=1` | `(300, 300, 0.1, 1.25)`, source `subagent_5m` — "Subagents use the five-minute TTL even on a subscription" |
| `TestResolveCacheRegime_ReadsEnvThroughConfigEnv` | injected `config.Env.Getenv` | resolution never calls `os.Getenv`; `git grep -n 'os\.Getenv' -- internal/scheduler` returns nothing |
| `TestResolveCacheRegime_NoAppendixCKeyMoved` | — | `config.Defaults().Scheduler.Cache` is byte-identical to Appendix C after resolution runs. The regime is computed beside the config, never written back into it, which is what keeps `TestDefaults_MatchesAppendixCVerbatim` green |

### `internal/scheduler/youngdaly_test.go`

These cases live in `youngdaly_test.go`, which **absorbs** the shipped `formulas_test.go` rather than sitting beside it: `TestYoungDaly_Formula` already exists there and the two files cannot both declare it. Move the shipped assertions into the row below.

| Test | Input | Expected |
|---|---|---|
| `TestYoungDaly_Formula` | `δ=20`, `M=1800`; **plus the four shipped `formulas_test.go` assertions**: `(30,600)`, `(0,600)`, `(-1,5)`, `(30,0)`, `(30,-5)` | `268.3281572999748` (±1e-9); `√(2·30·600)` (±1e-9); the four degenerate inputs all `0` |
| `TestYoungDaly_NonPositive` | `(0,1800)`, `(20,0)`, `(-1,5)`, `(NaN,5)`, `(Inf,5)` | all `0` — the NaN/Inf rows are the declared behaviour change over the shipped body, which returns `NaN` for them |
| `TestMTBF_FromBurnRate` | `context=120_000`, `hard=147_000`, `burn=900` | `1800.0` |
| `TestMTBF_ZeroWhenAtOrAboveCeiling` | `context=150_000`, `hard=147_000` | `0` |
| `TestMTBF_ZeroWhenBurnUnknown` | `burn=0` | `0` |
| `TestResolveDelta_ConfigOverridesRuntime` | cfg `= ptr(30)`, inputs `= ptr(12)` | `(30, true)` |
| `TestResolveDelta_RuntimeUsedWhenConfigNil` | cfg `= nil`, inputs `= ptr(12)` | `(12, true)` |
| `TestResolveDelta_NilMeansMeasureNotZero` | both `nil` | `(0, false)`; and `Evaluate` sets `young_daly_delta_unmeasured=1` and **never** fires `TriggerYoungDaly` — the explicit test that JSON `null` is not read as zero |

### `internal/scheduler/skirental_test.go`

`TestSkiRentalShouldWrite`: `r=0.1, w=1.25` ⇒ threshold `12.5`; `expectedReads=12` ⇒ `false`, `12.5` ⇒ `false`, `12.6` ⇒ `true`. `r=0` or `w=0` ⇒ `false` (the `w=0` row is the declared behaviour change over the shipped body). A grep test in the same file asserts the literal `12.5` appears only inside `_test.go`.

`skirental_test.go` also **absorbs** the two shipped `formulas_test.go` cases under their existing names, since that file is deleted in the same commit: `TestSkiRental_ComputedNotLiteral` (`13/0.1/1.25` ⇒ true, `12/0.1/1.25` ⇒ false, `1/0/1.25` ⇒ false) and `TestSkiRental_ThresholdTracksConfig` (`6/0.2/1.0` ⇒ true, `5/0.2/1.0` ⇒ false, `-100/0.2/1.0` ⇒ false). Neither asserts the `w = 0` case, so both pass unchanged against the new body.

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
| `TestEvaluate_IdleColdCache_Fires` | `LastAPICallTS=Now−400_000`, **known 5-minute regime** | `true`, `Reasons` contains `idle_cold_cache`, `TTL=TTLCold`. The regime must be pinned: on the unknown rung a 400-second gap is `expiring`, and leaving it unpinned would make this row assert the old scalar behaviour by accident |
| **`TestEvaluate_CacheExpiring_FiresBeforeExpiry`** | known 5-minute regime, gap 250 s (`0.83·TTL`, above the `0.8` fraction) | `true`, `Reasons` contains `cache_expiring` and **not** `idle_cold_cache`; `TTL == TTLExpiring`; `CacheFactor > 0`. This is the row that buys the `(1−r)·n` saving: the prefix is still readable, so the summarization request this recommendation leads to reads it at `r` instead of reprocessing it at full price |
| **`TestEvaluate_CacheExpiring_SilentBelowFraction`** | known 5-minute regime, gap 200 s (`0.67·TTL`) | `cache_expiring` absent. The trigger must not fire during ordinary between-turn pauses |
| **`TestEvaluate_CacheExpiring_SilentWhenRegimeUnknown`** | unknown regime, gap 3000 s (above `0.8·TTLMax`) | `cache_expiring` absent. Firing here would mean compacting on a threshold derived from a TTL the scheduler has just admitted it cannot identify |
| **`TestEvaluate_CacheExpiring_RequiresSoftFloor`** | known regime, gap 250 s, `ContextTokens` below the soft floor | `ShouldCompact == false`. Gated like every other trigger — an aging cache is not a reason to compact a small context |
| **`TestEvaluate_EffortChangeIsColdImmediately`** | `EffortChanged=true`, gap 0 s, any regime | `TTL == TTLCold`, `Breakdown["cold_reason_effort_change"]==1`. §5.4's second good moment to cut, manufactured instantly at a moment wall-clock reads as maximally warm |
| `TestEvaluate_ReasonsOrderStable` | all four clauses true | `["soft_floor","changepoint","young_daly","hard_ceiling","idle_cold_cache"]` exactly |
| `TestEvaluate_ArgmaxLatestWhenWarm` | default (warm) | `P.Pos == 118_000` — the latest boundary; `PScore == −1_916.8` (±1e-9) and negative, asserted as intended |
| `TestEvaluate_ArgmaxDeepestWhenCold` | `LastAPICallTS=Now−400_000`, **known 5-minute regime** | `P.Pos == 40_000` — the deepest boundary; `PScore == 2_916`; `Breakdown["rewrite"]==0` |
| **`TestEvaluate_UnknownRegimeDoesNotDeepCutAt400s`** | the same fixture, **unknown regime** | `P.Pos == 118_000`, not `40_000`; `Breakdown["rewrite"] > 0`. Same inputs, same 400-second gap, opposite cut — which is the whole finding. Under the old scalar the scheduler rewrote 80 000 tokens believing it free; here it rewrites 2 000 and pays for them honestly |
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
| `TestPSelectionAvailable_DefaultsFalse` | the shipped `formulas_test.go` case, moved here under its exact name and strengthened: `TestMain` records `PSelectionAvailable()` into a package var **before** calling `m.Run()`; the test asserts that recorded value | `false` — the only way to observe the process default without depending on test ordering |
| `TestEnableDisablePSelection` | `Enable` then `Disable`, each with `defer DisablePSelection()` | `true` then `false` |
| `TestPSelectionGate_ConcurrentAccess` (`-race`) | 64 goroutines reading while one writes | no race, no torn read. This case is why the flag is retyped from SP-01's plain `bool` to `atomic.Bool`: it fails on the shipped declaration, and is the test that justifies the retype in the ADR |

### `internal/scheduler/schedulertest/suite.go` and `behaviour.go`

Flip every `t.Skip` SP-01 left. `RunEvaluateSuite` gains: threshold arithmetic, the five trigger clauses, the argmax limbs, purity, and `Breakdown` completeness. `RunDetectorSuite` gains: normalization, bounded posterior, marshal round-trip, determinism. `RunRuntimeSuite` gains: `Observe` → `State` consistency, `NotifyActivity` → `IdleSince`, `Persist` round-trip. Do not rename SP-01's entry points (Rule W-3). A merge blocker check: `grep -R "t.Skip" internal/scheduler/schedulertest/` must return nothing.

**`behaviour.go` is a shipped grader SP-12 inherits, and flipping the skip is what makes it run.** Its header says so: *"They are authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so SP-12 inherits them rather than writing its own grader."* The skip is `skipIfStub` in `suite.go:99-106`, which probes the factory-supplied `Runtime.Evaluate` for `core.IsNotImplemented` (`suite.go:91-95`); the moment SP-12 supplies a real Runtime, the whole truth table runs — against the package-level `scheduler.Evaluate`, which is what these cases actually call. As shipped it would fail against the `Evaluate` specified here for two independent reasons: `baseInputs()` supplies no candidate, so no case can ever reach `ShouldCompact = true`; and its Young–Daly fixture keys on the wrong clock and derives `M` without the host buffer. `behaviour.go`'s own note (lines 24–26) authorises the second fix in as many words — *"adjust `assumedMTBFSeconds` and the Young-Daly fixture below rather than the truth table's other cases"* — and the first changes no case's expectation, only the fixture every case starts from. **Three edits, and nothing else in the file; the truth table itself is untouched:**

1. **`baseInputs()` gains a placeable candidate.** It sets no `Candidates` at all today, so `chooseP` reports `ok == false` and `Evaluate` takes its `no_candidates` branch — `ShouldCompact = false` — while `changepoint_fires`, `hard_ceiling_fires`, `young_daly_fires` and `idle_cold_cache_fires` all `require.True(t, got.ShouldCompact)`. Refusing to claim a compaction the scheduler cannot place is the correct behaviour (it is what `TestEvaluate_NoCandidates` pins and what closes G7.6), so the fixture is what changes, not the truth table. Add, alongside the other `const` values at the top of the file and in the same style (no forbidden literal, since this is not a `_test.go` file):

   ```go
   candidatePosTokens         = 120_000 // strictly below quietContextTokens (150 000)
   candidateReclaimableTokens = 40_000
   candidateCouplingEdges     = 12
   ```

   and, in `baseInputs()`:

   ```go
   in.Candidates = []scheduler.Candidate{{
       Pos:               candidatePosTokens,
       RoundBoundary:     true,
       ReclaimableTokens: core.Tokens(candidateReclaimableTokens),
       Coupling:          candidateCouplingEdges,
   }}
   ```

   `Pos` must stay strictly below `quietContextTokens` for the same reason `baseInputs()` in `testdata_test.go` does: a candidate beyond `n` is not a possible prefix position. `RoundBoundary: true` keeps the case out of the `round_boundary_relaxed` path. The two negative cases are unaffected — `quiet_baseline_does_not_compact` still has no true disjunct, and `below_soft_floor_suppresses_every_other_trigger` still fails the AND-gate.

2. **`baseInputs()` gains `in.LastCompactionTS = in.Now`.** The Young–Daly clause is keyed on `LastCompactionTS`, and `Evaluate` computes no interval at all when it is `≤ 0` (`young_daly_no_baseline`). Without this line `runYoungDalyFormulaCase` compares its expected `I*` against a `YoungDalySeconds` of `0` and fails, and `young_daly_fires` can never fire. Seeding it at `in.Now` gives `elapsed = 0`, so the quiet baseline still reports no `young_daly` reason.

3. **`assumedMTBFSeconds` subtracts the host buffer, and `young_daly_fires` retargets its clock.** The grader derives `headroom := EffectiveWindow − Cfg.HardCeilingMargin − ContextTokens` (line 79), which omits `HostAutoCompactBuffer`; SP-12's `mtbfSeconds` measures headroom to `HardCeiling = EffectiveWindow − 13 000 − margin`. With the shipped fixture (window 200 000, margin 15 000, context 150 000, burn 500/min, δ 30) the grader expects `I* = √(2·30·(35 000/500·60)) = 501.996 s` while `Evaluate` produces `√(2·30·(22 000/500·60)) = 397.995 s`, and `runYoungDalyFormulaCase`'s `require.InDelta(…, 1e-6)` fails on the difference. Change the derivation to `headroom := float64(in.EffectiveWindow) - float64(scheduler.HostAutoCompactBuffer) - float64(in.Cfg.HardCeilingMargin) - float64(in.ContextTokens)`, which is `22 000` and matches `Evaluate` exactly. In `young_daly_fires`, replace `in.LastCacheWriteTS = in.Now - core.UnixMilli(elapsedMS)` with `in.LastCompactionTS = in.Now - core.UnixMilli(elapsedMS)`: `LastCacheWriteTS` never enters the decision (§8.4's E1 correction and `ttl.go` above), so setting it moves nothing. The case's own `elapsedMS = int64(interval*2*millisPerSecond) + millisPerSecond` then works out to about 797 s of elapsed time against a 397.995 s interval, and the clause fires.

The grader keeps discriminating after all three edits: the two negative cases still assert the negative space precisely, and `runYoungDalyFormulaCase` still compares `Decision.YoungDalySeconds` against `scheduler.YoungDaly(δ, M)` computed independently in the test file — which is the point of that case, and the reason `assumedMTBFSeconds` must be *corrected* rather than deleted.

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
| `TestAssemble_CouplingRecomputedEveryCall` | assemble twice with three candidates and no turn advance | `fakeGraph` records exactly 6 `CrossingEdges` calls — coupling is deliberately **not** cached: it is two binary searches, ~0.27 µs, and the only invalidation probe `dag` offers costs far more than it saves |
| `TestAssemble_TurnPosCachedUntilTurnAdvances` | assemble twice at the same `maxTurn` | `fakeGraph` records exactly 1 `NodesAfter(0)` call and one `segs.Range` per turn, not two rounds of them |
| `TestAssemble_TurnPosRebuiltWhenTurnAdvances` | assemble, advance `maxTurn`, assemble again | 2 `NodesAfter(0)` calls; the new turn resolves to a candidate |
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
| `TestRuntime_ContextTokensFromSegments` | `fakeSegmentLog.Range` returning closed segments of 10 000 + 25 000 tokens, and 4 000 tokens observed through the tap since the current segment opened | `Breakdown["context_tokens"] == 39_000` — closed segments **plus** `openSegTokens`, so `n` does not lag the live context by the whole open segment; `Range` erroring ⇒ `4_000` (the open accumulator alone) and no panic |
| `TestRuntime_OpenSegmentTokensResetOnClose` | observe 4 000 tokens of tool use, close the segment, observe 1 500 more | the close passes `feats["tokens"] == 4000`; after the roll-open `context_tokens` counts `4 000` (now closed) + `1 500` (open) with no double count |
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
| `TestStateCodec_SchedulerRoundTrip` | the document printed in the spec above | every field round-trips; **and the document is re-derived, not just re-read**: feeding its own fields back through `scheduler.Evaluate` reproduces it — `reasons == ["soft_floor","young_daly"]`, `should_compact == true`, `urgency == 1`, `young_daly_seconds == 268.3281573` (±1e-6), `p_score == −5379.3` and equal to `reclaimable − rewrite − distortion` |
| `TestStateCodec_UnknownFieldsIgnored` | add `"future_key": 1` to both files | load succeeds, no warning escalated beyond `Debug` |
| `TestStateCodec_WritesAtomicallyNotAppendOnly` | write both files twice | `paths.WriteAtomic` used (a temp file appears in `.qompack/tmp/` and is renamed); `paths.AppendOnly` never called; the second write replaces rather than appends |
| `TestStateCodec_TurnListsCapped` | 10 000 entries in each list | exactly `maxTurnHistory` written, oldest dropped |

### `internal/daemon/scheduler_tap_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestWrapServices_CallsInnerSeamsFirst` | `Services` with recording inner seams, each returning a distinctive value | every decorated inner seam ran and tap work is observed **after** it; the pass-through is asserted per shape — `SessionStart` and `ObservePrompt` return the inner `(hookio.Output, error)` unchanged, and `ObserveTool`, `ObserveStop` and `SessionEnd` return the inner `error` unchanged (those three have no `hookio.Output` at all, per `internal/daemon/options.go:122-130`) |
| `TestWrapServices_UndecoratedSeamsUntouched` | `Services` with `PreCompact`, `Rehydrate`, `MCPInitialized` and `StatusExtra` set | all four function values are the same pointers after wrapping — SP-12 decorates five seams and only five |
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
| `TestFrontier_CloseWritesRealSegmentTokens` | a **real** `store.SegmentLog` (not `fakeSegmentLog`): open a segment, observe 4 000 tokens of tool use through the tap, close it via `closeSegmentLocked` | `segs.Get(id).Tokens == 4_000`, and the store logs no "closed without a tokens feature" `Warn`. This is the case a fake cannot catch: `feats["tokens"]` is the only writer of `Segment.Tokens` in the system, and omitting it leaves every segment at zero permanently, which silently pins `context_tokens` to 0 and `ShouldCompact` to false |
| `TestFrontier_ResidualRecomputed` | context 40 000, encoded segments summing 31 000 | `residual == 9 000` |
| `TestFrontier_ResidualNeverNegative` | encoded sum exceeds context | `residual == 0` |
| `TestFrontier_ResidualOverBudgetWarnsOnce` | residual 25 000 > `maxResidualTokens` 20 000, **run twice: once with nothing to encode and once with a three-segment unencoded backlog** | exactly one `Warn` per session, gauge set to 1, in **both** runs — a backlog must not suppress the warning — and the backlog run's log line carries `unencodedClosed=3` |

### `internal/daemon/scheduler_idle_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestIdleTasksRegistered` | `RegisterSchedulerIdleWork` against a **real** `daemon.New`-constructed controller, which already holds SP-05's `drain`/`sketches`/`metrics` at 10/20/30 | `d.Idle().Registered()` returns nine names, and the six SP-12 names appear in exactly this relative order **after** all three SP-05 names: `act.advance_frontier, precompute_slice, refresh_delta, rebuild_bloom, compact_dag, gc`, registered at 110…160. Asserting "exactly six" would only pass against a bare controller — a test that cannot see the thing it checks |
| `TestIdleActingTaskSkippedInDegradedPassive` | `IdleController` whose `mode()` reports `ModeDegradedPassive` | `RunOnce`'s `ran` list contains all five unprefixed SP-12 names and **not** `act.advance_frontier` — 00-ARCHITECTURE §12.1's "no scheduler-initiated checkpoints", enforced by SP-05's prefix rule. Asserted by containment, not by list length, so the assertion holds whether the controller also carries SP-05's three tasks |
| `TestIdleWorkBindsDaemon` | `RegisterSchedulerIdleWork(d, rt, o)` | `r.d == d` afterwards; calling it twice is idempotent |
| `TestIdleTaskInertBeforeSessionBind` | run tasks before any `BindSession` | `refreshDecision` evaluates once, the decision carries `Breakdown["error_no_window"]==1` and an empty `Background`, every task returns nil and performs no work |
| `TestIdleRefreshesDecisionOncePerPass` | `FakeClock`; run all six task bodies inside one `RunOnce` budget, then advance past `idleRunBudget` and run again | `Evaluate` ran exactly twice, not twelve times; every task in a pass read the same `lastDecision` |
| `TestIdleFrontierSkippedCounted` | a decision containing `BackgroundAdvanceFrontier`, then two further passes in which `act.advance_frontier` never runs (controller in `ModeDegradedPassive`, then budget exhausted by an earlier task) | `obs.Counter("sched.frontier.skipped") == 2` and `obs.Gauge("sched.frontier.ticks_since_advance") == 2`; after one real `advanceFrontier` run the gauge is 0 and the counter stops rising. This is what distinguishes "O5 never got the budget" from the residual `Warn`'s "O5 ran and is behind" |
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
| `BenchmarkAssembleCandidates_2000ToolUses` | **≤ 20 ms/op cold, ≤ 200 µs/op warm** | the turn→`Pos` cache is the difference; `CrossingEdges` is uncached and paid on both paths |
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
| `TestPolicy_PauseModelAnchors` | `lat := eval.DefaultLatencyModel()`; residual 150 000 then 20 000 | `25_500 ms` then `6_000 ms`, computed as `lat.PauseBaseMS + lat.PausePerKResidualMS × residual / 1000` with the shipped `3000` / `150` — i.e. **0.15 ms per residual token, not 0.12**. The stock anchor sits inside §6.7's observed "15–40 s" band and the frontier-advanced anchor is the §8.5 "10–20K residual" case. The coefficients are read from `eval`, never restated here, so the row cannot drift from the harness it describes |

### `test/replay/phase4_test.go` — the Phase 4 exit criterion

Runs against SP-02's committed 24-session synthetic corpus in `testdata/sessions/synthetic/` with `ReplayOptions{Deterministic: true, Seed: 1}`. Policies compared: SP-02's `stock` baseline and `l3policy.New(config.Defaults())` (`qompack-l3`).

| Test | Assertion |
|---|---|
| `TestPhase4_RewriteTokensReduced` | `Σ Score.RewriteTokens[qompack-l3] ≤ 0.80 × Σ Score.RewriteTokens[stock]` across all 24 sessions. This is the direct measurement of "measured reduction in total rewrite tokens per session". The per-session table is written to `.qompack/eval/phase4-rewrite.json` for the PR comment. |
| `TestPhase4_NoDivergenceRegression` | For `FirstDivergenceTurn`, `FileSetJaccard`, `DecisionPreservation`, `RedundantReads` and `ReAttempts`, `qompack-l3` regresses by **≤ 2%** against `stock` (§11.3). A regression beyond 2% fails unless the PR body carries a `sign-off:` trailer, matching the `replay-gate` rule. |
| `TestPhase4_ResidualSpanFlatAsSessionGrows` | Least-squares slope of `median(Score.ResidualSpan)` against session turn count over the 24 sessions is **≤ 0.02 tokens/turn**, and `median(ResidualSpan)` for the longest quartile is **≤ 1.25 ×** that of the shortest quartile. **What this bounds is `n − p`, not frontier advancement** — see the row below and the paragraph after this table. |
| `TestPhase4_ResidualUnderMaxResidualTokens` | `Score.ResidualSpan.P95 ≤ config.Defaults().Checkpoint.Frontier.MaxResidualTokens` (20 000), matching §8.5's "10–20K tokens rather than 150K, regardless of how long the session has run". |
| `TestPhase4_ResidualSpanIsRewriteSpan` | The negative control on the two rows above: across the corpus, `Σ Score.ResidualSpan × cfg.Scheduler.Cache.WriteMultiplier == Σ Score.RewriteTokens` to within per-session rounding. The two quantities are **collinear by construction** in the harness (`replay.go:217-223` computes `residual = prefixTokens − keep.P`; `score.go:59-61,86` computes `RewriteTokens = round(w × Σ(n − clamp(P)))`), so `TestPhase4_RewriteTokensReduced` and the two residual rows are one measurement wearing three hats, not three independent gates. Asserting the identity makes that visible in the suite instead of leaving a reader to infer three confirmations from one. |
| `TestPhase4_CompactionPauseModelled` | `Score.CompactionPauseMS` is a **linear function of residual span** under the harness's own coefficients: `lat := eval.DefaultLatencyModel()`, and for every run `PauseMS[i] == round(lat.PauseBaseMS + lat.PausePerKResidualMS × ResidualSpan[i] / 1000)`, asserted exactly. The coefficients are **read from `eval`, never restated** — the shipped values are `3000` and `150` (0.15 ms per residual token), and a copy in this file would silently describe a model the harness is not using. The modelled-ness is recorded by SP-12's own artifact — `test/replay/phase4_test.go` writes `.qompack/eval/phase4-pause.json` containing `{"pause_modelled": true, "intercept_ms": lat.PauseBaseMS, "ms_per_k_residual": lat.PausePerKResidualMS, "per_session": […]}`, the numbers taken from the same `lat` the assertion used — and **not** by inventing a field on `eval.Report` or `eval.Score`, neither of which has one (00-ARCHITECTURE §5.18). Deterministic replay makes no model call, so reporting a "measured" pause would be dishonest measurement — precisely the sin §1.3 RC-3 indicts — and the flatness claim is carried by residual span, which is real. |
| `TestPhase4_PSelectionGateHonoured` | With `scheduler.DisablePSelection()` in effect, `l3policy` still produces a `KeepSet` (p-selection is not submodular selection) but `analyzer.NewSelector` refuses to construct — the closing-note-3 boundary asserted from both sides. |

**What this suite proves, and what it does not.** These rows bound `n − p`: how late the policy places the cut. They do **not** exercise O5. `ResidualSpan` in the harness is `prefixTokens − KeepSet.P`, computed inside `Replay` from the one number a policy returns, and `RewriteTokens` is `w` times the same span — the identity `TestPhase4_ResidualSpanIsRewriteSpan` asserts. `l3policy` by design has no store, no `SegmentLog`, no `checkpoint.Writer` and no idle worker (it may not import `internal/daemon`), so frontier advancement is not in the replay path in any form: a policy that simply returned the latest candidate would pass every row above with the whole of `scheduler_frontier.go` deleted. **The §10 Phase 4 amortization clause is therefore discharged by the live integration test, V4-VERIFY §4.4's `TestV4_FrontierAdvancementKeepsResidualSpanODelta`, which drives a real daemon and carries its own `advanceOnSegmentClose = false` negative control.** The replay rows here are the cut-placement half of the exit criterion and are labelled as such in the PR body; claiming they test O5 would be exactly the overclaim §1.3 RC-3 indicts.

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
**Files modified:** `internal/scheduler/doc.go`, `internal/scheduler/detector.go` — SP-01's `NewBOCD` and `stubDetector` live in `detector.go`, so `bocd.go` declares the `bocd` type and its methods only: delete `stubDetector` and repoint `NewBOCD` at the real constructor in place. Declaring `NewBOCD` a second time in `bocd.go` does not compile.

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

**Files added:** `internal/scheduler/thresholds.go`, `internal/scheduler/cacheregime.go`, `internal/scheduler/ttl.go`, `internal/scheduler/youngdaly.go`, `internal/scheduler/skirental.go`, `internal/scheduler/dropclass.go`, and their `_test.go` peers.
**Files deleted:** `internal/scheduler/formulas.go` and `internal/scheduler/formulas_test.go` — `YoungDaly` and `SkiRentalShouldWrite` move into `youngdaly.go`/`skirental.go` in this same commit, so the deletion and the additions must land together or the package holds two declarations of each and does not compile.
**Files modified:** `internal/scheduler/types.go` (the three godoc corrections in the `thresholds.go` section: `Decision.HardCeilingTokens`, `TriggerHardCeiling`, `TriggerYoungDaly`).

- [ ] Write `thresholds_test.go`, `cacheregime_test.go`, `ttl_test.go`, `youngdaly_test.go`, `skirental_test.go`, `dropclass_test.go` first (6 + 7 + 6 + 8 + 4 + 6 cases), including `TestEffectiveWindow_Section25Arithmetic`, `TestSlidingTTLUsesAPICallNotCacheWrite`, `TestResolveCacheRegime_LadderOrder`, `TestClassifyTTL_UnknownRegimeIsNotColdAt400s`, `TestTTLAnchorIsNeverLaterThanStop` and `TestResolveDelta_NilMeansMeasureNotZero`, and carrying over `formulas_test.go`'s four cases under their existing names (`TestYoungDaly_Formula`, `TestSkiRental_ComputedNotLiteral`, `TestSkiRental_ThresholdTracksConfig` here; `TestPSelectionAvailable_DefaultsFalse` with `gate.go` in commit 3).
- [ ] Run `go test ./internal/scheduler/` — **must fail** on undefined symbols.
- [ ] Implement the five files; `git rm internal/scheduler/formulas.go internal/scheduler/formulas_test.go` in the same change.
- [ ] `grep -rn "func YoungDaly\|func SkiRentalShouldWrite" internal/scheduler/` returns exactly one line each — the duplicate-declaration check this commit exists to pass.
- [ ] `go test ./internal/scheduler/ -race` — green.
- [ ] `go run ./tools/devtool lint` — the `nomagic` pass must be clean; verify `12.5`, `0.55`, `20000`, `300` and `0.1`/`1.25` appear only in `_test.go`, and that all four host constants (`HostAutoCompactBuffer`, `HostMaxOutputCap`, `HostDefaultContextWindow`, `HostDefaultMaxOutput`) carry their `//nomagic:allow` annotations.
- [ ] Footer: `Refs: SP-12, G1.2, G1.3, G1.4, G5.2, §2.2, §2.5, §5.4, §6.7, §8.4, §8.7`

---

### Commit 3 — `feat(scheduler): implement the composite trigger and cache-aware p-selection`

Makes `Evaluate` real: the four-clause trigger gated on the soft floor, candidate filtering to changepoint ∩ API-round boundaries, the `reclaimable·r − rewrite − λ·coupling` argmax with the TTL-aware tie-break, the O3 background plan, the full `Breakdown`, and the `PSelectionAvailable` gate that keeps SP-15's submodular selection inert until a real runtime exists.

**Files added:** `internal/scheduler/pselect.go`, `internal/scheduler/gate.go`, `internal/scheduler/evaluate_test.go`, `internal/scheduler/pselect_test.go`, `internal/scheduler/gate_test.go`, `testdata/golden/scheduler/decision-{warm,expiring,cold}.json`.
**Files modified:** `internal/scheduler/evaluate.go` (SP-01's zero-`Decision` stub body replaced in place — the file already exists), `internal/scheduler/types.go` (the two additive `Inputs` fields; **no constants** — all five `TriggerReason`, four `TTLState` and six `BackgroundTask` values already ship, and re-declaring them as `Reason*`/`Task*` would be eleven duplicate constants), `internal/scheduler/schedulertest/suite.go` (flip the skips), `internal/scheduler/schedulertest/behaviour.go` (the three fixture edits specified in the test plan).

- [ ] Write all 28 `evaluate_test.go` cases, the 8 `pselect_test.go` cases and the 3 gate tests first; generate the three golden `Decision` documents by hand from the worked arithmetic in this plan (the `baseInputs()` warm/cold score tables), not by capturing implementation output.
- [ ] Run `go test ./internal/scheduler/ -run TestEvaluate` — **must fail** (stub returns a zero `Decision`).
- [ ] Add the two `Inputs` fields with the ADR-referenced comment block. Use the shipped `Trigger*`/`Background*` identifiers throughout; add no constant.
- [ ] Implement `pselect.go`, replace `evaluate.go`'s stub body, then `gate.go` (which takes `PSelectionAvailable` over from the now-deleted `formulas.go` and retypes the flag to `atomic.Bool`).
- [ ] Flip every `t.Skip` in `internal/scheduler/schedulertest/`; `grep -R "t.Skip" internal/scheduler/schedulertest/` must return nothing.
- [ ] Apply the three `behaviour.go` fixture edits **in this commit**, because flipping the skip is what makes that grader run: a candidate in `baseInputs()`, `LastCompactionTS` seeded at `Now`, and `assumedMTBFSeconds` subtracting `HostAutoCompactBuffer` with `young_daly_fires` retargeted onto `LastCompactionTS`. Then `go test ./internal/scheduler/schedulertest/` — all six truth-table cases and `runYoungDalyFormulaCase` green.
- [ ] `go test ./internal/scheduler/... -race -count=2` — green.
- [ ] `go test ./internal/scheduler/ -run TestEvaluate_ScoreArithmeticExact -v` — confirm `−1629.3` exactly.
- [ ] `go run ./tools/devtool lint vet` — including the import-graph check proving `scheduler` still imports foundation packages only.
- [ ] Footer: `Refs: SP-12, G1.1, G5.1, G5.2, G7.1, G7.6, G8.2, §5.3, §5.4, §8.4`

---

### Commit 4 — `feat(daemon): classify droppable blocks and assemble p-selection candidates`

Adds the wave-3 half of `reclaimable(p)`: the `store.ToolUseRecord` adapter onto commit 2's ranking, an O(log N) suffix-sum index that makes §5.4's monotonicity structural, the `observer.Signals` → `scheduler.Features` translation that keeps `observer` free of a `scheduler` import, and candidate assembly with a memoized turn→`Pos` map and live, uncached `CrossingEdges` — coupling is deliberately recomputed per candidate, two binary searches at ~0.27 µs, because `dag`'s only invalidation probe costs far more than it saves and would serve stale values after a `Pos`-moving upsert.

**Files added:** `internal/daemon/scheduler_droppable.go`, `internal/daemon/scheduler_features.go`, `internal/daemon/scheduler_candidates.go`, `internal/daemon/scheduler_testhelpers_test.go`, and the three `_test.go` peers (5 + 7 + 10 cases).

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
**Files modified:** `internal/cli/daemon.go` (blocks 1 and 2 of the `runDaemon` wiring — construct and tap; block 3, idle registration, lands with commit 6 because that is where the tasks exist). `cmd/qompack/main.go` is not touched: it has no daemon subcommand.

- [ ] Write all 24 `scheduler_runtime_test.go` cases, the 5 `scheduler_state_test.go` cases and the 10 `scheduler_tap_test.go` cases first. Transcribe the five decorated seam signatures from `internal/daemon/options.go:122-130` — three return `error` only, and a wrapper written against `(hookio.Output, error)` will not compile.
- [ ] Run `go test ./internal/daemon/ -run 'TestRuntime|TestStateCodec|TestWrapServices'` — **must fail**.
- [ ] Implement `scheduler_state.go`, then `scheduler_runtime.go`, then `scheduler_tap.go`, then add the `internal/cli/daemon.go` wiring — registering the `Bind` hook **after** SP-08's `opts.Bind` call so the tap decorates seams SP-08 has already set, and before `daemon.New(opts)`. Do not add store/graph/ledger construction there: SP-11 creates that block and SP-13 extends it, SP-12 must not open a second store, and Block 1 only reads those fields. Follow the shared-file protocol in the `internal/cli/daemon.go` section — SP-12 is not the only writer of this file.
- [ ] `go test ./internal/daemon/... -race -count=2` — green, including `TestRuntime_ConcurrentObserveEvaluatePersist`.
- [ ] `go build ./...` and `go run ./tools/devtool build-all` — the composition-root change must cross-compile for all six targets.
- [ ] `go run ./tools/devtool lint` — confirm the import-graph check still passes. Then check the shared-file protocol by eye: in `internal/cli/daemon.go`, SP-08's observer `Bind` comes first, Block 1 and Block 2 follow it, and both sit before `daemon.New(opts)`; SP-11's resident-set block and SP-13's `InstallMCPOp` are not on this branch yet — same-wave branches never branch from each other — and land above Block 1 and immediately before `daemon.New` respectively: SP-11's when this branch rebases onto the `develop` SP-11 merged into, SP-13's when SP-13 rebases afterwards. `internal/cli/daemon.go` is 126 lines today and SP-12's three blocks add about 20; if it crosses SP-12's self-imposed 150-line ceiling (a house rule, not an architecture bound — see the `internal/cli/daemon.go` section), perform the `internal/cli/scheduler_wiring.go` extraction described there in this same commit.
- [ ] Footer: `Refs: SP-12, G1.3, G8.2, §8.4, 00-ARCHITECTURE §5.13`

---

### Commit 6 — `feat(daemon): add O3 idle background work and O5 frontier advancement`

Registers the six idle tasks on SP-05's `IdleController` without editing daemon internals, and implements continuous frontier advancement: segment close on changepoint/todo-completion/passing-test, `checkpoint.Writer.Advance` over closed-and-unencoded segments with a hard DPI guard, and residual-span accounting held under `maxResidualTokens`.

**Files added:** `internal/daemon/scheduler_frontier.go`, `internal/daemon/scheduler_idle.go`, `internal/daemon/scheduler_frontier_test.go`, `internal/daemon/scheduler_idle_test.go`, `test/e2e/scheduler_idle_test.go`.
**Files modified:** `internal/cli/daemon.go` (block 3: `RegisterSchedulerIdleWork` after `daemon.New`).

- [ ] Write both test files first (12 + 11 cases), including `TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes`, `TestFrontier_CloseWritesRealSegmentTokens`, `TestIdleTaskGatedByDecisionBackground`, `TestIdleFrontierSkippedCounted` and `TestIdleActingTaskSkippedInDegradedPassive`.
- [ ] Run `go test ./internal/daemon/ -run 'TestFrontier|TestIdle'` — **must fail**.
- [ ] Implement `scheduler_frontier.go` then `scheduler_idle.go`; wire `RegisterSchedulerIdleWork` into `internal/cli/daemon.go`'s post-`daemon.New` block. Register the acting task as **`act.advance_frontier`** at priority 110 and the other five unprefixed at 120–160 — SP-05's `RunOnce` uses that prefix to suppress scheduler-initiated checkpoints in `degraded-passive`, and 10/20/30 are already taken by `drain`/`sketches`/`metrics`.
- [ ] Confirm the `"tokens"` pseudo-feature is in `closeSegmentLocked`'s `feats` map: `grep -n '"tokens"' internal/daemon/scheduler_frontier.go` returns a line. Without it `Segment.Tokens` is zero forever and the whole trigger is dead.
- [ ] `go test ./internal/daemon/... -race` — green.
- [ ] `go test ./test/e2e/ -run TestDaemonIdleRunsSchedulerWork` — the daemon starts, a hook payload flows through the tap, the session goes idle, `act.advance_frontier` runs against the SP-01 `checkpoint.Writer` stub, and no `Loud` is logged beyond the expected `sched.frontier.no_writer` counter.
- [ ] Footer: `Refs: SP-12, G1.5, G7.1, §8.2, §8.4, §8.5`

---

### Commit 7 — `test(scheduler): add Phase 4 replay harness, benchmarks and the L3 ADR`

Closes Phase 4 with the exit-criterion harness, the eight micro-benchmarks and their budgets, the hot-path guard test, and the ADR recording the eleven decisions this slice made that a reader would otherwise have to reverse-engineer.

**Files added:** `test/replay/l3policy/policy.go`, `test/replay/l3policy/policy_test.go`, `test/replay/phase4_test.go`, `internal/scheduler/bench_test.go`, `internal/daemon/scheduler_bench_test.go`, `docs/adr/0012-scheduler-l3.md`.
**Files modified:** `testdata/bench-baseline.txt` (append all **eight** new benchmark baselines — `BenchmarkEvaluate_64Candidates`, `BenchmarkBOCDObserve_4Features`, `BenchmarkBOCDMarshal`, `BenchmarkFeaturesFrom`, `BenchmarkAssembleCandidates_2000ToolUses`, `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates`, `BenchmarkReclaimableIndexBuild_5000Blocks`, `BenchmarkSchedulerTap_ObserveTool`; miss one and it ships with nothing for `benchstat` to compare against).

- [ ] Write `phase4_test.go` (7 cases) and `l3policy/policy_test.go` (5 cases) first against the 24-session synthetic corpus; run them — **must fail** because `l3policy` does not exist. Both files read their latency coefficients from `eval.DefaultLatencyModel()`; neither declares a pause constant of its own.
- [ ] Implement `l3policy/policy.go`; iterate until all seven Phase 4 assertions and all five policy tests pass. It imports `internal/scheduler` and must **not** import `internal/daemon` — `TestPolicy_DoesNotImportDaemon` enforces it.
- [ ] Add the 8 benchmarks and `TestSchedulerNotOnHotPath`.
- [ ] `go run ./tools/devtool bench` then `benchstat` against `develop` — record the baselines; no benchmark may exceed its stated budget.
- [ ] `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop` — the `replay-gate` must be green with no `sign-off:` trailer required.
- [ ] Write `docs/adr/0012-scheduler-l3.md` covering all eleven decisions: (1) the two additive `Inputs` fields and why they need no §0 amendment, (2) the δ precedence order and why config-non-nil wins, (3) the linear cache-factor ramp across the expiring band rather than a cliff, (4) the cap-at-32 candidate rule and why the highest-`Pos` boundaries are kept, (5) the §2.5 window-resolution ladder and why no Appendix C key was added for it, (6) why `DropClass` lives in `scheduler` and `ClassifyDrop` is a `daemon` adapter (the `test/replay` import rule of §3.2), (7) why L0 events reach L3 through `Options.Bind` seam decoration rather than a new op route or an edit to SP-05/SP-08, (8) the deletion of `formulas.go` and the three declared behaviour changes that came with redistributing its symbols — `YoungDaly`'s NaN/Inf guard, `SkiRentalShouldWrite`'s `w <= 0` guard, and `pSelectionAvailable`'s retype from `bool` to `atomic.Bool` — each with the test that forced it, (9) the three `schedulertest/behaviour.go` fixture edits and why correcting `assumedMTBFSeconds` (rather than the truth table) is the change the grader's own note authorises, (10) why coupling is computed live and only the turn→`Pos` map is cached, with the `Stats()` cost and the stale-`Pos` correctness argument, (11) why the idle priority band is 110–160 and what `sched.frontier.skipped` distinguishes that `sched.residual_over_budget` cannot.
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

This subplan is **heavy**: two packages, twelve new source files, two deletions, 181 enumerated tests and 8 benchmarks, one numerical algorithm with a serialization format, and a replay harness. Partition it across parallel subagents in four dependency-ordered rounds. **The commit plan stays strictly sequential in the main session** — subagents produce file contents and test results, they never commit, never touch git, and never open a branch.

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
| **A2 — scalars** | `internal/scheduler/thresholds.go`, `cacheregime.go`, `ttl.go`, `youngdaly.go`, `skirental.go`, `dropclass.go` + tests; the deletion of `formulas.go`/`formulas_test.go`; the three `types.go` godoc corrections | Full file contents; the worked-example table (180 000 / 99 000 / 147 000 / 268.328 / the cache-factor ramp) confirmed by test output; the four `formulas_test.go` cases shown passing under their new homes. **A2 must be told that `YoungDaly`, `SkiRentalShouldWrite` and `PSelectionAvailable` already exist in `formulas.go`** — its job is to move them, with the two declared guard changes, not to declare them again |
| **A3 — droppable + features** | `internal/daemon/scheduler_droppable.go`, `scheduler_features.go` + tests + the fake `store`/`dag`/`SegmentLog` helpers | Full file contents; the rapid monotonicity property's pass output; `BenchmarkFeaturesFrom` and `BenchmarkReclaimableIndexBuild` numbers. **Depends on A2's `scheduler.DropClassOf`** — hand A3 that four-line signature up front; it must not redefine the tool table |

A1, A2 and A3 share no file. A3's fake helpers are the only artifact later rounds reuse, so A3 must return them as a standalone file with no dependency on A1 or A2.

**Integration point 1.** Main session lands A1 as commit 1 and A2 as commit 2, running `devtool lint vet` between them. A3's output is held for commit 4.

### Round B — two subagents in parallel

| Subagent | Owns | Returns |
|---|---|---|
| **B1 — Evaluate + p-selection** | `internal/scheduler/evaluate.go` (stub body replaced), `pselect.go`, `gate.go` + tests + the three golden `Decision` JSON files, and the three `schedulertest/behaviour.go` fixture edits | Full file contents; the 28-case result table; the exact `PScore` for the arithmetic test; `BenchmarkEvaluate` µs/op and allocs/op; the `schedulertest` truth table green with the skip flipped |
| **B2 — candidates** | `internal/daemon/scheduler_candidates.go` + tests | Full file contents; the `NodesAfter` call-count evidence for the turn→`Pos` cache tests and the `CrossingEdges` call count showing coupling is recomputed per call; `BenchmarkAssembleCandidates` cold/warm numbers |

B1 depends on A2's symbols and on the main session having already added the two `Inputs` fields. B2 depends on A3's `reclaimableIndex` and the fakes. Neither depends on the other. Hand B1 the golden-document arithmetic from this plan explicitly so it derives the goldens from the spec rather than from its own output.

**Integration point 2.** Main session lands commit 3 (B1 + the schedulertest skip flip) then commit 4 (A3 + B2), running the full `./internal/...` test suite with `-race` between them.

### Round C — two subagents, sequential with a narrow overlap

| Subagent | Owns | Returns |
|---|---|---|
| **C1 — Runtime + state + tap** | `internal/daemon/scheduler_runtime.go`, `scheduler_state.go`, `scheduler_tap.go` + tests, and the exact `internal/cli/daemon.go` diff (blocks 1 and 2) | Full file contents; the `internal/cli/daemon.go` diff as a patch, not a rewritten file; the 24 + 5 + 10 case result tables; the two JSON state documents produced by a real round-trip. Hand C1 the five decorated seam signatures from `internal/daemon/options.go:122-130` verbatim — three return `error` only |
| **C2 — frontier + idle** | `internal/daemon/scheduler_frontier.go`, `scheduler_idle.go` + tests, `test/e2e/scheduler_idle_test.go`, and the `internal/cli/daemon.go` block-3 diff | Full file contents; the nine registered task names in run order with priorities (SP-05's three at 10/20/30, then SP-12's six at 110–160 with `act.advance_frontier` first); the DPI-guard test transcript showing the batch-drop-and-retry path; the `feats["tokens"]` value a real `SegmentLog.Close` recorded |

C2 needs `schedRuntime`'s field set and method receivers from C1, so give C2 C1's struct definition and method signatures **before** it starts — that is a 40-line contract, not the whole file, and it keeps the two agents from redefining the same type. Run C1 to completion first if the struct is not yet settled; otherwise run them in parallel with that contract fixed.

**Integration point 3.** Main session lands commit 5 (C1) then commit 6 (C2), running `devtool build-all` after commit 5 because it is the one that touches the composition root.

### Round D — one subagent

| Subagent | Owns | Returns |
|---|---|---|
| **D1 — Phase 4 + benchmarks + ADR** | `test/replay/l3policy/`, `test/replay/phase4_test.go`, both `bench_test.go` files, `docs/adr/0012-scheduler-l3.md` | Full file contents; the seven Phase 4 assertion outcomes with the actual measured numbers (rewrite ratio, five divergence deltas, residual slope, P95 residual, the residual↔rewrite identity); the eight benchmark results against their budgets. D1 declares no latency constant: both test files read `eval.DefaultLatencyModel()` |

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
- [ ] The residual↔rewrite identity is asserted (`TestPhase4_ResidualSpanIsRewriteSpan`), and the PR body states plainly that the three rows above are one measurement of cut placement (`n − p`), not three independent gates — the O5 amortization claim is discharged by V4-VERIFY §4.4's live `TestV4_FrontierAdvancementKeepsResidualSpanODelta` and its `advanceOnSegmentClose = false` negative control.
- [ ] Compaction pause is derived linearly from residual span using the harness's own coefficients, read from `eval.DefaultLatencyModel()` — shipped values `3000 ms + 0.15 ms/token` — recorded in `.qompack/eval/phase4-pause.json` with `"pause_modelled": true` and the coefficients taken from that same model, and never presented as a measured wall-clock in deterministic replay.

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
- [ ] **Spec coverage self-review against the Design context section.** Walk each quoted block and point at the code that implements it: the four-clause trigger (`evaluate.go`); `soft_floor` at 55% and `hard_ceiling` one turn below the host threshold (`thresholds.go`); `candidates = changepoint ∩ API-round` (`pselect.go` `eligible` + `scheduler_candidates.go`); `reclaimable(p)` (`scheduler_droppable.go`); `rewrite(p) = w·(n−p)`, zero when cold (`pselect.go` + `ttl.go`); `distortion(p) = λ·coupling` (`pselect.go` via `dag.CrossingEdges`); `argmax` (`chooseP`); the sliding-TTL correction keyed on last API call (`ttl.go`, `Runtime.NotifyActivity`); the cache-regime ladder and the request-start anchor (`cacheregime.go`), which are what make `§5.1`'s *"verify against current pricing before tuning"* an executed instruction rather than a standing one; `§5.4`'s *"scheduled against cache state"* applied to the **expiring** band as well as the cold one (`TriggerCacheExpiring` in `evaluate.go`); `√(2·δ·M)` with measured δ (`youngdaly.go`, `RecordCompactionCost`); BOCD over paths/tools/time/todos with pruning (`bocd.go`); O3's four named activities (`scheduler_idle.go`); O5 segment close and frontier advance (`scheduler_frontier.go`); the §2.2 compactable tool set and the §8.7 ephemeral-first ranking (`scheduler.DropClassOf` + `daemon.ClassifyDrop`); §2.5's `effectiveContextWindow` arithmetic and the window-resolution ladder (`thresholds.go`, `BindSession`); §2.6's API-round boundary (`NoteAPIRound`, fed by the `ObserveStop` wrapper); the §8.2 encoded-once DPI guard (`advanceFrontier`'s `ErrAlreadyEncoded` path); Phase 4's exit criterion (`phase4_test.go`).
- [ ] **Event path proven end to end.** `TestWrapServices_*` plus `test/e2e/scheduler_idle_test.go` show a real hook payload reaching `Observe`, a `Stop` recording a round boundary, and an idle tick running the six tasks — the scheduler is wired, not merely written.
- [ ] `act.advance_frontier` is the only prefixed idle task, and `TestIdleActingTaskSkippedInDegradedPassive` proves it is suppressed in `degraded-passive` while the other five keep running.
- [ ] **Placeholder scan.** `grep -RniE 'TODO|FIXME|TBD|XXX|unimplemented|not implemented|handle .* appropriately' internal/scheduler internal/daemon/scheduler_* test/replay/l3policy docs/adr/0012-scheduler-l3.md` returns nothing. No function returns `core.ErrNotImplemented` in any SP-12-owned file.
- [ ] **Type consistency with the Interface contract.** Every signature in the Produces block compiles exactly as written: `Evaluate(Inputs) Decision`, `NewBOCD(float64, []string) Detector`, `YoungDaly(float64, float64) float64`, `SkiRentalShouldWrite(float64, float64, float64) bool`, `PSelectionAvailable() bool`, `EffectiveWindow`/`SoftFloor`/`HardCeiling`/`ClassifyTTL`/`CacheFactor`, `DropClassOf(string, bool, bool) DropClass`, `ClassifyDrop(store.ToolUseRecord) DropClass`, `NewSchedulerRuntime(SchedulerRuntimeOptions) (scheduler.Runtime, error)`, `RegisterSchedulerIdleWork(Daemon, scheduler.Runtime, SchedulerRuntimeOptions) error`, `WrapServicesForScheduler(*Services, scheduler.Runtime, SchedulerRuntimeOptions)`, `CloseSchedulerRuntime(scheduler.Runtime) error`, `PrecomputedSlice(scheduler.Runtime) (dag.Slice, bool)`, `FeaturesFrom(*FeatureHistory, observer.Signals, string, core.UnixMilli) scheduler.Features`. `TriggerReason` string values match §5.13 exactly: `soft_floor`, `changepoint`, `young_daly`, `hard_ceiling`, `idle_cold_cache`, **under SP-01's shipped identifiers `TriggerSoftFloor`…`TriggerIdleColdCache`**. `BackgroundTask` values match: `advance_frontier`, `gc`, `precompute_slice`, `refresh_delta`, `rebuild_bloom`, `compact_dag`, **under `BackgroundAdvanceFrontier`…`BackgroundCompactDAG`**. `TTLState` values match: `warm`, `expiring`, `cold`, `unknown`. `grep -rnE "\b(Reason|Task)[A-Z][A-Za-z]*[[:space:]]+(TriggerReason|BackgroundTask|TTLState)\b" internal/ test/` returns nothing: no `Reason*`/`Task*` alias is introduced anywhere. The gate is written against the **type**, not against a list of names, so a later subplan minting a brand-new `ReasonSomethingElse TriggerReason = "…"` trips it too — a two-name alternation (`ReasonSoftFloor\|TaskAdvanceFrontier`) only catches re-declarations of the eleven values that already ship and lets every new mint through. Anchoring on the type also keeps the gate from firing on unrelated identifiers that merely start with `Reason` (`negknow`'s `ReasonHash`, the `contract` monitor test names), which a bare `Reason[A-Z]` prefix grep would flag.
- [ ] No method added to another subplan's interface (Rule W-3); the `checkpoint.Writer`, `store.SegmentLog`, `dag.Graph`, `negknow.Ledger` and `daemon.IdleController` surfaces are used exactly as §5 declares them.
- [ ] Rule W-2 honoured: the `checkpoint.Writer` call sites are exercised against `testdata/golden/contracts/checkpoint/` fixtures and re-run against SP-10's real implementation at the V4 verification checkpoint.
- [ ] All 181 enumerated tests exist by name and are green; all 8 benchmarks report within their budgets; all four property tests (`SoftFloorBelowHardCeiling`, `CacheFactor_MonotoneDecreasing`, `reclaimable` monotonicity, BOCD posterior normalization) plus `TestBOCD_MarshalRoundTrip_Property` pass under `rapid`.
- [ ] `state/bocd.json` and `state/scheduler.json` round-trip, and both self-heal loudly from corruption without losing store data.
- [ ] Six idle tasks registered at 110–160 with the stated names, coexisting with SP-05's `drain`/`sketches`/`metrics` at 10/20/30 for nine in total; each inert until an `Evaluate` has placed it in `Decision.Background`, and `refreshDecision` is what produces that `Evaluate` on the idle path.
- [ ] The three-block `internal/cli/daemon.go` addition is the only modification outside SP-12-owned files, apart from the four in-package files SP-12 is explicitly assigned in the file map (`internal/scheduler/types.go`, `detector.go`, `evaluate.go`, `schedulertest/behaviour.go`) and the deletion of `formulas.go`/`formulas_test.go`. `internal/daemon`'s pre-existing SP-05 files are unmodified (`git diff develop..HEAD --stat internal/daemon/` lists only `scheduler_*.go`), `cmd/qompack/main.go` is untouched, and `internal/observer` is untouched. The tap reaches L0 through SP-05's `Options.Bind` seam precisely so this stays true. This bullet bounds **SP-12's own diff**; it is not a claim that SP-12 is the only subplan writing `internal/cli/daemon.go`. SP-08, SP-11 and SP-13 write into the same window, and the fixed order — SP-08's `Bind`, SP-11's resident-set block, SP-12's Blocks 1–2, SP-13's extension and `InstallMCPOp`, `daemon.New`, SP-12's Block 3 — is stated in the `internal/cli/daemon.go` section and must be checked there rather than asserted away here.
- [ ] Commit count verified: exactly **7** (within the mandated 5–8).
- [ ] No `Co-Authored-By`, `Signed-off-by`, `Generated with` or `🤖` in any commit message, merge message, tag or PR body.
- [ ] `docs/adr/0012-scheduler-l3.md` written and covers all eleven recorded decisions.
- [ ] CI green on the branch across all nine jobs; PR body carries the Phase 4 numbers.

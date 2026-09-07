# 12. Scheduler L3: the composite trigger, BOCD, cache regimes and p-selection

Date: 2026-09-06

## Status

Accepted. SP-12 ships as seven commits on `feat/sp12-scheduler-l3` (commit 1 BOCD; commit 2
thresholds / TTL model / Young–Daly / ski rental / drop classes; commit 3 the composite trigger and
cache-aware p-selection with the `schedulertest` suite flipped and three goldens frozen; commit 4
droppable classification and candidate assembly; commit 5 the `scheduler.Runtime` with persisted
BOCD and burn state; commit 6 O3 idle work and O5 frontier advancement — commits are named by
ordinal because hashes change on rebase). Commit 7 — the Phase 4 replay harness, the eight benchmarks, the hot-path guard and
this ADR — is the one this document ships in. The benchmark numbers below are the quiet-machine
run of 2026-09-06 whose raw output is appended to `testdata/bench-baseline.txt` in this commit.

## Context

Qompack.md §8.4 gives layer L3 the decision of *when* to compact and *where* to cut:

> should_compact = tokens > soft_floor AND (at_changepoint OR elapsed > young_daly_interval OR
> tokens > hard_ceiling OR idle_gap > ttl_max OR (regime_known AND idle_gap > 0.8·ttl) OR
> effort_changed)

with `p` chosen by `score(p) = reclaimable(p)·r − rewrite(p) − distortion(p)` over
`candidates = changepoint boundaries ∩ API-round boundaries`, and §5.4's bimodality — edit as
late as possible while the prompt cache is warm, cut deep once it is provably cold. SP-01 shipped
the seam (`Evaluate`, `Detector`, `Runtime`, the wire constants) as stubs; SP-12 makes every
line of it real. 00-ARCHITECTURE.md §5.13 fixes `Evaluate` as a pure function of `Inputs`, and
§3.2 confines `internal/scheduler` to the foundation packages, which is what makes the whole of
§8.4 unit-testable, replayable, and — through `test/replay/l3policy` — measurable by SP-02's
harness without a daemon.

This ADR records the eleven design decisions the plan asked to be recorded, every controller
ruling that changed the plan's shape while the branch was executed, the runtime's own notes, the
replay policy's modelling conventions, the Phase 4 measurement, and the eight benchmarks.

## The eleven decisions

1. **Two additive `Inputs` fields, no §0 amendment.** `LastCompactionTS` and `CouplingLambda`
   (and, by rulings R7/R9/R13, six more: `Regime`, `LastRequestStartTS`, `EffortChanged`,
   `ExpiringTriggerFraction`, `AssumeMaxTTLSeconds`, `HostTriggerAbsent`) are new members of a
   struct `internal/scheduler` owns. Nothing outside SP-12 constructs `Inputs`; every field's zero
   value reproduces the pre-SP-12 behaviour (0 disables the Young–Daly clause, a zero `Regime`
   falls back to the unknown rung, and so on). Appending to an owned struct is inside §5's
   latitude; renaming, retyping or removing would not have been.
2. **δ precedence: config-non-nil wins.** `resolveDelta` reads `Cfg.YoungDaly.MeasuredDeltaSeconds`
   first and `Inputs.MeasuredDeltaSeconds` second. A configured δ is an operator statement
   ("my compactions cost this much") that a runtime EWMA must not silently overrule; nil in both
   means *measure*, never zero (`TestResolveDelta_ConfigOverridesRuntime`,
   `TestResolveDelta_NilMeansMeasureNotZero`, and `TestEvaluate_YoungDaly_UnmeasuredDeltaDisablesClause`
   for the Evaluate half, ruling R25).
3. **A linear cache-factor ramp across the expiring band, not a cliff.** `CacheFactor` is 1 while
   warm, 0 once cold, and `(TTLMax − gap) / (TTLMax − 0.5·TTLMin)` clamped to [0, 1] in between
   (ruling R2). A cliff at the expiry edge would make `score(p)` discontinuous in the one input
   that is measured with the least precision — an idle gap read off hook timestamps — and would
   let a one-second difference flip the cut from the latest boundary to the deepest one.
4. **Cap at 32 candidates, keep the highest-`Pos` boundaries — after the round-boundary filter.**
   §5.4: "Twenty candidates, not 167,000." `Evaluate` first keeps the API-round boundaries
   (relaxing to the full set only when none is one), then sorts by `Pos` and keeps the last
   `maxScoredCandidates` (`TestEvaluate_CandidatesCappedAt32`,
   `TestEvaluate_CapAppliesAfterRoundBoundaryFilter`, ruling R44); the daemon's assembler applies
   the same cap before handing candidates over. A very early boundary can only win when the
   cache is cold, and in that regime `rewrite = 0` makes even the 32nd-latest boundary a deep cut
   relative to `n`, so dropping the oldest boundaries loses no resolution where the decision is
   actually close. Filtering first keeps a cap slot from being spent on a boundary the filter
   would then discard — with the cap first, 32 off-boundary candidates could discard every
   eligible one and "relax" onto ineligible ones.
5. **The §2.5 window-resolution ladder has no Appendix C key.** `EffectiveWindow`,
   `HostAutoCompactBuffer` (13 000), `HostMaxOutputCap` (20 000), `HostDefaultContextWindow` and
   `HostDefaultMaxOutput` describe the *host*, not Qompack tunables: they are Claude Code's own
   trigger arithmetic, and a key for them would invite an operator to "configure" a number the
   host does not read. They are the four host constants carrying `//nomagic:allow`, the only such
   allowances in this slice beside `maxShingles` and `maxTurnHistory`. The runtime's ladder
   clamps an out-of-range `CLAUDE_CODE_AUTO_COMPACT_WINDOW` / `CLAUDE_CODE_MAX_CONTEXT_TOKENS`
   to [100 000, 1 000 000] rather than rejecting it (ruling R53): only an unparsable, empty or
   non-positive value falls through to the next rung, and `window_source` still names the rung.
6. **`DropClass` lives in `scheduler`; `ClassifyDrop` is a `daemon` adapter.** The §2.2 tool
   table is pure spec needing no import beyond `strings`, and `test/replay/l3policy` must score
   candidates without importing `internal/daemon`, a composition root nothing may import (§3.2).
   `daemon.ClassifyDrop` adapts `store.ToolUseRecord` onto `DropClassOf`, so the daemon and the
   replay policy classify identically by construction rather than by a parity test. The
   runtime's droppable blocks come from the DAG's `KindToolResult` nodes joined to
   `store.ToolUse`, cached on `maxTurn` (ruling R51).
7. **L0 events reach L3 through `Options.Bind` seam decoration.** `WrapServicesForScheduler`
   wraps the observer services the daemon already composes, so the scheduler tap sees every
   tool observation and every `Stop` without a new op route, without an edit to SP-05's IPC or
   SP-08's observer, and on the worker pool (budget B-C), never on the hook path (budget B-A).
   The wiring order is fixed — `WireObserver` first, `wireScheduler` second — because a runtime
   installed on `opts.Sched` before the observer is wired would be fed twice (C1's note).
8. **`formulas.go` deleted; three declared behaviour changes.** SP-01's `YoungDaly`,
   `SkiRentalShouldWrite`, `PSelectionAvailable` and its flag were redistributed to
   `youngdaly.go`, `skirental.go` and `gate.go` in the same commit, and each gained the guard its
   new test forced: `YoungDaly` returns 0 on NaN/±Inf inputs (`TestYoungDaly_NonPositive`);
   `SkiRentalShouldWrite` refuses `w <= 0` rather than dividing by it
   (`TestSkiRentalShouldWrite`); `pSelectionAvailable` became an `atomic.Bool` because SP-12
   adds two writers — `EnablePSelection` on runtime construction and again on every
   `BindSession` (a per-project daemon binds many sessions; a `--resume` of the same session
   after `Close` must reopen the gate, ruling R51 and the commit-5 review), `DisablePSelection`
   on shutdown and in every test's defer — while `analyzer.NewSelector` reads it from arbitrary
   goroutines (`TestPSelectionGate_ConcurrentAccess`, hardened by ruling R39).
9. **Three `schedulertest/behaviour.go` fixture edits, and a fourth.** The shipped grader's own
   note (behaviour.go:20–26) says its fixture predates a placeable candidate and a Young–Daly
   baseline; the plan gives `baseInputs()` both, and corrects `assumedMTBFSeconds` to subtract
   `HostAutoCompactBuffer` from the headroom — the fixture, not the truth table, was wrong about
   where the hard ceiling sits, so correcting the fixture is the change the note authorises.
   Ruling R5 added a fourth edit: `baseInputs()` pins the KNOWN five-minute regime built from the
   file's own cfg constants, because the `idle_cold_cache_fires` row (gap 400 s, TTL 250 s) is
   only cold under a known regime — the unknown rung reaches to 3 600 s.
10. **Coupling is computed live; only the turn→`Pos` map is cached.** `dag.CrossingEdges(pos)`
    is two binary searches over presorted slices (0.27 µs warm, ADR 0007), so 32 candidates cost
    ~9 µs; the only invalidation probe `dag.Graph` offers is `Stats()`, which walks every node
    once and every edge three times and allocates two maps per call — roughly 50 000 map
    operations on the 5 000-node graph this slice benchmarks against, to avoid 9 µs. Worse, a
    `Stats()`-keyed cache would be wrong: a non-anchor `AddNode` upsert that moves a node's `Pos`
    changes no count, so stale coupling would be served indefinitely. The turn→`Pos` map is
    keyed on the Runtime's own `maxTurn` instead: a moved `Pos` is at most one turn stale and
    never stale across a compaction, because a compaction always follows at least one new turn.
    The benchmark below shows the difference: 2.7 ms cold against 2.8 µs warm.
11. **The idle priority band is 110–160; `sched.frontier.skipped` says why not.** SP-12's six
    O3/O5 tasks register after every pre-existing idle task (`observer.persist` at 50 among them,
    ruling R21) at priorities `110 + n×10`, in the order §8.4 names: advance_frontier first
    because it is the latency lever, then precompute_slice, refresh_delta, rebuild_bloom,
    compact_dag, gc. `sched.residual_over_budget` counts the residual exceeding
    `maxResidualTokens`; `sched.frontier.skipped` counts an advance that was *possible but
    declined* — no closed segment, the gate off, or the writer refusing — which the budget
    counter cannot distinguish from a frontier that simply had nothing to do.

## Runtime notes (commits 5–6)

- `IdleSince` survives a daemon restart: `lastActivity` is restored from the persisted API-call
  anchor, so a restarted daemon does not see a fresh session as a fresh idle gap (C1, commit-5 fix
  round). A restore is never a burn sample, and `contextTokens` is persisted with the closed
  segments included so the first post-restore Evaluate does not jump.
- `refreshDecision` recomputes the residual before every Evaluate (a plan gap: the plan read a
  residual the frontier had already moved), and the starvation count lags one refresh — the
  counter reports the state the previous refresh saw, which is one idle tick behind and stated
  as such (C2).
- DPI offenders are identified through `Get(id).EncodedOnce` on the checkpoint writer; SP-10 is
  asked to expose the ids directly (C2).
- Shutdown persists scheduler state through `closeScheduler` → `CloseSchedulerRuntime`, deferred
  in `runDaemon` right after registration (ruling R52): without it a daemon stopped between two
  idle persists lost everything since the last one. That persist runs after `d.Run` returns,
  i.e. after `Stop` has released `daemon.lock`; it stages in `.qompack/tmp/` and renames into
  `state/`, which a "gone" watcher could observe as a post-lock write. Accepted for SP-12
  (ruling R55); its proper home is a shutdown seam inside SP-05's `Stop`, recorded for
  V4-VERIFY.
- Subagent `Stop` counts as activity only; the tap records a round boundary for the parent's
  `Stop` alone (C1).
- `refresh_delta` deviates from the plan's body. The plan's task table says "pull
  `obs.Hist("checkpoint_finalize").Snapshot().P50` and fold it into `deltaEWMA`; recompute the
  burn EWMA; `Persist`". Shipped: `refreshDeltaTask` seeds `deltaEWMA` from that P50 **once**,
  only while `deltaSamples == 0` (the task is planned exactly while δ is unmeasured, so the
  fold can never double-count a direct `RecordCompactionCost`), and recomputes the
  closed-plus-open context count the burn-rate sample reads rather than the burn EWMA itself
  (a burn sample needs two timestamps, which a recompute does not have). A later, better P50 is
  therefore never re-folded — after the seed only `RecordCompactionCost` moves δ.
- **Handoff to SP-10 (checkpoint writer).** Two hazards for the real writer to keep in mind,
  neither a defect of this branch: (1) the frontier's retry logic assumes `Advance` is atomic —
  nothing marked on `ErrAlreadyEncoded`, as `store.MarkEncoded` is; if SP-10's `Advance` marks
  a prefix before refusing, the empty-remainder branch leaves the frontier behind the encoded
  state, and any abort of a draft that already marked segments (`Close`, or a rebind) makes
  those originals permanently un-checkpointable, because DPI is one-way. (2) A `SessionEnd`
  that arrives while `ckpt.Begin` is in flight inside `ensureDraft` clears `r.draft` first;
  `ensureDraft` then stores the new draft (the session id is unchanged) and it stays open until
  the next new-id bind aborts it. Bounded — `Run` joins before shutdown and the idle controller
  is serial — and no data is at risk, but it is a draft the writer will see aborted late.

## Controller rulings

Recorded from `.superpowers/sdd/V4-SP-12-scheduler-l3/progress.md`; each names what changed and
why. Cost-if-wrong is stated there; every ruling below is local to SP-12-owned files unless noted.

- **R1** `ResolveCacheRegime(getenv, cfg, model, subagent, assumeMaxTTLSeconds)` (fifth parameter
  by R42): the ladder disabled > subagent_5m > force_5m > enable_1h > unknown, with truthiness
  "trimmed, lower-cased, non-empty and not one of 0/false/no/off" and the per-family
  `DISABLE_PROMPT_CACHING_<FAMILY>` variable; `HostOneHourTTLSeconds`/`HostOneHourWriteMultiplier`
  are documented API pricing, outside the forbidden literal sets.
- **R2** regime-aware `CacheFactor(state, gap, reg)` (decision 3) — the plan's
  `CacheFactor(state, gap, ttlSeconds)` made `TestEvaluate_UnknownRegimeDoesNotDeepCutAt400s`
  impossible.
- **R3** the regime form of `ClassifyTTL` wins over the plan's earlier signature.
- **R4** `scoreCandidates` takes `r`/`w` from the resolved regime, never from cfg; `Breakdown`
  carries both the cfg multipliers and the effective `regime_*_multiplier` pair.
- **R5** the fourth fixture edit (decision 9).
- **R6** `Breakdown["regime_rung"]` is numeric (unknown 1, enable_1h 2, disabled 3, force_5m 4,
  subagent_5m 5); `fired_at_ttl_fraction` is 0 when `TTLMax ≤ 0`; `Breakdown` never holds NaN/Inf
  because the state codec JSON-marshals it.
- **R7** `Inputs.HostTriggerAbsent`: with `DISABLE_COMPACT` or an unenforced window the
  hard-ceiling clause still reports, but `Urgency` is capped at Advisory and
  `Breakdown["urgency_capped_advisory"] = 1` (`TestEvaluate_HostTriggerAbsentCapsUrgency`).
- **R8** `schedulertest` keeps `RunSchedulerSuite`, loses `skipIfStub` and every `t.Skip`, gains
  `RunDetectorSuite`; `suite_test.go`'s fake is a minimal non-stub runtime.
- **R9** the config key is SP-12's: `runtime.scheduler.cache.{expiringTriggerFraction: 0.8,
  assumeMaxTTLSeconds: 3600}` (00-ARCHITECTURE §11.5), added to `internal/config` with defaults,
  validation and regenerated docs — the one ruling that touches SP-01's package, reversible.
- **R10** the `devtool cover` landed-gate edits fold into commit 3; **R36** drops
  `probeBlind["scheduler"]` and updates the cover tripwire list.
- **R11** `gate.go` lands in commit 2 with `formulas.go`'s deletion so the package never holds
  two declarations of `PSelectionAvailable`.
- **R12** the three cli blocks live in `internal/cli/scheduler_wiring.go` (`wireScheduler`,
  `registerSchedulerIdle`, `closeScheduler`) with three call lines in `runDaemon`, which had
  already crossed the plan's 150-line ceiling; SP-12 opens no ledger.
- **R13** `CacheRegime` is declared in `types.go`; **R17** the shared test fixture is written by
  the controller; **R31** commit 1 ships a reduced fixture because the full one needs commit-2
  fields.
- **R14/R15** the fan-out schedule and keeping the SDD workspace in place for V4-VERIFY.
- **R16** no `00-ARCHITECTURE.md` edit: §5.13 already lists `cache_expiring`.
- **R18** the TTL-anchor property is asserted over `resolveTTLAnchor` in `ttl_test.go` and the
  Evaluate half in `evaluate_test.go`.
- **R19** `rebuild_bloom`'s body is the `fn` from SP-09's `negknow.Maintainer.MaintenanceTask`
  when the ledger implements it, registered once under SP-12's name behind SP-12's gate.
- **R20** `eval.Score.ResidualSpan`/`CompactionPauseMS` are `Percentiles` and `RewriteTokens` is
  `int`, so the Phase 4 identity and pause-model assertions iterate `Run.ResidualSpan`,
  `Run.PauseMS`, `Run.PrefixTokens` and `Run.Keeps` per compaction point; the P95 and slope rows
  read the percentile fields.
- **R21/R22** idle-task and e2e assertions by containment and by effect, because
  `IdleController` exposes no `Registered()`.
- **R23** the real `obs` API (`Add(1)`, `Set(int64)`); `paths.WriteAtomic` takes a mode.
- **R24** the plan's precondition for `SoftFloor < HardCeiling` was arithmetically false; the
  true one is `(1 − pct)·window > 13 000 + margin + 1`.
- **R25/R26** accepted deviations in A2's files (see decision 2; unexported source constants; a
  nil getenv reads as an empty environment).
- **R27/R43** `devtool lint`'s sleepcheck failed on the branch base at
  `test/e2e/v3_x08_test.go:239`; develop has since replaced that sleep with a ticker (7f65d28),
  so the finding is discharged by the pre-PR rebase onto develop's head.
- **R28** the clean-branch baseline runs in a throwaway worktree, not the dirty SP-10 one.
- **R29/R30** `slices.SortFunc` in the crossing index; the candidate assembler gains a logger,
  metrics and `Invalidate()`, and `segs.Range` errors propagate.
- **R32** the stubs guard's scheduler row gets `pureMethods: allMethodsAreReal` in commit 1.
- **R33** under Adams–MacKay `post[0] ≡ H` after normalisation, so the plan's `post[0] > 0.5`
  can never fire; the detector reads the run-length-1 mass, and the first observation after
  `Reset` never declares.
- **R34** hard-cap overflow mass folds into the top row (the plan's re-slice collapsed the
  posterior and re-declared every 512 turns); the per-row predictive folds the feature product
  into two logarithms, pinned to the plan's formula at 1e-9, because the literal code measured
  167 µs/op against a 150 µs budget.
- **R35** the JSON-schema golden edit is what `TestJSONSchema_Golden` requires.
- **R37** "all four clauses ⇒ five reasons" is unachievable — the hard ceiling drives `M` to 0,
  which disables Young–Daly — so `TestEvaluate_ReasonsOrderStable` asserts the three maximal
  achievable combinations and the canonical order.
- **R38** `*Into` stack-buffer variants behind the plan's signatures (allocs 8 → 6).
- **R39** `TestPSelectionGate_ConcurrentAccess` read once before checking `stop`, so its
  `reads ≥ 64` cannot lose a scheduling race; **R40** `TestBOCD_PosteriorBounded` asserts the
  deterministic proxy (posterior length per batch) instead of a wall-clock ratio; **R45** the
  plan-mandated `< 10 ms` wall-clock in `scheduler_features_test.go` is a min-of-5 sample — three
  flaky-timing hardenings on a shared machine.
- **R41** the repo's commit hook caps subjects at 64 characters, not the plan's 72.
- **R42** `runtime.scheduler.cache.assumeMaxTTLSeconds` never reached `ResolveCacheRegime` (the
  unknown rung hard-wired 3 600); the function takes it as its fifth parameter and
  `TestResolveCacheRegime_UnknownRungUsesAssumedMaxTTL` /
  `TestEvaluate_AssumedMaxTTLReachesClassification` pin it end to end.
- **R44** the 32-candidate cap runs after the round-boundary filter (decision 4).
- **R46** `bocdPriorBeta` is 0.02, not the plan's 1.0; the hazard stays the Appendix C key. With
  `β0 = 1` on unit-interval features the Normal-Inverse-Gamma run predictive keeps a scale near
  0.22 for the first hundreds of observations irrespective of the data's variance, and the product
  over independent streams lets three well-fitted streams outweigh one stream's excursion: on the
  committed corpus the plan's detector made one declaration over 95 generator changepoints, and
  its own step test passed only because the fixture steps all four streams by 0.6 at once. At
  `β0 = 0.02` it declares 26 times with 24 within three turns of a true boundary (measured in a
  scratch copy with the prior scale as the only change; the plan's window features are not the
  problem — a current-turn-versus-prior-window variant detects no better). Re-measured in the
  final fix round by running the policy's own detector over the committed corpus: 26
  declarations / 24 hits at the Appendix C hazard and 47 / 42 at 0.02, both reproduced exactly,
  against the **105** generator changepoints the corpus's `Meta` lists — the 95 above is D1's
  denominator as written; the numerators are what the ratio rests on. All of it is measured at
  the replay cadence (one observation per tool-bearing turn), not the daemon's — see "Phase 4
  measurement".
- **R47** the two Phase 4 residual rows are re-scoped to the quantity the policy controls:
  per compaction point `achievable_i = n_i − Pos(latest eligible candidate)` (eligible =
  changepoint ∩ round boundary; `n_i` with none) and `slack_i = ResidualSpan_i − achievable_i`.
  `TestPhase4_ResidualUnderMaxResidualTokens` asserts `slack_i = 0` at every warm point and, at
  cold points, that `P` is the deep cut `chooseP` selects — recomputed with `scheduler.Evaluate`
  over the same candidate set, never trusted from the policy;
  `TestPhase4_ResidualSpanFlatAsSessionGrows` asserts the plan's slope and quartile bars on
  median slack per session. Both record the absolute numbers (`residual_p95`, `residual_slope`)
  and their bars in `phase4-rewrite.json` with `discharged_by` naming V4-VERIFY §4.4's live
  test. `TestPhase4_ResidualSpanIsRewriteSpan` is exactly as the plan states it.
- **R48** the `tools/devtool/importrules.go` declaration of `test/replay/l3policy` and the
  `test/replay/main.go` blank import that registers `qompack-l3` for `--policies` are the
  controller's, landing in commit 7.
- **R49** the replay conventions stand: `Now` is the timestamp of the turn the compaction
  precedes; the window is `n + HostAutoCompactBuffer`; positions and weights come from
  `eval.Blocks`; the budget does not constrain the cut; the detector is not `Reset` at a logged
  compaction.
- **R50** two runtime tests that need `closeSegmentLocked` moved from commit 5 to commit 6.
- **R51** droppable blocks from the DAG's `KindToolResult` nodes joined to `store.ToolUse`,
  cached on `maxTurn`; `BindSession` re-enables the p-selection gate (decisions 6 and 8).
- **R52** shutdown persist through `closeScheduler` (runtime notes).
- **R53** the window ladder clamps rather than rejects (decision 5).
- **R54** the replay keep-set carries Qompack.md §8.5's tier-3 pointer tier (conventions below).
- **R55** the post-lock shutdown persist is accepted for SP-12; its proper home is SP-05's `Stop`
  (runtime notes).
- **R56** `devtool replay --policies stock,qompack-l3` fails the driver's keep-budget check because §2.2
  retains every `Test` result unconditionally (18 of 39 points exceed 40 000 on preserved results
  alone); R54's shape ships, the gate's default policy set never runs `qompack-l3`, and a §2.2
  revision is Qompack.md's, not SP-12's.

## The replay policy's conventions

`test/replay/l3policy` replays a logged session through `scheduler.Evaluate` with nothing but
the log. Its conventions are stated in the package comment and summarised here because they
decide what the Phase 4 numbers mean:

- every position — candidates' `Pos`, `P`, `n` — is read from `eval.Blocks`, so `P` lives in the
  coordinate the harness subtracts it from;
- reclaimable tokens use the harness's own per-result weights and a supersession derived from
  the log (a result whose every path is touched again later in the same prefix);
- coupling is the count of logged path-sharing call pairs straddling `p`;
- the detector is fed **one observation per tool-bearing turn**, with the plan's window
  features over eight-turn windows. That is *not* the daemon's cadence: the daemon observes
  once per PostToolUse record **and** once per main-agent `Stop` — an empty observation,
  plan-mandated by the tap table — so a live stream carries about twice the observations, with
  half-content windows and a `time` stream that alternates between the think gap and the
  generation gap. Every Phase 4 figure below is a replay-cadence figure; the detector's
  sensitivity under the daemon's real cadence is unmeasured on this branch and is carried to
  V4-VERIFY §4.4's live test (ruling R57; the attempted realignment and its numbers are under
  "Phase 4 measurement");
- candidates are §8.4's intersection — the detector's declared turns that are API-round
  boundaries (every non-user turn), capped to the highest-`Pos` 32 — and `Candidates` exposes
  the set so the harness checks the cut against it; with no candidate there is no cut: Qompack
  does nothing, the host's own compaction runs, and the keep-set is stock's;
- the regime is the KNOWN five-minute one (the ladder run under `FORCE_PROMPT_CACHING_5M`
  alone); `Now` is the timestamp of the turn the compaction precedes, because the host compacts
  on submit;
- a logged compaction is the host's own auto-compact, so the window is modelled as
  `n + HostAutoCompactBuffer` and `hard_ceiling` fires where the host fired — *whether* to
  compact is the log's decision, *where* to cut is `Evaluate`'s;
- the keep-set is the cached prefix before `P` at no cost, plus what Qompack.md says a
  checkpoint carries after it: every result §2.2 preserves (`DropNone`) with the file block it
  produced, every decision and elimination block (§8.5 tiers 1–2), and a §8.5 tier-3 pointer
  tier — the file blocks of the tail's most recently touched paths, most recent first, within
  the budget the harness hands every policy (ruling R54). Droppable results are dropped (that is
  what `reclaimable(p)` reclaims), message blocks after `P` are what the summary replaces, and
  `Tokens` is the retained tail — the quantity the harness prices as rehydration.

The pointer tier is the part of that model the plan's one-sentence keep-set definition ("every
logged tool-call id before P plus every non-droppable id after it") does not state, and it is
load-bearing: in the harness a re-touch of a file whose `file:` block is absent from the keep-set
is repaired with an inserted `FileRead`, which `Compare` counts as a redundant read and as the
first divergence. The plan's definition drops every read in the tail, the most recent read of a
path included, so it hands the harness exactly the eager restore §8.5 says Qompack replaces with
pointers ("no code snippets; files are pointers with a one-line reason") and then charges the
missing restore as divergence. Measured on the corpus with everything else equal, the literal
definition regresses first_divergence_turn 11 → 3 and redundant_reads 43 → 76; with the pointer
tier they move to 19 and 7.

**The keep budget — ruled (R56).** The driver (`test/replay`) refuses to compare a policy whose
`KeepSet.Tokens` exceeds the 40 000 keep budget at any point ("a policy that cheats on the budget
is not comparable"), and `qompack-l3` does, on `--policies stock,qompack-l3` (the gate's default
list `stock,null,oracle` never runs it). The overrun is the unconditional §2.2 retention: at 18
of the 39 compaction points the tail's preserved results alone exceed the budget — the corpus's
synthetic `Test` tool, absent from §2.2's table, is `DropNone`, and its results run to 11 000
tokens each. Bounding the pointer tier to what the budget leaves after that retention (§8.5's
"tier 3 truncates first") changes none of the overruns and costs the primary metric:
fraction_of_opt 0.947 → 0.624, below stock's 0.695, with first_divergence_turn 13 and
redundant_reads 25. Evicting preserved results oldest-first under the budget would satisfy the
driver but rewrites the plan's sentence. The shipped policy keeps R54's shape (the tier has the
budget to itself). Ruling R56: the overrun is §2.2's retention, not the tier's, and a §2.2 revision is
Qompack.md's to make, not SP-12's; the driver's FAIL line appears only when an operator adds
`qompack-l3` to `--policies`, and the gate's default set never does.

## Phase 4 measurement (commits 1–6, `l3policy`, corpus `testdata/sessions/synthetic`)

The seven assertions of `test/replay/phase4_test.go` were run against the 24-session synthetic
corpus with `ReplayOptions{Deterministic: true, Seed: 1}` and the harness's default K and budget.
All seven pass.

| Row | Bar | Measured |
|---|---|---|
| Σ RewriteTokens ratio, qompack-l3 / stock | ≤ 0.80 | **0.7059** (11 922 745 / 16 891 126) |
| first_divergence_turn (higher better) | ≤ 2 % regression | stock 11 → **19** |
| file_set_jaccard | ≤ 2 % | 1.0 → 1.0 |
| decision_preservation | ≤ 2 % | 1.0 → 1.0 |
| redundant_reads (lower better) | ≤ 2 % | stock 43 → **7** |
| re_attempts | ≤ 2 % | 0 → 0 |
| slack: warm points on the latest eligible candidate | slack = 0 | 17 of 17 |
| slack: cold points on `chooseP`'s deep cut | recomputed | 12 of 12 (one with slack 86 667: refactor-across-files-1021 at 160, two candidates, cold) |
| points with no eligible candidate → stock's keep-set | — | 10 of 39 |
| median slack vs turn count, slope | ≤ 0.02 tokens/turn | 0.0000 |
| longest / shortest quartile median slack | ≤ 1.25× (0/0 = 1) | 1.000 |
| residual ↔ rewrite identity, pause identity | exact | hold at every point of every run |

Recorded and **not enforced by replay** (R47): `residual_p95 = 685 564` against the plan's
20 000, `residual_slope = 500.8` tokens/turn against 0.02. The corpus's compaction points sit
2–40 turns (up to 340 000 tokens) past the last task boundary and twelve of them are cold, so
`n − p` is that distance by construction; the amortization claim those bars express is the
frontier's, discharged by V4-VERIFY §4.4's `TestV4_FrontierAdvancementKeepsResidualSpanODelta`.

**Cadence (ruling R57).** Every row above was measured with the detector fed one observation
per tool-bearing turn, which is the replay policy's convention and not the daemon's: the daemon
also pushes one empty observation per main-agent `Stop` (plan-mandated), so its stream is ~2×
this one with half-content windows and an alternating `time` stream. The final fix round
attempted to realign `l3policy.detect` to the daemon's stream — one observation per `ToolCall`,
then one empty observation per non-user turn, both stamped `Turn.TS` because the log records no
per-hook clock — and re-ran the seven rows. Under that cadence the rewrite ratio was **0.8227**
(13 895 786 / 16 891 126, bar 0.80 — red), first_divergence_turn 11 → 14, redundant_reads
43 → 27, file_set_jaccard / decision_preservation / re_attempts unchanged, slack slope 0.0000
and quartile ratio 1.000, 15 warm and 3 cold points on their cuts with 21 of 39 points having no
candidate (recorded: residual_p95 731 731, residual_slope 753.5), and the identity and pause
rows held; the detector declared 33 times with 3 declarations within three turns of the 105
generator changepoints (38 / 3 at hazard 0.02). The realignment was reverted under R57's revert
clause and the shipped numbers are the replay-cadence ones. **Live detector sensitivity under the
daemon's real cadence is unmeasured on this branch and is carried to V4-VERIFY §4.4 (the live
test)** — together with the question the numbers raise, whether the empty `Stop` observation
should instead carry the Stop's signals onto the last tool observation (review-6 Minor 11's
alternative), which needs a ruling because the Stop observation is plan-mandated.

Beyond the five §11.3 rows, from the driver's own report (`--policies stock,qompack-l3`):
fraction_of_opt 0.695 → 0.947; compaction_pause_ms P50 51 264 → 29 981 (modelled);
rehydration_tokens 960 785 → 3 518 441 and first_turn_after_ms P50 3 072 → 5 479, the price of
the §2.2 preservation rule on a synthetic tool the table does not name. A new policy has no
baseline for the gate to judge these against; they are stated here and in the PR body.

## The hot-path guard

`TestSchedulerNotOnHotPath` (`internal/cli/scheduler_hotpath_test.go`) is a `go/ast`
reachability walk over the package's non-test files, by name — every call of or reference to a
top-level function is an edge, and a method call `x.M()` is an edge to every method named `M`,
a deliberate over-approximation that can only add paths. Its roots are read off the `Cmd`
literals carrying `Hook: true` (today `doHook` and `runSessionStart`), so a hook added later is
guarded without editing the test. It asserts that nothing reachable from a hook reaches
`runDaemon`, `wireScheduler`, `registerSchedulerIdle` or `closeScheduler`, references
`scheduler.Evaluate`, `daemon.WrapServicesForScheduler`, `daemon.NewSchedulerRuntime`,
`daemon.RegisterSchedulerIdleWork` or `daemon.CloseSchedulerRuntime`, references the scheduler
package at all, or calls any `Evaluate` method — and, as its positive control, that the same walk
finds all three wiring functions and all four daemon symbols under `runDaemon`. 31 functions are
reachable from the hook roots; none is on the scheduler.

## Benchmarks (quiet machine, `go test -bench -benchmem -count=3`, minimum of three)

| Benchmark | Budget | Measured (min) | allocs/op |
|---|---|---|---|
| `BenchmarkEvaluate_64Candidates` | ≤ 50 µs/op, ≤ 8 allocs/op | 4.8 µs | 6 |
| `BenchmarkBOCDObserve_4Features/full_posterior` | ≤ 150 µs/op | 29.4 µs | 2 |
| `BenchmarkBOCDObserve_4Features/steady_state` | ≤ 20 µs/op | 2.2 µs | 2 |
| `BenchmarkBOCDMarshal` | ≤ 2 ms/op | 26.9 µs | 1 |
| `BenchmarkFeaturesFrom` | ≤ 100 µs/op | 19.8 µs | 39 |
| `BenchmarkReclaimableIndexBuild_5000Blocks` | ≤ 3 ms/op | 0.55 ms | 17 |
| `BenchmarkAssembleCandidates_2000ToolUses/cold` | ≤ 20 ms/op | 1.60 ms | 536 |
| `BenchmarkAssembleCandidates_2000ToolUses/warm` | ≤ 200 µs/op | 4.1 µs | 11 |
| `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates/cold` | ≤ 25 ms/op | 2.68 ms | 1 970 |
| `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates/warm` | ≤ 25 ms/op | 15.3 µs | 25 |
| `BenchmarkSchedulerTap_ObserveTool` | ≤ 1.5 ms/op | 41.9 µs | 73 |

Every minimum is inside its budget by at least 5×. The Assemble/RuntimeEvaluate rows are
measured after the final fix round's fixture change (every changepoint turn a round boundary, so
`Evaluate` scores the full 32-candidate cap); the warm rows' extra allocations are that larger
candidate slice. The eight names are the ones `testdata/bench-baseline.txt` gains in this commit.

## Known limitations

- **`n` does not shrink after a host auto-compact (ruling R58, doc-only this wave).** A
  `SessionStart(source=compact)` re-fire for the same session id takes `BindSession`'s same-id
  branch: it re-reads the model/subagent hints, re-resolves the regime and restarts the
  Young–Daly clock (`lastCompactionTS = now`), but leaves the token accounting untouched.
  `recomputeContextTokensLocked` is `Σ Segment.Tokens` over every closed segment of the session
  since its start plus the open accumulator — the plan defines `n` session-cumulative — so after
  the host has compacted the live context to a summary, `n` stays near its pre-compaction
  value: `n > hard` remains true, `UrgencyNow` and `ShouldCompact` stay latched, `mtbfSeconds`
  is 0 (the Young–Daly clause is disabled by `M = 0`), and `precompute_slice` is planned on
  every idle pass. Nothing in this wave acts on `ShouldCompact` (no consumer outside
  `scheduler`, `daemon` and `l3policy`), which is why this ships as a limitation rather than a
  fix. Carried to V4-VERIFY / SP-11: on the same-id `source=compact` re-fire, re-baseline `n` —
  remember `compactBaseTurn = maxTurn`, count only closed segments with
  `StartTurn > compactBaseTurn`, reset `openSegTokens`, and let SP-11's rehydrator supply the
  post-compact size when it merges. `Candidate.Pos` stays in the session-cumulative coordinate,
  so `n − p` remains consistent; only the threshold comparisons need the re-baseline.
- **Live detector cadence is unmeasured** — see "Phase 4 measurement", *Cadence (ruling R57)*.

## Consequences

- `Evaluate` is pure and replayable; every §8.4 clause has a named test and a Breakdown key, and
  `/qompack:status` and the eval harness read the same numbers the decision used.
- The cache regime is a first-class input: the scheduler reasons over a `(TTLMin, TTLMax, r, w)`
  range and charges the dearer write price when it cannot identify the regime, which biases it
  toward shallower cuts rather than costlier ones.
- `internal/scheduler`'s import set is unchanged (`core`, `paths`, `config`, `logging`, `obs`), and
  `test/replay/l3policy` proves the drop classification and candidate scoring can be reused
  without a composition root.
- The detector's prior scale (R46) decides whether L3 ever has a candidate: at the plan's `β0 = 1`
  it had none on sessions shaped like the corpus, live as well as in replay. At `β0 = 0.02` and
  the Appendix C hazard it still finds no boundary in ten of the corpus's 39 compaction points,
  where Qompack correctly does nothing and the host compacts; a higher hazard finds more
  (42 of the corpus's 105 generator boundaries at 0.02, replay cadence) at the cost of spurious
  declarations, and `scheduler.changepoint.hazardRate` is the operator's key to turn.
- The Phase 4 replay rows measure cut placement against the candidate set; the absolute residual
  bars are recorded for the live frontier test and never enforced here.
- No hook subcommand path reaches the scheduler; budget B-A is untouched by this slice, and the
  guard fails the build if a later edit changes that.

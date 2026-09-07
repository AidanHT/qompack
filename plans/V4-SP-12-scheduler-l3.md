# SP-12: L3 scheduler — Qompack cadence and measured advisory policies

**Status:** original Wave 3 implementation user-reported complete; M5 amendments remain planned, no migration gate newly passed. SP-19 M0-00 first integrates completed SP-10–13 and accepts M0-G0 before its remaining tasks and corrective follow-ups. **Planning owner/model:** writer C, requested gpt-5.6-terra medium; coordinator consolidation; independent review recorded in [evidence ledger](MIGRATION-EVIDENCE.md).

**Branch:** existing `feat/sp12-scheduler-l3` | **Wave:** 3 (V4) | **Prerequisites:** completed SP-01/05/06/07/08 preserved; SP-19 reconciliation, SP-20 durable frontier/state and SP-13 retrieval gates before M3-dependent enablement | **Design sections:** Qompack.md §§5–6, 8.4, 10–11.

## Mission

Schedule Qompack checkpoint work and select advisory candidates using observations with known scope, age and coverage. Keep three actions separate: advancing Qompack's committed checkpoint frontier; choosing its future injection/results; and requesting or blocking native compaction. The first two operate without the third.

The sibling `../qompack-sp12` at `4c954e0` contains scheduler implementation, including `PSelectionAvailable`, `YoungDaly` and `SkiRentalShouldWrite`. This is implemented-unverified evidence to reconcile, not permission to replace the branch or replay completed commits. Root stubs do not establish missing work. Preserve the 2026-08-26 future implementation partition and seven commit identifiers below, with corrected semantics.

## Gap closure map

| Retained gap ID | Contribution, accountable gap owner and future evidence | Residual |
|---|---|---|
| G1.1, G1.2, G1.3, G1.5 | SP-12: bounded cadence, dirty-state candidates and qualified forecasts; M5-G12-A/B/C | Candidate boundaries do not prove low semantic dependence |
| G1.4 | SP-19 owns host-limit compatibility; SP-12 supplies local observations for M0-G2/M5-G12-D | No claim to remove a native blocking-limit cliff |
| G5.1 | SP-12 local candidate/frontier records; M5-G12-C/E | No native cut selection or eviction |
| G5.2, G7.1, G7.6 | SP-19 owns cache/request cost and repeated-failure observations; SP-12 consumes qualified M0-G5/M5-G12-B evidence | No cache marker control, native cold-cut saving or guaranteed avoidance of host failure |
| G8.2 | SP-10 owns checkpoint fallback for summary uncertainty; SP-12 supplies measured cadence observations | No proof of summarizer quality, native O(delta) or first-turn saving |

## Design context (verbatim from Qompack.md)

Heading retained; this section summarizes v1.5 rather than reproducing obsolete source or executable examples. Separate checkpoint work, selection of future Qompack representations, and native compaction actions.

[Qompack.md](../Qompack.md) v1.5 and [architecture §0.1](00-ARCHITECTURE.md) supersede v1.3 formulas and policy claims. The old design excerpts remain available in Git history; they are not a second implementation specification. Ledger E11–E13/E17 and A01–A05/A13 record the disposition and target evidence limits.

## Out of scope

Native history rewriting, historical cut selection, cache-marker changes, arbitrary result deletion, default automatic-compaction veto, or optimization blocking of manual compact. A bounded veto alone is unsafe without a reliable recovery/proactive distinction. No automatic native trigger is required for plugin utility. Young–Daly is retired from the default policy; old settings may be read compatibly as deprecated harness diagnostics.

## Interface contract

### Consumes

Retain `internal/scheduler/types.go`, `evaluate.go`, `runtime.go`, `formulas.go`, `detector.go` and `schedulertest` contracts until SP-19 audits the active sibling. Use SP-20 committed publication identities/frontier and explicit gaps, SP-10 checkpoint eligibility, SP-11 complete-record budget, SP-13 resolvable authorized handles, and SP-19 per-capability observations/request ledger.

### Produces

A Qompack cadence decision, next-representation candidate and separately qualified native-control capability record. Missing, stale, cross-session, or immediately-post-compact token observations remain unknown. No `PSelectionAvailable` boolean may silently certify host control; migrate its consumers and guards compatibly on a proposed `arch/scheduler-capabilities` prerequisite if needed.

## Implementation spec

### 1. Baseline cadence and lifecycle

Begin with configurable cadence, dirty-state triggers and bounded maintenance. Reuse the existing daemon worker where sufficient; future lifecycle changes must include cancellation, queue and CPU/I/O quotas, locking, restart reconciliation, and crash recovery. Idle time is not free compute. Background work cannot delay finalization indefinitely, publish references before objects, or turn gaps into a complete frontier.

### 2. Observations and forecasts

Keep provider/model/session/epoch, observation timestamp, estimator version, source and completeness with each signal. Inspect status-line integration without overwriting user configuration; unknown values cannot establish headroom. Experimental predictive reserves include parallel tool bursts and model-output allowance, then calibrate against observed errors on held-out workloads. Expired observations revert to the simple cadence.

Token/cache state cannot be derived exactly from an arbitrary request or tool timestamp. Native TTL and rate schedule depend on request and billing category. Preserve old metrics with their old labels while introducing SP-19 request-category accounting; missing usage is not zero.

### 3. Policy boundaries and cost

Retain useful pruned changepoint implementation only with reported actual state/time bounds. Boundaries are candidates. Remove native O(delta), unsupported unimodality, cache-free replacement and impossible suffix-cut arithmetic from current policy and tests. Compare a declared cache example using N versus w + (N−1)r, not w/r as the ordinary break-even.

Keep Young–Daly and ski-rental historical functions/configuration readable if required, disabled as production timing guarantees. `internal/scheduler/skirental.go` remains this plan's future ownership; SP-16 does not edit it. No extra algorithm is a release dependency without ablation against cadence.

### 4. Supported actions and failure behavior

Checkpoint-frontier advancement and future representation choice require only their local contracts. Native request/block paths stay disabled until separate installed-version evidence establishes both mechanism and safety; manual compact is never blocked for optimization. Unknown adapter versions degrade to local-only behavior. A documented PreCompact input field is not a summarizer output setter.

## Test plan (TDD)

Future execution only: existing `go test ./internal/scheduler/...`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race` and `go run ./tools/devtool replay --ci`. Inspect the reconciled sibling test definitions first. New lifecycle/capability/calibration cases and their runner entry points are future tasks; a skipped host case retains unverified status.

| Gate | Observable future acceptance / retained artifact |
|---|---|
| M5-G12-A | Cadence remains useful with unknown observations; cancelled, full-queue, disk/lock and crash cases leave bounded recoverable work; worker/cadence trace |
| M5-G12-B | Provider/model/date/scope, missing telemetry and forecast error bands recorded; parallel output allowance and expiry tested; calibration/ablation report |
| M5-G12-C | Checkpoint, future-representation and native-action decisions distinguishable; no unsupported cut emitted; adapter decision transcript |
| M5-G12-D | Older/unknown/managed hosts and manual/automatic/recovery ambiguity keep native optimization disabled; packaged canary artifact from SP-19 |
| M5-G12-E | Concurrent and failed work cannot advance beyond durable dependencies or conceal event gaps; crash/frontier manifest |

Performance figures in the original plan are targets, never newly measured facts. Record startup, I/O, locking, payload sizes and tail latency on a quiet runner after future parallel work; preserve old outputs.

## Commit plan

All seven identifiers are retained for reconciliation. SP-19 first determines which work already exists; perform only the required corrective remainder, in separately authorized commits. No branch/worktree or commit is created here.

### Commit 1 — `feat(scheduler): qualify bounded changepoint candidates`

- [ ] Reconcile the existing pruned detector and state formats, add candidate/coverage failure cases, retain actual complexity evidence for M5-G12-B.

### Commit 2 — `fix(scheduler): separate cadence from historical cache formulas`

- [ ] Preserve compatible readers, retire Young–Daly defaults and w/r policy claims, add missing/stale signal and arithmetic cases; record setting rollback.

### Commit 3 — `feat(scheduler): expose supported local action decisions`

- [ ] Adapt evaluation/guards and candidate contracts together; M5-G12-C/D must show local utility with native actions disabled.

### Commit 4 — `fix(daemon): assemble qualified scheduler observations`

- [ ] Reconcile existing feature/candidate paths and SP-19 observation categories, test unknown scope/age and multiple parallel producers.

### Commit 5 — `fix(daemon): bound scheduler worker lifecycle`

- [ ] Add cancellation, quotas, bounded queues and restart compatibility around the existing runtime; retain M5-G12-A crash artifacts.

### Commit 6 — `fix(daemon): advance only durable checkpoint frontiers`

- [ ] Integrate SP-20/SP-10 acknowledged dependencies and gaps; run M5-G12-E and restore/disable drill.

### Commit 7 — `test(scheduler): compare supported policies and document gates`

- [ ] Preserve historical replay output names, add cadence/calibration ablations and request accounting, record uncertainty and independent review before opt-in.

## Subagent strategy

These are future implementation roles, not the current planning team. Preserve the 2026-08-26 independent-authoring intent subject to available execution permissions and agreed contracts; integrations and commits remain sequential. Existing files in the sibling are reconciled in place; absent names below are proposed future files, not evidence of absence.

| Future role | Exclusive assignment after contract agreement |
|---|---|
| Main owner | Shared `types.go`, capability contracts, shared fixtures/goldens, CLI composition, integration and seven commits |
| A1 detector | Scheduler `bocd.go` and detector-specific tests |
| A2 policy helpers | Scheduler thresholds/cache-regime/TTL/Young–Daly/ski-rental/drop-class helper files and their tests; no shared types |
| A3 observations | Daemon `scheduler_droppable` and `scheduler_features` files/tests |
| B1 local decisions | Scheduler `evaluate`, `pselect`, `gate` files/tests; no helper ownership |
| B2 candidates | Daemon `scheduler_candidates` files/tests |
| C1 runtime | Daemon `scheduler_runtime`, `scheduler_state`, `scheduler_tap` files/tests; CLI proposals to main only |
| C2 frontier | Daemon `scheduler_frontier`, `scheduler_idle` files/tests after SP-20 contract |
| D1 evaluation | `test/replay/l3policy`, phase4 fixtures/tests and scheduler ADR; absent paths are proposed |
| Independent cost reviewer | Read-only observation, request-ledger, policy and calibration artifacts |

Main publishes interfaces before A/B authoring; C consumes agreed decisions/frontier, D integrates last. No subagent modifies shared configuration or another plan's files. Future tests and commits require their own authorization.

## Exit criteria

- [ ] M5-G12-A–E pass with named versioned artifacts; native controls remain disabled where unverified.
- [ ] Local cadence works without token/cache observations or native compaction control.
- [ ] Declared budgets and actual serialized representations are feasible; optional complexity is justified against baseline cadence.
- [ ] SP-19 reconciles active work and preserved historical outputs before any remaining implementation is claimed complete.

## Done checklist

- [ ] Future implementation commits, compatibility review and gate artifacts are complete.
- [ ] V4 VERIFY and later V5 integration record actual results without changing prior V3 closure.
- [ ] Planning-only review is recorded separately in MIGRATION-EVIDENCE.md.

**Rollout/rollback:** observation/advisory first, cadence after M1–M3 contracts, forecasts only after calibration. Disable forecasts and native controls independently; retain cadence/last usable checkpoint. Restore compatible scheduler state or explicit empty/unknown state without deleting evidence. Never revert to unsafe old timing defaults.

**Blockers:** M5-U12-cache-observation (SP-19: canary scope/age/usage completeness; consequence unknown headroom); M5-U12-native-control (SP-19: installed action/safety matrix; consequence disabled); M5-U12-calibration (SP-12 evaluation owner: collect forecast errors and ablations; consequence simple cadence). All are future gates, not planning test results.

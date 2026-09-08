# SP-15: Phases 5 and 6 / L2 — representation selection and state-aware loop warnings

**Status:** EXECUTED 2026-09-08 on `feat/sp15-analyzer-selection-and-grammar` from `develop@7c735ac`; all seven commit deliverables landed and the M5/M6 gates dispositioned. One exit item remains open: the mandatory independent adversarial review. Execution record: [plans/sdd/V5-SP-15/report-main.md](sdd/V5-SP-15/report-main.md). **Planning owner/model:** writer C, requested gpt-5.6-terra medium; coordinator consolidation and independent review in [ledger](MIGRATION-EVIDENCE.md).

**Branch:** proposed future use of `feat/sp15-analyzer-selection-and-grammar`, from reconciled `develop` | **Wave:** 4 (V5) | **Prerequisites:** completed SP-01/06/07/08 preserved; SP-19, SP-20/M1–M2, SP-13/M2, SP-10/11 M3, SP-12 supported scheduling | **Design:** Qompack.md §§4, 6, 8.3–8.4, 10–11 | **Gap:** G6.3 plus qualified selection contributions in TRACEABILITY.

## Mission

Select at most one compatible representation of an item for a future Qompack injection or supported result, including dependency closure and all serialized overhead. This plan owns integration with SP-11's actual consumer; it cannot end with an unused selector.

Detect repeated goal/target/action/failure states while accounting for observed file/environment changes and progress. Start warning-only, with bounded deduplication and no self-triggering loop. Preserve useful existing grammar/sketch machinery, but require ablation before adding complexity.

## Design context (verbatim from Qompack.md)

The existing heading is retained; this section summarizes the revised v1.5 contract rather than reproducing obsolete source or executable examples. [Qompack.md](../Qompack.md) §§4/6/8.3 and [architecture §0.1](00-ARCHITECTURE.md) qualify DPI, action-divergence proxies, approximate relation graphs and selection guarantees. Ledger A01–A08/A12/A14 own the dispositions.

Existing root `internal/analyzer/types.go`, `selector.go`, `delta.go`, `redundancy.go`, `selector_test.go` and `analyzertest` establish contracts. `internal/grammar/sequitur.go`, `types.go`, `formatwarning.go`, `formatwarning_test.go` and `grammartest` establish grammar/warning locations. These definitions are not runtime evidence.

## Out of scope

Native-history cuts or eviction, arbitrary truncation, automatic prohibitions from loop detection, action-distribution KL estimates from edit distance, sound-program-slice claims, universal task-quality or greedy approximation guarantees. No mandatory model/service dependency or automatic command replay.

## Interface contract

### Consumes

SP-20 immutable observations, current authority, scopes, fidelity, dependency coverage and durable handles; SP-13 authorization/resolution envelopes; SP-10/11 complete-record budgets and overflow outcomes; SP-12 local action/observation records. Existing `arch/store-tooluses-by-session` pre-step remains a reconciliation dependency: SP-19 determines whether `ToolUsesBySession` is already available before proposing any missing interface amendment.

### Produces

A deterministic feasible set of exact spans, structured capsules, pointers or archive-only choices, each with coverage, estimated assembled cost, provenance and dependency closure. A warning record has a scoped state signature, observed progress, uncertainty, dedup key, expiry and bounded delivery. Future SP-11 injection and SP-21 owned-result consumers receive proposals only through their accepted contracts; SP-21 output replacement retains its separate opt-in gate.

## Implementation spec

### 1. Selection and integration

G6.3 retains its original concern: useful negative knowledge can be lost during compression. Prioritize applicable elimination evidence under the declared budget with provenance and safe recovery, while preserving uncertain/stale qualifications from SP-20. The former claim that it is always the highest-Delta content is a heuristic hypothesis, not a correctness or optimality guarantee.

Use transparent heuristics first. At most one mutually compatible representation per item; required dependencies, records, wrappers, handles and report overhead count. Tiny/zero budgets or oversized mandatory/conflicting records produce SP-11's explicit overflow and authorized archive-recovery outcome, not partial JSON or dropped constraints marked successful.

Use a stated nonnegative saturating coverage objective where suitable. Redundancy subtraction does not automatically preserve monotonicity, and dependency/representation constraints change the feasible family. Compare the same declared objective against exact solutions on small instances; this is not an oracle for task completion or a generic 1−1/e guarantee. Tie-breaking must be deterministic.

Main implementation owner integrates selection into the SP-11 assembler and any later supported result adapter. Validate actual delivered serialization, not only a unit selector return. Count the assembled representation using the supported estimator and calibrate against provider-reported usage; per-chunk sums do not establish exact provider tokens.

### 2. Qualified diagnostic signals

Shared files/tool ordering form an approximate relation graph; preserve unknown edges and do not prove outside content irrelevant. Action/file overlap and edit distance are behavior diagnostics, not unbiased KL or correctness labels. Changepoints are candidate boundaries with measured pruned costs. Misra–Gries returns candidates pending exact verification. MinHash similarity does not establish equivalence or exact deltas. Disk deduplication alone does not shrink delivered context.

DPI non-increase under its relevant Markov model is not strict loss each pass; retrieval changes available information. Keep original evidence and derivative provenance instead of prohibiting all summaries of summaries.

### 3. State-aware warnings

Compare goal, target, action and failure with relevant observed file/environment dependencies. Ordinary edit-test progress, changed constraints, different failure signatures and missing observation coverage must not be treated automatically as loops. Begin warning-only, deduplicate within bounded windows, cap volume and suppress warning/retrieval/injection feedback. Warnings never create binding eliminations.

Retain Sequitur if its codec/diagnostics help, with compatibility readers and an ablation against simpler state signatures. No further sketch/grammar machinery is a core dependency. Log usefulness and false alarms separately from repeated-action frequency.

## Test plan (TDD)

No execution here. Future existing entry points: `go test ./internal/analyzer/... ./internal/grammar/...`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool replay --ci`. Future phase5/6 and closed-loop consumer cases must be added where absent, with actual entry points recorded by commit 7.

| Gate | Observable future acceptance / artifact |
|---|---|
| M5-G15-A | At most one compatible representation, complete dependency closure/overhead, deterministic ties and valid zero/tiny/overflow outcomes. For G6.3, a current authoritative elimination qualified as applicable by SP-20 is included with its evidence or receives explicit authorized archive-only/overflow status and recovery path; stale/uncertain records keep their qualification and never become active constraints. Retain serialized-budget and small-objective comparison artifacts for all three cases. |
| M5-G15-B | DAG/action/changepoint/sketch/similarity claims remain qualified, exact claims independently checked; diagnostic counterexample fixtures and provenance |
| M5-G15-C | Selected records reach the real SP-11 future injection and permitted result consumer without invalid serialization or unsupported native action. G6.3 consumer scenarios demonstrate current-applicable retention versus explicit archive-only/overflow, stale-or-uncertain qualification, and resolvable permitted evidence; retain the consumer integration trace. |
| M6-G15-A | Progress/correction/environment changes and normal edit-test loops evaluated; bounded deduplicated warnings never amplify themselves; held-out false-alarm/usefulness report |
| M6-G15-B | Sequitur compared to state signatures with codec compatibility, resource cost and uncertainty; ablation or optional disabled status |

Fixed inputs, controlled closed-loop repository snapshots and failure recovery are distinct evidence layers. Include baseline variation, failed trials and held-out tasks; task completion/constraints/recoverability dominate, cost and diagnostic divergence remain separate. Old synthetic benchmark files retain their labels and provenance.

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Split grammar/codec/progress-warning checks from analyzer diagnostic/selector/budget checks on isolated fixtures after their contracts are agreed. Run the relevant existing analyzer/grammar package or real-case selection first; small exact-objective counterexamples belong in this fast lane.

**When broader checks are necessary.** Run actual SP-11 serialization/selection and warning-feedback seams after consumer integration. Full phase5/6 replay, held-out trials and ablations belong to commit 7 or the final V5 policy candidate, with declared sample sizes; keep benchmark timing isolated.

Each implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Retain seven numbered future commit identifiers; SP-19 reconciles existing work before assigning a remainder. Compatible tests/contract changes accompany their implementation; no actions occur during planning.

### Commit 1 — `test(grammar): reconcile retained sequence contracts`

- [x] Inspect and preserve existing grammar/codec interfaces, add progress-state and compatibility fixtures, and document optional Sequitur scope. **Landed** `f73c299`; the codec seam was frozen ahead of it in `6eac57c`.

### Commit 2 — `feat(grammar): emit bounded state-aware warnings`

- [x] Implement warning-only dedup/progress/self-suppression behavior with M6-G15-A tests and independent false-positive review. **Landed** `f903899`. False-positive evidence is 0/7 on held-out progress streams; the *independent* review remains OPEN (see report-main.md section 11).

### Commit 3 — `fix(analyzer): qualify retrospective diagnostic signals`

- [x] Reconcile scorer/dependency inputs and exactness claims, add M5-G15-B counterexamples, preserve diagnostic baseline names. **Landed** `badd27b`.

### Commit 4 — `feat(analyzer): describe evidence-backed representations`

- [x] Add representation/fidelity/dependency/overhead contracts and compatible readers; keep historical evidence reachable. **Landed** `cb2f74d`, over the shared types frozen in `ba98ce0`.

### Commit 5 — `feat(analyzer): select feasible complete representations`

- [x] Add deterministic heuristic selection and small-instance exact comparisons for the declared objective; demonstrate M5-G15-A including overflow. **Landed** `b142ba8`. Ratios 1.000 x5, 0.975, 0.738; no bound asserted.

### Commit 6 — `feat(checkpoint): integrate selection and scoped warnings`

- [x] Main owner integrates with SP-11/daemon, updates obsolete native-p-selection guards compatibly, verifies M5-G15-C and independent disable paths. **Landed** `8d338fb` + `f84cdf4`; guards updated in `1b0fd5d`. No import-rule amendment was needed.

### Commit 7 — `test(replay): evaluate selection and warning policies`

- [x] Add proposed phase5/6 consumer/failure/ablation cases, retain actual M5/M6 artifacts and rollback drill; no automatic default flip from a mock or synthetic score. **Landed** `86dd0be`. No default was flipped: both switches still ship false.

## Subagent strategy

Future roles preserve A–E; planning models above do not select Qompack runtime models. No future agents are launched now. Main lands one contract-first slice, then A, B, C and D author concurrently against the frozen contract.

| Future role | Exclusive source ownership; absent names are proposed |
|---|---|
| A — Sequitur core | `internal/grammar/sequitur.go`, proposed `rules.go`, core invariant tests |
| B — codec/warnings | Grammar codec/warn/formatwarning files and warning fixtures/tests; no core/types edits |
| C — diagnostic inputs | Analyzer block/delta/redundancy files and tests; no selector/shared types |
| D — selector | Analyzer selector/greedy files, budget/objective tests |
| E — evaluation | Proposed replay analyzer policy and phase5/6 tests, held-out report fixtures |
| Main implementation owner | Shared types, `internal/checkpoint` and daemon integration, configuration/guards, conformance activation, shared fixtures, seven commits |
| Independent reviewer | Read-only constraint, consumer, cost and false-alarm evidence |

**Contract-first slice (Main, before any fan-out).** Main lands and freezes the shared surface B and D consume, then dispatches. The slice must freeze:

| Frozen artifact | Consumed by |
|---|---|
| Shared representation types — exact span, structured capsule, pointer and archive-only choice, each with coverage, estimated assembled cost, provenance and dependency-closure fields | A, B, C, D |
| Grammar codec contract — encode/decode signatures, version tag, compatibility-reader behaviour, and the warning record's scoped state signature, observed progress, uncertainty, dedup key, expiry and bounded-delivery fields | B |
| Selector objective/feasibility contract — the stated nonnegative saturating coverage objective, the deterministic tie-break rule, at-most-one mutually compatible representation per item, dependency/record/wrapper/handle/report overhead accounting, and SP-11's explicit overflow and archive-recovery outcome | D |
| Shared fixture layout, conformance-activation switch names, and the independent selector/warning disable switches | A, B, C, D, E |

After freeze, A, B, C and D run concurrently on their existing exclusive file sets. B is briefed on the codec contract and D on the objective/feasibility contract, not on A's or C's finished files. E starts as soon as the contracts it measures are frozen rather than waiting for implementations; its full phase5/6 replay, held-out trials and ablations still belong to commit 7 or the final V5 policy stage.

**The only serial edges.** Everything not listed here overlaps; the seven-commit sequence still lands in order in the main session, gating the commits and not the authoring.

| Serial edge | Reason |
|---|---|
| Contract-first slice → A, B, C, D | B and D code against frozen shared types, not against sibling output |
| Main → shared `internal/checkpoint` and rehydration files | Main coordinates them after SP-11's handoff and this plan owns them until it hands off; SP-16 never edits them concurrently. Correctness constraint, not conservatism |
| Actual SP-11 consumer integration (commit 6) → E's combined evidence | E must measure delivered serialization, not a unit selector return |
| Later integration session → generated config documentation | Follows SP-14/SP-18 ownership |
| Authoring drains → quiet-runner benchmarks | Benchmarks are recorded on quiet runners, not during concurrent load; a number produced under co-load is requeued |

**Ownership overlap to resolve at dispatch.** This plan names "daemon integration" in Main's ownership; SP-14 names `internal/daemon/handlers`. That file must be assigned to exactly one plan's owner at dispatch time and the other plan briefed against its contract; it is never edited by both.

This plan's handoff is what unblocks SP-16's integration slice and SP-14's status surface, so landing the contract-first slice early is what parallelizes the whole wave.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21); these choices govern future delegates, not historical planning models or Qompack runtime calls. Preserve A–E and their file sets.

| Existing role | Requested model and effort | Reason |
|---|---|---|
| Main implementation owner | Opus 5 / high | Multi-file judgment: freezes the shared contract, integrates `internal/checkpoint`/daemon and configuration, sequences seven commits |
| A Sequitur core; B codec/warnings | Opus 4.8 / high | Bounded single-package invariant/codec/progress work against the frozen contract; extra grammar machinery still needs its ablation |
| C diagnostic inputs | Opus 4.8 / high | Bounded single-package slice; preserve approximate evidence and provenance rather than invent correctness labels |
| D selector | Opus 5 / high | Cross-consumer judgment: the declared objective and serialized feasibility checks span SP-11 and SP-21 consumers; escalate a cross-consumer conflict to the Fable 5.1 / high reviewer |
| E evaluation | Opus 4.8 / high; Opus 5 / low for result collation alone | Test design and failure attribution need reasoning; collecting already-produced artifacts is mechanical |
| Independent constraint/consumer reviewer | Fable 5.1 / high | Review G6.3 retention, dependency closure, overflow and actual SP-11 delivery across components |

One Fable review seat at a time, reusing its thread across roles; A, B, C and D each take their own child so they author concurrently. Preserve the quiet-run benchmark requirement and main-only shared checkpoint/configuration integration.

**Dispatch contract.** Every unit owns an exclusive file set and reports to a file with a short structured return: status, files touched, gates exercised, open questions. Each unit carries a per-unit tool-call budget and stops with BLOCKED after three identical failures instead of retrying. No `git stash` — the stash list is shared across all worktrees of one repository. Confirm any `go test -run` filter actually selects cases; it prints `ok` when it matches nothing, which caused three real misdiagnoses in the preceding session.

## Exit criteria

- [x] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass. Recorded in report-main.md section 9; every `-run` filter was confirmed with `-v` to match real cases.
- [~] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence. **PARTIAL.** Ownership and concurrency were enforced (five roles, five worktrees, disjoint write sets, no two roles owning one file) and the requested-versus-available routing is recorded. Opus 4.8 and Fable 5.1 are not selectable in this client, so every role ran Opus 5 under R1's documented fallback and no routing claim is made. **The mandatory independent adversarial review is NOT satisfied and remains open** — R1 is explicit that an author's own recheck cannot discharge it.

- [x] M5-G15-A/B/C and M6-G15-A/B have versioned artifacts or explicit optional-disabled disposition. M5-G15-A/B/C and M6-G15-A carry evidence; M6-G15-B is an explicitly-accepted optional-disabled disposition (the ablation found Sequitur not justified).
- [x] Actual consumer integration respects all serialized overhead, dependencies and overflow; no native control prerequisite. Assembled cost remains an ESTIMATE (M5-U15-representation-overhead is open).
- [x] Warnings remain bounded and warning-only; evaluation reports normal progress and uncertainty. 0/7 false alarms, 0 amplification over 210 fed-back observations.
- [x] Compatible readers, independent disable switches and rollback are reviewed by the coordinator; the mandatory INDEPENDENT review is still open (report-main.md section 11).

## Done checklist

- [x] All seven commit deliverables landed with actual results; the landed order deviates from the plan's numbering (report-main.md section 12).
- [x] No generic optimality or task-success claim survives: no (1-1/e) or approximation-bound claim appears in any shipped file, and the 0.738 non-monotone counterexample is committed rather than tuned away.
- [x] Execution is recorded in plans/sdd/V5-SP-15/ (contract.md plus reports A-E and report-main.md).

**Rollout/rollback:** report-only diagnostics, then opt-in selection, then justified warning/grammar policies. Disable selector and warnings independently; fall back to SP-11 complete-record heuristic and preserved archives. Restore compatible codec/state readers or verified backup after incompatible writes; never delete evidence for convenience.

**Blockers:** M5-U15-consumer-contract (main + SP-11: reconcile the real assembler, no selection enablement before M5-G15-C); M5-U15-representation-overhead (selector owner: assembled-model calibration, estimates/overflow until known); M6-U15-progress-observability (warning owner: dependency/coverage matrix, uncertain warnings only); M6-U15-sequitur-value (evaluation owner: ablation, optional disabled until justified).

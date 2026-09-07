# SP-15: Phases 5 and 6 / L2 — representation selection and state-aware loop warnings

**Status:** remaining Wave 4 plan; M5/M6 corrective tasks unchecked. **Planning owner/model:** writer C, requested gpt-5.6-terra medium; coordinator consolidation and independent review in [ledger](MIGRATION-EVIDENCE.md).

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

The implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Retain seven numbered future commit identifiers; SP-19 reconciles existing work before assigning a remainder. Compatible tests/contract changes accompany their implementation; no actions occur during planning.

### Commit 1 — `test(grammar): reconcile retained sequence contracts`

- [ ] Inspect and preserve existing grammar/codec interfaces, add progress-state and compatibility fixtures, and document optional Sequitur scope.

### Commit 2 — `feat(grammar): emit bounded state-aware warnings`

- [ ] Implement warning-only dedup/progress/self-suppression behavior with M6-G15-A tests and independent false-positive review.

### Commit 3 — `fix(analyzer): qualify retrospective diagnostic signals`

- [ ] Reconcile scorer/dependency inputs and exactness claims, add M5-G15-B counterexamples, preserve diagnostic baseline names.

### Commit 4 — `feat(analyzer): describe evidence-backed representations`

- [ ] Add representation/fidelity/dependency/overhead contracts and compatible readers; keep historical evidence reachable.

### Commit 5 — `feat(analyzer): select feasible complete representations`

- [ ] Add deterministic heuristic selection and small-instance exact comparisons for the declared objective; demonstrate M5-G15-A including overflow.

### Commit 6 — `feat(checkpoint): integrate selection and scoped warnings`

- [ ] Main owner integrates with SP-11/daemon, updates obsolete native-p-selection guards compatibly, verifies M5-G15-C and independent disable paths.

### Commit 7 — `test(replay): evaluate selection and warning policies`

- [ ] Add proposed phase5/6 consumer/failure/ablation cases, retain actual M5/M6 artifacts and rollback drill; no automatic default flip from a mock or synthetic score.

## Subagent strategy

Future roles preserve A–E; planning models above do not select Qompack runtime models. No future agents are launched now. Main agrees shared contracts before independent authoring and sequences integrations A→B, C→D, consumer wiring, then E.

| Future role | Exclusive source ownership; absent names are proposed |
|---|---|
| A — Sequitur core | `internal/grammar/sequitur.go`, proposed `rules.go`, core invariant tests |
| B — codec/warnings | Grammar codec/warn/formatwarning files and warning fixtures/tests; no core/types edits |
| C — diagnostic inputs | Analyzer block/delta/redundancy files and tests; no selector/shared types |
| D — selector | Analyzer selector/greedy files, budget/objective tests |
| E — evaluation | Proposed replay analyzer policy and phase5/6 tests, held-out report fixtures |
| Main implementation owner | Shared types, `internal/checkpoint` and daemon integration, configuration/guards, conformance activation, shared fixtures, seven commits |
| Independent reviewer | Read-only constraint, consumer, cost and false-alarm evidence |

Main coordinates shared checkpoint/rehydration files after SP-11's handoff; SP-16 never edits them concurrently. Generated config documentation follows SP-14/SP-18 ownership in the later integration session. Benchmarks are recorded on quiet runners after future authoring completes, not during concurrent load.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21); these choices govern future delegates, not historical planning models or Qompack runtime calls. Preserve A–E and their file sets.

| Existing role | Requested model and effort | Reason |
|---|---|---|
| A Sequitur core; B codec/warnings | Opus 4.8 / high | Bounded invariant/codec/progress work; B consumes A's agreed contract, and extra grammar machinery still needs its ablation |
| C diagnostic inputs | Opus 4.8 / high | Preserve approximate evidence and provenance rather than invent correctness labels |
| D selector | Opus 4.8 / high | Implement the declared objective and serialized feasibility checks; escalate a cross-consumer conflict to Fable 5.1 / high |
| E evaluation | Opus 4.8 / high; medium for result collation alone | Test design and failure attribution need reasoning; collecting already-produced artifacts is bounded |
| Independent constraint/consumer reviewer | Fable 5.1 / high | Review G6.3 retention, dependency closure, overflow and actual SP-11 delivery across components |

Start independent A and C slices after shared contracts. Schedule B after A's contract and D after C's contract; implementations may overlap only on agreed disjoint files. Main integrates actual consumers before E's combined evaluation. At most two authors and one ready reviewer are active, with one Fable maximum; roles may reuse threads and do not each require a child. Preserve the quiet-run benchmark requirement and main-only shared checkpoint/configuration integration.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] M5-G15-A/B/C and M6-G15-A/B have versioned artifacts or explicit optional-disabled disposition.
- [ ] Actual future consumer integration respects all serialized overhead, dependencies and overflow; no native control prerequisite.
- [ ] Warnings remain bounded and warning-only; evaluation reports normal progress and uncertainty.
- [ ] Compatible readers, independent disable switches and rollback are reviewed.

## Done checklist

- [ ] Seven future commit remainders and V5 integration accepted with actual results.
- [ ] Any unavailable integration remains unverified; no generic optimality or task-success claim survives.
- [ ] Planning review is recorded separately in MIGRATION-EVIDENCE.md.

**Rollout/rollback:** report-only diagnostics, then opt-in selection, then justified warning/grammar policies. Disable selector and warnings independently; fall back to SP-11 complete-record heuristic and preserved archives. Restore compatible codec/state readers or verified backup after incompatible writes; never delete evidence for convenience.

**Blockers:** M5-U15-consumer-contract (main + SP-11: reconcile the real assembler, no selection enablement before M5-G15-C); M5-U15-representation-overhead (selector owner: assembled-model calibration, estimates/overflow until known); M6-U15-progress-observability (warning owner: dependency/coverage matrix, uncertain warnings only); M6-U15-sequitur-value (evaluation owner: ablation, optional disabled until justified).

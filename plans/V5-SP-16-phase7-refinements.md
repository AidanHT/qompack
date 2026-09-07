# SP-16: Phase 7 refinements — scoped reuse and bounded retrieval experiments

**Status:** remaining Wave 4 plan; M6 future tasks unchecked. **Planning owner/model:** writer C, requested gpt-5.6-terra medium; coordinator consolidation and independent review in [ledger](MIGRATION-EVIDENCE.md).

**Branch:** proposed future use of `feat/sp16-phase7-refinements` from reconciled `develop` | **Wave:** 4 (V5) | **Prerequisites:** completed SP-01/03/06/09 preserved; SP-19, SP-20/M1–M2, SP-13/M2, SP-10/11 M3, SP-12/SP-15 M5 | **Design:** Qompack.md §§5–6, 8.3/8.7, 10–11.

## Mission

Reuse evidence only with explicit session/project/worktree, branch/version, authorization, dependency coverage, applicability and expiry. Do not share conclusions across unrelated repositories or import another session's unfinished intent. Adaptive retrieval and promotion affect future Qompack representations; they do not evict native messages.

## Design context (verbatim from Qompack.md)

Heading retained; current v1.5 requirements are summarized here without repeating old executable examples. [Qompack.md](../Qompack.md) §§6/8.7 and ledger A06/A08/A12/A14/A15 qualify Bloom coverage, complete-record serialization, experimental retrieval and optional complexity. No gap is unconditionally closed by a refinement. Contributions to G3.3, G6.1/G6.2, G4.3/G7.3 and G7.6 remain mechanism-and-evidence claims in TRACEABILITY.

Existing `internal/mcp/tools.go` declares `Promoter`; `internal/store/segments.go`, `segment.go`, `internal/negknow/descriptor.go`, `staleness.go`, and `internal/checkpoint` contracts provide evidence locations. Active SP-12 sibling scheduler work is implemented-unverified, and its `skirental.go` ownership stays with SP-12.

## Out of scope

Native history/cache control, unlimited retained memory with bounded storage, fixed Bloom false-positive rate under unlimited insertion, optimal arbitrary byte truncation, exact usage from chunk sums, or a Codex runtime port. TinyLFU, GreedyDual-style scores, LLMLingua-2, ACE and cache-aware compression are optional experiments after simple baselines, not release dependencies or permission to call an external model/service.

## Interface contract

### Consumes

SP-20 immutable evidence, exact state/applicability, retention/authorization contracts and filter generation/watermark; SP-13 real retrieval response/error envelopes; SP-11 complete-record injection/budget and SP-15 representation selection; SP-12 supported local observation/cadence records.

### Produces

Scope-qualified reusable candidates, bounded retrieval reminders/attempt records, demand/usefulness observations, and suggestions for future representation promotion. A retrieval error is never “not tried”; stale/unknown applicability cannot become active solely through a filter or heuristic. Cross-session summaries remain attributed derived claims.

## Implementation spec

### 1. Scoped reuse and consent

Define project identity independently of a guessed path string, then bind repository, worktree, branch/version and session relationships with observed evidence. Scope/authorization precedes preview and expansion. Preserve unknown relationships, failed children and asynchronous tails explicitly. Expiry, deletion, supersession and correction must invalidate relevant reuse; unsupported dependency extraction yields uncertain.

User requirements/decisions retain their authority and scope. Other sessions' hypotheses, conclusions and unfinished tasks cannot silently become current user intent. An optional warm prior is a labeled statistical candidate, not inherited truth. No external service or credential reuse without explicit consent.

### 2. Bounded retrieval and residency experiments

Triggers may include changed context, unresolved references, applicable eliminations or repeated errors. Cap reminders, attempts, work queues and processing costs; prevent own-result/warning feedback loops and stop when authorization or recovery fails. Value-of-information is an experimental policy, not an exact computable oracle.

Record usefulness separately from request frequency, distinct touches, failed attempts and missing telemetry. High recovery cost can justify keeping a complete span. Demand-based promotion only changes the next Qompack injection or result; same-epoch delivery does not prove native residency. Relative deltas retain SP-21's independent availability contract.

### 3. Filters, retention and representation budgets

Reuse sufficient existing exact indexes. If per-segment Bloom acceleration is adopted, publish generation and coverage atomically with its index, confirm positives exactly, and bypass stale/incomplete negatives. Bound capacity/rebuild cost; a fixed filter cannot hold an unbounded insertion stream at fixed error rate.

Tune complete records under SP-11/SP-15 serialized budgets, with deterministic overflow and recoverable archive status. Preserve delta bases, live leases, checkpoint/evidence roots, pending publication and rollback retention. Maintenance has cancellation, quotas, crash recovery and explicit expiry; SessionEnd is not its only recovery path.

### 4. Optional experiments and configuration compatibility

Keep current settings readable through SP-19 versioned validation/deprecation; do not make `runtime.phase7` defaults a promise of optional enablement. Leave scheduler ski-rental files to SP-12. New heuristics or compression models require declared baseline/objective, privacy/resource review, ablation and separate service authorization where applicable. Lack of benefit leaves them disabled.

## Test plan (TDD)

No tests run now. Existing future commands: `go test ./internal/store/... ./internal/negknow/... ./internal/checkpoint/...`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool replay --ci`. Proposed scope/reuse/phase7 cases and a controlled task runner must be created in future commits if absent.

| Gate | Observable future acceptance / artifact |
|---|---|
| M6-G16-A | Session/project/worktree/branch/version changes, denied access, expiry, corrections and other unfinished intent cannot silently cross scope; applicability/authorization transcript |
| M6-G16-B | Changed references/errors trigger bounded attempts with missing telemetry visible; usefulness distinct from frequency; retrieval burden and failed-attempt report |
| M6-G16-C | Promotion changes only actual future Qompack delivery with complete record/overhead budgeting; consumer and overflow trace |
| M6-G16-D | Optional policies compared to simple baselines on held-out tasks, uncertainty/resource/privacy costs reported; adoption decision or disabled disposition |
| M6-G16-E | Stale/incomplete filters, live readers/delta bases, pending writes, cancellation, quotas and crashes preserve valid references or explicit unavailable states; maintenance/recovery manifest |

Correctness and recoverability gates are independent of lower token cost. Preserve historical synthetic outputs, include failures and re-reading of changed files as potentially useful work.

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Use separate short scope/expiry/authorization, filter-watermark, retrieval-reminder and serialized-promotion cases while the main owner retains coupled implementation. An optional fixture worker may run independent cases in its own store; shared-state interactions stay one owned scenario.

**When broader checks are necessary.** Run real SP-13/SP-11/SP-15 consumer and maintenance/recovery seams once their inputs are integrated. Full phase7/held-out evaluation belongs to commit 7 or the final V5 policy candidate. Optional disabled policies need a recorded disposition rather than unnecessary experimental runs.

The implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Seven original identifiers retained; reconcile any existing work first, then implement only the separately authorized remainder.

### Commit 1 — `test(refinement): specify scoped reuse and setting compatibility`

- [ ] Define applicability/expiry/authorization fixtures and versioned setting behavior; no optional default flip.

### Commit 2 — `feat(refinement): qualify warm priors and retrieval triggers`

- [ ] Replace the old SP-16 ski-rental assignment with bounded observations/triggers; retain SP-12 ownership and deprecated reader compatibility.

### Commit 3 — `fix(store): qualify filter coverage and demand records`

- [ ] Reuse exact records, add optional bounded filter generation/rebuild and usefulness metadata only when needed; run M6-G16-B/E.

### Commit 4 — `feat(daemon): apply scope-aware reusable candidates`

- [ ] Integrate authority/dependency/expiry checks; run M6-G16-A including branch/worktree/child failures.

### Commit 5 — `feat(checkpoint): promote only future compatible representations`

- [ ] Integrate through SP-11/SP-15 contracts after their handoff, verify M6-G16-C and preserve archives.

### Commit 6 — `fix(refinement): bound serialization and maintenance`

- [ ] Retire arbitrary truncation, add complete-record overflow, cancellation/quotas/recovery and compatible-state rollback.

### Commit 7 — `test(refinement): evaluate reuse and optional policies`

- [ ] Run M6-G16-A–E in future, record ablations/failures/uncertainty and disabled alternatives; document independent switches and rollback drill.

## Subagent strategy

Retain the original sequential future implementer with an optional bounded test writer; six-package shared integration does not justify concurrent ownership. This is separate from planning writer C. Main owns shared configuration, daemon, store and checkpoint integration plus all seven commits, after prior owners hand off. Optional test writer owns only newly agreed scope/filter fixtures and test files, never production files. Independent applicability/privacy reviewer reads future artifacts.

Proposed absent implementation files include warm-prior, segment-filter, demand-record, promotion and phase7 integration/test files within their existing packages. SP-19 first reconciles actual names, and main records an exact non-overlapping file allowlist before future delegation. `internal/scheduler/skirental.go` is explicitly excluded. No blind file creation over active sibling work.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). Retain the sequential main implementer; this plan's six-package integration remains one owner's work. At most one optional test worker and one independent reviewer may overlap.

| Existing role / bounded task | Requested model and effort | Reason and boundary |
|---|---|---|
| Scope/filter test helper | Opus 4.8 / high | Author only assigned stale-scope, expiry, filter-watermark and promotion cases after contracts freeze |
| Same helper, experiment/result inventory only | Opus 4.8 / medium | Map completed baseline artifacts and explicit deferrals; no speculative algorithm implementation |
| Independent applicability/privacy reviewer | Opus 4.8 / high | Check project/worktree/version applicability, unknown evidence and bounded reminders |
| Focused unresolved trust or retention decision | Fable 5.1 / high | Replace a generic repeat review with one precise cross-session authorization or GC/recovery question, then return the decision to main |

SP-12 retains ski-rental ownership; SP-15's consumer handoff still precedes shared integration. Optional experiments must justify themselves against the simple baseline before extra workers are assigned. Keep tests that mutate shared state or collect timings sequential and isolated. A reviewer cannot approve its own test or implementation work; a required independent review remains a separate task even if optional workers are omitted.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] M6-G16-A–E have actual versioned artifacts or optional-disabled disposition.
- [ ] Reuse never converts stale/unknown evidence or another session's unfinished intent to current authority.
- [ ] Retrieval/warning volume and maintenance are bounded, with missing telemetry visible.
- [ ] Promotion is demonstrated on future Qompack representations, without native-context claims.

## Done checklist

- [ ] Future seven-commit remainders, compatibility review and V5 gate completed with actual results.
- [ ] Retention/backup and independent disable/rollback drill accepted.
- [ ] Planning review recorded separately in MIGRATION-EVIDENCE.md.

**Rollout/rollback:** start report-only scoped candidates, then opt-in bounded retrieval/promotion after M1–M5. Disable reuse, reminders and optional policies independently; fall back to current-session exact retrieval and SP-11 complete-record assembly. Preserve archive evidence and compatible state readers, or restore verified backup where old readers cannot interpret new writes.

**Blockers:** M6-U16-applicability-coverage (state owner: dependency/scope extraction matrix, uncertain until covered); M6-U16-usefulness-telemetry (evaluation owner: usefulness/attempt instrumentation and held-out report, no promotion benefit claim); M6-U16-authorization (trust reviewer: denied-preview/expansion matrix, reuse disabled across unverified scope); M6-U16-optional-policy-value (evaluation owner: ablation, optional disabled).

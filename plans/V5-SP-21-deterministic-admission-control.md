# SP-21: deterministic admission control — opt-in safe transformation of delivered results

**Status:** M4 future plan. Planning owner: Writer B using gpt-5.6-terra at medium effort. Implementation is separately authorized only after M1–M3 gates.

**Branch:** proposed `feat/sp21-deterministic-admission-control` and matching sibling worktree; not created | **Wave:** 4 (V5), extension after M3 | **Dependencies:** SP-19/M0 target inventory, SP-20/M1 durable capture/publication/identity, SP-20/SP-13 M2 authorization/recovery, and SP-10/SP-11 M3 lifecycle/recovery. Default state is off.

---

## Mission

SP-21 defines an opt-in deterministic admission pipeline for a newly delivered result. It captures durably, verifies the capture/publication outcome, selects at most one compatible representation, produces a resolvable handle, and then performs a single permitted transformation. Failure of capture, parsing, policy selection, or handle resolution passes through the original result unless privacy policy denies delivery.

Fresh Qompack-owned retrieval responses are the first supported transformation surface, before delivery. Already-processed Qompack envelopes bypass repeat processing to prevent recursion. A tiny host schema allowlist is accepted only after target-specific parser/version/coexisting-hook evidence. Unknown schemas and parsers pass through. This plan does not redefine SP-20 identity/publication contracts.

Planning owner: Writer B, requested gpt-5.6-terra at medium effort; effective model/effort and usage not exposed. Future implementation owner: a transformation owner supported by a host-boundary tester and an independent host/recovery reviewer. [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) records the initial branch, dirty files, worktrees, model requests and blockers; row [X08](MIGRATION-EVIDENCE.md#mandatory-future-scenario-ownership-matrix) is this plan's sole accountable requirement, and rows E09/E12/E13 are its target evidence.

## Design context (verbatim from Qompack.md)

The template heading is retained; v1.5 requirements are summarized without executable examples.

### Existing evidence and target unknowns

§7.3 records seven declared hook events in `internal/pluginmanifest/manifest.go` and requires SP-19 to distinguish observation, injection, new-result replacement, usage attribution, estimation, request, blocking and history rewriting as separate capabilities. New-output replacement is documented; installed support is unverified. MIGRATION-EVIDENCE E09 records the documented `updatedToolOutput` mechanism with an unknown installed version, E12 keeps native cuts, history rewriting and cache-marker controls unsupported, and E13 keeps injection, attribution, estimation and request/block control unverified until target canaries run.

§5.5 admits capsules and pointers only for newly delivered Qompack or allowlisted host output, and only against capture-before-replacement, schema and recovery evidence. §7.1 gives recording, reinjection, output replacement and experimental policy independent future kill switches, and Appendix C keeps replacement and unsupported native controls off. §8.7 states that admission begins with owned responses after the M1–M3 recovery gates, and that deterministic self-contained capsules precede deltas and allowlisted host transformations. §12 assigns optimizer failure and malformed output to SP-21 pass-through after policy checks, with replacement off until recovery. §10 Phase 7 assigns M4 admission to this plan separately from SP-16 reuse work, and it is not a prerequisite for SP-17/SP-18 production evaluation.

### Sequence and fidelity rules

M4 follows M1–M3 because replacement is safe only when retained content remains durably captured and recoverable. The synchronous sequence is capture, durability verification, one deterministic representation decision, resolvable handle verification, and one transform. It never chains transforms, rewrites native history, or relies on native eviction/control.

The record preserves both structured and displayed meaning: status, stderr, diagnostics, interruption/media flags, input/output counts, signatures, source identity, source span/offsets where available, parser/schema version, fidelity, coverage, and the transformed display. A same-epoch prior delivery is not proof that a relative delta has a valid baseline. Self-contained capsules precede relative deltas.

### Recursion, coexistence and enablement rules

Competing hooks are expected. The target contract must establish ordering, coexistence behavior, output schema, and idempotence marker behavior. An already-processed envelope marker bypasses repeat admission; fresh owned results remain eligible for their first transformation. Automatic replacement stays feature-switched off until the target gate passes; privacy denial follows the applicable privacy policy rather than an optimization fallback.

§11.1 keeps task completion, constraint/regression failures and evidence recoverability separate from cost, and compares stock, currently implemented Qompack, corrected checkpoint/retrieval and admission independently. A cost-only result never enables admission.

## Out of scope

| Item | Owner |
|---|---|
| M1 capture/object/publication identity, durable frontier and migration semantics | SP-20/M1 |
| M2 derived-state authority, uncertainty and authorization semantics | SP-20/M2 |
| MCP protocol/server behavior, tool discovery, preview authorization and retrieval UI | SP-13 |
| M3 checkpoint presentation, rehydration selection and latest-usable lifecycle | SP-10/SP-11 |
| Native-history rewriting, delivered-result deletion, cache-marker manipulation, compaction control and automatic veto of manual compact | Unsupported; SP-19 capability register records the disabled status |
| Broad parser support, a generic schema matcher, model/API dependencies and untested host output forms | Not authorized by this plan |
| Command replay, denied-read bypass, hash-as-authorization and secret logging | Not authorized by this plan |
| Scoped/expiring project reuse and bounded demand promotion | SP-16 |

## Interface contract

### Consumes

| Input | Requirement |
|---|---|
| M1 capture/publication | Durable identity and verified publication before replacement |
| M2/SP-13 recovery/authorization | Handle resolution and access decision before transform |
| Host delivered-result adapter | Target-tested schema/version and coexisting-hook semantics |
| Privacy policy | Denial action and audit disposition |
| Feature switch | Off by default; explicit opt-in only |

### Produces

| Output | Requirement |
|---|---|
| Admission record | Input identity, parser/version, decision, transform, coverage/fidelity, and reason |
| Captured original | Durable before any replacement and resolvable under policy |
| One transformed result | Preserves structured/displayed fields and a resolvable handle |
| Pass-through | Exact original when non-private failure/unknown policy occurs |
| Denial result | Privacy-policy outcome with audit diagnostic, never an optimization substitute |

### Required invariants

1. The feature switch ships off. Enablement requires the M4 admission gate; no default, upgrade path, or unknown host environment turns it on.
2. Durable capture and verified publication precede every replacement. Capture, durable-write or acknowledgement failure forbids pointer replacement and preserves the host result.
3. One delivered result receives at most one representation decision and one transform. Transforms never chain, and no stage re-enters the pipeline.
4. An already-processed Qompack envelope bypasses admission. A fresh owned result is eligible exactly once, and repeated delivery of the same result is idempotent.
5. Parser dispatch requires an exact schema and version match from the allowlist with deterministic tie-breaking. Unknown schema, unknown version, unrecognized format, and binary/multimodal payloads pass through.
6. Every emitted handle resolves under current authorization before delivery. An unresolvable, denied or unavailable handle blocks the transform and stays visible as such.
7. Structured and displayed meaning survive together: status, stderr, diagnostics, interruption and media flags, input/output counts, signature, source identity and spans, parser/schema version, fidelity, and coverage.
8. A self-contained capsule precedes any relative delta. A delta requires a verified compatible baseline; a same-epoch prior delivery is not that proof.
9. A changed failure signature, a changed parser/schema version, or uncertain capture resets delta eligibility to a capsule.
10. Failure of capture, parsing, selection, resolution or target evidence passes the exact original through. Only privacy policy produces a denial, and a denial is never an optimization substitute.
11. Admission never rewrites native history, deletes a delivered result, manipulates cache markers, controls compaction, replays a command, or treats a hash as authorization.

## Implementation spec

### M4-01: feature switch and target allowlist

Ship disabled. Enable only for Qompack-owned results first, then a small enumerated host schema/version allowlist proven on a supported target. Parser dispatch uses exact schema/version matching and deterministic tie-breaking. Unknown values pass through. The switch is independent of the recording, reinjection and experiment switches named in Qompack.md §7.1, so disabling admission never disables capture.

### M4-02: deterministic pipeline

Perform capture, durability verification, single representation selection, handle-resolution verification, then one transform. Each stage produces an observable decision. A failure passes through unchanged unless privacy requires denial. Capture failure forbids pointer replacement.

### M4-03: fidelity-preserving representation

Retain status, stderr, diagnostics, interruption and media flags, counts, signature, identifying spans, structured content, and displayed content. Prefer verbatim retention when recovery cost is high. Unrecognized test formats, binary/multimodal payloads and unknown schemas pass through under privacy policy. A capsule is self-contained before any delta. Relative output requires a verified compatible baseline, and same-epoch delivery alone is insufficient proof. A changed failure signature, parser/version change or uncertain capture resets delta eligibility.

### M4-04: recursion and competing hooks

Distinguish fresh owned results from already-processed envelopes. Transform an eligible fresh response once, then mark and bypass repeat parser/admission work. Target testing covers competing hook order, repeated delivery, malformed output, duplicate markers, and another hook’s transformation. The admission record identifies the observed chain without claiming unobserved order.

### M4-05: recovery and rollback

Every emitted handle resolves under current authorization before delivery. When target/schema/recovery evidence fails, disable the feature switch and pass through. Before a new admission schema write, prove compatible reader support or take a verified backup; after a write, rollback validates compatible read or restores backup before re-enabling.

## Test plan (TDD)

No tests run in this planning pass. Future TDD starts with contract failures. Existing future commands are `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, and `go run ./tools/devtool plugin-validate`; none runs here. The host-adapter admission test entrypoint is proposed future work and requires target evidence before creation; do not represent it as an existing test.

Every row below is future acceptance and remains unchecked. Each maps to MIGRATION-EVIDENCE row X08, whose accountable task is this plan.

| ID | Future scenario, owner, and required artifact |
|---|---|
| T21-SWITCH-01 | Default-off, explicit opt-in, and target-gate disable behavior; **transformation owner**; switch-state and independent-kill-switch trace showing capture unaffected |
| T21-PIPE-01 | Capture and publication verify before one transform/handle emission; **capture-boundary owner**; ordered stage-decision record against real SP-20 seams |
| T21-PASS-01 | Capture/parser/selection/handle failure passes original through unless privacy denies; **transformation owner**; byte-identical pass-through artifact plus a distinct privacy-denial audit diagnostic |
| T21-FIDELITY-01 | Status, stderr, diagnostics, interruption/media flags, counts, signatures, spans, structured/displayed forms survive; **representation owner**; field-by-field preservation fixture including binary/multimodal pass-through |
| T21-BASELINE-01 | Self-contained capsule precedes delta; same-epoch prior delivery cannot prove a baseline; **representation owner**; baseline-verification record with absent/corrupt/changed-signature resets |
| T21-RECURSE-01 | Fresh owned response transforms once, processed envelope bypasses, duplicate delivery is idempotent; **host-coexistence owner**; marker/idempotence transcript |
| T21-HOST-01 | Exact schema/version allowlist, unknown parser pass-through, and competing hooks matrix; **host-coexistence owner**; installed host/provider/OS/version canary transcript, or an explicit unverified disposition that keeps the allowlist empty |
| T21-RECOVERY-01 | Every emitted handle resolves under authorization; denied/unavailable remain visible; **recovery/rollback owner**; resolution audit against real SP-13/M2 authorization, with denied, unavailable and uncertain kept distinct |
| T21-QUALITY-01 | Supported task completion/constraints/recoverability compared to unmodified output on controlled held-out tasks; failures and uncertainty retained, no cost-only win; **transformation owner with V6 evaluation review**; three-layer comparison report with predeclared margins, or an inconclusive result |
| T21-ROLLBACK-01 | Disable/pass-through and compatible-reader-or-backup rollback before/after schema write; **recovery/rollback owner**; pre-write reader-or-backup proof and post-write restore artifact with evidence retained |

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Run schema/parser/fidelity/budget and pass-through/recursion cases as short independent groups on frozen admission contracts. Resolve actual test names after the proposed package exists. Test capture ordering and authorized handle resolution against real SP-20/SP-13 seams before drawing integration conclusions.

**When broader checks are necessary.** Reserve competing-hook/installed-host, pre/post-write rollback and controlled unmodified-output comparisons for the integrated opt-in candidate. Every T21 gate remains required for enablement, including quality and missing-baseline failure cases; isolated unit success does not enable admission.

Each implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Future implementation only; six small conventional commits, no attribution trailers. Each row places its contract/test before compatible implementation and retains its validation evidence. All rows are unchecked and require separate authorization after M1–M3.

| Commit | Scope and validation gate |
|---|---|
| [x] 1. `test(admission): specify target schema and pass-through contracts` | T21-SWITCH-01, T21-PASS-01 and T21-HOST-01 contract failures against the frozen adapter surface; the allowlist stays empty until target evidence exists |
| [x] 2. `feat(admission): record verified capture and deterministic decisions` | T21-PIPE-01 using SP-20 adapters without redefining identity/publication; ordered stage decisions and capture-failure non-replacement |
| [x] 3. `fix(admission): preserve delivered result fidelity` | T21-FIDELITY-01 and T21-BASELINE-01 capsule/delta, structured/displayed, signature, count and span behavior |
| [x] 4. `feat(admission): emit one resolvable representation` | T21-RECOVERY-01 after the M2/SP-13 authorization/recovery gate; denied, unavailable and uncertain stay distinct |
| [ ] 5. `fix(admission): bypass processed envelopes and coexist with hooks` | T21-RECURSE-01 and the T21-HOST-01 competing-hook matrix; recursion, idempotence and observed-order evidence |
| [ ] 6. `test(admission): prove opt-in lifecycle and rollback` | T21-QUALITY-01 comparison against unmodified output and T21-ROLLBACK-01 backup/reader rollback; consolidates target gating, privacy and recovery artifacts |

## Subagent strategy

No future implementation subagents are authorized now; planning delegates are recorded separately. After separate implementation authorization, the architecture amendment must first reserve any new package surface. The proposed file map is `internal/admission/types.go`, `pipeline.go`, `parser.go`, `capsule.go`, `report.go`, and corresponding tests, with a composition-root adapter added only where the target contract permits it. It does not alter SP-20 storage/publication files.

| Future role | Proposed file scope | Integration responsibility |
|---|---|---|
| A — capture boundary | `internal/admission/pipeline.go` and capture/publication adapter tests | Uses SP-20 contracts; proves durable capture before transform |
| B — deterministic representation | `parser.go`, `capsule.go`, and fidelity/parser tests | Owns exact allowlist, version dispatch, capsule/delta rules |
| C — recovery and rollback | `report.go` and recovery/rollback tests | Owns handle-state diagnostics and disable/pass-through rollback evidence |
| D — host coexistence | Target adapter test fixture and competing-hook tests | Owns target version/schema/order observations, never a broad parser |
| Main implementation owner | `types.go`, feature-switch wiring, shared fixtures, integration, and commits | Integrates the single transform pipeline and resolves shared-contract conflicts |
| Independent host-boundary reviewer | Read-only target evidence review | Approves default-off, schema allowlist, competing hooks, pass-through, and enablement evidence |

No role changes SP-20 identity/publication semantics or edits source outside its approved future file set.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21); keep the existing A–D/main file ownership and default-off admission gate. No worker starts dependent implementation before M1–M3 and the reserved adapter contract are ready.

| Existing role | Requested model and effort | Reason and boundary |
|---|---|---|
| Main implementation owner | Opus 5 / high | Multi-file judgment: shared types, test contracts, feature wiring, pipeline integration and shared-contract conflicts |
| A capture boundary | Opus 5 / high | Multi-file judgment: apply the already-accepted publication adapter and prove capture-before-transform ordering; no SP-20 redesign |
| B deterministic representation | Opus 4.8 / high | Bounded slice: exact schema/parsers, capsule fidelity and baseline eligibility within owned files |
| C recovery/rollback | Opus 4.8 / high | Bounded slice: distinguish denied/unavailable/resolvable outcomes and switch-off behavior |
| D host coexistence | Opus 4.8 / high | Bounded slice: design competing-hook and version cases; never a broad parser |
| Transcript collation (support seat) | Opus 5 / low | Mechanical collation of actual target transcripts and run transcripts into the owning role's report file; no design judgment |
| Independent host/recovery reviewer | Fable 5.1 / high | Trace capture-before-replacement, authorized handle resolution, recursion and structured/displayed semantics across the assembled pipeline |

**Schedule.** Main lands the shared types and test contracts slice first; that is the one contract-first serial step. It must freeze the reserved `internal/admission` package name and file map, the exported surface in `types.go` (pipeline input/output, decision and outcome enums, handle and capsule shapes, error taxonomy for pass-through versus privacy denial), the default-off feature switch and target-allowlist config shape, and the shared test fixtures and helper signatures the T21 cases are authored against. Once that slice lands, A, B, C and D run concurrently on their existing exclusive file sets; no role waits on another role's slice. Main then integrates the single transformation pipeline and owns feature wiring.

**Serial edges.** These are the only ordering constraints: the architecture amendment reserves `internal/admission` before any authoring begins; Main's shared types/test-contracts slice precedes A–D; Commit 4 still follows the M2/SP-13 authorization and recovery gate; and the independent host-boundary reviewer runs on the assembled pipeline in a separate thread, not per slice. No role changes SP-20 identity or publication semantics, or edits source outside its approved file set. Do not parallelize a shared hook environment, golden fixture or enablement decision. Supported quality and recovery must pass before opt-in, irrespective of model choice.

**Authorization status.** This plan's status line authorizes implementation only after the M1–M3 gates. The M1–M3 implementation is complete and its focused gates pass on the integrated candidate, but the formal V4 sign-off is outstanding: five items are listed in `V4-report.md` section 19. Starting SP-21 therefore requires reading "after M1–M3 gates" as the delivered and focused-tested contracts rather than the signed V4 report. That reading is a user decision to record at dispatch, not a change this plan makes; this plan asserts neither that the gate is met nor that it is not.

**Dispatch decision, 2026-09-08.** The user authorized starting SP-21 on that reading: "after M1–M3 gates" is taken as the delivered and focused-tested contracts, not the signed V4 report. Recorded here because the paragraph above requires it to be recorded rather than assumed. What the decision does not change: V4 sign-off is still outstanding (`V4-report.md` §19, item 4 closed and item 3 half closed, the rest open), the feature switch stays refused, the host allowlist stays empty pending B01, and every T21 gate remains required before enablement. The architecture amendment reserving `internal/admission` landed first, as the serial edge above requires.

**Dispatch contract.** Each unit receives exclusive file ownership, writes its findings to a report file, and returns a short structured summary rather than prose. Each unit carries a per-unit tool-call budget and stops with a BLOCKED report after three identical failures instead of retrying. No unit runs `git stash`: the stash list is shared across all worktrees of one repository. Any `go test -run` filter must be confirmed to select actual cases — it prints `ok` when it matches nothing, which caused three misdiagnoses in the preceding session.

**Resource isolation.** Write-producing checks use separate worktrees or copies at the recorded HEAD, with separate stores, spools, fixtures and daemon/IPC identities. Never run two admission writers, a competing-hook matrix and a rollback drill, or a schema write and its reader against one fixture, except inside the single owned scenario that deliberately tests that interaction. Host canaries use disposable sessions, never a user's production session or data.

## Exit criteria

All criteria are future and unchecked.

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.
- [ ] Parallel handoffs record accepted inputs, unique file/resource ownership and the shared SP-14–21 worker pool; provisional checks remain distinct from final M4 acceptance.
- [ ] M1–M3 and target/privacy prerequisites are complete.
- [ ] Default-off switch, already-processed envelope bypass, and exact parser/version allowlist are proven.
- [ ] Every replacement follows capture, verification, one selection, resolvable handle, one transform.
- [ ] Failure passes through except documented privacy denial.
- [ ] Fidelity and self-contained-capsule-before-delta tests pass.
- [ ] Competing-hook and rollback evidence pass before enablement.
- [ ] T21-QUALITY-01 shows retained task completion, constraints and recoverability against unmodified output; a cost-only result does not enable admission.
- [ ] Every X08 scenario has a real check and artifact, or an explicitly accepted blocked/disabled disposition that keeps the allowlist empty.

## Done checklist

### Planning-review checks

- [ ] This new plan preserves the requested heading order, M4/M1–M3 dependency boundary, and 5–8 future commit convention.
- [ ] It uses only existing future validation commands and labels the host entrypoint proposed.
- [ ] Its milestone sections, invariants, scenario ownership, commit gates and rollback section match the SP-19/SP-20 house structure, and its requirement IDs trace to MIGRATION-EVIDENCE X08.
- [ ] No source/config/test/fixture/runtime/Git work occurred in this planning pass.

### Future implementation checks

- [ ] T21-SWITCH-01 through T21-ROLLBACK-01 pass on named future entrypoints.
- [ ] The independent host-boundary review approves target evidence before the feature switch is enabled.

### Rollout, rollback and blockers

**Enablement order:** ship disabled; enable first for Qompack-owned retrieval responses only; add an enumerated host schema/version allowlist entry only after that exact schema and version produce a target canary transcript and a competing-hook observation. An unverified or unknown target keeps the allowlist empty and admission off. Enablement never follows from unit success, a cost result, or a provisional check on an intermediate snapshot.

**Rollback order:** disable replacement, restore pass-through, verify the compatible old/new reader or restore the verified backup, then preserve diagnostics and original captures for audit. Before any new admission schema write, prove a compatible deployed reader or take a consistent verified backup; after that write, repeat the drill against the actual new artifact. Never delete captures, diagnostics or evidence to make a retry look clean, and never silently downgrade a format. Disabling admission never disables capture, and a disabled pipeline still preserves the original result.

**Blockers.** M1 durable publication, M2 authorization/recovery, M3 latest-usable lifecycle, target adapter evidence and the privacy policy all block enablement. Ledger blockers apply directly: B01 installed host versions/permissions blocks the host allowlist and every target claim; B03 SP-20 durable recovery blocks all pointer replacement; B04 SP-13 retrieval blocks handle resolution; B08 routing and B09 concurrency govern future delegation. E09 keeps new-result replacement documented with an unknown installed target, E12 keeps native cuts, history rewriting and cache-marker controls unsupported, and E13 keeps injection/attribution/estimation/request control unverified. Failure of any prerequisite keeps admission disabled and the plan actionable; it never converts a pass-through into a delivery guarantee.

# SP-21: deterministic admission control — opt-in safe transformation of delivered results

**Status:** M4 future plan. Planning owner: Writer B using gpt-5.6-terra at medium effort. Implementation is separately authorized only after M1–M3 gates.

**Branch:** proposed `feat/sp21-deterministic-admission-control` and matching sibling worktree; not created | **Wave:** 4 (V5), extension after M3 | **Dependencies:** SP-19/M0 target inventory, SP-20/M1 durable capture/publication/identity, SP-20/SP-13 M2 authorization/recovery, and SP-10/SP-11 M3 lifecycle/recovery. Default state is off.

---

## Mission

SP-21 defines an opt-in deterministic admission pipeline for a newly delivered result. It captures durably, verifies the capture/publication outcome, selects at most one compatible representation, produces a resolvable handle, and then performs a single permitted transformation. Failure of capture, parsing, policy selection, or handle resolution passes through the original result unless privacy policy denies delivery.

Fresh Qompack-owned retrieval responses are the first supported transformation surface, before delivery. Already-processed Qompack envelopes bypass repeat processing to prevent recursion. A tiny host schema allowlist is accepted only after target-specific parser/version/coexisting-hook evidence. Unknown schemas and parsers pass through. This plan does not redefine SP-20 identity/publication contracts.

## Design context (verbatim from Qompack.md)

The template heading is retained; v1.5 requirements are summarized without executable examples.

M4 follows M1–M3 because replacement is safe only when retained content remains durably captured and recoverable. The synchronous sequence is capture, durability verification, one deterministic representation decision, resolvable handle verification, and one transform. It never chains transforms, rewrites native history, or relies on native eviction/control.

The record preserves both structured and displayed meaning: status, stderr, diagnostics, interruption/media flags, input/output counts, signatures, source identity, source span/offsets where available, parser/schema version, fidelity, coverage, and the transformed display. A same-epoch prior delivery is not proof that a relative delta has a valid baseline. Self-contained capsules precede relative deltas.

Competing hooks are expected. The target contract must establish ordering, coexistence behavior, output schema, and idempotence marker behavior. An already-processed envelope marker bypasses repeat admission; fresh owned results remain eligible for their first transformation. Automatic replacement stays feature-switched off until the target gate passes; privacy denial follows the applicable privacy policy rather than an optimization fallback.

## Out of scope

- Redefining M1 capture/object/publication identities, M2 authorization, M3 recovery, or SP-13 retrieval behavior.
- Native-history rewriting, deletion of delivered results, cache-marker manipulation, compaction control, or automatic veto of manual compact.
- Broad parser support, a generic schema matcher, model/API dependencies, or untested host output forms.
- Replaying commands, bypassing denied reads, or treating hashes as authorization.

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

## Implementation spec

### IS-21-01: feature switch and target allowlist

Ship disabled. Enable only for Qompack-owned results first, then a small enumerated host schema/version allowlist proven on a supported target. Parser dispatch uses exact schema/version matching and deterministic tie-breaking. Unknown values pass through.

### IS-21-02: deterministic pipeline

Perform capture, durability verification, single representation selection, handle-resolution verification, then one transform. Each stage produces an observable decision. A failure passes through unchanged unless privacy requires denial. Capture failure forbids pointer replacement.

### IS-21-03: fidelity-preserving representation

Retain status, stderr, diagnostics, interruption and media flags, counts, signature, identifying spans, structured content, and displayed content. Prefer verbatim retention when recovery cost is high. Unrecognized test formats, binary/multimodal payloads and unknown schemas pass through under privacy policy. A capsule is self-contained before any delta. Relative output requires a verified compatible baseline, and same-epoch delivery alone is insufficient proof. A changed failure signature, parser/version change or uncertain capture resets delta eligibility.

### IS-21-04: recursion and competing hooks

Distinguish fresh owned results from already-processed envelopes. Transform an eligible fresh response once, then mark and bypass repeat parser/admission work. Target testing covers competing hook order, repeated delivery, malformed output, duplicate markers, and another hook’s transformation. The admission record identifies the observed chain without claiming unobserved order.

### IS-21-05: recovery and rollback

Every emitted handle resolves under current authorization before delivery. When target/schema/recovery evidence fails, disable the feature switch and pass through. Before a new admission schema write, prove compatible reader support or take a verified backup; after a write, rollback validates compatible read or restores backup before re-enabling.

### Blockers and rollback

M1 durable publication, M2 authorization/recovery, M3 latest-usable lifecycle, target adapter evidence, and privacy policy are blockers. Rollback order is disable replacement, restore pass-through, verify old/new reader or backup, then preserve diagnostics and original captures for audit.

## Test plan (TDD)

No tests run in this planning pass. Future TDD starts with contract failures. Existing future commands are `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, and `go run ./tools/devtool plugin-validate`; none runs here. The host-adapter admission test entrypoint is proposed future work and requires target evidence before creation.

| ID | Future assertion |
|---|---|
| T21-SWITCH-01 | Default-off, explicit opt-in, and target-gate disable behavior |
| T21-PIPE-01 | Capture and publication verify before one transform/handle emission |
| T21-PASS-01 | Capture/parser/selection/handle failure passes original through unless privacy denies |
| T21-FIDELITY-01 | Status, stderr, diagnostics, interruption/media flags, counts, signatures, spans, structured/displayed forms survive |
| T21-BASELINE-01 | Self-contained capsule precedes delta; same-epoch prior delivery cannot prove a baseline |
| T21-RECURSE-01 | Fresh owned response transforms once, processed envelope bypasses, duplicate delivery is idempotent |
| T21-HOST-01 | Exact schema/version allowlist, unknown parser pass-through, and competing hooks matrix |
| T21-RECOVERY-01 | Every emitted handle resolves under authorization; denied/unavailable remain visible |
| T21-QUALITY-01 | Supported task completion/constraints/recoverability compared to unmodified output on controlled held-out tasks; failures and uncertainty retained, no cost-only win |
| T21-ROLLBACK-01 | Disable/pass-through and compatible-reader-or-backup rollback before/after schema write |

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Run schema/parser/fidelity/budget and pass-through/recursion cases as short independent groups on frozen admission contracts. Resolve actual test names after the proposed package exists. Test capture ordering and authorized handle resolution against real SP-20/SP-13 seams before drawing integration conclusions.

**When broader checks are necessary.** Reserve competing-hook/installed-host, pre/post-write rollback and controlled unmodified-output comparisons for the integrated opt-in candidate. Every T21 gate remains required for enablement, including quality and missing-baseline failure cases; isolated unit success does not enable admission.

The implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Future six-commit proposal, all unchecked and requiring separate authorization.

- [ ] Commit 1 — `test(admission): specify target schema and pass-through contracts` for T21-SWITCH-01, T21-PASS-01, and T21-HOST-01.
- [ ] Commit 2 — `feat(admission): record verified capture and deterministic decisions` using SP-20 adapters without redefining identity/publication.
- [ ] Commit 3 — `fix(admission): preserve delivered result fidelity` for capsule/delta, structured/displayed, signature, count, and span behavior.
- [ ] Commit 4 — `feat(admission): emit one resolvable representation` after M2/SP-13 authorization/recovery gate.
- [ ] Commit 5 — `fix(admission): bypass processed envelopes and coexist with hooks` for recursion/idempotence/ordering evidence.
- [ ] Commit 6 — `test(admission): prove opt-in lifecycle and rollback` for target gating, privacy, recovery, T21-QUALITY-01 comparison against unmodified output, and backup/reader rollback.

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
| A capture boundary | Opus 4.8 / high | Apply the already-accepted publication adapter and prove ordering; no SP-20 redesign |
| B deterministic representation | Opus 4.8 / high | Exact schema/parsers, capsule fidelity and baseline eligibility within owned files |
| C recovery/rollback | Opus 4.8 / high | Distinguish denied/unavailable/resolvable outcomes and switch-off behavior |
| D host coexistence | Opus 4.8 / high | Design competing-hook and version cases; medium only for collation of actual target transcripts |
| Independent host/recovery reviewer | Fable 5.1 / high | Trace capture-before-replacement, authorized handle resolution, recursion and structured/displayed semantics across the assembled pipeline |

After main fixes shared types and test contracts, A/B can author disjoint slices; schedule C/D as slots become free and their inputs exist. Main integrates the single transformation pipeline and owns feature wiring. The final reviewer reads the integrated result and actual target/quality artifacts in a separate thread. Maximum three children total, one Fable at a time; do not parallelize a shared hook environment, golden fixture or enablement decision. Supported quality and recovery must pass before opt-in, irrespective of model choice.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] M1–M3 and target/privacy prerequisites are complete.
- [ ] Default-off switch, already-processed envelope bypass, and exact parser/version allowlist are proven.
- [ ] Every replacement follows capture, verification, one selection, resolvable handle, one transform.
- [ ] Failure passes through except documented privacy denial.
- [ ] Fidelity and self-contained-capsule-before-delta tests pass.
- [ ] Competing-hook and rollback evidence pass before enablement.

## Done checklist

### Planning-review checks

- [ ] This new plan preserves the requested heading order, M4/M1–M3 dependency boundary, and 5–8 future commit convention.
- [ ] It uses only existing future validation commands and labels the host entrypoint proposed.
- [ ] No source/config/test/fixture/runtime/Git work occurred in this planning pass.

### Future implementation checks

- [ ] T21-SWITCH-01 through T21-ROLLBACK-01 pass on named future entrypoints.
- [ ] The independent host-boundary review approves target evidence before the feature switch is enabled.

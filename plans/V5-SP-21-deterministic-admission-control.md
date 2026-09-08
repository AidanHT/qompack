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
| [x] 5. `fix(admission): bypass processed envelopes and coexist with hooks` | T21-RECURSE-01 and the T21-HOST-01 competing-hook matrix; recursion, idempotence and observed-order evidence |
| [x] 6. `test(admission): prove opt-in lifecycle and rollback` | T21-QUALITY-01 comparison against unmodified output and T21-ROLLBACK-01 backup/reader rollback; consolidates target gating, privacy and recovery artifacts |

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

- [x] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass. See [Implementation evidence](#implementation-evidence-2026-09-08); one timed-out `stubskips` run is recorded there as a timeout and not as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence. **Not applicable as executed and therefore unchecked:** no subagents ran. The A–D role split was not used; one implementer authored all six commits serially. The independent host/recovery review has not run and remains open.
- [ ] Parallel handoffs record accepted inputs, unique file/resource ownership and the shared SP-14–21 worker pool; provisional checks remain distinct from final M4 acceptance. **Unchecked:** no parallel handoff occurred. File-set disjointness against the three concurrently developed siblings was verified instead, per the evidence section.
- [ ] M1–M3 and target/privacy prerequisites are complete. **Unchecked:** M1–M3 are delivered and focus-tested, but the target prerequisite is not complete — B01 keeps installed host versions and permissions unverified.
- [x] Default-off switch, already-processed envelope bypass, and exact parser/version allowlist are proven. T21-SWITCH-01, T21-RECURSE-01 and the exactness half of T21-HOST-01; the allowlist is proven empty rather than proven populated.
- [x] Every replacement follows capture, verification, one selection, resolvable handle, one transform. The sequence and every refusal in it are tested; the transform itself is not performed by this package and no caller may act on `OutcomeTransform`, because the switch is refused.
- [x] Failure passes through except documented privacy denial. Every stage failure and both non-denial policy outcomes are covered.
- [x] Fidelity and self-contained-capsule-before-delta tests pass. T21-FIDELITY-01 and T21-BASELINE-01.
- [ ] Competing-hook and rollback evidence pass before enablement. **Half unchecked:** rollback evidence passes (T21-ROLLBACK-01). Competing-hook evidence is the explicitly accepted unverified disposition this plan permits, not an installed-host canary transcript, so it does not support enablement.
- [ ] T21-QUALITY-01 shows retained task completion, constraints and recoverability against unmodified output; a cost-only result does not enable admission. **Unchecked:** the comparison is inconclusive — there are no controlled held-out tasks and no observations. The second clause is enforced structurally: no field of `Comparison` or `LayerResult` can name a cost, so no arithmetic in the verdict can reach one.
- [x] Every X08 scenario has a real check and artifact, or an explicitly accepted blocked/disabled disposition that keeps the allowlist empty. Eight rows have real checks; T21-HOST-01 and T21-QUALITY-01 carry recorded dispositions, and both keep the allowlist empty and the switch off.

## Done checklist

### Planning-review checks

- [x] This new plan preserves the requested heading order, M4/M1–M3 dependency boundary, and 5–8 future commit convention.
- [x] It uses only existing future validation commands and labels the host entrypoint proposed.
- [x] Its milestone sections, invariants, scenario ownership, commit gates and rollback section match the SP-19/SP-20 house structure, and its requirement IDs trace to MIGRATION-EVIDENCE X08.
- [x] No source/config/test/fixture/runtime/Git work occurred in this planning pass. The implementation pass that followed is recorded separately below; this box is about the planning pass only.

### Future implementation checks

- [x] T21-SWITCH-01 through T21-ROLLBACK-01 pass on named entrypoints in `./internal/admission`. Two of the ten pass as recorded dispositions rather than as evidence: see the row table below.
- [ ] The independent host-boundary review approves target evidence before the feature switch is enabled. **Open.** The review has not run, and the switch is not enabled.

### Rollout, rollback and blockers

**Enablement order:** ship disabled; enable first for Qompack-owned retrieval responses only; add an enumerated host schema/version allowlist entry only after that exact schema and version produce a target canary transcript and a competing-hook observation. An unverified or unknown target keeps the allowlist empty and admission off. Enablement never follows from unit success, a cost result, or a provisional check on an intermediate snapshot.

**Rollback order:** disable replacement, restore pass-through, verify the compatible old/new reader or restore the verified backup, then preserve diagnostics and original captures for audit. Before any new admission schema write, prove a compatible deployed reader or take a consistent verified backup; after that write, repeat the drill against the actual new artifact. Never delete captures, diagnostics or evidence to make a retry look clean, and never silently downgrade a format. Disabling admission never disables capture, and a disabled pipeline still preserves the original result.

**Blockers.** M1 durable publication, M2 authorization/recovery, M3 latest-usable lifecycle, target adapter evidence and the privacy policy all block enablement. Ledger blockers apply directly: B01 installed host versions/permissions blocks the host allowlist and every target claim; B03 SP-20 durable recovery blocks all pointer replacement; B04 SP-13 retrieval blocks handle resolution; B08 routing and B09 concurrency govern future delegation. E09 keeps new-result replacement documented with an unknown installed target, E12 keeps native cuts, history rewriting and cache-marker controls unsupported, and E13 keeps injection/attribution/estimation/request control unverified. Failure of any prerequisite keeps admission disabled and the plan actionable; it never converts a pass-through into a delivery guarantee.

## Implementation evidence, 2026-09-08

Branch `feat/sp21-deterministic-admission-control`, worktree `../qompack-sp21`, stacked on
`feat/sp21-prerequisites` (which is itself off `develop` 7c735ac). Six commits, matching the commit
plan one to one. The package is `internal/admission`, foundation-only under
[00-ARCHITECTURE §3.2](00-ARCHITECTURE.md), reserved by the prerequisite branch before any
authoring, as the serial edge above requires.

**Execution deviated from the subagent strategy, and the deviation is the honest record.** No
subagents ran. The A–D role split, the shared worker pool and the independent reviewer seat were
not used; one implementer authored all six commits serially, contract-first. The role table above
is left unchanged because it describes what the plan proposed, not what happened. The consequence
that matters is that the independent host/recovery review has not run.

### Scenario status

| ID | Status | Evidence |
|---|---|---|
| T21-SWITCH-01 | Passing | The zero `Gate` refuses; opt-in admits an owned result; `Gate` structurally cannot name the recording, reinjection or experiment switches §7.1 keeps independent |
| T21-PIPE-01 | Passing | Ordered stage record over the capture and publication ports; capture failure, unverified publication and canonical-only fidelity each refuse separately |
| T21-PASS-01 | Passing | Every stage failure passes the original through; privacy denial is the only denial, and it survives the switch being off |
| T21-FIDELITY-01 | Passing | `Preserves` walks `Meaning` reflectively and is proven exhaustive by per-field mutation; binary/multimodal payloads select nothing |
| T21-BASELINE-01 | Passing | Capsule precedes delta; six reset causes recorded distinctly; baseline verification is unexported so a prior-delivery record cannot construct one |
| T21-RECURSE-01 | Passing | Own-marker and foreign-marker bypass before capture; idempotence proven as a mark-then-redeliver round trip |
| T21-HOST-01 | **Disposition** | Exact schema and version matching is proven. The competing-hook matrix is the explicitly accepted unverified disposition this plan permits: B01 keeps the installed host's hook order and output schema unobserved, so the allowlist stays empty. Recorded in `internal/contract/capability.go` and asserted by a test, so adding a host target without canary evidence fails |
| T21-RECOVERY-01 | Passing, with a stated limit | Denied, unavailable, uncertain and unknown each block the transform and stay visible; a denied handle is a pass-through, never a privacy denial. The seam is a port — the adapter binding it to real SP-13/M2 authorization is a composition-root task not done here, so the artifact's "against real SP-13/M2 authorization" clause is not satisfied |
| T21-QUALITY-01 | **Inconclusive** | The permitted alternative artifact. No controlled held-out tasks and no observations exist. The mechanism enforces it: zero observations are inconclusive rather than a pass, margins are predeclared by construction, the verdict is the worst layer, and cost has no field to live in |
| T21-ROLLBACK-01 | Passing | Fixed rollback order; pre-write compatible-reader-or-verified-backup proof with all-readers semantics; declared-downgrade requirement; post-write read-back-or-restore with evidence retained on every path, failures included |

### Runs

Focused per-commit runs on `./internal/admission` throughout, per R2. `go test ./internal/admission/`
holds **100% statement coverage** against the package's 90% floor at every commit. `go vet`,
`gofmt`, and `devtool lint --only=importgraph,nomagic,runpatterns,docmarkers,coveragefloors,sleepcheck`
all pass; `importgraph` reports 66 packages, confirming the foundation-only commitment holds with
`core` as the single non-stdlib import. The three ownership guards
(`TestAllStubsReturnNotImplemented`, `TestV1_StubGraphIsInertAndOwned`,
`TestStubRegistry_ListsEveryPackageOnDisk`) pass.

Every commit carried negative controls: the mechanism was broken, the run confirmed red, and the
break was reverted. Twenty-one controls across commits 3–6. Two of them did not compile on first
attempt and reported nothing; both were fixed and rerun rather than counted, because a control that
does not run looks exactly like a guard that works.

### Whole-tree validation

`go test -p 1 -timeout=30m ./...` on the branch tip: **64 packages ok, 3 packages failing**, four
tests in total. All ten `devtool lint` sub-checks pass, verified with a real exit code rather than a
piped one.

An earlier attempt at this run was wrong in three ways at once and is recorded because the shape
recurs: it ran concurrently with `devtool lint`, it piped `go test` through `grep`, and it capped the
result at `head -30`. The pipe made the shell report exit 0 while the suite was red, the cap hid most
of the failure list, and the co-load produced three `internal/negknow` timing breaches that do not
exist when the suite runs alone. A first `stubskips` run was also killed at a 400s timeout; it passes
in the serialized run. None of those three negknow failures is real, and none is in the counts above.

### Pre-existing failures, not caused by this work

All four surviving failures reproduce identically on clean `develop` 7c735ac, each verified with
`-run` under `-v` so that a pattern matching nothing could not be mistaken for a pass:

| Test | Package | On develop 7c735ac |
|---|---|---|
| `TestCarriedDefects_WaveReportRequiresResolution` | `test/guards` | Fails — SP05-D1, SP06-D2, SP08-D1 and SP10-D1 are `deferred:V4-VERIFY` while `V4-report.md` exists |
| `TestV3_HotPathUnchangedWithLedgerResident` | `test/e2e` | Fails — a gated hot-path budget breaches on the base itself |
| `TestIntegration_BeladyPMinLandsAtLowCoupling` | `test/integration` | Fails — the open Belady p_min item from V4 sign-off |
| `TestIntegration_HotPathWarmWithRealResidentState` | `test/integration` | Fails — same hot-path budget class |

This branch modifies neither `CARRIED-DEFECTS.tsv` nor `V4-report.md`, and adds nothing to
`test/integration`. It does add one file to `test/e2e`, so the e2e row was baselined on develop
specifically rather than argued from the diff. All four belong to V4 sign-off, not to SP-21.

### What this does not establish

Admission is **off**, and nothing here is an argument to turn it on. The feature switch ships
refused, the host allowlist is empty, the quality comparison is inconclusive, the competing-hook
matrix is an unverified disposition, the independent host-boundary review has not run, and no
composition root wires the pipeline — the ports have no adapters, so `internal/admission` has no
consumers. The commit plan is complete; the enablement gate is not, and the two were never the same
thing.

## Merge and integration

Everything in this section and the next describes work after the implementation pass, so both sit
below the evidence section rather than above it: the plan's proposal tense stops at
[Implementation evidence](#implementation-evidence-2026-09-08).

SP-21 **is** wave-4 work — [R3](MIGRATION-EVIDENCE.md) names the population as SP-15, SP-16, SP-14
and SP-21 — but it is outside the *ordered* chain. V5-VERIFY: "retain SP15→SP16→SP14 order and
evaluate SP21 separately after its M1–M3 prerequisites." Separately does not mean outside: V5-VERIFY
scopes SP-21 in through its M4 row, its exit criterion and its SP-21 enabled-surface matrix, and
`verify/v5` is cut from the verified integrated `develop`, so an SP-21 merge lands in that branch's
ancestry. The M1–M3 gate is **SP-20's** milestone gate, not a gate SP-21 owns.

### Branch state

| Branch | Tip | Ahead of develop | Worktree |
|---|---|---|---|
| `develop` | `7c735ac` | — | `../qompack-develop` |
| `feat/sp21-prerequisites` | `197d12d` | 6 | `../qompack-sp21-prereq` |
| `feat/sp21-deterministic-admission-control` | `a7d92eb` | 14 | `../qompack-sp21` |
| `feat/sp15-analyzer-selection-and-grammar` | `db4ec2e` | 19 | `../qompack-sp15` |
| `feat/sp16-phase7-refinements` | `03ba720` | 10 | `../qompack-sp16` |
| `feat/sp14-slash-commands-and-observability` | `154d2de` | 9 | `../qompack-sp14` |

### One merge, not two

`feat/sp21-prerequisites` is a strict prefix of `feat/sp21-deterministic-admission-control`:
`git merge-base --is-ancestor` between them is true, and the 14 commits are the 6 prerequisite
commits plus 8 authored on top — the 6 of the commit plan and 2 later docs commits. Merging the
admission branch therefore carries the prerequisite branch whole, and merging both produces a
redundant merge commit.

The prerequisite branch is worth merging **alone** in exactly one case: if the admission merge is
held pending the independent host-boundary review, its six commits still close V4 sign-off item 4
and half of item 3, which are useful without M4.

### Four mechanics that stop an operator

**`develop` is checked out in a sibling worktree.** The main repo is on `verify/v3`;
`git checkout develop` there fails with `'develop' is already used by worktree at .../qompack-develop`.
Merging a branch that another worktree has checked out is permitted — only `checkout` and
`branch -d` are blocked — so run merges with `git -C` against the develop worktree.

**Without `--no-ff` the hook never runs.** Every branch's merge-base is develop's own tip, so a plain
`git merge` fast-forwards, creates no commit, and silently ignores `-m`. A clean fast-forward is not
evidence that the subject was acceptable.

**The commit-msg hook rejects git's default merge subject.** `.git/hooks/commit-msg` shells
`go run ./tools/devtool check-commit-msg`, so Go must be on the merging shell's PATH. The subject
grammar is `^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.,-]+\))?: .{1,64}$`,
with a trailing period rejected separately. `Merge branch 'x' into develop` matches no type and is
refused. Three consequences: the **scope must be lowercase**, so `chore(SP-21)` fails where
`chore(sp21)` passes; a `feat`/`fix` subject additionally requires a `Refs:` footer, which a merge
does not need, so prefer `chore`; and body lines are capped at 100 runes.

**Attribution trailers are rejected on any line**, subject included: `co-authored-by`,
`signed-off-by` and `generated with` case-insensitively, plus the robot emoji. CI re-greps the whole
pushed range for the same patterns. A bare `Claude-Session:` line is not in that set and passes the
hook — it is excluded by this plan's "no attribution trailers" convention, not by the checker.

### Commands

Run in Git Bash; the POSIX forms below are parse errors in PowerShell. Confirm the preconditions
first — `git -C "$D" rev-parse --short HEAD` is `7c735ac` and `git -C "$D" status --porcelain` is
empty.

```sh
D=C:/Users/Quant/Documents/Programming/Projects/qompack-develop

# SP-21, on its own track. Carries feat/sp21-prerequisites.
git -C "$D" merge --no-ff feat/sp21-deterministic-admission-control \
  -m "chore(sp21): integrate deterministic admission control"
```

If the hook rejects the subject, the merge leaves `MERGE_HEAD` and a staged index with no commit:
re-commit with `git -C "$D" commit -m "chore(sp21): ..."`, or back out with
`git -C "$D" merge --abort`. Conflicts stop git on their own — resolve in the develop worktree, then
commit with a conforming subject. `--no-commit` only suppresses the auto-commit on a *clean* merge.
Develop is local-only and unpushed, so an unwanted merge undoes with
`git -C "$D" reset --hard 7c735ac` before any push, or `git -C "$D" revert -m 1 <merge-sha>` after.

The wave-4 chain that V5-VERIFY actually gates on is SP-15 → SP-16 → SP-14, in that order, with
validation between merges. Its recipe belongs to V5-VERIFY; it is named here only because SP-21
shares the two mechanics above. SP-15 and SP-16 both modify `docs/config-reference.md`,
`internal/config/defaults.go`, `internal/config/runtime.go`, `internal/config/validate_test.go` and
`testdata/golden/config/schema.json`, so the collision class to expect at the SP-16 merge is an
added-config-key clash and a stale golden, not logic.

### Validation, serialized

R3 bounds agent count and validation load separately for a recorded reason: the preceding corrective
session exhausted machine memory running the whole tree alongside its agents. B09 states the standing
rule — one heavy job per machine, quiet timing gates exclusive. After each merge run these in order,
each to completion:

| Step | Command | Why |
|---|---|---|
| 1 | `go build ./...` | Cheapest failure first |
| 2 | `go vet ./...` | Catches merge-shaped errors that still compile |
| 3 | `go test -p 1 -timeout=30m ./...` | The standing convention for the serialized whole-tree run |
| 4 | `go run ./tools/devtool lint` | All ten sub-checks, **after** the suite, never beside it |

Two traps, both of which produced wrong conclusions in this plan's own implementation pass:

- **Never pipe `go test` through `grep` or `head`.** The pipeline's exit code is the last command's,
  so the shell reports success on a red suite, and the cap truncates the failure list.
- **Co-load fabricates timing failures.** Running `devtool lint` beside the suite produced three
  `internal/negknow` timing breaches that do not exist serialized, and killed `stubskips` at 400s.

Re-baseline after each merge rather than predicting counts; SP-15 alone touches 88 files and the
package totals will not survive integration. What should stay constant is the *failure set*: the four
tests named in the pre-existing-failures table above fail on clean `develop` `7c735ac` and will still
fail after any merge here. A fifth failure is merge damage.

**Authorization.** The dispatch decision recorded above authorized *starting* SP-21. It does not
authorize merging it; that is a separate coordinator action. Merging carries the pending independent
host-boundary review forward — it does not satisfy it, and B08 forbids treating a skipped mandatory
review as discharged.

## Maximum-parallelism dispatch

### How to trigger it

`ultracode` is a keyword a **user types in a prompt**. This file containing the word triggers
nothing; a plan is read as content, never as a dispatch instruction. To fan the remaining work out,
paste a prompt of this shape:

```text
ultracode — execute the Maximum-parallelism dispatch table in
plans/V5-SP-21-deterministic-admission-control.md. One child per row, each in its own
worktree off a7d92eb, exclusive file ownership as listed, report to its own file.
```

The keyword also escalates reasoning effort. That escalation is authorized here for the adversarial
review seats only; blanket premium effort across mechanical rows is not, per R1.

### Remaining work

The six delivered commits are **not** in scope — they are done, and no subagent ran for any of them.
This dispatch is forward-looking only and does not retroactively parallelize delivered work. Four of
the six outstanding items in the closing paragraph of the evidence section are fannable; the refused
feature switch and the competing-hook disposition are coordinator decisions, not work units.

### Seats

R3 raised the coordinated SP-14–21 cap from three to **at most eight active implementation children,
one level deep, at most two Fable, no nesting**, and names SP-21 in its population. That pool is
**shared** with SP-14, SP-15, SP-16 and cooperating V4–V6 work; the budget below is the residual.
Confirm no other wave-4 plan has active children before dispatching, and reduce the count if it does.
V5-VERIFY still reads "at most three active children, one Fable" — that text predates R3 and is
superseded for agent count only. The serialization rule above is unaffected by R3.

R3's palette: Opus 5 high for multi-file judgment, **Opus 4.8 high for bounded single-package
slices**, Opus 5 low for mechanical collation, **Fable 5.1 high for adversarial review**.

| Unit | Seat | Exclusive ownership | Returns |
|---|---|---|---|
| A1 Privacy port adapter | Opus 4.8 / high | one new composition-root file | adapter and focused tests |
| A2 Capturer port adapter | **Opus 5 / high** | one new composition-root file | capture-before-transform ordering proof |
| A3 Publisher port adapter | **Opus 5 / high** | one new composition-root file | publication-verification binding |
| A4 Parser port adapter | Opus 4.8 / high | one new composition-root file | owned-schema parser and tests |
| A5 Resolver port adapter | Opus 4.8 / high | one new composition-root file | SP-13/M2 binding; carries serial edge 3 |
| A6 B01 target canary | Opus 4.8 / high | `test/canary` fixture only | transcript, or an unverified disposition |
| A7 Held-out task set | Opus 4.8 / high | new eval fixture only | observations for the existing `report.go` |
| B1 Host-boundary review | Fable 5.1 / high | its own report file | independent verdict on the assembled pipeline |
| B2 Methodology review | Fable 5.1 / high | its own report file | adversarial review of A7's design |

A2 and A3 keep **Opus 5 / high** because the plan's existing role table assigns the capture boundary
there as multi-file judgment. This table governs the remaining work, and supersedes that table only
for A1 and A4–A7, which are genuinely single-file slices.

**Adapters land at a composition root, never in `internal/admission`.** That package is
foundation-only under §3.2 with `core` as its single non-stdlib import, enforced by `importgraph`; an
adapter importing SP-13 or SP-20 from inside it breaks the guard. If the adapters need a *new*
package, that package is a coordinator-owned pre-slice — `plans/OWNERS.tsv`, §3.2, §6.4,
`test/guards/stubs_test.go`'s registry and `wantStubPackages`, and the §6.4 transcription in
`test/guards/v1_integration_test.go` must all name it first, mirroring serial edge 1. Note the naming
trap: `internal/cli/capture_admission.go` already exists and is unrelated hook-capture code.

A7 must not rebuild `report.go` — the predeclared-margin mechanism ships and is tested. The gap is
that no held-out observations exist. A7 produces observations and *proposes* rather than applies any
change to `report.go`. A6 likewise cannot edit the allowlist: a positive canary returns a handoff,
because the allowlist entry in `internal/contract/capability.go` is the enablement decision and stays
coordinator-owned and unparallelized.

### Added ordering constraints

The four edges in **Serial edges** above are development-order constraints. Edges 1 and 2 already
landed; edge 3 is satisfied by commit 4, and A5 now carries the M2/SP-13 dependency. This dispatch
adds three edges those four do not cover, so that paragraph's "only" is scoped to development order:

1. Any new-package pre-slice precedes A1–A5.
2. B2 follows A7 — it reviews A7's methodology.
3. B1 follows the coordinator's composition-root wiring, because edge 4 requires the *assembled*
   pipeline, not a per-slice review.

Merges are serial. Heavy validation is serial. Only authoring fans out.

### Preconditions

R3 is explicit that the raised cap holds only while its controls hold, so all five are required:
exclusive file ownership per child; a contract-first slice landed before fan-out; report-to-file with
short structured returns, each unit writing **only** its own report path; a per-unit tool-call budget
with a stop-and-report-BLOCKED rule after three identical failures; and heavy validation serialized
at one command per machine.

Children may run focused checks — `go test ./internal/admission/`, and `go vet` on owned packages.
The whole-tree suite, `devtool lint` and any timing gate are coordinator-only. Every child's brief
must name the four known pre-existing failures, or a child running a broad suite will report a false
BLOCKED. Briefs also carry the `git stash` prohibition (the stash list is shared across every
worktree of one repository), the zero-match `go test -run` trap, and the commit rules above.

### Claims this dispatch may not make

B08 forbids claiming observed routing: model identities are requested, and effective routing is not
exposed. No speedup, cost saving or measured-efficiency claim follows from running this in parallel.
Nothing here enables admission.

# SP-10: L4 checkpointer — durable evidence checkpoints and bounded recovery preparation

**Status:** M3 planning correction. Planning owner: Writer B using gpt-5.6-terra at medium effort. A future implementation owner acts only after the gates named here. This plan authorizes no implementation, test execution, Git action, or merge.

**Branch:** `feat/sp10-checkpointer-l4` | **Wave:** 3, original implementation user-reported complete; migration corrections remain future work | **Planning dependencies:** SP-19 M0-00 first integrates completed SP-10–13 and accepts M0-G0; remaining SP-19/M0, SP-20/M1 and M2 state/recovery, then SP-13/M2 gate corrective recovery enablement. These remediation dependencies do not require repeating the original delivery before it can be merged.

---

## Mission

SP-10 owns the M3 lifecycle contract: build immutable checkpoints from durable Qompack evidence, publish only checkpoints whose dependencies resolve, and make the latest usable checkpoint available to SessionStart without waiting for host post-compaction activity.

Waves 0–2 and the user's later completion report for original SP-10–13 are preserved. The implementation worktree at `../qompack-sp10` was observed at `bd4a42d2d230b46800b964b150562d5418ddb549` during the initial in-progress snapshot; M0-00 refreshes the actual delivery tip before merging. Root stubs are not an absence-of-work signal. Subsequent M0 reconciliation identifies only the corrective remainder against the accepted combined baseline.

The frozen checkpoint schema remains a compatibility input. Future work adds versioned compatible readers and adapters, retains old fixtures and identities, and never resets the schema or silently changes hash/error meaning.

A checkpoint separates immutable captured evidence from normalized text, summaries, extracted decisions, focus text, and reports, which are derivatives. Available provenance, transform version, fidelity, coverage, scope, authority, conflicts, event order, arrival order, and uncertainty travel with those derivatives. It never claims complete native history or model compliance.

The previous native O(delta) and guaranteed first-turn-saving claims are withdrawn. Native summarization receives conversation history according to approved evidence. Qompack can measure bounded maintenance of its own durable state, but cannot claim it changes native summarization input, native cuts, cache behavior, or first-turn cost without target-specific evidence.

Read-only source evidence grounds the reconciliation: sibling `internal/checkpoint/precompact.go` exposes `FileWriter.PreCompact`; `focus.go` exposes `FocusInstructions`; `finalize.go` exposes `FileWriter.Finalize`; `reader.go` exposes `Latest`, `Get`, `Chain`, and `Verify`; `migrate.go` exposes `Migrate`; `writer.go` exposes `Begin`, `Advance`, and `Abort`; and `internal/daemon/wire_checkpoint.go` exposes `BindCheckpoint` and `WireCheckpoint`. Existing tests include `internal/checkpoint/precompact_test.go`, `reader_test.go`, `migrate_test.go`, `writer_test.go`, `writer_paths_test.go`, and `test/e2e/checkpoint_test.go`. These are implementation evidence to reconcile, not proof that M3 gates have passed.

### Current planning owner and future implementation owner

| Responsibility | Owner and boundary |
|---|---|
| M3 plan and requirements | Writer B, Markdown-only planning owner |
| Inventory, target adapter evidence, branch reconciliation | SP-19/M0 owner |
| Durable capture/publication/migration | SP-20/M1 owner |
| State, authority, conflicts, and recovery prerequisites | SP-20/M2 owner |
| Pointer identity, authorization, and retrieval compatibility | SP-13/M2 owner |
| Existing SP-10 worktree implementation | Future SP-10 implementation owner, after M0–M2 gates |
| SessionStart recovery presentation | SP-11 owner, coordinated through the M3 gate |

## Design context (verbatim from Qompack.md)

The existing template heading is retained; current v1.5 requirements are summarized without obsolete executable examples.

Importance ordering remains a policy: tier 1 contains essential durable material, tier 2 contains decisions and active work, and tier 3 contains recoverable handles and residue. It is a declared reconstruction policy, not proof that arbitrary truncation is optimal.

The immutable evidence boundary is captured host payload, which may be partial, redacted, truncated, unsupported media, or absent. A derivative cannot upgrade it to complete process/file output. Existing field order and frozen fixtures remain compatibility obligations. Compatible readers map missing legacy information to explicit unknown states; they never invent completeness.

### Host boundary correction

Evidence E10 in `plans/MIGRATION-EVIDENCE.md` says PreCompact receives `custom_instructions` as input and PostCompact supplies `compact_summary`. SP-10 must not set a `customInstructions` output field or depend on an unsupported output setter.

PreCompact may create/finalize a local checkpoint within a bounded timeout and may record supplied instruction input as context when policy permits. It cannot promise to alter host instructions, history, compaction boundaries, cache markers, or delivered results.

PostCompact is optional observation. It may store a delivered compact summary as a qualified derivative with conservative correlation. SessionStart recovery never waits for PostCompact; it selects the latest usable published checkpoint under a local deadline.

### Lifecycle model

The committed frontier is durable event order, not guessed host history. It can include explicit gaps, in-flight work, retryable failures, abandoned attempts, and unknown relationships. Arrival order is stored separately when different.

Each PreCompact attempt has a locally generated attempt identity. It may correlate with host data when supported but never fabricates a host ID. Qompack injections and wrappers are excluded from independent primary evidence and remain traceable derivatives.

Publication verifies durable evidence before committing references and the frontier. Capture/verification failure preserves the prior usable checkpoint and writes a lifecycle diagnostic. Under deadline/cancellation/partial input, implementation finalizes the latest internally consistent usable checkpoint where possible; it does not wait for unbounded analysis.

### Budget and coverage model

Accounting counts the assembled representation: records, wrappers, handles, reports, and diagnostics, using a named estimator and version. Estimates and missing telemetry remain qualified as unknown where applicable.

Tiny/zero budgets and oversized critical records emit explicit overflow rather than silently discarding tier 1 or claiming complete recovery. Diagnostics identify blocking class, serialized/estimated size, estimator, affected evidence, recovery path, and coverage. Essential exact exceptions or small spans remain allowed where policy permits.

Coverage states are `qompack-included`, `archive-only`, `native-load-observed`, `expired-or-deleted`, and `unknown`, with timestamps/epochs and fidelity. Native-load observation proves neither exact bytes nor completeness.

## Out of scope

- Implementation, tests, benchmarks, builds, probes, generators, installations, merges, or rebases in this planning pass.
- Recreating SP-10 from root stubs or replacing reconciled worktree progress.
- Native history rewriting/eviction, cache-marker manipulation, compaction triggering, or automatic compaction veto.
- A PreCompact instruction-output setter, a native delta-summarization claim, or a guaranteed first-turn-saving claim.
- Waiting for PostCompact, fabricated host IDs, schema reset, fixture rewrites, or unversioned incompatible reads.
- SP-12 scheduling, SP-13 authorization policy, SP-20 capture/state remediation, or SP-11’s final presentation.

## Interface contract

### Consumes

| Contract | Required use |
|---|---|
| Frozen checkpoint/pins schema and fixtures | Compatible reader/adapters; retained fixture behavior |
| M1 durable evidence/publication | Reference only verified durable evidence; preserve provenance/fidelity |
| M2 state | Preserve authority, scope, conflict, supersession, dependencies, uncertainty |
| SP-13 recovery contract | Required before pointer recovery; unresolved/denied stays diagnostic |
| PreCompact host adapter | Treat custom instructions as input only; create bounded local attempt |
| PostCompact adapter | Optional derivative observation; absence is normal |
| SessionStart adapter | Requests latest usable checkpoint with a local deadline |

### Produces

| Output | Contract |
|---|---|
| Immutable checkpoint | Versioned record with identities, derivative provenance, lifecycle, coverage, accounting, overflow, recovery diagnostic, tier order |
| Published frontier | Advances only with verified dependencies; represents gaps and in-flight work |
| Latest usable selection | Bounded verified fallback to prior usable checkpoint; no PostCompact wait |
| Local attempt record | Local ID, conservative correlation, deadline outcome, diagnostic; never a fabricated host ID |
| Compatible reader | Explicit migrations for supported old versions; safe diagnostics for unsupported/corrupt input |
| Optional PostCompact derivative | Qualified summary observation only |
| Recovery handoff | Qualified result to SP-11 only after SP-13/M2 pointer gate |

### Required invariants

1. Primary evidence is immutable and never replaced by a derivative or prior injection.
2. Publication follows durable dependency verification.
3. Failed/unknown/denied/corrupt input remains uncertainty or a recovery diagnostic.
4. Qompack-owned injection/wrapper content cannot become independent primary evidence.
5. Compatibility is reader-led, additive, and retains old fixtures/identities.
6. Outside-budget cases emit overflow plus coverage/recovery diagnostics.
7. Latest-usable selection is bounded and PostCompact-independent.

## Implementation spec

This is a future specification. The implementation owner first reconciles the SP-10 worktree against M0 and classifies existing code as retained, adapted, deferred, or removed with evidence and migration consequences.

### IS-10-01: compatible reader and migration

Retain the frozen shape and provide explicit compatible migrations for provenance, lifecycle, coverage, accounting, and diagnostic additions. Legacy omissions read as legacy-unknown. Future versions fail without mutation. Retain originals so migration rollback is verifiable.

### IS-10-02: evidence and state separation

Assemble from M1 durable evidence and M2 state. Label user requirements, explicit decisions, tool observations, agent hypotheses, and extracted candidates distinctly. Derivatives cite source identities and transform versions; decision extraction remains a candidate until authority rules say otherwise.

### IS-10-03: frontier and publication

Maintain committed frontier separately from arrival order and in-flight state. Verify referenced durable objects before publishing references/frontier. Failures retain the prior usable checkpoint and record diagnostic state.

### IS-10-04: bounded lifecycle

PreCompact creates a local attempt and runs bounded local work. Deadline pressure selects latest internally consistent state or reports failure. It neither synthesizes host summaries nor uses an output setter. PostCompact attaches only under conservative correlation. SessionStart immediately queries the local usable chain.

### IS-10-05: own-injection exclusion

Retain tagging/stripping only for Qompack-owned material. Exclude it before primary-evidence extraction, preserve its provenance, and do not delete unrelated text merely resembling a marker.

### IS-10-06: accounting and overflow

Serialize complete records by declared tier policy, estimate the assembled output including overhead, and preserve essential exact exceptions when policy allows. When unfit, record explicit overflow/recovery/coverage diagnostics; do not use arbitrary cuts or completeness claims.

### IS-10-07: pointer recovery gate

Before SP-13/M2 confirms identity, authorization, expiry, and error distinctions, pointers are archive-only diagnostics. After the gate, resolved, denied, expired-or-deleted, unavailable, corrupt, and unknown remain distinct for SP-11.

### IS-10-08: maintenance, crash, and rollback

Idle maintenance has cancellation, quotas, bounded queues, dirty-state recovery, and Qompack-local measurements. Publication/restart handling identifies in-flight attempts, preserves corrupt/partial artifacts for diagnosis, and never advances past durable verified evidence.

Before any newly versioned checkpoint is written, the future owner must demonstrate either that the deployed reader is compatible with both old and new records or that a verified backup of all affected checkpoint artifacts and manifests can restore the prior reader. After a new-schema write, rollback verifies that the prior reader remains usable through the compatible-reader path or restores the verified backup before exposing recovery. Rollback never deletes evidence to make a retry appear clean. If the frozen interface must change, the future owner follows the architecture amendment path `arch/<reason>` from `develop` and lands it before dependent work.

### IS-10-09: mandatory lifecycle scenarios

The future lifecycle suite must give each scenario an observable artifact state, frontier state, attempt state, coverage/overflow diagnostic where applicable, and SessionStart recovery result:

| Scenario | Required observable result |
|---|---|
| Repeated compaction | Consecutive attempts create distinct local identities; usable chain/fallback remains verifiable |
| Resume after restart | In-flight/draft state is classified and the last published usable checkpoint remains selectable |
| Forked session | Shared ancestry and per-session frontier are distinct; no cross-session fabricated correlation |
| Manual compaction | User-requested attempt is recorded and never vetoed for optimization |
| Automatic compaction | Adapter absence or unsupported trigger leaves a diagnostic and does not imply native control |
| Failed capture/publication | Prior usable checkpoint and committed frontier remain unchanged; failure is visible |
| Duplicate delivery | Distinct host events remain distinct when known; duplicate arrival does not corrupt publication |
| Out-of-order delivery | Arrival/event order divergence is retained and recovery chooses only verified state |
| Missing event/relationship | Gap or unknown relationship is represented without invented identifiers |
| Empty evidence | Empty record is explicit and does not become a completeness claim |
| Oversized critical evidence | Overflow identifies excluded/retained material, estimator, coverage, and recovery route |
| Unavailable recovery target | Denied, expired-or-deleted, unavailable, corrupt, and unknown outcomes remain distinct |

## Test plan (TDD)

No tests run in this planning pass. Future TDD records a failing contract test before compatible implementation and a passing result after integration. Existing future commands are `go run ./tools/devtool test`, `go test ./internal/checkpoint ./internal/pins ./test/e2e`, and `go test ./internal/checkpoint/checkpointtest ./internal/pins/pinstest`; none is executed here. A host-adapter lifecycle entrypoint is proposed future work, not an existing command, and requires target evidence before creation.

| ID | Future gate |
|---|---|
| T10-COMPAT-01, revised from `TestGoldenCheckpointRoundTrip` | Old fixtures decode through compatible reader and expose legacy unknowns |
| T10-COMPAT-02, revised from `TestMigrateRejectsFutureVersion` | Future version fails without mutation; migration retains identity and rollback evidence |
| T10-EVIDENCE-01, revised from `TestSourceSetCarriesNoText` | Primary-evidence path cannot accept live history/injection as independent evidence |
| T10-DPI-01, revised from `TestAdvanceIsDPIGuarded` | Derivatives cannot replace primary evidence; own-injection exclusion is observable |
| T10-FRONTIER-01 | Frontier advances only after dependency verification and preserves event/arrival order distinction |
| T10-FRONTIER-02 | Gap/in-flight/failure preserves prior usable checkpoint with diagnostic |
| T10-LIFE-01, revised from `TestPreCompactFinalizesAsIsNearDeadline` | Deadline yields bounded latest usable result or explicit diagnostic |
| T10-LIFE-02 | SessionStart selects usable checkpoint without PostCompact under deadline |
| T10-LIFE-03 | Local attempt correlation never fabricates host identity |
| T10-HOST-01, revised from `TestFocusContainsSentinel` | PreCompact treats custom instructions as input and emits no unsupported output setter |
| T10-HOST-02 | PostCompact summary remains qualified derivative and cannot alter primary evidence |
| T10-ACCOUNT-01 | Accounting includes record/wrapper/handle/report/diagnostic overhead under named estimator |
| T10-ACCOUNT-02 | Zero/tiny budget and oversized tier-1 create explicit overflow/recovery/coverage diagnostic |
| T10-COVER-01 | Coverage, timestamps/epochs, fidelity, and legacy-unknown values round-trip |
| T10-POINTER-01, revised from `TestValidatePointersMissingFile` | Pre-gate pointer is archive-only and cannot enable recovery |
| T10-POINTER-02 | Post-gate resolution/denial/expiry/unavailable/corruption/unknown remain distinct |
| T10-CRASH-01 | Object/index/publication crash cannot publish unresolved frontier; restart retains predecessor |
| T10-ROLLBACK-01 | Compatible-reader rollback retains artifacts without data loss |
| T10-MAINT-01, revised from `TestCadenceFinalizesWhenDraftReachesBudget` | Maintenance obeys cancellation/quota/queue/recovery and makes no native-effect claim |
| T10-LIFE-04 | Repeated compaction, resume, fork, manual, automatic, failure, duplicate, out-of-order, missing, empty, oversized, and unavailable-recovery scenarios produce the required observable results |

### Named unresolved verification actions

| Action | Owner | Evidence required |
|---|---|---|
| V10-01 | SP-19/M0 | Reconcile SP-10 worktree state and ownership |
| V10-02 | SP-19/M0 | Target-tested PreCompact input and optional PostCompact adapter behavior |
| V10-03 | SP-20/M1 | Capture/publication failure and external object/index crash boundary |
| V10-04 | SP-20/M2 | State authority/conflict/scope/fidelity/coverage/uncertainty inputs |
| V10-05 | SP-13/M2 | Pointer identity, authorization, recovery, expiry, and error distinctions |
| V10-06 | SP-10/SP-11 | SessionStart no-PostCompact, timeout, crash, and rollback drill |
| V10-07 | SP-10/SP-12 | Local maintenance cost/calibration separated from native claims |

## Commit plan

This is a future eight-commit proposal. Original Commit 1–8 remain historical plan identifiers; M0 alone determines whether any corresponding work was completed. These numbered remediation commits do not replay or infer completion of that history. The subjects use architecture-supported conventional types and must be made only under the globally required separate future authorization.

| Proposed commit | Historical area mapped | Contract and test first | Compatible implementation | Validation/evidence and rollback |
|---|---|---|---|---|
| 1. `fix(pins): preserve checkpoint input provenance` | Commit 1 pins | Add unchecked M1 provenance/fidelity contract tests around `internal/pins/pins.go` and `store.go` | Adapt pin input records only through compatible fields/adapters | Validate retained pins fixtures; verify compatible reader or backup before any new write |
| 2. `test(checkpoint): specify legacy reader migration` | Commit 2 schema/migration/injection | Add T10-COMPAT-01/02 around `types.go`, `migrate.go`, `migrate_test.go`, and frozen fixtures | Implement compatible reader mappings, never a schema reset | Prove old/new reads and rollback to prior reader/verified backup |
| 3. `feat(checkpoint): publish verified frontier state` | Commit 3 source/draft/frontier | Add T10-FRONTIER-01/02 against `source.go`, `writer.go`, and `writer_paths_test.go` | Publish only durable dependencies; represent gaps/in-flight/arrival order | Crash-boundary evidence; rollback preserves predecessor and manifest |
| 4. `fix(checkpoint): qualify extracted state and injections` | Commit 4 decisions | Add T10-EVIDENCE-01 and T10-DPI-01 around `decisions.go`, `inject.go`, and their tests | Preserve derivative provenance, authority/conflict/uncertainty, own-injection exclusion | Verify no primary-evidence replacement; rollback preserves original evidence |
| 5. `feat(checkpoint): account for overflow and gated pointers` | Commit 5 truncation/pointers | Add T10-ACCOUNT-01/02 and T10-POINTER-01 around `truncate.go`, `validate.go`, and fixtures | Whole-record accounting; archive-only pointers until SP-13/M2 gate | Verify overflow diagnostics and disabled recovery; no new schema write without compatibility/backup |
| 6. `fix(checkpoint): select latest usable recovery` | Commit 6 finalize/manifest/reader | Add T10-LIFE-02, T10-CRASH-01, and T10-ROLLBACK-01 around `finalize.go`, `reader.go`, `reader_test.go` | Bounded verified fallback and restart recovery | Exercise corrupt/missing chain and pre/post-write rollback path |
| 7. `fix(checkpoint): remove unsupported focus output dependency` | Commit 7 focus | Add T10-HOST-01/02 around `focus.go`, `precompact.go`, and `precompact_test.go` | Treat PreCompact instructions as input; record optional PostCompact derivative | Target adapter evidence; missing host entrypoint remains proposed, with safe no-observation fallback |
| 8. `feat(daemon): wire bounded checkpoint lifecycle` | Commit 8 daemon wiring | Add T10-LIFE-01/03/04 and T10-MAINT-01 around `internal/daemon/wire_checkpoint.go` and `test/e2e/checkpoint_test.go` | Local attempts, timeout, cancellation, and lifecycle scenarios without native-control claims | Target-tested lifecycle evidence; rollback removes enablement before data migration rollback |

Integration order is 1 through 8 after M0–M2 gates. Any interface change first follows `arch/<reason>` from `develop`, lands before the affected commit, and is then consumed by the existing `feat/sp10-checkpointer-l4` worktree.

## Subagent strategy

No future implementation subagents are authorized now; current planning delegates are recorded separately. After M0 reconciliation and separately authorized future implementation, the implementation owner keeps the branch, final integration, lifecycle files, fixtures, and commits. The original A–D ownership is retained where compatible:

| Future role | File set | Integration order |
|---|---|---|
| A — pins/schema compatibility | `internal/pins/pins.go`, `store.go`; `internal/checkpoint/schema.go`, `migrate.go`, `obs.go`, `source.go`, `inject.go` and their tests | Supplies commits 1–2; no frozen schema redefinition |
| B — qualified decisions | `internal/checkpoint/decisions.go` and its tests | Supplies commit 4 after M2 contract is available |
| C — accounting/pointers | `internal/checkpoint/truncate.go`, `validate.go`, `gitindex.go` and their tests | Supplies commit 5 after M1 and before recovery enablement |
| D — reader/focus | `internal/checkpoint/manifest.go`, `reader.go`, `focus.go` and tests | Supplies commits 6–7 after compatible reader agreement |
| Main implementation owner | `draft.go`, `writer.go`, `finalize.go`, `precompact.go`, `checkpoint.go`, `internal/daemon/wire_checkpoint.go`, shared fixtures, integration tests | Integrates 3, then 6–8; owns branch and all commits |
| Independent lifecycle reviewer | Read-only review of PreCompact, PostCompact, SessionStart, crash/rollback evidence, and T10-LIFE-01 through 04 | Reviews after commit 8 before any enablement decision |

The reviewer is independent of the implementation owner and reports lifecycle evidence rather than changing code. No role recreates root stubs; each works from the reconciled SP-10 worktree.

## Exit criteria

All criteria are future and unchecked.

- [ ] V10-01 through V10-05 are complete with published interfaces and fallbacks.
- [ ] SP-10 worktree progress is reconciled as retained, adapted, deferred, or removed.
- [ ] Compatible readers retain fixtures/identities and expose legacy uncertainty without schema reset.
- [ ] Publication verifies dependencies; gaps/failures/corruption/in-flight work preserve a usable predecessor and diagnostic.
- [ ] PreCompact has no output-setter dependency; PostCompact is optional/non-blocking.
- [ ] SessionStart selects latest usable verified checkpoint under deadline without PostCompact.
- [ ] Own injections/wrappers remain excluded primary-evidence derivatives.
- [ ] Complete accounting produces explicit overflow/recovery/coverage diagnostics.
- [ ] Pointer recovery stays disabled until SP-13/M2 gate, then remains qualified.
- [ ] Crash, restart, cancellation, lifecycle, compatibility, and rollback gates, including all IS-10-09 scenarios, pass on named future entrypoints and supported target evidence.
- [ ] Maintenance evidence is Qompack-local and makes no native-effect/model-compliance claim.

## Done checklist

### Future implementation checks

**These unchecked checks remain the implementation handoff, alongside the Exit criteria and T10 gates.**

- [ ] Writer B using gpt-5.6-terra medium remains planning owner and future implementation owner is separately identified.
- [ ] `feat/sp10-checkpointer-l4` and original Commit 1–8 persist as audit references; remediation is distinct.
- [ ] SP-19/M0, SP-20/M1, SP-20/M2, and SP-13/M2 dependencies precede pointer recovery.
- [ ] Compatible versioned reader/adapters retain frozen schema, fixtures, and rollback evidence.
- [ ] Evidence/derivatives, authority/conflicts, scope/uncertainty, coverage/fidelity remain distinct.
- [ ] Frontier distinguishes committed/gap/in-flight/event/arrival states; local attempts never fabricate host IDs.
- [ ] PreCompact uses supported input semantics; PostCompact is optional; SessionStart never waits indefinitely.
- [ ] Whole-representation accounting and overflow preserve recovery diagnostics for outside-budget evidence.
- [ ] No implementation code, executable snippet, unsupported native O(delta)/first-turn claim, arbitrary truncation directive, or false completeness claim remains.

### Planning-review checks

- [ ] This planning pass changed only this Markdown file and ran no tests/builds/benchmarks/probes/generators/installers/Git mutations.

# SP-11: L5 rehydrator — qualified SessionStart recovery and bounded additional context

**Status:** M3 planning correction. Planning owner: Writer B using gpt-5.6-terra at medium effort. Future implementation requires the globally separate authorization and M0–M2 gates.

**Branch:** `feat/sp11-rehydrator-l5` | **Wave:** 3, original implementation user-reported complete; migration corrections remain future work | **Dependencies:** SP-19 M0-00 first integrates completed SP-10–13 and accepts M0-G0; then remaining SP-19/M0 reconciliation, SP-20/M1 durable publication, SP-20/M2 state/authority/recovery, SP-10 M3 lifecycle and SP-13/M2 pointer authorization/resolution gate corrective recovery enablement. Preserve and merge the original delivery without treating these follow-up tasks as unfinished original implementation.

---

## Mission

SP-11 consumes the latest usable checkpoint at SessionStart and assembles bounded additional context for recovery. It restores qualified Qompack evidence, current authorized state, an explicit coverage/drop/overflow report, and retrieval affordances. It does not rewrite native history, prove native load completeness, or wait for PostCompact.

The current authority controls active recovery. A captured original intent remains immutable evidence, but a later authenticated user correction, cancellation, scope restriction, or conflict resolution supersedes it as active instruction. The rehydrated output displays both only where policy permits and labels the older record as superseded rather than treating it as current.

Sibling source evidence shows existing implementation progress: `../qompack-sp11/internal/rehydrate/build.go`, `items.go`, `budget.go`, `render.go`, `drops.go`, and `standing.go`; `internal/rules/scanner.go`; `internal/skills/indexer.go`; and `internal/daemon/rehydrate_service.go`. Existing test evidence includes their package tests, `internal/rehydrate/rehydratetest`, `internal/rules/rulestest`, `internal/skills/skillstest`, and daemon rehydration tests. This is implementation to reconcile after M0, not a completed-gate claim.

## Design context (verbatim from Qompack.md)

The template heading is retained; v1.5 requirements are summarized without obsolete executable examples.

The earlier eight-item ordering is retained as an importance policy: authorized requirements/invariants; current authoritative intent; qualified eliminations and decisions; current work; resolvable handles; report; and retrieval notice. The final representation is bounded and transparent about what it did not include.

Complete accounting includes every assembled byte/token contribution: wrapper and injection metadata, section labels, handles, coverage/drop/overflow/recovery diagnostics, and report overhead. Use an identified estimator and retain its version/calibration status. If a minimum or maximum budget cannot accommodate an essential record, emit explicit overflow and recovery diagnostics with coverage/fidelity; do not silently cut it or claim the context is complete.

SessionStart compact recovery requests the latest verified usable checkpoint under a bounded local deadline. It must work when no PostCompact event arrives. A delivered PostCompact summary is optional derivative evidence and never the prerequisite for recovery. Asynchronous native load events may establish only observed delivery; they cannot prove exact bytes, absence, native completeness, or model compliance.

Instruction restoration is scope-tested. Path rules or nested instruction files are eligible only when M2 policy and verified scope evidence authorize them. A pointer to a file never automatically injects its directory or a directory-level instruction file. Unknown, denied, expired, conflicting, or unverified scope produces a report entry and a retrieval affordance rather than an injected instruction.

## Out of scope

- Writing/publishing checkpoints, committed-frontier ownership, or PreCompact lifecycle, owned by SP-10/M3.
- M1 capture/publication/identity and M2 authority/state contracts, owned by SP-20.
- Pointer authorization and retrieval policy, owned by SP-13/M2.
- Native history rewriting, native cut/cache control, automatic compaction veto, or waiting for PostCompact.
- Directory-wide or pointer-directory auto-injection of instructions.
- New-result replacement/admission, owned by SP-21/M4.

## Interface contract

### Consumes

| Input | Required treatment |
|---|---|
| SP-10 latest usable checkpoint | Verify reader result and lifecycle/coverage diagnostics before assembly |
| SP-20 evidence/state | Preserve identity, provenance, authority, conflict, supersession, fidelity, coverage, scope, and uncertainty |
| SP-13 pointer resolution | Use a handle only after identity/authorization result; preserve denied/unavailable distinctions |
| SessionStart compact adapter | Bounded local recovery request; no PostCompact prerequisite |
| Rule and skill sources | Explicit scope and parser-version checks; unknown parser/scope never injects |
| Token estimator | Counts full assembled representation and records estimator identity |

### Produces

| Output | Contract |
|---|---|
| Additional context | Bounded, tagged Qompack derivative with section accounting and no native-completeness claim |
| Recovery report | Included/archive-only/native-load-observed/expired-or-deleted/unknown coverage plus fidelity, overflow, dropped items, unresolved handles, and fallback |
| Instruction result | Only explicitly authorized, scope-tested rules; no pointer-directory inference |
| SessionStart result | Latest usable verified checkpoint or a bounded diagnostic/fallback |
| Drop reporter handoff | Qualified report for SP-13, never an authority or availability substitute |

### Required invariants

1. Current authority wins over obsolete intent while older evidence stays traceable.
2. Every included derivative records provenance and available fidelity/coverage.
3. The complete assembled payload is budgeted, including wrapper/report overhead.
4. Overflow, unavailable recovery, and scope denial remain explicit.
5. SessionStart never waits indefinitely for PostCompact.
6. Load observation cannot prove bytes, absence, completeness, or compliance.
7. Pointers never trigger directory-wide instruction injection.

## Implementation spec

### IS-11-01: reconciliation and compatible inputs

M0 classifies existing SP-11 worktree code as retained, adapted, deferred, or removed. SP-11 consumes SP-20 publication/identity contracts without redefining them. Reader and report adapters retain existing fixture meaning and map legacy omissions to unknown.

### IS-11-02: authority-aware recovery

Build uses M2’s current authority view before ordering items. It preserves historical original intent and changes as evidence, but selects active instruction from current authority. Conflicts and unresolved authority yield a safe report/retrieval state rather than a fabricated active goal.

### IS-11-03: qualified complete-record budgeting

Budget the exact assembled payload after tags, headings, handle forms, report, and diagnostics. Render only records allowed by authority/scope policy. Oversized critical records, tiny budgets, and zero budgets create overflow with retained identity, estimator, coverage, and recovery path. Do not automatically truncate a record merely because it crosses a local boundary.

### IS-11-04: SessionStart lifecycle

Use latest usable checkpoint selection from SP-10 under a bounded deadline. On absent, corrupt, incomplete, or unsupported input, emit a bounded Qompack diagnostic only when the host adapter supports it; otherwise return safe empty behavior and preserve an internal report. PostCompact summaries attach as optional derivatives under conservative correlation.

### IS-11-05: rules, skills, and pointers

Rule parsing/version handling is deterministic and scope-tested. Only explicitly approved path rules are considered. Nested/directory instruction content is never inferred from pointer location. Skills become a compact index only when their source and parser version are known; unknown material remains a resolvable/reported handle. A pointer remains archive-only until SP-13/M2 says it is authorized and resolvable.

### IS-11-06: recovery report and rollback

Report separately: Qompack-included, archive-only, native-load-observed, expired-or-deleted, and unknown. Before an added output/schema version is emitted, demonstrate compatible old/new readers or retain a verified backup. After writing a new representation, rollback validates the older reader path or restores the verified backup before recovery exposure. An interface change uses `arch/<reason>` from `develop` before dependent work.

## Test plan (TDD)

No tests run in this planning pass. Future TDD creates failing contract cases before compatible implementation. Existing commands for future validation are `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go test ./internal/rehydrate/... ./internal/rules/... ./internal/skills/...`, and `go run ./tools/devtool plugin-validate`; none runs here. A host lifecycle/load-observation entrypoint is proposed future work and needs target evidence before creation.

| ID | Future assertion |
|---|---|
| T11-AUTH-01 | Later authorized correction supersedes obsolete original intent while preserving provenance |
| T11-AUTH-02 | Conflict, cancellation, unknown authority, and cross-scope records cannot become active instruction |
| T11-BUDGET-01 | Count content, wrapper, handle, report, and diagnostic overhead under named estimator |
| T11-BUDGET-02 | Zero/tiny budgets and oversized critical records emit overflow/recovery/coverage diagnostics |
| T11-LIFE-01 | SessionStart compact selects latest usable checkpoint without PostCompact under bounded deadline |
| T11-LIFE-02 | Missing/corrupt/incomplete checkpoint gives safe bounded fallback and report |
| T11-LOAD-01 | Native-load observation is reported without asserting exact bytes, absence, completeness, or compliance |
| T11-SCOPE-01 | Approved explicit rule scope injects deterministic parsed content |
| T11-SCOPE-02 | Pointer directory, unknown scope, denied scope, malformed parser, and unknown version do not inject instruction content |
| T11-POINTER-01 | Resolution/denial/expired/unavailable/corrupt/unknown handles stay distinct in output/report |
| T11-CORRECT-01 | Correction after compact is honored on resume, repeated compact, fork, and restart |
| T11-ROLLBACK-01 | Reader/output compatibility or verified backup restores recovery before/after new version write |

## Commit plan

Retain the original seven numbered commit identifiers. SP-19 first determines existing completion; these unchecked items describe only the future corrective remainder, not a replay of completed work.

### Commit 1 — `fix(rules): require verified explicit instruction scope`

- [ ] Reconcile scanner/parser/glob behavior and T11-SCOPE cases; no pointer-directory injection or invented loaded bytes.

### Commit 2 — `fix(skills): qualify scoped index and budget metadata`

- [ ] Preserve current skill-index implementation, test scope/parser/version/denial and assembled overhead; provide compatible fallback.

### Commit 3 — `fix(rehydrate): select current authoritative recovery state`

- [ ] Adapt build/items/render using SP-20 state and SP-10 usable checkpoint; validate T11-AUTH/CORRECT while retaining superseded evidence.

### Commit 4 — `feat(rehydrate): serialize complete budgeted recovery records`

- [ ] Adapt budget/drops/report behavior for T11-BUDGET/POINTER, zero/tiny/oversized records and explicit overflow; no native total-context promise.

### Commit 5 — `fix(daemon): bind bounded SessionStart recovery`

- [ ] Reconcile rehydrate_service and compact/resume/fork/clear lifecycle cases; validate T11-LIFE without waiting for PostCompact.

### Commit 6 — `test(rehydrate): verify qualified coverage and recovery`

- [ ] Add installed-host/load/correction/rollback cases and measured Qompack-added versus total observed context; preserve historical Phase 3 replay outputs with accurate diagnostic labels.

### Commit 7 — `docs(rehydrate): record scope budget and rollback decisions`

- [ ] Update future ADR 0011 and migration/coverage evidence for the corrected contract; independent lifecycle/constraint review and T11-ROLLBACK before enablement.

Integration remains 1 through 7 after prerequisite interface agreement. Any missing interface amendment uses a proposed `arch/<reason>` branch in the future; reconcile the existing SP-11 worktree before any branch/worktree action.

## Subagent strategy

No future implementation subagents are authorized now; planning delegates are recorded separately. After M0 and separate implementation authorization, future file ownership is: A owns `internal/rules` and `internal/skills` scope/parser tests; B exclusively owns `internal/rehydrate/items.go`, `render.go` and their authority tests; C exclusively owns `budget.go`, `drops.go` and their budget/report tests; D owns daemon/e2e lifecycle tests; the main implementation owner owns `build.go`, `rehydrate_service.go`, fixtures, integration, and commits. An independent lifecycle reviewer examines SessionStart, PostCompact absence, load qualification, correction, fork/resume, and rollback before enablement. No role changes SP-20 publication/identity contracts.

## Exit criteria

- [ ] M0–M2 and SP-10 latest-usable gates publish compatible input contracts.
- [ ] Current authority over obsolete intent is demonstrated across compact/resume/fork/restart/correction.
- [ ] All representation overhead is accounted for and overflow is explicit.
- [ ] SessionStart recovery has bounded no-PostCompact and failure fallback paths.
- [ ] Scope matrices show no pointer-directory auto-injection.
- [ ] Load-event reporting remains qualified and pointer states remain distinct.
- [ ] Compatibility/backup rollback works before and after a new representation write.

## Done checklist

### Planning-review checks

- [ ] This plan preserves branch `feat/sp11-rehydrator-l5`, major heading order, and future 5–8 commit convention.
- [ ] Source/test evidence is qualified as implementation progress, not runtime verification.
- [ ] No implementation code, executable block, test execution, or Git mutation occurred in this planning pass.

### Future implementation checks

- [ ] T11-AUTH-01 through T11-ROLLBACK-01 pass on named future entrypoints.
- [ ] Existing rehydrator/rules/skills conformance suites run without skipped behavior after reconciliation.
- [ ] Recovery enablement remains off until SP-13/M2 resolution and host-adapter gates pass.

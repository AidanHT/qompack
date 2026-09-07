# SP-19: prerequisite migration reconciliation, capability contracts and baseline accounting

**Status:** proposed M0 remediation prerequisite; implementation not started by this planning revision. The user now reports original Wave 3 SP-10–13 complete. Preserve that completion history; their combined merge and the revised migration gates are not certified here.

**Branch:** proposed `feat/sp19-migration-reconciliation`, from the accepted combined `develop` HEAD after M0-G0, not created here | **Wave:** 3 remediation, not a new wave | **Prerequisites:** M0-00 merges completed SP-10 → SP-11 → SP-12 → SP-13 and passes M0-G0 before M0-01–05 or commits 1–8 start | **Runs in parallel with:** prerequisite read-only inventory/review only; merges and shared integration are serialized | **Design sections:** §§0–3, 5, 7, 9–12 | **Gaps addressed:** G1.4, G5.2, G7.1, G7.6, G8.1, G9.3.

---

## Mission

First integrate the completed original Wave 3 branches into one reviewed baseline. Only after M0-G0 passes, reconcile that assembled plugin against the revised migration contracts and establish capability/evidence inventory and corrected accounting alongside preserved historical baselines. This does not restart SP-01–13 or invalidate the user's V3 CI waiver. The prerequisite accepts an integration baseline; it does not certify native capabilities, complete M1–M3, or close the revised V4 verification gate.

Planning owner: main coordinator, requested GPT-6 Astra; effective model/effort and usage not exposed. Future implementation owner: repository/plan auditor, supported by a host-contract tester and independent baseline reviewer. [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) records the initial branch, dirty files, worktrees, model requests and blockers.

## Design context (verbatim from Qompack.md)

The existing template heading is retained; current v1.5 requirements are summarized without obsolete executable examples.

The original planning snapshot at root HEAD `7f92af5` on `verify/v3` has an older integration view. Existing `../qompack-sp10` through `../qompack-sp13` contain source; their previously recorded HEADs are historical observations, not assumed final delivery tips. The user's later completion statement supersedes the initial in-progress status. `V3-report.md` records local Windows results, synthetic replay limitations, the 2026-08-26 CI waiver and the J5 backfill obligation. No new runtime result follows from inspecting these documents.

`internal/pluginmanifest/manifest.go` declares the package surface; `internal/hookio`, `internal/cli/hookclient.go`, `internal/daemon/handlers.go` and `internal/contract` are host adapters/observers. `go.mod` declares Go 1.26 with toolchain go1.26.6 and a hand-rolled MCP implementation rather than an SDK dependency. `internal/config/defaults.go`, `runtime.go`, `load.go` and the actual config metadata/goldens must be reconciled with the revised prose Appendix C before future checks that expect executable examples.

The currently observed contract monitor in architecture §12.1 treats unavailable producers as informational success and searches sentinel text. Such evidence must not become installed capability certification. Missing events can be lifecycle gaps rather than proof of a failed host feature.

## Out of scope

No implementation action is authorized now. No configuration changes, account/billing changes, native compaction experiments in an active user session, old report rewrites, corpus regeneration, branch switches, rebases, worktree creation or baseline replacement. No product port to Codex or OpenAI calls. Native-history rewriting/cuts/cache-marker manipulation remain unsupported.

## Interface contract

### Consumes

Inspect master/subplans, `CARRIED-DEFECTS.tsv`, completed reports and `sdd/` records; current source/test definitions and package/config consumers; installed versions and restrictions during the future authorized pass. Preserve the current root dirty-file baseline and refresh it before any future code edit.

### Produces

| Contract | Required fields and semantics |
|---|---|
| Repository reconciliation | Existing branches/worktrees/dirty state, completed vs active task IDs, owner, retained/adapted/remediated disposition; no inferred completion |
| M0-G0 merge handoff | Original integration HEAD, nominated delivery HEADs, reviewed incoming changes and conflict decisions, accepted merge HEAD after each step, commands/results/artifacts, inherited failures, independent review and final combined HEAD; all four deliveries retained |
| Capability register | Separate observation, injection, new-result replacement, usage attribution, token estimation, compaction request, blocking and history rewriting; mechanism, provider/version/platform, source/artifact/date, supported status and disabled fallback |
| Request ledger | Request identity/parent attribution, provider/model, rate-table date, pricing mode, reported categories, missing categories, estimate vs invoice, retries/aborts/compaction/child work and non-token charges |
| Contract amendment | Versioned identities, durable publication/frontier, retrieval/error envelope, current-state authority and coverage; SP-20/SP-13/SP-10/SP-11 approve before implementation |
| Baseline provenance | Snapshot, dirty changes, corpus hash/type, metric definition, seed/model/provider/date, historical failures, new vs old fields and non-comparable cases |

Evidence statuses are `documented`, `verified_in_target`, `implemented_unverified`, `unsupported`, `experimental`, and `unknown`. A skipped canary leaves its capability unverified and records why. Reported absence, unknown observation and unsupported mechanism are different outcomes.

## Implementation spec

### M0-00: merge completed SP-10–13 before the rest of SP-19

This mandatory prerequisite is future work only. Before it passes, SP-19 may perform only the inventory, review, integration fixes and validation needed for this merge. Do not start M0-01–05, the `arch/migration-contracts` amendment, SP-19 commits 1–8, or dependent SP-20 work. The main integration owner performs merges; reviewers do not mutate branches. Do not create the SP-19 implementation branch from the old root snapshot or an individual sibling branch.

**Preflight and preservation**

- [ ] Read the complete delivered SP-10–13 implementations, their completion/review artifacts and applicable decisions in `sdd/`, alongside [architecture §9](00-ARCHITECTURE.md#9-git-strategy) and the current [V4 verification plan](V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md). Nominate each final delivery HEAD with its owner; record current `develop`, merge bases, worktree paths, dirt, pending operations and relevant test provenance. Recheck tips before each merge; new commits require renewed review.
- [ ] Identify the worktree that owns `develop`; reconcile its state before use. Keep unrelated files, ongoing sessions, uncommitted planning revisions and all original feature branches intact. No auto-stash, reset, force-push, branch replacement or silent rebase. If relevant uncommitted work prevents a reproducible delivery, record the exact blocker and have its owner resolve it before proceeding. Do not sweep unrelated dirt into a merge.
- [ ] Review each incoming change relative to its merge base and current integration tree. Build a file/contract overlap map, including changes that Git can merge automatically. Record completed behavior, test coverage and compatibility obligations that must survive; a conflict-free text merge alone is insufficient.

**Ordered integration and semantic review**

| Step | Existing delivery branch | Required review before accepting the combined result |
|---|---|---|
| 1 | `feat/sp10-checkpointer-l4` | Checkpoint/pin schemas and readers, frontier publication, PreCompact lifecycle, daemon registration and retained fixtures |
| 2 | `feat/sp11-rehydrator-l5` | Checkpoint-to-rehydrator handoff, SessionStart/resume handling, rules/skills scope, budgets and initial resident service wiring |
| 3 | `feat/sp12-scheduler-l3` | Scheduler-to-checkpoint frontier, worker ownership/cancellation, observation/config consumers and interaction with the existing daemon lifecycle |
| 4 | `feat/sp13-mcp-retrieval-layer` | MCP-to-store/checkpoint/coverage contracts, CLI bootstrap and negotiated schemas; SP-11's resident handles extended without duplicate store/ledger/graph instances |

- [ ] Merge into `develop` in exactly this order, preserving the repository's non-fast-forward merge convention. If a nominated delivery is already integrated, prove its ancestry and retained behavior and record the existing merge instead of creating a duplicate merge. Do not undo existing accepted history to manufacture this ordering; document any pre-existing order discrepancy for independent review before continuing.
- [ ] Resolve conflicts on the incoming branch under architecture §9, with both affected owners reviewing the intended combined behavior. Preserve original commits and make any necessary integration correction separately reviewable; do not conceal a fix inside a hand-edited merge commit or select an entire side merely to remove conflicts. Conflicting schemas, configuration/defaults, manifests, golden fixtures, generated artifacts, hook handlers and composition roots require explicit decisions. Regenerate an artifact only through its existing authorized workflow after its source contract is settled; never refresh a fixture simply to make a failure disappear.
- [ ] Trace actual producers, consumers and failure paths across the four components, including changes without textual conflicts. Check one intended resident store/ledger/graph, compatible checkpoint readers and MCP envelopes, retained hook registrations, startup/resume/compaction flows, scheduler lifetime, and historical retrieval behavior. Record new migration deficiencies for M0-01–05/SP-20/affected follow-ups; do not disguise them as resolved during merging. Never remove working code, tests, recorded constraints or user changes merely to fit a competing branch.
- [ ] After each accepted merge, inspect the complete resulting diff and run the affected existing package/seam checks in the future authorized session. Attach exact HEAD, commands, outcomes and artifact paths. No next merge after an unexplained regression, unresolved conflict or loss of completed behavior. Shared-file fixes have one editor and independent review.

**Combined-baseline acceptance**

- [ ] At the final combined HEAD, run the applicable existing whole-tree and cross-component validation described below, then review all four deliveries together. Demonstrate inclusion of every nominated HEAD and verify intended behavior, because ancestry alone does not show that later resolutions retained it. No unresolved index entries, conflict markers in changed code, unexplained deletions, accidental stubs, omitted tests or unreviewed public-contract changes may remain.
- [ ] Distinguish integration regressions from reproducible inherited failures, stale assertions superseded by the planning revision, unavailable environments and unperformed migration gates. Retain evidence and a named follow-up owner for each inherited limitation. Any merge-induced regression or unexplained failure blocks M0-G0. Existing waivers apply only to their recorded scope; no new waiver or skipped integration pass is inferred. If provenance cannot establish that a failure is inherited, keep the merge gate blocked.
- [ ] Independent review accepts a handoff in the existing evidence ledger containing the pre-merge baseline, four delivery-to-merge mappings, conflict/compatibility decisions, actual validation artifacts, inherited limitations and final combined HEAD. M0-G0 passes only when the combined baseline preserves the delivered work and all material integration findings are resolved. Keep unverified/unsafe features disabled or exercise the baseline only in disposable validation sessions; merging is not deployment or migration enablement.
- [ ] Only after M0-G0 acceptance, start the remaining SP-19 tasks and proposed branch from that exact combined baseline, then SP-20 after its own prerequisites. Full revised V4 migration verification follows the corrective work; requiring it before M0-G0 would create a dependency cycle. No prior completion checkbox or report is rewritten to represent this new gate.

### M0-01: preserve and map

After M0-G0, refresh the accepted combined baseline and source-owner records. Preserve SP-01–13 completion history, V3 waived-open J5 and unrelated dirt. Classify which revised requirements are already satisfied and which require remediation; do not repeat completed implementation commits. Map original phases: Phase 0→SP02/V2; Phase1→SP04/06/V2 plus SP08/V3; Phase2→SP03/09 with SP11/13 consumption; Phase3→SP10/11/V4; Phase4→SP12/V4; Phases5/6→SP15/V5 with completed SP07; Phase7→SP16/V5. Production remains V6. Stable gap/revision IDs remain in TRACEABILITY.

### M0-02: shared-contract handoff

After M0-G0, prepare the future `arch/migration-contracts` amendment before dependent migration implementation commits. Retain existing public symbols and old fixture bytes through compatible readers where possible. An additive contract cannot falsely interpret old missing fields as complete evidence. Agree unique source-file ownership across daemon composition roots, store schemas, checkpoint writer/reader, rehydrator and MCP. Preserve current branch names; no auto-stash/reset/rebase. The completed original deliveries have already been combined by M0-00; dependency ordering now governs corrective follow-ups and enablement, not a restart or reordering of those merges. Keep dependent features disabled until M1/M2 recovery is established. Do not create a checkpoint↔retrieval package cycle: consume shared identities/envelopes through composition roots.

### M0-03: host contracts and packaging

Design disposable session canaries for supported/older/unknown host versions, managed restrictions, invalid hook payloads and competing hooks. Test observation and injection separately. SessionStart compact reinjection is independent of optional PostCompact; InstructionsLoaded is asynchronous. New-result replacement requires exact supported output shapes. PreCompact custom instructions are input. Blocking capability does not establish safe automatic veto: recovery/proactive distinction is missing, automatic optimization veto stays off and manual compact is never blocked for optimization.

Validate the installed package using the Claude CLI's documented plugin-validation command, after confirming the supported version's syntax. Existing local `go run ./tools/devtool plugin-validate` checks are complementary, not host certification. A missing validation entrypoint is a named task to create a compatibility runner in this future pass. Never conduct destructive/recovery capability probes in the active user session.

### M0-04: baseline and consumers

Locate every consumer of `YoungDaly`, `SkiRentalShouldWrite`, rewrite cost, p-selection, `FocusInstructions`, `CustomInstructions`, `BloomOnly`, `AnswerAbsent`, `Ephemeral`, `Truncate`, `MarkEncoded`, per-chunk token sums and `dropped` semantics across source, fixtures, test guards, config metadata and plans. Record exact locations before altering semantics. `internal/eval`, `test/replay/main.go`, `test/replay/phases.go`, `tools/devtool/planchecks.go`, `test/guards/buildorder_test.go` and config golden tests are explicitly in the future compatibility audit.

Preserve `testdata/baseline/phase0.json` and existing benchmark files; add a versioned corrected request ledger beside old metrics. Do not replace an old definition in place or fill unavailable usage with zero. Pricing arithmetic uses current supported provider rates at measurement time and separates subscriptions, estimates and invoices. The conditional cache illustration is N versus w+(N−1)r, not w/r as ordinary break-even. Native compaction input, Qompack frontier work, added tokens and total observed context are separate measurements.

### M0-05: carry reconciliation

SP05-D1 goes to SP-20 durable drain/ack remediation. SP02-D1–D6 stay one V4 baseline/corpus unit; SP06-D2/SP08-D1 remain paired performance/novelty decisions. Existing fixed/wontfix entries SP04-D2/D3/D7 and SP06-D1 are not reopened silently; SP-20 adds fidelity/GC requirements beside their historical limitations. SP-11-C28 (`frontierOf`→real `Ref.Frontier`) is checked against active work, not assumed unresolved. V4 verifies retained SP07 NodeID/generation/fixture rulings from `V2-SP07-handoff.md`. `CARRIED-DEFECTS.tsv` is only updated later with actual dispositions/evidence under its existing guard.

## Test plan (TDD)

All commands below are future execution references. Inspect and use existing `go run ./tools/devtool test`, `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool replay --ci`, and relevant `internal/contract`, `internal/hookio`, `test/guards`, `test/e2e` suites. Their task names are present in `tools/devtool/main.go`; check actual options before use. No check was run here. The target-host canary and request-ledger integration entrypoints must be added if still absent; do not represent them as existing tests.

For M0-G0, inspect delivered tests and their recorded results first. After each merge, run the existing tests for affected checkpoint/pins, rehydration/rules/skills, scheduler, MCP and daemon/CLI seams as applicable. At the final combined HEAD, use existing `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool lint`, `go run ./tools/devtool plugin-validate` and the delivered integration/e2e entrypoints relevant to those seams. Record the command, HEAD, environment, exit result and artifact for each; missing entrypoints or unavailable platforms remain explicit. Compare to the same check on the relevant source/pre-merge baseline when attributing a failure. Retain the existing replay/performance obligations where applicable, with measurements on an isolated quiet runner; historical percentages cannot certify the merge. Do not require still-unimplemented SP-19/20 migration tests to pass at this prerequisite, waive a new regression, weaken an assertion silently, or claim a repository validator proves installed-host compatibility.

| Gate | Observable future acceptance / required artifact |
|---|---|
| M0-G0 completed-wave integration | Four nominated SP-10–13 deliveries are retained in one accepted `develop` baseline, ordered merges or verified prior integrations are mapped, semantic/conflict review and applicable validation artifacts exist, no unexplained integration regression remains, and the independent reviewer accepts the handoff. Blocks all remaining SP-19 work until passed; migration/runtime capability gates stay separate. |
| M0-G1 mapping | Every actual task/worktree has retained progress/owner/disposition; no completed checkbox reset; reconciliation snapshot attached |
| M0-G2 adapter | Every enabled production feature has mechanism plus target canary artifact including host/provider/OS/version/date; unsupported/unknown optimizations remain off |
| M0-G3 lifecycle | Missing/duplicate/out-of-order events or absent PostCompact never create complete frontier or unsafe veto; canary transcript and event ledger retained within privacy policy |
| M0-G4 packaging | Installed manifest/launcher/MCP validates and discovers actual tools; repository-only validation is labeled as such |
| M0-G5 accounting | Missing/retry/aborted/compaction/subagent usage remains attributed or unknown, with category sums and rate-schedule provenance; no invoice claim from an estimate |
| M0-G6 baseline | Original metric outputs preserved; corrected labels/corpus changes separately versioned; SP02-D1–D6 resolved together or explicitly blocked |

## Commit plan

Future implementation only; eight small conventional commits at most, no attribution trailers. Retain any already-completed equivalent work and create only the required remediation delta. Each row includes its contract/test before compatible implementation and retained validation evidence.

**Mandatory integration prelude:** complete M0-00/M0-G0 first. The four existing feature deliveries merge in their stated order under architecture §9; necessary incoming-branch conflict fixes are separate reviewed integration commits. Record actual hashes and retained prior integrations, never invent hashes or duplicate completed feature commits. These are prerequisite integration operations, not additional SP-19 feature commits or a renumbering of commits 1–8 below. None is performed in this planning pass.

| Commit | Proposed outcome and future action |
|---|---|
| 1 — `docs(plans): reconcile completed migration baseline` | [ ] After M0-G0, consume its accepted combined HEAD and merge evidence; inventory/carry/ownership snapshot and capability unknowns, independent auditor review |
| 2 — `test(contract): define target capability canaries` | [ ] Disposable fixtures/runner and explicit skip/degraded outcomes; review privacy before execution |
| 3 — `fix(contract): separate capability observations` | [ ] Compatible observation/status changes with no inferred completeness; run relevant contract tests later |
| 4 — `test(plugin): verify installed adapter packaging` | [ ] CLI validation and competing-hook/version canaries; record target artifact or keep unverified |
| 5 — `feat(eval): add request usage ledger` | [ ] Category/missing/attribution contract and compatible ledger beside old metrics |
| 6 — `fix(config): version migration settings safely` | [ ] Reader/default/deprecation/guard compatibility and independent switches; no old fixture reset |
| 7 — `test(eval): preserve baseline provenance` | [ ] Old/new metric comparison and arithmetic checks, failed trials and unknown telemetry retained |
| 8 — `docs(migration): record prerequisite gates` | [ ] Actual validation, disabled capabilities, rollback rehearsal and dependent-owner handoff |

## Subagent strategy

During M0-00, the main integration owner alone mutates the integration branch; the original SP-10–13 owners review their deliveries and incoming-branch corrections. A separate read-only integration reviewer checks preservation, shared contracts and validation evidence. Bounded read-only inventory may overlap, but no concurrent merge or shared-file writer is allowed. The host tester, baseline implementation owner and other SP-19 implementation roles do not start their remaining tasks before M0-G0.

After M0-G0, the future repository auditor owns planning/carry inventory, host tester owns `internal/contract`/`internal/hookio` and proposed canary fixtures, baseline owner owns `internal/eval`/`test/replay`, and independent reviewer reads results. Main implementation coordinator alone integrates architecture/config/daemon shared contracts and commits. Agree exact changed file lists from the refreshed tree; no two owners edit one file. Future branch/worktree convention: `feat/sp19-migration-reconciliation`, proposed `../qompack-sp19`, from the accepted combined base; the subsequent architecture amendment uses `arch/migration-contracts`. None is created in this planning pass. Native planning threads are the separate P-stage team in the ledger, not these future roles.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). These are optional future execution delegates; the main integration owner still owns every merge and shared-contract decision. The model policy cannot bypass M0-G0.

| Stage / existing role | Requested model and effort | Reason and boundary |
|---|---|---|
| M0-00 delivery/artifact inventory | Opus 4.8 / low | Extract exact HEADs, paths and recorded outcomes against a checklist; return unexplained differences without architectural guesses |
| M0-00 shared-contract analyst | Opus 4.8 / high | Trace checkpoint/rehydration/scheduler/MCP producers and consumers read-only; Fable 5.1 / high for a substantive multi-owner conflict |
| M0-00 independent integration reviewer | Fable 5.1 / high | Assess retained behavior, conflict decisions and combined validation before accepting all four deliveries |
| After M0-G0: repository auditor | Opus 4.8 / medium | Reconcile accepted baseline, carries and consumer locations; causal uncertainty goes to the contract owner |
| After M0-G0: host-contract tester; accounting/baseline owner | Opus 4.8 / high | Bounded independent contract/test slices; artifacts alone cannot establish installed behavior or invoice accuracy |
| Independent baseline/accounting reviewer | Opus 4.8 / high | Review uncertainty and old/new compatibility; Fable 5.1 / high only for an unresolved cross-contract conflict |

Before M0-G0, use one inventory child and one read-only contract analyst where useful; the final reviewer uses a free slot once combined evidence is ready. Merges remain serial SP-10→SP-11→SP-12→SP-13, with no child Git mutations. After M0-G0 and shared interfaces, host and accounting work may overlap on their agreed files. At most three children total and one Fable; roles are reused or retained in main when no independent work is ready. Do not launch the post-gate team during the merge prerequisite.

## Exit criteria

- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] M0-G0 records all four completed deliveries integrated and independently accepted before M0-01–05 or commits 1–8 began; final baseline and branch ancestry are retained as evidence.
- [ ] M0-G1–G6 have actual artifacts or explicit disabled/unverified dispositions accepted by dependent owners.
- [ ] M1/M2/M3 contract and file ownership is agreed; completed/active work is preserved.
- [ ] Unsupported controls remain disabled, manual compact unblocked for optimization.
- [ ] Historical metrics and V3 waiver/J5 backfill remain visible.
- [ ] Runtime verification is recorded by host/provider/platform/version/date; no mock-only certification.

## Done checklist

- [ ] Future owner records actual commands, results, skips, failures and changed paths.
- [ ] Future commits use conventional types, preserve attribution policy and existing branch history.
- [ ] Independent review approves the recovery and accounting contracts before dependent activation.
- [ ] Rollback/unknown environment behavior is tested and handed off to SP-20/SP-13/SP-10/SP-11.

### Rollout, rollback and blockers

**Merge prerequisite recovery:** retain original delivery branches/HEADs and the integration HEAD before each merge. Stop on unresolved ownership, changed delivery tips, conflicts or unexplained regression. Abort only an in-progress merge initiated by this integration task in its verified worktree; never abort another user's operation. Correct incoming-branch changes with owner review and recheck the affected evidence before retrying. If an accepted shared merge later needs undoing, use separately reviewed revert or forward-fix commits under repository policy, assess later dependent changes, and retain all original history; do not reset shared `develop`. Source rollback does not roll back runtime data: M0-00 does not deploy the plugin or migrate a user's data. M0-G0 stays blocked until the accepted combined baseline is re-established.

Roll out observation/report-only first. Disable newly added capabilities independently if canaries fail; keep compatible old readers and the historical baseline. Before any new data/config format write, retain a consistent supported backup and verify old/new readers. After new writes, rollback uses the compatible reader or verified backup; no silent format downgrade. Existing old behavior with a known unsafe absent/error meaning is not the fallback for corrected consumers: return unavailable/uncertain.

B01 installed versions/permissions and B02 current worktree state in the ledger block target certification until the named canaries and owner reconciliation. B05 J5 depends on billing restoration; its waiver is preserved and no account change is authorized. Missing usage/model routing metadata remains unknown. Failure of any prerequisite keeps dependent pointer replacement disabled while planning remains actionable.

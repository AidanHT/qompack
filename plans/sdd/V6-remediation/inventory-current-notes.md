> **Candidate 6 and 7 dispositions, 2026-10-02 (C6.2; supersede the dispositions below).** Every row
> now carries `c6_result` and `c6_evidence`, from candidate 6 (`verify/v6` `99d0b18`) evidence, and
> `c7_result` and `c7_evidence`, for candidate 7 (`verify/v6` `d20309c0`, code `b31d0753`), all appended
> to `inventory-current.tsv`. Candidate 6: 251 `verified_in_target`, 34 `partial_verified`,
> 1 `implemented_unverified`, 5 `failed`, 7 `unknown`, 3 `unsupported`, 3 `documented`. Candidate 7:
> 251 `verified_in_target`, 39 `partial_verified`, 1 `implemented_unverified`, 7 `unknown`,
> 3 `unsupported`, 3 `documented`. The seven
> original columns are unchanged; the `result=` field inside `limitation` is the pre-close-out record.
> Evidence codes, the rule, the pending rows with what closes each, the fourteen section 3
> identifiers and the SP19/20/21 switches are in `../V6-closeout/inventory-c6-map.md`. Two statements
> below no longer hold: delivery-journal rollover is enabled by default (D2, C1.10; SP20-D4 `fixed`),
> and the selection switches are `runtime.selection.submodularEnabled` and
> `runtime.selection.loopWarningsEnabled`.

> **Candidate reconciliation, 2026-09-22 (supersedes on snapshot identity).** The current
> candidate snapshot is committed **HEAD `65bc8d7`** (`docs: record V6 recovery progress and
> unresolved capacity gate`), 16 commits past the old `c95b7af` snapshot, and the **working tree is
> clean** — every family the body below calls `WORKING-TREE`/`(M)`/"under authorship"/"under
> integration" has now **LANDED as a commit** on `verify/v6`. Landing map:
> ingest.go pre-ACK (SP20-D6) → `c34acb4`; delivery_order/terminal + generation-segment authority →
> `891582e`; bounded immutable delivery index (radix) → `4cda64d`; fsck publication (detection) →
> `7e5a141`; observation-binding + maintenance/publication-sync → `99108a2`; original-publication
> recovery before replay (prompt_delivery) → `22ff16c`; leased-replay order + policy retirement →
> `c34acb4`; backup pre-copy delivery frontier → `e950486`; assembled-budget enforcement (1.6.18) →
> `06681ef`; Linux exited-lock reclaim → `5c8329e`; MCP diagnostics sync / exited-child recognition →
> `108bd63`; product-child race wired into nightly → `8948992`; recovery/diagnostic prose + the
> `commands.go` self-test **user-facing Summary** correction ("report host-contract and subsystem
> checks; exit non-zero on a critical failure") → `1775eda`; release.md §7 narrowed to the release
> workflow with the historical-CI evidence → the accepted docs handoff.
>
> **Committing did not change any disposition.** Every landed row stays `implemented_unverified`; no
> row is `verified_in_target`; the **final integrated run on the candidate is pending** (a whole-tree
> `go run ./tools/devtool test` is executing in the detached sibling `qompack-v6-rc1`, not here).
> **Delivery-journal rollover** (`enableDeliveryGenerations = false`, `internal/daemon/delivery_generation.go:141`)
> ships **default-off and is NOT accepted** for release: bounded memory only, no bounded-storage /
> in-place rollover promise. Default-off is a **mandatory capacity-carry-blocker, not a silent
> waiver**, and neither disabled admission nor a rollover focused pass is production enablement.
> Historical FAIL rows (`1.13.4`, `1.17.12`) and human/CI rows (`1.18.12`, `1.17.19`) are preserved
> unchanged; no PASS or human-UAT completion is inferred. Five TSV rows were reconciled off stale
> working-tree language (`1.5.7`, `1.5.12`, `1.8.6`, `1.17.9`, `1.17.15`); all **304 IDs** remain,
> one row each. The `c95b7af`-plus-working-tree snapshot definition in the body below is retained as
> chronology and is superseded by this block. The remaining gates are enumerated in
> `current-candidate-gate-map.md` (which supersedes `automated-closeout-run-map.md`).
>
> Coordinator correction, 2026-09-22: this document contains the inventory worker's
> earlier `c95b7af` handoff below. It is not a current RC or an execution report.
> Main has since committed `99108a2` (bound observation intents), `22ff16c`
> (recover original publication), `7e5a141` (fsck publication status), and
> `c34acb4` (leased replay ordering and durable denial retirement). Journal
> capacity, GC read safety, backup snapshot and assembled-budget changes are
> still under integration. Imported `execution.tsv` statuses apply only to each
> historical run's recorded artifact, never the final bundle.
>
> Main corrected three assertions in the companion TSV: `1.2.5` keeps synthetic
> OPT diagnostics without a task-quality ceiling; `1.6.18` requires assembled
> estimates rather than exact chunk sums; `1.17.8` preserves project work under
> declared retention and rollback policy. All 304 original IDs remain. No
> candidate PASS or human UAT completion is inferred from these edits.

# V6 current-inventory collation — notes

Companion to `inventory-current.tsv`. **Scope/mapping only — NO runtime, build, benchmark or git-write
performed. No PASS is claimed; Main joins new run evidence after this handoff.**

**Reconciliation pass (2026-09-21).** This revises the earlier draft, which was collated against the
frozen source baseline `301a8e9` while much of the V6 remediation source was still uncommitted and
"under authorship." Since then most of that source has **landed as commits on `verify/v6`** and the
candidate snapshot has moved. This pass re-qualifies every changed row against the **current snapshot**
and preserves all 304 original IDs and their old→new assertion mapping unchanged. The snapshot is
explicitly **not a final RC** (§9): the tip is a committed HEAD *plus* a large uncommitted working
tree, and no row has been executed on the candidate.

**Baseline vs current snapshot.**
- *Source-reconciliation baseline* (unchanged, immutable): `plans/sdd/V6-VERIFY/inventory.md` /
  `execution.tsv` at `301a8e9` — the origin of every ID, historical assertion and old→new mapping.
- *Current candidate snapshot* (what the TSV `current_*`/`limitation` columns now describe): committed
  **HEAD `c95b7af`** (all remediation commits below are ancestors of it) **plus an uncommitted working
  tree** (delivery ordering/terminal/rollover, fsck-publication, SP08-D3 regression tests, and in-place
  edits to `ingest.go`/`prompt_delivery.go`/`publication_sync.go`/`maintenance.go`). Committed ≠ passed:
  nothing here has been run on the candidate, so every landed item stays `implemented_unverified`.

## 1. Provenance and method

- **Immutable inputs:** `plans/sdd/V6-VERIFY/inventory.md` (304-ID source reconciliation, base
  `301a8e9`) and `plans/sdd/V6-VERIFY/execution.tsv` (keyed per-ID result overlay); current normative
  plan `plans/V6-VERIFY-production-readiness-and-uat.md`. These are preserved unchanged.
- **TSV columns:** `id · original_assertion_reference · current_assertion · current_test_symbols ·
  source_paths · disposition · limitation`. `original_assertion_reference` and `current_assertion`
  come from inventory.md §1 (historical vs current-owner criterion); `current_test_symbols` and
  `disposition` are inventory.md's already-verified current symbols/dispositions (foundation and
  unchanged families reference those verified definitions rather than a whole-file re-read, per the
  brief); `limitation` carries execution.tsv's `result`/`runs`/limitation verbatim, plus any
  working-source flag.
- **source_paths derivation:** extracted from each row's cited `symbol · pkg` tokens (mapped
  `pkg → internal/<pkg>`, plus literal `test/*`, `tools/devtool`, `docs/*`, `internal/*.go:line`).
  Rows whose symbol cell carries no explicit package fall back to the **SP-family home package**,
  labelled `(family default; no explicit pkg cited)`. Derived, not authoritative — Main confirms on
  the candidate.
- **Remediation-source overlay:** the V6 remediation source not in base `301a8e9` is folded into the
  affected rows' `limitation`. The source-symbol annotations were partly
  resolved to `LANDED verify/v6 (<commit>)` for the parts now committed, with the still-uncommitted
  parts explicitly named as `WORKING-TREE`/`(M)`; disposition stays `implemented_unverified`/`unknown`
  (never PASS) regardless of commit state. Detailed in §5.

## 2. Uniqueness count — exact

**304 distinct IDs, `1.1.1`–`1.18.14`, one TSV row each, zero duplicates, zero dropped.** Family
breakdown (sums to 304):

`1.1`=28 · `1.2`=14 · `1.3`=17 · `1.4`=17 · `1.5`=21 · `1.6`=21 · `1.7`=14 · `1.8`=15 · `1.9`=13 ·
`1.10`=19 · `1.11`=17 · `1.12`=18 · `1.13`=18 · `1.14`=10 · `1.15`=15 · `1.16`=13 · `1.17`=20 ·
`1.18`=14.

## 3. Migration additions (SP19/20/21) — not renumbered into the 304

From inventory.md §C (source of truth `internal/config/defaults.go:155-162`, `runtime.go:118-157`,
`internal/config/migration.go`, `internal/admission/types.go`). **Every switch ships as below;
disabled = refused-until-gate, recorded disabled, never "passed."**

| Switch (config key) | Owner/gate | Default | State |
|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | SP-20 M1-01 | false | DISABLED (refused until M1) |
| `runtime.migration.publication.durableFrontier` | SP-20 M1-02 | false | DISABLED |
| `runtime.migration.reinjection.sessionStartCompact` | injection kill-switch | true | ENABLED (the one tested adapter) |
| `runtime.migration.replacement.newResult` | SP-21 M4 admission | false | DISABLED — admission off; pass-through |
| `runtime.migration.compaction.automaticVeto` | SP-19 M0-03 native veto | false | DISABLED/refused (native veto retired) |
| `runtime.migration.compaction.blockManualCompact` | §12 | false | HARDWIRED off |
| `runtime.migration.experiments.enabled` | SP-15/16 | false | DISABLED |
| `runtime.pselection.submodularEnabled` | SP-15 | false | DISABLED |
| `runtime.grammar.loopWarningsEnabled` | SP-15 | false | DISABLED |

- **SP-19 (M0 mapping/canaries/ledger/baseline/state):** `internal/state/*` —
  `TestSet_CannotAcquireUserAuthorityOrRewriteEvidence`,
  `TestSupersede_OnlyAuthoritativeSourcesMaySupersedeAuthoritativeState`,
  `TestConflict_IsRenderedUntilResolvedByAnAuthorizedSource`,
  `TestScope_DoesNotLeakAcrossSessionsOrWorktrees`,
  `TestDecode_RefusesDocumentsItCannotReadRatherThanGuessing`; producer wiring
  `internal/daemon/options.go:339-352`; `TestDoctor_EveryMigrationGateIsPendingInThisBuild` · cli.
- **SP-20 (M1–M2 capture/publication/backup/rollback/uncertainty/retention/scope):**
  `test/integration/capture_policy_test.go` (`TestCapturePolicyToHookEnvelopeRoundTrip`,
  `TestCapturePolicyProducesTheCompleteFidelitySet`); `test/fault/publication_test.go`;
  `internal/store/migrate_test.go`
  (`TestImport_LostCursorFallsBackToTheFrontierWithoutDuplicating`,
  `TestImport_LegacyUnknownFidelityIsNeverUpgraded`); `test/canary/optimize_test.go`
  (`TestCanary_CompactionBlocking`, `TestCanary_UsageAttribution`).
- **SP-21 (M4 admission — opt-in, disabled):** `internal/admission/*` — `TestSwitchIsOffByDefault`,
  `TestADisabledGateOutranksAProcessedEnvelope`, `TestDisabledGateCapturesNothing`,
  `TestDisablingAdmissionCannotDisableRecording`, `TestPrivacyOutranksTheBypass`,
  `TestAdmissionIsIdempotentAcrossRedelivery`, `TestEveryMeaningFieldIsPreserved`,
  `TestTheShippedComparisonIsInconclusive`, `TestRollbackOrderIsFixed`,
  `TestPostWriteRollbackValidatesReadBackOrRestores`; `TestCanary_NewResultReplacement`;
  `TestSwitch_GatedCapabilitiesAreRefusedNotDisabled` · test/release.

## 4. Fourteen §3 integration identifiers — not renumbered into the 304

From inventory.md §D. **No `TestV6_*` for §3 exists in base `301a8e9`;** the historical names are
identifiers only, realised across `test/{canary,platform,release,security,fault,integration,e2e}` +
`internal/cli` + `internal/pluginmanifest`. Genuine gaps: **packaged-bundle-live** and
**human/held-out evaluation**.

| §3 | Current mapped case(s) | Disposition |
|---|---|---|
| 3.1 PackagedBundleObservesARealSession | `TestCanary_HostInventory`, `_RegisterIsInternallyConsistent`; `TestV5_DegradedPassiveIsStillCorrect…` · e2e | partial — packaged live NOT-RUN |
| 3.2 UniversalLauncherPreservesHookSemantics | `TestPlatform_WindowsHookLauncherForms`, `_PluginRootWithSpacesAndUnicode`, `_ReadOnly*`; `TestCanary_CompetingHooks`, `_InvalidHookPayload` | mapped |
| 3.3 DocumentedCommandsRunAgainstShippedBundle | `TestCanary_PluginValidate`; `TestGenCommandDocs_MatchesTheInstalledHelp` (source-derived) | partial — against-shipped-artifact NOT-RUN |
| 3.4 EliminationStalenessSurvivesPackaging+MCP | `TestV3_ObserverFileVersionsDriveEliminationStaleness`; `TestCanary_MCPLauncherDiscoversTools` | partial — upgrade-survival NOT-RUN |
| 3.5 EphemeralRetrievalResultsAreEvictedFirst | native eviction **RETIRED** → `TestPropose_ChoosesAtMostOneRepresentationPerItem`; legacy `TestV4_EphemeralRetrievalResultsRankFirstForEviction` | retired/replaced (see §6) |
| 3.6 FsckRepairsSeededCorruption | `internal/cli/fsck_test.go`; `TestFault_AuditSeesADeletedObjectUnderALiveIndex`; **+publication accounting (LANDED `80a3e04`; `fsck_publication` WORKING-TREE)** | mapped — packaged fsck live NOT-RUN |
| 3.7 DoctorAgreesWithStatus+Subsystems | `TestDoctor_AgreesWithStatusOnModeAndProvenance`, `_ReportsCapabilityEvidenceWithoutInventingIt` | mapped |
| 3.8 ConfigReferenceDescribesTheBinary | `TestGenConfigDocs_LeavesMatchDefaultsOneToOne`, `TestUserGuideCoversEveryGeneratedCommandAndTool` | mapped |
| 3.9 InstallUpgradeUninstallByteIdentical | `TestInstall_HostCLIInstallUpgradeUninstall`; admission `TestRollbackOrderIsFixed`; **+maintenance backup/restore (LANDED `3ab1523`/`4a12eff`)** | mapped — full packaged NOT-RUN |
| 3.10 DegradedPassiveFromPackagedBundle | `TestV5_DegradedPassiveIsStillCorrect…`; `TestPlatform_UnknownSettingsVersion`, `_UnsupportedOptimizationsDisabled` | mapped — packaged NOT-RUN |
| 3.11 NoSecretAndNoNetworkFullPackaged | `TestSecurity_NoSecretReachesAnyDurableSurface`, `_TelemetryCannotBeTurnedOn`, `_MCPServerNeverImportsOSExec`; **+V6-AUTH/capture-scope (LANDED `c9b5251`/`00e0c98`)** | mapped — full-session live NOT-RUN; archive-auth was FAILED |
| 3.12 CheckpointToRehydrationRoundTripBundle | `TestE2E_SessionStartCompactAfterFailedSummary`, `TestE2E_Checkpoint*`; `TestFault_CheckpointDropsAnUnresolvablePointer` | mapped — bundle NOT-RUN |
| 3.13 ReleaseArtifactsReproducible | `TestAssembleBundle_Deterministic`/`_ChecksumsFormat`, `TestWriteArchiveChecksums`, `TestReleaseCheckDeterminismVersion`; `TestBundle_OnDiskMatchesGenerator`; `TestCanary_PackagingShape` | mapped — reproducibility run NOT-RUN |
| 3.14 HotPathHoldsEverySubsystemResident | `TestIntegration_HotPathWarmWithRealResidentState`, `_HotPathDegradesRatherThanBlocks`; **+pre-ACK correction (test LANDED `c78f610`; `ingest.go` M)** | mapped — measured latency NOT-RUN (quiet runner) |

## 5. Remediation-source overlay (V6 remediation; not in base `301a8e9`)

Mapped additively to the affected IDs, marked `implemented_unverified`/`unknown`, **never PASS**.
Symbols folded into the TSV `limitation` for the IDs shown.

**Landed-vs-working split (current snapshot).** All commits below are ancestors of HEAD `c95b7af`;
being committed does **not** change disposition — none has been executed on the candidate.

- **LANDED (committed on `verify/v6`), `implemented_unverified`:**
  - `c9b5251` mcp origin checks + `00e0c98` unprovable-file-payload refusal — V6-AUTH + capture-scope.
  - `8f2750a` publish tool use against observation identity + `99108a2` bind recoverable observation
    publication intents — **SP08-D2 observation-binding core now committed** (was working-source in the
    draft); `internal/store/observation_{lookup,publication,audit,guards}.go` are committed/clean.
  - `62f268f` durable prompt publication recovery + `46613fd` recover-leased-prompts-before-advancing
    + `c78f610` retain-refused-deliveries/capture-replayed-prompt — **SP08-D3 prompt-replay recovery now
    committed** (was "under authorship" in the draft); the inverted regression
    `TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero` and the SP20-D6 pre-ACK guard test
    (`hotpath_ack_tail_test.go`) landed here too.
  - `3ab1523` certified maintenance recovery + `4a12eff` backup CLI verify/restore — V6-RECOVERY-2.
  - `80a3e04` publication gap accounting (detection) — V6-RECOVERY-1.
  - `19344e3` bounded object reads (read-perf) · `c95b7af` (HEAD) preserve missing journal migration
    anchors.
- **WORKING-TREE (uncommitted at this snapshot), `implemented_unverified`/`unknown`:** the bounded
  leased-delivery ordering, terminal-completion disposition, journal rollover (generation/radix), and
  publication fsck below; plus in-place `(M)` edits to `internal/daemon/ingest.go` (SP20-D6 pre-ACK impl),
  `internal/observer/prompt_delivery.go`, `internal/store/publication_sync.go`, and
  `internal/store/maintenance.go` (later refinements over the committed core).

- **N-CAP capture-scope containment** (cross-package project/authorization boundary):
  `internal/hookio/capture_scope_v6_test.go` (`TestCaptureScope_*`, `TestCaptureScopeRaw_*`),
  `internal/cli/capture_scope_v6_test.go` (`TestAdmitHookCapture_*`, `TestScopeGuard_*`),
  `internal/observer/capture_scope_v6_test.go` (`TestOnToolUse_Refuses*`/`…StillStored`),
  `internal/daemon/capture_scope_v6_test.go` (`TestDispatchOp_OutOfProjectDirectIPCDoesNotReachWAL`,
  `TestAdmitDelivery_*`). LANDED `00e0c98` (unprovable-file-payload refusal). → extends 1.5.20, 1.13.5, 1.17.12, §3.11; SP-20 capture scope. `implemented_unverified`.
- **Archive provenance/metadata (V6-AUTH-1/2):** `internal/mcp/authorize.go`, `handlers.go`,
  `handlers_common.go`, `authorize_v6_test.go` (`TestV6_LostFilePathDoesNotBecomePathlessAuthority`,
  `TestV6_DedupDoesNotLaunderARestrictedHash`, `TestV6_HashRefusesStoreWithoutProvenance`);
  `test/security/v6_authorization_test.go` (`TestV6_ArchivedReadRetainsItsAuthorizationBoundary`,
  `TestV6_HashAddressesDoNotBypassPathAuthorization`). LANDED `c9b5251` (mcp origin checks). → 1.13.4
  (historical execution=**FAILED**), 1.17.12 (historical **FAILED**), 1.5.20, §3.11. Fix committed;
  historical FAIL is not cleared — `implemented_unverified` until Main re-runs the security gate.
- **Maintenance backup/restore (V6-RECOVERY-2) — LANDED `3ab1523`/`4a12eff`:**
  `internal/store/maintenance.go`+`_test.go` (`TestMaintenance_*` — incl. `_CorruptedBackupIsRefused`,
  `_AdversarialManifestRefusedBeforeReadingFiles`, `_StagedRehashCatchesPostCopyCorruption`;
  `maintenance_test.go` clean, `maintenance.go` carries later `(M)` refinements),
  `internal/cli/backup.go`+`_test.go` (`TestBackupCLI_*`), `internal/cli/writer_lease.go`,
  `internal/daemon/borrowed_lease.go`+`_test.go` (`TestBorrowedLease_*`),
  `internal/store/backup_test.go` (`TestRollbackDrill_*`, `TestBackup_*`), `docs/backup.md`. → 1.17.7/8/9,
  §3.9; SP-20 rollback/backup, SP-21 rollback. `implemented_unverified`; packaged install NOT-RUN.
- **Publication accounting (V6-RECOVERY-1) — LANDED `80a3e04` (fsck_publication CLI still WORKING-TREE):**
  `internal/store/publication_audit.go`+`_test.go` (`TestAuditPublication_*`),
  `internal/daemon/publication_audit.go`+`_test.go`
  (`TestLoudPublicationGaps_*`, `TestAccountPublicationGaps_*`), `internal/cli/fsck.go`,
  `test/fault/v6_fsck_test.go` (`TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence`,
  `TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus`),
  `test/fault/publication_test.go` (F4-1/F4-4 rows re-dispositioned); uncommitted
  `internal/cli/fsck_publication.go`+`_test.go` on top. → 1.17.15, §3.6; SP-20 M1
  publication. **Detection, NOT recovery.** `implemented_unverified` pending packaged run.
- **Pre-ACK latency correction (SP20-D6):** guard test committed at `c78f610`
  (`internal/daemon/hotpath_ack_tail_test.go`,
  `TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler`, clean); the pre-ACK handler change
  in `internal/daemon/ingest.go` remains **uncommitted (M)**. → 1.5.7, 1.5.12, §3.14. Alters the
  measured B-A window; numeric budget stays a diagnostic (E-2). `implemented_unverified` pending Main's
  re-measure.
- **Prompt replay / recovery (SP08-D3) — now LANDED, was "under authorship" in the draft:** core
  committed at `62f268f`/`46613fd`/`c78f610` — `internal/store/prompt_recovery.go`+`_test.go`,
  `internal/observer/prompt_restart_v6_test.go`, `internal/daemon/drain_prompt_capture_test.go`,
  `prompt_record_test.go` (`TestObservePrompt_*`) all committed/clean; the old negative-control
  `TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured` was **inverted** to
  `TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero`. Later `(M)` refinements to
  `internal/observer/prompt_delivery.go` and `internal/store/publication_sync.go` remain uncommitted.
  → 1.8.6. `implemented_unverified` on the candidate. Verbatim-across-crash is now recovered, but the
  **delivery-journal rollover is NOT accepted** (coordinator decision), so nothing here promises
  rollover; a drained prompt reaching an over-capacity journal has no rollover guarantee.
- **Bounded leased-delivery ordering + terminal completion (SP08-D3 issue 1) — WORKING-TREE
  (uncommitted):** `internal/daemon/delivery_order.go`+`_test.go`,
  `internal/daemon/delivery_terminal.go`+`_test.go`+`_integration_test.go`;
  `internal/daemon/prompt_order_v6_test.go`, `internal/observer/derived_publication_v6_test.go`.
  Every leased observer event publishes only after every earlier leased arrival of the same session
  reaches the committed frontier (fail-closed on an unreadable frontier); a leased-then-denied line is
  preserved pending via a durable terminal disposition (`j.terminal[]`) without fabricating a capture
  ACK. → 1.8.6 (turn-zero ordering), delivery-journal invariants. **Not a host-first-order claim**;
  SP08-D2's original failure fixture is preserved, not re-fixtured. `implemented_unverified`.
- **Delivery-journal rollover (SP20-D4) — WORKING-TREE (uncommitted), rollover NOT accepted:**
  `internal/daemon/delivery_generation.go`+`_test.go`+`_wiring_test.go`,
  `internal/daemon/delivery_radix.go`+`_test.go` (child author). Version-gated segmented generation +
  radix addressing toward capacity rollover; the 65536/64MiB journal cap otherwise stops leases.
  Bounded memory yes, **bounded storage / in-place rollover explicitly NOT accepted** by the
  coordinator — present as source only, promises nothing. → SP-20 capacity, 1.8.6 rollover caveat.
  `implemented_unverified`/`unknown`.
- **Publication fsck (V6-RECOVERY-1 detection) — WORKING-TREE (uncommitted):**
  `internal/cli/fsck_publication.go`+`_test.go` layered over the committed `80a3e04` startup/status
  accounting. **Detection, NOT recovery.** → 1.17.15, §3.6. `implemented_unverified` pending packaged run.

## 6. Retirement / replacement reasoning (explicit)

- **Unsafe native eviction / native control — RETIRED (E-1, §3.5, 1.15.7).** No native compaction
  veto, no O(delta) native latency, no guaranteed first-turn savings, no native ephemeral-eviction.
  Replacements: `runtime.migration.compaction.automaticVeto=false` (refused,
  `TestCanary_CompactionBlocking`); `blockManualCompact` hardwired false; scheduler advisory only;
  §3.5/1.15.7 native ephemeral-eviction → representation policy
  `TestPropose_ChoosesAtMostOneRepresentationPerItem`; admission ships disabled → pass-through, output
  unmodified (`TestTheShippedComparisonIsInconclusive`, `TestDisablingAdmissionCannotDisableRecording`).
  Restoring the native-control tests would contradict the current criterion; **not recommended.**
- **Exactness guarantees — the *approximation/optimality* guarantees are retired; *byte-exact
  recovery* is retained.** The submodular **(1−1/e)** approximation bound (1.15.6) and the guaranteed
  savings/O(delta) promises are retired to a *feasible-consumer* criterion with diagnostics/bounded
  warnings (F-3): `SubmodularEnabled=false`, substance held by
  `TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer`; the absent
  `PropGuaranteeUnitCost`/`PropGuaranteeKnapsack` are retired-with-the-guarantee, **not gaps.** By
  contrast, **byte-exact** canonicalization inverse (1.4.7 `TestRestore*`/`FuzzRestore`), Merkle
  domain-separation (1.4.4) remain compatible correctness regressions. Row 1.6.18 instead
  requires an assembled representation estimate and calibration; exact chunk-token additivity is
  retired by V6 section 5. Legacy chunk-sum tests remain diagnostics, not acceptance proof.
- **Universal performance promises — DIAGNOSTIC, not acceptance (E-2).** B-A p99<15ms, B-B<2ms,
  B-E<2s, B-F p95<250ms, dedup ≥4.0, 4:1, sublinear growth, fraction-of-OPT: all **retained as
  measured-and-reported**, re-measured with binary/provider/OS/payload context, never a universal
  target. Affected IDs: 1.1.27, 1.2.12, 1.4.14, 1.5.12, 1.6.16/17/19, 1.7.7/10, 1.8.10/11/13,
  1.9.11/12, 1.10.16/17/18, 1.11.15/16, 1.12.14/16/17, 1.13.16, 1.15.12/13/14, 1.16.5/11, 1.17.3/5/6.
  Hosted-runner fsync tail is a known measurement artifact (owner decision Q1); do not re-derive it
  into a constant.
- **Other retirements (recorded, not re-added):** 1.1.28 unchanged-`Qompack.md` retired (design doc
  now v1.5 with a Revision log; the v1.4-vs-v1.5 baseline discrepancy is recorded for the plan owner,
  E-3); 1.17.14 §12.3 nine-row decomposition replaced by `test/fault/*` (F-4, still owes a per-row
  mapping); 1.18.x fixed-count/verbatim/name assertions replaced by lighter current checks (F-5);
  `tools/devtool/configdocs` removed, generator relocated (F-6); 1.16.10 prefix-reorder non-delivery
  is a declared ADR statement (F-8, `docs/adr/0016`).

## 7. Unresolved rows returned explicitly

Rows Main must settle with a run or a human step (not resolvable from committed source alone):

- **Failed (execution.tsv), fix now COMMITTED, security re-run owed:** `1.13.4` (V6-AUTH ID/authority,
  `c9b5251`), `1.17.12` (archive authorization / full security gate, `c9b5251`/`00e0c98`). The
  historical FAIL is preserved; committing the fix does not clear it.
- **unknown (human/CI step, not resolvable from source):** `1.18.12` (human UAT-01…12, mandatory,
  NOT-RUN — preserved as a human-unexecuted identifier), `1.17.19` (four CI jobs — needs
  repo/CI/branch-protection inspection).
- **`1.8.6` reclassified `implemented_unverified` (was "in flight"):** SP08-D3 verbatim prompt-replay
  recovery has **landed** (`62f268f`/`46613fd`/`c78f610`) with the inverted regression; the leased-delivery
  ordering + terminal-completion pieces remain WORKING-TREE. Verbatim-across-crash recovered; the
  delivery-journal rollover is NOT accepted, so no rollover promise. Owed: Main's run on the candidate.
- **partial / open confirm item:** `1.1.24` (F-1 SP-15 guard-rename divergence — coordinator ruling:
  retire the historical rename expectation), `1.5.15`/`1.13.14` (F-2 contract producers wired; real
  observation NOT-RUN — confirm via `self-test`/`status`), `1.10.18` (residual-span ≥30% — two
  `replay --phase 4` runs), `1.15.2` (F-3 verify), `1.18.1/3/4/8/9/10/11` (SP-18 restructure — current
  substance held; historical sub-assertions have no standalone test).
- **partial_verified (focused only, full gate not run):** `1.13.17`, `1.17.7/8/9/15/16/17/20`,
  all of `1.18.1–1.18.11/13/14` (docs-integrated focused checks; human workflow unverified).

All other rows are `implemented_unverified` (source present, full current assertion not executed —
a focused prerequisite failure blocks the release evaluation, per execution.tsv).

## 8. Standing statement

This is a scope/mapping inventory of the current tree, not runtime assurance. No historical PASS is
copied; no remediation case is asserted PASS/FAIL. The base-committed symbols are inventory.md's
already-cross-checked current functions; source_paths are derived; the remediation-overlay rows are
`implemented_unverified`/`unknown` whether or not their source has landed. Main attaches the keyed run
overlay (`execution.tsv` successor) and independent review after this handoff.

## 9. Reconciliation report — preserved / gaps / new changes / limitations

**IDs preserved.** All **304** original inventory IDs (`1.1.1`–`1.18.14`) retained, one TSV row each,
**zero dropped, zero duplicated, zero renumbered** (verified: 304 data rows, 304 unique IDs). Every
row keeps its `original_assertion_reference` (historical criterion) beside `current_assertion`
(current-owner criterion); the old→new mapping is unchanged. The SP19/20/21 additive gates (§3, nine
switches) and the fourteen §3 integration identifiers (§4) are carried alongside, **not** merged into
the 304. UAT identifiers (`1.18.12` UAT-01…12) are preserved as **human-unexecuted**.

**Six-class taxonomy crosswalk** (brief's classes ← this inventory's dispositions):
- `documented` — assertion described in current docs/ADR but with no standalone current test: the
  F-5/F-8 doc-decomposition rows (1.16.10 ADR statement; parts of 1.18.x historical sub-assertions).
- `implemented_unverified` — source present (landed **or** working-tree), current assertion NOT
  executed on the candidate: the dominant class; every remediation-overlay row (§5) and all rows whose
  `limitation` carries execution.tsv's focused-prerequisite-failure note.
- `verified_in_target` — **none claimed.** No row is certified against the installed/packaged target;
  focused passes do not certify a final installed package. This is the headline limitation.
- `unknown` — not resolvable from source: `1.18.12` (human UAT), `1.17.19` (CI/branch-protection).
- `experimental` — shipped-disabled, refused-until-gate: SP-15/16/19/20/21 migration switches
  (`experiments.enabled`, `pselection.submodularEnabled`, `grammar.loopWarningsEnabled`, admission
  `newResult`), recorded disabled, never "passed."
- `unsupported` — retired-by-criterion-change (§6): native compaction veto / native ephemeral eviction
  (E-1), the (1−1/e) approximation and guaranteed-savings promises (F-3), universal performance targets
  demoted to diagnostics (E-2); plus the native Read-permission product decision left to Main.

**Original mapping gaps (unchanged from inventory.md, restated).** §4 genuine gaps:
**packaged-bundle-live** (§3.1/3.3/3.4/3.6/3.9/3.10/3.11/3.12/3.13 — all "packaged/against-shipped
NOT-RUN") and **human/held-out evaluation** (`1.18.12`). §3.11 archive-auth history is FAILED
(now fixed-but-unverified). §7 lists the rows Main must settle with a run or a human step.

**Actual new changes since the draft (base `301a8e9` → current snapshot).**
1. Remediation source that was "working-source/under authorship" in the draft has **landed as commits**
   (ancestors of HEAD `c95b7af`): V6-AUTH (`c9b5251`), capture-scope refusal (`00e0c98`),
   observation-binding SP08-D2 (`8f2750a`/`99108a2`), prompt-replay recovery SP08-D3
   (`62f268f`/`46613fd`/`c78f610`), maintenance recovery (`3ab1523`), backup CLI (`4a12eff`),
   publication accounting (`80a3e04`), bounded object reads (`19344e3`), journal migration anchors
   (`c95b7af`). Disposition is **unchanged** — `implemented_unverified` — because none was run on the
   candidate. Eight TSV `WORKING-SOURCE:` markers were re-labelled `LANDED verify/v6 (<commit>)`.
2. The SP08-D3 negative control was **inverted**:
   `TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured` →
   `_ReplayedPromptIsCapturedAtTurnZero` (owner changed the criterion; recorded, not a silent pass).
3. **New uncommitted working-tree families** appeared that did not exist in the draft: bounded
   leased-delivery ordering + terminal-completion (`delivery_order`/`delivery_terminal` + the
   `prompt_order_v6`/`derived_publication_v6` regressions), journal rollover (`delivery_generation`/
   `delivery_radix`, SP20-D4), and publication fsck (`fsck_publication`). In-place `(M)` edits:
   `ingest.go` (SP20-D6 pre-ACK), `prompt_delivery.go`, `publication_sync.go`, `maintenance.go`.
4. SP20-D6's guard test committed (`c78f610`) while its `ingest.go` implementation stays `(M)` — the
   two rows (1.5.7/1.5.12) now name that committed/uncommitted split explicitly.

**Limitations / missing target evidence (this is NOT a final RC).**
- The snapshot is HEAD `c95b7af` **+ an uncommitted working tree**; the source is still changing (main
  store/observer, child daemon capacity). The delivery ordering/terminal/rollover and fsck-publication
  families, and the `(M)` hot-path/prompt/maintenance edits, are **not committed** and may change.
- **No `verified_in_target` row.** Nothing has been executed on the candidate in this pass (the brief
  forbids test/build/replay here); raw focused passes elsewhere do not certify the installed package.
  All PASS/FAIL evidence is owed to Main's keyed run overlay and independent review.
- **Delivery-journal rollover is NOT accepted** (coordinator): the `delivery_generation`/`delivery_radix`
  source is present but promises no capacity rollover; over-capacity behaviour remains "leases stop."
- Historical FAIL rows (1.13.4, 1.17.12) stay FAILED until re-run; committing a fix does not clear them.
- `source_paths` are **derived** from cited `symbol · pkg` tokens (family-default fallback where no
  package is cited) — indicative, confirmed by Main on the candidate, not authoritative.

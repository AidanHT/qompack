# V6 packaging-readiness map — requirement → entry point

Scope/mapping only. **No release or readiness claim. No test/build/benchmark/installer/generator/
host/probe was executed; read-only source/CLI inspection.** Entry points are cited from current
source; existing artifacts are diagnostic, never copied as a new PASS. Commands are the invocation
shape, not a run record — Main runs them and joins evidence.

**Standing platform fact (`docs/release.md §3`, generated).** Only `windows/amd64` carries historical SP17 committed
records (`installed-verified` for those recorded artifacts); `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/arm64` are
cross-compile-only and read `unknown`. Every "packaged-live", "installed-host" and "human" row below
is NOT-RUN on this tree regardless of the unit mapping.

**Standing human/external dependencies.** An installed Claude Code host (per OS) for every
`[installed-host]` row; a human executor for all twelve UAT scenarios; a published release + a
current registry-name primary-source lookup for the release rows (no web lookup performed here, so
name/registry availability is **unverified**); a CI runner for the release-workflow rows (no workflow
has ever run — `docs/release.md §7`).

## A. §3 cross-component integration (3.1–3.14)

Command shape unless noted: `go test ./<pkg> -run '<Test>' -count=1 -timeout=30m` (add
`GOMAXPROCS=2` for the packaged `test/fault`/`test/e2e` suites, which assemble a bundle via
`go run ./tools/devtool bundle`). Source of the mapping: `plans/sdd/V6-VERIFY/inventory.md §D`.

| §3 | requirement (short) | current entry point(s) · path | missing / external dep |
|---|---|---|---|
| 3.1 | packaged bundle observes a real session | `TestCanary_HostInventory`, `_RegisterIsInternallyConsistent` · test/canary; `TestV5_DegradedPassiveIsStillCorrect…` · test/e2e | packaged-live disposable session `[installed-host]` NOT-RUN |
| 3.2 | universal launcher preserves hook semantics | `TestPlatform_WindowsHookLauncherForms`, `_PluginRootWithSpacesAndUnicode`, `_ReadOnly*` · test/platform; `TestCanary_CompetingHooks`, `_InvalidHookPayload` · test/canary | non-Windows launcher forms unverified |
| 3.3 | documented commands run against shipped bundle | `TestCanary_PluginValidate` · test/canary; `TestGenCommandDocs_MatchesTheInstalledHelp` · tools/devtool (source-derived) | against-shipped-artifact `[installed-host]` NOT-RUN |
| 3.4 | elimination staleness survives packaging + MCP | `TestV3_ObserverFileVersionsDriveEliminationStaleness` · test/e2e; `TestCanary_MCPLauncherDiscoversTools` · test/canary | upgrade-survival NOT-RUN |
| 3.5 | ephemeral results evicted first — **native eviction RETIRED** | `TestPropose_ChoosesAtMostOneRepresentationPerItem` · analyzer; legacy `TestV4_EphemeralRetrievalResultsRankFirstForEviction` · test/e2e | none — retirement, not a gap (E-1) |
| 3.6 | fsck repairs seeded corruption + startup accounting | `internal/cli/fsck_test.go`; `TestFault_AuditSeesADeletedObjectUnderALiveIndex`, `TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence`, `TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus` · test/fault | packaged fsck live NOT-RUN; detection≠recovery |
| 3.7 | doctor agrees with status + subsystems | `TestDoctor_AgreesWithStatusOnModeAndProvenance`, `_ReportsCapabilityEvidenceWithoutInventingIt` · cli | live capability canaries NOT-RUN |
| 3.8 | config reference describes the binary | `TestGenConfigDocs_LeavesMatchDefaultsOneToOne` · tools/devtool; `TestUserGuideCoversEveryGeneratedCommandAndTool` · test/docs | — |
| 3.9 | install/upgrade/uninstall byte-identical + rollback | `TestInstall_HostCLIInstallUpgradeUninstall` · test/e2e; `TestBackupCLI_*` · cli; `TestMaintenance_*`, `TestRollbackDrill_*` · store | full packaged install `[installed-host]` NOT-RUN; old-release matrix NOT-RUN |
| 3.10 | degraded-passive from packaged bundle | `TestV5_DegradedPassiveIsStillCorrect…` · test/e2e; `TestPlatform_UnknownSettingsVersion`, `_UnsupportedOptimizationsDisabled` · test/platform | packaged unknown-schema `[installed-host]` NOT-RUN |
| 3.11 | no secret / no network full packaged + authorization | `TestSecurity_NoSecretReachesAnyDurableSurface`, `_TelemetryCannotBeTurnedOn`, `_MCPServerNeverImportsOSExec` · test/security; `TestV6_ArchivedReadRetainsItsAuthorizationBoundary`, `_HashAddressesDoNotBypassPathAuthorization` · test/security; capture-scope suites (§C) | archive-authorization was **FAILED**; V6-AUTH fix landed (commit c9b5251); security-packaged-first passed focused dirty-build probes; final artifact verification remains owed; full-session live NOT-RUN; **host native-Read parity unverified** |
| 3.12 | checkpoint↔rehydration round trip (bundle) | `TestE2E_SessionStartCompactAfterFailedSummary`, `TestE2E_Checkpoint*` · test/e2e; `TestFault_CheckpointDropsAnUnresolvablePointer` · test/fault | bundle round-trip NOT-RUN |
| 3.13 | release artifacts reproducible | `TestAssembleBundle_Deterministic`/`_ChecksumsFormat`, `TestWriteArchiveChecksums`, `TestReleaseCheckDeterminismVersion` · tools/devtool; `TestBundle_OnDiskMatchesGenerator` · pluginmanifest; `TestCanary_PackagingShape` · test/canary | real-binary reproducibility run NOT-RUN (no `devtool package` cmd; assembler is the entry point) |
| 3.14 | hot path holds every subsystem resident | `TestIntegration_HotPathWarmWithRealResidentState`, `_HotPathDegradesRatherThanBlocks` · test/integration | measured latency on a quiet runner NOT-RUN; SP20-D6 pre-ACK correction (commit c78f610) re-measure owed |

## B. SP17-M7 acceptance rows (packaging/release)

Requirements verbatim from `plans/V6-SP-17-packaging-hardening-and-release.md`. Historical dispositions
from `docs/release.md` "Acceptance rows" (generated from SP-17 records — diagnostic only).

| row | requirement (short) | current entry point(s) · path | command shape | status / missing dep |
|---|---|---|---|---|
| M7-01 | installed launcher/manifest/tool discovery on host/OS matrix; identifiers+checksums | `TestInstall_HostCLIInstallUpgradeUninstall` · test/e2e; `TestCanary_HostInventory`, `_PluginValidate`, `_PackagingShape` · test/canary | `go test ./test/e2e ./test/canary` | windows/amd64 `installed-verified`; other OS + real host manifest resolution `[installed-host]` NOT-RUN |
| M7-02 | spaces/Unicode/case/long-path/managed correct; unsupported optimizations disabled | `TestPlatform_ProjectRootShapes`, `_ReadOnly*`, `_MixedCaseProjectRoot`, `_PluginRootWithSpacesAndUnicode`, `_UnsupportedOptimizationsDisabled` · test/platform | `go test ./test/platform` | windows/amd64 verified; other targets `unknown` |
| M7-03 | denied/symlink/secret/archive/decompression leak-free and bounded | `TestSecurity_*` · test/security; `TestV6_ArchivedReadRetainsItsAuthorizationBoundary`, `_HashAddressesDoNotBypassPathAuthorization`; capture-scope suites | `go test ./test/security` | historical record **FAILED** (`posture_out_of_project_capture_is_archived`); V6-AUTH fix landed (c9b5251) → `implemented_unverified`, security re-run owed |
| M7-04 | forced failure at each publication boundary → recoverable or explicit-incomplete; no new dangling | `TestFault_PublicationBoundaries` (F4-1/F4-4 rows), `TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence`, `TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus` · test/fault | `GOMAXPROCS=2 go test ./test/fault -timeout=30m` | historical record **FAILED** (`capture_sidecar_stage_one_only`); publication accounting landed (80a3e04) — **detection, not recovery**; `implemented_unverified` pending packaged run |
| M7-05 | old readers/import restart/rollback before+after new writes; backup + identity parity | `TestRollbackDrill_*`, `TestBackup_RestoreOpensAsARealStore` · store; `TestBackupCLI_*` · cli; `TestMaintenance_*` · store | `go test ./internal/store ./internal/cli` | legacy import/cutover **gated closed**; old-released-reader matrix + human parity NOT-RUN |
| M7-06 | independent kill switches stop their feature; unknown schema degrades; uninstall retained-data choice | `TestSwitch_GatedCapabilitiesAreRefusedNotDisabled` · test/release; `TestPlatform_UnknownSettingsVersion` · test/platform; `TestInstall_…Uninstall…` · test/e2e | `go test ./test/release ./test/platform ./test/e2e` | uninstall/upgrade `[installed-host]` NOT-RUN |
| M7-07 | release benchmark: primary/constraint/recovery outcomes, uncertainty, complete-or-unknown accounting | `internal/eval/*`, `internal/commands/cmd_eval.go` (usage/ledger) | `qompack eval --json` (reports "no evaluation artifacts are readable in this build", exit 1) | **live-task layer excluded** (`eval.LiveRunner` nil, ruling R7-2); held-out/changing-requirement eval is a human/model obligation NOT-RUN |
| M7-08 | platforms/licenses/version/name checks + rollback rehearsal attached; no unsupported feature advertised | `go run ./tools/devtool release-check`; `TestReleaseCheck*`, `TestValidateBundleVersion`, `TestGoModVersions` · tools/devtool; `RehearseRollback`→`RollbackDrill` · store | `go run ./tools/devtool release-check [--tag vX.Y.Z]` | no workflow has run; **registry-name/tag availability not looked up → unverified**; release-tag vs `core.Version`=0.1.0 vs last tag disagree |

## C. SP18-M7 acceptance rows (docs/UAT) — owned docs are the deliverable

Requirements verbatim from `plans/V6-SP-18-documentation-and-uat.md`. Owned docs listed are the ones
in this seat's editable scope; test/docs enforces their shape.

| row | requirement (short) | owned doc(s) + enforcing test · path | status / missing dep |
|---|---|---|---|
| M7-01 | every capability/residual/risk explained + tested fallback; requirement-to-doc map | this map + `docs/user-guide.md`, `docs/security.md`, `docs/troubleshooting.md`; `TestOwnedDocsExist`, `TestADRIndexListsEveryADR` · test/docs | met at doc level; capability truth still `implemented_unverified` pending Main's run |
| M7-02 | config docs exactly cover source keys/ranges/defaults/deprecations; stale-detection | `docs/config-reference.md` (generated); `TestGenConfigDocs_CommittedPageIsCurrent`, `_LeavesMatchDefaultsOneToOne`, `_GatedSwitchesRenderFromSource` · tools/devtool | generator NOT-RUN here; check owed |
| M7-03 | seven commands/eight tools + retained IDs match installed package (not plan) | `docs/user-guide.md`; `TestUserGuideCoversEveryGeneratedCommandAndTool` · test/docs; `TestGenCommandDocs_*`, `TestGenMCPDocsCheckDetectsDrift` · tools/devtool | against-installed-package comparison `[installed-host]` NOT-RUN |
| M7-04 | links/anchors resolve; proposed paths labeled; no stale fixed doc/job counts | all owned docs; `TestRelativeLinksResolve`, `TestNoPlannedPointerToAnExistingPage` · test/docs | check owed |
| M7-05 | UAT-01–12 executed by a human on the supported artifact, or explicit skip | `docs/uat.md`; `TestUATRetainsAllTwelveIDs`, `TestUATSectionsHaveTheRecordShape`, `TestUATUnexecutedRowsSayUnverified`, `TestUATConfigKeysExist` · test/docs | **all 12 human runs NOT executed**; every Result block reads `not executed — capability unverified` |
| M7-06 | docs + UAT describe/report backup, independent switches, rollback before/after new writes consistently | `docs/backup.md`, `docs/security.md §9`, `docs/release.md §4/§5`, `docs/uat.md`; cross-checked by the above | consistent at doc level (point-in-time same-build restore vs old-release/post-new-write downgrade kept distinct); operator run NOT-RUN |
| M7-07 | claims omit unsupported control/performance/price/completeness guarantees; independent reader review | `docs/release.md §7`, `docs/security.md §8`, `docs/cannot-do.md`; enforced by `TestCannotDoCoversSection12Limits`, `_NamesTheUnimplementedChecks` · test/docs | doc claims clean; **independent human reader review** is the open external dep |

## D. Missing entry points and terminology notes

- **No completed live-host release evidence is established here for:** packaged-bundle-live sessions (3.1/3.3/3.9/3.10/3.11/3.12/3.13),
  human UAT (SP18-M7-05 / 1.18.12), held-out/changing-requirement model evaluation (SP17-M7-07 live
  layer — `eval.LiveRunner` deliberately nil), the four required CI jobs (1.17.19 — needs repo/CI
  inspection), and a current-registry name/tag availability lookup (no web primary-source lookup
  performed → unverified). Historical `devtool install-check` and `devtool security-audit` commands
  were **removed**; their obligations are realised by `test/e2e` install and `test/security`
  respectively (inventory §1.17.7/1.17.12).
- **Recovery terminology, kept distinct throughout the owned docs and this map:**
  *point-in-time, same-build restore* (`backup restore` into a fresh destination, verified by this
  build, source and later writes retained, no activation) is **not** *old-release compatibility* or
  *post-new-write downgrade* (legacy import/cutover, gated closed; `AutomaticDowngrade` is always
  false). A clean integrity report certifies the checks it ran, never an older reader.
- **Prompt worker replay / recovery limit.** Worker/drain capture, delivery-bound prompt
  digests, durable turn recovery and publication sync are implemented in `46613fd`, `62f268f`
  and `c78f610`. Parent runs `prompt-recovery-and-lease-focused`,
  `prompt-publication-sync-contracts` and `integrated-recovery-capture-packages` passed
  the observer cases; the broader package run also retained unrelated failures, subsequently
  corrected in `integrated-regression-fixture-corrections`. These are source-level diagnostics.
  Concurrent delivery order, inherited derived tool/stop publication cuts, old unlinked prompt
  records and installed-host first-prompt recovery remain separate obligations.
- **Delivery journal.** 65,536-entry / 64 MiB admission bound; **automatic rollover is not
  implemented and must not be promised** (`docs/backup.md`, `docs/troubleshooting.md §9`). On the
  bound, new identified deliveries stay pending and report unavailable; they must not be processed as
  identity-free replacements; existing identities stay readable; the recovery is preserve-and-migrate,
  not manual journal deletion.
- **Host native Read authorization parity remains unverified.** Project containment plus capture-time
  policy is **not** parity with a live/managed/command-line host permission decision
  (`docs/security.md §1/§8`). A containment test establishes no host-permission claim.

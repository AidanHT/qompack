"""The fixed prose of plans/sdd/V6-closeout/inventory-c8-map.md; dispose_c8.py adds the generated tables."""

HEAD = """# V6 inventory on candidate 8 (C6.2)

Workstream `c6-final`, 2026-10-08, on `closeout/c6-final` (cut from `develop` `7fbb8a40`). This map gives
every one of the 304 inventory rows a disposition from candidate 8's own evidence, the candidate release
0.3.0 was cut from (D79, D80). It extends `inventory-c6-map.md` (candidates 6 and 7), which stays as
history; codes, readings and rulings that page defines keep their meaning there.

**What changed in `inventory-current.tsv`.** Two columns are appended to the eleven that were there:
`c8_result` and `c8_evidence`. No row was added, dropped or renumbered (304 ids, one row each), and the
eleven existing columns, the seven original ones and the c6 and c7 columns, are byte-for-byte as they
were (`c6-final/dispose_c8.py` checks it before it writes). The table and this page are written by
`c6-final/dispose_c8.py` from `inventory-map.tsv` (D37's evidence-step map) and the per-row decisions in
that script; this page's fixed prose is `c6-final/map_text.py`.

Paths below are relative to `plans/sdd/V6-closeout/`.

## Candidate identity

- Candidate 8: `verify/v6` `3ec62ad2e01b985640c0f1fb832df3917f766a5f`, the freeze merge of
  `closeout/integration` `e8c62191` (tree `4f3deaf4`), its second freeze (`phase3/c8-CANDIDATE.md`;
  `phase3/c8/night.log`, "candidate 8 frozen at"). Bundles `qompack-bundles/c8`, windows-amd64
  `BUNDLE.json` sha256 `61ba9c37...dcd8b`, `bin/qompack.exe` sha256 `93eb09f3...81a9`.
- The pre-freeze check ran on `e8c62191`, and no file outside `plans/` differs between it and the
  candidate (`c6-final/runs/c8-identity-proofs.txt`, section 1), so the pre-freeze logs are runs of the
  candidate's product and test code.
- The release: tag `v0.3.0` on `1a368a4b` (D80). Between the candidate and the tag commit, the bundle and
  test paths (internal, cmd, plugin, go.mod, go.sum, `.goreleaser.yaml`, tools, test) differ only in two
  deleted lines of `test/guards/releasenotes_test.go`, the release-notes interim list (proof section 2);
  `develop` `7fbb8a40`, this branch's base, is the same (section 3). Every published `bin/` is
  byte-identical to the frozen one (`phase3/c8/release-bin-compare.txt`).
- Candidate 7 to candidate 8: 257 files outside `plans/` changed, 63 non-test files under internal/,
  cmd/ and plugin/ (proof section 4, with the file list). Every row is therefore judged on candidate 8's
  own runs, not carried, and each cell says whether the row's source paths changed c7->c8.

## Vocabulary and rule

The file's own vocabulary, as on candidates 6 and 7: `verified_in_target`, `partial_verified`,
`implemented_unverified`, `failed`, `unknown`, `unsupported`, `documented` (and `experimental` for the
shipped-disabled switches below).

- `verified_in_target`: every evidence step the row names (D37 map) has an executed artifact on
  candidate 8 and it is green. A live step written as alternatives (`C4.3 / UAT-03`) is met when a
  scenario it names ran on candidate 8 and passed; the cell names the alternatives that did not run
  there. A benchmark row whose guarantee was superseded (`disposition` `superseded-guarantee`) is
  verified by its measurement on candidate 8, and the cell says where a figure is over its old budget
  and which ruling accepted that.
- `partial_verified`: every automated step is green on candidate 8, and a further part was not re-run on
  it (a live scenario carried from candidate 7), has no possible artifact, or is a Linux fsync-bound half
  that is not verified in target by rule; the cell names the part.
- `unknown`: no step can produce an artifact (D37(c)); never counted as a pass.
- `unsupported`: retired by a recorded criterion change (E-1, F-3, D36). `documented`: a meta or prose
  row with no test by design.

Not verified in target, by rule, as before: Linux fsync-bound rows (B-A, B-B) on the Docker Desktop
container (D53(b)); hosted-runner fsync figures are reports (Q1); Windows reference timing runs on AC
(D57(d)), and the Windows hot-path rows are judged by the designated quiet C5.1 run (D57(e)). Every
candidate 8 timing step ran on AC and was VALID (`phase3/c8/chain.log`, `overnight-outcome.txt`:
`invalid_power=0`).

No row carries a candidate 5, 6 or 7 measurement: all eight C5.2 chunks ran on candidate 8 (D75(b),
D78(a)), so D57(e)'s carry and the three readings of `inventory-c6-map.md` are no longer needed.

## Evidence codes

Hosted job ids are GitHub Actions jobs of ci.yml run `37562946379` (attempt 2, concluded success) and
nightly run `37562945914`, both on head `3ec62ad2`, and release.yml run `37738581717` on `1a368a4b`
(`c6-final/runs/c8-hosted-runs.txt`, read with `gh run view`).

| code | artifact | state |
|---|---|---|
| `FREEZE8` | `phase3/c8-CANDIDATE.md`, `phase3/c8/night.log` (freeze line, the six `bin/` sha256, bundles host-validated) | identity |
| `W8` | Windows whole tree on candidate 8's code: `phase3/c8/prefreeze/` (`summary.log`: gate, integration, testpkgs, internal and e2efunc, every step exit 0 and VALID on AC; run `c8-20261007T020207Z-3312` on `e8c62191`, product-identical); the overnight `win-race` step (`devtool test-race`, every non-e2e package, `phase3/c8/chain.log` "step win-race finished exit=0"); hosted `test (windows-latest)` 112617576326 at `-count=2`, green on attempt 2. Attempt 1 ended internal/daemon's binary at 1185 s with a runtime-class crash whose cause is undetermined and did not recur (D75(c), `phase3/c8/hosted/windows-crash.md`) | green |
| `L8` | the overnight container `linux-tree` step (non-root, `-race`, exit 0); hosted `test (ubuntu-latest)` 112617607869 and `test (macos-latest)` 112617576573 (attempt 2; attempt 1's macOS red was TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes's fixture-sanity count, a test defect, D75(c), `phase3/c8/hosted/macos-settle-rootcause.md`) | green |
| `LE2E8` | the container `linux-e2e` step (`-race`, exit 0) and `linux-e2e-timing` (exit 1: test/e2e 336 pass, 1 fail, X11, B-A/B-B only, fsync-bound, D53(b)); hosted `test-e2e` ubuntu 112617600107, macos 112617577144, windows 112617615389 | green but Linux X11 (D53(b)) |
| `CHILD8` | the container `linux-child` step (exit 0); nightly `race-product-child` 112604139130 | green |
| `WRACE8` | the overnight `win-race` step; nightly `race-windows` 112604139160 | green |
| `FUZZ8` | nightly `37562945914`: 31 jobs, all success (25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded) | green |
| `GATE8` | `phase3/c8/prefreeze/gate.log`; `phase3/c8/merged-tree.txt` (plan lint, test/guards, test/docs on tree `4f3deaf4`); hosted `verify` 112617604957 and `lint-windows` 112617607237 | green |
| `DOCS8` | hosted `docs` 112617577665 (the three gen-*-docs `--check` and test/docs); test/docs in `W8` testpkgs | green |
| `COVER8` | hosted `cover` 112617608984; release-check's `ci-local cover` (`REL8`) | green |
| `SEC8` | hosted `security` 112617608087; release-check's govulncheck (`REL8`) | green |
| `XBUILD8` | hosted `crossbuild` 112617617843; release-check's build-all (`REL8`); the six-target freeze build (`night.log`) | green |
| `PV8` | hosted `plugin-validate` 112617607514; `phase3/c8/host-validate.txt` (`claude plugin validate --strict --json` accepted the frozen bundle) | green |
| `BUNDLES8` | the overnight `bundles` step (two builds byte-identical, `phase3/c8/bundle-diff.txt` empty); hosted release-dry-run's release-version bundles equal the frozen ones, 91 files (`phase3/c8/hosted-release-bundles.txt`) | green |
| `REPLAY8` | hosted `replay-gate` 112617607924; nightly `replay-recorded` 112604139040 | green |
| `WTIME8` | the overnight `win-timing` step (`p3-win-timing`, `p3-win-hotpath`, exit 0, VALID on AC) | green |
| `WE2ET8` | the overnight `win-e2e-timing` and `win-x11-alone` steps (test/e2e's timing rows and X11 alone, exit 0, VALID on AC) | green |
| `LTIME8` | the overnight `linux-timing` step (exit 1): only TestIntegration_HotPathWarmWithRealResidentState fails, B-A and B-B, fsync-bound, D53(b); every other row passes (`phase3/c8-CANDIDATE.md`) | green but the hot-path row |
| `C51-8` | quiet C5.1 on candidate 8, Windows on AC (`phase3/c8/quiet/c51-win.log`, `c51-win-bf.log`, `quiet-run.txt`): B-A p99 16.4 ms and B-B 11.3 ms against 50 PASS, B-D p99 88.5 ms reported, B-E p99 166.8 ms (B-E_cpu 46.9 ms) against 2000 PASS, spawn floor p50 12.5 ms; B-F p99 73.7 ms against 250 PASS. The Linux quiet run is report-only (D53(b)), and its `quiet-c51-linux/` records were not committed (`chain.log` names them) | Windows green; Linux report-only |
| `C52-8` | quiet C5.2 on candidate 8, all eight chunks, ten ABBA rounds against `cf31e01`, `paired.txt` and `completeness.tsv` (0 incomplete sides) in `phase3/c8/quiet-c52-{win-observer,win-store,win-checkpoint,win-other,linux-observer,linux-store}/` and `phase3/c8-c52/quiet-c52-{linux-checkpoint,linux-other}/`. Slower than the base with significance, recorded and not acted on: negknow Open (1.16x Windows, 1.34x Linux), symbols Enclosing_100KB (1.011x), Linux Finalize (1.32x), ConfigLoad_ColdNoFiles (1.037x) and CrossingEdges (1.018x), each inside its budget (D75(b), D78(a)) | green as a measurement |
| `REL8` | `release-check --tag v0.3.0` on candidate 8 on the reference host, in a scratch clone (`phase3/c8/release-check.json`): all 18 steps PASS, none skipped (version agreement, ci-local fmt-check, lint, vet, build, test, cover, plugin-validate, gen-config-docs, build-all, generated docs, guards, govulncheck, licenses, real-binary determinism, rollback rehearsal, plugin-validate, marketplace) | green |
| `CI8` | hosted ci.yml `37562946379` on `3ec62ad2`: all 21 jobs green (attempt 2 re-ran attempt 1's two reds, D75(c)), release-dry-run 112617577868 among them | green |
| `TAG8` | release.yml `37738581717` on tag `v0.3.0` (`1a368a4b`): release-check `--tag`, the assembly, the marketplace, the release notes and the goreleaser draft, success; published `bin/` byte-identical to the frozen ones (`phase3/c8/release-bin-compare.txt`, D80) | green |
| `REH8` | D80's HTTPS install rehearsal from the published pre-release (the ledger row D80 and docs/install.md section 9): Windows on the real profile at local scope (one session loaded 0.3.0 through the `qompack-windows-amd64` entry, its MCP server connected, the six commands listed, SessionStart, UserPromptSubmit, PostToolUse and Stop succeeded, then uninstall, marketplace remove and an unchanged real home) and in an isolated profile; Linux in the container with no model (the `qompack-linux-amd64` entry installed, `bin/qompack` kept `-rwxr-xr-x`, ran, and equals the frozen binary) | green |
| `LIVE8` | the live re-check on the frozen candidate 8 bundle (D76, `live/rerun-c8/`): UAT-02 (C4.2), UAT-04, UAT-05 run 2, UAT-06, UAT-07, UAT-08, UAT-11, UAT-12 steps 1-5 and 8; C4.3, C4.4, C4.5, C4.6, C4.7, C4.9 (b), C1.6, the C1.7 restore smoke and F-C48-1. 20 sessions, 650 hook calls, none with a host-reported failure or timeout (`live/rerun-c8/D53i/summary.md`, D53(i)); the audit's 5 minors became known issues 17 and 18 and doc corrections (D76(b)-(f)) | green |
| `CARRY-L7` | live scenarios not re-run on candidate 8, carried from candidate 7's pass with a carry-forward note naming the files changed since (D53(f)): C4.1 (also run on the released bytes, `REH8`), C4.8, C4.9 legs (a) and (c), C1.7's post-new-write restore, UAT-01, UAT-03, UAT-05 run 1, UAT-09, UAT-10 and UAT-12's upgrade leg (`live/rerun-c8/CARRIED.md`; the UAT notes are in docs/uat.md's Result blocks) | carried, not re-run |
| `C55-8` | C5.5 on the frozen candidate 8 bundle (D77; `eval/runs/c55-c8/`, `eval/c55-c8-notes.md`): run `20261007T184908Z-abe10e`, 40 of 40 trials, read as confirmatory; task success 18/20 on each arm, difference 0.000, interval [-0.214, 0.214]: the pre-registered verdict is inconclusive, which allows the release (A8 item 1) and is quoted, never claimed as a benefit | executed; verdict inconclusive |
"""

TAIL = """## The fourteen section 3 integration identifiers

Not renumbered into the 304. Every case named below exists at `3ec62ad2` (checked with `git grep` for
each `func Test...(` on the candidate; `c6-final/runs/map-symbols-at-c8.txt` shows the inventory map's
own symbols resolve as they did on candidate 6).

| section 3 | cases | c7 result | c8 result | candidate 8 evidence; what is open |
|---|---|---|---|---|
| 3.1 PackagedBundleObservesARealSession | TestCanary_HostInventory, TestCanary_RegisterIsInternallyConsistent, TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent | `partial_verified` | `verified_in_target` | W8, L8, LE2E8; the frozen c8 bundle observed 20 real sessions (LIVE8) and the released bytes one more through the marketplace entry (REH8) |
| 3.2 UniversalLauncherPreservesHookSemantics | TestPlatform_HookLauncherForms, TestPlatform_PluginRootWithSpacesAndUnicode, TestPlatform_ReadOnlyBundleDir and its ReadOnly siblings, TestCanary_CompetingHooks, TestCanary_InvalidHookPayload | `verified_in_target` | `verified_in_target` | W8, L8 (ubuntu, macos) |
| 3.3 DocumentedCommandsRunAgainstShippedBundle | TestCanary_PluginValidate, TestGenCommandDocs_MatchesTheInstalledHelp | `partial_verified` | `verified_in_target` | W8, DOCS8, PV8; C4.5 ran the six commands against the frozen bundle in a real session (LIVE8); known issue 17: status's description names a 'last decision' the page does not show (D76(b)) |
| 3.4 EliminationStalenessSurvivesPackaging+MCP | TestV3_ObserverFileVersionsDriveEliminationStaleness, TestCanary_MCPLauncherDiscoversTools | `partial_verified` | `partial_verified` | W8, LE2E8, L8; UAT-06 exercised record_eliminated and already_tried across a resume, a fork and a parent restart on c8 (LIVE8). Open: survival across an upgrade (C4.8, UAT-12's upgrade leg) was not re-run on c8 (CARRY-L7) |
| 3.5 EphemeralRetrievalResultsAreEvictedFirst | TestPropose_ChoosesAtMostOneRepresentationPerItem; legacy TestV4_EphemeralRetrievalResultsRankFirstForEviction | `unsupported` | `unsupported` | native eviction retired (E-1); both green (W8, L8, LE2E8) |
| 3.6 FsckRepairsSeededCorruption | internal/cli fsck tests, TestFault_AuditSeesADeletedObjectUnderALiveIndex, TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence | `partial_verified` | `verified_in_target` | W8, L8 (test/fault green on c8 on all three hosted OSes); packaged fsck on real stores ran on c8 (LIVE8: C1.6, the C1.7 restore smoke, UAT-04). Publication accounting is detection, not recovery; known limits: fsck beside a live daemon (D57(b)) and known issue 18 (D76(c)) |
| 3.7 DoctorAgreesWithStatus+Subsystems | TestDoctor_AgreesWithStatusOnModeAndProvenance, TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt | `verified_in_target` | `verified_in_target` | W8, L8; C4.5's paired status, doctor and fsck reads agree (LIVE8) |
| 3.8 ConfigReferenceDescribesTheBinary | TestGenConfigDocs_LeavesMatchDefaultsOneToOne, TestUserGuideCoversEveryGeneratedCommandAndTool | `verified_in_target` | `verified_in_target` | W8, L8, GATE8, DOCS8 |
| 3.9 InstallUpgradeUninstallByteIdentical | TestInstall_HostCLIInstallUpgradeUninstall, TestRollbackOrderIsFixed, TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite, internal/store TestMaintenance_* | `partial_verified` | `partial_verified` | W8, LE2E8; release-check's rollback rehearsal (REL8); install and uninstall on the released bytes (REH8). Open: the installed upgrade (C4.8, UAT-12's upgrade leg) was not re-run on c8 (CARRY-L7); TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI on hosted and container lanes |
| 3.10 DegradedPassiveFromPackagedBundle | TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent, TestPlatform_UnknownSettingsVersion, TestPlatform_UnsupportedOptimizationsDisabled | `partial_verified` | `verified_in_target` | W8, L8, LE2E8; from the packaged bundle on c8: C4.9 (b) (a newer `runtime.migration.settingsVersion` in the project config) and C1.6 (a daemon killed mid-session is taken over) (LIVE8); C4.9 (c), the unavailable object, is carried (CARRY-L7) |
| 3.11 NoSecretAndNoNetworkFullPackaged | TestSecurity_NoSecretReachesAnyDurableSurface, TestSecurity_TelemetryCannotBeTurnedOn, TestSecurity_MCPServerNeverImportsOSExec, TestV6_ArchivedReadRetainsItsAuthorizationBoundary, TestV6_HashAddressesDoNotBypassPathAuthorization | `partial_verified` | `verified_in_target` | W8, L8, SEC8; the full packaged session ran on c8 (LIVE8: C4.6, UAT-12 with planted credentials and deny-ruled files) |
| 3.12 CheckpointToRehydrationRoundTripBundle | TestE2E_SessionStartCompactAfterFailedSummary, the TestE2E_Checkpoint* rows, TestFault_CheckpointDropsAnUnresolvablePointer | `partial_verified` | `verified_in_target` | W8, LE2E8, CHILD8; real compactions on the frozen bundle (LIVE8: C4.3, UAT-04's eight compactions, UAT-05 run 2, UAT-06's six) |
| 3.13 ReleaseArtifactsReproducible | TestAssembleBundle_Deterministic, TestAssembleBundle_ChecksumsFormat, TestWriteArchiveChecksums, TestReleaseCheckDeterminismVersion, TestBundle_OnDiskMatchesGenerator, TestCanary_PackagingShape | `verified_in_target` | `verified_in_target` | BUNDLES8 (two builds byte-identical; hosted release-dry-run's equal), W8, L8; release-check's determinism step PASS (REL8); the published bin/ equal the frozen ones (TAG8) |
| 3.14 HotPathHoldsEverySubsystemResident | TestIntegration_HotPathWarmWithRealResidentState, TestIntegration_HotPathDegradesRatherThanBlocks, TestV3_HotPathUnchangedWithLedgerResident | `partial_verified` | `partial_verified` | Windows verified on AC: the integration row isolated (WTIME8) and X11 alone (WE2ET8) pass, and C51-8 passes (B-A p99 16.4 ms, B-B 11.3 ms against 50); TestIntegration_HotPathDegradesRatherThanBlocks green (W8, L8). Open half: Linux, where both rows fail B-A/B-B fsync-bound in the container (LTIME8, LE2E8), not verified in target (D53(b)) |

Counts: 10 `verified_in_target`, 3 `partial_verified`, 1 `unsupported` (candidate 7: 4, 9 and 1).

## SP-19, SP-20 and SP-21 switches

Shipped defaults from `internal/config/defaults.go`, which is byte-unchanged between candidates 7 and 8
(`git diff --stat d20309c0 3ec62ad2 -- internal/config/defaults.go` is empty), as is
`internal/daemon/delivery_generation.go`. The switch tests named in `inventory-c6-map.md`
(TestSwitch_GatedCapabilitiesAreRefusedNotDisabled, TestDoctor_EveryMigrationGateIsPendingInThisBuild,
TestGenConfigDocs_GatedSwitchesRenderFromSource, TestCanary_CompactionBlocking,
TestCanary_NewResultReplacement, TestSwitchIsOffByDefault and the internal/admission suite,
TestCapturePolicyToHookEnvelopeRoundTrip, TestCapturePolicyProducesTheCompleteFidelitySet and the
internal/state suite) all exist at `3ec62ad2` and are green on it (W8, L8).

| switch | owner / gate | default at c8 | c8 result | note |
|---|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | SP-20 M1-01 | false | `experimental` | refused until its gate; recorded disabled, never passed |
| `runtime.migration.publication.durableFrontier` | SP-20 M1-02 | false | `experimental` | refused until its gate |
| `runtime.migration.reinjection.sessionStartCompact` | injection kill switch | true | `verified_in_target` | the one enabled adapter: automated tests green (W8, L8); the live compaction round trip ran on c8 (LIVE8: C4.3, UAT-04), and so did the kill switches (C4.7, `live/rerun-c8/C4.7/` off and re-injection sessions) |
| `runtime.migration.replacement.newResult` | SP-21 M4 admission | false | `experimental` | admission off, output passes through unmodified |
| `runtime.migration.compaction.automaticVeto` | SP-19 M0-03 | false | `unsupported` | the native veto is retired (E-1); refused |
| `runtime.migration.compaction.blockManualCompact` | section 12 | false | `unsupported` | hardwired off |
| `runtime.migration.experiments.enabled` | SP-15/16 | false | `experimental` | disabled |
| `runtime.selection.submodularEnabled` | SP-15 | false | `experimental` | disabled; refused without p-selection |
| `runtime.selection.loopWarningsEnabled` | SP-15 | false | `experimental` | disabled; a reload reports it has no effect in 0.3.0 (D51) |

**Delivery-journal rollover (SP-20, not a config key)** ships enabled (`enableDeliveryGenerations = true`,
`internal/daemon/delivery_generation.go`, unchanged c7->c8), with D6 and D16's residuals in the release
notes' Known limits; SP20-D4 is `fixed`, and its evidence test is green on candidate 8 (W8, L8).

## Corrections to earlier records

- The c6 and c7 cells of 1.17.18 said actionlint runs at the tag. No actionlint step exists in
  release.yml or in release-check; the tag runs release-check `--tag`, the assembly, the marketplace,
  the release notes and the goreleaser draft (TAG8).
- 1.17.19 was to close on branch protection (C7.3). `develop` and `main` are now protected against force
  pushes and deletion, but neither requires a status check, so no ci.yml job is "required"; the row stays
  `partial_verified` (`c6-final/runs/c8-hosted-runs.txt`).
- The c7 column's pending parts (P-CI7, P-REL7, P-TAG, P-C52R, P-LIVE, P-C55) were never landed on
  candidate 7, which was superseded (D59). Candidate 8 has its own runs for each, and the c7 column is
  left as written.

## Rows that could not be verified in target on candidate 8

Every row that is not `verified_in_target` is in the two tables above with its reason. Grouped:
- Linux fsync-bound halves, not verified in target by rule (D53(b)): 1.5.12, 1.12.14, 1.17.6 and
  section 3.14.
- Live parts not re-run on candidate 8 (CARRY-L7): 1.17.8, 1.17.9 (with the unproven execution of
  TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting) and sections 3.4 and 3.9, all on the
  installed upgrade (C4.8, UAT-12's upgrade leg).
- No possible artifact: 1.11.16 (no rehydrate benchmark), 1.14.10 (no commandstest package or commands
  benchmark), 1.17.10 (arm64 and darwin/amd64 cross-compile only; no non-Windows Claude Code session,
  D34(c)), 1.17.14 (F-4's per-row mapping), 1.18.1, 1.18.3, 1.18.4, 1.18.8 and 1.18.11 (F-5), and the
  seven `unknown` rows.
- A repository setting: 1.17.19 (no required status checks).
"""

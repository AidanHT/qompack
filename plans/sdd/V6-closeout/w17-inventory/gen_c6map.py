"""Writes plans/sdd/V6-closeout/inventory-c6-map.md from the c6 columns of inventory-current.tsv.

The prose is fixed here; the per-row tables are generated, so the map and the TSV cannot disagree.
"""
import csv, os
from collections import Counter

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "..", ".."))
INV = os.path.join(ROOT, "plans/sdd/V6-remediation/inventory-current.tsv")
OUT = os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-c6-map.md")

rows = list(csv.reader(open(INV, encoding="utf-8", newline=""), delimiter="\t"))[1:]
assert len(rows) == 304 and len({r[0] for r in rows}) == 304
count = Counter(r[7] for r in rows)
order = ["verified_in_target", "partial_verified", "implemented_unverified", "failed", "unknown",
         "unsupported", "documented", "experimental"]


def cell(s):
    return s.replace("|", "/").replace("\n", " ")


def table(results):
    out = ["| id | current assertion | c6 result | evidence and what closes it |", "|---|---|---|---|"]
    for r in rows:
        if r[7] in results:
            out.append(f"| {r[0]} | {cell(r[2])[:90]} | `{r[7]}` | {cell(r[8])} |")
    return "\n".join(out)


HEAD = """# V6 inventory on candidate 6 (C6.2)

Workstream `w17-inventory`, 2026-10-02, on `closeout/w17-inventory` (cut from `closeout/integration`
`9a56b305`). This map gives every one of the 304 inventory rows a disposition from candidate 6's
evidence. It supersedes the candidate 1 snapshot language of `../V6-remediation/inventory-current-notes.md`
and the `65bc8d7` gate list of `../V6-remediation/current-candidate-gate-map.md`; both stay as history.

**What changed in `inventory-current.tsv`.** Two columns are appended to the seven that were there:
`c6_result` and `c6_evidence`. No row was added, dropped or renumbered (304 ids, one row each), and the
seven original columns are byte-for-byte as they were: `limitation` keeps the historical execution
record, and its `result=` field is the pre-close-out state, superseded by `c6_result`. The table is
written by `w17-inventory/dispose.py` from `inventory-map.tsv` (D37's evidence-step map) and the
per-row decisions in that script; this page is written from the table by `w17-inventory/gen_c6map.py`.

## Candidate identity

- Candidate 6: `verify/v6` `99d0b18cf7d5991aa733e70ce52a542b80aa7710`, a `--no-ff` merge of
  `closeout/integration` `9a56b305` (`99d0b18^2`). Its tree differs from `9a56b305` only under `plans/`.
- Frozen bundles: `qompack-bundles/c6` (`bundle --archive --version 0.3.0`), windows-amd64 `BUNDLE.json`
  sha256 `a55f4c66...`, `bin/qompack.exe` sha256 `2de82d25...` (`phase3/c6/freeze.log`).
- The Windows pre-freeze check ran on integration `61b0cd66`. The only commit between it and `9a56b305`
  is the docs waiver `9a56b305`, which touches one report under `plans/`, and no file outside `plans/`
  differs between `61b0cd66` and the candidate: `git diff --stat 61b0cd66 99d0b18 -- . ':(exclude)plans'`
  is empty (`w17-inventory/runs/unchanged-proofs.txt`). The pre-freeze logs are therefore runs of the
  candidate's product and test code.

## Vocabulary and rule

The file's own vocabulary: `verified_in_target`, `partial_verified`, `implemented_unverified`, `failed`,
`unknown`, `unsupported`, `documented` (and `experimental` for the shipped-disabled switches below).

- `verified_in_target`: every evidence step the row names (D37 map) has an executed artifact on
  candidate 6 and it is green, or the row's benchmark ran on candidate 5 over covered code that is
  byte-unchanged at the candidate (C52 below).
- `partial_verified`: every automated step the row names is green on candidate 6, and a further part is
  pending (the candidate 6 live lane, C5.5) or has no possible artifact, or is a Linux fsync-bound half
  that is not verified in target by rule; the cell names the part and what closes it. This is the
  pending state for Phase 4 and C5.5 rows.
- `implemented_unverified`: nothing the row needs has executed on candidate 6 yet.
- `failed`: a candidate 6 artifact for the row's assertion is red. Kept until the red has a root cause
  and fix, or a recorded disposition.
- `unknown`: no step can produce an artifact (D37(c)); never counted as a pass.
- `unsupported`: retired by a recorded criterion change (E-1, F-3, D36). `documented`: a meta or prose
  row with no test by design.

Not verified in target, by rule: Linux fsync-bound rows (B-A, B-B) on the Docker Desktop container
(D53(b)); hosted-runner fsync figures are reports, never constants (Q1).

## Evidence codes

Paths are relative to `plans/sdd/V6-closeout/`. Job ids are GitHub Actions jobs of ci.yml run
`36955046276` or nightly run `36955043924`, both on head `99d0b18`.

| code | artifact | state |
|---|---|---|
| `WIN` | Windows whole tree: `phase3/c6/prefreeze/internal.log`, `testpkgs.log`, `integration.log` (tree `61b0cd66`, product-identical); `phase3/c6/p3-win-race.log` (`99d0b18`, `devtool test-race`, every non-e2e package); `phase3/c6/p3-win-e2e-timing.log` (`99d0b18`, test/e2e, isolated) | green except `RED-X11` and `RED-FAULT` |
| `LNX` | hosted `test (ubuntu-latest)` 110676058062: `-race`, whole tree but test/e2e, expected-failure reconciliation empty; `test (macos-latest)` 110676058123 likewise; container `phase3/c6/linux/cx-p3-p3-linux-tree-99d0b18-20261002T041733Z-artifacts` (non-root, `-race`, co-load declared: every package PASS, 0 fail) | green |
| `LE2E` | hosted `test-e2e` ubuntu 110676058080, macos 110676058201, windows 110676058155 (under `QOMPACK_NONREFERENCE_DISK`); container `phase3/c6/linux/cx-p3-p3-linux-e2e-timing-99d0b18-20261002T035853Z-artifacts` (335 pass, 3 skip, 1 fail: X11, fsync-bound, D53(b)) and the container `-race` lane `cx-p3-p3-linux-e2e-99d0b18-20261002T044416Z-artifacts` (336 pass, 3 skip, 0 fail; X11 reports under co-load, D39). The three skips are TestInstall_HostCLIInstallUpgradeUninstall and TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting (no claude CLI in the container) and TestE2E_RequiredProductChildRaceInstrumentation (its own lane) | green but isolated X11 |
| `CHILD` | nightly `race-product-child` 110676054635 (ubuntu, `QOMPACK_REQUIRE_CHILD_RACE=1`); container `cx-p3-p3-linux-child-99d0b18-20261002T052037Z-artifacts` (8 of 8 pass, `CGO_ENABLED=1 GOFLAGS=-race`) | green |
| `WRACE` | `phase3/c6/p3-win-race.log`; hosted corroboration: nightly `race-windows` 110676054888 | green |
| `FUZZ` | nightly fuzz matrix, 25 jobs (`gh run view 36955043924 --json jobs`: 31 jobs, all success: 25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded) | green |
| `GATE` | `phase3/c6/prefreeze/gate.log` (build; vet for windows, linux, darwin; fmt-check; gen-config-docs and gen-mcp-docs `--check`; lint subset) and `runpatterns-after-waiver.log`; hosted `verify` 110676058066 (fmt-check, full `devtool lint`, vet, build) and `lint-windows` 110676058082 | green |
| `DOCS` | hosted `docs` 110676058055 (gen-config-docs, gen-mcp-docs, gen-command-docs `--check`; test/docs) | green |
| `COVER` | hosted `cover` 110676057900 (`devtool cover`, OWNERS.tsv floors); `coveragefloors` in `gate.log` | green |
| `SEC` | hosted `security` 110676058089 (govulncheck; importgraph, testdeps, bindeps) | green |
| `XBUILD` | hosted `crossbuild` 110676058185 (`build-all`); the six-target bundle build in `freeze.log` | green |
| `PV` | hosted `plugin-validate` 110676058031; `phase3/c6/host-validate.txt` (`claude plugin validate --strict`, CLI 2.1.280, accepted) | green |
| `BUNDLES` | `phase3/c6/p3-bundleA.json`, `p3-bundleB.json` and `chain.log` ("bundles identical: 91 files", six targets) | green |
| `REPLAY` | hosted `replay-gate` 110676058077 (`devtool replay --phase 0 --ci` over the 24-session synthetic corpus, C5.3's comparison); nightly `replay-recorded` 110676054886 | green |
| `WTIME` | `phase3/c6/p3-win-timing.log` (D28 isolated timing pass, all twelve rows) | green |
| `LTIME` | `phase3/c6/linux/cx-p3-p3-linux-timing-99d0b18-20261002T035234Z-artifacts` (store 2, negknow 7, mcp 1, daemon 1 pass; TestIntegration_HotPathWarmWithRealResidentState fails, fsync-bound, D53(b)) | green but the hot-path row |
| `C52` | quiet C5.2 on candidate 5 `0d06ab12` (`phase3/c5/quiet/c52-win/`, `c52-linux/`, `c52-names.tsv`; ten ABBA rounds against `cf31e01`, D54), carried by unchanged covered code (next section) | green as a measurement |
| `FREEZE` | `phase3/c6/freeze.log` | recorded |
| `RED-FAULT` | hosted `test (windows-latest)` 110676058036: `-count=2`, the second pass of TestFault_Lifecycle/out_of_order_sessionend left a dangling retention root ("retention root ... names 158e3206c3f5, which is not held. Owner: internal/daemon"); the first pass, `WIN` and `LNX` passed | red, open (w17 ci) |
| `RED-X11` | TestV3_HotPathUnchangedWithLedgerResident red on Windows in isolation (`p3-win-e2e-timing.log`, `p3-win-x11-alone.log`: B-A p99 81.9/98.3 ms and B-B 57.3/81.9 ms against 50 ms, 593 deliveries deferred to the spool, spawn floor p50 29-30 ms) and in hosted `release-dry-run` (ubuntu, release-check runs it without the non-reference-disk declaration); the container red is D53(b) | red, open (w17 x11win) |
| `RED-RELDRY` | hosted `release-dry-run` 110676058084: release-check passed version (skipped, no tag), fmt-check, lint, vet, build and test, then failed in `ci-local cover` on X11; build-all, generated docs, guards, govulncheck, licenses, determinism, rollback rehearsal, plugin-validate and marketplace were not reached | red, open (w17 release/ci) |
| `C51` | quiet C5.1 on `99d0b18`, `phase3/c6/quiet/` (`quiet.sh` 05:25-05:32Z, `bench-hotpath --iterations 5000`, `quiet-run.txt`). Windows (`c51-win.log`, `c51-win-hotpath.json`): B-A p99 30.72 ms and B-B 24.58 ms against 50 PASS, B-D 72.23 ms reported, B-E 170.75 ms (CPU 31.25 ms) against 2000 PASS, spawn floor p50 14.42 ms; B-F p99 73.73 ms against 250 PASS (`c51-win-bf.log`). Linux (`c51-linux/c51-linux.log`): B-A p99 65.54 ms and B-B 61.44 ms against 15 FAIL, 3591 of 5130 hot-path requests deferred to the client spool and 0 lost, fsync-bound, not verified in target (D53(b)); B-D 24.06 ms reported; B-E 384.11 ms (CPU 7.76 ms) PASS; B-F p99 20.48 ms PASS (`c51-linux/bf/.../test.jsonl`); quiet.sh warned the container was not idle after its run (1-min load 2.97). `homeguard-check.txt`: real home unchanged | Windows green; Linux B-A/B-B red (D53(b)) |
| `P-LIVE` | the candidate 6 live lane (D53(f); `coordinator/rerun-parts-c6.js`): C4.1 through a `qompack-windows-amd64` entry, C4.3, C4.4, C4.5, C4.6, C4.8 (upgrade from the c5 bundle), C4.9, C1.7 restore smoke, UAT-01, 03, 04, 05, 06, 09, 10, 12 | pending |
| `P-C55` | C5.5 confirmatory evaluation (D53(g)) | pending |
| `CARRY-C4` | passed on candidate 4 `9f6a2fad` (`live/report-c4.md`) and not in the c6 lane: C4.2, UAT-02 (and UAT-07, 08, 11). Carry-forward note (D53(f)): 204 files changed since under internal/, cmd/ and plugin/ (`git diff --shortstat 9f6a2fad 99d0b18 -- internal cmd plugin`), 98 of them non-test product files, +5720/-654 (the same command with `':(exclude)*_test.go'`) | not re-run |

## C5.2 rows: why the candidate 5 measurement carries

`w17-inventory/runs/unchanged-proofs.txt` holds the per-file commands and their output, and
`w17-inventory/runs/c52-package-diffs-c5-to-c6.txt` the package-level ones. Between candidate 5 and
candidate 6, no product file changed in internal/store, negknow, canon, chunk, symbols, dag, sketch,
eval, config, paths (only the test helper package pathstest), scheduler, rules or skills. The files the
remaining benchmarks run are unchanged too: observer `tooluse.go` and `tombstone.go`; checkpoint
`finalize.go`, `writer.go`, `decisions.go`, `inject.go` and `truncate.go`; the daemon's eight
`scheduler_*.go` files; and every benchmark source file. The product changes inside their import
closures (`runs/c52-closure-c5-to-c6.txt`) are additive or off the measured path: new declarations in
core (`CutHostPluginTool`) and obs (`nonrefdisk.go`, `spool_submode.go`) that no benchmarked package
calls; checkpoint's nil-checked `PricedDrops` hook on `preCompact`, which BenchmarkFinalize does not
enter, and three new constants; in observer, `OnToolUse` calls the changed `collectThrash` only when a
Grammar is wired, and the benchmark harness wires none, so the measured path is unchanged, while
`sessionState` gained two fields (`ThrashFloor`, `ReplyWarning`); grammar's self-marker
predicate, used only by the disabled state-warning detector; ipc's `SpoolFiles` refactor on the drain
listing; cli's usage text. Two store benchmark files changed only in fixture plumbing
(`newTestStore(b)` in place of a zero `testing.T`). internal/hostperm's `policy.go` did change, so its
BenchmarkEvaluate figure does not carry; inventory-map.tsv lists it under 1.12.17 by a name collision,
and 1.12.17 rests on the scheduler benches instead.

**This carry is an interpretation, and it awaits a coordinator ruling.** C6.2 asks for covered code that
is byte-unchanged, proven with `git diff --stat` over its paths. Taken over whole packages, the diff is
empty for store, negknow, canon, chunk, symbols, dag, sketch, eval, config, paths (outside pathstest),
scheduler, rules and skills, but not for obs (+93, two new files), cli (+82/-3), observer (+191/-9),
checkpoint (+32) and daemon (+1320/-80). Those packages' rows (1.1.16, 1.1.27, 1.8.2, 1.8.13, 1.10.17,
1.12.17, 1.16.11) are verified in target only because their benchmarks' executed files are unchanged and
the other changes are additive or off the measured path (interpretation 2). If the ruling is the
whole-package reading instead, those seven rows become `partial_verified`, pending a quiet C5.2 re-run on
candidate 6.
"""

parts = [HEAD]
parts.append("## Counts\n")
parts.append("| c6 result | rows |\n|---|---|")
for k in order:
    if count.get(k):
        parts.append(f"| `{k}` | {count[k]} |")
parts.append(f"| total | {sum(count.values())} |\n")
parts.append("## Rows that are not `verified_in_target`\n")
parts.append("Every row below names what is missing and what closes it. The other "
             f"{count['verified_in_target']} rows are verified in target; their cells in the TSV list "
             "the evidence codes.\n")
parts.append("### Failed on candidate 6\n")
parts.append(table({"failed"}) + "\n")
parts.append("### Pending: automated part green, live or evaluation part outstanding\n")
parts.append(table({"partial_verified", "implemented_unverified"}) + "\n")
parts.append("### No possible artifact, retired, or documented\n")
parts.append(table({"unknown", "unsupported", "documented"}) + "\n")

TAIL = """## The fourteen section 3 integration identifiers

Not renumbered into the 304. Mapped cases are the notes' section 4 table, re-checked on the candidate:
every named test exists at `99d0b18` (`TestPlatform_WindowsHookLauncherForms` is now
`TestPlatform_HookLauncherForms`; `TestV5_DegradedPassiveIsStillCorrect...` is
`TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent`).

| section 3 | candidate 6 cases | c6 result | evidence; what closes it |
|---|---|---|---|
| 3.1 PackagedBundleObservesARealSession | TestCanary_HostInventory, TestCanary_RegisterIsInternallyConsistent, TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent | `partial_verified` | WIN, LNX, LE2E; the packaged-bundle session is P-LIVE (C4.1 install from the frozen c6 bundle, sessions) |
| 3.2 UniversalLauncherPreservesHookSemantics | TestPlatform_HookLauncherForms, TestPlatform_PluginRootWithSpacesAndUnicode, TestPlatform_ReadOnlyBundleDir and its ReadOnly siblings, TestCanary_CompetingHooks, TestCanary_InvalidHookPayload | `verified_in_target` | WIN, LNX (ubuntu, macos) |
| 3.3 DocumentedCommandsRunAgainstShippedBundle | TestCanary_PluginValidate, TestGenCommandDocs_MatchesTheInstalledHelp | `partial_verified` | WIN, DOCS, PV (the frozen c6 bundle validated by the installed CLI); commands run against the shipped bundle in a real session: P-LIVE (C4.5) |
| 3.4 EliminationStalenessSurvivesPackaging+MCP | TestV3_ObserverFileVersionsDriveEliminationStaleness, TestCanary_MCPLauncherDiscoversTools | `partial_verified` | WIN, LE2E, LNX; survival across an upgrade: P-LIVE (C4.8, UAT-12 upgrade leg) |
| 3.5 EphemeralRetrievalResultsAreEvictedFirst | TestPropose_ChoosesAtMostOneRepresentationPerItem; legacy TestV4_EphemeralRetrievalResultsRankFirstForEviction | `unsupported` | native eviction retired (E-1); the replacement and the legacy row are green (WIN, LNX, LE2E) |
| 3.6 FsckRepairsSeededCorruption | internal/cli fsck tests, TestFault_AuditSeesADeletedObjectUnderALiveIndex, TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence | `partial_verified` | WIN, LNX (these cases pass; RED-FAULT is another test/fault case); publication accounting is detection, not recovery; packaged fsck on a real store: P-LIVE (C1.7 restore smoke) |
| 3.7 DoctorAgreesWithStatus+Subsystems | TestDoctor_AgreesWithStatusOnModeAndProvenance, TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt | `verified_in_target` | WIN, LNX |
| 3.8 ConfigReferenceDescribesTheBinary | TestGenConfigDocs_LeavesMatchDefaultsOneToOne, TestUserGuideCoversEveryGeneratedCommandAndTool | `verified_in_target` | WIN, LNX, GATE, DOCS |
| 3.9 InstallUpgradeUninstallByteIdentical | TestInstall_HostCLIInstallUpgradeUninstall, TestRollbackOrderIsFixed, TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite, internal/store TestMaintenance_* | `partial_verified` | WIN, LE2E; TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI (container, hosted); the installed install/upgrade/uninstall: P-LIVE (C4.1, C4.8, UAT-01, UAT-12) |
| 3.10 DegradedPassiveFromPackagedBundle | TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent, TestPlatform_UnknownSettingsVersion, TestPlatform_UnsupportedOptimizationsDisabled | `partial_verified` | WIN, LNX, LE2E; from the packaged bundle: P-LIVE (C4.9) |
| 3.11 NoSecretAndNoNetworkFullPackaged | TestSecurity_NoSecretReachesAnyDurableSurface, TestSecurity_TelemetryCannotBeTurnedOn, TestSecurity_MCPServerNeverImportsOSExec, TestV6_ArchivedReadRetainsItsAuthorizationBoundary, TestV6_HashAddressesDoNotBypassPathAuthorization | `partial_verified` | WIN, LNX, SEC; the historical archive-authorization FAIL is cleared for the automated half; the full packaged session: P-LIVE (C4.6, UAT-12) |
| 3.12 CheckpointToRehydrationRoundTripBundle | TestE2E_SessionStartCompactAfterFailedSummary, the TestE2E_Checkpoint* rows, TestFault_CheckpointDropsAnUnresolvablePointer | `partial_verified` | WIN, LE2E, CHILD; the bundle round trip in a real compaction: P-LIVE (C4.3, UAT-03, 04, 05) |
| 3.13 ReleaseArtifactsReproducible | TestAssembleBundle_Deterministic, TestAssembleBundle_ChecksumsFormat, TestWriteArchiveChecksums, TestReleaseCheckDeterminismVersion, TestBundle_OnDiskMatchesGenerator, TestCanary_PackagingShape | `verified_in_target` | BUNDLES (two builds, six targets, byte-identical), WIN, LNX; release-check's own determinism step was not reached on hosted (RED-RELDRY) |
| 3.14 HotPathHoldsEverySubsystemResident | TestIntegration_HotPathWarmWithRealResidentState, TestIntegration_HotPathDegradesRatherThanBlocks, TestV3_HotPathUnchangedWithLedgerResident | `failed` | the hot-path row passes isolated on Windows (WTIME) and the quiet run passes on Windows (C51); Linux B-A/B-B are not verified in target (D53(b)); RED-X11 is red on Windows in isolation; closes with X11's root cause and fix, or disposition |

## SP-19, SP-20 and SP-21 switches

Shipped defaults read from `internal/config/defaults.go` at the candidate; tests green on candidate 6
in WIN and LNX: TestSwitch_GatedCapabilitiesAreRefusedNotDisabled (test/release),
TestDoctor_EveryMigrationGateIsPendingInThisBuild (internal/cli),
TestGenConfigDocs_GatedSwitchesRenderFromSource (tools/devtool), TestCanary_CompactionBlocking and
TestCanary_NewResultReplacement (test/canary), TestSwitchIsOffByDefault and the internal/admission suite,
TestCapturePolicyToHookEnvelopeRoundTrip and TestCapturePolicyProducesTheCompleteFidelitySet
(test/integration), and the internal/state suite. Two key names in the notes' table were wrong and are
corrected here: the selection switches live under `runtime.selection`, not `runtime.pselection` or
`runtime.grammar`.

| switch | owner / gate | default at c6 | c6 result | note |
|---|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | SP-20 M1-01 | false | `experimental` | refused until its gate; recorded disabled, never passed |
| `runtime.migration.publication.durableFrontier` | SP-20 M1-02 | false | `experimental` | refused until its gate |
| `runtime.migration.reinjection.sessionStartCompact` | injection kill switch | true | `partial_verified` | the one enabled adapter: automated tests green; the live compaction round trip is P-LIVE (C4.3); kill switches passed live on candidate 3 only (C4.7, not in the c6 lane) |
| `runtime.migration.replacement.newResult` | SP-21 M4 admission | false | `experimental` | admission off, output passes through unmodified |
| `runtime.migration.compaction.automaticVeto` | SP-19 M0-03 | false | `unsupported` | the native veto is retired (E-1); refused |
| `runtime.migration.compaction.blockManualCompact` | section 12 | false | `unsupported` | hardwired off |
| `runtime.migration.experiments.enabled` | SP-15/16 | false | `experimental` | disabled |
| `runtime.selection.submodularEnabled` | SP-15 | false | `experimental` | disabled; refused without p-selection |
| `runtime.selection.loopWarningsEnabled` | SP-15 | false | `experimental` | disabled; a reload reports it has no effect in 0.3.0 (D51) |

**Delivery-journal rollover (SP-20, not a config key).** The notes and the `65bc8d7` gate map record
rollover as default-off and not accepted. That is superseded: D2 and C1.10 enabled it by default
(`enableDeliveryGenerations = true`, `internal/daemon/delivery_generation.go`), with D6 and D16's
residuals documented, and SP20-D4 is `fixed` in `plans/CARRIED-DEFECTS.tsv` with
TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart green on candidate 6 (WIN, LNX).

## Corrections to earlier records

- The pre-existing reds the D37 map warned about, TestBudget_DetectorScan (1.9.12) and TestBudgetBF
  (1.13.16), pass on candidate 6 in the isolated timing passes on both OSes.
- 1.13.4 and 1.17.12's historical FAILs are cleared for their automated halves; their real-session
  halves remain pending.
- 1.14.5 is retired by D36(a), not pending: the three TestCheckpoint_* tests it names no longer exist.
- 1.17.19's four SP-17-era job names never existed in ci.yml; the row is judged on the candidate's
  hosted run and on branch protection.
- `inventory-map.tsv`'s 1.12.17 entry internal/hostperm BenchmarkEvaluate is a name collision (above).
- 1.17.4's `current_test_symbols` still name TestPlatform_WindowsHookLauncherForms; the row is judged on
  its replacement TestPlatform_HookLauncherForms, which now runs on every OS (`packaging/report.md`).

## Evidence folded in after the first pass, and the review round

The chain's container `-race` lanes landed green after the first disposition pass (`linux race exit=0
2026-10-02T05:25:05Z` in `phase3/c6/chain.log`) and are folded into `LNX`, `LE2E` and `CHILD` above;
they changed no row. Quiet C5.1 then landed (`quiet exit=1 2026-10-02T05:32:09Z`, the exit from the
Linux B-A/B-B rows, D53(b)) and is the `C51` code: 1.10.16 is verified in target on it.

The independent review of the first pass changed five rows. 1.5.12, 1.12.14 and 1.17.6 are `failed`,
not pending: each asserts the hot-path B-A budget, and RED-X11's isolated Windows runs breach it on
candidate 6, which is the same rule that makes section 3.14 `failed`. The quiet Windows pass does not
cancel a red artifact; the rows clear when w17 x11win records a root cause and fix, or a disposition.
1.13.17 is `partial_verified`: its installed-host half (C4.4) is part of the row, as the D37 map says.
1.17.4 now names the renamed test it is judged on.

Still to land: the candidate 6 live lane (P-LIVE) and C5.5 (P-C55).
"""
parts.append(TAIL)
open(OUT, "w", encoding="utf-8", newline="\n").write("\n".join(parts))
print("wrote", OUT, dict(count))

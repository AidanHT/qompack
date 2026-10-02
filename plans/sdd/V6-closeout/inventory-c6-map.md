# V6 inventory on candidate 6 (C6.2)

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
  pending (the candidate 6 live lane, the quiet C5.1 run, C5.5) or has no possible artifact; the cell
  names the part and what closes it. This is the pending state for Phase 4 and C5.5 rows.
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
| `LNX` | hosted `test (ubuntu-latest)` 110676058062: `-race`, whole tree but test/e2e, expected-failure reconciliation empty; `test (macos-latest)` 110676058123 likewise | green |
| `LE2E` | hosted `test-e2e` ubuntu 110676058080, macos 110676058201, windows 110676058155 (under `QOMPACK_NONREFERENCE_DISK`); container `phase3/c6/linux/cx-p3-p3-linux-e2e-timing-99d0b18-20261002T035853Z-artifacts` (335 pass, 3 skip, 1 fail: X11, fsync-bound, D53(b)) | green but X11 |
| `CHILD` | nightly `race-product-child` 110676054635 (ubuntu, `QOMPACK_REQUIRE_CHILD_RACE=1`) | green |
| `WRACE` | `phase3/c6/p3-win-race.log` | green |
| `FUZZ` | nightly fuzz matrix, 27 jobs | green |
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
| `P-C51` | quiet C5.1 on both OSes, `phase3/c6/quiet` (`quiet.sh`, running in the chain at disposition time) | pending |
| `P-LIVE` | the candidate 6 live lane (D53(f); `coordinator/rerun-parts-c6.js`): C4.1 through a `qompack-windows-amd64` entry, C4.3, C4.4, C4.5, C4.6, C4.8 (upgrade from the c5 bundle), C4.9, C1.7 restore smoke, UAT-01, 03, 04, 05, 06, 09, 10, 12 | pending |
| `P-C55` | C5.5 confirmatory evaluation (D53(g)) | pending |
| `CARRY-C4` | passed on candidate 4 `9f6a2fad` (`live/report-c4.md`) and not in the c6 lane: C4.2, UAT-02 (and UAT-07, 08, 11). Carry-forward note (D53(f)): 204 product files changed since (`git diff --shortstat 9f6a2fad 99d0b18 -- internal cmd plugin`) | not re-run |

## C5.2 rows: why the candidate 5 measurement carries

`w17-inventory/runs/unchanged-proofs.txt` holds the commands and their output. Between candidate 5 and
candidate 6, no product file changed in internal/store, negknow, canon, chunk, symbols, dag, sketch,
eval, config, paths (only the test helper package pathstest), scheduler, rules or skills. The files the
remaining benchmarks run are unchanged too: observer `tooluse.go` and `tombstone.go`; checkpoint
`finalize.go`, `writer.go`, `decisions.go`, `inject.go` and `truncate.go`; the daemon's eight
`scheduler_*.go` files; and every benchmark source file. The product changes inside their import
closures (`runs/c52-closure-c5-to-c6.txt`) are additive or off the measured path: new declarations in
core (`CutHostPluginTool`) and obs (`nonrefdisk.go`, `spool_submode.go`) that no benchmarked package
calls; checkpoint's nil-checked `PricedDrops` hook on `preCompact`, which BenchmarkFinalize does not
enter; observer's prompt-path changes and two new `sessionState` fields; grammar's self-marker
predicate, used only by the disabled state-warning detector; ipc's `SpoolFiles` refactor on the drain
listing; cli's usage text. Two store benchmark files changed only in fixture plumbing
(`newTestStore(b)` in place of a zero `testing.T`). internal/hostperm's `policy.go` did change, so its
BenchmarkEvaluate figure does not carry; inventory-map.tsv lists it under 1.12.17 by a name collision,
and 1.12.17 rests on the scheduler benches instead.

## Counts

| c6 result | rows |
|---|---|
| `verified_in_target` | 257 |
| `partial_verified` | 28 |
| `implemented_unverified` | 1 |
| `failed` | 5 |
| `unknown` | 7 |
| `unsupported` | 3 |
| `documented` | 3 |
| total | 304 |

## Rows that are not `verified_in_target`

Every row below names what is missing and what closes it. The other 257 rows are verified in target; their cells in the TSV list the evidence codes.

### Failed on candidate 6

| id | current assertion | c6 result | evidence and what closes it |
|---|---|---|---|
| 1.5.19 | every hook exits 0 under faults | `failed` | WIN+LNX; RED-FAULT: the row names the whole test/fault package, which has one red on c6 (hosted windows-latest -count=2, TestFault_Lifecycle/out_of_order_sessionend); the reported failure is a dangling retention root, not a hook exit; TestFaultActive_UnsetIsInertForAllElevenSites green (WIN, LNX); clears with RED-FAULT's disposition |
| 1.17.11 | hooks exit 0 full fault matrix (54) | `failed` | WIN+LNX; RED-FAULT: TestFault_Lifecycle/out_of_order_sessionend left a dangling retention root on hosted windows-latest's second -count=2 pass on c6 (passed in WIN and LNX); open with w17 ci; closes on a root cause and fix, or a recorded disposition |
| 1.17.14 | §12.3 nine degradation rows | `failed` | WIN+LNX; RED-FAULT: the test/fault suite that replaces the section 12.3 rows has one red on c6 (hosted windows-latest -count=2); otherwise green in WIN and LNX; F-4's per-row mapping is still owed |
| 1.17.18 | release pipeline dry run | `failed` | WIN; RED-RELDRY: ci.yml release-dry-run on c6 failed in release-check's ci-local cover step (X11 on ubuntu without the non-reference-disk declaration); build-all, generated docs, guards, licenses, determinism, rollback rehearsal, plugin-validate and marketplace were not reached; actionlint and the goreleaser snapshot run only on a tag; open with w17 release/ci |
| 1.17.19 | four new CI jobs required | `failed` | ci.yml 36955046276 on c6: 19 of 21 jobs green, release-dry-run (RED-RELDRY) and test (windows-latest) (RED-FAULT) red; the four SP-17-era jobs (package-gate, platform-matrix, fault-gate, install-gate) were never created, their scope runs inside test/test-e2e/crossbuild/plugin-validate/release-dry-run; develop has no branch protection (gh api: Branch not protected) and main is absent on origin, which C7.3 closes |

### Pending: automated part green, live, quiet or evaluation part outstanding

| id | current assertion | c6 result | evidence and what closes it |
|---|---|---|---|
| 1.5.12 | hot-path budget (re-measure, no universal claim) | `partial_verified` | WTIME+LTIME; Windows: the isolated hot-path row passes (WTIME). Linux: the container row fails on B-A/B-B, fsync-bound, not verified in target (D53(b)); hosted timing is report-only (Q1). RED-X11 measures the same B-A/B-B on Windows and is red on c6, open with w17 x11win. Closes with P-C51 (quiet C5.1 on both OSes) and X11's disposition |
| 1.5.15 | producers declared for shipped services | `partial_verified` | WIN+LNX; producers and their tests green on c6; the real-observation half (F-2) needs a real session: CARRY-C4 (C4.2 passed on c4, not in the c6 lane); the c6 lane's status/doctor reads (P-LIVE) refresh it |
| 1.6.4 | redaction at choke point | `partial_verified` | WIN+LNX+LE2E; pending P-LIVE (UAT-12, C4.6) |
| 1.8.6 | verbatim prompt capture | `partial_verified` | WIN+LNX+LE2E+CHILD; pending P-LIVE (UAT-06); not in the c6 lane: UAT-02, C4.2 (CARRY-C4) |
| 1.10.14 | PreCompact writes checkpoint | `partial_verified` | WIN+LE2E; pending P-LIVE (C4.3, UAT-03) |
| 1.10.16 | checkpoint budget (re-measure) | `partial_verified` | WIN; Windows B-E measured PASS on c6 inside X11's isolated harness runs (p3-win-e2e-timing.log: B-E p99 825 ms and, in p3-win-x11-alone.log, 786 ms, against 2000 ms); the designated quiet run is pending P-C51 |
| 1.11.11 | checkpoint-as-fallback | `partial_verified` | WIN+LNX+LE2E; pending P-LIVE (C4.3, UAT-04) |
| 1.11.12 | SessionStart compact/clear wired | `partial_verified` | WIN+LE2E+CHILD; pending P-LIVE (C4.3, UAT-03, UAT-05) |
| 1.11.16 | L5 latency budgets | `partial_verified` | C52; C52 measured rules BenchmarkPathScoped and skills BenchmarkIndex; internal/rehydrate has no benchmark, so the rehydrate share of L5 latency has no artifact |
| 1.12.14 | scheduler off hot path; B-A<15ms unchanged | `partial_verified` | WIN+LNX; structural half TestSchedulerNotOnHotPath green (WIN, LNX); the B-A half is pending P-C51, and Linux B-A is not verified in target (D53(b)) |
| 1.13.4 | expand re-materializes | `partial_verified` | WIN+LNX; the historical V6-AUTH FAIL is cleared for the automated half: test/security and the internal/mcp TestV6_* suites pass on c6 on Windows, Linux and macOS (WIN, LNX); the real-session half is pending; pending P-LIVE (C4.4, C4.6, UAT-12) |
| 1.13.5 | re_read current/historical | `partial_verified` | WIN+LNX; automated half green on c6 (D7 deny-rule honouring, capture-scope suites); the real-session half is pending; pending P-LIVE (C4.4, C4.6, UAT-12) |
| 1.13.14 | mcp.server_registered real | `partial_verified` | WIN+LNX; producer and seam tests green on c6; real mcp.server_registered observation (F-2) needs a real session: pending P-LIVE (C4.4 sessions run the MCP server) |
| 1.14.6 | eval reports OPT/secondaries | `partial_verified` | WIN+LNX; reporting-rule unit tests green (WIN, LNX); the live trials are pending P-C55 (D53(g)) |
| 1.14.10 | conformance/benches/coverage | `partial_verified` | COVER; coverage half green (COVER); no commandstest conformance package and no internal/commands benchmark exist, so those halves have no artifact |
| 1.17.6 | per-platform pays no launcher cost; B-A<15/B-E<2s | `implemented_unverified` | the launcher split cannot run (see 1.17.5, D37(c)); the B-A/B-E half is pending P-C51, and Linux B-A is not verified in target (D53(b)) |
| 1.17.7 | install-gate works | `partial_verified` | WIN+LE2E; backup/maintenance units and test/e2e install tests green (WIN, LE2E); TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI (container and hosted runs), RED-RELDRY stopped release-check before its rollback-rehearsal step (the step's two tests pass in the e2e lanes); pending P-LIVE (C4.1, C4.8, UAT-01) |
| 1.17.8 | Installed bundle install/upgrade/uninstall preserves project work and declared retention p | `partial_verified` | WIN+LE2E; maintenance/backup units green (WIN); TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite passes in the e2e lanes (WIN, LE2E); release-check's rollback step not reached (RED-RELDRY); the installed upgrade/uninstall is pending; pending P-LIVE (C4.8, UAT-12) |
| 1.17.9 | upgrade safety / schema-bump quarantine | `partial_verified` | WIN+LNX; schema-bump units green (WIN, LNX); TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting skips without the claude CLI (container, hosted) and the Windows chain log is non-verbose, so its execution on c6 is unproven; the old-released-reader matrix has no artifact (D37 map); C4.8 upgrade from the c5 bundle is pending; pending P-LIVE (C4.8) |
| 1.17.10 | cross-platform matrix | `partial_verified` | WIN+LNX; test/platform green on windows/amd64 (WIN; also hosted test windows-latest, where only test/fault was red), linux/amd64 (LNX) and darwin/arm64 (hosted macos-latest); windows/arm64, linux/arm64 and darwin/amd64 are cross-compile only (XBUILD), unknown; C4.11 Linux installed host: model sessions unknown (D34(c)), no-model half not run on c6; not in the c6 lane: C4.11 (D34(c): recorded unknown) |
| 1.17.12 | security audit, 3 proofs | `partial_verified` | WIN+LNX; the historical FAIL is cleared for the automated half: test/security (incl. TestV6_ArchivedReadRetainsItsAuthorizationBoundary) and test/guards pass on c6 on Windows, Linux and macOS; the real-session privacy half is pending; pending P-LIVE (C4.6, UAT-12) |
| 1.17.15 | qompack fsck | `partial_verified` | WIN+LNX; fsck and publication-accounting tests green (WIN, LNX), detection not recovery; the live restore smoke C1.7 is pending P-LIVE |
| 1.17.16 | qompack doctor 16 checks | `partial_verified` | WIN+LNX; doctor tests green (WIN, LNX); D34(e)'s self-test fix is on c6; the real-session half passed on c4 only; not in the c6 lane: C4.2 (CARRY-C4) |
| 1.17.17 | version drift guard; single source | `partial_verified` | WIN; version units green (WIN); release-check's version agreement SKIPPED without a tag, and core.Version and plugin.json still read 0.1.0 at c6 while the bundles are stamped 0.3.0 by bundle --version: the agreement half closes at C7.1/C7.4 (release-check --tag v0.3.0) |
| 1.18.1 | owned doc set = 21 docs | `partial_verified` | WIN+LNX+DOCS; TestOwnedDocsExist green (WIN, LNX, DOCS); the historical count assertions (IsTwentyOne, HasNoDuplicates, StartWithH1) have no current test (F-5) |
| 1.18.3 | config-ref ranges = Validate() | `partial_verified` | WIN+GATE+DOCS; replacement TestGenConfigDocs_LeavesMatchDefaultsOneToOne green (WIN, GATE, DOCS); it pins leaves to defaults, not documented ranges to Validate() (F-5) |
| 1.18.4 | runtime namespace additive | `partial_verified` | WIN+GATE+DOCS; replacement TestGenConfigDocs_GatedSwitchesRenderFromSource green (WIN, DOCS); the runtime-section additivity marker has no standalone test (F-5) |
| 1.18.8 | ADRs D1–D12 present/correct | `partial_verified` | WIN+LNX+DOCS; TestADRIndexListsEveryADR green (WIN, LNX, DOCS); the per-ADR shape and decision-id checks have no current test (F-5) |
| 1.18.11 | 12 UAT scenarios conformant + quote phase criteria | `partial_verified` | WIN+LNX+DOCS; UAT shape tests green (WIN, LNX, DOCS); no test quotes the phase exit criteria |

### No possible artifact, retired, or documented

| id | current assertion | c6 result | evidence and what closes it |
|---|---|---|---|
| 1.1.1 | repo shape provenance (SP-19 baseline) | `documented` | FREEZE; meta row: candidate 6 identity is FREEZE (99d0b18, bundle and exe sha256); no test exists by design |
| 1.1.28 | — | `documented` | retired E-3: Qompack.md is v1.8 with its Revision log (v1.6 D5, v1.7 D36, v1.8 D41); no test by design |
| 1.10.18 | residual-span reduction ≥30% | `unknown` | D37(c): no step runs the two replay --phase 4 runs with the frontier toggled; not verified in target, no new harness before release |
| 1.14.5 | checkpoint-now DPI | `unsupported` | WIN+LNX; retired by D36(a): 0.3.0 ships no manual checkpoint, /qompack:checkpoint is removed with exit criterion SP14-M3-01 and its three TestCheckpoint_* tests; TestAll_CoversEverySlashCommand pins the six on c6 (WIN, LNX); the c6 lane's C4.5 (P-LIVE) runs the six in a real session |
| 1.15.6 | submodular lazy-greedy (1−1/e) bound | `unsupported` | WIN+LNX; F-3: the (1-1/e) guarantee is retired; the held substance TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer is green on c6 (WIN, LNX) and submodular selection ships disabled |
| 1.15.7 | ephemeral results rank first | `unsupported` | WIN+LNX; E-1/section 3.5: native ephemeral eviction is retired; its replacement TestPropose_ChoosesAtMostOneRepresentationPerItem is green on c6 (WIN, LNX) |
| 1.15.14 | analyzer/grammar benches | `unknown` | C52: internal/analyzer and internal/grammar have no benchmark (c52-names.tsv), so the row has nothing to measure; not verified in target |
| 1.16.5 | O4 measured benefit | `unknown` | D37(c): no test or step computes the warm-vs-cold O4 delta; not verified in target, no new harness before release |
| 1.16.10 | declared non-delivery of prefix reorder | `unknown` | D37(c): TestPrefixReorderingNotAttempted is absent and no declaration exists (no docs/adr/0016; no file under docs/ or Qompack.md declares prefix-reorder non-delivery); not verified in target |
| 1.17.3 | binary size budget | `unknown` | BUNDLES; D37(c): no binary-size check exists; diagnostic only, the frozen c6 binaries measure 9.29-10.49 MB (windows-amd64 qompack.exe 10,416,640 B); not verified in target |
| 1.17.5 | launcher overhead in B-D | `unknown` | D37(c): bench-hotpath has no --bundle universal/native mode, so the launcher-overhead split cannot run as written; not verified in target |
| 1.18.9 | architecture quotes ten invariants | `documented` | WIN+LNX+DOCS; F-5: the verbatim ten-invariants claim is retired; 00-ARCHITECTURE.md carries the invariants, only ADR-index presence is executed (TestADRIndexListsEveryADR green on c6) |
| 1.18.12 | UAT executed end-to-end by a human | `unknown` | DOCS; D3: the human half has no possible artifact; the agent-run form is the c6 live lane UAT-01, 03, 04, 05, 06, 09, 10, 12 (P-LIVE) plus earlier-candidate UAT-02/07/08/11 results CARRY-C4; TestUATUnexecutedRowsSayUnverified green (DOCS) |

## The fourteen section 3 integration identifiers

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
| 3.14 HotPathHoldsEverySubsystemResident | TestIntegration_HotPathWarmWithRealResidentState, TestIntegration_HotPathDegradesRatherThanBlocks, TestV3_HotPathUnchangedWithLedgerResident | `failed` | the hot-path row passes isolated on Windows (WTIME); Linux B-A/B-B are not verified in target (D53(b)); RED-X11 is red on Windows in isolation; closes with X11's root cause or disposition and P-C51 |

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

## Evidence still landing

At disposition time the chain was in its Linux `-race` lanes (container `linux-tree`, `linux-e2e`,
`linux-child`), then quiet C5.1. The container lanes are supplementary to `LNX` and `LE2E` and change
no row unless they go red; quiet C5.1 closes P-C51 for 1.5.12, 1.10.16, 1.12.14 and the B-A/B-E half of
1.17.6 on Windows (Linux B-A/B-B stays not verified in target, D53(b)). The live lane closes P-LIVE.

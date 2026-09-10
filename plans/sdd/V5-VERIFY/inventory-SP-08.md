# V5-VERIFY inventory — SP-08 (observer, L0)

- **SP:** SP-08 (`internal/observer`, L0 observer), historical rows `I-08.1`–`I-08.15` taken from
  `plans/V3-SP-08-observer-l0.md` lines 263–277 at HEAD `7f92af5`.
- **Reconciliation target:** SP-20 `T20-M1-01`–`05` — captured host payload versus full process/file
  output, distinct events with equal content, child/gap provenance.
- **Owner plan sections consulted:** `plans/V4-SP-20-capture-storage-and-state-remediation.md`
  §"observation identity and publication ordering" (l. 99), §"storage/retention" (l. 103),
  §"exit criteria" item 5 (l. 84), requirement table `T20-M1-01`–`08` (ll. 127–134), commit ledger
  item 3 (l. 159). Also `plans/sdd/V4-VERIFY/reconciliation-map.md` rows `rc-v4-sp08-01`–`08`
  (reused where still valid; every result below was re-run on this tree) and
  `plans/CARRIED-DEFECTS.tsv` (`SP08-D1`).
- **Tree / HEAD:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @
  `87c0c1d` ("chore(sp21): integrate deterministic admission control").
- **Platform:** Windows 11 Home 10.0.26200, go1.26.6 windows/amd64, Intel Core Ultra 7 155H (22
  logical CPUs). Machine shared with other V5-VERIFY children throughout.
- **Date:** 2026-09-08.

## 1. Counts

| Disposition | Rows |
|---|---:|
| `MAPPED` | 13 |
| `MAPPED-CMD` | 0 |
| `SUPERSEDED-BY-WAVE4` | 1 |
| `RETIRED` | 1 |
| `MISSING` | 0 |
| `NEEDS-COORDINATOR` | 0 |
| **Total** | **15** |

| Result | Rows |
|---|---:|
| `PASS` | 13 |
| `FAIL` | 0 |
| `FAIL-BASELINE` | 0 |
| `FAIL-COLOAD-SUSPECT` | 2 |
| `SKIP` | 0 |
| `NOT-RUN` | 0 |

No row was dropped. Two clauses inside otherwise-passing rows were executed only in part and are
listed in [§4](#4-deferred-to-coordinator): the 60 s fuzz leg of `I-08.2` and the `-race` leg of
`I-08.3` (their non-fuzz / non-race legs ran green here).

Artifact root:
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-08/`
(abbreviated `…/inv/SP-08/` below).

## 2. Rows

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-08.1 | Addressable tombstones; `TestTombstone_DesignExample` renders the §8.1 marker; golden byte-equal | MAPPED | `internal/observer`: `TestTombstone_RendersTheSection81Form`, `TestTombstone_IsAddressable`, `TestTombstoneGolden`, `TestNormalizeToolName`, `TestIsCompactable`, `TestSupersedableClass` (16 tests selected) | PASS | `…/inv/SP-08/I-08.1.txt` | direct | The named `TestTombstone_DesignExample` does not exist; the design example survives byte-for-byte as line 1 of `testdata/golden/observer/tombstones.txt` (`[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]`), frozen by `TestTombstoneGolden`. The historical pattern still selects the replacements, so the command ran unchanged. |
| I-08.2 | Task-boundary signals: todo completion, go/jest/pytest/cargo verdicts, git commit incl. `&&` chains; fuzz finds no panic | MAPPED | `internal/observer`: `TestExtractSignals_*` (9, incl. `GitCommitInChain`, `GitCommitWithDashC`), `TestExtractTestOutcome_*` (8, incl. `PowerShell`), `TestPathsFromInput_*` (5), `TestResponseText_AllFourShapes` — 24 selected; `FuzzExtractSignals` seed corpus passes | PASS | `…/inv/SP-08/I-08.2.txt`, `…/inv/SP-08/I-08.2-seed.txt` | direct | Fuzzing itself (`-fuzz … -fuzztime 60s`) is a barred run class for this child — seed corpus only was executed; the 60 s leg is deferred (§4). |
| I-08.3 | `PostToolUse` pipeline under race; a `PutBytes` failure returns `hookio.Empty(), nil` and increments `observer.err.put` | SUPERSEDED-BY-WAVE4 | `internal/observer`: `TestOnToolUse_PutFailureRemainsUnpublished`, `TestOnToolUse_IndexFailureStopsPublication`, `TestOnToolUse_PublishesTheReferenceAgainstItsObservationIdentity`, `TestOnToolUse_UnlinkableCaptureBlocksPublication` plus the other 22 `TestOnToolUse_*` (26 selected) | PASS | `…/inv/SP-08/I-08.3.txt` | direct | SP-20 l. 99 / `T20-M1-03`–`05` changed the requirement itself: a put or index failure is no longer soft. Output is still `hookio.Empty()` but the error is now `ErrUnpublished` wrapping `core.ErrDegraded` so the delivery stays retryable, `observer.err.put` still increments, and no dangling index record is left. Run WITHOUT `-race` (barred class); the race leg is deferred (§4). |
| I-08.4 | Supersession: per-path, per-class, never marks a later record, ephemeral in neither direction, lookback capped at 32 | MAPPED | `internal/observer`: `TestSupersede_*` (17, incl. `NeverMarksLaterRecord`, `EphemeralNeitherDirection`, `LookbackCapped`, `DifferentPathIgnored`, `DifferentClassIgnored`), `TestIsSuperset_EmptyOlder`, `TestIsSuperset_IgnoresMultiplicity`, `TestIsSuperset_Properties` (20 selected) | PASS | `…/inv/SP-08/I-08.4.txt` | direct | `PropertyIsSupersetReflexive` no longer exists as a top-level name; reflexivity is a `rapid` subtest of `TestIsSuperset_Properties`. The historical pattern still selects everything else, so it ran unchanged. |
| I-08.5 | DAG edge emission; `Pos` monotone and pre-increment; symbols capped at 64 | MAPPED | `internal/observer`: `TestGraph_PosIsMonotoneAndPreIncrement`, `TestGraph_SymbolsCappedAt64`, `TestGraph_SymbolsSkippedAboveCap`, `TestGraph_IDsComeFromDagConstructors` and 14 more `TestGraph_*` (18 selected) | PASS | `…/inv/SP-08/I-08.5.txt` | direct | `TestNodeIDFormats` does not exist; node-ID form is asserted by `TestGraph_IDsComeFromDagConstructors`, which the unchanged pattern already selects. |
| I-08.6 | Sketch feeding and never the bloom filter; zero `Bloom` identifiers in non-test observer source | MAPPED | `internal/observer`: `TestSketches_*` (6), `TestObserverNeverFeedsBloom`, `TestObserverSourceHasNoBloomReference` (8 selected) | PASS | `…/inv/SP-08/I-08.6.txt` | direct | Pattern selects exactly the historical set. |
| I-08.7 | Verbatim, immutable user capture; `Canon.Strip == nil`; identical text re-submitted yields the same root and no new objects | MAPPED | `internal/observer`: `TestOnUserPrompt_StoresVerbatim`, `TestOnUserPrompt_VerbatimAgainstARealStore`, `TestOnUserPrompt_NeverRegenerated`, `TestVerbatimPromptID` and 10 more (14 selected) | PASS | `…/inv/SP-08/I-08.7.txt` | direct | **One clause is retired, not merely renamed:** `TestOnUserPrompt_StoresVerbatim` (`prompt_test.go:156`) now asserts `Canon.Strip` is **non-nil and empty** — a nil `Strip` is canon's "strip every optional class", the opposite request. See §3. |
| I-08.8 | Thrash warning delivered in `full` mode, silent in `ModePassive` | MAPPED | `internal/observer`: `TestOnUserPrompt_ThrashWarningInFullMode`, `TestOnUserPrompt_NoThrashWarningInPassiveMode`, `TestOnUserPrompt_ThrashWarnedOncePerRule` | PASS | `…/inv/SP-08/I-08.8.txt` | direct | **Pattern corrected.** `-run 'TestOnUserPrompt_Thrash'` selects only the two positive tests and silently drops the passive-mode half of the row's own assertion; ran `-run 'TestOnUserPrompt_.*Thrash'` instead. |
| I-08.9 | Subagent capture (G10.1): summary + tool-result hashes stored; `TestOnStop_RetrievalPathG10_1` round-trips a real store | MAPPED | `internal/observer`: `TestOnStop_*` (14, incl. `RetrievalPathG10_1`, `SubagentCapturesSummary`, `SubagentCapturesToolHashes`, `CaptureIsDeterministic`), `TestTailAssistantText_*` (2) — 16 selected | PASS | `…/inv/SP-08/I-08.9.txt` | direct | Pattern selects exactly the historical set. |
| I-08.10 | BOCD feature extraction: all fields finite and in range; recent ring bounded at 16 | MAPPED | `internal/observer`: `TestFeatures_AllFinite`, `TestFeatures_RecentRingBounded`, `TestFeatures_RecentTextIsBounded` and 11 more (14 selected), run at `-rapid.checks=500` | PASS | `…/inv/SP-08/I-08.10.txt` | direct | `pgregory.net/rapid` is still imported by `features_test.go`, so `-rapid.checks` is still meaningful. |
| I-08.11 | SessionStart branching, SessionEnd order `Close → Graph.Flush → Store.Flush → sketch.Save×2 → state write → GC`; `tried.bloom` never created | MAPPED | `internal/observer`: `TestOnSessionStart_*` (7), `TestOnSessionEnd_Order`, `TestOnSessionEnd_NeverWritesTriedBloom`, `TestOnSessionEnd_GCPolicyFromConfig`, `TestOnSessionEnd_GCFailureIsSoft`, `TestOnSessionEnd_SegmentClosedWithFeatures`, `TestState_*` (11) — 23 selected | PASS | `…/inv/SP-08/I-08.11.txt` | direct | `TestOnSessionEnd_Order` still pins steps 4–6 by file evidence (sketches and the state file absent at `Store.Flush`, present at GC), i.e. the historical six-step order. |
| I-08.12 | Passive mode still records: identical write counts, only `AdditionalContext` differs | MAPPED | `internal/observer`: `TestModePassiveStillWrites` (`tooluse_test.go:354`) | PASS | `…/inv/SP-08/I-08.12.txt` | direct | The test compares store counts, graph counts and turn between `ModeFull` and `ModePassive` and permits a difference only in `HookSpecificOutput.AdditionalContext`. |
| I-08.13 | Observer e2e through the real daemon: 44 index lines; `sketches/tried.bloom` absent | MAPPED | `test/e2e`: `TestE2E_ObserverThroughDaemon`, `TestE2E_HooksExitZeroUnderFaultInjection`, `TestE2E_SupersessionVisibleAfterRestart`, `TestE2E_VerbatimPromptSurvivesRestart` | FAIL-COLOAD-SUSPECT | `…/inv/SP-08/I-08.13.txt` | direct | 3 of 4 pass. `TestE2E_ObserverThroughDaemon` failed at `observer_e2e_test.go:213`: `index/tool_use.jsonl` reached **40 of 44** lines within `obsProcessBound` (`daemon.IdleTickMax` + allowance; ≈70 s here) and the diagnostic listed **36 undrained client spool files** — most hook deliveries degraded to the spool and were waiting on the 30 s idle-tick drain. A wall-clock-bounded condition on a machine shared with other verify children; not adjudicated here. Quiet re-run in §4. |
| I-08.14 | Phase-1 exit criterion: `DedupRatio ≥ 4.0`; `ratioOn ≥ ratioOff·1.25`; store growth sublinear | MAPPED | `test/e2e`: `TestPhase1_DedupRatioReadHeavy`, `TestPhase1_CanonicalizationGapOnTestOutput`, `TestPhase1_StoreGrowthSublinear` + `PathsReachTheObserver`, `ResponseBytesAreReal`, `ReportArtifact`, `CorpusSweep`, `HotPathBudgetDocumented` (8 selected) | PASS | `…/inv/SP-08/I-08.14.txt` | direct | All 8 pass in 319 s. Thresholds unchanged in `phase1_exit_test.go`: `phase1ExitRatio = 4.0` (l. 62), `canonGapFloor = 1.25` (l. 69). |
| I-08.15 | Observer B-C budgets: `OnToolUse_FileRead64KB` and `OnToolUse_TestOutput256KB` p99 < 50 ms; `Tombstone` < 2 µs | RETIRED | `internal/observer`: `BenchmarkOnToolUse_FileRead64KB`, `BenchmarkOnToolUse_TestOutput256KB` (each × `Deduped`/`Delta`/`AllNovel`), `BenchmarkTombstone`; breach tracked as `SP08-D1` in `plans/CARRIED-DEFECTS.tsv` | FAIL-COLOAD-SUSPECT | `…/inv/SP-08/I-08.15.txt` | direct | The p99 < 50 ms **gate no longer exists**: `runOnToolUseBench` (`tooluse_test.go:583`) "asserts NOTHING about latency, deliberately" and only `b.ReportMetric`s `p50-ms`/`p99-ms`; the breach lives in the carried-defect ledger as `SP08-D1` (`deferred:V4-VERIFY`, still unresolved on this tree). Command exited 0 in 33 s. Measured: `Tombstone` 603.4 ns/op (**meets** < 2 µs); `FileRead64KB` p99 57.3 / 213.0 / 180.2 ms; `TestOutput256KB` p99 458.8 / 245.8 / 491.5 ms (Deduped/Delta/AllNovel) — every `OnToolUse` figure over the historical 50 ms. See §5 Q1. |

## 3. Old-to-new assertion map

| Row | Historical | Current | Reason / replacement |
|---|---|---|---|
| I-08.1 | `TestTombstone_DesignExample` | `TestTombstone_RendersTheSection81Form` + `TestTombstoneGolden` | Renamed, not weakened. The §8.1 form is asserted character-by-character in `RendersTheSection81Form`, and the historical design-example string is line 1 of `testdata/golden/observer/tombstones.txt`, byte-frozen by `TestTombstoneGolden` over 13 fixture records. Agrees with `rc-v4-sp08-06`. |
| I-08.3 | `PutBytes` failure → `hookio.Empty(), nil` (soft) | `hookio.Empty()` + `ErrUnpublished` wrapping `core.ErrDegraded`; `TestOnToolUse_PutFailureRemainsUnpublished`, `TestOnToolUse_IndexFailureStopsPublication` | SP-20 `T20-M1-03`/`04`/`05` (plan ll. 99, 131, 159): an unacknowledged capture must remain retryable rather than be swallowed, so "soft failure" is no longer the requirement. The test's own comment says so ("Replaces the historical PutFailureIsSoft assertion"). Counters `observer.err.put` / `observer.err.index` / `observer.err.link` retained. |
| I-08.3 (added) | — | `TestOnToolUse_PublishesTheReferenceAgainstItsObservationIdentity`, `TestOnToolUse_UnlinkableCaptureBlocksPublication` | New requirement from the same SP-20 section: the daemon-assigned `core.ObservationID` reaches the index record and the capture sidecar's `Published`/`ToolUseID`/`Root` join, and a capture that is not durable blocks publication. This is the "captured host payload versus full output" and "child/gap provenance" half of the reconciliation target as it lands on the observer. |
| I-08.4 | `PropertyIsSupersetReflexive` | `TestIsSuperset_Properties` (rapid subtests) | Property rows were folded into one `Test` function so `-run` selects them as subtests; reflexivity is one of its `rapid.Check` blocks. |
| I-08.5 | `TestNodeIDFormats` | `TestGraph_IDsComeFromDagConstructors` | Renamed; the assertion moved from literal ID strings to "IDs are produced by the `dag` constructors" — the stronger form of the same requirement. |
| I-08.7 | `Canon.Strip == nil` | `require.NotNil(opts.Canon.Strip)` + `require.Empty(...)` in `TestOnUserPrompt_StoresVerbatim` | **Semantics inverted.** In today's canon a *nil* `Strip` means "strip every optional class" — the opposite of verbatim. The verbatim requirement is now a non-nil, empty `Strip`. The row's other clauses (same root, no new objects on re-submission) are unchanged and covered by `TestOnUserPrompt_NeverRegenerated` and `TestOnUserPrompt_VerbatimAgainstARealStore`. |
| I-08.8 | `-run 'TestOnUserPrompt_Thrash'` | `-run 'TestOnUserPrompt_.*Thrash'` | Pattern correction. The passive-mode half of the row is `TestOnUserPrompt_NoThrashWarningInPassiveMode`, which the historical prefix pattern does not match — the historical command would print `ok` while never running the assertion the row exists for. |
| I-08.15 | "p99 < 50 ms" as a gate | No assertion; `b.ReportMetric("p99-ms")` plus `plans/CARRIED-DEFECTS.tsv` row `SP08-D1` | B-C is a soft, ungated budget: a host-dependent number was deliberately removed from the build so unrelated changes do not fail on it. The obligation moved to the carried-defect ledger, whose row is still `deferred:V4-VERIFY`. |

## 4. Deferred to coordinator

Run on a quiet machine, serially.

| Row | Exact command | Reason |
|---|---|---|
| I-08.2 (fuzz leg) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/observer/ -run FuzzExtractSignals -fuzz FuzzExtractSignals -fuzztime 60s` | Fuzz runs are a barred class for this child. Seed corpus ran green here. |
| I-08.3 (race leg) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/observer/ -run 'TestOnToolUse_' -race -v -count=1 -timeout=30m` | Race runs are a barred class for this child. The non-race run of the same 26 tests was green. |
| I-08.13 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./test/e2e/ -run 'TestE2E_ObserverThroughDaemon|TestE2E_HooksExitZeroUnderFaultInjection|TestE2E_SupersessionVisibleAfterRestart|TestE2E_VerbatimPromptSurvivesRestart' -v -count=1 -timeout=30m` | `FAIL-COLOAD-SUSPECT`: index reached 40/44 inside a wall-clock `Eventually`, with 36 client spool files undrained. A quiet re-run separates co-load from a wave-4 daemon-admission regression. |
| I-08.15 | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/observer/ -run '^$' -bench 'BenchmarkOnToolUse_|BenchmarkTombstone' -benchtime 2s -timeout=30m` | Budget row; the reported p99 figures are load-sensitive and were taken with other verify children running. 33 s wall on this host. |

## 5. Questions

- **Q1 — `SP08-D1` ownership and scope.** The ledger row reads
  `SP08-D1  SP-08  V3-VERIFY  deferred:V4-VERIFY  BenchmarkOnToolUse_TestOutput256KB  …`. V4-VERIFY is
  behind us and this is a V5 inventory, so the deferral has no live owner. Two parts: (a) does
  V5-VERIFY resolve or consciously re-defer it, and (b) the defect text names **only** the 256 KB
  test-output case, but the breach measured on this tree extends to `BenchmarkOnToolUse_FileRead64KB`
  (p99 57.3 ms `Deduped`, 213.0 ms `Delta`, 180.2 ms `AllNovel`) — widen the row's summary, or open a
  second row, before any wave report claims B-C coverage? Note that `test/guards`'s
  `TestCarriedDefects_WaveReportRequiresResolution` is on the known-baseline-failure list, so nothing
  currently enforces this.
- **Q2 — Spool degradation in `TestE2E_ObserverThroughDaemon`.** 36 of 44 hook deliveries fell back to
  the client spool. If the quiet re-run reproduces, it is not a test-timing question but the daemon's
  accept/admission path, which SP-21's admission work and SP-20 `T20-M1-05` both touch — it would need
  an owner outside SP-08.
- **Q3 — Row `I-08.7` disposition.** Scored `MAPPED` because the row's headline requirement (verbatim,
  immutable capture) is fully asserted, with the `Canon.Strip == nil` clause recorded as retired in §3.
  If the coordinator wants clause-level scoring, this row splits into `MAPPED` + `RETIRED`.

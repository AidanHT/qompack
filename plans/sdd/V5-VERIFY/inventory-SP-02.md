# V5-VERIFY inventory — SP-02 (replay harness / Belady baseline / accounting)

**SP:** SP-02 — replay, accounting, baseline.
**Rows:** 14 (`I-02.1` … `I-02.14`), taken verbatim from the planning snapshot at root `7f92af5`
(`verify/v3`). None dropped, none merged.

**Owner plan sections consulted**

- `plans/V4-SP-19-migration-reconciliation.md` — reconciliation target, esp. the gate table:
  **M0-G5 accounting** (line 118) and **M0-G6 baseline** (line 119: "Original metric outputs
  preserved; corrected labels/corpus changes separately versioned; SP02-D1–D6 resolved together or
  explicitly blocked"), plus line 27 (no corpus regeneration / no baseline replacement) and line 103
  (SP02-D1–D6 travel as one baseline/corpus unit).
- `plans/sdd/V4-VERIFY/reconciliation-map.md` §1 and rows `V4-SP02-01` … `V4-SP02-10` (lines
  289–299), and the serial-suite table rows **S10** and **S13** (lines 615, 618). Reused where still
  valid; every result below was re-executed on this tree.
- `plans/CARRIED-DEFECTS.tsv` — `SP02-D1` … `SP02-D6`. Five are `fixed`; **`SP02-D6` is
  `deferred:V5-VERIFY`**, i.e. this checkpoint is its named resolver (see §5 Q1).
- `plans/V2-SP-02-replay-harness-belady-baseline.md` lines 1095 and 1498 — the E-1…E-5 budget
  definitions behind row `I-02.14`.

**Tree / HEAD:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @
`87c0c1d` ("chore(sp21): integrate deterministic admission control" — develop with wave 4
integrated). Working tree left unmodified apart from this file (`git status --porcelain` compared
before and after the driver runs: identical).
**Platform:** Windows 11, `go1.26.6 windows/amd64`, Intel Core Ultra 7 155H (22 logical CPUs).
**Date:** 2026-09-08. Machine shared with sibling inventory agents throughout.

## Counts

| Disposition | Rows |
|---|---:|
| `MAPPED` | 12 |
| `MAPPED-CMD` | 1 |
| `SUPERSEDED-BY-WAVE4` | 1 |
| `RETIRED` | 0 |
| `MISSING` | 0 |
| `NEEDS-COORDINATOR` | 0 |
| **Total** | **14** |

| Result | Rows |
|---|---:|
| `PASS` | 14 |
| `FAIL` | 0 |
| `FAIL-BASELINE` | 0 |
| `FAIL-COLOAD-SUSPECT` | 0 |
| `SKIP` | 0 (one *in-row* skip, `TestSynthesize_WriteCorpus`, recorded in the row note) |
| `NOT-RUN` | 0 rows; **one sub-command deferred** (`I-02.9`'s `-fuzz` campaign, §4) |

Every `PASS` in the table was produced by a command executed on this tree in this session; no
disposition was scored as a pass. Artifact paths are under
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-02/`
(abbreviated `…/inv/SP-02/` below).

## Row table

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| `I-02.1` | Block/demand extraction: cumulative `Pos`, per-`paths.Key` file blocks, elimination demands matched by approach class | MAPPED | `internal/eval`: `TestBlocks_PositionsAreCumulative`, `TestBlocks_FileBlockPerDistinctKey`, `TestBlocks_DecisionMarkerExtracted`, `TestBlocks_ToolResultTokensFromPayload`, `TestDemands_OnlyPreCompactionBlocks`, `TestDemands_DeduplicatesWithinTurn`, `TestDemands_EliminationMatchByApproachClass`, `TestDemands_ToolResultAndDecisionReferences`, `TestApproachClass_Normalization` | PASS | `…/inv/SP-02/I-02.1.txt` | direct | Pattern verified with `-list` first (9 definitions selected); 9 `--- PASS`, `ok … 1.192s`. All three clauses of the historical expectation have a named test. |
| `I-02.2` | Belady OPT — knapsack, exactness, budget, determinism, fallback | MAPPED | `internal/eval`: `TestBelady_UnitWeightsMatchesClassicBelady`, `TestBelady_KnapsackBeatsGreedyDensity`, `TestBelady_PMinIsEarliestDropped`, `TestBelady_PIsPrefixLengthWhenNothingDropped`, `TestBelady_ZeroValueBlocksPruned`, `TestBelady_BudgetNeverExceeded`, `TestBelady_Deterministic`, `TestBelady_FallbackWhenDPTooLarge`, `TestBelady_ContextCancelled`, `TestBelady_NonPositiveBudgetIsEmptyNotAnError` | PASS | `…/inv/SP-02/I-02.2.txt` | direct | All three named expectations (`UnitWeightsMatchesClassicBelady`, `KeepSet.Tokens <= budget` via `BudgetNeverExceeded`, `PMinIsEarliestDropped`) exist under their historical names. `SP02-D4` (pMin over candidates vs all blocks) is `fixed` and pinned by `TestCarriedDefect_SP02D4_PMinIsMeasuredOverCandidates`, outside this row's pattern. |
| `I-02.3` | §5.6 breakpoint OPT, always labelled not-plugin-actionable | MAPPED | `internal/eval`: `TestBreakpointOPT_KnownOptimum`, `TestBreakpointOPT_NoteIsAlwaysTheDisclaimer`, `TestBreakpointOPT_EchoesMarkerBudget`, `TestBreakpointOPT_PositionsAscending`, `TestBreakpointOPT_CandidatesCappedAt256`, `TestBreakpointOPT_EmptySession` | PASS | `…/inv/SP-02/I-02.3.txt` | direct | `Plan.Note == NotPluginActionable` is `TestBreakpointOPT_NoteIsAlwaysTheDisclaimer`; the driver-side twin `TestReplayDriver_BreakpointDisclaimerPrinted` also exists (not run here — it belongs to the driver rows). |
| `I-02.4` | Policies `stock` (§2.4 step 7), `null`, `oracle`; oracle byte-identical to `BeladyDetail`; stock keeps 5 files at 5K, `P == 0` | MAPPED | `internal/eval`: `TestStockPolicy_TopFiveFilesFiveKEach`, `TestStockPolicy_PreservationMinimums`, `TestStockPolicy_UsedNeverGoesNegative`, `TestStockPolicy_BudgetSmallerThanOneBlockTerminates`, `TestOraclePolicy_DelegatesToBelady`, `TestNullPolicy_Empty`, `TestPolicyNames_Sorted` | PASS | `…/inv/SP-02/I-02.4.txt` | direct | Bodies read. `policy_test.go:70-74` asserts exactly 5 `file:` keeps (`f4`…`f8`) and `require.Equal(t, 0, ks.P, "a Full Compact rewrites the whole message array, so p_min is 0")`. `belady_test.go:262` is `require.Equal(t, viaBelady, viaPolicy)` — the byte-identity clause. `TestPolicyNames_Sorted` still asserts the **three** built-ins `{null, oracle, stock}` inside `internal/eval`; the five-member set in the V4 map's `V4-SP02-02` is the *driver's* registry (`qompack-rehydrate`, `qompack-l3` are registered by `test/replay`), so this row is not superseded. `SP02-D6` touches this row's §2.4-step-7 model — see §5 Q1. |
| `I-02.5` | Counterfactual replay, horizon, determinism, live-mode refusal | MAPPED | `internal/eval`: `TestReplay_NullPolicyInjectsRepairForEveryDemand`, `TestReplay_OracleNeedsNoRepairsWhenItFits`, `TestReplay_PopulatesTheFieldsScoreRunNeeds`, `TestReplay_LiveModeRefusedWithoutEnv`, `TestReplay_LiveRunnerAbsent`, `TestReplay_DeterministicAcrossRuns`, `TestReplay_HorizonRespected`, `TestReplay_MultipleCompactionsAreParallelSlices` | PASS | `…/inv/SP-02/I-02.5.txt` | direct | 8 definitions / 11 `--- PASS` (subtests). Both named clauses have their own test: live-mode refusal without `QOMPACK_EVAL_LIVE`, and the two-run `go-cmp` equality. |
| `I-02.6` | The five §4.2 divergence metrics | MAPPED | `internal/eval`: 16 `TestCompare_*` definitions incl. `TestCompare_IdenticalRuns`, `TestCompare_EditDistanceProperties`, `TestCompare_EditDistanceKnown`, `TestCompare_DecisionPreservationIgnoresHorizonAgreement`, `TestCompare_GoldenScenarios` | PASS | `…/inv/SP-02/I-02.6.txt` | direct | 19 `--- PASS` (incl. subtests). `TestCompare_IdenticalRuns` carries the `{Horizon,1,0,true,1,0,0}` tuple; `TestCompare_EditDistanceProperties` carries symmetry. `TestCompare_DecisionPreservationIgnoresHorizonAgreement` is `SP02-D5`'s closing evidence and sits inside this row's pattern. |
| `I-02.7` | Scoring: micro-averaged fraction-of-OPT, §5.2 rewrite arithmetic, percentiles, report | MAPPED | `internal/eval`: 15 `TestScoreRun_*` (incl. `TestScoreRun_RewriteTokensSection52TableA` / `TableB`, `TestScoreRun_NoHardcodedMultiplier`, `TestScoreRun_Percentiles_NearestRank`), 7 `TestReport_*`, `TestMetricsOf_CoversEveryDirection` | PASS | `…/inv/SP-02/I-02.7.txt` | direct | 23 `--- PASS`. Constants read in source: `score_test.go:135` asserts `17_000` for `rewrite_span_tokens` (table A) and `score_test.go:151` asserts `157_000` (table B). `TestScoreRun_NoHardcodedMultiplier` (D11 at runtime) present and green. |
| `I-02.8` | The 24-session synthetic corpus is byte-stable | MAPPED | `internal/eval`: `TestSynthesize_ByteIdenticalForSeed`, `TestSynthesize_MatchesCommittedCorpus`, `TestSynthesize_EveryCompactionHasDemands`, `TestCorpus_ManifestHashesMatch`, `TestCorpus_CountAtLeastMinSessions`, `TestCorpus_SeedsAndFilesAreUnique`, `TestCorpus_RaisesEveryDemandKind`, `TestCorpus_BeladyBudgetBinds`, `TestSynthesize_ShapeInvariants`, `TestSynthesize_StockBeatsNullOnEverySession`, `TestSynthesize_MetaCarriesProvenance`, `TestSynthesize_IDIsShapeQualified`, `TestSynthesize_ToolMixOrderIndependent`, `TestSynthesize_DifferentSeedsDifferentSessions` | PASS | `…/inv/SP-02/I-02.8.txt` | direct | 93 `--- PASS` incl. subtests, `ok … 1.988s`. **One in-row skip:** `TestSynthesize_WriteCorpus` — quoted reason `"platform: set QOMPACK_EVAL_WRITE_CORPUS=1 to regenerate the committed corpus"`. That is the corpus *writer*, skipped by design and correctly not run here (M0-G6 and plan line 27 forbid corpus regeneration). All three historical clauses — regeneration byte-identity, `EveryCompactionHasDemands`, `CORPUS.json` hashes — were executed green. `TestCorpus_RaisesEveryDemandKind` / `TestCorpus_BeladyBudgetBinds` are `SP02-D1` / `SP02-D3`'s closing evidence and sit inside this row's pattern. |
| `I-02.9` | Recorded-transcript importer + redaction; 8 rules, idempotent, refuses in-repo destinations, no crashers | MAPPED | `internal/eval`: `TestImport_MapsToolUseAndResult`, `TestImport_TokensFromUsage`, `TestImport_CompactBoundaryBecomesCompactionAt`, `TestImport_UnknownRecordSkippedNotFatal`, `TestImport_MarksImportedSessionsAsRecorded`, `TestImport_RefusesDestinationInsideRepo`, `TestImport_RequiresSessionsDir`, `TestImport_UsesSessionsDirFromEnv`, `TestImport_LimitStopsEarly`, `TestRedact_AllEightRules`, `TestRedact_NoRedactLeavesSecretsIntact`, `TestRedact_Idempotent`, `TestRedact_URLCredentialsRejectsAMarkerUsername`, `TestRedact_PreservesTurnCountAndTokens`, `TestRedact_DoesNotMutateItsInput`; `FuzzRedact` (seed corpus) | PASS | `…/inv/SP-02/I-02.9a.txt`, `…/inv/SP-02/I-02.9b.txt` | direct | First command: 15 `--- PASS`. The second command's **fuzz campaign is deferred** (§4). As partial evidence I ran `go test ./internal/eval/ -run FuzzRedact` (seed corpus only, no fuzzing): `--- PASS: FuzzRedact` with every seed green, so the committed seeds and crashers are clean on this tree. "No crashers" over 60 s of *new* input remains unproven here. |
| `I-02.10` | Sublinear-growth guardrail; committed fixture `Exponent ≈ 0.62 ± 0.02` | MAPPED | `internal/eval`: `TestCheckSublinearGrowth_Sublinear`, `_Linear`, `_TooFewSamples`, `_SpanTooSmall`, `_NonMonotoneRawBytes`, `_DropsUnusableSamples`, `_Empty` | PASS | `…/inv/SP-02/I-02.10.txt` | direct | 7 `--- PASS`. The exact historical tolerance is in source at `internal/eval/growth_test.go:58` — `require.InDelta(t, 0.62, got.Exponent, 0.02)` over the committed fixture under `testdata/golden/eval/growth/`. |
| `I-02.11` | The replay gate — 2 % rule, sign-off trailer, phase assertions, corpus staleness, bloom FP ceiling | MAPPED (pattern corrected) | `test/replay`: 23 `TestGate_*` incl. `TestGate_TwoPercentBoundaryExclusive`, `TestGate_SignOffAllowsNamedMetricOnly`, `TestGate_BloomFPCeiling`, `TestGate_CorpusStaleness`, `TestGate_GrowthInconclusiveFails`; **plus** `TestReplayDriver_PhaseChecksMayNotBeDisabledInCI` (`test/replay/e2e_test.go:252`) and `TestRunPhaseChecks_RunsEveryMergedPhase` (`test/replay/gate_test.go:246`) | PASS | `…/inv/SP-02/I-02.11.txt`, `…/inv/SP-02/I-02.11b.txt` | direct | 23 `--- PASS` on the `TestGate_` pattern. The historical name `TestGate_PhaseChecksMayNotBeDisabledInCI` **does not exist** and is silently not selected by the `TestGate_` filter — the exact "prints ok for nothing" trap. Renamed; see §3. Both replacements run green (the second prints the phase-5/6 selection and Sequitur-ablation tables). |
| `I-02.12` | Phase-0 baseline reproducible: two `--write-baseline` runs byte-identical; `policies.stock.fraction_of_opt` equals committed `testdata/baseline/phase0.json`; `"corpusTier":"synthetic"` present; `--write-baseline` is boolean and a positional path exits 2 | SUPERSEDED-BY-WAVE4 | Literal CLI run twice (outputs redirected outside the tree) + `test/replay`: `TestReplayDriver_BaselineIsByteReproducible`, `TestReplayDriver_EndToEnd`, `TestReplayDriver_BaselineHasExactlyNineteenKeysPerPolicy`, `TestReplayDriver_LeftoverArgumentsAreBadInput`, `TestReplayDriver_RefusesACrossTierBaseline`, `TestReplayDriver_RefusesABaselineRecordedOverAnotherCorpus`, `TestCorpusIdentity_*` (5) | PASS | `…/inv/SP-02/I-02.12.txt`, `…/inv/SP-02/I-02.12-cli-a.txt`, `…/inv/SP-02/I-02.12-cli-b.txt`, `…/inv/SP-02/I-02.12-cli-badflag.txt`, baselines `…/inv/SP-02/v5-phase0-a.json` and `v5-phase0-b.json` | direct | The **equality target moved** under SP-19 M0-G6 — see §3. Measured: the two `--write-baseline` runs are `cmp`-identical; `corpusTier` is `"synthetic"`; fresh `policies.stock.fraction_of_opt` is **0.258291**, exactly the committed `testdata/baseline/phase0-recall.json`; the preserved `testdata/baseline/phase0.json` reads **0.695164** over a three-policy set and is *required* not to match the live corpus. Flag semantics reconfirmed live: `--write-baseline <path>` printed `replay: unexpected argument "…"` and `exit status 2`, having measured nothing. Runs used `--out ""` and a scratch `--baseline`; `git status --porcelain` unchanged before/after. |
| `I-02.13` | Replay driver e2e; exit 0; report parses | MAPPED | `test/replay`: `TestReplayDriver_EndToEnd` | PASS | `…/inv/SP-02/I-02.13.txt` | direct | 1 `--- PASS`, `ok … 1.432s`. Body read: asserts `exitOK`, `"24 sessions"` on stdout, the report unmarshals into `DriverReport`, `stock.fraction_of_opt` equals the committed baseline, `Regressions` empty, `CorpusTier == tierSynthetic`, `Latency == latencyModelled`. |
| `I-02.14` | Eval benchmark budgets E-1…E-5 (≤ 250 / 50 / 20 / 15 ms per op) | MAPPED-CMD | `go test ./internal/eval/ -bench BenchmarkBeladyDetail_400Turns\|BenchmarkSynthesize_320Turns\|BenchmarkCompare_400Actions\|BenchmarkBreakpointOPT_256Candidates -benchtime 2s -run ^$`; E-1 via `test/replay`: `TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus`, `TestReplayDriver_MaxCPUExceeded` | PASS | `…/inv/SP-02/I-02.14.txt`, `…/inv/SP-02/I-02.14b.txt` | direct | Measured under co-load: BeladyDetail **1.688 ms/op** (E-2 ≤ 250), Synthesize **0.802 ms/op** (E-3 ≤ 50), Compare **1.604 ms/op** (E-4 ≤ 20), BreakpointOPT **1.816 ms/op** (E-5 ≤ 15). Margins are 8×–148×, so co-load cannot flip the verdict and no quiet re-run is needed. `-run ^$` added so no test body runs alongside. **E-1 is not in the row's command** — it is the driver's CPU budget (< 120 s, `plans/V2-SP-02-replay-harness-belady-baseline.md:1095`), covered by the two `test/replay` tests above, both run green. See §5 Q3. |

## 3. Old-to-new assertion map

| Row | Historical | Current | Reason / replacement |
|---|---|---|---|
| `I-02.11` | `TestGate_PhaseChecksMayNotBeDisabledInCI`, expected to be selected by `go test ./test/replay/ -run TestGate_` | `test/replay`: **`TestReplayDriver_PhaseChecksMayNotBeDisabledInCI`** (`test/replay/e2e_test.go:252`), asserting the `exitPhaseNoSkip` behaviour and `report.PhaseChecksSkipped == true`; and **`TestRunPhaseChecks_RunsEveryMergedPhase`** (`test/replay/gate_test.go:246`), asserting `runPhaseChecks` executes every registered phase at or below the requested one | Renamed into the driver-e2e family when the phase registry moved to `test/replay/phases.go` (`phaseChecks` map, `runPhaseChecks`). **The historical pattern is a silent no-op:** `-run TestGate_` does not select the new name and `go test` prints `ok` regardless. Corrected pattern for V5 and later: `-run 'TestGate_\|TestReplayDriver_PhaseChecks\|TestRunPhaseChecks_'`. |
| `I-02.12` | "`policies.stock.fraction_of_opt` equals the committed `testdata/baseline/phase0.json`" | **`testdata/baseline/phase0-recall.json`** is the live baseline (`defaultBaselinePath`, `test/replay/main.go:78`); `testdata/baseline/phase0.json` is preserved unchanged as the pre-correction record and is now *required to differ* from a live replay (`TestCorpusIdentity_RefusesTheOldCorpusBaseline`, `TestCorpusIdentity_TheTwoCommittedBaselinesDescribeTwoDifferentCorpora`) | SP-19 **M0-G6**: "Original metric outputs preserved; corrected labels/corpus changes separately versioned." The corpus correction that closed `SP02-D1` / `SP02-D3` moved stock's fraction-of-OPT from 0.695164 to 0.258291 and added `qompack-rehydrate` to the policy set, so the two artifacts describe two different corpora by construction. Both stay committed, each with its `.ledger.v1.json` and `.provenance.v1.json` sidecar. Consistent with V4 map row `V4-SP02-05` (`SUPERSEDED-BY-CORRECTIVE`, commit `e35b44e`), re-verified on this tree. The row's *procedure* (write twice, compare bytes, check `corpusTier`) is unchanged and passes. |
| `I-02.14` | "E-1..E-5" implied by a four-benchmark command | E-1 lives in `test/replay`, not `internal/eval`: `TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus` (and its negative `TestReplayDriver_MaxCPUExceeded`) | Not a semantic change — the row's command only ever measured E-2…E-5. Recorded so E-1 is not scored `MISSING` by a future name-only scan. See §5 Q3. |

Rows **not** superseded despite a plausible reading of the V4 map: `I-02.4`. `V4-SP02-02` reports a
five-member policy registry; that is the **driver's** set (`stock,null,oracle,qompack-rehydrate` by
default, plus `qompack-l3` from `test/replay/l3policy`). `internal/eval`'s own registry is still
`{null, oracle, stock}` and `TestPolicyNames_Sorted` still asserts exactly that, so the historical
row stands as written.

## 4. Deferred to coordinator

One sub-command only. No row is `NOT-RUN`, and no row is `FAIL-COLOAD-SUSPECT`.

| Row | Exact command | Reason |
|---|---|---|
| `I-02.9` (fuzz half) | `go test ./internal/eval/ -run xxx -fuzz FuzzRedact -fuzztime 60s` | Fuzz runs are outside this seat's mandate. The `-run` half of the row and the `FuzzRedact` **seed corpus** both ran green here (`…/inv/SP-02/I-02.9a.txt`, `…/inv/SP-02/I-02.9b.txt`), so the row is scored `PASS` on executed evidence; what is deferred is only the 60 s campaign over *new* inputs ("no crashers"). Note the row's `-run xxx` guard is the correct shape — it suppresses the unit tests while `-fuzz` runs — but on its own `-run xxx` would print `ok` for nothing, so the command is meaningful only with `-fuzz` present. |

Explicitly **not** deferred: `I-02.14`. Its four budgets cleared by 8×–148× under co-load, so a
quiet-window re-measurement would change no verdict. If the coordinator wants a clean number for the
record anyway, the command is in the row's evidence cell and the ns/op figures are in
`…/inv/SP-02/I-02.14.txt`.

## 5. Questions

**Q1 — `SP02-D6` names V5-VERIFY as its resolver, and no SP-02 row covers it.**
`plans/CARRIED-DEFECTS.tsv` carries `SP02-D6  SP-02  V2-VERIFY  deferred:V5-VERIFY  -  hostSkillBudget
is still dead: 2.4 step 7's invoked-skills restore cannot be modelled without a Session that records
skill invocations, and Session is fixed by 5.18 (the 4/3 hostPadTokens half is fixed and the stock
model now applies it)`. Per that file's own header, "No unresolved row may survive its RESOLVER's
completion report", and `deferred:` counts as unresolved. **This checkpoint must therefore fix it,
re-defer it to a named later checkpoint with the reason added to the detail document, or mark it
`wontfix` — doing nothing blocks the V5 report.** None of my 14 rows asserts it; the closest,
`I-02.4`, exercises the half that *was* fixed (`policy_test.go:80` cites `SP02-D6` for the 4/3
host-estimate padding). Which disposition does the coordinator want, and does it belong to this seat
or to SP-02's plan owner? Related: `TestCarriedDefects_WaveReportRequiresResolution` (`test/guards`)
is on this tree's pre-existing-failure list, so that guard may already be red for exactly this
reason — I did not run it (not one of my rows).

**Q2 — should the V5 inventory row text be rewritten, or preserved verbatim with the map carrying
the correction?** V4-VERIFY's convention was explicit: "The inventory's 212 rows are preserved
verbatim there; this file adds a parallel adjudication keyed by row ID, so that history is never
rewritten." I followed it — `I-02.12` still reads `phase0.json` in the table, and §3 carries the
correction to `phase0-recall.json`. If V5 instead wants the row text updated in place (so future
scans stop re-deriving the same correction), say so and the two affected rows (`I-02.11`, `I-02.12`)
can be restated.

**Q3 — is `TestReplayDriver_BothLimitsHoldOnTheCommittedCorpus` the accepted home for E-1?**
Row `I-02.14` is titled "E-1..E-5" but its command measures only E-2…E-5, and E-1 is a driver CPU
budget (< 120 s) with no benchmark. I scored the row `PASS` on both halves. Confirm the mapping so a
later name-only pass does not score E-1 `MISSING`.

**Q4 — M0-G5 (accounting) has no row in this SP-02 set.** The reconciliation target names "request
categories and missing telemetry" alongside the baseline gate, but all 14 `I-02.*` rows are
replay / Belady / baseline; none asserts usage attribution, category sums, or rate-schedule
provenance. Either M0-G5 lands entirely in another seat's rows, or SP-02 is short a row. I did not
invent one, and I am flagging rather than scoring it `MISSING`, since I cannot see the other seats'
row sets.

## Method notes

- Every `-run` / `-bench` pattern was confirmed with `go test -list <pattern> <pkg>` **before** the
  run; the enumerations are in `…/inv/SP-02/list-01.txt` … `list-14.txt`. One pattern selected
  nothing under its historical name (`I-02.11`) and is corrected in §3. The other thirteen selected
  the definitions their rows name.
- No `go test` output was piped; every run was redirected to a file and read back, and exit codes
  were captured separately from the redirect.
- No race run, no fuzz campaign, no `./...`, no unfiltered `test/e2e` or `test/integration` run, no
  `devtool` invocation, no golangci-lint.
- The only writes performed anywhere in the repository are this file and its parent directory. The
  two driver invocations wrote to the scratchpad (`--out ""`, `--baseline <scratch path>`);
  `git status --porcelain` taken before and after is byte-identical.

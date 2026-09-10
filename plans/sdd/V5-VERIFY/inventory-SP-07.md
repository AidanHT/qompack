# V5-VERIFY inventory — SP-07 (DAG / dependencies)

- **Rows:** `I-07.1` … `I-07.11` (11 retained IDs, §2.7 of `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md` at HEAD `7f92af5`). None dropped.
- **Reconciliation target:** SP-20 `T20-M2-01`/`T20-M2-02` and SP-15 `M5-G15-B` — *approximate observed relation graph, unknown dependency coverage retained*.
- **Owner plan sections consulted:** `plans/V4-SP-20-capture-storage-and-state-remediation.md` §M2-01, §M2-02 and its T20 test-plan table (lines 109–137); `plans/V5-SP-15-analyzer-selection-and-grammar.md` gate row `M5-G15-B` (line 64), delivery line 95 (landed `badd27b`), acceptance line 174; `plans/sdd/V4-VERIFY/reconciliation-map.md` (its row IDs are `V4-SPnn-mm`, disjoint from the `I-07.x` space, so nothing there was reusable verbatim — its method and vocabulary were); `plans/CARRIED-DEFECTS.tsv` (no SP-07/dag row).
- **Tree / HEAD:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @ `87c0c1d` (*chore(sp21): integrate deterministic admission control*).
- **Platform:** Windows 11, `go1.26.6 windows/amd64`. **Date:** 2026-09-08. Machine shared with concurrent sibling agents.
- **Artifacts:** `C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-07/`

## Counts

| Disposition | Rows |   | Result | Rows |
|---|---:|---|---|---:|
| `MAPPED` | 11 |   | `PASS` | 10 |
| `MAPPED-CMD` | 0 |   | `FAIL` | 0 |
| `SUPERSEDED-BY-WAVE4` | 0 |   | `FAIL-BASELINE` | 0 |
| `RETIRED` | 0 |   | `FAIL-COLOAD-SUSPECT` | 0 |
| `MISSING` | 0 |   | `SKIP` | 0 |
| `NEEDS-COORDINATOR` | 0 |   | `NOT-RUN` | 1 |

`I-07.11` is scored `PASS` on its executed test half; its second command (the package benchmark
sweep) is deferred and listed in §4. Every `-run` pattern was confirmed with `go test -list` before
execution (`00-list.txt`); no pattern selected zero tests.

## Reconciliation finding (why nothing here is superseded)

`internal/dag` is fully present on this tree and none of its eleven historical assertions was
weakened by wave 4. The reconciliation target lands **beside** the package, not inside it:

- **SP-15 `M5-G15-B`** (commit `badd27b`) adds the qualification that the DAG is an *approximate
  relation graph*: `internal/analyzer/testdata/diagnostics/shared-file-edge-is-not-relevance/` with a
  `provenance.json`, asserted by `internal/analyzer`: `TestRelations_ASharedFileEdgeIsPreservedAndProvesNothing`
  (run here, **PASS**, artifact `sp15-qualifier.txt`). Its qualified claim — "an edge is an
  association, not a dependency; `analyzer.isDependenceEdge` excludes `shared_file` from the Requires
  closure, and `analyzer.NewRelations` preserves every excluded edge rather than filtering it away" —
  is *consistent with* SP-07's own closing-note-3 stance (`I-07.7`) and adds no obligation to
  `internal/dag`. The over-claim it forbids (absence of an edge read as irrelevance) is exactly what
  `I-07.7`'s guard already mechanizes on the producer side.
- **SP-20 `T20-M2-01`/`T20-M2-02`** ("unknown dependency coverage retained") is realized in
  `internal/negknow` / `internal/core`, not in `internal/dag`: `negknow.Health.DependencyCoverage`
  (`internal/negknow/authorization.go:164`, consumed at `internal/daemon/reusable.go:106,144`) and
  `core.CoverageUnknown` / `OutcomeUncertain` (`internal/core/evidence.go:53,136`). Those assertions
  belong to the negknow/state row spaces; no `I-07.x` assertion changes because of them.

Consequence: all eleven rows are **retained compatible historical regressions**. The old-to-new map
in §3 is therefore entirely test *renames* plus one genuine pattern correction (`I-07.7`).

## 1–2. Row table

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| `I-07.1` | 9 node kinds, 8 edge kinds, multipliers exactly `1.00,1.00,1.00,0.95,0.88,0.60,0.50,0.30`, node-ID scheme | `MAPPED` | `internal/dag`: `TestNodeKindTextRoundTrip`, `TestEdgeKindTextRoundTrip`, `TestEdgeKindMultiplierTable`, `TestNodeKindTablesAligned`, `TestNodeIDConstructors`, `TestNodeIDLongKeyHashSuffix`, `TestNodeIDControlCharsSanitized`, `TestParseNodeID` (8 selected) | `PASS` | `I-07.1.txt` | direct | Multiplier table read at `kinds.go:81-91` and pinned case-for-case at `kinds_test.go:112-131`: produces/consumes/explains 1.00, shared_symbol 0.95, shared_file 0.88, seq 0.60, control 0.50, supersedes 0.30 — the historical eight, unchanged. `TestNodeKindTablesAligned` pins `len == 10` (nine kinds + the `KindInvalid` sentinel). |
| `I-07.2` | Graph mutation: validation, upsert merge, anchor earliest-`Pos`, dedup, dangling, tombstones | `MAPPED` | `internal/dag`: `TestAddNodeValidation`, `TestAddNodeUpsertMerge`, `TestAddNodeEphemeralSticky`, `TestAnchorNodePosIsEarliest`, `TestAddEdgeValidation`, `TestAddEdgeWeightNormalized`, `TestAddEdgeDedup`, `TestAddEdgeFoldSurvivesReload`, `TestAddEdgeDanglingEndpoint`, `TestOutInCopies`, `TestTombstoneHidesNode`, `TestClosedGraphRejects` (12 selected) | `PASS` | `I-07.2.txt` | direct | The historical pattern still selects every clause; the `TestAddNode`/`TestAddEdge` families have grown (§3) rather than moved. |
| `I-07.3` | Concurrency safety, 8 writers × 8 readers, no race, no deadlock | `MAPPED` | `internal/dag`: `TestConcurrentMutationAndRead` (`graph_test.go:356`, confirmed by `-list`) | `NOT-RUN` | `00-list.txt` | direct | **Race runs are outside this seat's allowance.** The definition exists and is selected; deferred to the coordinator's quiet serial pass (§4). |
| `I-07.4` | `CrossingEdges(pos)` = `segment_coupling(p)`; `lo < pos <= hi` exact at both ends; matches brute force | `MAPPED` | `internal/dag`: `TestCrossingEdgesBoundaries`, `…DirectionIrrelevant`, `…ExcludesDangling`, `…EqualPositions`, `…EmptyGraph`, `TestCrossingEdgesGolden`, `TestNodesAfterOrdering`, `TestNodesAfterReturnsFreshSlice`, `TestPropCrossingEdgesMatchesBruteForce`, `TestPropNodesAfterMatchesFilter` (10 selected) | `PASS` | `I-07.4.txt` | direct | Run at `-rapid.checks=1000` as written (`pgregory.net/rapid` is live in `index_test.go`). The property tests were renamed `Prop…` → `TestProp…`; the unanchored `-run` element still selects them, so the historical command needed no edit. The SP-15 qualification constrains what a *caller* may infer from an edge, not what `CrossingEdges` computes. |
| `I-07.5` | Backward/forward slicing with scores, not keep/drop; max-path scoring never additive; `DefaultSliceOptions(Defaults()).Thin == true` | `MAPPED` | `internal/dag`: `TestBackwardSliceChain`, `TestBackwardSliceTakesMaxPath`, `TestForwardSliceDirection`, `TestThinDropsControlOnly`, `TestSliceMaxNodes`, `TestSliceMaxNodesExactFitNotTruncated`, `TestSliceMaxDepth`, `TestSliceMinScoreFloor`, `TestSliceOrderTieBreak`, `TestSliceGoldenBackward`, `TestDefaultSliceOptionsFromConfig`, `TestSliceDeadlineTruncates` (12 selected) | `PASS` | `I-07.5.txt` | direct | `slice_test.go:332-335` asserts `DefaultSliceOptions(config.Defaults()).Thin` true verbatim, and that `"full"` and `"bogus"` both yield false. The max-path clause is `TestBackwardSliceTakesMaxPath`. |
| `I-07.6` | Slice properties: thin slice ⊆ full, scores bounded and monotone | `MAPPED` | `internal/dag`: `TestPropThinSliceIsSubsetOfFull`, `TestPropScoresBoundedAndMonotone` | `PASS` | `I-07.6.txt` | direct | Run at `-rapid.checks=1000`. Renamed `Prop…` → `TestProp…` (§3); the pattern is unaffected. |
| `I-07.7` | No selection authority: no exported keep-set/drop-list/`map[NodeID]bool`; `doc.go` still contains `NO SELECTION AUTHORITY` | `MAPPED` | `internal/dag`: `TestNoBooleanKeepAPI` (`api_guard_test.go:93`) **plus** `TestNoSelectionAuthorityNoteSurvives` (`api_guard_test.go:172`) | `PASS` | `I-07.7.txt`, `I-07.7b.txt` | direct | **Pattern corrected** (§3): the `doc.go` half of the assertion now lives in its own test, which the historical `-run TestNoBooleanKeepAPI` does not select. Both halves were run under the corrected pattern and pass. `doc.go:7` still carries the note verbatim. The AST guard also asserts it inspected a non-zero number of exported declarations, so it cannot pass vacuously. |
| `I-07.8` | Append-only NDJSON log, torn tail (`TruncatedTail == true`), corrupt line loud exactly once, auto-flush at 2000, compaction; log round-trip property | `MAPPED` | `internal/dag`: `TestOpenRoundTrip`, `TestOpenMissingFile`, `TestOpenTornTail`, `TestOpenCorruptLines`, `TestOpenResultIsMaintainer`, `TestFlushIsAppendOnly`, `TestFlushIsNoOpWhenNothingPending`, `TestFlushErrorRetainsPending`, `TestAutoFlushAt2000`, `TestCompactDropsTombstoned`, `TestCompactNoOpBelowThreshold`, `TestCompactFlushesPendingFirst`, `TestCompactPreservesSliceAnswers`, `TestCompactCancelled`, `TestCompactOnClosedGraph` (15 selected); `TestPropLogRoundTrip` | `PASS` | `I-07.8a.txt`, `I-07.8b.txt` | direct | Both historical commands run. `log_test.go:263` requires `TruncatedTail` true on the torn tail; `log_test.go:290-295` requires `LoadErrors == 1` and `require.Len(loud, 1)` — "exactly one Loud per Open, never one per bad line". The property ran at `-rapid.checks=500` (16.6 s). |
| `I-07.9` | §8.1 item-4 edge builders; builder output acyclic over 200 built tool uses | `MAPPED` | `internal/dag`: 16 `TestBuild*` (incl. `TestBuildToolUseEmitsSection814Edges`, `TestBuildToolUseControlOnlyWhenNoSharedState`, `TestBuildToolUseSupersedes`, `TestBuildSegmentChain`) + `TestBuilderOutputIsAcyclic` (17 selected) | `PASS` | `I-07.9.txt` | direct | `builders_test.go:594` — `acyclicToolUses = 200`, the historical count unchanged. |
| `I-07.10` | Thin-vs-full measured tradeoff, **without** `-update`: mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`, `ns_thin ≤ ns_full` | `MAPPED` | `internal/dag`: `TestThinVsFullComparison` (`slice_compare_test.go:200`) | `PASS` | `I-07.10.txt` | direct | Re-run fresh with `-count=1` and no `-update`. Eight seeds: ratio 0.334–0.474 (mean ≈ 0.429 ≤ 0.75), recall 0.878–0.882 (mean ≈ 0.880 ≥ 0.85), thin wall clock below full on every seed (e.g. 401 µs vs 1.32 ms). A measured row, but it clears each threshold with margin, so co-load is not a plausible confound. |
| `I-07.11` | Slicing latency budgets: `BackwardSlice5000`/`ForwardSlice5000` median < 1 ms, `CrossingEdges` median < 5 µs, `BuildToolUse` < 3 µs | `MAPPED` | `internal/dag`: `TestSliceLatencyBudget` (subtests `backward`, `forward`), `TestCrossingLatencyBudget` (`bench_test.go:242,310`) | `PASS` (test half; bench half `NOT-RUN`, §4) | `I-07.11.txt` | direct | Re-run fresh with `-count=1` under co-load; both pass. `CrossingEdges` logged 140 ns/call against the 5 µs ceiling (~35× margin). The row's **second** command, `go test ./internal/dag/ -bench . -benchtime 2s`, is an unfiltered benchmark sweep over 11 benchmarks (incl. `BenchmarkOpen20k`/`BenchmarkCompact20k`) at 2 s each; it exceeds this seat's one-minute benchmark cap and is the only source for the `BuildToolUse < 3 µs` clause. Deferred (§4). |

## 3. Old-to-new assertion map

No row is `RETIRED` or `SUPERSEDED-BY-WAVE4`. The map below is therefore (a) one corrected pattern and
(b) the renames a reader of the historical commands needs in order to know what actually ran.

**(a) Corrected pattern — one row.**

| Row | Historical command | Corrected command | Reason |
|---|---|---|---|
| `I-07.7` | `go test ./internal/dag/ -run TestNoBooleanKeepAPI -v` | `go test ./internal/dag/ -run 'TestNoBooleanKeepAPI\|TestNoSelectionAuthorityNoteSurvives' -v` | The row asserts two things. The AST keep-set guard is `TestNoBooleanKeepAPI`; the "`doc.go` still contains `NO SELECTION AUTHORITY`" half was split into `TestNoSelectionAuthorityNoteSurvives` (`api_guard_test.go:172`), which the historical pattern does **not** select. Running the historical command alone would have scored that half vacuously. Both were run — `I-07.7b.txt`. |

**(b) Renames — historical name → current definition(s).** Every one is still selected by the
historical unanchored `-run` element, so no other command needed editing; they are recorded so the
mapping is explicit rather than incidental.

| Row | Historical name | Current definition(s) | Reason |
|---|---|---|---|
| `I-07.2` | `TestAddNode` | `TestAddNodeValidation`, `TestAddNodeUpsertMerge`, `TestAddNodeEphemeralSticky` | Split one test per clause. |
| `I-07.2` | `TestAddEdge` | `TestAddEdgeValidation`, `TestAddEdgeWeightNormalized`, `TestAddEdgeDedup`, `TestAddEdgeFoldSurvivesReload`, `TestAddEdgeDanglingEndpoint` | Split; `FoldSurvivesReload` is coverage beyond the historical clause. |
| `I-07.4` | `PropCrossingEdgesMatchesBruteForce` | `TestPropCrossingEdgesMatchesBruteForce` | `rapid` properties carry the `Test` prefix so `go test` runs them at all. |
| `I-07.4` | `PropNodesAfterMatchesFilter` | `TestPropNodesAfterMatchesFilter` | Same. |
| `I-07.5` | `TestBackwardSlice` | `TestBackwardSliceChain`, `TestBackwardSliceTakesMaxPath` | Split; the max-path (never additive) clause is the second. |
| `I-07.5` | `TestForwardSlice` | `TestForwardSliceDirection` | Renamed. |
| `I-07.5` | `TestSliceMax` | `TestSliceMaxNodes`, `TestSliceMaxNodesExactFitNotTruncated`, `TestSliceMaxDepth` | Split. |
| `I-07.5` | `TestSliceGolden` | `TestSliceGoldenBackward` (`golden_test.go:247`) | Golden split by direction. |
| `I-07.6` | `PropThinSliceIsSubsetOfFull`, `PropScoresBoundedAndMonotone` | `TestPropThinSliceIsSubsetOfFull`, `TestPropScoresBoundedAndMonotone` | `Test` prefix, as above. |
| `I-07.8` | `TestOpen` | `TestOpenRoundTrip`, `TestOpenMissingFile`, `TestOpenTornTail`, `TestOpenCorruptLines` (plus `TestOpenResultIsMaintainer`, from the conformance file) | Split per failure mode. |
| `I-07.8` | `TestFlush` | `TestFlushIsAppendOnly`, `TestFlushIsNoOpWhenNothingPending`, `TestFlushErrorRetainsPending` | Split. |
| `I-07.8` | `PropLogRoundTrip` | `TestPropLogRoundTrip` | `Test` prefix. |
| `I-07.9` | `TestBuild` | 16 `TestBuild*` definitions in `builders_test.go` | Family grown; `TestBuildToolUseEmitsSection814Edges` is the §8.1 item-4 clause proper. |

**Additive qualifier (not a supersession), recorded for the reconciliation target.** SP-15
`M5-G15-B` adds `internal/analyzer`: `TestRelations_ASharedFileEdgeIsPreservedAndProvesNothing` with
fixture `internal/analyzer/testdata/diagnostics/shared-file-edge-is-not-relevance/` and its
`provenance.json` (commit `badd27b`). It qualifies how a *consumer* may read a DAG edge — association,
not dependency; absence of an edge is not irrelevance — and preserves every excluded edge rather than
filtering it away. It changes no `I-07.x` assertion, and was run here (**PASS**, `sp15-qualifier.txt`)
as evidence the qualification is live on this tree.

## 4. Deferred to coordinator

| Row | Exact command | Reason |
|---|---|---|
| `I-07.3` | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/dag/ -run TestConcurrentMutationAndRead -race -v -count=1` | Race runs are outside this seat's allowance. Definition confirmed present by `-list`; needs the quiet serial pass. |
| `I-07.11` (bench half) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test ./internal/dag/ -bench . -benchtime 2s -run '^$'` | Unfiltered benchmark sweep (11 benchmarks × 2 s, incl. `Open20k`/`Compact20k`) exceeds the one-minute benchmark cap, and is a wall-clock gate that co-load would distort. It is also the only source for the row's `BuildToolUse < 3 µs` clause — `BenchmarkAddToolUse` (`bench_test.go:390`) — which the test half does not cover. |

Two artifacts in the scratch directory (`I-07.3-norace.txt`, `qualification-analyzer.txt`, timestamped
18:54–18:55) predate this pass and were **not** produced or counted here. The first records
`TestConcurrentMutationAndRead` passing *without* `-race`, which is not what `I-07.3` asks for; the
row stays `NOT-RUN`.

No row was classified `FAIL-COLOAD-SUSPECT`: the two timing rows this seat could legitimately run
(`I-07.10`, and `I-07.11`'s test half) were re-run fresh with `-count=1` and both cleared their
thresholds with large margin.

## 5. Questions

None blocking. Two items for the coordinator's awareness rather than adjudication:

1. **`I-07.11` is a partial row by construction.** Its `BuildToolUse < 3 µs` clause has no test-side
   assertion at all — only `BenchmarkAddToolUse`. If the quiet pass does not run the deferred bench
   command, that clause stays unverified for V5; it must not be inferred from the
   `TestSliceLatencyBudget` / `TestCrossingLatencyBudget` pass.
2. **Reconciliation-target scope.** SP-20 `T20-M2-01`/`T20-M2-02` ("unknown dependency coverage
   retained") has no assertion inside `internal/dag`; it is carried by `negknow.Health.DependencyCoverage`
   and `core.CoverageUnknown` / `OutcomeUncertain`. If the coordinator intends those to be *scored*
   under SP-07 rather than under the negknow/state row spaces, this report does not do so and a new
   row would have to be added.

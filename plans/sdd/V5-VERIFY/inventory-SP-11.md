# V5-VERIFY inventory — SP-11 (rehydrator L5, rules, skills)

**Subplan:** SP-11 — rehydration / path-scoped rules / skill index (L5).

**Owner plan sections consulted**
- `plans/V4-SP-11-rehydrator-l5.md` @ HEAD — §"Implementation stages" IS-11-01…IS-11-06, the
  T11-AUTH/BUDGET/LIFE/LOAD/SCOPE/POINTER/CORRECT/ROLLBACK future-assertion table (ll. 103–114), and
  the Done checklist (ll. 160–176). **This document at HEAD is a forward-looking planning pass:**
  it states "No tests run in this planning pass", every T11 gate is an unchecked future assertion,
  and it no longer carries the historical verification table these rows came from. See §5 Q1.
- `plans/sdd/V4-VERIFY/inventory.md` and `plans/sdd/V4-VERIFY/reconciliation-map.md` rows
  `V4-SP11-01`…`V4-SP11-24` — the adjudicated source of the historical assertions (reused where
  still valid; **every result below was re-run on this tree**, none inherited).
- `plans/CARRIED-DEFECTS.tsv` — no SP-11 row exists; `SP02-D6` is owned by V5-VERIFY but belongs to
  `internal/eval`, not to this slice.

**Tree / HEAD:** `C:/Users/Quant/Documents/Programming/Projects/qompack-v5`, branch `verify/v5` @
`87c0c1dd8ba10e928a70c83262599fadf5e47d55` ("chore(sp21): integrate deterministic admission control").
**Platform:** Windows 11 Home 10.0.26200, go1.26.6 windows/amd64, Intel Core Ultra 7 155H (22 logical).
**Date:** 2026-09-08. **Machine was co-loaded** (sibling V5-VERIFY children in the same tree).

**Rows:** 19 (`I-11.1` … `I-11.19`), all reconciled, none dropped.

**Disposition counts:** MAPPED 16 · MAPPED-CMD 0 · SUPERSEDED-BY-WAVE4 0 · RETIRED 2 · MISSING 1 ·
NEEDS-COORDINATOR 0.

**Result counts:** PASS 19 · FAIL 0 · FAIL-BASELINE 0 · FAIL-COLOAD-SUSPECT 0 · SKIP 0 (row-level;
two *sub-tests* platform-skip on Windows — see §5 Q3) · NOT-RUN 0.

**Wave-4 impact: none.** `git diff --name-only f84cdf4 87c0c1d` touches no file under
`internal/rehydrate/`, `internal/rules/`, `internal/skills/` or `internal/state/`, and no
`internal/config` rehydrate key. Wave 4 (SP-14/15/16/19/20/21) therefore superseded **zero** SP-11
requirements; the two RETIRED rows below were retired by the earlier **V4 corrective units D/E/H**
(`9d9550d`, `76e0a9e`, `0259c68`, `df9e32b`), which are already in this tree's base. The vocabulary
has no `SUPERSEDED-BY-CORRECTIVE` slot, so those rows are recorded as `RETIRED` with an explicit
replacement pointer rather than mis-attributed to wave 4.

---

## 1. Row table

Artifact paths are relative to
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/inv/SP-11/`.

| Row | Historical assertion (abbrev) | Disposition | Current evidence | Result | Artifact | Conf. | Note |
|---|---|---|---|---|---|---|---|
| I-11.1 | Glob engine + `paths:` frontmatter: `**` semantics, CRLF frontmatter, unclosed frontmatter is not a rule | MAPPED | `internal/rules`: `TestMatch_Star_WithinSegment`, `TestMatch_DoubleStar_ZeroSegments` / `_ManySegments` / `_Trailing_MatchesDirItself`, `TestMatch_BareGlob_ImpliesAnyDepth`, `TestMatch_CharClass`, `TestMatch_QuestionMark`, `TestMatch_NoMatchAcrossSegmentWithSingleStar`, `TestMatch_RejectsPathologicalPattern`, `TestPropMatch_NeverPanics`, `TestParseFront_ListForm` / `_InlineArray` / `_ScalarForm` / `_CRLF` / `_Unclosed` / `_NoFrontmatter` / `_UnknownKeysIgnored` / `_OversizeFrontmatter` — 18 tests | PASS | `I-11.1.txt` | direct | Run verbatim with `-rapid.checks=1000`; `rapid` is registered by `internal/rules/glob_test.go`. |
| I-11.2 | Path-scoped rule restoration (G4.1): pointer set only; skips unscoped/empty/oversize; deterministic | MAPPED | `internal/rules`: `TestPathScoped_MatchesOnePointer`, `_MatchesInlineRuleDirectlyUnderDotClaude`, `_SkipsUnscoped`, `_SkipsEmptyPaths`, `_SkipsOversizeFile`, `_NoPointers`, `_MissingClaudeDirIsNotAnError`, `_Deterministic`, `_CaseInsensitivePointer`, `_BodyIsCRLFNormalized`, `_TokensBaselineIsSet` — 11 tests | PASS | `I-11.2.txt` | direct | `TestPathScoped_Deterministic` carries the repeat-run clause internally; no separate 10-run loop needed. |
| I-11.3 | Nested `CLAUDE.md` (G4.2): containing dir only, never the project root; unicode/spaces/>260-char paths | MAPPED | `internal/rules`: `TestNestedClaudeMD_MissingRootIsNotAnError`, `_ContainingDir`, `_ExcludesProjectRoot`, `_WalksAncestors`, `_AncestorTwoLevelsUp`, `_AncestorWalkStopsBeforeRoot`, `_Dedups`, `_UnicodeAndSpaces`, `_LongPath`, `_NestedFlag`, `_OutermostFirstSurvivesCase` — 11 tests | PASS | `I-11.3.txt` | direct | "never ancestors" is now conditional: `_WalksAncestors` / `_NestedFlag` gate ancestor walking behind an explicit flag, and `_AncestorWalkStopsBeforeRoot` + `_ExcludesProjectRoot` preserve the root exclusion. See §3.4. |
| I-11.4 | Skill index (G4.4): budget from `runtime.rehydrate.skillIndexTokens`, never the literal 450; prefix truncation not cheapest-first | MAPPED | `internal/skills`: `TestIndex_DirAndFlatForms`, `_NameFromFrontmatterElseBasename`, `_DescriptionFallsBackToFirstBodyLine`, `_DescriptionEmpty`, `_BudgetPrefixTruncation`, `_ZeroBudgetReturnsAll`, `_Deterministic`, `_SkipsSymlinks`, `_SkipsOversize`, `TestBodyTokens`, `TestBodyTokens_StripsFrontmatter`, `TestBodyTokens_MissingFile` — 12 tests; plus `TestSkillIndex_FitsTheConfiguredBudget`, `TestSkillIndex_ZeroBudgetIsTheFullSet` | PASS | `I-11.4.txt`, `I-11.4b.txt` | direct | Forbidden-literal clause verified structurally: `450` appears only in doc comments and test prose; `internal/rehydrate/items.go:1015` reads `r.Cfg.Runtime.Rehydrate.SkillIndexTokens`. `TestIndex_SkipsSymlinks` platform-skips (§5 Q3). |
| I-11.5 | The eight §8.6 items in normative order; `## 1.`…`## 8.` at increasing offsets with `## 6a.`/`## 6b.` between 6 and 7 | MAPPED | `internal/rehydrate`: `TestRenderOrder_IsIotaOrder`, `TestBuild_ItemOrderInPayload`, `TestBuild_RankCountsEmittedItemsFromZero` | PASS | `I-11.5.txt` | direct | **Pattern corrected**, see §3.1. `render.go:74,76` still emit the literal `## 6a.` / `## 6b.` headings between items 6 and 7. |
| I-11.6 | Injection tagging and sentinel; `<!-- qompack:injected seq=7 ver=1 -->` wrapper round-trips | MAPPED | `internal/rehydrate`: `TestBuild_InjectionTagging`, `TestUnwrap_RoundTrips`, `_ToleratesTrailingContent`, `_RejectsMalformed`, `_AcceptsAFutureSchemaVersion`, `TestWrap_TagsThePayload`, `_IsStrippableByTheCheckpointer`, `_UsesTheCheckpointSchemaVersion`; `internal/contract`: `TestSentinelRoundTrip`, `TestSentinelRenderIsAnInertComment`, `TestSentinelNotObservedGivesTwoChances` | PASS | `I-11.6a.txt`, `I-11.6b.txt` | direct | **Pattern corrected**, see §3.1: the sentinel assertions moved package. Wrapper literal confirmed at `render.go:251` and `render_test.go:27`. |
| I-11.7 | Item 1 invariants never truncated, even at a forced 200-token budget (`Degraded == true`, `Truncated == false`) | RETIRED | `internal/rehydrate`: `TestInvariants_Verbatim`, `_OmitsAnEmptySource`, `_EmptyOmitsSection` (never-truncated clause, via `isFixedUnit`); `TestBuild_Tier1ThatCannotFitIsDroppedWhole`, `TestBuild_ZeroBudgetProducesExplicitOverflow`, `TestBuild_WrapperAloneOverBudgetInjectsNothing` (forced-tiny-budget clause) | PASS | `I-11.7.txt`, `I-11.13.txt` | direct | `Result.Truncated` no longer exists — see §3.3. The *behaviour* the row protects (item 1 is whole or absent, never cut) is asserted and green. |
| I-11.8 | Item 2 verbatim intent from L0, never a summary (G2.3, G7.3): three named tests | MAPPED | `internal/rehydrate`: `TestUserIntent_FromL0NotCheckpoint`, `TestUserIntent_EarliestTurnOfThisSessionWins`, `TestUserIntent_FencedPromptSurvivesVerbatim` — all three named tests still exist — plus 8 siblings (`_AgreeingSourcesReportNothing`, `_ResolvesTheDerivedFirstPromptID`, `_IgnoresOtherSessions`, `_FallsBackWhenNotFound`, `_FallsBackWhenStoreErrors`, `_StripsPriorInjections`, `_CapsAtMaxIntentBytes`, `_EvolutionUnitsFollowTheOriginal`) | PASS | `I-11.8.txt` | direct | V4 read this as SUPERSEDED (unit E `76e0a9e` authority model). On this tree the historical assertion still holds *as written* — verbatim L0 capture is the only raw source; the authority model adds the evolution/agreement units on top, it does not replace L0. Recorded MAPPED, not superseded. |
| I-11.9 | Item 3 eliminations digest + standing instruction; top-N by slice score, both scopes, stale note verbatim, `"Before committing to an approach, call already_tried."` always present | MAPPED | `internal/rehydrate`: `TestEliminations_*` — 21 tests incl. `_TopNFromConfig`, `_OrderedBySliceScore`, `_BothScopesRead`, `_StaleRendersTheVerbatimNote`, `_LedgerErrorFallsBackToCheckpoint`, `_NilLedgerReportsTheAbsence`, `_GetUnknownKeepsCheckpointCopy`; `TestStandingInstruction_IsOneBareLine`, `_IsStable`, `_IsTheVerbatimSentence` | PASS | `I-11.9.txt` | direct | Sentence confirmed byte-for-byte at `internal/rehydrate/standing.go:5`. Unit D (`9d9550d`/`aa073fa`) added the ledger-failure rendering tests; nothing historical was removed. |
| I-11.10 | Items 4/5/6 decisions, current work, pointers-not-contents; no code fence in 4–6; multiline pointer units dropped with a `Loud` | MAPPED | `internal/rehydrate`: `TestDecisions_RenderWhatWhyRejectedEvidence`, `_OmitsEmptyLines`, `_OrderedBySliceScoreThenTurnThenID`, `_GuardRejectsAFencedUnit`; `TestCurrentWork_BlockedOnNil`, `_RendersABlocker`, `_AllEmptyOmitsSection`; `TestPointers_FilesBeforeTools`, `_RankedBySliceScore`, `_GuardRejectsMultilineUnit`, `_WhyIsBoundedToOneLine`, `_OmitsAZeroHash`; `TestGuardNoContents` | PASS | `I-11.10.txt` | direct | `TestGuardNoContents` is the no-fence guard; `TestPointers_GuardRejectsMultilineUnit` is the `Loud`-drop clause. |
| I-11.11 | Items 6a/6b restored instructions; whole-rule-or-nothing; host head-truncation and 25K-cap warnings | MAPPED | `internal/rehydrate`: `TestRestored_PathRulesBeforeNested`, `_DropNamesTheMatchingPointerWhenTheMatcherIsWired`, `_BodyIsWholeOrAbsent`, `_ScanErrorDegrades`, `_NilScanner`, `_RuleWithNoGlobsHasNoDanglingClause`; `TestSkillIndex_RendersTheCompactIndex`, `_DropsReportUnindexedSkills`, `_WarnsOnHostHeadTruncation`, `_WarnsOnHostTotalCap`, `_BodyTokensErrorsAreSilent`, `_NilIndexerReportsTheAbsence` | PASS | `I-11.11.txt` | direct | Both host-warning clauses have a named test (`_WarnsOnHostHeadTruncation`, `_WarnsOnHostTotalCap`). |
| I-11.12 | Item 7 drop report (G4.5): fixed kind order; `… and N more; call dropped()`; `Result.Dropped` complete regardless of rendering | MAPPED | `internal/rehydrate`: `TestDropReport_OrdersPathRulesFirst`, `_SortsByIDWithinAKind`, `_CollapsesSameKindEmptyID`, `_EmptyYieldsNoSection`; `TestMoreDropsUnit_IsThePricedCountedTail` | PASS | `I-11.12.txt` | direct | Tail string confirmed verbatim at `items.go:1147` and asserted at `items_test.go:1382`. Unit H (`0259c68`) added `dropKindOverflow` on top; nothing historical was removed. |
| I-11.13 | 8–12K budget discipline (G3.3): `Tokens <= 12 000` always; smaller than stock 50K+25K; monotone and prefix-wise embedded in budget | RETIRED | `internal/rehydrate`: `TestClampBudget_NeverRaisesAndNeverExceedsTheCap`, `TestBuild_NeverExceedsMaxTokens`, `TestBuild_SmallerThanStock`, `TestBuild_MinFillReadmitsUnits`, `TestBuild_MonotoneInBudget`, `TestBuild_Tier1ThatCannotFitIsDroppedWhole`, `TestBuild_ZeroBudgetProducesExplicitOverflow`, `TestBuild_WrapperAloneOverBudgetInjectsNothing`, `TestBuild_LatestEvolutionSurvivesTruncation`, `TestBuild_SingleOversizedCriticalRecordOverflows`, `TestBuild_OverflowDetectedWithoutACalibratedEstimator` — 11 tests | PASS | `I-11.13.txt` | direct | The cap and monotonicity clauses are green. **Three named tests are gone and one clause has no successor** — see §3.2 and §5 Q2 (carry-forward). |
| I-11.14 | Drop-report persistence (backs MCP `dropped`): corrupt state deleted + one `Loud`; golden state file byte-equal | MAPPED | `internal/rehydrate`: `TestReporter_RecordThenCurrentDrops`, `_NeverRehydratedIsNotAnError`, `_EmptySlicesMarshalAsArrays`, `_CorruptStateIsDeletedAndLoudOnce`, `_ResetDeletesAndIsIdempotent`, `_CancelledContextIsReported`; `TestNewReporter_NilLoggerIsTolerated`; `TestState_MatchesFrozenGolden`, `TestState_GoldenAccountsForEveryToken` | PASS | `I-11.14.txt` | direct | Golden-bytes clause is `TestState_MatchesFrozenGolden`; run without `-update`. |
| I-11.15 | Rehydration goldens and degradation, **without** `-update` | MAPPED | `internal/rehydrate`: `TestBuild_Golden_PayloadsMatchFrozenBytes`, `TestBuild_Golden_PayloadsRoundTripThroughUnwrap`, `TestBuild_NonCompactSourceEmitsNothing`, `TestBuild_ContextCancelled`, `TestBuild_NilDeps` | PASS | `I-11.15.txt` | direct | `-update` was **not** passed; goldens matched the committed bytes as-is. |
| I-11.16 | G7.5 checkpoint is the fallback when the summarizer fails: **zero** reads of `TranscriptPath`; identical payload with/without a `<summary>` block | MAPPED | `internal/rehydrate`: `TestBuild_NoTranscriptRead_ClosesG75`; `test/e2e`: `TestE2E_SessionStartCompactAfterFailedSummary` | PASS | `I-11.16a.txt`, `I-11.16b.txt` | direct | Reinforced structurally: `grep -n TranscriptPath internal/rehydrate/*.go` returns **nothing** — the package has no path by which it could read the transcript. |
| I-11.17 | Daemon `SessionStart(compact\|clear)` service | MAPPED | `internal/daemon`: `TestService_*` — 17 tests incl. `_CompactEmitsAdditionalContext`, `_ClearResetsState`, `_ClearOnMissingStateIsNoOp`, `_ReinjectionKillSwitchEmitsNothing`, `_DegradedPassiveEmitsNothing`, `_ResumedOrForkedSessionInheritsProjectCheckpoint`, `_OutOfOrderCheckpointDeliveryReflectsWhateverIsCurrentlyLatest`, `_PanicRecovered` | PASS | `I-11.17.txt` | direct | **The source row is malformed**: the `(compact\|clear)` pipe split the CSV, so `functionality` is truncated to "Daemon \`SessionStart(compact" and the command/expected cells are shifted one place left. Reconciled against the intended command `go test ./internal/daemon/ -run 'TestService_' -v`. V4 recorded this row RETIRED (native custom-instruction compliance is not assertable); the *daemon-side* half asserted here is fully live. `TestService_StateWriteFailureStillEmits` platform-skips (§5 Q3). |
| I-11.18 | Rehydration e2e: `additionalContext` contains `## 1. Invariants`, `## 7. No longer in context`, `## 8. Retrieval` and the session sentinel; ≤ 12 000 tokens; `clear` resets state | MAPPED | `test/e2e`: `TestE2E_SessionStartCompact`, `_CompactRestoresCheckpointItems`, `_CompactUnderBudget`, `_Clear`, `_CompactNoStore`, `_CompactAfterFailedSummary`, `_Latency` — 7 tests | PASS | `I-11.18.txt` | direct | Filtered `-run 'TestE2E_SessionStart'` only — no unfiltered `./test/e2e` run. Budget clause is `_CompactUnderBudget`; `clear` clause is `_Clear`. |
| I-11.19 | L5 benchmark budgets: `L5-BUILD` p99 < 250 ms; `L5-RULES` < 50 ms; `L5-SKILLS` < 20 ms; `TestE2E_SessionStartLatency` p99 < 1.5 s | MISSING | `internal/rules`: `BenchmarkPathScoped` = **3.68 ms/op** (168 486 B, 2 006 allocs) vs 50 ms; `internal/skills`: `BenchmarkIndex` = **11.98 ms/op** (280 135 B, 1 226 allocs) vs 20 ms; `test/e2e`: `TestE2E_SessionStartLatency` PASS (4.78 s wall for the whole percentile harness). `internal/rehydrate` declares **no benchmark at all** | PASS | `I-11.19.txt`, `I-11.18.txt` | direct | Three of four clauses measured and inside budget **on a co-loaded host** (a pass under co-load is the strong direction). **`L5-BUILD` has no home:** `go test -list Benchmark ./internal/rehydrate/` enumerates nothing. Carries `V4-SP11-22` unchanged. Owner: `internal/rehydrate` must add `BenchmarkBuild` exercising `Build` end-to-end against p99 < 250 ms. No machine-checked budget gate exists for any L5 name — `L5-RULES` appears only as a prose comment at `internal/rules/scanner.go:214`; the numbers above are eyeball comparisons, not an enforced gate. |

---

## 2. What was run

Each row was executed as its own focused invocation with `-count=1`, output redirected to the
artifact file named above (never piped through `grep`/`head`/`tail`). Every `-run` pattern was first
confirmed non-empty with `go test -list <pattern> <pkg>`; the patterns that selected nothing are
corrected in §3.1. No `./...`, no unfiltered `./test/e2e` or `./test/integration`, no race run, no
fuzz run, no `devtool replay --ci` / `bench-hotpath` / `test` / `test-race`, no `golangci-lint`.

The only benchmark invocations were `-run '^$' -bench . -benchtime 2s` against one package at a time;
the longest was `internal/rules` at 33 s wall.

No FAIL, no FAIL-BASELINE and no FAIL-COLOAD-SUSPECT was produced. None of the five known
pre-existing failures on this tree lies in an SP-11 row's blast radius
(`test/guards`, `test/e2e:TestV3_HotPathUnchangedWithLedgerResident`, `test/integration` ×2,
`internal/negknow:TestBudget_Open`), so no baseline classification was needed.

---

## 3. Old-to-new assertion map

### 3.1 Corrected `-run` patterns (selected nothing on this tree)

| Row | Historical pattern element | Selects | Replacement | Why |
|---|---|---|---|---|
| I-11.5 | `TestBuild_RankIsOneBasedAmongEmitted` | nothing | `internal/rehydrate`: `TestBuild_RankCountsEmittedItemsFromZero` | **Semantics changed, not just the name.** `Item.Rank` is now the *0-based* position among emitted items ("Rank is the 0-based position among EMITTED items, so an omitted kind consumes none"), introduced by `df9e32b`. Any downstream consumer that read rank as 1-based is off by one. V4 (`rc-v4-sp11-06`) could not find a successor and left the clause `SPLIT-REVIEW`; on this tree the successor exists and is green, so the split is resolved — but the off-by-one is a real contract change and is flagged here rather than silently absorbed. |
| I-11.5 | `TestRenderOrder_` | `TestRenderOrder_IsIotaOrder` | same (stem still matches) | `TestRenderOrder_EightNormativeKindsInIotaOrder` was renamed to `TestRenderOrder_IsIotaOrder`; the prefix pattern still selects it, so the run itself needed no correction. |
| I-11.6 | `TestSentinel_`, `TestBuild_SentinelIsLastLineInsideTags` | nothing | `internal/contract`: `TestSentinelRoundTrip`, `TestSentinelRenderIsAnInertComment`, `TestSentinelNotObservedGivesTwoChances` | The sentinel moved out of `internal/rehydrate` into `internal/contract` (it is a cross-component contract assertion, not a rehydrator detail). The *placement* clause — "the sentinel is the last line **inside** the injection tags" — has **no successor in any package**; `render_test.go:27,31` assert only that the opening tag is the payload prefix and precedes the closing tag. Recorded as a gap, not a pass. See §5 Q4. |
| I-11.13 | `TestBuild_PrefixTruncationNotCheapestFirst`, `TestBuild_CarryForward`, `PropBuild_` | nothing | `TestBuild_MonotoneInBudget`, `TestBuild_Tier1ThatCannotFitIsDroppedWhole`, `TestBuild_ZeroBudgetProducesExplicitOverflow`, `TestBuild_SingleOversizedCriticalRecordOverflows` | See §3.2. The `-rapid.checks=500` flag in the historical command was also dropped: `internal/rehydrate` no longer registers any rapid property, so the flag would abort the run. |

### 3.2 RETIRED — I-11.13, prefix truncation replaced by whole-or-nothing overflow

**Reason.** V4 corrective unit H (`0259c68`, on top of `df9e32b`) replaced the *truncated-flag* budget
model with an *explicit-overflow* model: content that cannot fit is no longer cut to a cheapest-first
prefix and flagged, it is dropped whole and named in the drop report with a recovery path
(`budget.go:270` — "whole or not at all — call dropped() for the full accounting"). The historical
phrase "prefix-wise embedded in budget" therefore no longer describes the algorithm.

**Replacement.** The surviving invariants — the hard cap (`TestBuild_NeverExceedsMaxTokens`,
`TestClampBudget_NeverRaisesAndNeverExceedsTheCap`), smaller-than-stock
(`TestBuild_SmallerThanStock`), monotone-in-budget (`TestBuild_MonotoneInBudget`) and min-fill
re-admission (`TestBuild_MinFillReadmitsUnits`) — are all present and green, joined by the new
overflow assertions. The `Tokens <= 12 000` clause is fully intact.

**Residual gap.** The **carry-forward** clause has no successor of any name.
`grep -rin 'carry.forward|carryForward' internal/rehydrate/ internal/checkpoint/` finds only an
unrelated comment in `internal/checkpoint/migrate.go:13`. V4 flagged this in `rc-v4-sp11-14` as
needing an independently reviewed split; **it is still open on this tree** — see §5 Q2.

### 3.3 RETIRED — I-11.7, `Result.Truncated` no longer exists

**Reason.** The same overflow rework moved `Truncated` from `Result` to `Item`
(`internal/rehydrate/types.go:65-66` — "Truncated reports whether **this Item** was cut to fit the
budget"); `Result` retains `Degraded` (`types.go:113`) but has no `Truncated` field. The historical
assertion `Degraded == true && Truncated == false` is therefore not expressible against `Result`.

**Replacement.** The behaviour it protected is asserted three ways and is green:
`TestInvariants_Verbatim` requires `isFixedUnit(u)` for every invariant unit ("tier-1 units carry the
zero DropEntry: they are never truncated"); `TestBuild_Tier1ThatCannotFitIsDroppedWhole` covers the
forced-tiny-budget case the historical 200-token budget stood for; and
`TestBuild_WrapperAloneOverBudgetInjectsNothing` covers the degenerate end.

### 3.4 Scope drift noted, not retired — I-11.3, "never ancestors"

The historical expectation reads "containing directory only, **never ancestors**, never the project
root". The current package has `TestNestedClaudeMD_WalksAncestors`, `_AncestorTwoLevelsUp` and
`_NestedFlag`: ancestor discovery **is** implemented, behind an explicit flag, with
`_AncestorWalkStopsBeforeRoot` and `_ExcludesProjectRoot` preserving the root exclusion. The
absolute "never ancestors" reading is stale; the load-bearing half (the project root is never
restored as a nested rule) holds and is green. Recorded MAPPED with this note rather than RETIRED,
because the row's own headline — "containing directory only … never the project root" — still passes.

---

## 4. Deferred to coordinator

**No row is NOT-RUN and no row is FAIL-COLOAD-SUSPECT.** Every one of the 19 rows was executed on
this tree and passed. Two items nonetheless want a quiet-machine or coordinator pass:

| Item | Exact command | Why |
|---|---|---|
| I-11.19 re-measure | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test -count=1 ./internal/rules/ -run '^$' -bench . -benchtime 2s` and `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test -count=1 ./internal/skills/ -run '^$' -bench . -benchtime 2s` | Both were measured under co-load and both passed (3.68 ms vs 50 ms; 11.98 ms vs 20 ms). `L5-SKILLS` at 60 % of its budget is the thinner margin; a quiet re-run would firm the number. Not a failure. |
| SP-11 slice under race, ×2 (V4 row `V4-SP11-01`; outside my 19 rows, recorded for completeness) | `cd "C:/Users/Quant/Documents/Programming/Projects/qompack-v5" && go test -race -count=2 -timeout=30m ./internal/rehydrate/... ./internal/rules/... ./internal/skills/...` | Race runs are barred to me by the hard rules. The three packages are green single-threaded; the race+repeat gate has not been exercised on this tree. |

---

## 5. Questions

**Q1 — the owner plan no longer describes the shipped slice.** `plans/V4-SP-11-rehydrator-l5.md` at
HEAD is a 176-line forward-looking planning document. It states "No tests run in this planning pass",
every one of its T11-AUTH-01 … T11-ROLLBACK-01 gates is an unchecked *future* assertion "on named
future entrypoints", and stage IS-11-02 says the build/items/render path must be "adapt[ed] using
SP-20 state and SP-10 usable checkpoint". Meanwhile the code it plans for is fully shipped and green
(19/19 rows). The reconciliation target handed to me — "T11-AUTH/BUDGET/LIFE/LOAD/SCOPE/POINTER/
CORRECT/ROLLBACK; current intent and bounded extra context" — therefore names a gate set that has
**no test coverage anywhere**, because it was never implemented. I did not mark 19 rows MISSING
against it: the historical rows and the T11 gates are two different contracts, and the shipped code
satisfies the historical one. **Coordinator decision needed:** is the T11 gate set (a) live V5 scope
that must be built, which would make this slice largely uncovered; (b) deferred to a later
checkpoint; or (c) a planning artefact that should be reconciled away? Nothing in this inventory can
settle it.

**Q2 — carry-forward has no assertion and no owner.** The historical I-11.13 clause "monotone and
prefix-wise embedded in budget" included a carry-forward behaviour asserted by
`TestBuild_CarryForward`, deleted by the overflow rework. V4 (`rc-v4-sp11-14`) recorded that it
"has no successor and needs an independently reviewed split"; that review has not happened, and no
carry-forward symbol exists in `internal/rehydrate` today. Either the behaviour was intentionally
dropped with the truncated-flag model — in which case the clause should be retired in writing — or it
survived and is untested. Owner if it must be rebuilt: `internal/rehydrate`.

**Q3 — two Windows platform skips inside otherwise-passing rows.** Both are environmental, not
defects, but they mean two behaviours are unverified on this platform:

- I-11.4, `internal/skills`: `TestIndex_SkipsSymlinks` — *"platform: this process may not create
  symlinks: … A required privilege is not held by the client."* The symlink-skipping behaviour of the
  skill indexer has not been exercised here.
- I-11.17, `internal/daemon`: `TestService_StateWriteFailureStillEmits` — *"platform: a directory
  mode bit does not deny file creation on Windows."* The state-write-failure degradation path has not
  been exercised here.

Both would run on the Linux CI leg. Flagging so the coordinator does not read "19 PASS" as "every
behaviour exercised on this host".

**Q4 — the sentinel placement assertion is gone (I-11.6).** "The sentinel is the last line inside the
injection tags" has no test in `internal/rehydrate` or `internal/contract`. V4 already noted the
§4.8 cross-component form as MISSING. The wrapper round-trip — which is what the row's *expected*
column actually demands — is green, so I did not mark the row MISSING; but if the sentinel's position
inside the tags is contractual (the `hook.additional_context_delivered` monitor reads the transcript
tail for it), it is currently unpinned.

**Q5 — no L5 budget is machine-enforced.** `L5-BUILD`, `L5-RULES`, `L5-SKILLS` and the
`SessionStart` p99 exist as prose in the plan and, for `L5-RULES`, as one comment at
`internal/rules/scanner.go:214`. There is no gate in `tools/devtool` that fails a build when a budget
is breached, and `internal/rehydrate` has no benchmark at all. The numbers in row I-11.19 are my
comparisons against the plan text, not an enforced check.

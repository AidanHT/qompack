# Wave 14 — w14-decisions (D46 follow-ups)

Branch `closeout/w14-decisions`. Workflow `wf_e9768966-e7a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `9e297d8515e217fb0d13179b9a11a0e19af5dc30`

### Root cause

(1) Checkpoint decisions came from two paths, and both ranked other sessions' decisions by those sessions' turn numbers. The first path is the extractor's per-pass rank: extractDecisions ranks by slice score, then Turn descending, then caps at 64. The second is Draft.mergeDecisionsLocked, which sorts by Turn descending and caps at 64 again. A decision minted from another session's project-scoped elimination (source b) takes that elimination's DAG node turn, which is in the other session's numbering. For this session's first segment (from=0), every such record in the project passes the from-turn cut. So 64 foreign records at high foreign turns fill the cap, and this session's own eliminations and decision pins are pushed out of the checkpoint. On a8c74335 the new rows show the head of cp.Decisions as "foreign approach 00/01", the foreign records at turns 569 and 568, instead of the session's own decisions. The from-turn cut itself also compared foreign turns against this session's segment start. As a result, a foreign record at a low foreign turn, recorded during this session, was never minted by a later Advance.
(2) Draft.refreshNegativeKnowledge (wave 13) builds Decision values and merges them. It never called the extractor's emit, so no dag.KindDecision node and no explains edge from the elimination node existed for a seal-time decision. Slice scoring (the rehydrate buildDecisions scores, and the extractor's own rank) had nothing to score.

### Summary

## w14-decisions: decision ranking (D46) and seal-time DAG nodes

Both D46 follow-ups are fixed on closeout/w14-decisions (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w14-decisions, cut from a8c74335), and the SP-09 plan note is added.
- All three new rows were RED on a8c74335 and pass on the new tree.
- Reverting each of the five fix hunks one at a time turns at least one row red every time.
- No budget or bound numbers were added, no assertion or threshold was changed, and no t.Skip or nolint was added.

### What changed

**(1) Own decisions rank first; foreign ones fill the rest by recorded time** (c0f39781: internal/checkpoint/decisions.go, writer.go, plus comments in draft.go and truncate.go)
- **Marking foreign candidates.** `decisionCandidate` gains `foreign bool` and `recorded core.UnixMilli`. When a session runs an extraction, `fromEliminations` marks another session's project-scoped record as foreign and stores the record's TS.
- **Extractor order.** `rank` puts every own candidate first, in the unchanged §9 order (score, then Turn descending, then ID). Foreign candidates follow, newest recorded first, then by ID. The cap is applied after this sort.
- **Draft order.** `Draft.mergeDecisionsLocked` keeps the same order across passes: own decisions by Turn descending, then foreign ones by recorded time descending, capped at 64. Truncate cuts from the tail, so it now drops foreign decisions before any own one.
- **Where foreignness comes from.** The artifact's Decision shape is frozen and must not change, so the new `Draft.foreignDecisionsLocked` works it out from the draft's carried eliminations plus the current pass's candidates. A decision ID is minted from what, why and evidence, not the turn. An ID that one of this session's own records also mints counts as own. A resumed draft therefore ranks exactly as the draft that persisted it did.
- **Decision under D33: foreign records skip the from-turn cut.** That cut compared foreign turns with this session's segment start, which D46's "not by foreign turn numbers" rules out. Duplicates are still removed by ID in the draft, and the extra work is O(records) on the idle path.
- `ExtractDecisions` (public, no session) behaves as before. It now wraps the internal function, which returns candidates.

**(2) Seal-time decisions emit their DAG node** (8e81e8ec)
- The extractor's emit became `emitDecisions(src, cands)`.
- `refreshNegativeKnowledge` builds candidates with `evidence: dag.EliminationNode(r.ID)`, merges them, and emits the node and explains edge for the ones the draft kept. This matches the extractor, which emits only its capped set.
- The emitted values (node turn from `eliminationTurn`, Root = evidence, token estimate) are the ones a later Advance computes for the same record. That later emission is a no-op: the graph keeps one node and one edge, unchanged. The test asserts this.

**Plan note** (c023f9af)
- A dated note in plans/V3-SP-09-negative-knowledge.md, directly under the Query pseudocode, cites wave 13 / 1d289874 and w13-ledger reviewer finding 3.
- It says: a filter hit with no visible candidate is a plain AnswerAbsent (counted under absent) when a record in the ledger's own view backs it. It is bloom_only only when no record in view backs it.
- It names the tests that pin both behaviours: TestLedger_OtherSessionsRecordIsAPlainAbsence, TestQuery_BloomOnly and TestQuery_ScopeSession_OtherSessionHidden_NextIdle. The pseudocode itself is not rewritten.

### New tests (internal/checkpoint/decisions_foreign_test.go)
- **TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations**
  - Setup: 70 foreign project-scoped eliminations. Record i is recorded later than record i-1 but sits at foreign turn 569-i, so ranking by turn and ranking by recorded time disagree completely.
  - First pass (from turn 0): the session's own elimination and a decision pin come first, and the 62 newest-recorded foreign decisions follow in order.
  - Second pass: adds another own decision, plus a new foreign record at foreign turn 2, below this segment's start and recorded last. All 3 own decisions come first, and the late foreign decision is the first foreign one.
- **TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes:** the sealed checkpoint (budget 1<<20, so the cap decides what is kept) holds the own decision the seal-time refresh added and the own decision from Advance, then foreign ones by recorded time.
- **TestPreCompactSealTimeDecisionEmitsItsDAGNode**
  - The seal-time decision has exactly one KindDecision node (turn 12, Root = evidence, Tokens > 0) and one explains edge from its elimination node.
  - The successor draft's Advance over the segment holding turn 12 extracts the same decision, and afterwards the node and edge are identical to before.

### Reverting each fix hunk (runs/02)
| Hunk reverted | Rows that go red |
|---|---|
| M1: extractor's own-first rank | TestAdvance…, TestPreCompactKeeps… |
| M2: draft merge back to Turn-descending | TestAdvance…, TestPreCompactKeeps… |
| M3: from-turn cut restored for foreign records | TestAdvance… |
| M4: no emission in refresh | TestPreCompactSealTime… |
| M5: foreign map ignores carried eliminations | TestAdvance…, TestPreCompactKeeps… |

M5 shows the derivation is load-bearing: a pass's capped candidates do not name every foreign decision the draft already holds.

### Commands and results (Windows 11, -p 2, shared machine; no wall-clock failures seen)
- **RED on base.** `go test -p 2 -count=1 -run '^(TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations|TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes|TestPreCompactSealTimeDecisionEmitsItsDAGNode)$' -v ./internal/checkpoint/` with the four product files at a8c74335: exit 1, 3 FAIL (runs/01-red-windows.log).
- **RED per reverted hunk.** Same command, run by a temporary diagnostic script (scratch only, not committed) once per reverted hunk: exit 1 each time, table above (runs/02-red-by-reverting-each-fix-windows.log).
- **GREEN.** Same command on the final tree: 3 PASS.
- **Checkpoint package.** `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint/...`: ok for both internal/checkpoint (99.5 s) and checkpointtest (runs/03).
- **CLI live rows.** `go test -p 2 -count=1 -timeout=30m -run '^TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)$' -v ./internal/cli/`: 7 PASS (runs/04).
- **Two focused e2e rows.** `go test -p 2 -count=1 -timeout=30m -run '^(TestV4_WhyAndDroppedAnswerFromRealProducers|TestE2E_EliminationLifecycle)$' -v ./test/e2e/`: 2 PASS (runs/05).
- **Docs.** `go test -p 2 -count=1 ./test/docs`: ok (runs/06).
- **Format and vet.** `go run ./tools/devtool fmt` made no changes and `fmt-check` exits 0. `go vet ./internal/checkpoint/` and `GOOS=linux go vet ./internal/checkpoint/` pass, and commit c0f39781 vets on its own.
- **Lint.** `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0. Re-running with only runpatterns and docmarkers after committing the logs: both PASS.
- Note for the report: runpatterns splits `-run` alternations at `|`, and earlier reports allowlist that. Each name inside the alternations above is a real test.

### Criterion changes
None. No existing assertion, golden, threshold or timeout was touched. The serialized decision order is no longer purely Turn-descending: own decisions come first, then foreign ones. D46 authorizes that, and the comments on Truncate, maxDraftDecisions, mergeDecisionsLocked and refreshNegativeKnowledge now say so. No plan text states the old order, so no plan edit was needed.

### Not run (seat limits)
- Whole test/e2e and test/integration, the hot-path rows, and -race.
- Linux: the container is stopped.

### Commits

- c0f39781 fix(checkpoint): rank own decisions before other sessions' ones
- 8e81e8ec fix(checkpoint): emit the DAG node for seal-time decisions
- c023f9af docs(plans): note SP-09 Query's record-backed absence
- 9e297d85 docs(v6): add w14-decisions evidence logs

### Tests

- `go test -p 2 -count=1 -run '^(TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations|TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes|TestPreCompactSealTimeDecisionEmitsItsDAGNode)$' -v ./internal/checkpoint/ (product files at a8c74335)` — RED as required: exit 1, 3 FAIL (runs/01-red-windows.log)
- `same rows, run by a temporary diagnostic script (scratch only) with each of 5 fix hunks reverted in turn` — exit 1 every time; each hunk turns at least one row red (runs/02-red-by-reverting-each-fix-windows.log)
- `go test -p 2 -count=1 -run '^(TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations|TestPreCompactKeepsOwnDecisionsAheadOfForeignOnes|TestPreCompactSealTimeDecisionEmitsItsDAGNode)$' -v ./internal/checkpoint/` — PASS 3/3
- `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint/...` — ok (checkpoint 99.5s, checkpointtest 1.3s) (runs/03)
- `go test -p 2 -count=1 -timeout=30m -run '^TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)$' -v ./internal/cli/` — 7 PASS (runs/04)
- `go test -p 2 -count=1 -timeout=30m -run '^(TestV4_WhyAndDroppedAnswerFromRealProducers|TestE2E_EliminationLifecycle)$' -v ./test/e2e/` — 2 PASS (runs/05)
- `go test -p 2 -count=1 ./test/docs` — ok (runs/06)
- `go vet ./internal/checkpoint/ && GOOS=linux go vet ./internal/checkpoint/` — clean (also clean at c0f39781 alone)
- `go run ./tools/devtool fmt / fmt-check` — no changes / exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0; runpatterns and docmarkers PASS again after the logs were committed

### Criterion changes

- None. No assertion, golden, threshold, budget or timeout was changed, and no t.Skip or nolint was added. Behaviour change authorized by D46: the draft's serialized decision order is now this session's own decisions (Turn descending, then ID) followed by other sessions' project-scoped-elimination decisions (recorded time descending, then ID), instead of one Turn-descending list. The comments on Truncate, maxDraftDecisions, mergeDecisionsLocked and refreshNegativeKnowledge were updated. No plan text states the old order.

### Open issues

- Same defect, different package, outside this seat's scope: internal/rehydrate/items.go buildDecisions re-sorts a checkpoint's decisions by slice score, then Turn descending. When scores tie (often 0), a foreign decision's Turn, which is in the other session's numbering, can still put it ahead of own decisions in the injected rehydration under its token budget. D46's rule would need the same own-first / recorded-time order there, e.g. derived from r.Checkpoint.Eliminated and the session. Not changed here.
- refreshNegativeKnowledge still mints only this session's own decisions at the seal, as wave 13 designed. If a checkpoint seals before any segment closes, it carries foreign project-scoped eliminations in eliminated[] but no foreign decisions for them. Now that foreign decisions cannot displace own ones, adding them at the seal would be harmless, but D46 did not ask for it.
- Cost: mergeDecisionsLocked now re-mints one decision ID per carried elimination on every call (once per encoded segment, and once at the seal). This is O(records) SHA work on the idle path and on PreCompact. No benchmark covers it; the whole checkpoint package ran in 99.5 s with no budget row failing.
- Advance now considers every carried foreign project-scoped record on every pass (the from-turn cut was dropped for foreign records only), and emits DAG nodes for the foreign decisions that make the capped set. Those nodes carry foreign node turns, as the from=0 pass already did. Re-emission is a fixed point.
- Not run under this seat's limits: whole test/e2e and test/integration, the hot-path rows, -race, and the Linux gate (container stopped). The coordinator should run them on the integrated candidate.

## Independent review

### review:decisions: needs-fixes

- **minor** `internal/rehydrate/items.go:932-941 (consumer of the order set in internal/checkpoint/writer.go:1082-1115)` — D46 is enforced in the checkpoint artifact but not where the checkpoint is used. buildDecisions re-sorts cp.Decisions by slice score, then Turn descending. When scores tie (often at 0), a foreign decision's Turn, which uses the other session's numbering (e.g. 569), sorts ahead of the session's own decisions (e.g. turn 3). Under the rehydration token budget, the tail that gets dropped can therefore be the session's own decisions. The implementer disclosed this as open issue 1, and it is outside this seat's scope (checkpoint/dag/negknow), so it is not a defect in this branch. But the task's user-facing goal ('own decisions always rank before foreign ones') is only met for the sealed artifact, not for what gets injected.
  - Evidence: items.go:932-941: `sort.SliceStable(decs, ... si > sj ... decs[i].Turn > decs[j].Turn ...)`. With TestAdvanceOwnDecisionsRankBeforeOtherSessionsProjectEliminations's fixture (own turns 3 and 8, foreign turns 500-569), an equal-score comparison puts every foreign decision first. mcp/handlers.go:878 iterates all decisions and is not affected.
  - Fix: Route to the coordinator as a follow-up in internal/rehydrate. Keep the checkpoint's order as the primary key: sort own before foreign, with foreignness taken from r.Checkpoint.Eliminated and the session, the same way Draft.foreignDecisionsLocked derives it. Sort foreign decisions by recorded time. Add a rehydrate row with 64 foreign decisions and a tight budget, and assert the own decisions survive.
- **nit** `internal/checkpoint/decisions.go:469-484 (mergeByID) with decisions.go:367-374 and rank at :497-506` — When an own record and a foreign project-scoped record mint the same DecisionID (same target, approach, reason and evidence), mergeByID keeps whichever candidate has the lower Turn. That comparison uses two numberings: the foreign candidate's turn is in the other session's numbering. If the foreign candidate wins, the extractor's rank puts a genuinely own decision in the foreign tier. With more than 64 own candidates in one pass, it can then be capped out and left un-emitted. Draft.foreignDecisionsLocked later classifies the same ID as own, so the pass order and the draft order disagree for that ID.
  - Evidence: mergeByID: `if c.d.Turn < merged[i].d.Turn { merged[i] = c }` does not look at c.foreign. rank sorts `!cands[i].foreign` first. foreignDecisionsLocked (writer.go:584-586) deletes any ID that an own record also mints.
  - Fix: In mergeByID, when the two candidates' foreign flags differ, keep the own (non-foreign) one. Otherwise keep the lower-Turn rule. Optionally add a unit row where an own and a foreign record share what/why/evidence.

## Fix seat (review resolution) — status `done`, head `35f87ffc`

### Root cause

rehydrate buildDecisions ranked all checkpoint decisions by slice score, then Turn; decisions from other sessions' project-scoped eliminations carry that session's turn numbers, so on a score tie they sorted ahead of the session's own decisions, and fillPrefix's tail-first budget cut dropped the own decisions from the injected payload, undoing the checkpoint's D46 order.

### Summary

Fix seat for w14-decisions (branch closeout/w14-decisions, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w14-decisions, head 35f87ffc on top of the implementer's 9e297d85, base a8c7433).

## Review resolution
- Finding 1 (minor): D46 held in the sealed checkpoint but not in the rehydration payload that uses it. I confirmed it and fixed it, test first.
  - Evidence: internal/rehydrate/items.go buildDecisions re-sorted cp.Decisions by slice score, then Turn descending, then ID. A foreign decision's Turn is the other session's turn number, so on a score tie every foreign decision sorted ahead of the own ones. fillPrefix (budget.go) admits units in builder order and stops at the first one that does not fit, so the budget cut the session's own decisions.
  - RED on 9e297d85 with only the new test file present: in TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes the payload showed foreign decisions at turns 569, 568 and 567, and the own decision was reported under "decision ... did not fit the rehydration budget". TestDecisions_OwnRankBeforeOtherSessionsDecisions failed with "Not equal". Log: plans/sdd/V6-closeout/w14-decisions/runs/07-red-rehydrate-windows.log.
  - Scope: this fix goes outside the implementer's scope (checkpoint, dag, negknow) into internal/rehydrate. I made it because D46's goal is the order that actually gets injected, and none of the other seats touch internal/rehydrate (safeCut is internal/mcp; observer is the daemon/scheduler). The reviewer suggested routing it to a follow-up instead; the coordinator may still choose to handle it that way.

## What changed (e6ef8e6d)
- internal/checkpoint/writer.go:
  - The draft's foreign-decision derivation now lives in a pure `foreignDecisions(eliminated, session, cands)`. `Draft.foreignDecisionsLocked` calls it, so the draft's behaviour is unchanged.
  - New exported `checkpoint.ForeignDecisions(cp Checkpoint) map[core.DecisionID]core.UnixMilli` works from cp.Eliminated and cp.Session. It is safe to rely on: Eliminated is tier 1 (never truncated, uncapped) and carries every project-scoped record that could mint a foreign decision, so the reader classifies decisions exactly as the writer did.
- internal/rehydrate/items.go buildDecisions: the sort's primary key is now own before foreign. Own decisions keep the old order (slice score, Turn descending, ID). Foreign decisions follow by recorded time descending, then ID, matching the checkpoint's rank and D46's wording.
  - When the checkpoint has no foreign records, the map is empty and the order is identical to before. That is why no golden changed.
- New file internal/rehydrate/decisions_foreign_test.go. The fixture has 64 foreign project-scoped decisions whose turn order is the reverse of their recorded-time order, stored ahead of the own ones on purpose. It also has 2 own eliminations and 1 own decision pin.
  - TestDecisions_OwnRankBeforeOtherSessionsDecisions checks the exact order, including a foreign decision with slice score 0.9 staying behind every own one.
  - TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes checks through Build: every own decision is injected, the budget really cuts (at least one decision dropped), and the dropped ones are exactly the oldest-recorded foreign ones.

## Commands and results (Windows, -p 2, machine shared with other seats)
- `go test -p 2 -count=1 -run TestDecisions_OwnRankBeforeOtherSessionsDecisions ./internal/rehydrate/` and `go test -p 2 -count=1 -run TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes ./internal/rehydrate/` (run together as one alternation): exit 1 before the fix, PASS after.
- `go test -p 2 -count=1 -run TestDecisions_ -v ./internal/rehydrate/`: PASS, 6 rows including the 4 existing item-4 rows.
- `go test -p 2 -count=1 ./internal/rehydrate/ ./internal/checkpoint/`: ok (1.07 s and 54.4 s). Log: runs/08-green-rehydrate-checkpoint-windows.log.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/rehydrate/ ./internal/checkpoint/`: exit 0 on Windows and with GOOS=linux.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.
- Not run, per this seat's limits: Linux, -race, hot-path rows, and the whole test/integration and test/e2e packages. The other consumer, mcp/handlers.go:878, iterates all decisions without re-sorting and is unaffected.

## Criterion changes
None. The existing assertions in TestDecisions_OrderedBySliceScoreThenTurnThenID are unchanged and still pass, because that fixture has no foreign records.

## Open issues
1. rehydrate sliceCriteria (items.go:882) still seeds the relevance slice with every cp.Decisions node, foreign ones included. This could let other sessions' project-scoped decisions pull item 3's elimination scores toward other sessions' work. It predates this wave and I did not change it; it should go to the coordinator as a possible D46-adjacent follow-up.
2. `ForeignDecisions` rebuilds each decision's id from the eliminations to classify it. A foreign decision whose record has left cp.Eliminated would be treated as own. Today's writer cannot produce that, because Eliminated is uncapped tier 1 and merge-only.
3. The implementer's open issue 1 (this finding) is now closed.

## Owner decisions
No new budget or bound numbers were added.

### Commits

- c0f39781 fix(checkpoint): rank own decisions before other sessions' ones (implementer)
- 8e81e8ec fix(checkpoint): emit the DAG node for seal-time decisions (implementer)
- c023f9af docs(plans): note SP-09 Query's record-backed absence (implementer)
- 9e297d85 docs(v6): add w14-decisions evidence logs (implementer)
- e6ef8e6d fix(rehydrate): rank own decisions before other sessions' ones (fix seat)
- 35f87ffc docs(v6): add w14-decisions fix-seat evidence logs (fix seat)

### Tests

- `go test -p 2 -count=1 -run 'TestDecisions_OwnRankBeforeOtherSessionsDecisions|TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes' ./internal/rehydrate/ (on 9e297d85, before the fix)` — exit 1: both rows FAIL, own decisions dropped from the payload (runs/07-red-rehydrate-windows.log)
- `go test -p 2 -count=1 -run TestDecisions_ -v ./internal/rehydrate/ (and TestBuild_OwnDecisionsSurviveTheBudgetOverForeignOnes)` — PASS, 7 rows
- `go test -p 2 -count=1 ./internal/rehydrate/ ./internal/checkpoint/` — ok rehydrate 1.07s, ok checkpoint 54.4s (runs/08-green-rehydrate-checkpoint-windows.log)
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/rehydrate/ ./internal/checkpoint/ (Windows and GOOS=linux)` — exit 0 both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Open issues

- rehydrate sliceCriteria (internal/rehydrate/items.go:882) still seeds the relevance slice with every cp.Decisions node, foreign ones included, which may bias item 3's elimination scores toward other sessions' work; it predates this wave and is a possible D46-adjacent follow-up
- checkpoint.ForeignDecisions classifies a decision as foreign only when its record is in cp.Eliminated; that holds today because Eliminated is uncapped tier 1 and merge-only, and would break if Eliminated were ever capped
- This fix goes outside the implementer's scope (checkpoint/dag/negknow) into internal/rehydrate; the reviewer suggested routing it to a follow-up, so the coordinator should confirm the scope extension

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



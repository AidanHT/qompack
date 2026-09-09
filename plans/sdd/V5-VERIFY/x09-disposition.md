# V5-VERIFY §4.9 disposition — `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly`

| Field | Value |
|---|---|
| Retained identifier | `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly` |
| Current criterion (plan §4, authoritative) | SP-11/SP-15/SP-16 complete-record overflow replaces arbitrary byte truncation. |
| Disposition | **authored** |
| Level | `test/integration` (in-process, real components). The historical seam was a direct `checkpoint.Truncate` call; the three producers on this tree — `checkpoint.Truncate`, `checkpoint.Promote`/`Apply`, and `analyzer.Propose` → `rehydrate.Build` — are all pure or in-process functions, and `Promote` has no daemon wiring at all (no production caller; `runtime.phase7.retrieval.demandPromotion` defaults to false), so an e2e seam would have added a process boundary the criterion does not cross. |
| File | `test/integration/v5_x09_test.go` |
| Base | `verify/v5` @ `87c0c1d`, branch `v5/x09` |
| Production changes | none |

## Producers established on this tree

| Owner | Producer | Where |
|---|---|---|
| SP-10/SP-11 | `checkpoint.Truncate(c, budget, tiers, est)` — the §6.9 cut; `FileWriter.Finalize` folds its drops into the sealed artifact | `internal/checkpoint/truncate.go`, `finalize.go:63-72` |
| SP-16 | `checkpoint.Promote(PromotionRequest) PromotionPlan`, `PromotionPlan.Apply(c)` — reorders `Pointers.Tools` so promoted hashes lead; `promotion_overflow` drops when the overhead budget refuses | `internal/checkpoint/promote.go` |
| SP-15 | `analyzer.Propose(ctx, p, cands, lambda, budget) (Proposal, error)` — `Overflow` with `Chosen == nil`, `Item`, `Archive`, `Reason` when a mandatory item fits at no representation | `internal/analyzer/greedy.go:310-360, 898-915` |
| SP-11 (consumer) | `rehydrate.Build` with `Request.Selection *SelectionOutcome`; `applySelection` files one `overflow` drop naming `sel.Item`; `rehydrate.Overflowed(drops)` recognises it | `internal/rehydrate/selection.go`, `budget.go:274-291` |
| composition root | `daemon.rehydrateService.selectionFor` translates `Proposal` → `SelectionOutcome` and passes the SAME budget to the selector and to `Build` | `internal/daemon/rehydrate_selection.go`, `rehydrate_service.go:153` |

Not on this tree: any "action history" block in the checkpoint (`grep -rn ActionHistory` finds nothing), any "tier reserve" curve or `TestTierReserveMatchesCurve`. Those historical producers are retired below.

## Test names and what each asserts

One top-level test, `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly`, in five labelled arms (no subtests, so the identifier selects the whole row):

1. **SP-16 promotion (real demand).** Six tool pointers whose hashes are real `store.PutBytes` roots with real `RecordToolUse` records; the OLDEST pointer's key is fed three `requested` + three `useful` observations through the production `store.DemandLog`, read back via `Aggregate()` (so `Instrumented()` and a known `Usefulness()` are real). `Promote` with `Enabled=true`, `TargetSeq > ObservedSeq`, `Authority=tool_observation` admits exactly it; `Apply` moves it to index 0, keeps every other pointer in the writer's order, and does not mutate its input.
2. **SP-11 cut ladder at EVERY budget from the full size (2884 tokens) down to 1.** At each budget: surviving tier-2/3 slices are byte-identical PREFIXES of the input (no record is ever shortened; `alternatives_rejected` is emptied wholesale or kept whole); the drop report names exactly the removed records once each, by the id the retrieval tools resolve (`tool_pointer`/tool_use_id, `file_pointer`/path, `open_question`/`oq_N`, `alternatives`+`decision`/decision id, `narrative`, `next_step`); the §10 order holds (narrative before any pointer; tools exhausted before files; no tier-2 record while tier 3 stands; open questions before alternatives; alternatives before any decision; next_step last); the outcome fits the budget unless a single `budget_exceeded`/`tier1` entry is present, in which case every cuttable field is empty and tier 1 alone exceeds the budget; consecutive budgets are monotone (`kept(b) ⊆ kept(b+1)` as prefixes); determinism is checked byte-for-byte at sampled budgets. The promoted pointer is the LAST tool pointer to go (gone at budget 2494), some budget keeps it while cutting an unpromoted peer, and at the budget it goes every decision and open question is still intact — promotion never lifts tier 3 into tier 2.
3. **Budget 1 / tier 1.** `Truncate` at 1 returns tier 1 byte-identical to the input (JSON of `invariants`, `user_intent`, `eliminated`) with exactly one `budget_exceeded` entry; the real `FileWriter.Begin`/`Finalize(…, 1)` still seals an artifact carrying that entry, with all three ledger eliminations written whole (reasons unchanged), and `Reader.Verify` reports nothing.
4. **SP-15 → SP-11 overflow.** Candidates are built the way the daemon builds them (rendered-line + pointer representation per active record, `Mandatory=true`), the SP-12 gate opened as analyzer's own tests open it (`scheduler.EnablePSelection` with cleanup). At the full rehydration budget `Propose` carries all three and `Build` renders each record whole (`- target — "approach" — reason [active]`), `Overflowed` false. At `cheapest pointer cost − 1`, `Propose` returns `Overflow=true`, `Chosen` empty, `Tokens 0`, `Item` an identifier (no spaces), `Archive == [Item]`, `Reason` naming the item; `Build` over that outcome files one `overflow` drop with ID `Item`, detail carrying `OVERFLOW`, the recovery path `recall(id)` and the selector's reason; `Overflowed(res.Dropped)` is true; every record is reported `archive_only`; no byte of any record's approach text is injected; `Result.Tokens ≤ budget`.
5. **Append-only** against THIS run's sealed artifact: a truncating `paths.OpenFile` and a second `paths.CreateNew` of the sealed sequence are refused with `core.ErrAppendOnly`/`os.ErrExist`. (`testutil.Project.AssertAppendOnly` seeds `0001.json` itself and cannot run on a project whose 0001 the test sealed — the same reason `test/integration/v4_x09_test.go` does not call it.)

## Negative controls and how they were proven

Both severe the producer through a REAL switch; no source edit was needed.

- **NC1 — promotion off through its shipped config state.** `Promote` is re-run with `Enabled = Cfg.Runtime.Phase7.Retrieval.DemandPromotion` (asserted `false`, the default). The plan still admits the candidate (report-only), `Order` is empty, `Apply` returns the writer's order unchanged. At the witness budget (2861) where arm 2 kept the promoted pointer while cutting a peer, the SAME pointer is now absent — it is the tail and is cut first. Proves the "promoted survives" assertion depends on promotion, not on the fixture.
- **NC2 — selection off through its rollback path.** `Build` is re-run with `Selection == nil`, which is exactly what a disabled `runtime.selection.submodularEnabled` (asserted `false`, the default) hands it. No `overflow` and no `archive_only` drops exist, `Overflowed` is false, and all three records render whole under the shipped slice-score ranking. Proves the overflow entry in arm 4 came from the selection, not from the rehydrator's budget arithmetic.

Authoring also observed the assertions go red on real behaviour twice before the test settled (both were assertion corrections, not production edits): the overflowed record is reported under BOTH `archive_only` and `overflow` (see Observations), and the seeded append-only probe collides with a sealed 0001.

## Old-to-new assertion map

| Historical expectation (§4.9 of the head text) | Disposition |
|---|---|
| "The checkpoint produced by 4.8" as setup | **corrected**: 4.8 is a sibling row with no artifact to share; the fixture is built over the real store, ledger and demand log in this test. |
| `checkpoint.Truncate` at 12 000, 6 000, 3 000, 900, 1 | **corrected**: the fixture is 2884 tokens, so those points are meaningless here; every budget from 2884 to 1 is walked instead, which subsumes any five points. |
| Monotone: `kept(b1) ⊆ kept(b2)` for `b1 < b2` | **kept** — checked for every consecutive pair, as byte-identical prefixes. |
| "At 6 000 the action-history block is gone before any decision is dropped" | **retired**: no action-history block exists in `checkpoint.Checkpoint` on this tree (SP-15's grammar/thrash record never became a checkpoint field). The surviving claim — tier 3 is exhausted before any decision goes — is asserted at every budget. |
| "At 900 all of tier 3 (pointers **including promoted ones**, narrative) is gone and tier 2 is partially cut in the documented order" | **corrected and kept**: the budget is whatever the ladder reaches, not 900; promoted pointers are the last tier-3 pointers to go and still go before any tier-2 record; the within-tier order is asserted stage by stage. |
| "At 1, tier 1 is byte-identical to the input with a single `DropEntry{Kind:"budget_exceeded"}` and no error" | **kept** (byte-identical on the three fields' JSON, one entry with ID `tier1`); "no error" is trivially true — `Truncate` has no error return — so it is asserted where it matters: `Finalize` at budget 1 still seals a verifying artifact. |
| "`TestTierReserveMatchesCurve` still passes against the committed curve artifact" | **retired**: no tier-reserve curve, artifact or test exists on this tree; SP-16 shipped promotion as a reorder under an overhead budget, not as measured tier reserves. |
| Seam "SP-15's tier-3 action history under SP-16's measured tier reserves" | **corrected** to the criterion's actual seam: SP-15's complete-record selection overflowing into SP-11's rehydrator, and SP-16's promotion ordering under SP-11's cut. |

## Observations (not defects, recorded for the coordinator)

- `rehydrate.applySelection` reports the overflowed record under BOTH `archive_only` (recovery path) and `overflow` (reason), because `Proposal.Archive` names the same item `Overflow.Item` does and the archive loop runs first. Nothing is lost and both entries are honest; the test pins the double report so a change to it is noticed. If a single entry is preferred, that is an SP-15/SP-11 owner decision.
- In the shipped daemon the selector and `Build` receive ONE budget (`rehydrate_service.go:153`). A selector overflow at that budget (pointer cost of every mandatory record exceeding it) implies the injection wrapper alone also exceeds it, so `Build` returns its `overflow`/`payload` entry before eliminations are built and the selector's `Item` never reaches the report. Both paths are explicit overflows (`Overflowed` is true either way); this test decouples the two budgets to show the SP-15 → SP-11 entry itself.

## Unverified remainder

- The daemon's own `selectionFor` translation is unexported and was mirrored, not called; its behaviour is covered by `internal/daemon/rehydrate_selection_test.go`, not by this row. No e2e run exercises a selector overflow through the real daemon because, per the observation above, no realistic single budget produces one that the wrapper overflow does not pre-empt.
- `promotion_overflow` (the SP-16 overhead budget refusing a candidate) is not driven here; it is unit-covered in `internal/checkpoint/promote_test.go`. The row's criterion is about the cut, and the overhead budget in the test is deliberately non-binding.
- Wall-clock: 10–19 s on this machine depending on co-load (the ladder is ~2900 `Truncate` calls); no timing is asserted.

## Run command and result

```
cd <worktree> && go test ./test/integration -list 'TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly'
  → TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly

cd <worktree> && go test ./test/integration -run '^TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly$' -count=1 -v
  → --- PASS: TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly (11.87s)   ok  (run 1)
  → --- PASS: TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly (19.43s)   ok  (run 2)
  → --- PASS: TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly (10.12s)   ok  (run 3)
```

`gofmt -l ./test/integration` clean; `go vet ./test/integration` clean;
`go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` → all five PASS.

# Role D — selector and objective

**Status:** DONE. Landed as `b142ba8` (`feat(analyzer): select feasible complete representations`)
on the integration branch, authored on `wip/sp15-d-selector` from `6eac57c`.

**Filed by Main, not by D.** A tool-level guardrail in D's harness blocked subagents from writing
report `.md` files, and D did not circumvent it — it returned the report as its structured summary
instead. The content below is D's, transcribed; the verification in "Main's independent check" is
Main's own.

## Files

| File | State |
|---|---|
| `internal/analyzer/selector.go` | modified — `NewSelector` byte-identical, `stubSelector` → `greedySelector` with a real `Select` |
| `internal/analyzer/greedy.go` | new — lazy-greedy engine, `Propose`, and contract §3's objective |
| `internal/analyzer/selector_test.go` | modified — 5 constructor cases kept verbatim, 26 added |
| `internal/analyzer/objective_test.go` | new — the M5-G15-A brute-force harness |
| `internal/analyzer/testdata/objective/*.json` | new — 7 instances |

`types.go`, `delta.go`, `redundancy.go` and `analyzertest/**` untouched, as the ownership map
requires.

## Gates, as D reported them

```
gofmt -l internal/analyzer                          → clean
go build ./internal/analyzer/                       → ok
go vet ./internal/analyzer/...                      → ok
go test ./internal/analyzer/... -count=1            → ok (0.944s / 0.936s)
go test ./internal/analyzer/... -count=1 -race      → ok (2.2s)
go run ./tools/lint/nomagic ./internal/analyzer/... → clean
```

92 `=== RUN` lines under `-v`, zero skips inside `internal/analyzer`. The `-run` trap was checked:
`-run TestObjective -v` lists all seven named subtests rather than printing a bare `ok`.

## Heuristic vs. exact (M5-G15-A) — measured, no bound asserted

| Instance | heuristic | exact | ratio | tokens | iters | chosen |
|---|---|---|---|---|---|---|
| dependency-closure | 2.550000 | 2.550000 | 1.000000 | 140/150 | 7 | 3 |
| mandatory-overflow | 0.000000 | 0.000000 | 1.000000 | 0/30 | 0 | 0 |
| plain | 1.950000 | 2.000000 | 0.975000 | 285/300 | 12 | 3 |
| qualification-g63 | 1.020000 | 1.020000 | 1.000000 | 70/80 | 8 | 2 |
| redundancy-nonmonotone | 1.550000 | 2.100000 | **0.738095** | 100/120 | 8 | 2 |
| tiny-budget | 0.300000 | 0.300000 | 1.000000 | 10/12 | 6 | 1 |
| zero-budget | 0.000000 | 0.000000 | 1.000000 | 0/0 | 2 | 0 |

**plain, 0.975** — pure cost-blindness. The walk ranks by marginal gain, not gain per token, so it
buys `item:a`'s exact span (1.0 for 120) where the optimum buys the capsule (0.6 for 40) and spends
the difference on `item:c`'s span.

**redundancy-nonmonotone, 0.738** — the designed counterexample, and the single most useful number
in this table. `item:a` and `item:b` share one evidence root and λ=1.2 exceeds their weights, so
`F({b}) = 1.0` while `F({a,b}) = 0.8`: **adding an element lowers the objective.** Greedy takes `a`
first (equal gain, lower Item, per the declared tie-break) and its 60-token price locks out the
40+40+40 set the optimum uses. This is precisely why no (1−1/e) claim is made anywhere in this
subplan, and why the fixture is committed rather than tuned away.

`mandatory-overflow` reports `iters=0` honestly — the overflow test runs before the greedy loop.
`zero-budget` reports `iters=2`: both moves priced and both refused, which is a measured refusal
rather than a short circuit.

**Observable pruning.** `TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer` measures
lazy=12 against eager=21 and asserts both modes select the *same set*.

## Decisions

1. **`Propose` is a free function with the guards literally on its path.** A `Selector` method was
   rejected: `Selector` is constructed over `[]Block`, whose shape is pinned by analyzertest,
   test/guards and test/integration, so a method would ignore its own receiver's blocks. `Propose`
   calls `NewSelector(p, candidateBlocks(cands), …)` and returns whatever it returned — same code,
   same order — then reads `p` back from `sel.P()` so the solver runs on the validated value. A
   pre-p candidate refuses the WHOLE call with `ErrBudget` rather than being silently filtered,
   because silent filtering would satisfy feasibility rule 4 while hiding a caller assembling
   against the wrong `p`.
2. **`redundancy(S)` is evidence-root duplication.** Contract §3 named the term without defining
   it. Ratified by Main as amendment A1.
3. **Overflow yields `Chosen == nil`.** The error is nil, because the caller needs `Reason` and
   `Archive`, but nothing is selected: a partial set carrying a flag is exactly the "dropped
   constraint reported as success" the plan forbids.
4. **Mandatory room is reserved against the whole budget before anything optional is bought**, with
   dependencies shared and counted once. A mandatory record the greedy never reaches (zero weight
   gives zero gain) is force-carried afterwards at its cheapest qualified representation.
5. **`Mandatory` alone does not bind.** Binding requires a deliverable representation whose
   `Prov.Qualification.Active()` holds, and a binding candidate is then restricted to active
   representations — so an authoritative elimination is never carried at stale evidence, which a
   consumer would correctly decline as binding, losing the constraint while reporting success.
6. **`RepArchiveOnly` never appears in `Chosen`** (ratified as amendment A3).
7. A bundled dependency is carried at its cheapest deliverable representation; a `Requires` entry
   outside the candidate set is read as already present in the prefix.
8. **An unscored block contributes zero.** Inventing a default would let unmeasured content
   displace measured content.
9. A cancelled context reports `core.ErrBudget` wrapping `ctx.Err()`, satisfying both
   `errors.Is(…, ErrBudget)` and `errors.Is(…, context.Canceled)`.
10. **Determinism is structural**: candidates sorted by Item ascending, representations by
    (Kind asc, Cost asc, Coverage desc, input order), `Requires` copied and sorted. Asserted against
    a shuffled input and against caller-slice mutation. No output is built by ranging a map.

No approximation bound appears in any file D owns.

## Main's independent check

Verified rather than accepted:

- Both `NewSelector` guards are byte-identical to the pre-SP-15 source, in the original order.
- `analyzertest`'s never-skipped `/constructor` block passes (4 cases) and the `/behaviour` block
  now genuinely RUNS (6 cases, previously a Rule W-1 skip).
- `grep` for bound claims across D's files returns only explicit *denials* of one.
- The full analyzer package and its conformance subpackage pass on the composed tree.

## Open questions, and Main's rulings

| D's question | Ruling |
|---|---|
| Confirm the redundancy definition | **Confirmed** as amendment A1: evidence-root duplication, chosen over a similarity-weighted term because it is exact, deterministic, threshold-free, and does not couple the selector to the near-duplicate detector |
| `analyzertest/suite_test.go` still reports a Rule W-1 skip with the gate closed | **Correct as is.** That case deliberately binds the STUB (`TestAnalyzerSuite_ShapePassesAgainstStub`); it is asserting that the shape holds against a stub, so skipping its behaviour block is the right answer. The real selector is exercised by `TestAnalyzerSuite_SelectorBehaviourRunsAgainstTheRealSelector` |
| The ship-order gate is process-wide; watch for parallel cases at integration | **Checked.** No case in `internal/analyzer` or `internal/daemon` that opens the gate uses `t.Parallel()`; all restore it with `t.Cleanup` |
| `Propose` has no consumer yet | **Resolved** in commit 6 (`f84cdf4`): `internal/daemon/rehydrate_selection.go` maps `negknow.Record` onto `Mandatory` + `Provenance.Qualification` and hands the outcome to `rehydrate.Build` |
| Is `submodularEnabled` gating the new surface the intended coupling? | **Yes**, and the daemon checks it a second time before doing any work, so a disabled selector costs no store read. `loopWarningsEnabled` gates warnings independently |
| `./test/guards` is RED on this base | **Pre-existing and confirmed**: `TestCarriedDefects_WaveReportRequiresResolution/{SP08-D1,SP10-D1}`, wave-2 carried-defect bookkeeping, no SP-15 file involved |

## Defect integration found in D's work

`Proposal.Reason` was prose that *contained* the offending item's name, while the contract said it
"names the overflowing item". `rehydrate` consumed it as a `DropEntry.ID`, which would have put a
whole sentence in the id column of the drop report. Fixed in `f84cdf4` by splitting `Proposal.Item`
(identifier) from `Proposal.Reason` (explanation) rather than by parsing one out of the other.

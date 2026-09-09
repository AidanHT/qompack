# V5-VERIFY §4.8 — `TestV5_GrammarAndPromotionCoexistInFinalize`

**Identifier (retained):** `TestV5_GrammarAndPromotionCoexistInFinalize`
**Current criterion (authoritative, §4 row 4.8):** SP-15/SP-16 codec/selection/promotion coexist
under scope, overhead and provenance.
**Disposition:** `partial` — the assertable parts are asserted with real producers; the parts the
historical text promised that no producer on this tree delivers are recorded below as retired or
unverified, never as passed.
**Base:** `verify/v5 @ 87c0c1d` (develop with wave 4 integrated: SP-15, SP-16, SP-14, SP-21).
**Branch:** `v5/x08`.

## Level and file

In-process integration: `test/integration/v5_x08_test.go`, package `integration`.

The historical seam was e2e (`checkpoint-now --json` after MCP `expand` through the daemon). It is
unwritable on this tree: `checkpoint-now` is not a subcommand (`qompack checkpoint` is the
PreCompact hook, `internal/commands/spec.go`), `checkpoint.Promote` has no production call site
(`plans/V5-SP-16-M6-evidence.md` §5, "partially met"), and no grammar fold is wired into
`checkpoint.Finalize` — so nothing across a process boundary can observe either producer. The rig
composes the seam the way `internal/cli/scheduler_wiring.go` does: one `grammar.New()` handed to
both the real observer and the real `checkpoint.SourceSet`; real store, DAG, ledger, pin store,
`checkpoint.FileWriter`; the real `mcp` server with the real file-backed promoter
(`mcp.NewPromoter` over `.qompack/state/promotions.json`) and the real `store.DemandLog`.

## Production change (own commit, first)

`fix(checkpoint): resolve tool pointers by root, not object` — `internal/checkpoint/finalize.go`,
regression test `TestFinalizeKeepsToolPointersWhoseRootTheStoreHolds` in
`internal/checkpoint/finalize_paths_test.go`.

`keepResolvableTools` decided whether a tool pointer survived Finalize with `src.Store.Has(p.Hash)`.
A tool pointer's `Hash` is the result's ROOT (`internal/observer/tooluse.go` records
`store.PutResult.Root.Hash`; `expand` resolves it through `GetRoot`), and a root is not an object:
`chunk.RootHash` digests the chunk list, and `store.Has` answers for chunk objects only. On the
production path every observer-captured tool pointer was therefore dropped at Finalize as
`pointer_unresolvable` ("object missing from the store (collected?)"). The only existing test of
that branch (`TestFinalizeDropsToolPointersTheStoreNoLongerHolds`) uses a fake whose `Has` is
always false, so it could not see it; no test covered survival with a real store.

The fix keeps a pointer when `Has(hash)` is true (a pointer naming a chunk directly) OR the root
resolves through `GetRoot` and every chunk it lists is still held — exactly what `expand(hash)`
needs. The drop test stays green (its fake's `Has` is false for the chunks too), the new test pins
the positive direction against the real `FSStore` and asserts the fixture's pointer really names a
root (`Has(root)` false) so it cannot decay into a chunk-hash test.

Red proof: with `finalize.go` reverted to the base version (`git checkout --` on that one file,
then restored from a copy; no stash), `TestFinalizeKeepsToolPointersWhoseRootTheStoreHolds` fails
("a pointer whose root and chunks are held must survive Finalize") and this row fails at
"every observed tool use must have become a tier-3 pointer that survived Finalize's store check".
Both pass with the fix. `go test ./internal/checkpoint -count=1` passes in full (118 s).

## Subtests and what each asserts

| Subtest | Asserts, with real producers |
|---|---|
| `GrammarInducesRulesFromTheObservedCycles` | After 11 `FileRead FileEdit Bash(test:fail)` cycles through `observer.OnToolUse`/`OnUserPrompt`, the shared `grammar.Sequitur` has a top-level `Compressed()` sequence shorter than the 55 symbols appended, at least one rule whose expansion covers the whole cycle, and a non-empty `Thrash(2)`. |
| `CodecRoundTripsTheLiveGrammar` | SP-15's codec: `MarshalBinary` of the live grammar restores into a fresh `grammar.New()` with identical `Compressed()` and `Rules()`; a frame cut in half is refused by both `DecodeSnapshot` and `UnmarshalBinary` (codec negative control). |
| `FinalizeWritesTheV1ShapeWithNoGrammarSection` | Real `Begin`/`Advance`/`Finalize` over the segment the observer opened and `SessionEnd` closed: `version == 1`, top-level key set identical to `testdata/golden/checkpoints/0001-minimal.json`, all 33 tool uses present as tier-3 pointers with distinct roots, 11 file pointers, frontier at turn 11, the writer's own `seg N turns` narrative line present; and — recorded as ABSENT — no `"grammar-compressed"` text in the narrative, `sketch_refs` exactly `{tried, touch}`, no `grammar/actions.seq` on disk. The produced file is held to `TestGoldenCheckpointsContainNoCodeBlocks`'s three-layer rule. |
| `DisabledPromotionIsRecordedAsDisabledNeverAsApplied` | The real switch: the loader resolved `runtime.phase7.retrieval.demandPromotion=false`; `config.MigrationGates()` lists it with `Passed=false`; `config.Defaults()` with it set true is refused by `Validate` naming M6-G16-C; a project whose `config.json` asks for `true` loads as `false`. `checkpoint.Promote` with that value: `Considered=2`, `Admitted()=1`, `Order` empty, an omission saying it was disabled and naming the key; `Apply` returns the archived pointer order and the byte-identical document. |
| `EnabledPromotionReordersOnlyTheNextCheckpointAndSurvivesTruncation` | The enabled evaluation (a request value — no build can turn the switch on): the tail pointer, re-expanded 3× through the real `expand` handler (counts and `promoted` flag read from `_meta.qompack`, listed by the real promoter), with a real instrumented demand record, is the only promoted hash; the uninstrumented second candidate is `Withheld` with the frequency-is-not-usefulness reason. `Apply` puts it first without adding or removing anything and without mutating the archived checkpoint; at the largest budget that cuts a tool pointer, `Truncate` cuts it without promotion and keeps it with, within budget. The promoted document keeps the v1 key set, `version: 1`, and the no-code rule. |
| `OverheadBudgetOverflowsWithADropEntry` | With `Overhead` one token below the real estimator's price of the pointer: `PromotionOverflowed`, `Spent=0`, `Order` empty, one `promotion_overflow` drop naming the tool-use id, handed back by `Apply`; zero overhead promotes nothing. |
| `ProvenanceAndScopeGates` | `TargetSeq == ObservedSeq` refuses every candidate with the same-epoch reason; an unrecognised `core.Authority` is refused; the real promoter reports no promotions for another session (scope). |
| `NegativeControl_CorruptPromotionsFileYieldsNoCandidates` | Sever the producer through its real corrupt-file path: overwrite `promotions.json` with garbage, open a new promoter, `Promoted()` is empty, the corrupt bytes are quarantined under `tmp/quarantine/`; the same composition step yields `Considered=0`, `Apply` is the identity, and the tail pointer stays at the tail. |
| `NegativeControl_AnUnfedGrammarInducesNothing` | A fresh grammar has no sequence, rules or thrash. |
| `SealedCheckpointIsAppendOnly` | `testutil.AssertAppendOnly`'s two checkpoint probes against the artifact this row sealed (that helper seeds `0001.json` itself and requires it absent): a truncating `paths.OpenFile` is `core.ErrAppendOnly`, a second `paths.CreateNew` at the same seq is `os.ErrExist`, bytes unchanged. |

## Negative control and how it was proven

1. **Real switch, both directions on the same inputs.** The disabled arm (the config value the
   real loader produced) asserts `Order` empty and `Apply` identity; the enabled arm asserts the
   reorder and truncation survival. Either alone could be vacuous; together they prove the
   assertion tracks the switch.
2. **Real fault path.** Corrupting `promotions.json` — the promoter's documented quarantine path,
   not a stub — removes every candidate and the reorder disappears.
3. **Codec.** A truncated frame is refused.
4. **Production fix.** Red on the base `finalize.go`, green with the fix (see above); the drop-side
   test that pre-existed stays green.
5. **Fixture self-checks.** The row fails if any two pointers share a root (they did once: the
   store's canonicalizer strips timings, so outputs varying only in timings hashed equal — fixed by
   varying a word), or if the regression fixture's pointer is a chunk hash rather than a root.

## Old-to-new assertion map

| Historical expectation (§4.8 at HEAD `7f92af5`) | Disposition |
|---|---|
| Setup: 11 `FileRead FileEdit Bash test:fail` cycles through the real observer | **kept** — driven through `observer.OnToolUse`; `ExtractTestOutcome` asserted `TestFail` per cycle |
| Setup: expand one root 3× through MCP `expand` (threshold 2) | **kept** — the real handler, the real promoter, `promoteAfterExpansions` read from config; `promoted` flips at 2, count reaches 3 |
| Input: `checkpoint-now --json` | **retired** — no such subcommand; replaced by the real `FileWriter.Begin/Advance/Finalize` and the on-disk `NNNN.json` |
| Narrative contains `"Action history (grammar-compressed"` | **retired as absent** — no grammar fold is wired into Finalize; asserted NOT present so a future wiring fails this row and forces a rewrite of this map |
| `sketch_refs.grammar == "grammar/actions.seq"` | **retired as absent** — `sketch_refs` is exactly `{tried, touch}`; no such file is written |
| `pointers.tools` entry whose `summary` starts with `"promoted: re-expanded 3×"` at an elevated weight | **corrected** — promotion adds no field and rewrites no summary (Rule W-2, `promote.go`); it REORDERS `pointers.tools` so the pointer survives `Truncate`. Asserted as "promoted pointer leads, survives the budget that cut it" |
| Order: grammar first, promotion second | **retired** — there is no grammar section to order against |
| `"version": 1` unchanged and v1 key set identical to the golden | **kept** — before and after promotion |
| `TestGoldenCheckpointsContainNoCodeBlocks` passes on the produced file | **kept** — the unexported guard's three layers re-stated over the produced bytes, before and after promotion |
| `Finalize` wall time < 2 s (B-E) | **retired here** — timing gates are co-load sensitive on this machine and §5 assigns them to quiet runners; not asserted |
| "coexist" = both blocks survive a merge of SP-15 and SP-16 in one file | **corrected** — the two producers coexist as: one grammar instance feeding both observer and SourceSet, real codec round trip, and promotion applied to the finalized document without schema change |

## Unverified remainder

- **Grammar fold into the checkpoint narrative** — no producer on this tree (`Compressed()` has no
  caller outside `internal/grammar`). Recorded as absent, not verified.
- **Promotion at a production call site** — `checkpoint.Promote` is a pure function nothing in the
  daemon calls (M6-G16-C partially met; the rehydration-budget half of SP-16 commit 5 is not
  written). The enabled arm here is the composition root's call as it would be made; the pairing
  of `Demand.Key` to `Pointer.Hash` is done by this test, which is the check the evidence document
  says belongs to the unwritten wiring commit.
- **`runtime.phase7.retrieval.demandPromotion` enabled in a real build** — refused by `Validate`
  while the gate is pending; recorded as disabled.
- **SP-15 selection (`internal/analyzer`) under overhead** — not exercised by this row; it reaches
  `rehydrate.Build` as request data at the daemon root (§3a.2) and is §4.7's seam. SP-16 scoped
  reuse (`internal/daemon/reusable.go`) is §4.12's. "Scope" here is asserted at the promoter's
  per-session boundary only.
- **B-E timing** — not asserted (see map).

## Run

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x08
go test ./test/integration -list 'TestV5_GrammarAndPromotionCoexistInFinalize'
go test ./test/integration -run '^TestV5_GrammarAndPromotionCoexistInFinalize$' -count=1 -v
go test ./internal/checkpoint -run '^TestFinalizeKeepsToolPointersWhoseRootTheStoreHolds$' -count=1
```

Results (Windows 11, go1.26.6, this machine under other agents' load):

- `-list` selects exactly `TestV5_GrammarAndPromotionCoexistInFinalize`.
- Focused run, twice with `-count=1`: PASS (7.6 s and 17.1 s wall; all ten subtests PASS both
  times).
- `go test ./internal/checkpoint -count=1`: ok (118 s).
- Red proof on the pre-fix `finalize.go`: both the regression test and this row FAIL as described.
- `gofmt -l ./test ./internal`: clean. `go vet ./test/e2e ./test/integration`: clean.
- `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns`: PASS.

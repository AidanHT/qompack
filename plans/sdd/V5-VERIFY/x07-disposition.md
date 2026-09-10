# V5-VERIFY §4.7 disposition — `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint`

| Field | Value |
|---|---|
| Retained identifier | `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint` |
| Current criterion (plan §4, authoritative) | SP-15 selected complete records reach actual SP-11 consumer without native cuts. |
| Disposition | **authored** |
| Level | e2e — `test/e2e/v5_x07_test.go` |
| Base | `verify/v5` @ `87c0c1d`, branch `v5/x07` |
| Production changes | none (the red-run edit below was applied and reverted during authoring; `git diff` was empty except the new files) |

## The seam on this tree

The historical §4.7 named a seam that was never built: scheduler `Decision.P` → `dag.NodesAfter(P.Pos)` →
`analyzer.NewSelector(p=P.Pos)` → checkpoint narrative. On this tree the SP-15 consumer integration
is `internal/daemon/rehydrate_selection.go` (plans/sdd/V5-SP-15/contract.md §1, report-main.md §1):

```
negknow ledger + checkpoint.Eliminated  --rehydrateService.eliminationCandidates-->  []analyzer.Candidate
analyzer.Propose(ctx, p=0, cands, lambda, budget)  ------------------------------->  analyzer.Proposal
daemon maps Proposal onto rehydrate.SelectionOutcome (Keep / Archive / Overflow / Item / Reason)
rehydrate.BuildWithStats(Request{Selection: ...})   --item 3 honours it; item 7 carries archive/overflow
```

The consumer is the daemon's real `rehydrateService.OnCompact`, driven through the real binary's
`session-start` hook with `source=compact`. Two real guards sit in front of the selector, and both are
exercised as switches: the operator key `runtime.selection.submodularEnabled` (checked first in
`selectionFor`), and the closing-note-3 gate `scheduler.PSelectionAvailable()` that only a constructed
`daemon.NewSchedulerRuntime` opens (checked inside `analyzer.NewSelector`, which `Propose` routes
through). "Without native cuts": `scheduler.Supports(CapNativeCut)` is `ClassUnsupported` on this
tree and no configuration changes that; the capability that lets the selector run is the LOCAL
runtime over the real store and DAG, and `Propose` is called at `p = 0`.

## Subtests and what each asserts

| Subtest | Config | Gate | Asserts |
|---|---|---|---|
| `selected_complete_records_reach_item3` | `submodularEnabled=true`, default budgets (8000–12000) | open (real `NewSchedulerRuntime`) | `CapNativeCut` unavailable/unsupported; compact payload contains `## 3. Approaches already eliminated`; each of the 3 recorded eliminations appears as the consumer's COMPLETE line `target — "approach" — reason [active]`; the three lines render in record-id-ascending order (the selector's own order: `Proposal.Chosen` walks candidates by Item, `SelectionOutcome.Keep` replaces the slice-score ranking); drop report has no `archive_only` and no `overflow` naming a record; runtime close releases the gate; `AssertAppendOnly`. |
| `explicit_overflow_reaches_item7` | `submodularEnabled=true`, `rehydrate.minTokens=1`, `maxTokens=120` | open | Preconditions asserted, not assumed: the wrapper fitted (no `overflow/payload` entry, so `Build` reached `buildAll`) and the 48 record ids price above 120 tokens under the daemon's own `tokens.NewForProject` estimator. Then: all 48 records reported `archive_only` by id, every one carrying the recovery wording; exactly one `overflow` entry whose ID column is a record id and whose detail says "representation selection"; zero `elimination`-kind drops naming a record (an overflowed selection admits nothing). `AssertAppendOnly`. |
| `negative_control_operator_switch_off` | `submodularEnabled=false`, same pressure | open | Same preconditions; then zero `archive_only`, zero record-naming `overflow`, zero selection wording, and a POSITIVE count of `elimination`-kind drops naming records — the shipped ranking reached the consumer and reported what it could not fit. `AssertAppendOnly`. |
| `negative_control_gate_closed` | `submodularEnabled=true`, same pressure | closed (no runtime; `DisablePSelection` first because the gate is process-wide and another row may leak it open) | Gate stays closed across the compaction; same absence-of-signature assertions as control 1. This is §6's replacement guard: the prerequisite is an actual local capability and its absence is honest degradation to the shipped path, never a failed session. |

## Negative control, and how it was proven

Two real switches, each severed in its own arm under the same pressure that makes the selector's
decision visible in the consumer's drop report (read back through the real `rehydrate.Reporter`):

1. `runtime.selection.submodularEnabled=false` (project config) with the gate open.
2. No `scheduler.Runtime` constructed (gate closed) with the operator switch on.

In addition, a source-edit red-run was performed during authoring: in
`internal/daemon/rehydrate_selection.go`, the first guard of `selectionFor` was changed to
`if !s.o.Cfg.Runtime.Selection.SubmodularEnabled || true { return nil }` (producer severed).
Result: `selected_complete_records_reach_item3` FAILED at the order assertion
(`elim_6afe9cab41b5 is out of place`), `explicit_overflow_reaches_item7` FAILED at the archive_only
count (the report carried the shipped path's `elimination` drops instead), and both negative controls
PASSED — exactly the expected polarity. The edit was reverted with `git checkout --`; `git status
--porcelain` afterwards showed only the new test file.

## Old-to-new assertion map

| Historical expectation (§4.7 at HEAD `7f92af5`) | Disposition |
|---|---|
| 300-turn `eval.Synthesize` seed `0x5105_0001` session through the observer, scheduler bound, `advanceOnSegmentClose=true` | **retired** — the seam the setup fed (scheduler cut → suffix selection → checkpoint) was never built; the shipped seam is elimination selection into the rehydrator. Replaced by a real hook-driven session with eliminations recorded through the real `record_eliminated` handler. |
| `Runtime.Evaluate()`; `Decision.ShouldCompact == true` with non-empty `Reasons` | **retired** — a scheduler decision is not a prerequisite of the shipped selector; `Propose` runs at `p = 0`. §4.13 owns the retirement of native-cut assertions; the real `NewSchedulerRuntime` is still constructed here because it is the local capability that opens the gate. |
| Build `analyzer.Block`s for `dag.NodesAfter(P.Pos)`; every offered block has `Pos ≥ P.Pos` | **retired** — `dag.NodesAfter` does not exist on this tree and the selector's candidates are eliminations, all at `Pos 0`. `NewSelector`'s invariant-4 guard still runs on every `Propose` (§4.6 asserts it from both sides). |
| Select under a 12 000-token budget | **corrected** — the daemon hands `Propose` the rehydration budget (`runtime.rehydrate.maxTokens`, default 12 000); arm 1 runs at that default, arms 2–4 at 120 to force the overflow branch. |
| `Dropped` contains no `KindUserPrompt` / `KindDecision` / `KindElimination` block | **corrected** — the shipped selector carries every ACTIVE elimination as `Mandatory`, so at the default budget no record is archived or overflowed (arm 1 asserts both counts are zero) and each is rendered complete; under pressure the outcome is an EXPLICIT overflow with a recovery path (arm 2), never a silent drop. |
| Then run `checkpoint-now`; the written checkpoint contains every surviving decision | **retired** — the selection's consumer is the rehydration payload and its drop report, not the checkpoint file; `checkpoint-now` is not a command on this tree. Replaced by: every selected record reaches item 3 whole, in the selector's order. |
| `encoded_segments` disjoint across checkpoints (`MarkEncoded` never re-encodes) | **retired here** — a checkpoint-frontier property, not a selection one; V4 §4 rows own it. |
| `Truncate` at the same budget produces the same tier-1 content byte-for-byte | **retired here** — §4.9 owns truncation/overflow ordering. |
| (implicit) selection is downstream of a native cut position | **retired as unsafe** — `scheduler.CapNativeCut` is `ClassUnsupported`; arm 1 asserts the register says so while the selection demonstrably fed the consumer, and control 2 asserts the replacement guard (local capability) from the closed side. |

## Unverified remainder

- The `p` the selector reasons with is not observable from outside the daemon; that it is `0` is
  documented in `eliminationCandidates` and exercised by `internal/daemon/rehydrate_selection_test.go`,
  not re-asserted here.
- The order assertion in arm 1 is a necessary condition of the selection path, not by itself a
  sufficient one (with hash-based record ids the pre-SP-15 order can coincide with id order by
  chance); sufficiency rests on arms 2–4 and on the red-run above.
- Representation KIND is not observable at the consumer: `SelectionOutcome.Keep` carries item ids
  only, so a record the selector carried as a pointer renders exactly like one carried as its exact
  span. The row asserts complete records reach the consumer; it cannot assert which representation
  was bought. Recorded as a known fidelity gap of the shipped seam, not a defect of this row.
- `runtime.selection.submodularEnabled` ships OFF; arm 1's positive result is under an explicit
  opt-in and is not evidence about the default session.

## Run

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x07
go test ./test/e2e -list 'TestV5_ScheduledCut'              # → TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint
go test ./test/e2e -run '^TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint$' -count=1 -timeout=10m -v
```

Result: PASS, twice, `-count=1` each (15.51 s, 15.55 s); all four subtests pass on both runs. Red-run with the producer severed: 2 FAIL (arms 1–2), 2 PASS (controls), as
expected. `gofmt -l ./test ./internal` clean; `go vet ./test/e2e ./test/integration` clean;
`go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` clean.

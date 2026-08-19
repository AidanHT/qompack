# SP-02 → V2-VERIFY handoff

**Written by the SP-02 implementation session, for the wave-1 verification checkpoint.**
**Read this before running §2.2 of `V2-VERIFY-primitives-store-dag-and-baseline.md`.**

`V2-VERIFY` §2.2 was written before `internal/eval` and `test/replay` existed. Six of its twenty
SP-02 rows do not match what shipped. Two of those **pass without testing anything** — they exit 0
having run nothing — and one would actively damage the code if followed literally.

Every claim below was checked by running the command. Nothing here is inferred.

---

## §1. Rows that pass vacuously

These are the dangerous ones. A green tick against them means nothing was verified.

### V2-SP02-15 — the `--` separator voided every flag

The row reads:

```sh
go run ./tools/devtool replay -- --corpus testdata/sessions/synthetic \
    --baseline testdata/baseline/phase0.json --phase 0 --ci
```

`devtool replay` forwards its arguments verbatim, so the driver received `["--", "--corpus", …]`.
Go's `flag` stops at a bare `--`. Every flag after it was discarded: the driver replayed the
**default** corpus, **without** `--ci`, **without** `--phase 0`, and exited 0. The stdout is
byte-identical to a real run.

**This is fixed in the driver, not in the row.** Unconsumed arguments are now refused:

```
replay: unexpected argument "--corpus": replay takes flags only, and a bare "--" stops flag
parsing, so every flag after it is silently ignored — drop the separator
```

exit 2. Covered by `TestReplayDriver_LeftoverArgumentsAreBadInput`.

**Correct the row to drop the `--`:**

```sh
go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
    --baseline testdata/baseline/phase0.json --phase 0 --ci
```

### V2-SP02-17 — `-run TestImports` matches no test

<!-- runpatterns: the transcript below demonstrates the unsatisfiable command this section reports -->
```
$ go test -run TestImports ./internal/eval/
ok  github.com/qompack/qompack/internal/eval  1.763s [no tests to run]
$ echo $?
0
```

The import-purity check never ran. The test is `TestImportGraph_EvalIsFoundationOnly` in
`internal/eval/importgraph_test.go`.

**Correct command:** `go test -run TestImportGraph_ ./internal/eval/`

---

## §2. A row that would damage the code if followed

### V2-SP02-12 — "`grep -rn "t.Skip" internal/eval` returns nothing"

It returns three hits, and all three must stay:

| Location | What it is |
|---|---|
| `internal/eval/synth_test.go:333` | The `QOMPACK_EVAL_WRITE_CORPUS=1` guard on `TestSynthesize_WriteCorpus`. It exists so CI can never rewrite the corpus it is measuring against. Removing it makes the corpus self-modifying under test. |
| `internal/eval/evaltest/suite.go:102` | The Rule W-1 skip machinery itself. It is dormant because `eval` is real, but it is the mechanism, not a skip. Removing it deletes SP-01's conformance-suite contract. |
| `internal/eval/evaltest/suite.go:97` | A comment describing the line above. |

**What the row is actually trying to assert** — no Rule W-1 *behaviour* skip remains — already holds
and is checked two ways: `go run ./tools/devtool lint --only=stubskips` reports `evaltest` with zero
skips (`internal/eval`'s single skip is reported as `platform-gated`), and
`TestRunHarnessSuite_AgainstEvalNew` runs the whole suite against the real `eval.New`.

**Correct observable:** `go run ./tools/devtool lint --only=stubskips` lists no skips for
`internal/eval/evaltest`, and `internal/eval`'s one skip is the platform-gated notice.

---

## §3. Rows whose named tests differ

The behaviours are all covered; only the names drifted. `go test ./test/replay/...` and
`go test ./internal/eval/...` are green.

| Row | Plan says | Actual |
|---|---|---|
| V2-SP02-13 | "all 19 `TestGate_*` rows" | **17** `TestGate_*`, plus 3 `TestPhase0_*`, plus 13 `TestReplayDriver_*` — 36 tests in `test/replay` |
| V2-SP02-13 | `TestGate_Phase0ExitCriterion` | `TestPhase0_SessionCountFloor`, `TestPhase0_RequiresStock`, `TestPhase0_Reproducibility` |
| V2-SP02-13 | `TestGate_PhaseChecksMayNotBeDisabledInCI` | `TestReplayDriver_PhaseChecksMayNotBeDisabledInCI` |
| V2-SP02-06 | `TestReplay_OracleFewerRepairsThanStock` | `TestReplay_OracleNeedsNoRepairsWhenItFits` |
| V2-SP02-14 | `--to <path>` | No such flag. Use `--write-baseline --baseline <path>`; `--out` is the *report*, not the baseline. |

Rows **02, 03, 04, 05, 07, 08, 09, 10, 11, 16, 18, 19, 20** are correct as written and pass. `-race`
works on the Windows toolchain here, so V2-SP02-01 is fine.

---

## §4. Things the checkpoint must *do*, not just check

### 4.1 `landedSubplans` must gain SP-03 … SP-07 — this one is load-bearing

`tools/devtool/cover.go` holds:

```go
var landedSubplans = map[string]bool{"SP-01": true, "SP-02": true}
```

A package's §6.4 coverage floor is skipped until its owner appears here. Before SP-02, the gate was
`o.Owner != "SP-01"`, so a landed SP-02 would have shipped with **no floor at all** — `internal/eval`
printed `exempt (stub, owned by SP-02)` and its 90.9% was never compared to 85%.

**The same hole is now open for SP-03 … SP-07.** If this set is not updated as wave 1 merges, rows
V2-SP03-xx … V2-SP07-xx "Coverage floor" will print `exempt` and pass while measuring nothing.

`TestLandedSubplansMatchesTheBranch` fires when SP-03 lands, which forces someone to look — but its
assertion message still says "SP-03 has not landed", which reads backwards at that moment. **Rewrite
that test once the whole wave is in**, so it asserts the wave-1 set rather than a single tripwire.

Inferring "landed" from the `OWNERS.tsv` probe was tried and is unsound, so do not reach for it:
`probeStillStub` only recognises the `core.ErrNotImplemented` shape, but `chunk.Split` returns `nil`
and `grammar.Append` returns nothing. It gates code nobody has written and exempts code that
shipped. The explicit set is deliberate.

### 4.2 `replay-gate` must become a required check

`ci.yml` no longer carries `continue-on-error` on `replay-gate`, but "required" is a
branch-protection setting on `develop` that cannot be set from the working tree. Until it is set, a
red gate does not block a merge. `bench-gate` still carries `continue-on-error`; SP-05 owns removing
it.

---

## §5. Standing facts about the Phase-0 numbers

Recorded so nobody reads a designed property as a defect, or a re-baselining as a regression.

**`stock` and `null` report identical `rewrite_tokens` (16 891 126) and `pause_p95` (116 198).** This
is by construction, not a bug: a Full Compact rewrites the whole message array, so `p_min = 0` for
both, and §5.2's rewrite cost `w·(n − p_min)` collapses to `w·n`. Documented at
`internal/eval/policy.go:259`. Consequence: **the 2% rule cannot distinguish `stock` from `null` on
those two metrics** until a partial-compaction policy exists. The primary metric separates them
cleanly — `fraction_of_opt` 0.695164 vs 0.000000.

**All latency is modelled**, tagged `"latency": "modelled"` on every number. Changing
`DefaultLatencyModel()` moves `pause_p95` by far more than 2% and will read as a regression. It is
not one — it is an instrument change, and the correct response is re-baselining per ADR 0003, **not**
a `Sign-off:` trailer. The sign-off exists for deliberate trade-offs within one instrument.

**`retrieval_hit_rate` is 0.0 for all three policies, with `retrieval_actions: 0`.** No policy
retrieves until SP-13. The driver prints a note beside it saying the zero is an absence of calls
rather than a failure. Inert, not broken.

**Corpus staleness clock.** `CORPUS.json` carries `regeneratedAfterPhase: 0`, and the gate fails at
`--phase > 2`. Phase 3 requires regeneration — protocol in ADR 0003.

**The recorded tier is empty.** §6.3 tier 2 (recorded sessions) gates releases. The importer exists
and is tested, but no recorded corpus has ever been collected, so release-gating on recorded
sessions is unproven. Recorded transcripts are never committed, so this cannot be fixed by SP-02.

**Two SP-01 stub packages sit at 0.0% coverage** — `internal/canon` and `internal/symbols` — observed
while wiring the floor. Not SP-02's business; SP-04 should know its starting point.

---

## §6. Where SP-02 went outside its declared file list

The subplan named `internal/eval`, `test/replay`, `testdata/`, `docs/adr/`,
`internal/cli/register_eval.go`, `tools/devtool/task_replay.go` and `.github/workflows/ci.yml`.
These were also touched, each mechanically necessary:

| File | Why |
|---|---|
| `tools/devtool/replay.go` | SP-01 shipped the registered task here. Modified in place rather than adding a duplicate `task_replay.go`. |
| `tools/devtool/importrules.go` | `test/replay` must be a composition root or `lint --only=importgraph` fails. |
| `tools/devtool/cover.go` | See §4.1 — without it the coverage floor could not bind and DoD "cover shows `internal/eval` ≥ 85%" was unsatisfiable. |
| `internal/cli/commands.go` | One line registering `evalCmds()`. |
| `test/guards/stubs_test.go`, `test/guards/v1_integration_test.go` | Rule W-1 flips: `eval` is no longer a stub. |
| `plans/OWNERS.tsv` | Header comment only, describing the new `cover` rule. No row changed. |

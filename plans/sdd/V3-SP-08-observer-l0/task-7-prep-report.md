# Task 7 prep report — Phase 1 exit-criterion harness (isolated worktree run)

Date: 2026-08-25. Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08-t7`,
detached at `7885650` (Commit 5 head — no `session.go`, so no session-start calls are made and
sessions run with no open segment, which is fine for ratios). Nothing committed, nothing pushed;
the deliverables are handed over as artifacts:

- `qompack-sp08-t7/test/e2e/phase1_exit_test.go` — the harness (seven `TestPhase1_*` rows plus
  `corpus`/`corpusPayloadFor`/`eventsFor` and the ruling's volatile refresh).
- `qompack-sp08-t7/phase1-ratios.json` — every measured ratio and the supporting sizes.

## Verdict

**All four design-number gates pass, unweakened**: read-heavy `DedupRatio` = **189.35 ≥ 4.0**
(the §10 Phase 1 exit criterion), and the test-output-heavy canonicalization gap =
**4.008× ≥ 1.25×** (108.74 on vs 27.13 off). 7 of the 8 test rows are GREEN; the eighth,
`TestPhase1_HotPathBudgetDocumented`, is RED by design — it asserts `docs/adr/0008-observer-l0.md`
exists with a B-A p99 figure, and the ADR is Commit 7's own later deliverable (see "RED/GREEN").

## The measured ratios

All ratios are `store.Stats().DedupRatio` = `RawBytes / Bytes` (§5.8); no other definition is
used anywhere in the harness.

| Run | Canon | RawBytes | Stored | Ratio | Gate |
|---|---|---:|---:|---:|---|
| read-heavy (seed 0x51080001) | on | 14,003,773 | 73,959 | **189.35** | ≥ 4.0 → **PASS** |
| read-heavy | off | 14,003,773 | 77,236* | 181.31 | (reference) |
| test-output-heavy (seed 0x51080002) | on | 7,815,160 | 71,870 | **108.74** | — |
| test-output-heavy | off | 7,815,160 | 288,059 | 27.13 | on ≥ off×1.25 → gap **4.008×**, **PASS** |

*read-heavy off stored bytes back-computed from RawBytes/ratio; the harness reports ratios.

Supporting probes (read-heavy canon-on run): Files=106, MarkSuperseded=133, HLL cardinality=101,
objects=128, tool calls driven=336 (of 400 `Stats().ToolUses`; the difference is prompt events
and generator bookkeeping), raw bytes/call=41,678 (floor 1,024), first-half stored=72,455 vs
second-half=1,504 (sublinear gate). CorpusSweep replayed all committed synthetic sessions
without a panic, ratios 66.7–99.8.

## The controller ruling and how it was implemented

My earlier run stopped at the canonicalization gate: with corpus bytes re-served byte-identical,
the gap measured **0.969** (canon-on was *worse* — it collapsed nothing exact dedup didn't
already collapse, and paid delta/bookkeeping overhead). The controller ruled the measurement was
structurally unable to show O2's value and ordered a **deterministic, seeded volatile-substring
refresh** on the bytes served for the **bash and testrunner groups only**, per §8.1's own
premise that timestamps/PIDs/durations make every Bash and test-runner output unique in reality.

Implementation (`refreshVolatile` in the test file, fully documented there):

- Rewrites, in the served copy only, **exactly and only substring classes the configured strip
  set covers** (config.Defaults() strip = timestamps, ansi, pids, addresses, tmpPaths,
  durations): wall-clock `hh:mm:ss` (bare and inside ISO-8601), `pid=NNN`-style ids,
  `goroutine NNN [` ids, fractional `N.NNs`/`NNNms` run durations, and `0x…`/`@…` hex addresses.
  I verified each rewrite shape against `internal/canon`'s actual matchers (`generic.go`,
  `tools.go`, `numeric.go`) so canon-on collapses every refreshed variant to one canonical form.
- Every replacement preserves span length and shape; substituted digit runs are kept ≥ 3 bytes
  where canon's token is 3 bytes, so no refreshed span is dropped by canon's no-growth guard
  (e.g. 1–2-digit goroutine ids are left alone).
- Values derive from `(seed, seq)` via a splitmix64 stream; the seed is FNV-64a of the session
  ID, so a session's event stream is a pure function of (Synthesize seed, spec) and is
  byte-identical across the canon-on and canon-off runs (RawBytes agree on both sides).
- `fileread`/`grep`/`glob`/`webfetch` bytes stay byte-identical per path — an unchanged file
  re-read IS identical in reality. No committed corpus file was edited; `internal/eval` and
  `internal/observer` are untouched; the refresh is pure and lives only in the test file.

Diagnosis of why it now works: the bash group's committed captures (curl/docker/ls/npm/ps)
contain essentially **zero** spans in the covered classes, so the refresh is a near-no-op there
and read-heavy barely moved (189.345 vs 189.353 before). The testrunner group is duration-rich
(go-test-pass alone has ~117 duration spans, go-test-fail adds goroutine ids and ten 0x
addresses), so each serving is honestly unique: canon-off must store every dirtied chunk
(288 KB) while canon-on collapses them to the canonical form plus small deltas (72 KB).

## Adaptations from the brief's verbatim code (all deliberate, all noted in-file)

1. **`payloadFor` → `corpusPayloadFor`**: package `e2e` already has a `payloadFor`
   (hooks_test.go) with a different signature; the loader keeps the plan's parameter list under
   the new name, **extended by `seed`** (the ruling's refresh needs it; the plan's four-argument
   signature predates the ruling).
2. **Bash-group choice per spec**: the plan routes Bash → "testrunner" for testOutputHeavy and
   → "bash" for readHeavy, but the binding signature has no spec parameter; the choice is
   carried by the corpus value instead (`withBashFrom(c, "testrunner")` for the test-heavy runs).
3. **Group table extended**: `eval.Synthesize` emits tool names the plan's five-rule mapping
   does not cover (`FileRead`, `Write`, `Test`, `Task`, `record_eliminated`, …). File-content
   tools map to `fileread`, `Test` → `testrunner`, everything unclaimed falls back to `bash` —
   deterministically. Corpus directory names verified on disk: `ansi bash fileread git glob
   grep sp06 testrunner webfetch` — all groups the table routes into exist.
4. **`eventsFor` tool_input shapes**: as the plan prescribes — `{"file_path":…}` for file tools,
   `{"pattern":…,"path":…}` for Grep/Glob, `{"command":…}` for Bash, `tc.Args` kept when present.
   `eval` field spellings verified against `internal/eval`: `Session.ID/Turns`,
   `Turn.Role/Text/ToolCalls`, `ToolCall.Name/ID/Paths/Args` all match the plan.
5. **`TestPhase1_HotPathBudgetDocumented`** asserts file + content (no skip-if-absent): it
   requires the ADR to exist, to carry a B-A p99 figure and at least one millisecond figure, and
   it *accepts* a local-Windows figure with CI platform figures marked "pending" (CI has not run
   on this branch). Per the brief's TDD ordering for a gate commit, it stays RED until the ADR
   lands.
6. **Report artifact**: `TestPhase1_CanonicalizationGapOnTestOutput` writes `phase1-dedup.json`
   (the plan's name) into its test temp dir on every run, pass or fail; the handed-over
   `phase1-ratios.json` at the worktree root records the same numbers plus diagnostics.

## RED/GREEN history

- **RED (previous run, honest stop)**: gap gate failed at 0.969 vs 1.25 with byte-identical
  re-serving; stopped and reported rather than weakening the threshold. Controller ruling
  followed.
- **GREEN (this run, after the ruling's refresh)**: `go test ./test/e2e/ -run Phase1 -count=1
  -timeout=30m -v` → 7/8 PASS in 76.8 s; all four ratio/gap/sublinear/probe gates pass at the
  design numbers (assertions never edited: `>= 4.0`, `>= ratioOff*1.25`).
- **RED (by design)**: `TestPhase1_HotPathBudgetDocumented` — `docs/adr/0008-observer-l0.md`
  does not exist at 7885650 (nor in the main sp08 worktree). It goes green when Commit 7 writes
  the ADR.
- Determinism confirmed: a second `-count=1` run of the two ratio gates reproduced the numbers
  byte-identically (189.3451; 108.7402 / 27.1304; gap 4.0081) — the refresh is pure in
  (seed, seq) as ruled.

No timing benchmarks were run (another agent is using the machine; ratios are load-insensitive).
`go vet ./test/e2e/` clean; `gofumpt -l` clean on the harness file.

## Surprises / notes for the implementer

- Canon-on is (slightly) *worse* than canon-off on byte-identical replays — visible in the
  read-heavy pair (189.35 on vs 181.31 off is the refreshed-bash residue; the pre-ruling
  measurement had 189.35 vs 192.39). This is expected overhead (deltas/bookkeeping for volatile
  spans that never vary) and is why identical re-serving cannot measure O2.
- `Stats().ToolUses` (400) counts more than the driven PostToolUse events (336): the generator's
  user turns and bookkeeping calls are included by the store's own accounting. The
  bytes-per-call floor is computed over driven calls (336), the stricter reading.
- The cross-check holds: `test/dedup` gets its 1.25× testrunner gain from cross-file
  near-duplicates (go-test-pass vs go-test-rerun) at the canonicalizer level; this harness gets
  its gap from per-serving volatile refresh at the store level. Both agree canon clears the
  floor comfortably, so the observer is not mangling bytes on the way in.

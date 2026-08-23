# SP-07 → V2 checkpoint: reconciliation and carry-forward

> **STATUS — HISTORICAL. This is the SP-07 → V2-VERIFY handoff, written on the SP-07 branch before
> the wave-1 merge, and §C was discharged at that merge.** Read §A, §B and §D as the record of what
> SP-07 reconciled; read §C for the reasoning only. **All three of its ACTION items are CLOSED** —
> each carries its closing evidence inline below. Do not re-do them: re-recording `backward_slice_scores`
> (ACTION 1) would raise `internal/testutil/fixtures_test.go`'s `frozenCount` past 38 and break that
> package's contract test. Anything still open from wave 1 lives in `plans/V2-report.md`, not here.

**Branch:** `feat/sp07-dependence-dag-and-slicing` (merged into `develop` at the wave-1 merge)
**Read before executing §2.7 of `V2-VERIFY-primitives-store-dag-and-baseline.md`.**

Seven rows of §2.7 and one whole-tree row do not reconcile against a literal reading of their
*Expected result* column. Each entry below gives what the row expects, what the branch does, the
evidence, and the resolution.

Two of them are defects in the **check**, not in the code — and one of those, followed literally,
would have you delete a mechanism the architecture mandates. Those are §A. The rest are deviations
forced by fixtures SP-01 froze before SP-07 was written; they need acceptance, not repair (§B).
Items marked **ACTION** are real work someone has to do (§C).

Everything else in §2.7 reconciles. Verified-green record is §D.

---

## A. Rows whose expectation is wrong

### V2-SP07-16 — `grep -rn "t.Skip" internal/dag` cannot return nothing

**Do not satisfy this row by deleting the skip.**

`internal/dag/dagtest/suite.go:124` calls `t.Skip(ruleW1SkipMsg)`. That is Rule W-1's mandatory
mechanism (`00-ARCHITECTURE.md` D9, §5.22): SP-01 ships each conformance suite alongside its
interface, and the suite must skip — with that exact message — when the factory under test is still
a stub. It is what makes same-wave parallel builds safe. `dagtest/suite_test.go:42`
(`TestRunGraphSuite_StubIsSkipped`) is an **SP-01 test that asserts the skip fires**; removing the
skip breaks it and violates §5.22.

The row's *intent* is already satisfied — dag's own conformance run has zero skips, because
`dag.Open` is no longer a stub:

```sh
go test -run Conformance ./internal/dag/ -v | grep -c -- "--- SKIP"   # => 0
```

**Resolution.** Replace the row's grep with the assertion it was reaching for — zero skips in dag's
own conformance run (above), or a grep scoped to call sites outside the SP-01-owned suite:

```sh
grep -rnE "t\.Skip\(" internal/dag --include='*.go' | grep -v 'dagtest/suite\.go'   # => empty
```

Note the bare string `t.Skip` also appears in four **comments** (`bench_test.go:63`,
`dagtest/behaviour.go:237` and `:239`, `dagtest/suite.go:119`), each explaining why a skip is *not*
used at that site. The row's grep matches those too, so it returns hits even with no skip present.

### V2-SP07-20 — `devtool cover` never checks dag's floor, so this row passes vacuously

`tools/devtool/cover.go` `continue`s on every package not owned by SP-01, printing:

```
exempt (stub, owned by SP-07): dag
```

It never tests whether the package is still a stub — the string is emitted regardless.
`plans/OWNERS.tsv:36` (`dag  SP-07  85  AddNode`) declares the floor, so the floor is registered and
unenforced: this row would report success at 0 % coverage just as readily.

Measured directly instead: **90.9 %** of statements.

```sh
go test -coverprofile=/tmp/dag.cover ./internal/dag/     # coverage: 90.9% of statements
```

**ACTION.** SP-02 hit this same hole and closed it for `internal/eval` by adding a `landedSubplans`
list to `cover.go` — see `plans/V2-SP02-handoff.md` §4.1, which records that the list must gain
SP-03…SP-07 as wave 1 merges. **That list does not exist on this branch**: SP-07 was cut from
`develop` before SP-02 merged, so `cover.go` here is still the unconditional version. Once both are
in, the fix for dag is one line:

> add `SP-07` to `landedSubplans` in `tools/devtool/cover.go`

Do this for every wave-1 package as it lands, not just dag — the hole currently exempts `chunk`,
`canon`, `symbols`, `store`, `sketch` and `eval` too, so every wave-1 coverage row in §2.7 and §3 is
vacuous until its subplan is on the list.

---

## B. Deviations forced by SP-01's frozen fixtures

Rule W-2 froze `testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl` before SP-07 was
written, and four of SP-07's own specifications conflict with those bytes. The frozen fixtures won.
`docs/adr/0007-dag-slices-are-scores-not-drop-decisions.md` carries the full reasoning; this section
lists only the consequences visible from §2.7.

### V2-SP07-03 — the NodeID is 378 bytes, not 376

The frozen node line pins the prefix:

```json
{"type":"node","id":"file:src/auth.ts","kind":4,...}
```

`file:` is 5 bytes. SP-07 §D-2's shorter prefixes would not reproduce that fixture, so the long
forms (`file:`, `tooluse:`, `toolresult:`, `assistant:`, `userprompt:`, `symbol:`, `decision:`,
`elimination:`, `segment:`) are contractual. The arithmetic is then:

```
5 ("file:") + 360 (head) + 1 ("~") + 12 (Hash.Short) = 378
```

The row's 376 assumes a 3-byte prefix. `TestNodeIDLongKeyHashSuffix` asserts 378 as a literal.
**Read the row as 378.**

### V2-SP07-12 / -13 — the generation record is `"type":"generation"`, not a `g` record

The rows call it "the `g` header" and expect "a `g` record with `gen:1`". SP-01's frozen line shape
discriminates every line on a `"type"` field, so the four line types are `node`, `edge`, `tombstone`
and `generation`. The semantic content the rows ask for is present:

```json
{"type":"generation","v":1,"gen":1,"ts":1730000000000,"nodes":12,"edges":10}
```

`gen:1` is literally there. Only the discriminator token differs. **Read `g` as
`"type":"generation"`.**

### V2-SP07-02 — kinds round-trip through `String()`/`Parse*`, not `encoding.TextMarshaler`

The row expects "text round-trips for all kinds". They do — `TestNodeKindTextRoundTrip` and
`TestEdgeKindTextRoundTrip` cover every kind — but via `String()` and `ParseNodeKind` /
`ParseEdgeKind`, not `MarshalText` / `UnmarshalText`. Adding the `TextMarshaler` pair was verified
empirically to break both marshalling *and* unmarshalling of the frozen fixtures, because the kinds
are pinned as **integers** (`"kind":4` ⇒ `KindFile`) and `encoding/json` prefers `TextMarshaler`
over the integer form. `doc.go` carries a standing warning against adding it.

The multiplier table, the kind count, and the numbering the row checks are all unaffected. The same
constraint is why `KindInvalid` / `EdgeInvalid` are **last** in their iota blocks rather than first:
prepending a sentinel renumbers every kind and breaks `node_line.jsonl`.

### V2-SP07-11 — `thin-vs-full.json` has no `ns_thin` / `ns_full` fields

The row expects `ns_thin ≤ ns_full` in the published table. The committed golden deliberately
carries **no timings**: a golden containing nanosecond measurements differs on every run, so it
would either be rewritten constantly or compared so loosely it asserts nothing.

The claim is asserted directly in the test instead, per seed, and logged:

```sh
go test -run TestThinVsFullComparison ./internal/dag/ -v
# seed 1: ... (thin 253.31µs, full 720.53µs)   ... thin is 2.5-4x cheaper on every seed
```

**Read the row as: the assertion is in the test, not the golden.** The size/recall/precision fields
the row also checks *are* in the golden and do reproduce within 2 %.

One caveat worth knowing, because it bit this branch: a single slice costs a few hundred
microseconds, which is **not** safely above the clock's granularity on Windows — Go's monotonic
clock falls back to roughly millisecond resolution when no process holds the system timer finer, and
that can change mid-run. The original per-seed assertion compared two single-shot readings 8 µs
apart and failed under `go test ./...` load. It now times a batch of 20 and divides. If you touch
this measurement, keep the batch.

### V2-SP07-05 — the concurrency test runs 250 iterations, not 2 000

`TestConcurrentMutationAndRead` uses 8 writers × 8 readers × **250**. The reasoning is recorded at
the test itself: every reader iteration calls `CrossingEdges` and `NodesAfter` against an index a
concurrent writer has almost certainly dirtied, so each pays a full O(N log N) rebuild — by design,
that is the contended path the test exists to exercise. Total cost therefore grows as
`iterations × N log N` **and** N grows with iterations. At 2 000 the graph reaches ~32 000 nodes and
the run takes minutes; at 250 it takes seconds and interleaves just as densely.

Raising it buys no additional coverage and costs quadratic time. **Read the row as 250**, or move
throughput-under-contention to a benchmark, where it belongs.

---

## C. Carry-forward work items

**All three ACTION items in this section are CLOSED.** They were open on the SP-07 branch; each was
discharged at or before the wave-1 merge, and the closing evidence is recorded under each item. The
text is kept in its original tense so the reasoning stays readable — the **CLOSED** paragraph under
each item is what applies now.

### ACTION 1 — `backward_slice_scores` is still unrecorded — **CLOSED (`14a9668`)**

`testdata/golden/contracts/dag/MANIFEST.json` lists `backward_slice_scores` as `record-by-owner`,
and SP-07 is the owner, but recording it flips `internal/testutil/fixtures_test.go`'s guard from
23 recorded / 5 record-by-owner to 24 / 4 — a **different package's** contract test. SP-07 shipped
the four fixtures its own plan names (`nodeid.json`, `graph-basic.jsonl`, `crossing.json`,
`slice-backward.json`, plus `thin-vs-full.json`) and left this one visible rather than quietly
editing another package's guard mid-wave.

Either record it and update the guard to 24/4 in the same commit, or drop the manifest entry if
`slice-backward.json` is judged to cover it. Do not leave it as-is: a permanently unrecorded
`record-by-owner` entry trains people to ignore the manifest.

> **CLOSED at the wave-1 merge by `14a9668` — "fix(dag,testutil): declare dag's five goldens, drop
> the unrecorded row" — which took the second arm.** The same commit declared the five dag goldens
> SP-07 had shipped as committed files without declaring (`nodeid`, `graph_basic`, `crossing`,
> `slice_backward`, `thin_vs_full`), so `testdata/golden/contracts/dag/MANIFEST.json` now holds seven
> frozen entries — those five plus SP-01's `node_line` and `edge_line` — and **no
> `backward_slice_scores` row**; the behaviour it would have pinned is already covered by
> `slice_backward` and `TestThinDropsControlOnly`. The guard in `internal/testutil/fixtures_test.go`
> moved in the same commit and now reads `require.Equal(t, 38, frozenCount)` with `pendingCount` at 4.
> **Do not record `backward_slice_scores`:** doing so would raise `frozenCount` past 38 and break
> `internal/testutil`'s contract test. Verify with
> `go test ./internal/testutil/ -run TestContractFixture_EveryManifestIsReadable`.

### ACTION 2 — add SP-07 to `landedSubplans` in `tools/devtool/cover.go` — **CLOSED (V2-MERGE-14)**

See V2-SP07-20 above. This converges with `plans/V2-SP02-handoff.md` §4.1, which raised the same
item first; treat that as the authoritative version and add SP-07 to the list it describes.

> **CLOSED at the wave-1 merge, row V2-MERGE-14 of `plans/V2-report.md`.** `landedSubplans` exists in
> `tools/devtool/cover.go` and holds **SP-01 … SP-07**, all seven `true`; `probeBlind` is still
> exactly `{scheduler, grammar, contract, redact}`. SP-02's and SP-04's branches had each rewritten
> the map independently, which is why the merge had to take neither side whole. The transcription is
> pinned by `TestLandedSubplansMatchesTheBranch`, rewritten in the same row to fail loudly on drift.
> Every wave-1 coverage floor is live as a result — dag measures 90.7 % against its 85 % floor.

### ACTION 3 — V2-ALL-04 cannot run in this environment — **CLOSED (superseded)**

The row requires pushing `verify/v2` and confirming the CI matrix. **This repository has no git
remote**, so there is nothing to push to and no CI to observe. `ci-local` is the closest available
equivalent and is green end to end (§D). Either add a remote before the checkpoint or record the row
as environment-blocked — but do not mark it passed on the strength of `ci-local`, which does not run
the cross-OS matrix, `crossbuild`, `security`, or `docs`.

> **CLOSED — superseded. The premise no longer holds: the remote exists and CI has run.** `git remote -v`
> reports `origin https://github.com/AidanHT/qompack.git`, and the cross-OS matrix has executed against
> the merged tree. `plans/V2-report.md` §16 ("What the first CI runs found") is the authoritative
> record — including §16.1's defects, which are exactly the class `ci-local` could not have surfaced,
> and §16.4's remaining open items. Read §16 rather than treating V2-ALL-04 as environment-blocked.
> One related item did **not** close this way and is carried in `V2-report.md`, not here: making
> `replay-gate` a *required* check is hosted branch protection, a repository setting rather than a
> commit.

### INHERIT — the DAG is not acyclic, and SP-09 must tolerate it

D-7 forbids one specific cycle (an assistant consuming the result of the tool use it emitted). The
**whole graph is not a DAG and cannot be**: the ordinary Read-then-Edit pattern closes a legitimate
loop, because §8.1 item 4 directs a shared-file edge *into* a tool use that consumed a file and *out
of* one that produced it.

```
tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1
```

Every edge there is individually correct. `TestReadThenWriteClosesALegitimateCycle` pins it so it
cannot be assumed away. Slicing tolerates it by construction — scores strictly decrease along any
path, each node finalizes once, and the `minScore` floor bounds the walk.

**SP-09's negative-knowledge detector scans this same graph for the test-fail → revert →
different-approach pattern and inherits the same obligation.** A naïve recursive descent over these
edges will not terminate. This is stated in ADR 0007; it is repeated here because SP-09 is in a
later wave and will not otherwise see it.

### NOTE — wall-clock gates are scaled under instrumentation

`TestSliceLatencyBudget` and `TestCrossingLatencyBudget` assert §6.4's and §8.4's budgets on every
`go test`, including `ci-local`'s uninstrumented `test` step — so the **real** budget is enforced in
CI. They scale their ceiling when the binary carries instrumentation, because the same walk measures:

| Build | Backward slice | Inflation |
|---|---|---|
| uninstrumented | 0.40 ms | — |
| `-covermode=atomic` (`devtool cover`) | 0.81–1.18 ms | ~3× |
| `-race` (`devtool test-race`) | 2.02 ms | ~5× |
| both | 9.23 ms | ~23× |

Factors are 4× for coverage and 8× for race, multiplied when both apply, each above its measured
inflation. They scale rather than skip: Rule W-1 bans `t.Skip`, and a gate that evaporates under
`-race` is one nobody notices has stopped running. All four modes still fail on the regression class
the gates exist to catch (the `orderByScore` defect cost 5.4×).

If a slower CI box misses the uninstrumented 1 ms budget, that is a **real** signal about that host,
not a reason to raise `sliceBudget`.

---

## D. Verified green on this branch

| Gate | Command | Result |
|---|---|---|
| V2-SP07-01 | `go test -race ./internal/dag/...` | ok |
| V2-ALL-01 | `go test -race ./...` | ok, whole module |
| V2-ALL-03 | `go run ./tools/devtool ci-local` | exit 0, all 8 steps |
| V2-ALL-05 | `Qompack.md` untouched | empty diff |
| V2-SP07-19 | `devtool lint` (`importgraph`) | `internal/dag` imports only `core`, `paths`, `config`, `logging` |
| V2-SP07-20 | measured, not via `cover` | 90.9 % ≥ 85 % |
| §2.7 `-run` patterns | every pattern matches ≥ 1 test | see note below |

**One `-run` pattern was dead and has been fixed.** V2-SP07-10 runs
`-run 'TestSliceGolden|TestCrossingEdgesGolden'`, and the backward-slice golden test was named
`TestBackwardSliceGolden` — which that pattern does **not** match, so the row would have verified
`crossing.json` only and reported `ok` while silently never touching `slice-backward.json`. Renamed
to `TestSliceGoldenBackward`, which both that pattern and V2-SP07-08's `TestSlice` reach.

Worth generalising: `go test -run` prints `ok` when a pattern matches nothing. Any §2.7 row whose
pattern drifts from the test names becomes a silent pass, not a failure. All SP-07 patterns were
checked against `go test -list` for this reason; the other subplans' rows have not been.

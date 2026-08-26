# SP-08 — carried defects

One thing SP-08 knowingly ships. It has a row in `plans/CARRIED-DEFECTS.tsv`, and
`test/guards/carrieddefects_test.go` will not let `V3-VERIFY` write `plans/V3-report.md` while it is
still `open`.

**A note on this file's name.** It is `V2-SP-08-…` rather than `V3-SP-08-…` because
`test/guards/carrieddefects_test.go` derives the one document a row's section may live in from the
row id and the single constant `carriedDefectsWave`, which is `"V2"` — that guard's own comment
says "a later wave keeping its own manifest changes this one constant", and no later wave has kept
one. `plans/CARRIED-DEFECTS.tsv` is still the only manifest, and it already carries SP-02, SP-04,
SP-05 and SP-06 rows alongside this one. Renaming the scheme is a change to a guard SP-08 does not
own; when a wave-3 manifest is cut, this file moves with it.

**This is not a reason to hold the merge.** The cost is inside `store.PutBytes`, it is bounded, and
it is invisible to the user: the hook path acknowledges from the WAL before L0 processing runs, so a
B-C overrun delays index freshness and blocks nothing. Writing it down is the alternative to hoping
someone re-derives it at V3-VERIFY.

Fixing the row means setting its status to `fixed`; deciding not to means setting it to
`deferred:<a later checkpoint>` and adding a paragraph here saying why. Doing neither fails the gate.

---

## SP08-D1 — `OnToolUse` over a large tool result breaches budget B-C

**Symptom.** `OnToolUse` on a 256 KB `go test` result costs far more than budget **B-C**
(`l0_process`, p99 < 50 ms, soft, `Gated: false`) allows, and does so on the realistic case as well
as the pessimal one. On a 64 KB source read it stays inside the budget on every fixture.

The number moves with one variable — how much of the result differs from the previous call — so the
two shipping benchmarks each measure three points on that axis rather than one. `objects` is the
count of chunks the store actually holds after the run, and is what proves a fixture is doing what
its name says.

Measured on a quiet machine (Intel Core Ultra 7 155H, windows/amd64),
`-benchtime 200x -count=5`, against a real `store.Open` and a real `dag.Open` on a temp root. p50 and
p99 are read out of the observer's own `observer.tooluse` histogram, which is the instrument B-C is
evaluated against in production; the ns/op column is Go's mean.

| benchmark | fixture | ns/op | objects | p50 (ms) | p99 (ms) | B-C |
|---|---|---|---|---|---|---|
| `BenchmarkOnToolUse_FileRead64KB` | `Deduped` | 11.5–13.4 ms | 5 | 12.3–14.3 | 14.3–18.4 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `Delta` | 15.8–16.7 ms | 204 | 15.4–16.4 | 24.6–45.1 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `AllNovel` | 23.7–24.6 ms | 905 | 24.6 | 36.9–45.1 | inside |
| `BenchmarkOnToolUse_TestOutput256KB` | `Deduped` | 39.9–65.6 ms | 34 | 41.0–61.4 | 53.3–131.1 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `Delta` | 39.3–55.6 ms | 236 | 41.0–49.2 | 53.3–114.7 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `AllNovel` | 163.6–224.1 ms | 12209 | 147.5–213.0 | 262.1–655.4 | **breach** |

The three fixtures are:

- **`Deduped`** — one identical payload every call. It dedups completely from the second call on, so
  the store never writes a chunk (`objects` stays at the first call's count). It is the optimistic
  end and is kept only for comparison; an earlier revision of these benchmarks measured only this
  and reported 2–18% headroom, which is what the review caught.
- **`Delta`** — one line differs per call: Qompack.md §8.1's own near-duplicate case, *"same test
  suite, one new failure"*. This is the realistic shape and **the fixture the acceptance criterion
  below is stated against.**
- **`AllNovel`** — a marker every 512 bytes, below the store's 1 KB minimum FastCDC chunk, so every
  chunk of every call is novel. The pessimal end.

**Diagnosis.** Three costs stack, and only the third is new information.

1. Canonicalization, roughly 25–30 ms of the 256 KB fixed cost. This is SP-04's per-rule prefilter
   scan, already recorded as **SP04-D5**: cost is linear in the rule count, and 256 KB is 2.5× the
   100 KB that row's `BenchmarkRun_Bash100KB` is stated against.
2. MinHash, roughly 10–15 ms, at the configured 128 permutations over 256 KB of shingles.
3. **The novel-chunk object-write path inside `store.PutBytes`** — zstd compression plus one file
   write per novel chunk. This is what separates the three fixtures: 34 objects and ~45 ms against
   12 209 objects and ~180 ms for the same payload size. It is amplified on Windows, where per-file
   create/write/close is materially more expensive than on Linux, so the CI reference platform is
   expected to read better than the numbers above.

This row is downstream of **SP06-D2**, which already records that `PutBytes`'s own cold and warm
budgets (3 ms / 400 us) are 9x and 19x over on Windows and have never been measured on the reference
platform. SP08-D1 is what that costs once a hook calls `PutBytes` on a real tool result: the same
mechanics, seen from the caller, at a payload size SP-06's own benchmarks do not cover. V3-VERIFY
should dispose of the two together, and a fix to SP06-D2 may close this row without SP-08 changing
a line.

Nothing in the observer's own bookkeeping is on this scale. Its measurable share is about 7 ms of
repeated `responseText` decoding — the payload is unwrapped four times per event, once at step 3 and
three more times inside the §5.21 pure functions — and reducing that means changing the shape of
`ExtractSignals` and `ExtractTestOutcome`, which are frozen by §5.21.

**Evidence.** `BenchmarkOnToolUse_TestOutput256KB` (and `BenchmarkOnToolUse_FileRead64KB` for the
inside-budget half), in `internal/observer/tooluse_test.go`. Reproduce with:

```
go test -bench BenchmarkOnToolUse -benchtime 200x -count=5 -timeout=30m ./internal/observer/
```

Neither benchmark asserts a latency. B-C is soft and ungated and these numbers move with the host, so
a benchmark that failed the build on one would make every unrelated change look like a regression.
That is precisely why the breach is recorded here instead.

**Why it is deferred rather than fixed.** The dominant cost is `store.PutBytes`'s object-write path,
which is SP-06's mechanics and outside SP-08's scope: the observer never chunks, compresses or writes
an object by hand (resolved decision 1), it hands bytes to the store and the store decides. Reaching
into that from here would be exactly the cross-subplan edit Rule W-1 forbids.

It is also not user-visible. §8.1's own instruction for a budget overrun is to *"degrade to async
queue-and-drain rather than blocking"*, and that is already the architecture: the hook acknowledges
from the write-ahead log before L0 processing runs, so a B-C overrun delays index freshness and
blocks nothing in the session. B-C is `Gated: false` in `internal/obs/budgets.go` for that reason.

No threshold is edited. Moving a §11.3 budget row is a sign-off SP-08 may not give itself.

**Acceptance (V3-VERIFY).** Either of:

1. `BenchmarkOnToolUse_TestOutput256KB/Delta` reports p99 < 50 ms on the CI reference platform on a
   quiet host, at `-benchtime 200x -count=5`; or
2. an explicit §11.3 sign-off that records the measured figures and either moves the B-C row or
   re-defers this one to a later checkpoint with a reason.

Re-measure the whole table above, not just the `Delta` row: `Deduped` breaching at all on the run
recorded here — it did not on an earlier, quieter pass — is the sign that the 256 KB fixture sits
close enough to the budget that host noise crosses it, which is itself information for the decision.

Two candidate fixes belong to that conversation rather than to this row: pushing the object write
off the ingest path (the queue-and-drain §8.1 names, taken one level deeper), and reconsidering
whether a 256 KB tool result is the payload the budget should be stated against — the plugin's own
`runtime.hotPath.maxPayloadBytes` default is 1 MiB, four times larger again.

---

## V3-VERIFY dispositions (2026-08-26)

**SP08-D1 -> deferred:V4-VERIFY, adjudicated with SP06-D2.** V3 isolated re-measurement
(BenchmarkOnToolUse, -count=3, quiet host): 256 KB p99 90-98 ms (Deduped/Delta) and 164-197 ms
(AllNovel) against B-C's soft 50 ms; 64 KB Deduped p50 9.2-10.2 ms. B-C is Reported, never gated
(section 2.4), the daemon's response to overrun is sampling + backpressure rather than blocking,
and the cost is dominated by store.PutBytes's per-novel-chunk object-write path - the same path
SP06-D2 now measures over budget on Linux as well as Windows. The two rows are one defect seen
from two layers and travel together to V4-VERIFY, where the checkpointer's encode path (the other
large PutBytes caller) lands and the budget-vs-implementation decision has its full evidence.

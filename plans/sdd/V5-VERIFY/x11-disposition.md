# V5-VERIFY §4.11 — `TestV5_SegmentBloomNarrowsRecall` disposition

**Identifier (retained):** `TestV5_SegmentBloomNarrowsRecall`

**Current criterion (V5-VERIFY §4, authoritative):** SP-20/SP-16 filter generation/coverage:
exact positives, stale/incomplete negatives bypass.

**Disposition:** authored, with one unverified remainder (below).

## Level and file

`test/integration/v5_x11_test.go` (in-process, package `integration`).

The historical row was an e2e seam: `store.SegmentsMayContain` answering a `tools/call recall`
question without object reads. On this tree that seam does not exist. The producer SP-16 shipped is
`internal/store/segmentfilter.go` (`BuildSegmentFilter`, `Lookup`, `WriteSegmentFilter`,
`ReadSegmentFilter`, `SegmentFilterPublisher.PublishFilter`) plus `SweepSegmentFilters` in
`internal/store/phase7maint.go`. It has no consumer outside the store: `recall`'s handler calls
`store.Search`, which never consults `Segment.BloomRef`; no daemon or scheduler task builds a
filter; and `runtime.phase7.filters.segmentBloom` is a `config.MigrationGates()` entry with
`Passed=false`, so `config.Validate` refuses it. An e2e test could therefore only observe the
absence of the feature. The criterion — generation, coverage, exact positives, bypassed negatives —
is fully assertable in-process over the real store, so the test lives in `test/integration`.

## Test names and what each asserts

`TestV5_SegmentBloomNarrowsRecall` (one top-level function; the arms are sequential sections over
one real store, plus one subtest):

1. **Generation from the exact index.** 12 closed segments × 50 tool uses through `store.Open`,
   the real `SegmentLog` and `RecordToolUse`; one distinctive path recorded once, in segment 7.
   Each segment's filter is built from keys read back from `ToolUsesByPath` bucketed by the
   segment's turn range (the index says what a segment holds, not the test). Asserts
   `Coverage.Complete`, `Incomplete==""`, `Keys` (10, or 11 for segment 7), and the segment id,
   generation and turn range published with the bits.
2. **Publication is durable.** `WriteSegmentFilter` then `PublishFilter`; the store is closed and
   reopened; every `Segment.BloomRef` equals `SegmentFilterRef(id)`, the file exists, and
   `ReadSegmentFilter` round-trips coverage with the bits.
3. **Narrowing.** Looking the distinctive path up in all 12 on-disk filters yields no bypass,
   `FilterMaybe` for segment 7, at most one extra false-positive segment (historical tolerance),
   and `FilterMiss` for at least 10. Observed: maybe=[7], miss=[1 2 3 4 5 6 8 9 10 11 12]. A path
   every segment holds is `FilterMaybe` in all 12.
4. **Exact positives.** Every `FilterMaybe` segment is confirmed against `ToolUsesByPath` within
   its turn range; only segment 7 survives, with exactly the recorded tool-use id. `store.Search`
   (the producer behind `recall`) returns exactly that hit.
5. **A positive is never a result.** Segment 3's filter is republished from a superset key source
   that includes the distinctive path. It reads back `FilterMaybe`, `SkipsSegment()` is false, the
   exact index for segment 3 confirms nothing, and `Search` is unchanged.
6. **Stale negatives bypass.** The same lookup at `generation+1` yields zero `FilterMiss`; segment
   7 is still `FilterMaybe`; every segment is bypass-or-maybe.
7. **Incomplete negatives bypass.** Segment 7's filter is rebuilt from a key source that errors
   after one common key (before the distinctive path), published, and read back:
   `Complete=false`, `Incomplete=source_error`, `Keys=1`; the distinctive path is `FilterBypass`
   (never `FilterMiss` — that would be a false negative about the one segment that holds it); the
   common key is still `FilterMaybe`; `Search` still finds the hit.
8. **Corrupt file is no filter.** `seg-0007.bloom` truncated to 8 bytes: `ReadSegmentFilter`
   returns `sketch.ErrMalformed` and nil; a nil filter's `Lookup` is `FilterBypass`; `Search` still
   finds the hit.
9. **Maintenance keeps what the log names.** `SweepSegmentFilters` with the live `BloomRef` set:
   `Found=12`, `Kept=12`, nothing removed.
10. **Negative control** (below), then `p.AssertAppendOnly(t)`.
11. Subtest `switch is recorded as disabled, never as passed`: a project config file setting
    `runtime.phase7.filters.segmentBloom: true` resolves to `false` through `testutil.NewProject`
    and through `config.Load` (provenance `OriginDefault`, a warning keyed on the switch);
    `config.MigrationGates()` lists the key with `Passed=false`, owner `SP-16`, gate naming
    `M6-G16-E`; `Validate()` on defaults-plus-switch reports exactly that key with "has not passed
    in this build".

## Negative control and how it was proven

Real runtime switch, no source edit: `store.SweepSegmentFilters(ctx, root, map[string]bool{})` —
the producer's own maintenance entry point with an empty keep set — removes all 12 filter files
while the append-only segment log still names them. The test then asserts `len(Removed)==12`,
every `BloomRef` still non-empty, `ReadSegmentFilter` returns `core.ErrNotFound` for every
segment (a dangling reference is defended as "no filter"), the same lookup that skipped 11 segments
in arm 3 now yields zero `FilterMiss`, zero `FilterMaybe` and 12 `FilterBypass`, and `Search`
still returns the hit. If the narrowing in arm 3 were coming from anywhere but the filters on
disk, a miss would survive here and the control would fail.

Proof it bites: arm 3 requires ≥10 misses and the control requires 0 misses over the same
segments, same key, same generation; the only difference between the two is the files the switch
removed. Both ran green twice (`-count=1`).

## Old-to-new assertion map

| Historical expectation (§4.11 at HEAD 7f92af5) | Disposition |
|---|---|
| 12 closed segments × 50 tool uses; one distinctive path only in segment 7 | **kept** (arm 1; through the real store and segment log) |
| `store.SegmentsMayContain(path)` returns `[7]` (≤1 extra false positive) | **corrected**: no such method exists; the per-segment `Lookup` over on-disk filters yields maybe=[7] with ≤1 extra allowed and ≥10 misses (arm 3) |
| zero `Open`/`OpenSpan`/`GetChunk`/`GetRoot` calls during the filter answer | **retired**: no instrumented store or object-read counter exists on this tree, and `ReadSegmentFilter`/`Lookup` take no `store.Store` at all — they read only `sketches/seg-NNNN.bloom`, so object reads are impossible by construction rather than counted |
| `tools/call recall {"query":"path:<path>"}` returns the hit | **corrected**: `recall` does not consult filters on this tree; the row asserts the exact producer behind it, `store.Search{Path}`, returns exactly the recorded tool use (arms 4, 5, 7, 8, control) |
| truncating segment 7's bloom file to 8 bytes makes `SegmentMayContain` conservative `(true, err)` | **corrected**: `ReadSegmentFilter` returns `sketch.ErrMalformed` + nil, and a nil filter's `Lookup` is `FilterBypass` (arm 8) — same conservative direction, current API |
| emits exactly one `Loud` for that segment id | **retired**: the reader has no logging seam and emits nothing; the error is returned to the caller. No producer for this clause exists |
| `recall` still returns the same hit after corruption — the filter is never a source of truth | **kept** (arms 7, 8 and the control, via `store.Search`) |
| (new, from the current criterion) coverage/generation published atomically with the index; stale and incomplete negatives bypass; exact positives | **added** (arms 1, 2, 5, 6, 7) |
| (new, plan rule for disabled features) the switch is recorded as disabled, never passed | **added** (subtest) |

## Unverified remainder

- **End-to-end narrowing of `recall`:** no consumer on this tree reads `Segment.BloomRef` during
  retrieval, so "the filter narrows recall" is asserted at the store seam (the filters narrow the
  segment set; `Search` supplies the exact hit) and NOT as a change in `recall`'s I/O. Recorded as
  unverified; owner SP-16 (M6-G16-E) / SP-13 when a consumer lands.
- **Segment generation:** nothing on this tree rewrites a closed segment, so the generation is
  caller-supplied (`x11Generation`); staleness is exercised by passing `generation+1` to `Lookup`,
  not by a real rewrite.
- **Over-capacity and cancellation incompleteness** are covered by the unit tests in
  `internal/store/segmentfilter_test.go`, not re-asserted here.
- `runtime.phase7.filters.segmentBloom` remains **disabled** (gate M6-G16-E not passed). This row
  records that; it does not pass it.

## Run command and result

```
cd <worktree> && go test -list '^TestV5_SegmentBloomNarrowsRecall$' ./test/integration
  → TestV5_SegmentBloomNarrowsRecall
cd <worktree> && go test -count=1 -run '^TestV5_SegmentBloomNarrowsRecall$' ./test/integration
  → ok  github.com/qompack/qompack/test/integration  37.795s   (run 1, -v; includes compile)
  → ok  github.com/qompack/qompack/test/integration  13.951s   (run 2)
```

`gofmt -l ./test ./internal` empty; `go vet ./test/integration` clean;
`go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` PASS.
No production change. Base: `verify/v5 @ 87c0c1d`.

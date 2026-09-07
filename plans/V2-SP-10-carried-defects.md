# SP-10 — carried defects

One row. See `plans/CARRIED-DEFECTS.tsv` for the machine-readable form and
`test/guards/carrieddefects_test.go` for what makes it load-bearing.

The `V2-` prefix in this file's name is the guard's hardwired wave string, not a claim that SP-10
belongs to wave 1; `plans/V2-SP-08-carried-defects.md` carries a V3 subplan's rows under the same
convention.

---

## SP10-D1 — `Finalize` is 5.4× over its 50 ms budget on Windows, and the reference platform has never been measured

**Status:** open, owned by V4-VERIFY, to be adjudicated alongside SP06-D2 and SP08-D1, which are the same class.

### What the criterion says

The subplan's exit criteria (plan line 1570) require `BenchmarkFinalize` mean **< 50 ms**, and
separately that no micro-benchmark regress more than 10% against `testdata/bench-baseline.txt`.

### What was measured

Quiet machine, Intel Core Ultra 7 155H, `windows/amd64`, over the §13 fixture (40 segments, 64
decisions, 200 file pointers, 200 tool pointers). The `-count=6` column is the representative one and
is what `testdata/bench-baseline.txt` now records; the `-benchtime=20x` column is a colder run kept
because it is what the CPU profile below was taken from.

| benchmark | `-count=6` | `-benchtime=20x` | budget | verdict |
|---|---|---|---|---|
| `BenchmarkExtractDecisions` | 11.3–12.3 ms/op | 11.47 ms/op | < 20 ms | pass |
| `BenchmarkAdvanceSegment` | 16.5–18.9 ms/op | 10.49 ms/op | < 25 ms | pass |
| **`BenchmarkFinalize`** | **264–284 ms/op** | 471.09 ms/op | **< 50 ms** | **fail, 5.4×** |
| `BenchmarkTruncate` | 1.54–1.89 ms/op | 1.92 ms/op | — | — |
| `BenchmarkStripInjections` | 0.36–0.46 ms/op | 0.33 ms/op | — | — |

### The benchmark is sound; the number is real

Setup runs under `b.StopTimer()`, both the store and the DAG are flushed before `b.StartTimer()` so
Windows cannot bill the setup's write-back to the first fsync inside the timed region, and teardown
is excluded as well. The number is `Finalize` alone.

### Where the time goes

CPU profile, filtered to the `Finalize` subtree:

```
Finalize subtree total          10.98s / 76.85s
  paths.Norm                     9.69s      <- 88% of Finalize
    path/filepath.toNorm         5.95s
    syscall.FindFirstFile        4.26s
    path/filepath.walkSymlinks   3.71s
    syscall.GetFileAttributesEx  4.51s
```

`ValidatePointers` calls `paths.Norm(root, ptr.Path)` once per file pointer — 200 in this fixture.
On Windows `filepath.EvalSymlinks` runs `toNorm`, which issues a **`FindFirstFile` per path
component** to recover the canonical casing.

### Why this is carried rather than fixed

`path/filepath.toNorm` is defined only in `path/filepath/path_windows.go`. It does not exist on the
Linux path at all, so the single largest term — 5.95s of the 9.69s — is provably a Windows-only
cost, and `GetFileAttributesEx` is a far more expensive way to stat a file than Linux's `lstat`.
The reference platform is therefore very likely to land somewhere else entirely.

It has not been measured. The only WSL distribution installed on the development host is
`docker-desktop`, whose filesystem is read-only, and the Docker daemon is not running; both are user
actions to change. **No Linux number is quoted here because none was taken.**

Making `Norm` cheaper means either changing SP-01's `internal/paths` or restructuring
`ValidatePointers` around a memoised path prefix — on the function whose entire job is to refuse a
path that escapes the project root. That is not a change to invent without a reviewer, and this
subplan's own final review contains two separate cases of a plausible-looking change to a
security-adjacent function being wrong.

### Precedent

`SP06-D2` records that "the `PutBytes` cold and warm budgets (3 ms / 400 µs) are 9× and 19× over on
Windows and have never been measured on the reference platform", deferred to V4-VERIFY and
adjudicated together with `SP08-D1`. This row is the same shape, at the same checkpoint, and should
be adjudicated with them.

### What resolving it looks like

Measure `BenchmarkFinalize` on the reference platform. Then either:

- it is inside 50 ms there, and this row closes as a Windows-only characteristic that the budget was
  never written against — in which case say so in the budget's own text, because the next person to
  run it on Windows will file this again; or
- it is over there too, and the fix is a real one: batch or memoise the pointer-path resolution in
  `ValidatePointers`, with the escape check preserved exactly and reviewed on its own terms.

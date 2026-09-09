# SP-10 — carried defects

One row. See `plans/CARRIED-DEFECTS.tsv` for the machine-readable form and
`test/guards/carrieddefects_test.go` for what makes it load-bearing.

The `V2-` prefix in this file's name is the guard's hardwired wave string, not a claim that SP-10
belongs to wave 1; `plans/V2-SP-08-carried-defects.md` carries a V3 subplan's rows under the same
convention.

---

## SP10-D1 — `Finalize` is 5.4× over its 50 ms budget on Windows, and the reference platform has never been measured

**Status:** open, owned by V4-VERIFY, to be adjudicated alongside SP06-D2 and SP08-D1, which are the same class. The `paths.Norm` hot spot named in the row was confirmed and removed at V5-VERIFY (see the findings below); the row stays open because Windows is still over the 50 ms criterion after the fix and the reference platform is still unmeasured.

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

### V5-VERIFY findings (2026-09-08, branch `v5/sp10-d1`)

Artifacts named `scratchpad/sp10d1/<file>` below live in the V5-VERIFY session scratchpad,
`C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/00571f79-eff6-41ba-80f7-98a4f4f99619/scratchpad/sp10d1/`
(session-local; copy them before the session is cleaned if they need to outlive it).

**The hot spot was real.** A fresh CPU profile of `BenchmarkFinalize` on the base (`verify/v5` @
87c0c1d, `-benchtime=20x -cpuprofile`, focused on `FileWriter.Finalize`) put `paths.Norm` at 6.84 s
of Finalize's 8.64 s — 79% on this run, 88% on the original — all of it `filepath.EvalSymlinks`:
`toNorm` 4.46 s (`FindFirstFile` 3.15 s), `walkSymlinks` 2.37 s. `ValidatePointers` handed every
one of the 200 file pointers to `Norm`, and `Norm` resolves its WHOLE argument, so every pointer
re-resolved every component of the project root (twice: once for the root, once for the pointer),
one `Lstat` plus one `FindFirstFile` per component on Windows. The per-pointer cost was therefore
proportional to how deep the project root sits on disk, which is not a property of the pointer.

**Characterization test** — `TestValidatePointersCostDoesNotGrowWithRootDepth`
(`internal/checkpoint/validate_cost_test.go`, commit 3ad91c2). It pins the cost model rather than
the clock: the same fifty pointers validated under a root eight directories deeper must not
allocate more per pointer (`testing.AllocsPerRun`, which co-load cannot inflate — ADR 0010). The
two roots are held to the same path length so `filepath.Join`'s length-dependent allocation count
cannot masquerade as resolution cost.

- RED on the base: `validating 50 pointers under a root 8 directories deeper allocated 112.0 more
  per pointer (13534 vs 7934 per run)`, exit 1 — `scratchpad/sp10d1/characterization-red-before.txt`
  (the first run, before the fix was tightened, recorded 114.0: `characterization-before.txt`).
- GREEN after the fix: `checkpoint-after2-full.txt` (the whole package, `-count=1 -v`: 277 pass, 2
  skip, exit 0).
- Three companion pins say what the fix may not change: `TestValidatePointersFollowsADirectorySymlinkLikeNorm`
  and `TestValidatePointersFollowsAFileSymlinkLikeNorm` (a symlinked component resolves to the
  target's index entry exactly as `Norm` resolved it; both skip on this host, where symlink creation
  needs a privilege the account lacks, and run on Linux CI) and
  `TestValidatePointersMatchesTheIndexTheWayTheFilesystemMatchesNames` (a wrong-case pointer matches
  the index where the filesystem matches names, `paths.DefaultFold`, and is missing where it does not).

Command: `go test ./internal/checkpoint -run '^TestValidatePointersCostDoesNotGrowWithRootDepth$' -count=1 -v`

**Fix** — commit b1a8318, `internal/checkpoint/validate.go`, a `pointerResolver` local to one
`ValidatePointers` call. A pointer that is relative, lexically inside the root, and whose every
component below the root is a plain directory or a plain regular file is answered lexically, with
one `Lstat` per DISTINCT directory (cached for the call) plus the `Lstat` the check needs anyway.
For such a path `Norm`'s answer can differ from the lexical relative form only in the per-component
spelling Windows' `toNorm` recovers, and neither consumer of the answer can see that: `Lstat`
matches names case-insensitively exactly where `toNorm` changes anything, and the index lookup goes
through `paths.Key`, which folds case on exactly those platforms. Every pointer the writer mints is
already a `Norm` output, for which the two are identical byte for byte.

Everything else still goes through `paths.Norm` unchanged, syscall for syscall: absolute or
volume-qualified paths, lexical escapes, the root itself, symlinks and reparse points (a directory
must be exactly `fs.ModeDir` — a junction is `ModeDir|ModeIrregular` on Go 1.23+ and is sent to
`Norm`), an `Lstat` that fails for any reason other than absence, 8.3 aliases (`~`) and components
ending in `.` or a space (which Win32 strips, so `FindFirstFile` would have respelled them). The
escape verdict is never decided by the fast path: a lexical escape is handed to `Norm`, whose
answer is the one the drop is written from.

**No output byte changed.** The checkpoint package's golden tests (`TestGoldenCheckpointRoundTrip`,
`TestMinimalGoldenIsReproducible`, `TestGoldenTruncationFixtures`, `TestCheckpointGolden_*`) pass
unchanged on the fixed code, and a throwaway probe (not committed) that sealed a draft through
`Finalize` and then ran `ValidatePointers` over 26 mixed inputs — wrong case, `./`-relative,
trailing slash, directories, missing files, missing directories, escapes, `..`, `.`, empty, absolute,
volume-relative, leading backslash, an 8.3-shaped name, backslash separators, `.git/HEAD`, and a
planted `v2.index` with clean, dirty and wrongly-cased tracked files — produced the same Finalize
`sha256` (`40ff0d0b…8014`, 1197 bytes) and the same 44 drop lines before and after:
`scratchpad/sp10d1/probe-before.lines` vs `probe-after2.norm` (`diff` empty; the one normalized
token is `t.TempDir()`'s random suffix inside the absolute-path input).

**Measurements** — SHARED HOST, other agents' e2e and integration suites running throughout;
wall-clock numbers are the host's, the allocation columns are the code's. Same fixture, same
machine (Core Ultra 7 155H, windows/amd64), before/after binaries run back to back:

| run | before (87c0c1d) | after (b1a8318) |
|---|---|---|
| `-benchtime=20x`, lighter load (`bench-before.txt`, `bench-after-20x.txt`) | 452 ms/op, 43423 allocs/op, 5.59 MB/op | 111 ms/op, 17026 allocs/op, 4.04 MB/op |
| `-count=6`, heavy load (`bench2-before-count6.txt`, `bench2-after-count6.txt`) | 524, 524, 552, 531, 4586, 887 ms/op; 43.4k allocs/op | 114, 177, 103, 89, 155, 262 ms/op; 17.2k allocs/op |
| ValidatePointers inside the Finalize profile (`before-top.txt`, `after-top.txt`) | 7.02 s of 8.64 s | 0.19 s of 2.11 s |

Command: `go test ./internal/checkpoint -run '^$' -bench '^BenchmarkFinalize$' -benchmem -count=6`

After the fix the remaining Finalize time on Windows is syscall-bound elsewhere — `Begin`
(opening the successor draft), `keepResolvableTools`' `store.Has` stat per tool pointer, and the
`persist`/`CreateNew` fsyncs — none of it in `Norm`.

**Disposition.** Windows is still over the 50 ms criterion after the fix on this host (best round
89 ms/op under load; the quiet-machine number is the coordinator's to take), and the reference
platform remains unmeasured, so the row stays `open` with `BenchmarkFinalize` as its evidence —
the second bullet of "What resolving it looks like" is now done, the first is not. The row's
summary still quotes the 264–284 ms pre-fix figure and the 88% share; both are historical after
b1a8318. `testdata/bench-baseline.txt` was NOT regenerated (its rule, and this branch's).

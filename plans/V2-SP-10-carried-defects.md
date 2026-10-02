# SP-10 — carried defects

One row. See `plans/CARRIED-DEFECTS.tsv` for the machine-readable form and
`test/guards/carrieddefects_test.go` for what makes it load-bearing.

The `V2-` prefix in this file's name is the guard's hardwired wave string, not a claim that SP-10
belongs to wave 1; `plans/V2-SP-08-carried-defects.md` carries a V3 subplan's rows under the same
convention.

---

## SP10-D1 — `Finalize` is 5.4× over its 50 ms budget on Windows, and the reference platform has never been measured

**Status:** `deferred:V6-VERIFY` since V5-VERIFY (2026-09-10); it was open, owned by V4-VERIFY, to be adjudicated alongside SP06-D2 and SP08-D1, which are the same class. The `paths.Norm` hot spot named in the row was confirmed and removed at V5-VERIFY (see the findings below). The row stays unresolved because Windows meets the 50 ms criterion only at high turbo clocks and the reference platform is still unmeasured; the measurements are in "V5-VERIFY disposition" at the end.

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
(`internal/checkpoint/validate_cost_test.go`, commit 3ad91c2 on `v5/sp10-d1`, landed as `e476373`). It pins the cost model rather than
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

**Fix** — commit b1a8318 (landed on verify/v5 as `72f3226`), `internal/checkpoint/validate.go`, a `pointerResolver` local to one
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

| run | before (87c0c1d) | after (b1a8318, landed as 72f3226) |
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
b1a8318 (landed as `72f3226`). `testdata/bench-baseline.txt` was NOT regenerated (its rule, and this branch's).

### V5-VERIFY disposition (2026-09-10)

`deferred:V6-VERIFY`. The coordinator's quiet-machine numbers, taken serially on `verify/v5`: E9 ran
inside the checkpoint package's benchmark sweep at `-benchtime 2s` without `-benchmem`, the H2 sweep
is `devtool bench`, and E9b and E9c ran
`go test ./internal/checkpoint -run '^$' -bench '^BenchmarkFinalize$' -benchmem -count=6` (`1a1bbaf`
differs from `0d5c999` only in `tools/devtool`):

| window | probe on the summary line | ms/op | file |
|---|---|---|---|
| AC turbo, `0d5c999`, `-benchtime 2s` | AC, processor performance 170 %, foreign load 2.8 % | 43.75 | `scratchpad/quiet/E9-I-10.17.txt` |
| AC turbo, `0d5c999`, H2 sweep | AC, processor performance 208 %, foreign load 3.1 % | 42.77 | `scratchpad/quiet/v5-bench.txt` |
| battery base clock, `1a1bbaf`, `-count=6` | battery 55 %, processor performance 92 %, foreign load 7.1 % | 66.6, 67.3, 69.9, 70.0, 72.1, 73.1 | `scratchpad/quiet/E9b-finalize-count6.txt` |
| AC, `1a1bbaf`, `-count=6` (repeat) | AC, processor performance 128 %, foreign load 3.5 % | 63.6, 65.6, 65.8, 65.9, 66.2, 70.3 | `scratchpad/quiet/E9c-finalize-count6.txt` |

A first `-count=6` attempt on AC read 46.8–49.8 ms over five samples before the lid closed mid-run
and the sixth read 67.4 ms; its output file was overwritten by the re-take above and it is not
cited as evidence. The fix did what it set out to do — 264–284 ms when the row was opened, 111 ms
under load on the fix branch (`b1a8318`, which landed as `72f3226` after `d403363` resolved tool pointers
by root, so at 17.0 k allocs/op against the landed chain's 23.6–23.8 k), 42.8–43.8 ms at 170–208 % — but
the 50 ms criterion holds on this host only at high turbo clocks: at 128 %
the same tree reads 27–41 % over, and at base clock — the regime a laptop on battery and a CI
runner without turbo actually run in — 33–46 % over. The
allocation columns (23.6–23.8 k allocs/op, 4.47–4.62 MB/op wherever recorded) agree across windows to
within 1 %, so the spread is the clock, not the code. The second bullet of "What resolving it looks like" (a real fix, escape check
preserved and reviewed) is done; the first (a reference-platform figure) is not, and it decides the
row: the `toNorm` cost this row was about does not exist on Linux, and what remains is `Begin`'s
successor draft, the per-pointer `store.Has` stat and the `persist`/`CreateNew` fsyncs. The row
therefore stays unresolved, re-owned by V6-VERIFY together with SP06-D2 and SP08-D1, with
`BenchmarkFinalize` as its evidence and CI's `timing` job as its judge. `testdata/bench-baseline.txt`
was not regenerated.

---

## V6-VERIFY disposition (2026-10-01, coordinator under owner decision D33; ledger D54)

**SP10-D1 -> fixed (budget met on the reference platform; Windows residual recorded).** Measured by quiet C5.2 on candidate 5 `0d06ab12`, ten balanced ABBA rounds against the pre-Phase-2 base `cf31e01`, medians per call (`plans/sdd/V6-closeout/phase3/c5/quiet/c52-win/paired.txt` and `c52-linux/paired.txt` on verify/v6 `593003e9`; Windows 11, Intel Core Ultra 7 155H; Linux is the Docker Desktop container, valid for CPU- and read-bound rows, not for fsync-bound ones (D53(b))):
`BenchmarkFinalize` reads **46.05 ms** on Linux and **56.85 ms** on Windows, against exit criterion
1570's 50 ms. Linux is the platform the budget is stated for; on Windows the figure is 0.87x the base,
10 of 10 rounds faster.

Two caveats are recorded honestly:
- **Windows is still 14 % over.** The Windows residual is NTFS file creation, not code Qompack can
  remove.
- **Linux is 1.33x the base (34.37 ms) while allocations fell 29 %.** That is consistent with the
  checkpoint barriers w6-ckptsync added under D26: checkpoints/, the MANIFEST line and the segment marks
  are now fsynced before the seal, and on this container each fsync costs several milliseconds. The
  attribution is by inspection, not by profile.

Finalize runs inside PreCompact's B-E (2,000 ms; quiet C5.1 B-E p99 162 ms on Windows, 310 ms on Linux),
so neither figure reaches the user.

---

## V6-VERIFY candidate 6 confirmation (2026-10-02, C6.3)

Candidate 6 is `verify/v6` `99d0b18`. Every `Test*` evidence test below ran green on it in the Windows whole tree (pre-freeze tree `61b0cd66`, product-identical, and the `-race` pass `phase3/c6/p3-win-race.log`) and in hosted ci.yml `36955046276` `test (ubuntu-latest)` (`-race`) and `test (macos-latest)`; codes and paths are in `sdd/V6-closeout/inventory-c6-map.md`. A `Benchmark*` evidence symbol exists on the candidate; `go test` does not execute benchmarks, and the measurement a row rests on is named in its row. Status is unchanged; `CARRIED-DEFECTS.tsv` remains the source of record.

| row | status | ruling | on candidate 6 |
|---|---|---|---|
| SP10-D1 | `fixed` | D54 (quiet C5.2 on candidate 5) | BenchmarkFinalize: covered code unchanged since the candidate 5 measurement (`sdd/V6-closeout/w17-inventory/runs/unchanged-proofs.txt`); `finalize.go` is byte-unchanged, and candidate 6's only checkpoint change is the nil-checked `PricedDrops` hook on `preCompact`, which Finalize does not enter |

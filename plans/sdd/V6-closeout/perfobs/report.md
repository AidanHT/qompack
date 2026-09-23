# Observer/checkpoint/negknow performance workstream report — C2.3–C2.5

Branch `closeout/perfobs`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `partial`, head `d1494c8`

### Root cause

SP08-D1: on the base, most of OnToolUse's CPU was observer and canon/MinHash work. The lexical-cohesion regexp took 13%: 8,000 allocating matches per event. The tool response was unwrapped up to 4 times per event (5.47 MB allocated per event vs 1.23 MB per decode). Production MinHash always took the slow variable-width shingle loop, because the store's width is 5 and only 8 had a fast path. Every line-anchored canon rule's regexp scanned the whole line for a CR and backtracked. With those fixed, the observer's own work is ~3% of OnToolUse. The rest is store.PutBytes: the V6 durability fsyncs (Linux VM: 141 of 144 ms of syscall time per Delta event) plus redaction (~33 ms of CPU per 256 KB; assignment_secret's prefilter passes on words like 'key'/'token' and scans 256 KB for zero matches). SP10-D1: a V6 regression. F4-8's ObjectOnDisk made keepResolvableTools stat each tool pointer's ROOT first (2 failing stats; a root is never an object file) and then each chunk once per pointer: 59% of Finalize on Windows. What remains is fsync (Linux trace: ~88 of 105 ms/op, 64 of them the successor draft's persist). SP09-D1: Open is graded on whole-process CPU including GC. Allocation sources that named nothing kept: core.ParseHash buffers, 64-char hex map keys, and one heap logLine per line.

### Summary

# V6 close-out C2.3–C2.5: carried performance defects SP08-D1, SP10-D1, SP09-D1

Branch `closeout/perfobs`, cut from `verify/v6` @ `cf31e01`. The last code commit is `de83e44`; the evidence commit is `d1494c8`. All logs cited are under `plans/sdd/V6-closeout/perfobs/runs/`, and `runs/INDEX.txt` gives the build behind each one. **The harness blocked me from writing this file, so this text is the report.** It is meant to be committed as `plans/sdd/V6-closeout/perfobs/report.md`.

This work changes none of the following: budget constants, `CARRIED-DEFECTS.tsv` statuses, persisted formats, golden files, assertions or timeouts. No `t.Skip`, `//nolint` or `//nomagic:allow` was added; `git diff cf31e01..HEAD | grep -E '^\+.*(t\.Skip|b\.Skip|nolint|nomagic:allow)'` returns nothing. There are no criterion changes.

## 0. Summary

| Row | Root cause | Result (interleaved A/B) | Proposal |
|---|---|---|---|
| **SP08-D1**: OnToolUse over a 256 KB result, B-C p99 < 50 ms | Observer and canon/MinHash CPU (fixed). The remainder is `store.PutBytes`'s V6 fsyncs plus redaction, both outside scope. | **Deduped:** −39% ns/op on Windows (p=0.005, p99 −45%) and Linux (75.7 → 46.4 ms, not significant: p=0.105). **All fixtures:** allocs −70 to −78%, B/op −38 to −41% (p<0.001). **Delta:** still fsync-bound, and noise hides any difference. | Stays deferred. The fsync floor needs an owner decision (§8.1). |
| **SP10-D1**: `BenchmarkFinalize` mean < 50 ms | **A V6 regression**: F4-8's `ObjectOnDisk` made every tool pointer stat its root first. Fixed. The remainder is fsync. | **Windows:** 998.6 → 257.2 ms/op (−74%, p<0.001), allocs −27%. **Linux VM:** 120.2 → 115.2 (not significant; fsync-bound), allocs −21%. | Regression fixed. Scope the criterion to storage, or record an ADR (§8.2). |
| **SP09-D1**: negknow `Open` < 300 ms CPU/op | Allocation-driven CPU, because GC counts against the budget. | **Load-independent columns:** allocs −35%, B/op −22%. **CPU at GOMAXPROCS=1 on Windows:** −10 to −11% (p=0.029 at n=12; p=0.050 at n=16). **CPU at GOMAXPROCS=4 on Linux or 22 on Windows:** no significant change. | Close as `fixed` only after a base-clock reference figure (§8.3). |

## 1. Measurement conditions

**No window was quiet.** On the Windows host (Core Ultra 7 155H, 22 threads, AC power), processor time ran at 58–90% and Processor Performance at about 155–162%. Other agents were running whole-tree suites the whole time: daemon, mcp, store, integration, e2e and fault test binaries, plus their detached daemons.

The Linux figures come from the `qompack-v6-linux-verification` container with GOMAXPROCS=4. That container is a WSL2 VM on the same CPUs, and its fsync goes to a slow virtual disk.

**Method.** I built base (`cf31e01`) and new test binaries once, then ran them alternately, one `-count=1` sample per side per round, and compared with benchstat.

**How to read the numbers.** Absolute latencies are co-loaded and are **not budget evidence**; the quiet distributions for C5.1/C5.2 are still owed. The load-independent evidence is allocs/op, B/op and CPU-profile shares. Every new cost test pins a count and was RED on the base.

**My own processes added load, and some of it was accidental:**
- `TaskStop` ended the outer shells of two A/B jobs but left their `ab_win.sh` loops running (the background-kill-orphans trap). I found and stopped them about an hour later. Their samples are quarantined in `runs/win-ab/{aborted,orphaned}-*` and used nowhere.
- `devtool lint`'s `stubskips` check started a whole-tree `go test`. I stopped my instance after about 35 minutes (details in §9).

## 2. SP08-D1: OnToolUse over a 256 KB tool result (B-C)

### 2.1 Where the time went on the base

Windows CPU profile, 400 events, shares of `onToolUse` (`runs/win-profiles/observer-256KB-*base*`):

| Stage | Share | In scope? |
|---|---|---|
| `store.PutBytes` (total) | 76.3% | — |
| — Redaction (redact regexps) | 25.7% | No |
| — `canon.(*registry).Run` (total) | 30.2% | Yes |
| —— Rule matching (`collect`) | 20.4% | Yes |
| ——— of which `testRunnerCanon` | 11.3% | Yes |
| —— MinHash | 8.3% | Yes |
| — Store I/O (`admitRecovery` / `putDeltas` / `putObject`) | ~28% | No |
| `features` → cohesion regexp `FindAll` | 13.0% | Yes |
| `ExtractSignals` (its own decode of the response) | 5.7% | Yes |
| `responseText` at step 3 | 4.8% | Yes |

The carried-defect diagnosis missed four things:
1. **The cohesion regexp.** This feature makes 8,000 allocating matches per event.
2. **Repeated decoding.** The response was unwrapped up to 4 times per event: 5.47 MB allocated per event, against 1.23 MB for a single decode.
3. **MinHash's fast path never ran.** `hashShingles` only has a constant-width loop for width 8, but every shipped caller uses width 5 (`canon.DefaultShingleSize` and `observer.minHashShingleSize`).
4. **The anchored canon rules.** Each opens with `lineLead = (?:[^\n]*\r)?…`, so on every line that passes the prefilter the regexp scans the whole line for a CR and then backtracks. On go test output every `--- PASS` line is a real match.

### 2.2 Changes, and how identity is shown

- **`77292e1`: cohesion tokens are scanned by hand.** Each window is scored event by event (no 256 KB `concatText`), and counts go through a reused lowercase buffer.
  - Identity: the original regexp is kept in the tests as an oracle. `TestTermFrequenciesMatchTheTokenRegexp` covers the corpus, invalid UTF-8, the token cap and two rapid generators. `TestWindowTermFrequenciesMatchTheConcatenation` checks the per-event form against the concatenation.
  - The cosine sums are sums of integers, so map iteration order cannot change the result.
  - Cost pin: `TestTermFrequenciesCostIsPerDistinctToken`. RED on base: 12,030 allocations for 4,000 tokens vs 1,211 for 400.
- **`efc5fa6`: the tool response is decoded once per event.** The exported §5.21 functions keep their signatures and stay pure; they delegate to unexported forms that take the text. `OnToolUse` passes in the text it decoded at step 3. `responseText` skips the decode attempts that the first non-whitespace byte already rules out.
  - Identity: the old `responseText` is kept as an oracle. `TestResponseTextMatchesEveryShapeOfTheOracle` covers every host shape, degenerate and invalid inputs, the corpus in 4 wrappings, and generated input.
  - Cost pin: `TestOnToolUseDecodesTheToolResponseOnce`. RED: 5.47 MB/event. GREEN: 1.73 MB/event.
- **`3dc229f`: a constant-width FNV-1a loop for width 5.**
  - Identity: `TestHashShingles_EveryWidthIsFNV1a` checks every width from 2 to 64. Two store-width cases were added to the bottom-k reference test. `TestMinHash_StableAcrossRuns` and every golden are unchanged.
- **`86ff483` + `de83e44`: plain-line forms for the 10 line-anchored canon rules.**
  - Mechanism: on a line with no CR and no ESC byte, `lineLead` is replaced by `[ \t]*`, `sepRun` by `[^\S\n]+`, `lineTail` by `[ \t]*$`, and the anchor becomes `\A`. The rule's lead literal is checked before any regexp runs, and whether the whole buffer is plain is decided once.
  - Why this is exact: every escape shape starts with ESC; the replaced fragments are non-capturing; and `(?m)^` and `\A` mark the same position in a line slice.
  - Guard: a rule is left on its full pattern if it is unanchored, a top-level alternation, or starts with an empty or whitespace literal.
  - Identity: the existing `TestPrefilterAgreesWithFullScan`, its rapid form and `FuzzPrefilterAgreesWithFullScan` compare against the unoptimized full scan, so they cover the new path. The fuzz target ran clean for 120 s and 90 s. The new `TestPlainLineRules` pins per-line equivalence and requires all 10 anchored rules to carry a plain form.
  - Canon micro-benchmark on Windows (`-cpu 1`, 10 rounds): Bash100KB 5.37 → 3.94 ms (−26.6%, p<0.001); GoTest allocs 284 → 168.

### 2.3 Results

**Windows, 8 rounds, 200x each** (`runs/win-ab-observer/benchstat.txt`):

| Fixture | ns/op | p50 | p99 | B/op | allocs |
|---|---|---|---|---|---|
| Deduped | 118.8 → 72.5 ms (−39.0%, p=0.005) | 106.5 → 73.7 (−30.8%, p<0.001) | 262.1 → 143.4 (−45.3%, p=0.034) | −39.8% | 26.8k → 6.0k |
| Delta | 476.9 → 613.4 ms (±126%, p=0.80; not separable, I/O under load) | — | — | −38.3% | 29.7k → 8.9k |
| AllNovel (one usable pair) | 2,166 → 1,283 ms | — | — | — | — |

**Linux, 8 rounds** (`runs/linux/ab/observer-benchstat.txt`):

| Fixture | ns/op | p50 | B/op | allocs |
|---|---|---|---|---|
| Deduped | 75.7 → 46.4 ms (p=0.105, one outlier) | 73.7 → 45.1 ms (p=0.071) | −41.3% | −77.6% |
| Delta | 228.5 → 213.3 ms (p=0.65) | — | −40.4% | −72.5% |
| AllNovel (n=3) | 425.7 → 313.7 ms | — | — | — |

The stopped partial run of intermediate build `92ad1db` pointed the same way: Deduped 81 → 48 ms, Delta 212 → 128 ms, AllNovel 308 → 196 ms.

**B-C is still breached on the 256 KB Delta fixture on both platforms.**

### 2.4 What remains, outside scope

In a Windows profile of the new build, **the observer's own work outside `PutBytes` is 2.6%** of `OnToolUse`. Syscalls are 61.6%, of which `ensureDir` → `CreateDirectory` alone is about 13%.

Linux execution trace (`92ad1db`, 101 Delta events, `runs/linux/trace/delta-syscall-top.txt`):
- **Syscall time: 144 ms per event, 141 ms of it fsync.** Half is `admitRecovery`'s pending write and half is `putDeltas` → `putSideRecord`. Both are `paths.WriteAtomic` with a file fsync and a directory fsync, i.e. the V6 durability barrier. That barrier did not exist when the row's 53–115 ms was measured.
- **CPU: about 65 ms per event.** Redaction about 33 ms, canonicalization about 25 ms (before `de83e44`), MinHash about 7 ms.
- **Redaction per rule** (a scratch test, not committed): on this fixture only `assignment_secret` passes its prefilter, which matches ordinary words such as "key" and "token". It then costs about 16.7 ms per 256 KB scan and finds 0 matches.

## 3. SP10-D1: `BenchmarkFinalize` (mean < 50 ms)

**The base had regressed.** It measured 242 ms/op with 29.4k allocs/op, against V5's 23.6–23.8k. The profile put **59.4% of Finalize in `keepResolvableTools` → `ObjectOnDisk` → `GetFileAttributesEx`**.

**Why.** F4-8 correctly switched from `Has` to `ObjectOnDisk`, which checks the disk. But `toolResultResolvable` statted the pointer's hash **first**, and a tool pointer names a root, which is never an object file. So every pointer paid 2 failing stats before the in-memory root index was even consulted. It then paid one stat per chunk, again per pointer, even when pointers shared chunks. That is 600 stats per Finalize where 200 suffice.

**Fix (`7c5d640`).**
- `GetRoot` (in memory, no side effects) is asked first; `present(h)` runs only if the root does not resolve with every chunk on disk. The boolean result is the same.
- The presence check is memoized for the call, so a shared chunk is statted once.
- F4-8's actual question is unchanged: every kept pointer's chunks were checked on disk.
- Tests:
  - Cost pin: `TestKeepResolvableToolsStatsEachChunkOnceAndNeverTheRoot` (RED on base).
  - Verdict pin: `TestKeepResolvableToolsVerdictsMatchTheDefinition` checks every keep/drop decision and the drop order.
  - Unchanged and passing: `TestFinalizeDropsAToolPointerWhoseChunkFileIsGone` and every golden.

**Results.**
- Windows, 10 rounds, 5x: 998.6 → 257.2 ms/op (−74.3%, p<0.001), allocs 29.25k → 21.26k, B/op −26.3%.
- Linux, 8 rounds, 10x: 120.2 → 115.2 ms (p=0.80), allocs 22.49k → 17.70k, B/op −12.6%.

**What remains is fsync.** Linux trace, 5 timed calls (`runs/linux/trace/finalize-syscall-top.txt`), about 105 ms/op in total:

| Component | ms/op |
|---|---|
| All fsync | ≈ 88 |
| — `Begin` persisting the successor draft (`WriteAtomic`), including a ≈ 38 ms directory fsync | ≈ 64 |
| — `CreateNew` of the artifact | ≈ 25 |
| Stats | ≈ 2 |
| Pointer validation | ≈ 0.7 |

After the fix, Windows spends 28.9% of Finalize in `keepResolvableTools`, 15.3% in `ValidatePointers` and 39.2% in `Begin` (including its verified read-back of the parent).

Two changes were considered and **not** made, because each alters a recorded design rather than making it cheaper:
- **Seeding the successor from memory.** `Begin` could take `UserIntent.Original` from memory instead of reading back the artifact it just wrote. That drops a verification read, and a JSON round trip changes invalid UTF-8, so the result would not be byte-identical.
- **Deferring the successor draft's first persist to its first Advance.** That changes §7's draft durability and would need an ADR.

## 4. SP09-D1: negknow `Open` (300 ms CPU/op)

**Base cost:** 400.6k allocs and 46.1 MB per Open of the 20,000-record log.

**Changes (`92ad1db`, `0f1bfab`):**
- A non-allocating `parseHash` with exactly `core.ParseHash`'s acceptance rule.
- `byMatch` and `byKey` keyed on the raw `core.Hash`. This is the re-keying **ruling R27 left "available to a later task"**. R27's 5 MB budget is unchanged, and the `dedupHex`/`MatchHex` spellings are kept.
- One `logLine` per replay, reset for each line, instead of a heap allocation per line.

**Regression found and fixed.** In `92ad1db`, `parseHash` decoded digits through a switch. A same-window profile diff showed it **slower** than what it replaced: 0.91 s vs 0.44 s over 30 Opens. `0f1bfab` switched to a 256-entry lookup table: 0.16 s vs 0.28 s. The A/B series of the intermediate build are superseded; READMEs in those directories say so.

**Tests.**
- `TestParseHash_AgreesWithCore`: identity against `core.ParseHash`.
- `TestParseHashOrZero_DoesNotAllocate`: RED on base, 1 alloc per parse.
- `TestReindex_AllocatesNoPerRecordKeys`: RED on base, 6,021 allocations for 2,000 records.

**Results:**

| Setting | CPU/op | allocs/op | B/op |
|---|---|---|---|
| Windows, GOMAXPROCS=1, n=12 | 111.7 → 99.2 ms (−11.2%, p=0.029) | 400.6k → 260.6k (−35.0%) | 43.9 → 34.1 MiB (−22.4%) |
| Windows, GOMAXPROCS=1, n=16 | 110.2 → 99.2 ms (−9.9%, p=0.050) | same | same |
| Windows, GOMAXPROCS=22 | not significant (±35%) | same | same |
| Linux, GOMAXPROCS=4 | 119.4 → 118.5 ms (not significant) | same | same |

**`TestBudget_Open` passed in every interleaved attempt.**

| Platform | Base CPU/op | New CPU/op |
|---|---|---|
| Windows | 114.6, 122.8, 207.0 ms | 104.9, 191.4, 120.5 ms (one internal retry) |
| Linux | 127.1, 128.0, 106.5 ms | 117.8, 124.9, 136.7 ms |

## 5. Tests run

Windows runs are from the worktree root. Logs are in `runs/`.

**New cost tests: RED on the base, then GREEN**
- `go test ./internal/checkpoint -run 'TestKeepResolvableTools' -count=1 -v -timeout=30m`
- `go test ./internal/observer -run 'TestTermFrequencies|TestWindowTermFrequencies' -count=1 -v -timeout=30m`
- `go test ./internal/observer -run 'TestOnToolUseDecodesTheToolResponseOnce' -count=1 -v -timeout=30m`
- `go test ./internal/negknow -run 'TestParseHash_AgreesWithCore|TestParseHashOrZero_DoesNotAllocate|TestReindex_AllocatesNoPerRecordKeys' -count=1 -v -timeout=30m`

**Equivalence and fuzz: pass**
- `go test ./internal/canon -run 'TestPlainLineRules|TestPrefilter' -count=1 -v -timeout=30m`
- `go test ./internal/canon -run '^$' -fuzz '^FuzzPrefilterAgreesWithFullScan$' -fuzztime 120s -parallel 4` (and 90 s after `de83e44`)

**Full packages at `de83e44`**
- `go test ./internal/canon ./internal/sketch ./internal/checkpoint -count=1 -timeout=30m`: PASS.
- `go test ./internal/negknow -count=1 -timeout=30m`: PASS (93.7 s).
- `go test ./internal/negknow ./internal/observer -count=1 -timeout=30m`: observer PASS (830.8 s).
  - negknow failed in this combined run: `TestBudget_RefreshStaleness` measured 10.50 ms wall against its 10 ms budget (CPU 6.43 ms).
  - Re-run alone 4 times, alternating base and new: PASS at 3.1–5.1 ms. This was **co-load**, not the change.

**Linux**, as non-root user `qtest` at `de83e44`, GOMAXPROCS=4, running each test binary with `-test.count=1 -test.timeout=30m`: canon, sketch, negknow, checkpoint and observer all exit 0. The same held at `92ad1db`.

**Format, vet, lint**
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet` on the 5 touched packages: exit 0.
- `devtool lint`: see §9.

## 6. Benchmarks run

Every benchmark was run through the interleaving drivers `runs/ab_win.sh` and `runs/linux_ab.sh`.

| Benchmark | Windows | Linux |
|---|---|---|
| `BenchmarkOnToolUse_TestOutput256KB`, Deduped and Delta, 200x | 8 rounds | 8 rounds |
| `BenchmarkOnToolUse_TestOutput256KB`, AllNovel, 200x | 1 usable pair | 3 rounds |
| `BenchmarkFinalize` | 10 rounds, 5x | 8 rounds, 10x |
| `BenchmarkOpen`, 10x | 12 rounds (GOMAXPROCS=22); 12 and 16 rounds (GOMAXPROCS=1) | 8 rounds |
| `BenchmarkRun_*` (canon) | 10 rounds, `-cpu 1` | 8 rounds |
| `TestBudget_Open` | 3 per side | 3 per side |

Linux also has execution-trace and CPU profiles of Finalize and of the Delta fixture.

## 7. Criterion changes

**None.** Two test-side edits, neither an assertion:
- The `seedActive` helper follows the new index key type.
- The comment on `budgetResidentBytes` notes that R27's re-keying has now happened. The constant is unchanged.

## 8. Owner proposals (C2.8). No status or budget was changed here; quiet distributions come first.

### 8.1 SP08-D1

**Keep it deferred**, re-owned to whoever owns the store's durability cost. The observer and canon/MinHash costs are fixed. What remains is V6's two `WriteAtomic` calls (4 fsyncs) per novel put on the `l0_process` path, about 141 ms per event on this VM, plus about 33 ms of redaction CPU per 256 KB. B-C is soft and ungated, and nothing blocks while it overruns.

The real fixes belong elsewhere:
- **`internal/store`:** amortize the pending-write and side-record fsyncs across a drain batch, as SP20-D1's group commit does.
- **`internal/redact`:** tighten `assignment_secret`'s prefilter, or apply the per-line treatment canon got here.

Alternative: state B-C against the payload class it was written for (64 KB stays inside the budget) and report 256 KB separately, with a number taken on a quiet reference host.

### 8.2 SP10-D1

**The V6 regression is `fixed`.** For the row itself, one of:
- **A storage-scoped criterion:** mean < 50 ms on the reference Linux host's local disk, judged by CI's `timing` job, with Windows and VM figures reported but not gated. On this VM, fsync alone is about 88 ms/op.
- **Meeting 50 ms on slow-fsync storage:** the lever is deferring the successor draft's first persist, which needs an ADR on §7. Editing the threshold is not the lever.

### 8.3 SP09-D1

**Close as `fixed`, but only after C5.2 supplies a base-clock reference figure below 300 ms. If it reads above, scope the 300 ms to turbo-capable hosts.**

Rough scaling from the V5 quiet figure (90.4 ms at 161%) with a ~10% cut gives about 81 ms, which scales to about 238 ms at 55%. The same arithmetic gave about 265 ms before. This is still an estimate, not a measurement.

## 9. Open issues

1. **I killed another workstream's process.** While stopping my own orphaned loops, I filtered processes on the shared session scratchpad path and stopped **PID 7532, `scratchpad\perfstore\store-basebench2.exe`** (the store perf workstream) at about 00:00Z. Its run is lost and needs re-running. I reported this to `main` at once.
2. **`devtool lint` never ran to completion as one invocation.**
   - PASS: golangci-lint (after its one real finding was fixed in `5a7f44c`), nomagic, importgraph, testdeps, sleepcheck, runpatterns, docmarkers, coveragefloors.
   - `bindeps` fails on the base `cf31e01` too (`golang.org/x/sys/unix`, 4 violations, reproduced in the container), so it is not from this branch.
   - `stubskips` runs a whole-tree `go test`, which this workstream was told not to run. I stopped it (my own process tree only).
   - Detached test daemons from that stopped run cannot be told apart from other agents' daemons, so I left them to exit on their own when idle.
3. **Quiet distributions (C5.1/C5.2) are still owed for all three rows.**
4. `internal/daemon/scheduler_tap.go` calls `ExtractSignals` on every event, which decodes the tool response once more on the daemon side. This is outside scope.

### Commits

- 7c5d640 perf(checkpoint): resolve tool pointers through the root index first
- 77292e1 perf(observer): score lexical cohesion without the token regexp
- efc5fa6 perf(observer): unwrap each tool response once per event
- 3dc229f perf(sketch): hash store-width shingles in a constant-width loop
- 86ff483 perf(canon): match plain lines with the anchored rules' plain forms
- 92ad1db perf(negknow): cut Open's per-record allocations
- 0f1bfab perf(negknow): decode digests through a digit table
- 5a7f44c test(observer): drop a redundant conversion in the decode test
- de83e44 perf(canon): test the plain lead before scanning a line for literals
- d1494c8 docs(perfobs): record the C2.3-C2.5 test and benchmark evidence

### Tests

- `go test ./internal/checkpoint -run 'TestKeepResolvableTools' -count=1 -v -timeout=30m (Windows)` — RED on base cf31e01: the root was statted twice (runs/win-ckpt-tools-cost-RED.txt). PASS after 7c5d640.
- `go test ./internal/observer -run 'TestTermFrequencies|TestWindowTermFrequencies' -count=1 -v -timeout=30m (Windows)` — RED on base features.go: 12,030 allocs for 4,000 tokens vs 1,211 for 400. PASS after 77292e1.
- `go test ./internal/observer -run 'TestOnToolUseDecodesTheToolResponseOnce' -count=1 -v -timeout=30m (Windows)` — RED on base: 5.47 MB per event vs 1.23 MB per decode. PASS after efc5fa6: 1.73 MB per event.
- `go test ./internal/negknow -run 'TestParseHash_AgreesWithCore|TestParseHashOrZero_DoesNotAllocate|TestReindex_AllocatesNoPerRecordKeys' -count=1 -v -timeout=30m (Windows)` — RED on base: 1 alloc per parse; 6,021 allocs to reindex 2,000 records. PASS after 92ad1db and 0f1bfab.
- `go test ./internal/canon -run 'TestPlainLineRules|TestPrefilter' -count=1 -v -timeout=30m (Windows)` — PASS
- `go test ./internal/canon -run '^$' -fuzz '^FuzzPrefilterAgreesWithFullScan$' -fuzztime 120s -parallel 4 (Windows)` — PASS at 86ff483 (120 s) and at de83e44 (90 s)
- `go test ./internal/canon ./internal/sketch ./internal/checkpoint -count=1 -timeout=30m (Windows, de83e44)` — PASS for all three
- `go test ./internal/negknow -count=1 -timeout=30m (Windows, de83e44)` — PASS (93.7 s)
- `go test ./internal/negknow ./internal/observer -count=1 -timeout=30m (Windows, de83e44)` — observer PASS (830.8 s). negknow FAIL: TestBudget_RefreshStaleness wall-clock 10.50 ms vs 10 ms under co-load. Re-run alone 4 times alternating base/new: PASS at 3.1-5.1 ms.
- `Linux container, as non-root user qtest, GOMAXPROCS=4: <pkg>.test -test.count=1 -test.timeout=30m for canon, sketch, negknow, checkpoint, observer (de83e44)` — All exit 0 (also all exit 0 at 92ad1db)
- `go run ./tools/devtool fmt-check; go vet on the 5 touched packages (de83e44)` — Both exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,sleepcheck and --only=runpatterns,docmarkers,coveragefloors` — PASS, after 5a7f44c fixed the unconvert finding
- `go run ./tools/devtool lint --only=bindeps (container, base cf31e01)` — FAIL, 4 x/sys/unix violations, also on the base: not from this branch
- `Interleaved A/B, Windows, 8 rounds, 200x: BenchmarkOnToolUse_TestOutput256KB (Deduped|Delta)` — Deduped -39.0% ns/op (p=0.005), p99 -45.3% (p=0.034). Delta not separable (I/O under load). allocs -70 to -78%, B/op -38 to -40%.
- `Interleaved A/B, Windows, 10 rounds, 5x: BenchmarkFinalize` — 998.6 -> 257.2 ms/op (-74.3%, p<0.001); allocs 29.25k -> 21.26k
- `Interleaved A/B, Windows, 12 and 16 rounds, -test.cpu 1: BenchmarkOpen` — CPU/op -11.2% (p=0.029) and -9.9% (p=0.050); allocs -35.0%, B/op -22.4%
- `Interleaved A/B, Linux, 8 rounds, GOMAXPROCS=4: OnToolUse 256 KB, Finalize, Open, canon BenchmarkRun_*` — OnToolUse: Deduped 75.7 -> 46.4 ms (p=0.105), Delta not significant, allocs -73 to -78%. Finalize: not significant (fsync-bound), allocs -21%. Open: CPU not significant, allocs -35%. Canon geomean -17.8%.
- `TestBudget_Open, 3 per side, interleaved, Windows and Linux` — PASS in every attempt on both builds

### Open issues

- plans/sdd/V6-closeout/perfobs/report.md is not committed: the harness blocks subagents from writing report files. The full text is this output's summary field. The evidence it cites is committed (d1494c8).
- I accidentally stopped PID 7532, scratchpad\perfstore\store-basebench2.exe, a process belonging to another workstream (store perf, C2.6/C2.7), at about 00:00Z. My process filter matched the shared session scratchpad path instead of my own bin\ subdirectory. Its run needs repeating. I notified main.
- No measurement was quiet: all latencies were co-loaded, so the C5.1/C5.2 quiet distributions are still owed for SP08-D1, SP10-D1 and SP09-D1.
- SP08-D1 256 KB Delta still breaches B-C on both platforms. The remainder is outside scope: store.PutBytes V6 fsyncs (Linux VM: 141 ms of fsync per event) and redaction (assignment_secret scans 256 KB for zero matches, about 16.7 ms).
- SP10-D1: Finalize is fsync-bound after the fix (Linux trace: about 88 of 105 ms/op, 64 of them the successor draft's persist). The 50 ms criterion is not met on WSL2 or Windows storage.
- SP09-D1: CPU improved only measurably at GOMAXPROCS=1 (-10 to -11%). At GOMAXPROCS=4 (Linux) and 22 (Windows) there is no significant difference. The base-clock reference figure is still missing.
- devtool lint was not completed as one run. stubskips (a whole-tree go test) was stopped by me per the do-not-run-the-whole-tree rule. bindeps fails on base cf31e01 too. Detached test daemons from the stopped stubskips run could not be attributed and were left to exit on their own when idle.
- TaskStop left ab_win.sh child loops running (the background-kill-orphans trap) for about 1 h. Their samples are quarantined under runs/win-ab/{aborted-observer-mixed-build,orphaned-first-observer-loop} and not used.
- internal/daemon/scheduler_tap.go calls observer.ExtractSignals per event, one more response decode on the daemon side. Outside scope.

### Needs the owner

- SP08-D1 disposition. Proposal: keep deferred, re-owned to the store's durability cost owner. Real fixes are store group-commit of the pending-write and side-record fsyncs, and a tighter redact assignment_secret prefilter. The alternative is a B-C restatement by payload class with quiet numbers.
- SP10-D1: mark the V6 keepResolvableTools regression fixed. For the row: a storage-scoped criterion (mean < 50 ms on the reference Linux host's local disk, via CI timing, with Windows and VM reported only), or an ADR to defer the successor draft's first persist off the PreCompact path.
- SP09-D1: close as fixed once a base-clock reference figure (C5.2) is under 300 ms CPU/op. Otherwise platform-scope the 300 ms budget to turbo-capable hosts.
- Decision note: byMatch/byKey are now keyed on raw digests. This is the re-keying ruling R27 explicitly left available. R27's 5 MB budget is unchanged.
- Commit the report text (summary field) at plans/sdd/V6-closeout/perfobs/report.md. The harness prevented this subagent from writing it.
- Tell the store perf workstream that its store-basebench2.exe run (PID 7532) was killed by this workstream and must be re-run.

## Independent review

### review:perfobs:both: needs-fixes

- **minor** `plans/sdd/V6-closeout/perfobs/report.md (absent at d1494c8)` — The workstream's required deliverable, the before/after report with an owner proposal per row, is not on the branch. The evidence commit d1494c8 and runs/INDEX.txt cite a report that does not exist in the tree, so the branch's evidence has no index document and the three owner proposals (SP08-D1, SP10-D1, SP09-D1) exist only in the subagent's return value.
  - Evidence: The implementer's result says report_path is "NOT WRITTEN ... blocked by the harness". `git -C qompack-cx-perfobs ls-tree -r HEAD plans/sdd/V6-closeout/perfobs` lists runs/ only, and no report.md.
  - Fix: The orchestrator (main) should commit the returned `summary` text verbatim as plans/sdd/V6-closeout/perfobs/report.md on closeout/perfobs, with a `docs(perfobs): ...` subject and a `Refs: V6-VERIFY, C2.3-C2.5` footer. Before committing, run the report-SHA reachability scan: every quoted SHA (cf31e01, 7c5d640..d1494c8, 92ad1db) must pass `merge-base --is-ancestor` against the branch.
- **minor** `process: shared host, PID 7532 scratchpad\perfstore\store-basebench2.exe` — This workstream stopped another workstream's benchmark process (store perf, C2.6/C2.7) because its process filter matched the shared scratchpad path. That breaks the rule against killing processes you do not own, and it invalidates that run's evidence. The branch code is unaffected, but the store workstream's A/B series is incomplete until re-run. Disclosed by the implementer.
  - Evidence: Report §9 item 1 and open_issues: "I accidentally stopped PID 7532 ... Its run needs repeating. I notified main."
  - Fix: Main should confirm the perfstore workstream knows its store-basebench2 series was cut short and re-runs it. Any C2.6/C2.7 benchstat that includes that truncated series must be discarded. Future briefs should filter on the owning workstream's own bin\ subdirectory, not the session scratchpad root.
- **nit** `commit 5a7f44c (test(observer): drop a redundant conversion in the decode test)` — This is the only commit in cf31e01..HEAD without a `Refs:` footer; all the others carry `Refs: V6-VERIFY, C2.3-C2.5`. It also fixes a lint finding in a test that efc5fa6 introduced, so efc5fa6 on its own does not pass golangci-lint (unconvert).
  - Evidence: `git log -1 --format=%B 5a7f44c` ends at "The assertion is unchanged." with no Refs line.
  - Fix: If the branch is re-stacked before integration, squash 5a7f44c into efc5fa6 (preferred, since it keeps every commit lint-clean) or add the Refs footer. Otherwise record it as accepted.
- **nit** `plans/sdd/V6-closeout/perfobs/runs/win-ab-observer, runs/linux/ab, runs/win-ab-observer-allnovel` — The brief asked for -count 10 + benchstat per platform. OnToolUse has 8 rounds on each platform, and Finalize and Open have 8 on Linux. The AllNovel sub-benchmark has one usable pair on Windows and n=3 on Linux, so no significance test is possible for it. The report states the actual counts honestly, and none of the absolute latencies are used as budget evidence, so this only weakens the A/B claims. It does not invalidate them.
  - Evidence: Report §6 table: Deduped/Delta 8 rounds on Windows and Linux; AllNovel "1 usable pair" on Windows and 3 rounds on Linux.
  - Fix: Take no action on this branch. Make sure the owed C5.1/C5.2 quiet-window runs use -count>=10 for all three OnToolUse fixtures, including AllNovel, on both platforms before any SP08-D1 disposition relies on them.

## Fix seat (review resolution) — status `partial`, head `fc65e00`

### Root cause

Both review findings are correct. (1) report.md is missing because the harness refuses report-file writes from subagents. The implementer's Write at 23:29:41Z was refused with "Subagents should return findings as text, not write report files". (2) At 23:55:16Z the implementer stopped PID 7532 (scratchpad\perfstore\store-basebench2.exe). Its PowerShell filter matched every process under the shared session scratchpad path, not only its own scratchpad\bin\. That cut short perfstore's win2-search basebench2 round 2: exit=127, only the Search_1000Roots line printed, and the DistinctChunks sample is missing. Perfstore's later win4-search series (basebench2 vs final3) has all 3 rounds for both benchmarks.

### Summary

Fix seat for closeout/perfobs (C2.3-C2.5). HEAD is fc65e00 and the working tree is clean. Both minor findings are verified correct. No code changed. Committed: runs/review/pid7532-stop-evidence.txt (fc65e00), raw transcript and file excerpts for finding 2.

The report is still not on the branch. My own instructions forbid subagents from writing report files (the same rule refused the implementer's Write), and the finding's own fix assigns the commit to main. To build plans/sdd/V6-closeout/perfobs/report.md, main should take the implementer's `summary` field verbatim as sections 0-9 (or the byte-exact extract named in report_path), append the section below, and commit it as a `docs(perfobs): ...` commit with the `Refs: V6-VERIFY, C2.3-C2.5` footer. I have already run the SHA-reachability scan (see §10).

----- BEGIN SECTION TO APPEND -----

## 10. Review resolution (fix seat, 2026-09-23)

The review passed this seat two minor findings. The same review also raised two nits that were not assigned here; they are listed at the end for the coordinator. This seat changed no code, test, budget or status. Its one commit, `fc65e00`, adds an evidence log.

### R1 (minor): `report.md` was missing from the branch

**Verified: correct.** At `d1494c8`, `git ls-tree -r HEAD plans/sdd/V6-closeout/perfobs` listed `runs/` only. The implementer's `Write` of this file, at 23:29:41Z, was refused with "Subagents should return findings as text, not write report files".

**Action.** The fix seat is under the same restriction. It did not write this file and did not route around the refusal. It returned this section, and main committed §0–§9, the implementer's `summary` field byte for byte, followed by this section, as the finding's own fix proposes. Before hand-back:
- Every SHA quoted in §0–§9 passes `git merge-base --is-ancestor <sha> HEAD` on `closeout/perfobs`: `cf31e01`, `7c5d640`, `77292e1`, `efc5fa6`, `3dc229f`, `86ff483`, `92ad1db`, `0f1bfab`, `5a7f44c`, `de83e44`, `d1494c8`. The SHA added in §10, `fc65e00`, is reachable too.
- Every `runs/` path cited in §0–§9 exists in the tree.
- **One correction to §9 item 1, which is kept verbatim above:** the stop happened at **23:55:16Z on 2026-09-22**, not "about 00:00Z". That is the transcript timestamp; see R2.

### R2 (minor): this workstream stopped another workstream's process

**Verified: correct.** Evidence is in `runs/review/pid7532-stop-evidence.txt` (`fc65e00`).

**What was stopped.** At 23:55:16Z the implementer ran `Get-Process | Where-Object { $_.Path -like '*<session id>*' } | Stop-Process -Force` to clear its own orphaned benchmark binaries. That filter matches the whole session scratchpad, which the perfstore workstream also uses. It stopped three processes:
- two `observer.test.exe` processes of the implementer's own, under `scratchpad\bin\base\`;
- PID 7532, `scratchpad\perfstore\store-basebench2.exe`, which belonged to perfstore.

The implementer's two other stops that used the scratchpad path (22:54:16Z and 23:51:52Z) were limited to `observer.test`. They hit only its own `scratchpad\bin\` binaries.

**What it cut short.** Perfstore's Windows Search A/B, `win2-search` (base `basebench2` vs `final2`, 3 rounds):
- Round 2 of `basebench2` started at 23:52:49.
- Its raw log ends in `exit=127` right after the `BenchmarkSearch_1000Roots` line, so the `BenchmarkSearch_1000Roots_DistinctChunks` sample for that round is missing.
- Every other perfstore series in `runs/ab/windows/` has all of its rounds: `wcpu-*`, `win2-read`, `win2-write`, `win3-*` and `win4-search`.

**Is it superseded?** Mostly, yes:
- Perfstore later ran `win4-search` against its newer build: base `basebench2` vs `final3`, 3 complete rounds, 00:17–00:50Z.
- However, `win2-search` still sits in perfstore's evidence directory, including the truncated raw file. At 00:03:54Z perfstore tabulated it with `DistinctChunks` at n=2.

**Who knows.**
- The implementer told main at 23:55:37Z.
- At 23:55:44Z main left `scratchpad\perfstore\COORDINATOR-NOTE-killed-run.txt` ("Re-run it, and do not use its partial output").
- As of 01:15Z, the perfstore implementer's transcript contains **no** reference to that note. It had not yet returned (last tool call 01:11:48Z). Nothing shows perfstore has seen the notice.

**Action by this seat.**
- It made no change to perfstore; it may not edit another worktree.
- It did not `SendMessage` the running perfstore agent. On 2026-09-22 a coordinator message to a running workflow agent resumed a duplicate copy of that agent (the checklist's 17:31 incident).
- It scanned for processes left by this workstream and found none, so it stopped nothing:
  - Windows: command lines or paths under `scratchpad\bin\`, or `ab_win.sh`.
  - Container: processes under `/work/cx-perfobs-*`.

**Owed to main**, since the finding's fix is addressed to main:
1. Tell perfstore's review or fix seat that `win2-search` must be marked superseded or discarded. No C2.6/C2.7 figure may use it, least of all the `DistinctChunks` n=2 row. `win4-search` is the complete replacement. A re-run is needed only if perfstore wants `final2` itself compared with base on Windows Search.
2. Adopt a brief rule for future dispatches: a process filter must match the workstream's own binary subdirectory or its own driver-script command line, never the shared session scratchpad root.

### Nits from the same review (not assigned to this seat)

- **`5a7f44c` has no `Refs:` footer.** The workstream rule requires the footer only on feat/fix commits, and this is a `test(observer)` commit, so it complies. The substantive point stands, though: it fixes an `unconvert` finding in a test that `efc5fa6` introduced, so `efc5fa6` alone is not lint-clean. If the branch is re-stacked, squash the two. This seat does not rewrite history.
- **Some A/B series have fewer than 10 rounds:**
  - OnToolUse: 8 rounds per platform.
  - Finalize and Open on Linux: 8 rounds.
  - AllNovel: 1 usable pair on Windows, n=3 on Linux.

  The owed C5.1/C5.2 quiet runs should use `-count` ≥ 10 for all three OnToolUse fixtures on both platforms before SP08-D1's disposition relies on them.

### Checks run by this seat

No code changed, so no Go test was re-run.
- `git merge-base --is-ancestor <sha> HEAD` for every quoted SHA: all reachable.
- Existence check of every cited `runs/` path: all present.
- Process scan, 0 matches:
  - PowerShell `Get-CimInstance Win32_Process`, filtered on `scratchpad\bin\` and `ab_win.sh`.
  - `docker exec qompack-v6-linux-verification ps -eo pid,etime,args`, filtered on `cx-perfobs`.
- `python perfobs-pid7532-evidence.py > runs/review/pid7532-stop-evidence.txt` (from the scratchpad): exit 0. The output is committed.

----- END SECTION TO APPEND -----

Workstream state is unchanged from the implementer's result. Code is final at de83e44 (7c5d640, 77292e1, efc5fa6, 3dc229f, 86ff483, 92ad1db, 0f1bfab, 5a7f44c, de83e44). d1494c8 and fc65e00 add evidence. There are no criterion changes. Owner proposals for SP08-D1, SP10-D1 and SP09-D1 are in the implementer's §8.

### Commits

- fc65e00 docs(perfobs): record the evidence for the PID 7532 stop

### Tests

- `git -C qompack-cx-perfobs merge-base --is-ancestor <sha> HEAD for each SHA quoted in the report (cf31e01 7c5d640 77292e1 efc5fa6 3dc229f 86ff483 92ad1db 0f1bfab 5a7f44c de83e44 d1494c8 fc65e00)` — all reachable
- `ls each runs/ path cited in the report (win-ab-observer/benchstat.txt, linux/ab/observer-benchstat.txt, linux/trace/{delta,finalize}-syscall-top.txt, ab_win.sh, linux_ab.sh, win-profiles/*, win-ab/{aborted,orphaned}-*)` — all present
- `PowerShell Get-CimInstance Win32_Process filtered on scratchpad\bin\ and ab_win.sh; docker exec qompack-v6-linux-verification ps -eo pid,etime,args | grep cx-perfobs` — 0 leftover perfobs processes on either platform; nothing stopped
- `python scratchpad/perfobs-pid7532-evidence.py > plans/sdd/V6-closeout/perfobs/runs/review/pid7532-stop-evidence.txt` — exit 0, 110 lines, committed in fc65e00
- `Go tests` — not re-run: no code changed in this seat

### Open issues

- plans/sdd/V6-closeout/perfobs/report.md is still not on closeout/perfobs. Subagents cannot write report files, so main has to commit it: the implementer's summary field verbatim, then the section 10 text in this summary. The SHA scan is already done and all SHAs are reachable.
- Perfstore has not seen the kill notice: its transcript has 0 references to scratchpad/perfstore/COORDINATOR-NOTE-killed-run.txt as of 01:15Z, and it has not returned yet. Its evidence dir still holds win2-search, including the truncated basebench2 round-2 raw file and the n=2 DistinctChunks row. win4-search is the complete replacement.
- Carried from the implementer: quiet C5.1/C5.2 distributions are still owed for SP08-D1, SP10-D1 and SP09-D1. The SP08-D1 256 KB Delta case still breaches B-C (the cost is store fsync plus redaction, outside this scope). SP10-D1 is fsync-bound. SP09-D1 CPU improves significantly only at GOMAXPROCS=1. devtool lint was never completed as a single run (bindeps fails on the base too; stubskips was stopped). daemon scheduler_tap decodes the response one extra time.
- Reviewer nits, not assigned to this seat: efc5fa6 alone is not lint-clean (5a7f44c fixes it; squash them if the branch is re-stacked). Several A/B series have fewer than 10 rounds; the owed quiet runs should use -count>=10 for all OnToolUse fixtures.

### Needs the owner

- Main: commit report.md, i.e. the implementer's summary verbatim (extract at scratchpad/perfobs-summary.md, sha256 d6255d46d8b20aec615d799c41190017b7f441e1accabb17e23368f7d25ce5c7) plus section 10 from this output, on closeout/perfobs, subject docs(perfobs): ..., footer Refs: V6-VERIFY, C2.3-C2.5.
- Main: tell perfstore's review/fix seat to mark win2-search superseded or discarded, so no C2.6/C2.7 figure uses it, and to cite win4-search. Do not SendMessage a running workflow agent (the 17:31 duplicate-resume incident); route it through the perfstore fix-seat brief instead.
- Main: add a brief rule that process filters must match a workstream's own binary subdirectory or its own driver-script command line, never the shared session scratchpad root.
- Owner decisions carried unchanged from the implementer section 8: SP08-D1 (keep deferred and re-own to store durability, or restate B-C by payload class); SP10-D1 (mark the keepResolvableTools regression fixed; storage-scoped criterion or an ADR); SP09-D1 (fixed only after a base-clock C5.2 figure under 300 ms CPU/op). Also the R27 re-keying note.


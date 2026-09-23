# Store performance workstream report — C2.6/C2.7 SP06-D2, SP20-D2

Branch `closeout/perfstore`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `fdb2ea94e07c298d4022438f4fad58e36659646f`

### Root cause

SP20-D2: on Windows, each object read paid three path lookups, because os.SameFile over a path-based Lstat reopens the file to get its ID. On Linux, each open paid four fcntl calls. Search re-read and re-verified chunks that candidates share (1,536 opens for 514 objects) and scanned text byte by byte. SP06-D2: Encode's sync.Pool was emptied by garbage collection, so the zstd encoder was rebuilt with GOMAXPROCS match-table sets, 29 MB per rebuild and 69% of a cold put's allocated bytes. About 70 path syscalls per cold put decided nothing (the protected-root walk, MkdirAll stats, a double walk and a dead remove in WriteAtomic). What remains is inherent: per-chunk file create/close/rename plus directory creates (with Defender on Windows), the pending marker's durability fsyncs, and canonicalization/MinHash CPU, which is 91% of warm.

### Summary

I didn't write report.md. The harness blocks subagents from writing report .md files (my Write was refused), so this summary is the report. All evidence is committed under plans/sdd/V6-closeout/perfstore/runs/ in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-perfstore, and the coordinator can commit this text as report.md.

BRANCH closeout/perfstore, base cf31e01, head fdb2ea9. Scope was internal/store, internal/paths and their benchmarks; internal/daemon was not touched. No budget constant, CARRIED-DEFECTS status, golden, threshold, assertion, timeout or skip was changed.

HOW TO READ THE TIMINGS. Every timing was taken while up to ten other agents were running builds and tests on the same 22-thread laptop (Core Ultra 7 155H, AC power). Windows total CPU sampled before each run was 34-91%, and the Linux VM load average (golang 1.26.6, GOMAXPROCS=4) was 4-10. Absolute numbers are 2-20 times the quiet V5 figures, and single runs vary up to 9 times. So every A/B interleaves the two binaries round by round in ABBA order, and the tables give the paired per-round ratio with an exact sign test. Where timing can't decide, I relied on numbers load can't move: Linux strace syscall counts, allocations per op, and bytes allocated. None of these timings is a quiet-host figure or should become a budget; the quiet re-measure belongs to C5.2. "Base" in every A/B is cf31e01 plus only the two new benchmark functions, so those rows exist on both sides.

1. ROOT CAUSE, WITH EVIDENCE (base profiles are in runs/profiles; Linux strace counts are in runs/linux-syscount, with the harness and script)

SP20-D2, read path:
- Windows paid 3 path lookups per object read. `readBoundedObject` called `os.Lstat` (GetFileAttributesEx), then `os.Open`, then `os.SameFile`. A path-based Lstat on Windows has no file ID, so `SameFile` opens the path a second time (`os.(*fileStat).loadFileId`). In the base profile, Lstat took 740 ms, Open 1.03 s and SameFile 770 ms of `readBoundedObject`'s 3.51 s. A read made 22 allocations, a quarter of them `paths.Long`'s GetFullPathNameW conversions.
- Linux `os.Open` of a disk file costs 4 fcntl calls: Go sets the descriptor non-blocking, the poller refuses it, and Go sets it blocking again (GOROOT os/file_unix.go newFile). strace per GetChunk: 11 file syscalls.
- The rest of a Linux read is required work: zstd decode is 38% and SHA-256 30% of GetChunk CPU.
- Search re-read, re-decoded and re-hashed chunks that candidates share. One Search over the Search_1000Roots fixture did 1,536 object opens for 514 distinct objects. On Linux, the text scan (countFold over about 4 MiB) was 40% of Search CPU once that I/O was removed.

SP06-D2, write path:
- The zstd encoder kept being rebuilt. `Encode` took encoders from a sync.Pool, which every second garbage collection empties. Each miss built an Encoder that allocates one 1.3 MB match-table set per concurrency slot, and the default is GOMAXPROCS slots. On this host that is 29,354,608 bytes per rebuild. In the base cold profile, encoder initialization was 327 MB of 474 MB allocated (69%).
- Path syscalls that decided nothing:
  - `paths.OpenFile` stat-walked every ancestor looking for the project root, even for .qompack/tmp/obj-* staging paths, which can never be protected.
  - `MkdirAll` stat'ed before every fanout create.
  - `WriteAtomic` walked the ancestors twice, removed its staging file after the rename had already consumed it, and on Windows ran a Chmod that cannot change anything.
  - Linux strace per cold put: 176.7 file syscalls, of which 67.85 were newfstatat, 36 fcntl and 3 unlinkat.
- The dominant cost is inherent:
  - Windows: per novel chunk, CreateDirectory, CreateFile, WriteFile, CloseHandle (where Defender scans the new file) and MoveFileEx, plus the pending marker's FlushFileBuffers.
  - Linux on disk: the pending marker's two fsyncs (file and directory, SP-20 invariant 9). Under the same load a cold put took 80-90 ms on overlayfs and 13-15 ms on tmpfs.
- Warm is not the store's cost. Canonicalization is 91% of warm PutBytes (the pids regexp alone 44%, sketch.MinHash 24%), redaction 6%, and everything the store does under 1%.

2. WHAT CHANGED (every commit keeps every integrity and durability property)
- 5213545, object leaf open:
  - Windows opens the object once with FILE_FLAG_OPEN_REPARSE_POINT and checks the handle's own Stat. A handle without the reparse attribute is an ordinary file reached without redirection, so the check and the read see the same file, with no window between them.
  - A reparse-point leaf (symlink, placeholder, dedup file) goes through the exact old Lstat/Open/Stat/SameFile path.
  - Linux and darwin keep the Lstat and add O_NOFOLLOW|O_NONBLOCK: a link or FIFO swapped in after the Lstat is refused rather than followed or waited on.
- 07ce85e: object paths are built in one allocation. Tests pin that the result is exactly filepath.Join's.
- 182ca37: `ensureDir` creates the fanout directory first and falls back to `MkdirAll` as before. It only saves syscalls; a probe showed no timing difference on either platform (runs/mkdirprobe), so the coordinator can drop it without losing anything measurable.
- e86c700: staging files open with O_NONBLOCK on unix.
- 2e861ea: `paths.Long` returns a short absolute path directly. It matches the old full-path rule on 40,000+ generated paths.
- 4f6873c: `paths.OpenFile` skips the root walk when the path contains none of checkpoints, pins or tried.bloom. That skip is provably safe: a generated-path test checks IsProtected ⇒ mayBeProtected, and every protected path still walks and is guarded.
- 592f148: `WriteAtomic` walks once, removes the staging file only if the rename didn't happen, and skips a Windows Chmod that can't change anything. The Sync, rename and directory fsync are untouched.
- 2ffe95c: within one Search call, each chunk referenced by two or more candidates is read once, through a sync.Once around `getObject`.
  - What is shared is `getObject`'s own outcome after the length check and content hash, or its refusal. It is keyed by hash and indexed length.
  - It lives for one call only and holds at most half of maxScanBytes, so it is not a cache.
- f4b5376: one zstd encoder for the whole process, with min(4, GOMAXPROCS) slots. EncodeAll is documented as concurrency-safe, and object bytes equal what a fresh encoder writes.
- 69d8753: countFold and indexFold jump between offsets whose byte matches the needle's first byte (bytes.IndexByte). Results match the old every-offset scan on 50,000 generated inputs.
- Tests only: 6f5f3ff adds BenchmarkPutObject_NovelChunk (no budget); 3d712ed fixes an errcheck finding in a new test.
- Evidence: 5ed352f, and fdb2ea9 with sha-map.txt. I reworded two subjects that were over 64 characters with git filter-branch (messages only); trees are identical, and the map translates the old ids that the logs quote.

This changes a recorded analysis: performance-analysis.md §2 and §9.3 recommended keeping Lstat and SameFile. On the Windows fast path they are replaced by the no-follow handle check, which keeps what they protected and removes the window between check and read. Its §3 conclusion that no write-path win exists is superseded by the encoder finding.

3. FAILING-FIRST
- TestSearch_ReadsASharedChunkOncePerSearch: a read-only store counts every rejected read. Three candidates sharing one damaged chunk counted 3 reads before and 1 after, and none of them is returned either way.
- TestEncode_KeepsItsEncoderAcrossGarbageCollection: before, an Encode after two collections allocated 29,354,608 bytes; after, it stays under one match table.
- The leaf tests (socket, directory, symlink to valid bytes, FIFO, no-follow open, Windows branch selection) pin guarantees that had no test before. They pass on both old and new code, because removing syscalls has no behavioural failing-first form.

4. BEFORE → AFTER (median; paired ratio; rounds where final was faster; sign-test p)

Windows, lower-load batch (CPU median 49-53%):
- GetChunk: 122.0 → 86.1 µs; −42%; 12/12; p<0.001. Allocs 22 → 9.
- OpenSpan_4KB_of_4MB: 201.3 → 119.0 µs; −42%; 12/12; p<0.001.
- PutBytes_100KB_Cold: 33.9 → 35.0 ms; ratios 0.86-1.55; 4/8; p=1.0, so no measurable change. Allocs 1,198 → 358; B/op 4.6 MB → 1.15 MB.
- PutBytes_100KB_Warm: 8.2 → 6.8 ms; not significant.
- PutObject_NovelChunk: 2.41 → 1.82 ms; −14%; not significant. Allocs 118 → 16.
- Search_1000Roots at 55-72% CPU: 1,064 → 75 ms; every round's ratio 0.02-0.31; 3/3. Allocs 36.5k → 7.4k.
- Search_1000Roots_DistinctChunks: 296 → 375 ms; not significant.

Windows, ~82% CPU batch: GetChunk 251.9 → 148.9 µs (−38%, 16/20, p=0.012); OpenSpan 453.7 → 229.3 µs (−49%, 19/20, p<0.001).

Windows process CPU per op:
- GetChunk: 234 → 111 µs; −53%; 8/8; p=0.008.
- NovelChunk: −14%; 7/8; p=0.07.
- Cold PutBytes, including per-iteration store setup: −3%; not significant.

Linux:
- GetChunk (load about 4.7): 34.7 → 29.9 µs; not significant. Process CPU per op 109 → 70 µs; −36%; 8/8; p=0.008. Allocs 15 → 10.
- OpenSpan: 36.1 → 34.6 µs.
- Search_1000Roots (load 6.5-10): 51.9 → 15.3 ms; every round's ratio 0.23-0.36; 4/4.
- DistinctChunks: 43.5 → 38.7 ms; not significant.
- Cold on overlay disk: 75.2 → 69.6 ms; not significant.
- Cold on tmpfs: 26.9 → 14.2 ms; not significant. Allocs 727 → 333.
- Warm: 10.4 → 9.1 ms; not significant.

Linux syscalls:
- GetChunk: 11 → 7.
- Cold put: 176.7 → 107.7; fsync count unchanged at 2.
- One Search: 16,896 → 3,598; opens 1,536 → 514.

Full per-round tables and raw outputs are in runs/ab; the analysis scripts are in runs/ab/tools.

5. CRITERION CHANGES: none. One new test constant, oneMatchTableBytes (1<<17 entries × 8 bytes), is the structural size of one klauspost match table, not a timing budget.

6. OWNER PROPOSALS (no status changed)

SP20-D2:
- Linux, the reference platform, is already inside every budget with the changes, even under load: GetChunk 29.9 µs vs 60, OpenSpan 34.6 vs 150, Search_1000Roots 15.3 ms vs 25.
- Windows: GetChunk 86 µs at 49% CPU. Against the V5 quiet base of 88.2 µs, the measured ratio of 0.58 projects about 51 µs quiet; that is a projection, not a measurement. OpenSpan is 119 µs, inside 150. Search_1000Roots is 57-173 ms against 25, and DistinctChunks 190-446 ms.
- What remains over, and why: one CreateFile, two ReadFile and a CloseHandle per object under Windows filter drivers, and the decode and hash that verify-on-read requires.
- Recommendation: keep verify-on-read (no cache, no verify-at-publication-only) and take these changes.
  - Mark the row fixed for the reference platform once the quiet C5.2 Linux run confirms GetChunk ≤ 60 µs and Search ≤ 25 ms.
  - Set a Windows platform-scoped budget from its quiet run; mark it fixed if quiet GetChunk comes in ≤ 60 µs. Scope Windows Search to its quiet figure.
  - Regenerate the bench baseline at quiet with at least 10 samples.

SP06-D2:
- Warm: recommend wontfix as a store budget. Its premise, "a pure index lookup", contradicts the required pipeline order (Qompack.md §8.1 item 1, §13 invariant 7), and the cost lives in canonicalization, MinHash and redaction. If a warm-path target is wanted, re-home it to internal/canon and internal/sketch.
- Cold, current figures, all co-loaded:
  - Linux tmpfs: 14.2 ms (V3 quiet: 5.1-5.9 ms).
  - Linux disk: 69.6 ms, fsync-bound.
  - Windows: 35.0 ms at about 53% CPU (V5 quiet: 15.74 ms).
- Why cold is still over: every novel chunk needs a presence check, create, write, close, rename and usually a directory create, plus the pending marker's fsyncs. Removing those would change durability (SP-20 invariant 9) or the object layout (§7.4 fanout, one file per chunk).
- Recommendation: re-budget per platform from C5.2's quiet run.
  - The reference budget comes from a Linux tmpfs store (the CPU path).
  - Disk-backed and hosted rows become report-only, the same policy as the hosted fsync tail (Q1).
  - Windows gets a platform-scoped budget.
  - Then fixed against those, or wontfix for the literal 3 ms / 400 µs.
- The encoder fix also applies to SP08-D1's OnToolUse (the perfobs lane).

7. TESTS (Linux runs as non-root qtest, on bundles of the exact commits)
- Full store and paths packages pass on both platforms at the final code, apart from one known timing test.
- TestGC_DeadlineOvershootIsBoundedByTheCheckInterval failed under co-load on both platforms. Re-run alone, it passed on both, so it was co-load, not a defect.
- Also passing: focused -race on Windows, test/platform long/unicode/space roots, the security, mcp and cli fsck consumers, go vet on all target platforms, fmt-check, and the devtool lint subset.
- devtool lint bindeps fails, but it fails identically on cf31e01: golang.org/x/sys/unix reaches the binary via internal/paths rename_noreplace_{linux,darwin}.go and syncdata_linux.go, from 3ab1523.

### Commits

- 5213545 perf(store): open object leaves once without following links
- 07ce85e perf(store): build object paths in one allocation
- 182ca37 perf(store): create fanout leaves before probing them
- e86c700 perf(store): open staging files non-blocking on unix
- 2e861ea perf(paths): return short absolute paths from Long directly
- 4f6873c perf(paths): skip the root walk for unprotectable paths
- 592f148 perf(paths): walk once and skip no-op work in WriteAtomic
- 2ffe95c perf(store): read each shared chunk once per Search
- f4b5376 perf(store): keep one zstd encoder for the process
- 6f5f3ff test(store): benchmark the per-novel-chunk object write
- 3d712ed test(store): check the read-only store type in the Search test
- 69d8753 perf(store): skip to candidate offsets in Search's text scan
- 5ed352f docs(v6): record perfstore close-out evidence
- fdb2ea9 docs(v6): map perfstore commit ids across a subject reword

### Tests

- `Windows: go test ./internal/store -count=1 -timeout=60m -v (at 69d8753)` — PASS, 487 passed, 1 pre-existing skip (symlink privilege) - runs/win-full-store-f6f7ff6.txt
- `Windows: go test ./internal/store -count=1 -timeout=60m -v (at 6f5f3ff, before the Search scan commit)` — 485 passed, 1 FAIL TestGC_DeadlineOvershootIsBoundedByTheCheckInterval (co-load, two deadline checks cost 371 ms); re-run alone at 3d712ed PASS (overshoot 30 ms vs limit 77 ms) - runs/win-full-store-final.txt, runs/win-gc-overshoot-alone-final.txt
- `Windows: go test ./internal/paths -count=1 -timeout=30m -v` — PASS - runs/win-full-paths-final.txt
- `Windows: CGO_ENABLED=1 go test -race ./internal/store -run 'TestSearch|TestNewSharedChunkReads|TestEncode_|TestStoreCompress|TestReadBoundedObject|TestGetChunk|TestOpenObjectLeaf|TestEnsureDir|TestPutObject' -count=1 -timeout=30m` — PASS (361 s) - runs/win-race-focused.txt
- `Windows: go test ./test/platform -run 'TestPlatform_ProjectRootShapes|TestPlatform_MixedCaseProjectRoot' -count=1 -timeout=30m -v` — PASS incl. long/unicode/spaces roots - runs/win-platform-rootshapes.txt
- `Windows: go test ./test/security -run TestSecurity_MalformedObjectsAreQuarantinedAndBounded; ./internal/mcp -run 'Damaged|TestExpand|TestReRead|Recall|Search'; ./internal/cli -run 'TestFsck_TheObjectSizeLimitIsTheStoresOwn|TestFsck_CleanProjectFindsNoDefect'` — PASS x3 - runs/win-consumers-focused.txt
- `Linux (non-root qtest, GOMAXPROCS=4): go test ./internal/store -count=1 -timeout=30m -v (at 69d8753)` — 491 passed, 1 platform skip, 1 FAIL TestGC_DeadlineOvershootIsBoundedByTheCheckInterval host-sanity under co-load; re-run alone PASS (overshoot 66 ms vs limit 91 ms) - runs/linux-full-store-f6f7ff6.txt, runs/linux-gc-overshoot-alone-f6f7ff6.txt
- `Linux (non-root): go test ./internal/store and ./internal/paths -count=1 -timeout=30m -v (at 6f5f3ff and 592f148)` — PASS (store 491 / paths 101 passed, platform skips only) - runs/linux-full-*.txt
- `Failing-first: go test ./internal/store -run TestSearch_ReadsASharedChunkOncePerSearch (pre-change code)` — FAIL expected 1 actual 3 reads; after change PASS - runs/win-search-shared-read-failing-first.txt, win-search-shared-read-after.txt
- `Failing-first: go test ./internal/store -run TestEncode_ (pre-change code)` — FAIL allocated 29354608 bytes per post-GC Encode; after change PASS - runs/win-encoder-failing-first.txt, win-encoder-after.txt
- `go run ./tools/devtool fmt-check; go vet ./internal/store ./internal/paths (GOOS=windows|linux|darwin, freebsd build)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,coveragefloors (final head)` — all PASS except bindeps, which fails identically on cf31e01 (pre-existing x/sys/unix) - runs/win-devtool-lint-final.txt, runs/base-cf31e01-devtool-lint-bindeps.txt
- `Benchmarks: interleaved ABBA A/B, base (cf31e01 + two new benchmarks) vs final, both platforms; strace syscall counts in a throwaway golang:1.26.6-bookworm container; process-CPU A/B` — see summary section 4; raw per-round outputs and load logs in runs/ab, strace counts in runs/linux-syscount

### Criterion changes

- None. No assertion, threshold, budget, golden, timeout or skip was changed. New tests only add assertions. The one new absolute number, oneMatchTableBytes = (1<<17)*8, is the structural size of one klauspost SpeedDefault long match table, used to tell 'used an encoder' from 'built one'; the two sides differ by more than two orders of magnitude, and it is not a timing budget.

### Open issues

- plans/sdd/V6-closeout/perfstore/report.md was not written: the harness blocks subagents from writing report .md files. The full report is the summary field; the coordinator can commit it as report.md.
- All timings were taken under heavy co-load (Windows CPU 34-91%, Linux load 4-10). Write-path timing changes are not measurable at this noise; read and Search gains are. The quiet C5.2 re-measure is needed before any budget number.
- devtool lint's stubskips, runpatterns and docmarkers sub-checks were not run: stubskips runs the whole tree, which the brief reserves for the coordinator. My first full lint run was stopped inside stubskips (only my own process tree).
- Pre-existing bindeps failure: golang.org/x/sys/unix reaches the shipped binary through internal/paths rename_noreplace_{linux,darwin}.go and syncdata_linux.go (from 3ab1523). It fails identically on cf31e01 and was not changed here.
- 182ca37 (create-first fanout dirs) only saves syscalls; a probe measured no timing effect on either platform. It is an independent commit the coordinator may drop.
- Not attempted: renaming staged objects by handle on Windows (would save one path open per novel chunk), and packing objects into fewer files (an architectural change). These are the remaining Windows write-path levers.
- TestGC_DeadlineOvershootIsBoundedByTheCheckInterval fails under co-load on both platforms and passes alone. Integrated whole-tree runs may hit it.
- Windows win2-search base round 2 exited 127 mid-run with no error text (possibly an external kill); that round's DistinctChunks pair is excluded. The Linux final Search A/B was stopped after 4 of 6 rounds; all 4 rounds favoured final with ratios 0.23-0.36.
- Two commit subjects were reworded with git filter-branch (messages only, trees identical). Logs and file names quoting the old ids are mapped in runs/sha-map.txt.
- In the Linux container I created user qtest (uid 10002) and my own directories /work/cx-perfstore-{base,wip1,head,final,final2,final3,basebench,basebench2,ab,ab2,mkdirprobe} plus bundles and scripts. They are left in place for inspection.

### Needs the owner

- SP06-D2 disposition. Proposed: warm 400 µs becomes wontfix as a store budget, optionally re-homed to internal/canon and internal/sketch. Cold is re-budgeted per platform from the quiet C5.2 run: Linux tmpfs as the reference (V3 quiet 5.1-5.9 ms), disk-backed and hosted rows report-only (fsync-bound, Q1 policy), Windows platform-scoped (V5 quiet 15.74 ms). Then fixed against those, or wontfix for the literal 3 ms.
- SP20-D2 disposition. Proposed: keep verify-on-read with no cache. Mark fixed on the reference platform once quiet Linux confirms GetChunk <= 60 µs and Search_1000Roots <= 25 ms (co-loaded final: 29.9 µs and 15.3 ms). Windows platform-scoped: GetChunk 86 µs co-loaded, about 51 µs projected quiet; Search 57-173 ms co-loaded. Regenerate the bench baseline at quiet with at least 10 samples.
- Accept or reject the Windows object-read fast path. It replaces the pre-open Lstat and SameFile with a no-follow handle check (reparse points still get the old check), which alters the recommendation in plans/sdd/V6-remediation/performance-analysis.md §2 and §9.3.
- Whether the new test constant oneMatchTableBytes (1 MiB, the structural size of one klauspost match table) in TestEncode_KeepsItsEncoderAcrossGarbageCollection counts as a new budget number under the checklist rule.
- The pre-existing bindeps violation (x/sys/unix in internal/paths) needs a 00-ARCHITECTURE §2.5 decision: extend the allow-list, or port those three files to syscall.

## Independent review

### review:perfstore:both: needs-fixes

- **minor** `plans/sdd/V6-closeout/perfstore/ (no report.md); plans/sdd/V6-remediation/performance-analysis.md:41-46,144-145,164` — The workstream's required deliverables are missing from the tree: the report, with before/after distributions and the per-row owner proposals, and any record that the Windows read fast path overrides a written guardrail. The implementer disclosed that report.md is absent, but nothing in the branch says that performance-analysis.md §2 ('Path verification — keep … do not remove it'), §7 item 4 ('Every A/B keeps the … Lstat/Fstat/SameFile path checks') and §9.3 are superseded on Windows.
  - Evidence: `ls plans/sdd/V6-closeout/perfstore` lists only `runs/`. object_open_windows.go:36-55 replaces the pre-open Lstat and the SameFile with a FILE_FLAG_OPEN_REPARSE_POINT handle check. That check is at least as strong: the leaf is never followed, a reparse leaf falls back to the old checked path, and there is no window between check and read. It still contradicts the recorded guardrail, and the only in-tree record of the change is the code comment.
  - Fix: The coordinator commits the implementer's summary as plans/sdd/V6-closeout/perfstore/report.md, and appends a dated 'superseded by C2.6 (5213545), pending owner acceptance' note to performance-analysis.md §2, §7.4 and §9.3, so the guardrail and the code do not disagree silently.
- **minor** `implementer summary §6 (SP20-D2 owner proposal)` — The owner proposal credits these changes with bringing Linux GetChunk and OpenSpan inside budget, but the paired Linux base was already inside for both. On Linux only Search_1000Roots moved from over to under budget. Under heavier load the base -count 10 run reads 77-107 µs, so any Linux GetChunk figure here depends on load. That changes what 'fixed on the reference platform' means for the owner.
  - Evidence: Summary §4 gives Linux GetChunk as 34.7 → 29.9 µs, not significant, and OpenSpan as 36.1 → 34.6 µs, while §6 says Linux 'is already inside every budget with the changes'. runs/ab/linux/base-linux-c10.txt shows BenchmarkGetChunk-4 at 77,257-106,789 ns/op at base. Search_1000Roots at 51.9 → 15.3 ms, with every round's ratio between 0.23 and 0.36, is the genuine Linux fix.
  - Fix: When the report is committed, reword the SP20-D2 proposal. On Linux, GetChunk and OpenSpan were inside budget before this work under paired load, and their state rests on the quiet C5.2 run. Search_1000Roots is the Linux component these commits fixed. GetChunk's overage is Windows-only.
- **nit** `commit 5ed352f message body` — The evidence commit message quotes 23e67d2, which filter-branch left unreachable.
  - Evidence: `git merge-base --is-ancestor 23e67d2 HEAD` fails. runs/sha-map.txt maps 23e67d2 to 2ffe95c, and the file names linux-full-store-f6f7ff6.txt and linux-full-store-4ea3ec2.txt also quote pre-rewrite ids.
  - Fix: Acceptable as is, because sha-map.txt is in the same tree. If the branch is restacked on integration, substitute the reachable ids (2ffe95c, 69d8753, 592f148) in the message and the file names.
- **nit** `plans/sdd/V6-closeout/perfstore/runs/linux-syscount/straces.tar.gz` — About 3.4 MB of evidence goes into permanent history of a public repository. That includes a 2.3 MB gzipped binary of raw strace output, which the committed per-op .counts files and syscount.sh already reproduce. I checked the traces and they contain no secrets.
  - Evidence: `git ls-tree -r -l HEAD plans/sdd/V6-closeout/perfstore` totals 3,375,358 bytes. The tarball alone is 2,350,271 bytes.
  - Fix: Optionally drop straces.tar.gz before integration, or move it to a CI artifact or release asset, and keep the .counts files, the harness and the script.

## Fix seat (review resolution) — status `partial`, head `21402a6f38638c1a100470673a29cf12cd311ca9`

### Root cause

Both findings are about the record, not the code. R1: the harness blocked the implementer's report write, and nothing in the tree recorded that 5213545 overrides the performance-analysis.md guardrails on Windows. R2: the implementer's §6 read an intermediate-load paired run as proof that the changes brought Linux GetChunk and OpenSpan inside budget. The runs show OpenSpan was always inside and GetChunk's position depends on load; only Search_1000Roots is a clear Linux fix, delivered by 69d8753.

### Summary

Both review findings are correct, and both are fixed except for one step. report.md is still not in the tree. The harness refuses report-file writes from subagents: the implementer's Write was refused with "Subagents should return findings as text, not write report files", and my instructions carry the same rule. So the coordinator has to commit plans/sdd/V6-closeout/perfstore/report.md, which is also what the reviewer's own fix proposes. Its content is:
- the implementer's summary, found in its StructuredOutput or in the Write draft inside transcript agent-ad5cd11a5399140bd.jsonl;
- with §6's SP20-D2 proposal replaced by the text under R2 below;
- followed by the "Review resolution" section below.

Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-perfstore, branch closeout/perfstore, head 21402a6. There is no code change in this round, so I ran nothing on Linux.

----- BEGIN section to append to report.md -----
## Review resolution (fix seat, 2026-09-22)

### R1 (minor): report.md missing; nothing records that the Windows read fast path overrides performance-analysis.md §2, §7.4 and §9.3

**Verified.**
- At fdb2ea9, `plans/sdd/V6-closeout/perfstore/` held only `runs/`.
- No `*.go` or `*.md` file references performance-analysis.md.
- `plans/sdd/V6-remediation/` was unchanged since cf31e01.

I checked the code claim myself:
- `openObjectLeaf` (object_open_windows.go:36-55) takes its Stat from the same handle it reads.
- Go 1.26.6 `syscall.Open` passes the high FILE_FLAG_* bits through to CreateFile and rejects unknown bits with ErrInvalid (GOROOT/src/syscall/syscall_windows.go ~400-410). So FILE_FLAG_OPEN_REPARSE_POINT is never silently dropped.
- The same `syscall.Open` adds FILE_FLAG_BACKUP_SEMANTICS on read opens, so a directory opens and is then refused by IsRegular.
- The reparse fallback `openObjectChecked(long, 0)` is the same Lstat/Open/Stat/SameFile sequence as cf31e01.

The reviewer's "at least as strong" holds.

**Action.** 418e871 adds dated notes to performance-analysis.md, each marked pending owner acceptance:
- §2, §7 item 4 and §9 item 3 each get a note that C2.7 (5213545) superseded or changed them.
- §2's premise that SameFile costs no syscall is corrected: on Windows, SameFile after a path-based Lstat reopens the path (loadFileId).
- §4 gets a note on the zstd encoder rebuild (f4b5376), which its "nothing on the write path" claim missed.
- §10's "preserved, unchanged" list is scoped to W1+W2.

report.md itself is left to the coordinator (harness rule; see the top of this summary).

**Evidence.**
- runs/fix-win-leaf-focused.txt: `go test ./internal/store -run 'TestOpenObjectLeaf|TestReadBoundedObject|TestGetChunk_Refuses|TestGetChunk_ADirectory|TestOpenObjectChecked' -count=1 -v` at fdb2ea9 on Windows. PASS, exit 0.
- runs/fix-devtool-lint-docmarkers.txt: PASS.

**Coverage gap (not a defect).** On Windows, the reparse branch is pinned only by an AF_UNIX socket leaf. This host cannot create symlinks: TestMaintenance_SymlinkComponentInBackupTreeRefused skips. A symlink leaf takes the same attribute-driven branch, but no Windows test creates one.

### R2 (minor): the SP20-D2 proposal credits the changes with bringing Linux GetChunk and OpenSpan inside budget

**Verified in part.** The implementer's §6 does overclaim. The reviewer's replacement wording is itself too strong for GetChunk. I re-derived the figures from the committed rounds with runs/ab/tools/table.py and paired.py.

**OpenSpan.** On Linux it was inside 150 µs in every run before this work:
- lx2-read base median 36.1 µs;
- lx-read base median 63.0 µs;
- base -count 10 run: 29-123 µs.

The reviewer is right, and these commits do not change OpenSpan's status.

**GetChunk.** Where the Linux figures sit against 60 µs depends on load, before and after, and so does the gain:

| Run | Compared | Load | Rounds | Base → final median | Paired result |
|---|---|---|---|---|---|
| lx2-read | cf31e01 vs 6f5f3ff | ~4.5 | 20 | 34.7 → 29.9 µs | 13/20, p=0.26 |
| lx-read | cf31e01 vs 592f148 | 3.8-5.4 | 12 | 78.7 → 59.1 µs | 8/12, p=0.39 |
| lcpu-getchunk | cf31e01 vs 6f5f3ff | ~8.8 | 8 | 80.1 → 57.4 µs | faster 8/8, geomean 0.72, p=0.008 |
| base -count 10 | cf31e01 only | 4.8 → 9.5 | 10 | 77-107 µs | — |

In lcpu-getchunk, base was over 60 µs in 6 of 8 rounds and final in 4 of 8. So "inside before this work" holds in one paired run only, and "GetChunk's overage is Windows-only" is not established. Three gains do not depend on load: syscalls 11 → 7, allocations 15 → 10, and process CPU per op −36 % (8/8).

**Search_1000Roots.** This is the one Linux benchmark these commits changed by a large, consistent paired margin:
- lx3-search, cf31e01 vs 69d8753, load 7-10: 51.9 → 15.3 ms, every round's ratio 0.23-0.36, 4/4.
- Per Search, syscalls fell from 16,896 to 3,598 and allocations from 25,682 to 7,787.
- The base's own position against 25 ms also depends on load. In lx-search (cf31e01 vs 2ffe95c, before the scan change) the base median was 22.4 ms, under budget in 6 of 8 rounds, and 2ffe95c alone moved nothing (+3 %, 4/8).

So on Linux the Search gain comes from 69d8753's text scan.

**Windows.** Final GetChunk was over 60 µs in every co-loaded round (minimum 67.6 µs).

**Action.** Replace report §6's SP20-D2 proposal with:

> **SP20-D2** (proposal; no status changed).
>
> *Linux, the reference platform.*
> - OpenSpan was inside 150 µs before this work in every run; these commits do not change its status.
> - GetChunk's position against 60 µs depends on load, before and after: across three paired runs, base medians were 34.7, 78.7 and 80.1 µs, and final medians 29.9, 59.1 and 57.4 µs.
> - The commits cut syscalls (11 → 7), allocations (15 → 10) and CPU per op (−36 %) regardless of load. They give a significant wall-clock gain only under heavier load (8/8 rounds, geomean 0.72). Linux GetChunk's budget status therefore rests on the quiet C5.2 run, not on these commits.
> - Search_1000Roots is the Linux benchmark these commits fixed: 51.9 → 15.3 ms paired, every round's ratio 0.23-0.36, driven by 69d8753.
>
> *Windows.*
> - GetChunk: 86 µs at about 49 % CPU, and over 60 µs in every co-loaded final round. A quiet figure of about 51 µs is a projection, not a measurement.
> - OpenSpan: 119 µs, inside 150.
> - Search_1000Roots: 57-173 ms against 25.
>
> *Recommendation.*
> - Keep verify-on-read, with no cache.
> - Mark the row fixed for the reference platform only if the quiet Linux C5.2 run gives GetChunk ≤ 60 µs and Search_1000Roots ≤ 25 ms, and record that the GetChunk result belongs to the quiet host rather than to this change.
> - Scope the Windows GetChunk and Search budgets to Windows' own quiet run.
> - Regenerate the bench baseline on a quiet host with at least 10 samples.
----- END section -----

**Recorded decision.** 418e871 marks the performance-analysis.md §2, §7.4 and §9.3 guardrails as changed by 5213545 on Windows, pending owner acceptance. It adds no new decision.

**Checks.** No budget constant, CARRIED-DEFECTS status, assertion, golden, threshold or timeout was changed, and there is no criterion change.

### Commits

- 418e871 docs(v6): note the Windows object-read change in the analysis
- 21402a6 docs(v6): record perfstore fix-seat verification runs

### Tests

- `Windows: go test ./internal/store -run 'TestOpenObjectLeaf|TestReadBoundedObject|TestGetChunk_Refuses|TestGetChunk_ADirectory|TestOpenObjectChecked' -count=1 -v -timeout=30m (at fdb2ea9)` — PASS, exit 0. Five tests ran; the symlink, FIFO and no-follow cases are unix-only files. Log: plans/sdd/V6-closeout/perfstore/runs/fix-win-leaf-focused.txt
- `go run ./tools/devtool lint --only=docmarkers (with the analysis edit)` — PASS, 257 plan documents scanned. Log: plans/sdd/V6-closeout/perfstore/runs/fix-devtool-lint-docmarkers.txt
- `go run ./tools/devtool fmt-check` — exit 0
- `python runs/ab/tools/table.py and paired.py over runs/ab/linux {lx2-read, lx-read, lx3-search, lx-search}, runs/ab/windows {win2-read, win3-read}, plus a paired recomputation of linux/lcpu-getchunk-cpu.tsv` — These re-derived the figures quoted in R2. Linux: GetChunk base medians 34.7 / 78.7 / 80.1 µs; lcpu-getchunk 8/8 rounds faster, p=0.008; Search 51.9 → 15.3 ms (4/4); lx-search base median 22.4 ms. Windows: final GetChunk min 67.6 µs.

### Open issues

- report.md is not in the tree. The harness refuses report-file writes from subagents, for the implementer and for this seat. The coordinator must commit plans/sdd/V6-closeout/perfstore/report.md: the implementer's summary, with §6's SP20-D2 proposal replaced by R2's text, plus the Review resolution section in this summary.
- Windows test coverage: the reparse-point fallback in openObjectLeaf is pinned only by an AF_UNIX socket leaf. This host has no symlink privilege, so no Windows test creates a symlinked object leaf. A privileged or Developer Mode run could add a direct symlink case.
- All earlier timing caveats still apply. Every figure is co-loaded, and only the quiet C5.2 run can place Linux GetChunk and Search against 60 µs and 25 ms.
- The implementer's other open items are unchanged: the pre-existing bindeps failure (x/sys/unix in internal/paths); the stubskips, runpatterns and docmarkers lint left to the integrated gate (docmarkers now passes on this branch); and TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, which fails under co-load and passes alone.

### Needs the owner

- Accept or reject the Windows object-read fast path (5213545). performance-analysis.md §2, §7.4 and §9.3 now carry dated notes marking the change as pending owner acceptance.
- SP20-D2 disposition, per the reworded proposal. Keep verify-on-read. Linux GetChunk's status rests on the quiet C5.2 run (co-loaded base medians 34.7-80.1 µs), and Search_1000Roots is the Linux benchmark these commits fixed. Windows GetChunk and Search get platform-scoped budgets from Windows' own quiet run.
- SP06-D2 disposition, as the implementer proposed: warm becomes wontfix as a store budget, and cold is re-budgeted per platform from the quiet C5.2 run.
- Whether oneMatchTableBytes, the structural 1 MiB bound in TestEncode_KeepsItsEncoderAcrossGarbageCollection, counts as a new budget number.
- The pre-existing bindeps allow-list decision for x/sys/unix in internal/paths.


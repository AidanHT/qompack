# V6 store-performance analysis — SP06-D2 / SP08-D1 / SP20-D2

**Owner:** V6 store-performance investigator (Opus 4.8, high effort).
**Status:** READ-ONLY inspection. No code changed; no test/build/benchmark was run (other authoring is
active). Findings only; no implementation until coordinator review. Main owns shared contracts and
durable-data decisions.
**Method:** current source (`internal/store`, `internal/observer`), `plans/CARRIED-DEFECTS.tsv`, and
the benchmark definitions. Numbers below are the carried measurements; I did not reproduce them.

## 1. The three carried defects (current record)

| ID | Bench | Budget | Carried measurement (Windows, V5) | Root path |
|---|---|---|---|---|
| **SP06-D2** | `BenchmarkPutBytes_100KB_Cold/Warm` | 3 ms / 400 µs | 15.74 ms / 5.65 ms; cold allocs +28% since wave-3; **never measured on the reference (Linux) platform** | `PutBytes` → per-novel-chunk `putObject` write |
| **SP08-D1** | `BenchmarkOnToolUse_TestOutput256KB` | B-C `l0_process` p99 < 50 ms | 53–115 ms (one changed line); 262–655 ms (all novel) | `observer.OnToolUse` → one `Store.PutBytes`, dominated by the per-novel-chunk object write |
| **SP20-D2** | `BenchmarkGetChunk`, `Search_1000Roots`, `OpenSpan_4KB_of_4MB` | 60 µs / 25 ms / 150 µs | 85–88 µs (26 allocs vs wave-3 45 µs/18); 82 ms vs 25 ms; span 19→27 allocs, 316–333 µs at base clock | verify-on-read (`readBoundedObject` + `getObject`) added Lstat + fstat + plaintext content-hash to every object read |

SP08-D1 is SP06-D2's write path at 256 KB scale (`OnToolUse` adds no per-chunk work of its own —
`internal/observer/tooluse.go:116` is a single `PutBytes`). Treat them as one write-path root cause.

## 2. Read path (SP20-D2): mandatory vs metadata vs waste

The read path is `GetChunk`/`OpenSpan` → `getObject` → `readObjectFile` → `readBoundedObject`
(`internal/store/objects.go:256-334`, `read.go:24-42,147-170`). Per object read:

1. `objectCandidates(h)` — `hexOf` (64-char hex string) + `objectDir` (`filepath.Join`) + two more
   `filepath.Join`. **~4–5 path allocs.**
2. `readBoundedObject`: `os.Lstat(paths.Long(path))` → **1 metadata syscall + 1 FileInfo alloc + 1
   `paths.Long` alloc**; regular-file check; `os.Open(paths.Long(path))` → **1 open + a 2nd
   `paths.Long` alloc**; `f.Stat()` → **1 fstat + 1 FileInfo alloc**; `os.SameFile`; then
   `io.ReadAll(io.LimitReader(f, limit+1))`.
3. `getObject`: `Decode` (zstd) if compressed; length cross-check; `core.HashBytes(DomainChunk, plain)`.

Classification:

- **Mandatory — keep (verify-on-read integrity):** `core.HashBytes` over the plaintext
  (`objects.go:451`), the zstd `Decode`, the indexed-length cross-check. These are the point of
  16ecc77 and the task forbids removing them. `HashBytes` is already 0-alloc (`core/hash.go:64`,
  guarded by `TestHashBytes_DoesNotAllocate`); its cost is CPU proportional to plaintext size, ~a few
  µs for a 4–8 KB chunk, and platform-independent.
- **Path verification — keep (Windows metadata cost, not waste):** the pre-open `Lstat` (refuses a
  symlink/reparse leaf before following it), the post-open `Fstat`+`SameFile` (swap-during-open TOCTOU
  guard). These are two extra metadata syscalls on top of the open. On Linux they are ~1–2 µs each; on
  Windows NTFS they are ~5–15 µs each — **this is the bulk of the SP20-D2 Windows overage and it is a
  filesystem constraint, not algorithmic waste.** `SameFile` is arguably redundant with the downstream
  content hash for *integrity*, but it is cheap (no syscall) and is path verification; do not remove it
  to make timing pass.
- **Algorithmic waste — safe to remove (both platforms, zero safety loss):**
  - **W1 — `io.ReadAll` regrowth in `readBoundedObject` (objects.go:326).** `limit` is
    `encodedObjectLimit()`/`MaxPutBytes` (up to 64 MiB), so `io.ReadAll` starts at 512 B and doubles to
    the object's actual size — `~log2(size/512)` allocations plus an O(size) copy per doubling, and a
    transient ~2× over-allocation. But `opened.Size()` is already known and already checked against
    `limit` at line 323. Replace with `buf := make([]byte, opened.Size()); io.ReadFull(f, buf)`: **1
    alloc, 1 read, no over-allocation, tighter bounded memory.** A short read (`ErrUnexpectedEOF`) and
    any wrong bytes are still caught (content hash); content-addressed objects are immutable and O_EXCL
    published, so they never grow in place. This is the single highest-value read fix and it helps
    every read bench — `GetChunk`, `OpenSpan`, and especially `Search_1000Roots`, which streams many
    candidate chunks through this path.
  - **W2 — double `paths.Long(path)` in `readBoundedObject`.** Lstat and Open each call
    `paths.Long(path)`; on Windows each allocates the `\\?\`-prefixed string. Compute it once. **−1
    alloc/read on Windows.**
  - **W3 (low value on default config) — candidate ordering.** `readObjectFile` always tries `.zst`
    first. On a `store.compression = none` store every read eats a failed `.zst` Lstat before the bare
    hit; ordering candidates by `s.compressing()` removes it. Default is compression-on, so this only
    helps the non-default store; list it, don't prioritize it.

Expected read effect of W1+W2: roughly **26 → ~20–22 allocs/op** and removal of the geometric copy
overhead. This narrows the gap on both platforms but **will not by itself close 85 µs → 60 µs on
Windows**, because the residual is the two metadata syscalls (Lstat+Fstat) and the hash — the first is
platform, the second is mandatory. See §5.

## 3. Write path (SP06-D2 / SP08-D1): mostly a filesystem constraint

Per novel chunk, `putObject` (`objects.go:196-227`) does: `objectWritten` (1 stat) → `Encode` (zstd) →
`ensureDir` (process-cached, ~free) → `tmpObjectPath` (`rand.Read(6)` + hex) → `writeStaged`
(`OpenFile O_EXCL` + `Write` + `Close`, **no fsync** — objects/ durability is SP-20's publication
barrier, not this path) → `renameObject` (rename, retried on Windows). That is ~5 syscalls per novel
chunk and it is already close to minimal.

- **Windows filesystem constraint (dominant), not algorithmic waste:** every novel chunk is its own
  small file. On Windows each freshly written file is scanned by Defender, so `renameObject`
  (`objects.go:168-184`) hits `ERROR_SHARING_VIOLATION`/`ACCESS_DENIED` and spins its
  `runtime.Gosched` retry loop; plus NTFS metadata ops (stat/open/close/rename) are individually slow.
  For 256 KB all-novel (`~tens of chunks`), this is what produces 262–655 ms. **No fsync/hash/path
  removal changes this**, and the batching that would (packing many chunks into one file) is an
  architectural change, out of scope for "smallest compatible correction."
- **Not measured on the reference platform (the biggest single SP06-D2 fact).** The 3 ms/400 µs budgets
  were set against Linux; the carried numbers are Windows-only. A large fraction of the 5.2×/14.1×
  overage is expected to be the per-file AV + NTFS metadata cost above, absent on Linux. **The first
  action for SP06-D2/SP08-D1 is to measure on Linux, not to change code.**
- **Algorithmic notes (small):** the `objectWritten` stat (objects.go:103) is wasted on all-novel and
  essential on warm/dedup; **do not** replace it with the in-memory `chunkSet` — that map is an
  "optimistic presence hint, not a durability witness" (`writeStaged`/`Has` docs), and skipping the
  write on a hint would let a reference point at a quarantined/absent object, breaking crash durability
  and verify-on-read. Keep it. `tmpObjectPath`'s `rand.Read`+hex is one small alloc/chunk; negligible
  beside the file ops.

## 4. Concrete correction list (for coordinator review — no implementation yet)

| # | Function (file:line) | Change | Retains | Expected saving |
|---|---|---|---|---|
| W1 | `readBoundedObject` (objects.go:303) | pre-size buffer to `opened.Size()` + `io.ReadFull`, drop `io.ReadAll(LimitReader)` | verify-on-read (hash still checks bytes), bounded memory (tighter), path checks unchanged | −3–5 allocs/read, no geometric copy, no 2× transient; helps Search/OpenSpan most |
| W2 | `readBoundedObject` (objects.go:303) | compute `paths.Long(path)` once | all | −1 alloc/read on Windows |
| W3 | `readObjectFile`/`objectCandidates` (objects.go:256,81) | try `s.compressing()` name first | all | −1 failed Lstat/read on `compression=none` only |

Nothing on the write path is a safe algorithmic win of comparable value; its overage is the
filesystem constraint in §3.

## 5. Windows filesystem constraint vs algorithmic waste (summary)

- **Filesystem constraint (platform, not fixable by these edits):** per-chunk small-file creation + AV
  scan + rename contention (writes); Lstat/Fstat/Open metadata latency on NTFS (reads). These explain
  most of the Windows-only overage in all three defects.
- **Algorithmic waste (fixable, both platforms, zero safety loss):** W1 read-buffer regrowth, W2
  double `paths.Long`, W3 candidate order. Real but modest; they improve allocs and copy cost, not the
  metadata-syscall floor.

The honest framing: SP20-D2 and SP06-D2 are **partly a code regression (verify-on-read allocs, W1) and
partly a platform-measurement gap.** Removing verification would "fix" the timing dishonestly and is
refused.

## 6. Safe, already-evidenced fix

**W1 (read-buffer pre-sizing)** is the safe first move. It is the same allocation discipline the repo
already committed for `core.HashBytes` (the `Sum(out[:0])` 0-alloc form, evidenced by
`BenchmarkRebuildBloom5000` 172 400 → 12 400 B/op): use the size you already know instead of letting a
stdlib helper grow a buffer. It changes no on-disk format, no durability barrier, and no verification;
the content hash downstream is unchanged, so a wrong or short read is still refused. It is the one
correction I would forward as low-risk without a contract decision.

## 7. Bounded quiet evaluation plan (proposal — do not run while other authoring is active)

1. **Reference-platform baseline first (no code change).** On a quiet Linux runner (WSL2 cross-run per
   the team's established path), run the three defect benchmarks against current `HEAD`:
   `GOMAXPROCS=2 go test -run '^$' -bench 'BenchmarkPutBytes_100KB_(Cold|Warm)$|BenchmarkGetChunk$|BenchmarkSearch_1000Roots$|BenchmarkOpenSpan_4KB_of_4MB$' -benchmem -benchtime=… -count=10 ./internal/store`
   and `BenchmarkOnToolUse_TestOutput256KB` in `./internal/observer`. This settles how much of each
   overage is Windows-only — likely most of SP06-D2/SP08-D1. Pipe carefully: `go test`'s exit code is
   masked by a pipe (known trap), so capture to a file and check status.
2. **A/B W1 (and W2) in isolation.** Same command, `-count=10+`, current vs the W1(+W2) branch; compare
   allocs/op and ns/op with the repo's bench-compare convention. Success = allocs drop and the reads
   move toward budget on the reference platform with byte-identical outputs.
3. **Windows characterization, not gating.** Re-run on Windows to record the residual metadata/AV floor
   and label it a platform characteristic (as the hosted-fsync-tail precedent does), not a code target.
4. **No fsync/hash/path removal is part of any variant.** Every A/B keeps the content hash, the
   Lstat/Fstat/SameFile path checks, and the SP-20 publication barrier.

## 8. Invariants that must not change (guardrails on any later implementation)

Privacy-before-persistence (`Redact` before canon/chunk/write, PutBytes step 1); exact fidelity (no
recompute of declared fidelity); verify-on-read (plaintext DomainChunk hash on every object read);
crash durability (SP-20 publication barrier; objects referenced only after they are durable);
bounded memory (W1 tightens it — never loosen the `MaxPutBytes`/`encodedObjectLimit` bounds); and
concurrency (the `putLock` stripes and `s.mu` discipline). W1/W2/W3 touch none of these.

## 9. Open items for the coordinator

1. Approve W1 (+W2) as the low-risk read correction to prototype, or hold for the reference-platform
   baseline in §7.1 first? (Recommend: baseline first, it may show SP06-D2/SP08-D1 are platform, and it
   costs no code.)
2. SP06-D2/SP08-D1 write-path overage is a Windows small-file/AV constraint; the only material code
   remedy is object packing (architectural, out of "smallest compatible correction"). Is a packing
   investigation in scope for V6, or is the correct V6 disposition "measure on Linux, characterize
   Windows, keep the budgets against the reference platform"?
3. `SameFile` is redundant with the content hash for integrity but is cheap path verification. Keep as
   defense-in-depth (my recommendation) or revisit? Durable-data call is main's.

## 10. Implemented — W1 + W2 (correctness only; no budget claim)

Coordinator approved the narrow W1+W2 implementation. Delivered in `internal/store/objects.go` and a new
`internal/store/object_read_buffer_test.go` (no other files).

**Change.** `readBoundedObject` now computes `paths.Long(path)` once (W2) and reads through
`readExactSize(f, opened.Size())` (W1): one buffer pre-sized from the already-bounds-checked Stat size,
`io.ReadFull`, then a one-byte EOF probe via `requireEOF`. `io.ReadAll(io.LimitReader(f, limit+1))` and
its post-read `len(raw) > limit` recheck are gone.

**Growth/append detection retained (the load-bearing part).** Reading only the prefix and hashing it
would accept an appended suffix under the valid-prefix hash; `requireEOF` refuses any trailing byte, and
`io.ReadFull` refuses a short read (a shrink). Both surface as ordinary read errors that
`readObjectFile` maps to `ErrDamaged` (wraps `core.ErrNotFound`), exactly as before.

**Preserved, unchanged:** the pre-open `Lstat`, the post-open `Fstat`+`SameFile` swap guard, the
`opened.Size() > limit` → `errObjectTooLarge` fast path, `getObject`'s indexed-length cross-check and
plaintext `DomainChunk` content hash, and the quarantine-on-damage behaviour. The existing
same-length-substitution, length-disagreement, damaged-frame and quarantine tests still exercise those
paths and pass (below), so the W1 buffer did not move any integrity decision.

**Semantics note (small, external contract intact):** the rare *race* where a file grows between its
Stat and the read is now refused inside `readBoundedObject` (a moving-target `ErrDamaged`) rather than
read-whole-then-rejected by `getObject`'s hash mismatch. Both yield `ErrDamaged` to callers; the
difference is that this race no longer quarantines (it is a concurrent-write race, not persistent
damage). Static corruption (wrong bytes at the recorded length, truncation visible at Stat time) is
unchanged and still reaches `getObject`'s quarantine.

**Focused verification (actual command and result; no benchmarks, no broad suite):**
```
$ GOMAXPROCS=2 go test -run 'TestReadExactSize|TestRequireEOF|TestReadBoundedObject|TestRecovery_|TestOpenReaders_|Quarantine|Damaged|Substituted' -count=1 ./internal/store
ok  github.com/qompack/qompack/internal/store  1.299s
```
Tests that ran and passed: new `TestReadExactSize` (exact / short-shrink / trailing-grow /
zero-empty / zero-with-bytes / read-error), `TestRequireEOF`, `TestReadBoundedObject_FilesystemBounds`
(regular / zero-byte / too-large / directory / missing); plus the existing integrity guards
`TestDecode_RejectsDamagedFrames`, `TestQuarantine_PreservesExistingEvidence`,
`TestGetObject_LengthDisagreementQuarantines`, `TestGetChunk_RejectsUnindexedSubstitutedObject`,
`TestOpenReaders_RejectSubstitutedChunk`, `TestGetChunk_QuarantineFailurePreservesObject`,
`TestGetChunk_QuarantinesCorruption`, and `TestRecovery_*` (7). `gofmt -l` and `go vet ./internal/store`
clean. **No benchmark was run** (quiet-freeze rule), so **no timing/allocation improvement is claimed
here and no carry row is closed** — the alloc/latency effect and any budget movement remain for the §7
quiet reference-platform A/B, and main retains all carry rows until that evidence exists.

**Correction to §3/§5 (attribution is a hypothesis, not a measurement).** My source-only reading that
Windows Defender scanning of each freshly written object file drives `renameObject`'s sharing-violation
retries is an **inference without traces** — `objects.go` handles `ERROR_SHARING_VIOLATION`/
`ACCESS_DENIED` by retrying, which is *consistent with* AV/indexer contention but does not prove it.
Treat "AV scan" throughout §3/§5 as a labelled hypothesis; confirming it needs Process Monitor / ETW
traces, not this analysis.

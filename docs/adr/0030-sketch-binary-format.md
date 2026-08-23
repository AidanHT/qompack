# ADR 0030 — The QPKS sketch binary format

- **Status:** Accepted
- **Subplan:** SP-03, `internal/sketch`
- **Refs:** `Qompack.md` §6.2, §8.1, §8.3, §11.4, Appendix A/C; `00-ARCHITECTURE.md` §2.4, §3.3, §5.7, §7, §7.4, §13

ADR numbers in this repository are `<SP number as three digits><sequence>`, so SP-03's ADRs are
`0030`, `0031`, … and can never collide with a sibling subplan's.

## Context

`internal/sketch` holds five probabilistic summaries — a Bloom filter, a Count-Min sketch, a
HyperLogLog, a Misra-Gries top-k and a MinHash signature — that persist under `.qompack/sketches/`
and survive restarts. §6.2 calls them permanent memory. The store they live beside is append-only,
so a file written by one plugin version is read by every later one, forever, and there is no
migration step in which a mistake could be repaired.

Two consequences follow, and they set every decision below. A sketch that cannot be read back is
worse than one that was never written, because the caller's fallback for "missing" is "start empty"
and the caller's fallback for "silently wrong" is nothing. And the write side sits on the hook path:
§8.1 item 5 updates Count-Min, HyperLogLog and — on explicit negative-knowledge events — the Bloom
filter inside budget B-A (`PostToolUse` p99 < 15 ms).

## Decision

One container format, `QPKS`, shared by all five sketches: a fixed 32-byte prefix (magic, version,
kind, reserved byte, count, created, param count, body length), a sorted block of named `float64`
params, a kind-specific body, and a trailing CRC-32C over everything before it. One encoder, one
decoder, five bodies.

The rest of this document is the "why" for each part of it.

## 1. Why an explicit version rather than an implicit one

`FormatVersion` is a `uint16` in the frame, and every `UnmarshalBinary` switches on it: `case 1:`
decodes the layout above, `default:` returns `ErrUnsupportedVersion`. Readers accept `Ver ≤
FormatVersion` and never above.

The rule this buys is that an older binary cannot half-read a newer plugin's file. Without a version
in the bytes, "newer" and "corrupt" are indistinguishable, and the safest available behaviour —
refuse — would then have to be applied to both, which would make every forward-compatible field
addition a data-loss event.

The upgrade path is stated so that it cannot be forgotten: when the layout changes, `FormatVersion`
becomes 2, a `case 2:` arm is added, and the `case 1:` arm stays **forever**. The frozen golden
fixtures in `testdata/golden/contracts/sketch/` are v1 bytes, and `TestGolden_V1StillDecodes`
decodes them with the current decoder on every run, so deleting that arm fails CI rather than
silently orphaning every sketch on disk.

## 2. Why CRC-32C rather than `encoding/gob` or JSON

`gob`'s wire format is tied to Go's type definitions. Adding a field changes what an old file
*means* without changing anything a reader could check, and a truncated `gob` stream decodes into a
plausible half-sketch — a Bloom filter with three quarters of its bits, which answers `Test` with
false negatives. A Bloom filter with false negatives is not a degraded cache; it is a filter that
tells the agent an approach was never tried when it was, which is the precise failure §8.3 exists to
prevent. JSON has the same defect plus a float round-trip and roughly a 2× size cost on a 32 MiB bit
array.

The QPKS layout is a fixed contract instead: every length is declared in the prefix and
cross-checked against the actual buffer before anything is allocated, and the CRC-32C catches the
bit rot that structural checks cannot. Castagnoli rather than IEEE because it has hardware support
on every architecture Qompack ships to, which keeps the checksum off the critical path of a 32 MiB
Bloom save — measured at 9.1 µs to marshal and 6.3 µs to unmarshal the default filter
(`BenchmarkBloomMarshal` / `BenchmarkBloomUnmarshal`, medians of the ten samples in
`testdata/bench-baseline.txt`).

The three failure modes are kept distinct on purpose: `ErrBadMagic` (not a sketch at all),
`ErrTruncated` (an interrupted write), `ErrCorrupt` (bit rot). §13 invariant 10 requires degradation
to be loud enough for an operator to tell an unplugged disk from a killed process. `LoadWithLog`
then maps all of them to `core.ErrNotFound` at the package boundary, wrapping rather than
swallowing, so a caller's "not found ⇒ start empty" branch is also its corrupt-file branch while
`errors.Is` still separates them in the log.

## 3. Why params are a sorted `name → float64` map, not a per-kind struct

A per-kind struct would need a per-kind decoder, five places to get the endianness right, and a
format break for every new parameter. The map gives one decoder for all five kinds and makes an
added parameter forward-compatible: an old reader sees a name it does not know and ignores it,
because the sketch's own `UnmarshalBinary` reads the params it needs by name.

`float64` is the single value type because every sizing parameter in Appendix A already is a real
number — `fpRate`, `epsilon`, `delta` — and the integers among them (`capacity`, `k`, `m`, `width`,
`depth`, `registers`) are all far below 2^53, so a `float64` carries them exactly. One value type
means one encoding decision (`math.Float64bits`, little-endian) rather than six.

Sorted ascending by name, and the decoder **rejects** an unsorted params block as `ErrMalformed`.
That is not tidiness: Go randomizes map iteration order deliberately, so without the sort the same
logical sketch would marshal to different bytes on every run, and both the frozen golden fixtures
and the `marshal ∘ unmarshal ∘ marshal` identity test would be asserting nothing.

## 4. Why `Created` is caller-supplied

`Created` is set through `SetCreated(core.UnixMilli)`, never read from a clock inside the package.

A sketch that stamped itself would make `MarshalBinary` non-deterministic, and every byte-level
guarantee in this package rests on determinism: the five frozen contract fixtures, the identity
test, and the fuzz targets that compare a re-marshal against its input. The alternative — injecting
a clock interface through five constructors — would put a mock in every test to buy the same
property a plain field already gives.

The cost is that a caller who forgets leaves `Created` at zero. That is visible (a 1970 timestamp in
`/qompack:status`) and harmless, which is the right direction for the trade.

## 5. Why param names are restricted to `[a-z0-9.]`

Names are 1–32 bytes from `[a-z0-9.]` only, enforced by **both** `EncodeHeader` and `DecodeHeader`.

The decode-side check is the security half: param names come off disk into a map, and an
unconstrained name is an arbitrary-byte string reaching a log line, an error message and the
`/qompack:status` renderer. The encode-side check is the more useful half — it makes the constraint
a compile-once-fail-immediately rule rather than something a caller discovers when a file will not
load. `TestHeader_EncodeRejectsBadParamName` pins that `EncodeHeader` refuses `"fpRate"` at the
source.

Restricting the alphabet rather than escaping it keeps the names comparable as raw bytes, which is
what makes the sort in §3 a plain bytewise sort with no collation to get wrong.

## 6. Why the Bloom param on disk is `fprate` while the config key stays `fpRate`

`sketches.bloom.fpRate` is the Appendix C configuration key and stays camel-cased, because that is
the schema users write and `docs/config-reference.md` publishes. The on-disk param is `fprate`,
because §5's alphabet has no uppercase in it.

The alternative — widening the alphabet to `[A-Za-z0-9.]` so the two could match — would trade a
one-line mapping for a permanently case-sensitive on-wire namespace in which `fpRate` and `fprate`
are different params that mean the same thing. The one-line mapping is cheaper and it is
single-sited: `bloom.go` declares `paramFPRate = "fprate"` as a constant used by both the encoder
and the decoder, so the two provably agree. A typo in only one of them would produce a filter that
saves cleanly and then fails to load — exactly the failure §6.2 exists to prevent — which is why it
is a constant and not a literal at four call sites.

The frozen golden fixtures pin the literal string, so a rename is a format break that fails CI
rather than a silent re-keying.

## 7. Why `MaxBloomBits` is `1 << 28` and not `1 << 31`

**The ceiling rule: every constructible sketch must also be marshallable.**

`MaxFrameBytes` is 64 MiB, the universal decode ceiling that bounds every allocation below it. At
`1 << 28` bits the largest legal Bloom body is `m/8` = 32 MiB, which leaves the 32-byte prefix, four
params and the 4-byte CRC roughly 50 % headroom inside that ceiling. At `1 << 31` the body alone
would be 256 MiB.

Without the rule, `NewBloom(MaxBloomCapacity, 1e-6)` would build a filter that `Save` writes happily
and `Load` then refuses as `ErrTooLarge` — a sketch that cannot survive a restart, which is the one
failure this package exists to prevent. `MaxCMSCells` is picked on the same basis.

Misra-Gries cannot be bounded that way, because its frame size depends on key lengths as well as on
counter count. The uniform rule that covers it is enforced once, in `EncodeHeader`: an assembled
frame over `MaxFrameBytes` is `ErrTooLarge`, never a partial frame and never a panic, so all five
`MarshalBinary` implementations inherit the guard by construction instead of each re-implementing
it.

**The cap re-derives `k`, and does not surface in `Capacity()`.** `bloomSizing` computes
`k = (m/n)·ln 2` from the *requested* `m` and then holds `m` itself inside `MaxBloomBits`. Where the
cap binds — above capacity ≈ 9.34 M at `p = 1e-6`, which `NewBloom(MaxBloomCapacity, 1e-6)` reaches
with legal configuration values — a `k` left over from the uncapped `m` is wrong in the expensive
direction: `k = (m/n)·ln 2` minimises the false-positive rate *at a given m*, so over-probing an
array 1.80x smaller than the one requested (4.82 × 10⁸ bits asked for, `MaxBloomBits` = 2²⁸ = 2.68 ×
10⁸ allocated) raises the rate as well as the cost. At that pair `m/n` lands on 16, where
the optimal `k` is 11 and the uncapped derivation gave 20. `k` is therefore re-derived from the
capped `m`, and only on that path, so no filter whose `m` was never capped moves.

`Capacity()` still reports the `(n, p)` it was constructed with, which the cap has now made
unachievable. That is deliberate — it is the *configuration* accessor, and it already surfaces the
`n` and `p` clamps — but it means the bit cap is a **third** clamp that `Capacity()` does not show.
`Bits()` reports the array actually allocated and `EstimatedFPRate()` reports the rate implied by
the bits actually set, which is the number §11.4 asks operators to watch. `Capacity()`'s doc comment
names all three so a reader is not left inferring it.

**The rule also runs downward.** Marshallable means *re-readable*, so the smallest sketch matters as
much as the largest: `Bloom`, `CMS`, `HLL` and `MisraGries` all refuse to marshal an unsized (zero
value) receiver, which would otherwise emit a structurally valid, correctly checksummed frame
declaring zero dimensions that the same build's decoder then refuses forever — 94 bytes for `CMS`,
54 for `HLL`. `SigSketch` is the legal exception, its zero value being the disabled signature, which
round-trips exactly. `sketchtest.RunSketchSuite` states the contract as the disjunction that covers
both ("refuse to marshal it, or decode what was written — never both"), so a future factory inherits
it instead of re-asserting it per type.

## 8. Why the Count-Min width is 2 719 and Appendix A's "2718" is the same number

Appendix A gives the formula `width = ⌈e/ε⌉` and then illustrates it: "ε = 0.001, δ = 0.01 → 2718 ×
5 ≈ 54 KB @ 4-byte counters". `e/0.001 = 2718.281828…`, and `⌈2718.281828…⌉ = 2719`. The
illustration is `e/ε` shown to display precision with the ceiling not applied; the formula is
normative and the illustration is not.

So the implementation is 2 719 × 5 = 13 595 counters, a 54 380-byte body. Appendix A's "≈ 54 KB" is
satisfied either way, which is why the discrepancy could have gone unnoticed for a release: 2 718
would produce a sketch that is correct, slightly under-provisioned, and permanently incompatible
with every conforming implementation. `TestCMS_AppendixASizing` pins 2 719 with the derivation in a
comment so nobody "corrects" it back.

The depth is `⌈ln(1/δ)⌉ = ⌈4.60517…⌉ = 5`, with no such ambiguity.

## 9. Why the MinHash shingle hash is FNV-1a and not SHA-256

Cost, and only cost. A 100 KiB tool result has ~102 393 shingle positions, every one of which is
hashed to decide whether the content-defined sampler keeps it. FNV-1a over an 8-byte shingle is a
handful of nanoseconds; `core.HashBytes` measures ~90 ns. The whole 100 KiB signature measures
1.749 ms on the development host with FNV-1a (`BenchmarkMinHash100KiB`, committed-baseline median); ~102 000 SHA-256 calls at 90 ns each would be 9.2 ms of
hashing on their own, on a per-tool-result path budgeted at 50 ms end to end.

The security argument that would normally force SHA-256 does not apply: the shingle hash's output
never leaves the signature, is never used as an identifier, and is not compared against anything an
adversary supplies. A collision degrades a similarity estimate; it does not authenticate anything.

**The caveat is that this choice is frozen forever.** Signatures are persisted in an append-only
index (`store.ToolUseRecord.Signature`). Changing the shingle hash, the permutation coefficients
(`a[i] = splitmix64(2i)|1`, `b[i] = splitmix64(2i+1)`) or the sampling rule makes every stored
signature incomparable with every new one — silently, because both sides still look like well-formed
signatures and `Jaccard` still returns a number. `TestMinHash_StableAcrossRuns`,
`TestSplitMix64_Frozen` and `TestFNV1a64_Frozen` pin the constants against published reference
vectors so that "frozen" is a test rather than a comment.

## 9a. Why the sampler keeps the smallest 8 192 distinct hashes rather than everything below a rate

The selection rule is the second half of the frozen algorithm, and it changed once — after the
whole-branch review, before any signature was written to a real index. It is recorded here in full,
because a reader of `MinHash` should be able to see what the rule is *not*.

**What shipped first.** A rate `⌈nsh / MinHashSampleTarget⌉` was derived from the shingle count and
every distinct hash at or below `MaxUint64/rate` was kept. Selection was content-defined *within* one
document, which bought shift invariance — but the threshold itself was a **step function of document
length**. Two near-identical documents whose shingle counts straddled a multiple of 8 192 were
sampled at densities differing by a whole integer factor, so for each permutation their minima agreed
with probability ≈ 1/rate however similar the documents were. Measured on 8 149- and 8 299-byte
documents with a true Jaccard of 0.980: an estimate of **0.453**, and `IsNearDup` false at the
configured 0.9. The failure recurs at every boundary — ~8 KB, ~16 KB, ~25 KB and so on through
~66 KB, the whole size range of an ordinary tool result — and it lands on a *growing* document, which
is exactly the input §8.1 item 1 exists to detect. `TestMinHash_OneNewFailure` passed only because
its fixture is 200 lines; at 210 the same test measures 0.414.

**What ships now.** The `MinHashSampleTarget` smallest **distinct** shingle hashes are kept, and a
document with fewer distinct hashes than that keeps all of them. The effective threshold is then the
target-th smallest distinct hash, which moves continuously with the document instead of stepping, so
two documents of similar size get near-identical thresholds. Across the same eight boundaries the
estimates are 0.96–1.00 against true Jaccards of 0.980–0.998.

`Signature{Perms uint16; Mins []uint64}`, its compact wire form and `Jaccard`'s body are unchanged:
this is a change to which shingles are selected, not to the estimator over them, and 00-ARCHITECTURE
§5.7 fixes that shape. A KMV/bottom-k *estimator* would have been a larger change to a fixed shape
and is not needed to remove the cliff.

**One defensive case disappears rather than being handled.** The old rate was derived from shingle
POSITIONS while selection applied to DISTINCT hashes, so a repetitive document — a megabyte of one
repeated build-log line has ~58 distinct 8-shingles among 1 048 569 positions — could sample to
nothing, and a sampled-to-nothing signature is byte-identical to the empty-document signature, which
reports Jaccard 1.0 against an empty document and would make SP-06 store a delta instead of the text.
The rule then halved its rate and re-ran the pass until something survived. Under bottom-k the
smallest *k* distinct values of a non-empty set are a non-empty set, so a document with at least one
shingle always keeps at least one hash. The retry is deleted, not left unreachable.

**Mechanism, and why it is not the obvious one.** Selection is a seeded threshold with a refinement
pass: `fnv1a64`'s output is uniform, so admitting the low `2 × target / nsh` of the hash range yields
about 2 × 8 192 candidates, which the pass narrows to exactly the target with an open-addressed
distinct set and a quickselect. The seed cannot change the answer — if at least `target` distinct
hashes fall below it, the target-th smallest of the whole document is among them; if fewer do, the
general pass runs with no threshold and returns what it always would have — so unlike the halving
retry it is an optimisation and not a rule. The obvious alternatives were measured on the 100 KiB
benchmark against its 2.5 ms budget: admit-everything-and-narrow costs 2.87 ms, and
sort-and-truncate 8.2 ms.

**Only documents above the target moved.** At or under 8 192 shingles no selection runs at all and
the code is a plain pass over every shingle, as before, which is why all five frozen fixtures under
`testdata/golden/contracts/sketch/` are byte-identical: the MinHash fixture's document is 4 096 bytes
(4 089 shingles). No signature has ever been persisted by a release, so nothing on disk is affected
either way — but the window for that has closed with SP-04 and SP-06, which is why the change landed
now.

## 10. Why mutable sketches are not goroutine-safe while `MinHash`/`Jaccard` are

`Bloom`, `CMS`, `HLL` and `MisraGries` are mutable and explicitly **not** safe for concurrent use.
`MinHash`, `Signature.Jaccard`, `Signature.IsNearDup`, `EncodeHeader`, `DecodeHeader`, `hash128`,
`splitmix64` and `fnv1a64` are pure and explicitly **are**.

The split follows ownership, not taste. There is exactly one writer of every mutable sketch — SP-05's
daemon, which holds each one behind its session-registry mutex — so an internal lock would be a
second, redundant one taken on every `Add`. At ~200 ns per `Add`, an uncontended mutex pair is a
double-digit percentage of the operation, charged to the hook path, to protect against a caller the
architecture does not permit.

The pure surface has no such owner: SP-06's store calls `MinHash` from a worker pool, so purity is a
requirement rather than a convenience. Stating both properties in the doc comments — every mutable
type says it is not safe, every pure function says it is — is what makes the distinction usable by a
caller who has not read this file.

## 11. Why `Load` and `LoadWithLog` are split

`LoadWithLog(p, s, log)` does the work. `Load(p, s)` is the §5.7 signature and hands it a
`logging.Nop()`.

The alternative the plan first considered was a package-global logger that `Load` would own. This
package refuses package-level mutable state: a global logger is initialization-order-dependent, it
is shared across the daemon's concurrently-open projects (whose log destinations differ), and it
makes every test that asserts on a Loud report race with every other one.

Making the logger a parameter puts the choice at the call site, where the caller knows which project
it is loading. `Load` remains for callers that genuinely have no logger — tests, tools — and its doc
comment names `LoadWithLog` explicitly, because choosing the silent path by accident is what §13
invariant 10 forbids.

What "silent" means here is narrower than it first appears, and the doc comment was corrected during
review to say so. A `Nop` logger has no destination behind it, so no log **line** is written
anywhere — not to the day log, not to `LOUD.log`. But `logging.Nop().Loud` still appends to the
process-wide ring that `logging.LastLoud` reads and still fires any observer installed through
`logging.AttachLoudObserver`, so a corrupt file loaded through `Load` does reach an `obs` counter
wired up at a composition root. That is a debugging aid, not the durable record an operator reads
after the fact, which is precisely why production call sites — SP-05's `SketchSet`, SP-09's
`negknow.Open` — must call `LoadWithLog`.

## 12. Why `ReplaceGenerational` delegates to `paths.ReplaceBloom`

`sketches/tried.bloom` is append-only (§3.3). `paths.WriteAtomic` refuses that exact path outright
through `paths.IsProtected`, returning `core.ErrAppendOnly` (§7.4), and `paths.ReplaceBloom(l, b,
seq)` is the single sanctioned exception. `sketch.Save` therefore refuses the path by base name
before it marshals anything, and `sketch.ReplaceGenerational` is the only door.

The plan's sample body called `paths.WriteAtomic` on `tried.bloom` directly and could not have run.
The correction is not merely to call the sanctioned function instead — it is that the mechanics stay
in `internal/paths`, which owns the guard. `paths.ReplaceBloom` already renames the current file to
`tried.bloom.<seq>.bak`, stages and swaps the new content, and prunes to exactly one backup
generation; `paths.pruneBloomBackups` parses that same filename family. Re-implementing the rename
in `internal/sketch` would mean two writers of one filename pattern and a guard with a hole in it.

What `sketch` adds on top is the part that needs the sketch: marshalling, rollback, and returning
the backup path. Two consequences of the delegation are worth recording, because callers depend on
them:

- The backup filename is `paths`' own `tried.bloom.<seq>.bak` — `%d`, not the plan's zero-padded
  `%04d` — because `paths`' pruner is what parses it. `ReplaceGenerational` returns the path rather
  than expecting SP-09 to format its own.
- `seq` **must increase monotonically, and the call is refused before anything moves.**
  `pruneBloomBackups` keeps the highest sequence, not the newest file, so a caller passing a lower
  `seq` has its own backup deleted the instant it is created. Detecting that afterwards — which is
  what the first implementation did, in two places and at length — left one path that destroys data:
  `ReplaceBloom` renames the current file to its backup *before* it stages the new content, the prune
  then deletes that backup for being low-sequenced, and a staging failure at that point finds nothing
  to roll back, so `sketches/tried.bloom` simply no longer exists. Reporting that accurately is not
  preventing it.

  `ReplaceGenerational` therefore pre-flights against the new `paths.HighestBloomBackupSeq(l)`, and
  a `seq` that does not strictly exceed the surviving backup returns `("", err)` wrapping
  `ErrMalformed` — naming the path, the survivor and the offending sequence — with no filesystem
  change at all. The two post-hoc checks remain as defence against a concurrent mutator, since a
  rollback that silently no-ops must never be quiet, but neither can now be reached by a
  mis-sequenced call.

  The parsing lives in `internal/paths` for the reason this whole section gives: that package writes
  the `tried.bloom.<seq>.bak` name and prunes by it, so a second parser in `internal/sketch` would be
  a second definition of the same filename family. `HighestBloomBackupSeq` and `pruneBloomBackups`
  share one. It is **exported** because SP-09 has the same question for a different reason: its
  rebuild counter drives `seq`, and nothing else on disk tells a restarted daemon where that counter
  had reached.

## Where this sits in the latency budget

**The composite the L0 model consumes.** §8.1 item 5's whole contribution to one `PostToolUse` is
one `CMS.Add`, one `HLL.Add` and one `Bloom.Test`. The budget for that triple is ≤ 5 µs with zero
allocations — at 5 µs it is 0.03 % of the 15 ms B-A hook budget, which is the quantitative form of
§8.1's "the rest is one append and a few hash lookups".

`BenchmarkL0SketchUpdate` measures it at **547.9 ns with zero allocations** (median of the ten
samples in `testdata/bench-baseline.txt`, range 481.2–588.3 ns) — 9.1× inside the latency budget,
and 0.004 % of B-A rather than the 0.03 % allowed. Both halves of the budget are met.

Its Bloom probe is a **hit**, against a filter pre-filled with the descriptors it probes. That is
both the conservative reading — a miss short-circuits at the first clear bit instead of reading all
seven positions — and the only fixture that matches production in kind, since `tried.bloom` holds
negative-knowledge descriptors and never paths. In production most probes miss; a side-by-side probe
put the difference at ~8–12 ns, about 2 % of the composite, because `Test` is dominated by its one
SHA-256 rather than by the seven positions it guards.

**That the allocation half is measured rather than assumed is the reason it is met.** These
benchmarks are what found the one defect in the model. The first recording of them put every hashing
row at 1 alloc/op and 32 B/op, the composite at 3 and 96 B, and `RebuildBloom` over 5 000 keys at
5 004 allocs/op — while every *latency* budget passed, because a nanosecond column cannot show an
allocation. The cause was one line in `core.HashBytes`, which ended `copy(out[:], h.Sum(nil))`;
`h.Sum(nil)` allocates the 32-byte slice it appends into. Writing the digest into the returned array
instead (`h.Sum(out[:0])`) took the helper from 1 alloc/op to 0 — and, in a one-off A/B probe rather
than a committed benchmark, from 104.1n ± 2 % to 90.36n ± 3 % — with byte-identical digests, and took
`RebuildBloom5000` from 5 004 allocs and 172 400 B to 4 and 12 400, exactly the 5 000 × 32 B it was
shedding per rebuild.
`TestL0SketchUpdate_ZeroAlloc` here and `TestHashBytes_DoesNotAllocate` in `internal/core` are the
guards that keep it that way. The plan's rationale column for those rows — "one SHA-256 of a short
key plus 7 bit tests" — was accurate about the arithmetic and silent about the allocation, which is
exactly the class of error a budget model catches only if somebody measures it.

**MinHash is deliberately not on B-A.** It is not part of §8.1 item 5, and no hook calls it. It runs
in the daemon's async worker under budget **B-C** (`l0_process`, p99 < 50 ms, soft), which is why its
budget rows are stated in milliseconds — 1.5 ms for 4 KiB, 2.5 ms for 100 KiB — while every hot-path
row here is stated in microseconds. Measured at 345.0 µs and 1.749 ms (medians over the committed
baseline's ten samples), the 100 KiB case clears B-C by more than 28×. The `MinHashSampleTarget` bound of 8 192 distinct shingle hashes is what keeps that
flat in document size; without it a 1 MiB tool result would cost ~134 million multiply-compares.

Both MinHash rows survived the fix round that replaced the sampler (§9a) without regressing, which
is the opposite of what adding a selection pass would suggest. Two restructurings paid for the
selector — shingle hashing moved into a batch helper whose default-width loop has a compile-time
constant length, so the compiler unrolls FNV-1a's multiply chain and overlaps consecutive shingles,
and hashing and permuting became two passes over a batch rather than one interleaved loop.

**The before/after figures this paragraph used to quote are not restated, because their "after" side
is in no committed file.** It read `367.5 µs → 287.1 µs` and `1.525 ms → 1.381 ms`, both "at
p = 0.000 over ten samples"; `testdata/bench-baseline.txt` was regenerated at `2d2b596` and now
records 345.0 µs and 1.749 ms as its medians, so the comparison cannot be reproduced from anything
this repository holds. A measured claim whose evidence has been overwritten is worse than no claim:
re-derive it with `devtool bench-compare` against a recorded pair if the comparison is wanted again.

The 100 KiB row's *allocation* did move, from 3 072 B / 2 allocs to 396 288 B / 4, and that IS
visible in the committed baseline: the selector's table and buffer are sized from
`MinHashSampleTarget` and not from the document, so a 1 MiB input allocates exactly the same. That
row carries no allocation budget; the six rows that do are all still at zero.

The 100 KiB row is nonetheless the tightest in the file, at 1.4× headroom against its 2.5 ms row, and
the baseline records the history at length: the first recording of it, under a loaded run, had a
median of 2.432 ms against the 2.5 ms budget with four of ten samples above it. MinHash hashes
shingles with FNV-1a and never calls `core.HashBytes`, so the allocation fix did not move that row —
a quieter machine did. The 2.5 ms figure is a Windows-laptop number to be re-derived on CI hardware
at V2-VERIFY, and it must be judged with benchstat over repeated samples, never one run. None of
that is a statement about B-C, which it clears by an order of magnitude.

Misra-Gries is likewise off B-A: §8.1 item 5 names Count-Min and HyperLogLog only, and the summary is
fed daemon-side. Its ≤ 5 µs/op budget is explicitly *amortized*, which matters — a single decrement
pass over a saturated k = 256 table measured 7.9 µs in a one-off probe (pre-fix recording, not among
the committed samples; the argument, not the digit, is what carries), and what keeps the row met is the
Misra-Gries potential bound: a pass cannot run more than once per k unit `Add`s.

## Consequences

- The format is frozen. Any change to the prefix layout, the param encoding, the domain strings, the
  shingle hash or the permutation derivation is a `FormatVersion` bump plus a permanent `case` arm,
  and the five frozen fixtures under `testdata/golden/contracts/sketch/` fail CI if one is attempted
  without it.
- One decoder serves five kinds, so a hardening fix (the size-before-allocate ordering, the
  bare-sentinel rejection paths that keep `DecodeHeader` at zero allocations on the whole fuzz
  surface) lands once and covers all of them.
- `float64` params mean an integer parameter above 2^53 could not be represented. Every ceiling in
  this package is far below that, and the ceilings are constants, so the constraint is checkable
  rather than latent.
- Callers must not assume a sketch is authoritative. §13 invariant 3 holds throughout: every answer
  here is a cache, `Bloom.Test` true and `CMS.Estimate` are upper bounds that need a record behind
  them, and `MisraGries.Top` absence is not evidence except above the `Total()/(k+1)` threshold.
- `MisraGries.Add` saturates rather than wrapping, deviating from the plan's plain `+=`. A wrapped
  total makes `Top` report negative counts while its doc promises a lower bound, and makes the
  retention threshold meaningless. A saturated count is still a lower bound; a wrapped one breaks
  both halves of the guarantee silently.

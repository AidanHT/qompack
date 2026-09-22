# V6 delivery-seal offline reader — segmented integrity check (work report)

**Author seat:** Opus 4.8 high, no children. **No git/config/auth/billing/permission changes; no
commits.** **Exclusive scope:** `internal/daemon/delivery_seal_tool.go` (edited),
`internal/daemon/delivery_segment_readonly.go` (new), `internal/daemon/delivery_seal_segment_v6_test.go`
+ `delivery_seal_segment_v6_unix_test.go` (new). No edits to any store source, order/terminal files, or
the now-main-owned producer files (`delivery_lease.go`, `delivery_generation.go`, `delivery_radix.go`,
`delivery_segment.go`, `delivery_path.go`).

## Round-2 corrections (main's review, all addressed)

1. **arrivalBase now propagates errors (3-result).** `checkSegment`'s predecessor closure and
   `checkLeaseJournal`'s `arrivalBase` are now `func(SessionID)(uint64,bool,error)`; a missing/corrupt
   predecessor page refuses instead of being swallowed into a false "new session". Test:
   `TestDeliverySealSegment_CorruptPredecessorPageWithFirstArrivalRefused` builds a store whose active
   segment's first lease is a NEW session at arrival 1 (the case where swallowing would pass the density
   check `1 == 0+1`), corrupts the segment's base-root radix page, and asserts a refusal.
2. **`scanGenChain` is a bounded stream with coherent tail handling.** It checks `genMaxLog`, regular
   file and `SameFile` before reading, streams a line at a time capped by `genMaxLine` (no 1 GiB
   allocation), honours `ctx`, and — instead of silently ignoring a committed tail beyond the head —
   **carries the tail forward as the recovered last root** (`recoveredTail`, reported by the tool) so the
   view is coherent. Test: `TestDeliverySealSegment_TornGenerationManifestRefused` (a torn manifest tail
   refuses).
3. **Confinement is anchored through the trusted state root.** All reads route through
   `pinDeliveryDirectory`/`pinDeliveryChild` (main's helpers): the generation, segments and each sequence
   directory are pinned (static symlink/irregular alias rejected, `SameFile` confirmed) and every file
   (head, log, pages, journals, seals) is read THROUGH the pinned `os.Root`; the pages radix is opened as
   a pinned child, not by absolute path. The four files are re-verified under the pinned identity after
   the scan. The doc comment states plainly this checks the STATIC path and opened-handle identity and is
   **not** a claim of atomic protection against every dynamic path race. Tests:
   `..._SegmentDirNotADirectoryRefused` (runnable) and `..._SymlinkedSegmentDirRefused` (unix).
4. **`scanAuthorityChain` requires a DENSE active progression.** Active must be exactly `prevActive+1`
   (overflow-guarded), matching the producer's protocol; a skipped/jumped active is rejected even with a
   valid chain, and the log-file stat error is handled explicitly (the old `mustStat` is gone). Test:
   `TestDeliverySealSegment_NonDenseActiveAuthorityRefused` (a crafted active jump 1→3 refuses).
5. **Byte-tree non-mutation and negative controls retained**, plus the fixtures above; original run
   failures preserved; no 70-rotation rerun (its mechanism is unchanged and main's).

## The defect (with a negative control)

`RepairDeliverySeal` / `DeliverySealOptions.run` built its `deliverySealSides` from the **hardcoded
legacy paths** (`state/delivery-leases.jsonl`, `state/delivery-acks.jsonl`) and never consulted the
segment authority. On a rotated store those legacy files are **segment 0**, which stays valid forever —
so `--check` (and therefore a packaged fsck/backup-restore that trusts it) reported the whole store
"checked" while the **active segment** was corrupt.

`TestDeliverySealSegment_RefusesCorruptActiveSegmentThatLegacyOnlyAccepts` is the negative control and
the fix in one: it builds a real rotated daemon store (segment 0 intact, active segment's lease journal
corrupted), **demonstrates segment 0 loads on its own** (exactly what the pre-fix tool validated — it
would have accepted the store), and then asserts the corrected tool **refuses**, naming the segment.

## Read-only segmented check (creates and writes nothing)

`--check` now, when a segment authority is present, validates the whole store read-only. Per the binding
trust/rollback decision it must NOT call the daemon's `openDeliverySegments`/`openDeliveryGenerations`
(they `O_CREATE` the log, `Mkdir`, and recover by **writing** a head). New read-only views in
`delivery_segment_readonly.go`:

- **Authority** (`readonlySegmentAuthority`): opens the head + log through an `os.Root` pinned to the
  state dir (no create), and validates the WHOLE transition chain from seq 0 — dense sequences,
  active-0 first then strictly-increasing active segments, each record chained from its predecessor, and
  the atomic head matching the record ending at `log_bytes`. A full scan is justified for an offline
  tool. A **complete committed tail beyond the head** is read as the recovered active view **with an
  explicit diagnostic and no on-disk checkpoint mutation**; a torn/conflicting/ahead/off-boundary head,
  a log without a head, or **migration evidence (generation store / segments dir) without an authority**,
  refuses. A genuinely unmigrated tree (no authority, no evidence) keeps the exact legacy behavior.
- **Generation store** (`openGenReadonly`): validates the manifest+head chain read-only (full chain) and
  opens the pages via `os.OpenRoot` on the existing dir (never `Mkdir`), reusing the producer's
  content-addressed page reader for exact full-key + integrity validation. Missing → refused, never read
  as empty.
- **Per segment** (`checkLeaseJournal`, `checkAckJournal`): every committed segment (0..active) is
  checked — its four files present, its lease journal scanned against its seal with arrivals **resuming
  from the segment's predecessor (base) root** (`arrivalBase`), and each lease **verified against the
  generation store's recorded identity, not its filename**. The ack journal is scanned against its seal
  and **every acknowledgement is joined to its ORIGINAL lease in the generation store with an exact
  identity comparison** — the archived-ACK join (a segment's ack file may reference a lease archived into
  an earlier segment). `TestDeliverySealSegment_ArchivedAckExactJoinPasses` exercises this.

Confinement/safeguards follow the current source convention: `os.Root`, `SameFile`, bounded
regular-file reads (FIFO/dir/oversize refused), non-canonical/unknown-key JSON refused. Byte-tree
digests of `.qompack/state` are identical before/after `--check`
(`TestDeliverySealSegment_ValidMultiSegmentTreePasses`), proving nothing is written to evidence (the
`.qompack/run` lock/heartbeat is outside state, excluded exactly as the legacy byte-tree test excludes
it). Lock ownership is re-checked (`holdsLock`) after the whole scan and before the verdict; the 90-second
staleness caveat remains a **refusal**, never a pretend-ownership.

## `--to v1` on a segmented store is refused

`runSegmented` refuses `--to v1` explicitly: converting a seal does **not** make an old writer able to
read the segment authority or history, and the tool says so, preserves every byte, and directs the
operator to a verified backup restored by a compatible reader.
`TestDeliverySealSegment_ConversionRefusedPreservesBytes` asserts the refusal AND that the state tree is
byte-identical afterward.

## Evidence (focused, `run.py`, GOMAXPROCS=2, recorded under `runs/`)

| run id | scope | result |
|---|---|---|
| `segment-offline-reader-03` | `TestDeliverySealSegment*` (13 cases: negative-control+fix, valid multi-segment passes + digest-unchanged, archived-ACK join, missing active files refused, missing head refused, newer schema refused, `--to v1` refused + bytes preserved, unmigrated legacy still checks, **corrupt-predecessor+first-arrival refused**, **torn generation manifest refused**, **non-dense active refused**, **segment-dir-not-a-directory refused**) | **PASS** (exit 0) |
| `segment-offline-reader-legacy-04` | `TestDeliverySeal_*`, `TestDeliverySealRuleR*` (the existing legacy tool tests) | **PASS** (exit 0) — no regression |
| `GOOS=linux go vet ./internal/daemon` | cross-compile + type-check incl. `..._SymlinkedSegmentDirRefused` (unix) | **PASS** (the unix symlink-alias test compiles for Linux CI; it is skipped on the Windows author machine) |

I did **not** repeat the 70-rotation live suite (its mechanism is unchanged and it is main's now); the
segmented store fixtures here reuse the existing `setRollover`/`openRolloverJournal` helpers.

## Open boundaries / helper API needs returned to main (not narrowed silently)

1. **CLI wire/exit semantics are unchanged.** `RepairDeliverySeal`'s signature and error-return
   contract are untouched; the segmented branch returns errors identically, so `internal/cli`'s
   `admin delivery-seal` exit codes are preserved. I did not edit `internal/cli` (out of scope); its
   tests use unmigrated trees and are unaffected.
2. **`--accept-torn-slot` is not applied to per-segment seals in this round.** A segment (or generation)
   whose seal has a torn slot is **refused** (evidence preserved), rather than offering Rule-R acceptance
   per segment. This is the conservative reading of the binding ("cannot invent a missing
   transition/base"); extending Rule R across segments, if wanted, is a follow-up. The legacy (segment-0-
   only, unmigrated) path retains full `--accept-torn-slot` behavior and its test IDs.
3. **Read helper API for main.** The read-only views (`readonlySegmentAuthority`, `openGenReadonly`,
   `checkLeaseJournal`, `checkAckJournal`, `genReadonly.resolveLease`/`arrivalAt`) live in
   `delivery_segment_readonly.go` and are available for main's other readers (e.g. a store-GC or
   backup-side segmented reader) without mutating the producer files. If main wants these surfaced
   differently (exported, or moved), that is a coordination decision, not something I changed in
   main-owned files.
4. **Not a rollback/old-reader guarantee.** This is same-build backup/restore integrity only; old-reader
   / new-write rollback remains separate, and no old-writer compatibility is claimed. Segmented `--to v1`
   is refused precisely to avoid implying otherwise.

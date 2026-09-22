# V6 delivery-journal capacity integration — work report

> ## IMPLEMENTED SEGMENT AUTHORITY + ROLLOVER (round 5) — corrects the rejected round-4 self-hash
>
> **My round-4 self-hash authority proposal was REJECTED and is retracted** (I had missed the appended
> Review in `delivery-segment-retention-decision.md`). The two mandatory corrections are now implemented
> and tested: (1) an immutable **chained transition log + atomic head** replaces the self-hash — a
> self-hash is integrity syntax, not a commit/continuity proof; (2) **absence is legacy ONLY on a
> genuinely unmigrated tree** — an active-0 authority is committed before any migration evidence, so
> evidence surviving without an authority is head loss and is refused, never silently reopened as legacy.
> The seam stays default-off pending main's review of THIS wire; rollover is real and tested, not a
> proposal.
>
> **Authority = two files (modelled exactly like the accepted generation manifest):**
> - `state/delivery-journal-log.jsonl` — append-only, chained, versioned **transition** records. Record
>   `seq` is immutable: `{v, seq, active, base_root, chain}`, `chain = H(salt, prevChain ‖ seq ‖ active ‖
>   base_root)`. The durable, **fsynced append IS the commit** of that transition.
> - `state/delivery-journal.json` — the **atomic head** (the file main's backup watches): `{v, format,
>   seq, active, base_root, chain, log_bytes, last_len}`. A fast-start checkpoint, never the sole
>   authority. `WriteAtomic` (temp→fsync→rename→dir-fsync).
>
> Record 0 is the **active-0** transition (`active:0, base_root:""`) — the legacy segment. `active≥1`
> records carry the generation `base_root` preceding that segment. `base_root` binding is validated on
> read (the head must match the record it names, `verifySegHeadBoundary`).
>
> **The REAL commit point + recovery (root-anchored, bounded startup):** recovery reads the atomic head,
> verifies in O(1) via `last_len` that the record ending at `log_bytes` decodes to exactly the head's
> `{seq,active,base_root,chain}`, then validates ONLY the tail beyond it. A head **behind** a complete
> chained tail is repaired FORWARD (head loss → adopt, never a silent revert to a lower active); a head
> **ahead** of the log, off a boundary, or disagreeing with its record → refused; a **torn** trailing
> record → refused and preserved. This is the generation store's proven pattern; a mutable head is never
> trusted over the log.
>
> **Rotation ordering (exclusive barrier; the transition append+fsync is the commit, head is the
> checkpoint):**
> 1. **Barrier** (`rotate`): set `rotating` (blocks `enter`, excluding BOTH pipelines while their normal
>    independent commits are undisturbed until the barrier closes), drain in-flight to zero. The owner
>    lock is NOT held across the drain wait. A second concurrent rotation waits and returns; the caller
>    retries on the fresh segment.
> 2. **Reconcile** the outgoing window into the generation store (idempotent) → immutable root `R`.
> 3. **Stage** `state/delivery-segments/<active+1,%020d>/` (four empty journal/seal files, fsynced);
>    a crashed prior attempt is adopted only if EXACTLY fresh-empty, else preserved and refused.
> 4. **Commit** the transition: append `{seq+1, active+1, base_root=R}` + fsync, then `WriteAtomic` the
>    head. A crash after the append but before the head write is adopted on reopen; before the append,
>    the OLD segment stays authoritative and the staged dir is a preserved attempt.
> 5. **Switch** the live window to the new segment (close old handles — files remain for retention —
>    reset maps/bytes/chains, repoint `j.path`, re-open via the same load/seal machinery), release barrier.
>
> **Dense global arrivals across the boundary:** a segment's leases carry global arrivals (e.g. 5, 6), so
> the segment reader seeds each session's predecessor from `base_root`'s last arrival before `loadFrom`'s
> dense check (`arrivalBase`); the legacy segment seeds nothing and starts at 1 exactly as before. This is
> the decision's "starts each session from its exact predecessor root."
>
> **Confinement:** head, log and staged segments go through an `os.Root` pinned to the state dir; reads
> `SameFile`-guard the opened handle and bound a regular file (FIFO/dir/oversize refused); non-canonical
> or unknown-key JSON is refused.
>
> **Tested (focused, `run.py`, GOMAXPROCS=2, recorded under `runs/`):**
>
> | run id | scope | result |
> |---|---|---|
> | `capacity-integration-seg-01` | authority: active-0+reopen, 72 transitions, **head-loss refusal**, complete-tail-adopt-never-revert, torn-tail preserve, conflicting-head refuse, staging idempotency/conflict | **PASS** |
> | `capacity-integration-rollover-final` | **live** rollover through the journal API (post-lint): seam crossing, **≥70 live rotations** + archived-nonce identity, restart-across-rotation, dormant-session arrivals, archived-ACK exact join + retained frontier | **PASS** (118 s) |
> | `capacity-integration-lint-verify-01` | generation+segment+wiring+radix after the lint fixes | **PASS** |
>
> Lint items corrected: `commitAck`/`commitTerminal` simplified to `error`-only (matches main's already-wired
> `delivery_terminal_generation.go`); `nonceAtArrival` now exercised with a varying session; `radixMaxKeyBytes`
> carries a `//nomagic:allow` format-bound comment.
>
> **Exact APIs for main's adapters** (all in `internal/daemon`): order adapter → `nonceAtArrival`,
> `sessionFrontier` (O(1) per-session oldest-pending); ACK archived-join is DONE in `commitAcks` (resolves
> an archived lease via `resolveLease` and compares identity); terminal → `resolveTerminalLease`,
> `commitTerminal(ctx, terminals) error`. **Main has already wired the terminal adapter**
> (`delivery_terminal_generation.go`: `loadActiveTerminalDispositions`, `archivedTerminalDenied`) against
> these — its call sites are honoured (I simplified `commitTerminal`/`commitAck` to `error`-only to match).
>
> **Open boundaries returned to main (not narrowed away):** (a) **store GC** must enumerate
> `state/delivery-segments/` + every segment's lease/ack roots with exact ACK identity joins — main owns
> the `gcrun.go` reader (a child is fixing legacy GC read errors there; I do not touch it); until it
> aligns, segmented collection must refuse rather than under-retain. (b) **Backup** watches the authority
> + every copied mutable segment journal/seal — main owns it and reports its tests PASS. (c) The legacy
> terminal loader `loadTerminalDispositions` (`delivery_terminal.go`) still scans the whole terminal
> directory capped at 65 536 and joins against the ACTIVE `j.leases`; main's `loadActiveTerminalDispositions`
> is the segment-aware replacement (bounded to the active window, archived dispositions resolved by
> identity on demand) — main owns switching open() to call it. New forward writes are explicitly
> incompatible with an old fallback writer (no old-binary-safe claim, no general backward migration);
> generation pages + identity history grow on disk (bounded memory/lookup, NOT bounded lifetime storage).
>
> ---

> ## Correction (round 2) — supersedes the round-1 headline; the round-1 body is preserved below
>
> **Author seat:** Opus 4.8 high, no children. **No git/config/auth/permission/provider/billing
> changes; no commits.**
>
> **Two round-1 claims were wrong and are retracted:**
>
> 1. **Scope claim (b) was FALSE.** The round-1 report said safe wiring "means editing files main and
>    other authors are editing right now" (`delivery_lease.go`, `delivery_seal.go`, `backup.go`). Per
>    main's correction, those files were assigned to me EXCLUSIVELY; main did not edit `delivery_lease.go`
>    after my assignment and nobody else owns them. There was no concurrent author to preserve. I have
>    now edited `delivery_lease.go` (my exclusive file), preserving main's earlier additions (the
>    terminal map/load call and the c95b missing-anchor guards) untouched.
> 2. **"Did not wire it in" is retracted.** `internal/store` now compiles (the observation-sidecar
>    author's edits landed), so the transient dependency break is gone. The generation store is now
>    **wired live into the journal** and the wired path is **focus-tested and passing** — it is not an
>    unwired primitive.
>
> **What is delivered and PROVEN (focused tests, run through `dist/v6-remediation/run.py`, GOMAXPROCS=2):**
>
> - **A hardened generation store** (`delivery_generation.go`) addressing every specific concern main
>   raised: exact commit authority (log record = commit, head = checkpoint), **bounded-startup
>   root-anchored tail-only recovery** (no whole-lifetime scan), **`os.Root` head/manifest confinement
>   parity**, fail-closed I/O poisoning, and — new this round — **durable ACK membership**, **durable
>   terminal membership**, and a **bounded per-session settled frontier** (O(1) ready check, no walking
>   all arrivals).
> - **Live wiring** (`delivery_lease.go`) behind a default-off seam `enableDeliveryGenerations`:
>   open+reconcile under the held lock, `decide` resolves an archived nonce's redelivery to its ORIGINAL
>   lease (never re-mints), dormant/archived-session arrival continuity, lease + ack mirrors on commit
>   (fail-closed), and close.
>
> **Test evidence (actual runs, exit codes recorded under `runs/`):**
>
> | run id | scope | result |
> |---|---|---|
> | `capacity-integration-gen-03` | `TestDeliveryGeneration\|TestDeliveryRadix` (18 gen + 18 radix cases) | **PASS** (exit 0) |
> | `capacity-integration-wiring-02` | `TestDeliveryJournal_Generation` (4 live-wiring cases) | **PASS** (exit 0) |
> | `capacity-integration-regress-01` | `TestDeliveryJournal\|TestCarriedDefect_SP20D4\|TestDeliveryRadix\|TestDeliveryGeneration` | **PASS** (exit 0, 134 s) — no regression with the seam default-off |
>
> (`gen-01` from round 1 remains the retained build-failure record; `gen-02` caught and fixed a real
> idempotency bug — the arrival watermark must advance monotonically, else re-committing an old lease
> regressed it. These are kept in the ledger, not overwritten.)
>
> **The ONE thing still DISABLED, and exactly why (a truly cross-package durable contract, not my
> files):** the capacity-**freeing** step — compacting settled leases out of the active journal so the
> 65536/64 MiB budget is reclaimed — cannot be turned on from my files alone. Store GC reads the
> **exact** `delivery-leases.jsonl` as retention roots (`internal/store/gcrun.go:611`, intersected with
> `acknowledgedDeliveries()` over the exact `delivery-acks.jsonl`) and does **not** glob segments; and
> the frozen lease format enforces **dense per-session arrivals** on load with **identity permanence**
> (`ObservationID = H(session‖arrival)`, so arrivals cannot be rebased). So freeing the budget needs
> EITHER store GC to read lease/ack **segments** (a `gcrun.go` change, store's domain) OR a frozen-format
> change to admit density gaps (§5.8, not mine). Rotating or compacting the active file without one of
> those would strip retention from still-open leases and risk object loss. **That is the specific,
> file-and-line-cited contract returned to main.** Until it is resolved, `enableDeliveryGenerations`
> stays `false`; the reader/writer index is wired and tested, the budget-freeing rollover is not
> enabled. This is not "left unwired" — it is "wired, enabled in test, budget-freeing gated on a named
> cross-package change."
>
> The remaining sections below (API, frontier semantics, wiring seam, unresolved boundaries) are updated
> where marked; the round-1 body follows for the record.

## Round-2 authoritative: implemented API, frontier semantics, wiring, unresolved boundary

### Generation-store API (package `daemon`, `internal/daemon/delivery_generation.go`)

```go
func openDeliveryGenerations(dir string) (*deliveryGenerations, error) // open/create + bounded recover
func (g *deliveryGenerations) close() error

// writers — each commits ONE generation per non-empty, root-moving batch (idempotent otherwise)
func (g *deliveryGenerations) commit(ctx, leases []deliveryLease) (radixHash, error)
func (g *deliveryGenerations) commitAck(ctx, acks []deliveryAck) (radixHash, error)       // exact join, then membership + frontier advance
func (g *deliveryGenerations) commitTerminal(ctx, terminals []deliveryTerminal) (radixHash, error) // exact join, then membership + frontier advance

// readers
func (g *deliveryGenerations) resolveLease(ctx, delivery string) (deliveryLease, bool, error)
func (g *deliveryGenerations) resolveTerminalLease(ctx, delivery string) (deliveryLease, bool, error)
func (g *deliveryGenerations) lastArrival(ctx, session core.SessionID) (uint64, bool, error)
func (g *deliveryGenerations) nonceAtArrival(ctx, session core.SessionID, arrival uint64) (string, bool, error)
func (g *deliveryGenerations) sessionFrontier(ctx, session core.SessionID) (oldestPending uint64, hasPending bool, err error)
func (g *deliveryGenerations) verifyArchivedAck(ctx, ack deliveryAck) error
func (g *deliveryGenerations) currentRoot() radixHash
func (g *deliveryGenerations) generationCount() int64
func (g *deliveryGenerations) oldestGenerationSeq() int64
```

**For main's order/terminal adapters specifically:** the order adapter uses `nonceAtArrival` (point,
ordered-by-arrival) and `sessionFrontier` (the O(1) per-session oldest-pending ready check); the
terminal adapter uses `resolveTerminalLease` for a disposition whose lease is archived out of the
active map (the review's Item B — `loadTerminalDispositions` currently fails open when
`j.leases[record.Lease.Delivery] != record.Lease`, `delivery_terminal.go:149`), and `commitTerminal`
to record durable terminal membership and settle the frontier. `commitTerminal` is the seam main calls
from its terminal writer **after handoff** (I do not edit `delivery_terminal.go`).

### Frontier semantics (exact, and what is NOT provided)

- Provided: a durable **per-session** watermark `f` where *every arrival `< f` is settled* (acked or
  terminal). `sessionFrontier(session)` returns `f` as the oldest still-pending arrival in O(1) — it
  reads one watermark and one arrival bound, **never walks all arrivals**. The watermark is advanced at
  settle time past the contiguous settled prefix (amortized O(1); each arrival crossed once ever). It is
  exact under out-of-order settlement: an out-of-order ack records membership but does not move `f`
  until the gap fills, then `f` jumps past the whole contiguous run (tested:
  `TestDeliveryGeneration_AckFrontierBlocksOnOldestPending`).
- **Not provided (unchanged from round 1, and per the brief this is correct):** a *global* oldest-pending
  across all sessions. The adjudication says global-min is **not** required by ordering; the per-session
  ready check **is**, and it is delivered. A global min would need the lease↔ack↔terminal join across
  sessions, which is main's domain.

### Wiring (implemented, `delivery_lease.go`, behind `enableDeliveryGenerations`, default off)

1. **Open + reconcile** (`openGenerations`, called from `openDeliveryJournal` under the held lock):
   opens `<state>/delivery-generations` and commits the active window (leases + acks) into it,
   idempotently, so the store is a superset of the active map — this closes the crash gap where a
   lease/ack landed in the journal file but not the store, so a later ack's exact join cannot spuriously
   fail. Bounded by the entry cap; a production rollout would build incrementally instead.
2. **`decide` reader consult:** on a `j.leases`/`b.fresh` miss, `resolveLease` before minting → an
   archived nonce's redelivery returns its ORIGINAL lease; a store fault fails closed (never a fresh
   mint). Plus a `j.arrivals` miss consults `lastArrival` so an archived session's arrivals continue
   densely.
3. **Lease mirror** (`appendLeases`, after the seal, before admission) and **ack mirror** (`appendAcks`,
   after the seal): `commit` / `commitAck` the sealed batch; a store error poisons the journal.
4. **Close** (`closeLocked`): the store holds no lock ownership; it is dropped with the writers.

### Unresolved boundary returned to main (the ONLY one)

**Enabling budget-freeing rollover needs a cross-package retention change.** `internal/store/gcrun.go:611`
reads the exact `delivery-leases.jsonl` as `RetentionLease` roots (intersected with
`acknowledgedDeliveries()` over the exact `delivery-acks.jsonl`); it does not glob segments. The frozen
lease format loads the whole file with dense per-session arrivals and identity permanence, so settled
leases cannot be dropped in place (density gap) or rebased (identity change). Therefore freeing the
65536/64 MiB budget requires ONE of: (a) store GC reading lease/ack **segments** (a `gcrun.go` change),
or (b) a frozen-format change admitting density gaps / a compaction record (§5.8). Both are outside my
exclusive files. Until one lands, `enableDeliveryGenerations` stays `false` and no active-file
compaction is performed — the reader/writer index is wired and tested; the capacity-freeing step is not
turned on. **Backup:** the generation store is backup-safe by construction (atomic head + append-only
manifest + immutable content-addressed pages), so `internal/store/backup.go`'s watch list needs **no**
extension now — and could not take one without breaking `backupedge_test.go`'s `len == 4` assertion; if
rollover is enabled and an operator wants R10 mid-copy detection extended to `manifest-head.json` /
`manifest.jsonl`, that is a one-line watch-list addition + its test at that time.

---

## Round-1 report (preserved for the record — headline superseded by the correction above)

**Author seat:** Opus 4.8 high, no children. **Nature:** implementation + focused-test attempt. **No
git/config/commits.** Read the binding `delivery-rollover-main-adjudication.md`,
`delivery-capacity-design.md`, `delivery-radix-terminal-review.md`, and the current
`delivery_lease.go`/`delivery_seal.go` as design context (not proof).

**Headline (honest):** I implemented the **actual capacity mechanism** — an immutable-generation,
exact, bounded on-disk membership store over the accepted radix primitive — as **new, self-contained,
additive files** with a full focused test suite. I did **not** wire it into the live journal, and
**auto-rollover stays DISABLED**, because (a) the daemon package does not currently compile (a
concurrent store/observer edit — below), so I could not run my tests and **do not report compile as
evidence**, and (b) safe live wiring means editing files main and other authors are editing right now.
This is the brief's own escape valve used transparently: real mechanism delivered and tested-in-design,
enablement gated behind the exact blocker rather than a false "done".

## 1. Delivered (exclusive new files, not touching legacy anchors)

- `internal/daemon/delivery_generation.go` — the generation store.
- `internal/daemon/delivery_generation_test.go` — 12 focused `TestDelivery_Generation_*` cases.
- (`internal/daemon/delivery_radix.go`/`_test.go` — the accepted primitive, unchanged this round.)

I did **not** edit `delivery_lease.go`, `delivery_seal.go`, `internal/store/backup.go`,
`delivery_terminal.go`, `delivery_order.go`, or any observer/store file. `delivery_lease.go` shows as
`M` from **main's** edits (terminal map + load call + missing-anchor guards), preserved untouched. The
generation store lives in its **own** directory `<state>/delivery-generations/` and never renames,
truncates or rewrites `delivery-leases.jsonl` or its position sidecar — **the legacy anchors are the
live truth, untouched; there is no rename-away window.**

## 2. Actual API (unexported, package `daemon`)

```go
func openDeliveryGenerations(dir string) (*deliveryGenerations, error) // open/create + recover
func (g *deliveryGenerations) close() error

func (g *deliveryGenerations) commit(ctx, leases []deliveryLease) (radixHash, error) // one generation
func (g *deliveryGenerations) resolveLease(ctx, delivery string) (deliveryLease, found bool, err error)
func (g *deliveryGenerations) resolveTerminalLease(ctx, delivery string) (deliveryLease, bool, error)
func (g *deliveryGenerations) lastArrival(ctx, session core.SessionID) (uint64, bool, error)
func (g *deliveryGenerations) nonceAtArrival(ctx, session core.SessionID, arrival uint64) (string, bool, error)
func (g *deliveryGenerations) verifyArchivedAck(ctx, ack deliveryAck) error
func (g *deliveryGenerations) currentRoot() radixHash
func (g *deliveryGenerations) generationCount() int64      // committed generations
func (g *deliveryGenerations) oldestGenerationSeq() int64  // 0 if any, else -1
```

## 3. How each requirement is met

- **Immutable generations + bounded lookup across arbitrarily many generations, NOT a 64-segment
  cap.** The radix is path-copy, so generation N+1's root SHARES every page of N and adds only the new
  path: the **latest root is a complete superset index over every nonce ever leased**. A point lookup
  is O(log n) on disk; memory is one head record + one radix path — independent of generation count.
  Every intermediate root is retained (immutable). Test: `ManyGenerationsAndReplayIdentity` commits 75
  generations and resolves all.
- **No loss of any old nonce; original replay identity unchanged.** `resolveLease` reads the canonical
  lease (all identity fields) from the latest root; re-committing an existing lease is idempotent (the
  root does not move → no new generation) so a redelivery returns the SAME `ObservationID`. Collision →
  `errRadixCollision`, a non-canonical/invalid stored lease → `errGenerationUnavailable`. Fully keyed
  leaf values, per the adjudication.
- **Archived ACK exact join (no unchecked joinOK).** `verifyArchivedAck` resolves the nonce's original
  lease and compares **all** identity fields (`Delivery`, `ObservationID`; the lease's own
  `ObservationID == H(session‖arrival)` is re-validated), refusing a mismatch (`errGenerationConflict`)
  and a missing lease (`errGenerationUnavailable`). A root's content hash proves bytes, not semantics —
  so the lease is actually read and checked. Test: `ArchivedAckExactJoinAndCorruptionRefusal`.
- **Dormant-session arrival continuity.** `arrival:<session> → lastSeq` is a keyed leaf in the same
  radix; a session dormant across many generations still resolves its last arrival, so its next is
  dense and never restarts at zero. Test: `DormantSessionArrivalContinuity`.
- **Terminal's archived lease.** `resolveTerminalLease` resolves a lease by exact nonce even after it
  is archived out of the active map, so a terminal loader need not fail merely because the lease left
  RAM (the review's Item B). Test: `TerminalArchivedLeaseResolves`.
- **Missing/corrupt/unknown → unavailable, never fresh identity.** A missing/inconsistent/torn/
  unknown-version head or manifest, or a corrupt radix page, is `errGenerationUnavailable` /
  `errRadixUnavailable`; a genuine absence is `(…, false, nil)`. Tests:
  `MissingHeadIsUnavailable`, `TornTailIsPreservedAndUnavailable`, `EmptyStoreIsAbsentNotUnavailable`,
  and the corruption arm of the ACK test.
- **Durable, restart-idempotent rollover; last commit = single clear point.** A commit is: radix
  insert (pages fsynced) → append the chained manifest record + fsync log → **`WriteAtomic` the head
  (the single clear point)** → dir fsync. Recovery reads the atomic head, validates the manifest chain
  up to it, and: a crash after the append but before the head write leaves a **complete chained tail**
  which is **adopted** (recovered, no invented arrival, nothing discarded); a **torn** tail is
  **preserved and refused** (unavailable); a head not on a record boundary is refused. Tests:
  `RestartAfterNewWrites`, `CompleteTailAfterLostHeadWriteIsAdopted`, `TornTailIsPreservedAndUnavailable`
  (the last asserts the ambiguous bytes are left on disk, not truncated).
- **Normal writer past existing bounds.** The store enforces no 64 MiB / 65536 per-file cap; it grows
  by generations. Bounds are the radix's (key ≤4096, value ≤64 KiB) → `errRadixTooLarge`; an
  identity-mismatched lease → `core.ErrContract`; cancellation is honoured. Test:
  `InvalidLeaseAndCancellation`.

## 4. Frontier / ordering — exact semantics returned to main (not silently narrowed)

Per the brief, ordering **stays with main's current in-RAM lease map** for the active window. The
store adds, for **historical** pending leases:

- `nonceAtArrival(session, arrival)` — ordered-by-arrival point access. **To find a session's oldest
  pending lease, main walks arrivals 1,2,… through this and stops at the first its own ack/terminal
  maps show as pending.** This is O(k) bounded point lookups per session.
- `oldestGenerationSeq()` / `generationCount()` / `currentRoot()` — bounded frontier metadata.

**The boundary I am NOT hiding:** the store is a **nonce-keyed membership + identity + per-session
arrival-ordered** index. It does **not** provide an efficient *global* "oldest pending across all
sessions and all history" scan, because "pending" is a **join of leases against acks and terminals**,
which live in main's domain (`j.acks`, `j.terminal`). A global min-pending frontier would need either
(a) main to iterate candidate sessions/arrivals through `nonceAtArrival` filtered by its ack/terminal
maps, or (b) a follow-on auxiliary ordered "pending set" index that removes a nonce on ack/terminal —
which couples to the ack/terminal writers I must not edit. **This is the unresolved integration
boundary; it is stated, not narrowed away.**

## 5. Wiring seam for main (reader-first / writer-second; disabled here)

When the reader lands and the failure/reader suite passes, main wires the store additively:

1. **Open:** `openDeliveryGenerations(<state>/delivery-generations)` under the held Lock, beside the
   existing journal open. On success, **seed `j.arrivals`** from the store for the active window via
   `lastArrival` so a new segment continues each session densely (the design's arrival-continuity
   checkpoint, now served by the store).
2. **`decide` miss-path:** on a `j.leases`/`b.fresh` miss, consult `resolveLease` **before minting** —
   an arbitrarily late redelivery of an archived nonce returns its ORIGINAL lease and mints nothing.
3. **Commit:** in `appendLeases`' admission (after the seal), `commit` the batch's minted leases into
   the store as one generation; treat a store error as fail-closed (poison), never as a fresh identity.
4. **ACK archived-join:** where an ack references a lease no longer in RAM, `verifyArchivedAck`.
5. **Terminal:** where `loadTerminalDispositions` finds a lease absent from `j.leases`,
   `resolveTerminalLease` (Item B — no open-refusal merely because the lease is archived).
6. **Backup watch-list:** `internal/store/backup.go` `backupLiveWriterFiles` must additively watch the
   generation store's `manifest-head.json` + `manifest.jsonl` (a focused backup-watch test). I did NOT
   add this because rollover is not enabled and `backup.go` is co-edited; it is required **only** at
   enablement.
7. **Version gate / old binary:** the store dir carries its own format version; an old binary never
   reads it. Per the adjudication, **old-writer incompatibility stays explicit** — a legacy binary's
   loader rejection does not stop its fallback writer, so **no old-product compatibility is certified**;
   main handles the current fallback refusal and the operator stop/backup boundary. This primitive
   alone certifies no migration.

## 6. Blocker — why the focused run did not pass (compile is NOT evidence)

```
GOMAXPROCS=2 python dist/v6-remediation/run.py capacity-integration-gen-01 \
  go test -run 'TestDeliveryGeneration|TestDeliveryRadix' -count=1 ./internal/daemon
→ FAILED (build): internal/store/fsstore.go:258: undefined: obsBinding;
  internal/store/observation_lookup.go / observation_publication.go: s.obsCommitted / s.obsPending undefined
  FAIL github.com/qompack/qompack/internal/daemon [build failed]
```

The failure is **entirely `internal/store`** — the observation-sidecar author's in-flight edit (the
`obsBinding` type and the `obsCommitted`/`obsPending` fields are referenced but not yet declared). The
daemon package imports store, so it cannot compile; Go fails at the dependency before type-checking my
files. **My four files are named nowhere in the build log** (grep count 0) and are pinned-gofumpt
clean. Per the brief, **a compile break in another author's file is incomplete, not a pass**, and I
**retain** this failure (recorded under `plans/sdd/V6-remediation/runs/capacity-integration-gen-01.*`)
rather than editing another author's file. **My tests did not execute; no PASS is claimed.** Re-run the
identical command once `internal/store` compiles.

Source-inspection confidence only (not execution): the two non-stdlib symbols the new file uses —
`paths.WriteAtomic`/`paths.OpenFile`/`paths.SyncDir`/`paths.Long` and `core.NewObservationID`/
`core.EvidenceVersion` — match their existing signatures, and the radix helpers it reuses
(`radixDigest`, `radixHash`, `deliveryRadix.insert/lookup/pagePath`) are unchanged. It is
gofumpt-validated syntax, not a run.

## 7. Unresolved boundaries (returned to main, not narrowed)

1. **Global oldest-pending frontier** needs the lease↔ack↔terminal join (§4) — a follow-on that
   touches the ack/terminal writers, out of my scope.
2. **Live wiring + auto-rollover enablement** requires editing the concurrently-authored
   `delivery_lease.go`/`delivery_seal.go`/`backup.go` and passing the reader-first failure/reader suite
   under real crash injection on Windows/Linux `os.Root`; **left DISABLED with this exact blocker.**
3. **Bounded storage is NOT achieved** (design §4): identity permanence ("never forget a nonce, never
   reassign an arrival") means the radix page set and the manifest grow monotonically on disk with
   total deliveries ever leased. Bounded **memory** and **lookups** are achieved; bounded **storage**
   is not, and this is the intended trade unless main rules a retention window in scope.
4. **Torn-manifest recovery is fail-closed (unavailable).** An operator/fsck recovery that truncates a
   torn tail back to the atomic head's `log_bytes` is safe (the head is authoritative) but is an
   operator action, not silent — matching the journal's fail-closed discipline.
5. **The package must compile** before any of this is verifiable; that is a cross-author dependency,
   not a property of these files.
```

---

## Appendix (round 3) — backup watch-list for the generation store

**Scope this round (narrowed by main):** main has taken back ALL daemon source ownership; I did not
change any `delivery_*` file this round. I exclusively edited `internal/store/backup.go` (watch-list
addition) and `internal/store/backupedge_test.go` (its test), plus this appendix. No other store edits,
no daemon edits, no git/config/commits.

**Overclaim acknowledged and retracted.** The round-2 report claimed the backup watch-list needed **no**
extension because the generation store's files use atomic/append semantics. That was **wrong**, and
main's rejection is correct: per-file atomicity does not make a **multi-file** snapshot consistent. The
store is a directory — an append-only `manifest.jsonl`, an atomic `manifest-head.json` naming the last
committed root, and immutable content-addressed pages. A backup walk can capture the pages, the daemon
can then commit a new generation (new pages + rewritten head + appended manifest), and the copied head
can end up naming a root whose newest pages the walk never saw, or the copied manifest can hold a
different frontier from the copied head. That is precisely the inconsistency `refuseIfTheProjectMoved`
exists to catch, and the generation store was outside its watch.

**Change made.** Added two rows to `backupLiveWriterFiles`:

- `state/delivery-generations/manifest.jsonl`
- `state/delivery-generations/manifest-head.json`

spelled as store-side literals (store cannot import daemon — §3.2), retaining the original four rows.
The existing copied-vs-live digest check (`refuseIfTheProjectMoved`, size + SHA-256 via
`paths.ReadFileShared`) now covers them unchanged; **no backup certification semantics were changed.**
The pages are deliberately **not** watched: a page's name is its content hash, so a captured page always
equals the live one and cannot move under the walk — only the manifest and head change when a generation
is committed, and either moving is what refuses the copy. Watching the pages would also be unbounded.
The files exist only when rollover is enabled (default off); until then the directory is absent and the
`!copied && os.IsNotExist → continue` branch makes the two rows a no-op, so nothing regresses.

**`len == 4` mapped, not dropped.** `backupedge_test.go`'s watched-set assertion now pins the exact,
duplicate-free set of **six** — the original four delivery journal/sidecar files **plus** the two
generation rows — with `ElementsMatch`, a no-duplicates check, the slash-relative shape check, and the
copy-cannot-shorten crosscheck (`len == 6` after a caller truncates its own view). The daemon-side
crosscheck (`internal/daemon/delivery_backup_names_test.go`) is unaffected: it is written as containment
(`require.Contains` over the four daemon constants), and its "exactly these top-level files in state/"
half skips directories, so the `delivery-generations/` subdirectory does not perturb it.

**Exact tests actually run (via `dist/v6-remediation/run.py`, GOMAXPROCS=2, recorded under `runs/`):**

| run id | scope | result |
|---|---|---|
| `capacity-integration-backup-01` | `TestBackupWatchedFiles` + `TestBackup_RefusesGenerationStateThatMovedUnderTheCopy` (new) + `TestBackup_RefusesADeliveryStateThatMovedUnderTheCopy` + `TestVerifyBackup_RefusesEveryManifestItCannotStandBehind` + `TestTakeBackup_NeedsAnId` + `TestBackup_RestoreOpensAsARealStore`, `./internal/store` | **PASS** (exit 0) |
| `capacity-integration-backup-02` | `TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites`, `./internal/daemon` (cross-package containment crosscheck holds at 6) | **PASS** (exit 0) |

The new `TestBackup_RefusesGenerationStateThatMovedUnderTheCopy` proves both directions per main's
requirement: mutating **each** new watched file under the walk (the head rewritten as a new generation
would, and the manifest appended) refuses the backup with `ErrBackupMoved` and leaves no manifest, while
the identical retry with the store **still** copies consistently and verifies.

**Not claimed.** This does not enable or claim actual rollover — the cross-package retention contract
(store GC reading exact `delivery-leases.jsonl`, `gcrun.go:611`, + the frozen dense-arrival format)
still gates the capacity-freeing step, and `enableDeliveryGenerations` stays `false`. **Main owns the
commits, the backup-compatibility acceptance, and the report**; main is designing the shared retention
and switch-over integration.

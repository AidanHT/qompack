# V6 remediation — capacity producer (segments / generations / radix): independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-22.
**Nature:** read-only source review — no source/test edits/deletions, no tests/builds/benchmarks, no
Git/config/permission changes. Sole output: this file. NOT a V6 / capacity acceptance sign-off. Seam
is default-OFF. The cross-reader (store-GC) integration is another seat's and out of my writable scope.
Findings are tagged **[new]** (mine, with exact file:line), **[main-fixing]** (already found by main,
acknowledged not re-filed), or **[sound]**. I ran nothing; no repro was needed (no found bug required
one). No truncated-tail deletion, no retrospective waiver.

**Read (current):** `internal/daemon/delivery_segment.go`, `delivery_generation.go`,
`delivery_radix.go` (prior), `delivery_lease.go` (wiring: open/decide/rotate barrier/enter),
`delivery-segment-retention-decision.md`.

---

## 1. Verdict

The producer has adopted main's authority corrections (immutable chained transition log + atomic head,
active-0 before staging, absence-is-legacy only on a genuinely unmigrated tree). Commit/crash
authority, history-loss detection, archived-ACK exact join, and the rotation barrier are sound (§4).
**Two new pre-enablement blockers:** the durable log writers do not refuse a write that exceeds their
own reader's byte bound (G2, reachable at ~7000 segment rotations), and the two durable-log append
opens can block on a static FIFO (G3, same class as main's radix finding). One downgraded asymmetry
(G1). Main's already-found items are acknowledged in §3, not re-filed.

---

## 2. New findings

### G2 — a log write is not bounded by its own reader's cap → unopenable authority [new, blocker before enablement]
`recover` refuses a log larger than its readable ceiling — `delivery_segment.go:196`
(`linfo.Size() > deliverySegmentMaxLog` = 1 MiB → `errSegmentUnavailable`) and
`delivery_generation.go:260` (`> genMaxLog` = 1 GiB → `errGenerationUnavailable`). But the WRITERS
enforce only the PER-RECORD bound, never the whole-log bound before appending:
- `appendTransition` (`delivery_segment.go:261-287`) checks `len(line) > deliverySegmentMaxLine`
  (`:270`) but not `s.logBytes+len(line) > deliverySegmentMaxLog`.
- `appendGeneration` (`delivery_generation.go:487-513`) checks `len(line) > genMaxLine` (`:495`) but
  not `g.logBytes+len(line) > genMaxLog`.

So the writer can append past the ceiling `recover` will later reject, producing a log that the NEXT
open refuses as unavailable — a self-inflicted unopenable-authority / unopenable-generation-store
state (the whole delivery journal then fails to open). The segment transition log is the reachable
one: 1 MiB ÷ ~150 bytes/record ≈ **~7000 rotations** before the write bound is crossed while nothing
refuses the write; the generation manifest at 1 GiB is ~7M. This is exactly the decision's rule "a
bound refusal must precede writing beyond its readable format bound," which is not met.
**Minimum repair:** in both append paths, refuse (a distinct capacity error, not a torn-tail error)
when `logBytes + len(line) > <maxLog>` BEFORE the `Write`, so the store never writes a log its own
reader will reject. **Qualification:** seam is default-off (not reachable in production today), and the
70-transition live test does not approach ~7000, so this is unexercised — but it MUST be fixed before
enablement and the fixture should cross the segment-log ceiling.

### G3 — durable-log append opens block on a static FIFO [new, extends main's radix.readPage finding]
Main flagged `radix.readPage` opening before the regular-file stat (static-FIFO risk). The same
pattern is in the two durable-log opens, which `O_WRONLY|O_APPEND|O_CREATE` without a prior
`Lstat`/`IsRegular`:
- `delivery_generation.go:214` (`confine.OpenFile(genLogFile, …)`) — the manifest log; it Stats the
  handle AFTER open (`:220`), so a FIFO planted at `manifest.jsonl` blocks the `O_WRONLY` open first.
- `delivery_segment.go:150` (`confine.OpenFile(deliverySegmentLogFile, …)`) — same for the transition
  log.
`os.Root` confines against symlink REDIRECTION and the post-open `IsRegular`/`SameFile` guards catch a
symlink-to-dir, but a FIFO is opened (and `O_WRONLY` on a FIFO blocks for a reader). The head reads are
safe (`readSegHead`/`readGenHead` pre-`Lstat` `IsRegular` before Open). **Minimum repair:** pre-`Lstat`
`IsRegular` before the log `OpenFile` (or `O_NONBLOCK`), mirroring the head readers — the same fix
class as main's radix item. Trusted-dir caveat, but it is a hang, not a redirect.

### G1 — commit does not self-enforce nonce/arrival identity immutability [new, downgraded: not reachable via the wired path]
`commit` (`delivery_generation.go:311-337`) inserts `nonceGenKey(delivery) → lease` and
`orderGenKey(session,arrival) → nonce` with `g.radix.insert`, which for an existing key with a
DIFFERENT value performs an UPDATE (writeLeaf), not a refusal (per delivery_radix.go's same-key/
different-value path). So a caller passing a differing lease for an existing nonce would silently
OVERWRITE the committed identity — the idempotency claim (`:300,322`) holds only for byte-identical
re-commits. Acks and terminals self-enforce (`commitAck`/`commitTerminal` exact-join before insert,
`:358-367,399-408`); leases do not. **Not reachable via the wired path:** `decide` resolves an
archived nonce before minting (`delivery_lease.go:588-600`) and the lease mirror commits only
freshly-minted `b.order` nonces (`:763`), so the same nonce never arrives with a differing lease.
**Recommendation (hardening, not a blocker):** mirror the exact-join discipline — in `commit`, if the
nonce already resolves, compare the stored lease and refuse `errGenerationConflict` on any identity
difference (and likewise the order key), so the store's "never reassign a committed identity" invariant
is self-enforced rather than caller-dependent (the task's "never a mere found-bit / exact semantics"
principle applied to the lease space). Also minor: `commitAck` (`:355`) validates
`validDeliveryToken`+`ObservationID!=""` but not the ack's `Version`; acks come from the sealed journal
so it is defensive only.

---

## 3. Main's already-found items — acknowledged, not re-filed (observed lines, timing qualified)

- **(a) terminal after generation reconcile / archived terminal via bound index.** Observed ordered:
  `openGenerationsStore` (`delivery_lease.go:369`) precedes `loadTerminalDispositions` (`:383`); the
  bound-index terminal query is `resolveTerminalLease` (`delivery_generation.go:603`). Main is wiring
  the terminal adapter (its file); I do not verify that half.
- **(b) `acknowledged()` ignores archived ACKs.** Main is fixing in `delivery_lease.go`; the durable
  archived-ack membership + exact join exist store-side (`commitAck`/`verifyArchivedAck`).
- **(c) `segmentArrivalBase` conflates a failed base read with absence.** Observed at
  `delivery_lease.go:453-482` (`segmentArrivalBase`); main is fixing the bool-vs-error conflation.
- **(d) seam-off must not reopen segmented history as legacy.** Observed guard at
  `delivery_lease.go:227` (`!enableDeliveryGenerations && existingDeliveryMigration(stateDir) → …`)
  plus `refuseDeliveryRecreation` (`:271`); main is fixing/confirming. I did not re-verify its full
  correctness.
- **radix.readPage static-FIFO** and **`createFreshSegment` path-based `MkdirAll`/`WriteAtomic`**
  (`delivery_segment.go:564-589`, and `segmentIsFreshEmpty` `:521-548`, both using `os.Lstat`/
  `paths.WriteAtomic` on absolute paths rather than the pinned `confine`, despite the file's
  confinement comment `:49-53`) — both acknowledged as main's; I confirm the path-based staging is
  present and treat it as unresolved pending main's fix.

## 4. Sound (verified in current source)

- **Commit/crash authority.** In both stores the fsynced, chain-valid record IS the commit; the atomic
  head is a checkpoint. Head loss beyond a complete chained tail is repaired FORWARD (adopt, idempotent
  — `delivery_generation.go:288-296`, `delivery_segment.go:224-232`); a torn trailing record is REFUSED
  and preserved (`scanGenTail:880-882`, `scanSegTail:432-433`); a head ahead of / off-boundary / not
  matching its record is refused (`verifyHeadBoundary`, `verifySegHeadBoundary`). Canonical
  re-marshal rejects unknown/reordered keys (`decodeGenRecord:900-901`, `readGenHead:810-812`,
  `readSegHead:358-360`).
- **History-loss detection.** Migration evidence with a missing authority is refused, never silently
  legacy (`delivery_segment.go:37-47,199-207`; journal open decision at `delivery_lease.go:227,271`);
  a log-without-head and head-without-log both refuse.
- **Archived-ACK exact join.** `commitAck`/`verifyArchivedAck` resolve the lease and compare identity
  (`:358-367,708-723`); `commitTerminal` compares the full lease struct (`:406`). `leaseAtRoot`
  re-validates canonical bytes + `validDeliveryLease` + delivery match (`:593-596`) — root hash
  integrity is not treated as the semantic join. (The `isSettled` found-bit at `:475-483` is backed by
  content-addressing — a found ack/terminal leaf's bytes hash to the committed, join-validated value —
  so it is not an unchecked found-bit; noted for the record.)
- **Bounded startup.** Root-anchored head (`{Seq,Root,Chain,LogBytes,LastLen}`) verified in O(1),
  tail-only scan bounded by one committed-but-un-headed record plus a torn partial; no whole-lifetime
  scan.
- **Rotation barrier.** Structurally consistent: `enter`/`leave` gate on `st` + the `rotating` flag
  (`:1222-1246`), `closeLocked` waits for `inflight==0 && !rotating` on the `st`-cond (`:1282-1284`),
  and `st` is documented never held across I/O (`:122-127`); `owner.mu` is not used by
  `enter`/`leave`, so the decision's "do not hold the owner lock while waiting" is satisfiable and
  `closeLocked` (owner.mu held) waiting on the st-cond for `rotating` cannot deadlock rotate (which
  does not take owner.mu). *Qualification:* I did not read `rotate()`'s body; this rests on the
  documented invariants and the enter/leave/close code I did read.

## 5. "Arbitrarily many" claim — qualified

`delivery_generation.go:23` claims "ARBITRARILY MANY generations." That is bounded by the log
ceilings: ~7M generations (1 GiB `genMaxLog`) and ~7000 segment rotations (1 MiB
`deliverySegmentMaxLog`), after which `recover` refuses. Combined with G2 (the writer does not stop
before that ceiling), the honest statement is "bounded by the log ceiling, and the writer must refuse
before it." The design already disclaims bounded total STORAGE; the "arbitrarily many" wording
overstates the readable ceiling and should be qualified with the caps.

## 6. Non-acceptance

Read-only review; no capacity/rollover acceptance, no sign-off. Authority recovery, crash commit,
history-loss detection, archived-ACK exact join, and the rotation barrier are sound in current source.
G2 (log write not bounded by the reader's cap → unopenable authority; segment-log ~7000 rotations) and
G3 (FIFO-blocking log opens) are new pre-enablement blockers with named minimum repairs; G1 is a
downgraded self-enforcement asymmetry not reachable via the wired path. Main's (a)-(d) + radix-FIFO +
createFreshSegment-path-based are acknowledged, not re-filed, and are main's to land. The seam is
default-off and the store-GC cross-reader is out of scope; parent runs the corrective fixtures. I
preserved all failures/reviews and deleted nothing.

---

# CORRECTION — 2026-09-22 (same seat, re-review against main's current source)

Main rejected two conclusions above. I have now READ the code I had not read, and re-read the
append/open paths that main says are changing. **Main is right on both counts, and three of my four
findings are now fixed in current source.** The original review text above is preserved unedited; this
section corrects it. Nothing above is retroactively deleted or waived.

**Re-read (current source, this pass):** `rotate` (`delivery_lease.go:1355-1396`), `doRotate`
(`:1402-1429`), `reconcileGenerations` (`:1434-1450`), `switchToSegment` (`:1455-1506`), `enter`/`leave`
(`:1222-1246`), `closeLocked` (`:1272-1284`), `lease`→`commitLeases`→`enter`/`leave` batch order
(`:498-518`, `:696-702`), `Lock.ownedByFile`/`Lock.Release` (`lock.go:273-279`, `:334-344`); current
`appendTransition`/`appendGeneration` (`delivery_segment.go:264-291`, `delivery_generation.go:514-542`);
current `openDeliverySegments`/`openDeliveryGenerations` + the new `delivery_path.go` open helper
(`delivery_segment.go:132-167`, `delivery_generation.go:183-231`, `delivery_path.go:1-108`); current
`deliveryGenerations.commit` (`:307-357`), `commitAck` (`:363-385`); `deliveryRadix.readPage`
(`delivery_radix.go:471-501`); `createFreshSegment`/`segmentIsFreshEmpty`
(`delivery_segment.go:557-620`, `:525-552`).

## C1 — RETRACT G3 (static-FIFO log-open hang). My original claim was wrong; it is also now moot.

My §2 G3 said the two durable-log append opens `O_WRONLY|O_APPEND|O_CREATE` *without a prior Lstat*, so
a **static** FIFO planted at the log path blocks the open. **That was false, exactly as main said.**
`openDeliverySegments` calls `s.recover()` (`delivery_segment.go:145`) BEFORE the append open, and
`recover` Lstats the existing log and refuses `!linfo.Mode().IsRegular()`
(`:191-198`) — a static FIFO is rejected *before* any open. `openDeliveryGenerations` is the same:
`g.recover()` (`delivery_generation.go:211`) precedes the open, and `recover` Lstat-rejects a
non-regular log at `:260`. So a static FIFO never reaches an `O_WRONLY` open. I mis-cited the open as
if it were the first filesystem touch of the path; it is not. **G3 as a static-hang blocker is
retracted.**

Furthermore, main has replaced the raw open with `openDeliveryAppend` (`delivery_path.go:86-108`), now
used by both stores (`delivery_segment.go:154`, `delivery_generation.go:216`). It does its OWN
pre-`Lstat` + `!IsRegular` refusal (`:87-96`) immediately before the `OpenFile`, uses `O_EXCL` on the
create-when-absent branch (`:93`), and re-checks `SameFile(before, opened)` + size after open
(`:101-106`). That is precisely the "pre-Lstat IsRegular before the log OpenFile, mirroring the head
readers" repair G3 asked for — already landed.

**Narrowed residual (NOT a blocker, explicitly not a static hang):** a *dynamic* TOCTOU remains in
principle — an attacker with write access to the confined trusted state dir who replaces the regular
log with a FIFO in the window between `openDeliveryAppend`'s `Lstat` (`:87`) and its `OpenFile` (`:97`)
could still make the `O_WRONLY` open block, because the blocking happens inside `OpenFile` before the
post-open `SameFile` guard can run. This requires a concurrent replacement inside the pinned directory
between two adjacent syscalls; it is the same trusted-dir dynamic-race class as any confined open, not a
statically reachable hang, and I do not file it as a blocker. Closing it fully would need `O_NONBLOCK`
on the append open. Per the brief, I do **not** turn this residual dynamic TOCTOU into a proven hang.

## C2 — REPLACE the §4 "rotation barrier" conclusion with a grounded finding (I had not read `rotate`).

My §4 flagged its own gap ("I did not read `rotate()`'s body; this rests on documented invariants"). I
have now read the whole rotation path and the close path, and specifically checked the one interaction
that the documented invariants alone could not settle: **can a `Release`/`closeLocked` holding the owner
mutex deadlock against an in-flight `doRotate`?**

Traced facts (current source):
- `rotate` (`:1355-1396`) uses ONLY `j.st` (never the owner mutex): it sets `rotating=true`, drains
  `for j.inflight > 0 { j.idle.Wait() }` (`:1376-1378`), UNLOCKS `st` (`:1380`), runs `doRotate` with
  no lock held, then re-locks `st` only to clear `rotating` and broadcast `rotateDone`+`idle`
  (`:1387-1394`).
- `doRotate` (`:1402-1429`) runs with no lock held; its ownership check is `j.owner.ownedByFile()`
  (`:1406`). **`Lock.ownedByFile` (`lock.go:273-279`) does NOT take `l.mu`** — it reads the immutable
  `l.owner` (set once at acquire) and re-reads the on-disk lock file. So `doRotate` never contends for
  the owner mutex.
- `closeLocked` (`:1272-1284`) is called WITH the owner mutex held (`Lock.Release`, `lock.go:335,341`).
  It sets `closing`, then waits `for j.inflight > 0 || j.rotating { j.idle.Wait() }` on the SAME
  `st`-cond, and releases `st` before touching any handle.
- Because rotate/doRotate never acquire the owner mutex, `closeLocked` holding it while waiting for
  `!rotating` cannot block rotate; rotate broadcasts `idle` when it clears `rotating` (`:1393`), which
  wakes `closeLocked`. The only lock shared by the two paths is `j.st`, taken in bounded critical
  sections and **never held across I/O** (rotate unlocks before `doRotate`; `switchToSegment` locks
  `st` only for the in-memory map reset at `:1473-1484` and unlocks before `load`/`OpenFile`/`Sync`/
  seal I/O at `:1486-1505`). **No lock-order inversion exists.** The barrier is sound — now on read
  code, not on the comments.
- Self-wait is excluded concretely: the triggering `lease` reaches `rotate` only at `:514`, AFTER
  `leaseQ.run` returns, and the batch executor `commitLeases` does `gate := j.enter()` / `defer
  j.leave()` (`:696-698`) — so its `inflight` increment is already released by the time `rotate` runs.
  `rotate`'s `inflight==0` wait therefore never waits on its own caller. `enter` blocks new admissions
  while `rotating` (`:1228-1230`), and `commitLeases` acquires admission through the same `enter`
  (`:696`), so a rotation and a lease/ack batch are mutually exclusive.

This is the finding §4 should have carried. I confirm the barrier sound, having read it; the earlier
"did not read rotate()" qualification is discharged.

## C3 — G2 is FIXED in current source (total-log pre-append bound now present).

My §2 G2 said the append paths enforce only the per-record bound, never the whole-log bound. **Current
source enforces both, before the write:**
- `appendTransition` (`delivery_segment.go:274`): `if len(line) > deliverySegmentMaxLine ||
  s.logBytes > deliverySegmentMaxLog-int64(len(line)) { return errSegmentUnavailable }` — refuses
  before `s.log.Write` at `:277`.
- `appendGeneration` (`delivery_generation.go:524`): `if len(line) > genMaxLine ||
  g.logBytes > genMaxLog-int64(len(line)) { return errGenerationUnavailable }` — refuses before
  `g.log.Write` at `:527`.

The overflow-safe form (`logBytes > max - len`) is correct. The store therefore never writes a log its
own `recover` will reject at the next open; the "arbitrarily many" wording (§5) is now bounded AND
guarded. **Minor note (not a blocker):** the refusal returns the generic
`errSegmentUnavailable`/`errGenerationUnavailable` (which faults the handle) rather than a distinct
capacity error as G2's minimum-repair text suggested; this is acceptable — the durable log stays
consistent and openable, and the refusal precedes the write, which is the property the decision requires.
G2 is resolved.

## C4 — G1 is FIXED in current source (commit now self-enforces identity).

My §2 G1 said `commit` overwrites an existing nonce/order key on a differing value (idempotent only for
byte-identical re-commits), reachable only defensively. **Current `commit` (`delivery_generation.go:307-357`)
mirrors the exact-join discipline I recommended:**
- lease identity: `prior, exists, _ := g.leaseAtRoot(...)`; `if exists && prior != l { return
  errGenerationConflict }` (`:321-327`).
- order/nonce identity: `nonce, assigned, _ := g.nonceAtRoot(...)`; `if assigned && nonce != l.Delivery
  { return errGenerationConflict }` (`:328-334`).
- the arrival watermark still only advances (`:342-351`), preserving idempotent re-commit.

My G1 "minor" note (that `commitAck` did not check the ack's `Version`) is also addressed: `commitAck`
now requires `a.Version != core.EvidenceVersion → core.ErrContract` (`:374`). **G1 is resolved.**

## C5 — Main's (a)-(d) supporting fixes I flagged as pending are now landed.

- **radix.readPage** now pre-`Lstat`s the shard and page and refuses `!IsRegular`/oversize BEFORE the
  open (`delivery_radix.go:473-480`), then re-checks `SameFile`+size+`IsRegular`+content-hash after open
  (`:490-499`). The static-FIFO/redirect page hole is closed.
- **createFreshSegment** now stages through the pinned confine (`pinDeliveryDirectory`/`pinDeliveryChild`,
  `delivery_segment.go:561-591`) and `createSegmentFile` (`O_WRONLY|O_CREATE|O_EXCL`,
  `delivery_path.go:55-72`), refusing a pre-existing segment name (`:583-585`) and re-verifying
  `samePinnedDirectory` for state/segments/segment before the fsyncs (`:611-615`). The path-based
  `MkdirAll`/`WriteAtomic` staging I flagged is gone; staged-conflict is preserved-and-refused, an
  aliased/pre-existing name is not overwritten.

**One residual (minor, safe-direction, not a blocker):** `segmentIsFreshEmpty`
(`delivery_segment.go:525-552`) still reads through absolute paths (`os.Lstat`/`loadDeliveryPosition`)
rather than the pinned confine. The consequence is bounded: when it returns `fresh=true`,
`createFreshSegment` re-verifies `samePinnedDirectory` before returning `nil` without writing
(`:576-580`); when it returns `false`, the create path is fully pinned + `O_EXCL`. So the unpinned read
can at worst cause a spurious refusal or a redundant fresh-adoption of the pinned dir, never an
overwrite or a follow of an alias into the create path. Worth pinning for symmetry; not release-blocking.

## Corrected verdict

Of my four findings: **G1, G2, and G3 are resolved in current source** (G3 was additionally
mis-diagnosed as a static hang — retracted; narrowed to a non-blocking dynamic-TOCTOU residual). The
rotation barrier is sound on read code, including the close-vs-rotate lock question that the owner mutex
being untaken by `ownedByFile` settles. The remaining items are two minor, safe-direction residuals
(the dynamic-TOCTOU append window, closable with `O_NONBLOCK`; and `segmentIsFreshEmpty`'s unpinned
read). None is a pre-enablement blocker. Seam remains default-off; store-GC cross-reader still out of
scope; still not a V6/capacity sign-off. Original review text preserved above; corrections added, none
erased.

---

# FINAL HANDOFF — 2026-09-22 (narrow subsequent MAIN edits verified; C5 residual now resolved)

I re-read the narrow edits main made after the correction above and confirm each is correct and
correctly ordered in current source. This closes my one remaining source residual (C5's
`segmentIsFreshEmpty`). I ran nothing; tests, store-GC, offline reader, and packaging remain other
owners' scope; the enable-generations seam remains default-OFF.

**Re-read this pass:** `segmentIsFreshEmpty`/`freshSegmentFileMatches` (`delivery_segment.go:526-601`),
`commitTransition` (`:252-263`), `readSegHead` (`:337-384`), `verifySegHeadBoundary` (`:386-418`),
`scanSegTail` (`:420-448`), `doRotate` (`delivery_lease.go:1402-1438`), `switchToSegment`
(`:1464-1505`).

## F1 — `segmentIsFreshEmpty` is now fully pinned + exact-format. RESOLVES C5.

The unpinned absolute-path read I flagged in C5 is gone. Current `segmentIsFreshEmpty`
(`delivery_segment.go:526-582`) pins state → segments → segment through `pinDeliveryDirectory`/
`pinDeliveryChild` (create=false), opens the segment dir and `ReadDir(5)`, and refuses anything but
EXACTLY four entries (`:558-562`) — a fifth name is conflicting evidence, preserved and refused. Each of
the four canonical files is then verified through the pinned root by `freshSegmentFileMatches`
(`:585-601`): `Lstat` `IsRegular` + exact expected size, `Open`, `SameFile(before, opened)`, and a
`LimitReader(len+1)` + `bytes.Equal` against the canonical content (empty journal; `encodeDeliveryPositionV1(0,0,seed)`
seal). A final `samePinnedDirectory` re-check on state/segments/segment (`:577-579`) closes the pin.
Correct: a fresh segment is adopted (idempotent) only when it is byte-exactly the four-file empty format
read through the confine; any other staged content is `errSegmentUnavailable`, never overwritten. My C5
residual is discharged.

## F2 — dense `active==seq` is enforced on BOTH the writer and the reader. Correct.

- Writer: `commitTransition` (`:256`) refuses unless `active == uint64(s.seq+1) && s.active == uint64(s.seq)`
  (plus `s.seq == math.MaxInt64` overflow), so a committed transition always advances Seq and Active in
  lockstep from the active-0 base (`initActiveZero` → Seq 0/Active 0).
- Reader: `readSegHead` (`:360`) refuses `head.Active != uint64(head.Seq)`; `verifySegHeadBoundary`
  (`:402`) requires the boundary record's `Seq`/`Active`/`BaseRoot` to equal the head's; `scanSegTail`
  (`:441-442`) requires each tail record to satisfy `rec.Seq == last.seq+1 && rec.Active == uint64(rec.Seq)`
  and chain-validate, else the tail is torn and refused.

So a skipped predecessor (Seq gap) or an `Active != Seq` record is rejected at commit time AND on
recovery — the reader cannot silently adopt a non-dense chain. The new test rejecting a skipped
predecessor is consistent with `scanSegTail`'s `rec.Seq != last.seq+1` refusal. Active-0's empty BaseRoot
is the sanctioned special case (`validSegBaseRoot` gates it; non-zero active requires 32-byte hex),
consistent with `commitTransition`'s `hexToRadixHash(baseRoot)` requirement for rotations.

## F3 — `doRotate` overflow/cancellation/ownership-before-commit is correct.

Current `doRotate` (`delivery_lease.go:1402-1438`) orders: `ctx.Err()` (`:1403`) → `ownedByFile()`
(`:1406`) → archive outgoing window (`reconcileGenerations`, `:1411`) → require non-zero root (`:1414`)
→ **segment-overflow guard `j.segment == math.MaxUint64`** (`:1418`) → stage fresh segment (`:1423`) →
**re-check `ctx.Err()` (`:1426`) and `ownedByFile()` (`:1429`) immediately before the durable commit** →
`commitTransition` (`:1433`) → `switchToSegment` (`:1437`). This is correct: the transition is committed
only after its fresh target exists durably and after the outgoing window is archived (so a crash after
commit finds a base root that is a superset of the segment's leases/acks), and the re-checked
cancellation/ownership narrows the window in which a cancelled context or a lost lock still commits a
transition. The residual (ownership can still change between the `:1429` check and the `Sync` inside
`commitTransition`) is inherent to any check-then-syscall and is caught by the crash-authority pattern on
reopen — not a defect. The comment "The fsynced transition log commits the switch; the atomic head
checkpoints it" now correctly matches `appendTransition` (Write→Sync = commit, then `writeHead`).

## F4 — `switchToSegment` now propagates handle-Close errors before the reset. Correct.

Current `switchToSegment` (`:1464-1492`) handles each of `writer`, `ackWriter`, `seal`, `ackSeal` by
capturing the handle into a local, niling the field FIRST (preserving the drop-before-Close /
no-double-close invariant the close path documents), then `if err := …Close(); err != nil { return
deliveryJournalError() }`. A Close failure therefore returns before the in-memory reset and repoint, and
`rotate` records it as the handle's fault (`:1389-1391`), forcing a reopen. This is consistent: the
transition was already committed durably by `commitTransition`, so a reopen adopts the new active
segment from the committed head; the discarded in-memory maps would be rebuilt on reopen regardless, and
per-append `Sync` means an fd-Close failure loses no committed data. Conservative and correct.

## Qualified source-review handoff

Within the source I inspected, the capacity producer's rotation/segment/generation authority is
correct: crash-commit + head-loss + torn-tail authority, dense `active==seq` chaining (writer and
reader), archived-ACK/lease exact-join self-enforcement (G1 fixed), total-log pre-append bounds (G2
fixed), pre-`Lstat`/`SameFile` confined opens for logs and pages (G3 fixed/retracted), pinned exact-format
fresh staging with conflict-preservation, a sound rotation barrier (owner mutex untaken by the
rotation path; `ownedByFile` lock-free), overflow guards, and Close-error propagation on switch. The
narrowed residuals are two non-blocking, safe-direction items (the dynamic-TOCTOU append window,
closable with `O_NONBLOCK`; and — now resolved — `segmentIsFreshEmpty` pinning).

**Explicitly out of my scope / not certified here, owned by main or other seats:** the new tests
(`delivery_path_v6_test`, `delivery_segment_test`, `delivery_generation_*_test`, the live-70 rollover
test — I did not run them), the store-GC cross-reader (`gc_delivery_segments.go`), the offline reader,
and packaging/rollback. I make no rotation rollback-readiness or enable-by-default claim; the
`enableDeliveryGenerations` seam remains default-OFF and any new shared-contract decision (e.g. adding
`O_NONBLOCK`, or the ACK/terminal cross-store contract) is main's to make. This is a read-only source
review, not a V6/capacity acceptance sign-off. All prior review text and failure evidence above is
preserved; nothing was erased.

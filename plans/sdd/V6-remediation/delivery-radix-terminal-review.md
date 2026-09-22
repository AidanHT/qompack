# V6 remediation — delivery radix + terminal primitives: independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source review only — no source/test edits, no tests/builds (main is running focused
tests; I ran nothing), no git/config, no signoff. Only output: this file. I did not touch the
observation-sidecar or ordering authors' files. No rollout/capacity guarantee is endorsed; the
terminal is not yet wired into the child-owned ordering/drain, so only the PRIMITIVES are reviewed,
not their integration. Crash/concurrency/platform properties below are SOURCE reasoning, not proven —
they need the failure/rollover suite and real crash injection under Windows/Linux `os.Root`.

**Read:** `internal/daemon/delivery_radix.go` + `_test.go`, `delivery_terminal.go` + `_test.go`,
`delivery-terminal-decision.md`, `delivery-rollover-main-adjudication.md`, and the integration/lock
surfaces in `delivery_lease.go` (`openDeliveryJournal`→`loadTerminalDispositions`, `enter`/`inflight`,
`appendLeases`, `closeLocked`, `usable`/`poison`, `owner.mu`/`j.st`).

---

## 1. Verdict

Both primitives are high quality and largely sound; their tests are genuine and non-vacuous (not
"compile ⇒ zero errors"). Two items must be resolved before/with wiring — one is a **wiring lock
constraint** on `retireDenied`, one is a **fail-closed availability posture** (an orphaned terminal
bricks journal open) that main should confirm. The rest are minor. Neither primitive repeats the
mistaken "one Write is power-loss atomic" claim — both use temp/stage + atomic rename + directory
fsync, and the radix adds content-addressing so a torn page is never misread (per the main
adjudication: one write only prevents interleaving under its mutex).

---

## 2. Radix (`delivery_radix.go`) — sound; one coverage note

**Confirmed correct:**
- **Absence vs unavailability.** A complete valid traversal returns `(nil,false,nil)` only for a real
  absence (empty tree; leaf sharing the prefix but differing in the full hash, `:188`; divergence
  inside a branch skip, `:195`). Every missing/corrupt/over-size/unknown-version page under a non-zero
  root, and a zero child pointer, is `errRadixUnavailable` — never a false absence (`:171-213`,
  `readPage :459-481`, `decodeRadixPage`).
- **Placement, not just parse.** A leaf is trusted only after `bitsEqual(node.keyHash,target,0,depth)`
  (`:179`) — the leaf's hash must agree with the path bits already committed — before the full-hash
  and original-key compare. Sound placement proof under a trusted, content-addressed root.
- **Collision.** Full-hash match with a differing original key is `errRadixCollision` on both lookup
  (`:186`) and insert (`:253`), never an overwrite, never an absence. The original key is stored and
  compared; SHA-256 collision resistance is the (correctly unstated-as-testable) backstop assumption.
- **Bounds.** Key ≤4096, value ≤64 KiB, page ≤ key+value+128; exact-consumption decode (no trailing
  bytes for a leaf, exact size for a branch); lookup ≤256+1 steps; insert recursion bounded by hash
  width. No unbounded read/alloc/traversal.
- **Immutable page IO.** Content-addressed name; existing page re-read+verified+fsync'd and refused if
  bytes disagree (`:361-376`), never overwritten; new page staged `O_EXCL` → write → `f.Sync` →
  rename → shard `SyncDir` (`:400-424, 394`); new-shard `SyncDir(dir)` first (`:356`). Concurrent
  identical-page landing accepted after verify (`:384-392`) — handles the Windows rename-over race.
  `syncFile` opens `O_RDWR` for Windows FlushFileBuffers.
- **Symlink confinement.** All IO through `os.OpenRoot(dir)`; `IsRegular` checks on read/write; a
  swapped shard/page link cannot redirect outside the root, and content-addressing neutralises an
  in-root redirect (the read verifies the hash). This rests on `os.Root` semantics — a Go-version/
  platform gate to keep in mind, not to re-derive here.
- **Crash before/after root commit.** `insert` fsyncs every path-copy page before returning the new
  root; the parent commits that root separately. A crash before the parent commits leaves the new
  pages as orphans and the prior root intact (`TestDeliveryRadix_CrashOrphanLeavesOldGenerationsUsable`
  confirms an orphan is left in place and old roots still read exactly). Sound w.r.t. the parent's
  atomic root commit — which is NOT in this file and is unreviewed.

**Tests** exercise the hard cases honestly: empty-tree absence, >64 generations each answering its own
keys and reporting future keys ABSENT (not unavailable), immutability, reopen, collision refusal,
wrong-root/corrupt-page unavailability, missing-descendant path-locality, corrupt-sibling isolation,
200-bit skip compression + absence inside the skip, cancellation, crash orphan, input bounds, and a
direct malformed-page decoder table with a positive control.

**Minor (not a blocker):** the trickiest path — `insertAt`'s mid-skip re-base (`:293-304`,
`packBits(node.skipBits,(p+1)-depth,newSkipLen)`) — has no TARGETED test; it is exercised only
incidentally by `ManyGenerations` (whose exhaustive per-generation verification would fail if the
re-base were wrong, so coverage effectively exists but is non-obvious). A targeted 3-key test that
inserts a key diverging INSIDE an existing compressed skip would harden the most intricate logic.

## 3. Terminal (`delivery_terminal.go`) — sound primitive; two items for wiring/main

**Confirmed correct:**
- **Lease binding.** A terminal is valid only when its on-disk bytes equal the canonical
  `terminalFor(record.Lease)` AND the filename equals the lease's `ObservationID` hash (`readTerminal
  :100-105`), and `loadTerminalDispositions` refuses open unless `j.leases[delivery] == record.Lease`
  exactly (`:140`). Full session/nonce/request-hash/arrival/observation binding; a
  path-traversal `ObservationID` (`"../outside"`) is refused by `terminalFileName`
  (`TestDeliveryTerminal_RejectsUnknownOrChangedLease`).
- **No false capture ACK.** The terminal lives in a separate dir/format and is loaded into `j.terminal`,
  never `j.acks`; `acknowledged()` reads only the ack journal. A retired-denied lease is
  `acknowledged()==false` (test-pinned). Airtight: retirement cannot fabricate a capture ACK, and
  `retireDenied` mints no lease/observation.
- **Restart/missing/corrupt/staging.** Missing dir → pending (`:113`); a non-canonical name (a
  `WriteAtomic` staging temp) is skipped and preserved (`:136`,
  `TestDeliveryTerminal_StagingArtifactIsNotACompletion`); corrupt/conflicting evidence refuses open
  and preserves the bytes (`TestDeliveryTerminal_MalformedEvidenceIsPreservedAndUnavailable`).
- **Durability / write-before-offset.** `retireDenied` does `WriteAtomic` (temp+fsync+rename+dir
  fsync) → `SyncDir(state)` → re-`readTerminal` verify → poison on any failure, all before returning
  nil. The PRIMITIVE fulfils "durable before return"; the offset-advance ordering is the ordering/drain
  wiring's responsibility and is unreviewed (pending handoff).
- **Ownership.** `retireDenied` holds `j.owner.mu` across the write and checks `owned()`+`usable()`,
  so `Release` (which needs `owner.mu`) cannot overtake the disposition, and `closing` cannot be set
  mid-write. Lock order `owner.mu → j.st` matches `Release`/`closeLocked` (`owner.mu → st`); no
  inversion with the lease/ack batches, which use `enter()`+`j.st` and never take `owner.mu`.

**Item A — wiring constraint (must be honored at handoff).** `retireDenied` self-acquires
`j.owner.mu` (`:166`) and does NOT enter the `inflight` barrier. Therefore the pending ordering/drain
wiring **must not call `retireDenied` while already holding `owner.mu`** (non-reentrant → deadlock),
and should understand that a disposition write runs concurrently with in-flight lease/ack batches
(safe as written — it touches only the terminal file + `j.terminal`, and reads `j.leases` under
`j.st`). This is the exact "then main wires terminal lookup/retirement" seam the decision defers; flag
it so the wiring does not deadlock or assume batch-exclusion.

**Item B — orphaned terminal bricks open (confirm posture with main).** If the lease journal loses a
lease (truncation) whose terminal file survives, `loadTerminalDispositions` finds `j.leases[delivery]
!= record.Lease` and **refuses the whole journal open** (`:140-141`) with no recovery path. This is
consistent with the journal's fail-closed model (a torn seal also refuses), but a single
orphaned/conflicting terminal makes the daemon unopenable. The decision's "the capacity migration must
fold/resolve terminal dispositions alongside leases and ACKs" implies a resolution path is owed; main
should confirm this is the intended posture (vs. quarantining the orphan) and that recovery/fsck can
clear it.

**Minor (not blockers):**
- `retireDenied` holds the singleton `owner.mu` across `WriteAtomic`+`SyncDir`+re-verify — a coarse
  lock across fsync. Acceptable for a rare replay-denial path; note it serializes the lock during the
  write.
- `retireDenied` writes via the ABSOLUTE `path` (`:207-208`) but verifies via the confined `os.Root`
  (`:214`); a symlink divergence is caught by the re-verify (→ poison), fail-closed — but writing
  through the confined root would remove the asymmetry.

## 4. Misleading-assertion check

The radix and terminal tests are genuine load-bearing assertions with positive controls
(e.g. `DecodeRejectsMalformedPages` proves the good page still decodes so the rejections are not
vacuous; `ManyGenerations` verifies both presence AND future-key absence per generation). I found no
"compiles ⇒ zero errors" style claim in these test files. **What I cannot certify:** I ran nothing, and
a passing focused unit test does not establish the crash/durability/concurrency properties, which
depend on `os.Root`/fsync/rename behavior per platform and on the (unwritten) parent integration —
those need the failure/rollover suite under real crash injection. Preserve that uncertainty.

## 5. Non-acceptance / scope

Source review of the two new primitives only; no clearance, no capacity/rollout guarantee, no
signoff. The radix is a sound bounded membership/absence primitive and the terminal a sound
payload-free denial-disposition primitive that cannot fabricate a capture ACK; both use
atomic-rename + dir-fsync durability (no single-Write-atomic claim). Before wiring: honor Item A
(retireDenied lock reentrancy) and resolve Item B (orphaned-terminal open refusal) with main. Whether
the radix actually closes the journal's capacity bound (F5) depends on the parent integration — the
root commit, the lease/ACK/arrival fold, and the rollover transaction — which is not in these files
and is not reviewed here.

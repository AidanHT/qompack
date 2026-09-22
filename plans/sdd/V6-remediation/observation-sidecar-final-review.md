# V6 remediation — observation sidecar (index/observations.jsonl): final independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source review only — no source/test edits, no tests/builds (main is running the parent
observer tests; I ran nothing), no config/permission/auth/provider/billing/Git. Only output: this
file. Crash/durability/concurrency claims below are SOURCE reasoning; a focused unit PASS does not
prove behaviour under real power-loss (only simulated cut points). Prior review artifacts untouched.

**Read:** `internal/store/observation_publication.go`, `observation_lookup.go`,
`tooluseindex.go` (RecordToolUse/Superseding + `recordSupersedingCore`/`completeSupersedeMarks`),
`fsstore.go` (obs fields, `obsPubMu`, load order), `publication_sync.go`, and the observer
integration (`identity.go`, `tooluse.go`, `stop.go`, `prompt_delivery.go`, `supersede.go`); audited
`observation-binding-work.md` Part C against source and `observation-binding-decision.md` / arch §0.2.

---

## 1. Verdict

The implementation is careful and, against the decision, largely sound: it keeps §0.2's frozen wire
(exported + private `tuRec`), binds via an intent-first versioned sidecar, PRESERVES the atomic
record+marks write, recovers a genuine partial batch with later-supersession preserved, and never
claims one Write is power-loss atomic. Part C is honest and self-critical. **Two findings warrant
main's attention** — one is a real bounds gap (F1), one a liveness stickiness beyond what Part C
disclosed (F2). Two lower items (F3/F4) and confirmations follow. No signoff; the store-side binding
is reviewed, not the parent observer test (which Part C correctly does not claim passes).

---

## 2. Confirmed correct (verified in source)

- **Frozen wire preserved / immutable binding.** The intent carries the private `tuRec`
  (`observation_publication.go:80-96`); the exported and private tool_use lines are untouched. A
  reservation binds the id to `reservedIdentity{Root,Session,Tool}` (`:60-68,284-288,355-360`); a
  foreign writer cannot rebind (`recordToolUseCore:250`, `recordSupersedingCore:371`).
- **Atomic record+marks write is NOT split** (the risk I flagged in the prompt follow-up).
  `recordSupersedingCore` (`tooluseindex.go:345-426`) is the original SP08-D2 single-`tuW.write`
  batch, unchanged; `publishObservation` routes *around* it, writing the fsynced intent BEFORE and
  reconciling AFTER. So "record present, marks missing" only arises from a torn single write, and
  `completeSupersedeMarks` (`:432-`) completes only the still-`StatusOK` missing marks, skipping a
  target superseded by a LATER record — later supersession preserved (Part C defect #2 fix, real).
- **id-only-after-commit is safe (F3 dependency, not a bug).** `obsCommitted[obs]` stores only the
  id, but commit is gated on `observationPublishedLocked` which re-checks the reserved Root both at
  runtime (`:585`) and on reopen (`:322-333`), and `obsReserved` guards the id — sound *because* the
  tool_use index is append-only (same id/different Root → `ErrAppendOnly`). Confirm no future path
  rewrites a record's Root.
- **Duplicate / conflicting intents, incl. committed conflicts.** A second intent naming a different
  record for a committed/pending obs → `obsAmbiguous` (`:276-282`); an id under different metadata →
  ambiguous (`:284`). Ambiguity is checked FIRST in both `ToolUseByObservation` (`observation_lookup.go:45`)
  and `RecoverToolUseByObservation` (`:630`), so a committed-then-conflicted obs reports degraded, not
  the stale record. Idempotent re-reservation writes nothing (`sameIntent`, `:382-385,456-457`).
- **Missing target vs satisfied.** `observationPublishedLocked` blocks only on a target that is
  present AND `StatusOK` (`:327-331`); an absent or already-superseded target is "done" — correct (a
  no-op supersede cannot block completion). The complete/filter paths agree (`:551-559`, `:438-446`).
- **Failed sync → restart durability.** A torn/partial intent on reload fails `decodeObservationLine`
  (byte-canonical round-trip, known version) → uncertain + `markObservationUnavailable(probe)` →
  degraded, never false absence; `probeObservation` attributes a truncated tail. `SyncPublication`
  runs before AND after the append; a missing original object keeps the intent incomplete rather than
  fabricating a publication (`:542-544,579-581`, Part C #3).
- **Concurrency / mutex.** `obsPubMu` serialises all reserve/publish; `s.mu` guards the maps;
  `completeIntentLocked` never holds `s.mu` across `recordSupersedingCore` (it RLock/Locks in
  segments), so no reentrant `s.mu`; lock order is consistently `obsPubMu → s.mu`. `loadObservationsLocked`
  runs AFTER `loadToolUse` (`tooluseindex.go:181`), which `observationPublishedLocked` requires.
- **No single-Write-atomic claim** (`observation_publication.go:22-24` cites main's adjudication).

---

## 3. Findings

### F1 — total scan bounds do NOT bound open-time I/O (medium; "bounds not merely constants")
`loadObservationsLocked` returns `false` when `totalBytes/totalEntries` exceed the caps
(`observation_publication.go:156-159`), but `scanIndexJSONL` (`tooluseindex.go:46-55`) treats a
`false` return as *one bad line* (`bad++`) and **does not break** — it reads the file to EOF
regardless. So `maxObservationTotalBytes` (1 GiB) / `maxObservationTotalEntries` (1<<20) are
*processing* caps (they set `uncertain` and stop *applying* lines), not *work* caps: a 10 GB or
100M-line `observations.jsonl` is fully read at every open. The per-line bound + scanner buffer bound
each line, so this is fail-safe for correctness (uncertain, never false absence) but the "total scan
bounds" the comment (`:33-38`) advertises are not effective. **Implication:** unbounded open-time I/O
on a large/hostile sidecar; the capacity story ("bounded memory and each individual operation") is
not met for the load. **Fix direction:** give `scanIndexJSONL` a real stop signal (break on a
sentinel) or document these as apply-caps and add a separate size/entry ceiling that aborts the load.
**Reproducer (suggestion, do not run here):** write N > `maxObservationTotalEntries` valid intent
lines and assert every line is read (e.g. a counting fn wrapper) — showing the scan does not stop at
the cap.

### F2 — `obsSidecarUncertain` is sticky for the process; a transient runtime I/O error stalls ALL observation publication until restart (medium liveness; beyond Part C's disclosure)
The flag is set in 8 places and **never cleared at runtime** (only re-derived by
`loadObservationsLocked` at open). `reserveConflictLocked` refuses EVERY new observation while it is
set (`:393-395`, `ErrDegraded`). A runtime `appendObservationIntent` write/sync/close/dir-sync failure
sets it (`:430-445`). Because the observer routes every leased tool/stop/prompt capture through
`RecordToolUse(rec.Observation!="")` → `publishObservation` → (uncertain) `ErrDegraded`, and the
observer maps that to `o.unpublished(...)` = `ErrUnpublished` (`tooluse.go:177`, `stop.go:293`,
`prompt_delivery.go:164-166`), the daemon leaves the delivery pending and **re-delivers forever** —
so a single *transient* fsync blip (e.g. a momentary ENOSPC/EIO that then clears) converts into a
process-lifetime refusal of all new observation publications and a leased-capture retry-stall until
daemon restart. Part C discloses the *torn-tail-on-load* subset ("blocks new reservations … until
fsck") but NOT this runtime-transient stickiness, and no sidecar fsck-repair exists to clear it.
**Implication:** liveness cliff — one transient error disables L0 publication for the running daemon.
**Fix direction (main's contract choice):** clear `obsSidecarUncertain` on a subsequent successful
full load/verify, or scope the poison to the specific failed intent rather than the whole sidecar,
or confirm the fail-closed-until-restart posture and provide the recovery. **Reproducer
(suggestion):** reserve one intent with an injected `SyncData` failure → flag set; then reserve a
DIFFERENT valid obs on a now-healthy disk → still `ErrDegraded`, proving the flag never clears (no
fault seam exists today, so this is reasoned from source: grep shows no runtime clear).

### F3 — id-only committed map (low; confirmation, not a defect)
See §2. Safe under append-only + `obsReserved` + reopen re-derivation; flag only so a future
record-rewrite path cannot silently drift a binding.

### F4 — sidecar path is Lstat-checked but not os.Root-confined (low; TOCTOU, open-time)
`refuseNonRegularSidecar` (`:403-415`) Lstats then `appendObservationIntent`/`loadObservationsLocked`
open the ABSOLUTE path via `paths.OpenFile`/`scanIndexJSONL`'s `os.Open` (both follow symlinks). A
symlink swapped between the Lstat and the open would be followed. Unlike `delivery_radix` (os.Root
confined), this sidecar is not. Open-time, within the trusted store dir, so low risk (not a
whole-filesystem hostile-actor claim); note it for parity with the radix confinement model.

---

## 4. Part C audit

Part C is honest and matches source: defects #1–#6 correspond to the code (wiring into the public
path with factored legacy cores; real partial-batch cut + `completeSupersedeMarks`; SyncPublication
before/after; `obsSidecarUncertain` + strict canonical decode + `core.NewObservationID` ids;
`reservedIdentity` rebind refusal; non-regular refusal). It correctly does NOT claim the parent
observer test passes, and its "durable-contract choices for main" already surface the torn-tail
conservatism and the target-level "must be our mark" question. **What it under-states:** the F2
runtime-transient stickiness (framed only as torn-tail/fsck) and the F1 total-bound ineffectiveness
(claimed as "total scan bytes and entries are bounded"). I did not run `obs-sidecar-v2-*`; a focused
PASS + clean `gofmt`/`vet` is not proof of the crash-cut behaviour under real power-loss, and "the
swept set equals the mutate-guarded set" (readonly classification) I did not verify.

## 5. Non-acceptance

Source review of the store-side sidecar only; no signoff, no all-fixed claim. The design honours §0.2
and the decision, preserves the atomic write, and recovers a real partial batch with later
supersession intact. F1 (open-time I/O not bounded by the total caps) and F2 (uncertain flag sticky →
process-lifetime publication stall after a transient error) are the two items for main to correct or
consciously accept; F3/F4 are low. Per the terminal review, Item A is honored and Item B stands
(orphan/conflict remains unavailable with preserved evidence, restore only from verified backup until
an explicit recovery procedure) — the same fail-closed-until-repaired posture is what F2 extends to
the observation sidecar and should be ruled consistently. Uncertainty about untested code and
platform fsync/symlink gates is preserved; I ran nothing.

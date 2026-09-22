# V6 remediation — assembled budget + backup pre-copy frontier: independent review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-22.
**Nature:** read-only source/test review — no source/test/golden/report edits or deletions, no
tests/builds/benchmarks (authors are working; no perf samples spent), no Git/config/permission
changes. Only output: this file. This is NOT final V6 / packaged-restore / rollback sign-off. The
daemon/store-GC capacity paths are changing under other seats and are NOT certified here. Findings
carry exact file:line and consequence; I ran nothing.

**Read:** `internal/rehydrate/budget.go`, `render.go`, `build.go` (trim loop), `assembled-budget-work.md`;
`internal/store/backup.go` (`refuseIfTheProjectMoved`, TakeBackup), `backup_segments.go`,
`backup-frontier-resolution.md`, and the retained negative-control references.

---

## 1. Assembled budget (rehydrate) — no release blocker within reachable inputs

The V6 §5 / inventory 1.6.18 requirement (assembled estimate, cap remeasure after each eviction,
explicit overflow, unchanged current-intent priorities, shares sum with no additive/provider claim) is
met, and the arithmetic is sound:

- **Assembled estimate, not fragment sum.** `render` sets `res.Tokens = estimate(d, res.Text)` over the
  complete wrapped payload (`render.go:240`); the per-fragment `a.used` values survive only as
  non-negative weights (`:219-225`). Correct fix for the root cause the work report names.
- **Σ Items == Tokens is lossless.** `allocateAssembledTokens` (`render.go:257-287`) clamps any
  negative row to 0, charges a positive diff to the first item, and deducts a negative diff from the
  tail bounded by each row's own value. Checked: with `target ≥ 0`, `deficit = sum-target ≤ sum`, so
  the backward loop always fully absorbs the deficit → `Σ Items == target` exactly, and no row goes
  negative. A negative `target` is clamped to 0 (all rows → 0), still lossless. No overflow in the
  int domain for supported estimators.
- **Cap remeasure after each eviction.** The hard-cap loop (`build.go:258-285`) re-renders, then
  `res.Tokens = estimate(d, res.Text)` and re-runs `allocateAssembledTokens` + `syncStatTokens` after
  EACH eviction (not decrement-by-share). Terminates: it removes exactly one item per iteration
  (`res.Items` shrinks), so it converges on item COUNT regardless of a non-monotonic/rounding
  estimator, ending at `Tokens ≤ budget` or empty (`:286-289` zeroes an empty payload). No infinite
  loop.
- **Explicit overflow, priorities intact.** A whole-section eviction appends `evictionDrop(gone.Kind)`
  (ID `"evicted"`, `budget.go:285-292`), and `Overflowed` recognizes it alongside the wrapper-alone and
  tier-1 shapes (`:306-313`). `evictIndex` (`:208-215`) still drops the last non-tier-1 item first, so
  current-intent/tier-1 priorities are unchanged.
- **Negative-estimator honesty.** `allocateAssembledTokens`'s negative-`target` clamp is arithmetic
  safety only; the work report §6 correctly flags a genuinely negative assembled estimate as OUTSIDE
  the supported set (both shipped estimators are non-negative), returned to main, not claimed handled.
  I confirm no supported/reachable input reaches it.
- **Tests are meaningful (not vacuous).** `TestBuild_V6_ResultTokensIsTheAssembledEstimate`'s
  `got.Tokens == estimator.Estimate(got.Text)` is a real regression guard (the ORIGINAL running-sum
  code violated it — `assembled-budget-red-1` FAILED, `green-1` PASSED), and the paired
  `Σ Items == total` catches an allocation drift the identity alone would not; the
  separator-charging estimator forces the post-assembly trim path deterministically. (Red/green are
  the author's runs; I did not re-run.)

**Observation for main (reachable, arguably correct — not a blocker):** the hard-cap loop CAN evict
TIER-1 sections whole when only tier-1 remain (`evictIndex` returns `len-1` for an all-tier-1 set,
`budget.go:209-214`), so a budget where the wrapper fits (`overhead ≤ budget`, past the early guard at
`build.go:83`) but tier-1 content alone exceeds it yields an EMPTY, `Degraded` payload with an
`evictionDrop` per evicted section (e.g., a checkpoint with large invariants under a modest named
budget). This satisfies the absolute-hard-cap contract (`Result.Tokens ≤ budget`) and is a named
overflow, not silent — but it is a degradation shape (empty + overflow drops rather than partial
tier-1) worth main confirming as intended for §8.6. Reachable via `Request.Budget` between `overhead`
and tier-1 size; policy, not a bug, so I do not file it as a blocker or propose a change.

---

## 2. Backup pre-copy frontier (e950486) — correction acknowledged; no new blocker within reachable inputs

**I acknowledge my earlier `segment-authority-independent-review.md` §4 claim was WRONG.** I asserted
that comparing each captured file to its post-walk re-read guarantees consistency regardless of
`WalkDir` order. It does not: a watched file changed IMMEDIATELY BEFORE its own copy is captured in the
new state while earlier-copied files still belong to the prior generation, and a post-copy-only read
then matches the captured bytes → false success. Main reproduced this
(`TestBackup_RefusesFrontierChangedBeforeItsOwnCopy` / `backup-precopy-frontier-negative-control`) and
the 3-way pre/captured/post comparison is the right fix. My prior post-walk-only reasoning is
withdrawn.

Independent inspection of the fixed reader:

- **3-way invariant closes the hole.** `refuseIfTheProjectMoved` (`backup.go:375-428`) now takes a
  pre-copy `before` snapshot and requires `existed == copied` and `initial == captured`
  (`:404-407`) AND captured == post-copy (`:408-425`). A file changed before its own copy now trips
  `initial != f` → `ErrBackupMoved`. This ALSO closes the separate "new segment appeared under the
  walk" hole I raised earlier: a segment copied but absent from `before` trips `existed != copied`
  (`:405`).
- **Pre-copy snapshot precedes the first copy.** `beforeWriters, err := m.snapshotBackupWriters(ctx)`
  is at `backup.go:243`, BEFORE the `filepath.WalkDir` at `:258`. Ordering correct.
- **Bounded, halting segment discovery.** `backupSegmentNames` (`backup_segments.go:49-122`) Lstats the
  segments dir (missing → `nil,nil`; not-a-dir / symlink / escapes-root via `maintNoFollow` →
  `ErrBackupMoved`), confines through `os.OpenRoot` + `SameFile`, reads in bounded `ReadDir(64)`
  batches with a `maintMaxManifestFiles` count cap, rejects symlink entries and unexpected nesting
  (`:92-99`), and a `ReadDir`/open error RETURNS (`:113-114`, `snapshotBackupWriters:34-35,21`) —
  i.e. an unreadable segment directory HALTS the backup (`ErrBackupMoved`), never a silent empty watch
  set. This is the right posture and matches the decision's "no unreadable glob becomes an empty set."
- **Cancellation + bounded streaming.** `ctx.Err()` is checked in the watch loop (`:398-400,420-422`)
  and the enumeration (`:86-88`, `snapshotBackupWriters:26-27`); digests stream via `backupFileDigest`
  through the Windows shared-delete reader with opened-identity validation. No unbounded read.
- **Maintenance lease contract unchanged.** This change is the consistency BELT for a racing daemon;
  the maintenance CLI still requires the daemon writer lease (recovery-work contract), and
  `refuseIfTheProjectMoved` does not alter it. The resolution correctly disclaims that it is not a lock
  against a live writer beyond the monotonic protocol.
- **Immutable-page assumption is explicit, not silent.** Content-addressed pages are deliberately not
  watched (name == hash); the resolution §33 disclaims immutable-page closure. A same-name/wrong-bytes
  page corruption is caught only at read-time hash verify, not by backup — disclosed, acceptable.

**Limitations I confirm are correctly disclaimed (not new findings):** ABA edits (relies on the
journal's monotonic writer protocol — a seal seq only advances, journals only append), immutable-page
closure, old-reader compatibility, and final packaged/rollback certification remain outstanding per
`backup-frontier-resolution.md` §33-36. No release-blocking bug is reachable in the fixed reader within
the inputs I can construct; a minor safe-direction note: `initial != f` compares whole `BackupFile`
structs including `Name`, so any path-form skew between the `before` snapshot and the `WalkDir` rel
would cause a false REFUSE (conservative), never a false pass — both sides use `ToSlash` + `state/`, so
this is not currently reachable.

---

## 3. Non-acceptance

Read-only review; no V6 sign-off. Both finished changes are sound within the reachable inputs I
inspected: the assembled-budget arithmetic/overflow/eviction is correct and its tests are meaningful
(one reachable degradation shape — whole tier-1 eviction to empty under a mid-size budget — returned to
main as a policy confirmation, not a bug); the backup 3-way frontier fix correctly resolves my earlier
false post-walk-only claim and the new-segment hole, with bounded halting enumeration and cancellation.
No source authored, nothing deleted, no perf claim, no artifact from a repro (none was needed — I found
no concrete unresolved bug requiring one). The actively-changing daemon capacity and store-GC paths are
explicitly not certified here; main routes their critical review after stabilization.

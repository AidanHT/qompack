# rc1 daemon-fixture corrections — independent non-authoring review

- **Reviewer role:** independent critical review, non-authoring. No product/test edits, commits, delegation, or config/auth/provider changes. This report is the only file written. `qompack-v6-rc1` whole-tree run and its immutable `65bc8d7` tree were not touched.
- **Route probe:** canonical model `claude-opus-4-8` already verified in `route-closeout-review-result.json` and `closeout-review-result.json`.
- **Question posed:** do Main's corrections to the two daemon test files launder assertions or hide a real regression behind fixture changes? Are the old→new mappings proper? Any missing proof of cut / unprotected state?
- **Scope (bounded):** `internal/daemon/delivery_migration_guard_test.go`, `internal/daemon/delivery_publication_test.go`, and their source dependencies (`delivery_lease.go`, `delivery_migration_guard.go`, `delivery_generation.go`, `delivery_segment.go`, `delivery_publication_test.go` helpers). No tests were run by this review.
- **Date:** 2026-09-22.

## Verdict

Both corrections are **proper responses to real, pre-existing product behavior**, not assertion laundering and not a regression papered over. Each old assertion failed on the immutable `65bc8d7` tree because the **product** changed (a seam-off head-loss refusal contract; a drain ordering that reads the journal after leasing but before dispatch). The diffs touch **tests only** — the product recovery path is unchanged — so the "do not alter product recovery to make tests pass" constraint is satisfied. Two minor weaknesses are noted (F-A, F-B); neither blocks the corrections. No final V6 signoff is asserted.

## Identity qualification

| identity | value |
|---|---|
| source HEAD (verify/v6) | `65bc8d7707fed78e1b7344c2d23d5fa948a0a9c1` |
| whole-tree run | `qompack-v6-rc1`, immutable `65bc8d7` — **not touched by this review** |
| test file: delivery_migration_guard_test.go | git blob `e09ed28ed3cd6a3f`, 79L (M, test-only) |
| test file: delivery_publication_test.go | git blob `9496f06f507baf83`, 630L (M, test-only) |
| product recovery source | **unchanged** — refusal (`delivery_lease.go:227-228`) and drain ordering are pre-rc1 |
| focused run (completed) | `runs/rc1-daemon-fixture-corrections.log` — both corrected tests + siblings PASS (`ok internal/daemon 0.710s`) |
| linux 8-case product-child race (completed) | `runs/rc1-linux-child-race.log` — all 8 PASS; product child `sha256=5b0151bac4771ecb16bd78815465673e4951004f829d3f2f3d20fea3d6d4842d go=go1.26.6` |
| bundles | Windows/Linux built from `65bc8d7`; Windows Claude 2.1.263 strict manifest accepted but **not** an installed-host session |

## Old → new identifier mapping

| # | old identifier | new identifier | change |
|---|---|---|---|
| 1 | `TestDeliveryMigration_PrecommitArtifactsKeepLegacyAnchorUsable` | `TestDeliveryMigration_OrphanArtifactsPreserveLegacyAnchorsWithoutContinuing` | test renamed; expectation inverted `NoError`→`Error`; adds byte-identity preservation of 4 anchors + obsID + dir |
| 2 | `TestCrashCutBetweenReferenceAndFrontierRedelivers` (cut when `j.leases[testDeliveryToken('6')]` present) | same name; cut when `calls() > 0` | cut trigger re-anchored from lease-map membership to reference-callback count |

## Correction 1 — migration guard (inversion is the product's, not the fixture's)

**What the old test asserted:** with intact legacy anchors and an empty orphan `delivery-generations/` dir, `openDeliveryJournal()` succeeds and legacy continuation is allowed (identical lease re-issued).

**Why it failed on 65bc8d7** (`runs/rc1-integrated-whole-tree.log:17-23`, verbatim):
```
--- FAIL: TestDeliveryMigration_PrecommitArtifactsKeepLegacyAnchorUsable (0.19s)
  Received unexpected error: qompack: running in degraded mode: delivery journal
  unavailable; preserve it for recovery
  Messages: uncommitted artifacts cannot invalidate intact legacy anchors
```
The **product** refuses. Traced: seam default is off (`delivery_generation.go:141 enableDeliveryGenerations = false`); `openDeliveryJournal` hits `delivery_lease.go:227-228` — `if !enableDeliveryGenerations && existingDeliveryMigration(stateDir) { return nil, deliveryJournalError() }`. The empty `delivery-generations/` dir satisfies `existingDeliveryMigration` (→ `migrationEvidenceExists`, `delivery_lease.go:409-427`). This is the coordinator-blessed conservative contract: migration evidence surviving without a segment authority is indistinguishable from head loss after new writes, so continuation is refused (`delivery_lease.go:231-235`, `delivery_segment.go:40-46`). The refusal returns `core.ErrDegraded` (`delivery_lease.go:1342-1344`).

**Is the new test a proper mapping? Yes.**
- The fixture is essentially unchanged (same `os.Mkdir(delivery-generations)`); only the expectation follows the product. That is the *opposite* of laundering — a fixture trick would have altered setup to manufacture the error.
- The refusal is genuine product behavior, deliberate and documented in-product, and consistent with the sibling contracts in the same file (`MissingLegacyAnchorsDoNotRecreateIdentity`, `MissingAckAnchorsAreNotRecreated`) which already assert refuse-and-preserve.
- The new test is **stronger** on the safety property that matters: it snapshots all four legacy anchors (`deliveryLeaseFile`, `deliveryPositionFile`, `deliveryAckFile`, `deliveryAckPositionFile`), asserts the lease file carries `lease.ObservationID` before, and after refusal asserts every file is **byte-identical** and the orphan dir still exists (`delivery_migration_guard_test.go:45-62`). The refusal returns before any write (product line 228 precedes all mutating paths), so preservation is real, not incidental.
- State protected: legacy identities/anchors are preserved for recovery; no empty legacy history is minted; no arrival-sequence/observation-identity reuse. The old capability (auto-continue over an orphan) is intentionally removed.

**F-A (LOW, weakness — not laundering):** the new test asserts only `require.Error` and does not pin the refusal *reason*. Every refusal path in `openDeliveryJournal` returns the same `core.ErrDegraded`-wrapped sentinel, so `require.Error` cannot distinguish the intended seam-off/migration-evidence refusal (line 228) from an incidental one (e.g. ownership). Mitigated by: the setup deterministically reaches line 228 (fresh owned lock, `journal==nil`, seam off, evidence present), and the byte-identity preservation assertions bind the safety outcome. A tighter `require.ErrorIs(err, core.ErrDegraded)` would confirm the family but still not disambiguate the path — no per-reason sentinel exists to assert against. Informational only.

**Observation (product scope, INFO):** recovery from a genuine precommit-orphan now requires manual removal of the orphan dir; the daemon no longer auto-continues. This is the coordinator's conservative choice, not a test defect, but it is an operator-facing behavior change worth carrying in release notes.

## Correction 2 — crash cut 2 (boundary re-anchored, all substantive assertions kept)

**What the test proves (unchanged intent, `delivery_publication_test.go:559-561`):** the reference write completed and the process died before the frontier/ack record committed; the spool offset must NOT advance, the delivery must reappear, and a second pass must publish under the SAME identity.

**Why the old trigger failed on 65bc8d7** (`runs/rc1-integrated-whole-tree.log:24-30`, verbatim):
```
--- FAIL: TestCrashCutBetweenReferenceAndFrontierRedelivers (0.21s)
  delivery_publication_test.go:594: Not equal: expected: 1  actual: 0
  Messages: the reference write ran
```
Under the current ordering (journal read after leasing but before dispatch), the old condition `j.leases[testDeliveryToken('6')]` was already true at the journal lookup that precedes the reference callback, so the cut fired with `calls()==0` — before the reference write. That is an *earlier admission failure*, not the reference→frontier boundary; `require.Equal(1, calls())` correctly caught it.

**Is the new mapping proper? Yes.**
- `calls` counts `ObserveTool` invocations — the reference write (`newObservingDaemonWithResult`, one increment per observed tool use). `calls() > 0` gates the cut on the reference callback having actually run, re-establishing the exact boundary regardless of when the lease appears in the in-memory map. It is a *more direct* proxy for "reference write completed" than lease-map membership (which proxied an earlier step).
- Cut lands on the frontier only: the first `journal()` call (lease) runs at `calls()==0` → real journal → lease succeeds; the reference callback runs (`calls→1`); the next `journal()` call (frontier/ack) returns `deliveryJournalError()` → frontier cut. No intervening `journal()` call exists between the reference write and the frontier to mis-cut.
- **Lower boundary proven:** `require.Equal(t, 1, calls(), "the reference write ran")` (line 597) — cut is strictly after the reference write.
- **Upper boundary proven:** frontier uncommitted — `Drain` errors ("uncommitted frontier is not a completed drain", 596); spool file retained (600); offset zero (603); gap `DrainGapUnacknowledged`, `Complete==false` (605-607); on restart the same nonce reclaims the same identity, frontier commits, exactly one sidecar (610-621). All of these are the **old assertions, retained verbatim** — no coverage removed.
- No missing proof of cut: "reference ran" (calls==1) + "not acknowledged" (gap kind + `real.acknowledged` false→true across passes) + "identity reused" (single sidecar) together bind the crash strictly between reference and frontier.

**F-B (INFO):** the new trigger couples the test to the drainer invoking `ObserveTool` before the frontier `journal()` lookup. This is exactly the ordering under test; if a future product reorder moved the reference after the frontier, `calls()` would be 0 at the frontier lookup, the cut would not fire, the frontier would commit, and the test would fail loudly (redelivery/gap assertions) rather than pass silently. Self-protecting; acceptable.

## Prior-review status carried forward

- **F2 (combined-case coverage) — RESOLVED.** `runs/rc1-linux-child-race.log` runs all eight selected product-child tests against the current candidate under `-race`, all PASS, with the buildinfo product SHA `5b0151bac477…` (go1.26.6). This closes the F2 gap from `closeout-runtime-independent-review.md`.
- **F1 (hosted-runner confirmation) — STILL OPEN.** The green race evidence remains local Docker/WSL; the hosted `ubuntu-latest` `race-product-child` nightly job is still unobserved. Do not report that gate green on this basis (see `hosted-runner-fsync-tail`).

## Explicitly NOT asserted

No final V6 signoff. Human UAT, platform certification (installed-host session — the Windows Claude 2.1.263 acceptance is a strict-manifest check, not an installed-host run), eval, and rollback gates remain out of scope and unverified. Rollover stays default-off (`enableDeliveryGenerations = false`); nothing here accepts it. This review ran no tests; all findings derive from the diffs, the product source, and the committed rc1 run logs.

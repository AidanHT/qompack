# V6 remediation continuation — not release sign-off

## Coordinator update — 2026-09-22

Current source candidate is `65bc8d7707fed78e1b7344c2d23d5fa948a0a9c1`.
It is checked out clean and detached in sibling `qompack-v6-rc1`; primary
integration remains `qompack-v6` / `verify/v6`. The original dirty `qompack`
worktree and completed reports remain untouched. No release/tag/push/publish or
merge to develop has occurred. Human UAT has not been executed.

The integrated whole-tree gate is running against that candidate using
`go run ./tools/devtool test`, GOMAXPROCS=2, CGO_ENABLED=0. This one broad run is
justified by cumulative daemon, store, CLI, privacy, recovery and packaging
changes. Its evidence is `runs/rc1-integrated-whole-tree.*`, with per-package
artifacts under `rc1-whole-tree-artifacts/`. A running command is not a pass.
The original carried-defect sign-off guard is retained, including unresolved
capacity/performance rows. Rollover remains default-off; SP20-D4 is not fixed.

Accepted corrections and focused evidence:

- `57097f6` corrects the generated plugin homepage; the historical manifest
  golden remains unchanged under an explicit one-field assertion mapping.
- `4cda64d`, `7086bb5`, `891582e`, and `36205d1` add bounded immutable delivery
  identity, segmented GC retention and offline journal checks. Actual producer
  backup/restore caught and fixed an archived-ACK restart join. Seventy-rotation
  identity/retention and Linux focused race cases pass. Old-reader/rollback and
  resource-cost gates remain open before production enablement.
- GC rejects explicit empty/null/noncanonical/zero observation IDs as settlement
  authority. An isolated deliberately neutered source copy failed all five
  negative-control cases; the misleading earlier passing invocation was not a
  valid negative control. Supported seal-format validation replaced acceptance
  of arbitrary nonempty files.
- `5c8329e` fixes Linux lock recovery for exited, unreaped owners while preserving
  live-lock/IPC safety. `108bd63` fixes the MCP harness stderr race and Linux
  cleanup liveness. `8948992` adds an actual product-child race CI lane with a
  build-info assertion and retained detector reports. SnapshotB passed seven
  selected cases; snapshotC passed corrected X10 plus the six lock assertions.
  These are local Docker/WSL results, not hosted CI or installed-host evidence.
- `1775eda` corrects maintenance, privacy, UAT and CLI-help claims. The focused
  docs suite and generated command inventory pass. UAT-01 through UAT-12 remain
  explicitly unexecuted; no human interaction is inferred.
- The non-authoring Opus 4.8 runtime review found no introduced code blocker.
  The full current candidate race set and hosted CI still need their own results.
  All observed child routing is recorded separately; overlapping resumed usage
  snapshots must not be summed or represented as invoiced cost.

GitHub nightly `35704111100` is real historical evidence on `9c84e31d`:
26 jobs succeeded and three failed. Saved logs show B-B budget failures on Linux
and Windows, plus Windows permission-fixture/deadline failures. It cannot certify
the current candidate. The earlier “no CI has ever run” map is superseded.
Public name endpoint lookups were also performed, without asserting registrability
or trademark clearance. No-tag release-check can run with version agreement
skipped; --tag requires both declared-version agreement and the actual HEAD tag.

Remaining automated obligations include final integrated/package results,
supported-environment privacy/recovery/compatibility, quiet performance,
held-out/closed-loop evaluation implementation and trials, and final independent
acceptance. Native host permission parity and unavailable installed targets remain
unknown. Human UAT and separate release authorization remain external gates.
V6 and the project are not complete with only human tests remaining.

## Earlier handoff — retained chronology, superseded above

2026-09-21. User authorized non-human corrections and bounded parallel children.
Worktree `qompack-v6`, branch `verify/v6`; tested source and dirty overlays are
identified per run. No release, tag, publish or merge to develop has occurred.
Human UAT remains unexecuted. The coordinator is Codex; requested Fable identity
is not established. All completed child invocations checked here report canonical
`claude-opus-4-8`; raw invocation reports are retained under `routing/`.

## Accepted focused evidence so far

- `observer-derived-recovery-integrated`: real observer replay after the index/link
  cut passes. Subsequent store hardening still requires revalidation.
- `ordered-reused-lease-fixture`: same-session successor waits for predecessor
  replay/ACK; original observation identity and supersession regressions remain.
- `delivery-terminal-wiring-corrected`: denial disposition survives restart,
  never fabricates capture ACK, and unavailable policy/journal/identity retains
  the source. The physical symlink transition requires Linux evidence because
  Windows may skip it. Publication-time policy recheck and concurrency guards
  were added subsequently and are not certified by this earlier pass.
- `delivery-migration-anchor-guard`: missing legacy anchors over surviving
  migration evidence are refused, committed as `c95b7af`. No rollover claim.
- `compat-old-linux-reference-identity`: the isolated reference binary from
  integrated source `301a8e9` reports `v0.2.0-640-g301a8e9`. The earlier build
  command failed only at its incorrect `--version` argument; it produced the
  preserved binary SHA256 `8cb4c0bcc3b98626a625cf0cf53898bab4316effd3d41820f85c3f38fa096bd0`.
  This is an older source reference, not a released-artifact compatibility pass.

## Corrections still under integration

The capacity author's first handoff did not wire the new radix/generation helper
into the live journal. Its capacity closure is rejected; the author is continuing
with exclusive journal/seal/backup-watch scope. Automatic rollover is not yet
accepted or certified. The helper's focused test attempt failed during another
source edit, so its absence from the compiler errors proves nothing.

The store sidecar's earlier review missed caller-modified supersede targets after
commit and attributed uncertainty losing to a committed lookup. The coordinator
rejected those conclusions; the independent reviewer withdrew them in the next
review appendix. Full immutable intent, target preconditions, bounded load/append,
concurrency and confined path handling are being corrected. Prior failed tests,
rejected wire-format/rollover prototypes and reviews are preserved.

Terminal review O1: loss of the only source bytes for an earlier leased event
remains unavailable; successors cannot invent or bypass that history. Restore a
matching source/verified backup, preserve original evidence, and never delete a
lease to obtain a fresh identity. O2's retirement race was corrected with the
shared in-flight key, not accepted as an atomic guarantee. O3 cancellation checks
were added to the deferred replay loop. O4 needs an executed Linux transition.

Observation intent uncertainty is now included in the bounded startup audit,
read-only fsck publication row and same-build restore proof. These latest changes
are implemented pending focused validation. A transient uncertain append requires
store reopen; torn/conflicting evidence is not automatically truncated or repaired.

## Remaining release obligations

Freeze and identify source after author/reviewer handoffs; run justified focused
and final gates with actual child instrumentation; produce and identify the final
package; run automated install/upgrade/recovery/compatibility/privacy workflows,
quiet performance and held-out evaluations on available supported environments.
Record unavailable targets and integrations explicitly. No old source or temporary
package certifies the final installed bundle. Human UAT and release/publish approval
remain separate. V6 is not complete with only human tests remaining.

`preservation-integration-continued.json` rechecked all 20 original files unchanged.
Raw usage snapshots can overlap across resumed sessions and must not be summed or
represented as invoiced/subscription cash. Final acceptance/report remains pending.

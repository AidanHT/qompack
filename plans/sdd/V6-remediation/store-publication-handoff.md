# Observation publication correction — focused handoff

2026-09-21, `verify/v6`, based on `c95b7af` plus the source overlays identified
in each run. This is not release acceptance or installed-plugin evidence.

Publication intent now lives in the additive version 3
`index/observations.jsonl`; the legacy tool-use wire remains unchanged. The
intent records the original record, requested supersede IDs and selected target
identities. Recovery checks the original object closure, completes interrupted
record/mark publication and preserves later supersession. Uncertain, corrupt or
unreadable intent evidence remains unavailable. Close drains publishers; pending
reservations exclude conflicting legacy writers. Read-only fsck and restore
reader proof expose intent uncertainty.

The observer recovers the original intent before deriving new objects or marks.
A missing/mismatched capture refuses leased publication. The two old publication
fixtures now use the real store, with the same observation session throughout.
Original test IDs and the failed combined run are retained.

Executed evidence:

- `integrated-store-observer-publication`: store package passed (250.160 s);
  overall run failed on two observer fixtures. Never an overall pass.
- `observer-package-after-real-fixtures`: complete observer package passed.
- `observer-original-intent-after-drift`: original-ID/link tests and real-store
  replay after later supersession plus missing capture link passed. This closes
  the additional evidence request in `store-integrity-final-review.md` §3.
- `linux-publication-order-race-focused`: store/observer/daemon/CLI selected
  in-process paths passed under `-race`, including Unix FIFO and symlink cases.
  Exact source is the immutable `linux-focused-publication-20260921-1823.tar`
  snapshot; subsequent audit classification and fixture changes are newer.
- `publication-store-observer-lint`: one unchecked test type assertion failed;
  `publication-observer-lint-corrected` passed after correction (also CLI).
- `publication-nomagic-scoped`: failed on three safety bounds whose values
  coincide with defaults; named bounds/justified exemptions retain the same
  behavior. `publication-nomagic-scoped-corrected` passed.

Independent reviewer `store-integrity-final-review.md` inspected current source
and found no new store defect. Its pending-fixture description predates the
later successful runs above. Review is not a claim of release sign-off.

Limits: the observation sidecar has a 64 MiB / 1,048,576-entry lifetime cap;
exhaustion refuses new reservations and keeps delivery pending. There is no
automatic intent compaction, uncertain-tail truncation, general F4 orphan
reconstruction or power-loss atomicity guarantee for a single file write.
Static aliased/nonregular paths are refused; adversarial concurrent filesystem
replacement is not fully established by these tests. Final supported-platform,
packaged recovery/compatibility, performance, held-out evaluation and human UAT
remain separate obligations. Historical reports and original workspace dirt
remain preserved.

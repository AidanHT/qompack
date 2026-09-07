# SP-20 prerequisite implementation progress

Status: in progress; M1/M2 and revised V4 are not accepted. The user authorized V4 execution and
continued prerequisite work on 2026-09-07. This worktree is
`feat/sp20-capture-storage-and-state-remediation`, based on integrated `develop` at `919ca3a`.
Original SP10–13 completion, accepted M0-G0, and the V3 waiver are preserved. No migration
switch is enabled. No active user data store, installer, deployment, or final V4 branch is used.

## Contract and compatible correction slices

`029d065` adds the shared observation identity and qualified retrieval-envelope types, with
frozen records unchanged. The sidecar primary key is ObservationID; a repeated or absent host
ID cannot serve as that key. The envelope alone cannot prove complete fresh absence, so its
qualifier returns uncertain for absent. This is a shared contract slice, not the raw-capture
producer or the complete T20-M1-01/02 gate.

The next compatible daemon correction addresses specific SP05-D1 failure modes before the
remaining publication implementation:

- Sync the WAL before transport acknowledgement; full object/reference durability remains a
  separate gate. Worker acknowledgement now reflects callback rejection or cancellation.
- Distinguish an in-flight handler from a completed one. Rejection remains retryable; the
  bounded content-hash dedup window is still local to one daemon and is not durable identity.
- Advance drain offsets after acknowledgement, preserve incomplete trailing records, and
  return state-persistence failures before deleting sources.
- Persist blob cleanup intent with consumed offsets. Keep bytes until all currently visible
  spool references are consumed; retry failed deletion after restart. Validate blob names,
  type, size and opened-file identity before reading.
- Reject corrupt or inconsistent drain progress without dispatching or deleting evidence.
  Preserve it for an explicit recovery decision.
- Report an unconfigured observer as an acknowledged unavailable no-op and log live handler
  failures without payloads. This is not successful capture.

These are small prerequisite/compatible-correction commits under V4's execution sequence, not
closure of SP-20's eight planned implementation units. The planned raw capture, publication,
retention, import, state and integration units remain incomplete. They land on the eventual
integrated candidate only after their own acceptance.

## Validation and independent review

All runtime checks here are provisional Windows checks on disposable Go test roots. No host
plugin session, user store, Linux/macOS runner or final integrated candidate was exercised.
Artifacts are in this worktree's `.v4-artifacts/`; the `drain-references*.run.json` records name
the baseline commit, dirty source/test SHA-256 values, exact selection, elapsed time and result.
Each artifact is one distinct run and failures are retained.

| Artifact | Result and interpretation |
|---|---|
| `evidence-red`, `evidence-green` | New core contract initially failed to compile, then passed. The independently reviewed final contract is committed at `029d065` |
| `drain-red`, `drain-green`, `drain-corrected`, `drain-race` | Earlier failing/partial and passing slices retained; later runtime corrections supersede their daemon coverage |
| `drain-references` | Build failure from the missing encoding/json import; fixed, not omitted |
| `drain-references-corrected` | Two fixture failures: retained shared-blob cleanup journal was incorrectly expected deleted, and an inactive-session fixture kept its writer open |
| `drain-references-final` | Focused drain/blob/ingest selection passed, package time 0.902 s |
| `drain-references-race` | Daemon failed because another inactive-session fixture kept its WAL writer open; IPC passed 17.229 s and core passed 2.233 s |
| `drain-references-race-corrected` | Daemon race retry passed 11.766 s (21.883 s command elapsed) after closing the fixture writer before inactive drain |
| `drain-fmt`, `drain-vet` | Pinned whole-tree formatting check and `go vet ./internal/daemon ./internal/core` passed |

The corrected daemon race retry changed only the inactive-session fixture; unchanged IPC/core
sources and dependencies retain their results from the preceding run. The old same-process
dedup test's comment no longer promises dedup across restart. A new fresh-process fixture
demonstrates the remaining at-least-once boundary. Formatting and vet ran as two light commands
after the race job; at most one owned heavy validation command ran at a time.

A fresh independent Terra/high reviewer authored no runtime or tests in this slice. It required
durable cleanup intent, retryable deletion, shared-reference protection and strict progress
validation; those findings were fixed and reviewed. Native requested model/effort are recorded;
effective route, effective effort, token usage and cost were not exposed. The shared V4 fixture
author requested Terra/high. No nested delegation; the combined V4 pool stayed within three
active children. The independent packet is in the preparation worktree at
`plans/sdd/V4-VERIFY/runner-coverage-review.md`.

## Mandatory remaining work

### Provisional observer acknowledgement correction

On source `f6a8691` plus the hashes in `observer-publication-*.run.json`, required tool
content/reference failures now return generic `observer.ErrUnpublished` wrapping
`core.ErrDegraded`. A failed reference stops remembered-use, file-history, graph, sketch,
grammar and signal publication. The daemon returns an internal NAK and keeps live work out of
the completed seen set, so the WAL can retry it. Host output stays empty. Observer stage
counters remain available; neither the observer nor daemon log records backend error text on
this failure path.

The historical `TestOnToolUse_PutFailureIsSoft` and
`TestOnToolUse_IndexFailureStillFeedsSketchesAndDAG` assertions are replaced by
`TestOnToolUse_PutFailureRemainsUnpublished` and `TestOnToolUse_IndexFailureStopsPublication`.
`observer-publication-red` records both old behaviors failing the revised criteria.
`observer-publication-green` passes the affected observer/daemon/drain/ingest race selection
in 30.112 s (observer 5.691 s; daemon 5.067 s). After independent review requested explicit
error wrapping and privacy assertions, `observer-publication-privacy` passes the three changed
tests under race in 26.391 s (observer 3.166 s; daemon 4.052 s). Only test assertions changed
between those runs, so the unchanged broader selection retains its provisional coverage.
`observer-publication-guards.run.json` records formatting and affected-package vet.

The shared V4 fixture worker owned only `internal/daemon/observer_publication_test.go`; its
real store and WireObserver fixture injects object/reference failures, verifies the retained
WAL and incomplete seen state, repairs the store wrapper and drains the original delivery.
The fixture explicitly closes its inactive WAL writer before deletion on Windows. Independent
Terra/high review authored none of the source/tests and accepted this bounded correction in
the preparation worktree's review packet. Effective model/effort and usage were not exposed.
No host session, user store or final integrated candidate was exercised.

This is a failure acknowledgement correction, not a durable publication commit protocol.
Successful legacy store calls still do not prove object/index sync, delivery identity, leases
or a committed frontier. Prompt and stop failure/retry semantics remain required separate
work; this test does not cover them.

This correction does not close SP05-D1 or T20-M1-05 as a whole. A client may publish a fallback
after the spool-reference scan; a durable delivery/lease ledger must protect that late
reference. Distinct equal-byte deliveries still need distinct ObservationIDs, durable sequence
allocation, file generations and restart acknowledgement. Basename reuse and missing progress
cannot be solved by content hashes or current-file scans.

Raw privacy-before-persistence capture, faithful source envelopes, durable object → verified
reference → committed frontier publication, global GC/lease/delta/rollback roots, import and
backup parity, pre/post-write rollback, M2 authority and completeness producers, and their
SP10–13 consumers remain unverified or unimplemented. The current observer still absorbs some
store errors; callback acknowledgement is not proof of durable publication. Quiet full hot-path
measurements must assess the new WAL Sync cost. No performance acceptance follows from a race
or unit pass. Installed Claude Code recovery and all final V4 gates remain open.

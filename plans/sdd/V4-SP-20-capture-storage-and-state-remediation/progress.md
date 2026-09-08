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

### Provisional object integrity correction

On `3576900` plus the source/test hashes in `object-integrity-*.run.json`, every object read
checks its plaintext against the requested `DomainChunk` hash, including raw objects and
orphan objects without a roots entry. A valid zstd checksum and matching length alone did not
bind the bytes to that address. Open/OpenSpan and Search consume the same verifier. Delta
payload chunks use the same domain; their enclosing root metadata is a separate identity.

Physical reads reject nonregular leaf paths, compare Lstat/Open identities, check file size
before reading, and enforce a LimitReader bound if the file grows. Raw bytes are capped at
MaxPutBytes; compressed bytes include the actual encoder's maximum framing/block overhead.
Decompression retains its existing cap. This does not establish authorization of ancestor
directories or a complete defence against concurrent filesystem mutation.

Quarantine now uses a unique attempt directory and preserves prior evidence. A failed move
leaves the source intact and counts/logs `store.quarantine_failed`; reads still refuse its
contents. The historical `TestQuarantine_RemovesTheObjectEvenWhenTheMoveFails` assertion is
replaced by `TestQuarantine_PreservesExistingEvidence`, and the blocked quarantine root fixture
checks rejection without losing the source bytes. Historical corruption and indexed-length
tests locate the preserved evidence in the new attempt directory. No frozen data fixture was
rewritten. Read failures retain the legacy `core.ErrNotFound` contract; the M2 outcome envelope
must still distinguish corruption/unavailability from supported absence.

| Artifact | Result and interpretation |
|---|---|
| `object-integrity-red` | New replacement/oversize/quarantine fixtures failed against the prior implementation; package 1.232 s, command 8.014 s |
| `object-integrity-green` | Selected store read, search, write, recovery and integrity cases passed under race; package 71.378 s, command 95.289 s. The separate storetest package selected zero tests and contributes no coverage |
| `object-integrity-conformance` | Corrected selection of the actual `TestRunStoreSuite_AgainstRealStore` passed under race; package 3.434 s, command 9.951 s; source/test hashes unchanged |
| `object-integrity-guards` | Formatting and affected store-package vet results recorded separately |

Only one heavy command ran at a time. The shared Terra/high fixture owner authored only
`internal/store/object_integrity_test.go` and released it before validation. The coordinator
owned implementation, historical test mappings and commands. Independent Terra/high review
authored none of the source/tests; its bounded evidence review is in the V4 preparation
worktree's `plans/sdd/V4-VERIFY/runner-coverage-review.md`. Requested routing is recorded;
effective model/effort, usage, queue wait and task cost were not exposed. Checks used disposable
Windows test stores, not an installed host or final candidate.

This is partial T20-M1-04/M2-04 evidence, not their acceptance. Write-side existing-object
reuse remains optimistic; object sync, verified reference/frontier publication, crash cuts,
durable identities, migration, GC/lease/rollback roots and scoped retrieval remain mandatory.
The legacy writeStaged comment now accurately states that Flush does not sync its closed
object files. No write-side durability change or performance acceptance is implied.

### Provisional capture and privacy contract

On `16ecc77` plus the hashes in `capture-policy-*.run.json`, additive `core.CaptureDecision`
and `hookio.CaptureHook` establish a pure capture seam. A non-nil, explicitly versioned policy
must admit the bytes before an Event is derived. The source receives only bounded UTF-8/JSON
object validation before policy evaluation. Unchanged permitted bytes retain whitespace,
escapes, unknown fields and numeric spelling. The capture's `[]byte` JSON field uses base64
so serialization does not compact the original payload. Source, policy, capture and derived
Event buffers have separate ownership.

Policy fidelity is explicit, not inferred from byte differences. Exact requires byte identity
without redacted/truncated flags; other valid fidelity states remain qualified. Denial returns
`OutcomeDenied` with no bytes or Event; policy failure/panic, unsupported decisions and invalid
outputs return generic unavailable failures without backend text. Source/result size refusal
retains a truncated flag and returns no prefix to persist. This is a JSON host-delivery seam;
it does not infer complete underlying process output or implement binary-file capture.

`redact.CapturePolicy` supplies the `redact-json/v1` policy without changing the frozen
Redactor constructors. Enabled policy creation requires every configured pattern to compile
and pass the existing admission rules; rejected patterns are not logged. Explicit disabled
redaction configuration permits valid JSON unchanged. The new policy examines decoded keys,
strings and unknown fields, escaped raw spellings, and structured sensitive properties using
the existing built-in assignment/.env key rules. It preserves exact numeric values when a
redacted representation is encoded. Ambiguous duplicate keys, redacted key collisions, invalid
UTF-8/surrogates, nesting beyond 128 levels and matches spanning JSON syntax refuse retention.
The adapter is not a general guarantee that regex rules identify every secret.

| Artifact | Result and interpretation |
|---|---|
| `capture-policy-red` | Initial hookio fixture used unavailable `require.NotNegative` and did not compile; redact fixtures failed against the stub. The compile error is preserved and contributes no hookio assertion evidence |
| `capture-policy-red-corrected` | Corrected fixture compiled; both packages failed their new assertions against the explicit stubs |
| `capture-policy-green` | Capture, real JSON privacy policy and their composed envelope round trip passed under race; command 35.758 s, hookio 2.015 s, redact 2.055 s, integration 2.177 s |
| `capture-policy-reviewed` | After review removed pre-policy Event parsing, affected capture/composition and retained core/ReadEvent/redactor contracts passed under race; command 31.822 s, core 1.782 s, hookio 2.246 s, redact 2.741 s, integration 2.489 s |
| `capture-policy-guards` | Formatting, affected-package vet and the actual importgraph/testdeps subchecks passed; no full lint or coverage-floor gate implied |

The reviewer required explicit policy fidelity and Event derivation only after permission;
both changes are implemented and covered. New redact implementation/tests were unchanged
between the green and reviewed runs, so their earlier focused evidence carries provisionally
under that dependency review. The integration fixture composes real policy/capture functions
and JSON serialization, not a daemon, filesystem or installed host. All original frozen
fixtures remain unchanged.

The shared V4 Terra/high fixture worker owned only new `internal/hookio/capture_test.go` and
released it before execution; the coordinator corrected fixture compilation/bounds, added
the review cases and owned remaining source/tests and commands. A separate Terra/high reviewer
authored none of them. Luna/medium supplied exact read-only policy/transport inventory. Model
and effort requests are recorded; effective route, usage and queue wait were not exposed.
At most two children were active together and only one heavy command ran at a time.

This is partial T20-M1-01/02 preparation and selected legacy redactor regression evidence.
The new APIs are unwired, `runtime.migration.capture.rawEvidence` remains refused, and current
CLI/spool/blob/WAL paths still lack privacy-before-persistence and faithful raw capture. Strict
policy loading before fallback, raw sidecar ownership/limits, durable per-delivery identity,
lease/ack retention, source-to-observation publication, all fidelity/lifecycle cases and
installed recovery remain mandatory. No T20 or V4 row closes from this seam.

### Provisional durable delivery assignment

On `9e939dd` plus the hashes in `delivery-lease-*.run.json`, the daemon-private journal binds
a nonzero caller delivery nonce, session and nonzero request digest to a persisted arrival
sequence and derived ObservationID. Separate nonces with equal content remain separate events;
the same nonce and binding reuses its identity across restart. Binding conflicts fail without
appending. Empty session is a valid scope; no host identifier is fabricated.

One `Lock` owns and serializes one journal, heartbeat and release. Its additive random owner
generation prevents an old Lock in the same process from operating on a later acquisition.
Release closes its journal first; close failure retains singleton ownership. Lost, released,
faulted or failed-open owners refuse further assignments. Backend errors are generic and the
journal stores no request bytes. A digest here is a binding value, not evidence of privacy or
object publication.

The bounded canonical JSONL journal and its canonical position companion live under the owned
state directory. A new row is appended and synced, then the byte/count/rolling-hash position is
atomically written, before maps advance or an identity returns. Reload validates every row,
version, derived ID, sequence, duplicate binding and the sealed prefix. A complete valid tail
can be synced and sealed; torn data, missing one companion, complete-line truncation and
changed prefix are preserved and refused. The live writer rechecks its seal before appending.
Short write, sync, position and close failures require release/reacquisition; no silent repair
under the failed owner is allowed. Journal/record/entry caps refuse further work; no journal
compaction is implemented. File-leaf identity and bounds checks are not full ancestor authority.
Loss or rollback of both journal and position still requires outer backup/migration authority.

| Artifact | Result and interpretation |
|---|---|
| `delivery-lease-red` | Real pre-implementation stub failures, package 0.545 s; negative refusal cases alone supplied no positive implementation evidence |
| `delivery-lease-green` | Selected journal and existing lock race cases passed, package 4.617 s / command 16.249 s; malformed-row fixtures initially lacked a position file and stopped before parsing, so that apparent coverage is superseded |
| `delivery-lease-reviewed` | Corrected row fixtures and live-seal/failed-open regressions passed under race, package 5.105 s / command 13.932 s |
| `delivery-lease-lifecycle` | Added physical/line-size refusal plus actual startup-drain, idle, held-lock and admin-shutdown consumers passed under race, package 4.484 s / command 15.040 s |
| `delivery-lease-guards` | Formatting passed in 4.524 s and affected daemon vet in 2.125 s; no full lint, coverage, performance or aggregate gate implied |

The last run adds one bounds test only. Runtime sources and prior assertions are unchanged
from the reviewed run; the independent coverage review carries that focused evidence. The
shared Terra/high fixture worker owned only `delivery_lease_test.go`, releasing it before main
corrected the position fixture. Main owned runtime, failure fixtures and commands; a separate
Terra/high reviewer authored none of them. Its acceptance and final dependency review are in
the preparation worktree's `runner-coverage-review.md`. Requested settings are recorded;
effective routing/effort, usage and queue wait were not exposed. At most two children and one
heavy command ran concurrently. All write fixtures used disposable Windows test directories.

This is partial T20-M1-03/04/05 prerequisite evidence and selected SP05 lock/lifecycle regression
coverage. No ingress/drain path opens the journal yet. Stable nonce production, approved request
hashing, raw sidecars, lease acknowledgement/retention, object sync and verified reference to
frontier publication, the late-client fallback race, GC/import/backup/rollback and M2 authority
remain required. No installed-host, T20 or final V4 gate closes from this slice.

# Coordinator decision: durable observation binding in the private store index

2026-09-21. The existing exported ToolUseRecord has an Observation field, but its frozen
MarshalJSON intentionally omits it. The actual index/tool_use.jsonl uses a separate private tuRec
format (tooluseindex.go explicitly distinguishes it from the frozen type wire). Keep exported
MarshalJSON, Store interface, all historical lines and every frozen fixture byte unchanged.

Authorize an OPTIONAL omitempty observation_id field on newly written private tuRec content lines,
including the atomic record-plus-supersede append. Old private-index readers use json.Unmarshal and
ignore additive keys; demonstrate that old read compatibility instead of guessing. Observation
identity is metadata, not a synthesized HostID, and must not alter ArgsDigest or existing IDs.
This binds the index record and observation in one append, eliminating the need for a separately
reserved publication intent and its new crash/turn-reservation states. New readers recover that
binding on reopen; old readers may display content but do not gain this replay guarantee. No older
writer/downgrade support is inferred. The published capture sidecar remains the post-record link.

Add a narrow optional ObservationReader capability with ToolUseByObservation(ctx,id). An in-memory
secondary map may be populated by the same existing index loader/insertion path; ambiguous mappings
must report degraded, not absent, never mint a new record based on an incomplete lookup. Validate
nonempty observation ID encoding, cancellation, closed-store behavior, old records without the field,
reopen identity, superseding writes, and duplicate/mismatched identity. Do not change frozen interfaces
or fixture bytes. Record the compatibility distinction and exact tests in the work report.

Main owns observer fallback/leased failure propagation/sync ordering and read-only classifications.
Subagent exclusive scope: internal/store/fsstore.go (only additive observation map),
internal/store/tooluseindex.go, internal/store/tooluse.go (Observation comment only), and NEW
internal/store/observation_lookup.go, observation_lookup_test.go. No other source/tests, no commits,
no child agents, no broad/race/performance runs. Existing files have main changes; preserve them.
If an actual repository guard freezes tuRec's new-record shape, stop and return that exact guard;
do not weaken it. Direct focused Go exit status and unique failure logs required.

## Superseding decision: versioned observation sidecar (2026-09-21)

The private-index proposal above is REJECTED before integration. Independent review found the
normative architecture section 0.2 requires new fields in versioned sidecars, not private tuRec.
Preserve the original proposal, result and review as failed design evidence. Restore the frozen
private record shape; do not amend architecture or invent approval. Canonical child route was
confirmed claude-opus-4-8. Passing shape tests did not satisfy the architecture contract.

Implement the binding in index/observations.jsonl, versioned and append-only, with a durable
publication intent BEFORE a new legacy record append. An intent identifies the observation and
redacted legacy record plus exact selected supersede operations. It must never be exposed as a
published record while the record/required operations are missing. Add an optional explicit
ObservationRecovery capability RecoverToolUseByObservation(ctx,id) that resumes the original
intent and only returns a record after its legacy publication is complete. A read-only lookup
may report an incomplete intent as unavailable; never false absence. Legacy records without an
intent remain legacy evidence. Preserve the old public and private line formats and fixture bytes.

The store owns serialized reservation/publication under a separate publication mutex so concurrent
leased calls cannot steal a reserved derived ID. Complete existing intent before accepting a
conflicting ID, or refuse explicitly. Do not manufacture host IDs. Repeated host IDs may legitimately
map multiple observations to the same compatible record, so duplicate observation versus repeated
host identity must be distinguished. No ambiguous lookup may mint a new event.

Intent must be flushed and its directory synced before legacy append. Partial/malformed/unknown
sidecar data is unavailable, not absence. Replay original redacted record and supersede targets;
do not recompute them from current policy/session state. A partial legacy batch may leave record
without marks: complete only missing operations whose precondition still holds, preserve later
supersession, and refuse ambiguity. Do not claim one Write is power-loss atomic. Original failures
and incomplete states stay visible. Objects are already put before publication; main owns object
sync and capture-link-before-ACK. A sidecar intent is never itself permission to ACK.

Keep ObservationReader.ToolUseByObservation as a pure committed lookup for existing consumers,
with Recovery separate. Additive interfaces only. Context, closed/read-only, malformed/truncated,
unknown version, same-process and restart cut points, distinct equal-content observations,
conflicting reservation, supersede partial batch and later supersession need focused tests. Bound
individual records and recovery work explicitly; exhaustion is unavailable, never no binding.
No migration of historical bytes or enablement of pending migration switches.

Exclusive source ownership for this revision: internal/store/fsstore.go, tooluseindex.go,
tooluse.go (Observation comments), observation_lookup.go/test.go and NEW observation_publication.go,
observation_publication_test.go. Also readonly_test.go ONLY capability classification. If another
lifecycle hook is required, return its exact necessary edit to main. Main owns publication_sync.go
and observer files. First save rejected private-index edits to dist/v6-remediation/rejected-binding
without touching other authors. No commits, children, broad/race/performance tests. Record unique
focused runs through python dist/v6-remediation/run.py. Return any unresolved durable-contract
choice to main rather than downgrading it silently.

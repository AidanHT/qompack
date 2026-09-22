# V6 remediation verification allocation

Date: 2026-09-21. Implementation is authorized; human UAT and release publication are separate.
This is a new evidence series. `V6-report.md` and `sdd/V6-VERIFY/` remain immutable.

## Source and artifact boundaries

Preliminary focused runs name their source HEAD and dirty paths. Runs overlapping disjoint authoring
are diagnostic only, including intermediate build failures; they do not certify a release bundle.
After integration, freeze all source, test, launcher, schema and product documentation changes in
conventional commits. Build once per supported target/mode from that source, record bundle identity
and hashes, and reuse each required artifact. Subsequent evidence/report commits do not silently
change the tested source identity or trigger another entire release matrix.

## Allocation and reasons

| Group | Allocation | Reason and acceptance limit |
|---|---|---|
| Origin/privacy | Focused MCP/store cases, then real packaged security cases | ID/root/chunk origins, legacy lost paths, dedup, capture-before-persistence, metadata redaction; host native permission remains a separate capability |
| Publication | Bounded store/daemon cases, then F4-1/F4-4 packaged cuts | Startup/status must surface incomplete state while preserving evidence; detection is not reconstruction |
| Maintenance | Hostile manifest, writer exclusion, certification loss, no-replace and read-only source cases; packaged backup round trip | Same-build non-vacuous reader proof, checkpoint/seal integrity, source/later writes retained; no older-release claim |
| Whole-tree | One integrated devtool test gate after focused prerequisites; targeted reruns for actual failures | Capture admission and daemon startup affect callers across packages; inspect artifact outcomes as well as Go exit codes |
| Race | Reviewed V4 race contract, one supported local platform run after authoring | Parent `-race` does not instrument a separately built product child; record instrumented paths and missing coverage explicitly |
| Linux | Isolated immutable source snapshot in pinned Go container | Image `golang:1.26.6-bookworm`, digest `sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36`; qualify Docker/WSL environment and retain snapshot identity |
| Package/platform | Actual available isolated runner and installed artifact per scenario | Cross-compilation is not installed-target proof; unavailable macOS/native environments remain unverified |
| Deterministic evaluation | Existing replay entry point after argument review | Diagnostic harness scores are not task-quality guarantees; preserve frozen corpus and baseline |
| Performance | Existing declared budgets, measured after authoring on a quiet runner | The initial co-loaded B-F failure is retained. A later quiet run has its own identity and does not erase it; no post-outcome threshold change |
| Human UAT | UAT-01 through UAT-12, pending user operation | Automated packaged calls do not substitute for the requested interaction |

Use the existing test-defined sample sizes and thresholds for their exact workloads. Report actual
sample counts, platform/binary identity, distributions, co-load and exclusions. No new universal
savings, latency, accuracy or two-percent guarantee is proposed. Closed-loop/held-out model trials
require their own declared task set, repeated baseline, model identity, completion/constraint
outcomes and usage completeness; a synthetic replay does not certify this layer.

## Review findings to preserve

- Original AUTH-1/AUTH-2 failures and old-to-new test assertions remain linked.
- Independent recovery R1 requires durable refusal of an uncertified backup after lease loss;
  preserving only the command error is insufficient. The new certification marker is checked by
  operator verification, restore and fsck.
- Recovery R2 uses bounded streaming copy in operator maintenance; R3 keeps verify/restore off the
  live store; R5 is removed by platform atomic no-replace publication without a stale lockfile.
- The first capture implementation allowed unprovable full file captures and assumed metadata
  preceded a truncated response. Coordinator review rejected those exceptions; corrected tests
  must exercise reordered/ambiguous payloads and supplied-capture disagreement.
- V6-HOST-1 cannot be cleared by reconstructing static settings. A user preference question about
  supported retrieval scope is pending; neither branch of that choice counts as native-policy proof.

Final reporting retains all 304 original inventory identifiers, migration additions, integration
identifiers, explicit failures/skips/disabled states, three evaluation layers and rollback limits.
No release tag, merge back to develop, publish, configuration/authentication or billing change is
part of this work.

## Compatibility corrections found during integration

The original affected-package run is preserved. Its output timestamps contain long gaps and
wall-clock durations well beyond the requested ten-minute package bound; the exact environmental
cause was not established. Timeout outcomes are incomplete, not product passes or valid latency
measurements. Separately observed assertion failures are handled below.

- File-producing synthetic `Read` requests in `capture_admission_test.go`, `hooks_test.go` and
  daemon spool fixtures now include their intended in-project `file_path`. Missing paths remain
  explicitly refused in the dedicated negative tests.
- `TestHookCapture_OversizeRetainsARedactedPrefix` maps to
  `TestHookCapture_OversizeRetainsClassificationWithoutOpaqueBytes`. Partial, binary and hard-cap
  transport tests retain their original identifiers, fidelity, source-size, no-event and no-secret
  checks; opaque retained-byte success assertions are retired because an incomplete envelope
  cannot prove containment. The replacement assertion requires byte-free evidence.
- Reconstructed lifecycle events serialize absent tool input as JSON `null`. That is accepted as
  no structured path for pathless events; a file producer with the same input remains unavailable.
- Original raw envelopes are checked as well as redacted envelopes so replacing a path cannot
  grant containment. Duplicate structured path keys, including edits-array members, fail closed.
- Streaming restore initially collided with the layout helper's generated `.gitignore`. Staging
  now creates only the parent directories of manifest files; no unverified default
  files are generated. The original failed
  run remains available; the corrected maintenance/fsck focused run passed.

## Additional carried findings

The structural guard also executes every unresolved evidence symbol (including benchmarks); its
whole-package invocation was broader than intended and reached its default ten-minute timeout.
Its report-existence gate correctly reports unresolved V6 carry rows. The immutable blocked
`V6-report.md` must not be deleted or relabeled to suppress that result. Future focused guard runs
name the structural tests explicitly; the final suite retains the unresolved sign-off result.

SP20-D6's corrected test maps `GatedBASampleExcludesThePreACKHandler` to
`GatedBASampleIncludesThePreACKHandler`: the receive-time diagnostic remains a lower bound, while
the gated estimate adds measured pre-ACK handler duration. The client exit tail is still an
estimate, not a proven upper bound.

SP20-D4's retained cap test now checks that overflow refuses publication and keeps WAL/spool input
pending instead of ACKing and replaying identity-free observations. This mitigates data loss;
it does not implement journal rollover or close the capacity obligation. No carry row is silently
changed to fixed on that partial evidence.

## Derived publication regression assertion updates

These changes are under implementation; no PASS is inferred until a named current run verifies it.
The original negative control and its source copy are retained before correction.

- `TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart` retains its identifier and
  the required one-record assertion. The corrected fixture first creates a real capture sidecar,
  then models the missing link durably and reopens; both tool and subagent paths are exercised.
- `TestRedelivery_SidecarDescribingAnotherDeliveryIsNotRecognized` retains identity rejection but
  requires `ErrUnpublished` and no duplicate record. Conflict is not a new delivery.
- `TestRedelivery_DerivedToolIDWithADifferentRootMintsFreshly` retains its historical identifier;
  its obsolete duplicate-minting success is retired. Replaying one leased observation under changed
  derivation retains the original record and root, before any new object derivation.
- `TestRedelivery_ProbeOnAStoreThatCannotAnswerNamesTheRightCause` now expects the worker to retain
  unavailable leased work, with an index-stage error instead of attempting a doomed new object put.
  Client pass-through is a separate contract; worker success cannot certify missing evidence.
- `TestOnUserPrompt_LeasedCaptureFailurePropagates` now uses a real store/valid sidecar with an injected
  PutBytes failure. It asserts that the object-write failure was actually reached, no index landed,
  and the turn did not advance; a missing recovery capability cannot accidentally satisfy the test.

A single `appendFile.write` serializes record/mark bytes but does not prove power-loss atomicity.
The new sidecar intent must recover partial persistence without overwriting later supersession.

`TestRedelivery_ReferenceTheIndexDoesNotConfirmIsNotRecognized` keeps its two distinct cases:
missing index data may be reconstructed only from the durable intent, preserving original ID/root;
conflicting index data returns unavailable with no overwrite or duplicate. The corruption fixture
now edits a closed test store directly, since a compliant writer must refuse a reserved-ID theft.

The retained `TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent` now
requires the same-session successor to wait for predecessor replay/ACK. The injected
journal failure follows a real handler call rather than rejecting the newly added
pre-publication ordering query. The historical supersession assertion remains an
explicit redundant observer delivery after the successor commits. Read and Stop
crash replays still assert original identity, actual handler calls and unchanged
index bytes; the test does not move events to separate sessions to avoid ordering.

The retained `TestOnToolUse_PublishesTheReferenceAgainstItsObservationIdentity`
now uses the real store and a capture whose session matches the request. It checks
the persisted observation join again after reopening. The earlier fake store had
no recovery capability and the fixture's sessions differed. The retained
`TestOnToolUse_UnlinkableCaptureBlocksPublication` now checks a real store and
requires both object and tool-use indexes to remain empty when capture is absent.
The observer rejects that absence before any derived publication. Original failure:
`integrated-store-observer-publication`; focused correction:
`observer-real-publication-contract`; whole observer package:
`observer-package-after-real-fixtures` (passed). The store portion of the original
combined run passed; the combined run itself remains failed.

V6 section 5 explicitly replaces exact fragment-token addition. The assembled
budget correction therefore changes numeric token fields in the existing
`TestState_MatchesFrozenGolden` case (historical 7149 versus assembled estimate
7124) while its rendered payload and all other state fields stay unchanged.
Main retained that original test ID and every golden byte. It still requires a
lossless read/write round trip of the frozen state, byte-identical rendered
payload and equality of all non-price fields. The replacement assertions estimate
the independently frozen payload through the configured estimator and require
nonnegative per-item allocations summing to that total. No golden was regenerated.
`assembled-budget-pkg-1` preserves the original failure;
`assembled-budget-frozen-wire-mapping` passes the revised contract and new
assembled-budget cases. These are estimator checks, not provider usage calibration.

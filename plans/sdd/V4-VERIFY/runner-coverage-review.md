# Runner coverage review

## Evidence reviewed

This review is read-only. It covers `tools/devtool/test.go`, the CI and nightly
workflows, `test/e2e/harness.go`, child-process launch paths, the co-load guard,
and the current e2e test inventory. It does not rerun the known over-30-minute
race attempt.

`devtool test` currently runs `go test -timeout=30m ./...`, so it includes e2e
in a single plain aggregate. The R2 `devtool test-race` command obtains `go
list ./...`, removes exactly the `.../test/e2e` package, and runs the resulting
non-e2e list with `go test -race -timeout=30m` and the co-load declaration. CI
retains a separate plain `test-e2e` job.

The e2e harness builds the tested executable with `go build` in
`test/e2e/harness.go:71`; it does not add `-race`. Its command inherits the
environment, as do the harness run and the daemon/hot-path child launch paths.
Therefore `GOFLAGS=-race` can, in principle, reach those `go build` child
commands and create race-instrumented children when CGO/toolchain support is
available. No successful artifact demonstrates that setup over the intended e2e
corpus, nor proves that it has the same coverage as the original full race gate.

## Recommended usable operational split

Use a fast, coverage-preserving operational runner arrangement:

| Lane | Command scope | Assertions / process instrumentation |
| --- | --- | --- |
| Plain aggregate | `devtool test` with `go test -timeout=30m ./...` | Preserves the existing full behavior suite, including all e2e scenarios; tested child binaries are ordinary builds. |
| Unit/integration race | `devtool test-race` aligned to CI/nightly’s non-e2e package set, with the existing co-load setting | Exercises the in-process packages that CI races today, including ordinary Go test binaries; excludes the harness-driven e2e executable. |
| Plain e2e | Existing isolated `test-e2e` job | Retains all harness behavior, daemon, spool, checkpoint, MCP, fault, observer, session, store, scheduler, and V3 scenario assertions; child executable is ordinary. |
| Timing/quiet lane | Existing timing lane, with required co-load controls | Carries deadline and p95 assertions that are invalid or unstable under race instrumentation. |

This division preserves ordinary behavior coverage and gives a practical race lane
for the packages that complete under the configured wall-clock budget. It should
also retain the current `UnderCoload` guard: only X11 explicitly uses it today;
the other named yielders are the GC deadline tests, seven negative-knowledge
budgets, integration hot-path, and the current timing cases.

`TestBudgetBF` now uses the ADR-0010 quiet-lane discipline. Under co-load it
keeps the tool-response, call-mix, and histogram-count assertions and reports
the p95; it deliberately defers both the p95 and `CheckBudgets` gate. The CI
timing lane runs `TestBudgetBF` in `./internal/mcp`, without the declaration,
and performs those gates. `test/guards/coload_test.go` derives yielders from
source and verifies this routing. It is not a substitute for an e2e race claim.

## What this does not satisfy

The split above is an operational recommendation, not fulfillment of the
original full `devtool test-race ./...` gate. The original command includes 62
runnable e2e tests (five hook CLI; six daemon/spawn/spool/self-test; two
checkpoint; two MCP; three fault; two negative-knowledge; six observer; eight
phase-one; ten session-compaction; one scheduler; three V1; two store; twelve
V3 X01-X12). Those tests involve a separately built executable and other child
processes.

Although external `GOFLAGS=-race` would normally be inherited by the harness
build command, there is no recorded successful representative child-race smoke
or full e2e race result. Race instrumentation can also change timing enough to
invalidate the X11 timing assertion, and the full corpus previously exceeded the
30-minute budget. That timeout is operational evidence only: it neither shows
that every relevant child process was instrumented nor shows that no uncovered
e2e race remains.

Accordingly V4-ALL-01 remains open until one of these provides affirmative
evidence: an equivalent race-capable e2e split that succeeds within the agreed
budget, or a deliberately scoped representative child-race run with documented
instrumented process evidence and an accepted rationale for every omitted
scenario. The plain e2e job is necessary behavior coverage but cannot establish
that missing harness-race coverage is equivalent.

The R2 local runner has the same package-selection intent as CI's Linux/macOS
race job. It is not platform-identical: CI's Windows job uses plain
`go test -count=2` because that workflow retains `CGO_ENABLED=0`, whereas
`devtool test-race` requires a local race-capable CGO toolchain. No R2 command
or timing result is asserted by this review.

## Bounded legacy drain review (SP05-D1 compatibility)

The reviewed daemon change is accepted as a bounded at-least-once correction
for the legacy WAL/client-spool drain. It fsyncs the accepted WAL line, retains
the line and any externalized payload until a handler acknowledgement and a
durable drain offset exist, and retries rejected, cancelled, or failed blob
handling. `PendingBlobs` records post-ack deletion work; strict progress-state
validation stops rather than treats malformed, null, inconsistent, or
out-of-range state as consumable progress.

Before deleting an acknowledged blob, the drainer scans every outstanding spool
record from each persisted offset. An incomplete, unreadable, or invalid source
blocks collection. This keeps a shared payload available when a WAL copy has
acknowledged but a client-fallback copy remains unacknowledged. The reviewed
fixtures cover those cases, restart cleanup, state-write failure, Windows
deletion retry, incomplete lines, and the intentionally possible fresh-process
redelivery.

The focused artifact `drain-references-final.run.json` records a pass for:

```
go test -p=1 -count=1 -timeout=5m \
  -run 'TestDrain|TestResolveBlob|TestIngest|TestAcceptWireLine|TestLiveDispatchedLine' \
  ./internal/daemon
```

An earlier daemon/ipc/core race aggregate failure was confined to the existing
`TestNAKDuplicateIsDedupedOnDrain` fixture: it declares the session inactive
while leaving its WAL writer open, so Windows correctly rejects retirement of
that file. The fixture correction closes `dd.ing` before `Drain`; it does not
remove a runtime assertion. The test still requires one dispatch of the two
identical lines in the same daemon lifetime and now states that fresh-process
redelivery remains possible. The preserved original failure is diagnostic
evidence, not a product race result.

`drain-references-race-corrected.run.json` records
`go test -race -p=1 -count=1 -timeout=10m ./internal/daemon` passing in 11.766
seconds of package time (21.883 seconds elapsed). Earlier IPC (17.229 seconds)
and core (2.233 seconds) race evidence remains unchanged-dependency evidence.
Pinned whole-tree format checking and changed-package vet also passed. These
are recorded artifacts; this review did not rerun them.

This acceptance does not complete SP-20 M1/M2. The scan is necessarily a
snapshot: a client may publish a fallback descriptor after it, so it cannot be
the final blob reachability authority. There is still no persisted
ObservationID/arrival lease, durable retry identity, full raw
privacy-before-persistence envelope, object-to-verified-reference-to-frontier
publisher, global object/backup/import/GC roots, M2 completeness or authority
producer, or enabled migration gate. A stale basename-based progress entry and
a later reused spool filename are likewise outside this legacy compatibility
fix and require durable delivery/file generation identity.

## Preparatory corrections review

The reviewed prep slice is accepted as compatible preparatory work. It does not
enable a migration gate or establish full V4 verification.

The R2 runner splits the local race command to the CI non-e2e package set and
has a direct selector test. CI's isolated timing lane adds `TestBudgetBF` and
`./internal/mcp`; its co-load branch retains response, mix, and histogram
assertions while deferring p95 and registry-budget judgement to that isolated
lane. The checkpoint reader and replay edits are comments only: they correctly
say a reader's zero `Frontier` is not a durable frontier and that replay's
last-user-turn boundary is synthetic arithmetic, pending a versioned durable
seq-to-frontier record.

The paths additions test staging and destination replacement failures without
altering the implementation. Windows-only coverage includes conversion-only
UNC server/share preservation and embedded-NUL rejection before source handle,
destination replacement, or atomic staging side effects. The recorded
`paths-boundaries.run.json` pass reports `go test -p=1 -count=1
-coverprofile=.v4-artifacts/paths-boundaries.cover ./internal/paths`, with
90.9% statement coverage. The prior corrected paths artifact at 89.4% remains
valid historical evidence. This review did not rerun either command.

No mandatory issue was found in these bounded corrections. They do not satisfy
V4-ALL-01 or prove race instrumentation/coverage of harness-built e2e child
processes; the separate full-race/e2e-equivalence residual above remains open.

## MCP consumer correction review

This read-only review accepts the bounded MCP consumer correction as a
compatible preparatory change. It is not an SP-20 or V4 final gate claim.

`already_tried` maps every `Ledger.Query` error to a successful MCP domain
response with `state: "unavailable"`, `degraded: true`, a generic reason, and
a recovery note. It does not map an error to `absent` or `active`. The handler's
Loud message is constant and the response contains no backend error string;
the focused cases include `core.ErrNotFound`, cancellation, deadline expiry,
and an error carrying a private Windows path. This limits the assertion to the
MCP handler's response and diagnostic: it does not prove that every dependency
which could log independently has the same redaction policy.

The historical `initialize.json` and `tools-list.json` are retained. The active
tests use `initialize.v2.json` and `tools-list.v2.json`. A structural comparison
found that initialize v2 changes only `instructions`; tools-list v2 preserves
all eight tool names, titles, order, and input schemas, and changes descriptions
only for `recall`, `expand`, `re_read`, `already_tried`, `timeline`, `why`, and
`dropped`. The names `.v2` identify versioned test fixtures; they do not add
MCP protocol negotiation or let an old client select the former semantic
contract.

The generated MCP page and initialize instructions now distinguish Qompack's
ephemeral record metadata from host eviction and native-context retention.
This is consistent with the implementation: retrieval output can be stored and
ranked within Qompack, while the plugin cannot assert that the host will retain
or evict its context. The generated page no longer promises host-level eviction
or complete capture.

JSON shape remains readable by a permissive legacy decoder, as
`unavailable_test.go` demonstrates. A caller that treats `state` as a closed
three-value enum is semantically incompatible until it handles `unavailable`;
the tool description, initialize instruction, and generated page state that
limit and require unknown states to confer neither absence nor prohibition.

The recorded `mcp-unavailable-green.run.json` artifact passed the focused race
selection over `internal/mcp` and `tools/devtool` in 24.356 seconds (package
times 3.041 and 2.080 seconds). It covers the updated AlreadyTried,
initialize, tools-list, Tool, and generated-document guards. This review did
not rerun it. The subsequent review-only changes are comments and golden-path
annotations: runtime logic, test logic, fixtures, and generator inputs used by
the recorded command are unchanged, so no additional full chain is required
for this bounded acceptance. It does not establish a full MCP or whole-tree
race gate.

This correction also remains pending the M2 producer work: there is no
completeness/authority witness that would make an unavailable or absent answer
an SP-20 publication gate result. Installed host clients remain outside this
repository's compatibility proof; they must accept the additional state and
follow the current initialize/tools-list guidance before relying on it.

## Observer publication acknowledgement review

This read-only review accepts the bounded SP-20 observer correction in
`qompack-sp20` at `f6a8691` plus its dirty files. It corrects only the legacy
tool-capture acknowledgement boundary and does not close an M1/M2 or V4 gate.

`observer.OnToolUse` now returns the generic `observer.ErrUnpublished`, which
wraps `core.ErrDegraded`, for a failed `PutBytes` or `RecordToolUse`. The helper
counts `observer.err.put` or `observer.err.index` and logs only the stage. It
does not wrap, emit, or log the backend error. The returned PostToolUse output
remains empty. On a failed reference write it returns before remembered uses,
file history, exploration sketches, DAG emission, grammar, signals, and
feature state can advance.

The active daemon path has completion-aware `seen.begin`/`finish`: a live
worker adds a line to the completed set only after its `runIngested` response
acknowledges. An unpublished tool capture becomes the generic IPC NAK
`"observation handling failed"`; the daemon's tool and stop logs no longer
include callback error values. The tested repair sequence retains the exact WAL
line, confirms that no tool-use reference is queryable, clears the injected
failure, then drains the same line into a reference. This uses the real
WireObserver and Store with injected publication failures, rather than a
callback-only double.

The recorded `observer-publication-green.run.json` selected observer, routing,
drain, and ingest race cases passed in 30.112 seconds (observer 5.691 seconds;
daemon 5.067 seconds). After review-added privacy assertions, the recorded
`observer-publication-privacy.run.json` passed its three changed cases under
race in 26.391 seconds (observer 3.166 seconds; daemon 4.052 seconds). The
assertions cover the degraded sentinel, absence of injected backend text in the
observer error and IPC reply, no derived effects after index failure, and repair
through the retained WAL. This review did not rerun either command.

The remaining limitations are deliberate and material: prompt and stop
acknowledgement have not received this correction; raw ingress has not yet
been privacy-transformed before persistence; there is no durable
observation/delivery identity or lease; retry after an uncertain crash can
redeliver; and successful legacy object/index calls do not create a verified
object-to-reference-to-frontier publication authority or M2 coverage witness.

## Object-read integrity correction review

This read-only review accepts the bounded object-read correction in
`qompack-sp20` at `3576900` plus its reviewed dirty store files. It is a
legacy read-integrity and evidence-preservation correction only; it does not
close an SP-20 M1/M2 or V4 gate.

`getObject` now binds every returned raw or decoded object to the requested
`core.DomainChunk` hash, after the existing indexed-length check where one is
available. This closes the same-length, valid-zstd substitution path and the
unindexed/orphan path, where no roots length exists. The same domain applies
to delta storage: `putDeltas` ultimately persists `splitChecked` chunk bytes,
whose identifiers are also `DomainChunk`; there is no separate delta-object
hash domain to exempt.

`readBoundedObject` rejects nonregular leaves, checks the opened file remains
the lstat'ed file, rejects an over-limit physical size before reading, and
uses `io.LimitReader(limit+1)` to retain that allocation bound across a file
growth race. Raw objects are limited to `MaxPutBytes`; zstd objects use the
maximum encoded size from the same pooled default encoder that writes them,
so valid incompressible maximum-size frames remain readable. Rejections are
quarantined where possible and preserve the legacy `core.ErrNotFound`
degradation, including the explicit physical-size error text.

Quarantine now creates one `MkdirTemp` attempt directory per rejected object
and retains the historical object basename inside it. This prevents a repeat
or concurrent rejection from overwriting evidence. If the quarantine root,
attempt creation, or move fails, the original source is retained, the caller
still receives no bytes and `ErrNotFound`, and the store logs only the hash
and reason while counting `store.quarantine_failed`; it does not delete the
source or include raw backend detail. The mapped historical test confirms an
occupied former fixed destination remains intact while new evidence is stored
in a distinct attempt directory.

The preserved `object-integrity-red` artifact documents the pre-correction
failure. `object-integrity-green.run.json` records the selected store race
command passing in 71.378 seconds with 70 source-matching top-level cases.
That initial selection matched zero `store/storetest` cases, so it makes no
conformance coverage claim. The corrected separate
`object-integrity-conformance.run.json` records
`TestRunStoreSuite_AgainstRealStore` under race passing in 3.434 seconds of
package time (9.9506784 seconds elapsed). This review did not rerun either
command.

The fixtures cover valid compressed and raw same-length substitution,
unindexed substitution, lazy `Open`/`OpenSpan` reads, all four
compressed/raw and indexed/orphan physical-size combinations, and quarantine
root failure retaining the source. They do not create a durable publication
frontier, an object-reference reachability authority, a delivery identity or
lease, privacy-before-persistence transformation, an M2 completeness witness,
or backup/import/GC root coverage. Those remain required SP-20 work.

## Relevant locations

- `tools/devtool/test.go:24,48` — aggregate and race command construction.
- `.github/workflows/ci.yml`, `.github/workflows/nightly.yml` — non-e2e race
  package selections and separate plain e2e/timing jobs.
- `test/e2e/harness.go:40,71,102` — harness construction, child build, and run.
- `test/daemon/daemon.go:123` and `test/bench/hotpath/process.go:84,117` —
  inherited-environment child launch paths.
- `test/guards/coload_test.go:76` — co-load enforcement.
- `docs/adr/0010-wall-clock-budget-judgement.md` — quiet-lane and wall-clock
  rationale.

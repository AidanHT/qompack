# V5-VERIFY §4.3 — disposition

| | |
|---|---|
| Retained identifier | `TestV5_HookEventToTombstoneToRetrievalAfterRestart` |
| Current criterion (plan §4, row 4.3) | SP-20 acknowledged capture survives interrupted drain/restart, or records explicit gap. |
| Disposition | **authored**, with one production fix the row found |
| Level and file | e2e — `test/e2e/v5_x03_test.go` (real binary for every hook; real daemon composed in-process the way `v4_harness_test.go` composes it; a second real daemon over the same tree for the restart) |
| Base | `verify/v5` @ `87c0c1d` |

## Why this level

The historical seam crosses a process boundary (hook process → daemon → restart), and it still does:
every event enters through the real `qompack observe tool` / `session-start` / `flush` binaries. The
daemon is composed in-process for the restart half because on Windows a hard-killed daemon's lock is
reclaimable only after the 90 s heartbeat staleness window (`internal/daemon/lock_windows.go`,
`lock.go` step 4); a spawned-and-killed daemon would turn the row into a wall-clock gate. The
in-process composition is the shipped one (`WireObserver` → `New` → `Run`), and the interruption is
two real switches — a cancelled drain context and a `Stop` whose drain budget is already spent —
not a stub.

## Test names and what each asserts

`TestV5_HookEventToTombstoneToRetrievalAfterRestart` (one function, four phases, one subtest):

- **Phase 1 — live, acknowledged.** 6 `observe tool` hooks reach the daemon live. Asserts one lease
  and one committed-frontier record (`state/delivery-leases.jsonl`, `state/delivery-acks.jsonl`)
  per delivery, and for every acknowledgement: the capture sidecar
  (`store.ReadCaptureSidecar`) is readable, names the same delivery token, is `Published`, carries a
  non-zero root, and its retained host bytes name the same `tool_use_id` as its verified reference.
- **Phase 2 — captured on disk, never seen by the daemon.** `run/state.bin` is flipped to
  `HotSpool` (the same switch `TestE2ESpoolSubmodeEndToEnd` uses); 8 more hooks append to their own
  `client-<pid>.ndjson` spools. Asserts 8 decodable spool lines each carrying a delivery nonce, the
  index unchanged at 6, and still 6 leases — a delivery the daemon has not seen has no identity yet.
- **Phase 3 — the interrupted drain.** A probe registered as a second `Options.Bind` wraps
  `Services.ObserveTool` (the real observer still runs; the wrapper only counts completions) and
  cancels the drain's context after the 3rd spooled handler completion. Asserts `Drain` returns
  `context.Canceled` with `drained == 2`; `DrainGapState` is `Observed`, not `Complete`, with exactly
  one `unacknowledged` gap, positive `PendingBytes`, and no `unleased`/`corrupt_line` gap. On disk:
  9 leases, 8 acks, 9 index lines — the cut delivery is leased, captured, indexed and linked but not
  acknowledged, which is SP05-D1's state with every stage durable and the frontier withheld. Then
  `Stop` under an already-cancelled context; asserts the acknowledgement journal is byte-identical
  after the shutdown (the replay was NOT finished by the stop).
- **Phase 4 — restart.** A second daemon over the same tree. Asserts: the startup drain brings acks to
  14 and `DrainGapState` to `Complete` with no gaps; 14 leases (the redelivered cut line took its
  ORIGINAL lease back — same `ObservationID` before and after); both journals are append-only
  prefixes of their pre-restart bytes; every live delivery's identity is unchanged; every acknowledged
  delivery resolves in the restarted index (`Store.ToolUse`) with a root equal to its sidecar's
  reference; the index holds exactly 14 lines (no duplicate for the redelivered record); the client
  spool is released; a further `Drain` is a no-op; `flush` through the real binary clears the
  session's `state/session-recovery.json` marker; `AssertAppendOnly`.
- **Subtest `corrupt spool line is recorded as a gap`** (own project): 4 spooled hooks; the first
  byte of the first client spool file is corrupted in place. Asserts `Drain` returns no error and
  delivers the other 3; `DrainGapState` is not `Complete` and names the file with one
  `corrupt_line` gap; counter `drain_file_error == 1`; the lost id is absent from the acks and
  `Store.ToolUse` answers `core.ErrNotFound` for it.

## Negative controls and how they were proven

1. **Built-in, phase 3:** the drain must return `context.Canceled` and report an `unacknowledged`
   gap; a drain that could not be interrupted, or accounting that did not record the withheld
   frontier, fails there.
2. **Built-in, phase 3→4:** the ack journal must be byte-identical across the exhausted-budget
   `Stop`; a shutdown that finished the replay would make phase 4 a test of nothing.
3. **Subtest:** a real spooled line corrupted on disk (the plan's "corrupt file" switch). The same
   `DrainGapState` reader that answers `Complete` in phase 4 must answer a named gap here, and the
   store must answer not-found — never coverage.
4. **Red on the base tree (production defect, see below):** the first two runs of this test on
   `87c0c1d` failed in phase 1 with `leases=0 acks=0; counters=[l0_delivery_unleased=6 ...]` —
   every live delivery unleased. The fix commit turns the row green; reverting
   `deliveryNonceBytes` to 16 in `internal/ipc/wire.go` turns it red again at the same line.

## Production change (own commit, first)

`fix(ipc): mint the 64-hex delivery nonce the journal accepts` — `internal/ipc/wire.go`
`deliveryNonceBytes` 16 → 32. The hook client minted a 32-hex-character nonce; the daemon's delivery
journal (`internal/daemon/delivery_lease.go`, `validDeliveryToken`) accepts only 64 lowercase hex
characters, so `ingest.leaseDelivery` was refused with `ErrContract` for EVERY real delivery and
counted it `l0_delivery_unleased`. Because a lease is publication order's first stage, no capture
sidecar, verified reference or committed frontier was ever written on the shipped path; only the
unit tests, which hand-roll 64-character tokens on the journal side and pin 32 on the client side,
ran that chain. The fix aligns the producer to the durable format's contract (loosening the
journal's validation of an on-disk format was the larger and less safe change). Two tests that
pinned the 32-character length were re-pointed (`internal/ipc/capture_wire_test.go`,
`internal/cli/capture_fidelity_test.go`), and the missing cross-package pin was added:
`internal/daemon/delivery_nonce_test.go` `TestDeliveryJournal_AcceptsTheNonceTheHookClientMints`
takes the label from `ipc.NewDeliveryNonce` and hands it to the real journal.

Compatibility: previously spooled lines carrying 32-character nonces remain what they were —
unleased gaps — exactly as before the fix. Wire cost is +32 bytes per request, within the frame
budget.

## Old → new assertion map (historical §4.3 text)

| Historical expectation | Disposition |
|---|---|
| "Observe 40 tool uses, `flush`, kill the daemon, restart it" | **Corrected.** 6 live + 8 spooled deliveries (enough to leave acknowledged, indexed-but-unacknowledged and untouched records at once). No `flush` before the interruption — a flush drains and persists offsets, which would hide the very interruption the row exists to test; `flush` runs after the restart instead. "Kill" is a cancelled drain context plus a `Stop` with an exhausted drain budget (Windows staleness, above); "restart" is a second real daemon over the same tree. |
| "`tools/call expand` by `tool_use_id` for the 1st, 20th and 40th records; all return content" | **Retired here.** Retrieval-by-handle is row 4.2's criterion. What this row keeps of it: every acknowledged delivery resolves in the restarted index and its sidecar's verified reference names that record's root — the join a retrieval would follow. |
| "`tools/call re_read {path, at: turn:12}` equals the version stored at or before turn 12; `source` is `store` or `worktree`" | **Retired here.** `re_read` exists (`mcp.ToolReRead`, `ReReadArgs{Path, At, Full}`, `internal/mcp/handlers_span.go`) and resolves a version from captured file history, but it is retrieval and belongs to row 4.2's criterion; the `worktree` substitution half is retired as unsafe by the plan (row 4.2: "no historical/current substitution"). Not asserted on this row. |
| "the superseded first read is still `StatusSuperseded` in `tool_use.jsonl`" | **Retired here** (already held by `TestE2E_SupersessionVisibleAfterRestart`). Replaced by the append-only journal prefixes, the unchanged identities and the exact index line count across the restart. |
| — (new, from the current criterion) | **Added.** Explicit gap recording: `DrainGapState` kinds/`PendingBytes`/`Complete`, the `drain_file_error` counter, the flush recovery marker, and the not-found answer for a lost record. |

## Unverified remainder

- A real process kill of a spawned daemon is not exercised (90 s lock staleness on Windows). The
  in-process `Stop` with an exhausted drain budget is the shipped shutdown path, but process death
  between an fsync and a rename is not modelled here.
- A `SessionEnd` recovery marker *persisting* across a restart is not asserted: the flush's own drain
  pass sees a released spool once a lost line's offset has advanced, so only the cleared case is
  observable through real producers on this tree.
- Observations for the SP-20 owner, not asserted: (a) a `Drain` cancelled before it visits any file
  publishes `Observed:true, Complete:true` from the previously persisted state (`drain.go`, the
  `ctx.Err()` break before `drainFile`); the flush route is unaffected because it returns on the
  error first. (b) `PendingBytes` counts only files the pass visited, not files after the break.
  (c) `drain_file_error` is shared between corrupt lines and per-file errors.
- `devtool fmt-check` lists `internal/commands/envelope_test.go` as not gofumpt-formatted on the
  base tree; it is untouched by this work and left to the coordinator's lint pass.

## Run command and result

```
cd <worktree> && go test ./test/e2e -list '^TestV5_HookEventToTombstoneToRetrievalAfterRestart$'
  → TestV5_HookEventToTombstoneToRetrievalAfterRestart
cd <worktree> && go test ./test/e2e -run '^TestV5_HookEventToTombstoneToRetrievalAfterRestart$' -count=1
  → PASS twice on the committed file (8.0 s and 8.0 s; the subtest ~2.2 s), after two earlier
    passes (10.9 s, 11.8 s). One intermediate iteration failed in phase 2 with 9 spool lines
    instead of 8: a phase-1 hook had missed its ACK deadline under load, spooled the same delivery
    a second time under the same nonce, and the phase-1 wait ended while the drain had refused that
    duplicate once ("delivery still in progress"). Fixed in the test: the wait also requires an
    empty client spool, and per-phase spool counts filter by the phase's id prefix. That is host
    load surfacing the real client fallback, not a producer defect.
cd <worktree> && go test ./internal/daemon -run '^TestDeliveryJournal_AcceptsTheNonceTheHookClientMints$' -count=1
  → PASS
cd <worktree> && go test ./internal/ipc -run 'Nonce' -count=1   → PASS (4 tests)
cd <worktree> && go test ./internal/cli -run '^TestHookCapture_DeliveryNonceAndCaptureReachTheSpooledRequest$' -count=1
  → PASS
gofmt -l ./test ./internal → clean; go vet ./test/e2e ./test/integration ./internal/ipc ./internal/daemon ./internal/cli → clean
go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns → PASS
```

# V6 remediation — SP08-D3 residuals: prompt ordering + derived-ID publication (design + regressions)

**Owner:** V6 prompt-order author (Opus 4.8, high). **Nature:** READ-ONLY — design + regression
authoring only. No production edits. New test files authored:
`internal/daemon/prompt_order_v6_test.go`, `internal/observer/derived_publication_v6_test.go`, and
this doc. I did not read the other author's in-flight `delivery_lease.go`/`delivery_seal.go`/
`delivery_archive`/rollover designs as contract; I use only the already-shipped `deliveryLease`
fields (`ArrivalSeq`, `ObservationID`) that `publishCapture` (`ingest.go:919-938`) consumes today.

Main's shipped fixes (confirmed via `prompt_recovery.go`, `publication_sync.go`, `prompt.go`,
`prompt_delivery.go`, and `prompt-critical-review-followup.md`) close the STALE-TURN, index-before-
link (prompt path), default-warning-API and fsync-ordering defects. The follow-up review confirms
these but explicitly does not prove ORDERING. Two residuals remain; both are pinned below with the
real store / real lease / real sidecar, actual subprocess exit status (no pipeline masking).

## Concrete test outcomes (run after `dist/v6-remediation/parallel-tests-ready`)

- `internal/daemon` `TestPromptOrder_V6_*` — **exit 0 (PASS)**: both tests confirm the current
  (wrong) behaviour. `dist/v6-remediation/prompt-order-daemon.log`.
- `internal/observer` `TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart` — **exit 1
  (FAIL)** at the final assertion (line 77): `subagent_<s>_1` exists, i.e. the redelivery duplicated.
  `dist/v6-remediation/prompt-order-derived-observer.log`. This is a failing regression: it flips
  GREEN when the fix lands.

## Issue 1 — concurrent same-session publication order (turn 0 ≠ host original)

**Failure cut (pinned, GREEN).** Two same-session prompts leased in host order — P0 = arrival N
("first"), P1 = arrival N+1 ("second") — but PUBLISHED worker-side in reverse
(`runIngested(P1)` before `runIngested(P0)`) yield `prompt_<s>_0 == "second"` and
`prompt_<s>_1 == "first"`. The test asserts `ArrivalSeq(P0) < ArrivalSeq(P1)` to make the inversion
concrete.

**Root cause.** The turn is assigned by PUBLICATION order — whoever acquires `st.mu` and records
first. `PromptFrontier` supplies "the next free turn" but has no notion of arrival order, and `st.mu`
serialises to prevent turn COLLISION, not INVERSION (review §2 confirms only collision-safety). So a
later leased arrival published first takes turn 0 — the id the rehydrator serves as the verbatim
original.

**Smallest compatible fix — assess main's fail-closed-dispatch candidate (recommended).** Refuse to
publish arrival A of session S while the immediately-prior arrival (A−1) of S is leased but not yet
acknowledged; nonblocking (return non-OK, leave the WAL pending), with retry scheduling.
- **Layer.** The DISPATCH layer (`ingest.dispatch`, and the drain's `Dispatch`), not `runIngested`
  (which is shared and below the gate — the regression is driven there only to isolate the hazard).
- **Exact source needs** (main owns): (a) a per-session "next-expected-publish arrival" query on the
  delivery journal (it already keys arrivals by session — `arrivals[session]`); (b) a gate before
  `run`: `if lease.ArrivalSeq != nextExpectedPublish(session) { retain WAL; count; schedule retry }`;
  (c) bounded, nonblocking retry via the existing WAL/spool re-drain (no in-memory queue), with a
  backoff so a wedged prior arrival cannot spin a worker.
- **Bounded memory.** One small integer per active session (same order as the existing arrival map);
  retries ride the WAL. No unbounded growth.
- **Stale turns.** With in-arrival-order publication, `PromptFrontier`'s `next` equals arrival order,
  so `prompt_<s>_0` is the earliest arrival — no `PromptFrontier` change needed.
- **Competing reserved derived IDs.** N/A for prompts (`VerbatimPromptID` is turn-derived; in-order
  publication makes turn == arrival order).

**Separate, UNPROVABLE coverage boundary (pinned, GREEN —
`TestPromptOrder_V6_UnleasedEarlierSpooledIsSeparateUncertainty`).** An earlier prompt that was
SPOOLED (client fallback) but not yet leased is invisible to the daemon; a later LIVE prompt leased
and published first legitimately takes turn 0. No per-arrival gate helps — the earlier delivery has
no arrival sequence yet — and host order cannot be inferred from worker order. **Guarantee to claim:
in-order among LEASED arrivals only; NOT "the host-first prompt is universally the original."** The
fail-closed fix must not be described as closing this case.

## Issue 2 — derived tool/stop index-before-link duplicate after restart (inherited SP08-D2 residual)

**Failure cut (pinned, RED).** `captureSubagent` writes the index record `subagent_<s>_0` and then
links the sidecar SOFT (`stop.go:281`). A crash between `RecordToolUse` and `linkObservation` leaves
the record present with no Published reference. On restart (fresh observer, turn counter 0) the SAME
leased identity is redelivered: `observationRecord` returns false (Published never set), and
`freeDerivedTurn(0)` finds turn 0 occupied by the cut record and mints `subagent_<s>_1` — a SECOND
capture for one delivery. (The tool-no-id path `tu_<s>_<turn>_<n>` has the identical shape.)

**Root cause.** The derived tool/stop path recognises a redelivery ONLY via the sidecar's Published
flag; unlike the prompt path it has no cross-restart identity recovery (no `PromptFrontier`
equivalent, no obs-bound recovery), so an interrupted derived publication is unrecoverable and
`freeDerivedTurn`'s collision-avoidance converts the cut record into a duplicate.

**Smallest compatible fix — assess main's versioned-publication-intent candidate (recommended).** A
new durable, VERSIONED publication-intent record for derived IDs, written BEFORE the index record and
recognised on redelivery, keyed by `ObservationID`.
- **Why not overload `ArgsDigest`** (as the prompt path does): a derived tool/stop `ArgsDigest` is
  genuine tool-argument content (`subagentArgs`/tool args); binding obs into it corrupts that
  semantic (review H1 already flags the prompt overload as a documented exception — widening it to
  tools is worse) and `PromptFrontier`-style digest scanning is O(index) on the hot path.
- **Why not overload the Published sidecar fields**: `Published`/`ToolUseID`/`Root` are the
  reference-AFTER-record join; the intent is needed BEFORE the record (to reserve the derived id), a
  distinct lifecycle stage. Overloading `Published` conflates "reserved" with "published."
- **Exact source needs** (main owns): (a) a store capability to reserve/read a derived publication
  intent keyed by obs — written and fsync-ordered (`SyncPublication`-style) before `RecordToolUse`;
  versioned/additive, fail-closed when absent for leased derived captures (as `PromptRecovery`/
  `PublicationSync` already are); (b) the observer derived-id paths (`captureSubagent`, tool-no-id in
  `tooluse.go`) look up the intent by obs BEFORE `freeDerivedTurn` — if present, adopt its reserved
  id/turn (recover the cut) instead of minting; the existing `linkObservation` then completes
  publication and the intent is superseded by the Published sidecar.
- **Bounded memory.** One durable intent per leased derived delivery, keyed by obs, cleared/superseded
  once Published; a bounded scan fails closed to unavailable (mirror `PromptFrontier`'s `1<<18`).
- **Competing reserved derived IDs.** Two concurrent derived captures of one session must each reserve
  their OWN turn: the intent must durably advance a per-session derived-turn frontier, so a reserved-
  but-unpublished turn reads as OCCUPIED to a later capture AND to a restart's `freeDerivedTurn`
  (otherwise two restarts both reserve turn 0). The intent therefore doubles as the derived analogue
  of `PromptFrontier`. `PromptFrontier` already counts `SubagentStop` turns
  (`prompt_recovery.go:42-44`), so a `DerivedFrontier` can reuse that turn reconciliation; reconciling
  `st.Turn` from the durable index before minting closes the stale-turn half, the intent closes the
  interrupted-publication half.

**Exactly-once caveat.** As with prompts, claim only to tested cuts. The intent narrows the window to
intent→index→link; a crash between intent and index (or before the intent's own fsync) must fail
closed — re-mint from the reconciled frontier, absorbed by the store's append-only dedup on identical
id+root. Do NOT claim universal exactly-once.

## Implementation status

Design only. No production edits; implementation awaits main's decision on (1) the fail-closed
dispatch gate + retry and (2) the versioned publication-intent record. No release signoff. The two
regressions are the acceptance criteria: issue 1's `..._LaterLeasedArrivalPublishedFirstTakesTurnZero`
must flip to `"first"` at turn 0, and issue 2's duplicate assertion must go GREEN, when the fixes land.

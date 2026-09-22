# V6 remediation — SP08-D3 replayed-prompt capture (implementation record)

**Owner:** V6 prompt-replay author (Opus 4.8, high). **Status:** IMPLEMENTED — Option A, per main's
decision. No new persisted schema or frozen interface; existing sidecar joins only.

## 1. Defect

A prompt reaching the daemon only by WAL/spool replay was never verbatim-captured: `runIngested`'s
prompt arm ran only the sentinel scan, the delivery was acknowledged and its spool copy deleted, the
observer's turn counter never advanced, and the next LIVE prompt was recorded as `prompt_<s>_0` — the
id the rehydrator serves as the verbatim original.

## 2. Design — Option A (worker is the sole authoritative recorder)

The verbatim capture is done by the WORKER and the DRAIN REPLAY, under the leased observation
identity, exactly as a tool result's is. The live reply path keeps ONLY the synchronous thrash
warning. The split is a context marker, not a second Services seam:

- **`internal/observer/prompt_delivery.go` (NEW):** `WithPromptReplyOnly(ctx)` marks the live reply
  path; `opObservePrompt = "observe.prompt"` (the sidecar `Op` for a prompt); `promptReplyOutput`
  drains the pending warning under the session lock and records nothing; `recordPromptDurable` writes
  the index record + observation-reference link + DAG node and, for a LEASED delivery, returns
  `ErrUnpublished` on a lost required write.
- **`internal/observer/prompt.go`:** `onUserPrompt` now has two paths. Reply-only → drain the warning,
  no record, no turn advance. Worker/replay (normal ctx, carrying `WithObservation`) → recognize a
  redelivery via `observationRecord`/`adoptTurn` (the SP08-D2 join, now reached for prompts) and
  absorb it; else PutBytes → `recordPromptDurable` → link → close the turn. A leased lost capture
  propagates `ErrUnpublished`; an unleased in-process caller keeps the soft, turn-advancing base
  behaviour. The old fused `recordPrompt` was removed; `pendingThrashLines` now labels the warning
  with `st.Turn+1` (the turn the session advances into) so the rendered line is byte-identical to the
  pre-Option-A post-increment label (SP-15).
- **`internal/daemon/handlers.go`:** `handleObservePrompt`'s reply capture is called with
  `observer.WithPromptReplyOnly(rec)` (warning only). `recordHotPathSample`/SP20-D6 untouched; the
  scope/admission path untouched.
- **`internal/daemon/daemon.go`:** `runIngested`'s prompt arm now runs the authoritative capture —
  `scanSentinelForPrompt` (kept) then `ObservePrompt(ctx, ev)` under the leased identity ctx carries,
  discarding the Output (no late injection). A capture that is not durable returns non-OK, which
  `ingest.dispatch`/`drain` leave un-acknowledged (delivery stays pending). `drainDispatch` no longer
  increments the loss counter.

**Frontier ACK** still follows capture/reference/link success (`ingest.dispatch` gates on the
handler's OK; `commitDelivery` is stage 3). Prompt durability is therefore ASYNCHRONOUS after the
durable WAL ACK, exactly as tool captures already are.

## 3. Tests (focused only; logs in dist/v6-remediation/prompt-*.log; GOMAXPROCS=2)

Owned NEW / inverted:
- `internal/daemon/drain_prompt_capture_test.go` — inverted the SP08-D3 evidence test (original id
  `TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured` → `..._ReplayedPromptIsCapturedAtTurnZero`;
  mapping kept in a file comment, CARRIED-DEFECTS.tsv is main-owned): a replayed prompt IS captured
  at turn 0, a later live prompt stays at turn 1 (no substitution), no third turn / no double count.
  Plus `TestSP08D3_DistinctSameTextRepliesStayDistinct` (identical text ⇒ distinct turns, no content
  dedup), `TestSP08D3_CaptureFailureIsNotAcknowledged` (a closed store ⇒ not frontier-acked, spool
  retained), `TestSP08D3_ReplayThroughRunIngestedEmitsNoOutput` (no late injection).
- `internal/observer/prompt_delivery_test.go` — reply-only records nothing; worker records + advances
  turn; a leased Put failure returns `ErrUnpublished` and does not advance; an unleased failure stays
  soft and advances; empty prompt is not a turn; `submitReply` helper.

Test-only edits OUTSIDE the stated exclusive set, made because Option A moves warning/record off the
reply path (flagged for main; test-only, trivially revertible):
- `internal/observer/prompt_test.go` — two thrash-warning tests re-pointed to the reply-only path
  (`submitReply`); they assert the byte-identical warning.
- `internal/daemon/prompt_record_test.go` — two tests whose assertions encoded the pre-Option-A
  "reply records / replay is sentinel-only" model:
  (a) `TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock` now drains the ring after
  the lock is released (the capture is the worker's under Option A); the reply-late assertion still
  holds because reply-only also takes the session lock.
  (b) `TestObservePrompt_RecordingOutlivesTheReplyDeadline` — trimmed the obsolete tail asserting a
  replay "must not call the seam again" (Option A makes the worker/replay the recorder, so it DOES
  call the seam; the fake held-seam rig has no sidecar and would block on the second call). "No
  double recording" is now pinned via the observation-identity join by the SP08-D3 daemon test. The
  reply-lifecycle assertions are unchanged. The other seven `TestObservePrompt_*` machinery tests
  pass unchanged.

## 4. Limitations (reported honestly; no claim of exactly-once beyond tested cuts)

- **Turn-zero-original ordering.** The turn is assigned by the observer's session counter under the
  session lock, in the order captures acquire it — i.e. worker/drain PROCESSING order. For spooled
  replay the drain processes lines in spool/WAL order, so turn 0 is the first spooled prompt (pinned).
  For two prompts of one session in flight CONCURRENTLY through the ingest worker pool, lock order is
  processing order, which is not provably submission order. Normal operation serialises a session's
  prompts (submit → await → submit), so this does not arise in practice, but it is NOT proven in
  general. The code records whatever is processed first as turn 0; it never inspects text to pick an
  "original", so it cannot silently promote later text — but under a genuinely out-of-order concurrent
  burst the turn numbers would follow processing order.
- **Crash window between the index record and its observation-reference link.** A process killed
  between `RecordToolUse` and `linkObservation` leaves a record with no `Published` reference; a
  redelivery is then NOT recognized by the sidecar and re-runs the record write, which the store's
  append-only dedup absorbs (same id, same root) before the link is completed. A fresh observer
  re-derives the same turn (Turn=0), so no duplicate turn results. Exactly-once therefore holds only
  up to the cuts the tests exercise, not in general.
- **Unleased / in-process deliveries** (no nonce, or a caller with no daemon) use worker-only capture
  with no cross-restart identity guarantee and keep the soft, turn-advancing failure behaviour;
  there is no content-based dedup, so distinct same-text deliveries stay distinct.
- **`counterPromptReplayedUncaptured`** is retired to zero on captured replays (the name is kept for
  dashboard/compat). Genuine non-durable captures are surfaced as non-ack (drain gap/error), which
  the existing gap/counter machinery records; empty/degraded input is not relabelled as a lost prompt.

## 5. Not touched / preserved

SP20-D6 (handler duration in the gated estimate, `recordHotPathSample`), the scope/admission gate,
`ingest.Accept`/drainer nonce-with-unavailable-journal behaviour, and the duplicate seal-residual
reporters are all left to main. No `.claude`/config/auth changes.


## Coordinator correction, 2026-09-21

The author's initial pass and crash-recovery claims above are superseded here.
A pipeline masked a 150-second Go timeout; its failed output is preserved in
`runs/prompt-author-record-timeout.log`. Earlier scratch logs were overwritten by
the author, so they cannot establish a complete failure history. The coordinator
uses direct subprocess exit status in the new run records.

`prompt-restart-negative-control` failed both fresh-delivery cases after an
unpersisted restart: equal text was conflated; different text remained unpublished.
The implementation now binds a leased prompt's synthetic argument digest to its
observation ID and recovers the durable turn frontier through an additive bounded
store capability. This retains prompt IDs and the frozen record wire shape.
An index-before-sidecar cut repairs the original record instead of guessing a turn.
Old unlinked records without the binding cannot prove which delivery produced them.

Prompt publication now verifies and flushes the content/recovery object closure,
then root/tool-use indices, before linking and acknowledging publication. Unsupported
store capabilities and missing objects remain unpublished. This is an explicit
filesystem sync protocol, not evidence of power-loss behavior on every platform.
Other tool/stop publication paths retain their separately qualified recovery limits.

The default observer API retains warning output; worker capture is explicitly
marked and cannot consume warnings. Warning labels are fixed when queued, so worker
and reply scheduling agree. WAL/lease rejection now returns a transport NAK with
empty hook output so the client can retain its fallback.

Parent-run evidence: `prompt-recovery-and-lease-focused`,
`prompt-publication-sync-contracts`, and `prompt-acceptance-and-structural-guards`
passed. These are focused working-tree checks, not installed-host or release proof.
The observer tests include multi-turn equal-text restart with a missing publication
link, and refuse acknowledgement of a published reference whose object is missing.
Concurrent same-session processing order remains a distinct integration obligation.

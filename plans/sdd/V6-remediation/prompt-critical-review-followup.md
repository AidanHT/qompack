# V6 remediation — prompt replay follow-up review (corrections to F1 + durable-ACK)

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** read-only source/evidence review — no tests/builds/benchmarks, no edits except this
report, no config/auth/billing/permission changes. Test success is NOT inferred from the parent's
shell pipelines; I did not run anything. No release signoff. Supersedes nothing — prior artifacts
(`prompt-critical-review.md`, etc.) preserved.

**Read:** `internal/store/prompt_recovery.go`, `internal/store/publication_sync.go`,
`internal/observer/prompt.go`, `internal/observer/prompt_delivery.go`, `internal/observer/observer.go`
(`WarningTurn`), `internal/daemon/daemon.go` (`runIngested` prompt arm, `WithPromptCaptureOnly`), plus
`ArgsDigest` consumers across `internal/`.

---

## 1. Verdict

The two defects I raised are addressed, and the correction main flagged in my prior review is real:

- **My earlier "durable-ACK is sound" was too strong.** The base index append did not fsync the
  objects or the index, so the frontier ACK could be committed over page-cache-only data. The new
  **`SyncPublication` inserts that fsync** (object closure → root/tool-use indices → Index dir) before
  `linkObservation` and the ACK, in correct referent-before-reference order. Now durable **up to
  `File.Sync`/`SyncDir`**, and the code qualifies power-loss to exactly those primitives (§3).
- **F1 (stale-turn id reuse after crash/restart) is fixed** by `PromptFrontier` turn reconciliation
  from the durable index (forward-only) plus an observation-bound `ArgsDigest` that makes the
  index-before-link cut recoverable across a restart. Both the stall (distinct text) and the
  same-text conflation are closed (§2).
- **The prior cosmetic warning off-by-one is fixed** by `WarningTurn`, and the default observer
  warning API is restored via `WithPromptCaptureOnly` (§4).

Four residual findings (H1–H4), all low, none blocking; H1 warrants a doc/consumer check. Exactly-once
remains claimed only to tested cuts; V6 is not cleared.

---

## 2. F1 is fixed — mechanism confirmed

On the worker path (`obs != ""`), before minting an id, `recoverPrompt`
(`prompt_delivery.go:71-98`) calls `PromptFrontier` (`prompt_recovery.go:19-56`):

- **Turn reconciliation.** `PromptFrontier` computes `next` as the max session turn in `s.toolUse`
  (+1 for a `UserPromptSubmit`/`SubagentStop` record) and `recoverPrompt` sets `st.Turn = next` when
  `next > st.Turn` (forward-only). `s.toolUse` is the **durable index** loaded on store Open — the
  crash-independent source of truth, not the idle-only `observer.json` that caused F1. A fresh
  distinct prompt after a stale-state restart therefore mints `prompt_<session>_<trueTurn>`, which
  cannot collide → **the stall is closed.**
- **Cross-restart cut recovery.** The index record's `ArgsDigest` is now
  `promptDeliveryDigest(prompt, obs)` = digest of `{prompt, observation_id}`
  (`prompt_delivery.go:50-61,152-156`). `PromptFrontier` returns the record whose digest matches; if a
  prior run wrote the index record but crashed before the sidecar link, the redelivery finds that
  record by its obs-bound digest, `SyncPublication`s it, and completes `linkObservation`
  (`prompt_delivery.go:86-97`). Because the digest binds the delivery identity, this works even after
  several turns and loss of `observer.json` — the gap I raised.
- **Same-text conflation closed.** Two identical-text deliveries have **different** obs, hence
  different `promptDeliveryDigest`, so a new delivery's digest never matches an earlier record; and
  the reconciled turn gives it a distinct `prompt_<session>_<turn>` id. The old same-id/same-root
  silent absorb can no longer occur.
- **`adoptTurn` alone is no longer relied on** for a never-seen delivery: reconciliation happens
  before minting regardless of recognition. Correct.

Concurrency is safe: both `onUserPrompt` paths serialize on `st.mu`, so `PromptFrontier` sees the
prior delivery's committed record / advanced `st.Turn` before the next mints (`prompt.go:88-90,
102-148`).

Edge behaviours checked and acceptable: forward-only bump never lowers a legitimately-ahead
`st.Turn`; a tool-only max turn mints at that turn (a prompt shares its turn with that turn's tools);
an empty session recovers to turn 0.

---

## 3. Durable-ACK correction — `SyncPublication` (my prior conclusion revised)

`recordPromptDurable` now orders: `RecordToolUse` → **`syncPrompt`** → `linkObservation`
(`prompt_delivery.go:159-177`). `SyncPublication` (`publication_sync.go:58-88`):

- walks the root's object closure (chunks + `Deltas`/`Orig`/`Base`), bounded (`>4096` roots or
  `>65536` objects → `ErrDegraded`, fail closed);
- for each object: `GetChunk` (integrity read), then `syncPublicationObject` opens `O_RDWR`, guards a
  swap with `os.SameFile`, `File.Sync`s (Windows FlushFileBuffers needs a write handle), and
  `SyncDir`s the directory chain up to `Objects`;
- then `Sync`s the root and tool-use index writers and `SyncDir(Index)`.

Ordering is **objects-durable-before-index-durable**, which is the correct crash-consistency
direction (an index record that survives has its referents durable). The frontier ACK
(`commitDelivery`) fires only when `runIngested` returns OK, which requires `syncPrompt` +
`linkObservation` to have succeeded — so the ACK now follows a genuine fsync of the capture closure,
which the base append did not provide. Power-loss is correctly qualified to `File.Sync`/`SyncDir`
(`publication_sync.go:57`), not over-claimed.

Missing capability fails closed: a store without `PublicationSync` (or `PromptRecovery`) makes
`syncPrompt`/`recoverPrompt` return `unpublished` (`prompt_delivery.go:63-82`), so a leased prompt is
never ACKed over an unsynced capture. Sound, with the coupling noted at H4.

---

## 4. Warnings — fixed, default API preserved

`WarningTurn` is fixed when the pending-thrash queue first becomes nonempty
(`prompt.go:241-242`, `st.WarningTurn = st.Turn + 1`) and the reply path renders at that turn
(`prompt_delivery.go:121`, `pendingThrashAt(st, st.WarningTurn)`). This decouples the label from
worker/reply scheduling, resolving the prior cosmetic off-by-one. The direct/default API keeps its old
behaviour: `pendingThrashLines` uses `st.Turn` (`prompt.go:257-259`), and a non-worker caller returns
the warning (`prompt.go:160-162`); only the explicitly `WithPromptCaptureOnly` worker leaves it queued
(`daemon.go:857`). Warning behaviour is preserved.

---

## 5. Residual findings (all low)

- **H1 — `ArgsDigest` semantic overload for prompts (low; doc + consumer check).** A prompt's
  `ArgsDigest` is now the digest of `{prompt, observation_id}`, diverging from the field's own doc
  ("the domain-separated digest of the tool's arguments", `store/tooluse.go:39`) and from its
  `ArgsPreview`, which stays prompt-only (`prompt_delivery.go:152`). Two identical-text prompts now
  carry different `ArgsDigest`. No functional consumer breaks — content dedup is by root, and the only
  `ArgsDigest` matcher is `PromptFrontier` (`prompt_recovery.go:48`); `tooluseindex.go` stores/loads it
  verbatim and nothing recomputes digest-from-preview. *Required correction:* update the `ArgsDigest`
  doc to record the prompt exception, and confirm no args-equality/inventory consumer treats prompt
  `ArgsDigest` as content-derived. Wire shape is unchanged (still `core.Hash`).
- **H2 — legacy unlinked cut records are not recovered (disclosed, bounded).** A pre-upgrade cut
  record has a non-obs digest, so `PromptFrontier` will not match it; `recoverPrompt` returns
  `recovered=false` and a **fresh** record is written at the reconciled turn — a duplicate prompt
  record for that one legacy delivery (not a loss, stall, or conflation). The code discloses this
  (`prompt_delivery.go:141-143`). Acceptable for a forward-only fix; no correction beyond the
  disclosure, but it should be listed among the "not exactly-once" carve-outs, not folded into the
  general claim.
- **H3 — duplicate-digest → `ErrDegraded` poison-pill (low).** If two records ever share the obs-bound
  digest, `PromptFrontier` returns `ErrDegraded` (`prompt_recovery.go:49-51`) → `recoverPrompt`
  unpublished → the delivery re-drains and re-fails indefinitely. Should not arise (digest unique per
  delivery), but a pre-existing duplicate would wedge that delivery. Consider treating the ambiguous
  case as "record at max turn, do not recover" rather than an indefinite stall, or bounding retries.
- **H4 — optional interfaces are effectively mandatory for leased prompts (note).** `PublicationSync`
  and `PromptRecovery` are declared additive/optional, but `syncPrompt`/`recoverPrompt` fail closed to
  `unpublished` when absent, so any leased prompt on a store lacking them can never publish. Intended
  (production `FSStore` implements both); document that a store used for leased prompt capture MUST
  implement both, so "optional" is not read as "degradable."
- **Minor:** the recognition path now re-runs `SyncPublication` on every recognized redelivery of an
  already-durable record (`prompt.go:104`) — redundant I/O, harmless; and `syncPrompt` collapses
  "capability absent" and "sync failed" into the same `stageIndex` counter, losing that distinction
  for diagnostics.

---

## 6. Non-acceptance

Read-only review; no gate cleared, no signoff. F1's stall and same-text conflation are closed by
durable-index turn reconciliation plus the obs-bound digest; the durable-ACK gap I previously
understated is closed by `SyncPublication`, durable up to `File.Sync`/`SyncDir` with power-loss
correctly qualified; warnings and the default API are preserved. H1 needs a doc/consumer check; H2 is
a disclosed legacy-cut duplicate to keep out of any exactly-once claim; H3/H4 are low-severity
fail-closed edges. Exactly-once remains claimed only to the tested cuts, and I did not verify the
parent's test run — no result there is treated as evidence.

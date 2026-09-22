# V6 remediation — prompt replay (SP08-D3) critical review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** source review only — no source/test edits, no tests/builds/benchmarks, no
config/auth/billing/permission changes. Only output: this file. Author files were not modified.
The author's focused checks are still running (a broader prompt-only check hung); no in-flight test
result is treated as final evidence here.

**Read:** `internal/observer/prompt.go`, `internal/observer/prompt_delivery.go`,
`internal/observer/state.go` (turn persistence), `internal/observer/identity.go` (recognition /
adoptTurn / freeDerivedTurn), `internal/daemon/daemon.go` (`runIngested` prompt arm, `drainDispatch`),
`internal/daemon/handlers.go` (`handleObservePrompt`, `callObservePromptWithDeadline`,
`startPromptRecording`). Prior reviews in this directory preserved unchanged.

---

## 1. Verdict

The Option-A split (live reply = warning only; worker/replay = the authoritative verbatim capture
under the leased observation identity) is well-constructed and the durable-ACK ordering is sound.
**One release-relevant defect is confirmed and matches main's suspicion (F1):** after a crash/restart
where `observer.json` is stale, a genuinely-new prompt delivery mints `prompt_<session>_<staleTurn>`
and either **stalls the session's frontier** (distinct text) or is **silently conflated and dropped**
(same text). `adoptTurn` does not cover it, and the tool path's `freeDerivedTurn` cannot be
transplanted verbatim because the prompt id encodes a semantically load-bearing turn. Nothing here
clears V6 and no exactly-once claim beyond the tested cuts is endorsed.

---

## 2. What is sound (confirmed in code)

- **Live/worker split preserves the warning path.** `handleObservePrompt` →
  `callObservePromptWithDeadline` runs `ObservePrompt(WithPromptReplyOnly(rec), e)` — reply-only:
  drains `PendingThrash` under `st.mu`, returns it (ModeFull only), records nothing, advances no turn
  (`prompt.go:92-95`, `prompt_delivery.go:50-62`). The authoritative capture is the WAL-accepted
  worker job / drain replay reaching `runIngested`'s prompt arm under `WithObservation`
  (`daemon.go:846-862`). No double capture. Warning label uses `st.Turn+1` to stay byte-identical to
  the pinned SP-15 wording (`prompt.go:244-259`). *"Normal OnUserPrompt warning behavior" is
  preserved.*
- **Durable recording / ACK gating is correct.** A leased delivery whose `PutBytes`,
  `RecordToolUse`, or `linkObservation` fails returns `ErrUnpublished` (`prompt.go:122-124`,
  `prompt_delivery.go:95-108`); `ingest.dispatch`/`drain` leave it un-acknowledged so the frontier is
  never committed over a capture that did not become durable. An unleased in-process caller keeps the
  base soft-failure behaviour. Sound.
- **Observation identity is durable across restart.** `obs` is the journal lease's
  `H(session‖arrival)`, recovered identically on WAL replay, so a *published* prompt's redelivery is
  recognized by `observationRecord` (sidecar `Published` + `ToolUseID` + `Root` re-confirmed against
  the index, `identity.go:107-125`) across a restart, and absorbed.
- **Index→sidecar cut recovery (same process) is sound.** Record then link; a cut leaves an
  unpublished record; the redelivery re-runs the record (append-only dedup absorbs same-id/same-root)
  and completes the link (`prompt_delivery.go:74-108`). The doc correctly limits exactly-once to
  tested cuts.
- **Sidecar fields are not repurposed as reservations.** The prompt path uses the same
  publish-join (`Published`/`ToolUseID`/`Root`) the tool path uses; it adds no reservation semantics.
  Good — do not let the F1 fix change that.

---

## 3. F1 — Confirmed defect: stale-turn id reuse for a never-seen prompt after crash/restart

**Severity:** Medium–High. Stall variant halts the session's spool-tail frontier; conflation variant
is a silent G2.3 distinct-prompt loss. Reachable interleaving, not a clean-restart case.

**Root cause.** `st.Turn` is restored only from `observer.json`, which is written **only by idle
`Persist`** (`state.go:169-204,247-254`), never per turn. The frontier ACK (`commitDelivery`) is
independent of that idle write, so a crash can leave turns advanced-and-acknowledged but **not
state-persisted** — on restart `st.Turn` resumes behind the true frontier.

**Path.** On the worker/replay path a genuinely-new delivery (fresh `obs`, sidecar not `Published`)
is **not** recognized by `observationRecord`, so it takes the fresh-capture branch and mints
`VerbatimPromptID(session, st.Turn)` = `prompt_<session>_<staleTurn>` (`prompt_delivery.go:84`).
`adoptTurn` fires **only** on the recognized-redelivery branch (`prompt.go:103-107`), so — exactly as
main warned — it does not rescue a never-seen delivery whose turn counter was reset below the index.

**Two outcomes, both bad:**

- **Distinct text (root differs) → stall.** `RecordToolUse(prompt_S_k, newRoot)` against an existing
  `prompt_S_k` with a different root is `ErrAppendOnly` → (leased) `ErrUnpublished`
  (`prompt_delivery.go:95-101`) → not acknowledged → re-drained → re-collides every pass. This is
  precisely the "same id, different root … drain retries that line forever with the rest of the spool
  file stuck behind it" hazard the tool path documents and defends against with `freeDerivedTurn`
  (`tooluse.go`/`identity.go:185-200`). **The prompt path has no equivalent guard.**
- **Same text (root identical) → silent conflation/drop.** `RecordToolUse(prompt_S_k, sameRoot)`
  dedups **successfully and silently** (same id, same root), then `linkObservation(newObs, {prompt_S_k,
  root})` stamps the *new* delivery's sidecar onto the *earlier* prompt's record. The distinct new
  delivery is recorded as the old turn, its position lost, and the frontier is acknowledged as
  "captured." A collision-detection-only fix does **not** catch this (there is no error).

**Cross-restart, this also breaks the cut recovery of §2:** that recovery assumes the redelivery
re-mints the *same* id, which holds only while `st.Turn` is stable. After a restart with stale
`st.Turn`, the re-run mints a *different* turn id — orphaning the pre-cut record and writing a
duplicate (distinct text) or conflating (same text).

**Why the tool fix doesn't transplant.** The tool derived id encodes a *window position* that may be
probed to any free slot; the prompt id encodes the *turn*, which numbers `dag.UserPromptNode(turn)`,
segment bookkeeping and `LastPromptTurn`. Probing to an arbitrary free turn would corrupt turn
semantics, so the fix must recover the **true** turn, not dodge to a free one.

---

## 4. Recommended compatible correction (main owns the decision)

The `VerbatimPromptID` shape `prompt_<session>_<turn>` is frozen and reached by retrieval/replay, so
keep it and correct the *turn input* instead:

1. **Reconcile `st.Turn` to a durable frontier before minting a fresh prompt id** — a source that
   survives a crash independent of idle `Persist`. Candidates: the store index (highest existing
   `prompt_<session>_<t>` and tool-turn for the session, +1), or the committed delivery frontier. A
   fresh delivery then always mints an unused turn, which fixes **both** the stall and the same-text
   conflation at once. This is the clean root fix and touches no frozen contract.
2. **If a forward-only collision advance is used as a backstop**, note it fixes only the stall
   (`ErrAppendOnly`), not the same-text conflation — so it is insufficient alone and must accompany
   (1).
3. **Do not paper over this by repurposing the sidecar publish fields as a turn reservation** — that
   silently changes the publish-join the tool path shares (the concern main flagged). Recover the
   turn; keep the join semantics.
4. **Guard the same-text absorb explicitly:** a fresh-capture `RecordToolUse` that dedups into a
   record whose sidecar/`obs` is *not this delivery's* must be treated as a collision to advance past,
   not a redelivery to absorb — a safety net even once (1) lands.

Any of these is a behavior/durable-data decision; return the choice to main. Whichever lands should
be pinned by a test that (a) advances turns, (b) simulates a crash with a **stale** `observer.json`
(turns acknowledged, not idle-persisted), (c) delivers a new distinct prompt and a new same-text
prompt, and asserts neither stalls nor is conflated.

---

## 5. Minor / secondary

- **Warning-label turn race (cosmetic).** The reply-only goroutine and the async capture worker both
  contend for `st.mu` in nondeterministic order; if the worker wins first it increments `st.Turn`
  before the reply drains, labeling the thrash warning one turn high. No data loss; a rare
  off-by-one in a warning line. (`prompt.go:138-140` vs `prompt_delivery.go`/`pendingThrashLines`
  `st.Turn+1`.)
- **`counterPromptReplayedUncaptured` retired to zero** but its name kept for dashboards
  (`daemon.go:900-905`); harmless, noted so a reader does not treat a zero as "no replays."

---

## 6. Non-acceptance

Source review only; nothing is marked PASS and no gate is cleared. The Option-A split, durable-ACK
ordering, obs durability and same-process cut recovery are sound; F1 (stale-turn id reuse for a
never-seen prompt after crash/restart) is a confirmed defect whose compatible correction is accurate
turn recovery (§4), not `adoptTurn` and not `freeDerivedTurn`. Exactly-once is not endorsed beyond the
tested cuts, and V6 is not cleared. The author's hung/in-flight checks are not evidence either way;
all prior review artifacts in this directory are preserved.

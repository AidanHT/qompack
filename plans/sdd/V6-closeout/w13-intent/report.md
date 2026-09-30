# Wave 13 — w13-intent (live-lane defects, D45)

Branch `closeout/w13-intent`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `e5712c49`

### Root cause

(1) rehydrate readL0Intent read the L0 capture through an 8,192-byte LimitReader and quoted the prefix as the whole tier-1 unit. The prefix fitted, so no overflow was named, and it never equalled the checkpoint's whole copy, so intent_mismatch was logged Loud every time. (2) user_intent.evolution was filled only by encoding closed segments. A session's segment closes at SessionEnd, so every prompt since the session last ended, corrections included, sits in the open segment that no PreCompact reads. Also, a derived parent from another session seeded the Original, and the cap kept the oldest 64 entries. (3) A fork gets a new session id, so prompt_<fork>_0 is the fork's own first prompt. Nothing recorded the lineage (the host names no parent), so L0-wins overrode the parent's original with it.

### Summary

W13-INTENT REPORT (branch closeout/w13-intent, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-intent, base 84d2512, head e5712c49). All three D45 intent defects are root-caused and fixed, each with a deterministic test that was RED before its fix. No real Claude Code session and no Linux container were used. The coordinator must re-run the live rows (UAT-04/05/06, C4.3) on the fixed candidate.

(1) F-UAT04-1, first prompt cut at 8 KiB
ROOT CAUSE: internal/rehydrate/items.go readL0Intent read the L0 capture through `io.LimitReader(rc, maxIntentBytes)` with `maxIntentBytes = 8192` ("the tail of a paste is not the statement of intent") and quoted that prefix as the whole tier-1 unit.
- The 8 KiB prefix fitted the 9,400-char payload ceiling, so the budget pass admitted it whole. No overflow was named, and the item-2 state row said truncated:false.
- The prefix never equalled the checkpoint's whole copy (the checkpointer reads prompts with an unbounded io.ReadAll). So the `fallback != text` branch logged a spurious intent_mismatch Loud at every compaction. The capture itself was never cut: the observer stores e.Prompt whole.
FIX (7e46cb69):
- readL0First reads the capture whole up to `intentReadLimit = maxUTF8BytesPerHostChar(3) * PayloadCeilingChars` (28,200 bytes). A text longer than that cannot fit the D5 ceiling.
- A readable original is one fixed tier-1 unit. The budget pass admits it whole, or names it: `OVERFLOW: the verbatim original user intent ... restore: expand(tool_use_id=prompt_<s>_0)`.
- A capture longer than the limit gets no unit at all. It is named as a tier-1 overflow with that pointer and compared with nothing, so no mismatch is claimed.
- Build marks such an overflow degraded and sets item 2's row to truncated.
- The UAT-04 17,774-char brief is now named, never cut. The Loud line is the truthful "tier-1 material exceeds the hard budget cap", not intent_mismatch.
- The host-ceiling property (PropBuild_NeverExceedsTheHostCeiling) checked every other tier-1 record for wholeness but not the original, which is why the defect slipped through. It now checks the original too. Diagnostic run 01-red-property-8k-reader.log shows it failing against an 8,192-byte reader.

(2) F-UAT05-1 / F-UAT06-2, corrections never reach user_intent.evolution
ROOT CAUSE: Evolution was filled only in checkpoint/writer.go encodeSegmentLocked, which runs only for CLOSED segments. The observer closes a session's segment only at SessionEnd (observer/session.go); the scheduler closes one only on todo, test or commit.
- At every host compaction the prompts since the session last ended sit in the open segment.
- draftForPreCompact catches up only a cold draft and only closed segments. A live draft (Finalize's successor) is never refreshed at PreCompact.
- The evidence agrees: UAT-05 and UAT-06 index_segments.jsonl show segments closing only after the sessions ended, and every checkpoint has evolution [], narrative "" and empty current_work.
Two more defects on the same path:
- (a) seedTierOne took the derived parent (project-wide maxSeq) and copied its Original even when another session sealed it. Every later session in a project started from someone else's original and reported intent_mismatch at its first rehydration.
- (b) The evolution cap kept the OLDEST 64 entries, so any correction after a session's 65th prompt was lost.
- (Also relevant: the graph's userprompt node is keyed by turn alone, so graph-derived prompts can belong to another session.)
FIX (f869fc60, 631bda38):
- store.SessionPrompts: an optional FSStore capability returning one session's UserPromptSubmit records in turn order, from one bounded index scan. It is added to readOnlyReadsAllowlist.
- checkpoint/intent.go recomputes UserIntent from the session's own prompt records: the first record is the Original, every later one is an evolution entry, verbatim, oldest first, deduplicated. It runs at Begin (fresh and resumed), after each Advance, and in PreCompact just before the seal (Draft.RefreshIntent).
- Prompt texts are cached per draft and handed to Finalize's successor.
- Only this session's own chain seeds its intent. Reader.Latest is the last resort when the first record is unreadable.
- The cap keeps the newest 64 and adds one DropEntry {user_intent_evolution, elided} naming how many were left out.
- The graph-based evolution loop and appendEvolutionLocked are removed.
- The rehydrator already renders evolution newest-first under the original.

(3) F-UAT06-1, a fork replaces the original intent
ROOT CAUSE: `--fork-session` gives the fork a new id, and the host reports SessionStart source "fork" (the evidence shows observer log `source=fork` and hook_name SessionStart:fork). prompt_<fork>_0 is the fork's own first prompt.
- The fork's checkpoint held the parent's original only by accident, through the project-wide maxSeq inheritance that fix (2)(a) removes.
- buildUserIntent's L0-wins rule then overrode it with the fork's first prompt as intent_mismatch.
- No hook names the parent, and nothing recorded the lineage.
FIX (dc8445e9):
- checkpoint/lineage.go: NoteFork writes state/lineage-<session>.json (v1) once. It records the project's newest verifying checkpoint as the parent, plus the parent's session and the origin session (following the parent's own lineage). With no checkpoint, the parent is recorded as unknown. ReadLineage reads it back.
- A fork's drafts inherit the parent checkpoint's original and evolution, re-read at every Begin. The fork's own prompts, its first included, follow as evolution. If the parent no longer verifies, the fork's own chain is the fallback.
- daemon/session_lineage.go: handleSessionStart calls noteFork on source "fork", in every mode. The rehydrate service passes Request.Lineage.
- rehydrate: item 2 verifies a fork's original against the ORIGIN session's prompt_<origin>_0.
  - The unit opens with the unquoted line "(forked session: the original request of session <8>, which this session continues)".
  - A user_intent_source/fork drop entry names the parent, the checkpoint and expand(prompt_<origin>_0).
  - The fork's first prompt is an evolution delta; if an older checkpoint lacks it, it is added from L0.
  - A real disagreement is still the Loud, L0-wins intent_mismatch.
  - A fork with an unknown parent keeps its own first prompt and gets a notice saying the parent is unknown.
  - hostOrderNotice now takes the session whose record is presented. The SP08-D3 host-order rules (D35/D38) are otherwise untouched, and TestUserIntent_HostEarlierLaterTurnIsNamed and its sibling rows pass.
DOCS (aee29f18): an ADR 0011 §10 review correction; an architecture.md §7 paragraph; a user-guide Current-authority paragraph; a new cannot-do.md limit, "A forked session's parent is inferred, not reported by the host". Qompack.md and 00-ARCHITECTURE were not revised (that needs authorization).

TESTS ADDED (all RED before their fix; evidence in plans/sdd/V6-closeout/w13-intent/runs/):
- rehydrate: TestUserIntent_FirstPromptPastEightKiBIsReadWhole, TestBuild_FirstPromptPastEightKiBThatFitsIsInjectedWhole, TestBuild_FirstPromptLongerThanTheCeilingIsNamedNotCut, TestUserIntent_OversizedL0CaptureIsNeverCut, TestUserIntent_ForkKeepsTheParentsOriginal, TestUserIntent_ForkedParentMismatchIsStillLoud, TestUserIntent_ForkWithUnknownParentSaysSo, TestBuild_ForkBlockMatchesUAT06.
- checkpoint (real FSStore; prompt records made exactly as observer recordPromptDurable makes them): TestPreCompactCarriesACorrectionFromTheOpenSegment, TestPreCompactCarriesEveryCorrectionAcrossCheckpoints, TestEvolutionKeepsTheNewestRestatementsPastTheCap, TestBeginDoesNotAdoptAnotherSessionsIntent, TestBeginResumesFromTheSessionsOwnCheckpoint, TestNoteForkRecordsTheCheckpointTheForkContinues, TestNoteForkIsWrittenOnce, TestNoteForkWithNoCheckpointRecordsNoParent, TestForkCarriesTheParentsIntentThenItsOwnPrompts, TestForkOfAForkKeepsTheFirstOrigin.
- store: TestSessionPrompts_IsOneSessionsPromptsInTurnOrder.
- daemon (real writer, real store, dispatchOp): TestSessionStartFork_RecordsTheForksLineage, TestCompactRehydration_ForkShowsTheParentsOriginal. Both were RED with the two daemon wiring lines stashed (03-red-fork-daemon.log).
- The compact row sets dd.compactBudget to a minute because it tests content, not timing; the timing rows are elsewhere and unchanged.

CRITERION CHANGES (rationale):
- (a) TestUserIntent_CapsAtMaxIntentBytes asserted "> " plus the first 8,192 bytes as item 2's unit, which is the defect itself. It is replaced by TestUserIntent_WallOfTextIsNamedNotCut (same input, asserts no unit and a named tier-1 overflow with the expand pointer), because D5 and UAT-05 forbid a record cut mid-record.
- (b) The evolution cap now keeps the newest 64 and names the elided count; the draft.go design comment is rewritten. It used to keep the oldest, which loses current authority; Qompack.md §8.6 says current authority takes precedence over obsolete intent. No test pinned the old policy.
- (c) seedTierOne no longer copies a derived parent's Original when another session sealed it. The reader-level resumed-session rule (TestLatestInheritsAnotherSessionsChain) is unchanged, and the chain's Parent link is unchanged.
- (d) The host-ceiling property is strengthened, not loosened.
- No skips, no nolint, no nomagic:allow, no lowered bounds, no regenerated goldens.

LOAD: No wall-clock failure was observed. The first lint run failed only because another seat held golangci-lint's lock ("parallel golangci-lint is running"); the rerun passed.

### Commits

- 7e46cb69 fix(rehydrate): inject the L0 original whole or name it, never cut
- f869fc60 feat(store): enumerate one session's prompt records in turn order
- 631bda38 fix(checkpoint): carry a session's corrections into user_intent.evolution
- 133c42ed test(rehydrate): drop an ineffectual assignment in the ceiling property
- dc8445e9 fix(rehydrate): keep a forked session's parent intent as its original
- aee29f18 docs: describe whole originals, carried corrections and forked intent
- e5712c49 test(v6): record the w13-intent red and green runs

### Tests

- `go test -p 2 -count=1 -run 'TestUserIntent_FirstPromptPastEightKiBIsReadWhole|TestBuild_FirstPromptPastEightKiBThatFitsIsInjectedWhole|TestBuild_FirstPromptLongerThanTheCeilingIsNamedNotCut|TestUserIntent_OversizedL0CaptureIsNeverCut' ./internal/rehydrate/ (before fix)` — FAIL, 4 of 4 RED for the right reasons (runs/01-red-whole-original.log)
- `go test -p 2 -count=1 -run 'TestUserIntent_FirstPromptPastEightKiBIsReadWhole|TestBuild_FirstPromptPastEightKiBThatFitsIsInjectedWhole|TestBuild_FirstPromptLongerThanTheCeilingIsNamedNotCut|TestUserIntent_OversizedL0CaptureIsNeverCut|TestUserIntent_WallOfTextIsNamedNotCut' ./internal/rehydrate/ (after fix)` — ok (runs/01-green-whole-original.log)
- `temporary diagnostic: go test -p 2 -count=1 -run 'TestBuild_NeverExceedsTheHostCeiling' ./internal/rehydrate/ -rapid.checks=2000 with the reader temporarily set back to 8192 bytes, then restored` — FAIL as expected: the strengthened property catches the 8 KiB reader after 185 cases (runs/01-red-property-8k-reader.log)
- `go test -p 2 -count=1 -run 'TestPreCompactCarriesACorrectionFromTheOpenSegment|TestPreCompactCarriesEveryCorrectionAcrossCheckpoints|TestEvolutionKeepsTheNewestRestatementsPastTheCap|TestBeginDoesNotAdoptAnotherSessionsIntent|TestBeginResumesFromTheSessionsOwnCheckpoint' ./internal/checkpoint/ (before fix / after fix)` — 5 of 5 RED (evolution [], oldest-kept cap, adopted other session's original) (runs/02-red-evolution.log); after the fix, ok (runs/02-green-evolution.log)
- `go test -p 2 -count=1 -run 'TestSessionPrompts_IsOneSessionsPromptsInTurnOrder|TestEarliestPrompt_PicksTheEarliestHostStampedPromptOfTheSession' ./internal/store/` — ok
- `go test -p 2 -count=1 -run 'TestNoteForkRecordsTheCheckpointTheForkContinues|TestNoteForkIsWrittenOnce|TestNoteForkWithNoCheckpointRecordsNoParent|TestForkCarriesTheParentsIntentThenItsOwnPrompts|TestForkOfAForkKeepsTheFirstOrigin' ./internal/checkpoint/ (scaffold / after fix)` — RED against no-op scaffolds; the fork's checkpoint Original was the fork's first prompt (runs/03-red-fork-checkpoint.log); after the fix, ok (runs/03-green-fork-checkpoint.log)
- `go test -p 2 -count=1 -run 'TestUserIntent_ForkKeepsTheParentsOriginal|TestUserIntent_ForkedParentMismatchIsStillLoud|TestUserIntent_ForkWithUnknownParentSaysSo|TestBuild_ForkBlockMatchesUAT06' ./internal/rehydrate/ (before / after)` — 4 of 4 RED (runs/03-red-fork-rehydrate.log); after the fix, ok (runs/03-green-fork-rehydrate.log)
- `go test -p 2 -count=1 -run 'TestSessionStartFork_RecordsTheForksLineage|TestCompactRehydration_ForkShowsTheParentsOriginal' ./internal/daemon/ (wiring stashed / restored)` — 2 of 2 RED with the handlers.go and rehydrate_service.go wiring stashed (runs/03-red-fork-daemon.log); ok with it restored (runs/03-green-fork-daemon.log)
- `go test -p 2 -count=1 ./internal/rehydrate/... ./internal/checkpoint/... (final code)` — ok: rehydrate 2.3s, rehydratetest 3.0s, checkpoint 133.2s, checkpointtest 1.6s (runs/05-checkpoint-rehydrate-full.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 549.9s (runs/04-daemon-full.log). Run with all code changes; only the string and comment wording changed afterwards
- `go test -p 2 -count=1 ./internal/store/` — ok 435.3s (runs/02-store-full.log); store unchanged since
- `go test -p 2 -count=1 ./test/docs/` — ok (runs/04-docs.log)
- `go run ./tools/devtool fmt-check; go vet (windows) and GOOS=linux go vet ./internal/checkpoint/ ./internal/rehydrate/ ./internal/store/ ./internal/daemon/` — all exit 0 (runs/05-fmt-check.log, runs/05-vet.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS on all 8 sub-checks (runs/05-lint.log). The first attempt failed only on another seat's golangci-lint lock; then one ineffassign in my own test helper was fixed (133c42ed)

### Criterion changes

- TestUserIntent_CapsAtMaxIntentBytes (which asserted the 8 KiB prefix as item 2's unit, i.e. the defect) is replaced by TestUserIntent_WallOfTextIsNamedNotCut: same input, asserts no unit and a named tier-1 overflow with expand(tool_use_id=prompt_s4_0). Rationale: D5 whole records and UAT-05's 'record cut mid-record' fail criterion.
- The evolution cap keeps the newest 64 instead of the oldest, with a named elided-count DropEntry. The draft.go design comment is rewritten. Rationale: §8.6 current authority; the old policy lost every correction after a session's 65th prompt.
- seedTierOne no longer copies another session's Original from a derived (project-newest) parent. Only the session's own chain, or a recorded fork lineage, seeds intent. The reader-level resumed-session rule and the Parent chain link are unchanged.
- The graph-derived evolution loop in encodeSegmentLocked, and appendEvolutionLocked, are removed. Evolution is recomputed from the session's own prompt records (store.SessionPrompts).
- PropBuild_NeverExceedsTheHostCeiling is strengthened: it now asserts item 2's original is whole or named (requireOriginalWholeOrNamed).

### Open issues

- The coordinator must re-run the live rows UAT-04, UAT-05, UAT-06 and C4.3 on the fixed candidate. Only deterministic reproductions were run here.
- Host-generated UserPromptSubmit records (for example '<\task-notification>...' in UAT-06 session B) become evolution entries like any other prompt. They are not filtered: the record is what the host submitted as a user turn, and a filter would be a heuristic. They can take evolution-share room ahead of older corrections.
- Section 2 always renders the original first and the evolution newest-first beneath it. UAT-05's 'the corrected requirement must appear above the superseded one' holds among evolution entries but not relative to the original (UAT-06 requires the original first). The UAT wording may need clarifying. docs/uat.md UAT-06 step 4 also describes the fork as 'start a new one from the same project' rather than `--resume <id> --fork-session`. UAT docs belong to the mcpresp seat and were not edited.
- Latent defect of the same class, not fixed (outside this seat's items): checkpoint current_work and encodeSegmentLocked still read the dependence graph's userprompt nodes, which are keyed by turn alone, so they can carry another session's prompt in a multi-session project.
- Checkpoints sealed by candidate 3, which adopted another session's intent, still produce a Loud intent_mismatch until the session seals a checkpoint with the fixed build. That happens at the same compaction, because PreCompact precedes the compact SessionStart. Forks started under candidate 3 have no lineage record and keep the old behaviour.
- Refresh cost: a draft reads every prompt object of its session once (cached, and handed to Finalize's successor). After a daemon restart, the cold PreCompact of a very long session pays that once inside its window. It was not measured beyond the existing checkpoint budget and benchmark rows, which pass.
- Qompack.md §8.5/§8.6 and 00-ARCHITECTURE were not revised; that needs explicit authorization. ADR 0011 §10 carries the correction.

### Needs the owner

- intentReadLimit = maxUTF8BytesPerHostChar (3) x PayloadCeilingChars (9,400) = 28,200 bytes (internal/rehydrate/items.go). DERIVATION: a UTF-16 code unit takes at most 3 UTF-8 bytes (an invalid byte counts as one unit), so a text longer than this cannot be emitted whole inside the D5 ceiling. It replaces the 8,192-byte cap. IF WRONG: set too low, a capture that could have fitted is named with its expand pointer instead of injected (never cut); the only case is a prompt carrying a large stripped injection span. Set too high, it costs only rehydration memory, since the budget pass decides fit. It is a composed derivation, not a new number, but it is listed for approval.
- Evolution cap policy (value unchanged at maxIntentEvolution = 64): the cap now keeps the NEWEST 64 instead of the OLDEST 64, and a single DropEntry {user_intent_evolution, elided} names how many were left out (internal/checkpoint/intent.go, draft.go). WHY: keeping the oldest discards every correction after a session's 65th prompt, which violates §8.6 current authority. IF WRONG: early restatements of very long sessions leave the checkpoint (each remains a verbatim prompt capture in the store).
- Fork parent inference (a design decision, D-level): the host names no parent for `--fork-session`, so NoteFork takes the project's newest verifying checkpoint when the fork starts as the one it continues. It records this once in the new state file state/lineage-<session>.json (wire v1, included in backups because state/ is). RISK: forking an older session after a newer one compacted in the same project inherits the newer session's intent. MITIGATION: the original is labelled with its session, verified against that session's L0 capture, reported as user_intent_source/fork, and documented in cannot-do.md. With no checkpoint, the parent is recorded unknown and the fork's own first prompt stands. Alternatives would read the transcript (ADR 0011 forbids it as an intent source) or wait for a host-supplied parent id.
- The fork provenance entry (user_intent_source, id 'fork', about 250 characters) is added to section 7 at every compaction of a fork, following the host_order notice precedent. It is not a degradation (Degraded stays false). Please confirm this is acceptable next to the unit's own provenance line.

## Independent review

### review:intent: needs-fixes

- **major** `internal/checkpoint/lineage.go:419-431, 491-494 (NoteFork / forkIntentFor); docs/cannot-do.md new 'forked session' entry` — A fork inherits its parent's intent only as it stood at the parent's last SEALED checkpoint. Two cases lose intent. (1) If the parent made corrections after its last compaction and then forked, the fork's original and evolution come from that older checkpoint, so the fork is rehydrated with the superseded requirement. That is the F-UAT06-2 defect class this workstream fixes, now on the fork path. (2) If the parent never compacted, there is no checkpoint, the lineage is recorded as 'unknown', and the fork's own first prompt is shown as the original. That is exactly the F-UAT06-1 symptom, only with a notice added. The parent's prompt records are in the store in both cases. Only the checkpoint snapshot is consulted.
  - Evidence: NoteFork sets ParentSeq = maxSeq(w.l) and reads nothing else about the parent. forkIntentFor returns pc.UserIntent.{Original,Evolution} from reader.Get(rec.ParentSeq). Nothing reads store.SessionPrompts(ParentSession). In UAT-06 the parent compacted just before forking, so the live re-run will pass, but the general case does not hold. cannot-do.md describes the wrong-parent risk and the no-checkpoint case. It does not say that the parent's post-checkpoint corrections are dropped.
  - Fix: Record ParentSession (and At) in the lineage. Build the fork's inherited intent from the parent's own prompt records (SessionPrompts(ParentSession) filtered to TS <= lineage.At, recursively through the parent's lineage for its original), falling back to the checkpoint copy only when the records are unreadable. Add a checkpoint test: the parent corrects after its last compaction, then forks, and the fork's checkpoint must carry that correction. Also add one line to cannot-do.md stating what a fork of a never-compacted parent gets, or apply the same parent inference to it (for example, the session with the newest prompt before the fork started) and list that heuristic for the owner.
- **major** `internal/checkpoint/intent.go:178-201 (setIntentLocked cap); internal/checkpoint/truncate.go Truncate; internal/config/defaults.go:60 (BudgetTokens 12000)` — Evolution now really fills with up to 64 verbatim prompts of unbounded size. Truncate never cuts user_intent (tier 1). A session with a few large pastes, or a long brief like UAT-04's (~4.5k tokens) plus ordinary prompts, pushes the checkpoint over its 12,000-token budget. Truncate then removes narrative, every tool and file pointer, open questions, decisions and next_step, and still records budget_exceeded. Before this change evolution was empty in practice, so this interaction was never exercised live. The rehydrator can show only as many deltas as fit item 2's share anyway, so most of the stored text buys nothing at rehydration and costs the tier-2/3 material that C4.3 recovery uses. No test or open_issue covers it.
  - Evidence: The cap is count-only: `if len(evo) > maxIntentEvolution { evo = evo[elided:] }`, with no per-entry or total size bound. Truncate: 'tier 1 (invariants, user intent, eliminations) never' is cut, and a 'budget_exceeded' tier1 entry is written when the cuttable tiers are exhausted.
  - Fix: Also bound evolution by size. Keep the newest whole entries up to a token share derived from the checkpoint budget or the rehydrator's item-2 share, and list that number for the owner. Name the elided entries by prompt id, e.g. expand(tool_use_id=prompt_<s>_<turn>) for the oldest and newest elided, so the D5 pointer requirement holds. Add a test with, for example, 64 prompts of about 1k tokens each, asserting that pointers and decisions still survive Truncate, or that the trade-off is an explicit, documented choice.
- **minor** `internal/rehydrate/items.go:474-479 (evolutionOf)` — The legacy-checkpoint shim re-adds the fork's own first prompt as the NEWEST evolution delta whenever the checkpoint's evolution does not list it. That also happens for new-build checkpoints: (a) when the newest-64 cap has elided it, because the fork has made more than 64 prompts; (b) when setIntentLocked deduplicated it because it equals the inherited original. In (a) the fork's oldest statement is rendered at the top of 'Evolution (most recent first)', presenting a superseded prompt as the current authority. In (b) it duplicates the original.
  - Evidence: `if own := readL0First(ctx, d, r.Session); own.state == l0Whole && !listedIn(evo, own.text) { out = append(out, ...) }` is prepended before the newest-first checkpoint entries. setIntentLocked drops `text == original` and elides the oldest entries past 64.
  - Fix: Apply the shim only when the checkpoint cannot contain the fork's first prompt. For example, skip it when the checkpoint carries the user_intent_evolution/elided drop, when own.text equals the original, or when the checkpoint was sealed by a build that writes fork evolution (a marker or schema field). Add a rehydrate row covering a fork whose first prompt was elided.
- **minor** `internal/checkpoint/intent.go:100-113, 160-172; internal/checkpoint/writer.go:99-105 (handoff)` — Memory grows without bound. The per-draft promptText cache holds the verbatim text of every prompt in the session, and every record is read even when the newest-64 cap will discard it. The cache is handed to the successor draft on every Finalize. Drafts can stay open for the daemon's life (advanceAllSessions' own comment), so a long session with large pastes pins all of its prompt bytes in memory. A handoff entry whose successor Begin fails or resumes is never consumed.
  - Evidence: refreshIntentLocked loops over all recs and calls promptTextLocked for each before setIntentLocked applies the cap. promptText has no size or count bound. takeHandoff is called only on the fresh-draft path of Begin.
  - Fix: Walk the records newest-first and stop once the cap (and any size share) is filled, reading record 0 separately for the Original. Cache only the kept entries, or bound the cache. Delete w.handoff[s] on the resume and failure paths of Begin.
- **minor** `internal/checkpoint/lineage.go:437 (paths.WriteAtomic); internal/checkpoint/intent.go:165-169; docs/architecture.md:469-470` — The lineage record is the only non-derivable input to a fork's intent, but it is written with WriteAtomic and no directory flush. architecture.md still says every state/ document is 'derived or rebuilt' and safe to lose. If the record is lost or unreadable after the fork has sealed checkpoints, forkIntentFor returns nil. refreshIntentLocked then treats recs[0] (the fork's own first prompt) as the Original and overwrites the inherited original seeded from the fork's own chain. The rehydrator, with no lineage, then shows the fork's first prompt as the verbatim original with no mismatch. F-UAT06-1 comes back silently.
  - Evidence: `if err := paths.WriteAtomic(p, b, draftPerm)` is not followed by paths.SyncDir(l.State). In refreshIntentLocked, `if i == 0 && d.fork == nil { if text != "" { original = text } }` overrides the seeded Original.
  - Fix: Call paths.SyncDir on the state directory after writing the lineage, and document lineage-<session>.json in architecture.md as a non-derived state record, including whether backup and restore carry it. Optionally stop a refresh from replacing a chain-seeded Original that came from a different session's first prompt.
- **nit** `internal/checkpoint/lineage.go:411-431; internal/rehydrate/items.go:654-660; commits 133c42ed, aee29f18, e5712c49` — Four small issues. (1) The Lstat-then-WriteAtomic check is not atomic, so a live start and a spooled replay can both write the record, despite the 'written once' claim. (2) A SessionStart:fork replayed after the fork has already sealed a checkpoint makes maxSeq name the fork's own checkpoint as its parent. (3) hostOrderNotice's detail still says '… is this session's first captured prompt' when the record belongs to the fork's origin session. (4) Three of the seven commits have no Refs footer. The repo's convention is inconsistent, but the fix commits carry one.
  - Evidence: In NoteFork, `rec.ParentSeq, rec.ParentSession, rec.OriginSession = seq, pc.Session, pc.Session` has no check that pc.Session != s. The hostOrderNotice Detail string is unchanged while it now takes the origin session.
  - Fix: Skip or step back over checkpoints sealed by s itself in NoteFork. Create the record exclusively (O_EXCL rename or a link-based create) if 'once' is a claim. Use the presented session's id in the host_order detail. Add a 'Refs: V6-VERIFY, C4.3' footer to the test and docs commits when the branch is folded.

## Fix seat (review resolution) — status `done`, head `8e6d7cc0`

### Summary

FIX SEAT, closeout/w13-intent (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-intent). I added six commits on top of the implementer's e5712c49. Every change has a regression row that was RED before the fix. Each touched package passed in full at d3c160c7. The one exception is the co-load wall-clock GC row noted below, which passes when run alone.

## Review resolution

1. **MAJOR: a fork inherits only the parent's last sealed checkpoint. CONFIRMED and FIXED (9573785f, with 484c2bc3).**
   - Root cause (internal/checkpoint/lineage.go at e5712c49): `NoteFork` set `ParentSeq = maxSeq`, and `forkIntentFor` copied that checkpoint's `UserIntent`. Nothing read the parent's prompt records.
   - Consequences: corrections the parent made after its last compaction were lost. A parent that never compacted gave the fork nothing, or, when another session had compacted, that other session's intent.
   - RED evidence (runs/06-red-review-checkpoint.log):
     - `TestForkCarriesTheParentsCorrectionsAfterItsLastCheckpoint`: evolution was [corr60, forkFirst], with corr75 missing.
     - `TestForkOfANeverCompactedParentInheritsItsIntent`: `ParentSession` was "sess_sp10_other".
     - `TestForkInheritsOnlyWhatItsParentSaidBeforeTheFork`: the original was the fork's own first prompt.
   - Fix, inheritance: a fork inherits what its conversation holds. That is the parent's own prompt records stamped at or before the fork started (`Lineage.At`, now the host's stamp on the fork's SessionStart via `hookTime`). The walk continues recursively through each ancestor's lineage, with a visited-set guard. The origin session's first prompt is the original; every later prompt, including the fork's own, is evolution. The parent checkpoint copy is only the fallback when the records cannot be enumerated.
   - Fix, parent inference: the parent is now the session whose prompt was the project's newest at or before the fork started (new `store.LatestPrompt`, a bounded read-only index scan). A store without that capability falls back to the newest checkpoint's session (row `TestForkWithoutPromptEnumerationUsesTheParentsCheckpoint`).
   - Cost: `ParentSeq` still costs one manifest read and one load. I deliberately avoided `Reader.Latest`, which walks and verifies every newer checkpoint, on the session.start route. `ParentSeq` is set only when the parent sealed the project's newest checkpoint.
   - The reviewer's never-compacted case is resolved by applying the inference, not just documenting it. cannot-do.md now states the inference and the case it gets wrong.

2. **MAJOR: evolution unbounded by size squeezes tier 2/3. CONFIRMED and FIXED (253cc8a4).**
   - RED: `TestEvolutionHoldsTheRehydrationCeilingSoPointersSurvive`. Pasting 64 prompts of about 4,000 chars each made Truncate cut the tool pointers and record budget_exceeded.
   - Fix: the refresh walks the candidates newest first. It keeps whole restatements while both bounds hold: 64 entries and `checkpoint.EvolutionCeilingChars` (9,400 host chars, which is `rehydrate.PayloadCeilingChars`). It stops at the first restatement that would pass either bound. That one and every older one are left out unread, since item 2's prefix fill could inject none of them.
   - One DropEntry {user_intent_evolution, elided} names `expand(tool_use_id=…)` for the newest and oldest left out.
   - Duplicates are now kept at their NEWEST position, so a repeated correction stays the current authority.
   - `TestEvolutionCeiling_IsThePayloadCeiling` ties the constant to the rehydrator's ceiling.

3. **MINOR: the legacy shim re-adds the fork's first prompt on top. CONFIRMED and FIXED (b5fea566).**
   - RED rows (runs/06-red-review-rehydrate.log): `TestUserIntent_ForkFirstPromptTheCheckpointLeftOutIsNotShownAsNewest` and `TestUserIntent_ForkFirstPromptEqualToTheOriginalIsNotRepeated`.
   - Fix: the shim is skipped when `checkpoint.EvolutionElided(cp)` is true or when the prompt equals the shown original.
   - `TestUserIntent_ForkFirstPromptMissingFromTheCheckpointIsAddedAsNewest` pins the case the shim still exists for.
   - Also fixed: the provenance entry printed "checkpoint 0000" for a parent with no checkpoint (RED: `TestUserIntent_ForkOfAParentWithNoCheckpointNamesNone`).

4. **MINOR: memory growth and the handoff leak. CONFIRMED and FIXED (253cc8a4).**
   - RED (runs/06-red-review-cache.log): `TestRefreshHoldsOnlyTheTextsItKeeps` held 101 texts where 65 was expected, and `TestBeginConsumesAStashedCacheOnEveryPath` failed on the live path.
   - Fix: the cache is replaced each refresh by the original plus the kept texts. Duplicates share the kept string. Evolution reads are bounded to `evolutionReadLimit` bytes, and a newest-first walk stops early. `Begin` takes the stash at the top, on every path.

5. **MINOR: lineage durability and docs. PARTLY REBUTTED, docs FIXED (d3c160c7), fallback FIXED (9573785f).**
   - Rebuttal: "no directory flush" is wrong. `paths.WriteAtomic` ends with `return fsyncDir(filepath.Dir(p))` (internal/paths/atomic.go). On Windows, D24's NTFS premise applies: the next flush on the volume carries the rename, and the fork's first prompt capture is flushed before its acknowledgement.
   - Correct part: architecture.md said every `state/` document is derived. It now names `state/lineage-<session>.json` as the one non-derived record, says a backup carries it (`backupSkipDirs` excludes only backup/run/tmp/logs/metrics), and says what a power cut before the fork's first prompt costs.
   - The optional fix is also implemented: an unreadable lineage record falls back to the fork's own chain, and the chain's original is not replaced (RED: `TestForkWithAnUnreadableLineageKeepsTheIntentItInherited`).

**Also fixed:** `TestSessionStartFork_RecordsTheForksLineage` now asserts `At` equals the host stamp. It was RED with the daemon's own time (runs/06-red-review-daemon-at.log).

## Criterion changes (rationale)

- `NoteFork` gained an `at` parameter. The checkpoint tests call it through the helper `f.noteFork`, which also publishes sources, as daemon wiring does before any hook. This is mechanical.
- The `TestNoteForkRecordsTheCheckpointTheForkContinues` assertion message was reworded; the value is unchanged.
- The daemon lineage row was strengthened with two assertions and a replayed request stamp.
- The dedup position changed from oldest to newest. No test asserted the old order. It changed for current authority.
- The provenance wording changed. Existing assertions (parent id, expand pointer) still hold.
- No assertion was deleted or loosened, no threshold was changed, and no skip was added.

### Commits

- 484c2bc3 feat(store): name the newest other session's prompt before a moment
- 253cc8a4 fix(checkpoint): bound evolution to what one rehydration can carry
- 9573785f fix(checkpoint): inherit a fork's intent from what its parent said
- b5fea566 fix(rehydrate): keep a left-out or repeated fork prompt out of evolution
- d3c160c7 docs: describe the evolution bounds and how a fork's parent is chosen
- 8e6d7cc0 test(v6): record the w13-intent review-round runs

### Tests

- `go test -p 2 -count=1 ./internal/checkpoint/ -run 'TestForkCarriesTheParentsCorrectionsAfterItsLastCheckpoint|TestForkOfANeverCompactedParentInheritsItsIntent|TestForkInheritsOnlyWhatItsParentSaidBeforeTheFork|TestForkWithAnUnreadableLineageKeepsTheIntentItInherited|TestEvolutionHoldsTheRehydrationCeilingSoPointersSurvive' (on e5712c49 plus the tests; the new constant was read as its literal for this one run)` — RED as intended, all five failing for the reported reasons (runs/06-red-review-checkpoint.log)
- `go test -p 2 -count=1 ./internal/checkpoint/ -run 'TestRefreshHoldsOnlyTheTextsItKeeps|TestBeginConsumesAStashedCacheOnEveryPath' (before the fix)` — RED: 101 cached texts where 65 expected, and a stash left on the live path (runs/06-red-review-cache.log)
- `go test -p 2 -count=1 ./internal/rehydrate/ -run 'TestUserIntent_ForkFirstPromptTheCheckpointLeftOutIsNotShownAsNewest|TestUserIntent_ForkFirstPromptEqualToTheOriginalIsNotRepeated|TestUserIntent_ForkOfAParentWithNoCheckpointNamesNone' (before the fix)` — RED, all three (runs/06-red-review-rehydrate.log)
- `go test -p 2 -count=1 ./internal/daemon/ -run 'TestSessionStartFork_RecordsTheForksLineage' (fork noted at daemon time, temporary revert)` — RED: At was 60 s after the host stamp (runs/06-red-review-daemon-at.log); the revert was then undone
- `go test -p 2 -count=1 ./internal/checkpoint/ -run 'TestForkCarriesTheParentsCorrectionsAfterItsLastCheckpoint|TestForkOfANeverCompactedParentInheritsItsIntent|TestForkInheritsOnlyWhatItsParentSaidBeforeTheFork|TestForkWithAnUnreadableLineageKeepsTheIntentItInherited|TestForkWithoutPromptEnumerationUsesTheParentsCheckpoint|TestEvolutionHoldsTheRehydrationCeilingSoPointersSurvive|TestRefreshHoldsOnlyTheTextsItKeeps|TestBeginConsumesAStashedCacheOnEveryPath|TestForkCarriesTheParentsIntentThenItsOwnPrompts|TestForkOfAForkKeepsTheFirstOrigin|TestEvolutionKeepsTheNewestRestatementsPastTheCap'` — PASS (the rows were also run through prefix patterns during the work)
- `go test -p 2 -count=1 ./internal/rehydrate/ -run 'TestUserIntent_ForkFirstPromptTheCheckpointLeftOutIsNotShownAsNewest|TestUserIntent_ForkFirstPromptEqualToTheOriginalIsNotRepeated|TestUserIntent_ForkOfAParentWithNoCheckpointNamesNone|TestUserIntent_ForkFirstPromptMissingFromTheCheckpointIsAddedAsNewest|TestEvolutionCeiling_IsThePayloadCeiling|TestBuild_ForkBlockMatchesUAT06'` — PASS
- `go test -p 2 -count=1 ./internal/store/ -run 'TestLatestPrompt_IsTheNewestOtherSessionsPromptAtOrBefore|TestSessionPrompts_IsOneSessionsPromptsInTurnOrder|TestReadOnly_EveryExportedMethodIsClassified'` — PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint/ ./internal/rehydrate/ (at d3c160c7)` — PASS: checkpoint 64.8 s, rehydrate 0.8 s (runs/07-checkpoint-rehydrate-full.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (at d3c160c7)` — PASS in 278.6 s (runs/07-daemon-full.log)
- `go test -p 2 -count=1 -timeout=30m ./internal/store/ (at d3c160c7, machine co-loaded by other seats)` — FAIL only on TestGC_DeadlineTruncatesAndResumes ('511' is not less than '256'), a wall-clock row the ledger already lists as load-sensitive (runs/07-store-full-coload.log). The same store code passed the whole package earlier (runs/06-store-full.log, 311 s).
- `go test -p 1 -count=3 -timeout=30m ./internal/store/ -run '^TestGC_DeadlineTruncatesAndResumes$' -v` — PASS 3/3 alone (runs/07-store-gc-deadline-alone.log): co-load, not this change. The store diff only adds LatestPrompt.
- `go run ./tools/devtool fmt-check; go vet (windows and GOOS=linux) ./internal/checkpoint/ ./internal/rehydrate/ ./internal/store/ ./internal/daemon/` — PASS (runs/07-fmt-check.log, runs/07-vet.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS, all 8 sub-checks (runs/07-lint.log)
- `go test -p 2 -count=1 ./test/docs` — PASS (runs/07-docs.log)

### Open issues

- The live UAT-04, UAT-05 and UAT-06 rows still need the coordinator's re-run on the fixed candidate. No real Claude Code session was used here, and the Linux container was not started, so there is no Linux -race run of these rows.
- Pre-existing, not introduced here: a checkpoint's user_intent.original is still stored whole, whatever its size. A first prompt far above 12,000 tokens makes Truncate record budget_exceeded, as it did before wave 13. The rehydrator names such an original by expand pointer and never cuts it.
- The elided count counts candidates past the stop point without reading them. It can include duplicates or unreadable records, so 'N earlier restatements' is an upper bound. The ids for the newest and oldest are exact.
- Out of scope: TestGC_DeadlineTruncatesAndResumes fails under co-load on Windows (511 vs <256). It was already known from Phase 3 win-timing, and it passes alone.

### Needs the owner

- intentReadLimit = maxUTF8BytesPerHostChar (3) x PayloadCeilingChars (9,400) = 28,200 bytes (internal/rehydrate/items.go), carried from the implementer unchanged. DERIVATION: a text longer than this cannot be emitted whole inside the D5 ceiling. IF WRONG: too low, and a prompt that could have fitted is named with an expand pointer instead of injected (never cut). Too high costs only rehydration memory.
- NEW, composed and not a new number: checkpoint.EvolutionCeilingChars = 9,400 host chars (internal/checkpoint/intent.go). It is rehydrate.PayloadCeilingChars (the D5 9,500 less the 100-char probe reserve), and TestEvolutionCeiling_IsThePayloadCeiling ties the two. DERIVATION: item 2 admits evolution newest first and stops at the first delta that does not fit, so restatement text beyond one payload ceiling can never be injected. IF WRONG: lower, and restatements a rehydration could have shown are only named. Higher, and a session of long pastes spends the checkpoint's 12,000-token budget on uninjectable tier-1 text, so Truncate cuts pointers and decisions (the reviewer's finding). Companion: evolutionReadLimit = 3 x 9,400 = 28,200 bytes read per restatement. A longer capture is known to exceed the bound, and is named rather than read.
- Evolution policy, REVISED from the implementer's item. It still keeps the NEWEST, now under two bounds (64 entries AND 9,400 chars), and stops at the first restatement that does not fit. The single DropEntry {user_intent_evolution, elided} now names expand(tool_use_id=…) for the newest and oldest left out. Duplicates are kept at their newest position, not the oldest. WHY: current authority (§8.6), and consistency with the rehydrator's prefix fill. IF WRONG: one very large newest prompt (over 9,400 chars) makes the checkpoint carry no evolution, and every earlier correction is only named. The rehydrator would show none of them either.
- Fork parent inference, REVISED from the implementer's item and D-level. The parent is now the session whose prompt was the project's newest at or before the fork's SessionStart stamp (store.LatestPrompt). It was 'the session that sealed the project's newest checkpoint', which is still the fallback for a store without prompt enumeration. WHY: most sessions never compact. The old rule gave a fork of a never-compacted session another session's intent whenever any checkpoint existed, and nothing when none did. 'Most recently active' is what Claude Code's own --continue means. The fork now inherits the parent's prompt records stamped before the fork, not the parent's last checkpoint, so corrections made after that checkpoint are carried. RISK: forking a session other than the one last prompted in the same project inherits the last-prompted session's intent. That is documented in cannot-do.md, and the rehydration labels the session and verifies the original against that session's own L0 capture. The alternatives remain reading the transcript (ADR 0011 forbids it) or a host-supplied parent id.
- The fork provenance entry (user_intent_source, id 'fork') is still added to section 7 at every compaction of a fork (Degraded stays false). Its wording now says the parent was inferred and omits the checkpoint clause when there is none. It is about 336 characters, or 371 with the checkpoint clause, for UUID-length session ids (the implementer quoted about 250). Please confirm.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



# Wave 19 forkwork (candidate 8, D59)

Branch `closeout/w19-forkwork`. Workflow `wf_d5ae67fb-6ab`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `84b6f802`

### Root cause

The derived CurrentWork.Goal was set in encodeSegmentLocked (internal/checkpoint/writer.go, the `prompts[len(prompts)-1]` line at about 921). It read the dependence graph's userprompt nodes whose turn falls in the encoded segment's range. Those nodes are keyed by turn alone (dag.UserPromptNode(turn)), so different sessions share them. In UAT-06 the fork's segment 6 covered turns 2-4. The graph had userprompt:2 (the parent's 60 correction), userprompt:3 (the fork's already_tried) and userprompt:4 (the parent's record_eliminated instruction). After sortNodesByTurn the highest turn won, so the goal was the parent's turn-4 prompt, cut at goalMaxRunes = 160. A second, smaller problem: prompts in the still-open segment never counted. The fork's newest prompts (turn 5 /qompack:why and turn 7 "must be 45") were in segment 7 (5-12), which was open at PreCompact. intent.go already documents this graph collision and moved user_intent off the graph for the same reason; current work was the one derivation left on it. In the live data checkpoint 0004 came out right only by luck: the fork's turn-0 prompt replaced the Ref on the shared userprompt:0 node.

### Summary

PRODUCT CODE CHANGED (internal/checkpoint). Candidate 8 should batch it.

F-C7-UAT06-1 is fixed in commit 84b6f802 on closeout/w19-forkwork. The base was a357d187.

**How a fork inherits (unchanged).** At Begin the fork's draft gets the intent of the session it continues. When records can be listed this is the parent's prompt records up to the fork moment (`d.fork.fromRecords`). Otherwise it is the parent checkpoint's copy. The parent's original is the Original, and the parent's later prompts come first in the evolution, followed by every prompt of the fork's own. Eliminations and decisions carry through seedTierOne / carriedBy / carryDecisionsLocked. The diff does not touch any of that.

**How Current work was chosen before.** It was "one sentence of the most recent prompt", taken from the graph's userprompt nodes inside each encoded segment's turn range. Root cause is in root_cause.

**The fix** (in `internal/checkpoint/intent.go`, `source.go` and `writer.go`):
- New `deriveCurrentWorkLocked` runs at the end of `refreshIntentLocked`. That function already runs at Begin, at a resumed draft, after every Advance and at PreCompact just before the seal.
- It takes the goal from the newest of the session's own prompt records (`store.SessionPrompts`). The text is read through the draft's prompt-text cache and `fromStore`, so the injection-tag stripping still applies. The goal is still `truncRunes(firstSentence(text), goalMaxRunes)`, with NextStep empty and BlockedOn nil.
- It does nothing when `workExplicit` is set (SetCurrentWork still wins, §7) or when the session has no prompt of its own.
- If the newest record is unreadable or empty, the goal is left as it was. That was the old behaviour too.
- An in-memory field `goalFrom` remembers which record the goal came from. This stops a paste too large for the evolution cache from being re-read at every refresh.
- In `encodeSegmentLocked` the old graph-read derivation now runs only when the store does not have `SessionPrompts`. That matches seedIntent's existing fallback.
- No new numbers or constants.

**A fork with no prompt of its own yet.** Nothing is derived, so the goal stays as the fork's draft began: empty.
- If the fork's only segment is still open, the goal was empty before the fix and is empty after it. `TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal` passes on the base and after.
- If that fork has a closed segment over turns where the parent has prompts, the base put the parent's prompt into the goal (the same defect). It is empty now. `TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork` was red on the base with the record_eliminated string and is green now.

**Plain --resume (same session id) is unaffected.** All its prompts are records of that one session, so the goal is still the session's newest prompt. In the live data, checkpoints 0001 (limiter.py), 0002 (notes.md) and 0004 (design.md) would get the same goal as before. `TestResumedSessionCurrentWorkIsItsNewestPrompt` also adds another session's prompt at a turn inside the resumed session's encoded range. On the base that test was red ("Build the CSV importer."); it is green now.

**Small change for ordinary sessions.** The goal now follows the newest prompt even when it sits in the open segment, the same way user_intent already does (F-UAT05-1). A new draft that Finalize opens after sealing also gets the goal at Begin, instead of starting empty until a segment is encoded.

**Red-first.** `TestForkCurrentWorkIsTheForksNewestPrompt` copies the live shape exactly:
- Parent prompts at turns 0, 2, 4 and 6 in a closed segment 0-7, compacted.
- Then the fork: turn 0 in a closed segment 0-1 and a compaction; turn 3 in a closed segment 2-4; turns 5 and 7 in an open segment from 5; then a second compaction.

On the base it failed with actual = "Call mcp__plugin_qompack_qompack__record_eliminated with target \"per-client rate limit\", approach \"100 requests per minute\" and reason \"superseded by the user's". That is the live string byte for byte. After the fix the goal is "Correction: the limit must be 45 requests per minute per client, not 60." The same test checks that the Original and the full inherited-then-own evolution are unchanged. `TestExplicitCurrentWorkSurvivesAPromptRefresh` checks that SetCurrentWork still wins over the PreCompact refresh.

**Checks run, all passing.**
- `go vet` on Windows and with GOOS=linux, golangci-lint on ./internal/checkpoint/..., and `devtool fmt-check`. fmt-check first failed because my edit tool wrote CRLF line endings; I converted the files back to LF before committing.
- `devtool lint --only=docmarkers,runpatterns`.
- Full `./internal/checkpoint/...` once (167 s).
- The two e2e rows and three daemon fork rows listed in tests, each by exact name.
- No docs or generated inputs moved, so `./test/docs` and the gen-*-docs --check commands were not needed.

**Doc note for the docs seat (not edited here).** docs/uat.md line 859 (the UAT-06 finding) can be marked fixed by 84b6f802 once candidate 8's live lane re-runs UAT-06. With this fix, checkpoint 0005's goal there would be the 45 correction.

### Commits

- 84b6f802 fix(checkpoint): derive current work from own prompt records

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint -run '^(TestForkCurrentWorkIsTheForksNewestPrompt|TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork|TestResumedSessionCurrentWorkIsItsNewestPrompt)$' -count=1  (on base a357d187 + new test file, before the fix)` — RED as intended: TestForkCurrentWorkIsTheForksNewestPrompt got the parent's record_eliminated goal (the live string); TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork got the same parent prompt; TestResumedSessionCurrentWorkIsItsNewestPrompt got another session's 'Build the CSV importer.'
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint -run '^(TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal|TestExplicitCurrentWorkSurvivesAPromptRefresh)$' -count=1  (on base, before the fix)` — ok (these two pin behaviour that already held: empty goal for a no-prompt fork with an open segment, explicit current work wins)
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint -run '^(TestForkCurrentWorkIsTheForksNewestPrompt|TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork|TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal|TestResumedSessionCurrentWorkIsItsNewestPrompt|TestExplicitCurrentWorkSurvivesAPromptRefresh)$' -count=1 -v` — all 5 PASS after the fix
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint/... -count=1` — ok internal/checkpoint 167.250s; ok internal/checkpoint/checkpointtest 1.112s
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./test/e2e -run '^(TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint|TestV5_PreCompactToRehydrateToDroppedRoundTrip)$' -count=1 -v` — both PASS (ok test/e2e 25.503s)
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/daemon -run '^(TestSessionStartFork_RecordsTheForksLineage|TestCompactRehydration_ForkShowsTheParentsOriginal|TestService_ResumedOrForkedSessionInheritsProjectCheckpoint)$' -count=1 -v` — all 3 PASS
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go vet ./internal/checkpoint/... && GOOS=linux go vet ./internal/checkpoint/...` — clean
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/checkpoint/...` — exit 0, no findings
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run ./tools/devtool fmt-check` — exit 0 (after converting the CRLF line endings my edit tool wrote back to LF)
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers

### Open issues

- Not run here: -race on the touched package (the coordinator runs -race at night), and the candidate 8 live re-run of UAT-06 to confirm checkpoint 0005's goal is the fork's 45 correction on a real host.
- docs/uat.md line 859 (UAT-06 finding) belongs to the docs seat. It needs a status update once candidate 8's live lane confirms the fix.
- UAT-06 doc finding not in this seat's scope: the row's expected result still says "Section 2's first unit is the verbatim original intent", which disagrees with D50 (evolution first, original last). Already routed by the lane.

## Independent review

### review:forkwork: needs-fixes

- **minor** `internal/checkpoint/writer.go:925 (with internal/checkpoint/intent.go:193-196 and internal/store/prompt_recovery.go:92)` — When a store has the SessionPrompts capability but cannot answer, Current work is now never derived. The graph-read fallback in encodeSegmentLocked is gated only on the type assertion (`src.Store.(store.SessionPrompts)`), not on whether the call works. FSStore.SessionPrompts returns core.ErrDegraded once the tool-use index passes promptScanLimit (1<<18 records), and it also fails on any ctx or read error. In that case refreshIntentLocked returns at `if !ok { return }`, before it reaches the new deriveCurrentWorkLocked, and the graph form is skipped because the store is 'capable'. Before this commit the goal was still derived from the graph in that state. Now every session's checkpoint carries an empty current_work.goal (fresh drafts start empty, see the no-prompt fork test), and nothing reports it beyond the existing intent Warn. No test covers a store that has the capability and then fails, and none covers the kept graph-only branch.
  - Evidence: intent.go:193: `own, ok := sessionPromptRecords(...); if !ok { return }`, and deriveCurrentWorkLocked is called only at the end of that function (intent.go:230). writer.go:925: `if _, capable := src.Store.(store.SessionPrompts); !capable && !d.workExplicit && len(prompts) > 0 {`. prompt_recovery.go:92-93: `if len(s.toolUse) > promptScanLimit { return nil, core.ErrDegraded }`. grep finds no checkpoint test that fakes a failing SessionPrompts.
  - Fix: Have refreshIntentLocked record whether the last SessionPrompts call answered, for example an in-memory `d.promptsAnswered bool` set next to goalFrom. In encodeSegmentLocked, use the graph form when `!capable || !d.promptsAnswered`. When the fallback runs, keep only prompt nodes whose Ref resolves (src.Store.ToolUse) to a record with Session == d.session. The fallback then cannot bring back F-C7-UAT06-1's cross-session collision. Add a row with a store wrapper whose SessionPrompts returns core.ErrDegraded and assert that the goal is still the session's own newest closed-segment prompt.
- **nit** `commit 84b6f802 message body; internal/checkpoint/current_work_fork_test.go:22-23` — The commit message and the test file's header comment both say current work comes from the session's own prompt records 'newest first'. The code takes the last record of an ascending-turn list (own[len(own)-1]); it does not walk records newest first. If the newest record is unreadable, the code keeps the previous goal instead of falling back to the next-newest prompt. The wording implies a fallback that does not exist.
  - Evidence: intent.go:246: `rec := own[len(own)-1]` ... `if st != textWhole || text == "" { return }`. Commit body: 'Current work now comes from the session's own prompt records, newest first'.
  - Fix: Reword the comment to 'from the session's newest own prompt record (an unreadable newest record leaves the goal as it was)'. If the commit is reworded before batching into candidate 8, fix the message there too. Otherwise leave it.

## Fix seat (review resolution) — status `done`, head `71d4b191`

### Root cause

F-C7-UAT06-1 (implementer, 84b6f802): CurrentWork.Goal was derived in encodeSegmentLocked from the graph's userprompt nodes in the encoded segment's turn range. Those nodes are keyed by turn only (dag.UserPromptNode(turn)), so different sessions share them. The fork's segment 2-4 therefore took the parent's turn-4 record_eliminated prompt, and prompts in the still-open segment were never counted. Review finding (fixed in 71d4b191): after 84b6f802, the graph fallback was gated only on the type assertion `src.Store.(store.SessionPrompts)`. A store that has the capability but cannot answer derived no goal at all. FSStore.SessionPrompts returns core.ErrDegraded past promptScanLimit (1<<18 records) and fails on any ctx or read error, and refreshIntentLocked returns at `if !ok` before deriveCurrentWorkLocked runs. The graph path that was kept for non-capable stores still had the cross-session collision.

### Summary

PRODUCT CODE CHANGED (internal/checkpoint). Candidate 8 should batch it.

F-C7-UAT06-1 is fixed on closeout/w19-forkwork, and the head is now 71d4b191. Commit 84b6f802 is the implementer's fix. Commit 71d4b191 fixes the one review finding. The base was a357d187.

**How a fork inherits (unchanged by either commit).** The fork's draft starts with the parent's prompt records up to the fork moment (d.fork.fromRecords), or the parent checkpoint's copy when records can't be listed. The parent's original is the Original. The evolution lists the parent's later prompts first, then every prompt of the fork's own. Eliminations and decisions carry through seedTierOne, carriedBy and carryDecisionsLocked.

**Fix from 84b6f802.** deriveCurrentWorkLocked runs at the end of refreshIntentLocked, which runs at Begin, after each Advance and at PreCompact.
- The goal comes from the session's own newest prompt record (store.SessionPrompts), so prompts in the open segment count too: `truncRunes(firstSentence(text), goalMaxRunes)`.
- SetCurrentWork still wins (workExplicit).
- A fork with no prompt of its own yet keeps an empty goal. Before the fix, a fork with a closed segment got the parent's prompt here (the same defect).
- Plain --resume (same session id) still gets the session's newest prompt.

## Review resolution

**Finding (minor): "a store that has SessionPrompts but fails never derives Current work". CONFIRMED and fixed in 71d4b191.**
- *Check:* intent.go returned at `if !ok` before deriveCurrentWorkLocked, and writer.go:925 gated the graph form only on the type assertion. FSStore.SessionPrompts (internal/store/prompt_recovery.go:92) returns core.ErrDegraded past promptScanLimit.
- *Red-first row:* `TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph` has two subtests. One uses a wrapper that hides the capability; the other uses a wrapper whose SessionPrompts returns core.ErrDegraded. Each subtest has the session's prompts at turns 0 and 2, another session's prompt at turn 3, and a closed segment 0-3.
- *On 84b6f802 it was red both ways:*
  - degraded: the goal was "" (the reviewer's regression);
  - no capability: the goal was "Build the CSV importer." The cross-session collision was still alive on the graph path that was kept, which the review did not mention.
- *The fix follows the reviewer's suggestion:*
  - New in-memory field `Draft.promptsAnswered`. refreshIntentLocked sets it from sessionPromptRecords' ok on every refresh.
  - encodeSegmentLocked uses the graph form whenever `!d.promptsAnswered && !d.workExplicit`.
  - New helper `ownNewestPrompt` takes the highest-turn prompt node in the segment and skips any node whose Ref resolves (src.Store.ToolUse) to a record of a different session. A node whose record can't be read is not skipped (no evidence it is foreign); readPromptText then decides, as before.
  - When the fallback writes the goal it clears goalFrom. Otherwise the next refresh that can answer would see the same newest record ID and keep the graph-derived goal.
- No new numbers or constants.
- *After the fix:* both subtests pass and all five rows from 84b6f802 still pass (inside the full-package run).

**Known limit, not a defect introduced here.** In the degraded case the goal comes only from closed segments, so prompts in the open segment are missed. That is the same as the base's behaviour, and user_intent is also left as it was while degraded (the existing Warn). Also, if a later prompt from another session replaced the Ref on a shared turn's node, this session's prompt at that turn can't be seen from the graph. The fallback then picks an older prompt of its own or nothing, never the other session's.

**Checks, all passing on 71d4b191:**
- go vet on Windows and with GOOS=linux, and golangci-lint on ./internal/checkpoint/... (exit 0).
- devtool fmt-check (exit 0). The working diff had no CRLF; I wrote the files through a UTF-8 script after a first attempt in the default cp1252 encoding broke the build and was reverted.
- devtool lint --only=docmarkers,runpatterns: both PASS.
- Full ./internal/checkpoint/... once: ok in 102 s.
- Three daemon fork rows and two e2e rows, each by exact name: all pass.
- No docs or generated inputs moved, so ./test/docs and the gen-*-docs --check commands were not needed.

### Commits

- 84b6f802 fix(checkpoint): derive current work from own prompt records
- 71d4b191 fix(checkpoint): keep a session-own graph goal when prompts fail

### Tests

- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint -run '^TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph$' -count=1  (on 84b6f802 + the new test, before the fix)` — RED as intended: subtest 'capability degraded' got goal "" (review finding); subtest 'without the capability' got the other session's "Build the CSV importer." (collision still alive on the kept graph path)
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint -run '^TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph$' -count=1 -v` — PASS, both subtests, after the fix
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/checkpoint/... -count=1` — ok internal/checkpoint 102.062s; ok internal/checkpoint/checkpointtest 1.084s
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./internal/daemon -run '^(TestSessionStartFork_RecordsTheForksLineage|TestCompactRehydration_ForkShowsTheParentsOriginal|TestService_ResumedOrForkedSessionInheritsProjectCheckpoint)$' -count=1 -v` — all 3 PASS
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go test -p 2 ./test/e2e -run '^(TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint|TestV5_PreCompactToRehydrateToDroppedRoundTrip)$' -count=1 -v` — both PASS (ok test/e2e 25.286s)
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go vet ./internal/checkpoint/... && GOOS=linux go vet ./internal/checkpoint/...` — clean
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/checkpoint/...` — exit 0, no findings
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run ./tools/devtool fmt-check` — exit 0
- `cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-forkwork && go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers

### Open issues

- Not run here: -race on internal/checkpoint (the coordinator runs -race at night).
- Not run here: candidate 8's live re-run of UAT-06, which should confirm checkpoint 0005's goal is the fork's 45 correction on a real host.
- docs/uat.md line 859 (the UAT-06 finding) belongs to the docs seat. Mark it fixed by 84b6f802 + 71d4b191 once candidate 8's live lane confirms.
- Outside this seat's scope and already routed by the lane: UAT-06's expected result still says "Section 2's first unit is the verbatim original intent", which disagrees with D50 (evolution first, original last).
- Known limit, unchanged from the base: while SessionPrompts cannot answer (store degraded past promptScanLimit), the goal is taken only from closed segments' graph nodes and user_intent is left as it was. A prompt whose shared-turn node another session's later capture re-pointed cannot be seen there. The fallback then gives an older own prompt or nothing, never another session's.


## Verify — verdict `sound`, 0 finding(s)

An independent verify seat re-checked the fix seat's head against every review finding and found nothing still wrong.

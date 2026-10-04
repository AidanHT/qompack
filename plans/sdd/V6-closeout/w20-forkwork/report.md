# Wave 20 forkwork (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-forkwork`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **minor** `internal/checkpoint/intent.go:231 (deriveCurrentWorkLocked call) / internal/checkpoint/writer_test.go:438`: No test checks the derived goal of a non-fork session that has only one prompt, which is the most common shape for a first compaction. A mutant that passes the evolution-sliced `own` (the records with the first one removed) instead of `newest` gives every one-prompt session an empty current_work.goal. The whole internal/checkpoint suite still passes against it. The only one-prompt goal assertion (writer_test.go:438) is `require.NotContains(goal, "stale summary")`, which an empty goal also satisfies.
- **minor** `internal/checkpoint/writer.go:932 (`d.goalFrom = ""` in the graph fallback)`: No test covers the fix seat's goalFrom reset, the line that brings the records-derived goal back after the graph fallback ran. Removing it leaves the whole suite green. Without it, after any transient SessionPrompts failure the goal stays stuck on an older closed-segment prompt for the rest of the draft's life, even after SessionPrompts answers again.
- **minor** `internal/checkpoint/intent.go:248 (goalFrom early return)`: No test covers the read-once guarantee that the goalFrom field exists for. With the guard deleted the suite stays green, and a newest prompt larger than evolutionReadLimit (a paste of up to 4 MiB, the hookCaptureMaxBytes cap) is then read whole and run through fromStore at every refresh, meaning every idle Advance tick, instead of once per draft.
- **minor** `internal/checkpoint/writer.go:926-935`: While SessionPrompts is failing, the graph fallback overwrites a goal that came from the session's newest prompt record with an OLDER prompt: the newest own prompt in a closed segment. A transient failure (for example ctx expiry inside the 2 s idle Advance budget, since SessionPrompts checks ctx.Err() per record) therefore moves current work backwards. If PreCompact's RefreshIntent also fails in that window, the sealed checkpoint carries the older goal. The intent is deliberately 'left as it was' when degraded, but current work is not.
- **minor** `internal/checkpoint/intent.go:247`: Current work now always follows the session's newest prompt record, open segment included, and Qompack's own slash commands are captured as ordinary UserPromptSubmit records. A user who runs /qompack:status (or /qompack:why <id>, or /qompack:dropped) just before compacting gets current_work.goal = "/qompack:status". That is rendered in the rehydration's section 5 as the task in flight. On candidate 7 the open segment's prompts never counted, so this is new exposure that the fix introduces, though the spec text ('the most recent prompt') literally allows it.
- **minor** `internal/checkpoint/intent.go:247 (with store/prompt_recovery.go:83-89 sort order)`: 'Newest' means highest turn, and the observer deliberately keeps turns in publication order. When a prompt that went to a client spool is published after one its host sent later (the observer counts this as observer.prompt_out_of_host_order, prompt_delivery.go:107), current work names the older prompt. The record's TS (the host stamp) holds the true order. This class predates the fix (the graph form was turn-keyed too), but D59's 'the fork's own newest prompt' is not met in that case.
- **minor** `internal/checkpoint/intent.go:252`: When the newest own prompt record is unreadable (object missing or corrupt), the goal is 'left as it was'. On a fresh draft (cold PreCompact, the successor Finalize opens, a new daemon) that means EMPTY, even though older own prompts are readable and the evolution in the same checkpoint carries them. The checkpoint ends up with no current work although the session has prompts.
- nit `internal/checkpoint/current_work_fork_test.go:22-24`: The header comment still says current work comes from 'the session's own prompt records, newest first'. The code takes only the single highest-turn record and has no fallback. The review flagged this as a nit and it was not reworded.
- nit `internal/checkpoint/intent.go:244 (len(own)==0 early return) with writer.go resumeDraft`: A fork draft persisted by candidate 7 that already holds the parent's prompt as its goal (the F-C7-UAT06-1 state) keeps that stale goal after an upgrade to candidate 8, until the fork records a prompt of its own. deriveCurrentWorkLocked returns early when the fork has no own records, and nothing clears a derived goal that came from elsewhere. This needs a mid-session upgrade of a prompt-less fork, so it is very narrow.
- nit `internal/checkpoint/intent.go:251-254 (deriveCurrentWorkLocked)`: goalFrom is recorded only after a non-empty read. If the newest prompt is past evolutionReadLimit (so not cached) and its text is empty after fromStore (pure injection markup), it is read whole with io.ReadAll at every refresh: every idle-tick Advance and every PreCompact. This is cheap in practice and found by code reading only.
- nit `internal/checkpoint/intent.go:305 (selectEvolutionLocked) with intent.go:308-312`: Pre-existing, on the refresh path that c8 now runs on every Advance and PreCompact. An oversized newest prompt (textTooLarge) is not cached, so every refresh reads evolutionReadLimit+1 bytes (about 37.6 KB) of it again. c8's derive does not add to this, because goalFrom memoizes the whole read.
- nit `internal/checkpoint/current_work_fork_test.go:23-24; internal/daemon/status_order_test.go:21-24`: Two test comments still overstate their evidence; both review nits were left unfixed. The forkwork test header says current work comes from the session's own prompt records 'newest first', but the code takes own[len(own)-1] and does not fall back to an older prompt. The status-order comment says every base run failed 'each by the fourth read', but the statusorder reviewer saw one fail at read 5.

## impl:forkwork: status `done`, head `6dfc8d2b33a88a0d4ccb9370ad2337618c6f136c`

### Root cause

deriveCurrentWorkLocked took own[len(own)-1] blindly. It had no rule for skipping slash commands, no walk back past an unreadable or blank prompt, and no clear for a goal no own prompt supports. It remembered its result only after a successful non-empty read. The graph fallback overwrote whatever goal was held without comparing turns, so a failing SessionPrompts moved current work back to a closed segment's older prompt. selectEvolutionLocked did not remember a record it had found too long to read in full, so it re-read 28 KB of it at every refresh.

### Summary

PRODUCT CODE CHANGED (internal/checkpoint/intent.go, writer.go, source.go only).

Branch closeout/w20-forkwork in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-forkwork, 2 commits on 738d67c7, working tree clean, nothing pushed.

Root causes:
- deriveCurrentWorkLocked looked only at own[len(own)-1]. It had no rule for skipping a prompt, no fallback to an older one, and remembered its result (goalFrom) only after a successful non-empty read.
- The graph fallback in encodeSegmentLocked was gated only on !promptsAnswered. It did not check whether the goal it held came from a newer prompt.
- selectEvolutionLocked did not remember a record it had found too long to read in full, so it read it again on every refresh.

Product change (commit 6dfc8d2b):
- **How the goal is found.** The derived goal is the newest own prompt that can give one (goalOf). The walk goes back from the newest record, at most goalWalkLimit (= maxIntentEvolution) records. It skips a slash-command invocation, a blank prompt and a prompt whose bytes cannot be read.
- **Slash-command rule.** Any "/qompack:" invocation is skipped, whatever its arguments, and so is a prompt that is only one bare "/name" token. Another command's arguments are kept, and a path is not treated as a command.
- **Clearing a stale goal.** When every own prompt was read and none can give a goal, a held derived goal is cleared. This covers a candidate-7 draft whose fork goal is the parent's prompt.
- **Remembering the walk.** The walk is remembered per newest record whether or not it found a goal. It is not remembered when the context ran out mid-walk.
- **Graph fallback.** It now replaces the goal only with a prompt at a later turn than the held goal came from (new goalTurn/goalTurnSet fields). It applies the same skip rules and walk bound. The goalFrom reset it used to do is removed (proof under finding (b)).
- **Oversized prompts.** The evolution remembers the record it stopped at for being too long (d.oversized). A refresh no longer re-reads 28 KB of that record.
- **Order stays by captured turn.** Decision documented in code and pinned by a row.

User intent (Original/Evolution) is unchanged: every prompt is still kept there verbatim, slash commands included.

Proof that the rows bite:
- Every behaviour change has a row that was red on 738d67c7.
- Each mutant was run with -overlay against the current-work rows and every one was killed. The audit's m4 and m6, the two perf-nit mutants, the turn gate, the slash skip, walking only the newest record, removing the walk bound, removing the clear, removing the ctx guard, and ordering by host stamp instead of turn.
- Audit m4 applied to the base code is also killed, by the new one-prompt row.

docs/ was not touched. Docs follow-ups for the coordinator are listed under needs_owner.

### Commits

- 2594c7a5316c0f71fb2e1a06ea89e94e64137193 test(checkpoint): pin a one-prompt session's derived goal
- 6dfc8d2b33a88a0d4ccb9370ad2337618c6f136c fix(checkpoint): close the current-work derivation edges

### Findings resolution

- **fixed**: (a) intent.go:231 / writer_test.go:438 — no row checks a one-prompt non-fork session's goal (mutant m4 survives)
  - Held: the shipped code was correct, but no test covered this case. Commit 2594c7a5 is tests only and passes on the base product code (verified with a stash). It adds TestOnePromptSessionCurrentWorkIsItsPrompt in current_work_fork_test.go, which asserts the goal exactly. It also turns TestAdvanceStripsInjectionsFromStoredPrompts' NotContains into an exact Equal against StripInjectionsCount(injected). Killed: m4 on the base code fails with expected "We are building a rate limiter for the Kite API gateway.", actual "". m4 on HEAD fails TestOnePromptSessionCurrentWorkIsItsPrompt and TestOversizedNewestPromptIsReadOncePerDraft/a_pasted_injection_block. The skeptics' correction holds: writer_test:438 was a two-prompt session, not a one-prompt one.
- **rebutted**: (b) writer.go:932 — no row covers the goalFrom reset in the graph fallback
  - On the base code the line mattered. TestCurrentWorkMovesForwardAroundAPromptListFailure is green on base and would fail there if the reset were removed. Under (d)'s turn gate the reset never changes the outcome, so the line was removed and the reason is in the writer.go comment. Proof: the fallback now takes only an own node at turn u, later than the held goal's turn t. The node's record has the same root and text as what the records path sees. If that record had been listed at the last successful derivation, the newest-first walk would have reached it before t and taken it, unless a transient read failure skipped it. So it was not listed, and the newest record has changed when the records answer again. That re-derives the goal with or without the reset. In the transient case both versions give the same goal. In the non-production case where a node's root differs from its record's, the reset would move current work backwards. Mutant m2 is now the shipped code. The auditor's scenario (transient failure, recovery, then the goal still correct) is pinned by TestCurrentWorkMovesForwardAroundAPromptListFailure and TestTransientPromptListFailureNeverMovesCurrentWorkBackwards.
- **fixed**: (c) intent.go:248 — no row covers the goalFrom read-once guard (mutant m6 survives)
  - Held. New row TestOversizedNewestPromptIsReadOncePerDraft uses promptProbeStore, which wraps Open to record the bytes read on each open. Over Begin, 5 Advances and a RefreshIntent, it asserts exactly 1 whole read and exactly 1 bounded read of the oversized root. It counts operations and has no timing. Killed: m6 (guard removed) fails both the a_long_paste and a_pasted_injection_block subtests on 'read whole once, for the goal'. The skeptic's correction is adopted: the row counts whole reads rather than a 2x-bytes bound, and refreshes happen at Begin, Advance-with-or-without-segments and PreCompact, not on every idle tick.
- **fixed**: (d) writer.go:926-935 — a transient SessionPrompts failure lets the graph fallback move current work backwards
  - Held. TestTransientPromptListFailureNeverMovesCurrentWorkBackwards was red on base: expected the 45 correction, actual the 60 one, both persisted and in the sealed checkpoint. Fix: goalTurn/goalTurnSet records the turn the held goal was read from, set by either derivation. The fallback applies only when !goalTurnSet || turn > goalTurn. TestCurrentWorkMovesForwardAroundAPromptListFailure pins that the gate still lets the fallback move FORWARD to a newer own prompt while degraded, and that the records take over again on recovery. Mutant 'gate' kills both rows. Note: the fallback only fires on the pass after a failed refresh, because Advance encodes before it refreshes; both rows model that.
- **fixed**: (e) intent.go:247 — /qompack: and other bare slash-command prompts become current_work.goal
  - Held. Rule decided from capture evidence: Claude Code hands UserPromptSubmit the prompt as typed. The live stores under plans/sdd/V6-closeout/live/**/index_tool_use.jsonl hold "/qompack:status", "/qompack:why dec_...", "/qompack:dropped --json", "/qompack:recall ..." and "/qompack:pin <rule>". No built-in such as /compact appears in any of them despite many compactions. Rule (isCommandInvocation): skip any "/qompack:" invocation whatever its arguments, since those commands inspect or annotate the session and /qompack:pin's rule becomes an invariant. Also skip a prompt that is only one bare "/name" token, where a name is a letter or digit followed by letters, digits, '-', '_', '.' or ':'. Kept: another command with arguments ("/fix-issue 42 ...", whose arguments are the user's own words) and paths ("/usr/local/bin/..."). The evolution still lists the command verbatim, as the UAT accepted; only current work skips it. Rows TestSlashCommandPromptIsNotCurrentWork (8 subtests, 6 red on base) and TestSlashCommandPromptIsNotCurrentWorkFromTheGraph (red on base) cover the records path and the graph path. Mutant 'slash' kills them.
- **rebutted**: (f) intent.go:247 — 'newest' is by turn while publication order can differ from host order
  - Turn is kept, documented in deriveCurrentWorkLocked's comment and pinned by TestCurrentWorkFollowsCapturedTurnOrder. Mutant 'ts_order', which sorts by record TS, kills it. Why: (1) D35(b) makes captured turns canonical and never renumbered, and architecture.md section 7 says the same. (2) The evolution in the same checkpoint lists the records in turn order; choosing current work by TS would contradict the evolution's last entry exactly when the order is disputed. (3) The race is a documented, counted and warned residual (D35(b)/D38, docs/cannot-do.md), with a window of about two 2-second spool checks. (4) record TS is the hook's wall clock, or the observer's clock when req.TS is 0, so a clock step can reorder it, while turns are strictly increasing. This is not a regression: the graph-based form also ordered by turn.
- **fixed**: (g) intent.go:252 — an unreadable newest prompt leaves a fresh draft's goal empty
  - Held, implemented as the task directs. TestUnreadableNewestPromptFallsBackToTheNewestReadableOne was red on base (actual ""). It now gives the 60 correction, the same prompt the evolution ends with. The walk is bounded by goalWalkLimit, and the graph fallback walks the same way. Mutant 'walk_one' kills it. Recorded limit: the records path stays authoritative and is not gated by turn. If a newer prompt that was readable becomes unreadable later (corruption), the goal can step back to the newest readable prompt, which is what this finding asks for.
- **fixed**: (h) nit — a candidate-7 fork draft persisted with the parent's prompt as goal keeps it after upgrade
  - Decision: a resumed draft derives its goal again. goalFrom and goalTurn are in memory only, so Begin's refresh walks again. When every own prompt was read and none gives a goal (exhaustive), a held derived goal is cleared, because it cannot have come from this session. TestResumedDraftReDerivesCurrentWork plants a persisted fork draft whose goal is the parent's rateReadLimiter. Subtest no_prompt_of_its_own expects "" and only_a_slash_command_of_its_own expects ""; both were red on base. Subtest a_prompt_of_its_own expects the fork's 45 correction. Mutant 'exhaustive' kills the first two.
- **fixed**: (i) perf nits — goalFrom set only after a non-empty read; oversized newest prompt re-read each refresh
  - goalFrom is now set after every completed walk, found or not, but not after a ctx-interrupted walk. TestInterruptedGoalWalkIsWalkedAgain kills the mutant without the ctx guard. selectEvolutionLocked remembers the record it found too long to read in full (d.oversized, at most one per refresh, since the walk stops there). TestOversizedNewestPromptIsReadOncePerDraft has three subtests: a long paste, a pasted injection block, and an injection block after only a command. Its bounded-read count was 7 against an expected 1 on base. Mutants 'memo_found_only' and 'oversized_memo' are killed.
- **fixed**: nit — current_work_fork_test.go:22-24 header says 'newest first'
  - Reworded to describe the walk. It is now accurate because the code walks newest first.
- **deferred**: nit — internal/daemon/status_order_test.go:21-24 'each by the fourth read' overstates
  - Not in my files (I own internal/checkpoint/** only). Suggested wording for that file's owner: 'within the first few reads'.

### Tests

- `go test -p 1 -count=1 ./internal/checkpoint -run '^(TestTransientPromptListFailureNeverMovesCurrentWorkBackwards|TestSlashCommandPromptIsNotCurrentWork|TestSlashCommandPromptIsNotCurrentWorkFromTheGraph|TestSlashCommandWalkIsBounded|TestUnreadableNewestPromptFallsBackToTheNewestReadableOne|TestOversizedNewestPromptIsReadOncePerDraft|TestResumedDraftReDerivesCurrentWork)$' (rows run against 738d67c7 product code)`: RED as intended: the goal moved back to the 60 correction; slash-command goals in 6 of 8 subtests plus the graph row; the bound row showed /qompack:status; the unreadable-newest row gave an empty goal; 7 bounded reads against 1 expected and an empty injection goal; the resumed-draft rows kept the parent's goal or the slash command. TestCurrentWorkFollowsCapturedTurnOrder, TestCurrentWorkMovesForwardAroundAPromptListFailure and TestOnePromptSessionCurrentWorkIsItsPrompt are green on base: they pin decisions or guard against regressions.
- `git stash -u; go test -p 1 -count=1 ./internal/checkpoint -run '^(TestOnePromptSessionCurrentWorkIsItsPrompt|TestAdvanceStripsInjectionsFromStoredPrompts)$' (commit 2594c7a5 on base product code)`: PASS (tests-only commit stands alone)
- `go test -p 1 -count=1 -overlay <base intent.go with m4> ./internal/checkpoint -run '^TestOnePromptSessionCurrentWorkIsItsPrompt$'`: FAIL as intended (expected the first sentence, actual ""): audit mutant m4 on base is killed
- `11 mutants via go test -p 1 -count=1 -overlay scratchpad/mut/<m>.json ./internal/checkpoint -run <the 12 current-work rows by exact name>`: All killed: m4_own, m6_guard, memo_found_only, oversized_memo, gate, slash, walk_one, walk_unbounded, exhaustive, ctx_guard, ts_order
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: ok 188.794s on HEAD 6dfc8d2b
- `go test -p 1 -count=20 -timeout=30m ./internal/checkpoint -run '^(TestOnePromptSessionCurrentWorkIsItsPrompt|TestTransientPromptListFailureNeverMovesCurrentWorkBackwards|TestCurrentWorkMovesForwardAroundAPromptListFailure|TestInterruptedGoalWalkIsWalkedAgain|TestSlashCommandPromptIsNotCurrentWork|TestSlashCommandPromptIsNotCurrentWorkFromTheGraph|TestSlashCommandWalkIsBounded|TestUnreadableNewestPromptFallsBackToTheNewestReadableOne|TestCurrentWorkFollowsCapturedTurnOrder|TestOversizedNewestPromptIsReadOncePerDraft|TestResumedDraftReDerivesCurrentWork|TestAdvanceStripsInjectionsFromStoredPrompts|TestForkCurrentWorkIsTheForksNewestPrompt|TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph|TestResumedSessionCurrentWorkIsItsNewestPrompt)$'`: ok 135.002s
- `go test -p 1 -race -count=3 -timeout=30m ./internal/checkpoint -run '<same exact-name pattern as the -count=20 row>'`: ok 30.091s
- `go test -p 1 -count=1 -timeout=30m ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_PreCompactToRehydrateToDroppedRoundTrip|TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint|TestE2E_SessionStartCompactRestoresCheckpointItems)$'`: ok 43.220s (all 5 PASS)
- `go test -p 1 -count=1 -timeout=30m ./test/guards`: ok 226.851s
- `GOOS=windows|linux|darwin go vet ./internal/checkpoint`: clean on all three
- `golangci-lint run ./internal/checkpoint/... (pinned build; GOOS windows, linux, darwin)`: clean on all three (one errcheck on an unchecked type assertion in the new test helper fixed first)
- `go run ./tools/devtool fmt-check`: clean
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers

### Criterion changes

- current_work.goal skips prompts that are a slash-command invocation: any '/qompack:' invocation, or a single bare '/name' token. A command with arguments, other than Qompack's own, is kept. Reason: such a prompt states no task, and the captured form is the typed text (live UAT stores).
- current_work.goal is the newest own prompt that can give a goal, walking back at most goalWalkLimit records past unreadable, blank and command prompts, rather than the newest record only. Reason: a fresh draft with an unreadable newest prompt was left with no current work (finding g).
- A held derived goal is cleared when every own prompt was read and none gives a goal. Reason: a resumed candidate-7 fork draft kept its parent's prompt (finding h).
- The graph fallback replaces the goal only with a prompt at a later turn than the held goal came from. Reason: current work must never move backwards during a SessionPrompts failure (finding d).
- TestAdvanceStripsInjectionsFromStoredPrompts: the goal check is tightened from NotContains to an exact Equal. Reason: an empty goal satisfied the NotContains.

### Open issues

- internal/daemon/status_order_test.go:21-24 comment nit ('each by the fourth read' should be 'within the first few reads') is outside my files; it is left for that file's owner.
- test/docs and the gen-*-docs --check commands were not run: no docs or generated inputs moved.

### Needs owner

- goalWalkLimit (intent.go) is a named constant equal to the existing maxIntentEvolution (64), not a new number. It caps how many of the newest prompt records one goal derivation, and the graph fallback, look at. A session whose newest 64 prompts are all skipped slash commands keeps the goal it had, or has none on a fresh draft (pinned by TestSlashCommandWalkIsBounded).
- Criterion change (e): current_work.goal now skips any /qompack: invocation and a bare '/name' prompt; another command's arguments are kept. user_intent is unchanged and still lists every prompt verbatim. A short note in docs/architecture.md section 7, or wherever the rehydration's section 5 is described, would make the rule visible. This seat does not own docs.
- Criterion change (g)/(h): the derived goal is the newest prompt that can give one, walking back past unreadable, blank and command prompts. A derived goal is cleared when every own prompt was read and none can give one. This replaces 'a session with no readable prompt of its own keeps the CurrentWork it has'.
- Decision (f): current work stays ordered by captured turn, not by host stamp (D35(b)). If the owner wants host-stamp order, the evolution's order must change with it, or the two will contradict each other. docs/cannot-do.md's out-of-host-order entry could add one line: 'current work follows captured order too'.
- Decision (b): the graph-fallback goalFrom reset (writer.go:932) was removed rather than covered by a row. It is redundant under the new turn gate; the proof is in the writer.go comment and the findings resolution.

## review:forkwork:0:r1: verdict `needs-fixes`, 4 finding(s)

- **minor** `internal/checkpoint/intent.go:272 (d.goalFrom = newest), with intent.go:300-303`: Regression from base. goalFrom is now remembered after a walk that skipped the newest prompt because Open failed transiently (textUnreadable, with the ctx still live). The goal stays on the older prompt until the user types another prompt. The evolution in the same checkpoint does move on, so a sealed checkpoint's current_work.goal contradicts the evolution's last entry. Base set goalFrom only after a successful read, so the next refresh retried and recovered. The same flaw also breaks the seat's proof for removing the writer.go reset in finding (b). Example: newest w and u were unreadable, so the records gave goal t and goalFrom=w. During a degraded window the fallback moves to u. Once the records answer again, newest==goalFrom, so the readable w is never picked up.
  - Evidence: Probe TestProbeTransientOpenFailureOfNewestPrompt (overlay test file, flakyOpenStore fails Open of the turn-4 root twice, i.e. one refresh's bounded evolution read plus the goal walk's read). Steps: prompts 0 and 2, begin, prompt 4 (the 45 correction), advance with the failure, advance x2, precompact. HEAD: evolution == [60, 45] but goal == "Correction: the limit must be 60 ..." (FAIL, expected the 45 goal). The same probe on a git-archive copy of 738d67c7: PASS (goal recovers to 45).
  - Fix: Have newestGoalLocked also report whether it skipped any textUnreadable record before it stopped (for example a `complete` bool that is false once one is skipped). In deriveCurrentWorkLocked, set d.goalFrom = newest only when that walk read every record it passed whole. Still install the found goal and goalTurn either way. A permanently missing root then costs one failed Open per refresh and no bytes. Add a red-first row shaped like the probe: Open of the newest root fails once (or twice) in one refresh, then succeeds, and the sealed goal must equal the evolution's last entry. Then restate the (b) proof under that invariant.
- **minor** `internal/checkpoint/writer.go:936 with source.go:184-190 (goalTurn/goalTurnSet are in-memory only)`: The new turn gate does not cover a resumed draft. After a daemon restart goalTurnSet is false. If SessionPrompts is failing at that point (persistent core.ErrDegraded past the scan limit, or ctx expiry), the next Advance that encodes a closed segment replaces the persisted goal with the closed segment's older own prompt. That is the backwards move finding (d) said must never happen, and the restart path leaves it open.
  - Evidence: Probe TestProbeResumedDegradedDraftMovesBackwards (overlay). Steps: prompts 0 and 2 in closed seg 1 [0,3], prompt 4 (45) open, plantDerivedGoal(45). Then setDegraded(true), f.begin() (resumed; the persisted goal is asserted == 45), advance, advance(1). Result: goal == "Correction: the limit must be 60 ..." (FAIL, expected 45).
  - Fix: Persist goalTurn in the draft wire (next to WorkExplicit, as an optional field so a candidate-7 file decodes it as unknown) and restore it in resumeDraft. Or, in the fallback, when !goalTurnSet on a resumed draft whose held goal is non-empty, keep the held goal rather than overwrite it: the records derivation still re-derives (and clears a c7 parent goal) once they answer. Pin this with a resumed-draft-degraded row like the probe.
- **nit** `internal/checkpoint/writer.go:1474 and :1483 (ownNewestGoal walk bound and skip-unreadable)`: The graph fallback's two new behaviours have no row. Removing its goalWalkLimit bound, or changing `continue` to `break` on an unreadable node (so it no longer walks back past an unreadable newest node), leaves every current-work, fork, prompt and intent row green. The seat's 'walk_unbounded' kill covers only the records walk.
  - Evidence: Mutants graph_unbounded (bound removed) and graph_unreadable_stop (continue->break) were run with -overlay against -run 'CurrentWork|Slash|Oversized|Goal|Fork|Prompt|Intent'. Both: ok (survived).
  - Fix: Add two degradedPromptsStore rows. (1) A closed segment whose newest own node's root is missing: the goal comes from the next older own node. (2) GoalWalkLimitForTest slash-command nodes on top of a real prompt in one closed segment: the goal stays unset/held.
- **nit** `internal/checkpoint/intent.go:298, :300 (walk/exhaustive bounds), :333 (TrimSpace), :365 (isCommandToken leading-punctuation rule)`: Several changed lines survive mutation. `len(own)-i <= goalWalkLimit` to `<` and `exhaustive = len(own) <= goalWalkLimit` to `<` both survive: TestSlashCommandWalkIsBounded uses 66 records with the real prompt at position 65, so an under-walk by one is not seen. `strings.TrimSpace(g) == ""` to `g == ""` survives: no row has a whitespace-only prompt, which is a behaviour change from base, where such a prompt became the goal. Dropping `i > 0 &&` in isCommandToken survives: no row covers "/-x" or "/_". Clearing goalTurnSet in the exhaustive case, setting goalTurn in the fallback, and `>` to `>=` also survive, but those are effectively equivalent given ascending segment encoding.
  - Evidence: Overlay mutants walk_lt, exh_lt, trim_g and tok_dash (each a single-line edit) run against the current-work/fork/prompt/intent rows: all ok. Killed for comparison: tok_off (TestSlashCommandPromptIsNotCurrentWork/a_bare_command), graph_goalof (TestSlashCommandPromptIsNotCurrentWorkFromTheGraph), found_turn_noset (both prompt-list-failure rows), exhaustive clear (TestResumedDraftReDerivesCurrentWork x2).
  - Fix: Tighten TestSlashCommandWalkIsBounded to also place the real prompt at exactly position goalWalkLimit (it must be found) and at goalWalkLimit+1 (it must not). Add a whitespace-only newest prompt to TestSlashCommandPromptIsNotCurrentWork (expect the older goal). Add a "/-flag" case that is kept as a goal.

## fix:forkwork:r1: status `done`, head `2780f75cf634d1df54e937ed7960267987b28beb`

### Root cause

Round 1: deriveCurrentWorkLocked took own[len(own)-1] blindly, with no skip rule, no walk back past an unreadable or blank prompt, and no clear. It remembered its result only after a successful non-empty read. The graph fallback overwrote any held goal without comparing turns, and the evolution re-read an oversized record at every refresh. Round 2 (review): the round-1 memo marked a walk as done even after it skipped a record it could not read, so a transient Open failure left the goal on an older prompt until the next user prompt. The fallback's turn gate was in memory only, so a daemon restart during a SessionPrompts failure could move current work backwards again.

### Summary

PRODUCT CODE CHANGED (internal/checkpoint/intent.go, writer.go, source.go and draft.go only).

Branch closeout/w20-forkwork in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-forkwork now has 3 commits on 738d67c7. The working tree is clean and nothing is pushed.

Root causes (round 1, unchanged):
- deriveCurrentWorkLocked took own[len(own)-1] blindly. It had no skip rule, no walk back past an unreadable or blank prompt, and no clear for a goal no own prompt supports. It remembered its result only after a successful non-empty read.
- The graph fallback overwrote whatever goal was held, without comparing turns.
- selectEvolutionLocked re-read an oversized record at every refresh.

Round-1 product change (6dfc8d2b):
- The goal is the newest own prompt that gives one, walking back at most goalWalkLimit records past slash commands, blank prompts and unreadable prompts.
- Slash-command rule: skip any /qompack: invocation, and a prompt that is a single bare /name token.
- A held derived goal is cleared when every own prompt was read and none gives a goal.
- The graph fallback moves the goal only to a later turn (goalTurn).
- d.oversized remembers the record the evolution found too long, so it is not read again.
- Current work stays ordered by captured turn (D35(b)).

## Review resolution (round 1 review, commit 2780f75c)

Both minor findings held and are fixed, each with a row that fails on 6dfc8d2b.

**1. Goal stuck on an older prompt after a transient read failure.**
- Fix: newestGoalLocked now returns `complete`: true only when every record the walk passed was read whole. goalFrom is set only on a complete walk. An incomplete walk still installs the goal it found and its turn, and the next refresh walks again.
- A root that is gone for good costs one failed Open per refresh and no bytes read, the same as the base code.
- Row: TestTransientReadFailureOfTheNewestPromptIsReadAgain. On HEAD 6dfc8d2b it fails with expected 45, actual 60; it passes on 738d67c7.

**2. The turn gate was lost on a daemon restart.**
- Fix: goalTurn is persisted as an optional `goal_turn` key in the draft file. It is restored in resumeDraft.
- setGoalTurnLocked marks the draft dirty when only the turn changes. That happens when a repeated prompt gives the same goal text, the evolution is unchanged, and so nothing else would trigger a write.
- A file without goal_turn but holding a derived goal was written by candidate 7. Candidate 7 derived that goal from a segment it had already encoded, so any segment the fallback encodes later lies past it, and taking its prompt is never a backwards move. For a candidate-7 fork that held its parent's prompt, taking it is the fix.
- Rows: TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails (fails on HEAD and on base with expected 45, actual 60). TestResumedDraftKeepsTheTurnOfAnUnchangedGoal (kills the mutant that removes the dirty marking; without it the goal moves back to the restated original). TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph (pins the candidate-7 decision).

**3. The (b) proof, restated under the new invariant.** It is in the writer.go comment. If goalFrom is remembered, the walk read every record from the newest down to the goal it took. So a later-turn own node u that the fallback takes was not listed at that walk; if it had been, the walk would have read it before the older goal, seen the same text, and taken it. When the records answer again, either the newest record has changed, so the goal is derived again, or u was published out of host order behind an unchanged newest. In the second case the earlier walk had already found nothing newer that gives a goal, so u is also what the records give. Either way the goalFrom reset is not needed.

**4. Nits: every reviewer mutant is now killed.**
- graph_unbounded and graph_lt: TestSlashCommandWalkFromTheGraphReadsExactlyItsBound, which puts the real prompt at exactly the bound and one past it.
- graph_unreadable_stop (continue changed to break): TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph.
- walk_lt: TestSlashCommandWalkReadsExactlyItsBound.
- exh_lt: a new subtest of TestResumedDraftReDerivesCurrentWork with exactly GoalWalkLimitForTest slash commands.
- tok_dash: a "/-flag" subtest that keeps "/-flag" as the goal.
- trim_g: TestBlankPromptIsNotCurrentWork. A whitespace prompt is always cached as "" by the evolution, so the row also has a case where the evolution never read the blank prompt (it stopped at a newer oversized injection block). That is the only path where TrimSpace matters.
- I also found that the ctx guard had become nearly redundant under `complete`. A new row, TestInterruptedGoalWalkKeepsTheGoalItHas, shows it still matters: with an oversized goal prompt and an interrupted walk, removing the guard lets the goal step back to the cached original.
- The remaining reviewer mutants are equivalent under ascending segment encoding, as the reviewer said: goalTurnSet in the exhaustive case, setting goalTurn in the fallback, and > changed to >=.

**Mutants re-run on 2780f75c** (all with -overlay against the current-work rows). Killed: complete_ignored, no_restore, no_write, turn_dirty, graph_unbounded, graph_lt, graph_unreadable_stop, walk_lt, exh_lt, trim_g, tok_dash, gate, ctx_guard, memo_found_only, m6_guard, no_clear, walk_one, slash, oversized_memo, m4_own. Three of these (no_write, tok_dash, oversized_memo) first showed as killed only because they did not compile. I rewrote them so they compile, then re-ran them, and all three are killed for real. ts_order lives in test-unchanged code and was killed in round 1.

User intent (Original/Evolution) is unchanged. docs/ was not touched.

### Commits

- 2594c7a5316c0f71fb2e1a06ea89e94e64137193 test(checkpoint): pin a one-prompt session's derived goal
- 6dfc8d2b33a88a0d4ccb9370ad2337618c6f136c fix(checkpoint): close the current-work derivation edges
- 2780f75cf634d1df54e937ed7960267987b28beb fix(checkpoint): retry a goal walk past an unreadable prompt

### Findings resolution

- **fixed**: (a) intent.go:231 / writer_test.go:438: no row checks a one-prompt non-fork session's goal (mutant m4 survives)
  - 2594c7a5 adds TestOnePromptSessionCurrentWorkIsItsPrompt and makes TestAdvanceStripsInjectionsFromStoredPrompts check the goal with an exact Equal. m4 on base: killed. m4 on 2780f75c (pass own instead of newest): killed by TestOnePromptSessionCurrentWorkIsItsPrompt and TestOversizedNewestPromptIsReadOncePerDraft/a_pasted_injection_block.
- **rebutted**: (b) writer.go:932: no row covers the goalFrom reset in the graph fallback
  - The reset is redundant under the turn gate, so it was removed rather than covered by a row. The proof is restated in the writer.go comment under the round-2 invariant that goalFrom is remembered only after a complete walk; it also covers out-of-host-order publication. The auditor's scenario is pinned by TestCurrentWorkMovesForwardAroundAPromptListFailure and TestTransientPromptListFailureNeverMovesCurrentWorkBackwards. The reviewer's counterexample (an unreadable newest record w with goalFrom=w) can no longer happen: an incomplete walk does not set goalFrom.
- **fixed**: (c) intent.go:248: no row covers the goalFrom read-once guard (mutant m6 survives)
  - TestOversizedNewestPromptIsReadOncePerDraft counts whole and bounded Opens; it has no timing. m6_guard on 2780f75c is killed in all 3 subtests.
- **fixed**: (d) writer.go:926-935: a transient SessionPrompts failure lets the graph fallback move current work backwards
  - Turn gate (goalTurn) added in 6dfc8d2b. Since 2780f75c it also survives a restart through the draft file's goal_turn key. Rows: TestTransientPromptListFailureNeverMovesCurrentWorkBackwards, TestCurrentWorkMovesForwardAroundAPromptListFailure, TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails and TestResumedDraftKeepsTheTurnOfAnUnchangedGoal. The gate mutant is killed by three rows.
- **fixed**: (e) intent.go:247: /qompack: and other bare slash-command prompts become current_work.goal
  - Rule (isCommandInvocation): skip any /qompack: invocation, and a prompt that is one bare /name token. Evidence: the live UAT stores hold the typed /qompack:* prompts, and no built-in command ever reaches the hook. Rows: TestSlashCommandPromptIsNotCurrentWork (now with a /-flag subtest), TestSlashCommandPromptIsNotCurrentWorkFromTheGraph, TestSlashCommandWalkIsBounded, TestSlashCommandWalkReadsExactlyItsBound and TestSlashCommandWalkFromTheGraphReadsExactlyItsBound. Mutants slash and tok_dash are killed.
- **rebutted**: (f) intent.go:247: 'newest' is by turn while publication order can differ from host order
  - Turn order is kept and documented (D35(b): captured turns are canonical, the evolution uses the same order, and current work must not contradict the evolution's last entry). Pinned by TestCurrentWorkFollowsCapturedTurnOrder; mutant ts_order was killed in round 1.
- **fixed**: (g) intent.go:252: an unreadable newest prompt leaves a fresh draft's goal empty
  - Both walks go back to the newest readable prompt. Rows: TestUnreadableNewestPromptFallsBackToTheNewestReadableOne and, new this round, TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph (fails on base with expected 60, actual empty). walk_one and graph_unreadable_stop are killed.
- **fixed**: (h) nit: a candidate-7 fork draft persisted with the parent's prompt as goal keeps it after upgrade
  - A resumed draft derives its goal again, and the goal is cleared when the walk is exhaustive. Rows: TestResumedDraftReDerivesCurrentWork (4 subtests, including exactly GoalWalkLimitForTest slash commands) and TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph (degraded path). no_clear and exh_lt are killed.
- **fixed**: (i) perf nits: goalFrom set only after a non-empty read; oversized newest prompt re-read each refresh
  - goalFrom is remembered after a complete walk, whether or not it found a goal. d.oversized remembers the oversized record. memo_found_only and oversized_memo are killed by TestOversizedNewestPromptIsReadOncePerDraft.
- **fixed**: nit: current_work_fork_test.go:22-24 header says 'newest first'
  - Reworded in 6dfc8d2b.
- **deferred**: nit: internal/daemon/status_order_test.go:21-24 'each by the fourth read' overstates
  - This file is outside my scope (I own internal/checkpoint/** only). Suggested wording for its owner: 'within the first few reads'.
- **fixed**: REVIEW minor 1: intent.go:272: goalFrom remembered after a walk that skipped a transiently unreadable newest prompt (regression from base)
  - Held: I reproduced the reviewer's probe as TestTransientReadFailureOfTheNewestPromptIsReadAgain. It fails on 6dfc8d2b (expected 45, actual 60) and passes on 738d67c7. Fix in 2780f75c: newestGoalLocked returns complete, and goalFrom is set only when complete is true; the found goal and its turn are still installed. promptProbeStore.failOpens injects the failures by count, with no timing. Mutant complete_ignored is killed.
- **fixed**: REVIEW minor 2: writer.go:936 with source.go:184-190: the turn gate does not cover a resumed draft
  - Held: TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails fails on 6dfc8d2b with expected 45, actual 60. Fix: the optional draftFile.GoalTurn (goal_turn, omitempty) is written by persistLocked and restored by resumeDraft. setGoalTurnLocked marks the draft dirty on a turn-only change, pinned by TestResumedDraftKeepsTheTurnOfAnUnchangedGoal. A candidate-7 file has no goal_turn and keeps the ungated fallback; that is never a backwards move, because candidate 7's goal came from a segment it had already encoded. Pinned by TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph. Mutants no_restore, no_write and turn_dirty are killed. Unknown keys are ignored when the draft is decoded (json.Unmarshal; no DisallowUnknownFields).
- **fixed**: REVIEW nit: writer.go:1474/1483: graph fallback walk bound and skip-unreadable have no row
  - New rows: TestSlashCommandWalkFromTheGraphReadsExactlyItsBound (the bound's last node is found; one past the bound is not) and TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph. Both fail on base. graph_unbounded, graph_lt and graph_unreadable_stop are killed.
- **fixed**: REVIEW nit: intent.go walk/exhaustive bounds, TrimSpace, isCommandToken leading-punctuation
  - walk_lt is killed by TestSlashCommandWalkReadsExactlyItsBound/the_bound's_last_record. exh_lt is killed by the TestResumedDraftReDerivesCurrentWork subtest with exactly the bound. trim_g is killed by TestBlankPromptIsNotCurrentWork/one_the_evolution_never_read: the evolution caches a blank prompt as "", so TrimSpace matters only for a blank prompt the evolution never read, and the row builds that case. tok_dash is killed by the "/-flag" subtest. The other three are equivalent, as the reviewer noted: clearing goalTurnSet in the exhaustive case, setting goalTurn in the fallback, and > changed to >=.

### Tests

- `go test -p 1 -count=1 ./internal/checkpoint -run '^(TestTransientReadFailureOfTheNewestPromptIsReadAgain|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails)$' (on 6dfc8d2b product code, new rows only)`: RED as intended: both expected the 45 correction and got the 60 one
- `go test -p 1 -count=1 -overlay <738d67c7 intent/writer/source/draft.go> ./internal/checkpoint -run '^(TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph|TestBlankPromptIsNotCurrentWork|TestSlashCommandWalkFromTheGraphReadsExactlyItsBound|TestSlashCommandWalkReadsExactlyItsBound|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails)$'`: RED on base for all five. TestTransientReadFailureOfTheNewestPromptIsReadAgain and TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph pass on base (the first is a round-1 regression; the second pins a decision)
- `20 mutants via go test -p 1 -count=1 -overlay scratchpad/mut2/<m>.json ./internal/checkpoint -run <current-work rows>`: All killed: complete_ignored, no_restore, no_write, turn_dirty, graph_unbounded, graph_lt, graph_unreadable_stop, walk_lt, exh_lt, trim_g, tok_dash, gate, ctx_guard, memo_found_only, m6_guard, no_clear, walk_one, slash, oversized_memo, m4_own. The three mutants whose first versions did not compile were rewritten and re-run.
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: ok 222.371s on 2780f75c
- `go test -p 1 -count=20 -timeout=30m ./internal/checkpoint -run '^(TestInterruptedGoalWalkKeepsTheGoalItHas|TestTransientReadFailureOfTheNewestPromptIsReadAgain|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails|TestResumedDraftKeepsTheTurnOfAnUnchangedGoal|TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph|TestSlashCommandPromptIsNotCurrentWork|TestBlankPromptIsNotCurrentWork|TestSlashCommandWalkReadsExactlyItsBound|TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph|TestSlashCommandWalkFromTheGraphReadsExactlyItsBound|TestResumedDraftReDerivesCurrentWork|TestOnePromptSessionCurrentWorkIsItsPrompt|TestTransientPromptListFailureNeverMovesCurrentWorkBackwards|TestCurrentWorkMovesForwardAroundAPromptListFailure|TestInterruptedGoalWalkIsWalkedAgain|TestSlashCommandPromptIsNotCurrentWorkFromTheGraph|TestSlashCommandWalkIsBounded|TestUnreadableNewestPromptFallsBackToTheNewestReadableOne|TestCurrentWorkFollowsCapturedTurnOrder|TestOversizedNewestPromptIsReadOncePerDraft|TestAdvanceStripsInjectionsFromStoredPrompts|TestForkCurrentWorkIsTheForksNewestPrompt|TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph|TestResumedSessionCurrentWorkIsItsNewestPrompt)$'`: ok 289.680s
- `go test -p 1 -race -count=3 -timeout=30m ./internal/checkpoint -run '<same exact-name pattern as the -count=20 row>'`: ok 54.614s
- `go test -p 1 -count=1 -timeout=30m ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_PreCompactToRehydrateToDroppedRoundTrip|TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint|TestE2E_SessionStartCompactRestoresCheckpointItems)$'`: ok 57.180s (all 5 PASS)
- `go test -p 1 -count=1 -timeout=30m ./test/guards`: ok 60.925s
- `GOOS=windows|linux|darwin go vet ./internal/checkpoint`: clean on all three
- `golangci-lint run ./internal/checkpoint/... (pinned build; GOOS windows, linux, darwin)`: clean on all three
- `go run ./tools/devtool fmt-check`: clean
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers

### Criterion changes

- current_work.goal skips prompts that are a slash-command invocation: any '/qompack:' invocation, or a single bare '/name' token (a name starts with a letter or digit, so '/-flag' is kept). Reason: such a prompt states no task, and the captured form is the typed text (live UAT stores).
- current_work.goal is the newest own prompt that can give a goal, walking back at most goalWalkLimit records past unreadable, blank and command prompts. Reason: finding (g).
- A held derived goal is cleared when every own prompt was read and none gives a goal. Reason: finding (h).
- The graph fallback replaces the goal only with a prompt at a later turn than the held goal came from, and that turn now survives a daemon restart (draft-file goal_turn). Reason: finding (d) and review finding 2.
- A goal walk is remembered (goalFrom) only when it read every record it passed. Reason: review finding 1, where a transient read failure held the goal behind the evolution.
- TestAdvanceStripsInjectionsFromStoredPrompts: the goal check is tightened from NotContains to an exact Equal. Reason: an empty goal satisfied NotContains.
- TestSlashCommandPromptIsNotCurrentWork gains a '/-flag' subtest; the blank-prompt case is its own test (TestBlankPromptIsNotCurrentWork), because the intent skips a blank prompt and the table's evolution assertion does not hold for one.

### Open issues

- internal/daemon/status_order_test.go:21-24 comment nit ('each by the fourth read' should be 'within the first few reads') is outside my files; it is left for that file's owner.
- test/docs and the gen-*-docs --check commands were not run: no docs or generated inputs moved. The new goal_turn draft-file key is not described in any doc; a search for the existing draft keys outside .go files found none.
- Not tested: on-disk drafts written by the pre-fix candidate-8 builds (738d67c7 to 6dfc8d2b) have no goal_turn and are treated like candidate-7 files. Candidate 8 is not frozen, so no such drafts should exist outside test lanes.

### Needs owner

- goalWalkLimit (intent.go) equals the existing maxIntentEvolution (64); it is not a new number. It caps how many of the newest prompt records one goal derivation, and the graph fallback, look at. Pinned at exactly the bound and at one past it for both walks.
- Criterion change (e): current_work.goal skips any /qompack: invocation and a bare '/name' prompt; another command's arguments are kept, and so is a '/-flag' prompt. user_intent is unchanged. Docs (architecture.md section 7) could mention the rule; this seat does not own docs.
- Criterion change (g)/(h): the derived goal is the newest prompt that can give one; a held derived goal is cleared when every own prompt was read and none gives one.
- Decision (f): current work stays ordered by captured turn (D35(b)); docs/cannot-do.md's out-of-host-order entry could add 'current work follows captured order too'.
- Decision (b): the graph-fallback goalFrom reset was removed as redundant; the proof is in the writer.go comment.
- Decision (review 2): the draft file gains an optional goal_turn key (omitempty, ignored by older readers). A file without it, i.e. one from candidate 7, keeps the ungated graph fallback, on the argument that candidate 7's goal came from a segment it had already encoded.

## review:forkwork:0:r2: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/checkpoint/intent.go:272-279 (`if complete { d.goalFrom = newest }`), with newestGoalLocked intent.go:300-312`: The round-2 fix brings back finding (c)/(i) in a narrow shape. If any record the walk passes stays unreadable for good (a zero root, or bytes that were never stored or were collected), `complete` is false at every refresh, so goalFrom is never remembered. The walk then re-reads, at every Advance and PreCompact refresh, the goal prompt behind that record. If the goal prompt is past evolutionReadLimit (a paste of up to hookCaptureMaxBytes = 4 MiB), nothing caches it, so it is read whole each time. The new comment at intent.go:276-277 ('A record whose bytes are gone for good costs one failed Open per refresh, and no bytes') is false in this shape. On base, the same shape cost one failed Open per refresh and no bytes.
  - Evidence: Overlay probe TestProbeReviewUnreadableNewestOversizedGoalReadCount (scratchpad/probe/probe_review_test.go, uses only existing helpers). Steps: promptAs(0, rateAsk), promptAs(2, rateCorrection45 + filler past EvolutionReadLimitForTest), lostPromptAs(4), begin, then advance x5, counting reads with promptProbeStore. The goal is correctly the 45 correction, but readsOf(root of the big prompt) gives whole=6, bounded=1, where 1 whole read was expected. Command: go test -p 1 -count=1 -overlay <probe overlay> ./internal/checkpoint -run '^TestProbeReview'. Result: 'expected: 1, actual: 6'.
  - Fix: Remember which record the held goal was read from, for example a new in-memory `goalRec core.ToolUseID` set next to setGoalTurnLocked in the found case and in the fallback (via the node's Ref). In newestGoalLocked, when own[i].ID == d.goalRec, reuse d.cp.CurrentWork.Goal without reading; a record's bytes never change. An incomplete walk then costs only the failed Opens of the unreadable records, and the transient-failure recovery that TestTransientReadFailureOfTheNewestPromptIsReadAgain pins still works. Add a red-first row shaped like the probe: a permanently unreadable newest record, an oversized goal prompt behind it, N refreshes, and exactly one whole read. Fix the comment at intent.go:276-277 to match.
- **nit** `internal/checkpoint/draft.go:372-376, writer.go:525, intent.go:283-286, intent.go:376`: Four changed lines have no row that kills their mutants, and the seat's report does not call them equivalent:
(A) persistLocked writes goal_turn even when goalTurnSet is false;
(B) resumeDraft sets goalTurnSet: true for a file with no goal_turn;
(H) the clear branch drops `complete`, so a transiently unreadable newest record clears a held goal for one refresh;
(L) the /qompack: prefix is tested on the untrimmed text, so ' /qompack:status' becomes the goal.
Mutants A and B differ from the shipped code only when a graph prompt sits at turn 0. Even so, the decision TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph claims to pin (a candidate-7 file keeps the UNGATED fallback) is not actually pinned: its fork prompt is at turn 1, which passes the gate at turn 0 as well. Mutant H can seal an empty goal: the fallback took the session's only goal-giving prompt u, the records then answer while u's Open fails once, and a compaction lands in that window.
  - Evidence: Mutants applied with -overlay and run against the 77 current-work, intent and resume rows (pattern in scratchpad/mut/pat.txt). All of these SURVIVE (rc=0): A_persist_always, B_resume_set_true, H_clear_ignores_complete, L_prefix_untrimmed. These are killed: P_oversized_unreadable, Q_found_complete_true, R_clear_no_len, S_goalfrom_check_outside, T_no_found_setturn (4 rows), U_tok_no_ns. Also surviving, and equivalent or harmless in my reading: C (clear keeps the turn), D (> changed to >=), G (no oversized reset), M (no turn set in the fallback), V (restored turn - 1), E ('/' alone counts as a command).
  - Fix: Add four small rows:
(1) A draft with no derived goal persists no goal_turn. Or: restart into a failing list where the only goal-giving graph prompt is at turn 0, and the fallback still takes it. This kills A.
(2) The candidate-7 row with the fork's own prompt at turn 0 (or the parent's goal at a turn above the fork prompt's). This kills B.
(3) The fallback holds the goal from the only goal-giving prompt u; the records answer while u's Open fails once; the goal is unchanged after that refresh. This kills H.
(4) A ' /qompack:status\n' subtest in TestSlashCommandPromptIsNotCurrentWork. This kills L.

## fix:forkwork:r2: status `done`, head `692e76147070ecff50e78797b4ba96d63003c974`

### Root cause

Round 1: deriveCurrentWorkLocked took own[len(own)-1] blindly. It had no skip rule, no walk back past an unreadable or blank prompt, and no clear. The graph fallback overwrote any held goal without comparing turns, and the evolution re-read an oversized record at every refresh. Round 2: the walk memo (goalFrom) marked a walk as done after it skipped an unreadable record, and the fallback's turn gate was held in memory only. Round 3: goalFrom remembered a whole walk, not each record, so a walk that never completed (a record gone for good) re-read the oversized goal prompt behind that record at every refresh. Separately, the round-1 walk-back had no turn gate of its own: after a restart, a walk that could not read the record its goal came from stepped current work back to an older prompt.

### Summary

PRODUCT CODE CHANGED. Only internal/checkpoint/intent.go, writer.go, source.go and draft.go changed. Round 3 touched intent.go, source.go, writer.go and current_work_test.go.

Branch closeout/w20-forkwork, in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-forkwork, now has 4 commits on 738d67c7. The working tree is clean and nothing is pushed.

Rounds 1 and 2 are unchanged. Current work is the newest own prompt that gives a goal. The walk goes back at most goalWalkLimit records past slash-command, blank and unreadable prompts. A held derived goal is cleared when every own prompt was read and none gives a goal. The graph fallback moves the goal only to a later turn, and that turn survives a restart through the draft file's goal_turn key. Current work stays ordered by captured turn (D35(b)).

## Review resolution (round 2 review, commit 692e7614)

**Minor finding: an incomplete walk re-read the goal prompt at every refresh. It held, and is fixed.**
- Red row: TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce has a permanently unreadable newest record with an oversized goal prompt behind it, then begin, 5 Advances and a PreCompact refresh.
  - On 2780f75c: 7 whole reads where 1 is right.
  - On 738d67c7: the goal is empty.
- Fix: the walk now keeps what each record it read whole gives, in an in-memory goalSeen map keyed by record id, with at most goalWalkLimit entries. A record's bytes never change.
  - Each record is read once per draft while it stays in reach.
  - A record whose bytes are gone for good costs one failed Open per refresh and no bytes, which is now what the comment says.
- This is a general form of the reviewer's goalRec suggestion. It also covers a long /qompack: prompt that the walk passes.

**goalFrom removed.** With goalSeen in place, the audit's mutant m6 (removing the goalFrom guard) survived. goalFrom had become a fast path that no row could tell apart from its removal. I removed it rather than keep two memos.
- The records are now walked at every refresh. They have the last word whenever they answer.
- The writer.go proof of why the fallback needs no goalFrom reset is no longer needed and has been replaced.
- The property m6 guarded (an oversized prompt is not re-read at every refresh) is still pinned. Mutants S1 (no lookup), S2 (no store) and S3 (no install) are each killed by TestOversizedNewestPromptIsReadOncePerDraft and the new row.

**New defect found while checking that goalFrom could go. It is a round-1 regression and is fixed.**
- After a restart, a walk that could not read the record its goal came from took an OLDER prompt's goal: 60 instead of 45.
- TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable is red on 2780f75c and passes on 738d67c7.
- Fix: a walk that did not read every record it passed replaces the goal only with one from the held goal's turn or later.
- A complete walk still has the last word, so a goal_turn past every record heals. That can happen when the store is restored from a backup older than the draft. TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal pins this and kills G2.
- The same gate covers a walk that the context interrupts, so the separate ctx guard was removed. TestInterruptedGoalWalkKeepsTheGoalItHas and TestInterruptedGoalWalkIsWalkedAgain still pass.

**Nits: all four held and are fixed with rows.**
- (A) TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph: a draft with no derived goal persists no goal_turn, so after a restart into a failing list the fallback takes a turn-0 prompt.
- (B) TestResumedCandidateSevenDraftKeepsTheUngatedFallback: a candidate-7 file whose prompt is at turn 0. Row A kills B as well.
- (H) TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable: the only goal-giving record is unreadable after a restart. The goal stays, and a compaction in that window seals it.
- (L) New subtest 'qompack why after whitespace' (' /qompack:why dec_...\n'). A bare ' /qompack:status' would not kill L, because the bare-token rule already skips it, so the subtest gives the command an argument.
- I also removed a redundant `!found` from the clear case. That mutant was a pure logical equivalent.

**Mutation proof on 692e7614.** All runs use -overlay against the 33 current-work rows.
- Killed (27): A_persist_always, B_resume_set_true, H_clear_ignores_complete, L_prefix_untrimmed, S1, S2, S3, G1_gate_removed, G2_no_complete, C_complete_never_false, walk_one, walk_lt, exh_lt, no_clear, m4_own, slash, trim_g, tok_dash, gate, no_restore, no_write, turn_dirty, graph_unbounded, graph_lt, graph_unreadable_stop, oversized_memo.
- m4_own, slash and tok_dash first failed to compile. I rewrote them so they compile, re-ran them, and they are killed for real.
- Equivalent and surviving:
  - G3 (dropping `!d.goalTurnSet ||` in the records gate): goalTurn is always 0 when unset, because setGoalTurnLocked forces it and resume sets it only together with the flag.
  - G4 (`>=` changed to `>`): one turn holds one prompt record, so an equal turn means the same record and the same goal.
- The round-2 mutants complete_ignored, memo_found_only and ctx_guard no longer exist: the code they mutated was removed.

User intent (Original and Evolution) is unchanged. docs/ was not touched.

### Commits

- 2594c7a5316c0f71fb2e1a06ea89e94e64137193 test(checkpoint): pin a one-prompt session's derived goal
- 6dfc8d2b33a88a0d4ccb9370ad2337618c6f136c fix(checkpoint): close the current-work derivation edges
- 2780f75cf634d1df54e937ed7960267987b28beb fix(checkpoint): retry a goal walk past an unreadable prompt
- 692e76147070ecff50e78797b4ba96d63003c974 fix(checkpoint): read each goal record once, never step back

### Findings resolution

- **fixed**: (a) intent.go:231 / writer_test.go:438: no row checks a one-prompt non-fork session's goal (mutant m4 survives)
  - 2594c7a5 added TestOnePromptSessionCurrentWorkIsItsPrompt and an exact Equal in TestAdvanceStripsInjectionsFromStoredPrompts. m4_own re-run on 692e7614: killed by TestOnePromptSessionCurrentWorkIsItsPrompt and TestOversizedNewestPromptIsReadOncePerDraft.
- **rebutted**: (b) writer.go:932: no row covers the goalFrom reset in the graph fallback
  - The reset was removed in round 1 as redundant, and goalFrom itself is now gone (692e7614). The records are walked at every refresh and have the last word when they answer. A walk that cannot read the fallback's prompt does not step back past its turn. The auditor's scenario is pinned by TestCurrentWorkMovesForwardAroundAPromptListFailure and TestTransientPromptListFailureNeverMovesCurrentWorkBackwards.
- **fixed**: (c) intent.go:248: no row covers the goalFrom read-once guard (mutant m6 survives)
  - Read-once is now held by goalSeen, a per-record memo, and the goalFrom guard is removed. With goalSeen in place, m6 had become equivalent, so I removed the code rather than leave a mutant no row can kill. The property is pinned by TestOversizedNewestPromptIsReadOncePerDraft and TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce, which count Opens with no timing. They kill S1, S2, S3 and oversized_memo.
- **fixed**: (d) writer.go:926-935: a transient SessionPrompts failure lets the graph fallback move current work backwards
  - The fallback's turn gate is persisted as goal_turn. Since 692e7614 the records walk is gated too: an incomplete walk never steps back past the held goal's turn. Rows: TestTransientPromptListFailureNeverMovesCurrentWorkBackwards, TestCurrentWorkMovesForwardAroundAPromptListFailure, TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails, TestResumedDraftKeepsTheTurnOfAnUnchangedGoal and TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable. Mutants gate and G1 are killed.
- **fixed**: (e) intent.go:247: /qompack: and other bare slash-command prompts become current_work.goal
  - Rule (isCommandInvocation): skip any /qompack: invocation, tested on the trimmed text, and a prompt that is a single bare /name token. Rows: TestSlashCommandPromptIsNotCurrentWork (now with '/-flag' and ' /qompack:why dec_...\n' subtests), TestSlashCommandPromptIsNotCurrentWorkFromTheGraph, TestSlashCommandWalkIsBounded, TestSlashCommandWalkReadsExactlyItsBound and TestSlashCommandWalkFromTheGraphReadsExactlyItsBound. Mutants slash, tok_dash and L are killed.
- **rebutted**: (f) intent.go:247: 'newest' is by turn while publication order can differ from host order
  - Captured-turn order is kept and documented (D35(b): turns are canonical, the evolution uses the same order, and current work must agree with the evolution's last entry). Pinned by TestCurrentWorkFollowsCapturedTurnOrder. Mutant ts_order was killed in round 1.
- **fixed**: (g) intent.go:252: an unreadable newest prompt leaves a fresh draft's goal empty
  - Both walks fall back to the newest readable prompt. Rows: TestUnreadableNewestPromptFallsBackToTheNewestReadableOne and TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph. walk_one and graph_unreadable_stop are killed. The fallback is now limited by the turn gate when the draft holds a goal from a later turn.
- **fixed**: (h) nit: a candidate-7 fork draft persisted with the parent's prompt as goal keeps it after upgrade
  - A resumed draft derives its goal again, and the goal is cleared when a complete walk finds none. A candidate-7 file (no goal_turn) keeps the ungated fallback. Rows: TestResumedDraftReDerivesCurrentWork (4 subtests), TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph and TestResumedCandidateSevenDraftKeepsTheUngatedFallback (new, at turn 0). no_clear, exh_lt and B are killed.
- **fixed**: (i) perf nits: goalFrom set only after a non-empty read; oversized newest prompt re-read each refresh
  - goalSeen records what each record read whole gives, including when it gives no goal, so neither a goal-giving nor a goal-less oversized record is read twice per draft. d.oversized covers the evolution's bounded read. Pinned by TestOversizedNewestPromptIsReadOncePerDraft and TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce.
- **fixed**: nit: current_work_fork_test.go:22-24 header says 'newest first'
  - Reworded in 6dfc8d2b.
- **deferred**: nit: internal/daemon/status_order_test.go:21-24 'each by the fourth read' overstates
  - This file is outside my scope (I own internal/checkpoint/** only). Suggested wording for its owner: 'within the first few reads'.
- **fixed**: REVIEW (round 1) minor 1: goalFrom remembered after a walk that skipped a transiently unreadable newest prompt
  - Fixed in 2780f75c, where goalFrom was set only after a complete walk. goalFrom no longer exists: every refresh walks again, reading uncached records only. TestTransientReadFailureOfTheNewestPromptIsReadAgain still pins the recovery.
- **fixed**: REVIEW (round 1) minor 2: the turn gate does not cover a resumed draft
  - goal_turn is persisted and restored (2780f75c). Rows: TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails and TestResumedDraftKeepsTheTurnOfAnUnchangedGoal. Mutants no_restore, no_write and turn_dirty are killed on 692e7614.
- **fixed**: REVIEW (round 1) nits: graph walk bound, graph skip-unreadable, intent walk/exhaustive bounds, TrimSpace, '/-flag'
  - Rows were added in 2780f75c. On 692e7614, graph_unbounded, graph_lt, graph_unreadable_stop, walk_lt, exh_lt, trim_g and tok_dash are all killed.
- **fixed**: REVIEW (round 2) minor: intent.go:272-279: an incomplete walk is never remembered, so an oversized goal prompt behind a permanently unreadable record is read whole at every refresh
  - Held. TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce fails on 2780f75c (expected 1 whole read, actual 7) and on 738d67c7 (goal empty). Fix in 692e7614: goalSeen, an in-memory memo of what each record read whole gives, keyed by record id, bounded by goalWalkLimit. The comment now says an unreadable record costs one failed Open and no bytes, which is true. goalFrom became redundant and was removed. Killed: S1_seen_no_lookup, S2_seen_not_kept, S3_no_install.
- **fixed**: REVIEW (round 2) nit A: persistLocked writes goal_turn when goalTurnSet is false
  - TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph checks that the file has no goal_turn, then restarts into a failing list where the only goal-giving prompt is at turn 0, and expects the fallback to take it. A_persist_always is killed.
- **fixed**: REVIEW (round 2) nit B: resumeDraft sets goalTurnSet true for a file without goal_turn; the candidate-7 decision was not actually pinned
  - TestResumedCandidateSevenDraftKeepsTheUngatedFallback: a candidate-7 file holding a different derived goal, with the session's prompt at turn 0. The fallback replaces the goal. B_resume_set_true is killed by this row and by the row for A.
- **fixed**: REVIEW (round 2) nit H: the clear branch dropping `complete` clears a held goal on a transiently unreadable record
  - TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable: the goal held after a restart comes from the session's only goal-giving record, whose Opens fail. The goal is unchanged after that refresh and is sealed so. I used the resume shape because, in the fallback shape the reviewer suggested, goalSeen could serve the record from memory. H_clear_ignores_complete is killed by this row and by TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable.
- **fixed**: REVIEW (round 2) nit L: the /qompack: prefix tested on untrimmed text
  - The shipped code already trimmed, so there was no product change, only a row. New subtest TestSlashCommandPromptIsNotCurrentWork/qompack_why_after_whitespace (' /qompack:why dec_991dbff588ec\n'). It needs the argument, because a bare ' /qompack:status' is also caught by the bare-token rule. L_prefix_untrimmed is killed.
- **fixed**: NEW (found this round): a resumed draft whose goal record is unreadable steps current work back to an older prompt
  - A round-1 regression. TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable fails on 2780f75c (expected 45, actual 60) and passes on 738d67c7. Fix: an incomplete walk replaces the goal only with one from the held goal's turn or later. A complete walk still has the last word, pinned by TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal. G1 and G2 are killed; G3 and G4 are equivalent.

### Tests

- `go test -p 1 -count=1 -overlay <2780f75c intent/source/writer/draft.go> ./internal/checkpoint -run '^(TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable|TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable|TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph|TestResumedCandidateSevenDraftKeepsTheUngatedFallback|TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal)$'`: RED as intended for the two behaviour rows: OversizedGoalReadOnce (expected 1, actual 7) and NeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable (expected 45, actual 60). The four nit rows pass on 2780f75c; they exist to kill mutants A, B, H and G2.
- `same pattern with -overlay <738d67c7 product files + goalWalkLimit stub>`: Only OversizedGoalReadOnce fails (goal empty). The backwards row passes on base, so it is a round-1 regression.
- `28 mutants via go test -p 1 -count=1 -overlay scratchpad/mut3/<m>.json ./internal/checkpoint -run <33-row current-work pattern below>`: 26 killed (listed in the summary); m4_own, slash and tok_dash were rewritten so they compile, then killed for real. 2 equivalent survivors: G3 (goalTurn is 0 when unset) and G4 (one turn holds one record).
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint`: ok 94.939s on 692e7614
- `go test -p 1 -count=20 -timeout=30m ./internal/checkpoint -run '^(TestBlankPromptIsNotCurrentWork|TestCurrentWorkFollowsCapturedTurnOrder|TestCurrentWorkMovesForwardAroundAPromptListFailure|TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph|TestExplicitCurrentWorkSurvivesAPromptRefresh|TestForkCurrentWorkIsTheForksNewestPrompt|TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork|TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal|TestInterruptedGoalWalkIsWalkedAgain|TestInterruptedGoalWalkKeepsTheGoalItHas|TestOnePromptSessionCurrentWorkIsItsPrompt|TestOversizedNewestPromptIsReadOncePerDraft|TestResumedCandidateSevenDraftKeepsTheUngatedFallback|TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph|TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable|TestResumedDraftKeepsTheTurnOfAnUnchangedGoal|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable|TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails|TestResumedDraftReDerivesCurrentWork|TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal|TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph|TestResumedSessionCurrentWorkIsItsNewestPrompt|TestSlashCommandPromptIsNotCurrentWork|TestSlashCommandPromptIsNotCurrentWorkFromTheGraph|TestSlashCommandWalkFromTheGraphReadsExactlyItsBound|TestSlashCommandWalkIsBounded|TestSlashCommandWalkReadsExactlyItsBound|TestTransientPromptListFailureNeverMovesCurrentWorkBackwards|TestTransientReadFailureOfTheNewestPromptIsReadAgain|TestUnreadableNewestPromptFallsBackToTheNewestReadableOne|TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph|TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce|TestAdvanceStripsInjectionsFromStoredPrompts)$'`: ok 222.887s
- `go test -p 1 -race -count=3 -timeout=30m ./internal/checkpoint -run '<same 33-name exact pattern as the -count=20 row>'`: ok 52.074s
- `go test -p 1 -count=1 -timeout=30m ./test/e2e -run '^(TestE2E_CheckpointHookWritesImmutableArtifact|TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_PreCompactToRehydrateToDroppedRoundTrip|TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint|TestE2E_SessionStartCompactRestoresCheckpointItems)$'`: ok 77.978s, all 5 PASS
- `go test -p 1 -count=1 -timeout=30m ./test/guards`: ok 52.726s
- `GOOS=windows|linux|darwin go vet ./internal/checkpoint`: clean on all three
- `golangci-lint run ./internal/checkpoint/... (pinned build from tools/pinned/go.mod, built for the host and run with GOOS windows, linux, darwin)`: rc=0 on all three
- `go run ./tools/devtool fmt-check`: clean (rc=0)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers

### Criterion changes

- current_work.goal skips prompts that are a slash-command invocation: any '/qompack:' invocation (whitespace-trimmed), or a single bare '/name' token. A name starts with a letter or digit, so '/-flag' is kept. Reason: such a prompt states no task, and the captured form is the typed text (live UAT stores).
- current_work.goal is the newest own prompt that can give a goal, walking back at most goalWalkLimit records past unreadable, blank and command prompts. Reason: finding (g).
- A held derived goal is cleared when every own prompt was read and none gives a goal. Reason: finding (h).
- The graph fallback replaces the goal only with a prompt at a later turn than the held goal came from, and that turn survives a daemon restart (draft-file goal_turn). Reason: finding (d) and round-1 review finding 2.
- Round 3: the records walk replaces the goal with one from an earlier turn only when it read every record it passed. Reason: a newly found round-1 regression, where a resumed draft with an unreadable goal record stepped back from 45 to 60.
- Round 3: each prompt record the goal walk reads whole is read once per draft (goalSeen), replacing the round-2 rule of remembering only complete walks (goalFrom). Reason: round-2 review minor, where an oversized goal prompt behind a permanently unreadable record was read whole at every refresh.
- TestAdvanceStripsInjectionsFromStoredPrompts: the goal check is tightened from NotContains to an exact Equal. Reason: an empty goal satisfied NotContains.
- TestSlashCommandPromptIsNotCurrentWork gains '/-flag' and ' /qompack:why dec_...\n' subtests. The blank-prompt case is its own test (TestBlankPromptIsNotCurrentWork), because the intent skips a blank prompt and the table's evolution assertion does not hold for one.

### Open issues

- The internal/daemon/status_order_test.go:21-24 comment nit ('each by the fourth read' should be 'within the first few reads') is outside my files and is left for that file's owner.
- test/docs and the gen-*-docs --check commands were not run because no docs or generated inputs moved. The optional goal_turn draft-file key is not described in any doc; no existing draft key is documented outside .go files either.
- Not tested: on-disk drafts written by pre-fix candidate-8 builds (738d67c7 to 6dfc8d2b) have no goal_turn and are treated like candidate-7 files. Candidate 8 is not frozen, so no such drafts should exist outside test lanes.
- Two equivalent mutants survive in the records-walk gate and are kept for readability. G3 drops `!d.goalTurnSet ||`; this is equivalent because goalTurn is always 0 when unset. G4 changes `>=` to `>`; this is equivalent because one turn holds one prompt record.

### Needs owner

- goalWalkLimit (intent.go) equals the existing maxIntentEvolution (64), so it is not a new number. It caps how many of the newest prompt records one goal walk, and the graph fallback's walk, look at. It also bounds the new in-memory goalSeen map, so goalSeen introduces no new number. Both walks are pinned at exactly the bound and one past it.
- Criterion change (e): current_work.goal skips any /qompack: invocation (whitespace-trimmed) and a bare '/name' prompt. Another command's arguments are kept, and so is a '/-flag' prompt. user_intent is unchanged. docs/architecture.md section 7 could mention the rule; this seat does not own docs.
- Criterion change (g)/(h): the derived goal is the newest prompt that can give one. A held derived goal is cleared when every own prompt was read and none gives one.
- Decision (f): current work stays ordered by captured turn (D35(b)). docs/cannot-do.md's out-of-host-order entry could add 'current work follows captured order too'.
- Decision (round 3): goalFrom (the 'newest record already walked' memo) and the ctx guard were removed. Read-once is held by goalSeen, a per-record memo of what each record read whole gives. Current work is re-walked at every refresh, at a cost of at most goalWalkLimit map lookups and one small map allocation. The records have the last word whenever they answer.
- Criterion change (round 3): a walk that could not read every record it passed (an unreadable record, or the context running out) replaces the goal only with one from the held goal's turn or later. A complete walk replaces it unconditionally, so a goal_turn past every record heals; that happens when the store is restored from a backup older than the draft.
- Decision (round 2, kept): the draft file has an optional goal_turn key (omitempty; older readers ignore it). A file without it, i.e. one written by candidate 7, keeps the ungated graph fallback. That is now pinned at turn 0 by TestResumedCandidateSevenDraftKeepsTheUngatedFallback.

## verify:forkwork: verdict `needs-fixes`, 1 finding(s)

- **nit** `internal/checkpoint/intent.go:276 (`d.setGoalTurnLocked(0, false)` in deriveCurrentWorkLocked's clear case)`: No row covers dropping the goal turn when a complete walk clears the derived goal. If the line is replaced with `_ = 0`, the whole internal/checkpoint package stays green, so this is a surviving mutant that is not equivalent. With the mutant, a cleared draft keeps goalTurnSet=true and persists goal_turn next to an empty goal. The graph fallback then refuses any prompt at or before that stale turn, so current work stays empty during a prompt-list failure. That happens in the backup-restore shape the seat itself pins (TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal), and for a fork whose fallback goal came from a parent node at a higher turn. The seat's mutation list kills `no_clear` (the whole clear case removed) but not this partial mutant.
  - Evidence: I built the mutant M8 with awk, replacing that line with `_ = 0`, and ran `go test -p 1 -count=1 -overlay M8.json ./internal/checkpoint`. Result: `ok ... 96.157s`, the full package passed. I wrote an overlay probe, TestProbeClearedGoalDropsItsTurn. It sets f.src.Store to a promptProbeStore, records promptAs(0, "/qompack:status"), then calls plantDerivedGoalAt(rateReadLimiter, goal_turn 9) and f.begin(). The goal is cleared at that point; HEAD persists goal_turn=nil and M8 persists a non-nil value. The probe then records promptAs(4, rateAsk) and closedSeg(1,0,5), calls setDegraded(true), f.advance(d) and f.advance(d,1), and asserts goal == rateAskGoal. Results: HEAD PASS. M8 FAIL with `expected: "We are building a rate limiter for the Kite API gateway." actual: ""`.
  - Fix: Add a red-first-against-the-mutant row shaped like the probe. A resumed draft whose goal_turn lies past every record gets cleared by a complete walk because only a slash-command prompt exists. The row then asserts that the persisted file has no goal_turn, and that a later prompt the fallback encodes during a prompt-list failure becomes the goal. No product change is needed.


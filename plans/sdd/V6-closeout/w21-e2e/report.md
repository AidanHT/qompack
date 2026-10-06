# Wave 21 e2e (candidate 8 pre-freeze)

Branch `closeout/w21-e2e`. Workflow `wf_792967ff-952`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:e2e: status `done`, head `03f6824aa5c177ec2a432bd2c704fb990281bf49`

### Root cause

X10: deterministic. The test criterion was stale after C4.3's 84b6f802: current_work.goal is now the user's newest prompt, which in this arm is the echoed warning. The bisect shows a357d187 green and 84b6f802, 738d67c7 and f905ec9c red, all on the same assertion. X01 panic: an unguarded rep.Snapshot dereference in the disk subtest after a failed live read. X01 Source none: the internal/cli command client dials within the hooks' 25 ms Windows connect budget, fixed by closeout/w19b-cmdconnect, which is not in this tree. SessionStartLatency 1.53 s: measured in a daytime run that was co-loaded but did not declare it; the row also gated where the other wall-clock rows report under the co-load declaration.

### Summary

Wave 21 e2e report (C3.x, D53(a)). Branch closeout/w21-e2e, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-e2e. Three commits on top of f905ec9c, not pushed. No product code changed. Only test/e2e files changed: test/e2e/v5_x10_test.go, test/e2e/v5_x01_test.go and test/e2e/sessionstart_compact_test.go.

Result: test/e2e passed the e2efunc command 3 of 3 at HEAD 03f6824a, every row 3/3. Run (a) on f905ec9c had one red, the X10 sequitur subtest. It was a deterministic, stale criterion, not timing.

STEP (a): the e2efunc command, run once on f905ec9c with nothing else of mine running.
- Command: env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -skip '^TestV3_HotPath' ./test/e2e. I added -v so rows could be counted; nothing else differed.
- When and where: 06:49:05Z to 07:03:24Z, on AC (Win32_Battery status 2).
- Result: exit 1, 841 s. 111 rows ran: 109 PASS, 1 SKIP, 1 FAIL.
  - The SKIP is TestE2E_RequiredProductChildRaceInstrumentation. It skips with a permitted "platform:" reason ("product-child race mode not requested"). Nightly's race-product-child job is the one that runs it.
  - The one red was TestV5_ThrashWarningVisibleInStatusAndCheckpoint/sequitur_warning_is_warning_only_and_once_per_rule. The other three X10 arms passed.
- Load: wave 19d's daemon.test started at 06:59:57Z and overlapped the last 3.5 minutes. The X10 red is deterministic (see below), so that overlap does not explain it.
- Not red in this run: X01 and TestE2E_SessionStartLatency both passed.

ROOT CAUSES (step b)

1. X10 sequitur subtest: a deterministic test criterion made stale by a product change, not a timing problem.
- The failure was in the checkpoint check, not in delivering the warning. The assertion text: "[qompack] possible loop: FileRead→FileEdit→Bash repeated 4× (turns 2) — consider a different approach" should not contain the prefix, in ".current_work.goal of the sealed checkpoint".
- Why: the arm echoes the delivered warning back three times as the user's prompt, then seals the checkpoint. Since C4.3's 84b6f802 (wave 19 forkwork), current_work.goal is derived from the session's own newest prompt record: its first sentence, capped at 160 runes, with the still-open segment included. So the goal is the user's newest words, which quote the warning.
- Bisect, each on a git-archive copy, quiet, on AC, run with -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$':
  - a357d187 (84b6f802's parent): PASS
  - 84b6f802: FAIL
  - 738d67c7: FAIL
  - f905ec9c: FAIL
  - All three failures are the same assertion. This explains wave 20's 3/3 red on HEAD and on base.
- This is D53(a)'s evolution ruling (a user prompt is kept verbatim even when it quotes a warning), reached by a second field that carries a user's prompt.
- Fix, a criterion change in x10v5RequireWarningOnlyAsEchoedPrompts: current_work.goal may also be EXACTLY an echoed prompt. A goal holding the warning in any other form still fails, and so does any other field holding it.
- The decision you asked for on v5_x10_test.go:472-484 (first-prompt delivery against the 250 ms promptReplyDeadline): it is a declared timing row, and it already sits in the right lane, so I changed nothing there.
  - It already reads obs.NonReferenceDisk and obs.UnderCoload, and internal/obs/nonrefdisk.go and ci.yml's test-e2e comment name it.
  - It is judged where test/e2e runs alone: ci.yml test-e2e, and the pre-freeze e2e and e2efunc steps. c8-night re-runs e2efunc on AC when the power verdict is not VALID.
  - Evidence: across 8 X10 runs (a, focused, the three bisect trees, c1, c2, c3), no undeclared late or deferred reply occurred. The only LATE branch in any run was the arm that forces the reply late through its hold.

2. TestE2E_SessionStartLatency: a timing row whose gating did not match the other wall-clock rows. Not a product defect.
- What it measures: p99 of 30 warm compact session-starts, each a whole hook process and its round trip, against 1.5 s. With 30 samples, nearest-rank p99 is the maximum sample.
- What was inconsistent: it never read the co-load declaration, so it gated even under QOMPACK_UNDER_COLOAD, where X11 and the integration hot-path row report instead. Its only guard was a -short skip that nothing runs and that ADR 0010 rules out.
- Wave 20's 1.53 s came from a daytime run that was co-loaded but did not declare it.
- My measurements (p99, which here is the max of 30):

| Run | Conditions | p99 | Samples |
|---|---|---|---|
| Focused, alone | quiet | 190 ms | 76-190 ms |
| e2efunc c1 | 19d co-load, undeclared | 319 ms | median 107 ms |
| e2efunc c2 | quiet | 281 ms | median 132 ms |
| e2efunc c3 | quiet | 295 ms | median 92 ms |
| Declared co-load | QOMPACK_UNDER_COLOAD=1 | 312 ms | (logged, not gated) |

- Fix, per ADR 0010 decision 2: every run logs p99 and the samples. Under QOMPACK_UNDER_COLOAD it reports, naming the budget it does not apply and the lanes that still apply it (test-e2e, pre-freeze e2e and e2efunc). Run alone it gates as before, now also under -short.
- Unchanged: the budget and the statistic. I added no new number.
- No non-reference-disk handling: the row is not fsync-bound, because the state file's durable write follows the answer (C1.16). The WSL2 container, a slow-fsync stack, also passed it (c5/c6 linux test.jsonl).
- test/guards passes. TestColoadYieldersAreJudgedInIsolation covers test/e2e through the test-e2e job.

3. X01 (TestV5_ObserveToStatusRoundTrip): two separate problems.
- The panic: when the live status read failed, the daemon_down subtest dereferenced rep.Snapshot (nil) at v5_x01_test.go:665. A panic ends the whole test binary, which is how wave 20 lost X14.
  - Fix: the comparison moved into x1v5RequireDiskHoldsLive. On a nil payload it reports one error naming the live read's source and reason, then the disk arm checks the rest of its own source.
  - New row TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead. Red first: with the original unguarded body it panicked ("nil pointer dereference"). With the guard it passes.
  - The live subtest's source assertion now quotes status's reason, so a failure shows whether it was a connect miss or a silent daemon.
- Why live read Source none: a connect miss in the command client. This is product code in internal/cli, which wave 19d owns, so I did not touch it.
  - newCommandClient sets no ConnectDeadline, so `qompack status` dials within the hooks' budget, config.ConnectDeadlineMsWindows = 25 ms.
  - A miss comes back OK:false with no text. fetchDaemonStatus, having seen the daemon listening, gives the "did not answer within 10s" reason.
  - The disk source has no latency.json while the daemon runs (it is written on Stop), so the result is Source none.
  - This is exactly the symptom closeout/w19b-cmdconnect 089c87b2 describes and fixes (250 ms command connect budget, plus one resend on a fast failure). D63 records cmdconnect as verified, and c8-night.sh's preconditions require w19b-cmdconnect merged into integration before the pre-freeze.
- I did not add a retry to the test, so a real Source none still fails the row.
- I could not reproduce the Source none: X01 passed 5 of 5 here (a, c1, c2, c3, and once on HEAD plus the cmdconnect diff applied in a scratch tree).

4. X11 (TestV3_HotPath*): left alone as instructed. It is skipped by e2efunc and judged alone on AC by the night chain.

STEP (c): the e2efunc command three more times at HEAD 03f6824a, sequential, -count=1 each, with -v.
- Exits:
  - run 1: exit 0, 836 s, 07:17:58Z to 07:32:08Z
  - run 2: exit 0, 761 s
  - run 3: exit 0, 742 s, ending 07:57:28Z
- All three on AC.
- Load: run 1 was co-loaded by wave 19d (daemon.test, analyzer, canon, core and other test binaries at the start, daemon.test until about 07:24). Runs 2 and 3 were essentially quiet (only a brief checkpoint.test at 07:32). A sampler logged running test binaries every 60 s.
- Pass counts:
  - 111 of 112 top-level rows passed 3/3.
  - TestE2E_RequiredProductChildRaceInstrumentation skipped 3/3 for its permitted platform reason (by design, not a finding).
  - All 233 subtests passed 3/3.
- So nothing came out below 3/3, and no finding was left to resolve.

CHECKS
- go vet ./test/e2e for windows, linux and darwin: OK.
- golangci-lint (pinned version, via tools/pinned/go.mod) on ./test/e2e/...: exit 0.
- go run ./tools/devtool fmt-check: exit 0.
- go run ./tools/devtool lint --only=docmarkers,runpatterns: PASS both.
- go test -p 1 ./test/guards: ok.
- test/docs: not run. No docs or workflows moved.
- No leftover processes. The real ~/.qompack and ~/.claude were not touched. Scratch copies were removed.

### Commits

- 2753bce5 test(e2e): let X10's goal carry an echoed prompt verbatim
- d942d8a5 test(e2e): report X01's failed live read instead of panicking
- 03f6824a test(e2e): gate SessionStart latency like the other timing rows

### Tests

- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -v -skip '^TestV3_HotPath' ./test/e2e   (step a, f905ec9c)`: exit 1, 841 s: 109 PASS, 1 SKIP (platform), 1 FAIL (TestV5_ThrashWarningVisibleInStatusAndCheckpoint/sequitur_warning_is_warning_only_and_once_per_rule)
- `go test -p 1 -count=1 -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e   (on git-archive copies of a357d187, 84b6f802, 738d67c7)`: a357d187 PASS; 84b6f802 FAIL; 738d67c7 FAIL (same .current_work.goal assertion each time)
- `go test -p 1 -count=1 -run '^TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead$' -v ./test/e2e   (red first, original unguarded body)`: FAIL: should not panic, nil pointer dereference
- `go test -p 1 -count=1 -run '^TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead$' -v ./test/e2e`: PASS
- `go test -p 1 -count=1 -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e`: PASS, all four arms
- `go test -p 1 -count=1 -run '^TestE2E_SessionStartLatency$' -v ./test/e2e`: PASS, p99 190 ms (gated)
- `QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -run '^TestE2E_SessionStartLatency$' -v ./test/e2e`: PASS, p99 312 ms reported, budget not applied (log names test-e2e and pre-freeze e2e/e2efunc)
- `go test -p 1 -count=1 -run '^TestV5_ObserveToStatusRoundTrip$' -v ./test/e2e   (scratch tree: HEAD plus the closeout/w19b-cmdconnect diff)`: PASS, all three subtests
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -v -skip '^TestV3_HotPath' ./test/e2e   x3 sequential (step c, 03f6824a)`: exit 0 x3 (836 s, 761 s, 742 s); 111/112 top-level rows 3/3 PASS, 1 platform skip 3/3; 233/233 subtests 3/3
- `go vet ./test/e2e (GOOS=windows, linux, darwin)`: OK
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./test/e2e/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 1 -count=1 ./test/guards`: ok 31.983s

### Criterion changes

- test/e2e/v5_x10_test.go x10v5RequireWarningOnlyAsEchoedPrompts: .current_work.goal may equal an echoed prompt exactly. Reason: since C4.3/84b6f802 the goal is derived from the session's newest prompt record, the user's own words, which is D53(a)'s evolution ruling applied to a second field. Any other field holding the warning, or a goal holding it in any other form, still fails.
- test/e2e/sessionstart_compact_test.go TestE2E_SessionStartLatency: the -short skip is removed, so the row now also gates under -short. Under QOMPACK_UNDER_COLOAD it reports p99 instead of gating, naming the budget and the lanes that still apply it (ADR 0010 decision 2), and it always logs the measurement. Reason: make it consistent with X11 and the integration hot-path row; wave 20's 1.53 s came from undeclared co-load, against quiet p99 190-319 ms. Budget (1.5 s) and statistic unchanged.

### Open issues

- X01 live Source none on Windows comes from the internal/cli command client dialling within the hooks' 25 ms connect budget. On this tree it can still recur under load until closeout/w19b-cmdconnect (089c87b2..4bdb4acd) is merged into integration. c8-night.sh already requires that merge before the pre-freeze. The test now reports such a failure with status's reason instead of panicking, and adds no retry, so a real Source none still fails.
- X11 (TestV3_HotPath*) was not run, as instructed: e2efunc skips it and the night judges it alone on AC. So test/e2e was run in full only without the two X11 rows.
- TestE2E_SessionStartLatency p99 inside full e2efunc runs is 281-319 ms (190 ms alone) against 1.5 s. It stays gated in every lane that runs test/e2e alone.

### Needs owner

- Coordinator ruling to record: X10's criterion change extends D53(a)'s evolution exception to current_work.goal. Since C4.3's 84b6f802 the goal is the user's newest prompt, and in this arm that prompt is the echoed warning. The alternative is a product change in internal/checkpoint goalOf: skip a prompt that quotes Qompack's own warning, as /qompack: invocations are skipped. I did not make that change, because it conflicts with D45/D46 (a user's words are kept even when they quote a warning).
- Coordinator ruling to record: TestE2E_SessionStartLatency now reports instead of gating under QOMPACK_UNDER_COLOAD, per ADR 0010 decision 2. The budget and statistic are unchanged and no new number was added; no lane today runs test/e2e under that declaration.

## review:e2e:r1: verdict `needs-fixes`, 5 finding(s)

- **minor** `test/e2e/v5_x10_test.go:522 (x10v5DeliverOrRecover, reached from x10v5LateReplyArm :813-840); seat report step (b)1`: X10 is not deterministic when test/e2e runs alone on AC. Its first-prompt and recovery-prompt delivery is a wall-clock judgement: a 250 ms reply deadline counted from the hook's own timestamp (internal/daemon/handlers.go promptReplyBudget). An undeclared late or deferred reply fails the row. The seat calls this a declared timing row but says it is judged in the pre-freeze e2efunc step. In an e2efunc that runs X10 (the brief's -skip '^TestV3_HotPath'), a timing event can therefore refuse the freeze. The seat's evidence (no undeclared late reply in 8 runs) undersamples the rate.
  - Evidence: Red-first run of X10 on a git-archive copy of f905ec9c: quiet, on AC (Win32_Battery status 2), 08:35-08:36Z, -p 1 -count=1. The sequitur arm failed on the expected .current_work.goal assertion. The late-reply arm ALSO failed: "the held reply must be counted late or deferred: ... the reply to \"still red after one more try\" was late (l0_prompt_reply_late 2 -> 3) or deferred to the hook's client spool (true). Only a run that declares a non-reference disk ... or co-load ... may answer that with recovery" (v5_x10_test.go:522 via :820). The arm's code is byte-identical at HEAD. At HEAD the late-reply arm passed 31/31 (e2efunc 1, -count=10, plus -count=20 of that arm alone), so the observed rate is 1 in 32. The sequitur arm passed 11/11 with no undeclared LATE branch. The coordinator's pending prefreeze.sh (qompack-v6 working copy, modified 07:23Z, during the seat's run) already puts TestV5_ThrashWarningVisibleInStatusAndCheckpoint in E2E_TIMING_ROWS, so e2efunc skips it and overnight-c8.sh's win-e2e-timing judges it alone on AC. In that lane the same ~3% undeclared-late rate applies, and no local declaration licenses it.
  - Fix: Correct the seat's classification statement and report to match prefreeze.sh: X10 (and SessionStartLatency) are timing rows that e2efunc skips (E2E_TIMING_ROWS), judged by ci.yml test-e2e and the night's win-e2e-timing, not by e2efunc. Record the observed 1/32 undeclared late recovery-prompt rate so a win-e2e-timing red on X10's delivery branch is classified as timing, not product. Optionally hand the coordinator one note: skipping all of X10 also removes its deterministic checkpoint criterion from the pre-freeze (the stale-goal red fixed here would only have surfaced after the freeze). A subtest-scoped -skip could keep the in-process detector arm in e2efunc.
- **minor** `test/e2e/sessionstart_compact_test.go:657 and :692-694 (also commit 03f6824a message)`: Under the declaration, the row's comment and its runtime log name "ci.yml's test-e2e job and the pre-freeze e2e and e2efunc steps" as the lanes that still apply the 1.5 s budget. Under the coordinator's pending prefreeze.sh, e2efunc SKIPS TestE2E_SessionStartLatency (E2E_TIMING_ROWS), and c8-night's pre-freeze runs e2efunc, not e2e. Both are coordinator scripts under plans/sdd, not in-tree lanes. Meanwhile the in-tree lanes that do apply it go unnamed: devtool test/cover/ci-local/release-check's isolated test/e2e pass and ci.yml's cover job. ADR 0010 decision 2 requires the log to name the job that still applies the limit.
  - Evidence: Observed at HEAD with QOMPACK_UNDER_COLOAD=1: "sessionstart_compact_test.go:692: QOMPACK_UNDER_COLOAD is set: ... test/e2e run alone without it applies it: ci.yml's test-e2e job and the pre-freeze e2e and e2efunc steps". qompack-v6 prefreeze.sh: E2E_TIMING_ROWS="TestE2E_SessionStartLatency TestV5_ThrashWarningVisibleInStatusAndCheckpoint"; e2efunc_body runs -skip "$ef_skip". tools/devtool/test.go wholeTreePasses runs test/e2e alone with obs.UnderColoadEnv cleared. coordinator README item 4: "e2efunc step skips test/e2e's timing rows ... TestE2E_SessionStartLatency".
  - Fix: Name only in-tree lanes in the comment and log, for example "ci.yml's test-e2e job and every devtool pass that runs test/e2e alone (test, cover, ci-local, release-check)", and drop "e2efunc" (and "pre-freeze") from both strings.
- **nit** `test/e2e/v5_x01_test.go:746-757`: When the live payload is missing, x1v5RequireDiskHoldsLive reports it with Errorf and returns. daemon_down then goes on to the cross-source loop at :751-757, which compares each disk budget status with the failed live report's. Under Source none every live row is Unavailable, so the loop FailNows with a second, misleading message ("B-A: the two sources agree on whether it was observed"). The helper's doc says it returns "so the disk arm checks the rest of its own source", but the next check is cross-source. No panic remains, and nothing is hidden.
  - Evidence: Red-first confirmed: with HEAD's x01 file overlaid on f905ec9c and the nil guard (lines 364-369) removed, TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead fails with "should not panic ... nil pointer dereference". With the guard it passes. x1v5RunStatus guarantees len(rep.Budgets)==len(obs.Budgets()), so the loop cannot index-panic, but it does compare against Unavailable live rows.
  - Fix: Have x1v5RequireDiskHoldsLive return whether a live payload existed, and run the rep.Budgets cross-source loop only when it did, so a failed live read yields exactly the one error the new row pins.
- **nit** `test/e2e/sessionstart_compact_test.go:663-667 (criterion-change comment); seat report root cause 2`: The evidence behind SessionStartLatency's root cause is thin in the report, and its quiet-host tail is understated. The report quotes quiet p99 of 190-319 ms and asserts that wave 20 was undeclared co-load without citing samples. Because p99 over 30 samples is the maximum, one spawn outlier decides the row.
  - Evidence: My quiet e2efunc at HEAD (08:06-08:19Z, AC, nothing else running) gave p99 632.7 ms from a single outlier (next sample 253.7 ms, 20 of 30 under 108 ms). -count=10 alone: p99 142-276 ms, 10/10 PASS. Wave 20's own failing samples (scratchpad w20v-e2e2.log): median ~400 ms, every sample 158 ms-1.53 s. The whole distribution is shifted 3-4x, which does support the co-load root cause.
  - Fix: Cite the wave-20 sample list as the evidence for co-load (the whole distribution moved, not one outlier), and record the 633 ms quiet outlier in the criterion comment so the 2.4x quiet margin is on record. No code change.
- **nit** `test/e2e/v5_x10_test.go:619-626, :666`: The current_work.goal exemption extends D53(a), which the seat correctly escalated as needs_owner. The goal is a derived summary that rehydration presents as the task, not a verbatim log like evolution, so the extension does not follow automatically. As written, the exemption also cannot tell a goal derived from the echoed prompt apart from the warning written there by any other route, because the two strings are identical.
  - Evidence: internal/checkpoint/intent.go deriveCurrentWorkLocked/newestGoalLocked takes truncRunes(firstSentence(prompt), 160) from the newest own prompt. The arm's last prompts before r.PreCompactReply are the echoes (v5_x10_test.go:779-791). Bisect confirmed for f905ec9c only (the base fails on this assertion); the a357d187/84b6f802 bisect was not re-run.
  - Fix: Keep the escalation. A stricter alternative needs no policy extension: send one ordinary prompt after the echoes and before PreCompact, so the derived goal is that prompt and current_work.goal stays under the no-warning rule. Otherwise, at least assert positively that goal == ac (the newest prompt). Process note: after all measurements were done, my `devtool lint --only=stubskips` ran a whole-tree go test from 08:48 to 08:59Z. I stopped it, along with its children, all mine, and swept for orphans (none). It overlapped none of the runs reported here.

## fix:e2e:r1: status `done`, head `ab73828ab716fcb79e1ddf8631c38999ac8e5eed`

### Root cause

X10 sequitur subtest: deterministic. The test scenario went stale after C4.3's 84b6f802: current_work.goal became the session's newest prompt, and the arm sealed right after echoing the warning back. Bisect: a357d187 green; 84b6f802, 738d67c7 and f905ec9c red. Fixed by ending the arm on an ordinary prompt, not by an exemption. X10's delivery branch is a declared timing row (250 ms promptReplyDeadline; D56(a) licenses recovery only under a declaration), with about 1 undeclared late recovery prompt in 47 runs on AC. X01 panic: an unguarded rep.Snapshot dereference, then a second misleading cross-source failure; both are fixed. X01 Source none: the internal/cli command client dials within the hooks' 25 ms connect budget, which closeout/w19b-cmdconnect fixes and which is not in this tree. SessionStartLatency 1.53 s: undeclared co-load (the whole distribution moved, median about 380 ms); the row also gated where the other wall-clock rows report under the co-load declaration.

### Summary

Wave 21 e2e report (C3.x, D53(a)), fix round 1. Branch closeout/w21-e2e in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w21-e2e. Six commits on f905ec9c, none pushed. No product code changed. Only three files changed: test/e2e/v5_x10_test.go, test/e2e/v5_x01_test.go and test/e2e/sessionstart_compact_test.go.

RESULT
- test/e2e is green at HEAD ab73828a, and all five review findings are fixed.
- The brief's e2efunc command, run once at HEAD (quiet, on AC): exit 0. 111 top-level rows passed, 1 skipped for its permitted platform reason, and all 233 subtests passed.
- X10 passed 5 of 5 alone, all four arms.
- Round 1's three sequential e2efunc runs at 03f6824a (3/3, every row) still stand for every row this round did not touch.

ROOT CAUSES (round 1, revised where the review changed them)

1. X10 sequitur subtest: a deterministic, stale test scenario, not timing.
- Since C4.3's 84b6f802, current_work.goal is the session's newest prompt (its first sentence, capped at 160 runes). The arm sealed the checkpoint right after echoing the warning back, so the goal was the user's quote of the warning.
- Bisect (round 1): a357d187 passes. 84b6f802, 738d67c7 and f905ec9c all fail on the same assertion.
- Round 1 let the goal equal an echo. The review showed that exemption could not tell the user's quote from a warning Qompack wrote into the goal, because the two strings are byte for byte the same.
- This round withdraws the exemption, so the walk is f905ec9c's strict one again. Red first: with the exemption removed, the arm failed on .current_work.goal (09:11Z).
- The arm now sends one ordinary prompt after the echoes (x10v5GoalPrompt). It requires that prompt to get no warning, and requires the sealed goal to equal it exactly.
- X10 then passed 5 of 5, and again inside the full run.
- No policy extension is needed now, so the needs_owner ruling round 1 asked for is withdrawn.

2. X10's delivery branch is a declared timing row.
- What it judges: first-prompt and recovery-prompt delivery against promptReplyDeadline (250 ms, counted from the hook's own timestamp).
- On the reference host a late reply fails the row by design: D56(a) licenses recovery only under QOMPACK_NONREFERENCE_DISK or QOMPACK_UNDER_COLOAD.
- Correction to round 1's report:
  - Under the coordinator's pending prefreeze.sh, both X10 and TestE2E_SessionStartLatency are in E2E_TIMING_ROWS, so the pre-freeze e2efunc skips them.
  - They are judged by ci.yml test-e2e (hosted, where QOMPACK_NONREFERENCE_DISK licenses X10's recovery branch) and by overnight-c8.sh's win-e2e-timing (local AC, no declaration). The pre-freeze does not judge them.
  - The brief's e2efunc command (-skip '^TestV3_HotPath') does run them, and that is the command I ran.
- Measured rate of an undeclared late recovery prompt in the late-reply arm: 1 in 47 runs, all on AC (the reviewer's 32 plus my 15 across both rounds).
  - The one failure was the reviewer's run on a f905ec9c copy.
  - The full-mode and degraded arms had no undeclared late reply in any run.
- So in win-e2e-timing, an X10 red carrying the message "a reference disk must deliver the warning on the first prompt after the loop, but the reply to ... was late ... or deferred" is a timing red on the reference host. Any other X10 assertion is functional.

3. X01 (TestV5_ObserveToStatusRoundTrip).
- The panic was an unguarded rep.Snapshot dereference; d942d8a5 fixed it.
- The live read's Source none comes from the internal/cli command client dialling within the hooks' 25 ms connect budget. closeout/w19b-cmdconnect fixes that, and it is not in this tree (wave 19d owns the file).
- This round adds 902207b4: after a failed live read, the disk arm now reports exactly one error instead of seven.

4. TestE2E_SessionStartLatency: a timing row.
- It now reports instead of gating under QOMPACK_UNDER_COLOAD (ADR 0010 decision 2). Budget and statistic unchanged.
- Wave 20's 1.53 s came from an undeclared co-loaded run, and its whole distribution moved: samples 158 ms to 1.53 s, median about 380 ms.
- Quiet tail: p99 142 to 319 ms in wave 21's runs. The worst quiet tail seen was one 633 ms sample (the next was 254 ms), in the reviewer's run.

5. X11: not run, as instructed.

REVIEW RESOLUTION

Finding 1 (minor), X10 classification: CORRECT, resolved in this report.
- I verified the reviewer's evidence:
  - qompack-v6 prefreeze.sh line 48 sets E2E_TIMING_ROWS="TestE2E_SessionStartLatency TestV5_ThrashWarningVisibleInStatusAndCheckpoint", and e2efunc_body runs with -skip "$ef_skip".
  - c8-night.sh lines 27-28 say the same.
  - The reviewer's x10-base.log shows the late-reply arm failing at v5_x10_test.go:522 via :820, with late 2 -> 3 and spooled true.
- Section 2 above gives the corrected classification and the 1-in-47 rate.
- No in-tree code or comment named e2efunc for X10, so no code changed for this finding.
- Extra observation from the same logs: in 2 of the reviewer's 11 logged late-reply runs, the session's FIRST prompt was already late (the counter read 1 before the hold). That prompt is not judged by the row. In my 7 runs this round, none was.
- Optional note for the coordinator, verified:
  - Skipping all of X10 also takes its deterministic in-process detector arm out of e2efunc. (The stale goal red, now fixed, would otherwise only have surfaced after the freeze.)
  - A subtest-scoped skip keeps that arm. This skip pattern ran only state_aware_detector_is_progress_aware_and_cannot_feed_itself (PASS), and SessionStartLatency reported "no tests to run" under it: '^(TestV3_HotPath.*|TestE2E_SessionStartLatency)$|^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^(sequitur_warning_is_warning_only_and_once_per_rule|degraded_passive_records_the_loop_and_says_nothing_until_restored|a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh)$'
  - The other three arms each judge a delivery against the 250 ms deadline, so they belong in the timing lane.

Finding 2 (minor), lanes named in SessionStartLatency's log and comment: CORRECT, fixed in ab73828a.
- The pre-freeze steps are coordinator scripts outside the tree, and the pending e2efunc skips this row.
- New constant scColoadJudges names only in-tree lanes that run test/e2e alone without the co-load declaration: ci.yml's test-e2e and cover jobs, and devtool test, cover, ci-local and release-check.
- Verified against the code:
  - tools/devtool/test.go wholeTreePasses clears obs.UnderColoadEnv for test/e2e's own pass.
  - cover.go coverPasses uses wholeTreePasses.
  - cilocal.go runs taskTest and taskCover.
  - releasechecksteps.go reuses ciLocalSteps.
  - ci.yml's cover job runs devtool cover.
- The comment and the log both use the constant.

Finding 3 (nit), X01 second misleading failure: CORRECT, fixed in 902207b4, red first.
- x1v5RequireDiskHoldsLive now returns whether the live arm forwarded a payload.
- The budget loop moved into x1v5RequireBudgetsAgree. It still checks row order and B-D, and compares observed status only after a live read.
- TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead now pins the whole comparison.
- Red first: with the loop unguarded it recorded 7 errors (the reason, plus "the two sources agree" for B-A, B-B, B-C, B-E, B-F and B-G). With the guard it records exactly 1.
- After a live read, both comparisons still fail on a real disagreement.

Finding 4 (nit), thin SessionStartLatency evidence: CORRECT, fixed in ab73828a (comment and commit message).
- I read both sample lists myself.
- Wave 20 (scratchpad w20v-e2e2.log): 30 samples from 158.2 ms to 1.5309 s; the 15th and 16th are 371 and 393 ms.
- The reviewer's quiet run (scratchpad e2efunc.log line 530): p99 632.7 ms, next sample 253.7 ms, 20 of 30 under 108 ms.
- The comment now records both, and that one spawn decides a 30-sample nearest-rank p99. No number changed.

Finding 5 (nit), goal exemption: CORRECT. I took the stricter alternative in 24e78f77 (section 1 above).
- The positive check goal == x10v5GoalPrompt keeps the no-warning rule from passing vacuously.
- The product behaviour round 1 escalated (a goal that quotes a warning when the user's newest prompt does) is no longer exercised by X10. It is listed under open issues, not needs_owner.

CHECKS (this round)
- go vet ./test/e2e for windows, linux and darwin: OK.
- Pinned golangci-lint on ./test/e2e/...: exit 0.
- go run ./tools/devtool fmt-check: exit 0.
- devtool lint --only=docmarkers,runpatterns: PASS both.
- test/guards and test/docs: not re-run this round, because no guard, doc or workflow moved. test/guards passed in round 1.
- Full run of the brief's e2efunc command at HEAD: 09:21:59Z to 09:34:55Z, exit 0, 774 s, on AC at start and end (Win32_Battery status 2).
  - A 60 s sampler saw no other test binary in any of its 12 samples.
  - SessionStartLatency p99 was 243.6 ms.
- Process hygiene: no process of mine is left. My background find, sampler and monitor all ended. The real ~/.qompack and ~/.claude were not touched.

FOR THE COORDINATOR
- 2753bce5's exemption is reverted inside 24e78f77. Squash the pair at integration if a single X10 commit is wanted. I did not rewrite the branch.

### Commits

- 2753bce5 test(e2e): let X10's goal carry an echoed prompt verbatim (round 1; its exemption is withdrawn by 24e78f77)
- d942d8a5 test(e2e): report X01's failed live read instead of panicking (round 1)
- 03f6824a test(e2e): gate SessionStart latency like the other timing rows (round 1)
- 24e78f77 test(e2e): end X10's full-mode arm on an ordinary prompt (review finding 5)
- 902207b4 test(e2e): report X01's failed live read only once (review finding 3)
- ab73828a test(e2e): name SessionStart latency's in-tree judges (review findings 2 and 4)

### Tests

- `go test -p 1 -count=1 -run '^TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead$' -v ./test/e2e   (red first: budget loop extracted, unguarded)`: FAIL: should have 1 item(s), but has 7 (the reason plus 'the two sources agree' for B-A, B-B, B-C, B-E, B-F, B-G)
- `go test -p 1 -count=1 -run '^TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead$' -v ./test/e2e   (with the liveRead guard)`: PASS
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e   (red first: exemption removed, no goal prompt; 09:11-09:13Z, AC)`: FAIL sequitur arm only: warning must not be written into .current_work.goal of the sealed checkpoint; the other three arms PASS
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=5 -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e   (09:14-09:20Z, AC)`: PASS 5/5, every arm 5/5; the only LATE branch was the arm's forced hold (late 0 -> 1 each time)
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -v -skip '^TestV3_HotPath' ./test/e2e   (HEAD ab73828a, 09:21:59-09:34:55Z, AC, sampler saw no other test binaries)`: exit 0, 774 s: 111 top-level PASS, 1 SKIP (TestE2E_RequiredProductChildRaceInstrumentation, platform reason), 0 FAIL; 233/233 subtests PASS; SessionStartLatency p99 243.6 ms
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -skip '<subtest-scoped pattern in the summary>' -v ./test/e2e`: PASS: only state_aware_detector_is_progress_aware_and_cannot_feed_itself ran
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -run '^TestE2E_SessionStartLatency$' -skip '<same pattern>' -v ./test/e2e`: ok [no tests to run]: the combined pattern still skips the row
- `GOOS=windows|linux|darwin go vet ./test/e2e`: OK on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./test/e2e/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -v -skip '^TestV3_HotPath' ./test/e2e   x3 sequential (round 1, HEAD 03f6824a)`: exit 0 x3; 111/112 top-level 3/3 PASS, 1 platform skip 3/3; 233/233 subtests 3/3 (still stands for every row this round did not touch)
- `go test -p 1 -count=1 ./test/guards   (round 1)`: ok 31.983s (no guard, doc or workflow moved this round)

### Criterion changes

- test/e2e/v5_x10_test.go (24e78f77): the current_work.goal exemption added in 2753bce5 is WITHDRAWN, so the no-warning walk is f905ec9c's strict one again. The full-mode arm now sends one ordinary prompt (x10v5GoalPrompt) after the echoes. It requires that prompt to get no warning, and requires the sealed current_work.goal to equal it exactly. Reason: since C4.3/84b6f802 the goal is the newest prompt, and an echoed prompt is byte for byte the warning, so an exemption could not tell the user's quote from a warning Qompack wrote. This adds checks and removes none.
- test/e2e/v5_x01_test.go (902207b4): the disk arm compares each budget row's observed status with the live arm's only when the live read forwarded a payload. Row order and B-D are still checked in every case. Reason: a failed live read is already reported once, naming status's source and reason, and the live subtest fails too. Comparing against its all-unavailable rows repeated that failure six times under a misleading message. A real disagreement after a live read still fails, and this is pinned by TestV5_ObserveToStatusDiskArmReportsAFailedLiveRead.
- test/e2e/sessionstart_compact_test.go TestE2E_SessionStartLatency (03f6824a, plus ab73828a): the -short skip is removed. Under QOMPACK_UNDER_COLOAD it reports p99 instead of gating (ADR 0010 decision 2). Every run logs its samples. The log names the in-tree lanes that still apply the budget (scColoadJudges), no longer the coordinator's pre-freeze steps. Reason: consistency with X11 and the integration hot-path row. Wave 20's 1.53 s was undeclared co-load (158 ms to 1.53 s, median about 380 ms), against a quiet p99 of 142 to 319 ms and a worst quiet sample of 633 ms. Budget and statistic unchanged.

### Open issues

- X10's delivery branch is a timing row judged on the reference host with no declaration (D56(a)). An undeclared late or deferred recovery prompt was seen in 1 of 47 late-reply-arm runs on AC (the reviewer's 32 plus my 15). In overnight-c8.sh's win-e2e-timing, an X10 red with the message 'a reference disk must deliver the warning on the first prompt after the loop, but the reply ... was late ... or deferred' is a timing red. Any other X10 assertion is functional.
- The pending prefreeze.sh skips all of X10, which also takes its deterministic in-process detector arm out of the pre-freeze. A subtest-scoped -skip keeps it; the pattern is verified and quoted in the summary. This is the coordinator's choice; I did not edit coordinator scripts.
- Observation, not judged by any X10 assertion: in 2 of the reviewer's 11 logged late-reply runs, the session's first prompt reply was already late (the late counter read 1 before the hold). None of my 7 runs this round showed it. The hot-path rows judge B-A, not X10.
- Product behaviour X10 no longer exercises: current_work.goal is the session's newest prompt verbatim, so a user prompt that quotes Qompack's warning becomes the goal. D45/D46 keep a user's words, so I made no change. This is flagged for awareness; no ruling is needed for this candidate.
- Carried from round 1: X01's live Source none on Windows comes from the internal/cli command client dialling within the hooks' 25 ms connect budget. It can recur under load until closeout/w19b-cmdconnect is merged, which c8-night.sh requires before the pre-freeze. A real Source none still fails the row, now with exactly one disk-arm error.
- Carried from round 1: X11 (TestV3_HotPath*) was not run. e2efunc skips it, and the night judges it alone on AC.
- Branch history: 24e78f77 withdraws 2753bce5's goal exemption. Squash them at integration if one X10 commit is preferred.

### Needs owner

- Coordinator ruling to record: TestE2E_SessionStartLatency reports instead of gating under QOMPACK_UNDER_COLOAD, per ADR 0010 decision 2. The budget (1.5 s) and the statistic (nearest-rank p99 of 30) are unchanged, and no number was added. The log names the in-tree lanes that still gate it (scColoadJudges). No lane today runs test/e2e under that declaration.
- Withdrawn: round 1's request to extend D53(a) to current_work.goal is no longer needed. X10 now ends on an ordinary prompt and keeps the goal under the strict no-warning rule.

## review:e2e:r2: verdict `sound`, 0 finding(s)


## verify:e2e: verdict `sound`, 0 finding(s)



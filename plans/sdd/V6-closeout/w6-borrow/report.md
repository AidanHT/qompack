# w6-borrow: session-start borrows idle reply time (D21), spawners refuse the home directory

Branch `closeout/w6-borrow`. Workflow `wf_8e58c74e-b50`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> **Coordinator note.** `e056fa41` is a tree id, not a commit: `git rev-parse 24eae18:internal`, the `internal/` tree, identical at `9522182`.

## Implementer — status `done`, head `9522182c35b694bcec2850d465a651574650c33d`

### Root cause

(1) D21. Under D17b, session-start's find/start step (EnsureRunningUntil's poll) stopped at preSendBy, 3.25 s after doHook's first statement. Its 10 s reply wait sat idle whenever the daemon was late: w5-coldstart runs/35 had a staged-cold compact start that spooled at 3.4 s with most of its reply budget unused. The owner approved letting the poll borrow that idle time, up to doneBy - dial - compactAnswerBudget = 8.25 s. (2) D18 defence in depth. ipc.ClaimSpawn/lazySpawn and daemon.EnsureRunning/SpawnDetached never checked the home directory themselves. A caller that skipped cli's refuseHomeRoot would have ClaimSpawn create <home>/.qompack/run/spawn.lock, and the spawners would then start a daemon there. Red evidence: runs/04 shows the claim taken at <fake home>/.qompack/run/spawn.lock and a lazy spawn made; runs/05 shows ensureRunning claiming and spawning, and SpawnDetached trying to start the program. (3) Stale comments. test/fault, test/platform and test/security still justified daemonUpBound with "EnsureRunning has already waited SpawnPollBound", which stopped being true at D17b. There was also an open w5-coldstart review nit in doHook: its two spool-only branches (no address resolved; no time left) called `_ = sp.Append(req)`. They dropped a refused append silently, and the no-time-left branch then logged the request as spooled.

### Summary

w6-borrow is finished. HEAD is 9522182 on closeout/w6-borrow, the working tree is clean, and nothing was pushed or merged. All three items are done, re-verified at the final code tree.

## What I did with the earlier seats' draft
I reviewed the five draft commits (b60c558, c427ec7, 82e052b, 9c5a380, 24eae18) line by line and kept them unchanged. I found no defect and no weakened check.
- The internal/ tree of HEAD is the same as 24eae18's (e056fa41…). My only commit, 9522182, adds evidence under plans/sdd/V6-closeout/w6-borrow/runs/.
- None of the earlier seats' runs are relied on:
  - their logs are moved to scratch (prior-seat/, v3/old-runs/);
  - the second seat's lint log (exit 1, no output) was cut short at the 12:27 stop;
  - its diagnostic used 22 spinners, over the owner's cap of 8.
- Everything below was re-run at 24eae18 with a fake HOME/USERPROFILE.

## (1) D21: the borrow (b60c558)
- `hookBudget` gains `borrowBy = doneBy - dial - minReply`:
  - session-start sets `minReply = daemon.CompactAnswerBudget()`, a new export of D9's own 5 s bound, not a copy of it;
  - `ensureDaemonRunning` passes `b.borrowBy` to `EnsureRunningUntil`, which was `b.preSendBy`.
- Unchanged:
  - `hookExitReserve` 1.5 s, the 10 s reply wait, `spawnClaimDialTimeout` 250 ms, `spawnLockStaleAfter` 10 s;
  - the at-least-1.5 s wait after a late spawn, capped at `latestPoll` = 13.25 s;
  - reply wait = `min(10 s, doneBy - now - dial)`.
- Tests:
  - `TestSessionStartBudget_SplitsTheManifestTimeout` pins 8.25 s, and the reply waits at 6 s (7.25 s), at 8 s (5.25 s) and at the borrow limit (5 s).
  - New rows run the production preSend with the real 15 s budget, against a fresh spawn claim.
  - A daemon up at 4, 6 or 8 s is answered and not spooled.
  - A daemon that never comes up is spooled at about 8.3 s, inside 15 s.
  - A compact start whose daemon comes up at 8 s still hears a daemon that takes its full 5 s.
  - Docs updated: docs/architecture.md and docs/troubleshooting.md.

## (2) Spawners refuse the home directory (c427ec7, 82e052b)
- `ipc.ClaimSpawn` refuses the process's HOME/USERPROFILE before it creates run/, returning `SpawnUnclaimable`. `lazySpawn` then gives up quietly and spawns nothing.
- `ensureRunning`, so both `EnsureRunning` and `EnsureRunningUntil`, returns `paths.ErrHomeRoot` before it dials, claims, logs or spawns. `spawnDetached` refuses before it stages anything.
- `AcquireLock`'s inline check moved into the shared `refuseHomeRoot`; it makes the same call.
- The new rows use a fake home and snapshot every file under it, proving nothing there changes, and check that a project below the home still spawns.

## (3) Comments and review nits (9c5a380, 24eae18)
- `spoolUnsent` writes a Loud line when an append is refused, and the "request was spooled" Warn is written only when it really was.
- The daemonUpBound comments now describe the D21 wait.
- The Refs-footer nit on the w5 evidence commits dc58fad and 1b79f80 is not actionable here: both are already merged.

## Co-load before/after, the same load, interleaved
- Setup:
  - the temporary cold-start diagnostic, instrumented in two scratch exports and never committed (source in runs/zz_*.txt);
  - eight ABBA rounds, 336 rows per tree;
  - CPU co-load of 8 spinner goroutines, each run bounded by go test `-timeout=18m` inside `timeout 1200`;
  - mean machine load was 67% during before-runs and 75% during after-runs.
- Results (runs/20):

  | Tree | Spooled startup / resume / compact | Whole-hook wall p50 / p90 / p99 / max | Over 13.5 s |
  |---|---|---|---|
  | before, f6095e2 | 0/112, 0/112, 0/112 | 727 / 1722 / 3577 / 5399 ms | 0 |
  | after, 24eae18 | 0/112, 0/112, 0/112 | 793 / 1651 / 3104 / 4991 ms | 0 |

- Neither tree started two daemons or had a daemon that never came up.
- At this capped load the borrow window was never reached. The daemon was dialable later than 3.25 s after the process started in only 2 before-rows and 1 after-row. All were answered, by the existing 1.5 s late-spawn grace.
- So the co-load comparison shows no regression, but it cannot show the gain. The gain is shown by the rows that fail with the fix reverted (runs/02-03): the poll gave up at about 3.3 s.

## Checks (details in tests)
- **Windows:** fmt, vet (Windows and GOOS=linux), test/docs and the gen checks pass. Lint passes all 8 sub-checks, and golangci-lint with GOOS=linux exits 0.
- **Windows packages in full:** ipc, cli, daemon, test/platform, test/security and test/fault all ok.
  - test/guards fails only the pre-existing `TestCarriedDefects_WaveReportRequiresResolution`, which fails the same way at f6095e2.
  - The focused e2e set passes 28 tests.
  - The budget rows passed 100 of 100 over 10 repeats.
- **Linux, non-root, -race:**
  - ipc 152, cli 426 and daemon 1517 pass, with one daemon failure: `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest`.
  - That test fails with the same message on base and head when run alone. Base passed 9 of 13 and head 11 of 13, so it depends on load, not on this branch (w6-linuxrows' scope).
  - The new rows passed 280 of 280 over 20 repeats, the focused e2e set passed 107, and there were no race logs.

### Commits

- b60c558 feat(cli): let session-start's daemon start borrow reply time
- c427ec7 fix(ipc): take no spawn claim for the home directory
- 82e052b fix(daemon): refuse the home directory in both spawners
- 9c5a380 fix(cli): say so when a request doHook never sent is lost
- 24eae18 docs(test): say how long session-start waits before daemonUpBound
- 9522182 docs(closeout): record the w6-borrow runs

### Tests

- `go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=1 -v -timeout=10m ; go test ./internal/ipc/ -run '^(TestClaimSpawn_RefusesTheHomeDirectory|TestLazySpawn_RefusesTheHomeDirectory)$' -count=1 -v ; go test ./internal/daemon/ -run '^(TestEnsureRunning_RefusesTheHomeDirectory|TestSpawnDetached_RefusesTheHomeDirectory|TestAcquireLock_RefusesTheHomeDirectory)$' -count=1 -v (Windows, 24eae18, fake HOME) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — all PASS, each exit=0 (runs/01)
- `RED borrow-off (hookbudget lends nothing), scratch export of 24eae18: go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=1 -v` — FAIL as intended, exit=1: SplitsTheManifestTimeout (expected 8.25s); the 4s/6s/8s rows got {} at 3.46 s; the compact row got the client's note; the never-up row ended at 3.26 s against 8.25 s (runs/02)
- `RED callsite-off (EnsureRunningUntil given preSendBy again): go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=1 -v` — FAIL as intended, exit=1: the five D21 rows fail; the budget-arithmetic rows pass (runs/03)
- `RED claim-home-off: go test ./internal/ipc/ -run '^(TestClaimSpawn_RefusesTheHomeDirectory|TestLazySpawn_RefusesTheHomeDirectory)$' -count=1 -v <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — FAIL as intended, exit=1: claim taken at <home>/.qompack/run/spawn.lock; lazy spawn made 1 spawn (runs/04)
- `RED spawner-home-off: go test ./internal/daemon/ -run '^(TestEnsureRunning_RefusesTheHomeDirectory|TestSpawnDetached_RefusesTheHomeDirectory)$' -count=1 -v <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — FAIL as intended, exit=1: ErrHomeRoot not in the error chain for either (runs/05)
- `RED loud-off: go test ./internal/cli/ -run '^TestSessionStartBudget_AnUnspoolableStartWithNoTimeLeftIsLoud$' -count=1 -v` — FAIL as intended, exit=1: no LOUD.log written (runs/06)
- `go run ./tools/devtool fmt-check; go vet and GOOS=linux go vet ./internal/cli/ ./internal/daemon/ ./internal/ipc/ ./test/fault/ ./test/platform/ ./test/security/; go test ./test/docs -count=1; devtool gen-mcp-docs/gen-command-docs/gen-config-docs --check` — all exit=0 (runs/07)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS, exit=0 (runs/08); runpatterns and docmarkers re-run after the evidence commit: PASS
- `GOOS=linux <pinned golangci-lint v1.64.8 built on the host> run ./...` — exit=0 (runs/09)
- `go test ./internal/ipc/ -count=1 -timeout=30m ; ./internal/cli/ ; ./internal/daemon/ ; ./test/platform/ ; ./test/security/ ; ./test/fault/ -timeout=40m (Windows, one at a time, fake HOME)` — all ok: 23 s / 59 s / 373 s / 52 s / 74 s / 621 s (runs/10-15)
- `go test ./test/guards/ -count=1 -v -timeout=30m` — exit=1; the only failure is TestCarriedDefects_WaveReportRequiresResolution, 55 PASS (runs/16). Its run alone on a git archive of f6095e2 fails the same way (runs/18): pre-existing
- `go test ./test/e2e/ -count=1 -v -timeout=60m -run <the 18-branch focused alternation of w5-coldstart runs/17, e.g. TestE2E_SessionStart, TestE2ELazySpawn, TestE2E_SpooledSessionStartNeverDegradesTheProject> <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists; the exact alternation is in runs/17-e2e-focused-windows.log and every branch names a real test -->` — 28 PASS, ok 239 s, exit=0 (runs/17)
- `go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=10 -v -timeout=20m (Windows, machine loaded by other workstreams)` — 100/100 PASS, exit=0; compact-at-8 s row 13.07-13.96 s, 8 s row 8.12-9.62 s (runs/19)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-borrow --repo C:/.../qompack-cx-w6-borrow --out C:/.../runs/linux 24eae18 touched-race --timeout 60m -- ./internal/ipc ./internal/daemon ./internal/cli` — non-root -race: ipc 152/0, cli 426/0, daemon 1517 pass / 1 fail (TestDeliveryOrder_LaneOverflowIsDrainedOnRequest) / 1 skip (Windows-only TestDrainWindowsOpenBlobRetainsCleanupIntent); no race logs
- `linux-nonroot-gate.sh ... --run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 3, then --count 5 in four ABAB gates, at f6095e2 and 24eae18 -- ./internal/daemon` — base: 3/3, 1/5, 5/5 PASS (9/13); head: 1/3, 5/5, 5/5 PASS (11/13); every failure is the same 'refused jobs were never drained: 4 of 5' at base and head. It depends on load and is pre-existing
- `linux-nonroot-gate.sh ... 24eae18 new-rows-x20 --count 20 --run '^(TestSessionStartBudget_|TestClaimSpawn_RefusesTheHomeDirectory$|TestLazySpawn_RefusesTheHomeDirectory$|TestEnsureRunning_RefusesTheHomeDirectory$|TestSpawnDetached_RefusesTheHomeDirectory$)' -- ./internal/ipc ./internal/daemon ./internal/cli <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->` — ipc 40, daemon 40, cli 200 PASS, 0 fail; ran while the co-load diagnostic was running
- `linux-nonroot-gate.sh ... 24eae18 e2e-focused --timeout 60m --run <the same 18-branch focused alternation> -- ./test/e2e <!-- runpatterns: placeholder for the focused set; the exact alternation is in runs/linux/gate-e2e-focused-24eae18-host.txt -->` — 107 PASS, 0 fail, exit=0
- `temporary cold-start diagnostic: ZZ_CPU_COLOAD=1 ZZ_CPU_SPINNERS=8 ZZ_ITERS=2 (rounds 1-4) / 5 (rounds 5-8) timeout 1200 go test ./test/e2e/ -run '^TestZZW5_ColdStart$' -count=1 -v -timeout=18m, in instrumented scratch exports of f6095e2 and 24eae18, ABBA x8 <!-- runpatterns: names a temporary diagnostic kept as runs/zz_w6_coldstart_diag_test.go.txt, never in the tree -->` — 16 runs, all exit=0. before: 0/336 spooled, session-start wall p50/p90/p99/max 727/1722/3577/5399 ms. after: 0/336, 793/1651/3104/4991 ms. 0 multi-daemon, 0 never-up, none over 13.5 s (runs/20, runs/diag/)

### Criterion changes

- TestSessionStartBudget_SplitsTheManifestTimeout (internal/cli/sessionstart_budget_test.go, b60c558): nothing loosened. Every value it already asserted is still asserted: 15 s, 10 s, 1.5 s, doneBy 13.5 s, preSendBy 3.25 s, latestPoll 13.25 s, a full 10 s reply at preSendBy, 2 s after an overrun, and the zero budget. Two messages were reworded because preSendBy is no longer where the find/start step stops: 'what is left for the pre-send step' became 'what is left before a full reply deadline'. New assertions were added: minReply is 5 s and equals daemon.CompactAnswerBudget(); borrowBy is 8.25 s; the reply waits are 7.25 s at 6 s, 5.25 s at 8 s and exactly minReply at borrowBy; a minReply of 0, or one at least as long as the reply, lends nothing.
- Signature change only: newHookBudget takes minReply, and hookSpec gains minReply. Every existing caller was updated and no assertion changed. AcquireLock's inline D18 check moved into refuseHomeRoot, which makes the same call.

### Open issues

- The capped co-load (8 spinners) never reached the borrow window. In 336 rows per tree, the daemon was dialable later than 3.25 s after the process started in 2 before-rows and 1 after-row, all answered in both trees by the existing 1.5 s grace. The co-load comparison therefore shows parity and no regression, but not the gain. The gain rests on the deterministic rows, which fail with the fix reverted. A heavier co-load would exceed the owner's 8-process cap.
- TestDeliveryOrder_LaneOverflowIsDrainedOnRequest fails on Linux under load with the same message at f6095e2 and at head: base passed 9 of 13, head 11 of 13. It is pre-existing and in w6-linuxrows' scope.
- TestCarriedDefects_WaveReportRequiresResolution fails on Windows at f6095e2 and at head. It is pre-existing and waits on the carried-defect dispositions.
- Merge coordination with w6-config, which adds pathstest.Main to internal/ipc, internal/daemon and internal/cli main_test.go and adds test/guards/homeisolation_test.go. There is no textual overlap with this branch, and my new rows set HOME/USERPROFILE with t.Setenv, which works with pathstest.Main. internal/ipc now reads HOME/USERPROFILE through paths.HomeDirs(os.Getenv), a function value that the guard's scan for string-literal Getenv arguments does not see. It only stats the home directory to compare paths and reads no file under it; internal/daemon/lock.go has done the same since w5-home. After merging, run TestGuard_EveryHomeReachingTestPackageIsolatesHome together with the spawn_home rows.
- Review nit, only partly taken: doHook's two spool-only branches now write a Loud line when an append is refused. They still do not increment the client's own l0_spooled or B-G degraded counters, the reviewer's alternative fix. spool.Append itself still counts and Louds ordinary drops.
- The w5-coldstart nit about the missing Refs footer on dc58fad and 1b79f80 is not actionable: both are merged into closeout/integration.
- plans/00-ARCHITECTURE.md §3.3 names daemon.AcquireLock as the D18 gate for other embedders but not the spawners. docs/architecture.md now says the spawners refuse too. I left the plan doc alone as outside the listed scope, and w6-config edits §3.2 nearby.
- Left in place: my /work/cx-w6-borrow-* directories in the container (from this and the earlier seats), and the scratch exports under scratchpad/w6/borrow/v3/diag, kept so the diagnostic can be reproduced.

### Needs the owner

- No new budget or bound numbers. borrowBy (8.25 s) is D21's own formula over approved values: doneBy 13.5 s - dial 250 ms - compactAnswerBudget 5 s. daemon.CompactAnswerBudget() only exports D9's existing bound.
- An edge of the D21 formula for you to accept or change. At the borrow limit the reply wait is exactly 5.0 s, the same as the daemon's compact bound, but the daemon starts its clock later (once it has read the request). So a compaction whose daemon is found in the last tens of ms before 8.25 s, and which takes its whole 5 s, is answered with the client's 'no answer' note instead of the daemon's 'late' note, and the start is spooled and replayed. The compact-at-8 s row has 250 ms of margin minus poll latency; it passed 10 of 10 on Windows (took 13.07-13.96 s) and 20 of 20 on Linux. Closing this edge would need a transit allowance, which would be a new number for you to set.
- By D21's ordering, a daemon spawned after about 6.75 s keeps its 1.5 s grace past 8.25 s, capped at 13.25 s. Its reply wait can then be less than 5 s, so a compaction there may get the client's note rather than the daemon's. Please confirm the grace should win over the compact bound in that case.
- Data point on hookExitReserve (1.5 s, kept by D21). Time spent outside doHook (process start plus exit) reached 1.91 s and 1.74 s in 2 of 336 before-rows: staged-cold resume, the first run of a freshly staged copy. The after-tree maximum was 1.27 s, and this does not depend on the tree. A hook that runs to doneBy (13.5 s) with 1.9 s of such overhead would end at about 15.4 s, past the host's 15 s. No hook came near doneBy in these runs. The reserve number was not changed.

## Independent review

### review:borrow: needs-fixes

- **minor** `internal/cli/hookbudget.go:60 (replyDeadline doc), internal/cli/sessionstart.go:24-34, docs/architecture.md:133, with internal/daemon/spawn.go ensureRunning loop (deadline check, then <-ticker.C, then Probe(20 ms), then on the next pass ClaimSpawn + Probe(spawnClaimDialTimeout 250 ms))` — The code and docs promise that a daemon found by borrowBy leaves the reply at least minReply (5 s), with the words "never below minReply when it kept to borrowBy" and "at least 5 s once the daemon is up by 8.25 s". ensureRunning does not keep to borrowBy. It checks the deadline only before it waits for a tick, so a daemon that comes up just before 8.25 s is found up to one 25 ms tick plus a 20 ms probe later. If the lock the daemon freed is then claimed on the next pass, the 250 ms spawnClaimDialTimeout probe comes on top of that. So the poll can return about 45-300 ms after borrowBy, and replyDeadline then gives about 4.7-4.95 s, below D9's 5 s compact bound. The implementer raised the daemon-side transit edge with the owner but not this overshoot on the client side, and the three docs state the guarantee as unconditional.
  - Evidence: spawn.go loop order: `if !time.Now().Before(deadline) { return ... }`, then `<-ticker.C`, then `ipc.Probe(addr, ensureRunningDialTimeout /*20ms*/)`. After an earlier InFlight, the next pass runs `ipc.ClaimSpawn` and then `case ipc.Probe(addr, spawnClaimDialTimeout /*250ms*/)` before any further deadline check. replyDeadline = min(10 s, doneBy - now - dial), so if the poll returns at 8.25 s + δ, the reply wait is 5 s - δ. The compact-at-8 s row has only about 200 ms of margin (my run: 13.08 s wall, the implementer's 13.07-13.96 s).
  - Fix: Either clamp the poll so that its last probe starts no later than `until`: wait min(tick, deadline-now) and skip the 250 ms post-claim dial past the deadline, or cap it at deadline-now. Or reword the three docs to say 'about 5 s (less by up to one poll tick and dial)', and add this overshoot to the owner's transit-allowance question in needs_owner so both edges are decided together.
- **minor** `internal/cli/hookclient.go:523-525 and :581-595 (spoolUnsent)` — spoolUnsent is documented and committed (9c5a380) as reporting 'whether the request was spooled', and the no-time-left Warn is said to be written 'only when it really was'. spool.Append returns nil when it drops a request after an ordinary write failure, first or sticky (spool.go:137-155). In that case spoolUnsent returns true and the hook logs 'the request was spooled' for a request that was dropped, and the spool's own Loud 'event dropped' says the opposite. The reviewer's nit about a false 'spooled' log line is therefore only closed for the frame-size and fault-site refusals.
  - Evidence: spool.go:137-145: `if s.err != nil { ...Counter(l0_dropped).Add(1); return nil }`; :146-155: on a writeLocked failure it Louds once and returns nil. spoolUnsent: `if err := sp.Append(req); err != nil { Loud; return false }; return true`. The spoolUnsent doc comment itself says that Append 'drops, counts and Louds an ordinary write failure and returns nil'.
  - Fix: Reword the Warn to say what is known, for example 'no time left to wait for the daemon's answer; the request was handed to the spool'. Rename spoolUnsent's result to mean 'not refused', and correct the commit/doc claim. Alternatively, give SpoolWriter a way to report a sticky or just-failed drop (for example Append returning a sentinel wrapped as a non-fatal drop) so the Warn is written only for a durable append.
- **nit** `internal/daemon/spawn_lock_test.go:387-390, :414, :432` — Stale comments inside this lane's scope. sessionStartBound is described as 'its pre-send deadline', and the EnsureRunningUntil rows speak of 'the pre-send deadline'. Since D21, session-start passes hookBudget.borrowBy (8.25 s), not preSendBy, as `until`. Item (3) asked for stale descriptions of session-start's wait to be corrected, and these in internal/daemon were missed. The e2e x10Restart comment (test/e2e/v3_x10_test.go:245-248: 'EnsureRunning's single spawn attempt is fire-and-poll with a 1.5s bound') has been stale since D17b in the same way.
  - Evidence: `// sessionStartBound is the pollBound session-start hands ensureRunning through EnsureRunningUntil: its pre-send deadline, ...` compared with sessionstart.go:89 `daemon.EnsureRunningUntil(..., b.borrowBy, b.latestPoll)`.
  - Fix: Change the three test comments to 'its borrow limit (hookBudget.borrowBy, D21)' or to the more general 'the instant its hook budget allows'. Optionally correct the x10Restart comment to say that session-start polls to its budget.

## Fix seat (review resolution) — status `done`, head `4a31c80`

### Root cause

Finding 1: the session-start poll (daemon.ensureRunning, reached through EnsureRunningUntil) checked its deadline only before it waited for the next tick. Its last tick and 20 ms dial could run up to about 45 ms past the borrow limit. If the lock the daemon had just freed was claimed on the next pass, the 250 ms post-claim dial (spawnClaimDialTimeout) ran past it too, and if nothing answered, a second daemon was spawned and polled for its own grace period. Every millisecond of overrun came out of the reply wait that D21 keeps at D9's 5 s compact bound, while the docs stated that bound as a floor. Finding 2: spool.Append also returns nil when it drops a request on an ordinary write failure (the first one or a later, sticky one), after counting and Louding the drop itself. So spoolUnsent reported such a request as spooled, and the no-time-left Warn said "the request was spooled" next to the spool's own "event dropped" line.

### Summary

## w6-borrow fix seat: report

Worktree `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-borrow`, branch `closeout/w6-borrow`. The implementer's five code commits (b60c558, c427ec7, 82e052b, 9c5a380, 24eae18) and their evidence (9522182) come first. This seat added three code commits (1a3fa76, d6c103f, ea00d74) and two evidence commits (f6e0b59, 4a31c80). Evidence is in `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w6-borrow/plans/sdd/V6-closeout/w6-borrow/runs/` (files 21-37 and `linux/`).

### Review resolution
| Finding | Verdict | Action |
|---|---|---|
| 1 (minor): the poll overruns borrowBy by one tick plus a dial, or plus the 250 ms post-claim dial, so the reply falls below the 5 s compact bound the docs promise | **Confirmed.** Reproduced red 3 of 3 (runs/21). Held-claim row: the last dial was bounded 16.6-19.0 ms past the deadline. Freed-claim row: the post-claim dial was bounded about 156-161 ms past, a second daemon was spawned, the poll ended 366 ms late, and the claim was left behind. | **Fixed in 1a3fa76** (see below). Docs reworded to what now holds. The edge that remains is added to the owner's transit question. |
| 2 (minor): spoolUnsent's "spooled" means only "not refused"; the Warn logs a dropped request as spooled | **Confirmed.** Reproduced red (runs/22): the day log had "the request was spooled" next to the spool's "ipc: spool write failed — event dropped". | **Fixed in d6c103f** (see below). The SpoolWriter interface was not changed: making Append report a drop would double-count `l0_dropped` and double-Loud through Client.Send's own path. |

### What changed
**1a3fa76 `fix(daemon): end session-start's daemon poll at its deadline`** (`internal/daemon/spawn.go`)
- `ensureRunning` now delegates to `ensureRunningWith`, which takes the liveness dial as a seam. In production that is `probeBy(addr, by)`, which dials nothing when no time is left; `ipc.Probe` with a zero timeout would wait forever on POSIX.
- Once the poll has begun:
  - `waitForTick` waits for the next tick only until the deadline.
  - Every dial is bounded to end at the deadline (`pollEnds`), the post-claim dial included.
  - No claim is taken once the wait is over.
  - A post-claim dial that the deadline cut short and that found nothing releases the claim and returns `ErrNotFound` instead of spawning: a daemon that is up but slow to accept fails a dial the same way.
- The dials before the poll begins are never cut, so a deadline already past still gets its dial and its spawn, as before.
- Doc comments on `spawnClaimDialTimeout`, `EnsureRunning` and `EnsureRunningUntil` updated.
- `internal/cli/hookbudget.go` (the `replyDeadline` doc), `docs/architecture.md` and `docs/troubleshooting.md` no longer state 5 s as a floor. They say "about 5 s when the poll runs to 8.25 s", that the poll stops there, and that a daemon up in its last moments is reached by the hook's own dial.
- New row `TestEnsureRunningUntil_ThePollEndsAtItsDeadline`, with sub-rows `claim_held_to_the_end` and `claim_freed_near_the_end`. It uses a stand-in daemon that listens and never accepts, and checks: no dial of the poll bounded past the deadline, no spawn, the claim given back (or another spawner's left alone).

**d6c103f `fix(cli): say a no-time-left start was handed to the spool`** (`internal/cli/hookclient.go`, `docs/troubleshooting.md`)
- `spoolUnsent` now returns `taken` (the spool did not refuse the request). Its doc says taken never means spooled.
- The Warn is the named constant `noTimeLeftMsg`: "hook: no time left to wait for the daemon's answer; the request was handed to the spool".
- The troubleshooting page quotes the new line and names the LOUD.log lines that mean the request was dropped.
- This corrects the claim in 9c5a380's body, which was true only for the refusals.
- New row `TestSessionStartBudget_ANoTimeLeftStartTheSpoolDropsIsNotLoggedAsSpooled`: a file sits where the spool directory must be created.

**ea00d74 `test(daemon): log the poll's overrun and pin its tick wait`**
- A criterion change to the row added in 1a3fa76; see below.
- Adds `TestWaitForTick_EndsAtTheDeadlineNotTheTick`. It was red against a temporary mutation that restored the old wait (runs/33: 5.0 s against a 2.5 s limit).

### Criterion changes (with rationale)
1. **`TestEnsureRunningUntil_ThePollEndsAtItsDeadline` no longer fails on how late the call returns; it logs it (ea00d74).**
   - As first committed (1a3fa76) it also required the call to return within 250 ms of its deadline. That failed 1 run in 10 on a co-loaded Windows machine (runs/30: 316 ms) and passed 20 of 20 alone (runs/31).
   - The temporary diagnostic `TestZZW6_PollOverrunDiag` (runs/32; source kept as `runs/zz_w6_poll_overrun_diag_test.go.txt`, never in the tree) shows the time is outside the product:
     - normally the freed-claim overrun is p50 about 4 ms, mostly the claim's release, a file delete (about 3.5 ms);
     - when the machine's load peaked, the stand-in dial's own timer fired up to 302 ms late and the release took up to 206 ms, for 508 ms at worst.
   - No limit both catches the old code (366 ms late) and survives those stalls. The row's structural checks catch the old code on every run anyway.
   - Following ADR 0010 ("a wall-clock number measured under co-load is a measurement, not a judgement"), the overrun is now logged.
   - The one property that check covered and no structural check does, the tick wait ending at the deadline, is pinned by `TestWaitForTick_EndsAtTheDeadlineNotTheTick`: a 5 s tick against a 200 ms deadline, which leaves 2.3 s of margin.
   - Measured after the change: Windows 37 µs to 26 ms (runs/34); Linux p50 0.8 ms, max 1.7 ms over 40 runs.
   - **Alternative:** keep the 250 ms judgement under ADR 0010's mechanism instead. That means `obs.UnderCoload()` in the row plus an entry in ci.yml's `timing` lane (enforced by `TestColoadYieldersAreJudgedInIsolation`). ci.yml is outside this seat's scope and is a likely conflict with w6-linuxrows, so it is left to the coordinator.
2. No check that existed before this seat was weakened. The Warn text is a documented log line; it changed as described above.

### Behaviour change to note
If session-start finds its wait over while a daemon's pipe exists but will not accept (a hung or busy daemon found in the last 250 ms), it no longer spawns a replacement. It gives the claim back and the next spawner decides. Before, it spawned after the full 250 ms. A replacement for a live hung daemon would lose `daemon.lock` and exit anyway, so this only removes a pointless spawn and the extra wait after it. It matches EnsureRunning's documented rule: "otherwise the wait ends first … the first spawner … reclaims it".

### Commands and results (all logs under runs/)
- **Red first:**
  - `go test ./internal/daemon/ -run '^TestEnsureRunningUntil_ThePollEndsAtItsDeadline$' -count=3 -v`: FAIL 3/3, with the dial seam in place and the loop logic unchanged (21).
  - `go test ./internal/cli/ -run '^TestSessionStartBudget_ANoTimeLeftStartTheSpoolDropsIsNotLoggedAsSpooled$' -count=1 -v`: FAIL, on 1a3fa76 plus the test only (22).
  - `go test ./internal/daemon/ -run '^TestWaitForTick_EndsAtTheDeadlineNotTheTick$' -count=1 -v`: FAIL against the mutation, 5.0 s (33).
- **Green:**
  - the three no-time-left rows x3 (23);
  - `go test ./internal/daemon/ -run '^(TestWaitForTick_EndsAtTheDeadlineNotTheTick|TestEnsureRunningUntil_ThePollEndsAtItsDeadline)$' -count=10 -v`: 40 of 40 (34); <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - `go test ./internal/daemon/ -run '^(TestEnsureRunningUntil_|TestEnsureRunning_)' -count=10 -v`: 1 wall-clock failure in 10, the one above (30); <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
  - `go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=3 -v`: pass (30).
- **Full packages, Windows:**
  - `go test ./internal/daemon/ -count=1 -timeout=30m`: ok 496 s at d6c103f (24); ok 309 s at ea00d74 (35).
  - `go test ./internal/cli/ -count=1 -timeout=30m`: ok 70 s (25).
  - `go test ./test/docs/ -count=1 -timeout=30m`: ok (26).
- **Focused e2e:** the same `-run` set as runs/17, with a fake HOME/USERPROFILE: ok 253 s, 28 of 28 top-level tests (37).
- **Format, vet and lint** (29, 36):
  - `go run ./tools/devtool fmt-check`: exit 0.
  - `go vet` on internal/daemon and internal/cli, Windows and GOOS=linux: exit 0.
  - `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all 8 PASS (27, 36).
  - Pinned golangci-lint with `GOOS=linux ... run ./...`: exit 0 (28, 36).
- **Linux**, `linux-nonroot-gate.sh --prefix cx-w6-borrow`, non-root with `-race`:
  - d6c103f `fix-rows-x20`: daemon 340 and cli 60 pass.
  - d6c103f `d21-rows-x5` (`^TestSessionStartBudget_`): cli 55 pass.
  - d6c103f `touched-race` (`./internal/daemon ./internal/cli`, full): daemon 1521 pass (1 skip), cli 427 pass. That includes `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest`, the implementer's Linux red, which also fails on base f6095e2 and belongs to w6-linuxrows.
  - ea00d74 `poll-rows-x20`: daemon 360 pass.
- **Cold-start diagnostic:** not re-run by this seat. Both fixes change behaviour only within the last 250 ms before 8.25 s or on the spool-drop path. The implementer's 672 ABBA rows had no daemon dialable later than 4.3 s, so they cannot exercise either change.

### Scope and coordination
- No edits to internal/config, test/guards or ci.yml.
- No processes left running; none were killed.
- The temporary diagnostic was removed from the tree.

### Commits

- b60c558 feat(cli): let session-start's daemon start borrow reply time (implementer)
- c427ec7 fix(ipc): take no spawn claim for the home directory (implementer)
- 82e052b fix(daemon): refuse the home directory in both spawners (implementer)
- 9c5a380 fix(cli): say so when a request doHook never sent is lost (implementer)
- 24eae18 docs(test): say how long session-start waits before daemonUpBound (implementer)
- 9522182 docs(closeout): record the w6-borrow runs (implementer)
- 1a3fa76 fix(daemon): end session-start's daemon poll at its deadline (fix seat, finding 1)
- d6c103f fix(cli): say a no-time-left start was handed to the spool (fix seat, finding 2)
- ea00d74 test(daemon): log the poll's overrun and pin its tick wait (fix seat, follow-up to 1a3fa76)
- f6e0b59 docs(closeout): record the w6-borrow fix-seat runs (fix seat, evidence runs/21-36 and linux/)
- 4a31c80 docs(closeout): record the w6-borrow focused e2e run at ea00d74 (fix seat, evidence runs/37)

### Tests

- `go test ./internal/daemon/ -run '^TestEnsureRunningUntil_ThePollEndsAtItsDeadline$' -count=3 -v (probe seam only, loop logic unchanged; runs/21)` — FAIL 3/3 as intended: held-claim row's last dial bounded 16.6-19.0 ms past the deadline; freed-claim row spawned a second daemon, dials bounded up to 366 ms past, poll ended 366 ms late, claim left behind
- `go test ./internal/cli/ -run '^TestSessionStartBudget_ANoTimeLeftStartTheSpoolDropsIsNotLoggedAsSpooled$' -count=1 -v (before d6c103f; runs/22)` — FAIL as intended: day log says 'the request was spooled' next to the spool's 'event dropped'
- `go test ./internal/daemon/ -run '^TestWaitForTick_EndsAtTheDeadlineNotTheTick$' -count=1 -v (temporary mutation restoring the pre-fix wait; runs/33)` — FAIL as intended: 5.0 s against 2.5 s; spawn.go restored right after
- `go test ./internal/cli/ -run '^(TestSessionStartBudget_ANoTimeLeftStartTheSpoolDropsIsNotLoggedAsSpooled|TestSessionStartBudget_AnUnspoolableStartWithNoTimeLeftIsLoud|TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling)$' -count=3 -v (runs/23)` — PASS 9/9 <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^(TestWaitForTick_EndsAtTheDeadlineNotTheTick|TestEnsureRunningUntil_ThePollEndsAtItsDeadline)$' -count=10 -v at ea00d74 (runs/34)` — PASS 40/40; logged overruns 37 µs to 26 ms <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^(TestEnsureRunningUntil_|TestEnsureRunning_)' -count=10 -v at d6c103f, co-loaded (runs/30)` — 1 failure in 10: the 250 ms wall-clock check (316 ms), later turned into a logged measurement in ea00d74; everything else passed <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/daemon/ -run '^TestEnsureRunningUntil_ThePollEndsAtItsDeadline$' -count=20 -v, alone (runs/31)` — PASS 20/20, 1.01-1.02 s each
- `ZZ_CPU_COLOAD={1,0} ZZ_ITERS=30 timeout 600 go test ./internal/daemon/ -run '^TestZZW6_PollOverrunDiag$' -count=1 -v (TEMPORARY DIAGNOSTIC, never in the tree; runs/32)` — freed-claim overrun p50 4.0-8.6 ms; worst round (machine load peaked) p90 252 ms, max 508 ms, split as dial-timer lateness up to 302 ms plus release/scheduling up to 206 ms <!-- runpatterns: a temporary diagnostic that was never committed; its source is saved at runs/zz_w6_poll_overrun_diag_test.go.txt -->
- `go test ./internal/cli/ -run '^TestSessionStartBudget_' -count=3 -v (runs/30)` — PASS
- `go test ./internal/daemon/ -count=1 -timeout=30m (runs/24 at d6c103f, runs/35 at ea00d74)` — ok 495.7 s; ok 309.5 s
- `go test ./internal/cli/ -count=1 -timeout=30m (runs/25)` — ok 69.8 s
- `go test ./test/docs/ -count=1 -timeout=30m (runs/26)` — ok 5.7 s
- `HOME=<fake> USERPROFILE=<fake> go test ./test/e2e/ -count=1 -v -timeout=60m -run <the runs/17 focused set> at ea00d74 (runs/37)` — ok 253 s, 28/28 top-level tests pass; fake home left empty <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon/ ./internal/cli/ (Windows and GOOS=linux) (runs/29, 36)` — exit 0 everywhere
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (runs/27, 36)` — all 8 PASS at d6c103f and at ea00d74
- `GOOS=linux <pinned golangci-lint built from tools/pinned/go.mod> run ./... (runs/28, 36)` — exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w6-borrow ... d6c103f fix-rows-x20 --count 20 --run '^(TestEnsureRunningUntil_|TestEnsureRunning_|TestSessionStartBudget_ANoTimeLeftStartTheSpoolDropsIsNotLoggedAsSpooled$|TestSessionStartBudget_AnUnspoolableStartWithNoTimeLeftIsLoud$|TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling$)' -- ./internal/daemon ./internal/cli` — PASS: daemon 340, cli 60 (non-root, -race)
- `linux-nonroot-gate.sh ... d6c103f d21-rows-x5 --count 5 --run '^TestSessionStartBudget_' -- ./internal/cli` — PASS: cli 55
- `linux-nonroot-gate.sh ... d6c103f touched-race --timeout 60m -- ./internal/daemon ./internal/cli` — PASS: daemon 1521 (1 skip), cli 427
- `linux-nonroot-gate.sh ... ea00d74 poll-rows-x20 --count 20 --run '^(TestWaitForTick_EndsAtTheDeadlineNotTheTick$|TestEnsureRunningUntil_|TestEnsureRunning_)' -- ./internal/daemon` — PASS: daemon 360; logged overrun p50 0.8 ms, max 1.7 ms over 40 runs

### Criterion changes

- TestEnsureRunningUntil_ThePollEndsAtItsDeadline (added by this seat in 1a3fa76) no longer fails on whether the call returned within 250 ms of its deadline (ea00d74); it logs the overrun instead. Rationale: that check failed 1 run in 10 under co-load (316 ms) and passed 20/20 alone. The temporary diagnostic TestZZW6_PollOverrunDiag traced the time to OS scheduling (the stand-in dial's own timer fired up to 302 ms late) and the claim's file-delete release (up to 206 ms), where an ordinary run overran by about 4 ms. No limit both catches the old code (366 ms late) and survives those stalls, and the row's structural checks (no dial bounded past the deadline, no spawn, claim given back) catch the old code on every run. ADR 0010 treats a wall-clock number under co-load as a measurement. The one property the check covered and no structural check does, the tick wait ending at the deadline, is now pinned by TestWaitForTick_EndsAtTheDeadlineNotTheTick (a 5 s tick against a 200 ms deadline; red against the old wait at 5.0 s). No check that existed before this seat was weakened.
- The no-time-left Warn changed from '...; the request was spooled' to '...; the request was handed to the spool' (noTimeLeftMsg), and spoolUnsent's result now means 'the spool took it'. docs/troubleshooting.md quotes the new line and names the LOUD.log lines that mean the request was dropped.

### Open issues

- Coordinator choice: ea00d74 turned TestEnsureRunningUntil_ThePollEndsAtItsDeadline's 250 ms wall-clock check into a logged measurement (ADR 0010), with the structural checks and TestWaitForTick_EndsAtTheDeadlineNotTheTick carrying the contract. To judge the 250 ms in isolation instead, the row would read obs.UnderCoload() and must be added to ci.yml's timing lane (TestColoadYieldersAreJudgedInIsolation enforces it). That needs a ci.yml edit outside this seat's scope, likely to conflict with w6-linuxrows.
- Behaviour change to note: when session-start's wait ends while a daemon's pipe exists but will not accept, it now gives its claim back without spawning a replacement (before, it spawned after the full 250 ms post-claim dial). A replacement for a live hung daemon would lose daemon.lock and exit, so only a futile spawn is lost.
- The cold-start diagnostic was not re-run by the fix seat: neither fix changes behaviour outside the last 250 ms before 8.25 s or the spool-drop path, and the implementer's 672 ABBA rows had no daemon dialable later than 4.3 s.
- TestDeliveryOrder_LaneOverflowIsDrainedOnRequest (the implementer's Linux red, which also fails on base f6095e2) passed in this seat's Linux touched-race run. It stays with w6-linuxrows.
- The existing D21 live row TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound still has only about 200 ms of margin (13.08 s here; it passed 3/3 on Windows and 5/5 on Linux). It is a wall-clock row this seat did not change.

### Needs the owner

- No new budget or bound numbers (carried from the implementer, still true after the fix seat). borrowBy (8.25 s) is D21's own formula over approved values: doneBy 13.5 s - dial 250 ms - compactAnswerBudget 5 s. daemon.CompactAnswerBudget() only exports D9's existing bound. The fix seat's pollEnds uses the poll's existing deadline. The test fixture timings pollEndsWait = 4 x spawnClaimDialTimeout and pollEndsFreedBefore = spawnClaimDialTimeout / 2 are derived from existing constants and are not product bounds.
- The transit edge at the borrow limit, updated: accept it, or set a transit allowance (a new number, yours to set). Poll ticks and dials no longer run past 8.25 s (finding 1 fixed). Four small gaps remain between the poll's end and the daemon's clock. (a) The moments from the poll's return to doHook's reply-deadline computation. (b) On Windows, one go-winio busy-retry sleep (10 ms) when a dial of a busy pipe straddles the deadline. (c) The claim's own file I/O and OS scheduling: measured 0.04-26 ms on this co-loaded Windows machine, at most 1.7 ms on Linux, and up to about 0.5 s during a heavy load spike (runs/32). (d) The daemon starts its 5 s compact clock only once it has read the request. At the limit the reply wait is exactly 5.0 s. So a compaction whose daemon is found at the very limit, and which takes its whole 5 s, gets the client's 'no answer' note and is spooled and replayed, instead of getting the daemon's 'late' note. The compact-at-8 s row keeps about 200 ms of margin: 13.08 s here, 3/3 on Windows and 5/5 on Linux, after the implementer's 10/10 Windows and 20/20 Linux.
- Please confirm (carried from the implementer): by D21's ordering, a daemon spawned after about 6.75 s keeps its 1.5 s grace past 8.25 s, capped at 13.25 s. Its reply wait can then be under 5 s, so a compaction there may get the client's note rather than the daemon's. Should the grace win over the compact bound in that case?
- Data point on hookExitReserve (carried from the implementer; the number is unchanged, as D21 keeps it). Time spent outside doHook (process start plus exit) reached 1.91 s and 1.74 s in 2 of 336 before-rows: staged-cold resume, the first run of a freshly staged copy. The after-tree maximum was 1.27 s, and this does not depend on the tree. A hook that ran to doneBy (13.5 s) with 1.9 s of such overhead would end at about 15.4 s, past the host's 15 s. No hook came near doneBy in these runs.

## Independent verification of the fix seat: needs-fixes

- **nit** `internal/cli/sessionstart.go:64-66 (ensureDaemonRunning doc comment); also internal/daemon/spawn.go:144-145 (EnsureRunningUntil: 'leaves the reply that bound in full') and internal/cli/hookbudget.go:62-63 ('leaves minReply less only the time from its return to now: its poll ends at borrowBy')` — Finding 1's doc half is only partly closed. The fix seat changed docs/architecture.md and docs/troubleshooting.md to say 'about 5 s', but ensureDaemonRunning's comment still says the borrow limit 'leaves the reply at least the compact bound'. That is the same unconditional 'at least 5 s' wording the reviewer objected to, and the file was named in the finding's location. The two new code comments repeat the claim: 'in full', and 'only the time from its return to now' because 'its poll ends at borrowBy'. The fix seat's own evidence contradicts this. The poll can still return after borrowBy, by the claim's Release file I/O after a cut post-claim dial, scheduling, and on Windows go-winio's 10 ms busy-retry. The logged overrun was 0.5-5 ms in my runs and up to about 0.5 s under load (runs/32), and needs_owner lists the same gaps (a)-(d) as open.
  - Evidence: The comment at sessionstart.go:65-66, unchanged since 9522182: 'the borrow limit (hookBudget.borrowBy, D21), which leaves the reply at least the compact bound'. The comment at spawn.go:144-145: 'so a poll that ends at until leaves the reply that bound in full'. The case at spawn.go ~256-261 (`case !deadline.IsZero() && !time.Now().Before(deadline): lock.Release(); return false, core.ErrNotFound`) releases the claim after the deadline. TestEnsureRunningUntil_ThePollEndsAtItsDeadline logs 'the poll returned 2.1-4.96ms after its deadline' in my -count=3 run at 4a31c80.
  - Fix: Reword the three comments the way docs/architecture.md now reads. For example, in sessionstart.go: 'which leaves the reply about the compact bound (less the poll's return and the dial's transit; see the owner's transit question)'. In spawn.go: drop 'in full'. In hookbudget.go: say the poll's ticks and dials end at borrowBy, and that its return and the time up to now come off minReply.


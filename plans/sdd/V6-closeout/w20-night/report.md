# Wave 20 night: candidate 8's chain and re-check (C3.1)

Branch `verify/v6`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **blocker** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:36-51 (timed), with recrun.sh:10 and quiet.sh:404-408`: The power-rule retry is a no-op for every AC-gated phase3 and quiet step, and its log line is misleading. Each step writes its record through recrun.sh, which refuses to overwrite an existing <id>.json/.log. So when try 1 is INVALID-POWER, try 2 exits within seconds without running anything (recrun returns 2, phase3 exits 1, quiet.sh exits 1). timed then logs `step win-timing try 2 exit=1 VALID power=AC nn`. That reads as a valid failure on AC of a run that never happened, and the only real record left on disk is the invalid try 1. This affects win-timing, win-e2e-timing, win-x11-alone and quiet c51-win (c51-win and c51-win-bf are both recrun ids). It works only for release-check, which bypasses recrun.
- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:25-34,39-47 and header lines 5-7`: When acwait times out (no AC within 180 min) it returns 1, but timed ignores that and runs the step on battery. If the power source does not change during the step, timed logs `step X try 1 exit=N VALID power=BAT nn`. A battery run is therefore labelled VALID, which contradicts the header ('starts only on AC') and D57(d) ('a battery run is invalid ... neither a pass nor a fail'). Separately, t0 is taken after acwait's last power() check. An AC cut in that gap logs its event 105 before t0, so it is not counted, and the step is again called VALID with only the trailing power=BAT as a clue. Each of the five timed steps (and each retry) can wait its own 180 min, with no overall deadline.
- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:77 (release-check under timed)`: The whole release-check (about 2.5-3 h of fmt, lint with stubskips, vet, build, whole-tree test, cover, build-all, guards, govulncheck, licenses, determinism, rollback, plugin-validate, marketplace) sits in one INVALID-POWER window and is retried automatically. Only two of its parts carry AC-sensitive rows: the isolated test/e2e passes of `ci-local test` and `ci-local cover` (X11 B-A/B-B and the D42 pair, plus X10's first-prompt delivery), about 22 min each. The shared passes declare co-load. Given this laptop's AC cycling, a transition somewhere in 3 h is likely. That voids the run, then adds up to 180 min of acwait and a second 3-hour run, which can itself be voided (return 99). The night then reaches about 11 h and runs into the owner's day.
- **major** `plans/sdd/V6-closeout/coordinator/c8-night.sh:30-33 with prefreeze.sh:17-20`: prefreeze.sh appends to summary.log (`>>`) and overwrites each <step>.log. c8-night accepts a step when `grep -q "^step $s exit=0 "` matches anywhere in that file, so a line left by an earlier run satisfies the gate. Two plausible cases: the coordinator dry-runs `prefreeze.sh gate` into phase3/c8/prefreeze after merging wave 19b, or c8-night is relaunched after a refusal. In either case a step that fails tonight still passes, and the tree is frozen and pushed while the logs on disk show the failure.
- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:72-80`: The local tag v0.3.0 is created in the ref store shared by every worktree and stays there for the whole release-check (3 h or more, up to about 9 h with retries and waits), and nothing guarantees it is deleted. There is no trap, so a kill, a reboot, or the set -u crash in acwait (finding below, reachable inside timed after the tag exists) leaves it in place. While it exists, a `git push --tags` (or `git push origin v0.3.0`) from any of the 195 worktrees publishes it. That triggers release.yml, whose provenance attestation cannot be withdrawn (D57(c)). A leftover tag also makes the next night refuse release-check. The tag-creation result is not checked either: if it fails, release-check runs and fails at version agreement.
- **major** `plans/sdd/V6-closeout/coordinator/phase3.sh:65-69,79 (bundles step, run tonight by overnight-c8.sh:61)`: The bundles step's exit status is that of the last command in its case arm, the `for t ... done > p3-claude-plugin-validate.txt` loop, and that loop ends with `echo exit=$?`. A failed `rec p3-bundleA`/`p3-bundleB` build, a byte difference (`diff ... && echo identical`) or a rejected `claude plugin validate` therefore all report `step bundles exit=0`. The chain logs 'windows race+bundles exit=0', and the C3.11 reproducibility gate reads green. The lint, gens and fuzz arms have the same masking, but they are not run tonight.
- **major** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:28,31-34 (as called by c8-night.sh:30)`: The pre-freeze refusal path is exposed to timing flakes the chain judges properly later. `internal` and `testpkgs` run with -p 2, which is real co-load, without QOMPACK_UNDER_COLOAD. So every wall-clock row of ci.yml's timing lane in internal/store, negknow, mcp and daemon (TestBudget_*, TestBudgetBF, TestGC_Deadline*, TestFeaturesFrom_LexicalCohesionShingleCap) is judged under co-load, and on whatever power source the laptop has, with no power record. Likewise, e2efunc judges X10's first-prompt delivery: TestV5_ThrashWarningVisibleInStatusAndCheckpoint fails on a late or deferred reply unless co-load or a non-reference disk is declared (test/e2e/v5_x10_test.go:472-484). One red stops the night before the freeze, although win-timing judges exactly those rows alone on AC after it. devtool test itself declares co-load for its parallel pass (test.go:100-104).
- **minor** `qompack-v6 plans/sdd/V6-closeout/coordinator/c8-night.sh (header comment, step 2) and prefreeze.sh; ledger D53(a)`: D53(a) says the pre-freeze merged-tree check runs integration's hot-path rows alone. c8-night.sh omits prefreeze's `hotpath` step, and its header says those rows are judged 'likewise' on AC, but win-timing's pattern (read from ci.yml's timing lane) holds only TestIntegration_HotPathWarmWithRealResidentState. So three rows never run alone, non-race, on the reference host in candidate 8's chain: TestIntegration_HotPathDegradesRatherThanBlocks, BAPopulationIsTheDaemonHistogram and SpoolTransitionJudgedPerMode. They run only under win-race (-race, co-load declared) after the freeze, and in hosted CI.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:17-21,28-31`: power() output is parsed unsafely. If the second word is missing (WMI returns no Win32_Battery instance, so the output is 'AC ' or 'BAT '), `"$3"` is unbound and set -u kills overnight-c8. Every later step is lost (race, Linux, quiet, release-check), and if the tag exists it is left behind. Any PowerShell error text (merged by 2>&1) makes $2 a word like 'Get-CimInstance'. The chain then waits 180 min on AC and logs garbage percentages. `$(power)` is also unquoted, so it is open to globbing.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:63,67 and c8-night.sh:53`: Several external commands have no time bound, and the detached chain has no watchdog. `docker desktop start` and `docker desktop stop` wait indefinitely by default. `git push` can block on a Git Credential Manager prompt in a detached session. A hung Docker Desktop start or WSL failure, or an expired credential, freezes the chain and holds keep-awake all night.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:45,51`: If the claude CLI is not on PATH in the detached environment, `bundle -host-validate` exits 0 with outcome 'unverified' ('the bundle is BUILT, not host-validated'). c8-night would then log 'bundles built and host-validated', which would be false.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:27-35`: Three D60(f) and merged-tree preconditions are not checked. First, nothing confirms that wave 19b (closeout/w19-rehydrate, closeout/w19b-cmdconnect) is in H; a launch before the merge freezes a candidate that fails C4.6 live. Second, the prefreeze gate runs plan lint on integration only, while the frozen candidate also contains verify/v6's own plans (the ledger, c7-CANDIDATE.md). An unsatisfiable -run pattern added to the ledger before the freeze would surface only in release-check's lint, about 1 h in. Third, every wave so far has needed runpatterns waivers for its reports, and wave 19b's reports are not yet merged.
- **minor** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:15,30`: The `hp` pattern skips all four TestIntegration_HotPath* rows, but only TestIntegration_HotPathWarmWithRealResidentState is in the AC timing lane. TestIntegration_HotPathDegradesRatherThanBlocks is a functional drain and degrade row ('no event is lost after the next drain'), which bears directly on candidate 8's passprogress and spoolwatch drain changes. On Windows it now runs only under -race co-load in win-race, after the freeze, and no wave 17c-19 report ran test/integration. The other two rows are pure fixtures. The c8-night header's 'likewise' (judged on AC) is not true for these three.
- **minor** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:23-26 (gate)`: The gate omits gen-command-docs --check, licenses --check and govulncheck. release-check runs all three ('generated docs', 'licenses', 'govulncheck'), but only after about 2-2.5 h of ci-local. The following would fail release-check late instead of refusing in minutes: a stale docs/commands.md from wave 19b's cmdconnect (command client error texts), a new dependency, or a Go vulnerability published before tonight (govulncheck also needs the network).
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:21-24,57-59 and overnight-c8.sh:81`: Neither script has an EXIT trap. If c8-night dies outside stop(), for example on an unbound variable or a kill, the sentinel stays and keepawake.ps1 holds ES_SYSTEM_REQUIRED indefinitely. overnight-c8 always ends with `log "done"`, so night.log's 'overnight finished exit=$?' is always 0 and carries no information. Step failures are visible only in chain.log.
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:34,41 (budgets)`: The session budgets have no margin. The sessions part plans 5 (UAT-05 run 2: 1, UAT-06: 3, F-C48-1: 1) against a budget of 5, and retrieval plans 2 (UAT-12 sessions A and B) against a budget of 2. The budget counts failed sessions, and candidate 7's lane lost sessions to a usage limit and to an API outage (D58(g); UAT-05 step 5 'the first attempt was void, a usage limit'). One void session, or one deny-rule settings file that `-p` silently ignores, ends the part as partial and forces another re-check run.
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:43-49 (retrieval part text)`: The UAT-12 re-check does not exercise D60(c)(1), the most serious wave 19b problem: 40 Bash-shaped pointers took 5 s through hostperm's on-disk resolution, so any project with a Read deny rule got the deferred note instead of its rehydration. Session A makes only a handful of tool calls, and nothing asserts that the block was injected rather than the deferred note, or records SessionStart:compact's host-seen latency. D60(c)(2)'s ',' and '=' punctuation are also not covered (only '(1)' and the apostrophe are).
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:36 and COMMON's WORKTREE (live-rerun-c7.js:34 after the swap)`: The generated script depends on state that does not exist at 738d67c7. It cites 'ADR 0011 §23', and UAT-05's loss-notice wording exists only on closeout/w19-rehydrate (ba217e44); at 738d67c7 the ADR ends at §22 and docs/uat.md has no notice text. Also, WT=qompack-cx-live is checked out on closeout/live7 (a1529fc1), while the brief tells agents they are on 'closeout/live8 at the frozen candidate'. Neither the generator nor the script creates or checks that branch.
- **major** `C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md:78 (D59, last sentence) and :240 (C1.6 cell); plans/sdd/V6-closeout/live/report-c7.md:37-39, 362-363`: C1.6 is marked 'fixed', but the fix does not hold. D59 and report-c7 say C1.6 'stays evidenced by its deterministic rows and candidate 4's lane'. Candidate 4's lane never ran C1.6, and its audit marked C1.6 partial because the startup accounting was being interrupted. The only live C1.6 run is candidate 3's, which predates wave 15c's rewrite of the publication pass (d84e1a7, not an ancestor of candidate 4 9f6a2fad). The checklist's C1.6 cell still promises that 'candidate 6's whole tree and live lane re-confirm it', but no candidate 6 lane ever ran (D57(c) moved the lane to candidate 7). Candidate 8's re-check (mkrecheck8.py) even deletes the audit's C1.6 question. So the publication pass that ships has never had a live C1.6 run.
- **major** `plans/sdd/V6-remediation/inventory-current.tsv rows 1.10.17, 1.16.11, 1.1.27; qompack-v6 coordinator/overnight-c8.sh (no c52 step); plans/sdd/V6-closeout/w17-inventory/runs/c52-executed-files.txt:107-110, 24-28`: D57(e)'s carry rule does not hold on candidate 8, and nothing re-dispositions the affected rows. Under D57(e), a candidate 5 C5.2 measurement counts as verified_in_target only while every file the benchmark executes is byte-unchanged. Candidate 8 changes internal/checkpoint/intent.go, source.go and writer.go, which BenchmarkFinalize and BenchmarkAdvanceSegment execute (rows 1.10.17 and 1.16.11; SP10-D1's 'fixed' rests on Finalize 46.05 ms against 50). It also changes internal/cli/config.go, which BenchmarkHookNoop_InProcess executes (row 1.1.27). overnight-c8.sh has no C5.2 step, and the inventory has c6_result and c7_result columns but no candidate 8 column, so C6.2's re-disposition from this candidate's evidence is not planned. By code reading the perf effect is nil: those fixtures record no prompts, so deriveCurrentWorkLocked returns at len(own)==0, and the writer.go branch is skipped either way. The process gap is real all the same.
- **major** `docs/uat.md:21-24; qompack-v6 V6-CLOSEOUT-CHECKLIST.md:72 (D53(f)) and :78 (D59 'The other rows carry from candidate 7 by the diff'); live/rerun-c4/C4.2, live/report.md (C4.7, candidate 3)`: Several rows carry to candidate 8 from candidates 3 and 4 with no D53(f) carry-forward note. D53(f) requires one for any row that is not re-run, naming the files that changed. D59's 'the other rows carry from candidate 7 by the diff' is false for C4.2 (last run on candidate 4), C4.7 (kill switches, last run on candidate 3), UAT-02, UAT-07 (retrieval four ways), UAT-08 (record_eliminated) and UAT-11, because none of them ran on candidate 7. Between candidate 4 and candidate 8 the tree took waves 15-19b: ledger, paging, hostperm 8.3 deny, reload semantics (D49, which governs kill-switch reloads), rehydrate, drain and status. No note names those files, and the candidate 8 re-check does not touch these rows.
- nit `prefreeze.sh:3-4; overnight-c8.sh:5-6; generated re-check header (live-rerun-c7.js:1-19 carried through)`: Several doc comments claim what the code does not do. prefreeze.sh says it runs 'at below-normal priority', but nothing sets a priority. overnight-c8 says timing steps 'start only on AC', but they run on battery after 180 min. The generated re-check still describes live-uat.js, replacing __CANDIDATE__, closeout/w7-livelane, branch closeout/live, a 36-session budget, and Install/Recovery phases with C1.6/C4.11 details. release-check is not recorded through recrun (no JSON with head, env and argv), and the cp at overnight-c8.sh:78 can copy a stale dist/release-check.json if `go run` fails to compile.

## impl:night: status `done`, head `c2b03a54543eb08f1a9a7ac3891c8f08680b59bb`

### Summary

No product code changed. Everything is coordinator tooling under plans/sdd/V6-closeout/coordinator/ on verify/v6: seven commits on top of 0f8ce75f, all with a "Refs: V6-VERIFY, C3.1" footer and no attribution trailers. Nothing was run for real: no chain, freeze, bundle build, tag, suite, Docker or Claude Code session.

All 21 findings and the nit were re-verified against the scripts before any change. 18 are fixed. Three are partial, because they also need edits to the ledger or the inventory, and those files are not mine.

**Proof the fixes are needed and work**
- **The harness:** nightharness.sh is committed. It stubs powershell, pwsh, docker, go, claude, gh, timeout, date and sleep, uses real git on scratch repositories, and runs on a fake clock, so no case depends on a wall-clock margin. It never touches the real repository, ~/.claude or ~/.qompack. On the final scripts all 39 cases pass, in 1500 s.
- **Red on 0f8ce75f:** I ran the same harness against the old overnight-c8.sh, c8-night.sh, prefreeze.sh and phase3.sh. All 18 selected cases fail, each on the assertion that matches its defect:
  - O2: try 2 is refused with "refusing to overwrite".
  - O3: the log says "VALID power=BAT", and the old night ran on battery until 16:01 the next day with no deadline.
  - O8: it crashes on "$3: unbound variable".
  - O9: v0.3.0 is left in the shared ref store after a signal.
  - N5: an earlier run's pass line lets a failing gate freeze.
  - N7: an "unverified" host validation is accepted.
  - N9: the keep-awake sentinel survives a signal.
  - P2-P5: the bundles and lint arms exit 0 on failure.
  - F1, F3, F4, N2, N8, O11: each fails on its own missing check.

**What tonight's chain now does**
- **Power verdicts:**
  - Each gated try takes t0 before its last power check, then reads Kernel-Power 105 (power source changed) and 506 (Modern Standby) by epoch.
  - VALID needs AC at the start and no event during the run.
  - INVALID-POWER moves try 1's new records into <step>.invalid-power-1/ under their own names, then retries once.
  - NOT-REFERENCE: the step still runs on battery when the night's single 180-minute AC wait budget is spent, but is never labelled VALID.
  - SKIPPED: the step would start after NIGHT_DEADLINE.
  - Gated steps wait in a queue while the work that does not need AC runs, and that load also drains the battery towards the charger's restore point.
- **release-check:**
  - It runs in a `git clone --no-local` scratch copy whose origin and push URL point nowhere. The v0.3.0 tag exists only in that copy, and an EXIT trap removes it.
  - It is recorded through recrun with --evidence-copy and time-stamped output.
  - Its power validity is judged only over the isolated test/e2e passes of ci-local test and ci-local cover. It retries only when a power event fell inside one of those windows, or the window ran on battery, and a second run can still end before the deadline.
- **New C5.2 step:** an AC-gated quiet.sh c52-win run of BenchmarkFinalize, BenchmarkAdvanceSegment and BenchmarkHookNoop_InProcess against cf31e01.
- **Outcome:** docker and push calls have time limits, and night.log now ends with the counts of passed, failed, invalid, not-reference and skipped steps.
- **phase3.sh:** the bundles, lint, gens and fuzz steps now fail if any of their commands fails. win-timing also runs the three functional integration hot-path rows alone, and checks that each one reported a pass.
- **prefreeze.sh:**
  - A fresh summary.log per run, every line tagged with that run's id and a power verdict.
  - QOMPACK_UNDER_COLOAD=1 on the shared -p 2 passes; the strict steps clear any inherited value.
  - The integration step keeps TestIntegration_HotPathDegradesRatherThanBlocks, TestIntegration_HotPathBAPopulationIsTheDaemonHistogram and TestIntegration_HotPathSpoolTransitionJudgedPerMode, and fails unless each passes.
  - The gate adds gen-command-docs --check, licenses --check and govulncheck. If govulncheck cannot reach its database, gate.log says GOVULNCHECK-UNREACHABLE and the gate passes, so that tree is not checked locally; a reported vulnerability or any other error fails the gate.
- **c8-night.sh:**
  - An EXIT trap releases keep-awake, and the script writes its own output to night.log.
  - It refuses unless the tips of closeout/w19-rehydrate, closeout/w19b-cmdconnect and every closeout/w19c-* and w20-* branch are merged into integration. C8_EXEMPT names a branch left out on purpose, and each exemption is logged.
  - The merged tree (verify/v6 + integration) must pass runpatterns, docmarkers, coveragefloors, test/guards and test/docs in a scratch clone, and the frozen tree must equal that linted tree.
  - An earlier run's prefreeze directory is moved aside, never read.
  - An e2efunc failure without a VALID power record is re-run once on AC; a VALID failure refuses.
  - Host validation must be "accepted".
  - The push runs with no prompt and a time limit, and nightly is dispatched only after it succeeds.

**The re-check generator (mkrecheck8.py)**
- Session budgets are now sessions 6, retrieval 5, carried 8 and resilience 4, 23 in all. Each part has one spare (retrieval two), and a spare may only replace a void or invalid session.
- New C1.6 leg.
- New carried part: UAT-02/C4.2, UAT-07, UAT-08, UAT-11 and C4.7.
- UAT-12 now covers the ',' and '=' denied names, proves the deny rules are in effect in each project, and adds session C: 40+ Bash and MCP calls, then /compact must inject the block, never the deferred note, with SessionStart:compact's latency recorded.
- UAT-09 O-1: whether SessionEnd reaches the daemon after a final /compact.
- The generated header holds the closeout/live8 launch notes, and each part's first step refuses any other branch or base.
- It refuses a candidate without ADR 0011 §23: it refused 738d67c7 and built from 1dd7b00d. It also refuses a missing BUNDLE.json, leftover candidate 7 strings, Date.now and TypeScript, and output node cannot parse.

**README**
README.md now has the candidate 8 launch, watch and abort procedure.

**Checks**
- fmt-check passes.
- Plan lint (runpatterns, docmarkers, coveragefloors), test/guards (37.6 s) and test/docs (3.8 s) all pass on the merged tree of c2b03a54 + 738d67c7.
- In the qompack-v6 worktree itself runpatterns fails, but only because the git-ignored dist/v6-remediation/ holds stray .go packages that are not mine.
- No Go package or Go test changed, so vet, golangci-lint, -count=20 and -race do not apply.

### Commits

- 067fab8f chore(v6): retry invalid-power night steps on their own records
- 86193037 chore(v6): fail phase3's multi-command arms on any failure
- bdc15929 chore(v6): gate candidate 8's freeze on this run's evidence
- 836bc3d2 chore(v6): widen candidate 8's live re-check
- ccea07ab docs(v6): record candidate 8's launch and abort procedure
- 51f34451 chore(v6): add a dry harness for candidate 8's night scripts
- c2b03a54 docs(v6): tighten candidate 8's abort and re-run steps

### Findings resolution

- **fixed**: 0 BLOCKER overnight-c8.sh timed(): retry is a no-op because recrun.sh refuses to overwrite; try 2 logged VALID
  - Reproduced: base harness O2 shows 'refusing to overwrite p3-win-timing' on try 2. Fix 067fab8f: each try snapshots the evidence dir; on INVALID-POWER the try's new entries move into <step>.invalid-power-<n>/ under their own names, so each JSON's log field stays true. The INVALID line names that directory. Records keep their canonical ids, and quiet steps move their whole dir. Green rows: O2, O6, O10. Red on base: O2.
- **fixed**: 1 acwait timeout ignored (battery run VALID), t0 after last power check, no overall deadline
  - Reproduced: base O3 logs 'VALID power=BAT' and runs on battery until 16:01 the next day. Fix: t0 is taken before the last power read. NOT-REFERENCE when no AC within one night-wide 180-minute AC_WAIT_BUDGET_MIN, counted in polls. NIGHT_DEADLINE (default 08:00): no step or wait starts after it. Gated steps queue behind the work that does not need AC. Green: O3, O4, O5, U3. Red: O3.
- **fixed**: 2 release-check judged in one 3-hour INVALID-POWER window and auto-retried
  - stamped.sh epoch-stamps release-check's output, recorded via recrun with --evidence-copy and no stale cp. rc_windows finds the isolated test/e2e pass of 'ci-local test' and 'ci-local cover', from the last package line to e2e's result line, which is conservative. INVALID-POWER only for an event in a window or a window on battery. Retry only if now plus try 1's duration is before the deadline. Starts only if RC_EST_S lets it end in time. Green: O6 (event in window: retried), O7 (event during lint: VALID, not retried), O3 (windows on battery).
- **fixed**: 3 prefreeze summary.log appended; c8-night accepts any earlier run's pass line
  - prefreeze truncates summary.log per run and tags every line run=<PREFREEZE_RUN>. c8-night greps '^step <s> exit=0 run=<id> ' and moves an earlier prefreeze dir aside to prefreeze.run-<n>. Green: N5, F4. Red: N5 (base froze on the stale line).
- **fixed**: 4 v0.3.0 tag created in the shared ref store, no trap, creation unchecked
  - release-check runs in a `git clone --no-local --no-tags` scratch clone. Origin and push URL are set to file:///nonexistent/...; the tag is made only there and checked with describe; an EXIT trap removes the clone. The shared store is checked before and after. Green: O1 (TAG=v0.3.0 in clone, ORIGIN unreachable, clone removed, no tag in source), O9 (TERM mid-run: exit 143, clone removed). Red: O9 (base leaves v0.3.0 in the shared store).
- **fixed**: 5 phase3.sh bundles arm masks build, diff and validate failures (lint, gens, fuzz too)
  - 86193037: every multi-command arm accumulates r and ends with [ $r -eq 0 ]. bundles validates each of the six targets by name, fails on a missing one, uses xargs -r, and fails on an empty or different sha list. fuzz loops over a file, not a subshell pipe, and fails on no targets. Green: P1-P6. Red: P2, P3, P4, P5.
- **fixed**: 6 prefreeze -p 2 passes judged without QOMPACK_UNDER_COLOAD and with no power record
  - integration, testpkgs and internal declare QOMPACK_UNDER_COLOAD=1 as ci.yml's test job does. e2e, e2efunc and hotpath clear an inherited value; NONREFERENCE_DISK is always unset. Every step records power=<verdict>. c8-night re-runs an e2efunc failure once on AC when its verdict is not VALID, and refuses a VALID failure. Skeptic note accepted: e2efunc stays strict (-p 1, no declaration). Green: N1, N10, N11, F4. Red: F4.
- **fixed**: 7 D53(a): integration hot-path rows not run alone; c8-night header 'likewise' false
  - The pre-freeze integration step now skips only TestIntegration_HotPathWarmWithRealResidentState; the three functional rows run there and must each report '--- PASS'. win-timing also runs those three alone on AC (p3-win-hotpath, -v, per-row check). Headers corrected. Green: F3, P7, N1. Red: F3.
- **fixed**: 8 power() parsing unsafe (no battery instance, error text, unquoted), set -u crash
  - power.sh: the query prints 'POWER AC|BAT <pct>' and anything unparsed reads 'UNKNOWN ?'. Events come from Kernel-Power 105 and 506 by epoch with an END sentinel; an unreadable log gives NOT-REFERENCE. No unguarded positional reads. Green: U1, U2, O8 (nobattery, error text and garbage, no crash). Red: O8 ('$3: unbound variable').
- **fixed**: 9 no time bounds on docker desktop start/stop, git push, gh
  - docker desktop start/stop --timeout 600/300 inside an outer timeout, run only when the chain must start the engine (D56(g)). docker ps/start/update/stop bounded. Push: GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never timeout -k 30 300. gh: GH_PROMPT_DISABLED=1 timeout 120, dispatched only after a successful push. Green: O11, N8, O1. Red: O11, N8.
- **fixed**: 10 bundle -host-validate 'unverified' logged as host-validated
  - c8-night parses the outcome from host-validate.txt and refuses anything but 'accepted', naming it. It also refuses a missing bin, BUNDLE.json or checksums.txt. Green: N7. Red: N7 (base pushed an unverified bundle).
- **fixed**: 11 c8-night preconditions missing (wave 19b merged, merged-tree plan lint)
  - Refuses unless the w19-rehydrate, w19b-cmdconnect and every w19c-*/w20-* tip is an ancestor of integration's HEAD; C8_EXEMPT logs each exemption with its tip. Refuses if verify/v6 is off-branch or has tracked changes. Merged tree (verify/v6 + H) built in a no-push scratch clone must pass runpatterns, docmarkers, coveragefloors, test/guards and test/docs, and its tree must equal the frozen tree. Verified for real on c2b03a54 + 738d67c7: lint PASS x3, guards ok 37.6 s, docs ok 3.8 s. Green: N2, N3, N4, N6, N12. Red: N2.
- **fixed**: 12 prefreeze hp pattern skips all four TestIntegration_HotPath rows
  - Same fix as 7. hpwall='^TestIntegration_HotPathWarmWithRealResidentState$' for the integration skip; the hotpath step runs '^TestIntegration_HotPath' alone.
- **fixed**: 13 gate omits gen-command-docs --check, licenses --check, govulncheck
  - Added to the gate. A govulncheck failure whose output is a recognised vuln.go.dev, proxy or sum network error is written as GOVULNCHECK-UNREACHABLE and not fatal; c8-night copies a WARNING into night.log. Reported vulnerabilities or any other error fail the gate. Skeptic 2 is right that TestGenCommandDocs_CommittedPageIsCurrent in 'internal' already catches a stale commands.md; the gate check just refuses sooner. Green: F1, F2, N1. Red: F1.
- **fixed**: 14 no EXIT trap for the keep-awake sentinel; overnight exit always 0
  - EXIT trap plus INT/TERM/HUP traps in c8-night; overnight-c8 has an EXIT trap for its clone. overnight writes overnight-outcome.txt with all counts and exits 0 only when everything passed VALID; night.log copies it. A hard Stop-Process kill skips traps, and the README abort procedure covers that. Green: N9, N1. Red: N9.
- **fixed**: 15 mkrecheck8 session budgets have no margin
  - Budgets now sessions 6 (5+1), retrieval 5 (3+1+1 for a deny-rule settings file the host ignored), carried 8 (7+1), resilience 4 (3+1). RERUN states a spare only replaces a void or invalid session, with the reason in sessions.tsv and the notes, and to run rows in order and record skips if short.
- **fixed**: 16 UAT-12 re-check misses D60(c)(1) 40-pointer load and the ',' and '=' names
  - Retrieval adds private/a,b.txt and private/k=v.txt, plus a deny-rule proof in each project's first turn. New session C: 40+ Bash and MCP calls, then /compact must inject the block, not 'Qompack could not deliver this compaction's rehydration'. It records section 6's pointer count, session_start_compact_deferred, LOUD.log and SessionStart:compact's host-seen latency from hooks.json. Skeptic note: D53(i) already recorded latency in aggregate; the leg adds a per-session assertion.
- **fixed**: 17 generated script depends on ADR 0011 §23 and closeout/live8, neither checked
  - The generator refuses a candidate whose ADR 0011 lacks a '## 23. ... loss' heading: it refused 738d67c7 and built from 1dd7b00d. The docstring states the wave 19b/19c dependency. The generated header carries exact live8 create/verify steps, and every part's first step checks branch closeout/live8, CAND as ancestor, and a diff limited to the lane's evidence and docs/uat.md, else returns blocked.
- **partial**: 18 C1.6 marked fixed on a false citation; no live C1.6 since candidate 3
  - Re-check: the resilience part gains a C1.6 leg. A daemon ended mid-session by its own lane, fsck before, candidate 3's two publication-gap cuts, restart, the startup accounting counters and LOUD line with no 'incomplete', fsck after, re-fingerprint, and an honest statement on automatic recovery. The audit's C1.6 question is kept. Not done, ledger is read-only to me: D59's C1.6 sentence and the C1.6 cell (V6-CLOSEOUT-CHECKLIST.md:240) still cite candidate 4's lane and a candidate 6 lane that never ran; both need the coordinator's correction.
- **partial**: 19 D57(e) C5.2 carry breaks on candidate 8 (checkpoint intent/source/writer.go, cli/config.go); no c52 step
  - overnight-c8 gains an AC-gated c52-win step: quiet.sh with QUIET_PKGS='checkpoint cli' and QUIET_BENCH_FILTER for BenchmarkFinalize, BenchmarkAdvanceSegment and BenchmarkHookNoop_InProcess against cf31e01, 10 ABBA rounds, its own evidence dir quiet-c52. Green: O1 asserts the call. Not done, not my file: inventory-current.tsv needs a candidate 8 disposition for rows 1.10.17, 1.16.11 and 1.1.27 from tonight's result.
- **partial**: 20 rows carried from candidates 3/4 (UAT-02/07/08/11, C4.2, C4.7) with no D53(f) note
  - Re-check gains a 'carried' part re-running UAT-02 with C4.2 (1), UAT-07 (1), UAT-08 (2), UAT-11 (1) and C4.7's two kill switches (2: runtime.mode off; runtime.migration.reinjection.sessionStartCompact false), sized from candidate 4's sessions.tsv rows and candidate 3's C4.7, 7+1 sessions. Not done, ledger: D59's 'the other rows carry from candidate 7 by the diff' needs correcting. C4.4 is not re-run: it passed live on candidate 7 and D61(b)(4) reverts hostperm; a carry note is owed if wave 19c keeps hostperm changes.
- **fixed**: nit 0 doc comments claim what code does not; release-check not recorded; stale cp
  - prefreeze header now says normal priority. overnight header describes VALID/INVALID-POWER/NOT-REFERENCE/SKIPPED truthfully. mkrecheck8 rewrites the generated header and meta.phases (asserts no 'live-uat.js', '__CANDIDATE__', 'closeout/w7-livelane', '36 real'). release-check is recorded by recrun as p3-release-check-tag with --evidence-copy, moved aside per try, and refused rather than re-read if a stale record exists (O12).

### Tests

- `cd plans/sdd/V6-closeout/coordinator && sh nightharness.sh   (all 39 cases, final scripts at c2b03a54)`: 39 case(s), 0 failed; wall 1500 s
- `sh <scratch>/red-on-base.sh O1_all_ac O2_transition_retries_with_own_records O3_battery_night_is_never_valid O8_unreadable_power_never_crashes O9_signal_removes_the_clone O11_engine_and_container N2_unmerged_branch_refuses N5_stale_prefreeze_pass_cannot_satisfy N7_unverified_host_validation_refuses N8_push_timeout_is_bounded N9_signal_releases_keepawake P2_bundle_build_failure_propagates P3_byte_difference_propagates P4_validate_rejection_propagates P5_lint_arm_middle_failure F1_govulncheck_offline_is_reported_not_fatal F3_integration_requires_functional_rows F4_fresh_summary_and_declarations   (the same harness against 0f8ce75f's overnight-c8.sh, c8-night.sh, prefreeze.sh, phase3.sh)`: 18 case(s), 18 failed (expected red), each on its defect's assertion, e.g. O2 'try 2 really ran (no overwrite refusal)', O3 'no VALID verdict on battery in chain.log', O8 'no unbound variable', O9 'no tag in the shared ref store', N5 'this run's gate failure refuses'
- `sh -n power.sh stamped.sh overnight-c8.sh c8-night.sh prefreeze.sh phase3.sh nightharness.sh; python -m py_compile mkrecheck8.py`: all parse
- `python mkrecheck8.py live-rerun-c7.js <scratch>/x.js 738d67c74db6743733c6744694ae8c3928d4052a <c7 bundle>`: refused: ADR 0011 at the candidate has no section 23 (expected at integration today)
- `python mkrecheck8.py live-rerun-c7.js <scratch>/x.js 1dd7b00de02644f531690f5e51bd7fc17d246332 <c7 bundle>   (sample on a tree with ADR §23)`: generated; node AsyncFunction parse OK; no Date.now/Math.random/TypeScript; no candidate 7 strings; header, meta.phases, budgets 6/5/8/4 and audit coverage reviewed
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns   (in the qompack-v6 worktree)`: docmarkers PASS; runpatterns FAIL for an environmental reason: git-ignored dist/v6-remediation/ holds stray .go packages (not mine, untouched) that go test -list compiles
- `scratch clone of verify/v6 c2b03a54 merged with closeout/integration 738d67c7 (merged tree eb71c187): go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors && go test -count=1 ./test/guards ./test/docs`: PASS runpatterns, PASS docmarkers, PASS coveragefloors; ok test/guards 37.596s; ok test/docs 3.805s
- `power.sh's PowerShell event query against the real System log (read-only, last 24 h)`: returned 105 and 506 events as '<epoch> AC=0|1' / '<epoch> STANDBY' plus the END sentinel; NoMatchingEventsFound id confirmed
- `go vet (windows/linux/darwin), golangci-lint, -count=20, -race -count=3`: not applicable: no Go package or Go test was touched

### Criterion changes

- prefreeze integration step: it now skips only TestIntegration_HotPathWarmWithRealResidentState, which the night judges alone on AC. The three functional hot-path rows run there with -v and must each print '--- PASS'. Stricter.
- prefreeze integration, testpkgs and internal now run under QOMPACK_UNDER_COLOAD=1, as ci.yml's shared test job does, so their wall-clock rows report instead of judging. Reason: they run in parallel at -p 2, and the same rows are judged alone on AC by win-timing (finding 6). The strict steps (e2e, e2efunc, hotpath) clear any inherited declaration.
- phase3 win-timing also runs the three functional integration hot-path rows alone on AC (p3-win-hotpath), per D53(a). Stricter.
- phase3 lint, gens, fuzz and bundles fail if any command fails, not only the last. bundles also fails on a missing target or an empty sha list. Stricter.
- The gate adds gen-command-docs --check, licenses --check and govulncheck. A govulncheck failure that is only a recognised network or database-unreachable error is recorded (GOVULNCHECK-UNREACHABLE, plus a WARNING in night.log) and is not fatal. Any vulnerability or other error fails the gate.
- A gated step that could not get AC is labelled NOT-REFERENCE (neither a pass nor a fail), never VALID. INVALID-POWER now also covers Kernel-Power 506 (Modern Standby) during the step.
- release-check is INVALID-POWER only when a power event falls inside, or a battery state covers, its isolated test/e2e pass in ci-local test or ci-local cover. Before, any event in the whole 3-hour run voided it. Reason: D57(d) concerns timing measurements, and every other pass declares co-load or holds no timing judgement (finding 2).
- A pre-freeze e2efunc failure whose power verdict is not VALID is re-run once on AC, within the shared wait budget; before, it refused immediately. A failure with a VALID power verdict still refuses.
- c8-night newly refuses on: an unmerged wave 19b, 19c or 20 tip; a merged tree that fails plan lint, test/guards or test/docs; a frozen tree that differs from the linted one; and any host-validation outcome other than 'accepted'. Nightly is dispatched only after a successful push. Stricter.
- New SKIPPED verdict: a step that would start after NIGHT_DEADLINE does not run. Before, there was no deadline.

### Open issues

- The live re-check can be generated only once wave 19c's ADR 0011 section 23 is in the candidate. If wave 19c renumbers or retitles §23, mkrecheck8.py refuses, and its check and part text must be updated.
- The qompack-v6 worktree's git-ignored dist/v6-remediation/ holds stray .go packages, so `devtool lint --only=runpatterns` run there gives a false red. I left those files alone. c8-night lints in a scratch clone, so the night is unaffected.
- C4.4 is not re-run in the re-check: it passed live on candidate 7, and D61(b)(4) reverts hostperm to a357d187. If wave 19c keeps hostperm changes, a D53(f) carry note or a C4.4 session is owed.
- Modern Standby entries (Kernel-Power 506) appear in today's real log, and a standby during a gated step now makes it INVALID-POWER. keepawake.ps1 holds ES_SYSTEM_REQUIRED only. If tonight shows STANDBY invalidations, ES_DISPLAY_REQUIRED may be needed (memory notes a display hold was needed once). This is unverified and was not changed.
- The new deadline means a night spent on battery SKIPs or NOT-REFERENCEs its gated steps instead of running into the day; base O3 showed the old chain running until 16:01 the next day.
- The harness cannot exercise real PowerShell, Docker, git push or the claude CLI. The PowerShell queries were checked read-only against the real log, and the clone, merge and lint path against today's real trees.
- The mkrecheck8 sample was generated against the closeout/w19-rehydrate tip only as a dry run. The real script must be generated against the frozen candidate and its c8 bundle, and its header steps followed.

### Needs owner

- NIGHT_DEADLINE, default 08:00 local (a new number): no night step or AC wait starts after it. Derivation: the owner needs the CPU by day. The c6/c7 nights ended around 04:00-05:30 local, a full c8 night with release-check is about 7.5 h, and 08:00 leaves room for part of one AC wait. It can be overridden at launch.
- RC_EST_S = 10800 s (3 h, new): release-check starts only if it can end by the deadline. Derivation: SP-17's local record of 8451 s on a smaller tree, and w17-release's 2.5-3 h estimate, rounded up. A retry is sized by try 1's own measured duration instead.
- AC_WAIT_BUDGET_MIN = 180 now bounds the whole night's AC waiting, counted in one-minute polls. Before, it was 180 per step, up to about 15 h in total.
- Time limits on hangs, not on measured work (new numbers, taken from the audit's fix suggestions): docker desktop start 600 s and stop 300 s (outer limit +60 s); docker start 300 s, docker stop 120 s, docker ps and update 60 s; git push 300 s; gh workflow run 120 s.
- The live re-check now plans 23 real sessions (sessions 6, retrieval 5, carried 8, resilience 4, each with a spare). That is against D3's ~40-80, which C5.5's 40 trials also draw on, and D53(g) already notes the owner's session budget is exceeded.
- Ratify the criterion changes listed below, in particular: release-check's power validity limited to its isolated test/e2e windows; co-load declared on the pre-freeze shared -p 2 passes; a pre-freeze e2efunc failure without a VALID power record re-run once on AC; the network-only govulncheck failure being non-fatal in the gate.
- Ledger and inventory edits for the coordinator, read-only to me: correct D59's C1.6 sentence and the C1.6 cell (V6-CLOSEOUT-CHECKLIST.md:240); correct D59's 'the other rows carry from candidate 7 by the diff', which does not cover UAT-02/07/08/11, C4.2 or C4.7; add a candidate 8 disposition for inventory rows 1.10.17, 1.16.11 and 1.1.27 from tonight's c52-win.

## review:night:0:r1: verdict `needs-fixes`, 8 finding(s)

- **major** `plans/sdd/V6-closeout/coordinator/c8-night.sh:44-51 (with keepawake.ps1:10 and README.md:109-110)`: An early refusal can leave keep-awake held with no time limit. keepawake.ps1 creates its sentinel when the file is missing (`if (-not (Test-Path $Sentinel)) { New-Item ... }`), and it only reaches that check after pwsh has started and run Add-Type. c8-night fires keepawake in the background and keeps no pid. Its quickest refusals come about 1.5 s after launch: 'verify/v6 has tracked changes', 'integration worktree is not clean', a branch not merged. When one of those fires, the EXIT trap deletes the sentinel before keepawake.ps1 checks for it, so keepawake.ps1 creates it again and holds ES_SYSTEM_REQUIRED until someone notices. These refusals are the most likely ways a launch fails, and the README tells the coordinator that a refusal already releases keep-awake. The audit's finding 14 asked for exactly this guarantee, so in practice it is not delivered.
  - Evidence: Timed on this host: pwsh -File with the same Add-Type took 2.39 s to reach Test-Path, and a sentinel deleted 0.3 s after launch read sentinel-exists=False at that point. One power_read through powershell 5.1 takes 0.97 s. I added harness case X7_early_refusal_keepawake_race to scratchpad/probe/probeharness.sh: the pwsh stub mimics keepawake.ps1 (sleep 2.4, then create the sentinel if it is missing), and the case dirties the ledger. Result: night rc=1 'REFUSED: verify/v6 has tracked changes', then 4 s later keepawake.sentinel exists and keepawake.log says 'keep-awake held while ... exists'. The committed N2/N9/N12 cases cannot catch this because their pwsh stub returns at once.
  - Fix: Make keepawake.ps1 exit when the sentinel is missing at start, and never create it; the README re-run path already creates it first. In c8-night, capture `kp=$!` after the pwsh launch, and have cleanup() run both `rm -f "$sentinel"` and `kill "$kp" 2>/dev/null` (or Stop-Process on its winpid). Add a harness case whose pwsh stub has that startup delay.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:48,61 with c8-night.sh:40-41,49,166-168 and power.sh:81-89`: The night deadline can roll over by 24 hours between the two scripts. c8-night works out its deadline epoch, but passes only the HH:MM string to overnight-c8.sh, and never checks the deadline before launching it. overnight-c8.sh then computes 'the next HH:MM after now'. If the pre-freeze ends after the deadline, overnight-c8.sh gets TOMORROW's HH:MM and runs every gated step, Linux lane and release-check, VALID, into the owner's day. That is what NIGHT_DEADLINE exists to prevent. Two ways in: a late launch, or the e2efunc AC re-run finishing just after the deadline, since its AC wait is bounded by the deadline but the 30-minute re-run is not.
  - Evidence: Probe X1_deadline_rolls_over_between_scripts: NIGHT_DEADLINE=22:10 and e2efunc taking 15 min, fake clock starting at 22:00. night.log reads 'deadline=2026-10-03T22:10:00', but chain.log reads 'start candidate=... deadline=2026-10-04T22:10:00', followed by 'step win-timing try 1 start power=AC 77' and every other gated step. Nothing is SKIPPED.
  - Fix: Have c8-night export NIGHT_DEADLINE_EPOCH="$deadline". overnight-c8.sh should use it when set (checked numeric) and fall back to deadline_epoch only when run standalone. If the epoch has already passed, every step must record SKIPPED and the outcome file must still be written. Optionally refuse a standalone deadline more than about 14 h away unless told to allow it. Add the X1 case to the harness.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:251 (rc_verdict) with rc_windows 224-247`: Release-check's power verdict fails open. When rc_windows finds no AC-sensitive window, rc_verdict returns 'VALID (no AC-sensitive window was reached)' whatever the power history says. That is right only when release-check stopped before `ci-local test`. If release-check PASSES (so both e2e passes ran) but its output carries no recognisable header, the run is called VALID even when it ran wholly on battery. Ways that can happen: drift in the header format or in ciLocalSteps' names ('test', 'cover'), or a stamping failure. The windows are release-check's only power judgement, so a parsing miss must not read as VALID.
  - Evidence: Probe X3_rc_no_windows_on_battery_is_valid: timeline BAT throughout, and release-check output that uses '--- release-check: ci-local test ---' instead of '=== ... ==='. chain.log: 'step release-check try 1 exit=0 VALID (no AC-sensitive window was reached) power=BAT 77->BAT 77; events during the run: []', counted as a pass.
  - Fix: Return 'VALID (no AC-sensitive window was reached)' only when the log shows the run stopped before it: the stamped log has '=== release-check: version agreement ===' (a format check) and no '=== release-check: ci-local test ===' header, and rc != 0. When rc = 0 and no window was found, return NOT-REFERENCE 'AC-sensitive windows not found in the log'. Add a harness case.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:82,101-109,130-149 (compare 272-276); c8-night.sh:170`: A re-run into an evidence directory that already holds records repeats the BLOCKER's mislabel. Only release_check refuses an existing record. For the gated phase3 and quiet steps, recrun.sh's 'refusing to overwrite' makes the step exit 1 within seconds, and run_gated logs it as a VALID failure of a run that never happened. overnight-c8.sh also treats an existing power.tsv as normal: it appends the header only when the file is missing. c8-night prints overnight-outcome.txt without first removing an earlier copy, so an overnight that dies early can be reported with an older run's counts. The README's re-run path uses a fresh directory, but nothing enforces that.
  - Evidence: Probe X2_rerun_into_same_dir_mislabels: two overnight runs into the same directory. The second logs 'refusing to overwrite p3-win-timing / step win-timing exit=2 / step win-timing try 1 exit=1 VALID power=AC 77->AC 77', and the same for win-e2e-timing and win-x11-alone.
  - Fix: At start, overnight-c8.sh should refuse when E already holds a night's records (power.tsv rows, p3-win-*.json, quiet*/, overnight-outcome.txt, *.invalid-power-*/). Alternatively, check each gated step's record ids before a try, as release_check does, and record 'exit=2 existing record' as a refusal, never VALID. Have c8-night delete or move aside $E8/overnight-outcome.txt before it launches overnight-c8.sh.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:177-205 (compare 328)`: Docker engine ownership is decided from a single `docker ps` probe made when the Linux lanes start, not from the engine's state at the night's start. If the owner's engine was up at the start (line 328 logs 'engine up at start=1') but `docker ps` fails once at lane time, the chain sets ll_started=1 and then runs `docker desktop stop`. The failure could be an engine restart, a WSL hiccup after win-race, or a probe slower than 60 s. `docker desktop start` is a no-op that succeeds on a running Desktop, so the stop takes down the owner's supabase engine, which D56(g) and the owner's Docker rule forbid. The base script used the start-of-night probe.
  - Evidence: Probe X4_transient_ps_failure_stops_owner_engine: a docker stub where the 3rd `ps` fails and every other call succeeds. chain.log: 'engine up at start=1' ... 'docker engine not answering: starting it' ... 'engine started by this chain' ... 'engine stopped exit=0'; calls.log has 'docker desktop stop'.
  - Fix: Record engine_up_at_start from line 328's probe. Run `docker desktop stop` only when the engine was down at the night's start AND this chain started it. Before treating a failed `docker ps` as 'engine down', retry it, or ask `docker desktop status`.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:130-139 (preconditions block 54-68)`: Two cheap checks the README lists as before-launch conditions are made only AFTER the freeze commit: qompack-bundles/c8 must not exist, and qompack-cx-cand must be clean. If either fails, c8-night has already spent the merged-tree check and the 1-2 h pre-freeze, then refuses with an unpushed freeze commit on verify/v6, which README Abort step 6 turns into a coordinator decision to reset. This check order predates this diff, but the task asked for correct preconditions.
  - Evidence: Probe X5_bundle_dir_checked_after_freeze: with Projects/qompack-bundles/c8 created beforehand, night.log ends 'REFUSED: .../qompack-bundles/c8 already exists' and verify/v6's HEAD has moved to the freeze merge (the check 'refused before anything is frozen' fails).
  - Fix: In step 2, add `[ -e "$root/qompack-bundles/c8" ] && stop ...` and `[ -z "$(git -C "$CAND" status --porcelain)" ] || stop ...`. Keep the post-checkout cleanliness check as well.
- **nit** `plans/sdd/V6-closeout/coordinator/power.sh:44,58-65`: The verdict depends only on the start reading and the 105/506 events. The end reading p1 is recorded but never used, although the audit's fix asked for 'start AC, end AC, no transitions'. A p1 of BAT with no event in the window is inconsistent and should not read VALID. Kernel-Power 42 (sleep or hibernate) and 107 (resume) are not watched either, and both appear in this host's log (10-01 12:37:49, 10-03 09:49:48).
  - Evidence: Read Kernel-Power 105/506/507/42/107 for the last 4 days, read-only. No 506 fell inside the keep-awake windows of the c6/c7 nights (10-01 21:03 to 10-02 06:01). 42/107 pairs occur outside them.
  - Fix: In run_gated and prefreeze's run(), downgrade VALID to NOT-REFERENCE when p1 does not start with 'AC ' and no event explains it. Add Id=42,107 to the Get-WinEvent filter and emit them as SLEEP and RESUME events, which count as INVALID-POWER.
- **nit** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:285-306`: release-check's try 2 runs in try 1's scratch clone. Anything try 1 left that is not git-ignored, such as a fuzz corpus written under testdata/fuzz on a failure (one such directory is untracked in the main repo now), shows up in try 2's recrun record as source_dirty. Try 2's reference run then does not start from the pristine candidate tree that rc_prepare verified.
  - Evidence: rc_prepare checks `status --porcelain` empty once, before try 1. The retry loop goes back to `continue` with the same $RCT/repo and no further check.
  - Fix: Before try 2, remove the clone and run rc_prepare again (cheap compared with a 3 h run), or at least require `git -C "$RCT/repo" status --porcelain` to be empty and refuse the retry otherwise.

## review:night:1:r1: verdict `needs-fixes`, 10 finding(s)

- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:105-106 (c52-win), header :9-11`: The new C5.2 step hard-codes three benchmarks (QUIET_PKGS="checkpoint cli", filter Finalize|AdvanceSegment|HookNoop_InProcess). But c8-night.sh:61 refuses unless every closeout/w20-* tip is merged, and wave 20 changes files that other C5.2 benchmarks execute. Under D57(e) those rows lose their carry too, and nothing re-measures them. (1) w20-redeliver changes the three daemon scheduler files that rows 1.12.17 and 1.16.11's daemon benches execute. (2) w20-config changes internal/config/config.go (a new Warning.VersionedReset field) and migration.go, both executed by BenchmarkConfigLoad_ColdNoFiles (row 1.1.27). (3) Row 1.10.17 itself names BenchmarkExtractDecisions, and the checkpoint set (which also holds 1.16.11's StripInjections and Truncate) executes intent.go, source.go, writer.go and draft.go. Those files changed in w19 and w20 forkwork, and only a set-level executed-files proof exists. Finding 19 is therefore still open within the seat's own files.
  - Evidence: w17-inventory/runs/c52-executed-files.txt (3630f54e) gives the executed files per set. The daemon set (rows 1.12.17, 1.16.11) executes delivery_radix.go and scheduler_candidates/droppable/features/runtime/state/tap.go. The config set (1.1.27) executes config.go, deadlines.go, defaults.go, load.go, migration.go and validate.go. The checkpoint set (1.10.17, 1.16.11) executes decisions.go, draft.go, ... intent.go ... source.go ... writer.go. `git diff --stat 738d67c7 closeout/w20-redeliver` shows scheduler_runtime.go +70, scheduler_state.go +54/-, scheduler_tap.go +118/- (64508520: the tap now claims an observation identity under the runtime lock on every delivery). `git diff 738d67c7 closeout/w20-config` shows config.go +6 (struct field) and migration.go. closeout/w20-forkwork changes intent.go +167, draft.go and writer.go. inventory-current.tsv 1.12.17 says 'C52 measured ... the daemon's scheduler_bench_test.go benches (covered code unchanged c5->c6)', and 1.10.17 lists `BenchmarkFinalize/AdvanceSegment/ExtractDecisions`.
  - Fix: Derive the C5.2 set by construction instead of fixing it at three. Parameterise c52exec.py and c52tests.py (today C5,C6 = 0d06ab12,99d0b18c) to d20309c0..<frozen SHA>, run them after the freeze or before launch on the merged integration, and feed every set with an executed changed file into QUIET_PKGS and QUIET_BENCH_FILTER. Log the derived list in chain.log. The minimum change is QUIET_PKGS="checkpoint cli config daemon" with QUIET_BENCH_FILTER='^Benchmark(Finalize|AdvanceSegment|ExtractDecisions|StripInjections|Truncate|HookNoop_InProcess|ConfigLoad_ColdNoFiles|FeaturesFrom|ReclaimableIndexBuild_5000Blocks|AssembleCandidates_2000ToolUses|RuntimeEvaluate_2000ToolUses_32Candidates|SchedulerTap_ObserveTool)$'. Then update the header and README to match.
- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:105-106, 177-206 (linux_lanes), 338`: SP10-D1 (C2.4, row 1.10.17) is disposed 'fixed' on the LINUX Finalize figure. Windows Finalize was already over its 50 ms budget on candidate 5. Candidate 8 changes the files Finalize executes, so D57(e)'s carry breaks for the very number the disposition rests on. The chain re-measures it only on Windows (c52-win). There is no c52-linux step, so tonight's run cannot re-confirm or re-dispose SP10-D1. The audit's own evidence for finding 19 ('SP10-D1's fixed rests on Finalize 46.05 ms against 50') is the Linux number.
  - Evidence: Ledger D54: 'SP10-D1 fixed: Finalize is 46.05 ms on Linux against 50, the platform the budget is stated for; Windows is 56.85 ms, 0.87x the base.' The candidate 5 carry came from phase3/c5/quiet/c52-{win,linux}/paired.txt. quiet.sh supports c52-linux with the same QUIET_PKGS and QUIET_BENCH_FILTER, since c52_plan is shared. overnight-c8.sh queues only c51-win and c52-win, and linux_lanes stops the container before them.
  - Fix: Inside linux_lanes, while the container is up, add `step c52-linux env QUIET_PKGS=... QUIET_BENCH_FILTER=... sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-c52-linux" c52-linux`, using the same derived list as the Windows step. Give it a power record, and AC-gate it or label it NOT-REFERENCE on battery. Otherwise, record in needs_owner that SP10-D1's candidate 8 disposition rests on a code-reading argument, and redo that argument on w20-forkwork's intent.go (+167 lines), not on 738d67c7.
- **major** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:112-121 (RERUN), :146-153 (carried part), :182-187 (audit coverage swap)`: D53(f) is not met for the rows the re-check does not re-run: 'A row that is not re-run gets a carry-forward note naming the files that changed.' Those rows are UAT-01, -03, -04, -10, UAT-09 apart from O-1, UAT-12's upgrade leg, C4.1, C4.4, C4.8, C4.10, C4.11 and C1.7. Neither the generator nor README writes or demands their notes, and the generated audit's coverage list omits all of them, so the audit cannot flag a missing note. For several of these rows, waves 19-20 changed the code the row tests. UAT-04: rehydrate budget.go, build.go, items.go, notice.go and render.go, the overflow and loss-notice path. UAT-03: checkpoint intent, source, writer and draft. C4.4: wave 19c keeps hostperm/patterns.go (+51), and dropped() now redacts per D60(iii). This meets the seat's own open-issue condition ('if wave 19c keeps hostperm changes, a D53(f) carry note or a C4.4 session is owed'). C4.8 and the UAT-12 upgrade leg: state/scheduler.json gains last_applied_observation. UAT-10: eval provenance/ledger ordering (w20-status) and statusorder.
  - Evidence: `git diff --stat a357d187 closeout/w19-rehydrate -- . ':!plans' ':!*_test.go'` shows internal/hostperm/patterns.go | 51 +, plus rehydrate budget/build/items/notice/render/types and daemon/rehydrate_service.go. closeout/w20-redeliver adds scheduler_state.go `LastAppliedObservation ... json:"last_applied_observation,omitempty"`. closeout/w20-status changes eval/ledger.go, provenance.go and livetask.go. The generated audit (sample x8.js) has 'Coverage: for each of C4.2, C4.3, C4.5, C4.6, C4.7, C4.9, C1.6, F-C48-1, UAT-09 O-1 and D53(i)' and 'For every re-run UAT Result block (UAT-02, 05, 06, 07, 08, 11, 12)'. Candidate 4 ran C4.4 in UAT-07's session (sessions.tsv retrieval-c4 5: 'UAT-07 ... + C4.4 eight tools + C4.5 status').
  - Fix: Have mkrecheck8.py compute `git diff --stat d20309c0 <CAND>` over each non-re-run row's packages and add a RERUN instruction: the lane writes the D53(f) carry-forward line (files changed, why the row still holds) into each such row's Result block, which is an allowed edit. Add those rows to the audit's coverage list as 'carried: note present and accurate'. Add C4.4 to the carried part in UAT-07's session, which costs no extra session. Add UAT-04 to the sessions part (1 session, about 590 s on candidate 4), or list it in needs_owner for an explicit carry ruling.
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:130 (C4.5 in the sessions part)`: The checklist row C4.5 reads 'Slash commands run in a real session and match docs/commands.md'. The re-check's C4.5 is CLI-only: `qompack status --json` twice, plus status and doctor --json. w19b-cmdconnect rewrote the command clients that every /qompack: command body calls through Bash. The six commands last ran together in a real session on candidate 3. The audit stage will judge C4.5 against the row's wording.
  - Evidence: V6-CLOSEOUT-CHECKLIST.md:337. closeout/w19b-cmdconnect changes internal/cli/qompack_commands.go | 130 +++-- and doctor.go. sessions.tsv shows the last run of all six commands: 'retrieval 7 C4.5 six /qompack: commands' (the candidate 3 lane). Candidate 7's C4.5 recorded only CLI status and doctor.
  - Fix: In an existing session, such as UAT-07's carried session or UAT-06's fork, run the six /qompack: commands (dropped, eval, pin, recall, status, why). Record the init event's slash_commands and each output against docs/commands.md. It costs no extra session.
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:131 (F-C48-1) and :130 (C4.5)`: w20-status commit 2e4ba732 fixes a second live route to F-C48-1's symptom. With two sessions open in one project, the first-started one compacts or resumes while History.LastSessionID names the other. Its marker counted an absence: one such restart left session_start.fires pending, and two made it SevCritical and degraded a healthy project to degraded-passive. The re-check never opens two concurrent sessions: F-C48-1 runs one session, and UAT-06's compact, resume and fork run one after another. C4.5's reads do not assert session_start.fires either.
  - Evidence: `git show -s 2e4ba732`: 'With two sessions open in one project the first-started one compacts or resumes while LastSessionID names the other, so its own PreCompact or SessionEnd marker counted an absence ... two made it SevCritical and degraded a healthy project to degraded-passive.' The F-C48-1 text describes 'a session with a few captures, a mid-session /compact ...' (1 session).
  - Fix: In F-C48-1's project, start a second session B and keep it open, then run session A's mid-session /compact. Status must read session_start.fires holding, not marker-absent-once or failing, with no degraded-passive. This is +1 session, so the sessions budget becomes 7. At minimum, add a session_start.fires 'holding' assertion to C4.5's reads of the UAT-06 store.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:61, README.md:39-58 (step 2)`: The precondition checks only closeout/w19-rehydrate, closeout/w19b-cmdconnect and the glob patterns closeout/w19c-* and closeout/w20-*. The audit-until-dry rule (D61(a)) has already produced follow-up rounds named like w19b. A next round named closeout/w20b-* or closeout/w21-*, or a fix pushed onto an already-merged wave 19 branch, would not be checked, and the night would freeze without it.
  - Evidence: `git for-each-ref` today lists the wave branches w19-*, w19b-cmdconnect and w20-{config,cover,docs,drain,forkwork,redeliver,sessionend,status}. Wave 19c landed on closeout/w19-rehydrate, not on a w19c-* branch, so the naming is not stable. Line 61 globs only 'refs/heads/closeout/w19c-*' and 'refs/heads/closeout/w20-*'.
  - Fix: Require every closeout/w* branch whose tip is not an ancestor of candidate 7 (d20309c0) to be an ancestor of H, or to be named in C8_EXEMPT. Log each one. Update README step 2 to say the same.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:168-176 (step), 190-192 (Linux lanes)`: D57(d) requires that 'Timing steps record the power source and any transition.' linux-timing and linux-e2e-timing are timing judgements (both were red on candidate 6), and they run through step(). step() logs one start reading only: no t0, no power_events_since, no verdict and no power.tsv row. A red Linux timing row tonight therefore cannot be classified against a power event.
  - Evidence: step() runs `log "step $s_name start power=$(power_read)"`, then the command, then `log ... finished exit=$s_rc`. phase3/c6/chain.log shows 'step linux-timing exit=1' and 'step linux-e2e-timing exit=1'.
  - Fix: Have step() take t0 before power_read, and afterwards record power_events_since and power_verdict into power.tsv and chain.log as record() does. It reports only and does not gate, at least for the *-timing steps.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:39-58 (Before launch)`: The launch procedure is the last checkpoint before c8-night freezes the candidate, and it omits D61(a)'s freeze precondition: 'a candidate is frozen only after a broad parallel audit comes back dry, with every finding verified by two skeptics; wave 19c's areas get their own.' It also never states the latest launch time. The night is about 7.5 h: pre-freeze about 1 h (candidate 6: 53 min), the overnight before release-check about 3.5 h (candidate 6: 3 h 13 min plus win-hotpath and c52), then RC_EST_S of 3 h. A launch after roughly NIGHT_DEADLINE minus 7.5 h silently SKIPs release-check, the local reference run D57(a) requires before the tag.
  - Evidence: Ledger D61(a). phase3/c6/prefreeze/summary.log runs 01:24 to 02:17Z, and phase3/c6/chain.log runs 02:18:52 to 05:32:13Z with no release-check. overnight-c8.sh:268-270 skips release-check when now + RC_EST_S passes the deadline.
  - Fix: Add a step 0: the D61(a) audit of wave 19c and wave 20 is dry, with its ledger row cited. In step 6, state 'launch by about NIGHT_DEADLINE minus 7.5 h, or release-check is SKIPPED (C3.12 and D57(a) unmet)'.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:139-152 (The live re-check)`: The README tells the coordinator to generate and launch the 23-session re-check 'after the night', with no gate on the night's result or on hosted CI. A VALID red, or an unresolved INVALID-POWER, NOT-REFERENCE or SKIPPED step, means candidate 9 may follow. Spending 23 real sessions on a candidate about to be replaced runs against D53(g): 'the owner's session budget is already exceeded'.
  - Evidence: README line 151: 'Launch the script as a Workflow only with the owner's go. It runs 23 real sessions in four parts.' overnight-c8.sh writes overnight-outcome.txt with failed, invalid_power, not_reference and skipped counts, but nothing reads it before the lane.
  - Fix: Add a precondition before generation: overnight-outcome.txt shows failed=0, every invalid, not-reference or skipped step is dispositioned in the ledger (or re-run), and hosted ci.yml and nightly.yml on the frozen SHA are green or each red is classified.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:81-86, 99, 133`: The freeze checks that the frozen tree equals the linted merged tree (verify/v6 + H). But every product suite in pre-freeze (gate, integration, testpkgs, internal, e2efunc) ran on integration's tree H. Those verdicts stand for the frozen tree only while verify/v6 contributes nothing outside plans/, and the script never checks this. Today it holds only by coincidence: merge-base d20309c0..verify/v6 changes 19 paths, all under plans/.
  - Evidence: `git diff --name-only $(git merge-base HEAD 738d67c7) HEAD | grep -v '^plans/'` prints nothing today. c8-night runs the Go suites on "$INT" (line 99) and only lint, guards and docs on the merged tree (line 83).
  - Fix: After merged_tree, add `git -C "$M/repo" diff --name-only "$H" HEAD | grep -v '^plans/' | grep -q . && stop "verify/v6 adds non-plan paths the pre-freeze check did not test"`.

## fix:night:r1: status `done`, head `df873aa9737f3960936e65946885d8df2c75a96a`

### Summary

No product code changed. Every change is coordinator tooling under plans/sdd/V6-closeout/coordinator/ on verify/v6. Round 0 made seven commits (067fab8f..c2b03a54); this round adds five on top (bc54677f, 2c06e1f6, 2f1d30fe, c3217dcc, df873aa9). Each has a "Refs: V6-VERIFY, C3.1" footer and no attribution trailer. Nothing was run for real: no chain, freeze, bundle build, tag, suite, Docker or Claude Code session.

**Review resolution (round 1)**
I re-checked all 16 reviewer findings, both nits and the reviewer's unraised X6 probe against c2b03a54's scripts. All hold. All are fixed, each behind a harness case that fails on c2b03a54 for the matching defect.

Red evidence:
- 20 new or changed cases, run against c2b03a54's scripts: 20 of 20 failed, each on its defect's assertion. They are U5, K1, O1, O3, O13-O21 and N13-N19.
- O22 and O23 fail against the derivation script without its two new guards.
- N20 fails against 2f1d30fe with "Filename too long" in the merged-tree clone.

Green: all 60 harness cases pass on df873aa9 (3365 s).

**Fixed beyond the review**
- Release-check is retried only on AC, never at once on battery (the X6 probe).
- Both scratch clones set core.longpaths. The longest tracked path is 181 characters, so a mktemp clone sits only about 24 characters under Windows' 260-character path limit. My own long scratch path hit that limit.
- c52derive exits 2 instead of selecting every row when every trace fails. Selecting all ~60 rows would add about 3.5 h per OS to the night.
- c52derive also refuses a stray untracked or ignored .go file, which -coverpkg would compile into every trace.

**The C5.2 derivation (c52derive.py, new)**
For each listed benchmark it runs one traced iteration (-benchtime=1x with a set-mode coverage profile). It selects a row when an executed product file changed in code since candidate 7. It also selects a row when the benchmark's own _test.go file or its testdata/ changed, or when a non-Go file beside its executed code changed.

Checked against the real toolchain on a scratch export of 69860c1d:
- BenchmarkHistogram_Observe executes 8 files, internal/obs/hist.go and registry.go among them, the same as w17-inventory's accepted c52exec proof. It took 9 s.
- BenchmarkFinalize executes 117 files, internal/checkpoint/intent.go among them. It took 37 s.

**Other checks**
- On the merged tree of verify/v6 df873aa9 and integration 738d67c7, built in a no-push scratch clone: runpatterns, docmarkers and coveragefloors PASS; test/guards ok (71.3 s); test/docs ok (6.9 s). verify/v6 adds no path outside plans/.
- fmt-check: exit 0.
- `python c52derive.py --selftest`: PASS.
- mkrecheck8 refuses 738d67c7 (ADR 0011 at that commit has no section 23) and generates from 69860c1d. The generated script parses under node, holds no Date.now or Math.random, keeps no candidate 7 string, and carries budgets 8/5/8/4 = 25.

**Machine hygiene**
I stopped one of my own harness runs early, and its sh.exe and overnight-c8 children survived. I found them by command line, stopped exactly that tree and removed its scratch directory. No harness process remains.

**Open items carried forward**
- From round 0: ledger and inventory corrections, and C4.4's carry (now resolved by re-running C4.4).
- New: the three wave 15 branch tips, and the new numbers listed below.

### Commits

- 067fab8f chore(v6): retry invalid-power night steps on their own records
- 86193037 chore(v6): fail phase3's multi-command arms on any failure
- bdc15929 chore(v6): gate candidate 8's freeze on this run's evidence
- 836bc3d2 chore(v6): widen candidate 8's live re-check
- ccea07ab docs(v6): record candidate 8's launch and abort procedure
- 51f34451 chore(v6): add a dry harness for candidate 8's night scripts
- c2b03a54 docs(v6): tighten candidate 8's abort and re-run steps
- bc54677f chore(v6): widen candidate 8's re-check to carried rows
- 2c06e1f6 chore(v6): close round 1's night findings with harness cases
- 2f1d30fe docs(v6): update candidate 8's launch, abort and re-check steps
- c3217dcc chore(v6): set core.longpaths in the night's scratch clones
- df873aa9 docs(v6): describe keepawake's and c52derive's refusals

### Findings resolution

- **fixed**: R0-0 BLOCKER timed() retry was a no-op (recrun refuses to overwrite)
  - Round 0 (067fab8f): each try's new records move to <step>.invalid-power-<n>/. Still green: O2, O6, O10.
- **fixed**: R0-1 acwait timeout ignored, t0 after the power check, no deadline
  - Round 0. Round 1 adds: the end reading, Kernel-Power 42 and 107, and c8-night's deadline passed as an epoch. Green: O3, O4, O5, U3, U5, N14.
- **fixed**: R0-2 release-check judged over one 3-hour window
  - Round 0: only the AC-sensitive windows are judged. Round 1: no window is VALID only when the run failed before ci-local test; a retry needs AC and gets a fresh clone. Green: O6, O7, O14, O20, O21.
- **fixed**: R0-3 prefreeze summary appended; stale pass lines accepted
  - Round 0. Green: N5, F4.
- **fixed**: R0-4 v0.3.0 tag created in the shared ref store
  - Round 0: the tag lives in an isolated clone, which now also sets core.longpaths (c3217dcc). Green: O1, O9, N20.
- **fixed**: R0-5 phase3 arms masked failures
  - Round 0. Green: P1-P7.
- **fixed**: R0-6 prefreeze -p 2 passes without the co-load declaration or a power record
  - Round 0. Round 1 adds the end reading to each step's power verdict. Green: N1, N10, N11, F4.
- **fixed**: R0-7/R0-12 integration hot-path rows not run alone
  - Round 0. Green: F3, P7, N1.
- **fixed**: R0-8 power() parsing unsafe; set -u crash
  - Round 0. Green: U1, U2, O8.
- **fixed**: R0-9 no time bounds on docker, push and gh
  - Round 0. Round 1 adds engine ownership (see R1-5). Green: O11, N8, O15.
- **fixed**: R0-10 host-validate 'unverified' accepted
  - Round 0. Green: N7.
- **fixed**: R0-11 c8-night preconditions
  - Round 0. Round 1 generalizes the branch check to every closeout/w* tip and moves every check before the pre-freeze. Green: N2-N4, N12, N15-N18.
- **fixed**: R0-13 gate lacked gen-command-docs, licenses and govulncheck
  - Round 0. Green: F1, F2, N1.
- **fixed**: R0-14 no keep-awake EXIT trap; overnight always exit 0
  - Round 0. Round 1 closes the early-refusal race (see R1-1). Green: N9, N13, K1.
- **fixed**: R0-15 re-check budgets had no margin
  - Now sessions 8, retrieval 5, carried 8, resilience 4 = 25. The generator asserts these budgets.
- **fixed**: R0-16 UAT-12 lacked the 40-pointer leg and the , and = names
  - Round 0. Unchanged this round.
- **fixed**: R0-17 ADR §23 and closeout/live8 dependencies unchecked
  - Round 0. Re-verified: the generator refuses 738d67c7 and generates from 69860c1d.
- **partial**: R0-18 C1.6 marked fixed on a false citation
  - The re-check has a C1.6 leg. The ledger's D59 sentence and the C1.6 cell at V6-CLOSEOUT-CHECKLIST.md:240 need the coordinator; the ledger is read-only to me.
- **partial**: R0-19 D57(e) C5.2 carry broken, no c52 step
  - Fixed by construction this round (see R1-7 and R1-8). Still open and not mine: inventory-current.tsv needs a candidate 8 disposition for 1.10.17, 1.16.11, 1.1.27 and 1.12.17 from tonight's c52-derive, c52-win and c52-linux.
- **partial**: R0-20 rows carried from candidates 3 and 4 without a D53(f) note
  - Fixed in the generator this round (see R1-9). Still open and not mine: D59's 'the other rows carry from candidate 7 by the diff' in the ledger.
- **fixed**: R0-nit doc comments, release-check record, stale cp
  - Round 0. Headers rewritten again this round.
- **fixed**: R1-1 (major) early refusal can leave keep-awake held with no time limit
  - keepawake.ps1 never creates its sentinel: a sentinel missing at start, or after Add-Type, means it exits without holding. c8-night logs the keep-awake pid. Its EXIT trap kills that process, but only while it is still the shell's running job, so a recycled pid is never hit. MSYS kill was verified to terminate a native PowerShell child. Red on c2b03a54: N13 ('the keep-awake process is logged') and K1 (real pwsh: the sentinel was created and the process held). Green: N13, K1, N9.
- **fixed**: R1-2 (minor) deadline rolls over 24 h between the two scripts
  - c8-night exports NIGHT_DEADLINE_EPOCH, and overnight uses it without recomputing; past it, every step is SKIPPED, the container is never started and the outcome file is still written. Standalone, a deadline more than NIGHT_MAX_AHEAD_H=16 h away refuses unless NIGHT_ALLOW_FAR=1; c8-night applies the same guard. Red: N14, O19, N19. Green: N14, O19, N19.
- **fixed**: R1-3 (minor) release-check power verdict failed open
  - With no window found, the verdict is VALID only when rc != 0, the version-agreement header is present and ci-local test's header is absent. Otherwise it is NOT-REFERENCE 'AC-sensitive windows not found in the log'. Red: O14(1). Green: O14 parts 1 and 2.
- **fixed**: R1-4 (minor) re-run into a populated evidence dir mislabels refusals as VALID
  - overnight-c8 refuses (exit 2, before writing anything) a directory holding chain.log, power.tsv, overnight-outcome.txt, p3-*, quiet*, c52-derive*, release-check* or *.invalid-power-*. c8-night refuses phase3/c8 with an earlier overnight's records before anything runs. Red: O13, N15. Green: O12 (repurposed), O13, N15.
- **fixed**: R1-5 (minor) docker ownership decided from one probe at lane time
  - engine_up_at_start is recorded from the night's first probe. docker_answers retries three probes 30 s apart. An engine that was up at the start is never stopped, even if the chain asked Desktop to start it. Red: O15. Green: O15, O11, O1.
- **fixed**: R1-6 (minor) bundle dir and candidate cleanliness checked only after the freeze
  - Both are checked in step 2, and again after the pre-freeze. Red: N16 (both legs). Green: N16.
- **fixed**: R1-7 (major) C5.2 set hard-coded at three benchmarks
  - New c52derive.py: a per-benchmark coverage trace on the frozen candidate against C8_PREV_CANDIDATE (d20309c0), selecting rows whose executed files changed. The chain logs the derived set and writes c52-derive/report.txt and selection.tsv. A whole-derivation failure measures the reviewer's 12-row floor (checkpoint, cli, config and daemon) and fails the step. A partially failed trace is selected, fail-closed. Red: O1, O16, O17, O22, O23. Green: all five. Validated on the real toolchain; the obs result matches the c52exec proof.
- **fixed**: R1-8 (major) no c52-linux; SP10-D1 rests on Linux Finalize
  - c52-linux is a queued AC-gated step: it brings the container up and down itself, measures the same derived set into quiet-c52-linux, keeps per-try records and is NOT-REFERENCE on battery. Red: O1, O3. Green: O1, O3.
- **fixed**: R1-9 (major) D53(f) carry notes missing for rows not re-run
  - mkrecheck8 embeds `git diff --stat` from d20309c0 to the candidate, outside plans and tests. The resilience part writes a carry note for UAT-01, -03, UAT-05 run 1, UAT-09 apart from O-1, UAT-10, UAT-12's upgrade leg, C4.1, C4.8, C4.9's other legs, C4.10, C4.11 and C1.7, naming the changed files each row exercises and why it holds; otherwise it records a finding. The audit's coverage list marks each note present and accurate. C4.4 re-runs in UAT-07's session. UAT-04 re-runs (+1 session).
- **fixed**: R1-10 (minor) C4.5 was CLI-only
  - The six /qompack: commands now run in the UAT-06 fork session, compared field by field with docs/commands.md and the init event's slash_commands. No extra session.
- **fixed**: R1-11 (minor) F-C48-1's second route (2e4ba732) not exercised
  - After the fork, the parent session is resumed and compacted (+1 session): a resume start and a compact start of the first-started session while the store's last session is the fork. session_start.fires must read holding with no degraded-passive, and C4.5's reads assert the same.
- **fixed**: R1-12 (minor) branch precondition glob too narrow
  - Every closeout/w* tip that is not an ancestor of candidate 7 must be merged into integration or named in C8_EXEMPT; the two named branches must exist. Red: N17 (closeout/w21-late). Green: N17, N2-N4. On the real repo this flags closeout/w15-docs, w15-ledger and w15-services (see needs_owner).
- **fixed**: R1-13 (minor) Linux timing steps had no power record
  - step() takes t0 before its power read and records the events, the end reading and a verdict in power.tsv and chain.log. A *-timing lane counts as pass or fail only when VALID; INVALID-POWER counts as invalid and NOT-REFERENCE as not reference. Red: O18. Green: O18, O3.
- **fixed**: R1-14 (minor) README lacked D61(a) step 0 and a launch-by time
  - Step 0: the dry audit, cited by its ledger row, is required. Step 6: launch by about NIGHT_DEADLINE minus 9.5 h (22:30 for 08:00), with the derivation of that figure, or release-check is SKIPPED.
- **fixed**: R1-15 (minor) re-check not gated on the night or on hosted CI
  - The README and the generated header both require: failed=0; every invalid, not-reference or skipped step re-run or dispositioned; and hosted ci.yml and nightly.yml green or classified.
- **fixed**: R1-16 (minor) verify/v6 non-plan paths not checked
  - After the merge, `git diff --name-only H HEAD` outside plans/ refuses the night and names the paths. Red: N18. Green: N18.
- **fixed**: R1-nit1 end reading ignored; Kernel-Power 42 and 107 not watched
  - power_verdict takes the end reading: a reading that is not AC, with no event to explain it, is NOT-REFERENCE. Events 42 and 107 read as SLEEP and RESUME and count as INVALID-POWER. The query was run read-only against the real System log: 4 SLEEP and 4 RESUME in 7 days. Red: U5. Green: U5.
- **fixed**: R1-nit2 release-check try 2 reused try 1's clone
  - Before try 2 the clone is removed and rc_prepare runs again. Red: O20 (try 2's source_dirty showed the file try 1 left). Green: O20.
- **fixed**: R1-X6 (probe, not raised) release-check retried immediately on battery
  - A retry first waits for AC within the night's budget; if none comes, the note says 'not retried: no AC for a second run'. Red: O21, O3. Green: O21, O3.

### Tests

- `cd plans/sdd/V6-closeout/coordinator && sh nightharness.sh   (all 60 cases, head df873aa9)`: 60 case(s), 0 failed; wall 3365 s
- `sh nightharness.sh U5_sleep_resume_and_the_end_reading K1_keepawake_never_creates_its_sentinel O1_all_ac O3_battery_night_is_never_valid O13_rerun_into_same_dir_refused O14_rc_windows_missing_is_not_valid O15_owner_engine_never_stopped O16_c52_derivation_failure_falls_back_to_floor O17_c52_trace_failure_is_selected O18_linux_steps_record_power O19_standalone_far_deadline_refused O20_rc_retry_uses_a_fresh_clone O21_rc_not_retried_on_battery N13_early_refusal_stops_keepawake N14_deadline_epoch_passes_to_overnight N15_earlier_overnight_records_refuse_before_freeze N16_bundle_dir_and_dirty_candidate_refuse_before_freeze N17_any_unmerged_wave_branch_refuses N18_verify_v6_product_path_refuses N19_far_deadline_refuses   (new harness against c2b03a54's scripts, in a scratch copy)`: 20 case(s), 20 failed (expected red), each on its defect's assertion
- `sh nightharness.sh O22_c52_every_trace_failing_falls_back_to_floor O23_c52_stray_go_file_refused   (against c52derive.py with its two guards removed)`: 2 case(s), 2 failed (expected red)
- `sh nightharness.sh N20_long_paths_in_the_scratch_clones   (against 2f1d30fe's scripts, then the fix)`: red: 'REFUSED: the merged tree ... could not be made' with 'unable to create file ... Filename too long'; green after c3217dcc
- `python c52derive.py --selftest`: selftest: PASS
- `c52derive.trace() with the real go on a scratch git-archive of 69860c1d: obs BenchmarkHistogram_Observe and checkpoint BenchmarkFinalize`: obs: 8 files executed (hist.go and registry.go, the same as the c52exec proof), 9 s; Finalize: 117 files incl. internal/checkpoint/intent.go, 37 s
- `power.sh power_events_since against the real System log (read-only, 7 days)`: AC=0 x30, AC=1 x29, STANDBY x72, SLEEP x4, RESUME x4, plus the END sentinel
- `python mkrecheck8.py live-rerun-c7.js <scratch>/recheck8-sample.js 738d67c7... <c7 bundle>, then with 69860c1d...`: refused 738d67c7 (no ADR 0011 section 23); generated from 69860c1d: node parse OK, no Date.now or Math.random, no candidate 7 strings, budgets [8,5,8,4] = 25, CARRY diff embedded (26 files changed)
- `sh -n power.sh stamped.sh overnight-c8.sh c8-night.sh prefreeze.sh phase3.sh nightharness.sh; python -m py_compile mkrecheck8.py c52derive.py`: all parse
- `go run ./tools/devtool fmt-check   (qompack-v6)`: exit 0
- `scratch no-push clone with core.longpaths: verify/v6 df873aa9 merged with closeout/integration 738d67c7 (tree fe503787): go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors && go test -p 1 -count=1 ./test/guards ./test/docs`: PASS runpatterns, PASS docmarkers, PASS coveragefloors; ok test/guards 71.276s; ok test/docs 6.854s; no non-plan path from verify/v6
- `go vet (windows/linux/darwin), golangci-lint, -count=20, -race -count=3, gen-*-docs --check`: not applicable: no Go package, Go test or generated input was touched

### Criterion changes

- Power verdict: VALID now also needs an AC end reading. An end reading that is not AC, with no event to explain it, is NOT-REFERENCE. Kernel-Power 42 (sleep) and 107 (resume) count as INVALID-POWER, like 105 and 506. Stricter.
- Every overnight step now has a power record. A Linux *-timing lane (a wall-clock judgement) counts as pass or fail only when VALID; INVALID-POWER counts as invalid and NOT-REFERENCE as not reference. Other AC-independent steps still count by exit status. Reason: D57(d), a battery run is neither a pass nor a fail.
- release-check with no AC-sensitive window found: VALID only when it failed before ci-local test began; otherwise NOT-REFERENCE. Before, it was VALID. Stricter.
- release-check's retry needs AC within the night's budget and runs in a fresh clone. Before, it retried at once in the same clone, even on battery.
- C5.2 on candidate 8 measures the set c52derive.py derives: rows whose executed product files, own benchmark file, fixtures or adjacent assets changed since d20309c0. It is measured on Windows and Linux, both AC-gated. Before, three fixed rows ran on Windows only. A failed trace is selected; a whole-derivation failure measures a static floor and fails the step.
- c8-night: every closeout/w* tip not already in candidate 7 must be merged or exempted, not only the w19c-* and w20-* globs. verify/v6 may add nothing outside plans/. Every precondition is checked before the pre-freeze. A deadline more than 16 h away refuses. Stricter.
- overnight-c8 refuses an evidence directory that already holds a night's records, and when run alone it refuses a deadline more than 16 h away. Stricter.
- Docker Desktop is stopped only when its engine was down at the night's start and this chain started it. Before, a single failed probe at lane time could make the chain stop the owner's engine.
- Re-check: UAT-04 and C4.4 re-run. C4.5 includes the six slash commands in a real session. A parent restart behind its fork must leave session_start.fires holding. Every row not re-run needs a D53(f) carry note checked against the embedded diff. Generation needs a clean night and classified hosted CI. Stricter.

### Open issues

- Three old branches hold commits that neither candidate 7 nor integration has: closeout/w15-docs 21c07942, closeout/w15-ledger 12817cd0 (fix(checkpoint) x2, fix(negknow)) and closeout/w15-services c212712d (fix(store), fix(fault)). Under the generalized precondition c8-night refuses until each is merged or named in C8_EXEMPT. Only w15-docs' 2ccc5ffe has an integration equivalent (91cf3d42). Whether the other fixes were superseded is unverified.
- The live re-check can be generated only once wave 19c's ADR 0011 section 23 is in the candidate. If §23 is renumbered or retitled, mkrecheck8.py refuses, and its check and part text must change.
- In the qompack-v6 worktree itself `devtool lint --only=runpatterns` is falsely red: the git-ignored dist/v6-remediation/ holds stray .go packages that are not mine. c8-night lints a scratch clone, so the night is unaffected. c52derive refuses such strays in the candidate (O23); qompack-cx-cand's dist/ holds binaries only today.
- The night is longer now: about 9.5 h with c52-derive (estimated 15-30 min) and quiet C5.2 on both OSes (about 2 h for today's derived set). A launch later than about NIGHT_DEADLINE minus 9.5 h SKIPs release-check. If the derived set reaches store or observer-200x, add about 1 h per OS.
- The harness's real-process boundary: K1 runs the real pwsh once, with no sentinel. Real PowerShell power queries, Docker, git push, gh and claude are not exercised. The event query was checked read-only against the real log, and c52derive's trace against the real toolchain on two benchmarks only.
- keepawake.ps1 still holds ES_SYSTEM_REQUIRED only. Modern Standby (506) events in the real log now invalidate a gated step; if tonight shows STANDBY invalidations, ES_DISPLAY_REQUIRED may be needed. Unverified, carried from round 0.
- Inventory dispositions for the C5.2 rows (1.10.17, 1.16.11, 1.1.27, 1.12.17 and any other row c52-derive selects) follow from tonight's c52-derive, c52-win and c52-linux results; inventory-current.tsv is not mine.

### Needs owner

- NIGHT_MAX_AHEAD_H = 16 h (new): both scripts refuse a deadline further away unless NIGHT_ALLOW_FAR=1. Derivation: a night is about 9.5 h plus up to 3 h of AC waiting, so 16 h admits an evening launch (from 16:00 for an 08:00 deadline) and refuses any launch made after the deadline hour, which the next-HH:MM rule would carry into the next day.
- C5.2 static floor (new, used only when c52derive.py cannot run at all): QUIET_PKGS 'checkpoint cli config daemon' with the 12 benchmarks the reviewer listed. Derivation: the 2026-10-03 diff from d20309c0 to integration and every w20 tip changes files in those packages' executed sets.
- docker_answers: 3 probes, 30 s apart (new), before an unanswered engine is treated as down. Derivation: a probe is bounded at 60 s, so a transient failure (WSL hiccup, restart) costs at most about 4 min, and one failure no longer starts or stops anything.
- c52derive's per-trace -timeout 10m is go test's own default, made explicit; not a new number.
- The live re-check now plans 25 real sessions (sessions 8, retrieval 5, carried 8, resilience 4). +2 over round 0: UAT-04, and the parent restart behind its fork. C5.5's 40 trials make 65 against D3's ~40-80; D53(g) notes the owner's session budget is already exceeded. The alternative is an explicit carry ruling for UAT-04.
- Decide closeout/w15-docs, closeout/w15-ledger and closeout/w15-services: merge them into integration or name them in C8_EXEMPT, and record which in the ledger. c8-night refuses until then.
- Ratify round 1's criterion changes, listed in criterion_changes below.
- Ledger and inventory edits, carried from round 0, read-only to me: D59's C1.6 sentence and the C1.6 cell (V6-CLOSEOUT-CHECKLIST.md:240); D59's 'the other rows carry from candidate 7 by the diff'; the inventory's candidate 8 C5.2 dispositions from tonight's derived set.
- Carried from round 0: NIGHT_DEADLINE 08:00; RC_EST_S 10800 s; AC_WAIT_BUDGET_MIN 180 as one night-wide budget; the docker, push and gh time limits.

## review:night:0:r2: verdict `needs-fixes`, 6 finding(s)

- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:54-59 (argument handling); README.md:158-172 (Abort step 7, the standalone re-run)`: When overnight-c8.sh runs alone, it never checks that <candidate-repo> is checked out at <candidate-sha>, or that the checkout is clean. The README's Abort step 7 runs it exactly this way, with a hand-typed '<sha>'. phase3, quiet and c52derive all run on whatever HEAD qompack-cx-cand holds, while release-check clones H. Every verdict is still logged as candidate H. Worse, if the checkout sits at candidate 7, c52derive compares candidate 7 with itself and selects nothing. The chain then logs that every candidate 5 C5.2 row carries (D57(e)) and skips c52-win and c52-linux. That is a clean result on the wrong tree. c8-night.sh is not exposed, because it checks out CAND at SHA itself.
  - Evidence: Probe X1, from the committed harness plus my case: qompack-cx-cand was detached at main (candidate 7, e0148b8f) and H was the frozen 2cd78545. overnight exited 0 with 'steps=13 passed=13 failed=0 ... skipped=0'. c52-derive/report.txt read 'candidate e0148b8f... against base e0148b8f...: 0 product .go file(s) [changed]', and c52-win and c52-linux never ran. Probe X2: a tracked change in qompack-cx-cand. Only c52-derive refused (failed=1 [c52-derive]); every phase3 step and the quiet steps ran on the dirty tree. Code: lines 54-59 parse C, H and E and go straight on. The only use of $C's state is `git -C "$C" rev-parse -q --verify refs/tags/v0.3.0`.
  - Fix: Right after the argument check:
- `case $H in *[!0-9a-f]*|'') refuse ... ;; esac; [ ${#H} -eq 40 ] || refuse "<candidate-sha> must be the full 40-character SHA"`
- `[ "$(git -C "$C" rev-parse HEAD)" = "$H" ] || refuse "$C is at $(git -C "$C" rev-parse HEAD), not the candidate $H"`
- `[ -z "$(git -C "$C" status --porcelain)" ] || refuse "$C is not clean"`

Add harness cases for a wrong checkout and a dirty checkout. Have README step 7 say that qompack-cx-cand must be detached at <sha>.
- **minor** `plans/sdd/V6-closeout/coordinator/c52derive.py:147 (-benchtime=1x) and :92-118 (classify, file-level 'executed' only)`: The C5.2 derivation is called 'D57(e) by construction', but it fails open in two ways.
- It traces each benchmark with b.N=1. Code a benchmark reaches only after its first iteration is invisible: periodic rotation, compaction, cache eviction once full.
- A changed file holding only declarations (const, type or var) has no coverage blocks, so it is never 'executed', even when the benchmark depends on it.
An empty selection makes overnight-c8.sh log that every candidate 5 C5.2 row carries, and skip c52-win and c52-linux. c52exec.py, the accepted method, carried the same blind spot but said so ('a type or struct change has no executable line'). c52derive's docstring does not.
  - Evidence: Real toolchain, a scratch module (python c52derive.py <repo> HEAD~1 <out> <list>). BenchmarkWork calls rotate() every 100th iteration and allocates make([]byte, Size). The candidate changes rotate.go (i*2 to i*3) and consts.go (Size 64 to 65536). Result: exit 0, 'product files executed: 1', 'verdict: carry holds', 'selected: 0', and filter.txt empty. The same script with -benchtime=1s (the row's listed benchtime) gives 'SELECTED: executes changed internal/p/rotate.go', but still misses consts.go. In today's diff, internal/rehydrate/types.go is declaration-only (Deps gains `judge *pathJudge`). No listed row is known to depend on it alone, so tonight's diff shows no proven miss.
  - Fix: Trace each row at its own benchtime from quiet.sh's list (third column: 61 rows at 1s, two at 10x, two at 200x) instead of 1x. Also select a row when any changed non-test .go file sits in a package directory holding one of its executed files: declaration-only changes there are not visible to coverage. Document both rules in the docstring, and add a selftest case for a declaration-only change.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:131-134 (Abort step 1 PowerShell)`: The abort snippet builds the process tree from ParentProcessId alone, with no check of creation times or of the root's command line. Windows never updates ParentProcessId when a parent exits. So any process whose dead parent's pid has been recycled into the night's tree is pulled in and killed by Stop-Process -Force. The root winpid comes from night.log; if the night has already ended, that pid itself may now belong to something else. The owner's machine is shared, and D56(g) forbids stopping the owner's Docker engine.
  - Evidence: Read-only Get-CimInstance Win32_Process query on this host now:
- 460 processes; 59 have a parent that no longer exists.
- Those 59 include com.docker.backend.exe, explorer.exe, Discord.exe, Spotify.exe, OneDrive.exe and WebexHost.exe.
- 4 more have a ParentProcessId that already names a newer process (csrss.exe and wininit.exe under svchost pid 1552).
If any of those 59 stale parent ids matches a pid in the night's live tree at abort time, the snippet stops, for example, com.docker.backend.exe, which is the owner's engine.
  - Fix: Check the root first: its Name must be bash.exe and its CommandLine must contain c8-night.sh (or overnight-c8.sh for a re-run); otherwise stop nothing. While walking the tree, add a child only when `$_.ParentProcessId -eq $p.ProcessId -and $_.CreationDate -ge $p.CreationDate`. List the tree with names and command lines before Stop-Process.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:388-398 (rc_retry_blocker) and :433`: The release-check retry is sized by try 1's duration. release-check stops at its first FAIL, so a try 1 that went red in ci-local test with a power event in its e2e window lasted only part of a full run. Try 2 is then admitted on that short estimate. If try 2 goes green, it runs the full ~3 h and can finish up to about 1.5 h after NIGHT_DEADLINE, in the owner's day. A power-caused e2e red is exactly the case the retry exists for.
  - Evidence: Probe X5: try 1 failed at 690 s with a BAT/AC flip inside the ci-local test e2e window: 'step release-check try 1 exit=1 INVALID-POWER ci-local-test:events[...] ... retried once in a fresh clone'. The retry was admitted on 690 s, and try 2 ran 03:16:30 to 05:23:13 (fake clock). It fit only because the deadline in that case was 06:00. With a 04:30 deadline it would still be admitted and end around 05:23.
  - Fix: Size the retry as max(try-1 seconds, RC_EST_S) whenever try 1 exited non-zero, or always use RC_EST_S. Add a harness case with a tight deadline.
- **nit** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:377 (rc_verdict 'on-battery')`: A release-check window that ran wholly on battery is labelled INVALID-POWER ('<sec>:on-battery') and counted under invalid_power. The script's own header (lines 13-16) and the README define a battery run as NOT-REFERENCE, and INVALID-POWER as an event during the run. Pass and fail are unaffected, since both labels are neither, but the outcome counts and power.tsv disagree with the documented taxonomy.
  - Evidence: Committed cases O3 and O21 assert 'INVALID-POWER*on-battery*'. The header says NOT-REFERENCE means 'ran on battery'.
  - Fix: Emit 'NOT-REFERENCE <sec>:on-battery' for a battery window and keep the AC-gated retry for it, or amend the header and README to say that a battery window is INVALID-POWER and retried.
- **nit** `plans/sdd/V6-closeout/coordinator/phase3.sh:109 (linux-child, unchanged from base)`: The local linux-child lane runs the eight product-child e2e rows under GOFLAGS=-race without --coload. nightly.yml's race-product-child job, the lane it mirrors, sets QOMPACK_UNDER_COLOAD='1' for the same rows. Locally, wall-clock judgements inside those rows (for example TestE2EHookRoundTrip) are therefore judged under race instrumentation, and step() counts the lane by exit status on any power source. The error can only be a false red, never a false green.
  - Evidence: nightly.yml:115-124 sets env QOMPACK_UNDER_COLOAD: '1' on race-product-child. phase3.sh:109 passes --no-race --env CGO_ENABLED=1 --env GOFLAGS=-race --env QOMPACK_REQUIRE_CHILD_RACE=1 and no --coload. The linux-tree and linux-e2e arms do pass --coload.
  - Fix: Add --coload to the linux-child arm, matching nightly.yml.

## review:night:1:r2: verdict `needs-fixes`, 4 finding(s)

- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:475-482 (queue order: c52-win/c52-linux run before release_check), :205-219 (run_gated checks the deadline only before a try), c52derive.py:92-101 (selection by executed file), README.md:73-81 (night sizing and 22:30 launch time)`: The night's length is undersized by about 4-5 hours, so release-check (C3.12, and D57(a)'s local reference run before the tag) will almost certainly be SKIPPED, and C5.2 will run into the owner's day. The cause is that c8-night requires every closeout/w20-* tip to be merged, and w20-config changes internal/config/config.go in code (a new Warning.VersionedReset field). config.go runs at package init (config.go:343 `var globalSchema = buildSchemaIndex()`), so every benchmark binary that links internal/config executes config.go. c52derive selects any benchmark that executes a code-changed file (D57(e) is file-level, so the selection itself is right), and so it will select about 55 of quiet.sh's 65 rows: the whole store family and the observer 200x rows included. The seat's own open issue already says that reaching store or observer-200x adds about 1 h per OS, but that outcome is close to certain, not a tail risk. Its 2 h estimate rested on the static floor, and its trace check ran on 69860c1d, which lacks w20-config. The order makes this worse. On AC, run_queue runs c51-win, c52-win and c52-linux before release_check, so the 3-hour release-check fails its RC_EST_S check and is SKIPPED. And because a gated step is bounded only at its start, c52-linux can start just before 08:00 and run about 3 h into the day.
  - Evidence: Probe on a git-archive copy of 738d67c7: `go test -p 1 -run '^$' -bench '^BenchmarkHistogram_Observe$' -benchtime=1x -covermode=set -coverpkg=github.com/qompack/qompack/... ./internal/obs`. Its executed files are internal/config/{config,deadlines,defaults,load,validate}.go, core/clock.go and obs/{hist,registry}.go. These are the '8 files' the seat cited as matching the c52exec proof, and config.go is among them. `git diff d20309c0 closeout/w20-config -- internal/config/config.go` adds `VersionedReset bool`, which is code under diff_is_code. `go list -deps` on store, observer, negknow, daemon, checkpoint, cli, config, canon, chunk, obs, eval, dag and scheduler includes internal/config; paths, symbols, sketch, rules and skills do not. hostperm is selected anyway through patterns.go (+51 on w19-rehydrate against d20309c0). On candidate 5, the full list took 3h29m for c52-win (03:22:32 to 06:51:09Z) and 3h03m for c52-linux (06:53 to 09:56Z), per phase3/c5/quiet/quiet-run.txt. A timeline for a 22:30 launch: pre-freeze ends about 23:30, the chain reaches the end of the Linux lanes about 03:15, c51-win runs to about 03:20 and c52-win to about 06:20. c52-linux then starts before the deadline and ends about 09:20, and release_check finds now+10800 s past 08:00 and records SKIPPED.
  - Fix: (1) Run release_check before the C5.2 gated steps. C3.12 and D57(a) are a release gate; C5.2 is a measurement that a later quiet night can repeat. (2) Give c52-win and c52-linux an end-by-deadline estimate, as release-check has RC_EST_S. Size it per selected package from candidate 5's per-round times (or use a per-row constant). SKIP a step that cannot finish by the deadline, and log its derived selection so a later night can measure exactly that set. (3) Derive the set before the freeze: run c52derive.py in c8-night's merged scratch clone after the lint, or as a README pre-launch step on the merged tree. The coordinator then knows the set and the night's real length at launch, and README step 6's '9.5 h / 22:30' becomes a computed figure. (4) Put one question to the owner, following the toolnames.go precedent: does a changed file with no executed statement on a changed line keep D57(e)'s carry? Examples are a struct field, or code reached only through config's init-time schema walk. c52derive could report c52exec.py's 'executed blocks over changed lines' per row without changing its fail-closed selection.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:475 (only c51-win is queued; compare overnight-c6.sh:34, which ran c51-win and c51-linux)`: No Linux C5.1 run happens on candidate 8. C5.1 reads 'on Windows and Linux'. Inventory row 1.10.16 is verified_in_target on 'quiet B-E passes on both OSes (C51: ... Linux 384.11 ms ...)', 1.17.6 cites Linux B-E and 1.17.5 cites Linux B-D. Those Linux figures are candidate 6's. Candidate 8 changes code that B-E (PreCompact) executes: checkpoint intent/source/writer/draft (w20-forkwork) and the drain that PreCompact replays through (passprogress, w20-drain). So the Linux halves of those rows have no candidate 8 evidence and no carry note. The container is already started for c52-linux.
  - Evidence: inventory-current.tsv rows 1.10.16, 1.17.5 and 1.17.6 (c6_evidence cells). phase3/c6/chain.log shows c51-linux took about 2 min (05:29:56 to 05:32:08Z) and exited 1, because the container's B-A/B-B breach and D53(b) leaves those two rows not verified in target. `git diff --stat $(git merge-base 738d67c7 closeout/w20-forkwork) closeout/w20-forkwork` shows changes to internal/checkpoint/intent.go (+168), draft.go, source.go and writer.go. closeout/w20-drain changes drain.go (+568).
  - Fix: Inside c52-linux's container window, run `quiet.sh <cand> cf31e01 <E>/quiet-c51-linux c51-linux` as a report-only step: its power is recorded, and its exit status counts as neither a pass nor a fail, because B-A/B-B are excluded by D53(b). Record B-D, B-E and B-F. Otherwise, add a needs_owner line saying that 1.10.16's, 1.17.5's and 1.17.6's Linux halves carry from candidate 6 only with a D53(f)-style note naming the changed checkpoint and drain files.
- **nit** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:197 (resilience part, the carry-note brackets for UAT-12's upgrade leg and C4.8)`: The carry-note pointer names `state/scheduler.json's last_applied_observation`. On closeout/w20-redeliver, a704a731 replaced that unshipped field with a map named `last_applied_observations`. An agent that searches the diff for the old name finds nothing.
  - Evidence: `git show closeout/w20-redeliver:internal/daemon/scheduler_state.go` line 114: `LastAppliedObservations map[core.SessionID]core.ObservationID json:"last_applied_observations,omitempty"`. a704a731's message: 'The map replaces the unshipped last_applied_observation of 64508520'.
  - Fix: Change the bracket text to `last_applied_observations` (a per-session map, additive and omitempty, version 1). Before regenerating, re-read the text against the merged candidate.
- **nit** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:162 (C4.5's reads)`: D59(c) says 'every map-derived list in status and doctor is swept'. The re-check diffs two `status --json` reads, but reads `doctor --json` only once, so doctor's order stability is never checked in a real store with two or more sessions.
  - Evidence: mkrecheck8.py:162 has 'read `qompack status --json` twice ... and diff them' and then 'Also read status and doctor --json once'. Ledger D59(c).
  - Fix: Read `qompack doctor --json` twice with no state change between and diff the two, as for status. This needs no extra session.

## fix:night:r2: status `done`, head `f97b1a4645e063600a4d2a7ffff16e8f3edd933f`

### Summary

No product code changed. Every change is coordinator tooling under plans/sdd/V6-closeout/coordinator/ on verify/v6. This round adds six commits (e8a3a2b1..f97b1a46) on top of round 1's df873aa9. Each has a "Refs: V6-VERIFY, C3.1" footer and no attribution trailer. Nothing was run for real: no chain, freeze, bundle build, tag, suite, Docker or Claude Code session.

**Review resolution (round 2)**
I re-checked all six findings and four nits against df873aa9. All ten hold, and all ten are fixed. Each fix has a harness row that fails on df873aa9. I also found two more defects and fixed them.

Confirmed on the real toolchain:
- The major finding holds. closeout/w20-config adds a struct field to internal/config/config.go, and config.go runs at package init (`var globalSchema = buildSchemaIndex()`). A traced BenchmarkHistogram_Observe on w20-config (d64129b2) against d20309c0 executes config.go and is selected. No executed block covers the changed lines 273-278.
- `go list -test -deps` shows that every listed package except paths, symbols, rules and hostperm links internal/config. hostperm is selected anyway, through patterns.go. That makes about 59 of the 65 rows: about 3 h 40 min on Windows and 3 h 15 min on Linux.

**Fixes**
- Night order. release-check (C3.12, D57(a)) now runs after the Linux lanes and C5.1. C5.2 runs after release-check.
- Estimates. c52-derive, c52-win and c52-linux start only when their estimate ends by the deadline, and the check is repeated after every AC wait. Otherwise they are SKIPPED, and the chain log points at the derived set for a later C5.2 night. `C8_C52_ONLY=1` runs exactly that night.
- release-check retry. After a red try 1, a retry needs the full RC_EST_S, because release-check stops at its first FAIL.
- Battery windows. A release-check window that ran wholly on battery is now NOT-REFERENCE, as the header defines it, not INVALID-POWER. It is still retried on AC.
- c51-linux (new step). Quiet C5.1 on Linux, report only, in its own container window. It covers the Linux halves of 1.10.16, 1.17.5 and 1.17.6. It counts under `reported=`, and as failed when it wrote no harness JSON.
- Standalone runs. Run alone, overnight-c8.sh refuses, before writing anything, a SHA that is not 40 characters, a checkout that is not at that SHA, and a checkout that is not clean.
- c52derive.py. Each row is traced at its own listed benchtime. A code change anywhere in a package the benchmark executes now selects the row, which covers declarations and files built only on another OS. The report also says, without selecting on it, whether an executed block covers a changed line.
- phase3.sh. linux-child passes --coload, as nightly.yml's race-product-child does.
- mkrecheck8.py. The text now names `last_applied_observations`, and C4.5 reads doctor --json twice and diffs the two reads.
- README. Launch times are computed from the records: launch by NIGHT_DEADLINE minus 8.5 h for release-check, or minus 15.5 h to fit C5.2 as well. The abort procedure uses nightabort.ps1. The re-run procedure says qompack-cx-cand must be detached at the full SHA.

**Defects found beyond the review**
1. The documented abort, before and after the reviewer's fix, does not stop the night. An MSYS shell starts every program by fork and exec, and the fork's stub exits. So the night's sh, go and python processes record a parent that no longer exists, and a ParentProcessId walk finds the root alone.
   - Probe on this host: the old snippet listed only the bash, and two sleeps outlived it. A real abort would have left overnight-c8.sh running.
   - The new nightabort.ps1 walks MSYS's own parent pids (`ps -l`). It then adds native children only when they were created after their parent, and refuses unless the root is still the logged night shell. It lists before it stops.
   - Verified on a probe tree of bash, sh, sleep, cmd and ping: the refusal works, all 6 processes are listed, and 0 remain after -Stop. Harness row K2 runs the real script on a real tree.
2. My own first version of the estimate check ran a step on battery once the AC wait budget was spent, without re-checking the estimate. Row O31 fails on that version and passes on the fix.

**Not done as suggested**
The reviewer's fix (3) was to run c52derive inside c8-night before the freeze. I did not, because it would push release-check about 45 min later every night. Instead, the README states the expected set and its estimated length from a static check and gives an optional pre-launch derivation command, and the chain logs each C5.2 estimate.

**Red, then green**
- New and changed rows against df873aa9's scripts: 10 of 11 failed (O1, O3, O21, O24-O30). O23 is modified, not new, and passes on both.
- P8 and K2 against df873aa9: both failed. O31 passes on df873aa9, which has no estimates, and fails on the pre-fix wait logic.
- The first full run of the fixed scripts was 69/70. O29 failed because a C5.2-only night's start line mentioned release-check; I fixed the line.
- Final full harness on accc0660's scripts: 70 cases, 0 failed, 2937 s.

**Machine hygiene**
Another seat overwrote two of my commit-message drafts in the shared scratchpad. I rewrote them in a private directory, and no committed content was affected. My probe processes were all stopped. The harness and the probes left no process behind, and my scratch clones are removed.

### Commits

- 067fab8f chore(v6): retry invalid-power night steps on their own records
- 86193037 chore(v6): fail phase3's multi-command arms on any failure
- bdc15929 chore(v6): gate candidate 8's freeze on this run's evidence
- 836bc3d2 chore(v6): widen candidate 8's live re-check
- ccea07ab docs(v6): record candidate 8's launch and abort procedure
- 51f34451 chore(v6): add a dry harness for candidate 8's night scripts
- c2b03a54 docs(v6): tighten candidate 8's abort and re-run steps
- bc54677f chore(v6): widen candidate 8's re-check to carried rows
- 2c06e1f6 chore(v6): close round 1's night findings with harness cases
- 2f1d30fe docs(v6): update candidate 8's launch, abort and re-check steps
- c3217dcc chore(v6): set core.longpaths in the night's scratch clones
- df873aa9 docs(v6): describe keepawake's and c52derive's refusals
- e8a3a2b1 chore(v6): trace c5.2 rows at their benchtime, see declarations
- 8f2ce775 chore(v6): declare co-load on the local linux child race lane
- 85de3ae7 chore(v6): correct two re-check readings of candidate 8's code
- 2ef4f007 chore(v6): stop exactly a night's process tree by its msys pids
- accc0660 chore(v6): put release-check before c5.2, fit steps to deadline
- f97b1a46 docs(v6): size candidate 8's night and fix its abort and re-run

### Findings resolution

- **fixed**: R0-0 BLOCKER timed() retry was a no-op (recrun refuses to overwrite)
  - Round 0 (067fab8f): each try's new records move to <step>.invalid-power-<n>/. Still green: O2, O6, O10.
- **fixed**: R0-1 acwait timeout ignored, t0 after the power check, no deadline
  - Round 0, extended in round 1. Round 2: the deadline and the step's estimate are checked again after every AC wait (O31). Green: O3, O4, O5, U3, U5, N14, O31.
- **fixed**: R0-2 release-check judged over one 3-hour window
  - Only the AC-sensitive windows are judged (rounds 0 and 1). Round 2: a window wholly on battery is NOT-REFERENCE; a retry after a red try needs RC_EST_S. Green: O6, O7, O14, O20, O21, O25.
- **fixed**: R0-3 prefreeze summary appended; stale pass lines accepted
  - Round 0. Green: N5, F4.
- **fixed**: R0-4 v0.3.0 tag created in the shared ref store
  - Round 0: the tag exists only in an isolated clone, which sets core.longpaths (c3217dcc). Green: O1, O9, N20.
- **fixed**: R0-5 phase3 arms masked failures
  - Round 0. Green: P1-P7.
- **fixed**: R0-6 prefreeze -p 2 passes without the co-load declaration or a power record
  - Rounds 0 and 1. Green: N1, N10, N11, F4.
- **fixed**: R0-7/R0-12 integration hot-path rows not run alone
  - Round 0. Green: F3, P7, N1.
- **fixed**: R0-8 power() parsing unsafe; set -u crash
  - Round 0. Green: U1, U2, O8.
- **fixed**: R0-9 no time bounds on docker, push and gh
  - Rounds 0 and 1. Green: O11, N8, O15.
- **fixed**: R0-10 host-validate 'unverified' accepted
  - Round 0. Green: N7.
- **fixed**: R0-11 c8-night preconditions
  - Rounds 0 and 1. Green: N2-N4, N12, N15-N18.
- **fixed**: R0-13 gate lacked gen-command-docs, licenses and govulncheck
  - Round 0. Green: F1, F2, N1.
- **fixed**: R0-14 no keep-awake EXIT trap; overnight always exit 0
  - Rounds 0 and 1. Green: N9, N13, K1.
- **fixed**: R0-15 re-check budgets had no margin
  - Budgets are 8, 5, 8 and 4 = 25. The generator asserts them; regenerated from 69860c1d this round.
- **fixed**: R0-16 UAT-12 lacked the 40-pointer leg and the , and = names
  - Round 0. Unchanged.
- **fixed**: R0-17 ADR §23 and closeout/live8 dependencies unchecked
  - Round 0. The generator still refuses a candidate without ADR 0011 section 23; regeneration from 69860c1d gives exit 0.
- **partial**: R0-18 C1.6 marked fixed on a false citation
  - The re-check has a C1.6 leg. The ledger's D59 sentence and the C1.6 cell (V6-CLOSEOUT-CHECKLIST.md:240) need the coordinator; the ledger is read-only to me.
- **partial**: R0-19 D57(e) C5.2 carry broken, no c52 step
  - Fixed by construction: c52derive, c52-win and c52-linux, made stricter this round (benchtime, package-dir rule). Not mine: inventory-current.tsv still needs candidate 8 C5.2 dispositions, which follow the C5.2 measurement. That measurement will most likely come on a C5.2-only night (see open issues).
- **partial**: R0-20 rows carried from candidates 3 and 4 without a D53(f) note
  - The generator writes the carry notes against the embedded diff (round 1). Not mine: D59's 'the other rows carry from candidate 7 by the diff' in the ledger.
- **fixed**: R0-nit doc comments, release-check record, stale cp
  - Round 0.
- **fixed**: R1-1 early refusal can leave keep-awake held
  - Round 1. Green: N13, K1, N9.
- **fixed**: R1-2 deadline rolls over 24 h between the two scripts
  - Round 1 (NIGHT_DEADLINE_EPOCH, NIGHT_MAX_AHEAD_H). Green: N14, O19, N19.
- **fixed**: R1-3 release-check power verdict failed open
  - Round 1. Green: O14.
- **fixed**: R1-4 re-run into a populated evidence dir mislabels refusals as VALID
  - Round 1. Green: O12, O13, N15.
- **fixed**: R1-5 docker ownership decided from one probe
  - Round 1. Green: O15, O11, O1.
- **fixed**: R1-6 bundle dir and candidate cleanliness checked only after the freeze
  - Round 1. Green: N16.
- **fixed**: R1-7 C5.2 set hard-coded at three benchmarks
  - Round 1: c52derive.py. Round 2: own benchtime and the package-dir rule (see R2-2). Green: O1, O16, O17, O22, O23, O30.
- **fixed**: R1-8 no c52-linux
  - Round 1. Green: O1, O3.
- **fixed**: R1-9 D53(f) carry notes missing
  - Round 1. Round 2 corrected the scheduler field name the notes point at (R2-N3).
- **fixed**: R1-10 C4.5 was CLI-only
  - Round 1.
- **fixed**: R1-11 F-C48-1's second route not exercised
  - Round 1.
- **fixed**: R1-12 branch precondition glob too narrow
  - Round 1. Green: N17, N2-N4.
- **fixed**: R1-13 Linux timing steps had no power record
  - Round 1. Green: O18, O3.
- **fixed**: R1-14 README lacked D61(a) step 0 and a launch-by time
  - Round 1. Round 2 replaces the launch-by figure with one computed from the records (see R2-5).
- **fixed**: R1-15 re-check not gated on the night or on hosted CI
  - Round 1.
- **fixed**: R1-16 verify/v6 non-plan paths not checked
  - Round 1. Green: N18.
- **fixed**: R1-nit1/nit2/X6 end reading, Kernel-Power 42/107, fresh clone per try, no retry on battery
  - Round 1. Green: U5, O20, O21.
- **fixed**: R2-1 (minor) a standalone overnight-c8.sh never checks the checkout is the candidate, or clean
  - Before writing anything, overnight-c8.sh refuses (exit 2) a SHA that is not 40 lower-case hex characters, a checkout whose HEAD is not that SHA (it names the actual commit), and a checkout with any `git status --porcelain` output. README step 7 now detaches qompack-cx-cand at the full SHA and checks status first. Red on df873aa9: O24, all 12 checks (a short SHA, a checkout at candidate 7, a tracked change and an untracked file each ran a whole night). Green: O24. O23 now makes its stray .go file git-ignored, so c52derive's guard is still the one exercised.
- **fixed**: R2-2 (minor) c52derive traces at b.N=1 and cannot see declaration-only changes
  - Each row is traced at its benchtime from quiet.sh (1s, 10x or 200x); a row without a valid benchtime refuses. A code change in any non-test file of a package the benchmark executes selects it, which also covers files the tracing OS does not build. The docstring documents both rules; selftest cases cover a declaration-only file, an other-OS file and an unrelated package. Real toolchain, the reviewer's scratch module (rotate() every 100th iteration, Size const): df873aa9 selected 0 rows; the fix selects the row for rotate.go (executed at 1s) and consts.go (package-dir rule). Red on df873aa9: O30 (5 checks). Green: O30 and the selftest.
- **fixed**: R2-3 (minor) README abort snippet walks ParentProcessId with no creation-time or root check
  - The finding holds, and the defect goes deeper: the walk never reaches the night's descendants (see the extra 'abort cannot stop the night' entry). The new nightabort.ps1 checks that the root is still the logged shell (MSYS pid maps to the logged Windows pid, command line names c8-night.sh or overnight-c8.sh), walks MSYS parent pids, adds a native child only when it was created after its parent, lists before stopping, and stops each process only while its pid and creation time are unchanged. overnight-c8.sh's start line now logs both pids. Green: K2 and the probe runs.
- **fixed**: R2-4 (minor) release-check retry sized by a short red try 1
  - A green try 1 ran the whole release-check, so its length is the retry estimate. A red try 1 shorter than RC_EST_S is sized at RC_EST_S. The note says which. Red on df873aa9: O25 (try 1 went red at 630 s with a source change in its e2e window; the retry was admitted with a 00:10 deadline). Green: O25.
- **fixed**: R2-5 (major) night undersized by 4-5 h: release-check SKIPPED, C5.2 runs into the day
  - Confirmed on the real toolchain (see summary). (1) release-check now runs after the Linux lanes and C5.1, and C5.2 after it (O1 order check). (2) c52-derive (C52_DERIVE_EST_S), c52-win and c52-linux (C52_EST_WIN and C52_EST_LINUX: candidate 5's per-package run time, plus C52_EST_OVERHEAD_S) start only when they end by the deadline, re-checked after every AC wait. A SKIPPED step names c52-derive/selection.tsv, and C8_C52_ONLY=1 runs the C5.2 night. Red on df873aa9: O26, O27, O29. Green: O26, O27, O29, O31. (3) The derivation is not moved into c8-night before the freeze, because that adds about 45 min before release-check every night; the README computes the night from the records and gives an optional pre-launch derivation command, and the chain logs each C5.2 estimate. (4) The owner's question is under needs_owner. c52derive reports per file whether an executed block covers a changed line, and counts rows selected without one, without changing the selection (O30; real w20-config trace: config.go 'none (changed lines 273-278)').
- **fixed**: R2-6 (minor) no Linux C5.1 on candidate 8
  - c51-linux is an AC-gated step in its own container window and its own evidence directory (quiet-c51-linux), so an INVALID-POWER try moves aside whole. A VALID run counts as reported (neither a pass nor a fail, D53(b)); a run that wrote no c51-linux-hotpath.json counts as failed; battery or events count as for any gated step. Red on df873aa9: O28, O1, O3. Green: O28, O1, O3.
- **fixed**: R2-N1 (nit) release-check battery window labelled INVALID-POWER
  - A window wholly on battery reads 'NOT-REFERENCE <sec>:on-battery', counts under not_reference, and is still retried on AC. A window with an event stays INVALID-POWER and takes precedence. Red on df873aa9: O3, O21. Green: O3, O21.
- **fixed**: R2-N2 (nit) phase3 linux-child lacks --coload
  - Confirmed: nightly.yml lines 115-124 set QOMPACK_UNDER_COLOAD '1' on race-product-child, and the gate's --coload sets the same. The arm now passes --coload; linux-timing does not. Red on df873aa9: P8. Green: P8.
- **fixed**: R2-N3 (nit) carry note names last_applied_observation
  - Confirmed on closeout/w20-redeliver (scheduler_state.go:114, a704a731). The note now reads last_applied_observations, a per-session map, additive and omitempty, version 1. Regenerated from 69860c1d: exit 0, parses, no Date.now or Math.random.
- **fixed**: R2-N4 (nit) doctor --json read only once
  - C4.5 now reads doctor --json twice and diffs the two reads, as for status (D59(c)). No extra session.
- **fixed**: R2-X1 (found this round) the documented abort cannot stop the night
  - An MSYS fork stub exits after exec, so the night's children record a dead Windows parent. Probe (my own processes): the old snippet listed the bash alone, and two sleeps outlived it. Fixed by nightabort.ps1 (see R2-3). Probe with the fix: refusal on a wrong pid, 6 processes listed (bash, sh, sh stub, sleep, cmd, ping), 0 left after -Stop. K2 runs this in the harness against the real script.
- **fixed**: R2-X2 (found this round) an estimate not re-checked after the AC wait
  - In my own first version, once the AC wait budget was spent, a gated step ran on battery without re-checking its estimate, so c52-win could start at 00:50 and run past a 01:30 deadline. The loop now re-checks the deadline and the estimate after every wait. O31 fails on that version and passes on the fix.

### Tests

- `sh nightharness.sh   (all 70 cases, scripts of accc0660, in qompack-v6/plans/sdd/V6-closeout/coordinator)`: 70 passed, 0 failed, exit 0, wall 2937 s. The first full run (69/70) failed only O29 on the C5.2-only start line, fixed before this run.
- `sh nightharness.sh O1_all_ac O3_battery_night_is_never_valid O21_rc_not_retried_on_battery O23_c52_stray_go_file_refused O24_candidate_checkout_must_be_the_candidate O25_rc_retry_after_a_red_try_needs_a_full_run O26_c52_skipped_when_it_cannot_end_by_the_deadline O27_c52_derivation_skipped_near_the_deadline O28_c51_linux_is_report_only O29_c52_only_night O30_c52_declarations_and_benchtime   (new harness against df873aa9's scripts, scratch copy)`: 11 cases, 10 failed (expected red). O23 is a modified case and passes on both versions.
- `sh nightharness.sh P8_linux_child_declares_coload_as_nightly O31_estimate_rechecked_after_the_ac_wait K2_nightabort_stops_exactly_the_night   (against df873aa9's scripts)`: P8 and K2 failed (expected red). O31 passed: df873aa9 has no estimates. O31 against the pre-fix wait logic: failed ('c52-win SKIPPED after the wait').
- `python c52derive.py --selftest`: selftest: PASS (new: benchtime parsing and refusals, changed_lines, profile blocks, line-level hit and miss, declaration-only, other-OS and unrelated-package cases)
- `python c52derive.py <scratch module> HEAD~1 <out> <list>: df873aa9's version, then this round's (real go, the reviewer's rotate/consts probe)`: df873aa9: 0 selected, 'carry holds'. Fix: 1 selected, 'executes changed internal/p/rotate.go; a changed file in a package it executes internal/p/consts.go', line-level 'yes'.
- `python c52derive.py <shared clone at closeout/w20-config d64129b2> d20309c0 <out> <one-row list: obs Histogram_Observe 1s>`: exit 0, 15 s. Selected: executes changed internal/config/config.go; line-level 'none (changed lines 273-278)'; counted as one row selected with no executed block on a changed line.
- `go list -test -deps ./internal/<pkg> for the 19 listed packages`: store, observer, negknow, daemon, checkpoint, cli, config, canon, chunk, obs, eval, dag, scheduler, sketch and skills link internal/config; paths, symbols, rules and hostperm do not.
- `pwsh -File nightabort.ps1 on a detached probe tree (bash c8-night.sh, sh child.sh, sleep, cmd, ping): a wrong Windows pid, then list, then -Stop`: Wrong pid: exit 2, nothing stopped. List: 6 processes. After -Stop: 0 of the probe's processes left. The old README snippet on the same kind of probe listed 1 process (the bash), and two sleeps survived (stopped by hand).
- `python mkrecheck8.py live-rerun-c7.js <scratch>/recheck8-sample.js 69860c1dab114a664626610c09596adfa2e7cb3a <c7 windows-amd64 bundle>`: exit 0 (the generator's own node parse check passes); no Date.now or Math.random; new text present: last_applied_observations, and doctor --json read twice
- `sh -n power.sh stamped.sh overnight-c8.sh c8-night.sh prefreeze.sh phase3.sh nightharness.sh; python -m py_compile mkrecheck8.py c52derive.py; PowerShell Parser.ParseFile nightabort.ps1`: all parse, 0 errors
- `go run ./tools/devtool fmt-check   (qompack-v6)`: exit 0
- `no-push shared scratch clone: verify/v6 f97b1a46 merged with closeout/integration 738d67c7 (tree 81cf8ebe): go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors && go test -p 1 -count=1 ./test/guards ./test/docs`: PASS runpatterns, PASS docmarkers, PASS coveragefloors; ok test/guards 33.0 s; ok test/docs 3.2 s; no path outside plans/ from verify/v6
- `sh shacheck.sh . plans/sdd/V6-closeout/coordinator/README.md`: only 12817cd0, 21c07942 and c212712d are not in HEAD. These are the w15 branch tips that step 2 names on purpose, unchanged since round 1.
- `go vet (windows/linux/darwin), golangci-lint, -count=20, -race -count=3, gen-*-docs --check`: not applicable: no Go package, Go test or generated input was touched

### Criterion changes

- Round 2. Night order: release-check (C3.12, D57(a)) now runs before C5.2. Reason: it is a release gate, and C5.2 is a measurement a later night can repeat.
- Round 2. c52-derive, c52-win and c52-linux are SKIPPED when their estimate would end after the deadline, re-checked after every AC wait. Before, a gated step was bounded only at its start. Stricter: a SKIPPED step makes the night exit non-zero, and the night stays out of the owner's day.
- Round 2. A release-check retry after a red try 1 needs RC_EST_S, not try 1's length. Stricter.
- Round 2. A release-check window that ran wholly on battery is NOT-REFERENCE, as the header's taxonomy says, not INVALID-POWER. It moves from invalid_power to not_reference and is still retried on AC. Neither label is a pass or a fail, before or after.
- Round 2. New step c51-linux, report only. A VALID run counts under reported=, and only a missing harness JSON fails it. Reason: D53(b), the container's B-A and B-B are not verified in target.
- Round 2. C5.2 derivation: each row is traced at its own benchtime, and a code change in any file of an executed package selects it. The selection can only widen (fail-closed). Line-level evidence is reported and never used to select.
- Round 2. overnight-c8.sh run alone refuses a short SHA, a checkout not at it, or one that is not clean. Stricter.
- Round 2. linux-child declares co-load, as nightly.yml's race-product-child does. Wall-clock rows in that -race lane are reported, not judged; linux-timing and linux-e2e-timing still judge them alone (D28). Before, a race-slowed row could only produce a false red.
- Round 2. Abort procedure: nightabort.ps1 walks MSYS parent pids, takes a native child only when created after its parent, and refuses unless the root is the logged night shell.
- Round 2. Re-check C4.5 reads doctor --json twice and diffs the reads (D59(c)). Stricter.
- Round 1 (carried for ratification): VALID needs an AC end reading, and Kernel-Power 42 and 107 count as INVALID-POWER; every overnight step has a power record, and Linux *-timing lanes count only when VALID; release-check with no window found is VALID only when it failed before ci-local test; a release-check retry needs AC and a fresh clone; C5.2 is the derived set on both OSes; c8-night's branch precondition covers every closeout/w* tip, verify/v6 may add nothing outside plans/, and a deadline more than 16 h away is refused; overnight-c8 refuses a populated evidence directory; Docker Desktop is stopped only when this chain started it; the re-check re-runs UAT-04 and C4.4, runs the six slash commands, restarts the parent behind its fork, writes D53(f) notes, and requires a clean night and classified hosted CI.

### Open issues

- C5.2 will most likely not run on the freeze night. About 59 of the 65 rows are selected (config.go runs at package init), which is 3 h 40 min on Windows and 3 h 15 min on Linux after release-check. A launch by 23:30 for 08:00 fits release-check, but c52-win and c52-linux will be SKIPPED with their estimates. Candidate 8's C5.2 evidence then needs either a C5.2 night (C8_C52_ONLY=1, about 7 h 40 min) or an afternoon launch by about 16:30. The inventory dispositions for the C5.2 rows (1.10.17, 1.16.11, 1.1.27, 1.12.17 and every other selected row) wait on that measurement; inventory-current.tsv is not mine.
- The estimates are not measured on candidate 8. C52_EST_* is candidate 5's per-package time. C52_DERIVE_EST_S (45 min) is derived, not measured at the new per-row benchtimes; one real row took 15 s with a warm build cache. The first real derivation's step duration in chain.log will show the real figure.
- nightabort.ps1 is verified on probe trees and in K2, not on a real night. Abort step 1's command-line sweep for orphans stays as the fallback. A shell can start a child between the listing and its own stop.
- linux-child's --coload has not run for real in the container; only phase3's arguments are checked (P8).
- Carried from round 1: closeout/w15-docs 21c07942, closeout/w15-ledger 12817cd0 and closeout/w15-services c212712d hold commits that neither candidate 7 nor integration has. c8-night refuses until each is merged or named in C8_EXEMPT. Whether their fixes were superseded is unverified.
- Carried: the live re-check can be generated only once wave 19c's ADR 0011 section 23 is in the candidate. If section 23 is renumbered, mkrecheck8.py refuses and its text must change.
- Carried: in the qompack-v6 worktree itself, `devtool lint --only=runpatterns` is falsely red, because the git-ignored dist/v6-remediation/ holds stray .go packages that are not mine. c8-night lints a scratch clone, and c52derive refuses such strays in the candidate (O23).
- Carried: keepawake.ps1 holds ES_SYSTEM_REQUIRED only. If tonight shows Modern Standby (506) invalidations, ES_DISPLAY_REQUIRED may be needed. Unverified.
- The harness's real-process boundary: K1 (the real pwsh, nothing to hold) and K2 (a real probe tree stopped by the real nightabort.ps1). Real PowerShell power queries, Docker, git push, gh and claude are not exercised.
- Other seats share this session's scratchpad: two of my generic-named draft files were overwritten by another seat. No committed content was affected; later work used a private subdirectory.

### Needs owner

- Ruling on D57(e) at line level. Does a changed file with no executed statement on a changed line keep the carry? The real case is closeout/w20-config's new Warning.VersionedReset field (config.go lines 273-278): every benchmark linking internal/config executes config.go at package init, but no executed block covers those lines. c52derive selects such rows (D57(e) is file-level) and reports them. If the answer is yes, a rule must say which line-level evidence counts. Precedent: the toolnames.go question.
- Choose the launch. (a) By NIGHT_DEADLINE minus 8.5 h (23:30 for 08:00): release-check fits, C5.2 is SKIPPED, and a C5.2 night follows (C8_C52_ONLY=1, launch by deadline minus 8 h). (b) By about NIGHT_DEADLINE minus 15.5 h (16:30): everything in one night, which needs an afternoon go because the laptop is busy from then.
- C52_EST_WIN and C52_EST_LINUX (new). Per-package seconds: candidate 5's summed started_at/ended_at of every phase3/c5/quiet/c52-{win,linux}/c52-*-r<round>-*.json record (all of the package's rows, both sides, 10 rounds), rounded up to the minute. A package not listed counts as the largest entry. Whole list: Windows 12720 s + overhead, Linux 11160 s + overhead.
- C52_EST_OVERHEAD_S = 600 s (new). Candidate 5 spent 430 s (Windows) and about 330 s (Linux, gate preparation included) outside the measured runs; rounded up to 10 min.
- C52_DERIVE_EST_S = 2700 s (new). One round of every listed row took 612 s on candidate 5; set-mode coverage adds up to about 1.5x, plus a coverage-instrumented link per row (about 10 s x 65) and the first instrumented build (about 3 min): about 30 min, 45 with margin.
- c51-linux is report only (new step). Its exit status is neither a pass nor a fail (D53(b)), and a VALID run without its harness JSON counts as failed. Ratify, or say the Linux halves of 1.10.16, 1.17.5 and 1.17.6 carry from candidate 6 with a D53(f) note instead.
- Ratify this round's criterion changes (criterion_changes below).
- Carried from round 1: NIGHT_MAX_AHEAD_H = 16 h (its comment re-derived this round: 7.25 h through release-check, about 15 h with C5.2); the static C5.2 floor; docker_answers' 3 probes 30 s apart; the live re-check's 25 sessions against an already exceeded budget (D53(g)); the decision on closeout/w15-docs, w15-ledger and w15-services; the ledger and inventory edits (D59's C1.6 sentence and the C1.6 cell, D59's 'carry by the diff', the candidate 8 C5.2 dispositions); NIGHT_DEADLINE 08:00, RC_EST_S 10800 s, AC_WAIT_BUDGET_MIN 180, and the docker, push and gh time limits.

## verify:night: verdict `needs-fixes`, 1 finding(s)

- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:58-59 (header), :113-118 (C52_FLOOR_PKGS/C52_FLOOR_FILTER and their comment), :399-400 (chain.log line); README.md "Watch" c52-derive bullet; c52derive.py docstring :31-33`: The C5.2 fallback is described as something it is not. When c52derive.py exits 2 (a stray .go file, a base that does not resolve, or every trace failing), the chain measures a fixed 12-row "static floor" over checkpoint, cli, config and daemon. The header calls it "the rows the 2026-10-03 diff reaches", and the comment says it was chosen "by w17-inventory's executed-file sets". The seat's own round-2 confirmation contradicts both. closeout/w20-config changes internal/config/config.go in code, and config.go runs at package init (`var globalSchema = buildSchemaIndex()`) in every benchmark binary that links internal/config. The diff therefore reaches about 59 of the 65 rows: store, observer, negknow, canon, chunk, obs, eval, dag, scheduler, sketch and skills too, and hostperm through patterns.go. The w17 executed-file sets the comment cites already count config/*.go among, for example, the obs set's 8 executed files. If the fallback fires, chain.log reads "the static floor is measured" over 12 rows and leaves about 47 rows with a broken D57(e) carry. A reader of the header could take that set as complete and treat the other rows as carried. The derivation is counted as a failed step, so the night does not read green, but the documented meaning of the fallback is wrong.
  - Evidence: overnight-c8.sh:58 says "When the derivation cannot run at all, the static floor (the rows the 2026-10-03 diff reaches) is measured". Lines 113-116 say "the rows whose executed packages the 2026-10-03 diff from candidate 7 reaches ... by w17-inventory's executed-file sets", and C52_FLOOR_PKGS="checkpoint cli config daemon". The seat's result, R2-5, says: "store, observer, negknow, daemon, checkpoint, cli, config, canon, chunk, obs, eval, dag, scheduler, sketch and skills link internal/config ... about 59 of the 65 rows". The real trace of BenchmarkHistogram_Observe on closeout/w20-config was selected because it "executes changed internal/config/config.go". w17-inventory/runs/c52-executed-files.txt (3630f54e) shows the obs set with "product files executed: 8", which the round-2 reviewer showed includes internal/config/{config,deadlines,defaults,load,validate}.go. Harness O16, O22 and O23 assert this 12-row filter as the fallback.
  - Fix: Either make the fallback fail closed (quiet.sh's whole list, which the existing c52-win and c52-linux estimate check will SKIP when it cannot end by the deadline), or keep the 12 rows and relabel them everywhere as a partial set: in the header, the floor comment, the chain.log line at :400, the README Watch bullet and the c52derive.py docstring. Have the chain.log line state that every other listed row's D57(e) carry stays broken until a derivation runs. Update O16, O22 and O23 to match.


# Wave 22 night seat

Branch `verify/v6`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:727 (engine_up_at_start), with container_up :295-313 and container_down :314-323`: Whether the chain owns the Docker engine is decided by one `docker ps` probe at the night's start. The code's own comment at :287 says one failed probe does not mean the engine is down, yet that single probe decides ownership. Failure path: the owner's engine (with the supabase stack) is up all night, the start probe fails once, and the engine later misses the three probes at the Linux lanes. container_up then runs `docker desktop start`, sets cs_started=1 when it returns 0, and container_down runs `docker desktop stop`. That stops the owner's engine, which D56(g) and the owner's directive forbid.
- **major** `plans/sdd/V6-closeout/coordinator/nightharness.sh:22-23 (T=$(mktemp -d), HB=$T/bin) and :1536 (PATH="$HB:$PATH"); claims at nightharness.sh:9-18 and README.md:29`: The dry harness has no guard that its stubs are actually on PATH. When TMPDIR is a Windows-form path (for example a Claude Code scratchpad written as C:/...), mktemp returns C:/..., and PATH="C:/...bin:$PATH" splits at the drive colon. Every stub (docker, powershell, go, gh, timeout, date, sleep) is then bypassed, and the copied night scripts run against the real tools. The header's claim ('nothing outside its temporary directory is touched ... starts no Docker, Claude Code or Go process') then does not hold. N cases could also reach the real gh if the real clock passed their deadline check.
- **major** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:145-154 (RERUN reading list), :225 (swap 'owner decisions D1-D61'), :172-183 (retrieval part); COMMON's reading list inherited from live-rerun-c7.js:34`: The generated live re-check never mentions D62, D63, D64 or D65, so several user-visible changes in candidate 8 are not covered or would be judged against stale rules. (1) D63/D64's free-text summary whitelist is not referenced anywhere: its root-unit rules and the accepted over-withholding of `git log --pretty=format:%h`, `curl localhost:3000` and {"skill":"plugin:name"}. The retrieval part asserts only that 'ordinary commands ARE shown (D60(c), D61(b))'. (2) D62(f): section 2, the user's verbatim prompts, is outside D50. Without that, session B's prompts naming private/deny.txt can be raised as a false UAT-12 finding. (3) D62 forkwork: Current work skips slash-command invocations. The lane runs six /qompack: commands and /compact turns but never checks this. (4) D62 status: fsck and eval read in a stable order (only status and doctor are read twice), and config-violations.json is written only on change and removed when clean. Neither is checked. The reading list also names 'w19c-*/' reports, which do not exist, and omits w21-*/ (w21-e2e, w21-polish). docs/security.md, which holds the whitelist, is not in COMMON's reading list.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:73 (trap 'exit 143' TERM) and overnight-c8.sh:249; harness cases N9 and O9`: sh defers a trapped signal until the current foreground child exits. A TERM sent to the night shell during the pre-freeze (up to 60-90 min a step) or release-check (about 3 h) therefore deletes the sentinel, stops the keep-awake and removes the scratch clone only after that whole step ends, and the step keeps running. N9 and O9 release the blocked stub (echo go > go.fifo) right after the kill, so they cannot observe the deferral. The header's 'released on every way out ... a catchable signal' holds only after that delay.
- **minor** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:166`: summary.log's header records `go=$(go env GOVERSION)`, run in the caller's working directory, not in <repo>. go.mod has `toolchain go1.26.6` with GOTOOLCHAIN=auto, so a launch from outside a module (Start-Process from a PowerShell window in C:\Users\Quant) records the local default toolchain, while every step runs on go1.26.6.
- **minor** `plans/sdd/V6-closeout/coordinator/phase3.sh:84 (win-e2e-timing -timeout=30m); same budget in ci.yml:308`: The night judges all of test/e2e in one binary under a 30-minute timeout, and the recorded margin is about 15%. A timeout is a VALID red (failed=1, so the live re-check is blocked) and says nothing about wall-clock budgets. Candidate 8 adds test/e2e work (113 to 114 test functions, X10 +24 lines, X14 +19 lines), and earlier runs of this package have hit the 1800 s wall.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:159 (RC_EST_S), :657-660 (release_check start), :401-411 (gated steps without an estimate)`: Four timing risks at the end of candidate 8's night. (1) RC_EST_S (3 h) has never been measured on this tree: release-check has never run locally with its current step list (lint with stubskips' whole-tree pass, test, cover, build-all, guards, govulncheck, determinism, rollback, plugin-validate, marketplace). Nothing stops it at the deadline, so an overrun lands in the owner's day. (2) release-check is not AC-gated at its start, and in this night no retry can fit, because try 1 ends about at the deadline. Given the charger's cut pattern, one AC event in either test/e2e window voids C3.12 / D57(a) for the night. (3) Gated steps without an estimate (win-e2e-timing about 26 min, c51-linux with its container start) may start just before the deadline and run past it.
- **minor** `plans/sdd/V6-closeout/coordinator/power.sh:42 (power_ps)`: Every power reading and event query runs `powershell -Command` with no time bound, and each step makes at least two of these calls. A hung WMI or PowerShell query stalls the chain with nothing to stop it, past the deadline and into the owner's day, with the keep-awake still held. Docker, git push and gh calls are all wrapped in timeout; this one is not.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:80-87`: The script refuses a deadline that is too far away but has no check for one that is too near. A launch for example 30 minutes before NIGHT_DEADLINE passes deadline_near, runs the 1 h pre-freeze, and then still freezes and pushes candidate 8 in the morning, with every overnight step SKIPPED. That uses up the freeze without any night evidence. README says to launch 8.5 h before the deadline, but nothing checks it or warns.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:83-85`: The keep-awake is started and its pid logged, but the night never confirms it holds. If pwsh is missing from the detached environment's PATH, or keepawake.ps1 fails, the night runs without a keep-awake request. A host sleep would then void steps; INVALID-POWER records it, but only after the fact.
- **minor** `plans/sdd/V6-closeout/coordinator/c8-night.sh:199-210; README.md:227-249 (Abort steps 6-7)`: Nothing documents how to recover from a refusal after the freeze, at the bundle build or at host validation ('unverified' or 'rejected'). A relaunch of c8-night.sh refuses because qompack-bundles/c8 exists. README's post-freeze path, running the overnight part alone, never host-validates the bundles, pushes verify/v6 or dispatches nightly.yml, so the live re-check's gates (bundle with BUNDLE.json, hosted ci.yml and nightly green) cannot be met by following the README.
- **minor** `plans/sdd/V6-closeout/coordinator/quiet.sh:99 (work=$(mktemp -d), never removed); no disk precondition in c8-night.sh or overnight-c8.sh`: Disk space is low and the night tooling only adds to it. quiet.sh keeps its scratch directory (clones of the base and candidate, plus built binaries) after every run, and nothing checks free space. Candidate 8's night makes two of these (c51-win, c51-linux), plus a merged-tree clone and up to two release-check clones, each building whole-tree test binaries into GOCACHE under a new path. The C5.2 night makes eight more quiet work directories.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:686-707; tools/devtool/releasechecksteps.go:201`: release-check's govulncheck step fails outright when the vulnerability database cannot be reached (rcFailf). prefreeze.sh's vuln_check reads the same condition as GOVULNCHECK-UNREACHABLE and does not fail on it. A network blip at night therefore shows up as a VALID red, 'failed=1 [release-check]', with no label, and the steps after it (licenses, determinism, rollback, plugin-validate, marketplace) never run.
- **minor** `internal/cli/doctor.go:1175-1181 (doctorMetricsAge); verify/v6 plans/sdd/V6-closeout/coordinator/mkrecheck8.py:167`: Tonight's live re-check (C4.5) requires two `qompack doctor --json` reads, and two `status --json` reads, to 'agree exactly'. They never do: status always differs in data.collected_at_ms (and the text header 'collected <time>'), and once metrics/latency.json is persisted, doctor --json differs in delivery.rollover's 'persisted Ns ago'. The candidate 7 lane happened to note these as observations. A strict reading of the c8 prompt fails C4.5 on a correct build.
- **minor** `plans/sdd/V6-closeout/coordinator/quiet.sh:149 ('cli HookNoop_InProcess ... 1.1.27'); internal/cli bench (fbb32c13)`: fbb32c13 changed BenchmarkHookNoop_InProcess to spool into a temp dir instead of the checkout. C5.2 runs it ABBA against cf31e01, whose own benchmark file still spools into its clone. So row 1.1.27's paired ratio compares two different workloads. The w20-status needs-owner says so ('A bench-compare of a pre-fbb32c13 base against a newer candidate compares slightly different workloads'). Neither quiet.sh nor the ledger notes it, and the C5.2 night will measure it tonight.
- **major** `plans/V6-CLOSEOUT-CHECKLIST.md:375 (C6.2); plans/sdd/V6-remediation/inventory-current.tsv header (columns c6_result, c6_evidence, c7_result, c7_evidence only)`: Candidate 8's plan does not cover C6.2: re-disposition all 304 inventory rows from candidate 8's evidence, with verified_in_target only where an executed artifact exists. The inventory has no candidate 8 column. D62(b) breaks D57(e)'s carry for every C5.2 row (config.go is linked everywhere), so rows 1.10.17, 1.16.11, 1.1.27, 1.12.17 and about 59 others need candidate 8 dispositions. The w20-night seat says those dispositions 'wait on that measurement; inventory-current.tsv is not mine'. D62(g) adds C6.3, C6.4, C6.5, C7.3 and C7.6 to the release plan, but neither C6.1 nor C6.2, and the coordinator README's candidate 8 section never names C6.2.
- **minor** `plans/V6-CLOSEOUT-CHECKLIST.md:360 (C5.3) and :317 (C3.3)`: Two remaining checklist items have no step in candidate 8's plan and no ruling. (1) C5.3, the deterministic replay evaluation (the 24-session phase-0 comparison) re-run on the frozen candidate, with fraction-of-OPT as a diagnostic: hosted replay-gate runs the gate, but no step records C5.3's result. (2) C3.3's product-child race lane on Windows: overnight-c8 runs win-race (devtool test-race) only. The product-child lane (QOMPACK_REQUIRE_CHILD_RACE=1) runs only in the Linux container (linux-child) and on ubuntu (nightly.yml race-product-child), and no ruling says that satisfies C3.3's Windows wording.
- nit `plans/sdd/V6-closeout/coordinator/c8-night.sh:107`: The precondition loop lists branches with `%(refname:short)` and then keeps only names matching '^closeout/w'. If a tag or remote ref shares a branch's name, refname:short becomes 'heads/closeout/...', and that branch is silently not checked.
- nit `plans/sdd/V6-closeout/coordinator/README.md:301-322 ('The docs figure')`: The section cites 'line 714 at integration 67e0fddb'; the sentence is now at docs/architecture.md:721 in 2bf29705. It also describes the marking as a pending docs change, but D65(a) records it as done in integration (458b583a, which is in H).
- nit `plans/sdd/V6-closeout/coordinator/README.md:85-89; nightharness.sh O1 checks 'the clone path is logged' / 'the clone is removed'`: (a) README says the harness takes up to about 2 hours. Under today's load it ran at about 20 cases an hour: 36 cases took about 1 h 40 min, so a sequential 86-case run would take about 4 h. (b) O1's sed `release-check clone \([^ ]*\) at` cannot parse a scratch path that contains a space. 'The clone is removed' then tests `test ! -e ""` and passes vacuously.

## impl:night: status `done`, head `6ded0876`

### Summary

I fixed every finding the night seat was given, plus the four known items and two nits. Four commits are on verify/v6 (base 4a0b68ea, HEAD 6ded0876). They touch only files under plans/sdd/V6-closeout/coordinator/. I did not push, did not edit the ledger, and left the untracked phase3/ files alone. Each commit added only its own paths.

**Tests.** The dry harness grew from 86 to 111 cases. I added 25: G1, X1-X22, Q1 and Q2. Every case now runs only after a stub guard confirms that each stubbed tool name resolves to the harness's own stub directory. All 23 new rows that test script behaviour fail against copies of the base scripts, and so does F5 (its criterion changed, below). On base, U1 also fails when TMPDIR is a C:/ path, because every stub was bypassed and the real powershell answered.

The full run on the final code, in 3 streams, passed 110 of 111. The one failure was O1. Its check that the night never touches `docker desktop` caught the new start-of-night status query. I fixed the script rather than the check: the night now asks Docker Desktop for its status only when the engine does not answer. I then re-ran every case whose result depends on docker calls (O1, O11, O15, O29, X1, X2, X3, X22) on the final files, and all passed. The final files are byte-identical to that re-run's snapshot.

The other 102 cases last ran on a copy that differs only in that one log line of overnight-c8.sh. Harness runs were slow because the machine was saturated by other seats, at roughly 2-8 minutes per case.

**Criterion changes:**
- F5's exact phase3.sh line now reads `-timeout=45m`. It is a budget for the whole test binary and judges nothing.
- O1's log-text check follows the reworded line ("left for the C5.2 night").
- N1 gains stricter checks (keep-awake confirmed, no WARNING, disk line logged).
- O11's scenario states that an engine down at the start runs no containers.
- N14 sets NIGHT_ALLOW_LATE=1, because its 10-minute deadline is now refused without it.
- Every case runs with the night's own C5.2 chunks turned off (`C8_NIGHT1_C52=0`), except X9-X11 and X22, which test the production default.

**Other checks:** `bash -n` passes on every script, `python -m py_compile` on every .py, and plan lint (runpatterns, docmarkers) passes in a clean scratch clone. The worktree itself fails runpatterns because of untracked dist/ files that were already there.

`mkrecheck8.py` was regenerated into my scratch directory and runs; its node parse check passes. The new output names D62-D67 (it had none on base), lists docs/security.md, names the age fields to remove, and drops the nonexistent `w19c-*` directory.

**Also changed:** candidate 6's retired overnight-c6.sh and freeze-night.sh had the same one-probe engine ownership bug, so they now refuse to run.

**Process notes:**
- The scratchpad turned out to be shared with other sessions, and my first base-copy directory there was deleted by someone else. I re-created everything under unique `w22night-*` names.
- I started no Docker, Go-test or Claude Code process. I did run one `go run` for the plan lint, and read `docker desktop status` once to learn its output format.
- The only real processes the harness starts are its own K1 and K2 probes. Nothing I started is still running.

### Commits

- 9331bdc6 fix(night): judge candidate 8's re-check against d62-d67
- 0bcfbc90 fix(night): judge row 1.1.27 by budget and clean quiet scratch
- 1030189e fix(night): own only an engine we start, guard the harness stubs
- 6ded0876 docs(night): plan c5.3, c3.3 and c6.2, and recovery after a freeze

### Findings resolution

- **fixed**: #47 engine ownership from one probe can stop the owner's Docker engine
  - 1030189e, overnight-c8.sh. Whether the engine was up at the start is now decided by three probes (docker_answers). The night counts the engine as its own (cs_started=1) only when it was down at the start and `docker desktop status`, read just before the start, said stopped. It never runs `docker desktop stop` while `docker ps` lists other containers, or when that listing fails. Docker Desktop's status is read only for an engine that is not answering. The same bug in candidate 6's overnight-c6.sh and freeze-night.sh is closed by making those retired scripts refuse. Red first: X1 (audit 2's own case: ps calls 1, 3, 4 and 5 fail), X2 (status says running) and X3 (other containers running, or the listing fails) all fail on 4a0b68ea and pass here.
- **fixed**: #48 the dry harness can bypass its stubs (Windows TMPDIR splits PATH)
  - 1030189e, nightharness.sh. The scratch path T is converted with cygpath -u, and the harness refuses one that still holds a colon. stub_guard runs inside every case subshell and fails the case unless docker, gh, go, powershell, pwsh, timeout, date, sleep, claude and df all resolve to $HB. The header and README claims are now conditional on the guard. Red first: the base harness's U1 with TMPDIR set to a C:/ path fails every check (the real powershell answered); the new harness passes U1 with the same TMPDIR. G1 shows the guard refuses a shadowed stub and a PATH without the stubs.
- **fixed**: #50 mkrecheck8.py misses D62-D67
  - 9331bdc6. The generated re-check now reads the ledger's D58-D67 rows and the w18-w22 reports (w19 and w19b as report*.md; the nonexistent w19c-*/ is dropped). docs/security.md and ADR 0011 section 23 items 5-10 join COMMON's reading list. The retrieval part cites the D63/D64 whitelist, D64(1)'s plain root, D64(4) and D67(l) as accepted over-withholding (not findings), D66(e), and D62(f)'s section 2. The sessions part runs a /compact in the fork after the six slash commands (D62 forkwork). C4.5 reads fsck and eval twice and checks config-violations.json (D62 status and config). Red first: base output has 0 mentions of D62-D67; the new output names them throughout. Regenerated into scratch against 2bf29705: exit 0, 220 lines, the node parse check passes.
- **fixed**: #14 two doctor or status reads differ by age fields, so C4.5 'agree exactly' cannot hold
  - 9331bdc6. C4.5 now compares the reads after removing the named fields: status --json's data.collected_at_ms and every provenance age_ms; the text status header's 'collected <time>' and its ', <age> old' suffixes; doctor --json's '(persisted <age> ago)' clause in the delivery rollover row (doctorMetricsAge). Any other difference is a finding. The fields were checked against internal/commands/statuscollect.go, render.go and internal/cli/doctor.go at 2bf29705.
- **fixed**: #51 a trapped signal is deferred until the foreground child ends
  - 1030189e. c8-night.sh and overnight-c8.sh run every long child through fg (background, then wait), so a TERM, INT or HUP runs the trap at once and stops that child. c8-night.sh covers merged_tree, the merged-tree lint, both prefreeze.sh runs, the bundle build and overnight-c8.sh; overnight-c8.sh covers gated_cmd, step and release-check. The header and README Abort step 2 state that the child's own children may outlive it, so nightabort.ps1 is still the way to stop the whole tree. Red first: X5 and X6 kill the night while its child is still blocked and require it to have exited, with the sentinel or clone gone, before the child is released. Both fail on base (only the hang guard ended them) and pass here.
- **fixed**: #52 summary.log records the caller's go version, not the repo's
  - 1030189e and 0bcfbc90. prefreeze.sh reads `go env GOVERSION` inside the repo. The same class is fixed in overnight-c8.sh's start line (go= read in the candidate) and quiet.sh's run record (go version in the candidate). The harness go stub now answers per working directory. Red first: X19 and Q1 fail on base and pass here.
- **fixed**: #53 the win-e2e-timing timeout copy should match ci.yml
  - 1030189e. phase3.sh's win-e2e-timing now has -timeout=45m, with the rationale in its header and the README (1513-1529 s measured on candidates 5 and 6; 1800.4 s timeouts seen before). The cliwork seat's branch had no commits when I set this, so I followed audit 2 #84's proposed 45m and noted in phase3.sh and the README to compare it with ci.yml before launch. Harness F5's exact-line check was updated (a recorded criterion change); F5 fails on base.
- **fixed**: #54 RC_EST_S was never measured, and steps could run past the deadline
  - 1030189e. RC_EST_S moved to power.sh, shared by both scripts. A watchdog stops a release-check still running at the deadline plus RC_GRACE_S (30 min) and records it SKIPPED-OVERRUN: neither a pass nor a fail, not retried. On battery, release-check waits for AC until its latest start, then runs. Every other step got an end-by-deadline estimate from candidate 6's records (STEP_EST), and the Linux lanes start no container when none of them fits. Red first: X16 (watchdog), X17 (AC wait) and X18 (estimates) fail on base and pass here.
- **fixed**: #55 power queries have no time bound
  - 1030189e and 0bcfbc90. power_ps now runs under `timeout -k 10 120`. A timed-out query reads 'UNKNOWN ?' or is unreadable, which already means NOT-REFERENCE. Swept: quiet.sh's load() PowerShell and docker exec queries are bounded too. Red first: X4 and Q1 fail on base and pass here.
- **fixed**: #56 no refusal for a deadline that is too near
  - 1030189e. c8-night.sh refuses a launch less than NIGHT_MIN_AHEAD_MIN (90) minutes before the deadline unless NIGHT_ALLOW_LATE=1. It logs a WARNING when the launch time plus 4.25 h plus RC_EST_S passes the deadline. Red first: X12 fails on base and passes here; N14 now sets NIGHT_ALLOW_LATE=1.
- **fixed**: #57 no check that the keep-awake holds
  - 1030189e. c8-night.sh polls keepawake.log for 'keep-awake held' for up to 15 one-second polls, stopping early if the process ends, and logs a WARNING without it. keepawake.ps1 now reports and exits when SetThreadExecutionState returns 0. Red first: X13 fails on base and passes here; N1 checks the 'held' line.
- **fixed**: #58 no documented recovery from a refusal after the freeze
  - 6ded0876. README Abort step 7 gains 'Frozen, then refused at the bundles or host validation': move the unvalidated bundle aside, rebuild and host-validate from the candidate worktree, push verify/v6 bounded, dispatch nightly.yml, record it in the ledger, then run the overnight part alone. Step 6 points to it. This is a documentation fix, so it has no harness row.
- **fixed**: #59 quiet.sh never removes its scratch, and there is no disk precondition
  - 0bcfbc90 and 1030189e. quiet.sh removes a scratch directory it made after a pass and keeps it, named in quiet-run.txt, after a failure. c8-night.sh and overnight-c8.sh require NIGHT_MIN_FREE_GB (40 GiB) free on TMPDIR's and GOCACHE's drives (disk_free_ok in power.sh). The drive showed 54 GB free today. Red first: X14, Q1 and Q2 fail on base and pass here.
- **fixed**: #60 govulncheck offline is a VALID red in release-check
  - 1030189e. prefreeze.sh's two govulncheck patterns moved to power.sh. A red release-check whose govulncheck section matches the unreachable pattern, with no vulnerability reported, is labelled 'not a product red' and counts NOT-REFERENCE. A reported vulnerability still counts as failed. Red first: X15 fails on base and passes here.
- **fixed**: #81 row 1.1.27's ratio against cf31e01 is not like for like
  - 0bcfbc90. Per D67(j), quiet.sh's absolute_rows judges BenchmarkHookNoop_InProcess against its absolute budget only (under 3 ms/op on the candidate). paired.txt prints no ratio for it, gives PASS or OVER-BUDGET with the reason, and absolute-budget.tsv records the same. Red first: Q1 (PASS) and Q2 (OVER-BUDGET) fail on base and pass here.
- **fixed**: #65 the plan lacks the C6.2 step
  - 6ded0876. A new README section, 'After the nights', puts C6.2 after the C5.2 nights and the live re-check, owned by an inventory seat that adds c8_result and c8_evidence from phase3/c8, the C5.2 chunk evidence and rerun-c8 (D67(k)). It makes C6.4 depend on it and names C6.1's docs-only descendant (docs/release.md section 1, step 2).
- **fixed**: #79 C5.3 and C3.3 have no step or ruling in the plan
  - 6ded0876. 'After the nights' records C5.3's artifact as the replay gate's report on the frozen SHA, with fraction-of-OPT a diagnostic only (D67(i)). It records C3.3 as satisfied by the Linux container's child-race lane plus hosted nightly's race-product-child job, with Windows running devtool test-race (D67(h)).
- **fixed**: Known (i): D65(c) a C5.2 chunk on battery must wait for AC until the deadline
  - 1030189e. The current code ran a chunk NOT-REFERENCE once the AC wait budget was spent, which is wrong. Now a chunk waits for AC past the budget until its latest start (the deadline minus its estimate), then is SKIPPED and left for the next C5.2 night; it never runs on battery. power_wait_ac accepts '-' for no budget. Red first: X7 (AC returns after the budget is spent) and X8 (AC never returns) fail on base and pass here.
- **fixed**: Known (ii): README abort step 5 names the wrong directory for a re-run
  - 6ded0876. Step 5 now names the night's own evidence directory: phase3/c8/quiet*/ for candidate 8's night, or phase3/c8-rerun-<n>/quiet*/ for a re-run. Step 7's re-run abort lists step 5.
- **fixed**: Known (iii): run C5.2 chunks in night 1 when they fit
  - 1030189e. After release-check, c52_night1 runs the full list's chunks in order (C8_NIGHT1_C52, default on). Each starts only if the machine is on AC at that moment and its estimate ends by the deadline. A chunk done here counts as passed; a VALID red counts as failed; anything else is left for the C5.2 night and counted neither way. pending_c52_night lists only what is left. Harness cases cover every branch: X22 (all fit), X9 (some do not fit), X10 (battery), X11 (a failing chunk, plus an INVALID-POWER retry). The README's done-step awk now also reads phase3/c8/power.tsv.
- **fixed**: Known (iv): accept closeout/w22-* branches and drop the w15 C8_EXEMPT
  - 1030189e and 6ded0876. The precondition already checks every closeout/w* branch; it now lists them with refname:lstrip=2. The README says the w15 branches are merged through wave 22's w15carry seat (D67(a)), with C8_EXEMPT needed only if a w15-docs branch tip is not itself merged. X21 (merged w22 and w15 branches proceed with no exemption) passes.
- **fixed**: Nit: refname:short can hide a branch that shares a tag's name
  - 1030189e. Branches are now listed with refname:lstrip=2. Red first: X20 fails on base and passes here.
- **fixed**: Nit: README harness time, and O1's clone-path parse with spaces
  - 1030189e and 6ded0876. The README states the harness run time as load-dependent (2-6 h sequential) and suggests splitting it across processes. rc_clones parses a release-check clone path whole, spaces included, and is used by O1, O9 and O20; O20 now also requires both paths to be parsed.
- **fixed**: Nit: the README's docs-figure section is stale
  - 6ded0876. It cites line 721 at 2bf29705, records that the marking landed in 458b583a (D65(a)), and says only the restatement from the C5.2 night's c116-rig runs is left.

### Tests

- `TMPDIR=/tmp sh nightharness.sh <111 cases split over three processes> (frozen copy of the final scripts)`: 110 of 111 passed. O1 failed on the start-of-night `docker desktop status` query. I fixed the script, not the check, and re-ran every docker-dependent case on the final files: O1, O11, O15, X1 (4 of 4 passed) and X2, X3, X22, O29 (4 of 4 passed). The final files are byte-identical to that re-run's snapshot.
- `sh nightharness.sh <F5 X1-X20 Q1 Q2> run from a directory holding the 4a0b68ea scripts and the new harness (red first)`: 23 cases, 23 failed, as intended.
- `TMPDIR=<C:/ form> sh <4a0b68ea nightharness.sh> U1_power_read_parses, then the same with the new harness`: Base harness: FAIL (stubs bypassed, the real powershell answered). New harness: PASS.
- `python mkrecheck8.py live-rerun-c7.js <scratch>/recheck-c8.js 2bf29705c8258b69d6f0e9bf45bd173d22f981dc <scratch>/bundle`: Exit 0, 220 lines, node parse check passed. D62-D67 are cited throughout, docs/security.md is listed, w19c- appears 0 times. The base generator gives 0 mentions of D62-D67.
- `bash -n on every coordinator/*.sh; python -m py_compile on every coordinator/*.py`: All clean.
- `go run ./tools/devtool lint --only=runpatterns,docmarkers (scratch clone of HEAD with the changes)`: PASS runpatterns, PASS docmarkers. The worktree itself fails runpatterns on untracked dist/ files that were already there.

### Open issues

- The cliwork seat's ci.yml test-e2e timeout was not visible: closeout/w22-cliwork had no commits when I checked. phase3.sh follows audit 2 #84's proposed 45m, and the README and phase3.sh both say to compare the two before launch.
- RC_EST_S is still the unmeasured 3 h estimate. The watchdog bounds an overrun (SKIPPED-OVERRUN at the deadline plus 30 min), but it does not measure release-check. Timing one release-check run on AC is still worth doing.
- A signal now runs the trap at once, but grandchildren of the stopped child (go test binaries, docker exec) can outlive it. This is documented; nightabort.ps1 is still the way to stop the whole tree.
- Harness runs take hours under daytime load: roughly 2-8 minutes per case on a machine saturated by other seats.

## review:night:r1: verdict `needs-fixes`, 3 finding(s)

- **major** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:776-797 (release_check's battery wait, then the post-wait estimate re-check); nightharness.sh O21 and X17`: On battery, release-check never 'runs either way' at its latest start, although the header (lines 82-84), README Before-launch step 7 and the log line all say it does. power_wait_ac only gives up at the first one-minute poll where now >= rc_latest (deadline minus RC_EST_S). In practice that poll lands after rc_latest, so the re-check `now + RC_EST_S > deadline` that follows always fails, and release-check is recorded SKIPPED. This is the branch the plan expects on a battery night: release-check is due about 03:45 for an 08:00 deadline, rc_latest is 05:00, and 75 minutes of wait fits inside the 180-minute budget. Base 4a0b68ea started release-check right away. Its e2e windows could still have come back VALID if AC returned before them, and now C3.12's run is dropped instead. O21 passes only because the fake clock lands its polls exactly on 05:00:00, and X17 tests only the branch where AC returns.
  - Evidence: In a scratch copy of HEAD's harness I added a case equal to O21 (timeline '0 BAT', AC_WAIT_BUDGET_MIN=600) that also moves the fake clock 7 s past the minute (echo $((START+7)) > $FAKE_CLOCK). Result: FAIL. chain.log reads 'release-check: no AC by its latest start or within the budget; it runs now ... 09:00:07Z' and then 'step release-check SKIPPED: it needs about 180 min (RC_EST_S) and would end after the deadline'. Unchanged O21 PASSES in the same run, because its release-check starts at exactly 05:00:00.
  - Fix: When the wait ends because the latest start was reached, run release-check as documented. Two ways: give power_wait_ac a target of rc_latest minus one poll plus query time (for example 90 s), or skip the post-wait estimate SKIP when the wait returned for that reason while still before the deadline. Add a harness row that starts off the minute grid (clock START+7, no AC, ample budget) and checks that try 1 runs. Show it red first on HEAD.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:455-470 ('The C5.2 night' launch block) and the after-freeze re-run (Abort step 7)`: #57 was fixed for c8-night.sh only, not across the whole class. The C5.2 night, and an overnight part run alone after the freeze, start keepawake.ps1 with Start-Process and no output redirect. Nothing confirms that it printed 'keep-awake held' (or the new 'keep-awake FAILED' line). Those nights still run with an unconfirmed keep-awake, and a host sleep shows up only afterwards as INVALID-POWER. The C5.2 night is the longest AC-gated night.
  - Evidence: README line 469: `Start-Process pwsh -WindowStyle Hidden -ArgumentList @('-NoProfile', '-File', "$co/keepawake.ps1", "$e/keepawake.sentinel")`, with no -RedirectStandardOutput. No later step reads a keep-awake log. Only c8-night.sh polls keepawake.log (lines 107-116).
  - Fix: Add `-RedirectStandardOutput "$e/keepawake.log"` to both launch blocks, plus a step that checks the log for 'keep-awake held' within about 15 s before launching overnight-c8.sh (and deletes the sentinel otherwise). Alternatively, have overnight-c8.sh run alone take the sentinel and do the same check c8-night.sh does.
- **minor** `commit 6ded0876 (verify/v6 tip)`: The commit subject is 66 characters, over the 64-character rule.
  - Evidence: `git log -1 --format=%s 6ded0876 | awk '{print length}'` gives 66: 'docs(night): plan c5.3, c3.3 and c6.2, and recovery after a freeze'. The other three subjects are 56, 62 and 64 characters. None has attribution trailers, and all carry 'Refs: V6-VERIFY, C5.2'.
  - Fix: Reword the tip commit's subject to 64 characters or fewer, for example 'docs(night): plan c5.3, c3.3, c6.2 and recovery after a freeze' (63). It is the unpushed tip of verify/v6, so only that commit's message changes.


---

## Wave 22b night seat (rounds after the restart)

Branch `verify/v6`. Workflow `wf_77be3fb4-369`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:night:r1: status `done`, head `44a7d6729b5b5a4fc24a3db8b04e70069d10554f`

### Summary

Fix round 1 for seat 'night' on verify/v6 is done. HEAD is 44a7d672; nothing is pushed. All three round-1 findings are fixed and verified red-first. Two more defects that the full harness run exposed in this seat's own files are also fixed red-first.

Finding 0, major: release-check was SKIPPED on a battery night. Fixed in 8815092a. The interrupted round committed it, and I verified it this round. The estimate is no longer re-checked after the AC wait: a wait that reaches the latest start runs release-check, the lateness is logged, and only a passed deadline skips it. X23 starts the clock 7 s off the minute grid. It fails 7 checks against the 6ded0876 scripts, which have the same tree as fe76d426, and passes on HEAD. O21 and X17 still pass.

Finding 1, minor: the keep-awake was never confirmed for the C5.2 night or the after-freeze re-run. Fixed in 4918dee8: keepawake-start.ps1 confirms "keep-awake held", and the README launch block starts the night only in its success branch. 6bcb97bf sweeps the class: phase3.sh's and quiet.sh's headers, which told an operator running them alone to "hold a keep-awake (keepawake.ps1)", now name keepawake-start.ps1. 6bcb97bf also tidies Abort step 7's wording. K3 fails 13 checks on 6ded0876 and passes on HEAD.

Finding 2, minor: the 66-character subject. 6ded0876 was amended to fe76d426 (62 characters, identical tree). All 8 commits since 4a0b68ea have subjects of 64 characters or fewer, no attribution trailers, and the Refs footer.

Found during my full harness run and fixed in 44a7d672:

(a) O9 was flaky, failing 4 of 7 runs with "rm: cannot remove .../repo: Device or resource busy". Since #51 (1030189e) the trap runs at once and TERMs only the direct child, and O9 still released its blocked stub at the signal. The stub then forked inside the clone while the trap removed it. O9 now releases the stub after the night has ended, as X6 already did, and N9 does the same. Their assertions are unchanged. O9, N9, X5 and X6 now wait, under a hang guard only, until the released stub's MSYS process chain has ended. This removes the "Directory not empty" scratch litter.

(b) The trap leaked the step's power snapshot (snap's mktemp) into TMPDIR on every signal. The EXIT trap now removes a snapshot still in flight, and each removal clears its name. O9's new TMPDIR check and the new X24 fail on 4918dee8 and pass on HEAD.

Final checks on 44a7d672:
- The dry harness passes 114 of 114 with the stub guard active, in three streams, with no stray output and no /tmp litter.
- The full devtool lint passes in a clean clone at -p 2 (all 10 sub-checks, stubskips included).
- bash -n, py_compile and the PowerShell parse are all clean.
- c52derive --selftest and homeguard_test pass.
- mkrecheck8.py regenerates and parses.
- The Linux non-root gate (-race) passes test/docs 27/27 and test/guards 199/199.

On Windows, test/guards has one failure: Defender's ML detection quarantined the built qompack binary. That is environmental and needs the owner.

Process notes:
- The host went into Modern Standby from 22:17 to 08:24 after a charger cut, which suspended my first runs. I stopped only my own process trees: the 21:59 lint and my three harness streams.
- I removed only my own scratch.
- The ledger and the untracked phase3/ files are untouched.

### Commits

- 8815092a fix(night): run release-check at its latest start on battery (interrupted round 1; verified this round)
- 4918dee8 fix(night): confirm the keep-awake of a night run alone (interrupted round 1; verified this round)
- fe76d426 docs(night): plan c5.3, c3.3, c6.2 and recovery after a freeze (6ded0876 amended for the 64-char subject; identical tree f1fed07f)
- 6bcb97bf docs(night): name keepawake-start.ps1 for runs done alone
- 44a7d672 fix(night): leave nothing behind a signal, deflake harness o9

### Findings resolution

- **fixed**: review_r1 #0 (major) overnight-c8.sh release_check: on battery, release-check never runs at its latest start; the post-wait estimate re-check SKIPs it whenever the one-minute poll lands after rc_latest
  - 8815092a, from the interrupted round, verified here. The pre-wait estimate check is the start decision. After power_wait_ac, only past_deadline skips release-check. A late start is logged as 'release-check: starts <n> s after its latest start ...', and the watchdog (the deadline plus RC_GRACE_S) bounds the end. The header and README Before-launch step 7 now describe the poll lateness. Red-first row X23 (clock at START+7, no AC with a 600-minute budget, then AC 3 s after the latest start) fails 7 checks against the 6ded0876 scripts (same tree as fe76d426) under HEAD's harness. It passes on HEAD, and O21 and X17 still pass. Class sweep: the C5.2 chunk wait keeps its documented D65(c) SKIPPED at its latest start, because a chunk never runs on battery. rc_retry_blocker and the other gated waits target the deadline itself.
- **fixed**: review_r1 #1 (minor) README C5.2 night launch and the after-freeze re-run start keepawake.ps1 with no confirmation (#57 fixed only for c8-night.sh)
  - 4918dee8, from the interrupted round, verified here. keepawake-start.ps1 creates the sentinel, starts keepawake.ps1 hidden with its output in keepawake.log, and waits up to 15 polls for 'keep-awake held'. Otherwise it deletes the sentinel, stops the process it started, and exits 1. The README launch block launches only inside 'if ($LASTEXITCODE -eq 0)', and Abort step 7 reuses that block. Red-first row K3 (real pwsh with fake held, FAILED and silent keep-awake scripts, plus the README block order) fails 13 checks on the 6ded0876 scripts and passes on HEAD. Class sweep in 6bcb97bf: phase3.sh's and quiet.sh's headers told an operator running them alone to 'hold a keep-awake (keepawake.ps1)' with nothing to confirm it; both now name keepawake-start.ps1. 6bcb97bf also tidies Abort step 7's wording. c8-night.sh already checks its own keep-awake. c7-night.sh and overnight-at*.sh belong to earlier candidates and are not launched.
- **fixed**: review_r1 #2 (minor) commit 6ded0876 subject is 66 characters
  - Amended to fe76d426 'docs(night): plan c5.3, c3.3, c6.2 and recovery after a freeze' (62 characters). Its tree is identical to 6ded0876's (f1fed07ffb79), so the red-first references to 6ded0876 in 8815092a and 4918dee8 hold for fe76d426. All 8 commits from 4a0b68ea to HEAD have subjects of 64 characters or fewer, no Co-Authored-By or Claude-Session trailers, and 'Refs: V6-VERIFY, C5.2'.
- **fixed**: Found in this round's full harness run: O9_signal_removes_the_clone is flaky (4 of 7 runs failed)
  - 44a7d672. The failing run's chain.log read: 'rm: cannot remove .../repo: Device or resource busy'. Since #51 (1030189e) the trap runs at once and TERMs only recrun.sh, so the subshell, stamped.sh and the go stub outlive it, as the overnight-c8.sh header already documents. O9 still released its blocked stub at the signal, a leftover from base 4a0b68ea, where the trap waited for its child. The released stub forked inside the clone while the trap removed it. O9 now releases the stub only after the night has ended (await_end), the same order X6 already used, and N9 does the same for c8-night.sh's merged-tree clone. Their assertions are unchanged. Also fixed: a stub released after its night kept writing in the case directory, so the harness could not remove its scratch. The blocking stub now records its MSYS ancestor chain (blk_chain), and O9, N9, X5 and X6 call release_and_await, which waits for that chain to end under a 120 s hang guard and is never a verdict. Evidence: with the new order O9 passed 6 of 6 runs, plus N9, X5 and X6, and the final 114-case run passed with no litter.
- **fixed**: Found in this round's full harness run: a signal mid-step leaks the step's power snapshot temp file into TMPDIR
  - 44a7d672, overnight-c8.sh. g_sf and rc_sf (snap's mktemp) are removed only after their step, so a TERM, INT or HUP mid-step left one file in TMPDIR per signal. cleanup(), the EXIT trap, now removes a snapshot still in flight. Every normal removal clears the variable, so no name is removed twice, and mid-flow cleanup calls are safe. The header's Signals paragraph says so. Red first: O9 now runs its night with TMPDIR=$W/tmp and checks that nothing is left there, and the new case X24 does the same for a signal during a gated step (c51-win). Both fail on 4918dee8's scripts and pass on HEAD.

### Tests

- `sh nightharness.sh <all 114 cases, split round-robin over 3 processes> (TMPDIR=/tmp, worktree clean at 44a7d672)`: PASS: 38+38+38 = 114 of 114 cases, every stream exited 0, with no stray output and no /tmp litter (08:58-10:04 on 2026-10-05).
- `sh nightharness.sh K3 X23 X17 O21 at 4918dee8 (before this round's edits)`: 4 of 4 PASS.
- `sh nightharness.sh X23 K3 against a copy of the 6ded0876 scripts with HEAD's harness (red first)`: Both FAIL as intended. X23 fails 7 checks (not SKIPPED, try 1 on battery, NOT-REFERENCE, late start logged, and the three AC-after-latest-start checks). K3 fails 13 checks (held, FAILED and silent branches, plus the README block).
- `sh nightharness.sh O9_signal_removes_the_clone, repeated, at 4918dee8`: Flaky: 4 of 7 runs failed with 'the clone is removed by the trap'. The kept chain.log reads 'rm: cannot remove .../repo: Device or resource busy' and 'could NOT be removed'.
- `sh nightharness.sh O9_signal_removes_the_clone x6, then N9 X5 X6, with the new release order`: O9 passed 6 of 6, and N9, X5 and X6 passed. The litter that remained then was fixed by release_and_await: O9, N9, X5 and X6 then passed in 180 s, and the recorded chains cut at the night pid.
- `sh nightharness.sh O9 X24 against a copy of the 4918dee8 scripts with the new harness (red first), then on the worktree`: 4918dee8: both FAIL on 'nothing is left in the night's TMPDIR'. Worktree: both PASS.
- `bash -n on coordinator/*.sh and linux/*.sh; py_compile on coordinator/*.py (cfile written to scratch); PowerShell Parser.ParseFile on keepawake-start.ps1, keepawake.ps1 and nightabort.ps1`: 18 shell scripts, 13 Python files and 3 PowerShell files: all clean.
- `python c52derive.py --selftest; python homeguard_test.py`: selftest PASS; 10 tests OK.
- `python mkrecheck8.py live-rerun-c7.js <scratch>/recheck-c8.js $(git rev-parse 2bf29705) <scratch>/bundle`: Exit 0, 220 lines, and its own async-function-body node parse check passed. The output cites D62 (6 times), D63 (4), D64 (5), D65 (1), D66 (3) and D67 (7), contains w19c- 0 times, and names collected_at_ms.
- `GOFLAGS=-p=2 go run ./tools/devtool lint (full) in a clean clone of 44a7d672 at %TEMP%/w22n-lint`: exit 0. PASS: golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips (pass 1: 72 packages in 1h7m34s; pass 2: test/e2e in 24m9s), runpatterns, docmarkers, coveragefloors. In the qompack-v6 worktree itself, golangci-lint and nomagic fail on untracked, ignored dist/v6-remediation/*.go files. This is pre-existing pollution and not mine.
- `go test -p 2 -count=1 -timeout=30m ./test/docs/... ./test/guards/... (Windows, clean clone of 44a7d672), with test/guards run twice`: test/docs ok. test/guards had one FAIL, TestV1_WriteSetConfinedAcrossFullHookSequence: 'go build ... a.out.exe: Operation did not complete successfully because the file contains a virus or potentially unwanted software' (Defender, Trojan:Win32/Bearfoos.B!ml), in both runs. This is environmental: the tree differs from base 4a0b68ea only under plans/.
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out <FD>/linux-night --prefix cx-linux-w22night 44a7d672... night-fr1-docs-guards --gomaxprocs 2 -- ./test/docs/... ./test/guards/...`: Linux, non-root (uid 10001), -race: test/docs 27 pass and test/guards 199 pass, 0 failures, go_test_exit=0. Artifacts are in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-night/.

### Criterion changes

- O9_signal_removes_the_clone: the blocked stub is now released only after the signalled night has ended (await_end), not at the signal, and 'the clone is removed by the trap' is read before the release. The assertions are unchanged. Rationale: since #51 the trap runs at once, and the running child's own children outlive it, as overnight-c8.sh's header documents. Releasing the stub at the signal made the check race a forking orphan inside the clone (4 of 7 runs failed). This is the order X6 already used.
- N9_signal_releases_keepawake: same release order (await_end, then release). The assertions are unchanged.
- O9, N9, X5 and X6 release the stub through release_and_await, which waits up to 120 s (a hang guard, never a verdict) for the stub's recorded MSYS chain to end, so the case's scratch is removable. The assertions are unchanged.
- O9 now runs its night with TMPDIR=$W/tmp and gains the check 'nothing is left in the night's TMPDIR'. This is a new, stricter check, red on 4918dee8.
- New case X24_signal_mid_step_leaves_nothing_in_tmpdir: a signal during gated step c51-win leaves nothing in the night's TMPDIR. It is red on 4918dee8.
- README: the harness has 114 cases. Its claim that 'nothing outside its temporary directory is touched' now names the nights' own scratch clones, which they make under TMPDIR and remove.

### Open issues

- Windows Defender's cloud ML intermittently quarantines qompack binaries built in %TEMP% as Trojan:Win32/Bearfoos.B!ml: 11 detections since 2026-09-27, including test/e2e's qompack.exe at 10:13 and this seat's go-build outputs at 10:29, 10:38 and 10:39 today. While it does, test/guards TestV1_WriteSetConfinedAcrossFullHookSequence and any test that builds cmd/qompack fail on this host, and so would release-check on the night. The same tests pass on Linux.
- The host entered Modern Standby from 22:17 to 08:24 on 2026-10-04/05 after a Kernel-Power 105 power-source change (the charger cut). No keep-awake was held by this seat's runs, so they were suspended for about 10 h and redone in the morning.
- The qompack-v6 worktree holds untracked, ignored dist/v6-remediation/*.go files, which make 'go run ./tools/devtool lint' (golangci-lint and nomagic) fail there. Run the full lint in a clean clone, or have the coordinator clear dist/. This seat did not delete them.
- %TEMP%/tmp.QaZsJzjgqm (a harness scratch of K3 and X23, created 21:41) predates this session, probably the interrupted round's. No process uses it, and it was left in place.
- A documented residual, unchanged: after a signal, the running step's own children (go test binaries under go.exe, docker exec) outlive the trap and can keep the release-check clone from being removed. It is logged as 'could NOT be removed', and nightabort.ps1 with README Abort steps 1 and 4 remain the remedy.
- Carried from round 0: RC_EST_S (3 h) is still unmeasured; the watchdog bounds an overrun. phase3.sh's win-e2e-timing uses -timeout=45m; compare it with the cliwork seat's ci.yml test-e2e value before launch.
- The Linux non-root gate does not apply to the night seat's own files, since no Go package changed. It was run on test/docs and test/guards to give a clean guards verdict unaffected by Defender.

### Needs owner

- Windows Defender quarantines the qompack Go binary (Trojan:Win32/Bearfoos.B!ml, an ML detection; 11 times since 2026-09-27, 4 today). Tests that build cmd/qompack, test/e2e and release-check fail on this host while it does. Adding an exclusion for the build/temp paths, or submitting a false-positive report, is the owner's decision; no agent changed AV settings.

## review:night:r2: verdict `needs-fixes`, 4 finding(s)

- **minor** `plans/sdd/V6-closeout/coordinator/README.md:545-549 (The C5.2 night, Abort step 3); compare README.md:283-290 and overnight-c8.sh:403-418`: The #47 fix was not applied across the whole class. The script and candidate 8's Abort step 3 both follow #47's third clause: never run `docker desktop stop` while other containers run, and treat 'engine left running' as final. The C5.2 night's Abort step 3 is unchanged from base 4a0b68ea. It still tells the operator: 'if the last `engine started by this chain` has no `engine stopped` after it, run `docker desktop stop`'. It does not say to run `docker ps` first, and it does not mention the 'engine left running' line that container_down now logs when other containers run on the engine.
  - Evidence: `git show 4a0b68ea:plans/sdd/V6-closeout/coordinator/README.md` lines 414-418 match HEAD's 545-549 word for word. Candidate 8's step at README.md:283-290 was rewritten to 'has no `engine stopped` or `engine left running` after it, run `docker ps` first: stop the engine ... only when no container but qompack-v6-linux-verification runs'. w16b-cover/report.md:150 records that starting the engine also auto-starts the owner's supabase_* containers through their restart policies. So a C5.2 night that starts the engine gets 'engine left running: other containers run on it' at each Linux chunk's container_down. An abort of that night, followed by step 3 as written, then stops the engine with the owner's containers on it, which the script itself refuses to do (D56(g)). No harness case covers the abort docs.
  - Fix: Rewrite the C5.2 night's Abort step 3 to match candidate 8's step 3. It should treat `engine left running` as a terminal line, run `docker ps` first, stop the engine only when no container but qompack-v6-linux-verification runs, and never stop an engine logged as `engine up at start=1`, `not started by this chain` or `the owner's engine`.
- **nit** `commit messages of 8815092a and 4918dee8`: Both commits name 6ded0876 as their red-first base. That commit was amended to fe76d426 and is no longer reachable from any branch, so it can be garbage-collected.
  - Evidence: `git branch -a --contains 6ded0876` prints nothing, and `git merge-base --is-ancestor 6ded0876 HEAD` fails. fe76d426 has the same tree (f1fed07f), and I reproduced the red against it: X23 fails 7 checks and K3 fails 13 on fe76d426's scripts with HEAD's harness.
  - Fix: Optional: record in the seat report or the ledger that 6ded0876 equals fe76d426 by tree, so the red-first base stays resolvable.
- **nit** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:140,155 (i_out, ef_out); overnight-c8.sh:776,833 (rgu_f, rc_wf)`: 44a7d672 fixed the leak class 'a signal mid-step leaves a temp file in TMPDIR' only for overnight-c8.sh's power snapshots. prefreeze.sh has no trap. When c8-night.sh's on_signal sends TERM to it during the integration or e2efunc step (each up to 60-90 minutes), its mktemp output file is left in TMPDIR, and the orphaned go test keeps writing to it. rc_wf and rgu_f have the same problem, but only in sub-second windows.
  - Evidence: `grep -n 'mktemp\|trap ' prefreeze.sh` shows mktemp at :140 and :155 and no trap. The leak is small (test output) and only on an aborted night, so it is not a gate.
  - Fix: Optional: give prefreeze.sh an EXIT/TERM trap that removes i_out and ef_out, or write them under the evidence directory.
- **nit** `plans/sdd/V6-closeout/coordinator/README.md:144-150; phase3.sh:12-13`: Both still say the cliwork seat's ci.yml timeout 'was not visible' and ask for a check before launch. It is now visible and agrees.
  - Evidence: closeout/w22-cliwork b77e7ce3 sets ci.yml's test-e2e to `go test -count=1 -timeout=45m ./test/e2e`, and dd8cf7b7 gives devtool's isolated test/e2e pass the same 45m. phase3.sh:90 has -timeout=45m.
  - Fix: Optional: record that the two values match (b77e7ce3).

## fix:night:r2: status `done`, head `d11f5f1ad289135dd957d3c2885874e952bfec57`

### Summary

Fix round 2 for seat 'night', on verify/v6 (C:/Users/Quant/Documents/Programming/Projects/qompack-v6). There was one open minor finding: the C5.2 night's Abort step 3 had not received the #47 fix that candidate 8's step 3 got. It is fixed in one commit on top of 44a7d672, the round-1 head the r2 verifier checked. No commits from an earlier interrupted attempt were found: HEAD was 44a7d672 and the tree was clean apart from the untracked phase3/ files, which I left alone.

What changed:
1. plans/sdd/V6-closeout/coordinator/README.md: the C5.2 night's Abort step 3 (now lines 545-555) is rewritten to match candidate 8's step 3. A last `engine started by this chain` counts as closed when either `engine stopped` or `engine left running` follows it. An `engine left running` line is final, because the chain found other containers on the engine or could not list them; the step notes that starting the engine also starts the owner's supabase_* containers. The operator runs `docker ps` first and runs `docker desktop stop` only when no container but qompack-v6-linux-verification runs. An engine logged as `engine up at start=1`, `not started by this chain` or `the owner's engine` is never stopped (D56(g)). The harness case counts are raised from 114 to 115 (the file table, Before step 5 and the run-time sentence).
2. plans/sdd/V6-closeout/coordinator/nightharness.sh: new case X25_readme_abort_never_stops_a_shared_engine with helpers x25_stops, x25_each and x25_ps_first. It reads the real README and joins each list item or paragraph across its wrapped lines, skipping code fences. Every passage that tells the operator to run `docker desktop stop` must:
   - treat `engine left running` as final;
   - put `docker ps` before `docker desktop stop`;
   - stop only when no container but qompack-v6-linux-verification runs;
   - name all three starts the chain does not own.
   It also requires at least two such passages (candidate 8 and the C5.2 night), so it cannot pass with nothing to check. It covers every README passage of this kind, not only the two Abort steps. The header comment mentions X25 and is reflowed.

Red first: X25 was run against the unfixed README at 44a7d672 and failed five checks, all on the C5.2 step: engine-left-running final, docker ps first, only-when-no-other-container, not-started-by-this-chain and the-owner's-engine. The count check and the `engine up at start=1` check passed, since both steps already had that marker. After the fix X25 passes.

Class sweep (git grep `docker desktop stop` over HEAD):
- The only live operator instructions are README.md:287 (candidate 8) and :549 (C5.2 night), and both now agree. Only overnight-c8.sh:411 runs `docker desktop stop` from a live script, and only after the ownership probes and an others_running check. X1-X3 already cover that.
- overnight-c6.sh:42 stops without that check, but the script is retired: line 19 exits 2 before reaching it.
- w16btasks.js, w16etasks.js, wave16b.js and wave16e.js hold the old 'stop the engine' brief text. They are dispatch scripts from waves that already ran, and the w16e report already corrects them.
- The ledger and the old reports are history and not mine to edit.
- No other night (C5.3, C6.2, recovery after a freeze) has an Abort section that touches Docker; Abort headings exist only at README :226 and :534.

All checks pass at HEAD d11f5f1a:
- bash -n and sh -n on all 18 .sh files under coordinator/ and linux/, and python -m py_compile on all 13 .py files. I removed the __pycache__ that py_compile created.
- The full dry harness: 115/115 PASS in three splits, every case behind its stub guard.
- The FULL go run ./tools/devtool lint, in a scratch clone of HEAD: every check passes, including stubskips (pass 1 in 14m6s, pass 2 on test/e2e in 24m15s). The scratch clone is removed.
- go test -p 2 -count=1 ./test/docs/... on Windows: ok.
- The Linux non-root gate, now that the container is up: uid 10001, test/docs 27/0/0 and test/guards 199/0/0, go_test_exit=0.
- mkrecheck8.py regenerated into scratch against candidate 2bf29705 (full SHA) with exit 0. verify/v6 HEAD itself is refused by design because it has no ADR 0011 section 23.

Docker: I never started or stopped the engine or the container; I only ran docker ps and the gate. Evidence is in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/night-fr2/ and C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-night-fr2/.

### Commits

- d11f5f1a docs(night): spare a shared engine in the c5.2 night's abort (README.md + nightharness.sh; footer Refs: V6-VERIFY, C5.2; no attribution trailers; subject 60 chars)

### Findings resolution

- **fixed**: minor: README.md:545-549 C5.2 night Abort step 3 not brought into #47's class (still said run docker desktop stop when the last 'engine started by this chain' has no 'engine stopped'; no docker ps check; no 'engine left running')
  - Commit d11f5f1a. The step is rewritten to match candidate 8's Abort step 3. `engine left running` is a terminal line, `docker ps` comes first, and the engine is stopped only when no container but qompack-v6-linux-verification runs. It never stops an engine logged as `engine up at start=1`, `not started by this chain` or `the owner's engine` (D56(g)), and it explains that starting the engine also starts the owner's supabase_* containers. Red-first row: nightharness.sh case X25_readme_abort_never_stops_a_shared_engine. Against the unfixed README at 44a7d672 it failed five checks, all on the C5.2 step (night-fr2/red-x25-on-44a7d672.txt); after the fix it passes (night-fr2/green-x25.txt), as in the full 115-case run. X25 checks every README passage that instructs `docker desktop stop`, so a later abort section cannot drop the rule unnoticed. Sweep: the only live operator instructions are README :287 and :549, now consistent. overnight-c6.sh:42 is unreachable (the script exits 2 at line 19), and the w16*/wave16* briefs are history from waves that already ran.

### Tests

- `sh plans/sdd/V6-closeout/coordinator/nightharness.sh X25_readme_abort_never_stops_a_shared_engine  (README at 44a7d672, before the fix)`: RED as intended: 1 case, 1 failed. Five checks failed, all on the C5.2 step: engine-left-running final, docker ps first, only-when-no-other-container, not-started-by-this-chain, the-owner's-engine (night-fr2/red-x25-on-44a7d672.txt)
- `sh plans/sdd/V6-closeout/coordinator/nightharness.sh X25_readme_abort_never_stops_a_shared_engine  (after the fix)`: PASS, 1 case, 0 failed (night-fr2/green-x25.txt)
- `sh plans/sdd/V6-closeout/coordinator/nightharness.sh <all 115 cases, three splits of 39/38/38> at d11f5f1a`: 115/115 PASS, 0 failed, every case behind its stub guard. Exit 0 on all three splits (night-fr2/full-s1.txt, full-s2.txt, full-s3.txt; 13:38-14:30 local)
- `bash -n && sh -n on plans/sdd/V6-closeout/coordinator/*.sh plans/sdd/V6-closeout/linux/*.sh; python -m py_compile plans/sdd/V6-closeout/coordinator/*.py`: 31/31 ok: 18 shell scripts and 13 Python files (night-fr2/syntax.txt). The generated __pycache__ was removed.
- `go run ./tools/devtool lint  (FULL, in a scratch clone of d11f5f1a)`: exit 0. PASS on golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips (pass 1: 72 packages in 14m6s; pass 2: test/e2e in 24m15s), runpatterns, docmarkers (378 docs) and coveragefloors (night-fr2/lint-full.txt)
- `go test -p 2 -count=1 -timeout=30m ./test/docs/...  (Windows, worktree)`: ok github.com/qompack/qompack/test/docs 22.159s (night-fr2/win-docs.txt)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out .../wave22/linux-night-fr2 --prefix cx-linux-w22night-fr2 d11f5f1ad289135dd957d3c2885874e952bfec57 night-fr2-docs-guards --gomaxprocs 2 -- ./test/docs/... ./test/guards/...`: exit 0. Ran as uid=10001 qompack-test with race=1. test/docs pass=27 fail=0 skip=0; test/guards pass=199 fail=0 skip=0; go_test_exit=0 (linux-night-fr2/gate.out)
- `python -B mkrecheck8.py live-rerun-c7.js <scratch>/live-rerun-c8.js 2bf29705c8258b69d6f0e9bf45bd173d22f981dc <scratch bundle with BUNDLE.json>`: exit 0, wrote a 47016-byte live-rerun-c8.js to night-fr2/mkrecheck/. With verify/v6 HEAD as the candidate it refuses by design, because that commit has no ADR 0011 section 23.

### Criterion changes

- None weakened. Added harness case X25 (the dry harness grows from 114 to 115 cases; the README counts are updated to match).

### Open issues

- overnight-c6.sh:42 still contains an unchecked `docker desktop stop`. It is unreachable because the retired script exits 2 at line 19. I left it as a historical record, since deleting or changing retired night scripts was outside this finding.


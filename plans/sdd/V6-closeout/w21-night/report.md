# Wave 21 night: candidate 8's two nights (C3.1, D62(c))

Branch `verify/v6`. Workflow `wf_792967ff-952`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:night: status `done`, head `9ef2a13e535098349683dc237b81e8240f8c7b49`

### Root cause

The C5.2 fallback was a hand-kept subset (12 rows in 4 packages) chosen from w17-inventory's executed-file sets. It missed that internal/config/config.go runs at package init in every benchmark binary that links internal/config, so under file-level D57(e) the diff reaches about 59 of the 65 rows, and D62(b) requires the full list.

### Summary

Wave 21, night-plan seat (D62(b)(c), C5.2, C1.16). Work is on verify/v6 in C:/Users/Quant/Documents/Programming/Projects/qompack-v6, two commits on f290a461 (6b18b874, 9ef2a13e), under plans/sdd/V6-closeout/coordinator/ only. No product code changed. Nothing was run on the machine except the dry harness, plan lint, fmt-check, and test/guards plus test/docs in a clean clone. No chain, freeze, bundle, tag, push or suite was run. The ledger was not touched.

(1) The C5.2 fallback is now the full list (D62(b))
- Root cause: overnight-c8.sh's fallback (C52_FLOOR_PKGS / C52_FLOOR_FILTER) was 12 rows in 4 packages, described as "the rows the 2026-10-03 diff reaches". That was false: closeout/w20-config changes internal/config/config.go, which runs at package init in every benchmark binary that links internal/config (about 59 of the 65 rows).
- The static floor is deleted. C8_C52_SET (default `full`) chooses the set:
  - `full` measures the FULL C5.2 list: QUIET_PKGS and QUIET_BENCH_FILTER both empty, which is quiet.sh's whole benches() list by its own construction (65 rows in 19 packages).
  - `derived` keeps c52derive.py's selection (D57(e)). If the derivation cannot run, the full list is measured too.
- The header and chain.log say what is measured ("c52: the FULL C5.2 list is measured: all 65 rows of quiet.sh's benches() list, with QUIET_PKGS and QUIET_BENCH_FILTER empty").
- The full-list estimate sums every listed package plus every package in the tables: 13320 s on Windows (222 min) and 11760 s on Linux (196 min). These match the totals the header already stated.
- c8-night.sh refuses if C8_C52_ONLY is set, so a C5.2 night's switch left in the launching window cannot turn the overnight part into a C5.2 night after the freeze.

(2) Two nights (D62(c))
- Candidate 8's night (c8-night.sh, then overnight-c8.sh) ends with release-check.
  - C5.2 and the rig are not in it. chain.log logs "not in this night (D62(c)): c116-rig c52-win c52-linux". overnight-outcome.txt ends with `pending_c52_night=[c116-rig c52-win c52-linux]`. They count neither for nor against its exit status.
  - Launch by NIGHT_DEADLINE minus 8.5 h (23:30 for 08:00). This is unchanged: pre-freeze about 1 h, the steps before release-check about 3 h 15 min, release-check 3 h (RC_EST_S).
- The C5.2 night (overnight-c8.sh alone with C8_C52_ONLY=1, on the frozen SHA) runs c116-rig, then c52-win, then c52-linux, all AC-gated, into phase3/c8-c52.
  - Deadline: c52-win 3 h 42 min plus c52-linux 3 h 16 min, plus the rig's 65 min upper bound (C116_EST_S), comes to 8 h 3 min.
  - Launch by NIGHT_DEADLINE minus 8.5 h (23:30 for 08:00). c52-win must start by 04:18 and c52-linux by 04:44, or that step is SKIPPED.
- README.md's candidate 8 section now gives, for both nights, the exact PowerShell launch commands, the deadlines, what to watch and the abort steps:
  - The C5.2 night's launch reads the SHA from night.log's `candidate 8 frozen at <sha>` line, checks that qompack-cx-cand is detached and clean, starts its own keep-awake, and removes C8_C52_ONLY and NIGHT_DEADLINE afterwards.
  - It has its own six-step abort: nightabort.ps1 using chain.log's pids; the sentinel; the container (c52-linux only); quiet.sh's lock files and its `work=` scratch directory; nothing to undo in git; re-run into a fresh directory.
  - Re-running only the overnight part of candidate 8's night: launch by NIGHT_DEADLINE minus 7.5 h.
  - The live re-check waits for the C5.2 night or its disposition, as it previously waited for a SKIPPED C5.2.

How the C1.16 rig is reproduced
- How the old runs were made (w2-lifetime runs/08-17 and 36): Windows, `go test ./internal/cli/ -run '^TestSessionStartCompact_UnderSameSessionIngest$' -count=1 -v -timeout=30m` with QOMPACK_C116_ROUNDS=30, QOMPACK_C116_WORKERS=8 and QOMPACK_C116_READ_BYTES=262144, in three conditions:
  - no extra load (runs/08, 17, 36);
  - in-process co-load, QOMPACK_C116_FSYNC_COLOAD=16 and QOMPACK_C116_CPU_COLOAD=4 (runs/14, 15);
  - an external generator "w2lt-stress" (runs/09, 16), which was never committed (`git log --all -S` finds it only in a run-log header).
- phase3.sh gains a `c116-rig` step that runs the first two conditions exactly, recorded as p3-c116-rig-noextra and p3-c116-rig-coload.
- Each run must pass and must show its own `--- PASS`, its `compact SessionStart wall (n=30)` line and its `of the rig's N Reads: ... indexed` line. That last line is the routing check 3f2da1b3 added; its absence flags a stale pre-3f2da1b3 rig.
- Both lines per run go to chain.log. The numbers are recorded, not judged against a bound; compare them with runs/17 (p99 188 ms) and runs/15 (p99 278 ms).
- The step is AC-gated. An INVALID-POWER try is moved aside and retried once (harness case O34).
- It runs first in the C5.2 night, so C1.16 never waits behind C5.2.

(3) The pre-freeze e2efunc step: rows named exactly
- What ci.yml says today (integration f905ec9c): the `timing` job names no test/e2e row ("test/e2e's are covered by `test-e2e` instead"). test-e2e runs all of test/e2e alone, and that is where its timing rows are judged. The local equivalent on AC is overnight-c8.sh's win-e2e-timing, plus win-x11-alone.
- prefreeze.sh's e2efunc now skips `^(TestV3_HotPath.*|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$`, plus any test/e2e row that ci.yml's timing job names (added automatically, because phase3.sh's win-timing judges those alone on AC).
- Checked against integration's 112 test/e2e Test functions, it skips exactly 3. The old `-skip '^TestV3_HotPath'` skipped only X11.
- Drift handling:
  - If a named row is not defined in test/e2e, or ci.yml's timing lane cannot be read, e2efunc does not run (exit 2).
  - c8-night.sh runs `sh prefreeze.sh --e2e-skips <integration>` among its preconditions, refuses on drift and names the row (N21). night.log logs the -skip pattern it will use.
- DEPENDENCY on wave 21's e2e seat: its worktree had no changes when I read it.
  - If it renames or removes either row, c8-night.sh refuses before anything runs until E2E_TIMING_ROWS in prefreeze.sh is updated.
  - If it moves a test/e2e row into ci.yml's timing job, e2efunc skips it automatically.
  - If it turns either row into a purely functional row (no wall-clock judgement), it should be taken out of E2E_TIMING_ROWS so the pre-freeze gates it again. That case cannot be detected automatically.

Checks
- nightharness.sh: 80 of 80 PASS in 2447 s.
  - 14 cases updated to the new plan: O1, O3, O4, O16, O17, O22, O23, O26, O27, O29, O30, O31, N1, F4.
  - 12 new cases: O32-O34, N21, N22, F5-F7, P9, P10.
  - 13 of these fail against the scripts at f290a461, as they should: O1, O16, O26, O29, O31, O33, N21, N22, F5, F6, F7, P9, P10.
  - The scratch repository gains test/e2e rows and a ci.yml timing lane. The go stub gains the rig's output.
- `python c52derive.py --selftest`: PASS.
- `sh -n` on every changed script: OK.
- In a clean clone of HEAD 9ef2a13e:
  - `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS.
  - `go run ./tools/devtool fmt-check`: exit 0.
  - `go test -p 1 -count=1 ./test/guards ./test/docs`: ok.
- No Go files changed, so vet and golangci-lint do not apply.

Files changed: README.md, c8-night.sh, overnight-c8.sh, phase3.sh, power.sh (its NIGHT_MAX_AHEAD_H explanation is updated; the value stays 16), prefreeze.sh and nightharness.sh, all under C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator/.

### Commits

- 6b18b87459c4969bfbe619670466728ea881690c chore(v6): give c5.2 a full night and skip e2e timing pre-freeze
- 9ef2a13e535098349683dc237b81e8240f8c7b49 docs(v6): launch, deadline and abort for both candidate 8 nights

### Tests

- `cd plans/sdd/V6-closeout/coordinator && sh nightharness.sh`: 80 case(s), 0 failed (2447 s)
- `sh <f290a461 scripts>/nightharness.sh F5_e2efunc_skips_exactly_the_timing_rows F6_e2efunc_named_row_drift_does_not_run F7_e2efunc_follows_ci_yml_timing_lane P9_c116_rig_reproduces_w2_lifetime P10_c116_rig_needs_its_own_evidence O1_all_ac O16_c52_derivation_failure_measures_the_full_list O26_c52_skipped_when_it_cannot_end_by_the_deadline O29_c52_only_night O31_estimate_rechecked_after_the_ac_wait O33_unknown_c52_set_refused N21_e2e_skip_drift_refuses_before_anything N22_c52_only_switch_refuses (red on base)`: 13 case(s), 13 failed, as intended
- `python plans/sdd/V6-closeout/coordinator/c52derive.py --selftest`: selftest: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns (clean clone of HEAD 9ef2a13e)`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool fmt-check (clean clone of HEAD 9ef2a13e)`: exit 0
- `go test -p 1 -count=1 ./test/guards ./test/docs (clean clone of HEAD 9ef2a13e)`: ok test/guards 33.1s; ok test/docs 3.0s
- `sh plans/sdd/V6-closeout/coordinator/prefreeze.sh --e2e-skips C:/Users/Quant/Documents/Programming/Projects/qompack-cx-int`: ^(TestV3_HotPath.*|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$ (exit 0); against a path with no test/e2e or ci.yml it names each drift and exits 2

### Criterion changes

- Candidate 8's C5.2 set: was the c52derive.py derived set, falling back to a 12-row static floor. Now the FULL C5.2 list (C8_C52_SET=full, default), per D62(b): D57(e) is file-level, and config.go runs at package init in nearly every benchmark binary.
- Derived mode (C8_C52_SET=derived): when the derivation cannot run, it now measures the full list instead of the static floor. Reason: a file-level rule with no execution trace cannot show that any row keeps its carry.
- C5.2 moves out of candidate 8's night into a separate C5.2 night (D62(c)). overnight-outcome.txt names c116-rig, c52-win and c52-linux as pending_c52_night, and they no longer affect the night's exit status.
- The C5.2 night adds c116-rig (AC-gated, Windows). It passes when both reproduced runs pass and each logs its distribution and Read-routing line. The distributions are recorded, not bounded. Reason: D62(c); the old distributions predate 3f2da1b3.
- Pre-freeze e2efunc also skips TestE2E_SessionStartLatency and TestV5_ThrashWarningVisibleInStatusAndCheckpoint (X10), plus any test/e2e row ci.yml's timing job names. Before, it skipped only TestV3_HotPath*. Reason: these are timing rows judged in the AC timing lane (win-e2e-timing), not in the pre-freeze run. Drift in the named list fails closed.
- c8-night.sh adds two preconditions: it refuses when C8_C52_ONLY is set, and when e2efunc's skip list has drifted from integration's test/e2e or ci.yml.
- README live re-check gate: the C5.2 night must have run with failed=0, or its rows be dispositioned. This keeps the old effective rule, under which a SKIPPED C5.2 had to be re-run or dispositioned.

### Open issues

- Depends on wave 21's e2e seat. prefreeze.sh's E2E_TIMING_ROWS assumes TestE2E_SessionStartLatency and TestV5_ThrashWarningVisibleInStatusAndCheckpoint stay test/e2e timing rows judged by test-e2e (and locally by win-e2e-timing). A rename or removal makes c8-night.sh refuse at its preconditions until the list is updated. A move into ci.yml's timing job is followed automatically. If the seat makes either row purely functional, it must be taken out of E2E_TIMING_ROWS; that case cannot be detected automatically.
- In the qompack-v6 worktree itself, `devtool lint --only=runpatterns` fails ('go test -list failed') because the git-ignored dist/v6-remediation/ directory holds Go packages that do not compile. This is unrelated to these commits. The lint passes in a clean clone, which is where c8-night.sh's merged-tree check runs it.
- docs/architecture.md's figure 'p99 1.85 s to 0.66 s' comes from the external-generator condition (runs/09 and 16), which cannot be reproduced. After the C5.2 night, the docs owner must decide which reproduced condition (no-extra or in-process co-load) the docs cite.
- When I checked, two go.exe processes not started by me (started 04:06, `go test ... -skip ^TestV3_HotPath ./test/e2e`) were running; I left them alone.

### Needs owner

- C116_EST_S = 3900 s (overnight-c8.sh), the rig's end-by-deadline estimate. It is an upper bound, not a measurement: two runs at -timeout=30m plus 5 min to build the internal/cli test binary. The runs it reproduces took 18-50 s each, but no run exists with its Reads reaching the daemon (after 3f2da1b3).
- C1.16's re-measure is recorded, not bounded. The rig passes on its functional assertions (every answer is the rehydration or the deferred note; every Read reaches the rig) and logs its n=30 distributions. Whether C1.16's 'answer reliably and fast' needs a numeric pass bound (with a derivation) is an owner ruling; the compactAnswerBudget is 5 s.
- X10 (all four arms, including the purely functional detector arm) and TestE2E_SessionStartLatency leave the pre-freeze gate for win-e2e-timing on AC, after the freeze. Keeping X10's functional arms pre-freeze would need the e2e seat to split X10's late/deferred-reply branch into a row of its own.
- The C5.2 night re-runs all three steps on any re-run; partial re-runs are not supported. Whether a C5.2 night that SKIPPED c52-linux can be completed by dispositioning it instead is a coordinator ruling.

## review:night:r1: verdict `needs-fixes`, 7 finding(s)

- **major** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:48 (E2E_TIMING_ROWS), README.md:58-66 (Before launch item 4)`: The pre-freeze skip of TestE2E_SessionStartLatency contradicts what wave 21's e2e seat has already committed, so it does not 'match whatever the e2e seat decides'. The seat's result says the e2e worktree 'had no changes when I read it'. That is stale: closeout/w21-e2e had three commits at 03:17 (03f6824a, 2753bce5, d942d8a5), almost an hour before 6b18b874/9ef2a13e (04:13). The drift precondition only checks that rows exist, so it cannot catch this disagreement about which lane applies a row's budget.
  - Evidence: In 03f6824a, test/e2e/sessionstart_compact_test.go:657 says: 'Run alone, as ci.yml's test-e2e and the pre-freeze e2e and e2efunc steps run test/e2e, it is judged against scLatencyP99'. Its co-load log line at :694 names 'the pre-freeze e2e and e2efunc steps' as lanes that still apply the 1.5 s budget. Running `sh prefreeze.sh --e2e-skips qompack-cx-w21-e2e` exits 0 and prints ^(TestV3_HotPath.*|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$, so e2efunc skips that row. Adversarial harness case X5 fails for this reason. Once w21-e2e is merged (c8-night.sh requires that, or an exemption), the candidate's own test text says e2efunc gates a row that e2efunc never runs.
  - Fix: The coordinator needs to rule one way and record it. Option 1: remove TestE2E_SessionStartLatency from E2E_TIMING_ROWS, so e2efunc judges it as 03f6824a says. Option 2: have the e2e seat change sessionstart_compact_test.go:657 and :694 to name win-e2e-timing (on AC, after the freeze) and the pre-freeze `e2e` step, not e2efunc. Then rewrite the result's dependency statement and README item 4 to cite 03f6824a and the chosen reading, instead of 'no changes'.
- **major** `plans/sdd/V6-closeout/coordinator/README.md:252-258; overnight-c8.sh:660-667`: The C5.2 night's deadline assumes AC stays connected for 8 hours. Every step in the night is AC-gated, c52-win and c52-linux are single steps of 3.7 h and 3.3 h, and full mode has no AC-independent work to fill an AC gap. D57(d) records that the charger cuts AC unattended at 90-100 % and restores it at 35-40 %; candidate 6's chain was off AC for 1 h 26 min. A single cut in such a night voids a whole OS. The README's '27 min' margin covers only 'a short AC wait'.
  - Evidence: Adversarial harness cases, 22:00 launch with deadline 06:30 (the README's 8.5 h):
- X1: a 90-min AC cut one hour into c52-win makes c52-win try 1 INVALID-POWER. The retry is VALID, but c52-linux is then SKIPPED ('needs about 196 min ... would end after the deadline').
- X2: a 60-min cut inside c52-linux makes try 1 INVALID-POWER and try 2 SKIPPED.
- X4: the night starts on battery and AC returns after 2 h (ac_wait_used=120min), so c52-linux is SKIPPED.
In every case the outcome is skipped=1, so the D62(b)/(c) full list on both OSes is not met. A repeat C5.2 night carries the same exposure.
  - Fix: Make one AC cut cost at most one retryable chunk. Split c52-win and c52-linux into AC-gated sub-steps by package group, each with its own quiet.sh evidence directory: for example observer, store, checkpoint, and the other 16 packages, each about 85 min or less with its own 10 ABBA rounds. A voided chunk's retry then fits. Alternatively, make 'the charger's cutoff is disabled for the night' a stated precondition checked at launch, or add an explicit allowance of at least 1.5 h for an AC cut to the C5.2 night's launch time (deadline minus 10 h) and say so in README and power.sh.
- **minor** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh:651,660`: overnight-c8.sh treats any C8_C52_ONLY value other than exactly '1' as candidate 8's night. A launch of the C5.2 night with a mistyped switch (true, yes, ' 1') therefore runs win-timing, the Linux lanes and release-check on the frozen candidate. c8-night.sh refuses every non-empty value, but the standalone script does not.
  - Evidence: Adversarial case X3: with C8_C52_ONLY=true, chain.log shows 'release-check must start by ...', then 'release-check clone ... tag v0.3.0 only in the clone' and 'step release-check try 1 start'. The run ends rc=0, and no c116-rig or C5.2 step runs.
  - Fix: Near the C8_C52_SET check, add `case ${C8_C52_ONLY:-} in ''|1) ;; *) refuse "C8_C52_ONLY must be empty or 1, not '$C8_C52_ONLY'" ;; esac`, and add a harness case for it.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:294-297 (C5.2 night Abort step 1)`: The orphan rule tells the coordinator to look for 'the rig's cli.test.exe' whose command line names qompack-cx-cand. Go runs test binaries from its go-build temp directory with -test.* flags only, and go.exe gets the relative ./internal/cli/. Neither command line names the checkout, so after a hard abort the rule misses the rig's orphans.
  - Evidence: A live Win32_Process listing on this host shows, for example, `C:\Users\Quant\AppData\Local\Temp\go-build4111625471\b001\e2e.test.exe -test.paniconexit0 -test.count=10 -test.timeout=40m0s -test.v=true -test.run=^TestV5_...$` and `go.exe test -p 1 ... ./test/e2e`, with no repository path in either. phase3.sh's c116-rig runs `go test ./internal/cli/ -run '^TestSessionStartCompact_UnderSameSessionIngest$' ...` from the checkout's directory.
  - Fix: Identify the rig's orphans by the row name instead: go.exe whose command line has `-run ^TestSessionStartCompact_UnderSameSessionIngest$`, and cli.test.exe whose command line has `-test.run=^TestSessionStartCompact_UnderSameSessionIngest$`, created after chain.log's start time. Keep the scratch-directory rule for quiet.sh's binaries. The same limitation applies to candidate 8's Abort step 1 orphan sentence for go test binaries; widen it the same way.
- **minor** `plans/sdd/V6-closeout/coordinator/prefreeze.sh:38,48; seat result needs_owner (X10)`: Skipping X10 as a whole removes its deterministic arms from the pre-freeze gate. The seat says keeping them 'would need the e2e seat to split X10', which is not accurate. Only two of X10's four subtests can fail on an undeclared late reply, and a second go test call with a subtest -run pattern can keep the other two without changing X10. The cost is concrete: a functional X10 red would now be found only after the freeze, when the candidate is already frozen and pushed.
  - Evidence: In qompack-cx-w21-e2e, v5_x10_test.go:730-733 defines four t.Run arms. x10v5DeliverOrRecover is called with forced=false only in the full-mode arm (:756) and the degraded arm (:910). The late-reply arm passes forced=true (:836), which x10v5RecoveryLicence licenses, and the detector arm (:956) is purely functional. Commit 2753bce5 reports that X10's full-mode arm 'failed deterministically on f905ec9c and on base 738d67c7', a functional red that the old e2efunc would have caught before the freeze.
  - Fix: In e2efunc_body, add `go test -p 1 -count=1 -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^(state_aware_detector_is_progress_aware_and_cannot_feed_itself|a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh)$' ./test/e2e`, with a `--- PASS:` check per arm (as integration_body does). Alternatively, correct the needs_owner text so the owner rules on an accurate premise.
- **minor** `plans/sdd/V6-closeout/coordinator/mkrecheck8.py:121-123`: README's live re-check gate now requires the C5.2 night (README.md:319), but the header that mkrecheck8.py writes into candidate 8's live re-check still lists only overnight-outcome.txt, the dispositions and hosted CI as launch conditions. The generated script would state a weaker gate than the README.
  - Evidence: mkrecheck8.py:121: '// Launch only after the night: overnight-outcome.txt shows failed=0, every invalid, not-reference or skipped step is re-run or dispositioned in the ledger, and hosted ci.yml and nightly.yml on the candidate are green or every red is classified'. There is no clause for the C5.2 night or for pending_c52_night.
  - Fix: Add to HEADER: 'the C5.2 night (phase3/c8-c52*/overnight-outcome.txt) shows failed=0 with every non-VALID step re-run or dispositioned, or its rows are dispositioned in the ledger'. Re-run mkrecheck8.py's own checks.
- **nit** `plans/sdd/V6-closeout/coordinator/c52derive.py:31-33; README.md:212,273,288`: Three stale or inexact texts remain:
- c52derive.py's docstring still says that when every trace fails 'overnight-c8.sh measures its static floor'. The floor is deleted; the fallback is now the full list.
- README:212 says to leave out 'the `$env:C8_C52_ONLY = '1'` line', but that statement shares line 273 with `$env:NIGHT_DEADLINE = '08:00'`.
- README:288 lists c52-names.tsv under quiet-c52/c52-win/, but quiet.sh writes it one level up ($wev/c52-names.tsv).
  - Evidence: `grep -n 'static floor' c52derive.py` returns line 33. README.md:273 reads `$env:NIGHT_DEADLINE = '08:00'; $env:C8_C52_ONLY = '1'`. quiet.sh:198 sets `names="$wev/c52-names.tsv"`.
  - Fix: In c52derive.py, change the docstring to 'overnight-c8.sh measures the full C5.2 list'. In README, split line 273 into two statements, or say 'omit the `$env:C8_C52_ONLY = '1'` statement'. Move c52-names.tsv to quiet-c52/ and quiet-c52-linux/ in the Watch paragraph.

## fix:night:r1: status `done`, head `3b07b91e7f03fe1ec2761370fce3e5d5f20469a1`

### Root cause

Round 1 had two root causes the review found. The C5.2 night measured each OS's whole list (about 3.7 h and 3.3 h) as one AC-gated step, but the charger cuts AC unattended and the System log shows on-AC stretches of 1 h 26 min and 2 h 57 min in two of the last three loaded nights, so a single cut voided a whole OS and its one retry rarely fit. And round 1's e2efunc classification was checked only against an empty e2e worktree; the e2e seat's 03f6824a named e2efunc as a judge, and its ab73828a has since removed that, while the drift check only verifies that the named rows exist.

### Summary

Wave 21 night-plan seat, fix round 1 (D62(b)(c), C5.2, C1.16). All work is on verify/v6 in C:/Users/Quant/Documents/Programming/Projects/qompack-v6 under plans/sdd/V6-closeout/coordinator/. Round 1 left two commits (6b18b874, 9ef2a13e); this round adds two more (70195bc1, 3b07b91e). No product code changed. No chain, freeze, bundle, tag, push or suite was run, and the ledger was not touched. The only real Go run was one: X10's detector-arm subtest, in a clean clone of integration, to confirm its -run pattern.

**What the night plan now is**

1. **The C5.2 fallback is the full list (D62(b)), unchanged from round 1.** The 12-row static floor is gone. Its claim that it covered the rows the diff reaches was false: internal/config/config.go runs at package init in about 59 of the 65 benchmark binaries.

2. **C5.2 now runs in chunks.** This round's fix for review finding 2.
   - The C5.2 night measures the full list in eight AC-gated steps: c52-<os>-<group> for the groups observer, store, checkpoint and other.
   - "other" is every remaining package, read from quiet.sh's own benches() list. So the chunks' packages are exactly the list's 19 packages and 65 rows, and no copy of the list is kept anywhere.
   - Each chunk is one quiet.sh run with its own non-empty QUIET_PKGS, its own 10 ABBA rounds and its own directory quiet-<chunk>/. A row's base and candidate sides alternate inside its chunk, so each chunk's evidence stands on its own, whichever night measured it.
   - A chunk is refused if its QUIET_PKGS would be empty, because quiet.sh reads an empty QUIET_PKGS as its whole list.
   - Estimates (candidate 5's time for the chunk's packages, plus the existing 600 s overhead per chunk):

     | Chunk | Windows | Linux |
     |---|---|---|
     | observer | 93 min | 61 min |
     | store | 65 min | 71 min |
     | checkpoint | 52 min | 56 min |
     | other (16 packages) | 42 min | 38 min |

   - With the rig's 65-minute bound, the night needs 9 h 3 min with no AC cut. Launch by NIGHT_DEADLINE minus 9.5 h (22:30 for an 08:00 deadline).
   - Each chunk starts only if its estimate ends by the deadline. Otherwise it is SKIPPED and the next chunk is tried.
   - C8_C52_STEPS runs only the C5.2 night steps it names, always in the plan's order. So a further C5.2 night measures just the steps an earlier one voided, skipped or ran off AC. README gives the awk command that lists the steps already done (a VALID try with exit 0 in any C5.2 night on this candidate).

3. **C1.16 rig: unchanged from round 1.** It runs w2-lifetime's procedure (runs/08-17 and 36) on the frozen candidate, AC-gated, first in the C5.2 night. Its distributions are recorded, not judged against a bound.

4. **Pre-freeze e2efunc.** This round's fix for review findings 1 and 5.
   - E2E_NIGHT_ROWS lists three rows, named exactly: TestV3_HotPathUnchangedWithLedgerResident (round 1 used the prefix TestV3_HotPath*), TestE2E_SessionStartLatency and TestV5_ThrashWarningVisibleInStatusAndCheckpoint. e2efunc skips them, plus any test/e2e row ci.yml's timing job names. On integration that skip pattern is ^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$.
   - e2efunc then runs X10's one arm that judges no wall clock, by itself, with `-run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^state_aware_detector_is_progress_aware_and_cannot_feed_itself$'`. It requires that arm's own PASS line, because a -run pattern that matches nothing exits 0.
   - The real Go run in the integration clone: exactly that subtest ran and passed (1.54 s), and the other three arms did not run.
   - Drift fails closed:
     - a named row test/e2e no longer defines;
     - an arm whose t.Run name test/e2e no longer holds;
     - an arm whose row e2efunc does not skip;
     - a ci.yml timing lane prefreeze.sh cannot read.
   - In each case e2efunc does not run (exit 2), and c8-night.sh refuses at its preconditions, naming the row or arm.
   - Both integration and closeout/w21-e2e give the pattern above, exit 0.

5. **Both nights' launch, deadline and abort steps are in README.md's candidate 8 section.** It now also has an orphan listing by process name and start time, and the per-chunk evidence paths.

**Review resolution**

- **Finding 1 (major): resolved.** The e2e seat's tip has removed the contradiction, so e2efunc keeps skipping the latency row.
  - The finding was right about 03f6824a, which named "the pre-freeze e2e and e2efunc steps" as lanes that judge TestE2E_SessionStartLatency.
  - The seat's later commit ab73828a (05:21, closeout/w21-e2e tip) removed that. The latency row's judges are now scColoadJudges: "ci.yml's test-e2e and cover jobs, and devtool test, cover, ci-local and release-check". `git show HEAD:test/e2e/sessionstart_compact_test.go | grep e2efunc` there finds nothing, and the commit message cites "the pending prefreeze.sh skips this row in e2efunc".
  - So the coordinator's classification stands: timing rows are judged alone on AC after the freeze, by win-e2e-timing. README item 4 now cites both commits instead of "no changes".
  - c8-night.sh's drift check still cannot detect a disagreement about which lane judges a row. That stays a stated dependency (open issue).
- **Finding 2 (major): fixed by the chunks above.** The System log (Kernel-Power 105) confirms the exposure:
  - The last three loaded nights had on-AC stretches of 1 h 26 min, 6 h 6 min and 2 h 57 min.
  - The two cuts inside a loaded night lasted 1 h 26 min and 1 h 18 min.
  - Now one cut voids at most one chunk (93 min at most), and that chunk is retried once. A chunk that cannot run is finished by a further C5.2 night through C8_C52_STEPS, instead of the whole night being re-run.
  - README states the remaining exposure. A C5.2 night has no AC-independent step to run during a cut. After AC_WAIT_BUDGET_MIN minutes of waiting, a chunk runs on battery as NOT-REFERENCE and measures nothing. So with the charger's cutoff active, plan for a second night.
  - Harness cases: O26 (deadline packing, the next smaller chunk still runs), O29, O36 (C8_C52_STEPS, in plan order), O37 (one cut voids one chunk), O38 (a group missing from the list), O39 (an unreadable list is refused).
- **Finding 3 (minor): fixed.** overnight-c8.sh refuses any C8_C52_ONLY other than empty or exactly 1, before writing anything. It also refuses a C8_C52_STEPS name that is not a C5.2 night step, and C8_C52_STEPS without C8_C52_ONLY=1. c8-night.sh unsets C8_C52_STEPS. Harness case: O35.
- **Finding 4 (minor): fixed in both nights' Abort step 1.** The new rule lists go.exe, devtool.exe and *.test.exe by name and CreationDate after the start line's UTC time, using the PowerShell command now in README. Then each process is matched to the step that ran at the time, before Stop-Process. In the C5.2 night, the rig's go.exe and cli.test.exe carry TestSessionStartCompact_UnderSameSessionIngest on their command lines; quiet.sh's builds and binaries carry its scratch directory.
- **Finding 5 (minor): fixed; its premise was partly inaccurate.**
  - Three of X10's four arms, not two, can fail on an undeclared late reply. The late-reply arm's forced licence covers only the held prompt: v5_x10_test.go calls x10v5RecoveryLicence(forced && attempt == 0), and its comment says "a recovery prompt that is late again needs a declaration like any other".
  - So only the detector arm judges no wall clock, and only it runs pre-freeze (E2E_FUNC_ARMS), with its own PASS check.
  - Harness cases: F5 and F8 (missing PASS line, red arm run, renamed arm).
- **Finding 6 (minor): fixed.** mkrecheck8.py's header now requires that the C5.2 night or nights (phase3/c8-c52*/power.tsv) hold a VALID try with exit 0 for c116-rig and every chunk, or that those rows are dispositioned. A dry run against closeout/w19-rehydrate's tip and the c7 bundle generated and parsed (rc=0).
- **Finding 7 (nit): fixed.**
  - c52derive.py's docstring now says the full C5.2 list is measured, not the static floor.
  - README's launch block puts each $env line on its own line, and Abort step 7 says exactly which line to leave out.
  - c52-names.tsv is documented under each quiet-<chunk>/ directory.

**Dependency on wave 21's e2e seat**

prefreeze.sh assumes the three timing rows and X10's detector arm keep their names and their classification at closeout/w21-e2e ab73828a. If any of them is renamed or removed, c8-night.sh refuses until E2E_NIGHT_ROWS or E2E_FUNC_ARMS is updated. If the seat's merged text names e2efunc as a judge again, or reclassifies a row, the lists must be updated by hand before launch; no check can see that.

Files changed this round: README.md, c8-night.sh, overnight-c8.sh, prefreeze.sh, power.sh, nightharness.sh, mkrecheck8.py and c52derive.py, all under C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator/.

### Commits

- 6b18b87459c4969bfbe619670466728ea881690c chore(v6): give c5.2 a full night and skip e2e timing pre-freeze
- 9ef2a13e535098349683dc237b81e8240f8c7b49 docs(v6): launch, deadline and abort for both candidate 8 nights
- 70195bc19f6173239d63fd20e6c7d005cf77cab4 chore(v6): chunk c5.2 by package, gate x10's detector arm early
- 3b07b91e7f03fe1ec2761370fce3e5d5f20469a1 docs(v6): chunked c5.2 nights, orphan listing, re-check gate

### Tests

- `cd plans/sdd/V6-closeout/coordinator && sh nightharness.sh`: 86 case(s), 0 failed (3079 s). Cases added this round: O35-O39, F8. Cases changed: O1, O16, O17, O22, O23, O26, O27, O29, O31, O32, N1, N21, F5, F6, F7.
- `nightharness.sh (this round's version) run against the round-1 scripts at 9ef2a13e, on every changed or new case`: Red as intended. First run: 20 cases, 20 failed (O1, O16, O17, O22, O23, O26, O27, O29, O31, O32, O35-O39, N1, N21, F5, F6, F7). After the e2efunc decision was revised: F5, F6, F8, N1 and N21 again, 5 failed.
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 30m -v -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^state_aware_detector_is_progress_aware_and_cannot_feed_itself$' ./test/e2e (clean clone of integration 39dfcb75)`: PASS. Only that subtest ran (1.54 s). The output's '    --- PASS: <row>/<arm> (' line matches prefreeze.sh's check.
- `sh plans/sdd/V6-closeout/coordinator/prefreeze.sh --e2e-skips <qompack-cx-int | qompack-cx-w21-e2e>`: Both print ^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$ with exit 0.
- `go run ./tools/devtool lint --only=docmarkers,runpatterns (clean clone of HEAD with the diff applied; cmp confirmed the diff equals what was committed)`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool fmt-check (same clone)`: exit 0
- `go test -p 1 -count=1 ./test/guards ./test/docs (same clone)`: ok test/guards 44.6s; ok test/docs 5.0s
- `python plans/sdd/V6-closeout/coordinator/c52derive.py --selftest`: selftest: PASS
- `python plans/sdd/V6-closeout/coordinator/mkrecheck8.py live-rerun-c7.js <scratch>/recheck-test.js <closeout/w19-rehydrate tip> <qompack-bundles/c7 windows-amd64>`: rc=0. The generated header carries the new C5.2 night clause, and the script parses.
- `sh -n on c8-night.sh, overnight-c8.sh, prefreeze.sh, power.sh, phase3.sh, nightharness.sh`: OK

### Criterion changes

- e2efunc's skip list names X11 exactly (TestV3_HotPathUnchangedWithLedgerResident, the row win-x11-alone runs) instead of the prefix TestV3_HotPath*. Reason: rows named exactly, and a future functional row with that prefix will not be skipped silently.
- e2efunc additionally runs X10's detector arm by itself and requires its PASS line. Reason: review finding 5. It is the only X10 arm that judges no wall clock, and the late-reply arm's licence forces only the held prompt.
- TestE2E_SessionStartLatency and X10 stay skipped in e2efunc and are judged after the freeze by win-e2e-timing. This round re-confirmed the round-1 classification against the e2e seat's ab73828a, which no longer names e2efunc as a judge.
- C5.2 is measured in eight AC-gated chunks per package group (c52-<os>-<group>) instead of one step per OS. The full list is the union of the chunks, with 'other' read from quiet.sh's own list, instead of QUIET_PKGS left empty. Reason: review finding 2, where one AC cut voided a whole OS.
- C5.2 estimates are per chunk, with C52_EST_OVERHEAD_S applied per chunk. The C5.2 night needs 9 h 3 min and launches by NIGHT_DEADLINE minus 9.5 h (was 8 h 3 min and minus 8.5 h).
- A C5.2 night may run only the steps named in C8_C52_STEPS. A step is done when any C5.2 night on the candidate holds a VALID try with exit 0 for it. The live re-check gate (README and mkrecheck8.py's header) requires that for c116-rig and all eight chunks, or a disposition.
- overnight-c8.sh refuses C8_C52_ONLY other than empty or 1, a C8_C52_STEPS outside a C5.2 night or naming a non-step, and, in a C5.2 night, a quiet.sh whose benches() list it cannot read.
- pending_c52_night in candidate 8's outcome now lists the nine C5.2 night steps (c116-rig and the eight chunks).

### Open issues

- Depends on wave 21's e2e seat (closeout/w21-e2e tip ab73828a). A rename or removal of TestV3_HotPathUnchangedWithLedgerResident, TestE2E_SessionStartLatency, TestV5_ThrashWarningVisibleInStatusAndCheckpoint or X10's detector arm makes c8-night.sh refuse until the lists in prefreeze.sh are updated. If the merged text names e2efunc as a judge again, or reclassifies a row, update E2E_NIGHT_ROWS and E2E_FUNC_ARMS by hand before launch: no check can detect that.
- README.md cites 03f6824a and ab73828a. Both are on closeout/w21-e2e, not yet reachable from verify/v6, and shacheck.sh reports them NOT-IN-HEAD, as it already does for 3f2da1b3, 12817cd0, 21c07942 and c212712d. They become reachable once w21-e2e is merged unrebased and frozen.
- With the charger's cutoff active, a C5.2 night has no AC-independent step to run during a cut. A cut costs the voided chunk plus the battery stretch: about 1 h 20 min under load, longer while the night waits idle. One night may therefore leave chunks for a further C5.2 night (C8_C52_STEPS).
- The empty-QUIET_PKGS refusal in gated_cmd is a second line of defence that no harness case reaches: c52_queue never queues a chunk with no packages, which O38 covers.
- In the qompack-v6 worktree itself, `devtool lint --only=runpatterns` still fails because git-ignored dist/v6-remediation/ holds Go packages that do not compile. This is unrelated to these commits; the lint passes in a clean clone.

### Needs owner

- C52_GROUPS = observer, store, checkpoint, plus "other" (overnight-c8.sh). Derivation: these are the three longest packages in C52_EST_WIN and C52_EST_LINUX (observer 83/51 min, store 55/61, checkpoint 42/46); the other 16 take 32 and 28 min. With C52_EST_OVERHEAD_S (600 s, kept, now per chunk, bounded by candidate 5's whole-list overhead of 430 and 330 s), no chunk exceeds 93 min. The chunks sum to 252 min on Windows and 226 on Linux, against 222 and 196 unchunked.
- The C5.2 night's launch: NIGHT_DEADLINE minus 9.5 h (22:30 for 08:00), for 9 h 3 min of estimates with no AC cut. Absorbing one cut needs an earlier launch that the owner allows (NIGHT_MAX_AHEAD_H permits from 16:00).
- Whether the charger's AC cutoff can be disabled for a C5.2 night. If not, expect more than one C5.2 night.
- Whether a C5.2 night should wait for AC all night rather than run a chunk on battery (NOT-REFERENCE, which measures nothing) once AC_WAIT_BUDGET_MIN (180) is spent.
- Accepting C5.2 evidence assembled per chunk across C5.2 nights. Each chunk is a self-contained ABBA comparison of its own rows against cf31e01.
- C116_EST_S = 3900 s stays an upper bound, not a measurement (two -timeout=30m runs plus the build). No run exists with the rig's Reads reaching the daemon.
- C1.16's re-measure is recorded, not bounded. Whether 'answer reliably and fast' needs a numeric bound with a derivation is an owner ruling; compactAnswerBudget is 5 s.
- X10's full-mode, degraded and late-reply arms are judged only after the freeze, in win-e2e-timing. Running them pre-freeze would catch a functional red earlier, at the price of a 250 ms prompt-reply deadline exposure that would block the freeze.

## review:night:r2: verdict `needs-fixes`, 3 finding(s)

- **minor** `plans/sdd/V6-closeout/coordinator/README.md:266-268 and :386-388; phase3.sh:12-21 (c116-rig header); docs/architecture.md:714 on closeout/integration 67e0fddb`: The C1.16 re-run reproduces only two of the w2-lifetime conditions: no extra load (runs/17, 36) and the in-process co-load (runs/15). The figure the shipped docs actually cite, 'p99 went from 1.85 s to 0.66 s' under 'an fsync co-load', comes from runs/09 to runs/16, which used the external generator. Those runs predate 3f2da1b3, so the same-session ingest the doc describes never happened in them. The README states that this condition has no reproduction, but no step and no live re-check condition makes anyone correct or disposition that sentence. The candidate would therefore be tagged with a measured claim that D62(c) says is stale and that the C5.2 night never re-measures.
  - Evidence: - docs/architecture.md:713-715 (integration 67e0fddb): 'Measured with internal/cli's TestSessionStartCompact_UnderSameSessionIngest under concurrent same-session ingest and an fsync co-load, the compact answer's p99 went from 1.85 s to 0.66 s (plans/sdd/V6-closeout/w2-lifetime/runs/)'.
- w2-lifetime/report.md:219 maps that figure to 'Same external fsync co-load | p99 1.85 s (runs/09) | p99 661 ms (runs/16)'.
- The in-process co-load the night re-runs measured p99 278 ms (runs/15), not 661 ms, so it does not stand in for the cited figure.
- runs/09's header gives the external generator's parameters in full: 16 goroutines each writing 64 KiB with write, fsync and rename in a loop on C:, plus 4 CPU spinners, for 150 s. A reproduction is therefore possible, not impossible.
- README:266-268 says only that 'that condition has no reproduction'. The live re-check gate (README:386-388, and mkrecheck8.py's HEADER) requires c116-rig to be done but says nothing about docs/architecture.md:714.
  - Fix: Choose one of two fixes.
(a) Add a README step under 'The C5.2 night', and a condition in the live re-check list and mkrecheck8.py's HEADER: before the live re-check, docs/architecture.md:714 must be restated from p3-c116-rig-coload / p3-c116-rig-noextra on the frozen candidate, or marked as a pre-3f2da1b3 measurement, by the docs owner, with the ruling recorded in the ledger.
(b) Commit a small generator under coordinator/ that follows runs/09's header, and add a third c116-rig sub-run (p3-c116-rig-external) that starts the generator in the background for that run. Extend P9 and P10 to cover it, and add its 65-min share to C116_EST_S and to the night's 9.5 h launch figure.
- **minor** `plans/sdd/V6-closeout/coordinator/README.md:321-336 (C5.2 night launch; reused by Abort step 7 at :230-241)`: The C5.2 night is launched with Start-Process -WindowStyle Hidden and no redirect. overnight-c8.sh's refusals go only to stderr: refuse() at overnight-c8.sh:134. This round added refusal classes, among them a mistyped C8_C52_ONLY or C8_C52_STEPS, an unreadable quiet.sh list and a dirty checkout, and each of them is lost with the hidden window. The keep-awake sentinel is created before the launch, and the README releases it only 'when chain.log's done: line appears'. A refused launch never writes chain.log, so the coordinator sees no night, gets no reason, and the keep-awake holds indefinitely. As written, these steps are not executable to a clear outcome on the refusal path.
  - Evidence: - overnight-c8.sh:134: refuse() { echo "overnight-c8.sh: REFUSED: $*" >&2; exit 2; }. Every refusal at :136-201 happens before chain.log exists.
- README:321 creates $e/keepawake.sentinel and :325 starts keepawake.ps1.
- README:335-336: 'delete $e/keepawake.sentinel when chain.log's done: line appears'.
- Probe on this host: Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden -RedirectStandardError <file> -PassThru -ArgumentList '-c "echo refused-probe >&2; exit 2"' returned exit=2, and the file held 'refused-probe'. So a hidden launch can keep its stderr.
- A file named launch.err does not match overnight-c8.sh:147's records pattern ^(p3-|quiet|c52-derive|release-check|...\.invalid-power-), so a relaunch into the same $e is still allowed.
  - Fix: In the C5.2 launch block, and therefore in the Abort step 7 re-run that reuses it, add -RedirectStandardError "$e/launch.err" to the bash Start-Process. Then add one line: 'If $e/chain.log does not exist a minute after launch, overnight-c8.sh refused: read $e/launch.err, delete $e/keepawake.sentinel, fix the cause and relaunch (a refusal writes no records).' Mention launch.err in the Watch paragraph.
- **nit** `plans/sdd/V6-closeout/coordinator/README.md:365, :371 (C5.2 night Abort steps 2 and 4); :234-237 (Abort step 7 re-run)`: Three small problems.
- The C5.2 night's Abort steps 2 and 4 name phase3/c8-c52/ literally. A further C5.2 night, which the chunked design expects, runs in c8-c52-2 and later directories ($e), so following those steps as written deletes the wrong sentinel and lock.
- Abort step 7's candidate 8 re-run reuses the C5.2 block with the C8_C52_ONLY line removed, but it never clears the variable first, as candidate 8's own launch block does at :122.
- overnight-c8.sh cannot detect a leftover C8_C52_ONLY=1, because 1 is a valid value, so the re-run would silently become a C5.2 night. That would show in chain.log as mode=c52-only.
  - Evidence: - README:365 reads 'Delete `phase3/c8-c52/keepawake.sentinel`'. README:371 reads 'Remove any `phase3/c8-c52/quiet-*/.quiet.lock`'. README:315 says 'a fresh directory per night: c8-c52-2, ... after it'.
- README:122 (candidate 8 launch) clears Env:C8_C52_ONLY, Env:C8_C52_SET and Env:C8_C52_STEPS. Abort step 7 (:234-237) only says to leave out the `$env:C8_C52_ONLY = '1'` line.
  - Fix: - In the C5.2 night's Abort steps 2 and 4, write `$e/keepawake.sentinel` and `$e/quiet-*/` (the night's own evidence directory).
- In Abort step 7, start the re-run with `Remove-Item Env:C8_C52_ONLY, Env:C8_C52_SET, Env:C8_C52_STEPS -ErrorAction SilentlyContinue`, as candidate 8's launch block does.

## fix:night:r2: status `done`, head `ba38a40682973bcc90cfeba8e9ef88c56eeda50e`

### Root cause

Round 2 recorded that the C1.16 re-run could not reproduce w2-lifetime's external co-load condition, because the generator was never committed. It treated that as the end of the matter. It did not follow the figure to the shipped claim built on it (docs/architecture.md's 1.85 s to 0.66 s), so nothing gated that sentence. The 'no reproduction' premise was also wrong: the source survived in the coordinator's scratchpad. Separately, the C5.2 launch was written as a direct hidden Start-Process of overnight-c8.sh. Candidate 8's night is protected because c8-night.sh redirects its own output first, but the direct launch had no such protection, so refusals, which go only to stderr, were lost. The Abort text copied candidate 8's fixed paths, although a C5.2 night can run in more than one directory.

### Summary

Wave 21 night-plan seat, fix round 2 (D62(b)(c), C5.2, C1.16). Everything is on verify/v6 in C:/Users/Quant/Documents/Programming/Projects/qompack-v6, under plans/sdd/V6-closeout/coordinator/. Rounds 1 and 2 left four commits (6b18b874 to 3b07b91e). This round adds two: 62c6f2f5 and ba38a406.

No product code changed. I ran no chain, freeze, bundle, tag, push or suite. I did not touch the ledger, docs/architecture.md, or any file wave 19d owns. This round changed no script logic: phase3.sh got a comment edit only.

**The night plan as it now stands**

Unchanged from round 2, with this round's corrections:
- **C5.2:** the full list runs in eight AC-gated chunks on a C5.2 night (C8_C52_ONLY=1, with C8_C52_STEPS for any further night).
- **C1.16 rig:** goes first in that night, AC-gated, and runs w2-lifetime's no-extra-load and in-process co-load conditions on the frozen candidate.
- **Pre-freeze e2efunc:** skips TestV3_HotPathUnchangedWithLedgerResident, TestE2E_SessionStartLatency and TestV5_ThrashWarningVisibleInStatusAndCheckpoint by exact name. It then runs X10's detector arm alone and checks for that arm's own PASS line.
- **Launch, deadline and abort steps** for both nights are in README.md's candidate 8 section.

**Review resolution**

**Finding 1 (minor): confirmed and fixed by the reviewer's option (a). Its evidence also turned up something new.**
- What I confirmed:
  - docs/architecture.md:713-715 on closeout/integration 67e0fddb cites "p99 went from 1.85 s to 0.66 s" for "same-session ingest and an fsync co-load".
  - w2-lifetime/report.md:219 maps that figure to runs/09 and runs/16, the external generator.
  - Both runs predate 3f2da1b3.
  - The in-process co-load run (runs/15) measured 278 ms, so it cannot stand in for 661 ms.
  - No step or re-check condition made anyone correct the sentence.
- New finding: the generator was never committed, but its source still exists in the coordinator session's scratchpad (w2lt-stress/main.go, written 2026-09-25 17:27, before runs/09).
  - The provenance holds up. The program prints `writes N`, as runs/09 and 16 record. Its work directory holds 1024 files, which is 16 writers times 64 names. Rebuilt there with go1.26.4, it reproduces that session's w2lt-stress.exe byte for byte (sha256 5d49a71ec8886fb0c66f4feeadb113455ee3ad0595dfd4635133888827d9c4f1).
  - So round 1's "that condition has no reproduction" was wrong in README and in phase3.sh's header.
- Fixes:
  - **62c6f2f5** commits the source verbatim as coordinator/w2lt-stress/main.go.txt and go.mod.txt. The `.txt` suffix follows the convention plans/ already uses (for example perfstore/runs/mkdirprobe/main.go.txt), so no Go tool reads it. README's file table records the provenance and how to build it.
  - **ba38a406** adds "The docs figure" under the C5.2 night, a new condition in the live re-check list, and the same condition in mkrecheck8.py's HEADER. Before the live re-check, a docs change the coordinator dispatches must either:
    - restate the sentence from p3-c116-rig-noextra and p3-c116-rig-coload, naming their conditions and giving no before-the-fix figure; or
    - mark it as measured before 3f2da1b3.
  - Either way the ruling goes in the ledger. Marking needs no number from the night, so it can go into candidate 8 before the freeze. After the freeze, either change goes in a descendant whose changes reach no bundle (D58(e)). docs/ is in no bundle: candidate 7's BUNDLE.json lists only the plugin manifest, .mcp.json, bin/, commands/, hooks/, LICENSE and THIRD_PARTY_NOTICES.md.
  - README and phase3.sh now say the generator is committed but not run by the night.
- I did not take option (b), the third sub-run. Why:
  - Its honest bound adds 30 min to C116_EST_S, which moves the C5.2 launch to the deadline minus 10 h.
  - The original runs the generator for a fixed 150 s, and no run exists of the rig with its Reads reaching the daemon. A rig that outlasts the generator would end its distribution without co-load, which is a source of flakiness.
  - (b) also would not remove the need for (a): the 1.85 s before-the-fix figure cannot be re-measured on the candidate.
  - It is offered under needs_owner.

**Finding 2 (minor): confirmed and fixed.**
- What I confirmed:
  - overnight-c8.sh's refuse() writes only to stderr, and every refusal comes before chain.log exists.
  - The C5.2 launch used a hidden window with no redirect, and the keep-awake sentinel was created before the launch.
- What I tested on this host:
  - Start-Process -WindowStyle Hidden -RedirectStandardError gives the child its own console (a conhost.exe child), so it survives the launching PowerShell exiting.
  - A refusal written 6 s after the launcher exited landed in the file.
  - A relaunch replaces the file rather than appending to it.
- The fix:
  - The launch block now sends stderr to "$e/launch.err" (line-continued). PowerShell's parser reads the block with 0 errors.
  - Its New-Item of $e takes -Force, so relaunching into the same $e runs as written.
  - A new rule: chain.log must exist a minute after launch. Between the last refusal and chain.log's start line the script only sets traps and variables, defines functions, writes power.tsv's header and makes one PowerShell power read. If chain.log is missing, read launch.err and delete the sentinel.
    - A `REFUSED:` line means a refusal, which writes no records: relaunch into the same $e.
    - Anything else means the night stopped before its start line: relaunch into a fresh $e, because power.tsv may already be there.
  - launch.err is now listed in Watch.
  - It does not match overnight-c8.sh's records pattern, and the try snapshots never move it, because it exists before the first one.

**Finding 3 (nit): confirmed, all three parts fixed.**
- The C5.2 night's Watch and Abort steps 2 and 4 now name the night's own `$e` (phase3/c8-c52 for the first night, c8-c52-2 and so on after it).
- Abort step 7's re-run now starts with `Remove-Item Env:C8_C52_ONLY, Env:C8_C52_SET, Env:C8_C52_STEPS -ErrorAction SilentlyContinue`. It also says why: overnight-c8.sh cannot tell a leftover C8_C52_ONLY=1 from a real switch.
- Step 7 also says to check that chain.log's start line reads `release-check must start by`, not `mode=c52-only`. It applies the launch.err rule unchanged.

**Dependencies**
- **On wave 21's e2e seat:** unchanged from round 2 (closeout/w21-e2e ab73828a; see open issues).
- **New: docs/architecture.md's figure.** A docs seat must mark or restate that sentence. Marking it in integration before candidate 8's freeze is cheapest. The live re-check is now gated on it.

**Files changed this round** (all under C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator/): README.md, mkrecheck8.py, phase3.sh (comment only), and the new w2lt-stress/main.go.txt and w2lt-stress/go.mod.txt.

### Commits

- 6b18b87459c4969bfbe619670466728ea881690c chore(v6): give c5.2 a full night and skip e2e timing pre-freeze
- 9ef2a13e535098349683dc237b81e8240f8c7b49 docs(v6): launch, deadline and abort for both candidate 8 nights
- 70195bc19f6173239d63fd20e6c7d005cf77cab4 chore(v6): chunk c5.2 by package, gate x10's detector arm early
- 3b07b91e7f03fe1ec2761370fce3e5d5f20469a1 docs(v6): chunked c5.2 nights, orphan listing, re-check gate
- 62c6f2f5cc8252417a2b221b3762601cfe19fb86 chore(v6): keep the c1.16 external co-load generator
- ba38a40682973bcc90cfeba8e9ef88c56eeda50e docs(v6): gate c1.16's stale docs figure, keep c5.2 refusals

### Tests

- `cd plans/sdd/V6-closeout/coordinator && sh nightharness.sh P1_bundles_pass P2_bundle_build_failure_propagates P3_byte_difference_propagates P4_validate_rejection_propagates P5_lint_arm_middle_failure P6_gens_and_fuzz_failures P7_win_timing_needs_the_hotpath_rows P8_linux_child_declares_coload_as_nightly P9_c116_rig_reproduces_w2_lifetime P10_c116_rig_needs_its_own_evidence`: 10 case(s), 0 failed (1 min 48 s). These are the cases that run phase3.sh's arms for real. This round's only script change is a comment in phase3.sh's header. No harness case reads README.md or mkrecheck8.py, so no new branch needed a case and the full 86-case suite (51 min) was not re-run.
- `sh -n plans/sdd/V6-closeout/coordinator/phase3.sh`: OK
- `go run ./tools/devtool fmt-check (clean clone of 3b07b91e with this round's files copied in; cmp confirmed each equals the committed blob at ba38a406)`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns (same clone)`: PASS runpatterns (1519 of 1519 checkable patterns resolved), PASS docmarkers (376 plan documents)
- `go test -p 1 -count=1 ./test/guards ./test/docs (same clone)`: ok test/guards 34.7s; ok test/docs 4.2s. guards reads phase3.sh, a reference-run script.
- `python plans/sdd/V6-closeout/coordinator/mkrecheck8.py plans/sdd/V6-closeout/coordinator/live-rerun-c7.js <scratch>/recheck-test.js ee280d3ad0127d82586f4fb14f6e7f17dd0d2d93 <qompack-bundles/c7 windows-amd64>`: rc=0. The generated header carries the new docs-figure clause, and the script parses (the generator's own node check).
- `pwsh: [System.Management.Automation.Language.Parser]::ParseFile on the C5.2 launch block taken from README.md`: parse errors: 0
- `pwsh probe: Start-Process 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden -RedirectStandardError <dir>/launch.err -PassThru -ArgumentList @(<child.sh>, <dir>), launcher exits after 1.5 s`: The child had its own conhost.exe and outlived the launcher. A REFUSED line written 6 s later was in launch.err, and stdout was not. A second launch to the same file replaced it rather than appending.
- `GOWORK=off go build -o <scratch>/w2lt-rebuild/w2lt-stress.exe . (in the scratchpad's w2lt-stress/, go1.26.4)`: sha256 5d49a71ec8886fb0c66f4feeadb113455ee3ad0595dfd4635133888827d9c4f1, identical to the 2026-09-25 w2lt-stress.exe. The committed main.go.txt and go.mod.txt are byte-identical copies (sha256 6788a400... and cb552058...).
- `sh plans/sdd/V6-closeout/coordinator/shacheck.sh <v6> README.md`: NOT-IN-HEAD: 03f6824a, 12817cd0, 21c07942, 3f2da1b3, ab73828a, c212712d (all reported in round 2), plus 67e0fddb, new this round: the integration tip that the docs-figure paragraph cites.

### Criterion changes

- The live re-check gains a precondition, in README.md's 'The live re-check' list and mkrecheck8.py's HEADER. docs/architecture.md section 7's '1.85 s to 0.66 s' must be restated from the frozen candidate's c116-rig runs or marked as measured before 3f2da1b3, in the candidate or a descendant whose changes reach no bundle (D58(e)), with the ruling in the ledger. Reason: review round 2, finding 1. The figure is runs/09 and 16, both before 3f2da1b3, and the C5.2 night re-measures neither.
- README.md and phase3.sh's c116-rig header no longer say the external co-load condition 'has no reproduction'. They say its generator is committed (coordinator/w2lt-stress) and that the night does not run it. Reason: the original source was found, and it rebuilds to the exact binary runs/09 and 16 used.
- The C5.2 night's launch (reused by Abort step 7) sends stderr to $e/launch.err and requires chain.log to exist a minute after launch: a REFUSED line means relaunch into the same $e, anything else means a fresh $e. Reason: finding 2. A refusal behind the hidden window was lost, and the keep-awake held.
- The C5.2 night's Watch and Abort steps 2 and 4 name the night's own $e instead of phase3/c8-c52/. Abort step 7's re-run clears C8_C52_ONLY, C8_C52_SET and C8_C52_STEPS first and checks that the start line has no mode=c52-only. Reason: finding 3.

### Open issues

- Depends on wave 21's e2e seat (closeout/w21-e2e tip ab73828a). If TestV3_HotPathUnchangedWithLedgerResident, TestE2E_SessionStartLatency, TestV5_ThrashWarningVisibleInStatusAndCheckpoint or X10's detector arm is renamed or removed, c8-night.sh refuses until prefreeze.sh's lists are updated. If the merged text names e2efunc as a judge again, or reclassifies a row, update E2E_NIGHT_ROWS and E2E_FUNC_ARMS by hand before launch; no check can detect that.
- New dependency: docs/architecture.md section 7's '1.85 s to 0.66 s' (line 714 at closeout/integration 67e0fddb) must be marked as measured before 3f2da1b3, or restated from the C5.2 night's c116-rig runs, by a docs change the coordinator dispatches, with the ruling in the ledger. Marking it in integration before candidate 8's freeze is cheapest. After the freeze it needs a descendant whose changes reach no bundle (D58(e)). The live re-check is gated on it, in both README and mkrecheck8.py's header.
- internal/daemon/session_start_compact.go:22-31 and the rig's header (internal/cli/sessionstart_compact_load_test.go:35) also cite w2-lifetime/runs. They describe the route before the fix, the diagnosis behind it, not the shipped route, so the new gate covers only the user-facing docs sentence. I did not touch them; they are product files outside this seat.
- README.md cites 03f6824a, ab73828a and now 67e0fddb. None is reachable from verify/v6 yet, and shacheck.sh reports them NOT-IN-HEAD, as it already does for 3f2da1b3, 12817cd0, 21c07942 and c212712d. They become reachable once integration and w21-e2e are merged without a rebase and frozen.
- With the charger's cutoff active, a C5.2 night has no AC-independent step to run during a cut. One night may therefore leave chunks for a further C5.2 night (C8_C52_STEPS). Unchanged from round 2.
- The empty-QUIET_PKGS refusal in gated_cmd is a second line of defence that no harness case reaches: c52_queue never queues an empty chunk, which O38 covers. Unchanged.
- In the qompack-v6 worktree itself, `devtool lint --only=runpatterns` fails because git-ignored dist/v6-remediation/ holds Go packages that do not compile. This is unrelated; the lint passes in a clean clone. A fresh clone under the scratchpad also needs per-command `git -c core.longpaths=true` to check out, because some paths are too long.

### Needs owner

- New: should the C5.2 night also re-run w2-lifetime's third condition (runs/16) with the recovered generator, now committed as coordinator/w2lt-stress? The design would be a third c116-rig sub-run, p3-c116-rig-external: build the generator, start it with 16 writers, 4 spinners and 150 s (runs/09's header) just before the rig, record its `writes N`, and fail the sub-run's validity if the generator ended before the rig. Cost: C116_EST_S grows from 3900 s to about 5760 s, which moves the C5.2 night's launch to NIGHT_DEADLINE minus 10 h (22:00 for 08:00). The fixed 150 s window is the risk: no run exists of the rig with its Reads reaching the daemon. Without it, the docs sentence is restated from the no-extra-load and in-process conditions, or marked as stale.
- Which ruling docs/architecture.md's figure gets: marked as measured before 3f2da1b3 (possible in candidate 8 before the freeze), or restated from the C5.2 night's c116-rig runs (needs a bundle-neutral descendant, D58(e)).
- C52_GROUPS = observer, store, checkpoint plus 'other' (overnight-c8.sh). Derivation: the three longest packages in C52_EST_WIN and C52_EST_LINUX (observer 83/51 min, store 55/61, checkpoint 42/46); the other 16 take 32 and 28 min. With C52_EST_OVERHEAD_S (600 s per chunk), no chunk exceeds 93 min. Unchanged.
- The C5.2 night's launch: NIGHT_DEADLINE minus 9.5 h (22:30 for 08:00), for 9 h 3 min of estimates with no AC cut. Absorbing one cut needs an earlier launch that the owner allows. Unchanged.
- Whether the charger's AC cutoff can be disabled for a C5.2 night; if not, expect more than one C5.2 night. Unchanged.
- Whether a C5.2 night should wait for AC all night rather than run a chunk on battery (NOT-REFERENCE) once AC_WAIT_BUDGET_MIN (180) is spent. Unchanged.
- Accepting C5.2 evidence assembled per chunk across C5.2 nights. Each chunk is a self-contained ABBA comparison against cf31e01. Unchanged.
- C116_EST_S = 3900 s stays an upper bound, not a measurement (two -timeout=30m runs plus the build). Unchanged.
- C1.16's re-measure is recorded, not bounded. Whether it needs a numeric bound with a derivation is an owner ruling; compactAnswerBudget is 5 s. Unchanged.
- X10's full-mode, degraded and late-reply arms are judged only after the freeze, in win-e2e-timing. Unchanged.

## verify:night: verdict `needs-fixes`, 1 finding(s)

- **nit** `plans/sdd/V6-closeout/coordinator/README.md:225-226 (candidate 8 Abort step 5) and :247-248 (Abort step 7's last sentence)`: The abort steps for Abort step 7's post-freeze re-run are still not exact. Step 7 says the re-run's abort is 'this list with chain.log's start line (step 1) and the re-run directory's sentinel (step 2)'. It does not re-point step 5, which names the first night's directory literally ('Remove any `phase3/c8/quiet*/.quiet.lock` ...'). The re-run writes its quiet.sh evidence to phase3/c8-rerun-<n>/quiet and quiet-c51-linux. Followed as written, step 5 looks in the wrong directory. The re-run's .quiet.lock and the quiet.sh scratch directory its quiet-run.txt names on its work= line are then left behind; that directory holds clones and test binaries under %TEMP%. Round 2's nit 3 found the same hard-coded-path problem in the C5.2 night's Abort steps 2 and 4, and those steps are fixed (they now say `$e/...`). Candidate 8's own list, as reused by step 7, was not changed.
  - Evidence: README.md:225: '5. Remove any `phase3/c8/quiet*/.quiet.lock` a hard kill left behind, and the quiet.sh scratch directory each `quiet*/quiet-run.txt` names on its `work=` line.' README.md:247-248: 'Its abort is this list with chain.log's start line (step 1) and the re-run directory's sentinel (step 2).' overnight-c8.sh:329 and :333 write c51-win to "$E/quiet" and c51-linux to "$E/quiet-c51-linux", where $E is the re-run's directory (README:233-234, `phase3/c8-rerun-<n>`). quiet.sh:97 creates the lock as "$wev/.quiet.lock" inside that evidence directory.
  - Fix: In Abort step 7's last sentence, say the re-run's abort is this list with chain.log's start line (step 1), and with the re-run directory in place of phase3/c8 in steps 2 and 5 (its keepawake.sentinel, and its quiet*/.quiet.lock and quiet*/quiet-run.txt). Alternatively, write step 5 as '<evidence dir>/quiet*/...', where the evidence dir is phase3/c8 or the re-run's directory.


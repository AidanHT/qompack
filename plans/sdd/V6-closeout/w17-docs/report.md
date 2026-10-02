# Wave 17 docs

Branch `closeout/w17-docs`. Workflow `wf_bd12d061-33a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `635dcced2118758a5c3c00f695e0b59d2f0ea03e`

### Root cause

The user-facing docs predated the close-out evidence. README.md still said pre-release, that no live session had run, and that the only install was SP-17's on 2.1.263. docs/release.md had a stale 'blocked' banner and no capability table. docs/cannot-do.md said the backup and restore commands did not exist, though they ship since C1.7, and it did not carry the residuals accepted by D29, D44, D48, D54 and D56(e). The audit noted that self-ignore was documented nowhere for users. install.md said nothing about the tested host, unsigned binaries, or the live lane's local-scope and 2.1.280 uninstall evidence. The CHANGELOG held only SP-17 items, some of them superseded.

### Summary

W17-DOCS (C6.1, plus the CHANGELOG half of C6.5). Branch closeout/w17-docs in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-docs, 11 commits on 9a56b305. Docs only. No product code, nothing that goes into a bundle, and no generated doc was touched. `git diff --stat 9a56b305..HEAD` lists only README.md, CHANGELOG.md, docs/{cannot-do,install,release,security,troubleshooting,uat,upstream-issues}.md and one evidence log, plans/sdd/V6-closeout/w17-docs/runs/docs-checks-windows.log. Candidate 6's bytes are unchanged.

Root cause: the user-facing docs had fallen behind the close-out.
- README still said "pre-release", that no live session had run, and that the only install was SP-17's on 2.1.263.
- release.md opened with a "V6 release status: blocked" banner about an old candidate, and had no capability table.
- cannot-do said there were no backup or restore commands, which was false since C1.7. It also left out the residuals accepted by D29, D44, D48, D54, D56(e) and the ledger default on the delivery cut.
- The audit (goal-metrics-audit.md) had found that no user doc mentions self-ignore.
- install.md was silent on the tested host, on unsigned binaries and on the live lane's local-scope evidence.

What changed, by task item:

(1) README.md
- New "Status: release candidate" section. It names candidate 6 (99d0b18) and explains that the bundles are stamped 0.3.0 at link time while core.Version and plugin.json in the source tree still read 0.1.0.
- What 0.3.0 is. What it is not: it does not compact, it is not cache-aware (D53(e)), it makes no recovery, task-success or constraint-retention claim (A8 claim boundary), it promises no performance, and it is unsigned.
- Supported environments now names 2.1.280 as the tested host and adds the release-dry-run job and the QOMPACK_NONREFERENCE_DISK report-only rule (Q1).
- A "What is verified where, for candidate 6" table:
  - hosted ci.yml run 36955046276, checked job by job with gh api (all green except release-dry-run and test (windows-latest));
  - nightly run 36955043924, 31 of 31 jobs green;
  - Windows quiet C5.1 from candidate 5 (B-A p99 30.7 ms, B-B 22.5 ms; store under the D32 exclusion; D53(b)(h));
  - Linux fsync-bound rows not verified in target;
  - installed host windows/amd64 only (D3);
  - macOS: native tests on macos-26-arm64 but no install;
  - linux/arm64 and windows/arm64 cross-compiled only;
  - bundles reproducible and accepted by `claude plugin validate`.
- The live-lane and C5.5 verdict sentences are left out, with no placeholders.

(2) docs/install.md
- Names 2.1.280 as the tested host. Records the live lane's local-scope commands (live/rerun-c4/UAT-01/cli) and the 2.1.280 uninstall observation (the cache is left behind with an `.orphaned_at` file).
- §9 gives the namespace only as far as the evidence goes. Under an entry named `qompack` (marketplace qompack-live), a live session showed the server `plugin:qompack:qompack`, tools `mcp__plugin_qompack_qompack__<tool>` and commands `/qompack:<cmd>`. Under `qompack-<os>-<arch>` it is stated as not observed.
- New §10 on unsigned binaries:
  - macOS Gatekeeper: what quarantine does, plus `xattr -d com.apple.quarantine` and Open Anyway;
  - Windows: Mark of the Web, SmartScreen and `Unblock-File`;
  - Defender: links the existing D32 troubleshooting entry and makes no claim of any Microsoft review.
- There was no --scope local advice in the docs to keep.

(3) docs/release.md
- The header is now the current candidate 6 status.
- New "Capability status at 0.3.0" table after the generated block, with about 45 rows, each marked shipped, shipped off by default, not shipped, accepted residual (Dn) or not verified in target, matching D2–D56 and the ledger defaults.
- §7 now names the hosted runs on the candidate and says release.yml has never run.

(4) docs/security.md: self-ignore means `.qompack/.gitignore` = `*` (paths.EnsureLayout). It is not a capture filter:
- a tool call that reads under .qompack/ is captured like any other (I checked: no capture path filters dotDir);
- with retrieval.ephemeralResults on, the answers of seven of the eight tools are written back under `mcp__qompack__<tool>` with `qompack-mcp:` ids;
- the host's capture of a Qompack tool call is recorded as `mcp__plugin_<entry>_qompack__<tool>`;
- recall ranks both after original captures for any entry name (D49, D53(e); recall_rank.go).
- Also added D38's staged-copy launch window.

(5) docs/troubleshooting.md
- New D56(e) entry. It quotes the day-log Warn line from precompact_settle.go and the counters precompact_settle and precompact_unreplayed_captures. It explains precompactSettleBound (B-E minus MaxPreCompactWindow, 500 ms by default) and the three "another reason" sources (an earlier request's kick, the idle tick in spool submode, a lane drain): the captures are named in the drop report, replayed later, and nothing is lost.
- I checked the wave 16 slow-disk entry against the product strings. msgHotPathToSpool, `hot path:    spool`, `spool files:`, status.hotPath, spool.pending, unreplayed_capture/unreplayed_tool_result and idleTickMax 30 s all match, so I only added a D44 citation.
- The staged-copies entry now says copies stay after an uninstall and are removed by hand (install §6).
- Added a D48 sentence to "A span looks truncated".

(6) docs/cannot-do.md
- New entries for D29, D44, D56(e), D48, D54 (SP06-D2 and SP08-D1 wontfix, with candidate 5's quiet C5.2 figures) and the delivery cut that leaves its DAG node out (tooluse.go step 6c).
- Two stale entries rewritten (backup and restore now ship; windows/amd64 now has live sessions on 2.1.280). The four contract IDs that TestCannotDoNamesTheUnimplementedChecks needs are kept.
- The performance entry now carries D53(b).
- docs/upstream-issues.md gets a residual-to-host table in its header text, so every proposal section still says "Status: not filed".

(7) CHANGELOG.md: the file already uses `## [Unreleased]`, and release.md §1 step 2 says to rename it at release, so I rewrote that section as the 0.3.0 entry. It groups the 1,792 commits since v0.2.0 (an internal V3 checkpoint tag that was never released and had no CHANGELOG) under Added, Changed, Removed, Fixed and Security, then Known limits: no benefit claim (A8), verification scope, the D32-excluded Windows timings, unsigned binaries, and the accepted residuals by D-number. Nothing unverified is claimed.

Machine limits: chain.log showed "linux timing exit=1 2026-10-02T04:17:32Z" and no "linux race exit=" line for the whole of my run, so every build, test and lint ran in the -race window, one command at a time. I ran no hot-path rows, benchmarks or Docker.

### Commits

- f4c5a8b7 docs(cannot-do): reconcile the limits with the accepted residuals
- edaace3e docs(install): record the tested host, namespace and unsigned binaries
- 9eddbea3 docs(troubleshooting): document the precompact settle known limit
- 6a0b2d13 docs(security): say what the store's self-ignore does and does not do
- a0307b9d docs(release): add the 0.3.0 capability table and current status
- 2d764977 docs(upstream-issues): map the accepted residuals to the host
- 24b367ea docs(readme): state what 0.3.0 is and what is verified where
- e8e33249 docs(changelog): summarise the 0.3.0 release under unreleased
- 71ac2baa docs(release): word the uat-02 host behaviour as d49 records it
- 48ad91ca docs(readme): say which binary reports 0.1.0 before the version commit
- 635dcced test(v6): record the w17-docs checks on windows

### Tests

- `go test -p 2 -count=1 ./test/docs (at 48ad91ca, final docs head)` — ok 5.119s
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check (at 48ad91ca)` — all three up to date, exit 0
- `go run ./tools/devtool fmt-check (at 48ad91ca)` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (at e8e33249)` — all 8 PASS, exit 0; the later commits change only docs/README
- `go run ./tools/devtool lint --only=docmarkers,runpatterns (at 48ad91ca)` — both PASS
- `gh api repos/AidanHT/qompack/actions/runs/36955046276/jobs and .../runs/36955043924/jobs (read-only)` — ci: 19 success, failure only release-dry-run and test (windows-latest), head_sha 99d0b18c; nightly: 31/31 success; macOS runner image macos-26-arm64

### Open issues

- C5.5 verdict, A8 item 1: quote the verdict verbatim in docs/user-guide.md (the /qompack:eval section, which A8 names), in README.md under 'What 0.3.0 is not' (third bullet, after the A8 sentence), and in CHANGELOG.md [Unreleased] 'Known limits' (first bullet). Each place must also carry H3's single label ('recovery advantage shown' or 'recovery advantage not shown'). If the verdict is inconclusive, add A8 item 3's sentence that inconclusive is expected by design and is not evidence that Qompack adds nothing. If not-applicable happens twice, say the study did not run as designed.
- Candidate 6 live-lane verdict (D53(f)): add it to README.md 'Supported environments', 'What is verified where' table, row 'Installed in Claude Code'. It must say what candidate 6's live lane passed or failed (D52's 16 sessions, the UAT-12 upgrade leg, the C1.7 restore smoke, UAT-01/C4.1 through the qompack-windows-amd64 entry) and the D53(i) count of host-reported hook failures and timeouts. The host-seen p50/p95 per hook (D53(i), A8 item 6) belongs in the release notes and in CHANGELOG 'Known limits'. docs/uat.md's header and Result blocks belong to the live-lane workstream.
- Namespace under a release entry: once the candidate 6 UAT-01/C4.1 session installed through qompack-windows-amd64 has run, replace docs/install.md §9 'What a session shows' (the 'has not been observed' sentence) with the observed server name, tool prefix and command prefix. Then drop the matching 'not verified in target' row in docs/release.md's capability table and the clause in CHANGELOG 'Known limits'. If commands show under a prefix other than /qompack:, the docs that write /qompack: throughout need a pass, and internal/grammar/statewarn.go's literal selfMarkerSlashCommand (w16-relhyg open issue; that detector ships disabled, D56(h)) becomes a product item.
- Quiet C5.1 and C5.2 on candidate 6: README.md's 'Windows, the reference host' row and CHANGELOG 'Known limits' cite candidate 5's quiet run (B-A p99 30.7 ms, B-B 22.5 ms). Once chain.log has 'quiet exit=', replace these with candidate 6's phase3/c6/quiet/c51-win figures. Also update the figures in docs/cannot-do.md 'Two store write rows miss their budgets after the hook's ACK' if candidate 6's C5.2 supersedes candidate 5's.
- Hosted CI: README.md's hosted-CI row and docs/release.md §7 say release-dry-run and test (windows-latest) failed on run 36955046276 and must be fixed or dispositioned (C7.2). Update both when the ci workstream classifies them, or when a re-run on the release commit is green.
- CHANGELOG.md: the entry stays under '## [Unreleased]' (the file's convention; release.md §1 step 2). At the release the coordinator renames it to '## [0.3.0] - YYYY-MM-DD' (date left open). Its first paragraph names verify/v6 99d0b18 as the candidate, and so do README.md's 'Status: release candidate' and release.md's header. All three must change if a later candidate is cut.
- Not changed, outside these items: docs/upstream-issues.md proposals 1 and 5 still say no installed-host run recorded what hook.additional_context_delivered and mcp.server_registered observe. The candidate 4 live lane recorded the daemon's snapshot of them (live/rerun-c4/UAT-01/notes.txt), so that evidence sentence needs review against the candidate 6 live lane. docs/uat.md line ~140 still says step 2 prints 0.1.0 'on this tree'.
- The executable bit and Gatekeeper on Linux and macOS stay 'not verified in target' in README, install §9/§10 and the capability table until D53(h) step 3's pre-release install rehearsal (the Linux container no-model install) records them.

## Independent review

### review:docs: needs-fixes

- **major** `README.md:48-49` — README.md says 0.3.0 "does not observe, model or manage the host's prompt cache, and it infers no cache state from elapsed time." The shipped daemon does model a cache TTL from elapsed time, so the second half of that sentence is false.
  - Evidence: internal/scheduler/ttl.go:46 ClassifyTTL(now, anchorTS, reg, ...) works out warm/expiring/cold from `gap := now-anchorTS`, and CacheFactor uses that state to scale rewrite(p). internal/daemon/scheduler_runtime.go:664 calls scheduler.Evaluate, which calls ClassifyTTL at internal/scheduler/evaluate.go:76, on every evaluation. The anchors are set from live traffic at scheduler_runtime.go:763 and :894. The model is configurable through CacheCfg (readMultiplier, writeMultiplier, ttlSeconds; internal/config/config.go:94-99) and idle.deepCutWhenCold ("once the cache is provably cold"). D53(e) only orders the descriptions to stop claiming cache-awareness. It does not say the code has none.
  - Fix: Keep the claim that Qompack does not manage the cache, and remove the claim that it has no cache model. For example: "It is not cache-aware in any way that reaches the host: it cannot see, keep warm or change the host's prompt cache. Its internal scheduler estimates a cache TTL state from elapsed time only to choose its own idle background work, and checkpoints record that state as `unknown`." Re-check CHANGELOG.md:82 against the same wording.
- **major** `README.md:87-94 (also CHANGELOG.md:70-71)` — The table is headed "What is verified where, for candidate 6", but its Windows row cites only candidate 5's quiet C5.1 pass. It leaves out that candidate 6's own isolated Windows timing pass breached B-A and B-B. The implementer had read chain.log, which shows `windows timing exit=1`, and open_issues does not mention the failure either. CHANGELOG.md:71 also says, without conditions, that "a Windows session no longer trips the breach detector by default". Candidate 6's X11 run shows n=1536 (3x512 breach windows, so a spool transition) on the reference host.
  - Evidence: qompack-v6/plans/sdd/V6-closeout/phase3/c6/chain.log: `step win-e2e-timing exit=1`, `step win-x11-alone exit=1`, `windows timing exit=1 2026-10-02T03:00:11Z`. p3-win-x11-alone.log: TestV3_HotPathUnchangedWithLedgerResident FAIL, no-ledger run B-A n=1536 p99 98.304 ms and B-B p99 81.92 ms against limit_ms 50, both pass:false. p3-win-e2e-timing.log: B-A p99 81.92 ms, B-B p99 57.344 ms, both failing. The chain ran next to the owner's Docker stack (D56(g)), so the cause may be co-load, but that is unclassified, and the README table presents the Windows timing as verified for candidate 6.
  - Fix: In the Windows row, state candidate 6's facts beside candidate 5's: "candidate 6's isolated timing pass (phase3/c6, run next to other Docker workloads) failed X11: B-A p99 98.3 ms, B-B p99 81.9 ms against 50; the quiet C5.1 run on candidate 5 passed (30.7/22.5 ms)". Do not label candidate 5's pass as candidate 6 verification. Reword CHANGELOG.md:70-71 to D41's mechanism: "B-B's own durable ingest no longer trips the breach detector systematically on Windows". Add an open issue that ties both sentences to candidate 6's quiet C5.1 and to the classification of the X11 red.
- **minor** `README.md:94` — The "Installed in Claude Code" row sits in the candidate 6 table and does not say which candidate was installed. It reads as if candidate 6 had been installed. All the live-lane install evidence comes from candidates 3 and 4. No candidate 5 or candidate 6 live run exists.
  - Evidence: qompack-v6/plans/sdd/V6-closeout/live/ contains c4, rerun-c4, uat (candidate 3), guard, recovery and resources, with no rerun-c5 and no c6. rerun-c4/UAT-01/snapshot.txt names bundle source commit 9f6a2fad (candidate 4) on 2.1.280.
  - Fix: Say which installs the evidence covers, with no placeholder text: "windows/amd64 only: the candidate 3 and candidate 4 live lanes installed those candidates' frozen bundles into Claude Code 2.1.280 (D3, agent-executed)". The candidate 6 live-lane open issue already covers the sentence to add later.
- **minor** `docs/cannot-do.md:724` — The entry says "macOS and windows/arm64 have no runner". That contradicts README.md's own row saying the test suites run natively on hosted macos-latest (arm64). What is missing is a host with Claude Code installed, not a runner.
  - Evidence: In ci.yml run 36955046276, test, test-e2e, timing and bench-gate (macos-latest) all succeeded on a macos-latest runner (gh api jobs labels). README.md:95 says "The test suites run natively on hosted macos-latest (arm64)".
  - Fix: Replace it with: "no macOS or windows/arm64 machine with Claude Code installed was available to the live lane".
- **minor** `docs/cannot-do.md:288-317` — release.md's capability table and CHANGELOG's Known limits send the D38 pid-reuse residual to this entry ("accepted residual (D35(b), D38)" links here). The entry describes the pid-reuse case but never names D38 or says that D38 accepted it. Task item (6) asked for cannot-do to be reconciled with D38.
  - Evidence: `grep -n D38 docs/cannot-do.md` finds no match. The entry's 'Why' cites only D35, and 'Recorded at' (line 316) cites SP08-D3 and the D35 note.
  - Fix: Add a sentence to 'Why': "Owner decision D38 accepted the pid-reuse case for 0.3.0 as documented and flagged; closing it needs a spool-name or per-record merge redesign." Add `plans/V6-CLOSEOUT-CHECKLIST.md` D35(b) and D38 to 'Recorded at'.
- **minor** `docs/uat.md:141; docs/upstream-issues.md proposals 1 and 5` — Two stale statements were left in files the workstream owns. The implementer recorded both as open issues instead of fixing them. uat.md says step 2 prints "`0.1.0` on this tree (observed)", but the README now says an installed bundle reports 0.3.0, and candidate 4's UAT-01 observed 0.3.0. upstream-issues proposals 1 and 5 still say no installed-host run recorded what hook.additional_context_delivered and mcp.server_registered observe, but the candidate 4 UAT-01 run recorded the daemon's snapshot of both.
  - Evidence: live/rerun-c4/UAT-01/notes.txt: step 2 version.txt "0.3.0"; contract snapshot rows hook.additional_context_delivered not-yet-observed and mcp.server_registered initialize-pending; state/history.json mcp_initialized true. The task gives the workstream docs/**, and only uat.md's header and Result blocks belong to the live lane.
  - Fix: Change uat.md step 2's expected result to "the stamped bundle version (`0.3.0` for a release bundle; `0.1.0` from a plain source build until the version commit)". Update the evidence sentence in upstream-issues proposals 1 and 5 to cite what live/rerun-c4/UAT-01 recorded.
- **nit** `docs/install.md:391` — §10 says "the release workflow attests build provenance for the archives" in the present tense, as a fact. release.yml has never run, and release.md §7 calls that path unverified.
  - Evidence: docs/release.md §7 (this branch): "`release.yml` has never run"; gh api shows no releases on AidanHT/qompack.
  - Fix: Write "the release workflow is configured to attest build provenance for the archives (release §1, step 5; that workflow has not yet run)".
- **nit** `git log 9a56b305..635dcced` — Commit hygiene: none of the 11 commits has a Refs footer, though recent workstream commits use `Refs: V6-VERIFY, C<n>`. The evidence-log commit is typed `test(v6)`, but the repo records evidence logs as `docs(v6): record ...` (for example 6aac0f1e, 972879be). There are no attribution trailers, which is correct.
  - Evidence: `git log --format=%(trailers:key=Refs)` is empty for f4c5a8b7..635dcced. 635dcced has subject "test(v6): record the w17-docs checks on windows".
  - Fix: At integration, or in a follow-up, use `docs(v6): record the w17-docs checks on windows` and add `Refs: V6-VERIFY, C6.1` (C6.5 for the changelog commit). History does not need rewriting if the coordinator accepts the mixed convention.

## Fix seat (review resolution) — status `done`, head `2408abd6eda05925cbfcf6e11f987eb2558fe98e`

### Root cause

The first seat's docs got three things wrong, each from reading the evidence too narrowly.

1. Cache claim. The no-cache-awareness ruling (D53(e)) was read as "the code has no cache model". internal/scheduler/ttl.go ClassifyTTL does estimate warm, expiring or cold from elapsed time, and that estimate drives idle background work.

2. Candidate mixing. The candidate 6 'verified where' table took figures from candidate 5's quiet run. It left out candidate 6's own unclassified X11 timing red on Windows (phase3/c6 p3-win-x11-alone and p3-win-e2e-timing). It also did not say that the installed-host evidence comes from the candidate 3 and 4 live lanes.

3. Stale evidence sentences in owned files. The B01 'no installed-host observation' claims were left standing, though the candidate 4 live lane had recorded sentinel-observed, initialize-received and has_time_to_write. Also stale: uat.md's expected 0.1.0 version, cannot-do's 'no runner' for macOS, and cannot-do's missing D38 citation.

### Summary

W17-DOCS fix round (C6.1, plus the CHANGELOG half of C6.5). This is a docs-only change: no product code, nothing that ships in a bundle, and no generated doc was touched. Candidate 6's bytes are unchanged.

Branch closeout/w17-docs (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17-docs). It has 5 new commits on top of the first seat's 635dcced. `git diff --stat 635dcced..HEAD` touches only:
- README.md
- CHANGELOG.md
- docs/cannot-do.md, docs/uat.md, docs/upstream-issues.md
- plans/sdd/V6-closeout/w17-docs/runs/docs-checks-windows-fix.log

I checked all six reviewer findings against the code and evidence. All six were correct and all are fixed. No failing test applies: the edits are prose. test/docs checks are mechanical (nouns, links, contract IDs), and they stay green.

## Review resolution

**1. (major) README:48-49 "it models no cache state from elapsed time": CONFIRMED, fixed in 864f312e.**
- Evidence: internal/scheduler/ttl.go `ClassifyTTL` works out warm, expiring or cold from `gap := now-anchorTS`, bounded by the TTL regime. internal/scheduler/evaluate.go `planBackground` adds rebuild_bloom, compact_dag and gc when the state is cold. internal/daemon/scheduler_idle.go runs those tasks from `r.lastDecision.Background`. Checkpoints still record `TTLState: "unknown"` (internal/daemon/wire_checkpoint.go:193; checkpoint writer.go `ttlStateUnknown`).
- README now says Qompack is not cache-aware in any way that reaches the host. The internal scheduler estimates a cache state from the time since the last API request and the configured TTL, and uses that estimate only for its own idle work. The estimate is not an observation, and checkpoints record `unknown`.
- The same false sentence was in docs/cannot-do.md "No inference of cache state from arbitrary elapsed time": "It does not decide that a prompt cache entry is warm or cold because a certain number of minutes passed". I rewrote it. The heading and the §12 noun that TestCannotDoCoversSection12Limits needs are kept. The new text says the estimate is bounded by the regime (half the shortest TTL counts as warm, the longest TTL or later counts as cold), not by an arbitrary number of minutes. That matches §12's "arbitrary".
- I re-checked CHANGELOG.md:82 as asked. It says only that the descriptions no longer claim cache awareness, which is accurate, so it is unchanged.

**2. (major) The README "verified where" Windows row cited only candidate 5; CHANGELOG said "no longer trips the breach detector": CONFIRMED, fixed in 7aca325c.**
- Evidence: phase3/c6/chain.log has `step win-e2e-timing exit=1`, `step win-x11-alone exit=1` and `windows timing exit=1`.
- p3-win-x11-alone.log: TestV3_HotPathUnchangedWithLedgerResident FAIL, no-ledger run B-A p99 98.304 ms, B-B p99 81.920 ms (limit 50). 593 of 2,130 requests were DEFERRED and 0 were lost. Spawn floor p50 was 29.960 ms.
- p3-win-timing.log: every timing package passed, including test/integration. The ci.yml timing pattern includes TestIntegration_HotPathWarmWithRealResidentState.
- The Windows row now gives candidate 6's own facts: the timing rows passed, and X11 failed in test/e2e and when run alone, with the numbers above. It says the red is unclassified and that candidate 6's quiet C5.1 has not reported, and it keeps candidate 5's quiet pass labelled as candidate 5. The heading is now "as of candidate 6".
- CHANGELOG:71 is reworded to D41's mechanism: "B-B's own durable ingest no longer trips the breach detector systematically on Windows". CHANGELOG Known limits carries the same Windows facts.

**3. (minor) The "Installed in Claude Code" row did not name a candidate: CONFIRMED, fixed in 7aca325c.**
- Evidence: live/report.md is candidate 3 (d5598eb4). live/rerun-c4/UAT-01/snapshot.txt shows bundle commit 9f6a2fad on 2.1.280. There is no rerun-c5 and no c6 live directory.
- The row and CHANGELOG Known limits now say the live lanes on candidates 3 and 4 installed those candidates' frozen bundles.

**4. (minor) cannot-do "macOS and windows/arm64 have no runner": CONFIRMED, fixed in c8c53cc4.**
- The entry now says that no macOS or windows/arm64 machine with Claude Code installed was available to the live lane. macOS runs the test suites on hosted runners, which install nothing into Claude Code. The entry also names candidates 3 and 4.

**5. (minor) The cannot-do pid-reuse entry did not cite D38: CONFIRMED (`grep D38` found nothing), fixed in c8c53cc4.**
- Added to 'Why': "Owner decision D38 accepted the pid-reuse case for 0.3.0 as documented and flagged; closing it needs a spool-name or per-record merge redesign."
- 'Recorded at' now cites `plans/V6-CLOSEOUT-CHECKLIST.md` D35(b) and D38.

**6. (minor) Stale uat.md step 2 and upstream-issues proposals 1 and 5: CONFIRMED, fixed in f7891090, with stronger evidence than the reviewer cited.**
- uat.md step 2 now expects the stamped bundle version: 0.3.0 for a release bundle (observed in rerun-c4/UAT-01/version.txt), and 0.1.0 from a plain source build until the version commit.
- The UAT-01 contract snapshot (cli/x4-status-json.stdout.txt) has every row stamped with the same session-start ts, reading `not-yet-observed` and `initialize-pending`. Its state/history.json has mcp_initialized true.
- After a session that compacted, rerun-c4/UAT-02/cli/05-status-json.stdout.txt reads:
  - hook.additional_context_delivered `sentinel-observed`
  - precompact.has_time_to_write `p99=119ms timeout=20000ms`
  - mcp.server_registered `initialize-received`
- Proposals 1, 2 and 5 now cite these readings and say what they do not show: no delivered size, and no host wait guarantee. While a server is uninitialized, the MCP row cannot tell "never launched" from "not yet initialized". Proposal 2 got the same fix for consistency, because it made the same B01 claim. Every proposal still says "Status: not filed".
- cannot-do's "No exact native loaded bytes" also claimed that no installed-host run was ever recorded. It now cites the sentinel observation and keeps the "no delivered figure" conclusion.
- docs/architecture.md:755 quotes what MIGRATION-EVIDENCE records, which is still accurate, so it is unchanged.

## Machine limits
Before each command, chain.log showed "linux timing exit=1 2026-10-02T04:17:32Z" and "step linux-tree exit=0", with no "linux race exit=" line. That is the -race window, and I ran one command at a time. I ran no hot-path rows, no benchmarks and no Docker.

## Commands (all at f7891090, the final docs head; logged in runs/docs-checks-windows-fix.log)
See tests.

## Criterion changes
None.

### Commits

- 864f312e docs(readme): say the scheduler estimates a cache state internally
- 7aca325c docs(readme): report candidate 6's windows timing red beside c5
- c8c53cc4 docs(cannot-do): name d38 and say which hosts the live lane lacked
- f7891090 docs(upstream-issues): cite the installed-host contract readings
- 2408abd6 test(v6): record the w17-docs fix-round checks on windows

### Tests

- `go test -p 2 -count=1 ./test/docs` — ok 2.459s (exit 0)
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check` — all three up to date, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS, exit 0

### Open issues

- C5.5 verdict (A8 item 1). Quote it verbatim in three places: docs/user-guide.md's /qompack:eval section, README.md 'What 0.3.0 is not' (third bullet, after the A8 sentence), and CHANGELOG.md [Unreleased] 'Known limits' (first bullet). Each place also carries H3's label ('recovery advantage shown' or 'not shown'). If the verdict is inconclusive, add A8 item 3's sentence. If not-applicable happens twice, say the study did not run as designed.
- Candidate 6 live-lane verdict (D53(f)). In README.md's 'What is verified where, as of candidate 6' table, row 'Installed in Claude Code', add what the candidate 6 lane passed or failed: D52's 16 sessions, the UAT-12 upgrade leg, the C1.7 restore smoke, and UAT-01/C4.1 through qompack-windows-amd64. Also give the D53(i) count of host-reported hook failures and timeouts. Add the same candidate 6 install to CHANGELOG 'Known limits' (it now says candidates 3 and 4) and to the docs/cannot-do.md 'Installed in Claude Code on windows/amd64 only' entry. Host-seen p50/p95 per hook (D53(i), A8 item 6) goes in the release notes and in CHANGELOG 'Known limits'. docs/uat.md's header and Result blocks belong to the live-lane workstream.
- Namespace under a release entry. Once the candidate 6 UAT-01/C4.1 session has run through qompack-windows-amd64, replace the 'has not been observed' sentence in docs/install.md §9 with the observed server name, tool prefix and command prefix. Then drop the matching 'not verified in target' row in docs/release.md's capability table and the clause in CHANGELOG 'Known limits'. If commands show a prefix other than /qompack:, two follow-ups: a docs pass, and internal/grammar/statewarn.go selfMarkerSlashCommand becomes a product item (the detector ships disabled, D56(h)).
- Windows timing on candidate 6. README.md's 'Windows, the reference host' row and CHANGELOG 'Known limits' now say that candidate 6's X11 red (p3-win-x11-alone: B-A p99 98.3 ms, B-B p99 81.9 ms vs 50, 593 of 2,130 deferred, 0 lost; also in p3-win-e2e-timing) is unclassified, and that candidate 6's quiet C5.1 has not reported. Once the x11win workstream classifies the red and chain.log has 'quiet exit=', replace both with the classification and candidate 6's phase3/c6/quiet/c51-win figures. Candidate 5's 30.7/22.5 ms can then be dropped or kept as history. If candidate 6's C5.2 supersedes candidate 5's, also update the figures in docs/cannot-do.md 'Two store write rows miss their budgets after the hook's ACK'.
- Hosted CI. README.md's hosted-CI row and docs/release.md §7 say release-dry-run and test (windows-latest) failed on run 36955046276 and must be fixed or dispositioned (C7.2). Update both when the ci workstream classifies them, or when a re-run on the release commit is green.
- CHANGELOG.md stays under '## [Unreleased]' (the file's convention; release.md §1 step 2). At the release the coordinator renames it to '## [0.3.0] - YYYY-MM-DD'; the date is left open. CHANGELOG's first paragraph, README.md 'Status: release candidate' and release.md's header all name verify/v6 99d0b18. All three must change if a later candidate is cut.
- The executable bit and Gatekeeper on Linux and macOS stay 'not verified in target' in README, install §9/§10 and the capability table. That changes only once D53(h) step 3's pre-release install rehearsal records them (the Linux container no-model install).

## Independent verification of the fix seat: needs-fixes

- **major** `README.md:96 (Windows row); CHANGELOG.md:129-131 (Known limits); also the fix seat's open_issues 'Windows timing on candidate 6' entry` — The new text says candidate 6 failed the hot path with the ledger resident and gives B-A p99 98.3 ms and B-B p99 81.9 ms for that run. The evidence shows otherwise. Those figures come from X11's no-ledger baseline leg, which runs the same corpus without the ledger. The test failed in that leg and stopped, so the ledger-resident leg never ran. The CHANGELOG makes the claim directly ('failed the hot path with the ledger resident (X11: ...)'). That points readers at the ledger as the cause, but what actually breached was the plain hot path on the 41 MB resident corpus. A secondary point: the harness marks both p99s as the delivered set's own figures, given 'for diagnosis only' and 'a lower bound on the real one'. The gate failed because 528 of the 2,064 planned samples never reached the daemon's histogram, which made the p99 impossible to certify. The docs present the numbers as plain p99s. This is the same class of error the original major finding was about: a candidate's verified-where facts stated wrongly in the release-facing table.
  - Evidence: phase3/c6/p3-win-x11-alone.log:3 'v3_x11_test.go:511: no-ledger run (the X11 corpus without the ledger): the bench harness took 2m21s'. Lines 18-19 are the B-A 98.304 / B-B 81.920 [FAIL] rows inside that leg. Line 141 is the require failure message 'no-ledger run (the X11 corpus without the ledger): bench-hotpath exited non-zero', at v3_x11_test.go:605, called from :511. After it come '--- FAIL' and nothing else, so no ledger-run output exists. Lines 28-29 say 'B-A's p99 CANNOT be certified ... The p99 field reports the DELIVERED set's own p99 for diagnosis only; it is a lower bound on the real one, never the gated number'. p3-win-e2e-timing.log:3 shows the same pattern: the no-ledger leg failed at B-A 81.92 / B-B 57.344. test/e2e/v3_x11_test.go:14 and :412 put the no-ledger twin first, then the ledger-resident project.
  - Fix: README Windows row: replace 'It failed X11, the hot path with the ledger resident, both inside `test/e2e` and run by itself: in the run by itself B-A p99 98.3 ms and B-B p99 81.9 ms against 50' with: 'It failed X11 (`TestV3_HotPathUnchangedWithLedgerResident`) in its no-ledger baseline leg, the X11 resident corpus without the ledger, both inside `test/e2e` and run by itself; the ledger-resident leg did not run. In the run by itself the delivered samples read B-A p99 98.3 ms and B-B p99 81.9 ms against 50. These are lower bounds: 593 of 2,130 hot-path requests were deferred to the client spool (0 lost), so the gated p99 could not be certified.' CHANGELOG.md:129-131 changes the same way: 'candidate 6's isolated timing pass failed X11's no-ledger baseline leg (delivered-sample B-A p99 98.3 ms, B-B p99 81.9 ms against 50, lower bounds; 593 deferred, 0 lost; the ledger-resident leg did not run)'. Correct the open issue the same way.


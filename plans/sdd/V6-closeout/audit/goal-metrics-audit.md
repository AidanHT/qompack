# V6 close-out audit: goal, metrics and release readiness (candidate 5)

Independent read-only audit, 2026-10-01, for the V6 close-out coordinator. Scope: is the work still
aimed at a production plugin that public users install, do the gates and benchmarks measure what users
experience, are the thresholds and reference environments sound, is C5.5's design valid, and what is
left before a release that meets D33's condition.

Method: I only read files, grepped and ran read-only `git log/show/diff`. I ran no test, build,
benchmark or container, because the quiet C5.2 run was in progress. Paths: `cand:` means
`../qompack-cx-cand` (frozen candidate 5, `0d06ab12`); `v6:` means `../qompack-v6` (verify/v6, where
the ledger and evidence live); `cx:` means `v6:plans/sdd/V6-closeout/`.

Severity: **blocker** (do not publish until resolved), **should-fix** (fix or disposition before
0.3.0), **note**.

---

## Top findings

| # | Sev | Finding | Evidence |
|---|---|---|---|
| F1 | blocker | **Candidate 5 fails its own gates, and was frozen without test/e2e.** The D28 isolated timing pass fails the same three e2e tests on Windows and Linux: `TestDaemonIdleRunsSchedulerWork`, `TestV5_ObserveToStatusRoundTrip` and `TestV5_ThrashWarningVisibleInStatusAndCheckpoint`. X11 (`TestV3_HotPathUnchangedWithLedgerResident`) also fails on both OSes. On isolated Windows, X11's no-ledger run shows B-A p99 61.4 ms against 50 ms, and the share Qompack controls outside the disk (hook_controlled_observed) is p50 13.3 ms. Wave 12 measured that share at 6.1 ms when X11 passed. The pre-freeze merged-tree check left out test/e2e, integration, fault, security, platform and release, and C3.2–C3.12 have not run on c5. At least the scheduler row encodes behaviour that D49 changed ("no ledger yet is not an advance_frontier error"), so it looks like a stale criterion. The X11 slowdown is unexplained. | `cx:phase3/c5/chain.log` (win-e2e-timing exit=1, linux-timing exit=1); `cx:phase3/c5/p3-win-e2e-timing.log:1-6, 24, 37, 150-182`; `cx:phase3/c5/linux/cx-p3-p3-linux-e2e-timing-*/summary.txt`; `cx:phase3/c5-CANDIDATE.md:12-15`; `cand:test/e2e/scheduler_idle_test.go:186-198`; ledger row "12 done" (`v6:plans/V6-CLOSEOUT-CHECKLIST.md:129`) |
| F2 | blocker | **The public install path has never been exercised.** Every real install so far used a local directory marketplace whose entry was named `qompack`. The release ships six `archive` entries named `qompack-<os>-<arch>`, which differs from plugin.json's `qompack`. Four things are unverified: the slash-command and MCP-tool namespace for such an entry, the executable bit after the host extracts the zip on linux/darwin, a first run of release.yml, and a download from a real HTTPS release asset. The code assumes the namespace: `hostQompackToolPrefix = "mcp__plugin_qompack_qompack__"`. Separately, marketplace.yml triggers only on `published` and skips pre-releases. Promoting a pre-release to a release fires `released`, not `published`, so the "test as a pre-release, then promote" route would silently never open the marketplace pull request. | `cand:docs/install.md:320-325, 352-360`; `cx:packaging/evidence/c7.5-marketplace/entry-name-probe.txt`; `cx:live/report.md:11,23`; `cand:internal/mcp/recall_rank.go:9-22`; `cand:.github/workflows/marketplace.yml:26-28,46`; `cand:docs/release.md:221-228` |
| F3 | blocker | **No V6 candidate has run on native Linux or macOS.** Hosted CI (C7.2) is the only planned evidence for either. The darwin binaries have never been executed in V6, and the C4.11 Linux install half has never run. Separately, ci.yml still gates B-A/B-B on hosted windows-latest, while the Q1 default the ledger adopted says hosted fsync-bound rows are report-only. Unless that is encoded, those jobs will be red and cannot be required checks under C7.3's branch protection. | `v6:plans/V6-CLOSEOUT-CHECKLIST.md:327-328, 361-364, 74`; `cx:live/report.md:410,487`; `cand:.github/workflows/ci.yml:294-342, 249-268`; `cand:internal/config/deadlines.go:174-183` |
| F4 | blocker (cheap) | **The shipped bundles carry no LICENSE and no third-party notices.** A bundle holds plugin.json, .mcp.json, hooks, commands, bin, BUNDLE.json and checksums.txt, and goreleaser uploads only the zips, checksums.txt and marketplace.json. The binaries statically link go-winio (MIT), klauspost/compress (BSD/Apache/MIT) and x/sys (BSD-3), and BSD-3 requires the notice in redistributed binary forms. THIRD_PARTY_NOTICES.md also says the Go standard library "is not redistributed", but the runtime and stdlib are compiled into every binary. | `cand:packaging/README.md:14-22`; `cx:phase3/bundleA.sha` (windows-amd64 file list); `cand:.goreleaser.yaml:57-60`; `cand:THIRD_PARTY_NOTICES.md:40-46, 843-844` |
| F5 | should-fix | **Linux fsync-bound budgets have no valid reference environment, and slow-fsync Linux users get a healthy session reported as degraded.** Details in §2.2. | `cand:internal/config/deadlines.go:152-170, 191-243`; `cx:phase3/c5/quiet/c51-linux/c51-linux.log:16-17,25,29` |
| F6 | should-fix | **As designed, C5.5 can confirm only "does not break", and even that only near ceiling.** With 20 trials per arm, two equal arms below 95 % success read "inconclusive" against the 0.20 margin. H3, the value claim, has no verdict, and the tasks are short manual-`/compact` sessions that stock probably passes (the pilot's stock trial recovered 1/1). What each verdict means for the release is not declared. Details in §3. | `cx:eval/preregistration.md:89-95, 114-140`; `cx:eval/runs/pilot3-stock/summary.md`; `cand:internal/eval/livetrial.go:608-645, 851-880` |
| F7 | should-fix | **D52 does not re-run rows whose code changed.** The c4→c5 product diff is 64 files and 3,196 insertions, including `internal/store/publication_audit.go` (+346, the startup publication pass) and 20 daemon files. Yet D52 runs UAT-12 *without* the upgrade leg and does not re-run C1.7, C4.8, C4.1/UAT-01, C4.2 or C4.7. The V6 plan says "no historical-source pass certifies a different installed bundle". | `git diff --stat 9f6a2fad 0d06ab12 -- internal cmd ':!*_test.go'`; `v6:plans/V6-CLOSEOUT-CHECKLIST.md:71`; `v6:plans/V6-VERIFY-production-readiness-and-uat.md:23` |
| F8 | should-fix | **The marketplace-facing text overclaims.** "Cache-aware, retrieval-backed context compaction" appears in plugin.json and the marketplace. Qompack does not compact, and the shipped checkpoint path passes `TTLState: "unknown"` because "scheduler.Runtime ... has no in-tree implementation". `/qompack:pin` says "never summarized away", but pins are re-injected after a compaction under the 9,500-character cap; nothing shields them from the summarizer. | `cand:plugin/.claude-plugin/plugin.json:4`; `cand:internal/pluginmanifest/manifest.go:205,266`; `cand:tools/devtool/marketplace.go:245`; `cand:internal/daemon/wire_checkpoint.go:186-192` |
| F9 | should-fix | **Windows timings may not be measured the way users run.** D32's Defender exclusion covers "Go build cache/temp and the worktrees". The bench stores and the live-lane scratch projects live under %TEMP%, so the quiet B-A/B-B rows and the live hook latencies may have been taken without Defender scanning the store. A user has Defender on and an unexcluded project. Whether the exact release binary is flagged by a stock Defender is also unobserved; troubleshooting.md says so. | `v6:plans/V6-CLOSEOUT-CHECKLIST.md:51`; `cx:coordinator/live-rerun-c5.js:34`; `cand:docs/troubleshooting.md:1042-1080` |

---

## 1. What Qompack promises a user

| Claim | Where |
|---|---|
| P1. Records tool results, prompts and turn boundaries into a local `.qompack/` store through 7 hooks | `cand:README.md:3-11`; `cand:docs/user-guide.md:15-20` |
| P2. Seals a checkpoint at PreCompact. After a compaction it injects a bounded rehydration block (≤ 9,500 host characters, whole records, overflow named with pointers) through SessionStart(compact) | `cand:README.md:8-10`; Qompack.md v1.6 entry; D5 |
| P3. Recall on demand: 8 MCP tools and 6 slash commands (recall, expand, re_read, already_tried, record_eliminated, timeline, why, dropped) | `cand:docs/user-guide.md:18-20`; `cand:docs/mcp-tools.md` |
| P4. A sidecar only: never rewrites, evicts or cuts native context | `cand:README.md:17-18`; `cand:docs/user-guide.md:24-25` |
| P5. No network I/O and no telemetry, now or by configuration | `cand:README.md:18`; `cand:docs/security.md:332-345` |
| P6. Privacy: secrets redacted, host deny rules honoured, out-of-project archived reads refused, retrieval re-checked against today's permissions | `cand:docs/security.md` §1-2; `cand:docs/install.md:256-260`; D7 |
| P7. Durability: fsync before ACK, nothing lost on a degrade, backup / verify / restore, fsck | `cand:docs/architecture.md` §4; `cand:docs/backup.md`; `cand:docs/troubleshooting.md:965-970` |
| P8. Never breaks the host session: hooks exit 0, output sizes bounded (D15), hook time bounded by manifest timeouts and D9/D21 | `cand:docs/user-guide.md:53-56`; D9, D13, D15, D21 |
| P9. Kill switches: recording, reinjection and the daemon can each be turned off independently | `cand:docs/release.md:134-155` |
| P10. Uninstall keeps your data; deletion is manual and is not secure erasure | `cand:docs/install.md:212-246` |
| P11. Platforms: six targets built; only windows/amd64 installed-verified; Claude Code ≥ 2.1.139 (≥ 2.1.224 for the marketplace) | `cand:docs/release.md:92-132`; `cand:README.md:32-36` |
| P12. Promises no improvement: no held-out quality trials yet | `cand:docs/user-guide.md:29-31` |
| P13 (marketplace headline) "Cache-aware, retrieval-backed context compaction" | `cand:plugin/.claude-plugin/plugin.json:4` |

P12 is the right claim boundary for 0.3.0 unless C5.5 shows more. P13 contradicts P4 and P12 and should be reworded (F8).

## 2. Claim → evidence map and metric validity

### 2.1 Map

| Claim | Verifying evidence | Valid? | Status |
|---|---|---|---|
| P1 capture | C4.2 (index counts vs transcript), suites | Valid (real host) | c4 pass; not re-run on c5 (F7) |
| P2 compaction round trip | C4.3, UAT-03/05/06 | Valid (real host, real model) | **failed on c4**; D52 re-runs on c5 |
| P3 tools / commands | C4.4 (tools), C4.5 (commands) | Valid | C4.4 failed on c4; C4.5 passed (c3, c4); D52 re-runs C4.4 and C4.5 |
| P4 sidecar | by construction, cannot-do.md | n/a | — |
| P5 no network | `TestGuard_NoNetworkImports` + CI import allow-list | Valid for the import graph | runs with test/guards |
| P6 privacy | C4.6 (0 secret hits, refusals) | Valid | c3 pass; D50 pointer change; D52 re-runs C4.6 |
| P7 durability | structural gates T9/T10/T14, crash tests, C1.7, C4.8 | Valid | C1.7 and C4.8 passed on c4 only; the publication audit was rewritten in c5 (F7) |
| P8 never break the host | C4.9 degraded paths; C5.6 host-seen hook deltas, 631 events with "outcome not success: 0" | **The right metric, but not declared as a release criterion.** The gated rows (B-A/B-B) are internal (§2.2) | C4.9 passed on c4; C5.6 has c3 data only |
| P9 kill switches | C4.7, test/release | Valid | c3 pass |
| P10 uninstall | C4.8 | Valid | c4 pass |
| P11 platforms | release-scope table, C4.11, hosted CI | Windows valid; nothing else | C4.11 Linux never ran; no CI on V6 (F3) |
| P12 / value | C5.5 | See §3 | not run |
| P13 cache-aware | **none planned** | — | unsupported (F8) |

**(a) Claims with no planned evidence:** P13 ("cache-aware", "compaction"); the pin "never
summarized away" wording; the public archive install path (F2); macOS execution of any kind (F3);
SessionStart:startup host-seen latency, which C5.6 never measured because all 26 pairs arrived before
init (`cx:live/resources/C5.6/summary.md:100-111`). The eval's transcript `durationMs` can supply
it (prereg §6).

**(b) Metrics that do not measure what users experience.**

- B-B (l0_ingest) is "PURELY A BUDGET. No production path branches on it" (`cand:internal/config/deadlines.go:79-85`).
- B-A breaches lead only to spool submode (`cand:internal/daemon/handlers.go:451-463`). A hook already gives up waiting at `AckDeadlineMs` and spools (`cand:internal/ipc/client.go:251-301`), so on Linux the host-perceived hook time is bounded by spawn + 5 ms connect + 17 ms ACK whatever B-B does. A B-A breach is therefore mostly a *label* for the user, not a stall.
- The user-perceived row, B-D hook_wall, is "Reported only, never gated" (`cand:internal/obs/budgets.go:27-28, 98-102`). The live arrival deltas are ungated too.
- **Recommendation:** make "host-reported hook failures/timeouts = 0 across the live lane and C5.5, apart from documented host behaviour" an explicit release criterion (it is 0 of 631 today), and report host-seen p50/p95 per hook in the release notes. This needs no new budget number.
- The five carried perf rows (SP06-D2 PutBytes, SP08-D1 B-C, SP10-D1 Finalize, SP09-D1 negknow Open, SP20-D2 GetChunk) measure internal components with no direct user-visible effect: B-C is async and soft; Finalize runs inside B-E's 2 s on a 20 s PreCompact; negknow Open is one-time; PutBytes is async; GetChunk costs microseconds inside B-F. They block `release-check` through test/guards (`cand:plans/CARRIED-DEFECTS.tsv`, five `deferred:V6-VERIFY` rows). Disposition them from the quiet C5.2 run (re-budget or wontfix per C2.8), and keep the user-facing B-E and B-F gated.

**(c) Thresholds whose derivation is wrong or stale.**

- Linux `L0IngestMsPortable = 15` and `AckDeadlineMsPortable = 17` are "PROVISIONAL" design predictions that the three-run protocol was supposed to replace and never did (`cand:internal/config/deadlines.go:152-160, 235-239`).
- D41's `budgetMs = max(15, l0IngestMs)` makes the B-A limit equal to the B-B limit. But a B-A sample *is* B-B + transit + 1 ms (`cand:internal/daemon/handlers.go:337-339`), so B-A always breaches before B-B. X11 on c5 shows the cascade: B-B's delivered p99 of 32.8 ms is within 50, but the transit share pushes B-A p99 to 61.4. The detector then moves to spool, and B-B becomes uncertifiable (`cx:phase3/c5/p3-win-e2e-timing.log:24-37`). Re-check this derivation as part of F1's triage.
- The Windows 50 ms was derived on 2026-09-13 (`deadlines.go:87-132`). That predates D26's extra fsyncs for payloads of 1 MiB or more and D2's rollover. The quiet c51-win run still passes with margin (B-B p99 22.5 ms; `cx:phase3/c5/quiet/c51-win.log:15-16`), so this is a note, not a change.

**(d) Invalid reference environments.**

- The Linux container is not a valid gate for 15 ms (§2.2).
- Hosted runners are already report-only under Q1, but that is not encoded in ci.yml (F3).
- Windows timings may be AV-excluded (F9).

### 2.2 The Linux B-B case (the case the brief asked about)

**What was measured.** In the container (Docker Desktop on WSL2, overlayfs on a VHDX), every
candidate since Sep 28 shows B-A ≈ B-B with p50 37–41 ms against 15 ms. Candidate 5's quiet run:

- B-B p50 36.9 ms, p99 245.8 ms, n = 1,539.
- Ledger: 5,130 sent, 1,540 delivered live, 3,590 deferred to the client spool, 0 lost.
- The transit share (hook_controlled_observed) is p50 1.0 ms. The whole cost is the disk.

Source: `cx:phase3/c5/quiet/c51-linux/c51-linux.log:16-17, 25, 29`. The same overlayfs makes a cold
put 80–90 ms against 13–15 ms on tmpfs (`cx:perfstore/report.md:36`). Hosted ubuntu measured B-B p50
0.9 ms and p99 4.6 ms (`cand:internal/config/deadlines.go:165-167`).

**Is the container a valid Linux reference for fsync-bound rows?** No, not as the environment that
prices or passes a 15 ms gate. It is a slow-fsync stack, about 40× hosted ubuntu at p50. It *is* a
valid and valuable reference for a real user class: devcontainers, and WSL2 users on Docker
Desktop. There the requirement should be "graceful, nothing lost, not alarming", and c51-linux
already shows 0 lost.

**Is 15 ms the right Linux budget?** There is no defensible derivation either way. The project's own
rules exclude both available environments: hosted figures can never become constants (Q1), and the
container is a slow-fsync outlier. So the Linux timing rows cannot be honestly passed or re-priced
before release. Record them as *not verified in target* with that reason, the same treatment D37
gives other unexecutable rows. Keep running the container rows as evidence of graceful behaviour.
Do not relabel the container a reference and do not change 15.

**What a slow-fsync Linux user actually experiences.**

1. Each hook waits at most 17 ms for the ACK, then spools a copy and exits. Host-perceived latency
   stays small: B-D p99 is 26 ms in c51-linux. The daemon still finishes the durable ingest, so the
   spooled copy is a duplicate the drain must skip (SP05-D2's cost: extra I/O, not loss).
2. After three consecutive 512-sample windows with p99 ≥ 15 ms (about 1,536 hot-path events in one
   session), the daemon switches to spool submode (`cand:internal/daemon/budget.go:95-124`;
   `handlers.go:451-463`). It writes a WARN and a LOUD line, "hot path degraded to spool submode";
   status shows `hot path: spool` until a new session or a daemon restart (D44;
   `cand:docs/troubleshooting.md:956-983`). The troubleshooting page attributes this to "heavily
   loaded" machines. On a slow disk it happens in every long session, and the page does not tell the
   user to raise `runtime.hotPath.budgetMs` and `runtime.budgets.l0IngestMs` (and `ackDeadlineMs`).
3. Captures in spool submode reach the store about two watcher intervals later
   (`cand:internal/daemon/spool_watch.go:44-53`). **Possible context gap, unverified:** the
   PreCompact route seals without draining client spools (`cand:internal/daemon/handlers.go:1171-1230`;
   `cand:internal/daemon/wire_checkpoint.go:145-196`). A compaction that fires within that window
   could seal a checkpoint without the newest tool results, and rehydration reads that checkpoint
   (D36). No test and no live row compacts in spool submode.

**Is this a D45 "healthy session looks broken" defect?** Yes, by D45's wording and by D41's
precedent: D41 fixed the identical symptom for Windows. The session loses nothing, yet it reports
itself degraded. It is should-fix rather than blocker, because it needs about 1.5k hook events in one
session on a slow disk.

**Minimal disposition, no new numbers:**

- (i) A D-record: Linux fsync-bound rows are not verified in target; the container is the slow-fsync
  acceptance environment.
- (ii) A troubleshooting entry for slow disks (WSL2, containers, network or encrypted filesystems):
  expected, harmless, and how to tune it.
- (iii) Either reword the transition's user-facing message, which currently says "degraded", or
  record the decision to keep it.
- (iv) One focused test that a compaction in spool submode still points at the last tool results, or
  a drain of client spools before the seal.

## 3. Eval validity (C5.5)

**Does the design test the value claim?** No. It tests harm.

- H1 is non-inferiority on *task success*, and most task checks are hidden tests of code that does
  not depend on pre-compaction memory (Clamp/Lerp/Sign, JoinList, ...).
- H3, recovery higher with Qompack, "is reported with the same interval and no separate verdict"
  (`cx:eval/preregistration.md:89-95, 139-140`).
- **The tasks are unlikely to discriminate.** Each has 1–3 short steps and then a manual `/compact`
  (`cand:testdata/eval/live/tasks-v2.json`). This is the least lossy compaction possible: the host's
  summary of a three-message session will normally keep "region code QX-7731-EU", "D-17 because
  grep" and the token the assistant itself repeated. The one stock pilot recovered the fact 1/1
  (`cx:eval/runs/pilot3-stock/summary.md`).
- Expect stock near ceiling on recovery and constraints. A Qompack advantage cannot show, and that is
  a property of the design, not evidence that Qompack adds nothing.

**Sample size.** `DecideLive` uses Wilson and Newcombe hybrid intervals with margin 0.20
(`cand:internal/eval/livetrial.go:608-645, 851-880`). With 20 trials per arm and *identical*
performance:

| Success in both arms | Lower bound | Verdict |
|---|---|---|
| 20/20 | −0.16 | non-inferior |
| 19/20 | −0.19 | non-inferior |
| 18/20 | −0.21 | **inconclusive** |
| 17/20 | −0.23 | **inconclusive** |
| 14/20 | −0.27 | **inconclusive** |

So unless both arms reach at least 95 %, the primary verdict is "inconclusive" even when Qompack is
exactly as good as stock. §7 admits large effects only, but does not say this. At 40 trials per arm
(the second batch §7 already permits if recorded before data), equal arms at 0.85–0.90 give lower
bounds of −0.16 and −0.14: conclusive.

**Stale preconditions.**

- A5's attestation `--known-open-defects none` cannot be honest on c5 today. c5 has unclassified e2e
  reds (F1), D52 rows still to run, and five carried defects still `deferred`.
- A7's §9 command still lacks `--confirmatory`. The w3-eval3 review nit is unresolved
  (`cx:eval/preregistration.md:371-375`; `cx:w3-eval3/report.md:140-142`).
- The §9 command rebuilds the bundle (`devtool bundle --version <v>`) instead of using the frozen
  release bundle. Reproducibility makes these equal only if `<v>` = `0.3.0` and the commit matches.
- The model `claude-sonnet-5` and its contingency (A6) are fine. Record the host version, since
  2.1.280 was "at the time of writing".

**Recommended pre-data amendment A8 (minimal):**

1. **Release consequence per verdict, declared now.**
   - *inferior*, or an H2 regression → release blocker.
   - *not-applicable* → re-run once under §8's rules.
   - *non-inferior*, *superior* or *inconclusive* → release, with the verdict quoted verbatim in the
     release notes and docs.
2. **The claim boundary.** No document may claim improved recovery unless H3's Newcombe lower bound
   is above 0. Give H3 that one labelled outcome, "recovery advantage shown / not shown".
3. **Sample size.** Either pre-register the second batch now (80 sessions; about 40 more Sonnet
   sessions), or state in §7 that equal performance below 95 % yields "inconclusive" by design.
4. **"Known open defect".** Define it as an item without a recorded fix or disposition in the ledger
   at plan time. Dispositioned residuals (D6, D38, D44, D48) and C2.8-ruled perf rows are listed in
   `plan.json`, not blocking.
5. **Run order.** Add `--confirmatory`, require a `--dry-run` first, and use the frozen release bundle
   with its BUNDLE.json SHA compared. Run C5.5 only after D52 passes, on the bundle that ships.

**Not recommended for 0.3.0:** redesigning the tasks to be lossier (bulk context, auto-compaction).
It would give a real benefit test, but it costs a new task set, re-validation and host thrash risk
(D52 removed the auto-compact override because it thrashed the host). Bound the claims instead.

## 4. Release items a public Claude Code plugin needs

| Item | State | Sev |
|---|---|---|
| Marketplace manifest and install from GitHub | Generated and validated locally (`cand:packaging/README.md:252-264`). Never installed from a real release (F2). Promotion trap in marketplace.yml (F2). The owner must enable the repository setting "Allow GitHub Actions to create and approve pull requests" (`cand:docs/release.md:42-44`) | blocker |
| Per-target entry UX | The user must pick the right entry. A wrong pick makes every hook fail with an exec error; the failures are non-blocking but noisy. Documented (`cand:docs/install.md:278-291`) | note |
| plugin.json fields | name, version, description, author, homepage, keywords. No `license` or `repository` (the marketplace entries carry both). Description overclaims (F8) | should-fix (description); note (fields) |
| Version agreement 0.3.0 | `core.Version = "0.1.0"` and the committed plugin.json says 0.1.0; bundles are stamped 0.3.0 by `--version`. The release commit must bump both (C7.1). That makes the released source = candidate + a version/docs diff, so **verify the release zips' `bin/` sha256 equal the frozen candidate bundles'**: release.yml builds on ubuntu, the frozen bundles were built on Windows | should-fix |
| LICENSE / notices | Not in the bundles; stdlib note wrong (F4) | blocker (cheap) |
| Install docs for six targets | §9 table and the exec-bit workaround exist. Not covered: macOS Gatekeeper / quarantine for a manually downloaded zip (`xattr -d com.apple.quarantine`), and a statement that binaries are not notarized. The Go linker ad-hoc-signs darwin/arm64, but no darwin binary has ever run | should-fix (docs) |
| Unsigned binaries | Windows: Defender ML false positives on dev builds (D32). Signing is "open and unowned" (`cand:docs/release.md:213-219`). Before publishing: scan the exact release exe with stock Defender and submit it to WDSI pre-emptively. A blocked binary leaves Qompack silently off with hook errors. SmartScreen does not apply: the host spawns the binary directly and no Mark-of-the-Web is involved. | should-fix (owner action) |
| Uninstall cleanup | Documented: data retained, Windows staged copies removed by hand, daemon exits on idle (`cand:docs/install.md:212-246`) | ok |
| CHANGELOG | `[Unreleased]` lists only SP-17 tooling. Line 51-52 says hooks "quote `${CLAUDE_PLUGIN_ROOT}`", which exec form superseded (C1.11). Missing every user-visible V6 change: exec-form hooks, the 9,500-character cap, rollover on by default with its D6 residuals, home refusal, six commands, D41 budgets, the Windows staged copy, the SessionEnd async contract | should-fix (C6.5) |
| README status | Says "not been released", that the versions disagree, and "the acceptance scenarios ... have not been executed" (`cand:README.md:20-30, 57-65`). Stale | should-fix (C6.1) |
| release.md | "V6 release status: blocked ... See plans/sdd/V6-VERIFY/" (`cand:docs/release.md:7-12`). The scope table is from 2026-09-16 and its generator reads only SP-17 records, so the live lane's windows evidence and any C4.11 Linux record will not raise a status unless added | should-fix |
| install.md | Written against Task 8 on 2.1.263 (`cand:docs/install.md:3-9`); the live lane used 2.1.280 | should-fix |
| Telemetry / privacy | Clear (`cand:docs/security.md:332-345`). The store self-ignores (`cand:internal/paths/layout.go:14-16`, `.qompack/.gitignore` = `*`), but the user docs never say so. One line in security.md | note |
| Upgrade from v0.2.0 | v0.2.0 is a V3 checkpoint tag (plugin.json 0.1.0), and the v* tags were never pushed, so no installable v0.2.0 exists. C4.8 rehearsed an upgrade from a dev build (301a8e9). Release notes should say 0.3.0 is the first installable release | note |
| Old hosts | Below 2.1.139, a hook may print usage text into context, and `doctor` does not check the host version (`cand:docs/install.md:11-20`). Documented | note |
| Public repo hygiene | `NEXT-SESSION.md` is tracked at the root. Qompack.md's executive summary is internal planning prose. 11.5k of 13.9k tracked files are plans/evidence | note |

The checklist's Phase 6–7 (C6.1–C7.6) covers README, scope table, CHANGELOG, version, CI, merge, tag
and marketplace. It does **not** cover: LICENSE/notices in bundles (F4), the namespace and exec-bit
rehearsal *before* public publish (F2), the promotion trigger (F2), encoding Q1 in ci.yml (F3),
release-bytes = candidate-bytes, Defender on the release exe (F9), or Gatekeeper docs.

## 5. Checklist hygiene and what remains

### 5.1 Boxes whose evidence exists

| Box | Evidence | Action |
|---|---|---|
| C1.4, C1.5 | ledger "7 done": "C1.4 and C1.5 pass on Windows and Linux" (`v6:plans/V6-CLOSEOUT-CHECKLIST.md:111`) | tick, citing `cx:w7-sp08d3/` |
| C1.6 | "live done": pass (`:131`) | tick, citing `cx:live/report.md` |
| C1.7 | "live4 done": recovery C1.7 pass (`:137`) | tick, citing `cx:live/report-c4.md:309`. Re-confirm on the release candidate (F7) |
| C2.1 | SP08-D3 `fixed` in CARRIED-DEFECTS.tsv (wave 8) | tick |
| C2.2 | SP20-D4 `fixed`; C1.10 ticked (D2, D6) | tick |
| C3.1 | still cites `a94a3fb`, but the live candidate is c5 `0d06ab12` (`cx:phase3/c5-CANDIDATE.md`) | update the text |
| C4.1, C4.5, C4.7, C4.10 (c3); C4.2, C4.8, C4.9 (c4) | recorded passes, on earlier bundles | annotate per candidate. Tick only on the release bundle, or with a written carry-forward argument (F7) |
| D44 text | names `qompack daemon stop`, which does not exist (`cand:docs/troubleshooting.md:996-1003`) | correct the ledger wording |

### 5.2 What genuinely remains, in dependency order

To reach D33's condition (frozen candidate; gates, live lane and evidence complete and green, or every
red dispositioned):

1. **Finish and read the c5 quiet run.** Read C5.1/C5.2 when the run ends.
   `c52-win-r3-candidate-store-1s exit=1` (`cx:phase3/c5/chain.log`) needs reading.
2. **Triage c5's reds (F1).** The three cross-platform e2e rows (test criterion vs product, with a
   ruling for each), X11 (find why the transit share doubled; check whether wave 15c's background
   publication pass and capture gate sit on the request path), and the Linux hot-path row (§2.2
   disposition). Do not start D52 or C5.5 on c5 before this: a product fix would void those sessions.
3. **Fold the cheap fixes into the next candidate (c6).**
   - Product and packaging: LICENSE + THIRD_PARTY_NOTICES (including Go's licence) into every bundle
     (F4); plugin.json, marketplace and pin wording (F8); marketplace.yml `released` trigger (F2); the
     self-record prefix matching any plugin segment (`recall_rank.go:14`) (F2); optionally the §2.2
     spool-submode message or drain.
   - Tree files test/guards reads: CARRIED-DEFECTS.tsv dispositions (C2.3–C2.8 from C5.2) and C6.3,
     so test/guards and `release-check` can pass on the frozen tree.
4. **Freeze c6.** Run the whole tree on it: C3.2–C3.12, including test/e2e, integration, fault,
   security, platform and release, race and child-race, lint/vet/fmt/golangci on both OSes, cover,
   gens/licences/govulncheck, the replay gate and C5.3, fuzz, both validators, reproducible bundles,
   and `release-check` with no tag. Then D28 timing on both OSes.
5. **Live lane on the c6 bundle.** D52's 16 sessions, plus the UAT-12 upgrade leg, a C1.7 restore
   smoke and a one-session UAT-01/C4.1 sanity check (F7). Run the C4.11 Linux no-model install in the
   container. For rows not re-run, write a carry-forward note naming the files that changed.
6. **C5.5 on the same bundle**, after amendment A8 (§3). C5.6 comes from the eval sessions, including
   SessionStart:startup from the transcript's `durationMs`.
7. **C7.2 hosted CI on the release candidate (F3).** Push verify/v6, classify every red, and encode
   Q1 for the fsync-bound rows on hosted Windows, or keep those jobs out of the required checks.
8. **Phase 6.** C6.1 docs (README, release.md status and regenerated scope, install.md 2.1.280 and
   marketplace, user-guide's improvement statement per C5.5, troubleshooting for slow disks, a
   Gatekeeper note); C6.2 inventory; C6.4 V6 report and independent review; C6.5 plan boxes and the
   CHANGELOG entry.
9. **Phase 7, in this order.**
   1. C7.1 version commit; verify `bin/` bytes equal the frozen bundles.
   2. C7.3 merge to develop and then main.
   3. `release-check --tag`, then the tag. This is release.yml's first run; it produces a draft.
   4. Publish the draft as a **pre-release**. Run the rehearsal against the real HTTPS asset: a real
      Windows session from the `qompack-windows-amd64` entry (slash and MCP namespace), and a Linux
      container no-model install (executable bit, `qompack version` through the hook).
   5. Defender-scan the release exe and submit it to WDSI.
   6. Promote to a full release; marketplace.yml must fire (fix from step 3).
   7. Merge the marketplace pull request, then the C7.5 clean-profile smoke test from GitHub.
   8. C7.6 housekeeping.

## Appendix: interval arithmetic used in §3

Wilson 95 % (z = 1.96), Newcombe hybrid lower bound = (p1 − p2) − √((p1 − l1)² + (u2 − p2)²),
exactly as `cand:internal/eval/livetrial.go:851-880` computes it.

| Trials per arm | Rate | Wilson interval | Lower bound for equal arms |
|---|---|---|---|
| 20 | 1.00 | [0.839, 1.000] | −0.161 |
| 20 | 0.95 | [0.764, 0.991] | −0.191 |
| 20 | 0.90 | [0.699, 0.972] | −0.214 |
| 20 | 0.85 | [0.640, 0.948] | −0.232 |
| 20 | 0.70 | [0.481, 0.855] | −0.268 |
| 40 | 0.90 | [0.770, 0.960] | −0.144 |
| 40 | 0.85 | [0.709, 0.929] | −0.162 |
| 40 | 0.70 | [0.546, 0.819] | −0.195 |

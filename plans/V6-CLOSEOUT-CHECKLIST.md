# V6 close-out checklist — from candidate to a released, proven Claude Code plugin

Opened 2026-09-22 by the Claude Code coordinator taking over from the interrupted Codex session.
Branch `verify/v6` (`../qompack-v6`); last candidate `65bc8d7` (+ fixture commit `3dab390`).
This file is the working ledger: every box is ticked only with an evidence path, a commit, or a
recorded owner decision. It does not replace the V6 plan's acceptance contract
(`V6-VERIFY-production-readiness-and-uat.md`), it sequences the work that satisfies it.

Standing rules (carried from NEXT-SESSION §C, the V6 plan and the user's directives): never weaken
a check (no `t.Skip`, no `//nolint`, no lowered threshold, no regenerated golden, no deleted
assertion); preserve every failure as evidence; no attribution trailers on commits; no `v*` tag,
push, merge to `develop`/`main` or publication without the matching owner authorization; any new
budget number goes to the owner before it is committed; never kill a process this session did not
start; hosted-runner fsync figures never become constants.

## Owner decisions (2026-09-22, recorded from the user's answers)

| # | Decision | Consequence |
|---|---|---|
| D1 | Release version **v0.3.0** | `core.Version` and `plugin.json` move to `0.3.0` (C7.1) |
| D2 | Delivery-journal rollover: **finish the gates, then enable by default** | C1.10 completes old-reader, backup/restore, GC-retention and resource tests, then flips `enableDeliveryGenerations` on |
| D3 | Live runs: **agent-run on this machine, moderate budget (~40–80 real sessions)** | UAT and live trials are recorded as *agent-executed on the real installed host*, never as human UAT |
| D4 | Outward actions: **push `verify/v6` + hosted CI and merge to `develop`; ask before `main`, tag, GitHub Release, marketplace** | C7.2–C7.3(develop) authorized; C7.3(main), C7.4, C7.5 need a further yes |
| D5 | Rehydration **fits under the host's 10,000-character `additionalContext` cap**: a priority-ordered payload of whole records within ~9,500 chars including the wrapper, with the rest reported as overflow plus pointers for the MCP tools | C1.14; authorizes revising Qompack.md §8.6 and ADR 0011 to match |
| D6 | Rollover residuals **accepted and documented**: a bounded rotation pause of 2.3–6.8 s every 65,536 deliveries (hooks fall back to the durable spool), and GC halts safely once the carry passes 65,536 unacknowledged archived leases. Both get a loud diagnostic and counter, a Warn before the first rotation recommends a backup to keep a downgrade path, and incremental archiving is deferred past the release | C1.10 closes with these residuals documented |
| D7 | V6-HOST-1 **closes with the residual documented**: saved-settings Read deny/ask rules are honoured and fail closed. Session-only rules, CLI flags and hook policies stay invisible to a plugin. This supersedes the older rejection of settings parsing (authority-review.md §6) | C1.9 |
| D8 | Config: an unappliable `runtime.redact` or `runtime.mode` **fails closed** (stops recording, reported loudly); every other invalid key still falls back and warns | C1.8; docs state the exception |
| D9 | (2026-09-25) Compact SessionStart: a **5 s bound** on the daemon's wait for the rehydration (`compactAnswerBudget`, one third of the 15 s manifest timeout) and an explicit **"rehydration deferred" note** instead of `{}` when it is late, the daemon is stopping, or the daemon never answers | C1.16 (`closeout/w2-lifetime`) |
| D10 | (2026-09-25) C1.17 policy: on Windows the daemon runs from a SHA-256-verified, read-only **staged copy** under `~/.qompack/bin/<sha256>/`, only when the hook runs from inside the plugin root. Idle exit is unchanged | C1.17; the uninstall docs must cover the staged copies |
| D11 | (2026-09-25) An unreadable checkpoint store or a failed rehydration build also answers with the deferred note, never silence | follow-up to C1.16 (wave 3) |
| D12 | (2026-09-25) Live eval: the weak task checks in `tool-output-recall` and `seed-recall` (only `go vet`, which the untouched fixture passes) are **fixed before any confirmatory trial**, under a new task-set id and hash with a logged amendment | C5.5 (wave 3) |

Defaults taken without a separate question (owner may overrule): C1.9 host deny-rule honoring;
C7.2 hosted runners report-only for fsync-bound rows (Q1 third option); pre-registration amendments
A2–A4 (appended by `w2-eval2` before any confirmatory trial) accepted; `qompack eval` reports the
pre-registered intention-to-treat decision (failed trials already counted as failures) and lists the
failed trials, rather than forcing "inconclusive"; `test/fault`'s two slow absence waits stay
as they are (test time only; no hook is slow).

**Routing.** On 2026-09-22 the user asked for parallel "ultracode" workflow subagents. Children
therefore run as workflow subagents inheriting the coordinator's model (Opus 5.5), not the
2026-09-14 Opus 4.8 headless route the V6 plan names; that override is superseded for this
close-out by the user's later instruction and every record says so.

## Dispatch log

| Wave | Run | Workstreams (branch `closeout/<ws>`, worktree `../qompack-cx-<ws>`, base `cf31e01`) |
|---|---|---|
| 1 | `wf_16dd5d95-b3a` | `ingest` C1.1 · `e2e` C1.2/C1.3 · `config` C1.8 · `hostperm` C1.9 · `rollover` C1.10 · `perfstore` C2.6/C2.7 · `perfobs` C2.3–C2.5 · `eval` C5.4 · `linux` C3.4 + the six unreported Windows packages. Each: implement → adversarial review (two lenses for ingest/hostperm/rollover) → fix seat |
| 1b | `wf_a704d10a-845` | `packaging` C1.11 + C1.12 + C7.5 prep (implement → two-lens review → fix) |
| 1c | `wf_e1d0d082-a01` | `rehydrate-cap` C1.14 (D5), branched from `closeout/packaging` `32e1a37` |
| int | — | All ten wave-1/1b/1c branches merged `--no-ff` into `closeout/integration` (`../qompack-cx-int`) at `b070bbe`, with no textual conflicts; `go build ./...` and `go vet ./...` are clean. The rollover merge's `git merge` hung for about 2 h after creating its commit (no child process; the likely cause is a Windows file lock over thousands of new evidence files). The coordinator stopped its own process and ran `git merge --quit`, and the merge commit `ecaa08a` stands. Integrated gates `int1-windows-whole-tree` and `int1-linux-race` (non-root, ALL-NON-E2E) started together, with co-load declared |
| 2 | `wf_0d8775ab-04e` | **Produced nothing.** Every implementer hit the account's weekly usage limit ("resets Sep 25, 7am America/Toronto") after stalling and retrying; results `[null ×6]`, no commits. Planned: off `b070bbe`: `w2-sessionend` C1.15 + C1.13 remainder + drained control-line sidecars · `w2-lifetime` C1.16 + C1.17 + admin.shutdown reply loss + hermetic mcpop tests · `w2-hookout` C1.18 + C1.20 + two unclassified e2e reds · `w2-rollover2` D6 diagnostics, gcrun memory claim, lock-held lookups, race-suite duration · `w2-lint` C1.19 · `w2-eval2` first independent review of the eval harness + `qompack eval` wiring |
| 2b | `wf_b2b236ea-ef1` | 2026-09-25 re-run of wave 2 in the same worktrees, off `b070bbe`. Each agent was told to inspect the first attempt's leftovers (uncommitted SessionStart/rehydrate phase histograms in `w2-lifetime`; repro logs in `w2-hookout`/`w2-rollover2`) and adopt or discard them deliberately. Plus a new `w2-wintriage` for the one Windows red that reproduces alone (`TestSecurity_ArchivedTextIsDataNeverAnInstruction`) and two slow fault subcases |

Integrated gates run 1 (`b070bbe`; evidence `plans/sdd/V6-closeout/integration/runs/` on
`closeout/integration` `6b3db32`). Linux non-root `-race`, every non-e2e package: green except
the carried-defect guard (expected until Phase 2) and the co-load hot-path row. The Windows whole
tree is **invalid**: the host slept mid-run, and the 30-minute alarms fired at 58 minutes. Its reds,
re-run alone on 2026-09-25, all pass except `TestSecurity_ArchivedTextIsDataNeverAnInstruction`,
which is Windows-only and assigned to `w2-wintriage`.

Incident: at 17:31 a coordinator `SendMessage` to two running workflow agents (ingest, eval)
resumed a second copy of each in the same worktree. The ingest copy stood down after writing
`handoff-from-duplicate.md`; the eval copy was stopped at ~17:47. `COORDINATOR-DECISION.md` in
the eval worktree records which files each copy wrote, and they are kept as evidence.

Additional finding at dispatch: a root-run Linux `-race` pass of `3dab390` (container
`/work/linux-test-candidate-3dab390-artifacts`) retained a **data race** in `internal/daemon`
(`handleAdminShutdown`'s `sync.Once` vs `daemon.Run`) and failures in `test/guards`,
`test/integration` and `test/security` beyond the ingest family — assigned to the `linux` lane.

## Where things stand (found at takeover)

- The "pending" integrated whole-tree run on `65bc8d7` actually **finished its first 78 packages and
  was interrupted inside the package list** — `test/guards`, `test/integration`, `test/platform`,
  `test/release`, `test/security` and `tools/devtool` never reported. Three packages failed:
  `internal/daemon` (2 fixtures, corrected by `3dab390`), `test/e2e` (5) and `test/fault` (6).
- The `test/e2e`/`test/fault` failures are one **real product regression, reproduced in isolation**
  (no co-load): `TestE2E_ObserverThroughDaemon` indexes 1 of 44 events and never recovers, even
  after the 30 s idle drain. Prime suspect: the V6 same-session ordering gate (`c34acb4`,
  `internal/daemon/delivery_order.go`) defers every concurrently dispatched event to the drain.
- Every row of the 304-row inventory is still `implemented_unverified`; `verified_in_target` = none.
- Seven carried defects remain unresolved and block the V6 report through `test/guards`:
  SP06-D2, SP08-D1, SP09-D1, SP10-D1, SP20-D2 (performance), SP08-D3 (fixed in V6, not yet
  re-dispositioned) and SP20-D4 (journal capacity).
- About 100 V6-remediation evidence files are untracked in `../qompack-v6`.

---

## Phase 0 — Take over cleanly

- [x] **C0.1** *(cf31e01)* Finalize `runs/rc1-integrated-whole-tree.json` as *interrupted* with its observed
      failures preserved (it still says `running`).
- [x] **C0.2** *(cf31e01)* Commit the untracked V6-remediation evidence on `verify/v6` (evidence-only commit).
- [x] **C0.3** *(cf31e01, fb3a803, 36c3e88, 7a7b4e8)* Record this checklist and the takeover in the V6 ledger.

## Phase 1 — Fix the product defects that block a working plugin

- [x] **C1.1** *(fixed on `closeout/ingest`, merged in integration; integrated gates pending)* Root-cause and fix the live-ingest regression (events stranded behind the ordering
      gate and not recovered by the drain). Failing regression test first; fixes the observer,
      thin-slice and X10 e2e cases and the six `test/fault` cases, or each gets its own diagnosis.
- [x] **C1.2** *(four causes, fixed on `closeout/e2e` (3893fac, 8da4435, 9d795a0+3c369c4, 43051d5); the turn defect by C1.1 on `closeout/ingest`; merged; `w2-sessionend` re-verifies the test)* `TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting`: fsck reports a
      non-monotone turn (`turn 0 after turn 1`) and "unexpected entry in the capture tree".
      Diagnose: downstream of C1.1, or a real publication-audit/turn-assignment defect.
- [x] **C1.3** *(stale criterion after 00e0c98, corrected in 555e289; green on Windows and Linux)* `TestV1_ConfigPrecedenceReachesHookBehaviour/bare_hook_records_an_over_budget_delivery…`
      (no bounded prefix survives). Diagnose against `00e0c98` capture refusal.
- [ ] **C1.4** Confirm `3dab390`'s two daemon fixture corrections on the new candidate.
- [ ] **C1.5** V6-AUTH-1/2 (`1.13.4`, `1.17.12`, historical FAIL): re-run the real-capture
      security regressions on the fixed candidate; they must pass without weakening.
- [ ] **C1.6** V6-RECOVERY-1: confirm automatic startup accounting + fsck detection of publication
      gaps on the packaged bundle; state honestly whether automatic *recovery* exists.
- [ ] **C1.7** V6-RECOVERY-2: operator backup/verify/restore through the shipped CLI
      (`4a12eff`), pre- and post-new-write rollback rehearsed on the packaged bundle.
- [x] **C1.8** *(fixed on `closeout/config` (D8), merged; integrated gates pending)* SP-18's discovered defect: one bad config key must not make every hook capture
      nothing while `self-test` says `config.load ok` — verify or fix `config.LoadForCapture`.
- [x] **C1.9** *(fixed on `closeout/hostperm` incl. the Windows alias bypass found by review (D7), merged; integrated gates pending)* V6-HOST-1 (archive reads vs the host's deny rules): implement honoring Claude Code
      `permissions.deny` `Read(...)` rules from the user/project/local/managed settings files for
      every retrieval form, with fail-closed on unreadable settings, and document the residual gap
      (session-only/CLI-flag rules are invisible to a plugin). *Default taken unless the owner
      objects.*
- [x] **C1.10** *(rollover enabled by default on `closeout/rollover` with gates 1–5 (D2, D6), merged; D6 diagnostics in wave 2; integrated gates pending)* SP20-D4 journal capacity (65,536 deliveries / 64 MiB, after which capture stops):
      per the owner decision, either complete the rollover gates (old-reader, backup/restore,
      resource cost, GC segment retention) and enable it, or keep it default-off with the limit,
      its symptom and its recovery documented.

- [x] **C1.11** *(fixed on `closeout/packaging`, pending merge + integrated gates)* Windows hooks break without Git Bash: shipped hooks are shell form and fail under
      the host's PowerShell fallback (`ParserError: Unexpected token 'observe'`); the platform
      test's PowerShell row asserts a string that is not shipped. Move hooks to exec form with the
      exact per-target binary (`bin/qompack.exe` on Windows); prove on the real host.
- [x] **C1.12** *(fixed on `closeout/packaging`, real-host proven, pending merge)* Claude Code 2.1.280 rejects Qompack's `PreCompact` output
      (`hookSpecificOutput.hookEventName` not accepted), so its instructions never reach the
      summarizer and the validation error is replayed into post-compaction context. Conform every
      hook's output to the current documented schema; pin with contract tests and a real session.
- [ ] **C1.13** Session end must wait for that session's earlier queued or deferred events
      (from the e2e finding), and the idle drain only runs after 120 s of inactivity
      (`DetectAfterSeconds`), while the e2e tests assume a 30 s drain. Confirm C1.1's fix covers
      both, or follow up.

Found by the packaging workstream's real-host sessions (evidence on `closeout/packaging` under
`plans/sdd/V6-closeout/packaging/`):

- [x] **C1.14** *(fixed on `closeout/rehydrate-cap` `2148fa6`; live session: 3,048 units inline, overflow pointer resolved via MCP `expand`; pending merge)* (D5) The rehydration payload (budget up to ~12K tokens) exceeds the host's
      10,000-character `additionalContext` cap. When it does, Claude gets a file path and a
      2,000-char preview and is not asked to read the file. Fit the payload under the cap, report
      the rest as overflow with pointers, and revise Qompack.md §8.6 and ADR 0011.
- [ ] **C1.15** The `SessionEnd` flush hook is reported "Hook cancelled". Plugin hooks share a
      1.5 s SessionEnd budget, and the manifest's 20 s timeout does not raise it for plugin hooks.
      The flush hook must answer well inside 1.5 s, and nothing may depend on it (§8.2).
- [ ] **C1.16** Under load, a `SessionStart(source=compact)` took 10.3 s and answered `{}` at the
      10 s reply deadline, which lost that rehydration. It must answer reliably and fast.
- [ ] **C1.17** The resident daemon outlives the session and keeps the plugin's `qompack.exe`
      open, which left a half-deleted plugin extraction directory. The same thing can break plugin
      update or uninstall on Windows. Fix with idle exit, or by running from a copy outside the plugin root.
- [ ] **C1.18** The daemon still renders and records PreCompact summarizer instructions that no
      host accepts (`precompact.custom_instructions_accepted` warns). Retire the producer and keep
      the checkpoint.
- [ ] **C1.20** Other hook fields are still unbounded by the host cap: the SessionStart
      `degradeBanner` systemMessage (`internal/daemon/handlers.go`) quotes contract
      Expected/Observed values with no length limit, and the UserPromptSubmit thrash warning
      (`internal/observer`) joins lines without a count limit. Bound both and pin each with a
      `HostChars <= cap` test.
- [ ] **C1.19** Pre-existing gate failures on the base: `devtool lint` bindeps
      (`golang.org/x/sys/unix` via `internal/paths`), and `stubskips` reports four skips with
      non-permitted reasons in `internal/daemon`, `internal/hookio` and `internal/store` tests.

## Phase 2 — Carried defects (all must be `fixed` or `wontfix` before the V6 report)

- [ ] **C2.1** SP08-D3 — verify the V6 prompt-replay recovery and re-disposition to `fixed`.
- [ ] **C2.2** SP20-D4 — per C1.10.
- [ ] **C2.3** SP08-D1 OnToolUse 256 KB (B-C) — profile, real speed-up first.
- [ ] **C2.4** SP10-D1 checkpoint `Finalize` — profile, real speed-up first.
- [ ] **C2.5** SP09-D1 negknow `Open` — profile, real speed-up first.
- [ ] **C2.6** SP06-D2 PutBytes cold/warm and **C2.7** SP20-D2 `GetChunk` verify-on-read — measure
      on Linux first (Windows small-file/AV cost), speed up where possible.
- [ ] **C2.8** Any budget that still cannot be met after real work goes to the owner with quiet
      measured distributions for a re-budget or `wontfix` ruling; never silently.

## Phase 3 — Whole-tree verification on one frozen candidate

- [ ] **C3.1** Freeze the fixed candidate (commit, `git describe`, source snapshot hash).
- [ ] **C3.2** Windows whole tree `go run ./tools/devtool test` — every package reports, all green.
- [ ] **C3.3** Windows race: `devtool test-race` plus the product-child race lane
      (`QOMPACK_REQUIRE_CHILD_RACE=1`, child SHA recorded).
- [ ] **C3.4** Linux (Docker, Go 1.26.6): whole tree, `-race`, `test/e2e`, child-race set.
- [ ] **C3.5** `devtool fmt-check`, `devtool lint`, `go vet`, golangci-lint (Windows and Linux).
- [ ] **C3.6** Coverage floors (`devtool cover`), including `store`/`paths` ≥ 90 % on Linux.
- [ ] **C3.7** `gen-config-docs`/`gen-command-docs`/`gen-mcp-docs --check`, `licenses --check`,
      `govulncheck`, import allow-list.
- [ ] **C3.8** Replay gate (`devtool replay --ci`) and the recorded-corpus tier.
- [ ] **C3.9** Short fuzz pass over every nightly fuzz target (`TestNightlyFuzzMatrix` has no stub).
- [ ] **C3.10** `devtool plugin-validate` and `claude plugin validate` with the installed CLI
      (2.1.280, newer than SP-17's 2.1.263).
- [ ] **C3.11** Reproducible bundles: two builds per target byte-identical, all six targets.
- [ ] **C3.12** `release-check` (no tag) green.

## Phase 4 — Prove it works as an installed Claude Code plugin (real host, real model)

- [ ] **C4.1** Install the frozen bundle through the real marketplace flow into an isolated
      Claude Code profile; hooks, MCP server (`qompack mcp`) and the seven commands discovered.
- [ ] **C4.2** Real headless sessions: every hook event captured; `.qompack/` index counts match
      the transcript; `status`, `doctor`, `fsck` clean.
- [ ] **C4.3** Real compaction round trip: `PreCompact` checkpoint → `SessionStart source=compact`
      → bounded rehydration payload injected → the model recovers pre-compaction facts.
- [ ] **C4.4** The real model uses each MCP tool (`recall`, `expand`, `re_read`, `already_tried`,
      `record_eliminated`, `timeline`, `why`, `dropped`) with correct results and errors.
- [ ] **C4.5** Slash commands run in a real session and match `docs/commands.md`.
- [ ] **C4.6** Privacy in a real session: planted secrets never reach any durable surface;
      out-of-project and deny-ruled archived reads are refused by every retrieval form.
- [ ] **C4.7** Kill switches in a real session: recording off, reinjection off, each independent.
- [ ] **C4.8** Upgrade from the previous build, uninstall, reinstall; project work preserved;
      backup → restore → fsck certified.
- [ ] **C4.9** Degraded paths: daemon killed mid-session, unknown schema, unavailable object —
      the host session is never broken.
- [ ] **C4.10** UAT-01…12 executed per the owner decision (agent-run on the real host, or human),
      results written into `docs/uat.md` with evidence.
- [ ] **C4.11** Linux installed host if a Linux Claude Code can be run (Docker); macOS and
      windows/arm64 recorded `unknown` unless a runner is provided.

## Phase 5 — Benchmarks and evaluation that show it works well

- [ ] **C5.1** Quiet hot-path run: `bench-hotpath --iterations 5000 --warm-daemon` (B-A…B-F) on
      Windows and Linux, full distributions, no co-load.
- [ ] **C5.2** Carried-workload benchmarks `-count 5` (store/read/search, OnToolUse 256 KB,
      Finalize, negknow Open, GetChunk) before/after Phase 2.
- [ ] **C5.3** Deterministic replay evaluation (24-session phase-0 comparison) re-run on the frozen
      candidate; fraction-of-OPT reported as a diagnostic only.
- [ ] **C5.4** Implement `eval.LiveRunner` (today nil; `qompack eval --json` exits 1): drives real
      `claude -p` sessions with and without Qompack under forced compaction, records per-category
      usage from the host's own JSON, completion and constraint outcomes.
- [ ] **C5.5** Pre-declare the live task set, sample size and margins (before outcomes), then run
      stock vs Qompack trials with held-out and changing-requirement tasks; report uncertainty,
      failed trials and cost honestly.
- [ ] **C5.6** Resource cost: store growth per session, daemon RSS/CPU, hook latency seen by the
      host during the live trials.

## Phase 6 — Documentation, inventory and the report

- [ ] **C6.1** Docs match evidence: README status, `docs/release.md` scope table, install, UAT
      results, cannot-do/upstream-issues, troubleshooting; generated docs regenerated if code moved.
- [ ] **C6.2** `inventory-current.tsv`: every one of the 304 rows re-dispositioned from this
      candidate's evidence (`verified_in_target` only with an executed artifact); the 14 §3
      integration identifiers and the SP19/20/21 switches mapped.
- [ ] **C6.3** `CARRIED-DEFECTS.tsv` final dispositions; `test/guards` green against the report.
- [ ] **C6.4** V6 close-out report (the plan's §8 template), with independent final review.
- [ ] **C6.5** Tick the V6 plan's boxes with evidence links; `CHANGELOG.md` release entry.

## Phase 7 — Release (each step needs its own authorization)

- [ ] **C7.1** Release version chosen; `core.Version`, `plugin.json` and tag agree.
- [ ] **C7.2** Push `verify/v6`; hosted `ci.yml` + `nightly.yml` on the candidate; every red
      classified (hosted fsync tail per owner Q1: report-only on hosted runners, gated on the quiet
      reference host — default proposed).
- [ ] **C7.3** Merge `verify/v6` → `develop` → `main`; enable branch protection.
- [ ] **C7.4** `release-check --tag`; tag; `release.yml` publishes six-target bundles + checksums.
- [ ] **C7.5** Publish a marketplace manifest so users can add the plugin from the public repo;
      install it from GitHub on a clean profile and smoke-test. Design adopted (research,
      2026-09-22): six per-target `archive`-source entries (`qompack-<os>-<arch>`) pinned by
      sha256 to the GitHub Release zips; all six targets ship `.zip`; a `devtool marketplace`
      generator plus a publish-time PR workflow. Still open: whether the host keeps exec bits when
      it extracts a zip on linux/darwin (needs a published pre-release).
- [ ] **C7.6** Housekeeping with owner consent: ~85 stale worktrees (some hold uncommitted work),
      the dirty root `verify/v3` checkout, the orphaned fault-test daemon left by the interrupted run.

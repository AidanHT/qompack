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
| D13 | (2026-09-25) SessionEnd contract: the flush hook answers once the request is **durably accepted** (WAL + lease), and the daemon settles and ends the session **asynchronously**; a killed hook loses nothing because the request is replayed. Drain-replayed flushes must take the same async end, so drain budgets cannot truncate SessionEnd | C1.15 (`closeout/w2-sessionend`) + wave-3 follow-up |
| D14 | (2026-09-25) 00-ARCHITECTURE.md §2.5 amendment **ratified**: `golang.org/x/sys/unix` (exact path) joins the closed runtime-dependency list | C1.19 |
| D15 | (2026-09-25) Hook-field size ceilings **confirmed**: degrade banner 200 host chars per quoted value, 64 for the id, whole banner under 1,000; thrash warning at most 5 warnings × 360 chars plus a counted tail, under 2,000 | C1.20 |
| D16 | (2026-09-25) The pre-first-rotation backup Warn fires **once per daemon run** from 3/4 of the window until the first rotation. This supersedes D6's "once per store"; no new durable marker | C1.10/D6 |
| D17 | (2026-09-26) Cold start: `EnsureRunning` **honours a fresh spawn lock** instead of starting a second daemon, and session-start's pre-send step (staging plus poll) gets a **bound so the whole hook ends inside the 15 s manifest timeout**; the exact number comes to the owner before commit | wave 5 `w5-coldstart`, follow-up to w3-startroute item (3) |
| D18 | (2026-09-26) A session whose project root resolves to the **home directory is refused**: Qompack records nothing for it and says why (status/doctor and one host message); `~/.qompack` stays the user-global layer only | wave 5 `w5-home` |
| D19 | (2026-09-26) Criterion changes **ratified**: §12.1 counts one chance per prompt delivery of the probe's own session sent after the probe was minted (w3-startroute, incl. its `internal/contract` scope); x13 asserts the hook's wall-clock fallback spool per arm instead of in the cross-arm equality (w4-e2eflakes `78b33a1`) | closes those needs-owner items |
| D20 | (2026-09-26) SP08-D1: **keep the full post-write re-proof**; only pure redundancy is removed (each directory fsynced once per pass). The quiet C5.2 run decides B-C; a miss returns to the owner for re-budget or wontfix | wave 5 `w5-dirsync`; C2.3/C2.8 |
| D21 | (2026-09-26) Session-start budget (D17b) **approved with borrowing**: 1.5 s exit reserve (`hookExitReserve`), 10 s reply wait, 250 ms post-claim dial (`spawnClaimDialTimeout`), 10 s spawn-lock freshness (`spawnLockStaleAfter`), at least 1.5 s for a daemon spawned late; the find/start step may borrow idle reply time up to `doneBy − dial − compactAnswerBudget` (8.25 s), keeping D9's 5 s compact bound. The daemon accepting dials during startup and holding requests until startup completes (`2f4867f`) is **ratified** | wave 6 `w6-borrow` implements the borrow |
| D22 | (2026-09-26) 00-ARCHITECTURE §3.2 **amended**: `internal/config` may import `internal/paths` (no cycle), so config reads use the shared-read helper and `paths.Global` | wave 6 `w6-config` |
| D23 | (2026-09-26) `TestGuard_EveryProductReadIsClassified` **ratified** as a standing guard: every product file open is classified as a shared or ordinary read with a reason | w5-winfiles |
| D24 | (2026-09-26) Windows directory sync stays a **no-op on the NTFS-journaling premise**, documented with its residual risk; no Windows directory barrier is added | docs in wave 6 |
| D25 | (2026-09-27) New numbers **approved**: `captureConfigGoneBudget` 250 ms (the hook re-checks a config.json its Lstat found and its open did not; the Windows rename-replace gap measured 18.8–115.4 ms in 140,000 saves), test criterion `atomicSaveMaxAbsentRuns` 3, x09's `x9FlushGCBound(pending)` with `x9CleanupReserve` 21.5 s (composed of existing bounds), test-only `gcSerialBound` 30 s / `gcSerialTick` 1 ms | w6-config, w6-linuxrows, w6-gcserial |
| D26 | (2026-09-27) Durability costs **accepted**: record_eliminated made durable (about 3.6x on loaded Linux, about 70 ms/op on Windows, against B-F's 250 ms p95); payloads of 1 MiB or more fsync the payload and spool/ before the WAL line, inside B-B (2.32x on loaded Linux); one directory fsync per `qompack pin`/unpin process; the D24 residual that a Windows backup reported certified can revert after a power cut (verification refuses it, and it is retaken) | w6-ckptsync |
| D27 | (2026-09-27) Stray spawn claim: **fix now**. A daemon that loses the lock, or Stop, gives run/spawn.lock back, so a later flush can start a daemon at once; the failing diagnostic from w6-linuxrows is the red test | wave 7 |
| D28 | (2026-09-27) Linux timing rows are judged in a **separate isolated non-race timing pass** (as ci.yml's timing lane does). The -race suite still runs every other row; no budget changes. Applies to the hot-path row x11 (B-A/B-B 15 ms) and ShingleCap (10 ms) | phase3.sh |
| D29 | (2026-09-27) D21 borrow edge **accepted as is**: a compaction found at the borrow limit, or a late-spawned daemon keeping its 1.5 s grace, may get the client's "no answer" note and be spooled and replayed; no transit allowance | w6-borrow |
| D30 | (2026-09-27) GC serialization: a request that finds a pass running **queues for one follow-up pass**, which answers every waiter of that kind; no cross-process lock while GC runs only inside the daemon (a guard enforces it) | w6-gcserial |
| D31 | (2026-09-27) `idleRunBudget` (2 s) becomes a **soft pass budget** for the requested drain, the spool watcher and the idle drain: no new line after the budget is spent; the line in progress finishes under `drainLineDeadline` (5 s); a session end's own drains stay unbudgeted | w6-linuxrows |
| D32 | (2026-09-27) Windows Defender false positive (Trojan:Win32/Bearfoos ML) on Qompack builds: the **owner** adds a Defender exclusion on this host (Go build cache/temp and the worktrees) and submits a false-positive report to Microsoft; the coordinator documents it in troubleshooting and tracks code signing as a release item | owner + wave 7 docs |
| D33 | (2026-09-27) The owner **delegates every remaining decision** to the coordinator: "Don't ask any more questions. Just make the most reasonable action and approach that is user centric around the purpose of qompack." With the earlier "continue until qompack is all done and ready for production and usage to the public", this covers the Phase 2 dispositions, new bounds, and the D4 release steps (main, tag, Release, marketplace). Conditions the coordinator holds itself to: release only on a frozen candidate whose gates, live lane and evidence are complete and green (or every red has a recorded disposition), never weaken a check, never change the owner's system security settings (the D32 Defender steps stay the owner's), and record each decision here with its rationale. | all remaining phases |
| D34 | (2026-09-27, coordinator under D33) Live lane: (a) accept about 82 real sessions against D3's ~40–80 (lane 36 = 8/12/12/4 Haiku sessions, C5.5 40, 6 already used): better evidence beats a two-session overrun; (b) model sessions run in the real profile with a local-scope install in disposable projects, and C4.1's install/discovery half runs in an isolated CLAUDE_CONFIG_DIR, because credentials are never copied; real ~/.qompack logs and staged copies and the host's own transcripts are inherent to real sessions, and the lane cleans the staged copies it creates; (c) C4.11's Linux model sessions are recorded unknown (no login in the container); the no-model Linux half runs if the frozen linux/amd64 bundle sits beside the Windows one; (d) the candidate becomes verify/v6 (integration merged in), run from a clean detached worktree so the ignored dist/ tree and later evidence commits cannot affect the gates; (e) fix before release: self-test starting a daemon for a daemon-disabled project, and /qompack:checkpoint (route it or stop shipping it) — wave 7b | live lane, freeze |
| D35 | (2026-09-27, coordinator under D33) (a) D27's rule as built is accepted: `Lock.Release` gives run/spawn.lock back (it reaches every lock holder, including the cli writer lease where the production loser exits); the losing daemon does not delete it itself, which keeps the one-spurious-spawn-per-10-s throttle. (b) SP08-D3: client spools are replayed in host order (by first record's req.TS; capture-by-drain accepted for HotSpool and daemon-disabled mode), and the residual live-vs-spool race is ruled out of the host-order guarantee: a prompt captured with an earlier req.TS than an already-published turn is recorded with a Warn and a notice naming the substituted turn, so the rehydrator never presents a later prompt as the original request without saying so; published turns are not re-numbered; the docs state the shipped guarantee. (c) A SessionEnd flush that arrives while the running daemon is stopping waits in the spool until the next session: documented limitation (replayed, not lost). | wave 8 |
| D36 | (2026-09-27, coordinator under D33) (a) 0.3.0 ships **no manual checkpoint**: /qompack:checkpoint is removed (w7b-checkpoint option B), six commands ship; rehydration reads the checkpoint PreCompact seals at that compaction, so a manual seal would be superseded before anything read it; exit criterion SP14-M3-01 is retired with it and returns with any future checkpoint-now route (${CLAUDE_SESSION_ID} can bind the session). Qompack.md §7.5 gets a revision-log entry under D33. (b) `qompack daemon` itself refuses to run in a daemon-disabled project, so release.md §4's no-resident-process promise holds whoever starts it. | wave 7b, wave 8b |
| D37 | (2026-09-27, coordinator under D33) Inventory (C6.2), from `inventory-map.md` (`59c1578`, merged `85e5b4c`): (a) C5.2's quiet run is widened to every benchmark an inventory row names (1.1.16, 1.1.27, 1.2.12, 1.3.7, 1.4.14, 1.7.8, 1.7.10, 1.8.2, 1.10.17, 1.11.16, 1.12.17, 1.15.14, 1.16.11), so those rows get an executed artifact; (b) rows whose named symbols only ever existed in the plans are judged on the replacement tests the map names; (c) rows that no step can execute (1.16.5 warm-vs-cold delta, 1.16.10 missing ADR 0016, 1.17.3 binary-size check, 1.17.5/1.17.6 launcher split, 1.10.18 frontier-toggle pair, 1.17.19 until C7.2, the human half of 1.18.12 under D3) are dispositioned honestly as not verified in target, with the reason, never as passed; no new harness is built for them before release. | Phase 5–6 |
| D38 | (2026-09-28, coordinator under D33) SP08-D3's residual pid-reuse case (a hook process that reuses a pid appends to an older undrained client-<pid> file, so its host-later prompt can keep an earlier place within one pass) is **accepted for 0.3.0** as documented and flagged (counter, Warn, rehydrator host_order entry); closing it needs a spool-name or per-record merge redesign, which is not worth the risk at the freeze. The microsecond window between staged-copy verification and CreateProcess (pre-existing) is accepted likewise. | wave 8 |
| D39 | (2026-09-28, coordinator under D33) The hot-path row in **co-load** mode (`QOMPACK_UNDER_COLOAD=1`) reports, rather than fails on, the daemon's §8.1/§12.2 breach transition to spool submode: co-load already waives B-A/B-B/B-E's wall budgets (ADR 0010), and the transition is those budgets' consequence (w9: B-A n=1536 = 3 breach windows x 512 samples, delivered p99 180 ms loaded Windows / 106 ms container vs 15 ms). Co-load still requires the transition to be loud and named (degraded WARN, LOUD.log), the delivery ledger to add up and 0 events lost. Isolated mode is unchanged: no spool transition, B-A/B-B gated, undelivered samples certified as over budget. | wave 10 |
| D40 | (2026-09-28, coordinator under D33) w9's check (c), refuse a B-A row whose n is exactly the 2000 spawn samples, is kept: it closes the snapshot-level re-point the reviewer found; its only false red is a genuine run whose B-A shortfall is exactly 64, which breach transitions (multiples of 512) do not produce. | wave 9 |
| D41 | (2026-09-28, coordinator under D33) **B-A's default budget becomes platform-derived: `runtime.hotPath.budgetMs` defaults to max(15, L0IngestMs for the platform)**, i.e. Linux 15, Windows 50, macOS 40; a user-set value is unchanged. Reason: the daemon's B-A sample is recvTS-reqTS + handler time + 1 ms (handlers.go recordHotPathSample), so it contains B-B's durable ingest by construction, and the V5 ruling re-budgeted B-B per platform (50/40 ms, fsync before ACK) without carrying B-A along. With B-A at 15 ms, every Windows session trips the §8.1 breach detector after 3 x 512 samples and stays in spool submode with a degraded WARN (isolated Phase 3 runs on `a94a3fb`: B-A p99 26.6 / 22.5 ms, p50 16.4 / 15.4 ms, with B-B p99 12.3 ms). No new number: it composes two approved ones. Qompack.md / 00-ARCHITECTURE get a revision entry. | wave 11 |
| D42 | (2026-09-29, coordinator under D33) X11's V2-relative ceiling (B-A p99 <= 1.25 x V2's 3.072 ms) compared two different quantities: V2's B-A excluded the handler and ingest was not durable; today's B-A contains B-B's fsync (approved: fsync-before-ACK, SP20-D1, SP20-D6). It is **rebased, not relaxed**: X11 runs the harness twice in one test on the same host, without and with the observer + 5 000-entry ledger resident, and gates the ledger run's hook_controlled_observed p50 (the like-for-like, non-ingest share) at <= 1.25 x the paired no-ledger run's (the existing factor; no new number), reporting both runs' B-A p99 and the V2 figure for the record. The absolute B-A/B-B gates stay. **Amendment (2026-09-29, ratified):** the ceiling is max(1.25 x base p50, base p50 + one reported tick), the tick (1.024 ms) read from the product's obs histogram, not typed in: samples are whole milliseconds, so below 4 ticks 25 % is finer than the quantity can resolve and a quantization flip (1.024 to 2.048 ms on Linux) would fail X11 with the product unchanged; at 4 ticks and above (every Windows run) the rule is exactly 1.25x. Wave 12 also tightened X11: an isolated run fails on any deferral note, and a pair with unequal n is refused. | wave 12 |
| D43 | (2026-09-29, coordinator under D33) `config.Load` WARNs (never clamps) when a configured runtime.hotPath.budgetMs is below the effective runtime.budgets.l0IngestMs: such a setting makes every durable delivery a B-A breach and degrades sessions. | wave 12 |
| D44 | (2026-09-29, coordinator under D33) Spool submode not recovering within a session (hooks stop dialing, so the detector gets no clean windows; a new session or a daemon restart ends it) is **accepted for 0.3.0** now that D41 removes the systematic Windows trigger; documented in troubleshooting with the way out (start a new session, or `qompack daemon stop`). | wave 12 |
| D45 | (2026-09-29, coordinator under D33) Phase 4 on candidate 3 found real product defects the test suites missed (`live/report.md`). **Every defect that loses, hides, mis-attributes or mis-reports user context, blocks a restore after ordinary use, or makes a healthy session look broken is fixed before 0.3.0** (wave 13): MCP self-records at turn 0 (fsck exit 1 after any MCP call); the elimination ledger opened only at the first compaction, session scope stored as "", staleness not refreshed in-session, checkpoints with empty decisions/eliminated; the first prompt cut at 8 KiB mid-word; user corrections never reaching user_intent.evolution; a fork replacing the original intent; a pin made with the daemon up missing from the view and the next checkpoint; empty checkpoint pointers; restore failing after an idle exit (segments naming an unwritten checkpoint), after an MCP root's GC, and on a pre-upgrade store; the 256 KiB sentinel tail missing after one large Read (session degraded to passive); status reporting 2 of 9 FAILING in healthy sessions; doctor vs fsck disagreeing; maxResponseBytes bounding the content not the response; `path:` not a glob and `tool:` rejecting host names; and the small message defects. Host behaviours (file re-attachment after compaction, binary files decoded by the host) and unobservable surfaces (usage categories) are documented, not fixed. Live rows re-run on the fixed candidate. | wave 13 |
| D46 | (2026-09-29, coordinator under D33) Wave 13's proposals accepted: intentReadLimit 28,200 B (3 B/char x the 9,400-char payload ceiling) and checkpoint.EvolutionCeilingChars = the payload ceiling (composed, tied by test); evolution keeps the newest restatements under 64 entries and 9,400 chars with one named drop entry pointing at expand for the newest and oldest left out; a fork's parent is the session with the project's newest prompt at or before the fork's start (store.LatestPrompt), falling back to the newest checkpoint's session, and the fork inherits the parent's prompts before the fork, with a provenance entry in section 7; backup verify does a full scratch restore plus fsck --seal-check under .qompack/tmp (kept on failure); daemon.lock gains an additive `root` field and a foreign lock is judged by heartbeat alone (existing 90 s); legacy unlinked prompt sidecars read as published when their reference exists; sentinel scan also reads 256 KiB from the mint offset until the probe is first seen (the existing window size); maxResponseBytes bounds the result text of expand and re_read (escaping included), an explicit span starts at its offset and wins over full, and a page never cuts a redacted region; **no retrieval response carries fidelity or coverage** (docs corrected; UAT-02 step 6 and UAT-12 steps 3-4 are reworded to match, since the feature does not exist). Follow-ups for 0.3.0 (wave 14): the observer's stale segment after a scheduler roll and the unbound scheduler after a mid-session daemon restart; decisions from other sessions' project-scoped eliminations rank after this session's own, and seal-time decisions emit their DAG decision node; safeCut's spurious refusal. | wave 13-14 |
| D47 | (2026-09-29, coordinator under D33) The live rows candidate 3 failed are **re-run on candidate 4** (install 4, sessions 9, retrieval 7, recovery 2 = at most 22 real sessions; `coordinator/mkrerun.py` + `rerun-parts.js`), with the upgrade path taken from the public v0.2.0 tag where it can build a bundle. This raises the live total from D34's ~82 to about 104 sessions (35 used + 22 + C5.5's 40): a fix that was never seen working in the real host is not evidence, and the extra sessions are Haiku. Candidate 3's results stay in the Result blocks as one history line each. | Phase 4 |
| D48 | (2026-09-29, coordinator under D33) safeCut after wave 14b is accepted: never unsafe, never a spurious refusal, O(log n) Redact calls per region; a remaining class of non-maximal cuts where separate redacted regions interleave with raw prefixes is accepted (the page is shorter than the largest that fits, and next_span continues it). | wave 14b |

Defaults taken without a separate question (owner may overrule): (2026-09-26, w5-home) a session whose process root is home stays refused even when a later payload names a project below it; `config print` at home shows the user-global layer; HOME and USERPROFILE both count as the home; (w5-winfiles) case folding protects on darwin too, NTFS stream suffixes are stripped, 8.3 names are documented as out of scope; (2026-09-26) the §7.4 protected-path guard folds case on case-insensitive filesystems (Windows, macOS), with 8.3 short names and stream suffixes documented as out of scope for a textual guard; a delivery cut between its index record and its link keeps preserve-and-report (the replayed run does not emit the lost DAG node), documented as a known limitation; C1.9 host deny-rule honoring;
C7.2 hosted runners report-only for fsync-bound rows (Q1 third option); pre-registration amendments
A2–A4 (appended by `w2-eval2` before any confirmatory trial) accepted; `qompack eval` reports the
pre-registered intention-to-treat decision (failed trials already counted as failures) and lists the
failed trials, rather than forcing "inconclusive"; `test/fault`'s two slow absence waits stay
as they are (test time only; no hook is slow); pre-registration amendments A5 (any known open defect
disqualifies a confirmatory run) and A6 (the `sonnet` contingency may be confirmatory if every trial
reports one resolved model) accepted; the retired PreCompact focus-text renderer and the
compatibility-only decoders stay through 0.3.x (debug record only); staged daemon copies under
`~/.qompack/bin` are removed by hand after uninstall (documented); carry-bound repair beyond
"preserve and report" is post-0.3.0 work.

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
| 2b | `wf_b2b236ea-ef1` | 2026-09-25 re-run of wave 2 in the same worktrees, off `b070bbe`. Each agent was told to inspect the first attempt's leftovers (uncommitted SessionStart/rehydrate phase histograms in `w2-lifetime`; repro logs in `w2-hookout`/`w2-rollover2`) and adopt or discard them deliberately. Plus a new `w2-wintriage` for the one Windows red that reproduces alone (`TestSecurity_ArchivedTextIsDataNeverAnInstruction`) and two slow fault subcases. **Result:** 19 agents, all completed. Model requests stalled for every agent 15:13–17:17 local and each was restarted. Reviews: wintriage, lint and rollover2 sound; lifetime, eval2, hookout and sessionend needs-fixes, each resolved by its fix seat. All seven branches are merged (`dc3649f`…`54a4334`); the only conflict was two additive blocks in `daemon.go`; daemon, cli and ipc pass after the merge |
| 3 | `wf_eed51aa0-3c3` | off `54a4334`: `w3-startroute` D11 + replayed spooled session.start mints an undelivered probe (false degrade) + cold-start wait evaluation · `w3-e2ereds` three e2e reds that fail on `b070bbe` (V4/V5 tombstone, V5 elimination surfaces) · `w3-paths` `WriteAtomic` ancestor walk escaping into a real `~/.qompack` + x/sys bump for GO-2026-5024 + Linux `TestGC_DeadlineOvershoot…` · `w3-eval3` D12 task set v2 + intention-to-treat verdict + dry run |
| 4 | `wf_9c2ba09a-353` | off `31255da` (w3-e2ereds merged): `w4-syncs` SP08-D1. The leased PostToolUse path syncs one root about four times per capture; remove only passes provably redundant on the fresh-publish path, pinned by a pass-count seam and crash tests · `w4-e2eflakes` the Windows e2e rows that fail only under load (spawn-in-flight helper, rehydrate-state sharing violation, wave-3 touch-set, V3 hot path) |
| 3/4 resume | `wf_85543bfd-f18` | 2026-09-26: the four paused workstreams (`w3-startroute`, `w3-paths`, `w4-syncs`, `w4-e2eflakes`) resumed in their own worktrees. Each seat first merges `closeout/integration` `6aff949`, treats the earlier commits and edits as an unreviewed draft, re-runs any evidence the pause cut short, and finishes the task. Review base is `6aff949`. Pipeline: implement → review → fix → verify, the verify seat checking the fix seat's resolutions. Script: `coordinator/resume-w3w4.js`. **Done:** all four went through review, fix and verify (every verify seat: sound); reports committed on each branch; merged into integration (`04ccb68`, `f816953`, `dbede8c`, `3aeecc0`) without conflicts; runpatterns waivers `1c9012e`; golangci errcheck fix `90e1db3`. On `90e1db3`: build, vet, fmt-check and every lint sub-check except stubskips pass; the Linux x09 row passes 3/3 alone. |
| 5 | `wf_e5dc7399-b3c` | off `90e1db3`: `w5-coldstart` D17 spawn lock, the NOUP daemon, session-start pre-send bound · `w5-home` D18 home-directory refusal · `w5-winfiles` backup long paths, shared reads of atomically replaced files (readMarker and audit), §7.4 case folding, paths.Global in config loaders · `w5-helpers` one "gone" definition for every shutdown helper, leased atomicFaultStore, x13 counter lookup · `w5-dirsync` D20 one directory fsync per pass · `w5-deps` klauspost/compress v1.18.7 and x/mod v0.40.0. Pipeline implement → review → fix → verify. Script `coordinator/wave5.js` |
| 5 resume | `wf_8f93ec11-36e` | 2026-09-26 17:45: a host restart (Start menu, "Other (Unplanned)") killed wave 5 34 minutes in, before any implementer finished. It was relaunched at about 18:00 from `coordinator/wave5-resume.js`, where each seat gets a resume brief: the earlier seat's commits and edits are an unreviewed draft, a transcript digest shows where it stopped, and all evidence is re-run. Docker Desktop and the container were restarted; `/work` is intact. **Done:** all six were reviewed; coldstart, home, winfiles and helpers were fixed and then verified sound, and dirsync and deps had nits only. Merged into integration (`ca8e410`…`a91917a`) without textual conflicts; runpatterns waivers `f6095e2`. On `f6095e2`, build, vet (Windows and Linux), fmt and every lint sub-check but stubskips are green. The quiet Windows run of the ten merge-overlap packages is green except the expected carried-defect guard (`4196798`). |
| 6 | `wf_937a8468-45d` | off `f6095e2`: `w6-borrow` D21 borrow, spawner-side home refusal, stale comments · `w6-config` D22 config→paths and shared config reads, test-home isolation plus a guard · `w6-ckptsync` checkpoint Finalize/MANIFEST barriers, a missing-barrier audit of every writer, D24 docs · `w6-gcserial` one GC pass per store · `w6-linuxrows` Linux load-sensitive rows (x09, spooled start, ThinSlice, LaneOverflow, ShingleCap). Script `coordinator/wave6.js` |
| 6 resume | `wf_c0743004-a90` | 2026-09-27 morning: relaunched after the overnight pause from `coordinator/wave6-resume.js`. Each seat gets a resume brief: the earlier seat's commits and edits are an unreviewed draft, `coordinator/digests/w6-<ws>.txt` shows where it stopped, and all evidence is re-run. Keep-awake held; container checked. **Stopped 12:27 at the owner's request** (the laptop was lagging): w6-linuxrows had started a container load generator (22 CPU busy-loops plus 8 fsync loops for 90 min) and w6-borrow a Windows cold-start co-load diagnostic. Both were killed with every other wave-6 process. The container and Docker Desktop were stopped to free RAM. Heads at the stop: borrow `24eae18`, config `ecadcf4`, ckptsync `dc17169` (2 uncommitted), gcserial `4b33c19`, linuxrows `c08d008`. Digests `coordinator/digests/w6-<ws>-2.txt`; `wave6-resume.js` now caps load generators. |
| 6 resume 2 | `wf_8e58c74e-b50` | 2026-09-27, on the owner's "proceed": Docker Desktop and the container were restarted (`/work` intact) and wave 6 was relaunched from `coordinator/wave6-resume.js`, with each seat also reading its `w6-<ws>-2.txt` digest. Load generators are capped at 20 min and 8 busy processes. **Done:** all five reviewed; config, gcserial and linuxrows verified sound after their fix seats; borrow (one comment nit) and ckptsync (an EnsureLayout marker gap, minor) carried to wave 7. Owner decisions D25–D32. Reports committed on each branch, then merged into integration with no conflicts (`bc1b0e3`…`898bb8b`). On `898bb8b`: build, vet (Windows and Linux), fmt-check, the lint subset incl. runpatterns (Windows) and golangci-lint for GOOS=linux are green. |
| 7 | `wf_0b499fad-a33` | off `898bb8b`: `w7-spawnclaim` D27 stray spawn claim, the w6-borrow comment nit, D32 Defender docs (effort high) · `w7-layout` the EnsureLayout marker gap and a lifecycle comment nit (high) · `w7-sp08d3` C2.1 SP08-D3 disposition plus the C1.4/C1.5 confirmations (medium). Script `coordinator/wave7.js`. Alongside it, `wf_00490efb-ff4` (`coordinator/prep7.js`): `w7-livelane` finishes `live-uat.js` for Phase 4 with no real sessions (high), and `w7-docs` makes the docs match the integrated code (medium). D28 is applied to `coordinator/phase3.sh` (`dc4c1a3`): the -race runs declare co-load, and new `win-timing`, `win-e2e-timing`, `linux-timing` and `linux-e2e-timing` steps judge the wall-clock rows alone. |
| 7 prep | `wf_00490efb-ff4` | **Done.** `w7-livelane`: live-uat.js finished (parts install 8, sessions 12, retrieval 12, recovery 4; audit stage kept), new `homeguard.py` (byte hashes of the three plugin files, never opens credentials) and `live_driver.py` (host-seen hook latency from stream arrival, daemon RSS/CPU and store size per turn); review found a major (planted secrets could reach committed streams) and seven minors, resolved by its fix seat; merged into verify/v6 (`0a8a0c2`), report `w7-livelane/report.md`. `w7-docs`: six doc corrections, review sound; merges with wave 7. |
| 7b | `wf_ec574dc8-a1f` | off `898bb8b`: `w7b-selftest` self-test and other user commands honour runtime.daemon.enabled, install.md §3 for 2.1.280, the troubleshooting watcher wording (medium) · `w7b-checkpoint` /qompack:checkpoint routed for real or no longer shipped (high). Script `coordinator/wave7b.js` |
| 7 done | `wf_0b499fad-a33` | **Done.** `w7-spawnclaim` verified sound (Lock.Release gives the claim back; guard row renamed; Defender docs; code signing an open release item). `w7-layout` review sound. `w7-sp08d3`: C1.4 and C1.5 pass on Windows and Linux; SP08-D3 not fixed (client spools replayed in file-name order), new pinning evidence test, D35 ruling. Reports committed; merged with `w7-docs` into integration without conflicts (`729007c`…`17a42f6`). On `17a42f6`: build, vet (both OSes), fmt-check green; test/guards fails only the expected carried-defect rows. Early Linux run on `898bb8b` (run4, `03c2136`): whole tree green except the guard rename (fixed by w7-spawnclaim) and the hot-path row, whose 28% spool deferral is this container's fsync cost (C5.1 decides). |
| 8 | `wf_b1789f1b-147` | off `17a42f6`: `w8-sp08d3fix` D35(b) host-order replay and substitution notice, row to fixed (high) · `w8-stagerace` the Windows concurrent-staging failure seen once under load, plus the w7-spawnclaim nits and D35(c) docs (high). Script `coordinator/wave8.js`, built from wave7b.js by `coordinator/mkwave.py` |
| 7b done | `wf_ec574dc8-a1f` | **Done.** `w7b-selftest` verified sound: self-test, `qompack mcp` and the command frontends honour runtime.daemon.enabled (the latter two trusted a stale state.bin); install.md §3 and troubleshooting corrected. `w7b-checkpoint`: option B, /qompack:checkpoint no longer ships (D36); verify left one doc nit. Merged into integration; the troubleshooting.md conflict was first resolved by taking the branch's whole file, which dropped wave 7's Defender section, and was re-done as a three-way merge (`399a141`). |
| 8b | `wf_9fc9b4b8-2d9` | off `399a141`: `w8b-polish` D36(b) daemon refusal, wave-7b nits, six-command updates to 00-ARCHITECTURE, TRACEABILITY, SP-14 (SP14-M3-01 retired) and Qompack.md §7.5 with a revision-log entry (medium). Script `coordinator/wave8b.js` |
| 8b done | `wf_9fc9b4b8-2d9` | **Done**, review sound: `qompack daemon` refuses a daemon-disabled project with one line naming the key and layer (exit 1, the one exception to runDaemon's never-non-zero contract, ratified under D33); cannot-do/troubleshooting/install/packaging nits fixed against the code; Qompack.md v1.7 (§7.5 six commands) with its errata record and re-pinned revision digest; 00-ARCHITECTURE, TRACEABILITY and SP-14 (SP14-M3-01 retired) updated. Merged `15e3a25`. Left for Phase 6: 00-ARCHITECTURE §5.17's status paragraph still lists a checkpoint seq/size; inventory rows citing the deleted TestCheckpoint_* tests need a D36 disposition. |
| 8 resume | `wf_50e94c35-219`, `wf_5d1852de-53c` | 2026-09-28: relaunched after the overnight pause from `coordinator/wave8-resume.js` (w8-sp08d3fix, w8-stagerace) and `coordinator/quiet-prep-resume.js` (quiet.sh), each seat reading its digest; keep-awake held, container checked. |
| quiet prep done | `wf_5d1852de-53c` | `coordinator/quiet.sh` (steps c51-win, c51-linux, c52-win, c52-linux; base `cf31e01`; 10 balanced ABBA rounds; completeness check fails a step on a skipped or short benchmark; idleness warnings; homeguard snapshot) reviewed and fixed. The review found a **blocker in phase3.sh**: the D28 timing pattern held a raw 0x01 byte instead of ``, so the timing steps would have run no test and passed; fixed (`1085bbb`) with a guard that refuses a pattern without TestBudgetBF. Merged into verify/v6 (`25b976e`). |
| 8 done + freeze | `wf_50e94c35-219` | **Wave 8 done.** `w8-sp08d3fix` verified sound: client spools replay in host order, prompt records carry host stamps, a host-earlier later capture is counted, warned and named in the rehydration (no renumbering); SP08-D3 moved to **fixed**; residual (pid reuse within one pass) documented and flagged (D38: accepted). `w8-stagerace`: a real Windows product race (a losing spawner could not read the copy the winner had just renamed in, reported failure and deleted a correct copy) fixed with delete-sharing verification, keep-only-held-open, never install over an existing copy; verify left an SHA-map issue (coordinator note) and two comment nits (fixed by the coordinator, `6879a0d`). Merged (`ce661f0`, `8ba97ed`; the troubleshooting conflict resolved by hand, keeping both sides). On `8ba97ed`: build, vet, fmt, full lint subset, Linux golangci, test/docs green; test/guards fails only the five carried perf rows. **Frozen** as verify/v6 `a94a3fb` (C3.1). Phase 3 lanes A (Windows) and B (Linux) started with daytime caps (container 8 CPUs, GOFLAGS=-p=6). |
| phase 3 stop | — | 2026-09-28: Claude Code stopped both Phase 3 lanes and the keep-awake because the host was critically low on memory (the Docker VM held about 8.2 GB with the Linux -race whole tree running beside the Windows whole tree; 7 GB free of 31.4). The stopped shells' test processes kept running and were killed (Windows and every `cx-p3` process in the container). No Phase 3 step completed; partial records under `phase3/` are not evidence. Not restarted: Claude Code's rule is to restart only when the owner asks. Plan for the restart: run the lanes one after the other (not side by side), cap the container's memory as well as its CPUs, and run overnight. |
| phase 3 restart | — | 2026-09-28, on the owner's "continue now": one chain, strictly sequential: lane B (linux-tree, linux-e2e, linux-child), then lane A (win-tree, win-race, lint, cover, gens, fuzz, bundles; GOFLAGS=-p=6), then the D28 timing steps (win-timing, win-e2e-timing, linux-timing, linux-e2e-timing). The container is capped at 8 CPUs and 8 GiB (`docker update --cpus 8 --memory 8g --memory-swap 8g`); the stopped run's partial records moved to `phase3/stopped-1/`. Progress in `phase3/chain.log`. quiet.sh and release-check follow once these are read. |
| phase 3 run 2 | — | Lane B (Linux) finished on `a94a3fb` (`phase3/chain-1.log`): linux-e2e and linux-child **pass**; linux-tree fails only the carried-defect guard (expected), the hot-path row (judged in the timing steps) and one new test flake, pathstest's telemetry cleanup race. win-tree finished: 72 packages pass (59 from the go test cache of the same candidate's first run), failing only the carried-defect guard and the hot-path row's structural check. Claude Code then stopped the chain a second time for low memory, during win-race; the orphaned processes were killed and the idle container stopped. The owner said continue: the chain resumes at win-race (`chain-2.log`, GOFLAGS=-p=4, container stopped), then win-timing and win-e2e-timing; the Linux timing steps follow with the container restarted. Wave 9 (`wf_0ae7e642-c8d`, `coordinator/wave9.js`, off `8ba97ed`) fixes the two test defects in parallel. |
| 9 done | `wf_0ae7e642-c8d` | **Wave 9 done**, merged (`8245d4e`). pathstest: the go command's telemetry (mode local when no mode file) started a detached sidecar that wrote into the isolated home while TempDir cleanup removed it; fixed with TEST_TELEMETRY_DIR plus an `off` mode file where the config dir follows HOME (Linux proof owed). Hot path: `ba.N > bd.N` replaced by an exact ledger identity (n + disclosed shortfall = 2064, B-A <= B-B, B-A != 2000, shortfall covered by DEFERRED, 0 lost). Root cause of the deferrals: the breach detector moves to spool after 3 x 512 over-budget windows (loaded host). Two new findings: the co-load row then fails its no-spool assertion (D39) and one full test/integration run under Phase 3 load reported **18 of 2130 hot-path events LOST** (in neither l0_ingest nor a spool line) — possibly a product defect. win-race on `a94a3fb` (`chain-2.log`): fails only the carried-defect guard and the old sourcing check (fixed in w9). |
| 10 | see below | Wave 10 off `8245d4e`: `lostev` (xhigh) root-causes the 18 lost events (product spool drop vs harness accounting race with the client-spool drain) and fixes it; `coloadspool` (high) implements D39 and w9's two comment nits. Load-bearing runs never overlap the isolated timing steps. |
| 10 done + freeze 2 | `wf_620a0532-b9a` | **Wave 10 done**, merged (`bef894a`, `fc5289c`). lostev: **no event was ever lost**; the harness's count-based guard missed deferrals the C1.13 client-spool watcher had already replayed into the store mid-run (proved on a forced-breach run: 26 replayed, all in the store, old guard said 20 LOST); the census now finds every sent (session, tool_use_id) in a spool, the WAL or the store, and a spooled duplicate can no longer mask a loss. coloadspool: D39 implemented (hotpathJudgeSpool). Three nits fixed by the coordinator (`1c87163`, `0dc36f0`, runpatterns waiver). Candidate 2 frozen as verify/v6 `aad1ceb` (test-only diff from `a94a3fb`), then **superseded before any run** by D41. Phase 3 on `a94a3fb` completed: lint, gens, fuzz (25 targets), bundles (79 files identical) pass; cover fails only on the hot-path row; win-timing (possibly overlapped by wave 10's diagnostics) fails B-A certification and TestGC_DeadlineTruncatesAndResumes (511 not < 256); win-e2e-timing fails only TestV3_HotPathUnchangedWithLedgerResident, B-A again (the D41 defect). |
| 11 | see below | Wave 11 off `fc5289c`: `babudget` (high) implements D41. Then candidate 3 is frozen and Phase 3 re-runs on it overnight, followed by quiet.sh. |
| 11 resume | `wf_002f8b87-e4d` | 2026-09-29, on the owner's "continue": keep-awake restarted; wave 11 relaunched from `coordinator/wave11-resume.js` (the stopped seat's three commits and untracked runs kept; X11's V2-relative ceiling to be evidenced, not changed). Container stays stopped. |
| 11 done | `wf_002f8b87-e4d` | **Wave 11 done**, merged (`cf727b7`). D41 implemented: budgetMs default = max(15, platform l0IngestMs) (15/50/40), schema golden, generated docs, 00-ARCHITECTURE, Qompack.md v1.8. Isolated Windows: TestIntegration_HotPathWarmWithRealResidentState **passes** (B-A p99 32.8 ms / 50, B-B p99 14.3 / 50, no spool); the degrade test still breaches as designed. X11 fails only its V2-relative ceiling (D42). Spool recovery finding: D44. |
| 12 | see below | Wave 12 off `cf727b7`: `x11pair` (high) implements D42, D43, D44's docs, and wave 11's comment nits (daemon/cmd doc comments stating a fixed 15 ms, ERRATA red-log wording, 'Owner decision D41' wording). Then candidate 3. |
| 12 done | `wf_740f3ac1-2de` | **Wave 12 done**. D42 implemented (paired no-ledger/ledger runs, hook_controlled_observed p50 gate, floor amendment ratified); D43 WARN in config.Load; D44 documented in troubleshooting (a new session or the daemon's idle exit ends spool submode; killing the daemon does not, state.bin keeps hot=spool); wave 11 nits fixed. Isolated Windows X11 **passes** (668 s; both runs' B-A p99 36.9 / 28.7 ms vs 50; observed p50 6.1 / 6.1 ms). |
| freeze 3 + live | `wf_3f8fa5f3-e45` | Wave 12 merged (`f490b16`). **Candidate 3 frozen** as verify/v6 `d5598eb` (`phase3/c3/CANDIDATE.md`); `../qompack-cx-cand` moved to it. Phase 4 live lane launched on it (`coordinator/live-uat-c3.js`, worktree `../qompack-cx-live`, bundle `qompack-bundles/c3/`), sequential real sessions, daytime-light. Tonight after the lane: `coordinator/overnight.sh` (timing, Linux proofs, quiet.sh), then the whole-tree re-runs on candidate 3. |
| live done | `wf_3f8fa5f3-e45` | **Phase 4 on candidate 3**: 29 real sessions of the 36 budgeted, audit verdict needs-fixes; evidence merged into integration (`84d2512`, `live/report.md`). Pass: C4.1 install, C4.5 six commands, C4.6 privacy (0 secret hits), C4.7 kill switches, C4.9 degraded paths, C4.10 Result blocks, C1.6, C1.7 (Read-only store only). Fail: C4.2 (fsck), C4.3 (whole-record and corrections), C4.4 (ledger tools before compaction), C4.8 (restore); UAT 6 pass / 6 fail. D45 routes the fixes. Candidate 3 is superseded; the overnight quiet chain waits for candidate 4. |
| 13 | see below | Wave 13 off `84d2512`, seven seats: `ledger` (xhigh), `intent` (xhigh), `restore` (xhigh), `pinsckpt` (high), `diag` (high), `mcpresp` (high), plus audit doc fixes inside `mcpresp`. -p 2 each, no timing rows (daytime). |
| 13 done | `wf_3da1508e-fe4` | **Wave 13 done** (24 agents): ledger (MCP self-records at the live turn, ledger opened on first MCP use, caller session scope, refresh-on-read staleness, eliminations and decisions sealed at PreCompact, why provenance, timeline), intent (whole-record original or named overflow, corrections into evolution, fork keeps the parent's intent), restore (idle-exit segment ordering and compatible dangling claims, GC-tombstoned MCP roots, legacy sidecars, truthful restore note, foreign lock, missing-object reason), pinsckpt (pins view refreshed while the daemon runs, checkpoint pointers), diag (sentinel scan from the mint offset, healthy-session contract assertions, doctor = fsck, status reasons and percentiles), mcpresp (response bound, glob `path:`, host tool names, messages, docs, UAT audit fixes). Each seat RED-before-fix, reviewed, fixed, verified (five sound; mcpresp one minor left, wave 14). Merged into integration `a8c7433` (two conflicts in internal/checkpoint resolved by keeping both sides). |
| 14 done / 14b | `wf_e9768966-e7a`, `wf_029d4490-109` | Integration fix `a72fff1` (a wave 13 reader of the append-only index/roots.jsonl classified; merged-tree packages then pass but for the carried-defect guard). **Wave 14**: observer follows every scheduler roll (DAG nodes, SessionEnd closes the successor) and the scheduler binds on a restarted daemon's first hook (verified sound, merged); decisions: this session's own rank first, seal-time decisions emit their DAG node (sound, merged). safecut fixed the spurious refusal but its verifier found a content-triggered latency regression (a raw URI user/dotenv key prefix makes safeCut O(L log n) Redact calls, seconds to minutes): **wave 14b** reworks it before candidate 4. |
| freeze 4 + re-run | `wf_8b477987-691` | 14b merged (`dab0031`; D48). Claude Code stopped the keep-awake, the 22:00 timer and the merged-tree check at 20:00 for low host memory; no orphans; restarted on the owner's "continue". Merged-tree checks pass but the carried perf rows. **Candidate 4 frozen** as verify/v6 `9f6a2fad` (`phase3/c4-CANDIDATE.md`), bundles `qompack-bundles/c4/`. Live re-run of candidate 3's failed rows launched (D47, `coordinator/live-rerun-c4.js`, worktree `../qompack-cx-live` on closeout/live4). |

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

## PAUSED 2026-09-28 23:10 (America/Toronto): resume here

Paused at the owner's request ("I will continue tomorrow"). Wave 11 (`wf_fc02f288-db0`, seat `babudget`, D41) was
stopped with TaskStop mid-verification; its one live `go test` tree was killed. Nothing of the close-out is running:
the container is stopped, Docker Desktop is left running, the keep-awake is released. Phase 3 chain-2 had finished.

State:
- Integration `closeout/integration` `fc5289c3` (`../qompack-cx-int`) holds waves 1-10.
- verify/v6 froze candidate 2 (`aad1ceb`), which D41 supersedes: no gate ran on it.
- Wave 11 is in `../qompack-cx-w11-babudget` (branch `closeout/w11-babudget`, base `fc5289c`), with three
  unreviewed commits: `748f43ef` config (B-A default = max(15, platform L0IngestMs)), `9700950b` docs(arch) and
  `c3782687` Qompack.md v1.8. Its evidence under `plans/sdd/V6-closeout/w11-babudget/runs/` is untracked. Digest:
  `coordinator/digests/w11-impl-babudget.txt`.
- The isolated Windows hot-path row **passes** with D41 (B-A n=2064, p99 32.8 ms vs 50, no spool).
  `TestV3_HotPathUnchangedWithLedgerResident` alone fails its separate X11 ceiling, B-A p99 within 25 % of V2's
  recorded 3.072 ms (got 24.6 ms). That ceiling predates the durable WAL and needs a coordinator decision from the
  resumed seat's evidence.

Resume, in order:
1. Start the keep-awake (`coordinator/keepawake.ps1` with a sentinel).
2. `Workflow({scriptPath: "../qompack-v6/plans/sdd/V6-closeout/coordinator/wave11-resume.js"})`.
3. Decide the X11 ceiling (D42) and render the report (wfreport.py, shacheck.sh, runpatterns, rpwaive.py).
4. Merge into integration and freeze candidate 3 on verify/v6. Point `../qompack-cx-cand` at it: detached, same
   directory, so the go test cache reuses the unchanged packages.
5. At night, with nothing else running: `sh coordinator/overnight.sh <cand> <sha> plans/sdd/V6-closeout/phase3/c3`
   runs the D28 timing, the waves 9-10 Linux proofs and quiet.sh C5.1/C5.2.
6. Then:
   - re-run the whole tree on candidate 3 with daytime caps (win-tree, win-race, linux-tree/e2e/child, lint, cover,
     gens, fuzz, bundles);
   - carried-defect dispositions (C2.3-C2.8) from the quiet numbers;
   - release-check;
   - Phase 4 live lane, the C5.5 eval, Phase 6 docs and report, Phase 7 release.

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
- [x] **C1.13** *(client-spool watcher replays client spools while their session is active, and a drain defers a spool copy whose worker is still publishing; `closeout/w2-sessionend`, merged into `closeout/integration` `54a4334`; integrated gates pending)* Session end must wait for that session's earlier queued or deferred events
      (from the e2e finding), and the idle drain only runs after 120 s of inactivity
      (`DetectAfterSeconds`), while the e2e tests assume a 30 s drain. Confirm C1.1's fix covers
      both, or follow up.

Found by the packaging workstream's real-host sessions (evidence on `closeout/packaging` under
`plans/sdd/V6-closeout/packaging/`):

- [x] **C1.14** *(fixed on `closeout/rehydrate-cap` `2148fa6`; live session: 3,048 units inline, overflow pointer resolved via MCP `expand`; pending merge)* (D5) The rehydration payload (budget up to ~12K tokens) exceeds the host's
      10,000-character `additionalContext` cap. When it does, Claude gets a file path and a
      2,000-char preview and is not asked to read the file. Fit the payload under the cap, report
      the rest as overflow with pointers, and revise Qompack.md §8.6 and ADR 0011.
- [x] **C1.15** *(D13: the flush hook answers once durably accepted (65–86 ms after a burst, was 3.5–4.3 s), and the daemon ends the session asynchronously; drain-replayed flushes are handed to the same async end; the flush has its own 500 ms ACK wait, derived from the host's 1.5 s budget; `closeout/w2-sessionend`, merged into `closeout/integration` `54a4334`; integrated gates pending)* The `SessionEnd` flush hook is reported "Hook cancelled". Plugin hooks share a
      1.5 s SessionEnd budget, and the manifest's 20 s timeout does not raise it for plugin hooks.
      The flush hook must answer well inside 1.5 s, and nothing may depend on it (§8.2).
- [x] **C1.16** *(D9: the rehydration now starts before phase-1 writes and runs outside the observer lock; compact SessionStart p99 1,853→661 ms under external fsync load, and an explicit deferred note replaces `{}`; `closeout/w2-lifetime`, merged into `closeout/integration` `54a4334`; integrated gates pending; D11 follow-up in wave 3)* Under load, a `SessionStart(source=compact)` took 10.3 s and answered `{}` at the
      10 s reply deadline, which lost that rehydration. It must answer reliably and fast.
- [x] **C1.17** *(D10: Windows daemon runs from a verified staged copy under `~/.qompack/bin/<sha256>/`, also on MCP lazy spawn; `closeout/w2-lifetime`, merged into `closeout/integration` `54a4334`; integrated gates pending)* The resident daemon outlives the session and keeps the plugin's `qompack.exe`
      open, which left a half-deleted plugin extraction directory. The same thing can break plugin
      update or uninstall on Windows. Fix with idle exit, or by running from a copy outside the plugin root.
- [x] **C1.18** *(PreCompact instruction producer retired; checkpoint kept; contract row honest; `closeout/w2-hookout`, merged into `closeout/integration` `54a4334`; integrated gates pending)* The daemon still renders and records PreCompact summarizer instructions that no
      host accepts (`precompact.custom_instructions_accepted` warns). Retire the producer and keep
      the checkpoint.
- [x] **C1.20** *(D15 ceilings, pinned by `HostChars` tests with pathological inputs; `closeout/w2-hookout`, merged into `closeout/integration` `54a4334`; integrated gates pending)* Other hook fields are still unbounded by the host cap: the SessionStart
      `degradeBanner` systemMessage (`internal/daemon/handlers.go`) quotes contract
      Expected/Observed values with no length limit, and the UserPromptSubmit thrash warning
      (`internal/observer`) joins lines without a count limit. Bound both and pin each with a
      `HostChars <= cap` test.
- [x] **C1.19** *(bindeps ratified by D14; the four skips now run on real junctions; `runpatterns` fixed in the reports; `closeout/w2-lint`, merged into `closeout/integration` `54a4334`; integrated gates pending)* Pre-existing gate failures on the base: `devtool lint` bindeps
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

- [x] **C3.1** *(`a94a3fb`, `v0.2.0-1417-ga94a3fb9`, source sha256 `c9971ea4…`; `plans/sdd/V6-closeout/phase3/CANDIDATE.md`)* Freeze the fixed candidate (commit, `git describe`, source snapshot hash).
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
      Claude Code profile; hooks, MCP server (`qompack mcp`) and the six commands (D36) discovered.
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
- [x] **C5.4** *(`eval.LiveRunner` + `devtool live-eval` delivered on `closeout/eval`; first independent review and fixes in `closeout/w2-eval2`; `qompack eval` wired; merged into `closeout/integration` `54a4334`; integrated gates pending)* Implement `eval.LiveRunner` (today nil; `qompack eval --json` exits 1): drives real
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

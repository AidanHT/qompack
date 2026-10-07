# V6 close-out report — Qompack 0.3.0, from the blocked V6 checkpoint to candidate 8

**Decision: NOT YET RELEASE READY.** This is a draft of C6.4. Candidate 8's second freeze
(integration `275165e9`, D72(b), D73(c)) is the release candidate, and the evidence that would accept
it does not exist yet. Every cell that belongs to it is marked `[OWED: ...]` below. No cell from an
earlier candidate is copied in as a run on this one. The report becomes a release decision only when
every `[OWED: ...]` cell holds an artifact path and no red is left without a recorded disposition
(D33's release condition, D66's exit rule). No tag, Release, merge to `main` or marketplace step is
authorized by this report (D4, C7.3-C7.5).

`plans/V6-report.md` (the 2026-09-20 BLOCKED checkpoint) stays immutable. This report records the
close-out that followed it. The working ledger is `plans/V6-CLOSEOUT-CHECKLIST.md` (decisions D1-D73,
the dispatch log, phases C0-C7), and every ruling cited here as Dnn is a row of that table.

## 0. Branch, HEAD and dirty baseline

| Item | Identity |
|---|---|
| Ledger branch | `verify/v6`, worktree `../qompack-v6`; this draft is written on `closeout/c6-report` from `verify/v6` `73242853` (D73) |
| Integration branch | `closeout/integration`, worktree `../qompack-cx-int`, head `275165e9` (waves 1-23 merged) |
| Close-out base | `cf31e01` (C0.1-C0.3: the interrupted `65bc8d7` run finalized, V6-remediation evidence committed) |
| V6 checkpoint candidate at takeover | `65bc8d7` plus fixture commit `3dab390` |
| Release candidate | Candidate 8, second freeze of integration `275165e9`: commit [OWED: second-freeze commit on verify/v6], tree [OWED: tree id], `git describe` [OWED: describe string], `sha256(git archive)` [OWED: source snapshot hash] |
| Frozen bundles | `qompack-bundles/c8` (second freeze): windows-amd64 BUNDLE.json sha256 [OWED: BUNDLE.json hash], six bin/ sha256 [OWED: bin/ hashes per target], host validation [OWED: `claude plugin validate --strict --json` outcome] |
| Superseded first freeze | `424f0d08` (integration `77374c3c`, tree `6d1def7c`); its records moved to `phase3/c8-freeze1` and `qompack-bundles/c8-freeze1` (D71(b)) |
| Root checkout | The main repository stays on `verify/v3` `7f92af5` with its historical dirt untouched (C7.6 housekeeping is owed and needs owner consent) |

Citations of integration commits resolve on `closeout/integration`; citations of `phase3/`,
`coordinator/` and the ledger resolve on `verify/v6`. A short SHA quoted here was checked to be
reachable from one of those two branches.

## 1. Date and environment

Close-out executed 2026-09-22 to 2026-10-06 (America/Toronto); this draft 2026-10-06.

| Item | Value |
|---|---|
| Reference host | Windows 11 Home 10.0.26200 (25H2 build 10.0.26200.9457 in the candidate 7 lane), Intel Core Ultra 7 155H, 22 logical CPUs, 31.4 GB RAM; timing taken on AC only (D57(d)), the store under a D32-excluded path (D53(h)) |
| Linux | Docker Desktop / WSL2 container, Go 1.26.6, non-root, capped at 8 CPUs and 8 GiB; valid for CPU- and read-bound rows, not fsync-bound ones (D53(b)) |
| macOS, windows/arm64, linux/arm64 | Hosted runners only (ci.yml, nightly.yml); no local runner. Installed-host macOS and arm64 sessions: `unknown` (C4.11) |
| Toolchain | `go 1.26`, `toolchain go1.26.6` (go.mod at `275165e9`) |
| Claude Code host | 2.1.280 (live lanes, C3.10) |
| Model in live sessions | claude-haiku-4-5-20251001 in every candidate 7 lane session; C5.5 analysis parameters in the pre-registration |
| Plugin version | `0.3.0` in `internal/core.Version` and `plugin.json` at `275165e9` (C7.1, D1) |
| Routing | Workflow subagents inheriting the coordinator's Opus 5.5, by the user's 2026-09-22 instruction, superseding the V6 plan's Opus 4.8 headless route for this close-out (ledger, "Routing") |

## 2. Candidates and why each was superseded

| # | Commit | Integration | Superseded because |
|---|---|---|---|
| 1 | `a94a3fb` (2026-09-28) | `8ba97ed` | Phase 3 found the hot-path accounting gap (18 "lost" events, later proved a harness count) and the co-load spool transition (D39); waves 9 and 10 |
| 2 | `aad1ceb` | test-only diff from `a94a3fb` | Superseded before any run: B-A's 15 ms default contained B-B's durable ingest, so every Windows session tripped the breach detector (D41) |
| 3 | `d5598eb` | `f490b16` | Its Phase 4 lane (29 sessions, `live/report.md`) failed C4.2, C4.3, C4.4, C4.8 and 6 of 12 UAT rows on real defects (D45); waves 13-14b |
| 4 | `9f6a2fad` | `46a4fefd` | Its re-run (22 sessions, `live/report-c4.md`) failed UAT-03, UAT-05, UAT-06, UAT-09, C4.3, C4.4 (D49, D50); wave 15 |
| 5 | `0d06ab12` | `af60666b` | Isolated timing failed three stale e2e rows and one live defect (X01 re-stamping); the goal-and-metrics audit (`audit/goal-metrics-audit.md`) is accepted (D53); wave 16 |
| 6 | `99d0b18c` | `9a56b305` | Replaced by candidate 7 = candidate 6 plus C7.1 and wave 17 (D57(c)); its machine evidence carried by byte proof; no live lane ran on it |
| 7 | `d20309c0` | `b31d0753` | Its lane (20 sessions, `live/report-c7.md`) failed UAT-05, UAT-12/C4.6 and C4.5 (D59), and the w17c review found a drain-budget defect (D58(c)); waves 18-19i |
| 8, freeze 1 | `424f0d08` (2026-10-05) | `77374c3c` | The diff-only verify (`wf_32da2f0b-11c`) found a major privacy leak in `pathNamedWithheld` (D71); wave 23 |
| 8, freeze 2 | [OWED: second-freeze commit] | `275165e9` | Release candidate; acceptance owed (section 9) |

## 3. Waves, seats and rulings

Every seat ran implement, adversarial review, fix and (from wave 3) verify inside its workflow, in its
own `closeout/<ws>` branch and `../qompack-cx-<ws>` worktree, and was merged `--no-ff` into
`closeout/integration`.

**Wave 1** (`wf_16dd5d95-b3a`, 1b `wf_a704d10a-845`, 1c `wf_e1d0d082-a01`). Seats ingest (C1.1, the
live-ingest regression behind the ordering gate), e2e (C1.2, C1.3), config (C1.8, D8 fail-closed
`runtime.redact`/`runtime.mode`), hostperm (C1.9, D7 saved-settings deny rules), rollover (C1.10,
D2/D6 rollover on by default), perfstore and perfobs (C2.3-C2.7), eval (C5.4 LiveRunner), linux (C3.4);
packaging (C1.11 exec-form hooks, C1.12 PreCompact output schema, C7.5 prep); rehydrate-cap (C1.14, D5
payload under the 10,000-character cap, `2148fa6`). All ten branches merged at `b070bbe`; the rollover
merge commit `ecaa08a` stands after a hung `git merge` was quit.

**Wave 2** (`wf_0d8775ab-04e` produced nothing at the weekly usage limit; 2b `wf_b2b236ea-ef1`).
w2-sessionend (C1.15, C1.13, D13 async session end), w2-lifetime (C1.16 compact SessionStart, D9 5 s
bound and deferred note; C1.17 Windows staged copy, D10), w2-hookout (C1.18 PreCompact producer retired,
C1.20 host-char ceilings, D15), w2-rollover2 (D6 diagnostics, D16), w2-lint (C1.19, D14 `x/sys/unix`),
w2-eval2 (first review of the eval harness), w2-wintriage. Merged `dc3649f`...`54a4334`.

**Waves 3 and 4** (`wf_eed51aa0-3c3`, `wf_9c2ba09a-353`, resumed `wf_85543bfd-f18`). w3-startroute (D11
deferred note on store failures, false-degrade probe), w3-e2ereds, w3-paths (`WriteAtomic` ancestor
walk, GO-2026-5024 bump), w3-eval3 (D12 task set `qompack-live-v2`); w4-syncs (SP08-D1 redundant root
syncs), w4-e2eflakes. Merged `04ccb68`, `f816953`, `dbede8c`, `3aeecc0`; waivers `1c9012e`, `90e1db3`.
Criterion changes ratified in D19.

**Wave 5** (`wf_e5dc7399-b3c`, resumed `wf_8f93ec11-36e` after an unplanned host restart). coldstart
(D17 spawn lock, pre-send bound), home (D18 home-directory refusal), winfiles (long paths, shared reads,
case folding; D23 guard), helpers, dirsync (D20 one directory fsync per pass), deps. Merged
`ca8e410`...`a91917a`; waivers `f6095e2`.

**Wave 6** (`wf_937a8468-45d`, resumed `wf_c0743004-a90` and `wf_8e58c74e-b50`). borrow (D21, D29),
config (D22, D25), ckptsync (D24 NTFS premise, D26 durability costs), gcserial (D30), linuxrows (D28
isolated timing pass, D31 soft pass budget). The first resume was stopped at the owner's request when a
load generator lagged the laptop; load generators are capped since. Merged `bc1b0e3`...`898bb8b`.

**Wave 7** (`wf_0b499fad-a33`, prep `wf_00490efb-ff4`, 7b `wf_ec574dc8-a1f`). spawnclaim (D27, D35(a)),
layout, sp08d3 (C1.4 and C1.5 pass on both OSes), livelane (`live-uat.js`, `homeguard.py`,
`live_driver.py`, merged `0a8a0c2`), docs; 7b selftest (commands honour `runtime.daemon.enabled`) and
checkpoint (D36: `/qompack:checkpoint` no longer ships, six commands). Merged `729007c`...`17a42f6`, then
`399a141` (three-way redo of a troubleshooting conflict that had dropped wave 7's Defender section).

**Wave 8** (`wf_b1789f1b-147`, resumed `wf_50e94c35-219`; 8b `wf_9fc9b4b8-2d9`; quiet prep
`wf_5d1852de-53c`). sp08d3fix (D35(b) host-order replay, SP08-D3 to `fixed`, D38 residual), stagerace (a
real Windows concurrent-staging race); 8b polish (D36(b) `qompack daemon` refusal, Qompack.md v1.7,
SP14-M3-01 retired); `quiet.sh`, whose review found a blocker in `phase3.sh` (a raw 0x01 byte made the
timing steps select nothing, fixed `1085bbb`). Merged `ce661f0`, `8ba97ed`, `15e3a25`, `25b976e`;
candidate 1 frozen.

**Waves 9 and 10** (`wf_0ae7e642-c8d`, `wf_620a0532-b9a`). pathstest telemetry race; the hot-path
ledger identity replacing a count comparison (`8245d4e`); lostev proved no event was ever lost (the
harness missed replayed deferrals) and rebuilt the census; coloadspool (D39, D40). Merged `bef894a`,
`fc5289c`; candidate 2 frozen and superseded by D41.

**Waves 11 and 12** (`wf_002f8b87-e4d`, `wf_740f3ac1-2de`). babudget (D41 platform-derived B-A
default, Qompack.md v1.8, `cf727b7`); x11pair (D42 paired X11 ceiling with its tick amendment, D43, D44
docs). Merged `f490b16`; candidate 3 frozen.

**Waves 13 and 14** (`wf_3da1508e-fe4`; `wf_e9768966-e7a`, 14b `wf_029d4490-109`). Seven seats under
D45 and D46: ledger, intent, restore, pinsckpt, diag, mcpresp (merged `a8c7433`, reader fix `a72fff1`);
wave 14's observer roll following, decision ranking, and safecut; 14b's safecut rework (D48, `dab0031`).
Candidate 4 frozen.

**Wave 15** (`wf_0a7ad63f-671`; 15a `wf_3f954a82-13d`; 15b `wf_44d938d8-58c`; 15c `wf_be7002a0-f70`).
rehydrate, ledger, services, docs under D49; 15a paging and snapshot under D50 (`96df358`); 15b docsb
(`a38a2b6`); 15c pubscan under D51 (one `os.Root` per pass, the pass yields to capture). Coordinator
fixes `a127a938`, `40db2e19`; merged `fe6b27f`, `f1ad272e`, `1718166`, `d84e1a7`, waiver `d5c9c53`.
Candidate 5 frozen.

**Wave 16** (`wf_90777431-3b4`; 16b `wf_747e62d8-a28`; 16c `wf_5e8aadd4-6ae`; 16d `wf_900edcee-56b`;
16e `wf_360586e9-18d`; 16f `wf_fc5fef3b-1b3`). e2erows, spoolseal (PreCompact after client spools,
slow-disk wording), relhyg (LICENSE and notices in every bundle), ci, winci (the 8.3 short-name
deny-rule bypass, a privacy defect); 16b settle, polish, cirec, cover (D55); 16c settle2, tidy (the
escaped-bracket 8.3 fail-open, `0a022893`); 16d sealrow, warnlate (D56(a) a real undelivered-warning
defect); 16e hpcover, settlewin (D56(c)); 16f rearm, settlekick, hpdepth (D56(d)-(f)). Merged
`ab6f9640`, `9d31280e`, `f90eb566`, `958edd2c`, `f91037bc`, `4ad76e32`, `63b9200a`, `89fc36dd`,
`496229c0`, `9649708e`, `32004b52`, `8e3600bd`, `40709576`, `df4d27ca`, `8fe6c1f4`, `c93b505f`,
`6d83c855`, `cb8490fc`; coordinator `61b0cd66`. Candidate 6 frozen.

**Wave 17** (`wf_bd12d061-33a`; 17b `wf_10516e89-86a`; 17c `wf_9e58aa2f-e6d`). ci (D57(a) hosted
release gates under Q1), release (version 0.3.0, `-buildvcs=false`, pre-release flag), inventory (C6.2
on candidates 6 and 7), docs, x11win (D57(d) battery invalidates reference timing; e1 on AC passed 3/3,
D57(g)); 17b docs2 and inventory2 (`docs/release-notes/v0.3.0.md` written); 17c spoolwatch (D58(b),
a test-timing red, criterion change recorded). Merged `9e41d7d0`, `70ed43d7`, `fea7c485`, `1ab0a438`,
`b31d0753`, `8c815770`, `edfbd6d5`, `6fcfc66d`. Candidate 7 frozen.

**Wave 18** (`wf_3a6c59cd-17f`, `wf_aed1c8a1-01a`). ssfires (D58(d), live finding F-C48-1, `8b5adfde`)
and passprogress (D58(c), D60(a): a spent drain pass counts only progress, `4dcd3432`).

**Wave 19** (`wf_d5ae67fb-6ab`; 19b `wf_4e6db3de-c66`; 19c `wf_558a3480-754`; 19d `wf_38a4c903-991`;
19e `wf_0c9ed8ca-c1d`; 19f `wf_cab8691e-9b2`; 19g `wf_a9b6cc1b-a92`; 19h `wf_b50111eb-587`; 19i
`wf_2081a1f9-7ba`). forkwork, loudcfg, statusorder and docs under D59 (`edafe3ee`, `c2f78e67`,
`0d920996`, `00f333a6`, the lane report committed verbatim `8bd80c85`); the rehydration block's
privacy screen through five rulings: D60(c) round 2, D61 screen, D63 whitelist, D64 closing rules,
D66(e) fail-closed after 19i; cmdconnect (D60(e), D61(c), `0cbb1bff`). Rehydrate merged at `71e5133d`;
19h `eca33155`.

**Audit 1 and wave 20** (`wf_6e041296-253`, `wf_a18b8846-180`). The first whole-tree audit of
`738d67c7` (10 dimensions, 156 agents) left 69 confirmed findings: 1 blocker, 22 major, 46 minor. Nine
seats fixed them: drain, redeliver, status, config, forkwork, docs, cover, sessionend, night (D62,
including (a) out-of-order memo finality and (i) the D31 amendment). Merged `f905ec9c`; night seat
`067fab8f`..`f97b1a46` on `verify/v6`.

**Wave 21** (`wf_792967ff-952`). e2e, polish and night (D65: the C5.2 night in package chunks, never on
battery; `6b18b874`..`ba38a406` on `verify/v6`). Integration head `2bf29705`.

**Audit 2 and wave 22** (`wf_27022a07-0e7`). The last whole-tree audit (D66(a)): 14 dimensions over
`2bf29705`, 202 agents, 87 survivors (3 blockers, 16 major, 68 minor; 7 refuted, 34 nits). Seats clock,
docs, w15carry, config, cliwork, rows, redeliver, spawn, contract and rehydrate, plus 19h and 19i (D67).
Merged `2a2e8f6c`; the hosted fixture fix `552822dc` integrated as `77374c3c` (D68). The account's
agent limit replaced the diff-only verify with mechanical gates (D68(a)). Candidate 8, freeze 1.

**Wave 23** (`wf_a7f89258-bde`). The diff-only verify ran once the limit lifted (`wf_32da2f0b-11c`, D71)
and found one major in `internal/rehydrate` path-value screening. w23-privacy (9 commits, `a4ec12b9`)
fixed it red-first plus a second major (valueNameForm) and three rounds of minors; w23-docs (4 commits,
`7d351d37`) fixed the troubleshooting and plan minors. Merged `cbb3a7f5`, `275165e9`. The privacy seat's
fourth review left 4 minors and a nit that did not converge; they are known issue 16 (D72(a)). The static
root-cause run `wf_8efe4557-020` classed the integration CI reds as test defects (D73).

## 4. Inventory and exit criteria

The 304 original row IDs (1.1.1-1.18.14) and the SP19/20/21 additions live in
`plans/sdd/V6-remediation/inventory-current.tsv`, with the old-to-new assertion map and evidence-step
map in `plans/sdd/V6-closeout/inventory-map.tsv` and `inventory-c6-map.md`. No row was added, dropped
or renumbered.

| Candidate | Disposition (304 rows) |
|---|---|
| 7 (history, `c7_result`) | 251 `verified_in_target`, 39 `partial_verified`, 7 `unknown`, 3 `unsupported`, 3 `documented`, 1 `implemented_unverified` |
| 8, second freeze | [OWED: C6.2 `c8_result`/`c8_evidence` columns, written after the C5.2 nights and the live re-check by the inventory seat (D67(k))] |

The candidate 7 column is not candidate 8's result. Rows that no step can execute are never marked
`verified_in_target` (D37(c)): 1.16.5, 1.16.10, 1.17.3, 1.17.5, 1.17.6, 1.10.18, 1.17.19 until
C7.2, and the human half of 1.18.12 under D3. A C5.2 measurement carries only where every file the
benchmark executes is byte-unchanged (D57(e)); candidate 8 changes `internal/config/config.go`, so C5.2 is
re-measured in full (D62(b)).

| Exit obligation (V6 plan section 2) | Status on candidate 8 |
|---|---|
| R2 run map, candidate identity, every incomplete result | Partial: the ledger and `phase3/` record every run to freeze 1; [OWED: second-freeze run map in `phase3/c8/`] |
| SP01-18 families and SP19/20/21 additions reconciled | [OWED: C6.2] |
| SP17-M7, SP18-M7, UAT-01-12 on the shipped package | Candidate 7 lane evidence plus [OWED: candidate 8 live re-check] |
| M0-M6 enabled capabilities with target evidence | [OWED: C6.2] |
| Old/new readers, backup, cutover, rollback rehearsed | C1.7 passed on candidate 4 (realistic store, pre- and post-new-write restores) and in candidate 7's install part (D59, D67(f)) |
| No hidden mandatory privacy, fidelity, recovery or regression blocker | The five V6-report blockers are closed (section 7); known issues 15-16 are disclosed privacy residuals of rare spellings; [OWED: C5.5 verdict for task regression] |

## 5. Cross-component integration identifiers (V6 plan section 3)

| ID | Status on candidate 8 |
|---|---|
| 3.1 packaged real session | `implemented_unverified` until [OWED: candidate 8 live re-check]; candidate 7 lane passed UAT-01, C4.1 |
| 3.2 launcher semantics | Exec-form hooks (C1.11) proven on Windows; macOS/Linux installed hosts `unknown` (C4.11) |
| 3.3 documented commands | Six commands (D36); C4.5 failed on candidate 7 and was fixed in wave 19 statusorder; [OWED: C4.5 on candidate 8] |
| 3.4 elimination/staleness through MCP after upgrade | Candidate 7 C4.4 passed; [OWED: C4.x carry note naming changed files] |
| 3.5 ephemeral eviction | Native assertion retired (V6 plan); `unsupported` |
| 3.6 fsck repair and preservation | Deterministic rows; C1.6 live [OWED: candidate 8 resilience part, D67(f)] |
| 3.7 doctor/status agreement | Wave 13 diag, wave 19/20 status; [OWED: two status reads with two or more sessions, D60(f)] |
| 3.8 config reference matches binary | Generated-docs checks in the pre-freeze gate; [OWED: second-freeze pre-freeze record] |
| 3.9 install/upgrade/uninstall | Candidate 7 C4.8 and UAT-12 upgrade leg passed; carried by diff [OWED: carry note] |
| 3.10 degraded passive | Candidate 7 C4.9 (a)(b) passed; [OWED: C4.9 settingsVersion leg on candidate 8, D59] |
| 3.11 no secret, no network | Candidate 7 C4.6 failed (F1, a denied path in a tool summary), fixed in waves 19-23; [OWED: C4.6 and UAT-12 sessions A/B on candidate 8] |
| 3.12 checkpoint to rehydration | Candidate 7 UAT-05 failed (silent drop at 150/150), fixed (D59(b), D60(ii)); [OWED: UAT-05 run 2 on candidate 8] |
| 3.13 reproducible artifacts | Candidate 7 hosted bundles byte-identical to local (D58(a)); [OWED: second-freeze bundles step and hosted release-dry-run comparison] |
| 3.14 hot path with every subsystem resident | X11 verified on AC on candidate 6's tree (D57(g)); [OWED: win-x11-alone on the second freeze] |

## 6. Commands actually run, and evidence paths

Every candidate's freeze record is `plans/sdd/V6-closeout/phase3/CANDIDATE.md` and
`phase3/c4-CANDIDATE.md` through `phase3/c7-CANDIDATE.md`; the chains, prefreeze and quiet runs are under
`phase3/c3/` to `phase3/c7/`; the drivers are `coordinator/phase3.sh`, `quiet.sh`, `prefreeze.sh`,
`overnight-c6.sh`, `c7-night.sh`, `c8-night.sh` and `overnight-c8.sh`. Each seat's report and run logs are
under `plans/sdd/V6-closeout/<seat>/`. Live lanes: `live/report.md`, `live/report-c4.md`,
`live/report-c7.md`, `live/sessions.tsv`.

Hosted CI, every run the ledger records:

| Run | Head | Outcome |
|---|---|---|
| ci.yml `36816905394`, nightly `36820740318` | candidate 5 | Reds routed to wave 16 (8.3 bypass, macOS, elevated runners) |
| ci.yml `36905843834` | `07a748cb` (integration `ae601390`) | Reds classified to 16d/16e |
| nightly `36955043924`, ci.yml `36955046276` | candidate 6 | Nightly green; two reds disposed (D57(a), D57(b)) |
| nightly `36981711009`, ci.yml `36981590450` | candidate 7 | Nightly green; one Windows test-timing red (D58(b)) |
| ci.yml `37361841760` | integration `2a2e8f6c` | Four reds, one fixture cause (D68) |
| ci.yml `37365986864` attempts 1 and 2 | integration `77374c3c` | Runner loss, unacquired jobs, then three test-only reds (D69, D70(b)) |
| ci.yml `37409857403`, nightly `37409848444` | freeze 1 `424f0d08` | Green on all three OSes (D70); superseded with freeze 1 |
| ci.yml `37533889761` | integration `275165e9` | Two test-only reds (D73) |
| ci.yml and nightly | candidate 8, second freeze | [OWED: hosted ci.yml run id and outcome on the frozen SHA] [OWED: nightly run id and outcome on the frozen SHA] |

Phase 3 on the second freeze (C3.2-C3.12):

| Gate | Evidence |
|---|---|
| Merged-tree plan lint, test/guards, test/docs (c8-night step 3) | [OWED: phase3/c8/night.log step 3] |
| Pre-freeze check of integration (gate, integration, testpkgs, internal, e2efunc) | [OWED: phase3/c8/prefreeze record] |
| Windows whole tree and -race (C3.2, C3.3) | [OWED: overnight run win-race and tree records] |
| Linux non-root -race, e2e, child race (C3.4) | Wave 23 Linux gate passed on `275165e9`: internal/rehydrate 1721/0, rehydratetest 14/0, internal/daemon 2078/0 (D73, `qompack-audit/wave23/linux`); whole tree [OWED: overnight run linux records] |
| fmt, lint, vet, golangci (C3.5) | [OWED: second-freeze gate record] |
| Coverage floors (C3.6) | [OWED: second-freeze cover record] |
| Generated docs, licenses, govulncheck, import allow-list (C3.7) | [OWED: second-freeze gate record] |
| Replay gate (C3.8, C5.3) | [OWED: replay gate report on the frozen SHA (D67(i))]; recorded-corpus tier not exercised (D67(g), known issue 13) |
| Fuzz pass (C3.9) | [OWED: nightly fuzz on the frozen SHA] |
| Plugin validation (C3.10) | [OWED: host validation of qompack-bundles/c8] |
| Reproducible bundles (C3.11) | [OWED: bundles step and hosted release-dry-run byte comparison] |
| release-check (C3.12), and `release-check --tag` on the reference host on AC (D57(a)) | [OWED: overnight-c8.sh release-check record] |
| Isolated timing: win-timing, win-e2e-timing, win-x11-alone (D28, D57(d)) | [OWED: overnight run timing records, power verdict VALID] |

## 7. Blockers from the V6 checkpoint, and how each closed

| V6-report finding | Close-out |
|---|---|
| V6-AUTH-1, V6-AUTH-2 (pathless authority, hash addresses bypass) | C1.5: the real-capture regressions pass on Windows and Linux without weakening (wave 7 sp08d3) |
| V6-RECOVERY-1 (publication gaps, no automatic accounting) | Startup publication accounting and fsck detection; live pass on candidate 3; C1.6's live evidence on candidate 8 is [OWED: resilience part of the live re-check (D67(f))]. Automatic *recovery* is not claimed beyond preserve-and-report |
| V6-RECOVERY-2 (no operator backup/restore) | C1.7: backup/verify/restore through the shipped CLI, pre- and post-new-write, passed on candidate 4 and in candidate 7's install part |
| V6-HOST-1 (host deny rules invisible) | C1.9 and D7: saved-settings Read deny/ask rules honoured fail-closed; session-only rules, CLI flags and hook policies stay invisible (documented); the 8.3 bypass (wave 16) and the hostperm depth fail-open (D56(d)) fixed |

## 8. Evaluation layers

| Layer | Evidence |
|---|---|
| Deterministic and mechanism | Phase 3 on every candidate, pre-freeze checks, hosted CI, the replay gate. Second freeze: [OWED: overnight run, hosted CI and nightly on the frozen SHA] |
| Closed-loop on the real host | Agent-executed sessions on the owner's host (D3, not human UAT): candidate 3 29 sessions, candidate 4 22, candidate 7 20, about 71 in all. Candidate 8 re-check: [OWED: live re-check C4.x and UAT rows (D59, D60(f), D67(f))]. Stock versus Qompack trials: [OWED: C5.5 confirmatory run] |
| Recovery and failure | test/fault, C1.7 live restores, C4.9 degraded paths, kill switches C4.7 (candidate 3). Candidate 8: [OWED: C1.6 and C4.9 settingsVersion leg] |

**Primary outcomes.** Task completion, constraint and regression, and recoverability are C5.5's H1-H3.
[OWED: C5.5 verdict, quoted verbatim under A8]. A8's rule (D53(g)): inferior, or an H2 regression,
blocks the release; not-applicable re-runs once; non-inferior, superior or inconclusive releases with
the verdict quoted; better recovery is claimed only if H3's lower bound is above 0.

**Sample and interval rationale.** Pre-registered in `plans/sdd/V6-closeout/eval/preregistration.md`:
task set `qompack-live-v2` (D12), 10 tasks x 2 trials x 2 arms = 40 sessions, 20 per arm, Newcombe 95 %
hybrid-score interval, non-inferiority margin 0.20. At success near 0.7 the half-width is about 0.27, so
the study detects breakage and large effects only; equal arms below 95 % read inconclusive by design.
No second batch (D53(g)). Run `--confirmatory`, dry run first, on the frozen candidate 8 bundle by its
BUNDLE.json SHA (D58(e)).

**Resource cost (C5.6).** Candidate 7's lane figures stay candidate 7's (D58(e)):
`live/rerun-c7/C5.6/summary.md` and `data.json`. D53(i) held on candidate 7: 474 hook calls, 0 failures, 0
timeouts (D59). Candidate 8's D53(i) count across the re-check and C5.5: [OWED: host-reported hook
failures and timeouts, and host-seen p50/p95 per hook for the release notes].

**Quiet benchmarks.** C5.1 on the second freeze: [OWED: C5.1 quiet hot-path run, Windows on AC]. C5.2
on the second freeze: [OWED: C5.2 nights, every chunk on AC]. Candidate 6's C5.1 (B-A p99 30.7 ms, B-B
24.6 ms against 50; D57(d)) and candidate 5's C5.2 (D54) are history, not candidate 8 evidence. Linux
fsync-bound rows (B-A, B-B) are not verified in target on the container (D53(b)), and hosted figures
never become constants (Q1).

## 9. Candidate 8 acceptance cells

| Cell | Evidence |
|---|---|
| Second freeze record (`phase3/c8-CANDIDATE.md`) | [OWED: written once the overnight run is classified (D70(c))] |
| Overnight run (overnight-c8.sh) | [OWED: phase3/c8 chain records with power verdicts] |
| Hosted ci.yml on the frozen SHA | [OWED: run id and per-job outcome] |
| Hosted nightly on the frozen SHA | [OWED: run id and per-job outcome] |
| C5.1 | [OWED: quiet hot-path run on AC] |
| C5.2 | [OWED: C5.2 nights incl. the C1.16 rig in its no-extra-load and in-process conditions (D65(a))] |
| Live re-check C4.x and UAT (`mkrecheck8.py`, about 8 sessions plus D60(f)'s rows) | [OWED: UAT-05 run 2; UAT-12 sessions A and B with C4.6; UAT-06 with C4.5; status after a mid-session compaction (D58(d)); C4.9 settingsVersion leg; rehydrate privacy and notice rows under a deny rule; two status reads with two or more sessions; C1.6 resilience part] |
| C5.5 | [OWED: confirmatory verdict under A8] |
| C6.2 inventory dispositions | [OWED: c8_result and c8_evidence for all 304 rows] |
| C6.4 independent final review | [OWED: non-authoring review of this report] |
| Pre-release, HTTPS install rehearsal, bin/ byte comparison (D53(h)) | [OWED: after the cells above; separate authorization under D4/D33] |

## 10. Known issues and test-only residuals

The user-facing items go to CHANGELOG.md and `docs/release-notes/v0.3.0.md` in the docs-only
descendant (D68(b)); the full text is `plans/sdd/V6-closeout/w22-known-issues.md`.

1. Idle windows across two daemon idle exits can degrade a project to passive recording.
2. On a score tie a fork's checkpoint can keep a parent decision ahead of its own (named in `dropped()`).
3. With a Read deny or ask rule, path-keyed drops past 64 host judgements show as "(path withheld)",
   and a section 6 summary naming one may be withheld (D71(d)); over-withholding only.
4. A rehydration build costs about 1.7x candidate 7's (about 2 ms), far inside the 5 s budget.
5. A hook's invalid-configuration log line does not name the file, variable or flag.
6. With `runtime.mode` off, a hook whose delivery read fails still appends one log line.
7. After a restart, the first compaction reads a very large newest prompt once in full.
8. A PreCompact that misses its 15 s deadline can be sealed twice; a checkpoint is always kept.
9. A SessionStart replayed from a spool marks its session live.
10. A segment close owed by a cut delivery can be lost in memory (D67(b)).
11. One scheduler token account per project (D67(b); docs/cannot-do.md).
12. A cut-short connect to a busy listener can spawn a duplicate daemon that exits (D61(c)).
13. The recorded-corpus replay tier is not exercised (D67(g)).
14. macOS assumes a case-insensitive volume (D67(m)).
15. A rooted path glued after `+ # ) ] } ! ^` inside one path-named value is not judged separately.
16. Rare spellings of a path inside a tool argument can still be shown: a truncated `~[name`, a login
    holding `@`, `$` or a non-ASCII letter, a non-identifier `%VAR%`, look-alike Unicode slashes or dots,
    and a non-canonical spelling of a refused path with a file name under 3 bytes (D72(a)).

Test-only residuals (ledger only, never the release notes): the hook-path connect budget rows (cliwork
FR1-2); drain rows applying `drainLineDeadline` to a real dispatch; wall-clock margins in the spawn rows;
the drain pass-cost row's 35-60 s; four rehydrate rows on a long Windows TEMP; two product-timer rows
(clock); the Windows CI leg's budget and the deterministic drain-row strand (D70(b), D73(2));
`TestPromptWarning_SlowDurableAcceptIsLateForTheClient`'s missing join (D70(b)); the per-prompt
history.json fsync and capWentOn's quadratic trim (D71(d)); `TestFault_DaemonKilledMidIngest`'s
dials-only wait (D73(1)); the unverified PreCompact settle lead (D73(b)). Second-freeze additions:
[OWED: anything the overnight run, hosted CI or live re-check appends to w22-known-issues.md].

## 11. Carried defects (C6.3)

`plans/CARRIED-DEFECTS.tsv` is the source of record. Every one of its 28 rows is `fixed` or `wontfix`
on `verify/v6` `73242853`, and the file is byte-identical on integration `275165e9`. No row is `open` or
`deferred:`, so `TestCarriedDefects_WaveReportRequiresResolution` has nothing to fail on. This draft
changes no row: every disposition the ledger settles is already recorded.

| Row | Status | Ledger | Confirming evidence on candidate 8 |
|---|---|---|---|
| SP08-D3 | `fixed` | C2.1, D35(b), D38 | Evidence test in the second-freeze tree [OWED: overnight run] |
| SP20-D4 | `fixed` | C1.10, C2.2, D2, D6, D16 | Evidence test in the second-freeze tree [OWED: overnight run] |
| SP09-D1, SP10-D1, SP20-D2 | `fixed` | C2.4, C2.5, C2.7, D54 | Rests on candidate 5's C5.2; [OWED: C5.2 on the second freeze (D62(b), D71(c))] |
| SP06-D2, SP08-D1 | `wontfix` | C2.3, C2.6, D54 | Rests on candidate 5's C5.2; [OWED: C5.2 on the second freeze (D62(b), D71(c))] |
| SP05-D2, SP20-D1 | `fixed` | V5 close-out; B-B re-budget | Windows C5.1 [OWED: C5.1 on the second freeze] |

The candidate 6 and 7 confirmations are in `plans/V2-WAVE1-carried-defects.md`; a candidate 8 note
belongs there once the owed runs exist.

## 12. Privacy, retention and rollback drill

Privacy: planted secrets reached no durable surface in the candidate 3 lane (C4.6, 0 hits); the
rehydration block's pointers and summaries withhold host-denied and out-of-project paths (D50, D60-D64,
D66(e), D71, D72), with known issues 3, 15 and 16 as the disclosed residuals; retrieval never carries
fidelity or coverage (D46). Retention: rollover on by default with the D6/D16 rotation pause and carry
bound, documented. Rollback: C1.7's backup, verify, restore and pre/post-new-write drill on the
shipped CLI; no automatic downgrade is promised. Kill switches: C4.7 passed on candidate 3. Candidate 8:
[OWED: C4.6 and UAT-12 under a deny rule in the live re-check].

## 13. Request usage and estimated price

Authoring and review calls (coordinator and workflow subagents on the owner's subscription) were not
collected into a request ledger for this close-out: usage, retry and compaction attribution, rate-table
date and cash impact are `unknown`. The weekly agent limit was reached twice (wave 2, D68(a)). Product
trial usage is recorded per category from the host's own JSON by `eval.LiveRunner` (C5.4) [OWED: C5.5
usage and estimated price, with rate-table date and completeness].

## 14. Decisions an owner should know

- **Scope and delegation.** 0.3.0 (D1); outward steps need a yes, then D33 delegates every remaining
  decision under conditions: release only on a frozen candidate with complete green evidence, never
  weaken a check, never change the owner's security settings.
- **Live budget.** D3's 40-80 sessions was raised to about 120 by D34, D47 and D52; every session is
  agent-run, not human UAT.
- **Behaviour users see.** Rehydration under the 10,000-character cap with overflow pointers (D5);
  config fails closed only for `runtime.redact`/`runtime.mode` (D8); home-directory projects refused
  (D18); no manual checkpoint, six commands (D36); a degraded compaction that dropped material is never
  silent (D59(b), D60(ii)); slow disks degrade to spool submode with nothing lost (D44, D53(c)).
- **Budgets.** B-A's default is platform-derived (D41); D25, D26 and D55 approved derived numbers;
  SP06-D2 and SP08-D1 are `wontfix` for 0.3.0 pending a batched-write store format (D54).
- **Platform limits.** Windows directory sync relies on NTFS journaling (D24); Windows runs the daemon
  from a verified staged copy (D10); a Defender false positive is the owner's to report (D32); code
  signing is an open release item.
- **Evidence rules.** Battery runs are invalid as reference timing (D57(d)); hosted fsync figures never
  become constants (Q1); D66 makes audit 2 the last whole-tree audit, with blocker and major findings
  blocking and minors becoming known issues.
- **Freeze history.** The first freeze stood through its night (D70) and was superseded only by the
  diff verify's major (D71); the second freeze launched from `275165e9` (D72(b), D73(c)). The owner is
  asked to keep the host on AC with the lid open (D70(a), D72(c)).

## 15. Post-release work

1. **Fail-closed handling of the remaining path spellings** (D72(a), known issue 16): treat the four
   minors and the nit from wave 23's fourth review as one class and withhold it whole, rather than
   another round of instance fixes. The first post-release item.
2. **The two test fixes** (D73(a)): `TestFault_DaemonKilledMidIngest`'s recoverSession waits out a
   fresh spawn claim and sends the next hook; the drain rows
   (`TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain` and
   `TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget`) call the real dispatch through
   withoutLineDeadline after the stall. Also `promptWG.Wait()` in
   `TestPromptWarning_SlowDurableAcceptIsLateForTheClient` (D70(b)).
3. **The PreCompact settle lead** (D73(b)): establish whether a settle leaves WAL-only refused or
   ring-dropped leased jobs out of its drop report (`precompact_settle.go`), red-first if it does.
4. **The per-prompt history.json fsync and capWentOn** (D71(d)): take the WentOn rewrite out of the
   session's ordering gate or batch it, and make capWentOn's trim linear.
5. **The Windows CI leg's budget** (D70(b)): a larger budget or one pass on windows-latest's -count=2
   leg, so a slow runner cannot kill internal/daemon while it is progressing.

Also carried: a product test pinning fsck's sidecar-before-root read order (D57(b)), incremental
archiving for rollover (D6), the batched-write store format (D54), and the C7.6 housekeeping.

## 16. Remaining blockers and next authorized action

Blocking release: every `[OWED: ...]` cell in sections 0, 2, 4, 5, 6, 8, 9, 10, 11, 12 and 13. Next, in
order (D53(h), D68(c), D71(c)): classify the second freeze's overnight run and hosted runs and write
`phase3/c8-CANDIDATE.md`; the C5.2 nights; the candidate 8 live re-check; C5.5; C6.2; fill this report
and give it its independent review (C6.4); C6.5; then the release steps, each under its authorization.

**Release gate:** not met. No V6 completion or production-readiness claim is made until the owed cells
are filled.

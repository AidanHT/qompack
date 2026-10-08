# V6 close-out report — Qompack 0.3.0, from the blocked V6 checkpoint to candidate 8

**Decision: RELEASED.** Qompack 0.3.0 was released on 2026-10-08 from candidate 8 (`3ec62ad2`), under
D79 (the release is cut) and D80 (it is published, rehearsed and promoted). Tag `v0.3.0` is on
`1a368a4b`, whose bundle paths are byte-identical to the candidate's, and every published `bin/` is
byte-identical to the frozen `qompack-bundles/c8` (D80, `phase3/c8/release-bin-compare.txt`). This
report is the record of that decision: every cell below names the artifact, run, commit or ruling that
answers it. A cell whose claim was not met says `NOT MET:` with its reason and the ruling that
dispositioned it. D33's release condition (a frozen candidate, complete evidence, every red fixed or
dispositioned) and D66's exit rule were judged met by D79 on D75-D78. The two reds found after the
release, on `develop` code identical to the tag's, are dispositioned as test defects by D81(a):
TestGC_DeadlineTruncatesAndResumes and TestPromptWarning_SlowDurableAcceptIsLateForTheClient (section
10). Neither blocks 0.3.0, and both are fixed for 0.3.1 on branch fix/v031-flakes. The independent
final review of this report (C6.4, section 9) ran three rounds, all needs-fixes, and between the
second and third the coordinator's D81 was applied. The findings and their resolutions are in
`plans/sdd/V6-closeout/c6-final-review.md`. One is open: D81(c)(4) found D73(b)'s PreCompact settle
lead real in 0.3.0, a fidelity-reporting defect in released code that no ledger row has yet
dispositioned (section 13). C6.4 stays unticked until the coordinator rules on it.

`plans/V6-report.md` (the 2026-09-20 BLOCKED checkpoint) stays immutable. This report records the
close-out that followed it. The working ledger is `plans/V6-CLOSEOUT-CHECKLIST.md` (decisions D1-D81,
the dispatch log, phases C0-C7), and every ruling cited here as Dnn is a row of that table.
Evidence paths written without a prefix are relative to `plans/sdd/V6-closeout/`.

## 0. Branch, HEAD and dirty baseline

| Item | Identity |
|---|---|
| Ledger branch | `verify/v6`, worktree `../qompack-v6`, head `1a368a4b` (the release commit, D79(a)); `develop` fast-forwarded to it and has since moved to `7fbb8a40` (marketplace PR #1 merge `fff45a45`, then the install correction). This revision of the report is written on `closeout/c6-final`, cut from `develop` `7fbb8a40` |
| Integration branch | `closeout/integration`, worktree `../qompack-cx-int`; candidate 8 was frozen from `e8c62191` (waves 1-23 and D74's `closeout/w23-lint`); its head is now `c61a211b`, after the live re-check (`49f52300`) and release step 2 merged into it |
| Close-out base | `cf31e01` (C0.1-C0.3: the interrupted `65bc8d7` run finalized, V6-remediation evidence committed) |
| V6 checkpoint candidate at takeover | `65bc8d7` plus fixture commit `3dab390` |
| Release candidate | Candidate 8, second freeze of integration `e8c62191` (D74(b)): commit `3ec62ad2e01b985640c0f1fb832df3917f766a5f` on `verify/v6`, tree `4f3deaf48a05b7145fdd16d867210ba9e5ebd7b4`, `git describe` `v0.2.0-2439-g3ec62ad2`, `sha256(git archive --format=tar 3ec62ad2)` `5b9d925b9d9b420cd711dc4484becb3b484ee28521d99826f668aadc7b0f59e9` (`phase3/c8-CANDIDATE.md`) |
| Frozen bundles | `qompack-bundles/c8` (second freeze; the sibling directory `../qompack-bundles`, outside the repository): windows-amd64 BUNDLE.json sha256 `61ba9c37dda03c14c44acb6824646a8d7410751bba6f1ce3c5c864d6382dcd8b`, checksums.txt sha256 `bd2184b6...2953b`; the six `bin/` sha256 are tabled in `phase3/c8-CANDIDATE.md` (windows-amd64 `qompack.exe` `93eb09f3...81a9`); host validation `claude plugin validate --strict --json` with Claude Code 2.1.280: accepted (`phase3/c8/host-validate.txt`), and the bundles step checked 6 of 6 targets (`phase3/c8/chain.log`) |
| Release | Tag `v0.3.0` on `1a368a4b` (D79(a)); release.yml `37738581717` success; published `bin/` byte-identical to the frozen ones, the other 78 extracted files identical, only each BUNDLE.json's `source.commit` differing (`phase3/c8/release-bin-compare.txt`); marketplace.yml `37746138600` opened PR #1, merged into `develop` as `fff45a45` (D80). Between `3ec62ad2` and `1a368a4b` the bundle and test paths differ only in two deleted lines of `test/guards/releasenotes_test.go` (`c6-final/runs/c8-identity-proofs.txt`) |
| Superseded first freeze | `424f0d08` (integration `77374c3c`, tree `6d1def7c`); its records moved to `phase3/c8-freeze1` and `qompack-bundles/c8-freeze1` (D71(b)). Neither is in the tree: the phase3 directory was never committed, and `qompack-bundles` is the sibling directory `../qompack-bundles`, outside the repository |
| Root checkout | The main repository stays on `verify/v3` `7f92af5` with its historical dirt untouched (C7.6 housekeeping is owed and needs owner consent) |

Every commit quoted in this report was checked with `git merge-base --is-ancestor`: all are ancestors
of `develop` (`7fbb8a40`) except C6.2's and C6.3's own commits `36dc82e4`, `920f9269` and `e404673c`,
which are on `closeout/c6-final`, and the 0.3.1 test fixes `c7f1dd38` and `352aec9b`, which are on
`fix/v031-flakes` (D81(a)); `6d1def7c` and `4f3deaf4` are tree ids, not commits.

## 1. Date and environment

Close-out executed 2026-09-22 to 2026-10-08 (America/Toronto); this revision 2026-10-08.

| Item | Value |
|---|---|
| Reference host | Windows 11 Home 10.0.26200 (25H2 build 10.0.26200.9457 in the candidate 7 and 8 lanes), Intel Core Ultra 7 155H, 22 logical CPUs, 31.4 GB RAM; timing taken on AC only (D57(d)), the store under a D32-excluded path (D53(h)) |
| Linux | Docker Desktop / WSL2 container, Go 1.26.6, non-root, capped at 8 CPUs and 8 GiB; valid for CPU- and read-bound rows, not fsync-bound ones (D53(b)) |
| macOS, darwin/amd64, windows/arm64, linux/arm64 | macOS is tested only on hosted macos-latest (arm64; ci.yml, nightly.yml). darwin/amd64, windows/arm64 and linux/arm64 are cross-compiled only, with no runner. Installed-host macOS and arm64 sessions: `unknown` (C4.11). Linux was installed in the container without a model (D80(c)); Linux model sessions are `unknown` (D34(c)) |
| Toolchain | `go 1.26`, `toolchain go1.26.6` (go.mod at `3ec62ad2`) |
| Claude Code host | 2.1.280 for the live lanes, the candidate 8 re-check, C5.5 and host validation (C3.10); 2.1.293 for D80's install rehearsal on the published release |
| Model in live sessions | claude-haiku-4-5-20251001 in every candidate 7 and candidate 8 lane session (`live/rerun-c8/D53i/summary.md`); claude-sonnet-5 in C5.5, as pre-registered (D77, `eval/runs/c55-c8/summary.md`) |
| Plugin version | `0.3.0` in `internal/core.Version` and `plugin.json` at `3ec62ad2` and at the tag; release-check's version agreement step passed with `--tag v0.3.0` (`phase3/c8/release-check.json`; C7.1, D1) |
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
| 8, freeze 2 | `3ec62ad2` (2026-10-06) | `e8c62191` | Not superseded: released as 0.3.0 (D79, D80). Its first launch from `275165e9` refused at the pre-freeze golangci-lint gate before anything was frozen (D74) |

## 3. Waves, seats and rulings

Every seat ran implement, adversarial review, fix and (from wave 3) verify inside its workflow. Most
seats worked in their own seat branch (`closeout/w2-lint`, for example) and sibling worktree, and were
merged `--no-ff` into `closeout/integration`. The exceptions committed on, or were merged into,
`verify/v6`: the wave 7 livelane seat (`0a8a0c2`), the wave 8 quiet prep (`25b976e`) and the wave 20
and wave 21 night seats.

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
syncs), w4-e2eflakes. Merged `04ccb68`, `f816953`, `dbede8c`, `3aeecc0`; runpatterns waivers `1c9012e`;
golangci errcheck fix `90e1db3`.
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
fixed it red-first, plus a second major its first review found (valueNameForm), and the minors of review
rounds 2 and 3; w23-docs (4 commits, `7d351d37`) fixed the troubleshooting and plan minors. Merged
`cbb3a7f5`, `275165e9`. The privacy seat's fourth review left 4 minors and a nit that did not
converge; they are known issue 16 (D72(a)). The static root-cause run `wf_8efe4557-020` classed the
integration CI reds as test defects (D73).

**Wave 23 lint (D74).** The second freeze's first launch from `275165e9` passed its merged-tree lint
and its integration, testpkgs, internal and e2efunc checks (VALID, on AC), then refused at the
pre-freeze gate on one golangci-lint finding: staticcheck SA4023 at `internal/daemon/daemon.go:1390`, a
`r == nil` test after `checkpoint.OpenReader`, which never returns nil. The code dates from `ada54d1e`;
wave 23's daemon test files made the linter re-analyse the package. Nothing was frozen or built.
`closeout/w23-lint` removed the dead comparison (`5c35bedc`, behaviour unchanged), merged as integration
`e8c62191`, and c8-night.sh was relaunched on the same evidence directory under a fresh pre-freeze run
id.

**Candidate 8 after the freeze (D75-D80).** The second freeze `3ec62ad2` ran its night on AC (D75,
`phase3/c8-CANDIDATE.md`). The live re-check `wf_090ea750-b1e` ran 20 real sessions on the frozen
bundle and passed every scenario (D76; merged into integration `49f52300`, rulings kept in
`c44a98d6`); its doc corrections were merged as `f06ed994` (`636867f4`, `6a7ba448`, `07e8f04b`). C5.5
ran 40 trials and read inconclusive (D77). The C5.2 night measured the last three chunks (D78,
`phase3/c8-c52/`). Release step 2 rewrote the release pages from candidate 8's evidence (`147abb29`,
`f05eda20`, `65b506b3`), integrated as `c61a211b` and merged into `verify/v6` as `a11477d2`; D79 cut
the release at `1a368a4b`, and D80 published, rehearsed and promoted it, then merged the marketplace
(`fff45a45`) and corrected the install page (`7fbb8a40`).

## 4. Inventory and exit criteria

The 304 original row IDs (1.1.1-1.18.14) and the SP19/20/21 additions live in
`plans/sdd/V6-remediation/inventory-current.tsv`, with the old-to-new assertion map and evidence-step
map in `inventory-map.tsv`, `inventory-c6-map.md` and `inventory-c8-map.md`. No row was added, dropped
or renumbered.

| Candidate | Disposition (304 rows) |
|---|---|
| 7 (history, `c7_result`) | 251 `verified_in_target`, 39 `partial_verified`, 7 `unknown`, 3 `unsupported`, 3 `documented`, 1 `implemented_unverified` |
| 8, second freeze (`c8_result`, C6.2, `36dc82e4`; 1.17.18 corrected in the C6.4 review round) | 275 `verified_in_target`, 16 `partial_verified`, 7 `unknown`, 3 `unsupported`, 3 `documented`; 0 `failed`, 0 `implemented_unverified`. Every cell rests on candidate 8's own artifacts, none on a candidate 5, 6 or 7 measurement (`inventory-c8-map.md`) |

The candidate 7 column is not candidate 8's result. Rows that no step can execute are never marked
`verified_in_target` (D37(c)): 1.10.18, 1.15.14, 1.16.5, 1.16.10, 1.17.3, 1.17.5 and the human half of
1.18.12 (D3) stay `unknown`. Of the 16 `partial_verified` rows, 1.5.12, 1.12.14 and 1.17.6 have a Linux
fsync-bound half (D53(b)); 1.17.8 and 1.17.9 rest on the installed upgrade carried from candidate 7
(D53(f)); 1.11.16, 1.14.10, 1.17.10, 1.17.14, 1.18.1, 1.18.3, 1.18.4, 1.18.8 and 1.18.11 have a half
with no possible artifact; 1.17.18's actionlint half has no artifact, because no actionlint step exists
in release.yml, ci.yml or release-check and no ruling retires it; and 1.17.19 needs a repository
setting (section 18). C5.2 was re-measured
in full on candidate 8, all eight chunks (D62(b), D75(b), D78(a)).

| Exit obligation (V6 plan section 2) | Status on candidate 8 |
|---|---|
| R2 run map, candidate identity, every incomplete result | Met. The run map is `phase3/c8-CANDIDATE.md` with `phase3/c8/chain.log`, `power.tsv`, `overnight-outcome.txt` (`steps=19 passed=16 failed=2 ... invalid_power=0 ... reported=1`), `phase3/c8/prefreeze/summary.log` and `phase3/c8-c52/` (`steps=3 passed=2 failed=1 [c116-rig]`); the incomplete results are named in sections 6 and 10 and in `inventory-c8-map.md` |
| SP01-18 families and SP19/20/21 additions reconciled | Met: C6.2 (`36dc82e4`, `inventory-c8-map.md`), the 14 section 3 identifiers and the SP19/20/21 switches included |
| SP17-M7, SP18-M7, UAT-01-12 on the shipped package | NOT MET in full: UAT-02, -04, -05 run 2, -06, -07, -08, -11 and UAT-12 steps 1-5 and 8 ran on the frozen bundle, whose `bin/` the release ships (D76, D80). UAT-01, -03, -09, -10, UAT-05 run 1 and UAT-12's upgrade leg were not re-run on candidate 8; they carry candidate 7's pass by diff, with notes naming the changed files (D53(f), D59, D76(f); `docs/uat.md`, `live/rerun-c8/CARRIED.md`). 1.17.10, 1.17.14 and the F-5 rows 1.18.x are `partial_verified` (section 4). D79 accepted the release on that evidence |
| M0-M6 enabled capabilities with target evidence | Met: the enabled adapter `runtime.migration.reinjection.sessionStartCompact` is `verified_in_target` (live round trip C4.3 and kill switches C4.7 on candidate 8); delivery-journal rollover ships enabled with SP20-D4's evidence test green; the gated switches are recorded `experimental` or `unsupported`, never passed (`inventory-c8-map.md`, "SP-19, SP-20 and SP-21 switches") |
| Old/new readers, backup, cutover, rollback rehearsed | Met by rehearsal, with the upgrade carried: release-check's rollback rehearsal step passed on candidate 8 (`phase3/c8/release-check.json`); a C1.7 restore smoke (backup create, verify, restore, fsck of source and destination, all exit 0) ran on candidate 8, and the post-new-write restore carries from candidate 7 with its note (`live/rerun-c8/CARRIED.md`, "C1.7"; D53(f)); C1.7's full run passed on candidate 4 |
| No hidden mandatory privacy, fidelity, recovery or regression blocker | Met except one item, NOT MET pending a ruling (last clause): the five V6-report blockers are closed (section 7); the live re-check's audit found no blocker or major (D76); known issues 3, 15, 16 and 19 are disclosed residuals; C5.5 quoted verbatim: "inconclusive — interval [-0.214, 0.214] straddles -0.200", with no H2 regression (constraint-clean +0.050) (D77, section 8); the two reds of ci.yml `37746311073` after the release are test defects, not product defects (D81(a), section 10). NOT MET, pending a ruling: D81(c)(4) found D73(b) real, a 0.3.0 PreCompact settle that could leave ring-held, WAL-only and predecessor-leased captures out of its drop report. It is a fidelity-reporting defect in released code, fixed for 0.3.1, and no ledger row yet says whether 0.3.0 discloses it as known issue 20 or keeps it ledger-only (section 13) |

## 5. Cross-component integration identifiers (V6 plan section 3)

Results are the `c8_result` of `inventory-c8-map.md`, "The fourteen section 3 integration identifiers".

| ID | Status on candidate 8 |
|---|---|
| 3.1 packaged real session | `verified_in_target`: the frozen bundle observed 20 real sessions (D76) and the released bytes one more through the `qompack-windows-amd64` marketplace entry (D80) |
| 3.2 launcher semantics | `verified_in_target` on Windows, ubuntu and macos test lanes; exec-form hooks (C1.11). macOS and arm64 installed hosts `unknown` (C4.11) |
| 3.3 documented commands | `verified_in_target`: C4.5 ran the six commands against the frozen bundle in a real session (`live/rerun-c8/C4.5/`), met with known issue 17 (D76(b)) |
| 3.4 elimination/staleness through MCP after upgrade | NOT MET in full, `partial_verified`: UAT-06 exercised record_eliminated and already_tried across a resume, a fork and a parent restart on candidate 8; survival across an upgrade carries from candidate 7 (`live/rerun-c8/CARRIED.md`, "C4.8"; D53(f), D76(f)) |
| 3.5 ephemeral eviction | `unsupported`: native eviction retired (E-1); its replacement and the legacy row are green on candidate 8 |
| 3.6 fsck repair and preservation | `verified_in_target`: packaged fsck on real stores on candidate 8 (C1.6 `live/rerun-c8/C1.6/`, the C1.7 restore smoke, UAT-04); detection, not recovery; known issue 18 (D76(c)) |
| 3.7 doctor/status agreement | `verified_in_target`: C4.5's paired status, doctor and fsck reads agree on candidate 8, with two sessions in the store (D60(f), `live/rerun-c8/C4.5/uat06/diff-reads.txt`) |
| 3.8 config reference matches binary | `verified_in_target`: the pre-freeze gate's generated-docs checks (`phase3/c8/prefreeze/gate.log`), release-check's generated docs step, hosted docs job |
| 3.9 install/upgrade/uninstall | NOT MET in full, `partial_verified`: rollback rehearsal on candidate 8 (release-check) and install/uninstall on the released bytes (D80); the installed upgrade (C4.8, UAT-12's upgrade leg) carries from candidate 7 (`live/rerun-c8/CARRIED.md`; D53(f), D76(f)) |
| 3.10 degraded passive | NOT MET in full, `partial_verified`: C4.9 (b), the settingsVersion leg, and C1.6 ran on the packaged bundle (`live/rerun-c8/C4.9/`, `C1.6/`); C4.9 (c), the packaged unavailable object, was not re-run on candidate 8 and carries from candidate 7 (`live/rerun-c8/CARRIED.md`, "C4.9 legs (a) and (c)"; D53(f)). UAT-07's never-stored hash on candidate 8 is a different case and does not stand in for it |
| 3.11 no secret, no network | `verified_in_target`: C4.6 and UAT-12 sessions A and B with planted credentials and deny-ruled files passed on candidate 8 (`live/rerun-c8/C4.6/`, `UAT-12/`) |
| 3.12 checkpoint to rehydration | `verified_in_target`: UAT-05 run 2 injected the loss notice on candidate 8 (`live/rerun-c8/UAT-05/`), with C4.3, UAT-04's and UAT-06's compactions |
| 3.13 reproducible artifacts | `verified_in_target`: two builds byte-identical (`phase3/c8/bundle-diff.txt` empty), hosted release-dry-run's bundles equal the frozen ones, 91 files (`phase3/c8/hosted-release-bundles.txt`), published `bin/` equal them (`phase3/c8/release-bin-compare.txt`). The V6 plan's wider wording, "licenses/name check", has an open half: no artifact records a registry name-availability check (the plan's section 4) |
| 3.14 hot path with every subsystem resident | NOT MET in full, `partial_verified`: Windows verified on AC (win-timing and win-x11-alone VALID, exit 0, `phase3/c8/power.tsv`; quiet C5.1 B-A p99 16.4 ms, B-B 11.3 ms against 50); Linux B-A/B-B fail in the container, fsync-bound, not verified in target by rule (D53(b), D75(a)) |

## 6. Commands actually run, and evidence paths

Every candidate's freeze record is `plans/sdd/V6-closeout/phase3/CANDIDATE.md` and
`phase3/c4-CANDIDATE.md` through `phase3/c8-CANDIDATE.md`; the chains, prefreeze and quiet runs are
under `phase3/c3/` to `phase3/c8/` and `phase3/c8-c52/`; the drivers are `coordinator/phase3.sh`,
`quiet.sh`, `prefreeze.sh`, `overnight-c6.sh`, `c7-night.sh`, `c8-night.sh` and `overnight-c8.sh`. Each
seat's report and run logs are in a directory named for the seat under `plans/sdd/V6-closeout/`. Live
lanes: `live/report.md`, `live/report-c4.md`, `live/report-c7.md`, `live/sessions.tsv`,
`live/rerun-c8/`.

Hosted CI, every run the ledger records:

| Run | Head | Outcome |
|---|---|---|
| ci.yml `36816905394`, nightly `36820740318` | candidate 5 | Reds routed to wave 16 (8.3 bypass, macOS, elevated runners) |
| ci.yml `36905843834` | `07a748cb` (integration `ae601390`) | Reds classified to 16d/16e |
| nightly `36955043924`, ci.yml `36955046276` | candidate 6 | Nightly green; two reds disposed (D57(a), D57(b)) |
| nightly `36981711009`, ci.yml `36981590450` | candidate 7 | Nightly green; one Windows test-timing red (D58(b)) |
| ci.yml `37361841760` | integration `2a2e8f6c` | Five red jobs (test on all three OSes, release-dry-run, cover), one fixture cause (D68) |
| ci.yml `37365986864` attempts 1 and 2 | integration `77374c3c` | Runner loss, unacquired jobs, then three test-only reds (D69, D70(b)) |
| ci.yml `37409857403`, nightly `37409848444` | freeze 1 `424f0d08` | Green on all three OSes (D70); superseded with freeze 1 |
| ci.yml `37533889761` | integration `275165e9` | Two test-only reds (D73) |
| ci.yml `37559750996` | integration `e8c62191` | Its Windows job failed TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes's fixture-sanity count, the test defect D75(c) records |
| ci.yml `37562946379`, nightly `37562945914` | candidate 8, `3ec62ad2` | ci.yml concluded success on attempt 2, all 21 jobs green; attempt 1's two reds dispositioned (D75(c): the macOS settle fixture is a test defect, `phase3/c8/hosted/macos-settle-rootcause.md`; the Windows crash at 1185 s is undetermined, did not recur and is not shown to be a blocker or major, `phase3/c8/hosted/windows-crash.md`). Nightly 31 of 31 jobs green (`c6-final/runs/c8-hosted-runs.txt`) |
| release.yml `37738581717` | tag `v0.3.0`, `1a368a4b` | Success: drafted the pre-release with six zips, checksums.txt, marketplace.json, LICENSE and THIRD_PARTY_NOTICES.md (D80) |
| marketplace.yml `37746138600` | after the promotion | Opened PR #1, whose marketplace.json equals the release asset; merged into `develop` as `fff45a45` (D80) |
| ci.yml `37746311073` | `develop` `fff45a45` | Two reds, both test defects (D81(a)). TestPromptWarning_SlowDurableAcceptIsLateForTheClient (job cover, internal/daemon) is D70(b)'s test-only missing join (section 13). TestGC_DeadlineTruncatesAndResumes (job timing (windows-latest), internal/store, `gc_test.go:475`, "a deadline that has already expired, over 700 objects, must truncate") priced its budget from one cold control mark (section 10). `fff45a45` differs from the tag only in `.claude-plugin/marketplace.json`, so both reds are on released test code; neither blocks 0.3.0, and both are fixed for 0.3.1 (fix/v031-flakes `c7f1dd38`, `352aec9b`) |
| ci.yml `37746414885` | `develop` `7fbb8a40` | Success. `7fbb8a40` differs from `fff45a45` only in docs/ and plans/, so the same product and test code is green here |

Phase 3 on the second freeze (C3.2-C3.12). The overnight steps are in `phase3/c8/chain.log` and
`power.tsv`: every step ran on AC with verdict VALID.

| Gate | Evidence |
|---|---|
| Merged-tree plan lint, test/guards, test/docs (c8-night step 3) | Pass on tree `4f3deaf4`: runpatterns, docmarkers, coveragefloors, test/guards and test/docs (`phase3/c8/merged-tree.txt`) |
| Pre-freeze check of integration (gate, integration, testpkgs, internal, e2efunc) | Pass: run `c8-20261007T020207Z-3312` on `e8c62191`, every step exit 0 and VALID on AC (`phase3/c8/prefreeze/summary.log`); `e8c62191` and `3ec62ad2` differ only under plans/ (`c6-final/runs/c8-identity-proofs.txt`) |
| Windows whole tree and -race (C3.2, C3.3) | Pass: the overnight win-race step (`devtool test-race`, exit 0, VALID); the whole tree through the pre-freeze internal, testpkgs and e2efunc steps and release-check's `ci-local test` step (PASS, `phase3/c8/release-check.json`); hosted test (windows-latest) green on attempt 2. The product-child race lane is the Linux container's linux-child step and nightly's race-product-child (D67(h)) |
| Linux non-root -race, e2e, child race (C3.4) | Pass: linux-tree, linux-e2e and linux-child, exit 0, VALID (`phase3/c8/power.tsv`); hosted test (ubuntu-latest) and test-e2e (ubuntu-latest) green |
| fmt, lint, vet, golangci (C3.5) | Pass: golangci-lint in the pre-freeze gate on Windows (`phase3/c8/prefreeze/gate.log`); release-check's fmt-check, lint and vet; hosted verify (ubuntu: fmt-check, `devtool lint`, go vet) and lint-windows green |
| Coverage floors (C3.6) | Pass: release-check's `ci-local cover`; hosted cover (ubuntu-latest, the store and paths floors on Linux) green |
| Generated docs, licenses, govulncheck, import allow-list (C3.7) | Pass: release-check's generated docs, licenses, govulncheck and guards steps; the gate's gen-*-docs and licenses checks (`phase3/c8/prefreeze/gate.log`); hosted security job (the import allow-list) and docs job green |
| Replay gate (C3.8, C5.3) | Pass: hosted replay-gate job of ci.yml `37562946379` on `3ec62ad2` (D67(i)); its report is the job's artifact and is not committed in the tree. NOT MET: the recorded-corpus tier. Nightly's replay-recorded job exited 0 because no recorded corpus is configured, so it skipped (`.github/workflows/nightly.yml`); not exercised for 0.3.0 (D67(g), known issue 13) |
| Fuzz pass (C3.9) | Pass: nightly `37562945914`, 25 fuzz jobs, every nightly target, all green (`c6-final/runs/c8-hosted-runs.txt`) |
| Plugin validation (C3.10) | Pass: `claude plugin validate --strict --json` (Claude Code 2.1.280) accepted the frozen bundle (`phase3/c8/host-validate.txt`); the bundles step validated 6 of 6 targets; release-check's plugin-validate steps; hosted plugin-validate green |
| Reproducible bundles (C3.11) | Pass: two builds byte-identical, 91 files (`phase3/c8/bundle-diff.txt` empty); hosted release-dry-run's release-version bundles equal the frozen ones in both directions (`phase3/c8/hosted-release-bundles.txt`, D53(h)(4)) |
| release-check (C3.12), and `release-check --tag` on the reference host on AC (D57(a)) | Pass: `release-check --tag v0.3.0` in a scratch clone of `3ec62ad2`, 18 of 18 steps PASS, none skipped, VALID on AC (`phase3/c8/release-check.json`, `chain.log`) |
| Isolated timing: win-timing, win-e2e-timing, win-x11-alone (D28, D57(d)) | Pass: all three exit 0, VALID on AC (`phase3/c8/power.tsv`). linux-timing and linux-e2e-timing failed B-A and B-B alone (p99 180 ms and 106 ms against 15 ms, 592 and 590 deliveries deferred, 0 lost), the container class D53(b) does not verify; NOT MET for Linux, dispositioned by D75(a). The Linux figures are quoted from `phase3/c8-CANDIDATE.md`; those two steps' logs were not committed, and their failure and exit codes are in `phase3/c8/power.tsv` and `overnight-outcome.txt` |

## 7. Blockers from the V6 checkpoint, and how each closed

| V6-report finding | Close-out |
|---|---|
| V6-AUTH-1, V6-AUTH-2 (pathless authority, hash addresses bypass) | C1.5: the real-capture regressions pass on Windows and Linux without weakening (wave 7 sp08d3); TestV6_HashAddressesDoNotBypassPathAuthorization green on candidate 8 (`inventory-c8-map.md`, 3.11) |
| V6-RECOVERY-1 (publication gaps, no automatic accounting) | Startup publication accounting and fsck detection; live pass on candidate 3, and on candidate 8 by the live re-check's resilience part (C1.6, D76, `live/rerun-c8/C1.6/`). Automatic *recovery* is not claimed beyond preserve-and-report; known issue 18 (D76(c)) |
| V6-RECOVERY-2 (no operator backup/restore) | C1.7: backup/verify/restore through the shipped CLI, pre- and post-new-write, passed on candidate 4 and in candidate 7's install part; on candidate 8 a restore smoke passed and the post-new-write restore carries from candidate 7 (`live/rerun-c8/CARRIED.md`, "C1.7"; D53(f)); release-check's rollback rehearsal passed |
| V6-HOST-1 (host deny rules invisible) | C1.9 and D7: saved-settings Read deny/ask rules honoured fail-closed; session-only rules, CLI flags and hook policies stay invisible (documented); the 8.3 bypass (wave 16) and the hostperm depth fail-open (D56(d)) fixed |

## 8. Evaluation layers

| Layer | Evidence |
|---|---|
| Deterministic and mechanism | Phase 3 on every candidate, pre-freeze checks, hosted CI, the replay gate. Candidate 8: the overnight run (section 6), ci.yml `37562946379` and nightly `37562945914` on `3ec62ad2`, release.yml `37738581717` at the tag |
| Closed-loop on the real host | Agent-executed sessions on the owner's host (D3, not human UAT): candidate 3 29 sessions, candidate 4 22, candidate 7 20, candidate 8 20 (D76), about 91 in all, plus D80's one release session. Stock versus Qompack trials: C5.5, 40 trials on candidate 8 (D77, `eval/runs/c55-c8/`) |
| Recovery and failure | test/fault, C1.7 live restores, C4.9 degraded paths, kill switches C4.7. Candidate 8: C1.6 and C4.9 (b) passed (`live/rerun-c8/C1.6/`, `C4.9/`), C4.7 passed (`C4.7/`); C4.9 (a) and (c) carry from candidate 7 |

**Primary outcomes.** Task completion, constraint and regression, and recoverability are C5.5's H1-H3.
The verdict of run `20261007T184908Z-abe10e`, quoted verbatim from its run record
(`eval/runs/c55-c8/summary.md`, "Decision (pre-registered rule, primary outcome)"):

> inconclusive — interval [-0.214, 0.214] straddles -0.200

Task success was 18/20 on each arm (difference 0.000). Constraint-clean was 20/20 against 19/20
(+0.050, [-0.116, 0.236]), so no H2 regression. Recovery was 6/8 on each arm (0.000, [-0.385, 0.385]),
so H3 reads "recovery advantage not shown". One stock trial (tool-output-recall/stock/2) hit the
10-minute step bound and is scored a failure on every outcome. A8's rule (D53(g)): inferior, or an H2
regression, blocks the release; non-inferior, superior or inconclusive releases with the verdict
quoted. Under A8 item 1 the inconclusive verdict allowed the release, and no document claims that
Qompack improves task success, constraint retention or recovery (D77(a), D77(b)).

**Sample and interval rationale.** Pre-registered in `plans/sdd/V6-closeout/eval/preregistration.md`:
task set `qompack-live-v2` (D12), 10 tasks x 2 trials x 2 arms = 40 sessions, 20 per arm, Newcombe 95 %
hybrid-score interval, non-inferiority margin 0.20. At success near 0.7 the half-width is about 0.27, so
the study detects breakage and large effects only; equal arms below 95 % read inconclusive by design.
No second batch (D53(g)). The run used the frozen candidate 8 bundle by its BUNDLE.json SHA
(`61ba9c37...dcd8b`), `--known-open-defects none` with the dispositioned residuals in its notes
(`eval/c55-c8-notes.md`, D75(e)), install `plugin-dir`, Claude Code 2.1.280, and `qompack eval` read it
as confirmatory (D77).

**Resource cost (C5.6).** Candidate 7's lane figures stay candidate 7's (D58(e)):
`live/rerun-c7/C5.6/summary.md` and `data.json` (daemon RSS and CPU). On candidate 8:
- D53(i) holds on the live re-check: 650 hook calls in 20 sessions, 0 host-reported failures or
  timeouts; host-seen median 89 ms, p95 195 ms over 609 measured pairs
  (`live/rerun-c8/D53i/summary.md`).
- D53(i) holds on C5.5: no trial on either arm had a hook problem (D77).
- Host-seen p50/p95 per hook on C5.5's qompack arm: SessionStart:startup 293/807 ms,
  SessionStart:compact 113/152 ms, PostToolUse 40-101/55-299 ms by tool, Stop 78/217 ms
  (`eval/runs/c55-c8/summary.md`); this table is in the release notes (D77(c)).
- Store growth: the qompack arm's mean store size is 213,433 bytes per trial
  (`eval/runs/c55-c8/summary.json`, `mean_store_bytes`).
- Daemon RSS and CPU were not re-measured on candidate 8; candidate 7's figures stand (D58(e)).

**Quiet benchmarks.** C5.1 on candidate 8, Windows on AC (`phase3/c8/quiet/c51-win.log`, and
`c51-win-bf.log` for B-F): B-A p99
16.4 ms and B-B 11.3 ms against 50 ms, B-E p99 166.8 ms against 2000 ms, B-F p99 73.7 ms against 250 ms,
all PASS. The Linux quiet run (c51-linux) is report-only, and its records were not committed (D53(b),
`phase3/c8-CANDIDATE.md`). C5.2 on candidate 8: all eight chunks, ten ABBA rounds against `cf31e01`
(`phase3/c8/quiet-c52-*/`, `phase3/c8-c52/quiet-c52-linux-*/`). The rows slower than the base with
significance are Windows negknow Open 1.16x and symbols Enclosing_100KB 1.011x (D75(b)), and Linux
Finalize 1.32x (46.6 ms against 50 ms), negknow Open 1.34x (75.1 ms against 300 ms),
ConfigLoad_ColdNoFiles 1.037x and CrossingEdges 1.018x. Each is inside its budget and off the hot
path, so each is recorded and not acted on (D78(a)). The C1.16 rig is in section 10. Hosted figures
never become constants (Q1).

## 9. Candidate 8 acceptance cells

| Cell | Evidence |
|---|---|
| Second freeze record (`phase3/c8-CANDIDATE.md`) | Written: commit, tree, describe, source hash, bundle and `bin/` hashes, the night's outcome and the hosted runs (D75) |
| Overnight run (overnight-c8.sh) | `phase3/c8/chain.log`, `power.tsv`, `overnight-outcome.txt`: 19 steps on AC, 16 passed, linux-timing and linux-e2e-timing failed on D53(b)'s class only, c51-linux reported, `invalid_power=0`; accepted by D75(a) |
| Hosted ci.yml on the frozen SHA | `37562946379` on `3ec62ad2`: success on attempt 2, 21 of 21 jobs; attempt 1's two reds dispositioned (D75(c)); job ids in `c6-final/runs/c8-hosted-runs.txt` |
| Hosted nightly on the frozen SHA | `37562945914` on `3ec62ad2`: 31 of 31 jobs green (25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded) |
| C5.1 | Windows on AC: pass (section 8, `phase3/c8/quiet/`); Linux report-only (D53(b)) |
| C5.2 | Complete, all eight chunks (D75(b), D78(a)). The C1.16 rig ran in both conditions (D65(a)): with no extra load n=30, p50 82.4 ms, p95 108.1 ms, p99 142.9 ms, every answer the rehydration and all 2144 Reads delivered; under the in-process co-load n=30, p50 221.1 ms, p95 385.7 ms, p99 417.1 ms, every answer the rehydration, 10 of 1665 Reads missing (`phase3/c8-c52/p3-c116-rig-noextra.json`, `p3-c116-rig-coload.json`). NOT MET as a rig pass: the step failed. D78(b) classes it a test defect (one shared pid and spool in an in-process rig, `phase3/c8-c52/c116-repro/`), and D78(c) records the field residual as known issue 19 |
| Live re-check C4.x and UAT (`mkrecheck8.py`, about 8 sessions plus D60(f)'s rows) | Passed (D76): `wf_090ea750-b1e`, 20 real sessions of a 25-session budget on the frozen bundle (`live/rerun-c8/`): UAT-05 run 2; UAT-12 sessions A and B with C4.6; UAT-06 with C4.5; status after a mid-session compaction (F-C48-1, `live/rerun-c8/F-C48-1/`); C4.9 settingsVersion leg; the rehydrate privacy and notice rows under a deny rule (UAT-12); two status reads with two sessions in the store (C4.5, `live/rerun-c8/C4.5/uat06/`); C1.6's resilience part. The audit returned 5 minors and 4 nits, no blocker or major; known issues 17 and 18 (D76(b), D76(c)) |
| C5.5 | Executed: "inconclusive — interval [-0.214, 0.214] straddles -0.200"; allows the release under A8 item 1 (D77) |
| C6.2 inventory dispositions | Done: `c8_result` and `c8_evidence` for all 304 rows (`36dc82e4`, `inventory-c8-map.md`) |
| C6.4 independent final review | Reviewed in three rounds; NOT MET for one finding (below). Record: `plans/sdd/V6-closeout/c6-final-review.md`, every finding with its resolution and commit. Non-authoring seats reviewed in three rounds. Round 1: needs-fixes (1 major, 5 minors, 1 nit). Round 2: needs-fixes (2 majors, 2 minors, 3 nits). The one finding the fix seats could not resolve, the TestGC_DeadlineTruncatesAndResumes red (1.1, 2.1), is dispositioned by D81(a), and the D81 pass applied it. Round 3 review: needs-fixes (2 majors, 1 minor); 3.1 and 3.3 are fixed. NOT MET: 3.2, the D73(b) settle defect found real by D81(c)(4), has no ledger disposition (section 13), so C6.4 is unticked until the coordinator's row exists |
| Pre-release, HTTPS install rehearsal, bin/ byte comparison (D53(h)) | Done (D80): published `bin/` byte-identical to `qompack-bundles/c8` (`phase3/c8/release-bin-compare.txt`); HTTPS install rehearsed with Claude Code 2.1.293 on Linux (container, no model, `bin/qompack` kept `-rwxr-xr-x`) and on Windows (isolated profile, then the real profile at local scope, where one session loaded 0.3.0, connected its MCP server, listed six commands and ran four hook events); then promoted. This rehearsal's record is ledger row D80 and `docs/install.md` section 9; no session files of it are committed |

## 10. Regression preservation, skips, failures and waivers

**Preserved records.** The completion reports are unchanged by the close-out: `git diff --quiet` is
empty for `plans/V1-report.md`, `V2-report.md` and `V3-report.md` from `cf31e01` to `7fbb8a40`, and
for `plans/V4-report.md`, `V5-report.md` and `V6-report.md` from `65bc8d7` to `7fbb8a40`. The V3
waiver of 2026-08-26 stays historical: J5 run 32932419445 and the three-platform p99 backfill remain
waived-open, and this close-out does not certify them. `plans/CARRIED-DEFECTS.tsv` changed in the
close-out in seven rows, each moved from `deferred:V6-VERIFY` to a final status by a ruling, and C6.3
added a comment block summarizing the 0.3.0 dispositions (`e404673c`, section 14). Every superseded
candidate's failures stay in its lane report and ledger row (section 2). The candidate 7 lane report
was committed verbatim (`8bd80c85`), and the first freeze's records were moved aside to
`phase3/c8-freeze1`, not deleted (D71(b)); that directory was never committed to the tree.

**Criterion changes, each ratified by a ruling.** This list names the rulings that changed a gate, a
budget or an acceptance criterion; the ledger (`plans/V6-CLOSEOUT-CHECKLIST.md`) is the complete record.
None was made to turn a red green without a recorded reason:
- D19: section 12.1 counts one chance per prompt delivery of the probe's own session sent after the
  probe was minted; x13 asserts the hook's wall-clock fallback spool per arm (`78b33a1`).
- D46: UAT-02 step 6 was revised. expand carries `_meta.qompack`, and no response carries fidelity or
  coverage.
- D36: exit criterion SP14-M3-01 was retired with the manual checkpoint.
- D50: UAT-05 is read literally, so the correction must render above what it supersedes. This change
  made the row stricter.
- D55: `TestStop_IsNotHeldBehindASessionEndsDrain` asserts the drain line's end cause instead of wall
  time.
- D58(b): `TestSpoolWatch_AnUnconsumableSpoolIsRetriedWithBackoffNotEveryTick` drives its look on the
  injected clock, with stricter assertions.
- D67(g): C3.8's recorded-corpus tier is not exercised for 0.3.0 (known issue 13).
- D67(h): C3.3's product-child race lane is satisfied by the Linux container and hosted nightly.
- D67(i): C5.3's artifact is the replay gate's report, and fraction-of-OPT is diagnostic only.
- D67(j): row 1.1.27 is judged by its absolute budget on candidate 8 only.
- D39: in co-load mode the hot-path row reports, rather than fails on, the daemon's breach transition to
  spool submode, because co-load already waives the wall budgets that cause it (ADR 0010). The
  transition must still be loud and named, the delivery ledger must add up, and no event may be lost.
  Isolated mode is unchanged.
- D41: B-A's default budget became max(15, L0IngestMs) per platform (Linux 15, Windows 50, macOS 40 ms).
  B-A's sample contains B-B's durable ingest by construction, so at 15 ms every Windows session tripped
  the breach detector. Candidate 2 was superseded over this (section 2). It composes two approved
  numbers and adds none.
- D42: X11's V2-relative ceiling was rebased, not relaxed. It now gates the like-for-like
  hook_controlled_observed p50 of a paired run with and without the ledger at 1.25 x the base run,
  instead of comparing B-A p99 with V2's figure, which excluded the handler and durable ingest. The
  amendment (2026-09-29, ratified) makes the ceiling max(1.25 x base p50, base p50 + one reported tick),
  because whole-millisecond samples cannot resolve 25 % below four ticks. The absolute B-A and B-B
  gates stay.
- D69(b): D68(e)'s "green on the frozen head before the freeze" became "before candidate 8 is
  accepted". The evidence required for acceptance did not change; a red that is wave 22's supersedes
  candidate 8 as it would have stopped the freeze.

**Waivers.** Three kinds are in force.
- **ADR 0010's co-load waiver (D39).** On a pass that declares co-load (`QOMPACK_UNDER_COLOAD=1`), the
  wall budgets of B-A, B-B and B-E are reported, not gated. The close-out night harness declares
  co-load on its shared passes (D62). The isolated timing passes and C5.1 do not declare it, so B-A and
  B-B are gated there. `ec1ce53d` corrected the co-load waiver notes in the JSON artifacts: the lanes
  that still enforce those rows judge on a reference disk. On the second freeze, co-load was declared
  by the pre-freeze integration, testpkgs and internal steps (`coordinator/prefreeze.sh`) and by the
  win-race, linux-tree, linux-e2e and linux-child steps (`coordinator/phase3.sh`). It was not declared
  by win-timing, win-e2e-timing, win-x11-alone, linux-timing, linux-e2e-timing, c51-win, c51-linux,
  the C5.2 chunks (`quiet-run.txt`: `QOMPACK_UNDER_COLOAD unset`) or release-check:
  `coordinator/overnight-c8.sh` unsets any inherited declaration before its steps. The C1.16 rig's
  co-load condition is the rig's own in-process load (16 fsync writers, 4 CPU spinners; D78).
- **The non-reference-disk declaration (Q1).** Hosted jobs set `QOMPACK_NONREFERENCE_DISK`, a
  separate declaration for a reason other than co-load: GitHub runners' fsync tail. Under it B-A,
  B-B and B-E's wall row are reported, not gated (D53(e), D55, ADR 0010 Addendum 2). D57(a) added
  it to release-dry-run and release.yml's tag-time job. The delivery ledger, B-E_cpu and every
  structural check stay gated, and the reference verdict on those rows is the local
  `release-check --tag` on the reference host, which passed on candidate 8.
- **`runpatterns` waivers on `-run` quotes in seat reports.** They change documentation only and never
  skip or relax a test. On closeout/integration since `cf31e01`: `893b11b9` (wave 1), `8d50d6bb` (wave
  2b), `1c9012e2` (waves 3 and 4), `f6095e27` (wave 5), `fc5289c3` (wave 10), `dfe99d90` (wave 14),
  `fe6b27fa` (wave 15), `d5c9c533` (wave 15c), `6f118a7b` (wave 16), `ae601390` (wave 16b), `877ed3f7`
  (wave 16c), `8489bc97` (waves 16d and 16e), `9a56b305` (wave 16f), `1d793976` (wave 17), `7aeb5c6e`
  (wave 17c), `738d67c7` (wave 19), `39dfcb75` (wave 19c), `2bf29705` (waves 19b and 19d to 19g),
  `456a0d24` (waves 19h and 19i) and `f905ec9c` (wave 20). These are the dedicated waiver commits, read
  from the commit subjects of `git log cf31e01..closeout/integration`. Seat reports in waves 2, 5 to 8,
  16 and 22 also carry waivers added in the commit that recorded the report, whose subject does not say
  so (for example `bf8a9c30`, `047e56e9`, `3e61cdd4`, `97eddc11` and `2a2e8f6c`). The complete record is
  the `<!-- runpatterns: ... -->` markers themselves: 297 in 64 files under plans/sdd/V6-closeout/ at
  `e8c62191`.

**Skips and unexecuted evidence, stated rather than passed.**
- Linux fsync-bound rows B-A and B-B are not verified in target on the container (D53(b)).
- Hosted fsync-bound rows are reported, not gated (Q1).
- macOS and arm64 installed-host sessions are `unknown`, and Linux was installed without a model
  session (C4.11, D34(c), D80(c)).
- Human UAT was not performed. Every live row is agent-executed (D3).
- The rows no step can execute stay unverified (D37(c), section 4).
- The recorded-corpus replay tier is not exercised: nightly's replay-recorded job skips without a
  corpus (D67(g), known issue 13).
- TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting skips without the claude CLI on the
  hosted and container lanes, and the Windows logs are non-verbose, so its execution on candidate 8 is
  not proven (1.17.9, `inventory-c8-map.md`).
- The `stubskips` lint sub-check was red through waves 3 to 5. Wave 16 ci split test/e2e into its own
  pass (`35f8757d`). On the first freeze's re-run, release-check's stubskips was killed at its
  45-minute guard while the host slept (D72). On candidate 8, release-check ran its 18 steps with none
  skipped (`phase3/c8/release-check.json`).

**Reds dispositioned as test defects.** D70(b) and D73 (section 13's test-only residuals). On the
second freeze and after it:
- linux-timing and linux-e2e-timing: D53(b)'s container class, not judged (D75(a)).
- ci.yml `37562946379` attempt 1, test (macos-latest): the settle row's fixture-sanity count, a test
  defect (D75(c), with ci.yml `37559750996`'s Windows job on `e8c62191`); a forced-schedule overlay
  reproduced it with every product assertion passing (`phase3/c8/settle-overlay/`).
- ci.yml `37562946379` attempt 1, test (windows-latest): internal/daemon ended at 1185 s with a
  runtime-class crash whose first lines the job did not keep. The cause is undetermined; it did not
  recur on attempt 2, the Windows and Linux `-race` trees passed, and it is not shown to be a blocker or
  a major (D75(c), D66(c)).
- c116-rig: a test defect of the in-process rig (D78(b)), with known issue 19 as its field residual
  (D78(c)).
- ci.yml `37746311073` on `develop` `fff45a45` (D81(a)), whose head differs from the tag `1a368a4b`
  only in `.claude-plugin/marketplace.json`, so both reds are on released test code. Neither blocks
  0.3.0, and both are fixed for 0.3.1 on branch fix/v031-flakes, with no product change:
  - Job cover: TestPromptWarning_SlowDurableAcceptIsLateForTheClient, D70(b)'s known missing join
    (fix/v031-flakes `c7f1dd38`).
  - Job timing (windows-latest): TestGC_DeadlineTruncatesAndResumes at
    `internal/store/gc_test.go:475`, "a deadline that has already expired, over 700 objects, must
    truncate". The collector is correct: the same job's
    TestGC_DeadlineOvershootIsBoundedByTheCheckInterval stops a pass with a spent deadline at object
    255. The row priced its budget at 4x one control mark, the binary's first and coldest, so the
    budget outlasted the judged pass's last check at object 512; a 150 ms stall injected into that
    one sample reproduced it 3 times in 3. The same job passed on ci.yml `37746414885` (`7fbb8a40`).
    The fix prices the budget from the fastest of four marks and leaves the assertion unchanged
    (fix/v031-flakes `352aec9b`). It is listed in `w22-known-issues.md`'s test-only residuals.

## 11. UAT-01 to UAT-12

Every row was executed by an agent on the owner's real installed host (D3). None is human UAT. The
"Last executed" column is history. The candidate 8 column is what the release rests on, and each row's
Result block in `docs/uat.md` reports candidate 8.

| Row | Last executed before candidate 8 (lane evidence) | Result there | Candidate 8 |
|---|---|---|---|
| UAT-01 install and disabled optimizations | candidate 7 (`live/rerun-c7/UAT-01/`) | pass | Carried from candidate 7 with a note naming the changed files (`docs/uat.md` UAT-01 Result, D53(f)); the install also ran on the released bytes (D80) |
| UAT-02 ordinary session, visible fidelity | candidate 4 (`live/rerun-c4/UAT-02/`) | pass on the row's fail criteria; non-exact fidelity not observable on this host | Pass on its fail criteria, re-run (D49, D76; `live/rerun-c8/UAT-02/`) |
| UAT-03 checkpoint survives the daemon | candidate 7 (`live/rerun-c7/UAT-03/`); failed on candidate 4 | pass | Carried from candidate 7 with a note naming the changed files (`docs/uat.md` UAT-03 Result, D76(f)) |
| UAT-04 compaction belongs to the host | candidate 7 (`live/rerun-c7/UAT-04/`) | pass, with a finding | Pass, re-run (D76; `live/rerun-c8/UAT-04/`) |
| UAT-05 block within budget, loss named | candidate 7 (`live/rerun-c7/UAT-05/`) | **fail**: silent drop at 150/150, fixed in wave 19 (D59(b), D60(ii)) | Pass: run 2 re-run and injected the loss notice (D76; `live/rerun-c8/UAT-05/`); run 1 carried from candidate 7 with its note (D76(f)) |
| UAT-06 compact, resume, fork, correct | candidate 7 (`live/rerun-c7/UAT-06/`) | pass, with a finding (C4.5 failed alongside it) | Pass, re-run with C4.5 (D76; `live/rerun-c8/UAT-06/`, `C4.5/`) |
| UAT-07 four ways back, `unavailable` never `absent` | candidate 4 (`live/rerun-c4/UAT-07/`) | pass | Pass, re-run (D76; `live/rerun-c8/UAT-07/`); O-1 ruled by D76(e) |
| UAT-08 elimination answers `active` with fields | candidate 4 (`live/rerun-c4/UAT-08/`) | pass | Pass, re-run (D76; `live/rerun-c8/UAT-08/`) |
| UAT-09 stale or unknown never prohibits | candidate 7 (`live/rerun-c7/UAT-09/`); failed on candidate 4 | pass | Carried from candidate 7 with a note naming the changed files (`docs/uat.md` UAT-09 Result) |
| UAT-10 self-observation keeps uncertainty | candidate 7 (`live/rerun-c7/UAT-10/`); not re-run on candidate 4 | pass | Carried from candidate 7 with a note naming the changed files (`docs/uat.md` UAT-10 Result) |
| UAT-11 admission off passes through | candidate 4 (`live/rerun-c4/UAT-11/`) | pass | Pass, re-run (D76; `live/rerun-c8/UAT-11/`) |
| UAT-12 privacy and lifecycle edges | candidate 7 (`live/rerun-c7/UAT-12/`) | **fail** as a row: upgrade leg pass, retrieval leg fail (F1, a denied path in a tool summary; fixed in waves 19-23) | Pass: sessions A and B with C4.6, steps 1-5 and 8 (D76; `live/rerun-c8/UAT-12/`); the upgrade leg (steps 6, 7, 9, 10) carried from candidate 7 with a note naming the changed files (`docs/uat.md` UAT-12 Result, D76(f)) |

UAT-02, UAT-07, UAT-08 and UAT-11 last ran on candidate 4 before this re-check, so their carry would
have had to span the diff from candidate 4. The candidate 8 re-check ran all four on the frozen bundle,
so no row now rests on candidate 4's evidence. UAT-01, UAT-03, UAT-09, UAT-10, UAT-05 run 1 and UAT-12's
upgrade leg rest on candidate 7's pass and a carry note, never on a candidate 8 run. The non-UAT
re-check rows are section 9's live re-check cell.

## 12. Actual rollout scope

What 0.3.0 ships, read from `docs/config-reference.md` and `docs/release-notes/v0.3.0.md` at the tag
`1a368a4b`:
- **Distribution.** A GitHub release built by `.goreleaser.yaml` (wave 17, coordinator `b31d0753`)
  for six targets (linux, darwin and windows, each on amd64 and arm64), published first as a
  pre-release and promoted after the install rehearsal (D80). The marketplace has one entry per
  target, and the user installs exactly one, from the repository form of the marketplace, with
  `--sparse .claude-plugin` on Windows (D80(a), D80(b); `docs/install.md` section 9). The binaries are
  not code-signed.
- **Where it was installed.** windows/amd64, the reference host, was installed into Claude Code
  (2.1.280 in the lanes, 2.1.293 for the release) and ran real sessions. linux/amd64 was installed in
  the container with no model session (D80(c)). macOS is tested on hosted macos-latest (arm64) only,
  and was never installed. darwin/amd64, linux/arm64 and windows/arm64 are cross-compiled only.
- **On by default.**
  - The resident daemon (`runtime.daemon.enabled`).
  - Redaction before storage (`runtime.redact.enabled`).
  - SessionStart compact reinjection (`runtime.migration.reinjection.sessionStartCompact`), the one
    tested injection adapter.
  - Delivery-journal rollover, with D6's residuals documented (D2).
  - Six slash commands, with no manual checkpoint (D36), and the MCP server.
- **Off and refused at load until their gates pass.**
  - Admission/replacement of new results (`runtime.migration.replacement.newResult`, SP-21 M4).
  - Raw evidence capture and the durable publication frontier (SP-20 M1).
  - The automatic compaction veto.
  - Experiments (SP-15/16).
  - Every `runtime.phase7.*` policy.
- **Off by default.** Submodular selection and loop warnings. Telemetry is hard-wired off, and the
  plugin performs no network I/O.
- **Release identity.** The tag is on `1a368a4b`, a docs-only descendant of candidate 8 whose bundle
  paths equal the candidate's, and every published `bin/` equals `qompack-bundles/c8` (D58(e), D79,
  D80). Each outward step had its own authorization (D4, D33, D79, D80).

## 13. Known issues and test-only residuals

The user-facing items are in CHANGELOG.md and `docs/release-notes/v0.3.0.md` (D79); the full text is
`plans/sdd/V6-closeout/w22-known-issues.md`.

1. Idle windows across two daemon idle exits can degrade a project to passive recording.
2. On a score tie a fork's checkpoint can keep a parent decision ahead of its own (named in `dropped()`).
3. With a Read deny or ask rule, path-keyed drops past 64 host judgements show as "(path withheld)",
   and a section 6 summary naming one may be withheld (D71(d)); over-withholding only.
4. A rehydration build costs about 1.7x candidate 7's (about 2 ms more), far inside the 5 s budget.
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
17. /qompack:status's description names a "last decision" the page does not show (D76(b)).
18. A session end's garbage collection sweeps an unindexed object the startup accounting reported;
    no capture content is lost, and fsck still names the gap (D76(c)).
19. On Windows, hook processes that share a reused pid share one fallback spool; once it reaches its
    64 MiB cap while unconsumed, a capture whose hook cannot reach the daemon is dropped, loudly
    (D78(c)).

Test-only residuals (ledger only, never the release notes): the hook-path connect budget rows (cliwork
FR1-2); drain rows applying `drainLineDeadline` to a real dispatch; wall-clock margins in the spawn rows;
the drain pass-cost row's 35-60 s; four rehydrate rows on a long Windows TEMP; two product-timer rows
(clock); the Windows CI leg's budget and the deterministic drain-row strand (D70(b), D73(2));
`TestPromptWarning_SlowDurableAcceptIsLateForTheClient`'s missing join (D70(b)); the per-prompt
history.json fsync and capWentOn's quadratic trim (D71(d)); `TestFault_DaemonKilledMidIngest`'s
dials-only wait (D73(1)). Second-freeze additions in `w22-known-issues.md`: the settle row's fixture
that does not wait for the live worker, and ci.yml's lost crash head (D75(c));
checkpoint.decision_read_error's by-design count on healthy stores, a diagnostic (D76(d)); the C1.16
rig's shared spool identity and the unprovable
Read target persisted nowhere (D78(b), D78(d)). After the release: the two reds of ci.yml
`37746311073`, TestGC_DeadlineTruncatesAndResumes's budget priced from one cold mark and
TestPromptWarning_SlowDurableAcceptIsLateForTheClient's missing join (D81(a), section 10).

Released-product residual awaiting the coordinator's ruling (not test-only, and not yet in the known
issues above): D73(b) listed a PreCompact settle lead as unverified, and D81(c)(4) has found it real.
In 0.3.0 a settle (`internal/daemon/precompact_settle.go`) could leave ring-held, WAL-only and
predecessor-leased captures out of its drop report, so the report could under-state what the
compaction left behind. 0.3.1 names every capture it leaves (section 18, item 3). No ledger row yet
rules whether 0.3.0 discloses it as known issue 20 in CHANGELOG.md and the release notes or keeps it
ledger-only with a stated reason. Until that row exists, section 4's no-hidden-blocker row is NOT MET
for this item and C6.4 is unticked (`plans/sdd/V6-closeout/c6-final-review.md`, finding 3.2;
`w22-known-issues.md`).

## 14. Carried defects (C6.3)

`plans/CARRIED-DEFECTS.tsv` is the source of record. Every one of its 28 rows is `fixed` (22) or
`wontfix` (6); no row is `open` or `deferred:`, so `TestCarriedDefects_WaveReportRequiresResolution`
has nothing to fail on, and test/guards is green. C6.3 changed no row's status: it gave each row its
0.3.0 disposition and candidate 8 evidence in the five detail documents (`920f9269`) and summarized
the groups in a comment block of the manifest (`e404673c`).

| Row | Status | Ledger | Confirming evidence on candidate 8 |
|---|---|---|---|
| SP08-D3 | `fixed` | C2.1, D35(b), D38, D78(c) | Evidence test green on candidate 8 (win-race, linux-tree, hosted test); the pid-reuse residual is known issue 19 (`plans/V2-SP-08-carried-defects.md`) |
| SP20-D4 | `fixed` | C1.10, C2.2, D2, D6, D16 | Evidence test green on candidate 8; rollover ships enabled with D6 and D16's residuals in the release notes' Known limits (`plans/V2-WAVE1-carried-defects.md`) |
| SP09-D1, SP10-D1, SP20-D2 | `fixed` | C2.4, C2.5, C2.7, D54 | C5.2 on candidate 8 (D75(b), D78(a)): every row inside its budget |
| SP06-D2, SP08-D1 | `wontfix` | C2.3, C2.6, D54 | C5.2 on candidate 8 (D75(b), D78(a)); the release notes' Known-limits line on PutBytes and the 256 KB tool result |
| SP05-D2, SP20-D1 | `fixed` | V5 close-out; B-B re-budget | Windows C5.1 on candidate 8: B-B p99 11.3 ms against 50 ms (`phase3/c8/quiet/c51-win.log`) |
| SP04-D2, SP04-D3, SP06-D1 | `wontfix` | V3-VERIFY, `docs/adr/0100-v3-verification.md` | Internal residuals with no user-visible effect and no release-notes entry (`plans/V2-SP-04-carried-defects.md`, `plans/V2-WAVE1-carried-defects.md`) |
| SP02-D6 | `wontfix` | ADR 0002 | Test-only residual, a replay evaluation harness design decision (`plans/V2-SP-02-carried-defects.md`) |

## 15. Privacy, retention and rollback drill

Privacy: planted secrets reached no durable surface in the candidate 3 lane (C4.6, 0 hits) or in
candidate 8's C4.6 session C and UAT-12 sessions A and B, run with planted credentials and deny-ruled
files (D76; `live/rerun-c8/C4.6/`, `UAT-12/`). The rehydration block's pointers and summaries withhold
host-denied and out-of-project paths (D50, D60-D64, D66(e), D71, D72); known issues 3, 15 and 16 are
the disclosed residuals, and section 5's goal line quoting the user's own words follows D62(f)
(D76(e)). Retrieval never carries fidelity or coverage (D46). Retention: rollover on by default with
the D6/D16 rotation pause and carry bound, documented; known issue 18 (D76(c)). Rollback: C1.7's
backup, verify, restore and pre/post-new-write drill on the shipped CLI, its restore smoke and
release-check's rollback rehearsal on candidate 8; no automatic downgrade is promised. Kill switches:
C4.7 passed on candidate 3 and again on candidate 8, recording off and reinjection off each set alone
(`live/rerun-c8/C4.7/`).

## 16. Request usage and estimated price

Authoring and review calls (coordinator and workflow subagents on the owner's subscription) were not
collected into a request ledger for this close-out: usage, retry and compaction attribution, rate-table
date and cash impact are `unknown`. The weekly usage limit was reached at least three times (wave 2,
candidate 7's live lane D58(g), D68(a)). Product trial usage is recorded per category from the host's
own JSON by `eval.LiveRunner` (C5.4). C5.5 on candidate 8 (`eval/runs/c55-c8/summary.md`,
`summary.json`): mean usage per qompack trial 2,735 input, 6,036 output, 366,842 cache-read and 21,512
cache-write tokens, against 2,727, 5,721, 359,663 and 18,918 on stock. The list-price-equivalent
estimate is 0.2250 USD per trial against 0.2101 on stock, from the 2026-09-22 rate table; the sessions
ran on a subscription, which has no per-token cash charge, so this is an estimate, not a cost
(D77). Completeness: 20 trials on each arm are marked `estimate_incomplete`, because 20 records per arm
do not split cache writes between the 5-minute and 1-hour TTLs.

## 17. Decisions an owner should know

- **Scope and delegation.** 0.3.0 (D1); outward steps need a yes, then D33 delegates every remaining
  decision under conditions: release only on a frozen candidate with complete green evidence, never
  weaken a check, never change the owner's security settings.
- **Live budget.** D3's 40-80 sessions was raised to about 120 by D34, D47 and D52; every session is
  agent-run, not human UAT. Candidate 8's re-check used 20 of its 25 sessions (D76), and C5.5 40
  trials (D77).
- **Behaviour users see.** Rehydration under the 10,000-character cap with overflow pointers (D5);
  config fails closed only for `runtime.redact`/`runtime.mode` (D8); home-directory projects refused
  (D18); no manual checkpoint, six commands (D36); a degraded compaction that dropped material is never
  silent (D59(b), D60(ii)); slow disks degrade to spool submode with nothing lost (D44, D53(c)).
- **Budgets.** B-A's default is platform-derived (D41); D25, D26 and D55 approved derived numbers;
  SP06-D2 and SP08-D1 are `wontfix` for 0.3.0 pending a batched-write store format (D54); the C5.2
  rows slower than the base are inside their budgets and recorded (D75(b), D78(a)).
- **Platform limits.** Windows directory sync relies on NTFS journaling (D24); Windows runs the daemon
  from a verified staged copy (D10); a Defender false positive is the owner's to report (D32, D80(f));
  code signing is an open release item.
- **Evidence rules.** Battery runs are invalid as reference timing (D57(d)); hosted fsync figures never
  become constants (Q1); D66 makes audit 2 the last whole-tree audit, with blocker and major findings
  blocking and minors becoming known issues.
- **Freeze history.** The first freeze stood through its night (D70) and was superseded only by the
  diff verify's major (D71); the second freeze launched from `275165e9` (D72(b), D73(c)), refused at
  its pre-freeze lint gate and was relaunched from `e8c62191` (D74), then stood (D75). The owner is
  asked to keep the host on AC with the lid open (D70(a), D72(c)).
- **The release.** D77(a) released on an inconclusive C5.5, and D77(b) binds every page not to claim
  an improvement. D79 cut the release; D79(c) and D80(d) turned on the repository setting that lets
  Actions open pull requests, for the marketplace step. D80(e): one isolated-profile marketplace add
  ran against the real profile once, when a wrapper dropped CLAUDE_CONFIG_DIR; its clone failed before
  it wrote anything, and the real profile's files matched their baseline.
- **After the release (D81).** develop's hosted CI is green on `7fbb8a40`, and the two reds before it
  are test defects (D81(a)). The v0.3.1 wave, on fix/v031-* branches not yet merged, fixes known issues 17 and 19, D73(b)'s settle report,
  D76(d)'s diagnostic and the test-only residuals, keeps known issue 18, and sets 0.3.1's release
  gate (D81(c)).

## 18. Post-release work

The v0.3.1 wave (`wf_50d07daa-d92`, five seats on fix/v031-* branches, D81(c)) has taken items 2, 3,
5 and part of 6 below. Those branches are not merged or released; 0.3.1's release gate is D81(c)(8).

1. **Fail-closed handling of the remaining path spellings** (D72(a), known issue 16): treat the four
   minors and the nit from wave 23's fourth review as one class and withhold it whole, rather than
   another round of instance fixes. The first post-release item.
2. **The test fixes** (D73(a), D70(b), D75(c), D78(b)): `TestFault_DaemonKilledMidIngest`'s
   recoverSession waits out a fresh spawn claim and sends the next hook; the drain rows
   (`TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain` and
   `TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget`) call the real dispatch through
   withoutLineDeadline after the stall; `promptWG.Wait()` in
   `TestPromptWarning_SlowDurableAcceptIsLateForTheClient`; the settle row waits for the live worker in
   settleGate; the C1.16 rig gives each hook its own spool identity and counts LOUD cap drops; and
   TestGC_DeadlineTruncatesAndResumes prices its budget from the fastest of four marks (D81(a)).
   For 0.3.1 the D70(b), D73(1), D73(2) and D75(c) rows are fixed in test code, with no product
   change and no loosened check (D81(c)(5)); the TestPromptWarning and TestGC fixes are
   fix/v031-flakes `c7f1dd38` and `352aec9b`; the rig's own spool identity comes with D81(c)(1)'s
   per-writer spool files.
3. **The PreCompact settle lead** (D73(b)): D81(c)(4) found it real. A settle could leave ring-held,
   WAL-only and predecessor-leased captures out of its drop report
   (`internal/daemon/precompact_settle.go`); for 0.3.1 it names every capture it leaves, and the
   summary names no cause it cannot know. Its 0.3.0 disposition awaits the coordinator's ruling
   (section 13).
4. **The per-prompt history.json fsync and capWentOn** (D71(d)): the ledger records contract r2's
   per-prompt history.json fsync, which sits off the hook reply path but inside the session's ordering
   gate, and capWentOn's quadratic trim, as known daemon minors. It records no remedy. Proposed, not
   ruled: move the fsync out of the ordering gate or batch it, and make the trim linear. The
   post-release seat decides.
5. **The Windows CI leg's budget and crash lines** (D70(b), D75(c)): for 0.3.1 the Windows test leg
   gets a 120-minute budget per binary, and ci.yml keeps a crashed binary's head and panic block and
   uploads test.json on failure (D81(c)(5)).
6. **Product follow-ups ruled for after the release:** for 0.3.1, the /qompack:status description
   drops 'last decision', with no last-decision feature (D81(c)(2), known issue 17);
   checkpoint.decision_read_error no longer counts an elimination node's empty root (D81(c)(3),
   D76(d)); every spool writer gets its own file, `client-<pid>-<16 hex>.ndjson`, which also removes
   D38's residual, while a 0.3.0 hook's `client-<pid>.ndjson` is still drained (D81(c)(1), known
   issue 19). Still open: known issue 18 stays a known issue in 0.3.1 (D81(c)(6)), and an unprovable
   Read target is recorded as unavailable (D78(d)).
7. **Branch protection's required checks** (1.17.19): `develop` and `main` are protected against force
   pushes and deletion but require no status check (`c6-final/runs/c8-hosted-runs.txt`). Making the
   ci.yml jobs required is a repository setting not set for 0.3.0.

Also carried: a product test pinning fsck's sidecar-before-root read order (D57(b)), incremental
archiving for rollover (D6), the batched-write store format (D54), the owner's Defender scan of the
release exe and any false-positive submission (D32, D80(f)), and the C7.6 housekeeping.

## 19. Remaining blockers and next authorized action

**Release gate: met; 0.3.0 released.** D79 judged the gate met on D75-D78, and D80 records the
publication: tag `v0.3.0` on `1a368a4b`, release.yml `37738581717`, published `bin/` byte-identical to
the frozen candidate, the HTTPS install rehearsed on Windows and Linux, then promoted; marketplace PR #1
merged as `fff45a45`. Every red known when D79 ruled was fixed or dispositioned, and so are the two
found after the release on code identical to the tag's: D81(a) classes TestGC_DeadlineTruncatesAndResumes
and TestPromptWarning_SlowDurableAcceptIsLateForTheClient, from ci.yml `37746311073`, as test defects
that do not block 0.3.0 (section 10). No release blocker is open.

**Close-out record: one item open.** C6.4's independent review is recorded in
`plans/sdd/V6-closeout/c6-final-review.md` (section 9): three review rounds, with D81 applied between
the second and third. Round 3's finding 3.2 is open: D73(b)'s settle defect, which D81(c)(4) found real
in 0.3.0, has no ledger disposition (section 13). C6.4 is unticked until the coordinator rules on it.

What remains:
- **The coordinator's ruling on D73(b)'s settle defect in 0.3.0** (section 13, review finding 3.2):
  either known issue 20 in CHANGELOG.md and the release notes, or ledger-only with its reason. Then
  section 4's no-hidden-blocker row and this section cite that row, and C6.4 is ticked.
- **NOT MET parts, dispositioned and disclosed:** the Linux fsync-bound halves (D53(b), D75(a)); the
  installed upgrade, UAT-01, -03, -09, -10, UAT-05 run 1 and UAT-12's upgrade leg carried from
  candidate 7 (D53(f), D59, D76(f)); macOS, arm64 and Linux model sessions `unknown` (C4.11, D34(c),
  D80(c)); C4.9 (c), the packaged unavailable object, carried from candidate 7 (section 5, 3.10);
  the recorded-corpus replay tier (D67(g)); the rows no step can execute (D37(c)); the C1.16 rig's
  co-load red (D78(b)).
- **C7.6** housekeeping, with owner consent: it deletes worktrees that hold uncommitted work, and
  D81(c)(7) keeps it open.
- The post-release items of section 18, and the 0.3.1 release under its gate (D81(c)(8)): the
  fix/v031-* branches merged and green on hosted ci.yml and nightly, Linux -race on the touched
  packages, the C1.16 rig's co-load condition with 0 missing Reads and 0 LOUD spool drops,
  `release-check --tag v0.3.1` on the reference host on AC, and the published marketplace installed
  on Windows and Linux.

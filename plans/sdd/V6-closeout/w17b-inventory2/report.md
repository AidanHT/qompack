# Wave 17b inventory2

Branch `closeout/w17b-inventory2`. Workflow `wf_10516e89-86a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `6416091fad2d4c3a7aaae40cb058e6b0acde5a0a`

### Root cause

Wave 17's fix round had two errors. The 1.5.12 cell described X11's two no-ledger-only reds as runs 'without and with the ledger'. And five C5.2 rows published verified on a carry their cells did not name. Re-checking that carry under D57(e) with an executed-files coverage trace showed the earlier per-file proof had missed three executed changed files (toolnames.go, manifest.go and the comment-only pathstest/home.go). Separately, the X11 reds behind the hot-path 'failed' rows ran on battery. D57(d) makes battery runs invalid as reference measurements, so those rows are judged by the AC quiet C5.1 run and e1 instead.

### Summary

## Wave 17b inventory2 (C6.2 / C6.3 follow-up)

Branch `closeout/w17b-inventory2` (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17b-inventory2), cut from `closeout/integration` b31d0753. Five commits, nothing pushed. Everything changed is under plans/. No Go code, plugin/**, plugin.json or hooks.json changed.

### Candidate 7 identity
- I polled night.log: "candidate 7 frozen at d20309c03ffc364e4cc48663be73cfbb1f2309b2 (integration b31d0753...)" at 07:59:43Z. I used that SHA.
- Pre-freeze passed: gate, testpkgs and internal, all exit=0.
- All six bin/ sha256 match w17-release's independent build. The bundles were built and host-validated, and verify/v6 was pushed at 08:00:17Z.
- `git diff --stat b31d0753 d20309c0 -- . ':(exclude)plans'` is empty.

### Files changed
- plans/sdd/V6-remediation/inventory-current.tsv:
  - Two new columns, `c7_result` and `c7_evidence`.
  - The c6 columns are re-disposed.
  - The 7 original columns are byte-identical to 9a56b305 (checked by script), 304 rows, LF.
- plans/sdd/V6-closeout/inventory-c6-map.md is regenerated. It is now titled "candidates 6 and 7" and gains:
  - candidate 7 identity;
  - c7 vocabulary;
  - new codes X11-E1, X11-BAT, C7-CARRY, PRE7, P-CI7, P-C52R, P-REL7 and P-TAG;
  - a D57(e) C5.2 table;
  - a c6/c7 count table;
  - a table of the rows that are not a plain carry;
  - a c7 column in section 3.
- w17-inventory/ scripts and proofs:
  - dispose.py and gen_c6map.py, updated.
  - c52exec.py (new).
  - runs/c52-executed-files.txt (new).
  - runs/c7-carry-proof.txt (new).
  - runs/w17b-*.log (the check logs).
- Superseding notes updated in inventory-current-notes.md, current-candidate-gate-map.md and inventory-map.md.
- Carried-defect documents: V2-SP-08, V2-SP-10 and V2-WAVE1 are reworded, and all five carried-defect documents gain a candidate 7 note. No status changed, so test/guards expectation data is untouched.

### Counts

| result | c6 | c7 |
|---|---|---|
| verified_in_target | 252 | 248 |
| partial_verified | 33 | 41 |
| implemented_unverified | 1 | 2 |
| failed | 5 | 0 |
| unknown | 7 | 7 |
| unsupported | 3 | 3 |
| documented | 3 | 3 |

### (1) The verification section's two findings
- **1.5.12 "without and with the ledger":** fixed. The cell now says both red artifacts were X11's no-ledger arm, in both isolated executions (p3-win-e2e-timing.log 81.9/57.3 ms, p3-win-x11-alone.log 98.3/81.9 ms). The ledger phase was never reached in either.
- **Five rows published verified on an unnamed C5.2 carry:** every C5.2 row's cell now names the carry (D57(e)) and its proof file. I did not settle this with prose: I measured which files each benchmark executes. c52exec.py runs each C5.2 benchmark once at -benchtime=1x under a set-mode coverage profile over the whole module. It then intersects the executed files with `git diff 0d06ab12 99d0b18c`. This is a trace, not a timing measurement, and I ran it after the pre-freeze finished.
- **What the trace found:** the earlier per-file argument had missed three executed changed files: internal/core/toolnames.go, internal/pluginmanifest/manifest.go and the test helper internal/paths/pathstest/home.go. Results by set:
  - **Execute no changed file, so they carry:** obs, config, paths, symbols, dag, rules, skills and internal/scheduler.
  - **Execute only pathstest/home.go, whose change is four comment lines with no //go: directive, so they carry on the comment-only reading:** eval, sketch, chunk, canon, store, negknow and checkpoint.
  - **Do not carry under D57(e) as written:**
    - BenchmarkTombstone (1.8.2) and the five daemon scheduler benches (1.12.17 and 1.16.11's daemon part) execute toolnames.go, which gained CutHostPluginTool. No executed block covers a changed line.
    - BenchmarkOnToolUse_* (1.8.13) executes observer.go, where sessionState gained two fields, which is a layout change on the measured path.
    - BenchmarkHookNoop_InProcess (1.1.27's cli part) executes changed lines 276 and 279 of manifest.go.
  - So 1.1.27, 1.8.2, 1.12.17 and 1.16.11 are partial_verified, and 1.8.13 is implemented_unverified. All five are pending P-C52R, a quiet C5.2 re-run on candidate 7.
  - SP08-D1's document now says its figure does not carry; the wontfix status stands on D54.

### (2) D57 applied
- **The X11 reds are battery runs:** both ran after the AC cut at 22:29:32 EDT. They are recorded as X11-BAT, invalid as reference measurements and neither pass nor fail.
- **WTIME was on AC:** it ran 22:18:53-22:23:42, before the cut.
- **Rows re-disposed from failed to partial_verified:** 1.5.12, 1.12.14 and section 3.14. Each Windows half is verified on the designated quiet C5.1 run on AC (B-A 30.72 ms, B-B 24.58 ms) and on e1 (X11-E1: 3/3 runs, pair 3/3, 0 deferred). Each also asserts Linux B-A/B-B, which D53(b) leaves not verified in target.
- **1.17.6 is partial_verified, not wave 17's stated fallback implemented_unverified:** C51 has executed its B-A and B-E halves, and its launcher split has no possible artifact. This also explains why it differs from 1.17.5.
- **1.10.16** no longer cites the battery B-E figures.
- **Not counted as reference measurements, for transparency:** the pre-freeze e2e/hotpath reds on 61b0cd66. They ran at below-normal priority beside the evening load, not in an isolated pass.

### (3) RED-FAULT and RED-RELDRY
- **On c6, these rows stay failed:** 1.5.19, 1.17.11, 1.17.14, 1.17.18 and 1.17.19. The red is on c6, and the root cause is recorded under D57(a)/(b).
- **1.5.19, 1.17.11, 1.17.14 on c7: partial_verified.**
  - test/fault with dd8e9fd2/88626fdd passes in PRE7 testpkgs, including TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence.
  - They close when hosted test (windows-latest) at -count=2, and the ubuntu and macos test legs, are green on c7.
  - 1.17.14 also keeps the still-owed F-4 mapping.
- **1.17.18 on c7: partial_verified.** It closes on a green hosted release-dry-run on c7 plus `release-check --tag v0.3.0` on the reference host (P-REL7). actionlint and goreleaser run at the tag (P-TAG).
- **1.17.19 on c7: implemented_unverified.** It closes on every ci.yml job green on c7, plus branch protection (C7.3).
- **1.17.17 on c7: partial_verified.** Version 0.3.0 is in c7's code and its version tests passed in PRE7 internal; the version agreement closes at the tag.

### (4) The candidate 7 carry
- The c6→c7 diff (runs/c7-carry-proof.txt) contains only:
  - product change: version.go and plugin.json;
  - test/fault, test/guards/nonrefdisk_test.go and the golden plugin.json;
  - two workflows and .goreleaser.yaml;
  - docs/, README and CHANGELOG.
- Rows over unchanged code carry as C7-CARRY.
- **Docs rows (1.18.5/6/7/10 and others) are partial_verified on c7:** docs changed, so test/docs re-ran. It is green on Windows in PRE7 and green in hosted docs job 110757119402 (ci.yml 36981590450); test (ubuntu-latest) is pending.
- **Rows that carry with an extra check:** 1.1.20 and 1.17.1 carry by the byte proof plus host-validate and the bin/ match. Guard and core rows carry with PRE7.
- **P-LIVE and P-C55** run on c7's frozen bundles (D57(c)).

### Wave 17 review nits resolved
- 1.5.12 now cites the bench-gate jobs.
- 1.17.5 vs 1.17.6 is explained.
- The GATE row now names the runpatterns waiver.
- The 1.15.8 false positive is recorded, and the row names TestSequitur_InvariantsHoldAfterEveryAppend.
- The map notes that the phase3 and coordinator paths resolve on verify/v6. They are now committed there (4e981166, c4c25353).

### Commits

- 3630f54ec42253c053a1ab9e9d8689106f7e2244 chore(v6): add the c7 carry and c5.2 executed-files proofs
- 1fee1c20b44fd5c34c9c0fbb1f6e9be3686c8c03 docs(v6): re-dispose the inventory under d57 and add candidate 7
- 2fb8761a9b0424860f742392a4fa9d082fdc11d0 docs(plans): state the carried-defect evidence on candidate 7
- 352b7a463b0587cb2d484e752725bd3a71cf0443 chore(v6): record the w17b inventory checks on windows
- 6416091fad2d4c3a7aaae40cb058e6b0acde5a0a docs(v6): cite candidate 7's first green hosted jobs

### Tests

- `go test -p 2 -count=1 ./test/guards` — ok (49.4s), runs/w17b-guards-windows.log
- `go test -p 2 -count=1 ./test/docs` — ok (3.0s), runs/w17b-docs-windows.log
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS docmarkers, PASS runpatterns (re-run after the last commit too)
- `go run ./tools/devtool gen-config-docs --check / gen-command-docs --check / gen-mcp-docs --check` — all up to date, exit 0 (runs/w17b-checks-windows.log)
- `python plans/sdd/V6-closeout/w17-inventory/c52exec.py > runs/c52-executed-files.txt` — all 19 benchmark sets exit 0; executed changed files only as listed in the summary
- `python plans/sdd/V6-closeout/w17-inventory/dispose.py --write; python plans/sdd/V6-closeout/w17-inventory/gen_c6map.py` — 304 rows; c6 252/33/1/5/7/3/3, c7 248/41/2/0/7/3/3; 7 original columns byte-identical to 9a56b305, LF (script check)
- `golangci-lint` — not run: no Go file touched

### Criterion changes

- None weakened. The C5.2 carry is now decided from a coverage trace of executed files, not a per-file argument. This is stricter: five rows moved from verified_in_target to partial_verified (1.1.27, 1.8.2, 1.12.17, 1.16.11) or implemented_unverified (1.8.13).
- 1.5.12, 1.12.14, 1.17.6 and section 3.14 went from failed to partial_verified under D57(d), (e) and (g). Wave 17's interpretation 4 ('failed if any isolated artifact is red even when the quiet run passes') is superseded by D57(e). The cited reds were battery runs.
- 1.17.6 goes to partial_verified rather than wave 17's stated fallback implemented_unverified. C51 executed its B-A/B-E halves, and the launcher split has no possible artifact, which is the map's partial_verified definition.
- Map vocabulary: (a) 'failed' now says a red stays on its own candidate even after a fix; a later candidate carrying the fix is judged on its own runs. (b) On candidate 7, partial_verified also covers a changed-code row whose Windows re-run is green (PRE7) and whose named hosted run is pending (P-CI7).
- Docs rows whose c6 result was verified_in_target (1.18.5, 1.18.6, 1.18.7, 1.18.10, section 3.8) are partial_verified on c7. docs/ changed, so the c6 evidence does not carry until test (ubuntu-latest) is green on c7.

### Open issues

- P-C52R: the candidate 5 C5.2 figures for 1.1.27 (BenchmarkHookNoop_InProcess), 1.8.2 (BenchmarkTombstone), 1.8.13 (the three BenchmarkOnToolUse_*), 1.12.17 and 1.16.11 (the five daemon scheduler benches) do not carry by D57(e) as written, because their benchmarks execute files that changed between c5 and c6 (runs/c52-executed-files.txt). These rows close on a quiet C5.2 re-run on candidate 7. For 1.8.2, 1.12.17 and 1.16.11, a ruling on toolnames.go would also close them (see needs_owner).
- P-CI7: ci.yml 36981590450 on d20309c0 was in progress. docs, plugin-validate, crossbuild, security, replay-gate, test-e2e ubuntu/macos, timing and bench-gate were already green. Still open: test (windows-latest) at -count=2 for 1.5.19/1.17.11/1.17.14; release-dry-run for 1.17.18/1.17.19; test (ubuntu-latest) for the docs rows 1.18.1/5/6/7/8/10/11 and section 3.8. When the run lands, change those rows' c7 cells in dispose.py and regenerate.
- P-REL7: release-check --tag v0.3.0 on the reference host (docs/release.md section 1) is still owed, for 1.17.18. P-TAG: the tag closes 1.17.17's version agreement and runs actionlint/goreleaser.
- P-LIVE and P-C55 run on candidate 7's frozen bundles and have no verdicts yet. Their rows are partial_verified on both candidates, with the cells naming the scenarios.
- 1.17.19 also needs branch protection on develop and main (C7.3).
- The inventory map cites phase3/c7/ (night.log, prefreeze/), which is still uncommitted in qompack-v6. The citations resolve once the coordinator commits it to verify/v6.
- Not cited as reference evidence: the pre-freeze e2e/hotpath reds on 61b0cd66 (prefreeze/e2e.log: X11's D42 pair at the ledger arm; prefreeze/hotpath.log: the integration row, 593 deferred). Both ran on AC but at below-normal priority, beside the evening load. I listed them under X11-BAT for transparency; D57(g) closes X11 on e1.
- No shipped doc was touched, so there are no evidence-dependent placeholder sentences to list.

### Needs the owner

- Comment-only reading of D57(e): seven C5.2 benchmark sets (eval, sketch, chunk, canon, store, negknow, checkpoint, covering 1.2.12, 1.3.7, 1.3.16, 1.4.14, 1.6.19, 1.9.12, 1.10.17, and the store/checkpoint part of 1.16.11; also SP06-D2, SP09-D1, SP10-D1 and SP20-D2's confirmations) execute exactly one changed file. That file is the test helper internal/paths/pathstest/home.go, whose c5->c6 change is four comment lines and no //go: directive. Wave 17b counts these as carrying. If you read 'byte-unchanged' literally instead, those rows move to partial_verified, pending P-C52R.
- toolnames.go: BenchmarkTombstone (1.8.2) and the five daemon scheduler benches (1.12.17, 1.16.11) execute internal/core/toolnames.go. That file gained a function (CutHostPluginTool) that none of them executes, and no executed block covers a changed line. D57(e) as written fails, so these rows are partial_verified. A ruling that an appended, unexecuted function leaves D57(e) met would make them verified_in_target. 1.8.13 (observer.go struct layout) and 1.1.27's cli bench (executed changed lines in manifest.go) fail under either reading.

## Independent review

### review:inventory2: needs-fixes

- **major** `plans/sdd/V6-closeout/w17-inventory/c52exec.py:48; plans/sdd/V6-remediation/inventory-current.tsv:117 (1.6.19) and :269 (1.16.11); plans/sdd/V6-closeout/inventory-c6-map.md:124,135` — The D57(e) executed-files proof only intersects with non-test .go files (`:(exclude)*_test.go`). The benchmark functions and their helpers live in _test.go files, and the benchmark executes them. For the internal/store set those files changed between c5 and c6. The cells for 1.6.19 (published verified_in_target) and 1.16.11's store part still say "the one executed file that changed c5->c6 is ... pathstest/home.go ... so the executed code is byte-identical". That is false. D57(e) as written ("every file the benchmark executes is byte-unchanged") is not met for the store set. Worse, the c6 change was made to fix a defect that invalidated candidate 5's own store C5.2 binary. The map also says the proof "lists the files with an executed statement, setup included", which overstates what it does.
  - Evidence: In `git diff 0d06ab12 99d0b18c`:
- internal/store/search_test.go changes the bodies of BenchmarkSearch_1000Roots and BenchmarkSearch_1000Roots_DistinctChunks (`t := &testing.T{}; newTestStore(t, …)` becomes `newTestStore(b, …)`).
- internal/store/gc_test.go makes the same change to BenchmarkGC_50kObjects.
- internal/store/testdouble_test.go changes newProject/newTestStore/openOver to take testing.TB. Its new comment says the zero T "left HOME and USERPROFILE pointing into a leaked temp tree for every benchmark after it, and pathstest.Main failed the binary after PASS (candidate 5's quiet C5.2)".
- Confirmed in qompack-v6 plans/sdd/V6-closeout/phase3/c5/quiet/c52-win/: all 10 c52-win-r*-candidate-store-1s.log end with "pathstest: HOME is ... at the end of the run, not the isolated home ... every test after it ran against that directory". GC ran before CountFold, Search and MarkEncoded in that output.
- c52-names.tsv ties Search_1000Roots to C5.2 "search" (1.6.19's store/read/search set) and GC_50kObjects/MarkEncoded/CountFold to 1.16.11's phase-7 store family.
- Other carrying sets (paths, obs, checkpoint, eval, sketch, chunk, canon, negknow, …) have no changed test file that their benchmarks execute. Only store is affected.
  - Fix: 1. Extend c52exec.py to also diff the bench package's *_test.go files between C5 and C6, and flag every changed test file that defines a run benchmark or a helper it calls (here search_test.go, gc_test.go and testdouble_test.go). Regenerate runs/c52-executed-files.txt.
2. In dispose.py, move 1.6.19 to partial_verified. Either carry only the PutBytes/GetChunk/OpenSpan/PutObject benches (bench_test.go and benchStore are byte-unchanged and ran before the leak) and send Search_*, GC_50kObjects, CountFold and MarkEncoded to P-C52R, or send the whole store set to P-C52R.
3. Change 1.16.11's store clause to "does not carry" and add the store family to P-C52R in the map's P-C52R code and C5.2 table.
4. Fix the map sentence at :124 so it says test files are now included (or, before step 1, that they were excluded).
5. Leave SP20-D2 citing only GetChunk and SP06-D2 only PutBytes cold/warm. Regenerate the TSV and map.
- **minor** `plans/sdd/V6-closeout/inventory-c6-map.md:103 (X11-BAT row; source gen_c6map.py:161)` — X11-BAT says the pre-freeze prefreeze/e2e.log red was "X11's D42 pair at the ledger arm". It was not: the red is the ledger-resident arm's gated B-A p99 breach, and the D42 pair was never evaluated. The state column also calls both pre-freeze reds "invalid as reference measurements (D57(d))", but both ran on AC. They are invalid because they were not isolated (D28: below-normal priority, evening load), not under D57(d)'s battery rule.
  - Evidence: prefreeze/e2e.log:150 has `ledger-resident run ...` with `B-A n=2064 ... p99= 57.344ms limit=50.000ms [FAIL]`. Lines 260-268 have `Messages: ledger-resident run ... bench-hotpath exited non-zero ... a gated budget breached`. There is no `X11 pair (gated: ...)` line; compare x11-e1 summary.txt, which has v3_x11_test.go:553 "X11 pair (gated: PASS)". w17-x11win/report.md:246 records the pre-freeze runs as "AC, below-normal priority, evening load".
  - Fix: In gen_c6map.py:161, replace "(X11's D42 pair at the ledger arm)" with "(X11's ledger-resident arm, B-A p99 57.3 ms against 50; the D42 pair is not reached)". Split the state column: battery runs are invalid under D57(d); the pre-freeze runs, on AC but not isolated, are not reference measurements under D28. Make the same correction in the 1.5.12/3.14 notes if they repeat it, then regenerate.
- **minor** `plans/sdd/V6-remediation/inventory-current.tsv:289 (1.17.18 c7_evidence), :290 (1.17.19); inventory-c6-map.md:109 (C7-CARRY) and the vocabulary section` — Candidate 7 dispositions use the file's own vocabulary inconsistently, in two places.
- 1.17.18: its c7 cell starts with the code C7-CARRY, which the map defines as "candidate 6's disposition and evidence carry". But the row's disposition changes from failed to partial_verified. The map defines partial_verified as "every automated step the row names is green on the candidate", and its c7 extension requires "Windows re-run is green (PRE7)". The row's central step, release-check / release-dry-run, has not run green on c7, and PRE7 did not run release-check.
- 1.17.19: it is implemented_unverified ("nothing the row needs has executed on the candidate yet"), yet the P-CI7 code itself lists seven ci.yml jobs already green on c7 when the map was written.
These two rows are judged by opposite readings of the same situation.
  - Evidence: 1.17.18's c7_evidence begins "C7-CARRY; TestReleaseCheckDeterminismVersion carries ...; closes on a green release-check: hosted release-dry-run on c7 (P-CI7) and release-check --tag v0.3.0 ... (P-REL7)". For 1.17.19, the P-CI7 row lists docs 110757119402, plugin-validate 110757119388, crossbuild, security, replay-gate and test-e2e ubuntu/macos as green on c7.
  - Fix: Drop the leading C7-CARRY from 1.17.18's cell, or define a code for "part carries, disposition re-judged". Give 1.17.18 and 1.17.19 the same disposition under one stated rule. Either both stay partial_verified, and the map's c7 partial definition gains "or the row's only remaining steps are named hosted/tag runs", or both are implemented_unverified with the definition adjusted. Regenerate.
- **nit** `plans/sdd/V6-closeout/w17-inventory/dispose.py:107; TSV rows 1.2.12, 1.3.7, 1.3.16, 1.4.14, 1.6.19, 1.9.12, 1.10.17` — These rows are published verified_in_target on "the comment-only reading ..., for the owner to ratify". D57(e) as written says byte-unchanged, and pathstest/home.go is not. The distinction is principled: a comment changes no compiled code, unlike toolnames.go's appended function. But the table publishes the more lenient reading before any ruling, and the previous verification round flagged exactly this pattern.
  - Evidence: `git diff 0d06ab12 99d0b18c -- internal/paths/pathstest/home.go`: +4 comment lines. needs_owner item 1 in the implementer result.
  - Fix: Record the reading as a D33 decision in the D57/D58 ledger, so the cells cite a ruling instead of "for the owner to ratify". Otherwise hold these rows at partial_verified until it is ratified.
- **nit** `plans/sdd/V6-closeout/inventory-c6-map.md (P-CI7 code; docs rows 1.18.1/5/6/7/8/10/11, section 3.8; 1.5.19/1.17.11/1.17.14)` — The rows were correct when the map was written. Since then, ci.yml 36981590450 on d20309c0 has test (ubuntu-latest) 110757119503 and test (macos-latest) 110757119443 completed success. The docs rows' only stated pending leg, and two of the three RED-FAULT legs, are now green.
  - Evidence: `gh run view 36981590450 --json jobs`: test (ubuntu-latest) success, test (macos-latest) success. test (windows-latest) and release-dry-run are still in_progress.
  - Fix: When the run lands, update the c7 cells in dispose.py as open_issues already says: the docs rows and 3.8 can close on 110757119503, and RED-FAULT then waits only on test (windows-latest). Regenerate.

## Fix seat (review resolution) — status `done`, head `f2fd71b7`

### Root cause

The implementer's D57(e) proof measured executed files with Go coverage, which never instruments _test.go files, and its diff also excluded them. So it missed the store set's changed benchmark bodies and helpers, which were the fix for candidate 5's own HOME leak. That let 1.6.19 publish verified_in_target on a carry D57(e) does not allow. Separately, X11-BAT misdescribed the pre-freeze red as the D42 pair and labeled it with the battery rule. And 1.17.18 and 1.17.19 were judged by opposite readings of the same candidate 7 situation.

### Summary

## Wave 17b inventory2, fix seat

The reviewer's three findings were all correct, and all three are fixed. The fix round added two commits on `closeout/w17b-inventory2` (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17b-inventory2), on top of the implementer's five. Nothing is pushed. Every change is under plans/; no Go code, plugin/**, plugin.json or hooks.json changed.

### Review resolution

**1. major: the D57(e) proof could not see `_test.go` files. Confirmed and fixed.**
- **Why it missed them:** Go coverage never instruments `_test.go` files, and c52exec.py also excluded them from its diff.
- **What changed c5->c6 in internal/store:**
  - The bodies of BenchmarkGC_50kObjects (gc_test.go) and both BenchmarkSearch_1000Roots* (search_test.go): a zero `testing.T` became `b`.
  - The helpers newProject, newTestStore, openOver and objectPaths in testdouble_test.go now take `testing.TB`.
  - Candidate 5's own store run shows the effect. All ten `phase3/c5/quiet/c52-win/c52-win-r*-candidate-store-1s.log` end with pathstest's "HOME is ... at the end of the run" failure after PASS. In those logs, CountFold, both Search benches and MarkEncoded ran after GC.
- **New proof half:** `plans/sdd/V6-closeout/w17-inventory/c52tests.py` writes `runs/c52-test-files.txt`. It reads git objects at 99d0b18c and runs nothing.
  - In each benchmark package's `_test.go` files it follows, by name, the functions reachable from each benchmark and from TestMain, with comments and string literals stripped.
  - It also lists every package-level initializer and `init()`, marking each changed or unchanged.
  - It intersects these with the 88 `_test.go` files that changed c5->c6, and gives a breakdown per benchmark.
- **Result:** only three sets execute a changed test file:
  - **store:** see the store table below.
  - **daemon:** new package initializer `errInjectedSpoolLock` in precompact_settle_retry_test.go.
  - **cli:** two new package initializers in precompact_spool_submode_test.go.
  - Every other carrying set executes no changed `_test.go` file. That includes observer, checkpoint, obs and paths, whose new test files their benchmarks never reach. This matches the reviewer's "only store".
- **Store set by group:**

| benchmarks | what they execute that changed | carries |
|---|---|---|
| GC, both Search | changed lines in their own bodies and helpers | no, under any reading |
| CountFold, MarkEncoded | ran after the HOME leak on c5 | no, under any reading |
| PutBytes*, PutObject, GetChunk, OpenSpan, OpenStore | reach the changed testdouble_test.go (newFakeClock and the storeOpt helpers) but no changed line in it; the binary also runs new maint_edge_test.go's `errInjectedBarrier` initializer | no under D57(e) as written; a ruling could carry them (needs_owner) |

- **Dispositions:**
  - 1.6.19 goes from verified_in_target to partial_verified (pending P-C52R).
  - 1.16.11's store part no longer carries.
  - The cells for 1.12.17 and 1.16.11 now name the daemon initializer, so a ruling on toolnames.go alone no longer closes them.
  - 1.1.27's cli cell names the new cli initializers.
  - 1.8.2, 1.9.12 and 1.10.17 now also cite the test-file proof as clean.
  - In V2-WAVE1-carried-defects.md, SP06-D2 (PutBytes) and SP20-D2 (GetChunk) now say their c5 figure does not carry. Their status still stands on D54, the same pattern as SP08-D1. SP09-D1 and SP10-D1 cite the clean test-file proof.
- **Map:** the C5.2 section now describes both halves of the proof, splits the store set into three table rows, and gives a third owner reading. P-C52R now includes the store set.

**2. minor: X11-BAT's account of the pre-freeze reds. Confirmed and fixed.**
- `phase3/c6/prefreeze/e2e.log:150` shows the ledger-resident arm red: B-A p99 57.344 ms against 50. There is no X11 pair line.
- The text now reads "X11's ledger-resident arm, B-A p99 57.3 ms against 50; the D42 pair is not reached", with explicit `phase3/c6/prefreeze/` paths.
- The state column is split: battery runs are invalid under D57(d); the pre-freeze runs were on AC but not isolated, so they are not reference measurements under D28; neither counts as a pass or a fail.
- The 1.5.12 and 3.14 cells never mentioned the pre-freeze runs, so they needed no change.

**3. minor: 1.17.18 and 1.17.19 were judged by inconsistent rules. Confirmed and fixed.**
- The map's c7 definition of partial_verified now reads: a row judged on c7's own runs (changed code, or a c6 red re-judged), where part of it has executed green on c7 (PRE7 or named P-CI7 jobs) and only named unlanded runs remain (P-CI7, P-REL7, P-TAG, C7.3).
- **1.17.18:** the leading C7-CARRY is dropped. The cell now starts with PRE7 and names release-check's steps that PRE7 ran green: gate, and test/release including TestReleaseCheckDeterminismVersion (testpkgs.log: `ok test/release`).
- **1.17.19:** now partial_verified, listing its green c7 jobs.

### Also folded in (new hosted evidence)
- I read `gh run view 36981590450 --json jobs` at 2026-10-02 08:31Z (read-only). 18 of 21 jobs were green. Still running: test (windows-latest) 110757119316, lint-windows 110757119431 and release-dry-run 110757119491.
- test (ubuntu-latest) 110757119503 and test (macos-latest) 110757119443 are green on c7. So docs rows 1.18.5, 1.18.6, 1.18.7, 1.18.10 and section 3.8 are verified_in_target on c7.
- The c7 cells of the RED-FAULT rows now wait only on test (windows-latest) at -count=2.
- The P-CI7 code text and the counts in inventory-current-notes.md are updated.

### Counts

| result | c6 | c7 |
|---|---|---|
| verified_in_target | 251 | 251 |
| partial_verified | 34 | 39 |
| implemented_unverified | 1 | 1 |
| failed | 5 | 0 |
| unknown | 7 | 7 |
| unsupported | 3 | 3 |
| documented | 3 | 3 |

All 304 rows are present, the 7 original columns are byte-identical to 9a56b305 (script check), and the file is LF.

### Commits

- 3630f54ec42253c053a1ab9e9d8689106f7e2244 chore(v6): add the c7 carry and c5.2 executed-files proofs
- 1fee1c20b44fd5c34c9c0fbb1f6e9be3686c8c03 docs(v6): re-dispose the inventory under d57 and add candidate 7
- 2fb8761a9b0424860f742392a4fa9d082fdc11d0 docs(plans): state the carried-defect evidence on candidate 7
- 352b7a463b0587cb2d484e752725bd3a71cf0443 chore(v6): record the w17b inventory checks on windows
- 6416091fad2d4c3a7aaae40cb058e6b0acde5a0a docs(v6): cite candidate 7's first green hosted jobs
- b28895b0 chore(v6): add the c5.2 test-file half of the d57(e) proof
- f2fd71b7 docs(v6): re-dispose the store c5.2 rows and fix review findings

### Tests

- `go test -p 2 -count=1 ./test/guards` — ok (26.5s), plans/sdd/V6-closeout/w17-inventory/runs/w17b-fix-guards-windows.log
- `go test -p 2 -count=1 ./test/docs` — ok (2.6s), runs/w17b-fix-docs-windows.log
- `go run ./tools/devtool fmt-check` — exit 0 (runs/w17b-fix-checks-windows.log)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers (371 plan documents)
- `go run ./tools/devtool gen-config-docs --check / gen-command-docs --check / gen-mcp-docs --check` — all up to date, exit 0
- `python plans/sdd/V6-closeout/w17-inventory/c52tests.py > runs/c52-test-files.txt` — exit 0; store, daemon and cli are the only sets that execute changed _test.go files
- `python plans/sdd/V6-closeout/w17-inventory/dispose.py --write; python plans/sdd/V6-closeout/w17-inventory/gen_c6map.py` — 304 rows; c6 251/34/1/5/7/3/3, c7 251/39/1/0/7/3/3; original 7 columns byte-identical to 9a56b305, LF
- `golangci-lint` — not run: no Go file touched

### Criterion changes

- None weakened. The D57(e) carry now also checks _test.go files, which is stricter: 1.6.19 moves from verified_in_target to partial_verified, and 1.16.11's store part no longer carries.
- On candidate 7, partial_verified is defined once for re-judged rows: part has executed green on c7 and only named unlanded runs remain. As a result, 1.17.19 goes from implemented_unverified to partial_verified, consistent with 1.17.18.
- Docs rows 1.18.5, 1.18.6, 1.18.7, 1.18.10 and section 3.8 are verified_in_target on c7, on hosted test (ubuntu-latest) 110757119503 and test (macos-latest) 110757119443 plus PRE7 and docs 110757119402.

### Open issues

- P-C52R: a quiet C5.2 re-run on candidate 7 is still owed. Benchmarks and rows: BenchmarkHookNoop_InProcess (1.1.27), BenchmarkTombstone (1.8.2), the three BenchmarkOnToolUse_* (1.8.13), the five daemon scheduler benches (1.12.17, 1.16.11), and now the whole internal/store set (1.6.19, 1.16.11; also the SP06-D2 and SP20-D2 confirmations). Evidence: runs/c52-executed-files.txt and runs/c52-test-files.txt.
- P-CI7: ci.yml 36981590450 on d20309c0 was still running at 08:31Z, with 18 of 21 jobs green. Still open: test (windows-latest) 110757119316 at -count=2 (closes 1.5.19, 1.17.11, 1.17.14), release-dry-run 110757119491 (1.17.18, 1.17.19) and lint-windows 110757119431 (1.17.19). When they land, update CI7_AT, CI7_GREEN and CI7_OPEN and those rows' c7 cells in dispose.py, then regenerate.
- P-REL7: release-check --tag v0.3.0 on the reference host is still owed for 1.17.18. P-TAG: the tag closes 1.17.17's version agreement and runs actionlint and goreleaser.
- P-LIVE and P-C55 run on candidate 7's frozen bundles and have no verdicts yet. Their rows are partial_verified on both candidates.
- 1.17.19 also needs branch protection on develop and main (C7.3).
- The map cites phase3/c7/ (night.log, prefreeze/). These citations resolve only after the coordinator commits that directory to verify/v6.
- No shipped doc was touched, so there are no evidence-dependent placeholder sentences.

### Needs the owner

- Comment-only reading of D57(e), now six sets: eval, sketch, chunk, canon, negknow and checkpoint (rows 1.2.12, 1.3.7, 1.3.16, 1.4.14, 1.9.12, 1.10.17 and 1.16.11's checkpoint part; also the SP09-D1 and SP10-D1 confirmations). Their only executed changed file is pathstest/home.go, whose change is four comment lines, and they execute no changed _test.go file. If 'byte-unchanged' is read literally instead, these rows move to partial_verified, pending P-C52R.
- Unreached-change ruling: (2) toolnames.go gained an unexecuted function. A ruling on that alone would carry 1.8.2. (3) Unreached test-file changes: the daemon scheduler benches and the store's PutBytes*, PutObject, GetChunk, OpenSpan and OpenStore benches reach no changed line of a test function, but each test binary runs a new package-level errors.New initializer (precompact_settle_retry_test.go and maint_edge_test.go), and testdouble_test.go is not byte-unchanged. A ruling covering (2) and (3) would carry 1.12.17 and those store figures. 1.6.19 and 1.16.11 stay partial_verified either way: GC, both Search benches, CountFold and MarkEncoded, as well as 1.8.13 and 1.1.27's cli bench, do not carry under any reading.

## Independent verification of the fix seat: needs-fixes

- **minor** `plans/sdd/V6-closeout/inventory-c6-map.md:149 (daemon row) and :160-166 (reading 3); dispose.py 1.12.17 and 1.16.11 cells; source gen_c6map.py; vs plans/sdd/V6-closeout/w17-inventory/runs/c52-test-files.txt (internal/daemon section)` — All three original findings are resolved, and I re-ran c52tests.py, which reproduces runs/c52-test-files.txt with the same content. One new inconsistency remains. The map and the row cells say the five daemon scheduler benches reach no changed line of a test function, and that their only changed test-file exposure is the new errInjectedSpoolLock initializer. The proof file they cite says something else: BenchmarkFeaturesFrom reaches changed lines in `tool` (prompt_warning_delivery_test.go), and daemon_test.go is an executed changed file (allOps initializer). The proof's hit is a false positive. BenchmarkFeaturesFrom only uses a struct field `o.tool` (scheduler_bench_test.go:41,56), and the name matching links that to the new method `(*warnDeliveryRig).tool` (prompt_warning_delivery_test.go:123). So the map's claim is right in substance, but it contradicts its own cited proof without saying why. It also leaves out daemon_test.go, which reading (3) would need to cover under the same 'not byte-unchanged' logic it applies to testdouble_test.go. An owner who rules on reading (3) from the cited proof would find FeaturesFrom listed as reaching changed lines.
  - Evidence: runs/c52-test-files.txt has `internal/daemon/prompt_warning_delivery_test.go: reached tool; changed lines inside tool` and `BenchmarkFeaturesFrom: prompt_warning_delivery_test.go (changed lines in tool)`, plus `internal/daemon/daemon_test.go: package initialization: var allOps at line 86 (unchanged)`. Map :149 names only toolnames.go and precompact_settle_retry_test.go. Map reading (3): 'reach no changed line of a test function, and neither do the daemon scheduler benches'. At 99d0b18c, scheduler_bench_test.go has `tool string` as an obs field and calls `FeaturesFrom(h, o.sig, o.tool, ...)`. The only `tool` function is `func (r *warnDeliveryRig) tool(...)`, which the benchmark never calls.
  - Fix: Choose one. (a) Make c52tests.py skip identifiers that follow a `.` (selector/field access), or that are declared as struct fields or locals, then regenerate runs/c52-test-files.txt. (b) Keep the proof as is, and add a sentence to the map's daemon row and reading (3) saying that FeaturesFrom's 'tool' hit is a name collision between the obs.tool field and (*warnDeliveryRig).tool, which the benchmark does not call. Either way, name daemon_test.go (executed through the unchanged allOps initializer, file changed) next to precompact_settle_retry_test.go in the daemon row and in reading (3), as testdouble_test.go is named for store. Then regenerate the map and TSV.


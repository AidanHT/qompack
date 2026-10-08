# C6.4 independent review of the V6 close-out report

The record that `plans/V6-CLOSEOUT-report.md` section 9's C6.4 cell cites. It lists each round's
verdict, every finding as the reviewer wrote it (condensed), and how each was resolved, with the
commit. The reviewers are non-authoring seats of workflow `wf_50d07daa-d92` (stages
`review:closeout:r1` and `review:closeout:r2`). Each read the report on `closeout/c6-final` against
the V6 plan's section 8 template and section 10 gate. The fixes are the `fix:closeout:r1` and
`fix:closeout:r2` stages on the same branch.

Commits quoted here are on `closeout/c6-final`, and none is on `develop` (`7fbb8a40`) yet:
`9325fc86`, `54c13eca` and `912e71c4` (round 1's fixes) and `c99a7794` (the inventory seat). Round
2's fixes are in the commit that adds this file.

**Status.** After round 2, every finding is resolved except one. The TestGC_DeadlineTruncatesAndResumes
red needs a ledger row, and only the coordinator may write D rows. It was a major in both rounds, so
C6.4 is not ticked in `plans/V6-CLOSEOUT-CHECKLIST.md` and the report is not signed off as the
close-out record until the row exists. Nothing else is open.

## Round 1, report at `88f2cbf4`: needs-fixes (1 major, 5 minors, 1 nit)

| # | Severity | Finding | Resolution |
|---|---|---|---|
| 1.1 | major | ci.yml `37746311073` on `develop` `fff45a45` has a red with no disposition: TestGC_DeadlineTruncatesAndResumes, job timing (windows-latest), `internal/store/gc_test.go:475`. `fff45a45` differs from the tag `1a368a4b` only in `.claude-plugin/marketplace.json`, so this is the released code. The report said the coordinator had classed it test-only but cited no artifact, and section 19 said no release blocker was open. | **Partly fixed** (`912e71c4`). The report no longer claims a classification. It marks the red NOT MET with no disposition in the header and in sections 6, 10, 13, 18 item 2 and 19, and section 10 sets out the evidence a ruling would weigh. The D row and the known-issues entry are the coordinator's, because this seat may not write D rows. Still open in round 2 (2.1). |
| 1.2 | minor | Section 3.10 was `verified_in_target`, but its packaged unavailable-object leg, C4.9 (c), is carried from candidate 7. By the map's own rule that makes it `partial_verified`. | **Fixed** (`54c13eca`, `912e71c4`). The reviewer suggested UAT-07 as an alternative and the seat rejected it with evidence: UAT-07 step 7 is a hash that was never stored, while C4.9 (c) is an indexed object whose bytes are gone (`live/rerun-c4/C4.9/notes.txt`). 3.10 is now `partial_verified`, section 3 counts 9/4/1, and the V6 plan's 3.10 box is unticked with the reason. |
| 1.3 | minor | The V6 plan's section 10 box for representative supported environments was ticked, while section 4's box for supported OS, filesystems and install stayed open for the same reason (macOS packaging, the arm64 targets, the carried upgrade). | **Fixed** (`912e71c4`). Unticked. The reason names the targets where the gate was met and points to section 4's open box and to C4.11, D34(c) and D80(c). |
| 1.4 | minor | CHANGELOG.md's 0.3.0 Known limits and `docs/release-notes/v0.3.0.md` still said that neither the Linux exec bit nor an install from the published marketplace had been observed, which contradicts D80. | **Fixed** (`9325fc86`). Both now match D80: Linux keeps the exec bit, the install from the release's own entries was rehearsed on Windows and Linux, and macOS is unobserved. These pages are reserved for the release step, so the seat named the edit for the coordinator to confirm. The published GitHub release body was not changed. |
| 1.5 | minor | C1.6 was unticked in the checklist although D76 records that it passed on candidate 8 and the report closes V6-RECOVERY-1 on it. | **Fixed** (`912e71c4`). Ticked on candidate 8 with D76 and `live/rerun-c8/C1.6/`: detection, not recovery, and known issue 18. |
| 1.6 | minor | Row 1.17.18 was `verified_in_target` while its actionlint half has no artifact. The 3.13 tick claimed "licenses/name check" although section 4 leaves the name check open. | **Fixed** (`54c13eca`, `912e71c4`). 1.17.18 is now `partial_verified`, so the 304 rows count 275/16/7/3/3 in the TSV, the map, the report, the plan ticks and C6.2. The 3.13 tick and its report row state that the name-check half is open. |
| 1.7 | nit | (a) The report quoted the Linux timing figures with no artifact. (b) Section 2 gave UTC dates. (c) The V6 plan header still read BLOCKED. | **Fixed** (`912e71c4`). (a) The figures cite `phase3/c8-CANDIDATE.md` and the report says the step logs are not committed. (b) Section 2 uses local dates. (c) The plan header points to the close-out report. |

## Round 2, report at `912e71c4`: needs-fixes (2 majors, 2 minors, 3 nits)

| # | Severity | Finding | Resolution |
|---|---|---|---|
| 2.1 | major | 1.1 again: the TestGC_DeadlineTruncatesAndResumes red is still undispositioned. The ledger ends at D80, and `w22-known-issues.md` does not list the red. The reviewer confirmed the report's evidence: `gc_test.go:441-442` prices the deadline, 471-479 asserts one pass outside co-load, and the timing (windows-latest) job passed on `37746414885` (`7fbb8a40`) and on `37562946379` attempt 1. | **Open: the coordinator's.** This seat may not write D rows, and its task does not open `w22-known-issues.md` to it. The report already states the red honestly, with no disposition, and now points to this record. Proposed row, from round 1's fix seat: *ci.yml `37746311073` on `develop` `fff45a45`, job timing (windows-latest): TestGC_DeadlineTruncatesAndResumes failed at `internal/store/gc_test.go:475`. This is a test defect of the wall-clock pricing class, not a product defect. The deadline is `gcResumeBudgetMultiple` times the control's mark (441-442), and outside co-load the single pass is judged with no re-pricing (471-479). The pass reached the sweep and the budget outlasted its first check: a correct collection, the shape CI run 34052269275 recorded. The runner is a hosted non-reference disk (Q1), and the same job passed on `37746414885` and on `37562946379` attempt 1. Test-only residual. After the release, give the non-co-load pass the co-load loop's re-price-on-miss, or price the budget from the judged pass.* Once the row exists: add the red to `w22-known-issues.md`'s test-only residuals, replace the report's NOT MET text in the header and in sections 4, 6, 10, 13, 18 item 2 and 19 with the row's citation, do the same in the V6 plan's section 2 sixth tick, and tick C6.4. |
| 2.2 | major | C6.1 is ticked "docs match evidence", but README.md and `docs/release.md` were never updated after the release. README's status paragraph says no release has been published and that the release workflow has never run. Its "Installed in Claude Code" row says windows/amd64 only. `docs/release.md`'s capability rows say the Linux exec bit, the published-marketplace install and the release workflow are not verified, and its section 7 says `release.yml` has never run. All of this contradicts D80 and release.yml `37738581717`. | **Fixed.** README.md: the status section (now "Status: released", anchor updated in `docs/uat.md`) states the release, the tag, release.yml `37738581717`, the byte comparison and the promotion. The "Installed in Claude Code" row adds D80's Windows and Linux installs, and the packaging paragraph and the host-version sentence follow D80. `docs/release.md`: the release-status paragraph says the release happened. The capability rows narrow to what is still unobserved: Linux sessions with a model, macOS and the arm64 targets, and the macOS exec bit. The release-workflow row is removed. Section 7 records the single release.yml run (its jobs and artifact checked with `gh run view` and the artifacts API), the Linux install, and that 0.3.0 shipped unsigned. `docs/uat.md`'s "no release has been published" sentence was stale in the same way and was corrected too. C6.1's tick text names this round. macOS stays unobserved everywhere. |
| 2.3 | minor | Report section 4's last exit-obligation row and the V6 plan's section 2 sixth tick say no blocker or major is open, citing D79, which ruled before the TestGC red existed. This contradicts sections 10 and 19. | **Fixed.** Both now read "except the unclassified TestGC_DeadlineTruncatesAndResumes red (section 10), pending its ledger row". |
| 2.4 | minor | The section 9 C6.4 cell gave round 1 only as counts, and no committed artifact listed the findings and their resolutions. | **Fixed.** This file. The C6.4 cell, the report header and section 19 cite it, and the cell no longer carries an [OWED] marker. |
| 2.5 | nit | Section 8 cited B-F to `c51-win.log`, but the figure is in `c51-win-bf.log`. | **Fixed.** Section 8 cites `phase3/c8/quiet/c51-win.log` and `c51-win-bf.log` for B-F. |
| 2.6 | nit | Section 6 said ci.yml `37361841760` had "Four reds". The run has five failed jobs with one cause. | **Fixed** after checking with `gh run view 37361841760`: the failed jobs are test on windows-latest, ubuntu-latest and macos-latest, release-dry-run and cover. The row reads "Five red jobs ..., one fixture cause (D68)". |
| 2.7 | nit | `live/rerun-c8/CARRIED.md` (lines 108 and 199) cites the range shorthand `rerun-c8/C4.9/cli/r8-r11`, which is not a path. `c99a7794` fixed the same shorthand in the map. | **Fixed.** Both places name the directory and its records: `r8-backup-create.*`, `r9-backup-verify.*`, `r10-backup-restore.*` and `r11-fsck-json-*`. The same shorthand in `docs/uat.md`'s UAT-03 Result was rewritten the way the map does it. |

## Checks run for round 2's fixes

- `go test -p 1 -count=1 ./test/docs/... ./test/guards/...` and
  `go run ./tools/devtool lint --only=docmarkers,runpatterns` on the round 2 tree, both passing.
- Every SHA quoted above was checked with `git merge-base --is-ancestor` against `closeout/c6-final`
  and `develop`.
- `gh run view 37738581717`: release workflow, push of `1a368a4b`, success. Its steps include
  release-check, the bundles, the marketplace, the host-validation upload (artifact
  `host-validation-evidence`), the release notes, the provenance attestation and goreleaser.

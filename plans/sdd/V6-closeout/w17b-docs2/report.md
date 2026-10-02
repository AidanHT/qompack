# Wave 17b docs2

Branch `closeout/w17b-docs2`. Workflow `wf_10516e89-86a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `6d9f5198df66442ea9f436ad3e32e86a5f5aaa53`

### Root cause

The w17-docs fix seat wrote the Windows row from X11's failing candidate 6 run. That run never reached the ledger leg: the figures it quoted (B-A p99 98.3 ms, B-B 81.9 ms) come from the no-ledger baseline leg, and they are lower bounds, not certified p99s. The run itself was on battery, and D57(d) now classes battery runs as invalid reference measurements. Separately, the w17-release version commit made several statements stale: the 0.1.0 text in README, uat.md and the releasechecksteps.go comment, the "blocked" status in release.md, and install.md section 9. release.yml's notes step could only render release-scope over SP-17 records from 2026-09-16 (F3).

### Summary

W17B-DOCS2 (C6.1, C6.5's CHANGELOG, F3). Branch closeout/w17b-docs2 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17b-docs2, 7 commits on b31d0753. Nothing that ships in a bundle changed: no plugin/**, plugin.json, hooks.json or bin/ Go code. The Go changes are one comment in tools/devtool and a new test/guards file.

(1) The w17-docs verification finding (major) is resolved. README's Windows row and CHANGELOG's Known limits no longer say candidate 6 failed the hot path "with the ledger resident", and the unclassified-red wording is gone. They now give:
- candidate 6's quiet C5.1 on AC (phase3/c6/quiet/): B-A p99 30.7 ms, B-B 24.6 vs 50; B-E p99 170.8 vs 2000 (B-E_cpu 31.3); B-F p99 73.7 vs 250 over 2000 tool uses, n=200
- the Windows -race tree pass, and the isolated wall-clock rows, which ran on AC: p3-win-timing finished at 22:23 local, before the AC cut at 22:29
- X11 3/3 on AC from phase3/c6/x11-e1/summary.txt: n=2064, nothing deferred; both legs B-A p99 36.9 ms, B-B 22.5-24.6 vs 50; hook_controlled_observed p50 5.1 ms in both legs against a 6.4 ceiling; the harness and the integration row also 3/3
- D57(d): every strict X11 failure since D41 was on battery, battery runs are neither a pass nor a fail, and on battery the hot path degrades to spool submode with nothing lost (D53(c))
- the D32/D53(h) statement on AC plus a Defender-excluded path

(2) Updated to match the evidence:
- **Hosted CI row:** release-dry-run is X11 on the hosted fsync tail, dispositioned by D57(a), and the job now declares QOMPACK_NONREFERENCE_DISK; test (windows-latest) is the test/fault read-order race, D57(b); the re-run on candidate 7 is said to be pending.
- **Linux:** the non-root -race tree, e2e and child pass. The fsync rows stay not verified in target (D53(b)), with the container's quiet B-A/B-B p99 of 65.5/61.4 ms against 15.
- **Bundles:** 91 files byte-identical, and candidate 7's one-byte bin/ difference is explained (D57(c)).
- **Workflows in the docs:** README's CI table now lists release-dry-run's release-version bundles and its nonreference declaration. release.md section 2 records D57(a) in place of "pending the owner's ruling".
- **fsck known limit (D57(b)):**
  - new troubleshooting section 9 entry. It quotes fsck's exact retention-row message (the hash is fsckShortHash's 12 hex digits) and the daemon-row snapshot note, and says to stop the daemon (idle exit, or end the pid in daemon.lock, because there is no stop command) and run fsck again.
  - new cannot-do section 4 entry.
  - new release.md capability row and CHANGELOG bullet.
- **AC/Defender note:** added to cannot-do's "No performance guarantee" entry, a new release.md capability row, the release notes and README. release.md section 1 step 3 says the reference release-check runs on AC.

(3) Candidate naming. README, CHANGELOG and release.md all already cite plans/, so each now says "release candidate 7, whose commit and frozen bundles are recorded in `plans/sdd/V6-closeout/phase3/c7-CANDIDATE.md`" (code span, not a link). No candidate 7 SHA is baked in. Candidate 6's 99d0b18 stays only as the base candidate 7 was built on.

(4) Stale text after the version commit:
- README: the version paragraph says core.Version and plugin.json read 0.3.0, so a plain source build reports 0.3.0, and that the v0.2.0 tag was never a release.
- release.md status: lists what the tag still waits on. Before the tag: candidate 7's live lane (D53(f)); C5.5 under A8; hosted ci/nightly on candidate 7 including the release-version bundle byte comparison (C7.2, D53(h)(4)); release-check --tag on the reference host on AC (D57(a)/(d)). After the tag: the pre-release, its install rehearsal (D53(h)(3)), the bin/ check and the promotion.
- release.md procedure: steps 5-6 describe the pre-release draft and the notes source.
- install.md section 9: the commands have nothing to fetch until publication (install from section 3 meanwhile). While 0.3.0 is a pre-release only form (c), pinned to v0.3.0, reaches it; (a) and (b) work after the promotion and the marketplace PR.
- uat.md lines 81, 141-142 and 146-149 are reworded; line 212 is kept as history.
- The releasechecksteps.go comment is updated.

(5) F3, release notes:
- **New file:** docs/release-notes/v0.3.0.md. It covers what 0.3.0 is and is not (A8's claim boundary), install with the six per-target entries and the pinned v0.3.0 command, unsigned binaries, verified-where by target, how the Windows timings were taken (D53(h), D57(d), D53(c)), the namespace caveat, and the residuals with their decisions (D6/D16, D29, D35(b)/D38, D35(c), D44, D48, D54, D56(e), D57(b), D24/D26, D7). Links are absolute blob/v0.3.0 URLs so they work on the GitHub release page.
- **release.yml notes step:** copies docs/release-notes/${GITHUB_REF_NAME}.md when it exists and otherwise falls back to release-scope --markdown. goreleaser's --release-notes is unchanged.
- **Guards:** new test/guards/releasenotes_test.go. It pins both branches, that the notes are written before goreleaser, and the --release-notes hand-off, with a negative test on reshaped steps. It also requires every notes file to be named v<semver>.md, open on '# Qompack <version>' and carry no TODO/TBD marker, and requires the notes file for core.Version to exist. That last rule is a new policy: every future version bump must ship its notes. Red-first check: with HEAD's release.yml put back, TestReleaseNotesStepPrefersTheTagsOwnNotes fails ("missing docs/release-notes/${GITHUB_REF_NAME}.md", "missing if [ -f ").

Commit hygiene: no attribution trailers, and every commit has a Refs footer. Three subjects were first committed over 64 characters counted with the prefix (CI's regex counts only the text after 'type(scope): ', so they were CI-valid). I reworded them on my own branch with filter-branch --msg-filter; the tree is unchanged (`git diff 4eb8501b 6d9f5198` is empty), and the refs/original backup was deleted.

### Commits

- 06b42f9b docs(readme): name release candidate 7 and its windows evidence
- 46fbf6ab docs(changelog): restate 0.3.0's known limits for candidate 7
- 55c84f4a docs(troubleshooting): a live-daemon fsck root may be transient
- a6b24672 docs(uat): drop the 0.1.0 source-build text after the bump
- e22640ea docs(release-notes): write 0.3.0 notes from the evidence
- 695d8b0c docs(release): restate 0.3.0's status and what is still owed
- 6d9f5198 ci(release): publish the tag's own notes file when it exists

### Tests

- `go test -p 2 -count=1 ./test/docs` — ok 2.535s (exit 0), final tree
- `go test -p 2 -count=1 ./test/guards` — ok 31.597s (exit 0), final tree
- `go test -p 2 -count=1 -run '^TestReleaseNotesStepPrefersTheTagsOwnNotes$' -v ./test/guards` — PASS; with HEAD's release.yml restored temporarily it FAILS (missing docs/release-notes/${GITHUB_REF_NAME}.md and the if [ -f branch); restored afterwards
- `go test -p 2 -count=1 -run '^TestReleaseNotesStepGuardRejectsReshapedSteps$' -v ./test/guards` — PASS
- `go test -p 2 -count=1 -run '^TestReleaseNotesFilesAreNamedForTheirTagAndFilled$' -v ./test/guards` — PASS
- `go run ./tools/devtool gen-config-docs --check; go run ./tools/devtool gen-command-docs --check; go run ./tools/devtool gen-mcp-docs --check` — all three up to date, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns,golangci-lint` — PASS golangci-lint, PASS runpatterns, PASS docmarkers (exit 0)
- `go vet ./tools/devtool ./test/guards` — exit 0

### Criterion changes

- New guard rows in test/guards/releasenotes_test.go: TestReleaseNotesStepPrefersTheTagsOwnNotes, TestReleaseNotesStepGuardRejectsReshapedSteps, TestReleaseNotesFilesAreNamedForTheirTagAndFilled. The last one requires docs/release-notes/v<core.Version>.md to exist, so every future version bump must ship hand-written notes. This adds a check and weakens none.

### Open issues

- C5.5 verdict (A8 item 1). Quote it verbatim with H3's single label ('recovery advantage shown' or 'recovery advantage not shown'): in docs/release-notes/v0.3.0.md, in a new 'Evaluation' section after 'What 0.3.0 is not'; in docs/user-guide.md's /qompack:eval section; in README.md 'What 0.3.0 is not' (third bullet, after the A8 sentence); and in CHANGELOG.md [Unreleased] 'Known limits' (first bullet). If the verdict is inconclusive, add A8 item 3's sentence: inconclusive is expected by design and is not evidence that Qompack adds nothing. If not-applicable comes twice, say the study did not run as designed. release.md's status paragraph then drops C5.5 from its 'still owed' list.
- Candidate 7 live-lane verdict (D53(f)) and the D53(i) count of host-reported hook failures and timeouts. Say what candidate 7's lane passed or failed (D52's 16 sessions, the UAT-12 upgrade leg, the C1.7 restore smoke, UAT-01/C4.1 through the qompack-windows-amd64 entry) in four places: README.md 'What is verified where', row 'Installed in Claude Code' (it now says candidates 3 and 4 only); the README paragraph under that heading, whose last sentence says the live lane and live evaluation have not run yet; CHANGELOG 'Known limits' ('by the live lanes on candidates 3 and 4'); and docs/cannot-do.md 'Installed in Claude Code on windows/amd64 only'. In docs/release-notes/v0.3.0.md, the windows/amd64 row ('earlier release candidates' frozen bundles were installed') must name candidate 7's install.
- D53(i) host-seen p50/p95 per hook (A8 item 6). Add a per-hook table to docs/release-notes/v0.3.0.md under 'Supported targets and what is verified where', after the Windows timing paragraph, and the same figures to CHANGELOG 'Known limits'. The figures do not exist yet.
- Namespace under a release entry. Once UAT-01/C4.1 has run through qompack-windows-amd64, replace the 'has not been observed' sentence in docs/install.md section 9 'What a session shows' and in docs/release-notes/v0.3.0.md (the paragraph after the targets table) with the observed server name, tool prefix and command prefix. Then drop the 'not verified in target' row for the namespace in docs/release.md's capability table and the clause in CHANGELOG 'Known limits'.
- Hosted CI on candidate 7 (C7.2). README.md's hosted-CI row and docs/release.md section 7 say neither ci.yml nor nightly.yml has run on candidate 7 yet. Replace that with the candidate 7 run IDs and outcomes, including whether the release-version-bundles artifact matches qompack-bundles/c7. Then drop the item from release.md's 'still owed' list. The release notes' hosted row says only that the suites run on all three runners, so it stays true either way.
- plans/sdd/V6-closeout/phase3/c7-CANDIDATE.md is cited as a code span from README 'Status', CHANGELOG's first paragraph and release.md's header. It resolves only once the candidate 7 freeze commit on verify/v6 writes that file; this branch, cut from integration, does not have it. If the freeze names the record differently, change those three sentences.
- The bundle claim in docs/release-notes/v0.3.0.md ('two builds of release candidate 6 were byte-identical across all 91 bundle files') and README's 'Every target's bundle' row ('Candidate 7's bundles are compared with them as above') rest on candidate 6. If candidate 7's freeze records its own double build, state candidate 7's result there instead.
- Outside this seat's files and left alone: release.yml's release-job env comment still says the declaration 'is pending the owner's ruling (C7.2)'; D57(a) has since recorded it. The trailing comment after the goreleaser step still says it 'creates a DRAFT release', with no mention of the pre-release flag. Both are comments only, but test/guards pins release.yml.
- Executable bit and Gatekeeper on Linux and macOS stay 'not verified in target' in README, install sections 9 and 10, the capability table and the release notes until D53(h)(3)'s pre-release install rehearsal (Linux container, no model) records them.
- CHANGELOG.md stays under '## [Unreleased]'. At the release the coordinator renames it '## [0.3.0] - YYYY-MM-DD' (release.md section 1, step 2).

## Independent review

### review:docs2: needs-fixes

- **minor** `README.md:104 (Linux row)` — The Linux row gives the container's quiet-run B-A/B-B figures as plain p99s ("its quiet run read B-A p99 65.5 ms and B-B p99 61.4 ms against 15"). The cited log says those gates failed because the p99 could not be certified, and that the figures are the delivered set's own p99, which is a lower bound. Most of the hot-path requests in that run were deferred. This is the same kind of error as the w17-docs verification's major finding, which this seat was asked to fix.
  - Evidence: phase3/c6/quiet/c51-linux/c51-linux.log: B-A n=1538 p99=65.536ms [FAIL] and B-B n=1538 p99=61.440ms [FAIL], against 5064 planned samples. Line 25: '5130 hot-path requests sent, 1539 delivered live to the daemon, 3591 DEFERRED to the client spool and 0 lost'. Lines 26-27: "B-A's p99 CANNOT be certified and its gate is failed on that ground: 3526 of the 5064 planned samples never reached the daemon's histogram ... The p99 field reports the DELIVERED set's own p99 for diagnosis only".
  - Fix: Reword to: 'its quiet run failed B-A and B-B because their p99s could not be certified: 3,591 of 5,130 hot-path requests were deferred to the client spool (0 lost), and the delivered samples alone read B-A p99 65.5 ms and B-B p99 61.4 ms against 15, which are lower bounds; B-E passed'.
- **minor** `docs/release-notes/v0.3.0.md:67-69` — The release notes say this release's binaries differ from candidate 6's 'only by an unreferenced copy of the old version string'. D57(c) and the w17-release byte comparison also record darwin/arm64's ad-hoc signature hash as a difference. README.md:93-95 states both, so the published notes say less than the evidence and disagree with the README.
  - Evidence: Ledger D57(c): 'Every bin/ differs from candidate 6 by one unreferenced byte ... plus darwin/arm64's ad-hoc signature hash.' w17-release/report.md:259: 'each binary differs by 1 byte (unreferenced "0.1.0" literal), darwin-arm64 also its 32-byte ad-hoc signature hash'. README.md:94-95 includes the signature hash.
  - Fix: Change it to '...differ from candidate 6's only by an unreferenced copy of the old version string, plus darwin/arm64's ad-hoc signature hash (decision D57(c))'.
- **minor** `docs/troubleshooting.md:1281; docs/cannot-do.md:572` — Both new fsck entries say --seal-check is the only fsck mode that takes the daemon's lock ('only `--seal-check` does' and 'without `--seal-check`'). --repair also takes the daemon lock, so the claim about the product's behaviour is wrong.
  - Evidence: internal/cli/fsck.go:216: 'qompack fsck: --seal-check and --repair both take the daemon lock'. fsck.go:131: ReadOnly 'is false under --repair, which writes, AND under --seal-check, which ... DOES acquire the daemon lock'.
  - Fix: Troubleshooting: 'a plain fsck does not take the daemon's lock (only `--repair` and `--seal-check` attempt it)'. Cannot-do: 'A plain fsck (without `--repair` or `--seal-check`) does not take the daemon's lock'.
- **minor** `.github/workflows/release.yml:74-80; docs/release-notes/v0.3.0.md (whole file); ledger D57(c)` — F3 only takes effect if the tagged commit contains this branch. Candidate 7's pre-freeze runs on integration b31d0753, which does not contain it. The tag would therefore have to be on a descendant that changes release.yml's notes step, adds test/guards/releasenotes_test.go and edits a tools/devtool comment. D57(c) allows only 'candidate 7 or a docs-only descendant', and this descendant is not docs-only in a strict reading. If the tag is instead put on candidate 7 itself, the draft gets release-scope's stale SP-17 notes again, and the notes' blob/v0.3.0 links and anchors such as release.md#capability-status-at-030 point at the older docs. The implementer's open_issues do not mention this.
  - Evidence: phase3/c7/night.log: 'pre-freeze on integration b31d0753ff44...'. This branch is 7 commits on b31d0753 and touches .github/workflows/release.yml, test/guards/releasenotes_test.go and tools/devtool/releasechecksteps.go. D57(c): 'The release tags candidate 7 or a docs-only descendant, and the published bin/ must equal qompack-bundles/c7'. w17-release/report.md:168 already plans a manual fallback, `gh release edit v0.3.0 --notes-file`.
  - Fix: Add an open issue for the coordinator. Either record that a descendant counts as tag-eligible when its bundled bin/ equals qompack-bundles/c7 and it changes only docs, tests and workflows (none of which reach bin/ under -buildvcs=false), so this branch can land before the tag. Or keep the tag on candidate 7 and replace the draft's notes with `gh release edit v0.3.0 --notes-file docs/release-notes/v0.3.0.md` from this branch.
- **nit** `README.md:103 (Windows row); CHANGELOG.md:135-136` — The text says every strict X11 failure since D41, 'including candidate 6's isolated X11 runs inside test/e2e', ran on battery. p3-win-e2e-timing started on AC at 22:23:43 and crossed the AC cut at 22:29:32, so that run ran across a power transition rather than entirely on battery. Under D57(d) it is invalid either way, but the sentence is not exact.
  - Evidence: phase3/c6/p3-win-e2e-timing.json: started_at 2026-10-01T22:23:43-04:00, ended_at 22:49:16. D57(d): 'AC off 22:29:32 to 23:55:00 on 10-01'. p3-win-x11-alone (22:49-23:00) ran wholly on battery.
  - Fix: Say 'ran on battery or across the unattended AC cut' or 'ran with the AC cut', and keep the D57(d) citation.
- **nit** `.github/workflows/release.yml:32-33 and :95` — Two release.yml comments are now stale. The env comment says the declaration 'is pending the owner's ruling (C7.2)', but release.md section 2, README and D57(a) now record the ruling. The trailing comment says publishing follows 'reading the scope table in its notes', but for a tag that has a notes file this commit makes the notes the hand-written file. The seat's file scope limits it to the notes step, and the implementer listed both as open issues, but the published docs now contradict the workflow's own comments.
  - Evidence: release.yml:32 'is pending the owner's ruling'. release.yml:95 'taken after reading the scope table in its notes'. docs/release.md new text: 'Both declarations are recorded by decision D57(a)'.
  - Fix: Route both to the ci seat or the coordinator. Line 32: 'carries D53(e)/D55 to the tag-time gate, recorded by D57(a)'. Line 95: '...after reading its notes (docs/release-notes/<tag>.md, or release-scope's table when the tag has none); it is published as a pre-release first'.

## Fix seat (review resolution) — status `done`, head `fd969db9`

### Root cause

The implementer seat left two kinds of error. First, it quoted the Linux container's delivered-set p99s as if they were certified p99s. That is the same class of error as the w17-docs major finding. Second, it understated two facts: which fsck modes take the daemon lock, and the bin/ difference between candidates 6 and 7 that D57(c) records. Separately, F3's notes step only takes effect if the tagged commit contains this branch, and candidate 7's code (integration b31d0753) does not.

### Summary

FIX seat for W17B-DOCS2. Branch closeout/w17b-docs2 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w17b-docs2, now 8 commits on b31d0753 (head fd969db9). The fix commit fd969db9 touches only README.md, CHANGELOG.md, docs/cannot-do.md, docs/troubleshooting.md and docs/release-notes/v0.3.0.md. Nothing that ships in a bundle changed.

## Review resolution

1. **README.md:104, Linux row (minor). CONFIRMED and fixed.** C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/c6/quiet/c51-linux/c51-linux.log supports the finding:
   - Line 25: "5130 hot-path requests sent, 1539 delivered live ... 3591 DEFERRED ... 0 lost".
   - Lines 26-27: B-A's and B-B's p99 "CANNOT be certified"; the printed p99 is the delivered set's own, "a lower bound".

   The row now says the quiet run failed B-A and B-B because their p99s could not be certified. It gives the deferral count (3,591 of 5,130 deferred to the client spool, 0 lost) and calls the delivered-sample figures, B-A p99 65.5 ms and B-B p99 61.4 ms against 15, lower bounds. It cites the log path and keeps "B-E passed". A grep found no other shipped doc quoting 65.5/61.4 as p99s; the release notes' Linux row gives no figures.

2. **docs/release-notes/v0.3.0.md:67-69, bin/ difference (minor). CONFIRMED and fixed.**
   - The ledger's D57(c) reads "Every bin/ differs from candidate 6 by one unreferenced byte ... plus darwin/arm64's ad-hoc signature hash", and README.md:94-95 already said so.
   - The release notes now add "plus darwin/arm64's ad-hoc signature hash (decision D57(c))".
   - CHANGELOG.md's Known limits bullet had the same omission ("by one unreferenced byte (D57(c))") and is fixed the same way.

3. **docs/troubleshooting.md:1281 and docs/cannot-do.md:572, fsck lock (minor). CONFIRMED and fixed.** internal/cli/fsck.go:216 prints "--seal-check and --repair both take the daemon lock", and the ReadOnly field comment (fsck.go:131) says the same.
   - Troubleshooting now reads: "A plain fsck does not take the daemon's lock (only `--repair` and `--seal-check` attempt it)".
   - Cannot-do now reads: "A plain fsck (without `--repair` or `--seal-check`) does not take the daemon's lock".
   - No other shipped doc in this seat's files made the --seal-check-only claim.

4. **F3 and tag eligibility under D57(c) (minor). CONFIRMED as a coordination gap. No file change; carried as an open issue below.**
   - phase3/c7/night.log shows "pre-freeze on integration b31d0753...", and this branch is not in it.
   - D57(c) says "The release tags candidate 7 or a docs-only descendant".
   - This branch also changes .github/workflows/release.yml, adds test/guards/releasenotes_test.go and edits a tools/devtool comment.
   - None of those reach bin/ (devtool is not bundled, and builds use -buildvcs=false), but under a strict reading the branch is not docs-only.
   - If the tag lands on candidate 7 itself, the draft gets release-scope's stale SP-17 notes. The notes file would also be missing at that tag, so its blob/v0.3.0 links and anchors (for example release.md#capability-status-at-030) would not resolve.
   - The coordinator rules under D33, so this seat records the choice in open_issues and does not decide it.

## Verification (final tree, fd969db9)

- go test ./test/docs: ok
- go test ./test/guards: ok
- The three gen-*-docs --check commands: up to date
- fmt-check: exit 0
- lint --only=docmarkers,runpatterns: PASS

No Go was touched in this round, so golangci-lint was not re-run. The implementer's run passed on 6d9f5198, and the only Go files since then are unchanged.

## Carried forward

Every implementer open issue is still open; see open_issues. needs_owner was empty and stays empty. The guard rows the implementer added are TestReleaseNotesStepPrefersTheTagsOwnNotes, TestReleaseNotesStepGuardRejectsReshapedSteps and TestReleaseNotesFilesAreNamedForTheirTagAndFilled, all in test/guards/releasenotes_test.go.

### Commits

- 06b42f9b docs(readme): name release candidate 7 and its windows evidence
- 46fbf6ab docs(changelog): restate 0.3.0's known limits for candidate 7
- 55c84f4a docs(troubleshooting): a live-daemon fsck root may be transient
- a6b24672 docs(uat): drop the 0.1.0 source-build text after the bump
- e22640ea docs(release-notes): write 0.3.0 notes from the evidence
- 695d8b0c docs(release): restate 0.3.0's status and what is still owed
- 6d9f5198 ci(release): publish the tag's own notes file when it exists
- fd969db9 docs(v6): correct linux p99s, fsck lock modes and the bin/ diff

### Tests

- `go test -p 2 -count=1 ./test/docs` — ok 3.701s (exit 0), tree at fd969db9
- `go test -p 2 -count=1 ./test/guards` — ok 39.370s (exit 0), tree at fd969db9
- `go run ./tools/devtool gen-config-docs --check; go run ./tools/devtool gen-command-docs --check; go run ./tools/devtool gen-mcp-docs --check` — all three up to date, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns (1518 of 1518 checkable resolved), PASS docmarkers, exit 0

### Criterion changes

- Carried from the implementer: the new guard rows in test/guards/releasenotes_test.go are TestReleaseNotesStepPrefersTheTagsOwnNotes, TestReleaseNotesStepGuardRejectsReshapedSteps and TestReleaseNotesFilesAreNamedForTheirTagAndFilled. The last one requires docs/release-notes/v<core.Version>.md to exist. These rows add checks and weaken none. This fix round changed no criteria.

### Open issues

- Tag eligibility for F3 (review finding 4, for the coordinator under D33). Candidate 7's code is integration b31d0753, which does not contain closeout/w17b-docs2. This branch changes .github/workflows/release.yml's notes step, adds test/guards/releasenotes_test.go, edits a comment at tools/devtool/releasechecksteps.go:170 and adds docs. None of that reaches bin/ under -buildvcs=false. D57(c) allows only 'candidate 7 or a docs-only descendant'. Choose one: (a) record that a descendant is tag-eligible when its bundled bin/ equals qompack-bundles/c7 and it changes only docs, tests and workflows, then land this branch before the tag; or (b) tag candidate 7 itself and, before promotion, replace the draft's notes with `gh release edit v0.3.0 --notes-file docs/release-notes/v0.3.0.md` from this branch (w17-release report line 168 already plans this fallback). Under (b) the notes' blob/v0.3.0 links and anchors (for example release.md#capability-status-at-030, docs/release-notes/v0.3.0.md itself) would point at the older docs at that tag. They would have to be retargeted to a branch or commit that contains them.
- C5.5 verdict (A8 item 1). Quote it verbatim with H3's single label ('recovery advantage shown' or 'recovery advantage not shown') in four places: docs/release-notes/v0.3.0.md, in a new 'Evaluation' section after 'What 0.3.0 is not'; docs/user-guide.md's /qompack:eval section; README.md 'What 0.3.0 is not' (third bullet, after the A8 sentence); and CHANGELOG.md [Unreleased] 'Known limits' (first bullet). If the verdict is inconclusive, add A8 item 3's sentence: inconclusive is expected by design and is not evidence that Qompack adds nothing. If not-applicable comes twice, say the study did not run as designed. docs/release.md's status paragraph then drops C5.5 from its 'still owed' list.
- Candidate 7 live-lane verdict (D53(f)) and the D53(i) count of host-reported hook failures and timeouts. Say what candidate 7's lane passed or failed (D52's 16 sessions, the UAT-12 upgrade leg, the C1.7 restore smoke, UAT-01/C4.1 through the qompack-windows-amd64 entry) in these places: README.md 'What is verified where', row 'Installed in Claude Code' (it now names only candidates 3 and 4); the README paragraph under that heading, whose last sentence says the live lane and live evaluation have not run yet; CHANGELOG 'Known limits' ('by the live lanes on candidates 3 and 4'); and docs/cannot-do.md 'Installed in Claude Code on windows/amd64 only'. In docs/release-notes/v0.3.0.md, the windows/amd64 row ("earlier release candidates' frozen bundles were installed") must name candidate 7's install.
- D53(i) host-seen p50/p95 per hook (A8 item 6). Add a per-hook table to docs/release-notes/v0.3.0.md under 'Supported targets and what is verified where', after the Windows timing paragraph, and put the same figures in CHANGELOG 'Known limits'. The figures do not exist yet.
- Namespace under a release entry. Once UAT-01/C4.1 has run through qompack-windows-amd64, replace the 'has not been observed' sentence with the observed server name, tool prefix and command prefix in two places: docs/install.md section 9 'What a session shows', and docs/release-notes/v0.3.0.md, in the paragraph after the targets table. Then drop the namespace's 'not verified in target' row from docs/release.md's capability table and the namespace clause from CHANGELOG 'Known limits'.
- Hosted CI on candidate 7 (C7.2). README.md's hosted-CI row and docs/release.md section 7 say neither ci.yml nor nightly.yml has run on candidate 7 yet. Replace that with candidate 7's run IDs and outcomes, including whether the release-version-bundles artifact matches qompack-bundles/c7. Then drop the item from release.md's 'still owed' list.
- plans/sdd/V6-closeout/phase3/c7-CANDIDATE.md is cited as a code span from README 'Status', CHANGELOG's first paragraph and release.md's header. It resolves only once the candidate 7 freeze commit on verify/v6 writes that file. If the freeze names the record differently, change those three sentences.
- The bundle claim in docs/release-notes/v0.3.0.md ('two builds of release candidate 6 were byte-identical across all 91 bundle files') rests on candidate 6, and so does README's 'Every target's bundle' row ('Candidate 7's bundles are compared with them as above'). If candidate 7's freeze records its own double build, state candidate 7's result in both places instead.
- Outside this seat's files and left alone, both in .github/workflows/release.yml, which test/guards pins: the release job's env comment still says the declaration 'is pending the owner's ruling (C7.2)', though D57(a) has since recorded it; and the trailing comment after the goreleaser step still says it 'creates a DRAFT release', with no mention of the pre-release flag.
- Executable bit and Gatekeeper on Linux and macOS stay 'not verified in target' in README, install sections 9 and 10, the capability table and the release notes until D53(h)(3)'s pre-release install rehearsal records them.
- CHANGELOG.md stays under '## [Unreleased]'. At the release the coordinator renames it '## [0.3.0] - YYYY-MM-DD' (docs/release.md section 1, step 2).

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



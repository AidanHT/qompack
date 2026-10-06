# Wave 20 docs (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-docs`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `docs/release-notes/v0.3.0.md:66-70 (published verbatim by .github/workflows/release.yml:74-80); also CHANGELOG.md:12-14, docs/release.md:7-8`: This diff changes release.yml so that a tag with docs/release-notes/<tag>.md publishes that file verbatim as the draft's notes. The file says 'this release's binaries differ from candidate 6's only by an unreferenced copy of the old version string, plus darwin/arm64's ad-hoc signature hash (decision D57(c)). Candidate 6's machine evidence below carries to this release on that comparison.' That is true for candidate 7 only. D58(e) says the release tags candidate 8, and candidate 8 changes product code: internal/daemon/drain.go, registry.go, internal/checkpoint/intent.go, writer.go, internal/cli/config.go, internal/contract/assertions.go, plus wave 19b's rehydrate, hostperm and cli changes. So the published notes would claim a byte-carry and an evidence carry that no longer hold. CHANGELOG.md and docs/release.md still say 0.3.0 'is being cut from release candidate 7'.
- **major** `docs/release-notes/v0.3.0.md:66-70 (also CHANGELOG.md:139-141, README.md:91-97)`: The release notes say the release's binaries differ from candidate 6's only by one unreferenced version-string byte (plus darwin/arm64's signature hash), and that candidate 6's machine evidence carries to the release on that comparison. D58(e) says the release tags candidate 8, which is candidate 7 plus code changes that reach the bundle (and wave 19b's rehydrate rewrite). For the tagged release this claim is false.
- **major** `README.md:97-98`: README says 'The live lane and the live evaluation run on candidate 7's frozen bundles and have not run yet.' The same README's table row (line ~107) says the live lanes on candidates 3, 4 and 7 installed bundles. The release notes report candidate 7's live lane (20 sessions, 474 hook calls). The live evaluation C5.5 runs on candidate 8, not candidate 7.
- **major** `docs/release.md:12-16, docs/release.md:341, README.md:102`: The release-status text lists the candidate 7 live lane and hosted ci.yml/nightly.yml on candidate 7 as still owed, and says 'Neither workflow has run on candidate 7 yet' and 'Neither job has been re-run on candidate 7 yet'. The ledger records all of these as done. The owed items are now candidate 8's.
- **major** `README.md:22-23, CHANGELOG.md:12-13, docs/release.md:7-8 and 183-184`: Every release-identity sentence names 'release candidate 7' and c7-CANDIDATE.md, and the capability table cites decisions 'D1 to D57'. The c8 freeze merges integration into verify/v6 without editing docs, so the frozen candidate 8 tree, and a tag of it, will still say the release is candidate 7. The table also omits D58-D60.
- **major** `CHANGELOG.md:120-121`: The Security entry 'Rehydration pointers never show a path the host currently denies, a path outside the project, or a home- or variable-rooted path; they point by hash' is false on INT. A tool pointer's argument summary still shows a denied path. The fix is wave 19b's, and even after it D60 keeps documented limits that this absolute sentence does not mention. (Interaction with out-of-scope wave 19b.)
- **minor** `README.md:102 and docs/release.md:341`: The hosted-CI statements are stale. README says 'Neither job has been re-run on candidate 7 yet', and docs/release.md says 'Neither workflow has run on candidate 7 yet'. Both workflows did run on candidate 7: ci.yml 36981590450 (green except test (windows-latest), whose cause is fixed on INT) and nightly 36981711009 (green), as D58(a) records.
- **minor** `CHANGELOG.md:93-112 (Fixed)`: The [Unreleased] 0.3.0 section has no entry for candidate 8's user-visible fixes. The off-by-default features that release.md's capability table lists as shipped are also missing from Added.
- **minor** `docs/release-notes/v0.3.0.md:122-160 (Known limits); docs/release.md:225-230`: D59 classes 'backup refuses while a newer settingsVersion is in force' as a documented known limit. It is in backup.md and troubleshooting §6 but not in the release notes' limits or the capability table. After 19b, D60(c)(ii) ('below the smallest loss notice, nothing is injected') qualifies release-notes line 5 and README:36-37 'names what did not fit'.
- **minor** `docs/uat.md:752-753 (UAT-05 Result), docs/uat.md:1572-1573 (UAT-12 Result)`: Both Result blocks still say the ruling is open: UAT-05 'The coordinator decides which document is right', and UAT-12 'whether section 2 is in its scope is left to the coordinator'. D59(b) decided UAT-05's question, and D60(c)(i) ruled on D50's scope.
- **minor** `.github/workflows/ci.yml:622-624`: D53(h)(4) and D58(a) depend on `include-hidden-files: true` on the release-version-bundles upload: the pre-tag comparison with the frozen bundles needs .mcp.json and .claude-plugin/. No guard pins it. If a later workflow edit drops the line, the artifact silently loses those files, and the comparison covers fewer files than the release ships.
- **minor** `docs/troubleshooting.md:530-543 (§5 F-C7-UAT04-1 entry); docs/release.md:229`: The w19-docs review's nits were left unfixed: the fix seat resolved findings 1-5 only. (a) §5's Meaning says 'nothing older than the newest restatement is added after it', which the same paragraph's data contradicts: 7 of 13 evolution entries were kept, so older deltas do get in through item 2's share, and only the step-9a re-admission is skipped. (b) The Symptom names the state keys `Tokens`/`Budget`, but on disk they are lowercase `tokens`/`budget`. (c) release.md's combined D59 row links cannot-do only for the files-view limit, not for the evolution limit's own anchor.
- nit `test/docs/closeout_c7_claims_test.go:88-100`: TestUATIntroNamesCandidate7 requires the docs/uat.md introduction to contain `d20309c0` and D59. Candidate 8's live re-check (D59, D60(f)) will rewrite those result blocks, and docs/uat.md:20 already talks about candidate 8. Whoever updates the page for candidate 8 must change this test in the same commit, or the docs job and every test job go red.
- nit `.github/workflows/ci.yml and release.yml (runs-on: ubuntu-latest)`: Hosted runs now carry the annotation 'The ubuntu-latest label will migrate to Ubuntu 26 beginning October 19, 2026'. A tag, or a release-dry-run re-run, after that date runs release-check's whole tree, e2e and cover on an image that has never been green for this repo.
- nit `docs/install.md:259-262`: 'When a daemon spawn finds no copy of its own binary that verifies, it writes one' leaves out the held-open case. There, nothing is written or pruned and the daemon runs from the plugin binary.
- nit `docs/release.md:230`: The combined D59 capability row links troubleshooting §5/§9 and only the files-view cannot-do anchor. The evolution limit's own cannot-do entry is not linked.
- nit `docs/release-notes/v0.3.0.md:80-84 vs docs/install.md:376-378`: The release notes say the entry name appears only in the plugin id and the install path. install.md says it also appears in the session's plugins[].source.

## impl:docs: status `done`, head `822f8d4d`

### Summary

Docs and tests only. No product code changed. Branch closeout/w20-docs in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-docs, three commits on top of 738d67c7.

I re-checked every finding and nit in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave20/docs.json against 738d67c7, and every one holds. Each is now fixed. Before any doc edit I added rows that fail on 738d67c7 (test/docs/closeout_c8_claims_test.go: 5 tests, 22 failures, all red on the base).

What changed:
(1) Release identity. README.md, CHANGELOG.md and docs/release.md now name release candidate 8 (D58(e)) and its record by path, `plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md` "when it is frozen". The capability table now cites D1 to D61 and the candidate 8 tree. Each page lists what candidate 8 still owes: its night chain (c8-night.sh / overnight-c8), hosted ci.yml and nightly with the release-version bundle byte match, the live re-check (D59, D60(f)), C5.5 on its frozen bundles, and the local release-check --tag on AC power.
(2) The byte-carry paragraph. The release notes, README and CHANGELOG used to say candidate 6's evidence carries by a byte comparison. They now say candidate 8 changes product code, so its own night and lanes supply the evidence. The candidate 6/7 figures stay, labelled as theirs.
(3) Hosted CI. Candidate 7's runs are recorded, and I checked both with gh: ci.yml 36981590450 on d20309c0 failed only test (windows-latest), dispositioned by D58(b); nightly 36981711009 passed 31/31; the hosted bundles matched byte for byte. The pages now say candidate 8's runs are owed.
(4) README live sentence. It now says candidate 7's lane ran (D59), and that the live re-check and C5.5 on candidate 8 are owed.
(5) CHANGELOG.
   - Fixed gains: drain pass progress, fork current work, hook settingsVersion logged at warn, stable status order, same-session-restart, the loss notice (D59(b), D60(c)(ii)) and the command connect budget (D60(e)).
   - Added gains: the features that ship off by default.
   - Security: the absolute pointer sentence is replaced by D61(b)'s actual guarantee and its limits. That covers structured summaries judged whole, free text screened, aliases/globs/run-time names as limits, and sections 2-4 outside D50 per D60(c)(i).
(6) Known limits. The release notes, the capability table, CHANGELOG and two new docs/cannot-do.md entries now list: backup refuses while a newer settingsVersion is in force; below the smallest loss notice nothing is injected (named in the drop report and LOUD); and the limits of the free-text screen.
(7) docs/uat.md. UAT-05 and UAT-12 each get a "Ruling (...)" line, and the intro gets one sentence about the rulings. The candidate 7 verdicts are untouched.
(8) Tests.
   - test/guards/releasebundles_test.go pins include-hidden-files: true on release-dry-run's release-version-bundles upload. It goes red when I delete the line from ci.yml, and a negative-fixture test covers reshaped steps.
   - TestUATIntroNamesCandidate7 is renamed TestUATIntroNamesItsNewestCandidate and now follows the newest candidate the intro names. It is not weaker: candidate 7's d20309c0 is still pinned, and a new check requires a Result block Snapshot naming the same commit. A simulated candidate 8 intro without a matching Snapshot fails.
(9) Nits.
   - install.md: added the held-open staging case (confirmed in spawn_stage.go:154-159).
   - release.md: split the combined D59 row so the evolution limit gets its own cannot-do link.
   - Release notes: added plugins[].source to where the entry name appears.
   - troubleshooting §5: the keys are now lowercase tokens/budget (confirmed in drops.go and the state file), and the evolution clause is fixed (newest 7 of 13 carried, per the UAT-04 notes).
   - release.md §7: added the ubuntu-latest move to Ubuntu 26 on 2026-10-19 (annotation confirmed on run 36981590450). It says either tag before then, or first get a green run on the new image or pin ubuntu-24.04.

Some of these docs describe wave 19b/19c behaviour that is not in 738d67c7 (the loss notice, the D61 screen, the command connect budget). They are only correct once those waves merge as ruled.

Checks run: go vet on three OSes, golangci-lint, fmt-check, the full test/docs and test/guards packages, the gen-*-docs checks, docmarkers and runpatterns lint, and devtool check-commit-msg on all three commits. All pass.

### Commits

- 45fbf9fd docs(release): name candidate 8 and the evidence it still owes
- 65c2b617 test(docs): follow the newest candidate the uat intro names
- 822f8d4d test(guards): pin hidden files on the release-version upload

### Findings resolution

- **fixed**: 0 (ci, major) release notes v0.3.0.md:66-70 byte-carry and evidence-carry claim false for candidate 8; CHANGELOG/release.md 'cut from release candidate 7'
  - Holds: diff d20309c0..738d67c7 touches drain.go, registry.go, checkpoint, cli/config.go and contract, and 19b/19c add more. The skeptic's 'do it after freeze' point is answered by D58(e)/D60(f): candidate 8 is defined, and the pages name c8-CANDIDATE.md 'when it is frozen' and claim nothing about its results. The paragraph is rewritten so candidate 8's own night chain, hosted CI, live re-check and C5.5 supply the evidence, and they are listed as owed. Stale-claim rows in closeout_c8_claims_test.go stop the old sentences returning (red on base).
- **fixed**: 1 (docs, major) same carry paragraph; CHANGELOG:139-141, README:91-97
  - The release notes paragraph is rewritten as in 0. The README 'What is verified where' paragraph is rewritten for candidate 8, and the CHANGELOG Known-limits carry sentence now says the figures are candidate 6's and candidate 8's evidence is owed. The c6/c7 figures stay, labelled.
- **fixed**: 2 (docs, major) README:97-98 live lane/evaluation sentence
  - Now says candidate 7's lane ran (20 sessions, D59), its rows carry by the diff, its host-seen timings stay candidate 7's (D58(e)), and the live re-check and C5.5 on candidate 8 are owed. Stale-claim row added (red on base).
- **fixed**: 3 (docs, major) release.md:12-16, :341, README:102 owed list and 'not run on candidate 7'
  - Checked with gh: run 36981590450 on d20309c0 had every job green except test (windows-latest); nightly 36981711009 passed 31/31. Both runs, the D58(b) disposition and the hosted bundle byte match (phase3/c7/hosted-release-bundles.txt) are recorded in the release.md status paragraph, release.md §7 and the README row. The owed list is restated for candidate 8. Stale-claim rows added.
- **fixed**: 4 (docs, major) README:22-23, CHANGELOG:12-13, release.md:7-8 and 183-184 name candidate 7, c7-CANDIDATE.md, D1 to D57
  - All three name candidate 8 and plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md, and the capability table cites D1 to D61 and the candidate 8 tree. TestReleasePagesNameOneCandidateRecord (one record across the three pages, floor 8) and TestCapabilityTableDecisionRangeCoversItsRows (the range covers every Dn the table cites; on the base the table cited D59 under 'D1 to D57') were both red on the base.
- **fixed**: 5 (docs, major) CHANGELOG:120-121 absolute Security sentence
  - Replaced with D61(b)'s guarantee: pointers show no denied or out-of-project path (D50); file pointers and structured summaries are judged whole and point by hash; free text is screened (rule literal, withheld names, out-of-project absolute paths; withheld whenever rules are unavailable); section 7 drop entries never show such a path. Limits named: aliases, globs and run-time names are not resolved, mention-only text is withheld, and sections 2-4 are outside D50 (D60(c)(i)). A new cannot-do §5 entry documents the limits. Stale-claim row added.
- **fixed**: 6 (ci, minor) README:102 and release.md:341 'not run on candidate 7'
  - Same edit as finding 3. Both places now record the candidate 7 runs and say candidate 8's are owed.
- **fixed**: 7 (docs, minor) CHANGELOG lacks candidate 8 fixes and the shipped-off features
  - Fixed gains: drain pass progress (D58(c)), fork current work, settingsVersion at warn (D59), stable status order (D59(c)), same-session-restart (D58(d)), the loss notice (D59(b)/D60(c)(ii)) and the command connect budget (D60(e)). Added gains a 'Shipped off by default' line. The config-violations write-cost line is left out because w20-config has not landed (see open_issues).
- **fixed**: 8 (docs, minor) backup/settingsVersion and the below-smallest-loss-notice limit missing from the release notes and capability table
  - Added to the release notes Known limits (the intro now names backup.md and troubleshooting §6 for the backup refusal), the capability table, CHANGELOG Known limits, and a new cannot-do §4 entry for the loss-notice floor. README and the release notes' 'names what did not fit' are qualified. TestReleasePagesListTheCandidate8Limits was red on the base.
- **fixed**: 9 (docs, minor) uat.md UAT-05/UAT-12 Result blocks leave the ruling open
  - Each block gets a 'Ruling (...)' line: D59(b) plus D60(c)(ii) for UAT-05, and D60(c)(i) plus D61(b) for UAT-12. Section 2 is described as outside D50 only by D60(c)(i)'s enumeration, as the skeptic noted. The candidate 7 verdicts are untouched, and one intro sentence was added. TestUATOpenQuestionsNameTheirRuling was red on the base.
- **fixed**: 10 (ci, minor) include-hidden-files unpinned on the release-version-bundles upload
  - The skeptics said the zips still carry the hidden files. That is true, but the c7 file-by-file comparison did list all 12 hidden files as missing before 79025ae3, so the line matters to the comparison as performed. New guard TestReleaseBundlesUploadKeepsHiddenFiles fails when the line is deleted from ci.yml (shown, then restored). TestReleaseBundlesUploadGuardRejectsReshapedSteps covers a missing or false setting, the setting on another step or job, and a missing upload.
- **fixed**: 11 (complete, minor) troubleshooting §5 F-C7-UAT04-1 wording and Tokens/Budget keys; release.md:229 links
  - Keys are now lowercase `tokens`/`budget` (drops.go json tags). The clause now says the entries that fit section 2's share are carried newest first (7 of 13) and the rest are not re-admitted. cannot-do's matching sentence is aligned. The D59 capability row is split, and the evolution limit links its own cannot-do anchor. Stale-claim rows added.
- **fixed**: nit 0: TestUATIntroNamesCandidate7 hard-codes d20309c0
  - Replaced by TestUATIntroNamesItsNewestCandidate, which follows the newest candidate the intro names. Candidate 7's commit stays pinned, and a Snapshot cross-check was added (criterion change recorded).
- **fixed**: nit 1: ubuntu-latest moves to Ubuntu 26 on Oct 19
  - Confirmed on run 36981590450's annotations. docs/release.md §7 now states the risk and the options: tag before 2026-10-19, or get a green run on the new image / pin ubuntu-24.04, and record a disposition for any red that appears only on the new image. The workflows themselves are not mine to edit.
- **fixed**: nit 2: install.md held-open staging case
  - Confirmed in spawn_stage.go:154-159 (returns before copyStaged/pruneStaged). install.md §6 now states the case.
- **fixed**: nit 3: release.md:230 combined D59 row lacks the evolution cannot-do link
  - The row is split in two, and each half has its own links.
- **fixed**: nit 4: release notes omit plugins[].source
  - Added, matching install.md:376 and UAT-01. Stale-claim row added.

### Tests

- `go test -p 1 -count=1 -run '^(TestCloseoutC8StaleClaimsAreGone|TestReleasePagesNameOneCandidateRecord|TestCapabilityTableDecisionRangeCoversItsRows|TestReleasePagesListTheCandidate8Limits|TestUATOpenQuestionsNameTheirRuling)$' ./test/docs (on 738d67c7 docs, before edits)`: FAIL as intended: 13 stale claims, 3 candidate-7 records, capability table cites D59 under D1 to D57, 4 missing limits, 2 missing ruling lines
- `go test -p 1 -count=1 -run '^TestReleaseBundlesUploadKeepsHiddenFiles$' ./test/guards (with include-hidden-files deleted from ci.yml, then restored)`: FAIL as intended; passes with the line restored
- `go test -p 1 -count=1 -run '^TestUATIntroNamesItsNewestCandidate$' ./test/docs (simulated candidate 8 intro, no matching Snapshot; then with a matching Snapshot; then restored)`: FAIL then ok, as intended
- `go test -p 1 -count=1 ./test/docs ./test/guards`: ok (docs 5.0s, guards 106.9s); test/docs re-run in full after the final doc edits: ok
- `go test -p 1 -count=20 -run '^(TestCloseoutC8StaleClaimsAreGone|TestReleasePagesNameOneCandidateRecord|TestCapabilityTableDecisionRangeCoversItsRows|TestReleasePagesListTheCandidate8Limits|TestUATOpenQuestionsNameTheirRuling|TestUATIntroNamesItsNewestCandidate)$' ./test/docs`: ok (go test -list confirms all 6 names match)
- `go test -p 1 -count=20 -run '^(TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps)$' ./test/guards`: ok (go test -list confirms both names match)
- `go test -p 1 -race -count=3 -run '^(TestCloseoutC8StaleClaimsAreGone|TestReleasePagesNameOneCandidateRecord|TestCapabilityTableDecisionRangeCoversItsRows|TestReleasePagesListTheCandidate8Limits|TestUATOpenQuestionsNameTheirRuling|TestUATIntroNamesItsNewestCandidate)$' ./test/docs`: ok
- `go test -p 1 -race -count=3 -run '^(TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps)$' ./test/guards`: ok
- `go test -p 1 -count=1 -run '^TestReleaseNotesFilesAreNamedForTheirTagAndFilled$' ./test/guards`: ok
- `GOOS={windows,linux,darwin} go vet ./test/docs ./test/guards`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./test/docs/... ./test/guards/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 each
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool check-commit-msg <each of the 3 commit messages>`: exit 0 each; no subject over 64 chars, no body line over 100

### Criterion changes

- TestUATIntroNamesCandidate7 is renamed TestUATIntroNamesItsNewestCandidate and generalized. Before, it required the literal `d20309c0` plus D59 in the uat.md intro. Now it requires the newest 'candidate N (commit `xxxxxxxx`)' named, with N >= 7, a known candidate's commit to match (7 is still d20309c0), 'report candidate N', a decision >= D59, and a Result block Snapshot naming that commit. Reason: the candidate 8 live re-check would otherwise force a same-commit test edit or turn every job red. The check is stricter on today's page because the Snapshot cross-check is new.
- New docs criteria (closeout_c8_claims_test.go): 13 retired sentences must stay absent; README, CHANGELOG and release.md must name one candidate record, no older than candidate 8; the capability table's 'D1 to Dn' must cover every Dn it cites; the release notes and release.md must list the settingsVersion backup refusal and 'below the smallest loss notice, nothing is injected'; and the UAT-05/UAT-12 blocks must carry 'Ruling (D59(b)' / 'Ruling (D60(c)(i)' lines while their open questions remain. Reason: findings 0-9 and 11.
- New guard criterion: ci.yml release-dry-run's release-version-bundles upload must set include-hidden-files: true. Reason: finding 10; the D58(a) byte comparison needs .mcp.json and .claude-plugin/ in the artifact.

### Open issues

- Several of these docs describe wave 19b/19c behaviour that is not in 738d67c7: the D59(b) loss notice and its floor, D61(b)'s summary screen and limits, and D60(e)'s command connect budget. Those sentences are true only once 19b/19c merge as ruled. If 19c's rendering differs, the CHANGELOG Security entry, cannot-do §5's new entry, the release notes limits and the capability rows must be re-checked against it and against 19c's docs/security.md. One example is whether a file pointer's home- or variable-rooted path is still withheld, as at HEAD (pathgate.go homeOrVarRoot).
- plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md is cited by path and does not exist until c8-night.sh freezes candidate 8. The README table, the release notes table and the CHANGELOG Known-limits figures are candidate 6/7 results, labelled as such. Once candidate 8's night, hosted CI, live re-check and C5.5 are recorded, a docs-only descendant (allowed by D58(e)) must replace them before the tag, because release.yml publishes the notes verbatim.
- CHANGELOG has no entry for the config-violations write cost: closeout/w20-config has not landed. If it lands, add one Fixed/Diagnostics clause.
- plans/sdd/V6-closeout/w19-docs/report.md:307 quotes the old test name TestUATIntroNamesCandidate7 inside an alternation. runpatterns still passes because the other alternatives match; the report is history and was left alone.
- The Ubuntu 26 migration (2026-10-19) affects ci.yml and release.yml, which I do not own. I documented the risk only; no runs-on line is pinned.

### Needs owner

- New constant releaseCandidateFloor = 8 (test/docs/closeout_c8_claims_test.go). Derivation: D58(e), the release tags candidate 8 or a descendant whose changes reach no bundle, so an identity page naming an older candidate's record describes binaries the release does not ship.
- New constant uatIntroCandidateFloor = 7 (test/docs/closeout_c7_claims_test.go). Derivation: candidate 7's re-run (D59) is the newest on docs/uat.md, so the intro may name a newer candidate but never fall back.
- New constant uatIntroRulingFloor = 59. Derivation: D59 ruled candidate 7's re-run outcome. New table uatCandidateCommits {3: d5598eb4, 4: 9f6a2fad, 7: d20309c0}, taken from the commits docs/uat.md already records. Add candidate 8's commit when the re-check updates the page.
- Decide whether v0.3.0 is tagged before 2026-10-19, or whether ci.yml release-dry-run and release.yml's release job are first pinned to ubuntu-24.04 or proven green on Ubuntu 26 (docs/release.md §7).

## review:docs:0:r1: verdict `needs-fixes`, 5 finding(s)

- **minor** `CHANGELOG.md:139-141; docs/cannot-do.md:748-750`: Both pages say a structured tool-argument summary 'points by hash', just as a file pointer does. Neither D61(b) nor the code supports that. D61(b)(1) rules only how a structured summary is judged ('judged whole, exactly as a file pointer's path is'), not how it is rendered. A tool pointer points by tool_use_id. In the only summary-gate code that exists (closeout/w19-rehydrate, round 2), a withheld summary is replaced by a 'summary withheld: ...' note, not by a hash. CHANGELOG.md is release text, so this claims more than the evidence shows.
  - Evidence: CHANGELOG.md:139-141: 'A file pointer, and a structured tool-argument summary (the store's preview of a path argument), is judged whole, as `re_read` judges a path, and points by hash'. cannot-do.md:748-750: 'A file pointer and a structured summary ... are judged whole ... and point by hash.' git show closeout/w19-rehydrate:internal/rehydrate/pathgate.go:892-894 has withheldPathLabel = "file (path withheld: ...)" for file pointers and withheldSummary = "summary withheld: it names a path ..." for summaries. D61(b)(1) contains no rendering rule.
  - Fix: Keep 'points by hash' for file pointers only. Example: 'A file pointer is judged whole, as re_read judges a path, and points by hash; a structured tool-argument summary is judged the same way and, when refused, is replaced by a "summary withheld" note.' Re-check the wording against 19c's rendering when it lands.
- **minor** `docs/release-notes/v0.3.0.md:66-77; docs/release.md §1 (procedure); CHANGELOG.md [Unreleased]`: The interim text is now correct for the frozen candidate 8 tree, but nothing ensures it is replaced before the tag. The release notes, which release.yml publishes verbatim, now say: 'The figures in the table below are candidates 6's and 7's and stand until candidate 8's are recorded; the release is not published before they are.' The table rows themselves stay in the present tense ('The whole tree passes under -race', 'X11 passed 3 of 3'). The seat's open_issues says a docs-only descendant must replace them before the tag, but release.md §1 has no such step and no guard checks for it. If the tag is cut as-is, the published draft carries a self-referential 'not published before they are' sentence and candidate 6/7 figures. Separately, the candidate 8 composition and the CHANGELOG Fixed list describe wave 19b/19c behaviour that is not in 738d67c7, and 19c is not written yet: D61(b) replaces round 2. They also omit the wave 20 product branches, which c8-night.sh requires to be merged (closeout/w20-*: redeliver, status/fsck, drain, sessionend, config). So the pages are only true once those land as ruled.
  - Evidence: release-notes v0.3.0.md:75-77 as quoted. release.md §1 steps 1-7 contain no 'replace the candidate's evidence in the release notes/README table' step. c8-night.sh step 2 requires every refs/heads/closeout/w20-* merged. closeout/w20-sessionend already has 3 commits, and the wave20 audit files list major product findings for redeliver, status (fsck checkFiles) and drain. The seat's own open_issues 1-3 say the same.
  - Fix: Add a step to release.md §1 before step 4: 'Rewrite docs/release-notes/<tag>.md's verified-where paragraph and table, README's verified-where table and CHANGELOG Known limits from the tagged candidate's recorded evidence (cN-CANDIDATE.md, its night, hosted runs, live re-check, C5.5); a docs-only descendant is allowed (D58(e)).' Optionally add a stale-claim row that fails once core.Version's tag exists if the notes still contain 'stand until candidate 8's are recorded'. Record for the coordinator that w20-docs merges after 19c and the other w20 branches, followed by a docs re-check of the CHANGELOG Fixed entries and the candidate 8 composition sentence (README:93-96, release.md:16-18).
- **nit** `README.md:104-106`: 'Candidate 7's live lane ran: 20 real sessions on its frozen bundles (D59)' overstates. One of the 20 sessions ran candidate 5's bundle: UAT-12's upgrade leg used it as the previous build. The release notes say '19 sessions on that candidate's bundle'. The next sentence, 'Its rows carry to candidate 8 by the diff, confirmed by the live re-check', reads as if that re-check had already happened, but the same paragraph lists it as owed.
  - Evidence: docs/release-notes/v0.3.0.md:100-101: 'over the 417 measured pairs of the 19 sessions on that candidate's bundle'. uat.md UAT-12 Result: 'previous build candidate 5's frozen bundle (0d06ab12) -> the frozen candidate 7 bundle'.
  - Fix: Write: '20 real sessions, 19 of them on its frozen bundles (D59). Its rows carry to candidate 8 by the diff, which the owed live re-check is to confirm (D58(e)).'
- **nit** `test/docs/closeout_c7_claims_test.go (TestUATIntroNamesItsNewestCandidate, final check)`: The doc comment and criterion_changes say a Result block's Snapshot must name the intro's commit. The check is actually strings.Contains(body[end:], "commit "+commit), so the commit matches anywhere after the intro: history lines, notes, prose. It is looser than stated. It is still adequate today, because Snapshot lines do carry the commit after normalization.
  - Evidence: `if !strings.Contains(body[end:], "commit "+commit)`. The Snapshot at uat.md:260-262 wraps 'commit\n d20309c0...' and matches only after normalized().
  - Fix: Either restrict the match to Snapshot text (from 'Snapshot:' to the next 'Date:') or reword the comment and criterion to 'names that commit after the introduction'.
- **nit** `docs/release-notes/v0.3.0.md:75; CHANGELOG.md:170; docs/release.md:26`: The release notes, published verbatim, say 'candidates 6's and 7's', which is ungrammatical. Two added lines are left unwrapped against the files' ~100-column wrap: CHANGELOG.md:170 is 121 chars and release.md:26 is 161 chars.
  - Evidence: awk length check on the added lines. release-notes line 75: 'The figures in the table below are candidates 6's and 7's and stand'.
  - Fix: Write 'candidate 6's and candidate 7's'. Re-wrap CHANGELOG.md:170 and release.md:26 to about 100 columns.

## fix:docs:r1: status `done`, head `7d1f678e`

### Summary

Docs and tests only; no product code changed. Branch closeout/w20-docs in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-docs, five commits on top of 738d67c7 (three from round 1, two from this round).

Round 1 (carried forward): every audit finding and nit in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave20/docs.json held and is fixed. Release identity now names candidate 8 and plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md "when it is frozen", and the capability table cites D1 to D61. The candidate 6 byte-carry paragraph is gone. Candidate 7's hosted runs 36981590450 and 36981711009 are recorded, and candidate 8's are listed as owed. CHANGELOG Fixed/Added gained the candidate 8 entries, and Security was qualified to D61(b). The new known limits (backup refused under a newer settingsVersion; nothing injected below the smallest loss notice) are in the release notes, the capability table, CHANGELOG and cannot-do. UAT-05 and UAT-12 have ruling lines. Hidden files are pinned on the release-version-bundles upload. The UAT intro test follows the newest candidate. Docs nits fixed: install held-open, release.md links, plugins[].source, troubleshooting §5, and the Ubuntu 26 risk.

## Review resolution (round 1 review)
- **Minor 1, "points by hash" for structured summaries: holds, fixed.** D61(b)(1) says a structured summary is "judged whole, exactly as a file pointer's path is" and has no rendering rule. Both at HEAD (internal/rehydrate/items.go:1145, pathgate.go:127) and on closeout/w19-rehydrate (pathgate.go:894), a refused summary is replaced by withheldSummary ("summary withheld: ..."), not by a hash. CHANGELOG.md's Security entry and docs/cannot-do.md §5 now say: a file pointer is judged whole and points by hash; a structured summary is judged the same way and, when refused, is replaced by a "summary withheld" note. Stale-claim rows (red first) keep the old sentences out.
- **Minor 2, nothing ensures the interim text is replaced before the tag: holds, fixed.**
  - docs/release.md §1 step 2 now requires the replacement. In the same docs-only descendant commit (D58(e)), the release notes' verified-where paragraph and table, README's paragraph and table, CHANGELOG Known limits and release.md's status are rewritten from the tagged candidate's recorded evidence. I extended step 2 rather than inserting a step, so the "step 3/step 5" references elsewhere still point at the right steps.
  - New guard TestReleaseNotesForAPushedTagCarryNoInterimSentence (test/guards/releasenotes_test.go). It runs inside release.yml's release-check guards step. When GITHUB_REF_TYPE=tag, it fails if docs/release-notes/$GITHUB_REF_NAME.md still matches "until candidate N's are recorded". Branch pushes and the night chain's local release-check --tag rehearsal (no GITHUB_REF_TYPE) are not affected, because there the sentence is true of the tree.
  - I showed it red with GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.3.0 against today's notes. A negative-fixture test, TestReleaseNotesInterimGuardFiresOnlyOnATagPush, was red against a stub before the implementation.
  - The candidate 8 composition sentences (README, release.md) now say "among them ...", because every closeout/w20-* branch is also merged (c8-night.sh line 61).
  - The merge-order request is recorded in open_issues for the coordinator.
- **Nit, README "20 real sessions on its frozen bundles" / "confirmed by the live re-check": holds, fixed.** UAT-12's upgrade leg ran candidate 5's bundle (uat.md UAT-12 Snapshot, 0d06ab12), and the release notes say 19 sessions. README now reads "20 real sessions, 19 of them on its frozen bundles (D59). Its rows carry to candidate 8 by the diff, which the owed live re-check is to confirm". Stale-claim rows added.
- **Nit, the Snapshot cross-check was looser than stated: holds, fixed.** The check now goes through a new uatSnapshotNamesCommit, which looks only at text from "Snapshot:" to the next "Date:". New negative test TestUATSnapshotCommitCheckReadsOnlySnapshots covers three cases: commit named in a history line, in a note after the Snapshot, and with no Snapshot. All three failed against the old strings.Contains check and pass now. The live page still passes, because both UAT-01's and UAT-12's Snapshots name d20309c0.
- **Nit, "candidates 6's and 7's" and two unwrapped lines: holds, fixed.** The release notes now read "candidate 6's and candidate 7's". CHANGELOG's Security and Verified-where bullets and release.md's status paragraph are re-wrapped to 100 columns, so the old 121- and 161-character lines are gone. The CHANGELOG lines that remain at 101-102 columns predate this branch.

Checks this round:
- vet passes on windows, linux and darwin.
- golangci-lint exit 0.
- fmt-check exit 0.
- Full test/docs: ok. Full test/guards: ok.
- New and changed rows pass -count=20 and -race -count=3.
- gen-config-docs, gen-command-docs and gen-mcp-docs --check: exit 0 each.
- docmarkers and runpatterns lint: PASS.
- check-commit-msg on both new commits: exit 0.

### Commits

- 45fbf9fd docs(release): name candidate 8 and the evidence it still owes
- 65c2b617 test(docs): follow the newest candidate the uat intro names
- 822f8d4d test(guards): pin hidden files on the release-version upload
- 5ef4f45c docs(release): point only file pointers by hash; fix c7 counts
- 7d1f678e test(guards): fail a tag push whose notes keep interim figures

### Findings resolution

- **fixed**: 0 (ci, major) release notes v0.3.0.md:66-70 byte-carry/evidence-carry claim false for candidate 8; 'cut from release candidate 7'
  - Round 1: paragraph rewritten so candidate 8's own night chain, hosted CI, live re-check and C5.5 supply the evidence, listed as owed; stale-claim rows in test/docs/closeout_c8_claims_test.go (red on 738d67c7).
- **fixed**: 1 (docs, major) same carry paragraph; CHANGELOG:139-141, README:91-97
  - Round 1: README verified-where paragraph and CHANGELOG Known-limits rewritten for candidate 8; c6/c7 figures labelled as theirs. Round 2: composition list says 'among them', since the w20 product branches also merge.
- **fixed**: 2 (docs, major) README:97-98 live lane/evaluation sentence
  - Round 1 rewrite; round 2 corrected to '20 real sessions, 19 of them on its frozen bundles' and 'which the owed live re-check is to confirm'.
- **fixed**: 3 (docs, major) release.md:12-16, :341, README:102 owed list and 'not run on candidate 7'
  - Round 1: candidate 7's ci.yml 36981590450 (green except test windows-latest, D58(b)) and nightly 36981711009 (31/31) recorded after checking with gh; candidate 8's runs listed as owed.
- **fixed**: 4 (docs, major) README/CHANGELOG/release.md name candidate 7, c7-CANDIDATE.md, D1 to D57
  - Round 1: all name candidate 8 and plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md; capability table cites D1 to D61; TestReleasePagesNameOneCandidateRecord and TestCapabilityTableDecisionRangeCoversItsRows red on base.
- **fixed**: 5 (docs, major) CHANGELOG:120-121 absolute Security sentence
  - Round 1: replaced with D61(b)'s guarantee and limits. Round 2: 'points by hash' restricted to file pointers (see review finding below).
- **fixed**: 6 (ci, minor) README:102 and release.md:341 'not run on candidate 7'
  - Round 1, same edit as finding 3.
- **fixed**: 7 (docs, minor) CHANGELOG lacks candidate 8 fixes and shipped-off features
  - Round 1: Fixed and Added entries added; config-violations write cost left out until w20-config lands (open issue).
- **fixed**: 8 (docs, minor) backup/settingsVersion and below-smallest-loss-notice limits missing
  - Round 1: added to release notes Known limits, capability table, CHANGELOG and cannot-do; TestReleasePagesListTheCandidate8Limits red on base.
- **fixed**: 9 (docs, minor) uat.md UAT-05/UAT-12 Result blocks leave the ruling open
  - Round 1: 'Ruling (...)' lines added; candidate 7 verdicts untouched; TestUATOpenQuestionsNameTheirRuling red on base.
- **fixed**: 10 (ci, minor) include-hidden-files unpinned on release-version-bundles upload
  - Round 1: TestReleaseBundlesUploadKeepsHiddenFiles plus negative fixture TestReleaseBundlesUploadGuardRejectsReshapedSteps.
- **fixed**: 11 (complete, minor) troubleshooting §5 wording and Tokens/Budget keys; release.md:229 links
  - Round 1: lowercase tokens/budget, the evolution clause corrected (7 of 13 carried), the D59 capability row split with its own cannot-do link.
- **fixed**: nit 0: TestUATIntroNamesCandidate7 hard-codes d20309c0
  - Round 1: renamed TestUATIntroNamesItsNewestCandidate. Round 2: its Snapshot cross-check now reads only Snapshot text.
- **fixed**: nit 1: ubuntu-latest moves to Ubuntu 26 on Oct 19
  - Round 1: risk and options documented in docs/release.md §7; workflows not owned.
- **fixed**: nit 2: install.md held-open staging case
  - Round 1, confirmed in spawn_stage.go:154-159.
- **fixed**: nit 3: release.md combined D59 row lacks evolution cannot-do link
  - Round 1: row split.
- **fixed**: nit 4: release notes omit plugins[].source
  - Round 1, stale-claim row added.
- **fixed**: Review minor: CHANGELOG.md:139-141 and cannot-do.md:748-750 say a structured summary 'points by hash'
  - Holds: D61(b)(1) has no rendering rule. At HEAD (items.go:1145, pathgate.go:127) and on closeout/w19-rehydrate (pathgate.go:894), a refused summary becomes withheldSummary. Both pages now keep 'points by hash' for file pointers only and say a refused structured summary is replaced by a "summary withheld" note. Two stale-claim rows were red before the edit and pass now. Commit 5ef4f45c.
- **fixed**: Review minor: nothing ensures the interim evidence text is replaced before the tag; composition omits w20 product branches
  - Holds. Three changes. (1) release.md §1 step 2 now requires rewriting the notes' verified-where paragraph and table, README's paragraph and table, CHANGELOG Known limits and release.md's status from the tagged candidate's evidence, in a docs-only descendant (D58(e)). (2) New guard TestReleaseNotesForAPushedTagCarryNoInterimSentence: when GITHUB_REF_TYPE=tag, release-check's guards step fails if the tag's notes still match "until candidate N's are recorded". It was red with GITHUB_REF_TYPE=tag against today's notes. Negative fixture TestReleaseNotesInterimGuardFiresOnlyOnATagPush was red against a stub first. (3) The composition sentences say 'among them'. The merge order is carried to open_issues for the coordinator. Commits 5ef4f45c and 7d1f678e.
- **fixed**: Review nit: README '20 real sessions on its frozen bundles' and 'confirmed by the live re-check'
  - Holds: UAT-12's previous build was candidate 5's bundle (0d06ab12), and the release notes say 19 sessions. README now says '20 real sessions, 19 of them on its frozen bundles (D59). Its rows carry to candidate 8 by the diff, which the owed live re-check is to confirm'. Two stale-claim rows added, red first.
- **fixed**: Review nit: TestUATIntroNamesItsNewestCandidate Snapshot check looser than stated
  - Holds. A new helper, uatSnapshotNamesCommit, matches only from 'Snapshot:' to the next 'Date:'. Negative test TestUATSnapshotCommitCheckReadsOnlySnapshots covers a history line, a note after the Snapshot, and no Snapshot. It failed against the old strings.Contains check and passes now. The live uat.md still passes.
- **fixed**: Review nit: 'candidates 6's and 7's'; CHANGELOG.md:170 and release.md:26 unwrapped
  - Changed to 'candidate 6's and candidate 7's' (stale-claim row added). The CHANGELOG Security and Verified-where bullets and the release.md status paragraph are re-wrapped to 100 columns. The remaining 101-102-column CHANGELOG lines predate this branch.

### Tests

- `go test -p 1 -count=1 -run '^(TestCloseoutC8StaleClaimsAreGone|TestUATSnapshotCommitCheckReadsOnlySnapshots)$' ./test/docs (on 822f8d4d docs, before this round's edits)`: FAIL as intended: 5 stale claims (two hash sentences, 'candidates 6's and 7's', two README sentences); 3 fixture cases accepted by the loose check
- `go test -p 1 -count=1 -run '^TestReleaseNotesInterimGuardFiresOnlyOnATagPush$' ./test/guards (stub helper)`: FAIL as intended; ok after implementation
- `GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.3.0 go test -p 1 -count=1 -run '^TestReleaseNotesForAPushedTagCarryNoInterimSentence$' ./test/guards`: FAIL as intended (today's notes carry the interim sentence); ok without the tag env
- `go test -p 1 -count=1 ./test/docs`: ok (7.0s)
- `go test -p 1 -count=1 ./test/guards`: ok (78.4s)
- `go test -p 1 -count=20 -run '^(TestCloseoutC8StaleClaimsAreGone|TestUATSnapshotCommitCheckReadsOnlySnapshots|TestUATIntroNamesItsNewestCandidate)$' ./test/docs`: ok (go test -list confirms all 3 names)
- `go test -p 1 -count=20 -run '^(TestReleaseNotesForAPushedTagCarryNoInterimSentence|TestReleaseNotesInterimGuardFiresOnlyOnATagPush)$' ./test/guards`: ok (go test -list confirms both names)
- `go test -p 1 -race -count=3 -run '^(TestCloseoutC8StaleClaimsAreGone|TestUATSnapshotCommitCheckReadsOnlySnapshots|TestUATIntroNamesItsNewestCandidate)$' ./test/docs`: ok
- `go test -p 1 -race -count=3 -run '^(TestReleaseNotesForAPushedTagCarryNoInterimSentence|TestReleaseNotesInterimGuardFiresOnlyOnATagPush)$' ./test/guards`: ok
- `GOOS={windows,linux,darwin} go vet ./test/docs ./test/guards`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./test/docs/... ./test/guards/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 each
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS docmarkers, PASS runpatterns
- `go run ./tools/devtool check-commit-msg <each new commit message>`: exit 0 each
- `Round 1 rows (carried): go test -p 1 -count=20 / -race -count=3 -run '^(TestCloseoutC8StaleClaimsAreGone|TestReleasePagesNameOneCandidateRecord|TestCapabilityTableDecisionRangeCoversItsRows|TestReleasePagesListTheCandidate8Limits|TestUATOpenQuestionsNameTheirRuling|TestUATIntroNamesItsNewestCandidate)$' ./test/docs and '^(TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps)$' ./test/guards`: ok (round 1)

### Criterion changes

- Round 1: TestUATIntroNamesCandidate7 was renamed TestUATIntroNamesItsNewestCandidate and generalized to the newest candidate the intro names (floor 7, known commits pinned, decision >= D59, plus a Snapshot cross-check). Reason: the candidate 8 re-check would otherwise force a same-commit test edit.
- Round 2: the Snapshot cross-check now reads only Snapshot text ('Snapshot:' to the next 'Date:'), not anything after the intro, and has a negative fixture. Reason: review nit; the check had been looser than its comment stated. This makes it stricter.
- Round 1: new docs criteria in closeout_c8_claims_test.go: retired sentences stay absent; one candidate record across README/CHANGELOG/release.md, floor 8; the capability table's decision range covers its rows; the candidate 8 limits are listed; UAT-05/UAT-12 carry ruling lines. Reason: audit findings 0-9 and 11.
- Round 2: five more stale-claim rows: the two 'points by hash' sentences for structured summaries, 'candidates 6's and 7's', and two README sentences (the 20 sessions on the frozen bundles; 'confirmed by the live re-check'). Reason: this round's review findings.
- Round 1: new guard, ci.yml release-dry-run's release-version-bundles upload must set include-hidden-files: true. Reason: audit finding 10.
- Round 2: new guard TestReleaseNotesForAPushedTagCarryNoInterimSentence. On a tag push (GITHUB_REF_TYPE=tag), the tag's release notes may not still say their figures stand until the candidate's own are recorded. It is inactive on branch pushes and the local rehearsal, where the sentence is true of the tree. Reason: review minor 2; release.yml publishes the notes verbatim.

### Open issues

- Merge order, for the coordinator: merge w20-docs after closeout/w19-rehydrate (19c), closeout/w19b-cmdconnect and every other closeout/w20-* product branch. Then re-check the docs against what actually merged: the CHANGELOG Fixed entries, the candidate 8 composition sentences (README verified-where paragraph, release.md status), the CHANGELOG Security entry and cannot-do §5 against 19c's rendering and its docs/security.md (for example whether a file pointer's home- or variable-rooted path is still withheld, and whether the "summary withheld" note survives 19c), and the release notes limits and capability rows.
- plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md is cited by path and does not exist until the freeze. The README table, the release notes table and the CHANGELOG Known-limits figures are candidate 6/7 results, labelled as theirs. Per release.md §1 step 2, a docs-only descendant must replace them from candidate 8's evidence before the tag. The new guard fails the tag push's release-check until the notes' interim sentence is gone.
- CHANGELOG has no entry for the config-violations write cost: closeout/w20-config has not landed. If it lands, add one Fixed/Diagnostics clause.
- plans/sdd/V6-closeout/w19-docs/report.md:307 quotes the old test name TestUATIntroNamesCandidate7 inside an alternation. runpatterns still passes because the other alternatives match; it is a history report, so I left it alone.
- Ubuntu 26 migration (2026-10-19) affects ci.yml and release.yml, which I do not own. I documented the risk in docs/release.md §7 but pinned nothing.

### Needs owner

- New constant releaseCandidateFloor = 8 (test/docs/closeout_c8_claims_test.go). Derivation: D58(e), the release tags candidate 8 or a descendant whose changes reach no bundle.
- New constant uatIntroCandidateFloor = 7 (test/docs/closeout_c7_claims_test.go). Derivation: candidate 7's re-run (D59) is the newest on docs/uat.md.
- New constant uatIntroRulingFloor = 59 and table uatCandidateCommits {3: d5598eb4, 4: 9f6a2fad, 7: d20309c0}, taken from commits docs/uat.md records. Add candidate 8's commit when the re-check updates the page.
- New pattern releaseNotesInterimRE `until candidate [0-9]+'s are recorded` (test/guards/releasenotes_test.go). It is enforced only when GITHUB_REF_TYPE=tag. Derivation: release.yml publishes docs/release-notes/<tag>.md verbatim, so a pushed tag's notes must no longer carry the interim sentence (docs/release.md §1 step 2).
- Decide whether v0.3.0 is tagged before 2026-10-19, or whether ci.yml release-dry-run and release.yml's release job are first pinned to ubuntu-24.04 or proven green on Ubuntu 26 (docs/release.md §7).

## review:docs:0:r2: verdict `needs-fixes`, 3 finding(s)

- **minor** `docs/release.md:229 (capability table row); docs/release-notes/v0.3.0.md:167-168 (lead clause of the new Known-limits bullet)`: The new rows open with an unqualified guarantee that D61(b) does not give. The capability row says the block's pointers "never show a path the host denies". The release notes, which are published verbatim, say the block "shows no path your saved Read rules deny". D61(b)(5) records that aliases (8.3 names, links) and globs typed in free text are not resolved, and that names built at run time cannot be seen. So a free-text summary can still show a denied file through its alias, for example `type SECRET~1\key.pem` under a `secrets/**` deny rule whose literal does not appear. The release-notes bullet then lists that limit, so it contradicts its own first clause. The capability row puts the limit only in a separate accepted-residual row (line 249). It also says "the host denies", although only the saved Read rules are consulted (D7; that limit is the row at line 251). Task item 5 asked for D61's actual guarantee and limits. CHANGELOG.md:138 already words the lead correctly ("do not show a path the host's saved Read rules deny", with the limits in the same entry). These two places do not.
  - Evidence: ledger (verify/v6 0f8ce75f) D61(b)(5): "Recorded limits: aliases (8.3 names and links) and globs typed in free text are not resolved; names built at run time cannot be seen". release.md:229: "The rehydration block's pointers never show a path the host denies or an absolute path outside the project: ...". release-notes:167-171: "The rehydration block shows no path your saved Read rules deny ... The screen does not resolve aliases (8.3 names, links) or globs typed in free text".
  - Fix: Capability row: "The rehydration block's pointers are judged against the host's saved Read rules and the project boundary (D50): file pointers and structured tool-argument summaries are judged whole, free-text summaries are screened for rule literals, withheld paths and out-of-project absolute paths, and section 7's drop entries are withheld or redacted; aliases, globs and run-time names in free text are a recorded limit (row below)". Release-notes lead: "In its pointers, the rehydration block withholds a path your saved Read rules deny and an absolute path outside the project: ..." and keep the limits sentence. Add stale-claim rows for the two retired phrasings.
- **nit** `CHANGELOG.md:121-123 (Fixed, Diagnostics)`: "`status`, `doctor` and the other commands that call the daemon have a connect budget of their own" includes `qompack mcp`, which also calls the daemon. D61(c) and the cmdconnect code exclude it: mcp keeps runtime.daemon.connectDeadlineMs and its 10-attempt retry loop.
  - Evidence: ledger D61(c): "`qompack mcp` keeps runtime.daemon.connectDeadlineMs and its 10-attempt retry loop". closeout/w19b-cmdconnect internal/cli/qompack_commands.go commandConnectDeadline comment: "`qompack mcp` does not use it". The same branch's troubleshooting text says "`status`, `doctor` and the other slash-command frontends (`recall`, `why`, `dropped`, ...)".
  - Fix: Reword to "`status`, `doctor` and the other slash-command frontends that call the daemon have a connect budget of their own (`qompack mcp` keeps the hooks' budget and its retry loop)". Re-check the sentence once w19b-cmdconnect merges.
- **nit** `test/guards/releasenotes_test.go:166 (releaseNotesInterimRE); docs/release.md:52-54`: The new tag-push guard pins one exact phrasing, `until candidate N's are recorded`. Any rewording of the interim sentence passes it, and so does a rewrite that removes the sentence but leaves the candidate 6/7 table rows. release.md step 2 says the guard fails "while they still say their figures stand until the candidate's own are recorded", which reads as broader than the check. README's, CHANGELOG's and release.md's interim text ("owed", "Neither workflow has run on candidate 8 yet") is not guarded at all. It is not a correctness bug: the guard does what its comment says, it was shown red under GITHUB_REF_TYPE=tag, and it is inert on branch pushes and the night chain's local --tag rehearsal.
  - Evidence: var releaseNotesInterimRE = regexp.MustCompile(`until candidate [0-9]+'s are recorded`). I reproduced it locally: GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.3.0 go test -run TestReleaseNotesForAPushedTagCarryNoInterimSentence ./test/guards FAILs, and it passes without the env.
  - Fix: Optional hardening: on a tag push, also fail if the notes still contain "candidate 6's and candidate 7's" or "Neither workflow has run on candidate". Or say in release.md step 2 that the guard catches only that one sentence, so the rewrite of README, CHANGELOG and release.md stays a manual step-2 duty.

## fix:docs:r2: status `done`, head `3b8fd645`

### Root cause

The release pages were written while candidate 7 was the release candidate. They kept its identity and evidence after candidate 8 changed product code, and they worded D61's pointer screen more absolutely than its ruling, which records limits. The tag-push guard enforced the step-2 rewrite for only one sentence on one page.

### Summary

Docs and tests only; no product code changed. Branch closeout/w20-docs in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-docs has seven commits on top of 738d67c7: three from round 1, two from round 2, two from this round (round 3).

Round 1 (carried forward): every audit finding and nit in C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave20/docs.json held and is fixed.
- Release identity now names candidate 8 and plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md "when it is frozen", and the capability table cites D1 to D61.
- The candidate 6 byte-carry paragraph is gone. Candidate 7's hosted runs 36981590450 and 36981711009 are recorded, and candidate 8's are listed as owed.
- CHANGELOG has the candidate 8 Fixed/Added entries, and its Security entry is qualified to D61(b).
- The new known limits (backup refused under a newer settingsVersion; nothing injected below the smallest loss notice) are in place.
- UAT-05 and UAT-12 have ruling lines, the release-version upload pins hidden files, and the UAT intro test follows the newest candidate.
- The docs nits are fixed.

Round 2 (carried forward):
- "Points by hash" now applies to file pointers only. A refused structured summary gets a "summary withheld" note.
- release.md §1 step 2 requires the interim evidence text to be rewritten in a docs-only descendant before the tag, and a tag-push guard enforces part of that.
- The README session counts are corrected.
- The Snapshot cross-check reads only Snapshot text.
- Wording fixes and re-wraps.

## Review resolution (round 3)
- **Minor: an unqualified guarantee in the capability row (release.md:229) and the release-notes lead (v0.3.0.md:167). Holds, fixed.**
  - Ledger D61(b)(5) records that aliases (8.3 names, links) and globs in free text are not resolved, and that names built at run time cannot be seen. D7 limits the check to the saved Read rules. So "never show a path the host denies" and "shows no path your saved Read rules deny" claim more than D61 gives, and the notes bullet contradicted its own limits sentence.
  - The capability row now reads: pointers "are judged against the host's saved Read rules and the project boundary". File pointers and structured summaries are judged whole. Free-text summaries are withheld when they contain a rule literal, a withheld path's name or an out-of-project absolute path, or when the rules cannot be read. Section 7 entries are withheld or redacted. "Aliases, globs and run-time names in free text, and rules outside the saved settings, are recorded limits (rows below)".
  - The notes lead now reads "In its pointers, the rehydration block withholds a path your saved Read rules deny and an absolute path outside the project: ...", and the limits sentence is kept.
  - Two stale-claim rows were red on 7d1f678e and pass now. Commit ee30e2fc.
- **Nit: the CHANGELOG connect-budget clause includes `qompack mcp`. Holds, fixed.**
  - D61(c) and closeout/w19b-cmdconnect internal/cli/qompack_commands.go:191 ("`qompack mcp` does not use it") show that mcp keeps its own connect deadline and retry loop.
  - CHANGELOG now names "the other slash-command frontends that call the daemon", followed by "(`qompack mcp` keeps the hooks' budget and its retry loop, D61(c))".
  - A stale-claim row was red first and passes now. Commit ee30e2fc. The sentence should be re-checked once w19b merges (open issue).
- **Nit: the tag-push guard pinned one phrasing and covered only the notes. Holds, hardened and documented.**
  - releaseInterimMarkers (test/guards/releasenotes_test.go) now lists, per page, every interim sentence the four step-2 pages carry today:
    - notes: "until candidate N's are recorded" and "the release is not published before they are"
    - README: the record "when it is frozen", "own evidence is still owed", "Neither workflow has run on candidate N yet" and "that record is owed"
    - CHANGELOG: "supply the release's evidence, and they are owed"
    - release.md: "when it is frozen", "Still owed before the tag, all on candidate N" and "Neither workflow has run on candidate N yet"
  - The new live row TestReleasePagesForAPushedTagCarryNoInterimText checks README, CHANGELOG and release.md on a tag push. It reports every leftover marker, not just the first.
  - The new negative fixture TestReleasePagesInterimGuardFiresOnlyOnATagPush was red against a nil stub before the implementation.
  - Under GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.3.0, all 10 markers fire against today's tree. Without the env (branch push, or the night chain's local release-check --tag rehearsal) both live rows pass.
  - release.md step 2 now says the guard "catches only those exact sentences: rewriting the rest, the candidate 6 and 7 rows of both tables among it, stays this step's manual duty". I did not ban "candidate 6's and candidate 7's" outright, because the coordinator may legitimately keep candidate 7's host-seen hook figures after the rewrite.
  - Commit 3b8fd645.

Checks this round:
- vet passes on windows, linux and darwin for test/docs and test/guards.
- golangci-lint exit 0. fmt-check exit 0.
- Full test/docs: ok. Full test/guards: ok (44s).
- gen-config-docs, gen-command-docs and gen-mcp-docs --check: exit 0 each.
- docmarkers and runpatterns lint: PASS.
- New and changed rows pass -count=20 and -race -count=3.
- check-commit-msg on both new messages: exit 0. No attribution trailers.

### Commits

- 45fbf9fd docs(release): name candidate 8 and the evidence it still owes
- 65c2b617 test(docs): follow the newest candidate the uat intro names
- 822f8d4d test(guards): pin hidden files on the release-version upload
- 5ef4f45c docs(release): point only file pointers by hash; fix c7 counts
- 7d1f678e test(guards): fail a tag push whose notes keep interim figures
- ee30e2fc docs(release): state the block's pointer screen with its limits
- 3b8fd645 test(guards): fail a tag push whose release pages keep interim text

### Findings resolution

- **fixed**: 0 (ci, major) release notes v0.3.0.md:66-70 byte-carry/evidence-carry claim false for candidate 8; 'cut from release candidate 7'
  - Round 1: paragraph rewritten so candidate 8's own night chain, hosted CI, live re-check and C5.5 supply the evidence, listed as owed; stale-claim rows in test/docs/closeout_c8_claims_test.go (red on 738d67c7).
- **fixed**: 1 (docs, major) same carry paragraph; CHANGELOG:139-141, README:91-97
  - Round 1: README verified-where paragraph and CHANGELOG Known-limits rewritten for candidate 8; c6/c7 figures labelled as theirs. Round 2: composition list says 'among them', since the w20 product branches also merge.
- **fixed**: 2 (docs, major) README:97-98 live lane/evaluation sentence
  - Round 1 rewrite. Round 2 corrected it to '20 real sessions, 19 of them on its frozen bundles' and 'which the owed live re-check is to confirm'.
- **fixed**: 3 (docs, major) release.md:12-16, :341, README:102 owed list and 'not run on candidate 7'
  - Round 1: candidate 7's ci.yml 36981590450 (green except test windows-latest, D58(b)) and nightly 36981711009 (31/31) recorded after checking with gh; candidate 8's runs listed as owed.
- **fixed**: 4 (docs, major) README/CHANGELOG/release.md name candidate 7, c7-CANDIDATE.md, D1 to D57
  - Round 1: all three pages name candidate 8 and plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md; the capability table cites D1 to D61. TestReleasePagesNameOneCandidateRecord and TestCapabilityTableDecisionRangeCoversItsRows were red on base.
- **fixed**: 5 (docs, major) CHANGELOG:120-121 absolute Security sentence
  - Round 1: replaced with D61(b)'s guarantee and limits. Round 2: 'points by hash' restricted to file pointers.
- **fixed**: 6 (ci, minor) README:102 and release.md:341 'not run on candidate 7'
  - Round 1, same edit as finding 3.
- **fixed**: 7 (docs, minor) CHANGELOG lacks candidate 8 fixes and shipped-off features
  - Round 1: Fixed and Added entries added. The config-violations write cost is left out until w20-config lands (open issue).
- **fixed**: 8 (docs, minor) backup/settingsVersion and below-smallest-loss-notice limits missing
  - Round 1: added to the release notes' Known limits, the capability table, CHANGELOG and cannot-do. TestReleasePagesListTheCandidate8Limits was red on base.
- **fixed**: 9 (docs, minor) uat.md UAT-05/UAT-12 Result blocks leave the ruling open
  - Round 1: 'Ruling (...)' lines added; candidate 7 verdicts untouched; TestUATOpenQuestionsNameTheirRuling red on base.
- **fixed**: 10 (ci, minor) include-hidden-files unpinned on release-version-bundles upload
  - Round 1: TestReleaseBundlesUploadKeepsHiddenFiles plus negative fixture TestReleaseBundlesUploadGuardRejectsReshapedSteps.
- **fixed**: 11 (complete, minor) troubleshooting §5 wording and Tokens/Budget keys; release.md:229 links
  - Round 1: keys are lowercase tokens/budget, the evolution clause is corrected (7 of 13 carried), and the D59 capability row is split with its own cannot-do link.
- **fixed**: nit 0: TestUATIntroNamesCandidate7 hard-codes d20309c0
  - Round 1: renamed TestUATIntroNamesItsNewestCandidate. Round 2: its Snapshot cross-check now reads only Snapshot text.
- **fixed**: nit 1: ubuntu-latest moves to Ubuntu 26 on Oct 19
  - Round 1: risk and options documented in docs/release.md §7; workflows not owned.
- **fixed**: nit 2: install.md held-open staging case
  - Round 1, confirmed in spawn_stage.go:154-159.
- **fixed**: nit 3: release.md combined D59 row lacks evolution cannot-do link
  - Round 1: row split.
- **fixed**: nit 4: release notes omit plugins[].source
  - Round 1, stale-claim row added.
- **fixed**: Round 2 review minor: CHANGELOG.md:139-141 and cannot-do.md:748-750 say a structured summary 'points by hash'
  - D61(b)(1) has no rendering rule. A refused summary becomes withheldSummary at HEAD (items.go:1145, pathgate.go:127) and on w19-rehydrate (pathgate.go:894). Both pages now keep 'points by hash' for file pointers only. Two stale-claim rows were red first. Commit 5ef4f45c.
- **fixed**: Round 2 review minor: nothing ensures the interim evidence text is replaced before the tag; composition omits w20 product branches
  - release.md §1 step 2 now requires the rewrite in a docs-only descendant (D58(e)). Tag-push guard TestReleaseNotesForAPushedTagCarryNoInterimSentence added, red under GITHUB_REF_TYPE=tag. Composition sentences say 'among them'. Commits 5ef4f45c, 7d1f678e.
- **fixed**: Round 2 review nit: README '20 real sessions on its frozen bundles' and 'confirmed by the live re-check'
  - README now says '20 real sessions, 19 of them on its frozen bundles (D59)' and '...which the owed live re-check is to confirm'. Two stale-claim rows were red first.
- **fixed**: Round 2 review nit: TestUATIntroNamesItsNewestCandidate Snapshot check looser than stated
  - uatSnapshotNamesCommit reads only from 'Snapshot:' to the next 'Date:'. The negative test TestUATSnapshotCommitCheckReadsOnlySnapshots failed against the old check.
- **fixed**: Round 2 review nit: 'candidates 6's and 7's'; CHANGELOG.md:170 and release.md:26 unwrapped
  - The notes now say 'candidate 6's and candidate 7's' (stale-claim row added), and the paragraphs are re-wrapped to 100 columns.
- **fixed**: Round 3 review minor: release.md:229 capability row and release-notes v0.3.0.md:167-168 lead open with an unqualified guarantee D61(b) does not give
  - Holds. D61(b)(5) leaves aliases, globs and run-time names in free text unresolved, and D7 limits the check to the saved rules. The capability row now says the pointers are judged against the saved Read rules and the project boundary. It spells out what is judged whole and what free text is withheld for, and says aliases, globs, run-time names and rules outside the saved settings are recorded limits (rows below). The notes lead says 'In its pointers, the rehydration block withholds a path your saved Read rules deny and an absolute path outside the project', and its limits sentence is kept. Two stale-claim rows in TestCloseoutC8StaleClaimsAreGone were red on 7d1f678e and pass now. Commit ee30e2fc.
- **fixed**: Round 3 review nit: CHANGELOG.md:121-123 connect-budget clause includes `qompack mcp`
  - Holds: D61(c), and on w19b-cmdconnect qompack_commands.go:191 says '`qompack mcp` does not use it'. The clause now names the slash-command frontends and adds '(`qompack mcp` keeps the hooks' budget and its retry loop, D61(c))'. A stale-claim row was red first. Commit ee30e2fc.
- **fixed**: Round 3 review nit: tag-push guard pins one phrasing; README/CHANGELOG/release.md interim text unguarded; release.md step 2 reads broader than the check
  - Hardened and documented. A per-page releaseInterimMarkers table now holds 10 exact markers across the four step-2 pages (the notes' two, README's four, CHANGELOG's one, release.md's three). New live row TestReleasePagesForAPushedTagCarryNoInterimText checks README, CHANGELOG and release.md on a tag push and reports every leftover marker. New negative fixture TestReleasePagesInterimGuardFiresOnlyOnATagPush was red against a nil stub. Under GITHUB_REF_TYPE=tag all 10 markers fire on today's tree; without the env both live rows pass. release.md step 2 now says the guard catches only those exact sentences and the rest of the rewrite, the c6/c7 table rows included, stays manual. 'candidate 6's and candidate 7's' is not banned outright, because candidate 7's host-seen hook figures may legitimately stay. Commit 3b8fd645.

### Tests

- `go test -p 1 -count=1 -run '^TestCloseoutC8StaleClaimsAreGone$' ./test/docs (new rows, before the doc edits, on 7d1f678e docs)`: FAIL as intended: 3 retired phrasings (release.md capability row, release-notes lead, CHANGELOG connect-budget clause)
- `go test -p 1 -count=1 -run '^TestReleasePagesInterimGuardFiresOnlyOnATagPush$' ./test/guards (nil stub)`: FAIL as intended; ok after implementation
- `GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.3.0 go test -p 1 -count=1 -run '^(TestReleasePagesForAPushedTagCarryNoInterimText|TestReleaseNotesForAPushedTagCarryNoInterimSentence)$' ./test/guards`: FAIL as intended: all 10 interim markers reported (README 4, CHANGELOG 1, release.md 3, notes 2); ok without the tag env
- `go test -p 1 -count=1 ./test/docs`: ok (6.2s)
- `go test -p 1 -count=1 ./test/guards`: ok (44.5s)
- `go test -p 1 -count=20 -run '^(TestReleasePagesInterimGuardFiresOnlyOnATagPush|TestReleaseNotesInterimGuardFiresOnlyOnATagPush|TestReleasePagesForAPushedTagCarryNoInterimText|TestReleaseNotesForAPushedTagCarryNoInterimSentence)$' ./test/guards`: ok (go test -list confirms all 4 names)
- `go test -p 1 -race -count=3 -run '^(TestReleasePagesInterimGuardFiresOnlyOnATagPush|TestReleaseNotesInterimGuardFiresOnlyOnATagPush|TestReleasePagesForAPushedTagCarryNoInterimText|TestReleaseNotesForAPushedTagCarryNoInterimSentence)$' ./test/guards`: ok
- `go test -p 1 -count=20 -run '^TestCloseoutC8StaleClaimsAreGone$' ./test/docs`: ok
- `go test -p 1 -race -count=3 -run '^TestCloseoutC8StaleClaimsAreGone$' ./test/docs`: ok
- `GOOS={windows,linux,darwin} go vet ./test/docs ./test/guards`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./test/docs/... ./test/guards/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: exit 0 each
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS docmarkers, PASS runpatterns
- `go run ./tools/devtool check-commit-msg <each new commit message>`: exit 0 each
- `Round 2 rows (carried): go test -p 1 -count=20 / -race -count=3 -run '^(TestCloseoutC8StaleClaimsAreGone|TestUATSnapshotCommitCheckReadsOnlySnapshots|TestUATIntroNamesItsNewestCandidate)$' ./test/docs`: ok (round 2)
- `Round 1 rows (carried): go test -p 1 -count=20 / -race -count=3 -run '^(TestCloseoutC8StaleClaimsAreGone|TestReleasePagesNameOneCandidateRecord|TestCapabilityTableDecisionRangeCoversItsRows|TestReleasePagesListTheCandidate8Limits|TestUATOpenQuestionsNameTheirRuling|TestUATIntroNamesItsNewestCandidate)$' ./test/docs and '^(TestReleaseBundlesUploadKeepsHiddenFiles|TestReleaseBundlesUploadGuardRejectsReshapedSteps)$' ./test/guards`: ok (round 1)

### Criterion changes

- Round 1: TestUATIntroNamesCandidate7 was renamed TestUATIntroNamesItsNewestCandidate and generalized to the newest candidate the intro names (floor 7, known commits pinned, decision >= D59, plus a Snapshot cross-check). Reason: the candidate 8 re-check would otherwise force a same-commit test edit.
- Round 2: the Snapshot cross-check reads only Snapshot text and has a negative fixture. Reason: review nit; stricter than before.
- Round 1: new docs criteria in closeout_c8_claims_test.go (retired sentences absent; one candidate record across the release pages, floor 8; decision range covers its rows; candidate 8 limits listed; UAT ruling lines). Reason: audit findings 0-9 and 11.
- Round 2: five more stale-claim rows. Reason: that round's review findings.
- Round 3: three more stale-claim rows: 'The rehydration block's pointers never show a path the host denies' (release.md), 'The rehydration block shows no path your saved Read rules deny' (release notes), and the CHANGELOG clause giving every daemon-calling command its own connect budget. Reason: this round's review; D61(b)(5) and D61(c).
- Round 1: new guard requiring ci.yml release-dry-run's release-version-bundles upload to set include-hidden-files: true. Reason: audit finding 10.
- Round 2: new guard TestReleaseNotesForAPushedTagCarryNoInterimSentence (tag push only). Reason: review minor 2.
- Round 3: the tag-push guard widened from one notes phrasing to a per-page marker table over the notes, README, CHANGELOG and release.md, with a new live row TestReleasePagesForAPushedTagCarryNoInterimText and a new negative fixture TestReleasePagesInterimGuardFiresOnlyOnATagPush. Still inactive on branch pushes and the local rehearsal. release.md step 2 now states the guard's exact scope. Reason: this round's review nit; stricter than before.

### Open issues

- Merge order, for the coordinator: merge w20-docs after closeout/w19-rehydrate (19c), closeout/w19b-cmdconnect and every other closeout/w20-* product branch. Then re-check the docs against what actually merged:
- the CHANGELOG Fixed entries, including the connect-budget clause against w19b's final scope
- the candidate 8 composition sentences (README verified-where paragraph, release.md status)
- the CHANGELOG Security entry, cannot-do §5 and the new capability row against 19c's rendering and its docs/security.md (for example, whether a file pointer's home- or variable-rooted path is still withheld, and whether the "summary withheld" note survives 19c)
- the release notes' limits and capability rows
- plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md is cited by path and does not exist until the freeze. The README table, the release notes table and the CHANGELOG Known-limits figures are candidate 6/7 results, labelled as theirs. Per release.md §1 step 2, a docs-only descendant must replace them from candidate 8's evidence before the tag. On the tag push, the guards step now fails while any of the 10 listed interim sentences remains on the four pages. Rewriting everything else (the c6/c7 table rows among it) stays a manual step-2 duty.
- If the step-2 rewrite rewords an interim sentence rather than removing it, the guard will not catch the new wording. Remove interim text; do not reword it.
- CHANGELOG has no entry for the config-violations write cost, because closeout/w20-config has not landed. If it lands, add one Fixed/Diagnostics clause.
- plans/sdd/V6-closeout/w19-docs/report.md:307 quotes the old test name TestUATIntroNamesCandidate7 inside an alternation. runpatterns still passes because the other alternatives match; it is a history report, so I left it alone.
- The Ubuntu 26 migration (2026-10-19) affects ci.yml and release.yml, which I do not own. The risk is documented in docs/release.md §7; nothing is pinned.

### Needs owner

- New constant releaseCandidateFloor = 8 (test/docs/closeout_c8_claims_test.go). Derivation: D58(e), the release tags candidate 8 or a descendant whose changes reach no bundle.
- New constant uatIntroCandidateFloor = 7 (test/docs/closeout_c7_claims_test.go). Derivation: candidate 7's re-run (D59) is the newest on docs/uat.md.
- New constant uatIntroRulingFloor = 59 and the table uatCandidateCommits {3: d5598eb4, 4: 9f6a2fad, 7: d20309c0}, taken from commits that docs/uat.md records. Add candidate 8's commit when the re-check updates the page.
- New marker table releaseInterimMarkers (test/guards/releasenotes_test.go), which replaces the single releaseNotesInterimRE. It holds 10 exact interim sentences across docs/release-notes/<tag>.md, README.md, CHANGELOG.md and docs/release.md, enforced only when GITHUB_REF_TYPE=tag. Derivation: release.yml publishes the notes verbatim, and the tagged tree ships all four pages, so none may still say the candidate's evidence is owed (docs/release.md §1 step 2). Extend the table if new interim sentences are added.
- Decide whether v0.3.0 is tagged before 2026-10-19, or whether ci.yml's release-dry-run and release.yml's release job are first pinned to ubuntu-24.04 or proven green on Ubuntu 26 (docs/release.md §7).

## verify:docs: verdict `sound`, 0 finding(s)



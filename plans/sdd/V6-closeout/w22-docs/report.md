# Wave 22 docs seat

Branch `closeout/w22-docs`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `CHANGELOG.md:139-150 (Fixed bullet on rehydration pointers); docs/cannot-do.md:774-779 (section 5, 'What Qompack does instead'); docs/release.md:232 (capability row for the pointer screen) and docs/release.md:210 ('D1 to D61'); docs/release-notes/v0.3.0.md:167-172`: Four user-facing pages describe the D61(b)(2) screen that D63 replaced. That screen withheld free text only when it contained a rule's literal, a withheld name or an outside absolute path, and none of the four mentions the D63 whitelist HEAD actually runs. On HEAD a free-text summary is shown only when every token passes tokenSafe/plainTokenSafe/urlTokenSafe (internal/rehydrate/pathgate.go:1440-1620). So any command that holds a variable, a glob, a regex, a `%xx`, a backtick, a caret or a `name:` where a path may start is withheld, even when it names no rule literal or path. cannot-do section 5 also says the text is 'decoded and normalized', which is D61's wording: the code percent-decodes nothing. screenText (pathgate.go:2151) only strips ' " ` \ ^ and folds case, and a `%` before two hex digits withholds. D63 and D64 are cited only in docs/security.md:86/118 and ADR 0011. The capability table says it was written from D1-D61, and its row cites D61(b), the superseded rule. The w20-docs report's own open issue asked that the CHANGELOG Security entry, cannot-do section 5 and the capability row be re-checked against 19c; they never were.
- **major** `CHANGELOG.md:139-155 (Security); docs/release-notes/v0.3.0.md:167-172; docs/release.md:232 and :260 (capability rows); docs/cannot-do.md:759-782 (§5); docs/uat.md:1606-1612 (UAT-12 Ruling line); README.md:96`: Every user-facing release page except docs/security.md still describes D61's screen ('a free-text summary is withheld when it contains a rule literal, a withheld name or an absolute outside path'). D63 replaced that screen and D64 tightened its successor. A free-text summary is now SHOWN only when a whitelist proves every token safe, with the D64(1) root-unit rules and the accepted over-withholding of D64(4). On the corpus about 53 of 246 non-private previews are withheld: variables, globs, regexes, `localhost:3000`, `HEAD:x`, `@name`, a second `%`. None of these pages cites D63 or D64, and cannot-do §5's 'Recorded at' cites D61(b) only. The only over-withholding they disclose is 'free text that only mentions a rule's literal'. The D60(iv) whitespace-collapse limit appears only in ADR 0011. release.yml publishes the release notes verbatim. The w20-docs report's open issues asked for exactly this re-check once closeout/w19-rehydrate merged. The docs seat merged first (c10d9a40) and rehydrate later (752c073b). No later commit touches these pages.
- **major** `docs/release.md:22-33 ('Still owed before the tag, all on candidate 8: ...'); README.md:98-104; docs/release-notes/v0.3.0.md:71-75; docs/architecture.md:722`: The pages that list the evidence owed before the tag leave out two items. One is the C5.2 night: D62(b) requires C5.2 to be re-measured in full on candidate 8, because candidate 8 changes internal/config/config.go, which every benchmark links. The other is the C1.16 rig re-measure and its docs restatement (D62(c), D65(a)). release.md calls its list 'Still owed before the tag', and the README and the release notes give the same list, so a tag cut right after night 1 would satisfy every document while C5.2 is still unmeasured. architecture.md:722 promises 'candidate 8's C5.2 night re-measures it (D62(c))', which none of the release pages lists.
- **minor** `test/guards/releasenotes_test.go:154-173 (releaseInterimMarkers); CHANGELOG.md:12-14; docs/architecture.md:722`: The tag-push interim guard misses interim sentences that will still be in the tree. (a) CHANGELOG.md:12-14 carries the same 'recorded in `plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md` when it is frozen' sentence that README and release.md are guarded for, but CHANGELOG's only marker is 'supply the release's evidence, and they are owed'. release.md step 2 lists only 'CHANGELOG.md's Known limits', not this intro paragraph. (b) docs/architecture.md:722 'candidate 8's C5.2 night re-measures it' is interim text on a page that is neither in TestReleasePagesForAPushedTagCarryNoInterimText's page list nor in step 2's list. (c) Nothing checks on a branch push that each marker still matches today. A reworded interim sentence therefore disables its marker silently; the w20-docs report names this risk itself.
- **minor** `CHANGELOG.md:148-150 and :188-189; docs/release-notes/v0.3.0.md:171-172; docs/cannot-do.md:766-767; docs/uat.md:1606-1609; docs/adr/0011-rehydration-budget-and-item-order.md:824-831; test/docs/closeout_c8_claims_test.go:230`: The docs cite the wrong decision for section 2. They say sections 2 to 4 (or 'section 2's verbatim intent') are outside D50 and cite D60(c)(i) or D61(b). D60(c)(i) covers only sections 3 and 4. Section 2 was ruled by D62(f), which no document cites. ADR 0011 section 23's 'What D50 covers' paragraph names sections 3 and 4 only and never section 2, so the decision record lacks the ruling the user-facing pages rely on. The docs test pins the wrong citation: it requires 'Ruling (D60(c)(i)' in UAT-12.
- **minor** `CHANGELOG.md:116-131 (Diagnostics), :111-117 (Rehydration), :156-205 (Known limits); docs/release-notes/v0.3.0.md:123-177; docs/release.md capability table`: The CHANGELOG misses user-visible changes since candidate 7. (a) state/config-violations.json is now written only when it changes and removed when clean, so doctor's config.violations clears once the file is fixed (660d876f, 07ff04ad, d64129b2; troubleshooting.md:20-21 and section 6 document it). The w20-docs report left this open: 'If it lands, add one Fixed/Diagnostics clause'. (b) status names a disabled daemon, state.bin included, instead of a connect miss (3675a771, 722ef8e7; troubleshooting.md:131-135). (c) Current work, in every session and not only in forks, skips slash-command invocations, never moves backwards and falls back past an unreadable newest prompt (6dfc8d2b, 692e7614, 2780f75c; D62 forkwork). The CHANGELOG mentions forks only. (d) Two newly documented known limits are absent from the CHANGELOG known limits, the release-notes limits and the capability table: a quiet live session is ended as abandoned until its next hook (cannot-do.md:481-503, D62 sessionend), and a missed connect can lazily spawn a duplicate daemon that exits (troubleshooting.md:152-155, D61(c)).
- **minor** `CHANGELOG.md:100-128 ([Unreleased] Fixed)`: The Fixed section was written by w20-docs before the wave 20 product seats merged. It has no entry for: config-violations.json written only when it changes and removed when clean, with the new logging levels; the scheduler tap's double fold of a redelivered delivery's tokens (status open_segment_tokens 12 instead of 8); fsck's stable detail order; eval's stable error messages; current work skipping slash commands and never moving backwards; the drain memo's cost cut. The w20-config report ('CHANGELOG/release notes do not mention the logging changes ... CHANGELOG is not my file') and the w20-docs report ('If it lands, add one Fixed/Diagnostics clause') both left this open. Both seats have since merged and nobody added the entries.
- **minor** `docs/adr/0010-wall-clock-under-coload.md:221-225`: ADR 0010 Addendum 2 still says release.yml's `release` job declares QOMPACK_NONREFERENCE_DISK 'pending the owner's ruling, C7.2'. docs/release.md:131-143 points to this addendum as 'which holds the complete list' and says both declarations are recorded by D57(a). The pages contradict each other.
- **minor** `docs/uat.md:679`: UAT-05's expected result reads '`Tokens` and `Budget` in the state file from step 3 are the numbers to compare'. The state file's keys are lowercase `tokens` and `budget`. That is the error the candidate 8 stale-claim test removed from troubleshooting.md, and UAT-05's own Result block says 'Both states read tokens 0', so the page contradicts itself.
- **minor** `docs/uat.md:1197-1199 (UAT-09 Result, Observation); plans/sdd/V6-closeout/live/report-c7.md:217 and :314-315`: D62 rules UAT-09 O-1 by design: SessionEnd WAS delivered, and the abandoned-session sweep at idleExitSeconds=30 ran during a 34.5 s reply. Yet the user-facing UAT result still says 'the recording session's SessionEnd never reached the daemon, which ended it as abandoned'. That contradicts the ruling. The sessionend seat flagged the wording as wrong in files it did not own, and nobody fixed it.
- nit `docs/security.md:89`: The page says an http(s) URL is shown when it is 'built only from letters, digits and `- . _ ~ : / ? # @ & = +`'. In the code, each part after an `&` must be a plain token (urlTokenSafe, pathgate.go:1490-1497), so a `#` fragment or a `?` after an `&` withholds the URL. ADR 0011:1050-1054 states this correctly. The error over-withholds and leaks nothing.
- nit `docs/release.md:31-33`: The page says the local `release-check --tag` runs 'against a local tag it deletes afterwards'. The night tooling runs it in an isolated `git clone --no-local` scratch clone with no-push URLs, the tag exists only in that clone, and the clone is removed. No v0.3.0 tag ever enters the shared ref store (D62 night).
- nit `internal/cli/qompack_commands.go:190`: A code comment cites '(D61(e))' for `qompack mcp` keeping runtime.daemon.connectDeadlineMs. D61 has only parts (a) to (c); the ruling is D61(c), which the CHANGELOG cites correctly.
- nit `test/docs/closeout_c8_claims_test.go:239-241`: TestUATOpenQuestionsNameTheirRuling skips a block whose question text is gone ('continue'), so rewording the question makes the check vacuous instead of failing.
- nit `internal/cli/main_test.go (checkoutSpoolGuard); test/guards (no equivalent)`: The checkout-leak guard covers only internal/cli's test binary. test/guards also runs all six hooks in process, and the w20-status seat left checking that package for the same leak as outside its scope.
- nit `w20-forkwork/report.md Needs owner (decision (f), criterion change (e)); docs/architecture.md §7; docs/cannot-do.md`: The forkwork seat's decisions are not in the ledger or the docs: current work follows the captured turn, not the host's timestamp (first audit forkwork F6, declined); the goal is the newest goal-giving prompt; a bare `/name` is skipped. D62 mentions only the slash-command skip.
- nit `internal/rehydrate/pathgate.go (whole file)`: No coordinator ruling exists on pathgate.go's size, although 19c (F17) and the 19d review both asked for one. It now has 2766 lines, 1809 code lines, 118 functions and gocyclo 797.
- nit `docs/troubleshooting.md §7 (abandoned-session entry, about :1197-1205)`: The abandoned-session entry names only a long /compact as the hookless step. A long reply with no tool call (the UAT-09 O-1 shape D62 ruled on) is the second observed shape. cannot-do already states both.

## impl:docs: status `done`, head `3b14b9d0`

### Summary

I fixed all ten findings (#39, #62, #40, #41, #43, #44, #45, #46, #66, #70) on closeout/w22-docs, in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-docs. There are two commits on base 2bf29705, nothing is pushed, and the tree is clean. No product code changed.

The free-text rewrite (#39/#62) covers CHANGELOG.md (Security entry and Known limits), docs/release-notes/v0.3.0.md, docs/release.md (the shipped row and the residual row of the capability table), docs/cannot-do.md §5 and uat.md's UAT-12 ruling. I wrote it from docs/security.md:84-151 and D63/D64/D67(l). The pages now say:
- A free-text summary is shown only when the whitelist proves it safe. JSON string values and one-word summaries are judged the same way, and every free-text summary is withheld while the host's rules cannot be read.
- The over-withholding is accepted (D64(4)): variables, globs, regexes, % escapes, and name: shapes such as `localhost:3000` and `format:%h`.
- The D64(1) root-unit rule, the D60(c)(iv) whitespace-collapse limit, and that a path-named value holding several paths is judged piece by piece.
- 'Decoded and normalized' is gone, every page cites D63 and D64, and the capability table now says it was written from D1 to D67.
- cannot-do §5 also states the D67(l) ANSI default-character limit.

The rest of the docs work:
- **Owed before the tag (#40):** README, release.md, the release notes and CHANGELOG's owed sentence now list the C5.2 night (D62(b), D65(b)) and the C1.16 re-measure (D62(c), D65(a)). release.md step 2 now names architecture.md §7's C1.16 paragraph and the two Known issues sections.
- **Section 2 (#43):** cited as D62(f) wherever it is put outside D50, including the uat.md header and the UAT-12 ruling.
- **CHANGELOG (#44/#70):** the wave 19b-21 Fixed entries are in, taken from the w20 reports and commits. The new limits are in CHANGELOG, the notes and the capability table: the quiet session (D62), the second daemon (D61(c)), the cross-session segment close (D67(b)), the recorded-corpus tier (D67(g)) and the macOS case-insensitive volume (D67(m)).
- **cannot-do.md:** a new §4 entry for D67(b), including the lost-owed-close residual.
- **Smaller fixes:** ADR 0010 Addendum 2 cites D57(a) (#45). UAT-05 uses lowercase `tokens`/`budget` (#46). UAT-09 O-1 is reworded with the sessionend seat's text and ruled by design under D62 (#66).
- **Known issues:** the notes and CHANGELOG each have a Known issues heading citing D66(d). Its one interim sentence is a guarded marker, so the tag push fails until the coordinator fills the section.

For #41 I extended the guard's marker table instead of editing architecture.md (D66(b), class fix):
- New markers for CHANGELOG's "-CANDIDATE.md` when it is frozen" sentence and architecture.md's "C5.2 night re-measures it".
- The tag-push test now reads its page list from the marker table, so there is no second list to keep in step.
- A new liveness check requires every marker to still match its page until CHANGELOG has the version's own heading, so a reworded interim sentence now fails instead of silently disabling its marker.

Optional nits fixed: release.md's scratch-clone wording, the vacuous UAT ruling check, and the forkwork 'captured order' sentence in cannot-do. The other nits are in files that are not mine.

Every docs and guards check passes at HEAD: the docs and guards suites, the docmarkers and runpatterns lint, and fmt-check.

### Commits

- c1217a1e docs(release): describe the D63 whitelist and owed C5.2 night
- 3b14b9d0 test(guards): keep the release interim markers live

### Findings resolution

- **fixed**: #39 (major) four release pages describe D61(b)(2)'s blacklist screen; 'decoded and normalized'; capability range D1-D61
  - Fixed in c1217a1e. Red-first rows, all red on 2bf29705 and green at HEAD: TestCloseoutW22StaleClaimsAreGone (stale-claim rows for 'decoded and normalized', 'A free-text summary is withheld when', the CHANGELOG blacklist sentence, the release.md row and the release-notes 'are screened' wording) and TestReleasePagesDescribeTheD63Whitelist, which requires D63, D64, whitelist, 'shown only when', `localhost:3000`, `format:%h`, variable, glob, regular expression, `%`, 'root unit', 'collapses runs of whitespace' and 'piece by piece' on all four pages. TestCapabilityTableDecisionRangeCoversItsRows now runs against 'D1 to D67'.
- **fixed**: #62 (major) every release page except security.md still describes D61's screen; no D63/D64 citation; whitespace-collapse limit missing
  - Same commit (c1217a1e) and rows as #39. I also swept uat.md: its header and UAT-12 ruling cited D61(b) as the free-text remedy and now cite D61(b)(1) and D63/D64; the stale-claim row 'and free text is screened' was red on base. README:96 only lists D61 among candidate 8's product changes, which stays true. cannot-do's 'Recorded at' now cites D62(f), D63, D64 and D67(l).
- **fixed**: #40 (major) owed-before-tag lists omit the C5.2 night and the C1.16 re-measure
  - Fixed in c1217a1e. README, release.md, the release notes and CHANGELOG's owed sentence now list both items. release.md step 2 names docs/architecture.md §7's C1.16 paragraph. Red-first row: TestOwedEvidenceListsTheC52NightAndC116 (red on 2bf29705 for all three pages and for step 2).
- **fixed**: #41 (minor) interim guard misses CHANGELOG:12-14 and architecture.md:722; nothing checks markers stay live
  - Fixed in 3b14b9d0; architecture.md is guarded, not edited. Red-first row: with GITHUB_REF_TYPE=tag on 2bf29705, TestReleasePagesForAPushedTagCarryNoInterimText reported 8 problems and missed both sentences; at HEAD it also reports CHANGELOG's '-CANDIDATE.md` when it is frozen' and architecture.md's 'C5.2 night re-measures it'. The page list now comes from the marker table. New rows: TestReleaseInterimMarkersStillMatchTheirPages (red until the Known issues sentences existed) and TestReleaseInterimMarkersLivenessRejectsAReword.
- **fixed**: #43 (minor) section 2 cited as D60(c)(i)/D61(b) instead of D62(f); test pins the wrong ruling
  - Fixed in c1217a1e for every page in my files: CHANGELOG, the notes, release.md, cannot-do and uat.md (header and UAT-12 ruling). Red-first row: TestSection2IsRuledByD62f, which requires D62(f) in any block that puts section 2 outside D50 (red on base for all five pages). TestUATOpenQuestionsNameTheirRuling now requires both D60(c)(i) and D62(f) in UAT-12's ruling line (red on base). Not done: the sentence the finding asks for in ADR 0011 §23 ('What D50 covers'), because ADR 0011 is not my file.
- **fixed**: #44 (minor) CHANGELOG misses config-violations, status disabled-daemon, current-work and the quiet-session/duplicate-daemon limits
  - Fixed in c1217a1e. Diagnostics entries: config-violations.json written only on change and removed when clean, warn-level hook logging, status naming a disabled daemon. Rehydration entry: current work in every session. The quiet-session (D62) and second-daemon (D61(c)) limits are in CHANGELOG, the notes and release.md capability rows. Red-first rows: TestChangelogNamesTheWave20Fixes and TestReleasePagesListTheAudit2Limits, both red on base. The disabled-daemon entry does not describe state.bin, because D67(c) is changing that in another seat this wave.
- **fixed**: #70 (minor) CHANGELOG Fixed lacks the wave 20 fixes (redelivery double fold, fsck order, eval order, slash commands, drain memo)
  - Fixed in c1217a1e from the w20-config, drain, redeliver, status and forkwork reports. Capture: the drain memo (8.2 s to 7.5 ms; 546,004 to 5,107 journal queries) and a redelivered delivery applied to the scheduler once (state/scheduler.json open_segment_tokens). Diagnostics: session_start.fires with overlapping sessions, fsck detail order, eval messages. Wave 21 changed no user-visible behaviour. Covered by TestChangelogNamesTheWave20Fixes (red on base). Wave 22's own entries come after it merges.
- **fixed**: #45 (minor) ADR 0010 Addendum 2 says release.yml's declaration is 'pending the owner's ruling, C7.2'
  - Fixed in c1217a1e: it now reads 'recorded by D57(a)', and notes that D57(a) also records release-dry-run's declaration. A sweep for 'pending the owner' across README, CHANGELOG and docs finds nothing else. Stale-claim row in TestCloseoutW22StaleClaimsAreGone, red on base.
- **fixed**: #46 (minor) uat.md UAT-05 says `Tokens` and `Budget`
  - Fixed in c1217a1e: now lowercase `tokens` and `budget`. The stale-claim row is widened to docs/uat.md and was red on base. A sweep of every doc for capitalised `Tokens` finds only ADR 0012:334, which names a Go field, not a state-file key.
- **fixed**: #66 (minor) UAT-09 O-1 says SessionEnd never reached the daemon, contradicting D62
  - Fixed in c1217a1e with the sessionend seat's wording: the WARN fired during T9's 34.5 s reply, Stop revived the session, and SessionEnd ended it at 19:25:51; ruled by design, D62. The stale-claim row 'SessionEnd never reached the daemon' was red on base. report-c7.md:217 and :314 are under plans/sdd/V6-closeout/live/, outside my files, so they are not edited (open issue).

### Tests

- `go test -p 2 -count=1 ./test/docs/... (new rows against the 2bf29705 docs)`: FAIL as intended (red-first): TestUATOpenQuestionsNameTheirRuling, TestCloseoutW22StaleClaimsAreGone, TestReleasePagesDescribeTheD63Whitelist, TestSection2IsRuledByD62f, TestOwedEvidenceListsTheC52NightAndC116, TestReleasePagesHaveAKnownIssuesSection, TestReleasePagesListTheAudit2Limits, TestChangelogNamesTheWave20Fixes
- `GITHUB_REF_TYPE=tag go test -p 2 -count=1 -run '^TestReleasePagesForAPushedTagCarryNoInterimText$' ./test/guards/ (guard at 2bf29705 vs new guard)`: 2bf29705: 8 problems, CHANGELOG '-CANDIDATE.md` when it is frozen' and architecture.md not reported. New guard: both reported (expected FAIL on a tag push while the interim text stands)
- `go test -p 2 -count=1 ./test/docs/...`: ok (HEAD)
- `go test -p 2 -count=1 ./test/guards/...`: ok, 156.5 s (same content as HEAD)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool fmt-check`: exit 0
- `go vet ./test/docs/ ./test/guards/`: exit 0

### Criterion changes

- TestUATOpenQuestionsNameTheirRuling: UAT-12's expected ruling changed from 'Ruling (D60(c)(i)' to a ruling line that names both D60(c)(i) and D62(f), because D60(c)(i) covers only sections 3 and 4 and D62(f) ruled section 2 (audit 2 #43). The check is also stricter: the ruling line is now required even when the question's wording has changed, where before a reworded question skipped the check (audit 2 nit). UAT-05 still requires D59(b).

### Open issues

- The ADR 0011 §23 'What D50 covers' sentence for section 2 (D62(f)) that #43 asks for is not written: ADR 0011 is not my file. Every user-facing page in my set cites D62(f).
- plans/sdd/V6-closeout/live/report-c7.md:217 and :314 still carry the old UAT-09 O-1 wording. They are historical lane reports outside my files.
- Every page now says a path-named value holding several paths is judged piece by piece. That is true only once the rehydrate seat's audit 2 #26 fix merges; if that fix changes shape, these sentences need a recheck.
- The CHANGELOG's 'status names a disabled daemon' entry does not describe state.bin, on purpose: D67(c) is changing state.bin trust in another seat this wave, so wave 22's own CHANGELOG entries should add it after merge.
- Both Known issues sections carry the guarded sentence 'This list is filled from the ledger once candidate 8's last fixes are verified'. The coordinator must fill the list and delete that sentence (and its two markers) in release.md step 2's commit, or the tag push fails.
- Nits outside my files are not done: security.md:89 URL wording, the qompack_commands.go:190 D61(e) comment that should read D61(c), a checkout-leak TestMain in test/guards (only releasenotes_test.go there is mine), the troubleshooting long-reply shape, and the pathgate.go size ruling.

### Needs owner

- Coordinator: once the rehydrate seat's #26 fix merges, confirm that the 'piece by piece' sentences on the release pages match it.
- Coordinator: fill the two Known issues sections from the ledger under D66(d), and remove their interim sentence and its releaseInterimMarkers entries in release.md §1 step 2's docs-only commit.

## review:docs:r1: verdict `needs-fixes`, 3 finding(s)

- **minor** `CHANGELOG.md:216-218 (Known limits, 'The rehydration's free-text whitelist' bullet); docs/cannot-do.md:796-798 (§5 Limit)`: The new text says the free-text whitelist 'judges a glob that selects only files the block never recorded as written'. That limit belongs to structured globs, not to free text: a lone Glob or recall argument, or a path-named pattern value (ADR 0011:855-858 and :896-898). In free text, every glob is withheld. Two lines earlier, cannot-do itself says 'Globs in free text stay withheld (D67(l))', so the page contradicts itself. The paraphrase also drops the point of the limit, which is that such a glob can select a REFUSED file without spelling its literal (security.md:152 and ADR 0011:857 both say 'a refused file ... without spelling its literal').
  - Evidence: CHANGELOG.md:216-218: 'It does not resolve aliases ... ; a glob that selects only files the block never recorded is judged as written'. cannot-do.md:790-791: 'Globs in free text stay withheld (D67(l))'. cannot-do.md:796-798: 'The whitelist ... judges a glob that selects only files the block never recorded as written'. ADR 0011:850-858 says a free-text token holding `*`, `?`, `[` or `{` 'is withheld rather than decoded', and that 'A structured glob (item 6) ... one that selects a refused file Qompack never recorded without spelling its literal (`private/d*`) is the next bullet's limit'. No test row pins this sentence. The release notes and the release.md row do not carry it.
  - Fix: In both places, attach the sentence to structured values, not to the whitelist. For example: 'a structured glob (a lone Glob or recall pattern) that selects a refused file the block never recorded, without spelling its literal, is judged as written (D60(c)(iv))'. Optionally add a stale-claim row to closeout_c8_w22_claims_test.go for 'judges a glob that selects only files the block never recorded'.
- **nit** `docs/release-notes/v0.3.0.md:129-131 and :189-191; CHANGELOG.md:222-224; docs/release.md:271`: The new macOS case-insensitive limit (D67(m)) says it is documented in docs/security.md, and the notes link to security.md §8. At HEAD and at 2bf29705, security.md never mentions case sensitivity. The statement depends on the rehydrate seat's audit 2 #85, which writes it into security.md and ADR 0011 §23 and does not commit to §8. The seat flagged its #26 'piece by piece' dependency in needs_owner but not this one.
  - Evidence: `grep -n -i 'case-insensitive\|case-sensitive' docs/security.md docs/adr/0011*.md` finds no match at HEAD 3b14b9d0. rehydrate.json #85's fix reads: 'record the case-insensitive-volume assumption ... as a known limit in docs/security.md and ADR 0011 §23'.
  - Fix: Add a coordinator needs_owner item: after the rehydrate seat's #85 merges, confirm that security.md states the macOS limit and that the notes' `#8-known-limitations` anchor points at the section that holds it.
- **nit** `seat result, findings_resolution #43 and #66`: #43 and #66 are reported as 'fixed', but each finding's requested fix has a part outside the seat's files that is still undone: the D62(f) section-2 sentence in ADR 0011 §23 'What D50 covers' (#43), and report-c7.md:217/:314 (#66). These parts appear only under open_issues, not as deferred-known-issue. No other wave 22 seat's findings file takes the ADR 0011 D62(f) sentence, so the decision record keeps lacking the ruling that the user-facing pages now cite.
  - Evidence: docs.json #43 fix: 'Add a section 2 sentence to ADR 0011 section 23's "What D50 covers"'. A grep of rehydrate.json shows only #85 touching ADR 0011 §23 (macOS), not D62(f). The user-facing part of each finding is closed and pinned by red-first rows: TestSection2IsRuledByD62f, and the 'SessionEnd never reached the daemon' stale-claim row.
  - Fix: Report #43 as fixed for the user pages, with the ADR 0011 §23 D62(f) sentence routed to the ADR's owner or the coordinator. Report #66's report-c7.md part as left on purpose, since report-c7.md is a historical lane report.

## fix:docs:r1: status `done`, head `3eb54e36`

### Summary

Fix round 1 is done. The verifier's one minor finding is fixed in 3eb54e36 on closeout/w22-docs, in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-docs, on top of the earlier c1217a1e and 3b14b9d0. The tree is clean, nothing is pushed, and no product code changed.

**The finding.** CHANGELOG.md's Known limits and docs/cannot-do.md §5 said the free-text whitelist "judges a glob that selects only files the block never recorded as written". That contradicts D67(l), because every glob in free text is withheld, and it also dropped the point of the limit.

**The fix.** I attached the limit to structured globs instead, as ADR 0011 §23 and docs/security.md state it. The new sentence is: "a structured glob (a lone Glob or recall pattern) that selects a refused file the block never recorded, without spelling its literal (`private/d*`), is judged as written (D60(c)(iv))". The CHANGELOG now says first that globs in free text are always withheld, and cannot-do opens the sentence with "Outside free text".

**Class sweep (D66(b)).** docs/release-notes/v0.3.0.md and the residual row of the docs/release.md capability table did not carry this limit at all. They now carry the same sentence with an inline D60(c)(iv) citation. A grep of README.md, docs/uat.md and ADR 0010 for "never recorded" finds nothing else. docs/security.md and ADR 0011 already state the limit correctly and are not my files.

**New rows**, in test/docs/closeout_c8_w22_claims_test.go:
- Two stale-claim rows retire the old CHANGELOG and cannot-do wording.
- TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs requires the structured-glob sentence, with "refused file", "without spelling its literal" and D60(c)(iv), on all four whitelist pages. It also rejects the bare paraphrase "glob that selects only files" on those pages and on uat.md and README.md.

I reflowed the edited paragraphs to the pages' line width. Every check passes at HEAD.

### Commits

- c1217a1e docs(release): describe the D63 whitelist and owed C5.2 night
- 3b14b9d0 test(guards): keep the release interim markers live
- 3eb54e36 docs(release): attach the unrecorded-glob limit to structured globs

### Findings resolution

- **fixed**: FIX ROUND 1 (minor): CHANGELOG.md:216-218 and docs/cannot-do.md:796-798 attach the 'glob that selects only files the block never recorded' limit to the free-text whitelist, contradicting D67(l), and drop the 'refused file ... without spelling its literal' point
  - Fixed in 3eb54e36. Both passages now attach the limit to a structured glob (a lone Glob or recall pattern) that selects a refused file the block never recorded, without spelling its literal (`private/d*`), and cite D60(c)(iv). The CHANGELOG first says globs in free text are always withheld; cannot-do opens the sentence with 'Outside free text'. Class sweep: the same sentence is added to docs/release-notes/v0.3.0.md and to docs/release.md's residual capability row, which were both missing it. Red-first rows, both in test/docs/closeout_c8_w22_claims_test.go: (1) TestCloseoutW22StaleClaimsAreGone gains two stale-claim rows, red at 3b14b9d0 for CHANGELOG and cannot-do; (2) TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs is red at 3b14b9d0 (bare paraphrase on CHANGELOG and cannot-do, structured sentence missing on all four pages) and red against the 2bf29705 pages (structured sentence missing on all four). Both are green at 3eb54e36.

### Tests

- `go test -p 2 -count=1 -run '^(TestCloseoutW22StaleClaimsAreGone|TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs)$' ./test/docs/ (new rows, pages at 3b14b9d0)`: FAIL as intended (red-first): two stale-claim hits, two bare-paraphrase hits, structured sentence missing on all four whitelist pages
- `go test -p 2 -count=1 -run '^TestReleasePagesAttachTheUnrecordedGlobLimitToStructuredGlobs$' ./test/docs/ (pages checked out at base 2bf29705)`: FAIL as intended: structured sentence missing on CHANGELOG.md, docs/release-notes/v0.3.0.md, docs/release.md and docs/cannot-do.md
- `go test -p 2 -count=1 ./test/docs/...`: ok (3eb54e36)
- `go test -p 2 -count=1 ./test/guards/...`: ok, 167.5 s (3eb54e36 content)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool fmt-check`: exit 0
- `go vet ./test/docs/`: exit 0

### Criterion changes

- None in this round. The new rows only add checks; no existing assertion was loosened.

### Open issues

- docs/security.md:152 states the same limit without the word 'structured' ('a glob that selects a refused file the block never recorded without spelling its literal'), inside a sentence that begins with free-text limits. It keeps the 'refused file ... without spelling its literal' point, so it is not the reported defect, but adding 'structured' would make it unambiguous. security.md is not my file.
- Carried from round 0: the ADR 0011 §23 D62(f) sentence (not my file); the old UAT-09 O-1 wording in plans/sdd/V6-closeout/live/report-c7.md (historical, not mine); the 'piece by piece' sentences, which need a recheck against the rehydrate seat's #26 fix; and the state.bin wording for the disabled-daemon CHANGELOG entry, which comes after D67(c) merges.

### Needs owner

- Coordinator: once the rehydrate seat's #26 fix merges, confirm that the 'piece by piece' sentences on the release pages match it.
- Coordinator: fill the two Known issues sections under D66(d), and remove their interim sentence and its releaseInterimMarkers entries in release.md §1 step 2's docs-only commit.

## review:docs:r2: verdict `needs-fixes`, 4 finding(s)

- **minor** `commit 3eb54e36 (closeout/w22-docs)`: The commit subject is longer than the brief's limit of 64 characters for a subject. The repo's commit-msg hook did not catch it because it counts only the free text after 'type(scope): ' (1-64), not the whole subject.
  - Evidence: git log --format=%s 2bf29705..HEAD gives lengths 67 'docs(release): attach the unrecorded-glob limit to structured globs', 51 and 61. tools/devtool/checkcommitmsg.go:21 subjectRE `: .{1,64}$` counts only the part after the scope. No attribution trailers were found, and all three commits carry 'Refs: V6-VERIFY, C6.1'.
  - Fix: Reword the unmerged seat commit to a whole subject of 64 characters or fewer. For example, 'docs(release): tie the unrecorded-glob limit to structured globs' is 64.
- **minor** `docs/release-notes/v0.3.0.md:130-131; CHANGELOG.md (macOS Known limits bullet, '(D67(m), `docs/security.md`)'); docs/release.md:271 (capability row linking [security](security.md))`: The new D67(m) macOS limit is stated on the seat's pages, but all three pages say docs/security.md documents it. The release notes say it is 'in docs/security.md §8' (#8-known-limitations). At HEAD, docs/security.md has no word about case-sensitive or case-insensitive volumes, and no wave-22 sibling worktree adds one. So the published notes, which release.yml copies verbatim, point readers to a section that does not state the limit.
  - Evidence: grep -i 'case-sensitive|case-insensitive' docs/security.md finds nothing at 3eb54e36. `git diff 2bf29705..HEAD -- docs/security.md` across every qompack-cx-w22-* worktree, rehydrate's staged changes included, has 0 such lines. The behaviour itself is correct: internal/hostperm/policy.go:115 has fold: goos == "windows" || goos == "darwin".
  - Fix: Either have security.md's owner add the D67(m) statement to §8 before integration, as D67(m) requires, or, until it lands, point the three pages at the ledger's D67(m) rather than security.md §8. Add a docs row that fails while a page cites security.md for the macOS limit and security.md does not state it.
- **nit** `README.md:94-96 ('the rehydration block and the command client (decisions D58(e), D60(f), D61)')`: Audit #62 listed README:96. The README does not describe the screen, but its list of candidate-8 product changes still cites only D61 for the rehydration block. D61 introduced the blacklist that D63 replaced and D64 tightened. Every other release page now cites D63 and D64.
  - Evidence: At 3eb54e36, grep -n 'D63\|D64' README.md finds no match. The seat's diff to README.md touches only the owed-evidence paragraph.
  - Fix: Cite D63 and D64 beside D61 in that sentence.
- **nit** `docs/release-notes/v0.3.0.md:182 ('and so is any text that mentions a rule's literal'); docs/release.md:270 ('as does a mention of a rule's literal')`: These pages overstate the literal check. A rule's literal withholds a summary only where a name starts (D63(3)), so 'mysecrets' is not withheld under Read(./secrets/**). cannot-do §5 and security.md both say 'where a name starts'. The error over-states what is withheld; nothing leaks.
  - Evidence: cannot-do.md:789-790 reads 'free text that only mentions a Read rule's literal where a name starts'. security.md:96 reads 'where a name starts, no Read deny or ask rule's literal'. D63(3) reads 'at a name boundary'.
  - Fix: Add 'where a name starts' to both sentences.

## fix:docs:r2: status `done`, head `b1b246c8`

### Summary

Fix round 2 is done, and both of the verifier's minor findings are fixed. The work is on closeout/w22-docs in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w22-docs, HEAD b1b246c8. The tree is clean, nothing is pushed and no product code changed.

**Finding 1: subject over 64 characters.** I reworded 3eb54e36 to 3eb54e36, "docs(release): tie the unrecorded-glob limit to structured globs" (64 characters). Its tree is identical to 3eb54e36's and its body and Refs footer are unchanged. I checked the other subjects in the range: 51, 61 and 62 characters. None of the four commits carries an attribution trailer, and all have the "Refs: V6-VERIFY, C6.1" footer. The hook's own gap is in tools/devtool/checkcommitmsg.go, which is not my file; it is under open issues.

**Finding 2: three pages cite docs/security.md §8 for the macOS limit, which security.md does not state.** I did not point the pages at the ledger, because this tree's copy of plans/V6-CLOSEOUT-CHECKLIST.md stops at D57. Instead I added a §5 entry to docs/cannot-do.md (my file): "On macOS, a case-sensitive volume is treated as case-insensitive". It gives the Limit, Why, What Qompack does instead and Recorded at, checked against the code: internal/hostperm/policy.go sets fold for windows and darwin, and internal/paths/norm.go's DefaultFold does the same. The release notes' routing paragraph now lists only the Windows directory-sync limit as being in security.md §8. The CHANGELOG bullet now cites `docs/cannot-do.md` §5, and the docs/release.md capability row links to the new cannot-do anchor.

**Class fix (D66(b)).** The new row, TestReleasePagesRouteEachLimitToAPageThatStatesIt in test/docs/limit_routes_test.go, covers all 22 limits the release notes list, not only the macOS one. It works out where each of three places sends a limit, and fails unless each document it is sent to has one block holding that limit's phrases:
- the release notes' routing paragraph (each named exception's own documents, otherwise cannot-do);
- the CHANGELOG's Known limits, clause by clause, falling back to a bullet's "each documented in" lead;
- the last cell of the docs/release.md capability table.

A route to the close-out ledger must name its decision. The row also fails if docs/release.md or the CHANGELOG yields no route, or if a release-notes limit has no bullet or no document.

All checks pass at b1b246c8.

### Commits

- c1217a1e docs(release): describe the D63 whitelist and owed C5.2 night
- 3b14b9d0 test(guards): keep the release interim markers live
- 3eb54e36 docs(release): tie the unrecorded-glob limit to structured globs
- b1b246c8 docs(release): route each known limit to a page that states it

### Findings resolution

- **fixed**: FIX ROUND 2 (minor): commit 3eb54e36's whole subject is 67 characters, over the brief's limit of 64; the commit-msg hook counts only the text after 'type(scope): '
  - Reworded with an amend (3eb54e36 was HEAD, so only this seat's own branch changed) to 3eb54e36, 'docs(release): tie the unrecorded-glob limit to structured globs' (64 characters). `git diff --stat 3eb54e36 3eb54e36` is empty, so the tree is identical; body and footer are unchanged. Red first: `git log --format=%s 2bf29705..HEAD | awk 'length>64'` printed the 67-character subject at 3eb54e36 and prints nothing at b1b246c8 (subject lengths 61, 51, 64, 62). Class sweep: all four seat commits are within 64 characters, carry 'Refs: V6-VERIFY, C6.1', and have no attribution trailers. The hook's undercount is in tools/devtool/checkcommitmsg.go:21, which is not my file; it is listed under open issues.
- **fixed**: FIX ROUND 2 (minor): release-notes v0.3.0.md:130-131, CHANGELOG.md's macOS Known limits bullet and docs/release.md:271 cite docs/security.md (§8) for D67(m)'s macOS limit, which security.md does not state
  - Fixed in b1b246c8. docs/cannot-do.md §5 gains the entry 'On macOS, a case-sensitive volume is treated as case-insensitive'. It states that paths are compared and keyed, and the Read rules' patterns matched, without letter case; that two files on a case-sensitive APFS volume are treated as one; and that the Read rules err toward refusing. It cites D67(m), internal/hostperm/policy.go (New, fold) and internal/paths/norm.go (DefaultFold). The three pages now send the limit there: the release notes drop it from the security.md exception, the CHANGELOG cites `docs/cannot-do.md` §5, and release.md links cannot-do.md#on-macos-a-case-sensitive-volume-is-treated-as-case-insensitive. Red-first row, at the class level: TestReleasePagesRouteEachLimitToAPageThatStatesIt (test/docs/limit_routes_test.go) checks the route of every limit the release notes list (22) on all three pages. With the pages at 3eb54e36 it failed with exactly the three security.md macOS routes. With the pages at base 2bf29705 it failed because six limits had no release-notes bullet. It is green at b1b246c8. Writing it also showed that four probes were too narrow (host order, a PreCompact behind a replay, the files view, evolution entries). I widened those to phrases the routed documents actually use; no page was wrong for them.

### Tests

- `go test -p 2 -count=1 -run '^TestReleasePagesRouteEachLimitToAPageThatStatesIt$' ./test/docs/ (pages at 3eb54e36)`: FAIL as intended (red first): release notes, release.md and CHANGELOG each route 'macOS case-insensitive volume' to docs/security.md, which holds no block stating it
- `go test -p 2 -count=1 -run '^TestReleasePagesRouteEachLimitToAPageThatStatesIt$' ./test/docs/ (CHANGELOG, release notes, release.md and cannot-do checked out at base 2bf29705)`: FAIL as intended: no Known limits bullet for the whitelist, macOS, recorded-corpus tier, quiet live session, second daemon or cross-session close
- `git log --format=%s 2bf29705..HEAD | awk 'length>64'`: printed the 67-character subject at 3eb54e36; prints nothing at b1b246c8
- `go test -p 2 -count=1 ./test/docs/...`: ok (b1b246c8 content)
- `go test -p 2 -count=1 ./test/guards/...`: ok, 101.8 s (b1b246c8 content)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool fmt-check`: exit 0 (after the pinned gofumpt -w on the new test file only)
- `go vet ./test/docs/`: exit 0

### Criterion changes

- None. The new row adds checks and loosens no existing assertion. Four of its own probe phrases were widened while it was being written, before it was committed (host order, a PreCompact behind a replay, the files view, evolution entries), because the first versions matched no wording the routed documents use for limits they do state. No page was changed to satisfy them.

### Open issues

- tools/devtool/checkcommitmsg.go:21 (subjectRE `: .{1,64}$`) counts only the text after 'type(scope): ', so a whole subject over 64 characters gets past the hook. This is not my file. Its owner should bound the whole subject line to 64 characters and add a row for a subject of 65 or more.
- D67(m) says the macOS limit is stated in docs/security.md and ADR 0011, and neither states it at HEAD in any wave-22 worktree. The release pages no longer rely on that: they point at cannot-do §5. If security.md §8 later states the limit, the new row will also accept a route to it.
- This tree's copy of plans/V6-CLOSEOUT-CHECKLIST.md stops at D57, while the release pages cite D58 to D67 and send D67(g) to 'the close-out ledger'. The new row therefore checks a ledger route only for a named decision, not for the ledger's text. The route holds once verify/v6's ledger is merged into the release tree.
- Carried from earlier rounds: docs/security.md:152's unrecorded-glob sentence lacks the word 'structured' (not my file); the ADR 0011 §23 D62(f) sentence (not my file); the 'piece by piece' sentences, which need a recheck against the rehydrate seat's #26 fix; and the state.bin wording of the disabled-daemon CHANGELOG entry, which waits for D67(c) to merge.

### Needs owner

- security.md and ADR 0011 owners: add D67(m)'s macOS case-insensitive-volume statement to docs/security.md §8 and ADR 0011, as D67(m) requires.
- Coordinator: once the rehydrate seat's #26 fix merges, confirm that the 'piece by piece' sentences on the release pages match it.
- Coordinator: fill the two Known issues sections under D66(d), and remove their interim sentence and its releaseInterimMarkers entries in release.md §1 step 2's docs-only commit.
- devtool owner: make the commit-msg hook bound the whole subject line (see open issues).


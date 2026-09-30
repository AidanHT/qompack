# Wave 15 workstream rehydrate

Branch `closeout/w15-rehydrate`. Workflow `wf_0a7ad63f-671`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `644776d9497042c02c7bcd3b47f6f992a898665d`

### Root cause

Item 3: tier 1 was a prefix per item, not one prefix over the ADR 0011 §21.2 admission order, and the hard-cap loop evicted the literal tail instead of reversing admission. Item 1: checkpoint Latest stepped over a checkpoint that failed verification and returned its parent with no record of the refusal, so the payload, state and LOUD.log presented the parent as current. Item 2: evolution holds every later prompt and was funded only by item 2's fixed 10% share, while unused room flowed to item 7 and was never offered back to evolution. Item 4: section 2 rendered its units in admission order, original first. Item 5: section 6 printed recorded paths and tool summaries verbatim, with no containment or host-rule check. Item 6: Build logged the tier-1 overflow Loud on every build and had no per-session memory.

### Summary

w15-rehydrate (D49 + D50 rehydrate items). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-rehydrate, branch closeout/w15-rehydrate, off closeout/integration a38a2b6d. All six items are fixed and committed, one commit per item. The tree is clean. The temporary diagnostic internal/rehydrate/zz_diag_w15_test.go was deleted and never committed. No background processes are left running. The Linux container was not used.

ITEM 3: F-C4-UAT05-3, tier-1 eviction order (commit 3585a23e). I reviewed the earlier seats' uncommitted work and kept it.
- Root cause: ADR 0011 §21.1-2 admits tier 1 as ONE prefix in the order retrieval line, invariants, item 2. Build applied that prefix per ITEM instead. So when the retrieval line did not fit, the next item's smaller record still got in. At 150 tokens the payload kept the pin and lost section 8; at 160 it did the reverse.
- Fix: tier 1 is now one prefix over tier1Admission. The first record the budget cannot hold closes it. Later tier-1 records, every share, the skill index and min-fill are then refused and named. A record no payload could hold at all is passed over without closing tier 1.
- The hard-cap loop now first cuts section 7 to its floor. It then evicts in the reverse of admission order: shares and skill index, then item 2, invariants, retrieval line, and item 7 last.
- ADR vs code: ADR 0011 §18's text ("last non-tier-1 item, tail fallback") disagrees with §21.2's admission order. The code now follows §21.2. The ADR prose should be amended (docs seat).
- Tests: TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets, TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine, TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1 and TestEvictIndex_EvictsInReverseAdmissionOrder. All four were RED on the base, confirmed by temporarily restoring the base budget.go and build.go.

ITEM 1: F-C4-UAT03-1, silent checkpoint fallback (commit 1351e787).
- Root cause: fileReader.Latest stepped over a checkpoint that failed verification and returned its parent. The caller had no way to tell, so the service presented 0001 as current.
- checkpoint.Ref gains Refused: the newer checkpoints Latest refused, newest first. It is also returned beside ErrNotFound when nothing verifies.
- Build then:
  - writes "checkpoint 0001 (rolled back from 0002)" in the header;
  - leads section 7 and the drop report with a checkpoint_fallback entry: what was refused, what the payload was rebuilt from, what may be missing, and how to restore (recall/timeline, qompack fsck, qompack backup restore);
  - sets Degraded, with a new DegradedReason field that the state file records as degraded_reason.
- The daemon Louds "rehydrate: newest checkpoint refused; rolled back to 0001" (or "to no checkpoint").
- Tests:
  - TestLatestNamesTheCheckpointsItRefused and TestLatestNamesTheRefusalsWhenNothingVerifies (checkpoint package; RED only as a compile failure);
  - TestBuild_CheckpointFallbackIsNamed, TestBuild_CheckpointFallbackSurvivesATinyBudget, TestBuild_FallbackToNoCheckpointIsNamed and TestBuild_NoFallbackNoEntry (rehydrate package);
  - TestService_CheckpointFallbackIsNeverSilent and TestService_CheckpointFallbackToNothingIsNamed (daemon). These use the real reader with one byte of 0002 flipped, and were RED as behaviour.

ITEM 2: F-C4-UAT06-1 and F-C4-UAT06-3, the correction pushed out of section 2 (commit 9d18692d, refined in 79394e38).
- Change: tier1Units(ItemUserIntent) is now the original plus the NEWEST restatement, admitted ahead of the share. The share funds the older entries.
- Unused room: after every share, the skill index and a measurement of item 7, the remaining room goes to evolution newest first (Build step 9a). Measuring item 7 first keeps the payload monotone in the budget; holding back only item 7's reserve broke PropBuild_MonotoneInBudget.
- Whole records or a named drop (D5), within D46's ceilings.
- Older entries never render without the newest above them. A newest entry too large for any payload takes them out with it, named. The property test found this at 959 checks.
- The hard-cap loop takes item 2 apart one record at a time.
- Tests: TestBuild_UnusedRoomGoesToEvolutionNewestFirst and TestBuild_NewestRestatementIsAdmittedAheadOfTheShare (both RED before), and TestBuild_OlderDeltasNeverShowWithoutTheNewest (RED with the item-2 commit's build.go).

ITEM 4: D50, UAT-05 read literally (commit 79394e38).
- Section 2 now renders the evolution newest first, then the original, whole, after the label "Original:".
- The label is part of the original's unit, so the character pricing stays exact. It is added only when there is evolution.
- The label is one word on purpose. The frozen full fixture's 6a section fits its share with fewer than 13 characters to spare, and "Original request:" dropped that section at the max budget. It sits under the unchanged heading "## 2. Original user intent". I kept that heading so the e2e prefix assertions still hold.
- Test: TestBuild_Section2RendersTheCorrectionAboveTheOriginal (RED before).

ITEM 5: C4.6, UAT-12 F4 pointer paths (commit 8536a13f).
- Rule: a file pointer is shown by hash only when its path is outside the project (an absolute path, or one that climbs out with ..), or when the host's current Read deny or ask rules refuse it. Its drop entry then reads "expand(hash=...)".
- A tool pointer's summary is withheld when any path-like piece in it (a path, command-line arguments, or JSON, including escaped backslashes) is refused.
- If the rules cannot be established, every path is withheld, as re_read does.
- Wiring: new rehydrate.HostPaths in Deps. The new daemon Options.HostPolicy is passed through WireRehydrator. When nil it defaults to hostperm.New for the project, the same default internal/mcp uses.
- Tests:
  - TestBuild_PointersNeverShowAWithheldPath, TestBuild_DroppedPointersNeverShowAWithheldPath and TestBuild_UnavailableHostRulesWithholdEveryPath (all RED before);
  - TestRehydrateHostPaths_RefusesWhatTheHostDenies, TestRehydrateHostPaths_UnreadableRulesFailClosed and TestWireRehydrator_InstallsTheHostPathRules (daemon, hermetic policy).

ITEM 6: D50, UAT-04's 15 identical lines (commit 644776d9).
- Build returns Result.Tier1Overflow. When Request.Tier1OverflowReported is set, it logs the tier-1 line at Info instead of Loud. The service tracks that per session.
- Payloads and drop reports still name the overflow every time.
- Tests: TestService_Tier1OverflowIsLoudOncePerSession (RED: 3 Louds) and TestBuild_ReportedTier1OverflowIsNotLoudAgain.

Other seats: nothing here was theirs. Decisions carried across checkpoints and fork inheritance belong to the ledger seat and were not touched.

### Commits

- 3585a23e fix(rehydrate): admit and evict tier 1 in adr 0011 order
- 1351e787 fix(rehydrate): name a checkpoint fallback instead of hiding it
- 9d18692d fix(rehydrate): keep the correction in force in section 2
- 79394e38 fix(rehydrate): render the correction above the original request
- 8536a13f fix(rehydrate): withhold host-denied and out-of-project pointer paths
- 270e28d3 test(daemon): expect the fork's prompt above the parent's original
- 644776d9 fix(rehydrate): log a named tier-1 overflow loud once per session

### Tests

- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ ./internal/rehydrate/ ./internal/checkpoint/ (final, at HEAD 644776d9)` — ok for all three packages (daemon 368.8s, rehydrate 2.5s, checkpoint 112.3s)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (earlier full run, before 270e28d3)` — one failure, TestCompactRehydration_ForkShowsTheParentsOriginal, an order assertion made stale by item 4; fixed in 270e28d3 and green in the final run
- `go test -p 2 -count=1 -run 'TestBuild_MonotoneInBudget|TestBuild_NeverExceedsTheHostCeiling' ./internal/rehydrate/ -rapid.checks=3000` — ok (32.7s), after the older-deltas fix; the earlier run of the same command failed after 959 checks and led to TestBuild_OlderDeltasNeverShowWithoutTheNewest
- `go test -p 2 -count=1 -run '^(TestV4_PreCompactToCheckpointToRehydrateRoundTrip|TestV5_PreCompactToRehydrateToDroppedRoundTrip)$' ./test/e2e/` — ok (25.6s), after items 4-6
- `go test -p 2 -count=1 -run '^TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly$' ./test/integration/` — ok (6.2s)
- `go test -p 2 -count=1 -run '^(TestDaemonLedgerAnswersAForkWithItsParentsEliminations|TestLedgerAccessorsAreSafeAgainstTheLazyOpen|TestOpeningLedgerOpensOnFirstUseAndOnlyThere)$' ./internal/cli/` — ok
- `go test -p 2 -count=1 ./test/docs/` — ok (4.5s)
- `go test -p 2 -count=1 -run 'TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets|TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine|TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1|TestEvictIndex_EvictsInReverseAdmissionOrder' ./internal/rehydrate/ (with base budget.go and build.go restored)` — FAIL as expected (RED on base); green with the fix
- `go test -p 2 -count=1 -run 'TestService_CheckpointFallbackIsNeverSilent|TestService_CheckpointFallbackToNothingIsNamed' ./internal/daemon/ (before the daemon change)` — FAIL as expected (RED); ok after
- `go test -p 2 -count=1 -run 'TestService_Tier1OverflowIsLoudOncePerSession' ./internal/daemon/ (before the fix)` — FAIL: 3 Louds (RED); ok after
- `go test -p 2 -count=1 -run 'TestBuild_PointersNeverShowAWithheldPath|TestBuild_DroppedPointersNeverShowAWithheldPath|TestBuild_UnavailableHostRulesWithholdEveryPath' ./internal/rehydrate/ (items.go at its pre-fix state)` — FAIL (RED: private/deny.txt leaked into the payload and the drop report); ok with the fix
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0; all eight sub-checks PASS at HEAD
- `go run ./tools/devtool fmt-check; go vet ./internal/rehydrate/ ./internal/daemon/ ./internal/checkpoint/ (Windows and GOOS=linux)` — clean

### Criterion changes

- Degraded, full-12k, full-8k and token-bound rehydration goldens and state.json re-recorded for the D49 and D50 behaviour (evolution first, 'Original:' label, newest restatement in tier 1, unused room given to evolution)
- TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted: budget 6000 -> 5000, and the row now asserts that a whole section was evicted
- TestBuild_MinFillReadmitsUnits/host_ceiling_binds_first: compares re-admitted decisions instead of evolution deltas
- TestBuild_LatestEvolutionSurvivesTruncation: 'second-newest dropped' generalised to a newest-first prefix check at every index
- TestTier1Units_TakesOnlyTheVerbatimOriginal renamed and re-pinned as TestTier1Units_TakesTheOriginalAndTheNewestRestatement
- TestBuild_ForkBlockMatchesUAT06, TestCompactRehydration_ForkShowsTheParentsOriginal and TestUserIntent_ForkKeepsTheParentsOriginal: evolution-first order and label
- PropBuild_MonotoneInBudget item-2 nesting and requireOriginalWholeOrNamed original location follow the new render order

### Open issues

- The prose of ADR 0011 §18 (hard-cap loop: last non-tier-1 item, tail fallback) and §19 (only the verbatim original is tier 1) no longer matches the code. The code now follows §21.2's admission order, admits the newest restatement with the original (D49), and renders the evolution above the original (D50). This needs an ADR amendment, which is docs-seat scope.
- The section-2 label is 'Original:' rather than 'Original request:'. The frozen full-12k/full-8k fixture's 6a (path-rule) section fits its share with fewer than 13 characters to spare, and the longer label drops that section at the max budget (TestBuild_ItemOrderInPayload). The heading still says 'Original user intent'. If the audit wants the longer wording, the heading or share arithmetic has to change with it.
- Item 5's default host policy (nil Options.HostPolicy) is hostperm.New for the project, the same production default internal/mcp uses. Daemon-level tests that compact through WireRehydrator without a hermetic policy would read the machine's managed-settings sources, though never ~/.claude, since TestMain isolates HOME. internal/mcp has the same pattern.
- Item 5 decides which parts of a tool pointer's summary are paths heuristically: a piece that is rooted or contains a separator or a dot. A denied path spelled without a separator or dot (for example a bare file name with no extension) is judged only if a host rule matches it relative to the project root.
- The item 5 commit footer reads 'Refs: V6-VERIFY, C4.6' (the privacy criterion), not C4.3. The commit-msg hook accepted it.
- The per-session set behind item 6 (rehydrateService.tier1Loud) grows by one entry per session that overflows. It has no cap, and a daemon serves one project until it idles out.
- The candidate 5 live re-run is still owed for UAT-03, 05, 06 and 12 (section 6) to confirm these fixes in a real session.

### Needs the owner

- No new budget or bound constants were introduced. The only new constants are strings: checkpoint_fallback (drop kind), 'Original:' (label) and the two withheld-path phrases.
- Please approve the criterion changes below; each has its rationale written in the row itself. (a) testdata/golden/rehydrate/{degraded,full-12k,full-8k,token-bound}.txt and state.json were re-recorded after reading each diff. They gain the D49 newest restatement and unused-room deltas, and the D50 evolution-first order with the 'Original:' label. degraded.txt lost one skill-index line and one section-7 line to the newest restatement. (b) TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted: budget 6000 -> 5000. At 6000, cutting section 7 alone absorbed the overrun, so the row no longer tested eviction. At 5000 a whole section is evicted, and the row now asserts that eviction happened. (c) TestBuild_MinFillReadmitsUnits host_ceiling_binds_first: the 'named budget carries fewer deltas' comparison became impossible under D49's unused-room rule, so it now compares re-admitted decisions on a many-decision fixture. (d) TestBuild_LatestEvolutionSurvivesTruncation: the fixed 'second-newest is dropped' check became a newest-first prefix check at every index. (e) TestTier1Units_TakesOnlyTheVerbatimOriginal is renamed TestTier1Units_TakesTheOriginalAndTheNewestRestatement. (f) The fork order in TestBuild_ForkBlockMatchesUAT06 and TestCompactRehydration_ForkShowsTheParentsOriginal, and TestUserIntent_ForkKeepsTheParentsOriginal's unit-text prefix, now expect the label and the evolution-first order. (g) The original-location check in the host-ceiling property and the item-2 nesting in PropBuild_MonotoneInBudget follow the new render order; what they claim (the original is whole or named; admitted records nest) is unchanged.

## Independent review

### review:rehydrate: needs-fixes

- **major** `internal/rehydrate/build.go:147-150, 212-214, 234-238, 252, 266 (the `incomplete` closure); internal/rehydrate/budget.go:237-240 (fillTier1)` — When any tier-1 record that could fit an empty payload is refused, tier 1 is closed. The closure then also abandons every share section (3, 4, 5, 6, 6a), the skill index, min-fill and the step-9a evolution room. Neither ADR 0011 (§7 and §21.1 describe a prefix within an item's records) nor D49 ('eviction follows ADR 0011's tier-1 order') asks for this. Item 2 now adds the newest prompt to tier 1, and it is admitted last, so a long latest prompt that does not fit after the original wipes sections 3-6 even when thousands of characters are unused. This is a content regression at the default budget. It is also non-monotone in prompt size: a slightly larger prompt that no payload could hold is passed over, and then the shares fill.
  - Evidence: I ran a scratch copy (git archive of HEAD and of the base a38a2b6d) with a probe test at maxBudget(), using an original of about 4,500 characters, a newest evolution prompt of about 4,500 characters, one elimination, one decision, current work and one file pointer. HEAD: 5,781 of 9,400 characters, sections [invariants user_intent drop_report affordance]. Eliminations, decisions, current_work and pointers are all refused. Base: 5,836 characters, sections [invariants user_intent eliminations decisions current_work pointers drop_report affordance]. With an original of about 5,000 and a newest of about 4,200, the result is the same (HEAD loses sections 3-6). TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets encodes the over-broad rule ('while tier 1 is incomplete, nothing filled from a share is present'). TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1 covers only the unrepresentable case, so no test catches this.
  - Fix: Keep the closure inside tier 1, so a refused tier-1 record refuses the tier-1 records after it in tier1Admission order. Do not abandon the shares, skill index, min-fill or 9a unless the refused record is the retrieval line (item 8): pointers are unusable without it, and that is the UAT-05 150-token case the closure was written for. An alternative is to exempt item 2's newest restatement from closing tier 1, since it is the last tier-1 record and closing on it only kills shares. Add a regression row: a long original plus a long newest prompt at maxBudget must still carry sections 3-6, within the ceiling. Narrow the 'no share while incomplete' assertion in TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets to 'while the retrieval line is out'.
- **minor** `internal/rehydrate/pathgate.go:46-72, 87-100; internal/daemon/rehydrate_service.go:757-785` — Item 5's containment check misses paths that point at the home directory or use environment variables. `~/.ssh/id_rsa`, `$HOME/.aws/credentials` and `%USERPROFILE%\.aws\credentials` are judged 'inside the project' because they are not rooted and do not start with `..`. The daemon's host adapter then joins them under the project root (`<root>/~/.ssh/id_rsa`), so a Read deny rule on `~/.ssh/**` never matches. A Bash tool pointer summary such as `cat ~/.ssh/id_rsa` is shown verbatim in section 6, although D50 says those pointers never show a path outside the project.
  - Evidence: Scratch probe of pathJudge{root: C:\proj, host: true, refuses: allow-all}. summaryWithheld("cat ~/.ssh/id_rsa") = false, summaryWithheld("cat $HOME/.aws/credentials") = false and summaryWithheld("type %USERPROFILE%\\.aws\\credentials") = false. For comparison, "cat /etc/passwd" = true and "cat ../other/x" = true. withheld("~/.ssh/id_rsa") = false as a file pointer too.
  - Fix: In absLike (or in inside), treat a leading `~`, `~user`, `$VAR`/`${VAR}` or `%VAR%` segment as rooted, and so outside the project, which withholds it. Add these spellings to TestBuild_PointersNeverShowAWithheldPath.
- **minor** `docs/adr/0011-rehydration-budget-and-item-order.md §18 (lines 325-347), §19 (349-364), §21.1-2` — The ADR that governs this code now contradicts it. §18 says the hard-cap loop evicts 'the last non-tier-1 item, falling back to the last item', and cites the old degraded golden. §19 says item 2's tier-1 admission is 'the verbatim original only'. The code now evicts in reverse admission order (item 7 last), admits the newest restatement in tier 1, gives unused room to evolution (step 9a), and renders the evolution above the original. The cross-item closure of the shares (see the major finding) is not written down anywhere. The implementer listed this as an open issue, but no amendment exists on any branch.
  - Evidence: `sed -n 325,366p docs/adr/0011-...md` still holds the §18 and §19 text unchanged. The code comments in budget.go and build.go cite '§21.2' for an order and closure that §21 does not state.
  - Fix: Before this branch merges, route an ADR 0011 amendment (§22, dated, citing D49 and D50) to the docs seat. It should record: tier 1 as one prefix over tier1Admission; reverse-admission hard-cap eviction with item 7 last; item 2's tier-1 slice (the original plus the newest restatement); step 9a; section 2's render order and the 'Original:' label; and whatever share-closure rule survives the major finding. Mark §18's golden description and §19 as superseded.
- **nit** `internal/rehydrate/build.go:347-357, 394-397 (dropReportToFloor in the hard-cap loop)` — The hard-cap loop first cuts section 7 to its counted tail, and after each eviction re-renders it with onlyIfSmaller=false. Either step removes the checkpoint_fallback line from section 7. D49 requires section 7 to name the fallback. The header ('rolled back from') and Result.Dropped keep it. TestBuild_CheckpointFallbackSurvivesATinyBudget checks only Result.Dropped and the sort order, never res.Text.
  - Evidence: dropReportToFloor replaces items[i].Text with itemText(ItemDropReport, …, []string{tail.text}), so no entry line survives. kindRank puts checkpoint_fallback first only in the fill path.
  - Fix: Make the floor keep the first line when it is a checkpoint_fallback or tier-1 overflow entry and the pair fits (or price it into the floor when fellBack(r)). Assert strings.Contains(res.Text, dropLine(fallbackEntry)) in the tiny-budget row under the separator-charging estimator.
- **nit** `internal/checkpoint/reader.go:258-268 (refusedAfter)` — Every refused checkpoint newer than the one returned is counted, from any session. A corrupt checkpoint written by session B therefore makes session A's payload say 'checkpoint 0005 (rolled back from 0006)', that 0006 'does not describe this rehydration', and that later material 'may be missing'. The rehydration is marked degraded and a rollback is logged Loud on every compaction, although A's own newest checkpoint verified. This fails safe, but the payload states it as fact.
  - Evidence: paths.ManifestEntry has no Session field, and refusedAfter filters only by seq.
  - Fix: Soften the detail wording when the refused checkpoint's session is unknown ('a newer project checkpoint 0006 did not verify and may have been this session's'). Or, where the refused bytes still parse, read their Session field for attribution only, never for content.
- **nit** `internal/daemon/rehydrate_service.go:501-526 (recordUndelivered)` — The undelivered-state path writes Degraded: true but drops res.DegradedReason. A fallback rehydration the route could not deliver in time loses the 'rolled back to' reason in the state file.
  - Evidence: The State literal in recordUndelivered has no DegradedReason, while record() sets it.
  - Fix: Set DegradedReason: res.DegradedReason in recordUndelivered.
- **nit** `internal/rehydrate/verify_test.go:349-362` — intentNests was inserted between linesArePrefix's doc comment and its func. The comment 'linesArePrefix reports whether…' now sits above intentNests, and linesArePrefix has no doc comment of its own.
  - Evidence: The diff shows the new intentNests block placed after the '// linesArePrefix reports whether small's unit lines…' comment lines.
  - Fix: Move the linesArePrefix comment back directly above func linesArePrefix.
- **nit** `commit 270e28d3 (test(daemon): expect the fork's prompt above the parent's original)` — The commit has no Refs footer, unlike the other six commits on this branch (all 'Refs: V6-VERIFY, C4.x').
  - Evidence: `git log --format='%h%n%b' a38a2b6d..HEAD` shows no Refs line for 270e28d3.
  - Fix: When the branch is next rewritten or squashed for the merge, add 'Refs: V6-VERIFY, C4.3' to 270e28d3, or fold it into 79394e38, whose behaviour change it follows.

## Fix seat (review resolution) — status `done`, head `f3855ae6`

### Root cause

Review major: build.go used one `incomplete` flag both to close the tier-1 prefix and to abandon every share, the skill index, min-fill and step 9a. Item 2's newest restatement is admitted last in tier 1, so refusing it emptied sections 3-6. The fix separates `unanchored` (retrieval line refused, which closes the shares) from `incomplete` (which only blocks re-admitting item 2). Review minor 1: absLike in pathgate.go did not treat `~`, `$VAR`, `${VAR}` or `%VAR%` prefixes as rooted, so inside() classified them as project-relative. Review minor 2: ADR 0011 §18 and §19 were never amended for D49 and D50; §22 now records them.

### Summary

Fix seat for w15-rehydrate (D49 items 1-3 and D50 items 4-6). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-rehydrate, branch closeout/w15-rehydrate, base a38a2b6d, head f3855ae6. The tree is clean. The temporary diagnostic internal/rehydrate/zz_diag_w15_test.go is not in the tree and was never committed. I checked all three reviewer findings myself; all three were correct and all three are fixed. Each code fix has a regression test that failed before the fix. I started no background processes and no load generators, and did not use the Linux container.

## Review resolution
1. MAJOR: a tier-1 refusal abandoned every share, the skill index, min-fill and step 9a. CONFIRMED and FIXED in 6bf03c85.
   - Reproduction: new test TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom uses the UAT-05 fixture with a whole original of about 4,500 characters and a newest restatement of about 4,500 characters, at maxBudget. Before the fix it failed with "section eliminations was refused although the retrieval line is in". The payload held only sections 1, 2, 7 and 8, and named the elimination, the decision, current work and all five pointers as "did not fit".
   - Root cause: in build.go one `incomplete` flag controlled both the tier-1 prefix and whether the shares, skill index, min-fill and step 9a were abandoned.
   - Fix: a new `unanchored` flag is true only when the retrieval line (item 8) was refused. Only that flag abandons the shares, the skill index and min-fill. `incomplete` now only keeps item 2 from being re-admitted:
     - its older deltas are abandoned;
     - min-fill runs over shareOrder without ItemUserIntent (new helper withoutKind in budget.go);
     - step 9a is skipped.
     This is needed because item 2's pending units start with the refused tier-1 records, so min-fill would otherwise let them skip ahead of a refused invariant.
   - The tier-1 prefix and the UAT-05 case at 150 tokens are unchanged. TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine and the tier-1 prefix and monotonicity checks in TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets still pass. No golden changed.
2. MINOR: paths spelled with `~`, `$VAR`, `${VAR}` or `%VAR%` were treated as inside the project. CONFIRMED and FIXED in f244d941.
   - Reproduction: new test TestBuild_PointersNeverShowAHomeOrVariablePath failed before the fix with "the payload shows a withheld path".
   - Fix in pathgate.go: absLike now also matches homeOrVarRoot, which is `~` either alone or followed by a user name and a separator, `$VAR`, `${VAR}`, or `%VAR%`. Such a path counts as rooted, inside() finds it outside the project, and it points by hash only. The containment check runs before the host-rules check, so the daemon adapter that joins paths under the root never sees these paths. No daemon change was needed.
   - New test TestAbsLike_HomeAndVariableSpellingsAreRooted pins the edges. Project-relative names such as `~$report.docx`, `notes~/draft.md`, `$1`, `a$HOME/b` and `100%/x` stay project-relative.
3. MINOR: ADR 0011 contradicted the code. CONFIRMED and FIXED in f3855ae6.
   - Added §22, "Amendment (2026-09-30, owner decisions D49 and D50)", with eight points: the tier-1 prefix and exactly how far its closure reaches, as fixed under finding 1; item 2's tier-1 slice is the original plus the newest restatement; step 9a; hard-cap eviction in reverse admission order, with item 7 last; section 2 rendered evolution-first with the `Original:` label; the named checkpoint fallback; pointer privacy, including home and variable spellings; and the tier-1 overflow logged Loud once per session.
   - §18 and §19 now carry superseded notes, and the Status line is updated.
   - The budget.go and build.go comments now cite §22.1 instead of §21.1-2. The §21.2 citations were left as they are, because §21.2 does say the retrieval line is admitted first.
   - I wrote this in my own seat rather than handing it to the docs seat, because it records only this branch's behaviour. The coordinator may re-route that commit if the docs seat should own it.

## Commands and results (Windows; the machine was loaded by other seats; nothing failed on wall-clock time)
- `go test ./internal/rehydrate -run 'TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom' -count=1 -p 2`: FAIL before the fix, ok after.
- `go test ./internal/rehydrate -run 'TestBuild_PointersNeverShowAHomeOrVariablePath' -count=1 -p 2`: FAIL before the fix, ok after.
- `go test ./internal/rehydrate -run 'TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom|TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets|TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine|TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1' -count=1 -p 2`: ok.
- `go test ./internal/rehydrate -run 'TestBuild_PointersNeverShowAHomeOrVariablePath|TestBuild_PointersNeverShowAWithheldPath|TestBuild_DroppedPointersNeverShowAWithheldPath|TestBuild_UnavailableHostRulesWithholdEveryPath' -count=1 -p 2`: ok.
- `go test ./internal/rehydrate -run 'TestAbsLike_HomeAndVariableSpellingsAreRooted' -count=1 -p 2`: ok.
- `go test ./internal/rehydrate/... -count=1 -p 2` (full package, run at the final head): ok for rehydrate and rehydratetest, exit 0. All goldens unchanged by the fix seat.
- `go test ./internal/daemon -run 'TestService_|TestCompactRehydration_' -count=1 -p 2 -v`: 30 PASS, 0 FAIL. This is a focused run; the fix seat did not touch internal/daemon.
- `go test ./test/docs -count=1 -p 2`: ok.
- `go vet ./internal/rehydrate/` and `GOOS=linux go vet ./internal/rehydrate/`: clean. `GOOS=linux go test -c` of internal/rehydrate compiles.
- `go run ./tools/devtool fmt`: no changes.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all eight PASS, exit 0.

## Criterion change added by the fix seat (needs approval)
(h) TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets: "while tier 1 is incomplete, no share-filled section is present" is narrowed to "while the retrieval line is out, no share-filled section is present". "While tier 1 is incomplete, no evolution entry" is kept, as are the tier-1 prefix assertion and the rule that the prefix never shrinks as the budget grows.
- Rationale: the broad rule encoded the over-broad closure. Neither ADR 0011 §7 (a prefix within an item) nor D49 asks for it, and it emptied sections 3-6 of payloads that had room.
- The new TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom pins the correct behaviour.
- Consequence to approve: at budgets where the invariant or the original is refused but the retrieval line is in (for example 195 and 266 tokens with the shipped estimator), shares may now carry records. §6 has always allowed this and the base behaved this way; the base also had it.

### Commits

- 3585a23e fix(rehydrate): admit and evict tier 1 in adr 0011 order (implementer)
- 1351e787 fix(rehydrate): name a checkpoint fallback instead of hiding it (implementer)
- 9d18692d fix(rehydrate): keep the correction in force in section 2 (implementer)
- 79394e38 fix(rehydrate): render the correction above the original request (implementer)
- 8536a13f fix(rehydrate): withhold host-denied and out-of-project pointer paths (implementer)
- 270e28d3 test(daemon): expect the fork's prompt above the parent's original (implementer)
- 644776d9 fix(rehydrate): log a named tier-1 overflow loud once per session (implementer)
- 6bf03c85 fix(rehydrate): keep a tier-1 refusal from closing the shares (fix seat, review major)
- f244d941 fix(rehydrate): withhold home and variable-rooted pointer paths (fix seat, review minor 1)
- f3855ae6 docs(adr): amend adr 0011 for d49 and d50 (fix seat, review minor 2)

### Tests

- `go test ./internal/rehydrate -run 'TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom' -count=1 -p 2` — FAIL before 6bf03c85 (sections 3-6 refused with room); ok after
- `go test ./internal/rehydrate -run 'TestBuild_PointersNeverShowAHomeOrVariablePath' -count=1 -p 2` — FAIL before f244d941 (payload shows a withheld path); ok after
- `go test ./internal/rehydrate -run 'TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom|TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets|TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine|TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1' -count=1 -p 2` — ok
- `go test ./internal/rehydrate -run 'TestBuild_PointersNeverShowAHomeOrVariablePath|TestBuild_PointersNeverShowAWithheldPath|TestBuild_DroppedPointersNeverShowAWithheldPath|TestBuild_UnavailableHostRulesWithholdEveryPath' -count=1 -p 2` — ok
- `go test ./internal/rehydrate -run 'TestAbsLike_HomeAndVariableSpellingsAreRooted' -count=1 -p 2` — ok
- `go test ./internal/rehydrate/... -count=1 -p 2` — ok (rehydrate 3.0s, rehydratetest 1.9s), exit 0 at head f3855ae6; goldens unchanged by the fix seat
- `go test ./internal/daemon -run 'TestService_|TestCompactRehydration_' -count=1 -p 2 -v` — 30 PASS, 0 FAIL
- `go test ./test/docs -count=1 -p 2` — ok
- `go vet ./internal/rehydrate/ ; GOOS=linux go vet ./internal/rehydrate/` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all eight PASS, exit 0

### Criterion changes

- (a)-(g) implementer's criterion changes, carried unchanged; see needs_owner.
- (h) TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets: the share-filled-section assertion is narrowed from 'tier 1 incomplete' to 'retrieval line out'. The rationale is written in the test's doc comment and in needs_owner.

### Open issues

- Linux runtime evidence was not collected: the container is stopped per this seat's daytime limits. Only GOOS=linux vet and test compilation were run. The linux-nonroot-gate.sh run for this head is owed.
- Hot-path rows and the whole test/integration and test/e2e packages were not run, per this seat's limits. The coordinator's integrated run must cover them.
- ADR 0011 §22 was written in this seat, not the docs seat, because it records only this branch's behaviour. The coordinator may re-route commit f3855ae6 if the docs seat should own it.

### Needs the owner

- No new budget or bound constants were introduced, by the implementer or by the fix seat. The only new constants are strings: checkpoint_fallback (drop kind), 'Original:' (label), the two withheld-path phrases, and the fix seat's homeOrVarRoot pattern, a path-shape classifier that is not a bound.
- Please approve the implementer's criterion changes; each has its rationale in the test itself. (a) testdata/golden/rehydrate/{degraded,full-12k,full-8k,token-bound}.txt and state.json were re-recorded after reading each diff. They gain the D49 newest restatement and unused-room deltas, and the D50 evolution-first order with the 'Original:' label; degraded.txt lost one skill-index line and one section-7 line to the newest restatement. (b) TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted: budget 6000 -> 5000, so the row still exercises eviction, which it now asserts. (c) TestBuild_MinFillReadmitsUnits host_ceiling_binds_first now compares re-admitted decisions on a many-decision fixture. (d) TestBuild_LatestEvolutionSurvivesTruncation now checks a newest-first prefix at every index. (e) TestTier1Units_TakesOnlyTheVerbatimOriginal is renamed TestTier1Units_TakesTheOriginalAndTheNewestRestatement. (f) The fork-order expectations in TestBuild_ForkBlockMatchesUAT06, TestCompactRehydration_ForkShowsTheParentsOriginal and TestUserIntent_ForkKeepsTheParentsOriginal follow the label and the evolution-first order. (g) The original-location check in the host-ceiling property and the item-2 nesting in PropBuild_MonotoneInBudget follow the new render order; what they claim is unchanged.
- Please approve the fix seat's criterion change: (h) TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets now asserts 'no share-filled section while the retrieval line is out' instead of 'while tier 1 is incomplete'. The tier-1 prefix, the no-evolution-while-incomplete rule and the non-shrinking prefix checks are unchanged. The broad rule encoded the review-major regression (sections 3-6 emptied with room to spare), which neither ADR 0011 §7 nor D49 asks for. TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom pins the new behaviour. Consequence: when the invariant or the original is refused but the retrieval line is in, the shares may carry records, as ADR §6 allows and as the base did.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/rehydrate/pathgate.go:94 (summaryTokens) and :98-110 (summaryWithheld); homeOrVarRoot at :89-90` — Finding 2 is fixed for file pointers but not completely for tool-pointer summaries. summaryWithheld splits on `(` and `)` before absLike runs, so a `%VAR%` name that contains parentheses never reaches homeOrVarRoot as one token. `%ProgramFiles(x86)%\...` becomes `%ProgramFiles`, `x86` and `%\secret\cfg`. None of these counts as rooted, so the summary is shown verbatim. TestAbsLike_HomeAndVariableSpellingsAreRooted pins exactly this spelling as rooted (the regex allows `()` in the name), and ADR 0011 §22.7 says a variable-rooted path is never shown, so the test and the ADR claim coverage the summary path does not give. A flag with the path glued on (`-f~/.ssh/id_rsa`) also passes, because `~` is not at the start of its token.
  - Evidence: Scratch probe (git archive of HEAD f3855ae6) with pathJudge{root:`C:\proj`, host:true, refuses: allow-all}. summaryWithheld(`type "%ProgramFiles(x86)%\secret\cfg"`) = false. summaryWithheld(`type %ProgramFiles(x86)%\secret\cfg`) = false. summaryWithheld(`cat -f~/.ssh/id_rsa`) = false. For contrast, withheld(`%ProgramFiles(x86)%\secret\cfg`) as a file pointer = true, and `cat ~/.ssh/id_rsa`, `cat ${HOME}/...`, `cat "$HOME"/...`, `Get-Content $env:USERPROFILE\...` and the JSON `{"file_path":"~/.ssh/id_rsa"}` form are all withheld (true). Everything else checks out. Major fix 6bf03c85: the new test fails on 644776d9 (only sections 1, 2, 7 and 8 present) and passes at HEAD. Minor fix 3: ADR §22 matches the hard-cap loop and the admission code, and every test it cites exists. go test ./internal/rehydrate/... is ok, the focused daemon TestService_/TestCompactRehydration_ run is ok, and go vet is clean.
  - Fix: In summaryWithheld, check the raw summary for a `%NAME%` segment before tokenizing, so the parenthesised name stays whole. One way: match `(^|[\s"'`=,;|<>])%[A-Za-z_][A-Za-z0-9_()]*%` against the whole summary and withhold on a hit. Also consider treating a token that contains `~/`, `~\`, `$` or `%...%` after a leading flag prefix as rooted. Add `type "%ProgramFiles(x86)%\secret\cfg"` (and optionally `-f~/.ssh/id_rsa`) to the tool pointers in TestBuild_PointersNeverShowAHomeOrVariablePath.


## Coordinator follow-up

The verifier's remaining finding (summaryWithheld split `%ProgramFiles(x86)%` and missed a path glued to a flag) was fixed by the coordinator in `a127a938`: the whole summary is matched for a home- or variable-rooted segment before tokenising; both spellings were added to TestBuild_PointersNeverShowAHomeOrVariablePath, red before the fix, and `go test ./internal/rehydrate` passes. The re-recorded rehydrate goldens and the adjusted rows listed under needs_owner follow D49/D50's intended layout and are accepted under D33.

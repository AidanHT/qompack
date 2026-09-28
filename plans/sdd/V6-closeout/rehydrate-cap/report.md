# Rehydrate-cap workstream report — C1.14 (owner decision D5)

Branch `closeout/rehydrate-cap`. Workflow `wf_e1d0d082-a01`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `d1039b832ad7f75098e4d4ea2b4cf8a9a95cb7cb`

### Summary

C1.14 (owner decision D5) is done on closeout/rehydrate-cap, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-rehydrate. The branch has 4 commits on top of 32e1a37, and nothing was pushed or merged. Post-compaction rehydration now fits under the host's 10,000-character additionalContext cap, and one real-host session shows it arriving inline with an overflow pointer that resolves through the qompack MCP tools.

ROOT CAUSE
rehydrate.Build limited the payload only in tokens (maxTokens 12000, roughly 48,000 characters). Claude Code checks each hook field with `e.length<=s` against 1e4 (read from the installed 2.1.280 bundle; evidence/host-cap-unit.txt keeps the excerpt and the binary's sha256). The unit is UTF-16 code units, and exactly 10,000 is still delivered whole. Anything over is swapped for a file path and a 2,000-character preview. Measured before the fix:
- the frozen `full` fixture rendered 32,049 units;
- an end-to-end compact start over six path rules rendered 20,033 units;
- the hook client's Loud over-cap line fired (evidence/red-base-*.txt).

A second, older defect: item 7's lines carried no DropEntry, so they counted as fixed units. They were admitted all at once and then evicted by the hard-cap loop. The pre-D5 `degraded` golden therefore had no section 7 at all.

FIX (internal/rehydrate; hookio change limited to exporting the count)
- **Ceiling:** new hostcap.go. `HostContextCeilingChars = 9500` covers the whole compact field. `PayloadCeilingChars = 9400` is for Result.Text, leaving a 100-character reserve for the daemon's 62-character contract-probe line. A test ties that reserve to contract.RenderSentinel. The ceiling is a host constant, not configuration; the token budget still applies and can only bound lower.
- **Counting:** `hostChars` counts UTF-16 units, and a test ties it to the newly exported `hookio.HostChars`.
- **Admission in two dimensions:** every unit is priced in tokens and in exact host characters (units, section heading plus separator, wrapper). A unit is admitted only if it fits both, so the character bound is a guarantee.
- **Tier 1:** admitted record by record. The retrieval line goes first, then invariants, then the original prompt. The smallest possible item 7 (heading plus counted tail) is held back from the first admission. A record that does not fit becomes an explicit `tier1` overflow that names the record and its restore pointer; it is never cut.
- **Item 7:** filled as a prefix of lines plus `- … and N more; call dropped()`. Explicit overflows sort first.
- **Pointers in every omission's detail:** `re_read(path)`, `expand(tool_use_id=…)` for tool pointers and for the prompt's L0 record `prompt_<session>_0`, `Read <rule file>`, or `Read .qompack/checkpoints/NNNN.json (<field>)`.
- **Other changes:**
  - Fixed units are reserved inside their item's room.
  - Item 6b gets the unused share before item 7 does.
  - Min-fill respects both bounds.
  - A payload that would contain only item 7 is not emitted.
  - The hard-cap eviction loop also checks characters, as a fallback. The property test asserts it never fires on characters.

TESTS (each written to fail first)
- `TestBuild_HostCeiling_LargeSessionArrivesInline`: 32,049 units before, now ≤ 9,500. It checks priority order, whole records, pointers, and that section 7 accounts for every drop.
- Oversized-original tests for both the L0 and checkpoint-copy pointers.
- `PropBuild_NeverExceedsTheHostCeiling` (rapid; covers multibyte/astral text, one huge record, thousands of small records, pathological session ids and sequence numbers) and `FuzzBuild_HostCeiling` (137,642 execs in 60 s).
- `test/e2e TestE2E_SessionStartCompactFitsTheHostCap`, through the real binary and daemon: 20,033 units before, 7,378 after, with no Loud line.

EXISTING TESTS CHANGED, WITH REASON
- Builder drop-detail assertions (evolution, current_work, pointers, rules): D5 requires each omission to carry an actionable pointer.
- `TestBuild_MinFillReadmitsUnits`: the original assertion is kept unchanged under a 1-token-per-byte estimator, where minTokens binds first. A second case covers the baseline estimator, where the ceiling binds first. Byte size no longer separates the named and unset calls there, so that case asserts more re-admitted deltas instead.
- Goldens were re-recorded through a new `-update` generator (the same convention as mcp, dag and commands), after reading each diff:
  - full-12k and full-8k are now byte-identical (9,300 units);
  - a new `token-bound` case covers the token cut;
  - `degraded` now keeps a truncated section 7;
  - `no-checkpoint` gains one skill;
  - `state.json` updated.
- The config schema golden's `-update` refuses on this Windows host because of platform-specific defaults. I applied its single changed line by hand; `TestJSONSchema_Golden` verifies every other byte.

DOCS AND DECISIONS
- `Qompack.md` goes to v1.6: §8.6 gets the fixed 9,500-character bound, with a Revision-log entry. `plans/QOMPACK-ERRATA.md` has the matching v1.6 record, and the test/guards pin moved to v1.6 in the same commit.
- ADR 0011 gets §21 as an amendment (history kept), and both ADR index rows say so.
- docs/architecture.md §7, cannot-do, user-guide, troubleshooting (new entry), uat.md UAT-05 and upstream-issues are updated.
- The `runtime.rehydrate.maxTokens` description now mentions the ceiling, and config-reference.md was regenerated from it.

REAL HOST (1 of the 2 allowed sessions; plans/sdd/V6-closeout/rehydrate-cap/evidence/live-s1-host-cap-inline/)
- Setup: Claude Code 2.1.280, haiku-4-5, max-turns 4, `--plugin-dir` bundle from d61c8c6. Five pins were seeded through the real pins store. The first prompt was 7,816 bytes; then `/compact` was sent in-stream.
- Delivered context: the SessionStart(compact) additionalContext was 3,048 units, inline. There is no `<persisted-output>` anywhere in the transcript. The original prompt was left out whole and named in section 7 with `restore: expand(tool_use_id=prompt_374bb22a-…_0)`.
- Pointer resolution: the model called `mcp__plugin_qompack_qompack__expand` with `full=true`. The tool result contains the codeword TEAL-OSPREY-42, and the model quoted it.
- Before/after: replaying the same post-session state, the 32e1a37 binary renders 10,882 units and fires the over-cap Loud line; the D5 binary renders the same 3,048 units the host delivered.
- Cost: $0.117.
- Cleanup: settings.json and plugins/*.json hashes are identical before and after. The run's own `qompack-inline`, `session-env/<id>` and `projects/<scratch>` directories were removed by name. All daemons the runs started were stopped by PID. Paths in the evidence are redacted.

CRITERION CHANGES
C1.14 can be ticked from commits dc585c1, 9be2904, d61c8c6, d1039b8 and the evidence directory. UAT-05's expected results and fail conditions now include the ceiling. ADR 0011's index kind is now "Current amendment".

WHAT I DID NOT TOUCH
internal/daemon, internal/store and internal/mcp have no changes. The only internal/config change is the maxTokens doc tag.

### Commits

- dc585c13fa2b05938d90ddbfaa54da9455ee3e8b refactor(hookio): share the host's character count
- 9be2904a368c1faead23885f6bab4177defd0db7 fix(rehydrate): fit the compact payload under the host cap
- d61c8c6404f6091022ca7169b13347c7da0967d0 docs(rehydrate): record D5 in Qompack.md v1.6 and ADR 0011
- d1039b832ad7f75098e4d4ea2b4cf8a9a95cb7cb test(rehydrate): record the real-host D5 session and final runs

### Tests

- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/rehydrate/... ./internal/hookio/ ./internal/config/ ./test/e2e/ ./test/guards/ ./test/docs/` — exit 0
- `go run ./tools/devtool gen-config-docs --check` — up to date
- `go test ./internal/rehydrate/... -count=1` — ok (rehydrate, rehydratetest)
- `go test ./internal/rehydrate/ -run 'TestBuild_NeverExceedsTheHostCeiling|TestBuild_MonotoneInBudget' -count=1 -rapid.checks=3000` — ok; also passed at 5000 checks earlier
- `go test ./internal/rehydrate/ -run '^$' -fuzz=FuzzBuild_HostCeiling -fuzztime=60s -parallel=2` — PASS, 137,642 execs
- `go test ./internal/hookio/ -count=1` — ok
- `go test ./internal/config/ -count=1` — ok (TestJSONSchema_Golden verifies the hand-applied line)
- `go test ./test/docs/ -count=1` — ok
- `go test ./test/replay/ -count=1` — ok
- `go test ./internal/cli/ -run TestHookOutput_ -count=1` — ok
- `go test ./internal/daemon/ -run TestService_ -count=1` — ok (18 rehydrate/compact tests)
- `go test ./test/integration/ -run TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly -count=1` — ok
- `go test ./test/guards/ -run TestQompack -count=1 -v` — 3/3 PASS at v1.6
- `go test ./test/guards/ -count=1 -timeout=30m` — FAIL on TestGuard_TheV1DeliveryPositionHasOneEncoder and TestCarriedDefects_WaveReportRequiresResolution; both fail identically on base 32e1a37
- `go test ./test/e2e/ -run '<21 compact-rehydration cases incl. TestE2E_SessionStartCompactFitsTheHostCap>' -count=1 -v -timeout=30m` — 20 PASS; TestV5_EliminationThroughEveryFourSurfaces FAIL, identical on base 32e1a37 (expects a PreCompact hookSpecificOutput that C1.12 removed) <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go run ./tools/devtool lint` — fails only bindeps, stubskips, runpatterns — all pre-existing (C1.19 and packaging/report.md lines 123 and 317); nomagic, import graph and the rest pass
- `red run on base 32e1a37 (temporary test, not committed): golden full fixture at the default budget` — FAIL as expected: 32,049 UTF-16 units > 9,500 (evidence/red-base-large-session.txt)
- `red run on base 32e1a37 (temporary e2e test, not committed): compact start over six 2,600-character rules` — FAIL as expected: 20,033 units, Loud over-cap line fired (evidence/red-base-e2e-session-start.txt)
- `real host: claude -p stream-json session (haiku-4-5, max-turns 4, --plugin-dir bundle d61c8c6) with in-stream /compact` — exit 0; compact additionalContext 3,048 units inline, no persisted-output; expand pointer resolved via MCP and returned the codeword; base-binary replay of the same state 10,882 units (over the cap)

### Criterion changes

- C1.14: tick with commits dc585c1, 9be2904, d61c8c6, d1039b8 and evidence at plans/sdd/V6-closeout/rehydrate-cap/evidence/ (host-cap-unit.txt, red-base-*.txt, live-s1-host-cap-inline/, final-verification.txt).
- D5 recorded in the design of record: Qompack.md v1.6 §8.6, Revision log, and the QOMPACK-ERRATA v1.6 record; ADR 0011 §21 amendment, with the index kind changed from 'Stands' to 'Current amendment' in docs/adr/README.md and docs/architecture.md.
- docs/uat.md UAT-05: added the expected result 'arrives inline, whole field at most 9,500 UTF-16 units, no persisted-output'; the tier-1 overflow detail now names the record and its restore pointer; the fail conditions now include a field over 9,500, a file-path preview, and a record cut mid-record.
- C4.3 (real compaction round trip): partly exercised. One real session shows a bounded rehydration injected inline after an in-stream /compact and pre-compaction facts recovered through the MCP expand pointer, using seeded pins rather than a full capture run.

### Open issues

- Other hook outputs are not bounded by construction and belong to other owners. (1) The UserPromptSubmit thrash-warning additionalContext in internal/observer/prompt_delivery.go adds one line per newly looping grammar rule, with no limit on the count. (2) The SessionStart degradation banner (systemMessage, degradeBanner in internal/daemon/handlers.go) quotes contract-result strings, including the host-supplied Event.Source and extra-field keys. The rehydration itself emits no systemMessage. Both are still caught by the hook client's HostCapOverruns Loud line, but they need owner-side bounds to meet 'nothing relies on the file-path fallback' across all hooks.
- Pre-existing on base 32e1a37, not from C1.14: TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces fails with 'a full-mode PreCompact answers through hookSpecificOutput' (fallout from C1.12's ConformOutput); test/guards TestGuard_TheV1DeliveryPositionHasOneEncoder and TestCarriedDefects_WaveReportRequiresResolution fail; devtool lint runpatterns flags two rows in plans/sdd/V6-closeout/packaging/report.md (lines 123 and 317).
- Comments in internal/cli are now out of date: hookclient.go says 'a large rehydration is cut without any check failing', and TestHookOutput_OverTheHostCapIsLoud's doc comment calls the over-cap outcome 'an owner decision'. D5 has decided it and the Loud path is now a fallback that does not fire. I left them because internal/cli is outside this workstream's scope.
- Qompack.md now declares v1.6 and the test/guards pin was recomputed. If another close-out branch also revises Qompack.md, renumber at merge and recompute qompackDigest on the merged file.
- FuzzBuild_HostCeiling runs its seeds in normal go test but is not in nightly.yml's fuzz matrix. Registering it means changing nightly.yml and nightlyFuzzMatrixLen (24), which I left to the coordinator.
- testdata/golden/config/schema.json: -update refuses on Windows because of platform-specific defaults, so the one changed description line was applied by hand. A Linux/CI `go test ./internal/config -run TestJSONSchema_Golden -update` should produce identical bytes; confirm it there.
- The checkpointer adds a cosmetic 'pointer_git_unavailable' drop line when a project has a .git directory but no index file (seen in the live run). Not a C1.14 issue.
- I used git stash three times for the red runs; the stash stack is shared across all worktrees. Each of my stashes was popped immediately and none remain, but a stash-free method would be safer while other agents run concurrently.

### Needs the owner

- Awareness, no change needed unless you want one. Under D5's rules (order and whole records kept), tier 1 admits pinned invariants before the verbatim original prompt. The ceiling is 9,500 characters and L0 reads up to 8,192 bytes of the prompt, so a large original prompt next to a few KB of pins is always left out whole and reported with its expand pointer. That is what the live session showed. Likewise, the prefix rule (ADR 0011 §7) means one path rule larger than item 6a's allowance also shuts out every rule sorted after it. Both follow the design as written; revisit only if you want different priorities at this ceiling.

## Independent review

### review:rehydrate-cap:0: needs-fixes

- **minor** `internal/rehydrate/items.go:1108-1111 (skill index drop), items.go:526-528 (elimination drop); claims in docs/adr/0011-rehydration-budget-and-item-order.md:446 and docs/architecture.md §7` — D5 requirement 2 says every omitted record is reported with a pointer the model can act on. Skill-index lines that the new character ceiling squeezes out carry only "did not fit the rehydration budget", with no restore pointer. Elimination drops say "call already_tried(target, approach)", but the drop line shows only the record id, so the model has no target or approach to pass. ADR §21 and architecture.md still say "Every record the payload leaves out is named with the call that brings it back."
  - Evidence: testdata/golden/rehydrate/state.json (re-recorded on this branch) has ten entries like `skill | onboard | did not fit the rehydration budget` (onboard, perf-profile, pool-watch, release-notes, repro-protocol, schema-diff, secret-scan, tf-plan-review, token-forensics, trace-read). The full-12k golden (same data) hides them behind `… and 20 more; call dropped()`, and dropped() returns the same pointer-less detail. The builder has e.Source, the SKILL.md path, but never puts it in the drop detail.
  - Fix: In buildSkillIndex set Detail to "did not fit the rehydration budget" + restoreClause("Read "+oneLine(e.Source)) (Source is already on skills.Entry). For eliminations, either say "call dropped() for its target and approach, then already_tried(...)" or name a call that takes the id. Re-record the goldens with -update. Otherwise narrow the ADR §21 'Pointers' paragraph and architecture.md to the item kinds that really carry a pointer.
- **minor** `internal/daemon/handlers.go:862-869 (degradeBanner); internal/observer/prompt_delivery.go (thrash-warning additionalContext)` — D5 requirement 3 is only met for the rehydration's own output. The SessionStart systemMessage banner quotes contract-result Expected/Observed strings with no length bound, and Observed can include host-supplied values. Nothing bounds it under the 10,000-unit cap, so that field can still reach the host's file-path fallback. The implementer reported this as an open issue: scope forbade touching internal/daemon.
  - Evidence: degradeBanner returns fmt.Sprintf("Qompack: degraded to passive recording — %s expected %q, observed %q. ...", r.ID, r.Expected, r.Observed) without truncation. hookio.HostCapOverruns only logs Loud and does not bound the field.
  - Fix: Do not tick requirement 3 ('nothing in any hook output relies on the file-path fallback') for C1.14 alone. Send it to the daemon/observer owners: truncate the quoted Expected/Observed on a rune boundary to a fixed budget (for example, keep the whole banner under 1,000 units), bound the thrash-warning list with a count of the rest, and add a HostChars <= cap test for each.
- **nit** `internal/rehydrate/budget.go:228 (fillTier1 prefix stop); docs/adr/0011-rehydration-budget-and-item-order.md:421` — ADR §21 point 1 says one oversized pinned invariant 'no longer takes the forty that fit down with it'. That holds only when the oversized pin comes AFTER the ones that fit. fillTier1 sets truncated on the first record that does not fit and drops every later one, so an oversized pin at position 1 still takes down all later pins, at every compaction, because pins are append-only.
  - Evidence: budget.go:228 `if a.truncated || !next.within(room) { a.truncated = true; ... continue }`. This is the deliberate §7 prefix rule, kept for PropBuild_MonotoneInBudget, but the ADR wording implies first-fit behaviour.
  - Fix: Reword ADR §21 point 1: tier 1 now admits a prefix of records, so records before the first one that does not fit survive, and everything from that record onward is named. Or ask the owner whether tier-1 invariants should use first-fit and give up strict monotonicity. The implementer's needs_owner item raises the same question for rules.
- **nit** `internal/rehydrate/build.go:154` — reserveSkill.chars is always a tenth of the ceiling (940 units), even when the build has no skill-index units. That room is kept away from items 2–6a and then goes only to item 7. When a session has no skills, whole records in items 3–6a can be dropped while the payload stays well under the ceiling.
  - Evidence: `reserveSkill := cost{tok: core.Tokens(cfg.SkillIndexTokens), chars: limit.chars / dropReportReserveDiv}` is subtracted in `held` from the room for every share, whether or not all[ItemSkillIndex].units is empty. The e2e case renders 7,378 of 9,500 with oversized rules omitted.
  - Fix: Set reserveSkill (both dimensions) to zero when len(all[ItemSkillIndex].units)==0, or cap reserveSkill.chars at the skill index's own total priced chars plus its section cost. Then re-check PropBuild_MonotoneInBudget and the goldens.

### review:rehydrate-cap:1: needs-fixes

- **minor** `internal/rehydrate/items.go:1110 (skill drops), internal/rehydrate/items.go:528 (elimination drops); claims at docs/adr/0011-rehydration-budget-and-item-order.md:446, docs/architecture.md:254, plans/QOMPACK-ERRATA.md:315` — Not every omitted record comes with an actionable pointer, although the docs say it does. A skill-index entry that does not fit still gets the bare detail "did not fit the rehydration budget" with no restore clause. An elimination gets the generic "call already_tried(target, approach) or dropped()", which leaves out the target and approach the model would need to make that call. ADR 0011 §21 says "Every record the payload leaves out is named with the call that brings it back", architecture.md says the same, and the errata says "pointers in every drop detail". D5 requirement 2 asks for pointers the model can act on for everything omitted.
  - Evidence: The re-recorded state.json golden carries 9 skill drops (onboard, perf-profile, pool-watch, release-notes, repro-protocol, schema-diff, secret-scan, tf-plan-review, token-forensics), each reading only "did not fit the rehydration budget". buildSkillIndex still builds that detail with no restoreClause(...). The elimination detail at items.go:528 was not changed and names no record-specific call.
  - Fix: Add restoreClause("Read "+oneLine(e.Source)) to the skill-index drop detail (skills.Entry carries Source). Make the elimination drop name its call, e.g. already_tried(<target>, <approach>) using oneLine'd fields, or point to dropped()/timeline for that id. Re-record the goldens through -update and keep the per-record detail assertions in items_test. If you decide skills are exempt instead, narrow the three doc claims to say so.
- **minor** `internal/rehydrate/hostcap_test.go:470 (FuzzBuild_HostCeiling); .github/workflows/nightly.yml:19-42; test/guards/nightlyfuzz_test.go:41` — The new fuzz target was not added to the nightly fuzz matrix. It is the only Fuzz* function in internal/ missing from nightly.yml. The repo's own convention, recorded in nightly.yml's comments, is to register a fuzz target in the commit that lands it (FuzzDeliverySealSelect was, and FuzzServeLine was called out for shipping unregistered). As things stand, the universal D5 claim only ever runs against its four seeds in CI.
  - Evidence: A scan of every `func Fuzz*` under internal/ against the nightly.yml `fn:` rows reports exactly one unregistered target: FuzzBuild_HostCeiling. nightlyFuzzMatrixLen = 24. The implementer disclosed this and left it to the coordinator.
  - Fix: Add `- { pkg: ./internal/rehydrate, fn: FuzzBuild_HostCeiling }` to nightly.yml's matrix and change nightlyFuzzMatrixLen from 24 to 25 in the same commit. Watch for a merge conflict with any other close-out branch that also bumps the length.
- **minor** `plans/QOMPACK-ERRATA.md:333-336; internal/daemon/handlers.go:807-809 (degradeBanner systemMessage); internal/observer/prompt_delivery.go (thrash-warning additionalContext)` — D5 requirement 3 ('the systemMessage banner respects its own cap; nothing in any hook output relies on the file-path fallback') is only partly delivered. The rehydration itself is bounded. The SessionStart degradation banner (systemMessage) and the UserPromptSubmit thrash warning have no bound by construction. The daemon was out of scope, so leaving them is defensible. But the errata says both are "routed as open items in the C1.14 report", and no C1.14 report exists on the branch: plans/sdd/V6-closeout/rehydrate-cap/ contains only evidence/, and final-verification.txt does not mention them. The routing exists only in the implementer's transient structured output.
  - Evidence: `ls plans/sdd/V6-closeout/rehydrate-cap/` shows only `evidence`. handlers.go:808 sets `out.SystemMessage = degradeBanner(results)` without any length bound. The errata text cites a report file that does not exist.
  - Fix: Record both items durably: add them as checklist items or open issues in plans/V6-CLOSEOUT-CHECKLIST.md on verify/v6, or commit a short rehydrate-cap/report.md, and make the errata point to the real location. Owners: daemon for degradeBanner (bound it with hookio.HostChars at a fixed ceiling, with a counted tail), observer for the thrash warning. Until that lands, do not tick D5 requirement 3 as met across all hooks.
- **nit** `testdata/golden/config/schema.json:674` — The schema golden's one changed line was edited by hand because -update refuses on Windows (platform-specific defaults). The review lens asks for generator-only config and docs changes. TestJSONSchema_Golden does verify every byte, so this is a hygiene issue, not a correctness one.
  - Evidence: The d61c8c6 commit message says: "The schema golden's -update refuses on this Windows host (platform defaults), so its one changed line was applied by hand".
  - Fix: Regenerate on Linux (the WSL2 setup at /mnt/host/c works) with `go test ./internal/config -run TestJSONSchema_Golden -update`, confirm the bytes are identical, and note that in the evidence.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md:280 (§18) and :492 (Consequences); internal/cli/hookclient.go:477` — The pre-D5 figures are kept as history, which is correct, but they carry no inline marker. §18 still says the full-12k golden is "8034 tokens" with seven restored rules, and the Consequences section still says "full-8k.txt is full-12k.txt minus the last two restored rules". Only §21 and a one-line preface to Consequences say these are superseded. Separately, the internal/cli hookclient comment still says "a large rehydration is cut without any check failing", which is no longer true (disclosed; the file is out of scope).
  - Evidence: Both golden files are now byte-identical (cmp reports IDENTICAL). §18 has no '(pre-D5; see §21)' marker.
  - Fix: Add a short inline '(pre-D5 figure; see §21)' after the §18 golden numbers, keeping the original text. Route the hookclient.go comment update to the internal/cli owner.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md (§21 'D5, plans/V6-CLOSEOUT-CHECKLIST.md'); plans/QOMPACK-ERRATA.md v1.6 Origin` — The authority citation points to a file that does not yet record the decision on this branch. On base 32e1a37, plans/V6-CLOSEOUT-CHECKLIST.md's owner-decisions table lists only D1–D4. D5 is recorded only on verify/v6.
  - Evidence: On this branch, `grep '| D5' plans/V6-CLOSEOUT-CHECKLIST.md` finds nothing. It matches only on `git show verify/v6:plans/V6-CLOSEOUT-CHECKLIST.md`.
  - Fix: Merge this branch into verify/v6 (where D5 is recorded), or on merge confirm the combined checklist carries the D5 row so the citations in the ADR and the errata resolve.

## Fix seat (review resolution) — status `done`, head `e088648`

### Summary

# C1.14 (owner decision D5): rehydration fits the host's additionalContext cap. Workstream report, fix seat

Branch `closeout/rehydrate-cap` in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-rehydrate. It is cut from `closeout/packaging` 32e1a37. HEAD is e088648 and the working tree is clean. Nothing was pushed, tagged or merged.

Coordinator: please commit this text as `plans/sdd/V6-closeout/rehydrate-cap/report.md`. The errata now cites that path.

## Implementer round (dc585c1..d1039b8, already on the branch)

- **Root cause.** Qompack.md §8.6 and ADR 0011 bounded the rehydration only in tokens (maxTokens 12,000, about 48K characters). The frozen `full` fixture rendered 32,049 characters. An e2e compact SessionStart rendered 20,033 characters and set off the hook client's Loud overrun line. Claude Code 2.1.280 caps every hook field at 10,000 UTF-16 code units. It checks `field.length<=1e4`, inclusive (evidence/host-cap-unit.txt). Above the cap it substitutes a file path plus a 2,000-character preview.
- **Fix.**
  - `rehydrate.HostContextCeilingChars` = 9,500 is a host constant, not a config key. `PayloadCeilingChars` = 9,400, which leaves a 100-unit reserve for the daemon's contract-probe line.
  - Every unit is priced exactly in host characters as well as tokens, and admitted only if it fits both.
  - Tier 1 is admitted record by record, with the retrieval line first.
  - Item 7 is a bounded prefix plus a counted tail, and its floor is reserved from the first admission on.
  - Tier-1 overflow entries sort first.
  - `hookio.HostChars` is shared with the hook client.
- **Real-host proof** (live-s1, haiku-4-5, one session):
  - The compact context arrived inline at 3,048 units. The replay on the base build produced 10,882 units plus the Loud line.
  - The tier-1 overflow pointer `expand(tool_use_id=prompt_…_0)` resolved through the qompack MCP tool, and the model quoted the codeword.
  - `~/.claude/settings.json` was byte-identical before and after the session.

## Review resolution

1. **Finding 1 (skill-index drop has no pointer; elimination drop names no target/approach).** CONFIRMED and FIXED in 3c23918.
   - I wrote the tests first and they failed (evidence/review-fix/red-pointer-tests.txt).
   - `buildSkillIndex` now ends both skill drop details in `restoreClause(skillPointer(e))`. For a normal entry that is `; restore: Read <Source>`. For an entry with no Source it falls back to `; call dropped() for the full accounting`, so the line never reads a bare "Read ". This covers "did not fit the rehydration budget" and "not in the compact skill index".
   - A shown elimination's drop now reads `call already_tried(target="…", approach="…")`, built by the new `alreadyTriedCall`. The fields are the record's own oneLine'd target and approach, Go-quoted. The ledger collapses whitespace, so the call hits the same descriptor.
   - New tests: `TestSkillIndex_BudgetDropNamesTheSkillFile` and `TestEliminations_BudgetDropNamesItsOwnQuery`.
   - Changed test: `TestSkillIndex_DropsReportUnindexedSkills`. Rationale: the intended shape changed, since the detail now carries its pointer. Its fixture gained a Source on one entry so both the pointer case and the fallback case are pinned.
   - Goldens `no-checkpoint.txt` and `state.json` were re-recorded with `-update`. The diff is 14 detail strings only, with no change to tokens, units or items.
2. **Finding 3 (same defect plus doc claims).** Same fix as finding 1. Docs corrected in 7c1b6ac:
   - The ADR 0011 §21 "Pointers" paragraph now lists the skill and elimination pointers.
   - A new paragraph names the drop entries that are counts or reports rather than records: eliminations past eliminationsTopN (answered by the standing already_tried query), Checkpoint.Dropped entries, guard rejections, scan failures, and the §2.7 skill-body warnings.
   - A dated review-correction note records that the first draft claimed more than the code did.
   - The pointer lists in architecture.md, cannot-do.md, troubleshooting.md and user-guide.md are updated.
   - The errata's "pointers in every drop detail" is narrowed and corrected.
3. **Finding 4 (FuzzBuild_HostCeiling not in the nightly matrix).** CONFIRMED and FIXED in 5848d0d.
   - Added the row to .github/workflows/nightly.yml, with a comment, and moved `nightlyFuzzMatrixLen` from 24 to 25 in the same commit.
   - Red: TestNightlyFuzzMatrix failed with "should have 24 item(s), but has 25" while only the row was added. Green after the bump; the leg reports "live".
   - A two-way inventory finds 25 source targets and 25 rows, with no difference either way (evidence/review-fix/fuzz-inventory.txt).
   - **Merge note:** any other close-out branch that adds a row also moves the pin from 24, so the sum must be resolved by hand.
4. **Finding 2 (degradeBanner systemMessage and thrash-warning additionalContext are unbounded).** CONFIRMED, NOT FIXABLE IN SCOPE. The brief forbids touching internal/daemon, and the thrash warning lives in internal/observer.
   - The code excerpt is committed at evidence/review-fix/unbounded-hook-outputs.txt: `degradeBanner` quotes `%q` of Expected/Observed unbounded, the warning is `strings.Join(lines, thrashLineSep)` with no count bound, and a grep finds no bound in either.
   - D5 requirement 3 is met for the compact rehydration only. Do not tick it for all hooks. Routed under Open items below.
5. **Finding 5 (errata cites a C1.14 report that does not exist).** CONFIRMED and FIXED in 7c1b6ac.
   - The errata now states that requirement 3 holds for the rehydration only.
   - It names both producers and their owners, and points at the committed evidence file.
   - It points at this report's path, `plans/sdd/V6-closeout/rehydrate-cap/report.md`, which the coordinator commits as it did for packaging.

## Commands and results (review round)

- `go test ./internal/rehydrate -run 'TestSkillIndex_DropsReportUnindexedSkills|TestSkillIndex_BudgetDropNamesTheSkillFile|TestEliminations_BudgetDropNamesItsOwnQuery'` before the fix: FAIL ×3, as expected. After the fix: ok.
- Golden tests before `-update`: FAIL (no-checkpoint, state). `-update` then ok, and the diff was reviewed.
- `go test ./internal/rehydrate -count=1`: ok.
- `-rapid.checks=3000` on TestBuild_MonotoneInBudget and TestBuild_NeverExceedsTheHostCeiling: ok. TestBuild_MonotoneInBudget at 20000 checks: ok.
- `-fuzz ^FuzzBuild_HostCeiling$ -fuzztime 60s -parallel 2`: PASS, 251k execs.
- An ad-hoc budget sweep over the golden full fixture (scratch, not committed), base versus fix: no ceiling breach, no token non-monotonicity, no drop-superset violation. Its line-prefix counts were identical on base and fix.
- `go test ./test/e2e -run TestE2E_SessionStartCompactFitsTheHostCap`: ok, 7,378 host characters.
- `go test ./test/docs`: ok.
- `go test ./test/guards -run TestNightlyFuzz`: ok. The full `./test/guards` run has 2 failures, TestGuard_TheV1DeliveryPositionHasOneEncoder and TestCarriedDefects_WaveReportRequiresResolution. Both fail identically on d1039b8 with this round stashed, so they pre-exist and are unrelated.
- `devtool fmt-check`: exit 0. `go vet ./internal/rehydrate ./test/guards`: exit 0.
- `devtool lint`: golangci-lint, nomagic, importgraph, testdeps, sleepcheck, docmarkers and coveragefloors PASS. Three sub-checks fail, all pre-existing:
  - bindeps: x/sys/unix (C1.19).
  - stubskips: the same four daemon/hookio/store skips (C1.19).
  - runpatterns: two rows in packaging/report.md.
- All logs are under plans/sdd/V6-closeout/rehydrate-cap/evidence/review-fix/.

### Commits

- 3c23918 fix(rehydrate): point skill and elimination drops at their call
- 5848d0d ci(nightly): register FuzzBuild_HostCeiling in the fuzz matrix
- 7c1b6ac docs(rehydrate): match the pointer claims and route the open outputs
- e088648 test(rehydrate): record the review fix seat's verification runs

### Tests

- `go test ./internal/rehydrate -run 'TestSkillIndex_DropsReportUnindexedSkills|TestSkillIndex_BudgetDropNamesTheSkillFile|TestEliminations_BudgetDropNamesItsOwnQuery' -count=1 (before fix)` — FAIL x3 as intended (red-pointer-tests.txt)
- `go test ./internal/rehydrate -run 'TestSkillIndex_|TestEliminations_' -count=1 (after fix)` — ok
- `go test ./internal/rehydrate -run 'TestBuild_Golden_PayloadsMatchFrozenBytes|TestState_MatchesFrozenGolden' -update` — ok; diff = 14 drop-detail strings in no-checkpoint.txt/state.json only
- `go test ./internal/rehydrate -count=1 -timeout=30m` — ok
- `go test ./internal/rehydrate -run 'TestBuild_MonotoneInBudget|TestBuild_NeverExceedsTheHostCeiling' -rapid.checks=3000` — ok (monotone also ok at 20000)
- `go test ./internal/rehydrate -run '^$' -fuzz '^FuzzBuild_HostCeiling$' -fuzztime 60s -parallel 2` — PASS, 251,446 execs
- `go test ./test/guards -run TestNightlyFuzz -count=1 (row added, pin still 24)` — FAIL: should have 24 item(s), but has 25 (red)
- `go test ./test/guards -run TestNightlyFuzz -count=1 (pin 25)` — ok; rehydrate_FuzzBuild_HostCeiling reported live
- `go test ./test/guards -count=1` — FAIL: TestGuard_TheV1DeliveryPositionHasOneEncoder, TestCarriedDefects_WaveReportRequiresResolution; both fail identically on d1039b8 without this round (pre-existing)
- `go test ./test/e2e -run TestE2E_SessionStartCompactFitsTheHostCap -count=1` — ok; 7378 host characters (ceiling 9500)
- `go test ./test/docs -count=1` — ok
- `go run ./tools/devtool fmt-check; go vet ./internal/rehydrate ./test/guards` — exit 0; exit 0
- `go run ./tools/devtool lint` — exit 1 on pre-existing bindeps (C1.19), stubskips 4 daemon/hookio/store skips (C1.19), runpatterns 2 packaging/report.md rows; golangci-lint, nomagic, importgraph, testdeps, sleepcheck, docmarkers, coveragefloors PASS

### Criterion changes

- C1.14 / D5 req. 2 (every omitted record named with an actionable pointer): now true for skill-index entries (Read <SKILL.md>) and shown eliminations (already_tried with the record's own quoted target and approach). ADR 0011 §21 names the drop entries that are counts or reports rather than records.
- C1.14 / D5 req. 3: met for the compact rehydration (inline, <= 9,500 units, Loud overrun path unreachable for it). NOT met across all hook outputs: the degradation banner and thrash warning are routed to their owners.
- C1.14 / D5 req. 4: FuzzBuild_HostCeiling is now in the nightly fuzz matrix (25 rows, guard pin 25), so the universal claim is fuzzed nightly and no longer limited to its seeds.

### Open issues

- D5 requirement 3 is met for the SessionStart(source=compact) rehydration only. The SessionStart degradation banner (internal/daemon/handlers.go degradeBanner, systemMessage) quotes contract Expected/Observed values with no length bound. The UserPromptSubmit thrash warning (internal/observer/prompt_delivery.go, strings.Join(lines, thrashLineSep)) has no bound on how many lines it joins. The only guard on either is hookio.HostCapOverruns' Loud log line. Evidence: plans/sdd/V6-closeout/rehydrate-cap/evidence/review-fix/unbounded-hook-outputs.txt.
- Merge hazard: nightlyFuzzMatrixLen moved from 24 to 25 on this branch. Any other close-out branch that adds a nightly.yml row also moves it from 24, so the merge must resolve the sum by hand.
- Pre-existing on the base and not touched here: the test/guards failures TestGuard_TheV1DeliveryPositionHasOneEncoder and TestCarriedDefects_WaveReportRequiresResolution; lint bindeps and stubskips (C1.19); lint runpatterns on plans/sdd/V6-closeout/packaging/report.md lines 123 and 317; and test/e2e TestV5_EliminationThroughEveryFourSurfaces, which the implementer recorded as failing identically on 32e1a37 (C1.12 ConformOutput).

### Needs the owner

- Daemon owner: bound degradeBanner's systemMessage under the host cap. Cut the quoted Expected/Observed on a rune boundary to a fixed budget (for example, keep the whole banner under 1,000 UTF-16 units), measure with hookio.HostChars, and add a HostChars <= cap test. Suggest a new V6-CLOSEOUT-CHECKLIST item next to C1.14.
- Observer owner: bound the UserPromptSubmit thrash-warning additionalContext. Keep a bounded prefix of lines plus a count of the rest, measured with hookio.HostChars against the cap, and add a test.
- Coordinator: commit this summary as plans/sdd/V6-closeout/rehydrate-cap/report.md (the errata cites that path). Tick C1.14 for the rehydration only; D5 requirement 3 across all hook outputs stays open until the two items above land.


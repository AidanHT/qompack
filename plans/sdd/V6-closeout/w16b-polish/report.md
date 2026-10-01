# W16B-POLISH: hostperm 8.3 trigger, grammar self-markers, plugin keywords

Branch `closeout/w16b-polish`. Workflow `wf_747e62d8-a28`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `ea734df6`

### Root cause

(1) longNameAliases' glob branch and literalEqual/prefixRow gated the 8.3 comparison on strings.Contains(seg, "~") rather than the 8.3-shape predicate, so any tilde rule (Read(**/*~)) set shortPending and refused every nonexistent path. (2) statewarn's self-markers were the literal "/qompack:" and "mcp__qompack__" only, never the host's plugin namespace. (3) pluginmanifest.Default listed "cache" among the keywords.

### Summary

W16B-POLISH report (branch closeout/w16b-polish, base 6f118a7b, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16b-polish). All three items are done. Evidence logs are committed under plans/sdd/V6-closeout/w16b-polish/runs/ (01-13).

(1) hostperm 8.3 trigger (w16-winci verify finding, D55).
Root cause: in longNameAliases (internal/hostperm/policy.go) the glob branch set pending for any segment after the concrete prefix that contained a tilde (strings.Contains(s, "~")). The literal branch used shortShaped (a tilde then a digit). literalEqual and prefixRow (rules.go) also gated the 8.3 comparison on any tilde. So Read(**/*~) set RuleSet.shortPending, and every retrieval of a path that does not exist was refused with unknownShortName. That contradicts docs/security.md and doc.go.
Red first: the new seam row TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles (shortname_seam_windows_test.go) failed under Read(**/*~), Read(*~), Read(**/*.txt~) and Read(./foo.txt~). In each case a deleted file was Deny (2) where Allow (0) was expected (runs/01).
Fix:
- shortShaped is now the one 8.3-shape predicate, used by osAlias (paths), pending, literalEqual and prefixRow.
- It is widened to a tilde followed by a digit OR by a glob metacharacter that can stand for one: regex `~[0-9*?\[\\]`, the same `*?[\` set concretePrefix treats as glob. So Read(**/CREDEN~?.SEC) and Read(**/CONFIG~*/**) keep the 8.3 comparison and the fail-closed refusal.
- Path side: `*`, `?` and `\` cannot occur in a Windows file name. A name that does not exist and holds `~[` is now also refused, which fails closed.
- The new row also checks three things: the backup-file rules still deny an existing foo.txt~; under Read(**/CREDEN~1.SEC), Read(**/CREDEN~?.SEC) and Read(**/CONFIG~*/**) a deleted file is still Deny with Rule unknownShortName; and the long credentials.secret is Deny while public.txt is Allow.
- doc.go and docs/security.md each gain one sentence: a rule names an 8.3 name only with a tilde followed by a digit or a glob character; a backup-file rule such as Read(**/*~) names none.

(2) Grammar self-markers.
Root cause: IsSelfOriginated (internal/grammar/statewarn.go) matched only the literal "/qompack:" and the prefix mcp__qompack__. A release entry's host namespace was never recognised, so those self-originated events could feed the loop warning.
Red first: six new self rows in TestIsSelfOriginated failed (runs/04): host_mcp_tool_local_entry, host_mcp_tool_release_entry, host_mcp_tool_release_folded, and release_slash_command_in_goal/_at_start/_underscore.
Fix:
- New core.CutHostPluginTool(name, server) in internal/core/toolnames.go parses mcp__plugin_<entry>_<server>__<tool>: a non-empty entry with no "__", and <tool> after the last "__", non-empty.
- It lives in core because mcp imports grammar, so grammar cannot import mcp. core already holds the host tool-name table that both sides must agree on (toolnames.go), so no new import edge was needed.
- internal/mcp/recall_rank.go isHostQompackToolCall now calls it, plus slices.Contains(toolNamesInDesignOrder, tool). Its behaviour is unchanged, including the edge "mcp__plugin_a__qompack__why"; the existing TestIsRetrievalSelfRecord_* rows pass.
- grammar adds selfMarkerMCPServer = "qompack" and isHostSelfTool, which accepts any tool. A false positive only suppresses a warning, the direction the file leans.
- New hasSlashCommandMarker accepts "/qompack:" and "/qompack" + ('-' or '_') + a non-empty [A-Za-z0-9._-] run + ':'.
- Negative rows: another plugin's tool; another server in a qompack entry; a server only ending in qompack; an empty plugin segment; a segment hiding a "__" boundary; no tool; /review:qompack; /qompackish:; /my-qompack:; /qompack-:; and a bare /qompack-windows-amd64 with no colon.
- New tests: TestCutHostPluginTool (core), and TestHostToolNames_AreGrammarSelfMarkers (mcp), which pins that every host name recall ranks as a self-record is also a grammar self-marker, so the two server constants cannot drift.

(3) plugin.json keywords.
Red first: new TestManifest_KeywordsClaimOnlyWhatIsSupported failed with actual [compaction context memory cache] (runs/08).
Fix:
- Removed "cache" from pluginmanifest.Default.
- Regenerated plugin/ with `go run ./tools/devtool plugin-validate --write`.
- Made the same one-line edit to testdata/golden/plugin/.claude-plugin/plugin.json.
- The marketplace generator (tools/devtool/marketplace.go) emits no keywords, so nothing else needed regenerating. "compaction" is kept.

Commands and results (Windows, -p 2, machine shared with other seats):
- go test ./internal/hostperm -run 'TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles' -count=1 -p 2 -v: FAIL before the fix (runs/01, product files stashed), PASS 7/7 subtests after (runs/02)
- go test ./internal/hostperm -count=1 -p 2: ok (runs/03)
- go test ./internal/grammar -run 'TestIsSelfOriginated' -count=1 -p 2 -v: FAIL before (runs/04), PASS 27 subtests after (runs/05)
- go test ./internal/core -run 'TestCutHostPluginTool' -count=1 -p 2 -v: PASS
- go test ./internal/mcp -run 'TestHostToolNames_AreGrammarSelfMarkers' -count=1 -p 2 -v: PASS
- go test ./internal/mcp -run 'TestIsRetrievalSelfRecord_AnyPluginSegment' -count=1 -p 2 -v: PASS
- go test ./internal/mcp -run 'TestIsRetrievalSelfRecord_Negatives' -count=1 -p 2 -v: PASS
- go test ./internal/mcp -run 'TestRecallRanksSelfRecordsAfterOriginalCaptures' -count=1 -p 2 -v: PASS
- go test ./internal/core ./internal/grammar -count=1 -p 2: ok (runs/06)
- go test ./internal/mcp -count=1 -p 2 -timeout=30m: ok, 239 s (runs/07). It ran after the hostperm commit, so it includes TestHostPolicy_AShortNameSpellingIsRefused against the new predicate.
- go test ./internal/pluginmanifest -run 'TestManifest_KeywordsClaimOnlyWhatIsSupported' -count=1 -p 2 -v: FAIL before (runs/08), PASS after (runs/09)
- go test ./internal/pluginmanifest -count=1 -p 2: ok (runs/10)
- go run ./tools/devtool plugin-validate: OK (runs/11)
- go test ./test/docs -count=1 -p 2: ok (runs/12)
- go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns: exit 0 (runs/13)
- go run ./tools/devtool fmt-check: exit 0
- go vet on hostperm, core, grammar, mcp and pluginmanifest: pass on Windows and with GOOS=linux

No wall-clock failures occurred, so nothing needed a solo re-run. No Linux container, no -race, no load generators, and no processes left running.

Criterion changes, with rationale:
- (a) testdata/golden/plugin/.claude-plugin/plugin.json loses its "cache" line. This is an intentional content change ordered by D53(e)/F8, not a regeneration to match broken output. The golden test's other pins, including the historical homepage substitution, are untouched.
- (b) The hostperm 8.3 trigger narrows from "any tilde" to the shortShaped shape, as D55 orders. Deleted files under a rule such as Read(**/*~) are allowed again, which security.md and doc.go already promised. The trigger widens on the path side by `~[` only, and that direction fails closed.
- No assertion was deleted or loosened, and no threshold, skip or nolint was added.

### Commits

- 1db0dae6 fix(hostperm): narrow the 8.3 trigger to 8.3-shaped segments
- 61829efb fix(grammar): recognise the host's plugin namespace as self
- 0e21b15e fix(pluginmanifest): drop the cache keyword from plugin.json
- ea734df6 docs(v6): record w16b-polish docs and lint evidence

### Tests

- `go test ./internal/hostperm -run 'TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles' -count=1 -p 2 -v` — FAIL before fix (runs/01), PASS 7/7 after (runs/02)
- `go test ./internal/hostperm -count=1 -p 2` — ok (runs/03)
- `go test ./internal/grammar -run 'TestIsSelfOriginated' -count=1 -p 2 -v` — FAIL before (6 new self rows, runs/04), PASS 27 subtests after (runs/05)
- `go test ./internal/core -run 'TestCutHostPluginTool' -count=1 -p 2 -v` — PASS
- `go test ./internal/mcp -run 'TestHostToolNames_AreGrammarSelfMarkers' -count=1 -p 2 -v` — PASS
- `go test ./internal/mcp -run 'TestIsRetrievalSelfRecord_Negatives' -count=1 -p 2 -v` — PASS
- `go test ./internal/core ./internal/grammar -count=1 -p 2` — ok (runs/06)
- `go test ./internal/mcp -count=1 -p 2 -timeout=30m` — ok 239s (runs/07)
- `go test ./internal/pluginmanifest -run 'TestManifest_KeywordsClaimOnlyWhatIsSupported' -count=1 -p 2 -v` — FAIL before (runs/08), PASS after (runs/09)
- `go test ./internal/pluginmanifest -count=1 -p 2` — ok (runs/10)
- `go run ./tools/devtool plugin-validate` — OK 9 files (runs/11)
- `go test ./test/docs -count=1 -p 2` — ok (runs/12)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0 (runs/13)
- `go run ./tools/devtool fmt-check; go vet (Windows + GOOS=linux) on hostperm core grammar mcp pluginmanifest` — pass

### Criterion changes

- testdata/golden/plugin/.claude-plugin/plugin.json drops the "cache" keyword: an intentional content change ordered by D53(e)/F8 (cache awareness is unsupported), not a regeneration to match broken output. A new test pins the exact keyword list.
- hostperm's 8.3 trigger (pending, literalEqual, prefixRow) narrows from any tilde to shortShaped (a tilde followed by a digit or a glob metacharacter), per D55. Read(**/*~) no longer refuses deleted files. On the path side the predicate widens by `~[` only, which fails closed.

### Open issues

- Slash-command and MCP self-markers accept any entry segment, so another plugin named qompack-<x> (or qompack_<x>) would be treated as Qompack's own. The effect is only a suppressed warning, the direction statewarn leans. The host's actual release-entry namespace is still unobserved until the candidate 6 live lane (D53(f)).
- core.CutHostPluginTool was added to internal/core (outside the listed scope) because grammar sits below mcp. Sharing the predicate any other way would create an import cycle. core already holds the host tool-name table (toolnames.go).
- The eval-only prefixes named in w16-relhyg (internal/eval LiveQompackToolPrefix, tools/devtool/liveeval.go, testdata/eval/live allowed-tools) still assume an entry named qompack. They were not in this task's scope.

## Independent review

### review:polish: needs-fixes

- **minor** `internal/hostperm/policy.go:367 (shortNameRe), used at policy.go:590, rules.go:314, rules.go:379` — The narrowed predicate `~[0-9*?\[\\]` misses a glob that names an 8.3 name through a tilde inside a bracket expression, for example Read(**/CREDEN[~]1.SEC). On the base, any tilde turned on the 8.3 comparison, so this case is a fail-open regression on a privacy path. The rule spelling is contrived but legal. Read(**/*~), the case the task targets, is handled correctly.
  - Evidence: For the segment `creden[~]1.sec`, the tilde is followed by `]`, so shortShaped is false. On base 6f118a7b, strings.Contains(ps, "~") was true. As a result: (a) longNameAliases sets no pending, so a deleted file is now judged on its long name, and (b) prefixRow (rules.go:379) no longer tries path.Match(ps, sc.alt[j-1]). An existing credentials.secret whose 8.3 alt is CREDEN~1.SEC therefore does not match `creden[~]1.sec` and is Allowed, where the base Denied it. The seam rows cover only `~1`, `~?` and `~*`. No row has a tilde inside a class.
  - Fix: Treat a tilde inside a bracket expression as 8.3-shaped. One way is to add `]` and `-` to the follow set, as in `~[0-9*?\[\\\]-]`, or to add an alternative such as `\[[^\]]*~`. Then add a seam row: under Read(**/CREDEN[~]1.SEC), s.file is Deny and a deleted file is Deny with unknownShortName. Backup rules such as `*~` and `*.txt~` stay unshaped either way.
- **nit** `docs/security.md:45; internal/hostperm/doc.go:43` — On the path side, both docs still define an 8.3-shaped name as 'a tilde and a digit'. The shared predicate now also refuses a nonexistent path segment that holds `~[`, which `[` makes possible on Windows (for example a deleted `notes~[old].txt`, refused under any deny or ask rule). This fails closed, but the user-facing description no longer matches the behaviour.
  - Evidence: alias_windows.go:69 longName calls shortShaped(base) for kept, nonexistent segments. The new regex at policy.go:367 matches `~[`. Only the code comment at policy.go:360-366 mentions the path-side widening.
  - Fix: Add a clause to both sentences, e.g. 'a tilde followed by a digit (or `[`)', or keep the path side on `~[0-9]` with a separate path predicate. The task allows one predicate, so a doc fix is enough.
- **nit** `plans/00-ARCHITECTURE.md:811` — The architecture plan's plugin.json example still lists the keyword "cache", which this branch removed from the shipped manifest. It is outside this seat's listed scope (cirec owns 00-ARCHITECTURE rows this wave), so the coordinator should route it.
  - Evidence: grep shows `"keywords": ["compaction", "context", "memory", "cache"]` at plans/00-ARCHITECTURE.md:811. pluginmanifest.Default now emits [compaction context memory].
  - Fix: Have the cirec seat or the coordinator drop "cache" from the plan example, or record it as known plan drift.

## Fix seat (review resolution) — status `done`, head `fd48cce3`

### Root cause

The 1db0dae6 predicate ~[0-9*?\[\\] keyed only on the character after the tilde. A tilde inside a bracket expression (CREDEN[~]1.SEC, CREDEN[}-~]1.SEC) is followed by ] or -, so the 8.3 comparison stayed off. That was fail-open compared with the base's any-tilde trigger.

### Summary

FIX SEAT, w16b-polish, branch closeout/w16b-polish (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16b-polish). Head is fd48cce3 and the tree is clean.

## Review resolution
- Finding (minor, internal/hostperm/policy.go shortNameRe): the reviewer is right. I added a failing test, fixed the predicate, and the test now passes.
  - Root cause: commit 1db0dae6 narrowed the 8.3 trigger from any tilde (`strings.Contains(s, "~")`) to `~[0-9*?\[\\]`. A glob can also name an 8.3 name through a tilde inside a bracket expression, such as `CREDEN[~]1.SEC` or `CREDEN[}-~]1.SEC`. In both, the tilde is followed by `]` or `-`, so shortShaped returned false.
  - Effect: the 8.3 comparison stayed off in all three call sites (longNameAliases pending, prefixRow, literalEqual). An existing credentials.secret, whose 8.3 alt is CREDEN~1.SEC, was allowed, and a deleted file was judged on its long name. The base denied both, so this was a fail-open regression on a privacy path.
  - Red: I added `Read(**/CREDEN[~]1.SEC)` and `Read(**/CREDEN[}-~]1.SEC)` to the 8.3-shaped rows of TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles. Both subtests failed with expected 2 (Deny), actual 0 (Allow). Log: plans/sdd/V6-closeout/w16b-polish/runs/14-fix-bracket-red.txt.
  - Fix: `shortNameRe = regexp.MustCompile(`~[0-9*?\[\\]|\[[^\]]*~`)`. The new alternative matches a tilde anywhere inside an open bracket expression, including `[~`, `[a~`, `[}-~` and `[\~`.
    - I did not take the reviewer's alternative of adding `]` and `-` to the follow set. It misses `[~a]`, and Go's path.Match rejects `[]~]` as a bad pattern anyway.
    - Backup rules `*~`, `*.txt~` and `*.[ch]~` stay unshaped. In `*.[ch]~` the tilde comes after the class closes, and `[^\]]*` cannot cross `]`.
    - The comment on shortNameRe explains the class case and the `*.[ch]~` negative.
  - New test: TestShortShaped_NamesAnEightDotThreeNameOnlyByShape in internal/hostperm/rules_test.go runs on every OS. It pins 7 positive and 6 negative segments.
  - Docs: docs/security.md and internal/hostperm/doc.go said a rule names an 8.3 name "only with a tilde followed by a digit or glob character". That sentence now also names the bracket case (`Read(**/CREDEN[~]1.SEC)`).

## Commands and results (machine was loaded; -p 2; no -race, no hot-path rows, no whole integration/e2e packages)
- `go test -p 2 -count=1 -run '^TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles$' ./internal/hostperm` before the fix: exit 1, two bracket subtests FAIL (runs/14).
- After the fix I ran the same command with the pattern widened to also cover TestShortShaped_NamesAnEightDotThreeNameOnlyByShape, plus -v: exit 0, all 9 subtests and the predicate table PASS (runs/15). The rows are named individually here because the runpatterns lint splits alternations.
- `go test -p 2 -count=1 ./internal/hostperm`: ok, 3.2s (runs/16).
- `go test -p 2 -count=1 ./test/docs`: ok (runs/17).
- `go run ./tools/devtool fmt` then `fmt-check`: exit 0. `go vet ./internal/hostperm`: exit 0 on Windows and with GOOS=linux.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0, all eight PASS (runs/18).
- No wall-clock failures, so nothing needed a re-run. I started no background processes and no load generators, and did not touch the Linux container.

## Criterion changes
None. No assertion was loosened. The seam test gained two 8.3-shaped rows, and a new portable predicate test was added.

## Implementer's work (unchanged by this seat)
- 1db0dae6: hostperm narrow trigger.
- 61829efb: grammar self-markers.
- 0e21b15e: plugin.json drops "cache" and keeps "compaction".
- ea734df6: evidence.
- Its evidence logs are runs/01-13.

## Open items (pre-existing, not regressions, not fixed here)
- A glob that stands in for the tilde without containing one, such as `Read(**/CREDEN?1.SEC)` or `Read(**/CREDEN*1.SEC)`, can match the 8.3 alt CREDEN~1.SEC. It does not turn on the 8.3 comparison, and it did not on base 6f118a7b either (base keyed on a literal "~"). This is a pre-existing gap, outside this wave's scope, noted for the coordinator.

## needs_owner
None. The implementer had none, and this fix adds no budget or bound number.

### Commits

- 1db0dae6 fix(hostperm): narrow the 8.3 trigger to 8.3-shaped segments (implementer)
- 61829efb fix(grammar): recognise the host's plugin namespace as self (implementer)
- 0e21b15e fix(pluginmanifest): drop the cache keyword from plugin.json (implementer)
- ea734df6 docs(v6): record w16b-polish docs and lint evidence (implementer)
- fd48cce3 fix(hostperm): treat a tilde inside a glob class as 8.3-shaped (fix seat)

### Tests

- `go test -p 2 -count=1 -run '^TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles$' ./internal/hostperm (before fix)` — FAIL as expected: Read(**/CREDEN[~]1.SEC) and Read(**/CREDEN[}-~]1.SEC) Allow instead of Deny
- `go test -p 2 -count=1 -v -run for TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles and TestShortShaped_NamesAnEightDotThreeNameOnlyByShape ./internal/hostperm (after fix)` — PASS (9 subtests + predicate table)
- `go test -p 2 -count=1 ./internal/hostperm` — ok 3.226s
- `go test -p 2 -count=1 ./test/docs` — ok 4.062s
- `go run ./tools/devtool fmt-check; go vet ./internal/hostperm (windows and GOOS=linux)` — exit 0 all
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all 8 PASS

### Open issues

- Pre-existing (also on base 6f118a7b, not a regression): a glob with no tilde that stands in for it, such as Read(**/CREDEN?1.SEC) or Read(**/CREDEN*1.SEC), can match an 8.3 alt like CREDEN~1.SEC but does not turn on the 8.3 comparison. Out of this wave's scope.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/hostperm/policy.go:369 (shortNameRe), used at policy.go:589/592, rules.go:314, rules.go:379` — The fix is incomplete. The new alternative `\[[^\]]*~` stops at the first `]`, including an escaped `\]`. A bracket expression that lists an escaped `]` before the tilde, such as Read(**/CREDEN[\]~]1.SEC) or Read(**/CREDEN[\]-~]1.SEC), still contains a literal tilde, and path.Match accepts it and matches CREDEN~1.SEC, yet shortShaped returns false. This is the same fail-open regression against base 6f118a7b that the original finding raised. The base used strings.Contains(ps, "~") and shaped these segments, so it Denied. HEAD skips the alt comparison in prefixRow and the pending flag in longNameAliases, so an existing credentials.secret (8.3 alt CREDEN~1.SEC) is Allowed and a deleted file is judged on its long name. The fix seat says the alternative "matches a tilde anywhere inside an open bracket expression". That is not true once the class contains `\]`. The spelling is contrived but legal: compileOne keeps the backslash in the primary pattern, finishPattern accepts it because path.Match reports no error, and prefixRow passes it to path.Match.
  - Evidence: I ran a standalone probe in the scratchpad with HEAD's regex `~[0-9*?\[\\]|\[[^\]]*~` and path.Match(p, "creden~1.sec"). Results: `creden[\]~]1.sec` shaped=false match=true, and `creden[\]-~]1.sec` shaped=false match=true. The control cases come out as the fix seat says: `creden[~]1.sec`, `creden[}-~]1.sec` and `creden[\~]1.sec` shaped=true, and `*~` and `*.[ch]~` shaped=false. compilePath (rules.go:140-152) keeps the backslash in the primary pattern; only the Windows alias rewrites it to `/`. finishPattern (rules.go:200-240) lowercases and splits on `/` only. prefixRow (rules.go:378-381) tries sc.alt only when shortShaped(ps) is true. `go test -p 2 -count=1 ./internal/hostperm` passes at HEAD fd48cce3, because no row covers an escaped `]` in a class. The rest of the fix checks out: the seam rows and the TestShortShaped table are additive, no assertion was loosened, and the docs edits are accurate for the cases they name. The open item the fix seat noted (`CREDEN?1.SEC` and the negated class `CREDEN[^a]1.SEC` contain no tilde) is truly pre-existing, because the base keyed on a literal `~`.
  - Fix: Let the class alternative step over escapes, for example: shortNameRe = regexp.MustCompile(`~[0-9*?\[\\]|\[(?:\\.|[^\]\\])*\\?~`). I probed it: `creden[\]~]1.sec`, `creden[\]-~]1.sec`, `creden[~]1.sec`, `creden[}-~]1.sec`, `creden[\~]1.sec` and `creden\~1.sec` all come out shaped=true, and `*~` and `*.[ch]~` stay shaped=false. Add `creden[\]~]1.sec: true` to TestShortShaped_NamesAnEightDotThreeNameOnlyByShape (rules_test.go). Also add Read(**/CREDEN[\]~]1.SEC) to the 8.3-shaped rows of TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles (shortname_seam_windows_test.go); a backslash in a rule needs the existing JSON quoting there. Correct the shortNameRe comment so it does not overclaim. A simpler alternative that also fails closed: treat any segment holding both `[` and a later `~` as shaped, unless the tilde is the last character after the class closes.


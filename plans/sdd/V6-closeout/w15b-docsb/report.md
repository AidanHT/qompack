# Wave 15b workstream docsb

Branch `closeout/w15b-docsb`. Workflow `wf_44d938d8-58c`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `0dd90863`

### Root cause

Scrub gap (item 9): live_driver.py applied its scratch-prefix scrub, which already included the JSON-escaped form, only to driver-config.json and meta.json. stream.jsonl, stderr.txt and hooks.json were written through unplant, which replaces planted secrets and nothing else. So the host's JSON-escaped Windows path, with the OS user name, reached every committed stream. The other items are documentation corrections D50 ordered; none of them has a code root cause.

### Summary

Wave 15b docsb: the D50 documentation and evidence-record items are done on closeout/w15b-docsb (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15b-docsb), 5 commits on fe6b27fa, head 0dd90863. The tree is clean and nothing I started is still running. No product behaviour changed. The only code edit is one comment in internal/contract/refresh.go and generator text in tools/devtool/genmcpdocs.go.

WHAT CHANGED, item by item
(1) docs/uat.md header. Wave 15 had already rewritten it for candidate 4, but with the pre-D50 tally ('eight passing and three failing (UAT-03, UAT-06, UAT-09)'). I corrected it in place rather than adding a second statement. It now says the other eleven rows were re-run on candidate 4 (`9f6a2fad`) under D47: seven pass and four fail (UAT-03, UAT-05, UAT-06, UAT-09), with UAT-05 failing because D50 reads its expectation literally, and findings routed by D49 and D50. It also says UAT-10 was not re-run, its Result block still reports candidate 3, and it is re-run on the next candidate (D50). TestUATIntroDescribesTheCurrentRecord still passes.
(2) UAT-05. The docs/uat.md Result line now starts 'Result: fail — under D50 the expectation is read literally: section 2 renders the superseded semicolon original above the TAB correction that supersedes it.' The block's false parenthetical '(nothing older above it)' now reads 'below it ... (third, under two later ordinary prompts)', which matches run1-block2 lines 7-12. I did not touch the expectation or the fail text. In rerun-c4/UAT-05/notes.txt the 'Result: pass.' line is now a fail line citing D50. It says lines 31-32's 'nothing older is rendered above it' is wrong and that the run recorded pass, relabelled 2026-09-30.
(3) rerun-c4/C4.4/tool-matrix.md:25 now reads 'Result: failed on candidate 4 (R4-1; D50 relabelled it from partial, 2026-09-30)', keeping the original reason text.
(4) UAT-02 now reads 'Result: pass on fail criteria; row capability host-limited (D49) — no fail criterion occurred ...'. Wave 15's D49 test-coverage note under the block is kept. The line still starts with 'pass', which TestUATUnexecutedRowsSayUnverified requires.
(5) UAT-04 gains two findings:
- LOUD 'rehydrate: tier-1 material exceeds the hard budget cap' is written on every compaction: 15 lines in rerun-c4/UAT-04/store/logs_LOUD.log, although section 7 already names the overflow. D50 routes it to once per session.
- After the run, with the planted config in place, fsck exits 1 on index.files "index/files.json is absent while its log carries 4 path(s)". Candidate 3 saw the same row, and whether the planted config causes it was not isolated. The wave 15 services report is not merged on my base, so the block says 'under investigation'.
(6) UAT-06 now says the correction record itself is absent, and that 60 appears only inside an echoed record_eliminated prompt in the evolution list. I checked this against C-block2-SessionStart-compact.txt:13 and quote the text.
(7) UAT-12 gains two findings:
- F4: section 6 lists the deny-ruled file's absolute path and root hash, and the out-of-project file's absolute path, with no content. D50 routes it to a fix.
- The candidate 4 paging semantics: the final page answered truncated true with no next_span, and an explicit span 0:354352 gave next_span 217070:16384 where full: true gave 217070:137282. The block says this was fixed after candidate 4 under D50. It does not name 'wave 15a', because this is a user-facing page.
(8) live/recovery/C1.6/notes.txt gains a dated candidate 4 note. It cites the interrupted startup accounting:
- UAT-12 LOUD at 00:58:08Z (70 captures, 380 objects scanned) and 01:03:52Z (70 captures, 374 objects).
- UAT-05 LOUD at 02:19:59Z (14 captures).
- The 250 ms publicationStartupBound.
It says the 'no automatic recovery' statement is unchanged, and that D49's fix is a precondition for citing C1.6's startup accounting on a later candidate.
(9) Live driver scrub (plans/sdd/V6-closeout/coordinator/live_driver.py).
Root cause: the scrub, including the JSON-escaped form, was applied only to driver-config.json and meta.json. stream.jsonl, stderr.txt and hooks.json were written through unplant only (planted secrets), by design ('stream.jsonl and stderr.txt otherwise keep the host's bytes'). That is why the JSON-escaped Windows path reached 27 committed files.
Fix: a new scrub_forms() returns, longest first, the / form, the \ form, and the \ form JSON-escaped once and twice. The doubly escaped form covers a hook output the stream carries as a JSON string. Every output is now scrubbed, stream.jsonl, stderr.txt and hooks.json included. The docstring now warns that scrubbing changes byte lengths, so response sizes must be measured from probe outputs, not from stream.jsonl.
Self-test (live_driver_test.py): the fake host now emits a Windows-form cwd, a Windows path inside a JSON hook output, and a stderr line with the path. The test asserts that no spelling appears in any output file, and that the stream and stderr carry <scratch> in the escaped and doubly escaped positions.
RED first: on the base driver (git show HEAD:… swapped in) the test fails at stderr.txt with the literal scratch path; with the fix it passes (3 tests OK). The committed history is accepted under D50, so no evidence was rewritten.
(10a) genmcpdocs.go: an explicit span is read over the range it resolves to, which ends on the next chunk boundary; once that range is served, a page still short of the object's end continues in `store.chunk.max`-sized steps. docs/mcp-tools.md was regenerated with gen-mcp-docs.
(10b) refresh.go historyRead comment: three things reset Chances — a start's mint, the withdrawal of a lost start's probe (withdrawLostStartAnswer), and a scan that finds the probe. I checked each against the code: handlers.go:951, handlers.go:1008 and history.go:294.

CRITERION CHANGE (1): in live_driver_test.py test_turns_hooks_before_and_scrub I replaced `self.assertIn(tmp.replace("\\", "/"), read(os.path.join(out, "stream.jsonl")))` with its opposite: stream.jsonl must carry no spelling of the scratch prefix. Rationale: the old assertion pinned the design that the stream keeps the host's path bytes, which D50 overturns ('the live driver's scrub covers the JSON-escaped Windows path'). The new check is stricter: all four spellings are checked in every output file, not only in two of them.

VERIFICATION (daytime limits: -p 2, no load generators, no hot-path rows, no whole e2e or integration packages, Linux container not started):
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/contract ./tools/devtool`: exit 0 on Windows and with GOOS=linux.
- `go run ./tools/devtool gen-mcp-docs --check`: up to date.
- `go test -p 2 -count=1 ./test/docs`: ok.
- Full runs of the touched packages, `go test -p 2 -count=1 ./internal/contract ./tools/devtool`: both ok (tools/devtool 78 s).
- `python live_driver_test.py`: RED on the base driver (1 failure), GREEN with the fix (3 tests OK).
- Lint with runpatterns and docmarkers included: exit 0, run after all commits.
No wall-clock failures occurred.

Other: this report quotes no `go test -run` patterns. The five commits follow the house format with no attribution trailers, and the commit-msg hook accepted them. Scratch files are only under the w15b/docsb scratchpad.

### Commits

- 78201daa docs(uat): record the D50 dispositions of the candidate 4 re-run
- 26427099 docs(live): relabel UAT-05 and C4.4 and note C1.6 under D50
- 77d2c186 fix(live): scrub every spelling of the scratch prefix
- 99d9b9eb docs(mcp): say an explicit span resolves to a chunk boundary
- 0dd90863 docs(contract): name every reset of the sentinel's chances

### Tests

- `python plans/sdd/V6-closeout/coordinator/live_driver_test.py (base live_driver.py from HEAD swapped in)` — RED as intended: FAILED (failures=1), test_turns_hooks_before_and_scrub found the literal scratch path in stderr.txt
- `python plans/sdd/V6-closeout/coordinator/live_driver_test.py (fixed driver)` — OK, 3 tests, exit 0
- `go run ./tools/devtool gen-mcp-docs && go run ./tools/devtool gen-mcp-docs --check` — wrote docs/mcp-tools.md; check: up to date, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/contract ./tools/devtool (Windows and GOOS=linux)` — exit 0 both
- `go test -p 2 -count=1 ./test/docs` — ok (2.6s)
- `go test -p 2 -count=1 ./internal/contract ./tools/devtool` — ok both (2.3s, 78.3s)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, PASS runpatterns and PASS docmarkers (343 plan docs), run after all commits

### Criterion changes

- plans/sdd/V6-closeout/coordinator/live_driver_test.py test_turns_hooks_before_and_scrub: I replaced the assertion that stream.jsonl still contains the forward-slash scratch path with its opposite, because D50 requires the scrub to cover the host's stream (JSON-escaped Windows path). The new check is stricter: it asserts that no spelling of the prefix (/ form, \ form, the \ form JSON-escaped once and twice) appears in any output file, and that <scratch> appears in driver-config.json, meta.json, stream.jsonl and stderr.txt.
- docs/uat.md UAT-05: the candidate 4 verdict changes from pass to fail under D50's literal reading. The row's Expected and Fail text are unchanged.

### Open issues

- The UAT-04 fsck index.files exit 1 is recorded as 'under investigation': the wave 15 services seat's report is not merged on this base. When it lands, the coordinator should replace that clause with its finding.
- live/report-c4.md still shows 'C4.4 Result: partial' and 'UAT-05 Result: pass'. It is the coordinator's verbatim render of the part results, so I left it alone. The ledger/checklist rows for C4.4 and UAT-05 need the D50 labels, and the ledger is coordinator-only.
- The live driver still leaves the host's project slug for the scratch directory (for example C--Users-Quant-AppData-Local-Temp-claude-qompack-live-...), which also exposes the OS user name. D50 names only the JSON-escaped path, so this was not in scope; if wanted, it could be one more entry in scrub_forms.
- The scrub now changes byte lengths inside stream.jsonl. Any future lane that measures response sizes from the stream must measure them from probe outputs or the host's persisted files instead; the driver docstring says so.

## Independent review

### review:docsb: sound

- **nit** `commit 0dd90863 (internal/contract/refresh.go)` — The commit body has no 'Refs: V6-VERIFY, C4.10' footer. The other four commits in the range carry it, and so do the recent commits that touch internal/contract.
  - Evidence: `git log fe6b27fa..HEAD`: 0dd90863's body ends at 'the comment named only the mint.' with no Refs line. 78201daa, 26427099, 77d2c186 and 99d9b9eb all end with 'Refs: V6-VERIFY, C4.10'.
  - Fix: When the coordinator next rebases or merges, reword 0dd90863 to add 'Refs: V6-VERIFY, C4.10'. If the house rule is only 'should', accept it as is.
- **nit** `docs/uat.md:1505 (UAT-12 Result), docs/uat.md:~812 (UAT-06 Result)` — The reflowed Result text blocks break lines unevenly. In UAT-12, line 1505 is 129 characters, well past the block's ~100-column wrap. In UAT-06, a line ends early at 'hold there. Fixed since candidate', which leaves a ragged break.
  - Evidence: awk length check: docs/uat.md:1504 is 101 characters and 1505 is 129 ('truncated page always carries next_span, and an explicit span pages like full: true). ORDER: steps 2-5 ran after step 6, on the').
  - Fix: Rewrap the edited UAT-12 and UAT-06 Result lines to the block's ~100-column width. The content does not change.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


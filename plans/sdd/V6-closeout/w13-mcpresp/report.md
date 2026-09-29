# Wave 13 — w13-mcpresp (live-lane defects, D45)

Branch `closeout/w13-mcpresp`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `992443c0dc93ee36d0372de4280ad664602c2989`

### Root cause

D2: ResolveSpan limited only the raw content window, then the handler JSON-encoded it inside a body of other fields. The envelope plus escaping (up to 6x per character) went over the limit. Following next_span also broke: a cursor inside a chunk was moved back to the chunk start on the next call, chunk boundaries could split UTF-8 characters, and full ignored span. D6 (fidelity): per-capture fidelity exists only in capture sidecars with no lookup from a root or tool_use_id, and no component computes coverage per object, so the docs overstated the response. Retrieval D6 (selectors): search.go matched path only by equality, suffix or substring, and compared tool against the stored display name (FileRead) without mapping host names (Read). D9: parseRecallQuery and alreadyTried accepted empty values. authorizeHash reported every provenance failure with one sentence and no 'searched' field. runMCP laid out .qompack, opened logs and recorded the handshake before reading runtime.mode. The IPC client answers mode off with OK and no data, which forwardMCPCall read as 'the qompack daemon returned an empty result'.

### Summary

Wave 13 seat mcpresp, branch closeout/w13-mcpresp in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-mcpresp. It is cut from 84d2512 and carries 7 commits, not pushed. All six D45 items for this seat are done. Every product defect was first reproduced by a deterministic test that failed before the fix; those failing logs are under plans/sdd/V6-closeout/w13-mcpresp/runs/.

(1) install D2 / UAT-12 (a): maxResponseBytes limited the content, not what the tool returns.
- Root cause: ResolveSpan limited only the raw content window. handlers_span.go then JSON-encoded that window inside a body of about 300 bytes of other fields. JSON escaping can grow content up to 6x: quotes and backslashes double, and control characters and invalid UTF-8 bytes become 6-byte escapes.
- Reproduced: at the default limit, a 324,902-byte log gave 275,150 bytes of result text against 262,144. At limit 4096, escape-heavy content gave 7,262 bytes.
- Fix: new internal/mcp/response_bound.go. `boundedContent` renders the body, measures its text, and while it is over the limit cuts the window back. It cuts to a chunk start when one is inside the kept part, otherwise to a character boundary. It then redacts the raw window again and re-renders, reporting `truncated` and `next_span`.
- Following `next_span` also had to work. Before this change, a cut inside a chunk was moved back to the chunk start on the next call, which served the same bytes again or looped on the same page forever. A byte-level chunk boundary could also split a UTF-8 character, which reached the model as U+FFFD. And `full` ignored `span`, so a project with `retrieval.defaultSpan: full` could never page.
- So I added two span options, set only by expand and re_read: `ExactStart` (an explicit byte span starts exactly at its offset) and `RuneSafe` (a page never ends inside a multi-byte character; the extra bytes are read only when the tail really is incomplete). An explicit `span` now takes precedence over `full`.

(2) install D6 / F-UAT02-1, fidelity and coverage: I corrected the docs rather than adding fields.
- The data does not exist in a form I could add honestly. Per-capture fidelity lives only in capture sidecars keyed by observation id. There is no index from a root or tool_use_id to a sidecar; the only link is an in-memory observation-to-record map in the store, outside this seat's scope. MCP self-records and legacy records have no sidecar, and one root can come from several captures with different fidelities.
- No component computes coverage for an object.
- user-guide.md and mcp-tools.md now say the responses carry no fidelity or coverage field, and where each value does appear: fidelity on the capture record and in fsck's tally, coverage on `dropped` entries.

(3) retrieval D6, recall selectors:
- `path:` with `*`, `?` or `[` is now a glob matched against the whole path (scored as exact) and against every trailing part of it (scored as suffix). A malformed pattern returns the new `store.ErrBadPathGlob`, which reaches the model as a tool error. Plain paths match exactly as before.
- `tool:` now accepts host names. The host-to-display name table moved from observer to `core.DisplayToolName` (observer now delegates to it). Both sides are compared as display names, case-insensitively.

(4) retrieval D9 / C4.7, small message defects:
- An empty or blank recall query, or one with only empty selectors, is now a tool error.
- already_tried with an empty target or approach is now a tool error.
- The never-stored-hash reply still says `available:false`. It now adds `searched` ("the root index, including every indexed root's chunk list") and says no indexed root or chunk carries the hash.
- Mode off: `qompack mcp` reads the configuration before writing anything. It then lists the same 8 tools and answers every call with the new `mcp.ModeOffText` as a tool error, writing nothing and starting no daemon. It shares the inactive server with the home-directory refusal (D18). The forwarding handler, which `qompack recall` also uses, gives the same text when the client reports mode off.

(5) C45-2: docs/commands.md (generated) now says a non-zero exit reaches the user only as Claude Code's "Shell command failed" line.

(6) docs/uat.md and the C1.7 notes, per the audit findings:
- The intro now records the agent-run live lane (D3) on candidate 3: 6 pass, 6 fail, and all 12 rows re-run on the fixed candidate.
- UAT-03 and UAT-06 Result blocks name the `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120` override and say UAT-02 reproduced F-UAT02-4 at the default 1800 s.
- UAT-02's headline now says the non-exact-fidelity capability is unverified on this host.
- UAT-12 gains (c): the binary file was decoded as text by the host before Qompack saw it.
- UAT-01 step 4 was edited because it is stale against the shipped bundle: `devtool bundle` removes the `plugin/` prefix and writes one version into the binary, plugin.json and BUNDLE.json, so the manifest is `.claude-plugin/plugin.json` at the bundle root.
- UAT-01 step 8 is unchanged because it is not stale. `qompack self-test` always runs with no services wired (`DeclareProducers(&Services{})`), so `custom_instructions_accepted` shows not-yet-implemented there. The doc's "reads retired wherever its producer is declared" refers to a live daemon.
- The C1.7 notes are restated as "rehearsed on a Read-only, checkpoint-free store", citing F-UAT03-2, F-C49-2 and the cross-version defect as open at that time. C1.7 stays open.

Tests and checks:
- All new rows pass. Full-package runs pass for internal/core, observer, store, mcp and cli, plus one focused e2e paging row (`TestV5_TombstoneToExpandRoundTrip`).
- TestBudgetBF failed once under load in the first full mcp run (p95 360 ms against 250 ms). It passed alone (94 s) and in the final full mcp run. It is a known wall-clock test on this machine.
- Lint, fmt-check, go vet (Windows and GOOS=linux), test/docs and both gen --check commands are clean.
- No check was weakened.

Criterion changes, with rationale:
- (a) span.go's first listed property now says an `ExactStart` byte window begins at the offset it names. The resolver's default is unchanged, and `TestExplicitByteSpanAlignsOutward` and the property tests still pass unmodified.
- (b) In expand, an explicit byte span now starts exactly at its offset instead of the chunk start. The page still contains everything requested, and without this any cut inside a chunk cannot be followed.
- (c) An explicit `span` takes precedence over `full`.
- (d) Empty recall and already_tried arguments are now tool errors instead of answers.
- (e) The unknown-hash reason text changed and gained `searched`. The existing assertions (`available:false`, reason mentions provenance) still pass.
- (f) UAT-01 step 4's expectation text changed, as justified above.

No new budget or bound numbers were introduced.

### Commits

- 813c8f41 feat(store): make recall path: a glob and tool: take host names
- 0c53b089 fix(mcp): bound the response text, not only the content span
- d41797d6 fix(mcp): refuse empty recall and already_tried arguments
- 78488ecd fix(cli): make mode-off qompack mcp write nothing and say so
- 4e6349cb docs(mcp): document response bounds, selectors and mode off
- bc6a091d docs(uat): record the live lane run and the audit's scope fixes
- 992443c0 test(mcp): add the wave 13 mcpresp evidence logs

### Tests

- `go test -p 2 ./internal/store -run 'TestSearch_PathSelectorIsAGlob|TestSearch_PathGlobWholeKeyOutranksSuffix|TestSearch_PlainPathKeepsExactSuffixAndContains|TestSearch_MalformedPathGlobIsAnError|TestSearch_ToolSelectorAcceptsHostNames' -count=1 (before fix)` — RED as expected: 4 FAIL, PlainPath passes (it guards the old behaviour) (runs/red-store.txt)
- `go test -p 2 ./internal/mcp -run 'TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy|TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy|TestExpandMinimalSpanResponseTextFitsMaxResponseBytes|TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot' -count=1 (before fix)` — RED: 7262/7274/7263 bytes vs 4096; 275150 vs 262144 (runs/red-bound.txt)
- `go test -p 2 ./internal/mcp -run 'TestRecallEmptyQueryIsAToolError|TestAlreadyTriedEmptyTargetOrApproachIsAToolError|TestExpandNeverStoredHashNamesWhatWasSearched' -count=1 (before fix)` — RED: 3 FAIL, e.g. already_tried answered {"state":"absent"} (runs/red-messages.txt)
- `go test -p 2 ./internal/cli -run 'TestCmdMCPModeOffWritesNothingAndSaysSo|TestForwardMCPCallModeOffSaysSo' -count=1 (before fix)` — RED: actual 'the qompack daemon returned an empty result' (the live string) (runs/red-modeoff.txt)
- `go test -p 2 ./internal/mcp -run 'TestExpandFollowsNextSpanWhenDefaultSpanIsFull|TestExpandPagesNeverSplitARune|TestResolveSpanExactStartBeginsAtTheNamedOffset|TestResolveSpanRuneSafeNeverEndsInsideARune|TestRecallMalformedPathGlobIsAToolError|TestRecallToolSelectorTakesTheHostName' -count=1` — ok (the split-rune row was RED during development, a 1-byte partial-rune page, before RuneSafe)
- `go test -p 2 ./internal/store -count=1` — ok 448.9s (after all changes; runs/full-store.txt)
- `go test -p 2 ./internal/core -count=1 ; go test -p 2 ./internal/observer -count=1` — ok / ok
- `go test -p 2 ./internal/mcp -count=1 (first full run)` — FAIL only TestBudgetBF under co-load: p95=360ms vs 250ms (runs/full-mcp-1-budgetbf-under-coload.txt)
- `go test -p 2 ./internal/mcp -run '^TestBudgetBF$' -count=1` — ok 93.9s alone (runs/budgetbf-alone.txt)
- `go test -p 2 ./internal/mcp -count=1 (final head)` — ok 160.5s (runs/full-mcp-2-final.txt)
- `go test -p 2 ./internal/cli -count=1` — ok 65.8s (runs/full-cli.txt)
- `go test -p 2 ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$' -count=1` — ok 8.3s (runs/e2e-tombstone-to-expand.txt)
- `go test -p 2 ./tools/devtool -run 'TestMCPToolsDocsUpToDate|TestPluginValidateSeesEightTools|TestGenMCPDocsCheckDetectsDrift|TestMCPDocsGlanceLinksResolve|TestGenCommandDocs_RendersEverySection75Command|TestGenCommandDocs_MatchesTheInstalledHelp|TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand|TestGenCommandDocs_CommittedPageIsCurrent|TestGenCommandDocs_RejectsAMismatchedManifest' -count=1` — ok
- `go test -p 2 ./test/docs -count=1` — ok
- `go run ./tools/devtool gen-mcp-docs --check ; go run ./tools/devtool gen-command-docs --check` — both up to date
- `go run ./tools/devtool fmt-check ; go vet (and GOOS=linux go vet) ./internal/mcp ./internal/store ./internal/cli ./internal/core ./internal/observer ./tools/devtool` — clean, exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS (runs/lint-summary.txt); runpatterns and docmarkers re-run after adding the runs/ logs: PASS

### Criterion changes

- span.go: the first listed property now says an ExactStart explicit byte window begins at the offset it names. Rationale: a cut inside a chunk must be followable. The resolver's default is unchanged and all resolver property and table tests pass unmodified.
- expand: an explicit '<off>:<len>' span starts exactly at off (ExactStart), and a window stopping short of the object's end never splits a UTF-8 character (RuneSafe). Rationale: without both, a cut page either re-serves bytes, loops forever on the same page, or shows the model U+FFFD. The existing handler assertions (TestExpandSpanPaging, the x02 e2e paging row) still pass.
- An explicit span now takes precedence over full (including retrieval.defaultSpan full). Rationale: a full read always starts at 0, so paging was impossible.
- An empty or blank recall query (or one with only empty selectors) and an empty already_tried target or approach are now tool errors instead of answers. Rationale: the CLI already refuses an empty query, and answering 'absent' to an unasked question is a false claim.
- The never-stored-hash reply keeps available:false but gains 'searched' and a reason naming what was looked at. The existing TestExpandUnknownHashHasUnavailableProvenance assertions are unchanged and pass.
- docs/uat.md UAT-01 step 4: the instruction and expectation now point at .claude-plugin/plugin.json at the bundle root and its stamped version. Rationale: the text was stale against how devtool bundle builds the shipped bundle. No other expectation or verdict text was edited; Result-block wording was added only where the audit asked for it.

### Open issues

- UAT-02 step 6 and UAT-12 steps 3-4 still expect a Fidelity on the expand/re_read response. I fixed D6 in the docs rather than adding the field, and this seat may not edit those expectations, so a re-run will again record them as not observed until the coordinator decides.
- Only expand and re_read are held to maxResponseBytes. recall is bounded by its structure (at most 50 hits with 120-byte summaries). timeline, dropped and why are not measured against the key: they bypass boundedContent and would need their own truncation rule if one of them ever exceeds it. The JSON-RPC line around the result text adds _meta and escapes the text a second time, so it can exceed the key by roughly that amount.
- re_read has no span argument, so a cut re_read page continues through expand with the response's hash (now documented and tested). Adding a span argument to re_read would change the tool schema, so I left it.
- Belongs to the ledger seat, not fixed: _meta.qompack.hash names the response's own ephemeral capture, and MCP self-records that carry a path crowd real files out of path: answers (UAT-07 observation).
- Not in D45's list: the host lists the subagent tool as Task but names its tool_use Agent (F-UAT02-6), and the name table maps only Task to AgentTool, so tool:Agent matches only records stored under the literal name Agent.
- TestBudgetBF is wall-clock sensitive on this shared machine: it failed once under full-package load and passed alone and in the final full run. This is a known pre-existing timing test, not a regression from this change (the extra UTF-8 read happens only when a page tail really is an incomplete character).
- The live rows (UAT-07 steps 2-3 and 7, UAT-12 (a), C4.4 C44-1/5, C4.5 C45-2, C4.7 F1/F2) need re-running on the fixed candidate. No real Claude Code session was run here.

### Needs the owner

- No new budget or bound numbers were introduced.
- Decision (D6): expand/re_read carry no fidelity or coverage field, and this seat fixed the docs instead of adding one. Please confirm, and decide whether UAT-02 step 6 and UAT-12 steps 3-4 (which still expect a Fidelity on the response) should be reworded or wait for a later feature. Adding a real field needs a store capability that finds a capture's record from a tool_use_id or root; it does not exist today, and MCP and legacy records have no capture record at all.
- Decision: runtime.mcp.maxResponseBytes now bounds the result text the model receives (the JSON body, escaping included), not the JSON-RPC line. If the owner wants the whole line bounded, the key's meaning and docs change and the bound must also cover the _meta fields and the second round of escaping.
- Decision: an explicit byte span in expand now starts exactly at its offset instead of the enclosing chunk's start, and an explicit span takes precedence over full. Both are needed so a cut page can always be followed; please confirm, as they change what a mid-chunk span has always returned.
- UAT-01 step 8 was left unchanged because it is not stale: qompack self-test always runs with no services wired, so custom_instructions_accepted reads not-yet-implemented there, and 'retired' appears only on a live daemon. Rewording it to say this would be a clarity edit, not a staleness fix, and needs coordinator approval.

## Independent review

### review:mcpresp: needs-fixes

- **major** `docs/user-guide.md:352-355 (also docs/mcp-tools.md:42 via tools/devtool/genmcpdocs.go:132)` — Item (2) was fixed by changing the docs rather than the code, and the new text makes two claims the code does not back. 'Coverage is what `dropped` entries carry' is false. 'qompack fsck reports the store's fidelity tally', written inside the section that defines the core.Fidelity enum, points at a different enumeration.
  - Evidence: The dropped response is droppedBody{Drops []checkpoint.DropEntry}, and DropEntry holds only {kind, id, detail} (internal/checkpoint/types.go:155-162). No coverage field reaches any MCP response: core.Coverage values are set only in internal/admission (pipeline.go:229, types.go:345) and on doctor rows (internal/cli/doctor.go:593). fsck's tally comes from store.RestoreOriginal and counts store.Fidelity values (exact/full/canonical/unavailable/corrupt, internal/store/lifecycle.go:39-50). That is not the core.Fidelity vocabulary (exact/prefix/partial/redacted/truncated/binary/failure/unknown, internal/core/evidence.go:35-42) that the section describes and that UAT-02 and UAT-12 look for. The docs correction replaces one unsupported field claim with another.
  - Fix: Delete the dropped-entries coverage sentence. Say instead that no retrieval response carries a core.Coverage value, and name where it does appear (doctor rows, admission records), or say it is not surfaced at all. Reword the fsck sentence to say fsck tallies store-level restore fidelity (exact/full/canonical/unavailable/corrupt), a different enum from capture fidelity, and that capture fidelity appears only in the capture sidecar. Change genmcpdocs.go:132 the same way and regenerate mcp-tools.md with gen-mcp-docs.
- **minor** `internal/mcp/response_bound.go:28-31, 59-64 (boundedContent / shrinkSpan)` — The comment says re-redacting a shorter raw window is safe, because a cut is never applied to redacted bytes and a shorter window 'can only fire a subset' of the rules. That misses partial matches. When a cut lands inside a region the first round redacted, the kept prefix no longer matches the rule. The prefix is served unredacted, and the next page, which starts exactly there under ExactStart, serves the rest.
  - Evidence: pem_private_key is `(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----` (internal/redact/rules.go:84), and github_token needs 36+ characters. Scenario: a window over the limit contains a PEM block. shrinkSpan cuts on a rune boundary inside it, and the second-round Redact(span.Body) sees BEGIN with no END, so the first half of the key is served. The next_span page sees END with no BEGIN, so the second half is served too. This class of split already existed at chunk and page boundaries, and retrieval redaction is a second line behind capture-time redaction. But this change adds a new cut site, and its safety argument is wrong.
  - Fix: After each cut, check that Redact(kept raw) equals a prefix of the first-round Redact(full window). If it does not, move the cut back before the start of the redacted region; the redactor would need to report match ranges for this. Correct the comment either way. Also cap the loop's iterations, because a large redacted block with a small excess makes every round cut only about excess raw bytes.
- **minor** `docs/config-reference.md:85, internal/config/runtime.go:79 (runtime.mcp.maxResponseBytes)` — The key is documented as 'maximum bytes an MCP tool response may return', but only expand and re_read now go through boundedContent. recall, timeline, dropped and why are not measured against it. The implementer disclosed this in open_issues, but neither the key's doc tag nor mcp-tools.md says the bound covers the two content tools only.
  - Evidence: response_bound.go is called only from handlers_span.go (expand, reRead). jsonResponse (handlers_common.go:319) has no size check. The implementer's own open issue says timeline/dropped/why 'bypass boundedContent'.
  - Fix: Either bound jsonResponse generically (truncate list-valued bodies and add a truncated marker), or narrow the doc tag in config/runtime.go and regenerate config-reference.md so it says the key bounds the result text of expand and re_read, and list the unbounded tools for the owner.
- **minor** `docs/uat.md:131 and :149 (UAT-01 step 5)` — Step 4 was edited because `devtool bundle` removes the `plugin/` prefix. Step 5 has the same staleness and was left alone: it still tells the tester to read `plugin/hooks/hooks.json` 'in the installed bundle', and the handoff does not mention it.
  - Evidence: tools/devtool/bundle.go:43 pluginTreePrefix = "plugin/" is stripped, so the installed bundle has hooks/hooks.json at its root. docs/uat.md:131 '5. Record the hook entry points ... from `plugin/hooks/hooks.json` in the installed bundle'.
  - Fix: Report step 5 to the coordinator as the same staleness as step 4. If the scope allows, change the path to `hooks/hooks.json` at the installed bundle's root, with the same justification, and leave the expectation's event list unchanged.
- **nit** `tools/devtool/genmcpdocs.go:120` — The generator hardcodes the default as the literal string '(default `262144`)' instead of rendering it from config.Defaults(). A later change to the default would leave the generated doc wrong while gen-mcp-docs --check stays green. nomagic does not see it because it is inside a string.
  - Evidence: b.WriteString("**Responses are bounded.** `runtime.mcp.maxResponseBytes` (default `262144`) bounds the\n")
  - Fix: Render it with fmt.Fprintf(&b, ..., config.Defaults().Runtime.MCP.MaxResponseBytes), as the file already does for mcp.HomeRootRefusedText and ModeOffText.
- **nit** `git log 84d2512..992443c0: 4e6349cb, bc6a091d, 992443c0` — Three of the seven commits have no Refs footer. The other four carry 'Refs: V6-VERIFY, C4.4/C4.10'.
  - Evidence: `git log --format=%B` shows no Refs line on docs(mcp) 4e6349cb, docs(uat) bc6a091d or test(mcp) 992443c0. Subjects are conventional, and no commit has an attribution trailer.
  - Fix: When the branch is next rewritten, add 'Refs: V6-VERIFY, C4.4/C4.10' (or the matching D45 or audit reference) to these three commit messages.

## Fix seat (review resolution) — status `done`, head `7055bb58`

### Root cause

Reviewer finding 2: boundedContent re-redacted only the kept raw prefix after each cut. A redaction rule that needs both ends of its match (the PEM private-key rule) does not match half a block. A cut inside the block therefore served the kept half in the clear, and the next page, which starts exactly at the cut under ExactStart, served the other half. Reviewer finding 1: the docs named fidelity and coverage sources that do not exist. Drop entries have no coverage, and fsck tallies store restore fidelity, not capture fidelity.

### Summary

Fix seat for w13-mcpresp, working on branch closeout/w13-mcpresp in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-mcpresp. I added five commits on top of the implementer's head 992443c0. I checked all four reviewer findings against the code and all four were correct. Finding 1 (major) and finding 2 (the redaction split) turned out to be real defects. Finding 2 is a secret leak, reproduced RED before the fix.

## Review resolution
1. MAJOR, docs/user-guide.md and genmcpdocs.go: the fidelity/coverage claims were wrong. **Confirmed and fixed (289fdf6d).**
   - Evidence: `checkpoint.DropEntry` holds only kind, id and detail (internal/checkpoint/types.go:155-162).
   - fsck's `fidelity:` line counts `store.Fidelity` values: exact/full/canonical/unavailable/corrupt (internal/store/lifecycle.go:39-50, internal/cli/fsck.go:2026-2071). That is not the `core.Fidelity` enum.
   - Capture fidelity is written to the capture sidecar (internal/store/capture_sidecar.go:60), and no command or tool reads it back.
   - `core.Coverage` values are set only on `admission.Record` (pipeline.go:229, types.go:345).
   - doctor's coverage column is the contract observation vocabulary. `already_tried` states missing coverage in its `reason` and `note`, not as a value.
   - What changed: the user-guide section now says each of these things. The same false claim appeared twice more in the guide: "Each entry carries a `Coverage` value" under /qompack:dropped, and "with coverage attached" under the `dropped` tool. I corrected both. genmcpdocs.go was reworded the same way and docs/mcp-tools.md regenerated with gen-mcp-docs.
2. MINOR, response_bound.go: a cut could land inside a redacted region. **Confirmed as a real leak, fixed test-first (4dd662e1).**
   - Root cause: after a cut, only the kept raw bytes are re-redacted. A rule that needs both ends of its match does not fire on half of it.
   - The new row TestExpandBoundCutNeverSplitsARedactedRegion uses one single-chunk object: prose, then a private-key block stored with capture-time redaction off, then an escape-heavy tail. Explicit spans start 3,600 to 3,760 bytes before the block, and each is followed by next_span.
   - It was RED on the old code: leads 3,660-3,684 served "-----BEGIN PRIV..ATE KEY-----" in page 0, and the block body on the next page.
   - Fix: new `safeCut` accepts a cut only when Redact(kept) ++ Redact(rest) equals Redact(whole window) byte for byte. Otherwise it moves the cut back by a doubling distance. The redactor interface is unchanged, so no offsets cross the mcp/redact seam.
   - The reviewer's iteration-cap concern is handled without a new bound number. `shrinkSpan`'s charge now doubles with each round, and a charge past the window keeps only the first rune. The loop therefore converges in a logarithmic number of rounds; pinned by TestShrinkSpanChargeDoublesEachRound.
   - The comments now describe the real safety argument.
   - Still true, and not new: a redacted match that straddles a window's outer edge, set by ResolveSpan or a chunk boundary, is only as safe as capture-time redaction. That is the reviewer's "already existed" class.
3. MINOR, maxResponseBytes docs: the key was described as bounding every tool. **Confirmed and fixed by narrowing the docs (83022552 and 289fdf6d).**
   - The doc tag now reads "maximum bytes of result text the expand and re_read tools return (content JSON-escaped, with its envelope); other tools are not measured against it".
   - config-reference.md was regenerated. I edited the schema golden's one description by hand, following the 04a43a16 precedent, because its -update refuses Windows platform defaults.
   - mcp-tools.md names the unmeasured tools: recall, already_tried, record_eliminated, timeline, why, dropped. The user guide says the other tools are not measured.
   - Bounding the other tools generically is left for the owner.
4. MINOR, UAT-01 step 5: same staleness as step 4. **Confirmed and fixed (99f06296).**
   - Step 5 now reads `hooks/hooks.json` at the installed bundle's root. Justification: bundle.go strips `plugin/`, and the live lane read hooks/hooks.json at the bundle root (uat/UAT-01/cli/s5-s6-host-discovery.txt).
   - The expectation text and the seven-event list are unchanged.

## Tests (Windows, -p 2, machine co-loaded; nothing failed, so no re-runs were needed)
- The focused bound rows ran together, all PASS: TestExpandBoundCutNeverSplitsARedactedRegion, TestShrinkSpanChargeDoublesEachRound, TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy, TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy, TestExpandMinimalSpanResponseTextFitsMaxResponseBytes, TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot, TestExpandFollowsNextSpanWhenDefaultSpanIsFull, TestExpandPagesNeverSplitARune.
- Full packages: internal/mcp ok (160.8s), internal/config ok, test/docs ok, tools/devtool ok (91.3s).
- gen-mcp-docs, gen-config-docs and gen-command-docs --check all report up to date. fmt-check is clean, and go vet is clean on Windows and GOOS=linux for internal/mcp, internal/config and tools/devtool. The lint subset passed.
- Evidence: plans/sdd/V6-closeout/w13-mcpresp/runs/fix-*.txt. The RED log's served key delimiter is rewritten as PRIV[..]ATE so no contiguous credential shape is committed. That RED run used the wider 3600-3900 lead range, which I narrowed to 3600-3760 afterwards.
- I did not run the Linux container, real Claude Code sessions, hot-path rows, whole e2e/integration packages, or any load generator.

## Criterion changes
- None to any check. The only golden change is the schema's description of runtime.mcp.maxResponseBytes, a documentation string that follows the intended doc-tag change; no output was regenerated to match broken behaviour.

## Behaviour changes for the coordinator
- In the pathological case (a window that is mostly redacted), a page may now be shorter than the largest that would fit. That trade buys never splitting a redacted region and guaranteed convergence.
- A new error is possible: "no page of this content fits runtime.mcp.maxResponseBytes ... without cutting through content the privacy policy redacts". It is returned only when no safe cut leaves any content.

### Commits

- 4dd662e1 fix(mcp): never cut a bounded page inside a redacted region
- 289fdf6d docs(mcp): say where fidelity and coverage actually appear
- 83022552 docs(config): scope maxResponseBytes to expand and re_read
- 99f06296 docs(uat): read UAT-01's hooks.json at the bundle root
- 7055bb58 test(mcp): add the w13 mcpresp fix seat evidence logs

### Tests

- `go test -p 2 ./internal/mcp -run 'TestExpandBoundCutNeverSplitsARedactedRegion' -count=1 -v (before the fix, at implementer head 992443c0 + new test)` — FAIL as intended: lead 3660 page 0 span [4340 8027] served the private-key BEGIN delimiter in the clear; a temporary diagnostic showed leads 3660-3684 leaking
- `go test -p 2 ./internal/mcp -run 'TestExpandBoundCutNeverSplitsARedactedRegion' -count=1 -v` — PASS (10.7s) after safeCut
- `go test -p 2 ./internal/mcp -run 'TestShrinkSpanChargeDoublesEachRound' -count=1` — PASS
- `go test -p 2 ./internal/mcp -run 'TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy' -count=1 (run together with TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy, TestExpandMinimalSpanResponseTextFitsMaxResponseBytes, TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot, TestExpandFollowsNextSpanWhenDefaultSpanIsFull, TestExpandPagesNeverSplitARune)` — all PASS
- `go test -p 2 ./internal/config -run 'TestJSONSchema_Golden' -count=1` — PASS
- `go test -p 2 ./internal/mcp ./internal/config ./test/docs -count=1` — ok / ok / ok (internal/mcp 160.8s)
- `go test -p 2 ./tools/devtool -count=1` — ok (91.3s)
- `go run ./tools/devtool gen-mcp-docs --check && go run ./tools/devtool gen-config-docs --check && go run ./tools/devtool gen-command-docs --check` — all up to date
- `go run ./tools/devtool fmt-check; go vet ./internal/mcp ./internal/config ./tools/devtool; GOOS=linux go vet (same)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS

### Criterion changes

- None to any check. testdata/golden/config/schema.json changed by exactly one description string (runtime.mcp.maxResponseBytes), which follows the intended doc-tag change; it was not regenerated to match broken output.

### Open issues

- A redacted match that straddles a retrieval window's outer edge (the ResolveSpan window end or a page start) is still only as safe as capture-time redaction. This existed before this seat's work; the new cut site no longer adds to it. Closing it needs ResolveSpan to widen windows past redacted regions, or the redactor to report match ranges across the mcp/redact seam.
- recall, already_tried, record_eliminated, timeline, why and dropped results are not measured against runtime.mcp.maxResponseBytes (now documented as such). Bounding them generically is not done.
- UAT-02 step 6 and UAT-12 steps 3-4 still expect a Fidelity on expand/re_read responses, which the docs now say no retrieval response carries (implementer's D6 decision, carried below).
- The coordinator re-runs the live rows (install D2, D6, retrieval D6/D9, C4.7) on the fixed candidate.

### Needs the owner

- No new budget or bound numbers were introduced by either seat. The fix seat's convergence rule (charge doubles per round, safeCut back-off doubles) is algorithmic and has no tunable value; unicodeEscapeLen = 6 is encoding/json's u-escape width, hoisted from an existing local constant.
- Decision (D6): no retrieval response carries a fidelity or coverage field, and the docs, not the code, were corrected. Now accurate: capture fidelity is on the capture sidecar (no tool surfaces it), fsck's fidelity line is store-level restore fidelity (a different enum), and coverage values exist only on admission records. Please confirm, and decide whether UAT-02 step 6 and UAT-12 steps 3-4 (which still expect a Fidelity on the response) should be reworded or wait for a later feature. A real field needs a store capability that finds a capture's record from a tool_use_id or root; it does not exist today, and MCP and legacy records have no capture record.
- Decision: runtime.mcp.maxResponseBytes bounds only the result text of expand and re_read (the JSON body, escaping included), not the JSON-RPC line and not the other six tools. The key's doc tag, config-reference.md and mcp-tools.md now say so. If the owner wants the whole line or all tools bounded, the key's meaning and docs change.
- Decision: an explicit byte span in expand starts exactly at its offset instead of the enclosing chunk's start, and an explicit span takes precedence over full (implementer). Both are needed so a cut page can always be followed; please confirm, as they change what a mid-chunk span has always returned.
- Decision (fix seat): a bounded page may be shorter than the largest that fits when a redacted region lies near the cut, and a new tool error ('no page of this content fits runtime.mcp.maxResponseBytes ... without cutting through content the privacy policy redacts') is returned only when no safe cut leaves any content. Please confirm this trade (never split a redacted region over maximal page size).
- UAT-01 step 8 was left unchanged because it is not stale: qompack self-test always runs with no services wired, so custom_instructions_accepted reads not-yet-implemented there, and 'retired' appears only on a live daemon. Rewording it would be a clarity edit, not a staleness fix, and needs coordinator approval.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/mcp/response_bound.go:91-106 (safeCut), surfaced at response_bound.go:71-74` — safeCut backs off by doubling (1, 2, 4, ...) and gives up as soon as keep-back <= 0. When a redacted region starts before keep/2, the next power of two past (keep - regionStart) can already be >= keep. The loop then skips over every safe cut before the region, returns false, and boundedContent returns the new "no page of this content fits ... without cutting through content the privacy policy redacts" error even though a safe page exists. So the fix seat's owner-facing claim that this error is returned "only when no safe cut leaves any content" is false. The comment's "lands within a factor of two of the redacted region's start" also does not hold at the window's lower edge. There is no leak; this is a spurious refusal.
  - Evidence: I ran a read-only probe with go test -overlay and a scratch test file, so the worktree was not modified. The redactor was a fake `(?s)BEGIN.*?END` -> `<r>` redactor. (a) safeCut on window = 630x'a' + BEGIN + 2100x'x' + END + 300x'b' with keep=2730 returned cut=0, ok=false, while safeCut(keep=630) on the same window returned 630, ok=true. (b) boundedContent end to end with MaxResponseBytes=4096 and body = 650x\x01 + BEGIN + 2100x'x' + END + 100x'b' returned isError=true with "expand failed: no page of this content fits runtime.mcp.maxResponseBytes (4096 bytes) without cutting through content the privacy policy redacts". The first 500 bytes alone render to 3223 bytes, which fits. Real rules can match regions this large: pem_private_key's `(?s)...*?` runs to the next END, and the assignment, bearer and JWT rules have unbounded runs. Reaching it needs escape-heavy content before a redacted region that covers more than half of the kept window, so it is pathological, but the path is reachable through expand and re_read.
  - Fix: When keep-back would drop to <= 0 (or below the last unsafe cut's lower neighbour), do not give up. Search the interval between 0 and the last unsafe cut instead: binary-search down from the last unsafe cut for the largest safe cut, or step back linearly from the smallest probed unsafe point. Only return false after cut=first-rune-boundary has been tried and found unsafe. Add a row to TestExpandBoundCutNeverSplitsARedactedRegion, or a unit row on safeCut, with a redacted region starting before keep/2. Then correct the needs_owner statement and the safeCut comment.
- **nit** `docs/uat.md:1119 (UAT expectation for the dropped report)` — The false claim behind finding 1, that drop entries carry coverage, survives in the UAT expectation text: "Steps 3 and 5: the drop report is Qompack's recorded omissions with coverage attached". The fix seat corrected the identical phrase in docs/user-guide.md ("with coverage attached" under the `dropped` tool) and said the guide's other occurrences were fixed. This one was neither changed nor reported in open_issues or needs_owner. Only UAT-02 and UAT-12's fidelity expectations were carried to the owner.
  - Evidence: grep over the worktree at HEAD 7055bb58 finds `docs/uat.md:1119:- Steps 3 and 5: the drop report is Qompack's recorded omissions with coverage attached`. droppedBody (internal/mcp/handlers.go:853-863) carries drops, count, available, reason, denied and host_policy. DropEntry has only kind, id and detail. The corrected docs/user-guide.md and docs/mcp-tools.md now say that no entry carries a coverage value.
  - Fix: Add docs/uat.md:1119 to the coordinator carry list with the UAT-02/UAT-12 fidelity expectations. It is an expectation line, so rewording it is the coordinator's call. If approved, reword it to "recorded omissions, qualified by availability (available/reason) and withheld counts (denied/host_policy)" and keep the native-context sentence unchanged.


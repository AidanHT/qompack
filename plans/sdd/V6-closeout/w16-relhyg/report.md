# W16-RELHYG: release hygiene: notices, descriptions, marketplace trigger, recall prefix

Branch `closeout/w16-relhyg`. Workflow `wf_90777431-3b4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `4316de93`

### Root cause

F4: assemble() wrote only the plugin tree, binary, BUNDLE.json and checksums.txt, and goreleaser uploads only zips/checksums/marketplace, so no artifact carried a licence; licensesrender.go also hard-coded the false claim that the stdlib is not redistributed. F8: the description literal in pluginmanifest/marketplace/liveeval/e2e helper/cli usage overclaimed compaction and cache awareness, and the pin command text claimed immunity from summarization. F2: marketplace.yml listened to `published` only, which a pre-release-to-release promotion does not fire; recall_rank.go matched a fixed mcp__plugin_qompack_qompack__ prefix that assumes a marketplace entry named qompack.

### Summary

w16-relhyg release hygiene (D53(e); audit F2, F4 and F8). Branch closeout/w16-relhyg, cut from bb54c6ba, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-relhyg. All four items are done in six commits. Every touched package passes in full on Windows, all eight lint sub-checks pass, and two bundle builds are byte-identical.

(1) LICENSE and notices in every bundle (audit F4)
Root cause: assemble() wrote only the pluginmanifest tree, the binary, BUNDLE.json and checksums.txt, and goreleaser uploads only the zips, checksums.txt and marketplace.json, so no released artifact carried any licence text. Separately, licensesrender.go ended with a sentence saying the Go standard library "is not redistributed by this repository". That is false: the Go runtime and standard library are compiled into every binary.
- 2199adf5: `devtool licenses` now reads go.mod's `toolchain` line (go1.26.6) and the LICENSE file in the Go install that `go env GOROOT` reports. It renders that licence in section 2 under "### The Go runtime and standard library" (BSD-3-Clause, covering the golang.org/x code the standard library vendors). The false sentence is removed and THIRD_PARTY_NOTICES.md is regenerated.
- The statically linked module set is unchanged and still derived from `go list -deps` over the six targets: go-winio (MIT), klauspost/compress (BSD-3, Apache-2.0 and MIT parts) and x/sys (BSD-3). TestShippedModulesEqualTheAllowlistIntersection still passes.
- Tests: TestNotices_CarryTheGoLicence failed before the fix and passes after. It searches only inside the Go section, because x/sys ships the same licence words and a page-wide search passed vacuously, which I found while writing the test. TestRenderNotices_GoLicenceIsARedistributedSection is also new.
- 6f1e4a5e: new readBundleLegalFiles reads LICENSE and THIRD_PARTY_NOTICES.md (CRLF folded to LF; a missing or empty file is an error) into bundleAssembly.legal. assemble() puts both at the bundle root, so BUNDLE.json, checksums.txt and the zip all cover them, and it refuses to run without either.
- release-check's determinism step builds its own bundleAssembly, so it now passes the files too; without that it would have failed.
- .goreleaser.yaml extra_files also uploads LICENSE and THIRD_PARTY_NOTICES.md. test/guards/release_test.go TestGoreleaserBuildsNothingAndDraftsTheRelease now requires both globs (a stricter check, not a weaker one).
- Tests: TestAssembleBundle_Layout now expects the two files. New: TestReadBundleLegalFiles_ReadsTheRepositoryCopies, TestReadBundleLegalFiles_MissingFileIsAnError, TestAssembleBundle_ShipsTheLicenceAndNotices, TestAssembleBundle_RefusesWithoutLegalFiles, TestWriteArchive_CarriesTheLicenceAndNotices. The fixture-based tests use fixtureLegalFiles().
- Docs: packaging/README.md's layout list and docs/release.md step 6 are updated. There is nothing to change for plugin-validate: it checks only the plugin/ tree, which does not include the bundle-root legal files.
- Reproducibility: at clean commit 6f1e4a5e, `go run ./tools/devtool bundle --archive --version 0.0.0-relhyg --target windows/amd64 --target linux/amd64 --out <scratch>/repro-{a,b}` was run twice. `diff -r` found no difference; the zip hashes are linux a0e1795c…, windows 776ab490… and checksums.txt 20c05180…. Both zips list LICENSE and THIRD_PARTY_NOTICES.md, and both files match the repository copies byte for byte. Recorded in plans/sdd/V6-closeout/w16-relhyg/runs/bundle-reproducibility.txt (4316de93).

(2) Accurate descriptions (audit F8), b6a5b464
- New pluginmanifest.Description: "Qompack keeps a local record of the session and restores the important parts after Claude Code compacts its context, with tools to recall anything left out."
- Everything that carried the old text now uses it: plugin.json, tools/devtool/marketplace.go (the marketplace description is this sentence plus the existing per-target install hint), tools/devtool/liveeval_host.go, test/e2e/install_helpers_test.go, and the CLI usage line in internal/cli/dispatch.go (text only, because cli may not import pluginmanifest outside tests).
- /qompack:pin now reads "Pin an invariant to re-inject first after each compaction, within the 9,500-character cap; the host's summary can still drop it". I checked docs/architecture.md §7: pins are tier 1, but a pin that does not fit is left out whole and named in section 7, which is why the wording says "first" and "within the cap". docs/user-guide.md is reworded to match.
- Regenerated: plugin/ (`plugin-validate --write`) and docs/commands.md (`gen-command-docs`). The goldens testdata/golden/plugin/.claude-plugin/plugin.json and commands/pin.md, manifest_test.go and envelope_test.go carry the new text. This is a text change; no assertion was removed.

(3) marketplace.yml trigger (audit F2), 19f56522
Root cause: `on.release.types: [published]` plus a pre-release skip. Publishing a pre-release fires `published`, which the job skips; promoting it later fires only `released`, so the job never ran on the documented pre-release-then-promote route.
- Fix: the trigger is `types: [released]`. GitHub fires `released` once for a release published directly and once for a pre-release promoted to one, never for a draft or a pre-release. Adding `published` beside it would run the job twice for a direct release. The `!github.event.release.prerelease` guard stays.
- New guard test TestMarketplaceWorkflowRunsOnceForAFullRelease (test/guards; the repo tests its workflow files there, not in test/release). It failed with `published` and passes now. It requires exactly `[released]`, no push/pull_request/schedule/workflow_run triggers, and the pre-release guard.
- docs/release.md step 7, packaging/README.md and the workflow header comment are updated.

(4) Recall's self-record test (audit F2), 9c3cd776
- isHostQompackToolCall matches `mcp__plugin_<segment>_qompack__<tool>` only when the segment is non-empty and contains no "__", and the tool is exactly one of the eight in toolNamesInDesignOrder (the list ToolNames() returns). The ephemeral `mcp__qompack__` match is unchanged.
- New TestIsRetrievalSelfRecord_AnyPluginSegment covers segments qompack, qompack-windows-amd64, qompack_windows_amd64 and qompack-linux-arm64 against all eight tools; it failed before the fix.
- New TestIsRetrievalSelfRecord_Negatives covers another plugin's tool, another server with a `recall` tool, unknown tools on the qompack server (recall_all, deploy, empty, recallx), a server segment that only ends in "qompack", a missing segment, a segment hiding a second "__", and a standalone non-plugin server. It failed before the fix (recall_all matched).
- recall_k_test.go now spells the observed name literally, since the old prefix constant is gone.

Commands and results (Windows, -p 2, no -race; the daytime limits were followed: no hot-path rows, no whole e2e or integration package, no load generators, Linux container not started):
- go test -p 2 -count=1 -run 'TestIsRetrievalSelfRecord_AnyPluginSegment|TestIsRetrievalSelfRecord_Negatives|TestRecallRanksSelfRecordsAfterOriginalCaptures' ./internal/mcp/ : FAIL before the fix, PASS after.
- go test -p 2 -count=1 -run 'TestMarketplaceWorkflowRunsOnceForAFullRelease' ./test/guards/ : FAIL before the fix, PASS after.
- go test -p 2 -count=1 -run 'TestNotices_CarryTheGoLicence' ./tools/devtool/ : FAIL before the fix, PASS after.
- Full packages, one run each, all ok: tools/devtool 64.3s, internal/mcp 90.9s, internal/cli 77.3s, test/guards 46.3s, test/docs 2.7s, internal/pluginmanifest 1.1s, internal/commands 3.4s.
- go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns : all eight PASS.
- licenses --check, plugin-validate, gen-command-docs --check, gen-mcp-docs --check and fmt-check: all current.
- go vet on every touched package, Windows and GOOS=linux: ok.
- No wall-clock failures occurred, so nothing needed a solo re-run.

Hard-coded plugin_qompack_qompack prefix or /qompack: namespace (listed, not changed; docs that state the namespace are left for the candidate 6 live lane under D53(f)). Only the first item matters to a real release install:
- internal/grammar/statewarn.go `selfMarkerSlashCommand = "/qompack:"`. If a release entry namespaces its commands differently, a slash command typed under that namespace would not be recognised as Qompack's own. Also, its selfMarkerMCPTool covers only `mcp__qompack__`, not the host's `mcp__plugin_*_qompack__` form.
- Not a concern today, because both use a local marketplace entry named `qompack`: internal/eval/livetrial.go LiveQompackToolPrefix and the tools/devtool/liveeval.go copies (liveMCPToolPrefix, liveMCPServerName "plugin:qompack:qompack"), and the allowed-tools entry "mcp__plugin_qompack_qompack" in testdata/eval/live/{pilot,tasks,tasks-v2}.json.
- Tests only: internal/rehydrate/evolution_room_test.go and tier1_order_test.go.
- User-facing strings that name /qompack:status, /qompack:pin or /qompack:<cmd>: internal/daemon/handlers.go (degrade banner), internal/checkpoint/finalize.go, internal/commands/spec.go (usage lines), internal/commands/commands.go (error text). InjectionCloseTag "<!-- /qompack:injected -->" is a marker, not a namespace.
- Docs: docs/uat.md:208, docs/install.md:96, and /qompack: throughout docs/commands.md, user-guide.md, uat.md, cannot-do.md, troubleshooting.md and two ADRs.
- Plans: plans/sdd/V6-closeout/coordinator/live-uat*.js and eval/preregistration.md.

Criterion changes: none weakened. Three assertions changed text: the plugin.json description, the pin summary and its goldens. Three were made stricter: the release guard now requires the two new globs, TestAssembleBundle_Layout now expects two more files, and the marketplace trigger is now pinned.

New bound numbers: none, so there is nothing for needs_owner. The 9,500-character figure in the pin text is the existing rehydrate.HostContextCeilingChars.

No processes of mine are left running. Scratch files are under .../scratchpad/w16/relhyg/.

### Commits

- 9c3cd776 fix(mcp): rank Qompack tool calls last for any plugin segment
- b6a5b464 fix(pluginmanifest): describe what Qompack does, not compaction
- 19f56522 fix(release): pin the marketplace when a pre-release is promoted
- 2199adf5 fix(devtool): reproduce Go's licence in the third-party notices
- 6f1e4a5e fix(devtool): ship LICENSE and third-party notices in every bundle
- 4316de93 test(v6): record bundle reproducibility with the legal files

### Tests

- `go test -p 2 -count=1 -run 'TestIsRetrievalSelfRecord_AnyPluginSegment|TestIsRetrievalSelfRecord_Negatives|TestRecallRanksSelfRecordsAfterOriginalCaptures' ./internal/mcp/` — FAIL before fix (qompack-windows-amd64 segment unmatched; recall_all matched), PASS after
- `go test -p 2 -count=1 -run 'TestMarketplaceWorkflowRunsOnceForAFullRelease' ./test/guards/` — FAIL before fix (types [published]), PASS after
- `go test -p 2 -count=1 -run 'TestNotices_CarryTheGoLicence' ./tools/devtool/` — FAIL before fix, PASS after
- `go test -p 2 -count=1 -run 'TestReadBundleLegalFiles_ReadsTheRepositoryCopies|TestReadBundleLegalFiles_MissingFileIsAnError|TestAssembleBundle_ShipsTheLicenceAndNotices|TestAssembleBundle_RefusesWithoutLegalFiles|TestWriteArchive_CarriesTheLicenceAndNotices|TestAssembleBundle_Layout|TestAssembleBundle_Identity|TestAssembleBundle_Deterministic|TestWriteArchive_MembersMatchTheBundleDirectory|TestWriteArchive_Deterministic' ./tools/devtool/` — PASS (new tests did not compile before bundleAssembly.legal existed)
- `go test -p 2 -count=1 -run 'TestGoreleaserBuildsNothingAndDraftsTheRelease|TestGoreleaserGuardRejectsReshapedYAML|TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck' ./test/guards/` — FAIL before goreleaser edit (glob: LICENSE missing), PASS after
- `go test -p 2 -count=1 -timeout=20m ./tools/devtool/` — ok 64.3s
- `go test -p 2 -count=1 -timeout=20m ./internal/mcp/` — ok 90.9s
- `go test -p 2 -count=1 -timeout=20m ./internal/cli/` — ok 77.3s
- `go test -p 2 -count=1 ./test/guards/ ./test/docs/ ./internal/pluginmanifest/ ./internal/commands/ (one package at a time)` — all ok (46.3s, 2.7s, 1.1s, 3.4s)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS
- `go run ./tools/devtool licenses --check; plugin-validate; gen-command-docs --check; gen-mcp-docs --check; fmt-check` — all current/OK
- `go vet (Windows and GOOS=linux) ./internal/mcp/ ./internal/cli/ ./internal/pluginmanifest/ ./internal/commands/ ./tools/devtool/ ./test/guards/ ./test/e2e/` — ok
- `go run ./tools/devtool bundle --archive --version 0.0.0-relhyg --target windows/amd64 --target linux/amd64 --out <scratch>/repro-{a,b} (twice, at clean 6f1e4a5e) then diff -r` — byte-identical trees and zips; both zips carry LICENSE and THIRD_PARTY_NOTICES.md equal to the repo copies

### Criterion changes

- The expected text of the plugin.json description and the /qompack:pin summary changed in manifest_test.go, envelope_test.go and the goldens testdata/golden/plugin/.claude-plugin/plugin.json and commands/pin.md. This is a text change with no assertion removed: F8 requires the wording.
- TestAssembleBundle_Layout's exact file set now includes LICENSE and THIRD_PARTY_NOTICES.md (stricter).
- TestGoreleaserBuildsNothingAndDraftsTheRelease now also requires extra_files globs for LICENSE and THIRD_PARTY_NOTICES.md (stricter).
- New guard TestMarketplaceWorkflowRunsOnceForAFullRelease pins the trigger to exactly [released] (new check).
- TestRecallRanksSelfRecordsAfterOriginalCaptures now spells the host name mcp__plugin_qompack_qompack__expand literally, because the hostQompackToolPrefix constant was replaced; the assertion is unchanged.

### Open issues

- internal/grammar/statewarn.go recognises Qompack's own slash commands only by the literal "/qompack:" prefix, and its MCP self-marker matches only mcp__qompack__, not the host's mcp__plugin_*_qompack__ form. If a release entry (qompack-<os>-<arch>) namespaces commands or tools differently, those self-originated events go unrecognised. Fixing it is outside this seat's scope.
- The release entry's command and tool namespace is still unobserved; the candidate 6 live lane session from a local entry named qompack-windows-amd64 (D53(f)) will settle it. The eval-only prefixes LiveQompackToolPrefix and liveMCPServerName in tools/devtool/liveeval.go, plus the testdata/eval/live allowed-tools entries, assume an entry named qompack, which the live eval's own local marketplace provides.
- plugin.json keywords still include "cache" and "compaction". They were left alone because the task named only the description; "cache" may still suggest the cache awareness F8 calls unsupported.
- Go's PATENTS grant (GOROOT/PATENTS, and the x/sys PATENTS file) is not reproduced; the notices carry LICENSE files only, as before.
- The rehydration heading "Original user intent (verbatim from L0 capture — never summarized)" and the internal/pins/doc.go comment "never summarized" are outside F8's pin wording and were left unchanged.
- test/e2e/install_helpers_test.go changed one line (the disposable marketplace description now uses pluginmanifest.Description). It is vetted on both OSes but was not run, per the no-whole-e2e rule; the coordinator's nightly e2e run covers it.

## Independent review

### review:relhyg: sound

- **nit** `plans/00-ARCHITECTURE.md:808 (also plans/V1-SP-01-foundation-toolchain-and-contracts.md:62)` — The architecture plan's sample plugin.json still shows the old description "Cache-aware, retrieval-backed context compaction". Every shipped and generated copy was updated, but this plan sample was not.
  - Evidence: `git grep -n 'retrieval-backed'` at HEAD 4316de93 finds no hits outside plans/ apart from a historical comment in manifest.go. The plan sample's homepage (github.com/qompack/qompack) is already stale too, so the block is not being tracked.
  - Fix: Optional. Either point the 00-ARCHITECTURE §3.4 sample at pluginmanifest.Description or add a one-line note that the shipped description changed under D53(e)/F8. This is a plan doc and does not block.
- **nit** `internal/cli/dispatch.go:194-196` — The CLI usage line copies pluginmanifest.Description by hand, because cli may not import pluginmanifest outside tests. No test ties the two texts together, so they can drift. The pin summary's "9,500-character" is also a literal that no test ties to rehydrate.HostContextCeilingChars.
  - Evidence: The only link is the comment "The same claim as pluginmanifest.Description". internal/pluginmanifest/manifest.go:214 hard-codes 9,500, and internal/rehydrate/hostcap.go:26 holds the constant.
  - Fix: Optional. Add a cli _test.go (tests may import pluginmanifest) asserting that writeUsage's header contains Description without its leading "Qompack " and trailing period. Add a guard test asserting the pin Description contains the number in HostContextCeilingChars, formatted with a thousands separator.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.


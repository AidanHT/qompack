# w7b-checkpoint: /qompack:checkpoint is no longer shipped (option B)

Branch `closeout/w7b-checkpoint`. Workflow `wf_ec574dc8-a1f`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `1b3cac3b`

### Root cause

/qompack:checkpoint shelled out to `qompack checkpoint`, which is the PreCompact hook entry point: it reads a hook event from stdin and always exits 0, so the command never sealed or reported anything. The real frontend was never routed (SP-14 H3), and a test exception allowed a shipped command to route to a hook.

### Summary

I chose option B: the plugin no longer ships /qompack:checkpoint. The PreCompact hook and hooks.json are unchanged.

ROOT CAUSE. plugin/commands/checkpoint.md ran `qompack checkpoint`. That subcommand is the PreCompact hook entry point (internal/cli/hooks.go, Cmd{Hook:true}): it reads a hook event from stdin and always exits 0. So the command wrote nothing and reported nothing. The frontend (internal/commands/cmd_checkpoint.go) was never routed; spec.go's unroutedSubcommands and docs/commands.md both marked it "not yet routed" (SP-14 H3). TestSlashCommands_AreDiscoverable carried a special exception that let a shipped command route to a hook.

WHY B, NOT A.
- What A would cost. The session id would not have been the problem: I checked the 2.1.280 binary (bin/claude.exe). A plugin command's getPromptForCommand replaces ${CLAUDE_SESSION_ID} before it runs the ! bash block. But A still needs:
  - a new IPC op, which amends 00-ARCHITECTURE §5.4's closed op list;
  - a daemon handler and Services seam;
  - a sealing entry that skips PreCompact's contract bookkeeping;
  - a changed CheckpointNow signature, a new subcommand name and a regenerated plugin golden.
- The --reason flag has nowhere to go without a checkpoint schema change (SchemaVersion 1 has no reason field).
- Risk: a new wire op and a new daemon write path, added during the release freeze.
- What users would gain is near zero. Rehydration runs only for SessionStart source=compact (internal/rehydrate/build.go: `if r.Source != sourceCompact`). It reads the session's latest checkpoint (rehydrate_service.go latest -> Checkpoints.Latest), and that is the one PreCompact seals at that compaction. A manual seal taken earlier is superseded before anything reads it. Checkpoints are also sealed on the idle cadence (finalizeIfDue).
- B is mostly deletion and leaves the hook alone.

WHAT CHANGED (a0e84956, 24 files, +167/-463):
- internal/pluginmanifest: checkpoint removed from commandSpecs, with a comment saying why. The bundle is now 9 files and 6 commands. plugin/commands/checkpoint.md and testdata/golden/plugin/commands/checkpoint.md are deleted. The golden was removed with the command, not regenerated to match broken output.
- tools/devtool: pluginvalidate wantCommands 7 -> 6, with a derivation comment. The bundle and archive tests are updated. gen-command-docs no longer renders the unrouted marker, and its preamble now says there is no checkpoint command and that checkpoints are automatic, linking cannot-do.md#no-manual-checkpoint-command. docs/commands.md was regenerated with the generator.
- internal/commands: cmd_checkpoint.go, Deps.CheckpointNow, the checkpoint --reason flag entry and the unrouted mechanism (unroutedSubcommands, RouteNote, Spec.Routed) are removed. commandNames has six entries. pin_checkpoint_test.go is renamed pin_test.go, dropping its six TestCheckpoint_* tests, which tested the deleted frontend. The frontend stays in git history in case a real route is built later.
- internal/cli: slashCommandCmds registers every shipped spec. TestSlashCommands_AreDiscoverable now has no exception: a shipped command whose route is a hook fails. This is the regression test that would have caught the defect. TestSlashCommands_CheckpointRemainsTheHook still pins `qompack checkpoint` as exactly one hook entry that exits 0.
- Docs:
  - cannot-do.md §6: the "not routed" entry is replaced by "No manual checkpoint command", which says checkpoints are written automatically before each compaction and that a manual checkpoint is not offered yet.
  - user-guide.md: seven -> six. The /qompack:checkpoint section becomes "Checkpoints are automatic", keeping the observed fact that typing `qompack checkpoint` by hand is still a write.
  - packaging/README.md: seven -> six.
- test/docs: TestUserGuideMarksCheckpointAsUnrouted keeps its name because the SP-18 plan quotes it. When docs/commands.md has no checkpoint row, the guide must not document the command under its own heading and must say "a manual checkpoint is not offered yet".

FAILING TESTS FIRST (logs in plans/sdd/V6-closeout/w7b-checkpoint/runs/):
- red-cli-discoverable.txt: `go test -count=1 -run '^TestSlashCommands_AreDiscoverable$' ./internal/cli/` exit 1 before the fix. Message: "/qompack:checkpoint shells out to `qompack checkpoint`, which is a hook entry point".
- red-pluginmanifest.txt: `go test -count=1 ./internal/pluginmanifest/` exit 1 with the six-command expectation in place. TestManifest_GoldenBytes, TestManifest_SevenCommands and TestManifest_FilesAreStableBytes failed.
- red-docs-userguide-oldguide.txt: `go test -count=1 -run '^TestUserGuideMarksCheckpointAsUnrouted$' ./test/docs/` exit 1 against the old guide (temporarily restored from HEAD, then put back).

Last, I tightened three comments in internal/commands (doc.go, envelope.go, statuscollect.go) and reran vet and fmt-check on them.

### Commits

- a0e84956 fix(plugin): stop shipping the no-op /qompack:checkpoint
- 1b3cac3b docs(v6-closeout): record w7b-checkpoint evidence runs

### Tests

- `go test -count=1 -timeout=30m ./internal/cli/ (at a0e84956)` — ok 57.2s (runs/head-internal-cli.txt)
- `go test -count=1 -timeout=30m ./internal/commands/` — ok 1.5s (runs/head-internal-commands.txt)
- `go test -count=1 -timeout=30m ./internal/checkpoint/` — ok 82.5s (runs/head-internal-checkpoint.txt)
- `go test -count=1 -timeout=30m ./internal/daemon/` — ok 221.1s (runs/head-internal-daemon.txt)
- `go test -count=1 -timeout=30m ./internal/pluginmanifest/` — ok (runs/head-internal-pluginmanifest.txt)
- `go test -count=1 -timeout=30m ./test/docs/` — ok (runs/head-test-docs.txt)
- `go test -count=1 -timeout=30m ./tools/devtool/` — ok 89.8s (runs/head-tools-devtool.txt)
- `go test -count=1 -run '^TestSlashCommands_AreDiscoverable$' ./internal/cli/ (before fix)` — FAIL exit 1, as intended (runs/red-cli-discoverable.txt)
- `go test -count=1 -run '^TestUserGuideMarksCheckpointAsUnrouted$' ./test/docs/ (against the old guide)` — FAIL exit 1, as intended (runs/red-docs-userguide-oldguide.txt)
- `go run ./tools/devtool plugin-validate` — OK (9 file(s), 6 command(s), 7 hook event(s))
- `go run ./tools/devtool gen-command-docs --check` — docs/commands.md is up to date
- `go run ./tools/devtool bundle --out <scratch>/bundle --target windows/amd64 --host-validate --evidence <scratch>/host-validate.txt` — exit 0; `claude plugin validate --strict --json` accepted, success=true; the bundle's commands/ holds 6 files (runs/bundle-host-validate.json)
- `claude plugin validate <scratch>/bundle/qompack-plugin-v0.2.0-1279-ga0e84956-windows-amd64` — Validation passed, exit 0 (runs/claude-plugin-validate.txt)
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) on internal/cli, internal/commands, internal/pluginmanifest, tools/devtool, test/docs` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0 (runs/lint.txt)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7b-checkpoint --repo C:/.../qompack-cx-w7b-checkpoint --out C:/.../scratchpad/w7b/checkpoint/linux a0e84956 touched --timeout 30m -- ./internal/cli ./internal/commands ./internal/pluginmanifest ./tools/devtool ./test/docs` — exit 0, non-root uid 10001, -race: cli 427 pass, commands 116, pluginmanifest 28, devtool 333, docs 19, 0 fail/skip; source tree unchanged (runs/linux-touched-a0e8495/). One benign 'go: writing stat cache ... permission denied' line from the shared module cache.

### Criterion changes

- TestSlashCommands_AreDiscoverable: the checkpoint exception is removed, so every shipped command must route to a non-hook subcommand (tightened)
- TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand: the unrouted rendering and its 'at least one unrouted' assertion are retired as the test itself directed; it now asserts no unrouted marker, no checkpoint command, and a statement that checkpoints are automatic
- TestUserGuideMarksCheckpointAsUnrouted: gains a branch for when no checkpoint row exists (no heading for the command, plus a 'not offered yet' statement)
- TestManifest_SevenCommands, TestManifest_FilesAreStableBytes, TestManifest_GoldenBytes, TestAssembleBundle_Layout, TestCommandNames_MatchesSection75, TestSpecs_PinInstalledSurface, TestAll_CoversEverySlashCommand and the section75 lists: expectations go to six commands and nine bundle files
- TestCheckpoint_* (six tests): deleted along with the removed frontend

### Open issues

- Out of tree: the live-lane coverage in plans/sdd/V6-closeout/coordinator/live-uat.js must drop /qompack:checkpoint. The installed command count went from 7 to 6, and C4.1's "the seven commands discovered" and C4.5 should read six.
- Plan documents that I did not edit (outside my scope) still say seven commands or name checkpoint as a command: 00-ARCHITECTURE.md §5.17, §7.5 text and line 821 ('seven files'), the plugin-validate row at line 2667 ('7 commands'), TRACEABILITY.md, MIGRATION-EVIDENCE.md E08, and Qompack.md §7.5. Revising Qompack.md needs explicit authorization under its revision log.
- Kept a historical test name: TestManifest_SevenCommands now asserts six commands, because the V1/V3 verification plans quote it in `go test -run` patterns. Its comment says so.
- The removed TestCheckpoint_* tests are still cited in plans/sdd/V5-VERIFY/inventory-SP-14.md and plans/sdd/V6-VERIFY/inventory.md as historical records, in backticks rather than -run patterns; the runpatterns lint passes.
- An earlier full internal/cli run (runs/pkg-cli.txt) was taken before three comment-only edits in internal/commands; the head-*.txt runs are the ones at the fix commit.

### Needs the owner

- No new budget or bound numbers. The only constant changed is tools/devtool's wantCommands 7 -> 6: it counts the shipped commands, not a budget (§7.5's seven minus checkpoint, whose only route is the PreCompact hook). If it is wrong, plugin-validate fails with a count message.
- Criterion changes, with rationale:
(1) TestSlashCommands_AreDiscoverable loses its checkpoint exception. That tightens it: no shipped command may route to a hook.
(2) TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand drops its 'at least one unrouted' requirement, which its own message said to retire once every command is routed. It now asserts every listed command is routed, that the page never contains 'not yet routed' or '/qompack:checkpoint', and that it says checkpoints are automatic.
(3) TestUserGuideMarksCheckpointAsUnrouted keeps its old conditional logic for when the command has a row. When it has none, the test now requires the guide to have no /qompack:checkpoint heading and to have a 'Checkpoints are automatic' section saying a manual checkpoint is not offered yet.
(4) The six TestCheckpoint_* tests are deleted together with the frontend they tested.
(5) The expected bundle file counts go 10 -> 9 (golden and stable-bytes tests) and the bundle-tree lists drop commands/checkpoint.md.
No check was skipped, loosened or regenerated to match broken output.
- Coordinator decision under D33: ratify option B, not shipping a manual checkpoint in 0.3.0. If a real route is wanted later, ${CLAUDE_SESSION_ID} (verified present in 2.1.280 plugin command expansion) solves the session binding. It still needs a new §5.4 op and a home for --reason, which means a checkpoint schema bump.

## Independent review

### review:checkpoint: needs-fixes

- **minor** `docs/cannot-do.md:484` — The new 'What Qompack does instead' bullet says "`/qompack:status` and `qompack fsck` report the checkpoint store." The fsck half is true. /qompack:status does not report the checkpoint store: StatusReport and DaemonStatus (internal/commands/statuscollect.go:31-144) and daemon SessionState (internal/daemon/registry.go:20) carry no checkpoint seq, manifest or store field. The only checkpoint data in status is the checkpoint_finalize latency histogram and the checkpoint.cadence.local_seal counter.
  - Evidence: `git grep -i checkpoint -- internal/commands/statuscollect.go internal/commands/cmd_status.go internal/daemon/handlers.go internal/daemon/registry.go` finds no checkpoint-store field. fsck.go:155 does say "verify store, index, checkpoint and backup integrity".
  - Fix: Reword it to something like: "`qompack fsck` verifies the checkpoint tier, and `/qompack:status` shows checkpoint-finalize latency and the local cadence-seal counter." Or drop the status claim.
- **minor** `plans/V5-SP-14-slash-commands-and-observability.md:70 (reported, not edited)` — Exit criterion SP14-M3-01 ("checkpoint command does not request native compaction") was gated by the deleted TestCheckpoint_NeverRequestsNativeCompaction. The criterion is now vacuous because no checkpoint command exists. The implementer's open_issues name the V5/V6 inventories and the §5.17/§7.5 plan text, but not this exit-criterion row, so the coordinator may not see that an SP-14 gate was retired rather than satisfied.
  - Evidence: pin_checkpoint_test.go -> pin_test.go deletes TestCheckpoint_NeverRequestsNativeCompaction, whose doc comment was "the SP14-M3-01 gate". The plan row is unchanged at plans/V5-SP-14-...md:70.
  - Fix: Add SP14-M3-01 to the open_issues/needs_owner handoff: "retired with the command (option B); re-instate with any future checkpoint-now route". The coordinator then updates the plan row and the traceability row.
- **nit** `packaging/README.md:204` — The long-path risk item still names `commands/checkpoint.md` as the deep path, and that file is no longer in the bundle. The implementer changed line 18 of the same file from seven to six but missed this one.
  - Evidence: "4. **Long paths.** Windows `MAX_PATH` against a deep plugin root plus `commands/checkpoint.md`;"
  - Fix: Name a file that still ships, e.g. `hooks/hooks.json` or `commands/dropped.md`. Or say "the longest bundled path".
- **nit** `internal/checkpoint/draft.go:125` — The SetCurrentWork doc comment still names "SP-14's /qompack:checkpoint" as a caller that knows the real goal. That command no longer exists.
  - Evidence: "// SP-12's todo state and SP-14's /qompack:checkpoint know the real goal and next step"
  - Fix: Change it to "SP-12's todo state (and any future manual checkpoint route)". This is a comment-only change. If internal/checkpoint stays untouched under option B, carry it as a follow-up instead.
- **nit** `commit 1b3cac3b` — The evidence commit `docs(v6-closeout): record w7b-checkpoint evidence runs` has no Refs footer. The fix commit a0e84956 has one (`Refs: V6-VERIFY, C4.5/SP-14 H3`). Neither commit has attribution trailers, which is correct.
  - Evidence: `git log -1 --format=%B 1b3cac3b` ends at the body paragraph with no Refs line.
  - Fix: Add `Refs: V6-VERIFY, C4.5/SP-14 H3` when the branch is folded or rebased.

## Fix seat (review resolution) — status `done`, head `180569b4`

### Root cause

/qompack:checkpoint (plugin/commands/checkpoint.md) ran `qompack checkpoint $ARGUMENTS`. That name is the PreCompact hook entry point: it reads a hook event from stdin and always exits 0, so the slash command sealed nothing and reported nothing (SP-14 handoff edge H3). The implementer chose option B and removed the command. The fix seat found one doc-accuracy defect in that change: docs/cannot-do.md claimed /qompack:status reports the checkpoint store, and it does not.

### Summary

W7B-CHECKPOINT: FIX SEAT REPORT (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7b-checkpoint, branch closeout/w7b-checkpoint, HEAD 180569b4, base closeout/integration @ 898bb8b)

Option chosen (implementer, unchanged by the fix seat): B. /qompack:checkpoint is no longer shipped.
Why:
- Routing it for real needs three things: a new §5.4 daemon op, a session binding (${CLAUDE_SESSION_ID} is available in 2.1.280 plugin command expansion) and a place to record --reason, which means a checkpoint schema bump. That is more than a small change.
- It would help users very little. The block injected after a compaction is built from the session's latest checkpoint, and that is normally the one the compaction itself seals.
What stays the same: the PreCompact hook and hooks.json are unchanged. The plugin now ships 6 commands (plugin-validate's wantCommands goes 7 -> 6), and the bundle holds 9 files instead of 10. docs/commands.md was regenerated with the generator. docs/cannot-do.md and docs/user-guide.md now say checkpoints are written automatically and a manual one is not offered yet.
The implementer's evidence is committed under plans/sdd/V6-closeout/w7b-checkpoint/runs/:
- red runs;
- a full run of each touched package at a0e8495;
- plugin-validate, plus `claude plugin validate` on a built bundle;
- lint;
- the Linux non-root -race gate linux-touched-a0e8495 (go_test_exit=0, exit=0).

REVIEW RESOLUTION
1. Minor, docs/cannot-do.md:484 ("`/qompack:status` and `qompack fsck` report the checkpoint store"). CONFIRMED and FIXED in 180569b4.
   Evidence:
   - DaemonStatus (internal/commands/statuscollect.go) has only mode/contract/hot/sessions/latency/budgets/counters/spool/loud_tail/extra, with no checkpoint field.
   - `git grep -i checkpoint` finds no checkpoint-store field in statuscollect.go, cmd_status.go, daemon handlers.go or registry.go.
   - What status does show: the checkpoint_finalize histogram (internal/obs/budgets.go histCheckpointFinalize) and the counters map, which render.go prints in full. That map includes checkpoint.cadence.local_seal (internal/daemon/wire_checkpoint.go:329), which obs registry.Counter creates lazily on the first cadence seal.
   - fsck.go:155 does verify checkpoint integrity.
   The reworded bullet: "`qompack fsck` verifies the checkpoint tier against its manifest. `/qompack:status` reports no checkpoint list or latest seq; it shows only the `checkpoint_finalize` latency histogram and, once a cadence seal has happened, the `checkpoint.cadence.local_seal` counter."
   No new test: this corrects one prose claim, and a test asserting a sentence is absent would be brittle and prove nothing. The existing doc tests still pass (below).
2. Minor, SP14-M3-01 retired rather than satisfied. CONFIRMED. The fix seat did not edit anything for it, because plan and traceability files are the coordinator's. It is now handed off explicitly:
   - The deleted TestCheckpoint_NeverRequestsNativeCompaction called itself "the SP14-M3-01 gate" (visible in `git show a0e84956`).
   - plans/V5-SP-14-slash-commands-and-observability.md:70 still lists SP14-M3-01 unchanged.
   - Two inventory rows name the deleted tests: plans/sdd/V5-VERIFY/inventory-SP-14.md I-14.11 (and its line 88 note), and plans/sdd/V6-VERIFY/inventory.md 1.14.5.
   See open_issues and needs_owner.

COMMANDS AND RESULTS (fix seat, at 180569b4; the change touches documentation only, no Go code)
- go test -count=1 -timeout=30m ./test/docs -> exit 0 (ok, 4.5s)
- go run ./tools/devtool lint --only=docmarkers,runpatterns -> exit 0 (PASS both)
- go run ./tools/devtool gen-command-docs --check -> exit 0 ("docs/commands.md is up to date")
Logs are committed under plans/sdd/V6-closeout/w7b-checkpoint/runs/fix/. Go code is unchanged since a0e84956, so the implementer's package runs, plugin validation and Linux gate still describe the code at HEAD. No wall-clock failures came up. No background processes were started.

CRITERION CHANGES
- Fix seat: none.
- Implementer (carried forward; also listed under needs_owner): TestSlashCommands_AreDiscoverable is tightened. TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand now asserts that every listed command is routed. TestUserGuideMarksCheckpointAsUnrouted gets a no-row branch. The six TestCheckpoint_* tests are deleted along with the frontend they tested. Bundle counts go 10 -> 9.

LIVE-LANE NOTE: plans/sdd/V6-closeout/coordinator/live-uat.js is not in this tree. The shipped command count changed from 7 to 6, so its command coverage must drop /qompack:checkpoint.

### Commits

- a0e84956 fix(plugin): stop shipping the no-op /qompack:checkpoint (implementer)
- 1b3cac3b docs(v6-closeout): record w7b-checkpoint evidence runs (implementer)
- 180569b4 docs(cannot-do): stop claiming status reports the checkpoint store (fix seat)

### Tests

- `go test -count=1 -timeout=30m ./test/docs` — exit 0, ok (4.522s) at 180569b4
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — exit 0, PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool gen-command-docs --check` — exit 0, docs/commands.md is up to date
- `implementer runs at a0e84956 (internal/cli, internal/commands, internal/checkpoint, internal/daemon, internal/pluginmanifest, tools/devtool, test/docs, plugin-validate, claude plugin validate, Linux gate linux-touched-a0e8495)` — all green per plans/sdd/V6-closeout/w7b-checkpoint/runs/; Go code unchanged since

### Criterion changes

- Fix seat: none.
- Implementer (carried forward): TestSlashCommands_AreDiscoverable tightened; TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand now asserts every listed command is routed; TestUserGuideMarksCheckpointAsUnrouted gets a no-row branch; six TestCheckpoint_* tests deleted with their frontend; bundle counts 10 -> 9; SP14-M3-01 gate retired (see needs_owner).

### Open issues

- SP14-M3-01 (plans/V5-SP-14-slash-commands-and-observability.md:70) was retired with the command under option B, not satisfied. Its gate TestCheckpoint_NeverRequestsNativeCompaction was deleted. The coordinator should mark the row retired (re-instate it with any future checkpoint-now route) and update plans/TRACEABILITY.md.
- Inventory rows naming the deleted TestCheckpoint_* tests need a 'retired with option B' disposition: plans/sdd/V5-VERIFY/inventory-SP-14.md I-14.11 (and its line-88 note) and plans/sdd/V6-VERIFY/inventory.md 1.14.5. The implementer also flagged the §5.17/§7.5 plan text.
- The live-UAT lane (plans/sdd/V6-closeout/coordinator/live-uat.js, not in this tree) must drop /qompack:checkpoint: the shipped command count is now 6, not 7.

### Needs the owner

- No new budget or bound numbers. The only constant changed is tools/devtool's wantCommands 7 -> 6. It counts shipped commands and is not a budget: §7.5's seven minus checkpoint, whose only route is the PreCompact hook. If it is wrong, plugin-validate fails with a count message.
- Criterion changes, with rationale (implementer):
(1) TestSlashCommands_AreDiscoverable loses its checkpoint exception. This tightens it: no shipped command may route to a hook.
(2) TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand drops its 'at least one unrouted' requirement, which its own message said to retire once every command is routed. It now asserts that every listed command is routed, that the page never contains 'not yet routed' or '/qompack:checkpoint', and that it says checkpoints are automatic.
(3) TestUserGuideMarksCheckpointAsUnrouted keeps its old conditional logic for when the command has a row. When it has none, the guide must have no /qompack:checkpoint heading and must have a 'Checkpoints are automatic' section saying a manual checkpoint is not offered yet.
(4) The six TestCheckpoint_* tests are deleted together with the frontend they tested.
(5) Expected bundle file counts go 10 -> 9 (golden and stable-bytes tests), and the bundle-tree lists drop commands/checkpoint.md.
No check was skipped, loosened or regenerated to match broken output.
- Coordinator decision under D33: ratify option B, so 0.3.0 ships no manual checkpoint. If a real route is wanted later, ${CLAUDE_SESSION_ID} (verified present in 2.1.280 plugin command expansion) solves the session binding. It still needs a new §5.4 op and a home for --reason, which means a checkpoint schema bump.
- Ratify retiring exit criterion SP14-M3-01 ('checkpoint command does not request native compaction') together with the command. Its gate test was deleted, the criterion is vacuous while no checkpoint command ships, and it must come back with any future checkpoint-now route.

## Independent verification of the fix seat: needs-fixes

- **nit** `docs/cannot-do.md:485-487 (commit 180569b4)` — Finding 1 is fixed: the old claim that /qompack:status reports the checkpoint store is gone, and the fsck half is accurate (internal/cli/fsck.go checkCheckpoints checks checkpoints/MANIFEST.jsonl). But the new sentence goes too far the other way. It says status "shows only the `checkpoint_finalize` latency histogram and, once a cadence seal has happened, the `checkpoint.cadence.local_seal` counter." That is not true. Status also prints `checkpoint.sources.unavailable` once an idle pass finds no usable source set.
  - Evidence: internal/commands/render.go:72-76 prints the whole DaemonStatus.Counters map. internal/daemon/wire_checkpoint.go:318 defines counterSourcesUnavailable = "checkpoint.sources.unavailable". Line 394 increments it on the same registry as the cadence counter: both advanceFrontierTask (line 350) and finalizeIfDue (line 354) receive metricsOf(d), and finalizeIfDue increments counterCadenceSeal at line 593. So a status page can show a checkpoint.* counter the new text says it never shows. The checkpoint_finalize histogram also appears twice, once as the PreCompact hook row and once as budget B-E (statuscollect.go:173/181), but that part is still consistent with the text.
  - Fix: Drop the word "only" and the exhaustive list. Suggested text: "`/qompack:status` reports no checkpoint list or latest seq; checkpoint activity shows up only as the `checkpoint_finalize` latency (the PreCompact row and budget B-E) and any `checkpoint.*` counters, such as `checkpoint.cadence.local_seal`."


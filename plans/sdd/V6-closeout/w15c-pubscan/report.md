# Wave 15c workstream pubscan

Branch `closeout/w15c-pubscan`. Workflow `wf_be7002a0-f70`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `9d37a97adda8fe9e85dd1d418d388f233129a13f`

### Root cause

(1a) The pass was slow because of how it opened files, not because of the amount of data. Each time internal/store/publication_audit.go opened a directory or file, openPublicationPath opened a new os.Root of the project root, Lstat-ed every ancestor of the path through that root (on Windows each ancestor Lstat walks all the components again), and then opened the whole path. That is about 20 handle opens for every object fanout directory. On the 10x store (3938 directories, 3800 objects, 700 captures) this work made up most of the pass. On the same store copied fresh, the pass took 3.8-6.0 s before the change and 0.7-0.9 s after it. (1b) Nothing stopped the background continuation (5b8f1e1a) from running beside hook requests. accountPublicationAtStartup started it with goRun under runCtx, with no yield and no pacing.

### Summary

W15c pubscan seat. Branch closeout/w15c-pubscan in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15c-pubscan, cut from a38a2b6d. D51 is done: 8 commits, all tests and lints green, no numbers that need the owner. I did not touch internal/rehydrate, did not start the Linux container, did not run a real Claude session, and did not use hot-path rows.

## (1a) One os.Root per pass (commits 2fe70ecb, 16487804)
- AuditPublication now opens the project root once, as os.Root(paths.Long(s.root)).
- Each directory is reached through the handle of the directory that listed it (openPublicationDir):
  - It does a handle-based Lstat through the parent and refuses a symlink, a reparse point or a non-directory.
  - It then opens the directory with parent.OpenRoot + Open(".") when the walk needs to go into it, or with parent.Open for an object leaf that is only listed.
  - It requires os.SameFile between the Lstat result and the opened handle's Stat, so a link swapped in between the check and the open is also refused. This is stricter than before; the old Lstat-then-open had that race window.
- Sidecars and pending markers are read the same way (readPublicationFile: Lstat, Open, then regular-file and SameFile checks).
- Phase directories (records/captures, objects, state/pending) are walked one component at a time from the pass root, so a link anywhere on the way is refused.
- Confinement: four new link tests. On Windows the links are NTFS junctions made by makeDirLink. Each test puts a gap or object behind a link to a second project and checks nothing behind it is counted, the pass is Incomplete, and the expected note is present:
  - TestAuditPublication_LinkedCaptureTreeIsNotFollowed
  - TestAuditPublication_LinkedRecordsAncestorIsNotFollowed
  - TestAuditPublication_LinkedCaptureShardIsNotFollowed
  - TestAuditPublication_LinkedObjectFanoutIsNotFollowed
- All four also pass on the base tree (runs/links-on-base.txt), so the guarantee is the same one the old walk gave.
- The maintenance and GC confinement tests pass: the full internal/store run is ok (339 s).

## (1b) The pass yields to capture work (commits 2fe70ecb, d95f0520)
- The store side has a new field, PublicationScanCap.Yield func(ctx) error. The pass waits on it before each directory batch and before each entry, and stops as interrupted if it returns an error.
- The daemon side has a new captureGate (internal/daemon/publication_audit.go, field d.capture): a counter of work in flight plus an idle channel, no numbers.
  - Holders: dispatchOp (every live request), runIngested (worker and drain), and the three pieces of work a request leaves running after its reply: startPromptRecording, startReplyWork and launchSessionEnd. Each enters before its request leaves.
  - accountPublicationAtStartup sets scanCap.Yield = d.capture.wait for both the bounded part and the background part.
  - Stop cancels runCtx, which ends a paused pass.
- Two seams, both documented in the code:
  - A unit of work already started finishes: at most one ReadDir batch of 256 names, or one sidecar or marker read.
  - A fire-and-forget delivery is not counted while it sits in the ingest ring between the route's return and a worker picking it up. A busy worker holds the gate, so this gap only exists when every worker is idle.

## Measurement
File: runs/pubscan-10x-before-after.txt. Throwaway diagnostic, never committed. Two test binaries: before built from git archive a38a2b6d, after from this branch. One process at a time under timeout 1200. The rehydrate seat was running at the same time, so all numbers are under co-load.

| | Before | After |
|---|---|---|
| Pass, first after the writes (seed and measure in one process) | 16.2 s | 10.1 s |
| Pass, warm, fresh copy of one seeded store (2 rounds) | 3.8-6.0 s | 0.7-0.9 s |
| Pass, warm, the cold-run process | 5.6-5.8 s | 2.9-3.5 s |
| PutBytes p99 alone | 16.2 / 27.0 / 22.4 ms | 22.9 / 25.1 / 64.3 ms (64.3 is a load outlier) |
| PutBytes p99 beside an ungated pass | 18.7 / 27.1 / 28.0 ms | 28.4 / 19.6 / 26.4 ms |
| PutBytes p99 beside the gated pass (how the daemon runs it) | n/a | 19.9 / 18.9 / 20.3 ms |

- Beside the gated pass, PutBytes p99 is at or below the "alone" figures.
- A gated pass under back-to-back puts took about 4.4 s to finish.
- I did not reproduce w15-services' 15.1 s cold figure; my own first-pass-after-writes figure is 16.2 s before. The first pass is still 10 s after the change. That residue is the first open of freshly written directories, most likely antivirus scanning (the pass has to open each directory at least once). It no longer competes with capture work because of the gate.

## (2) Reload docs (commits d3e7cf0a, 29f572f4, 6cb194f1)
- reload_keys.go exports:
  - ReloadKeyClasses() and a ReloadEffect type with four effects.
  - LoudReloadNeedsRestart and LoudReloadNoEffect, now also used by reload.go.
  - AdminReloadChanged, AdminReloadRestartRequired and AdminReloadNoEffect, now also used by handleAdminReload.
  - The message and field strings are unchanged.
- runtime.mode's reader text now names the daemon's session start (w15-services review nit). Its classification is unchanged.
- docs/config-reference.md is generated, so I changed the generator: gen-config-docs renders a "Reloading the configuration" section from ReloadKeyClasses. It has four tables (takes effect on reload, read when the hook or command runs, needs a daemon restart, no effect in this build) and quotes both LOUD lines and admin.reload's changed, restart_required and no_effect fields. I regenerated the page.
- docs/troubleshooting.md §6 has a new entry, "A config change that did not take effect". It names the five no-effect keys "in 0.3.0" and links the generated section.
- Drift tests:
  - TestGenConfigDocs_ReloadSectionListsEveryClassifiedKey (tools/devtool) reads the committed page and requires each table to list exactly the table's keys for that effect, plus the D51 no-effect keys and the quoted strings. It was red before regeneration.
  - TestTroubleshootingNamesExactlyTheNoEffectReloadKeys (test/docs, stdlib-only, parses reload_keys.go) checks both directions. A mutation check made it fail.
  - troubleshooting must now also link config-reference.md#reloading-the-configuration.

## Tests (all -p 2)
- New daemon rows are green; the mutation check with the Yield wiring removed made both pause tests fail.
- Full package runs are ok: internal/store 339 s, internal/daemon 401 s, tools/devtool 128 s, test/docs 4.5 s.
- After 16487804 (a one-line unparam refactor), I re-ran all 39 publication tests of internal/store by exact name: ok.
- fmt-check, go vet on Windows and GOOS=linux, and gen-config-docs --check pass.
- Lint subset: the first run failed golangci-lint (unparam on eachPhaseEntry). After the fix, golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers and runpatterns all pass.
- No wall-clock failures.

### Commits

- 2fe70ecb perf(store): walk publication accounting through one os.Root
- d95f0520 fix(daemon): pause the publication pass while capture work runs
- d3e7cf0a feat(daemon): export the reload key classes and what a reload says
- 29f572f4 docs(config): generate what a config reload does with each key
- 6cb194f1 docs(troubleshooting): explain a config change that did not apply
- 16487804 refactor(store): drop eachPhaseEntry's unused result
- 2c60f799 docs(daemon): record the publication pass cost after D51
- 9d37a97a docs(sdd): add the w15c-pubscan evidence logs

### Tests

- `go vet ./internal/daemon/ (with publication_yield_test.go, before the gate existed)` — RED: undefined captureGate / d.capture (runs/yield-red-daemon.txt)
- `go test -p 2 -count=1 -run '^TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight$' ./internal/daemon/ (mutation: scanCap.Yield wiring removed)` — FAIL as expected: the publication pass finished beside a request in flight; same for TestStartupPublicationAccounting_StopEndsAPausedPass (runs/yield-mutation-daemon.txt)
- `go test -p 2 -count=1 -v -run '^TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight$' ./internal/daemon/ (and the other 9 gate/startup rows by exact name)` — PASS: 10 rows, ok 34 s (runs/yield-green-daemon.txt)
- `go test -p 2 -count=1 -v -run '^TestAuditPublication_LinkedCaptureTreeIsNotFollowed$' ./internal/store/ (the 4 link tests, on the base tree a38a2b6d)` — PASS on base (runs/links-on-base.txt)
- `go test -p 2 -count=1 -v -run '^TestAuditPublication_YieldBeforeEveryEntryLeavesTheAnswerUnchanged$' ./internal/store/ (and the other 5 walk rows)` — PASS (runs/walk-green-store.txt)
- `go test -p 2 -count=1 -v -run (all 39 publication tests of internal/store by exact name) ./internal/store/ after 16487804` — PASS, ok 7.9 s (runs/walk-unparam-store.txt) <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=1 -run '^TestGenConfigDocs_ReloadSectionListsEveryClassifiedKey$' ./tools/devtool/` — RED before regenerating the page, PASS after (runs/reload-docs-red.txt, runs/reload-docs-green.txt)
- `go test -p 2 -count=1 -run '^TestTroubleshootingNamesExactlyTheNoEffectReloadKeys$' ./test/docs/` — PASS; mutation (runtime.telemetry swapped for runtime.mode) FAIL as expected (runs/reload-troubleshooting-mutation.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/store/` — ok 339 s (at 2fe70ecb code; runs/full-store-daemon.txt)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 401 s (runs/full-store-daemon.txt)
- `go test -p 2 -count=1 -timeout=30m ./tools/devtool/ ; ./test/docs/` — ok 128 s; ok 4.5 s (runs/full-devtool-docs.txt)
- `go run ./tools/devtool fmt-check; go vet (windows and GOOS=linux) on internal/store, internal/daemon, tools/devtool, test/docs; go run ./tools/devtool gen-config-docs --check` — all pass
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — first run FAIL (unparam on eachPhaseEntry), fixed in 16487804; golangci-lint rerun PASS; nomagic, sleepcheck, runpatterns, docmarkers rerun PASS after the last commit; importgraph, testdeps, bindeps PASS
- `10x before/after diagnostic (throwaway, two go test -c binaries, timeout 1200 each, one process at a time)` — pass warm 3.8-6.0 s -> 0.7-0.9 s; first pass after the writes 16.2 s -> 10.1 s; PutBytes p99 beside the gated pass 18.9-20.3 ms vs alone 16-27 ms (runs/pubscan-10x-before-after.txt)

### Criterion changes

- None weakened. Nothing was skipped, marked nolint or nomagic:allow, lowered or regenerated to match broken output. The one regenerated page, docs/config-reference.md, gained a generated section, and its drift tests were red before regeneration.
- The note for 'records/captures is a regular file' changes from 'a directory under .qompack could not be read' to '... could not be opened'. openPublicationDir now refuses a non-directory before opening it instead of failing at ReadDir. The pass still reports Incomplete, and no test pinned the old text.
- test/docs TestTroubleshootingLinksConfigReferenceSections now also requires troubleshooting to link config-reference.md#reloading-the-configuration, which makes it stricter.

### Open issues

- The first pass after a large burst of writes still takes about 10 s on Windows (16.2 s before). What remains is the first open of freshly written directories, probably antivirus scanning, and the gate keeps it off capture work. Only Windows was measured, because the Linux container was stopped.
- Two seams in 'never beside a request' are documented in captureGate and PublicationScanCap.Yield rather than closed. (a) A unit of work already started when a request arrives finishes first: at most one 256-name ReadDir batch, or one sidecar or marker read. (b) A fire-and-forget delivery is not counted while it waits in the ingest ring between the route's ACK and a worker taking it; a busy worker holds the gate, so the gap needs every worker idle.
- The os.SameFile swap refusal in openPublicationDir and readPublicationFile has no deterministic test. Injecting a swap between the Lstat and the open would need a seam in production code. The link tests cover the Lstat and entry-type refusals.
- The full internal/store run was on 2fe70ecb, before the one-line unparam refactor in 16487804. After it, all 39 publication tests in internal/store were re-run by exact name and are green; the whole package was not re-run.
- This branch touches internal/daemon/daemon.go, handlers.go, session_end.go, session_start_compact.go, reload.go and reload_keys.go, each with a small change. The rehydrate seat's scope is internal/rehydrate, but a textual merge conflict in those daemon files is possible.
- Still carried from D51, not this seat's work: the -race pass over internal/daemon, negknow and mcp in the next quiet window. The capture gate adds a mutex on the request path, so that race pass should include this branch.

## Independent review

### review:pubscan: needs-fixes

- **major** `internal/daemon/publication_yield_test.go:151-186 (TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight)` — The pause test does not prove that the background pass pauses, which is the half D51 is about. With publicationBound=1ns, the bounded half of accountPublicationAtStartup reaches captureGate.wait with the gate held, calls onPark, and puts a signal in the 64-slot buffered `parked` channel before accountPublicationAtStartup returns. The first `select { case <-parked: ... }` is therefore satisfied by the bounded half. The follow-up `default:` check and the zero-counter assertion only pass because the background walk has not finished yet, which is timing, not a proven pause. The bounded half runs while serveOp still holds every live request back, so in production the background continuation is the only part that can overlap a hook request.
  - Evidence: Mutation run in a scratch copy of HEAD (git archive, not the worktree): I added `scanCap.Yield = nil` inside the d.goRun closure at publication_audit.go:142, so only the background pass stops yielding. `go test -p 2 -count=5 -run '^TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight$' ./internal/daemon/` returned `ok` (30.2 s), 5/5 green. The implementer's mutation (runs/yield-mutation-daemon.txt) removed the Yield wiring from both halves, so it could not show this. TestStartupPublicationAccounting_StopEndsAPausedPass has the same blind spot: a non-yielding background pass that is cancelled mid-walk also counts as Incomplete.
  - Fix: Make the park observable per pass. For example, change captureGate.onPark to `func(ctx context.Context)`, and in the test signal only when `_, ok := ctx.Deadline(); !ok`. The background pass runs under runCtx with no deadline; the bounded half runs under a WithTimeout. Alternatively, record parks in a counter and require a park that happens after accountPublicationAtStartup has returned. Then re-run the background-only mutation above and confirm it fails. Apply the same change to StopEndsAPausedPass.
- **minor** `internal/daemon/handlers.go:661-665, internal/daemon/session_start_compact.go:545-549, internal/daemon/session_end.go:211-215` — The three places that hold the gate after the reply (startPromptRecording, startReplyWork, launchSessionEnd) have no test. Only dispatchOp and runIngested are tested for holding the gate. If a later edit drops one of these enter/leave pairs, or moves enter into the goroutine, the background pass can run beside a prompt capture, compact reply work or a session end, and every current test stays green.
  - Evidence: publication_yield_test.go tests only TestDispatchOp_HoldsTheCaptureGateWhileServing and TestRunIngested_HoldsTheCaptureGateWhileApplying. No test reads d.capture.inFlight() after dispatchOp returns while the post-reply goroutine is still running.
  - Fix: Add one test per launcher. Block the launched work on a channel, assert that d.capture.inFlight() is still 1 after dispatchOp (or the launcher) has returned, release the work, then assert 0. No sleeps are needed.
- **minor** `internal/daemon/publication_audit.go:150-158 (captureGate doc) / internal/daemon/drain.go (Drain via drainOnRequest, watchClientSpools)` — Drains that hook traffic triggers after the ACK are only partly counted. drainOnRequest and the client-spool watcher run Drain, and Drain reads WAL/spool segments and appends fsynced delivery-lease journal records before each runIngested. Only runIngested holds the gate, so between deliveries the gate can drop to idle and the pass can run beside the drain's lease-journal fsyncs, which are session I/O. The captureGate doc names two seams (the unit already under way, and the ingest ring) but not this one.
  - Evidence: The diff adds enter/leave only in runIngested, dispatchOp and the three post-reply launchers. Drain, drainOnRequest and watchClientSpools are unchanged. The captureGate comment says capture work is 'a delivery a worker or a drain is applying (runIngested)'.
  - Fix: Hold the gate for each Drain pass started by drainOnRequest and watchClientSpools (enter before Drain, leave after). The startup drain can be left alone because it finishes before the publication pass starts. Alternatively, record this as a third named seam in the captureGate and PublicationScanCap.Yield comments and in the report's open issues.
- **minor** `docs/troubleshooting.md:621-623; tools/devtool/genconfigdocs.go writeReloadSection (docs/config-reference.md:249,260)` — Both pages tell the reader that admin.reload reloads unconditionally and answers with changed/restart_required/no_effect, but no qompack subcommand sends admin.reload in 0.3.0. Its only callers are the daemon route table and tests. A user following the troubleshooting entry cannot act on it. The §7 entry for admin.shutdown already states this limit explicitly for its op.
  - Evidence: `grep -rn OpAdminReload --include=*.go . | grep -v _test` finds only internal/daemon/daemon.go:488 and internal/ipc/op.go. The only CLI hit for 'admin.reload' is internal/cli/config_reload_live_test.go.
  - Fix: In the troubleshooting entry, and in the generator's prose (then regenerate), say that admin.reload is an IPC op with no qompack subcommand in this build, and that the file-change reload's LOUD lines and `config reloaded` day-log line are the user-visible form of the same three lists.
- **nit** `internal/store/publication_audit.go:497-537 (eachDirEntry) with internal/daemon/publication_audit.go captureGate.wait` — A paused pass holds its directory handles while it waits, and the wait has no bound. That is the pass os.Root, the phase-ancestor roots, the current shard or fanout root, and a File part-way through ReadDir. On Windows an open handle pins its directory: it cannot be removed or renamed while held. This matters for operator-side restore or cleanup of .qompack while a busy session keeps the pass parked. Before D51 these handles lived only for the length of a short walk.
  - Evidence: eachDirEntry defers f.Close and dir.Close until the directory's listing ends. proceed() parks inside the ReadDir loop. The captureGate comment documents the unbounded pause but not the handles held during it.
  - Fix: Note the held handles in the captureGate / accountPublicationAtStartup comment, or have the gate wait happen with the handles released (for example, wait before opening each directory and re-open after resuming). Accept this if the owner agrees it is harmless.
- **nit** `commits 29f572f4, 6cb194f1, 16487804, 2c60f799` — Four substantive D51 commits have no `Refs:` footer, while 2fe70ecb, d95f0520 and d3e7cf0a carry `Refs: V6-VERIFY, C1.6/C4.5`. The base's equivalent 'docs(daemon): record the background publication pass cost' (ead67446) carries one.
  - Evidence: `git log --format='%h %(trailers:key=Refs,valueonly)' a38a2b6d..HEAD` shows an empty Refs trailer for these four. No attribution trailers are present, which is correct.
  - Fix: Add `Refs: V6-VERIFY, C1.6/C4.5` to these commits when the branch is next rebased or squashed at integration.

## Fix seat (review resolution) — status `done`, head `94698995`

### Root cause

F1: the tests set publicationBound=1ns, so the bounded half of the pass parked on the held gate and triggered the park signal before accountPublicationAtStartup returned; the tests never saw the background pass park. F3: only runIngested entered the gate, so between a drain's deliveries (spool reads and fsynced lease-journal appends) the gate fell idle. F2 and F4 were missing tests and an inaccurate doc claim.

### Summary

Fix seat for w15c-pubscan, on closeout/w15c-pubscan. I verified all four review findings independently and all four were correct, so each one is fixed; none is rebutted. Before each fix I wrote a test and showed it fails; after the fix it passes. The worktree is clean and nothing I started is still running. I did not start any load generator or the Linux container, and I did not edit any file in internal/rehydrate.

## Review resolution

**F1 (major): the pause test did not prove the background pass pauses. Confirmed and fixed (9a8d569d, test only).**
- I reproduced the reviewer's mutation in the worktree and then restored the file: `bg := scanCap; bg.Yield = nil` inside the d.goRun closure of accountPublicationAtStartup, so only the background pass stops yielding.
- With the tests as they were at 9d37a97a, TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight and TestStartupPublicationAccounting_StopEndsAPausedPass both passed 3/3 under that mutation (ok, 52.6 s).
- Root cause: with publicationBound=1ns, the bounded half of the pass parks on the held gate before accountPublicationAtStartup returns, and that park satisfied the test's park signal.
- Fix: a new test helper, armedParkSignal, counts only parks that happen after accountPublicationAtStartup has returned. The bounded half runs synchronously, so every park it makes comes before that return. Once the background pass is seen parked, it is blocked on the gate's idle channel, which only the request's leave closes, so the check that it has not finished is no longer a race.
- Result: under the same mutation both tests now fail 3/3 ("the background publication pass finished beside a request in flight"). Unmutated they pass 3/3.
- Production code is unchanged (onPark keeps its signature).
- Evidence: runs/yield-bgonly-mutation-daemon.txt and runs/yield-bgonly-green-daemon.txt.

**F2 (minor): the three launchers that hold the gate after the reply had no test. Confirmed and fixed (59e5c671, tests only).**
- New tests:
  - TestStartPromptRecording_HoldsTheCaptureGateUntilTheCaptureEnds and TestStartReplyWork_HoldsTheCaptureGateUntilTheWorkEnds call the launcher directly with work blocked on a channel. Each checks the gate count is 1 after the launcher returns, releases the work, joins promptWG, and checks the count is 0.
  - TestLaunchSessionEnd_HoldsTheCaptureGateUntilTheEndFinishes sends a real flush through dispatchOp with SessionEnd held (holdSessionEnd). It checks the count is at least 1 after the answer and exactly 1 once SessionEnd is entered, then releases it, waits in awaitSessionEnds, and checks 0.
- None of them sleeps.
- Mutation A (the enter/leave pair removed from all three launchers): all three tests fail every time.
- Mutation B (enter moved inside each goroutine, which leaves a gap after the request): 10/10, 10/10 and 7/10 red. This shape cannot be caught every time by construction, and this is recorded in runs/launch-gate-mutations-daemon.txt.

**F3 (minor): drains that hook traffic triggers were only partly counted. Confirmed and fixed (bfc77162).**
- Failing tests first:
  - TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries (the drainOnRequest pass).
  - TestLookAtClientSpools_HoldsTheCaptureGateForItsPass (the client-spool watcher's pass).
  - Both wrap the drainer's Dispatch to record d.capture.inFlight() just before each delivery reaches runIngested, which is after the drain's own spool read and fsynced lease-journal record for that line.
  - Red: expected 1, got 0 in both.
- Fix:
  - daemon.Drain enters the gate for the whole pass.
  - lookAtClientSpools enters it around dr.DrainClientSpools.
  - The captureGate doc comment now names drain passes as capture work.
- Gating Drain itself also covers redrainOnceServing, the idle drain, and the flush settle drain; nesting only raises the count. The startup and Stop drains run before the pass starts or after it has been joined, so gating them changes nothing.
- The store's AuditPublication holds no lock across Yield (checked in internal/store/publication_audit.go), so there is no deadlock.
- Green 3/3, together with the existing gate tests. Evidence: runs/drain-gate-daemon.txt.

**F4 (minor): the docs described admin.reload as if a user could send it. Confirmed and fixed (26addc19).**
- Verified: outside tests, OpAdminReload appears only in internal/daemon/daemon.go:488 (the route table) and internal/ipc/op.go.
- New test TestAdminReloadIsDescribedAsAnOpWithoutACommand in test/docs, which checks both directions:
  1. No non-test .go file under cmd/ or internal/cli names OpAdminReload or "admin.reload". A temporary mutation file proved this half catches a sender.
  2. The troubleshooting entry "A config change that did not take effect" and the config-reference section "Reloading the configuration" both contain the sentence "no `qompack` subcommand sends `admin.reload` in this build".
- Red (both pages) before the edit; green after.
- docs/troubleshooting.md edited by hand. docs/config-reference.md changed through the generator's prose in tools/devtool/genconfigdocs.go writeReloadSection, then regenerated; gen-config-docs --check reports up to date.
- Both pages now say that the LOUD lines and the `config reloaded` line are the reload a user can see.
- Evidence: runs/reload-op-docs.txt.

## Test runs
The four -run commands are recorded verbatim in the runs/ logs named above. Some use `|` alternations, which the runpatterns lint splits (see open issues). Package runs:
- ./internal/daemon/: ok at bfc77162. The daemon code is identical at 94698995.
- ./tools/devtool/ and ./test/docs/: ok at 26addc19.
- The lint subset, fmt-check, and go vet on Windows and GOOS=linux all pass.
- Record: runs/fix-full-packages.txt.

## Not re-measured
I did not repeat the 10x diagnostic. None of my changes touches the walk. F3 only makes the pass also wait out drain passes, so the pass time and the PutBytes numbers the implementer recorded (runs/pubscan-10x-before-after.txt) still stand for a store with no drain running.

## Criterion changes
None. No check was weakened; tests were only added or tightened.

## Owner decisions
None new: I added no bound or budget number. The implementer's needs_owner list was empty.

### Commits

- 9a8d569d test(daemon): prove the background publication pass itself parks
- 59e5c671 test(daemon): pin the capture gate on the post-reply launchers
- bfc77162 fix(daemon): hold the capture gate for a whole drain pass
- 26addc19 docs(config): say admin.reload has no qompack subcommand
- 94698995 docs(sdd): add the w15c-pubscan fix-seat package runs

### Tests

- `go test -p 2 -count=3 -run (background-pass pause and stop tests, under the background-only Yield=nil mutation, tests at 9d37a97a) ./internal/daemon/  [verbatim in runs/yield-bgonly-mutation-daemon.txt]` — ok 52.6s: the tests were blind to the mutation, confirming F1 <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `same command and mutation after the armedParkSignal change` — FAIL 3/3 for both tests (expected)
- `go test -p 2 -count=3 -v -run (pause, stop and both TestCaptureGate tests) ./internal/daemon/  [runs/yield-bgonly-green-daemon.txt]` — PASS 3/3, ok 48.9s <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=1 -run (three launcher gate tests) ./internal/daemon/ with the enter/leave pairs removed  [runs/launch-gate-mutations-daemon.txt]` — FAIL all three (expected) <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=10 -v -run (three launcher gate tests) ./internal/daemon/ with enter moved into each goroutine` — red 10/10, 10/10 and 7/10 (this mutation cannot be caught every time by construction) <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=3 -v -run (three launcher gate tests) ./internal/daemon/ unmutated` — PASS <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=1 -v -run (TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries and TestLookAtClientSpools_HoldsTheCaptureGateForItsPass) ./internal/daemon/ before the fix  [runs/drain-gate-daemon.txt]` — FAIL: expected 1, actual 0 in both <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=3 -v -run (both drain tests plus the dispatchOp, runIngested and launchSessionEnd gate tests) ./internal/daemon/ after the fix` — PASS, ok 5.1s <!-- runpatterns: the -run argument is a prose placeholder for the exact test names in the cited runs/ log, not a command to verify -->
- `go test -p 2 -count=1 -run '^TestAdminReloadIsDescribedAsAnOpWithoutACommand$' ./test/docs/` — RED before the doc edits (both pages); GREEN after
- `go run ./tools/devtool gen-config-docs --check` — docs/config-reference.md is up to date, exit 0
- `go test -p 2 -count=1 -run '^TestGenConfigDocs_ReloadSectionListsEveryClassifiedKey$' ./tools/devtool/ (also ran TestTroubleshootingNamesExactlyTheNoEffectReloadKeys, TestTroubleshootingLinksConfigReferenceSections and TestRelativeLinksResolve in ./test/docs/)` — ok
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 367.6s at bfc77162 (daemon code unchanged since)
- `go test -p 2 -count=1 -timeout=30m ./tools/devtool/` — ok 96.3s
- `go test -p 2 -count=1 -timeout=30m ./test/docs/` — ok 4.2s
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all eight sub-checks PASS, exit 0
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) ./internal/daemon/ ./tools/devtool/ ./test/docs/` — clean

### Open issues

- The 10x publication-pass diagnostic was not repeated after the review fixes. The walk is unchanged, and F3 only makes the pass also wait out drain passes, so the implementer's numbers in runs/pubscan-10x-before-after.txt stand for a store with no drain running.
- The launchSessionEnd gate test catches a dropped enter/leave pair every time, but catches enter moved inside the goroutine only probabilistically (7/10 in the mutation run). This cannot be closed without a scheduling hook.
- The one seam the captureGate comment already names is unchanged: between a fire-and-forget delivery's ACK and a worker's runIngested, the delivery sits in the ingest ring with the gate idle.
- The runpatterns lint splits `|` alternations in committed reports. If the committed report quotes the alternation patterns from the runs/ logs, it may need per-test -run patterns or allowlist entries.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/daemon/publication_yield_test.go:36-51 (armedParkSignal) and its call sites at :277-291 (BackgroundPassPausesWhileARequestIsInFlight) and :320-327 (StopEndsAPausedPass)` — The F1 fix introduced a race that can hang the test. The fix is correct for the reviewer's mutation, and the background-only Yield=nil mutation now fails. But armedParkSignal ignores every park that happens before arm(). The background pass starts inside d.goRun before accountPublicationAtStartup returns, and it can reach its first Yield before the test goroutine calls arm(). That path is short: s.use(), os.OpenRoot, then eachPhaseEntry -> proceed -> captureGate.wait -> onPark. When this happens, the park is dropped. The pass then blocks on the idle channel and never calls onPark again. The test's first select has no timeout, and neither `parked` nor `finished` ever fires, so the test hangs until go test's -timeout (30 min under the repo's rule). It does not fail cleanly. In StopEndsAPausedPass, cancel() comes after that select, so the same hang applies there. Normally the test goroutine wins by microseconds. On this shared machine, under co-load or when the OS deschedules the test thread, it can lose.
  - Evidence: I ran this in a scratch git-archive copy of HEAD 94698995 (the worktree was not touched, and the copy has since been removed). I added a `time.Sleep(200 * time.Millisecond)` before the `arm()` at publication_yield_test.go:284 to stand in for a deschedule. `go test -p 2 -count=1 -timeout=150s -run '^TestStartupPublicationAccounting_BackgroundPassPausesWhileARequestIsInFlight$' ./internal/daemon/` gave `panic: test timed out after 2m30s`, with goroutine 41 blocked in `[select]` at publication_yield_test.go:287, the first select on parked/finished. At HEAD without the sleep, the nine gate tests passed at -count=2 (ok 50.6s). The other three fixes check out. F2: the three launcher tests are present and pass. F3: Drain and lookAtClientSpools now enter/leave, every production drain path goes through d.Drain or that watcher pass, and no deadlock is possible because only the publication pass waits on the gate and runCtx cancellation releases it. F4: docs and generator agree, gen-config-docs --check is clean, and TestAdminReloadIsDescribedAsAnOpWithoutACommand passes.
  - Fix: Tell the two passes apart by what they are, not by when the park happens. This was the reviewer's first suggestion. Change captureGate.onPark to `func(ctx context.Context)` and call `park(ctx)` in wait. In the test, signal only when `_, ok := ctx.Deadline(); !ok`. The background pass runs under runCtx, which has no deadline, and the bounded half runs under WithTimeout, so no park is lost whatever the ordering. If the seam must stay test-only, a second option: arm before calling accountPublicationAtStartup and have onPark ignore parks whose ctx is already Done. The bounded half's 1ns context has expired by then, and runCtx has not. Either way, re-run the background-only Yield=nil mutation to confirm it still fails. Also add a bounded fallback (for example `case <-time.After(liveOrderBound): t.Fatal(...)`) to the first select in both tests, so a lost signal fails the test instead of hanging it.


## Coordinator follow-up

The verifier's remaining finding (armedParkSignal could drop the background pass's only park and hang the test) was fixed by the coordinator in `40db2e19`: onPark receives the waiter's context and the background-pass signal matches a context with no deadline; both waits gained a bounded fallback. The gate tests pass at -count=3, and the background-only Yield=nil mutation fails both pause tests.

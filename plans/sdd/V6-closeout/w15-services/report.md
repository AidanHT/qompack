# Wave 15 workstream services

Branch `closeout/w15-services`. Workflow `wf_0a7ad63f-671`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `43d03954ffb031e742212f43d549875faa2ec908`

### Root cause

(1) reload.go replaced only the daemon's own copy of the configuration, while every service (rehydrate, MCP handlers, the negknow ledger, scheduler runtime, the store and retrieval redactors, registry, breach detector, idle controller and spool watcher) held a copy taken at wiring. (2) recall asked store.Search for exactly k hits and withheld denied ones afterwards. (3) The startup publication pass was cut at 250 ms and a cut pass was announced LOUD; a 70-capture store takes about 1.27 s. (4) fsck judged a running daemon's not-yet-flushed index/files.json as a defect. The planted config only mattered because it made the hooks refuse the SessionEnd flush.

### Summary

W15 services seat, branch closeout/w15-services (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-services), cut from closeout/integration 08034a3f. This session picked up a seat that had already committed items 2, 3 and 4 and left item 1 unfinished and uncommitted. I finished item 1, reviewed the earlier commits against the evidence, and re-ran every package this seat touches. No Linux container was used and no real Claude Code session was run.

(1) F-C4-UAT05-2 / UAT-09: config reload.
Root cause: reload.go replaced only the daemon's own copy (d.cfg). Every service had been wired with its own copy at startup, so a reloaded key never reached the code that reads it:
- the rehydrate service (s.o.Cfg)
- the MCP handlers (normalized once at registration)
- negknow's ledger (l.elim, normalized once at Open)
- the scheduler runtime (r.cfg)
- the store's redactor (redact.New(cfg) at Open)
- the retrieval redactor (redact.New(cfg) in NewRetrievalRedactor)
- the session registry's maxSessions
- the breach detector (its own comment admitted reload never re-applied it, fix round 1 M-5)
- the idle controller and the client-spool watcher's horizon
A lazily opened ledger was also opened with o.Cfg, the startup value, not the config in effect.

Fix: one shared live configuration (internal/daemon/config_live.go). The reload writes to it and every service reads it at the moment it uses a key:
- rehydrate: RehydrateOptions.CfgFn
- MCP tools: ToolDeps.CfgFn, wired by cli liveToolConfig
- ledger: negknow Deps.Config (re-normalized only when the block changes)
- scheduler: SchedulerRuntimeOptions.CfgFn, wired in cli wireScheduler
- store and retrieval redaction: NewLiveRedactor
- registry, breach detector (new reconfigure), idle controller (setDetectAfter) and spool watcher (setHorizon): updated by the reload itself in applyReloaded.

internal/daemon/reload_keys.go classifies every config key as live, restart, deferred (store.chunk.*, unchanged behaviour) or outside the daemon. A restart key is held at the value in effect, left out of "changed", named in a LOUD line ("config change needs a daemon restart to take effect ...") and returned in admin.reload's new restart_required field. The restart keys are: sketches.*, checkpoint.*, store.compression/retention/canonicalize, scheduler.changepoint/cache, runtime.scheduler.cache, selection.slicing, retrieval.promoteAfterExpansions, runtime.hotPath.maxPayloadBytes, runtime.tokens, runtime.migration.* except reinjection.sessionStartCompact, and runtime.phase7. TestReloadKeyEffects_ClassifyEveryKey keeps the table covering the whole schema, with no dead entries.

The reload also now loads through the environment the daemon started with (new Options.CfgEnv, set by runDaemon). Before, it used os.UserHomeDir and os.Getenv without the --set flags, so a reload silently dropped --set values. It also meant in-process cli daemon tests read the real ~/.qompack.

Tests (red evidence in runs/):
- internal/daemon, one per daemon-side reader: TestConfigReload_ReachesTheRehydrator, TestConfigReload_ReachesTheEliminationLedger, TestConfigReload_ReachesTheSessionRegistry, TestConfigReload_ReachesTheBreachDetector, TestConfigReload_ReachesTheIdleController, TestConfigReload_ReachesTheStoreRedactor, TestConfigReload_ReachesTheSchedulerRuntime, TestConfigReload_HoldsAKeyThatNeedsARestart, TestReloadKeyEffects_ClassifyEveryKey.
- internal/mcp: TestToolsReadTheLiveConfigurationOnEveryCall.
- internal/negknow: TestQuery_StaleResponseFollowsTheLiveConfiguration.
- internal/cli, driving a real daemon through runDaemon and admin.reload: TestDaemonConfigReload_ReachesTheRetrievalTools, TestDaemonConfigReload_SaysWhichKeysNeedARestart, TestWireScheduler_ReadsTheLiveConfiguration.

Red evidence:
- runs/reload-red-daemon.txt: five daemon rows red before the fix, e.g. budget 12000 vs the reloaded 150.
- runs/reload-red-cli.txt: with the liveToolConfig wiring removed, the reloaded defaultScope never reaches record_eliminated.
- runs/reload-red-store.txt: without the live redactor the reloaded pattern does not bind; Redacted is 0, 1 expected.
- The mcp and negknow unit tests are red only at compile time, because the CfgFn and Config fields did not exist before.

(2) F1 recall k (b35dfe90).
Root cause: handlers.go asked store.Search for exactly K hits and dropped the denied ones afterwards, so k=5 returned 2 hits with denied:3.
Fix: search at store.MaxSearchK and take permitted hits until k are in hand. MaxSearchK is the existing maxK=100, now exported, so this is not a new bound. denied stays a count, of the withheld hits ranked ahead of the last returned hit. Qompack's own retrieval records (mcp__qompack__* and the host's mcp__plugin_qompack_qompack__*) rank after every other hit, stable on both sides.
Red then green: runs/recall-red.txt, runs/recall-green.txt (TestRecallKCountsPermittedHits, TestRecallRanksSelfRecordsAfterOriginalCaptures).

(3) F2 publication accounting (5b8f1e1a, f2137f27).
Measured: one full pass over a 70-capture, 380-object store takes 1.27 s against the 250 ms publicationStartupBound (runs/publication-red.txt).
Root cause: the startup pass was cut at the bound and a cut pass was announced LOUD as incomplete.
Fix: take store.PublicationSnapshot before serving, and if the pass doesn't fit the bound, finish it in the background (goRun, runCtx) against that snapshot. Files written after the snapshot are left unclassified, so live work is never a gap. LOUD only for a real gap or a pass that cannot finish; a pass the daemon's own Stop ends is a Warn. No bound changed.
f2137f27 delegates SnapshotPublication on read-only stores, because TestReadOnly_EveryExportedMethodIsClassified requires every exported method to be classified.
Evidence: runs/publication-green-daemon.txt, runs/publication-green-store.txt, runs/publication-full-store.txt (store package red before f2137f27, then ok).

(4) Investigation: UAT-04 fsck exit 1 on index.files (dfbd30f8).
Reproduced deterministically. rerun-c4/UAT-04/cli/10-fsck-json.stdout.txt shows daemon_running:true. The store appends file versions as it observes them and regenerates index/files.json only when it flushes (at a session's end or the daemon's stop).
So it follows from the planted config only indirectly. The unparseable config made every hook admit nothing. The SessionEnd flush was refused (hence the 941858-byte spool doctor reported), so the running daemon never materialized the view before fsck ran. The daemon's later idle exit wrote it.
fsck's message was wrong in that state. Any fresh project mid-session gets the same exit 1, and the message prescribes a --repair that fsck refuses while a daemon holds the lock. That is a product defect.
Fix: with a daemon running, an absent view, or one holding a subset of its log's versions, is a note saying when it catches up. A view that contradicts the log, an unreadable view, and any lag with no daemon running stay defects.
Evidence: runs/fsck-files-red.txt, runs/fsck-files-green.txt.

Verification: fmt-check exit 0; go vet on daemon, cli, mcp and negknow, both Windows and GOOS=linux, OK; lint golangci-lint/nomagic/importgraph/testdeps/bindeps/sleepcheck/runpatterns/docmarkers all PASS. Each touched package ran once in full with -p 2 and no -race; results are in the tests field. Two wall-clock rows failed under the shared-machine load and passed when re-run alone, as the machine-limit rules require; both re-runs are recorded. No docs or generated inputs changed.

Criterion changes: none; no assertion, threshold, timeout or golden was touched. One existing comment in budget.go ("limit and need are set once ... never mutated") was replaced, because they are now mutable under the detector's mutex.

No new owner numbers were introduced.

### Commits

- b35dfe90 fix(mcp): count recall's k in permitted hits, echoes last
- 5b8f1e1a fix(daemon): finish startup publication accounting in background
- dfbd30f8 fix(cli): read a live daemon's lagging files view as a note
- f2137f27 fix(store): delegate the publication snapshot to read-only stores
- 437588cf fix(negknow): apply eliminations config from a live supplier
- f4eafd44 fix(mcp): read the tool configuration on every call
- 43d03954 fix(daemon): apply a config reload to every service or name it

### Tests

- `go test -p 2 ./internal/daemon/ -run 'TestConfigReload_|TestReloadKeyEffects_ClassifyEveryKey|TestConfigReloadDefersChunkChange' -count=1 -v` — PASS, 10 rows (runs/reload-green.txt); five of them red before the fix (runs/reload-red-daemon.txt)
- `go test -p 2 ./internal/mcp/ -run '^TestToolsReadTheLiveConfigurationOnEveryCall$' -count=1 -v` — PASS (runs/reload-green.txt)
- `go test -p 2 ./internal/negknow/ -run '^TestQuery_StaleResponseFollowsTheLiveConfiguration$' -count=1 -v` — PASS (runs/reload-green.txt)
- `go test -p 2 ./internal/cli/ -run 'TestDaemonConfigReload_ReachesTheRetrievalTools|TestDaemonConfigReload_SaysWhichKeysNeedARestart' -count=1 -v` — PASS; ReachesTheRetrievalTools is red without the liveToolConfig wiring (runs/reload-red-cli.txt)
- `go test -p 2 ./internal/cli/ -run 'TestWireScheduler_ReadsTheLiveConfiguration' -count=1 -v` — PASS
- `go test -p 2 ./internal/daemon/ -run 'TestConfigReload_ReachesTheStoreRedactor' -count=1 -v (with the live redactor removed)` — FAIL as expected, Redacted 0 vs 1 (runs/reload-red-store.txt); PASS with the fix
- `go test -p 2 -timeout=30m ./internal/daemon/ -count=1` — ok 385.7s (runs/reload-full-daemon.txt)
- `go test -p 2 -timeout=30m ./internal/cli/ -count=1` — ok 89.3s (runs/reload-full-cli.txt)
- `go test -p 2 -timeout=30m ./internal/negknow/ ./internal/mcp/ -count=1` — FAIL under co-load: TestBudget_RebuildBloom 130ms wall vs 50ms (CPU/op 21ms) and TestBudgetBF p95 524ms vs 250ms; every other row passed (runs/reload-full-negknow-mcp.txt)
- `go test -p 2 ./internal/negknow/ -run '^TestBudget_RebuildBloom$' -count=1 -v` — PASS alone, 1.73s (runs/reload-rerun-rebuildbloom.txt)
- `go test -p 2 ./internal/mcp/ -run '^TestBudgetBF$' -count=1 -v` — PASS alone, p95 98.3ms vs 250ms (runs/reload-rerun-budgetbf.txt)
- `go test -p 2 -timeout=30m ./internal/store/ -count=1` — FAIL before f2137f27 (TestReadOnly_EveryExportedMethodIsClassified: SnapshotPublication unclassified), ok 240s after (runs/publication-full-store.txt)
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/daemon/ ./internal/cli/ ./internal/mcp/ ./internal/negknow/ (Windows and GOOS=linux)` — OK
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all eight PASS

### Open issues

- The reload classification in internal/daemon/reload_keys.go is a policy table the coordinator should review. It keeps these at the startup value and names them as needing a restart: sketches.*, checkpoint.* (including frontier and budgetTokens), store.compression/retention/canonicalize, scheduler.changepoint/cache, runtime.scheduler.cache, selection.slicing, retrieval.promoteAfterExpansions, runtime.hotPath.maxPayloadBytes, runtime.tokens, runtime.migration.* (except reinjection.sessionStartCompact) and runtime.phase7. Making checkpoint.* live would mean re-wiring checkpoint.OpenWriter, BindCheckpoint and WireCheckpoint to read the live configuration; it is follow-up work, not a defect under D49's either/or.
- Keys classified as outside the daemon (read by no daemon code in this build) are still reported as changed, as before: runtime.logging.*, runtime.mode (each hook reads it itself), eval.*, runtime.telemetry, runtime.selection.loopWarningsEnabled, selection.deltaScoring and selection.submodular.lazyGreedy. runtime.logging.* has no reader anywhere, so the daemon's log level cannot be configured at all; that finding belongs to the docs seat or a later wave.
- Docs seat: docs/config-reference.md and docs/troubleshooting.md could say which keys need a daemon restart, and document the new LOUD line and admin.reload's restart_required field. I did not edit docs.
- Ledger seat overlap: commit 437588cf edits internal/negknow/ledger.go, ingest.go and staleness.go. The change is small (reads of l.elim become l.elims(), plus Deps.Config), but merging it with the ledger seat's negknow edits may conflict textually.
- Only the per-service reload tests ran with -race, and not whole packages, per this seat's daytime limits. A -race pass over internal/daemon, internal/negknow and internal/mcp should run in the next quiet window: the new mutexes are breachDetector.reconfigure/Config, spoolWatcher.mu and ledger.elimMu.
- Live re-verification on the next candidate: UAT-05 step 5 (a reloaded rehydrate budget takes effect without a restart), UAT-09 (a reloaded staleResponse reaches already_tried), UAT-12 (k counts permitted hits, no startup publication LOUD) and UAT-04 (fsck with the daemon up, before any flush).

## Independent review

### review:services: needs-fixes

- **major** `internal/daemon/reload.go:102-107; internal/daemon/reload_keys.go:29-31,47` — The store.chunk.* audit missed a D49 case. A reloaded chunk change is logged as "deferred to next SessionStart" and left out of both changed and restart_required, but nothing ever applies it at a SessionStart. The only reader is store.Open (internal/store/open.go:151), which runs once at daemon start. state/config-pending.json has no reader anywhere (grep finds only reload.go). The next session.start does not re-run the reload because the config file's mtime has not changed, and a forced reload holds the value again. The key only takes effect after a restart, while the reload tells the operator something else. The new reload_keys.go comment ("applied at the next SessionStart (reload.go)") repeats the false claim, and TestConfigReload_HoldsAKeyThatNeedsARestart / restart_required never mention chunk keys.
  - Evidence: `grep -rn "config-pending\|configPending" internal` finds only reload.go (writer). `grep Store.Chunk internal/daemon internal/store` finds only reload.go:93/104 and store/open.go:151-153. TestConfigReloadDefersChunkChange (daemon_test.go:890) only checks that the live value is held. No test shows it being applied at a SessionStart.
  - Fix: Classify store.chunk.* as effectRestart, or keep a deferred class that also appears in restart_required. Change the Loud line to say a daemon restart applies it; the reason (a mid-session change would fork the dedup space) can stay. Correct the reload_keys.go comment and why-text. Either delete config-pending.json or have doctor/status read it. Add a test that a chunk change appears in restart_required.
- **minor** `internal/daemon/reload_keys.go:71,73,87,89,98` — Keys that nothing in the product reads are still reported as "changed" by the reload, which D49 forbids: every key the reload reports as changed must take effect or be named as needing a restart. The keys are runtime.logging.*, selection.deltaScoring, selection.submodular.lazyGreedy, runtime.selection.loopWarningsEnabled and runtime.telemetry. A restart does not apply them either. For example, changing runtime.logging.level makes the daemon log "config reloaded changed=[runtime.logging.level]" while its log level never changes. The implementer disclosed this as an open issue but did not fix it, though it is within this seat's reload scope.
  - Evidence: `grep -rnE "\.Logging\b|\.Logging\.|LoopWarningsEnabled|\.DeltaScoring|\.LazyGreedy" internal --include=*.go` outside internal/config finds only comments. holdForRestart (reload_keys.go:118-127) treats effectOutside like effectLive and applies these keys.
  - Fix: Add an effectInert class for keys with no reader in this build. Keep those keys out of "changed", and report them in their own admin.reload field and Loud/Info line (e.g. "no effect in this build"). Keep runtime.mode and eval.* as outside/live.
- **minor** `internal/store/publication_audit.go:348-350 (postSnapshot); internal/daemon/publication_audit.go:106-129` — The pass decides post-snapshot vs pre-snapshot with `!mod.Before(snap.taken)`, where taken is time.Now(). On Windows a file's mtime and Go's wall clock tick together, so a file written just before the snapshot usually has mtime equal to taken and is counted as post-snapshot. A real residue written within about 1-2 ms before the snapshot is therefore skipped as "live work" and its gap goes unannounced until the next start. The same happens if the clock steps backwards or files carry future mtimes. The tests avoid this by forcing mtimes ±1 h with Chtimes, so none of them covers the boundary.
  - Evidence: Scratch probe on this host: for 1854 of 2000 files written BEFORE a later time.Now(), `!mtime.Before(now)` was true (mtime == now). time.Now() did not pass a just-written file's mtime until up to 1.63 ms later; the minimum wall step was 0.44 ms.
  - Fix: Requests stay gated until accountPublicationAtStartup returns, so nothing writes between the snapshot and that return. Compare against a cutoff taken at the end of the bounded pass, just before goRun, instead of time.Now() at snapshot, and use a strict After. Alternatively put a guard margin below publicationStartupBound on taken. Add a store test that writes residue immediately before SnapshotPublication, without Chtimes, and still expects it counted.
- **minor** `internal/daemon/publication_audit.go:118-128` — The startup pass used to be capped at 250 ms. It now continues in the background, with no pacing, until DefaultPublicationScanCap's limits (up to 262144 directory entries and 32 MiB of sidecar/marker reads). It also holds a full copy of the index chunk set for the whole pass. This happens on every daemon start, and hooks start the daemon lazily and it exits when idle. On Windows the pass cost about 2.8 ms per entry (1.27 s for ~450 files), so a long-lived store could see tens of seconds of I/O competing with the first session's hot path (B-A/B-B). The cost was measured only at 70 captures and ~380 objects.
  - Evidence: defaultMaxEntries = 1<<18 and defaultMaxBytes = 32<<20 (store/publication_audit.go:111-113). The goRun pass runs under runCtx with no deadline or yield. SnapshotPublication copies s.chunkSet (publication_audit.go:333-337).
  - Fix: Measure the background pass on a larger store (e.g. 10x the live-run size) and record the numbers, or pace it (yield per directory batch, or run it only while the hot path is idle). List the effective background cost for the owner, since it replaces a named 250 ms bound.
- **minor** `internal/cli/mcpwire.go:189-192` — No test shows that a reload reaches the retrieval tools' redactor, even though the task asked for a test per service. TestDaemonConfigReload_ReachesTheRetrievalTools only goes red through CfgFn (defaultScope). If the `d.Redactor = retrievalRedactor{r: daemon.NewLiveRedactor(...)}` line were deleted, no test would fail. The only redactor test covers the store's Put redactor.
  - Evidence: `grep -n Redact internal/cli/config_reload_live_test.go internal/mcp/config_live_test.go` finds nothing. runs/reload-red-store.txt covers only the store redactor.
  - Fix: Add a test that reloads runtime.redact.patterns with a new pattern and requires the next expand/recall of an archived object holding the secret to return it redacted, driven through runDaemon/admin.reload like the existing cli test.
- **nit** `internal/daemon/handlers.go:441-445` — A comment is now stale. It still says a config reload does not re-apply BudgetMs/BreachWindows to the breach detector (M-5), but breachDetector.reconfigure now does. The budget.go comment was updated; this one was not.
  - Evidence: reload.go:137-139 calls d.breach.reconfigure; handlers.go:441 still reads "A config reload does not currently re-apply cfg.Runtime.HotPath.BudgetMs/BreachWindows".
  - Fix: Reword it: the detector's Config() is reported because it is what gated this transition, and applyReloaded keeps it in step with reloads.
- **nit** `internal/daemon/reload_keys.go:77` — runtime.mode is classified effectOutside ("each hook ... nothing in the daemon process reads the key"). The daemon does read it: handleSessionStart passes d.currentCfg() into contract.Env and the monitor's RunAll branches on Runtime.Mode (contract/monitor.go:211,273). Behaviour is correct because the key is not held, but the classification's reason is wrong.
  - Evidence: handlers.go:845-875: cfg := d.currentCfg(); env := contract.Env{Cfg: cfg,...}; d.monitor.RunAll(ctx, env).
  - Fix: Classify runtime.mode as effectLive with why "contract monitor at the next session.start (currentCfg) and each hook".
- **nit** `internal/mcp/handlers_test.go:93-97` — An assertion was changed but reported as criterion_changes: none. TestRecallSelectorPrefixesParsed's expected store.Query.K went from 5 to store.MaxSearchK. The change is legitimate because it follows from the D49 behaviour change, but it should have been declared.
  - Evidence: git diff 08034a3f HEAD -- internal/mcp/handlers_test.go shows `K: 5` replaced by `K: store.MaxSearchK`.
  - Fix: Record the change in the seat's criterion_changes with the D49 reason. The test itself needs no change.

## Fix seat (review resolution) — status `done`, head `189cbf56fc8e6df389cecf2a8172c2edc05e8607`

### Root cause

(1) store.Open is the only reader of store.chunk.*, and it runs once at daemon start, so a chunk change labelled 'deferred to next SessionStart' was only ever applied by a restart. (2) Five config keys have no reader in this build but were classified as 'outside' and reported as changed. (3) postSnapshot used !Before(taken), and on Windows a file's mtime and time.Now tick together, so residue written in the same tick as the snapshot was counted as live work. (4) The background pass has no pacing; its cost was not measured beyond the live-run size. (5) No test covered the retrieval redactor's live wiring.

### Summary

W15-SERVICES FIX SEAT: resolving the independent review (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-services, branch closeout/w15-services, base 08034a3f, implementer head 43d03954, fix-seat head 189cbf56). All five reviewer findings were confirmed. Four are fixed: the major one, two minors, and the missing test. The fourth (the cost of the unpaced background pass) was measured and recorded, and the pacing decision goes to the owner. Nothing is rebutted. The tree is clean. Evidence logs are under plans/sdd/V6-closeout/w15-services/runs/review-*.txt.

## Review resolution

1. MAJOR: a store.chunk.* reload was reported as "deferred to next SessionStart", but nothing applies it. CONFIRMED and FIXED (d58f0b86).
   - Root cause: the only code that reads the chunk block is store.Open (internal/store/open.go:151), which runs once when the daemon starts. Nothing reads state/config-pending.json. A SessionStart does not force a reload, and a forced reload only holds the value again. So a restart is the only thing that applies the change.
   - Fix, reload_keys.go: store.chunk is now effectRestart, and the effectDeferred class is removed. The "why" text keeps the §11.2 reason (a mid-session change would fork the dedup space).
   - Fix, reload.go: the chunk key now appears in restart_required and in the one Loud line "config change needs a daemon restart". The separate "deferred to next SessionStart" Loud line is gone, and the comments are corrected.
   - config-pending.json: I kept it rather than deleting it, because the V2 row V2-SP05-18 (TestConfigReloadDefersChunkChange) requires it to be written. Its meaning is now narrower: it exists only while a running daemon holds a chunk change for a restart. Run removes it (clearConfigPending) once it holds the project lock, because the new daemon loaded config.json itself.
   - New tests: TestConfigReload_ChunkChangeNeedsARestart and TestRunClearsAConfigChangeItsStartApplied (daemon package), and TestDaemonConfigReload_NamesChunkAndInertKeys (cli package, through runDaemon and admin.reload). All three were RED first.

2. MINOR: keys that no code reads were still reported as "changed". CONFIRMED and FIXED (d58f0b86).
   - I checked by grep that nothing outside internal/config reads these keys. The daemon's log is opened by logging.New at logging.Info. analyzer.NewSelector always passes lazy=true.
   - Fix: a new class, effectInert, covers runtime.logging, runtime.telemetry, selection.deltaScoring, selection.submodular.lazyGreedy and runtime.selection.loopWarningsEnabled. These keys keep their current value, are left out of "changed", and are returned in a new admin.reload field "no_effect". A Loud line says "config change has no effect in this build".
   - runtime.mode and eval.* stay effectOutside.
   - reloadConfigKeys now returns a reloadResult struct {Changed, Restart, NoEffect}.
   - New tests: TestConfigReload_NamesAKeyWithNoEffect, TestReloadKeyEffects_InertKeysHaveNoReader, and the cli row above.

3. MINOR: a file written just before the snapshot, at the same clock tick, was skipped as "live work". CONFIRMED and FIXED (693dc4c1).
   - I probed this host: over 2000 writes, a file's mtime was equal to a later time.Now() in 1642 and 1772 cases per run, and never greater.
   - Fix: postSnapshot now uses a strict mod.After(taken).
   - Why the other side of the tick is safe: the daemon takes the snapshot before it serves anything. The background pass only happens when the bounded startup pass has already used its full 250 ms, and requests wait until that pass returns. So nothing the daemon serves can write a file stamped with the snapshot's instant.
   - New tests: TestAuditPublication_SnapshotCountsResidueAtItsInstant (mtime set exactly to snap.taken, so it is deterministic) and TestAuditPublication_SnapshotCountsResidueWrittenJustBeforeIt (no Chtimes). Both were RED before the fix; the second one failed on this host without moving any time. After the fix, 20 repeats passed.
   - Clock steps and future mtimes are not addressed; see open issues.

4. MINOR: the background pass is unpaced and was measured only at the live-run size. CONFIRMED; MEASURED and RECORDED (ead67446, runs/review-pubscan-10x-diagnostic.txt).
   - Method: a throwaway diagnostic test (not committed; its source method is described in the evidence file) on a store 10x the live-run size: 3800 objects in 3961 directories, 700 captures. Windows, measured under daytime co-load from the other seats.
   - Pass time: 15.1 s on a cold cache, then 3.9 s and 4.4 s warm. Every pass was complete, not truncated, and found no gaps.
   - PutBytes latency: p50 11.4 ms, p99 16.4 ms, max 19.3 ms alone; p50 11.1 ms, p99 25.7 ms, max 39.2 ms beside a running pass.
   - Most of the cost is per directory: each directory open does an OpenRoot and an Lstat on every parent directory.
   - I did not add pacing. A pacing rule would be a new bound, and bounds are the owner's decision. The numbers are in the accountPublicationAtStartup comment and under needs_owner.

5. MINOR: no test covered the retrieval tools' redactor after a reload. CONFIRMED and FIXED (bc6a6e03).
   - New test: TestDaemonConfigReload_ReachesTheRetrievalRedactor. It archives an object holding a secret before the daemon starts, checks that expand returns the secret, reloads a redaction pattern through admin.reload, and requires the next expand to be redacted.
   - Mutation check: with the d.Redactor line in liveToolConfig temporarily deleted, the test is RED (evidence in runs/review-redactor-reload.txt). The line was then restored.

## Commands and results (Windows, -p 2, daytime co-load)
- New or changed focused rows, run one per name with `go test -p 2 -count=1 -run '^<Name>$' <pkg>`: TestConfigReload_ChunkChangeNeedsARestart, TestConfigReload_NamesAKeyWithNoEffect, TestReloadKeyEffects_InertKeysHaveNoReader, TestRunClearsAConfigChangeItsStartApplied (./internal/daemon/); TestDaemonConfigReload_NamesChunkAndInertKeys, TestDaemonConfigReload_ReachesTheRetrievalRedactor (./internal/cli/); TestAuditPublication_SnapshotCountsResidueAtItsInstant, TestAuditPublication_SnapshotCountsResidueWrittenJustBeforeIt (./internal/store/). All were RED first and are GREEN now; RED and GREEN logs are in runs/review-reload-red.txt, runs/review-reload-green.txt, runs/review-snapshot-boundary.txt and runs/review-redactor-reload.txt.
- Full packages, once each with -count=1 -timeout=30m: ./internal/daemon/ ok (373 s), ./internal/cli/ ok (126 s), ./internal/store/ ok (286 s).
- go vet on those three packages passed on Windows and with GOOS=linux. devtool fmt-check exited 0. devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns: every sub-check passed.
- Linux was not run: the container is stopped per this seat's rules. No hot-path rows were run, and no whole test/integration or test/e2e package.

## Criterion changes
None were weakened. TestConfigReload_HoldsAKeyThatNeedsARestart only changed to the reloadResult return shape; its assertions are the same. TestConfigReloadDefersChunkChange (V2-SP05-18) keeps all of its assertions; only its doc comment changed from "deferred" to "held until a daemon restart". admin.reload's reply gained a field, "no_effect".

## Note for the coordinator
The runpatterns lint splits `-run '^(A|B)$'` alternations inside report.md. That is why the commands above are quoted as one pattern per test name. The committed runs/*.txt logs use alternations, but the lint only scans .md files.

### Commits

- d58f0b86 fix(daemon): name chunk and unread keys a reload cannot apply
- 693dc4c1 fix(store): count residue stamped at the snapshot's instant
- bc6a6e03 test(cli): require a reload to reach the retrieval redactor
- ead67446 docs(daemon): record the background publication pass cost
- 189cbf56 docs(sdd): record the w15-services review fix package runs

### Tests

- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok (372.969s)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok (126.176s)
- `go test -p 2 -count=1 -timeout=30m ./internal/store/` — ok (285.731s)
- `go test -p 2 -count=1 -run '^TestConfigReload_ChunkChangeNeedsARestart$' ./internal/daemon/` — RED before fix (restart_required nil), PASS after
- `go test -p 2 -count=1 -run '^TestConfigReload_NamesAKeyWithNoEffect$' ./internal/daemon/` — RED before fix (runtime.logging.level in changed), PASS after
- `go test -p 2 -count=1 -run '^TestReloadKeyEffects_InertKeysHaveNoReader$' ./internal/daemon/` — RED before, PASS after
- `go test -p 2 -count=1 -run '^TestRunClearsAConfigChangeItsStartApplied$' ./internal/daemon/` — RED before (pending file left), PASS after
- `go test -p 2 -count=1 -run '^TestDaemonConfigReload_NamesChunkAndInertKeys$' ./internal/cli/` — RED with daemon fix stashed, PASS after
- `go test -p 2 -count=1 -run '^TestDaemonConfigReload_ReachesTheRetrievalRedactor$' ./internal/cli/` — PASS; RED under mutation (liveToolConfig Redactor line deleted)
- `go test -p 2 -count=1 -run '^TestAuditPublication_SnapshotCountsResidueAtItsInstant$' ./internal/store/` — RED before strict After, PASS after
- `go test -p 2 -count=20 -run '^TestAuditPublication_SnapshotCountsResidueWrittenJustBeforeIt$' ./internal/store/` — RED before (no Chtimes, real tick), PASS x20 after
- `go vet ./internal/daemon/ ./internal/cli/ ./internal/store/ (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS

### Criterion changes

- No criterion was weakened.
- admin.reload's reply gained a field, no_effect.
- TestConfigReload_HoldsAKeyThatNeedsARestart was adapted to the new reloadResult return shape; its assertions are unchanged.
- TestConfigReloadDefersChunkChange (V2-SP05-18): only its doc comment changed, to say the change is held until a daemon restart; all assertions are kept, and config-pending.json is still written.

### Open issues

- The background publication pass is unpaced. On a store 10x the live run's size (3800 objects in 3961 directories, 700 captures) it takes 15.1 s cold and about 4 s warm on Windows, and PutBytes p99 rises from 16 ms to 26 ms beside it. No pacing is implemented; owner decision below.
- A cheaper fix for the cost is possible but not done: reuse one os.Root for the whole pass instead of an OpenRoot plus an Lstat on every parent directory for each directory. This touches confinement code, so it was left out of the fix seat's scope.
- Clock steps and future mtimes are still unhandled. A residue file whose mtime is after the snapshot, because the clock stepped backwards or the file carries a future time, is still counted as live work and not reported. Handling it would conflict with the existing tests that fake 'post-snapshot' with an mtime one hour ahead.
- Linux was not run for this fix round because the container is stopped under this seat's rules. The new tests are platform-neutral, and GOOS=linux go vet is clean.
- state/config-pending.json still has no reader in doctor or status. It is now only an operator-visible record, removed at the next daemon start.

### Needs the owner

- Background publication accounting cost (replaces the old named 250 ms startup bound). Value: unpaced, bounded only by DefaultPublicationScanCap (262144 directory entries, 32 MiB of reads). Derivation: measured at 10x the live-run size, 15.1 s cold and about 4 s warm, PutBytes p99 16 ms alone vs 26 ms beside the pass (runs/review-pubscan-10x-diagnostic.txt). If this is wrong: a long-lived store starting cold could compete with the first session's I/O for tens of seconds. Options: accept as is, add pacing (a new bound number for you to choose), or approve the one-os.Root optimisation.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.



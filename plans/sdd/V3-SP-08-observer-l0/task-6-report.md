# Task 6 report — Commit 6: SessionStart/SessionEnd lifecycle and daemon wiring

Branch: `feat/sp08-observer-l0`, one commit on top of 7885650.
Commit: `f1c77534910f7ff2cb9498a2df7ebd6b25d4cec9` —
`feat(observer): SessionStart/SessionEnd lifecycle and daemon wiring` (amended once, before any
push, to fold in the quarantine-scaffolding fix the first full `devtool test` run surfaced — see
"The devtool test incident" below).
Message verified: `go run ./tools/devtool check-commit-msg <file with git log -1 --format=%B>` →
exit 0 (the subcommand takes a FILE path, not a rev — bare/rev forms exit non-zero), and the body
carries the exact `Refs:` footer and no attribution trailer.

## Files

- `internal/observer/session.go` (new) — OnSessionStart (loadState-once via `session()`, frontier
  adoption, `ensureSegment` with PrevSegment/SegStartTurn/SegStartPos bookkeeping, source switch
  delegating compact/clear to the Rehydrator seam with the Info line when nil, unknown source =
  startup), OnSessionEnd in the brief's order (BuildSegment with `Members: nil` + 5-key feature
  map, `Segments().Close`, `Graph.Flush`, `Store.Flush`, `saveSketches` → `touch.cms`/`explore.hll`
  via `sketch.Save` — never `tried.bloom` — `persistState`, `Store.GC` with
  `GCPolicy{RetainDays/RetainSessions from config, Deadline: gcDeadline}`, explicit
  `st.mu.Unlock()` with no defer, then the `o.mu` delete of the session).
- `internal/observer/observer.go` — the two minimal Commit-0 bodies deleted; replaced by a pointer
  comment. `state.go`'s `Persist` was already complete (Commit 2) and still satisfies `Persister`.
- `internal/daemon/observer_ops.go` (new) — `WireObserver(o *Options)` opening store/DAG/SketchSet
  when nil per the controller ruling and assigning them back onto `*Options`; the single `o.Bind`
  attaching the five seams (ObserveTool/ObserveStop/SessionEnd wrapped to error-only,
  ObservePrompt assigned directly, SessionStart wrapped to add the nil-tolerant
  `s.Ledger.RefreshStaleness(ctx, s.Store)` on non-compact/clear sources, error → Warn);
  `modeSrc` captured from `s.Mode` with the nil guard mapping to `observer.ModePassive`
  (`mapContractMode`); `symbolAdapter` over `symbols.Extractor`; OnFeatures/OnSignals mapped to
  scheduler seams gated on `o.Sched != nil`; `RegisterObserverIdleWork` with the guarded
  `Persister` assertion registering `observer.persist` at priority 50. Zero `Handle(` occurrences
  (verified: `git grep -n 'Handle(' -- internal/daemon/observer_ops.go` → no matches, exit 1).
- `internal/cli/daemon.go` — the two wiring blocks (`WireObserver(&opts)` before `daemon.New` with
  Loud+continue on error; `RegisterObserverIdleWork(d, obsv)` after New succeeds; `&opts`, never
  `opts`), plus one deviation (see below).
- `internal/observer/session_test.go` (new) — every row of the brief's table.
- `internal/daemon/observer_ops_test.go` (new) — `TestWireObserver`,
  `TestWireObserver_SessionStartRefreshesStaleness`.
- `test/e2e/observer_e2e_test.go` (new) — the six e2e rows.

## RED evidence (tests first)

- observer group (session_test.go written before session.go):
  `go test ./internal/observer/ -run 'TestOnSessionStart|TestOnSessionEnd|TestState_' -count=1` →
  `session_test.go:471:55: undefined: stageGC / FAIL [build failed]` (the test-first constants).
- daemon group: `go test ./internal/daemon/ -run TestWireObserver -count=1` →
  `undefined: WireObserver / mapContractMode / RegisterObserverIdleWork / observerPersistPrio`,
  `FAIL [build failed]`.
- e2e: `go test ./test/e2e/ -run TestE2E_ObserverThroughDaemon -count=1 -timeout=30m` →
  `--- FAIL: TestE2E_ObserverThroughDaemon (33.39s): index/tool_use.jsonl never reached 44 lines
  (have 0)` — run against the unmodified production tree (only test files added).

## GREEN evidence

- `go test ./internal/observer/ -run 'TestOnSessionStart|TestOnSessionEnd|TestState_' -count=1` → ok (2.685s).
- `go test ./internal/daemon/ -run TestWireObserver -count=1` → ok (1.646s).
- `go test -count=1 ./internal/observer/... ./internal/daemon/...` → ok / ok / ok (observer 4.675s,
  observertest 2.131s — the behaviour rows `session_start_branches_on_source` and
  `session_end_flushes_without_reporting_an_error` now run against the real implementation and
  pass — daemon 6.020s).
- `go test -count=1 ./internal/cli/` → ok (2.141s) — runDaemon's in-process tests still green with
  the wiring in place.
- e2e, all six rows:
  - `TestE2E_ObserverThroughDaemon` PASS (3.79s)
  - `TestE2E_HooksExitZeroUnderFaultInjection` PASS (0.52s)
  - `TestE2E_SupersessionVisibleAfterRestart` PASS (2.43s)
  - `TestE2E_VerbatimPromptSurvivesRestart` PASS (1.97s)
  - `TestE2E_SubagentNameReachesTheDaemon` PASS (7.1s run, after one test-side fix — see
    Deviations item 3)
  - `TestE2E_ThinSliceDropsControlOnlyEdges` PASS (2.51s)

## Load-bearing proof

Deleted the two runDaemon calls (`daemon.WireObserver(&opts)` block and the
`RegisterObserverIdleWork` block), re-ran
`go test ./test/e2e/ -run TestE2E_ObserverThroughDaemon -count=1` →
`--- FAIL: index/tool_use.jsonl never reached 44 lines (have 0)` (33.64s). Restored both blocks;
`go build ./...` clean; the six e2e rows re-verified green in the full-suite run below.

## Race results

`go test -race -count=1 -timeout=30m ./internal/observer/... ./internal/daemon/...` →
ok observer (8.421s), ok observertest (2.930s), ok daemon (6.664s). No retries needed.

## Lint plan-gate: 3 → 0

Baseline (before this commit): `devtool lint` FAILED runpatterns with exactly 3 unsatisfiable
`-run` patterns, all naming `./internal/observer`:

1. `plans/V4-VERIFY-...md:358` — `TestOnSessionStart_CompactDelegates|TestOnSessionStart_CompactWithoutRehydratorIsEmpty|TestOnSessionStart_ClearDelegates`
2. `plans/V4-VERIFY-...md:360` — `TestOnSessionStart_StartupOpensSegment|TestOnSessionEnd_Order`
3. `plans/V6-VERIFY-...md:241` — `TestOnSessionStart|TestOnSessionEnd`

After this commit: `devtool lint` → `PASS runpatterns` (542 of 542 checkable resolved), PASS on
every sub-check, exit 0. `devtool fmt` clean (no rewrites), `devtool vet` clean.

## Gate confirmations (deliverable 7)

- `git grep -n 'Handle(' -- internal/daemon/observer_ops.go` → empty (exit 1).
- SP-05's pair, focused with the observer wired:
  `go test ./internal/daemon/ -run 'TestIngestACKPrecedesProcessing|TestMarkerIsWrittenByFlushAndCheckpointOnly' -count=1 -v`
  → `--- PASS: TestIngestACKPrecedesProcessing (0.01s)`, `--- PASS:
  TestMarkerIsWrittenByFlushAndCheckpointOnly (0.07s)`, ok.
- observertest behaviour rows, focused against the real implementation:
  `go test ./internal/observer/observertest/ -run 'TestObserverSuite_RealObserver/observer.New/behaviour/session_start_branches_on_source|...session_end_flushes_without_reporting_an_error' -count=1 -v`
  → both subtests RUN and the suite `--- PASS` (0.22s), ok.
- Import set: observer gained NO new imports (session.go uses core/dag/hookio/paths/sketch/store,
  all already in the realized set; negknow stays daemon-side in observer_ops.go). devtool lint's
  importgraph sub-check PASS.

## The devtool test incident (found by the gate, fixed, gate re-run)

The first full `go run ./tools/devtool test` failed exactly one test in the whole tree:
`test/guards` `TestV1_WriteSetConfinedAcrossFullHookSequence` — "WriteAtomic left staging files in
.qompack/tmp/: [d quarantine/]". Root cause: the new wiring makes every daemon start open the real
store, and SP-06's `store.Open` eagerly pre-creates `.qompack/tmp/quarantine/`
(internal/store/fsstore.go ensureStoreDirs) — an empty scaffolding directory the guard reads as
staging debris once the daemon is gone. The store's own `quarantine()` re-MkdirAlls that directory
at use (internal/store/objects.go), so the eager copy is redundant from the moment it exists.

Fix, kept inside this task's own file set (internal/cli/daemon.go): immediately after
`WireObserver` returns, runDaemon removes the freshly-created EMPTY directory with `os.Remove` —
which refuses a non-empty directory, so genuine quarantine evidence is never touched. Startup, not
shutdown, because the guard's "daemon has finished" signal is the lock release inside `Run`, which
precedes anything runDaemon could do afterwards. Neither internal/store nor test/guards was
edited. The guard then passes: focused re-run `ok github.com/qompack/qompack/test/guards 5.629s`.
The commit was amended (never pushed) to fold this in; fmt/vet/cli tests re-ran clean and
check-commit-msg re-verified exit 0 on the amended message.

## Remaining suite results

- `go test -count=1 -timeout=30m ./test/e2e/` (full package, incl. the six new rows and the
  fault matrix) → ok (92.816s) before the amend; e2e also re-ran green inside the devtool test
  gate (106.173s in the first gate run; the only failure in that run was the guard above).
- `go run ./tools/devtool test` → first run FAIL (the guard above, sole failure); re-run after the
  fix → PASS, exit 0 — every package ok, including `test/guards` (76.717s), `test/e2e` (107.901s)
  and `test/integration` (186.426s).
- `go run ./tools/devtool cover` → PASS, exit 0. Every floor met; the two packages this commit
  touches: `OK observer: 96.2% >= floor 75%`, `OK daemon: 82.5% >= floor 75%` (store 92.6%,
  dag 90.9%, ipc 84.8%, contract 84.2% — all unchanged-green).
- Focused SP-05 pair → PASS (recorded in Gate confirmations above)

## Deviations from the brief (all deliberate, none scope-changing)

1. **OnSessionEnd releases the session lock after step 4, not after step 6.** The brief's sketch
   holds `st.mu` through `persistState()` (step 5) and unlocks at step 7 — but `persistState`
   (landed in Commit 2, per the brief "Persist may already be complete; keep it") snapshots EVERY
   session under that session's own lock after taking `o.mu`, so holding the current session's
   lock into step 5 self-deadlocks on a non-reentrant mutex AND inverts resolved decision 9's lock
   order (never o.mu while a sessionState.mu is held). Steps 5–7 read nothing from `st`, so
   releasing after step 4 changes no observable ordering; the pinned call order
   (BuildSegment → Close → Graph.Flush → Store.Flush → sketches → state → GC → map delete) is
   unchanged and TestOnSessionEnd_Order pins it.
2. **runDaemon carries one block beyond the brief's two: a deferred `opts.Store.Close()` after
   `Run` returns.** The controller ruling says the store "lives for the process", which is exactly
   what happens in production — but `runDaemon` also runs IN-PROCESS in internal/cli's own tests
   (TestDaemon_ExitsZeroOnCleanStop and friends), where an unreleased append handle on
   index/*.jsonl makes the caller's t.TempDir cleanup fail on Windows. The close runs strictly
   after `d.Run` has returned (i.e., at process-exit time in production), so it changes nothing
   observable in the deployed binary; without it the cli test suite goes red on Windows. The
   observer_ops.go doc comment documents the lifetime as the ruling requires.
3. **TestE2E_SubagentNameReachesTheDaemon asserts via `store.Open` + `ToolUse`, not by
   json-decoding raw tool_use.jsonl lines.** First version decoded lines into
   `store.ToolUseRecord` and failed: the on-disk index lines use SHORT wire keys (`"sub":...`),
   not ToolUseRecord's long-key wire shape, so `Subagent` decoded empty while the file plainly
   carried `"sub":"code-reviewer"` (manual repro attached below). The fixed test waits schema-free
   on the raw line containing `code-reviewer`, then reads the record back through the store's own
   index over the same file — same property, no dependence on SP-06's private line schema.
   Repro line observed: `{"v":1,"id":"subagent_sess-dbg_0",...,"tool":"SubagentStop",...,
   "sub":"code-reviewer"}`.
4. **TestE2E_SupersessionVisibleAfterRestart reads the flip through `store.Open`+`ToolUse` (plus a
   schema-free wait on the raw `"op":"supersede"` line), not by expecting the FIRST record line to
   carry the superseded status.** SP-06's index is append-only: `MarkSuperseded` appends a
   `{"op":"supersede","id":...,"by":...}` line and the original record line is immutable, so the
   plan-row's literal reading (the first record's own status field flips in place) is not how the
   shipped store persists it. The property proven is the row's actual claim: after a full daemon
   restart the record answers StatusSuperseded with SupersededBy = the second read.
5. **TestState_AtomicWrite inspects concurrently from a reader goroutine rather than "via
   testutil".** session_test.go is an in-package test file, and §3.2 forbids an in-package test
   from importing internal/testutil (fakes_test.go documents the same constraint). The test pins
   the same property: 100 Persists under a hammering reader, zero torn/partial observations at the
   target path.
6. **`TestE2E_HooksExitZeroUnderFaultInjection` skips when running as root on POSIX** (WSL
   verification runs as root, where permission-bit read-only dirs are inert). CI runs non-root, so
   the row is exercised where it means something. On Windows the deny is a real icacls deny-ACE
   (same mechanism as internal/cli's spool-readonly site).
7. **Order test's step-4/step-5 relative order.** sketch.Save and persistState are direct
   package-function calls with no seam to interpose, so TestOnSessionEnd_Order pins their
   position by file-existence probes inside the recording fakes: at Store.Flush neither sketches
   nor state exist; at GC both do. Their relative order (4 before 5) is code order in
   onSessionEnd; every cross-collaborator boundary the plan row names is pinned exactly.

## Sketch-race check (the task's concurrency note)

Checked as instructed. `SketchSet.Save` takes `s.mu` and returns before touching any sketch unless
`s.dirty` is set; only `SketchSet.Write` sets dirty, and `git grep` shows ZERO production callers
of `SketchSet.Write` in the tree today — the observer feeds the raw pointers under its own
`sketchMu` (by design: that is what makes the observer own the sketch files' write). So
`handleFlush`/`Stop`/idle `Save` calls never read the sketch internals concurrently with observer
mutations, and the race suite is green over the wired daemon. LATENT hazard, reported as a
concern rather than fixed: the first future subplan that dirties the set through `SketchSet.Write`
(SP-09's Bloom writes go through negknow, but any future `Write` user) would make `Save`'s
`MarshalBinary` read the same CMS/HLL pointers the observer mutates under a DIFFERENT mutex — a
real data race. Closing it would need either the observer's feeds to go through `SketchSet.Write`
(impossible: observer cannot import daemon) or SP-05's Save to take the observer's lock (does not
exist across packages) — i.e., an arch-level seam decision, not a local edit. Nothing was changed
in SP-05's sketchset.go.

## Self-review notes

- Decision 7 (error policy) holds in session.go: only ctx.Err() escapes; every stage softed;
  OnSessionStart returns non-empty output only via the Rehydrator's verbatim return.
- Decision 11: one `o.now()` per entry point; no time.Now in internal/observer.
- No hand-assembled NodeIDs; BuildSegment is the only segment-node emitter; enrol unchanged.
- nomagic: no forbidden literals introduced (50/49/51 priorities, 8s gcDeadline pre-existing).
- Import discipline: observer's realized import set unchanged; daemon is a composition root
  (importrules.go) so observer/symbols/tokens imports in observer_ops.go are legal.
- Commit message verified with check-commit-msg and `git log -1 --format=%B` (no trailers).

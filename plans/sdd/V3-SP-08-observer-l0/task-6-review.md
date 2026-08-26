# Task 6 review — Commit 6: SessionStart/SessionEnd lifecycle and daemon wiring

Range reviewed: 7885650..f1c77534910f7ff2cb9498a2df7ebd6b25d4cec9 (one commit,
`feat(observer): SessionStart/SessionEnd lifecycle and daemon wiring`) on `feat/sp08-observer-l0`.
Files: internal/observer/session.go (+259), internal/observer/observer.go (stub bodies removed),
internal/daemon/observer_ops.go (+223), internal/cli/daemon.go (+36), internal/observer/session_test.go
(+647), internal/daemon/observer_ops_test.go (+175), test/e2e/observer_e2e_test.go (+436). No other
file touched — SP-05's daemon internals (daemon.go, options.go, handlers.go, sketchset.go) are
untouched by this diff, which is itself one of the brief's gates.

Verdict: **approved**. No critical or important findings; three minor observations below.

## Critical checks (each verified against the diff, not the report)

- **OnSessionEnd order.** `onSessionEnd` runs exactly: ctx check → one `o.now()` → `st.mu.Lock()`
  with no defer → step 1 (`dag.BuildSegment` with `Members: nil`, PrevID chain, `StartPos:
  st.SegStartPos`, `Tokens: PrefixTokens−SegStartPos`; then `Segments().Close` with the 5-key
  feature map) → `Graph.Flush` → `Store.Flush` → `saveSketches` → explicit `st.mu.Unlock()` →
  `persistState` → `Store.GC` → `o.mu.Lock(); delete; Unlock()` → `hookio.Empty(), nil`. Every
  stage is individually softed (`o.soft` no-ops on nil error — verified in observer.go:380).
  `TestOnSessionEnd_Order` pins the cross-collaborator order `[dag.BuildSegment, segments.close,
  graph.flush, store.flush, store.gc]` via recording fakes and pins steps 4–5 between Store.Flush
  and GC by file-existence probes inside the flush/GC fakes; step 7's map delete is asserted after
  return. The segment node's Pos/Tokens/Ref and the `segment:6 → segment:7` EdgeSequence chain edge
  are asserted.
- **Sanctioned deviation 1 (early lock release).** Verified the rationale against
  state.go at the pinned SHA: `persistState` copies the session map under `o.mu`, then
  `snapshotSession` takes **each session's own lock** — holding the current session's lock into
  step 5 would deadlock on the non-reentrant `sessionState.mu` and invert decision 9's order.
  After the release, nothing reads or writes `st` outside `snapshotSession`'s own lock: steps 5–7
  touch only `o.sess`/the file/GC. The brief's call order is intact and test-pinned. Correctly and
  safely implemented.
- **ensureSegment.** Adopt-open (`Current` ok && !Closed → `Segment`, `SegStartTurn` from the
  segment) vs open-new (`PrevSegment = st.Segment` first, `Open` with `StartTurn: st.Turn`,
  failure → soft + `st.Segment = 0`), and `SegStartPos = st.PrefixTokens` either way — the brief's
  text verbatim. Resume PrefixTokens: `o.session()` runs `o.once.Do(o.loadState)` (observer.go:409),
  so on the resume path PrefixTokens is rehydrated from state/observer.json before ensureSegment
  reads it. Frontier adoption takes `max(frontier, st.Turn)` with the soft stage `segment.frontier`.
- **compact/clear delegation.** `return o.opt.Rehydrate.OnCompact(ctx, e)` /
  `...OnClear(ctx, e)` — the Rehydrator's Output and error are returned verbatim, no rewrap.
  `TestOnSessionStart_CompactDelegates` asserts the verbatim Output; nil-Rehydrator branches
  return `hookio.Empty(), nil` with the Info line on compact only, per the brief.
- **RefreshStaleness placement.** `git grep negknow -- internal/observer` at the pinned SHA finds
  only comments (doc.go, session.go, sketches.go and test strings) — no import, no call. The call
  lives solely in observer_ops.go's wrapped `SessionStart` seam: nil-tolerant on both
  `s.Ledger` and `s.Store`, gated on `e.Source != "compact" && != "clear"`, error → `log.Warn`,
  never returned. Signature matches `negknow.Ledger.RefreshStaleness(ctx, store.Store) ([]string,
  error)` on this branch. `TestWireObserver_SessionStartRefreshesStaleness` covers exactly-one-call
  on startup, the compact/clear gate, error-is-not-a-hook-failure, and the nil-Ledger no-panic case.
- **Wiring shape.** Zero `Handle(` occurrences in observer_ops.go (read in full). Exactly one
  `o.Bind`. `s.ObservePrompt = obsv.OnUserPrompt` assigned directly — its Output reaches the host.
  ObserveTool/ObserveStop/SessionEnd wrapped to error-only, matching the Services signatures at the
  pinned SHA (options.go:134–138). `modeSrc` nil guard falls to `ModePassive`; `mapContractMode`
  sends both degraded states to Passive; `TestWireObserver` asserts the round-trip and that a fresh
  project reports ModeFull through a real daemon.
- **WireObserver open-when-nil ruling.** `store.Open(o.ProjectRoot, o.Cfg, store.Deps{Log,
  Metrics, Clock})`, `dag.Open(o.ProjectRoot, o.Cfg, log)`, `NewSketchSet(o.Cfg)` + `Load` — each
  only when the field is nil, each assigned back onto `*Options`. Verified `daemon.New` at the
  pinned SHA copies `o.Store/o.Graph/o.Sketches` into `Services` and that the monitor hoist
  (`svc.Mode = monitor.Mode` before the binds loop) is present, so the bind body captures a live
  Mode. `TestWireObserver` asserts instance identity with `require.Same` on all three, through a
  built daemon (catching the value-form Bind trap the brief names).
- **runDaemon diff.** Exactly the two mandated blocks (`WireObserver(&opts)` before `New` with
  Loud+continue; `RegisterObserverIdleWork(d, obsv)` guarded on `obsv != nil` after `New`), plus the
  two sanctioned extras (below) and the `path/filepath` import the Remove block needs. `&opts` is
  used, never `opts`. Bind is pointer-receiver over the unexported binds slice (options.go:101).
- **Sanctioned deviation 2 (deferred Close + startup Remove).** Close-race: every exit path of
  `daemon.Run` at the pinned SHA either calls `d.Stop` synchronously (ctx.Done, idle-exit,
  transport-failure arms) or blocks on `awaitStopCleanup` (admin.shutdown arm), and Stop's cleanup
  joins `runWG`, drains bounded, `ing.Wait()`s the worker pool — the goroutines that run observer
  work against the store — before Run returns. So the deferred `opts.Store.Close()` cannot ordinarily
  race in-flight work (residual: the `stopCleanupBound` timeout, minor finding 3). The `os.Remove`
  targets exactly `paths.Of(root).Tmp/quarantine` — the one dir `ensureStoreDirs` pre-creates at
  `store.Open` (fsstore.go:388, open.go:48) — `os.Remove` refuses a non-empty directory, and
  `quarantine()` re-`MkdirAll`s at use (objects.go:281). Safe, and it touches only that dir.
- **Persister / idle.** `RegisterObserverIdleWork` performs the single guarded assertion
  (`obsv.(observer.Persister)`, skip when false) and registers `"observer.persist"` at priority 50
  against `IdleController.Register(name, prio, fn)` — signature verified. The test brackets its
  position with marker tasks at 49/51. `Persist` (state.go) is loadState-free: persistState +
  Graph.Flush, ctx-checked.
- **Sketch ownership.** session.go step 4's comment carries the ownership note; `saveSketches`
  writes only `touch.cms`/`explore.hll` under `sketchMu`, never `tried.bloom`
  (`TestOnSessionEnd_NeverWritesTriedBloom` proves it against a real store). Verified
  `SketchSet.Save` at the pinned SHA is dirty-guarded and only `SketchSet.Write` sets dirty, while
  WireObserver hands the observer the raw `Touch/Explore/Top` pointers — so the observer's step-4
  write is the one that happens, exactly as the plan reasons. Sanctioned deviation 6: the latent
  Write/Save cross-mutex race is documented (observer_ops.go raw-pointer comment + report), not
  fixed, and nothing in sketchset.go was edited.
- **e2e rows.** All six drive the real binary (`Build(t)` + `Run` from the pre-existing harness —
  helpers verified to exist outside the new file) and assert the artifacts: 44 exact index lines,
  non-empty dag/deps.jsonl, the two sketch files present and tried.bloom absent; fault row exits 0
  throughout with observable failure records; supersession row asserts `StatusSuperseded` +
  `SupersededBy` after a full restart through a fresh `store.Open` (sanctioned deviation 3 — the
  asserted property is the plan row's actual claim, since the index is append-only with
  `op:supersede` lines); verbatim row asserts byte-exact prompt bytes with the three volatile
  substrings intact via `VerbatimPromptID(sess, 0)`; subagent row asserts `rec.Subagent ==
  "code-reviewer"` via the store's own index (sanctioned deviation 3, wire keys are short);
  thin-slice row asserts ≥1 `EdgeControlOnly` and `len(thin) < len(full)`. Root skip on POSIX
  (sanctioned deviation 4) is the guarded `runtime.GOOS != "windows" && os.Geteuid() == 0`.
- **Session tests.** Every row of the brief's table is present under its exact name (the three
  lint plan-gate `-run` patterns resolve, per the report's runpatterns 542/542 PASS).
  `TestState_AtomicWrite` uses the in-package reader goroutine (sanctioned deviation 5) and pins
  zero torn-content observations over 100 Persists. `TestState_ResumedPrevTurnStillGuardsTheCycle`
  pins the persisted-PrevTurn cycle guard. GCPolicy row pins 30/10/8s from config defaults.
- **Import set / hygiene.** session.go imports only core/dag/hookio/paths/sketch/store +
  filepath/context — all in the realized set; the import-set test (sketches_test.go) is untouched.
  No `time.Now` in internal/observer (grep: comments only). No hand-assembled NodeIDs
  (`dag.SegmentNode` used). All numeric literals are named constants; none from the nomagic
  forbidden sets. SP-05's ACK/marker tests are untouched by the diff and reported passing focused.

## Minor findings

1. **Fault-injection evidence source differs from the plan row's letter** (test/e2e/
   observer_e2e_test.go, `obsErrCounterTotal`). The row says "`LOUD.log` or the counter file
   records the failures"; the test instead sums `observer.err.*` from the live daemon's
   `op.status` snapshot. The property proven — the injected failures leave an observable record —
   is the row's substance, and the live counters are the same counters the daemon persists, but
   this third evidence channel is not in the sanctioned-deviation list. Plan-mandated letter,
   satisfied in spirit; noting for the record only.
2. **The pre-lock startup window now touches a live daemon's tree in the lock-held race**
   (internal/cli/daemon.go). `WireObserver` (store.Open + MkdirAll of tmp/quarantine) and the
   startup `os.Remove` now run before `AcquireLock` inside `daemon.Run`, so a second
   `qompack daemon` that will lose the lock briefly opens append handles on, and removes the empty
   quarantine dir of, a tree a live daemon owns. Verified the open path is write-benign (append-only
   opens, read-only replay — no truncation/rewrites), and `os.Remove` refuses a non-empty dir; the
   one narrow interleaving (loser removes the dir between the live daemon's `quarantine()` MkdirAll
   and its Rename) degrades the quarantine move to `quarantine()`'s existing delete-in-place
   fallback, which is Loud-logged and counted. Degradation-only, vanishingly narrow; acceptable.
3. **Residual Close-race window behind `stopCleanupBound`** (internal/cli/daemon.go deferred
   `opts.Store.Close()`). If an admin-shutdown Stop's cleanup wedges past 15s, `awaitStopCleanup`
   returns with cleanup still in flight (Loud-announced) and the deferred Close then runs under it.
   This is SP-05's pre-existing, deliberate last-resort bound on a process that is about to exit
   anyway; the ordinary paths are fully joined. Not attributable to this commit beyond noting the
   deferred Close inherits the window.

## Tests I would have run (not run: another agent may be editing the tree)

- `go test ./internal/observer/ -run 'TestOnSessionEnd_Order|TestState_' -count=1` and
  `go test ./internal/daemon/ -run TestWireObserver -count=1` to spot-confirm the report's GREEN
  claims. The report's RED evidence (build-failure on the test-first constants; the e2e 0/44 run
  against the unmodified tree) and the load-bearing-proof deletion run are internally consistent
  with the diff, so I did not consider a re-run necessary for the verdict.

## Strengths

Disciplined execution of an intricate brief: the lock-order deviation is correctly diagnosed and
correctly narrow; the wiring file is exactly the sanctioned Bind shape with unusually good
comments; the e2e rows genuinely exercise the whole binary path and their two schema-free rewrites
assert the plan rows' real properties rather than their literal spelling; and the quarantine-
scaffolding fix stayed inside the task's own file set instead of leaking into store or guards.

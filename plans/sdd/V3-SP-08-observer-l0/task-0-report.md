# Task 0 report — the `arch/sp08-observer-seams` amendment (Commit 0 of SP-08)

Branch: `arch/sp08-observer-seams` in `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08`,
cut from develop's tip `6ca496f`. One commit: **`bc257ee`** —
`fix(store): honour an empty Canon.Strip and a caller MinHash opt-out`.
`git rev-list --count develop..HEAD` = 1. Nothing pushed, nothing merged, no branch created or
deleted. (The commit was amended twice locally, both times before any push: once to fold in the
integration-test fix `ci-local` caught, once to record that fix in the message. Earlier SHAs
`3e0f822` and `bcbc57b` no longer exist on the branch.)

---

## 1. Controller ruling checked BEFORE implementing (d)

**Question:** is an `observe.prompt` request also appended to the WAL (`ingest.Accept`) and later
re-dispatched to the `ObservePrompt` seam by the drain?

**Answer: it is WAL'd, but the drain never re-dispatches it to the seam.** (d) was therefore safe to
implement, and was implemented.

Evidence — `d.svc.ObservePrompt` has exactly ONE production call site in the whole tree:

- `internal/daemon/handlers.go:458` — inside `callObservePromptWithDeadline`, reached only from
  `internal/daemon/handlers.go:445` (`handleObservePrompt`, the live reply path).
- `internal/daemon/daemon.go:320` registers `handleObservePrompt` for `ipc.OpObservePrompt`.

The drain path does NOT reach it:

- `internal/daemon/daemon.go:773-777` — `drainDispatch`: `case req.Op.HotPath(): d.runIngested(...)`.
  Every hot-path op (observe.tool / observe.prompt / observe.stop) bypasses `dispatchOp` and its
  route handlers entirely and deliberately, so a WAL-sourced line is not re-WAL'd.
- `internal/daemon/daemon.go:742-743` — `runIngested`'s `case ipc.OpObservePrompt:` body is exactly
  `d.scanSentinelForPrompt(ev)`. It never touches `d.svc.ObservePrompt`.
- `internal/daemon/ingest.go:283` and `internal/daemon/drain.go:226` both feed `runIngested`
  (through `resolveBlob`), not the route.

So under the pre-existing code, a prompt arriving while the contract was degraded was WAL'd and
sentinel-scanned, but the `ObservePrompt` seam — G2.3's verbatim capture — never ran, on the live
path or on any replay. §12.1 requires it to.

---

## 2. What I implemented

### (a) `store.PutOptions.Canon` can say "no optional classes" — `internal/store/put.go`

`canonOptions`' override test is now `if o.Canon.Strip != nil` instead of `len(o.Canon.Strip) > 0`,
so an empty non-nil slice means "no optional class" and a nil slice still means "use the store's
config". Inside that same gate, `!o.Canon.MinHash.Enabled` now forces `opts.MinHash.Enabled = false`.
The gate is the whole point: `Enabled` is a plain `bool` and `PutOptions.Canon` is a value field, so
an ungated rule would read every `store.PutOptions{}` in the tree as an opt-out, zero every
`PutResult.Signature` and silently retire `FSStore.nearDup`. The reverse direction stays config-wins
(a caller may turn the signature off, never on). `KeepDeltas` is still derived from `KeepRaw`.

`plans/00-ARCHITECTURE.md` §5.8 gains the matching `PutOptions` note. It was genuinely absent —
§5.8's `PutOptions` block carried no prose about `Canon` at all.

Blast-radius check: the only other `PutOptions{Canon: …}` call sites in the tree are
`test/integration/replaygrowth_test.go:260` and `test/integration/storepipeline_test.go:110`, both
of which pass `canon.OptionsFrom(cfg.Store.Canonicalize, …)`. That constructor always returns a
non-nil `Strip` AND `MinHash.Enabled` from config (true by default), so neither is read as an
opt-out and neither changes behaviour.

### (b) the subagent's name survives the IPC boundary

- `internal/cli/hookclient.go`: new `subagentExtras` struct (`{"subagent":true,"agent":"<name>"}`,
  with `agent` omitempty) plus `subagentNameKeys` / `subagentName`. `rawExtras`' `OpObserveStop`
  branch now marshals `subagentExtras{Subagent: true, Agent: subagentName(ev)}`. The name is
  resolved CLIENT-side, out of `hookio.Event.Extra`, where it is real — the first of
  `subagent_type`, `agent_name`, `agent` that `json.Unmarshal`s into a non-empty string. A numeric,
  object, empty or absent value falls through to the next key. With no name at all the marshalled
  bytes are exactly `{"subagent":true}` — byte-for-byte the pre-existing wire format, so
  `decodeSubagent` is untouched.
- `internal/daemon/handlers.go`: `resolveEvent` now restores `Event.Extra` from `req.Raw` through a
  new `restoredExtra` helper, when `req.Raw` decodes as a JSON object. A key the Event already
  carries wins, and the merge always allocates a fresh map — `resolveEvent` copies the Event by
  value, so writing into the copied map header would mutate a request the caller still holds.

### (c) the contract mode is reachable from a bound function

- `internal/daemon/options.go`: `Services` gains `Mode func() contract.Mode`, placed above the nine
  consumed seams with a doc comment naming it as the one PROVIDED seam (the inverse direction).
  Rule W-3 is satisfied: the struct is widened, nothing renamed or re-typed.
- `internal/daemon/daemon.go`: the `statePath` / `contract.NewMonitor` / `StandardAssertions()`
  register block is hoisted above the `for _, bind := range o.binds` loop, and
  `svc.Mode = monitor.Mode` is assigned before the loop runs.
- **`plans/00-ARCHITECTURE.md` §5.4 needed NO edit.** It already declares
  `Mode func() contract.Mode // SP-05 → SP-08` on `Services` and already carries the two-direction
  note plus the "New therefore constructs the monitor and assigns svc.Mode = monitor.Mode BEFORE it
  runs the bind loop" mandate (lines ~910-926). The code was the drifting party, exactly as the task
  ruling said. I verified the wording and changed nothing there.

### (d) degraded-passive stops silently dropping the verbatim prompt capture

`internal/daemon/handlers.go`'s `handleObservePrompt`: the seam call is no longer gated on
`mode.MayAct()`. It now runs under the route's existing `mode.MayRecord()` check (the same gate
`observe.tool` / `observe.stop` use), and `MayAct()` gates only whether the returned `hookio.Output`
reaches the reply — under `!MayAct()` the reply stays `hookio.Empty()`. The
`callObservePromptWithDeadline` wrapper is unchanged. Under `ModeOff` (`MayRecord()` false) the
route still returns before both the WAL append and the seam.

### Plan-copy confirmations (both checklist bullets) — NO plan edit was needed

- `plans/V3-SP-08-observer-l0.md:2647-2652` (Definition of Done) already reads "…contains no edit to
  `internal/store`, to `internal/daemon/handlers.go`, to `internal/daemon/options.go`, to
  `internal/daemon/daemon.go`, or to any file under `internal/cli` **except**
  `internal/cli/daemon.go`, whose `runDaemon` gains the Commit 6 observer wiring block and nothing
  else."
- `plans/V3-SP-08-observer-l0.md:2683-2687` (Done checklist file-map bullet) already says the same
  and names the amendment's five files.
- `plans/V3-SP-08-observer-l0.md:1838` / `:1843` already declare
  `func WireObserver(o *Options) (observer.Observer, error)` and
  `func RegisterObserverIdleWork(d Daemon, obsv observer.Observer)`, with the idle `Persist`
  registration in the second (`:1965-1968`). No `s *SessionRegistry` parameter and no `o.Idle()`
  call remain anywhere in the spec.

Amendment (d) needed no plan narrowing either: `internal/daemon/handlers.go` is already named in
both carve-outs.

---

## 3. TDD evidence

### RED

**(a) store** — `go test ./internal/store/ -run 'TestCanonOptions_EmptyStripMeansNoOptionalClasses' -count=1`

```
--- FAIL: TestCanonOptions_EmptyStripMeansNoOptionalClasses (0.06s)
    put_test.go:668:
        	Error:      	Not equal:
        	            	expected: 68
        	            	actual  : 39
        	Messages:   	an empty non-nil Canon.Strip must leave every optional class unapplied
FAIL	github.com/qompack/qompack/internal/store	1.892s
```

Expected: the empty non-nil `Strip` was flattened by `len(...) > 0`, so the store's six configured
classes ran and collapsed the 68-byte body (one ISO-8601 timestamp, two ANSI escapes) to 39 bytes.

**(b) cli** — `go test ./internal/cli/ -run 'TestRawExtras_ResolvesTheSubagentNameClientSide|TestHooks_SubagentNameReachesTheSpooledRequest' -count=1`

```
--- FAIL: TestRawExtras_ResolvesTheSubagentNameClientSide/subagent_type_wins
    expected: map[string]interface {}{"agent":"code-reviewer", "subagent":true}
    actual  : map[string]interface {}{"subagent":true}
--- FAIL: TestRawExtras_ResolvesTheSubagentNameClientSide/a_numeric_value_falls_through_to_the_next_key
    expected: map[string]interface {}{"agent":"explorer", "subagent":true}
    actual  : map[string]interface {}{"subagent":true}
--- FAIL: TestRawExtras_ResolvesTheSubagentNameClientSide/an_object_value_falls_through_to_the_next_key
    expected: map[string]interface {}{"agent":"planner", "subagent":true}
    actual  : map[string]interface {}{"subagent":true}
    … 6 of the 9 rows failed this way; the two "no name" rows passed, as they must
--- FAIL: TestHooks_SubagentNameReachesTheSpooledRequest (0.03s)
    expected: map[string]interface {}{"agent":"code-reviewer", "subagent":true}
    actual  : map[string]interface {}{"subagent":true}
FAIL	github.com/qompack/qompack/internal/cli	1.385s
```

Expected: `rawExtras` emitted a bare `{"subagent":true}` and never looked at `Extra` at all.

**(c) daemon, first RED — a compile failure**, which is the honest RED for a field that does not
exist yet:

```
internal\daemon\options_test.go:153:42: s.Mode undefined (type *Services has no field or method Mode)
FAIL	github.com/qompack/qompack/internal/daemon [build failed]
```

I then added ONLY the `Services.Mode` declaration (no assignment anywhere) and re-ran. That is the
behavioural RED for all three daemon tests at once:

```
--- FAIL: TestServicesModeIsAssignedBeforeBinds (0.00s)
        	Error:      	Expected value not to be nil.
        	Messages:   	the bind loop must run AFTER svc.Mode is assigned, or every bound seam captures nil
--- FAIL: TestResolveEvent_RestoresRawExtras (0.00s)
    --- FAIL: .../an_object_Raw_becomes_Extra:              "map[]" should have 2 item(s), but has 0
    --- FAIL: .../a_nil_Event_still_gets_the_restored_keys: "map[]" should have 1 item(s), but has 0
        	Error:      	Input ('') needs to be valid json.   (the pre-existing-Extra merge case)
--- FAIL: TestObserveStopSeamSeesTheRestoredAgentName (0.02s)
        	expected: "code-reviewer"
        	actual  : ""
        	Messages:   	the name the hook client resolved must survive the IPC boundary
FAIL	github.com/qompack/qompack/internal/daemon	1.393s
```

**(d) daemon** — `go test ./internal/daemon/ -run 'TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing|TestObservePrompt_ModeOffNeverInvokesTheSeam' -count=1`

```
--- FAIL: TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (0.63s)
    daemon_test.go:1585:
        	Error:      	Not equal:  expected: 1   actual: 0
        	Messages:   	degraded-passive still RECORDS: the ObservePrompt seam must run exactly once
FAIL	github.com/qompack/qompack/internal/daemon	2.493s
```

Expected: `mode.MayAct()` gated the call itself, so the seam never ran under degraded-passive. The
`hookSpecificOutput`-is-nil half of the same test already passed — which is precisely why the bug
was invisible. `TestObservePrompt_ModeOffNeverInvokesTheSeam` passed from the start; it is a guard
against over-correcting (d) into "always call the seam".

**ipc** — the extended `TestDecodeRequestRoundTrip` PASSED on its first run, before any production
change:

```
=== RUN   TestDecodeRequestRoundTrip
    frame_test.go:130: [rapid] OK, passed 100 tests (6.4101ms)
--- PASS: TestDecodeRequestRoundTrip (0.01s)
ok  	github.com/qompack/qompack/internal/ipc	1.729s
```

That is expected, and is recorded as such rather than dressed up as a driver: `ipc.Request.Raw`
already round-trips byte-for-byte through `EncodeRequest`/`DecodeRequest`. The added generator row
is a REGRESSION PIN — it fixes in place the one wire field the (b) fix now depends on, in the exact
`{"subagent":true,"agent":"<name>"}` shape, so a future `Raw` change that dropped a key would fail
here instead of silently renaming every subagent capture back to "subagent".

### GREEN

`go test -count=1 -run '<the seven new/extended tests>' ./internal/store/ ./internal/cli/ ./internal/daemon/ ./internal/ipc/`

```
ok  	github.com/qompack/qompack/internal/store	2.133s
ok  	github.com/qompack/qompack/internal/cli	1.565s
ok  	github.com/qompack/qompack/internal/daemon	1.538s
ok  	github.com/qompack/qompack/internal/ipc	1.833s
```

(d)'s own run, including the three pre-existing prompt tests and the pre-existing degraded-passive
test that all had to keep passing:

```
--- PASS: TestObservePromptRepliesWithinDeadline (0.03s)
--- PASS: TestObservePromptWALFailureIsObservable (0.03s)
--- PASS: TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (0.05s)
--- PASS: TestObservePrompt_ModeOffNeverInvokesTheSeam (0.08s)
--- PASS: TestDegradedPassiveSuppressesActingPaths (0.18s)
--- PASS: TestObservePromptBlockingVariantTimesOut (0.29s)
ok  	github.com/qompack/qompack/internal/daemon	1.794s
```

Full four-package run, `go test -count=1 -timeout=30m ./internal/store/ ./internal/cli/ ./internal/daemon/ ./internal/ipc/`, with the exit code read from the `go test` invocation itself and not through a pipe:

```
ok  	github.com/qompack/qompack/internal/store	139.950s
ok  	github.com/qompack/qompack/internal/cli	2.533s
ok  	github.com/qompack/qompack/internal/daemon	7.703s
--- FAIL: TestStateWriteIsAtomic (3.71s)
    state_test.go:132: rename …\.qompack\tmp\wa-2664570687 …\.qompack\run\state.bin: Access is denied.
FAIL	github.com/qompack/qompack/internal/ipc	17.146s
```

`TestStateWriteIsAtomic` is a PRE-EXISTING, load-sensitive Windows flake unrelated to this change.
It runs 500 `WriteState` calls each overlapped by two concurrent one-shot readers (1000 goroutines
opening the destination file) and the failure is a Windows rename losing to a concurrent open — in
`internal/ipc`, a package where this commit touched only `frame_test.go` and no production code at
all. It passes in isolation: `go test -count=3 -run 'TestStateWriteIsAtomic' ./internal/ipc/` →
`ok … 12.367s`, exit 0. It also passed inside `ci-local`'s own `test` stage below.

### Standalone tool runs

- `go run ./tools/devtool fmt` → exit 0, no output.
- `go run ./tools/devtool vet` → exit 0, no output.
- `go run ./tools/devtool lint` → exit 0; PASS on golangci-lint, nomagic, importgraph, testdeps,
  bindeps, sleepcheck, stubskips, runpatterns, docmarkers, coveragefloors.

---

## 4. `ci-local`

`go run ./tools/devtool ci-local`, run from the worktree root. It was run TWICE, and the first run
is reported honestly because it caught a real defect this task introduced.

### Run 1 — RED at the `test` stage

| stage | result |
| --- | --- |
| fmt-check | PASS |
| lint | PASS (all ten gates) |
| vet | PASS |
| build | PASS |
| test | **FAIL** |
| cover | not reached |
| plugin-validate | not reached |
| gen-config-docs | not reached |

```
ok  	github.com/qompack/qompack/test/guards	70.800s
--- FAIL: TestIntegration_DegradedPassiveStillWritesToTheRealStore (1.05s)
    contractmonitor_test.go:528:
        	Error:      	Should be false
        	Messages:   	ObservePrompt seam must not run while degraded
FAIL	github.com/qompack/qompack/test/integration	206.576s
devtool: ci-local: ci-local: step "test" failed: exit status 1
```

**This was caused by my change, and it is exactly the assertion (d) inverts.** A pre-existing
integration test bound an `ObservePrompt` seam returning
`AdditionalContext: "must-never-be-delivered"` and asserted both that no hook's Output carried a
`HookSpecificOutput` AND that the seam never ran at all. The first is the real §12.1 requirement and
still holds; the second is the bug (d) fixes, written down as a test.

Fix, in `test/integration/contractmonitor_test.go` (the one file outside the brief's list this task
touched — see §7 concern 5):

- `promptActed` renamed to `promptRan`, since under §12.1 running is what it must do — the other two
  flags stay `…Acted` because `SessionStart` and `PreCompact` are purely acting seams.
- `require.False(t, promptActed.Load(), "ObservePrompt seam must not run while degraded")` becomes
  `require.True(t, promptRan.Load(), "degraded-passive still records: the ObservePrompt seam must
  run, only its Output is suppressed")`.
- The test's own doc comment and the seam-binding comment now state the two-sided rule, and point at
  the `UserPromptSubmit` row of the existing no-`hookSpecificOutput` loop as the proof that the
  Output went nowhere. Nothing about the suppression half was weakened.

Re-run of that test alone: `go test ./test/integration/ -run
'TestIntegration_DegradedPassiveStillWritesToTheRealStore' -count=1 -timeout=30m` →
`ok  github.com/qompack/qompack/test/integration  2.202s`.

A tree-wide sweep for other tests pinning the old behaviour
(`grep -rn "ObservePrompt" test/ internal/ --include=*_test.go`) found no others: every remaining
hit is an op-name constant, a wire fixture, or the new daemon tests.

### Run 2 — GREEN, all eight stages

`CILOCAL_EXIT=0`.

| stage | result |
| --- | --- |
| fmt-check | PASS |
| lint | PASS — golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips, runpatterns, docmarkers, coveragefloors |
| vet | PASS |
| build | PASS |
| test | PASS — whole tree, `test/integration` included |
| cover | PASS |
| plugin-validate | PASS — `OK (10 file(s), 7 command(s), 7 hook event(s))` |
| gen-config-docs | PASS — `docs/config-reference.md is up to date` |

Coverage on the four packages this commit touches, from the `cover` stage:

```
internal/cli     32.590s  coverage: 82.9% of statements
internal/daemon  26.770s  coverage: 83.6% of statements
internal/ipc     48.118s  coverage: 84.8% of statements
internal/store  148.070s  coverage: 92.6% of statements
```

`internal/observer` is still listed by the cover gate as `exempt (stub, owned by SP-08)` — this
amendment adds nothing to it, as intended.

---

## 5. Files changed (12, all in commit `bc257ee`)

Production:

- `internal/store/put.go` — (a): `canonOptions`' override gate + the MinHash opt-out, and its doc.
- `internal/cli/hookclient.go` — (b): `subagentExtras`, `subagentNameKeys`, `subagentName`, and
  `rawExtras`' `OpObserveStop` branch.
- `internal/daemon/handlers.go` — (b): `resolveEvent` + the new `restoredExtra`. (d):
  `handleObservePrompt`'s two-gate split and its doc.
- `internal/daemon/options.go` — (c): `Services.Mode func() contract.Mode`.
- `internal/daemon/daemon.go` — (c): the monitor-construction hoist and `svc.Mode = monitor.Mode`.

Docs:

- `plans/00-ARCHITECTURE.md` — §5.8's `PutOptions` note (added; §5.4 needed nothing).

Tests:

- `internal/store/put_test.go` — `TestCanonOptions_EmptyStripMeansNoOptionalClasses`; `sketch` import.
- `internal/cli/hooks_test.go` — `TestRawExtras_ResolvesTheSubagentNameClientSide` (9-row table plus
  a byte-exactness assertion and two negative ones) and
  `TestHooks_SubagentNameReachesTheSpooledRequest`.
- `internal/daemon/daemon_test.go` — `TestResolveEvent_RestoresRawExtras` (7-row table plus the
  aliasing guard), `TestObserveStopSeamSeesTheRestoredAgentName`,
  `TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing`,
  `TestObservePrompt_ModeOffNeverInvokesTheSeam`.
- `internal/daemon/options_test.go` — `TestServicesModeIsAssignedBeforeBinds`.
- `internal/ipc/frame_test.go` — `TestDecodeRequestRoundTrip` now also draws a `Raw` carrying the
  agent name.
- `test/integration/contractmonitor_test.go` — the one assertion that pinned (d)'s old behaviour,
  inverted; see §4 run 1 and §7 concern 5. This is the only file outside the brief's list.

`Qompack.md` untouched. No sibling worktree touched.

---

## 6. Self-review

- **Completeness against (a)/(b)/(c)/(d).** Every bullet of the brief's pre-step is present: (a)'s
  three sub-bullets (the `!= nil` test, the gated MinHash opt-out, the §5.8 note) plus its named
  test; (b)'s two edits plus all three named tests; (c)'s three edits — with the §5.4 doc bullet
  discharged as "already correct, verified, unchanged" per the task ruling — plus its named test;
  (d)'s single edit plus its two tests.
- **Nothing extra in production.** Five production files, five behaviours. The one judgement call
  beyond the literal brief text is in (c): the brief says "hoist the two `statePath` /
  `contract.NewMonitor` lines", and I moved the three-line `contract.StandardAssertions()` register
  loop with them rather than stranding it below the bind loop. Same contiguous monitor-construction
  block, semantically identical, and splitting it would have left `monitor` declared far from its
  assertions.
- **Two tests beyond the named set**, both deliberate and both asserting production behaviour the
  named tests cannot reach. `TestHooks_SubagentNameReachesTheSpooledRequest` drives the real hook
  body end-to-end, which is the only way to prove `hookio.ReadEvent` actually files `subagent_type`
  into `Extra` — the table test builds `Extra` by hand and would pass happily over a key nothing
  ever populates. `TestObserveStopSeamSeesTheRestoredAgentName` asserts the restored `Extra` where
  it is spent, at a bound `Services.ObserveStop` seam, rather than at `resolveEvent`'s return.
- **Tests assert real behaviour, not mocks.** The store test runs the production canonicalizers and
  the production MinHash through `newTestStore` with no doubles injected. The daemon tests run a
  real `daemon.New` over a temp project with a real `contract.Monitor` and the real
  `dispatchOp` / `runIngested`. The cli end-to-end test runs the real `Dispatch` and decodes the
  real spooled frame off disk.
- **The gate is proven to be a gate.** The store test's second row is the one that would catch an
  ungated MinHash opt-out: its `MinHash.Enabled` is the same zero `false` as the first row's, and
  its `Signature` must still be non-zero. `TestPutBytes_NearDup` and
  `TestPutBytes_NoNearDupWhenMinHashDisabled` both still pass, so `FSStore.nearDup` is demonstrably
  alive rather than silently retired.
- **The (d) change cannot over-fire.** `TestObservePrompt_ModeOffNeverInvokesTheSeam` pins that
  `ModeOff` still returns before the seam and never creates the WAL file, and the pre-existing
  `TestDegradedPassiveSuppressesActingPaths` — whose bound `ObservePrompt` returns a non-empty
  `HookSpecificOutput` — still asserts a nil `HookSpecificOutput` in the reply.
- **Output pristine.** fmt, vet and all ten lint gates clean. No `TODO|TBD|FIXME|XXX`, no magic
  numbers, doc comments in the repository's voice.
- **Commit message.** Subject is 56 characters after `fix(store): `, inside the 64-character cap
  `tools/devtool/checkcommitmsg.go:21` enforces; every body line is ≤ 100 runes; the `Refs:` footer
  carries all five references including `§12.1 degraded-passive`. Validated with
  `go run ./tools/devtool check-commit-msg <file>` (exit 0) before committing, and re-read from
  `git log -1 --format=%B` after: a case-insensitive grep for
  `co-authored-by|signed-off-by|generated with|claude|🤖` returns 0 hits.

---

## 7. Concerns

1. **A blob-externalized request now pollutes `Extra` with the descriptor's keys.** `resolveEvent`
   restores `Extra` from any object-shaped `req.Raw`, and `ipc.Client`'s `externalize` replaces
   `Raw` with `{"blob":…,"bytes":…,"field":…,"x":<original>}` for an oversize request. The impact is
   confined and non-functional: `internal/daemon/blob.go`'s `resolveBlob` puts the ORIGINAL `Raw`
   back (`req.Raw = ref.X`) before `runIngested` runs, so the observer seams — the actual consumers
   of `Extra` — never see the descriptor. Only `handlers.go`'s reply-shaping routes could observe
   the three extra keys, and none of them reads `Extra`. I followed the brief's rule literally
   ("when `req.Raw` decodes as a JSON object") rather than inventing a blob-descriptor exclusion the
   brief does not authorize. Flagged so the controller can rule if it wants one added.
2. **(d) adds up to `promptReplyDeadline` (250 ms) of hot-path work under degraded-passive**, where
   the route previously returned immediately. That is the intended trade — §12.1 says L0/L1 keep
   running, and observe.tool / observe.stop already do their full work in that mode — and the
   deadline wrapper is unchanged, so the hook still replies inside budget. Worth naming because
   degraded-passive was previously the cheapest prompt path in the daemon and is now the same cost
   as `ModeFull`.
3. **`TestStateWriteIsAtomic` (`internal/ipc`) is a real, pre-existing Windows flake** under
   concurrent load, not caused by this commit (which changes no `internal/ipc` production code). It
   passed in isolation ×3 and inside `ci-local`. It is a latent CI-flake risk for whoever next runs
   the full suite under co-load.
4. **The ipc round-trip extension was green before the implementation.** It is a pin, not a driver.
   Recorded plainly in §3 rather than presented as TDD evidence it is not.
5. **I edited one file the brief did not name as mine: `test/integration/contractmonitor_test.go`.**
   Global constraints say not to, and I want the controller to see the reasoning rather than find it
   in a diff. Amendment (d) — which the controller ruled in mid-task — inverts a behaviour that this
   pre-existing test asserted directly (`"ObservePrompt seam must not run while degraded"`), so
   `ci-local` could not go green with the test as written. The alternatives were to abandon (d) or
   to hand back a red CI, both worse. The edit is the minimum: one `require.False` → `require.True`,
   one variable rename for accuracy, and two comments; the suppression assertion the test really
   guards (no `hookSpecificOutput` on any hook) is untouched and still passes. Flagging it as a
   scope deviation for the record.
6. **One stale comment left deliberately unfixed.** `test/e2e/daemon_e2e_test.go:178` names
   `acceptHotPathEvent (internal/daemon/handlers.go:336)` and `handleObservePrompt (handlers.go:369)`
   by line number; my edits shifted those functions to 378 and 416. The references were already
   approximate before this commit (they pointed at the doc-comment starts, 343 and 372), the test
   passes, and no lint gate checks them — so I left the file alone rather than widen the diff into a
   second unnamed file. Worth a one-line follow-up whenever `test/e2e` is next opened.

# Task 2 report — Commit 2: PostToolUse pipeline

**Status:** COMPLETE (with two out-of-scope breakages to hand on — see Concerns).
**Commit:** `ba477c5` `feat(observer): PostToolUse pipeline: store, index, sketches, DAG emission`
**Branch:** `feat/sp08-observer-l0` (worktree `qompack-sp08`), one commit, working tree clean.
**Files touched:** `internal/observer/**` only (16 files; verified with `git show --name-only`).

---

## 1. What landed

### Implementation

| File | Contents |
|---|---|
| `observer.go` | `Mode`/`ModeFull`/`ModePassive`, `SymbolLister`, `Rehydrator`, `FeatureSample`, `Persister`; `Options` **widened** (Touch/Explore/Hot/Symbols/Rehydrate/Mode/OnSignals/OnFeatures added; nothing renamed, Rule W-3); the §6.6/§8.1 constant block; the metric-name constants; `recentEvent`, `toolUseLite`, `sessionState`, the `observer` type; `New`, `maxResultBytes`, `mode`, `now`, `soft`, `count`, `timed`, `session`; minimal `OnUserPrompt`/`OnStop`/`OnSessionStart`/`OnSessionEnd`. `stubObserver` removed. |
| `state.go` | `stateVersion`, `persistedState`/`persistedSession`/`persistedToolUse`, `stateFilePath`, `loadState`, `setAsideCorruptState`, `rehydrate`, `persistState`, `snapshotSession`, `Persist`. |
| `tooluse.go` | `OnToolUse` (histogram wrapper) → `onToolUse` (the 14 steps verbatim), `canonOptions`, `rememberToolUse`, `normalizedPaths`. |
| `graph.go` | `emitToolGraph` (one `dag.BuildToolUse` call + the supersedes tail + `enrol` ×2 + the `LastToolUse*` update), `symbolNames`, `enrol`, `advancePos`, `edgeWeight`. |
| `sketches.go` | `feedSketches` and the Bloom prohibition, stated in the file comment. |
| `features.go` | `recordRecent`, `features`, `pathSet`, `toolDistribution`, `concatText`, `jaccard`, `totalVariation`, `cosineTokens`, `termFrequencies`, `newlyCompletedTodos`. |
| `prompt.go` | `collectThrash` and `pendingThrashLines` **only**, plus the file comment. `OnUserPrompt` and the verbatim capture are Commit 4's. |

The 14 steps are implemented in the brief's order, with its lock shape
(`session()` takes and releases `o.mu`, the caller then takes `st.mu`), `Pos` pre-increment
(`advancePos(st, 0)` then `advancePos(st, rec.Tokens)`), `st.LastTS = now` assigned **after**
`features()`, and step 8 leaving `superseded` nil under a comment naming `supersede.go`.

### Tests

`fakes_test.go` (local `core.Clock` double — §3.2 forbids an in-package test file importing the
`testutil` composition root — `fakeStore`, `fakeGraph` with faithful `Out`/`In`, `fakeSymbols`,
`fakeGrammar`, the `harness`), `tooluse_test.go`, `graph_test.go`, `sketches_test.go`,
`features_test.go`, plus `observer_test.go`, `state_test.go` and `prompt_test.go` (see §3).
Every row from the brief's four tables is present; the adaptations are listed in §3.

### observertest

`internal/observer/observertest/suite_test.go` now runs **two** suites: `fake-stub` (which still
skips `/behaviour` with the exact Rule W-1 string, so "the block passed" stays falsifiable) and
`observer.New`, wired to a real `store.Open` + `dag.Open` on `t.TempDir()`. The
previous `observer-new-stub` factory had to become the real one: it built `observer.New` with no
Store or Graph, which the real `New` now rejects. `behaviour.go` and `suite.go` are untouched.

---

## 2. TDD evidence

**RED** — the five assigned test files were written first; with the seven implementation files
moved aside and `observer.go` reverted to SP-01's stub, `go test ./internal/observer/ -count=1`:

```
internal\observer\fakes_test.go:363:10: undefined: FeatureSample
internal\observer\fakes_test.go:369:7: undefined: observer
internal\observer\fakes_test.go:413:3: unknown field Touch in struct literal of type Options
internal\observer\fakes_test.go:414:3: unknown field Explore in struct literal of type Options
internal\observer\fakes_test.go:415:3: unknown field Hot in struct literal of type Options
internal\observer\fakes_test.go:419:3: unknown field OnSignals in struct literal of type Options
internal\observer\fakes_test.go:424:40: undefined: FeatureSample
internal\observer\fakes_test.go:460:44: undefined: sessionState
internal\observer\features_test.go:15:28: undefined: sessionState
internal\observer\tooluse_test.go:387:34: undefined: observer
FAIL	github.com/qompack/qompack/internal/observer [build failed]
```

One behavioural RED after the code compiled, worth recording because it was a real finding rather
than a typo: `TestGraph_ObserverOutputIsAcyclic` failed (`expected: 475, actual: 16`). The fixture
read a file and later wrote it, which closes a legitimate loop through the `file:` node — a real
property of §8.1 item 4's shared-state modelling, and the shape `dag.TestBuilderOutputIsAcyclic`
excludes for exactly the same reason. The fixture now gives each file its single writer at first
touch and only readers afterwards, leaving the D-7 chain as the only thing that could close a cycle.

**GREEN**

```
go run ./tools/devtool fmt                     exit 0, no files rewritten
go run ./tools/devtool lint                    PASS golangci-lint, nomagic, importgraph, testdeps,
                                               bindeps, sleepcheck, stubskips, runpatterns,
                                               docmarkers, coveragefloors
go run ./tools/devtool vet                     exit 0
go test -count=1 ./internal/observer/...       ok internal/observer 2.995s
                                               ok internal/observer/observertest 1.832s
go test -race -count=1 -timeout=30m ./internal/observer/...
                                               ok internal/observer 6.096s
                                               ok internal/observer/observertest 2.511s
go test -count=1 -cover ./internal/observer/...
                                               internal/observer 93.3%  (floor 75%)
                                               internal/observer/observertest 96.0%
```

**Behaviour suite** — all eight cases run and pass against `observer.New`:

```
--- PASS: TestObserverSuite_RealObserver/observer.New/shape
--- PASS: .../behaviour/post_tool_use_never_blocks_the_tool_call
--- PASS: .../behaviour/user_prompt_capture_never_blocks_the_prompt
--- PASS: .../behaviour/session_start_branches_on_source
--- PASS: .../behaviour/stop_and_subagent_stop_are_both_accepted
--- PASS: .../behaviour/session_end_flushes_without_reporting_an_error
--- PASS: .../behaviour/every_entry_point_tolerates_a_malformed_event   (5 subcases)
--- PASS: .../behaviour/replaying_one_event_twice_is_not_an_error
--- PASS: .../behaviour/extract_signals_detects_todo_test_and_git       (7 subcases)
--- SKIP: TestObserverSuite_ShapePassesAgainstStub/fake-stub  (Rule W-1 message intact)
```

**The race run found a real defect**, not a test artefact: `TestOnToolUse_ConcurrentSessionsRaceFree`
reported a `DATA RACE` in `sketch.(*CMS).Add` from `feedSketches`. See §3.

---

## 3. Deviations and mechanical adaptations

### Substantive (two)

1. **`observer.sketchMu` — resolved decision 9's premise is false for `internal/sketch`.**
   Decision 9 says store, DAG, grammar and sketch calls may be made under the session lock because
   "SP-06/SP-07/SP-03 own their own internal synchronization". `internal/sketch/doc.go` says the
   opposite in as many words: *"No mutable type here is safe for concurrent use: Bloom, CMS, HLL,
   MisraGries and SigSketch all require external synchronisation, and the daemon (SP-05) owns every
   live sketch behind its session-registry mutex."* The three sketches are **per-project**, shared by
   every session, so the per-session lock cannot serialize them, and `go test -race` proved it. The
   observer is handed the pointers directly and decision 9 makes race-freedom a hard requirement, so
   `feedSketches` now takes a package-local `o.sketchMu`. It is taken only inside a `sessionState.mu`
   and never around `o.mu`, so it adds no new lock order, and it changes no signature in any package.
   *If the intent is that the daemon serializes hook dispatch for the whole process, this mutex is
   redundant but harmless; if not, it is required. Either way SP-11's wiring should be checked.*

2. **`sessionState.TodoTransitioned` — a field the brief's struct does not list.**
   §6.6's TodoTransition is "1.0 when the newest `recentEvent.Tool == "TodoWrite"` **and**
   `newlyCompletedTodos` fired for it". `recentEvent` has no such field, `recordRecent`'s signature is
   fixed by the brief, and `newlyCompletedTodos` is not idempotent (it records into `TodoDone`), so
   `features()` cannot re-derive the answer. One unexported per-event bool on `sessionState`, set at
   step 12 and read at step 13, is the smallest thing that works. It is deliberately **not** persisted
   — it describes one event, not the session.

### Mechanical

- **`obs` spellings.** `Registry.Counter(name)` returns `obs.Counter` whose increment method is
  `Add(n int64)`, so `count` is `…Counter(name).Add(1)`. `obs.Timed(h Histogram, f func() error) error`
  exists exactly as the brief assumes. The brief says `count` is the single place naming an `obs`
  method; there are now **two**: `count` (the only counter site) and `timed` (the only histogram
  site, since `Metrics.Hist(name)` is also a method). Both are adjacent in `observer.go`.
- **`paths.WriteAtomic` does not create the destination directory**, so `persistState` runs
  `os.MkdirAll(filepath.Dir(stateFile), 0o700)` first. Perm `0o600`, matching every other state
  writer in the repo.
- **`emitToolGraph` opens with `if ctx.Err() != nil { return }`.** The brief's pseudo-code does not
  have it; its *Failure modes* paragraph does ("a cancelled context short-circuits at the next stage
  boundary"). It also keeps `ctx` genuinely used, which is what a plain unused parameter would not.
- **Two counter bumps were written and then removed** to stay strictly inside the specified
  algorithm: `observer.neardup` on `res.NearDup != nil`, and `observer.superseded` in the
  supersedes-tail loop. Both metric names are declared in `observer.go` with a comment saying the
  supersession and SubagentStop paths (Commits 3 and 5) own the increments. **Commit 3 should claim
  them.**

### Test-row adaptations (each carries the reason in the test's own comment)

| Row | Adaptation |
|---|---|
| `TestOnToolUse_IndexFailureStillFeedsSketchesAndDAG` | "DAG nodes == 2" is unsatisfiable — `BuildToolUse` also mints the assistant node (and a file node when there is a path), so the minimum is 3. Asserts instead that both `dag.ToolUseNode` and `dag.ToolResultNode` are present, alongside the specified `Touch.Total() == 2` and `observer.err.index == 1`. |
| `TestOnToolUse_LastTSIsPreviousEventTS`, `TestFeatures_GapSecondsFromFakeClock` | `features()` reports nothing until `2*featureWindow` events are in hand, so the 45 s gap is placed between the 15th and 16th rather than between two events in isolation. |
| `TestOnToolUse_ToolUseRingEvictsAndClampsSubagentSince` | `OnUserPrompt`/`OnStop` do not exist yet, so `st.SubagentSince` is seeded directly after five calls (what a prompt would leave) and the "slices without panicking" half is asserted as `st.ToolUses[st.SubagentSince:]`. |
| `TestGraph_SequenceChainAcrossTurns`, `…IsSequenceWhenAFileIsShared`, `TestGraph_ObserverOutputIsAcyclic` | The turn is advanced through a `setTurn` helper where the prompt/stop path will advance it. |
| `TestGraph_ObserverOutputIsAcyclic` | Every file gets its single writer at first touch, mirroring `dag.TestBuilderOutputIsAcyclic`'s own documented exclusion (read-then-write closes a legitimate loop through the file node). |
| `TestGraph_SegmentNodeAndChainEdgeAtClose` | `session.go`'s close path is Commit 6's, so this pins the `dag.SegmentSpec` shape the three `sessionState.Seg*` fields exist to supply — which is the half SP-08 owns today. |
| `TestGraph_IDsComeFromDagConstructors` | The prompt and segment **nodes** are not emitted yet; the row asserts every node id and edge endpoint the observer *did* emit against its constructor, plus the two no-session-component contracts (`assistant:3`, `userprompt:3`) directly. |
| `TestGraph_SymbolEdges` | Node/edge existence and orientation against the real graph; **emission order** against `fakeGraph`, because `dag.NodesAfter` is Pos-ordered, not insertion-ordered, and order is a call-order property. |
| `TestObserverNeverFeedsBloom` | There is no `sketch.Bloom` seam to replace with a panicking double — neither `Options`, nor `store.Store`, nor `store.Deps` carries one. The absence is the stronger assertion, so the test reflects over every `Options` field for a Bloom type **and** drives a 50-event session through a store double whose every unimplemented method is a nil embedded interface that would panic. |
| `TestOnToolUse_OversizePayloadTruncated` | A 4 MiB payload exceeds `signals.go`'s own 4 MiB `maxScanBytes` first, so `responseText` returns the raw prefix and the observer then applies the configured 1 MiB cap. The test asserts exactly 1 MiB reaches `PutBytes` and says so in a comment. |

### Test files not named in the brief's checklist

`observer_test.go`, `state_test.go` and `prompt_test.go`. `state.go` and `prompt.go` land in this
commit but the brief pins their rows to `session_test.go`/`prompt.go`'s later commits, and without
them coverage sat at **72.0%**, below the ≥75% floor. They are placed beside the files they test so
they do not pre-empt `session_test.go`. Coverage is now 93.3%.

---

## 4. Benchmarks

`go test -run '^$' -bench BenchmarkOnToolUse -benchtime 200x -count=5 -timeout=30m ./internal/observer/`
on a quiet machine (Intel Core Ultra 7 155H, windows/amd64), real `store.Open` + `dag.Open` on
`b.TempDir()`. Go reports a mean, so `reportBudget` reads p50/p99 back out of the observer's own
`observer.tooluse` histogram — the same instrument budget B-C is evaluated against in production.

```
BenchmarkOnToolUse_FileRead64KB-22      200   10122763 ns/op   10.24 p50-ms   15.36 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   10625664 ns/op   11.26 p50-ms   15.36 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   10108529 ns/op   10.24 p50-ms   14.34 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200    9770942 ns/op   10.24 p50-ms   13.31 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   10093392 ns/op   10.24 p50-ms   18.43 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200   34300888 ns/op   36.86 p50-ms   49.15 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200   34216906 ns/op   36.86 p50-ms   40.96 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200   34098842 ns/op   36.86 p50-ms   40.96 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200   34330950 ns/op   36.86 p50-ms   45.06 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200   34112034 ns/op   36.86 p50-ms   40.96 p99-ms
```

Against **B-C (`l0_process`, p99 < 50 ms, soft, `Gated: false`)**:

- `FileRead64KB` — mean 9.77–10.63 ms, p50 10.24–11.26 ms, p99 13.31–18.43 ms. **Comfortably inside.**
- `TestOutput256KB` — mean 34.10–34.33 ms, p50 36.86 ms, p99 40.96–49.15 ms. **Inside, but with only
  2–18% headroom**, and it **breached** under co-load: an earlier run with other work on the machine
  gave p50 45–53 ms and p99 53–131 ms.

Where the 256 KB time goes (throwaway diagnostic benchmarks, since deleted; means over 100×):

```
full config                                  54–61 ms/op
store.canonicalize.minhash.enabled = false   38–52 ms/op     (MinHash ≈ 10–15 ms)
store.canonicalize.enabled = false           26–33 ms/op     (canon+MinHash ≈ 25–30 ms)
ExtractSignals + ExtractTestOutcome alone     4.9 ms/op
responseText alone                            2.4 ms/op
```

So the dominant cost is SP-04's canonicalization and SP-03's MinHash inside `store.PutBytes` on a
256 KB payload, driven by the config defaults (128 permutations), not observer bookkeeping. The
observer's own share is the ~7 ms of repeated `responseText` decoding: the payload is unwrapped
four times per event (step 3, step 11's `ExtractTestOutcome`, and `ExtractSignals` → both
`ExtractTestOutcome` and `isGitCommit`). That is the plan's own step ordering over §5.21's pure
functions, so it was not changed here — but it is the cheapest available saving if B-C ever needs to
be defended (see Concerns).

---

## 5. Self-review against the brief

- **14 steps** — present in order; step numbers appear as comments. Step 5's failure returns
  `hookio.Empty(), nil` after `soft("put")` with no index record; steps 7 and 8 are skipped when
  `empty`; steps 6 and 9–14 always run. `TestOnToolUse_EmptyResponseStillIndexed` pins that.
- **Lock shape** — `session()` is the only accessor, takes/releases `o.mu`, and never returns while
  holding it. `persistState` copies the map under `o.mu`, releases it, snapshots each session under
  that session's own lock, and re-takes `o.mu` only for the write. No path holds both.
- **`store.ArgsDigest`** — called once, never re-derived; no local preview table, no `argsPreviewMax`.
  `TestOnToolUse_ArgsDigestAndPreview` asserts against the function's own return, and
  `…IsKeyOrderInvariant` mirrors `store.TestArgsDigest_KeyOrderInvariant` at the production call site.
- **Node ids** — `grep -E '"(tooluse|toolresult|assistant|userprompt|file|symbol|segment):'` over the
  non-test sources returns nothing; every id comes from a `dag.*Node` constructor.
- **Item 4 through the builder** — `emitToolGraph` makes exactly one `dag.BuildToolUse` call and adds
  edges directly only for the supersedes tail and segment membership.
- **Import set** — `TestObserverImportSetIsExact` parses every non-test file and requires exactly
  `{canon config core dag grammar hookio logging obs paths sketch store tokens}`.
- **Bloom** — `TestObserverSourceHasNoBloomReference` walks every non-test AST for the identifiers.
- **Time** — `grep -rn "time.Now()" internal/observer/` returns nothing; `now()` is the only `Clock`
  call site.
- **Placeholders** — `grep -rnE "TODO|TBD|FIXME|XXX|not implemented"` over the sources returns
  nothing; `core.ErrNotImplemented` no longer appears anywhere in the package.
- **nomagic** — every constant is named with a reason; no `//nomagic:allow` was needed (no forbidden
  literal is written), and `maxResultBytes` falls back to `config.Defaults()` rather than to `1<<20`.
- **Commit** — subject exact, no trailing period, 58 chars after the prefix; body lines ≤ 100 runes;
  footer exactly `Refs: SP-08, §8.1 items 1,4,5, §8.2, §5.21`; `devtool check-commit-msg` passes; no
  attribution trailer or emoji (`git log -1 --format=%B` verified).

---

## 6. Concerns

1. **`test/guards/stubs_test.go` now FAILS, and it is outside my write scope.**
   `TestAllStubsReturnNotImplemented/observer` builds `observer.New(observer.Options{ProjectRoot: …,
   Cfg: …})` and `require.NoError`s it; the real `New` reports `observer: New: Store is required`.
   The fix is a `test/guards` edit — give the row a full `Options` and mark it
   `pureMethods: allMethodsAreReal` — which the brief forbade me to make. **Someone must land it
   before the branch merges.**
   ```
   --- FAIL: TestAllStubsReturnNotImplemented/observer
       stubs_test.go:147: Received unexpected error: observer: New: Store is required
   ```

2. **`go run ./tools/devtool cover` (and therefore `ci-local` and the `cover` CI job) will now FAIL
   for the same class of reason.** `tools/devtool/cover.go`'s `landedSubplans` does not list
   `SP-08`, and its mirror check fires when an exempt package's OWNERS probe stops looking like a
   bare stub — which `OnToolUse` just did. The intended remedy is in the error text itself: add
   `"SP-08": true` to `landedSubplans` so `internal/observer`'s 75% floor is enforced. Coverage is
   already 93.3%, so the floor passes the moment it is switched on. Also outside my write scope.

3. **`internal/sketch` is not goroutine-safe and resolved decision 9 says it is.** See §3 item 1.
   The mutex I added closes it locally, but the plan text is wrong and SP-11's daemon wiring should
   be checked: if the daemon does *not* serialize hook dispatch, any other holder of those sketch
   pointers has the same problem.

4. **`BenchmarkOnToolUse_TestOutput256KB` has almost no headroom against B-C.** It passes on a quiet
   machine (p99 40.96–49.15 ms against 50 ms) and breaches under co-load. B-C is soft and ungated, so
   this is not a merge blocker, but it is the number that will move first when a canonicalization
   rule is added — which is precisely what carried defect **SP04-D5** warns about, and this commit
   adds none. The cheapest observer-side saving, if it is ever needed, is to unwrap the tool response
   once per event instead of four times; that means either caching inside `signals.go` or hoisting
   the decoded body into the pure functions' callers, and both change §5.21's pure-function shape, so
   it is a plan question rather than a local edit.

5. **`observer.superseded` and `observer.neardup` are declared but never incremented.** Commit 3
   should claim both. See §3, *Mechanical*.

6. **Four entry points are deliberately minimal.** `OnUserPrompt`, `OnStop`, `OnSessionStart` and
   `OnSessionEnd` do the ctx check, record their histogram, and return `hookio.Empty(), nil`. They
   live in `observer.go` rather than in `prompt.go`/`stop.go`/`session.go`, because the brief scopes
   `prompt.go` to `collectThrash`/`pendingThrashLines` this commit and does not create the other two
   files. Commits 4–6 should move them out as they land. Each carries a one-line comment naming its
   commit.

7. **`Options` is nil-tolerant but `New` is not `Log`-tolerant.** The brief's decision 8 lists `Log`
   among the required five while `Options`' SP-01 doc comment said "a nil Log must be treated as
   `logging.Nop`". I followed decision 8 (error on nil) and updated the field comment accordingly.
   Consumers that relied on the nil-Log leniency — none exist in the tree today — would break.

---

# Appendix — fix commit for concerns 1 and 2

**Status:** COMPLETE. Both gates fixed; one PRE-EXISTING failure elsewhere in the tree found and
left alone (see A.5).
**Commit:** `2e13d39` `chore(devtool,guards): mark the observer landed for the stub and cover gates`
(on top of `ba477c5`; `ba477c5` was not amended).

## A.1 What changed

| File | Change |
|---|---|
| `tools/devtool/cover.go` | `"SP-08": true` added to `landedSubplans`, with a four-line note saying commit `ba477c5` made `internal/observer`'s OWNERS probe real and that the exempt-but-real cross-check is what forces the flag onto SP-08's own branch rather than onto a merge commit. |
| `tools/devtool/cover_test.go` | `TestLandedSubplansMatchesTheBranch`'s tripwire loop is now `{"SP-09"}`; its comment says wave 2 is cut, SP-08 left the list in `ba477c5` for the reason above, and SP-09 has not landed. The error text no longer claims "wave 2 has not been cut yet". |
| `test/guards/stubs_test.go` | The `observer` registry row builds a real `store.Open` + `dag.Open` on `t.TempDir()` through a new `observerOptions(t)` helper and is marked `pureMethods: allMethodsAreReal`, matching the shape `store`, `dag` and `sketch` already use. The old assertion (New succeeds on `ProjectRoot`+`Cfg` alone) is **restated, not dropped**, as `TestObserverNewRequiresItsCollaborators`: New succeeds with the five mandatory collaborators and errors for each of `ProjectRoot`/`Store`/`Graph`/`Log`/`Clock` missing. No other package's guard is touched. |

## A.2 Two further mirrors the change forced

Neither was named in the ruling; both are the same fact read from a second place, and both failed
on the first run of the required commands.

1. `test/guards/nightlyfuzz_test.go` — `nightlyFuzzLandedSubplans` is a hand transcription of
   `cover.go`'s map, and `TestNightlyFuzz_LandedSubplansMirrorsCoverGo` parses `cover.go` and
   `require.Equal`s the two. `"SP-08": true` added. **No side effect on the fuzz matrix:**
   `.github/workflows/nightly.yml` declares no target in `internal/observer`, so no waiver was
   revoked. Verbatim failure before the fix:
   `tools/devtool/cover.go's landedSubplans and this file's transcription of it disagree.`
2. `tools/devtool/planchecks_test.go` — `planDocsInScope` derives wave scope from `landedSubplans`,
   and `TestPlanDocsInScope_TracksLandedSubplans`'s fixture listed `V3-SP-08` as wave 3's only
   subplan, so wave 3 derived as landed and its "want out of scope" half inverted. The fixture now
   names `plans/V3-SP-09-negative-knowledge.md` as well, which is the true reason wave 3 is still
   out of scope. Its sibling `TestPlanDocsInScope_FollowsLandedSubplansWithoutASecondList` used
   `defer delete(landedSubplans, sp)`, which would now **remove** a genuinely-landed SP-08 for
   every test that ran after it; it saves and restores instead. Verbatim failures before the fix:
   `plans/V3-SP-08-observer-l0.md: want out of scope (SP-08 and later have not landed)` and the
   same line for `plans/V3-VERIFY-observer-and-negative-knowledge.md`.

## A.3 Required commands

```
go test -count=1 -timeout=30m ./test/guards/ ./tools/devtool/...
  ok  github.com/qompack/qompack/test/guards    47.638s
  ok  github.com/qompack/qompack/tools/devtool   2.298s
  EXIT=0

go run ./tools/devtool cover
  ... internal/observer             3.957s  coverage: 93.7% of statements   (floor 75%)
      internal/observer/observertest 2.175s coverage: 96.0% of statements
  --- FAIL: TestIntegration_TombstoneRoundTripThroughStore (0.17s)
      hookflow_test.go:885: Expected value not to be nil.
        tombstone "[cleared: sha256:c9244a613361... . 1.8KB . FileRead src/auth.ts . re-expandable]"
        must match the §8.1 item-2 form
  FAIL  github.com/qompack/qompack/test/integration  194.885s
  devtool: cover: cover: go test -coverprofile: exit status 1
  COVER_EXIT=1

go run ./tools/devtool test
  (exactly one failure across the whole tree, the same one)
  --- FAIL: TestIntegration_TombstoneRoundTripThroughStore (0.12s)
      hookflow_test.go:885: Expected value not to be nil.
        tombstone "[cleared: sha256:c9244a613361... . 1.8KB . FileRead src/auth.ts . re-expandable]"
        must match the §8.1 item-2 form
  FAIL  github.com/qompack/qompack/test/integration  231.645s
  devtool: test: exit status 1
  TEST_EXIT=1
```

(The middle dots in the two quoted tombstones are U+00B7 in the real output; they are transcribed
as ASCII periods here only to keep this file's own separators unambiguous.)

`gofmt -l` clean on both trees; `go vet ./tools/devtool/... ./test/guards/` clean.

**On the cover gate specifically.** `taskCover` runs `go test -coverprofile ./...` *before* it
applies any floor, so the failure above stops it before it prints a single floor line — the gate
cannot be shown green until A.5 is resolved. Both of the things it would have reported for
`observer` are nonetheless satisfied and independently checked: `floorApplies` now returns true, so
the exempt-but-real branch is not reached at all; `tools/devtool`'s own tests assert the landed set
is consistent; and the profile above measures `internal/observer` at **93.7% against its 75%
floor**.

## A.4 New concern — `devtool lint` (runpatterns) is now RED, and one row is a real plan defect

Landing the flag makes `pkgTestSetIsSettled("observer")` true, which is what turns *every* plan
`-run` pattern naming `./internal/observer` from "skipped, unlanded" into "checkable". Fifteen
patterns now resolve to nothing:

```
FAIL runpatterns: runpatterns: 15 unsatisfiable -run pattern(s):
  V3-VERIFY:388  "TestSupersede|TestIsSuperset|PropertyIsSuperset"
  V3-VERIFY:391  "TestOnUserPrompt|TestVerbatimPromptID"
  V3-VERIFY:392  "TestOnStop|TestTailAssistantText"
  V4-VERIFY:358  "TestOnSessionStart_CompactDelegates|...|TestOnSessionStart_ClearDelegates"
  V4-VERIFY:360  "TestOnSessionStart_StartupOpensSegment|TestOnSessionEnd_Order"
  V4-VERIFY:361  "TestOnUserPrompt|TestVerbatimPromptID"
  V5-VERIFY:266  "TestSupersede_|TestIsSuperset_|PropertyIsSupersetReflexive"
  V5-VERIFY:269  "TestOnUserPrompt_|TestVerbatimPromptID"
  V5-VERIFY:270  "TestOnUserPrompt_Thrash"
  V5-VERIFY:271  "TestOnStop_|TestTailAssistantText_"
  V6-VERIFY:235  "TestSupersede"
  V6-VERIFY:236  "TestGraphEdges"
  V6-VERIFY:238  "TestOnUserPrompt_NeverRegenerated"
  V6-VERIFY:239  "TestOnStop_RetrievalPathG10_1"
  V6-VERIFY:241  "TestOnSessionStart|TestOnSessionEnd"
```

Fourteen of the fifteen are simply **ahead of the branch** and are satisfied by SP-08's own
remaining commits — `TestSupersede*`/`TestIsSuperset*` by Commit 3,
`TestOnUserPrompt*`/`TestVerbatimPromptID` by Commit 4,
`TestOnStop*`/`TestTailAssistantText*` by Commit 5,
`TestOnSessionStart*`/`TestOnSessionEnd*` by Commit 6. So `devtool lint` stays red on this branch
until Commit 6 lands, and green afterwards. That is a direct, unavoidable cost of moving the
landing flag onto the branch instead of onto the merge, and the controller should know it before
the next push, because `lint` is a CI gate.

**The fifteenth is a genuine plan defect, not a timing artefact.**
`plans/V6-VERIFY-production-readiness-and-uat.md:236` (row 1.8.4) runs
`go test -run TestGraphEdges ./internal/observer/`, but `plans/V3-SP-08-observer-l0.md` names every <!-- runpatterns: quotes the defective V6 command this finding documents -->
DAG row `TestGraph_*` (`TestGraph_ToolUseProducesResult`, `TestGraph_SharedFileOrientation`, ...)
and Commit 2 shipped them under exactly those names. No SP-08 commit will ever declare a
`TestGraphEdges`, so that row verifies nothing and `go test` would print `ok` and exit 0 — the exact
failure mode `runpatterns` exists to catch, and the same class the 2026-08-23 wave-2+ scope audit
found ("later-wave plans drift from shipped code as a class"). The fix is a one-word plan edit
(`TestGraphEdges` -> `TestGraph_`), which is outside this task's write scope. **Flagged for the
controller.**

## A.5 New concern — a PRE-EXISTING failure in `test/integration`, not caused by either commit

`TestIntegration_TombstoneRoundTripThroughStore` fails, and the cause is **Commit 1** (`c0c63c9`),
not this task:

- `internal/observer/tombstone.go` before `c0c63c9` rendered
  `fmt.Sprintf("[cleared: sha256:%s . %s . %s %s . re-expandable]", rec.Root.Short(), ...)` —
  twelve hex characters and then the separator.
- `c0c63c9`'s extended renderer writes `tombstoneOpen`, then `rec.Root.Short()`, **then
  `tombstoneEllipsis`**, which is what Qompack.md §8.1's own example shows.
- `test/integration/hookflow_test.go:212`'s `hookflowTombstoneRE` — added in `6b57cdb`, *before*
  `c0c63c9` — is `^\[cleared: sha256:([0-9a-f]{12}) . [\d.]+KB . FileRead src/auth\.ts .
  re-expandable\]$` with no ellipsis. It therefore stopped matching the moment Commit 1 landed.

`hookflow_test.go` builds its own §8.1 chain by hand ("observer.OnToolUse replaces in V3") and never
calls `observer.New`, so neither `ba477c5` nor `2e13d39` can reach it; the provenance above is from
reading the two diffs rather than from a run at `c0c63c9`. Per the ruling I have **reported rather
than fixed** it. Resolving it is a one-line choice someone else owns: either widen the integration
regex to accept the ellipsis (matching §8.1's example and Commit 1's own golden), or drop the
ellipsis from the renderer. `test/integration` and `tombstone.go` are both outside this task's
write scope.

Everything else in `go run ./tools/devtool test` is green.

---

# Appendix B — the two follow-up fixes, folded into the gates commit by amendment

**Status:** COMPLETE. `go run ./tools/devtool test` is green tree-wide; `lint` is down to exactly
the 14 accepted ahead-of-the-branch patterns.
**Amended commit:** `f1f56e8` `chore(devtool,guards): mark the observer landed for the stub and cover gates`
(was `2e13d39`; subject unchanged, body extended, unpushed).

## B.1 The two fixes

1. **`plans/V6-VERIFY-production-readiness-and-uat.md:236` (row 1.8.4).**
   `go test -run TestGraphEdges ./internal/observer/` → `go test -run TestGraph_ ./internal/observer/`. <!-- runpatterns: left side quotes the defective V6 command being corrected -->
   That is the minimal edit that resolves to a non-empty set and keeps the row's intent: every DAG
   row `ba477c5` shipped is named `TestGraph_*` (`TestGraph_ToolUseProducesResult`,
   `TestGraph_SharedFileOrientation`, `TestGraph_SymbolEdges`, …), so the pattern now re-runs
   exactly the rows the line describes. The pattern contains no pipe, so `runpatterns`'
   escaped-pipe rule is not in play.

2. **`test/integration/hookflow_test.go:212` — `hookflowTombstoneRE`.** The regex now **requires**
   the ellipsis rather than accepting either form:
   ```
   ^\[cleared: sha256:([0-9a-f]{12})… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$
   ```
   The doc comment above it says why the ellipsis is the contract and not a tolerance: Qompack.md
   §8.1's own example marker elides the digest (`sha256:a3f2…`), and
   `testdata/golden/observer/tombstones.txt` pins the shipped renderer byte-for-byte with the
   ellipsis on every one of its lines — for example
   `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]`. Accepting
   either form would let the short hash silently stop announcing that it is an elision, which is
   the one thing that tells a reader the marker is an address to re-expand rather than a whole
   digest. The capture group is unchanged, so step 2 of the test still compares it against
   `rec.Root.Short()`.

## B.2 Verification after the amendment

```
go test -count=1 -timeout=10m -run TestIntegration_TombstoneRoundTripThroughStore ./test/integration/
  ok  github.com/qompack/qompack/test/integration  1.721s
  EXIT=0

go run ./tools/devtool test
  TEST_EXIT=0        (no FAIL line anywhere in the tree)

go run ./tools/devtool lint
  PASS golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips,
       docmarkers, coveragefloors
  FAIL runpatterns: 14 unsatisfiable -run pattern(s)   (the accepted set, enumerated in B.3)
  LINT_EXIT=1

go run ./tools/devtool check-commit-msg <git log -1 --format=%B>
  MSG_OK

git log -1 --format=%B | grep -iE 'co-authored-by|signed-off-by|generated with|claude-session|robot emoji'
  NO_TRAILERS
```

`go vet ./test/integration/` clean; `gofmt -l test/integration/` clean.

`plans/V6-VERIFY…:236` is **gone** from the runpatterns output — that was the fifteenth pattern,
and it was the only one of the fifteen that no SP-08 commit would ever have satisfied.

## B.3 The 14 accepted patterns — the set later tasks must watch shrink

These are ahead of the branch, not defects. Each is written by a later commit of THIS subplan, so
the correct expectation is that this list only ever gets shorter, never longer, and is empty once
Commit 6 lands. If a later task sees an entry that is not in this list, it is a new defect.

| # | Document:line | Pattern | Written by |
|---|---|---|---|
| 1 | `V3-VERIFY:388` | `TestSupersede\|TestIsSuperset\|PropertyIsSuperset` | Commit 3 |
| 2 | `V3-VERIFY:391` | `TestOnUserPrompt\|TestVerbatimPromptID` | Commit 4 |
| 3 | `V3-VERIFY:392` | `TestOnStop\|TestTailAssistantText` | Commit 5 |
| 4 | `V4-VERIFY:358` | `TestOnSessionStart_CompactDelegates\|TestOnSessionStart_CompactWithoutRehydratorIsEmpty\|TestOnSessionStart_ClearDelegates` | Commit 6 |
| 5 | `V4-VERIFY:360` | `TestOnSessionStart_StartupOpensSegment\|TestOnSessionEnd_Order` | Commit 6 |
| 6 | `V4-VERIFY:361` | `TestOnUserPrompt\|TestVerbatimPromptID` | Commit 4 |
| 7 | `V5-VERIFY:266` | `TestSupersede_\|TestIsSuperset_\|PropertyIsSupersetReflexive` | Commit 3 |
| 8 | `V5-VERIFY:269` | `TestOnUserPrompt_\|TestVerbatimPromptID` | Commit 4 |
| 9 | `V5-VERIFY:270` | `TestOnUserPrompt_Thrash` | Commit 4 |
| 10 | `V5-VERIFY:271` | `TestOnStop_\|TestTailAssistantText_` | Commit 5 |
| 11 | `V6-VERIFY:235` | `TestSupersede` | Commit 3 |
| 12 | `V6-VERIFY:238` | `TestOnUserPrompt_NeverRegenerated` | Commit 4 |
| 13 | `V6-VERIFY:239` | `TestOnStop_RetrievalPathG10_1` | Commit 5 |
| 14 | `V6-VERIFY:241` | `TestOnSessionStart\|TestOnSessionEnd` | Commit 6 |

Grouped by the commit that clears them: **Commit 3** clears 1, 7, 11 (three); **Commit 4** clears
2, 6, 8, 9, 12 (five); **Commit 5** clears 3, 10, 13 (three); **Commit 6** clears 4, 5, 14 (three).
`devtool lint` therefore stays red on this branch until Commit 6 and is green from there on.

Two of those names deserve a flag for the commits that own them, because a pattern that resolves is
not the same as a pattern that resolves to the test the row means:

- Commits 4 and 5 must declare tests whose names actually START with `TestOnUserPrompt` /
  `TestOnStop` / `TestTailAssistantText` / `TestVerbatimPromptID`, and Commit 3 with `TestSupersede`
  / `TestIsSuperset` / `PropertyIsSuperset`. `go test -run` anchors at the start of the test name,
  so a row naming `TestOnStop_RetrievalPathG10_1` needs that exact prefix, not a paraphrase.
- `V5-VERIFY:266`'s `PropertyIsSupersetReflexive` and `V3-VERIFY:388`'s `PropertyIsSuperset` do not
  begin with `Test`, `Benchmark`, `Fuzz` or `Example`, so `go test -run` can only match them as
  subtest names beneath a parent test. Commit 3 should check that its supersession property test
  really does spell its subtest that way, or the row will keep resolving to nothing after the
  parent lands.

## B.4 Concerns after this round

1. `devtool lint` is red until Commit 6, by the accepted trade in B.3. `lint` is a CI gate, so any
   push of this branch before Commit 6 will show a red `lint` job with exactly those 14 lines.
2. Nothing else. `devtool test` is green tree-wide, `devtool cover`'s only blocker (the
   `test/integration` tombstone regex) is fixed, and `internal/observer` measures 93.7% against its
   75% floor with the floor now switched on.

---

# Appendix C — review round: six findings applied, finding 1 STOPPED for a ruling

**Status:** BLOCKED on a controller ruling. Findings 2–7 are applied and fully verified; finding 1
is applied and its re-measurement **breaches budget B-C by 4–12×**, which is the condition the
ruling said to stop on ("If the varied-body p99 exceeds 50 ms, report the numbers and STOP for a
controller ruling instead of tuning").

**History was NOT re-minted.** `git reset --soft c0c63c9` and the two recommits are deliberately
not done: the pipeline commit's body is required to carry the new benchmark figures, and those
figures are exactly what is awaiting a ruling. Re-minting now would bake unratified numbers — and
possibly the wrong remedy — into a message. Every edit is in the working tree, verified, and one
`reset --soft` away from being committed the moment the ruling lands. Head is still
`f1f56e8` / `ba477c5`; `git status` shows six modified files and nothing else.

## C.1 Finding 1 — the review was right, and the effect is much larger than "optimistic"

`varyBody(body, i)` now splices an 8-character alphabetic iteration marker into the payload every
`varyStride` = 512 bytes, which is below the store's 1 KB minimum FastCDC chunk, so every chunk of
every iteration differs from the last. The marker is alphabetic on purpose: a numeric counter is a
canonicalization target (the `pids`, `addresses` and `durations` classes all rewrite numbers), and a
marker the canonicalizer normalized away would leave every iteration's **canonical** bytes identical
again — the same dedup, hidden one layer deeper.

`reportBudget` now also publishes `objects`, the count of chunks the store actually holds. That is
the metric that proves novelty; `dedup-x` is reported beside it but is explicitly not the proof,
because `store.Stats.DedupRatio` is `RawBytes/Bytes` over a **compressed** store and folds zstd and
within-payload repetition into the same number.

### The three-point measurement (256 KB `go test` output, `-benchtime 100x -count=2`)

Run from a throwaway diagnostic file, since deleted. A is what `ba477c5` shipped, C is what the
branch ships now, and B is Qompack.md §8.1's own canonical case — *"same test suite, one new
failure"* — one changed line per iteration.

| variant | ns/op | objects | dedup-x | p50 | p99 |
|---|---|---|---|---|---|
| **A** identical body (as shipped in `ba477c5`) | 33.2–34.4 ms | 34 | 1120 | 32.8–36.9 ms | 45.06 ms |
| **B** one changed line — §8.1's own example | 37.9–38.3 ms | 163 | 226 | 36.9–41.0 ms | **90.11 ms** |
| **C** every chunk novel (what is now committed) | 138–141 ms | 6099 | 3.76 | 147.5 ms | 180–197 ms |

`objects` climbing 34 → 163 → 6099 over 100 iterations is the evidence that the variation works: A
stores one payload's chunks and then nothing, C stores a fresh set every call.

**The finding that forces the stop is B, not C.** The realistic case — the one the design names —
is already **~1.8× over the 50 ms B-C p99 budget**. So the breach is not an artefact of my pessimal
fixture; C only shows how far it goes at the limit. Variant A, the only one that stayed inside
budget, is the unrealistic one.

### The two shipping benchmarks, re-measured (`-benchtime 200x -count=5`)

```
BenchmarkOnToolUse_FileRead64KB-22      200   20923420 ns/op   63.80 dedup-x   20.48 p50-ms   36.86 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   20470620 ns/op   63.80 dedup-x   20.48 p50-ms   45.06 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   19582115 ns/op   63.80 dedup-x   20.48 p50-ms   36.86 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   20149208 ns/op   63.80 dedup-x   20.48 p50-ms   30.72 p99-ms
BenchmarkOnToolUse_FileRead64KB-22      200   20966660 ns/op   63.80 dedup-x   20.48 p50-ms   40.96 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200  159868617 ns/op    3.757 dedup-x  163.8 p50-ms   229.4 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200  171634622 ns/op    3.757 dedup-x  163.8 p50-ms   458.8 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200  141865018 ns/op    3.757 dedup-x  147.5 p50-ms   196.6 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200  149142964 ns/op    3.757 dedup-x  147.5 p50-ms   245.8 p99-ms
BenchmarkOnToolUse_TestOutput256KB-22   200  184615058 ns/op    3.757 dedup-x  147.5 p50-ms   589.8 p99-ms
```

(That run predates the `objects` metric being added to `reportBudget`; the `objects` column in the
table above comes from the diagnostic run. Both benches otherwise measure exactly what ships.)

Against **B-C (`l0_process`, p99 < 50 ms, soft, `Gated: false`)**:

- `FileRead64KB` — mean 19.6–21.0 ms (was 9.8–10.6), p50 20.48 ms, p99 30.7–45.1 ms. **Still inside,
  but headroom fell from roughly 65% to 10–39%.**
- `TestOutput256KB` — mean 141.9–184.6 ms (was 34.1–34.3), p50 147.5–163.8 ms, p99 196.6–589.8 ms.
  **Outside by 4–12×.**

### Where the cost is, for the ruling

From the earlier diagnostic (Appendix A), the 256 KB payload's fixed cost splits roughly as
canonicalization + MinHash ≈ 25–30 ms and the rest of `PutBytes` ≈ 26–33 ms. Everything above that
in variant C is the **novel-chunk write path** that variant A never touched: zstd compression and an
object write per chunk, 6099 of them across 100 calls. It is SP-06/SP-04/SP-03 work inside
`store.PutBytes`, not observer bookkeeping — the observer's own share is the ~7 ms of repeated
`responseText` decoding already noted in Appendix A §4.

I have **not tuned anything**, per the instruction. Options a ruling might pick from, none of them
mine to take: accept the breach on a soft, ungated budget and record it; shrink the benchmark
payload to something closer to a session's median tool result; make the fixture's variation
realistic (variant B) rather than pessimal (variant C) and rule on 90 ms; move the novel-chunk write
off the hot path (§8.1's own "degrade to async queue-and-drain rather than blocking"); or re-open
B-C's 50 ms row.

## C.2 Findings 2–7 — applied and verified

| # | File | Change |
|---|---|---|
| 2 | `internal/observer/graph.go` | The mid-pipeline `ctx.Err()` short-circuit is **removed**. A comment in its place says why: past step 1 the bookkeeping is all-or-nothing, and returning early left the index entry written while `advancePos`/`LastToolUseID` went unadvanced with steps 11–14 still running on top. |
| 3 | `internal/observer/state.go` | `last_prompt_turn` **kept**, with a four-line comment saying it is beyond the pinned shape and why it is persisted anyway: it is `OnUserPrompt`'s own bookkeeping, it is recoverable from no other persisted field, and losing it on resume would make the first prompt after a daemon restart look like the session's first. Also recorded as a deviation here. |
| 4 | `internal/observer/observer_test.go` | `TestObserver_NowIsTheOnlyClockCallSite` → `TestObserver_NowTruncatesToUnixMilli`. The only-call-site property is enforced by the AST tests, not by this one. |
| 5 | `internal/observer/features_test.go` | The `var _ = core.UnixMilli(0)` shim and the now-unused `core` import are gone. |
| 6 | `plans/V6-VERIFY-…md` §1.8.5 | The unsatisfiable `grep -rn "Bloom" internal/observer/` (expecting zero) is replaced by the check that is actually enforced: `go test ./internal/observer/ -run 'TestObserverSourceHasNoBloomReference\|TestObserverNeverFeedsBloom' -count=1`. The expectation cell now says "zero `Bloom` **IDENTIFIERS**", and names the `go/parser` walk as the reason a grep cannot be the check — doc comments and the §8.1 item-5 prohibition legitimately spell Bloom in prose. The `TestSketches` / `TestOnSessionEnd_NeverWritesTriedBloom` command is kept, so the row's CMS/HLL and `tried.bloom` intent is intact. |
| 7 | `internal/observer/tooluse_test.go` | The `ByPathCalls == 0` assertion is kept, with a comment saying it is vacuous until `supersede.go` exists and that Commit 3 must keep it meaningful — the supersession scan it lands is what would start calling `ToolUsesByPath`, and resolved decision 6's ephemeral exclusion is what must keep it from doing so. |

### One follow-on the review did not name

Removing the mid-pipeline `ctx` check (finding 2) left `ctx` unused, and `golangci-lint` failed:

```
internal\observer\graph.go:35:34: `(*observer).emitToolGraph` - `ctx` is unused (unparam)
```

The parameter is now named `_ context.Context`. Arity and types are unchanged, so the signature the
plan pins is unchanged; only the binding name is dropped, and the doc comment says why the context
is taken and not used.

## C.3 Commands

```
go test -count=1 ./internal/observer/...
  ok  internal/observer               3.946s
  ok  internal/observer/observertest  2.150s        EXIT=0

go test -race -count=1 -timeout=30m ./internal/observer/...
  ok  internal/observer               6.323s
  ok  internal/observer/observertest  2.766s        EXIT=0

go test -run '^$' -bench BenchmarkOnToolUse -benchtime 200x -count=5 -timeout=30m ./internal/observer/
  (figures in C.1)                                  EXIT=0

go run ./tools/devtool lint
  PASS golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips,
       docmarkers, coveragefloors
  FAIL runpatterns: 14 unsatisfiable -run pattern(s)
  LINT_EXIT=1

go run ./tools/devtool test
  TEST_EXIT=0        (no FAIL line anywhere in the tree)
```

`gofmt -l internal/observer/` clean; `go vet ./internal/observer/` clean.

**The runpatterns count is unchanged at 14, and the set is identical to Appendix B §B.3** —
confirming the §1.8.5 row edit neither added nor removed a checkable pattern. `V6-VERIFY:236`
(`TestGraphEdges`) and `V6-VERIFY:237` (§1.8.5) are both absent from the list.

`check-commit-msg` was **not** re-run on new messages, because no new messages exist yet — see the
note at the top of this appendix.

## C.4 What is ready to commit the moment a ruling lands

```
M internal/observer/features_test.go     -> pipeline commit (finding 5)
M internal/observer/graph.go             -> pipeline commit (finding 2 + the unparam follow-on)
M internal/observer/observer_test.go     -> pipeline commit (finding 4)
M internal/observer/state.go             -> pipeline commit (finding 3)
M internal/observer/tooluse_test.go      -> pipeline commit (findings 1 and 7)
M plans/V6-VERIFY-production-readiness-and-uat.md -> gates commit (finding 6)
```

The re-mint is then `git reset --soft c0c63c9`, `git add internal/observer` + the pipeline message
with whatever bench figures the ruling settles on, then `git add` the five gates files plus the plan
and the gates message. `git log --oneline c0c63c9..HEAD` must show exactly 2.

---

# Appendix D — ruling applied: benches kept honest, SP08-D1 opened, both commits re-minted

**Status:** COMPLETE.
**Commits (exactly two on top of `c0c63c9`):**

| SHA | Subject |
|---|---|
| `d860ccd` | `feat(observer): PostToolUse pipeline: store, index, sketches, DAG emission` |
| `20a38e2` | `chore(devtool,guards): mark the observer landed for the stub and cover gates` |

Working tree clean. Both messages pass `check-commit-msg`; `git log c0c63c9..HEAD --format=%B`
grepped for `co-authored-by|signed-off-by|generated with|claude-session|robot emoji` returns nothing.

## D.1 Both fixtures kept, per bench

Each of the two shipping benchmarks now runs three sub-benches over the same payload, one per point
on the dedup axis. The top-level function names are unchanged, so `-bench BenchmarkOnToolUse` and
`go test -list` (which is how `test/guards/carrieddefects_test.go` resolves an evidence name) still
find them.

| sub-bench | what varies per iteration | why it is here |
|---|---|---|
| `/Deduped` | nothing | The optimistic end, kept for comparison. Dedups completely from the second call, so the store never writes a chunk — this is what the review caught the original benches measuring. |
| `/Delta` | one line, mid-payload | Qompack.md §8.1's own near-duplicate case, *"same test suite, one new failure"*. The realistic shape, and the fixture SP08-D1's acceptance criterion is stated against. |
| `/AllNovel` | a marker every 512 B | Below the store's 1 KB minimum FastCDC chunk, so every chunk is novel. The pessimal end. |

Supporting changes: `benchTag(i)` is the shared alphabetic marker (alphabetic so the `pids`,
`addresses` and `durations` canonicalizers cannot normalize the variation away and restore dedup one
layer deeper); `deltaBody` and `varyBody` are the two vary functions; `benchFixtures()` is the table;
`runOnToolUseBench` is the shared driver. `reportBudget` publishes `p50-ms`, `p99-ms`, `objects` and
`dedup-x`, with a comment saying `objects` is the proof and `dedup-x` is not (it is `RawBytes/Bytes`
over a *compressed* store, so it folds in zstd and within-payload repetition).

**No benchmark asserts or fails the build.** The two `require.GreaterOrEqual` payload-size guards are
gone; what remains is setup error handling (`store.Open`, `os.ReadFile`, `Stats`) and `b.Fatal` on an
actual `OnToolUse` error. `runOnToolUseBench`'s doc comment states the rule and points at SP08-D1 as
where a known latency problem belongs instead.

`objects` across the six sub-benches — 5, 204, 905 and 34, 236, 12209 — is the evidence that the
three fixtures land where their names say.

## D.2 Carried defect SP08-D1

**Manifest row** appended to `plans/CARRIED-DEFECTS.tsv`:

```
SP08-D1  SP-08  V3-VERIFY  open  BenchmarkOnToolUse_TestOutput256KB  OnToolUse over a 256 KB tool
result breaches budget B-C (l0_process, p99 < 50 ms): 53-115 ms on Qompack.md 8.1's own
one-changed-line case and 262-655 ms when every chunk is novel, dominated by store.PutBytes's
per-novel-chunk object-write path
```

(one tab-separated line; wrapped here only for the page.)

**Two deviations from the ruling's literal wording, both forced by the shipped guard.** Neither
changes what the row does; both were found by doing what the ruling also said — reading the existing
rows' format.

1. **`status` is `open` with `owner` `V3-VERIFY`, not `deferred:V3-VERIFY`.**
   `test/guards/carrieddefects_test.go:215` is
   `require.NotEqual(t, d.owner, target, "%s defers to %s, the checkpoint that already owns it — a
   deferral must move the row forward to a LATER checkpoint")`. SP-04's rows read
   `owner=V2-VERIFY, status=deferred:V3-VERIFY` because V2-VERIFY was their own wave's checkpoint and
   they were pushed past it; SP-08 is a wave-3 subplan whose own checkpoint *is* V3-VERIFY, so
   `deferred:V3-VERIFY` would name the checkpoint that already owns it and the guard rejects it.
   `owner=V3-VERIFY, status=open` produces the **identical** resolver — `carriedDefect.resolver()`
   returns the owner for an open row and the deferral target for a deferred one — so the row blocks
   `plans/V3-report.md` exactly as intended, and the acceptance criteria are stated for V3-VERIFY.

2. **The detail document is `plans/V2-SP-08-carried-defects.md`, not `plans/V3-SP-08-…`.**
   `carriedDefectsDocFor` derives the one legal path from the row id and the constant
   `carriedDefectsWave`, which is `"V2"`: for `SP08-D1` that is `plans/V2-SP-08-carried-defects.md`,
   falling back to `plans/V2-WAVE1-carried-defects.md`. A `V3-`prefixed file would not be found and
   the guard would fail asking for the section in the wave-wide document. `plans/CARRIED-DEFECTS.tsv`
   is still the only manifest and already carries SP-02, SP-04, SP-05 and SP-06 rows; the guard's own
   comment says "a later wave keeping its own manifest changes this one constant", and changing it is
   an edit to a guard SP-08 does not own. The file's header explains this in place, so a reader who
   expects `V3-` finds the reason rather than a puzzle.

**Detail document contents** (`plans/V2-SP-08-carried-defects.md`), following the SP-04 section
shape — Symptom / Diagnosis / Evidence / Why deferred / Acceptance:

- the full six-row measurement table (both benchmarks × three fixtures, ns/op, objects, p50, p99,
  in/out of budget), with the machine and the `-benchtime 200x -count=5` conditions stated;
- the diagnosis: canonicalization ~25–30 ms (SP04-D5's per-rule prefilter, at 2.5× the 100 KB that
  row is stated against), MinHash ~10–15 ms at 128 permutations, and the novel-chunk object-write
  path inside `store.PutBytes` — 34 objects/~45 ms against 12 209 objects/~180 ms for the same
  payload size — amplified on Windows, where per-file create/write/close is dearer than on Linux, so
  the CI reference platform should read better;
- a cross-reference to **SP06-D2**, which already records that `PutBytes`'s own cold and warm budgets
  are 9× and 19× over on Windows and have never been measured on the reference platform. SP08-D1 is
  what that costs seen from the caller, at a payload size SP-06's benchmarks do not cover; V3-VERIFY
  should dispose of the two together, and fixing SP06-D2 may close this row with no SP-08 change;
- the evidence benchmark names and a `-bench` reproduction line (deliberately not a `-run` pattern,
  so `devtool lint`'s runpatterns count is untouched);
- why it is deferred: the cost is SP-06's mechanics, reaching into it from here is the cross-subplan
  edit Rule W-1 forbids, and the hook path is unaffected because WAL-before-ACK already implements
  §8.1's "degrade to async queue-and-drain rather than blocking" — B-C is `Gated: false` for that
  reason. No threshold is edited; a §11.3 sign-off is not SP-08's to give;
- acceptance for V3-VERIFY: `BenchmarkOnToolUse_TestOutput256KB/Delta` p99 < 50 ms on the CI
  reference platform on a quiet host at `-benchtime 200x -count=5`, **or** an explicit §11.3 sign-off
  recording the figures and either moving the B-C row or re-deferring this one with a reason. It also
  asks for the whole table to be re-measured, because `/Deduped` breaching on this run when it had not
  on a quieter earlier pass is itself information: the 256 KB fixture sits close enough to the budget
  that host noise crosses it.

## D.3 The authoritative figures

Quiet machine (Intel Core Ultra 7 155H, windows/amd64), `-benchtime 200x -count=5`, real
`store.Open` + `dag.Open` on `b.TempDir()`. p50/p99 from the `observer.tooluse` histogram.

| benchmark | fixture | ns/op | objects | p50 (ms) | p99 (ms) | B-C |
|---|---|---|---|---|---|---|
| `FileRead64KB` | `Deduped` | 11.5–13.4 ms | 5 | 12.3–14.3 | 14.3–18.4 | inside |
| `FileRead64KB` | `Delta` | 15.8–16.7 ms | 204 | 15.4–16.4 | 24.6–45.1 | inside |
| `FileRead64KB` | `AllNovel` | 23.7–24.6 ms | 905 | 24.6 | 36.9–45.1 | inside |
| `TestOutput256KB` | `Deduped` | 39.9–65.6 ms | 34 | 41.0–61.4 | 53.3–131.1 | **breach** |
| `TestOutput256KB` | `Delta` | 39.3–55.6 ms | 236 | 41.0–49.2 | 53.3–114.7 | **breach** |
| `TestOutput256KB` | `AllNovel` | 163.6–224.1 ms | 12209 | 147.5–213.0 | 262.1–655.4 | **breach** |

This is the table in the pipeline commit body and in SP08-D1. It is one `-count=5` pass; the sub-bench
restructuring was confirmed not to have changed anything materially by a cheap `-benchtime 20x
-count=1` pass first, and no further benchmark cycles were spent.

## D.4 Commands

```
go test -count=1 ./internal/observer/...
  ok  internal/observer               3.143s
  ok  internal/observer/observertest  1.938s        EXIT=0

go test -race -count=1 -timeout=30m ./internal/observer/...
  ok  internal/observer               6.878s
  ok  internal/observer/observertest  2.811s        EXIT=0

go test -count=1 -timeout=20m -run TestCarriedDefects ./test/guards/
  ok  test/guards  42.435s                          EXIT=0

go run ./tools/devtool lint
  PASS golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips,
       docmarkers, coveragefloors
  FAIL runpatterns: 14 unsatisfiable -run pattern(s)   — the same 14 as Appendix B §B.3
  LINT_EXIT=1

go run ./tools/devtool test
  TEST_EXIT=0        (no FAIL line anywhere in the tree)

go run ./tools/devtool check-commit-msg <git log -1 --format=%B d860ccd>   OK
go run ./tools/devtool check-commit-msg <git log -1 --format=%B 20a38e2>   OK
git log c0c63c9..HEAD --format=%B | grep -i <trailer patterns>            NONE

git log --oneline c0c63c9..HEAD
  20a38e2 chore(devtool,guards): mark the observer landed for the stub and cover gates
  d860ccd feat(observer): PostToolUse pipeline: store, index, sketches, DAG emission
```

The runpatterns count is **still exactly 14** and the set is unchanged from Appendix B §B.3 — the
§1.8.5 row edit and the new defect document neither added nor removed a checkable pattern. Both
`V6-VERIFY:236` (`TestGraphEdges`) and `V6-VERIFY:237` (§1.8.5) are absent from it.

## D.5 File split at re-mint

`git reset --soft c0c63c9`, then:

```
d860ccd  internal/observer/**                (16 files)
         plans/CARRIED-DEFECTS.tsv           (SP08-D1 row)
         plans/V2-SP-08-carried-defects.md   (new)

20a38e2  tools/devtool/{cover.go,cover_test.go,planchecks_test.go}
         test/guards/{nightlyfuzz_test.go,stubs_test.go}
         test/integration/hookflow_test.go
         plans/V6-VERIFY-production-readiness-and-uat.md   (rows 1.8.4 and 1.8.5)
```

## D.6 Concerns

1. `devtool lint` stays red until Commit 6, by the accepted trade of Appendix B §B.3. `lint` is a CI
   gate, so any push before Commit 6 shows a red `lint` job with exactly those 14 lines.
2. The two SP08-D1 wording deviations in D.2 are the only places this round departs from the ruling
   as written, and both are forced by `test/guards/carrieddefects_test.go`. If the controller wants
   the literal `deferred:V3-VERIFY` status or the `V3-` filename, that is a change to that guard —
   one this task did not make.
3. SP08-D1 and SP06-D2 describe the same mechanics from two sides. They should be adjudicated
   together at V3-VERIFY; closing SP06-D2 may close SP08-D1 for free.

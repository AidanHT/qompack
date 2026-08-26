# Task 4 report — Commit 4: `feat(observer): verbatim UserPromptSubmit capture and thrash warnings`

- Branch: `feat/sp08-observer-l0`
- Base: `39d87ef`
- Commit: **`e957564`** — `feat(observer): verbatim UserPromptSubmit capture and thrash warnings`
- `git rev-list --count 39d87ef..HEAD` = **1**

## What landed

### `internal/observer/prompt.go` (grew from the thrash-only file into the full UserPromptSubmit half)

New production code:

- `OnUserPrompt` / `onUserPrompt` — the brief's nine-step algorithm, wrapped in `o.timed(histPrompt, …)`
  the way `OnToolUse` is. Returns an error only for `ctx.Err()`.
- `recordPrompt` — steps 4 and 5 factored out (the index entry and the DAG node), so the put-failure
  branch is a plain `else` rather than a mid-function jump.
- `VerbatimPromptID(s, t)` → `"prompt_<s>_<t>"`, exported.
- `verbatimOptions()` → `canon.Options{Strip: []canon.Class{}, MinHash: sketch.MinHashOptions{Enabled: false}}`
  — **both** fields, since the post-amendment store honours the MinHash opt-out only when `Strip` is
  non-nil.
- `promptArgs(prompt)` + `promptArgsDoc` → `{"prompt":<text>}` for `store.ArgsDigest`.
- Constants: `userPromptSubmit` (the single spelling used for `PutOptions.Tool`, `ToolUseRecord.Tool`
  **and** `HSO.HookEventName`), `promptSymbol` ("user", used for both the grammar symbol and the
  feature-window tool name), `promptIDPrefix`, `stagePromptPut` ("prompt.put"), `thrashLineSep`.
- `collectThrash` / `pendingThrashLines` / `thrashAdvice` are carried over unchanged from commit 3.

Algorithm details as specified: empty-prompt early return before `o.session`/`o.now`; `KeepRaw: true`,
`Ephemeral: false`, `Path: ""`; `res.Root.Tokens` with the `Tokens.EstimateString(…, tokens.ClassProse)`
fallback; `store.ArgsDigest(promptArgs(e.Prompt))`; `dag.BuildUserPrompt` + `o.enrol(st, dag.UserPromptNode(st.Turn))`;
grammar `"user"`; `LastPromptTurn` / `SubagentSince` / `Turn++` per decision 4; `recordRecent` → `features`
→ `LastTS` in that order; the hand-built `&hookio.HSO{HookEventName: "UserPromptSubmit", …}` literal
carrying the one-line comment that the constructor is deferred to V3-VERIFY; the `ModeFull` gate is
the only `o.mode()` call site in the package.

### `internal/observer/observer.go`

The minimal Commit-4 placeholder `OnUserPrompt` is deleted; the surrounding comment now reads "The
three entry points below". The `Observer` interface is untouched.

### `internal/observer/prompt_test.go`

All thirteen brief rows, plus one extra (`TestPromptArgs_IsTheToolInputAPromptDoesNotHave`) and three
local helpers (`promptOf`, `(*harness).submit`, `readRoot`) and two fixtures (`verbatimPrompt`,
`samplePrompt`). The two real-store rows use `newRealStoreObserver` (commit 3's pattern:
`store.Open(root, cfg, store.Deps{…})` + `dag.Open`), read bytes back with
`st.Open(ctx, root)` → `io.ReadAll`, and:

- `TestOnUserPrompt_VerbatimAgainstARealStore` first pins `config.Defaults().Store.Canonicalize.Strip`
  as the exact six classes, then asserts the prompt (curly apostrophe + `2026-08-24T12:34:56Z` +
  `PID 4711`) comes back byte-for-byte — **and** adds a control: the *same text* pushed through the
  *same store* as a `Bash` result loses both the timestamp and the PID. Without the control the row
  would pass against a store configured to strip nothing, which is precisely what it exists to rule out.
- `TestOnUserPrompt_NeverRegenerated` asserts `Stats().Objects` unchanged after the second identical
  submission, two records with identical `Root` and distinct `ID`/`TS`, and `Stats().ToolUses == 2`.

### `internal/observer/tooluse_test.go` (the two required extensions)

- `TestModePassiveStillWrites` now submits a prompt every fifth event and gives the harness a
  `fakeGrammar` above the thrash threshold, so the two runs differ in exactly the way §12 permits.
  It compares store counts, graph counts and `Touch.Total()` as before, and additionally asserts
  `warned(fullPrompts) == 1` and `warned(passivePrompts) == 0` — "only OnUserPrompt's
  AdditionalContext differs" is now asserted rather than asserted-by-omission.
- `TestOnToolUse_ConcurrentSessionsRaceFree` now drives one prompt **and** one tool use per iteration
  per session (2 × 200 of each). Expectations updated to `Turn == perSession`,
  `PrefixTokens == perSession*2*tokensPerPut` and `LastPromptTurn == perSession-1`, with
  `tokensPerPut` named rather than the previous bare `7`.

## Deviations / interpretations

1. **`on err: … and continue to step 6 with a zero root` (plan line 1457).** Read as *jump to step 6*:
   steps 4 and 5 are **skipped**, steps 6–9 still run. Rationale: the package's stated policy in
   `tooluse.go` step 5 is "never a dangling index record", and reading it the other way would write a
   `ToolUseRecord{Root: <zero>, Status: StatusOK}` naming an object that was never written, plus a DAG
   node pointing at it. The plan's two sibling idioms (lines 1031 and 1581) both say "return"; this one
   deliberately says "continue", which is what `TestOnUserPrompt_PutFailureStillIncrementsTurn`
   (`Turn == 1`, `observer.err.prompt.put == 1`) pins. The test also asserts no record and no graph
   node were written, so the interpretation is now pinned in both directions and is cheap to flip if
   V3-VERIFY disagrees.
2. **`pendingThrashLines`' rendering is commit 3's, not the plan prose's.** The plan text says
   `collectThrash` queues and `pendingThrashLines` sets `WarnedRules`, with
   `Message: "repeated action cycle detected", Turns: nil`. The **landed** commit-3 implementation
   marks `WarnedRules` at collect time and renders `Message: thrashAdvice` ("consider a different
   approach"), `Turns: []core.TurnIndex{st.Turn}`, with its own tests asserting exactly that. I did not
   change it: the observable contract the brief's rows specify — one warning per rule per session, full
   mode only — holds identically either way, and rewriting a landed, tested function inside this commit
   would have been out of scope. Flagged for V3-VERIFY.
   Consequence worth noting: because step 7 increments before step 9 drains, `Warning.Turns` carries the
   **post-increment** turn (e.g. `[1]` for the first prompt). The tests pin this.
3. **`Bytes: int64(len(body))`** as the plan specifies, rather than `res.Root.RawBytes` as `tooluse.go`
   uses. For this call site they are the same number (the body is exactly what was handed to `PutBytes`),
   so this is a spelling difference only.
4. `thrashLineSep` is a single-use named constant for the `strings.Join` separator; kept named for the
   same reason the rest of the package names its literals.

Nothing outside `internal/observer/**` was touched. `internal/hookio` was **not** edited.

## RED

Tests written first; before `prompt.go` grew its new functions the package did not build:

```
$ go test ./internal/observer/ -run 'TestOnUserPrompt|TestVerbatimPromptID|TestPromptArgs' -count=1
# github.com/qompack/qompack/internal/observer [github.com/qompack/qompack/internal/observer.test]
internal\observer\prompt_test.go:124:30: undefined: userPromptSubmit
internal\observer\prompt_test.go:155:19: undefined: userPromptSubmit
internal\observer\prompt_test.go:182:30: undefined: VerbatimPromptID
internal\observer\prompt_test.go:209:19: undefined: userPromptSubmit
internal\observer\prompt_test.go:212:19: undefined: VerbatimPromptID
internal\observer\prompt_test.go:221:38: undefined: promptArgs
internal\observer\prompt_test.go:263:26: undefined: VerbatimPromptID
internal\observer\prompt_test.go:299:31: undefined: VerbatimPromptID
internal\observer\prompt_test.go:301:31: undefined: VerbatimPromptID
internal\observer\prompt_test.go:334:36: undefined: promptSymbol
internal\observer\prompt_test.go:334:36: too many errors
FAIL	github.com/qompack/qompack/internal/observer [build failed]
```

## GREEN

Every command run separately, exit code checked on the command itself (never through a pipe that
would mask it).

| Command | Result |
|---|---|
| `go run ./tools/devtool fmt` | exit 0, no rewrites (`git status` showed only my four files) |
| `go run ./tools/devtool lint` | golangci-lint / nomagic / importgraph / testdeps / bindeps / sleepcheck / stubskips / docmarkers / coveragefloors **all PASS**; `runpatterns` FAIL with the expected shrunken set (below) |
| `go run ./tools/devtool vet` | exit 0 |
| `go test -count=1 -timeout=30m ./internal/observer/...` | `ok internal/observer 4.171s`, `ok internal/observer/observertest 2.047s` |
| `go test -race -count=1 -timeout=30m ./internal/observer/...` | `ok internal/observer 5.792s`, `ok internal/observer/observertest 2.782s` |
| `go run ./tools/devtool test` | exit 0 (whole tree, incl. `test/guards` 75.3s and `test/integration` 182.4s) |
| `go run ./tools/devtool cover` | **`OK observer: 95.6% >= floor 75%`**; every other floor OK |

The twenty-one focused rows all pass:

```
--- PASS: TestOnUserPrompt_StoresVerbatim
--- PASS: TestOnUserPrompt_VerbatimAgainstARealStore
--- PASS: TestOnUserPrompt_RecordsIndexEntry
--- PASS: TestOnUserPrompt_TurnIncrements
--- PASS: TestOnUserPrompt_DAGNodeAndSegmentEdge
--- PASS: TestOnUserPrompt_NeverRegenerated
--- PASS: TestOnUserPrompt_EmptyPromptIgnored
--- PASS: TestOnUserPrompt_GrammarSymbolAppended
--- PASS: TestOnUserPrompt_ThrashWarningInFullMode
--- PASS: TestOnUserPrompt_NoThrashWarningInPassiveMode
--- PASS: TestOnUserPrompt_ThrashWarnedOncePerRule
--- PASS: TestOnUserPrompt_PutFailureStillIncrementsTurn
--- PASS: TestVerbatimPromptID
--- PASS: TestPromptArgs_IsTheToolInputAPromptDoesNotHave
--- PASS: TestModePassiveStillWrites
--- PASS: TestOnToolUse_ConcurrentSessionsRaceFree
(+ the five carried-over TestCollectThrash/TestPendingThrashLines rows)
```

The observertest `/behaviour` block runs for real against `observer.New` and
`user_prompt_capture_never_blocks_the_prompt` passes with the real implementation (verified with
`-run TestObserverSuite_RealObserver -v`).

## Lint plan-gate: the remaining set

Was 11 accepted patterns before this commit; now **6**, and every one is a `TestOnStop*` /
`TestOnSessionStart*` / `TestOnSessionEnd*` row — i.e. exactly the expected residue, **no new entry**:

```
plans/V3-VERIFY-observer-and-negative-knowledge.md:392  -run "TestOnStop|TestTailAssistantText"
plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:358  -run "TestOnSessionStart_CompactDelegates|TestOnSessionStart_CompactWithoutRehydratorIsEmpty|TestOnSessionStart_ClearDelegates"
plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:360  -run "TestOnSessionStart_StartupOpensSegment|TestOnSessionEnd_Order"
plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:271  -run "TestOnStop_|TestTailAssistantText_"
plans/V6-VERIFY-production-readiness-and-uat.md:239  -run "TestOnStop_RetrievalPathG10_1"
plans/V6-VERIFY-production-readiness-and-uat.md:241  -run "TestOnSessionStart|TestOnSessionEnd"
```

Cleared by this commit: V3:391, V4:361, V5:269, V5:270, V6:238.

## Files changed

```
internal/observer/observer.go      (placeholder OnUserPrompt removed)
internal/observer/prompt.go        (OnUserPrompt, recordPrompt, VerbatimPromptID, verbatimOptions, promptArgs)
internal/observer/prompt_test.go   (13 brief rows + 1 + helpers)
internal/observer/tooluse_test.go  (the two required extensions)
4 files changed, 590 insertions(+), 28 deletions(-)
```

## Self-review

- **The plan's own review item** — `grep -n 'UserPromptSubmit' internal/observer` reviewed line by line.
  Three production write sites exist and none of them is an update: `Store.PutBytes` (content-addressed,
  no update path), `Store.RecordToolUse` (append-only), `dag.BuildUserPrompt` → `AddNode`/`AddEdge`
  (insert-if-absent). No `MarkSuperseded`, no `AppendFileVersion` and no second `PutBytes` is ever
  reachable for a prompt: `detectSupersession` is called only from `onToolUse`, it returns early on an
  empty `Path`, and prompt records carry `Path: ""`, so `ToolUsesByPath` can never return one. G2.3's
  "never regenerated" holds structurally, and `TestOnUserPrompt_NeverRegenerated` pins it against a
  real store.
- Import allow-set unchanged: `prompt.go` adds only `canon`, `dag`, `hookio`, `sketch`, `store`,
  `tokens`, `json`, `fmt`, `strings` — all already permitted; `importgraph` passes.
- No `time.Now()`; the single `o.now()` call sits directly after the ctx check and its value is reused
  for the record, the node and the feature sample.
- Locking follows decision 9: `o.session()` (takes and releases `o.mu`) then `st.mu.Lock(); defer Unlock()`
  for the remainder. `o.mu` is never taken under `st.mu`. Race suite clean with two sessions × 200
  prompts + 200 tool uses each.
- No magic literals: every number in the new code is either named or absent; `nomagic` passes.
- No `TODO`/`FIXME`/`core.ErrNotImplemented` left on this path.

## Concerns for the next task / V3-VERIFY

1. **`stop.go` (Commit 5) must reuse `verbatimOptions()`**, per the plan's "`stop.go`'s capture blob
   takes the same `verbatimOptions()` treatment". It is unexported and in-package, so this is free —
   just don't hand-roll a second `canon.Options`.
2. **`Warning.Turns` carries the post-increment turn** (deviation 2). If SP-15 or V3-VERIFY ever pins the
   rendered warning byte-for-byte against a plan-stated `Turns: nil` / `Message: "repeated action cycle
   detected"`, the mismatch is in commit 3's `pendingThrashLines`, not here.
3. **`WarnedRules` marking happens in `collectThrash`, not in the drain** (deviation 2). Consequence: a
   rule that goes above threshold while the session is in `ModePassive` is marked warned and its queued
   line is never drained until some later prompt in `ModeFull` — the queue itself survives, so the line
   is not lost, but it is delayed. That is the plan's own design (mode gates output, never writes) and
   `TestOnUserPrompt_NoThrashWarningInPassiveMode` pins the non-emission half.
4. **Put-failure semantics** (deviation 1) are a reading of an ambiguous plan line. Both halves are now
   asserted, so a V3-VERIFY ruling the other way is a two-line change plus one test edit.
5. `PendingThrash` is not persisted (state.go, by design), so a daemon restart between the collecting
   tool use and the next prompt drops the queued line. Pre-existing and documented; noted only because
   Commit 4 is the first commit where that queue actually has a consumer.

# Task 3 report — Commit 3: `feat(observer): supersession and near-duplicate redundancy detection`

**Branch** `feat/sp08-observer-l0` — one commit on top of `20a38e2`.
**Commit** `5e87f6b` `feat(observer): supersession and near-duplicate redundancy detection`
**Footer** `Refs: SP-08, §8.1 item 3, §5.8 MarkSuperseded` — no attribution trailer, no emoji
(verified with `check-commit-msg` and `git log -1 --format=%B`).

---

## 1. What was implemented

### `internal/observer/supersede.go` (new, 142 lines)

`detectSupersession(ctx, _ *sessionState, rec store.ToolUseRecord, res store.PutResult) []core.ToolUseID`
implements the brief's algorithm literally:

- Early return on `rec.Path == "" || rec.Ephemeral || supersedableClass(rec.Tool) == ""` — this is
  what keeps `tooluse_test.go`'s `ByPathCalls == 0` assertion for an `mcp__qompack__` result both
  true and *meaningful*: the scan is the thing that would otherwise call `ToolUsesByPath`, and both
  the class check and the `Ephemeral` check independently stop it.
- `Store.ToolUsesByPath(ctx, rec.Path, supersessionLookback)`; a failure goes through
  `o.soft("supersede.list", err)` and returns nil.
- The five in-loop skip filters in the brief's order: same id, already `StatusSuperseded`,
  `p.TS > rec.TS`, `p.Ephemeral`, class mismatch. Each is a separate `if … { continue }` carrying
  the brief's own reason, so the policy reads as a list rather than as one boolean.
- `p.Root == rec.Root` is the fast path; otherwise `Store.GetRoot(ctx, p.Root)` (soft on error,
  `continue`) then `isSuperset(res.Root.Chunks, pr.Chunks) || (thr > 0 && rec.Signature.IsNearDup(p.Signature, thr))`.
- `Store.MarkSuperseded(ctx, p.ID, rec.ID)` — **older first**; soft on error and `continue`, so a
  record the store refused to mark is never reported as marked.
- `o.count(counterSuperseded)` per mark; `o.count(counterNearDup)` once at the end when
  `res.NearDup != nil`. Both metric names were pre-declared by Commit 1 and are now incremented.
- `marked` is newest-first because `ToolUsesByPath` is; the loop is order-independent because every
  candidate is filtered on `p.TS <= rec.TS` regardless of the order it arrived in.
- **Zero `AddEdge` calls.** `grep -c AddEdge internal/observer/supersede.go` is 0.

`isSuperset(newer, older []core.ChunkRef) bool` — `false` when `len(older) == 0`, else set
containment of `older` in `newer`, multiplicity ignored.

### `internal/observer/tooluse.go` — step 8 wired

The nil placeholder is replaced by:

```go
var superseded []core.ToolUseID
if !empty {
    superseded = o.detectSupersession(ctx, st, rec, res)
}
```

`graph.go` was **not** touched: it already took `superseded []core.ToolUseID`, already handed
`superseded[0]` to `dag.BuildToolUse` as `ObservedTool.Supersedes`, and already emitted the tail
loop itself. The seam was exactly the shape the task described.

### `internal/observer/supersede_test.go` (new, 518 lines)

All 16 plan rows plus three additions (see §2).

### `internal/observer/fakes_test.go` — the double grew teeth

`fakeStore.ToolUsesByPath` returned `nil` unconditionally, and `MarkSuperseded` only recorded. Left
that way, **every negative row in the table would have passed for the wrong reason** and
`TestSupersede_SkipsAlreadySuperseded` would have asserted nothing. Changes:

- `ToolUsesByPath` now serves the recorded index for that path, newest first, capped at `limit`,
  and records the limit in a new `ByPathLimits []int`.
- `MarkSuperseded` applies the flip to the stored record as well as recording the call.
- New `GetRoot`, answering from a new `Roots map[core.Hash]store.Root` that `PutBytes` populates —
  and erroring for a root nobody stored, because a zero `store.Root` has an empty chunk list and
  `isSuperset` would then report "not a superset" instead of the miss it is.
- New `ByPathErr`, `GetRootErr`, `MarkErr` for the soft-failure rows; new `seed()`/`setRoot()`
  staging helpers; new `fakeGraph.edges()` accessor.

No existing test changed or broke.

---

## 2. Deviations from the plan text

1. **`st` is named `_`.** `detectSupersession` never reads the session state (the algorithm is a
   question about the store's per-path history), and `unparam` is enabled for non-test files. The
   parameter is kept — same arity, same types, same call shape — and named `_` with a comment,
   which is precisely the precedent `emitToolGraph` set for its unused `ctx` in Commit 2.
2. **Stage constants live in `supersede.go`, not `observer.go`.** The task named `supersede.go`,
   `supersede_test.go` and `tooluse.go` step 8 as mine; `observer.go` is not mine. The three dotted
   names (`supersede.list`/`.root`/`.mark`) are steps of this one algorithm, so they are declared at
   the top of the file that reports them, with a comment saying why. Note that Commit 1 *did*
   pre-declare `counterSuperseded`/`counterNearDup` in `observer.go` and did **not** pre-declare
   stage names — so no pre-arranged home was displaced.
3. **Three tests beyond the table.** `TestSupersede_StoreFailuresAreSoft` (list/root/mark, three
   subtests) pins resolved decision 7 on this file's three I/O calls;
   `TestSupersede_NearDupCounterFollowsThePutResult` pins that `observer.neardup` follows
   `PutResult.NearDup` and not the supersession outcome; `TestIsSuperset_IgnoresMultiplicity` pins
   the "chunk *set*" reading. All three are behaviour the plan states in prose and no table row
   covers.
4. **Two property subtests, not one.** The plan's single row states two properties. They are
   `PropertyIsSupersetReflexive` and `PropertyIsSupersetOfUnion`; both names match the plans'
   `PropertyIsSuperset` pattern.
5. **`rapid` was present** (`pgregory.net/rapid v1.1.0` in `go.mod`), so it was used. No dependency
   was added; no deterministic-loop fallback was needed. House style from `internal/store/prop_test.go`
   was followed: `rt.Fatalf`, not testify, inside `rapid.Check`.
6. **`countEdgeKind`, not `countKind`.** `graph_test.go` already owns a `countKind(dag.Graph,
   dag.NodeKind)`; the new edge-side helper is named to not collide.

---

## 3. RED / GREEN evidence

### RED, stage 1 — the seam does not exist

`go test ./internal/observer/ -run 'Supersede|IsSuperset|Property' -count=1`:

```
internal\observer\supersede_test.go:98:15: h.obs.detectSupersession undefined (type *observer has no field or method detectSupersession)
internal\observer\supersede_test.go:473:19: undefined: isSuperset
FAIL	github.com/qompack/qompack/internal/observer [build failed]
```

### RED, stage 2 — against a stub returning `nil` / `false`

A compile failure is a weak RED, so a stub (`detectSupersession → nil`, `isSuperset → false`) was
put in place and the focused run repeated. 16 test/subtest failures on real assertions:

```
--- FAIL: TestSupersede_IdenticalRootMarksEarlier (0.01s)
--- FAIL: TestSupersede_SupersetChunkSet (0.00s)
--- FAIL: TestSupersede_NearDuplicateAboveThreshold (0.00s)
--- FAIL: TestSupersede_DifferentPathIgnored (0.00s)
--- FAIL: TestSupersede_SkipsAlreadySuperseded (0.00s)
--- FAIL: TestSupersede_LookbackCapped (0.00s)
--- FAIL: TestSupersede_StatusSurvivesReopen (1.63s)
--- FAIL: TestSupersede_EmitsNoEdgesItself (0.00s)
--- FAIL: TestSupersede_StoreFailuresAreSoft (0.00s)
    --- FAIL: TestSupersede_StoreFailuresAreSoft/List
    --- FAIL: TestSupersede_StoreFailuresAreSoft/Root
    --- FAIL: TestSupersede_StoreFailuresAreSoft/Mark
--- FAIL: TestSupersede_NearDupCounterFollowsThePutResult (0.00s)
--- FAIL: TestIsSuperset_IgnoresMultiplicity (0.00s)
--- FAIL: TestIsSuperset_Properties (0.00s)
    --- FAIL: TestIsSuperset_Properties/PropertyIsSupersetReflexive
    --- FAIL: TestIsSuperset_Properties/PropertyIsSupersetOfUnion
FAIL	github.com/qompack/qompack/internal/observer	3.512s   [exit 1]
```

Representative messages, i.e. the assertions were about behaviour and not about a compile error:

```
TestSupersede_EmitsNoEdgesItself: newest first
  expected: []core.ToolUseID{"toolu_2","toolu_1"}   actual: ([]core.ToolUseID) <nil>
TestIsSuperset_Properties/PropertyIsSupersetReflexive:
  [rapid] failed after 0 tests: isSuperset(x, x) is false for a 1-chunk set
  [rapid] draw x: []byte{0x0}
```

The rows that *passed* against the stub are exactly the negative ones (`_SubsetDoesNotSupersede`,
`_NearDuplicateBelowThreshold`, `_DifferentClassIgnored`, `_NeverMarksLaterRecord`,
`_EphemeralNeitherDirection`, `_ToolUsesByPathIsMostRecentFirst`, `TestIsSuperset_EmptyOlder`) —
which is the expected shape and is why the positive rows above carry the weight.

The `testdata/rapid/` failure files rapid wrote during the RED run were deleted; the working tree at
commit time held only the four intended files.

### GREEN

```
go test ./internal/observer/ -run 'Supersede|IsSuperset|Property' -count=1
  ok  github.com/qompack/qompack/internal/observer  1.888s   [exit 0]

go test -count=1 ./internal/observer/...
  ok  github.com/qompack/qompack/internal/observer            3.359s
  ok  github.com/qompack/qompack/internal/observer/observertest 1.895s   [exit 0]

go test -count=1 -cover ./internal/observer/
  coverage: 94.9% of statements   (floor is 75%)
```

The three plan `-run` rows this commit satisfies were each executed verbatim and each really
selects the property subtests (this is the point of the parent-function naming):

```
go test ./internal/observer/ -run 'TestSupersede|TestIsSuperset|PropertyIsSuperset' -count=1 -v
  --- PASS: TestIsSuperset_Properties/PropertyIsSupersetReflexive
  --- PASS: TestIsSuperset_Properties/PropertyIsSupersetOfUnion
go test ./internal/observer/ -run 'TestSupersede_|TestIsSuperset_|PropertyIsSupersetReflexive' -count=1 -v
  --- PASS: TestIsSuperset_Properties/PropertyIsSupersetReflexive
  --- PASS: TestIsSuperset_Properties/PropertyIsSupersetOfUnion
go test ./internal/observer/ -run 'TestSupersede' -count=1
  ok   [exit 0]
```

### Race

```
go test -race -count=1 -timeout=30m ./internal/observer/...
  ok  github.com/qompack/qompack/internal/observer            6.377s
  ok  github.com/qompack/qompack/internal/observer/observertest 2.521s   [exit 0]
```

Run twice (before and after the comment-only amend); clean both times, no co-load.

### Whole-repo suite

```
go run ./tools/devtool test    → exit 0 (all packages ok; test/guards 66s, test/integration 155s)
go run ./tools/devtool fmt     → exit 0, no files rewritten
go run ./tools/devtool vet     → exit 0
```

Additionally, because this commit adds work to the hot path, `go run ./tools/devtool bench-hotpath`
was run: exit 0, `B-A p99 = 2.048 ms` against its 15 ms limit `[PASS]`, `B-B [PASS]`, `B-E [PASS]`.
(B-C is still reported as not measured — the harness note predates this wave.)

---

## 4. Lint plan-gate: 14 → 11, no new entries

Every other `devtool lint` sub-check passes: `golangci-lint`, `nomagic`, `importgraph`, `testdeps`,
`bindeps`, `sleepcheck`, `stubskips`, `docmarkers`, `coveragefloors`.

**Cleared by this commit (3):**

| Was red | Pattern |
|---|---|
| `plans/V3-VERIFY-observer-and-negative-knowledge.md:388` | `TestSupersede\|TestIsSuperset\|PropertyIsSuperset` |
| `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:266` | `TestSupersede_\|TestIsSuperset_\|PropertyIsSupersetReflexive` |
| `plans/V6-VERIFY-production-readiness-and-uat.md:235` | `TestSupersede` |

**Remaining 11 accepted patterns** — all of them name `./internal/observer` tests that later commits
of this subplan own (Commits 4–6: `prompt.go`, `stop.go`, `session.go`):

1. `plans/V3-VERIFY-observer-and-negative-knowledge.md:391` — `TestOnUserPrompt|TestVerbatimPromptID`
2. `plans/V3-VERIFY-observer-and-negative-knowledge.md:392` — `TestOnStop|TestTailAssistantText`
3. `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:358` — `TestOnSessionStart_CompactDelegates|TestOnSessionStart_CompactWithoutRehydratorIsEmpty|TestOnSessionStart_ClearDelegates`
4. `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:360` — `TestOnSessionStart_StartupOpensSegment|TestOnSessionEnd_Order`
5. `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:361` — `TestOnUserPrompt|TestVerbatimPromptID`
6. `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:269` — `TestOnUserPrompt_|TestVerbatimPromptID`
7. `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:270` — `TestOnUserPrompt_Thrash`
8. `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:271` — `TestOnStop_|TestTailAssistantText_`
9. `plans/V6-VERIFY-production-readiness-and-uat.md:238` — `TestOnUserPrompt_NeverRegenerated`
10. `plans/V6-VERIFY-production-readiness-and-uat.md:239` — `TestOnStop_RetrievalPathG10_1`
11. `plans/V6-VERIFY-production-readiness-and-uat.md:241` — `TestOnSessionStart|TestOnSessionEnd`

No entry in the 11 is new; each was in the original 14.

### How the gate actually resolves a `PropertyIsSuperset…` row (worth recording)

`tools/devtool/planchecks.go` builds its name list from `go test -list .*`, which enumerates
**top-level functions only**, then matches the pattern's first `/`-separated element unanchored
against them. So a bare `func PropertyIsSupersetReflexive` would not exist as a test at all, and a
top-level `TestPropertyIsSupersetReflexive` would satisfy the *gate* while `go test -run
PropertyIsSuperset` at runtime still resolves only the head element. The construction that is
correct in **both** places is a parent `TestIsSuperset_Properties` (matched by the `TestIsSuperset`
and `TestIsSuperset_` alternatives of the two multi-alternative rows) with the properties as
subtests underneath it — which is what §3's `-v` transcripts above demonstrate actually running.

---

## 5. Files

Absolute paths, all inside the SP-08 worktree; nothing outside `internal/observer/**` was touched.

- `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08/internal/observer/supersede.go` (new)
- `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08/internal/observer/supersede_test.go` (new)
- `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08/internal/observer/tooluse.go` (step 8)
- `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08/internal/observer/fakes_test.go` (double)

`git show --stat`: 4 files changed, 761 insertions(+), 19 deletions(-). Working tree clean.

---

## 6. Self-review

- **Direction of `EdgeSupersedes`.** `TestSupersede_IdenticalRootMarksEarlier` runs against a REAL
  `dag.Graph` and asserts three things, not one: exactly one `EdgeSupersedes` in the whole graph,
  that it runs `tooluse:toolu_1 → tooluse:toolu_2`, and that the reverse edge is absent. That is
  SP-07 D-1 and the `graph-basic.jsonl` fixture's direction.
- **`MarkSuperseded` argument order.** Asserted as a whole-struct equality
  (`[]supersedeCall{{Older:"toolu_1", By:"toolu_2"}}`) rather than as two separate field checks, so
  a transposition cannot pass.
- **The chunk-set rows cannot pass by accident.** `rootOf` derives the root hash *from* the chunk
  list, so the `p.Root == rec.Root` fast path can never answer a row that is about `isSuperset`; and
  the near-duplicate rows use deliberately **disjoint** chunk sets so the superset arm cannot answer
  them either. `sigPair` makes the Jaccard exact (0.95, 0.80, 0.50 at 20 permutations), not
  approximate.
- **Tests assert behaviour, not the mock.** The two rows that are about a *contract* —
  `_StatusSurvivesReopen` and `_ToolUsesByPathIsMostRecentFirst` — run the real `store.Open` +
  `dag.Open`, and the reopen row asserts the flip survives `Flush`/`Close`/`Open`. The fake's
  newest-first ordering is therefore pinned against the real store rather than assumed, which is the
  drift guard the plan asked for.
- **`observer.superseded` non-zero in the real-store test.** `require.Equal(t, int64(1),
  metrics.Counter("observer.superseded").Value())` in `TestSupersede_StatusSurvivesReopen`, i.e. the
  checklist item is satisfied against the real store and not only against the double.
- **No hand-assembled NodeIDs, no `time.Now`, no new numeric literals.** `supersede.go` uses
  `supersessionLookback` and `thr` from config; `nomagic` passes. `dag.ToolUseNode` is used in tests
  only. `grep -n "time.Now" internal/observer/supersede.go` is empty.
- **Import allow-set.** `supersede.go` imports `context`, `internal/core`, `internal/store` only.
- **Ephemeral in both directions is genuinely tested.** The new-side case asserts
  `ByPathCalls == 0`, i.e. exclusion happens *before* the scan, not after it; the prior-side case
  stages an `Ephemeral: true` record with an identical root, which would otherwise be marked.
- **The `ByPathCalls == 0` obligation in `tooluse_test.go:176-179` is discharged.** It is no longer
  vacuous: the fake now records the call and the pipeline now makes it for non-ephemeral reads
  (`TestSupersede_DifferentPathIgnored` asserts `ByPathCalls == 2`), so the MCP row's zero is a real
  exclusion rather than the absence of a caller.
- **Failure paths.** All three soft stages are exercised and their counters asserted; `MarkErr`
  additionally asserts that a refused mark is not reported in `marked` and does not bump
  `observer.superseded`.

---

## 7. Concerns to carry forward

1. **A prior record with a zero `Root` costs a soft error on every later read of that path.**
   Step 6 of `onToolUse` writes an index record even for an empty result
   (`TestOnToolUse_EmptyResponseStillIndexed`), and that record's `Root` is the zero hash. The
   algorithm as specified then reaches `GetRoot(zeroHash)` for it on every subsequent non-empty read
   of the same path, which the real store answers with an error → `observer.err.supersede.root` is
   bumped and the candidate skipped. The *outcome* is correct (an empty prior is never a subset of
   anything), but the error counter is inflated by a boring, permanent condition, up to once per
   such prior per call. I did **not** add a seventh filter, because the task said to implement the
   brief's algorithm exactly and a `p.Root == (core.Hash{})` skip is a spec change. Suggested
   disposition: a one-line skip in a later commit, or a V3-VERIFY note that
   `observer.err.supersede.root` is not a clean signal.
2. **`fakes_test.go` is now a small real store.** Making the double faithful was necessary (see §1),
   but Commits 4–7 inherit a `fakeStore` whose `ToolUsesByPath`/`MarkSuperseded` have behaviour, and
   any test that seeds `Records` now also feeds the supersession scan. The mitigation in place is
   `TestSupersede_ToolUsesByPathIsMostRecentFirst`, which pins the one contract the double asserts
   about itself against SP-06's real implementation.
3. **`detectSupersession` ignores `sessionState`.** Deliberate and documented, but it means
   supersession answers the same question before and after a daemon restart only because it reads
   the store. If a later subplan wants a session-scoped variant, the parameter is already there.
4. **Hot-path cost is now `1 + n` index reads per tool use** (one `ToolUsesByPath`, up to 32
   `GetRoot`s). `GetRoot` is documented as answering from the in-memory index and exempt from the
   read budget, and `bench-hotpath` B-A still passes at 2.048 ms against 15 ms — but B-C, the
   observer's own budget row, is still reported as unmeasured by that harness, so this commit's
   contribution to B-C has not actually been gated by anything. Worth a V3-VERIFY row.

---
---

# Fix round — coordinator review of `5e87f6b`, three ruled changes

**Amended commit** `39d87ef` (same subject, same `Refs:` footer, body extended with one paragraph
per change). `5e87f6b` no longer exists on the branch; the branch is still one commit on `20a38e2`.
`git status` clean. Message re-validated with `check-commit-msg` (exit 0) and `git log -1
--format=%B` (no `Co-Authored-By` / `Signed-off-by` / `Generated with` / `Claude-Session` / emoji).

**Files touched this round:** `internal/observer/tooluse.go`, `internal/observer/supersede.go`,
`internal/observer/observer.go`, `internal/observer/supersede_test.go`,
`internal/observer/fakes_test.go` — the commit is now 5 files, +869 / −20.
`internal/observer/observer.go` joined the set because ruled change 1 explicitly directs the
rationale into the counter's declaration comment.

## Change 1 — `observer.neardup` moved from the scan to the Put

`detectSupersession` no longer counts it. `tooluse.go` gained step **5a**, immediately after the
step-5 `PutBytes`:

```go
if res.NearDup != nil && !ephemeral {
    o.count(counterNearDup)
}
```

No path gate: the bump happens for pathless content, which is the whole point. The ephemeral
exclusion is resolved decision 6 (retrieval is not exploration).

The rationale is recorded in two places. `observer.go`, on the counter's declaration:

> `counterNearDup` is bumped at the PUT (tooluse.go step 5a), NOT inside the supersession scan.
> The store's near-duplicate signal exists for PATHLESS content — Bash and test-runner output,
> which §8.1 item 1 names as the noisiest content class and the one where "the dedup ratio is
> won or lost" — and supersession returns early on an empty Path, so counting it there made this
> counter read ~0 for exactly the class it was meant to measure.

and on `detectSupersession` itself, which now states that it counts `observer.superseded` and
nothing else, and says where `observer.neardup` went.

**Tests.** `TestSupersede_NearDupCounterFollowsThePutResult` asserted the old placement and is
gone. Three rows replace it, all driven through `OnToolUse` rather than through a direct call:

| Test | Asserts |
|---|---|
| `TestOnToolUse_NearDupCounterCountsPathlessOutput` | a **Bash** result (`PathsFromInput` yields nothing, so `Records[0].Path == ""` and `ByPathCalls == 0`) with `PutResult.NearDup` set bumps the counter to 1 — this is the regression guard: under the old placement `detectSupersession` returned early on the empty path and the counter stayed 0 |
| `TestOnToolUse_NearDupCounterSkipsRetrieval` | an `mcp__qompack__` result with `NearDup` set bumps **nothing** |
| `TestOnToolUse_NearDupCounterSilentWithoutASignal` | a normal read with no `PutResult.NearDup` bumps nothing, while `observer.tombstone` confirms the pipeline actually ran (so the zero is not a silent no-op) |

`fakeStore` gained a `NearDup *store.NearDupInfo` field that `PutBytes` returns, so the signal can
be staged at the point the store would really produce it.

## Change 2 — seventh filter: a prior with a zero `Root`

Added to the loop, after the class check and **before** the `p.Root == rec.Root` fast path and the
`GetRoot` call:

```go
if p.Root == (core.Hash{}) {
    continue
}
```

with a comment tying it to the two facts that make it correct: step 6 of `onToolUse` indexes a
record before it knows whether anything was stored, so zero-`Root` rows are legitimate index
content; and skipping them is the same rule `isSuperset` already applies to an empty `older` chunk
list. This is exactly concern 1 of the original report, now closed rather than carried.

`TestSupersede_ZeroRootPriorSkipped` seeds a zero-`Root` prior on the path and asserts all three
consequences: `h.Store.GetRoots` is **empty** (the lookup never happens, so the skip is before it
and not after), `observer.err.supersede.root` is **0**, and nothing is marked. `fakeStore.GetRoot`
gained a `GetRoots []core.Hash` recorder to make the first of those assertable.

## Change 3 — `TestSupersede_NearDupSubsetStillSupersedes`

New chunk set is a strict **subset** of the prior's ({c1,c2} against {c1,c2,c3}) while the
signatures sit at Jaccard 0.95 against the 0.9 threshold; the earlier record **is** marked. The
test opens by asserting `isSuperset(newer.Chunks, older.Chunks) == false`, so the containment arm is
demonstrably not what answered it.

The comment cites §8.1 item 3's wording — "a superset **or** near-duplicate of a prior read" — and
states that the two arms are independent and that the near-duplicate arm is deliberate about
later-read-wins even when the newer read is smaller. It also names
`TestSupersede_SubsetDoesNotSupersede` as its converse: same chunk relation, Jaccard 0.50, not
marked. The pair is what pins that containment and similarity have not been collapsed into one
test.

## Re-run evidence (all at `39d87ef`)

```
go test ./internal/observer/ -run 'Supersede|IsSuperset|NearDup|OnToolUse' -count=1
  ok  github.com/qompack/qompack/internal/observer  2.547s   [exit 0]
  -v: 52 PASS lines, 0 FAIL — includes TestSupersede_ZeroRootPriorSkipped,
      TestSupersede_NearDupSubsetStillSupersedes, the three TestOnToolUse_NearDupCounter* rows,
      and both PropertyIsSuperset* subtests

go test -count=1 ./internal/observer/...
  ok  github.com/qompack/qompack/internal/observer            3.037s
  ok  github.com/qompack/qompack/internal/observer/observertest 1.610s   [exit 0]

go test -race -count=1 -timeout=30m ./internal/observer/...
  ok  github.com/qompack/qompack/internal/observer            6.779s
  ok  github.com/qompack/qompack/internal/observer/observertest 2.836s   [exit 0]

go test -count=1 -cover ./internal/observer/
  coverage: 95.0% of statements   (was 94.9%; floor 75%)

go run ./tools/devtool fmt   -> exit 0, nothing rewritten
go run ./tools/devtool vet   -> exit 0
go run ./tools/devtool lint  -> golangci-lint, nomagic, importgraph, testdeps, bindeps,
                                sleepcheck, stubskips, docmarkers, coveragefloors all PASS;
                                runpatterns FAIL with exactly 11 unsatisfiable patterns
```

The 11 are byte-for-byte the same list as §4 of the original report — the four `TestOnUserPrompt*`,
three `TestOnStop*` and four `TestOnSessionStart/End*` rows owned by Commits 4–6. No new entry, and
none of the three cleared rows regressed.

## Concerns after this round

Concern 1 of the original report (**zero-`Root` prior inflating `observer.err.supersede.root`**) is
**closed** by ruled change 2. Concerns 2, 3 and 4 stand unchanged:

- `fakes_test.go` is now a small real store, and it grew again this round (`NearDup`, `GetRoots`);
  Commits 4–7 inherit it. Still pinned against SP-06 by
  `TestSupersede_ToolUsesByPathIsMostRecentFirst`.
- `detectSupersession` still ignores its `sessionState` (named `_`, `unparam` is on).
- B-C, the observer's own budget row, is still reported as unmeasured by `bench-hotpath`, so this
  commit's added hot-path work is gated by nothing. `bench-hotpath` was **not** re-run this round —
  the three changes remove one counter call from a loop, add one integer comparison to it, and add
  one nil check after the Put, none of which can plausibly move a budget that passed at 2.048 ms
  against a 15 ms limit. Worth a V3-VERIFY row regardless.

One new, minor note: `observer.neardup` and `observer.superseded` are now incremented from
different files (`tooluse.go` and `supersede.go`), which is correct but means a reader looking for
"where the near-dup counter is bumped" will not find it beside the supersession code the plan's
pseudocode put it next to. Both the counter declaration in `observer.go` and the
`detectSupersession` doc comment say so explicitly, so the pointer exists in both directions.

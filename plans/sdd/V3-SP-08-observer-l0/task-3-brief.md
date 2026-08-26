# Task brief — Commit 3: supersession and near-duplicate redundancy detection

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject unchanged (52 chars). This commit wires `OnToolUse` step 8 (replace the nil placeholder left by Commit 2).
- `store.ArgsDigest` is package-level; reading content by root in tests is `st.GetRoot(ctx, root)` / `st.Open(ctx, root) (io.ReadCloser, error)`; the package constructor is `store.Open(root, cfg, deps)`.


---
<!-- plan lines 93-114 -->

### §8.1 — L0 Observer, verbatim

> **Trigger:** `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`
>
> **Responsibilities:**
>
> 1. **Chunk and store.** Run FastCDC over the tool result. Suggested parameters for source text: `min = 1KB`, `target = 4KB`, `max = 16KB` — smaller than backup workloads because source files are smaller. Store novel chunks zstd-compressed; record the chunk list.
>    **Canonicalize first (O2).** Exact-hash dedup is defeated by volatile substrings: timestamps, ANSI escape codes, PIDs, memory addresses, temp-dir paths, and run durations make every `Bash` and test-runner output unique even when semantically identical. Before chunking, apply per-tool canonicalizers that strip or normalize these (store the canonical form; keep the volatile deltas as a tiny side record if byte-exact recovery matters). For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text. Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost.
> 2. **Emit the tombstone.** Replace the eventual cleared marker with an addressable one:
>    ```
>    [cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]
>    ```
>    This alone closes G3.2 at near-zero cost.
> 3. **Redundancy detection.** If the chunk set is a superset or near-duplicate of a prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the first candidates for eviction and should never appear in a summary.
> 4. **DAG edges.** Record `tool_use → tool_result → assistant_turn → next_tool_use`, plus shared-state edges keyed on file path and symbol name.
> 5. **Sketch updates.** Feed Count-Min and HyperLogLog. Feed the Bloom filter *only* on explicit negative-knowledge events (§8.3).
> 6. **Sequitur.** Append the tool symbol to the action grammar; check for high-multiplicity nonterminals and emit a thrash warning.
> 7. **Verbatim user capture.** Every `UserPromptSubmit` is written immutably to `index/segments.jsonl`. This is the durable version of section 6 of the summary prompt, and unlike section 6 it is never regenerated.
> 8. **Subagent capture.** On `SubagentStop`, store the subagent's returned summary *and*, where available, its tool-result hashes, so the parent has a retrieval path into detail it never held (G10.1).
>
> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.


---
<!-- plan lines 198-253 -->

### Inherited constraints from wave 1 (rows of `plans/CARRIED-DEFECTS.tsv` still `deferred:V3-VERIFY` — binding on this subplan, **not** quoted from `Qompack.md`)

Three of the wave-1 rows deferred to V3-VERIFY do not merely sit still while wave 2 is built on top
of them: they compound with everything SP-08 persists and with every exactness it asserts. The
authoritative status of each is its row in `plans/CARRIED-DEFECTS.tsv` — not this file — with the
diagnosis and acceptance criteria in `plans/V2-SP-04-carried-defects.md` and
`plans/V2-WAVE1-carried-defects.md`; V3-VERIFY §0a item 5 owns resolving or consciously re-deferring
them. What follows is not background reading. Each item is a constraint on what this subplan may do.

1. **SP04-D2 + SP04-D3 — canonicalization is not a stable contract yet, so no durable identity SP-08
   mints may assume that it is.** SP04-D2: canonicalization is not idempotent when a deletion joins
   two fragments into a value neither half contained. SP04-D3: one timestamp edge remains of the
   original three — a word byte immediately after an ISO timestamp defeats the rule. Both are
   deferred by decision rather than by oversight: the complete fix changes the `canon.Delta` contract
   that SP-06's content-addressed store stores against, and it is legal only under fixed-point
   composition, so D3 travels with D2. The evidence test is `TestKnownDeletionMediatedLimit`, whose
   third row is the BOM counterexample (commit 666b120). **The constraint on SP-08:** everything this
   subplan persists or content-addresses must stay re-derivable, or explicitly versioned, across a
   canonicalization change. A canonical-form hash may not be baked into a durable identity, an
   equality check, or a key that SP-08 cannot recompute once V3-VERIFY lands the fix. That reaches the
   `Root` written into every `ToolUseRecord`, the `(TS, Root)` pairs `AppendFileVersion` appends,
   `Node.Root` on every DAG node, the `Signature`/`NearDup` comparison supersession turns on, and the
   short hash the tombstone prints — every one of them an address over the *canonical* bytes, not the
   raw ones. Re-derivability is the cheap answer: let the store's own `Root` be the only identity, and
   mint no second one on top of it that only this package can read.

2. **SP04-D5 — the canonicalization rule count is hot-path budget, and there is none left to spend
   quietly.** Cost is now dominated by per-rule prefilter scans, so each new rule spends the budget
   linearly. Re-measured on a quiet machine at V2-VERIFY: `BenchmarkRun_GoTest` at 786.5 µs against
   its 1 ms budget row (21% headroom), and `BenchmarkRun_Bash100KB` at 2.612–5.103 ms, straddling its
   3 ms budget row. The deferral was confirmed by measurement, not assumed. **The constraint on
   SP-08:** this subplan may not add a canonicalization rule without re-measuring both benchmarks
   against their budget rows in the same change. The temptation is specific enough to name: the
   Phase 1 exit criterion is a dedup ratio measured with and without canonicalization, and a new strip
   class is the obvious way to buy ratio on test output. If a rule is genuinely required, the per-rule
   prefilter restructure has to come first — and since the canonicalizer registry and its strip
   classes are SP-04's under **Out of scope**, "add a rule" is a cross-subplan request, never a local
   edit.

3. **SP05-D1 — the drain is not lossless on abort, so the observer may not assume it is fed
   everything.** A drain aborted by idle-budget expiry consumes the line it interrupted: the file
   offset is advanced and the seen-set committed before the binding completed, so the event is lost.
   The deliberate poison-line-consume rule cannot distinguish a dying drain from a refusing handler,
   and separating them needs seen-set rollback, or post-dispatch commit plus a retry cap — a
   re-adjudication of an adjudicated rule rather than a surgical fix, which is why it is a
   checkpoint's and not SP-05's. **The constraint on SP-08:** "the drain delivers everything" is not
   an available assumption. Any counter, ledger or invariant this subplan needs to be *exact* either
   gets its own reconciliation path, or is documented as best-effort at the point it is declared. It
   lands on `state.Turn` and `state.PrefixTokens` — resolved decisions 4 and 5 make both monotone
   counters of events *observed*, not of events that happened — on the `ToolUses` ring and the
   `SubagentSince` cursor `OnStop` slices from, on supersession (a read the observer never saw cannot
   supersede an earlier one), and on the Phase 1 exit criterion itself, whose ≥ 4:1
   `Stats().DedupRatio` is a ratio over what arrived. `doc.go` names which of these are best-effort;
   none of them may claim an exactness the transport does not provide.

---

---
<!-- plan lines 640-724 -->

### Resolved decisions (read these before writing code; no decision below is open)

1. **Pipeline order.** §8.1 item 1 says "canonicalize first"; §5.22a says redaction is applied at
   the single choke point `store.Put`/`PutBytes`, *before* canonicalization and chunking. Both are
   satisfied because the observer never chunks, redacts, or canonicalizes by hand: it calls
   `store.PutBytes` with `PutOptions{Tool, Path, Canon}`, and SP-06's store performs
   redact → canonicalize → chunk internally. The observer's contribution to O2 is **per-tool
   canonicalizer selection**, delivered as `PutOptions.Tool` and `PutOptions.Path` (which is what
   `canon.Registry.For(tool, path)` dispatches on) and `PutOptions.Canon.Strip`. The observer's own
   ordering, after the Put returns, is: **index → file version → sketches → DAG → grammar →
   signals/features → tombstone.**
2. **`crlf` is unconditional.** §4 of the architecture: "Content entering the store is CRLF→LF
   normalized by the `crlf` canonicalizer before chunking." `canonOptions` therefore always
   includes `canon.Class("crlf")`, even when `store.canonicalize.enabled` is `false`. The
   with/without-canonicalization measurement of the Phase 1 exit criterion therefore compares
   *crlf-only* against *crlf + the six configured classes*, which is the honest A/B.
3. **§8.1 item 7's destination.** The design names `index/segments.jsonl`. In this architecture the
   segment log is `store.SegmentLog` (SP-06) and carries no per-turn payload field, and W-3 forbids
   adding one. The verbatim requirement is met with three durable artifacts, none of which is ever
   regenerated from a summary: (a) the prompt bytes stored as a content-addressed object via
   `store.PutBytes` with **no optional canonicalization class and no MinHash** (pre-step (a) above
   is what makes that request reach the store at all — see `prompt.go` for exactly how far
   "verbatim" reaches); (b) an append-only `tool_use.jsonl` entry with `Tool: "UserPromptSubmit"`
   and `ID: VerbatimPromptID(...)`; (c) a `KindUserPrompt` DAG node built by `dag.BuildUserPrompt`
   and enrolled in the currently open segment by an `EdgeSequence` running
   `userprompt:<turn> → segment:<id>` — members point **into** the segment, per SP-07 D-1 and
   `dag.BuildSegment`. This is what closes G2.3: the bytes are content-addressed, immutable, and
   reachable by hash forever.
4. **Turn accounting.** `state.Turn` starts at 0. `OnUserPrompt` records at `Turn` then increments.
   `OnToolUse` records at the current `Turn` without incrementing. `OnStop` increments in **both**
   directions — `subagent=false` because the assistant turn has ended, `subagent=true` because the
   capture itself occupies a turn slot (it is recorded at the pre-increment `Turn`, exactly like a
   prompt). Result: a monotone alternating user/assistant turn sequence in which every stored
   artifact carries the turn it was observed at.
5. **`Node.Pos`.** The observer maintains `state.PrefixTokens`, a monotone per-session token
   counter. Every node is created with `Pos = state.PrefixTokens` (its *start* position), then
   `state.PrefixTokens += node.Tokens`. Prompts, tool results and subagent captures all advance it.
6. **Ephemeral.** A tool whose normalized name starts with `mcp__qompack__` is a retrieval result:
   it is recorded with `Ephemeral: true`, is excluded from supersession (in both directions), and
   is not fed to the CMS/HLL/Misra-Gries (retrieval is not exploration). It still gets DAG nodes
   and a tombstone.
7. **Error policy.** No I/O failure ever escapes an `Observer` method. Every stage is wrapped by
   `o.soft(stage, err)`, which increments `obs.Counter("observer.err."+stage)`, logs at `Warn`, and
   returns. The methods return a non-nil error **only** for `ctx.Err()`. Every method returns
   `hookio.Empty()` unless it has a specific reason to emit output — exactly two exist:
   `OnUserPrompt` may emit a thrash warning, and only in `ModeFull`; and `OnSessionStart` returns
   verbatim whatever the `Rehydrator` seam returns for `source == "compact"` or `"clear"`.
   Counters are incremented through the `obs.Counter` value returned by
   `Registry.Counter(name)` using the increment method SP-01 defined on that interface; wrap every
   counter bump in the single helper `func (o *observer) count(name string)`, which is nil-safe on
   `o.opt.Metrics` and is the only place in the package that names an `obs` method — so adapting to
   SP-01's exact spelling (`Inc()` vs `Add(1)`) is a one-line change.
8. **Nil tolerance.** `Grammar`, `Touch`, `Explore`, `Hot`, `Symbols`, `Rehydrate`, `Mode`,
   `OnSignals`, `OnFeatures` and `Metrics` may each be nil; every call site guards. `New` returns an
   error only when `ProjectRoot == ""`, `Store == nil`, `Graph == nil`, `Log == nil`, or
   `Clock == nil`.
9. **Concurrency (two-level locking).** The daemon's worker pool may deliver two events for
   *different* sessions concurrently, so every entry point is written to be race-free under
   `go test -race`. `o.mu` guards **only** the `sess` map and the `stateFile` write; it is held for
   the map lookup/insert and released immediately. Each `sessionState` carries its own
   `mu sync.Mutex`, and every entry point takes it for the remainder of the method, so all work for
   one session is serialized and all mutation of `Turn`, `PrefixTokens`, `Recent`, `ToolUses`,
   `LastTS`, `TodoDone` and `WarnedRules` happens under it. Store, DAG, grammar and sketch calls are
   made while holding the session lock — SP-06/SP-07/SP-03 own their own internal synchronization,
   and one session is inherently sequential in the host anyway. Never take `o.mu` while holding a
   `sessionState.mu`; the shape is always map-lock → copy pointer → map-unlock → session-lock.
10. **Mode gates output, never writes.** §12 (`ModeDegradedPassive`): "L0 and L1 keep running
    (observe, chunk, store, sketches, DAG, verbatim capture …)". Therefore `o.mode()` is consulted
    in exactly one place — the `AdditionalContext` emission in `prompt.go` — and never guards a
    `PutBytes`, `RecordToolUse`, `AddNode`, `AddEdge` or sketch update. A test
    (`TestModePassiveStillWrites`) drives a full session with `Mode() == ModePassive` and asserts
    the store/DAG/sketch call counts are identical to `ModeFull`.
11. **Time.** Every timestamp in this package comes from
    `now := core.UnixMilli(o.opt.Clock.Now().UnixMilli())`, computed once at the top of each entry
    point (after the ctx check) and reused for the record, the DAG nodes and the feature sample, so
    one hook firing has exactly one timestamp. `time.Now()` never appears in `internal/observer`.
12. **`canon.Options.MinHash` type.** §5.6 declares the field as `MinHash MinHashOptions` inside a
    package that imports `sketch` (§3.2), and §5.7 declares `sketch.MinHashOptions`. Write
    `sketch.MinHashOptions{…}` at the call site. If SP-04 shipped a package-local alias
    (`type MinHashOptions = sketch.MinHashOptions`) the literal still compiles unchanged; if SP-04
    shipped a *distinct* struct, that is a §5 divergence and the response is an `arch/` amendment
    request, not a local workaround (§0).

---


---
<!-- plan lines 1146-1227 -->

### `internal/observer/supersede.go` (new)

**Responsibility.** §8.1 item 3, verbatim: *"If the chunk set is a superset or near-duplicate of a
prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the
first candidates for eviction and should never appear in a summary."*

```go
// Returns the ids it marked SUPERSEDED, most recent first. It writes to the STORE only and emits no
// DAG edges: EdgeSupersedes is part of the §8.1 item 4 edge set dag.BuildToolUse owns, and graph.go
// hands marked[0] to it as ObservedTool.Supersedes (see `graph.go` below).
func (o *observer) detectSupersession(ctx context.Context, st *sessionState,
    rec store.ToolUseRecord, res store.PutResult) (marked []core.ToolUseID)
func isSuperset(newer, older []core.ChunkRef) bool
```

Algorithm:

```
if rec.Path == "" || rec.Ephemeral || supersedableClass(rec.Tool) == "" { return nil }
prior, err := Store.ToolUsesByPath(ctx, rec.Path, supersessionLookback)
if err != nil { o.soft("supersede.list", err); return nil }
newSet := set of res.Root.Chunks[i].Hash
thr := o.opt.Cfg.Store.Canonicalize.MinHash.NearDupThreshold
for _, p := range prior {            // ToolUsesByPath returns most-recent-first (§5.8)
    superseded := false
    if p.ID == rec.ID { continue }
    if p.Status == store.StatusSuperseded { continue }      // already handled
    if p.TS > rec.TS { continue }                            // only ever mark EARLIER reads
    if p.Ephemeral { continue }                              // ephemeral results are already first-evicted
    if supersedableClass(p.Tool) != supersedableClass(rec.Tool) { continue }
    if p.Root == rec.Root { superseded = true }              // identical content, later read wins
    else {
        pr, err := Store.GetRoot(ctx, p.Root); if err != nil { o.soft("supersede.root", err); continue }
        superseded = isSuperset(res.Root.Chunks, pr.Chunks) ||
                     (thr > 0 && rec.Signature.IsNearDup(p.Signature, thr))
    }
    if !superseded { continue }
    if err := Store.MarkSuperseded(ctx, p.ID, rec.ID); err != nil { o.soft("supersede.mark", err); continue }
    o.count("observer.superseded")
    marked = append(marked, p.ID)                            // no AddEdge here — see graph.go
}
if res.NearDup != nil { o.count("observer.neardup") }
return marked
```

**`ToolUsesByPath` ordering.** §5.8 does not state an order in the signature, so this file states the
contract it relies on: SP-06's `tool_use.jsonl` is append-only and `ToolUsesByPath(path, limit)`
returns the **most recent `limit` records, newest first** (`FSStore.ToolUsesByPath` walks its
per-path id list backwards). `marked` therefore comes back newest-first too, which is what makes
`marked[0]` the right value for `ObservedTool.Supersedes`. The loop itself is order-independent —
every candidate is filtered by `p.TS <= rec.TS` — so an oldest-first store would change only which
end of `marked` graph.go reads. `TestSupersede_ToolUsesByPathIsMostRecentFirst` pins the order
against the real store so a drift is caught in wave 2, not wave 3.

`isSuperset(newer, older)` returns `false` when `len(older) == 0`, otherwise `true` iff every hash
in `older` is present in the `newer` set. Multiplicity is ignored (a chunk-hash set, per the design's
"chunk set"), so a file read twice in one result does not defeat it.

**Direction of `EdgeSupersedes`** is fixed by SP-07 D-1 and SP-08 does not get a vote: *"every edge
points from earlier/producer to later/consumer. There is no exception. `EdgeSupersedes` runs
superseded → superseding"* (`internal/dag/builders.go`). So From = the **superseded (older)** node,
To = the **superseding (newer)** node — `tooluse:<older> → tooluse:<newer>` — which is what
`BuildToolUse` emits from `ObservedTool.Supersedes`, what `builders_test.go` pins in both
directions, and what the frozen contract fixture `testdata/golden/contracts/dag/graph-basic.jsonl`
carries as `{"from":"tooluse:toolu_01ABCdef","to":"tooluse:toolu_02GHIjkl","kind":5}` — the bytes
its `MANIFEST.json` declares that "SP-08, SP-09 and SP-12 assert against".

Getting this backwards is not cosmetic: `EdgeSupersedes` carries the 0.30 multiplier of
`internal/dag/kinds.go`, so in the correct direction a backward slice from the current read reaches
the superseded one at a heavily discounted score — §8.1 item 3's "first candidate for eviction,
never summarized". Reversed, the superseded read becomes an *upstream* source of full-strength
relevance for everything the new read explains, which is the opposite of the intended ranking.

This is stated here because both SP-12 (eviction ranking) and SP-15 (redundancy) traverse it.

**"Should never appear in a summary"** is enforced structurally rather than by an observer-side
predicate, because `checkpoint` does not import `observer` (§3.2): the fact lives on
`store.ToolUseRecord.Status == store.StatusSuperseded` and on the `EdgeSupersedes` edge, both of
which SP-10 and SP-15 already read. A test asserts the status survives a store close/reopen.

---


---
<!-- plan lines 2061-2081 -->

### `supersede_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestSupersede_IdenticalRootMarksEarlier` | same file read twice, identical bytes | `MarkSuperseded(older, newer)` called once; exactly one `EdgeSupersedes`, running **older → newer** (`dag.ToolUseNode(older) → dag.ToolUseNode(newer)`, D-1), and **no** edge in the reverse direction |
| `TestSupersede_SupersetChunkSet` | read A = chunks {c1,c2}; read B = {c1,c2,c3} | B supersedes A |
| `TestSupersede_SubsetDoesNotSupersede` | read A = {c1,c2,c3}; read B = {c1,c2} with Jaccard below threshold | zero `MarkSuperseded` |
| `TestSupersede_NearDuplicateAboveThreshold` | signatures with Jaccard 0.95, threshold 0.9 | supersedes |
| `TestSupersede_NearDuplicateBelowThreshold` | Jaccard 0.80, threshold 0.9 | does not supersede |
| `TestSupersede_DifferentPathIgnored` | reads of `a.ts` then `b.ts` | zero `MarkSuperseded` |
| `TestSupersede_DifferentClassIgnored` | `Grep` on `a.ts` then `Read` of `a.ts` with a superset chunk set | zero `MarkSuperseded` |
| `TestSupersede_NeverMarksLaterRecord` | prior record with `TS` greater than the new one | zero `MarkSuperseded` |
| `TestSupersede_SkipsAlreadySuperseded` | three reads of one path | second call marks only the unmarked one; total `MarkSuperseded` == 2 across the run |
| `TestSupersede_EphemeralNeitherDirection` | ephemeral MCP result then a real read; and the reverse | zero `MarkSuperseded` in both orders |
| `TestSupersede_LookbackCapped` | 100 prior records | `ToolUsesByPath` called with `limit == 32` |
| `TestSupersede_StatusSurvivesReopen` | real store, mark, `Close`, `Open`, `ToolUse(older)` | `Status == store.StatusSuperseded`, `SupersededBy == newer` |
| `TestSupersede_ToolUsesByPathIsMostRecentFirst` | real store; three reads of one path | `ToolUsesByPath(path, 32)` returns the three records newest-first — the §5.8 ordering `marked[0]` (and therefore `ObservedTool.Supersedes`) relies on |
| `TestSupersede_EmitsNoEdgesItself` | fakeGraph; a read that supersedes two priors | `detectSupersession` adds **zero** edges; after `emitToolGraph` the graph holds exactly two `EdgeSupersedes` edges, both older→newer, one of them contributed by `dag.BuildToolUse` |
| `TestIsSuperset_EmptyOlder` | `older == nil` | `false` |
| `PropertyIsSupersetReflexive` (rapid) | random chunk sets | `isSuperset(x, x) == true`; `isSuperset(x∪y, x) == true` |


---
<!-- plan lines 2440-2448 -->

### Commit 3 — `feat(observer): supersession and near-duplicate redundancy detection`

- [ ] Write `supersede_test.go` (all 14 rows, including the rapid property test and the
      reopen-persistence test against the real SP-06 store). Confirm failure.
- [ ] Add `supersede.go`; wire step 8 of the `OnToolUse` algorithm.
- [ ] `go run ./tools/devtool test` — green; `observer.superseded` counter asserted non-zero in the
      real-store test.
- [ ] Footer: `Refs: SP-08, §8.1 item 3, §5.8 MarkSuperseded`


# SP-08: L0 observer: PostToolUse, UserPromptSubmit, Stop/SubagentStop, SessionStart/SessionEnd entry points, addressable tombstones, supersession, verbatim capture, and the Phase 1 exit criterion

> **Recommended model: Opus 5 · xhigh effort**
>
> Five hook entry points and a lot of wiring on top of primitives that already exist and are already tested. Wide, not deep — the Phase 1 exit criterion (≥4:1 dedup, p99 < 15 ms) is measured against machinery SP-04/05/06 already built.

**Branch:** `feat/sp08-observer-l0` (cut from `develop`) | **Wave:** 2 | **Prerequisites:** the branches of `["SP-01","SP-03","SP-04","SP-05","SP-06","SP-07"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 2 (SP-09 negative knowledge) | **Design sections:** §7.2 L0, §7.3 (PostToolUse, UserPromptSubmit, Stop/SubagentStop, SessionStart startup/resume, SessionEnd), §8.1 items 2,3,4,5,7,8, §10 Phase 1 (exit criterion) | **Gaps closed:** G1.5, G2.3, G3.2, G10.1

---

## Mission

This slice is layer **L0 — Observer**: the write path's front door. Every fact Qompack will ever
know enters the system through one of five entry points implemented here — `PostToolUse`,
`UserPromptSubmit`, `Stop`/`SubagentStop`, `SessionStart` (the `startup`/`resume` branch), and
`SessionEnd`. Wave 1 built the machinery: a content-addressed store, a chunker, a canonicalizer
registry, five sketches, a dependence DAG, and a resident daemon that can answer a hook in under
15 ms. None of it is fed by anything yet. SP-08 is the code that feeds it, and it is the last
subplan of Phase 1.

Why it exists in the design: §1.3 names **RC-1, "destructive rather than demotive"** — compaction
deletes from context without leaving a retrieval path. The observer is the mechanism that makes
demotion possible at all. It canonicalizes and stores every compactable tool result so that
clearing it later is lossless; it renders the addressable tombstone of §8.1 item 2 so a cleared
result still says *what* it was and *how to get it back* (G3.2); it detects when a later read
supersedes an earlier one so the earlier one can be evicted first and never summarized (§8.1
item 3); it records the DAG edges that make slicing possible (item 4); it feeds Count-Min and
HyperLogLog while deliberately **not** feeding the Bloom filter, which §8.1 item 5 reserves for
explicit negative-knowledge events; it writes the user's prompt **verbatim and immutably**, which
is the durable version of section 6 of Claude Code's summary prompt and, unlike section 6, is
never regenerated (item 7, G2.3); and on `SubagentStop` it stores a subagent's returned summary
together with its tool-result hashes so the parent gains a retrieval path into detail it never
held (item 8, G10.1). It also extracts the task-boundary signals — todo completion, passing test
run, git commit — that §3 G1.5 says are "all natural safe points; none are wired to compaction",
and hands them to the scheduler.

**What exists in the repo when you start.** `develop` contains SP-01's foundation (`core`,
`paths`, `config`, `logging`, `obs`, `hookio`, `cli`, `tokens`, `testutil`, `test/e2e`
scaffolding, the plugin manifest, CI, and a compiling `ErrNotImplemented` stub plus a
`<pkg>test` conformance suite for every §5 interface); SP-03's real `internal/sketch`; SP-04's
real `internal/chunk`, `internal/canon`, `internal/symbols`; SP-05's real `internal/ipc` and
`internal/daemon` with its op-routing table, `IdleController`, late-bound `Services`, hot-path
budget machinery and `internal/contract`; SP-06's real `internal/store` and `internal/redact`;
SP-07's real `internal/dag`. `internal/observer` is a stub only **in part**: the five `Observer`
methods return `core.ErrNotImplemented` and `ExtractSignals` returns the zero `Signals`, but
`Tombstone` and its `humanBytes` helper **ship real and tested** — §8.1 item 2 fully specifies the
rendered form, so SP-01 implemented it rather than stubbing it (`internal/observer/tombstone.go`
and `tombstone_test.go`, four passing tests including the GB tier). `internal/observer/observertest`
also ships, with `RunObserverSuite(t, name, factory)` and a `/behaviour` block guarded by
`skipIfStub`, so that guard lifts by itself the moment `observer.New` stops returning
`core.ErrNotImplemented`. `internal/grammar` is still an SP-01 stub (SP-15, wave 4) and
`internal/negknow` is being built by SP-09 in this same wave — the observer must tolerate both
being inert.

**What exists when you finish.** `internal/observer` is real and is the sole writer of that
package. SP-05's existing `observe.tool`, `observe.prompt`, `observe.stop`, `session.start` and
`flush` routes — WAL append, ACK ordering, spool submode and terminal marker intact — now find the
five `Services` seams bound, through `internal/daemon/observer_ops.go` (the one file SP-08 adds
outside its own package, exactly as SP-12 later adds `scheduler_runtime.go` there; the two
pre-step edits below live on a separate `arch/` branch). A read-heavy session drives a store whose
`Stats().DedupRatio` is at or above 4:1, measured with and without canonicalization, and the
hot-path bench-gate still passes B-A p99 < 15 ms on all three platforms with the observer wired in.
The `observertest` conformance suite's `/behaviour` block runs instead of skipping. Phase 1 is
closed.

---

## Design context (verbatim from `Qompack.md`)

### §7.2 — layer diagram, L0 row

```
├─────────────────────────────────────────────────────────────────┤
│ L0  OBSERVER              PostToolUse · UserPromptSubmit ·       │
│                           SessionStart · SessionEnd · Stop       │
└─────────────────────────────────────────────────────────────────┘
```

> Data flows up on the write path (L0 → L1 → L2), down on the read path (L3 → L4 → L5 → context).
> L6 is a lateral affordance available to the agent at any time. L7 is offline.

### §7.3 — hook surface (the six rows this subplan touches; the `PreCompact` row is SP-10's)

| Hook | Layer | Responsibility |
|---|---|---|
| `PostToolUse` | L0 | Chunk and store tool results; update DAG, sketches, Sequitur; detect redundancy |
| `UserPromptSubmit` | L0 | Capture user intent **verbatim and immutably** (closes G2.3); update BOCD features |
| `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |
| `PostToolUse` (todo/git) | L3 | Task-boundary signals for the scheduler |
| `Stop` / `SubagentStop` | L0 | Capture subagent detail before it is double-compressed (closes G10.1) |
| `SessionEnd` | L1 | Flush, compact the store, write session index |

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

### §2.2 — the compactable tool set (what a tombstone may replace)

```
FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
```

> Only high-volume, reproducible results are targeted. AgentTool and MCP results are preserved.

### §8.2 — the store facts L0 must maintain

> **File version history.** `index/files.json` maps path → list of `(timestamp, root_hash)`. This gives cheap answers to "what did this file look like when we made that decision," which is the most common thing lost across compaction.
>
> **Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.

### §6.6 — the cheap features the observer must emit

> Bayesian online changepoint detection maintains a distribution over run length since the last changepoint, updated in O(1) amortized with pruning. Run it over cheap features:
>
> - File-path locality (Jaccard over recently-touched paths)
> - Tool-type distribution shift
> - Lexical cohesion (TextTiling-style)
> - Inter-turn time gaps
> - Todo-list state transitions

### §3 gap rows this slice closes

| ID | Gap |
|---|---|
| G1.5 | **No task-boundary signals consulted.** Todo completion, passing test runs, git commits are all natural safe points; none are wired to compaction. |
| G2.3 | **Section 6 ("All user messages") is regenerated, not preserved.** It drifts along with everything else despite existing precisely to prevent drift. |
| G3.2 | **Tombstones carry no pointers.** `[Old tool result content cleared]` says a tool ran and nothing else — even though `FileRead`, `Grep`, and `Glob` results are trivially reproducible and `Bash` output is in the session log. |
| G10.1 | **Subagent output is compressed twice.** A subagent returns a summary; that summary is then summarized. The parent never held the detail, so there is no recovery at either level. |

### §9 traceability rows

| Gap | Closed by | Residual |
|---|---|---|
| G1.5 no boundary signals | L0 todo/git/test signals → L3 | — |
| G2.3 user messages regenerated | L0 verbatim capture, L5 verbatim replay | — |
| G3.2 pointerless tombstones | L0 addressable tombstone | — |
| G10.1 subagent double-compression | L0 `SubagentStop` capture | — |

### §10 Phase 1 — the exit criterion this subplan is graded on

> ### Phase 1 — Store and observer
>
> - FastCDC chunker, content-addressed object store, zstd
> - Per-tool output canonicalizers (timestamps, ANSI, PIDs, addresses) + MinHash near-dedup (O2)
> - `PostToolUse` and `UserPromptSubmit` hooks
> - Merkle index, file version history
> - Addressable tombstones (G3.2)
> - Redundancy / supersession detection
>
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

### §11.3 — guardrails

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### Appendix C — the configuration keys this subplan reads (verbatim)

```jsonc
  "store": {
    "chunk": { "min": 1024, "target": 4096, "max": 16384 },
    "compression": "zstd",
    "retention": { "days": 30, "sessions": 10 },
    "canonicalize": {
      "enabled": true,
      "strip": ["timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"],
      "minhash": { "enabled": true, "permutations": 128, "nearDupThreshold": 0.9 }
    }
  },
```

### Degradation rows binding on L0 (from `00-ARCHITECTURE.md` §12.1 and §12.3, which implement `Qompack.md` §12's risk register — **not** quoted from `Qompack.md`)

> `ModeDegradedPassive` behaviour: L0 and L1 keep running (observe, chunk, store, sketches, DAG, verbatim capture, elimination records — the store stays correct and the session's data is not lost). Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`, no scheduler-initiated checkpoints, no drop report.

> | any hook panic | recovered in `cli`, logged, `exit 0` with empty output |

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

## Out of scope

Each item names the sibling subplan that owns it. Do not implement any of these.

| Out of scope | Owner |
|---|---|
| FastCDC itself, `chunk.Split`, `chunk.RootHash`, gear hash parameters | SP-04 |
| The canonicalizer registry, the seven strip classes, the per-tool rules, `canon.Restore`, MinHash signature computation | SP-04 |
| `internal/symbols` — symbol extraction itself (SP-08 consumes it through an adapter) | SP-04 |
| Bloom / CMS / HLL / Misra-Gries / MinHash implementations, sizing formulas, serialization, `ResizeTarget`, `RebuildBloom` | SP-03 |
| Object layout, zstd, redaction at ingest, `roots.jsonl`, `tool_use.jsonl` and `files.json` **mechanics**, `SegmentLog` implementation, `MarkEncoded`, GC **mechanics**, `Stats`, `tokens.EstimateRoot` | SP-06 |
| The DAG's storage, `BackwardSlice`/`ForwardSlice`, `CrossingEdges`, `Compact`, the §8.1 item 4 builders (`BuildToolUse`, `BuildUserPrompt`, `BuildSegment`) and the D-2 NodeID constructors — SP-08 **calls** all of these and re-implements none of them | SP-07 |
| The thin hook client, IPC transport, framing, ACK, spool fallback, the B-A budget histogram, the sync→spool submode transition, `qompack session-start` dispatch, daemon start, `contract.Monitor.RunAll` at session start, `test/bench/hotpath` | SP-05 |
| Writing to `tried.bloom`, the elimination ledger, canonical descriptors, staleness, the three-way `already_tried` answer, heuristic elimination detection over the DAG | SP-09 |
| `SessionStart(source=compact)` and `source=clear` **semantics** — SP-08 owns only the `source` switch and delegates through the `observer.Rehydrator` seam declared here | SP-11 |
| The `PreCompact` hook, checkpoint writing, `ExtractDecisions`, focus instructions, pins | SP-10 |
| Closing a segment on a **changepoint**, frontier advancement (O5), BOCD itself, the composite trigger, p-selection, droppable-block classification and eviction ordering | SP-12 |
| Sequitur's algorithm, the two grammar invariants, thrash-warning **policy** and high-multiplicity detection (SP-08 only appends symbols and forwards whatever `Thrash` returns) | SP-15 |
| MCP tools, ephemeral-at-birth tagging on the retrieval side, `Promoter` | SP-13 |
| `/qompack:status` rendering | SP-14 |
| `eval.Synthesize`, `eval.SynthSpec` (SP-08 consumes it and adds no field to it), the 24-session synthetic corpus, the replay gate, Belady OPT, divergence metrics | SP-02 |
| Cross-platform packaging, the security audit, `fsck`/`doctor` | SP-17 |

---

## Interface contract

### Consumes (exact signatures from `00-ARCHITECTURE.md` §5; do not change any of them)

```go
// internal/hookio (§5.3) — SP-01
type Event struct {
    HookEventName  string          `json:"hook_event_name"`
    SessionID      core.SessionID  `json:"session_id"`
    TranscriptPath string          `json:"transcript_path"`
    CWD            string          `json:"cwd"`
    Source         string          `json:"source"`   // SessionStart: startup|resume|compact|clear
    Trigger        string          `json:"trigger"`
    ToolName       string          `json:"tool_name"`
    ToolUseID      core.ToolUseID  `json:"tool_use_id"`
    ToolInput      json.RawMessage `json:"tool_input"`
    ToolResponse   json.RawMessage `json:"tool_response"`
    Prompt         string          `json:"prompt"`
    StopHookActive bool            `json:"stop_hook_active"`
    Extra          map[string]json.RawMessage `json:"-"`
}
type Output struct {
    Continue           *bool  `json:"continue,omitempty"`
    SuppressOutput     *bool  `json:"suppressOutput,omitempty"`
    HookSpecificOutput *HSO   `json:"hookSpecificOutput,omitempty"`
    SystemMessage      string `json:"systemMessage,omitempty"`
}
type HSO struct {
    HookEventName      string `json:"hookEventName"`
    AdditionalContext  string `json:"additionalContext,omitempty"`
    CustomInstructions string `json:"customInstructions,omitempty"`
}
func Empty() Output

// internal/store (§5.8) — SP-06
Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error)
PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
GetRoot(ctx context.Context, root core.Hash) (Root, error)
RecordToolUse(ctx context.Context, rec ToolUseRecord) error
ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error
ArgsDigest(raw json.RawMessage) (core.Hash, string)   // §5.8: "SP-08 calls this; nothing else may
                                                      // re-derive it" — canonical-JSON digest under
                                                      // core.DomainArgs + the ≤120-byte preview
AppendFileVersion(ctx context.Context, path string, v FileVersion) error
FileHistory(ctx context.Context, path string) ([]FileVersion, error)
Segments() SegmentLog
Stats(ctx context.Context) (Stats, error)
GC(ctx context.Context, p GCPolicy) (GCReport, error)
Flush(ctx context.Context) error
// plus the value types: PutOptions{Tool, Path, Canon, KeepRaw, Ephemeral},
// PutResult{Root, Novel, Reused, Signature, NearDup}, NearDupInfo{PriorRoot, Jaccard, DeltaBytes},
// Root{Hash, Chunks, CanonBytes, RawBytes, Tokens}, Supersession{StatusOK, StatusSuperseded},
// ToolUseRecord{ID, Session, Turn, TS, Tool, ArgsDigest, ArgsPreview, Root, Path, Bytes, Tokens,
//               Signature, Status, SupersededBy, Ephemeral, Subagent},
// FileVersion{TS, Root, Turn, Bytes}, GCPolicy{RetainDays, RetainSessions, DryRun, Deadline},
// Stats{Objects, Bytes, RawBytes, DedupRatio, ToolUses, Segments, Files, Sketches}

// internal/store SegmentLog (§5.8) — SP-06
Open(ctx context.Context, s Segment) (core.SegmentID, error)
Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
Current(ctx context.Context, s core.SessionID) (Segment, error)
Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)

// internal/dag (§5.9) — SP-07
AddNode(n Node) error
AddEdge(e Edge) error
Flush(ctx context.Context) error
// Node{ID, Kind, Turn, TS, Pos, Ref, Root, Tokens, Ephemeral}; Edge{From, To, Kind, Weight, Turn}
// NodeKind: KindToolUse KindToolResult KindAssistant KindUserPrompt KindFile KindSymbol
//           KindDecision KindElimination KindSegment
// EdgeKind: EdgeSequence EdgeProduces EdgeConsumes EdgeSharedFile EdgeSharedSymbol
//           EdgeSupersedes EdgeExplains EdgeControlOnly

// internal/dag builders (§8.1 item 4) — SP-07. builders.go: "the one place in the repository that
// turns an observation of the transcript into nodes and edges", and ObservedTool's own doc names
// "observer (§5.7)" as its only production caller. SP-08 emits the item-4 chain ONLY through these.
func BuildToolUse(g Graph, o ObservedTool) error
func BuildUserPrompt(g Graph, o ObservedPrompt) error
func BuildSegment(g Graph, s SegmentSpec) error
type ObservedTool struct {
    ToolUseID, PrevToolUseID, Supersedes core.ToolUseID
    PrevTurn, Turn core.TurnIndex
    TS core.UnixMilli
    Pos, ResultPos int
    Tool, PathKey  string
    Writes         bool
    Symbols        []string          // resolved by the CALLER; BuildToolUse sorts and dedupes
    Root           core.Hash
    Tokens         core.Tokens
    Ephemeral      bool
}
type ObservedPrompt struct{ Turn core.TurnIndex; TS core.UnixMilli; Pos int; Tokens core.Tokens; Ref string }
type SegmentSpec struct {
    ID, PrevID core.SegmentID
    StartTurn, EndTurn core.TurnIndex
    TS core.UnixMilli
    StartPos int
    Tokens core.Tokens
    Members []NodeID
}

// internal/dag NodeID constructors (D-2) — SP-07. nodeid.go: "Every consumer builds IDs through
// the constructors below rather than concatenating strings … One component spelling a file node
// differently from another does not fail loudly — it silently produces two disconnected halves of
// the same graph." internal/observer NEVER assembles a NodeID from a string.
func ToolUseNode(id core.ToolUseID) NodeID      // "tooluse:<id>"
func ToolResultNode(id core.ToolUseID) NodeID   // "toolresult:<id>"
func AssistantNode(t core.TurnIndex) NodeID     // "assistant:<decimal turn>" — NO session component
func UserPromptNode(t core.TurnIndex) NodeID    // "userprompt:<decimal turn>" — NO session component
func FileNode(pathKey string) NodeID            // "file:<pathKey>"; does NOT re-fold through paths.Key
func SymbolNode(pathKey, name string) NodeID    // "symbol:<pathKey>#<name>"
func SegmentNode(id core.SegmentID) NodeID      // "segment:<decimal id>"

// internal/sketch (§5.7) — SP-03
func (c *CMS) Add(key []byte, n uint32)
func (h *HLL) Add(key []byte)
func (m *MisraGries) Add(key string, n int)
func (s Signature) IsNearDup(o Signature, threshold float64) bool
func Save(p string, s Sketch) error
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }

// internal/canon (§5.6) — SP-04
type Class string
type Options struct{ Strip []Class; KeepDeltas bool; MinHash MinHashOptions }

// internal/grammar (§5.11) — SP-15 (stub in wave 2; must be tolerated)
Append(s Symbol); Rules() []Rule; Thrash(minUses int) []Rule
func FormatWarning(w Warning) string

// internal/tokens (§5.20) — SP-01/SP-06
EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
EstimateString(s string, c Class) core.Tokens
func Classify(tool, path string, b []byte) Class

// internal/core (§4), internal/paths, internal/config, internal/logging, internal/obs
func HashBytes(domain string, b []byte) Hash
func (h Hash) String() string; func (h Hash) Short() string
func Norm(projectRoot, p string) (string, error); func Key(p string) string
func WriteAtomic(p string, b []byte) error
func (c Config) Get(dotted string) (any, bool)
Logger.Debug/Info/Warn/Error/Loud(msg string, kv ...any)
Registry.Hist(name string) Histogram; Registry.Counter(name string) Counter
```

### Produces (relied on by later subplans)

```go
// package observer — §5.21, normative and unchanged
type Observer interface {
    OnToolUse(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnUserPrompt(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnStop(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error)
    OnSessionStart(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnSessionEnd(ctx context.Context, e hookio.Event) (hookio.Output, error)
}
func Tombstone(rec store.ToolUseRecord) string
type Signals struct{ TodoCompleted, TestPassed, GitCommit bool; Paths []string }
func ExtractSignals(e hookio.Event) Signals

// package observer — additions SP-08 makes inside the package it owns
type Mode uint8
const (ModeFull Mode = iota; ModePassive)

type SymbolLister interface{ Names(path string, b []byte) []string }

type Rehydrator interface {                              // implemented by SP-11 in wave 3
    OnCompact(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnClear(ctx context.Context, e hookio.Event) (hookio.Output, error)
}

type FeatureSample struct {                              // daemon maps this to scheduler.Features
    Turn            core.TurnIndex
    TS              core.UnixMilli
    PathJaccard     float64
    ToolShift       float64
    LexicalCohesion float64
    GapSeconds      float64
    TodoTransition  float64
}

type TestOutcome uint8
const (TestUnknown TestOutcome = iota; TestPass; TestFail)
func ExtractTestOutcome(e hookio.Event) TestOutcome
func PathsFromInput(tool string, in json.RawMessage) []string
func NormalizeToolName(hostName string) string           // "Read" → "FileRead"
func IsCompactable(tool string) bool                     // §2.2 set
func TombstoneNote() string                              // the expand affordance line

type SubagentToolRef struct {
    ToolUseID core.ToolUseID `json:"tool_use_id"`
    Root      string         `json:"root"`
    Tool      string         `json:"tool"`
    Path      string         `json:"path"`
    Bytes     int64          `json:"bytes"`
}
type SubagentCapture struct {
    Session     core.SessionID    `json:"session"`
    Agent       string            `json:"agent"`
    Turn        core.TurnIndex    `json:"turn"`
    TS          core.UnixMilli    `json:"ts"`
    Summary     string            `json:"summary"`
    ToolResults []SubagentToolRef `json:"tool_results"`
}
func VerbatimPromptID(s core.SessionID, t core.TurnIndex) core.ToolUseID  // "prompt_<s>_<t>"
func SubagentCaptureID(s core.SessionID, t core.TurnIndex) core.ToolUseID // "subagent_<s>_<t>"

type Options struct {
    ProjectRoot string
    Cfg         config.Config
    Store       store.Store
    Graph       dag.Graph
    Grammar     grammar.Sequitur
    Touch       *sketch.CMS
    Explore     *sketch.HLL
    Hot         *sketch.MisraGries
    Tokens      tokens.Estimator
    Symbols     SymbolLister
    Rehydrate   Rehydrator
    Mode        func() Mode
    OnSignals   func(core.SessionID, Signals)
    OnFeatures  func(core.SessionID, FeatureSample)
    Log         logging.Logger
    Metrics     obs.Registry
    Clock       core.Clock
}
func New(o Options) (Observer, error)

// Persister lets the daemon's idle loop checkpoint observer state. The value returned by New
// ALWAYS satisfies it; observer_ops.go performs the single guarded assertion in the codebase
// (`p, ok := obsv.(Persister)`) and skips the idle registration when ok is false, so a future
// alternate Observer implementation cannot panic the daemon.
type Persister interface{ Persist(ctx context.Context) error }
```

**Import discipline (§3.2).** The §3.2 allow-table *permits* `observer` to import
`hookio store chunk canon sketch dag grammar negknow tokens` plus the foundation, but a permission
is a ceiling rather than an instruction: SP-08 declines two of them — `chunk`, because the observer
never chunks by hand (resolved decision 1), and `negknow`, because §8.1 item 5 forbids L0 from
touching the Bloom filter — and it never had `symbols` (hence `SymbolLister`), `scheduler` or
`checkpoint` (hence `Signals`/`FeatureSample` and the callbacks), or `contract` (hence
`observer.Mode`). The **binding** statement is the exit criterion at the end of this document: the
realized import set is exactly `core paths config logging obs hookio store canon sketch dag grammar
tokens` plus stdlib.

---

## Implementation spec

### Pre-step: the `arch/sp08-observer-seams` amendment (lands on `develop` before this branch)

Three shipped behaviours block SP-08 as written, and §0's amendment rule is explicit that the
response is a branch against the architecture rather than a local workaround: *"If an interface in §5 is
wrong, you do not work around it. You open a branch `arch/<short-reason>` off `develop`, change §5,
get it merged, and rebase."* Cut `arch/sp08-observer-seams` off `develop`, land it, then cut
`feat/sp08-observer-l0` from the result. It is small, and every part is spelled out here so the
implementer does not have to improvise.

**(a) `store.PutOptions.Canon` must be able to say "no optional classes".** Today
`FSStore.canonOptions` (`internal/store/put.go`) honours the caller's strip list only when it is
non-empty — `if len(o.Canon.Strip) > 0 { opts.Strip = o.Canon.Strip }` — and never reads
`o.Canon.MinHash` or `o.Canon.KeepDeltas` at all, so a caller asking for "nothing optional" silently
inherits the store's configured six classes *and* the store's MinHash setting. `internal/canon`
already draws exactly the distinction the store is dropping: `gateSet` treats a **nil** `Strip` as
"every class" and a **non-nil but empty** `Strip` as "exactly these — i.e. none — plus the always-on
structural ones" (`internal/canon/classes.go`). The amendment makes the store agree:

- change the override test to `if o.Canon.Strip != nil { opts.Strip = o.Canon.Strip }`, so an empty
  non-nil slice means "no optional class" and a nil slice still means "use the store's config";
- honour a caller-supplied opt-out on MinHash, gated by that same `Strip != nil` test: when
  `o.Canon.Strip != nil` **and** `o.Canon.MinHash.Enabled` is false, the returned options carry
  `MinHash.Enabled = false` regardless of `store.canonicalize.minhash.enabled`. The gate is
  load-bearing, not decoration. `sketch.MinHashOptions.Enabled` is a plain `bool` and
  `PutOptions.Canon` is a value field, so an explicit `false` is byte-identical to the zero value:
  an ungated rule would read every `store.PutOptions{}` in the tree as an opt-out, zero every
  `PutResult.Signature`, and silently retire `FSStore.nearDup` — §8.1 item 3's redundancy detector,
  whose output `supersede.go` depends on. `Strip` is the one field that *can* say "unset", so it
  carries the whole decision: a **nil** `Strip` means "I supplied no per-call canon override at
  all", a **non-nil** `Strip` means "this entire `canon.Options` is mine, MinHash included". Both of
  SP-08's call sites pass a non-nil `Strip` (`prompt.go`'s `verbatimOptions()` and `tooluse.go`'s
  `canonOptions()`), so the gate costs this subplan nothing. The reverse direction stays
  config-wins — a caller may turn the signature *off* for one Put, never on, because the permutation
  count and threshold are configuration the caller does not own;
- add the matching note to `plans/00-ARCHITECTURE.md` §5.8 under `PutOptions`: a nil `Canon.Strip`
  means "no per-call override at all — the store's configured classes *and* the store's MinHash
  setting", an empty non-nil `Canon.Strip` means "no optional class",
  `Canon.MinHash.Enabled == false` disables the signature for that one Put **only when
  `Canon.Strip` is non-nil**, and `Canon.KeepDeltas` remains derived from `PutOptions.KeepRaw`
  rather than read from `Canon`;
- add `TestCanonOptions_EmptyStripMeansNoOptionalClasses` in `internal/store`: a body carrying a
  timestamp and an ANSI escape, Put once with
  `Canon: canon.Options{Strip: []canon.Class{}, MinHash: sketch.MinHashOptions{Enabled: false}}` and
  once with `Canon: canon.Options{}`, against a store whose config has the six classes enabled. The
  first must come back with a zero `PutResult.Signature` and a `Root.CanonBytes` equal to the input
  length after CRLF/path normalization only; the second must not. The second Put is also the row
  that pins the gate: its `MinHash.Enabled` is the zero `false` too, and its `Signature` must still
  be non-zero, because its `Strip` is nil and a nil `Strip` is not an opt-out.

**(b) the subagent's name must survive the IPC boundary.** `hookio.Event.Extra` is
`map[string]json.RawMessage` tagged `json:"-"`: `hookio.ReadEvent` fills it in the *hook client*
process and `ipc.EncodeRequest` then drops it, so a daemon-side reader of `e.Extra` sees an empty
map in production and every subagent capture would be named `"subagent"`. Two edits fix it:

- `internal/cli/hookclient.go`'s `rawExtras` already builds `{"subagent":true}` for
  `observe stop --subagent`. Extend it to resolve the agent's name **client-side**, where `Extra` is
  real — the first of `Extra["subagent_type"]`, `Extra["agent_name"]`, `Extra["agent"]` that
  `json.Unmarshal`s into a non-empty string — and emit `{"subagent":true,"agent":"<name>"}`. A
  numeric or object value falls through to the next key; all three missing emits the existing
  `{"subagent":true}` byte-for-byte, so the wire format stays backward-compatible and
  `decodeSubagent` is untouched.
- `internal/daemon/handlers.go`'s `resolveEvent` re-populates `Event.Extra` from `req.Raw` when
  `req.Raw` decodes as a JSON object, so what the client parsed reaches every bound `Services` seam.
  This is a restoration rather than a new channel: `Extra` is already §5.3's documented home for a
  hook payload's unclaimed keys, and it is the only field of `hookio.Event` the transport silently
  empties.
- add `TestResolveEvent_RestoresRawExtras` in `internal/daemon`, and extend `internal/ipc`'s
  `TestDecodeRequestRoundTrip` with a request whose `Raw` carries the agent name.

**(c) the contract mode must be reachable from a bound function.** §12.1 gives the observer two
behaviours — `ModeFull` acts, `ModeDegradedPassive` records but does not — and §8.1 item 7's
thrash warning is gated on the first. The mode lives on the `contract.Monitor`, and nothing SP-08
can hold reaches one. `contract.NewMonitor` is called *inside* `daemon.New`
(`internal/daemon/daemon.go`, after the bind loop) into the unexported `d.monitor` field; it is on
neither `Options`, nor `Services`, nor the `Daemon` interface. `WireObserver` must return before
`New` is called at all, so there is no ordering that lets it dereference a monitor. Three edits fix
it:

- add `Mode func() contract.Mode` to `Services` in `internal/daemon/options.go`. It is a **provided**
  seam, the inverse direction to the nine consumed ones beside it: SP-05 fills it in, a bound
  function reads it. Rule W-3 is satisfied — the struct is widened, nothing is renamed or
  re-typed — but the struct belongs to SP-05, which is why the edit belongs on this branch and not
  on `feat/sp08-observer-l0`;
- in `internal/daemon/daemon.go`, hoist the two `statePath` / `contract.NewMonitor` lines above the
  `for _, bind := range o.binds` loop and assign `svc.Mode = monitor.Mode` before it runs. The hoist
  is safe because `NewMonitor` reads only `o.ProjectRoot`, `o.Log` and `o.Metrics`, none of which a
  bind produces; it is *necessary* because a bind body that captures `s.Mode` before the assignment
  captures nil and reports `ModePassive` for the process's whole life — a fully silent failure, since
  a passive observer still records and still exits 0;
- add the matching `Mode` line and its two-direction note to `plans/00-ARCHITECTURE.md` §5.4, and
  `TestServicesModeIsAssignedBeforeBinds` in `internal/daemon`: a bind body that captures `s.Mode`,
  a `daemon.New` over a temp project, and an assertion that the captured func is non-nil and returns
  `contract.ModeFull` on a fresh state directory. Asserting inside the bind body instead would pass
  vacuously — the monitor has not read `state/contract.json` yet at that point and answers `ModeFull`
  for every project, including a degraded one.

**Why an amendment rather than a workaround.** (a) cannot be worked around at all — nothing outside
`internal/store` can reach `FSStore.canonOptions` — and working (b) around by registering a `Handle`
route would replace SP-05's WAL-before-ACK path outright (see `internal/daemon/observer_ops.go`
below). (c) has two workarounds and both are worse than the amendment: constructing a second
`contract.NewMonitor` over the same `state/contract.json` gives the observer a monitor whose mode
diverges from the daemon's the moment either degrades, and reading the state file directly duplicates
§12.1's parsing in a package that §3.2 forbids from importing `contract` for anything else. All three
are exactly the "an interface in §5 is wrong" case §0 names.

The amendment is a separate branch and a separate merge; it does **not** count against SP-08's own
5–8 commit band, and `git rev-list --count develop..feat/sp08-observer-l0` is still 7 afterwards.

---

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

### `internal/observer/doc.go` (exists — SP-01's package doc; SP-08 rewrites it)

Package documentation stating layer L0, the §8.1 responsibility list, the sole-writer rule from
§5.21 ("No subplan other than SP-08 writes code in `internal/observer`"), and resolved decisions
1–12 above in comment form.

---

### `internal/observer/observer.go` (exists — SP-01's `Observer`/`Options`/`New`/`stubObserver`)

**Responsibility.** `Options`, `New`, the concrete type, per-session state map, mode gating, error
policy, metric names, `Persist`. SP-01's `Event`/`Output` aliases and the `Observer` interface are
unchanged (Rule W-3); `Options` is *widened* with the fields below rather than renamed, and
`stubObserver` is replaced by the real type.

```go
type observer struct {
    opt   Options
    mu    sync.Mutex                        // guards sess and the state-file write ONLY (decision 9)
    sess  map[core.SessionID]*sessionState
    once  sync.Once                         // loadState runs at most once per process
    maxResultBytes int                      // runtime.hotPath.maxPayloadBytes, read in New
    stateFile string          // <root>/.qompack/state/observer.json
}

func New(o Options) (Observer, error)                   // return value ALWAYS satisfies Persister
func (o *observer) Persist(ctx context.Context) error   // satisfies Persister
func (o *observer) mode() Mode                          // ModeFull when o.opt.Mode == nil
func (o *observer) now() core.UnixMilli                 // decision 11; the only Clock call site
func (o *observer) soft(stage string, err error)        // logs Warn + count("observer.err."+stage)
func (o *observer) count(name string)                   // nil-safe counter bump; only obs call site
func (o *observer) session(s core.SessionID) *sessionState // takes/releases o.mu, returns locked-by-caller state
```

`session(s)` is the only accessor: it locks `o.mu`, creates the entry on first use (with non-nil
`TodoDone`/`WarnedRules` maps), unlocks `o.mu`, and returns the pointer. Every entry point then does
`st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()` before touching any field
(decision 9).

`sessionState` (in-memory, mirrored to disk by `state.go`):

```go
type sessionState struct {
    mu            sync.Mutex     // decision 9: serializes all work for one session
    Turn          core.TurnIndex
    PrefixTokens  int
    Segment       core.SegmentID
    PrevSegment   core.SegmentID // the segment before Segment, or 0 — dag.SegmentSpec.PrevID
    SegStartTurn  core.TurnIndex // dag.SegmentSpec.StartTurn
    SegStartPos   int            // PrefixTokens when Segment opened — dag.SegmentSpec.StartPos
    LastTS        core.UnixMilli // TS of the PREVIOUS event; updated last, after features() runs
    LastToolUseID core.ToolUseID  // "" before the first tool use — dag.ObservedTool.PrevToolUseID
    LastToolUseTurn core.TurnIndex // its turn — dag.ObservedTool.PrevTurn
    LastPromptTurn core.TurnIndex
    SubagentSince int            // index into ToolUses at the last SubagentStop/user prompt
    Recent        []recentEvent  // ring, cap 2*featureWindow
    TodoDone      map[string]bool
    WarnedRules   map[grammar.RuleID]bool
    PendingThrash []grammar.Rule // collected in OnToolUse, drained by OnUserPrompt
    ToolUses      []toolUseLite  // ring, cap subagentWindowCap
}
type recentEvent struct{ Tool string; Paths []string; Text []byte; TS core.UnixMilli }
type toolUseLite struct{ ID core.ToolUseID; Root core.Hash; Tool, Path string; Bytes int64 }
```

`SubagentSince` indexes `ToolUses`, which is front-evicted at `subagentWindowCap`. Every eviction of
`k` entries therefore does `st.SubagentSince = max(0, st.SubagentSince-k)` in the same statement —
otherwise a long subagent run would slice past the end of the ring.

`LastToolUseID` and `LastToolUseTurn` are carried as the raw `core.ToolUseID` and turn rather than
as `dag.NodeID`s, because they are handed to `dag.BuildToolUse` as
`ObservedTool.PrevToolUseID`/`PrevTurn` and the builder mints the NodeIDs itself. Keeping the pair
— rather than a single "previous node" — is what lets the builder apply its `PrevTurn < Turn` guard,
which is the whole defence against the parallel-sibling cycle (see `graph.go`). The three `Seg*`
fields exist for the same reason on the segment side: `dag.SegmentSpec` needs `PrevID`, `StartTurn`
and `StartPos` at *close* time, and none of them is recoverable from `store.Segment`.

Constants (all annotated because §11.6's `nomagic` pass is on):

```go
const (
    featureWindow      = 8   // tool uses per BOCD comparison window (§6.6)
    maxSymbolsPerResult = 64
    symbolScanCap      = 256 << 10 // bytes; symbol extraction is skipped above this
    supersessionLookback = 32      // prior tool uses per path examined by supersede.go
    subagentWindowCap  = 256       // tool uses retained for SubagentStop linkage
    thrashMinUses      = 3
    cohesionTokenCap   = 4000
    gcDeadline         = 8 * time.Second // SessionEnd hook timeout is 20 s (§3.4 manifest)
)
```

Metric names registered on `o.opt.Metrics`: histograms `observer.tooluse`, `observer.prompt`,
`observer.stop`, `observer.session_start`, `observer.session_end`; counters
`observer.superseded`, `observer.neardup`, `observer.tombstone`, `observer.subagent_capture`,
`observer.signal.todo`, `observer.signal.test`, `observer.signal.git`, and `observer.err.<stage>`.
Every method body is wrapped as `obs.Timed(o.opt.Metrics.Hist("observer.<name>"), func() error {…})`
when `Metrics != nil`.

---

### `internal/observer/tombstone.go` (exists — SP-01 shipped `Tombstone` and `humanBytes`)

**Responsibility.** §8.1 item 2 in its exact rendered form, plus tool-name normalization and the
compactable-tool-set predicate of §2.2. The renderer is not written from scratch: SP-08 extends the
one SP-01 already shipped, and leaves `humanBytes` alone.

**Grammar of the marker.** Rendered from `store.ToolUseRecord` with no I/O:

```
[cleared: sha256:<12 hex>… · <size> · <ToolDisplay> <subject> · re-expandable]
```

- separator is `" · "` (space, MIDDLE DOT, space) — matching the design's `·`;
- ellipsis is `…`;
- `<12 hex>` is `rec.Root.Short()` (§4: first 12 hex chars); the design's `a3f2…` is an elision in
  prose, 12 hex is the architecture's canonical short form and is what the golden file records;
- `<size>` is `humanBytes(rec.Bytes)`, which **already ships and is not changed by SP-08**:
  `const bytesPerKB = 1024 //nomagic:allow binary byte-unit divisor for size rendering, not
  store.chunk.min`, `var sizeUnits = [...]string{"KB", "MB", "GB"}`, sizes below the divisor
  rendered as `"%dB"` and everything above it as `"%.1f%s"` scaled through KB → MB → **GB**. The GB
  tier is load-bearing: `TestHumanBytes_UsesTheBinaryDivisor` pins `1.0GB` and `3.0GB`, and SP-08
  may not narrow the renderer to B/KB/MB. 2458 bytes renders `2.4KB`, matching the design example;
- `<ToolDisplay>` is `NormalizeToolName(rec.Tool)`;
- `<subject>` is `rec.Path` when non-empty, else `rec.ArgsPreview` truncated to 48 runes with `…`;
  when both are empty the ` <subject>` group and its leading space are omitted entirely;
- when `rec.Status == store.StatusSuperseded`, ` · superseded` is inserted immediately before
  ` · re-expandable`;
- when `rec.Ephemeral`, ` · ephemeral` is inserted in the same position, before any `superseded`.

**What commit 1 actually changes.** `internal/observer/tombstone.go` already renders
`fmt.Sprintf("[cleared: sha256:%s · %s · %s %s · re-expandable]", rec.Root.Short(),
humanBytes(rec.Bytes), rec.Tool, rec.Path)`. SP-08 adds, to that existing function: the `…` after
the short hash; `NormalizeToolName(rec.Tool)` in place of the raw `rec.Tool`; the `ArgsPreview`
fallback subject and the no-subject elision (which removes today's double space on a pathless
record); and the ` · ephemeral` / ` · superseded` segments. `humanBytes` is untouched.

Two shipped tests are therefore *updated*, not written:

- `TestTombstone_RendersTheSection81Form` — its pinned string gains the ellipsis and becomes
  `[cleared: sha256:a3f2c9e14b70… · 2.4KB · FileRead src/auth.ts · re-expandable]`. `FileRead`
  already survives `NormalizeToolName` unchanged, so the ellipsis is the only delta.
- `TestHumanBytes_UsesTheBinaryDivisor` — **left exactly as it is**, GB rows and negative-size row
  included. If a change to `tombstone.go` makes it fail, the change is wrong.

`TestTombstone_IsAddressable` and `TestTombstone_HandlesAnEmptyRecord` also already ship and keep
passing; `TestTombstone_NoSubject` in the test plan below is the tightened successor to the latter
and replaces it.

```go
func Tombstone(rec store.ToolUseRecord) string
func TombstoneNote() string
```

`TombstoneNote()` returns exactly, on one line:

```
Cleared tool results above are re-expandable: call the qompack MCP tool expand with the sha256 shown in the marker, or with the tool_use_id.
```

**Tool tables.**

```go
var hostToDisplay = map[string]string{
    "Read": "FileRead", "NotebookRead": "FileRead", "Edit": "FileEdit",
    "MultiEdit": "FileEdit", "NotebookEdit": "FileEdit", "Write": "FileWrite",
    "Bash": "Bash", "BashOutput": "Bash", "PowerShell": "PowerShell",
    "Grep": "Grep", "Glob": "Glob", "WebSearch": "WebSearch", "WebFetch": "WebFetch",
    "Task": "AgentTool", "TodoWrite": "TodoWrite",
}
func NormalizeToolName(hostName string) string  // table hit, else hostName unchanged
func IsCompactable(tool string) bool            // display name ∈ §2.2 set
func supersedableClass(tool string) string      // "filecontent" | "search" | "exec" | "web" | ""
```

`IsCompactable` returns true for exactly `FileRead, Bash, PowerShell, Grep, Glob, WebSearch,
WebFetch, FileEdit, FileWrite` — the §2.2 list, nine display names because §2.2 writes
`Bash/PowerShell` as one row — and false for `AgentTool`, `TodoWrite`, and every `mcp__…` name,
matching "AgentTool and MCP results are preserved."

**Where these two exported functions are actually used** (so neither is dead code):

- `IsCompactable` gates the `observer.tombstone` counter in step 14 of `OnToolUse` — the counter
  measures *how many results the host may later clear*, which is the only honest denominator for
  G3.2 — and it is the predicate SP-11 (rehydration) and SP-13 (`expand`) call to decide whether a
  record may legally be represented by a marker at all. It reports **Claude Code's** §2.2 host set
  and nothing else. Qompack's own retrieval results are governed by the §8.7 ephemeral rule
  instead, carried on `store.ToolUseRecord.Ephemeral`, which is why an `mcp__qompack__…` result is
  simultaneously `IsCompactable == false` and first in the eviction order.
- `Tombstone` renders for **any** record, including non-compactable ones, because a caller may want
  an addressable marker for a result it is choosing to elide for its own budget reasons; the
  compactability decision belongs to the caller, not to the renderer. `TombstoneNote()` is emitted
  once per rehydration block by SP-11 and once per `expand` response by SP-13; SP-08 only ships and
  tests it.

`supersedableClass`: `filecontent` for FileRead/FileEdit/FileWrite; `search` for Grep/Glob; `exec`
for Bash/PowerShell; `web` for WebFetch/WebSearch; `""` (never supersedes, never superseded) for
everything else.

---

### `internal/observer/signals.go` (exists as SP-01's stub — `Signals` and a zero-returning `ExtractSignals`)

**Responsibility.** G1.5. Pure functions of `hookio.Event` — no clock, no state, no I/O — so they
are trivially testable and reusable by the daemon.

```go
func ExtractSignals(e hookio.Event) Signals
func ExtractTestOutcome(e hookio.Event) TestOutcome
func PathsFromInput(tool string, in json.RawMessage) []string
func commandOf(e hookio.Event) string        // tool_input.command, "" when absent
func responseText(e hookio.Event) []byte     // see rule below
```

`responseText` unwraps `e.ToolResponse` deterministically:
1. JSON string → the unquoted bytes;
2. object with `"content"` string → those bytes; object with `"content"` array → every element's
   `"text"` field joined with `"\n"`;
3. object with `"stdout"` → `stdout`, plus `"\n"+stderr` when `stderr` is non-empty;
4. anything else → `json.Compact` of the raw message.

Truncated at the package constant `maxScanBytes = 4 << 20 // 4 MiB: bounds the pure decoder's
allocation; the CONFIGURED cap is applied separately in tooluse.go`, with the retained prefix used
as-is. `responseText` deliberately does **not** read `o.maxResultBytes`: everything in this file is
a pure function of `hookio.Event` so that `ExtractSignals` stays reusable by the daemon and
trivially fuzzable, and the configured `runtime.hotPath.maxPayloadBytes` truncation happens at
step 3 of `OnToolUse` instead. `maxScanBytes` is strictly larger than the 1 MiB default so the two
caps never interact in practice; when a user configures a larger `maxPayloadBytes`, `maxScanBytes`
binds first and that is the documented behaviour.

`PathsFromInput` returns, in this order and deduplicated with order preserved: `file_path`,
`path`, `notebook_path`, then every `edits[].file_path`. Values are returned raw; the caller
normalizes with `paths.Norm`/`paths.Key`.

**Regex tables** (compiled once in `var` blocks, all case-insensitive where marked):

```go
var reTestRunner = regexp.MustCompile(`(?i)\b(go\s+test|npm\s+(run\s+)?test|yarn\s+test|pnpm\s+test|jest|vitest|pytest|python\s+-m\s+pytest|cargo\s+test|dotnet\s+test|mvn\s+test|gradle\s+test|rspec|ctest)\b`)
var reTestPass = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^ok\s+\S+`),
    regexp.MustCompile(`(?m)^PASS\b`),
    regexp.MustCompile(`(?i)\btests?:\s+\d+\s+passed\b`),
    regexp.MustCompile(`(?i)=+\s*\d+\s+passed`),
    regexp.MustCompile(`test result: ok\.`),
    regexp.MustCompile(`(?i)\bOK\s+\(\d+\s+tests?\)`),
}
var reTestFail = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^FAIL\b`),
    regexp.MustCompile(`(?m)^---\s+FAIL`),
    regexp.MustCompile(`(?i)\bFAILED\b`),
    regexp.MustCompile(`(?i)\b[1-9]\d*\s+failed\b`),
    regexp.MustCompile(`test result: FAILED\.`),
}
var reGitCommit  = regexp.MustCompile(`(?i)\bgit\s+(-C\s+\S+\s+)?commit\b`)
var reGitNoop    = regexp.MustCompile(`(?i)nothing to commit|no changes added to commit`)
```

- `ExtractTestOutcome`: `TestUnknown` unless `NormalizeToolName(e.ToolName) ∈ {Bash, PowerShell}`
  and `reTestRunner` matches the command. Then `TestFail` if any `reTestFail` matches the response
  text, else `TestPass` if any `reTestPass` matches, else `TestUnknown`. Fail wins over pass —
  a run with one failure is not a passing test run.
- `Signals.TestPassed = ExtractTestOutcome(e) == TestPass`.
- `Signals.GitCommit`: Bash/PowerShell, `reGitCommit` matches the command, `reGitNoop` does not
  match the response.
- `Signals.TodoCompleted`: `e.ToolName == "TodoWrite"` and the input's `todos` array contains at
  least one element with `"status":"completed"`. (Newly-completed detection — the transition, not
  the state — is done in `tooluse.go` against `sessionState.TodoDone`, because `ExtractSignals`
  must stay pure per §5.21.)
- `Signals.Paths`: `PathsFromInput` output, unchanged (raw, caller-normalized).

Malformed JSON in `ToolInput`/`ToolResponse` never panics and never errors: every unmarshal is into
a tolerant struct and failures yield zero values.

---

### `internal/observer/tooluse.go` (new)

**Responsibility.** The `PostToolUse` pipeline of §8.1 items 1–6, in the order fixed by resolved
decision 1.

```go
func (o *observer) OnToolUse(ctx context.Context, e hookio.Event) (hookio.Output, error)
func (o *observer) canonOptions() canon.Options
func (o *observer) rememberToolUse(st *sessionState, rec store.ToolUseRecord)
func normalizedPaths(projectRoot string, raw []string) []string
```

Algorithm, exactly:

```
1.  if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }
    now := o.now()
    st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()   // decision 9
2.  display := NormalizeToolName(e.ToolName)
    ephemeral := strings.HasPrefix(e.ToolName, "mcp__qompack__")
3.  body := responseText(e)
    if len(body) > o.maxResultBytes { body = body[:o.maxResultBytes] } // runtime.hotPath.maxPayloadBytes
    empty := len(body) == 0
4.  rawPath := first of PathsFromInput(display, e.ToolInput); pathKey := ""
    if rawPath != "" { n, err := paths.Norm(o.opt.ProjectRoot, rawPath); if err == nil { pathKey = paths.Key(n) } }
5.  var res store.PutResult                       // zero value: Root.Hash zero, Chunks nil, RawBytes 0
    if !empty {
        var err error
        res, err = o.opt.Store.PutBytes(ctx, body, store.PutOptions{
            Tool: display, Path: pathKey, Canon: o.canonOptions(),
            KeepRaw: true, Ephemeral: ephemeral })
        on err: o.soft("put", err); return hookio.Empty(), nil
    }
    // empty == true: skip the Put entirely and carry the zero PutResult forward. Steps 7 and 8
    // are ALSO skipped when empty (there is no content to version and no chunk set to compare);
    // steps 6, 9–14 run normally, so a zero-byte result still produces an index entry, sketch
    // feeds, DAG nodes and signals. This is what TestOnToolUse_EmptyResponseStillIndexed asserts.
6.  tok := res.Root.Tokens
    if tok == 0 && o.opt.Tokens != nil {
        tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.Classify(display, pathKey, body))
    }
    digest, preview := store.ArgsDigest(e.ToolInput)   // §5.8 owns this; never re-derived here
    rec := store.ToolUseRecord{ ID: e.ToolUseID, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: display, ArgsDigest: digest, ArgsPreview: preview,
        Root: res.Root.Hash, Path: pathKey, Bytes: res.Root.RawBytes, Tokens: tok,
        Signature: res.Signature, Status: store.StatusOK, Ephemeral: ephemeral }
    if e.ToolUseID == "" { rec.ID = core.ToolUseID(fmt.Sprintf("tu_%s_%d_%d", e.SessionID, st.Turn, len(st.ToolUses))) }
    Store.RecordToolUse(ctx, rec)                             // §8.1 item 1 index entry
    o.rememberToolUse(st, rec)                                // append to st.ToolUses; SubagentStop needs it
7.  if !empty && pathKey != "" && supersedableClass(display) == "filecontent" {
        Store.AppendFileVersion(ctx, pathKey, store.FileVersion{TS: now, Root: res.Root.Hash,
            Turn: st.Turn, Bytes: res.Root.RawBytes})          // §8.2 file version history
    }
8.  var superseded []core.ToolUseID
    if !empty { superseded = o.detectSupersession(ctx, st, rec, res) }  // §8.1 item 3 — supersede.go
9.  o.feedSketches(rec)                                       // §8.1 item 5 — sketches.go
10. o.emitToolGraph(ctx, st, rec, body, superseded)           // §8.1 item 4 — graph.go
11. if o.opt.Grammar != nil {                                 // §8.1 item 6 producer side
        o.opt.Grammar.Append(grammar.Symbol(display))
        switch ExtractTestOutcome(e) {
        case TestPass: o.opt.Grammar.Append("test:pass")
        case TestFail: o.opt.Grammar.Append("test:fail")
        }
        o.collectThrash(st)                                   // stores pending warnings only
    }
12. sig := ExtractSignals(e)
    sig.TodoCompleted = o.newlyCompletedTodos(st, e)          // transition, not state
    sig.Paths = normalizedPaths(o.opt.ProjectRoot, sig.Paths)
    if o.opt.OnSignals != nil { o.opt.OnSignals(e.SessionID, sig) }
    counters: observer.signal.{todo,test,git} incremented per true flag
13. o.recordRecent(st, display, sig.Paths, body, now)
    if fs, ok := o.features(st, now); ok && o.opt.OnFeatures != nil {
        o.opt.OnFeatures(e.SessionID, fs)                     // BOCD features → L3
    }
    st.LastTS = now                                           // AFTER features(): GapSeconds needs the previous TS
14. if IsCompactable(display) { o.count("observer.tombstone") } // this result may later be cleared
    return hookio.Empty(), nil                                 // PostToolUse emits nothing
```

`o.maxResultBytes` is read once in `New` from `cfg.Get("runtime.hotPath.maxPayloadBytes")`; when the
key is absent, not a number, or `<= 0`, it falls back to
`config.Defaults().Runtime.HotPath.MaxPayloadBytes` rather than a literal, so §11.6's no-hardcoding
rule holds without a `//nomagic:allow`.

`rememberToolUse(st, rec)` appends `toolUseLite{ID: rec.ID, Root: rec.Root, Tool: rec.Tool,
Path: rec.Path, Bytes: rec.Bytes}` to `st.ToolUses`. When the slice would exceed
`subagentWindowCap`, it drops `k` entries from the front and does
`st.SubagentSince = max(0, st.SubagentSince-k)` in the same operation (see `observer.go`). It is
called for every tool use, ephemeral or not, because a subagent's retrieval calls are part of what
the parent never held.

`normalizedPaths(projectRoot, raw)` maps each raw path through `paths.Norm` then `paths.Key`,
dropping entries whose `Norm` errors (an escape above the project root), and preserving order and
first-appearance dedup. It is the only place `Signals.Paths` is normalized; `ExtractSignals` itself
stays pure and returns raw values (see `signals.go`).

`canonOptions()`:

```go
cc := o.opt.Cfg.Store.Canonicalize
cls := []canon.Class{canon.Class("crlf")}                  // resolved decision 2 — always
if cc.Enabled { for _, s := range cc.Strip { if s != "crlf" { cls = append(cls, canon.Class(s)) } } }
return canon.Options{
    Strip: cls, KeepDeltas: true,
    MinHash: sketch.MinHashOptions{
        Enabled:          cc.Enabled && cc.MinHash.Enabled,
        Permutations:     cc.MinHash.Permutations,
        ShingleSize:      5,
        NearDupThreshold: cc.MinHash.NearDupThreshold,
    },
}
```

**`store.ArgsDigest` is not re-implemented here, and there is no local `argsPreviewMax`.** §5.8
ships `ArgsDigest(raw json.RawMessage) (core.Hash, string)` and names its caller in the function's
own doc comment: *"SP-08 calls this; nothing else may re-derive it, so that two subplans can never
disagree about what 'the same tool arguments' means."* It canonicalizes the `tool_input` document —
object keys sorted recursively, array order preserved, every number re-emitted **verbatim** as its
`json.Number` literal — digests the canonical bytes under `core.DomainArgs` (`"qompack.args.v1"`),
and returns the ≤120-**byte** preview built from `file_path`, `path`, `pattern`, `command`, `url` in
that order, falling back to the canonical JSON when the document has none of them. Whitespace runs
are collapsed, control bytes dropped, and a truncated preview ends in `…`.

A local `json.Compact` digest would be key-order-dependent and would defeat
`TestArgsDigest_KeyOrderInvariant` (`internal/store/tooluse_test.go`) at the single production call
site, so `internal/observer` declares no `argsDigestAndPreview`, no preview key table and no
`argsPreviewMax` of its own. The two records SP-08 writes that carry no `tool_input` — the verbatim
prompt and the subagent capture — go through the *same* function over a synthesized one-key
arguments document, so the ≤120-byte cap, the whitespace collapsing and the control-byte stripping
live in exactly one place (`prompt.go` step 4 and `stop.go` step 6). The cost is that those two
previews show their JSON wrapper (`{"prompt":"fix the pool bypass…"}`); that is the honest rendering
of what those records' "arguments" are, and it is worth more than a second truncator that could
drift from §5.8's.

**Failure modes.** `PutBytes` error → `soft("put")`, return empty, no index entry (never a dangling
record). `RecordToolUse` error → `soft("index")`, continue with sketches/DAG (the object is stored
and reachable by root hash). `AddNode`/`AddEdge` error → `soft("dag")`, continue. Every store call
uses the caller's `ctx`; a cancelled context short-circuits at the next stage boundary and returns
`ctx.Err()`.

**Budget.** This body runs inside the daemon worker, i.e. under **B-C** (`l0_process`, p99 < 50 ms,
soft). It is not on B-A. The only unbounded work is symbol extraction, capped by `symbolScanCap`
and `maxSymbolsPerResult`, and the supersession scan, capped by `supersessionLookback`.

---

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

### `internal/observer/sketches.go` (new)

**Responsibility.** §8.1 item 5, including the prohibition.

```go
func (o *observer) feedSketches(rec store.ToolUseRecord)
```

```
if rec.Ephemeral { return }                                  // retrieval is not exploration
if o.opt.Touch != nil {
    o.opt.Touch.Add([]byte("t\x00"+rec.Tool), 1)
    if rec.Path != "" { o.opt.Touch.Add([]byte("p\x00"+rec.Path), 1) }
}
if o.opt.Explore != nil && rec.Path != "" { o.opt.Explore.Add([]byte("p\x00"+rec.Path)) }
if o.opt.Hot != nil && rec.Path != "" { o.opt.Hot.Add(rec.Path, 1) }
```

The Count-Min is the §6.2 "File-touch frequency (which files are hot)" sketch; the two key prefixes
`p\x00` and `t\x00` keep the path and tool namespaces disjoint inside one CMS. The HyperLogLog
counts **distinct paths touched** — §6.2's "Breadth-of-exploration cardinality". Misra-Gries gives
`/qompack:status` a no-false-positives top-k.

**The Bloom filter is never referenced in this file, or anywhere in `internal/observer`.** §8.1
item 5: *"Feed the Bloom filter only on explicit negative-knowledge events (§8.3)"* — those events
are SP-09's. A CI-visible test (`TestObserverNeverFeedsBloom`) drives a full session through a
`store` whose `sketch.Bloom` is replaced by a panicking double and asserts no panic; a second
assertion greps the package source for the identifier `Bloom` and requires zero non-test hits.

---

### `internal/observer/graph.go` (new)

**Responsibility.** §8.1 item 4: *"Record `tool_use → tool_result → assistant_turn → next_tool_use`,
plus shared-state edges keyed on file path and symbol name."* — expressed **entirely** as a call to
`dag.BuildToolUse`. This file translates a `store.ToolUseRecord` into a `dag.ObservedTool`; it does
not decide a single node id, edge direction or edge kind for the item-4 chain.

```go
func (o *observer) emitToolGraph(ctx context.Context, st *sessionState, rec store.ToolUseRecord,
    body []byte, superseded []core.ToolUseID)
func (o *observer) symbolNames(rec store.ToolUseRecord, body []byte) []string
func (o *observer) enrol(st *sessionState, n dag.NodeID)   // one segment-membership edge
func (o *observer) advancePos(st *sessionState, tok core.Tokens) int
```

**No NodeID constructors live in this package.** `internal/dag/nodeid.go` is explicit about why:
*"Every consumer builds IDs through the constructors below rather than concatenating strings …
One component spelling a file node differently from another does not fail loudly — it silently
produces two disconnected halves of the same graph."* And the divergence really is silent —
`ParseNodeID` splits on the FIRST colon, so a hand-rolled `assistant:<session>:<turn>` is accepted
by `AddNode` as a `KindAssistant` node whose key is `<session>:<turn>`, disjoint from every
`dag.AssistantNode(turn)` the rest of the system emits and from the frozen
`testdata/golden/contracts/dag/graph-basic.jsonl`, which carries `"id":"assistant:1"` and
`"id":"symbol:src/auth.ts#refreshToken"`. SP-08 therefore calls `dag.ToolUseNode`,
`dag.ToolResultNode`, `dag.AssistantNode(turn)`, `dag.UserPromptNode(turn)`, `dag.FileNode`,
`dag.SymbolNode(pathKey, name)` and `dag.SegmentNode` — turn-keyed with **no session component**,
symbols keyed `"<pathKey>#<name>"` — and nothing else. Per-session assistant ids, if anyone ever
wants them, are an amendment to SP-07's D-2 table and to the frozen golden, not a private
redefinition here.

`emitToolGraph`:

```
pos       := o.advancePos(st, 0)            // the tool_use block's start position
resultPos := o.advancePos(st, rec.Tokens)   // the tool_result block's — decision 5
sup       := core.ToolUseID("")
if len(superseded) > 0 { sup = superseded[0] }   // most recent first, per supersede.go

err := dag.BuildToolUse(o.opt.Graph, dag.ObservedTool{
    ToolUseID:     rec.ID,
    PrevToolUseID: st.LastToolUseID,
    PrevTurn:      st.LastToolUseTurn,
    Supersedes:    sup,
    Turn:          rec.Turn,
    TS:            rec.TS,
    Pos:           pos,
    ResultPos:     resultPos,
    Tool:          rec.Tool,                 // display name; becomes the tool-use node's Ref
    PathKey:       rec.Path,                 // already paths.Key form; dag never re-folds it
    Writes:        rec.Tool == "FileEdit" || rec.Tool == "FileWrite",
    Symbols:       o.symbolNames(rec, body),
    Root:          rec.Root,
    Tokens:        rec.Tokens,
    Ephemeral:     rec.Ephemeral,
})
if err != nil { o.soft("dag", err) }

// Any FURTHER read this one superseded — BuildToolUse carries exactly one — in the same D-1
// direction the builder uses, superseded → superseding:
for _, older := range superseded[1:] {
    o.opt.Graph.AddEdge(dag.Edge{From: dag.ToolUseNode(older), To: dag.ToolUseNode(rec.ID),
        Kind: dag.EdgeSupersedes, Weight: 1, Turn: rec.Turn})
}

o.enrol(st, dag.ToolUseNode(rec.ID))
o.enrol(st, dag.ToolResultNode(rec.ID))
st.LastToolUseID, st.LastToolUseTurn = rec.ID, rec.Turn
```

That one call emits, in the builder's own order: the tool-use node; the tool-result node;
`tooluse → toolresult` `EdgeProduces`; the assistant node for `rec.Turn`; `toolresult:<prev> →
assistant` `EdgeConsumes` **only when the predecessor is in an earlier turn**;
`assistant → tooluse` as `EdgeSequence` or `EdgeControlOnly`; the file node and its `EdgeSharedFile`
edge oriented by `Writes`; one symbol node and `EdgeSharedSymbol` edge per distinct name, in
ascending order; and the `EdgeSupersedes` edge.

Four consequences are worth stating explicitly, because each one is a bug this plan previously had:

- **Thin slicing finally has something to drop.** `dag.turnLinkKind` makes the assistant → tool_use
  link an `EdgeSequence` when the call touches the same file or symbol as its predecessor and an
  `EdgeControlOnly` when it shares nothing. `EdgeControlOnly` has exactly one producer in the
  repository — that function — and `internal/dag/traverse.go`'s thin branch drops
  `EdgeControlOnly` **and nothing else**. SP-08 is the sole production emitter of transcript edges,
  so a hand-rolled emitter that always wrote `EdgeSequence` would make `DefaultSliceOptions(...).
  Thin == true` a no-op on every real graph, and ADR 0007's "43% the size, 88% of the relevant set,
  2.2× more precise" a property of `dagtest.Synth`'s `ControlOnlyFraction: 0.35` rather than of
  production traffic. Going through the builder is what makes that number true of real sessions.
- **No parallel-sibling cycle.** Resolved decision 4 keeps `Turn` fixed across the tool uses of one
  assistant message, so parallel tool calls share a turn index. `BuildToolUse` emits the consumes
  edge only under `o.PrevToolUseID != "" && o.PrevTurn < o.Turn`; emitting
  `toolresult:<prev> → assistant:<Turn>` for a same-turn sibling would close the three-node cycle
  `tooluse → toolresult → assistant → tooluse` that SP-07 D-7 names, which *"would make every
  backward slice from a tool use swallow that tool use's own forward chain, corrupting both the
  relevance scores of §8.3 and the segment_coupling counts of §8.4."* The guard comes free with the
  builder; carrying `st.LastToolUseTurn` is the only thing SP-08 has to do to feed it.
- **Shared state is anchored on the file, not on the previous tool use.** The builder runs
  `file → tooluse` for a read and `tooluse → file` for a write, both `EdgeSharedFile` (D-1: a read
  consumes the anchor, a write produces it). Two calls that touch one path are therefore coupled
  through the shared `file:` node, and `dag.sharesState` reads exactly those edges to decide the
  turn link. A `tooluse → tooluse` `EdgeSharedFile` edge is not part of the scheme and is not
  emitted, which is why `detectSupersession` no longer returns a `prevOnPath` and why `OnToolUse`
  needs no second index read for the graph.
- **The supersedes edge runs older → newer.** See `supersede.go`'s direction note; the builder is
  where that is enforced, and the loop above matches it for the tail of `marked`.

`symbolNames(rec, body)` returns `nil` unless `o.opt.Symbols != nil`, `rec.Path != ""`,
`supersedableClass(rec.Tool) == "filecontent"` and `len(body) <= symbolScanCap`; otherwise it takes
the first `maxSymbolsPerResult` unique names from `o.opt.Symbols.Names(rec.Path, body)` and hands
them over unsorted — `BuildToolUse` sorts and deduplicates, precisely so `dag/deps.jsonl`'s edge
order does not depend on how the extractor walked the file.

`enrol(st, n)` is segment membership and is a single edge, `AddEdge{n → dag.SegmentNode(st.Segment),
EdgeSequence, 1, st.Turn}`, skipped entirely when `st.Segment == 0`. The direction is the builder's:
`dag.BuildSegment` emits `member → segment` because members are the earlier end under D-1, and
`session.go` calls `BuildSegment` with `Members: nil` at close time to mint the segment node and the
`previous → current` chain edge. Emitting the membership edges incrementally rather than in that one
call is legal by D-6 — *"an edge may legally precede its endpoints"* — and is what keeps SP-08 from
holding a whole segment's node list in memory.

`advancePos(st, tok)` returns the pre-increment `st.PrefixTokens` and adds `int(tok)` to it —
resolved decision 5.

`Graph.Flush` is **not** called per tool use (that would put an append+fsync on the ingest path);
it is called from `OnStop`, `OnSessionEnd`, and `Persist`.

---

### `internal/observer/features.go` (new)

**Responsibility.** The five §6.6 features, computed from `sessionState.Recent`, delivered through
`Options.OnFeatures` for the daemon to translate into `scheduler.Features`.

```go
func (o *observer) recordRecent(st *sessionState, tool string, paths []string, body []byte, ts core.UnixMilli)
func (o *observer) features(st *sessionState, now core.UnixMilli) (FeatureSample, bool)
func jaccard(a, b map[string]struct{}) float64
func totalVariation(a, b map[string]float64) float64
func cosineTokens(a, b []byte) float64
```

- `recordRecent` appends a `recentEvent` holding the tool, the normalized paths, and the **first
  `cohesionTokenCap*8` bytes** of `body` (bounded memory), evicting from the front at
  `2*featureWindow` entries. It does **not** touch `st.LastTS`: `LastTS` is the timestamp of the
  *previous* event and is assigned as the last bookkeeping statement of each entry point, after
  `features` has been consulted. Assigning it earlier would make `GapSeconds` identically zero,
  which is the single easiest way to silently disable BOCD's time feature.
- `features` fills `Turn: st.Turn` and `TS: now` on the returned `FeatureSample` (the daemon needs
  both to call `scheduler.Runtime.Observe(ctx, f, turn)`), and returns `ok=false` until
  `len(st.Recent) >= 2*featureWindow`. Split into
  `cur = Recent[n-featureWindow:]` and `prev = Recent[n-2*featureWindow : n-featureWindow]`.
- **PathJaccard** = `jaccard(pathSet(cur), pathSet(prev))`; both empty → `1.0` (no locality shift);
  one empty → `0.0`.
- **ToolShift** = `totalVariation` of the normalized tool-name distributions =
  `0.5 * Σ_i |p_i − q_i|` over the union of tool names.
- **LexicalCohesion** = `cosineTokens(concat(cur.Text), concat(prev.Text))`: lowercase tokens
  matching `[A-Za-z_][A-Za-z0-9_]*`, first `cohesionTokenCap` tokens per side, term-frequency
  vectors, cosine similarity; `0.0` when either side has no tokens.
- **GapSeconds** = `float64(now − st.LastTS) / 1000`, `0` when `st.LastTS == 0`.
- **TodoTransition** = `1.0` when the newest `recentEvent.Tool == "TodoWrite"` and
  `newlyCompletedTodos` fired for it, else `0.0`.

`newlyCompletedTodos(st, e)` parses `tool_input.todos[]` into `{content, status}`, returns true iff
at least one todo has `status == "completed"` and its `content` is not already in `st.TodoDone`,
and then records every completed content string in `st.TodoDone`. This is the transition detector
that keeps `ExtractSignals` pure (resolved decision from `signals.go`).

All five values are finite; `NaN`/`Inf` are impossible because every divisor is guarded.

---

### `internal/observer/prompt.go` (new)

**Responsibility.** §8.1 item 7 and G2.3.

```go
func (o *observer) OnUserPrompt(ctx context.Context, e hookio.Event) (hookio.Output, error)
func VerbatimPromptID(s core.SessionID, t core.TurnIndex) core.ToolUseID
func verbatimOptions() canon.Options                        // the "no optional class" Put options
func promptArgs(prompt string) json.RawMessage              // {"prompt":<text>} for store.ArgsDigest
func (o *observer) collectThrash(st *sessionState)          // called from OnToolUse step 11
func (o *observer) pendingThrashLines(st *sessionState) []string
```

Algorithm:

```
1.  if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }
    if e.Prompt == "" { return hookio.Empty(), nil }
    now := o.now(); st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()
2.  body := []byte(e.Prompt)
3.  res, err := Store.PutBytes(ctx, body, store.PutOptions{
        Tool: "UserPromptSubmit", Path: "",
        Canon: verbatimOptions(),
        KeepRaw: true, Ephemeral: false })
    // verbatimOptions() is canon.Options{Strip: []canon.Class{}, MinHash: sketch.MinHashOptions{
    //     Enabled: false}} — an EMPTY, NON-NIL Strip: "no optional class", not "every class".
    // Pre-step (a) is what makes the store honour both fields; see the note below for exactly how
    // far "verbatim and immutably" (§7.3, §8.1 item 7) reaches.
    on err: o.soft("prompt.put", err) and continue to step 6 with a zero root
4.  id := VerbatimPromptID(e.SessionID, st.Turn)
    tok := res.Root.Tokens; if tok == 0 && Tokens != nil { tok = Tokens.EstimateString(e.Prompt, tokens.ClassProse) }
    digest, preview := store.ArgsDigest(promptArgs(e.Prompt))   // §5.8 owns the ≤120-byte cap
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "UserPromptSubmit",
        ArgsDigest: digest, ArgsPreview: preview,
        Root: res.Root.Hash, Bytes: int64(len(body)), Tokens: tok,
        Status: store.StatusOK})
5.  dag.BuildUserPrompt(o.opt.Graph, dag.ObservedPrompt{Turn: st.Turn, TS: now,
        Pos: o.advancePos(st, tok), Tokens: tok, Ref: string(id)})
    // emits userprompt:<turn> and userprompt:<turn> --consumes--> assistant:<turn>: the prompt is
    // the producer end (D-1), and §4.4 makes this the ONLY path by which a backward slice from a
    // tool use deep in a session reaches the request that set it off.
    o.enrol(st, dag.UserPromptNode(st.Turn))          // segment membership, graph.go
6.  if Grammar != nil { Grammar.Append(grammar.Symbol("user")) }
7.  st.LastPromptTurn = st.Turn; st.SubagentSince = len(st.ToolUses); st.Turn++
8.  o.recordRecent(st, "user", nil, body, now)
    if fs, ok := o.features(st, now); ok && OnFeatures != nil { OnFeatures(e.SessionID, fs) }
    st.LastTS = now                                  // after features(), per features.go
9.  out := hookio.Empty()
    if o.mode() == ModeFull {
        if lines := o.pendingThrashLines(st); len(lines) > 0 {
            out.HookSpecificOutput = &hookio.HSO{HookEventName: "UserPromptSubmit",
                AdditionalContext: strings.Join(lines, "\n")}
        }
    }
    return out, nil
```

**The one deliberate exception to resolved decision 2, and exactly how far it reaches.** The prompt
is stored with an **empty, non-nil** `Strip`, which `canon.gateSet` reads as "exactly these classes
— i.e. none — plus the always-on structural ones", and with MinHash off. §8.1 item 7 and §7.3 both
say "verbatim and immutably", and a user prompt is not file content read on two platforms, so there
is no dedup space to fork and nothing to gain from stripping timestamps, ANSI, PIDs, addresses,
tmp paths or durations out of the thing the user typed. This is written down here so a later
reviewer does not "fix" the inconsistency with `tooluse.go`.

Three things it does **not** mean, all of them structural and none of them optional:

- `Strip: nil` would be the *opposite* request. `internal/canon/classes.go`: *"A nil Strip means
  'every class' — the documented meaning of the zero Options."* An earlier draft of this plan asked
  for `Strip: nil` and got the strongest canonicalization available.
- `crlf` and `paths` still run. `canon.alwaysOn = {ClassCRLF, ClassPaths}` is *"applied regardless
  of Options.Strip"*, and Appendix C cannot disable either, because §4 makes CRLF→LF normalization a
  precondition of cross-platform dedup. "Without even the unconditional `crlf` class" is impossible
  by construction, and the plan does not claim it.
- Redaction still runs. `store.PutBytes` is REDACT → canonicalize → chunk (§5.22a, §13 invariant 7),
  and no caller may opt out: a credential pasted into a prompt must not reach `objects/` in the
  clear, where content-addressing makes it undeletable.

What "verbatim" therefore guarantees is precise and testable: the stored object is the user's bytes
with nothing removed but secrets and line-ending/separator normalization, no near-duplicate
signature, no delta-against-prior encoding, and no rewrite ever. `KeepRaw: true` means the store
keeps the canonicalizer's deltas, so `canon.Restore` reconstructs the pre-normalization bytes
exactly; between that and the immutable `tool_use.jsonl` entry, nothing the user typed is lost.
`stop.go`'s capture blob takes the same `verbatimOptions()` treatment for the same reason: it is
JSON this package generated, not host content.

**Never regenerated.** Nothing in `internal/observer` ever rewrites a prompt object, a prompt
`tool_use` entry, or a prompt DAG node. `store.PutBytes` is content-addressed and
`RecordToolUse` appends; there is no update path. A test asserts that submitting the same prompt
text twice yields two distinct records with the same root hash and that `objects/` grew by zero
chunks the second time.

**Thrash lines.** `collectThrash` calls `o.opt.Grammar.Thrash(thrashMinUses)` and appends to
`st.PendingThrash` each returned rule whose `r.ID` is not already a key of `st.WarnedRules`;
`pendingThrashLines` drains `st.PendingThrash` (setting it to nil), sets `st.WarnedRules[r.ID] = true`
for each drained rule, and renders each with `grammar.FormatWarning(grammar.Warning{Rule: r,
Repeats: r.Uses, Message: "repeated action cycle detected", Turns: nil})`. With SP-15's stub
`Thrash` returning nil, this whole path is inert — which is the wave-2 expectation.

---

### `internal/observer/stop.go` (new)

**Responsibility.** §8.1 item 8 / G10.1.

```go
func (o *observer) OnStop(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error)
func SubagentCaptureID(s core.SessionID, t core.TurnIndex) core.ToolUseID
func subagentName(e hookio.Event) string
func subagentArgs(agent, summary string) json.RawMessage   // {"agent":…,"summary":…} for ArgsDigest
func subagentSummary(e hookio.Event) string
func tailAssistantText(path string, maxBytes int64) string
```

Both branches begin with the same preamble as the other entry points:
`if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }`, `now := o.now()`,
`st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()`.

**Main-agent Stop (`subagent == false`).** Append `grammar.Symbol("stop")` when `Grammar != nil`;
`st.Turn++`; `Graph.Flush(ctx)` (softed as `"stop.flush"`); `st.LastTS = now`; return
`hookio.Empty(), nil`. No store writes: the assistant's own text is not a tool result and Claude
Code's transcript already holds it.

**SubagentStop (`subagent == true`).**

```
1.  agent := subagentName(e)          // e.Extra["agent"], else "subagent"
    // The three-way host-name resolution (subagent_type → agent_name → agent) happens in the HOOK
    // CLIENT, not here: hookio.Event.Extra is tagged `json:"-"`, so it is populated by
    // hookio.ReadEvent in the client process and dropped by ipc.EncodeRequest. Pre-step (b) is what
    // puts one resolved key back — rawExtras sends {"subagent":true,"agent":"<name>"} and
    // resolveEvent restores it into Extra daemon-side — so this function reads ONE key and falls
    // back to the literal "subagent" when it is absent, malformed, or not a non-empty JSON string.
    // Without pre-step (b) every production capture would be named "subagent"; the unit test alone
    // would never have caught it, which is why an end-to-end row asserts the name through the real
    // daemon (test plan, `stop_test.go` and `observer_e2e_test.go`).
2.  summary := subagentSummary(e)
    // (a) responseText(e) when e.ToolResponse is non-empty;
    // (b) else tailAssistantText(e.TranscriptPath, 512<<10);
    // (c) else "" → log Debug, still capture the hash list (the hashes are the point).
3.  refs := make([]SubagentToolRef, 0)
    for _, t := range st.ToolUses[min(st.SubagentSince, len(st.ToolUses)):] {
        refs = append(refs, SubagentToolRef{ToolUseID: t.ID, Root: t.Root.String(),
                                            Tool: t.Tool, Path: t.Path, Bytes: t.Bytes})
    }
4.  capture := SubagentCapture{Session: e.SessionID, Agent: agent, Turn: st.Turn, TS: now,
                               Summary: summary, ToolResults: refs}   // not `cap` — builtin shadow
    blob, err := json.Marshal(capture)   // struct field order ⇒ deterministic bytes
    if err != nil { o.soft("stop.marshal", err); return hookio.Empty(), nil }
5.  res, err := Store.PutBytes(ctx, blob, store.PutOptions{Tool: "SubagentStop", Path: "",
        Canon: verbatimOptions(), KeepRaw: true})     // prompt.go's empty non-nil Strip, MinHash off
    on err: o.soft("stop.put", err); st.Turn++; st.LastTS = now; return hookio.Empty(), nil
    tok := res.Root.Tokens
    if tok == 0 && o.opt.Tokens != nil {
        tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.ClassJSON)  // the blob is JSON
    }
6.  id := SubagentCaptureID(e.SessionID, st.Turn)
    digest, preview := store.ArgsDigest(subagentArgs(agent, summary))   // §5.8 owns the ≤120-byte cap
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "SubagentStop", ArgsDigest: digest, ArgsPreview: preview,
        Root: res.Root.Hash, Bytes: int64(len(blob)), Tokens: tok,
        Status: store.StatusOK, Subagent: agent})
7.  Graph.AddNode(dag.Node{ID: dag.ToolUseNode(id), Kind: dag.KindToolUse, Turn: st.Turn, TS: now,
        Pos: o.advancePos(st, tok), Ref: "SubagentStop", Root: res.Root.Hash, Tokens: tok})
    for _, r := range refs { Graph.AddEdge(dag.Edge{From: dag.ToolResultNode(r.ToolUseID),
        To: dag.ToolUseNode(id), Kind: dag.EdgeConsumes, Weight: 1, Turn: st.Turn}) }
    o.enrol(st, dag.ToolUseNode(id))                  // segment membership, graph.go
    // D-1, no exception: the subagent's RESULTS are the earlier/producer end and the capture is the
    // consumer, so every edge runs toolresult:<ref> → tooluse:<capture>. This is the one node set
    // SP-08 still builds by hand — a subagent capture is not a transcript tool call, so there is no
    // dag.Observed* shape for it — and the ids come from dag's constructors, never from string
    // concatenation. `Ref` is the tool name, matching what BuildToolUse puts on a tool-use node.
8.  st.SubagentSince = len(st.ToolUses); st.Turn++; st.LastTS = now
    o.count("observer.subagent_capture"); Graph.Flush(ctx)
9.  return hookio.Empty(), nil
```

Note that step 6 records the capture at the **pre-increment** `st.Turn`, and `SubagentCaptureID`
is minted from the same value, so the id, the record's `Turn`, the DAG node's `Turn` and the
`SubagentCapture.Turn` field all agree — resolved decision 4.

`tailAssistantText(path, maxBytes)`: `os.Stat`, seek to `max(0, size-maxBytes)`, read to EOF, drop
everything before the first `'\n'` (partial line), then scan lines from the end; unmarshal each into
`struct{ Type string "json:\"type\""; IsSidechain bool "json:\"isSidechain\""; Message struct{
Role string "json:\"role\""; Content []struct{ Type, Text string } "json:\"content\"" }
"json:\"message\"" }`; return the concatenation (joined with `"\n"`) of the `Text` fields of the
last line whose `Message.Role == "assistant"` and whose `Content` has at least one `Type == "text"`.
Any stat/open/read/parse failure returns `""` — never an error, never a panic.

**Why this closes G10.1.** The parent's context holds only the subagent's prose summary. After this
hook, `.qompack/objects/` holds that summary *and* a hash list pointing at every tool result the
subagent produced, all of which the parent never held. `expand(subagent_<session>_<turn>)` returns
the capture; each `root` inside it is independently `expand`-able. That is the retrieval path the
gap says does not exist.

---

### `internal/observer/session.go` (new)

**Responsibility.** §5.21's split-ownership table: SP-08 owns the `startup`/`resume` branch and the
whole of `OnSessionEnd`; SP-11's branches are reached through the `Rehydrator` seam.

```go
func (o *observer) OnSessionStart(ctx context.Context, e hookio.Event) (hookio.Output, error)
func (o *observer) OnSessionEnd(ctx context.Context, e hookio.Event) (hookio.Output, error)
func (o *observer) ensureSegment(ctx context.Context, st *sessionState, s core.SessionID, now core.UnixMilli)
```

`OnSessionStart`:

```
0.  if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }
    now := o.now()
1.  o.loadState()                                  // idempotent (sync.Once); state/observer.json
2.  st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()
3.  frontier, err := Store.Segments().Frontier(ctx, e.SessionID)
    if err == nil && frontier > st.Turn { st.Turn = frontier }        // resume
    if err != nil { o.soft("segment.frontier", err) }
4.  o.ensureSegment(ctx, st, e.SessionID, now)
5.  switch e.Source {
    case "compact":
        if o.opt.Rehydrate != nil { return o.opt.Rehydrate.OnCompact(ctx, e) }
        o.opt.Log.Info("rehydrator not built yet; startup bookkeeping only", "source", e.Source)
        return hookio.Empty(), nil
    case "clear":
        if o.opt.Rehydrate != nil { return o.opt.Rehydrate.OnClear(ctx, e) }
        return hookio.Empty(), nil
    default:                                       // "startup", "resume", ""
        o.opt.Log.Info("session registered", "session", e.SessionID, "source", e.Source,
                       "turn", st.Turn, "segment", st.Segment)
        return hookio.Empty(), nil
    }
```

`ensureSegment`: `cur, err := Segments().Current(ctx, s)`; if `err == nil && !cur.Closed` then
`st.Segment = cur.ID` and `st.SegStartTurn = cur.StartTurn`; otherwise
`st.PrevSegment = st.Segment` and `id, err := Segments().Open(ctx, store.Segment{Session: s,
StartTurn: st.Turn, StartTS: now, Features: map[string]float64{}, Closed: false})`, then
`st.Segment, st.SegStartTurn = id, st.Turn`. Either way `st.SegStartPos = st.PrefixTokens`, which is
`dag.SegmentSpec.StartPos` — the opening token position that makes a segment boundary a position
`CrossingEdges` can be asked about. On the resume path `PrefixTokens` has just been rehydrated from
`state/observer.json`, so the value is the real prefix offset rather than zero. An `Open` failure is
`soft("segment.open")` and leaves `st.Segment = 0`, which every call site tolerates (`enrol` emits
nothing, and step 1 of `OnSessionEnd` skips). **Closing on a changepoint is SP-12's**; the only
close SP-08 performs is the session-end close below.

**One obligation on the `startup`/`resume` branch lives outside this file: SP-09's
`RefreshStaleness`.** SP-09's out-of-scope table assigns this subplan *"Calling `RefreshStaleness`
from the `SessionStart` startup/resume branch — in `internal/daemon/observer_ops.go`, around SP-08's
`OnSessionStart`, never inside `internal/observer`"*, because it is the only wave-2 production caller
of the mechanism behind SP-09's §12 High-severity staleness row. `OnSessionStart` itself does **not**
make that call and must not: the exit criterion at the end of this document forbids
`internal/observer` importing `negknow` at all. It is the wrapped `s.SessionStart` seam in
`observer_ops.go` that calls `s.Ledger.RefreshStaleness(ctx, s.Store)` after `OnSessionStart`
returns, nil-tolerant on both `Ledger` and `Store`, gated on `e.Source` being neither `"compact"` nor
`"clear"`. It is recorded here so a reader of this branch does not conclude the obligation was
dropped.

`OnSessionEnd`, in this exact order (§7.3 "Flush, compact the store, write session index"), after
the same preamble — `ctx` check, `now := o.now()`, `st := o.session(...)`, `st.mu.Lock()` **without**
a `defer`, because step 7 releases it explicitly before touching `o.mu`:

```
1.  if st.Segment != 0 {
        feats := map[string]float64{}
        if fs, ok := o.features(st, now); ok {
            feats = map[string]float64{"path_jaccard": fs.PathJaccard, "tool_shift": fs.ToolShift,
                "lexical_cohesion": fs.LexicalCohesion, "gap_seconds": fs.GapSeconds,
                "todo_transition": fs.TodoTransition}
        }
        dag.BuildSegment(o.opt.Graph, dag.SegmentSpec{      // the segment NODE and the chain edge
            ID: st.Segment, PrevID: st.PrevSegment,
            StartTurn: st.SegStartTurn, EndTurn: st.Turn, TS: now,
            StartPos: st.SegStartPos,
            Tokens: core.Tokens(st.PrefixTokens - st.SegStartPos),
            Members: nil})                                   // membership was enrolled incrementally
        Segments().Close(ctx, st.Segment, st.Turn, feats)     // the session-index write
    }
2.  Graph.Flush(ctx)                                          // dag/deps.jsonl
3.  Store.Flush(ctx)                                          // §7.3 "Flush"
4.  o.saveSketches()                                          // sketches/touch.cms, explore.hll
5.  o.persistState()                                          // state/observer.json
6.  rep, err := Store.GC(ctx, store.GCPolicy{
        RetainDays:     o.opt.Cfg.Store.Retention.Days,
        RetainSessions: o.opt.Cfg.Store.Retention.Sessions,
        DryRun: false, Deadline: gcDeadline})                  // §8.2 "run on SessionEnd"
    if err != nil { o.soft("gc", err) } else {
        o.opt.Log.Info("gc", "scanned", rep.ScannedObjects, "deleted", rep.DeletedObjects,
                       "freed", rep.BytesFreed, "truncated", rep.Truncated)
    }
7.  st.mu.Unlock() (release before taking o.mu — decision 9's lock order), then
    o.mu.Lock(); delete(o.sess, e.SessionID); o.mu.Unlock()
    // written as an explicit unlock rather than `defer`, precisely because this step must not run
    // while o.mu is wanted; the method returns immediately after.
8.  return hookio.Empty(), nil
```

`BuildSegment` is called with `Members: nil` because `graph.go`'s `enrol` already emitted one
`member → segment` `EdgeSequence` per node as it arrived (legal under D-6, which makes an edge that
precedes its endpoints a normal interleaving). What is left for close time is the segment node
itself and the `previous → current` chain edge, which is what makes a boundary visible to
`CrossingEdges`: §8.4 scores a cut point by how many edges straddle it, and a boundary whose only
crossing edge is the chain link is exactly the cheap cut the scheduler hunts for. `st.PrevSegment`
is 0 for a project's first segment — `SegmentID` is 1-based precisely so 0 can mean "none".

`saveSketches` writes `filepath.Join(root, ".qompack", "sketches", "touch.cms")` and
`"explore.hll"` via `sketch.Save`, skipping nil sketches, softing errors. It **never** writes
`tried.bloom` — §3.3 reserves that file for `negknow.RebuildBloom`.

**Which of the two writers owns the sketch files.** SP-05's `handleFlush` also calls
`SketchSet.Save` after it calls the `SessionEnd` seam, so both run on one `flush`. The ownership is
SP-08's, and it is not a coin toss: `SketchSet.Save` returns early unless `SketchSet.dirty` is set,
and only `SketchSet.Write` sets it — whereas the observer mutates the sketches through the raw
`*sketch.CMS` / `*sketch.HLL` / `*sketch.MisraGries` pointers `WireObserver` hands it (see
`observer_ops.go`), so `dirty` stays false and `handleFlush`'s call is a no-op. Step 4 above is
therefore the write that actually happens. Both writers go through `sketch.Save` to the same two
paths, so even when something else in the daemon has dirtied the set the second write is idempotent
and neither can produce a half-file. `TestOnSessionEnd_Order` pins step 4's position;
`TestE2E_ObserverThroughDaemon` pins the files' existence after a real `flush` through the daemon.

Steps 1–6 are individually softed: a GC failure never prevents the state file from having been
written, and a segment-close failure never prevents the flush.

---

### `internal/observer/state.go` (new)

**Responsibility.** Crash-tolerant resume of turn index, prefix position and segment id.

File: `<root>/.qompack/state/observer.json`, written with `paths.WriteAtomic` (not append-only —
`state/` is daemon-persisted scratch, §3.3).

```jsonc
{
  "version": 1,
  "sessions": {
    "01J8…": { "turn": 41, "prefix_tokens": 128340, "segment": 7, "prev_segment": 6,
               "seg_start_turn": 33, "seg_start_pos": 104880,
               "last_ts": 1723406400123, "last_tool_use_id": "toolu_01A",
               "last_tool_use_turn": 40, "subagent_since": 12,
               "todo_done": ["migrate schema"], "tool_uses": [
                 {"id":"toolu_01A","root":"sha256:ab…","tool":"FileRead","path":"src/auth.ts","bytes":2458}
               ] }
  }
}
```

```go
const stateVersion = 1

type persistedState struct {
    Version  int                         `json:"version"`
    Sessions map[string]persistedSession `json:"sessions"`
}
type persistedSession struct {
    Turn         core.TurnIndex   `json:"turn"`
    PrefixTokens int              `json:"prefix_tokens"`
    Segment      core.SegmentID   `json:"segment"`
    PrevSegment  core.SegmentID   `json:"prev_segment"`
    SegStartTurn core.TurnIndex   `json:"seg_start_turn"`
    SegStartPos  int              `json:"seg_start_pos"`
    LastTS       core.UnixMilli   `json:"last_ts"`
    LastToolUseID   string        `json:"last_tool_use_id"`     // the ToolUseID, NOT a dag.NodeID
    LastToolUseTurn core.TurnIndex `json:"last_tool_use_turn"`
    SubagentSince int             `json:"subagent_since"`
    TodoDone     []string         `json:"todo_done"`
    ToolUses     []persistedToolUse `json:"tool_uses"`
}
type persistedToolUse struct {
    ID    string `json:"id"`
    Root  string `json:"root"`          // core.Hash.String() form: "sha256:…"
    Tool  string `json:"tool"`
    Path  string `json:"path"`
    Bytes int64  `json:"bytes"`
}
func (o *observer) loadState()
func (o *observer) persistState()
func (o *observer) Persist(ctx context.Context) error   // loadState-free; persistState + Graph.Flush
```

`Recent`, `WarnedRules` and `PendingThrash` are deliberately **not** persisted: the first is a
feature window that legitimately restarts cold after a daemon restart, and the other two are
warning-dedup state whose worst failure is one repeated thrash line. `TodoDone` is persisted as a
sorted slice and rehydrated into the map so the file is diffable and byte-stable across runs.
`Root` round-trips through `core.ParseHash`; a parse failure drops that one entry and logs `Debug`.
A `Version` other than `stateVersion` is treated exactly like a corrupt file (rename + fresh state),
which is the migration path for any future field change.

`loadState` runs at most once per process (`sync.Once`), tolerates a missing file (fresh state),
and on a JSON error logs `Warn`, renames the file to `observer.json.bad`, and starts fresh —
never blocks a session. `tool_uses` is capped at `subagentWindowCap` entries on write.
Sessions absent from disk start zeroed. `Persist` is what the daemon's `IdleController` registers,
so state survives a daemon kill between `SessionEnd`s.

---

### `internal/daemon/observer_ops.go` (new — the one file SP-08 adds outside its own package)

**Responsibility.** Attach the observer to SP-05's late-bound `Services` seams without editing
daemon internals and without touching the op-routing table, exactly as §5.4's extension-seam note
prescribes and as SP-12 will later do with `scheduler_runtime.go`.

```go
package daemon

// WireObserver constructs the L0 observer over the daemon's live services and binds it to the five
// L0 function seams. Called from runDaemon (internal/cli/daemon.go) on the addressable Options,
// BEFORE daemon.New: New applies o.binds inside itself, so a Bind registered afterwards never runs.
func WireObserver(o *Options) (observer.Observer, error)

// RegisterObserverIdleWork attaches the observer's O3 background work. Called from runDaemon
// immediately AFTER daemon.New succeeds — the same post-New shape SP-10's WireCheckpoint and
// SP-12's RegisterSchedulerIdleWork use.
func RegisterObserverIdleWork(d Daemon, obsv observer.Observer)

type symbolAdapter struct{ ex symbols.Extractor }   // observer must not import `symbols` (§3.2)
func (a symbolAdapter) Names(path string, b []byte) []string
```

**Two functions, not one, and no `*SessionRegistry` parameter.** The split is forced by SP-05's own
construction order, not by taste. `daemon.New` applies every bound function inside itself
(`for _, bind := range o.binds { bind(svc) }`, `internal/daemon/daemon.go`) and only afterwards
builds the session registry and the idle controller, which it exposes through `d.Registry()` and
`d.Idle()` on the `Daemon` interface. So the `Bind` half must run **before** `New` and the idle half
**after** it, and neither a registry nor an `IdleController` is reachable from `Options` at all —
`Options` carries no registry field and `Idle()` is a method on the daemon, not on `Options`. The
dropped `s *SessionRegistry` parameter was dead in any case: the wiring below does **not** write to
the session registry, and a caller-built `NewSessionRegistry()` would be a different object from the
one the daemon serves from.

**`Bind`, never `Handle`.** The wiring is one call:

```go
var modeSrc func() contract.Mode                          // filled by the bind body, read per event
obsv, err := observer.New(observer.Options{ /* … as below … */ })
if err != nil { return nil, err }
o.Bind(func(s *Services) {
    modeSrc = s.Mode                                      // SP-05 assigns svc.Mode before this runs
    s.ObserveTool   = func(ctx context.Context, e hookio.Event) error {
        _, err := obsv.OnToolUse(ctx, e); return err
    }
    s.ObservePrompt = obsv.OnUserPrompt                       // (hookio.Output, error) — matches
    s.ObserveStop   = func(ctx context.Context, e hookio.Event, subagent bool) error {
        _, err := obsv.OnStop(ctx, e, subagent); return err
    }
    s.SessionStart  = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
        out, err := obsv.OnSessionStart(ctx, e)
        if s.Ledger != nil && s.Store != nil && e.Source != "compact" && e.Source != "clear" {
            if _, rErr := s.Ledger.RefreshStaleness(ctx, s.Store); rErr != nil {
                o.Log.Warn("negknow: staleness refresh failed", "err", rErr.Error())
            }
        }
        return out, err
    }
    s.SessionEnd    = func(ctx context.Context, e hookio.Event) error {
        _, err := obsv.OnSessionEnd(ctx, e); return err
    }
})
```

`ObserveTool`, `ObserveStop` and `SessionEnd` return **only `error`** (`internal/daemon/options.go`);
their `hookio.Output` is discarded, which costs nothing because resolved decision 7 already fixes
those three at `hookio.Empty()`. `ObservePrompt` and `SessionStart` return `(hookio.Output, error)`
— those are the two entry points that may emit output — so `ObservePrompt` is assigned directly and
`SessionStart` is wrapped only to carry the output through unchanged (see the next paragraph).

**`SessionStart` is the one seam that is wrapped rather than assigned, and SP-09 is why.**
SP-09's out-of-scope table hands `RefreshStaleness`'s *only* wave-2 production caller to SP-08:
*"Calling `RefreshStaleness` from the `SessionStart` startup/resume branch — in
`internal/daemon/observer_ops.go`, around SP-08's `OnSessionStart`, never inside `internal/observer`"*.
It cannot live in `internal/observer`: the exit criterion at the end of this document forbids that
package importing `negknow` at all, so the wrapper above is the only sanctioned seam. The call is
nil-tolerant on both sides — a nil `Services.Ledger` and a nil `Services.Store` are both legitimate
in a stub build — it runs on the `startup`/`resume`/`""` branches only (the `compact` and `clear`
sources delegate to the rehydrator and are SP-11's), it never fails the hook, and its error is a
`Warn`, not a return. Without it wave 2 closes with SP-09's §12 High-severity staleness flip having
no production caller at all: V3-VERIFY's X2 drives `led.RefreshStaleness(ctx, st)` from the test
body, which proves the ledger works and proves nothing about whether anything calls it.

**SP-08 registers no ops through `Handle`, and that is not a style preference.** `Options.Handle`
*"registers h as the handler for op, replacing any previous registration"*, and `buildRoutes` copies
`o.Ops()` first, filling from `defaultRoutes` only for ops nobody claimed. SP-05's five default
routes are where the durability machinery lives: `handleObserveTool`/`handleObserveStop` are
`registry.Touch` then, unless the mode is `ModeOff`, `ingest.Accept` — *the WAL append that
00-ARCHITECTURE §2.4 makes the durability boundary*, and the reason ACK can be sent before any
indexing work. `handleObservePrompt` adds the synchronous reply inside `promptReplyDeadline`;
`handleFlush` is `registry.End`, `ingest.CloseSession`, the `SessionEnd` seam, `contract.WriteMarker`,
`SketchSet.Save` and `Drain`. Overriding any of them would delete WAL-before-ACK (E6's
`TestIngestACKPrecedesProcessing`, budget B-B), the hot-path breach/spool submode (E8), the session
registry, the terminal-hook marker (E10's `TestMarkerIsWrittenByFlushAndCheckpointOnly`) and the
mode-enforcement table's row 1 — and it could not put them back, because `ingest` is an unexported
field of an unexported struct that nothing outside SP-05's own files can reach. It would also leave
the four `Services` seams SP-05 built for exactly this purpose permanently nil. `Bind` is the
sanctioned seam: *"This is the seam a wave-2/3 subplan uses to attach its own function seams
(ObserveTool, SessionStart, …) without editing daemon internals or colliding with a sibling
subplan."* If a route override is ever genuinely wanted, SP-05 must first export a way for the
override to reach `ingest.Accept`; that is an `arch/` amendment, not a line in this file.

Two things follow. The observer never builds an `ipc.Response`, so it never names a monitor or the
registry's hot-path submode — `SessionRegistry` spells that `HotMode()`, not `HotPathMode()`, and
SP-08 calls neither. The contract mode still reaches the observer, but as a `func()` handed in
through `Services.Mode`, never as a monitor this file dereferences. And panic recovery stays where SP-05 put it, on the route side, rather than
being re-implemented per handler here.

- `symbolAdapter.Names` calls `a.ex.Extract(path, b)` and returns the deduplicated `Name` fields in
  first-appearance order.
- `Mode` is `func() observer.Mode { if modeSrc != nil && modeSrc() == contract.ModeFull { return
  observer.ModeFull }; return observer.ModePassive }` — over the `modeSrc` variable the `Bind` body
  fills, **not** over a monitor named here. There is no monitor to name at this point in the
  program: `contract.NewMonitor` runs inside `daemon.New`, into the unexported `d.monitor` field,
  and appears on neither `Options` nor the `Daemon` interface, so `WireObserver` — which must
  return before `New` is called at all — provably cannot reach one. `Services.Mode` (§5.4) is the
  seam that closes the gap, and it runs in the opposite direction to the five this file binds:
  SP-05 **provides** it, SP-08 **consumes** it. The nil guard is not decoration — `modeSrc` is nil
  in any test that builds the observer without ever calling `daemon.New`, and a nil check that
  falls through to `ModePassive` is the safe default (§12.1: when in doubt, do not act).
- `Services.Mode` is the one **SP-05-owned line SP-08 adds**, under Rule W-3 (widen, never rename):
  the field on `Services` in `internal/daemon/options.go`, and the two moved lines in
  `internal/daemon/daemon.go` that hoist `statePath` / `contract.NewMonitor` above the
  `for _, bind := range o.binds` loop and assign `svc.Mode = monitor.Mode` before it. The hoist is
  safe because `NewMonitor` reads only `o.ProjectRoot`, `o.Log` and `o.Metrics` — nothing a bind
  produces — and it is required because a bind body that runs before the monitor exists would
  capture a nil `s.Mode` and report `ModePassive` forever. `TestWireObserver` asserts the wiring by
  building a real daemon and checking the observer reports `ModeFull` on a fresh project.
- `OnFeatures` maps `observer.FeatureSample` field-for-field into `scheduler.Features` and calls
  `o.Sched.Observe(ctx, f, fs.Turn)` **when `o.Sched != nil`** (it is nil through wave 2).
- `OnSignals` logs the three booleans at `Debug` and, when `o.Sched != nil`, forwards them as a
  `scheduler.Features{TodoTransition: 1}` nudge on `TodoCompleted || TestPassed || GitCommit` — the
  G1.5 task-boundary delivery. It does **not** write to the session registry: `SessionState` carries
  no signal fields, and adding one would be a Rule W-3 change to SP-05's type.
- Sketch pointers come from `o.Sketches` (`*daemon.SketchSet`, SP-05): pass its `Touch`,
  `Explore` and `Top` members as the observer's `Touch`/`Explore`/`Hot`. They are handed over as raw
  pointers rather than through `SketchSet.Write`, which is why `OnSessionEnd` owns the sketch write
  (see `session.go`). If SP-05 named those members differently at merge time, adapt **only in this
  file** — never in `internal/observer`.
- The idle registration is `RegisterObserverIdleWork`'s whole body, not `WireObserver`'s:
  `if p, ok := obsv.(observer.Persister); ok { d.Idle().Register("observer.persist", 50, p.Persist) }`,
  so O3 idle time flushes the DAG and the state file. Priority `50` is mid-band: below SP-12's
  frontier advancement, above GC. It cannot sit in `WireObserver`: the `IdleController` is reached
  through `Daemon.Idle()`, and the daemon does not exist until `daemon.New` has returned.

---

## Test plan (TDD)

Every test below is written and run (failing) before the implementation it covers, inside the
commit that introduces it. Fixtures come from `internal/testutil` (SP-01) and
`testdata/corpora/toolout/` (SP-04). Fakes live in `internal/observer/fakes_test.go`:
`fakeStore` (in-memory, records every call), `fakeGraph`, `fakeGrammar`, `panicBloomStore`,
`fakeSymbols`, and `testutil.FakeClock`.

`fakeGraph` records nodes and edges **and implements the full `dag.Graph` interface, `Out` and `In`
included**. That is not optional bookkeeping: `dag.BuildToolUse` calls `turnLinkKind`, which calls
`sharesState`, which reads the graph back through `Out` and `In` to decide `EdgeSequence` versus
`EdgeControlOnly`. A double that returns nil from both would make every turn link control-only and
would silently invalidate `TestGraph_TurnLinkIsSequenceWhenAFileIsShared`. The reference behaviour
is `dag.Open(t.TempDir(), config.Defaults(), logging.Nop())`, and the graph rows that assert on edge
kinds run against that real graph rather than the recorder, with `fakeGraph` reserved for the rows
that only count calls (`TestGraph_FlushNotCalledPerToolUse`, the nil-tolerance rows).

### `tombstone_test.go`

| Test | Setup / input | Expected output |
|---|---|---|
| `TestTombstone_DesignExample` | `ToolUseRecord{Root: hash whose hex starts `a3f2c19d0b74`, Bytes: 2458, Tool: "FileRead", Path: "src/auth.ts"}` | `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]` |
| `TestTombstone_NoPathUsesArgsPreview` | `Tool: "Bash"`, `Path: ""`, `ArgsPreview: "go test ./internal/store/..."`, `Bytes: 812` | `[cleared: sha256:…… · 812B · Bash go test ./internal/store/... · re-expandable]` |
| `TestTombstone_LongPreviewTruncatedTo48Runes` | `ArgsPreview` of 200 ASCII chars | subject is exactly 47 runes + `…` |
| `TestTombstone_MegabyteSize` | `Bytes: 3_500_000` | contains ` · 3.3MB · ` |
| `TestTombstone_GigabyteSize` | `Bytes: 3 << 30` | contains ` · 3.0GB · ` — the shipped `humanBytes` GB tier survives the extension |
| `TestTombstone_SupersededMarker` | `Status: StatusSuperseded` | `… · superseded · re-expandable]` |
| `TestTombstone_EphemeralMarker` | `Ephemeral: true` | `… · ephemeral · re-expandable]` |
| `TestTombstone_BothMarkers` | ephemeral + superseded | `… · ephemeral · superseded · re-expandable]` |
| `TestTombstone_NoSubject` | `Path: ""`, `ArgsPreview: ""` | `[cleared: sha256:…… · 0B · Bash · re-expandable]` (no double space) |
| `TestTombstoneGolden` | 13 records covering every branch, size tiers B/KB/MB/GB included | byte-identical to `testdata/golden/observer/tombstones.txt` |
| `TestTombstoneNote_SingleLine` | — | contains `expand`, contains no `\n` |
| `TestNormalizeToolName` | table: Read→FileRead, MultiEdit→FileEdit, Write→FileWrite, Task→AgentTool, `mcp__qompack__recall`→unchanged | as stated |
| `TestIsCompactable` | the nine §2.2 names → true; `AgentTool`, `TodoWrite`, `mcp__qompack__expand` → false | as stated |
| `TestSupersedableClass` | FileRead/FileEdit/FileWrite→`filecontent`; Grep/Glob→`search`; Bash/PowerShell→`exec`; WebFetch/WebSearch→`web`; AgentTool→`""` | as stated |
| **`BenchmarkTombstone`** | one record | **< 2 µs/op, 0 allocations beyond the returned string** (contributes to B-C) |

### `signals_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestExtractSignals_TodoCompleted` | `TodoWrite`, input `{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"pending"}]}` | `TodoCompleted: true` |
| `TestExtractSignals_TodoNoneCompleted` | all `pending`/`in_progress` | `TodoCompleted: false` |
| `TestExtractTestOutcome_GoPass` | Bash, `go test ./...`, response `ok  	github.com/x/y	0.31s\n` | `TestPass` |
| `TestExtractTestOutcome_GoFail` | same command, response `--- FAIL: TestX\nFAIL\n` | `TestFail` |
| `TestExtractTestOutcome_JestPass` | `npm test`, `Tests:       42 passed, 42 total` | `TestPass` |
| `TestExtractTestOutcome_PytestMixed` | `pytest -q`, `2 failed, 40 passed` | `TestFail` (fail wins) |
| `TestExtractTestOutcome_CargoPass` | `cargo test`, `test result: ok. 12 passed; 0 failed` | `TestPass` |
| `TestExtractTestOutcome_NotATestCommand` | `ls -la`, response `PASS` | `TestUnknown` |
| `TestExtractSignals_GitCommit` | Bash, `git commit -m "x"`, response `[main 3f2a1] x\n 2 files changed` | `GitCommit: true` |
| `TestExtractSignals_GitCommitNoop` | same command, response `nothing to commit, working tree clean` | `GitCommit: false` |
| `TestExtractSignals_GitCommitInChain` | `git add -A && git commit -m x` | `GitCommit: true` |
| `TestPathsFromInput_Read` | `{"file_path":"src/a.ts"}` | `["src/a.ts"]` |
| `TestPathsFromInput_MultiEdit` | `{"file_path":"a","edits":[{"file_path":"b"},{"file_path":"a"}]}` | `["a","b"]` (dedup, order kept) |
| `TestPathsFromInput_Glob` | `{"pattern":"**/*.go","path":"internal"}` | `["internal"]` |
| `TestExtractSignals_MalformedJSON` | `ToolInput: []byte("{not json")` | zero `Signals`, no panic |
| `TestResponseText_AllFourShapes` | string / `{"content":"x"}` / `{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}` / `{"stdout":"o","stderr":"e"}` | `x`, `x`, `a\nb`, `o\ne` |
| `FuzzExtractSignals` | seeded from `testdata/corpora/toolout/` | never panics; always returns a `Signals` whose `Paths` are all valid UTF-8 |

### `tooluse_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnToolUse_StoresAndIndexes` | fakeStore, `Read src/auth.ts` with 2 KB body | one `PutBytes` with `Tool:"FileRead"`, `Path:"src/auth.ts"`; one `RecordToolUse` whose `Root` equals the `PutResult` root and `Turn == 0` |
| `TestOnToolUse_CanonOptionsAlwaysIncludeCRLF` | config with `canonicalize.enabled=false` | `PutOptions.Canon.Strip == []canon.Class{"crlf"}`, `MinHash.Enabled == false` |
| `TestOnToolUse_CanonOptionsFromConfig` | default config | `Strip` == `{crlf, timestamps, ansi, pids, addresses, tmpPaths, durations}`, `MinHash{Enabled:true, Permutations:128, NearDupThreshold:0.9}` |
| `TestOnToolUse_FileVersionAppendedForFileContent` | `Read` then `Edit` on the same path | two `AppendFileVersion` calls, both with the normalized key |
| `TestOnToolUse_NoFileVersionForGrep` | `Grep` with `path` | zero `AppendFileVersion` calls |
| `TestOnToolUse_ArgsDigestAndPreview` | Bash `{"command":"go test ./..."}` | `ArgsPreview == "go test ./..."`; `ArgsDigest`/`ArgsPreview` deep-equal `store.ArgsDigest(e.ToolInput)` — asserted against that call, never against a locally recomputed digest |
| `TestOnToolUse_ArgsDigestIsKeyOrderInvariant` | the same two arguments submitted as `{"a":1,"command":"x"}` and `{"command":"x","a":1}` | one `ArgsDigest` value, proving the observer did not re-derive it with `json.Compact` (mirrors `store.TestArgsDigest_KeyOrderInvariant` at the production call site) |
| `TestOnToolUse_PreviewTruncatedByStore` | 500-char command | `len(preview) <= 120` bytes and it ends in `…`; the cap is §5.8's, not the observer's |
| `TestOnToolUse_MCPResultIsEphemeral` | `tool_name: "mcp__qompack__expand"` | record `Ephemeral: true`; zero CMS/HLL/MG adds; no supersession scan |
| `TestOnToolUse_EmptyResponseStillIndexed` | `ToolResponse: null` | one `RecordToolUse` with `Bytes: 0`, zero `PutBytes` |
| `TestOnToolUse_PutFailureIsSoft` | fakeStore returns error from `PutBytes` | returns `hookio.Empty(), nil`; `observer.err.put` counter == 1; zero `RecordToolUse` |
| `TestOnToolUse_IndexFailureStillFeedsSketchesAndDAG` | `RecordToolUse` errors | CMS add count == 2, DAG nodes == 2, `observer.err.index` == 1 |
| `TestOnToolUse_CancelledContext` | pre-cancelled ctx | returns `ctx.Err()`, zero store calls |
| `TestOnToolUse_OversizePayloadTruncated` | 4 MiB body, `runtime.hotPath.maxPayloadBytes` = 1 MiB | `PutBytes` receives exactly 1 MiB |
| `TestOnToolUse_SignalsDelivered` | Bash `git commit`, `OnSignals` captured | callback fired once with `GitCommit: true`, `TestPassed: false` |
| `TestOnToolUse_TodoTransitionOnlyOnce` | same `TodoWrite` payload twice | `TodoCompleted` true then false |
| `TestOnToolUse_ReturnsEmptyOutput` | any event | `hookio.Output` deep-equals `hookio.Empty()` |
| `TestOnToolUse_RemembersToolUseForSubagentWindow` | 3 tool uses | `st.ToolUses` has 3 entries with matching ids, roots, tools, paths |
| `TestOnToolUse_ToolUseRingEvictsAndClampsSubagentSince` | `subagentWindowCap+10` tool uses after a prompt | `len(st.ToolUses) == subagentWindowCap`; `st.SubagentSince == 0`; the following `SubagentStop` slices without panicking |
| `TestOnToolUse_LastTSIsPreviousEventTS` | two events 45 s apart on `FakeClock` | the second call's `FeatureSample.GapSeconds == 45`, i.e. `LastTS` was still the first event's when `features` ran |
| `TestModePassiveStillWrites` (decision 10) | drive the same 20-event session twice, `Mode()` returning `ModeFull` then `ModePassive` | identical `PutBytes`/`RecordToolUse`/`AddNode`/`AddEdge`/CMS call counts; only `OnUserPrompt`'s `AdditionalContext` differs |
| `TestOnToolUse_ConcurrentSessionsRaceFree` | two goroutines × 200 events on two different session ids, `-race` | no race report; each session's `Turn` and `PrefixTokens` are exactly what a sequential run produces |
| **`BenchmarkOnToolUse_FileRead64KB`** | real store on `t.TempDir()`, 64 KB Go source body | **p50 < 5 ms, p99 < 50 ms (budget B-C `l0_process`)**; reported in the commit message |
| **`BenchmarkOnToolUse_TestOutput256KB`** | 256 KB noisy `go test` output from `testdata/corpora/toolout/` | **p99 < 50 ms (B-C)** |

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

### `graph_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestGraph_ToolUseProducesResult` | one tool use | nodes `dag.ToolUseNode(id)` (KindToolUse, `Ref == "FileRead"`) and `dag.ToolResultNode(id)` (KindToolResult); one `EdgeProduces` between them |
| `TestGraph_SequenceChainAcrossTurns` | tool use in turn 0, prompt, tool use in turn 2, both touching `a.ts` | `EdgeConsumes` `toolresult:<first> → dag.AssistantNode(2)` and `EdgeSequence` `dag.AssistantNode(2) → tooluse:<second>` |
| `TestGraph_ParallelSiblingsShareATurnAndGetNoConsumesEdge` | two tool uses in **one** turn (decision 4) | **zero** `toolresult:<first> → assistant:*` edges; both tool uses hang off the same `dag.AssistantNode(turn)`; the emitted subgraph is acyclic (D-7's `tooluse → toolresult → assistant → tooluse` cycle never forms) |
| `TestGraph_ObserverOutputIsAcyclic` | 200-event mixed session through `emitToolGraph`, `OnUserPrompt` and `OnStop` | a topological sort of every emitted edge succeeds — the observer-level counterpart of `dag.TestBuilderOutputIsAcyclic`, which guards the builders only |
| `TestGraph_TurnLinkIsControlOnlyWhenNothingIsShared` | `Read a.ts` then `Bash` with no path or symbol overlap | the second call's `assistant → tooluse` edge is `EdgeControlOnly`, so §6.4 thin slicing has something to drop on observer-produced graphs |
| `TestGraph_TurnLinkIsSequenceWhenAFileIsShared` | `Read a.ts` then `Edit a.ts` in a later turn | that edge is `EdgeSequence`, not `EdgeControlOnly` |
| `TestGraph_FirstToolUseOfASessionIsSequence` | one tool use, no predecessor | `EdgeSequence` — a control-only head would make the opening turn unreachable under a thin slice |
| `TestGraph_SharedFileOrientation` | `Read a.ts` then `Write a.ts` | one `dag.FileNode("a.ts")`; `EdgeSharedFile` `file:a.ts → tooluse:<read>` and `tooluse:<write> → file:a.ts` (D-1: a read consumes the anchor, a write produces it) |
| `TestGraph_NoToolUseToToolUseSharedFileEdge` | two reads of one path | both couple through the shared `file:` node; **zero** `EdgeSharedFile` edges whose endpoints are both tool-use nodes |
| `TestGraph_SymbolEdges` | fakeSymbols returning `["refreshToken","parseJWT"]` on `src/auth.ts` | nodes `dag.SymbolNode("src/auth.ts","parseJWT")` and `…"refreshToken"` — i.e. `symbol:src/auth.ts#parseJWT` — emitted in **ascending** name order by the builder, each with an `EdgeSharedSymbol` edge oriented like the file edge |
| `TestGraph_SymbolsSkippedAboveCap` | body of `symbolScanCap+1` bytes | `symbolNames` returns nil; zero symbol nodes |
| `TestGraph_SymbolsCappedAt64` | 200 distinct names | exactly 64 symbol nodes |
| `TestGraph_PosIsMonotoneAndPreIncrement` | three tool uses of 100, 200, 300 tokens | result node `Pos` values 0, 100, 300 |
| `TestGraph_SegmentMembershipPointsIntoTheSegment` | `st.Segment == 7` | `EdgeSequence` from the tool-use node **to** `dag.SegmentNode(7)`, matching `dag.BuildSegment`'s member direction — never `segment → tooluse` |
| `TestGraph_SegmentNodeAndChainEdgeAtClose` | segment 6 closed, segment 7 opened and closed | `dag.SegmentNode(7)` exists with `Ref == "<startTurn>-<endTurn>"`, and one `EdgeSequence` `segment:6 → segment:7` |
| `TestGraph_NilSymbolsTolerated` | `Options.Symbols == nil` | no panic, zero symbol nodes |
| `TestGraph_FlushNotCalledPerToolUse` | 10 tool uses | `fakeGraph.FlushCalls == 0` |
| `TestGraph_IDsComeFromDagConstructors` | one prompt, one tool use, one symbol, one segment | every emitted `Node.ID` and every edge endpoint is `require.Equal` against the corresponding `dag.ToolUseNode`/`ToolResultNode`/`AssistantNode`/`UserPromptNode`/`FileNode`/`SymbolNode`/`SegmentNode` call — in particular `assistant:<turn>` and `userprompt:<turn>` carry **no session component**, matching `testdata/golden/contracts/dag/graph-basic.jsonl` |

### `sketches_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestSketches_CMSFedWithPathAndTool` | one `Read src/a.ts` | `Touch.Estimate([]byte("p\x00src/a.ts")) == 1` and `Estimate([]byte("t\x00FileRead")) == 1` |
| `TestSketches_HLLCountsDistinctPaths` | reads of a.ts, b.ts, a.ts | `Explore.Cardinality() == 2` |
| `TestSketches_MisraGriesTopK` | 5 reads of a.ts, 2 of b.ts | `Hot.Top(1) == [{a.ts, 5}]` |
| `TestSketches_NoPathNoHLL` | Bash with no path | `Cardinality() == 0` |
| `TestSketches_EphemeralNotFed` | MCP result with a path | all three sketches unchanged |
| `TestSketches_NilTolerated` | all three nil | no panic |
| **`TestObserverNeverFeedsBloom`** | store wired with a `*sketch.Bloom` double whose `Add` panics; full 50-event session | no panic |
| **`TestObserverSourceHasNoBloomReference`** | parse every non-test `.go` file in `internal/observer` with `go/parser` | zero occurrences of the identifiers `Bloom`, `NewBloom`, `RebuildBloom` |

### `prompt_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnUserPrompt_StoresVerbatim` | prompt `"fix the pgbouncer 1.18 pool bypass"` | `PutBytes` receives exactly those bytes; `Canon.Strip != nil && len(Canon.Strip) == 0` (the empty-non-nil "no optional class" request, **not** `nil`, which `canon` reads as "every class"); `Canon.MinHash.Enabled == false`; `KeepRaw == true` |
| `TestOnUserPrompt_VerbatimAgainstARealStore` | real store on `t.TempDir()` with the six strip classes enabled in config; prompt containing a curly apostrophe, an ISO-8601 timestamp and `PID 4711` | `store.Open(root)` returns the prompt byte for byte, timestamp and PID intact, apostrophe intact — the assertion pre-step (a) exists for; a store without the amendment fails this row |
| `TestOnUserPrompt_RecordsIndexEntry` | same | `RecordToolUse` with `Tool == "UserPromptSubmit"`, `ID == "prompt_<session>_0"` |
| `TestOnUserPrompt_TurnIncrements` | two prompts | ids `prompt_s_0`, `prompt_s_1`; `st.Turn == 2` |
| `TestOnUserPrompt_DAGNodeAndSegmentEdge` | `st.Segment == 3`, first prompt of the session | node `dag.UserPromptNode(0)` (`userprompt:0` — no session component) of `KindUserPrompt`; `EdgeConsumes` `userprompt:0 → dag.AssistantNode(0)` from `dag.BuildUserPrompt`; `EdgeSequence` `userprompt:0 → dag.SegmentNode(3)` for membership |
| `TestOnUserPrompt_NeverRegenerated` | same prompt text submitted twice against a real store | two records, identical `Root`; `Stats().Objects` unchanged after the second |
| `TestOnUserPrompt_EmptyPromptIgnored` | `Prompt: ""` | zero store calls, `hookio.Empty()` |
| `TestOnUserPrompt_GrammarSymbolAppended` | fakeGrammar | `Append("user")` called once |
| `TestOnUserPrompt_ThrashWarningInFullMode` | fakeGrammar returning one rule with `Uses: 11` | output `HSO.AdditionalContext` non-empty, `HookEventName == "UserPromptSubmit"` |
| `TestOnUserPrompt_NoThrashWarningInPassiveMode` | same, `Mode() == ModePassive` | `hookio.Empty()` (§12 forbids injection when degraded) |
| `TestOnUserPrompt_ThrashWarnedOncePerRule` | same rule returned on two prompts | additionalContext on the first only |
| `TestOnUserPrompt_PutFailureStillIncrementsTurn` | `PutBytes` errors | `observer.err.prompt.put == 1`; `st.Turn == 1` |
| `TestVerbatimPromptID` | `("abc", 7)` | `"prompt_abc_7"` |

### `stop_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnStop_MainAgentIncrementsTurnAndFlushes` | `subagent=false` | `st.Turn` +1; `fakeGraph.FlushCalls == 1`; zero `PutBytes` |
| `TestOnStop_SubagentCapturesSummary` | `ToolResponse: {"content":"Found the bug in retry.ts"}` | `PutBytes` body unmarshals to a `SubagentCapture` with that `Summary` |
| `TestOnStop_SubagentCapturesToolHashes` | three tool uses recorded since the last prompt | `ToolResults` has 3 refs with the right ids, roots, tools, paths |
| `TestOnStop_SubagentWindowStartsAtLastPrompt` | prompt, 2 tool uses, subagent stop, 1 tool use, subagent stop | first capture 2 refs, second capture 1 ref |
| `TestOnStop_SubagentNameFromExtra` | `Extra["agent"] = "\"code-reviewer\""`, as pre-step (b)'s `resolveEvent` restores it from `Raw` | `Agent == "code-reviewer"`, and the same value on `ToolUseRecord.Subagent` |
| `TestOnStop_SubagentNameFallback` | no `Extra` keys, or `Extra["agent"]` holding a number or an object | `Agent == "subagent"` |
| `TestRawExtras_ResolvesTheSubagentNameClientSide` (`internal/cli`) | an `observe stop --subagent` payload whose `Extra` carries `subagent_type` (and separately: only `agent_name`; only `agent`; a numeric `subagent_type` plus a string `agent_name`; none of the three) | `rawExtras` emits `{"subagent":true,"agent":"<name>"}` for the first four in that preference order, and exactly `{"subagent":true}` for the last |
| `TestOnStop_SummaryFromTranscriptTail` | empty `ToolResponse`; temp JSONL whose last assistant line has two text blocks | `Summary == "block one\nblock two"` |
| `TestOnStop_TranscriptMissingIsSilent` | `TranscriptPath` points at a nonexistent file | `Summary == ""`, capture still written, no error |
| `TestOnStop_EmptySummaryStillStoresHashes` | no response, no transcript, 2 tool uses | capture written with 2 refs |
| `TestOnStop_ConsumesEdges` | 2 refs | two `EdgeConsumes`, each running `dag.ToolResultNode(ref) → dag.ToolUseNode(captureID)` — the refs are the earlier/producer end under D-1 — and none in the reverse direction |
| `TestOnStop_CaptureIsDeterministic` | two *independent* observers over a real store, each with a fresh session state, a `FakeClock` frozen at the same instant, the same session id and the same two preceding tool uses | both `PutBytes` bodies are byte-identical and produce the same root hash; `Stats().Objects` is unchanged after the second capture. (Within one session the capture is intentionally *not* repeatable: `Turn` advances and the id changes, which is what makes each capture addressable.) |
| `TestOnStop_RetrievalPathG10_1` | real store; capture then `store.ToolUse(SubagentCaptureID(...))` then `Open(root)` | JSON round-trips to the same `SubagentCapture` |
| `TestTailAssistantText_PartialFirstLine` | file whose truncation window starts mid-line | the partial line is discarded, the last complete assistant line is returned |

### `features_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestFeatures_NotReadyBeforeTwoWindows` | 15 events | `ok == false` |
| `TestFeatures_PathJaccardIdenticalWindows` | 16 events all touching `a.ts` | `PathJaccard == 1.0` |
| `TestFeatures_PathJaccardDisjointWindows` | first 8 on `a.ts`, next 8 on `b.ts` | `PathJaccard == 0.0` |
| `TestFeatures_PathJaccardBothEmpty` | 16 Bash events with no paths | `PathJaccard == 1.0` |
| `TestFeatures_ToolShiftIdentical` | all `Read` | `ToolShift == 0.0` |
| `TestFeatures_ToolShiftComplete` | 8 `Read` then 8 `Bash` | `ToolShift == 1.0` |
| `TestFeatures_ToolShiftHalf` | window A 8×Read; window B 4×Read+4×Bash | `ToolShift == 0.5` (±1e-9) |
| `TestFeatures_LexicalCohesionIdenticalText` | same body both windows | `LexicalCohesion == 1.0` (±1e-9) |
| `TestFeatures_LexicalCohesionDisjointVocabulary` | `"alpha beta"` vs `"gamma delta"` | `0.0` |
| `TestFeatures_GapSecondsFromFakeClock` | FakeClock advanced 45 s between the last two events | `GapSeconds == 45` |
| `TestFeatures_TodoTransition` | newest event is a `TodoWrite` with a newly completed item | `TodoTransition == 1.0` |
| `TestFeatures_AllFinite` (rapid) | random event streams | every field is finite and in its documented range |
| `TestFeatures_RecentRingBounded` | 1000 events | `len(st.Recent) == 16` |

### `session_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnSessionStart_StartupOpensSegment` | no current segment | one `SegmentLog.Open` with `StartTurn == 0`; `st.Segment` set |
| `TestOnSessionStart_ResumeReusesOpenSegment` | `Current` returns an open segment id 4 | zero `Open`; `st.Segment == 4` |
| `TestOnSessionStart_ResumeAdoptsFrontierTurn` | `Frontier` returns 37, state file absent | `st.Turn == 37` |
| `TestOnSessionStart_CompactDelegates` | `Source: "compact"`, fake Rehydrator | `OnCompact` called once; its output returned verbatim |
| `TestOnSessionStart_ClearDelegates` | `Source: "clear"` | `OnClear` called once |
| `TestOnSessionStart_CompactWithoutRehydratorIsEmpty` | `Rehydrate == nil` | `hookio.Empty()`, no error, segment still ensured |
| `TestOnSessionStart_UnknownSourceTreatedAsStartup` | `Source: "marble_origami"` | startup branch taken |
| `TestOnSessionEnd_Order` | recording fakes | call order exactly: `dag.BuildSegment` (segment node + `segment:<prev> → segment:<cur>` chain edge), `Segments().Close`, `Graph.Flush`, `Store.Flush`, `sketch.Save`×2, state write, `Store.GC` |
| `TestOnSessionEnd_GCPolicyFromConfig` | defaults | `GCPolicy{RetainDays:30, RetainSessions:10, DryRun:false, Deadline:8s}` |
| `TestOnSessionEnd_GCFailureIsSoft` | GC errors | returns `hookio.Empty(), nil`; `observer.err.gc == 1`; state file still written |
| `TestOnSessionEnd_NeverWritesTriedBloom` | real temp project | `.qompack/sketches/tried.bloom` does not exist after SessionEnd |
| `TestOnSessionEnd_SegmentClosedWithFeatures` | 16 prior events | `Close` receives `endTurn == st.Turn` and a 5-key feature map |
| `TestState_RoundTrip` | populate, `persistState`, new observer, `loadState` | turn, prefix tokens, segment, `prev_segment`, `seg_start_turn`, `seg_start_pos`, `last_tool_use_id`/`last_tool_use_turn` and the tool-use ring all restored |
| `TestState_ResumedPrevTurnStillGuardsTheCycle` | persist mid-session with `last_tool_use_turn == 7`, reload, then a tool use at turn 7 | no `toolresult → assistant` consumes edge (the parallel-sibling guard survives a daemon restart, because `PrevTurn` is persisted rather than recomputed as 0) |
| `TestState_CorruptFileRecovers` | write `{{{` to `state/observer.json` | fresh state; `observer.json.bad` exists; `Warn` logged; no error |
| `TestState_AtomicWrite` | inspect during write via `testutil` | no partial file observable at the target path |

### `test/e2e/observer_e2e_test.go` (new)

| Test | Setup | Expected |
|---|---|---|
| `TestE2E_ObserverThroughDaemon` | real binary, real daemon, real store on `t.TempDir()`; send `session-start`, 40 `observe tool`, 3 `observe prompt`, 1 `observe stop --subagent`, `flush` | every hook exits 0; `index/tool_use.jsonl` has 44 lines; `dag/deps.jsonl` non-empty; `sketches/touch.cms` and `explore.hll` exist; `sketches/tried.bloom` does not |
| `TestE2E_HooksExitZeroUnderFaultInjection` | make `.qompack/objects` read-only, then drive the same sequence | every hook exits 0; `LOUD.log` or the counter file records the failures |
| `TestE2E_SupersessionVisibleAfterRestart` | read a file twice, `flush`, restart daemon, read `tool_use.jsonl` | the first record carries `"status"` superseded and `superseded_by` of the second |
| `TestE2E_VerbatimPromptSurvivesRestart` | prompt containing a curly apostrophe, an ISO-8601 timestamp and a PID; flush; restart; `store.Open(root)` | bytes equal the original prompt exactly, with the volatile substrings intact — the end-to-end proof that pre-step (a) reached the real store |
| `TestE2E_SubagentNameReachesTheDaemon` | real binary; a `SubagentStop` payload carrying `subagent_type: "code-reviewer"` on stdin, driven through `qompack observe stop --subagent`, then `flush` | the `tool_use.jsonl` capture record's `subagent` field is `"code-reviewer"`, **not** `"subagent"` — the assertion the in-process unit test cannot make, because it is the IPC boundary that drops `Extra` |
| `TestE2E_ThinSliceDropsControlOnlyEdges` | 40 mixed tool calls through the real daemon, then read `dag/deps.jsonl` | at least one edge has `EdgeControlOnly`, and `BackwardSlice` with `Thin: true` returns strictly fewer nodes than with `Thin: false` — the property ADR 0007's 43%/88%/2.2× figures claim of production traffic |

### `test/e2e/phase1_exit_test.go` (new) — the Phase 1 exit criterion

**The call sequence is synthesized; the bytes are real.** `eval.Synthesize` is the right source for
the *shape* of a session — tool mix, re-read rate, changepoints, subagent calls — and the wrong
source for its *content*: every tool result it emits is a token count, `call.Result =
mustCompactJSON(map[string]int{"tokens": tokens})` (`internal/eval/synth.go`), and the committed
corpus shows it (`"result": {"tokens": 646}`, `"args": null`). Driving that through the observer
would measure roughly sixteen bytes per event with nothing volatile in them, which makes
`ratioOn == ratioOff` and the 1.25× canonicalization gate unpassable by any change to
`internal/observer`; `args: null` would also leave `pathKey` empty on every event, so no file
version, no supersession, no HLL or Misra-Gries feed and no shared-file or symbol edge would ever be
exercised — and the `FileRereadRate` the read-heavy spec turns on lives in `tc.Paths`, which the old
`eventsFor` never read.

So this harness pairs the synthesized sequence with **real tool output from
`testdata/corpora/toolout/`** — SP-04's committed bash, test-runner, grep, glob, git, ANSI, fileread
and webfetch captures, already listed under *Fixtures needed* below, and already the basis of
`test/dedup`'s honest with/without measurement:

```go
var readHeavy = eval.SynthSpec{
    Turns: 400,
    ToolMix: map[string]float64{"Read": 0.62, "Grep": 0.14, "Glob": 0.06, "Edit": 0.10, "Bash": 0.08},
    FileRereadRate: 0.55, TestOutputNoise: 0.0,
    Changepoints: 6, Eliminations: 3, SubagentCalls: 2,
    CompactionAt: []core.TurnIndex{180, 330},
}
var testOutputHeavy = eval.SynthSpec{
    Turns: 400,
    ToolMix: map[string]float64{"Bash": 0.55, "Read": 0.20, "Edit": 0.15, "Grep": 0.10},
    FileRereadRate: 0.25, TestOutputNoise: 0.85,
    Changepoints: 5, Eliminations: 4, SubagentCalls: 1,
    CompactionAt: []core.TurnIndex{200},
}
const seedReadHeavy, seedTestHeavy = 0x5108_0001, 0x5108_0002

// corpus loads testdata/corpora/toolout/<group>/*.txt once, keyed by the group directory name, in
// sorted filename order. Each file's sibling <name>.txt.meta.json carries {"tool":…,"path":…}; only
// the bytes are used here, the tool name comes from the synthesized call.
type corpus map[string][][]byte

// payloadFor picks the response bytes for one synthesized call, deterministically: the group is
// chosen from the tool name (Read/Edit → "fileread", Grep → "grep", Glob → "glob",
// Bash → "testrunner" for testOutputHeavy and "bash" for readHeavy, WebFetch → "webfetch"), and the
// file within the group is indexed by a hash of the call's first path — so re-reading a path
// re-serves the SAME bytes and FileRereadRate turns into real chunk reuse, while the "-v2" variants
// in the fileread and sp06 groups supply the "same file, two lines changed" case §6.1 names.
func payloadFor(c corpus, tool string, paths []string, seq int) []byte
```

**Driving the session through the observer.** `eval.Session` is a turn list, not a hook stream, so
the harness materializes hook events itself — one helper, no ambiguity:

```go
func eventsFor(s eval.Session, c corpus) []hookio.Event {
    var out []hookio.Event
    seq := 0
    for _, t := range s.Turns {
        if t.Role == "user" {
            out = append(out, hookio.Event{HookEventName: "UserPromptSubmit",
                SessionID: core.SessionID(s.ID), Prompt: t.Text})
            continue
        }
        for _, tc := range t.ToolCalls {
            // ToolInput is built from tc.Paths — eval.ToolCall keeps the paths in their own field
            // and leaves Args nil for ordinary calls, so without this the observer sees no path at
            // all and PathsFromInput returns nothing.
            in := json.RawMessage(`{}`)
            if len(tc.Paths) > 0 {
                in = mustJSON(map[string]string{"file_path": tc.Paths[0]})
            } else if len(tc.Args) > 0 {
                in = tc.Args
            }
            body := payloadFor(c, tc.Name, tc.Paths, seq)
            seq++
            out = append(out, hookio.Event{HookEventName: "PostToolUse",
                SessionID: core.SessionID(s.ID), ToolName: tc.Name, ToolUseID: tc.ID,
                ToolInput: in,
                ToolResponse: mustJSON(map[string]string{"content": string(body)})})
        }
    }
    return out
}
```

`{"file_path": …}` is the key `PathsFromInput` reads for `Read`/`Edit`/`Write`; for `Grep` and
`Glob` the helper emits `{"pattern":"…","path":paths[0]}` instead, matching those tools' real
payload shapes, and for `Bash` it emits `{"command":"go test ./..."}` with no path. The point is
only that a synthesized call arrives at `OnToolUse` looking like the hook payload it stands for.

The test calls `OnUserPrompt` for `UserPromptSubmit` events and `OnToolUse` for `PostToolUse`
events, in order, against a real `store.Open` on `t.TempDir()` with a `FakeClock` advancing 1 s per
event, then reads `store.Stats(ctx)`. **"Raw transcript" is `Stats().RawBytes`** (the pre-dedup byte
count SP-06 accumulates) and **"store size" is `Stats().Bytes`**; the ratio asserted is
`Stats().DedupRatio`, which §5.8 defines as `RawBytes / Bytes`. No other definition of the ratio is
used anywhere in this subplan.

`test/dedup` measures the same corpus at the canonicalizer level and is the cross-check: if
`TestPhase1_CanonicalizationGapOnTestOutput` and `test/dedup`'s `testrunnerGainFloor` (also 1.25)
disagree, the observer is doing something to the bytes on the way in, which is the bug to find —
not a reason to move either number.

| Test | Assertion |
|---|---|
| `TestPhase1_DedupRatioReadHeavy` | drive every `ToolCall` of `eval.Synthesize(seedReadHeavy, readHeavy)`, carrying `testdata/corpora/toolout/` bytes, through `observer.OnToolUse` against a real store with canonicalization **on**; `store.Stats().DedupRatio >= 4.0`. **This is the §10 Phase 1 exit criterion.** |
| `TestPhase1_CanonicalizationGapOnTestOutput` | run `testOutputHeavy` twice — once with `store.canonicalize.enabled=true`, once `false` — and assert `ratioOn >= ratioOff*1.25`. The ≥25% figure is this subplan's operational reading of *"the gap on test-output-heavy sessions justifies O2 on its own"*; both raw numbers are printed and written to `phase1-dedup.json` in the test's temp dir regardless of pass/fail. |
| `TestPhase1_PathsReachTheObserver` | the read-heavy run | `Stats().Files > 0`, at least one `AppendFileVersion`, at least one `MarkSuperseded`, and a non-zero HLL cardinality — the guard against a harness that silently stops exercising path-keyed behaviour, which is exactly how the previous `eventsFor` failed |
| `TestPhase1_ResponseBytesAreReal` | the read-heavy run | `Stats().RawBytes` divided by the tool-call count exceeds 1 KB, so the ratio is measured over real tool output rather than over `{"tokens":N}` envelopes |
| `TestPhase1_ReportArtifact` | the emitted JSON has keys `read_heavy_ratio_canon`, `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`, `raw_bytes`, `store_bytes`, `objects`, `tool_uses` |
| `TestPhase1_StoreGrowthSublinear` (§11.3 guardrail) | bytes stored over the second half of the read-heavy session are strictly less than over the first half |
| `TestPhase1_CorpusSweep` | when `testdata/sessions/synthetic/` exists, replay every session in it — through the same `eventsFor`, so its calls also carry corpus bytes — and log each ratio; informational, fails only if any session panics |

**Hook p99 < 15 ms** is the other half of the exit criterion and is measured by SP-05's existing
harness, now with a non-trivial handler behind it:

```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-observer.json
```

`TestPhase1_HotPathBudgetDocumented` asserts the committed ADR records a B-A p99 figure from the
three CI platforms; the gate itself is the `bench-gate` job, which fails the build if B-A p99 ≥ 15
ms. Micro-benchmarks `BenchmarkOnToolUse_*` and `BenchmarkTombstone` cover B-C.

### Fixtures needed

- `testdata/golden/observer/tombstones.txt` — 13 rendered markers, created in commit 1.
- `testdata/corpora/toolout/` — SP-04's committed raw bash/test/grep/glob/git/ANSI/fileread/webfetch
  output; **reused, not extended**, by both `BenchmarkOnToolUse_TestOutput256KB` and the Phase 1
  harness, which draws every tool-result payload from it.
- `internal/observer/testdata/transcript_tail.jsonl` — 40-line synthetic transcript with sidechain
  assistant messages, for `TestOnStop_SummaryFromTranscriptTail`.
- No new session fixtures: Phase 1 takes its call *sequence* from `eval.Synthesize` and its *bytes*
  from the corpus above. Adding real result bytes to `eval.SynthSpec` would be an `arch/` amendment
  against `internal/eval` and is explicitly not done here.

---

## Commit plan

### Commit 0 — the `arch/sp08-observer-seams` amendment (a **separate branch**, merged first)

The Implementation spec's pre-step, landed on `develop` before `feat/sp08-observer-l0` is cut. It is
one commit on its own branch and does not count toward SP-08's 5–8 band.

```
git fetch && git checkout develop && git pull
git checkout -b arch/sp08-observer-seams
```

- [ ] `fix(store): honour an explicit empty Canon.Strip and a caller MinHash opt-out` —
      `internal/store/put.go`'s `canonOptions` per pre-step (a), plus
      `TestCanonOptions_EmptyStripMeansNoOptionalClasses`, plus the §5.8 note in
      `plans/00-ARCHITECTURE.md`.
- [ ] Same commit: `internal/cli/hookclient.go`'s `rawExtras` forwarding the resolved agent name and
      `internal/daemon/handlers.go`'s `resolveEvent` restoring `Event.Extra` from `req.Raw`, per
      pre-step (b), with `TestRawExtras_ResolvesTheSubagentNameClientSide`,
      `TestResolveEvent_RestoresRawExtras` and the extended `TestDecodeRequestRoundTrip`.
- [ ] Same commit: `Services.Mode func() contract.Mode` in `internal/daemon/options.go`, the hoist of
      `statePath` / `contract.NewMonitor` above the bind loop in `internal/daemon/daemon.go` with
      `svc.Mode = monitor.Mode` assigned before it, the §5.4 declaration in
      `plans/00-ARCHITECTURE.md`, and `TestServicesModeIsAssignedBeforeBinds`, per pre-step (c).
- [ ] Confirm the copy of this plan you are working from carries the branch-purity carve-out: the
      Definition-of-Done criterion reads "no edit to `internal/store`, to
      `internal/daemon/handlers.go`, or to any file under `internal/cli` **except**
      `internal/cli/daemon.go`", and the Done checklist's file-map bullet says the same. If it does
      not, narrow both before cutting the feature branch — this is a plan edit, not a code edit, and
      it belongs on this branch. `internal/cli/daemon.go` holds the repository's only
      non-test `daemon.New` call site
      (`git grep -n 'daemon\.New(' -- internal cmd test tools ':!*_test.go'` → one hit), so with the
      criterion unnarrowed `WireObserver` has no caller it is permitted to have, all five `Services`
      seams stay nil, every hook still exits 0, and this subplan's own e2e rows
      (`TestE2E_ObserverThroughDaemon`, `TestE2E_SubagentNameReachesTheDaemon`) — which build and run
      the real `./cmd/qompack` binary — cannot pass. The call itself cannot be added here: it
      belongs to Commit 6, because `daemon.WireObserver` does not exist yet on this branch and a
      reference to it would not compile.
- [ ] Confirm, likewise, that the `observer_ops.go` declaration in the Implementation spec is
      `func WireObserver(o *Options) (observer.Observer, error)` plus
      `func RegisterObserverIdleWork(d Daemon, obsv observer.Observer)`, and the idle `Persist`
      registration has moved into the second function. The `s *SessionRegistry` parameter and the
      `o.Idle()` call the earlier draft carried are both unsatisfiable: `Bind` must run before
      `daemon.New`, while the registry and the idle controller are built inside it and reachable
      only afterwards through `d.Registry()` and `d.Idle()`.
- [ ] `go run ./tools/devtool ci-local` green on the amendment branch alone; merge to `develop`.
- [ ] Footer: `Refs: SP-08 pre-step, §0 amendment rule, §5.8 PutOptions, §5.3 Event.Extra`

---

The remaining work happens on **`feat/sp08-observer-l0`**, cut from `develop` with SP-01, SP-03,
SP-04, SP-05, SP-06, SP-07 **and the amendment above** already merged:

```
git fetch && git checkout develop && git pull
git checkout -b feat/sp08-observer-l0
```

Conventional Commits per §10: `<type>(<scope>): <subject>`, body explains the decision, footer
`Refs:` names the subplan, gaps and design sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(observer): addressable tombstones, tool classification, and task-boundary signals`

- [ ] Extend `internal/observer/tombstone_test.go` — it already ships with four passing tests — and
      write `signals_test.go` in full: all rows of both tables above, plus `BenchmarkTombstone` and
      `FuzzExtractSignals`. Update `TestTombstone_RendersTheSection81Form`'s pinned string for the
      new `…`, and leave `TestHumanBytes_UsesTheBinaryDivisor` untouched. Run
      `go test ./internal/observer/ -run 'Tombstone|HumanBytes|Signals|TestOutcome|PathsFromInput|ResponseText'`
      and confirm the **new** rows fail — including the updated §8.1-form assertion, which fails
      until the ellipsis lands — while `TestHumanBytes_UsesTheBinaryDivisor` and
      `TestTombstone_IsAddressable` still pass. A run in which those two also fail means the change
      broke a shipped guarantee.
- [ ] Rewrite `internal/observer/doc.go`, extend `tombstone.go`, implement `signals.go`.
- [ ] Add `testdata/golden/observer/tombstones.txt` (generate once, eyeball every line against
      §8.1 item 2, then commit).
- [ ] `go run ./tools/devtool fmt lint test` — green; the golden test passes byte-for-byte.
- [ ] Footer: `Refs: SP-08, G3.2, G1.5, §8.1 item 2, §2.2`

### Commit 2 — `feat(observer): PostToolUse pipeline with store, index, sketches and DAG emission`

- [ ] Write `fakes_test.go`, `tooluse_test.go`, `graph_test.go`, `sketches_test.go` (including
      `TestObserverNeverFeedsBloom` and `TestObserverSourceHasNoBloomReference`). Run and confirm
      failure.
- [ ] Add `observer.go` (Options/New/session map/locking/soft/metrics), `state.go`, `tooluse.go`,
      `graph.go`, `sketches.go`.
- [ ] Point `internal/observer/observertest.RunObserverSuite` — which **ships** — at the real
      implementation and make its `/behaviour` block pass. The block is guarded by `skipIfStub`,
      which probes `OnToolUse` for `core.ErrNotImplemented`, so landing the real `New` lifts the
      Rule W-1 skip by itself; the work is making the eight behaviour cases green, not editing the
      suite. Add the real factory alongside the two stub factories in
      `internal/observer/observertest/suite_test.go`.
- [ ] `go run ./tools/devtool test-race` for `./internal/observer/...` — green.
- [ ] `go test -bench BenchmarkOnToolUse -benchtime 200x ./internal/observer/` and paste the p50/p99
      into the commit body against budget **B-C (p99 < 50 ms)**.
- [ ] Footer: `Refs: SP-08, §8.1 items 1,4,5, §8.2, §5.21`

### Commit 3 — `feat(observer): supersession and near-duplicate redundancy detection`

- [ ] Write `supersede_test.go` (all 14 rows, including the rapid property test and the
      reopen-persistence test against the real SP-06 store). Confirm failure.
- [ ] Add `supersede.go`; wire step 8 of the `OnToolUse` algorithm.
- [ ] `go run ./tools/devtool test` — green; `observer.superseded` counter asserted non-zero in the
      real-store test.
- [ ] Footer: `Refs: SP-08, §8.1 item 3, §5.8 MarkSuperseded`

### Commit 4 — `feat(observer): verbatim UserPromptSubmit capture and BOCD feature emission`

- [ ] Write `prompt_test.go` and `features_test.go` in full. Confirm failure.
- [ ] Add `prompt.go`, `features.go`.
- [ ] Assert in review that no code path in the package rewrites a prompt object or record
      (`grep -n 'UserPromptSubmit' internal/observer` reviewed line by line).
- [ ] `go run ./tools/devtool test cover` — `internal/observer` at or above the 75% floor (§6.4).
- [ ] Footer: `Refs: SP-08, G2.3, §8.1 item 7, §6.6`

### Commit 5 — `feat(observer): Stop and SubagentStop capture with subagent tool-result hashes`

- [ ] Write `stop_test.go` and add `internal/observer/testdata/transcript_tail.jsonl`. Confirm
      failure.
- [ ] Add `stop.go`.
- [ ] Verify `TestOnStop_RetrievalPathG10_1` round-trips a real capture out of a real store.
- [ ] `go run ./tools/devtool test-race` — green.
- [ ] Footer: `Refs: SP-08, G10.1, §8.1 item 8`

### Commit 6 — `feat(observer): SessionStart startup/resume, SessionEnd flush, session index and GC`

- [ ] Write `session_test.go` in full. Confirm failure.
- [ ] Add `session.go`; complete `state.go`'s `Persist`.
- [ ] Add `internal/daemon/observer_ops.go` with `WireObserver`, `RegisterObserverIdleWork`,
      `symbolAdapter`, the **single `o.Bind`** attaching the five `Services` seams (no `Handle` call
      anywhere in the file), the mode mapping, the feature/signal forwarding, the nil-tolerant
      `s.Ledger.RefreshStaleness(ctx, s.Store)` inside the wrapped `SessionStart` seam that SP-09's
      out-of-scope table assigns to this subplan, and the idle `Persist` registration inside
      `RegisterObserverIdleWork`.
- [ ] Add the wiring block to `runDaemon` in `internal/cli/daemon.go` — the only non-test
      `daemon.New` call site, and the one file outside `internal/observer` this branch is permitted
      to modify. After the `opts.Log` / `opts.Metrics` / `opts.Clock` assignments and **before**
      `daemon.New(opts)`: `obsv, obsErr := daemon.WireObserver(&opts)`; on error,
      `opts.Log.Loud("observer unavailable; L0 capture disabled", "err", obsErr.Error())` and carry
      on, per §12.3's "everything else fails toward do nothing" and `runDaemon`'s contract that a
      degraded daemon still starts. Immediately after `d, err := daemon.New(opts)` succeeds:
      `if obsv != nil { daemon.RegisterObserverIdleWork(d, obsv) }`. Pass `&opts`, never `opts`:
      `Bind` is pointer-receiver over the unexported `binds` slice, so the value form compiles,
      appends to a copy, and leaves all five seams nil with every hook still exiting 0.
- [ ] Prove the block is load-bearing: delete the two calls, re-run
      `go test ./test/e2e/ -run TestE2E_ObserverThroughDaemon`, confirm it **fails**, restore them.
      That test is the only thing standing between a nil-seam merge and a green CI run.
- [ ] Confirm `git grep -n 'Handle(' -- internal/daemon/observer_ops.go` returns nothing, and that
      SP-05's `TestIngestACKPrecedesProcessing` and
      `TestMarkerIsWrittenByFlushAndCheckpointOnly` still pass with the observer wired — they are
      what a `Handle` override would silently break.
- [ ] Add `internal/daemon/observer_ops_test.go` with `TestWireObserver` — builds a real observer
      against a temp project, calls `WireObserver(&o)`, then `daemon.New(o)`, and asserts all five
      `Services` seams are non-nil **on the daemon built from the wired Options**, that
      `RegisterObserverIdleWork(d, obsv)` registers `observer.persist` at priority 50, that the mode
      mapping round-trips, and that a driven `observe.tool` request reaches the observer through
      SP-05's route (the daemon-side test V3-VERIFY H12 re-runs). Asserting through a built daemon
      rather than against the `Options` value is what catches the `WireObserver(o Options)` value
      form: `Bind` appends to `o.binds`, and nothing is observable until `New` runs those binds.
- [ ] Add `TestWireObserver_SessionStartRefreshesStaleness` beside it — a fake ledger bound to
      `Services.Ledger`, a `SessionStart` event with `Source: "startup"` driven through the wired
      seam, asserting exactly one `RefreshStaleness` call; and the same event with a nil `Ledger`
      asserting no panic and no error. This is the gate on SP-09's §12 High-severity staleness flip
      having a production caller at all.
- [ ] Add `test/e2e/observer_e2e_test.go` (all six rows).
- [ ] `go run ./tools/devtool build test test-race` and `go test ./test/e2e/ -run 'TestE2E_'` —
      green on Windows and on Linux.
- [ ] Confirm the import-graph check in `verify` still passes (observer must not have acquired an
      import of `scheduler`, `checkpoint`, `symbols` or `contract`).
- [ ] Footer: `Refs: SP-08, §7.3 SessionStart/SessionEnd, §8.2 GC, §5.21 split ownership`

### Commit 7 — `test(observer): Phase 1 exit-criterion harness and hot-path benchmarks`

- [ ] Add `test/e2e/phase1_exit_test.go` with the seven rows, the two `SynthSpec` literals, the
      `corpus`/`payloadFor` loader over `testdata/corpora/toolout/`, and the `eventsFor` that builds
      `ToolInput` from `tc.Paths`. TDD ordering for a gate commit: write the assertions **at the design's numbers
      first** (`>= 4.0`, `ratioOn >= ratioOff*1.25`), run them, and only then tune the observer. If
      an assertion fails, the fix goes in `internal/observer` (or in the `PutOptions` it passes) —
      **the threshold is never weakened**; a genuine need to move it is a §11.3 sign-off, not an
      edit.
- [ ] Run `go test ./test/e2e/ -run Phase1 -v`; record `read_heavy_ratio_canon`,
      `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon
      --json bench-observer.json` locally; record B-A p50/p99 and B-B p99.
- [ ] Add `docs/adr/0008-observer-l0.md`: the twelve resolved decisions, the §8.1-item-7 destination
      rationale, the tombstone grammar, the supersession rules, the measured dedup ratios, and the
      measured B-A/B-B/B-C numbers per platform from the CI run.
- [ ] `go run ./tools/devtool ci-local` — `verify`, `test`, `cover`, `bench-gate`, `replay-gate`,
      `plugin-validate`, `security`, `docs` all green.
- [ ] Footer: `Refs: SP-08, §10 Phase 1, §11.3, §8.1 performance budget`

Seven commits, each compiling and green for the packages it touches.

---

## Subagent strategy

This subplan is **heavy**: thirteen source files, thirteen test files, one file inside another
subplan's package, and a phase gate. Partition it across four parallel subagents after the main
session has landed commit 2's skeleton, because everything downstream depends on `Options`,
`sessionState` and the `soft`/metrics conventions being fixed first.

**Stays in the main session (never delegated):**

- `observer.go`, `state.go`, `doc.go` — the shared types every other file compiles against.
- `internal/daemon/observer_ops.go` — it touches another subplan's package and must be reviewed
  against SP-05's actual `Options`/`Bind`/`Services`/`SessionRegistry` shapes as merged.
- All seven commits. Subagents return **diffs and test results, never commits**; the main session
  stages, runs `devtool ci-local`, and commits in the sequential order above.
- The Phase 1 numbers and the ADR: they are the deliverable's headline claim and must be produced
  by one agent with one store configuration.

**Sequencing.** Main session first does commit 1 (pure functions — small, fast, unblocks the
tombstone golden everyone else references) and the `observer.go`/`state.go` skeleton of commit 2.
Only then fan out.

| Subagent | Owns | Inputs it is given | Returns |
|---|---|---|---|
| **A — write path** | `tooluse.go`, `graph.go`, `sketches.go` + `tooluse_test.go`, `graph_test.go`, `sketches_test.go`, `fakes_test.go` | the frozen `Options`/`sessionState` declarations, the §8.1 items 1/4/5 quotes, the 14-step `OnToolUse` algorithm verbatim | the six files plus `go test -run 'ToolUse|Graph|Sketches' -race` output and `BenchmarkOnToolUse` p50/p99 |
| **B — redundancy** | `supersede.go`, `supersede_test.go` | `fakes_test.go` from A (hand it the file once A returns it, or a stub copy if A is still running — A's version wins at integration), the §8.1 item 3 quote, the supersession rules table | the two files plus the property-test seed corpus and `MarkSuperseded` call counts |
| **C — capture path** | `prompt.go`, `features.go`, `stop.go` + `prompt_test.go`, `features_test.go`, `stop_test.go`, `testdata/transcript_tail.jsonl` | the frozen types, §8.1 items 6/7/8, §6.6's five-feature list, resolved decisions 3 and 4 | the seven files plus the feature-value assertions and `TestOnStop_RetrievalPathG10_1` output |
| **D — lifecycle + e2e** | `session.go`, `session_test.go`, `test/e2e/observer_e2e_test.go` | §5.21's split-ownership table, §8.2's GC paragraph, the `OnSessionEnd` ordered algorithm | the three files plus the ordered-call-log assertion output and the e2e run log from both Windows and Linux |

**Integration rules.**

1. Each subagent works in its own worktree (`git worktree add ../qompack-sp08-a
   feat/sp08-observer-l0`) and returns a patch; it never pushes to the branch.
2. No two subagents write the same file. `fakes_test.go` belongs to A alone; B and C declare any
   extra doubles in their own `*_test.go` files with distinct type names (`fakeStoreSupersede`,
   `fakeGrammarPrompt`).
3. A subagent that believes it needs a change to `Options`, `sessionState`, a shared constant, or
   any §5 interface **stops and returns the request**; the main session makes the change once and
   re-broadcasts the frozen declarations. This is the same discipline as the §0 amendment rule,
   scaled down.
4. The main session applies patches in the order A → B → C → D, running
   `go run ./tools/devtool fmt lint test-race` after each, and maps them onto commits 2–6.
5. `Persist`, `WireObserver` and the ADR are written by the main session after D lands, because
   they need the final shapes of everything above.

---

## Exit criteria

**Quoted verbatim from `Qompack.md` §10, Phase 1 — this slice closes the phase:**

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

**Quoted verbatim from `Qompack.md` §11.3:**

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup

**Measured form of the above (all must hold on the branch):**

- [ ] `TestPhase1_DedupRatioReadHeavy` passes: `store.Stats().DedupRatio >= 4.0` on
      `eval.Synthesize(0x51080001, readHeavy)`.
- [ ] `TestPhase1_CanonicalizationGapOnTestOutput` passes: `ratioOn >= ratioOff * 1.25` on
      `eval.Synthesize(0x51080002, testOutputHeavy)`, with both raw numbers recorded in
      `docs/adr/0008-observer-l0.md`.
- [ ] `TestPhase1_StoreGrowthSublinear` passes.
- [ ] `bench-gate` green on ubuntu-latest, macos-latest and windows-latest with the observer wired:
      **B-A p99 < 15 ms** and B-B p99 < 2 ms, at `--iterations 2000`; the three p99 figures are
      recorded in the ADR.
- [ ] `BenchmarkOnToolUse_FileRead64KB` and `BenchmarkOnToolUse_TestOutput256KB` within **B-C p99 <
      50 ms**; `BenchmarkTombstone` < 2 µs/op.

**Gap closure:**

- [ ] **G3.2** — `Tombstone` renders the §8.1 item 2 marker with hash, size, tool, subject and the
      `re-expandable` affordance; golden test locks the format.
- [ ] **G2.3** — every `UserPromptSubmit` is content-addressed, indexed and never rewritten;
      `TestOnUserPrompt_NeverRegenerated` and `TestE2E_VerbatimPromptSurvivesRestart` prove it.
- [ ] **G10.1** — `SubagentStop` stores summary + tool-result hashes;
      `TestOnStop_RetrievalPathG10_1` round-trips them out of a real store.
- [ ] **G1.5** — `ExtractSignals` detects todo completion, passing test runs and git commits, and
      `WireObserver` delivers them to the scheduler seam.

**Local criteria:**

- [ ] The Rule W-1 skip in the observer conformance suite no longer fires. `internal/observer/
      observertest` **ships** — `func RunObserverSuite(t *testing.T, name string, factory func(t
      *testing.T) observer.Observer)` in `suite.go`, with `behaviour.go` and `suite_test.go` beside
      it — so SP-08 creates nothing here. Its `/behaviour` block is gated by `skipIfStub`, which
      probes `OnToolUse` for `core.ErrNotImplemented`; landing the real `New` lifts the gate, and
      SP-08's job is to make the eight behaviour cases pass — `post_tool_use_never_blocks_the_tool_
      call`, `user_prompt_capture_never_blocks_the_prompt`, `session_start_branches_on_source`,
      `stop_and_subagent_stop_are_both_accepted`, `session_end_flushes_without_reporting_an_error`,
      `every_entry_point_tolerates_a_malformed_event`, `replaying_one_event_twice_is_not_an_error`
      and `extract_signals_detects_todo_test_and_git` — with the real factory registered in
      `suite_test.go`. `Tombstone` stays asserted in `internal/observer/tombstone_test.go` rather
      than in the suite, because `observertest` may not import `store` (§3.2).
- [ ] `go run ./tools/devtool ci-local` green: `verify` (gofumpt clean, `golangci-lint` clean,
      `go vet`, `nomagic`, import-graph layer check, test-only-dep check, build), `test` and
      `test-race`, `cover` (≥ 75% for `internal/observer`), `crossbuild`, `bench-gate`,
      `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] `internal/observer` imports exactly: `core paths config logging obs hookio store canon sketch
      dag grammar tokens` plus stdlib. No `scheduler`, `checkpoint`, `symbols`, `contract`,
      `negknow`, `analyzer`, or `daemon`.
- [ ] Zero occurrences of the IDENTIFIERS `Bloom`/`NewBloom`/`RebuildBloom` in non-test files under
      `internal/observer` — the enforced check is the AST-level
      `TestObserverSourceHasNoBloomReference` (`go/parser` over every non-test file), not a bare
      grep: doc comments legitimately name the Bloom filter to explain the prohibition.
- [ ] `.qompack/sketches/tried.bloom` is never created by any observer code path
      (`TestOnSessionEnd_NeverWritesTriedBloom`, `TestE2E_ObserverThroughDaemon`).
- [ ] Every hook subcommand still exits 0 under fault injection
      (`TestE2E_HooksExitZeroUnderFaultInjection`) — §13 invariant 6.
- [ ] The `arch/sp08-observer-seams` amendment is merged to `develop` **before** this branch is cut,
      and `feat/sp08-observer-l0` contains no edit to `internal/store`, to
      `internal/daemon/handlers.go`, to `internal/daemon/options.go`, to `internal/daemon/daemon.go`,
      or to any file under `internal/cli` **except**
      `internal/cli/daemon.go`, whose `runDaemon` gains the Commit 6 observer wiring block and
      nothing else. That one carve-out is deliberate: it is the repository's only non-test
      `daemon.New` call site, so without it `WireObserver` has no caller it is allowed to have.
- [ ] Exactly 7 commits on `feat/sp08-observer-l0` were planned; by controller ruling the branch
      carries the 7 plus the sanctioned gates chore (`chore(devtool,guards)`) and one post-review
      fix commit — 8 plus 1 — all conventional, none carrying an attribution trailer; CI's trailer
      grep passes. The amendment commit is on its own branch and is not one of them.
- [ ] `docs/adr/0008-observer-l0.md` exists and records the twelve resolved decisions plus every
      measured number.

---

## Done checklist

- [ ] Every quote in **Design context** has a corresponding implementation: §8.1 item 2 →
      `tombstone.go`; item 3 → `supersede.go`; item 4 → `graph.go`; item 5 → `sketches.go`
      (including the Bloom prohibition); item 7 → `prompt.go`; item 8 → `stop.go`; §7.3
      SessionStart/SessionEnd rows → `session.go`; §2.2 compactable set → `IsCompactable`; §6.6
      five features → `features.go`; §8.2 file version history and GC → `tooluse.go` step 7 and
      `session.go` step 6; §10 Phase 1 → `test/e2e/phase1_exit_test.go`.
- [ ] Placeholder scan: `git grep -nE 'TODO|TBD|FIXME|XXX|not implemented|handle (this|edge cases)'
      -- internal/observer internal/daemon/observer_ops.go test/e2e` returns nothing, and no
      function returns `core.ErrNotImplemented` in `internal/observer`.
- [ ] Type consistency with **Interface contract**: `Observer`, `Tombstone`, `Signals` and
      `ExtractSignals` match §5.21 character for character; every consumed signature is called with
      the exact argument types listed (in particular `store.ChangedSince` is not called at all here,
      `EstimateRoot` receives `[]core.ChunkRef`, and `MarkSuperseded(older, by)` argument order is
      older-first).
- [ ] No §5 interface owned by another subplan was modified, and no method was added to one
      (Rule W-3). The three behaviours that had to change were raised as the
      `arch/sp08-observer-seams` amendment and landed on `develop` first, exactly as §0 requires —
      including `Services.Mode`, which widens SP-05's struct rather than adding a method to an
      interface, and is declared in §5.4 on that branch before this one reads it.
- [ ] Only one file outside `internal/observer` and the test trees was **added** on this branch:
      `internal/daemon/observer_ops.go`; the only file **modified** outside them is
      `internal/cli/daemon.go`, by Commit 6's wiring block and nothing else. The amendment's five
      edits — `internal/store/put.go`, `internal/cli/hookclient.go`, `internal/daemon/handlers.go`,
      `internal/daemon/options.go` and `internal/daemon/daemon.go` — are on the amendment branch.
- [ ] `git diff develop..HEAD -- internal/cli/daemon.go` shows exactly the two wiring blocks — the
      `daemon.WireObserver(&opts)` call before `daemon.New` and the
      `daemon.RegisterObserverIdleWork(d, obsv)` call after it — and no other change.
- [ ] SP-09's assigned obligation is discharged, not silently dropped:
      `git grep -n 'RefreshStaleness' -- internal/daemon/observer_ops.go` returns the wrapped
      `SessionStart` call, `TestWireObserver_SessionStartRefreshesStaleness` passes, and
      `internal/observer` never imports `negknow` — the enforced checks are devtool's import-graph
      lint and the realized-import test in `sketches_test.go`; a bare
      `git grep -n 'negknow' -- internal/observer` hits doc COMMENTS that explain the prohibition,
      which is legitimate — the call is in the daemon seam because the observer is forbidden the
      import.
- [ ] `internal/observer` declares **no** NodeID constructor, no `argsPreviewMax`, no args-preview
      key table and no `argsDigest` helper:
      `git grep -nE '"(tooluse|toolresult|assistant|userprompt|file|symbol|segment):|argsPreviewMax' -- internal/observer ':!*_test.go'`
      returns nothing. The pattern is one line on purpose: the earlier wrapped form pasted a newline
      and six spaces into the middle of the alternation, so the copied command silently tested
      something else. The exclusion pathspec replaces the old "outside test fixtures" clause, which
      required a human to read the output and so was not a gate.
- [ ] `nomagic` clean: every literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and
      `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` inside `internal/observer` is
      either read from `config` or carries a `//nomagic:allow <reason>` comment. After this subplan
      the only such annotation in the package is the shipped `bytesPerKB = 1024` in `tombstone.go`;
      `120` no longer appears at all, because §5.8's `store.ArgsDigest` owns the preview cap.
- [ ] Commit count verified: `git rev-list --count develop..feat/sp08-observer-l0` is **7**, within
      the 5–8 band.
- [ ] `git log develop..feat/sp08-observer-l0 --format=%B | grep -Ei 'co-authored-by|signed-off-by|
      generated with|🤖'` returns nothing. **Do not add Co-Authored-By lines or any attribution
      trailers to any commit message.**
- [ ] `Qompack.md` is unmodified: `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] Self-review pass: re-read `tooluse.go`'s 14 steps against the §8.1 responsibility list, and
      re-read `session.go`'s `OnSessionEnd` against §7.3's "Flush, compact the store, write session
      index" and §8.2's "run on `SessionEnd`", confirming both orderings match this document.

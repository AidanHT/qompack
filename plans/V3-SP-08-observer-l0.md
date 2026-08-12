# SP-08: L0 observer: PostToolUse, UserPromptSubmit, Stop/SubagentStop, SessionStart/SessionEnd entry points, addressable tombstones, supersession, verbatim capture, and the Phase 1 exit criterion

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
SP-07's real `internal/dag`. `internal/observer` exists as SP-01's stub: the five `Observer`
methods, `Tombstone`, `Signals`, and `ExtractSignals` all compile and return
`core.ErrNotImplemented` or zero values, and `observertest`-style behaviour tests are `t.Skip`ped.
`internal/grammar` is still an SP-01 stub (SP-15, wave 4) and `internal/negknow` is being built by
SP-09 in this same wave — the observer must tolerate both being inert.

**What exists when you finish.** `internal/observer` is real and is the sole writer of that
package. The daemon routes `observe.tool`, `observe.prompt`, `observe.stop`, `session.start` and
`flush` into it through `internal/daemon/observer_ops.go` (the one file SP-08 owns outside its own
package, exactly as SP-12 later owns `scheduler_runtime.go` there). A read-heavy session drives a
store whose `Stats().DedupRatio` is at or above 4:1, measured with and without canonicalization,
and the hot-path bench-gate still passes B-A p99 < 15 ms on all three platforms with the observer
wired in. Every `t.Skip` in the observer conformance suite is off. Phase 1 is closed.

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
| The DAG's storage, `BackwardSlice`/`ForwardSlice`, `CrossingEdges`, `Compact` | SP-07 |
| The thin hook client, IPC transport, framing, ACK, spool fallback, the B-A budget histogram, the sync→spool submode transition, `qompack session-start` dispatch, daemon start, `contract.Monitor.RunAll` at session start, `test/bench/hotpath` | SP-05 |
| Writing to `tried.bloom`, the elimination ledger, canonical descriptors, staleness, the three-way `already_tried` answer, heuristic elimination detection over the DAG | SP-09 |
| `SessionStart(source=compact)` and `source=clear` **semantics** — SP-08 owns only the `source` switch and delegates through the `observer.Rehydrator` seam declared here | SP-11 |
| The `PreCompact` hook, checkpoint writing, `ExtractDecisions`, focus instructions, pins | SP-10 |
| Closing a segment on a **changepoint**, frontier advancement (O5), BOCD itself, the composite trigger, p-selection, droppable-block classification and eviction ordering | SP-12 |
| Sequitur's algorithm, the two grammar invariants, thrash-warning **policy** and high-multiplicity detection (SP-08 only appends symbols and forwards whatever `Thrash` returns) | SP-15 |
| MCP tools, ephemeral-at-birth tagging on the retrieval side, `Promoter` | SP-13 |
| `/qompack:status` rendering | SP-14 |
| `eval.Synthesize`, the 24-session synthetic corpus, the replay gate, Belady OPT, divergence metrics | SP-02 |
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

**Import discipline (§3.2).** `observer` may import `hookio store chunk canon sketch dag grammar
negknow tokens` plus the foundation. It **does not** import `symbols` (hence `SymbolLister`), does
not import `scheduler` or `checkpoint` (hence `Signals`/`FeatureSample` and the callbacks), does
not import `contract` (hence `observer.Mode`), and deliberately does not import `negknow` at all —
because §8.1 item 5 forbids L0 from touching the Bloom filter.

---

## Implementation spec

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
   regenerated from a summary: (a) the prompt bytes stored **uncanonicalized** as a
   content-addressed object via `store.PutBytes`; (b) an append-only `tool_use.jsonl` entry with
   `Tool: "UserPromptSubmit"` and `ID: VerbatimPromptID(...)`; (c) a `KindUserPrompt` DAG node
   anchored to the currently open segment by an `EdgeSequence` from `segment:<id>`. This is what
   closes G2.3: the bytes are content-addressed, immutable, and reachable by hash forever.
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

### `internal/observer/doc.go` (new)

Package documentation stating layer L0, the §8.1 responsibility list, the sole-writer rule from
§5.21 ("No subplan other than SP-08 writes code in `internal/observer`"), and resolved decisions
1–12 above in comment form.

---

### `internal/observer/observer.go` (new)

**Responsibility.** `Options`, `New`, the concrete type, per-session state map, mode gating, error
policy, metric names, `Persist`.

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
    LastTS        core.UnixMilli // TS of the PREVIOUS event; updated last, after features() runs
    LastToolUse   dag.NodeID     // "" before the first tool use
    LastResult    dag.NodeID
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

### `internal/observer/tombstone.go` (new)

**Responsibility.** §8.1 item 2 in its exact rendered form, plus tool-name normalization and the
compactable-tool-set predicate of §2.2.

**Grammar of the marker.** Rendered from `store.ToolUseRecord` with no I/O:

```
[cleared: sha256:<12 hex>… · <size> · <ToolDisplay> <subject> · re-expandable]
```

- separator is `" · "` (space, MIDDLE DOT, space) — matching the design's `·`;
- ellipsis is `…`;
- `<12 hex>` is `rec.Root.Short()` (§4: first 12 hex chars); the design's `a3f2…` is an elision in
  prose, 12 hex is the architecture's canonical short form and is what the golden file records;
- `<size>` is `humanBytes(rec.Bytes)`: `< kib` → `"%dB"`; `< kib*kib` → `"%.1fKB"` (round half to
  even via `strconv.FormatFloat(v,'f',1,64)`); else `"%.1fMB"`. `const kib = 1024 //nomagic:allow
  byte-unit divisor, not a config value`. 2458 bytes renders `2.4KB`, matching the design example;
- `<ToolDisplay>` is `NormalizeToolName(rec.Tool)`;
- `<subject>` is `rec.Path` when non-empty, else `rec.ArgsPreview` truncated to 48 runes with `…`;
  when both are empty the ` <subject>` group and its leading space are omitted entirely;
- when `rec.Status == store.StatusSuperseded`, ` · superseded` is inserted immediately before
  ` · re-expandable`;
- when `rec.Ephemeral`, ` · ephemeral` is inserted in the same position, before any `superseded`.

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

### `internal/observer/signals.go` (new)

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
func (o *observer) argsDigestAndPreview(e hookio.Event) (core.Hash, string)
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
    digest, preview := o.argsDigestAndPreview(e)
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
8.  prevOnPath := core.ToolUseID("")
    if !empty { _, prevOnPath = o.detectSupersession(ctx, st, rec, res) }  // §8.1 item 3 — supersede.go
9.  o.feedSketches(rec)                                       // §8.1 item 5 — sketches.go
10. o.emitToolGraph(ctx, st, rec, body, prevOnPath)           // §8.1 item 4 — graph.go
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

`argsDigestAndPreview`:
- digest: `core.HashBytes("qompack.args.v1", compact)` where `compact` is `json.Compact` of
  `e.ToolInput`, or the raw bytes when compaction fails;
- preview, per display name: FileRead/FileEdit/FileWrite → `file_path`; Grep → `pattern + " in " +
  path` (path omitted with the `" in "` when absent); Glob → `pattern`; Bash/PowerShell →
  `command`; WebFetch → `url`; WebSearch → `query`; default → the compacted JSON;
- truncated to `argsPreviewMax` runes, appending `…` when cut:
  `const argsPreviewMax = 120 //nomagic:allow §5.8 caps ToolUseRecord.ArgsPreview at 120 chars`.

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
// Returns the ids it marked SUPERSEDED and, as a by-product of the same index read, the id of the
// most recent prior non-ephemeral tool use on the same path — which graph.go needs for the
// EdgeSharedFile edge and which would otherwise cost a second ToolUsesByPath call on the hot path.
func (o *observer) detectSupersession(ctx context.Context, st *sessionState,
    rec store.ToolUseRecord, res store.PutResult) (marked []core.ToolUseID, prevOnPath core.ToolUseID)
func isSuperset(newer, older []core.ChunkRef) bool
```

Algorithm:

```
if rec.Path == "" || rec.Ephemeral || supersedableClass(rec.Tool) == "" { return nil, "" }
prior, err := Store.ToolUsesByPath(ctx, rec.Path, supersessionLookback)
if err != nil { o.soft("supersede.list", err); return nil, "" }
newSet := set of res.Root.Chunks[i].Hash
thr := o.opt.Cfg.Store.Canonicalize.MinHash.NearDupThreshold
for _, p := range prior {            // ToolUsesByPath returns most-recent-first (§5.8)
    superseded := false
    if p.ID == rec.ID { continue }
    if prevOnPath == "" && !p.Ephemeral && p.TS <= rec.TS { prevOnPath = p.ID }
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
    o.opt.Graph.AddEdge(dag.Edge{From: toolUseNode(rec.ID), To: toolUseNode(p.ID),
        Kind: dag.EdgeSupersedes, Weight: 1, Turn: rec.Turn})
    o.count("observer.superseded")
    marked = append(marked, p.ID)
}
if res.NearDup != nil { o.count("observer.neardup") }
return marked, prevOnPath
```

**`ToolUsesByPath` ordering.** §5.8 does not state an order in the signature, so this file states the
contract it relies on: SP-06's `tool_use.jsonl` is append-only and `ToolUsesByPath(path, limit)`
returns the **most recent `limit` records, newest first**. `prevOnPath` therefore falls out of the
first eligible iteration. If SP-06 shipped oldest-first, the only change needed here is to take the
*last* eligible element instead of the first; the supersession loop itself is order-independent
because every candidate is filtered by `p.TS <= rec.TS`. A test
(`TestSupersede_PrevOnPathIsMostRecent`) pins the behaviour against the real store so a drift is
caught in wave 2, not wave 3.

`isSuperset(newer, older)` returns `false` when `len(older) == 0`, otherwise `true` iff every hash
in `older` is present in the `newer` set. Multiplicity is ignored (a chunk-hash set, per the design's
"chunk set"), so a file read twice in one result does not defeat it.

**Direction of `EdgeSupersedes`** is From = the *superseding* (newer) node, To = the *superseded*
(older) node. This is stated here because both SP-12 (eviction ranking) and SP-15 (redundancy)
traverse it.

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
plus shared-state edges keyed on file path and symbol name."*

```go
func toolUseNode(id core.ToolUseID) dag.NodeID  // "tooluse:" + string(id)
func resultNode(id core.ToolUseID) dag.NodeID   // "toolresult:" + string(id)
func assistantNode(s core.SessionID, t core.TurnIndex) dag.NodeID // "assistant:<s>:<t>"
func promptNode(s core.SessionID, t core.TurnIndex) dag.NodeID    // "userprompt:<s>:<t>"
func fileNode(pathKey string) dag.NodeID        // "file:" + pathKey
func symbolNode(name string) dag.NodeID         // "symbol:" + name
func segmentNode(id core.SegmentID) dag.NodeID  // "segment:" + itoa
func (o *observer) emitToolGraph(ctx context.Context, st *sessionState, rec store.ToolUseRecord,
    body []byte, prevOnPath core.ToolUseID)
func (o *observer) advancePos(st *sessionState, tok core.Tokens) int
```

`emitToolGraph` emits, in order:

1. `AddNode{ID: toolUseNode(rec.ID), Kind: KindToolUse, Turn, TS, Pos: advancePos(st, 0), Ref: string(rec.ID), Tokens: 0, Ephemeral: rec.Ephemeral}`
2. `AddNode{ID: resultNode(rec.ID), Kind: KindToolResult, Turn, TS, Pos: advancePos(st, rec.Tokens), Ref: string(rec.ID), Root: rec.Root, Tokens: rec.Tokens, Ephemeral: rec.Ephemeral}`
3. `AddEdge{toolUse → result, EdgeProduces, 1, Turn}`
4. the §8.1 chain, when `st.LastResult != ""`:
   `AddNode{assistantNode(sess, rec.Turn), KindAssistant, Pos: current, Tokens: 0}`,
   `AddEdge{st.LastResult → assistant, EdgeSequence, 1, Turn}`,
   `AddEdge{assistant → toolUse, EdgeSequence, 1, Turn}`
5. file edges, when `rec.Path != ""`:
   `AddNode{fileNode(rec.Path), KindFile, Ref: rec.Path}`;
   `AddEdge{toolUse → file, EdgeConsumes}` for every class except `FileEdit`/`FileWrite`
   (i.e. `FileRead`, `search`, `exec`, `web`); `AddEdge{toolUse → file, EdgeProduces}` for
   `FileEdit`/`FileWrite`;
   plus the shared-state edge, when `prevOnPath != ""`:
   `AddEdge{toolUse → toolUseNode(prevOnPath), EdgeSharedFile}`. `prevOnPath` is the second return
   value of `detectSupersession` (step 8 of `OnToolUse`), so this costs no second index read; it is
   `""` for ephemeral results, for empty results, and for the first touch of a path, and the edge is
   simply not emitted in those cases.
6. symbol edges, when `o.opt.Symbols != nil`, `rec.Path != ""`, `supersedableClass == "filecontent"`
   and `len(body) <= symbolScanCap`: for the first `maxSymbolsPerResult` unique names returned by
   `o.opt.Symbols.Names(rec.Path, body)`, `AddNode{symbolNode(n), KindSymbol, Ref: n}` and
   `AddEdge{toolUse → symbol, EdgeSharedSymbol}`.
7. segment anchoring, when `st.Segment != 0`: `AddEdge{segmentNode(st.Segment) → toolUse, EdgeSequence}`.

Then `st.LastToolUse = toolUseNode(rec.ID)`, `st.LastResult = resultNode(rec.ID)`.

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
        Canon: canon.Options{Strip: nil, KeepDeltas: false,
                             MinHash: sketch.MinHashOptions{Enabled: false}},
        KeepRaw: true, Ephemeral: false })
    // NO canonicalization, NO minhash: "verbatim and immutably" (§7.3, §8.1 item 7).
    on err: o.soft("prompt.put", err) and continue to step 6 with a zero root
4.  id := VerbatimPromptID(e.SessionID, st.Turn)
    tok := res.Root.Tokens; if tok == 0 && Tokens != nil { tok = Tokens.EstimateString(e.Prompt, tokens.ClassProse) }
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "UserPromptSubmit",
        ArgsDigest: core.HashBytes("qompack.args.v1", body),
        ArgsPreview: truncateRunes(e.Prompt, argsPreviewMax),
        Root: res.Root.Hash, Bytes: int64(len(body)), Tokens: tok,
        Status: store.StatusOK})
5.  Graph.AddNode(dag.Node{ID: promptNode(e.SessionID, st.Turn), Kind: dag.KindUserPrompt,
        Turn: st.Turn, TS: now, Pos: o.advancePos(st, tok), Ref: string(id),
        Root: res.Root.Hash, Tokens: tok})
    if st.Segment != 0 { Graph.AddEdge(segmentNode(st.Segment) → promptNode, EdgeSequence) }
    if st.LastResult != "" { Graph.AddEdge(st.LastResult → promptNode, EdgeSequence) }
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

**The one deliberate exception to resolved decision 2.** The prompt is stored with `Strip: nil`,
i.e. *without* even the unconditional `crlf` class. §8.1 item 7 and §7.3 both say "verbatim and
immutably", and a user prompt is not file content read on two platforms, so there is no dedup space
to fork and nothing to gain from normalization — while a single normalized byte would make the
stored object no longer the thing the user typed. This is written down here so a later reviewer does
not "fix" the inconsistency with `tooluse.go`. `stop.go`'s capture blob takes the same treatment for
the same reason: it is JSON this package generated, not host content.

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
1.  agent := subagentName(e)          // Extra["subagent_type"] → Extra["agent_name"] → Extra["agent"] → "subagent"
    // Extra values are raw JSON: each candidate is json.Unmarshal'ed into a string and skipped
    // unless that succeeds and yields a non-empty value, so a numeric or object value falls
    // through to the next key rather than rendering as its JSON text.
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
        Canon: canon.Options{Strip: nil, KeepDeltas: false,
                             MinHash: sketch.MinHashOptions{Enabled: false}}, KeepRaw: true})
    on err: o.soft("stop.put", err); st.Turn++; st.LastTS = now; return hookio.Empty(), nil
    tok := res.Root.Tokens
    if tok == 0 && o.opt.Tokens != nil {
        tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.ClassJSON)  // the blob is JSON
    }
6.  id := SubagentCaptureID(e.SessionID, st.Turn)
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "SubagentStop", ArgsDigest: core.HashBytes("qompack.args.v1", []byte(agent)),
        ArgsPreview: truncateRunes(agent+": "+summary, argsPreviewMax),
        Root: res.Root.Hash, Bytes: int64(len(blob)), Tokens: tok,
        Status: store.StatusOK, Subagent: agent})
7.  Graph.AddNode(dag.Node{ID: toolUseNode(id), Kind: dag.KindToolUse, Turn: st.Turn, TS: now,
        Pos: o.advancePos(st, tok), Ref: string(id), Root: res.Root.Hash, Tokens: tok})
    for _, r := range refs { Graph.AddEdge(dag.Edge{From: toolUseNode(id),
        To: toolUseNode(r.ToolUseID), Kind: dag.EdgeConsumes, Weight: 1, Turn: st.Turn}) }
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
`st.Segment = cur.ID`; otherwise `id, err := Segments().Open(ctx, store.Segment{Session: s,
StartTurn: st.Turn, StartTS: now, Features: map[string]float64{}, Closed: false})` and
`st.Segment = id`. An `Open` failure is `soft("segment.open")` and leaves `st.Segment = 0`, which
every call site tolerates. **Closing on a changepoint is SP-12's**; the only close SP-08 performs
is the session-end close below.

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

`saveSketches` writes `filepath.Join(root, ".qompack", "sketches", "touch.cms")` and
`"explore.hll"` via `sketch.Save`, skipping nil sketches, softing errors. It **never** writes
`tried.bloom` — §3.3 reserves that file for `negknow.RebuildBloom`.

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
    "01J8…": { "turn": 41, "prefix_tokens": 128340, "segment": 7,
               "last_ts": 1723406400123, "last_tool_use": "tooluse:toolu_01A",
               "last_result": "toolresult:toolu_01A", "subagent_since": 12,
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
    LastTS       core.UnixMilli   `json:"last_ts"`
    LastToolUse  string           `json:"last_tool_use"`
    LastResult   string           `json:"last_result"`
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

### `internal/daemon/observer_ops.go` (new — the single file SP-08 adds outside its own package)

**Responsibility.** Wire the observer into SP-05's op-routing table without editing daemon
internals, exactly as §5.4's extension-seam note prescribes and as SP-12 will later do with
`scheduler_runtime.go`.

```go
package daemon

// WireObserver constructs the L0 observer over the daemon's live services and registers the five
// L0 ops. Called from the daemon's construction path once Options are complete.
func WireObserver(o Options, s *SessionRegistry) (observer.Observer, error)

type symbolAdapter struct{ ex symbols.Extractor }   // observer must not import `symbols` (§3.2)
func (a symbolAdapter) Names(path string, b []byte) []string
```

- `symbolAdapter.Names` calls `a.ex.Extract(path, b)` and returns the deduplicated `Name` fields in
  first-appearance order.
- `Mode` is `func() observer.Mode { if mon.Mode() == contract.ModeFull { return observer.ModeFull };
  return observer.ModePassive }`.
- `OnFeatures` maps `observer.FeatureSample` field-for-field into `scheduler.Features` and calls
  `o.Sched.Observe(ctx, f, fs.Turn)` **when `o.Sched != nil`** (it is nil through wave 2).
- `OnSignals` records the three booleans on the session registry and, when `o.Sched != nil`,
  forwards them as a `scheduler.Features{TodoTransition: 1}` nudge on `TodoCompleted ||
  TestPassed || GitCommit` — the G1.5 task-boundary delivery.
- Sketch pointers come from `o.Sketches` (`*daemon.SketchSet`, SP-05): pass its `Touch`,
  `Explore` and `Hot` members. If SP-05 named those members differently at merge time, adapt **only
  in this file** — never in `internal/observer`.
- Ops registered through `Handle`: `ipc.Op("observe.tool") → OnToolUse`,
  `"observe.prompt" → OnUserPrompt`, `"observe.stop" → OnStop(…, req.Raw contains
  {"subagent":true})`, `"session.start" → OnSessionStart`, `"flush" → OnSessionEnd`.
  Each handler recovers panics, converts `(hookio.Output, error)` into
  `ipc.Response{OK: err == nil, Mode: mon.Mode(), Hot: reg.HotPathMode(), Output: &out}` and never
  returns a Go error to the transport.
- `if p, ok := obsv.(observer.Persister); ok { o.Idle().Register("observer.persist", 50, p.Persist) }`
  so O3 idle time flushes the DAG and the state file. Priority `50` is mid-band: below SP-12's
  frontier advancement, above GC.

---

## Test plan (TDD)

Every test below is written and run (failing) before the implementation it covers, inside the
commit that introduces it. Fixtures come from `internal/testutil` (SP-01) and
`testdata/corpora/toolout/` (SP-04). Fakes live in `internal/observer/fakes_test.go`:
`fakeStore` (in-memory, records every call), `fakeGraph` (records nodes/edges), `fakeGrammar`,
`panicBloomStore`, `fakeSymbols`, and `testutil.FakeClock`.

### `tombstone_test.go`

| Test | Setup / input | Expected output |
|---|---|---|
| `TestTombstone_DesignExample` | `ToolUseRecord{Root: hash whose hex starts `a3f2c19d0b74`, Bytes: 2458, Tool: "FileRead", Path: "src/auth.ts"}` | `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]` |
| `TestTombstone_NoPathUsesArgsPreview` | `Tool: "Bash"`, `Path: ""`, `ArgsPreview: "go test ./internal/store/..."`, `Bytes: 812` | `[cleared: sha256:…… · 812B · Bash go test ./internal/store/... · re-expandable]` |
| `TestTombstone_LongPreviewTruncatedTo48Runes` | `ArgsPreview` of 200 ASCII chars | subject is exactly 47 runes + `…` |
| `TestTombstone_MegabyteSize` | `Bytes: 3_500_000` | contains ` · 3.3MB · ` |
| `TestTombstone_SupersededMarker` | `Status: StatusSuperseded` | `… · superseded · re-expandable]` |
| `TestTombstone_EphemeralMarker` | `Ephemeral: true` | `… · ephemeral · re-expandable]` |
| `TestTombstone_BothMarkers` | ephemeral + superseded | `… · ephemeral · superseded · re-expandable]` |
| `TestTombstone_NoSubject` | `Path: ""`, `ArgsPreview: ""` | `[cleared: sha256:…… · 0B · Bash · re-expandable]` (no double space) |
| `TestTombstoneGolden` | 12 records covering every branch | byte-identical to `testdata/golden/observer/tombstones.txt` |
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
| `TestOnToolUse_ArgsDigestAndPreview` | Bash `go test ./...` | `ArgsPreview == "go test ./..."`; `ArgsDigest == core.HashBytes("qompack.args.v1", compactJSON)` |
| `TestOnToolUse_PreviewTruncatedAt120Runes` | 500-char command | preview is 119 runes + `…` |
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
| `TestSupersede_IdenticalRootMarksEarlier` | same file read twice, identical bytes | `MarkSuperseded(older, newer)` called once; one `EdgeSupersedes` from newer→older |
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
| `TestSupersede_PrevOnPathIsMostRecent` | real store; three reads of one path | the third call's `prevOnPath` is the **second** read's id, and `graph_test`'s `EdgeSharedFile` points at it |
| `TestSupersede_PrevOnPathEmptyForFirstTouch` | first read of a path | `prevOnPath == ""`; zero `EdgeSharedFile` |
| `TestIsSuperset_EmptyOlder` | `older == nil` | `false` |
| `PropertyIsSupersetReflexive` (rapid) | random chunk sets | `isSuperset(x, x) == true`; `isSuperset(x∪y, x) == true` |

### `graph_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestGraph_ToolUseProducesResult` | one tool use | nodes `tooluse:<id>` (KindToolUse) and `toolresult:<id>` (KindToolResult); edge `EdgeProduces` between them |
| `TestGraph_SequenceChain` | two tool uses in one turn | `EdgeSequence` `toolresult:1 → assistant:<s>:<t>` and `assistant:<s>:<t> → tooluse:2` |
| `TestGraph_FileConsumesAndProduces` | `Read a.ts` then `Write a.ts` | `EdgeConsumes` from the read, `EdgeProduces` from the write, both to `file:a.ts` |
| `TestGraph_SharedFileEdge` | two reads of one path | `EdgeSharedFile` from the second tool use to the first |
| `TestGraph_SymbolEdges` | fakeSymbols returning `["refreshToken","parseJWT"]` | two `KindSymbol` nodes, two `EdgeSharedSymbol` edges |
| `TestGraph_SymbolsSkippedAboveCap` | body of `symbolScanCap+1` bytes | zero symbol nodes |
| `TestGraph_SymbolsCappedAt64` | 200 distinct names | exactly 64 symbol nodes |
| `TestGraph_PosIsMonotoneAndPreIncrement` | three tool uses of 100, 200, 300 tokens | result node `Pos` values 0, 100, 300 |
| `TestGraph_SegmentAnchor` | `st.Segment == 7` | `EdgeSequence` from `segment:7` to the tool-use node |
| `TestGraph_NilSymbolsTolerated` | `Options.Symbols == nil` | no panic, zero symbol nodes |
| `TestGraph_FlushNotCalledPerToolUse` | 10 tool uses | `fakeGraph.FlushCalls == 0` |
| `TestNodeIDFormats` | table | `tooluse:toolu_1`, `toolresult:toolu_1`, `assistant:s:3`, `userprompt:s:3`, `file:src/a.ts`, `symbol:refreshToken`, `segment:7` |

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
| `TestOnUserPrompt_StoresVerbatim` | prompt `"fix the pgbouncer 1.18 pool bypass"` | `PutBytes` receives exactly those bytes; `Canon.Strip == nil`; `Canon.MinHash.Enabled == false` |
| `TestOnUserPrompt_RecordsIndexEntry` | same | `RecordToolUse` with `Tool == "UserPromptSubmit"`, `ID == "prompt_<session>_0"` |
| `TestOnUserPrompt_TurnIncrements` | two prompts | ids `prompt_s_0`, `prompt_s_1`; `st.Turn == 2` |
| `TestOnUserPrompt_DAGNodeAndSegmentEdge` | `st.Segment == 3` | `KindUserPrompt` node; `EdgeSequence` `segment:3 → userprompt:s:0` |
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
| `TestOnStop_SubagentNameFromExtra` | `Extra["subagent_type"] = "\"code-reviewer\""` | `Agent == "code-reviewer"` |
| `TestOnStop_SubagentNameFallback` | no Extra keys | `Agent == "subagent"` |
| `TestOnStop_SummaryFromTranscriptTail` | empty `ToolResponse`; temp JSONL whose last assistant line has two text blocks | `Summary == "block one\nblock two"` |
| `TestOnStop_TranscriptMissingIsSilent` | `TranscriptPath` points at a nonexistent file | `Summary == ""`, capture still written, no error |
| `TestOnStop_EmptySummaryStillStoresHashes` | no response, no transcript, 2 tool uses | capture written with 2 refs |
| `TestOnStop_ConsumesEdges` | 2 refs | two `EdgeConsumes` from the capture node to each tool-use node |
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
| `TestOnSessionEnd_Order` | recording fakes | call order exactly: `Segments().Close`, `Graph.Flush`, `Store.Flush`, `sketch.Save`×2, state write, `Store.GC` |
| `TestOnSessionEnd_GCPolicyFromConfig` | defaults | `GCPolicy{RetainDays:30, RetainSessions:10, DryRun:false, Deadline:8s}` |
| `TestOnSessionEnd_GCFailureIsSoft` | GC errors | returns `hookio.Empty(), nil`; `observer.err.gc == 1`; state file still written |
| `TestOnSessionEnd_NeverWritesTriedBloom` | real temp project | `.qompack/sketches/tried.bloom` does not exist after SessionEnd |
| `TestOnSessionEnd_SegmentClosedWithFeatures` | 16 prior events | `Close` receives `endTurn == st.Turn` and a 5-key feature map |
| `TestState_RoundTrip` | populate, `persistState`, new observer, `loadState` | turn, prefix tokens, segment, tool-use ring all restored |
| `TestState_CorruptFileRecovers` | write `{{{` to `state/observer.json` | fresh state; `observer.json.bad` exists; `Warn` logged; no error |
| `TestState_AtomicWrite` | inspect during write via `testutil` | no partial file observable at the target path |

### `test/e2e/observer_e2e_test.go` (new)

| Test | Setup | Expected |
|---|---|---|
| `TestE2E_ObserverThroughDaemon` | real binary, real daemon, real store on `t.TempDir()`; send `session-start`, 40 `observe tool`, 3 `observe prompt`, 1 `observe stop --subagent`, `flush` | every hook exits 0; `index/tool_use.jsonl` has 44 lines; `dag/deps.jsonl` non-empty; `sketches/touch.cms` and `explore.hll` exist; `sketches/tried.bloom` does not |
| `TestE2E_HooksExitZeroUnderFaultInjection` | make `.qompack/objects` read-only, then drive the same sequence | every hook exits 0; `LOUD.log` or the counter file records the failures |
| `TestE2E_SupersessionVisibleAfterRestart` | read a file twice, `flush`, restart daemon, read `tool_use.jsonl` | the first record carries `"status"` superseded and `superseded_by` of the second |
| `TestE2E_VerbatimPromptSurvivesRestart` | prompt, flush, restart, `store.Open(root)` | bytes equal the original prompt exactly |

### `test/e2e/phase1_exit_test.go` (new) — the Phase 1 exit criterion

Fixtures are generated in-test with `eval.Synthesize` at fixed seeds, so the number is reproducible
offline and does not depend on filenames SP-02 chooses:

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
```

**Driving a synthetic session through the observer.** `eval.Session` is a turn list, not a hook
stream, so the harness materializes hook events itself — one helper, no ambiguity:

```go
func eventsFor(s eval.Session) []hookio.Event {
    var out []hookio.Event
    for _, t := range s.Turns {
        if t.Role == "user" {
            out = append(out, hookio.Event{HookEventName: "UserPromptSubmit",
                SessionID: core.SessionID(s.ID), Prompt: t.Text})
            continue
        }
        for _, tc := range t.ToolCalls {
            out = append(out, hookio.Event{HookEventName: "PostToolUse",
                SessionID: core.SessionID(s.ID), ToolName: tc.Name, ToolUseID: tc.ID,
                ToolInput: tc.Args, ToolResponse: tc.Result})
        }
    }
    return out
}
```

The test calls `OnUserPrompt` for `UserPromptSubmit` events and `OnToolUse` for `PostToolUse`
events, in order, against a real `store.Open` on `t.TempDir()` with a `FakeClock` advancing 1 s per
event, then reads `store.Stats(ctx)`. **"Raw transcript" is `Stats().RawBytes`** (the pre-dedup byte
count SP-06 accumulates) and **"store size" is `Stats().Bytes`**; the ratio asserted is
`Stats().DedupRatio`, which §5.8 defines as `RawBytes / Bytes`. No other definition of the ratio is
used anywhere in this subplan.

| Test | Assertion |
|---|---|
| `TestPhase1_DedupRatioReadHeavy` | drive every `ToolCall` of `eval.Synthesize(seedReadHeavy, readHeavy)` through `observer.OnToolUse` against a real store with canonicalization **on**; `store.Stats().DedupRatio >= 4.0`. **This is the §10 Phase 1 exit criterion.** |
| `TestPhase1_CanonicalizationGapOnTestOutput` | run `testOutputHeavy` twice — once with `store.canonicalize.enabled=true`, once `false` — and assert `ratioOn >= ratioOff*1.25`. The ≥25% figure is this subplan's operational reading of *"the gap on test-output-heavy sessions justifies O2 on its own"*; both raw numbers are printed and written to `phase1-dedup.json` in the test's temp dir regardless of pass/fail. |
| `TestPhase1_ReportArtifact` | the emitted JSON has keys `read_heavy_ratio_canon`, `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`, `raw_bytes`, `store_bytes`, `objects`, `tool_uses` |
| `TestPhase1_StoreGrowthSublinear` (§11.3 guardrail) | bytes stored over the second half of the read-heavy session are strictly less than over the first half |
| `TestPhase1_CorpusSweep` | when `testdata/sessions/synthetic/` exists, replay every session in it and log each ratio; informational, fails only if any session panics |

**Hook p99 < 15 ms** is the other half of the exit criterion and is measured by SP-05's existing
harness, now with a non-trivial handler behind it:

```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-observer.json
```

`TestPhase1_HotPathBudgetDocumented` asserts the committed ADR records a B-A p99 figure from the
three CI platforms; the gate itself is the `bench-gate` job, which fails the build if B-A p99 ≥ 15
ms. Micro-benchmarks `BenchmarkOnToolUse_*` and `BenchmarkTombstone` cover B-C.

### Fixtures needed

- `testdata/golden/observer/tombstones.txt` — 12 rendered markers, created in commit 1.
- `testdata/corpora/toolout/` — SP-04's committed raw bash/test/grep output; reused, not extended.
- `internal/observer/testdata/transcript_tail.jsonl` — 40-line synthetic transcript with sidechain
  assistant messages, for `TestOnStop_SummaryFromTranscriptTail`.
- No new session fixtures: Phase 1 uses `eval.Synthesize`.

---

## Commit plan

All work happens on **`feat/sp08-observer-l0`**, cut from `develop` with SP-01, SP-03, SP-04,
SP-05, SP-06 and SP-07 already merged:

```
git fetch && git checkout develop && git pull
git checkout -b feat/sp08-observer-l0
```

Conventional Commits per §10: `<type>(<scope>): <subject>`, body explains the decision, footer
`Refs:` names the subplan, gaps and design sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(observer): addressable tombstones, tool classification, and task-boundary signals`

- [ ] Write `internal/observer/tombstone_test.go` and `signals_test.go` in full (all rows of both
      tables above, plus `BenchmarkTombstone` and `FuzzExtractSignals`). Run
      `go test ./internal/observer/ -run 'Tombstone|Signals|TestOutcome|PathsFromInput|ResponseText'`
      and confirm they **fail** against SP-01's stub.
- [ ] Add `internal/observer/doc.go`, `tombstone.go`, `signals.go`.
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
- [ ] Flip off the `t.Skip`s in SP-01's observer conformance suite, or create
      `internal/observer/observertest` with `RunObserverSuite` if SP-01 shipped none (see
      **Exit criteria**), and run it against the real implementation.
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
- [ ] Add `internal/daemon/observer_ops.go` with `WireObserver`, `symbolAdapter`, the five op
      registrations, the mode mapping, the feature/signal forwarding, and the idle `Persist`
      registration.
- [ ] Add `test/e2e/observer_e2e_test.go` (all four rows).
- [ ] `go run ./tools/devtool build test test-race` and `go test ./test/e2e/ -run ObserverE2E` —
      green on Windows and on Linux.
- [ ] Confirm the import-graph check in `verify` still passes (observer must not have acquired an
      import of `scheduler`, `checkpoint`, `symbols` or `contract`).
- [ ] Footer: `Refs: SP-08, §7.3 SessionStart/SessionEnd, §8.2 GC, §5.21 split ownership`

### Commit 7 — `test(observer): Phase 1 exit-criterion harness and hot-path benchmarks`

- [ ] Add `test/e2e/phase1_exit_test.go` with the five rows, the two `SynthSpec` literals and
      `eventsFor`. TDD ordering for a gate commit: write the assertions **at the design's numbers
      first** (`>= 4.0`, `ratioOn >= ratioOff*1.25`), run them, and only then tune the observer. If
      an assertion fails, the fix goes in `internal/observer` (or in the `PutOptions` it passes) —
      **the threshold is never weakened**; a genuine need to move it is a §11.3 sign-off, not an
      edit.
- [ ] Run `go test ./test/e2e/ -run Phase1 -v`; record `read_heavy_ratio_canon`,
      `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon
      --json bench-observer.json` locally; record B-A p50/p99 and B-B p99.
- [ ] Add `docs/adr/0008-observer-l0.md`: the eight resolved decisions, the §8.1-item-7 destination
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
  against SP-05's actual `Options`/`SessionRegistry`/`Handle` shapes as merged.
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

- [ ] Every `t.Skip` in the observer conformance suite shipped by SP-01 is removed (Rule W-1); the
      suite passes against the real implementation. §5.22's named list of `<pkg>test` packages is
      illustrative and does not spell `observertest`; if SP-01's `develop` has no
      `internal/observer/observertest` package, SP-08 **creates** it in commit 2 — §5.22 says "for
      every interface above", §5.21 is one of those interfaces, and later waves need a factory-based
      suite to test their own `Observer` doubles against. Its shape is
      `func RunObserverSuite(t *testing.T, name string, factory func(t *testing.T) observer.Observer)`
      covering: all five methods return `hookio.Empty()` and a nil error on a well-formed event;
      none panics on a zero `hookio.Event`; all five return `ctx.Err()` on a pre-cancelled context;
      and `Tombstone` on a zero record is non-empty and single-line.
- [ ] `go run ./tools/devtool ci-local` green: `verify` (gofumpt clean, `golangci-lint` clean,
      `go vet`, `nomagic`, import-graph layer check, test-only-dep check, build), `test` and
      `test-race`, `cover` (≥ 75% for `internal/observer`), `crossbuild`, `bench-gate`,
      `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] `internal/observer` imports exactly: `core paths config logging obs hookio store canon sketch
      dag grammar tokens` plus stdlib. No `scheduler`, `checkpoint`, `symbols`, `contract`,
      `negknow`, `analyzer`, or `daemon`.
- [ ] Zero occurrences of `Bloom` in non-test files under `internal/observer`
      (`TestObserverSourceHasNoBloomReference`).
- [ ] `.qompack/sketches/tried.bloom` is never created by any observer code path
      (`TestOnSessionEnd_NeverWritesTriedBloom`, `TestE2E_ObserverThroughDaemon`).
- [ ] Every hook subcommand still exits 0 under fault injection
      (`TestE2E_HooksExitZeroUnderFaultInjection`) — §13 invariant 6.
- [ ] Exactly 7 commits on `feat/sp08-observer-l0`, all conventional, none carrying an attribution
      trailer; CI's trailer grep passes.
- [ ] `docs/adr/0008-observer-l0.md` exists and records the eight resolved decisions plus every
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
      (Rule W-3). Any need for one was raised as an `arch/` amendment instead.
- [ ] Only one file outside `internal/observer` and the test trees was added:
      `internal/daemon/observer_ops.go`.
- [ ] `nomagic` clean: every literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and
      `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` inside `internal/observer` is
      either read from `config` or carries a `//nomagic:allow <reason>` comment (`kib = 1024`,
      `argsPreviewMax = 120`).
- [ ] Commit count verified: `git rev-list --count develop..feat/sp08-observer-l0` is **7**, within
      the 5–8 band.
- [ ] `git log develop..feat/sp08-observer-l0 --format=%B | grep -Ei 'co-authored-by|signed-off-by|
      generated with|🤖'` returns nothing. **Do not add Co-Authored-By lines or any attribution
      trailers to any commit message.**
- [ ] `Qompack.md` is unmodified: `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] Self-review pass: re-read `tooluse.go`'s 14 steps against the §8.1 responsibility list, and
      re-read `session.go`'s `OnSessionEnd` against §7.3's "Flush, compact the store, write session
      index" and §8.2's "run on `SessionEnd`", confirming both orderings match this document.

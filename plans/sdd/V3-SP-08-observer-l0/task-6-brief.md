# Task brief — Commit 6: SessionStart/SessionEnd, state persistence, daemon wiring (observer_ops.go), e2e

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject: `feat(observer): SessionStart/SessionEnd lifecycle and daemon wiring`.
- CRITICAL plan defect, ruled: `runDaemon` (internal/cli/daemon.go:81-86) sets only `opts.Log/Metrics/Clock`; `opts.Store`, `opts.Graph`, `opts.Sketches` (and Ledger/Grammar/Sched/Checkpoints) are nil and NO production code opens a store or DAG. Therefore `daemon.WireObserver(o *Options)` MUST itself open what it needs when the field is nil — `store.Open(o.ProjectRoot, o.Cfg, store.Deps{…})` (find the Deps shape and how `internal/testutil/project.go:272` / `test/guards/probes.go:58` build one), `dag.Open(...)`, and a `*SketchSet` via the daemon's own constructor loaded from `.qompack/sketches/` — and assign them back onto `*Options` so `daemon.New` copies the SAME instances into `Services` (check how `New` builds `svc` from `Options`). `runDaemon`'s diff stays exactly the two wiring blocks the brief describes. Check whether the daemon has a shutdown/close path for services; if it does not, document in observer_ops.go that the store is flushed by `OnSessionEnd`/`Persist` and lives for the process.
- `plans/00-ARCHITECTURE.md` §5.4 already declares `Services.Mode`; the amendment landed `Mode` on the struct and the hoist in `daemon.New`. Keep the plan's nil guard on `modeSrc`.
- `paths.WriteAtomic(p, b, perm fs.FileMode)` has three args. `IdleController.Register(name string, prio int, fn func(ctx) error)` matches the plan.
- `negknow.Ledger.RefreshStaleness`: verify the exact signature on this branch before calling it (SP-09 is in flight on another branch; on develop it may be a stub — call whatever the interface declares, nil-tolerant).
- `Persist` may already be complete from Commit 2; keep it satisfying `Persister`.
- e2e tests: follow the existing `test/e2e` harness helpers for building/running the real binary; hook subcommands are spelled as the CLI defines them (read `internal/cli`), not as the plan abbreviates them.
- The 75% coverage floor: `landedSubplans` is updated in the MERGE commit (SP-06/07 precedent), not on this branch — do not edit tools/devtool.


---
<!-- plan lines 82-92 -->

### §7.3 — hook surface (the six rows this subplan touches; the `PreCompact` row is SP-10's)

| Hook | Layer | Responsibility |
|---|---|---|
| `PostToolUse` | L0 | Chunk and store tool results; update DAG, sketches, Sequitur; detect redundancy |
| `UserPromptSubmit` | L0 | Capture user intent **verbatim and immutably** (closes G2.3); update BOCD features |
| `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |
| `PostToolUse` (todo/git) | L3 | Task-boundary signals for the scheduler |
| `Stop` / `SubagentStop` | L0 | Capture subagent detail before it is double-compressed (closes G10.1) |
| `SessionEnd` | L1 | Flush, compact the store, write session index |


---
<!-- plan lines 123-128 -->

### §8.2 — the store facts L0 must maintain

> **File version history.** `index/files.json` maps path → list of `(timestamp, root_hash)`. This gives cheap answers to "what did this file look like when we made that decision," which is the most common thing lost across compaction.
>
> **Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.


---
<!-- plan lines 192-253 -->

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

---
<!-- plan lines 280-527 -->

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
<!-- plan lines 1627-1972 -->

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


---
<!-- plan lines 2173-2204 -->

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


---
<!-- plan lines 2467-2513 -->

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


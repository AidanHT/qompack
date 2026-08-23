# SP-13: L6 retrieval: the stdio MCP server and all eight tools with ephemeral-at-birth results, minimal-span defaults, and expansion promotion counting

> **Recommended model: Opus 5 · xhigh effort**
>
> Hand-rolled JSON-RPC 2.0 over stdio, eight handlers, a symbol-aware span resolver and ephemeral tagging. Protocol and plumbing work against a fully-specified contract.

**Branch:** `feat/sp13-mcp-retrieval-layer` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of `SP-01`, `SP-05`, `SP-06`, `SP-09` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-10 checkpointer, SP-11 rehydrator, SP-12 scheduler) | **Design sections:** §7.2 L6, §8.7, Appendix C `retrieval`, §12 (retrieval re-inflation row) | **Gaps closed:** G6.2

---

## Mission

This slice builds layer L6: the affordance layer. `Qompack.md` §8.7 opens with the reason it exists — *"The affordance layer. Without it, the store is a write-only log."* SP-06 made the store correct and SP-09 made negative knowledge durable, but nothing in the running session can reach either. This subplan delivers the reach: a hand-rolled JSON-RPC 2.0 MCP server over stdio (D6 in `00-ARCHITECTURE.md` §1), served by `qompack mcp`, executing all eight tools of the §8.7 table against the daemon's live handles.

It also owns the policy that keeps the affordance from becoming the disease. §8.7 names the self-defeating loop explicitly: every `expand` and `re_read` returns content *into* the context window, so an unpoliced retrieval layer recreates exactly the bloat it exists to solve. Three mechanisms answer it and all three are SP-13's: results are **tagged ephemeral at birth** (`_meta.qompack.ephemeral` on the response, plus a `store.ToolUseRecord` with `Ephemeral: true` so `analyzer.Block.Ephemeral` ranks it first for eviction, ahead of ordinary tool results); retrieval returns the **minimum sufficient span** by default, resolved through chunk boundaries and a symbol-aware widener, with an explicit `full=true` escape hatch; and repeated expansion of the same hash is counted by a **Promoter** that fires at `retrieval.promoteAfterExpansions`, so SP-16 can promote demand-proven content into the next checkpoint's pointer tier.

**What exists when you start.** `develop` carries SP-01 (the whole foundation: `core`, `paths`, `config` with the full Appendix C schema plus the §11.5 `runtime` namespace, `logging`, `obs`, `tokens`, `hookio`, `cli` dispatch, `testutil`, the `test/e2e` harness, `testdata/golden/contracts/**`, and a compiling `internal/mcp` stub returning `core.ErrNotImplemented` with an `mcptest.RunMCPSuite` conformance suite whose behaviour tests are `t.Skip`ped), SP-05 (`internal/ipc` transport and client, `internal/daemon` with its op-routing table / `IdleController` / late-bound `Services`, `internal/contract` with the `mcp.server_registered` assertion reporting `not-yet-implemented`), SP-06 (a real `store.Store`: `Search`, `OpenSpan`, `GetRoot`, `ToolUse`, `FileHistory`, `FileAt`, `SegmentLog`), and SP-09 (a real `negknow.Ledger` with the three-way `Answer`). `checkpoint.Reader` and `rehydrate.DropReporter` are still SP-01 stubs — SP-10 and SP-11 are same-wave siblings — so `why` is developed against the SP-01 fixtures in `testdata/golden/contracts/checkpoint/` and `dropped` against an in-test fake seeded from the same fixtures (there is no `contracts/rehydrate/` fixture and SP-13 does not create one), both under Rule W-2 and both re-verified against the real `checkpoint.OpenReader` and `rehydrate.NewReporter` at the wave-3 merge.

**What exists when you finish.** `internal/mcp` is complete: a panic-isolated JSON-RPC 2.0 stdio server handling `initialize`, `notifications/initialized`, `tools/list`, `tools/call` and `ping`; eight tools with published JSON input schemas and an in-repo schema validator that gates both runtime arguments and the conformance suite; a deterministic minimal-span resolver; the ephemeral tagging path; the Promoter with persisted per-session counts. `qompack mcp` runs the server as a thin transcoder that forwards `tools/call` to the daemon, where the handlers actually execute against live state. The `mcp.server_registered` observable is produced through SP-05's own seam — `InstallMCPOp` binds `daemon.Services.MCPInitialized` (which is what makes `contract.DeclareProducers` declare the producer instead of reporting `not-yet-implemented`) and the daemon sets `contract.SessionHistory.MCPInitialized` in `state/history.json` — the path `contract.HistoryPath(root)` returns, never `state/contract.json`, which is the Monitor's own persisted mode/reason/results — on the first `initialize`; `.qompack/state/mcp.json` is written alongside it as the human-readable handshake record used by `/qompack:status` and the e2e suite. `docs/mcp-tools.md` is generated from the tool table and diffed in CI, and budget **B-F** (`mcp_tool_call` p95 < 250 ms at `minimal` span) is benchmarked and gated.

---

## Design context (verbatim from Qompack.md)

### §7.2 — the layer L6 sits in

```
├─────────────────────────────────────────────────────────────────┤
│ L6  RETRIEVAL (MCP)       recall · re_read · already_tried      │
│                           timeline · why · dropped              │
├─────────────────────────────────────────────────────────────────┤
```

> Data flows up on the write path (L0 → L1 → L2), down on the read path (L3 → L4 → L5 → context). **L6 is a lateral affordance available to the agent at any time.** L7 is offline.

### §8.7 — L6 Retrieval tools (MCP), in full

> The affordance layer. Without it, the store is a write-only log.

| Tool | Signature | Purpose |
|---|---|---|
| `recall` | `(query, k=5)` → hits | Search the store by content, path, or symbol; returns hashes and summaries |
| `expand` | `(hash \| tool_use_id)` → content | Re-materialize a cleared tool result |
| `re_read` | `(path, at=null)` → content | Current or historical version of a file |
| `already_tried` | `(target, approach)` → bool + reason | Bloom membership plus the stored reason when present |
| `record_eliminated` | `(target, approach, reason)` | Write negative knowledge |
| `timeline` | `(from, to)` → segments | What happened between two points |
| `why` | `(decision_id)` → rationale | Retrieve a decision and its evidence |
| `dropped` | `()` → list | What is currently out of context |

> **Design note:** `already_tried` should be surfaced in the rehydrated context as a *standing instruction*, not merely an available tool. "Before committing to an approach, call `already_tried`." Otherwise the affordance exists and goes unused.

> **Retrieved content is born ephemeral (closes GB).** There is a self-defeating loop hiding here: every `expand` and `re_read` returns content *into the context window*, where it accumulates like any tool result — unpoliced, the retrieval layer recreates the exact bloat it exists to solve. The fix follows from the store's own guarantee: retrieved content is already indexed, so re-clearing it is free and lossless — it can always be retrieved again for the price of a tool call. Therefore:
>
> - Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).
> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch. Most post-compaction questions are "what did that one function look like," not "give me the file."
> - Repeated expansion of the same hash within a session is a signal, not a cost: the Analyzer promotes frequently-re-expanded content into the next checkpoint's pointer tier with a higher slice weight, so the system *learns* what eager restoration should have included — a demand-driven correction to the 8–12K rehydration budget.

### Appendix C — the `retrieval` block (verbatim; also §11.1 of `00-ARCHITECTURE.md`)

```jsonc
  "retrieval": {
    "ephemeralResults": true,
    "defaultSpan": "minimal",        // "minimal" | "full"
    "promoteAfterExpansions": 2
  },
```

Related Appendix C keys this slice reads and never redefines:

```jsonc
  "store": {
    "chunk": { "min": 1024, "target": 4096, "max": 16384 },
```
```jsonc
  "eliminations": {
    "requireEvidence": true,
    "defaultScope": "session",
    "rebuildOnStale": "nextIdle",
    "staleResponse": "flag"          // "flag" | "drop"
  },
```

`00-ARCHITECTURE.md` §11.5 runtime extension (also read-only for this slice):

```jsonc
  "mcp": { "spanWidenLines": 40, "maxResponseBytes": 262144 }
```

`00-ARCHITECTURE.md` §11.3 validation rules binding here: `defaultSpan ∈ {minimal, full}`; `promoteAfterExpansions ≥ 1`; `staleResponse ∈ {flag, drop}`; `defaultScope ∈ {session, project}`.

### §12 — risk register, the row this slice owns

| Risk | Severity | Mitigation |
|---|---|---|
| Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |

Also from §12, degradation doctrine (`00-ARCHITECTURE.md` §12.1 and §12.3):

> `ModeDegradedPassive` behaviour: … **MCP retrieval tools stay available, because they are pull-based and cannot make anything worse.**

> | MCP tool panic | recovered at the handler boundary, returned as `IsError`, never kills the server |

> | bloom load fails | rebuild from `records/eliminations.jsonl` (§3.3); if that fails, `already_tried` returns `absent` for everything — never a false positive |

### §3 gap G6.2 and its §9 closure row

| ID | Gap |
|---|---|
| G6.2 | **Direct cause of the most-reported failure mode** — the agent re-attempts something already eliminated, burns time, and eliminates it again. |

| Gap | Closed by | Residual |
|---|---|---|
| G6.2 re-attempt loop | L6 `already_tried` standing instruction | — |

### §8.3 — the three-way response `already_tried` must render

> 4. `already_tried` responses distinguish the cases: *active* returns the reason; *stale* returns "previously eliminated, but the evidence has changed since — re-verification may be warranted," which is strictly more useful to the agent than either a block or silence.
> 5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).

### §8.3 — the canonical descriptor `record_eliminated` writes through

> Canonical descriptor: `(normalized_path, symbol_or_null, approach_class, reason_hash)`. Insert into `tried.bloom`.

> 2. Every elimination carries `depends_on`: the hashes of the files or configs the elimination's reason rests on (lockfiles, compose files, the file under test).

### §10 Phase 2 — the exit criterion naming these two tools

> - `record_eliminated` and `already_tried` MCP tools, including the three-way active/stale/absent response
>
> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change.

### §10 Phase 7 — what the Promoter feeds (SP-16 consumes it, SP-13 produces it)

> - Demand-driven rehydration tuning: promote frequently-re-expanded hashes (§8.7) into the checkpoint pointer tier

### §11.2 — the secondary metric this slice makes measurable

| Metric | Definition |
|---|---|
| Retrieval hit rate | How often `expand`/`re_read` is called, and whether it prevented a re-read |

### `00-ARCHITECTURE.md` §2.4 — budget B-F (normative, CI-gated)

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

### `00-ARCHITECTURE.md` §5.16 — the span default, verbatim

> **Span default.** `expand` and `re_read` return the *minimum sufficient span* by default (`retrieval.defaultSpan: "minimal"`): the matching function/hunk resolved via the store's chunk boundaries plus a symbol-aware widener, capped at `store.chunk.max`. `full=true` is the escape hatch. Every response carries `_meta.qompack.ephemeral=true` when `retrieval.ephemeralResults` is set, and the observer records the resulting tool-use record with `Ephemeral: true` so `analyzer.Block.Ephemeral` ranks it first for eviction.

### `00-ARCHITECTURE.md` §12.1 — the contract observable this slice produces

| Assertion | How it is asserted |
|---|---|
| `mcp.server_registered` | the MCP server received `initialize` at least once this session |

---

## Out of scope

| Item | Owner |
|---|---|
| The seven slash commands (`status`, `recall`, `pin`, `checkpoint`, `why`, `dropped`, `eval`) and `plugin/commands/*.md`. `commands.Deps` calls the MCP handlers; it never reimplements them. | **SP-14** |
| `checkpoint.Reader`, `checkpoint.Writer`, `checkpoint.ExtractDecisions`, and the minting of `core.DecisionID`. `why` consumes `Reader`; it does not produce decisions. | **SP-10** |
| `rehydrate.DropReporter` implementation, the eight-item injection, and the in-context standing instruction (`rehydrate.StandingInstruction()`). `dropped` consumes the reporter. | **SP-11** |
| The negative-knowledge ledger itself: descriptors, `Canonicalize`, staleness flip, `RefreshStaleness`, `RebuildBloom`, `records/eliminations.jsonl`. `record_eliminated` and `already_tried` are thin fronts over `negknow.Ledger`. | **SP-09** |
| `store.Search`, `store.OpenSpan`, `store.GetRoot`, `SegmentLog`, GC, exact token accounting. | **SP-06** |
| `symbols.Extractor` (`Extract`, `Enclosing`, `References`). SP-13 declares a two-method `mcp.Widener` port and an adapter in the composition root; it never reimplements extraction. | **SP-04** |
| Droppable-block classification and the eviction ordering that consumes `Ephemeral` (`ReclaimableTokens`, p-selection). SP-13 sets the flag; SP-12 ranks on it. | **SP-12** |
| Consuming `Promoter.Promoted()` into the next checkpoint's pointer tier with a higher slice weight. SP-13 counts and exposes; SP-16 promotes. | **SP-16** |
| `internal/ipc` transport/framing/client and `internal/daemon` internals (registry, queue, workers, idle loop). SP-13 uses the `Handle` op seam and the `ipc.Client`. | **SP-05** |
| `internal/contract`'s `CMCPRegistered` assertion logic and the degradation state machine. SP-13 writes the observable file it reads. | **SP-05** |
| Generating `plugin/.mcp.json` from `internal/pluginmanifest`, and packaging `plugin/bin`. | **SP-01** (manifest) / **SP-17** (packaging) |
| `docs/user-guide.md`'s MCP chapter, `docs/troubleshooting.md`. `docs/mcp-tools.md` (generated) is SP-13's. | **SP-18** |
| The replay-harness metric "Retrieval hit rate" computation. SP-13 emits the `obs` counters it reads. | **SP-02** |

---

## Interface contract

### Consumes (exact signatures from `00-ARCHITECTURE.md` §5)

Every signature below is reproduced exactly. The `store.Store`, `store.SegmentLog` and
`checkpoint.Reader` blocks list **only the methods SP-13 calls** — the full interfaces are §5.8 and
§5.14 and are not restated or altered here. SP-13 calls no method outside these lists.

```go
// §5.8 internal/store
type Store interface {
    PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
    GetRoot(ctx context.Context, root core.Hash) (Root, error)
    GetChunk(ctx context.Context, h core.Hash) ([]byte, error)
    Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
    OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error)
    Has(h core.Hash) bool
    RecordToolUse(ctx context.Context, rec ToolUseRecord) error
    ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
    ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
    FileHistory(ctx context.Context, path string) ([]FileVersion, error)
    FileAt(ctx context.Context, path string, at time.Time) (FileVersion, error)
    Search(ctx context.Context, q Query) ([]Hit, error)
    Segments() SegmentLog
}
type Query  struct{ Text, Path, Symbol, Tool string; Since time.Time; K int }
type Hit    struct{ Root core.Hash; ToolUseID core.ToolUseID; Path, Tool string
                    TS core.UnixMilli; Score float64; Summary string; Span [2]int64 }
type Root   struct{ Hash core.Hash; Chunks []ChunkRef; CanonBytes, RawBytes int64; Tokens core.Tokens }
type PutOptions   struct{ Tool, Path string; Canon canon.Options; KeepRaw, Ephemeral bool }
type PutResult    struct{ Root Root; Novel, Reused int; Signature sketch.Signature; NearDup *NearDupInfo }
type ToolUseRecord struct{ ID core.ToolUseID; Session core.SessionID; Turn core.TurnIndex
                    TS core.UnixMilli; Tool string; ArgsDigest core.Hash; ArgsPreview string
                    Root core.Hash; Path string; Bytes int64; Tokens core.Tokens
                    Signature sketch.Signature; Status Supersession; SupersededBy core.ToolUseID
                    Ephemeral bool; Subagent string }
type FileVersion  struct{ TS core.UnixMilli; Root core.Hash; Turn core.TurnIndex; Bytes int64 }
type SegmentLog interface {
    Get(ctx context.Context, id core.SegmentID) (Segment, error)
    Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
    Current(ctx context.Context, s core.SessionID) (Segment, error)
    Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
}
type Segment struct{ ID core.SegmentID; Session core.SessionID
                     StartTurn, EndTurn core.TurnIndex; StartTS, EndTS core.UnixMilli
                     Features map[string]float64; Tokens core.Tokens; EncodedOnce bool
                     CheckpointSeq core.CheckpointSeq; Closed bool; BloomRef string }

// §5.10 internal/negknow
type Ledger interface {
    Record(ctx context.Context, r Record) (string, error)
    Query(ctx context.Context, target, approach string, scope Scope) (Answer, error)
    Get(ctx context.Context, id string) (Record, error)
}
type Answer struct{ State AnswerState; Record *Record; Note string; BloomOnly bool }
// AnswerAbsent, AnswerActive, AnswerStale
func Canonicalize(target, approach, reason string) Descriptor

// §5.14 internal/checkpoint
type Reader interface {
    Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
    Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
    List(ctx context.Context) ([]Ref, error)
    Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error)
}
type Decision  struct{ ID core.DecisionID; What, Why string; AlternativesRejected []string
                       Evidence core.Hash; Turn core.TurnIndex }
type DropEntry struct{ Kind, ID, Detail string }

// §5.22b internal/symbols — consumed ONLY through the composition-root adapter (§3.2 forbids
// `mcp` importing `symbols`).
type Extractor interface {
    Extract(path string, b []byte) []Symbol
    Enclosing(path string, b []byte, off int) (Symbol, bool)
}
type Symbol struct{ Name, Kind string; Line, Offset, Len int }

// §5.4 internal/ipc (used by internal/cli only, never by internal/mcp)
type Client interface{ Send(ctx context.Context, req Request, deadline time.Duration) (Response, error); Close() error }
type Request  struct{ Op Op; Session core.SessionID; TS core.UnixMilli; Reply bool
                      Event *hookio.Event; Raw json.RawMessage }
type Response struct{ OK bool; Mode contract.Mode; Hot HotPathMode; Output *hookio.Output
                      Err string; Data json.RawMessage }
const OpMCP Op = "mcp"                                    // SP-05 ships the constant; use it, never a string literal
type SpoolWriter interface{ Append(req Request) error; Path() string }
type ClientOptions struct{ ProjectRoot string; State State; Self string
                           Spawn func(projectRoot, self string) error
                           ConnectDeadline, AckDeadline time.Duration; MaxLine int; Clock core.Clock }
func NewClientWithOptions(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry,
                          o ClientOptions) Client
func ReadState(projectRoot string, fallback config.Config) State

// §5.4 internal/daemon extension seams (SP-05). Both are called by InstallMCPOp BEFORE daemon.New,
// because New registers its own fallback `mcp` route only for ops not already registered and calls
// DeclareProducers(svc) after applying every Bind.
func (Options) Handle(op ipc.Op, h ipc.Handler)
func (o *Options) Bind(fn func(*Services))
type Services struct{ /* … */ MCPInitialized func(ctx context.Context) bool /* SP-13 binds this */ }
func ServicesFrom(ctx context.Context) *Services
func RegistryFrom(ctx context.Context) *SessionRegistry
func (r *SessionRegistry) Snapshot() []SessionState        // session resolution, see spec §10
type SessionState struct{ ID core.SessionID; Source, TranscriptPath string
                          StartedTS, LastActivityTS, EndedTS core.UnixMilli
                          Events, Dropped, Externalized int64; Hot ipc.HotPathMode; Live bool }
func SpawnDetached(projectRoot, self string) error

// §5.19 internal/contract (SP-05) — the mcp.server_registered observable is
// SessionHistory.MCPInitialized, read and written at HistoryPath (state/history.json) and NEVER at
// state/contract.json, which is the Monitor's own persisted mode/reason/results file: two distinct
// schemas sharing one path corrupt each other the first time both are written
// (internal/contract/history.go's HistoryPath comment says exactly this).
type SessionHistory struct{ /* … */ MCPInitialized bool `json:"mcp_initialized"` /* … */ }
func HistoryPath(projectRoot string) string
func LoadHistory(path string) *SessionHistory
func SaveHistory(path string, h *SessionHistory) error

// wave-3 siblings, wired only at the post-merge rebase (spec §11)
func checkpoint.OpenReader(root string, log logging.Logger, m obs.Registry) (checkpoint.Reader, error) // SP-10
func rehydrate.NewReporter(root string, log logging.Logger) *rehydrate.Reporter                       // SP-11, satisfies mcp.DropReporter

// foundation
func core.HashBytes(domain string, b []byte) core.Hash
func core.ParseHash(s string) (core.Hash, error)
func paths.Norm(projectRoot, p string) (string, error)
func paths.Key(p string) string
func paths.WriteAtomic(p string, b []byte) error
func config.Load(env config.Env) (config.Config, config.Provenance, []config.Warning, error)
```

### Produces (what later subplans rely on)

```go
// package mcp — §5.16 signatures, UNCHANGED:
type Tool struct{ Name, Title, Description string; InputSchema json.RawMessage
                  Handler Handler; Ephemeral bool }
type Content  struct{ Type, Text string; Meta map[string]any }
type Response struct{ Content []Content; IsError, Ephemeral bool; Meta map[string]any }
type Handler  func(ctx context.Context, r Request) (Response, error)
type Server interface {
    Register(t Tool) error
    Serve(ctx context.Context, in io.Reader, out io.Writer) error
    Tools() []Tool
}
func NewServer(name, version string, log logging.Logger) Server
func RegisterAll(s Server, d ToolDeps) error
type DropReporter interface{ CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error) }
type Promoter interface {
    NoteExpansion(ctx context.Context, sess core.SessionID, h core.Hash) (count int, promoted bool, err error)
    Promoted(ctx context.Context, sess core.SessionID) ([]core.Hash, error)
}
// arg structs RecallArgs, RecallHit, ExpandArgs, ReReadArgs, AlreadyTriedArgs,
// AlreadyTriedResult, RecordEliminatedArgs, TimelineArgs, WhyArgs, DroppedArgs — verbatim §5.16.
// §5.16 gives them no json tags; SP-13 adds snake_case tags matching the response bodies below.
// Two of them gain ADDITIVE fields (no §5.16 field renamed, retyped or removed) because the
// handlers documented in the Implementation spec emit strictly more than the §5.16 shape:
type RecallHit struct {                       // §5.16 fields Hash, Path, Tool, Summary, TS, Score
    Hash, Path, Tool, Summary string
    TS    string
    Score float64
    ToolUseID string   `json:"tool_use_id"`   // ADDED: store.Hit.ToolUseID, so a hit is expandable
    Span      [2]int64 `json:"span"`          // ADDED: store.Hit.Span, the chunk-aligned match window
}
type AlreadyTriedResult struct {              // §5.16 fields State, Reason, Note, Evidence
    State, Reason, Note, Evidence string
    Scope        string     `json:"scope,omitempty"`         // ADDED: negknow.Record.Scope
    RecordedAt   string     `json:"recorded_at,omitempty"`   // ADDED: RFC3339 of Record.TS
    DependsOn    []core.Dep `json:"depends_on,omitempty"`    // ADDED: the §8.3 staleness evidence
    StaleBecause []string   `json:"stale_because,omitempty"` // ADDED: negknow.Record.StaleBecause
    Degraded     bool       `json:"degraded,omitempty"`      // ADDED: §12.3 ledger-failure path
}

// ── ADDITIVE extensions SP-13 makes inside the package it owns (§5 permits adding to a struct
// you own; no existing field is renamed, retyped, or removed) ──
type Request struct {          // §5.16 fields Session, Name, Args, Deadline unchanged
    Session  core.SessionID
    Name     string
    Args     json.RawMessage
    Deadline time.Time
    Turn     core.TurnIndex    // ADDED: the session's current turn; 0 when unknown
}
type ToolDeps struct {         // §5.16 fields unchanged
    Store       store.Store
    Ledger      negknow.Ledger
    Checkpoints checkpoint.Reader
    Rehydrator  DropReporter
    Promoter    Promoter
    Cfg         config.Config
    Widener     Widener        // ADDED: nil-tolerant symbol port (§3.2 forbids importing symbols)
    ProjectRoot string         // ADDED: for re_read worktree resolution and the observable file
    Clock       core.Clock     // ADDED
    Log         logging.Logger // ADDED; nil ⇒ logging.Nop()
    Metrics     obs.Registry   // ADDED; nil ⇒ no-op registry
}

// ── new exported surface owned by SP-13 ──
type Widener interface {
    // Widen returns [start,end) grown so that end lands on the end of the symbol enclosing
    // end-1, given the buffer b whose byte 0 corresponds to file offset base.
    Widen(path string, b []byte, off, end int64) (newOff, newEnd int64, ok bool)
    // Find returns the [start,end) of the named symbol in b, or ok=false.
    Find(path string, b []byte, name string) (off, end int64, ok bool)
}
// ServerOptions is how the two things NewServer's §5.16 signature cannot carry — the maximum
// accepted line length and the initialize callback — reach the server. NewServer is UNCHANGED and
// is defined as NewServerWithOptions(ServerOptions{Name, Version, Log}); adding a constructor is
// additive to a package SP-13 owns, whereas adding a method to the §5.16 Server interface would
// require an amendment (§0) and is therefore not done.
type ServerOptions struct {
    Name, Version string
    Log           logging.Logger  // nil ⇒ logging.Nop()
    MaxLine       int             // 0 ⇒ cfg.Runtime.HotPath.MaxPayloadBytes; callers pass it explicitly
    OnInitialize  func(Observable) // nil-tolerant; invoked after the initialize result is built
}
func NewServerWithOptions(o ServerOptions) Server
func ToolDefs(d ToolDeps) []Tool                       // the eight tools, handlers bound to d
func ToolNames() []string                              // sorted; consumed by plugin-validate
func RegisterProxy(s Server, fwd Handler) error        // same eight defs, one forwarding handler
func Dispatch(ctx context.Context, s Server, r Request) (Response, error) // panic-isolated
func NewPromoter(statePath string, threshold int, clk core.Clock) (Promoter, error)
func WriteInitializedObservable(projectRoot string, o Observable) error
type Observable struct {
    Initialized     bool   `json:"initialized"`
    TS              int64  `json:"ts"`
    ProtocolVersion string `json:"protocol_version"`
    ClientName      string `json:"client_name"`
    ClientVersion   string `json:"client_version"`
    ServerVersion   string `json:"server_version"`
    Tools           int    `json:"tools"`
    PID             int    `json:"pid"`
}
const StandingInstruction = "Before committing to an approach, call already_tried."
var ErrToolNotFound = errors.New("qompack: mcp tool not found")

// package daemon (new file, SP-13-owned): the op-routing installation.
// Calls o.Handle(ipc.OpMCP, …) AND o.Bind(func(s *Services){ s.MCPInitialized = … }), both before
// daemon.New(o), which is what makes contract.DeclareProducers declare CMCPRegistered.
func InstallMCPOp(o *Options, d mcp.ToolDeps) error
```

---

## Implementation spec

All paths are repo-relative to `C:/Users/Quant/Documents/Programming/Projects/qompack`.

### 1. `internal/mcp/jsonrpc.go` (new)

Wire types and codec for JSON-RPC 2.0 over newline-delimited stdio.

```go
type rpcRequest struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"`     // absent ⇒ notification
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}
type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}
type rpcResponse struct {
    JSONRPC string          `json:"jsonrpc"`          // always "2.0"
    ID      json.RawMessage `json:"id"`
    Result  any             `json:"result,omitempty"`
    Error   *rpcError       `json:"error,omitempty"`
}

const (
    codeParseError     = -32700
    codeInvalidRequest = -32600
    codeMethodNotFound = -32601
    codeInvalidParams  = -32602
    codeInternalError  = -32603
)
```

Error mapping, exhaustive:

| Condition | Code | Server continues |
|---|---|---|
| line is not valid JSON | `-32700` with `ID: null` | yes |
| `jsonrpc != "2.0"` or `method == ""` | `-32600` | yes |
| line length > `ServerOptions.MaxLine` (set by `cmd_mcp` from `cfg.Runtime.HotPath.MaxPayloadBytes`) | `-32600`, payload discarded, `Loud` once per process | yes |
| method not in the five supported | `-32601`, `data: {"method": <m>}` | yes |
| `initialize`/`tools/call` params not an object | `-32602` | yes |
| handler returns a non-nil `error` | **not** an RPC error — becomes `result.isError = true` (MCP semantics) | yes |
| handler panics | recovered, same as above, `Loud` | yes |

### 2. `internal/mcp/server.go` (new)

```go
type server struct {
    name, version string
    log           logging.Logger
    maxLine       int
    tools         []Tool
    byName        map[string]*Tool
    initialized   atomic.Bool
    onInitialize  func(Observable)     // from ServerOptions; nil-tolerant
    wmu           sync.Mutex
    enc           *json.Encoder
}
func NewServerWithOptions(o ServerOptions) Server   // MaxLine == 0 ⇒ defaultMaxLine (1 MiB, the
                                                    // ipc framing limit); callers pass
                                                    // cfg.Runtime.HotPath.MaxPayloadBytes
func NewServer(name, version string, log logging.Logger) Server // = NewServerWithOptions(
                                                    // ServerOptions{Name: name, Version: version, Log: log})
func (s *server) Register(t Tool) error   // error on empty Name, nil Handler, duplicate Name,
                                          // or InputSchema that fails schema.Compile
func (s *server) Tools() []Tool           // registration order == ToolDefs order
func (s *server) Serve(ctx context.Context, in io.Reader, out io.Writer) error
```

`Serve` loop, precisely:

1. `br := bufio.NewReaderSize(in, 64<<10)`; `s.enc = json.NewEncoder(out)`; `s.enc.SetEscapeHTML(false)`.
2. Read one line with an explicit cap (`io.LimitReader` over the remainder when a line exceeds `maxLine`, discarding to the next `\n`).
3. Empty/whitespace-only lines are skipped silently.
4. Decode into `rpcRequest`; on error emit `-32700`.
5. If `ID` is absent → notification: handle `notifications/initialized` (set `initialized`, no output) and any other `notifications/*` (ignore, no output). **Never write a response for a notification.**
6. Otherwise dispatch by method; write exactly one response line.
7. `ctx.Done()` or `io.EOF` → return nil. Any write error → return that error.
8. The whole per-line body runs inside `func() { defer recover-and-log }()` so a malformed payload can never kill the server.

Method handlers:

- **`initialize`** — params `{"protocolVersion": string, "capabilities": {...}, "clientInfo": {"name": string, "version": string}}`. Result:

```json
{"protocolVersion":"2025-06-18",
 "capabilities":{"tools":{"listChanged":false}},
 "serverInfo":{"name":"qompack","version":"<build version>"},
 "instructions":"Qompack retrieval. The transcript is durably stored and addressable: use recall to find hashes, expand to re-materialize a cleared tool result, re_read for a current or historical file version, timeline for what happened between two points, why for a decision's rationale, and dropped for what is currently out of context. Before committing to an approach, call already_tried. Record eliminations with record_eliminated so they survive compaction. Retrieval results are ephemeral and are evicted first — retrieve again rather than hoarding, and prefer the default minimal span over full=true."}
```
  `protocolVersion` echo rule: `supported = ["2025-06-18","2025-03-26","2024-11-05"]`; if the client's value is in `supported`, echo it; otherwise return `supported[0]`. After building the result, call `s.onInitialize(Observable{...})` (best-effort; a returned error is logged at `Warn`, never propagated) and set `s.initialized`.

- **`notifications/initialized`** — no response.

- **`tools/list`** — result `{"tools":[{"name","title","description","inputSchema"}...]}`, in `ToolDefs` order, `nextCursor` omitted. A `cursor` param is accepted and ignored.

- **`tools/call`** — params `{"name": string, "arguments": object}`. Missing `name` → `-32602`. Unknown `name` → result with `isError:true` and text `` `unknown tool "<name>"; call tools/list for the eight available tools` ``. Otherwise build `mcp.Request{Session, Name, Args, Deadline: now+callTimeout, Turn}` and call `Dispatch`. Result envelope:

```json
{"content":[{"type":"text","text":"…"}],
 "isError":false,
 "_meta":{"qompack":{"ephemeral":true,"tool_use_id":"qompack-mcp:a3f2c19b04de",
                     "hash":"sha256:a3f2…","span":[0,16384],"total_bytes":204800,
                     "truncated":true,"next_span":"16384:16384","expansions":2,
                     "promoted":false,"source":"store","elapsed_ms":31}}}
```
  `_meta.qompack` carries exactly the keys the handler set in `Response.Meta`, plus `ephemeral` (always present when `retrieval.ephemeralResults` is true) and `elapsed_ms` (always). Keys are emitted in sorted order so goldens are stable.

- **`ping`** — result `{}`.

`callTimeout = 5 * time.Second` (package const; `5` is not in the `nomagic` literal set).

### 3. `internal/mcp/schema.go` (new) — the in-repo JSON Schema subset

A ~180-line validator covering exactly what the eight schemas use: `type` (`object|string|integer|boolean|array`), `properties`, `required`, `additionalProperties:false`, `enum`, `minimum`, `maximum`, `default`, `description`, `items`.

```go
type Schema struct { /* parsed form */ }
func Compile(raw json.RawMessage) (*Schema, error)
func (s *Schema) Validate(doc json.RawMessage) []Violation
type Violation struct{ Pointer, Message string }  // Pointer is RFC-6901, e.g. "/k"
func (s *Schema) ApplyDefaults(doc json.RawMessage) (json.RawMessage, error)
```

`Validate` messages are fixed strings so tests can assert them exactly:
`required property missing`, `unknown property`, `expected <type>`, `value not in enum`, `below minimum`, `above maximum`.

`ApplyDefaults` fills `k=5`, `full=false`, `scope=<cfg.Eliminations.DefaultScope>` (the schema's literal default is `"session"`, matching Appendix C; the handler overrides from config when the caller omitted it).

### 4. `internal/mcp/tools.go` (new) — the eight definitions

`ToolDefs(d ToolDeps) []Tool` returns, in this exact order: `recall`, `expand`, `re_read`, `already_tried`, `record_eliminated`, `timeline`, `why`, `dropped`.

`Ephemeral` flags: `true` for all except `record_eliminated` (`false` — it is a write whose acknowledgement is a durable fact, not retrieved content).

Input schemas, byte-for-byte as emitted (compact, keys in the order shown):

```json
recall            {"type":"object","properties":{"query":{"type":"string","description":"Free text, or prefixed selectors combined with spaces: path:<glob>, symbol:<name>, tool:<ToolName>."},"k":{"type":"integer","minimum":1,"maximum":50,"default":5,"description":"Maximum number of hits."}},"required":["query"],"additionalProperties":false}
expand            {"type":"object","properties":{"hash":{"type":"string","description":"Root or chunk hash as sha256:<64 hex>. Provide exactly one of hash or tool_use_id."},"tool_use_id":{"type":"string","description":"tool_use_id taken from a tombstone or a recall hit."},"full":{"type":"boolean","default":false,"description":"Return the whole object instead of the minimum sufficient span."},"span":{"type":"string","description":"Explicit span: \"<off>:<len>\" in bytes, or \"L<start>-L<end>\" in lines."}},"required":[],"additionalProperties":false}
re_read           {"type":"object","properties":{"path":{"type":"string","description":"Project-relative path. A :<symbol> or :<line> suffix anchors the minimal span."},"at":{"type":"string","description":"Empty for the working-tree version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>."},"full":{"type":"boolean","default":false,"description":"Return the whole file instead of the minimum sufficient span."}},"required":["path"],"additionalProperties":false}
already_tried     {"type":"object","properties":{"target":{"type":"string","description":"File path, optionally :symbol — e.g. src/auth.ts:refreshToken."},"approach":{"type":"string","description":"The approach as one short verb phrase — e.g. widen pool timeout."}},"required":["target","approach"],"additionalProperties":false}
record_eliminated {"type":"object","properties":{"target":{"type":"string"},"approach":{"type":"string"},"reason":{"type":"string","description":"Why it does not work. Encode what a competent engineer with no session history would get wrong."},"scope":{"type":"string","enum":["session","project"],"default":"session"},"depends_on":{"type":"array","items":{"type":"string"},"description":"Project-relative paths whose contents this reason rests on; a change to any of them flips this record to stale."}},"required":["target","approach","reason"],"additionalProperties":false}
timeline          {"type":"object","properties":{"from":{"type":"string","description":"Turn index, RFC3339 timestamp, or empty for the session start."},"to":{"type":"string","description":"Turn index, RFC3339 timestamp, or empty for the current frontier."}},"required":[],"additionalProperties":false}
why               {"type":"object","properties":{"decision_id":{"type":"string","description":"A dec_<12 hex> id from a checkpoint or a rehydrated decision list."}},"required":["decision_id"],"additionalProperties":false}
dropped           {"type":"object","properties":{},"required":[],"additionalProperties":false}
```

Descriptions carry the §8.7 policy where the model will read it — `expand` and `re_read` both end with: `Returns the minimum sufficient span by default; pass full=true only when you genuinely need the whole object. Results are ephemeral and are evicted first.` `already_tried`'s description ends with `StandingInstruction`.

`RegisterAll(s Server, d ToolDeps) error` = `for _, t := range ToolDefs(d) { if err := s.Register(t); err != nil { return err } }`.

`RegisterProxy(s Server, fwd Handler) error` registers the same `ToolDefs(ToolDeps{})` values with `Handler` replaced by `fwd` — guaranteeing `tools/list` is byte-identical in the proxy process and in the daemon.

`Dispatch(ctx, s, r)`:

```go
func Dispatch(ctx context.Context, s Server, r Request) (resp Response, err error) {
    t := lookup(s, r.Name)
    if t == nil { return Response{}, ErrToolNotFound }
    defer func() {
        if p := recover(); p != nil {
            // errResponse(msg) == Response{IsError: true, Content: []Content{{Type: "text", Text: msg}}}
            resp = errResponse("internal error in tool " + r.Name)
            err = nil
            // caller logs Loud with the stack
        }
    }()
    return t.Handler(ctx, r)
}
```

### 5. `internal/mcp/span.go` (new) — the minimal-span resolver

This is the algorithmic core. It is a pure function of `(Root, args, cfg, Widener, a byte-reader)`.

```go
type SpanOpts struct {
    Full        bool
    Explicit    string        // "" | "<off>:<len>" | "L<a>-L<b>"
    Path        string        // paths.Key form, "" when unknown
    AnchorSym   string        // from re_read "file.ts:name"
    AnchorLine  int           // from re_read "file.ts:120"; 0 = none
    MaxSpan     int           // cfg.Store.Chunk.Max                (16384)
    MaxResponse int           // cfg.Runtime.MCP.MaxResponseBytes   (262144)
    WidenLines  int           // cfg.Runtime.MCP.SpanWidenLines     (40)
}
type SpanResult struct {
    Off, End   int64
    Total      int64
    Truncated  bool
    NextSpan   string   // "" when End == Total
    Widened    bool
    Body       []byte
}
func ResolveSpan(ctx context.Context, s store.Store, root store.Root,
                 w Widener, o SpanOpts) (SpanResult, error)
```

Offsets live in the **canonical** byte space. `offsets[i]` is the prefix sum of `root.Chunks[j].Len` for `j < i`; `total = offsets[len]`. If `total != root.CanonBytes`, log `Loud` (`"root chunk lengths disagree with CanonBytes"`) and proceed with `total`.

```
chunkStartAtOrBefore(x): largest offsets[i] <= x           (offsets[0]=0)
chunkEndAtOrAfter(x):    smallest offsets[i] >= x, else total
```

Algorithm:

```
1. if o.Full:
       end = min(total, o.MaxResponse)
       return {Off:0, End:end, Total:total, Truncated: end < total,
               NextSpan: spanStr(end, min(o.MaxResponse, total-end))}

2. if o.Explicit matches ^(\d+):(\d+)$  → off0,n
       off  = chunkStartAtOrBefore(clamp(off0, 0, total))
       end  = chunkEndAtOrAfter(clamp(off0+n, off+1, total))
       if end-off > o.MaxResponse { end = off + o.MaxResponse }
       goto 6

3. if o.Explicit matches ^L(\d+)-L(\d+)$ → a,b  (1-based, inclusive)
       probe = read [0, min(total, o.MaxResponse))
       off0  = byte offset of line a in probe (0 if a<=1; total if a > lines)
       end0  = byte offset just past the newline ending line b
       off = chunkStartAtOrBefore(off0); end = chunkEndAtOrAfter(end0)
       cap to o.MaxResponse; goto 6

4. anchor = 0; hardEnd = 0
   // The anchor probe is the one read that cannot be bounded by MaxSpan: a symbol or a line number
   // may legitimately sit anywhere in the object. It is bounded by MaxResponse and abandoned as
   // soon as the anchor is located, so the common case reads far less.
   if o.AnchorSym != "" || o.AnchorLine > 0:
       probe = read [0, min(total, o.MaxResponse))
       if o.AnchorSym != "" && w != nil:
           if a,b,ok := w.Find(o.Path, probe, o.AnchorSym); ok { anchor, hardEnd = a, b }
       else if o.AnchorLine > 0:
           anchor = byte offset of line o.AnchorLine in probe

5. off = chunkStartAtOrBefore(anchor)
   end = off
   for i where offsets[i] == off; i < len(chunks); i++:
       next = end + chunks[i].Len
       if end > off && next-off > int64(o.MaxSpan) { break }     // always ≥ 1 chunk
       end = next
       if end >= total { end = total; break }
   if hardEnd > end && hardEnd-off <= int64(o.MaxResponse) { end = chunkEndAtOrAfter(hardEnd) }

6. // symbol widening at the tail (skipped when o.Path == "" or w == nil or end == total)
   if o.Path != "" && w != nil && end < total:
       probeEnd = min(total, off + int64(2*o.MaxSpan))
       probe    = read [off, probeEnd)
       if _, e2, ok := w.Widen(o.Path, probe, 0, end-off); ok && e2 > end-off:
           extra := probe[end-off : e2]
           if bytes.Count(extra, []byte{'\n'}) <= o.WidenLines && off+e2 <= off+int64(o.MaxResponse):
               end = chunkEndAtOrAfter(off + e2); Widened = true

7. if end-off > int64(o.MaxResponse) { end = off + int64(o.MaxResponse); Truncated = true }
   Body = read [off, end)
   Truncated = Truncated || off > 0 || end < total
   NextSpan  = "" if end >= total else spanStr(end, min(o.MaxSpan, total-end))
```

`spanStr(off, n) = strconv.FormatInt(off,10) + ":" + strconv.FormatInt(n,10)`.

Reads go through `store.OpenSpan(ctx, root.Hash, off, n)` and are fully consumed with `io.ReadAll` under a `io.LimitReader(r, n)`.

**Where the numbers come from (D11, §11.6).** `SpanOpts` is never constructed with a literal. Both call sites (`expand`, `re_read`) fill it as `MaxSpan: cfg.Store.Chunk.Max`, `MaxResponse: cfg.Runtime.MCP.MaxResponseBytes`, `WidenLines: cfg.Runtime.MCP.SpanWidenLines`, and `Full: args.Full || cfg.Retrieval.DefaultSpan == "full"`. `ResolveSpan` itself reads no config — it is a pure function of its arguments, which is what makes it table- and property-testable.

**Normative properties (property-tested):** the returned `[Off,End)` always begins and ends on a chunk boundary or on `total`; `End > Off` for any non-empty root; `End-Off ≤ MaxResponse`; repeatedly following `NextSpan` from offset 0 concatenates to the full object exactly once with no gaps or overlaps.

### 6. `internal/mcp/ephemeral.go` (new)

```go
type ephemeralRec struct {
    ToolUseID core.ToolUseID
    Hash      core.Hash
}
func (h *handlers) recordEphemeral(ctx context.Context, r Request, toolName string,
                                   body []byte, path string) (ephemeralRec, error)
```

1. If `!cfg.Retrieval.EphemeralResults` → return a zero `ephemeralRec`, nil.
2. `pr, err := store.PutBytes(ctx, body, store.PutOptions{Tool: "mcp__qompack__" + toolName, Path: path, Ephemeral: true})`. On error: log `Warn`, return zero rec, nil — **a failure here must never fail the tool call**.
   **`PutOptions.Canon` is deliberately left at its zero value and `internal/mcp` never names `canon.Options`** — §3.2 does not permit `mcp` to import `canon`, and the import-graph check in `verify` would fail if it did. This costs nothing: the bytes being stored are either already-canonical store content being re-materialized (`expand`, and `re_read` off a stored version) or a serialized JSON body this package just produced, neither of which has volatile substrings to strip. The single exception — the worktree file `re_read` reads off disk — is stored as-is, which is correct for an ephemeral record whose only job is to be re-expandable.
3. Synthesize the id, byte-for-byte:
   `id := "qompack-mcp:" + core.HashBytes("qompack.mcp.tooluse.v1", session‖0x00‖toolName‖0x00‖args‖0x00‖decimal(tsMillis)).Short()`
   (`Short()` = first 12 hex chars, per §4.)
4. `store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: r.Session, Turn: r.Turn, TS: nowMillis, Tool: "mcp__qompack__"+toolName, ArgsDigest: core.HashBytes("qompack.mcp.args.v1", r.Args), ArgsPreview: firstN(compact(r.Args), 120), Root: pr.Root.Hash, Path: path, Bytes: int64(len(body)), Tokens: pr.Root.Tokens, Signature: pr.Signature, Ephemeral: true})`. Errors logged at `Warn`, swallowed.
5. `obs.Counter("mcp.ephemeral.recorded").Inc()`.

Every ephemeral tool's `Response.Meta` gets `"tool_use_id"` and `"hash"` from the returned rec so the agent can `expand` its own retrieval output later. `Response.Ephemeral = t.Ephemeral && cfg.Retrieval.EphemeralResults`.

`firstN` truncates to 120 runes — expressed as `argsPreviewRunes` read from a package const annotated `//nomagic:allow ToolUseRecord.ArgsPreview is capped at 120 chars by §5.8`.

### 7. `internal/mcp/promote.go` (new)

```go
type promoter struct {
    mu        sync.Mutex
    path      string
    threshold int
    clk       core.Clock
    st        promState
}
type promState struct {
    Version   int                                `json:"version"`   // 1
    Threshold int                                `json:"threshold"`
    Sessions  map[string]*promSession            `json:"sessions"`
}
type promSession struct {
    Counts   map[string]int `json:"counts"`      // "sha256:<hex>" → expansion count
    Promoted []string       `json:"promoted"`    // promotion order, deduplicated
    Updated  int64          `json:"updated"`
}
func NewPromoter(statePath string, threshold int, clk core.Clock) (Promoter, error)
```

- `statePath` = `<projectRoot>/.qompack/state/promotions.json`. Load on construction; a missing file is an empty state; a corrupt file is quarantined to `.qompack/tmp/quarantine/promotions-<ts>.json`, logged `Loud`, and treated as empty (§12.3 doctrine: fail toward "do nothing").
- `threshold` is `cfg.Retrieval.PromoteAfterExpansions`, validated `≥ 1` by `config.Validate`. If a caller passes `< 1`, `NewPromoter` returns an error.
- `NoteExpansion(ctx, sess, h)`: lock; `c := ++Counts[h.String()]`; if `c == threshold` append `h.String()` to `Promoted`; `promoted = c >= threshold`; `Updated = clk.Now().UnixMilli()`; persist with `paths.WriteAtomic` (marshalled with `json.MarshalIndent(st, "", "  ")` and map keys sorted by `encoding/json`'s own map ordering, which is sorted for `map[string]…`); return `(c, promoted, nil)`. A write error is returned to the caller, which logs `Warn` and continues — counting is advisory.
- `Promoted(ctx, sess)`: returns `[]core.Hash` parsed from `Promoted` in stored order.
- Called by `expand` and `re_read` only (they are the two tools that re-materialize content, per §8.7 "Repeated expansion of the same hash"). `recall` does not count.

### 8. `internal/mcp/handlers_common.go`, `handlers_span.go`, `handlers.go` (new) — the eight handlers

The `handlers` struct and the shared preamble live in `handlers_common.go` (written by the main session in commit 3, before the span handlers need it); `expand` and `re_read` live in `handlers_span.go` (commit 3); the other six live in `handlers.go` (commit 4). Shared preamble:

```go
func (h *handlers) run(name string, fn func(ctx context.Context, r Request, args json.RawMessage) (Response, error)) Handler
```
which: (a) applies `ApplyDefaults`, (b) validates against the tool's compiled schema and on any `Violation` returns `Response{IsError:true, Content:[{Type:"text", Text:"invalid arguments for <name>: <pointer>: <message>"}]}`, (c) starts an `obs` timer on both the `obs.BF` budget histogram and a per-tool `mcp.tool.<name>` histogram, (d) calls `fn`, (e) attaches `_meta` fields.

**Rule for misses, applied uniformly:** a *semantic* miss (unknown hash, unknown decision id, no such path version, empty range) is **not** an error — it returns `IsError:false` with a JSON body carrying `"found": false` and a `"searched"` field naming what was looked at. `IsError:true` is reserved for invalid arguments, backend failures, panics, and timeouts.

**`recall`** — parse `query` for space-separated selector prefixes `path:`, `symbol:`, `tool:`; remaining words are `Query.Text`. `K = args.K` (schema default 5). Call `store.Search(ctx, store.Query{Text, Path, Symbol, Tool, K})`. Body:

```json
{"hits":[{"hash":"sha256:…","tool_use_id":"toolu_01…","path":"src/auth.ts","tool":"FileRead","summary":"…","ts":"2026-08-11T18:04:02Z","score":0.82,"span":[0,4096]}],"count":3,"query":{"text":"pool timeout","path":"","symbol":"","tool":""}}
```
Empty result → `{"hits":[],"count":0,"found":false,…}`. Ephemeral record written over the serialized body.

**`expand`** — exactly one of `hash`/`tool_use_id` must be non-empty, else `IsError:true` with `expand requires exactly one of hash or tool_use_id`. Resolution:
- `tool_use_id` → `store.ToolUse`; `ErrNotFound` → `found:false`. `root = rec.Root`, `path = rec.Path`.
- `hash` → `core.ParseHash`; parse failure → `IsError:true`. `store.GetRoot`; on `ErrNotFound` try `store.GetChunk` (the agent may have pasted a chunk hash) and, if that succeeds, synthesize `store.Root{Hash: h, Chunks: []core.ChunkRef{{Hash: h, Len: len(b)}}, CanonBytes: int64(len(b))}`. Both missing → `found:false`.

Then `ResolveSpan` with `SpanOpts{Full: args.Full || cfg.Retrieval.DefaultSpan == "full", Explicit: args.Span, Path: path}`. Then `promoter.NoteExpansion(ctx, sess, root.Hash)`. Body:

```json
{"found":true,"hash":"sha256:…","path":"src/auth.ts","tool":"FileRead","span":[0,16384],"total_bytes":204800,"truncated":true,"next_span":"16384:16384","widened":false,"expansions":2,"promoted":true,"content":"…"}
```

**`re_read`** — parse `path` first:
- If `path` contains `:` and the segment after the **last** `:` is all digits → `AnchorLine`, base path is the prefix.
- Else if it contains `:` and the last segment matches `^[A-Za-z_][A-Za-z0-9_.$-]*$` → `AnchorSym`, base path is the prefix.
- Else the whole string is the path.
Then `paths.Norm(projectRoot, base)`; an error (escape above root, absolute path outside root) → `IsError:true` with `path escapes the project root`. `key := paths.Key(norm)`.

Version resolution from `at`:
- `""` → if `<projectRoot>/<norm>` exists and is a regular file ≤ `MaxResponse*4`, read it, `store.PutBytes` it with `Ephemeral:true` to obtain a root, `Meta["source"]="worktree"`. If the file does not exist, fall back to the newest `store.FileHistory(key)` entry, `Meta["source"]="store"`. If neither → `found:false`.
- RFC3339 → `store.FileAt(ctx, key, t)`.
- `sha256:<hex>` → `store.GetRoot`.
- `turn:<N>` → newest `FileHistory` entry with `Turn <= N`.
- anything else → `IsError:true` with `at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>`.

Then `ResolveSpan` with the anchors, then `NoteExpansion`. Body mirrors `expand` plus `"path"`, `"at"`, `"source"`, `"turn"`.

**`already_tried`** — `scope := negknow.Scope(cfg.Eliminations.DefaultScope)`; `ans, err := ledger.Query(ctx, args.Target, args.Approach, scope)`. Mapping:

| `ans.State` | `ans.BloomOnly` | `cfg.Eliminations.StaleResponse` | result |
|---|---|---|---|
| `AnswerAbsent` | false | — | `{"state":"absent"}` |
| `AnswerAbsent` | **true** | — | `{"state":"absent","note":"a filter hit was recorded but no backing record exists (possible false positive); treat as not previously tried"}` and `Meta["bloom_only"]=true` |
| `AnswerActive` | false | — | `{"state":"active","reason":rec.Reason,"evidence":rec.Evidence.String(),"scope":rec.Scope,"recorded_at":…,"depends_on":[…]}` |
| `AnswerStale` | false | `"flag"` | `{"state":"stale","reason":rec.Reason,"note":ans.Note,"evidence":…,"stale_because":rec.StaleBecause}` |
| `AnswerStale` | false | `"drop"` | `{"state":"absent"}` |

Two of those rows are deliberate belt-and-braces, and it matters that the implementer knows it rather than discovering it: SP-09's `Ledger.Query` **already** returns `AnswerAbsent` with `BloomOnly:true` when the filter hits but no visible record backs it, and **already** applies `eliminations.staleResponse == "drop"` internally. The MCP mapping restates both so that the tool's contract holds against any conforming `Ledger` (including the in-test fakes) and so that a future ledger change cannot silently turn a stale record into an `active` answer. The two layers must agree; a test asserts each.

A ledger error → `IsError:false` with `{"state":"absent","degraded":true,"reason":"elimination ledger unavailable"}` and a `Loud` log. This is the §12.3 rule verbatim: *"if that fails, `already_tried` returns `absent` for everything — never a false positive."* `ans.Note` is emitted exactly as the ledger produced it (SP-09 owns the §8.3 wording).

**`record_eliminated`** — validation: `target` and `approach` non-empty and ≤ 512 bytes; `reason` non-empty and ≤ 4096 bytes; `scope` (after default) ∈ `{session, project}`. Any violation → `IsError:true` naming the field. One further guard, because the effective scope interacts with the daemon's session resolution (spec §10): if the resolved `r.Session` is empty **and** the effective scope is `session`, return `IsError:true` with `cannot record a session-scoped elimination: no live session — pass scope="project", or retry once a session is active`. Writing it anyway would produce a record that SP-09's `visible(r, scope)` can never return, which is worse than a clear refusal. Then:

1. `ev, err := store.PutBytes(ctx, []byte(reason), store.PutOptions{Tool:"mcp__qompack__record_eliminated"})` → `evidence = ev.Root.Hash`. On error and `cfg.Eliminations.RequireEvidence` → `IsError:true` `cannot record elimination: evidence could not be stored`.
2. `deps := []core.Dep{}`; for each `depends_on` path: `k := paths.Key(norm)`; newest `FileHistory(k)` root; if absent, read the worktree file and `PutBytes` it; if that also fails, append `k` to `unresolved` and skip.
3. `rec := negknow.Record{Session, TS, Target, Approach, Reason, Desc: negknow.Canonicalize(target, approach, reason), Evidence: evidence, DependsOn: deps, Scope, Status: "active", Source: negknow.SourceMCP}`.
4. `id, err := ledger.Record(ctx, rec)`; error → `IsError:true` + `Loud`.
5. Body: `{"id":"…","descriptor":{"normalized_path":"…","symbol":"…","approach_class":"…","reason_hash":"sha256:…"},"scope":"project","evidence":"sha256:…","depends_on":[{"path":"docker-compose.yml","hash":"sha256:…"}],"depends_on_unresolved":[],"status":"active"}`.
6. `Ephemeral: false`. `obs.Counter("mcp.eliminations.recorded").Inc()`.

**`timeline`** — parse each bound: `""` → `from = 0`; `""` → `to = ` the greatest `EndTurn` in `Range(ctx, 0, math.MaxInt32)`, or `math.MaxInt32` when that call errors or the session has no segments. **Not `Frontier`** — SP-06 defines `Frontier` as the end of the last *contiguously encoded* segment, which is `0` in any session that has not checkpointed yet and would make the default range empty. Decimal → `core.TurnIndex`; RFC3339 → scan `Range(0, MaxInt32)` and take the first segment with `StartTS >= t` (for `from`) or the last with `EndTS <= t` (for `to`). Anything else → `IsError:true`. Then `Range(ctx, from, to)`. The body echoes the resolved `frontier` alongside `to` so the agent can see how much of the range a checkpoint already covers. Body:

```json
{"from":12,"to":41,"frontier":19,"segments":[{"id":13,"start_turn":12,"end_turn":19,"start_ts":"…","end_ts":"…","tokens":8412,"closed":true,"encoded_once":true,"checkpoint_seq":7,"features":{"paths":0.31,"tools":0.12}}],"count":3}
```
Every per-segment key is copied straight off `store.Segment`; nothing is derived. In particular there is **no tool-use count**: the store exposes no API that lists tool uses by turn range (`ToolUsesByPath` needs a path, and `Search` with an empty query and `K: 0` returns SP-06's default of five hits, not "all"), and adding one would be a Rule W-3 amendment against SP-06. `tokens` and `encoded_once` carry the same "how much happened here" signal without inventing a query. `frontier` is `SegmentLog.Frontier(session)`, emitted as `0` when nothing is encoded yet. Empty range → `{"segments":[],"count":0,"found":false}`.

**`why`** — `id := args.DecisionID`; reject empty. Search order: `checkpoints.Latest(session)`; if its `Decisions` lack the id, `checkpoints.List()` and walk newest→oldest with `Get`, capped at 32 checkpoints. On hit:

```json
{"found":true,"decision_id":"dec_a3f2c19b04de","what":"…","why":"…","alternatives_rejected":["…"],"evidence":"sha256:…","turn":37,"checkpoint_seq":7,"evidence_bytes":2411,"hint":"call expand with hash=sha256:… to read the evidence"}
```
`evidence_bytes` from `store.GetRoot(evidence).CanonBytes`; omitted when the root is unknown. Miss → `{"found":false,"decision_id":"…","searched_checkpoints":[7,6,5]}`. A nil `Checkpoints` (build without SP-10) → `{"found":false,"available":false,"reason":"checkpoint reader not present in this build"}`.

**`dropped`** — `entries, err := rehydrator.CurrentDrops(ctx, session)`. Body `{"drops":[{"kind":"path_rule","id":"api-conventions.md","detail":"…"}],"count":4}`. Nil reporter → `{"drops":[],"count":0,"available":false,"reason":"rehydrator not present in this build"}`. Error → `IsError:true` + `Loud`.

### 9. `internal/mcp/observable.go` (new)

```go
func WriteInitializedObservable(projectRoot string, o Observable) error
```
Writes `<projectRoot>/.qompack/state/mcp.json` via `paths.WriteAtomic`, `json.MarshalIndent(o, "", "  ")` + `"\n"`. Field order is the struct order given in the Interface contract. Directory is created with `0700` if missing. Together with `state/promotions.json` (spec §7) these are the only two files `internal/mcp` writes; everything else it stores goes through `store.PutBytes`.

**This file is not the contract observable.** SP-05 already fixed that mechanism and SP-13 must use it rather than invent a second one: the `mcp.server_registered` assertion reads `contract.SessionHistory.MCPInitialized` out of `state/history.json` — `contract.HistoryPath(projectRoot)`, which is deliberately **not** `state/contract.json`, the Monitor's own persisted mode/reason/results — and `contract.DeclareProducers` declares the producer only when `daemon.Services.MCPInitialized != nil`. The assertion's declared severity is **`SevInfo`** (`gated(CMCPRegistered, SevInfo, …)` in SP-01's `StandardAssertions`, pinned by `standard_test.go`), so a missing MCP handshake is surfaced and can never degrade a session on its own — which is why the write below is allowed to fail softly, and why SP-13 must not "strengthen" it. `mcp.json` is the *human-readable* record — what protocol version was negotiated, by which client, at what time, in which process — which `/qompack:status` and `test/e2e` read, and which is also what a user is told to look at in a "the tools aren't showing up" triage. The two are written from the same place (spec §10, `kind == "initialized"`), so they cannot disagree.

### 10. `internal/daemon/mcpop.go` (new file, package `daemon`, SP-13-owned)

```go
type mcpOpRequest struct {
    Kind    string          `json:"kind"`            // "call" | "initialized"
    Name    string          `json:"name,omitempty"`
    Args    json.RawMessage `json:"args,omitempty"`
    Turn    core.TurnIndex  `json:"turn,omitempty"`
    Observ  *mcp.Observable `json:"observable,omitempty"`
}
type mcpOpResponse struct {
    Content   []mcp.Content  `json:"content"`
    IsError   bool           `json:"is_error"`
    Ephemeral bool           `json:"ephemeral"`
    Meta      map[string]any `json:"meta,omitempty"`
}
func InstallMCPOp(o *Options, d mcp.ToolDeps) error
```

`InstallMCPOp` does exactly three things, in this order, and all three must happen **before** `daemon.New(o)` — `New` registers its own fallback `mcp` route only for ops that are not already registered, and calls `DeclareProducers(svc)` after applying every `Bind`:

1. builds an in-process `mcp.Server` (`mcp.NewServerWithOptions(mcp.ServerOptions{Name: "qompack", Version: version, Log: o.Log, MaxLine: o.Cfg.Runtime.HotPath.MaxPayloadBytes})` + `mcp.RegisterAll(s, d)`) and holds it in a closure alongside an `initialized atomic.Bool`;
2. `o.Handle(ipc.OpMCP, handler)`;
3. `o.Bind(func(s *daemon.Services) { s.MCPInitialized = func(context.Context) bool { return initialized.Load() } })` — the seam's shipped type is `func(ctx context.Context) bool` (`internal/daemon/options.go`), and **this is the line that makes `contract.DeclareProducers` declare `CMCPRegistered`**, so the assertion stops reporting `not-yet-implemented` and starts reporting a real observation.

The handler unmarshals `req.Raw` into `mcpOpRequest`:

- `kind == "initialized"` → `initialized.Store(true)`; write the contract observable read-modify-write style, `p := contract.HistoryPath(o.ProjectRoot); h := contract.LoadHistory(p); if !h.MCPInitialized { h.MCPInitialized = true; _ = contract.SaveHistory(p, h) }` — `LoadHistory` returns a `*contract.SessionHistory` and `SaveHistory` takes that pointer, so nothing is copied by value, and the path is `HistoryPath`'s `state/history.json`, never `state/contract.json` (writing a `SessionHistory` over the Monitor's own file is the corruption `internal/contract/history.go` warns about, and the assertion reads `HistoryPath` anyway, so a write to the wrong file would leave `mcp.server_registered` observing `initialize-not-received` for ever); then return `ipc.Response{OK:true}`. A `SaveHistory` error is logged at `Warn` and swallowed — a failed observable must never fail a retrieval session.
- `kind == "call"` → resolve session and turn (below), build `mcp.Request`, call `mcp.Dispatch`, marshal `mcpOpResponse` into `ipc.Response.Data`. `ErrToolNotFound` → `OK:true` with an `IsError` payload, never `ipc.Response{OK:false}` (the transport is healthy; the tool name was not).

**Session resolution — the MCP process has no session identity.** Claude Code launches a stdio MCP server once per client, hands it no `session_id`, and `initialize` carries only `clientInfo`. `ipc.Request.Session` therefore arrives empty and the *daemon* resolves it, which is the only place that can: take `RegistryFrom(ctx).Snapshot()`, prefer the entry with `Live == true` and the greatest `LastActivityTS`; if none is live, take the greatest `LastActivityTS` overall; if the registry is empty, use `core.SessionID("")`. The resolved id goes into `mcp.Request.Session`. Handlers degrade cleanly on the empty id: the ephemeral `ToolUseRecord` is written with an empty `Session` (it is still addressable by hash and `tool_use_id`), `timeline` falls back to `math.MaxInt32` for an empty `to`, and `dropped` returns `{"drops":[],"count":0,"available":false,"reason":"no live session"}`.

**Turn resolution — the registry does not carry one.** SP-05's `SessionState` has no turn index and SP-13 may not add one (Rule W-3, and `internal/daemon`'s registry is SP-05's). When `mcpOpRequest.Turn == 0`, the handler calls `svc.Store.Segments().Current(ctx, session)` and uses `max(seg.StartTurn, seg.EndTurn)` — SP-06 initialises an open segment's `EndTurn` to its `StartTurn`, so this is a defined, monotone lower bound on "where we are now". On any error, `Turn` stays `0`. This is honest under-approximation, not a guess: an ephemeral record's turn is used only for ordering, and ordering by segment start is correct.

Panic isolation is doubled: `Dispatch` recovers inside the handler, and the daemon's own op dispatcher recovers around it (SP-05).

### 11. `internal/cli/cmd_mcp.go` (replaces SP-01's stub) and `internal/cli/mcpwire.go` (new)

`cmd_mcp.go` implements `qompack mcp`:

1. Resolve project root (`QOMPACK_PROJECT_ROOT` → payload cwd → nearest `.git` → cwd, via `paths`).
2. `config.Load`. Open a logger into `.qompack/logs/`. **Nothing may be written to stdout except JSON-RPC lines** — the logger goes to the log file only; a test asserts this.
3. `addr, _ := ipc.Resolve(root)`; build the client with SP-05's lazy-spawn seam rather than a hand-rolled one:

```go
self, _ := os.Executable()
client := ipc.NewClientWithOptions(addr, nopSpool{}, log, metrics, ipc.ClientOptions{
    ProjectRoot: root, State: ipc.ReadState(root, cfg), Self: self,
    Spawn: daemon.SpawnDetached, Clock: clk,
})
```
   `nopSpool` (declared in `mcpwire.go`) implements `ipc.SpoolWriter` with `Append` returning nil and `Path` returning `""`. **MCP requests must never be spooled**: a spooled `expand` replayed minutes later on the daemon's idle drain would write a spurious ephemeral record for a result nobody can receive, and a spooled `tools/call` has no caller left to answer. The MCP transport is request/response or it is nothing; the failure path is the tool error in step 5, not a queue.
4. `s := mcp.NewServerWithOptions(mcp.ServerOptions{Name: "qompack", Version: buildVersion, Log: log, MaxLine: cfg.Runtime.HotPath.MaxPayloadBytes, OnInitialize: onInit})`, then `mcp.RegisterProxy(s, forward)`. `onInit` is the closure of step 6; passing it through `ServerOptions` is why SP-13 adds that constructor instead of a method on the §5.16 `Server` interface.
5. `forward` marshals `mcpOpRequest{Kind:"call", Name:r.Name, Args:r.Args}` and sends `ipc.Request{Op: ipc.OpMCP, Reply:true, Raw:…}` with deadline `callTimeout`. `Session` is left empty — the daemon resolves it (spec §10). On `!resp.OK` or a transport failure the first `Send` has already triggered SP-05's `lazySpawn` (that is what `ClientOptions.Self`/`Spawn` are for), so `forward` waits for the listener with a bounded, context-cancellable retry — `for i := 0; i < 10; i++ { select { case <-ctx.Done(): return …; case <-time.After(retryDelay): } ; if resp, err := client.Send(…); err == nil && resp.OK { return … } }` with `retryDelay = 150 * time.Millisecond` (a package const; `time.After` rather than `time.Sleep`, so the `devtool lint` ban is satisfied on its merits — a long-lived server must stay cancellable). After the last attempt return `Response{IsError:true, Content:[{Type:"text", Text:"qompack daemon unavailable; retrieval is temporarily offline — the store is intact and the same call will succeed once the daemon is running"}]}`.
6. `onInit := func(o mcp.Observable) { _ = mcp.WriteInitializedObservable(root, o); _, _ = client.Send(ctx, ipc.Request{Op: ipc.OpMCP, Reply:false, Raw: marshal(mcpOpRequest{Kind:"initialized", Observ:&o})}, ackDeadline) }` — declared before step 4 so it can be passed in `ServerOptions`.
7. `s.Serve(ctx, os.Stdin, os.Stdout)`; exit 0 on `io.EOF` or SIGINT/SIGTERM. `qompack mcp` is a long-lived server, not a hook: it exits 0 on clean shutdown and non-zero only on a failure to bind stdio.

`mcpwire.go` holds the composition-root glue that `internal/mcp` may not import:

```go
type symbolWidener struct{ ex symbols.Extractor }
func (w symbolWidener) Widen(path string, b []byte, off, end int64) (int64, int64, bool) {
    if end <= 0 || int(end) > len(b) { return off, end, false }
    s, ok := w.ex.Enclosing(path, b, int(end)-1)
    if !ok { return off, end, false }
    e := int64(s.Offset + s.Len)
    if e <= end { return off, end, false }
    return off, e, true
}
func (w symbolWidener) Find(path string, b []byte, name string) (int64, int64, bool) {
    for _, s := range w.ex.Extract(path, b) {
        if s.Name == name { return int64(s.Offset), int64(s.Offset + s.Len), true }
    }
    return 0, 0, false
}
func NewToolDeps(root string, cfg config.Config, st store.Store, l negknow.Ledger,
                 cr checkpoint.Reader, dr mcp.DropReporter, p mcp.Promoter,
                 ex symbols.Extractor, log logging.Logger, m obs.Registry,
                 clk core.Clock) mcp.ToolDeps   // ex == nil ⇒ ToolDeps.Widener stays nil

type nopSpool struct{}                               // step 3: MCP calls are never spooled
func (nopSpool) Append(ipc.Request) error { return nil }
func (nopSpool) Path() string             { return "" }
```

**The daemon bootstrap, and what SP-13 actually has to add to it.** `internal/cli/daemon.go` is SP-05's `qompack daemon` subcommand and the composition root that builds `daemon.Options`, but on `develop` it opens **no store, no ledger and no graph**: between `LoadConfigAndReport` and `daemon.New` it does exactly `opts := daemon.NewOptions(root, cfg)` plus `opts.Log`, `opts.Metrics` and `opts.Clock`, and `NewOptions` fills only `ProjectRoot`, `Cfg`, `Log`, `Metrics`, `Clock` and `Sketches`. So `st`, `ledger`, `syms` and `promoter` do not exist at that point and there is no one line to add. **SP-13 owns opening them**, in `internal/cli/daemon.go`, after the config load and **before `daemon.New(opts)`** — this whole block, not a line:

```go
syms := symbols.New()
st, err := store.Open(root, cfg, store.Deps{Symbols: syms, Log: log, Metrics: reg, Clock: clk})
if err != nil {
    log.Loud("daemon: store unavailable; retrieval and L3 are disabled for this daemon", "err", err.Error())
} else {
    defer func() { _ = st.Close() }()
    opts.Store = st
}
if g, err := dag.Open(root, cfg, log); err != nil {
    log.Loud("daemon: dag unavailable", "err", err.Error())
} else {
    opts.Graph = g
}
if opts.Store != nil {
    ledger, err := negknow.Open(root, cfg, opts.Sketches.Tried, negknow.Deps{
        Store: opts.Store, Graph: opts.Graph, Log: log, Metrics: reg, Clock: clk,
    })
    if err != nil {
        log.Loud("daemon: elimination ledger unavailable", "err", err.Error())
    } else {
        defer func() { _ = ledger.Close() }()
        opts.Ledger = ledger
    }
}
promoter, err := mcp.NewPromoter(filepath.Join(paths.Of(root).State, "promotions.json"),
    cfg.Retrieval.PromoteAfterExpansions, clk)
if err != nil {
    log.Loud("mcp: promoter unavailable; expansion counting is off", "err", err.Error())
}
if err := daemon.InstallMCPOp(&opts, NewToolDeps(root, cfg, opts.Store, opts.Ledger,
    ckptReader, dropReporter, promoter, syms, log, reg, clk)); err != nil {
    log.Loud("mcp op registration failed", "err", err.Error())
}
```

Three properties of that block are load-bearing and a reviewer must check each. (a) **Every failure degrades, never exits** — `runDaemon`'s whole contract is that it returns nil however badly things go (a hook's `lazySpawn` has already exited 0), so each constructor logs `Loud` and leaves its `Options` field nil; every MCP handler already tolerates a nil `Store`/`Ledger` and says `available:false`. (b) **`InstallMCPOp` is last**, because it must run after `opts.Store`/`opts.Graph`/`opts.Ledger` are assigned and before `daemon.New(opts)` — `New` seeds `Services` from those fields, registers its own fallback `mcp` route only for ops not already registered, and calls `DeclareProducers` after applying every `Bind`. (c) **This wiring is shared, and SP-13 is its single owner.** SP-12's L3 bootstrap reads the same `opts.Store`, `opts.Graph` and `opts.Ledger` for `daemon.SchedulerRuntimeOptions`, and SP-12's plan is corrected to say so rather than to open a second store: until this block lands, `NewSchedulerRuntime` reports its missing deps by name and L3 stays disabled. SP-13 merges last in wave 3 (§14), so the wave closes with one store, one ledger and one graph in the daemon process — if a reviewer finds a second `store.Open` in `internal/cli`, that is the defect.

`ckptReader` and `dropReporter` are **not** `daemon.Services` members — `Services` carries a `checkpoint.Writer` and a `Rehydrate` function, neither of which is what `why` and `dropped` need. They are constructed here, in the composition root, from the wave-3 siblings' own constructors:

| Value | At SP-13 merge time (commits 1–7) | After the wave-3 merge (commit 7's rebase step) |
|---|---|---|
| `ckptReader checkpoint.Reader` | typed `nil` | `checkpoint.OpenReader(root, log, metrics)` (SP-10); an error logs `Loud` and leaves it nil |
| `dropReporter mcp.DropReporter` | typed `nil` | `rehydrate.NewReporter(root, log)` (SP-11), which structurally satisfies `mcp.DropReporter` |

Every handler tolerates nil for both and says so in its response (`available:false`), which is exactly what makes commits 1–6 compile and pass on a `develop` that does not yet contain SP-10 or SP-11 (spec §8).

### 12. `tools/devtool/genmcpdocs.go` (new) and `docs/mcp-tools.md` (generated)

`devtool gen-mcp-docs` renders `docs/mcp-tools.md` from `mcp.ToolDefs(mcp.ToolDeps{})`: one `##` section per tool with its purpose sentence from §8.7, the input schema in a fenced `json` block, the response body shape, whether results are ephemeral, and the span rules for `expand`/`re_read`. One line is added to the devtool task table:

```go
"gen-mcp-docs": genMCPDocs,
```

CI's `docs` job runs it and `git diff --exit-code`. CI's `plugin-validate` job asserts `len(mcp.ToolNames()) == 8` and that the eight names match the §8.7 table exactly.

### Performance budget

| Budget | Clock | Target | How measured |
|---|---|---|---|
| **B-F** | `mcp_tool_call` — `Dispatch` entry → `Response` returned | **p95 < 250 ms** at `defaultSpan: "minimal"` | `TestBudgetBF` in `internal/mcp`, 200 calls over the 2 000-tool-use / 40 MB fixture, `obs.Histogram` percentiles |
| B-F (e2e) | full stdio round trip through the real binary + daemon | reported, not gated (it inherits B-D process cost) | `test/e2e/mcp_e2e_test.go`, posted as a bench artifact |

**Metric plumbing (SP-01 already owns it — do not invent a parallel one).** SP-01 ships `obs.BF BudgetID = "B-F" // mcp_tool_call: request → response`, gated on the **p95** of the histogram it names, with the limit read from `runtime.budgets.mcpToolCallMs` (default 250). The `run` preamble therefore observes into *that* histogram, and it must reach the name the way the budget table itself does, because `internal/obs` exports **no** `MetricFor`-style lookup and the histogram name is an unexported constant: the only exported accessor is `func Budgets() []Budget`, so the preamble resolves the row once at construction —

```go
// resolved once, in the handlers constructor; ok == false only if a build's table lost B-F,
// in which case the per-tool histogram is still recorded and the B-F one is skipped.
func bfHistName() (string, bool) {
    for _, b := range obs.Budgets() {
        if b.ID == obs.BF { return b.Hist, true }
    }
    return "", false
}
…
err := obs.Timed(h.metrics.Hist(h.bfHist), func() error { resp, err := fn(ctx, r, args); out = resp; return err })
```

— and additionally observes a per-tool `mcp.tool.<name>` histogram for `/qompack:status` breakdowns. **SP-13 adds no export to `internal/obs`**: the done checklist forbids editing outside SP-13's enumerated files, and `Budgets()` already answers the question. `TestBudgetBF` asserts against `cfg.Runtime.Budgets.MCPToolCallMs` converted to a `time.Duration`, never against a literal, so a budget change in `config.Defaults()` moves the gate rather than silently disagreeing with it. `Registry.CheckBudgets(cfg)` consequently reports a `BudgetBreach{Budget:"B-F"}` in production too, not only in the test.

**Bounded I/O — the honest worst case.** A single `expand`/`re_read` call reads at most: one anchor probe of `MaxResponse` (only when a symbol or line anchor was given), one widening probe of `2 × MaxSpan`, and the body of at most `MaxResponse` — `262144 + 32768 + 262144 = 557 056` bytes, and in the common anchorless minimal-span case just `16384 + 32768 = 49 152`. Nothing scales with object size; that bound, not the 250 ms itself, is what makes B-F achievable on a 40 MB store.

---

## Test plan (TDD)

Every test below is written and run (failing) before the code in its commit. `testify/require` only; `FakeClock` everywhere time appears; no `time.Sleep` outside the annotated daemon-retry helper.

### Fixtures

| Fixture | Content |
|---|---|
| `testdata/golden/mcp/tools-list.json` | the exact `tools/list` result for the eight tools |
| `testdata/golden/mcp/schemas/{recall,expand,re_read,already_tried,record_eliminated,timeline,why,dropped}.json` | the eight input schemas |
| `testdata/golden/mcp/initialize.json` | the exact `initialize` result at protocol `2025-06-18` |
| `testdata/corpora/mcp/` | fuzz seed corpus for `FuzzServeLine`: valid `initialize`, a notification, a truncated object, a 2 MiB line, embedded NUL, `{"jsonrpc":"1.0"}`; plus `initialize.ndjson`, a three-line hand-drive script (`initialize`, `notifications/initialized`, `tools/list`) used by the commit-6 smoke check |
| `testdata/golden/contracts/checkpoint/{full,minimal,empty}.json` (SP-01-generated, the same three files SP-11 reads) | drive `why` under Rule W-2. `full.json` has all tiers populated — 9 decisions, 12 file pointers, 23 eliminations — and its `dropped[]` array is also what seeds the `dropped` fake. Accessed through `testutil.ContractFixture(t, "checkpoint", …)`, which `t.Skip`s with `contract fixture not yet recorded (Rule W-2)` if a fixture is not yet frozen. **No test on this branch constructs a checkpoint by calling `checkpoint.Writer`.** |
| `internal/mcp/fake_test.go` | `fakeReader` (a `checkpoint.Reader` over the three fixtures, `Latest` → `full.json` at seq 7, `Get(5)` → `minimal.json`) and `fakeReporter` (a `mcp.DropReporter` returning `full.json`'s `dropped[]`). There is no `contracts/rehydrate/` fixture and SP-13 does not invent one: `DropReporter` is a two-line interface declared in `internal/mcp` itself, so a fake plus the V4 re-verification against `rehydrate.NewReporter` is the whole of Rule W-2 for it. |
| `internal/mcp/fixture_test.go` | `newFixture(t)` → temp project (`testutil.NewProject`), real store, real ledger, 40 seeded tool uses over 6 files including `src/auth.ts` with three functions and a 200 KB `bash` output |
| `internal/mcp/benchfixture_test.go` | `newBigFixture(t)` → 2 000 tool uses, 40 MB raw, built once per package via `sync.Once` |

### JSON-RPC server (commit 1)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestInitializeEchoesSupportedProtocolVersion` | server with zero tools | `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"claude-code","version":"2.1"}}}` | result `protocolVersion == "2025-03-26"`, `serverInfo.name == "qompack"` |
| `TestInitializeFallsBackToPreferredVersion` | same | `"protocolVersion":"1999-01-01"` | result `protocolVersion == "2025-06-18"` |
| `TestInitializeResultMatchesGolden` | server, version `"0.1.0"` | protocol `2025-06-18` | equals `testdata/golden/mcp/initialize.json` |
| `TestInitializeInstructionsCarryStandingInstruction` | same | — | `strings.Contains(instructions, mcp.StandingInstruction)` |
| `TestNotificationsInitializedProducesNoResponse` | server | `{"jsonrpc":"2.0","method":"notifications/initialized"}` | output buffer is empty; a following `ping` still answers |
| `TestNotificationWithUnknownMethodIgnored` | server | `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}` | output empty, no error |
| `TestPingReturnsEmptyObject` | server | `{"jsonrpc":"2.0","id":"p","method":"ping"}` | `{"jsonrpc":"2.0","id":"p","result":{}}` |
| `TestUnknownMethodReturnsMethodNotFound` | server | `method":"tools/subscribe"` | `error.code == -32601`, `error.data.method == "tools/subscribe"` |
| `TestMalformedJSONReturnsParseErrorAndKeepsServing` | server | line `{"jsonrpc":` then a valid `ping` | first response `-32700` with `"id":null`; second response is the ping result |
| `TestWrongJSONRPCVersionRejected` | server | `{"jsonrpc":"1.0","id":1,"method":"ping"}` | `-32600` |
| `TestOversizedLineRejectedAndStreamResynchronizes` | `maxLine = 1024` | a 4 KB line, then a valid `ping` | `-32600` for the first; ping answered normally |
| `TestNoStdoutPollution` | server with a tool whose handler writes to the logger | one `tools/call` | every line on `out` unmarshals into `rpcResponse`; nothing else present |
| `TestConcurrentCallsProduceWellFormedLines` | server, 8 goroutines feeding a pipe, `-race` | 200 interleaved `ping`s | 200 well-formed response lines, ids all distinct, no interleaved bytes |
| `TestHandlerPanicIsolated` | tool whose handler panics with `"boom"` | `tools/call` | result `isError:true`, text `internal error in tool panicky`; a subsequent `ping` succeeds |
| `TestServeReturnsNilOnEOF` / `TestServeReturnsOnContextCancel` | — | — | `Serve` returns `nil` |
| `FuzzServeLine` | server, seed corpus above | arbitrary bytes | never panics; `Serve` either answers or skips; process exits cleanly |

### Schema validator (commit 2)

| Test | Input | Expected |
|---|---|---|
| `TestSchemaAcceptsValidArgs` | `{"query":"pool","k":3}` against `recall` | zero violations |
| `TestSchemaRejectsUnknownProperty` | `{"query":"x","kk":1}` | one violation, `Pointer:"/kk"`, `Message:"unknown property"` |
| `TestSchemaRejectsMissingRequired` | `{}` against `why` | `Pointer:"/decision_id"`, `"required property missing"` |
| `TestSchemaRejectsWrongType` | `{"query":"x","k":"5"}` | `Pointer:"/k"`, `"expected integer"` |
| `TestSchemaRejectsEnumViolation` | `{"target":"a","approach":"b","reason":"c","scope":"global"}` | `Pointer:"/scope"`, `"value not in enum"` |
| `TestSchemaRejectsBelowMinimum` | `{"query":"x","k":0}` | `Pointer:"/k"`, `"below minimum"` |
| `TestSchemaRejectsAboveMaximum` | `{"query":"x","k":51}` | `Pointer:"/k"`, `"above maximum"` |
| `TestApplyDefaultsFillsK` | `{"query":"x"}` | `k == 5` |
| `TestApplyDefaultsFillsFullFalse` | `{"hash":"sha256:…"}` against `expand` | `full == false` |
| `TestAllEightSchemasCompile` | `ToolDefs` | eight compile without error |
| `TestSchemaGoldensStable` | `ToolDefs` | each schema byte-equals its golden file |
| `PropertySchemaAcceptsGeneratedValidDocs` (rapid) | generator emitting docs from a schema's own `properties` | zero violations for 1 000 draws |

### Conformance suite (commit 2)

`TestMCPConformance` calls `mcptest.RunMCPSuite(t, factory)` with every SP-01 `t.Skip` removed. Additional schema-derived cases:

| Test | Expected |
|---|---|
| `TestToolsListReturnsExactlyEightTools` | 8 tools, names equal `["recall","expand","re_read","already_tried","record_eliminated","timeline","why","dropped"]` in that order |
| `TestToolsListMatchesGolden` | result byte-equals `testdata/golden/mcp/tools-list.json` |
| `TestProxyAndDirectToolListsAreIdentical` | `tools/list` from `RegisterProxy` == from `RegisterAll` |
| `TestUnknownToolNameIsToolErrorNotRPCError` | `tools/call` with `name:"nope"` → `result.isError == true`, no `error` member |
| `TestEveryToolRejectsUnknownArgument` | table over eight tools, each with `{"zzz":1}` added | every one → `isError:true`, text starts `invalid arguments for <name>:` |
| `TestDispatchReturnsErrToolNotFound` | `Dispatch` with an unregistered name | `errors.Is(err, mcp.ErrToolNotFound)` |

### Span resolver (commit 3)

Fixture object: `src/auth.ts`, 200 000 canonical bytes, chunked by the real FastCDC into ~50 chunks, containing `refreshToken` spanning bytes 18 200–19 900.

| Test | Input | Expected |
|---|---|---|
| `TestMinimalSpanIsChunkAligned` | default opts | `Off` and `End` both appear in the chunk-offset set (or `End == Total`) |
| `TestMinimalSpanNeverExceedsChunkMax` | default opts | `End-Off ≤ 16384` unless a single chunk is larger |
| `TestSingleChunkObjectReturnsWholeChunk` | 900-byte object | `Off==0`, `End==900`, `Truncated==false`, `NextSpan==""` |
| `TestFullReturnsWholeObjectUntilResponseCap` | `Full:true`, 200 000 bytes | `End == 200000`, `Truncated == false` |
| `TestFullTruncatesAtMaxResponseBytes` | `Full:true` on a 400 000-byte object | `End == 262144`, `Truncated == true`, `NextSpan == "262144:137856"` |
| `TestExplicitByteSpanAlignsOutward` | `Explicit:"18300:100"` | `Off ≤ 18300`, `End ≥ 18400`, both chunk-aligned |
| `TestExplicitLineSpan` | `Explicit:"L10-L20"` | body starts at line 10, ends after line 20's newline, then chunk-aligned outward |
| `TestExplicitSpanBeyondEndClamps` | `Explicit:"999999:100"` | `Off == chunkStart(200000)`, `End == 200000` |
| `TestSymbolAnchorSelectsEnclosingFunction` | `AnchorSym:"refreshToken"` | body contains the full text of `refreshToken` |
| `TestSymbolWideningExtendsToFunctionEnd` | anchor whose chunk-aligned end lands mid-function | `Widened == true`, body ends with the function's closing brace |
| `TestSymbolWideningRefusedBeyondSpanWidenLines` | `WidenLines:2`, symbol needing 30 more lines | `Widened == false`, `End` unchanged |
| `TestNoWidenerIsTolerated` | `w == nil` | no panic, chunk-aligned result |
| `TestLineAnchor` | `AnchorLine:400` | body contains line 400 |
| `PropertyNextSpanPagingCoversObjectExactly` (rapid) | random object sizes 1 B–1 MB | concatenating pages from offset 0 following `NextSpan` reproduces the object byte-for-byte |
| `PropertySpanNeverExceedsMaxResponse` (rapid) | random opts | `End-Off ≤ MaxResponse` always |
| `BenchmarkResolveSpanMinimal` | 40 MB fixture | recorded; no allocation growth with object size |

### Tools (commits 3–4)

| Test | Setup / input | Expected |
|---|---|---|
| `TestRecallReturnsHashesAndSummaries` | fixture, `{"query":"pool timeout"}` | ≥1 hit; each hit has non-empty `hash` matching `^sha256:[0-9a-f]{64}$` and a `summary` |
| `TestRecallDefaultKIsFive` | `{"query":"a"}` on 20 matching results | `count == 5` |
| `TestRecallSelectorPrefixesParsed` | `{"query":"path:src/*.ts symbol:refreshToken tool:FileRead retry"}` | the `store.Query` recorded by a spy store is `{Text:"retry", Path:"src/*.ts", Symbol:"refreshToken", Tool:"FileRead", K:5}` |
| `TestRecallEmptyResultIsNotAnError` | `{"query":"zzzz"}` | `isError:false`, `count:0`, `found:false` |
| `TestExpandByToolUseID` | id of a seeded `FileRead` | `found:true`, content non-empty, `hash` equals the record's root |
| `TestExpandByHash` | root hash | same content as by id |
| `TestExpandAcceptsChunkHash` | a chunk hash from that root | returns exactly that chunk's bytes |
| `TestExpandBothArgsRejected` | `{"hash":"…","tool_use_id":"…"}` | `isError:true`, `expand requires exactly one of hash or tool_use_id` |
| `TestExpandNeitherArgRejected` | `{}` | same message |
| `TestExpandUnknownHashFoundFalse` | 64 hex zeroes | `isError:false`, `found:false` |
| `TestExpandMalformedHashIsError` | `"deadbeef"` | `isError:true` |
| `TestExpandFullEscapeHatch` | `{"tool_use_id":"…","full":true}` | body length == object length |
| `TestExpandSpanPaging` | `{"hash":"…","span":"16384:16384"}` | `span[0] ≤ 16384`, content matches the object slice |
| `TestReReadWorktreeCurrent` | file on disk, no store version | `source:"worktree"`, content matches disk |
| `TestReReadFallsBackToStoreWhenFileDeleted` | file removed from disk, store history present | `source:"store"` |
| `TestReReadAtTimestamp` | two versions at T1 and T2, `at` = T1+1s | content == version 1 |
| `TestReReadAtRootHash` | `at:"sha256:<v1 root>"` | content == version 1 |
| `TestReReadAtTurn` | versions at turns 4 and 9, `at:"turn:7"` | content == the turn-4 version |
| `TestReReadSymbolSuffixAnchorsSpan` | `{"path":"src/auth.ts:refreshToken"}` | body contains the whole function and is < 16384+widen bytes |
| `TestReReadLineSuffixAnchorsSpan` | `{"path":"src/auth.ts:400"}` | body contains line 400 |
| `TestReReadPathEscapeRejected` | `{"path":"../../etc/passwd"}` | `isError:true`, `path escapes the project root` |
| `TestReReadUnknownPathFoundFalse` | `{"path":"nope.ts"}` | `found:false` |
| `TestReReadBadAtIsError` | `{"path":"a.ts","at":"yesterday"}` | `isError:true` with the exact `at must be …` message |
| `TestAlreadyTriedAbsent` | empty ledger | `{"state":"absent"}` |
| `TestAlreadyTriedActiveReturnsReason` | ledger with an active record | `state:"active"`, `reason` equals the record's, `evidence` is the hash string |
| `TestAlreadyTriedStaleReturnsNote` | record flipped stale, `staleResponse:"flag"` | `state:"stale"`, `note` equals `Answer.Note` verbatim, `stale_because` present |
| `TestAlreadyTriedStaleDropReturnsAbsent` | same record, `staleResponse:"drop"` | `state:"absent"` |
| `TestAlreadyTriedBloomOnlyReportedAsAbsent` | ledger returning `BloomOnly:true` | `state:"absent"`, `meta["bloom_only"] == true` |
| `TestAlreadyTriedLedgerFailureReturnsAbsent` | ledger returning an error | `isError:false`, `state:"absent"`, `degraded:true`; one `Loud` recorded |
| `TestAlreadyTriedScopeFromConfig` | `defaultScope:"project"` | spy ledger saw `Scope("project")` |
| `TestRecordEliminatedWritesLedgerRecord` | valid args | ledger has one record with `Source == SourceMCP`, `Status == "active"` |
| `TestRecordEliminatedStoresEvidence` | reason `"pgbouncer 1.18 ignores it in transaction mode"` | `evidence` root resolves in the store to exactly that text |
| `TestRecordEliminatedResolvesDependsOnHashes` | `depends_on:["docker-compose.yml","package-lock.json"]` both in history | two `core.Dep`s with the newest roots |
| `TestRecordEliminatedUnresolvedDependencyReported` | `depends_on:["ghost.yml"]` | record written, `depends_on_unresolved:["ghost.yml"]` |
| `TestRecordEliminatedDefaultsScopeFromConfig` | scope omitted, `defaultScope:"project"` | stored `Scope == "project"` |
| `TestRecordEliminatedRejectsUnknownScope` | `scope:"global"` | `isError:true` (schema enum violation) |
| `TestRecordEliminatedRejectsEmptyReason` | `reason:""` | `isError:true` naming `reason` |
| `TestRecordEliminatedRejectsOversizeFields` | 600-byte target | `isError:true` naming `target` |
| `TestRecordEliminatedRefusesSessionScopeWithoutSession` | `Request.Session == ""`, scope defaulted to `session` | `isError:true` with the exact `cannot record a session-scoped elimination` message; ledger untouched. Same call with `scope:"project"` succeeds |
| `TestRecordEliminatedIsNotEphemeral` | valid call | `Response.Ephemeral == false`, no ephemeral `ToolUseRecord` written |
| `TestTimelineRangeByTurn` | 4 segments over turns 0–41, `{"from":"12","to":"30"}` | segments whose `[StartTurn,EndTurn]` intersect `[12,30]` |
| `TestTimelineEmptyToUsesLastSegmentEnd` | `{"from":"0"}` on a session with no encoded segments | `to` equals the greatest `EndTurn` in the log (**not** `Frontier`, which is `0` here); `frontier:0` is still reported; `count` covers every segment |
| `TestTimelineByTimestamp` | RFC3339 bounds | correct segment subset |
| `TestTimelineBadBoundIsError` | `{"from":"soon"}` | `isError:true` |
| `TestTimelineEmptyRangeFoundFalse` | `{"from":"900","to":"999"}` | `count:0`, `found:false` |
| `TestWhyFindsDecisionInLatestCheckpoint` | `fakeReader` over `contracts/checkpoint/full.json` (seq 7), asking for its first decision id | `found:true`, `what`/`why`/`alternatives_rejected`/`evidence`/`turn` match the fixture byte-for-byte |
| `TestWhySearchesParentChain` | decision present only in `minimal.json` at seq 5, `Latest` is seq 7 | `found:true`, `checkpoint_seq:5`, `searched_checkpoints` includes 7 and 6 |
| `TestWhyNotFoundReturnsFoundFalse` | `dec_ffffffffffff` | `found:false`, `searched_checkpoints` non-empty |
| `TestWhyNilReaderReportsUnavailable` | `Checkpoints: nil` | `available:false`, `isError:false` |
| `TestWhyEmptyIDIsError` | `{"decision_id":""}` | `isError:true` |
| `TestDroppedReturnsDropReport` | `fakeReporter` returning `full.json`'s `dropped[]` | `count` equals the fixture's entry count and every `{kind,id,detail}` round-trips unchanged and in order |
| `TestDroppedNilReporterReportsUnavailable` | `Rehydrator: nil` | `available:false`, `count:0`, `isError:false` |
| `TestDroppedReporterErrorIsToolError` | reporter returning an error | `isError:true`, one `Loud` |

### Ephemeral + Promoter (commit 5)

| Test | Expected |
|---|---|
| `TestEveryRetrievalResponseCarriesEphemeralMeta` | table over the seven ephemeral tools: `_meta.qompack.ephemeral == true` |
| `TestEphemeralToolUseRecordWritten` | after `expand`, `store.ToolUse(meta.tool_use_id).Ephemeral == true` and `Tool == "mcp__qompack__expand"` |
| `TestEphemeralRecordCarriesTurn` | `Request.Turn = 37` → record `Turn == 37` |
| `TestEphemeralDisabledByConfig` | `retrieval.ephemeralResults:false` → no `_meta.qompack.ephemeral`, no ephemeral record |
| `TestEphemeralStoreFailureDoesNotFailTheCall` | store returning an error on `PutBytes` | tool result still `isError:false` with content |
| `TestSyntheticToolUseIDIsDeterministic` | same session/name/args/ts | identical id; a different ts → different id |
| `TestNoteExpansionPromotesAtThreshold` | threshold 2; expand the same hash twice | first `(1,false)`, second `(2,true)` |
| `TestNoteExpansionStaysPromotedAfterThreshold` | third call | `(3,true)`, `Promoted` still length 1 |
| `TestPromotedListIsOrderedAndDeduped` | hashes A,A,B,B,A | `Promoted == [A,B]` |
| `TestPromoterStatePersistsAcrossRestart` | reopen from the same path | counts and promoted list preserved |
| `TestPromoterCorruptStateQuarantined` | garbage in `promotions.json` | empty state, file moved to `tmp/quarantine/`, one `Loud` |
| `TestPromoterThresholdFromConfig` | `promoteAfterExpansions:3` | promotion at the third expansion |
| `TestPromoterRejectsThresholdBelowOne` | `NewPromoter(path, 0, clk)` | error |
| `TestRecallDoesNotCountAsExpansion` | recall twice on the same hash | count stays 0 |
| `TestExpandMetaCarriesExpansionCount` | second expand | `_meta.qompack.expansions == 2`, `promoted == true` |
| `PropertyPromoterCountsMonotone` (rapid) | random call sequences | per-hash counts never decrease; `promoted ⟺ count ≥ threshold` |
| `TestPromotedIsReadableByLaterSubplan` | `Promoted(ctx, sess)` returns `[]core.Hash` parseable by `core.ParseHash` | — |

### Wiring, observable, e2e (commits 6–7)

| Test | Expected |
|---|---|
| `TestWriteInitializedObservable` | `.qompack/state/mcp.json` exists, unmarshals, `Initialized == true`, `Tools == 8` |
| `TestObservableOverwrittenOnSecondInitialize` | second call updates `TS`, file stays valid JSON |
| `TestInstallMCPOpRegistersOp` | spy `Options.Handle` recorded `ipc.OpMCP`, and `Options.Bind` recorded a func that sets `Services.MCPInitialized` to a non-nil value |
| `TestMCPInitializedSeamFlipsAfterInitialize` | `Services.MCPInitialized(ctx)` is `false` before any `kind:"initialized"` and `true` after; `contract.DeclareProducers` with that `Services` declares `CMCPRegistered` (`contract.HasProducer` true) |
| `TestInitializedWritesContractHistory` | after `kind:"initialized"`, `contract.LoadHistory(contract.HistoryPath(root)).MCPInitialized == true` — and `state/contract.json`, if it exists, is untouched; a second call is idempotent and does not rewrite `state/history.json` |
| `TestInitializedSurvivesHistoryWriteFailure` | `state/` made unwritable | handler still returns `OK:true`, one `Warn`, `MCPInitialized(ctx)` still true |
| `TestDaemonMCPOpDispatchesToolCall` | `mcpOpRequest{Kind:"call",Name:"recall",…}` through the handler | `ipc.Response.OK == true`, `Data` unmarshals to `mcpOpResponse` with hits |
| `TestDaemonMCPOpUnknownToolIsPayloadError` | `Name:"nope"` | `OK:true`, `Data.is_error == true` |
| `TestDaemonMCPOpResolvesSessionFromRegistry` | empty `ipc.Request.Session`; registry holds one ended session and one live session with the greater `LastActivityTS` | the ephemeral record's `Session` is the live one |
| `TestDaemonMCPOpEmptyRegistryDegrades` | empty registry | `dropped` returns `available:false`, `reason:"no live session"`; `expand` still succeeds with an empty `Session` on its record |
| `TestDaemonMCPOpResolvesTurnFromCurrentSegment` | `Turn:0`, open segment with `StartTurn == 12` | ephemeral record has `Turn == 12`; with no open segment, `Turn == 0` and no error |
| `TestCmdMCPWritesNothingButJSONRPCToStdout` | run `cmd_mcp` in-process against pipes with a logger set to Debug | every stdout line is a valid `rpcResponse` |
| `TestCmdMCPDaemonUnavailableReturnsToolError` | no daemon, lazy spawn disabled by building the client with `ClientOptions{Spawn: nil}`, `retryDelay` shortened by the test | `isError:true` with the exact "qompack daemon unavailable" text; server keeps serving and a following `ping` answers |
| `TestCmdMCPRetriesUntilListenerAppears` | spy `Spawn` func; fake listener appearing on the third retry | `Spawn` invoked exactly once (SP-05's `lazySpawn` is once-per-process), the call succeeds on the third attempt, and total attempts ≤ 10 |
| `TestCmdMCPNeverSpools` | daemon absent for the whole call | the `ipc.SpoolWriter` handed to the client is `nopSpool`; `.qompack/spool/` gains no file |
| `TestCmdMCPRetryIsCancellable` | `ctx` cancelled mid-retry | `forward` returns promptly with the tool-error response; no goroutine leak under `-race` |
| `TestStandingInstructionsAgree` (`test/e2e`) | `mcp.StandingInstruction == rehydrate.StandingInstruction()` after SP-11 merges; skipped with an explicit reason before | strings equal |
| `TestStdioServerEndToEnd` (`test/e2e`) | build the real binary, start a real daemon on a temp project seeded with 40 tool uses, drive `initialize` → `notifications/initialized` → `tools/list` → `tools/call recall` → `tools/call expand` over stdio | eight tools listed; recall returns hits; expand returns a chunk-aligned span; `.qompack/state/mcp.json` written |
| `TestMCPToolsDocsUpToDate` | `devtool gen-mcp-docs` produces no diff | — |
| `TestPluginValidateSeesEightTools` | `mcp.ToolNames()` | length 8, matching the §8.7 table |

### Benchmarks / budget gates

| Name | Budget | Assertion |
|---|---|---|
| `TestBudgetBF` | **B-F**, p95 < `runtime.budgets.mcpToolCallMs` (default 250 ms) | 200 `Dispatch` calls (100 `expand` minimal, 60 `recall`, 40 `re_read`) over `newBigFixture` (2 000 tool uses, 40 MB); asserts `obs.HistSnapshot().P95 < time.Duration(cfg.Runtime.Budgets.MCPToolCallMs)*time.Millisecond` and that `Registry.CheckBudgets(cfg)` reports no `B-F` breach; failure message prints p50/p95/p99/max |
| `BenchmarkDispatchExpandMinimal` | — | `benchstat`-tracked; >25% regression fails per §7 |
| `BenchmarkDispatchRecall` | — | same |
| `BenchmarkResolveSpanMinimal` | — | same |
| `BenchmarkToolsListMarshal` | — | same |

Coverage floor for `internal/mcp` is **85%** (§6.4). `TestBudgetBF` runs in the normal `test` job on all three platforms.

---

## Commit plan

Work happens on `feat/sp13-mcp-retrieval-layer`, cut from `develop` with SP-01, SP-05, SP-06 and SP-09 already merged. Exactly seven commits. Each compiles and passes `go run ./tools/devtool test` for the packages it touches before it is made.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(mcp): hand-rolled JSON-RPC 2.0 stdio server with panic-isolated dispatch`

- [ ] `git checkout develop && git pull && git checkout -b feat/sp13-mcp-retrieval-layer`
- [ ] Write **failing** tests: `internal/mcp/jsonrpc_test.go`, `internal/mcp/server_test.go` (the 17 server tests above), `internal/mcp/fuzz_test.go` (`FuzzServeLine`), goldens `testdata/golden/mcp/initialize.json`
- [ ] `go test ./internal/mcp/...` → red
- [ ] Add `internal/mcp/types.go` (the shared vocabulary listed under Subagent strategy, including `ServerOptions`), `internal/mcp/jsonrpc.go`, `internal/mcp/server.go`, `internal/mcp/doc.go`; add `testdata/corpora/mcp/*` seeds
- [ ] `go test ./internal/mcp/... -race` → green; `go test -run FuzzServeLine -fuzz FuzzServeLine -fuzztime 30s ./internal/mcp` → no crashers
- [ ] `go run ./tools/devtool fmt lint vet`
- Body: why a hand-rolled server (D6) and why notifications must never produce a response. Footer: `Refs: SP-13, §7.2 L6, §8.7, 00-ARCHITECTURE §5.16`

### Commit 2 — `feat(mcp): eight tool definitions, input schemas and the conformance suite`

- [ ] Write **failing** tests: `internal/mcp/schema_test.go` (11 cases + the rapid property), `internal/mcp/tools_test.go`, `internal/mcp/conformance_test.go` (the 6 conformance rows); goldens `testdata/golden/mcp/tools-list.json` and `testdata/golden/mcp/schemas/*.json`
- [ ] Remove every `t.Skip` from `internal/mcp/mcptest`'s behaviour tests that this commit satisfies
- [ ] `go test ./internal/mcp/...` → red
- [ ] Add `internal/mcp/schema.go`, `internal/mcp/tools.go` (`ToolDefs`, `ToolNames`, `RegisterAll`, `RegisterProxy`, `Dispatch`, `ErrToolNotFound`, `StandingInstruction`); the eight `Handler` bodies are transitional stubs returning `core.ErrNotImplemented` (replaced in commits 3 and 4, after which that sentinel must not appear anywhere in `internal/mcp`), so the only tests exercising them here are the schema/registry ones
- [ ] `go test ./internal/mcp/... -race` → green
- Footer: `Refs: SP-13, §8.7, G6.2`

### Commit 3 — `feat(mcp): minimal-span resolver and the expand/re_read handlers`

- [ ] Write **failing** tests: `internal/mcp/span_test.go` (13 cases + 2 rapid properties + `BenchmarkResolveSpanMinimal`), `internal/mcp/handlers_span_test.go` (the 19 `expand`/`re_read` rows), `internal/mcp/fixture_test.go`
- [ ] `go test ./internal/mcp/...` → red
- [ ] Add `internal/mcp/handlers_common.go` (the `handlers` struct and the `run` preamble), `internal/mcp/span.go` (`Widener`, `SpanOpts`, `SpanResult`, `ResolveSpan`), and the `expand` and `re_read` handlers in `internal/mcp/handlers_span.go`
- [ ] `go test ./internal/mcp/... -race` → green; `go test -bench=BenchmarkResolveSpan ./internal/mcp`
- Body: the chunk-alignment rule, the `2 × MaxSpan` probe bound, and why widening is refused past `spanWidenLines`. Footer: `Refs: SP-13, §8.7, 00-ARCHITECTURE §5.16 span default`

### Commit 4 — `feat(mcp): recall, already_tried, record_eliminated, timeline, why, dropped`

- [ ] Write **failing** tests: the remaining rows of `internal/mcp/handlers_test.go` (recall × 4, already_tried × 7, record_eliminated × 10, timeline × 5, why × 5, dropped × 3 — 34 rows) and `internal/mcp/fake_test.go`; wire `fakeReader`/`fakeReporter` to `testdata/golden/contracts/checkpoint/{full,minimal,empty}.json` through `testutil.ContractFixture` under Rule W-2
- [ ] `go test ./internal/mcp/...` → red
- [ ] Implement the six handlers; nil-tolerance for `Checkpoints` and `Rehydrator`
- [ ] `go test ./internal/mcp/... -race` → green; `go run ./tools/devtool cover` → `internal/mcp` ≥ 85%
- Body: the uniform semantic-miss rule (`found:false`, never `isError`), and the §12.3 "bloom load fails ⇒ absent for everything" mapping. Footer: `Refs: SP-13, §8.3, §8.7, G6.2`

### Commit 5 — `feat(mcp): ephemeral-at-birth tagging and the expansion Promoter`

- [ ] Write **failing** tests: `internal/mcp/ephemeral_test.go` (6 cases), `internal/mcp/promote_test.go` (10 cases + rapid property), and the `_meta` assertions in `conformance_test.go`
- [ ] `go test ./internal/mcp/...` → red
- [ ] Add `internal/mcp/ephemeral.go` and `internal/mcp/promote.go`; wire `recordEphemeral` into all seven ephemeral handlers and `NoteExpansion` into `expand`/`re_read`
- [ ] `go test ./internal/mcp/... -race` → green
- Body: quote the §8.7 ephemeral-at-birth clause and state that the flag's *consumer* is SP-12's droppable ranking. Footer: `Refs: SP-13, §8.7, §12 retrieval re-inflation row`

### Commit 6 — `feat(cli): qompack mcp subcommand, daemon mcp op, and the initialize observable`

- [ ] Write **failing** tests: `internal/mcp/observable_test.go` (2 cases), `internal/daemon/mcpop_test.go` (9 cases), `internal/cli/cmd_mcp_test.go` (5 cases)
- [ ] `go test ./internal/mcp/... ./internal/daemon/... ./internal/cli/...` → red
- [ ] Add `internal/mcp/observable.go`, `internal/daemon/mcpop.go`, `internal/cli/mcpwire.go`; replace SP-01's `internal/cli/cmd_mcp.go` stub; add the resident-set block of spec §11 to SP-05's daemon bootstrap in `internal/cli/daemon.go` — `symbols.New()`, `store.Open`, `dag.Open`, `negknow.Open`, `mcp.NewPromoter`, the `opts.Store`/`opts.Graph`/`opts.Ledger` assignments, then `daemon.InstallMCPOp(&opts, NewToolDeps(…))`, all before `daemon.New(opts)` and each failure logged `Loud` and degraded — passing typed-nil `ckptReader`/`dropReporter`
- [ ] `go test ./... -race` → green; `go run ./tools/devtool build && ./bin/qompack mcp < testdata/corpora/mcp/initialize.ndjson` returns a valid `initialize` result
- Body: why handlers execute daemon-side (single writer, warm state) and the client process is a transcoder. Footer: `Refs: SP-13, 00-ARCHITECTURE §2.4, §12.1 mcp.server_registered`

### Commit 7 — `test(mcp): B-F latency gate, stdio e2e suite, and generated docs/mcp-tools.md`

- [ ] Write **failing** tests: `internal/mcp/bench_test.go` with `TestBudgetBF` and four benchmarks, `internal/mcp/benchfixture_test.go`, `test/e2e/mcp_e2e_test.go`, `tools/devtool/genmcpdocs_test.go`, `TestMCPToolsDocsUpToDate`, `TestPluginValidateSeesEightTools`
- [ ] `go test ./internal/mcp/... ./test/e2e/...` → red
- [ ] Add `tools/devtool/genmcpdocs.go`, register `gen-mcp-docs` in the devtool task table, generate `docs/mcp-tools.md`, add the `gen-mcp-docs` step to CI's `docs` job and the 8-tool assertion to `plugin-validate`
- [ ] `go run ./tools/devtool gen-mcp-docs && git diff --exit-code`
- [ ] `go run ./tools/devtool ci-local` → all green; push and confirm CI green on the branch
- [ ] After SP-10, SP-11 and SP-12 have merged into `develop`: `git rebase develop`; replace the typed-nil `ckptReader`/`dropReporter` in `internal/cli/daemon.go` with `checkpoint.OpenReader(root, log, metrics)` and `rehydrate.NewReporter(root, log)`; un-skip `TestStandingInstructionsAgree`; re-point the `why`/`dropped` tests from `fakeReader`/`fakeReporter` to those real implementations while keeping the same `testdata/golden/contracts/checkpoint/**` inputs (Rule W-2 verification — a fixture the real reader cannot reproduce is a verification failure, not a fixture bug); re-run `ci-local`, then merge with `--no-ff` as the last wave-3 merge
- Footer: `Refs: SP-13, 00-ARCHITECTURE §2.4 B-F, §7, §8`

---

## Subagent strategy

This subplan is heavy (~2 600 LOC of implementation plus ~2 200 LOC of tests). Partition it across four parallel subagents. **The commit plan stays strictly sequential and is executed only by the main session** — subagents produce files, the main session sequences them into the seven commits.

**Stays in the main session, done first, before any subagent is dispatched.** Write and land the shared vocabulary so every subagent codes against the same types, with no guessing:

- `internal/mcp/types.go` — `Tool`, `Request` (incl. the added `Turn`), `Content`, `Response`, `Handler`, `Server`, `ServerOptions`, `ToolDeps` (incl. `Widener`, `ProjectRoot`, `Clock`, `Log`, `Metrics`), `DropReporter`, `Promoter`, `Widener`, `Observable`, `SpanOpts`, `SpanResult`, all ten §5.16 arg/result structs with their json tags and the additive fields on `RecallHit`/`AlreadyTriedResult`, `ErrToolNotFound`, `StandingInstruction`.
- `internal/mcp/tools.go`'s schema constants (the eight JSON literals, verbatim from the Implementation spec) and `ToolNames()`.
- The eight golden schema files and `testdata/golden/mcp/tools-list.json`.
- `internal/mcp/handlers_common.go` (the `handlers` struct and the `run` preamble) and `internal/mcp/fixture_test.go` (`newFixture`).

Commit 1's server tests already need `Tool`, `Handler`, `Server` and `Response`, so `types.go` lands **in commit 1's staging area**; the eight schema literals, `ToolNames()` and the schema goldens land in commit 2's. Neither is a separate commit.

| Subagent | Owns (files) | Returns | Integration |
|---|---|---|---|
| **A — protocol** | `internal/mcp/jsonrpc.go`, `server.go`, `schema.go`, plus `jsonrpc_test.go`, `server_test.go`, `schema_test.go`, `fuzz_test.go`, `testdata/corpora/mcp/*`, `testdata/golden/mcp/initialize.json` | a passing protocol layer with zero dependency on any handler; `Register`/`Serve`/`Tools`/`Dispatch` behaviour green | becomes commits 1 and the server half of 2 |
| **B — span + content tools** | `internal/mcp/span.go`, `handlers_span.go`, `span_test.go`, `handlers_span_test.go` | `ResolveSpan` passing all 13 unit tests and both rapid properties; two handlers passing their 19 rows | becomes commit 3; must not touch `handlers.go` or `handlers_common.go` |
| **C — knowledge + index tools** | `internal/mcp/handlers.go` (recall, already_tried, record_eliminated, timeline, why, dropped), `handlers_test.go`, `fake_test.go` (`fakeReader`, `fakeReporter`, spy store/ledger) reading `testdata/golden/contracts/checkpoint/**` | six handlers passing their 34 rows, nil-tolerant for `Checkpoints`/`Rehydrator` | becomes commit 4 |
| **D — policy + wiring** | `internal/mcp/ephemeral.go`, `promote.go`, `observable.go`, `internal/daemon/mcpop.go`, `internal/cli/cmd_mcp.go`, `internal/cli/mcpwire.go`, `tools/devtool/genmcpdocs.go`, `test/e2e/mcp_e2e_test.go`, `internal/mcp/bench_test.go` | the Promoter, the ephemeral path, the transcoder, the observable, the docs generator, the B-F gate | becomes commits 5, 6 and 7 |

Rules for the partition:

- Subagents **A and B may start immediately** after the main session lands `types.go` + schemas. **C** may start at the same time (it depends only on `types.go` and the fixtures). **D** must wait for A (it registers on a real `Server`) and for the `recordEphemeral` call sites that B and C create — dispatch D once A is green and B/C have their handler signatures fixed, and have D stub the call sites behind a one-line `h.recordEphemeral(...)` that B and C already invoke.
- **File-level exclusivity is absolute.** Handlers are split across `handlers.go` (C) and `handlers_span.go` (B) precisely so two subagents never edit one file. The `handlers` struct and the shared `run` preamble live in `handlers_common.go`, written by the main session and read-only to every subagent.
- Each subagent returns a **diff plus its own `go test ./internal/... -race` output**; the main session re-runs the full suite before every commit and is the only actor that runs `git commit`.
- The daemon-bootstrap edit (spec §11's resident-set block: `symbols.New`, `store.Open`, `dag.Open`, `negknow.Open`, `mcp.NewPromoter`, the three `Options` assignments and `InstallMCPOp`) is made by the **main session**, never a subagent, because it is the only cross-subplan file touched, SP-12 depends on the same three assignments, and it must be re-checked after the wave-3 rebase.

---

## Exit criteria

### From `Qompack.md`, verbatim

Phase 2 (§10) — the exit criterion that names two of this slice's tools:

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

SP-13's contribution to it is the surface, and it is verified locally as: `already_tried` returns `"stale"` (never `"active"`) for every record whose `depends_on` hash changed in the `dependency-change-mid-session` synthetic fixture, and `"absent"` under `staleResponse:"drop"` — i.e. **zero stale-block incidents attributable to the MCP layer**.

§11.3 guardrails, verbatim:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

§12 risk row, verbatim, discharged by this slice:

> | Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |

`00-ARCHITECTURE.md` §2.4, verbatim:

> | **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

### Local, measurable

- [ ] All eight §8.7 tools registered, named exactly as the design table, with published JSON input schemas; `tools/list` byte-equals its golden.
- [ ] `mcptest.RunMCPSuite` passes with **zero remaining `t.Skip`s** (Rule W-1 merge blocker).
- [ ] Every retrieval response (the seven ephemeral tools) carries `_meta.qompack.ephemeral == true` under `retrieval.ephemeralResults: true`, and each writes a `store.ToolUseRecord` with `Ephemeral: true`. `record_eliminated` carries neither.
- [ ] Default `expand`/`re_read` responses are chunk-boundary aligned and ≤ `store.chunk.max` (16384) bytes before widening; `full=true` returns the whole object up to `runtime.mcp.maxResponseBytes` (262144); the paging property test reconstructs objects exactly.
- [ ] `already_tried` renders all three states — `absent`, `active`, `stale` — with the ledger's note verbatim, honours `eliminations.staleResponse`, and returns `absent` (never `active`) on any ledger failure or `BloomOnly` hit.
- [ ] `NoteExpansion` fires `promoted == true` at exactly `retrieval.promoteAfterExpansions`; `Promoted()` returns a deduplicated, promotion-ordered `[]core.Hash` that survives a process restart.
- [ ] `Services.MCPInitialized` is bound by `InstallMCPOp`, so `contract.DeclareProducers` declares `CMCPRegistered` and SP-05's assertion reports a real observation rather than `not-yet-implemented`; the first `initialize` sets `contract.SessionHistory.MCPInitialized` in `state/history.json` (`contract.HistoryPath(root)` — `state/contract.json` is the Monitor's own file and stays untouched) and writes `.qompack/state/mcp.json` as the human-readable handshake record. `qompack self-test` on a session that has used the MCP server reports `mcp.server_registered` OK.
- [ ] `TestBudgetBF` green on ubuntu, macos and windows: p95 < 250 ms over 200 calls against the 2 000-tool-use / 40 MB fixture.
- [ ] Handler panics, malformed lines, oversized lines and unknown methods never terminate `Serve`; the fuzz target finds no crashers in 30 s locally and 10 min nightly.
- [ ] `internal/mcp` line coverage ≥ **85%** (§6.4).
- [ ] `gofumpt -l` empty, `golangci-lint run` clean, `go vet` clean, the `nomagic` pass clean — the forbidden literals this package could plausibly reach for (`16384` chunk max, `120` args-preview width, `450`, `12000`, `10000`) appear nowhere in `internal/mcp` except behind an annotated `//nomagic:allow` line; `store.chunk.max`, `retrieval.promoteAfterExpansions`, `retrieval.defaultSpan`, `runtime.mcp.spanWidenLines` and `runtime.mcp.maxResponseBytes` are read from `config.Config` at every use site (D11, §11.6), import-graph check clean (`internal/mcp` imports only `store`, `negknow`, `checkpoint` + the foundation `core`/`paths`/`config`/`logging`/`obs` — **no `symbols`, no `canon`, no `sketch`, no `ipc`, no `daemon`, no `rehydrate`**; the `Widener` port and the untouched `PutOptions.Canon` field are what keep the first two out).
- [ ] `devtool gen-mcp-docs` produces no diff; `plugin-validate` sees exactly 8 tools.
- [ ] CI green on `feat/sp13-mcp-retrieval-layer` for `verify`, `test`, `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] Post-rebase on the merged wave-3 `develop`: `why` answers against the real `checkpoint.Reader`/`ExtractDecisions` output and `dropped` against the real `rehydrate.DropReporter`, with the Rule W-2 golden fixtures reproduced by the real implementations.

---

## Done checklist

- [ ] Branch `feat/sp13-mcp-retrieval-layer` cut from a `develop` containing SP-01, SP-05, SP-06, SP-09.
- [ ] Exactly **7 commits**, in the stated order, each with its conventional-commit message and a `Refs:` footer naming SP-13, the gap IDs, and the `Qompack.md` sections.
- [ ] **No `Co-Authored-By` lines and no attribution trailers on any commit message** — re-checked with `git log --format=%B feat/sp13-mcp-retrieval-layer ^develop | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'` returning nothing.
- [ ] TDD honoured: for each commit, the enumerated tests were written and observed failing before the implementation landed.
- [ ] **Spec coverage self-review**: walk the "Design context" section top to bottom and point at the code or test that implements each quoted item — the §8.7 tool table (all eight), the three ephemeral bullets, the Appendix C `retrieval` block (all three keys), the `runtime.mcp` keys, the §8.3 three-way response and `scope`, the §12 re-inflation row, the §12.1 `mcp.server_registered` observable, the §12.3 bloom-failure and panic rows, and budget B-F.
- [ ] **Placeholder scan**: `grep -rn -E 'TODO|TBD|FIXME|XXX|not implemented|unimplemented' internal/mcp internal/daemon/mcpop.go internal/cli/cmd_mcp.go internal/cli/mcpwire.go tools/devtool/genmcpdocs.go docs/mcp-tools.md` returns nothing (`core.ErrNotImplemented` must no longer appear in `internal/mcp`).
- [ ] **Type consistency with the Interface contract**: every §5.16 name, field and signature is present and unchanged; the five additive fields (`Request.Turn`, `ToolDeps.Widener/ProjectRoot/Clock/Log/Metrics`) are additions only; no method was added to another subplan's interface (Rule W-3).
- [ ] Out-of-scope discipline: no file under `internal/checkpoint`, `internal/rehydrate`, `internal/negknow`, `internal/store`, `internal/symbols`, `internal/scheduler`, `internal/commands` or `internal/analyzer` was modified. The only edits outside SP-13's own files are: the `t.Skip` removals in SP-01's `mcptest`, the replacement of SP-01's `internal/cli/cmd_mcp.go` stub, the **resident-set block** added to SP-05's `internal/cli/daemon.go` (spec §11: `symbols.New`, `store.Open`, `dag.Open`, `negknow.Open`, `mcp.NewPromoter`, the `opts.Store`/`opts.Graph`/`opts.Ledger` assignments and the `InstallMCPOp` call, plus their `Loud`-and-degrade error paths and the two `defer Close`s — and nothing else in that file), the `gen-mcp-docs` entry in `tools/devtool`, and the two CI job additions (`docs`, `plugin-validate`). `git diff --stat develop` is checked against exactly that list.
- [ ] `Qompack.md` is untouched at the repository root.
- [ ] Commit count verified in `5–8`; wave-3 merge performed last, with `--no-ff`, after SP-10, SP-11 and SP-12.

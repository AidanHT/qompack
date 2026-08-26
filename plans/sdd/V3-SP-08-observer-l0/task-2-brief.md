# Task brief — Commit 2: PostToolUse pipeline (observer.go, state.go, tooluse.go, graph.go, sketches.go, features.go)

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject: `feat(observer): PostToolUse pipeline: store, index, sketches, DAG emission`.
- THIS commit also lands `features.go` and `features_test.go` (the plan lists them under Commit 4): `OnToolUse` steps 12–13 and the rows `TestOnToolUse_TodoTransitionOnlyOnce`, `TestOnToolUse_LastTSIsPreviousEventTS`, `TestOnToolUse_SignalsDelivered` need `newlyCompletedTodos`, `recordRecent` and `features`. Implement the whole `features.go` spec and every `features_test.go` row (both are included below).
- THIS commit creates `prompt.go` containing ONLY `collectThrash(st)` and `pendingThrashLines(st)` (step 11 needs the first; keep the second beside it) plus a file comment; `OnUserPrompt` and the rest of prompt.go are Commit 4's.
- Step 8 (`detectSupersession`) is Commit 3's: leave `superseded` nil at step 8 with a one-line comment naming supersede.go; do not write supersede.go.
- `TestModePassiveStillWrites` and `TestOnToolUse_ConcurrentSessionsRaceFree` drive tool-use events only in this commit (OnUserPrompt does not exist yet); Commit 4 extends them.
- Add `TestObserverImportSetIsExact` (beside `TestObserverSourceHasNoBloomReference`): parse every non-test `.go` file in `internal/observer` with `go/parser` and assert the set of `github.com/qompack/qompack/internal/...` imports is EXACTLY `{core, paths, config, logging, obs, hookio, store, canon, sketch, dag, grammar, tokens}` (the devtool import-graph check permits `negknow`/`chunk`, so this is the only enforcement).
- `paths.WriteAtomic` is `WriteAtomic(p string, b []byte, perm fs.FileMode) error` (three args) — follow existing callers for the perm.
- `store.ArgsDigest` is a package-level function; `PutResult` also carries `Truncated bool` and `Redacted int` (ignore them). `NodeKind`/`EdgeKind` each end with a trailing `KindInvalid`/`EdgeInvalid`.
- `hookio.Output`/`HSO` are hand-built where needed; no edit to `internal/hookio`.
- `Persist` in state.go may be completed here (it is `persistState` + `Graph.Flush`); Commit 6 only revisits it if needed.


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
<!-- plan lines 123-137 -->

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
<!-- plan lines 733-825 -->

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


---
<!-- plan lines 1000-1145 -->

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


---
<!-- plan lines 1228-1428 -->

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


---
<!-- plan lines 1755-1825 -->

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


---
<!-- plan lines 2032-2060 -->

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


---
<!-- plan lines 2082-2117 -->

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


---
<!-- plan lines 2155-2172 -->

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


---
<!-- plan lines 2422-2439 -->

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


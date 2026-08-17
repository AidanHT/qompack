# SP-10: L4 checkpointer: the versioned importance-ordered checkpoint schema, decision extraction, PreCompact hook, pins, focus instructions with the O1 incremental span, and store-only regeneration

> **Recommended model: Opus 5 · max effort**
>
> Nine gaps close here, and the checkpoint is the durable artifact every other Qompack claim rests on: the `MarkEncoded` DPI guard, §6.9 importance-ordered truncation, git-backed pointer validation, and the O1 span instruction. Reasoning-dense at moderate volume — a good `max` candidate.

**Branch:** `feat/sp10-checkpointer-l4` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of `["SP-01","SP-06","SP-07","SP-08","SP-09"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-11 rehydrator, SP-12 scheduler, SP-13 MCP) | **Design sections:** §6.9, §7.2 L4, §7.3 (PreCompact), §8.5, §12 (PreCompact timeout, summarizer-ignores-instruction rows) | **Gaps closed:** G2.2, G2.4, G2.5, G2.6, G3.4, G4.3, G5.3, G7.4, G9.1

---

## Mission

This slice builds layer L4 — the checkpointer — which is the durable artifact that makes every other Qompack claim true. Claude Code's summary "lives only in context" (G9.1) and is "prose, not typed state" (G2.4); each pass re-summarizes the previous summary, and the data processing inequality guarantees the degradation curve in §1.1. The checkpointer replaces that with an **immutable, versioned, importance-ordered JSON artifact** written from the store's originals and never from anything sitting in the context window. Because it is typed, it can be diffed, validated and queried; because it is append-only on disk, the next compaction cannot rewrite it; because it is importance-ordered (§6.9), truncation at any budget yields the best available reconstruction rather than the head of a positional cut.

Three things in this slice are load-bearing for other subplans. **`ExtractDecisions` is the only producer of `core.DecisionID`** — without it SP-13's `why(decision_id)` tool has nothing to answer with, and the `KindDecision`/`EdgeExplains` nodes it emits into the DAG are what let slice scores rank decisions inside a budget. **`FocusInstructions`** carries the O1 incremental-span paragraph that the closing note calls "the cheapest line in the entire plan relative to what it buys": it shrinks the most expensive call in the session and operationalizes never-compress-a-compression inside a pipeline the plugin otherwise cannot reach. **`Advance`** is the incremental writer that keeps the frontier moving during idle windows, so `PreCompact` only finalizes and stays inside the 2 s budget B-E rather than trying to do O(session) work inside a 20 s hook.

**What exists when you start.** `develop` carries: the full Go 1.26 module, `internal/core` (`Hash`, `SessionID`, `TurnIndex`, `SegmentID`, `CheckpointSeq`, `DecisionID`, `Tokens`, `Dep`, `ChunkRef`, `Clock`, the sentinel errors), `internal/paths` (`WriteAtomic`, `AppendOnly`, `CreateNew`, `Norm`, `Key`, long-path handling), `internal/config` (the whole of Appendix C plus the `runtime` namespace), `internal/logging` (incl. `Loud`), `internal/obs`, `internal/tokens`, `internal/hookio`, `internal/cli` with a no-op `qompack checkpoint` subcommand, `internal/daemon` + `internal/ipc` with the op-routing table, `IdleController.Register` and the late-bound `Services` set (SP-05), the L1 store with `SegmentLog` and `MarkEncoded` (SP-06), the dependence DAG with `BackwardSlice`/`CrossingEdges` (SP-07), the observer with verbatim user capture (SP-08), and the negative-knowledge ledger (SP-09). `internal/checkpoint` and `internal/pins` exist as SP-01 compiling stubs returning `core.ErrNotImplemented`, with a `checkpointtest` conformance suite whose behaviour tests are `t.Skip`ped.

**What exists when you finish.** `internal/pins` is a working append-only invariant log with tombstone deletion and a materialized `invariants.json` view. `internal/checkpoint` implements the §8.5 schema verbatim as versioned Go types with a live migration hook, `Writer` (`Begin`/`Advance`/`Finalize`/`Abort`) over a `SourceSet` that structurally cannot carry live context text, `Reader` (`Latest`/`Get`/`List`/`Chain`/`Verify`) over `checkpoints/MANIFEST.jsonl` with re-hash verification and parent fallback, `Truncate` with tier1-never/tier2-late/tier3-first ordering, `ExtractDecisions`, `ValidatePointers` against the working tree and the git index, `FocusInstructions` with the O1 span paragraph, and injection tagging with `StripInjections`. The `PreCompact` hook writes a real checkpoint and emits `customInstructions`; the idle controller advances the frontier and, on the cadence §8.5 requires, finalizes a checkpoint even when compaction never fires; and every `checkpointtest` skip is flipped off.

---

## Design context (verbatim from Qompack.md)

### §6.9 Progressive / embedded encoding

> **Closes:** G4.3, G7.3
>
> Embedded coders order the bitstream by importance so truncation *at any point* yields the best available reconstruction for that budget. Current truncation is positional — skills keep their head, PTL retry drops the oldest rounds, which is where intent lives.
>
> If the checkpoint is written in importance order, every budget cut is automatically near-optimal and PTL recovery stops deleting the task statement first. **This is a serialization-order change, not an algorithm** — one of the cheapest wins available.

### §7.2 layer diagram, L4 row

```
├─────────────────────────────────────────────────────────────────┤
│ L4  CHECKPOINTER          PreCompact → immutable versioned       │
│                           artifact, importance-ordered           │
├─────────────────────────────────────────────────────────────────┤
```

### §7.3 hook surface, PreCompact row

| Hook | Layer | Responsibility |
|---|---|---|
| `PreCompact` | L4 | Write immutable checkpoint; emit focus instructions via `custom_instructions` |

### §7.4 directory layout (the two directories this slice writes)

```
├── checkpoints/
│   ├── 0001.json                  # immutable, importance-ordered
│   └── 0002.json
├── pins/
│   └── invariants.json            # user- and agent-pinned, never summarized
```

> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### §8.5 L4 — Checkpointer (the whole section)

> **Trigger:** `PreCompact` (and independently on the scheduler's own cadence, so checkpoints exist even when compaction does not fire).
>
> **Output:** an immutable, versioned, **importance-ordered** JSON artifact. Ordering is the embedded-coding principle from §6.9 — truncation at any point yields the best available reconstruction for that budget.

```jsonc
{
  "version": 1,
  "session": "…",
  "seq": 7,
  "created": "…",
  "parent": "0006.json",
  "encoded_segments": [12, 13, 14],     // DPI guard: from originals only

  // ── Tier 1: never truncated ──────────────────────────────
  "invariants": [ … ],                   // pinned, verbatim
  "user_intent": {
    "original": "…",                     // verbatim, from L0, never regenerated
    "evolution": [ … ]                   // verbatim deltas
  },
  "eliminated": [                        // negative knowledge, structured
    { "target": "src/auth.ts:refreshToken",
      "approach": "widen pool timeout",
      "reason": "pgbouncer 1.18 ignores it in transaction mode",
      "evidence": "sha256:…",
      "depends_on": [                     // staleness guard (§8.3):
        { "path": "docker-compose.yml", "hash": "sha256:…" },
        { "path": "package-lock.json",  "hash": "sha256:…" }
      ],
      "scope": "project",                 // "session" | "project"
      "status": "active" }                // "active" | "stale"
  ],

  // ── Tier 2: truncate late ────────────────────────────────
  "decisions": [
    { "what": "…", "why": "…", "alternatives_rejected": [ … ],
      "evidence": "sha256:…" }
  ],
  "open_questions": [ … ],
  "current_work": { "goal": "…", "next_step": "…", "blocked_on": null },

  // ── Tier 3: truncate first ───────────────────────────────
  "pointers": {
    "files":  [ { "path": "…", "hash": "sha256:…", "why": "…" } ],
    "tools":  [ { "tool_use_id": "…", "hash": "sha256:…", "summary": "…" } ]
  },
  "narrative": "…",                      // prose residue, last resort

  // ── Metadata ─────────────────────────────────────────────
  "sketch_refs": { "tried": "tried.bloom", "touch": "touch.cms" },
  "dropped": [ { "kind": "path_rule", "id": "api-conventions.md" } ],
  "cache": { "p_chosen": 148230, "rewrite_tokens": 18770, "ttl_state": "warm" }
}
```

> Note what is **not** here: no code snippets. Files are pointers with a one-line reason. This is §4.4 applied directly, and it is where most of the 50K eager-restore budget is reclaimed.
>
> **Focus instruction emission.** `PreCompact` can supply `custom_instructions`. Qompack generates these from the checkpoint rather than leaving them to the user, and the standing instruction template implements §4.5:
>
> > Encode what a competent engineer with no session history would get wrong. Do not restate file contents, directory structure, or command output — those are retrievable. Prioritise: intent, decisions and their rationale, approaches eliminated and why, and constraints discovered empirically.
>
> **Incremental summarization via focus instructions (O1).** Because a durable checkpoint already covers everything through turn `N`, the focus instructions also narrow the summarizer's *span*:
>
> > A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn N, including all decisions, eliminations, and file state up to that point. Do not re-summarize that material. Summarize only what happened after turn N: new decisions, new eliminations, new intent, current work.
>
> This is the plugin-legal analogue of Session Memory Compact, and it attacks three problems at once. The most expensive call in the session (G7.1) shrinks, because the model is asked to compress a fraction of the transcript rather than all of it. DPI exposure drops, because segments already encoded are never re-summarized — the instruction *operationalizes* the never-compress-a-compression invariant inside Claude Code's own pipeline, where the plugin otherwise has no reach. And summary quality rises, because the summarizer's attention is concentrated on the recent, relevant span instead of diluted across 167K tokens (partially mitigating G8.2). The one caveat: `custom_instructions` is advisory — the summarizer may ignore the span restriction — so the checkpoint remains the authoritative record and the rehydrator never depends on the summary having complied.
>
> **Regeneration rule (closes GC).** Checkpoints are **always generated from the store** — the chunk objects, segment log, verbatim intent captures, and structured records — never from content currently sitting in the context window. This matters because the rehydrator's own prior injection lives in message history and will be mangled by the next Claude Code summarization pass; a checkpoint that trusted the surviving in-context version would be compressing a compression through the back door. The rehydrator tags every injection with its checkpoint sequence number precisely so the next checkpoint pass can identify and ignore that material as a source.
>
> **Amortized compaction — the latency architecture (O5).** […] The fix is to keep the checkpoint frontier moving continuously: every time a segment closes (changepoint, todo completion, passing test run), encode it into the checkpoint during the next idle moment, advancing frontier `N` all session long. When compaction fires, the O1 instruction restricts the summarizer to turns after `N` — and that residual span is now 10–20K tokens rather than 150K, **regardless of how long the session has run**. Short novel prefill, short decode, every time.
>
> This is precisely the stop-the-world vs. incremental garbage collection distinction. […] Per-compaction cost drops from **O(session) to O(delta)** — amortized O(1) per turn […]

### §8.2, the DPI guard this slice must call

> **Segment log.** Changepoint-delimited segments, each with: start/end turn, feature summary, encoded-once flag, and checkpoint reference. **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint. It is re-encoded from the *original chunks* or not at all.

### §4.4 — what may and may not be encoded

> **Reconstructible at near-zero cost — must become pointers, never prose:**
> - File contents (re-read)
> - `git status`, `git diff`, branch state
> - Directory structure
> - Deterministic tool output (`Grep`, `Glob`)
> - Test results (re-run)
>
> **Non-reconstructible — this is what the budget is for:**
> - User intent and its evolution
> - Decisions and their rationale
> - Approaches ruled out and why
> - Exact text of an error that will not reproduce
> - Causal chains of reasoning
> - Constraints discovered empirically

### §4.6 — the invariant this slice enforces mechanically

> **The only fix is structural: never compress a compression.** Encode each transcript segment exactly once, from the original on disk, and append. This is a non-negotiable invariant of the Qompack design and is enforced by construction in §8.4.

### §9 gap traceability rows owned here

| Gap | Closed by | Residual |
|---|---|---|
| G2.2 nothing pinned | `pins/invariants.json` + sketches | — |
| G2.4 prose not typed state | L4 typed checkpoint schema | Claude Code's own summary stays prose |
| G2.5 no ground-truth check | L4 validates pointer set against `git status` | — |
| G2.6 session-memory drift | Qompack checkpoint is the durable source | — |
| G3.4 snippets in summary | L4 focus instructions forbid them | Advisory to the summarizer, not enforced |
| G4.3 skill head-truncation | L4 importance ordering; L5 skill index | Cannot change Claude Code's own truncation |
| G5.3 homogeneous treatment | L4 three-tier typed schema | — |
| G7.4 circuit breaker | L4 checkpoints mean the session survives a give-up | — |
| G9.1 not durable | L4 immutable versioned checkpoints | — |

**Where each of those gaps is actually closed in this slice** (every row names a file and the test
that fails if the closure regresses):

| Gap | Closed by, concretely | Failing test if it regresses |
|---|---|---|
| G2.2 | `internal/pins` append-only log + `pins/invariants.json` materialized view; `Begin` seeds `Checkpoint.Invariants` from `src.Pins.All` verbatim and `Truncate` lists `invariants` in tier 1 | `TestPinsRemoveWritesTombstone`, `TestBeginSeedsTierOneFromSources`, `TestTruncateNeverTouchesTierOne` |
| G2.4 | `schema.go` — the §8.5 shape as typed Go with a `version` field and a migration hook; nothing in the artifact is free prose except `narrative` | `TestSchemaFieldOrderIsImportanceOrder`, `TestGoldenCheckpointRoundTrip`, `TestMigrateRejectsFutureVersion` |
| G2.5 | `validate.go` + `gitindex.go` — every file pointer checked against the working tree and the parsed `.git/index` before the artifact is written; `Finalize` removes unresolvable pointers | `TestValidatePointersMissingFile`, `TestValidatePointersDirtyAgainstIndex`, `TestFinalizeRemovesUnresolvablePointers` |
| G2.6 | `SourceSet` + `Advance`'s store-only encoding + `SegmentLog.MarkEncoded`: the checkpoint is the durable source and can never be derived from a Claude Code summary or from a surviving in-context injection | `TestSourceSetCarriesNoText`, `TestAdvanceStripsInjectionsFromStoredPrompts`, `TestAdvanceIsDPIGuarded` |
| G3.4 | `focus.go`'s `snippetProhibition` paragraph (advisory, per §9's residual) **plus** the mechanical backstop: no code ever enters the artifact, because pointers are `{path, hash, why}` and `why` is a single line | `TestGoldenCheckpointsContainNoCodeBlocks`, `TestAdvancePointersCarryNoContent`, `TestFocusContainsSentinel` |
| G4.3 | `truncate.go` — importance ordering replaces head-first truncation, so any budget cut is near-optimal (§6.9); tier 1 is structurally uncuttable | `TestTruncateDropsTierThreeFirst`, `TestTruncateReachesTierTwoOnlyAfterTierThree`, `TestTruncateIsMonotone` |
| G5.3 | the three-tier typed schema itself: intent, eliminations, decisions, pointers and prose each get their own slot and their own lifetime instead of one prose blob | `TestTruncateDropsToolPointersBeforeFilePointers`, `TestTruncateEmptiesAlternativesBeforeDroppingDecisions` |
| G7.4 | `Truncate` has no error return and always yields a writable tier-1 document, and `Finalize` writes even when everything else has been cut, so a session whose auto-compact circuit breaker has tripped still has a durable artifact to resume from | `TestTruncateNeverTouchesTierOne`, `TestPreCompactFinalizesAsIsNearDeadline` |
| G9.1 | `paths.CreateNew` + `0444` + `MANIFEST.jsonl` re-hash: the artifact is on disk, immutable, versioned and chained by `parent` | `TestFinalizeIsImmutable`, `TestManifestLineFormat`, `TestChainReturnsOldestFirst`, `TestVerifyReturnsMismatchedSeqs` |

### §11.3 guardrail applicable here

> - Hook p99 latency < 15ms (L0), **< 2s (L4)**

### §12 risk rows owned here

| Risk | Severity | Mitigation |
|---|---|---|
| `PreCompact` timeout too short to write a checkpoint | Medium | Write incrementally on the scheduler's cadence so `PreCompact` only finalizes. Never depend on doing all the work in the hook. |
| Summarizer ignores the incremental-span instruction | Low | `custom_instructions` is advisory; the checkpoint remains authoritative and the rehydrator never depends on summary compliance (§8.5) |

> - **Cannot guarantee the summarizer honours focus instructions** — span narrowing (§8.5) and snippet prohibition (G3.4) are advisory; the durable checkpoint is the backstop for both.

### Appendix C, the `checkpoint` block (verbatim)

```jsonc
  "checkpoint": {
    "budgetTokens": 12000,
    "incrementalSpanInstruction": true,
    "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
    "tiers": { "never": ["invariants","user_intent","eliminated"],
               "late":  ["decisions","open_questions","current_work"],
               "first": ["pointers","narrative"] }
  },
```

### §10 Phase 3 exit criterion (the half this slice owns)

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

---

## Out of scope

| Not built here | Owned by |
|---|---|
| The `SessionStart(source=compact)` rehydrator, the eight-item injection, the drop report surface, `DropReporter`, `rules`, `skills`, the 8–12K budget | **SP-11** |
| Emitting the injection tags into `additionalContext` (SP-10 defines the tag constants and `StripInjections`; SP-11 *writes* the tags) | **SP-11** |
| BOCD, the composite trigger, p-selection, `CacheInfo` *values*, segment **closing**, the idle controller itself, `scheduler.Runtime` | **SP-12** |
| The MCP server and the `why` tool that consumes `checkpoint.Reader` + `core.DecisionID` | **SP-13** |
| `/qompack:checkpoint`, `/qompack:pin`, `/qompack:why` slash commands and their output formatting | **SP-14** |
| Grammar-compressed action history folded into the checkpoint (`internal/checkpoint/grammar.go`) | **SP-15** |
| Demand-driven pointer promotion and truncation-boundary tuning (`internal/checkpoint/promote.go`) | **SP-16** |
| The elimination ledger, descriptors, staleness flip, bloom rebuild | **SP-09** (consumed here) |
| `SegmentLog`, `MarkEncoded` semantics, store objects, GC | **SP-06** (consumed here) |
| `BackwardSlice`, `CrossingEdges`, DAG persistence | **SP-07** (consumed here) |
| Verbatim user capture into `segments.jsonl`, `observer.OnSessionStart` source switch | **SP-08** (consumed here) |
| `IdleController`, the op-routing table, `Services`, the contract monitor's assertion runner | **SP-05** (consumed here) |
| `paths.CreateNew`/`AppendOnly`/`WriteAtomic`, config loading, `tokens.Estimator` | **SP-01** / **SP-06** (consumed here) |

**One boundary worth stating twice, because §8.5 and §8.4 both use the word "cadence."** SP-12 decides *when a compaction should happen* (BOCD, Young–Daly, the composite trigger, p-selection) and *when a segment closes*. SP-10 decides only *when a draft is sealed into an immutable checkpoint* — the `checkpoint_cadence` idle task of spec §16, whose two conditions are computed from the draft alone (its estimated size against `checkpoint.budgetTokens`, and its encoded-segment count). That is the second half of §8.5's trigger clause and it deliberately does not consult the scheduler, so it works on a `develop` where SP-12 has not merged. SP-10 never triggers a compaction and never closes a segment.

---

## Interface contract

### Consumes (exact signatures already on `develop`)

```go
// core (§4)
type Hash [32]byte
func (h Hash) String() string                       // "sha256:" + hex
func HashBytes(domain string, b []byte) Hash
type SessionID string; type ToolUseID string; type TurnIndex int
type SegmentID int; type CheckpointSeq int; type DecisionID string
type Tokens int; type UnixMilli int64
type Dep struct{ Path string; Hash Hash }
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
var ErrNotFound, ErrAppendOnly, ErrAlreadyEncoded, ErrBudget, ErrContract error

// paths
func WriteAtomic(p string, b []byte) error
func AppendOnly(p string) (*os.File, error)
func CreateNew(p string) (*os.File, error)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string

// config
type CheckpointCfg struct {
    BudgetTokens int
    IncrementalSpanInstruction bool
    Frontier FrontierCfg
    Tiers TiersCfg
}
type FrontierCfg struct{ AdvanceOnSegmentClose bool; MaxResidualTokens int }
type TiersCfg struct{ Never, Late, First []string }

// store (§5.8)
func (Store) GetRoot(ctx context.Context, root core.Hash) (Root, error)
func (Store) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
func (Store) Has(h core.Hash) bool
func (Store) ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
func (Store) ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
func (Store) FileHistory(ctx context.Context, path string) ([]FileVersion, error)
func (Store) Segments() SegmentLog
type SegmentLog interface {
    Get(ctx context.Context, id core.SegmentID) (Segment, error)
    Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
    Current(ctx context.Context, s core.SessionID) (Segment, error)
    MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
    Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
    Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error)
}

// dag (§5.9)
func (Graph) AddNode(n Node) error
func (Graph) AddEdge(e Edge) error
func (Graph) Node(id NodeID) (Node, bool)
func (Graph) In(id NodeID) []Edge
func (Graph) Out(id NodeID) []Edge
func (Graph) BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
const ( KindDecision NodeKind = …; EdgeExplains EdgeKind = … )

// negknow (§5.10)
func (Ledger) Active(ctx context.Context, scope Scope) ([]Record, error)
func (Ledger) All(ctx context.Context) ([]Record, error)

// grammar (§5.11)
func (Sequitur) Compressed() []Symbol

// tokens (§5.20)
func (Estimator) Estimate(b []byte, c Class) core.Tokens
func (Estimator) EstimateString(s string, c Class) core.Tokens

// daemon extension seams (§5.4, SP-05)
func (Options) Handle(op ipc.Op, h ipc.Handler)
type IdleController interface{ Register(name string, prio int, fn func(ctx context.Context) error); … }
```

### Produces (relied on by SP-11, SP-12, SP-13, SP-14, SP-15, SP-16)

```go
// package checkpoint — the §5.14 contract, implemented in full
const SchemaVersion = 1
const SentinelPhrase = "qompack checkpoint"
const InjectionOpenTag  = "<!-- qompack:injected seq=%d ver=%d -->"
const InjectionCloseTag = "<!-- /qompack:injected -->"

type Checkpoint struct { … }        // exactly the §5.14 / §8.5 shape, field order normative
type UserIntent struct{ Original string `json:"original"`; Evolution []string `json:"evolution"` }
type Decision struct {
    ID core.DecisionID `json:"id"`
    What string `json:"what"`; Why string `json:"why"`
    AlternativesRejected []string `json:"alternatives_rejected"`
    Evidence core.Hash `json:"evidence"`
    Turn core.TurnIndex `json:"turn"`
}
type CurrentWork struct{ Goal, NextStep string; BlockedOn *string }
type Pointers struct{ Files []FilePointer `json:"files"`; Tools []ToolPointer `json:"tools"` }
type FilePointer struct{ Path string `json:"path"`; Hash core.Hash `json:"hash"`; Why string `json:"why"` }
type ToolPointer struct{ ToolUseID core.ToolUseID `json:"tool_use_id"`; Hash core.Hash `json:"hash"`; Summary string `json:"summary"` }
type DropEntry struct{ Kind string `json:"kind"`; ID string `json:"id"`; Detail string `json:"detail,omitempty"` }
type CacheInfo struct{ PChosen int `json:"p_chosen"`; RewriteTokens int `json:"rewrite_tokens"`; TTLState string `json:"ttl_state"` }
type Invariant = pins.Invariant
type SourceSet struct{ Store store.Store; Segments store.SegmentLog; Ledger negknow.Ledger;
                       Pins pins.Store; Graph dag.Graph; Grammar grammar.Sequitur; Tokens tokens.Estimator }
type Draft struct{ /* opaque */ }
type Ref struct{ Seq core.CheckpointSeq; Path string; SHA256 core.Hash; Bytes int64;
                 Tokens core.Tokens; Frontier core.TurnIndex; Created core.UnixMilli }

type Writer interface {
    Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error)
    Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error)
    Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error)
    Abort(d *Draft) error
}
type Reader interface {
    Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
    Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
    List(ctx context.Context) ([]Ref, error)
    Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error)
    Verify(ctx context.Context) ([]core.CheckpointSeq, error)
}
// ADDITIVE (SP-10 owns the package; §5.14 is not modified, only extended):
type Compactor interface{ PreCompact(ctx context.Context, in PreCompactInput) (PreCompactResult, error) }
type PreCompactInput struct {
    Session core.SessionID; Trigger string; Now time.Time; Deadline time.Time
    HookTimeout time.Duration   // the manifest's PreCompact timeout, supplied by the daemon;
                                // recorded as timeout_ms. Never a literal inside `checkpoint`.
    Budget core.Tokens; Cfg config.CheckpointCfg
    Cache CacheInfo; ExtraDrops []DropEntry
    CurrentWork *CurrentWork    // nil ⇒ use the value the draft derived (spec §8, step 3f)
    OpenQuestions []string      // appended to the draft's derived list; may be nil
}
type PreCompactResult struct {
    Ref Ref; Instructions string; Drops []DropEntry
    Truncated bool; Wall time.Duration; NewDraft bool
}
// Truncated is true iff at least one entry in the finalized checkpoint's Dropped list has a
// Kind from the truncation table of spec §10 — {narrative, tool_pointer, file_pointer,
// open_question, alternatives, decision, next_step, budget_exceeded}. Pointer-validation
// drops (pointer_missing, pointer_invalid, …) are NOT truncation and do not set it.
func (d *Draft) SetCache(c CacheInfo)
func (d *Draft) AddDrops(e ...DropEntry)
func (d *Draft) SetCurrentWork(w CurrentWork)
func (d *Draft) SetOpenQuestions(q []string)
func (d *Draft) AddOpenQuestion(q string)
func (d *Draft) Frontier() core.TurnIndex
func (d *Draft) Seq() core.CheckpointSeq

func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)
func ValidatePointers(ctx context.Context, root string, p Pointers) ([]DropEntry, error)
func ExtractDecisions(ctx context.Context, src SourceSet, from core.TurnIndex) ([]Decision, error)
func MintDecisionID(what, why string, evidence core.Hash) core.DecisionID
func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string
type FocusOptions struct{ IncrementalSpan bool; Frontier core.TurnIndex; CheckpointPath string; ForbidSnippets bool }
func StripInjections(s string) string
func StripInjectionsCount(s string) (string, int)
func OpenTag(seq core.CheckpointSeq) string
func Migrate(raw []byte) ([]byte, error)
func Marshal(c Checkpoint) ([]byte, error)          // canonical bytes; Migrate-free
func Unmarshal(raw []byte) (Checkpoint, error)      // runs Migrate first
func Filename(seq core.CheckpointSeq) string        // "0007.json"
func CreatedNow(clk core.Clock) string              // "2006-01-02T15:04:05.000Z"
// SetObservers installs the package-level logger and metrics registry used by the
// package-level functions (ExtractDecisions, StripInjectionsCount, ValidatePointers) that
// take no receiver and whose signatures §5.14 fixes. Both default to no-ops, so those
// functions are usable without a writer; OpenWriter calls it once. See spec §5b.
func SetObservers(log logging.Logger, m obs.Registry)
func OpenWriter(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) (*FileWriter, error)
func OpenReader(root string, log logging.Logger, m obs.Registry) (Reader, error)
// *FileWriter implements Writer and Compactor; the unexported *fileReader implements Reader.

// package pins
type Invariant struct {
    ID string `json:"id"`; Text string `json:"text"`
    Source string `json:"source"`; Pinned core.UnixMilli `json:"pinned"`
}
type Store interface {
    Add(ctx context.Context, inv Invariant) error
    Remove(ctx context.Context, id string) error
    All(ctx context.Context) ([]Invariant, error)
    Materialize(ctx context.Context) error
}
// The unexported *pinStore implements Store. `m` backs the pins.badline counter.
func Open(root string, log logging.Logger, m obs.Registry, clk core.Clock) (Store, error)
func MintID(text string) string   // "inv_" + first 12 hex of HashBytes("qompack.pin", text)
```

**`Ref` field population — normative.** `Ref.Seq`, `Ref.Path`, `Ref.SHA256`, `Ref.Bytes` and
`Ref.Created` come from `checkpoints/MANIFEST.jsonl`, whose four-key shape §3.3 fixes and which
this subplan may not extend. `Ref.Tokens` and `Ref.Frontier` are therefore **writer-only**: they
are populated on the `Ref` returned by `Finalize` and `PreCompact`, and are `0` on every `Ref`
returned by `Reader.Latest`/`Get`/`List`. Consumers that need the frontier of the most recent
checkpoint read `frontier` from `.qompack/state/precompact.json` (below). A doc comment on `Ref`
states this, and `TestReaderRefLeavesWriterOnlyFieldsZero` asserts it, so SP-11 does not silently
depend on a zero.

**Contract with SP-05 (contract monitor observable).** After every `PreCompact`, SP-10 writes `.qompack/state/precompact.json` via `paths.WriteAtomic`:

```json
{"seq":7,"sentinel":"qompack checkpoint","emitted_at":1754902951000,"wall_ms":812,"timeout_ms":20000,"instructions_bytes":1104,"frontier":58,"span_instruction":true}
```

This file is the observable for `contract.CPreCompactTiming` (`wall_ms` vs `timeout_ms`, warn above 60%) and `contract.CPreCompactCustomInstr` (`sentinel` searched for in the post-compaction summary). Until this file exists, both assertions report `OK:true, Severity:SevInfo, Observed:"not-yet-implemented"` per §12.

---

## Implementation spec

All new files are under `internal/checkpoint/` (16) and `internal/pins/` (2), plus exactly one new file elsewhere (`internal/daemon/wire_checkpoint.go`), one modified file elsewhere (`internal/cli/hook_checkpoint.go`), and one conditional file (`internal/core/hash_json.go`, added only if SP-01 did not already give `core.Hash` JSON methods — see §3). Nineteen new files in total. Package `checkpoint` may import `store dag negknow pins grammar tokens` and the foundation (`core paths config logging obs`) — **it may not import `hookio`, `scheduler`, `ipc` or `daemon`** (§3.2). That is why `PreCompactResult` returns plain data and the daemon composition root converts it to `hookio.Output`.

**`nomagic` (§11.6) in this package.** Every value that Appendix C owns is read from `config.CheckpointCfg` and never written as a literal: the 12 000-token budget comes from `cfg.Checkpoint.BudgetTokens`, the 20 000-token residual ceiling from `cfg.Checkpoint.Frontier.MaxResidualTokens`, the tier membership from `cfg.Checkpoint.Tiers`, and the 20 s `PreCompact` timeout from `PreCompactInput.HookTimeout`. Two rune caps in this spec collide with the forbidden literal set by coincidence rather than by meaning — the `120`-rune cap on `FilePointer.Why` and on a dropped open question's `Detail`, which have nothing to do with `scheduler.idle.detectAfterSeconds: 120`. Both are written as
`const pointerWhyMaxRunes = 120 //nomagic:allow one-line pointer reason cap; unrelated to scheduler.idle.detectAfterSeconds`
and used through the constant. No other literal in `internal/checkpoint` or `internal/pins` appears in the §11.6 set.

### 1. `internal/pins/pins.go` — Invariant type and ID minting

```go
package pins

type Invariant struct {
    ID     string         `json:"id"`
    Text   string         `json:"text"`
    Source string         `json:"source"` // "user" | "agent" | "decision"
    Pinned core.UnixMilli `json:"pinned"`
}

func MintID(text string) string {
    h := core.HashBytes("qompack.pin", []byte(normalizeWS(text)))
    return "inv_" + hex.EncodeToString(h[:])[:12]
}
// normalizeWS collapses every run of unicode.IsSpace to a single U+0020 and trims both ends.
func normalizeWS(s string) string
```

Valid `Source` values are exactly `"user"`, `"agent"`, `"decision"`. The empty string is normalized to `"agent"` silently (it is the common "caller did not care" case); any other non-empty value is rewritten to `"agent"` **and** a `Warn` naming the rejected value is logged.

### 2. `internal/pins/store.go` — append-only log + materialized view

- Log: `.qompack/pins/invariants.jsonl`, opened with `paths.AppendOnly`, one compact JSON object per line, `\n`-terminated. Two record shapes, byte-for-byte:
  - `{"op":"add","id":"inv_ab12cd34ef56","text":"never edit generated/ by hand","source":"user","pinned":1754902951000}`
  - `{"op":"del","id":"inv_ab12cd34ef56","pinned":1754903001000}`
- `Add`: if `inv.ID == ""` set `inv.ID = MintID(inv.Text)`; if `inv.Pinned == 0` set from `clk.Now().UnixMilli()`. If the id is already **live** (added and not tombstoned), return `nil` without appending — `Add` is idempotent. Empty/whitespace-only `Text` returns `fmt.Errorf("pins: empty invariant text")`. Text longer than 2000 bytes is truncated at the last rune boundary ≤ 2000 and a `Warn` logged.
- `Remove`: if the id is not live, return `core.ErrNotFound`. Otherwise append a `del` record. **Never rewrites the log.**
- `All`: replay the log start-to-end into an ordered map (`map[string]Invariant` + insertion-order slice); `add` inserts or refreshes, `del` deletes. Returns live invariants sorted by `Pinned` ascending, tiebreak `ID` ascending. A malformed line is skipped, counted with `m.Counter("pins.badline").Add(1)` on the `obs.Registry` handed to `Open`, and logged once per open at `Warn`; it never aborts the replay.
- `Materialize`: `paths.WriteAtomic(".qompack/pins/invariants.json", json.MarshalIndent(All(), "", "  ")+"\n")`. `invariants.json` is a **derived view** and is the only pins file written non-append-only; `invariants.jsonl` is the truth and is the file `paths.TestAppendOnlyGuard` targets. `Materialize` is called at the end of every successful `Add`/`Remove` and by `checkpoint.FileWriter.Finalize`.
- `Open` creates `.qompack/pins/` with mode `0755`, replays the log once into memory, guards all mutation with a `sync.Mutex`, and returns the unexported `*pinStore` as a `Store`.

### 3. `internal/checkpoint/schema.go` — the §8.5 schema verbatim

Struct field declaration order **is** the JSON serialization order and **is** the importance order of §6.9. A golden test asserts the byte order of top-level keys.

```go
package checkpoint

const SchemaVersion = 1

type Checkpoint struct {
    Version         int                `json:"version"`
    Session         core.SessionID     `json:"session"`
    Seq             core.CheckpointSeq `json:"seq"`
    Created         string             `json:"created"`
    Parent          string             `json:"parent,omitempty"`
    EncodedSegments []core.SegmentID   `json:"encoded_segments"`

    // Tier 1 — never truncated
    Invariants []Invariant      `json:"invariants"`
    UserIntent UserIntent       `json:"user_intent"`
    Eliminated []negknow.Record `json:"eliminated"`

    // Tier 2 — truncate late
    Decisions     []Decision  `json:"decisions"`
    OpenQuestions []string    `json:"open_questions"`
    CurrentWork   CurrentWork `json:"current_work"`

    // Tier 3 — truncate first
    Pointers  Pointers `json:"pointers"`
    Narrative string   `json:"narrative"`

    // Metadata
    SketchRefs map[string]string `json:"sketch_refs"`
    Dropped    []DropEntry       `json:"dropped"`
    Cache      CacheInfo         `json:"cache"`
}
type CurrentWork struct {
    Goal      string  `json:"goal"`
    NextStep  string  `json:"next_step"`
    BlockedOn *string `json:"blocked_on"`   // explicit null, never omitted (§8.5 shows null)
}
```

`core.Hash` marshals as `"sha256:<hex>"` (its `String()` form) — `Hash` gets `MarshalJSON`/`UnmarshalJSON` in `core` only if SP-01 did not already ship them; if it did not, this subplan adds them in `internal/core/hash_json.go` (an additive change to a foundation type, permitted because it changes no signature in §5).

`SketchRefs` is always exactly `{"tried":"tried.bloom","touch":"touch.cms"}` — the two keys §8.5 shows, no more.

**`eliminated[]` is a documented superset, not drift.** §5.14 fixes the field as `[]negknow.Record`, and `negknow.Record` (§5.10) serializes `id`, `session`, `ts`, `descriptor`, `stale_since`, `stale_because` and `source` in addition to the seven keys §8.5 illustrates (`target`, `approach`, `reason`, `evidence`, `depends_on`, `scope`, `status`). Every §8.5 key is present with the §8.5 spelling and semantics; the extra keys are SP-09's record identity and staleness provenance, which the rehydrator and `already_tried` need. A golden test (`TestEliminatedCarriesEverySection85Key`) asserts the seven §8.5 keys are all present on each entry of `0002-full.json`, so the superset can never become a subset.

`func (c *Checkpoint) ensureNonNil()` initializes every nil slice to an empty slice and `SketchRefs` to the constant map, so `encoded_segments`, `invariants`, `eliminated`, `decisions`, `open_questions`, `dropped`, `pointers.files`, `pointers.tools` serialize as `[]`, never `null`. Called by `Marshal`.

`func Marshal(c Checkpoint) ([]byte, error)` uses a `json.Encoder` with `SetEscapeHTML(false)` and `SetIndent("", "  ")`, appends a trailing `\n`. Byte-identical output for identical input is a property test.

`func Filename(seq core.CheckpointSeq) string` → `fmt.Sprintf("%04d.json", seq)`; seq > 9999 keeps growing (`%04d` widens naturally). `func CreatedNow(clk core.Clock) string` → `clk.Now().UTC().Format("2006-01-02T15:04:05.000Z")`.

### 4. `internal/checkpoint/migrate.go` — the versioned migration hook

```go
type migration func(raw []byte) ([]byte, error)
var migrations = map[int]migration{}  // key = version being migrated FROM; empty at v1

// Migrate reads only {"version":N}, then applies migrations[N], migrations[N+1], … until the
// document is at SchemaVersion. It never decodes the full document with the current struct
// before migrating.
func Migrate(raw []byte) ([]byte, error)
```

Failure modes: `version` absent or not a number → `fmt.Errorf("checkpoint: missing version: %w", core.ErrContract)`; `version < 1` → same; `version > SchemaVersion` → `fmt.Errorf("checkpoint: version %d written by a newer plugin (max %d): %w", v, SchemaVersion, core.ErrContract)`; a version in `(1, SchemaVersion)` with no registered migration → `core.ErrContract`. `Unmarshal(raw []byte) (Checkpoint, error)` calls `Migrate` first, then `json.Unmarshal` with `DisallowUnknownFields` **off** (forward compatibility: unknown fields are dropped on read and a `Warn` is logged naming them).

### 5. `internal/checkpoint/inject.go` — injection tagging (GC regeneration rule)

```go
const InjectionOpenTag  = "<!-- qompack:injected seq=%d ver=%d -->"
const InjectionCloseTag = "<!-- /qompack:injected -->"
func OpenTag(seq core.CheckpointSeq) string // fmt.Sprintf(InjectionOpenTag, seq, SchemaVersion)
func StripInjections(s string) string
func StripInjectionsCount(s string) (string, int)
```

`StripInjectionsCount` runs three passes in order:

1. **Paired blocks.** `regexp.MustCompile(`(?s)<!--\s*qompack:injected\s+seq=\d+\s+ver=\d+\s*-->.*?<!--\s*/qompack:injected\s*-->`)` — replaced with `""`, one count per match.
2. **Unmatched open tag** (the summarizer truncated or mangled the close tag). `regexp.MustCompile(`(?s)<!--\s*qompack:injected\s+seq=\d+\s+ver=\d+\s*-->.*?(\n\n|$)`)` — replaced with the captured terminator, so stripping stops at the paragraph break rather than eating the rest of the document.
3. **Mangled residue.** `regexp.MustCompile(`(?m)^[ \t]*(<!--[ \t]*)?/?qompack:injected\b.*$\n?`)` — removed, catching the case where the summarizer reproduced the tag text without the comment syntax.

Result is `strings.TrimSpace`d only if the input was non-empty and the result is whitespace-only (returns `""`). Every string read out of the store on the way into a checkpoint goes through the package-level helper

```go
func fromStore(b []byte) string {
    s, n := StripInjectionsCount(string(b))
    if n > 0 { pkgMetrics().Counter("checkpoint.injection_stripped").Add(int64(n)) }
    return s
}
```

### 5b. `internal/checkpoint/obs.go` — observers for the receiver-less functions

§5.14 fixes `ExtractDecisions`, `Truncate`, `ValidatePointers` and `StripInjectionsCount` as **package-level functions with no logger or registry parameter**, yet this spec requires them to emit counters and `Warn` logs. Rather than amend §5.14 or smuggle a text-carrying field into `SourceSet`, the package keeps one set of observers:

```go
var (
    obsMu  sync.RWMutex
    curLog logging.Logger = logging.Nop()
    curReg obs.Registry   = nopRegistry{}
)
// SetObservers is called once by OpenWriter. Safe to call repeatedly; last write wins.
func SetObservers(log logging.Logger, m obs.Registry)
func pkgLog() logging.Logger
func pkgMetrics() obs.Registry

// nopRegistry implements obs.Registry with discard Hist/Counter/Gauge, an empty Snapshot and a
// nil BudgetBreach slice. It is the default so the package is usable — and unit-testable —
// with no writer constructed.
type nopRegistry struct{}
```

`TestPackageFunctionsWorkWithoutObservers` calls `ExtractDecisions`, `Truncate` and `ValidatePointers` in a fresh process-equivalent state (observers never set) and requires no panic and no nil dereference. Every counter named elsewhere in this spec (`checkpoint.injection_stripped`, `checkpoint.decision_read_error`, `checkpoint.manifest_mismatch`, `checkpoint.cold_precompact`, `checkpoint.manifest_badline`) is read through `pkgMetrics()` or, inside `*FileWriter` methods, through `w.m` — which `OpenWriter` has already passed to `SetObservers`, so the two agree.

### 6. `internal/checkpoint/source.go` — the SourceSet that cannot carry text

```go
type SourceSet struct {
    Store    store.Store
    Segments store.SegmentLog
    Ledger   negknow.Ledger
    Pins     pins.Store
    Graph    dag.Graph
    Grammar  grammar.Sequitur
    Tokens   tokens.Estimator
}
func (s SourceSet) Validate() error  // every field non-nil, else fmt.Errorf naming the field
```

Every field is an **interface**. `TestSourceSetCarriesNoText` uses `reflect.TypeOf(SourceSet{})` and requires `Field(i).Type.Kind() == reflect.Interface` for all fields — a `string`, `[]byte`, or struct field would fail the build's test gate. This is the compile/test-time expression of the §8.5 regeneration rule: there is no field into which live context text could be passed.

### 7. `internal/checkpoint/draft.go` — the incremental draft

```go
type Draft struct {
    mu       sync.Mutex
    session  core.SessionID
    seq      core.CheckpointSeq
    parent   core.CheckpointSeq
    cp       Checkpoint
    encoded  map[core.SegmentID]bool
    frontier core.TurnIndex
    src      SourceSet
    started  time.Time
    dirty    bool
    workExplicit bool                  // SetCurrentWork was called; stop deriving
}
func (d *Draft) Seq() core.CheckpointSeq
func (d *Draft) Frontier() core.TurnIndex
func (d *Draft) SetCache(c CacheInfo)
func (d *Draft) AddDrops(e ...DropEntry)
func (d *Draft) SetCurrentWork(w CurrentWork)   // overrides whatever Advance derived
func (d *Draft) SetOpenQuestions(q []string)    // replaces the list
func (d *Draft) AddOpenQuestion(q string)       // appends, deduped by exact string, cap 32
func (d *Draft) snapshot() Checkpoint     // deep copy under the mutex
```

`workExplicit bool` records whether `SetCurrentWork` was called; once it is true, `Advance` stops deriving `CurrentWork` and leaves the caller's value alone. `SetOpenQuestions`/`AddOpenQuestion` are additive on top of the derived list and are how SP-12 and SP-14 contribute later without editing this package.

**Persistence.** After every mutating call the draft is written to `.qompack/state/draft-<session>.json` with `paths.WriteAtomic` (this is `state/`, not `checkpoints/`, so atomic replacement is legal):

```json
{"session":"…","seq":7,"parent":6,"frontier":58,"encoded":[12,13,14],"started":1754902000000,"checkpoint":{…}}
```

`Begin` first tries to load this file; if it exists, matches the session, and its `seq` is still unclaimed in `MANIFEST.jsonl`, the draft is resumed (this is what makes daemon restarts free). If it exists but its `seq` is claimed, the file is renamed to `draft-<session>.stale.json`, a `Warn` is logged, and a fresh draft is begun.

### 8. `internal/checkpoint/writer.go` — `Begin`, `Advance`, `Abort`

```go
type FileWriter struct {
    root   string; cfg config.Config; log logging.Logger
    m      obs.Registry; clk core.Clock
    reader Reader                       // built by OpenWriter over the same root
    mu     sync.Mutex; drafts map[core.SessionID]*Draft
}
func OpenWriter(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) (*FileWriter, error)

// relPath renders an absolute checkpoint path as the project-relative, forward-slash form
// ".qompack/checkpoints/0007.json" used in the O1 focus paragraph.
func (w *FileWriter) relPath(abs string) string
```

The token estimator is never a `FileWriter` field: it is always `d.src.Tokens`, taken from the `SourceSet` handed to `Begin`. That keeps the "everything comes from the sources" rule literal.

Throughout `Begin`, `Advance`, `Finalize` and `ExtractDecisions`, the identifier `src` denotes the draft's own `SourceSet` (`d.src`) — the writer holds no store, ledger, graph or estimator of its own.

**`Begin(ctx, s, parent, src)`** — steps, in order:

1. `src.Validate()`; error propagates.
2. Resume-or-create the draft from `state/draft-<session>.json` (§7 above).
3. `seq = maxSeqInManifest() + 1` (0 → 1 for the first checkpoint of a project). `parent` argument overrides the recorded parent when non-zero.
4. Seed tier 1 that does not depend on segments:
   - `Invariants`: `src.Pins.All(ctx)`, verbatim, sorted as `pins.All` returns them.
   - `UserIntent.Original`: if `parent != 0`, copied from `Reader.Get(parent).UserIntent.Original` (the parent chain is how the verbatim original survives arbitrarily many checkpoints). If `parent == 0`, taken from the **first** `UserPrompt` node in the DAG for this session (`dag.KindUserPrompt`, lowest `Turn`), its text read from `src.Store.Open(node.Root)` and passed through `fromStore`. If neither exists, `""`.
   - `Eliminated`: `src.Ledger.All(ctx)` filtered to records whose `Session == s` **or** `Scope == "project"`, copied verbatim (both `active` and `stale`, because §8.5's `status` field exists precisely to carry the distinction).
   - `SketchRefs`, `Version`, `Session`, `Seq`, `Parent` (as `Filename(parent)`, `""` when `parent == 0`), `Cache = CacheInfo{TTLState:"unknown"}`.
5. `frontier = src.Segments.Frontier(ctx, s)`.
6. Persist and return.

**`Advance(ctx, d, segs)`** — the O5 incremental encoder. For each id in `segs`, in ascending id order:

1. `seg, err := src.Segments.Get(ctx, id)`. If `!seg.Closed` → skip with a `Debug` log (only closed segments may be encoded; SP-12 closes them).
2. If `seg.EncodedOnce && seg.CheckpointSeq != d.seq` → the DPI guard already fired for this segment; skip it and record it in `skipped`.
3. Encode from **originals only**:
   - **Intent evolution.** Every `dag.KindUserPrompt` node with `seg.StartTurn ≤ Turn ≤ seg.EndTurn`, text via `src.Store.Open(node.Root)` → `fromStore` → appended verbatim to `UserIntent.Evolution` (deduped by exact string; capped at 64 entries, oldest kept — intent history is tier 1).
   - **Eliminations.** Re-read `src.Ledger.All(ctx)` and merge by `Record.ID`, so status flips that happened since `Begin` are reflected.
   - **Decisions.** `ExtractDecisions(ctx, src, seg.StartTurn)`; merged into `cp.Decisions` by `Decision.ID`, first occurrence wins (lowest turn). After the merge the slice is sorted **`Turn` descending, tiebreak `ID` ascending** and capped at `maxExtractedDecisions` (64), keeping the head. Newest-first is the tail-first cut order `Truncate` relies on; the slice-score ranking inside `ExtractDecisions` decides *which* 64 survive a single extraction pass, not the serialized order.
   - **Pointers — files.** For every `dag.KindFile` node in the segment's turn range, take `paths.Key(node.Ref)`, look up `src.Store.FileHistory(ctx, path)` and use the **latest** version's `Root` as the pointer hash. `Why` is a single line, ≤ `pointerWhyMaxRunes` (120), built as `fmt.Sprintf("touched at turn %d via %s", turn, tool)` where `tool` is the tool name of the tool-use node that produced the file edge, or `"referenced"` when unknown. Dedup by path, keep the highest turn. **No file bytes are read into the checkpoint.**
   - **Pointers — tools.** For every `dag.KindToolUse` node in range whose store record has `Status != StatusSuperseded` and `Ephemeral == false`, emit `{tool_use_id, hash: rec.Root, summary: rec.ArgsPreview}` (`ArgsPreview` is already capped at 120 chars by SP-06). Superseded and ephemeral results are excluded — §8.1 item 3 says superseded reads "should never appear in a summary", and §8.7 makes retrieval results the first eviction candidate.
   - **Pointer ordering — normative, because `Truncate` cuts tail-first.** Both pointer slices are kept in **descending turn order** with no sort pass and no extra persisted state: segments are encoded in ascending id (therefore ascending turn) order, so each new pointer is **prepended** to its slice. Re-encountering a path or tool-use id removes the existing entry first and prepends the newer one, which is what "dedup by path, keep the highest turn" means operationally. Result: index 0 is the newest pointer and the tail is the oldest, so `Truncate`'s tail-first cut removes the least recently touched material first. `TestAdvanceKeepsPointersNewestFirst` asserts the invariant after three `Advance` calls.
   - **Open questions.** For every elimination record merged above whose `Status == "stale"`, append `fmt.Sprintf("re-verify %q for %s — evidence changed (%s)", r.Approach, r.Target, strings.Join(r.StaleBecause, ", "))` to `cp.OpenQuestions`, deduped by exact string, capped at 32 entries (newest dropped once full). This is §8.3's "previously eliminated, but the evidence has changed since — re-verification may be warranted" rendered as a tier-2 question rather than lost. Entries added by `SetOpenQuestions`/`AddOpenQuestion` are preserved and always sort first.
   - **Current work.** Skipped entirely when `d.workExplicit`. Otherwise `CurrentWork.Goal` = the first sentence (same splitter as §9, capped at 160 runes) of the **most recent** `dag.KindUserPrompt` node at or before `seg.EndTurn`, read through `fromStore`; `NextStep` = `""`; `BlockedOn` = `nil`. An empty `NextStep` is a valid checkpoint — the schema requires the key, not a value — and SP-12 (which owns todo state) and SP-14's `/qompack:checkpoint` supply the richer value through `SetCurrentWork`. Do not invent a next step from tool history: a fabricated next step is exactly the drift this layer exists to eliminate.
   - **Narrative.** One line per segment appended to `cp.Narrative`, exactly: `fmt.Sprintf("seg %d turns %d-%d: %d tool uses over %d files; %s\n", seg.ID, seg.StartTurn, seg.EndTurn, nTools, nFiles, featureSummary(seg.Features))` where `featureSummary` renders the map's keys sorted ascending as `k=%.2f` joined by `", "`. SP-15 appends its grammar section to the same field from `checkpoint/grammar.go`; it never rewrites these lines.
4. `src.Segments.MarkEncoded(ctx, encodable, d.seq)`. If it returns `core.ErrAlreadyEncoded`, the whole `Advance` returns that error unchanged after persisting the draft — the caller (idle worker) logs `Loud` and drops those ids. Nothing partial is rolled back: the draft is a superset, and the manifest is written only at `Finalize`.
5. `d.frontier = max(d.frontier, maxEndTurn(encoded))`; `d.cp.EncodedSegments = sorted(union)`; persist; return `d.frontier`.

**`Abort(d)`** deletes `state/draft-<session>.json`, drops the in-memory draft, and returns nil (idempotent — a missing file is not an error). It does **not** un-mark encoded segments: those segments are legitimately encoded into a draft that will be re-`Begin`ned with the same seq, and `MarkEncoded` is idempotent for the same seq.

**Performance.** `Advance` over one segment holding 40 tool uses and 12 files: < 25 ms (benchmark `BenchmarkAdvanceSegment`). It runs only during idle windows, never on the hot path.

### 9. `internal/checkpoint/decisions.go` — `ExtractDecisions`

```go
const maxExtractedDecisions = 64
func MintDecisionID(what, why string, evidence core.Hash) core.DecisionID {
    h := core.HashBytes("qompack.decision",
        []byte(normalizeWS(what)+"\x00"+normalizeWS(why)+"\x00"+evidence.String()))
    return core.DecisionID("dec_" + hex.EncodeToString(h[:])[:12])
}
func ExtractDecisions(ctx context.Context, src SourceSet, from core.TurnIndex) ([]Decision, error)
```

Three sources, merged by `Decision.ID` (first wins, lowest `Turn` first):

**(a) `EdgeExplains` chains in the DAG.** For every edge `e` with `e.Kind == dag.EdgeExplains` and `e.Turn >= from`: let `expl = src.Graph.Node(e.From)` (the explaining assistant or tool-result node) and `tgt = src.Graph.Node(e.To)`. Read `expl`'s text from `src.Store.Open(expl.Root)` capped at 32 KiB, run `fromStore`. Then:
- `what` = the first sentence of `tgt`'s text if `tgt.Kind == dag.KindDecision`, else `fmt.Sprintf("%s %s", verbFor(tgt.Kind), tgt.Ref)` where `verbFor` maps `KindFile→"changed"`, `KindToolUse→"ran"`, `KindSymbol→"modified"`, everything else `"decided about"`. Capped at 160 runes at a rune boundary.
- `why` = the first two sentences of the explaining text, capped at 400 runes at a rune boundary. Sentence split: the first occurrence of `. `, `.\n`, `! `, `? ` or end-of-string, scanning forward.
- `alternatives_rejected` = `nil`.
- `evidence` = `expl.Root`; `turn` = `e.Turn`.
Chains are followed one hop only (`EdgeExplains` from `expl` to a further node is a separate decision) — this is thin slicing's tradeoff applied to decisions and keeps the pass linear in edges.

**(b) Elimination alternatives.** For every `negknow.Record` from `src.Ledger.All(ctx)` whose `TS` maps to a turn `>= from` (turn taken from the DAG node `elimination:<record.ID>` when present, else `from`), and whose `Approach != ""`:
- `what` = `fmt.Sprintf("rejected %q for %s", r.Approach, r.Target)`
- `why` = `r.Reason`
- `alternatives_rejected` = `[]string{r.Approach}`
- `evidence` = `r.Evidence`

**(c) Explicit records.** Every `pins.Invariant` from `src.Pins.All(ctx)` with `Source == "decision"`:
- `what` = the text before the first `" because "`, or the whole text when absent
- `why` = the text after the first `" because "`, or `"pinned by the user as a standing decision"` when absent
- `alternatives_rejected` = `nil`; `evidence` = `core.Hash{}`; `turn` = `from`

**Ranking.** After merge, run `src.Graph.BackwardSlice(criteria, dag.SliceOptions{Thin: true, Decay: 0.85, MaxNodes: 5000, Deadline: 250*time.Millisecond})` where `criteria` is the most recent `KindUserPrompt` node plus the current segment node (`segment:<id>` from `src.Segments.Current`). Score each decision by `slice.Scores["decision:"+string(d.ID)]`, defaulting to `0`. Sort descending by score, tiebreak descending by `Turn` (recent first), tiebreak ascending by `ID` for total determinism. Truncate to `maxExtractedDecisions`.

**DAG emission.** For every decision in the returned set, emit (idempotently — `AddNode`/`AddEdge` on an existing id is a no-op by SP-07's contract):

```go
src.Graph.AddNode(dag.Node{
    ID: dag.NodeID("decision:" + string(d.ID)), Kind: dag.KindDecision,
    Turn: d.Turn, TS: nowMilli, Ref: string(d.ID), Root: d.Evidence,
    Tokens: src.Tokens.EstimateString(d.What+" "+d.Why, tokens.ClassProse),
})
src.Graph.AddEdge(dag.Edge{From: evidenceNodeID, To: dag.NodeID("decision:"+string(d.ID)),
    Kind: dag.EdgeExplains, Weight: 1, Turn: d.Turn})
```

`evidenceNodeID` is the explaining node for source (a), `dag.NodeID("elimination:"+r.ID)` for (b), and omitted for (c). Errors from `AddNode`/`AddEdge` are logged at `Warn` and do not fail the extraction — decisions are still returned.

**Errors.** A store read failure for one explaining node skips that decision and increments `obs.Counter("checkpoint.decision_read_error")`; only a `SourceSet.Validate()` failure or a `ctx` cancellation returns an error.

**Performance.** `BenchmarkExtractDecisions` over a 5 000-node / 12 000-edge DAG with 800 `EdgeExplains`: < 20 ms.

### 10. `internal/checkpoint/truncate.go` — importance ordering (§6.9)

```go
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)
```

Size measurement: `sizeOf(c)` marshals with `b, _ := Marshal(c)` (a marshal error is impossible for a well-formed `Checkpoint` and yields `sizeOf == 0`, which fits any budget and therefore cuts nothing) and returns `est.Estimate(b, tokens.ClassJSON)`. Because `Marshal` is cheap relative to the budget check, `Truncate` re-measures after every cut group, not after every element, using a doubling backoff: cut `1, 2, 4, 8, …` elements between measurements, then binary-search back to the smallest cut that fits.

Membership comes from `t` (`Never`/`Late`/`First`), whose field names are exactly the JSON keys `invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`, `pointers`, `narrative`. A field listed in `t.Never` is never cut regardless of the order below. The **within-tier cut order is fixed in code** (config decides membership, not order):

```
tier3CutOrder = narrative
              → pointers.tools   (tail-first, one at a time)
              → pointers.files   (tail-first, one at a time)
tier2CutOrder = open_questions   (tail-first)
              → decisions[].alternatives_rejected  (emptied, all decisions, in one step)
              → decisions        (tail-first)
              → current_work.next_step → ""        (last tier-2 cut; goal and blocked_on kept)
```

Tail-first is lowest-value-first because `Advance` keeps both pointer slices and the decision slice in **descending turn order** (spec §8, "Pointer ordering" and "Decisions"): index 0 is the newest, the tail is the oldest, and recency is the only ordering signal available at truncation time that does not require re-running a slice. `TestTruncateDropsPointersTailFirst` and `TestAdvanceKeepsPointersNewestFirst` together pin this contract; changing the ordering in `Advance` without changing the cut direction here is a correctness bug, not a style choice.

Every cut appends a `DropEntry`:

| Cut | `Kind` | `ID` | `Detail` |
|---|---|---|---|
| narrative | `"narrative"` | `"narrative"` | `"prose residue dropped at budget"` |
| a tool pointer | `"tool_pointer"` | the `tool_use_id` | `"truncated at budget; expand(hash) still resolves"` |
| a file pointer | `"file_pointer"` | the path | `"truncated at budget; re_read(path) still resolves"` |
| open question | `"open_question"` | `fmt.Sprintf("oq_%d", i)` | the question text, capped at `pointerWhyMaxRunes` (120) runes |
| alternatives | `"alternatives"` | the decision id | `"alternatives_rejected emptied at budget"` |
| a decision | `"decision"` | the decision id | `"truncated at budget; why(decision_id) still resolves"` |
| next step | `"next_step"` | `"current_work.next_step"` | `"truncated at budget"` |

If tier 3 and tier 2 are exhausted and the document still exceeds `budget`, `Truncate` returns the tier-1-only checkpoint plus `DropEntry{Kind:"budget_exceeded", ID:"tier1", Detail:fmt.Sprintf("tier 1 is %d tokens against a %d budget; written in full per §8.5", sizeOf(c), budget)}`. **Tier 1 is never truncated and `Truncate` never returns an error** — a checkpoint always gets written (this is what closes G7.4: the session survives even when everything else has given up).

`budget <= 0` means unlimited: `Truncate` returns the input unchanged with a nil drop list.

### 11. `internal/checkpoint/validate.go` + `gitindex.go` — ground truth (G2.5)

```go
func ValidatePointers(ctx context.Context, root string, p Pointers) ([]DropEntry, error)
```

Package `checkpoint` **must not use `os/exec`** (the `security` CI job restricts it to `daemon`, `cli`, `tools/`), so "checked against `git status`" is implemented by reading git's own on-disk state in pure Go. Three checks per file pointer:

1. **Existence / type.** `paths.Norm(root, ptr.Path)`; a normalization error (escape above root) → `DropEntry{Kind:"pointer_invalid", ID: ptr.Path, Detail:"path escapes the project root"}`. Then `os.Lstat`. `os.IsNotExist` → `DropEntry{Kind:"pointer_missing", ID: ptr.Path, Detail:"file no longer exists in the working tree"}`. A directory → `DropEntry{Kind:"pointer_invalid", …, Detail:"path is a directory"}`.
2. **Working-tree drift vs the git index.** Compared against the parsed `.git/index` entry: if `entry.Size != stat.Size()` or `entry.MTimeSec != uint32(stat.ModTime().Unix())` → `DropEntry{Kind:"pointer_dirty", ID: ptr.Path, Detail: fmt.Sprintf("modified since index on %s", branch)}`. Present in the working tree but absent from the index → `DropEntry{Kind:"pointer_untracked", ID: ptr.Path, Detail:"not tracked by git"}`. `pointer_dirty` and `pointer_untracked` are **informational**: the pointer is kept, because a dirty file is exactly the file the agent is working on.
3. **Callers remove only** `pointer_missing` and `pointer_invalid` pointers. `FileWriter.Finalize` implements that removal.

`gitindex.go` — pure-Go reader, no dependency:

- Resolve the git dir: `<root>/.git`; if it is a **file**, read it, require the prefix `gitdir: `, and use the (possibly relative) path that follows, cleaned and joined against `root`. If `.git` is absent → return `(nil, errNoGitDir)`.
- `HEAD`: read `<gitdir>/HEAD`; if it starts with `ref: refs/heads/`, the branch is the remainder trimmed of whitespace; otherwise the branch string is `"detached@" + first 12 chars`.
- `index`: read `<gitdir>/index`. Header: 4-byte magic `DIRC`, `uint32` version (big-endian), `uint32` entry count. Supported versions: **2 and 3**. Each entry, all big-endian: `ctime_sec, ctime_nsec, mtime_sec, mtime_nsec, dev, ino, mode, uid, gid, size` (10 × `uint32` = 40 bytes), a 20-byte SHA-1 object id, a `uint16` flags word; when version == 3 and `flags & 0x4000 != 0`, an additional `uint16` of extended flags. Then the path bytes, NUL-terminated, followed by 1–8 NUL bytes of padding so the total entry length from the entry start is a multiple of 8. Path length is `flags & 0x0FFF`, and when that equals `0x0FFF` the path is NUL-terminated and read to the terminator instead.
- Version 4 (path prefix compression), an unknown magic, a short read, or an entry count that overruns the buffer → return `errIndexUnsupported`. `ValidatePointers` then performs check 1 only and appends one `DropEntry{Kind:"pointer_git_unavailable", ID:"", Detail:"<reason>"}` so the condition is visible rather than silent.
- Index keys are stored under `paths.Key` so case-insensitive filesystems match (§4).
- The whole index is read once per `ValidatePointers` call and capped at 64 MiB; beyond that, `errIndexUnsupported`.

`ValidatePointers` returns an error only when `ctx` is cancelled. Everything else is reported as `DropEntry`s.

Tool pointers are validated by `Finalize` instead, where the store is in scope: `!src.Store.Has(ptr.Hash)` → `DropEntry{Kind:"pointer_unresolvable", ID: string(ptr.ToolUseID), Detail:"object missing from the store (collected?)"}` and the pointer is removed.

### 12. `internal/checkpoint/manifest.go` + `reader.go`

**`checkpoints/MANIFEST.jsonl`** — one compact JSON object per line, keys in this order, `\n`-terminated, written with `paths.AppendOnly`:

```
{"seq":7,"sha256":"sha256:9f2c…","bytes":4821,"created":"2026-08-11T10:22:31.145Z"}
```

`created` is written with the `CreatedNow` layout (`2006-01-02T15:04:05.000Z`) and parsed back with the same layout into `Ref.Created` (`core.UnixMilli`); an unparseable `created` yields `Ref.Created == 0` and counts as a malformed line for the counter but does not drop the entry, because `seq`/`sha256`/`bytes` are what verification needs.

`func appendManifest(root string, r Ref) error`; `func readManifest(root string) ([]Ref, error)` (malformed lines skipped, counted in `checkpoint.manifest_badline`, logged once per call at `Warn`, replay continues); `func maxSeq(root string) core.CheckpointSeq`; `func seqFromFilename(name string) (core.CheckpointSeq, error)` parses `"0006.json"` → `6` and is what `Chain` uses to follow `Checkpoint.Parent`.

**`Reader`** — `OpenReader` returns the unexported `*fileReader`, which holds `root`, `log` and `m`:
- `List` → `readManifest`, sorted ascending by seq. A `Ref.Path` is the absolute path of `checkpoints/<Filename(seq)>`. `Ref.Tokens` and `Ref.Frontier` are zero (writer-only fields, see the Interface contract).
- `Get(seq)` → read the file, `sha256` the raw bytes, compare against the manifest entry. Mismatch or missing file → `logging.Loud("checkpoint manifest mismatch", "seq", seq, "expected", want, "observed", got)`, `obs.Counter("checkpoint.manifest_mismatch").Add(1)`, return `fmt.Errorf("checkpoint %04d: %w", seq, core.ErrContract)`. On success run `Unmarshal` (which runs `Migrate`).
- `Latest(s)` → walk the manifest descending; return the first checkpoint that verifies **and** whose `Session == s`; if none matches the session, return the newest that verifies from any session (a resumed session legitimately inherits the project's checkpoint chain) with `Ref.Seq` set. On a mismatch the walk continues to the parent — that is §12's "refuse to use the affected checkpoint, fall back to its parent". If nothing verifies, return `core.ErrNotFound`. `Latest` never degrades the mode itself; it returns the error and the daemon wiring calls `contract.Monitor.Degrade`.
- `Chain(seq)` → follow `Parent` filenames from `seq` down to the root, returning **oldest-first**. A cycle (a parent seq ≥ the child's) aborts with `core.ErrContract`; depth is capped at 1024.
- `Verify()` → re-hash every manifest entry, return the seqs that mismatch (empty slice, not nil, when clean). This backs `qompack fsck`.

### 13. `internal/checkpoint/finalize.go` — `Finalize` inside budget B-E

```go
func (w *FileWriter) Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error)
```

Steps, in order, each with a deadline check against `ctx`:

1. `cp := d.snapshot()`; `cp.Created = CreatedNow(w.clk)`; `cp.Version = SchemaVersion`; `cp.ensureNonNil()`.
2. **Pointer validation.** `drops, _ := ValidatePointers(ctx, w.root, cp.Pointers)`; remove `pointer_missing` and `pointer_invalid` file pointers; remove tool pointers whose `Hash` fails `src.Store.Has`; append every drop to `cp.Dropped`.
3. **Truncate.** `if budget <= 0 { budget = core.Tokens(w.cfg.Checkpoint.BudgetTokens) }`; `cp, tdrops := Truncate(cp, budget, w.cfg.Checkpoint.Tiers, d.src.Tokens)`; append `tdrops`.
4. **Marshal** → `b`. `h := sha256.Sum256(b)`.
5. **Write immutably.** `f, err := paths.CreateNew(filepath.Join(root,"checkpoints",Filename(seq)))`. On `os.ErrExist`, increment `seq`, update `cp.Seq` and re-marshal, retry — at most 8 times, then return `core.ErrAppendOnly`. Write, `Sync`, `Close`, then set the file read-only (`os.Chmod(p, 0o444)`; on Windows that maps to `FILE_ATTRIBUTE_READONLY`, which is what SP-01's `paths` layer already does for this call).
6. **Append MANIFEST**, then `src.Pins.Materialize(ctx)`.
7. Delete `state/draft-<session>.json`, drop the in-memory draft, and start a **fresh** draft with `parent = seq` so frontier advancement continues immediately after a compaction (this is what makes the next residual span O(delta)).
8. Return `Ref{Seq, Path, SHA256: h, Bytes: int64(len(b)), Tokens: d.src.Tokens.Estimate(b, tokens.ClassJSON), Frontier: d.frontier, Created: w.clk.Now().UnixMilli()}`.

**Failure modes.** A write failure after `CreateNew` removes the partial file and returns the error (no manifest line is appended, so the manifest never references a file that does not verify). A manifest append failure after a successful write logs `Loud`, leaves the checkpoint file in place, and returns the error — `fsck` reconciles by re-hashing orphan files. `ctx` cancellation between steps 2 and 4 falls through to step 3 with the current `cp` (importance ordering means a truncated document is still the best available for its size, §6.9 and §12's PreCompact-timeout row) and completes the write.

**Budget.** `BenchmarkFinalize` over a draft with 40 segments, 64 decisions, 200 file pointers and 200 tool pointers: mean < 50 ms, giving 40× headroom under **B-E (`checkpoint_finalize` p99 < 2 s, §11.3 L4)**.

### 14. `internal/checkpoint/focus.go` — focus instructions (§4.5 + O1)

```go
const SentinelPhrase = "qompack checkpoint"

const standingTemplate = "Encode what a competent engineer with no session history would get wrong. " +
    "Do not restate file contents, directory structure, or command output — those are retrievable. " +
    "Prioritise: intent, decisions and their rationale, approaches eliminated and why, and " +
    "constraints discovered empirically."

const incrementalTemplate = "A durable checkpoint (`%s`) fully covers the session through turn %d, " +
    "including all decisions, eliminations, and file state up to that point. Do not re-summarize " +
    "that material. Summarize only what happened after turn %d: new decisions, new eliminations, " +
    "new intent, current work."

const snippetProhibition = "Do not include code snippets: every file named above is available by " +
    "path and hash through the qompack checkpoint, and a pointer costs roughly 20 tokens where a " +
    "snippet costs roughly 500."

func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string
```

Assembly, joined by `"\n\n"`, in this order:

1. `standingTemplate` — **verbatim §8.5/§4.5**, always emitted.
2. `fmt.Sprintf(incrementalTemplate, o.CheckpointPath, int(o.Frontier), int(o.Frontier))` — emitted only when `o.IncrementalSpan` is true **and** `o.Frontier > 0`. `o.CheckpointPath` is the project-relative path `.qompack/checkpoints/0007.json` (forward slashes on every platform, produced by `paths.Norm`).
3. `snippetProhibition` — emitted when `o.ForbidSnippets` (G3.4). It contains `SentinelPhrase`, which is what SP-05's `precompact.custom_instructions_accepted` assertion searches for in the post-compaction summary. Because the sentinel must survive being restated by the summarizer, it is a natural phrase, not a marker token.

The result is trimmed of trailing whitespace and capped at 4 000 bytes (a `customInstructions` payload larger than that is a sign of a bug; the cap truncates at a paragraph boundary and logs `Warn`).

**Advisory handling is normative.** Nothing in this package or in SP-11 may branch on whether the summarizer honoured either paragraph. The checkpoint on disk is the authoritative record (§8.5, §12 "Summarizer ignores the incremental-span instruction"). `TestNoForbiddenImports` enforces this mechanically: `internal/checkpoint` cannot import `hookio` and therefore has no access to a transcript to check compliance against.

### 15. `internal/checkpoint/precompact.go` — the PreCompact path

```go
type Compactor interface{ PreCompact(ctx context.Context, in PreCompactInput) (PreCompactResult, error) }
func (w *FileWriter) PreCompact(ctx context.Context, in PreCompactInput) (PreCompactResult, error)
```

The finalize-only path, budget **B-E p99 < 2 s**:

1. `start := w.clk.Now()`. Compute the internal deadline in three steps, in this order:
   ```go
   const finalizeGuard   = 400 * time.Millisecond  // leave the caller room to reply
   const maxPreCompact   = 1500 * time.Millisecond // B-E is 2 s p99; stay well inside it
   const minFinalizeWindow = 250 * time.Millisecond
   hard := in.Deadline.Add(-finalizeGuard)
   if h := start.Add(maxPreCompact); h.Before(hard) { hard = h }
   if !hard.After(start.Add(minFinalizeWindow)) { hard = start.Add(minFinalizeWindow) }
   ctx, cancel := context.WithDeadline(ctx, hard)
   ```
   The floor matters: a host that hands us a deadline already inside `finalizeGuard` (or in the past) must still get a written checkpoint, because §12's PreCompact-timeout row says finalize as-is, not give up. With the floor, `Finalize` always has at least 250 ms of non-cancelled context to marshal and write the tier-1 document. `TestPreCompactFinalizesAsIsNearDeadline` exercises exactly this path.
2. Fetch the live draft for the session. If there is none (compaction fired before any idle window — the cold path), `Begin` one and call `Advance` with **only** the closed, unencoded segments that fit the remaining time: encode segments newest-first and stop when `w.clk.Since(start) > 800*time.Millisecond`. `NewDraft` is set true in the result and `obs.Counter("checkpoint.cold_precompact")` is incremented.
3. `d.SetCache(in.Cache)`; `d.AddDrops(in.ExtraDrops...)`; when `in.CurrentWork != nil`, `d.SetCurrentWork(*in.CurrentWork)`; for each `q` in `in.OpenQuestions`, `d.AddOpenQuestion(q)`.
4. `ref, err := w.Finalize(ctx, d, in.Budget)`. If `err != nil`, log `Loud` and return the error — the daemon wiring still returns `hookio.Empty()` and the hook still exits 0.
5. `cp, _, _ := w.reader.Get(ctx, ref.Seq)` — deliberately a re-read, not `d.snapshot()`: it re-hashes the file against the manifest line just appended, so a corrupt or short write is caught here rather than at the next session start. On error, fall back to the pre-write `cp` for instruction generation and log `Loud`. Build `instr := FocusInstructions(cp, ref, FocusOptions{IncrementalSpan: in.Cfg.IncrementalSpanInstruction, Frontier: ref.Frontier, CheckpointPath: w.relPath(ref.Path), ForbidSnippets: true})`. `ForbidSnippets` is **always** true on this path, which is what guarantees `SentinelPhrase` appears in every emitted `customInstructions` payload — SP-05's `precompact.custom_instructions_accepted` assertion depends on that and on nothing else.
6. Write `.qompack/state/precompact.json` (the SP-05 observable, shape given in the Interface contract) with `paths.WriteAtomic`. `timeout_ms` is `in.HookTimeout.Milliseconds()` — the value comes from the daemon, so no literal `20000` appears in `internal/checkpoint` and the `nomagic` pass stays green.
7. Return `PreCompactResult{Ref: ref, Instructions: instr, Drops: cp.Dropped, Truncated: hasTruncationDrop(cp.Dropped), Wall: w.clk.Since(start), NewDraft: …}`, where `hasTruncationDrop` tests membership in the eight truncation kinds listed with `PreCompactResult` in the Interface contract.

`obs.Timed(w.m.Hist("checkpoint.precompact"), …)` wraps the whole call so `/qompack:status` and the bench gate see B-E.

### 16. `internal/daemon/wire_checkpoint.go` — NEW file, composition root

This is the only file this subplan adds to a package it does not own; it adds **no** modification to any existing daemon file, which is exactly what SP-05's extension seams exist for.

```go
func WireCheckpoint(o *Options, w *checkpoint.FileWriter, r checkpoint.Reader, src checkpoint.SourceSet)
```

- Registers `o.Handle(ipc.Op("checkpoint"), handleCheckpoint)`. The handler reads `req.Event` (a `hookio.Event` with `HookEventName == "PreCompact"`, `Trigger ∈ {manual, auto}`) and builds `checkpoint.PreCompactInput`:
  - `HookTimeout` = the `PreCompact` timeout declared in `internal/pluginmanifest` (20 s today). The daemon is a composition root and may import `pluginmanifest`; `internal/checkpoint` may not, which is why the value is passed in rather than read there.
  - `Deadline` = `now + HookTimeout − 4 s`. Two seconds of that margin cover the client's own 18 s wait (spec §17) and two cover host jitter, so the daemon always answers before the client gives up and the client always answers before Claude Code's 20 s timeout.
  - `Cache` from `scheduler.Runtime`'s last `Decision` when `Services.Sched != nil` (`PChosen`, `RewriteTokens`, `TTLState` from `Decision.P.Pos`, `Decision.Breakdown["rewrite"]` and `Decision.TTL`), else `CacheInfo{TTLState:"unknown"}` — SP-12 is a same-wave sibling and may not be merged yet, so the nil branch is the one that must work first.
  - `Budget` = `core.Tokens(cfg.Checkpoint.BudgetTokens)`; `Cfg` = `cfg.Checkpoint`; `CurrentWork`/`OpenQuestions`/`ExtraDrops` nil in this wave (SP-11 and SP-14 fill them later through the same struct).

  It then calls `PreCompact` and returns

```go
ipc.Response{OK: true, Output: &hookio.Output{
    HookSpecificOutput: &hookio.HSO{HookEventName: "PreCompact", CustomInstructions: res.Instructions},
}}
```

  In `contract.ModeDegradedPassive` the handler still writes the checkpoint (the store must stay correct and the artifact is durable state, not an action on the context window) but returns `Output: nil` — no `customInstructions` is emitted, per §12.1's "everything that *acts* is off".
- Registers idle work: `o.Idle().Register("advance_frontier", 10, advanceAllSessions)` — for every live session, `segs := src.Segments.Unencoded(ctx, s)` filtered to `Closed`, then `w.Advance(ctx, draft, ids)`. `core.ErrAlreadyEncoded` is logged `Loud` and the ids are skipped. Registration is skipped when `cfg.Checkpoint.Frontier.AdvanceOnSegmentClose` is false. Frontier advancement **continues in `ModeDegradedPassive`**: it writes only `state/draft-*.json` and never touches the context window, and §12.1 explicitly keeps L1-correctness work running.
- Registers `o.Idle().Register("checkpoint_cadence", 20, finalizeIfDue)` — this is the second half of §8.5's trigger clause, *"and independently on the scheduler's own cadence, so checkpoints exist even when compaction does not fire."* For each live session with an open draft, `finalizeIfDue` calls `Finalize` when **either** of these holds, and does nothing otherwise:
  1. the draft's snapshot already estimates at or above `cfg.Checkpoint.BudgetTokens` (12 000 by default) — the draft is full, so seal it rather than let it grow past what a checkpoint may cost; or
  2. at least 8 segments have been encoded into the draft since `Begin`.

  Both are computed from state this package already holds, so the cadence does not depend on SP-12. Unlike the `PreCompact` path, this one **is** scheduler-initiated, so it is **disabled in `ModeDegradedPassive`** exactly as §12.1 requires ("no scheduler-initiated checkpoints"). `Finalize` starts a fresh draft with `parent = seq`, so advancement resumes on the next idle tick.
- Registers `o.Idle().Register("materialize_pins", 40, …)` calling `src.Pins.Materialize`.
- Panic isolation: the op handler and all three idle functions recover, log `Loud`, and return `ipc.Response{OK:false}` (op handler) or the recovered error (idle work), never propagating a panic into the daemon's worker pool.

### 17. `internal/cli/hook_checkpoint.go` — MODIFIED

SP-01 ships this subcommand as a no-op that writes `hookio.Empty()`. This subplan replaces the body with: read the event (`hookio.ReadEvent(os.Stdin, 1<<20)`), send `ipc.Request{Op:"checkpoint", Session: e.SessionID, Event:&e, Reply:true}` with an 18 s deadline, and write `resp.Output` when present, `hookio.Empty()` otherwise. The three deadlines nest strictly and deliberately: the daemon's internal `PreCompact` deadline (`now + HookTimeout − 4 s` = 16 s, itself clamped to `start + 1.5 s` by spec §15) < the client's 18 s wait < the manifest's 20 s hook timeout. Nothing on this path can make Claude Code wait out its own timeout. **Every** error path — read failure, connect failure, spool fallback, NAK, timeout, panic — writes `hookio.Empty()` and exits 0 (§2.3 "Hook subcommands must always `exit 0`").

---

## Test plan (TDD)

Tests are written before the implementation in each commit. Package-level coverage floor for `checkpoint` is **90%** (§6.4). `testify/require` only; `assert` is banned; every time-dependent test takes `testutil.FakeClock`.

### Fixtures to create

| Fixture | Contents |
|---|---|
| `testdata/golden/checkpoints/0001-minimal.json` | seq 1, no parent, 1 invariant, intent original only, empty everything else; the field-order golden |
| `testdata/golden/checkpoints/0002-full.json` | seq 2, parent `0001.json`, 3 invariants, intent + 4 evolution deltas, 2 eliminations (1 active + 1 stale with `depends_on`), 5 decisions, 3 open questions, current work with `blocked_on: null`, 6 file pointers, 4 tool pointers, 3 narrative lines, 2 dropped entries, cache `{"p_chosen":148230,"rewrite_tokens":18770,"ttl_state":"warm"}` |
| `testdata/golden/checkpoints/0003-truncated.json` | `0002` truncated to 900 tokens: narrative empty, pointers empty, open questions empty, 2 decisions, full tier 1 |
| `testdata/golden/checkpoints/0004-tier1-over-budget.json` | tier 1 alone above budget; proves tier 1 survives with a `budget_exceeded` drop |
| `testdata/golden/checkpoints/MANIFEST.jsonl` | the four lines matching the above |
| `testdata/golden/checkpoints/focus/standing.txt` | `FocusInstructions` with `IncrementalSpan:false, ForbidSnippets:true` |
| `testdata/golden/checkpoints/focus/incremental.txt` | `FocusInstructions` with `IncrementalSpan:true, Frontier:58, CheckpointPath:".qompack/checkpoints/0007.json"` |
| `testdata/fixtures/gitindex/v2.index`, `v3.index`, `v4.index`, `truncated.index` | small real git index files (3 entries each) generated once by a documented `git` invocation and committed |
| `testdata/golden/contracts/checkpoint/*.json` | Rule W-2 fixtures SP-11/SP-13 test against (a valid checkpoint + a valid `Ref`) |

### `internal/pins`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestPinsAddAppendsOneLine` | temp project, `testutil.FakeClock` fixed at `1700000000000` ms | `Add({Text:"never edit generated/"})` | `invariants.jsonl` has exactly one line; it equals `{"op":"add","id":"inv_<12hex>","text":"never edit generated/","source":"agent","pinned":1700000000000}` |
| `TestPinsAddIsIdempotent` | as above | `Add` twice with identical text | still one line; `All()` length 1 |
| `TestPinsMintIDStable` | — | `MintID("  a  b ")` and `MintID("a b")` | equal; prefix `inv_`; total length 16 |
| `TestPinsRemoveWritesTombstone` | 2 pins | `Remove(id1)` | 3 lines; third is `{"op":"del","id":"…","pinned":…}`; `All()` returns only pin 2 |
| `TestPinsRemoveUnknownIsNotFound` | empty | `Remove("inv_deadbeefcafe")` | `errors.Is(err, core.ErrNotFound)` |
| `TestPinsReplaySkipsMalformedLine` | log with `{"op":"add"…}` + `not json` + `{"op":"add"…}` | `All()` | 2 invariants, no error, counter `pins.badline == 1` |
| `TestPinsMaterializeView` | 3 pins, 1 removed | `Materialize()` | `invariants.json` is a 2-element JSON array, indented 2 spaces, trailing newline, sorted by `Pinned` |
| `TestPinsAppendOnlyGuard` | — | attempt `os.OpenFile(jsonl, O_TRUNC)` through `paths` | `core.ErrAppendOnly` |
| `TestPinsEmptyTextRejected` | — | `Add({Text:"   "})` | non-nil error, log untouched |
| `TestPinsSourceNormalized` | — | `Add({Text:"x", Source:"bogus"})` | stored `source` is `"agent"` |

### `internal/checkpoint` — schema, migration, injection

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestSchemaFieldOrderIsImportanceOrder` | — | `Marshal(golden 0002)` | the sequence of top-level keys, scanned with `json.Decoder.Token()`, equals exactly `[version session seq created parent encoded_segments invariants user_intent eliminated decisions open_questions current_work pointers narrative sketch_refs dropped cache]` |
| `TestGoldenCheckpointRoundTrip` | goldens 0001–0004 | `Unmarshal` then `Marshal` | byte-identical to the file |
| `TestEmptySlicesSerializeAsArrays` | zero `Checkpoint` | `Marshal` | contains `"encoded_segments":[]`, `"invariants":[]`, `"eliminated":[]`, `"decisions":[]`, `"open_questions":[]`, `"dropped":[]`, `"files":[]`, `"tools":[]`; contains no `null` except `"blocked_on":null` |
| `TestBlockedOnNullIsExplicit` | `CurrentWork{Goal:"x"}` | `Marshal` | contains `"blocked_on":null` |
| `TestHashMarshalsAsSha256Prefix` | `Decision{Evidence: h}` | `Marshal` | `"evidence":"sha256:<64 hex>"` |
| `TestEliminatedCarriesEverySection85Key` | golden `0002-full.json` | decode `eliminated[]` into `map[string]any` | every entry has all seven §8.5 keys — `target`, `approach`, `reason`, `evidence`, `depends_on`, `scope`, `status` — with `scope ∈ {session, project}` and `status ∈ {active, stale}`; extra `negknow.Record` keys are permitted |
| `TestMigrateV1IsIdentity` | golden 0002 | `Migrate` | output equals input |
| `TestMigrateRejectsFutureVersion` | `{"version":2}` | `Migrate` | `errors.Is(err, core.ErrContract)`, message contains `"newer plugin"` |
| `TestMigrateRejectsMissingVersion` | `{}` | `Migrate` | `errors.Is(err, core.ErrContract)` |
| `TestUnmarshalDropsUnknownFields` | golden 0002 + `"future_field":1` | `Unmarshal` | no error; a `Warn` naming `future_field` |
| `TestStripInjectionsPairedBlock` | — | `"a\n"+OpenTag(7)+"\nX\n"+InjectionCloseTag+"\nb"` | `"a\n\nb"`, count 1 |
| `TestStripInjectionsUnmatchedOpen` | — | `"a\n"+OpenTag(7)+"\nX\n\nkeep me"` | contains `"keep me"`, does not contain `"X"`, count 1 |
| `TestStripInjectionsMangledResidue` | — | `"a\nqompack:injected seq=3 ver=1\nb"` | `"a\nb"`, count 1 |
| `TestStripInjectionsLeavesCleanTextAlone` | 1 000 rapid-generated strings without the tag | `StripInjections` | identity (property test, `pgregory.net/rapid`) |
| `TestSourceSetCarriesNoText` | — | reflect over `SourceSet` | every field `Kind() == reflect.Interface` |
| `TestGoldenCheckpointsContainNoCodeBlocks` (**G3.4**, §13 invariant 5) | all files in `testdata/golden/checkpoints/` | raw bytes + decoded doc | fails if the raw bytes contain ``` ``` ```; fails if any decoded string value matches `(?m)^\s*(func |class |def |import |package |const |return |if \(|\}\s*$)`; fails if any `pointers.files[].why` or `pointers.tools[].summary` contains `\n` |

### `internal/checkpoint` — writer, draft, DPI guard

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestBeginSeedsTierOneFromSources` | temp project, 3 pins, ledger with 2 records (1 project-scope from another session), DAG with a first user prompt | `Begin(sess, 0, src)` | draft has 3 invariants, `UserIntent.Original` equals the stored prompt verbatim, `Eliminated` length 2 |
| `TestBeginInheritsOriginalIntentFromParent` | checkpoint 1 on disk with a known original, no user-prompt node | `Begin(sess, 1, src)` | `UserIntent.Original` equals checkpoint 1's, byte-for-byte |
| `TestBeginResumesPersistedDraft` | write `state/draft-s.json` with frontier 40, seq unclaimed | `Begin` | `d.Frontier() == 40`, `d.Seq()` equals the persisted seq |
| `TestBeginDiscardsDraftWithClaimedSeq` | draft seq 3, MANIFEST already has seq 3 | `Begin` | new draft with seq 4; `draft-s.stale.json` exists |
| `TestAdvanceEncodesClosedSegmentsOnly` | segments 12 (closed), 13 (open) | `Advance(d, [12,13])` | `EncodedSegments == [12]`; frontier equals segment 12's `EndTurn` |
| `TestAdvanceIsDPIGuarded` | segment 12 already `MarkEncoded` at seq 5 | `Advance(d(seq 6), [12])` | `errors.Is(err, core.ErrAlreadyEncoded)`; the draft is still persisted |
| `TestAdvanceStripsInjectionsFromStoredPrompts` | a stored user prompt containing `OpenTag(4)+"stale summary"+InjectionCloseTag` | `Advance` | no evolution entry contains `"stale summary"` or `"qompack:injected"` (**GC regeneration rule**) |
| `TestAdvanceExcludesSupersededAndEphemeralTools` | 3 tool uses: ok, superseded, ephemeral | `Advance` | exactly one tool pointer |
| `TestAdvancePointersCarryNoContent` | a 40 KB file read stored | `Advance` | serialized draft is < 4 KB; the file's content substring is absent |
| `TestAdvanceIsIdempotentForSameSeq` | segment 12 | `Advance` twice, same draft | one entry in `EncodedSegments`; no error |
| `TestAbortLeavesEncodedMarksIntact` | after `Advance` | `Abort` | `state/draft-*.json` gone; `Segments.Get(12).EncodedOnce == true` |
| `TestAdvanceKeepsPointersNewestFirst` | segments 12, 13, 14 each touching `a.ts`, `b.ts` and one new path | three `Advance` calls, ascending | `Pointers.Files[0]` is the highest-turn path and the slice is strictly descending by the recorded turn; `a.ts` appears once, at its highest turn |
| `TestAdvanceDerivesOpenQuestionsFromStaleEliminations` | ledger with 1 active + 2 stale records, `StaleBecause:["docker-compose.yml"]` | `Advance` | `OpenQuestions` has exactly 2 entries, each starting `re-verify "` and naming the changed dependency; a second `Advance` does not duplicate them |
| `TestAdvanceDerivesCurrentWorkGoalFromLatestPrompt` | 3 user prompts in range, the last `"Fix the retry loop. Then ship."` | `Advance` | `CurrentWork.Goal == "Fix the retry loop."`; `NextStep == ""`; `BlockedOn == nil` |
| `TestSetCurrentWorkSuppressesDerivation` | as above | `SetCurrentWork({Goal:"X"})` then `Advance` | `Goal == "X"` — derivation does not overwrite an explicit value |
| `TestPackageFunctionsWorkWithoutObservers` | observers never set (`SetObservers` not called) | `ExtractDecisions`, `Truncate`, `ValidatePointers` | no panic, no nil dereference, results identical to the observed runs |

### `internal/checkpoint` — decisions

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestExtractFromEdgeExplains` | DAG: assistant node A (text `"Switched to a transaction-scoped pool. pgbouncer 1.18 ignores pool_timeout in transaction mode. Extra."`) `--EdgeExplains-->` file node `src/auth.ts`, turn 12 | `ExtractDecisions(src, 0)` | 1 decision; `What == "changed src/auth.ts"`; `Why` starts `"Switched to a transaction-scoped pool."` and includes the second sentence, excludes `"Extra."`; `Evidence == A.Root`; `Turn == 12` |
| `TestExtractFromElimination` | ledger record `{Target:"src/auth.ts:refreshToken", Approach:"widen pool timeout", Reason:"pgbouncer 1.18 ignores it in transaction mode"}` | `ExtractDecisions` | `What == "rejected \"widen pool timeout\" for src/auth.ts:refreshToken"`; `AlternativesRejected == ["widen pool timeout"]` |
| `TestExtractFromDecisionPin` | pin `{Text:"use pgx directly because the pool wrapper hides timeouts", Source:"decision"}` | `ExtractDecisions` | `What == "use pgx directly"`, `Why == "the pool wrapper hides timeouts"` |
| `TestDecisionIDIsStableAndDeterministic` | — | `MintDecisionID` twice with the same inputs, and with whitespace variants | equal; prefix `dec_`; length 16; a different `evidence` yields a different id |
| `TestExtractDeduplicatesByID` | the same explains-edge duplicated at turns 12 and 30 | `ExtractDecisions` | 1 decision with `Turn == 12` |
| `TestExtractRanksBySliceScore` | 3 decisions; slice scores 0.9/0.1/0.5 for their decision nodes | `ExtractDecisions` | order is the 0.9, 0.5, 0.1 decisions |
| `TestExtractEmitsDecisionNodesAndEdges` | one explains-edge | `ExtractDecisions` | `Graph.Node("decision:dec_…")` exists with `Kind == dag.KindDecision`; an `EdgeExplains` edge points from the evidence node to it |
| `TestExtractCapsAtSixtyFour` | 200 explains-edges | `ExtractDecisions` | exactly 64 returned |
| `TestExtractRespectsFromTurn` | edges at turns 5 and 40 | `ExtractDecisions(src, 20)` | only the turn-40 decision |
| `TestExtractSurvivesStoreReadFailure` | one explaining node whose root is absent from the store | `ExtractDecisions` | no error; that decision omitted; counter `checkpoint.decision_read_error == 1` |

### `internal/checkpoint` — truncation (§6.9)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestTruncateDropsTierThreeFirst` | golden 0002, budget just below its size | `Truncate` | `Narrative == ""`; tier 1 and tier 2 byte-identical to the input |
| `TestTruncateDropsToolPointersBeforeFilePointers` | budget forcing 4 pointer cuts | `Truncate` | all 4 dropped are tool pointers; file pointers intact |
| `TestTruncateDropsPointersTailFirst` | 6 file pointers | budget forcing 2 cuts | the last two in slice order are gone |
| `TestTruncateReachesTierTwoOnlyAfterTierThree` | budget 900 | `Truncate` | `Pointers.Files` and `Pointers.Tools` empty **before** any decision is dropped; result equals golden `0003-truncated.json` |
| `TestTruncateEmptiesAlternativesBeforeDroppingDecisions` | 5 decisions with alternatives | budget between | every `AlternativesRejected` is empty; all 5 decisions present |
| `TestTruncateNeverTouchesTierOne` | budget 10 | `Truncate` | `Invariants`, `UserIntent`, `Eliminated` byte-identical; one `DropEntry{Kind:"budget_exceeded"}`; **no error** |
| `TestTruncateHonoursConfiguredNeverList` | `TiersCfg.Never` additionally lists `"pointers"` | budget 900 | pointers survive; decisions are cut instead |
| `TestTruncateZeroBudgetIsUnlimited` | golden 0002 | `Truncate(c, 0, …)` | output equals input, nil drops |
| `TestTruncateIsMonotone` (property, `rapid`) | random checkpoints, random budget pairs `b1 < b2` | `Truncate` | `sizeOf(Truncate(c,b1)) ≤ sizeOf(Truncate(c,b2))` and the `b1` result's field set is a subset of the `b2` result's |
| `TestTruncateDropEntriesAreComplete` | any cut | `Truncate` | one `DropEntry` per removed element, with the `Kind` from the table in §10 |

### `internal/checkpoint` — pointer validation (G2.5)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestValidatePointersMissingFile` | pointer to `src/gone.ts`, not on disk | `ValidatePointers` | one `DropEntry{Kind:"pointer_missing", ID:"src/gone.ts"}` |
| `TestValidatePointersDirectory` | pointer to `src/` | — | `DropEntry{Kind:"pointer_invalid", Detail:"path is a directory"}` |
| `TestValidatePointersEscape` | pointer `../../etc/passwd` | — | `DropEntry{Kind:"pointer_invalid", Detail:"path escapes the project root"}` |
| `TestValidatePointersDirtyAgainstIndex` | `v2.index` fixture + a file whose size differs | — | `DropEntry{Kind:"pointer_dirty"}`; `Detail` contains the branch name from `HEAD` |
| `TestValidatePointersUntracked` | file present, absent from index | — | `DropEntry{Kind:"pointer_untracked"}` |
| `TestValidatePointersCleanFileProducesNoDrop` | file matching its index entry's size and mtime | — | empty drop slice |
| `TestGitIndexV3ExtendedFlags` | `v3.index` fixture with an extended-flag entry | `parseIndex` | 3 entries, correct paths, correct sizes |
| `TestGitIndexV4Unsupported` | `v4.index` | `parseIndex` | `errIndexUnsupported`; `ValidatePointers` still runs check 1 and adds `pointer_git_unavailable` |
| `TestGitIndexTruncated` | `truncated.index` | `parseIndex` | `errIndexUnsupported`, no panic |
| `TestGitDirAsFileWorktree` | `.git` file containing `gitdir: ../real/.git` | `ValidatePointers` | resolves and reads the real index |
| `TestValidatePointersNoGitDir` | no `.git` | — | working-tree checks only + one `pointer_git_unavailable` |
| `FuzzParseGitIndex` | seed corpus = the four fixtures | arbitrary bytes | never panics; always returns an error or a well-formed entry list |

### `internal/checkpoint` — manifest and reader

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestManifestLineFormat` | one finalize | read `MANIFEST.jsonl` | exactly `{"seq":1,"sha256":"sha256:<64hex>","bytes":<n>,"created":"<RFC3339.mmm>Z"}\n` |
| `TestFinalizeIsImmutable` | after finalize | reopen `0001.json` with `O_WRONLY` | permission error; file mode is `0444` on POSIX / read-only attribute on Windows |
| `TestFinalizeSeqCollisionIncrements` | pre-create `0001.json` | `Finalize` | file `0002.json` written; manifest seq 2; `cp.Seq == 2` |
| `TestGetDetectsManifestMismatch` | flip one byte in `0002.json` | `Get(2)` | `errors.Is(err, core.ErrContract)`; a `Loud` entry; counter incremented |
| `TestLatestFallsBackToParent` | `0003.json` corrupted | `Latest(sess)` | returns checkpoint 2, no error |
| `TestLatestNotFoundWhenNothingVerifies` | all corrupted | `Latest` | `errors.Is(err, core.ErrNotFound)` |
| `TestChainReturnsOldestFirst` | 1←2←3 | `Chain(3)` | seqs `[1,2,3]` |
| `TestChainRejectsCycle` | `0002.json` with `parent:"0002.json"` | `Chain(2)` | `errors.Is(err, core.ErrContract)` |
| `TestVerifyReturnsMismatchedSeqs` | 2 of 4 corrupted | `Verify` | exactly those two seqs, ascending |
| `TestListSkipsMalformedManifestLine` | manifest + `garbage\n` | `List` | the good refs; counter `checkpoint.manifest_badline` incremented; no error |
| `TestReaderRefLeavesWriterOnlyFieldsZero` | one finalize with frontier 58 | `List`, `Get`, `Latest` | every returned `Ref` has `Tokens == 0` and `Frontier == 0`; the `Ref` returned by `Finalize` has `Frontier == 58` |
| `TestFinalizeRemovesUnresolvablePointers` | draft with 1 file pointer to a deleted file and 1 tool pointer whose root was GC'd | `Finalize` | both pointers absent from the written file; `Dropped` contains `pointer_missing` and `pointer_unresolvable` |

### `internal/checkpoint` — focus instructions (O1)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestFocusStandingTemplateVerbatim` | — | `FocusInstructions(c, ref, FocusOptions{ForbidSnippets: true})` (matching the golden's stated options) | equals `testdata/golden/checkpoints/focus/standing.txt`; the first paragraph is byte-identical to §8.5's template quoted in this plan |
| `TestFocusStandingOnlyWhenAllOptionsOff` | — | `FocusInstructions(c, ref, FocusOptions{})` | output is exactly `standingTemplate` — one paragraph, no span, no prohibition, no sentinel |
| `TestFocusIncrementalSpanNamesPathAndTurn` | frontier 58, path `.qompack/checkpoints/0007.json` | `IncrementalSpan:true` | equals `focus/incremental.txt`; contains ``A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn 58`` and `"Summarize only what happened after turn 58"` |
| `TestFocusOmitsSpanWhenFrontierZero` | frontier 0 | `IncrementalSpan:true` | output has no incremental paragraph |
| `TestFocusOmitsSpanWhenConfigDisabled` | `incrementalSpanInstruction:false` via `PreCompact` | — | one paragraph plus the snippet prohibition |
| `TestFocusContainsSentinel` | `ForbidSnippets:true` | — | `strings.Contains(out, checkpoint.SentinelPhrase)` |
| `TestFocusForwardSlashesOnWindows` | `CheckpointPath` from a Windows absolute path | — | contains `/`, contains no `\` |
| `TestFocusCappedAtFourThousandBytes` | 5 000-byte injected path | — | `len(out) <= 4000`; ends at a paragraph boundary |
| `TestNoForbiddenImports` | — | `go/parser` scan of every non-test file in `internal/checkpoint` and `internal/pins` | the import set is a subset of `{core, paths, config, logging, obs, store, dag, negknow, pins, grammar, tokens}` plus stdlib; `hookio`, `scheduler`, `ipc`, `daemon`, `os/exec`, `net`, `net/http` all absent. `hookio`'s absence is the mechanical form of the advisory-handling rule: the package cannot read a transcript, therefore it cannot branch on whether the summarizer complied. |

### `internal/checkpoint` — PreCompact

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestPreCompactFinalizesExistingDraft` | draft advanced through segment 14 | `PreCompact` | `NewDraft == false`; `0001.json` on disk; `Instructions` contains the span paragraph with the draft's frontier |
| `TestPreCompactColdPathBeginsDraft` | no draft, 3 closed unencoded segments | `PreCompact` | `NewDraft == true`; counter `checkpoint.cold_precompact == 1`; a checkpoint is still written |
| `TestPreCompactWritesStateObservable` | any | `PreCompact` | `state/precompact.json` parses and has `sentinel == "qompack checkpoint"`, `timeout_ms == 20000`, `span_instruction == true`, `wall_ms >= 0` |
| `TestPreCompactFinalizesAsIsNearDeadline` | `Deadline = now + 300ms` (inside `finalizeGuard`), 40 unencoded segments | `PreCompact` | returns within 600 ms with a valid checkpoint; `EncodedSegments` is a strict subset; no error (§12 timeout row) |
| `TestPreCompactStartsFreshDraftAfterFinalize` | — | `PreCompact` then `Advance` | the new draft's `parent` equals the finalized seq; frontier keeps advancing |
| `TestPreCompactErrorStillLeavesHookHealthy` | read-only `checkpoints/` | `PreCompact` | error returned; the daemon wiring test asserts the hook writes `hookio.Empty()` and exits 0 |
| `TestPreCompactRecordsSuppliedTimeout` | `HookTimeout: 20*time.Second` | `PreCompact` | `state/precompact.json` `timeout_ms == 20000`; `grep -n "20000" internal/checkpoint/*.go` finds nothing (the `nomagic` contract) |
| `TestCadenceFinalizesWhenDraftReachesBudget` | draft advanced past `cfg.Checkpoint.BudgetTokens` | one idle tick of `checkpoint_cadence` | `0001.json` written without any `PreCompact`; a fresh draft exists with `parent == 1` (§8.5's "checkpoints exist even when compaction does not fire") |
| `TestCadenceFinalizesAfterEightSegments` | 8 encoded segments, draft under budget | one idle tick | a checkpoint is written |
| `TestCadenceIsOffInDegradedPassive` | `ModeDegradedPassive`, draft over budget | one idle tick | **no** checkpoint written (§12.1 "no scheduler-initiated checkpoints"); `advance_frontier` still ran and the draft still grew |

### Conformance and e2e

- `checkpointtest.RunWriterSuite` / `RunReaderSuite` / `RunPinsSuite`: every `t.Skip` from SP-01 removed; a merge-blocking test asserts `grep -R "t.Skip" internal/checkpoint internal/pins` finds nothing. **All three suites live in `checkpointtest`** — §5.22 lists `checkpointtest` and no `pinstest`, and since `checkpoint.Invariant` is an alias of `pins.Invariant` the pins suite belongs to the same package. `internal/pins` has no conformance subpackage of its own.
- `test/e2e/checkpoint_test.go`: build the real binary, start a real daemon against a temp project, replay 60 synthetic turns through `qompack observe tool`, then close 3 segments **by calling `store.SegmentLog.Close` directly** (SP-06's API, already on `develop`) — the e2e must not depend on SP-12's changepoint detector, which is a same-wave sibling and may not be merged. Run one idle tick, then invoke `qompack checkpoint` with a real `PreCompact` payload on stdin. Assert: exit code 0; stdout parses as `hookio.Output` with `hookSpecificOutput.customInstructions` containing the span paragraph and `SentinelPhrase`; `checkpoints/0001.json` exists, is read-only, and re-hashes to its manifest line; `state/precompact.json` exists.
- `test/e2e/checkpoint_degraded_test.go`: with `contract.ModeDegradedPassive` forced, assert the checkpoint file is still written and `customInstructions` is **absent**.

### Benchmarks (named budgets)

| Benchmark | Budget | Assertion |
|---|---|---|
| `BenchmarkFinalize` (40 segments, 64 decisions, 400 pointers) | **B-E `checkpoint_finalize` p99 < 2 s (§11.3 L4)** | mean < 50 ms, recorded in `testdata/bench-baseline.txt` |
| `BenchmarkAdvanceSegment` (40 tool uses, 12 files) | O5 idle work | mean < 25 ms |
| `BenchmarkTruncate` (golden 0002 at budget 12000) | — | mean < 5 ms |
| `BenchmarkExtractDecisions` (5 000 nodes, 800 explains edges) | — | mean < 20 ms |
| `BenchmarkStripInjections` (256 KB transcript tail) | — | mean < 2 ms |
| `devtool bench-hotpath --hook checkpoint -n 200` | **B-E p99 < 2 s**, hard CI fail | measured across linux/macos/windows |

---

## Commit plan

Work happens on `feat/sp10-checkpointer-l4`, cut from `develop` **after** SP-01, SP-06, SP-07, SP-08 and SP-09 have merged and verification V3 is green.

```
git fetch origin && git checkout develop && git pull
git checkout -b feat/sp10-checkpointer-l4
```

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

Every commit compiles, passes `go run ./tools/devtool test` for the packages it touches, and passes `go run ./tools/devtool lint`.

### Commit 1 — `feat(pins): append-only invariant log with tombstones and materialized view`

- [ ] Write `internal/pins/pins_test.go` and `internal/pins/store_test.go` with all ten pins tests above; run `go test ./internal/pins/...` and confirm they **fail** against the SP-01 stub.
- [ ] Add `internal/pins/pins.go` (`Invariant`, `MintID`, `normalizeWS`).
- [ ] Add `internal/pins/store.go` (`Open`, `Add`, `Remove`, `All`, `Materialize`, log replay, `paths.AppendOnly` + `paths.WriteAtomic`).
- [ ] Flip the `t.Skip`s in `checkpointtest.RunPinsSuite` (the pins suite ships inside `checkpointtest`; §5.22 defines no `pinstest` package).
- [ ] Run `go run ./tools/devtool test` and `lint`; all pins tests pass.
- Footer: `Refs: SP-10, G2.2, §7.4, §8.5`

### Commit 2 — `feat(checkpoint): §8.5 schema, migration hook, and injection tagging`

- [ ] Write `internal/checkpoint/schema_test.go`, `migrate_test.go`, `inject_test.go`, `source_test.go` and `nocode_test.go`; commit the goldens `0001-minimal.json`, `0002-full.json`, `MANIFEST.jsonl`, and the W-2 contract fixtures. Confirm the tests **fail**.
- [ ] Add `internal/checkpoint/schema.go`, `migrate.go`, `inject.go`, `source.go`, `obs.go` (`SetObservers`, `pkgLog`, `pkgMetrics`, `nopRegistry`), and `internal/core/hash_json.go` if `core.Hash` lacks JSON methods.
- [ ] Verify `TestSchemaFieldOrderIsImportanceOrder` and `TestGoldenCheckpointsContainNoCodeBlocks` pass (the §13 invariant-5 gate).
- [ ] `go test -fuzz=FuzzStripInjections -fuzztime=60s ./internal/checkpoint`.
- Footer: `Refs: SP-10, G2.4, G3.4, G5.3, §6.9, §8.5`

### Commit 3 — `feat(checkpoint): SourceSet draft and the incremental Advance writer`

- [ ] Write `internal/checkpoint/draft_test.go` and `writer_test.go` (all eleven writer/draft tests); confirm failure.
- [ ] Add `internal/checkpoint/draft.go` and `internal/checkpoint/writer.go` (`OpenWriter`, `Begin`, `Advance`, `Abort`, draft persistence to `state/draft-<session>.json`, `fromStore`).
- [ ] Confirm `TestAdvanceIsDPIGuarded` and `TestAdvanceStripsInjectionsFromStoredPrompts` pass — these two are the mechanical enforcement of §4.6 and the §8.5 regeneration rule.
- [ ] `go test -race ./internal/checkpoint`.
- Footer: `Refs: SP-10, G2.6, G9.1, §8.2, §8.5, §4.6`

### Commit 4 — `feat(checkpoint): decision extraction and DAG decision nodes`

- [ ] Write `internal/checkpoint/decisions_test.go` (all ten decision tests) plus `BenchmarkExtractDecisions`; confirm failure.
- [ ] Add `internal/checkpoint/decisions.go` (`MintDecisionID`, `ExtractDecisions`, the three sources, slice ranking, `KindDecision`/`EdgeExplains` emission).
- [ ] Wire `ExtractDecisions` into `Advance`.
- [ ] `go test -bench=BenchmarkExtractDecisions -benchtime=50x ./internal/checkpoint`; confirm < 20 ms/op.
- Footer: `Refs: SP-10, G5.3, §8.5, §6.4`

### Commit 5 — `feat(checkpoint): importance-ordered truncation and git-backed pointer validation`

- [ ] Write `internal/checkpoint/truncate_test.go` (ten tests incl. the `rapid` monotonicity property) and `validate_test.go` + `gitindex_test.go` + `FuzzParseGitIndex`; add the goldens `0003-truncated.json`, `0004-tier1-over-budget.json` and the four git-index fixtures. Confirm failure.
- [ ] Add `internal/checkpoint/truncate.go` and `internal/checkpoint/gitindex.go`, `internal/checkpoint/validate.go`.
- [ ] Confirm `TestTruncateNeverTouchesTierOne` passes and that `internal/checkpoint` imports neither `os/exec` nor `net` (the `security` job's rule) — assert it in `TestNoForbiddenImports`.
- [ ] `go test -fuzz=FuzzParseGitIndex -fuzztime=60s ./internal/checkpoint`.
- Footer: `Refs: SP-10, G2.5, G4.3, §6.9, §8.5, §4.4`

### Commit 6 — `feat(checkpoint): Finalize, MANIFEST, and the Reader with parent fallback`

- [ ] Write `internal/checkpoint/manifest_test.go`, `reader_test.go`, `finalize_test.go` (all manifest/reader tests) plus `BenchmarkFinalize` and `BenchmarkTruncate`; confirm failure.
- [ ] Add `internal/checkpoint/manifest.go`, `reader.go`, `finalize.go`.
- [ ] Confirm the immutability test: `0001.json` is `0444` and `paths.CreateNew` refuses a second write.
- [ ] `go test -bench=BenchmarkFinalize -benchtime=20x ./internal/checkpoint`; record the result in `testdata/bench-baseline.txt`.
- Footer: `Refs: SP-10, G7.4, G9.1, §7.4, §8.5, §11.3`

### Commit 7 — `feat(checkpoint): focus instructions with the O1 incremental span`

- [ ] Write `internal/checkpoint/focus_test.go` (all eight focus tests) and commit `testdata/golden/checkpoints/focus/standing.txt` and `focus/incremental.txt`; confirm failure.
- [ ] Add `internal/checkpoint/focus.go` (`SentinelPhrase`, the three templates, `FocusInstructions`).
- [ ] Confirm the two golden files match the §8.5 quotations in this plan character-for-character.
- Footer: `Refs: SP-10, G3.4, G7.1, §4.5, §8.5 O1, §12`

### Commit 8 — `feat(daemon): wire PreCompact, idle frontier advancement, and the checkpoint hook`

- [ ] Write `internal/checkpoint/precompact_test.go` (six tests), `test/e2e/checkpoint_test.go`, `test/e2e/checkpoint_degraded_test.go`; confirm failure.
- [ ] Add `internal/checkpoint/precompact.go` (`Compactor`, `PreCompactInput/Result`, the `finalizeGuard` deadline arithmetic, `state/precompact.json`).
- [ ] Add `internal/daemon/wire_checkpoint.go` (op handler with the `HookTimeout`/`Deadline` arithmetic, three idle registrations — `advance_frontier`, `checkpoint_cadence`, `materialize_pins` — the two degraded-mode branches, panic isolation).
- [ ] Modify `internal/cli/hook_checkpoint.go` to send the op with `Reply:true` and write `resp.Output`, always exiting 0.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 200 --hook checkpoint --warm-daemon --json out.json`; confirm **B-E p99 < 2 s**.
- [ ] Run `go run ./tools/devtool ci-local` (verify, test, cover, plugin-validate, security, replay-gate) and confirm green.
- Footer: `Refs: SP-10, G2.5, G7.4, G9.1, §7.3, §8.5, §11.3, §12`

Merge into `develop` with `--no-ff` in the wave-3 order (SP-10 merges before SP-11 and SP-13, which consume `checkpoint.Reader` and `core.DecisionID`).

---

## Subagent strategy

This subplan is **heavy**: two packages, nineteen new files, seven independent algorithmic cores. Partition the *authoring* across four parallel subagents while the **main session keeps the git branch, every commit, and every file that touches the draft/writer lifecycle** — because those files are where the subagents' outputs meet and a conflict there is a correctness bug, not a merge conflict.

**Main session owns (never delegated):** the branch and all eight commits; `internal/checkpoint/draft.go`, `writer.go`, `finalize.go`, `precompact.go`; `internal/daemon/wire_checkpoint.go`; `internal/cli/hook_checkpoint.go`; every golden checkpoint fixture (they are the shared contract the subagents' tests assert against, so one author must own their exact bytes); and the final integration run of `devtool ci-local`.

**Subagent A — pins and schema.** Files: `internal/pins/pins.go`, `internal/pins/store.go` and their tests; `internal/checkpoint/schema.go`, `migrate.go`, `source.go`, `obs.go`, `inject.go` and their tests. Returns: the two files' full source, the exact top-level key order it produced, and the `MintID`/`MintDecisionID` hex prefixes it observed for the fixture inputs. Integration: main session diffs the produced key order against the §8.5 quotation in this plan before committing.

**Subagent B — decisions.** Files: `internal/checkpoint/decisions.go` + `decisions_test.go` + `BenchmarkExtractDecisions`. Given: the `dag` and `negknow` signatures from the Interface contract section, and a fake `SourceSet` built on `dagtest`/`negknowtest` fakes. Returns: the file, the benchmark number, and the exact decision IDs minted for the three fixture inputs (main session pastes those into `0002-full.json`). Must not touch `writer.go`; it exposes `ExtractDecisions` and the main session calls it from `Advance`.

**Subagent C — truncation and validation.** Files: `internal/checkpoint/truncate.go`, `validate.go`, `gitindex.go` and their tests plus `FuzzParseGitIndex`. Given: the cut-order table and the `DropEntry` kind table verbatim from this plan, and the git index binary layout spec. Returns: the three files, the fuzz corpus, and the byte content of `0003-truncated.json` produced by running `Truncate` on `0002-full.json` at budget 900 (main session commits that output as the golden). This is the largest independent unit and the one with zero coupling to the draft lifecycle.

**Subagent D — manifest, reader, focus.** Files: `internal/checkpoint/manifest.go`, `reader.go`, `focus.go` and their tests. Given: the manifest line format byte-for-byte and the three focus templates verbatim. Returns: the three files plus the two `focus/*.txt` goldens. Integration: main session verifies the goldens character-for-character against the §8.5 quotations in the Design context section before committing — a paraphrase here silently breaks the §12 `precompact.custom_instructions_accepted` assertion.

**Sequencing.** Dispatch A, C and D immediately and in parallel (they share no file). Dispatch B after A returns, because B's tests import `checkpoint.SourceSet` from A's `source.go`. The main session writes `draft.go` and `writer.go` while A/C/D are running, using the signatures fixed in this plan, then integrates in commit order 1→8. **Commits stay strictly sequential and are made only by the main session**, so each commit's "tests fail first, then pass" record is real.

---

## Exit criteria

**Quoted verbatim from `Qompack.md` — the Phase 3 criterion this slice half-owns (§10 Phase 3):**

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

SP-10 delivers the producing half — the durable checkpoint and the span instruction; SP-11 delivers the consuming half and the two are measured together at V4. SP-10's own gate is the second clause: **`eval` reports a reduction in residual span at compaction** on the multi-compaction synthetic sessions when frontier advancement is enabled versus disabled. Measured exactly like this, and recorded in the PR body:

```
go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
    --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=false --json off.json
go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
    --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=true  --json on.json
```

Pass condition: `on.json`'s `Score.ResidualSpan.P50` is at least **30% below** `off.json`'s on the same corpus, and `Score.Divergence` does not regress by more than the 2% of §11.3. `eval.Score.ResidualSpan` is SP-02's field (wave 1, already on `develop`), so this gate does not wait on SP-12.

**Quoted verbatim from `Qompack.md` §11.3:**

> - Hook p99 latency < 15ms (L0), < 2s (L4)

**Quoted verbatim from `Qompack.md` §8.5:**

> Note what is **not** here: no code snippets. Files are pointers with a one-line reason.

**Local criteria — all must hold on the branch before merge:**

- [ ] `go run ./tools/devtool test` green on linux, macos and windows; `-race` clean.
- [ ] `go run ./tools/devtool lint` and `vet` clean; `gofumpt -l` empty; the `nomagic` pass green — no literal from the §11.6 set (`12000`, `20000`, `10000`, `2048`, `1024`, `4096`, `16384`, `300`, `120`, `450`) in `internal/checkpoint`, `internal/pins` or the two files elsewhere, except the single annotated `pointerWhyMaxRunes` declaration.
- [ ] Line coverage for `internal/checkpoint` ≥ **90%** and `internal/pins` ≥ **90%** (§6.4 puts `checkpoint` in the 90% group; `pins` is held to the same floor because it is append-only state).
- [ ] The `checkpointtest` conformance suite shipped by SP-01 (§5.22) — `RunWriterSuite`, `RunReaderSuite`, `RunPinsSuite` — runs with **zero** `t.Skip` remaining.
- [ ] `devtool bench-hotpath --hook checkpoint -n 200`: **B-E p99 < 2 s** on all three CI platforms.
- [ ] `BenchmarkFinalize` mean < 50 ms; `BenchmarkAdvanceSegment` < 25 ms; `BenchmarkExtractDecisions` < 20 ms; no micro-benchmark regresses > 10% against `testdata/bench-baseline.txt`.
- [ ] `TestGoldenCheckpointsContainNoCodeBlocks` green (§13 invariant 5 / G3.4).
- [ ] `TestSourceSetCarriesNoText` green (§8.5 regeneration rule, §13 invariant 1).
- [ ] `TestAdvanceIsDPIGuarded` green (§4.6, §8.2 encoded-once flag).
- [ ] `TestCadenceFinalizesWhenDraftReachesBudget` and `TestCadenceIsOffInDegradedPassive` green — §8.5's "and independently on the scheduler's own cadence" is implemented and correctly gated by §12.1.
- [ ] `paths.TestAppendOnlyGuard` green against `checkpoints/` and `pins/invariants.jsonl` (§13 invariant 2).
- [ ] `security` CI job green: `internal/checkpoint` and `internal/pins` import no `net`, `net/http`, `os/exec`.
- [ ] The import-graph check confirms `checkpoint` imports only `store dag negknow pins grammar tokens` plus the foundation, and `pins` imports the foundation only.
- [ ] `test/e2e/checkpoint_test.go` and `checkpoint_degraded_test.go` green: `qompack checkpoint` exits 0, emits `customInstructions` in `full` mode and none in `degraded-passive`, and writes a read-only checkpoint that re-hashes to its manifest line.
- [ ] `replay-gate` green with no metric regressing more than 2% (§11.3).
- [ ] Exactly 8 commits, conventional-commit formatted, no attribution trailers.

---

## Done checklist

- [ ] Every constant, template and schema quoted in **Design context** has a corresponding implementation and at least one test naming it: the §8.5 JSON shape (schema.go + `TestSchemaFieldOrderIsImportanceOrder`), the §4.5 standing template (focus.go + `TestFocusStandingTemplateVerbatim`), the O1 paragraph (focus.go + `TestFocusIncrementalSpanNamesPathAndTurn`), the §6.9 ordering (truncate.go + the six truncation tests), the Appendix C `checkpoint` block (read through `config.CheckpointCfg`, never hardcoded), the §8.2 encoded-once guard (`TestAdvanceIsDPIGuarded`), the §7.4 append-only invariant (`paths.TestAppendOnlyGuard`).
- [ ] Placeholder scan: `grep -rniE "TODO|TBD|FIXME|not implemented|handle appropriately|add tests" internal/checkpoint internal/pins internal/daemon/wire_checkpoint.go internal/cli/hook_checkpoint.go` returns nothing (except `core.ErrNotImplemented` if it legitimately remains in an unrelated stub, which it must not in these two packages).
- [ ] Type consistency: every signature in **Interface contract → Produces** exists verbatim in the built package; `go build ./...` plus a compile-time assertion file `var _ Writer = (*FileWriter)(nil); var _ Reader = (*fileReader)(nil); var _ Compactor = (*FileWriter)(nil); var _ pins.Store = (*pinStore)(nil)`.
- [ ] `checkpoint.Invariant` is an alias of `pins.Invariant` and `pins` does not import `checkpoint` (no cycle, §3.2).
- [ ] `core.DecisionID` is produced **only** by `MintDecisionID` — `grep -rn "DecisionID(" internal/ | grep -v checkpoint/decisions.go` shows only consumers.
- [ ] No file in `internal/checkpoint` imports `hookio`, `scheduler`, `ipc` or `daemon`.
- [ ] Golden checkpoint fixtures exist at `testdata/golden/checkpoints/` and the W-2 contract fixtures at `testdata/golden/contracts/checkpoint/` are consumable by SP-11 and SP-13 without modification.
- [ ] Commit count verified at **8** (within the 5–8 range): `git rev-list --count develop..feat/sp10-checkpointer-l4` equals 8.
- [ ] No co-author or attribution trailers: `git log develop..HEAD --format=%B | grep -niE "co-authored-by|signed-off-by|generated with|🤖"` returns nothing.
- [ ] `Qompack.md` is unmodified: `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] Branch pushed, CI green on `feat/sp10-checkpointer-l4`, ready to merge into `develop` ahead of SP-11 and SP-13.

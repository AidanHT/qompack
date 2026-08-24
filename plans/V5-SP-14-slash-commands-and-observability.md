# SP-14: L6 slash commands: status, recall, pin, checkpoint, why, dropped, eval and the /qompack:status observability surface

> **Recommended model: Opus 5 · xhigh effort**
>
> Explicitly "a frontend and nothing else" — every behaviour it exposes already has exactly one implementation elsewhere. The cheapest plan in the set to run well.

**Branch:** `feat/sp14-slash-commands-and-observability` (cut from `develop`) | **Wave:** 4 | **Prerequisites:** the branches of SP-01, SP-02, SP-05, SP-06, SP-09, SP-10, SP-11, SP-12, SP-13 already merged into `develop` (post-V4 `develop`), plus the `arch/checkpoint-now-subcommand` pre-step (§2.3's `checkpoint-now` line — opened and merged by the session that opens wave 4, before any wave-4 feature branch is cut; see the cli section) | **Runs in parallel with:** SP-15 (`analyzer`, `grammar`, `checkpoint/grammar.go`) and SP-16 (`checkpoint/promote.go`, `sketch` warm start) — no file is shared with either | **Design sections:** §7.5 (commands), §8.7, §11 (surfaced metrics), §12 (status surfaces size) | **Gaps closed:** none exclusively. `/qompack:status` is the *surface* the §9 matrix names in the G8.1 row ("G8.1 no observability | L7 replay harness; `/qompack:status`") and `/qompack:eval` is the surface for the G8.3 row ("G8.3 no feedback | L7 fraction-of-OPT metric"); closure of both is credited to SP-02's L7 harness. SP-14 owns the presentation, not the measurement.

---

## Mission

Qompack is a sidecar that observes, stores, schedules, checkpoints, rehydrates and retrieves — and until this subplan lands, every one of those behaviours is invisible to the human sitting in front of Claude Code. `Qompack.md` §1.3 names the third root cause as **RC-3 — Unmeasured**: *"No distortion metric, no ceiling, no regression signal. Constants (13K buffer, 20K reserve, 5 files, 50K budget, 5K/file, 25K skills) are all unvalidated."* §0 states the consequence: *"the first symptom of a bad compaction is behavioural degradation noticed several turns later."* (§3 restates it as G8.1: *"First signal of a bad compaction is behavioural degradation noticed several turns later."*) SP-02 built the offline half of the answer (replay, Belady OPT, fraction-of-OPT). SP-14 builds the online half: the seven slash commands of the §7.5 manifest, with `/qompack:status` as the single screen that answers "is this thing working, and is it hurting me?"

This slice is **a frontend and nothing else**. Every retrieval behaviour it exposes already has exactly one implementation elsewhere: `recall`, `why` and `dropped` are the MCP tool handlers SP-13 registered, invoked in-process through `mcp.Tool.Handler`; `checkpoint` is SP-10's `checkpoint.Writer.Begin/Advance/Finalize`; `pin` and `pin --eliminated` are SP-10's `pins.Store` and SP-09's `negknow.Ledger`; `eval` is SP-02's `eval.Harness`. SP-14 adds no second implementation of any of them.

**The one place that claim needs a precise statement is `status`, and it is stated here normatively.** SP-05 already ships both the `status` op and its payload: `internal/daemon/daemon.go`'s `defaultRoutes` registers `ipc.OpStatus: d.handleStatus`, and `internal/daemon/handlers.go` declares `daemon.StatusSnapshot{Mode string; Contract []contract.Result; Hot string; Sessions []SessionState; Latency map[string]obs.HistSnapshot; Budgets []obs.BudgetBreach; Counters map[string]int64; SpoolFiles int; LoudTail []string; Extra json.RawMessage}`. `test/bench/hotpath/measure.go` unmarshals that exact payload — it is the hot-path harness's only source for B-B and for the gated B-A row — so **`ipc.OpStatus` and `daemon.StatusSnapshot` are frozen for this subplan: SP-14 does not register a handler for `ipc.OpStatus`, does not change the payload's shape, and does not edit `d.handleStatus`.** Registering an `Options`-side handler for `status` would silently replace the default route (`buildRoutes` gives an `Options` registration priority) and break `bench-gate` with a JSON type error rather than a degradation. `commands.StatusSnapshot` is therefore a **composition**, not a re-implementation: it *consumes* `daemon.StatusSnapshot` over `ipc.OpStatus` for mode, contracts, latency, loud tail and hot-path submode, and obtains the six sections no existing route exposes — store stats, sketches, frontier, checkpoint, scheduler, GC — over a **new** op, `ipc.Op("status.full")`, registered by `cli` through SP-05's `daemon.Options.Handle` seam. That is why the wave-4 placement is load-bearing (00-ARCHITECTURE §14: *"Slash commands are wave 4, not wave 3. `commands.Deps` names `checkpoint.Writer`, `scheduler.Runtime`, and the MCP handlers — all wave 3."*): building this in wave 3 would mean reporting on golden fixtures instead of on the real surfaces.

**What exists in the repo when you start.** A `develop` that has passed verification V4. `internal/core`, `paths`, `config`, `logging`, `obs`, `contract`, `tokens`, `hookio`, `cli`, `pluginmanifest`, `testutil`, `test/e2e` (SP-01). `internal/eval` with the harness and the 24-session synthetic corpus (SP-02). `internal/ipc` + `internal/daemon` with the op-routing table, `IdleController`, the late-bound `Services` set and the B-A/B-B/B-D/B-E budget histograms (SP-05). `internal/store` with `Stats`, `SegmentLog`, `GC` (SP-06). `internal/negknow` with the ledger and `Health()` (SP-09). `internal/checkpoint` + `internal/pins` (SP-10). `internal/rehydrate` with the `DropReporter` (SP-11). `internal/scheduler` with `Runtime` and `Decision.Breakdown` (SP-12). `internal/mcp` with all eight tools registered through `mcp.RegisterAll` (SP-13). `internal/commands` exists only as SP-01's compiling stub: the `Command` interface, `Deps`, and an `All` returning `core.ErrNotImplemented` runners.

**Decision — the `commandstest` conformance suite.** 00-ARCHITECTURE §5.22 says SP-01 ships an exported suite "for every interface above", but its illustrative package list (`sketchtest, canontest, …`) does not name `commandstest`. Do not block on the ambiguity: **check for `internal/commands/commandstest/` on the branch. If it exists, flip every `t.Skip` off (Rule W-1, a merge blocker). If it does not exist, SP-14 creates it in commit 1** with exactly this shape, and no amendment is required because `commands` is SP-14's own package:

```go
// package commandstest
// RunCommandSuite asserts the SHAPE contract of §5.17 against any Command set:
// seven commands, §7.5 order, Name() matches pluginmanifest.Commands()[i].Name,
// --help exits 0 for each, --json emits a parseable Envelope for each, and no
// Run implementation writes to os.Stdout/os.Stderr directly.
func RunCommandSuite(t *testing.T, name string, factory func(t *testing.T) []commands.Command)
```

**What exists when you finish.** `internal/commands` with seven real `Command` implementations sharing one flag parser, one JSON envelope and one deterministic renderer; `internal/commands/StatusSnapshot`, a fully typed observability record composed from the daemon's own `daemon.StatusSnapshot` (over the untouched `ipc.OpStatus`) plus the six extended sections carried over the new `ipc.Op("status.full")`, and collected locally from disk when the daemon is unreachable; the seven `plugin/commands/*.md` prompt files and `docs/commands.md`, both **generated** from `internal/pluginmanifest`'s existing single command table (`commandSpecs`, extended in place) so the manifest, the binary and the docs cannot drift; a `devtool plugin-validate` assertion that all seven commands exist, are registered, and resolve to real subcommands of the built binary; and golden output tests for every command in both human and `--json` modes. Every `t.Skip` in `commandstest` is removed — a merge blocker under Rule W-1.

---

## Design context (verbatim from Qompack.md)

### §7.5 — plugin manifest (the seven command names, in this order)

```json
  "commands": [
    "status", "recall", "pin", "checkpoint", "why", "dropped", "eval"
  ]
```

### §8.7 — L6 retrieval tools (MCP), the table the frontends delegate to

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

> **Retrieved content is born ephemeral (closes GB).** … Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).
> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch.

### §8.3 — elimination sources (source #2 is `/qompack:pin --eliminated`)

> Sources, in descending reliability:
> 1. Explicit agent declaration via the MCP tool `record_eliminated(target, approach, reason)`
> 2. Slash command `/qompack:pin --eliminated`
> 3. Heuristic detection: a test-fail → revert → different-approach pattern in the DAG
> 4. Explicit user statements ("that didn't work," "we tried that")
>
> Canonical descriptor: `(normalized_path, symbol_or_null, approach_class, reason_hash)`. Insert into `tried.bloom`.

**Note on numbering:** the list above is §8.3's *sources* list (items 1–4). The scope rule below is item **5 of §8.3's separate staleness list**, not a fifth source:

> 5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).

### §8.5 — the checkpoint the `/qompack:checkpoint` command finalizes

> **Trigger:** `PreCompact` (and independently on the scheduler's own cadence, so checkpoints exist even when compaction does not fire).
>
> **Output:** an immutable, versioned, **importance-ordered** JSON artifact.

> **Regeneration rule (closes GC).** Checkpoints are **always generated from the store** — the chunk objects, segment log, verbatim intent captures, and structured records — never from content currently sitting in the context window.

### §11.1 / §11.2 — the metrics `/qompack:eval` and `/qompack:status` surface

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

| Metric | Definition |
|---|---|
| First-divergence turn | Turns after compaction before compacted and uncompacted branches diverge |
| File-set Jaccard | Overlap of files touched over the next K turns |
| Redundant work rate | Re-reads of already-read files; re-attempts of eliminated approaches |
| Decision preservation | Fraction of pre-compaction decisions still correctly recalled |
| Rewrite tokens/session | Total `w · (n − p_min)` paid |
| Rehydration budget | Tokens spent restoring context |
| Retrieval hit rate | How often `expand`/`re_read` is called, and whether it prevented a re-read |
| **Compaction pause** | Wall-clock of the summarization call; target O(delta) under frontier advancement (O5) |
| **Residual span at compaction** | Tokens between frontier N and the compaction point — the direct driver of pause time |
| **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### §11.3 — guardrails the status latency section renders against

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §11.4 — the bloom watch-for the sketch section renders

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### §12 — the two risk rows that name `/qompack:status`

| Risk | Severity | Mitigation |
|---|---|---|
| Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |
| Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |

> **On any `SevCritical` failure:** … `/qompack:status` leads with a banner naming the failed assertion, what was expected, and what was observed. **Nothing fails silently — that is the whole point of the mechanism.** (00-ARCHITECTURE §12.1)

> `ModeDegradedPassive` behaviour: L0 and L1 keep running … Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`, no scheduler-initiated checkpoints, no drop report. MCP retrieval tools stay available, because they are pull-based and cannot make anything worse. … (00-ARCHITECTURE §12.1)

### Phase 1 exit criterion (rendered as a PASS/FAIL note on the dedup line)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions … hook p99 < 15ms.

### 00-ARCHITECTURE §2.4 — the latency budgets the status table renders against

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-A** | `hook_controlled` — client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (§11.3 L0) | CI on linux/macos/windows, 5 000 iterations |
| **B-B** | `l0_ingest` — daemon read → WAL append returned | p99 < 2 ms | daemon self-metrics + CI |
| **B-C** | `l0_process` — WAL → fully chunked, stored, DAG/sketches updated (async) | p99 < 50 ms | soft |
| **B-D** | `hook_wall` — includes host process creation | reported, not gated; tracked in `/qompack:status` and the bench artifact | — |
| **B-E** | `checkpoint_finalize` — `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | CI |
| **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

**Plus B-G, which §2.4 does not list and `internal/obs` does.** `obs.Budgets()` returns seven entries, not six: B-A…B-F followed by `{ID: "B-G", Hist: "hook_degraded", Pct: 99, Gated: false, Limit: runtime.budgets.hookDegradedMs}` (default 1 000 ms). B-G bounds the synchronous spool append a hook pays inside `ipc.Client.Send` when the daemon is unreachable — the one step of `Send` no deadline governs, and the exact path on which B-A's daemon-anchored `hook_controlled` series has no sample by construction. It is **reported only, structurally**: `hook_degraded` is written only by a hook process's own per-process registry, which is never persisted, and a sample can exist only while the daemon is down, so no production evaluator can ever see one. `/qompack:status` therefore renders B-G exactly the way it renders B-D — as a reported row, never as a gate — and that is the whole of SP-14's obligation to it.

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-G** | `hook_degraded` — the spool append inside `ipc.Client.Send` when the daemon is unreachable | `runtime.budgets.hookDegradedMs` (1 000 ms), reported, not gated; rendered in `/qompack:status` | — |

### 00-ARCHITECTURE §5.17 — the interface this subplan implements (normative)

```go
type Command interface {
    Name() string                                  // status|recall|pin|checkpoint|why|dropped|eval
    Run(ctx context.Context, args []string, out io.Writer) error
}
func All(d Deps) []Command
type Deps struct {
    Store store.Store; Ledger negknow.Ledger; Checkpoints checkpoint.Reader
    Writer checkpoint.Writer; Pins pins.Store; Sched scheduler.Runtime
    Eval eval.Harness; Metrics obs.Registry; Contract contract.Monitor; Cfg config.Config
}
```

> `/qompack:status` output is the observability surface (G8.1): mode (`full`/`degraded-passive`), contract-assertion table, store size + dedup ratio, sketch fill ratios and estimated FP rate, hook latency p50/p99 per hook against budgets B-A/B-D, frontier turn and residual tokens, last checkpoint seq/size, last scheduler `Decision.Breakdown`, GC stats, and the last five `Loud` messages.

### 00-ARCHITECTURE §3.4 — the plugin command files this subplan fills in

> `plugin/commands/*.md` — seven files, one per §7.5 command, each a frontmatter'd prompt that shells out to the corresponding `qompack` subcommand.

### Config keys this slice reads (Appendix C + §11.5, verbatim values)

```jsonc
"store":      { "retention": { "days": 30, "sessions": 10 } }
"checkpoint": { "budgetTokens": 12000 }
"sketches":   { "bloom": { "capacity": 10000, "fpRate": 0.01 } }
"eliminations": { "requireEvidence": true, "defaultScope": "session" }
"retrieval":  { "ephemeralResults": true, "defaultSpan": "minimal" }
"eval":       { "replayOnPhaseGate": true, "minSessions": 20 }
"runtime":    { "hotPath": { "budgetMs": 15 }, "logging": { "level": "info" } }
```

---

## Out of scope

| Item | Owner |
|---|---|
| The MCP server, the eight tool handlers, `mcp.RegisterAll`, ephemeral tagging, minimal-span resolution, the `Promoter` | **SP-13** |
| The checkpoint schema, `Writer.Begin/Advance/Finalize`, `Truncate`, `ExtractDecisions`, `FocusInstructions`, `pins.Store` implementation | **SP-10** |
| `rehydrate.DropReporter` and everything it reports on | **SP-11** |
| `scheduler.Evaluate`, `Runtime`, BOCD, p-selection, `Decision.Breakdown` computation | **SP-12** |
| The elimination ledger, descriptor canonicalization, staleness, bloom rebuild | **SP-09** |
| `store.Stats`, `store.GC` mechanics, `SegmentLog`, dedup ratio computation | **SP-06** |
| `obs.Registry`, `Histogram`, `HistSnapshot`, the budget histograms and their sampling; `logging.Loud` and `LOUD.log` writing | **SP-01** (+ **SP-05** for the hot-path histograms) |
| `contract.Monitor`, `StandardAssertions`, the degradation state machine | **SP-05** |
| `eval.Harness`, `Belady`, `ScoreRun`, `Report`, `Synthesize`, the synthetic corpus, the replay-gate | **SP-02** |
| `qompack config print/schema`, `docs/config-reference.md`, `gen-config-docs` | **SP-01** (generator) / **SP-18** (prose) |
| `qompack fsck`, `qompack doctor`, `qompack bench`, packaging, the `plugin/bin` launcher shim | **SP-17** |
| `docs/user-guide.md`, `docs/troubleshooting.md` ("how to read `/qompack:status`"), `docs/mcp-tools.md` | **SP-18** |
| **Ownership override for `docs/commands.md`.** 00-ARCHITECTURE §3.1 files it under the SP-18 `docs/` tree, but it is a **generated** artefact, not prose: SP-14 owns its generator (`pluginmanifest.CommandDocs()`), the committed file, and the CI staleness gate. SP-18 may link to it and must never hand-edit it. | **SP-14** (generator + file) / **SP-18** (links only) |
| The `qompack checkpoint` **hook** subcommand (PreCompact client) — SP-14 adds the separate `checkpoint-now` subcommand and never edits the hook path | **SP-05** (client) / **SP-10** (semantics) |
| Analyzer selection, Sequitur thrash warnings, Phase-7 promotion | **SP-15**, **SP-16** |

---

## Interface contract

### Consumes (exact signatures from 00-ARCHITECTURE §5; call sites only, no edits)

```go
// §5.8 store
store.Store.Stats(ctx context.Context) (store.Stats, error)
store.Store.GC(ctx context.Context, p store.GCPolicy) (store.GCReport, error)
store.Store.PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error)
store.Store.FileHistory(ctx context.Context, path string) ([]store.FileVersion, error)
store.Store.Segments() store.SegmentLog
store.SegmentLog.Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
store.SegmentLog.Unencoded(ctx context.Context, s core.SessionID) ([]store.Segment, error)
// types: store.Stats{Objects int; Bytes, RawBytes int64; DedupRatio float64; ToolUses, Segments, Files int; Sketches map[string]int}
//        store.GCPolicy{RetainDays, RetainSessions int; DryRun bool; Deadline time.Duration}
//        store.GCReport{ScannedObjects, LiveObjects, DeletedObjects int; BytesFreed int64; Roots int; Duration time.Duration; Truncated bool}
//        store.Segment{ID core.SegmentID; StartTurn, EndTurn core.TurnIndex; Tokens core.Tokens; EncodedOnce, Closed bool; ...}

// §5.10 negknow
negknow.Ledger.Record(ctx context.Context, r negknow.Record) (string, error)
negknow.Ledger.Health() negknow.Health
negknow.Canonicalize(target, approach, reason string) negknow.Descriptor
// negknow.Health{Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool}

// §5.14 checkpoint + pins
checkpoint.Reader.List(ctx context.Context) ([]checkpoint.Ref, error)
checkpoint.Writer.Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src checkpoint.SourceSet) (*checkpoint.Draft, error)
checkpoint.Writer.Advance(ctx context.Context, d *checkpoint.Draft, segs []core.SegmentID) (core.TurnIndex, error)
checkpoint.Writer.Finalize(ctx context.Context, d *checkpoint.Draft, budget core.Tokens) (checkpoint.Ref, error)
checkpoint.Writer.Abort(d *checkpoint.Draft) error
pins.Store.Add(ctx context.Context, inv pins.Invariant) error
pins.Store.Remove(ctx context.Context, id string) error
pins.Store.All(ctx context.Context) ([]pins.Invariant, error)
pins.Store.Materialize(ctx context.Context) error

// §5.13 scheduler
scheduler.Runtime.Evaluate(ctx context.Context) (scheduler.Decision, error)
// scheduler.Decision{ShouldCompact bool; Reasons []TriggerReason; P Candidate; PScore float64;
//                    Breakdown map[string]float64; Urgency Urgency; TTL TTLState;
//                    YoungDalySeconds float64; Background []BackgroundTask;
//                    SoftFloorTokens, HardCeilingTokens core.Tokens}

// §5.16 mcp — handlers are invoked in-process; no server is started
mcp.NewServer(name, version string, log logging.Logger) mcp.Server
mcp.RegisterAll(s mcp.Server, d mcp.ToolDeps) error
mcp.Server.Tools() []mcp.Tool          // Tool.Handler is exported; SP-14 calls it directly
mcp.Handler = func(ctx context.Context, r mcp.Request) (mcp.Response, error)

// §5.18 eval
eval.Harness.Load(dir string) ([]eval.Session, error)
eval.Harness.Replay(ctx context.Context, s eval.Session, p eval.Policy, o eval.ReplayOptions) (eval.Run, error)
eval.Harness.Belady(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error)
eval.Harness.ScoreRun(r eval.Run, opt map[core.TurnIndex]eval.KeepSet) eval.Score
eval.Harness.Report(ctx context.Context, scores map[string][]eval.Score) (eval.Report, error)

// §5.19 contract, §5.2 obs/logging, §5.4 ipc
contract.Monitor.Mode() contract.Mode
contract.Monitor.Report() []contract.Result
obs.Registry.Hist(name string) obs.Histogram        // Histogram.Snapshot() obs.HistSnapshot
ipc.Resolve(projectRoot string) (ipc.Addr, error)
ipc.NewClient(addr ipc.Addr, spool ipc.SpoolWriter, log logging.Logger, m obs.Registry) ipc.Client
ipc.Client.Send(ctx context.Context, req ipc.Request, deadline time.Duration) (ipc.Response, error)
```

### Produces (relied on by SP-17 `doctor`/`fsck`, SP-18 docs, and the CI `plugin-validate`/`docs` jobs)

```go
// package commands  (all new; the §5.17 Command/All/Deps shapes are preserved verbatim)
func All(d Deps) []Command
func Names() []string                                        // §7.5 order
func Dispatch(ctx context.Context, name string, args []string, out io.Writer, d Deps) error
func ExitCode(err error) int                                 // 0 ok, 2 usage, 1 runtime
var ErrUsage = errors.New("qompack: usage")

type StatusSnapshot struct { /* fully specified below */ }
type SnapshotSources struct { /* fully specified below */ }
type StatusFull struct { /* the six extended sections; fully specified below */ }

// OpStatusFull is SP-14's own op. It is NOT added to ipc.KnownOps — that vocabulary is pinned at
// thirteen entries by internal/ipc's own test — and nothing on this path consults ipc.Op.Valid:
// daemon.buildRoutes copies every Options-registered op into d.routes verbatim, dispatchOp looks
// the op up in that map, and ipc.Client.Send never validates. ipc.OpStatus stays SP-05's.
const OpStatusFull = ipc.Op("status.full")

func Collect(ctx context.Context, src SnapshotSources) StatusSnapshot        // local/disk fallback
func CollectFull(ctx context.Context, src SnapshotSources) StatusFull        // the six extended sections
func NewStatusFullOpHandler(src SnapshotSources) ipc.Handler                 // registered by cli at daemon start
func FetchSnapshot(ctx context.Context, d Deps) (StatusSnapshot, bool)       // bool = came from daemon
// daemonStatus (package-private, statusfetch.go) is the JSON-tag mirror of the subset of
// daemon.StatusSnapshot SP-14 reads. It is a mirror by choice, not by compulsion: §3.2's
// composition-roots row does not decide an edge between two of its own members and the shipped
// import-graph check skips a root's imports entirely (see the import-direction rule below), so
// the mirror is what decouples the wire schema from the daemon's internals;
// TestStatus_DaemonPayloadMirrorIsCurrent pins it against a payload
// produced by the real daemon, so the mirror cannot drift silently.
func RenderStatus(w io.Writer, s StatusSnapshot) error        // deterministic human text
func RenderJSON(w io.Writer, command string, data any) error  // the shared envelope

// package pluginmanifest  (EXTENDS the shipped manifest.go; no new generator, no new file)
type CommandFlag struct{ Name, Type, Default, Help string }
type CommandDoc struct {                       // the shipped type, with four SP-14 fields appended
    Name, Description, ArgumentHint, Subcommand, AllowedTools string   // shipped, unchanged
    Summary  string                            // SP-14
    Usage    string                            // SP-14
    Flags    []CommandFlag                     // SP-14
    Sections []string                          // SP-14
}
func Commands() []CommandDoc                   // a copy of the shipped commandSpecs, AllowedTools filled
func CommandNames() []string
func SubcommandFor(name string) string
func CommandDocs() []byte                      // exact bytes of docs/commands.md
// renderCommand and (Manifest).Files stay the SINGLE producer of plugin/commands/<name>.md, so
// pluginmanifest.Validate and `devtool plugin-validate --write` keep working unchanged.

// package cli  (new file slash.go, plus a table edit in commands.go and a 3-line edit in dispatch.go)
func slashCmds() []Cmd                                       // the seven §7.5 Cmd entries
func runSlash(ctx context.Context, name string, env Env, args []string, out, errw io.Writer) error
```

**Import-direction rule this subplan obeys and states normatively:** `internal/cli` imports `internal/commands`; `internal/commands` imports **neither** `internal/cli` nor `internal/daemon`. 00-ARCHITECTURE §3.2 groups `daemon`, `cli`, `commands`, `testutil` and `cmd/qompack` in a single "composition roots — may import anything; nothing may import them" row: *"may import anything"* covers the other members of that row (this is already how `cmd/qompack` imports `cli`), and *"nothing may import them"* binds the non-composition-root packages. The shipped check already permits the edge and will not need touching: `tools/devtool/importrules.go:37-38` declares both `cli` and `commands` as composition roots, and `tools/devtool/importgraph.go:179-180` skips a root's own imports entirely, so `cli → commands` is never examined. Inverting the dependency would in any case put command rendering inside `cli` and make `commands` unreachable from tests. `internal/pluginmanifest` is a foundation-level package and may not import `internal/commands` (nothing may import a composition root, §3.2) — which is exactly why the command table lives in `pluginmanifest` and `commands` imports it, not the reverse. That clause does **not** reach `commands → daemon`, which is an edge between two members of the composition-roots row and is therefore undecided by §3.2 and unexamined by the check. `commands` nevertheless never names `daemon.StatusSnapshot` as a Go type — it decodes the daemon's reply through its own JSON-tag mirror (`daemonStatus`) — as a deliberate decoupling choice, so that a daemon-side field rename fails `TestStatus_DaemonPayloadMirrorIsCurrent` by name instead of silently emptying four status sections. See the `daemonStatus` note under `statusfetch.go` step 3. The `status.full` handler is a value produced by `commands` and registered by `cli` through SP-05's `daemon.Options.Handle(op, h)` seam, so `internal/daemon` is never edited and `ipc.OpStatus` keeps SP-05's own `d.handleStatus`.

**Additive `Deps` fields.** SP-14 owns `internal/commands` and therefore extends its own `Deps` struct. The ten fields of §5.17 are unchanged in name, type and order; the following are appended. Every one is nil-tolerant — a nil member means "that section is unavailable", never a panic (00-ARCHITECTURE §5.4 `Services` rule).

```go
type Deps struct {
    // ── §5.17, unchanged ──
    Store store.Store; Ledger negknow.Ledger; Checkpoints checkpoint.Reader
    Writer checkpoint.Writer; Pins pins.Store; Sched scheduler.Runtime
    Eval eval.Harness; Metrics obs.Registry; Contract contract.Monitor; Cfg config.Config
    // ── SP-14 additive ──
    Graph       dag.Graph            // checkpoint.SourceSet assembly
    Grammar     grammar.Sequitur     // checkpoint.SourceSet assembly
    Tokens      tokens.Estimator     // checkpoint.SourceSet assembly
    MCP         mcp.ToolDeps         // recall / why / dropped delegate to these handlers
    Policies    []eval.Policy        // eval policy set, supplied by the composition root
    ProjectRoot string
    Session     core.SessionID       // "" ⇒ resolved at run time (see checkpoint-now)
    Log         logging.Logger
    Clock       core.Clock           // ALL durations and timestamps in output come from here
    Getenv      func(string) string  // nil ⇒ os.Getenv; cli sets it from Env.Getenv
}
```

`Getenv` mirrors `cli.Env.Getenv` for exactly the reason SP-01 introduced that field: the two environment reads this package makes (`QOMPACK_SESSION_ID`, `QOMPACK_SESSIONS_DIR`) must be injectable so no test has to mutate the real process environment. A package-private `d.getenv(k)` falls back to `os.Getenv` when the field is nil, so a bare `Deps{}` still behaves.

---

## Implementation spec

### Global rules for every command in this package

1. **No ANSI colour, ever.** Output is read by a model and by golden tests. Colour is not emitted, not even when `os.Stdout` is a TTY.
2. **All time comes from `d.Clock`.** `d.Clock.Now()` for timestamps (rendered `time.RFC3339` in UTC), `d.Clock.Since(t)` for durations. Under `testutil.FakeClock` this makes every byte of output deterministic. `time.Now()` must not appear in `internal/commands` — a `go vet`-adjacent grep in the package test asserts this.
3. **All maps are rendered sorted by key.** `Decision.Breakdown`, `Stats.Sketches` and the JSON of both are emitted through a sorted key slice.
4. **Fixed numeric formatting.** `formatBytes`: `< 1024` → `"%d B"`; otherwise KB/MB/GB/TB base 1024 → `"%.1f KB"`. `formatDuration`: `< 1ms` → `"%.0fµs"`; `< 1s` → `"%.1fms"`; otherwise `"%.2fs"`. Ratios `"%.2f:1"`, fill ratios `"%.3f"`, FP rates `"%.4f"`, scores `"%.2f"`.
5. **`--json` envelope**, written with `json.Encoder` configured `SetEscapeHTML(false)` + `SetIndent("", "  ")`, one trailing newline:
   ```go
   type Envelope struct {
       Command string          `json:"command"`
       Schema  int             `json:"schema"`   // 1
       OK      bool            `json:"ok"`
       Error   string          `json:"error,omitempty"`
       Data    json.RawMessage `json:"data"`
   }
   ```
   Struct field order in Go's `encoding/json` is declaration order, so the byte output is stable.
6. **Exit codes.** `ExitCode(nil) == 0`; `errors.Is(err, ErrUsage) → 2`; anything else → 1. Hook subcommands are untouched by this rule and still always exit 0 (00-ARCHITECTURE §2.3).
7. **`--help` on every command** prints `usage: qompack <subcommand> <usage-string>` followed by one line per flag (`  --name  <type>  (default <d>)  <help>`), sourced from `pluginmanifest.Commands()`, and returns nil (exit 0). Both `--help`/`-help` (the registered bool flag) and `-h` (which the `flag` package reports as `flag.ErrHelp`) take this same path and return nil — `parse` maps `flag.ErrHelp` to `errHelpRequested`, a package-private sentinel the command turns into "print usage, return nil", **not** into `ErrUsage`.
8. **Where errors go, in both modes — normative, because every golden depends on it.** Nothing in `internal/commands` writes to `os.Stderr` or `os.Stdout`; everything goes to the `out` writer handed to `Run`.
   - **Human mode.** The command writes one line `<COMMAND>  error: <err>` to `out`, then returns the error. For `errors.Is(err, ErrUsage)` it instead writes the same usage block `--help` writes, prefixed by `usage error: <err>`, and returns the wrapped `ErrUsage`.
   - **`--json` mode.** The command writes `renderJSONError(out, name, err)` — a complete `Envelope` with `ok:false`, `error:<err.Error()>`, `data:null` — and returns the error. **A non-zero exit is always still accompanied by a valid envelope on stdout**; that pair is what `TestProperty_EnvelopeAlwaysValidJSON` asserts.
   - `cli`'s `runSlash` adapter prints nothing of its own; it returns the error, tagging it `errAlreadyReported` so `cli.Dispatch` does not print a second line, and `errUsage` when `errors.Is(err, ErrUsage)` so `Dispatch` maps it to exit 2. `commands.ExitCode` remains the in-package statement of the same policy and is what the `commands` tests assert against.
9. **Nil-`Deps` discipline — no command may panic on a missing component.** Every command begins with a `require` check naming the exact `Deps` members it needs, and returns `fmt.Errorf("%w: /qompack:%s needs %s, which failed to open (see .qompack/logs)", core.ErrNotFound, name, member)` (exit 1) when one is nil. The required sets are fixed:
   | Command | Required `Deps` members |
   |---|---|
   | `status` | none — every section degrades to `unavailable` independently; `status` must run on a completely broken project |
   | `recall`, `why`, `dropped` | `MCP` (its own required members are SP-13's problem), `Log` |
   | `pin` (default / `--list` / `--remove`) | `Pins`, `Clock` |
   | `pin --eliminated` | `Ledger`, `Clock`, `Cfg`; plus `Store` only when evidence must be synthesized or `--depends-on` is given |
   | `checkpoint` | `Writer`, `Checkpoints`, `Store`, `Pins`, `Ledger`, `Graph`, `Grammar`, `Tokens`, `Clock` |
   | `eval` | `Eval`, `Cfg` |

### `internal/pluginmanifest/manifest.go` (modified — the shipped command table, extended in place)

**Decision — extend, never duplicate.** `internal/pluginmanifest` already is the single command table and already gates it. `manifest.go` declares `var commandSpecs = []CommandDoc{…}` (all seven, §7.5 order), `func renderCommand(c CommandDoc) []byte`, and `func (m Manifest) Files() (map[string][]byte, error)` keying them repo-relative as `plugin/commands/<name>.md`; `Validate(dir, m)` reports `"content differs"` per file and `tools/devtool/pluginvalidate.go` fails the task on any diff. A second generator emitting different bytes without retiring the first would make `plugin-validate` report seven `content differs` diffs, so SP-14's own clean-diff gate in commit 8 could not pass. **SP-14 therefore appends its four fields to the shipped `CommandDoc`, fills them in `commandSpecs`, and replaces `renderCommand`'s body — `Manifest.Files()` remains the one and only producer of the seven files. There is no `CommandFiles()` and no `CommandMarkdown()`.**

```go
type CommandFlag struct{ Name, Type, Default, Help string }

// CommandDoc: the five shipped fields, unchanged in name, type and order, plus SP-14's four
// appended after them.
type CommandDoc struct {
    Name         string        // §7.5 slash-command name              (shipped)
    Description  string        // frontmatter description               (shipped)
    ArgumentHint string        // frontmatter argument-hint             (shipped)
    Subcommand   string        // argv[1] of the binary                 (shipped)
    AllowedTools string        // filled by Default(); see below        (shipped)
    Summary      string        // one sentence, ends with a period      (SP-14)
    Usage        string        // e.g. "<query> [--k N] [--json]"       (SP-14)
    Flags        []CommandFlag //                                       (SP-14)
    Sections     []string      // Qompack.md sections surfaced          (SP-14)
}

func Commands() []CommandDoc              // a defensive copy of commandSpecs with AllowedTools filled
func CommandNames() []string
func SubcommandFor(name string) string    // "" if unknown
func CommandDocs() []byte                 // exact bytes of docs/commands.md
```

`Description` and `Summary` are kept as two fields rather than collapsed, because the frontmatter `description:` line is already committed in seven files and its bytes are diffed by `plugin-validate`; `Summary` is the longer sentence the `--help` block and `docs/commands.md` render. Where a row below gives only a summary, `Description` keeps its shipped value verbatim.

`Default(version)` keeps filling `AllowedTools`, with one substitution: `Bash(qompack <sub>:*)` becomes `Bash(${CLAUDE_PLUGIN_ROOT}/bin/qompack <sub>:*)`, built from the existing `binaryRef` constant rather than a new literal. `commandSpecs`' `checkpoint` row changes `Subcommand` from `"checkpoint"` to `"checkpoint-now"` (00-ARCHITECTURE §2.3 binds bare `qompack checkpoint` to the PreCompact hook client). Both changes regenerate committed bytes, which is why the seven `plugin/commands/*.md` files are listed as **regenerated** in commit 8, not as untouched.

`Commands()` returns exactly these seven, in this order (the §7.5 order):

| Name | Subcommand | Usage | ArgHint | Summary | Sections |
|---|---|---|---|---|---|
| `status` | `status` | `[--json] [--no-gc] [--section NAME]...` | `[--json] [--section NAME]` | `Show the Qompack observability surface: mode, contracts, store, sketches, latency, frontier, checkpoint, scheduler, GC and loud messages.` | §8.1, §11, §12 |
| `recall` | `recall` | `<query> [--k N] [--json]` | `<query>` | `Search the Qompack store by content, path or symbol and return hashes and summaries.` | §8.7 |
| `pin` | `pin` | `<text> \| --list \| --remove ID \| --eliminated --target T --reason R <approach> [--json]` | `<text> or --list or --eliminated --target T --reason R <approach>` | `Pin an invariant verbatim, or record an eliminated approach in the negative-knowledge ledger.` | §7.4, §8.3 |
| `checkpoint` | `checkpoint-now` | `[--session ID] [--budget N] [--json]` | `[--session ID]` | `Write an immutable, importance-ordered checkpoint from the store on demand.` | §8.5 |
| `why` | `why` | `<decision_id> [--json]` | `<decision_id>` | `Retrieve a recorded decision, its rationale and its evidence.` | §8.7 |
| `dropped` | `dropped` | `[--json]` | `` | `List the rules, nested CLAUDE.md files and skills that are currently out of context.` | §8.6, §8.7 |
| `eval` | `eval` | `[--corpus DIR] [--policy NAME]... [--baseline NAME] [--k N] [--budget N] [--seed N] [--json]` | `[--corpus DIR] [--policy NAME]` | `Replay the corpus and print the fraction-of-Belady-OPT report with the regression table.` | §6.10, §11.1 |

`CommandDoc.Flags` is enumerated exhaustively below — `--help` output, `docs/commands.md` flag tables and the `flag.FlagSet` registration all read this one list, so it is normative. Every command additionally declares `{Name: "json", Type: "bool", Default: "false", Help: "emit the machine-parseable Envelope instead of human text"}` and `{Name: "help", Type: "bool", Default: "false", Help: "print usage and exit 0"}`; those two are appended by `Commands()` to every spec and are **not** repeated per row.

| Command | Name | Type | Default | Help |
|---|---|---|---|---|
| `status` | `no-gc` | `bool` | `false` | skip the dry-run GC probe (the GC section reports as unavailable) |
| `status` | `section` | `string (repeatable)` | `""` | render only these sections: mode contracts store sketches latency frontier checkpoint scheduler gc loud daemon |
| `recall` | `k` | `int` | `5` | number of hits to return (§8.7 `recall(query, k=5)`) |
| `pin` | `list` | `bool` | `false` | list the pinned invariants instead of adding one |
| `pin` | `remove` | `string` | `""` | remove the invariant with this id (writes a tombstone, never a rewrite) |
| `pin` | `eliminated` | `bool` | `false` | record an eliminated approach instead of an invariant (§8.3 source 2) |
| `pin` | `target` | `string` | `""` | elimination target, e.g. `src/auth.ts:refreshToken` (required with `--eliminated`) |
| `pin` | `reason` | `string` | `""` | why the approach was eliminated (required with `--eliminated`) |
| `pin` | `scope` | `string` | `session` | `session` or `project` (§8.3 item 5; default is `eliminations.defaultScope`) |
| `pin` | `evidence` | `string` | `""` | `sha256:…` hash of the evidence; synthesized from `--reason` when omitted |
| `pin` | `depends-on` | `string (repeatable)` | `""` | file whose version the elimination's reason rests on (staleness guard, §8.3) |
| `checkpoint` | `session` | `string` | `""` | session id to checkpoint (see the resolution order below) |
| `checkpoint` | `budget` | `int` | `12000` | token budget for `Finalize` (default `checkpoint.budgetTokens`) |
| `eval` | `corpus` | `string` | `""` | session corpus directory (see the resolution order below) |
| `eval` | `policy` | `string (repeatable)` | `""` | restrict the run to these policy names (default: all registered) |
| `eval` | `baseline` | `string` | `stock` | policy to treat as the baseline row and regression reference |
| `eval` | `k` | `int` | `20` | divergence horizon: turns compared after each compaction (§4.2 "next *K* actions") |
| `eval` | `budget` | `int` | `12000` | keep-set token budget (default `checkpoint.budgetTokens`) |
| `eval` | `seed` | `int64` | `1` | RNG seed passed to `eval.ReplayOptions.Seed` |

Defaults shown as `12000` are rendered in the docs and `--help` from `config.Defaults()` at generation time, never written as a literal in `internal/commands` (00-ARCHITECTURE §11.6 forbids the literal `12000` outside `internal/config/defaults.go`; `pluginmanifest` reads it from `config.Defaults()`).

**Why `checkpoint` maps to `checkpoint-now`.** 00-ARCHITECTURE §2.3 already binds `qompack checkpoint` to the PreCompact hook client. The slash command therefore resolves to `checkpoint-now`, and the mapping is data in this table rather than a special case in any dispatcher. `commands.Dispatch` accepts either spelling.

`renderCommand(c)` — the shipped function, its body replaced — renders, byte-for-byte (LF endings, single trailing newline; `%s` substitutions from the spec):

```
---
description: <c.Description>
argument-hint: "<c.ArgumentHint>"
allowed-tools: <c.AllowedTools>
---

<c.Summary>

Run the command below and report its output to the user **verbatim**. Do not re-format,
re-order, summarise, or truncate the tables — they are deterministic and machine-parseable by
design (Qompack.md §11, §12). If the command fails, show its stderr verbatim and stop.

!`${CLAUDE_PLUGIN_ROOT}/bin/qompack <c.Subcommand> $ARGUMENTS`
```

`Manifest.Files()` keeps keying these `plugin/commands/<name>.md` (repo-relative, forward slashes), unchanged — that is what `pluginmanifest.Validate` walks and what `devtool plugin-validate --write` materializes. `CommandDocs()` renders `docs/commands.md`: an H1 `# Qompack slash commands`, a generated-file warning line, one table row per command (`| `/qompack:name` | `qompack subcommand` | summary |`), then one `##` section per command containing usage, the flag table, and the design sections it surfaces.

### `internal/commands/spec.go`

`Names()` → `pluginmanifest.CommandNames()`. `specFor(name string) pluginmanifest.CommandDoc` panics only on a programmer error (unknown constant), never on user input.

### `internal/commands/flags.go`

```go
type common struct{ JSON bool }
func newFlagSet(spec pluginmanifest.CommandDoc, out io.Writer) (*flag.FlagSet, *common)
```
Creates `flag.NewFlagSet(spec.Subcommand, flag.ContinueOnError)`, sets `fs.SetOutput(io.Discard)` (usage is rendered by us), registers `--json`, and registers `--help` as a bool. `parse(fs, args)` maps `flag.ErrHelp` and any parse error to `fmt.Errorf("%w: %v", ErrUsage, err)`. A repeatable string flag is `type stringList []string` implementing `flag.Value` (`String()` joins with `,`; `Set` appends).

### `internal/commands/envelope.go`

`RenderJSON(w io.Writer, command string, data any) error` marshals `data` into `Envelope{Command: command, Schema: 1, OK: true, Data: …}`. `renderJSONError(w, command string, err error) error` emits `OK:false, Error: err.Error(), Data: json.RawMessage("null")`.

### `internal/commands/limits.go` — every constant this package owns, in one file

Nothing below duplicates an Appendix C default, so none of them trips the `nomagic` pass (§11.6 forbids only the literals in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` and `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}`). Anything that *is* an Appendix C value — the 12000 token budget, the 30/10 retention pair, the 0.01 bloom FP rate, the 15 ms hot-path budget, the 20-session minimum — is read from `d.Cfg` at the use site and never appears as a literal here.

```go
const (
    envelopeSchema        = 1                        // Envelope.Schema and StatusSnapshot.Schema
    phase1DedupRatio      = 4.0                      // §10 Phase 1 exit criterion, "≥ 4:1"
    saturationMultiple    = 10                       // §11.4 "At 1% they are safe; at 10% …"
    recallDefaultK        = 5                        // §8.7 recall(query, k=5)
    evalDefaultK          = 20                       // §4.2 divergence horizon, "next K actions"
    evalDefaultSeed       = 1
    evalDefaultBaseline   = "stock"
    loudTailBytes         = 64 << 10
    loudTailCount         = 5                        // §5.17 "the last five Loud messages"
    loudLineMaxRunes      = 160
    gcProbeDeadline       = 250 * time.Millisecond
    statusDeadline        = 1500 * time.Millisecond
    mcpDeadline           = 10 * time.Second
    checkpointFinalizeBudget = 2 * time.Second       // B-E render default, §11.3 "< 2s (L4)"
    budgetIngestMs        = 2.0                      // B-B render default
    budgetProcessMs       = 50.0                     // B-C render default (soft)
    budgetMCPCallMs       = 250.0                    // B-F render default, p95
)
// sectionOrder is the ONE definition of section order. RenderStatus, filterSections,
// Unavailable sorting and the --section validator all read it; nothing re-lists these names.
var sectionOrder = []string{"mode","contracts","store","sketches","latency",
                            "frontier","checkpoint","scheduler","gc","loud","daemon"}
```

The four `budget*`/`checkpointFinalizeBudget` values are **render defaults, not the live limits**. Every §2.4 budget except B-A and B-D does have a config key — `runtime.budgets.{l0IngestMs, l0ProcessMs, checkpointFinalizeMs, mcpToolCallMs}`, plus `runtime.budgets.hookDegradedMs` for B-G — and the live limit always comes from `obs.Budgets()`'s `Budget.Limit(src.Cfg)`. These constants are used only where no `Cfg` is attached to the snapshot being rendered (a hand-built `StatusSnapshot` in a golden test, a `--json` payload replayed without its config), so `RenderStatus` never prints an empty budget column. Each carries the `//nomagic:allow B-E render default, 00-ARCH §2.4` style comment if the pass ever grows to cover them.

### `internal/commands/statussnapshot.go` — the observability record (byte-for-byte JSON shape)

```go
type StatusSnapshot struct {
    Schema      int            `json:"schema"`        // 1
    ProjectRoot string         `json:"project_root"`
    Session     core.SessionID `json:"session"`
    Collected   string         `json:"collected"`     // RFC3339 UTC from Clock
    FromDaemon  bool           `json:"from_daemon"`
    Mode        ModeInfo       `json:"mode"`
    Contracts   []ContractRow  `json:"contracts"`
    Store       StoreInfo      `json:"store"`
    Sketches    SketchInfo     `json:"sketches"`
    Latency     []LatencyRow   `json:"latency"`
    Frontier    FrontierInfo   `json:"frontier"`
    Checkpoint  CheckpointInfo `json:"checkpoint"`
    Scheduler   SchedulerInfo  `json:"scheduler"`
    GC          GCInfo         `json:"gc"`
    Loud        []LoudMessage  `json:"loud"`
    Daemon      DaemonInfo     `json:"daemon"`
    Unavailable []string       `json:"unavailable"`   // section names that could not be collected
    Filtered    []string       `json:"filtered"`      // section names suppressed by --section (zeroed above)
}

type ModeInfo struct {
    Mode      string `json:"mode"`            // contract.Mode.String()
    Degraded  bool   `json:"degraded"`
    Reason    string `json:"reason,omitempty"`
    Assertion string `json:"assertion,omitempty"`
    Expected  string `json:"expected,omitempty"`
    Observed  string `json:"observed,omitempty"`
}
type ContractRow struct {
    ID string `json:"id"`; OK bool `json:"ok"`; Severity string `json:"severity"` // info|warn|crit
    Expected string `json:"expected"`; Observed string `json:"observed"`; Detail string `json:"detail,omitempty"`
}
type StoreInfo struct {
    Objects int `json:"objects"`; Bytes int64 `json:"bytes"`; RawBytes int64 `json:"raw_bytes"`
    DedupRatio float64 `json:"dedup_ratio"`; MeetsPhase1 bool `json:"meets_phase1_ratio"` // ≥ 4.0
    ToolUses int `json:"tool_uses"`; Segments int `json:"segments"`; Files int `json:"files"`
    SketchCounts []KV `json:"sketch_counts"`     // sorted by Key
}
type KV struct{ Key string `json:"key"`; Value int `json:"value"` }
type SketchInfo struct {
    BloomRecords int `json:"bloom_records"`; BloomActive int `json:"bloom_active"`; BloomStale int `json:"bloom_stale"`
    BloomFillRatio float64 `json:"bloom_fill_ratio"`; BloomEstFPRate float64 `json:"bloom_est_fp_rate"`
    BloomNeedsResize bool `json:"bloom_needs_resize"`; BloomConfiguredFPRate float64 `json:"bloom_configured_fp_rate"`
    SaturationWarning bool `json:"saturation_warning"`  // EstFPRate ≥ 10 × configured  (§11.4)
}
type LatencyRow struct {
    Clock   string  `json:"clock"`      // metric name, e.g. "hook_controlled.observe_tool"
    Budget  string  `json:"budget"`     // "B-A" … "B-F", or "" when the clock has no budget
    N       int64   `json:"n"`
    P50Ms   float64 `json:"p50_ms"`
    P95Ms   float64 `json:"p95_ms"`
    P99Ms   float64 `json:"p99_ms"`
    MaxMs   float64 `json:"max_ms"`
    LimitMs float64 `json:"limit_ms"`
    Gated   bool    `json:"gated"`
    Pass    bool    `json:"pass"`
}
type FrontierInfo struct {
    Turn int `json:"turn"`; ResidualTokens int `json:"residual_tokens"`
    MaxResidualTokens int `json:"max_residual_tokens"`; UnencodedSegments int `json:"unencoded_segments"`
}
type CheckpointInfo struct {
    Seq int `json:"seq"`; Path string `json:"path"`; Bytes int64 `json:"bytes"`
    Tokens int `json:"tokens"`; Frontier int `json:"frontier"`; SHA256 string `json:"sha256"`
    Count int `json:"count"`
}
type SchedulerInfo struct {
    Evaluated bool `json:"evaluated"`; ShouldCompact bool `json:"should_compact"`
    Reasons []string `json:"reasons"`; TTL string `json:"ttl"`; Urgency int `json:"urgency"`
    PPos int `json:"p_pos"`; PTurn int `json:"p_turn"`; PSegment int `json:"p_segment"`
    PCoupling int `json:"p_coupling"`; PScore float64 `json:"p_score"`
    Breakdown []FKV `json:"breakdown"`                    // sorted by Key
    YoungDalySeconds float64 `json:"young_daly_seconds"`
    SoftFloorTokens int `json:"soft_floor_tokens"`; HardCeilingTokens int `json:"hard_ceiling_tokens"`
    Background []string `json:"background"`
}
type FKV struct{ Key string `json:"key"`; Value float64 `json:"value"` }
type GCInfo struct {
    DryRun bool `json:"dry_run"`; Scanned int `json:"scanned"`; Live int `json:"live"`
    Collectable int `json:"collectable"`; BytesFreed int64 `json:"bytes_freed"`
    Roots int `json:"roots"`; DurationMs float64 `json:"duration_ms"`; Truncated bool `json:"truncated"`
    RetainDays int `json:"retain_days"`; RetainSessions int `json:"retain_sessions"`
}
type LoudMessage struct{ TS string `json:"ts"`; Message string `json:"message"`; Raw bool `json:"raw"` }
type DaemonInfo struct {
    Reachable bool `json:"reachable"`; Addr string `json:"addr"`; HotPath string `json:"hot_path"`
}
```

### `internal/commands/collect.go` — how each section is obtained

```go
type SnapshotSources struct {
    ProjectRoot string
    Session     core.SessionID
    Cfg         config.Config
    Clock       core.Clock
    Log         logging.Logger
    Store       store.Store
    Ledger      negknow.Ledger
    Checkpoints checkpoint.Reader
    Sched       scheduler.Runtime
    Metrics     obs.Registry
    Contract    contract.Monitor
    SkipGC      bool
}
func Collect(ctx context.Context, src SnapshotSources) StatusSnapshot
```

Each block is independent; a nil source or a returned error appends the section name to `Unavailable` and leaves the zero value. `Collect` never returns an error and never panics — **each of the ten blocks runs inside its own `func() { defer func(){ if r := recover(); r != nil { unavailable(name, fmt.Sprintf("panic: %v", r)); src.Log.Loud(...) } }(); … }()` closure**, so a panicking `Stats` implementation costs one section, not the process. `Unavailable` carries `"<section>: <reason>"` strings (`"store: unexpected EOF"`, `"scheduler: nil"`), and the human renderer splits on the first `": "` to fill `<NAME>\n  — unavailable (<reason>)`. `Unavailable` is sorted in the fixed section order below, never in completion order, so it is deterministic under concurrency.

1. **Mode.** `src.Contract.Mode().String()`. `Degraded = mode != "full"` (compare against `contract.ModeFull.String()`). When degraded, the banner fields are taken from the **first** `contract.Result` in `Report()` with `!OK && Severity == contract.SevCritical`, ordered by the order `Report()` returns; if none is critical, the first `!OK` row is used and `Reason` is set to `"degraded without a failing assertion (see LOUD.log)"`.
2. **Contracts.** `src.Contract.Report()` → one `ContractRow` per result, in returned order. `sevName`: `contract.SevInfo→"info"`, `SevWarn→"warn"`, `SevCritical→"crit"`.
3. **Store.** `src.Store.Stats(ctx)`. `MeetsPhase1 = DedupRatio >= phase1DedupRatio` where `const phase1DedupRatio = 4.0` (§10 Phase 1 exit criterion). `SketchCounts` = `Stats.Sketches` flattened and sorted by key.
4. **Sketches.** `src.Ledger.Health()`. `BloomConfiguredFPRate = src.Cfg.Sketches.Bloom.FPRate`. `SaturationWarning = Health.EstFPRate >= 10*BloomConfiguredFPRate` — the §11.4 watch-for stated as "At 1% they are safe; at 10% the agent starts skipping viable approaches"; the multiplier `10` is `const saturationMultiple = 10`.
5. **Latency.** For each `(clock, budgetID)` in the table below, read `src.Metrics.Hist(name).Snapshot()`; emit a row only when `N > 0`, except that the six `hook_controlled.<hook>` rows are always emitted (a hook that never fired is itself information) with `N: 0` and `Pass: true`.

   **Where the histograms come from on each path.** `obs.Registry` is daemon-resident, so on the daemon path the numbers are current — but they arrive as `daemon.StatusSnapshot.Latency`, a `map[string]obs.HistSnapshot` the daemon fills from `obs.Budgets()` plus `hook_controlled_observed`. That map is therefore keyed by exactly the seven budget clocks below plus `hook_controlled_observed`; the `hook_controlled.<hook>` and `hook_wall.<hook>` sub-rows have no key in it and render `no samples` on the daemon path. **The disk path is the same source, not a second one.** `internal/daemon/metrics.go:20-30` names `writeMetricsSnapshot` → `obs.Registry.Persist` "the ONE writer" of `.qompack/metrics/latency.json`, driven by `idleWriteMetrics` (`handlers.go:872-874`), so that file is a dump of the same daemon registry, one idle tick stale, carrying the same keys. **No producer of a `hook_controlled.<hook>` or a `hook_wall` sample exists anywhere in the tree** — `recordHotPathSample` (`internal/daemon/handlers.go:184-199`) observes only the aggregate `histName(obs.BA)` and `hook_controlled_observed`, even though `req.Op` is in scope, and `hook_wall` appears only as a constant and a doc comment (`internal/obs/budgets.go:24, :46`). So the six sub-rows and B-D's own aggregate render `no samples` on **both** paths until a producer is written, which is an unowned instrumentation gap rather than a rendering choice — see "The per-hook latency element is unfillable in today's tree" below. On the disk-fallback path `sourcesFromDeps` supplies a **read-only registry hydrated from `.qompack/metrics/latency.json`** — 00-ARCHITECTURE §3.3 defines that file as "rolling histograms for /status and bench", which is exactly this use — via a package-private `loadMetricsFromDisk(root string) obs.Registry` that unmarshals each entry into a fixed `obs.HistSnapshot` and serves `Hist(name).Snapshot()` from it. A missing or unparseable file yields an empty registry, every row reads `no samples`, and `latency` is **not** added to `Unavailable` (the section rendered correctly; it simply has nothing to report). `loadMetricsFromDisk` never writes, so looking at status cannot perturb the metrics it reports.

   | metric name | Budget | LimitMs source | Gated |
   |---|---|---|---|
   | `hook_controlled` | B-A | `Budget.Limit(Cfg)` → `runtime.hotPath.budgetMs` | yes |
   | `hook_controlled.observe_tool` … `.observe_prompt`, `.observe_stop`, `.session_start`, `.checkpoint`, `.flush` | B-A | same | yes |
   | `hook_wall` and `hook_wall.<hook>` | B-D | 0 (B-D has no config key by design) | no (reported, never gated) |
   | `l0_ingest` | B-B | `Budget.Limit(Cfg)` → `runtime.budgets.l0IngestMs` (2) | yes |
   | `l0_process` | B-C | `Budget.Limit(Cfg)` → `runtime.budgets.l0ProcessMs` (50) | no (soft) |
   | `checkpoint_finalize` | B-E | `Budget.Limit(Cfg)` → `runtime.budgets.checkpointFinalizeMs` (2000) | yes |
   | `mcp_tool_call` | B-F | `Budget.Limit(Cfg)` → `runtime.budgets.mcpToolCallMs` (250) | yes (p95, not p99) |
   | `hook_degraded` | B-G | `Budget.Limit(Cfg)` → `runtime.budgets.hookDegradedMs` (1000) | no (reported, never gated) |

   **The seven budget rows are read off `obs.Budgets()`, not re-typed here.** `Budget{ID, Hist, Pct, Gated, Limit(config.Config)}` already carries every column, in B-A…B-G order, and every `Limit` reads its own config key at call time. `LatencyRow.Budget`, `.LimitMs`, `.Gated` and the percentile `Pass` compares against are therefore `string(b.ID)`, `b.Limit(src.Cfg)`, `b.Gated` and `b.Pct` — which is why `budgetIngestMs`, `budgetProcessMs`, `checkpointFinalizeBudget` and `budgetMCPCallMs` in `limits.go` are **render defaults only**, used when a snapshot arrives with no `Cfg` attached, never as the live limit. The `.<hook>` sub-rows are the one thing not in `obs.Budgets()`: they inherit B-A's id, limit and gating from the `hook_controlled` entry.

   `Pass` compares `b.Pct` (p99 for every budget except B-F's p95) against `LimitMs`. Rows with `LimitMs == 0` always report `Pass: true` and render `budget B-D (reported, not gated)`. **B-G is the second ungated row and renders `budget B-G 1.00s (reported, not gated)` — non-zero limit, `Gated: false`, `Pass: true` unconditionally.** Its ungatedness is structural, not soft: `hook_degraded` is written only by a hook process's own registry, which `internal/cli`'s `newHookMetrics` never persists, and a sample exists only while the daemon is unreachable, so a sample and an evaluator can never coexist. `/qompack:status` on the disk path is the one place a human ever sees the number, which is exactly why the row is here. **The metric names above are SP-05's registry keys, not SP-14's invention** — `obs.Registry.Hist(name)` returns a fresh empty histogram for a name nobody registered, so a renamed or not-yet-emitted clock degrades to a `no samples` row rather than a failure. Rows are emitted in exactly the table's order; `hook_controlled.<hook>` sub-rows follow the §3.4 hook order (`observe_tool, observe_prompt, observe_stop, session_start, checkpoint, flush` — six subcommands serving the seven `hooks.json` entries, since `Stop` and `SubagentStop` share `observe stop`).
   **The per-hook latency element is unfillable in today's tree, and SP-14 cannot fill it.** §5.17 asks for "hook latency p50/p99 **per hook** against budgets B-A/B-D" (`00-ARCHITECTURE.md:1930`). Neither half of that is producible today: no code anywhere in the repository observes a `hook_controlled.<hook>` sample — `recordHotPathSample` sub-keys nothing, though `req.Op` is in scope at `internal/daemon/handlers.go:184` — and `hook_wall` (B-D) has no writer at all outside `internal/obs/obs_test.go`. Writing either producer means editing `internal/daemon` and `internal/cli`'s `newHookMetrics` (`internal/cli/hookclient.go`), and this subplan's Definition of Done forbids itself the first of those. **SP-14 therefore delivers the rendering, not the samples**: the six sub-rows and the B-D row are emitted, correctly labelled, and read `no samples` until a producer exists. **The producer is unowned** — no subplan in waves 3–5 claims it — and is recorded as such in `plans/TRACEABILITY.md`'s unowned-obligations section; closing it needs either an `arch/per-hook-latency-series` branch that sub-keys `recordHotPathSample` by `req.Op` and gives `hook_wall` a persisted writer, or an `arch/` amendment narrowing §5.17's "per hook" to the aggregate clock. That decision is 00-ARCHITECTURE's to make, not SP-14's, and until it is made **no exit criterion in this file may assert a non-zero `n` on a `.<hook>` row** — which is why `TestE2E_StatusAgainstRealDaemon` asserts against the aggregate `hook_controlled` row and the `.observe_tool` sub-row is asserted only to be *present* with `n == 0` and `pass == true`.

6. **Frontier.** `src.Store.Segments().Frontier(ctx, session)` → `Turn`. `Unencoded(ctx, session)` → `UnencodedSegments = len(segs)` and `ResidualTokens = Σ seg.Tokens`. `MaxResidualTokens = Cfg.Checkpoint.Frontier.MaxResidualTokens`.
7. **Checkpoint.** `src.Checkpoints.List(ctx)` → `Count = len(refs)`; the entry with the highest `Seq` fills the rest (`SHA256 = ref.SHA256.String()`, `Path = filepath.ToSlash(ref.Path)` made project-relative via `paths.Norm`).
8. **Scheduler.** `src.Sched.Evaluate(ctx)`. The section is labelled *evaluated now* in the human output — this is a fresh evaluation of the daemon's current state, which is the honest reading of "the last scheduler decision" and requires no daemon edit. `Breakdown` sorted by key. `Reasons` are `string(r)` in returned order. `Background` are `string(t)`.
9. **GC.** Skipped when `src.SkipGC`. Otherwise `src.Store.GC(ctx, store.GCPolicy{RetainDays: Cfg.Store.Retention.Days, RetainSessions: Cfg.Store.Retention.Sessions, DryRun: true, Deadline: gcProbeDeadline})` with `const gcProbeDeadline = 250 * time.Millisecond`. `Collectable = report.DeletedObjects` (dry run reports would-delete). `DurationMs` from `report.Duration`. `Truncated = report.Truncated`, **and it is not optional**: since `4708ebe` a probe whose 250 ms expires inside the mark's hash harvest returns `GCReport{Truncated: true}` with `DeletedObjects == 0` and nothing collected (`internal/store/gcrun.go:92-101` — sweeping against an incomplete live set would delete live objects, so the pass ends with no cursor), so a status that drops the flag prints `collectable 0` for a store with gigabytes to reclaim, which is exactly the silent-success shape this project's gates exist to prevent. The human renderer's `partial:` field (see the `GC` render line below) is that flag, and when it is `yes` the line also carries `— probe truncated at 250 ms; collectable is a lower bound, not the store's total`; the JSON carries `"truncated": true`. This is deliberately a dry run: `/qompack:status` reports storage growth (§12 risk row) and must never mutate the store as a side effect of being looked at.
10. **Loud.** Read `<root>/logs/LOUD.log` with `os.ReadFile` capped at `loudTailBytes = 64 << 10` (seek to `max(0, size-64KiB)`), split on `\n`, drop empty lines, take the last `loudTailCount = 5`. Each line is parsed as JSON; if it parses to an object with a string `msg` (or, failing that, `message`) field, emit `LoudMessage{TS: string(ts), Message: msg, Raw: false}` where `ts` is the object's `ts`/`time` field rendered as-is; otherwise emit `LoudMessage{Message: truncate(line, 160), Raw: true}`. A missing file yields an empty slice and is **not** an `Unavailable` entry — no loud messages is the good case.
11. **Daemon.** Filled by `FetchSnapshot`, not by `Collect`.

### `internal/commands/statusop.go` and `statusfetch.go`

**The two-op split, stated once and normatively.** `ipc.OpStatus` belongs to SP-05 and is not re-registered: `d.handleStatus` keeps serving it and `daemon.StatusSnapshot` keeps its shape, because `test/bench/hotpath/measure.go` decodes that exact payload for B-B and for the gated B-A row. What that payload does **not** carry is the six sections `/qompack:status` adds — store stats, sketches, frontier, checkpoint, scheduler, GC — none of which the daemon has any other reason to compute. Those travel over SP-14's own op.

```go
const OpStatusFull = ipc.Op("status.full")

type StatusFull struct {
    Schema     int            `json:"schema"`      // 1
    Session    core.SessionID `json:"session"`
    Collected  string         `json:"collected"`   // RFC3339 UTC from Clock
    Store      StoreInfo      `json:"store"`
    Sketches   SketchInfo     `json:"sketches"`
    Frontier   FrontierInfo   `json:"frontier"`
    Checkpoint CheckpointInfo `json:"checkpoint"`
    Scheduler  SchedulerInfo  `json:"scheduler"`
    GC         GCInfo         `json:"gc"`
    Unavailable []string      `json:"unavailable"`
}

func CollectFull(ctx context.Context, src SnapshotSources) StatusFull
func NewStatusFullOpHandler(src SnapshotSources) ipc.Handler
```
`NewStatusFullOpHandler` returns a handler that runs `CollectFull(ctx, srcWithSession(req.Session))`, marshals it, and returns `ipc.Response{OK: true, Data: b}`. `Mode` and `Hot` are left zero — `dispatchOp` stamps both on every response before it leaves the daemon. Panics inside the handler are recovered and returned as `ipc.Response{OK: false, Err: …}` (00-ARCHITECTURE §12.3 "any hook panic → recovered"). `CollectFull` runs blocks 3, 4, 6, 7, 8 and 9 of `Collect` — the same code, the same per-section `recover`, the same `Unavailable` discipline — and nothing else; blocks 1, 2, 5, 10 and 11 are precisely what the daemon's own `status` reply already answers.

**Why a new op rather than `Services.StatusExtra`, and what happens to that seam.** SP-05 ships `StatusExtra func(ctx context.Context) (json.RawMessage, error)` (`internal/daemon/options.go:130`), calls it inside `handleStatus` (`internal/daemon/handlers.go:771-774`) and attributes it to SP-14 by name (`plans/V2-SP-05-daemon-ipc-and-hot-path.md:520, :1362`; reaffirmed `plans/V4-SP-12-scheduler-l3.md:499, :2011`). **SP-14 does not bind it, and states here that it stays unbound.** The seam hangs the extended payload off `ipc.OpStatus`'s reply, which this subplan has frozen: `daemon.StatusSnapshot` is decoded byte-for-byte by `test/bench/hotpath/measure.go` for B-B and the gated B-A row, and growing its `Extra` field on every `status` call — including the ones `bench-hotpath` makes in its measurement loop — puts the six extended sections (a store `Stats`, a sketch health read, a frontier scan, a checkpoint list, a scheduler `Evaluate` and a GC probe) on the daemon's own status path, where the harness pays for them and cannot opt out. `status.full` is a separate route with a separate deadline that only `/qompack:status` ever sends, so the cost is charged to the caller that wants it. The price is one extra round trip (steps 3 and 4 of `FetchSnapshot`), which is the deliberate trade. **Consequence, recorded so it is not rediscovered:** `Services.StatusExtra` therefore has no producer anywhere in waves 0–5 and ships as a dead extension point. Its `// SP-14` attribution in `plans/V2-SP-05:520` and `plans/V4-SP-12:499` is stale and must be corrected to `// unowned` by those files' owners — SP-14 does not edit them, and does not silently accept an obligation it has just declined.

`OpStatusFull` is deliberately **not** added to `ipc.KnownOps()`: `internal/ipc/op_test.go`'s `TestKnownOps_ListsAllThirteenSorted` pins that vocabulary at thirteen entries, and nothing on this path consults `ipc.Op.Valid` — `buildRoutes` copies every `Options`-registered op into `d.routes` verbatim, `dispatchOp` is a map lookup on `d.routes`, and `ipc.Client.Send` validates nothing. `status.full` is also not a hot-path op (`Op.HotPath()` is false for anything outside the three `observe.*` ops), so a spooling client never intercepts it.

```go
func FetchSnapshot(ctx context.Context, d Deps) (StatusSnapshot, bool)
```
1. `addr, err := ipc.Resolve(d.ProjectRoot)`. Bind `addrPath := ""` and, only when `err == nil`, `addrPath = addr.Path`. On error skip to step 5 (**never dereference `addr` on the error path** — `ipc.Resolve` returns the zero `Addr`).
2. `c := ipc.NewClient(addr, nopSpool{}, logOrNop(d.Log), metricsOrNop(d.Metrics))` where `nopSpool` implements `ipc.SpoolWriter` with `Append(ipc.Request) error { return nil }` and `Path() string { return "" }` — a status query must never leave junk in the spool. `logOrNop` returns `logging.Nop()` when `d.Log == nil`; `metricsOrNop` returns a package-private no-op `obs.Registry` when `d.Metrics == nil`, because `NewClient` takes both unconditionally and `status` must run on a project where nothing else opened.
3. **`ipc.OpStatus` first.** `resp, _ := c.Send(ctx, ipc.Request{Op: ipc.OpStatus, Session: d.Session, TS: core.UnixMilli(d.Clock.Now().UnixMilli()), Reply: true}, statusDeadline)` with `const statusDeadline = 1500 * time.Millisecond`. If `resp.OK && len(resp.Data) > 0` and `json.Unmarshal` into `daemonStatus` succeeds, fill the five sections that payload carries and continue to step 4; otherwise skip to step 5. The mapping is fixed:
   | `commands.StatusSnapshot` | from `daemonStatus` |
   |---|---|
   | `Mode` | `mode` (string), with the banner fields taken from the first `!OK && Severity == SevCritical` entry of `contract`, exactly as `Collect` block 1 does |
   | `Contracts` | `contract` (`[]contract.Result`), one `ContractRow` each, in returned order |
   | `Latency` | `latency` (`map[string]obs.HistSnapshot`), joined against `obs.Budgets()` by `Budget.Hist`; a budget with no key renders `no samples` |
   | `Loud` | `loud_tail` (`[]string`), parsed by the same NDJSON reader `Collect` block 10 uses |
   | `Daemon` | `DaemonInfo{Reachable: true, Addr: addrPath, HotPath: hotName(resp.Hot)}` |

   `daemonStatus` is a package-private struct in `statusfetch.go` carrying exactly those five JSON tags (`mode`, `contract`, `hot`, `latency`, `loud_tail`) — a mirror rather than an import. §3.2's composition-roots row (`00-ARCHITECTURE.md:410`) is ambiguous for an edge between two of its own members — *"may import anything"* and *"nothing may import them"* both apply — and `tools/devtool/importgraph.go:179-180` skips a root's own imports entirely, with `TestImportGraph_CompositionRootTestsMayImportAnything` pinning that as intended; the tree already ships `test/bench/hotpath` → `internal/daemon`, a root→root edge. The mirror is therefore chosen for decoupling, not compulsion: `commands` decodes a wire payload it does not own, so a daemon-side field rename must fail a named test rather than a compile. `hotName` is an explicit switch — `ipc.HotPathMode` has no `String()` method, so `fmt.Sprint` on it would print an integer and break the goldens: `ipc.HotSync → "sync"`, `ipc.HotSpool → "spool"`, anything else → `"unknown"`. The daemon also stamps `resp.Hot` on every response, so `hotName(resp.Hot)` is authoritative even when `daemonStatus.Hot` is absent.
4. **`OpStatusFull` second, on the same client.** `resp2, _ := c.Send(ctx, ipc.Request{Op: OpStatusFull, Session: d.Session, TS: …, Reply: true}, statusDeadline)`. On `resp2.OK` with a payload that unmarshals into `StatusFull`, copy its six sections and merge its `Unavailable` entries into `snap.Unavailable`. On any failure — an older daemon that has no such route answers `ipc.Response{OK: false, Err: "unknown op: status.full"}` — the six sections are collected locally instead, from `sourcesFromDeps(d)`, and only they fall back; the five sections of step 3 stay daemon-fresh. Set `snap.FromDaemon = true` and return `(snap, true)`.
5. Full fallback: `snap := Collect(ctx, sourcesFromDeps(d))`; `snap.Daemon = DaemonInfo{Reachable: false, Addr: addrPath, HotPath: "unknown"}`; return `(snap, false)`. The fallback reads the same on-disk state the daemon would have loaded, so a stopped daemon degrades freshness, not correctness.
6. `sourcesFromDeps(d)` copies `ProjectRoot, Session, Cfg, Clock, Log, Store, Ledger, Checkpoints, Sched, Contract` straight across and sets `SkipGC` from the `--no-gc` flag. Its **only** I/O is `Metrics: loadMetricsFromDisk(d.ProjectRoot)` (see the latency block above) — `d.Metrics` in a slash-command process is a freshly created, empty registry and would render every latency row as `no samples`, which would be a lie rather than a degradation.

### `internal/commands/status.go` + `renderstatus.go`

`statusCmd.Run` parses `--json`, `--no-gc`, repeatable `--section`, `--help`; calls `FetchSnapshot`; filters sections when `--section` is given; then either `RenderJSON(out, "status", snap)` or `RenderStatus(out, snap)`.

**`--section` semantics — normative, so human and JSON never disagree.** Valid names are exactly `mode contracts store sketches latency frontier checkpoint scheduler gc loud daemon`; an unknown name is `ErrUsage` (exit 2) naming the offending value and listing the valid set. The filter is applied **identically in both modes**, by a single `filterSections(snap, want []string) StatusSnapshot` step that runs before rendering: every non-selected section's field is reset to its zero value and its name is appended to `snap.Filtered` (sorted in the fixed section order). `Unavailable` is left untouched — "you asked me not to show it" and "I could not collect it" are different facts and the JSON must keep them apart. The header line and `Schema`/`ProjectRoot`/`Session`/`Collected`/`FromDaemon` are never filtered. `--no-gc` additionally short-circuits collection itself (the probe is not merely hidden, it is not run), which is why `gc` lands in `Unavailable` rather than `Filtered` in that case.

`RenderStatus` writes, in this exact order, through `text/tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)` for the tabular blocks:

```
QOMPACK STATUS  ·  project <root>  ·  session <id|(none)>  ·  <collected>  ·  source <daemon|disk>
```
then, **only when `Mode.Degraded`**, the loud banner, flush-left, before anything else:
```
!!!!  QOMPACK DEGRADED — <mode>  !!!!
  failed assertion : <assertion|(none)>
  expected         : <expected>
  observed         : <observed>
  effect           : no additionalContext injection, no customInstructions, no
                     scheduler-initiated checkpoints, no drop report.
                     L0 and L1 keep recording; the store stays correct.
  recovery         : two consecutive clean SessionStart runs restore full mode.
```
(the `effect` and `recovery` paragraphs are the verbatim behaviour of 00-ARCHITECTURE §12.1 and are constant strings, not computed);
then `MODE  <mode>`; then sections `CONTRACTS`, `STORE`, `SKETCHES`, `LATENCY (p50 / p95 / p99 / max)`, `FRONTIER`, `CHECKPOINT`, `SCHEDULER (evaluated now)`, `GC (dry run, <deadline>)`, `LOUD (last 5)`, each separated by a single blank line, each with a two-space-indented body. A section listed in `Unavailable` renders as `<NAME>\n  — unavailable (<reason>)`. Concrete lines:

- `STORE`: `objects`, `stored` (formatBytes Bytes), `raw` (formatBytes RawBytes), `dedup ratio  <x.xx>:1   (Phase 1 exit criterion >= 4.00:1: PASS|FAIL)`, `tool uses`, `segments`, `files`.
- `SKETCHES`: `tried.bloom  fill <f.fff>  est FP <f.ffff>  configured FP <f.ffff>  records <n> (active <a>, stale <s>)  resize: yes|no`, followed by `  ** bloom saturation: estimated FP rate is >= 10x configured — rebuild/resize (Qompack.md §11.4) **` when `SaturationWarning`; then one line per `SketchCounts` entry.
- `LATENCY`: one row per `LatencyRow`: `<clock>  <p50> / <p95> / <p99> / <max>   n=<N>   budget <B-x> <limit>  PASS|FAIL` — or `no samples` in place of the percentiles when `N == 0`, and `budget B-D (reported, not gated)` when `!Gated`.
- `FRONTIER`: `frontier turn`, `residual tokens <n>  (max <m>)`, `unencoded segments <n>`.
- `CHECKPOINT`: `last seq <n>  <path>  <bytes>  <tokens> tokens  frontier <turn>  <sha256 short>` and `total <count>`; `(none)` when `Count == 0`.
- `SCHEDULER`: `should compact`, `reasons` (comma-joined), `ttl`, `p  pos=<n> turn=<n> segment=<n> coupling=<n> score=<x.xx>`, `breakdown  k=v  k=v` (sorted), `young-daly  <x.x>s`, `soft floor <n> tokens   hard ceiling <n> tokens`, `background  <comma-joined>`.
- `GC`: `scanned <n>   live <n>   collectable <n>   would free <bytes>   roots <n>   took <dur>   partial: yes|no` and `retention  <days>d / <sessions> sessions`. `partial` is `GCReport.Truncated`. When it is `yes`, a second line follows: `— probe truncated at 250 ms; collectable is a lower bound, not the store's total` — a truncated mark returns zero collected, so `partial: yes` with `collectable 0` must never be read as a clean store (Collect block 9).
- `LOUD`: `(none)` or up to five `  <ts>  <message>` lines.

### `internal/commands/mcpfront.go` — the shared MCP-handler frontend

```go
type toolInvoker struct{ tools map[string]mcp.Tool }
func newToolInvoker(d mcp.ToolDeps, log logging.Logger) (*toolInvoker, error)
func (ti *toolInvoker) call(ctx context.Context, name string, args any) (mcp.Response, error)
```
`newToolInvoker` builds `s := mcp.NewServer("qompack", "in-process", log)` (the version string is never transmitted — no stdio server is started), calls `mcp.RegisterAll(s, d)`, and indexes `s.Tools()` by `Tool.Name`. `call` marshals `args` to `json.RawMessage`, then invokes `t.Handler(ctx, mcp.Request{Session: sess, Name: name, Args: raw, Deadline: clock.Now().Add(mcpDeadline)})` with `const mcpDeadline = 10 * time.Second`. An unknown tool name returns `fmt.Errorf("%w: mcp tool %q", core.ErrNotFound, name)`. A handler panic is recovered and converted to an error. **No MCP server is started and no stdio is touched** — this is an in-process call of the same handler `qompack mcp` serves, which is what makes "exactly one implementation" literally true.

`decodeContent[T any](r mcp.Response) (T, string, bool)` returns the decoded payload from `r.Content[0].Text`; on decode failure it returns the raw text and `false`, and the caller renders the raw text (human) or puts it under `"raw"` (JSON). `r.IsError` is surfaced as `OK:false` with the content text as the error message and exit code 1.

### `internal/commands/recall.go`

Flags: `--k N` (default `recallDefaultK = 5`, §8.7 `recall(query, k=5)`), `--json`. Positional args are joined with a single space to form the query; empty query → `ErrUsage`. Calls `call(ctx, "recall", mcp.RecallArgs{Query: q, K: k})`, decodes `[]mcp.RecallHit`. Human output:

```
RECALL  "<query>"  k=<k>  hits=<n>
  #  SCORE  HASH          PATH                     TOOL      WHEN                  SUMMARY
  1  0.91   sha256:a3f2…  src/auth.ts              FileRead  2026-08-11T09:12:00Z  refreshToken retry path
```
`(no hits)` when empty. `HASH` uses `core.Hash.Short()` semantics: first 12 hex chars, prefixed `sha256:` and suffixed `…`; the hit's `Hash` is already a string, so it is truncated to `sha256:` + 12 chars. JSON data is `{"query":…,"k":…,"hits":[…],"ephemeral":<r.Ephemeral>}`.

### `internal/commands/why.go`

Flags: `--json`. Exactly one positional arg (the decision id); zero or more than one → `ErrUsage`. Calls `call(ctx, "why", mcp.WhyArgs{DecisionID: id})`, decodes `checkpoint.Decision`. Human:
```
WHY  <id>
  what        : <what>
  why         : <why>
  turn        : <turn>
  evidence    : <sha256 short>
  alternatives rejected:
    - <alt>
```
`core.ErrNotFound` from the handler renders `WHY  <id>\n  (no decision with that id — /qompack:status shows the last checkpoint seq; decisions are minted by the checkpointer)` and exits 1.

### `internal/commands/dropped.go`

Flags: `--json`. No positional args. Calls `call(ctx, "dropped", mcp.DroppedArgs{})`, decodes `[]checkpoint.DropEntry`, groups by `Kind` (kinds sorted lexicographically, entries within a kind sorted by `ID`). Human:
```
DROPPED  <n> item(s) currently out of context
  path_rule (2)
    - api-conventions.md — glob src/api/** matched 3 pointer files
  nested_claude_md (1)
    - src/worker/CLAUDE.md
```
`(nothing dropped)` when empty. This is the §8.6 item-7 drop report on demand; the rehydrator owns producing it.

### `internal/commands/pin.go`

Flags: `--list`, `--remove ID`, `--eliminated`, `--target T`, `--reason R`, `--scope session|project`, `--evidence sha256:…`, `--depends-on PATH` (repeatable `stringList`), `--json`. Modes are mutually exclusive; more than one of `--list/--remove/--eliminated` → `ErrUsage`.

**Default (invariant pin), §7.4 `pins/invariants.json` + G2.2.**
1. Text = positional args joined with a space; empty → `ErrUsage`.
2. `id := "inv_" + core.HashBytes("qompack.pin", []byte(text)).String()[7:19]` — the 12 hex chars after the `sha256:` prefix. Deterministic, so pinning the same text twice is a no-op rather than a duplicate.
3. `existing, _ := d.Pins.All(ctx)`; if any has that ID → print `PIN  already pinned  <id>` and return nil.
4. `d.Pins.Add(ctx, pins.Invariant{ID: id, Text: text, Source: "slash-command", Pinned: core.UnixMilli(d.Clock.Now().UnixMilli())})`, then `d.Pins.Materialize(ctx)`.
5. Output `PIN  <id>\n  <text>`; JSON `{"action":"add","id":…,"text":…,"already":false}`.

**`--list`** prints `PINS  <n>` and one `  <id>  <text>` line per invariant, sorted by `Pinned` ascending then `ID`.

**`--remove ID`** calls `d.Pins.Remove(ctx, id)` (a tombstone record — never a rewrite, §3.3) then `Materialize`; prints `PIN  removed  <id>`.

**`--eliminated` — §8.3 elimination source #2.**
1. `--target` and `--reason` are required; either missing → `ErrUsage` with the exact usage line.
2. Approach = positional args joined with a space; empty → `ErrUsage`.
3. `scope := d.Cfg.Eliminations.DefaultScope` unless `--scope` is given; a value outside `{session, project}` → `ErrUsage`.
4. **Evidence.** If `--evidence` is given it must parse via `core.ParseHash`; a value that does not parse is `ErrUsage` (exit 2), never a silent fallback. Otherwise, when `d.Cfg.Eliminations.RequireEvidence` is true, synthesize evidence honestly rather than refusing the user:
   ```go
   res, err := d.Store.PutBytes(ctx, []byte(reason), store.PutOptions{
       Tool:      "qompack:pin",
       Path:      "",                 // not file-scoped
       Canon:     canon.Options{},    // no stripping: the user's sentence is stored verbatim
       KeepRaw:   false,
       Ephemeral: false,              // an elimination's evidence must outlive the session (§8.3)
   })
   ```
   and use `res.Root.Hash`. The explicit zero `canon.Options` matters: it is what makes the evidence hash a pure function of the reason text, which is what makes `pin_eliminated.*` golden-able. The user's own statement is the evidence, it is content-addressed like everything else, and the record is never evidence-free. If `RequireEvidence` is false and no flag is given, `Evidence` is the zero hash. If `RequireEvidence` is true and `d.Store` is nil, fail with the nil-`Deps` error of global rule 9 rather than writing an evidence-free record.
5. **`depends_on`.** For each `--depends-on p`: `k, err := paths.Norm(d.ProjectRoot, p)` (a path that escapes the root is `ErrUsage`); `hist, err := d.Store.FileHistory(ctx, paths.Key(k))`; use the `Root` of the entry with the **greatest `TS`** — `FileHistory` is documented as a version list, not as sorted, so select by `TS` rather than trusting slice order, breaking ties on the greatest `Turn`. Result: `core.Dep{Path: paths.Key(k), Hash: <that root>}`. A path with no history is reported as a warning line `  ! <path>: no version history in the store — omitted from depends_on` and omitted from `DependsOn` — an elimination that depends on a file the store has never seen cannot have its staleness evaluated, and silently recording a zero hash would make it permanently stale-looking. Duplicate `--depends-on` values are de-duplicated by `paths.Key` and the surviving list is sorted by path, so the record is byte-stable.
6. `rec := negknow.Record{Session: d.Session, TS: now, Target: target, Approach: approach, Reason: reason, Desc: negknow.Canonicalize(target, approach, reason), Evidence: ev, DependsOn: deps, Scope: negknow.Scope(scope), Status: "active", Source: negknow.SourceSlashCommand}`; `id, err := d.Ledger.Record(ctx, rec)`.
7. Output:
```
PIN --eliminated  <id>
  target      : <target>
  approach    : <approach>
  reason      : <reason>
  scope       : <scope>
  evidence    : <sha256 short>
  depends on  : <path> @ <sha256 short>
  descriptor  : path=<normalized_path> symbol=<symbol|null> class=<approach_class> reason=<hash short>
```
The descriptor line renders the canonical descriptor of §8.3 verbatim in its four fields, so a user can see exactly what `already_tried` will match on.

### `internal/commands/checkpointnow.go`

Flags: `--session ID`, `--budget N` (default `d.Cfg.Checkpoint.BudgetTokens` = 12000), `--json`.

**Session resolution, in order** — first non-empty wins:

1. `--session ID`.
2. `d.getenv("QOMPACK_SESSION_ID")`.
3. `d.Session` (set by `cli`'s `runSlash` only when `env.Getenv` supplied one).
4. **Newest session in the segment log.** `segs, err := d.Store.Segments().Range(ctx, 0, core.TurnIndex(math.MaxInt32))`; take the `Segment` with the greatest `StartTS` (ties broken by the greatest `ID`) and use its `Session`. This is a read-only, API-legal fallback that needs no new interface and no daemon edit.
5. Otherwise: `fmt.Errorf("no active session; pass --session (tried --session, QOMPACK_SESSION_ID, the caller's session, and the newest segment in %s)", segmentsPath)`, exit 1.

**Why not "ask the daemon".** An earlier draft resolved step 4 from `FetchSnapshot().Session`, which is circular: the daemon-side handler fills `snap.Session` from `req.Session`, which is the value we are trying to discover. The segment-log fallback is the one that actually carries information.

Flow:
1. `refs, _ := d.Checkpoints.List(ctx)`; `parent := max(refs[i].Seq)` (0 when none).
2. `segs, err := d.Store.Segments().Unencoded(ctx, sess)`; keep only `seg.Closed && !seg.EncodedOnce`; collect `ids`. An empty set is not an error: the checkpoint still finalizes with tier-1/2 content and `encoded_segments: []`.
3. `src := checkpoint.SourceSet{Store: d.Store, Segments: d.Store.Segments(), Ledger: d.Ledger, Pins: d.Pins, Graph: d.Graph, Grammar: d.Grammar, Tokens: d.Tokens}` — note the type has no field that can carry live context text, which is precisely §8.5's regeneration rule made uncompilable.
4. `draft, err := d.Writer.Begin(ctx, sess, parent, src)`; on error exit 1.
5. `frontier, err := d.Writer.Advance(ctx, draft, ids)`. If `errors.Is(err, core.ErrAlreadyEncoded)`: print `CHECKPOINT  refused: DPI guard — segment(s) already encoded into an earlier checkpoint (Qompack.md §4.6, §8.2)`, call `d.Writer.Abort(draft)`, exit 1.
6. `start := d.Clock.Now()`; `ref, err := d.Writer.Finalize(ctx, draft, core.Tokens(budget))`; `elapsed := d.Clock.Since(start)`.
7. Output:
```
CHECKPOINT  seq <n>
  path        : checkpoints/0008.json
  sha256      : sha256:9f3c…
  bytes       : 38.2 KB
  tokens      : 4120  (budget 12000)
  frontier    : turn 412
  segments    : 3 encoded  (parent seq 7)
  finalize    : 812.0ms   budget B-E 2.00s  PASS
```
`FAIL` when `elapsed > 2s`; the limit is `checkpointFinalizeBudget = 2 * time.Second` from 00-ARCHITECTURE §2.4 B-E / §11.3 "< 2s (L4)".

### `internal/commands/eval.go`

Flags: `--corpus DIR`, `--policy NAME` (repeatable), `--baseline NAME` (default `"stock"`), `--k N` (default `evalDefaultK = 20`), `--budget N` (default `d.Cfg.Checkpoint.BudgetTokens`), `--seed N` (default 1), `--json`.

**Corpus resolution, in order** — first directory that exists wins; the chosen path is echoed in the header so the run is never ambiguous:

1. `--corpus DIR` (an explicit value that does not exist is exit 1 naming the path — never a silent fallback).
2. `d.getenv("QOMPACK_SESSIONS_DIR")` — the same variable 00-ARCHITECTURE §6.3 tier 2 and the nightly workflow use.
3. `<d.ProjectRoot>/.qompack/eval/replay` (§7.4's own corpus location).
4. `<cwd>/testdata/sessions/synthetic`, but **only when `<cwd>/go.mod` declares `module github.com/qompack/qompack`** — i.e. only when the command is run from inside the Qompack repo itself. A user's project has no `testdata/sessions/synthetic` and must not be probed for one.
5. Otherwise exit 1: `no session corpus found; pass --corpus DIR (tried $QOMPACK_SESSIONS_DIR, <root>/.qompack/eval/replay, ./testdata/sessions/synthetic)`.

**Policy discovery.** `d.Policies` is filled by the composition root. `cli`'s `runSlash` populates it by probing the harness for an optional convenience interface — `if p, ok := d.Eval.(interface{ Policies() []eval.Policy }); ok { d.Policies = p.Policies() }`. This is a **type assertion, not an amendment**: SP-02's `eval.Harness` interface (§5.18) is untouched, Rule W-3 is respected, and a harness that does not implement it simply yields an empty set and the "no policies registered in this build" path below.

```go
sessions, err := d.Eval.Load(corpus)                      // err → exit 1, message names corpus
if len(d.Policies) == 0 { print "EVAL  no policies registered in this build"; RenderJSON empty report; return nil }
selected := d.Policies filtered by --policy (all when the flag is absent), always including --baseline when present

// Belady OPT is a property of the SESSION and the budget, not of the policy (§6.10: "replay a
// session, observe what was actually needed after each compaction, compute the optimal
// keep-set"). Compute it ONCE per session and share it across policies — computing it inside
// the policy loop would multiply the most expensive call in the command by len(selected)
// and change no number.
opt := map[string]map[core.TurnIndex]eval.KeepSet{}
for _, s := range sessions {
    m := map[core.TurnIndex]eval.KeepSet{}
    for _, at := range s.CompactionAt {
        ks, err := d.Eval.Belady(ctx, s, at, core.Tokens(budget))
        if err != nil { warn(s.ID, at, err); continue }
        m[at] = ks
    }
    opt[s.ID] = m
}

scores := map[string][]eval.Score{}
for _, p := range selected {
    for _, s := range sessions {
        run, err := d.Eval.Replay(ctx, s, p, eval.ReplayOptions{K: k, Seed: seed, Budget: core.Tokens(budget), Deterministic: true})
        if err != nil { warn(s.ID, p.Name(), err); continue }
        scores[p.Name()] = append(scores[p.Name()], d.Eval.ScoreRun(run, opt[s.ID]))
    }
}
report, err := d.Eval.Report(ctx, scores)                 // err → exit 1
```
`Deterministic: true` is forced — CI-safe replay only; live mode is `QOMPACK_EVAL_LIVE=1` and is SP-02's, never reachable from this command. Every skipped session/policy pair emits one `  ! skipped <session> / <policy>: <err>` line before the table, so a partial run is visible rather than silently thinner. When `len(sessions) < d.Cfg.Eval.MinSessions` (default 20) the report is still printed, preceded by `  ** only <n> sessions loaded; Qompack.md §11.3 wants >= <min> **`.

Human output (the fraction-of-OPT report of §11.1 plus every §11.2 secondary metric, baseline row first, remaining policies sorted by name):
```
EVAL  corpus <dir>  sessions <n>  budget <n> tokens  k=<k>  seed=<seed>

  POLICY      FRAC-OPT  FIRST-DIV  JACCARD  DEC-PRES  REDUNDANT  RE-ATTEMPTS  REWRITE-TOK  REHYD-TOK  HIT-RATE
  stock       0.612     3.1        0.710    0.480     14         6            41200        62000      0.00
  qompack     0.874     9.4        0.930    0.910     2          0            18770        10400      0.62

  LATENCY (p50 / p95, ms)         PAUSE            RESIDUAL-SPAN     FIRST-TURN-AFTER
  stock                           18400 / 31200    148300 / 161000   4100 / 7300
  qompack                         2600 / 4100      13820 / 19400     900 / 1400

  REGRESSIONS (Qompack.md §11.3: no metric may regress by more than 2% to improve another)
  (none)
```
**Where each column comes from** — the `eval.Score` returned for each policy by `report.Policies[name]`, which is SP-02's aggregate; the command does **not** re-average `scores[name]` itself, so there is exactly one aggregation implementation and it is SP-02's:

| Column | `eval.Score` field | Format |
|---|---|---|
| FRAC-OPT | `FractionOfOPT` (§11.1 primary) | `%.3f` |
| FIRST-DIV | `Divergence.FirstDivergenceTurn` | `%.1f` |
| JACCARD | `Divergence.FileSetJaccard` | `%.3f` |
| DEC-PRES | `Divergence.DecisionPreservation` | `%.3f` |
| REDUNDANT | `Divergence.RedundantReads` | `%d` |
| RE-ATTEMPTS | `Divergence.ReAttempts` | `%d` |
| REWRITE-TOK | `RewriteTokens` (Σ `w·(n − p_min)`) | `%d` |
| REHYD-TOK | `RehydrationTokens` | `%d` |
| HIT-RATE | `RetrievalHitRate` | `%.2f` |
| PAUSE | `CompactionPauseMS.P50` / `.P95` | `%.0f / %.0f` |
| RESIDUAL-SPAN | `ResidualSpan.P50` / `.P95` | `%.0f / %.0f` |
| FIRST-TURN-AFTER | `FirstTurnAfterMS.P50` / `.P95` | `%.0f / %.0f` |

That is every row of the §11.2 table except `Divergence.ToolEditDistance` and `Divergence.SameDecision`, which are per-run booleans/counts with no meaningful policy-level aggregate and are carried only in the `--json` payload. The LATENCY block is the v1.2 addition (compaction pause, residual span, first-turn-after) and is what makes the O5 amortization claim visible from the command line. Regressions come straight from `report.Regressions` (`Metric, Policy, Baseline, Observed, DeltaPct, Allowed`), one line `  <flag><metric>  <policy>  <baseline> → <observed>  (<deltaPct>%)`; a row with `Allowed == false` is prefixed `!!` and one with `Allowed == true` by two spaces. JSON data is `{"corpus":…,"sessions":n,"skipped":[…],"report":<eval.Report>}`.

### `internal/commands/dispatch.go`

```go
func All(d Deps) []Command   // statusCmd, recallCmd, pinCmd, checkpointCmd, whyCmd, droppedCmd, evalCmd — Specs() order
func Names() []string
func Dispatch(ctx context.Context, name string, args []string, out io.Writer, d Deps) error
func ExitCode(err error) int
```
`Dispatch` matches on either the slash name or `pluginmanifest.SubcommandFor(name)`; an unmatched name returns `fmt.Errorf("%w: unknown qompack subcommand %q", core.ErrNotFound, name)`.

### `internal/cli/slash.go` (new file, SP-14) + a table edit in `commands.go` + 3 lines in `dispatch.go`

**There is no subcommand switch in package `cli`, and SP-14 does not add one.** `cli.Dispatch` (`internal/cli/dispatch.go`) matches argv against the data table `All()` builds in `internal/cli/commands.go`; the only `switch argv[1]` in the file is `case "-h", "--help", "help"`. `Cmd.Run` is `func(ctx context.Context, env Env, args []string, out, errw io.Writer) error` — it returns an **error**, not an exit code — and `Env` exists precisely so no command reads `os.Args`, `os.Stdout` or `os.Getenv` directly. Six of the seven commands are already registered as data, in `commands.go`'s `notImplemented` table: `status`, `recall`, `pin`, `why`, `dropped`, `eval`. `checkpoint-now` is registered nowhere.

SP-14's cli work is therefore three edits, all of them data:

**(a) `internal/cli/commands.go` — remove six rows, splice in seven.** Delete the `{"status",…}`, `{"recall",…}`, `{"pin",…}`, `{"why",…}`, `{"dropped",…}` and `{"eval",…}` entries from `notImplemented` (leaving `mcp`, `fsck`, `doctor` and `bench` to their owners), and add one line to `All()` next to the existing `cmds = append(cmds, evalCmds()...)`:

```go
cmds = append(cmds, slashCmds()...)
```

Removing the bare `eval` row is exactly what `internal/cli/register_eval.go` reserved it for ("the bare `eval` entry stays in the not-implemented list, because running the replay harness from the binary is SP-14's `/qompack:eval`"). `eval import` keeps working unchanged: `cli.match` tries the two-word name before the one-word one, so `qompack eval import …` still reaches `runEvalImport` and `qompack eval …` reaches SP-14's command.

**(b) `internal/cli/slash.go` (new) — the seven `Cmd` entries and the one adapter.**

```go
// slashCmds is the §7.5 verb group: one Cmd per slash command, in Commands() order. Summaries
// come from pluginmanifest so `qompack help`, `--help` and docs/commands.md cannot disagree.
func slashCmds() []Cmd {
    out := make([]Cmd, 0, len(pluginmanifest.Commands()))
    for _, spec := range pluginmanifest.Commands() {
        sub := spec.Subcommand           // captured per iteration
        out = append(out, Cmd{
            Name:    sub,                // "status" … "checkpoint-now" … "eval"
            Summary: spec.Summary,
            Run: func(ctx context.Context, env Env, args []string, o, errw io.Writer) error {
                return runSlash(ctx, sub, env, args, o, errw)
            },
        })
    }
    return out
}

// runSlash builds commands.Deps from env and runs one slash command.
func runSlash(ctx context.Context, name string, env Env, args []string, out, errw io.Writer) error
```

`runSlash` resolves the project root with `loadForCommand(env)`'s own `paths.Resolve(env.Getenv, …)` path (never `os.Getenv`), loads config, opens the logger, opens `store`, `negknow`, `dag`, `grammar`, `pins`, `checkpoint` reader/writer, `tokens`, builds `mcp.ToolDeps`, constructs `contract.NewMonitor(log, metrics, filepath.Join(root, "state", "contract.json"))`, sets `Getenv: env.Getenv` and `Session` from `env.Getenv("QOMPACK_SESSION_ID")`, fills `Policies` by the optional-interface probe described under `/qompack:eval`, sets `Clock: env.Clock` (`Dispatch` has already defaulted it to `core.SystemClock()`), and calls `commands.Dispatch(ctx, name, args, out, deps)`. **Every `Open` is wrapped: an error leaves that member nil, logs at WARN, and never aborts `runSlash`** — a status command must work on a broken store, that is when it is needed most. Commands other than `status` surface a missing member through the nil-`Deps` rule (global rule 9) rather than a panic.

`commands.Dispatch` writes its own diagnostics to `out` (global rule 8), so `runSlash` returns `errAlreadyReported`-wrapped errors and `Dispatch` prints nothing on top of them — the same contract `runEvalImport` already follows.

**(c) `internal/cli/dispatch.go` — three lines, so exit 2 stays reachable.** `Dispatch` today returns `ExitError` (1) for every non-nil `Cmd.Run` error and `ExitUsage` (2) only for an unknown subcommand or a malformed `--set`. Global rule 6 requires a usage error to exit 2. `TestStatus_SectionUnknown`, `TestRecall_EmptyQuery` and their siblings assert that inside `internal/commands` via `ExitCode(err) == 2`; without this edit the real binary would still print 1, so the package contract and the process contract would quietly disagree. SP-14 therefore declares one small edit to SP-01's file: a package-private sentinel and one branch.

```go
// errUsage marks an error a subcommand has classified as a usage error, so Dispatch can map it
// to ExitUsage without importing the package that raised it (§2.3's exit-code policy).
var errUsage = errors.New("qompack: usage")
…
    if err != nil {
        if !errors.Is(err, errAlreadyReported) {
            fmt.Fprintf(errw, "qompack %s: %v\n", cmd.Name, err)
        }
        if errors.Is(err, errUsage) {
            return ExitUsage
        }
        return ExitError
    }
```

`runSlash` performs the translation, so `dispatch.go` gains no import: `if errors.Is(err, commands.ErrUsage) { return fmt.Errorf("%w: %w", errUsage, errAlreadyReported) }`. A hook subcommand is unaffected — the `cmd.Hook` early return still precedes this branch, so §2.3's "a hook always exits 0" is untouched.

**Amendment specified by this subplan, landed as a wave-4 pre-step.** 00-ARCHITECTURE §2.3's subcommand tree has no `checkpoint-now` line. SP-14 declares it, but does **not** carry it inside its own feature branch. §2.3 is a fenced ASCII tree (`00-ARCHITECTURE.md:110-122`), not a table, and every non-hook subcommand that carries an annotation gets its own line, so the edit is one annotated line inserted after `00-ARCHITECTURE.md:116` (`├── qompack checkpoint`):

```
├── qompack checkpoint-now      ← on-demand checkpoint from the store (`/qompack:checkpoint`)
```

leaving bare `qompack checkpoint` as the PreCompact hook client and preserving the hook/non-hook distinction that :124's "Hook subcommands must always `exit 0`" rule depends on. **It is not appended to the grouped `status|recall|pin|...` line** — that line carries no `←` annotation, and `checkpoint-now` needs one.

**It lands on an `arch/checkpoint-now-subcommand` branch cut from `develop`, merged before the first wave-4 feature branch is cut**, matching §9's "`arch/<reason>` off `develop`, must land before dependent work" (`00-ARCHITECTURE.md:2439`). The branch is opened and merged by the session that opens wave 4, not by the SP-14 session — SP-14's branch does not exist yet at that point, which is exactly why the edit cannot sit in SP-14's commit 7 under README's merge order (`plans/README.md:45`, SP-14 last). SP-14 owns the *content* of the line and this file is where it is specified; SP-14 owns none of the *scheduling*.

**Collision rule for the other two wave-4 branches.** Neither SP-15 nor SP-16 touches §2.3: SP-15's only 00-ARCHITECTURE amendment is the `arch/store-tooluses-by-session` pre-step against §5.8, and SP-16's two documentation edits are to §11.5 and §5.8 inside its own feature branch — all of them hundreds to thousands of lines away from `110-122`, so git cannot conflict them with this hunk. If a wave-4 branch later needs an edit inside `110-122`, it rebases onto `arch/checkpoint-now-subcommand` rather than re-editing the block; two branches must never carry hunks in the same fenced tree.

The line changes no §5 interface and needs no Rule W-3 negotiation (`00-ARCHITECTURE.md:16-18, :24-26`).

`internal/cli` also registers the `status.full` op at daemon construction, in the same file where SP-05 builds `daemon.Options`:

```go
opts.Handle(commands.OpStatusFull, commands.NewStatusFullOpHandler(commands.SnapshotSources{
    ProjectRoot: opts.ProjectRoot,
    Session:     "",              // per-request; NewStatusFullOpHandler overrides from req.Session
    Cfg:         opts.Cfg,
    Clock:       opts.Clock,
    Log:         opts.Log,
    Store:       opts.Store,      // the daemon's live store — no second open
    Ledger:      opts.Ledger,
    Checkpoints: checkpointReader, // the same reader the daemon already holds
    Sched:       opts.Sched,       // may be nil in a pre-SP-12 build; CollectFull tolerates it
    Metrics:     opts.Metrics,
    Contract:    contractMonitor,
    SkipGC:      false,
}))
```
Every member is a `daemon.Options` field of 00-ARCHITECTURE §5.4 or a value `cli` already built to construct those options; nothing new is opened, `internal/daemon` is not edited, and **`ipc.OpStatus` is not among the ops registered here** — `buildRoutes` prefers an `Options` registration over the default route, so registering `status` would silently displace `d.handleStatus` and break `bench-gate`. This is the whole reason `status` prefers the daemon: `obs.Registry` is daemon-resident, so the latency section is *only* real on this path — the disk fallback reads `metrics/latency.json` and is explicitly labelled `source disk` in the header.

### `tools/devtool` changes

- **New task `gen-command-docs`**: writes `pluginmanifest.CommandDocs()` to `docs/commands.md`.
- **`plugin-validate` — the byte-diff half needs no addition.** `tools/devtool/pluginvalidate.go` already calls `pluginmanifest.Validate(dir, m)` and fails the task on any `"missing"`, `"content differs"` or `"not produced by the generator"` diff, and `Manifest.Files()` is still the single producer of the seven `plugin/commands/<name>.md` files. Because SP-14 changes `renderCommand`'s body, the `checkpoint` row's `Subcommand` and the `AllowedTools` prefix, the committed bytes are regenerated once with `devtool plugin-validate --write` in commit 8; that is a checklist item, not a new assertion.
- **`plugin-validate` additions** (in the file implementing that task):
  1. Assert `pluginmanifest.CommandNames()` equals `["status","recall","pin","checkpoint","why","dropped","eval"]` — the §7.5 `"commands"` array, in that order. **Do not assert against a `commands` key in `plugin/.claude-plugin/plugin.json`: that file has no such key** (00-ARCHITECTURE §3.4 pins its exact contents to `name/version/description/author/homepage/keywords`; Claude Code discovers commands as files). The physical assertion is instead that `filepath.Glob("plugin/commands/*.md")` yields exactly the seven `<name>.md` files, no more and no fewer, so an orphaned or missing file fails the gate.
  2. Assert `commands.Names()` equals the same list (binary agrees with manifest).
  3. Build the binary and, for each spec, run `qompack <spec.Subcommand> --help`, requiring exit code 0 and stdout beginning with `usage: qompack <spec.Subcommand>` (commands resolve to real subcommands — including `checkpoint-now`, which is why it is registered in `cli`'s table and added to 00-ARCHITECTURE §2.3 by the `arch/checkpoint-now-subcommand` pre-step).
  4. Assert `docs/commands.md` equals `pluginmanifest.CommandDocs()`.
- **`.github/workflows/ci.yml`**: add `go run ./tools/devtool gen-command-docs` + `git diff --exit-code docs/commands.md` to the existing `docs` job, immediately after the `gen-config-docs` step.

### Performance budgets for this slice

| Operation | Budget | Source |
|---|---|---|
| `checkpoint-now` finalize | p99 < 2 s (**B-E**) | §11.3 "< 2s (L4)", 00-ARCH §2.4 |
| MCP-backed `recall`/`why`/`dropped` handler call | p95 < 250 ms (**B-F**, `minimal` span) | 00-ARCH §2.4 |
| `Collect` excluding the GC probe | p95 < 250 ms on a warm 2 000-tool-use project | local budget, benchmarked |
| GC dry-run probe | 250 ms of the deadline-bounded loops, plus the mark's index walks | `GCPolicy.Deadline` bounds the mark's hash harvest and the sweep; the mark's in-memory index walks answer to ctx alone (`internal/store/gcrun.go`, `gcBudget.cancelled`), so the probe can overshoot by the walk. Measure it; do not assert it. |
| `RenderStatus` on a full snapshot | < 5 ms | local budget, benchmarked |

`/qompack:status` is never on a hook path, so B-A does not apply to it; it *reports* B-A. The daemon-side `status.full` handler is likewise off the hot path — `ipc.Op.HotPath()` is true only for the three `observe.*` ops — so `CollectFull` inherits `Collect`'s p95 < 250 ms local budget and nothing else. `bench-gate` must be unmoved by this subplan: `ipc.OpStatus` keeps SP-05's handler and `daemon.StatusSnapshot` keeps its shape, so `test/bench/hotpath/measure.go` decodes exactly what it decoded before.

---

## Test plan (TDD)

Every test below is written before the code in its commit and must fail first. Fixtures live in `testdata/golden/commands/`. All tests construct `Deps` with `testutil.NewProject(t)`, `testutil.FakeClock` fixed at `2026-08-11T09:12:00Z`, and fakes defined in `internal/commands/fakes_test.go` (`fakeStore`, `fakeLedger`, `fakeReader`, `fakeWriter`, `fakePins`, `fakeSched`, `fakeHarness`, `fakeMonitor`, `fakeRegistry`, `fakePolicy`) implementing the §5 interfaces with table-driven canned returns.

### Table and wiring

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestCommandNames_MatchesSection75` | — | `pluginmanifest.CommandNames()` | exactly `["status","recall","pin","checkpoint","why","dropped","eval"]`, same order |
| `TestSubcommandFor` | — | `"checkpoint"`, `"status"`, `"nope"` | `"checkpoint-now"`, `"status"`, `""` |
| `TestAll_OneCommandPerSpec` | `Deps{}` | `All(Deps{})` | len 7; `Name()` of element *i* equals `Commands()[i].Name` |
| `TestCommandDocFieldsPreserveShippedValues` | — | `pluginmanifest.Commands()` | the five shipped `CommandDoc` fields keep their pre-SP-14 values for every row except `checkpoint`'s `Subcommand` (now `checkpoint-now`) and the `${CLAUDE_PLUGIN_ROOT}` prefix in `AllowedTools`; every row has a non-empty `Summary`, `Usage` and at least the two appended flags |
| `TestDispatch_AcceptsBothSpellings` | fake deps | `"checkpoint"` and `"checkpoint-now"` | both reach `checkpointCmd.Run` (recorded by a spy) |
| `TestDispatch_UnknownName` | `Deps{}` | `"nope"` | `errors.Is(err, core.ErrNotFound)`, `ExitCode(err) == 1` |
| `TestExitCode` | — | `nil`, `ErrUsage`-wrapped, other | `0`, `2`, `1` |
| `TestHelp_EveryCommand` | `Deps{}` | `--help` for all seven | exit 0, stdout starts `usage: qompack <sub>`, one line per flag declared in `spec.Flags` (including the appended `--json`/`--help`) |
| `TestHelp_DashH_SameAsHelp` | `Deps{}` | `-h` for all seven | byte-identical to `--help`; exit 0, **not** 2 (`flag.ErrHelp` → help path, global rule 7) |
| `TestJSONFlag_EveryCommand` | fake deps | `--json` for all seven | stdout parses as `Envelope` with `command` set and `schema == 1` |
| `TestJSONErrorEnvelope_EveryCommand` | fake deps forced to fail | `--json` for all seven | stdout still parses as `Envelope` with `ok == false`, non-empty `error`, `data == null`; exit code is 1 (or 2 for `ErrUsage`) — global rule 8 |
| `TestNilDeps_EveryCommandButStatus` | `Deps{Cfg: config.Defaults(), Clock: fake}` | run each of the six non-`status` commands | exit 1, `errors.Is(err, core.ErrNotFound)`, message names the missing `Deps` member; **no panic** (global rule 9) |
| `TestNilDeps_StatusStillRuns` | same | `status` | exit 0 — `status` has no required members |
| `TestNoDirectStdio` | — | AST scan of `internal/commands/*.go` excluding `_test.go` | zero references to `os.Stdout`, `os.Stderr`, `fmt.Print*` — everything goes to the `out` writer |
| `TestNoTimeNowInPackage` | — | `go/parser` over `internal/commands/*.go` excluding `_test.go` | zero `time.Now` selector expressions |
| `TestNoANSI` | fake deps | every command, human mode | output contains no `\x1b[` |

### `/qompack:status`

| Test | Setup | Expected |
|---|---|---|
| `TestStatus_HumanGolden_Full` | full fakes: Stats{Objects:1240, Bytes:19_294_618, RawBytes:85_144_371, DedupRatio:4.412, ToolUses:2013, Segments:17, Files:148}, Health{Records:214,Active:197,Stale:17,FillRatio:0.184,EstFPRate:0.0009}, one B-A histogram with p99=7.8ms, frontier turn 412 / residual 13820, checkpoint seq 7, a `Decision` with `Breakdown{"reclaimable":6180,"rewrite":-23462.5,"distortion":-1.2}`, GCReport{Scanned:1240,Live:1188,Deleted:52,BytesFreed:1_992_294,Truncated:false}, empty LOUD.log | byte-equal to `testdata/golden/commands/status_full.txt`; contains `dedup ratio  4.41:1`, `partial: no` and `PASS`; sections appear in the normative order |
| `TestStatus_JSONGolden_Full` | same | byte-equal to `status_full.json`; `data.store.meets_phase1_ratio == true` |
| `TestStatus_DegradedBanner` | `fakeMonitor` returns `ModeDegradedPassive` and a `Result{ID:"hook.additional_context_delivered", OK:false, Severity:SevCritical, Expected:"sentinel token present in transcript tail", Observed:"sentinel not found in 3 consecutive prompts"}` | first non-header line is `!!!!  QOMPACK DEGRADED — degraded-passive  !!!!`; banner carries the assertion id, expected and observed; `data.mode.degraded == true` |
| `TestStatus_DegradedWithoutCriticalAssertion` | mode degraded, all results OK | `Reason == "degraded without a failing assertion (see LOUD.log)"` |
| `TestStatus_NilDepsUnavailable` | `Deps{Cfg: config.Defaults(), Clock: fake}` only | exit 0; `unavailable` contains exactly `mode`, `contracts`, `store`, `sketches`, `frontier`, `checkpoint`, `scheduler`, `gc` (in that fixed section order), each with reason `nil`, and each rendering `— unavailable (nil)`. It does **not** contain `latency` (an empty registry is a valid `no samples` render, not a collection failure) and does **not** contain `loud` or `daemon` |
| `TestStatus_StoreStatsError` | `fakeStore.Stats` returns `io.ErrUnexpectedEOF` | `unavailable` contains `store`; every other section still renders |
| `TestStatus_LoudTail_LastFive` | LOUD.log with 8 NDJSON lines | exactly the last 5, oldest first, `Raw == false` |
| `TestStatus_LoudTail_NonJSONLine` | LOUD.log with one 400-char plain line | `Raw == true`, message truncated to 160 chars |
| `TestStatus_LoudTail_MissingFile` | no LOUD.log | `Loud` empty, `unavailable` does **not** contain `loud`, human prints `(none)` |
| `TestStatus_BloomSaturationWarning` | `Health.EstFPRate = 0.10`, `Cfg.Sketches.Bloom.FPRate = 0.01` | `saturation_warning == true`; human contains `bloom saturation` and `§11.4` |
| `TestStatus_LatencyBudgets` | B-A p99 = 21ms with `Runtime.HotPath.BudgetMs = 15`; a `hook_wall` row; a zero-sample `checkpoint_finalize`; a `hook_degraded` row at p99 = 340ms | B-A row `FAIL`, B-D row `(reported, not gated)` and `pass == true`, B-E row `no samples`, B-G row renders `budget B-G 1.00s (reported, not gated)` with `gated == false` and `pass == true` |
| `TestStatus_LatencyRowsComeFromObsBudgets` | `Cfg.Runtime.Budgets.HookDegradedMs = 333`, `Cfg.Runtime.Budgets.MCPToolCallMs = 99` | the B-G row's `limit_ms` is `333` and B-F's is `99` — the column is `Budget.Limit(Cfg)`, never `limits.go`'s render default; the seven rows appear in `obs.Budgets()` order |
| `TestStatus_BGIsNeverGated` | `hook_degraded` p99 far above the limit | `pass == true` and no `FAIL` anywhere in the human output — B-G is reported only, structurally |
| `TestStatus_LatencyFromDiskFile` | no daemon; a hand-written `.qompack/metrics/latency.json` with a `hook_controlled` entry at p99 = 7.8ms | that row renders with `n > 0`; `loadMetricsFromDisk` never writes (assert the file's mtime and bytes are unchanged) |
| `TestStatus_LatencyMissingMetricsFile` | no daemon, no `metrics/latency.json` | every row reads `no samples`; `unavailable` does **not** contain `latency` |
| `TestStatus_NoGCFlag` | `--no-gc` | `fakeStore.GC` never called; `unavailable` contains `gc` |
| `TestStatus_GCIsDryRun` | default | recorded `GCPolicy` has `DryRun == true`, `Deadline == 250ms`, `RetainDays == 30`, `RetainSessions == 10`; a second arm returns `GCReport{Truncated: true}` (the shipped shape of a mark that ran out of deadline: `DeletedObjects == 0`, `BytesFreed == 0`) and asserts `GC.Truncated == true`, `partial: yes` in the human render with the lower-bound line, and `"truncated": true` in the JSON — a golden that renders `collectable 0` with `partial: no` for that report fails |
| `TestStatus_SectionFilter` | `--section store --section loud` | only the header, `STORE`, `LOUD` blocks present |
| `TestStatus_SectionFilter_JSON` | `--section store --section loud --json` | the filter applies identically: `data.filtered` lists the nine suppressed sections in fixed section order, their fields are zero values, `data.unavailable` is unchanged, and `schema`/`project_root`/`session`/`collected`/`from_daemon` are still populated |
| `TestStatus_SectionUnknown` | `--section nope` | `ErrUsage`, exit 2; message names `nope` and lists the eleven valid section names |
| `TestStatus_CollectRecoversSectionPanic` | `fakeStore.Stats` panics | exit 0; `unavailable` contains `store: panic: …`; every other section still renders; no panic escapes `Collect` |
| `TestStatus_UnavailableIsSortedBySectionOrder` | three sections fail in a shuffled order across 50 runs | `unavailable` is byte-identical every run |
| `TestStatus_MapsAreSorted` | `Breakdown` inserted in random order 50× | rendered breakdown identical every run |
| `TestStatus_FetchPrefersDaemon` | fake `ipc` server answering `status` with a `daemonStatus`-shaped payload and `status.full` with a `StatusFull` | `FromDaemon == true`, `Daemon.Reachable == true`; `fakeStore.Stats` never called locally; both ops were sent, `status` first |
| `TestStatus_DaemonPayloadMirrorIsCurrent` | a real daemon's `status` reply captured in `test/e2e` and committed as `testdata/golden/commands/daemon_status_payload.json` | `json.Unmarshal` into `daemonStatus` populates `mode`, `contract`, `hot`, `latency` and `loud_tail`; a tag the daemon no longer emits fails the test naming the field — this is the drift guard for the deliberately decoupled mirror |
| `TestStatus_StatusFullUnknownOpFallsBackPerSection` | `status` answers normally; `status.full` answers `OK:false, Err:"unknown op: status.full"` (an older daemon) | `FromDaemon == true`; mode/contracts/latency/loud come from the daemon; store/sketches/frontier/checkpoint/scheduler/gc come from the local `sourcesFromDeps` collection; no section is silently empty |
| `TestStatus_DoesNotRegisterOpStatus` | AST scan of `internal/commands/*.go` and `internal/cli/*.go` excluding `_test.go` | zero `Handle(` calls whose op argument is `ipc.OpStatus` or the literal `"status"`; the only registered op is `commands.OpStatusFull` — the guard that keeps `bench-gate` green |
| `TestStatus_FetchFallsBackToDisk` | no listener | `FromDaemon == false`; local `Collect` ran; nothing appended to the spool (assert the spool dir is empty); `Daemon.HotPath == "unknown"` |
| `TestStatus_FetchResolveError` | `QOMPACK_PROJECT_ROOT` pointing at a path `ipc.Resolve` rejects | exit 0, `FromDaemon == false`, `Daemon.Addr == ""`, no nil-pointer dereference of the zero `ipc.Addr` |
| `TestStatus_FetchNilLogAndMetrics` | `Deps{Log: nil, Metrics: nil}` | `ipc.NewClient` receives non-nil substitutes; no panic |
| `TestStatus_HotPathNames` | daemon replies with `Sync`, then `Spool`, then an out-of-range value | `Daemon.HotPath` renders `"sync"`, `"spool"`, `"unknown"` — never an integer |
| `TestStatusFullOpHandler_ReturnsSections` | `NewStatusFullOpHandler(src)` | `resp.OK`, `resp.Data` unmarshals to `StatusFull` with `Schema == 1` and all six sections populated |
| `TestStatusFullOpHandler_RecoversPanic` | source whose `Stats` panics | `resp.OK == false`, `resp.Err` non-empty, no panic escapes |
| `TestStatusFullOpIsNotAKnownOp` | — | `ipc.Op("status.full").Valid() == false` and `len(ipc.KnownOps()) == 13` — SP-14 adds a route, never a wire-vocabulary entry |
| `TestDeterminism_RenderStatus` | one snapshot rendered 100× | all 100 outputs byte-identical |

### `/qompack:recall`, `/qompack:why`, `/qompack:dropped`

| Test | Setup | Expected |
|---|---|---|
| `TestRecall_DelegatesToMCPHandler` | spy handler registered as tool `recall` | handler received `RecallArgs{Query:"pool timeout", K:5}` — the §8.7 default k=5 |
| `TestRecall_KFlag` | `--k 12` | `RecallArgs.K == 12` |
| `TestRecall_HumanGolden` / `TestRecall_JSONGolden` | three canned hits | byte-equal to `recall_hits.txt` / `recall_hits.json` |
| `TestRecall_NoHits` | empty result | `(no hits)`; JSON `hits: []` (never `null`) |
| `TestRecall_EmptyQuery` | no positional args | `ErrUsage`, exit 2 |
| `TestRecall_HandlerIsError` | `Response{IsError:true, Content:[{Text:"store closed"}]}` | exit 1, stderr-free, human prints `RECALL  error: store closed` |
| `TestRecall_UnparsableContent` | `Content[0].Text = "not json"` | human prints the raw text; JSON has `data.raw == "not json"` |
| `TestRecall_NoSecondImplementation` | AST scan of `recall.go`, `why.go`, `dropped.go` | zero references to `store.Store`, `negknow.Ledger` or `checkpoint.Reader` — these three commands may reach the data **only** through `toolInvoker` |
| `TestWhy_DelegatesToMCPWhy` | spy | `WhyArgs{DecisionID:"dec_9f3c1a2b4d5e"}` |
| `TestWhy_Golden` | canned `checkpoint.Decision` with two alternatives | byte-equal to `why_decision.txt` / `.json` |
| `TestWhy_MissingOrExtraArgs` | 0 args, 2 args | `ErrUsage` both times |
| `TestWhy_NotFound` | handler returns `core.ErrNotFound` | exit 1, the explanatory line about checkpoint-minted decisions |
| `TestDropped_DelegatesAndGroups` | 5 entries across 3 kinds, inserted unsorted | kinds lexicographic, entries by ID; counts in the headers |
| `TestDropped_Empty` | empty | `(nothing dropped)`; JSON `[]` |
| `TestDropped_Golden` | canned | byte-equal to `dropped_list.txt` / `.json` |

### `/qompack:pin`

| Test | Setup | Expected |
|---|---|---|
| `TestPin_DeterministicID` | text `"never bypass the pool"` | id equals `"inv_" + HashBytes("qompack.pin", text).String()[7:19]`; recomputed identically across runs |
| `TestPin_Idempotent` | pin the same text twice | second run prints `already pinned`, `fakePins.Add` called once, exit 0 |
| `TestPin_CallsMaterialize` | one pin | `Materialize` called exactly once after `Add` |
| `TestPin_List` | 3 invariants inserted out of order | sorted by `Pinned` then `ID` |
| `TestPin_Remove` | `--remove inv_abc` | `Remove` called with that id, then `Materialize`; output `PIN  removed  inv_abc` |
| `TestPin_Eliminated_WritesLedger` | `--eliminated --target src/auth.ts:refreshToken --reason "pgbouncer 1.18 ignores it in transaction mode" "widen pool timeout"` | `Ledger.Record` receives `Status:"active"`, `Source: negknow.SourceSlashCommand`, `Scope: "session"` (the Appendix C default), `Desc` equal to `negknow.Canonicalize(target, approach, reason)` |
| `TestPin_Eliminated_ScopeFlag` | `--scope project` | `Scope == "project"` |
| `TestPin_Eliminated_BadScope` | `--scope global` | `ErrUsage`, exit 2, ledger untouched |
| `TestPin_Eliminated_RequiresTargetAndReason` | omit each in turn | `ErrUsage` both times; usage line names the missing flag |
| `TestPin_Eliminated_SynthesizesEvidence` | `RequireEvidence: true`, no `--evidence` | `Store.PutBytes` called with the reason bytes and `Tool:"qompack:pin"`; `Record.Evidence` equals the returned root hash |
| `TestPin_Eliminated_ExplicitEvidence` | `--evidence sha256:<64hex>` | that hash used verbatim; `PutBytes` not called |
| `TestPin_Eliminated_BadEvidence` | `--evidence not-a-hash` | `ErrUsage`, exit 2, ledger untouched, `PutBytes` not called |
| `TestPin_Eliminated_EvidenceIsDeterministic` | same reason text, two runs | identical `Evidence`; recorded `PutOptions` has `Canon == canon.Options{}`, `KeepRaw == false`, `Ephemeral == false` |
| `TestPin_Eliminated_DependsOnPicksLatestByTS` | history written out of order (TS 3, 1, 2) | the dep hash is the root of the TS-3 entry, not of the last slice element |
| `TestPin_Eliminated_DependsOnDeduped` | the same path passed twice, plus two paths given in reverse order | one entry per path, list sorted by `paths.Key` |
| `TestPin_Eliminated_DependsOnEscapesRoot` | `--depends-on ../../etc/passwd` | `ErrUsage`, exit 2, ledger untouched |
| `TestPin_Eliminated_NoEvidenceNotRequired` | `RequireEvidence: false` | `Evidence` is the zero hash; `PutBytes` not called |
| `TestPin_Eliminated_DependsOn` | `--depends-on docker-compose.yml --depends-on package-lock.json`, both with history | two `core.Dep` entries with `paths.Key` paths and the latest root hashes |
| `TestPin_Eliminated_DependsOnUnknownFile` | file with no history | warning line printed, dep omitted, record still written |
| `TestPin_Eliminated_Golden` | canned | byte-equal to `pin_eliminated.txt` / `.json`; the descriptor line shows all four §8.3 fields |

### `/qompack:checkpoint`

| Test | Setup | Expected |
|---|---|---|
| `TestCheckpointNow_BeginAdvanceFinalize` | 3 closed unencoded segments, latest ref seq 7 | `Begin(parent=7)`, `Advance` with exactly those 3 ids, `Finalize(budget=12000)`; output shows `seq 8` |
| `TestCheckpointNow_FiltersOpenAndEncoded` | 5 segments: 3 closed-unencoded, 1 open, 1 encoded | `Advance` receives only the 3 |
| `TestCheckpointNow_NoSegments` | zero unencoded | still finalizes; output `segments : 0 encoded` |
| `TestCheckpointNow_AlreadyEncoded` | `Advance` returns `core.ErrAlreadyEncoded` | `Abort` called, exit 1, message names the DPI guard and §4.6 |
| `TestCheckpointNow_SourceSetShape` | — | the constructed `checkpoint.SourceSet` has every field non-nil; a compile-time assertion documents that no field can carry context text |
| `TestCheckpointNow_SessionResolution` | `--session` / `QOMPACK_SESSION_ID` / `d.Session` / newest segment by `StartTS` / none | the first four succeed in that priority order (each shadowing the ones below it); the fifth exits 1 with `no active session; pass --session` and the message names all four attempted sources |
| `TestCheckpointNow_NewestSegmentWins` | `Range` returns three sessions' segments with shuffled `StartTS` | the session of the greatest-`StartTS` segment is used; ties broken by the greatest `ID` |
| `TestCheckpointNow_NilWriter` | `Deps.Writer == nil` | exit 1, `errors.Is(err, core.ErrNotFound)`, message names `Writer`; no panic |
| `TestCheckpointNow_BudgetFlag` | `--budget 4000` | `Finalize` receives `core.Tokens(4000)` |
| `TestCheckpointNow_BEBudgetReported` | FakeClock advanced 2.4 s across `Finalize` | line reads `budget B-E 2.00s  FAIL` |
| `TestCheckpointNow_Golden` | canned ref | byte-equal to `checkpoint_now.txt` / `.json` |
| `BenchmarkCheckpointNow` | real writer over a temp project with 40 segments | reports ns/op; the test asserts wall time `< 2s` (**B-E**) |

### `/qompack:eval`

| Test | Setup | Expected |
|---|---|---|
| `TestEval_RunsHarness` | `fakeHarness` with 24 sessions, 2 policies, 3 compaction points each | `Belady` called exactly **72 times total** (24 sessions × 3 points, computed once per session and shared across policies — Belady is a property of the session and the budget, not the policy); `Replay` called 24× per policy with `Deterministic == true`; `ScoreRun` receives that session's OPT map keyed by compaction turn |
| `TestEval_ForcesDeterministic` | `QOMPACK_EVAL_LIVE=1` set in the environment | recorded `ReplayOptions.Deterministic` is still true for every call |
| `TestEval_CorpusResolution` | in turn: `--corpus` to a real dir, `--corpus` to a missing dir, `$QOMPACK_SESSIONS_DIR`, `<root>/.qompack/eval/replay`, a temp dir whose `go.mod` declares the qompack module + `testdata/sessions/synthetic`, and none of the above | resolves 1/3/4/5 in that priority; the missing explicit `--corpus` exits 1 naming the path; the last case exits 1 with the three-path "tried" message |
| `TestEval_CorpusDoesNotProbeForeignRepo` | cwd has `testdata/sessions/synthetic` but a `go.mod` for another module | that directory is **not** selected |
| `TestEval_PolicyProbe` | harness implementing `Policies() []eval.Policy`, and one that does not | the first fills `d.Policies`; the second yields the empty-set path; neither requires an `eval.Harness` method |
| `TestEval_SkippedPairsReported` | `Replay` fails for one session | one `! skipped …` line before the table; that pair absent from `scores`; exit 0 |
| `TestEval_PrintsFractionOfOPT` | scores 0.612 / 0.874 | both rows present, `%.3f`, baseline row first |
| `TestEval_AggregatesFromReportNotLocally` | `report.Policies["qompack"].FractionOfOPT = 0.874` while the raw `scores` slice averages to 0.5 | the table prints `0.874` — the command never re-averages; SP-02 owns aggregation |
| `TestEval_LatencyBlock` | `Score.CompactionPauseMS/ResidualSpan/FirstTurnAfterMS` populated | the `LATENCY (p50 / p95, ms)` block renders all three §11.2 v1.2 columns for every policy |
| `TestEval_PolicyFilter` | `--policy qompack` | only `qompack` and the baseline are run |
| `TestEval_EmptyPolicySet` | `Policies: nil` | exit 0; `no policies registered in this build`; JSON `data.report.policies == {}` |
| `TestEval_BelowMinSessions` | 5 sessions, `MinSessions: 20` | warning line naming both numbers; report still printed; exit 0 |
| `TestEval_Regressions` | report with one `Allowed:false` regression | row prefixed `!!`; text quotes the 2% rule |
| `TestEval_LoadError` | `Load` returns `core.ErrNotFound` | exit 1, message names the corpus dir |
| `TestEval_Golden` | canned report | byte-equal to `eval_report.txt` / `.json` |

### Property, manifest, docs and e2e

| Test | Kind | Expected |
|---|---|---|
| `TestProperty_EnvelopeAlwaysValidJSON` | `rapid`, 500 cases: random flag orders, random unicode query strings, randomly nil `Deps` members, every command with `--json` | stdout always parses as `Envelope`; never panics; exit code ∈ {0,1,2} |
| `TestProperty_HumanOutputHasNoTabsOutsideTables` | `rapid` over snapshots | every rendered line is stable across two renders and contains no `\r` |
| `TestRenderCommand_Golden` | 7 goldens under `testdata/golden/commands/plugin/` | byte-equal, LF endings, single trailing newline |
| `TestManifestFiles_MatchCommittedPluginDir` | `pluginmanifest.Validate(repoRoot, Default(core.Version))` | zero `Diff`s — the in-test twin of the `plugin-validate` gate, run against the one and only generator |
| `TestManifestFiles_HasExactlySevenCommandFiles` | `Manifest.Files()` keys under `plugin/commands/` | exactly the seven `<name>.md` paths, no more and no fewer — an orphaned or missing file fails here as well as in `plugin-validate` |
| `TestCommandDocs_MatchesCommittedDocs` | compare `CommandDocs()` to `docs/commands.md` | equal |
| `TestRenderCommand_ReferencesRealSubcommand` | for each spec | body contains `${CLAUDE_PLUGIN_ROOT}/bin/qompack <subcommand> $ARGUMENTS` and `allowed-tools` names the same subcommand |
| `TestE2E_EverySubcommandResolves` (`test/e2e`) | build the real binary | `qompack <sub> --help` exits 0 for all seven; stdout starts with `usage: qompack <sub>` |
| `TestE2E_StatusAgainstRealDaemon` (`test/e2e`) | real daemon on a temp project seeded with 50 tool uses | `qompack status --json` exits 0, `from_daemon == true`, `store.tool_uses == 50`, latency section has a `hook_controlled` row with `n >= 50` — the **aggregate** B-A clock, which `recordHotPathSample` is the shipped writer of. The `hook_controlled.observe_tool` sub-row must be *present* with `n == 0` and `pass == true`; asserting a non-zero `n` on it would gate this subplan on a producer nobody owns (see the latency block's per-hook note) |
| `TestE2E_StatusWithDaemonStopped` (`test/e2e`) | daemon killed | exits 0, `from_daemon == false`, `store.objects > 0`, spool dir empty |
| `TestE2E_CheckpointNowThenStatus` (`test/e2e`) | run `checkpoint-now`, then `status --json` | `checkpoint.seq` in status equals the seq the command printed |
| `TestPluginValidate_DetectsDrift` (`tools/devtool`) | mutate one byte of `plugin/commands/why.md` | the task fails, naming the file |
| `TestGenCommandDocs_Idempotent` (`tools/devtool`) | run twice | second run leaves the file unchanged |
| `BenchmarkCollect` | warm fake store with 2 000 tool uses, `SkipGC: true` | reports ns/op; asserts p95 `< 250ms` over 20 runs |
| `BenchmarkRenderStatus` | full snapshot | asserts `< 5ms` per render |
| `BenchmarkMCPFrontend` | in-process `recall` through `toolInvoker` | asserts p95 `< 250ms` (**B-F**, `minimal` span) |

**Fixtures to create:** `testdata/golden/commands/{status_full.txt,status_full.json,status_degraded.txt,recall_hits.txt,recall_hits.json,why_decision.txt,why_decision.json,dropped_list.txt,dropped_list.json,pin_eliminated.txt,pin_eliminated.json,checkpoint_now.txt,checkpoint_now.json,eval_report.txt,eval_report.json}` and `testdata/golden/commands/plugin/{status,recall,pin,checkpoint,why,dropped,eval}.md`. Goldens are regenerated with `go test ./internal/commands -run Golden -update` (an `-update` flag registered in `internal/commands/golden_test.go`).

---

## Commit plan

Work happens on `feat/sp14-slash-commands-and-observability`, cut from `develop` **after** V4 verification is green:

```
git checkout develop && git pull && git checkout -b feat/sp14-slash-commands-and-observability
```

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

Every commit compiles and passes `go run ./tools/devtool test` for the packages it touches. Footers use the `Refs:` form of 00-ARCHITECTURE §10. **The exact subject and footer of each commit are fixed here — copy them literally:**

| # | Subject line (`<type>(<scope>): <subject>`) | Footer |
|---|---|---|
| 1 | `feat(commands): command table, dispatch, flags and JSON envelope` | `Refs: SP-14, G8.1, §7.5` |
| 2 | `feat(commands): status snapshot collector and the status.full daemon op` | `Refs: SP-14, G8.1, §11.3, §11.4, §12` |
| 3 | `feat(commands): render /qompack:status with the degraded banner` | `Refs: SP-14, G8.1, G9.3, §11, §12` |
| 4 | `feat(commands): recall, why and dropped as MCP-handler frontends` | `Refs: SP-14, G3.1, G4.5, §8.6, §8.7` |
| 5 | `feat(commands): pin, pin --eliminated and on-demand checkpoint-now` | `Refs: SP-14, G2.2, G6.1, §4.6, §8.3, §8.5` |
| 6 | `feat(commands): eval fraction-of-OPT report` | `Refs: SP-14, G8.3, §6.10, §11.1, §11.2` |
| 7 | `feat(cli): dispatch the seven §7.5 slash commands and register the status op` | `Refs: SP-14, §7.5, §8.1` |
| 8 | `build(plugin): generate plugin/commands and docs/commands.md, gate them in CI` | `Refs: SP-14, G9.3, §7.5` |

Bodies explain the decision, not the diff (§10), wrap at 100 columns, and carry no trailer other than the `Refs:` line above.

### Commit 1 — `feat(commands): command table, dispatch, flags and JSON envelope`

- [ ] Write failing tests first: `internal/pluginmanifest/commands_test.go` (`TestCommandNames_MatchesSection75`, `TestSubcommandFor`, `TestRenderCommand_Golden`, `TestCommandDocFieldsPreserveShippedValues`), `internal/commands/dispatch_test.go` (`TestAll_OneCommandPerSpec`, `TestDispatch_AcceptsBothSpellings`, `TestDispatch_UnknownName`, `TestExitCode`), `internal/commands/flags_test.go` (`TestHelp_EveryCommand`, `TestHelp_DashH_SameAsHelp`, `TestJSONFlag_EveryCommand`, `TestJSONErrorEnvelope_EveryCommand`), `internal/commands/hygiene_test.go` (`TestNoTimeNowInPackage`, `TestNoANSI`, `TestNoDirectStdio`).
- [ ] Run `go test ./internal/commands ./internal/pluginmanifest` — must fail.
- [ ] Modify `internal/pluginmanifest/manifest.go`: append `Summary`, `Usage`, `Flags`, `Sections` to the shipped `CommandDoc`, add `CommandFlag`, fill the four new fields in `commandSpecs` from the tables above, change the `checkpoint` row's `Subcommand` to `checkpoint-now`, prefix `AllowedTools` with `binaryRef`, replace `renderCommand`'s body, and add `Commands`, `CommandNames`, `SubcommandFor`, `CommandDocs`. `Manifest.Files()` is not touched. Defaults that mirror Appendix C are read from `config.Defaults()`, never typed as literals (§11.6).
- [ ] Resolve the `commandstest` decision: flip the skips off if SP-01 shipped the suite, otherwise create `internal/commands/commandstest/suite.go` with `RunCommandSuite` as specified, plus `internal/commands/suite_test.go` calling it.
- [ ] Add `internal/commands/{spec.go,flags.go,envelope.go,dispatch.go,limits.go,deps.go,require.go}` with the seven command structs returning `core.ErrNotImplemented` from `Run` (help, `--json`, error-envelope and nil-`Deps` paths already real).
- [ ] Add `testdata/golden/commands/plugin/*.md` (7 files).
- [ ] `go run ./tools/devtool fmt lint test` green.

### Commit 2 — `feat(commands): status snapshot collector and the status.full daemon op`

- [ ] Write failing tests: `collect_test.go` (`TestStatus_NilDepsUnavailable`, `TestStatus_StoreStatsError`, `TestStatus_CollectRecoversSectionPanic`, `TestStatus_UnavailableIsSortedBySectionOrder`, `TestStatus_LoudTail_*`, `TestStatus_BloomSaturationWarning`, `TestStatus_LatencyBudgets`, `TestStatus_LatencyRowsComeFromObsBudgets`, `TestStatus_BGIsNeverGated`, `TestStatus_LatencyFromDiskFile`, `TestStatus_LatencyMissingMetricsFile`, `TestStatus_GCIsDryRun`, `TestStatus_MapsAreSorted`), `statusop_test.go` (`TestStatusFullOpHandler_ReturnsSections`, `TestStatusFullOpHandler_RecoversPanic`, `TestStatusFullOpIsNotAKnownOp`), `statusfetch_test.go` (`TestStatus_FetchPrefersDaemon`, `TestStatus_DaemonPayloadMirrorIsCurrent`, `TestStatus_StatusFullUnknownOpFallsBackPerSection`, `TestStatus_DoesNotRegisterOpStatus`, `TestStatus_FetchFallsBackToDisk`, `TestStatus_FetchResolveError`, `TestStatus_FetchNilLogAndMetrics`, `TestStatus_HotPathNames`), plus `fakes_test.go`.
- [ ] Run `go test ./internal/commands` — must fail.
- [ ] Add `statussnapshot.go`, `collect.go`, `statusop.go` (`OpStatusFull`, `StatusFull`, `CollectFull`, `NewStatusFullOpHandler`), `statusfetch.go` (`daemonStatus`, `FetchSnapshot`), `metricsdisk.go` (`loadMetricsFromDisk`).
- [ ] `go run ./tools/devtool test-race` green for `./internal/commands`.
- [ ] `go test ./test/bench/...` and `go run ./tools/devtool bench-hotpath --iterations 2000` still green — the check that `ipc.OpStatus` and `daemon.StatusSnapshot` were genuinely left alone.

### Commit 3 — `feat(commands): render /qompack:status with the degraded banner`

- [ ] Write failing tests: `renderstatus_test.go` (`TestStatus_HumanGolden_Full`, `TestStatus_JSONGolden_Full`, `TestStatus_DegradedBanner`, `TestStatus_DegradedWithoutCriticalAssertion`, `TestStatus_SectionFilter`, `TestStatus_SectionFilter_JSON`, `TestStatus_SectionUnknown`, `TestStatus_NoGCFlag`, `TestDeterminism_RenderStatus`) and `BenchmarkRenderStatus`.
- [ ] Run `go test ./internal/commands -run Status` — must fail.
- [ ] Add `status.go`, `renderstatus.go`, `format.go` (`formatBytes`, `formatDuration`, `sevName`, `shortHash`).
- [ ] Generate goldens: `go test ./internal/commands -run Golden -update`; review the diff by eye against the §5.17 section list.
- [ ] `go test ./internal/commands -bench BenchmarkRenderStatus` under 5 ms/op.

### Commit 4 — `feat(commands): recall, why and dropped as MCP-handler frontends`

- [ ] Write failing tests: `mcpfront_test.go`, `recall_test.go`, `why_test.go`, `dropped_test.go` including `TestRecall_NoSecondImplementation` and `BenchmarkMCPFrontend`.
- [ ] Run `go test ./internal/commands -run 'Recall|Why|Dropped'` — must fail.
- [ ] Add `mcpfront.go`, `recall.go`, `why.go`, `dropped.go`; generate the six goldens.
- [ ] `go test ./internal/commands -bench BenchmarkMCPFrontend` p95 under 250 ms (**B-F**).

### Commit 5 — `feat(commands): pin, pin --eliminated and on-demand checkpoint-now`

- [ ] Write failing tests: `pin_test.go` (all 22 cases above) and `checkpointnow_test.go` (all 12 cases + `BenchmarkCheckpointNow`).
- [ ] Run `go test ./internal/commands -run 'Pin|Checkpoint'` — must fail.
- [ ] Add `pin.go`, `checkpointnow.go`; generate `pin_eliminated.*` and `checkpoint_now.*` goldens.
- [ ] `go test ./internal/commands -bench BenchmarkCheckpointNow` under 2 s (**B-E**).

### Commit 6 — `feat(commands): eval fraction-of-OPT report`

- [ ] Write failing tests: `eval_test.go` (all 15 cases above).
- [ ] Run `go test ./internal/commands -run Eval` — must fail.
- [ ] Add `eval.go`; generate `eval_report.*` goldens.
- [ ] `go run ./tools/devtool cover` — `internal/commands` **and** `internal/pluginmanifest` at or above the 75% floor (§6.4 "everything else"; neither package is in the 90%/85% groups).

### Commit 7 — `feat(cli): dispatch the seven §7.5 slash commands and register status.full`

- [ ] Write failing tests: `test/e2e/commands_test.go` (`TestE2E_EverySubcommandResolves`, `TestE2E_StatusAgainstRealDaemon`, `TestE2E_StatusWithDaemonStopped`, `TestE2E_CheckpointNowThenStatus`).
- [ ] Run `go test ./test/e2e -run TestE2E_` — must fail.
- [ ] Add `internal/cli/slash.go` (`slashCmds`, `runSlash`); remove the six `notImplemented` rows and splice `slashCmds()` into `All()` in `internal/cli/commands.go`; add the `errUsage` sentinel and its one branch in `internal/cli/dispatch.go`; register `commands.NewStatusFullOpHandler` at daemon construction through `daemon.Options.Handle(commands.OpStatusFull, …)` — and **not** for `ipc.OpStatus`.
- [ ] Confirm the `arch/checkpoint-now-subcommand` pre-step is already on `develop` — `grep -n 'checkpoint-now' plans/00-ARCHITECTURE.md` must hit inside §2.3's tree (110-123). SP-14's branch does **not** edit `00-ARCHITECTURE.md`; the line is specified in the cli section above and landed before wave 4 was cut.
- [ ] `go run ./tools/devtool build test` green; run `./bin/qompack status` manually against a real project and eyeball every section; confirm `./bin/qompack status --section nope` exits **2**.

### Commit 8 — `build(plugin): generate plugin/commands and docs/commands.md, gate them in CI`

- [ ] Write failing tests: `tools/devtool/plugin_validate_test.go` (`TestPluginValidate_DetectsDrift`), `tools/devtool/gen_command_docs_test.go` (`TestGenCommandDocs_Idempotent`), and `internal/pluginmanifest/commands_files_test.go` (`TestManifestFiles_MatchCommittedPluginDir`, `TestManifestFiles_HasExactlySevenCommandFiles`, `TestCommandDocs_MatchesCommittedDocs`, `TestRenderCommand_ReferencesRealSubcommand`).
- [ ] Run `go test ./tools/... ./internal/pluginmanifest` — must fail.
- [ ] Add the `gen-command-docs` devtool task; extend `plugin-validate` with the four assertions; regenerate the seven `plugin/commands/*.md` with `go run ./tools/devtool plugin-validate --write` and commit them together with the generated `docs/commands.md`; add the two steps to the `docs` job in `.github/workflows/ci.yml`.
- [ ] `go run ./tools/devtool plugin-validate gen-command-docs` then `git diff --exit-code` — clean.
- [ ] `go run ./tools/devtool ci-local` green; push and confirm every CI job is green on the branch.

Merge back into `develop` with `--no-ff` in the wave-4 merge order.

---

## Subagent strategy

This subplan is small enough that subagents are unnecessary — it is one package, one extended manifest table, three small `cli` edits and one devtool task, all sequential. The implementer may optionally dispatch a single subagent to write the golden-fixture test bodies for commits 3–6 in parallel with the renderer implementation, provided that subagent is given this document's "Test plan (TDD)" section verbatim and writes only `_test.go` files and `testdata/golden/commands/**`.

---

## Exit criteria

**Quoted verbatim from `Qompack.md`, the criteria this slice must surface correctly:**

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

SP-14 does not *achieve* these numbers — SP-06, SP-05 and SP-02 do. SP-14's exit condition is that `/qompack:status` and `/qompack:eval` **report** them, correctly and deterministically, and that a wrong number is visible rather than silent.

**Local Definition of Done:**

- [ ] All seven §7.5 commands implemented; `commands.Names()` equals `["status","recall","pin","checkpoint","why","dropped","eval"]` in that order.
- [ ] `/qompack:status` renders all **ten** §5.17 elements plus daemon reachability (SP-14's own eleventh section): mode with a loud banner when degraded, contract table with expected vs observed, store size and dedup ratio, sketch fill ratio and estimated FP rate, latency percentiles for every entry of `obs.Budgets()` — B-A…B-F **and B-G**, with B-D and B-G rendered as reported-not-gated — plus the six `hook_controlled.<hook>` sub-rows §5.17 names **per hook**, always emitted in §3.4 order and reading `no samples` (`n == 0`, `pass == true`) until the unowned per-hook producer exists, frontier turn and residual tokens, last checkpoint seq and size, last scheduler `Decision.Breakdown`, GC statistics, the last five `Loud` messages, and daemon reachability.
- [ ] `ipc.OpStatus` is untouched: no `Handle` registration for it anywhere in SP-14's diff, `daemon.StatusSnapshot`'s shape unchanged, `internal/daemon` unedited, and `commands.OpStatusFull` absent from `ipc.KnownOps()` — proved by `TestStatus_DoesNotRegisterOpStatus` and `TestStatusFullOpIsNotAKnownOp`.
- [ ] `recall`, `why` and `dropped` reach data **only** through `mcp.Tool.Handler` — proved by `TestRecall_NoSecondImplementation`.
- [ ] `pin` writes through `pins.Store`; `pin --eliminated` writes through `negknow.Ledger` with a canonical §8.3 descriptor, an evidence hash and resolved `depends_on` hashes.
- [ ] `checkpoint-now` drives `Begin → Advance → Finalize` and honours the `ErrAlreadyEncoded` DPI guard.
- [ ] `eval` prints the fraction-of-OPT report, every aggregatable §11.2 secondary metric including the v1.2 latency trio (compaction pause, residual span, first-turn-after), and the §11.3 regression table; `Belady` is computed once per session, not once per session per policy.
- [ ] Every command supports `--json` with the `Envelope{command,schema,ok,error,data}` shape and `--help` (and `-h`); a failing run still emits a valid envelope and a non-zero exit; no command panics on a nil `Deps` member; every human output is byte-stable across 100 renders under `FakeClock`; no ANSI escapes and no direct `os.Stdout`/`os.Stderr` writes anywhere.
- [ ] `plugin/commands/*.md` and `docs/commands.md` are byte-identical to their generators; `devtool plugin-validate` asserts manifest ≡ binary ≡ docs and that each of the seven resolves to a real subcommand of the built binary.
- [ ] `commandstest` exists and is green: every `t.Skip` removed if SP-01 shipped it, or the suite created per the decision in "What exists in the repo when you start" (Rule W-1).
- [ ] `go run ./tools/devtool ci-local` green: `gofumpt -l` empty, `golangci-lint run` clean, `go vet`, `nomagic`, import-graph check (`commands` imports neither `cli` nor `daemon`; `pluginmanifest` does not import `commands`), `go test ./... -race`.
- [ ] Coverage for `internal/commands` ≥ 75% (§6.4 floor for "everything else").
- [ ] Benchmarks within budget: `BenchmarkCheckpointNow` < 2 s (**B-E**), `BenchmarkMCPFrontend` p95 < 250 ms (**B-F**), `BenchmarkCollect` p95 < 250 ms, `BenchmarkRenderStatus` < 5 ms.
- [ ] `bench-gate` and `replay-gate` still green on the branch — this subplan adds nothing to the hot path and must not move B-A. `bench-gate` is the specific gate at risk, because `test/bench/hotpath/measure.go` decodes `daemon.StatusSnapshot` over `ipc.OpStatus` for B-B and for the gated B-A row; leaving that op and that payload alone is what keeps it green, and re-running `devtool bench-hotpath` after commit 2 and again after commit 7 is how it is checked rather than assumed.
- [ ] All CI jobs green on `feat/sp14-slash-commands-and-observability`.

---

## Done checklist

- [ ] Every constant, formula, schema and table quoted in "Design context" has a corresponding implementation or rendering: §7.5 command list (command table), §8.7 tool table (`recall`/`why`/`dropped` delegation + ephemeral flag in the JSON envelope), §8.3 four elimination sources item 2 and the canonical descriptor (`pin --eliminated`), §8.3 scope semantics (`--scope`), §8.5 regeneration rule (`SourceSet` assembly), §11.1 fraction-of-OPT (`eval`), §11.2 secondary metrics (`eval` columns), §11.3 guardrails (latency budget rows + regression table), §11.4 bloom watch-for (saturation warning), §12 storage-growth and bloom-saturation rows (`status` store + sketch sections), Phase 1 4:1 ratio (dedup line), B-A/B-B/B-C/B-D/B-E/B-F **and B-G** (latency table, sourced from `obs.Budgets()`).
- [ ] Placeholder scan: `rg -n "TBD|FIXME|implement appropriately|handle edge cases" internal/commands internal/pluginmanifest internal/cli plugin/commands docs/commands.md` returns nothing (the plan file's own checklist line is the only permitted match anywhere).
- [ ] Type consistency: every signature used in the code matches "Interface contract" exactly; `commands.Command`, `commands.All` and the ten original `commands.Deps` fields are byte-identical to 00-ARCHITECTURE §5.17; the additive `Deps` fields are documented in-file with the reason each is needed.
- [ ] No §5 interface owned by another subplan was changed, and no method was added to one (Rule W-3). The only files touched outside SP-14's own (`internal/commands/**`, including the new `internal/commands/commandstest/`, and `testdata/golden/commands/**`) are: `internal/cli/slash.go` (new), `internal/cli/commands.go` (six `notImplemented` rows removed, one `append` line added), `internal/cli/dispatch.go` (the `errUsage` sentinel and its one branch), `internal/pluginmanifest/manifest.go` (the `CommandDoc` extension and the `renderCommand` body), `tools/devtool` (new task + assertions), `.github/workflows/ci.yml` (`docs` job), `plugin/commands/*.md` (regenerated by the existing generator), `docs/commands.md` (generated), `test/e2e/commands_test.go` (new). `00-ARCHITECTURE.md` is **not** in that list: §2.3's `checkpoint-now` line lands on the `arch/checkpoint-now-subcommand` pre-step before wave 4 is cut, not on this branch. No file under `internal/daemon`, `internal/mcp`, `internal/checkpoint`, `internal/negknow`, `internal/scheduler`, `internal/store`, `internal/ipc`, `internal/obs` or `internal/eval` is edited — in particular `internal/daemon/handlers.go`'s `StatusSnapshot` and `daemon.go`'s `defaultRoutes` are left exactly as SP-05 shipped them.
- [ ] Commit count verified: 8 commits, within the 5–8 range, each with a conventional-commit message and a `Refs: SP-14, §7.5, §8.7, §11, §12` footer.
- [ ] No `Co-Authored-By`, `Signed-off-by`, `Generated with` or 🤖 in any commit message, merge commit, tag or PR body.
- [ ] `Qompack.md` unmodified: `git diff develop -- Qompack.md` is empty.

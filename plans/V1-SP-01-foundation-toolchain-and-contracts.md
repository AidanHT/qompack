# SP-01: Foundation: repo init, Go toolchain, CI, config, .qompack layout, plugin manifest, no-op hooks, test scaffolding, and every interface stub

**Branch:** `feat/sp01-foundation-toolchain-and-contracts` (cut from `develop`) | **Wave:** 0 | **Prerequisites:** none — the dependency list is `[]`, so `develop` is the commit created by this subplan's own repository initialization | **Runs in parallel with:** nothing; SP-01 is the sole subplan of wave 0 | **Design sections:** §7.4, §7.5, Appendix C, §12 (configurable multipliers), Closing note | **Gaps closed:** none directly — SP-01 is the substrate on which G1–G10 are closed by later waves

---

## Mission

This slice creates the repository and everything that must exist before any other subplan can compile a line of code. It is the only serial subplan in the entire build: seventeen subplans across five waves branch from the `develop` it produces, and every one of them consumes its types, its config, its `.qompack/` layout rules, its test fixtures, and — most importantly — its compiling interface stubs. Nothing here is an algorithm. Everything here is a *seam*.

Qompack's design rests on a claim that only holds if the seams are fixed first: that a rate–distortion, cache-aware compaction layer can be built by many hands in parallel because the interfaces in `00-ARCHITECTURE.md` §5 are normative and shipped as compiling stubs with conformance suites from day one (decision D9). SP-01 is the mechanism that makes that true. It also owns three cross-cutting invariants that the design document states in prose and that must become *mechanical* here or they will be violated silently later: the append-only invariant of §7.4 (`checkpoints/`, `pins/`, `sketches/tried.bloom` are additive-only — the physical enforcement of the DPI argument in §4.6), the no-hardcoding rule of D11/§11.6 (`r`, `w`, TTL and every §11.3 budget are config keys with a lint gate), and the four closing-note build-order priorities, encoded as CI guards rather than as good intentions.

**What exists when you start.** A directory `C:/Users/Quant/Documents/Programming/Projects/qompack` containing exactly two things: `Qompack.md` (the canonical design document, immutable — never modify it) and `plans/` (this file, `00-ARCHITECTURE.md`, and the other seventeen subplan prompts). There is no `.git`, no `go.mod`, no source, no CI.

**What exists when you finish.** An initialized git repository with `main` and `develop`; a Go 1.26 module `github.com/qompack/qompack` that builds a single static binary; a linting and task-running toolchain including the in-repo `nomagic` analysis pass and the import-graph dependency-DAG check of §3.2; a complete GitHub Actions pipeline; the full Appendix C configuration system plus the §11.5 `runtime` extension namespace with five-layer precedence, per-leaf fallback-not-crash validation, provenance and JSON Schema emission; `internal/paths` with a mechanically enforced append-only guard, `WriteAtomic`, `Norm`/`Key` and Windows long-path handling; `internal/core`, `internal/hookio`, `internal/cli`, `internal/logging` (with the `Loud` channel), `internal/obs` (log-bucket histograms and budget IDs B-A..B-F) and a baseline `internal/tokens` estimator; the plugin bundle generated from `internal/pluginmanifest` with `plugin-validate` diffing; six no-op hook entry points that always exit 0; `internal/testutil` and the `test/e2e` harness; and a compiling `core.ErrNotImplemented` stub plus a `<pkg>test` conformance suite plus `testdata/golden/contracts/` fixtures for **every** interface in §5. `go build ./...`, `go vet ./...`, `golangci-lint run`, and `go test ./...` are all green, and CI is green on the branch.

---

## Design context (verbatim from Qompack.md)

### §7.4 — Directory layout and the append-only invariant

```
.qompack/                       # gitignored, project-root
├── config.json
├── objects/                       # content-addressed, zstd-compressed
│   └── ab/cd/abcdef…              # sha256, 2-level fanout
├── index/
│   ├── tool_use.jsonl             # tool_use_id → root hash, ts, tool, args
│   ├── files.json                 # path → [(ts, root_hash)] version history
│   └── segments.jsonl             # changepoint-delimited segment log
├── sketches/
│   ├── tried.bloom                # negative knowledge — NEVER regenerated
│   ├── touch.cms                  # file-touch frequency
│   └── explore.hll                # exploration cardinality
├── dag/
│   └── deps.jsonl                 # dependence edges for slicing
├── grammar/
│   └── actions.seq                # Sequitur state over the action log
├── checkpoints/
│   ├── 0001.json                  # immutable, importance-ordered
│   └── 0002.json
├── pins/
│   └── invariants.json            # user- and agent-pinned, never summarized
└── eval/
    ├── replay/                    # counterfactual fork logs
    └── opt/                       # Belady keep-sets
```

> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### §4.6 — the invariant being enforced

> **The only fix is structural: never compress a compression.** Encode each transcript segment exactly once, from the original on disk, and append. This is a non-negotiable invariant of the Qompack design and is enforced by construction in §8.4.

### §7.5 — Plugin manifest sketch

```json
{
  "name": "qompack",
  "version": "0.1.0",
  "description": "Cache-aware, retrieval-backed context compaction",
  "hooks": {
    "PostToolUse":       [{ "command": "qompack observe tool" }],
    "UserPromptSubmit":  [{ "command": "qompack observe prompt" }],
    "PreCompact":        [{ "command": "qompack checkpoint", "timeout": 20 }],
    "SessionStart":      [{ "command": "qompack session-start" }],
    "SessionEnd":        [{ "command": "qompack flush" }]
  },
  "mcpServers": {
    "qompack": { "command": "qompack", "args": ["mcp"] }
  },
  "commands": [
    "status", "recall", "pin", "checkpoint", "why", "dropped", "eval"
  ]
}
```

### §7.3 — Hook surface (the six hooks the manifest must cover)

| Hook | Layer | Responsibility |
|---|---|---|
| `PostToolUse` | L0 | Chunk and store tool results; update DAG, sketches, Sequitur; detect redundancy |
| `UserPromptSubmit` | L0 | Capture user intent **verbatim and immutably** (closes G2.3); update BOCD features |
| `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |
| `PreCompact` | L4 | Write immutable checkpoint; emit focus instructions via `custom_instructions` |
| `PostToolUse` (todo/git) | L3 | Task-boundary signals for the scheduler |
| `Stop` / `SubagentStop` | L0 | Capture subagent detail before it is double-compressed (closes G10.1) |
| `SessionEnd` | L1 | Flush, compact the store, write session index |

### Appendix C — configuration schema (the normative defaults `config.Defaults()` must reproduce)

```jsonc
{
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
  "scheduler": {
    "softFloorPct": 0.55,
    "hardCeilingMargin": 20000,
    "youngDaly": { "enabled": true, "measuredDeltaSeconds": null },
    "changepoint": { "hazardRate": 0.004, "features": ["paths","tools","time","todos"] },
    "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
    "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
  },
  "checkpoint": {
    "budgetTokens": 12000,
    "incrementalSpanInstruction": true,
    "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
    "tiers": { "never": ["invariants","user_intent","eliminated"],
               "late":  ["decisions","open_questions","current_work"],
               "first": ["pointers","narrative"] }
  },
  "sketches": {
    "bloom": { "capacity": 10000, "fpRate": 0.01 },
    "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
    "hll":   { "registers": 2048 }
  },
  "eliminations": {
    "requireEvidence": true,
    "defaultScope": "session",
    "rebuildOnStale": "nextIdle",
    "staleResponse": "flag"          // "flag" | "drop"
  },
  "retrieval": {
    "ephemeralResults": true,
    "defaultSpan": "minimal",        // "minimal" | "full"
    "promoteAfterExpansions": 2
  },
  "selection": {
    "slicing": "thin",
    "deltaScoring": "cheap",
    "submodular": { "lambda": 0.4, "lazyGreedy": true }
  },
  "eval": { "replayOnPhaseGate": true, "minSessions": 20 }
}
```

### §12 — the risk rows that make configurability mandatory

| Risk | Severity | Mitigation |
|---|---|---|
| **Undocumented hook contracts change** (G9.3): `SessionStart` `source=compact`, `additionalContext` reaching context, `PreCompact` timing | High | Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently. |
| Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |
| Hook latency on the hot path | Medium | Async queue-and-drain fallback; hard p99 budget |
| Cache multipliers change | Low | Read `r` and `w` from config, never hardcode |
| Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |

### §5.1 / §5.6 — the multipliers and the derived threshold that must never be literals

> Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing before tuning**, since the ratio drives several thresholds below.

> **Ski rental for the write decision.** … Practically: write the cache when expected remaining reads exceed `w/r ≈ 12.5`.

### §11.3 — Guardrails (the budgets `internal/obs` must express)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### §8.1 — the hot-path budget statement

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

### §8.2 — the retention default the GC policy config encodes

> **Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.

### §8.5 — the checkpoint artifact SP-01 must hand-author as a frozen `format` fixture

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

The Go shape that must serialize to exactly this is `00-ARCHITECTURE.md` §5.14 (`checkpoint.Checkpoint`), which adds `Decision.ID`/`Decision.Turn` (`core.DecisionID`, `core.TurnIndex`) — SP-01 declares that struct as part of the `checkpoint` stub and the frozen fixture in §16 is its serialization with every tier populated.

### §8.6 — the two budgets Appendix C has no key for (hence `runtime.rehydrate`)

> - A compact skill index (names and one-line descriptions only, ~450 tokens) so skill awareness returns

> **Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K. The whole point is that pointers plus retrieval replace eager restoration. Measure this in the harness before relaxing it.

### §8.7 — the retrieval keys the `runtime.mcp` namespace supports

> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch.

### §2.5 / Appendix A — the trigger arithmetic behind `hardCeilingMargin: 20000`

```
effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
autoCompactThreshold   = effectiveContextWindow − 13_000
```

### Appendix A — the formulas whose parameters live in Appendix C

```
Bloom filter sizing
m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7

Count-Min sizing
width = ⌈e/ε⌉      depth = ⌈ln(1/δ)⌉
ε = 0.001, δ = 0.01  →  2718 × 5 ≈ 54 KB @ 4-byte counters

Young–Daly optimal checkpoint interval
I* = √(2·δ·M)

Ski-rental cache-write threshold
write when  E[remaining reads] > w/r   (≈ 12.5 at r=0.1, w=1.25)
```

### Closing note (the four build-order priorities SP-01 encodes as CI guards)

> The four things that actually matter, if the plan has to be cut down:
>
> 1. **Phase 0.** Without measurement, everything else is opinion.
> 2. **Phase 1 + 2.** Content-addressed store with canonicalization and addressable tombstones, plus evidence-linked negative knowledge. Slightly more than the original weekend estimate once staleness is included — and staleness is not optional, because negative knowledge that cannot expire eventually blocks a viable approach and inverts the feature's value.
> 3. **The cache correction.** Do not ship slicing or submodular selection before p-selection. Selection quality is real, but an arbitrary subset of a cached prefix is a worst-case edit, and shipping it first would make the system measurably more expensive while looking smarter.
> 4. **The incremental-span instruction.** Once checkpoints exist, one paragraph of `custom_instructions` shrinks the most expensive call in the session and operationalizes never-compress-a-compression inside a pipeline the plugin otherwise cannot touch. It is the cheapest line in the entire plan relative to what it buys.
>
> Everything past Phase 5 is refinement on a system that already works.

---

## Out of scope

Every item below is named with the sibling subplan that owns it. SP-01 ships the **stub and the conformance suite** for each; it never ships the behaviour.

| Out of scope for SP-01 | Owner |
|---|---|
| Replay harness, Belady OPT, divergence metrics, `eval.Synthesize`, the 24-session synthetic corpus, `test/replay` driver, replay-gate enforcement logic | SP-02 |
| Bloom / Count-Min / HyperLogLog / Misra-Gries / MinHash algorithms, sketch binary formats' bodies, `MergeFrom`, `Scale`, `ResizeTarget`, `RebuildBloom` | SP-03 |
| FastCDC gear hash and boundary logic, canonicalizer bodies and per-tool rules, `canon.Restore`, symbol extraction | SP-04 |
| `internal/ipc` transport bodies (named pipe / unix socket server, framing, ACK), `internal/daemon` in full, `internal/contract` assertion *bodies* and the degradation state machine, `qompack self-test`, `test/bench/hotpath` harness, the B-A/B-B/B-D/B-E bench-gate implementation | SP-05 |
| `internal/store` behaviour, `internal/redact` rule bodies, object layout writing, GC, exact chunk-level token accounting in `tokens.EstimateRoot` | SP-06 |
| `internal/dag` behaviour: edge model persistence, `BackwardSlice`/`ForwardSlice`, `CrossingEdges`, `NodesAfter` | SP-07 |
| `internal/observer` in full, real hook semantics behind the no-op entry points, tombstone rendering, supersession, verbatim capture | SP-08 |
| `internal/negknow` in full: descriptors, ledger, staleness, bloom-as-cache | SP-09 |
| `internal/checkpoint` and `internal/pins` behaviour, `ExtractDecisions`, `FocusInstructions`, `Truncate`, `ValidatePointers` | SP-10 |
| `internal/rehydrate`, `internal/rules`, `internal/skills` behaviour | SP-11 |
| `internal/scheduler` behaviour: BOCD, Young–Daly, composite trigger, p-selection, `PSelectionAvailable`'s real answer | SP-12 |
| `internal/mcp` JSON-RPC server body and the eight tool handlers | SP-13 |
| `internal/commands` behaviour and the *content* of `plugin/commands/*.md` beyond the shell-out skeleton; `/qompack:status` rendering; `docs/commands.md` | SP-14 |
| `internal/analyzer` and `internal/grammar` behaviour | SP-15 |
| Warm start, promotion, per-segment blooms, ski-rental policy application | SP-16 |
| `goreleaser` *release execution*, `plugin/bin` population, cross-platform validation matrix, security audit, `qompack fsck`/`doctor` bodies, release pipeline | SP-17 |
| `README.md`, `docs/architecture.md`, `docs/user-guide.md`, `docs/troubleshooting.md`, `docs/mcp-tools.md`, `docs/commands.md`, `docs/cannot-do.md`, `docs/upstream-issues.md`, `docs/security.md`, `docs/uat.md`, `docs/adr/**` | SP-18 |

**One docs exception, stated so the `docs` CI job is satisfiable.** `docs/config-reference.md` is **generated and committed by SP-01**, not by SP-18: §8's `docs` job runs `devtool gen-config-docs --check` and `git diff --exit-code`, which requires the generated file to already be in the tree on this branch. SP-01 owns the generator *and* its output; SP-18 owns every other page under `docs/` and may link to this one but never hand-edits it.

---

## Interface contract

### Consumes

SP-01 has no in-repo prerequisites. It consumes only the Go standard library and the two closed-list runtime dependencies of §2.5, at these exact call sites:

```go
// github.com/klauspost/compress/zstd — used by internal/store/compress.go (real, tested)
func zstd.NewWriter(w io.Writer, opts ...zstd.EOption) (*zstd.Encoder, error)
func zstd.NewReader(r io.Reader, opts ...zstd.DOption) (*zstd.Decoder, error)
func (e *zstd.Encoder) EncodeAll(src, dst []byte) []byte
func (d *zstd.Decoder) DecodeAll(input, dst []byte) ([]byte, error)

// github.com/Microsoft/go-winio — used by internal/ipc/dial_windows.go (build tag: windows)
func winio.DialPipe(path string, timeout *time.Duration) (net.Conn, error)
```

Test-only: `github.com/stretchr/testify/require`, `github.com/google/go-cmp/cmp`, `pgregory.net/rapid`.

### Produces

Everything below is what wave 1 and later depend on. Signatures are exactly as normative in `00-ARCHITECTURE.md` §4 and §5; SP-01 may not alter them.

**`internal/core`** — real, not a stub:

```go
type Hash [32]byte
func (h Hash) String() string            // "sha256:" + 64 lowercase hex
func (h Hash) Short() string             // first 12 hex chars, no prefix
func ParseHash(s string) (Hash, error)
func HashBytes(domain string, b []byte) Hash   // sha256(domain || 0x00 || b)

type SessionID string
type ToolUseID string
type TurnIndex int
type SegmentID int
type CheckpointSeq int
type DecisionID string
type Tokens int
type UnixMilli int64

type ChunkRef struct{ Hash Hash; Len int }
type Dep struct{ Path string; Hash Hash }

type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock

var (
    ErrNotImplemented = errors.New("qompack: not implemented")
    ErrNotFound       = errors.New("qompack: not found")
    ErrAppendOnly     = errors.New("qompack: append-only violation")
    ErrAlreadyEncoded = errors.New("qompack: segment already encoded (DPI guard)")
    ErrBudget         = errors.New("qompack: budget exceeded")
    ErrDegraded       = errors.New("qompack: running in degraded mode")
    ErrContract       = errors.New("qompack: host contract violated")
)

// SP-01 addition, used by the build-order guards and by every stub test:
func IsNotImplemented(err error) bool     // errors.Is(err, ErrNotImplemented)
var Version = "0.1.0"                     // overridden at link time by SP-17
func NowMilli(c Clock) UnixMilli
func (t UnixMilli) Time() time.Time
```

**`internal/paths`** — real:

```go
type Layout struct {
    Root, Dot                       string // project root, <root>/.qompack
    Objects, Index, Sketches, DAG   string
    Grammar, Checkpoints, Pins, Eval string
    Records, State, Run, Spool, Logs, Metrics, Tmp string
}
func Resolve(getenv func(string) string, payloadCWD string) (string, error)
func Global(home string) string            // <home>/.qompack — the §3.3 cross-project layer
func Of(root string) Layout
func EnsureLayout(l Layout) error          // mkdirs + writes <root>/.qompack/.gitignore == "*\n"
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func KeyFold(p string, fold bool) string
func DefaultFold() bool                    // true on windows and darwin
func WriteAtomic(p string, b []byte, perm fs.FileMode) error
func AppendOnly(p string) (io.WriteCloser, error)
func AppendJSONL(p string, v any) error
func CreateNew(p string, b []byte) error   // O_EXCL then chmod read-only
func OpenFile(p string, flag int, perm fs.FileMode) (*os.File, error)
func IsProtected(root, p string) bool
func ReplaceBloom(l Layout, b []byte, seq int) error
func Long(p string) string                 // \\?\ prefixing on windows; identity elsewhere
func CheckpointPath(l Layout, seq core.CheckpointSeq) string   // <checkpoints>/0007.json
func ManifestPath(l Layout) string                             // <checkpoints>/MANIFEST.jsonl
type ManifestEntry struct {
    Seq core.CheckpointSeq `json:"seq"`; SHA256 string `json:"sha256"`
    Bytes int64 `json:"bytes"`; Created core.UnixMilli `json:"created"`
}
func AppendManifest(l Layout, e ManifestEntry) error
func ReadManifest(l Layout) ([]ManifestEntry, error)
```

**`internal/config`** — real, full §5.1 surface, plus `RuntimeCfg` spelled out under "Implementation spec".

**`internal/logging`**, **`internal/obs`**, **`internal/hookio`**, **`internal/tokens`** (baseline), **`internal/cli`**, **`internal/pluginmanifest`** — real, full §5.2/§5.3/§5.20 surfaces.

**Stubs returning `core.ErrNotImplemented`, with exact §5 signatures:** `chunk`, `canon`, `symbols`, `redact`, `sketch`, `store`, `dag`, `grammar`, `negknow`, `analyzer`, `scheduler`, `checkpoint`, `pins`, `rehydrate`, `rules`, `skills`, `mcp`, `commands`, `eval`, `ipc`, `daemon`, `observer`, `contract`.

**Conformance suites** (`<pkg>test` subpackages), each exporting `Run…Suite(t *testing.T, name string, factory func(*testing.T) T)`:

```
chunktest    canontest    sketchtest   symbolstest  redacttest   tokenstest
storetest    dagtest      negknowtest  grammartest  analyzertest schedulertest
checkpointtest pinstest   rehydratetest rulestest   skillstest   mcptest
evaltest     ipctest      contracttest observertest
```

**22 suites.** The rule that fixes the set, so nobody has to guess: *one suite for every package in §5 that declares an interface a later subplan implements, named `<pkg>test` with no exceptions* (hence `rulestest` for `internal/rules` and `skillstest` for `internal/skills` — §5.15 gives `rules.Scanner` and `skills.Indexer` equal standing and both are SP-11's). The packages that declare an interface and still get **no** suite are exactly the composition roots `daemon`, `commands` and `cli`: their behaviour is delegation, and delegation is covered end to end by `test/e2e` rather than by a factory-driven suite. `logging` and `obs` also have none, because SP-01 implements them itself and tests them directly.

`storetest` exports **two** suites, per §5.22: `RunStoreSuite(t, name, factory func(*testing.T) store.Store)` and `RunSegmentLogSuite(t, factory func(*testing.T) store.SegmentLog)`.

**`internal/testutil`**:

```go
type Project struct{ Root string; Cfg config.Config; Clock *FakeClock; Log logging.Logger }
func NewProject(t *testing.T, opts ...ProjectOpt) *Project
func (p *Project) Store(t *testing.T) store.Store
func (p *Project) RunHook(t *testing.T, name string, e hookio.Event) hookio.Output
func (p *Project) WithFiles(t *testing.T, files map[string]string) *Project
func (p *Project) AssertAppendOnly(t *testing.T)
type FakeClock struct{ /* … */ }
func NewFakeClock(t0 time.Time) *FakeClock
func (c *FakeClock) Now() time.Time
func (c *FakeClock) Since(t time.Time) time.Duration
func (c *FakeClock) Advance(d time.Duration)
func Golden(t *testing.T, name string, got []byte)          // -update rewrites
func GoldenJSON(t *testing.T, name string, v any)
func WindowsHostileFiles() map[string]string
```

---

## Implementation spec

Repo-relative paths throughout. Module path: `github.com/qompack/qompack`.

### 1. Repository skeleton

**`.gitignore`** — exactly the normative minimum of `00-ARCHITECTURE.md` §9:

```gitignore
# Qompack runtime store — never committed
/.qompack/
**/.qompack/

# Build artifacts
/bin/
/dist/
/plugin/bin/
*.exe
*.dll
*.so
*.dylib
*.test
*.out
coverage.*
/testdata/bench-*.json

# Recorded sessions contain user code and prompts
/testdata/sessions/recorded/*
!/testdata/sessions/recorded/.gitkeep

# Tooling
.idea/
.vscode/*
!.vscode/extensions.json
.DS_Store
Thumbs.db
```

**`LICENSE`** — MIT, `Copyright (c) 2026 Qompack contributors`.

**`.gitattributes`**:

```
* text=auto eol=lf
*.go text eol=lf
*.md text eol=lf
*.golden -text
testdata/corpora/** -text
*.png binary
*.zst binary
```

`*.golden -text` and `testdata/corpora/** -text` are load-bearing: golden files and fuzz corpora must be byte-identical on Windows checkouts or every golden test fails on the dev machine.

**`.editorconfig`**: `root = true`; `[*] end_of_line = lf, insert_final_newline = true, charset = utf-8`; `[*.go] indent_style = tab`; `[*.{json,jsonc,yml,yaml,md}] indent_style = space, indent_size = 2`.

**`go.mod`**:

```
module github.com/qompack/qompack

go 1.26

toolchain go1.26.4

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/google/go-cmp v0.7.0
	github.com/klauspost/compress v1.18.0
	github.com/stretchr/testify v1.10.0
	golang.org/x/tools v0.34.0
	pgregory.net/rapid v1.1.0
)
```

Indirect requirements (`davecgh/go-spew`, `pmezani/go-difflib`, `gopkg.in/yaml.v3`, `golang.org/x/sys`, `golang.org/x/mod`, `golang.org/x/sync`) are whatever `go mod tidy` resolves; `go.sum` is committed.

**Decision (resolved, not open): which module `tools/` belongs to.** `00-ARCHITECTURE.md` §2.6 fixes the invocation as `go run ./tools/devtool <task>` from the repository root, and `gen-config-docs` must reach `internal/config`. Both are impossible if `tools/` carries its own `go.mod`, because a nested module is excluded from the parent module's package pattern and cannot be run with `./tools/...`. Therefore:

- **`tools/devtool/**` and `tools/lint/nomagic/**` are packages of the *root* module.** `go run ./tools/devtool <task>` and `go run ./tools/lint/nomagic ./...` work from the root, exactly as §2.6 and §8 write them.
- `golang.org/x/tools` is therefore a root-module requirement (it supplies `go/analysis`, `singlechecker` and `analysistest`, which §2.6 and §6.1 both mandate). It is a **build-time** dependency only: `devtool lint` sub-check `bindeps` runs `go list -deps ./cmd/qompack` and fails if `golang.org/x/tools`, `stretchr/testify`, `google/go-cmp` or `pgregory.net/rapid` appears, so §2.5's "the entire runtime dependency list is zstd + winio" stays true of the shipped binary.
- The **external tool binaries** (gofumpt, golangci-lint, govulncheck, benchstat) are pinned in a separate nested module at **`tools/pinned/`**, which nothing in the root module imports, so they never reach any `go list ./...` output.

**`tools/pinned/go.mod`**:

```
module github.com/qompack/qompack/tools/pinned

go 1.26

require (
	github.com/golangci/golangci-lint v1.64.8
	golang.org/x/perf v0.0.0-00010101000000-000000000000
	golang.org/x/vuln v1.1.4
	mvdan.cc/gofumpt v0.8.0
)
```

`golang.org/x/perf` (benchstat) has no tagged release; resolve its pseudo-version once by running `go get golang.org/x/perf/cmd/benchstat@latest` inside `tools/pinned/` and commit the resulting `tools/pinned/go.mod` and `tools/pinned/go.sum` (this replaces the placeholder pseudo-version above). `tools/pinned/tools.go` carries `//go:build tools` and blank-imports `mvdan.cc/gofumpt`, `github.com/golangci/golangci-lint/cmd/golangci-lint`, `golang.org/x/vuln/cmd/govulncheck` and `golang.org/x/perf/cmd/benchstat` so the pins are real. Every invocation of a pinned tool anywhere in this repository is spelled `go run -modfile=tools/pinned/go.mod <import/path>`.

### 2. `.golangci.yml`

```yaml
run:
  timeout: 5m
  build-tags: []
linters:
  disable-all: true
  enable:
    - govet
    - staticcheck
    - errcheck
    - revive
    - gocritic
    - ineffassign
    - unconvert
    - unparam
    - misspell
    - bodyclose
    - gosec
    - forbidigo
    - copyloopvar
linters-settings:
  forbidigo:
    forbid:
      - p: '^fmt\.Print.*$'
        msg: "use logging.Logger or write to the io.Writer the caller supplied"
      - p: '^time\.Sleep$'
        msg: "banned outside test/bench — take a core.Clock (§6.1)"
      - p: '^net\.Dial$'
        msg: "no network I/O (D10); unix sockets are dialled in internal/ipc only"
    exclude-godoc-examples: false
  errcheck:
    check-type-assertions: true
  revive:
    rules:
      - name: exported
      - name: package-comments
issues:
  exclude-rules:
    - path: (_test\.go|^test/|^tools/)
      linters: [forbidigo, gosec, unparam]
    - path: ^internal/ipc/
      text: 'net\.Dial'
      linters: [forbidigo]
  max-issues-per-linter: 0
  max-same-issues: 0
```

### 3. `tools/devtool` — the task runner

**`tools/devtool/main.go`** — `switch os.Args[1]` over the canonical task names of §2.6, each implemented in its own file. `devtool` is a package of the *root* module (so it can import `internal/config` for `gen-config-docs`) and shells out to the pinned tools via `go run -modfile=tools/pinned/go.mod <import/path>`.

Tasks and exact behaviour:

| Task | Behaviour | Exit non-zero when |
|---|---|---|
| `fmt` | `go run -modfile=tools/pinned/go.mod mvdan.cc/gofumpt -l -w .` | never (mutating) |
| `fmt-check` | same with `-l` only; prints offending files | any file listed |
| `lint` | `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./...` ; then `nomagic` ; then `importgraph` ; then `testdeps` ; then `bindeps` ; then `sleepcheck` ; then `stubskips` | any sub-check fails |
| `vet` | `go vet ./...` | vet fails |
| `build` | `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/qompack/qompack/internal/core.Version=$(version)" -o bin/qompack ./cmd/qompack` | build fails |
| `build-all` | the 6 §2.6 targets into `dist/qompack-<os>-<arch>[.exe]` | any fails |
| `test` | `go test ./...` | tests fail |
| `test-race` | `go test -race ./...` | tests fail |
| `cover` | `go test -coverprofile=coverage.out -covermode=atomic ./...` then per-group floor check (§6.4) | any group under floor |
| `bench` | `go test -bench=. -benchmem -run '^$' ./...` | bench fails to build |
| `bench-hotpath` | runs `go run ./test/bench/hotpath` with the passed flags if `test/bench/hotpath` exists; otherwise prints `bench-hotpath: harness not present (owned by SP-05)` and exits 0 | harness present and B-A p99 ≥ budget |
| `replay` | runs `go run ./test/replay` with the passed flags if that directory exists; otherwise prints `replay: driver not present (owned by SP-02)` and exits 0 | driver present and gate fails |
| `plugin-validate` | regenerates the bundle from `internal/pluginmanifest` into a temp dir, byte-compares against `plugin/`, then asserts 7 commands and (once `mcp.RegisterAll` is real) 8 tools | any diff |
| `fsck` | `go run ./cmd/qompack fsck` | non-zero exit from the binary |
| `ci-local` | `fmt-check` → `lint` → `vet` → `build` → `test` → `cover` → `plugin-validate` → `gen-config-docs` (check mode) | any step fails |
| `gen-config-docs` | renders `docs/config-reference.md` from `config.Defaults()` + schema metadata; `--check` mode diffs instead of writing | drift in `--check` |
| `gen-contract-fixtures` | writes/records `testdata/golden/contracts/**` (see §12 below) | recording requested for a package whose implementation is a stub |
| `install-hooks` | writes `.git/hooks/commit-msg` | write fails |
| `check-commit-msg <file>` | validates a Conventional Commit message and rejects attribution trailers | invalid |

**`tools/devtool/importgraph.go`** — the §3.2 dependency-DAG check. It runs `go list -json ./internal/... ./cmd/... ./test/...`, maps each package to its declared allow-set, and reports every internal import not in the set. (`./test/...` is included so the two test composition roots below are actually checked for the "nothing may import them" half of the rule.) The allow-set table is transcribed verbatim from §3.2 into `tools/devtool/importrules.go`:

```go
var foundation = []string{"core", "paths", "config", "logging", "obs"}

// compositionRoots may import anything; nothing may import them.
// The first five are §3.2's list; test/e2e and test/guards are added by SP-01 because they
// import the whole tree and must be subject to the same "nothing may import them" half.
var compositionRoots = map[string]bool{
    "daemon": true, "cli": true, "commands": true, "testutil": true, "cmd/qompack": true,
    "test/e2e": true, "test/guards": true,
}

var allow = map[string][]string{
    "core":      {},
    "paths":     {"core"},
    "config":    {"core"},
    "logging":   {"core", "paths", "config"},
    "obs":       {"core", "paths", "config"},
    "hookio":    {}, "sketch": {}, "chunk": {}, "symbols": {}, "redact": {},
    "grammar":   {}, "rules": {}, "skills": {}, "pins": {}, "tokens": {},
    "eval":      {}, "scheduler": {},               // foundation-only, added implicitly
    "canon":     {"sketch"},
    "dag":       {},
    "store":     {"chunk", "canon", "sketch", "symbols", "redact", "tokens"},
    "negknow":   {"sketch", "store", "dag"},
    "analyzer":  {"store", "dag", "sketch", "scheduler"},
    "checkpoint":{"store", "dag", "negknow", "pins", "grammar", "tokens"},
    "rehydrate": {"checkpoint", "store", "negknow", "dag", "rules", "skills", "tokens"},
    "mcp":       {"store", "negknow", "checkpoint"},
    "contract":  {"hookio", "store"},
    "ipc":       {"hookio", "contract"},
    "observer":  {"hookio", "store", "chunk", "canon", "sketch", "dag", "grammar", "negknow", "tokens"},
    "pluginmanifest": {},
}
// Every non-foundation package additionally gets `foundation` appended at check time.
// A package absent from `allow` and absent from `compositionRoots` is an error:
// new packages must be declared here, which forces an architecture amendment.
```

Additional assertion: for every composition root R and every non-root package P, `P` must not import `R`. And `internal/<pkg>test` conformance subpackages are permitted to import their own package plus `testutil`, `core`, and the test-only dependencies.

**`tools/devtool/testdeps.go`** — `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` (`.Imports` excludes test-only imports); fails if any line contains `stretchr/testify`, `google/go-cmp`, or `pgregory.net/rapid`, except for `internal/testutil`, the `internal/<pkg>/<pkg>test` conformance subpackages, and anything under `test/`, all of which legitimately export test helpers.

**`tools/devtool/bindeps.go`** — the §2.5 runtime-dependency assertion, and the reason `golang.org/x/tools` in the root `go.mod` is harmless: `go list -deps ./cmd/qompack` must contain **no** module path other than the standard library, `github.com/qompack/qompack/…`, `github.com/klauspost/compress/…` and `github.com/Microsoft/go-winio/…`. Any other module in the shipped binary's transitive closure — `golang.org/x/tools`, testify, go-cmp, rapid — fails the check by name. Run for all six §2.6 `GOOS`/`GOARCH` pairs so a build-tagged import cannot sneak in on one platform.

**`tools/devtool/sleepcheck.go`** — `go/ast` scan for `time.Sleep` selector calls in every non-`test/bench` file, including `_test.go`. Reports file:line. This is §6.1's "wall-clock sleeps are banned".

### 4. `tools/lint/nomagic` — the D11 / §11.6 analysis pass

**`tools/lint/nomagic/literals.go`** — the literal set, exactly §11.6's plus `8000` as §11.6 itself instructs:

```go
var forbiddenFloats = []float64{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}
var forbiddenInts   = []int64{20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}
```

**`tools/lint/nomagic/nomagic.go`** — an `x/tools/go/analysis` pass:

```go
var Analyzer = &analysis.Analyzer{
    Name: "nomagic",
    Doc:  "forbids literals that duplicate a config default outside internal/config/defaults.go (D11, §11.6)",
    Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
    for _, f := range pass.Files {
        name := pass.Fset.Position(f.Pos()).Filename
        if exemptFile(name) { continue }
        allow := allowedLines(pass.Fset, f)   // lines carrying "//nomagic:allow <reason>"
        ast.Inspect(f, func(n ast.Node) bool {
            lit, ok := n.(*ast.BasicLit); if !ok { return true }
            line := pass.Fset.Position(lit.Pos()).Line
            if allow[line] { return true }
            switch lit.Kind {
            case token.INT:
                v, err := strconv.ParseInt(lit.Value, 0, 64)
                if err == nil && containsInt(forbiddenInts, v) {
                    pass.Reportf(lit.Pos(), "literal %d duplicates a config default; read it from config (D11, §11.6)", v)
                }
            case token.FLOAT:
                v, err := strconv.ParseFloat(lit.Value, 64)
                if err == nil && containsFloat(forbiddenFloats, v) {
                    pass.Reportf(lit.Pos(), "literal %s duplicates a config default; read it from config (D11, §11.6)", lit.Value)
                }
            }
            return true
        })
    }
    return nil, nil
}
```

`exemptFile` returns true for: `internal/config/defaults.go`, any path ending `_test.go`, anything under `tools/`, `test/`, or `testdata/`. Float comparison is on the **parsed value**, so `0.10` and `1e-1` are caught as `0.1`; an `//nomagic:allow <reason>` comment on the same line exempts that line and the reason is mandatory (a bare `//nomagic:allow` is itself reported).

`tools/lint/nomagic/main.go` wraps it in `singlechecker.Main(Analyzer)` so `devtool lint` can invoke it as `go run ./tools/lint/nomagic ./...`.

Package-level test `nomagic_test.go` uses `analysistest.Run` against `tools/lint/nomagic/testdata/src/a/a.go`, which contains one violation per literal class plus one allowed line, with `// want` comments.

### 5. `internal/core`

**`core/hash.go`**

```go
type Hash [32]byte

func HashBytes(domain string, b []byte) Hash {
    h := sha256.New()
    h.Write([]byte(domain))
    h.Write([]byte{0x00})
    h.Write(b)
    var out Hash
    copy(out[:], h.Sum(nil))
    return out
}

func (h Hash) String() string { return "sha256:" + hex.EncodeToString(h[:]) }
func (h Hash) Short() string  { return hex.EncodeToString(h[:])[:12] }

func ParseHash(s string) (Hash, error) {
    s = strings.TrimPrefix(s, "sha256:")
    if len(s) != 64 { return Hash{}, fmt.Errorf("%w: hash must be 64 hex chars, got %d", ErrNotFound, len(s)) }
    b, err := hex.DecodeString(s)
    if err != nil { return Hash{}, fmt.Errorf("%w: %v", ErrNotFound, err) }
    var out Hash; copy(out[:], b); return out, nil
}
```

`Hash` also implements `MarshalJSON`/`UnmarshalJSON` producing/accepting the `"sha256:…"` text form, because every on-disk record in §5 embeds hashes as strings.

**`core/ids.go`** — the ID/scalar types plus `func NewDecisionID(seed []byte) DecisionID { return DecisionID("dec_" + HashBytes("qompack.decision", seed).Short()) }`.

**`core/clock.go`** — `systemClock` implementing `Clock`; `NowMilli`; `UnixMilli.Time()`.

**`core/errors.go`** — the seven sentinels verbatim plus `IsNotImplemented`.

**`core/version.go`** — `var Version = "0.1.0"`.

Domain-separation registry, documented in `core/hash.go` as a comment block and asserted by a test that these exact strings are used nowhere else with different meanings:

```
qompack.chunk.v1     chunk content hashes            (SP-04)
qompack.root.v1      Merkle root over chunk hashes   (SP-04/06)
qompack.neg.v1       negative-knowledge bloom key    (SP-09)
qompack.decision     decision IDs                    (SP-10)
qompack.args.v1      tool-arg digests                (SP-06)
```

**The one deliberate non-member.** `00-ARCHITECTURE.md` §2.4 defines the IPC endpoint name as
"first 12 hex chars of `sha256(normalizedAbsProjectRoot)`" — an **undomained** digest. `HashBytes`
prepends `domain || 0x00` and would therefore produce a different name, so `ipc.Resolve` calls
`sha256.Sum256` directly and this registry deliberately has no `qompack.project.v1` entry. The
comment block says so, so a later reader does not "fix" it into `HashBytes` and silently move every
project's pipe name.

### 6. `internal/paths`

**`paths/resolve.go`** — §3.3 resolution order, exactly:

```go
func Resolve(getenv func(string) string, payloadCWD string) (string, error) {
    if v := getenv("QOMPACK_PROJECT_ROOT"); v != "" { return filepath.Abs(v) }
    if payloadCWD == "" { return "", fmt.Errorf("%w: no project root", core.ErrNotFound) }
    abs, err := filepath.Abs(payloadCWD)
    if err != nil { return "", err }
    for d := abs; ; {
        if fi, err := os.Stat(filepath.Join(d, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
            return d, nil
        }
        parent := filepath.Dir(d)
        if parent == d { break }
        d = parent
    }
    return abs, nil
}
```

(`.git` may be a file in a worktree — hence the `IsRegular` branch. Worktrees are explicitly encouraged by §9, so this case is not hypothetical.)

**`paths/layout.go`** — `Of(root)` fills every field from the §3.3 tree; `EnsureLayout` `MkdirAll`s `objects index sketches dag grammar checkpoints pins eval/replay eval/opt records state run spool logs metrics tmp` under `<root>/.qompack` with `0o700`, then writes `<root>/.qompack/.gitignore` containing exactly `*\n` via `WriteAtomic` if absent.

**`paths/norm.go`**

```go
func Norm(projectRoot, p string) (string, error) {
    if projectRoot == "" { return "", fmt.Errorf("%w: empty project root", core.ErrNotFound) }
    abs := p
    if !filepath.IsAbs(p) { abs = filepath.Join(projectRoot, p) }
    abs = filepath.Clean(abs)
    if r, err := filepath.EvalSymlinks(abs); err == nil { // resolve only when it stays inside root
        if inside(projectRoot, r) { abs = r }
    }
    rel, err := filepath.Rel(filepath.Clean(projectRoot), abs)
    if err != nil { return "", err }
    rel = filepath.ToSlash(rel)
    if rel == ".." || strings.HasPrefix(rel, "../") {
        return "", fmt.Errorf("%w: path escapes project root: %s", core.ErrNotFound, p)
    }
    return rel, nil
}

func DefaultFold() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }
func KeyFold(p string, fold bool) string { if fold { return strings.ToLower(p) }; return p }
func Key(p string) string { return KeyFold(p, DefaultFold()) }
```

`KeyFold` exists so golden fixtures can pin a fold setting and stay byte-identical across platforms; production code calls `Key`. Original casing is never destroyed — callers keep the `Norm` result alongside the `Key`.

**`paths/long.go` / `paths/long_windows.go`** — on Windows, `Long(p)` returns `\\?\` + the cleaned absolute path when `len(abs) >= 240` and the path is not already prefixed and not a UNC path (UNC gets `\\?\UNC\…`); elsewhere it is the identity. Every `os.OpenFile`/`os.Stat` inside `paths` goes through `Long`.

**`paths/atomic.go`**

```go
func WriteAtomic(p string, b []byte, perm fs.FileMode) error {
    root, ok := rootOf(p)                      // nearest ancestor containing ".qompack"
    if ok && IsProtected(root, p) {
        return fmt.Errorf("%w: WriteAtomic on protected path %s", core.ErrAppendOnly, p)
    }
    dir := tmpDirFor(p)                        // <root>/.qompack/tmp, else filepath.Dir(p)
    if err := os.MkdirAll(dir, 0o700); err != nil { return err }
    f, err := os.CreateTemp(Long(dir), "wa-")
    if err != nil { return err }
    tmp := f.Name()
    defer func() { _ = os.Remove(Long(tmp)) }()
    if _, err := f.Write(b); err != nil { f.Close(); return err }
    if err := f.Sync(); err != nil { f.Close(); return err }
    if err := f.Close(); err != nil { return err }
    if err := os.Chmod(Long(tmp), perm); err != nil { return err }
    if err := os.Rename(Long(tmp), Long(p)); err != nil { return err }
    return fsyncDir(filepath.Dir(p))           // no-op on windows
}
```

**`paths/appendonly.go`** — the mechanical §7.4 guard:

```go
func IsProtected(root, p string) bool {
    l := Of(root)
    rel, err := filepath.Rel(l.Dot, filepath.Clean(p))
    if err != nil || strings.HasPrefix(rel, "..") { return false }
    rel = filepath.ToSlash(rel)
    switch {
    case rel == "sketches/tried.bloom":       return true
    case strings.HasPrefix(rel, "checkpoints/"): return true
    case strings.HasPrefix(rel, "pins/"):        return true
    }
    return false
}

// OpenFile is the ONLY opener allowed anywhere in the codebase for paths under .qompack.
func OpenFile(p string, flag int, perm fs.FileMode) (*os.File, error) {
    root, ok := rootOf(p)
    if ok && IsProtected(root, p) {
        if flag&os.O_TRUNC != 0 {
            return nil, fmt.Errorf("%w: O_TRUNC on %s", core.ErrAppendOnly, p)
        }
        if flag&(os.O_WRONLY|os.O_RDWR) != 0 && flag&os.O_APPEND == 0 && flag&os.O_EXCL == 0 {
            return nil, fmt.Errorf("%w: non-append write on %s", core.ErrAppendOnly, p)
        }
    }
    return os.OpenFile(Long(p), flag, perm)
}

func AppendOnly(p string) (io.WriteCloser, error) {
    if filepath.Ext(p) != ".jsonl" && !strings.HasSuffix(p, ".ndjson") && !strings.HasSuffix(p, ".log") {
        return nil, fmt.Errorf("%w: AppendOnly is for *.jsonl/*.ndjson/*.log only: %s", core.ErrAppendOnly, p)
    }
    return OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}

func CreateNew(p string, b []byte) error {
    f, err := OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
    if err != nil { return err }             // os.ErrExist for a second write of 0007.json
    if _, err := f.Write(b); err != nil { f.Close(); return err }
    if err := f.Sync(); err != nil { f.Close(); return err }
    if err := f.Close(); err != nil { return err }
    return os.Chmod(Long(p), 0o444)          // FILE_ATTRIBUTE_READONLY on Windows
}

// ReplaceBloom is the ONLY legal way to replace sketches/tried.bloom (§3.3).
func ReplaceBloom(l Layout, b []byte, seq int) error {
    cur := filepath.Join(l.Sketches, "tried.bloom")
    if _, err := os.Stat(Long(cur)); err == nil {
        bak := filepath.Join(l.Sketches, fmt.Sprintf("tried.bloom.%d.bak", seq))
        if err := os.Rename(Long(cur), Long(bak)); err != nil { return err }
        pruneBloomBackups(l, 1)              // keep exactly one generation
    }
    tmp := filepath.Join(l.Tmp, fmt.Sprintf("tried.bloom.%d", seq))
    if err := os.MkdirAll(Long(l.Tmp), 0o700); err != nil { return err }
    if err := os.WriteFile(Long(tmp), b, 0o600); err != nil { return err }
    return os.Rename(Long(tmp), Long(cur))
}
```

`AppendJSONL(p, v)` marshals `v` with `json.Marshal` (no HTML escaping: use an `Encoder` with `SetEscapeHTML(false)`), appends a single `\n`-terminated line through `AppendOnly`, and rejects any payload containing a raw newline.

`CheckpointPath` formats `%04d.json`; `AppendManifest` appends a `ManifestEntry` to `checkpoints/MANIFEST.jsonl` — and note that `MANIFEST.jsonl` lives *under* `checkpoints/`, so `IsProtected` covers it and only `AppendOnly` can write it, which is exactly the intent.

**Error handling per failure mode.** Truncating a protected path → `core.ErrAppendOnly`. Writing an existing checkpoint seq → `os.ErrExist` (callers wrap as `ErrAppendOnly`). `WriteAtomic` on a protected path → `ErrAppendOnly`. `Rename` across volumes cannot occur because `tmp/` is inside `.qompack/`. A read-only target on Windows makes `os.Rename` fail with `ERROR_ACCESS_DENIED`; `WriteAtomic` retries once after `os.Chmod(p, 0o600)` and returns the original error if the retry fails.

### 7. `internal/config`

**`config/config.go`** — the §5.1 types verbatim, plus the concrete section structs. Every leaf carries five struct tags: `json`, `doc`, `rng` (valid range as text), `enum` (pipe-separated), `sec` (the `Qompack.md` section that motivates it). Example:

```go
type CacheCfg struct {
    ReadMultiplier  float64 `json:"readMultiplier"  doc:"prompt-cache read multiplier r"  rng:"(0,1]"  sec:"§5.1"`
    WriteMultiplier float64 `json:"writeMultiplier" doc:"prompt-cache write multiplier w" rng:"[1,∞)"  sec:"§5.1"`
    TTLSeconds      int     `json:"ttlSeconds"      doc:"sliding cache TTL"               rng:"(0,∞)"  sec:"§5.4"`
}
```

**Struct-tag rule (applies to every config struct in this subplan, including the `runtime` ones below).** The one-line struct summaries that follow elide tags for readability; the real declarations carry all five on every leaf. The `json` tag is **mandatory and explicit** on every field — never inferred — and its value is exactly the key spelling of Appendix C / §11.5 (`idleExitSeconds`, `readMultiplier`, `nearDupThreshold`, `promoteAfterExpansions`, …). Go's default marshalling would emit the exported Go name and silently break both the Appendix C golden and the §11.5 key contract, so `TestDefaults_MatchesAppendixCVerbatim` and `TestDefaults_RuntimeNamespace` exist precisely to catch a missing tag.

Section structs, exhaustively: `StoreCfg{Chunk ChunkCfg; Compression string; Retention RetentionCfg; Canonicalize CanonicalizeCfg}`, `ChunkCfg{Min,Target,Max int}`, `RetentionCfg{Days,Sessions int}`, `CanonicalizeCfg{Enabled bool; Strip []string; MinHash MinHashCfg}`, `MinHashCfg{Enabled bool; Permutations int; NearDupThreshold float64}`, `SchedulerCfg{SoftFloorPct float64; HardCeilingMargin int; YoungDaly YoungDalyCfg; Changepoint ChangepointCfg; Cache CacheCfg; Idle IdleCfg}`, `YoungDalyCfg{Enabled bool; MeasuredDeltaSeconds *float64}`, `ChangepointCfg{HazardRate float64; Features []string}`, `IdleCfg{DetectAfterSeconds int; BackgroundWork, DeepCutWhenCold bool}`, `CheckpointCfg{BudgetTokens int; IncrementalSpanInstruction bool; Frontier FrontierCfg; Tiers TiersCfg}`, `FrontierCfg{AdvanceOnSegmentClose bool; MaxResidualTokens int}`, `TiersCfg{Never, Late, First []string}`, `SketchesCfg{Bloom BloomCfg; CMS CMSCfg; HLL HLLCfg}`, `BloomCfg{Capacity int; FPRate float64}`, `CMSCfg{Epsilon, Delta float64; WarmStartFromProject bool}`, `HLLCfg{Registers int}`, `EliminationsCfg{RequireEvidence bool; DefaultScope, RebuildOnStale, StaleResponse string}`, `RetrievalCfg{EphemeralResults bool; DefaultSpan string; PromoteAfterExpansions int}`, `SelectionCfg{Slicing, DeltaScoring string; Submodular SubmodularCfg}`, `SubmodularCfg{Lambda float64; LazyGreedy bool; Enabled bool \`json:"-"\`}`, `EvalCfg{ReplayOnPhaseGate bool; MinSessions int}`.

**Decision (resolved, not open): where the §5.12 ship-order flag lives.** §5.12 names `config.SelectionCfg.Submodular.Enabled`, but Appendix C has no such key and §11.1 requires `Defaults()` to serialize to Appendix C byte-for-byte. Therefore `SubmodularCfg.Enabled` carries `json:"-"` and is a **derived** field: `Load` sets it from `Runtime.Selection.SubmodularEnabled` (§11.5 namespace, default `false`). The Appendix C golden stays exact and §5.12's Go-level name is honoured.

**`config/runtime.go`** — the §11.5 namespace, spelled out with the two additive groups SP-01 owns:

```go
type RuntimeCfg struct {
    Mode      string        `json:"mode"`       // auto | full | passive | off
    Daemon    DaemonCfg     `json:"daemon"`
    HotPath   HotPathCfg    `json:"hotPath"`
    Logging   LogCfg        `json:"logging"`
    Redact    RedactCfg     `json:"redact"`
    Telemetry TelemetryCfg  `json:"telemetry"`
    Rehydrate RehydrateCfg  `json:"rehydrate"`
    MCP       MCPCfg        `json:"mcp"`
    Budgets   BudgetsCfg    `json:"budgets"`    // SP-01 addition: §11.3 budgets B-B..B-F as keys
    Selection RSelectionCfg `json:"selection"`  // SP-01 addition: closing-note-3 ship-order gate
    Tokens    RTokensCfg    `json:"tokens"`     // SP-01 addition: baseline estimator constants
}
type DaemonCfg   struct{ Enabled bool; IdleExitSeconds, MaxSessions, AckDeadlineMs, ConnectDeadlineMs int }
type HotPathCfg  struct{ BudgetMs, BreachWindows int; SpoolOnBreach bool; MaxPayloadBytes int }
type LogCfg      struct{ Level string; MaxFileMB, MaxFiles int }
type RedactCfg   struct{ Enabled bool; Patterns []string }
type TelemetryCfg struct{ Enabled bool }
type RehydrateCfg struct{ MinTokens, MaxTokens, SkillIndexTokens, EliminationsTopN int }
type MCPCfg      struct{ SpanWidenLines, MaxResponseBytes int }
type BudgetsCfg  struct{ L0IngestMs, L0ProcessMs, CheckpointFinalizeMs, MCPToolCallMs int }
type RSelectionCfg struct{ SubmodularEnabled bool `json:"submodularEnabled"` }
type RTokensCfg  struct {
    ProseCharsPerToken, CodeCharsPerToken, JSONCharsPerToken   float64
    DiffCharsPerToken, BinaryCharsPerToken                      float64
    ImagePixelsPerToken, ImageMaxTokens, PDFTokensPerPage       int
    CalibrationMin, CalibrationMax, CalibrationAlpha            float64
}
```

`runtime.budgets` deliberately excludes B-A, which already has a key (`runtime.hotPath.budgetMs = 15`); duplicating it would create two sources of truth for the number §11.3 names. B-D is reported, never gated (§2.4), so it has no key.

**`config/defaults.go`** — the single file exempt from `nomagic`. It returns the Appendix C document exactly, plus:

```go
Runtime: RuntimeCfg{
    Mode: "auto",
    Daemon:  DaemonCfg{Enabled: true, IdleExitSeconds: 1800, MaxSessions: 8, AckDeadlineMs: 8, ConnectDeadlineMs: 5},
    HotPath: HotPathCfg{BudgetMs: 15, BreachWindows: 3, SpoolOnBreach: true, MaxPayloadBytes: 1048576},
    Logging: LogCfg{Level: "info", MaxFileMB: 10, MaxFiles: 5},
    Redact:  RedactCfg{Enabled: true, Patterns: []string{}},
    Telemetry: TelemetryCfg{Enabled: false},
    Rehydrate: RehydrateCfg{MinTokens: 8000, MaxTokens: 12000, SkillIndexTokens: 450, EliminationsTopN: 8},
    MCP:     MCPCfg{SpanWidenLines: 40, MaxResponseBytes: 262144},
    Budgets: BudgetsCfg{L0IngestMs: 2, L0ProcessMs: 50, CheckpointFinalizeMs: 2000, MCPToolCallMs: 250},
    Selection: RSelectionCfg{SubmodularEnabled: false},
    Tokens: RTokensCfg{
        ProseCharsPerToken: 4.0, CodeCharsPerToken: 3.6, JSONCharsPerToken: 3.2,
        DiffCharsPerToken: 3.4, BinaryCharsPerToken: 3.0,
        ImagePixelsPerToken: 750, ImageMaxTokens: 1600, PDFTokensPerPage: 1800,
        CalibrationMin: 0.6, CalibrationMax: 1.6, CalibrationAlpha: 0.2,
    },
},
```

**`config/jsonc.go`** — tolerant reader. A single-pass state machine over the bytes with four states (`normal`, `inString`, `lineComment`, `blockComment`), escape-aware inside strings; it replaces comment bytes with spaces (preserving byte offsets so `Provenance.Location` line numbers stay exact) and then removes commas that are followed only by whitespace before `}` or `]`. Exported: `func StripJSONC(b []byte) []byte`.

**`config/load.go`** — the five-layer merge:

```go
func Load(env Env) (Config, Provenance, []Warning, error) {
    keys := schemaKeys()                       // dotted-key → leaf kind, built by reflection over Config
    merged := toMap(Defaults())                // map[string]any tree
    prov := Provenance{}
    for k := range keys { prov[k] = Source{Origin: OriginDefault, Location: "config.Defaults()"} }
    var warns []Warning

    apply := func(raw []byte, origin Origin, loc string) {
        var layer map[string]any
        if err := json.Unmarshal(StripJSONC(raw), &layer); err != nil {
            warns = append(warns, Warning{Key: "", Message: "unparseable config: " + err.Error(), Location: loc})
            return
        }
        deepMerge(merged, layer, "", keys, prov, origin, loc, &warns)
    }
    // Layers 2–5, in exactly this order. Missing files are not errors.
    // NOTE: §3.2 gives `config` the allow-set {core} — it may NOT import `paths`. The two
    // config-file locations are therefore joined inline here rather than via paths.Global/Of.
    userPath    := filepath.Join(env.HomeDir, ".qompack", "config.json")
    projectPath := filepath.Join(env.ProjectRoot, ".qompack", "config.json")
    if b, err := os.ReadFile(userPath); err == nil    { apply(b, OriginUserFile, userPath) }
    if b, err := os.ReadFile(projectPath); err == nil { apply(b, OriginProjectFile, projectPath) }
    applyEnv(merged, env.Getenv, keys, prov, &warns)     // QOMPACK_<SEC>__<KEY>__<SUB>
    applyFlags(merged, env.Flags, keys, prov, &warns)    // --set dotted.key=value

    cfg := fromMap(merged)
    cfg.Runtime.Selection.SubmodularEnabled → cfg.Selection.Submodular.Enabled
    for _, v := range cfg.Validate() {
        setLeaf(merged, v.Key, defaultLeaf(v.Key))
        warns = append(warns, Warning{Key: v.Key, Location: prov[v.Key].Location,
            Message: fmt.Sprintf("invalid value, using default: %v not in %v", v.Got, v.Want)})
        prov[v.Key] = Source{Origin: OriginDefault, Location: "fallback after violation"}
    }
    cfg = fromMap(merged)                      // re-derive after fallbacks
    return cfg, prov, warns, nil               // error is ALWAYS nil unless env.ProjectRoot == ""
}
```

`deepMerge` walks the incoming tree; for each **leaf** present in `keys` it overwrites and records provenance; for each key not in `keys` it emits `Warning{Key: dotted, Message: "unknown key", Location: loc}` and drops it (§11.3 forward compatibility). Maps merge recursively; arrays replace wholesale (a project that sets `store.canonicalize.strip` means exactly that list).

Env mapping: for each `QOMPACK_`-prefixed variable, drop the prefix, split on `__`, lowercase each part, and match case-insensitively against the dotted key index; unmatched → `Warning`. `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER=0.08` → `scheduler.cache.readMultiplier`. Values are parsed according to the leaf's reflected kind: `bool` via `strconv.ParseBool`, numbers via `ParseFloat`/`ParseInt`, `[]string` by splitting on `,`, `*float64` where the literal `null` yields `nil`.

Flags: `--set <dotted.key>=<value>` accumulated by `cli` into `Env.Flags`, applied last with `OriginFlag` and `Location: "--set"`.

`Location` for file layers is `"<path>:<line>"`, computed by re-scanning the (comment-blanked) bytes for the leaf's key path with a small offset-tracking decoder (`json.Decoder` + `InputOffset()`), then converting offset → line.

**`config/validate.go`** — every rule of §11.3, one `Violation` per failing leaf, never a panic:

```
store.chunk.min < store.chunk.target < store.chunk.max
store.compression ∈ {zstd, none}
store.retention.days ≥ 1 ; store.retention.sessions ≥ 1
store.canonicalize.strip ⊆ {timestamps, ansi, pids, addresses, tmpPaths, durations, crlf, paths}
store.canonicalize.minhash.permutations ∈ [16,512]
store.canonicalize.minhash.nearDupThreshold ∈ (0,1]
scheduler.softFloorPct ∈ (0,1)
scheduler.hardCeilingMargin > 0
scheduler.changepoint.hazardRate ∈ (0,1)
scheduler.cache.readMultiplier ∈ (0,1] ; scheduler.cache.writeMultiplier ≥ 1 ; scheduler.cache.ttlSeconds > 0
checkpoint.budgetTokens ∈ [1000,100000]
checkpoint.frontier.maxResidualTokens > 0
checkpoint.tiers.{never,late,first} ⊆ {invariants,user_intent,eliminated,decisions,open_questions,
    current_work,pointers,narrative}, pairwise disjoint, union == that whole set
sketches.bloom.capacity ≥ 100 ; sketches.bloom.fpRate ∈ (0,0.25)
sketches.cms.epsilon ∈ (0,1) ; sketches.cms.delta ∈ (0,1)
sketches.hll.registers is a power of two in [64,65536]
eliminations.defaultScope ∈ {session,project}
eliminations.rebuildOnStale ∈ {nextIdle,immediate,never}
eliminations.staleResponse ∈ {flag,drop}
retrieval.defaultSpan ∈ {minimal,full} ; retrieval.promoteAfterExpansions ≥ 1
selection.slicing ∈ {thin,full} ; selection.deltaScoring ∈ {cheap,medium,expensive}
selection.submodular.lambda ≥ 0
eval.minSessions ≥ 1
runtime.mode ∈ {auto,full,passive,off}
runtime.hotPath.budgetMs > 0 ; runtime.hotPath.breachWindows ≥ 1 ; runtime.hotPath.maxPayloadBytes ≥ 4096
runtime.logging.level ∈ {debug,info,warn,error}
runtime.rehydrate.minTokens ≥ 1 ; minTokens ≤ maxTokens ; skillIndexTokens ≥ 1 ; eliminationsTopN ≥ 1
runtime.mcp.spanWidenLines ≥ 0 ; runtime.mcp.maxResponseBytes ≥ 4096
runtime.tokens.* charsPerToken ∈ [1,20] ; imagePixelsPerToken ≥ 1 ; pdfTokensPerPage ≥ 1
runtime.tokens.calibrationMin ∈ (0,1] ; calibrationMax ≥ 1 ; calibrationAlpha ∈ (0,1]
runtime.budgets.* > 0
runtime.telemetry.enabled must be false            ← hardwired; a true value is a Violation
```

**Who reports a violation, and why it is not `config` itself.** §11.3 requires every violating leaf to fall back to its default, be reported through `logging.Loud`, and be recorded in `.qompack/state/config-violations.json`. `config` can do the *fallback* but must do neither of the other two: §3.2 gives `logging` and `obs` the allow-set `{core, paths, config}`, so `logging` imports `config` and the reverse edge is an import cycle; and `config`'s own allow-set is `{core}`, so it cannot reach `paths.WriteAtomic` either. The responsibility therefore splits, and the split is normative because three later subplans call `Load`:

- **`config.Load` performs the per-leaf fallback and *returns* the evidence.** §5.1 fixes `Load`'s return tuple, so the evidence travels in `[]Warning`: one entry per violation, `Warning{Key: "<dotted.key>", Message: "invalid value, using default: <got> not in <want>", Location: "<file>:<line>"}`, alongside the unknown-key warnings. `config` also exports `func ViolationsFromWarnings(ws []Warning) []Violation`, a pure decoder over those messages, so the caller gets the typed §11.3 list without a second `Load` and without `config` growing a stateful field. `Load` never writes a file, never logs, and — per its own contract — never returns a non-nil error for a bad value.
- **The composition root that called `Load` reports it.** `cli` (and later `daemon`) does, immediately after `Load` returns: `logging.Loud` once per violation, then `paths.WriteAtomic(l.State + "/config-violations.json", …)` with the JSON array of `Violation`. `cli.LoadConfigAndReport(env) (config.Config, config.Provenance, error)` is the single helper both roots call, so the reporting exists exactly once and no caller can forget it.

`TestLoad_InvalidLeafFallsBackNotCrash` asserts the `config`-side half (fallback + warnings + nil error); `TestCLI_ConfigViolationsAreLoudAndPersisted` in `internal/cli` asserts the reporting half (a `Loud` line per violation and the file on disk). This is §11.3's "Behaviour on invalid config is not crash", implemented without violating §3.2.

**`config/schema.go`** — `JSONSchema()` reflects over `Config`, emitting draft-2020-12 with `title`, `type`, `default`, `description` (from `doc`), `enum` (from `enum`), and `x-qompack-section` (from `sec`). Deterministic key order (sorted) so the output is golden-testable.

**`config/provenance.go`** — `Provenance.Render(cfg Config, w io.Writer)` prints the effective config as JSON with a trailing `// <origin> <location>` comment per leaf, which is what `qompack config print --provenance` emits.

### 8. `internal/logging`

`logging/logging.go`: `New(dir string, lvl Level) (Logger, io.Closer, error)` opens `<dir>/qompack-YYYYMMDD.log` via `paths.AppendOnly`, wraps a `slog`-free hand-rolled line writer emitting `ts=<RFC3339Nano> level=<lvl> msg="…" k=v …` with values quoted by `strconv.Quote` when they contain space or `=`. Rotation on `MaxFileMB`, keeping `MaxFiles`, renaming to `qompack-YYYYMMDD.<n>.log`.

`logging/loud.go`: `Loud` writes the same line to three destinations — the day log, `<dir>/LOUD.log` (append-only, never rotated), and an in-memory ring of the last 32 loud messages exposed as `func LastLoud() []string` for `/qompack:status` (SP-14 consumes it).

Counting loud events in `obs` must not make `logging` import `obs`: §3.2 gives `logging` the allow-set `{core, paths, config}`, and `logging → obs` would be rejected by `devtool lint`'s own `importgraph` check. The observer is therefore a plain function, with no `obs` type in the signature:

```go
// package logging
func AttachLoudObserver(fn func(msg string, kv ...any))   // nil clears; last writer wins
```

The composition roots (`cli`, `daemon`) wire it once at start-up with `func(msg string, kv ...any) { reg.Counter("loud.total").Add(1) }`. `TestLoud_ThreeDestinations` installs a counting closure directly and asserts it fired once; a separate `TestLoud_CounterWiring` in `internal/cli` asserts the real `obs` registry sees `loud.total == 1` after a `Loud` call through the dispatcher.

`Nop()` returns a logger discarding everything except `Loud`, which it still records in the ring (so tests can assert loudness without a filesystem).

### 9. `internal/obs`

`obs/hist.go` — the fixed-bucket log histogram (§2.5 says "we use a fixed-bucket log histogram"). 1/8-octave buckets over microseconds:

```go
const nBuckets = 256      // 8 buckets/octave × 32 octaves: 1µs … ~1.2 h

func bucketFor(u uint64) int {
    if u == 0 { return 0 }
    e := bits.Len64(u) - 1
    var f uint64
    if e >= 3 { f = (u >> uint(e-3)) & 7 } else { f = (u << uint(3-e)) & 7 }
    i := int(8*uint64(e) + f)
    if i >= nBuckets { i = nBuckets - 1 }
    return i
}
func bucketUpper(i int) time.Duration {   // inclusive upper bound of bucket i, in µs
    e, f := i/8, i%8
    lo := (uint64(8) + uint64(f)) << uint(e) >> 3
    hi := (uint64(8) + uint64(f) + 1) << uint(e) >> 3
    if hi <= lo { hi = lo + 1 }
    return time.Duration(hi) * time.Microsecond
}
```

`Snapshot()` computes P50/P95/P99/P999 by walking cumulative counts and returning `bucketUpper(i)` for the bucket containing the rank — deliberately **conservative** (for observations ≥ 8 µs it over-reports by at most 2^(1/8)−1 ≈ 9.05%; below 8 µs the buckets are 1 µs wide so the absolute error is ≤ 1 µs), so a latency gate can never pass because of rounding. Every budget in §2.4 is in milliseconds, three orders of magnitude above the coarse region, so the bound that matters is the 9.05% one. `Max` is tracked exactly as a separate `int64`. Counts are `uint32` per bucket (1 KB per histogram); `Observe` is lock-free via `atomic.AddUint32`, and `Snapshot` is a racy-but-monotone read, which is correct for metrics.

`obs/budgets.go` — the six budget IDs of §2.4, as data:

```go
type BudgetID string
const (
    BA BudgetID = "B-A" // hook_controlled: client main() → exit
    BB BudgetID = "B-B" // l0_ingest: daemon read → WAL append returned
    BC BudgetID = "B-C" // l0_process: WAL → stored + DAG/sketches updated
    BD BudgetID = "B-D" // hook_wall: includes host process creation (reported, never gated)
    BE BudgetID = "B-E" // checkpoint_finalize: PreCompact entry → exit
    BF BudgetID = "B-F" // mcp_tool_call: request → response
)
type Budget struct{ ID BudgetID; Hist string; Pct int; Gated bool; Limit func(config.Config) time.Duration }
func Budgets() []Budget
```

with limits read from config, never literals: `B-A → runtime.hotPath.budgetMs` (p99, gated), `B-B → runtime.budgets.l0IngestMs` (p99, gated), `B-C → runtime.budgets.l0ProcessMs` (p99, **not** gated — §2.4 calls it soft), `B-D → 0` (reported only, `Gated:false`), `B-E → runtime.budgets.checkpointFinalizeMs` (p99, gated), `B-F → runtime.budgets.mcpToolCallMs` (p95, gated). `Registry.CheckBudgets(cfg)` returns one `BudgetBreach` per gated budget whose observed percentile exceeds the limit, carrying `Windows` = consecutive breach count maintained per budget inside the registry.

`obs/registry.go` — `New(clock core.Clock) Registry`; maps guarded by `sync.RWMutex` with lazy creation; `Snapshot()` returns a deep copy; `Persist(l paths.Layout) error` writes `metrics/latency.json` via `paths.WriteAtomic`. `Timed(h, f)` records `time.Since` around `f` even when `f` returns an error.

### 10. `internal/tokens` — baseline estimator (G10.2 groundwork)

`tokens/classify.go`:

```go
func Classify(tool, path string, b []byte) Class {
    switch strings.ToLower(filepath.Ext(path)) {
    case ".png", ".jpg", ".jpeg", ".gif", ".webp": return ClassImage
    case ".pdf":                                    return ClassPDF
    case ".json", ".jsonl", ".ndjson":              return ClassJSON
    case ".patch", ".diff":                         return ClassDiff
    case ".md", ".txt", ".rst":                     return ClassProse
    }
    if len(b) >= 4 && (bytes.HasPrefix(b, []byte("%PDF")) ) { return ClassPDF }
    if looksBinary(b) { return ClassBinary }        // NUL in first 8 KB, or >30% non-printable
    if bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), []byte("diff --git")) { return ClassDiff }
    if json.Valid(b) { return ClassJSON }
    if tool == "FileRead" || tool == "Grep" || looksCode(b) { return ClassCode }
    return ClassProse
}
```

`looksCode` counts lines ending in `{`, `;`, `:` and lines beginning with common keywords (`func `, `def `, `class `, `import `, `const `, `let `, `var `, `#include`) — code if ≥ 15% of the first 200 lines match.

`tokens/estimate.go`:

```
raw(b, c) = ceil(len(b) / charsPerToken(c))                      for prose/code/json/diff/binary
raw(b, Image) = min(imageMaxTokens, ceil(w*h / imagePixelsPerToken))
                where (w,h) come from image.DecodeConfig; on failure ceil(len(b)/1000)
raw(b, PDF)   = pages * pdfTokensPerPage
                where pages = count of "/Type" followed by optional space and "/Page"
                not immediately followed by "s", scanned over the whole byte slice; min 1
Estimate = round(raw * Factor())
```

`Factor()` starts at **1.0** — an uncalibrated estimator must be the identity, which is what makes the arithmetic in the `TestEstimate_*` table exact — and is replaced on first load if `~/.qompack/calibration.json` holds an entry for this project.

`Calibrate(observed, estimated)` updates `factor = clamp(factor*(1-α) + α*float64(observed)/float64(estimated), calibrationMin, calibrationMax)` with α = `runtime.tokens.calibrationAlpha`, ignoring calls where `estimated == 0`, and persists `{ "<sha256(projectRoot).Short()>": factor }` to `paths.Global(home)/calibration.json` via `paths.WriteAtomic`.

`EstimateString(s, c)` is `Estimate([]byte(s), c)` and exists only so hot callers avoid a copy audit; it is part of the §5.20 surface and is tested by the same table.

`EstimateRoot(ctx, chunks []core.ChunkRef, c Class)` — baseline: `Σ ceil(chunk.Len / charsPerToken(c))`, memoized in an in-process `map[core.Hash]core.Tokens` so repeated roots over the same chunks are O(1). SP-06 replaces the per-chunk value with a measured one keyed by the same map; the signature and the memo key do not change.

### 11. `internal/hookio`

`hookio/event.go` — `ReadEvent(r io.Reader, limit int64) (Event, []byte, error)` reads at most `limit` bytes via `io.LimitReader`, returns `core.ErrBudget` when the limit is hit, unmarshals **twice**: once into `Event` (with all fields `omitempty`-tolerant) and once into `map[string]json.RawMessage`; every map key not claimed by a struct tag goes into `Extra`. Malformed JSON returns the raw bytes and a wrapped error so `cli` can log the payload and still exit 0. A `null` or missing field never panics because every field is a value type with a zero.

`hookio/output.go` — `WriteOutput` marshals with `SetEscapeHTML(false)` and a trailing `\n`. `Empty()` returns `Output{}` (serializes to `{}`). `SessionStartOutput(ctx string) Output` and `PreCompactOutput(instr string) Output` are convenience constructors that fill `HSO.HookEventName` correctly — SP-11 and SP-10 use them so the hook-event-name string appears once.

### 12. `internal/cli` and `cmd/qompack`

`cli/dispatch.go`:

```go
type Cmd struct {
    Name    string
    Hook    bool            // hook subcommands must ALWAYS exit 0 (§2.3)
    Run     func(ctx context.Context, env Env, args []string, out, errw io.Writer) error
}
type Env struct {
    Getenv func(string) string
    Stdin  io.Reader
    Set    map[string]string   // accumulated --set k=v
    Clock  core.Clock
}
func Dispatch(ctx context.Context, cmds []Cmd, argv []string, env Env, out, errw io.Writer) int
```

Exit-code policy, enforced by `Dispatch`:

| Situation | Exit |
|---|---|
| hook subcommand, any outcome including panic | **0** |
| `self-test`, assertion failure | 1 |
| non-hook subcommand, error | 1 |
| unknown subcommand or bad flags | 2 |
| `--help`/no args | 0 |

`cli/recover.go` wraps every `Run` in `defer func(){ if r := recover(); r != nil { log.Loud("panic in "+name, "recover", r, "stack", string(debug.Stack())); … } }()`; for hook commands the deferred handler additionally writes `hookio.Empty()` to stdout if nothing has been written yet, so the host always receives valid JSON.

`cli/hooks.go` — the six no-op hook entry points SP-01 ships. The order is forced and is not the obvious one: the payload limit is a config key, config lives under the project root, and the project root comes out of the payload. The cycle is broken by reading stdin under the **default** limit first.

1. `hookio.ReadEvent(env.Stdin, bootstrapLimit)` where `bootstrapLimit = int64(config.Defaults().Runtime.HotPath.MaxPayloadBytes)` (1 MiB). This is deliberate: a hook must never need a config file to read its own stdin. On any error, keep the zero `Event` and continue — the hook still has to answer.
2. `paths.Resolve(env.Getenv, ev.CWD)` (best effort; on failure, skip steps 3–4 and go straight to step 5).
3. `config.Load(config.Env{ProjectRoot: root, HomeDir: …, Getenv: env.Getenv, Flags: env.Set})`. If the *effective* `runtime.hotPath.maxPayloadBytes` is smaller than the bytes already read, log at `Warn` and record `truncated: true` in step 4 — never re-read, never fail.
4. `paths.EnsureLayout`, then append one line to `.qompack/logs/hooks-YYYYMMDD.jsonl` recording `{ts, hook, session_id, tool_name, bytes, truncated}` — no payload content, so the no-op hooks are observable in wave 0 without storing anything.
5. Write the response and return nil.

| Subcommand | Response written |
|---|---|
| `observe tool` | `hookio.Empty()` |
| `observe prompt` | `hookio.Empty()` |
| `observe stop [--subagent]` | `hookio.Empty()` |
| `session-start` | `hookio.SessionStartOutput("")` → `{"hookSpecificOutput":{"hookEventName":"SessionStart"}}` |
| `checkpoint` | `hookio.PreCompactOutput("")` → `{"hookSpecificOutput":{"hookEventName":"PreCompact"}}` |
| `flush` | `hookio.Empty()` |

Non-hook subcommands SP-01 ships: `config print [--provenance] [--json]`, `config schema`, `version`, `help`. Every other subcommand named in §2.3 (`mcp`, `daemon`, `status`, `recall`, `pin`, `why`, `dropped`, `eval`, `fsck`, `doctor`, `bench`, `self-test`) is registered with a `Run` that writes `qompack <name>: not implemented in this build\n` to `errw` and returns `core.ErrNotImplemented` (exit 1) — so the dispatch table is complete from day one and later subplans only replace a function value.

`cmd/qompack/main.go` — under 150 LOC: build `cli.Env`, call `cli.Dispatch(context.Background(), cli.All(), os.Args, env, os.Stdout, os.Stderr)`, `os.Exit` with the result. No package-level initialization beyond `var` declarations (§2.5: the hot path must not pay for init).

### 13. `internal/pluginmanifest` and `plugin/`

```go
type Manifest struct {
    Plugin   PluginJSON
    Hooks    HooksJSON
    MCP      MCPJSON
    Commands []CommandDoc
}
func Default(version string) Manifest
func (m Manifest) Files() (map[string][]byte, error)  // repo-relative path → exact bytes
func Write(dir string, m Manifest) error
type Diff struct{ Path, Reason string; Want, Got []byte }
func Validate(dir string, m Manifest) []Diff
```

`Files()` emits, with two-space indentation and a trailing newline (so `git diff --exit-code` is stable):

- `plugin/.claude-plugin/plugin.json` — exactly the §3.4 object, `version` from `core.Version`.
- `plugin/hooks/hooks.json` — exactly the §3.4 seven-entry object covering all six §7.3 hooks (`Stop` and `SubagentStop` are separate entries; `PostToolUse` carries `"matcher": "*"`).
- `plugin/.mcp.json` — exactly `{ "mcpServers": { "qompack": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack", "args": ["mcp"] } } }`.
- `plugin/commands/{status,recall,pin,checkpoint,why,dropped,eval}.md`.

Each command file is generated from a `CommandDoc{Name, Description, ArgumentHint, Subcommand, AllowedTools}` and has this exact shape (shown for `status`):

```markdown
---
description: Qompack status — mode, contracts, store, latency, last decision
argument-hint: "[--json]"
allowed-tools: Bash(qompack status:*)
---

Run the Qompack status command and report its output verbatim to the user.

!`qompack status $ARGUMENTS`
```

`Validate(dir, m)` compares every generated byte slice against the file on disk, returning a `Diff` for missing, extra (any file under `plugin/` not in `Files()` except `plugin/bin/**`), and differing files. `devtool plugin-validate` prints the diffs and exits 1 if any exist, additionally asserting `len(m.Commands) == 7` and — once `mcp.RegisterAll` no longer returns `ErrNotImplemented` — that exactly the eight §8.7 tool names are registered.

### 14. Interface stubs

#### 14.0 Types §5 names but does not define — decided here, once

Several §5 signatures reference a type §5 never spells out. SP-01 must declare them or nothing compiles, and if it declares them inconsistently the wave-1 subplans fight over the definition. These are the decisions; they are additive to §5 and require no amendment, and every one of them appears in the `testdata/golden/contracts/` fixture set so a later owner cannot quietly re-shape them.

```go
// package obs — §5.2 names Counter, Gauge and Snapshot without defining them.
type Counter interface{ Add(n int64); Value() int64 }
type Gauge   interface{ Set(v int64); Add(d int64); Value() int64 }
type Snapshot struct {
    TS       core.UnixMilli           `json:"ts"`
    Hists    map[string]HistSnapshot  `json:"hists"`
    Counters map[string]int64         `json:"counters"`
    Gauges   map[string]int64         `json:"gauges"`
}

// package ipc — §5.4 gives AddrKind and HotPathMode only as comments.
type AddrKind uint8
const (
    NamedPipe AddrKind = iota + 1
    UnixSocket
)
type HotPathMode uint8
const (
    HotSync HotPathMode = iota   // connect to the daemon (normal)
    HotSpool                     // §12.2: append to the spool, never connect
)
const (
    ACK byte = 0x06
    NAK byte = 0x15
)
const MaxLineBytes = 1 << 20     // §2.4 "1 MiB max line"

// package daemon — §5.4 Options.Sketches is *SketchSet, undefined.
type SketchSet struct {
    Tried   *sketch.Bloom
    Touch   *sketch.CMS
    Explore *sketch.HLL
    Top     *sketch.MisraGries
}

// package dag — §5.9 Graph.Stats() returns GraphStats, undefined.
type GraphStats struct{ Nodes, Edges int; ByKind map[NodeKind]int; Bytes int64 }

// package negknow — §5.10 Open takes deps Deps, undefined.
type Deps struct {
    Store store.Store; Graph dag.Graph
    Log logging.Logger; Metrics obs.Registry; Clock core.Clock
}

// package contract — §5.19 Env.History is typed History, undefined.
type History interface {
    Saw(id ID) bool                      // observed at least once, this session or a prior one
    LastSeen(id ID) (core.UnixMilli, bool)
    Record(id ID, at core.UnixMilli)
    Sessions() int                       // sessions observed; assertions needing "two sessions" read this
}

// package paths — one spelling for the global layer (~/.qompack), used by config and tokens.
func Global(home string) string           // filepath.Join(home, ".qompack")
```

`obs.Registry.Persist(l paths.Layout) error` is likewise an SP-01 addition to the §5.2 interface (it writes `metrics/latency.json`, which §3.3 requires but §5.2 has no method for). Adding to an interface the adding subplan owns is legal under §0; adding to someone else's is not.

#### 14.1 The stub pattern

Every package below ships the **complete** §5 type set (structs, enums, sentinels, options) as real declarations, and every constructor/method body as a stub. The stub pattern, uniformly:

```go
// package store — SP-06 owns the implementation.
type stubStore struct{}

func Open(root string, cfg config.Config, deps Deps) (Store, error) {
    return stubStore{}, nil          // constructing is legal; every operation is not
}
func (stubStore) Put(context.Context, io.Reader, PutOptions) (PutResult, error) {
    return PutResult{}, core.ErrNotImplemented
}
…
```

Three rules for stubs, all mechanically checked:

1. **Constructors succeed, operations fail.** `Open`/`New`/`NewX` return a usable value and a nil error so composition roots can be wired in wave 0; every method returns `core.ErrNotImplemented` (or, for methods with no error return, a zero value — `Bloom.Test` returns `false`, `Graph.CrossingEdges` returns `0`, `Sequitur.Rules` returns `nil`).
2. **No behaviour is faked.** A stub never returns plausible-looking data. `store.Stats` returns `Stats{}, core.ErrNotImplemented`, not a zeroed-but-nil-error `Stats{}`.
3. **Pure functions that are fully specified by the architecture are implemented, not stubbed.** These are: `chunk.Params.Validate`, `chunk.DefaultParams`, `chunk.RootHash`, `scheduler.YoungDaly`, `scheduler.SkiRentalShouldWrite`, `scheduler.PSelectionAvailable`, `negknow.Descriptor.Key`, `checkpoint.StripInjections` and the two injection-tag constants, `grammar.FormatWarning`, `rehydrate.StandingInstruction`, `observer.Tombstone`, `contract.Mode.String`, `redact.Nop`, and **`analyzer.NewSelector`'s constructor validation**. They have exact, closed definitions and later subplans consume them immediately:

```go
// chunk
func RootHash(chunks []Chunk) core.Hash {
    buf := make([]byte, 0, len(chunks)*32)
    for _, c := range chunks { buf = append(buf, c.Hash[:]...) }
    return core.HashBytes("qompack.root.v1", buf)
}

// scheduler
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64 {
    if deltaSeconds <= 0 || mtbfSeconds <= 0 { return 0 }
    return math.Sqrt(2 * deltaSeconds * mtbfSeconds)
}
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
    if r <= 0 { return false }
    return expectedReads > w/r          // §5.6 — computed, never written as 12.5
}
func PSelectionAvailable() bool { return pSelectionAvailable }
var pSelectionAvailable = false         // SP-12 flips this to a real capability report

// negknow
func (d Descriptor) Key() []byte {
    var b bytes.Buffer
    b.WriteString(d.NormalizedPath); b.WriteByte(0x1f)
    b.WriteString(d.Symbol);         b.WriteByte(0x1f)
    b.WriteString(d.ApproachClass);  b.WriteByte(0x1f)
    b.Write(d.ReasonHash[:])
    h := core.HashBytes("qompack.neg.v1", b.Bytes())
    return h[:]
}

// checkpoint
const InjectionOpenTag  = "<!-- qompack:injected seq=%d ver=%d -->"
const InjectionCloseTag = "<!-- /qompack:injected -->"
func StripInjections(s string) string   // removes every open…close span, non-greedy, tolerant of a
                                        // missing close tag (drops to end of string in that case)

// rehydrate
func StandingInstruction() string { return "Before committing to an approach, call already_tried." }

// observer
func Tombstone(rec store.ToolUseRecord) string {
    return fmt.Sprintf("[cleared: sha256:%s · %s · %s %s · re-expandable]",
        rec.Root.Short(), humanBytes(rec.Bytes), rec.Tool, rec.Path)
}

// grammar — §5.11 leaves the wording open; SP-01 closes it, because SP-08 injects this
// string through UserPromptSubmit and SP-15 asserts it. One line, no trailing newline.
func FormatWarning(w Warning) string {
    return fmt.Sprintf("[qompack] possible loop: %s repeated %d× (turns %s) — %s",
        strings.Join(symbolStrings(w.Rule.Expansion), "→"), w.Repeats, turnRange(w.Turns), w.Message)
}
// e.g. "[qompack] possible loop: Read→Edit→Bash repeated 11× (turns 42–74) — consider a
//       different approach"; turnRange renders "42–74" for a span and "42" for a single turn.

// contract — §5.19 declares Mode.String() without fixing the strings. These three are the
// strings §12 uses in prose and /qompack:status prints, so they are frozen here.
func (m Mode) String() string {
    switch m {
    case ModeFull:            return "full"
    case ModeDegradedPassive: return "degraded-passive"
    case ModeOff:             return "off"
    }
    return "unknown"
}

// analyzer — §13 invariant 4 and closing note 3 are structural, so the CONSTRUCTOR is real
// even though Select is a stub. This is what test/guards asserts in wave 0.
func NewSelector(p int, blocks []Block, sl dag.Slice, delta map[dag.NodeID]float64,
                 lambda float64, lazy bool) (Selector, error) {
    // Order matters: the Pos check runs FIRST so that the guard test asserting §13 invariant 4
    // gets that error in every build, not the ship-order error that would mask it today.
    for _, b := range blocks {
        if b.Pos < p {
            return nil, fmt.Errorf("%w: block %s at pos %d precedes p=%d (§5.3, §13 invariant 4)",
                core.ErrBudget, b.ID, b.Pos, p)
        }
    }
    if !scheduler.PSelectionAvailable() {
        return nil, fmt.Errorf("%w: submodular selection requires p-selection (closing note 3)",
            core.ErrNotImplemented)
    }
    return stubSelector{p: p}, nil
}
// stubSelector.P() returns p; stubSelector.Select returns Selection{}, core.ErrNotImplemented.
// SP-15 replaces Select and the filtering loop; it may not remove either check.
```

`observer.Tombstone`'s output is asserted byte-for-byte against §8.1's example (`[cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]`). `humanBytes` divides by **1024** (not 1000) and renders one decimal place with `B`/`KB`/`MB`/`GB` units and no space before the unit, so `2457 → "2.4KB"` (2457/1024 = 2.399…, truncated-then-rounded to one decimal); under 1024 it prints the integer plus `B`. The divisor is stated because 1000 would render the same input as `2.5KB` and the golden would drift.

`store/compress.go` (real, used by SP-06 later): `func Encode(b []byte) ([]byte, error)` / `func Decode(b []byte) ([]byte, error)` over a package-level `sync.Pool` of `zstd.Encoder`/`zstd.Decoder` at `zstd.SpeedDefault`, with a `MaxDecodedSize` of 64 MiB to bound allocation from untrusted input (§13 invariant 7).

`ipc/dial_windows.go` (real, `//go:build windows`): `func dial(a Addr, timeout time.Duration) (net.Conn, error) { return winio.DialPipe(a.Path, &timeout) }`. `ipc/dial_other.go` (`//go:build !windows`): `net.Dial("unix", a.Path)`. `ipc/resolve.go` is real and implements §2.4's path resolution including the 100-byte `sun_path` fallback and the endpoint-name derivation exactly as §2.4 words it:

```go
// §2.4: hash12 = first 12 hex chars of sha256(normalizedAbsProjectRoot).
// Undomained on purpose — see the domain registry note in core/hash.go.
func endpointHash(projectRoot string) (hash12, hash8 string) {
    sum := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(projectRoot))))
    h := hex.EncodeToString(sum[:])
    return h[:12], h[:8]
}
```

Windows yields `\\.\pipe\qompack.<hash12>`; POSIX tries `$XDG_RUNTIME_DIR/qompack/<hash12>.sock`, then `<os.TempDir()>/qompack-<uid>/<hash12>.sock`, and falls back to `<os.TempDir()>/qp-<hash8>.sock` when the resolved path exceeds 100 bytes. Directories are created `0700` and sockets `0600`.

`contract/contract.go` stub semantics — this is the one stub whose *behaviour* matters in wave 0, because §12.1 demands a fresh build report `ModeFull`:

```go
func StandardAssertions() []Assertion {
    return []Assertion{
        notYetImplemented(CSessionStartFires,          SevCritical, "SessionStart hook fires"),
        notYetImplemented(CSessionStartSourceCompact,  SevCritical, "SessionStart arrives with source=compact after PreCompact"),
        notYetImplemented(CAdditionalContext,          SevCritical, "additionalContext reaches the transcript"),
        notYetImplemented(CPreCompactTiming,           SevWarn,     "PreCompact has time to write"),
        notYetImplemented(CPreCompactCustomInstr,      SevWarn,     "custom_instructions accepted"),
        notYetImplemented(CHookPayloadShape,           SevCritical, "hook payload shape matches hookio.Event"),
        notYetImplemented(CMCPRegistered,              SevInfo,     "MCP server received initialize"),
        notYetImplemented(CTranscriptReadable,         SevWarn,     "transcript_path exists and parses"),
        notYetImplemented(CPluginRootResolves,         SevWarn,     "CLAUDE_PLUGIN_ROOT expands to an existing binary"),
    }
}

func notYetImplemented(id ID, declared Severity, desc string) Assertion {
    return Assertion{ID: id, Severity: declared, Description: desc,
        Check: func(ctx context.Context, e Env) Result {
            // §12.1: an assertion whose PRODUCER is absent from the build reports
            // OK/SevInfo, never a degradation. SP-05 replaces Check, not this rule.
            return Result{ID: id, OK: true, Severity: SevInfo,
                Expected: desc, Observed: "not-yet-implemented", TS: core.NowMilli(e.Clock)}
        }}
}
```

`NewMonitor` returns a monitor whose `RunAll` executes registered assertions, returns `ModeFull` unless some result has `OK == false && Severity == SevCritical`, and whose `Degrade` writes `state/contract.json` + `logging.Loud`. That much is real in SP-01 because the CI guard tests it; the assertion *observations* are SP-05's.

### 15. Conformance suites (`<pkg>test`)

One subpackage per interface, at `internal/<pkg>/<pkg>test/`. Each exports one `Run…Suite` per interface in that package and follows Rule W-1 exactly: **shape** assertions always run; **behaviour** assertions are guarded by a probe.

```go
// package storetest
func RunStoreSuite(t *testing.T, name string, factory func(t *testing.T) store.Store) {
    t.Run(name+"/shape", func(t *testing.T) {
        s := factory(t)
        require.NotNil(t, s)
        // every method is callable and either succeeds or returns a KNOWN sentinel
        _, err := s.PutBytes(context.Background(), []byte("x"), store.PutOptions{})
        requireKnownError(t, err)     // nil | ErrNotImplemented | ErrNotFound | ErrBudget | ErrDegraded
    })
    if skipIfStub(t, probe(factory)) { return }   // t.Skip("behaviour: implementation is a stub (Rule W-1)")
    t.Run(name+"/behaviour", func(t *testing.T) { … })
}

func probe(factory func(t *testing.T) store.Store) func(*testing.T) bool {
    return func(t *testing.T) bool {
        _, err := factory(t).PutBytes(context.Background(), []byte("probe"), store.PutOptions{})
        return core.IsNotImplemented(err)
    }
}
```

`skipIfStub` calls `t.Skip` with the exact message `behaviour: implementation is a stub (Rule W-1)`. A CI check (`devtool lint`, sub-check `stubskips`) greps test output for that message and **reports** the count; it becomes a merge blocker for a subplan only when the subplan owns the package — enforced by `tools/devtool/stubskips.go` reading `plans/OWNERS.tsv`.

`plans/OWNERS.tsv` is written by SP-01 and read by three sub-checks (`stubskips`, `cover`, `gen-contract-fixtures`). Tab-separated, one line per package, `#`-comments and a header line allowed:

```
package	owner	floor	probe
core	SP-01	75	-
paths	SP-01	90	-
config	SP-01	90	-
store	SP-06	90	PutBytes
negknow	SP-09	90	Query
…
```

`package` is the `internal/` name (or `cmd/qompack`); `owner` is the subplan ID; `floor` is the §6.4 line-coverage floor; `probe` names the method `isStub` calls to decide whether the package is still a stub (`-` for packages with no stub phase). All 23 stubbed packages plus the 10 SP-01 implements are listed, and `devtool lint` fails if a package exists on disk but not in the file — so a new package cannot appear without an ownership decision.

Behaviour blocks SP-01 authors up front, so the owning subplan inherits real tests rather than writing its own grader:

| Suite | Behaviour assertions authored by SP-01 (skipped until the owner lands) |
|---|---|
| `chunktest` | boundary stability under insertion (≤2 chunks perturbed); determinism; `Min ≤ len ≤ Max` except last; `RootHash` matches the domain-separated formula |
| `canontest` | idempotence; `Restore∘Canonicalize == identity` with `KeepDeltas`; never grows input; registration order preserved |
| `sketchtest` | marshal/unmarshal round-trip; CRC rejection; Bloom no-false-negatives; CMS `Estimate ≥ true count`; HLL within 3×2.3% on 100k keys; Misra-Gries no false positives; MinHash Jaccard within 0.1 of exact on known sets |
| `symbolstest` | `Enclosing` returns the smallest containing span; `Extract` is stable under CRLF |
| `redacttest` | idempotence; bounded growth; every built-in rule fires on its positive fixture and not on its negative |
| `tokenstest` | monotone in length; `Factor` clamped to `[calibrationMin, calibrationMax]`; `EstimateRoot` equals the sum over chunks |
| `storetest` | put/get round-trip; global dedup (second put of identical bytes has `Novel == 0`); `ChangedSince` detects a hash change; `MarkEncoded` returns `ErrAlreadyEncoded` on a second seq; append-only files never shrink |
| `dagtest` | slice scores descend; `Thin` drops `EdgeControlOnly`; `CrossingEdges` counts straddling edges exactly |
| `negknowtest` | three-way `absent`/`active`/`stale`; bloom rebuilt from active records only; `BloomOnly` set when no record backs a hit |
| `grammartest` | Sequitur's two invariants after every append |
| `analyzertest` | `NewSelector` errors on any `block.Pos < p`; errors while `PSelectionAvailable()` is false |
| `schedulertest` | `Evaluate` is pure (same `Inputs` → identical `Decision`, no I/O); composite trigger truth table; `YoungDaly` matches √(2δM) |
| `checkpointtest` | schema round-trip; `Truncate` drops tier 3 then tier 2 and never tier 1; goldens contain no fenced code blocks (§13 invariant 5) |
| `pinstest` | append-only; `Remove` writes a tombstone and `All` excludes it; `Materialize` regenerates the view |
| `rehydratetest` | `Items` in the normative `ItemKind` order; total tokens ≤ `Budget` |
| `rulestest` | `paths:`-glob matching; nested `CLAUDE.md` discovery |
| `skillstest` | `Index` returns names + one-line descriptions only (no skill bodies); returned tokens ≤ the passed budget; deterministic order |
| `mcptest` | JSON-RPC 2.0 framing; `initialize`/`tools/list`/`tools/call`/`ping`; every response schema-valid; `_meta.qompack.ephemeral` present when configured |
| `evaltest` | deterministic replay (same seed → identical `Run`); `Belady` keep-set is optimal on a 6-item brute-forceable instance |
| `ipctest` | framing round-trip; 1 MiB line limit; ACK byte `\x06`, NAK `\x15` |
| `contracttest` | a not-yet-implemented assertion yields `OK:true, SevInfo`; a `SevCritical` failure drives `ModeDegradedPassive`; two clean runs restore |
| `observertest` | `Tombstone` renders the §8.1 form; `ExtractSignals` detects todo/test/git |

### 16. Golden contract fixtures (`testdata/golden/contracts/`, Rule W-2)

`devtool gen-contract-fixtures` maintains one directory per package with a `MANIFEST.json`:

```json
{ "package": "store", "owner": "SP-06",
  "fixtures": [
    { "name": "tool_use_line", "kind": "format", "state": "frozen",
      "input": "input/tool_use_record.json", "want": "want/tool_use_line.jsonl" },
    { "name": "dedup_second_put", "kind": "behaviour", "state": "record-by-owner",
      "input": "input/two_reads.txt", "want": "" }
  ] }
```

Two fixture kinds, and the distinction is the honest part of this design:

- **`format`** fixtures are authorable *now*, because §5 and §8.5 specify the byte layout, not an algorithm. SP-01 hand-authors and freezes them: one `index/tool_use.jsonl` line, one `index/roots.jsonl` line, one `index/segments.jsonl` line, one `dag/deps.jsonl` node line and edge line, one `records/eliminations.jsonl` record, one `pins/invariants.jsonl` add and one tombstone, one `checkpoints/MANIFEST.jsonl` entry, one complete `checkpoints/0001.json` conforming to §8.5 (with every tier populated and **no code snippets**), the Appendix C config document, one `hookio.Event` per hook type with its parsed form, and the `contract` result set. Consumers in wave 1+ test against these immediately.
- **`behaviour`** fixtures name their input and are marked `record-by-owner`. The owning subplan runs `devtool gen-contract-fixtures --record <pkg>`, which refuses to record while the implementation is a stub, writes the `want/` file, and flips `state` to `frozen`. From that point they are byte-frozen for every consumer, and the wave's verification checkpoint re-runs them (Rule W-2: a fixture the real implementation cannot reproduce is a verification failure, not a fixture bug).

`testutil.ContractFixture(t, pkg, name) (input []byte, want []byte, frozen bool)` is the accessor every consumer uses; when `frozen` is false the consumer test `t.Skip`s with `contract fixture not yet recorded (Rule W-2)`.

### 17. `internal/testutil` and `test/e2e`

`testutil/project.go` — `NewProject` creates `t.TempDir()`, `git init`s it only when `WithGit()` is passed (so `paths.Resolve`'s `.git` walk is exercised both ways), calls `paths.EnsureLayout`, loads config with `QOMPACK_PROJECT_ROOT` set to the temp root, installs a `FakeClock` at `2026-01-01T00:00:00Z`, and registers `t.Cleanup` that closes the logger. `ProjectOpt` values: `WithGit()`, `WithConfig(json string)` (writes `.qompack/config.json`), `WithEnv(k, v string)`, `WithFiles(map[string]string)`, `WithClock(t0 time.Time)`.

`(*Project).RunHook` has two modes selected by `QOMPACK_E2E_BINARY`: in-process (default — calls `cli.Dispatch` with a pipe stdin, fastest) and real-binary (used by `test/e2e`, spawns `bin/qompack`). Both return the parsed `hookio.Output`. §6.2 mandates both modes ("real binary or in-proc"), and the real-binary mode needs `os/exec`, which §8's `security` job otherwise permits only in `internal/daemon`, `internal/cli` and `tools/`. **Decision: the allowlist is extended with `internal/testutil`**, with the exec call isolated in `internal/testutil/spawn.go` and an inline comment naming §6.2. This is safe because `testutil` is a composition root that nothing outside `_test.go` files imports — `devtool lint`'s `bindeps` sub-check proves `os/exec` never reaches `cmd/qompack` through it. The alternative (moving the spawn into `test/e2e`) would contradict §6.2's normative comment, so it is rejected rather than silently done.

`(*Project).AssertAppendOnly` performs, against the live project, exactly the §3.3 conformance list: truncating write to `checkpoints/0001.json`, in-place rewrite of `pins/invariants.jsonl`, `WriteAtomic` onto `sketches/tried.bloom`, and an out-of-order checkpoint seq write (writing `0001.json` twice) — asserting all four fail with `core.ErrAppendOnly` or `os.ErrExist`.

`testutil/winpath.go` — `WindowsHostileFiles()` returns the fixture set §6.2 requires: a path with spaces (`src/my folder/a b.ts`), a path over 260 characters (twelve nested 24-char directories plus a 40-char filename), a case-colliding pair (`src/Foo.ts` and `src/foo.ts` — created as one file on case-insensitive filesystems, which the test asserts rather than fights), a CRLF file, and a file `chmod`'d `0444`.

`testutil/golden.go` — `Golden(t, name, got)` compares against `testdata/golden/<pkg>/<name>` and rewrites it when `-update` is passed (`var update = flag.Bool("update", false, …)` in the package). Comparison normalizes CRLF→LF before diffing so a Windows checkout does not produce false failures; the file itself is written with LF.

`test/e2e/harness.go` — `Build(t)` runs `go build -o <tempdir>/qompack[.exe] ./cmd/qompack` once per test binary (guarded by `sync.Once`), returns the path; `Run(t, bin string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int)`.

`test/e2e/hooks_test.go` — the wave-0 e2e case: build the real binary, run all six hook subcommands against a real temp project with representative payloads, assert exit code 0 for every one, assert stdout parses as `hookio.Output`, and assert that `.qompack/logs/hooks-*.jsonl` gained exactly six lines.

### 18. `test/guards` — the closing-note and contract CI guards

`test/guards/buildorder_test.go`, four tests named for the closing-note items they encode:

```go
// Closing note 1: "Phase 0. Without measurement, everything else is opinion."
func TestGuard_Phase0BeforeStore(t *testing.T) {
    if isStub(evalProbe) && !isStub(storeProbe) {
        t.Fatal("store (Phase 1) is implemented while eval (Phase 0) is still a stub — " +
            "closing note 1: without measurement, everything else is opinion")
    }
}

// Closing note 2: store + negative knowledge take priority over everything downstream.
func TestGuard_StoreAndNegknowBeforeCheckpoint(t *testing.T) {
    if !isStub(checkpointProbe) && (isStub(storeProbe) || isStub(negknowProbe)) {
        t.Fatal("checkpoint (Phase 3) implemented before store and negknow (Phases 1–2)")
    }
}

// Closing note 3: no submodular selection before p-selection.
func TestGuard_SubmodularInertWithoutPSelection(t *testing.T) {
    require.False(t, config.Defaults().Runtime.Selection.SubmodularEnabled,
        "runtime.selection.submodularEnabled must default to false until SP-12 merges")
    require.False(t, config.Defaults().Selection.Submodular.Enabled,
        "the derived SelectionCfg.Submodular.Enabled must follow the runtime key")
    // A selector may never be constructed over a block that sits before p, in any build.
    // errors.Is pins the REASON, so this cannot pass because of the ship-order check instead.
    _, err := analyzer.NewSelector(100, []analyzer.Block{{Pos: 99}}, dag.Slice{}, nil, 0.4, true)
    require.ErrorIs(t, err, core.ErrBudget,
        "NewSelector must refuse a block with Pos < p (§13 invariant 4)")
}
func TestGuard_SelectorRefusesWithoutPSelection(t *testing.T) {
    if scheduler.PSelectionAvailable() { t.Skip("scheduler has landed; SP-12 owns this assertion") }
    _, err := analyzer.NewSelector(100, []analyzer.Block{{Pos: 200}}, dag.Slice{}, nil, 0.4, true)
    require.ErrorIs(t, err, core.ErrNotImplemented,
        "NewSelector must refuse while PSelectionAvailable() is false (closing note 3)")
}

// Closing note 4: the incremental-span instruction is on by default.
func TestGuard_O1FlagDefaults(t *testing.T) {
    d := config.Defaults()
    require.True(t, d.Checkpoint.IncrementalSpanInstruction)
    require.True(t, d.Checkpoint.Frontier.AdvanceOnSegmentClose)
    require.Equal(t, 20000, d.Checkpoint.Frontier.MaxResidualTokens)
}
```

`isStub(probe)` calls the package's canonical operation and reports `core.IsNotImplemented`. The probes live in `test/guards/probes.go`. `test/guards` and `test/e2e` import everything, so both are added to `compositionRoots` in `tools/devtool/importrules.go` alongside `daemon`, `cli`, `commands`, `testutil` and `cmd/qompack`; nothing may import them.

`test/guards/contract_test.go` — the §12.1 assertion:

```go
func TestGuard_FreshBuildReportsModeFull(t *testing.T) {
    p := testutil.NewProject(t)
    m := contract.NewMonitor(p.Log, obs.New(p.Clock), filepath.Join(paths.Of(p.Root).State, "contract.json"))
    for _, a := range contract.StandardAssertions() { require.NoError(t, m.Register(a)) }
    res, mode := m.RunAll(context.Background(), contract.Env{ProjectRoot: p.Root, Cfg: p.Cfg, Log: p.Log, Clock: p.Clock})
    require.Equal(t, contract.ModeFull, mode, "a freshly built develop must report ModeFull (§12.1)")
    for _, r := range res {
        if r.Observed == "not-yet-implemented" {
            require.True(t, r.OK); require.Equal(t, contract.SevInfo, r.Severity)
        }
    }
}
```

`test/guards/writeset_test.go` — §13 invariant 7: run all six hooks in-process against a temp project inside a temp `HOME`, snapshot the filesystem before and after, and assert every created/modified path is under `<root>/.qompack/` or `<home>/.qompack/`.

### 19. GitHub Actions

`.github/workflows/ci.yml` — jobs exactly as §8, on every push to every branch and every PR into `develop`/`main`.

**Every job begins with the same two steps**, written out in full in each job (GitHub Actions has no step-level reuse and YAML anchors are not supported in workflow files):

```yaml
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x', cache: true }
```

Referred to below as `«prelude»`. Every `steps:` list is a real list of real steps — there is no prose in the YAML.

```yaml
name: ci
on:
  push:
    branches: ['**']
  pull_request:
    branches: [develop, main]
env:
  GOTOOLCHAIN: local
  CGO_ENABLED: '0'
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x', cache: true }
      - run: go run ./tools/devtool fmt-check
      - run: go run ./tools/devtool lint
      - run: go vet ./...
      - run: go build ./...
      - name: reject attribution trailers
        run: |
          base="${{ github.event.pull_request.base.sha }}"
          if [ -z "$base" ]; then base="$(git rev-parse HEAD~1 2>/dev/null || git rev-parse HEAD)"; fi
          if git log --format=%B "$base..HEAD" | grep -Eiq 'Co-Authored-By|Signed-off-by|Generated with|🤖'; then
            echo "attribution trailer found in commit range (§10)"; exit 1
          fi
      - name: conventional commits
        run: |
          base="${{ github.event.pull_request.base.sha }}"
          if [ -z "$base" ]; then base="$(git rev-parse HEAD~1 2>/dev/null || git rev-parse HEAD)"; fi
          git log --format=%s "$base..HEAD" | while read -r s; do
            echo "$s" | grep -Eq '^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.-]+\))?: .{1,64}$' \
              || { echo "bad subject: $s"; exit 1; }
          done
  test:
    strategy: { fail-fast: false, matrix: { os: [ubuntu-latest, macos-latest, windows-latest], go: ['1.26.x'] } }
    runs-on: ${{ matrix.os }}
    steps:
      - «prelude»
      - if: runner.os != 'Windows'
        run: go test -race ./...
      - if: runner.os == 'Windows'
        run: go test -count=2 ./...
  cover:
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run ./tools/devtool cover
      - uses: actions/upload-artifact@v4
        with: { name: coverage, path: coverage.out }
  crossbuild:
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run ./tools/devtool build-all
  bench-gate:
    continue-on-error: true      # NOT required until the end of wave 1 (§8); SP-05 flips this
    strategy: { fail-fast: false, matrix: { os: [ubuntu-latest, macos-latest, windows-latest] } }
    runs-on: ${{ matrix.os }}
    steps:
      - «prelude»
      - run: go run ./tools/devtool bench-hotpath --iterations 2000 --json bench.json
  replay-gate:
    continue-on-error: true      # NOT required until the end of wave 1 (§8); SP-02 flips this
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop
  plugin-validate:
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run ./tools/devtool plugin-validate
      - run: git diff --exit-code -- plugin/
  security:
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...
      - run: go run ./tools/devtool lint --only=importgraph,testdeps,bindeps
      - name: import allowlist (§8, D10)
        run: |
          go list -deps -f '{{.ImportPath}} {{join .Imports " "}}' ./... > imports.txt
          ! grep -E '^github.com/qompack/qompack/(internal|cmd)/' imports.txt | grep -Ev '^github.com/qompack/qompack/internal/ipc ' | grep -E '\b(net/http|net/url|crypto/tls)\b'
          # os/exec allowlist: daemon (detached self-spawn), cli, and testutil (§6.2 real-binary RunHook)
          ! grep -E '^github.com/qompack/qompack/(internal|cmd)/' imports.txt | grep -Ev '^github.com/qompack/qompack/internal/(daemon|cli|testutil) ' | grep -E '\bos/exec\b'
  docs:
    runs-on: ubuntu-latest
    steps:
      - «prelude»
      - run: go run ./tools/devtool gen-config-docs --check
```

`«prelude»` is expanded literally in the committed file; it appears here only so the job list stays readable. `actions/upload-artifact@v4` is the only action beyond checkout/setup-go, and it is used solely for the coverage profile.

`bench-gate` and `replay-gate` are wired now and marked `continue-on-error: true` with an inline comment naming the subplan that removes the flag — that is §8's "wired but not yet required" made explicit rather than left implicit.

`.github/workflows/nightly.yml` — cron `0 3 * * *`: `go test -fuzz` 10 min per target over `chunk.Split`, every canonicalizer, `sketch.UnmarshalBinary`, `hookio.ReadEvent`, `ipc` framing, `config.Load`, `checkpoint` JSON (fuzz targets are declared by SP-01 as `FuzzX` functions that call the stub and assert no panic; owners add corpora); Windows `-race`; `bench-hotpath --iterations 5000`; live-mode replay when `QOMPACK_SESSIONS_DIR` is set.

`.github/workflows/release.yml` — tag-triggered (`v*`), runs `goreleaser release --clean` with `GITHUB_TOKEN`; SP-17 fills in bundle assembly and attestation. `.goreleaser.yaml` ships now with the six §2.6 targets, `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X github.com/qompack/qompack/internal/core.Version={{.Version}}"`, and `checksum.name_template: checksums.txt`.

`.github/ISSUE_TEMPLATE/bug.yml` (version, OS, `qompack config print --provenance` output, `qompack doctor` output, reproduction) and `upstream-tracker.yml` (the §12 upstream-issues list: agent-initiated compaction, PTL retry ordering, path-scoped rule re-injection, skill index re-injection, drop report surface). `.github/CODEOWNERS`: `* @qompack` plus `/plans/ @qompack`.

### 20. The commit-msg hook

`devtool install-hooks` writes `.git/hooks/commit-msg` (mode 0755):

```sh
#!/bin/sh
exec go run ./tools/devtool check-commit-msg "$1"
```

`check-commit-msg` enforces: subject matches `^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.-]+\))?: .{1,64}$` with no trailing period; body lines ≤ 100 chars; **rejects** any line matching `(?i)co-authored-by|signed-off-by|generated with` or containing `🤖`; requires a `Refs:` footer line on `feat` and `fix` commits. Exit 1 with the offending line on any failure.

---

## Test plan (TDD)

Tests are written before the code in each commit. Every name below is the literal Go test name.

### `internal/core`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestHashBytes_DomainSeparation` | — | `HashBytes("a", []byte("bc"))` vs `HashBytes("ab", []byte("c"))` | different hashes; asserts the `0x00` separator matters |
| `TestHashBytes_KnownVector` | — | `HashBytes("qompack.root.v1", nil)` | equals `sha256("qompack.root.v1\x00")`, computed inline in the test |
| `TestHash_StringShortParse_RoundTrip` | — | 100 random hashes | `ParseHash(h.String()) == h`; `len(h.Short()) == 12`; `h.String()` has the `sha256:` prefix and 64 lowercase hex chars |
| `TestParseHash_Rejects` | — | `""`, `"sha256:"`, 63 hex, 65 hex, `"sha256:zz…"` | `error`, wrapping `ErrNotFound` |
| `TestHash_JSONRoundTrip` | — | struct with a `Hash` field | marshals to `"sha256:…"`, unmarshals identically |
| `TestNewDecisionID_Format` | — | `NewDecisionID([]byte("x"))` | matches `^dec_[0-9a-f]{12}$` |
| `TestSentinels_AreDistinct` | — | the seven sentinels | pairwise `!errors.Is`; `IsNotImplemented(fmt.Errorf("w: %w", ErrNotImplemented))` is true |

### `internal/paths`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestResolve_EnvWins` | temp dir with `.git`, `QOMPACK_PROJECT_ROOT=/other` | — | returns `/other` absolute |
| `TestResolve_WalksToGitDir` | `<tmp>/.git/`, cwd `<tmp>/a/b/c` | — | returns `<tmp>` |
| `TestResolve_GitFileWorktree` | `<tmp>/.git` as a **file** containing `gitdir: …` | cwd `<tmp>/a` | returns `<tmp>` |
| `TestResolve_FallsBackToCWD` | no `.git` anywhere | cwd `<tmp>/a` | returns `<tmp>/a` |
| `TestNorm_RejectsEscape` | root `<tmp>` | `../outside`, `<tmp>/../x` | error containing "escapes project root" |
| `TestNorm_ForwardSlashRelative` | root `<tmp>` | `<tmp>\a\b.ts` (Windows-style) | `a/b.ts` |
| `TestKeyFold` | — | `("Src/Foo.TS", true)`, `("Src/Foo.TS", false)` | `src/foo.ts`, `Src/Foo.TS` |
| `TestEnsureLayout_CreatesAllDirsAndSelfIgnore` | fresh temp root | — | all 17 directories exist — the 16 `MkdirAll` targets listed in `paths/layout.go` plus `eval/` itself, which `eval/replay` and `eval/opt` imply; `.qompack/.gitignore` content is exactly `*\n` |
| `TestWriteAtomic_ReplacesAndSyncs` | existing file with `old` | `WriteAtomic(p, []byte("new"), 0600)` | content `new`; no `wa-*` files left in `tmp/` |
| `TestWriteAtomic_RefusesProtected` | layout | `WriteAtomic(<checkpoints>/0001.json, …)` | `ErrAppendOnly` |
| **`TestAppendOnlyGuard`** | full layout with `checkpoints/0001.json`, `pins/invariants.jsonl`, `sketches/tried.bloom` present | (a) `OpenFile(cp, O_WRONLY\|O_TRUNC, 0600)` (b) `OpenFile(pins, O_WRONLY, 0600)` (c) `WriteAtomic(bloom, …)` (d) `CreateNew(cp, …)` a second time (e) `AppendOnly("x.json")` | (a)(b)(c) `ErrAppendOnly`; (d) `os.ErrExist`; (e) `ErrAppendOnly` for the wrong extension. **All five must fail.** |
| `TestCreateNew_SetsReadOnly` | layout | `CreateNew(<checkpoints>/0002.json, b)` | file exists, mode has no write bit; a subsequent `os.WriteFile` fails |
| `TestAppendJSONL_OneLinePerRecord` | layout | three records, one containing `\n` in a string field | file has exactly 3 lines; the embedded newline is JSON-escaped, not raw |
| `TestReplaceBloom_KeepsOneBackup` | existing `tried.bloom` | `ReplaceBloom(l, b, 1)` then `(l, b, 2)` | `tried.bloom` is the newest; exactly one `.bak` remains (`tried.bloom.2.bak`) |
| `TestLongPath_Over260` | `testutil.WindowsHostileFiles()` | write + read a 300-char path | succeeds on all platforms |
| `TestManifest_AppendAndRead` | layout | 3 `ManifestEntry` appends | `ReadManifest` returns them in order with exact seq/sha/bytes |

Property test `TestNorm_Property` (rapid): for any generated relative path of safe segments, `Norm(root, Norm(root, p))` is stable and never contains `\` or `..`.

### `internal/config`

| Test | Setup | Input | Expected |
|---|---|---|---|
| **`TestDefaults_MatchesAppendixCVerbatim`** | — | `json.Marshal(Defaults())` with the `runtime` key removed | deep-equals the parsed Appendix C document embedded as `testdata/golden/config/appendix-c.jsonc`. **This is the §11.1 golden test.** |
| `TestDefaults_RuntimeNamespace` | — | `Defaults().Runtime` | matches the §11.5 document plus the three SP-01 additions, field by field |
| `TestStripJSONC` | — | line comments, block comments, a `//` inside a string, trailing commas in objects and arrays | comments blanked (byte offsets preserved), trailing commas removed, string content untouched |
| `TestLoad_PrecedenceFiveLayers` | user file sets `scheduler.softFloorPct=0.5`; project file sets it to `0.6`; env sets `0.7`; flag sets `0.8` | — | effective `0.8`, `Provenance["scheduler.softFloorPct"].Origin == OriginFlag` |
| `TestLoad_DeepMergePerLeaf` | project file `{"scheduler":{"softFloorPct":0.6}}` | — | `SoftFloorPct == 0.6` **and** `Cache.ReadMultiplier == 0.1` and `Idle.DetectAfterSeconds == 120` (all other scheduler defaults intact) |
| `TestLoad_EnvKeyMapping` | `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER=0.08` | — | `0.08`, origin `OriginEnv` |
| `TestLoad_NullMeansMeasure` | project file `{"scheduler":{"youngDaly":{"measuredDeltaSeconds":null}}}` | — | `MeasuredDeltaSeconds == nil`, **not** a pointer to 0 |
| `TestLoad_UnknownKeyWarnsNeverErrors` | project file `{"store":{"futureKey":1},"newSection":{}}` | — | two `Warning`s, `error == nil`, config otherwise default |
| `TestLoad_InvalidLeafFallsBackNotCrash` | project file `{"scheduler":{"softFloorPct":1.5},"store":{"chunk":{"min":9999}}}` | — | `SoftFloorPct == 0.55`, chunk min `1024`; two `Warning`s naming those two keys; `ViolationsFromWarnings` decodes both; `error == nil`; **no file written and no logger touched** (that is `cli`'s half) |
| `TestCLI_ConfigViolationsAreLoudAndPersisted` (in `internal/cli`) | same project file, via `cli.LoadConfigAndReport` | — | two `Loud` messages; `.qompack/state/config-violations.json` contains both `Violation`s |
| `TestLoad_UnparseableFileWarns` | project file `{` | — | one `Warning`, defaults intact, no error |
| `TestLoad_ProvenanceLocationHasLine` | project file with `softFloorPct` on line 4 | — | `Location` ends `config.json:4` |
| `TestValidate_EveryRule` | table-driven, **one case per line of the `config/validate.go` rule list above** (that list is the source of truth for the count; a rule with two bounds gets two cases, one per bound), each mutating exactly one leaf out of range | — | exactly one `Violation` with the expected `Key` |
| `TestValidate_RuleTableIsComplete` | — | reflect over every leaf in `Config` | every leaf either appears in the `validate.go` rule list or is in an explicit `unconstrained` allowlist — so a new key can never be added without a validation decision |
| `TestValidate_TiersPartition` | tiers with `pointers` in both `late` and `first`; tiers missing `narrative` | — | violations for non-disjoint and non-covering |
| `TestValidate_TelemetryMustBeFalse` | `runtime.telemetry.enabled = true` | — | one `Violation` on `runtime.telemetry.enabled` |
| `TestSubmodularEnabled_DerivedFromRuntime` | `runtime.selection.submodularEnabled = true` | — | `Selection.Submodular.Enabled == true`; and `json.Marshal(cfg.Selection)` contains no `enabled` key |
| `TestJSONSchema_Golden` | — | `Defaults().JSONSchema()` | byte-equals `testdata/golden/config/schema.json`; keys sorted; every leaf has `default`, `description`, `x-qompack-section` |
| `TestGet_DottedLookup` | — | `Get("scheduler.cache.writeMultiplier")` | `1.25, true`; `Get("nope")` → `nil, false` |
| `FuzzConfigLoad` | — | arbitrary bytes as the project file | never panics; always returns a validated config |

### `internal/logging` / `internal/obs`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestLogger_LevelFiltering` | `New(dir, Warn)` | Debug/Info/Warn/Error | file contains only the last two |
| `TestLoud_ThreeDestinations` | `New(dir, Info)` + `AttachLoudObserver` with a counting closure | `Loud("contract broken", "id", "x")` | day log has it; `LOUD.log` has it; `LastLoud()` returns it; the closure fired exactly once |
| `TestLogger_Rotation` | `MaxFileMB=1, MaxFiles=2` | 3 MB of lines | at most 2 rotated files plus the current one |
| `TestHistogram_BucketMonotone` (rapid) | — | random durations | `bucketFor` is non-decreasing in the input; `bucketUpper(bucketFor(u)) >= u` |
| `TestHistogram_PercentileConservative` | 10 000 observations of exactly 10 ms | — | `P99 >= 10ms` and `P99 <= 10ms * 1.0905` |
| `TestHistogram_MaxExact` | observations including one 1.234567 s | — | `Max == 1234567µs` exactly |
| `TestBudgets_AllSixPresentAndConfigDriven` | — | `Budgets()` | six entries B-A..B-F; B-A limit `15ms` from `runtime.hotPath.budgetMs`; B-D `Gated == false`; B-C `Gated == false`; changing config changes every limit |
| `TestCheckBudgets_CountsConsecutiveWindows` | registry with B-A over budget three times | — | one `BudgetBreach` with `Windows == 3` |
| `BenchmarkHistogram_Observe` | — | — | reports ns/op; **budget: `Observe` must be under 100 ns/op**, because it is on the B-B path |

### `internal/tokens`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestClassify_Table` | — | 14 cases: `.png`, `.pdf`, `%PDF` magic, `.json`, valid JSON without extension, `diff --git` body, `.md`, Go source, NUL-containing bytes, `.ts`, Grep output, empty | the expected `Class` per case |
| `TestEstimate_ProseVsCode` | — | 4000 bytes each | prose `1000`, code `1112` (`ceil(4000/3.6)`) |
| `TestEstimate_ImageFromDimensions` | 100×100 PNG encoded in-test | — | `ceil(10000/750) == 14` |
| `TestEstimate_ImageCappedAt1600` | 4000×4000 PNG header | — | `1600` |
| `TestEstimate_PDFPageCount` | synthetic PDF bytes with three `/Type /Page` and one `/Type /Pages` | — | `3 * 1800` |
| `TestCalibrate_ClampsAndPersists` | fake home | `Calibrate(2000, 1000)` ×20 | `Factor()` ≤ `1.6`; `~/.qompack/calibration.json` written; a fresh `New` reads it back |
| `TestEstimateRoot_SumsChunks` | — | 3 `ChunkRef` of len 1000 (`ClassProse`) | `750` |
| `TestEstimate_MonotoneInLength` (rapid) | — | random byte slices | longer input never yields fewer tokens for the same class |

### `internal/hookio`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestReadEvent_AllSevenHookPayloads` | fixtures in `testdata/golden/contracts/hookio/input/` | `PostToolUse`, `UserPromptSubmit`, `SessionStart(startup\|resume\|compact\|clear)`, `PreCompact`, `Stop`, `SubagentStop`, `SessionEnd` | every field parsed as the golden `want/` file says |
| `TestReadEvent_UnknownFieldsPreserved` | — | payload with `"future_field": {"a":1}` | `Extra["future_field"]` holds the raw JSON; no error |
| `TestReadEvent_MissingFieldsNeverPanic` | — | `{}` | zero-valued `Event`, nil error |
| `TestReadEvent_NullFields` | — | `{"tool_input":null,"prompt":null}` | zero values, nil error |
| `TestReadEvent_LimitExceeded` | limit 16 | 100-byte payload | `core.ErrBudget`, raw bytes returned |
| `TestWriteOutput_EmptyIsMinimal` | — | `Empty()` | exactly `{}\n` |
| `TestWriteOutput_NoHTMLEscaping` | — | `SystemMessage: "a<b&c"` | output contains `a<b&c` literally |
| `TestSessionStartOutput_Shape` | — | `SessionStartOutput("ctx")` | `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"ctx"}}` |
| `FuzzReadEvent` | — | arbitrary bytes | never panics |

### `internal/cli` and the no-op hooks

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestDispatch_HookAlwaysExitsZero` | table of the six hook subcommands × 5 fault injections (unreadable stdin, malformed JSON, unresolvable project root, read-only `.qompack`, panicking inner function via a test hook) | — | exit code `0` in all 30 combinations; stdout always parses as `hookio.Output` |
| `TestDispatch_NonHookErrorExitsOne` | — | `qompack status` (stub) | exit 1, stderr names the subcommand |
| `TestDispatch_UnknownCommandExitsTwo` | — | `qompack wat` | exit 2, usage on stderr |
| `TestDispatch_PanicRecovered` | injected panicking command marked `Hook: true` | — | exit 0; `Loud` recorded with a stack |
| `TestHooks_WriteHookLog` | temp project | run all six | `.qompack/logs/hooks-*.jsonl` has 6 lines with the right `hook` values and no payload text |
| `TestConfigPrint_Provenance` | project file overriding one key | `config print --provenance` | output contains `scheduler.softFloorPct` annotated with `project` and the file path |
| `TestConfigSchema_Emits` | — | `config schema` | parses as JSON; equals `Defaults().JSONSchema()` |
| `TestSetFlagParsing` | — | `--set scheduler.cache.readMultiplier=0.08 --set eval.minSessions=5` | both land in `Env.Flags` and take effect |

### `internal/pluginmanifest`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestManifest_GoldenBytes` | — | `Default("0.1.0").Files()` | each of the 10 files byte-equals its golden in `testdata/golden/plugin/` |
| `TestManifest_CoversAllSixHooks` | — | `Hooks` | keys are exactly `PostToolUse UserPromptSubmit SessionStart PreCompact Stop SubagentStop SessionEnd`; timeouts `5,5,15,20,5,10,20`; `PostToolUse` has `matcher: "*"` |
| `TestManifest_SevenCommands` | — | `Commands` | names exactly `status recall pin checkpoint why dropped eval` |
| `TestManifest_CommandsShellOutToBinary` | — | each `.md` | frontmatter has `description`, `argument-hint`, `allowed-tools`; body contains `` !`qompack <name> $ARGUMENTS` `` |
| `TestValidate_DetectsDrift` | write bundle, then mutate one byte of `hooks.json` | `Validate` | exactly one `Diff` naming that path |
| `TestMCPJSON_UsesPluginRoot` | — | `.mcp.json` | command is `${CLAUDE_PLUGIN_ROOT}/bin/qompack`, args `["mcp"]` |

### Stubs and conformance suites

| Test | Expected |
|---|---|
| `TestAllStubsReturnNotImplemented` (in `test/guards`) | reflectively walks a registry of every stub constructor; every method invoked with zero arguments returns `core.ErrNotImplemented` or a documented zero value; a table in `probes.go` lists the 23 packages and asserts none is missing |
| `Test<Pkg>Suite_ShapePassesAgainstStub` (one per suite, 22 total; `storetest` contributes two — `RunStoreSuite` and `RunSegmentLogSuite`) | the suite's shape block passes and the behaviour block is skipped with `behaviour: implementation is a stub (Rule W-1)` |
| `TestRootHash_Formula` | `RootHash([{Hash:h1},{Hash:h2}])` equals `HashBytes("qompack.root.v1", h1||h2)` |
| `TestYoungDaly_Formula` | `YoungDaly(30, 600) == math.Sqrt(2*30*600)`; `YoungDaly(0, 600) == 0`; `YoungDaly(-1, 5) == 0` |
| `TestSkiRental_ComputedNotLiteral` | `SkiRentalShouldWrite(13, 0.1, 1.25) == true`; `SkiRentalShouldWrite(12, 0.1, 1.25) == false`; `SkiRentalShouldWrite(1, 0, 1.25) == false`. Source assertion: `grep -R "12\.5" internal/ --include=*.go` outside tests returns nothing |
| `TestDescriptorKey_Stable` | a fixed `Descriptor` yields a fixed 32-byte key, frozen as a golden — later subplans must not change bloom keying without a golden update |
| `TestStripInjections` | text with one full span, one unclosed span, and one bare close tag → the specified outputs (span removed; unclosed span removes to end; bare close tag removed) |
| `TestTombstone_MatchesDesignExample` | `ToolUseRecord{Root: <hash starting a3f2>, Bytes: 2457, Tool: "FileRead", Path: "src/auth.ts"}` → `[cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]` with the real 12-hex short hash in place of `a3f2…` |
| `TestStoreCompress_RoundTrip` (rapid) | `Decode(Encode(b)) == b` for random slices up to 1 MiB; `Decode` of a 100 MiB decompressed bomb returns an error, not an allocation |
| `TestIPCResolve_SunPathFallback` | a project root producing a >100-byte socket path → falls back to `<tmp>/qp-<hash8>.sock`; on Windows → `\\.\pipe\qompack.<hash12>` |

### `internal/testutil` and `test/e2e`

| Test | Expected |
|---|---|
| `TestProject_LayoutAndCleanup` | `NewProject` produces a full layout; `QOMPACK_PROJECT_ROOT` set; cleanup removes nothing outside `t.TempDir()` |
| `TestProject_AssertAppendOnly` | passes against a correct layout; a deliberately-weakened `paths.OpenFile` (via a test-only build-tag shim) makes it fail — proving the assertion has teeth |
| `TestFakeClock_Deterministic` | `Advance` moves `Now`; `Since` is exact; no wall-clock reads |
| `TestGolden_UpdateFlag` | with `-update`, rewrites; without, compares; CRLF in the file does not cause a false failure |
| `TestWindowsHostileFiles_AllCreatable` | all five fixture classes create successfully or, for the case-collision pair on a case-insensitive FS, collapse to one file and the test asserts that outcome explicitly |
| `TestE2E_AllSixHooksExitZero` (in `test/e2e`) | builds the real binary; runs all six hooks; every exit code 0; every stdout parses; hook log has six lines |
| `TestE2E_ConfigPrintFromRealBinary` | real binary `config print --json` parses and deep-equals `Defaults()` for a project with no config file |

### Guards

`TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull`, `TestGuard_WriteSetConfinedToQompack`, `TestGuard_NoNetworkImports` (parses `go list` output; asserts `net/http`, `net/url`, `crypto/tls` absent from every non-test package and `net` present only in `internal/ipc`).

### Toolchain tests

`TestNoMagic_Analyzer` (`analysistest.Run` over `tools/lint/nomagic/testdata`), `TestImportGraph_RejectsViolation` (a synthetic package list containing `store → negknow` is rejected with a message naming §3.2), `TestImportGraph_AcceptsRealRepo` (runs against the actual repo and must pass), `TestTestDeps_RejectsProductionTestify`, `TestCheckCommitMsg` (table: valid subjects, over-length subject, trailing period, missing type, `Co-Authored-By` in body, `🤖` in body, missing `Refs:` on a `feat` — expected accept/reject per case).

### Benchmarks with budgets

| Benchmark | Budget |
|---|---|
| `BenchmarkHistogram_Observe` | < 100 ns/op (feeds B-B) |
| `BenchmarkConfigLoad_ColdNoFiles` | < 2 ms/op — config load happens once per hook process in later waves and must not eat B-A's 15 ms |
| `BenchmarkHookNoop_InProcess` | < 3 ms/op for `observe tool` end to end in-process; this is the wave-0 headroom measurement against **B-A p99 < 15 ms** (§11.3, §8.1). Recorded into `testdata/bench-baseline.txt` as the first baseline entry. |
| `BenchmarkPathsWriteAtomic_4KB` | < 2 ms/op on the CI runners |

### Fixtures needed

`testdata/golden/config/appendix-c.jsonc` (Appendix C verbatim), `testdata/golden/config/schema.json`, `testdata/golden/plugin/**` (10 files), `testdata/golden/contracts/**` (per §16), `testdata/corpora/hookio/**` (fuzz seeds: the seven hook payloads plus five malformed variants), `testdata/corpora/config/**` (five JSONC seeds), `testdata/sessions/recorded/.gitkeep`, `tools/lint/nomagic/testdata/src/a/a.go`.

---

## Commit plan

All work after commit 1 happens on `feat/sp01-foundation-toolchain-and-contracts`, cut from `develop`. Exactly 8 commits.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — repository initialization (on `main`, then branch)

```
chore: initial commit — design document and build plans
```

- [ ] `git init` with `git -c init.defaultBranch=main init` at the repo root
- [ ] Write `.gitignore` (§1 of the implementation spec, verbatim) and `LICENSE` (MIT)
- [ ] `git add Qompack.md plans .gitignore LICENSE` — **nothing else may be in this commit**
- [ ] `git commit` with the message above (no body, no trailers)
- [ ] `git branch develop` and `git switch develop`
- [ ] `git switch -c feat/sp01-foundation-toolchain-and-contracts`
- [ ] Verify: `git log --oneline --all` shows one commit on two branches; `git show --stat HEAD` lists only `Qompack.md`, `plans/*`, `.gitignore`, `LICENSE`

### Commit 2 — toolchain

```
build(toolchain): go module, lint config, devtool task runner, nomagic pass

Establishes the Go 1.26 module, the closed runtime dependency list of
00-ARCHITECTURE §2.5, and the three in-repo checks that make the architecture's
rules mechanical rather than aspirational: the nomagic literal gate (D11), the
import-graph dependency DAG (§3.2), and the test-only-dependency check.

Refs: SP-01, §11.6, §3.2, §2.5, §2.6
```

Files: `go.mod`, `go.sum`, `tools/pinned/go.mod`, `tools/pinned/go.sum`, `tools/pinned/tools.go`, `.golangci.yml`, `.goreleaser.yaml`, `.gitattributes`, `.editorconfig`, `tools/devtool/*.go`, `tools/lint/nomagic/*.go`, `tools/lint/nomagic/testdata/src/a/a.go`.

- [ ] Write `TestNoMagic_Analyzer`, `TestImportGraph_RejectsViolation`, `TestTestDeps_RejectsProductionTestify`, `TestCheckCommitMsg` **first** — they fail (no analyzer, no rules table)
- [ ] `go mod init github.com/qompack/qompack`; add the six requires above; `go mod tidy`
- [ ] `go mod init` inside `tools/pinned/`, add the four pinned tool modules, resolve the `golang.org/x/perf` pseudo-version with `go get golang.org/x/perf/cmd/benchstat@latest`, commit `tools/pinned/go.sum`
- [ ] Implement `nomagic`, `importgraph`, `testdeps`, `bindeps`, `sleepcheck`, `stubskips`, `check-commit-msg`, `install-hooks`, and the remaining `devtool` tasks
- [ ] `go test ./tools/...` green; `go run ./tools/devtool install-hooks`
- [ ] `go run ./tools/devtool lint` green (trivially — no `internal/` yet)

### Commit 3 — core primitives and the `.qompack` layout

```
feat(core,paths): cross-cutting primitives, .qompack layout, append-only guard

core.Dep and core.ChunkRef live here rather than in store/negknow/tokens so the
store can answer staleness without importing negknow and tokens can size a root
without importing store (§3.2). paths makes the §7.4 append-only invariant
mechanical: checkpoints/, pins/ and sketches/tried.bloom cannot be truncated or
rewritten through any code path in this repository.

Refs: SP-01, §7.4, §4.6, §3.3
```

Files: `internal/core/*.go`, `internal/paths/*.go` and their tests.

- [ ] Write every `internal/core` and `internal/paths` test listed above **first**, including `TestAppendOnlyGuard` — all fail to compile
- [ ] Implement `core` (hash, ids, clock, errors, version), then `paths` (resolve, layout, norm, long, atomic, appendonly, manifest)
- [ ] `go test ./internal/core/... ./internal/paths/...` green, including on a >260-char path
- [ ] `go run ./tools/devtool lint` green

### Commit 4 — configuration system

```
feat(config): Appendix C defaults, five-layer load, validation, provenance, schema

Defaults() reproduces Appendix C byte-for-byte (golden-tested) and adds only the
§11.5 runtime namespace, including the rehydrate and mcp keys Appendix C lacks.
Invalid leaves fall back to their default and are reported loudly rather than
crashing the hook that loaded them: a hook that dies takes observability with it.

Refs: SP-01, Appendix C, §11.1, §11.2, §11.3, §11.5, §11.6
```

Files: `internal/config/*.go`, `testdata/golden/config/appendix-c.jsonc`, `testdata/golden/config/schema.json`, `testdata/corpora/config/**`, `docs/config-reference.md` (generated).

- [ ] Write `TestDefaults_MatchesAppendixCVerbatim` and the full §11.3 rule table **first** — they fail
- [ ] Implement types, `defaults.go`, `jsonc.go`, `load.go`, `validate.go`, `provenance.go`, `schema.go`
- [ ] `go run ./tools/devtool gen-config-docs` writes `docs/config-reference.md`; commit it, then confirm `gen-config-docs --check` exits 0
- [ ] `go test ./internal/config/...` green; `go test -run FuzzConfigLoad -fuzz FuzzConfigLoad -fuzztime 30s ./internal/config` clean
- [ ] `go run ./tools/devtool lint` green — confirms `nomagic` accepts `defaults.go` and would reject the same literals elsewhere

### Commit 5 — logging, metrics, tokens, hook codecs

```
feat(logging,obs,tokens,hookio): Loud channel, log-bucket histograms, estimator

Budget IDs B-A..B-F are data with limits read from config, so §11.3's numbers are
gated rather than asserted. The Loud channel exists because §12 requires that no
degradation is ever silent. hookio absorbs host drift: unknown fields are kept in
Extra and missing fields never panic.

Refs: SP-01, §11.3, §8.1, §12, G10.2
```

Files: `internal/logging/*.go`, `internal/obs/*.go`, `internal/tokens/*.go`, `internal/hookio/*.go`, `testdata/corpora/hookio/**`.

- [ ] Write the logging/obs/tokens/hookio tests and `BenchmarkHistogram_Observe` **first**
- [ ] Implement the four packages
- [ ] `go test ./internal/logging/... ./internal/obs/... ./internal/tokens/... ./internal/hookio/...` green
- [ ] `go test -bench=BenchmarkHistogram_Observe ./internal/obs` under 100 ns/op

### Commit 6 — binary, no-op hooks, plugin bundle

```
feat(cli,pluginmanifest): binary dispatch, no-op hooks, generated plugin bundle

Hook subcommands exit 0 unconditionally, including on panic, malformed stdin and
an unresolvable project root — a non-zero hook exit surfaces noise to the user and
can block the turn. The plugin bundle is generated from one typed source so the
§7.5 manifest, the physical hooks.json and the docs can never drift silently.

Refs: SP-01, §7.5, §7.3, §2.3, §3.4
```

Files: `internal/cli/*.go`, `cmd/qompack/main.go`, `internal/pluginmanifest/*.go`, `plugin/**`, `testdata/golden/plugin/**`.

- [ ] Write `TestDispatch_HookAlwaysExitsZero` (all 30 fault-injection combinations) and the manifest golden tests **first**
- [ ] Implement `cli`, `cmd/qompack`, `pluginmanifest`; generate `plugin/**` with `devtool plugin-validate --write`
- [ ] `go run ./tools/devtool build` produces `bin/qompack`; `bin/qompack version` prints `0.1.0`
- [ ] `go run ./tools/devtool plugin-validate` clean; `git diff --exit-code -- plugin/` clean

### Commit 7 — every §5 interface stub and its conformance suite

```
feat(contracts): ErrNotImplemented stubs and conformance suites for every seam

This is the mechanism that makes waves 1–5 parallel (D9). Every interface in
00-ARCHITECTURE §5 compiles today with the exact normative signature, returns
core.ErrNotImplemented, and ships a <pkg>test suite whose shape assertions run now
and whose behaviour assertions are skipped under Rule W-1 until the owner lands.
Pure functions with closed definitions — RootHash, YoungDaly, SkiRentalShouldWrite,
Descriptor.Key, StripInjections, Tombstone — are implemented, not stubbed, because
their consumers need them before their packages are real.

Refs: SP-01, §5, §5.22, D9
```

Files: `internal/{chunk,canon,symbols,redact,sketch,store,dag,grammar,negknow,analyzer,scheduler,checkpoint,pins,rehydrate,rules,skills,mcp,commands,eval,ipc,daemon,observer,contract}/**` plus each `<pkg>test` subpackage, `plans/OWNERS.tsv`.

- [ ] Write each `<pkg>test` suite **first**, including the behaviour blocks that will stay skipped
- [ ] Write `TestAllStubsReturnNotImplemented`, `TestRootHash_Formula`, `TestYoungDaly_Formula`, `TestSkiRental_ComputedNotLiteral`, `TestDescriptorKey_Stable`, `TestStripInjections`, `TestTombstone_MatchesDesignExample`, `TestStoreCompress_RoundTrip`, `TestIPCResolve_SunPathFallback`
- [ ] Implement the stubs and the pure functions
- [ ] `go build ./...` green; `go test ./internal/...` green, with all 22 conformance suites reporting their behaviour block skipped and every skip carrying the message `behaviour: implementation is a stub (Rule W-1)`
- [ ] `go run ./tools/devtool lint` green — the import-graph check now has real packages to verify and must pass against §3.2

### Commit 8 — test scaffolding, fixtures, CI, guards

```
test(ci): testutil fixture, e2e harness, contract fixtures, pipeline, build guards

The four closing-note priorities become CI guards rather than good intentions: no
Phase-1 store without Phase-0 measurement, no checkpointer without store and
negative knowledge, submodular selection inert until p-selection exists, and the
O1 span instruction on by default. bench-gate and replay-gate are wired and marked
continue-on-error with the subplan that removes the flag named inline.

Refs: SP-01, Closing note, §8, §6.2, §6.4, §12.1
```

Files: `internal/testutil/**`, `test/e2e/**`, `test/guards/**`, `testdata/golden/contracts/**`, `testdata/sessions/recorded/.gitkeep`, `.github/workflows/{ci,nightly,release}.yml`, `.github/ISSUE_TEMPLATE/{bug,upstream-tracker}.yml`, `.github/CODEOWNERS`, `testdata/bench-baseline.txt`, `tools/devtool/genfixtures.go`.

- [ ] Write the guard tests and the e2e tests **first** — `TestGuard_FreshBuildReportsModeFull` and `TestE2E_AllSixHooksExitZero` fail until `testutil` and the harness exist
- [ ] Implement `testutil`, `test/e2e/harness.go`, `test/guards/probes.go`, `devtool gen-contract-fixtures`
- [ ] `go run ./tools/devtool gen-contract-fixtures` writes the frozen format fixtures; commit them
- [ ] `go run ./tools/devtool ci-local` green end to end
- [ ] `go test -race ./...` green on the dev machine; `go test -count=2 ./...` green
- [ ] Push the branch; confirm CI: `verify`, `test` (×3 OS), `cover`, `crossbuild`, `plugin-validate`, `security`, `docs` all green; `bench-gate` and `replay-gate` report their not-present message and pass

---

## Subagent strategy

SP-01 is **heavy** and its packages are mostly independent once `core` exists. Partition as follows. The commit plan stays strictly sequential and is executed only by the main session — subagents produce files, never commits.

**Phase A — main session only (blocking, ~1 hour).** Commit 1 (repo init, branches) and the whole of `internal/core` plus `go.mod`. Everything downstream imports `core`; parallelizing before it exists produces merge conflicts in the one file everyone touches. Also write `plans/OWNERS.tsv` here, because three subagents read it.

**Phase B — six parallel subagents.** Each is given: this subplan file, `00-ARCHITECTURE.md` §3.2/§4/§5, the already-written `internal/core`, and an explicit file list. Each returns *only* the files in its list plus their tests, and a one-paragraph report naming any signature it could not satisfy (which is an escalation to the main session, never a silent deviation).

| Subagent | Owns | Returns |
|---|---|---|
| **S1 — toolchain** | `go.mod` requires, `tools/pinned/**`, `.golangci.yml`, `.goreleaser.yaml`, `.gitattributes`, `.editorconfig`, `tools/devtool/**`, `tools/lint/nomagic/**` | a passing `go test ./tools/...` and a `devtool lint` that runs |
| **S2 — paths** | `internal/paths/**` | `TestAppendOnlyGuard` passing, including the >260-char path case |
| **S3 — config** | `internal/config/**`, `testdata/golden/config/**`, `docs/config-reference.md` | `TestDefaults_MatchesAppendixCVerbatim` passing and the full validation table (one case per rule line) |
| **S4 — observability + codecs** | `internal/logging/**`, `internal/obs/**`, `internal/tokens/**` (+ `tokenstest`), `internal/hookio/**`, `testdata/corpora/hookio/**` | the histogram benchmark result, the `tokenstest` suite, and the seven parsed hook payload goldens |
| **S5 — L1/L2 stubs** | `internal/{chunk,canon,symbols,redact,sketch,store,dag,grammar,negknow}/**` and their `<pkg>test` suites | `go build` green for those nine packages, **nine** suites (`chunktest canontest symbolstest redacttest sketchtest storetest dagtest grammartest negknowtest`), and the pure functions (`RootHash`, `Descriptor.Key`) implemented |
| **S6 — L3–L7 stubs** | `internal/{analyzer,scheduler,checkpoint,pins,rehydrate,rules,skills,mcp,commands,eval,ipc,daemon,observer,contract}/**` and their suites | `go build` green for those fourteen packages, **twelve** suites (`analyzertest schedulertest checkpointtest pinstest rehydratetest rulestest skillstest mcptest evaltest ipctest contracttest observertest` — `commands` and `daemon` are composition roots and have no suite), the `analyzer.NewSelector` constructor guard *implemented* (not stubbed), and the pure functions (`YoungDaly`, `SkiRentalShouldWrite`, `StripInjections`, `StandingInstruction`, `Tombstone`, `FormatWarning`, `Mode.String`) implemented |

Suite arithmetic, so nothing is silently dropped: 1 (S4) + 9 (S5) + 12 (S6) = **22 conformance suites**, matching the list in the Interface contract section exactly.

S5 and S6 are the largest. Give each the same one-page stub template (the three stub rules from §14 of the implementation spec) so the twenty-three packages come back stylistically identical — a reviewer scanning for a faked implementation must be able to do it by pattern, not by reading each file.

**Phase C — main session only.** `internal/cli`, `cmd/qompack`, `internal/pluginmanifest`, `plugin/**`. These are the composition root: they import everything, so they can only be written once S1–S6 have landed, and they are the place where a signature mismatch between subagents surfaces as a compile error. Do not delegate this.

**Phase D — two parallel subagents.**

| Subagent | Owns | Returns |
|---|---|---|
| **S7 — test scaffolding** | `internal/testutil/**`, `test/e2e/**`, `testdata/golden/contracts/**` format fixtures, `devtool gen-contract-fixtures` | the e2e harness building and running the real binary |
| **S8 — CI** | `.github/**`, `testdata/bench-baseline.txt` | the three workflow files, lint-clean YAML |

**Phase E — main session only.** `test/guards/**` (the closing-note and contract guards must be authored by whoever holds the whole picture), then the eight commits in order, running the stated verification after each.

**Integration rule for all phases.** Subagents write to the working tree directly, never to branches. After each phase the main session runs `go build ./... && go run ./tools/devtool lint && go test ./...` before starting the next; a failure is fixed in the main session if it is a one-line signature mismatch and returned to the owning subagent otherwise. No subagent may edit a file outside its list, and no subagent may edit `Qompack.md` or `00-ARCHITECTURE.md` under any circumstances.

---

## Exit criteria

### Quoted from Qompack.md

SP-01 precedes Phase 0, so no phase exit criterion applies to it directly. The criteria it must **make measurable for later waves**, quoted verbatim, are:

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions. *(Phase 0 — SP-02 achieves it; SP-01 ships `eval.minSessions: 20` as the config default and the `replay-gate` job that will enforce it.)*

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms. *(Phase 1 — SP-06 achieves the dedup ratio and SP-08 the hook p99, both measured by SP-02's replay corpus; SP-01 ships `store.Stats.DedupRatio`, budget B-A and the bench-harness contract that will measure them.)*

And the guardrails SP-01 must express as configuration and CI, verbatim from §11.3:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

### Local, measurable Definition of Done

1. `git log --oneline develop..feat/sp01-foundation-toolchain-and-contracts` shows exactly 7 commits (commit 1 is the shared root of `main` and `develop`), each a valid Conventional Commit, none containing an attribution trailer.
2. `go build ./...` and `go vet ./...` exit 0 on Windows, Linux and macOS.
3. `go run ./tools/devtool lint` exits 0: `golangci-lint`, `nomagic`, `importgraph`, `testdeps`, `bindeps`, `sleepcheck`, `stubskips`, `runpatterns`, `docmarkers` — **nine** sub-checks — all clean. In particular `importgraph` passes against the real repository, and `bindeps` proves `go list -deps ./cmd/qompack` contains only stdlib, `github.com/qompack/qompack/…`, zstd, go-winio and `golang.org/x/sys/windows` — so `golang.org/x/tools` in the root `go.mod` never reaches the shipped binary. **Post-V2 correction:** SP-01 shipped seven sub-checks; `runpatterns` and `docmarkers` were added afterwards, in the round that followed V2-VERIFY's discovery of 25 silently-disabled gates, and they read the plan documents rather than the code (`tools/devtool/planchecks.go`) — `runpatterns` fails a `go test -run` in `plans/**` whose pattern matches no test in the package it names, `docmarkers` fails an unfilled all-caps placeholder left in specification prose. `golang.org/x/sys/windows` joined the allowed closure in SP-05's task 6, for the named pipe's per-user SID ACL and `internal/paths`'s Windows rename-replace.
4. `go run ./tools/devtool fmt-check` prints nothing.
5. `go test ./...` exits 0; all 22 conformance suites report a skipped behaviour block, and every skip emitted anywhere in the tree carries the message `behaviour: implementation is a stub (Rule W-1)` or `contract fixture not yet recorded (Rule W-2)` — no other skip reason is permitted.
6. `go test -race ./...` exits 0 on Linux and macOS; `go test -count=2 ./...` exits 0 on Windows.
7. `go run ./tools/devtool cover` meets every §6.4 floor for the packages SP-01 **implements**, and exempts every package it only **stubs**. The exemption is mechanical, not a judgement call: `cover` reads `plans/OWNERS.tsv` for each package's owner and floor, and skips the floor of any package whose owner is not listed in `tools/devtool/cover.go`'s `landedSubplans`, printing `exempt (stub, owned by <SP-NN>)` for each so the exemption list is visible in the job log rather than hidden in a constant (a `main` composition root prints `exempt (composition root, §6.4)` instead). Floors that bind this wave: `config`, `paths` and `tokens` ≥ 90% (§6.4 row 1); `core`, `logging`, `obs`, `hookio`, `cli`, `pluginmanifest`, `testutil` ≥ 75% (§6.4 row 3). Every 90%-floor package SP-01 merely stubs — `store`, `sketch`, `chunk`, `canon`, `redact`, `negknow`, `checkpoint` — is exempt until its owner lands, as are the 85%-floor stubs `scheduler`, `dag`, `analyzer`, `rehydrate`, `eval`, `mcp`. A package's floor binds from the moment its owner is added to `landedSubplans`, in the commit that lands that subplan, and `cover` fails if a landed owner's package still has a probe that looks like a bare `core.ErrNotImplemented` stub. **Post-V2 correction, three parts.** `tokens` is a **90%** package, not 75%: `00-ARCHITECTURE.md` §6.4 lists it in the 90% group and `plans/OWNERS.tsv` ships `tokens SP-01 90 -`, which is the number the gate enforces — the 75% here was 15 points weaker than reality. `redact` belongs in the 90%-floor stub enumeration, which §6.4 lists and this item omitted. And the exemption rule is **`landedSubplans`, not owner identity**: `cover.go` records that it was originally spelled `o.Owner != "SP-01"` — the same rule only while SP-01 was the only landed subplan — and that SP-04 is what separated the two, because chunk, canon and symbols would otherwise have stayed exempt forever with the job log still calling them stubs.
8. `go run ./tools/devtool plugin-validate` exits 0 and `git diff --exit-code -- plugin/` is clean.
9. `go run ./tools/devtool gen-config-docs --check` exits 0.
10. `go run ./tools/devtool build-all` produces all six §2.6 targets.
11. `TestDefaults_MatchesAppendixCVerbatim` passes — `config.Defaults()` minus `runtime` is byte-equivalent to Appendix C.
12. `TestAppendOnlyGuard` passes: all five illegal write attempts against `checkpoints/`, `pins/` and `sketches/tried.bloom` fail.
13. `TestDispatch_HookAlwaysExitsZero` passes all 30 fault-injection combinations, and `TestE2E_AllSixHooksExitZero` passes against the real built binary.
14. `TestGuard_FreshBuildReportsModeFull` passes — a freshly built `develop` reports `ModeFull` with every not-yet-implemented assertion at `OK:true, SevInfo` (§12.1).
15. All four closing-note guards pass, and `TestGuard_NoNetworkImports` and `TestGuard_WriteSetConfinedToQompack` pass.
16. Benchmarks within budget: `BenchmarkHistogram_Observe` < 100 ns/op, `BenchmarkConfigLoad_ColdNoFiles` < 2 ms/op, `BenchmarkHookNoop_InProcess` < 3 ms/op, `BenchmarkPathsWriteAtomic_4KB` < 2 ms/op; results committed to `testdata/bench-baseline.txt`.
17. CI is green on the branch for `verify`, `test` (3 OS), `cover`, `crossbuild`, `plugin-validate`, `security`, `docs`; `bench-gate` and `replay-gate` run and report their not-present message.
18. `Qompack.md` is byte-identical to its state at commit 1 (`git diff <root-commit> HEAD -- Qompack.md` is empty).

---

## Done checklist

- [ ] Repository initialized: `main` and `develop` exist; the root commit contains exactly `Qompack.md`, `plans/`, `.gitignore`, `LICENSE`
- [ ] Work landed on `feat/sp01-foundation-toolchain-and-contracts`, cut from `develop`
- [ ] Commit count verified: 8 total (1 root + 7 on the branch), each with a Conventional Commit subject and a `Refs:` footer
- [ ] **No `Co-Authored-By` lines and no attribution trailers in any commit message**, verified with `git log --format=%B | grep -Ei 'Co-Authored-By|Signed-off-by|Generated with|🤖'` returning nothing
- [ ] Spec coverage self-review against "Design context": §7.4 tree and append-only invariant implemented in `paths` and asserted by `TestAppendOnlyGuard`; §7.5 manifest generated by `pluginmanifest` covering all six §7.3 hooks; Appendix C reproduced verbatim by `config.Defaults()` and golden-tested; §12's "read `r` and `w` from config, never hardcode" enforced by `nomagic`; the closing note's four priorities encoded as passing guard tests
- [ ] Every §5 interface has a compiling stub with the exact normative signature — checked by diffing the declared signatures against `00-ARCHITECTURE.md` §5 package by package
- [ ] Every §5 interface has a `<pkg>test` conformance suite (22 suites, `storetest` exporting two); every behaviour block is skipped with the exact Rule W-1 message; `plans/OWNERS.tsv` lists every package on disk with its owner, §6.4 floor and stub probe
- [ ] `testdata/golden/contracts/` contains the frozen `format` fixtures and a `MANIFEST.json` per package correctly marking `behaviour` fixtures `record-by-owner`
- [ ] Type consistency with the Interface contract section: `core.Dep`, `core.ChunkRef`, `core.Hash`, `core.Clock`, the seven sentinels, `config.Config`/`Provenance`/`Violation`/`Warning`, `paths.Layout`, `obs.Histogram`/`Registry`/`BudgetBreach`, `hookio.Event`/`Output`/`HSO`, `logging.Logger` — all spelled exactly as declared here and as `00-ARCHITECTURE.md` §4/§5 declare them
- [ ] Placeholder scan clean: `grep -RniE 'TODO|TBD|FIXME|XXX|not sure|implement appropriately|handle edge cases' -- ':!Qompack.md' ':!plans/'` returns nothing (stub bodies return `core.ErrNotImplemented`, which is a declared contract, not a placeholder)
- [ ] `Qompack.md` unmodified
- [ ] `go run ./tools/devtool ci-local` green
- [ ] CI green on the branch; `bench-gate`/`replay-gate` present, wired, and marked `continue-on-error` with the removing subplan named inline
- [ ] Branch merged into `develop` with `--no-ff`; verification checkpoint V1 run on `develop`; tag `v0.0.1` applied

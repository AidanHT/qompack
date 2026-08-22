# SP-06: L1 store: sha256 two-level fanout zstd objects, redaction at ingest, Merkle roots, tool_use index, file version history, segment log with the encoded-once DPI guard, GC, and exact token accounting

> **Recommended model: Opus 5 · xhigh effort**
>
> Large but conventional storage engineering — content addressing, zstd, Merkle roots, append-only indices, resumable mark-and-sweep GC — with the spec written out file by file. Volume, not novelty.

**Branch:** `feat/sp06-content-addressed-store` (cut from `develop`) | **Wave:** 1 | **Prerequisites:** the branches of `["SP-01"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 1 (SP-02, SP-03, SP-04, SP-05, SP-07) | **Design sections:** §7.2 L1, §7.4, §8.2, §10 Phase 1 (store half), §12 (storage growth), §13 invariant 7 (redaction) | **Gaps closed:** G2.1, G3.1, G10.2

---

## Mission

This slice builds layer **L1 — STORE**: the durable, content-addressed, globally-deduplicated memory that every later layer reads from and writes to. `Qompack.md` §1.3 names RC-1 — "Compaction deletes from context without leaving a retrieval path, even though the source data is durable on disk" — as the first of three root causes. The store is the retrieval path. Without it, the tombstone in §8.1 item 2 has nothing to point at, the checkpointer of §8.5 has no source that is not itself a summary, and the MCP `expand`/`re_read` tools of §8.7 are unimplementable. §9 credits L1 with closing **G3.1** ("unreachable transcript"), **G2.1** jointly with the append-only invariant and the `encoded_segments` guard, and **G10.2** ("coarse token estimation → L1 exact chunk-level accounting").

Four things in this slice are load-bearing beyond ordinary storage work. First, **redaction at ingest**: `internal/redact` is applied inside `store.Put`/`PutBytes` *before* canonicalization and chunking, because objects are content-addressed and immutable — a secret that reaches `objects/` cannot be deleted without breaking every root that references its chunk. Doing this in wave 1 rather than discovering it in SP-17's wave-5 audit is the whole point. Second, the **segment log's encoded-once flag**, whose `MarkEncoded` is the mechanical enforcement of §4.6's "never compress a compression": a segment already encoded into checkpoint *k* can never be re-encoded into checkpoint *j ≠ k*, and the attempt returns `core.ErrAlreadyEncoded`. Third, **file version history and `ChangedSince`**, the hash-comparison primitive that SP-09's entire staleness machinery — the mitigation for the one §12 risk rated High — is built on. Fourth, **exact chunk-level token accounting**, which replaces the host's 4/3 padding and flat 2,000-token images with per-chunk cached measurements, real image/PDF sizing, and a per-project calibration factor.

**What exists when you start.** SP-01 has merged into `develop`. The repository is initialized (`main`, `develop`), the Go 1.26 module `github.com/qompack/qompack` compiles, and CI (`verify`, `test`, `cover`, `crossbuild`, `plugin-validate`, `security`, `docs`) is green. `internal/core` provides `Hash`, `HashBytes`, `ChunkRef`, `Dep`, `Clock`, and every sentinel error. `internal/paths` provides `Norm`, `Key`, `WriteAtomic`, `AppendOnly`, `CreateNew` and the append-only guard. `internal/config` provides the complete Appendix C schema plus the `runtime` namespace including `runtime.redact`. `internal/logging`, `internal/obs`, `internal/testutil` and `test/e2e` scaffolding exist. Crucially, **`internal/store`, `internal/redact` and `internal/tokens` exist as compiling stubs returning `core.ErrNotImplemented`**, with `storetest.RunStoreSuite`, `storetest.RunSegmentLogSuite`, `redacttest` and `tokenstest` shipped as conformance suites whose behaviour tests are `t.Skip`ped (Rule W-1). `internal/chunk`, `internal/canon`, `internal/symbols` and `internal/sketch` are also SP-01 stubs — their real implementations (SP-04, SP-03) land in this same wave and merge *ahead* of this branch, so per **Rule W-2** you develop against the stubs plus `testdata/golden/contracts/{chunk,canon,symbols,sketch}/` fixtures, and the wave-1 verification checkpoint re-runs your tests against the real implementations.

**What exists when you finish.** `internal/store` is a complete, concurrent-safe, crash-tolerant implementation of the §5.8 `Store` and `SegmentLog` interfaces over `.qompack/objects/ab/cd/<sha256>.zst`, `index/roots.jsonl`, `index/tool_use.jsonl`, `index/files.{jsonl,json}`, `index/segments.jsonl` and `index/sessions.jsonl`, with search, deadline-bounded resumable mark-and-sweep GC, and `Stats.DedupRatio` feeding the Phase 1 ≥ 4:1 exit criterion. `internal/redact` scrubs the nine secret families 00-ARCHITECTURE §5.22a enumerates — as ten ordered rules, `sk-ant-` being split out ahead of the generic `sk-` — plus admitted user patterns, idempotently, at the single choke point. `internal/tokens` replaces SP-01's baseline estimator body with exact per-chunk counts with real image and PDF sizing and a persisted per-project calibration factor. `storetest.RunStoreSuite` and `storetest.RunSegmentLogSuite` have zero remaining skips, and every benchmark below is inside budget.

---

## Design context (verbatim from Qompack.md)

Everything quoted here is normative input. Do not open `Qompack.md` to implement this subplan; do not modify it, ever.

### §7.2 — the layer this subplan owns

```
├─────────────────────────────────────────────────────────────────┤
│ L1  STORE                 CDC chunks · Merkle index · sketches · │
│                           dependence DAG · segment log           │
├─────────────────────────────────────────────────────────────────┤
```

### §7.4 — Directory layout (the parts SP-06 writes)

```
.qompack/                       # gitignored, project-root
├── config.json
├── objects/                       # content-addressed, zstd-compressed
│   └── ab/cd/abcdef…              # sha256, 2-level fanout
├── index/
│   ├── tool_use.jsonl             # tool_use_id → root hash, ts, tool, args
│   ├── files.json                 # path → [(ts, root_hash)] version history
│   └── segments.jsonl             # changepoint-delimited segment log
```

> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### §8.2 — L1 Store (quoted in full; this subplan is this section)

> **Content addressing.** SHA-256 over chunk bytes, two-level directory fanout. Deduplication is global across the project, so four reads of one file cost one chunk set.
>
> **File version history.** `index/files.json` maps path → list of `(timestamp, root_hash)`. This gives cheap answers to "what did this file look like when we made that decision," which is the most common thing lost across compaction.
>
> **Segment log.** Changepoint-delimited segments, each with: start/end turn, feature summary, encoded-once flag, and checkpoint reference. **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint. It is re-encoded from the *original chunks* or not at all.
>
> **Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.

### §8.1 item 1 — the ingest pipeline order (the store's input contract)

> 1. **Chunk and store.** Run FastCDC over the tool result. Suggested parameters for source text: `min = 1KB`, `target = 4KB`, `max = 16KB` — smaller than backup workloads because source files are smaller. Store novel chunks zstd-compressed; record the chunk list.
>    **Canonicalize first (O2).** Exact-hash dedup is defeated by volatile substrings: timestamps, ANSI escape codes, PIDs, memory addresses, temp-dir paths, and run durations make every `Bash` and test-runner output unique even when semantically identical. Before chunking, apply per-tool canonicalizers that strip or normalize these (store the canonical form; keep the volatile deltas as a tiny side record if byte-exact recovery matters). For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text. Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost.

### §6.1 — why content-defined chunking (the dedup claim this store must deliver)

> Hash each chunk, store once, reference by hash. Four reads of a 2,000-line file collapse to one chunk set plus three near-empty reference lists. This is `borg`/`restic`/ZFS. O(n) with a rolling hash — microseconds per megabyte.
>
> The Merkle structure is free once content-addressing, and **it is the retrieval index**: `tool_use_id → root hash → chunk list`. Compaction becomes rehashing pointers rather than deleting content.

### §8.3 — the two clauses that define `ChangedSince`

> 2. Every elimination carries `depends_on`: the hashes of the files or configs the elimination's reason rests on (lockfiles, compose files, the file under test). The Observer already tracks file-version history, so detecting a change to any dependency is a hash comparison it performs anyway.
> 3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.

### §4.6 — the DPI wall that `MarkEncoded` enforces

```
I(X; T₃) ≤ I(X; T₂) ≤ I(X; T₁)
```

> **The only fix is structural: never compress a compression.** Encode each transcript segment exactly once, from the original on disk, and append. This is a non-negotiable invariant of the Qompack design and is enforced by construction in §8.4.

### §8.7 — the minimal-span contract `OpenSpan` and `Search` back

> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch. Most post-compaction questions are "what did that one function look like," not "give me the file."

| Tool | Signature | Purpose |
|---|---|---|
| `recall` | `(query, k=5)` → hits | Search the store by content, path, or symbol; returns hashes and summaries |
| `expand` | `(hash \| tool_use_id)` → content | Re-materialize a cleared tool result |
| `re_read` | `(path, at=null)` → content | Current or historical version of a file |

### §10 Phase 1 — the store half, and the exit criterion

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

### §11.3 — guardrails that bind this slice

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup

### §12 — the risk row this slice mitigates

| Risk | Severity | Mitigation |
|---|---|---|
| Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |

### §9 — the traceability rows this slice is accountable for

| Gap | Closed by | Residual |
|---|---|---|
| G2.1 recursive compression | §7.4 append-only invariant; `encoded_segments` guard | — |
| G3.1 unreachable transcript | L1 store + L6 `expand`/`recall` | — |
| G10.2 coarse token estimation | L1 exact chunk-level accounting | Claude Code's own estimate unchanged |

### §2.2 — the estimation behaviour G10.2 indicts and this slice replaces

> Token estimation pads by 4/3 and flat-rates images and PDFs at 2,000 tokens. Thinking blocks count text only, not the JSON wrapper or signature.

### §3 G10.2 — verbatim

> | G10.2 | **Token estimation is coarse.** 4/3 padding compacts earlier than necessary; flat 2,000 tokens for images and PDFs is badly wrong in both directions for document-heavy sessions. |

### Appendix C — the config block this slice consumes

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

### 00-ARCHITECTURE §5.22a — the redaction contract (verbatim)

> Secrets must never reach `objects/`. Redaction is therefore applied at the single choke point — `store.Put`/`PutBytes`, before canonicalization and chunking — not at the audit stage.
>
> Built-in rules: private-key PEM blocks, `AKIA…`/`ASIA…`, `ghp_`/`gho_`/`github_pat_`, `sk-`/`sk-ant-`, bearer tokens, `password=`/`secret=`/`token=` assignments, `.env` value lines, JWTs, and connection strings with embedded credentials. A fuzz target asserts idempotence (`Redact(Redact(x)) == Redact(x)`) and that no rule ever matches across the whole input.

### 00-ARCHITECTURE §5.8 — GC semantics (verbatim)

> **GC semantics.** Roots are: every checkpoint's `pointers`, every pin, every elimination's `evidence` and `depends_on`, every `tool_use`/file-version entry inside the retention window (`max(30 days, 10 sessions)` by default — "whichever is longer" per §8.2). Collection is authoritative **mark-and-sweep** from those roots; an approximate refcount is maintained alongside purely for `/qompack:status` and for cheap "is this worth keeping" decisions. GC runs on `SessionEnd` and during idle (O3), always with a `Deadline`, always resumable (`GCReport.Truncated`).

### 00-ARCHITECTURE §5.20 — token estimation (verbatim)

> Images and PDFs are estimated from actual dimensions/page count, not the host's flat 2 000. Calibration compares our estimate against `usage.input_tokens` deltas parsed from the transcript when available; the factor is clamped to `[0.6, 1.6]` and stored in `~/.qompack/calibration.json`.

---

## Out of scope

| Item | Owner |
|---|---|
| FastCDC implementation, gear hash, `chunk.RootHash` internals, boundary-stability fuzzing | **SP-04** |
| Canonicalizer registry, per-tool rules, `canon.Restore`, the with/without-canonicalization dedup comparison corpus | **SP-04** |
| `symbols.Extractor` implementation (`Extract`, `Enclosing`, `References`) | **SP-04** |
| Bloom / CMS / HLL / Misra-Gries / MinHash implementations and their serialization | **SP-03** |
| `internal/dag` and every DAG edge, including `EdgeSupersedes` | **SP-07** |
| Daemon, IPC, hot-path budgets, `IdleController` registration of GC | **SP-05** |
| `PostToolUse`/`UserPromptSubmit`/`Stop`/`SessionStart`/`SessionEnd` hook entry points; the addressable tombstone string; supersession *detection* (SP-06 owns only `MarkSuperseded`'s mechanics); the call sites of `Flush`/`GC`/session-index | **SP-08** |
| Elimination ledger, canonical descriptors, staleness *policy* (SP-06 owns only `ChangedSince`), bloom rebuild | **SP-09** |
| `checkpoint.Writer`/`Reader`, the callers of `MarkEncoded`, `encoded_segments` population | **SP-10** |
| Rehydration, drop report | **SP-11** |
| Scheduler, idle loop, droppable-block classification, frontier *advancement* (SP-06 owns only `SegmentLog.Frontier`'s computation) | **SP-12** |
| MCP `expand`/`re_read`/`recall` tool handlers and their span widening (SP-06 owns `OpenSpan` and `Search`, not the tools) | **SP-13** |
| `/qompack:status` rendering of `Stats` and `GCReport` | **SP-14** |
| Per-segment bloom *writer* (SP-06 reserves and reads `Segment.BloomRef`), cross-session warm start, promotion | **SP-16** |
| Security audit, `qompack fsck`/`doctor`, cross-platform matrix | **SP-17** |
| Store-growth guardrail computation from `store.Stats` in the replay gate | **SP-02** |
| `internal/config` schema, `internal/paths`, `internal/core`, `internal/testutil`, `test/e2e` scaffolding | **SP-01** |

---

## Interface contract

### Consumes (already on `develop` as SP-01 stubs; real behind Rule W-2 golden fixtures)

```go
// internal/core
type Hash [32]byte
func (h Hash) String() string            // "sha256:" + hex
func (h Hash) Short() string
func ParseHash(s string) (Hash, error)
func HashBytes(domain string, b []byte) Hash
type SessionID string; type ToolUseID string; type TurnIndex int
type SegmentID int; type CheckpointSeq int; type Tokens int; type UnixMilli int64
type ChunkRef struct{ Hash Hash; Len int }
type Dep struct{ Path string; Hash Hash }
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
var ErrNotFound, ErrAppendOnly, ErrAlreadyEncoded, ErrBudget, ErrDegraded error

// internal/paths — SIGNATURES EXACTLY AS SP-01 SHIPS THEM. Do not re-derive from memory.
type Layout struct {
    Root, Dot                        string // project root, <root>/.qompack
    Objects, Index, Sketches, DAG    string
    Grammar, Checkpoints, Pins, Eval string
    Records, State, Run, Spool, Logs, Metrics, Tmp string
}
func Of(root string) Layout                 // root is the PROJECT root, never <root>/.qompack
func EnsureLayout(l Layout) error           // mkdirs + writes <root>/.qompack/.gitignore == "*\n"
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte, perm fs.FileMode) error   // NOTE: three args
func AppendOnly(p string) (io.WriteCloser, error)              // NOTE: io.WriteCloser, not *os.File
func AppendJSONL(p string, v any) error
func OpenFile(p string, flag int, perm fs.FileMode) (*os.File, error)
func Long(p string) string                  // \\?\ prefixing on windows; identity elsewhere

// internal/config
type Config struct{ Store StoreCfg; Runtime RuntimeCfg; /* … */ }
// StoreCfg.Chunk{Min,Target,Max int}; StoreCfg.Compression string;
// StoreCfg.Retention{Days,Sessions int}; StoreCfg.Canonicalize canon-facing cfg;
// RuntimeCfg.Redact{Enabled bool; Patterns []string}
// RuntimeCfg.Tokens RTokensCfg — SP-01 already owns every numeric constant this slice's
// estimator needs. SP-06 READS these keys and defines no literal duplicate of any of them
// (D11 / 00-ARCHITECTURE §11.6):
type RTokensCfg struct {
    ProseCharsPerToken, CodeCharsPerToken, JSONCharsPerToken float64 // defaults 4.0, 3.6, 3.2
    DiffCharsPerToken, BinaryCharsPerToken                   float64 // defaults 3.4, 3.0
    ImagePixelsPerToken, ImageMaxTokens, PDFTokensPerPage    int     // defaults 750, 1600, 1800
    CalibrationMin, CalibrationMax, CalibrationAlpha         float64 // defaults 0.6, 1.6, 0.2
}

// internal/logging, internal/obs
type Logger interface{ With(...any) Logger; Debug/Info/Warn/Error/Loud(string, ...any) }
func Nop() Logger                      // logging only — there is NO obs.Nop()
type Registry interface{ Hist(string) Histogram; Counter(string) Counter; Gauge(string) Gauge; /* … */ }
func New(c core.Clock) Registry        // obs.New — the only obs constructor SP-01 ships

// internal/chunk (SP-04)
type Params struct{ Min, Target, Max int }
type Chunk struct{ Offset int64; Len int; Hash core.Hash }
type Chunker interface {
    Split(data []byte) []Chunk
    SplitStream(r io.Reader, fn func(Chunk, []byte) error) error
}
func New(p Params) Chunker
func RootHash(chunks []Chunk) core.Hash

// internal/canon (SP-04)
type Class string
type Delta struct{ Offset, Len int; Original string; Class Class }
type Options struct{ Strip []Class; KeepDeltas bool; MinHash sketch.MinHashOptions }
type Result struct {
    Canonical []byte; Deltas []Delta; Applied []string
    Signature sketch.Signature; Reduced float64
}
type Registry interface {
    Register(c Canonicalizer) error
    For(tool, path string) []Canonicalizer
    Run(tool, path string, in []byte, o Options) (Result, error)
    Names() []string
}
func Default(cfg config.CanonicalizeCfg) Registry
func Restore(canonical []byte, deltas []Delta) ([]byte, error)

// internal/sketch (SP-03)
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }
type Signature struct{ Perms uint16; Mins []uint64 }
func (s Signature) Jaccard(o Signature) float64
func (s Signature) IsNearDup(o Signature, threshold float64) bool
func (s Signature) MarshalBinary() ([]byte, error)
func (s *Signature) UnmarshalBinary([]byte) error

// internal/symbols (SP-04)
type Symbol struct{ Name, Kind string; Line, Offset, Len int }
type Extractor interface {
    Extract(path string, b []byte) []Symbol
    Enclosing(path string, b []byte, off int) (Symbol, bool)
    References(b []byte, names []string) map[string]int
}
func New() Extractor
```

### Produces (normative for SP-08, SP-09, SP-10, SP-12, SP-13, SP-14, SP-16, SP-17)

Every signature in 00-ARCHITECTURE §5.8 (`Store`, `SegmentLog`, `Root`, `PutOptions`, `PutResult`, `NearDupInfo`, `Supersession`, `ToolUseRecord`, `FileVersion`, `Query`, `Hit`, `Segment`, `GCPolicy`, `GCReport`, `Stats`, `Deps`, `Open`) and §5.22a (`redact.Match`, `redact.Redactor`, `redact.New`, `redact.Nop`) and §5.20 (`tokens.Class`, `tokens.Estimator`, `tokens.New`, `tokens.Classify`) is implemented unchanged. In addition SP-06 ships these **purely additive** declarations in packages it owns (permitted by 00-ARCHITECTURE §5: "a subplan may add methods to a struct it owns"; no existing field, method or signature is changed or removed, so no amendment is required):

```go
// package store — additive fields on a struct SP-06 owns
type PutResult struct {
    Root      Root
    Novel     int
    Reused    int
    Signature sketch.Signature
    NearDup   *NearDupInfo
    Truncated bool // ADDITIVE: input exceeded MaxPutBytes and the tail was discarded
    Redacted  int  // ADDITIVE: number of redact.Match spans replaced on the way in
}

// package store — additive sentinels and helpers
var ErrSegmentOpen  = errors.New("qompack: segment not closed")
var ErrSegmentExists = errors.New("qompack: segment id already allocated")
const MaxPutBytes = 64 << 20

// ArgsDigest canonicalizes a tool_input JSON document and returns its digest plus the
// ≤120-char preview for ToolUseRecord. SP-08 calls this; nothing else may re-derive it.
func ArgsDigest(raw json.RawMessage) (core.Hash, string)

// ApproxRefs is the approximate refcount of §5.8 GC semantics, for /qompack:status only.
// FSStore is the exported concrete type returned by Open; SP-14 reaches this through the
// narrow interface below rather than a bare type assertion on *FSStore.
type RefCounter interface{ ApproxRefs(h core.Hash) uint32 }
func (s *FSStore) ApproxRefs(h core.Hash) uint32

// package tokens — additive, so `store` can feed measurements without `tokens` importing `store`
type ChunkSink interface{ NoteChunk(h core.Hash, c Class, b []byte) core.Tokens }
func NewExact(cfg config.Config, calibPath, chunkCachePath string) Estimator
func DefaultCalibPath() string   // $QOMPACK_HOME | os.UserHomeDir | os.TempDir + /.qompack/calibration.json
func EstimateImage(b []byte) (core.Tokens, bool)
func EstimatePDF(b []byte) (core.Tokens, bool)
```

**Consumer notes that are part of the contract.** `ChangedSince` takes `[]core.Dep` and never `[]negknow.Dep` (§3.2 — `store` must not import `negknow`). `tokens.EstimateRoot` takes `[]core.ChunkRef` and never `store.Root` (§3.2 — `tokens` must not import `store`). `store.Open` fills every nil member of `Deps` with a working default **except that `Deps.Redact` nil defaults to `redact.New(cfg)`, never `redact.Nop()`** — a nil redactor must never mean "no redaction".

**Path resolution — normative.** `store.Open(root, …)`'s `root` is the **project root**, matching `dag.Open` and `negknow.Open`. Every path in this document is derived from `l := paths.Of(root)`: objects under `l.Objects`, all index files under `l.Index`, daemon state under `l.State`, atomic-write staging and quarantine under `l.Tmp`. Never hand-join `".qompack"`.

**Two additive files under `index/`.** `index/files.jsonl` and `index/sessions.jsonl` do not appear in `Qompack.md` §7.4 or 00-ARCHITECTURE §3.3's tree. They are runtime artifacts in exactly the spirit of §3.3's own "runtime-only, not in §7.4, additive" block (`records/`, `state/`, `run/`, `spool/`): `files.jsonl` is the append-only log behind the `files.json` view §7.4 names, and `sessions.jsonl` is what makes GC's "10 sessions" axis computable. Neither changes a §5 interface, so no amendment is required (§0); both are declared here so SP-14 and SP-17 can enumerate the store's write set.

---

## Implementation spec

### Package layout

```
internal/redact/
├── redact.go          Redactor implementation, interval set, splice
├── rules.go           the ten built-in rules + user pattern compilation
├── redact_test.go
└── fuzz_test.go
internal/tokens/
├── exact.go           the exact estimator, per-chunk cache, ChunkSink
├── classify.go        Classify(tool, path, b) Class + per-class weights
├── media.go           EstimateImage (PNG/JPEG/GIF/WebP), EstimatePDF
├── calibrate.go       EWMA calibration, ~/.qompack/calibration.json
├── chunkcache.go      state/chunktokens.bin load/append/compact
└── *_test.go
internal/store/
├── store.go           FSStore, Open, Deps defaulting, locking, Close
├── objects.go         object path, zstd encoder/decoder pools, put/get/quarantine
├── roots.go           roots.jsonl writer/loader, in-memory root+chunk index
├── put.go             Put / PutBytes — the redact→canon→chunk→store pipeline
├── read.go            GetChunk, GetRoot, Open, OpenSpan, Has
├── tooluse.go         tool_use.jsonl, RecordToolUse, MarkSuperseded, ArgsDigest
├── files.go           files.jsonl + files.json view, FileHistory, FileAt, ChangedSince
├── segments.go        SegmentLog: Open/Close/Get/Range/Current/Unencoded/Frontier/MarkEncoded
├── search.go          Search, scoring, span resolution
├── gc.go              GCPolicy execution, resumable mark-and-sweep, state/gc.json
├── stats.go           Stats, DedupRatio, ApproxRefs
├── flush.go           Flush, session index (index/sessions.jsonl)
├── storetest/         RunStoreSuite, RunSegmentLogSuite (bodies filled in by SP-06)
└── *_test.go, bench_test.go
```

### `internal/redact` — the choke point (§13 invariant 7, §5.22a)

`New(cfg config.Config) Redactor` compiles the built-in rule table in **fixed order** and appends `runtime.redact.patterns` as `custom:<i>`. When `runtime.redact.enabled` is false, `New` returns a redactor whose `Redact` is the identity and whose `Rules()` is empty — but `store.Open` still calls it, so the choke point is never bypassed at the call site.

**User-pattern admission rules (these are what keep the growth bound below true).** A `runtime.redact.patterns` entry is rejected at `New` — one `logging.Loud` line naming the pattern and the reason, the redactor still returns with every other rule active — when any of the following holds: it fails `regexp.Compile`; `re.MatchString("")` is true (a zero-width pattern would splice a placeholder between every byte); or it is not provably ≥ 3 bytes wide, tested by asserting that `re.FindStringIndex` on each of the 128 single-ASCII-byte strings and on the empty string returns no match. Rejection is per-pattern, never fatal, and the count is exposed as `obs.Counter("redact.pattern_rejected")`. At match time `Redact` additionally skips any match with `e-s < 3`, so a pattern that slips through the admission test still cannot break the bound.

Rule table, applied in this exact order. `group` names the submatch index whose span is replaced; `0` means the whole match.

| # | Name | Pattern (Go RE2) | group |
|---|---|---|---|
| 1 | `pem_private_key` | `(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----` | 0 |
| 2 | `aws_access_key_id` | `\b(?:AKIA\|ASIA)[0-9A-Z]{16}\b` | 0 |
| 3 | `github_token` | `\b(?:ghp\|gho\|ghu\|ghs\|ghr)_[A-Za-z0-9]{36}\b\|\bgithub_pat_[A-Za-z0-9_]{22,}\b` | 0 |
| 4 | `anthropic_key` | `\bsk-ant-[A-Za-z0-9_\-]{16,}` | 0 |
| 5 | `generic_sk_key` | `\bsk-[A-Za-z0-9]{20,}` | 0 |
| 6 | `jwt` | `\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}` | 0 |
| 7 | `bearer_token` | `(?i)\bbearer\s+([A-Za-z0-9\-._~+/]{20,}={0,2})` | 1 |
| 8 | `credentialed_uri` | `\b[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s/@:]+:([^\s/@]{3,})@` | 1 |
| 9 | `assignment_secret` | `(?i)\b(?:password\|passwd\|secret\|token\|api[_-]?key\|access[_-]?key\|client[_-]?secret)\b[ \t]*[:=][ \t]*("[^"\n]{4,}"\|'[^'\n]{4,}'\|[^\s,;"'\n]{4,})` | 1 |
| 10 | `dotenv_value` | `(?m)^[ \t]*(?:export[ \t]+)?[A-Z][A-Z0-9_]*(?:KEY\|TOKEN\|SECRET\|PASSWORD\|PASSWD\|CREDENTIAL\|CREDENTIALS\|DSN\|PRIVATE)[A-Z0-9_]*[ \t]*=[ \t]*([^\s#][^\n]{7,})$` | 1 |

Rule 4 precedes rule 5 so `sk-ant-…` is labelled precisely; rule 10 is key-name-gated because `Redact([]byte)` has no path argument, so `FOO=1` and `PORT=8080` must never be touched.

Replacement text is `«redacted:` + rule name + `»`. The algorithm, verbatim:

```go
// placeholderClose is a STRING, never a rune constant. See the V2 reconciliation note below.
const (
    placeholderOpen  = "«redacted:"
    placeholderClose = "»"
)

var placeholderRe = regexp.MustCompile(`«redacted:[a-z0-9_:]+»`)

func (r *rx) Redact(in []byte) ([]byte, []Match) {
    if len(in) == 0 || !r.enabled { return in, nil }
    var consumed intervals
    // Idempotence guard: existing placeholders are immutable and unmatchable.
    for _, loc := range placeholderRe.FindAllIndex(in, -1) { consumed.add(loc[0], loc[1]) }
    var hits []Match
    for _, rule := range r.rules {
        for _, loc := range rule.re.FindAllSubmatchIndex(in, -1) {
            s, e := loc[0], loc[1]
            if rule.group > 0 {
                s, e = loc[2*rule.group], loc[2*rule.group+1]
                if s < 0 { continue }
            }
            if e-s < 3 { continue }   // growth-bound floor; see the admission rules above
            // NO whole-input guard. EVERY rule may match the entire input, not only
            // pem_private_key — see the V2 reconciliation note below.
            if consumed.overlaps(s, e) { continue }
            consumed.add(s, e)
            hits = append(hits, Match{Offset: s, Len: e - s, Rule: rule.name})
        }
    }
    if len(hits) == 0 { return in, nil }
    sort.Slice(hits, func(i, j int) bool { return hits[i].Offset < hits[j].Offset })
    out := make([]byte, 0, len(in)+len(hits)*32)
    prev := 0
    for _, h := range hits {
        out = append(out, in[prev:h.Offset]...)
        out = append(out, placeholderOpen...); out = append(out, h.Rule...)
        out = append(out, placeholderClose...)
        prev = h.Offset + h.Len
    }
    return append(out, in[prev:]...), hits
}
```

> **V2 reconciliation — two real bugs were corrected in the algorithm above, both of which shipped fixed and neither of which may be copied back.** They are recorded as **D11** and **D12** in *Spec resolutions* below and are re-stated here because this is the block a later subplan would copy from. `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.6a ⑫ (**V2-ALL-06**) is the checkpoint finding.
>
> **(a) `out = append(out, '»')` appended a rune constant, and Go truncated it.** `»` is U+00BB, value 187, which fits in a byte — so `append` to a `[]byte` writes the single byte `0xBB` instead of the two-byte UTF-8 sequence `C2 BB`. (The opening `«` was never affected: it arrived as part of a *string* literal.) Verified empirically, the original line emitted `c2 ab … 6a 77 74 bb`, which is **invalid UTF-8** and — far worse — a shape `placeholderRe` can never match, because that regex matches `»` as UTF-8. The idempotence guard at the top of `Redact` was therefore **silently dead**: a second pass no longer recognized its own output, and `Redact(Redact(x)) != Redact(x)` — the one property 00-ARCHITECTURE §5.22a names explicitly. The fix is to append the **string** constant `placeholderClose`, as above. The shipped `internal/redact/redact.go` declares it as a string with this reasoning in its doc comment for exactly this reason; do not "tidy" it into a rune.
>
> **(b) The whole-input guard returned real secrets unredacted, and it is gone.** The line was `if rule.name != "pem_private_key" && s == 0 && e == len(in) { continue }`. It means `Redact("@@SEC_AWS_AKID@@")` returns the key **in the clear** — and the same for a bare JWT, a bare `ghp_` token, or any other family whose entire value is the whole input. It is also mechanically incompatible with the frozen suite: `redacttest`'s `jwt` positive fixture *is* nothing but a JWT, so the guard yields zero matches and its `require.NotEmpty` fails. The conflict is forced, not a preference. **This document's own PEM carve-out reasoning applies verbatim to all ten families**, so the carve-out was generalized rather than special-cased, and §5.22a's greediness property is preserved where it is actually meaningful — over the corpus fixtures, as the paragraph below now states.

`intervals` is a sorted `[]struct{s, e int}` with binary-search `overlaps` and insert — O(m log m) for m matches. Growth bound: every replacement emits at most `len("«redacted:") + 24 + len("»")` = 37 bytes (11 + 24 + 2; rule names are capped at 24 bytes, `custom:<i>` included) and, by the admission rules and the `e-s < 3` skip, consumes at least 3 bytes — so `len(out) ≤ 13*len(in) + 37` unconditionally, which is the "bounded factor" 00-ARCHITECTURE §5.22a requires. The corpus test asserts the tighter practical bound `len(out) ≤ 2*len(in) + 64`.

§5.22a's "no rule ever matches across the whole input" is asserted **over the corpus fixtures** (`TestRedact_NeverWholeInputExceptPEM`), where it is a statement about *rule greediness* rather than about short inputs. **All ten families may match an entire input**, not just `pem_private_key`: a tool result that *is* nothing but a private key, an AWS key or a JWT must still be fully redacted, and refusing the match there would put the secret in `objects/` — the exact outcome §13 invariant 7 forbids. **Failure modes:** an invalid or inadmissible user pattern is logged once through `logging.Loud` at `New` and skipped, never fatal; `Redact` never returns an error and never returns a nil slice for a non-empty input.

### `internal/tokens` — exact chunk-level accounting (G10.2)

**Scope boundary with SP-01's baseline.** SP-01 §10 ships `internal/tokens` **real, not stubbed**: a working `Classify`, a byte-ratio `Estimate`, a memoized `EstimateRoot`, `Calibrate`, and the `runtime.tokens.*` config keys. SP-01 explicitly calls its estimator a *baseline* and hands the per-chunk value to this subplan ("SP-06 replaces the per-chunk value with a measured one keyed by the same map; the signature and the memo key do not change"). SP-06 therefore replaces the *body* of the estimator and touches nothing else:

- **`Classify` is kept exactly as SP-01 wrote it, plus one inserted rule.** Immediately before SP-01's `%PDF` magic check, insert: PNG (`\x89PNG\r\n\x1a\n`), JPEG (`\xFF\xD8\xFF`), GIF (`GIF87a`/`GIF89a`) or WebP (`RIFF`…`WEBP`) magic → `ClassImage`. Store content frequently arrives with `Path == ""`, where SP-01's extension switch cannot fire and a PNG would otherwise classify `ClassBinary`. Nothing else in `Classify` changes, so SP-01's `TestClassify_Table` continues to pass unmodified.
- **Every numeric constant that already has a `runtime.tokens.*` key is read from config**, never written as a literal (D11, 00-ARCHITECTURE §11.6): `*CharsPerToken`, `ImagePixelsPerToken`, `ImageMaxTokens`, `PDFTokensPerPage`, `CalibrationMin`, `CalibrationMax`, `CalibrationAlpha`. The only new numeric constants SP-06 introduces are `unitWeight` (the unit-scanner's per-class calibration) and the host's documented 1568 px long-edge clamp, neither of which has a config key and neither of which is in §11.6's forbidden literal set.
- **One SP-01 test changes, and it is in scope.** `TestEstimate_ProseVsCode` asserts the byte-ratio numbers (`prose 1000`, `code 1112` for 4 000 bytes) that the unit scanner replaces; commit 2 rewrites it against the exact estimator. `TestClassify_Table`, `TestEstimate_ImageFromDimensions` (100×100 → 14), `TestEstimate_ImageCappedAt1600` (4 000×4 000 → 1600), `TestEstimate_PDFPageCount` (3 pages → `3·PDFTokensPerPage`) and `TestEstimateRoot_SumsChunks` (3×1 000 prose → 750) all still pass unmodified under the spec below — verify that before rewriting anything, because a failure in one of them means the spec was implemented wrong, not that the test is stale.

Unit counting (deterministic, tokenizer-free, byte-exact across platforms):

```go
// units counts BPE-approximating units. It never allocates.
func units(b []byte) int {
    n, i := 0, 0
    for i < len(b) {
        c := b[i]
        switch {
        case isAlnum(c): // letters, digits, '_' and '\''
            j := i; for j < len(b) && isAlnum(b[j]) { j++ }
            n += (j - i + 3) / 4        // ceil(L/4): words longer than 4 bytes split
            i = j
        case c == ' ' || c == '\t' || c == '\r':
            j := i; for j < len(b) && (b[j]==' '||b[j]=='\t'||b[j]=='\r') { j++ }
            if j-i > 2 { n += (j - i) / 4 }   // long indentation runs are their own tokens
            i = j
        case c == '\n':
            n++; i++
        case c < 0x80:
            n++; i++                      // each punctuation byte is its own unit
        default:
            r, sz := utf8.DecodeRune(b[i:])
            if r >= 0x0800 { n += 2 } else { n++ }
            i += sz
        }
    }
    return n
}

var unitWeight = map[Class]float64{
    ClassProse: 0.92, ClassCode: 1.00, ClassJSON: 1.05,
    ClassDiff: 1.00, ClassBinary: 1.35, ClassImage: 1.00, ClassPDF: 1.00,
}

// Factor() is applied on EVERY path, including media, exactly as SP-01's baseline
// `Estimate = round(raw * Factor())` does. At the default factor 1.0 the media numbers are
// unchanged, which is why SP-01's two image tests keep passing.
func (e *exact) Estimate(b []byte, c Class) core.Tokens {
    raw := 0.0
    switch c {
    case ClassImage: if t, ok := e.EstimateImage(b); ok { raw = float64(t) }
    case ClassPDF:   if t, ok := e.EstimatePDF(b);   ok { raw = float64(t) }
    }
    if raw == 0 {                       // not media, or unparseable media
        w := unitWeight[c]
        if c == ClassImage || c == ClassPDF { w = unitWeight[ClassBinary] }
        raw = float64(units(b)) * w
    }
    return core.Tokens(math.Round(raw * e.Factor()))
}
```

`EstimateImage` parses real dimensions — PNG `IHDR` at byte offsets 16–24 big-endian; JPEG by walking `0xFF` markers to `SOF0`–`SOF3`/`SOF5`–`SOF7`/`SOF9`–`SOF11` and reading height/width at marker+5; GIF logical screen descriptor at offsets 6–10 little-endian; WebP `VP8X` (24-bit widths at offset 24) / `VP8 ` (offsets 26–30) / `VP8L` (14-bit packed at offset 21) — then applies the host's documented sizing: long edge clamped to **1568 px** (the one SP-06-introduced constant here; it has no config key), `tokens = ceil(w·s · h·s / cfg.ImagePixelsPerToken)` where `s = min(1, 1568/maxDim)`, result clamped to `[1, cfg.ImageMaxTokens]`. Unparseable image → `(0, false)`, and `Estimate` falls back to the unit scanner at `unitWeight[ClassBinary]` as the snippet above shows. The package-level `EstimateImage(b []byte) (core.Tokens, bool)` declared under "Produces" is a convenience wrapper that uses `config.Defaults().Runtime.Tokens`; the estimator's own method is the one that honours a project override.

`EstimatePDF` counts pages by matching `/Type\s*/Page[^s]` (never `/Pages`), inflates every `FlateDecode` stream with `compress/zlib` (stdlib; a stream that fails to inflate is skipped), and keeps printable runs of ≥ 3 bytes from the inflated content. If the recovered text is under `50·pages` units the document is treated as **scanned** and the result is `pages · cfg.PDFTokensPerPage` (default 1800 — the same key SP-01's baseline uses, which is what keeps `TestEstimate_PDFPageCount` green). Otherwise it is a **text** PDF and the result is `max(pages, round(units(text) · unitWeight[ClassProse]))` — the extracted text is the token cost; there is no separate per-page surcharge, because inventing one would be a magic number with no config key and no measurement behind it. Pages of 0 are treated as 1.

`EstimateRoot(ctx, chunks []core.ChunkRef, c Class) core.Tokens` sums a **per-chunk-hash** cache and **never re-scans bytes**. The cache stores the class-independent `units(b)` count, not a token count, so the memo key stays `core.Hash` exactly as SP-01 §10 requires; the class weight and the calibration factor are applied at read time:

```
EstimateRoot = round( Σ_i unitsOf(chunk_i) · unitWeight[c] · Factor() )
```

A cache miss falls back to `ceil(float64(ref.Len) / charsPerToken(c))` — `charsPerToken` reading `runtime.tokens.{Prose,Code,JSON,Diff,Binary}CharsPerToken` (4.0 / 3.6 / 3.2 / 3.4 / 3.0), with `ClassImage` and `ClassPDF` using `BinaryCharsPerToken` because a chunk of an image or PDF is opaque bytes — and increments `obs.Counter("tokens.chunk_miss")`. This is byte-for-byte SP-01's baseline formula, which is why `TestEstimateRoot_SumsChunks` (3 × 1 000 bytes of `ClassProse` → `3·ceil(1000/4.0)` = 750) still passes on an empty cache. The cache is populated exclusively through `NoteChunk(h, c, b)`, which `store.Put` calls once per novel chunk while it still holds the plaintext:

```go
func (e *exact) NoteChunk(h core.Hash, c Class, b []byte) core.Tokens {
    u := units(b)                                   // class-independent
    e.mu.Lock()
    if _, ok := e.cache[h]; !ok {
        e.cache[h] = uint32(u)
        e.pending = append(e.pending, record{h, uint32(u)})
    }
    e.mu.Unlock()
    return core.Tokens(math.Round(float64(u) * unitWeight[c]))  // factor applied at read
}
```

Cache persistence: `<Layout.State>/chunktokens.bin`, a 16-byte header (`'Q','P','K','T'`, uint16 version 1, uint16 reserved 0, uint64 entries) followed by 36-byte records `[32]byte hash | uint32 units`, appended on `Flush`/`Close` and fully rewritten via `paths.WriteAtomic(p, b, 0o644)` when the file exceeds twice the in-memory cap of 250 000 entries. In-memory eviction is insertion-order (oldest 25 % dropped at the cap); an evicted entry is still on disk and reloads on next `NewExact`.

Calibration keeps SP-01's formula and its three config keys, and adds only a warm-up: `Calibrate(observed, estimated)` ignores non-positive arguments, computes `ratio = observed/estimated`, and updates `ewma = ewma*(1-α) + α*ratio` with `α = cfg.CalibrationAlpha` (0.2), `ewma` initialised to the first ratio. `Factor()` returns `1.0` until `samples ≥ 5` and `clamp(ewma, cfg.CalibrationMin, cfg.CalibrationMax)` — i.e. `[0.6, 1.6]` — thereafter, which is what 00-ARCHITECTURE §5.20 requires. The warm-up is the only deviation from SP-01's baseline and is compatible with SP-01's `TestCalibrate_ClampsAndPersists`, whose 20 samples clear it and whose only assertion is `Factor() ≤ 1.6`. State is persisted per project, keyed by `core.HashBytes("qompack.project.v1", []byte(projectRoot)).Short()` (first 12 hex chars), to `~/.qompack/calibration.json`:

```json
{"version":1,"projects":{"9f2a1c04bb7e":{"factor":1.03,"ewma":1.0312,"samples":42,"updated":1734128400123}}}
```

The loader accepts **both** shapes: this versioned document, and SP-01's flat `{"<key>": <factor>}` baseline shape, which it reads as `{factor, ewma: factor, samples: 5}` and rewrites in the versioned shape on the next save — so upgrading does not throw away a calibrated project. Written with `paths.WriteAtomic(p, b, 0o600)` (it lives outside the project and holds nothing secret, but user-global state is not world-readable), at most once per 30 s and unconditionally on `Close`. A missing or corrupt file means `factor = 1.0` and a single `Loud` line — never a failure.

### `internal/store` — object layer

**Object path.** `objects/<h[0:2]>/<h[2:4]>/<h>.zst` where `h` is the lowercase 64-char hex of `chunk.Chunk.Hash`. When `config.Store.Compression == "none"` the suffix is omitted; readers try `.zst` first, then the bare name, so a store whose compression setting changed mid-life still reads. The chunk hash is produced by `internal/chunk` and is **treated as opaque** — the store never re-derives it, so no assumption about SP-04's hash domain leaks into SP-06.

**Integrity.** Encoders are created with `zstd.WithEncoderCRC(true)`, so every frame carries a content checksum that `DecodeAll` verifies. `GetChunk` additionally asserts the decoded length equals the `ChunkRef.Len` recorded in `roots.jsonl`. A checksum or length failure moves the object to `.qompack/tmp/quarantine/<h>.zst`, emits `log.Loud("store: object quarantined", "hash", h.Short(), "reason", …)`, increments `obs.Counter("store.quarantined")`, and returns `core.ErrNotFound` — the §12.3 "store corrupt" row, verbatim.

**Encoder pooling.** A package-level pool sized `min(GOMAXPROCS, 4)`:

```go
type codec struct{ enc chan *zstd.Encoder; dec chan *zstd.Decoder }

func newCodec(n int) *codec {
    c := &codec{enc: make(chan *zstd.Encoder, n), dec: make(chan *zstd.Decoder, n)}
    for i := 0; i < n; i++ {
        e, _ := zstd.NewWriter(nil,
            zstd.WithEncoderLevel(zstd.SpeedDefault),
            zstd.WithEncoderConcurrency(1),
            zstd.WithEncoderCRC(true))
        d, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
        c.enc <- e; c.dec <- d
    }
    return c
}
func (c *codec) compress(dst, src []byte) []byte {
    e := <-c.enc; defer func() { c.enc <- e }()
    return e.EncodeAll(src, dst[:0])
}
```

`WithEncoderConcurrency(1)` is deliberate: it keeps klauspost from spawning goroutines per call, which is what makes B-C predictable.

**Object write.** `putObject(h, plain []byte) (int64, bool, error)` returns compressed bytes written and whether it was novel. It returns `(0, false, nil)` immediately when `Has(h)` — zero, not the existing file's size, because the only caller adds the returned value to `Stats.Bytes` and an already-counted object must not be counted twice. Otherwise it compresses, writes to `.qompack/tmp/obj-<12 hex random>`, `Sync()`, `Close()`, `os.MkdirAll` the two fanout directories, then `os.Rename` into place. On Windows, `ERROR_ACCESS_DENIED` and `ERROR_SHARING_VIOLATION` (antivirus contention) are retried three times with 1 ms / 2 ms / 4 ms backoff before failing; a rename over an existing file is success, not an error (another goroutine won the race with byte-identical content). A failed object write **fails the enclosing `Put`** — a root whose chunks are not all present would be an unreadable lie.

> **V2 reconciliation — two clauses in the sentence above did not ship, and both changes are deliberate.** Recorded as **D15** and **D17** in *Spec resolutions* below; the checkpoint finding is `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.6a ⑬ (**V2-ALL-06**). Read the sentence without them.
>
> - **The per-object `Sync()` before the rename is gone.** One 100 KB result is tens to hundreds of chunks, and serialized fsyncs measured **1.9 s** against a 3 ms B-C budget. The rename still supplies **atomicity** — no torn object is ever visible — and **durability is batched into `Flush`**, which is how borg, restic and git commit a repository transaction (git does not fsync loose objects by default either). **A crash can now lose an object but can never corrupt one.** The reachable crash states are: neither object nor root line (clean); an object with no root line (an orphan, which GC collects); or a root line without its object, which `GetChunk`/`Open` report as `core.ErrNotFound` and `Has` catches by falling back to a stat. That last state is degraded-**but-detected**; repairing it is `qompack fsck`'s job and is an explicit **residual SP-17 inherits**.
> - **The `1 ms / 2 ms / 4 ms` backoff was replaced by `runtime.Gosched()` retries.** §6.1 bans wall-clock sleeps outright, `devtool lint`'s `sleepcheck` enforces the ban with **no annotation escape hatch**, and `core.Clock` exposes only `Now`/`Since` — there is nothing legitimate to sleep on. The ban is also correct on its own terms here: 7 ms of backoff inside a 3 ms budget is precisely what it exists to prevent. The retry *count* is unchanged; a lock that outlives the retries fails the `Put`, the caller degrades (§12.3), and the identical object is rewritten on the next attempt, because the content address has not changed.

### `internal/store` — the ingest pipeline (`put.go`)

```go
func (s *FSStore) PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error) {
    var res PutResult
    if len(b) > MaxPutBytes { b, res.Truncated = b[:MaxPutBytes], true }
    res.Root.RawBytes = int64(len(b))

    // 1. REDACT — before canonicalization, before chunking. §13 invariant 7.
    red, matches := s.deps.Redact.Redact(b)
    res.Redacted = len(matches)

    // 2. CANONICALIZE (O2, §8.1 item 1).
    cr, err := s.deps.Canon.Run(o.Tool, o.Path, red, s.canonOptions(o))
    if err != nil {                       // never fatal: fall back to the redacted bytes
        s.log.Warn("store: canonicalize failed, storing redacted bytes", "tool", o.Tool, "err", err)
        cr = canon.Result{Canonical: red}
    }
    canonical := cr.Canonical
    res.Signature, res.Root.CanonBytes = cr.Signature, int64(len(canonical))

    // 3. CHUNK.
    chunks := s.deps.Chunker.Split(canonical)
    root := chunk.RootHash(chunks)
    res.Root.Hash = root

    // 4. Root-level dedup: an identical root is a pure reference, zero writes.
    if e, ok := s.rootIndex.get(root); ok {
        raw := res.Root.RawBytes                       // THIS put's raw size…
        res.Root, res.Novel, res.Reused = e.Root, 0, len(e.Root.Chunks)
        res.Root.RawBytes = raw                        // …not the stored root's.
        res.NearDup = s.nearDup(o.Path, root, res.Signature)
        s.countRaw(o, raw)
        return res, nil
    }

    // 5. Chunk-level dedup + object writes + token measurement.
    class := tokens.Classify(o.Tool, o.Path, canonical)
    refs := make([]core.ChunkRef, len(chunks))
    for i, c := range chunks {
        plain := canonical[c.Offset : c.Offset+int64(c.Len)]
        n, novel, err := s.putObject(c.Hash, plain)
        if err != nil { return res, fmt.Errorf("store: put chunk %s: %w", c.Hash.Short(), err) }
        if novel {
            res.Novel++; s.addBytes(n)
            if sink, ok := s.deps.Tokens.(tokens.ChunkSink); ok { sink.NoteChunk(c.Hash, class, plain) }
        } else { res.Reused++ }
        refs[i] = core.ChunkRef{Hash: c.Hash, Len: c.Len}
    }
    res.Root.Chunks = refs
    res.Root.Tokens = s.deps.Tokens.EstimateRoot(ctx, refs, class)

    // 6. Volatile side record (§8.1 "keep the volatile deltas as a tiny side record").
    var deltaRoot core.Hash
    if o.KeepRaw && len(cr.Deltas) > 0 { deltaRoot, err = s.putDeltas(ctx, cr.Deltas); if err != nil { return res, err } }

    // 7. Append the roots.jsonl line and publish to the in-memory index.
    if err := s.appendRoot(rootLine{Root: res.Root, TS: s.now(), Tool: o.Tool, Path: o.Path,
        Class: uint8(class), Eph: o.Ephemeral, Sig: res.Signature, Deltas: deltaRoot}); err != nil { return res, err }
    res.NearDup = s.nearDup(o.Path, root, res.Signature)
    s.countRaw(o, res.Root.RawBytes)
    return res, nil
}
```

`Put(ctx, r io.Reader, o)` reads through `io.LimitReader(r, MaxPutBytes+1)` into a pooled buffer and delegates; reading more than `MaxPutBytes` sets `Truncated` and discards the tail rather than erroring, because the caller is a hook that must exit 0.

`putDeltas(ctx, deltas []canon.Delta) (core.Hash, error)` serializes `deltas` as one compact JSON array (`[{"o":…,"l":…,"c":"timestamps","s":"2024-06-13T09:11:04Z"},…]`, keys in that fixed order), chunks it with the same `Chunker`, writes the chunks through `putObject`, and appends a `rootLine` carrying `"tool":"«deltas»"` and `"path":""` so the delta root is an ordinary, GC-visible root. It **does not** recurse through `PutBytes`: it must not be redacted again (the deltas were extracted from bytes that step 1 already redacted, so they are clean by construction) and must not be canonicalized (canonicalizing the record of what canonicalization removed is meaningless). Delta bytes count toward `Stats.Bytes` but **not** toward `Stats.RawBytes` — they are store overhead, not transcript, and folding them into the numerator would inflate `DedupRatio`. `KeepRaw == false`, or an empty `cr.Deltas`, means no delta root and `"deltas"` is omitted from the line.

`o.Ephemeral` (§8.7 "retrieved content is born ephemeral") is recorded on the `rootLine` as `"eph":true` and has exactly one mechanical consequence inside L1: **GC does not treat an ephemeral root as in-window by age**. An ephemeral root's chunks survive only while some non-ephemeral root, checkpoint pointer, pin or elimination still references them — which is safe precisely because objects are content-addressed, so a chunk shared with a real tool result is never collected. Everything else about ephemerality (eviction ranking, `_meta.qompack.ephemeral`) belongs to SP-13 and SP-15; L1 only makes the flag durable and lets GC reclaim retrieval spam.

`canonOptions` maps `config.Store.Canonicalize` onto `canon.Options`: `Strip` from `canonicalize.strip`, `KeepDeltas = o.KeepRaw`, `MinHash{Enabled: canonicalize.minhash.enabled, Permutations: …, NearDupThreshold: …}`. When `canonicalize.enabled` is false, `Strip` is `nil` and only the `crlf` normalization of 00-ARCHITECTURE §4 remains, so Windows and Linux reads of the same file still dedup.

`nearDup(path, root, sig)` compares `sig` against the signature of the most recent prior root for `paths.Key(path)` (from the in-memory per-path index). It returns non-nil when `prior.Signature.Jaccard(sig) ≥ canonicalize.minhash.nearDupThreshold` **and** `prior.Hash != root`, with `DeltaBytes = |CanonBytes(new) − CanonBytes(prior)|`. Path `""` or MinHash disabled → nil.

`countRaw` accumulates `RawBytes` per session for `Stats.DedupRatio`, counting **every** `Put` including exact-duplicate ones — that is what makes the ratio the Phase 1 exit criterion rather than a compression ratio.

### `roots.jsonl` — byte-for-byte

One compact JSON object per line, `\n`-terminated, written through `paths.AppendOnly`, keys emitted in this exact order by a hand-written marshaller (not `encoding/json` struct order, so goldens are stable), `SetEscapeHTML(false)` semantics — no `<` escaping:

```
{"v":1,"root":"sha256:6f1c…","ts":1734128400123,"tool":"FileRead","path":"src/auth.ts","raw":24188,"canon":23904,"tokens":5942,"class":1,"eph":false,"chunks":[{"h":"sha256:1a2b…","n":4096},{"h":"sha256:9c8d…","n":3771}],"sig":{"p":128,"m":"AAAB…"},"deltas":"sha256:44ff…"}
```

`sig`, `deltas` and `eph` are omitted entirely when absent/false. `sig.m` is standard base64 (`base64.StdEncoding`) of `sketch.Signature.MarshalBinary()`. `class` is the `tokens.Class` ordinal **in 00-ARCHITECTURE §5.20 declaration order, pinned here because it is persisted**: `0 ClassProse, 1 ClassCode, 2 ClassJSON, 3 ClassDiff, 4 ClassImage, 5 ClassPDF, 6 ClassBinary`. The sample line is a `FileRead` of a `.ts` file, hence `1`. A loader that meets an unknown ordinal treats it as `0` and counts `obs.Counter("store.index.badclass")`. A GC tombstone is a different line shape on the same file (append-only is preserved; nothing is ever rewritten):

```
{"v":1,"op":"gc","root":"sha256:6f1c…","ts":1734131000000}
```

**Loader.** `Open` scans `roots.jsonl` once with a `bufio.Scanner` at a 4 MiB buffer, building: `rootIndex map[core.Hash]*rootEntry`, `chunkSet map[core.Hash]struct{}`, `refs map[core.Hash]uint32` (the approximate refcount), and `byPath map[string][]core.Hash` ordered by `ts`. A tombstoned root is deleted from `rootIndex` and its chunks decremented; chunks reaching zero leave `chunkSet`. A malformed line is counted (`obs.Counter("store.index.badline")`), logged once per file at `Warn`, and skipped — a truncated final line from a crash must not make the store unopenable.

### `tool_use.jsonl` and `MarkSuperseded`

```
{"v":1,"id":"toolu_01ABC","s":"sess-7f","turn":42,"ts":1734128400123,"tool":"FileRead","argd":"sha256:aa11…","argp":"src/auth.ts (offset 0, limit 200)","root":"sha256:6f1c…","path":"src/auth.ts","bytes":24188,"tokens":5942,"sig":{"p":128,"m":"AAAB…"},"st":0,"by":"","eph":false,"sub":""}
```

`st` is `Supersession` (0 = `StatusOK`, 1 = `StatusSuperseded`). Because the file is append-only, `MarkSuperseded(older, by)` appends a mutation record rather than rewriting:

```
{"v":1,"op":"supersede","id":"toolu_01ABC","by":"toolu_01XYZ","ts":1734128500000}
```

The loader applies records in file order, last-wins, into `toolUse map[core.ToolUseID]*ToolUseRecord` plus `byPathTU map[string][]core.ToolUseID` (ts-ordered) backing `ToolUsesByPath(path, limit)` which returns the most recent `limit` records, newest first, `limit ≤ 0` meaning all. `MarkSuperseded` returns `core.ErrNotFound` if either id is unknown, and is idempotent for an identical `(older, by)` pair. `RecordToolUse` rejects a duplicate `ID` with an already-different `Root` (`fmt.Errorf("%w: tool_use %s already recorded", core.ErrAppendOnly, rec.ID)`) and is a silent no-op for a byte-identical re-record, so WAL replay after a daemon crash is safe.

`ArgsDigest(raw json.RawMessage) (core.Hash, string)` canonicalizes the JSON (decoded with `json.Decoder.UseNumber()`, recursively sorted object keys, no insignificant whitespace, array order preserved, numbers re-emitted **verbatim as their `json.Number` literal** — never through `float64`, which would silently corrupt any argument above 2⁵³ such as a byte offset or an epoch-nanosecond timestamp; string escaping via `SetEscapeHTML(false)`), returns `core.HashBytes("qompack.args.v1", canonical)` and a preview: for an object, the values of `file_path`/`path`/`pattern`/`command`/`url` joined with `" "` if present, else the canonical JSON; trimmed of control characters, collapsed whitespace, and truncated on a rune boundary to `argsPreviewMax` with a trailing `…`.

```go
const argsPreviewMax = 120 //nomagic:allow §5.8 specifies ToolUseRecord.ArgsPreview as ≤120 chars
```

### File version history and `ChangedSince`

`index/files.jsonl` is the append-only truth; `index/files.json` is the materialized view §7.4 names. This is precisely the pattern 00-ARCHITECTURE §3.3 mandates for `pins/invariants.jsonl` → `invariants.json` ("the log is the truth"), applied for the same reason: rewriting a whole map on the hot path would blow B-C.

```
{"v":1,"path":"src/auth.ts","ts":1734128400123,"turn":42,"root":"sha256:6f1c…","bytes":24188}
```

The view, regenerated by `Flush` via `paths.WriteAtomic`, with paths sorted lexicographically and versions ascending by `ts`:

```json
{"version":1,"generated":1734128999000,"files":{"src/auth.ts":[{"ts":1734128400123,"root":"sha256:6f1c…","turn":42,"bytes":24188}]}}
```

- `AppendFileVersion(ctx, path, v)` normalizes with `paths.Key(path)`, rejects `v.Root == core.Hash{}` with `core.ErrNotFound`, and is a no-op when the newest existing version has the same `Root` **and** the same `Turn` (idempotent WAL replay).
- `FileHistory(ctx, path)` returns versions ascending by `ts`; unknown path → `(nil, core.ErrNotFound)`.
- `FileAt(ctx, path, at)` returns the last version with `TS ≤ at.UnixMilli()`; none → `core.ErrNotFound`. A zero `at` means "latest".
- `ChangedSince(ctx, deps)` returns the subset whose current hash differs, under these three normative rules:
  1. history exists and the newest `Root != dep.Hash` → **changed**;
  2. history exists and the newest `Root == dep.Hash` → **unchanged**;
  3. **no history for the path → unchanged**, plus `obs.Counter("store.changed_since.unknown_path")`.

  Rule 3 is deliberate and is part of the contract SP-09 codes against. `ChangedSince`'s specified meaning is "the subset of deps whose current file-version hash *differs*"; absence of a recorded version is not a difference. Reporting unknowns as changed would flip every `scope:"project"` elimination to stale on the first session against a fresh clone with an empty store — destroying the feature §8.3 calls "the highest-value single feature in the plugin" — while the counter keeps the situation observable to `/qompack:status`. Deps are matched by `paths.Key(dep.Path)`; the returned slice preserves input order and carries the *input* `Dep` values unchanged.

### `SegmentLog` — the DPI guard

`index/segments.jsonl`, append-only, four record shapes:

```
{"v":1,"op":"open","id":12,"s":"sess-7f","st":40,"sts":1734128000000}
{"v":1,"op":"close","id":12,"et":57,"ets":1734129000000,"tok":18402,"feat":{"paths":0.42,"tools":0.19,"time":0.03,"todos":1}}
{"v":1,"op":"encode","id":12,"seq":7,"ts":1734129100000}
{"v":1,"op":"bloom","id":12,"ref":"sketches/seg-0012.bloom"}
```

The `bloom` record is **parsed and surfaced as `Segment.BloomRef` by SP-06 but never written by it** — SP-16 adds the writer, which is exactly the reservation 00-ARCHITECTURE §5.8 describes with `"" until SP-16`.

- `Open(ctx, s Segment) (core.SegmentID, error)` — `s.ID == 0` allocates `maxID+1` (1-based, monotonic per project); a non-zero `s.ID ≤ maxID` returns `ErrSegmentExists`. `EndTurn` is initialised to `StartTurn`, `Closed=false`, `EncodedOnce=false`.
- `Close(ctx, id, endTurn, feats)` — unknown id → `core.ErrNotFound`; already closed with the same `endTurn` → no-op; already closed with a different `endTurn` → `core.ErrAppendOnly`; `endTurn < StartTurn` → `fmt.Errorf("store: segment %d end turn %d precedes start turn %d", …)`. **`Segment.Tokens` can only ever be set here.** The caller supplies it as `feats["tokens"]`; the writer removes that entry from the persisted `feat` map, stores it as `core.Tokens(math.Round(v))` in the `"tok"` field, and it becomes `Segment.Tokens` on load. There is no setter and no second `Close`, so a caller that omits `feats["tokens"]` leaves `Tokens == 0` **permanently** — SP-12, which closes segments, must always pass it, and `Close` logs at `Warn` (once per segment) when it is absent so the omission is visible rather than silently zero.
- `MarkEncoded(ctx, ids, seq)` — **validate the entire batch before appending anything**, so a partial DPI violation can never half-mark:

```go
func (l *segLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
    l.mu.Lock(); defer l.mu.Unlock()
    pending := make([]core.SegmentID, 0, len(ids))
    for _, id := range ids {
        seg, ok := l.byID[id]
        if !ok { return fmt.Errorf("%w: segment %d", core.ErrNotFound, id) }
        if !seg.Closed { return fmt.Errorf("%w: segment %d", ErrSegmentOpen, id) }
        if seg.EncodedOnce {
            if seg.CheckpointSeq == seq { continue }   // idempotent for the same seq
            return fmt.Errorf("%w: segment %d already encoded into checkpoint %d, refused for %d",
                core.ErrAlreadyEncoded, id, seg.CheckpointSeq, seq)
        }
        pending = append(pending, id)
    }
    for _, id := range pending {
        if err := l.append(encodeRec{ID: id, Seq: seq, TS: l.now()}); err != nil { return err }
        l.byID[id].EncodedOnce, l.byID[id].CheckpointSeq = true, seq
    }
    return nil
}
```

- `Frontier(ctx, s)` — sort the session's segments by `StartTurn` and walk while `EncodedOnce` is true; return the `EndTurn` of the last consecutively-encoded segment, or `0` when the first is unencoded or the session has no segments. Contiguity matters: frontier *N* means "a checkpoint fully covers the session through turn *N*" (§8.5 O1/O5), which a non-contiguous max would misstate. **`0` is the "no coverage" value and is deliberately indistinguishable from "turn 0 is covered"** — SP-10 (`TestFocusOmitsSpanWhenFrontierZero`), SP-08 (`TestOnSessionStart_ResumeAdoptsFrontierTurn`) and SP-13 (`timeline`'s empty upper bound) all already treat it that way, and the cost of the collision is one suppressed span-narrowing paragraph on a session whose first segment is a single turn. Do not "fix" this with a `-1` sentinel: three merged consumers would break.
- `Unencoded(ctx, s)` — closed segments with `!EncodedOnce`, ascending by `StartTurn`.
- `Current(ctx, s)` — the open segment; none → `core.ErrNotFound`; more than one open (a bug elsewhere) → the highest id plus one `Loud` line.
- `Range(ctx, from, to)` — segments intersecting `[from, to]` ascending by `StartTurn`; an open segment matches whenever `to ≥ StartTurn`.

`FSStore.Segments() SegmentLog` returns the single `*segLog` the store opened over `<Layout.Index>/segments.jsonl`; it is created in `store.Open`, shares the store's lifetime, and returns a log whose every method yields `core.ErrDegraded` after `Close`. It is never nil, so callers do not nil-check it.

### `Search` — backing `recall`

```go
const (
    wPathExact    = 1.00
    wPathSuffix   = 0.70
    wPathContains = 0.45
    wTool         = 0.20
    wTextUnit     = 0.25   // per doubling of occurrences
    wSymbolExact  = 1.00
    wSymbolRefU   = 0.20
    wSymbolRefCap = 0.80
    wRecency      = 0.12
    maxCandidates = 512
    maxScanBytes  = 32 << 20
    maxK          = 100
)
```

Candidates are tool-use records filtered by `Tool` (case-insensitive equality; `""` matches everything), `Since` (`TS ≥ Since.UnixMilli()`; a zero `Since` matches everything) and, when `Path != ""`, by **key match**, which means `k := paths.Key(rec.Path)` satisfies any one of `k == qp`, `strings.HasSuffix(k, "/"+qp)`, or `strings.Contains(k, qp)` — the same three predicates the scoring block below grades, so no candidate can survive the filter and then score zero on the path term. Content is only materialized for `Text`/`Symbol` queries, newest-first, stopping at `maxCandidates` records or `maxScanBytes` decompressed (then `obs.Counter("store.search.truncated")`; no error — a partial ranked answer beats a failure on the retrieval path).

```go
qp := paths.Key(q.Path)                // "" when the query has no path term
score := 0.0
switch {
case qp == "":                          // no path term contributes nothing
case key == qp:                         score += wPathExact
case strings.HasSuffix(key, "/"+qp):    score += wPathSuffix
case strings.Contains(key, qp):         score += wPathContains
}
if q.Tool != "" { score += wTool }
if q.Text != "" {
    occ := countFold(content, q.Text)
    if occ == 0 && score == 0 { continue }
    score += math.Min(1.0, wTextUnit*math.Log2(1+float64(occ)))
}
if q.Symbol != "" {
    if sym, ok := exactSymbol(syms, q.Symbol); ok {
        score += wSymbolExact; span = [2]int64{int64(sym.Offset), int64(sym.Offset + sym.Len)}
    } else if n := s.deps.Symbols.References(content, []string{q.Symbol})[q.Symbol]; n > 0 {
        score += math.Min(wSymbolRefCap, wSymbolRefU*float64(n))
    } else if score == 0 { continue }
}
final := score + wRecency*recencyRank   // recencyRank = 1 - rankByTS/len(candidates) ∈ [0,1]
```

Sort by `final` descending, tiebreak `TS` descending, then `ToolUseID` ascending — total and deterministic, so goldens are stable. `K ≤ 0` becomes 5 (the `recall` default of §8.7); `K > maxK` is clamped. `Hit.Span` is the symbol span for a symbol hit, the first text occurrence widened outward to the enclosing chunk boundaries for a text hit, and `[0, CanonBytes]` otherwise — which is what makes `OpenSpan(root, span[0], span[1]-span[0])` the minimal-sufficient-span primitive SP-13 needs. `Hit.Summary` is the first non-blank line of the span, control characters stripped, truncated to `argsPreviewMax`.

### `Open`, `OpenSpan`, `GetRoot`, `Has`

`Open(ctx, root)` returns a lazy `io.ReadCloser` that decompresses one chunk at a time in `Root.Chunks` order — a 40 MB root never fully materializes. `OpenSpan(ctx, root, off, n)` walks the `ChunkRef` lengths to find the intersecting chunks, decompresses only those, and returns a reader over the trimmed span: `off < 0` → 0; `off ≥ total` → an empty reader with no error; `n ≤ 0` → to the end; `off+n > total` → clamped. `GetRoot` returns `core.ErrNotFound` for an unknown or tombstoned root. `Has(h)` tests `chunkSet` membership without I/O, falling back to `os.Stat` only when the index says no (tolerating an object present from a crashed run whose index line never landed).

### GC — deadline-bounded resumable mark-and-sweep

`GC(ctx, GCPolicy{RetainDays, RetainSessions, DryRun, Deadline})`. Each retention axis is read three ways, and the tri-state is normative because the zero `GCPolicy` is what a careless caller passes: **`0` inherits** `config.Store.Retention` (30 days / 10 sessions), so `GC(ctx, GCPolicy{})` is the safe default-retention run and never a mass deletion; a **positive** value overrides it; a **negative** value means "no window on this axis" and is the only way to force collection — it exists for tests and for `qompack fsck --gc-all` (SP-17). A zero `Deadline` means unbounded. "Whichever is longer" (§8.2) is implemented as a **disjunction**: an index entry is in-window when `RetainDays ≥ 0 && entry.TS ≥ now − RetainDays·24h` **OR** `RetainSessions ≥ 0 && entry.Session` is among the `RetainSessions` most recent sessions in `index/sessions.jsonl`. An **ephemeral** root (`"eph":true`) is never in-window by the age clause; only the session clause and an explicit root reference keep it alive.

**Mark.** The root set, per 00-ARCHITECTURE §5.8, is assembled without importing `checkpoint`, `pins` or `negknow` — those packages import `store`, so the reverse edge would be an import cycle. Instead GC harvests hashes structurally: it reads `checkpoints/MANIFEST.jsonl` for the checkpoint file list and scans each `NNNN.json`, plus `pins/invariants.jsonl` and `records/eliminations.jsonl`, with a streaming `json.Decoder`, collecting **every string token matching `^(?:sha256:)?[0-9a-f]{64}$` anywhere in the document**. The prefix is optional on purpose: `core.Hash.String()` is the canonical text form, but nothing in the schema forces a producer to use it, and a hash serialized bare would otherwise be invisible to the mark phase and its object silently deleted. Any 64-hex string is therefore treated as a live hash. That is a conservative superset of "pointers, pins, evidence and depends_on", which is exactly the right error direction for a collector: over-retention costs disk, under-retention is data loss. Missing files are not an error (waves 3–5 have not shipped them yet). To that set are added every root of an in-window `tool_use` record and every in-window file version.

The live **chunk** set is the union of `Root.Chunks` over live roots, resolved through `rootIndex`. It is written to `.qompack/state/gc-live.bin` as sorted 32-byte hashes so a resumed sweep can binary-search it without redoing the mark.

**Sweep.** `filepath.WalkDir("objects")` in lexicographic order; any object whose hash is absent from the live set is deleted (`DryRun` counts without deleting) and a `{"op":"gc"}` tombstone is appended to `roots.jsonl` for each fully-collected root. Both phases check `ctx.Err()` and the deadline every 256 items; on expiry the state file is written and `GCReport.Truncated = true` is returned.

```json
{"v":1,"phase":"sweep","cursor":"objects/3a/9f/3a9f…zst","live_digest":"sha256:…","roots":8123,"scanned":41200,"deleted":915,"freed":38221008,"started":1734129000000}
```

`live_digest` is `core.HashBytes("qompack.gclive.v1", sortedLiveChunkBytes)`. A resumed run continues from `cursor` **only if** the recomputed live digest matches; otherwise it restarts the mark phase, because new roots may have appeared. `GCReport` is filled cumulatively across resumptions within the same generation. Failure to delete one object (Windows sharing violation) logs at `Debug`, increments `obs.Counter("store.gc.skipped")` and continues — GC never aborts a session.

### `Stats`, `Flush`, and the session index

`Stats.Objects` is `len(chunkSet)`. `Stats.Bytes` is the running sum of compressed object bytes written, persisted in `state/store.json` and recomputed by a full walk when that file is missing. `Stats.RawBytes` is the cumulative `RawBytes` over every `Put` including exact duplicates. `Stats.DedupRatio = RawBytes / Bytes` (0 when `Bytes == 0`) — **this is the number the Phase 1 exit criterion measures**. `ToolUses`, `Segments`, `Files` are index cardinalities; `Sketches` reports store-held sketch counts as `{"signatures": n}` where n is the number of roots carrying a MinHash signature (bloom/cms/hll files belong to the daemon, not to L1's store).

`Flush(ctx)` — the mechanics SP-08's `SessionEnd` and SP-05's idle loop invoke, whose entry points SP-06 does **not** own: (1) durably flush every open append-only handle — `paths.AppendOnly` returns an `io.WriteCloser`, not an `*os.File`, so the store keeps each handle behind a `type syncer interface{ Sync() error }` assertion and calls `Sync()` when it succeeds, skipping silently when it does not (a wrapper that buffers is still correct, just not fsynced, and this must never be the reason `SessionEnd` fails); (2) materialize `index/files.json`; (3) persist the token chunk cache and calibration; (4) persist `state/store.json`; (5) append a session record to `index/sessions.jsonl` for every session with counters changed since its last record (last-wins on load, so append-only is preserved):

```
{"v":1,"s":"sess-7f","start":1734128000000,"end":1734129900000,"turns":312,"tooluses":840,"roots":611,"objects":2044,"bytes":18442112,"rawbytes":92210560,"dedup":5.0}
```

`Close()` runs `Flush`, drains and closes the codec pools, and closes every file handle. `Close` on an already-closed store is a no-op (returns nil). Every method on a closed store that can return an error returns `core.ErrDegraded`; the two that cannot — `Has(h) bool` returns `false`, and `Segments()` returns the same non-nil log whose own methods return `core.ErrDegraded` — are the documented exceptions, because changing their signatures would be a §5.8 amendment.

### Concurrency, and `Open` defaulting

`FSStore` is safe for concurrent use by multiple goroutines — the daemon's worker pool depends on it. One `sync.RWMutex` guards the in-memory indices; each append-only file has its own `sync.Mutex` held only across the `Write` (so a large object write never blocks an index append); object writes take no lock at all, being content-addressed with an atomic rename.

```go
func Open(root string, cfg config.Config, deps Deps) (Store, error) {
    l := paths.Of(root)                                        // root is the PROJECT root
    if deps.Log == nil     { deps.Log = logging.Nop() }
    if deps.Clock == nil   { deps.Clock = core.SystemClock() }
    if deps.Metrics == nil { deps.Metrics = obs.New(deps.Clock) }  // there is no obs.Nop()
    if deps.Chunker == nil { deps.Chunker = chunk.New(chunk.Params{
        Min: cfg.Store.Chunk.Min, Target: cfg.Store.Chunk.Target, Max: cfg.Store.Chunk.Max}) }
    if deps.Canon == nil   { deps.Canon = canon.Default(cfg.Store.Canonicalize) }
    if deps.Symbols == nil { deps.Symbols = symbols.New() }
    if deps.Tokens == nil  { deps.Tokens = tokens.NewExact(cfg, tokens.DefaultCalibPath(),
        filepath.Join(l.State, "chunktokens.bin")) }
    if deps.Redact == nil  { deps.Redact = redact.New(cfg) }   // NEVER redact.Nop()
    if err := paths.EnsureLayout(l); err != nil { return nil, err }
    if err := os.MkdirAll(filepath.Join(l.Tmp, "quarantine"), 0o755); err != nil { return nil, err }
    …
}
```

`paths.EnsureLayout` creates the whole `.qompack/` tree (including `objects/`, `index/`, `state/`, `tmp/`) and writes the self-ignoring `.qompack/.gitignore`; the store adds only `tmp/quarantine/`, which is store-specific and not in the SP-01 layout. It then loads the five indices (`roots`, `tool_use`, `files`, `segments`, `sessions`). A missing `.qompack/` is created; an unreadable one returns an error (the daemon then refuses to start and the client spools, per §12.3).

### Performance budgets

| What | Budget | Rationale |
|---|---|---|
| `PutBytes` of 100 KB into an **empty** store, stub canon (`BenchmarkPutBytes_100KB_Cold`) | **≤ 3 ms** | B-C `l0_process` p99 < 50 ms with headroom for canon+chunk+DAG |
| `PutBytes` of 100 KB, all chunks already present (`BenchmarkPutBytes_100KB_Warm`) | **≤ 400 µs** | the four-reads-of-one-file case of §8.2 |
| `GetChunk` warm | **≤ 60 µs** | `expand` inside B-F p95 < 250 ms |
| `OpenSpan` 4 KB out of a 4 MB root | **≤ 150 µs** | minimal-span default of §8.7 |
| `Search` over 1 000 roots / 8 MB | **≤ 25 ms** | `recall` inside B-F |
| `EstimateRoot` over 64 cached chunks | **≤ 5 µs** | called on every `Put` |
| `Redact` over 100 KB | **≤ 2 ms** | on the ingest path |
| `MarkEncoded` over 100 segments | **≤ 1 ms** | inside B-E (`PreCompact` p99 < 2 s) |
| `Open` over 50 000 roots | **≤ 400 ms** | daemon start, off the hot path |
| `GC` mark+sweep over 50 000 objects | **≤ 2 s** with `Deadline` honoured to ±50 ms | idle work, resumable |
| `Stats.DedupRatio` on the read-heavy fixture | **≥ 4.0** | §10 Phase 1 exit criterion |

---

## Test plan (TDD)

Every test below is written and run (failing) before the implementation in its commit. `testify/require` only; `assert` is banned. Every test that touches time takes `testutil.FakeClock`. Every test constructs its store through `testutil.NewProject(t)`.

### Fixtures to create

- `testdata/corpora/secrets/*.txt` — 12 files, one per rule plus two mixed and one adversarial (`«redacted:jwt»` already present, a `FOO=1` line, a `PORT=8080` line).
- `testdata/corpora/toolout/sp06/fileread-auth-v1.txt` … `-v4.txt` — one 24 KB TypeScript file in four versions differing by 2, 18 and 240 lines; the four-reads-of-one-file case of §8.2. **The `sp06/` subdirectory is deliberate**: 00-ARCHITECTURE §3.1 assigns `testdata/corpora/toolout/` to the canonicalizer goldens, which are SP-04's and land in the same wave; nesting keeps two parallel branches from colliding on one directory listing.
- `testdata/corpora/media/{tiny.png,tiny.jpg,tiny.gif,tiny.webp,two-page-text.pdf,one-page-scanned.pdf}` — all < 12 KB, generated by a committed `tools/devtool gen-fixtures` step, not vendored blobs.
- `testdata/golden/store/roots.jsonl`, `tool_use.jsonl`, `files.json`, `segments.jsonl`, `sessions.jsonl` — byte-for-byte goldens produced by `TestGolden_IndexFormats`.
- `testdata/golden/contracts/{chunk,canon,symbols,sketch}/` — consumed, not produced (SP-01 ships them; Rule W-2).

### `internal/redact`

| Test | Setup / input | Expected |
|---|---|---|
| `TestRedact_PEMBlock` | a 1 674-byte RSA PEM block embedded between two prose paragraphs | exactly one `Match{Rule:"pem_private_key"}`; output contains `«redacted:pem_private_key»` and neither `BEGIN RSA` nor `END RSA` |
| `TestRedact_AWSKeys` | `@@SEC_AWS_AKID@@` and `ASIA-example-key-id` in one line | 2 matches, rule `aws_access_key_id`; surrounding text byte-identical |
| `TestRedact_GitHubTokens` | `ghp_` + 36 chars, `gho_` + 36, `github_pat_` + 30 | 3 matches, rule `github_token` |
| `TestRedact_AnthropicBeforeGeneric` | `@@SEC_ANTHROPIC_AB@@` | 1 match with rule `anthropic_key`, **not** `generic_sk_key` |
| `TestRedact_JWT` | a three-part `eyJ…` token | 1 match, rule `jwt` |
| `TestRedact_BearerValueOnly` | `Authorization: Bearer abcdefghijklmnopqrstuvwx` | output retains the literal `Bearer ` prefix; only the value is replaced |
| `TestRedact_CredentialedURI` | `postgres://app:h4nter2@db.internal:5432/prod` | output is `postgres://app:«redacted:credentialed_uri»@db.internal:5432/prod` exactly |
| `TestRedact_AssignmentValueOnly` | `api_key = "swordfishswordfish"` and `PASSWORD: hunter22` | 2 matches; the key names survive verbatim |
| `TestRedact_DotenvGatedByKeyName` | `PORT=8080`, `FOO=1`, `DATABASE_URL=postgres://u:pw@h/db`, `STRIPE_SECRET_KEY=@@SEC_STRIPE@@` | `PORT` and `FOO` untouched; the other two redacted |
| `TestRedact_Idempotent` | every corpus file, `Redact(Redact(x))` | byte-equal to `Redact(x)`; second call returns 0 matches |
| `TestRedact_NeverWholeInputExceptPEM` | each corpus file | for every non-PEM-only input, no `Match` has `Offset==0 && Len==len(in)` |
| `TestRedact_BoundedGrowth` | every corpus file | `len(out) ≤ 2*len(in)+64` |
| `TestRedact_Disabled` | `runtime.redact.enabled=false` | output is the input slice, `Rules()` empty |
| `TestRedact_InvalidUserPattern` | `runtime.redact.patterns=["(("]` | `New` succeeds, one `Loud` line, built-ins still active |
| `TestRedact_UserPattern` | pattern `INTERNAL-[0-9]{6}` over `case INTERNAL-004213` | 1 match, rule `custom:0` |
| `TestRedact_RejectsZeroWidthUserPattern` | `runtime.redact.patterns=["x*"]` | pattern rejected at `New`, one `Loud` line, `redact.pattern_rejected == 1`; `Redact` over 4 KB of prose returns the input unchanged |
| `TestRedact_RejectsNarrowUserPattern` | `runtime.redact.patterns=["[0-9]"]` | rejected; a single digit is never replaced |
| `TestRedact_GrowthBoundHoldsUnderHostilePattern` | an *admitted* 3-byte-wide pattern `[0-9]{3}` over 4 KB of digits | `len(out) ≤ 13*len(in)+37` |
| `FuzzRedactIdempotent` | seeds = the corpus | `Redact(Redact(x)) == Redact(x)`; never panics; non-empty in → non-empty out |
| `TestRedact_Deterministic` (rapid) | random byte strings, 200 runs | two `Redact` calls on the same input produce identical output and identical `[]Match` |

### `internal/tokens`

| Test | Setup / input | Expected |
|---|---|---|
| `TestClassify_All` | one sample per class **with `path == ""`** (PNG magic, `%PDF-`, NUL-heavy blob, `{"a":1}`, `diff --git`, `.go` source, plain prose) | the seven expected `Class` values; the PNG case is the one rule SP-06 adds to SP-01's `Classify` |
| `TestClassify_SP01TableStillPasses` | SP-01's `TestClassify_Table` 14 cases, re-run | unchanged results — the inserted image-magic rule must not perturb any of them |
| `TestUnits_Deterministic` | the 4 KB TypeScript fixture | `units` returns a fixed golden integer; identical across two calls |
| `TestEstimate_ProseVsCode` | the same 10 KB payload classified prose then code | code estimate > prose estimate; both within `[len/5, len/2]` |
| `TestEstimateImage_PNG` | a 1 024×768 PNG | `ceil(1024*768/750) = 1049` tokens |
| `TestEstimateImage_Downscale` | a 4 000×3 000 PNG | scale = 1568/4000 = 0.392; `ceil(1568*1176/750) = 2459` → clamped to **1600** |
| `TestEstimateImage_JPEG_GIF_WebP` | the three fixtures | dimensions parsed correctly, `ok == true` |
| `TestEstimateImage_Garbage` | 64 random bytes with a PNG magic prefix | `(0, false)` |
| `TestEstimatePDF_TextPDF` | `two-page-text.pdf` (≈ 900 words) | pages = 2; result within ±15 % of `units(text)*0.92`; **not** 2 000, which is the flat rate §2.2 indicts, and **not** `2*PDFTokensPerPage` |
| `TestEstimatePDF_ScannedPDF` | `one-page-scanned.pdf` | `cfg.Runtime.Tokens.PDFTokensPerPage` (1 800 at defaults); asserted against the config value, never the literal |
| `TestEstimateRoot_CacheHit` | `NoteChunk` for 8 chunks, then `EstimateRoot` over the same refs | sum equals the sum of `NoteChunk` returns; `tokens.chunk_miss == 0` |
| `TestEstimateRoot_CacheMiss` | `EstimateRoot` over 8 unseen refs of 4 096 bytes, `ClassCode` | `8*ceil(4096/CodeCharsPerToken)` = `8*ceil(4096/3.6)` = `8*1138` = **9 104**; `tokens.chunk_miss == 8` |
| `TestEstimateRoot_ClassIndependentCache` | `NoteChunk(h, ClassProse, b)` then `EstimateRoot([h], ClassCode)` | the cached `units` is reused (`chunk_miss == 0`) and the result is `round(units*unitWeight[ClassCode])` — proving the memo key is `core.Hash` alone, as SP-01 §10 requires |
| `TestEstimateRoot_SP01BaselineStillPasses` | SP-01's `TestEstimateRoot_SumsChunks`: 3 refs of 1 000 bytes, `ClassProse`, empty cache | `750` — the miss path is byte-for-byte SP-01's baseline formula |
| `TestChunkCache_Persists` | `NoteChunk` ×100, `Close`, re-`NewExact` | all 100 hits reload; `chunktokens.bin` header is `QPKT` + version 1; records are 36 bytes |
| `TestChunkCache_CorruptFile` | truncate `chunktokens.bin` mid-record | `NewExact` succeeds, loads the intact prefix, one `Loud` line |
| `TestCalibrate_ClampAndThreshold` | 4 samples of ratio 3.0 then a 5th | `Factor()==1.0` after 4 (warm-up); after the 5th it is `CalibrationMax` (1.6), clamped |
| `TestCalibrate_LowClamp` | 10 samples of ratio 0.2 | `Factor() == CalibrationMin` (0.6) — the EWMA is seeded with the first ratio, so it sits at 0.2 from sample 1 and only the ≥5-sample warm-up delays the clamp |
| `TestCalibrate_ReadsConfigNotLiterals` | `runtime.tokens.{calibrationMin,calibrationMax,calibrationAlpha}` overridden to `0.8/1.2/0.5` | the clamp and the step both follow the override; no literal 0.6/1.6/0.2 anywhere in `internal/tokens` |
| `TestCalibrate_ReadsSP01FlatFile` | a hand-written flat `{"9f2a1c04bb7e":1.25}` calibration.json | `NewExact` loads `factor=1.25`; the next save rewrites the versioned shape |
| `TestCalibrate_Persists` | calibrate, `Close`, reopen with the same project root | factor survives; JSON matches the documented shape |
| `TestCalibrate_IgnoresZero` | `Calibrate(0, 100)`, `Calibrate(100, 0)` | no state change |
| `BenchmarkEstimateRoot_64Cached` | 64 cached refs | **≤ 5 µs/op** |
| `BenchmarkUnits_100KB` | 100 KB of code | ≤ 500 µs/op, 0 allocs |

### `internal/store` — objects and roots

| Test | Setup / input | Expected |
|---|---|---|
| `TestPutBytes_FanoutLayout` | 64 KB of prose | for every chunk, `objects/<h[0:2]>/<h[2:4]>/<h>.zst` exists; the two directory names are the first four hex chars |
| `TestPutBytes_CompressionNone` | `store.compression="none"` | objects have no `.zst` suffix and are byte-identical to the plaintext chunks |
| `TestPutBytes_GlobalDedup` | the four `fileread-auth-v*.txt` versions, four `PutBytes` calls | 4 distinct roots; `Novel` strictly decreasing after the first; total distinct objects < 1.6× the objects of v1 alone |
| `TestPutBytes_IdenticalRootIsFree` | the same payload twice | second call: `Novel==0`, `Reused==len(chunks)`, `roots.jsonl` gains no line |
| `TestPutBytes_DedupHitReportsThisPutsRawBytes` | a fake canon registry that strips a timestamp line, then two payloads differing **only** in that line (so both canonicalize to one root, with different raw sizes) | both calls report their **own** `Root.RawBytes`; `Stats.RawBytes` is the sum of the two input sizes, not twice the first — the regression that would silently understate `DedupRatio` |
| `TestPutBytes_KeepRawStoresDeltaRoot` | a canon registry emitting 3 deltas, `PutOptions{KeepRaw:true}` | `roots.jsonl` gains a second line with `"tool":"«deltas»"`; `canon.Restore(canonical, decodedDeltas)` reproduces the redacted input byte-for-byte; `Stats.RawBytes` is unchanged by the delta root while `Stats.Bytes` grows |
| `TestPutBytes_KeepRawFalseStoresNoDeltas` | the same canon registry, `KeepRaw:false` | exactly one root line, no `"deltas"` key |
| `TestPutBytes_EphemeralFlagPersists` | `PutOptions{Ephemeral:true}`, `Close`, reopen | the root line carries `"eph":true` and the reloaded index reports it |
| `TestPutBytes_RedactionBeforeChunking` | a payload containing `@@SEC_AWS_AKID@@` | `PutResult.Redacted==1`; **no object file anywhere under `objects/` contains the literal `AKIA`** (assert by decompressing every object) |
| `TestPutBytes_RedactionRunsWhenDepsRedactIsNil` | `Deps{Redact:nil}` | the same assertion holds — nil never means "no redaction" |
| `TestPutBytes_CanonicalizeBeforeChunk` | a fake canon registry that uppercases | stored chunks are uppercase; `Root.CanonBytes == len(upper)`, `RawBytes == len(input)` |
| `TestPutBytes_CanonFailureFallsBack` | a canon registry returning an error | `Put` succeeds, stores the redacted bytes, logs at `Warn` |
| `TestPut_ReaderTruncation` | an `io.Reader` of `MaxPutBytes+1024` bytes | `Truncated==true`, no error, `RawBytes==MaxPutBytes` |
| `TestPutBytes_NearDup` | v1 then v2 of the fixture with MinHash enabled at threshold 0.9 | `NearDup != nil`, `Jaccard ≥ 0.9`, `PriorRoot` = v1's root |
| `TestPutBytes_NoNearDupForDistinctPaths` | two unrelated payloads on different paths | `NearDup == nil` |
| `TestGetChunk_Roundtrip` | put then get every chunk | bytes equal, lengths equal |
| `TestGetChunk_QuarantinesCorruption` | flip one byte inside an object file | `GetChunk` returns `core.ErrNotFound`; the file moved to `tmp/quarantine/`; one `Loud` line; `store.quarantined == 1` |
| `TestOpen_StreamsFullRoot` | a 4 MB root | `io.ReadAll(Open(...))` equals the canonical bytes |
| `TestOpenSpan_Boundaries` | a 4 MB root; spans `(0,10)`, `(mid,4096)`, `(total-5,100)`, `(total,10)`, `(-5,10)`, `(0,-1)` | exact expected slices; `(total,10)` is empty with no error; `(0,-1)` is the whole root |
| `TestHas_NoIO` | put, then `Has` for a known and an unknown hash | true / false; no file opened for the true case (asserted with a counter) |
| `TestOpenStore_LoadsIndex` | put 50 roots, `Close`, reopen | `Stats.Objects` and `GetRoot` for all 50 match pre-close values |
| `TestOpenStore_TruncatedFinalLine` | append half a JSON line to `roots.jsonl`, reopen | opens successfully; `store.index.badline == 1`; all prior roots present |
| `TestClosedStoreErrors` | `Close` then every method | all return `core.ErrDegraded`; second `Close` is nil |
| `TestConcurrentPut` (`-race`) | 8 goroutines × 50 `PutBytes` with 20 % overlapping payloads | no race; total distinct objects equals the single-threaded result |
| `BenchmarkPutBytes_100KB_Cold` | 100 KB, empty store | **≤ 3 ms/op** |
| `BenchmarkPutBytes_100KB_Warm` | same payload repeated | **≤ 400 µs/op** |
| `BenchmarkGetChunk` | 4 KB chunk | **≤ 60 µs/op** |
| `BenchmarkOpenSpan_4KB_of_4MB` | — | **≤ 150 µs/op** |
| `BenchmarkOpenStore_50kRoots` | pre-built index | **≤ 400 ms/op** |

### `internal/store` — indices

| Test | Setup / input | Expected |
|---|---|---|
| `TestRecordToolUse_Roundtrip` | one record with every field set | `ToolUse` returns it field-for-field |
| `TestRecordToolUse_IdempotentReplay` | the same record twice | one line in `tool_use.jsonl`, no error |
| `TestRecordToolUse_ConflictingReplay` | same ID, different `Root` | `core.ErrAppendOnly` |
| `TestToolUsesByPath_Ordering` | 5 records on one path, limit 3 | newest-first, exactly 3 |
| `TestMarkSuperseded_AppendOnly` | mark, then reopen the store | `Status==StatusSuperseded`, `SupersededBy` set; `tool_use.jsonl` contains the original line **unmodified** plus one `"op":"supersede"` line |
| `TestMarkSuperseded_Unknown` | unknown ids | `core.ErrNotFound` |
| `TestArgsDigest_KeyOrderInvariant` | `{"a":1,"b":2}` vs `{"b":2,"a":1}` | identical hash; preview identical |
| `TestArgsDigest_Preview` | `{"file_path":"src/auth.ts","limit":200}` | preview `src/auth.ts`; a 400-char command truncates to 120 bytes ending in `…` on a rune boundary |
| `TestAppendFileVersion_History` | 3 versions of one path | `FileHistory` ascending by ts; `files.jsonl` has 3 lines |
| `TestAppendFileVersion_Idempotent` | same root + turn twice | one line |
| `TestFileAt` | versions at t=100,200,300; query at 250 | the t=200 version; query at 50 → `core.ErrNotFound` |
| `TestChangedSince_Changed` | dep hash = v1 root, store now at v2 | the dep is returned |
| `TestChangedSince_Unchanged` | dep hash = the newest root | empty result |
| `TestChangedSince_UnknownPath` | dep on a path never stored | empty result; `store.changed_since.unknown_path == 1` |
| `TestChangedSince_KeyNormalization` | dep path `.\SRC\Auth.TS` on Windows, stored as `src/auth.ts` | matched (via `paths.Key`) |
| `TestChangedSince_PreservesInputOrder` | 5 deps, 2 changed at indices 1 and 3 | returned in input order with input `Dep` values |
| `TestFilesJSON_MaterializedByFlush` | 2 paths × 2 versions, `Flush` | `index/files.json` matches the golden byte-for-byte; paths sorted, versions ascending |
| `TestGolden_IndexFormats` | a scripted 6-operation session with `FakeClock` | `roots.jsonl`, `tool_use.jsonl`, `segments.jsonl`, `files.json`, `sessions.jsonl` byte-identical to the goldens |

### `internal/store` — segment log (`storetest.RunSegmentLogSuite`, all skips removed)

| Test | Setup / input | Expected |
|---|---|---|
| `TestSegment_OpenAllocatesMonotonicIDs` | 3 `Open` calls with `ID:0` | 1, 2, 3 |
| `TestSegment_OpenRejectsExistingID` | `Open` with `ID:1` after 1 exists | `ErrSegmentExists` |
| `TestSegment_CloseSetsEndTurnAndFeatures` | close id 1 at turn 57 with 4 features | `Get` reflects both; `Closed==true` |
| `TestSegment_CloseIdempotent` / `_Conflict` | same endTurn / different endTurn | nil / `core.ErrAppendOnly` |
| `TestSegment_MarkEncodedSetsFlag` | close then `MarkEncoded([1], 7)` | `EncodedOnce==true`, `CheckpointSeq==7` |
| `TestSegment_MarkEncodedIdempotentSameSeq` | mark twice with seq 7 | nil both times; one `"op":"encode"` line |
| **`TestSegment_MarkEncodedRefusesDifferentSeq`** | mark with 7, then with 8 | `errors.Is(err, core.ErrAlreadyEncoded)`; **this is the DPI guard of §4.6** |
| `TestSegment_MarkEncodedBatchIsAllOrNothing` | ids `[2,3]` where 3 is already encoded at another seq | error; segment 2 remains unencoded; `segments.jsonl` gains no line |
| `TestSegment_MarkEncodedRejectsOpen` | mark an open segment | `ErrSegmentOpen` |
| `TestSegment_FrontierIsContiguous` | segments 1–4 with 1, 2 and 4 encoded | frontier = `EndTurn` of segment 2, not of 4 |
| `TestSegment_FrontierZeroWhenNoneEncoded` | 3 closed, 0 encoded | 0 |
| `TestSegment_Unencoded` | 4 segments, 2 encoded, 1 open | the 1 closed-unencoded segment only |
| `TestSegment_Current` | one open | that one; after closing → `core.ErrNotFound` |
| `TestSegment_Range` | segments [0,10],[11,20],[21,-] open | `Range(5,15)` = the first two; `Range(25,30)` = the open one |
| `TestSegment_BloomRefParsed` | hand-append a `{"op":"bloom"}` line, reopen | `Segment.BloomRef == "sketches/seg-0012.bloom"` (SP-16's reservation) |
| `TestSegment_SurvivesReopen` | full lifecycle, `Close`, reopen | every field round-trips |
| `BenchmarkMarkEncoded_100` | 100 closed segments | **≤ 1 ms/op** |

### `internal/store` — search, stats, GC, flush

| Test | Setup / input | Expected |
|---|---|---|
| `TestSearch_ByPathExactBeatsSuffix` | roots on `src/auth.ts` and `web/src/auth.ts`, query `src/auth.ts` | the exact key ranks first |
| `TestSearch_ByText` | 3 roots, the query term appearing 1, 4 and 0 times | 2 hits, the 4-occurrence root first, spans widened to chunk boundaries |
| `TestSearch_BySymbol` | a fake `symbols.Extractor` returning `refreshToken` at offset 812 len 240 | `Span == [812, 1052]`; score includes `wSymbolExact` |
| `TestSearch_ToolFilterAndSince` | 6 roots across 2 tools and 2 timestamps | only matching records returned |
| `TestSearch_DefaultKIsFive` | 20 matching roots, `K:0` | 5 hits |
| `TestSearch_Deterministic` | run the same query 20 times | identical ordering every time |
| `TestSearch_TruncatesWithoutError` | 600 candidate roots | no error, `store.search.truncated == 1`, hits still ranked |
| `TestSearch_SummaryIsFirstNonBlankLine` | a payload starting with two blank lines | summary equals line 3, ≤ 120 bytes |
| `BenchmarkSearch_1000Roots` | 1 000 roots / 8 MB | **≤ 25 ms/op** |
| `TestStats_DedupRatio` | the four fixture versions each read 4 times (16 puts) | `RawBytes` = 16 × file size; `DedupRatio ≥ 4.0` |
| **`TestPhase1ExitCriterion_ReadHeavy`** | the read-heavy corpus: 40 reads across 10 files with edits between | `Stats.DedupRatio ≥ 4.0` — §10 Phase 1, quoted verbatim in the test doc comment |
| `TestStats_SublinearGrowth` | 200 puts of a 100 KB payload mutated 1 % each time | `Bytes` after 200 puts < 25 × `Bytes` after 8 puts (§11.3 sublinear guardrail) |
| `TestGC_CollectsUnreferenced` | 10 roots, 2 referenced by a fake checkpoint JSON, `GCPolicy{RetainDays:-1, RetainSessions:-1}` | the other 8 roots' exclusive chunks deleted; the 2 survive |
| `TestGC_ZeroPolicyInheritsConfigAndDeletesNothing` | the same 10 roots written "now", `GCPolicy{}` | `DeletedObjects == 0` — the zero policy is default retention (30/10), never a mass deletion |
| `TestGC_RetentionIsWhicheverIsLonger` | a root 60 days old but in the most recent session, `RetainDays:30, RetainSessions:10` | retained |
| `TestGC_EphemeralNotInWindowByAge` | one ephemeral and one ordinary root, both written "now", `RetainDays:30, RetainSessions:-1`, no explicit root references | the ordinary root survives; the ephemeral root's exclusive chunks are collected |
| `TestGC_HarvestsBareHexHash` | a checkpoint JSON carrying a hash as bare 64-hex with no `sha256:` prefix | that root survives |
| `TestGC_HarvestsHashesFromCheckpointPinsEliminations` | hand-written `checkpoints/0001.json`, `pins/invariants.jsonl`, `records/eliminations.jsonl` each containing one `sha256:` string | all three roots survive; no import of `checkpoint`/`pins`/`negknow` (asserted by the import-graph test) |
| `TestGC_MissingRootFilesAreNotAnError` | no checkpoints/pins/eliminations on disk | GC succeeds |
| `TestGC_DryRun` | — | `DeletedObjects` > 0 reported, zero files removed |
| `TestGC_DeadlineTruncatesAndResumes` | 5 000 objects, `Deadline: 5ms` | `Truncated==true`; `state/gc.json` written with a cursor; a second run with a generous deadline completes and the union of both reports equals a single unbounded run |
| `TestGC_LiveDigestMismatchRestartsMark` | truncate, `PutBytes` a new root, resume | the mark phase restarts; the new root is never collected |
| `TestGC_TombstonesRootsAppendOnly` | collect 3 roots, reopen | `GetRoot` → `core.ErrNotFound`; the original root lines are still present in `roots.jsonl` alongside 3 `"op":"gc"` lines |
| `TestGC_ContextCancel` | cancel mid-sweep | returns `ctx.Err()` with a partially-filled report; no corruption on reopen |
| `BenchmarkGC_50kObjects` | — | **≤ 2 s/op**; deadline honoured to ±50 ms |
| `TestFlush_WritesSessionIndex` | 2 sessions, `Flush` after each | 2 lines in `sessions.jsonl`, dedup ratio present, last-wins on re-flush |
| `TestFlush_Idempotent` | `Flush` twice with no work between | the second appends nothing |
| `TestAppendOnlyGuard_StoreFiles` | attempt truncation of every `*.jsonl` this package writes | every attempt fails (`testutil.Project.AssertAppendOnly`) |

### Property tests (`pgregory.net/rapid`)

- `PropPutGetRoundtrip` — random payloads 0–256 KB with random class hints: `io.ReadAll(Open(root))` equals the canonicalized-and-redacted bytes, always.
- `PropOpenSpanMatchesSlice` — random `(off, n)` against a random root: `OpenSpan` equals `canonical[clamp(off):clamp(off+n)]`.
- `PropDedupMonotone` — for random payload pairs, the object count after putting both is ≤ the sum of putting each alone.
- `PropRedactIdempotent` — random inputs built from a secret-fragment alphabet: `Redact(Redact(x)) == Redact(x)`.
- `PropChangedSinceIsExactlyHashInequality` — random dep sets: the result is exactly the set of deps with a recorded, different newest root.
- `PropMarkEncodedNeverDowngrades` — random `MarkEncoded` sequences: once `EncodedOnce` is true, `CheckpointSeq` never changes and no call ever clears the flag.

### e2e (`test/e2e`)

- `TestE2E_StoreSurvivesProcessRestart` — build the real binary, run an in-process ingest of 200 payloads, kill and reopen: every root, tool-use, file version and segment is intact and `Stats` matches.
- `TestE2E_SecretNeverLandsInObjects` — ingest a payload containing all ten secret families through the store; walk `objects/`, decompress everything, assert none of the ten literals appears anywhere. This is §13 invariant 7 tested end to end.

---

## Commit plan

Work happens on `feat/sp06-content-addressed-store`, cut from `develop` **after** SP-01 has merged. Exactly 8 commits. Each commit compiles, and `go run ./tools/devtool test` passes for the packages it touches before it is made. Conventional Commits per 00-ARCHITECTURE §10, footer `Refs:` naming the subplan, gaps and design sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(redact): built-in secret rules applied before anything enters the store`

- [ ] `git checkout develop && git pull && git checkout -b feat/sp06-content-addressed-store`
- [ ] Add fixtures `testdata/corpora/secrets/*.txt` (12 files listed in the fixture table).
- [ ] Write `internal/redact/redact_test.go` and `fuzz_test.go` with all 21 rows of the `internal/redact` test table (20 tests + `FuzzRedactIdempotent`). Run `go test ./internal/redact/...` — **every behavioural test must fail** against the SP-01 stub.
- [ ] Implement `internal/redact/rules.go` (the ten-rule table, user-pattern compilation **and the three admission rules**, `Loud` + `redact.pattern_rejected` on rejection) and `internal/redact/redact.go` (interval set, placeholder guard, the `e-s < 3` skip, splice, `New`, `Nop`, `Rules`).
- [ ] Remove the `t.Skip` lines from `internal/redact/redacttest`.
- [ ] Run: `go test ./internal/redact/... -race`, `go test -run=XXX -fuzz=FuzzRedactIdempotent -fuzztime=60s ./internal/redact`, `go run ./tools/devtool lint`.
- Files: `internal/redact/{redact.go,rules.go,redact_test.go,fuzz_test.go}`, `internal/redact/redacttest/*.go`, `testdata/corpora/secrets/*`.
- Footer: `Refs: SP-06, §13 invariant 7, ARCH §5.22a`

### Commit 2 — `feat(tokens): exact chunk-level accounting, media sizing and per-project calibration`

- [ ] Add `testdata/corpora/media/*` via the `gen-fixtures` devtool task.
- [ ] Write `internal/tokens/{exact_test.go,media_test.go,calibrate_test.go,chunkcache_test.go}` with all 22 tokens tests plus the two benchmarks. Run — **all fail**.
- [ ] Implement `exact.go` (`units`, `unitWeight`, `Estimate`, `EstimateString`, `EstimateRoot`, `NoteChunk`, `ChunkSink`), `media.go` (PNG/JPEG/GIF/WebP/PDF), `calibrate.go`, `chunkcache.go`, `NewExact`, `DefaultCalibPath`. Every numeric constant that has a `runtime.tokens.*` key is read from `cfg`; the only new literals are `unitWeight` and the 1568 px clamp.
- [ ] Edit — do not rewrite — SP-01's `classify.go`: insert the single image-magic rule immediately before the `%PDF` check. Nothing else in `Classify` changes.
- [ ] Keep `tokens.New(cfg, calibPath)` delegating to `NewExact(cfg, calibPath, "")` so SP-01's call sites keep compiling.
- [ ] Re-run SP-01's own `internal/tokens` tests. `TestClassify_Table`, `TestEstimate_ImageFromDimensions`, `TestEstimate_ImageCappedAt1600`, `TestEstimate_PDFPageCount`, `TestEstimateRoot_SumsChunks` and `TestCalibrate_ClampsAndPersists` **must still pass unmodified** — if one fails, the exact estimator is wrong, not the test. `TestEstimate_ProseVsCode` asserts the byte-ratio numbers the unit scanner replaces and is the **one** SP-01 test this commit rewrites; note the rewrite in the commit body.
- [ ] Remove the `t.Skip` lines from `tokenstest`.
- [ ] Run: `go test ./internal/tokens/... -race -count=2`, `go test -bench=. ./internal/tokens`, `go run ./tools/devtool lint`.
- Files: `internal/tokens/{classify.go (one-rule edit),exact.go,media.go,calibrate.go,chunkcache.go,*_test.go}`, `internal/tokens/tokenstest/*.go`, `tools/devtool/genfixtures.go`, `testdata/corpora/media/*`.
- Footer: `Refs: SP-06, G10.2, §8.2, ARCH §5.20`

### Commit 3 — `feat(store): content-addressed zstd object layer with two-level fanout`

- [ ] Write `internal/store/{store_test.go,objects_test.go,roots_test.go,put_test.go,read_test.go}` covering the 25 object/root tests and 5 benchmarks, plus `PropPutGetRoundtrip`, `PropOpenSpanMatchesSlice`, `PropDedupMonotone`. Add the `testdata/corpora/toolout/sp06/fileread-auth-v*.txt` fixtures **here**, not in commit 6 — `TestPutBytes_GlobalDedup` and `TestPutBytes_NearDup` are in this commit and consume them. Run — **all fail**.
- [ ] Implement `store.go` (`FSStore`, `Open` over `paths.Of(root)` with full `Deps` defaulting, locking, `Segments()`, `Close`), `objects.go` (codec pool, `putObject`, `getObject`, quarantine, Windows retry), `roots.go` (line marshaller with the pinned `class` ordinals, `appendRoot`, loader, `rootIndex`/`chunkSet`/`refs`/`byPath`), `put.go` (the redact→canon→chunk pipeline, `nearDup`, `putDeltas`, `countRaw`), `read.go` (`GetChunk`, `GetRoot`, `Open`, `OpenSpan`, `Has`).
- [ ] Add `github.com/klauspost/compress` to `go.mod` (already on the §2.5 allowed list; no policy change).
- [ ] Run: `go test ./internal/store/... -race`, `go test -bench=. ./internal/store`, `go run ./tools/devtool lint vet`.
- Files: `internal/store/{store.go,objects.go,roots.go,put.go,read.go}` + their tests, `testdata/corpora/toolout/sp06/*`, `go.mod`, `go.sum`.
- Footer: `Refs: SP-06, G3.1, §8.2, §6.1, ARCH §5.8`

### Commit 4 — `feat(store): tool_use index, file version history and ChangedSince`

- [ ] Write `internal/store/{tooluse_test.go,files_test.go}` with the 19 index tests plus `PropChangedSinceIsExactlyHashInequality`. Run — **all fail**.
- [ ] Implement `tooluse.go` (`RecordToolUse`, `ToolUse`, `ToolUsesByPath`, `MarkSuperseded` as an appended mutation record, `ArgsDigest`) and `files.go` (`AppendFileVersion`, `FileHistory`, `FileAt`, `ChangedSince` with the three normative rules, `materializeFilesJSON`).
- [ ] Run: `go test ./internal/store/... -race -count=2`.
- Files: `internal/store/{tooluse.go,files.go,tooluse_test.go,files_test.go}`.
- Footer: `Refs: SP-06, §8.2, §8.3 items 2-3, ARCH §5.8`

### Commit 5 — `feat(store): segment log with the encoded-once DPI guard`

- [ ] Write `internal/store/segments_test.go` with the 17 segment tests, `PropMarkEncodedNeverDowngrades`, and `BenchmarkMarkEncoded_100`. Run — **all fail**.
- [ ] Implement `segments.go`: the four record shapes, id allocation, `Open`/`Close`/`Get`/`Range`/`Current`/`Unencoded`/`Frontier`, and `MarkEncoded` with batch pre-validation.
- [ ] Fill in `storetest.RunSegmentLogSuite` and delete every `t.Skip` in it.
- [ ] Run: `go test ./internal/store/... -race`, `go test -bench=MarkEncoded ./internal/store`.
- Files: `internal/store/{segments.go,segments_test.go}`, `internal/store/storetest/segmentlog.go`.
- Footer: `Refs: SP-06, G2.1, §4.6, §8.2, ARCH §5.8`

### Commit 6 — `feat(store): search, stats, flush and the session index`

- [ ] Write `internal/store/{search_test.go,stats_test.go,flush_test.go}` with the 8 search tests, the 3 stats tests including `TestPhase1ExitCriterion_ReadHeavy`, the 2 flush tests plus `TestAppendOnlyGuard_StoreFiles`, and `BenchmarkSearch_1000Roots`. The `testdata/corpora/toolout/sp06/fileread-auth-v*.txt` fixtures already landed in commit 3; the read-heavy corpus for `TestPhase1ExitCriterion_ReadHeavy` (10 files, 40 reads with edits between) is generated in-test from them, not committed. Run — **all fail**.
- [ ] Implement `search.go` (weights, candidate assembly, scoring, span resolution, summary), `stats.go` (`Stats`, `DedupRatio`, `ApproxRefs`, `state/store.json`), `flush.go` (`Flush`, `index/sessions.jsonl`).
- [ ] Run: `go test ./internal/store/... -race`, `go test -bench=Search ./internal/store`.
- Files: `internal/store/{search.go,stats.go,flush.go}` + tests.
- Footer: `Refs: SP-06, §8.7, §10 Phase 1, §11.3`

### Commit 7 — `feat(store): deadline-bounded resumable mark-and-sweep GC`

- [ ] Write `internal/store/gc_test.go` with the 12 GC tests and `BenchmarkGC_50kObjects`. Run — **all fail**.
- [ ] Implement `gc.go`: the tri-state retention axes (`0` inherits config, positive overrides, negative disables) with `max(30 days, 10 sessions)` as a disjunction, structural `(?:sha256:)?[0-9a-f]{64}` harvesting from checkpoints/pins/eliminations without importing those packages, live chunk set + `state/gc-live.bin`, lexicographic sweep with a cursor, `state/gc.json`, tombstone appends, `GCReport` accumulation.
- [ ] Run: `go test ./internal/store/... -race`, `go run ./tools/devtool ci-local`.
- Files: `internal/store/{gc.go,gc_test.go}`.
- Footer: `Refs: SP-06, §8.2, §12 storage growth, ARCH §5.8 GC semantics`

### Commit 8 — `test(store): conformance suites, e2e coverage and benchmark baselines`

- [ ] Delete every remaining `t.Skip` in `storetest` **first**, run `go test ./internal/store/... -run 'Suite'`, and record which assertions fail — TDD ordering still applies here: the suite is the specification and any failure is a real gap in commits 3–7, fixed in a fixup on the owning commit, never papered over by weakening the suite.
- [ ] Fill in `storetest.RunStoreSuite` completely and assert in a meta-test that `storetest` contains zero `t.Skip` occurrences (the W-1 merge blocker, mechanized).
- [ ] Add `test/e2e/store_test.go` with `TestE2E_StoreSurvivesProcessRestart` and `TestE2E_SecretNeverLandsInObjects`.
- [ ] Generate `testdata/golden/store/*` from `TestGolden_IndexFormats` and commit them.
- [ ] Update `testdata/bench-baseline.txt` with this package's benchmark results.
- [ ] Run: `go run ./tools/devtool ci-local` (fmt, lint, vet, test -race, cover), confirm `internal/store` ≥ 90 % (its §6.4 floor) and `internal/redact`, `internal/tokens` ≥ 90 % (SP-06's self-imposed override), and push. Do **not** gate on `chunk`/`canon` coverage — they are SP-04 stubs on this branch.
- Files: `internal/store/storetest/*.go`, `test/e2e/store_test.go`, `testdata/golden/store/*`, `testdata/bench-baseline.txt`.
- Footer: `Refs: SP-06, ARCH §5.22 W-1, §6.4`

---

## Subagent strategy

This subplan is **heavy**: three packages, roughly 5 500 lines including tests. Partition it across six parallel subagents on **file-disjoint** boundaries, then integrate in the main session. The commit plan stays strictly sequential — subagents produce code, the main session commits it in the order above.

**Main session keeps, and no subagent may touch:**

- `internal/store/store.go` — the `FSStore` struct, `Open`'s `Deps` defaulting, the lock discipline, the closed-store guard, and `Close`. This file is the integration point for subagents C, D and E; centralizing it removes the only realistic merge conflict.
- `go.mod` / `go.sum`, the commit sequence, and the final `ci-local` run.
- Every decision about field names on `PutResult`, `rootLine` and the JSONL shapes — these are consumed by four later subplans and must not drift.

**Subagent A — redaction.** Owns `internal/redact/**` and `testdata/corpora/secrets/**`. Input: the rule table and the `Redact` algorithm from this document verbatim. Returns: the package plus a table of `(rule, corpus file, match count)` proving each rule fires exactly once on its fixture. Blocks commit 1.

**Subagent B — token accounting.** Owns `internal/tokens/**`, `tools/devtool/genfixtures.go` and `testdata/corpora/media/**`. Input: `units`, `unitWeight`, the `runtime.tokens.*` key list (it defines no literal duplicate of any of them), the 1568 px clamp, the PDF procedure, the calibration EWMA/warm-up/clamp, and the scope boundary with SP-01's baseline. It must be told explicitly: **`Classify` is a one-rule edit, not a rewrite**, and SP-01's six other `internal/tokens` tests must still pass. Returns: the package, measured estimates for the six media fixtures so the main session can sanity-check them against the test table, and the diff of SP-01's `TestEstimate_ProseVsCode`. Blocks commit 2.

**Subagent C — objects and roots.** Owns `internal/store/{objects.go,roots.go,put.go,read.go}`. Input: the object path rule, the codec pool code, the `PutBytes` pipeline verbatim, and the `roots.jsonl` byte format. Must declare — not define — the `FSStore` fields it needs (`codec`, `rootIndex`, `chunkSet`, `refs`, `byPath`, `bytesOnDisk`) and return that list to the main session, which adds them to `store.go`. Blocks commit 3.

**Subagent D — indices.** Owns `internal/store/{tooluse.go,files.go,segments.go}`. Input: the three JSONL formats, the three `ChangedSince` rules, and the `MarkEncoded` code verbatim. Same field-declaration protocol as C (`toolUse`, `byPathTU`, `fileHist`, `segByID`). Blocks commits 4 and 5. **D is the highest-risk subagent** — the DPI guard and `ChangedSince` are the two behaviours other subplans build directly on, so its output gets a line-by-line review in the main session against the §8.2 and §8.3 quotes above.

**Subagent E — search, stats, flush, GC.** Owns `internal/store/{search.go,stats.go,flush.go,gc.go}`. Input: the weight constants, the scoring block, the `Stats` definitions and the GC algorithm. Depends on C's and D's in-memory index shapes, so it starts **after** C and D have returned their field lists — this is the one sequencing constraint inside the parallel phase. Blocks commits 6 and 7.

**Subagent F — conformance and e2e.** Owns `internal/store/storetest/**`, `test/e2e/store_test.go`, and the golden generation. Starts as soon as C and D land in the working tree; it consumes the public API only, so it never conflicts. Blocks commit 8.

**Integration protocol.** Each subagent returns (1) its files, (2) the `FSStore` fields it requires, (3) the exact `go test` command that proves its slice green, and (4) any place where this document was ambiguous — ambiguities are resolved in the main session and written back into this file's spec before the commit, never resolved locally by the subagent. The main session runs `go build ./...` after each subagent's return and `go run ./tools/devtool ci-local` before commits 3, 5, 7 and 8.

---

## Exit criteria

**From `Qompack.md` §10 Phase 1, verbatim — the store half:**

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

`TestPhase1ExitCriterion_ReadHeavy` asserts the ratio half (`Stats.DedupRatio ≥ 4.0`) inside this branch. The hook p99 half is SP-05's B-A budget and SP-08's to close at the end of wave 2; SP-06's contribution is bounded by `BenchmarkPutBytes_100KB_Cold ≤ 3 ms` against budget **B-C** (`l0_process` p99 < 50 ms). The with-and-without-canonicalization comparison corpus is SP-04's deliverable; SP-06 supplies the measurement surface (`Stats.DedupRatio` with `canonicalize.enabled` toggled) and `TestStats_DedupRatio` proves the toggle changes the number.

**From `Qompack.md` §11.3, verbatim:**

> - Store growth sublinear in session length after dedup

Asserted by `TestStats_SublinearGrowth`; the replay-gate wiring of the same guardrail is SP-02's.

**From `Qompack.md` §8.2, verbatim:**

> **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint.

Asserted by `TestSegment_MarkEncodedRefusesDifferentSeq` and `TestSegment_MarkEncodedBatchIsAllOrNothing`.

**Local criteria — all must hold on the branch head:**

- [ ] `go run ./tools/devtool ci-local` green: `gofumpt -l` empty, `golangci-lint run` clean, `go vet` clean, the `nomagic` pass clean (every chunk parameter read from config; the one `//nomagic:allow` is the documented `argsPreviewMax`), the import-graph check clean (`store` imports only `chunk canon sketch symbols redact tokens` + foundation; `tokens` and `redact` import foundation only).
- [ ] `go test ./... -race` green on Linux and Windows; `-count=2` on Windows.
- [ ] Coverage for `internal/store` ≥ **90 %** — its floor under 00-ARCHITECTURE §6.4. `redact` and `tokens` are not named in §6.4 and therefore carry the 75 % "everything else" floor; SP-06 **self-imposes 90 % on both** (redaction is §13 invariant 7 and G10.2 is a correctness gap) and asserts it in `devtool cover` with an explicit override list, not by claiming a §6.4 membership they do not have. `chunk` and `canon` are SP-04's; they are stubs on this branch and their coverage is **not** gated here.
- [ ] Zero `t.Skip` remaining in `internal/store/storetest`, `internal/redact/redacttest`, `internal/tokens/tokenstest` — asserted by a meta-test, not by inspection.
- [ ] Every benchmark in the budget table meets its number on the CI Linux runner; results appended to `testdata/bench-baseline.txt`.
- [ ] `FuzzRedactIdempotent` runs 60 s with zero crashes and zero new corpus entries that violate idempotence.
- [ ] `TestE2E_SecretNeverLandsInObjects` green — no built-in secret literal appears in any object under `objects/`.
- [ ] CI green on `feat/sp06-content-addressed-store` for `verify`, `test`, `cover`, `crossbuild`, `security`, `docs`.
- [ ] Exactly 8 commits, each with a conventional-commit subject and a `Refs:` footer, none carrying an attribution trailer.

---

## Done checklist

- [ ] Branch `feat/sp06-content-addressed-store` was cut from a `develop` containing SP-01, and nothing was merged into it from a sibling wave-1 branch.
- [ ] Every item in **Design context** is implemented and traceable to a test: §8.2 content addressing → `TestPutBytes_FanoutLayout`; global dedup → `TestPutBytes_GlobalDedup`; file version history → `TestAppendFileVersion_History`; the encoded-once DPI guard → `TestSegment_MarkEncodedRefusesDifferentSeq`; GC retention `max(30 days, 10 sessions)` → `TestGC_RetentionIsWhicheverIsLonger`; §8.1 canonicalize-first → `TestPutBytes_CanonicalizeBeforeChunk`; §8.3 items 2–3 → the five `ChangedSince` tests; §8.7 minimal span → `TestOpenSpan_Boundaries`; §10 Phase 1 ratio → `TestPhase1ExitCriterion_ReadHeavy`; §12 storage growth → the GC suite; §13 invariant 7 → `TestPutBytes_RedactionBeforeChunking` and the e2e test; G10.2 → the twenty-two `tokens` tests.
- [ ] Placeholder scan clean: `go run ./tools/devtool lint` runs the unfinished-work grep (deferred-work markers, `FIXME`, `XXX`, `not implemented`, bare `panic("`) over `internal/store`, `internal/redact` and `internal/tokens` and reports nothing outside `_test.go` files that deliberately assert on sentinel text.
- [ ] `core.ErrNotImplemented` appears nowhere in `internal/store`, `internal/redact`, `internal/tokens`.
- [ ] Type consistency with the **Interface contract**: every method of `store.Store` and `store.SegmentLog` matches 00-ARCHITECTURE §5.8 character-for-character; `ChangedSince` takes `[]core.Dep`; `EstimateRoot` takes `[]core.ChunkRef`; the only additions are the ones enumerated under "Produces", all additive, none changing an existing signature.
- [ ] No package outside the §3.2 allowance is imported: `store` does not import `negknow`, `checkpoint`, `pins`, `dag`, `observer` or `daemon`; `tokens` does not import `store`; `redact` imports foundation only.
- [ ] Commit count verified: exactly 8 commits, within the 5–8 rule of 00-ARCHITECTURE §10.
- [ ] No `Co-Authored-By`, `Signed-off-by`, `Generated with`, or 🤖 in any commit message, merge message, tag or PR body on this branch — verified with `git log develop..HEAD --format=%B | grep -niE "co-authored-by|signed-off-by|generated with|🤖"` returning nothing.
- [ ] `Qompack.md` is byte-identical to its state on `develop` (`git diff develop -- Qompack.md` is empty).
- [ ] The wave-1 merge order is respected: this branch merges into `develop` **after** SP-03 and SP-04, and its Rule W-2 golden-fixture tests are re-run against the real `chunk`, `canon`, `symbols` and `sketch` implementations at the V2 verification checkpoint, with any fixture the real implementation cannot reproduce treated as a verification failure rather than a fixture bug.

---

## Spec resolutions recorded during implementation

Per the **Integration protocol** ("ambiguities are resolved in the main session and written back into this file's spec before the commit, never resolved locally by the subagent"), these are the points where this document conflicted with a frozen SP-01 artifact already on `develop`. In every case the frozen artifact won, because it is what the merge gate actually runs.

| # | Conflict | Resolution |
|---|---|---|
| D1 | This document specifies compact `{"v":1,…}` on-disk lines for `roots.jsonl` / `tool_use.jsonl` / `segments.jsonl`; `testdata/golden/contracts/store/want/*.jsonl` (state `frozen`, owner SP-06) hold long-key documents whose `MANIFEST.json` calls each "one index/… line". | They are **different artifacts** and both stand. `internal/store/fixture_test.go` — the only thing that enforces the frozen fixtures — asserts nothing about files on disk: it round-trips the **Go types** `Root`, `ToolUseRecord` and `Segment` through `encoding/json` and compares with `require.JSONEq`. The compact shapes are the **file** format, with their own goldens under `testdata/golden/store/`. Consequence, and it is binding: **no json-tagged field may be added to `Root`, `ToolUseRecord` or `Segment`**, since that would break the round-trip. `PutResult` carries no json tags and appears in no fixture, so its two additive fields are safe. |
| D2 | Rule 3 `github_token` uses `[A-Za-z0-9]{36}`; `redacttest`'s positive fixture is `ghp_` + **38** characters, which `{36}` + `\b` cannot match. | Quantifier relaxed to `{36,}`. |
| D3 | `redacttest`'s `github_token` **negative** fixture is `token: ghz_1234567890abcdefghijklmnopqrstuvwxyz12` and the suite requires `require.Empty(matches)` — but rule 9 `assignment_secret` matches bare `token` followed by `:`. | Bare `token` is accepted only with `=`, which is exactly what 00-ARCHITECTURE §5.22a writes ("`password=`/`secret=`/`token=` assignments"). The `:` form — an extension for YAML/JSON-shaped input, needed by this document's own `PASSWORD: hunter22` case — is limited to unambiguous key names (`password`, `passwd`, `secret`, `api_key`, `access_key`, `client_secret`, `auth_token`, `access_token`, `refresh_token`, `api_token`). |
| D4 | The `if e-s < 3 { continue }` growth-bound floor drops `redacttest`'s bounded-growth fixture `password=x`, whose value span is one byte, but that suite asserts a match **fires**. | The floor is applied to the **full match span**, not to the replaced group span. Density — and therefore the growth bound — is governed by the full span (per-rule matches never overlap), so the bound is preserved while a short secret value is still redacted. The ≥3-byte **admission** requirement for user-supplied patterns is unchanged, which is where the adversarial growth risk actually lives. |
| D5 | This document keys the calibration file by `core.HashBytes("qompack.project.v1", …)`. | `internal/core/hash.go`'s domain registry documents at length that `qompack.project.v1` deliberately does **not** exist (it would move every project's IPC endpoint). SP-01's existing `calibKey` — domain `qompack.tokens.calib.v1` over `filepath.Clean(scope)` — is kept unchanged; `internal/tokens/project_calib_test.go` pins it. The persisted key is still a 12-hex `.Short()`, so the documented file shape is unaffected. |
| D6 | Commits 1, 2 and 8 say to "remove the `t.Skip` lines" from the three conformance suites and to assert zero `t.Skip` occurrences. | Those suites contain no static skips. Rule W-1 is implemented as a **runtime probe** (`skipIfStubStore`/`skipIfStub`, which call the seam and test for `core.ErrNotImplemented` or the documented zero value), so the behaviour blocks switch on automatically once a real implementation lands. The meta-test therefore asserts the probes report *not-a-stub* for the real implementations — i.e. that the behaviour blocks actually ran — which is the property the "zero skips" wording was reaching for. |
| D7 | The plan's `codec` pool duplicates zstd machinery. | SP-01's `internal/store/compress.go` already ships pooled `Encode`/`Decode`, documented as the door "SP-06's real Put/PutBytes calls directly", and its decoder is bounded by `WithDecoderMaxMemory(64 MiB)` — a §13 invariant 7 protection the plan's snippet lacks. The plan's pool is dropped in favour of the existing one. |
| D8 | `internal/chunk` is an SP-01 stub whose `Split` returns `nil`, so a literal reading of the pipeline stores a zero-chunk root for every input — silent data loss that looks like success. | `FSStore.splitChecked` validates that the chunker's output tiles `[0, len)` contiguously and falls back to a single whole-input chunk otherwise, counting `store.chunker.degraded`. This is a permanent data-loss guard, not a W-2 workaround: `Open(root)` must reproduce what was put regardless of chunker quality. Store tests inject a deterministic chunker double for the dedup assertions. `chunk.RootHash` is **real** in SP-01 and is used unchanged. |
| D9 | `index/sessions.jsonl` records carry `roots`/`objects`/`bytes`/`rawbytes`/`dedup`, which read as store-wide totals — but a store-wide snapshot changes whenever *any* session writes, so every Flush would re-append every session and `TestFlush_Idempotent` could not hold. | Every counter in a session record is scoped to **that session**, derived from the tool_use index (`Bytes` is the session's deduplicated chunk footprint; `Dedup` is its own ratio). Deriving rather than accumulating also means `PutOptions` needs no session field, and a rebuilt index yields identical records. |
| D10 | Not a conflict, but out of the document's scope and required for a green tree: landing a real store flips three build-order guards. | `test/guards/stubs_test.go`'s `store` entry takes `pureMethods: allMethodsAreReal`; `test/guards/v1_integration_test.go`'s `v1AssertZeroPayloads` becomes probe-aware so it asserts the zero-payload property only for packages still stubbed; `test/guards/probes.go` needs no change (`buildorder_test.go` tests for *violations*, which a real store satisfies). |
| **D11** | **The `Redact` algorithm printed above contains a real bug.** `out = append(out, '»')` appends a *rune constant*: `»` is U+00BB, whose value 187 fits in a byte, so Go silently truncates it to the single byte `0xBB` instead of the two-byte UTF-8 sequence `C2 BB`. (The opening `«` is unaffected — it is appended as part of a *string*.) | Verified empirically: the plan's line produces `c2 ab … 6a 77 74 bb`, which is **invalid UTF-8**, and — far worse — `placeholderRe` matches `»` as UTF-8, so it can never match the writer's own output. The idempotence guard is therefore **silently dead** and `Redact(Redact(x)) != Redact(x)`, failing the one property §5.22a names explicitly. Fixed by appending the string constant `placeholderClose`. **The buggy line should be corrected in this document before any other subplan copies it.** |
| **D12** | The guard `if rule.name != "pem_private_key" && s == 0 && e == len(in) { continue }` is a security hole, and it is also mechanically incompatible with the frozen suite. | Removed. `redacttest`'s `jwt` positive fixture *is* nothing but a JWT, so the guard makes it produce zero matches and `require.NotEmpty` fails — the conflict is forced, not a preference. Independently, the guard means `Redact("@@SEC_AWS_AKID@@")` returns the key **unredacted**. This document's own PEM carve-out reasoning ("a tool result that *is* nothing but a private key must still be fully redacted… the exact outcome §13 invariant 7 forbids") applies verbatim to all ten families, so the carve-out is generalized rather than special-cased. §5.22a's "no rule ever matches across the whole input" is preserved where it is actually meaningful — as `TestRedact_NeverWholeInputExceptPEM` over the corpus fixtures, i.e. as a statement about rule greediness rather than about short inputs. |
| **D20** | **Landing a real store makes `TestGuard_Phase0BeforeStore` fail on this branch in isolation.** | The guard fires when `internal/store` is real while `internal/eval` is still an SP-01 stub, citing closing note 1 ("without measurement, everything else is opinion"). `internal/eval` is **SP-02's**, a wave-1 sibling, so a branch containing only SP-06 structurally cannot satisfy it. **The guard is left untouched, and this branch's CI is red on that one test by decision.** It encodes a ship-order constraint about what reaches an integration branch; neutralizing it — by branch-name detection, an env-var gate, or deleting the assertion — would discard exactly the signal it exists to raise. It clears by itself once SP-02 merges into `develop`, which §1 of the V2 checkpoint already sequences ahead of SP-06. Documented in full at **V2-VERIFY §2.6a ①**, which is where it is meant to be settled. Four other guard call sites DID need updating, because they assert wave-0 stub behaviour that a landed store legitimately no longer has: `stubs_test.go`'s `store` row takes `pureMethods: allMethodsAreReal`; `v1AssertZeroPayloads` and `TestV1_StubGraphIsInertAndOwned` become probe-aware so they check only packages still stubbed; and `storeProbe`, `testutil.Project.Store`, the `storetest` factories and `rehydratetest`'s factory all now `Close` the store they open, because a real store holds append-only handles that block `t.TempDir` cleanup on Windows. |
| **D19** | **The "`Redact` over 100 KB ≤ 2 ms" budget is not reachable with a per-rule RE2 design, and redaction was 87 % of the entire ingest path.** | Partly fixed, partly a budget defect — recorded with measurements rather than papered over. **Fixed:** each rule now sits behind a mandatory-literal `bytes.Contains` prefilter, taking the dominant no-secret case from 32.5 ms to **0.64 ms** per 100 KB (51×, one alloc), and a warm 100 KB `PutBytes` from 25.1 ms to 227 µs. Equivalence is asserted against a forced no-prefilter path over the corpus and 50 boundary probes, by a one-secret-per-literal probe proving each literal is genuinely mandatory, and by a dedicated fuzz target; the probe suite was mutation-tested with eight deliberately broken literals, all caught. **Not fixed:** 8.2 ms when the payload merely contains a keyword (`key`, `token`) that no rule ultimately matches, and 27.2 ms when secrets are genuinely present. Measured per rule, **every rule except `pem_private_key` costs ≥ 2 ms for one 100 KB pass on its own**, so the budget is unreachable for any payload needing even a single full scan — `pem_private_key` is 250× cheaper only because its literal `-----BEGIN ` triggers Go's regexp literal-prefix scanner, which `\b` and character-class starts defeat. Collapsing the ten rules into one alternation was measured directly and is **0.53–0.67×, i.e. slower**, because the merged NFA costs more per byte than the passes it saves. The one remaining structural option is to run each regex only inside windows around its literal hits, making cost proportional to secret count rather than payload size; it is **deliberately not taken**, because slicing the input changes `\b` and `(?m)^$` semantics at every window edge and that is not a correctness risk worth accepting on the function whose sole job is keeping secrets out of `objects/`. Recommended for SP-17 hardening, together with revising the 2 ms figure, which appears to have been set without measuring Go's `regexp`. |
| **D15** | **`putObject`'s per-object `Sync()` before the rename is incompatible with its own budget.** | Measured: one 100 KB result is tens to hundreds of chunks, and serialized fsyncs cost **1.9 s** against a 3 ms budget (B-C). Removed. The rename still provides **atomicity** — no torn object is ever visible — and **durability** is batched into `Flush`, which is exactly how borg, restic and git commit a repository transaction (git does not fsync loose objects by default either). A crash can now lose an object but can never corrupt one. Crash states are: neither object nor root line (clean); object without a root line (an orphan, which GC collects); or a root line without its object, which `GetChunk`/`Open` report as `core.ErrNotFound` and `Has` catches by falling back to a stat. That last state is degraded-but-detected rather than silent corruption, and repairing it is `qompack fsck`'s job in SP-17 — flagged here as a residual for that subplan. |
| D16 | The performance table is stated without a platform, but several budgets are unreachable on Windows. | The subplan itself says benchmarks are measured **on the CI Linux runner**, and profiling confirms the residual gap is syscall cost: 93 % `runtime.cgocall`, i.e. ~25 file create+rename pairs per put and one open+read per `GetChunk`, which cost 10–40× less on Linux. Recorded so the numbers are read against the right platform, not silently re-tuned. Avoidable syscalls were cut regardless (one candidate filename checked on write instead of two; fanout `MkdirAll` memoized per process with a create-and-retry fallback; the per-write `MkdirAll` of `.qompack/tmp` only fires if it has actually gone missing), taking the cold put from 34.7 ms to 22.9 ms before the redaction fix below. |
| D17 | The rename backoff cannot be implemented as specified. | The plan specifies "three times with 1 ms / 2 ms / 4 ms backoff", but §6.1 bans wall-clock sleeps outright, `devtool lint`'s `sleepcheck` enforces that with **no annotation escape hatch**, and `core.Clock` exposes only `Now`/`Since` — there is nothing legitimate to sleep on. The ban is also correct on its own terms here: 7 ms of backoff inside a 3 ms budget is precisely what §6.1 exists to prevent. Retries are kept, separated by `runtime.Gosched()`; a lock that outlives them fails the Put, the caller degrades (§12.3), and the identical object is rewritten on the next attempt because the content address has not changed. |
| D18 | Two smaller contradictions in the roots-line and dedup specs. | (a) The sample roots line shows `"eph":false` while the rule beneath it says `sig`, `deltas` and `eph` are "omitted entirely when absent/false" — the **rule** is followed, which is what the golden pins. (b) `TestPutBytes_GlobalDedup`'s "Novel strictly decreasing after the first" does not hold for fixture v4, which rewrites 240 lines *and* grows the file 19 %, so it legitimately writes more chunks than v1; the ≤1.6× bound is scoped to v1–v3 (the small-edit case §6.1 actually describes) and v4 is covered by its own `TestPutBytes_LargeRewriteStillSharesChunks`. **V2 correction:** the 1.6× bound died with the injected double whose 65-chunk split calibrated it; under the real chunker the checkpoint replaced it with span-derived bounds (`novel ≤` chunks spanning the diff, and v2+v3 together cheaper than one more read), and the v4 sharing claim moved to 97 KB scale — v4's first-16 KB rewrite removes every rolling-hash cut point, so the chunker hits its max-size clamp and the positional cut shares nothing, which a fixture arm now pins as the explanation. |
| D14 | Commit 2 names `TestEstimate_ProseVsCode` as "the **one** SP-01 test this commit rewrites". A second one also has to change: `TestEstimate_ImageDecodeFailureFallsBackToByteLength`. | Forced by this document's own spec, which states "Unparseable image → `(0, false)`, and `Estimate` falls back to the unit scanner at `unitWeight[ClassBinary]`". SP-01 instead flat-rated an undecodable image at `imageFallbackBytesPerToken = 1000`, making 2 500 bytes of opaque content cost **3 tokens** — the same flat-rating §2.2 indicts and G10.2 exists to close. Rewritten as `TestEstimate_ImageDecodeFailureFallsBackToUnitScanner`, asserting the unit-scanner result and that opaque bytes are not flat-rated into near-nothing. The other six SP-01 `internal/tokens` tests pass unmodified, as required. |
| **D21** | **`openFS` leaked every append-only handle it had already opened whenever a later step of the open failed.** | Found by commit 8's `TestRecovery_OpenFailsCleanlyWhenAnIndexIsUnusable`, which surfaced as `t.TempDir` cleanup failures on Windows: with `index/tool_use.jsonl` occupied by a directory, `roots.jsonl` was already open and stayed open forever. A failed `openFS` returns no store, so **nothing in the process can ever close what it opened** — the handles leak for the lifetime of the daemon, and on Windows they also lock the index files against every other process, including the next attempt to open the store that just failed. Fixed with `FSStore.releaseWriters`, run on every failure path after the first handle is acquired. It deliberately does not call `Flush`: the store never became usable, so there is nothing of the caller's to persist. |
| **D22** | **`parseRootLine` accepted records it could not interpret, unlike the other three loaders.** | It checked only for `op == "gc"`; any other `op`, and any `v` at all, fell through to the content-record path. A later wave appending a new record type to `roots.jsonl` — or any `v:2` line — would therefore have been indexed by this build as a **phantom root**: an entry with no chunks and zero bytes that `Stats` counts, `Search` ranks and `GC` reasons about, reconstructed entirely from a line whose meaning this build does not know. `loadToolUse` and `segLog.load` already skip on both axes. Fixed so `roots.jsonl` agrees with them: an unknown version or an unknown `op` is counted as a bad line and skipped. Safe to add because both writers (`marshalRootLine` and `marshalGCTombstone`) emit `"v":1`. |
| **D23** | **Seven entry points accepted a `context.Context` and never read it — including the entire ingest hot path.** | `Put`, `PutBytes`, `GetChunk`, `Open`, `OpenSpan`, `MarkSuperseded` and `GC` ignored cancellation, while `RecordToolUse`, `AppendFileVersion`, `Search`, `Stats`, `Flush` and the segment writers all honoured it — an inconsistency introduced across the file-disjoint subagent slices, which is exactly the class of defect the integration session exists to catch. A `ctx` in the signature that is never read is worse than no `ctx`: SP-05 runs every store call under budget B-C, and a method that ignores its deadline makes that budget advisory — the call still runs to completion and the budget is already blown by the time it returns. `PutBytes` is the worst case, being redaction + canonicalization + chunking + compression + object writes. Fixed in all seven. The **exemption is now explicit and asserted rather than accidental**: `GetRoot`, `ToolUse`, `ToolUsesByPath`, `FileHistory`, `FileAt`, `ChangedSince` and the five reading `SegmentLog` methods answer from memory under an `RLock` and deliberately do *not* check, because refusing to hand back data the store already holds would deny a caller the one answer it needs while shutting down. Both halves are swept by `TestContract_BlockingEntryPointsHonourCancellation` and `TestContract_InMemoryLookupsIgnoreCancellation`. |
| **D24** | **`clampSpan` and `truncateRunes` each promised a bound they did not enforce.** | Both found by commit 8's helper unit tests. `clampSpan` raised `lo` to 0 but never lowered it to `total`, despite its doc comment claiming it "bounds `[lo, hi)` to `[0, total]`" — so `score`'s exact-symbol branch, whose offset comes from a symbol extractor rather than from the content, could publish a `Hit` whose `Span` starts past the root's `CanonBytes`. Never a crash, because both consumers (`OpenSpan`, `summarize`) re-clamp defensively; but retrieval *publishes* `Hit.Span`, and a consumer has no way to distinguish a nonsense offset from a real one, so `Span[0] ≤ Root.CanonBytes` belongs in the producer rather than in each reader. `truncateRunes` appended `previewEllipsis` unconditionally, so `truncateRunes(s, 1)` returned **three** bytes — a truncation returning more than it was asked for. It now drops the ellipsis rather than break the bound, still cutting on a rune boundary. Both were latent: reachable only through inputs no current caller produces (`argsPreviewMax` is the sole bound passed today), which is exactly how they survived. Both now do what their doc comments always said. |
| **D25** | **`chunkSet`'s `int32` narrowing was unguarded on the read path.** | The narrow width is deliberate — `chunkSet` holds one entry per chunk in the whole store, so `int32` rather than `int` is what keeps a million-chunk index affordable — but `c.Len` does not only come from the chunker. Replaying an index line, it is whatever number JSON admits, so a bare `int32(c.Len)` wraps 3 × 10⁹ to a **negative** length. `GetChunk` passes the recorded length straight to `getObject` as the expected size, so one bad digit in one line would turn every read of that chunk into a spurious integrity failure — or, landing on `-1` by coincidence, into a *silently disabled* integrity check. `chunkLenOrUnknown` now reports `-1` ("present, length unknown") for anything outside `[0, MaxPutBytes]`, degrading that chunk to checksum-only, which is exactly the degradation an object with no index entry already receives. Surfaced by `gosec` G115 during the final `ci-local`, then given a real test rather than a `//nolint`. |
| **D26** | **`plans/V2-VERIFY-primitives-store-dag-and-baseline.md` was edited concurrently on two wave-1 branches, and one of the two edits is uncommitted.** | SP-06 added §2.6a to it; SP-04 independently added a recommended-model header and a **new §2.0 merge-inspection section with `V2-MERGE-*` rows** that does not exist on `develop`. The edits sit in different regions, so git will merge them **cleanly and silently** — there is no conflict to force a deliberate resolution, and a `-X ours`/`-X theirs` resolution would drop one half without a word. The halves are complementary, not redundant: losing SP-04's costs the merge-inspection gates, losing SP-06's costs the seven carried-forward corrections §2.6 depends on. **The urgent half is SP-04's, which was still uncommitted in its worktree when SP-06 landed** (`…/qompack`, branch `feat/sp04-…`, 65 insertions) — if it is never committed, no branch carries it and `git show` cannot recover it, so a `checkout`, `reset` or `stash drop` in that worktree destroys it with no conflict and no trace. This row is the backup carrier: it lives in a file only SP-06 touches, so it survives any resolution of the collision it describes. The instruction and its two detection greps are at **V2-VERIFY §2.6a ⑧**, and the same warning is repeated in commit 8's message, which no file-level merge can rewrite. |
| — | **Where D21–D25 were committed.** | All are defects in code owned by commits 3–7, found by commit 8's suite and by the final `ci-local`, which this document requires be "fixed in a fixup on the owning commit, never papered over by weakening the suite". They are folded into **commit 7** rather than spread across commits 3–6: the "exactly 8 commits" constraint forbids adding fixup commits, and rebasing five commits to place each fix on its precise origin would have put every earlier commit's compiles-and-passes-in-isolation property at risk for no reviewer benefit. Commit 7 is also where `store.Open` became the real constructor, so it is the commit that first made D21–D23 reachable through the public API at all. Commit 7 was re-verified in an isolated worktree after each amendment (`go build ./...` plus `go test` over all six packages it touches), so its stand-alone property is asserted rather than assumed. |
| D13 | Two fixture-level errors in the test table: `TestRedact_DotenvGatedByKeyName` expects `DATABASE_URL=postgres://u:pw@h/db` to be redacted, but `DATABASE_URL` carries none of rule 10's gated key names and `pw` is 2 bytes — below `credentialed_uri`'s `{3,}` floor. The fixture list is described as "12 files" but enumerates 13. | Password lengthened so `credentialed_uri` fires (the realistic case; the floor is left alone). 13 fixtures shipped — 10 rules + 2 mixed + 1 adversarial — since dropping one would leave a built-in rule without its own fixture. |

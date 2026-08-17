# SP-04: FastCDC chunker, the per-tool canonicalizer registry with MinHash near-dedup (O2), and the shared symbol extractor

> **Recommended model: Opus 5 · xhigh effort**
>
> FastCDC and the canonicalizer registry are written out in the plan; the only real trap is the byte-exact `Restore` inverse and single-pass match composition, which `xhigh` handles. Pure libraries, no concurrency, no I/O.

**Branch:** feat/sp04-chunking-canonicalization-and-symbols (cut from develop) | **Wave:** 1 | **Prerequisites:** SP-01 (its branch already merged into develop) | **Runs in parallel with:** sibling subplans of wave 1 (SP-02, SP-03, SP-05, SP-06, SP-07) | **Design sections:** §6.1, §8.1 item 1, §8.7 (minimal-span symbol resolution), §10 Phase 1 (chunker + canonicalizers), Appendix C store.chunk / store.canonicalize | **Gaps closed:** none assigned directly — SP-04 is a pure-library slice; the §6.1 gaps G3.1, G3.2 and G2.1 are closed on top of it by SP-06 (store) and SP-08 (observer), and G10.2's exact chunk-level accounting is sized from `chunk.Chunk` by SP-06.

---

## Mission

This slice builds the three content-pipeline primitives that every byte entering the Qompack store passes through: **content-defined chunking**, **per-tool canonicalization**, and **language-agnostic symbol extraction**. All three are pure libraries — no I/O, no daemon, no hooks, no config mutation. `internal/chunk` and `internal/symbols` import foundation packages only; `internal/canon` imports foundation plus `internal/sketch` (§3.2 import table). Nothing in this subplan touches a file under `.qompack/`.

It exists because `Qompack.md` §6.1 identifies content-defined chunking as the mechanism that collapses "the same file read four times with two lines changed" into one chunk set plus three near-empty reference lists, and because §8.1 item 1 identifies canonicalization as the place where that collapse is actually won or lost: *"Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost."* Exact-hash dedup is defeated by timestamps, ANSI escapes, PIDs, memory addresses, temp paths and run durations, so a chunker without canonicalizers in front of it produces a store that grows linearly with the number of test runs. The MinHash signature attached to every canonical result is the second half of that mechanism — "same test suite, one new failure" is detected as a near-duplicate and stored as a delta against the prior version rather than as a fresh full text. `internal/symbols` lands here rather than in a consumer because §5.22b names it *"One owner, four consumers"*: `store.Query.Symbol` (SP-06), `dag` shared-symbol edges (SP-07), the analyzer's symbol-reference counting (SP-15), and the `mcp` minimal-sufficient-span widener (SP-13). Four consumers across three waves is exactly the shape that produces four divergent regex heuristics unless one subplan owns it and lands first.

**What exists when you start.** SP-01 has merged into `develop`. The repository is initialized (`main`, `develop`), the Go 1.26 module `github.com/qompack/qompack` builds, `gofumpt`/`golangci-lint`/the in-repo `nomagic` pass/the import-graph check/`tools/devtool` all run, and CI's `verify`, `test`, `cover`, `crossbuild`, `plugin-validate`, `security` and `docs` jobs are green. `internal/core` provides `Hash`, `HashBytes`, `ParseHash`, `ChunkRef`, `Dep`, `Clock`, and the sentinel errors. `internal/config` provides `Defaults()`, `Load`, `Validate` and the full Appendix C schema plus the §11.5 `runtime` namespace. `internal/paths`, `internal/logging`, `internal/obs`, `internal/testutil` exist. Critically, SP-01 has already shipped **compiling stubs returning `core.ErrNotImplemented`** for `internal/chunk`, `internal/canon`, `internal/symbols`, and `internal/sketch`, together with the conformance suites `internal/canon/canontest` and `internal/symbols/symbolstest` whose behaviour tests are `t.Skip`ped, and `testdata/golden/contracts/{canon,symbols}/` fixture directories.

**What exists when you finish.** `internal/chunk` implements FastCDC with normalized chunking at `min 1024 / target 4096 / max 16384`, a deterministic gear table pinned by a golden digest, `Split`, `SplitStream`, `Params.Validate`, and the domain-separated Merkle `RootHash`. `internal/canon` implements a deterministic registry with fourteen canonicalizers — seven generic classes and seven per-tool rule sets — every one idempotent and structurally non-growing, every one keeping its volatile substrings as `canon.Delta` side records so `canon.Restore` is a byte-exact inverse, and every `Registry.Run` result carrying a MinHash signature plus a delta-versus-full dedup decision for SP-06 to act on. `internal/symbols` implements `Extract`, the `Enclosing` minimal-sufficient-span resolver of §8.7, and `References`. The `canontest` and `symbolstest` suites have their skips removed and their behaviour bodies implemented. `testdata/corpora/toolout/` holds a committed golden corpus of real bash, test-runner, grep, glob, file-read, webfetch and git output with before/after fixtures, and `testdata/canon-dedup-report.json` holds a measured with-versus-without-canonicalization dedup comparison that SP-08 consumes for the Phase 1 exit criterion. Every latency and throughput budget in this document has a committed benchmark.

---

## Design context (verbatim from Qompack.md)

**§6.1 Content-defined chunking + Merkle addressing** (verbatim):

> **Closes:** bloat, G3.1, G3.2, G2.1
>
> The largest single source of transcript growth is the same file read four times with two lines changed. Fixed-size chunking fails because one inserted line shifts every boundary. Content-defined chunking cuts at boundaries determined by a rolling hash of a sliding window, so an insertion perturbs one chunk and the rest realign.
>
> Hash each chunk, store once, reference by hash. Four reads of a 2,000-line file collapse to one chunk set plus three near-empty reference lists. This is `borg`/`restic`/ZFS. O(n) with a rolling hash — microseconds per megabyte.
>
> The Merkle structure is free once content-addressing, and **it is the retrieval index**: `tool_use_id → root hash → chunk list`. Compaction becomes rehashing pointers rather than deleting content.

**§8.1 item 1, Chunk and store** (verbatim):

> 1. **Chunk and store.** Run FastCDC over the tool result. Suggested parameters for source text: `min = 1KB`, `target = 4KB`, `max = 16KB` — smaller than backup workloads because source files are smaller. Store novel chunks zstd-compressed; record the chunk list.
>    **Canonicalize first (O2).** Exact-hash dedup is defeated by volatile substrings: timestamps, ANSI escape codes, PIDs, memory addresses, temp-dir paths, and run durations make every `Bash` and test-runner output unique even when semantically identical. Before chunking, apply per-tool canonicalizers that strip or normalize these (store the canonical form; keep the volatile deltas as a tiny side record if byte-exact recovery matters). For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text. Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost.

**§8.1 Performance budget** (verbatim):

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

**§8.7 minimum sufficient span** (verbatim):

> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch. Most post-compaction questions are "what did that one function look like," not "give me the file."

**§10 Phase 1 — Store and observer** (verbatim):

> - FastCDC chunker, content-addressed object store, zstd
> - Per-tool output canonicalizers (timestamps, ANSI, PIDs, addresses) + MinHash near-dedup (O2)
> - `PostToolUse` and `UserPromptSubmit` hooks
> - Merkle index, file version history
> - Addressable tombstones (G3.2)
> - Redundancy / supersession detection
>
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

**Appendix C — `store.chunk` and `store.canonicalize`** (verbatim):

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

**§2.2 MicroCompact — the compactable tool set this slice must canonicalize for** (verbatim):

```
FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
```

That list is the closed set of `tool` values the per-tool canonicalizers of §8 below must recognise; anything else falls through to the seven generic canonicalizers only.

**§11.3 Guardrails** (verbatim, the line that binds this slice):

> - Store growth sublinear in session length after dedup

Sublinear growth is exactly what a chunker without canonicalizers in front of it cannot deliver on test-output-heavy sessions, which is why `test/dedup` measures the with-versus-without gap rather than asserting the ratio by assumption.

**§5.5 of 00-ARCHITECTURE — normative properties of `internal/chunk`** (verbatim):

> Normative properties (fuzz-tested by SP-04): boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

**§5.6 of 00-ARCHITECTURE — normative properties of `internal/canon`** (verbatim):

> Normative properties (SP-04): `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**§4 of 00-ARCHITECTURE — text normalization** (verbatim):

> **Text normalization.** Content entering the store is CRLF→LF normalized by the `crlf` canonicalizer before chunking, so a Windows and a Linux read of the same file dedup to the same chunks. The original line-ending class is recorded as a `canon.Delta`.

**§6.4 of 00-ARCHITECTURE — coverage floor:** `chunk` and `canon` are in the **90%** line-coverage group; `symbols` is in the **75%** group.

**§11.6 of 00-ARCHITECTURE — the no-hardcoding rule** (verbatim, binding on this slice because `1024`, `4096`, `16384`, `0.9` are all in the forbidden set):

> the in-repo `nomagic` analysis pass fails the build on any float literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` or integer literal in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` appearing outside `internal/config/defaults.go`, `*_test.go`, and explicitly annotated `//nomagic:allow <reason>` lines.

---

## Out of scope

| Item | Owner |
|---|---|
| Content-addressed object files `objects/ab/cd/<sha256>.zst`, zstd compression, `index/roots.jsonl`, `index/tool_use.jsonl`, `index/files.json`, GC, `store.Stats.DedupRatio` | **SP-06** |
| `store.PutResult.NearDup` / `NearDupInfo` population and the actual delta-vs-full storage write | **SP-06** (SP-04 supplies the pure decision function it calls) |
| `internal/redact` and its application at `store.Put`/`PutBytes` before canonicalization | **SP-06** |
| `sketch.MinHash`, `Signature.Jaccard`, `Signature.IsNearDup`, shingling, Bloom/CMS/HLL/Misra-Gries, sketch serialization | **SP-03** |
| `PostToolUse` wiring, addressable tombstones, supersession/redundancy detection, and the Phase 1 exit-criterion measurement itself (≥ 4:1, hook p99 < 15 ms) | **SP-08** |
| DAG `EdgeSharedSymbol` edge construction from `symbols.Extract` output | **SP-07** |
| `analyzer.NewCheapScorer` symbol-reference Δ-scoring built on `symbols.References` | **SP-15** |
| `mcp` `expand`/`re_read` minimal-span responses built on `symbols.Enclosing` and `store.OpenSpan` | **SP-13** |
| `config.CanonicalizeCfg` shape, defaults, validation, JSON Schema, provenance | **SP-01** |
| `internal/tokens` estimation and per-project calibration | **SP-01** (baseline) / **SP-06** (exact chunk-level) |
| `internal/daemon`, `internal/ipc`, hot-path budgets B-A/B-B/B-C | **SP-05** |
| Replay harness, Belady OPT, divergence metrics, the replay-gate | **SP-02** |
| `.gitattributes` creation (SP-04 only *appends* one line to it), `.gitignore`, CI workflow files | **SP-01** |
| Packaging, cross-platform matrix, docs | **SP-17**, **SP-18** |

---

## Interface contract

### Consumes (exact signatures, all shipped by SP-01 on `develop`)

```go
// package core (internal/core) — §4 of 00-ARCHITECTURE
type Hash [32]byte
func (h Hash) String() string          // "sha256:" + hex
func (h Hash) Short() string           // first 12 hex chars
func ParseHash(s string) (Hash, error)
func HashBytes(domain string, b []byte) Hash   // sha256(domain || 0x00 || b)
type ChunkRef struct{ Hash Hash; Len int }
var ErrNotImplemented, ErrNotFound error

// package config (internal/config) — §5.1, §11.1
func Defaults() Config
type Config struct{ Store StoreCfg /* …§5.1… */ }
type StoreCfg struct {
    Chunk        ChunkCfg        `json:"chunk"`
    Compression  string          `json:"compression"`
    Retention    RetentionCfg    `json:"retention"`
    Canonicalize CanonicalizeCfg `json:"canonicalize"`
}
type ChunkCfg struct{ Min, Target, Max int }
type CanonicalizeCfg struct {
    Enabled bool       `json:"enabled"`
    Strip   []string   `json:"strip"`
    MinHash MinHashCfg `json:"minhash"`
}
type MinHashCfg struct {
    Enabled          bool    `json:"enabled"`
    Permutations     int     `json:"permutations"`
    NearDupThreshold float64 `json:"nearDupThreshold"`
}

// package sketch (internal/sketch) — §5.7. Stubbed on develop; real at V2 (SP-03, same wave).
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }
type Signature struct{ Perms uint16; Mins []uint64 }
func MinHash(data []byte, o MinHashOptions) Signature
func (s Signature) Jaccard(o Signature) float64
func (s Signature) IsNearDup(o Signature, threshold float64) bool
```

**Rule W-2 compliance for the `sketch` dependency.** `sketch` is a same-wave sibling. `canon` therefore never depends on `sketch` behaviour for its own correctness: the only calls are `sketch.MinHash` (pass-through into `Result.Signature`) and, inside the one-line helper `canon.NearDup`, `Signature.Jaccard`. Every decision this slice makes about near-duplicates is expressed by the pure function `canon.Decide(jaccard float64, …)`, which is fully unit-testable with hand-written Jaccard values against the stub. Golden fixtures for `canon.Decide` live at `testdata/golden/contracts/canon/dedup-decisions.json` and are re-run against the real `sketch.MinHash` at the V2 verification checkpoint.

**If SP-01's `config.CanonicalizeCfg` field names differ from the shapes above**, adapt only `internal/canon/default.go` and `internal/chunk/params.go`. Never edit `internal/config`; that is SP-01's package and a change there requires an `arch/` amendment (§0 of 00-ARCHITECTURE).

### Produces (relied on by later subplans)

```go
// package chunk — §5.5 of 00-ARCHITECTURE, verbatim, plus SP-04-owned additive symbols
type Params struct{ Min, Target, Max int }
func DefaultParams() Params
func FromConfig(c config.Config) Params            // additive
func (p Params) Validate() error
func (p Params) Normalized() Params                // additive: deterministic clamping
type Chunk struct{ Offset int64; Len int; Hash core.Hash }
type Chunker interface {
    Split(data []byte) []Chunk
    SplitStream(r io.Reader, fn func(Chunk, []byte) error) error
}
func New(p Params) Chunker
func RootHash(chunks []Chunk) core.Hash
func (c Chunk) Ref() core.ChunkRef                 // additive: core.ChunkRef{Hash, Len}
func Refs(chunks []Chunk) []core.ChunkRef          // additive: for tokens.EstimateRoot
const ChunkDomain = "qompack.chunk.v1"             // additive: SP-06 names objects by this hash
const RootDomain  = "qompack.root.v1"              // additive
const GearWindow  = 64                             // additive: rolling-window length in bytes
// ParamsReporter is additive and is how the effective (post-Normalized) parameters are read
// back through the §5.5 Chunker interface, which deliberately has no Params method. The value
// returned by New always satisfies it: v, ok := c.(chunk.ParamsReporter).
type ParamsReporter interface{ Params() Params }   // additive

// package canon — §5.6 of 00-ARCHITECTURE, verbatim, plus SP-04-owned additive symbols
type Class string
const (
    ClassTimestamps Class = "timestamps"
    ClassANSI       Class = "ansi"
    ClassPIDs       Class = "pids"
    ClassAddresses  Class = "addresses"
    ClassTmpPaths   Class = "tmpPaths"
    ClassDurations  Class = "durations"
    ClassCRLF       Class = "crlf"
    ClassPaths      Class = "paths"
)
func KnownClasses() []Class                        // additive
func ParseClass(s string) (Class, bool)            // additive
type Delta struct{ Offset, Len int; Original string; Class Class }
type MinHashOptions = sketch.MinHashOptions        // additive alias
type Result struct {
    Canonical []byte
    Deltas    []Delta
    Applied   []string
    Signature sketch.Signature
    Reduced   float64
}
type Options struct{ Strip []Class; KeepDeltas bool; MinHash MinHashOptions }
func OptionsFrom(cfg config.CanonicalizeCfg, keepDeltas bool) Options   // additive
type Canonicalizer interface {
    Name() string
    Applies(tool, path string) bool
    Canonicalize(in []byte, o Options) (Result, error)
}
type Match struct{ Offset, Len int; Token []byte; Class Class }         // additive
type Matcher interface{ Matches(in []byte, o Options) []Match }         // additive
type Registry interface {
    Register(c Canonicalizer) error
    For(tool, path string) []Canonicalizer
    Run(tool, path string, in []byte, o Options) (Result, error)
    Names() []string
}
func NewRegistry() Registry
func Default(cfg config.CanonicalizeCfg) Registry
func Restore(canonical []byte, deltas []Delta) ([]byte, error)
func LineEndingClass(deltas []Delta, canonicalNewlines int) string  // additive: "lf"|"crlf"|"mixed"
type Strategy uint8                                // additive
const (StrategyFull Strategy = iota; StrategyDelta)
type DedupDecision struct {                        // additive — consumed by SP-06
    NearDup       bool
    Jaccard       float64
    Strategy      Strategy
    EstDeltaBytes int
}
func Decide(jaccard float64, canonLen, priorLen int, threshold float64) DedupDecision  // additive
func NearDup(a, b sketch.Signature, threshold float64) (bool, float64)                 // additive
var ErrUnknownClass, ErrNotMatcher, ErrDuplicateName, ErrDeltaRange, ErrDeltaOrder error

// package symbols — §5.22b of 00-ARCHITECTURE, verbatim
type Symbol struct{ Name, Kind string; Line, Offset, Len int }
type Extractor interface {
    Extract(path string, b []byte) []Symbol
    Enclosing(path string, b []byte, off int) (Symbol, bool)
    References(b []byte, names []string) map[string]int
}
func New() Extractor
const MaxExtractBytes = 4 << 20                    // additive
const MaxSymbols      = 20000                      // additive //nomagic:allow symbol-count cap, not a config value
```

**Consumer map for the produced symbols** (why they cannot move after this branch merges): `chunk.New`/`Split`/`RootHash`/`Refs` → `store.Put` (SP-06) and `observer.OnToolUse` (SP-08). `canon.Default`/`Run`/`Restore`/`Decide` → `store.Deps.Canon` (SP-06), `observer` (SP-08). `symbols.New` → `store.Deps.Symbols` and `store.Query.Symbol` (SP-06), `dag` `EdgeSharedSymbol` (SP-07), `analyzer.NewCheapScorer` (SP-15), `mcp` span widener (SP-13).

---

## Implementation spec

### 1. `internal/chunk/gear.go` — the gear table

Responsibility: a package-level, immutable, deterministic 256-entry `uint64` gear table, generated at `init()` from a fixed seed so no 4 KB literal table is committed and cross-platform determinism is provable.

```go
package chunk

// gearSeed is arbitrary but frozen: changing it re-chunks every object in every existing
// store. It is pinned by TestGearTableGolden.
const gearSeed uint64 = 0x9067_4E5A_1CDB_7F31

var gear [256]uint64

func init() {
    s := gearSeed
    for i := range gear {
        gear[i] = splitmix64(&s)
    }
}

// splitmix64 is the reference SplitMix64 generator (Steele, Lea & Flood 2014).
func splitmix64(x *uint64) uint64 {
    *x += 0x9E37_79B9_7F4A_7C15
    z := *x
    z = (z ^ (z >> 30)) * 0xBF58_476D_1CE4_E5B9
    z = (z ^ (z >> 27)) * 0x94D0_49BB_1331_11EB
    return z ^ (z >> 31)
}
```

`//nomagic:allow gear-table generator constants are not config values` on the three multiplier lines if `nomagic` flags them.

### 2. `internal/chunk/params.go` — parameters, validation, clamping

```go
type Params struct{ Min, Target, Max int }

func DefaultParams() Params {
    c := config.Defaults().Store.Chunk   // 1024 / 4096 / 16384 — never written as literals here
    return Params{Min: c.Min, Target: c.Target, Max: c.Max}
}

func FromConfig(c config.Config) Params {
    return Params{Min: c.Store.Chunk.Min, Target: c.Store.Chunk.Target, Max: c.Store.Chunk.Max}
}
```

`Validate()` returns a non-nil error, wrapping a package sentinel, for each of:

| Rule | Error text |
|---|---|
| `Target` is not a power of two | `chunk: target 5000 is not a power of two` |
| `Target < 256` or `Target > 1<<20` | `chunk: target 128 outside [256, 1048576]` |
| `Min < GearWindow` (64) | `chunk: min 32 below gear window 64` |
| `Min < Target/8` | `chunk: min 256 below target/8 = 512` |
| `Min >= Target` | `chunk: min 4096 must be < target 4096` |
| `Max < 2*Target` | `chunk: max 6000 below 2*target = 8192` |
| `Max > 16*Target` | `chunk: max 200000 above 16*target = 65536` |

Defaults satisfy every rule: `1024 ≥ 64`, `1024 ≥ 4096/8 = 512`, `1024 < 4096`, `16384 = 4×4096 ∈ [8192, 65536]`, `4096 = 2^12`.

`Normalized()` applies deterministic clamping, in this exact order, and is what `New` uses so that `New` can keep its no-error signature (§5.5):

1. `Target` → if `Target ≤ 0`, `Target = DefaultParams().Target`; otherwise largest power of two `≤ Target`, then clamped into `[256, 1<<20]`.
2. `Min` → `max(Min, GearWindow, Target/8)`; if `Min ≥ Target`, `Min = Target/4`.
3. `Max` → if `Max ≤ 0`, `Max = 4*Target` (the Appendix C `16384 / 4096` ratio, so a zero value reproduces the default shape rather than collapsing to the floor); otherwise clamped into `[2*Target, 16*Target]`.

`New(p Params) Chunker` stores `p.Normalized()` and precomputes `maskS`, `maskL`. The returned `*chunker` implements the additive `ParamsReporter` interface (`Params() Params`) so `store` can log the effective parameters and detect that a bad config was clamped; callers reach it with `c.(chunk.ParamsReporter)`.

### 3. `internal/chunk/fastcdc.go` — the boundary algorithm

Masks are derived from `Target`, never written as literals:

```go
bits    := bits.TrailingZeros64(uint64(p.Target))  // 12 for Target=4096
maskS   := topBits(bits + 2)                       // 14 bits → 0xFFFC000000000000
maskL   := topBits(bits - 2)                       // 10 bits → 0xFFC0000000000000
func topBits(n int) uint64 { return ^uint64(0) << (64 - n) }
```

This is FastCDC normalized chunking at level 2 (Xia et al. 2016, Appendix B): the harder mask applies below `Target` so short chunks are rare, the easier mask applies above `Target` so long chunks are rare; the two regimes together put the expected chunk length at `Target`.

**Rolling hash.** `fp = (fp << 1) ^ gear[b]` — **XOR, not addition**. With XOR, bit *j* of `fp` is exactly the XOR of bit `j-t` of `gear[b_{i-t}]` for `t = 0..j`, so bit *j* depends on exactly the last `j+1` bytes with no carry leakage. Both masks live in bits 50..63, so the boundary decision at absolute position *i* is a pure function of `data[i-63 : i+1]`. That is what makes boundary stability a content property rather than a chunk-start property, and it is the reason `Validate` requires `Min ≥ GearWindow`: the 64-byte priming window `[s+Min-64, s+Min)` always lies inside the current chunk, so `SplitStream` needs no cross-chunk carry buffer.

```go
// nextCut returns the length of the chunk starting at data[0].
func (c *chunker) nextCut(data []byte) int {
    n := len(data)
    if n <= c.p.Min {
        return n
    }
    if n > c.p.Max {
        n = c.p.Max
    }
    normal := c.p.Target
    if normal > n {
        normal = n
    }
    var fp uint64
    for j := c.p.Min - GearWindow; j < c.p.Min; j++ { // prime the 64-byte window
        fp = (fp << 1) ^ gear[data[j]]
    }
    i := c.p.Min
    for ; i < normal; i++ {
        fp = (fp << 1) ^ gear[data[i]]
        if fp&c.maskS == 0 {
            return i + 1
        }
    }
    for ; i < n; i++ {
        fp = (fp << 1) ^ gear[data[i]]
        if fp&c.maskL == 0 {
            return i + 1
        }
    }
    return n
}
```

`Split(data []byte) []Chunk`:

```go
func (c *chunker) Split(data []byte) []Chunk {
    if len(data) == 0 {
        return nil
    }
    out := make([]Chunk, 0, len(data)/c.p.Target+1)
    d := sha256.New()
    var hb [sha256.Size]byte
    var off int64
    for len(data) > 0 {
        n := c.nextCut(data)
        d.Reset()
        d.Write(chunkDomainBytes) // []byte("qompack.chunk.v1")
        d.Write(zeroByte)         // []byte{0x00}
        d.Write(data[:n])
        d.Sum(hb[:0])
        out = append(out, Chunk{Offset: off, Len: n, Hash: core.Hash(hb)})
        off += int64(n)
        data = data[n:]
    }
    return out
}
```

`d.Write(domain); d.Write([]byte{0}); d.Write(payload)` is byte-identical to `core.HashBytes(ChunkDomain, payload)`; `TestChunkHashMatchesCoreHashBytes` asserts it over 1 000 random payloads. Exactly two allocations per `Split` call — the returned slice and one `sha256.New()` hasher that is `Reset` per chunk — which is how §5.5's "allocation-free apart from the returned slice" is met in practice (the hasher is O(1), not O(chunks)); `BenchmarkSplit_1MiB` asserts `≤ 2 allocs/op`. No state is shared across calls, so the method is safe for concurrent use.

`SplitStream(r io.Reader, fn func(Chunk, []byte) error) error`:

- Buffer `buf := make([]byte, 0, p.Max*2)`.
- Loop, in this exact order so the refill and emit conditions cannot deadlock on `len(buf) == p.Max`:
  1. Refill from `r` into `buf` until `len(buf) > p.Max` **or** the reader reports EOF. (`> p.Max`, not `≥`: at exactly `p.Max` bytes with more input pending, a cut computed now could still be extended by the next byte, and an `≥` test here would emit nothing and re-enter refill having read nothing — an infinite loop.)
  2. While `len(buf) > p.Max`, **or** EOF has been seen and `len(buf) > 0`: take `n := nextCut(buf)`, hash, call `fn(Chunk{Offset, n, hash}, buf[:n])`, then slide `buf = buf[n:]` by `copy`ing the remainder back to the front.
  3. Exit when EOF has been seen and `len(buf) == 0`.
- `nextCut` never inspects beyond `p.Max` bytes, so a cut taken with `len(buf) > p.Max` is identical to the cut `Split` would take with the whole remainder present. That equivalence is what `TestSplitStream_MatchesSplit` proves exhaustively.
- The `[]byte` passed to `fn` aliases the internal buffer and is **only valid for the duration of the call**; this is documented on the interface and asserted by `TestSplitStream_BufferAliasingDocumented`, which copies inside the callback and compares against `Split`.
- `fn` returning a non-nil error aborts immediately and `SplitStream` returns that error unwrapped.
- A read error other than `io.EOF` is returned unwrapped after flushing nothing. `io.ErrUnexpectedEOF` is treated as EOF only if some bytes were read.
- `SplitStream` over any reader produces exactly the same chunk sequence as `Split` over the fully-read bytes. Property-tested against 1-byte, 7-byte, 4096-byte and full-size readers.

`RootHash(chunks []Chunk) core.Hash`:

```go
func RootHash(chunks []Chunk) core.Hash {
    b := make([]byte, 0, len(chunks)*sha256.Size)
    for _, c := range chunks {
        b = append(b, c.Hash[:]...)
    }
    return core.HashBytes(RootDomain, b)
}
```

`RootHash(nil)` is `core.HashBytes(RootDomain, nil)` — a fixed, non-zero value, tested. Domain separation means `RootHash` of a single chunk is **never** equal to that chunk's own hash, tested explicitly.

**Performance budget.** §8.1: *"FastCDC over 100KB is well under 1ms."* Gates, enforced by benchmarks in commit 2 and by `benchstat` against `testdata/bench-baseline.txt` (§7 of 00-ARCHITECTURE, >25% regression fails):

| Benchmark | Budget |
|---|---|
| `BenchmarkSplit_100KB` | < 800 µs/op |
| `BenchmarkGearScan_1MiB` (boundary scan only, no hashing) | ≥ 400 MB/s |
| `BenchmarkSplit_1MiB` | ≥ 120 MB/s, ≤ 2 allocs/op |
| `BenchmarkSplitStream_4MiB` | ≤ 40 ms/op, ≤ 2 allocs/op amortized |
| `BenchmarkRootHash_1000Chunks` | < 40 µs/op |

### 4. `internal/canon/types.go` — classes, options, result, deltas

`Class` constants exactly as listed under *Produces*. `KnownClasses()` returns them in registration order: `crlf, ansi, timestamps, durations, pids, addresses, tmpPaths, paths`. `ParseClass` is case-sensitive and matches the Appendix C spellings exactly (note `tmpPaths` is camelCase).

**Gating rule.** `Options.Strip` selects which classes are in play; the gate is applied once, centrally, in the registry's accept loop (§5 below), never inside an individual `Matches` implementation. `ClassCRLF` and `ClassPaths` are **structural and always applied**, regardless of `Strip`, because §4 of 00-ARCHITECTURE makes CRLF→LF normalization a precondition of cross-platform dedup and `paths` (separator normalization) is the same argument for path text. All six Appendix C `strip` classes are opt-in through `Strip`. `OptionsFrom(cfg, keepDeltas)` maps `cfg.Strip` through `ParseClass`, skipping unknown entries (SP-01's `config.Validate` already reports them), and copies `cfg.MinHash` into `Options.MinHash` with `ShingleSize: DefaultShingleSize` (`= 5`) since Appendix C has no shingle key.

`Delta.Offset` is in **canonical (output) coordinates**; `Delta.Len` is the length of the replacement token in the output; `Delta.Original` is the exact original bytes that were replaced. Deltas are emitted in ascending `Offset` order, and `d[i].Offset + d[i].Len ≤ d[i+1].Offset` always holds.

`LineEndingClass(deltas []Delta, canonicalNewlines int) string` counts as `k` the deltas whose `Class == ClassCRLF` **and** whose `Original` is exactly `"\r\n"` (the `ClassCRLF` class also carries the always-on trailing-whitespace rule of §8, whose deltas must not be counted as line endings), and returns `"lf"` when `k == 0`, `"crlf"` when `k == canonicalNewlines`, and `"mixed"` otherwise. The caller supplies `canonicalNewlines` (`bytes.Count(canonical, []byte{'\n'})`) because the function is deliberately free of a second scan over the payload. This satisfies §4 of 00-ARCHITECTURE — *"The original line-ending class is recorded as a `canon.Delta`"* — without changing the §5.6 `Result` struct, which SP-04 may not do (§0 amendment rule).

### 5. `internal/canon/registry.go` — single-pass match composition

The registry does **not** chain byte transforms. Chaining makes delta coordinates ambiguous (a later stage shifts an earlier stage's offsets) and makes `Restore` order-dependent. Instead every canonicalizer also implements `Matcher`, and `Run` performs one pass over the **original** input:

```go
type candidate struct {
    Match
    rank  int    // registration index
    owner string // canonicalizer name
}

func (r *registry) Run(tool, path string, in []byte, o Options) (Result, error) {
    for _, c := range o.Strip {
        if _, ok := ParseClass(string(c)); !ok {
            return Result{}, fmt.Errorf("%w: %q", ErrUnknownClass, c)
        }
    }
    if len(in) == 0 {
        return Result{Canonical: nil, Applied: []string{}}, nil
    }
    head, tail := in, []byte(nil)
    if len(in) > MaxInputBytes { // 8 << 20
        head, tail = in[:MaxInputBytes], in[MaxInputBytes:]
    }
    var cands []candidate
    for i, c := range r.canons {
        if !c.Applies(tool, path) {
            continue
        }
        for _, m := range c.(Matcher).Matches(head, o) {
            cands = append(cands, candidate{Match: m, rank: i, owner: c.Name()})
        }
    }
    sort.SliceStable(cands, func(a, b int) bool {
        x, y := cands[a], cands[b]
        if x.Offset != y.Offset { return x.Offset < y.Offset }
        if x.Len != y.Len       { return x.Len > y.Len }   // longest at an offset wins
        return x.rank < y.rank                              // then registration order
    })
    // accept greedily, class-gated, non-overlapping, non-growing
    gate := gateSet(o.Strip) // set(o.Strip) ∪ {ClassCRLF, ClassPaths}
    accepted := cands[:0]
    end := 0
    for _, m := range cands {
        if !gate[m.Class]       { continue } // Strip gating, enforced HERE (see below)
        if len(m.Token) > m.Len { continue } // non-growth is structural
        if m.Len == 0           { continue } // an insertion can never be legal
        if m.Offset < end       { continue } // overlaps an accepted match
        accepted = append(accepted, m)
        end = m.Offset + m.Len
    }
    out, deltas := applyMatches(head, accepted, o.KeepDeltas)
    out = append(out, tail...)
    res := Result{Canonical: out, Deltas: deltas, Applied: appliedNames(accepted, r)}
    if len(in) > 0 { res.Reduced = 1 - float64(len(out))/float64(len(in)) }
    if o.MinHash.Enabled { res.Signature = sketch.MinHash(out, o.MinHash) }
    return res, nil
}
```

`applyMatches`:

```go
func applyMatches(in []byte, ms []candidate, keep bool) ([]byte, []Delta) {
    out := make([]byte, 0, len(in))
    var deltas []Delta
    prev := 0
    for _, m := range ms {
        out = append(out, in[prev:m.Offset]...)
        if keep {
            deltas = append(deltas, Delta{
                Offset:   len(out),
                Len:      len(m.Token),
                Original: string(in[m.Offset : m.Offset+m.Len]),
                Class:    m.Class,
            })
        }
        out = append(out, m.Token...)
        prev = m.Offset + m.Len
    }
    return append(out, in[prev:]...), deltas
}
```

`accepted := cands[:0]` is the standard in-place filter idiom and is deliberate: the loop only ever writes to an index at or below the index it is reading, so reusing the backing array is safe and removes one allocation from the hot path.

**Where `Strip` gating is enforced.** In the accept loop, not inside each `Matches` implementation. A `Matches` method may emit every match its patterns find; the registry is the single place that decides whether a `Class` is in play. `gateSet(strip)` returns `set(strip) ∪ {ClassCRLF, ClassPaths}` — the two structural, always-on classes of §4 above. Enforcing it centrally means a third-party canonicalizer registered by a later subplan cannot bypass the user's `store.canonicalize.strip` setting, and it makes `TestRun_StripGatesOptionalClasses` a test of one function rather than of fourteen. Every match a canonicalizer emits therefore **must** carry the `Class` assigned to it in the tables of §7 and §8; a match with the zero `Class` is dropped by the gate and `TestMatcherClassAssigned` asserts that no built-in ever emits one.

`Applied` lists the names of canonicalizers that contributed at least one **accepted** match, deduplicated, in registration order. §5.6 calls this field "canonicalizer names, in application order"; because composition is a single pass over the original input rather than a transform chain, *application order is defined to be registration order* — there is no other order in which the canonicalizers can be said to have been applied. `TestApplied_DeduplicatedRegistrationOrder` pins it. Deterministic by construction.

`Register(c Canonicalizer) error` returns `ErrDuplicateName` on a repeated `Name()` and `ErrNotMatcher` if `c` does not implement `Matcher` — the registry has no fallback path, which is what keeps composition single-pass and delta coordinates unambiguous. `For(tool, path)` returns the applicable canonicalizers in registration order. `Names()` returns all registered names in registration order.

**Large-input policy.** Inputs above `MaxInputBytes = 8 << 20` are canonicalized over the first 8 MiB only and the remainder is appended verbatim. Deterministic, and delta offsets remain valid because the tail is appended after `applyMatches` returns.

**Per-canonicalizer `Canonicalize`.** Every built-in implements it as `applyMatches` over its own `Matches` output with the same sort/accept pass restricted to `rank = 0`, so a canonicalizer used standalone behaves exactly as it does inside the registry. `Result.Signature` is the zero value from a standalone `Canonicalize`: only `Registry.Run` populates it.

### 6. `internal/canon/restore.go` — the byte-exact inverse

```go
func Restore(canonical []byte, deltas []Delta) ([]byte, error) {
    if len(deltas) == 0 { return append([]byte(nil), canonical...), nil }
    out := make([]byte, 0, len(canonical)+64)
    prev := 0
    for i, d := range deltas {
        if d.Offset < prev || d.Len < 0 || d.Offset+d.Len > len(canonical) {
            return nil, fmt.Errorf("%w: delta %d offset=%d len=%d canonical=%d",
                ErrDeltaRange, i, d.Offset, d.Len, len(canonical))
        }
        if i > 0 && d.Offset < deltas[i-1].Offset {
            return nil, fmt.Errorf("%w: delta %d offset %d precedes delta %d offset %d",
                ErrDeltaOrder, i, d.Offset, i-1, deltas[i-1].Offset)
        }
        out = append(out, canonical[prev:d.Offset]...)
        out = append(out, d.Original...)
        prev = d.Offset + d.Len
    }
    return append(out, canonical[prev:]...), nil
}
```

Failure modes: `ErrDeltaRange` for a negative or past-end offset/length; `ErrDeltaOrder` for a non-monotonic list. Both wrap with `%w` so callers can `errors.Is`. Neither ever panics on adversarial input — asserted by `FuzzRestore`.

### 7. `internal/canon/generic.go` — the seven structural/generic canonicalizers

Every rule below is a `(pattern, token, class)` triple applied through the non-growing guard. Tokens are chosen so no pattern can ever match a token (all tokens use `<` and `>`, which appear in no pattern), which is what makes idempotence structural rather than tested-and-hoped.

| # | Name | `Applies` | Class | Rules (pattern → token) |
|---|---|---|---|---|
| 1 | `crlf` | always | `crlf` | hand-scan for the exact 2-byte `\r\n` → `\n` (Len 2, Token 1) |
| 2 | `ansi` | always | `ansi` | CSI `\x1b\[[0-?]*[ -/]*[@-~]` → `` ; OSC `\x1b\][^\x07\x1b]*(\x07|\x1b\\)` → `` ; two-byte `\x1b[@-Z\\-_]` → `` |
| 3 | `timestamps` | always | `timestamps` | ISO-8601 `\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z\|[+-]\d{2}:?\d{2})?` → `<ts>`; RFC-1123 `(Mon\|Tue\|Wed\|Thu\|Fri\|Sat\|Sun), \d{2} (Jan\|Feb\|Mar\|Apr\|May\|Jun\|Jul\|Aug\|Sep\|Oct\|Nov\|Dec) \d{4} \d{2}:\d{2}:\d{2} GMT` → `<ts>`; syslog `(Jan\|…\|Dec) [ \d]\d \d{2}:\d{2}:\d{2}` → `<ts>`; epoch `(?:^\|[\[\s])\d{10,13}(?:[\]\s]\|$)` (digits only replaced) → `<ts>`; bare clock `\b\d{2}:\d{2}:\d{2}(\.\d{1,6})?\b` → `<ts>` |
| 4 | `durations` | always | `durations` | Go composite `\b\d+h\d+m\d+(\.\d+)?s\b` and `\b\d+m\d+(\.\d+)?s\b` → `<d>`; simple `\b\d+(\.\d+)?\s?(ns\|µs\|us\|ms\|s\|m\|h)\b` → `<d>`; `\bin \d+(\.\d+)?\s?(ms\|s)\b` → `in <d>` (only the numeric+unit span is the match) |
| 5 | `pids` | always | `pids` | `(?i)\bpid[=: ]\s*(\d{2,7})\b` → digits become `<n>`; `^\[(\d{2,7})\]` per line → digits become `<n>`; `\bprocess (\d{2,7})\b` → digits become `<n>` |
| 6 | `addresses` | always | `addresses` | `\b0x[0-9a-fA-F]{6,16}\b` → `<addr>`; `@[0-9a-f]{6,8}\b` → `<addr>`; `\b(?:127\.0\.0\.1\|localhost\|0\.0\.0\.0\|\[::1\]):(\d{4,5})\b` → port digits become `<p>` |
| 7 | `tmpPaths` | always | `tmpPaths` | `/tmp/[A-Za-z0-9._+-]+(/[A-Za-z0-9._+-]+)*` → `<tmp>`; `/var/folders/[A-Za-z0-9._+/-]+` → `<tmp>`; `(?i)[A-Za-z]:\\\\Users\\\\[^\\\\]+\\\\AppData\\\\Local\\\\Temp\\\\[^\s"']*` → `<tmp>`; `(?i)[A-Za-z]:/Users/[^/]+/AppData/Local/Temp/[^\s"']*` → `<tmp>`; `\$TMPDIR/[^\s"']*` → `<tmp>` |

The non-growing guard silently drops any match shorter than its token (e.g. a 2-digit PID against `<n>`); the small dedup loss is deliberate and is preferable to a structural exception. `TestNonGrowingGuard_SkipsShortMatches` pins that behaviour with `pid=7` (no match accepted) versus `pid=41235` (accepted).

Regexes are compiled once with `regexp.MustCompile` at package `init`; `Matches` uses `FindAllSubmatchIndex` and, for rules with a capture group, emits the match for the **group span** only, not the whole match. RE2 has no lookahead; the `crlf` and `bash` progress rules are therefore hand-scanned byte loops, specified below.

### 8. `internal/canon/tools.go` — the seven per-tool canonicalizers

`Applies(tool, path)` compares `tool` case-insensitively against the alias sets below (§2.2's compactable tool set with both Claude Code spellings).

| # | Name | Tool aliases | Rules |
|---|---|---|---|
| 8 | `bash` | `bash`, `powershell`, `shell` | (a) **progress collapse** — hand-scan: for each `\r` byte not immediately followed by `\n`, emit a match spanning from the start of the current line through and including that `\r`, token `` (this deletes the overwritten prefix that npm/pip/docker progress bars leave behind). (b) **trailing whitespace** — per line, `[ \t]+` immediately before `\n` or EOF → ``. (c) `\bnpm WARN [a-z]+ [^\n]*deprecated[^\n]*\n` is **not** stripped (semantic). |
| 9 | `testrunner` | `bash`, `powershell`, `shell` | Applied unconditionally; the rules only fire on runner-shaped text. **go:** `^ok\s+\S+\s+(\d+\.\d{1,3}s)` → `<d>`; `\((cached)\)` kept; `^\s*--- (PASS\|FAIL\|SKIP): \S+ \((\d+\.\d{2}s)\)` → `<d>`; `^goroutine (\d{1,6}) \[` → `<n>`. **jest:** `^(Time:\s+)(\d+(\.\d+)?\s?s)` → `<d>`; `\bworker (\d{1,3})\b` → `<n>`; `^\s*at .*\(([^)]*node_modules[^)]*)\)` left alone. **pytest:** `={2,} .* in (\d+\.\d+s) ={2,}` → `<d>`; `Using --randomly-seed=(\d+)` → `<seed>`; `\bgw(\d{1,2})\b` → `<n>`; `^rootdir: (.*)$` left to `tmpPaths`. **cargo:** `finished in (\d+\.\d+s)` → `<d>`; `^\s*Finished .* in (\d+\.\d+s)` → `<d>`; `^\s*Compiling \S+ v\S+ \(([^)]+)\)` left to `tmpPaths`. |
| 10 | `grep` | `grep`, `search`, `ripgrep` | (a) trailing whitespace per line → ``. (b) **path prefix normalization** — hand-scan each line for a leading `path:line:` prefix (`[^\n:]+:\d+:`); inside that prefix only, every `\` → `/` (Class `paths`, 1:1 length). (c) a leading drive letter `^[A-Za-z]:/` inside the prefix → `/` (Class `paths`). |
| 11 | `glob` | `glob`, `ls`, `find` | Whole-line paths: every `\` → `/` (Class `paths`); trailing whitespace per line → ``. |
| 12 | `fileread` | `read`, `fileread`, `write`, `filewrite`, `edit`, `fileedit` | (a) UTF-8 BOM `\xEF\xBB\xBF` at offset 0 → `` (Class `paths`). (b) trailing whitespace per line → ``. No line-number-gutter stripping: a content line beginning with digits and a tab is indistinguishable from a gutter and stripping it would break idempotence on a second pass. |
| 13 | `webfetch` | `webfetch`, `websearch`, `fetch` | (a) `[?&]utm_[a-z]+=[^&\s"'<>]*` → `` . (b) `[?&](sid\|sessionid\|_t\|ts\|cb\|nonce)=([A-Za-z0-9._-]+)` → value becomes `<v>`. (c) `\bW/"[^"]{4,}"` → `<etag>`. (d) `\bnonce="[^"]{8,}"` → `<nonce>`. |
| 14 | `git` | `bash`, `powershell`, `shell`, `git` | (a) `^commit ([0-9a-f]{40})$` → `<sha>`. (b) `^index ([0-9a-f]{7,40})\.\.([0-9a-f]{7,40})` → each group `<sha>`. (c) `^From ([0-9a-f]{40}) ` → `<sha>`. (d) `\bgit version \d+\.\d+\.\d+` kept. Hunk headers `@@ -a,b +c,d @@` are **never** touched — they are semantic. |

**Class assignment for every per-tool rule (normative — the gate of §5 drops any match whose class is not in play).** `Class` is a closed set of eight values fixed by §5.6 of 00-ARCHITECTURE, which SP-04 may not extend (§0 amendment rule), so every per-tool rule is assigned the existing class whose meaning it matches. Only `crlf` and `paths` are always-on; every other assignment below is opt-in through `store.canonicalize.strip`, and all six Appendix C `strip` values are present by default, so every rule below fires under `config.Defaults()`.

| Canonicalizer | Rule | Class | Gated by |
|---|---|---|---|
| `bash` | (a) progress collapse (CR-overwrite residue) | `ansi` | `strip: ansi` — CR-overwrite is terminal cursor control, the same class of artefact as a CSI sequence |
| `bash`, `grep`, `glob`, `fileread` | trailing whitespace before `\n`/EOF | `crlf` | always-on — line-terminator normalization, same argument as CRLF→LF (§4 of 00-ARCHITECTURE) |
| `grep` | `path:line:` prefix separator normalization; drive-letter strip | `paths` | always-on |
| `glob` | whole-line separator normalization | `paths` | always-on |
| `fileread` | UTF-8 BOM at offset 0 | `paths` | always-on |
| `testrunner` | every duration capture (`go`, `jest`, `pytest`, `cargo`) | `durations` | `strip: durations` |
| `testrunner` | volatile numeric run ids — `goroutine N`, jest `worker N`, pytest `gwN`, `--randomly-seed=N` | `pids` | `strip: pids` |
| `webfetch` | `utm_*`, session/cache-buster values, `W/"…"` etags, `nonce="…"` | `addresses` | `strip: addresses` — opaque volatile identifiers, the same class as a hex address |
| `git` | `commit`/`index`/`From` object hashes | `addresses` | `strip: addresses` |

Because trailing-whitespace deltas carry `ClassCRLF`, `LineEndingClass` counts only `ClassCRLF` deltas whose `Original` is exactly `"\r\n"` (§4 above).

Registration order in `Default(cfg)` is exactly §5.6's: `crlf, ansi, timestamps, durations, pids, addresses, tmpPaths, bash, testrunner, grep, glob, fileread, webfetch, git`.

`Default(cfg config.CanonicalizeCfg) Registry`: when `cfg.Enabled == false`, the returned registry contains **only** `crlf`, because CRLF→LF normalization is structural per §4 of 00-ARCHITECTURE and disabling it would fork the dedup space between Windows and POSIX readers of the same file. Documented on the function and pinned by `TestDefault_DisabledKeepsCRLF`.

### 9. `internal/canon/dedup.go` — the near-dup decision surfaced to the store

```go
func NearDup(a, b sketch.Signature, threshold float64) (bool, float64) {
    j := a.Jaccard(b)
    return a.IsNearDup(b, threshold), j
}

func Decide(jaccard float64, canonLen, priorLen int, threshold float64) DedupDecision {
    d := DedupDecision{Jaccard: jaccard, Strategy: StrategyFull}
    if canonLen <= 0 || priorLen <= 0 {
        return d
    }
    d.NearDup = jaccard >= threshold
    longer := canonLen
    if priorLen > longer { longer = priorLen }
    d.EstDeltaBytes = int(math.Round((1-jaccard)*float64(longer))) + deltaFrameBytes // 64
    if d.NearDup && d.EstDeltaBytes*2 < canonLen {
        d.Strategy = StrategyDelta
    }
    return d
}
```

Rationale for `EstDeltaBytes*2 < canonLen`: a delta is only worth storing when it is under half the size of storing the canonical text outright, otherwise the chunk-level dedup that FastCDC already provides is cheaper and simpler. `threshold` is `store.canonicalize.minhash.nearDupThreshold` from config (Appendix C default `0.9`) and is **never written as a literal** in this package (§11.6 forbids `0.9`). SP-06 calls `Decide` and populates `store.NearDupInfo{PriorRoot, Jaccard, DeltaBytes}` from it.

### 10. `internal/symbols/extract.go` — the language-agnostic extractor

Heuristic and parser-free per §5.22b. Structure:

- `dialectFor(path string) dialect` maps the lowercased extension to one of: `go`, `tsjs`, `python`, `rust`, `jvm` (`.java .kt .kts .scala .cs .swift`), `cfamily` (`.c .h .cc .cpp .hpp .cxx .m .mm`), `ruby`, `shell` (`.sh .bash .zsh .ps1`), `php`, `none` (`.md .json .yaml .yml .toml .txt .lock .csv`), or `generic` (everything else).
- Each dialect carries an ordered list of `declRule{re *regexp.Regexp, group int, kind string}` and a `blockStyle` of `braces`, `indent`, or `line`.

**Declaration rules per dialect** (group index in parentheses):

- **go** — `^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)` (1) → `func`; `^type\s+([A-Za-z_]\w*)` (1) → `type`; `^(?:var|const)\s+([A-Za-z_]\w*)` (1) → `var`/`const`; inside a `^(?:var|const) \($` block, `^\t([A-Za-z_]\w*)` (1) → `var`/`const`. Block style `braces`.
- **tsjs** — `^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)` → `func`; `^(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)` → `class`; `^(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)` → `type`; `^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>` → `func`; `^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)` → `const`; method `^\s{2,}(?:(?:public|private|protected|static|async|readonly|get|set)\s+)*([A-Za-z_$][\w$]*)\s*\(` → `func`, rejected when the captured name is in the keyword deny-set `{if, for, while, switch, catch, return, do, else, try, function, constructor_skip_none}`. Block style `braces`.
- **python** — `^(\s*)def\s+([A-Za-z_]\w*)` (2) → `func`; `^(\s*)class\s+([A-Za-z_]\w*)` (2) → `class`; `^([A-Z_][A-Z0-9_]*)\s*=` (1) → `const`; `^([a-z_]\w*)\s*=` (1) → `var`. Block style `indent`.
- **rust** — `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+(\w+)` → `func`; `^\s*(?:pub\s+)?(?:struct|enum|trait|type|union)\s+(\w+)` → `type`; `^\s*(?:pub\s+)?(?:const|static)\s+(\w+)` → `const`; `^\s*impl(?:<[^>]*>)?\s+(?:[\w:<>, ]+\s+for\s+)?([\w:]+)` → `type`. Block style `braces`.
- **jvm** — `^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|private|protected|internal|open|final|static|abstract|override|suspend|sealed|data|partial|\s)*(?:class|interface|enum|struct|object|record|protocol|actor)\s+(\w+)` → `class`; `^\s*(?:@\w+\s*)*(?:public|private|protected|internal|open|final|static|override|suspend|async|\s)*(?:fun|func)\s+(\w+)` → `func`; C#/Java method `^\s*(?:(?:public|private|protected|internal|static|final|override|virtual|async)\s+)+[\w<>\[\],.?]+\s+(\w+)\s*\([^;]*\)\s*\{?\s*$` → `func`; `^\s*(?:public\s+)?(?:static\s+)?(?:final|const|val)\s+[\w<>\[\].]+\s+(\w+)\s*=` → `const`. Block style `braces`.
- **cfamily** — `^\s*(?:typedef\s+)?(?:struct|enum|union|class|namespace)\s+(\w+)` → `type`; `^#define\s+(\w+)` → `const`; `^[A-Za-z_][\w \t\*&:<>,]*?\b(\w+)\s*\([^;]*\)\s*\{?\s*$` → `func`. Block style `braces`.
- **ruby** — `^\s*def\s+([\w.?!=\[\]]+)` → `func`; `^\s*(?:class|module)\s+([\w:]+)` → `class`; `^\s*([A-Z][A-Z0-9_]*)\s*=` → `const`. Block style `indent`, with the `end`-inclusion rule below.
- **shell** — `^\s*(?:function\s+)?([\w.-]+)\s*\(\)\s*\{` → `func`; `^\s*function\s+([\w.-]+)\s*\{` → `func`; `^([A-Z_][A-Z0-9_]*)=` → `const`. Block style `braces`.
- **php** — `^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*function\s+&?(\w+)` → `func`; `^\s*(?:abstract\s+|final\s+)?(?:class|interface|trait|enum)\s+(\w+)` → `class`; `^\s*const\s+(\w+)` → `const`. Block style `braces`.
- **none** — no rules; `Extract` returns an empty, non-nil slice.
- **generic** — the union of the `tsjs` function/class rules and the `cfamily` function rule; block style chosen per declaration: `braces` if an unquoted `{` appears within four lines of the declaration, otherwise `indent`.

**Span computation.**

- `braces`: starting at the declaration's line start, scan forward with a small state machine tracking `"`, `'`, `` ` ``, `//`, `#` (shell/ruby only), `/* */`, and backslash escapes. Find the first unquoted, uncommented `{`; from there match `{`/`}` to depth zero. `Len` runs from the declaration's line start through the closing `}` inclusive. If no `{` is found within 4 lines of the declaration, or the file ends before depth returns to zero, the span is the declaration line only (or declaration-start-to-EOF respectively) — `TestExtract_UnterminatedBlock` pins the EOF case.
- `indent`: `Len` runs from the declaration line start to the start of the first subsequent non-blank, non-comment line whose leading-whitespace width is `≤` the declaration's, exclusive; if none, to EOF. **Ruby exception:** if that terminating line's trimmed text is exactly `end` or begins with `end` followed by a non-word byte, the span extends through that line inclusive.
- `line`: `Len` is the declaration line, newline exclusive.

Leading-whitespace width counts a tab as 4 columns.

**Bounds and caps.** `Extract` operates on `b[:min(len(b), MaxExtractBytes)]` (4 MiB) and stops after `MaxSymbols` (20 000) symbols. Every returned `Symbol` satisfies `0 ≤ Offset`, `0 < Len`, `Offset+Len ≤ len(b)`, and `Line ≥ 1` (1-based, counting `\n`). Results are sorted by `Offset` ascending, then `Len` descending, so a class precedes its methods.

`Enclosing(path, b, off) (Symbol, bool)`: returns the **smallest** span containing `off`; ties broken by the larger `Offset`. Returns `(Symbol{}, false)` when `off < 0`, `off ≥ len(b)`, or no symbol contains `off`. This is the §8.7 minimal-sufficient-span resolver; SP-13 caps its widened result at `store.chunk.max`.

`References(b, names) map[string]int`: single pass tokenizing `b` into identifier tokens matching `[A-Za-z_$][A-Za-z0-9_$]*`, incrementing a counter for every token present in the name set. Case-sensitive. Empty names and names longer than 256 bytes are ignored. Duplicate names collapse. Every requested name appears in the result map, with `0` when unseen. O(len(b)) regardless of `len(names)`.

**Performance budget.** `BenchmarkExtract_100KB` < 2 ms/op; `BenchmarkEnclosing_100KB` < 2 ms/op; `BenchmarkReferences_100KB_50Names` < 1 ms/op.

### 11. Conformance suites

`internal/canon/canontest/canontest.go` — replace the `t.Skip` bodies SP-01 shipped:

```go
func RunCanonSuite(t *testing.T, name string, factory func(t *testing.T) canon.Registry)
func RunCanonicalizerSuite(t *testing.T, name string, factory func(t *testing.T) canon.Canonicalizer)
```

`RunCanonSuite` asserts, for every corpus file under `testdata/corpora/toolout/`: idempotence (`Run(Run(x)) == Run(x)`), non-growth (`len(canonical) ≤ len(input)`), `Restore(canonical, deltas) == input` with `KeepDeltas: true`, deterministic `Applied` ordering across 10 repeats, `ErrUnknownClass` on a bogus `Strip` entry, `ErrDuplicateName` on double registration, and `ErrNotMatcher` on a `Canonicalizer` that is not a `Matcher`.

`internal/symbols/symbolstest/symbolstest.go`:

```go
func RunSymbolsSuite(t *testing.T, factory func(t *testing.T) symbols.Extractor)
```

Asserts span well-formedness, sort order, `Enclosing` smallest-span selection, `Enclosing` false on out-of-range offsets, `References` word-boundary correctness and zero-fill, and no panic on 4 MiB of random bytes.

**If SP-01 named these functions differently, keep SP-01's names** and fill in the bodies; renaming a conformance entry point is a W-3 violation.

---

## Test plan (TDD)

Every test below is written and run **failing** before the implementation in its commit. Fixtures are listed at the end. All tests use `testify/require` (`assert` is banned, §6.1) and `go-cmp` for structural diffs. No test sleeps.

### `internal/chunk`

| Test | Setup / input | Expected |
|---|---|---|
| `TestParamsValidate_Table` | 8 cases: defaults; `Target=5000`; `Target=128`; `Min=32`; `Min=256,Target=4096`; `Min=4096,Target=4096`; `Max=6000,Target=4096`; `Max=200000,Target=4096` | defaults `nil`; the other 7 return the exact error strings in the §3 table |
| `TestParamsNormalized_Table` | `{0,5000,0}`, `{32,4096,1000}`, `{8192,4096,16384}` | `{512,4096,16384}`, `{512,4096,8192}`, `{1024,4096,16384}` |
| `TestNewClampsInvalidParams` | `New(Params{32,5000,10})` then `Params()` | `{512,4096,8192}`, and `Split` still honours those bounds |
| `TestGearTableGolden` | sha256 over the 256 gear values little-endian encoded | equals `testdata/golden/chunk/gear-table.sha256` (created once with `-update`) |
| `TestSplit_Empty` | `Split(nil)`, `Split([]byte{})` | `nil`, length 0 |
| `TestSplit_ShorterThanMin` | 900 bytes of `0x41` | exactly 1 chunk, `Offset 0`, `Len 900` |
| `TestSplit_ExactlyMin` | 1024 bytes | exactly 1 chunk, `Len 1024` |
| `TestSplit_SizeBounds` | 4 MiB deterministic pseudo-random (splitmix64 seed 7) | every chunk but the last has `1024 ≤ Len ≤ 16384`; the last has `1 ≤ Len ≤ 16384` |
| `TestSplit_Contiguity` | same buffer | `chunks[i].Offset == chunks[i-1].Offset+chunks[i-1].Len`; `Σ Len == len(data)` |
| `TestSplit_AllZeros_HitsMaxOnly` | 1 MiB of `0x00` | every chunk except the last has `Len == 16384`. A constant byte stream drives `fp` to the fixed point `F = ⊕_{t=0..63}(gear[b] << t)` after the 64-byte priming and holds it there, so the mask test gives the same answer at every position. For the frozen seed this was computed ahead of implementation: `gear[0x00] = 0xc08cf3d020100c0b`, `F = 0xbf84514fe00ffbf9`, and `F & maskL != 0`, so no cut fires and every chunk runs to `Max`. `gear[0xFF]` gives `F = 0x14d0009bc868f5be`, also non-zero under both masks. **If this test ever fails, the gear seed — not the assertion — is what changed; pick a new `gearSeed`, re-run `TestGearTableGolden -update`, and re-verify both constants** |
| `TestSplit_Determinism` | same buffer, 100 repeats, plus a second `Chunker` instance | identical `[]Chunk` every time |
| `TestSplit_GoldenBoundaries` | 1 MiB seed-7 buffer | offsets equal `testdata/golden/chunk/boundaries-1mib.json` (`-update` to create) |
| `TestSplit_MeanChunkSize` | 8 MiB seed-11 buffer | mean chunk length ∈ `[3200, 5200]`. Normalized chunking at NC=2 centres slightly above `Target=4096` because the `Min=1024` floor suppresses the short tail; the reference simulation of this parameterisation measures ≈ 4 730 bytes on incompressible input, so the band is centred on the real value, not on `Target` |
| `TestChunkHashMatchesCoreHashBytes` | 1 000 random payloads 1 B–20 KB | `Split` chunk hash == `core.HashBytes(ChunkDomain, payload)` for single-chunk inputs |
| `TestRootHash_Empty` | `RootHash(nil)` | equals `core.HashBytes(RootDomain, nil)`, non-zero |
| `TestRootHash_DomainSeparation` | one chunk | `RootHash([]Chunk{c}) != c.Hash` |
| `TestRootHash_OrderSensitive` | two chunks, swapped | different roots |
| `TestRefs_RoundTrip` | 50 chunks | `Refs` yields `core.ChunkRef{Hash, Len}` pairwise equal |
| `TestSplitStream_MatchesSplit` | seed-13 buffers of 0, 1, 1023, 1024, 16385, 100 000, 4 MiB bytes × readers of 1, 7, 4096, and full size | chunk sequence identical to `Split` in every combination |
| `TestSplitStream_CallbackError` | callback returns `errBoom` on chunk 3 | `SplitStream` returns `errBoom` unwrapped; callback not called again |
| `TestSplitStream_ReadError` | reader returning `errRead` after 5 KB | returns `errRead` |
| `TestSplitStream_BufferAliasingDocumented` | callback copies each slice | copies concatenate to the original input |
| `TestPropBoundaryStability_Insertion` | seeded 512 trials (fixed seed 21, so the outcome is deterministic, not flaky): 256 KB buffer, insert 1–500 bytes at a random offset `k` | Novelty is measured as "chunks of the mutated stream whose bytes appear in no chunk of the original" — the dedup-relevant notion. Four assertions: (1) chunks entirely before `k` are byte-identical (**hard, per trial**); (2) a realignment index `m` exists after which every chunk is byte-identical to a chunk of the original tail (**hard, per trial**); (3) novel-chunk count `≤ 12` in every trial (**hard, per trial**); (4) `≤ 2` in `≥ 85%` of trials and `≤ 3` in `≥ 95%` of trials (**hard aggregate**). The test `t.Log`s the full novelty histogram so drift is visible in CI output. **These thresholds are measured, not aspirational:** a reference simulation of this exact algorithm at `min 1024 / target 4096 / max 16384` over 150 trials of this shape gave 84% at 1 novel chunk, 93.3% at `≤ 2`, 97.3% at `≤ 3`, max 6. §5.5's "perturbs at most 2 chunks" is the modal behaviour of a content-defined boundary rule, not a deterministic guarantee — a cut suppressed inside the `Min` region of a shifted chunk start cascades, and no CDC parameterisation removes that tail. Asserting `≤ 2` per trial would fail on a correct implementation, which is why it is stated as a distribution |
| `TestPropBoundaryStability_Deletion` | same shape and seed, delete 1–500 bytes | the same four assertions with the same thresholds |
| `TestPropSizeBounds` (rapid) | arbitrary `[]byte` up to 200 KB | all-but-last within `[Min, Max]` |
| `TestPropAllBytesCovered` (rapid) | arbitrary `[]byte` | concatenating `data[c.Offset:c.Offset+c.Len]` reproduces the input exactly |
| `FuzzSplit` | seeds from `testdata/corpora/chunk/*.bin` plus the toolout corpus | never panics; contiguity, coverage, size bounds and determinism hold on every input |
| `BenchmarkSplit_100KB` | 100 KB seed-3 buffer | **< 800 µs/op** (§8.1 "well under 1ms") |
| `BenchmarkGearScan_1MiB` | boundary scan only | **≥ 400 MB/s** |
| `BenchmarkSplit_1MiB` | | **≥ 120 MB/s** |
| `BenchmarkSplitStream_4MiB` | | **≤ 40 ms/op** |
| `BenchmarkRootHash_1000Chunks` | | **< 40 µs/op** |

### `internal/canon`

| Test | Setup / input | Expected |
|---|---|---|
| `TestKnownClassesCoverConfigDefaults` | `config.Defaults().Store.Canonicalize.Strip` | every entry parses through `ParseClass` (guards the §11.3 validation rule "strip values are known canon.Classes" across the package boundary) |
| `TestParseClass_Table` | `"tmpPaths"`, `"tmppaths"`, `"crlf"`, `"bogus"` | `(ClassTmpPaths,true)`, `("",false)`, `(ClassCRLF,true)`, `("",false)` |
| `TestRegistry_RegisterDuplicateName` | register `crlf` twice | second returns `ErrDuplicateName` |
| `TestRegistry_RegisterNonMatcher` | a `Canonicalizer` without `Matches` | `ErrNotMatcher` |
| `TestRegistry_ForDeterministicOrder` | `Default(defaults)`, `For("Bash","")` | `[crlf ansi timestamps durations pids addresses tmpPaths bash testrunner git]` in that exact order, 100 repeats identical |
| `TestRegistry_NamesFromDoubles` | a registry with three test doubles registered in a known order | `Names()` returns them in registration order; written in commit 3, where no real canonicalizer exists yet |
| `TestRegistry_Names` | `Default(defaults)` | the 14 names in registration order (commit 5 — `default.go` does not exist before it) |
| `TestMatcherClassAssigned` | every canonicalizer in `Default(defaults)`, run over every corpus file | no emitted `Match` has the zero `Class`; every emitted `Class` is one of the eight `KnownClasses()` — the guard that keeps the §5 gate from silently discarding a rule |
| `TestRun_UnknownStripClass` | `Options{Strip: []Class{"nope"}}` | `ErrUnknownClass`, `Result` zero |
| `TestRun_EmptyInput` | `Run("Bash","",nil,opts)` | `Result{Canonical:nil, Applied:[]string{}}`, `Reduced 0`, nil error |
| `TestRun_StripGatesOptionalClasses` | input with a timestamp and CRLF, `Strip: []Class{}` | timestamp survives, CRLF still normalized, `Applied == ["crlf"]` |
| `TestOverlapResolution_LongerAtSameOffsetWins` | a synthetic 2-canonicalizer registry emitting `(0,10)` rank 1 and `(0,4)` rank 0 | the 10-byte match is accepted |
| `TestOverlapResolution_EarlierOffsetWins` | `(0,10)` and `(4,4)` | only `(0,10)` accepted |
| `TestOverlapResolution_RankBreaksTies` | `(0,4)` rank 1 and `(0,4)` rank 0 | rank 0 accepted, `Applied` names it |
| `TestNonGrowingGuard_DropsGrowingMatch` | a test double emitting `Match{Len: 1, Token: []byte("<xxx>")}` (commit 3, no real canonicalizer yet) | the match is silently dropped; output equals input; `Applied` is empty |
| `TestNonGrowingGuard_SkipsShortMatches` | `pid=7` and `pid=41235` (commit 4, once `pids` exists) | `pid=7` unchanged; `pid=41235` → `pid=<n>` |
| `TestApplied_DeduplicatedRegistrationOrder` | input triggering `timestamps` twice and `ansi` once | `Applied == ["ansi","timestamps"]` |
| `TestReduced_Value` | 1 000-byte input reduced to 900 | `Reduced == 0.1` within 1e-9 |
| `TestSignature_OnlyWhenEnabled` | `Run` with `MinHash.Enabled` false, then true | false → `Signature.Perms == 0 && len(Signature.Mins) == 0`; true → `Signature` deep-equals `sketch.MinHash(res.Canonical, o.MinHash)` recomputed by the test. Both assertions hold against SP-01's stub and against SP-03's real implementation, so no W-2 skip is needed |
| `TestCRLF_Table` | `"a\r\nb\r\n"`, `"a\rb"`, `"a\nb"` | `"a\nb\n"` + 2 deltas; unchanged (lone CR is `bash`'s job); unchanged |
| `TestANSI_Table` | `"\x1b[31mERR\x1b[0m"`, `"\x1b]0;title\x07x"`, `"\x1bMx"` | `"ERR"`, `"x"`, `"x"` |
| `TestTimestamps_Table` | 6 rows: ISO with offset, ISO with `Z` and nanos, RFC-1123, syslog, epoch-ms in brackets, bare clock | each replaced by `<ts>`, deltas hold the originals |
| `TestDurations_Table` | `"1m30.5s"`, `"12ms"`, `"0.02s"`, `"5 s"`, `"3ns"` | `<d>` each |
| `TestPIDs_Table` | `"pid=41235"`, `"PID: 990"`, `"[12345] boot"`, `"pid=7"` | first three → `<n>`, last unchanged |
| `TestAddresses_Table` | `"0x00007ffee4a1"`, `"Object@1f2e3d4a"`, `"127.0.0.1:54123"` | `<addr>`, `<addr>`, `127.0.0.1:<p>` |
| `TestTmpPaths_Table` | POSIX `/tmp/…`, macOS `/var/folders/…`, Windows backslash and forward-slash `AppData\Local\Temp\…` | `<tmp>` each |
| `TestBash_ProgressCollapse` | `"downloading 10%\rdownloading 90%\rdone\n"` | `"done\n"`, 2 deltas |
| `TestBash_ProgressGatedByStrip` | the same input with `Strip` omitting `ansi`, then including it | omitted → progress residue survives and `Applied` does not contain `bash`; included → collapsed. Pins the §8 class assignment against the §5 gate |
| `TestBash_TrailingWhitespaceIsAlwaysOn` | `"x   \ny\n"` with `Strip: []Class{}` | `"x\ny\n"` — the trailing-whitespace rule carries `ClassCRLF` and is therefore not gateable |
| `TestBash_ProgressDoesNotEatCRLF` | `"a\r\nb\n"` with both `crlf` and `bash` registered | `"a\nb\n"`, `Applied == ["crlf"]` |
| `TestBash_TrailingWhitespace` | `"x   \ny\t\n"` | `"x\ny\n"` |
| `TestTestRunner_Go` | a 12-line `go test` block with `ok`, `--- PASS`, `(cached)`, `goroutine 17 [` | durations → `<d>`, goroutine id → `<n>`, `(cached)` intact |
| `TestTestRunner_Jest` | `Time:` line, `worker 3` | `<d>`, `<n>` |
| `TestTestRunner_Pytest` | `== 3 passed in 0.12s ==`, `--randomly-seed=1234567`, `gw0` | `<d>`, `<seed>`, `<n>` |
| `TestTestRunner_Cargo` | `test result: ok. … finished in 0.01s`, `Finished dev … in 1.23s` | `<d>` each |
| `TestGrep_PathPrefixOnly` | `"src\\a.ts:12:const x = a\\b\n"` | `"src/a.ts:12:const x = a\\b\n"` — the backslash **after** the prefix survives |
| `TestGlob_SeparatorsAndTrailing` | `"src\\a.ts  \nsrc\\b.ts\n"` | `"src/a.ts\nsrc/b.ts\n"` |
| `TestFileRead_BOMAndTrailing` | BOM + `"a  \nb\n"` | `"a\nb\n"` |
| `TestWebFetch_Table` | url with `utm_source`, `sid=`, `W/"abc12345"`, `nonce="…"` | `` , `<v>`, `<etag>`, `<nonce>` |
| `TestGit_Table` | `commit <40hex>`, `index abc1234..def5678 100644`, `@@ -1,7 +1,9 @@` | first two → `<sha>`, hunk header untouched |
| `TestPropIdempotence_EveryCanonicalizer` (rapid + corpus) | arbitrary bytes and every corpus file, per canonicalizer and via `Default` | `Run(Run(x)) == Run(x)` byte-for-byte |
| `TestPropNonGrowing_EveryCanonicalizer` (rapid + corpus) | same | `len(canonical) ≤ len(input)` always |
| `TestPropRestoreIsExactInverse` (rapid + corpus) | same, `KeepDeltas: true` | `Restore(canonical, deltas) == input` |
| `TestRestore_NoDeltas` | | returns a copy, not the same backing array |
| `TestRestore_OutOfRange` | `Delta{Offset: 999, Len: 1}` on a 10-byte canonical | `ErrDeltaRange` |
| `TestRestore_Unordered` | deltas at offsets `[10, 2]` | `ErrDeltaOrder` |
| `TestRestore_NegativeLen` | `Delta{Offset:0, Len:-1}` | `ErrDeltaRange` |
| `TestLineEndingClass_Table` | 0 deltas / 3 newlines; 3 deltas / 3 newlines; 1 delta / 3 newlines | `"lf"`, `"crlf"`, `"mixed"` |
| `TestDecide_Table` | 6 rows: `(j=0.95, canon=10000, prior=10200, th=0.9)`; `(0.89,…)`; `(0.95, canon=200, prior=10000)`; `(1.0, 10000, 10000)`; `(0.9, 0, 100)`; `(0.5, 1000, 1000)` | `Delta`; `Full` + `NearDup false`; `Full` (delta bigger than half of 200); `Delta` with `EstDeltaBytes == 64`; `Full` with zero-length guard; `Full` |
| `TestDecide_GoldenFixture` | `testdata/golden/contracts/canon/dedup-decisions.json` | every row reproduced |
| `TestDefault_DisabledKeepsCRLF` | `Default(CanonicalizeCfg{Enabled:false})` | `Names() == ["crlf"]` |
| `TestOptionsFrom_MapsAppendixC` | `config.Defaults().Store.Canonicalize` | `Strip` has the 6 Appendix C classes; `MinHash.Permutations == 128`; `NearDupThreshold == 0.9`; `ShingleSize == 5` |
| `TestGoldenCorpus_AllFiles` | every file under `testdata/corpora/toolout/` with its `.meta.json` | canonical output equals `testdata/golden/canon/<rel>.canon.txt` (`-update` regenerates) |
| `TestCanonConformance` | `canontest.RunCanonSuite(t, "default", …)` | passes with zero skips |
| `FuzzCanonicalizeRun` | seeds from the toolout corpus | never panics; idempotent; non-growing; `Restore` exact |
| `FuzzRestore` | arbitrary canonical bytes + arbitrary delta lists | never panics; returns an error or a valid byte slice |
| `BenchmarkRun_Bash100KB` | 100 KB of `bash/npm-install.txt` repeated | **< 3 ms/op** |
| `BenchmarkRun_GoTest` | `testrunner/go-test-pass.txt` | **< 1 ms/op** |
| `BenchmarkRestore_100KB` | | **< 1 ms/op** |

### `internal/symbols`

| Test | Setup / input | Expected |
|---|---|---|
| `TestExtract_Go` | a 60-line Go file with a method, a func, a type, a `const (` block of 3 | 6 symbols, kinds `func,func,type,const,const,const`, spans brace-matched |
| `TestExtract_TSClassMethods` | a TS class with 3 methods and an arrow-function const | class span contains all three method spans; arrow const has kind `func` |
| `TestExtract_TSKeywordDenySet` | `  if (x) {` inside a class body | not extracted |
| `TestExtract_Python` | module with `class A:` + 2 methods + a module-level `CONST = 1` | class span ends before `CONST`; methods nested inside |
| `TestExtract_Rust` | `pub async fn`, `pub struct`, `impl X for Y` | 3 symbols, kinds `func,type,type` |
| `TestExtract_JVM` | Java class + 2 methods; Kotlin `fun`; C# `public static async Task<int> Run(` | correct kinds |
| `TestExtract_CFamily` | C header with `#define`, `typedef struct`, a function definition | 3 symbols |
| `TestExtract_Ruby_EndInclusive` | `def foo` … `end` | span includes the `end` line |
| `TestExtract_Shell` | `function build() {` and `build2() {` | 2 `func` symbols |
| `TestExtract_PHP` | class + method + const | 3 symbols |
| `TestExtract_None` | `README.md`, `package.json` | empty non-nil slice |
| `TestExtract_Generic` | `.zig` file with `fn main() {` | 1 `func` symbol via generic braces |
| `TestExtract_BraceInStringIgnored` | `func a() { s := "}" ; }` | span ends at the real closing brace |
| `TestExtract_BraceInCommentIgnored` | `func a() { // }` then a real `}` | same |
| `TestExtract_UnterminatedBlock` | `func a() {` then EOF | span runs to EOF |
| `TestExtract_SortOrder` | nested class/method | sorted by `Offset` asc then `Len` desc |
| `TestExtract_CapAtMaxSymbols` | 25 000 one-line Go consts | exactly 20 000 symbols returned, no panic |
| `TestExtract_TruncatesAt4MiB` | 5 MiB Go file | no symbol has `Offset ≥ 4<<20` |
| `TestExtract_LineNumbersAre1Based` | 3 declarations on lines 1, 5, 9 | `Line == 1,5,9` |
| `TestEnclosing_SmallestSpanWins` | offset inside a method inside a class | the method is returned |
| `TestEnclosing_OutsideAnySymbol` | offset in a top-of-file comment | `(Symbol{}, false)` |
| `TestEnclosing_OutOfRange` | `off = -1` and `off = len(b)` | `(Symbol{}, false)` both |
| `TestReferences_WordBoundaries` | body containing `parse`, `parseX`, `Xparse`, `_parse` for name `parse` | count `1` |
| `TestReferences_ZeroFill` | names `["a","b"]`, body has only `a` | `{"a":1,"b":0}` |
| `TestReferences_DedupAndCaps` | duplicate names, a 300-byte name, an empty name | duplicates collapse; over-long and empty names absent |
| `TestPropSpansWellFormed` (rapid) | arbitrary bytes with random extensions | every span within bounds, `Len > 0`, sorted |
| `FuzzExtract` | seeds from `testdata/corpora/symbols/*` | never panics; spans in bounds |
| `TestSymbolsConformance` | `symbolstest.RunSymbolsSuite` | passes with zero skips |
| `BenchmarkExtract_100KB` | a 100 KB TypeScript file | **< 2 ms/op** |
| `BenchmarkEnclosing_100KB` | | **< 2 ms/op** |
| `BenchmarkReferences_100KB_50Names` | | **< 1 ms/op** |

### `test/dedup` — the with/without measurement harness

`test/dedup/` is an **additive directory under the existing `test/` grouping** of §3.1 (alongside `test/e2e`, `test/bench/hotpath`, `test/replay`). It lives outside `internal/` deliberately: measuring dedup requires importing both `chunk` and `canon` in one package, and the §3.2 import table forbids `canon` from importing `chunk`. Nothing imports `test/dedup`.


| Test | Setup | Expected |
|---|---|---|
| `TestDedupRatio_WithVsWithout` | for every group under `testdata/corpora/toolout/`: chunk each file with `chunk.New(DefaultParams())` twice — once on raw bytes, once on `canon.Default(defaults).Run(...)` output — and count distinct chunk hashes and their byte totals | per group and overall: `ratioWithout = rawBytes / uniqueBytesWithout`, `ratioWith = canonBytes / uniqueBytesWith`, `gain = ratioWith / ratioWithout`. **Asserts:** `gain ≥ 1.25` for the `testrunner` group; `gain ≥ 1.0` overall (canonicalization is never worse); `ratioWith > 1.0` for the `fileread` group (the two-version pair must share chunks) |
| `TestDedupReport_Written` | no flag — recompute the whole report in memory and compare against the committed bytes | equal byte-for-byte, so the report can never drift from the corpus. The **only** writer is `TestDedupRatio_WithVsWithout` under `-write-report`, exactly as `-update` works for the golden files; running the suite without the flag never touches `testdata/` |

**If `gain` for the `testrunner` group comes in below 1.25**, the corpus is under-exercising O2 — add a second rerun pair (a `pytest-rerun.txt` differing from `pytest-pass.txt` by one failure and its durations) rather than lowering the threshold. The threshold is the whole point of the deliverable: it is the number SP-08 cites when it argues that O2 pays for itself on test-output-heavy sessions (§10 Phase 1).

Report schema (committed at `testdata/canon-dedup-report.json`, consumed by SP-08):

```json
{
  "generated_by": "SP-04",
  "chunk_params": { "min": 1024, "target": 4096, "max": 16384 },
  "groups": [
    { "group": "testrunner", "files": 9,
      "raw_bytes": 0, "canon_bytes": 0,
      "chunks_without": 0, "unique_chunks_without": 0, "unique_bytes_without": 0,
      "chunks_with": 0,    "unique_chunks_with": 0,    "unique_bytes_with": 0,
      "ratio_without": 0.0, "ratio_with": 0.0, "gain": 0.0 }
  ],
  "overall": { "ratio_without": 0.0, "ratio_with": 0.0, "gain": 0.0 }
}
```

(The zeros above are the schema shape; `-write-report` fills real measured values, and the committed file contains those measured values.)

### Fixtures to create

- `testdata/corpora/toolout/bash/{npm-install,docker-build,ls-la,curl-verbose,ps-aux}.txt`
- `testdata/corpora/toolout/testrunner/{go-test-pass,go-test-fail,go-test-rerun,jest-pass,jest-fail,pytest-pass,pytest-fail,cargo-test-pass,cargo-test-fail}.txt` — `go-test-rerun.txt` is the same suite as `go-test-pass.txt` with one new failure and different durations: the §8.1 "same test suite, one new failure" case
- `testdata/corpora/toolout/grep/{grep-symbol,grep-many}.txt`
- `testdata/corpora/toolout/glob/glob-ts.txt`
- `testdata/corpora/toolout/fileread/{read-auth-ts,read-auth-ts-v2}.txt` — identical except two changed lines
- `testdata/corpora/toolout/webfetch/webfetch-docs.txt`
- `testdata/corpora/toolout/git/{git-status,git-diff,git-log}.txt`
- `testdata/corpora/toolout/ansi/ansi-colored-build.txt`
- One `<name>.meta.json` beside each corpus file, giving the exact `(tool, path)` pair passed to `Registry.Run`. The value is not free choice — it selects which per-tool canonicalizers `Applies` returns true for, so it is fixed per group:

  | Group | Files | `.meta.json` |
  |---|---|---|
  | `bash` | 5 | `{"tool":"Bash","path":""}` |
  | `testrunner` | 9 | `{"tool":"Bash","path":""}` |
  | `grep` | 2 | `{"tool":"Grep","path":""}` |
  | `glob` | 1 | `{"tool":"Glob","path":""}` |
  | `fileread` | 2 | `{"tool":"Read","path":"src/auth.ts"}` |
  | `webfetch` | 1 | `{"tool":"WebFetch","path":""}` |
  | `git` | 3 | `{"tool":"Bash","path":""}` |
  | `ansi` | 1 | `{"tool":"Bash","path":""}` |

  Total 24 files, which is the count commit 7 checks in.
- `testdata/golden/canon/**.canon.txt` — generated by `go test ./internal/canon -run TestGoldenCorpus_AllFiles -update`
- `testdata/golden/chunk/{gear-table.sha256,boundaries-1mib.json}`
- `testdata/corpora/chunk/*.bin` — 6 fuzz seeds: all-zeros 64 KB, all-`0xFF` 64 KB, incompressible seed-3 64 KB, a 200 KB UTF-8 source file, a 4 KB file, a 1-byte file
- `testdata/corpora/symbols/` — one file per dialect, copied from the extractor unit-test inputs
- `testdata/golden/contracts/canon/dedup-decisions.json`, regenerated in commit 7
- `testdata/canon-dedup-report.json`

**Capture procedure for the corpus (do this, do not synthesize by hand):** run each command in a scratch clone of this repository and a scratch Node/Python/Rust project, redirect combined stdout+stderr to the target file, cap each file at 64 KB (`head -c 65536`), and visually confirm no credential, token, email address or absolute home path of a real user beyond what the `tmpPaths` rules cover appears in the bytes. Commands: `npm install --no-audit`, `docker build .`, `ls -la`, `curl -v https://example.com`, `ps aux`, `go test ./... -v`, `npx jest`, `pytest -v`, `cargo test`, `grep -rn "Canonicalize" internal/`, `find . -name '*.ts'`, `git status`, `git diff HEAD~1`, `git log -n 20`. ANSI output requires forcing colour (`--color=always`, `FORCE_COLOR=1`). Commit the raw bytes unmodified.

**`.gitattributes` (one appended line, SP-04's only edit to an SP-01 file):**

```gitattributes
testdata/corpora/** -text
```

Without it, git's line-ending normalization rewrites the CRLF fixtures on Windows checkout and the `crlf` canonicalizer's golden tests become unfalsifiable.

---

## Commit plan

Work happens on **`feat/sp04-chunking-canonicalization-and-symbols`**, cut from `develop` with SP-01 already merged:

```
git fetch origin && git checkout develop && git pull --ff-only
git checkout -b feat/sp04-chunking-canonicalization-and-symbols
```

Exactly 7 commits, within the mandated 5–8 (§10 of 00-ARCHITECTURE). Subjects are imperative mood and every header line is ≤ 72 characters, per §10. Each commit compiles and passes `go run ./tools/devtool test` for the packages it touches, plus `go run ./tools/devtool lint` and `gofumpt -l` (must print nothing).

**TDD ordering is per commit and is not optional.** For each commit, in this order: (1) write the tests listed under *Write first*; (2) run them and **observe them fail** — a compile failure against the SP-01 `ErrNotImplemented` stub counts as a failure only for the first commit that touches a package, thereafter the test must fail on an assertion, not on a missing symbol; (3) paste the failing output into the working notes; (4) write the implementation; (5) re-run until green; (6) only then `git commit`. A commit whose tests were never observed red is a process failure even if the code is correct.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(chunk): implement FastCDC gear hash, Split and RootHash`

- [ ] Add `internal/chunk/gear.go`, `internal/chunk/params.go`, `internal/chunk/fastcdc.go`, `internal/chunk/doc.go`; replace the SP-01 stub bodies in `internal/chunk/chunk.go` with real implementations, keeping the §5.5 signatures byte-identical.
- [ ] Write first (must fail): `TestParamsValidate_Table`, `TestParamsNormalized_Table`, `TestNewClampsInvalidParams`, `TestGearTableGolden`, `TestSplit_Empty`, `TestSplit_ShorterThanMin`, `TestSplit_ExactlyMin`, `TestSplit_SizeBounds`, `TestSplit_Contiguity`, `TestSplit_AllZeros_HitsMaxOnly`, `TestSplit_Determinism`, `TestSplit_MeanChunkSize`, `TestChunkHashMatchesCoreHashBytes`, `TestRootHash_Empty`, `TestRootHash_DomainSeparation`, `TestRootHash_OrderSensitive`, `TestRefs_RoundTrip`.
- [ ] Create `testdata/golden/chunk/gear-table.sha256`: `go test ./internal/chunk -run TestGearTableGolden -update`. (`boundaries-1mib.json` is generated in commit 2, where `TestSplit_GoldenBoundaries` lives.)
- [ ] Run: `go run ./tools/devtool fmt lint test` and `go test -race ./internal/chunk`.
- [ ] Body explains why XOR replaces the classic FastCDC addition (exact 64-byte window ⇒ content-defined boundaries independent of chunk start) and why the gear seed is frozen. Footer: `Refs: SP-04, §6.1, §8.1 item 1, Appendix C store.chunk`.

### Commit 2 — `feat(chunk): add SplitStream, boundary properties, fuzz and benches`

- [ ] Add `internal/chunk/stream.go` (`SplitStream`), `internal/chunk/chunk_prop_test.go`, `internal/chunk/fuzz_test.go`, `internal/chunk/bench_test.go`, `testdata/corpora/chunk/*.bin`, and generate `testdata/golden/chunk/boundaries-1mib.json` with `go test ./internal/chunk -run TestSplit_GoldenBoundaries -update`.
- [ ] Write first (must fail): `TestSplitStream_MatchesSplit`, `TestSplitStream_CallbackError`, `TestSplitStream_ReadError`, `TestSplitStream_BufferAliasingDocumented`, `TestSplit_GoldenBoundaries`, `TestPropBoundaryStability_Insertion`, `TestPropBoundaryStability_Deletion`, `TestPropSizeBounds`, `TestPropAllBytesCovered`, `FuzzSplit`.
- [ ] Add benchmarks `BenchmarkSplit_100KB`, `BenchmarkGearScan_1MiB`, `BenchmarkSplit_1MiB`, `BenchmarkSplitStream_4MiB`, `BenchmarkRootHash_1000Chunks`; record results and assert each budget in the commit body.
- [ ] Run: `go test -race ./internal/chunk`, `go test -fuzz=FuzzSplit -fuzztime=120s ./internal/chunk`, `go test -bench=. -benchmem ./internal/chunk`.
- [ ] Footer: `Refs: SP-04, §8.1 performance budget, 00-ARCHITECTURE §5.5`.

### Commit 3 — `feat(canon): add registry, match composition and byte-exact Restore`

- [ ] Add `internal/canon/types.go`, `internal/canon/registry.go`, `internal/canon/apply.go`, `internal/canon/restore.go`, `internal/canon/errors.go`; replace the SP-01 stub bodies keeping §5.6 signatures byte-identical.
- [ ] Write first (must fail): `TestKnownClassesCoverConfigDefaults`, `TestParseClass_Table`, `TestOptionsFrom_MapsAppendixC`, `TestRegistry_RegisterDuplicateName`, `TestRegistry_RegisterNonMatcher`, `TestRegistry_NamesFromDoubles`, `TestRun_UnknownStripClass`, `TestRun_EmptyInput`, `TestSignature_OnlyWhenEnabled`, `TestOverlapResolution_LongerAtSameOffsetWins`, `TestOverlapResolution_EarlierOffsetWins`, `TestOverlapResolution_RankBreaksTies`, `TestNonGrowingGuard_DropsGrowingMatch`, `TestApplied_DeduplicatedRegistrationOrder`, `TestReduced_Value`, `TestRestore_NoDeltas`, `TestRestore_OutOfRange`, `TestRestore_Unordered`, `TestRestore_NegativeLen`, `FuzzRestore`. (`OptionsFrom`, `Run`'s `Strip` gate and the `Signature` pass-through all land in this commit, so their tests belong here — a test written in commit 5 against commit-3 code could not be observed failing first.)
- [ ] Overlap tests use a test-only two-rule canonicalizer defined in `internal/canon/testdouble_test.go`; no real canonicalizer exists yet in this commit.
- [ ] Run: `go run ./tools/devtool fmt lint test`, `go test -race ./internal/canon`, `go test -fuzz=FuzzRestore -fuzztime=120s ./internal/canon` (120 s, matching the Definition of Done; nightly CI then runs 10 min/target per §6.1 of 00-ARCHITECTURE).
- [ ] Body explains why composition is single-pass over the original input rather than a transform chain (unambiguous delta coordinates; `Restore` is order-independent; non-growth becomes structural). Footer: `Refs: SP-04, §8.1 item 1 (O2), 00-ARCHITECTURE §5.6`.

### Commit 4 — `feat(canon): add the seven generic and structural canonicalizers`

- [ ] Add `internal/canon/generic.go`, `internal/canon/generic_test.go`.
- [ ] Write first (must fail): `TestCRLF_Table`, `TestANSI_Table`, `TestTimestamps_Table`, `TestDurations_Table`, `TestPIDs_Table`, `TestAddresses_Table`, `TestTmpPaths_Table`, `TestNonGrowingGuard_SkipsShortMatches`, `TestRun_StripGatesOptionalClasses`, `TestLineEndingClass_Table`, `TestPropIdempotence_EveryCanonicalizer` (generic set only), `TestPropNonGrowing_EveryCanonicalizer` (generic set only), `TestPropRestoreIsExactInverse` (generic set only).
- [ ] Run: `go run ./tools/devtool fmt lint test`, `go test -race ./internal/canon`, `go run ./tools/devtool cover` and confirm `internal/canon` is on track for the 90% floor.
- [ ] Footer: `Refs: SP-04, §8.1 item 1, Appendix C store.canonicalize.strip`.

### Commit 5 — `feat(canon): add per-tool rules, Default registry and near-dup Decide`

- [ ] Add `internal/canon/tools.go`, `internal/canon/default.go`, `internal/canon/dedup.go`, `internal/canon/tools_test.go`, `internal/canon/dedup_test.go`.
- [ ] Write first (must fail): `TestRegistry_ForDeterministicOrder`, `TestRegistry_Names`, `TestBash_ProgressCollapse`, `TestBash_ProgressGatedByStrip`, `TestBash_TrailingWhitespaceIsAlwaysOn`, `TestBash_ProgressDoesNotEatCRLF`, `TestBash_TrailingWhitespace`, `TestTestRunner_Go`, `TestTestRunner_Jest`, `TestTestRunner_Pytest`, `TestTestRunner_Cargo`, `TestGrep_PathPrefixOnly`, `TestGlob_SeparatorsAndTrailing`, `TestFileRead_BOMAndTrailing`, `TestWebFetch_Table`, `TestGit_Table`, `TestMatcherClassAssigned` (unit inputs; extended to the corpus in commit 7), `TestDefault_DisabledKeepsCRLF`, `TestDecide_Table`.
- [ ] Extend the three property tests from commit 4 to the full 14-canonicalizer `Default` registry.
- [ ] Run: `go run ./tools/devtool fmt lint test`, `go test -race ./internal/canon`.
- [ ] Body records that `nearDupThreshold` and `permutations` are read from config and never written as literals (§11.6 forbids `0.9`). Footer: `Refs: SP-04, §8.1 item 1 (MinHash near-dedup), Appendix C store.canonicalize.minhash`.

### Commit 6 — `feat(symbols): add heuristic Extract, Enclosing and References`

- [ ] Add `internal/symbols/dialect.go`, `internal/symbols/extract.go`, `internal/symbols/span.go`, `internal/symbols/references.go`, `internal/symbols/extract_test.go`, `internal/symbols/fuzz_test.go`, `internal/symbols/bench_test.go`, `testdata/corpora/symbols/*`; replace the SP-01 stub bodies keeping §5.22b signatures byte-identical.
- [ ] Write first (must fail): every `TestExtract_*`, `TestEnclosing_*`, `TestReferences_*`, `TestPropSpansWellFormed`, `FuzzExtract`.
- [ ] Add `BenchmarkExtract_100KB`, `BenchmarkEnclosing_100KB`, `BenchmarkReferences_100KB_50Names`; assert the three budgets in the commit body.
- [ ] Run: `go run ./tools/devtool fmt lint test`, `go test -race ./internal/symbols`, `go test -fuzz=FuzzExtract -fuzztime=120s ./internal/symbols`, `go test -bench=. ./internal/symbols`.
- [ ] Footer: `Refs: SP-04, §8.7 minimal sufficient span, 00-ARCHITECTURE §5.22b`.

### Commit 7 — `test(canon): add the tool-output corpus and dedup-ratio harness`

- [ ] Capture and commit `testdata/corpora/toolout/**` (24 raw files + their `.meta.json`) per the capture procedure; append `testdata/corpora/** -text` to `.gitattributes`.
- [ ] Add `internal/canon/golden_test.go`, `internal/canon/fuzz_test.go` (`FuzzCanonicalizeRun`), `internal/canon/bench_test.go`, `test/dedup/dedup_test.go`, `test/dedup/report.go`.
- [ ] Implement the behaviour bodies and remove every `t.Skip` in `internal/canon/canontest/canontest.go` and `internal/symbols/symbolstest/symbolstest.go`; add `TestCanonConformance` and `TestSymbolsConformance` in the owning packages.
- [ ] Write first (must fail): `TestGoldenCorpus_AllFiles`, `TestDedupRatio_WithVsWithout`, `TestDedupReport_Written`, `FuzzCanonicalizeRun`, `BenchmarkRun_Bash100KB`, `BenchmarkRun_GoTest`, `BenchmarkRestore_100KB`.
- [ ] Extend `TestMatcherClassAssigned` from unit inputs to every corpus file.
- [ ] Generate goldens: `go test ./internal/canon -run TestGoldenCorpus_AllFiles -update`; generate and commit `testdata/canon-dedup-report.json` via `go test ./test/dedup -run TestDedupRatio_WithVsWithout -args -write-report`, then re-run the package **without** the flag and confirm `TestDedupReport_Written` is green against the committed bytes.
- [ ] Regenerate `testdata/golden/contracts/canon/*.json` and `testdata/golden/contracts/symbols/*.json` from the real implementations. Where a value differs from SP-01's placeholder fixture, update the fixture and state the change in the commit body — SP-01's canon fixtures are necessarily synthetic because SP-01 shipped no canonicalizers, and Rule W-2's "any fixture the real implementation cannot reproduce is a verification failure" is evaluated at V2 against SP-06's tests, not against SP-01's placeholders.
- [ ] Run the full local gate: `go run ./tools/devtool ci-local`, `go test -race ./...`, `go test -bench=. ./internal/chunk ./internal/canon ./internal/symbols`, `go run ./tools/devtool cover` (assert `chunk` ≥ 90%, `canon` ≥ 90%, `symbols` ≥ 75%).
- [ ] Footer: `Refs: SP-04, §10 Phase 1, §8.1 item 1, 00-ARCHITECTURE §5.22 W-2`.

**Before opening the merge into `develop`:** `git log --format=%B origin/develop..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'` must print nothing, and CI's `verify`, `test`, `cover`, `crossbuild`, `security` and `docs` jobs must be green on the branch.

---

## Subagent strategy

This subplan is **heavy**: three independent packages, fourteen canonicalizers, nine dialects, and a corpus. Partition it across four parallel subagents. The commit plan stays strictly sequential in the main session — subagents produce files and reports, they never run `git commit`.

**Subagent A — `internal/chunk`.**
Scope: `gear.go`, `params.go`, `fastcdc.go`, `stream.go`, `doc.go`, and every `*_test.go` in `internal/chunk`, plus `testdata/corpora/chunk/*.bin`.
Given: §5.5's signatures verbatim, the §3 clamping table, the `nextCut`/`Split`/`SplitStream` pseudocode above, and the five benchmark budgets.
Returns: the file list, the measured numbers for all five benchmarks, the generated `gear-table.sha256` and `boundaries-1mib.json` contents, and the observed distribution for `TestPropBoundaryStability_Insertion` (percentage of trials at ≤ 2 novel chunks, and the maximum observed).
Blocking condition it must report rather than fix: if `≤ 2` novel chunks is achieved in under 85% of trials, or `≤ 3` in under 95%, or any single trial exceeds 12 novel chunks, do **not** loosen the assertion — report the full histogram to the main session, which decides whether `maskS`/`maskL` bit counts or the `Min` floor need adjusting. The reference distribution the thresholds were set from is 84% at exactly 1 novel chunk, 93.3% at `≤ 2`, 97.3% at `≤ 3`, max 6; a result materially worse than that indicates a bug in the rolling hash or the priming window, not a bad threshold.

**Subagent B — `internal/canon` core.**
Scope: `types.go`, `registry.go`, `apply.go`, `restore.go`, `errors.go`, `default.go`, `dedup.go`, `testdouble_test.go`, `registry_test.go`, `restore_test.go`, `dedup_test.go`.
Given: §5.6 verbatim, the sort/accept pseudocode including `gateSet`, `applyMatches`, `Restore`, and `Decide` verbatim from this document.
Returns: the exported surface it produced (must match *Produces* exactly), and confirmation that `Options.Strip` gating is enforced **in the registry accept loop** (not in `Matches`), that `ClassCRLF`/`ClassPaths` are always-on, and that the non-growing guard and the zero-`Class` drop are all covered by named tests.
Must **not** write any canonicalizer: it consumes only the test double.

**Subagent C — `internal/canon` canonicalizers and corpus.**
Scope: `generic.go`, `tools.go`, `generic_test.go`, `tools_test.go`, `golden_test.go`, `fuzz_test.go`, `bench_test.go`, `testdata/corpora/toolout/**`, `testdata/golden/canon/**`.
Given: the two rule tables above verbatim, the per-tool **class-assignment table** (every emitted `Match` must carry the class listed there — a zero `Class` is dropped by the registry gate), the token set, the capture procedure, and the `Matcher` contract from Subagent B's `types.go` (the main session hands B's `types.go` and `apply.go` to C before C starts writing implementations; C may write its tests immediately).
Returns: per-canonicalizer, the exact regexes compiled and the tokens used; the corpus manifest with byte sizes; a confirmation that no fixture contains a credential; and the measured `Reduced` value per corpus file.
Hard rule it must uphold: every token is shorter than or equal to the shortest match its pattern can produce, or the guard is relied on deliberately and that reliance is named in a test.

**Subagent D — `internal/symbols`.**
Scope: `dialect.go`, `extract.go`, `span.go`, `references.go`, all `*_test.go` in `internal/symbols`, `testdata/corpora/symbols/*`, and the behaviour bodies of `internal/symbols/symbolstest/symbolstest.go`.
Given: §5.22b verbatim, the ten-dialect rule table, the three block styles, the caps, and the three benchmark budgets.
Returns: the dialect table as implemented, one worked example per dialect (input → expected symbols), and the three benchmark numbers.

**Stays in the main session, never delegated:**

1. The signature freeze — reading SP-01's shipped stubs for `chunk`, `canon`, `symbols` and confirming the produced signatures are byte-identical to §5.5/§5.6/§5.22b before any subagent starts. A subagent that "improves" a §5 signature causes a W-3 violation across three later waves.
2. Adapting to SP-01's actual `config.CanonicalizeCfg` field names in `default.go` and `params.go`.
3. `test/dedup` — it needs A's chunker and C's corpus and canonicalizers, so it is written after all three land.
4. Regenerating `testdata/golden/contracts/{canon,symbols}/`.
5. The `.gitattributes` edit.
6. The seven commits, in order, each with its own `devtool` run.
7. The final re-read of every produced file against the *Design context* section.

**Integration order:** A and D are fully independent and land first (commits 1, 2, 6 can be prepared in any order but are committed in the numbered sequence). B must land before C compiles. C's corpus capture can start immediately, in parallel with everything. Merge conflicts inside `internal/canon` are avoided by the file split: B owns `registry.go`/`apply.go`/`restore.go`/`types.go`/`errors.go`/`dedup.go`/`default.go`; C owns `generic.go`/`tools.go`. Neither edits the other's files; a needed change is reported to the main session.

---

## Exit criteria

**Quoted verbatim from `Qompack.md` §10 Phase 1 — the half of the criterion this slice owns:**

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

SP-04 owns **"measure with and without canonicalization"** and delivers it as `testdata/canon-dedup-report.json` plus `test/dedup`. The `≥ 4:1` ratio and the `< 15ms` hook p99 are **SP-08**'s and **SP-05**'s to achieve and assert; SP-04 must not claim them.

**Quoted verbatim from `Qompack.md` §8.1 — the performance clause this slice owns:**

> FastCDC over 100KB is well under 1ms

**Quoted verbatim from 00-ARCHITECTURE §5.5 and §5.6 — the normative properties this slice must prove:**

> boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

> `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**Local Definition of Done:**

- [ ] `go run ./tools/devtool ci-local` is green.
- [ ] `go test -race ./internal/chunk ./internal/canon ./internal/symbols ./test/dedup` passes on Linux, macOS and Windows via CI's `test` matrix.
- [ ] `gofumpt -l` prints nothing; `golangci-lint run` is clean; the `nomagic` pass is clean (no `1024`/`4096`/`16384`/`0.9` literals outside `*_test.go`).
- [ ] The import-graph check passes: `chunk` and `symbols` import foundation only; `canon` imports foundation plus `sketch`.
- [ ] Cross-platform determinism (§5.5 of 00-ARCHITECTURE) is asserted, not assumed: `TestGearTableGolden` and `TestSplit_GoldenBoundaries` compare against committed goldens and run on ubuntu, macos and windows in CI's `test` matrix. A golden mismatch on any one platform is a hard failure, never a per-platform golden.
- [ ] Coverage: `internal/chunk` ≥ 90%, `internal/canon` ≥ 90%, `internal/symbols` ≥ 75% (§6.4).
- [ ] `BenchmarkSplit_100KB` < 800 µs/op; `BenchmarkGearScan_1MiB` ≥ 400 MB/s; `BenchmarkSplit_1MiB` ≥ 120 MB/s and ≤ 2 allocs/op; `BenchmarkSplitStream_4MiB` ≤ 40 ms/op and ≤ 2 allocs/op amortized; `BenchmarkRootHash_1000Chunks` < 40 µs/op.
- [ ] `BenchmarkRun_Bash100KB` < 3 ms/op; `BenchmarkRun_GoTest` < 1 ms/op; `BenchmarkRestore_100KB` < 1 ms/op.
- [ ] `BenchmarkExtract_100KB` < 2 ms/op; `BenchmarkEnclosing_100KB` < 2 ms/op; `BenchmarkReferences_100KB_50Names` < 1 ms/op.
- [ ] `benchstat` against `testdata/bench-baseline.txt` shows no micro-benchmark regression > 25%.
- [ ] `FuzzSplit`, `FuzzCanonicalizeRun`, `FuzzRestore`, `FuzzExtract` each run 120 s with zero crashers; seed corpora committed.
- [ ] `canontest.RunCanonSuite` and `symbolstest.RunSymbolsSuite` contain zero `t.Skip` calls (W-1 merge blocker).
- [ ] `testdata/canon-dedup-report.json` is committed and `TestDedupReport_Written` reproduces it byte-for-byte; the `testrunner` group shows `gain ≥ 1.25`.
- [ ] `TestDedupRatio_WithVsWithout` shows overall `gain ≥ 1.0` — canonicalization is never worse than raw.
- [ ] `git log --format=%B origin/develop..HEAD` contains no `Co-Authored-By`, `Signed-off-by`, `Generated with`, or `🤖`.
- [ ] Commit count on the branch is exactly 7.
- [ ] CI green on `feat/sp04-chunking-canonicalization-and-symbols`.

---

## Done checklist

- [ ] Branch `feat/sp04-chunking-canonicalization-and-symbols` was cut from a `develop` that already contains SP-01.
- [ ] `Qompack.md` is unmodified (`git diff origin/develop -- Qompack.md` is empty).
- [ ] `internal/config`, `internal/core`, `internal/paths`, `internal/sketch` are unmodified — this branch touches no package it does not own, except one appended line in `.gitattributes`.
- [ ] Every signature in *Interface contract → Produces* matches §5.5, §5.6 and §5.22b of 00-ARCHITECTURE byte-for-byte; additive symbols are additive only, and no §5 signature was changed or removed.
- [ ] Spec coverage self-review: every constant quoted in *Design context* appears in the implementation or in a test — `1024/4096/16384` via `config.Defaults().Store.Chunk`; `permutations 128` and `nearDupThreshold 0.9` via `OptionsFrom`; the six `strip` classes via `KnownClasses`; the `< 1ms` FastCDC-over-100KB clause via `BenchmarkSplit_100KB`; the "minimum sufficient span" clause via `symbols.Enclosing`; the "measure with and without canonicalization" clause via `testdata/canon-dedup-report.json`.
- [ ] Placeholder scan: `grep -rniE 'TODO|TBD|FIXME|XXX|not implemented|handle edge cases' internal/chunk internal/canon internal/symbols test/dedup` returns nothing (the SP-01 `ErrNotImplemented` stub bodies are all replaced).
- [ ] Type consistency: `Chunk.Ref()` produces `core.ChunkRef`; `canon.MinHashOptions` is an alias of `sketch.MinHashOptions`; `canon.Result.Signature` is `sketch.Signature`; `symbols.Symbol.Kind` is one of `func|type|class|const|var` in every dialect.
- [ ] Every canonicalizer is registered in `Default` in the §5.6 order and appears in `Names()`.
- [ ] Every rule of every canonicalizer emits a `Match` carrying the `Class` assigned to it in §7/§8, and `TestMatcherClassAssigned` is green over the whole corpus — no rule is silently discarded by the `Strip` gate.
- [ ] Every canonicalizer has a named idempotence test, a named non-growth test, and a named `Restore`-inverse test.
- [ ] `testdata/corpora/toolout/**` contains no credential, API key, bearer token, private key, or real user email; `.gitattributes` carries `testdata/corpora/** -text`.
- [ ] Commit count verified: exactly 7, within the mandated 5–8.
- [ ] No co-author or attribution trailers on any commit, merge commit, tag or PR body.
- [ ] Out-of-scope discipline verified: no file under `internal/store`, `internal/sketch`, `internal/observer`, `internal/dag`, `internal/mcp`, `internal/analyzer`, `internal/redact`, `internal/config`, `internal/daemon`, `internal/ipc`, `internal/eval` was created or modified.

# SP-03: Sketch library: Bloom, Count-Min, HyperLogLog, Misra-Gries, MinHash with versioned serialization, resize and merge

> **Recommended model: Opus 5 · xhigh effort**
>
> Appendix A supplies every sizing formula verbatim; the work is careful bit-level implementation, a frozen CRC-checked binary format, and fuzz/property coverage. Fully-decided numerics + heavy test authoring is Opus 5's sweet spot — no need for a stronger tier.

**Branch:** `feat/sp03-sketch-library` (cut from `develop`) | **Wave:** 1 | **Prerequisites:** the branches of `["SP-01"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 1 (SP-02, SP-04, SP-05, SP-06, SP-07) | **Design sections:** §6.2, §8.1 item 5, Appendix A (bloom/CMS sizing), §11.4, §12 (bloom saturation) | **Gaps closed:** none directly. This slice supplies the §6.2 primitives through which SP-09 closes G6.1, G6.2, G6.3 and G2.2, and it owns the §12 "Bloom saturation" mitigation end to end.

---

## Mission

`internal/sketch` is the permanent-memory substrate of Qompack. §6.2 of `Qompack.md` names the property that makes it load-bearing: a fixed-size bit array **is not context**, so it survives arbitrarily many compactions without degradation — "The DPI cascade does not touch it because it was never compressed." §5.5's cache-compatibility audit rates the whole family **Invisible**: sketches "live outside the token stream entirely. Zero cache cost." Everything else in the plugin pays a token price; this package does not, which is why the design leans on it for the highest-value single feature in the plugin (negative knowledge) and for the cheap statistics that drive scheduling and warm start.

This subplan delivers all five sketches in full — Bloom, Count-Min, HyperLogLog, Misra-Gries, MinHash — each with the exact Appendix A sizing formulas, a versioned CRC-checked binary container, `MarshalBinary`/`UnmarshalBinary`, and `Save`/`Load` with corruption detection. On-disk stability across plugin versions is a hard requirement rather than a nicety: `sketches/tried.bloom` is one of the three append-only locations of §7.4, `store.ToolUseRecord.Signature` embeds a MinHash signature in an append-only index line, and a format that silently changes meaning between versions would corrupt negative knowledge — the one thing in the system that must never produce a false block. Serialization therefore gets its own commit, its own fuzz corpus, and frozen golden fixtures.

Two mechanisms in this package exist purely to serve later subplans, and both are named in the risk register. The first is the §12 **bloom-saturation** mitigation: `FillRatio`, `EstimatedFPRate`, `ResizeTarget` (grow 2× above 0.5 fill) and `RebuildBloom` from an arbitrary key iterator. §11.4 states the stakes — "At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize." SP-09 drives the rebuild from `records/eliminations.jsonl`; SP-03 owns the mechanics, including the rename-with-one-generation-backup replacement path that keeps `tried.bloom` inside the append-only invariant. The second is **merge and scale**: `CMS.MergeFrom`, `CMS.Scale`, `HLL.MergeFrom`, `MisraGries.MergeFrom` are the primitives Phase 7's cross-session warm start (O4) is built from — "Warm-start Count-Min with the project's historical hot-file distribution."

**What exists when you start.** `develop` contains SP-01's foundation: `internal/core` (`Hash`, `HashBytes`, `UnixMilli`, the sentinel errors), `internal/paths` (`WriteAtomic`, `Norm`, `Key`, long-path handling), `internal/config` (the whole Appendix C schema including `sketches.bloom/cms/hll` and `store.canonicalize.minhash`), `internal/logging` (with the `Loud` channel), `internal/obs`, `internal/testutil`, the devtool task runner, the `nomagic` lint pass, the import-graph check, and CI. `internal/sketch` exists as a compiling stub whose every function returns `core.ErrNotImplemented`, alongside `internal/sketch/sketchtest` whose behaviour tests are `t.Skip`ped (Rule W-1).

**What exists when you finish.** `internal/sketch` is complete, has no stubs and no skips, holds ≥ 90 % line coverage (00-ARCHITECTURE §6.4 floor for `sketch`), ships five fuzz targets with committed seed corpora, ships frozen golden fixtures under `testdata/golden/contracts/sketch/` that SP-04 and SP-06 test against under Rule W-2, and ships micro-benchmarks whose numbers feed the L0 hot-path budget model. No sibling wave-1 subplan is blocked on anything in it beyond the `Signature` type that SP-04 (`canon`) and SP-06 (`store`) embed.

---

## Design context (verbatim from Qompack.md)

Everything below is quoted exactly. Nothing in this subplan requires opening the design document.

**Section-reference convention, because the two documents share a numbering space.** A bare `§N` always means `Qompack.md` §N. A reference to the architecture document is always written `00-ARCHITECTURE §N`. The four numbers that collide and would otherwise mislead are called out explicitly wherever they appear: §6.4 (Qompack.md = dynamic slicing; 00-ARCHITECTURE = coverage floors), §11.3 (Qompack.md = evaluation guardrails; 00-ARCHITECTURE = config validation), §11.6, §12.3 and §13 (00-ARCHITECTURE only — `Qompack.md` has no subsections under §12 and no §13), and §7/§8 (Qompack.md = plugin architecture / component specifications; 00-ARCHITECTURE = benchmark harness / CI pipeline).

### §6.2 — Bloom filters and sketches for permanent memory

> **Closes:** G6.1, G6.2, G6.3, G2.2
>
> A Bloom filter over canonicalized attempted-approach descriptors costs ~12KB for 10,000 entries at 1% false positive rate. False positives are the *safe* direction: you skip something you might have retried.
>
> The property that matters: **it is immortal.** A fixed-size bit array is not context, so it survives arbitrarily many compactions without degradation. The DPI cascade does not touch it because it was never compressed.
>
> Companion sketches:
>
> | Sketch | Purpose | Size |
> |---|---|---|
> | Bloom | `already_tried(x)` membership | ~12KB / 10K entries @ 1% FP |
> | Count-Min | File-touch frequency (which files are hot) | ~54KB @ ε=0.001, δ=0.01 |
> | HyperLogLog | Breadth-of-exploration cardinality | ~2KB, 2.3% error |
> | Misra-Gries | Deterministic top-k with no false positives | O(k) |

### Appendix A — Bloom filter sizing

> ```
> m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
> n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7
> ```

### Appendix A — Count-Min sizing

> ```
> width = ⌈e/ε⌉      depth = ⌈ln(1/δ)⌉
> ε = 0.001, δ = 0.01  →  2718 × 5 ≈ 54 KB @ 4-byte counters
> ```

### §8.1 item 5 — Sketch updates (L0 Observer responsibilities)

> 5. **Sketch updates.** Feed Count-Min and HyperLogLog. Feed the Bloom filter *only* on explicit negative-knowledge events (§8.3).

### §8.1 — Performance budget

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

### §8.1 item 1 — MinHash near-duplicate detection (the consumer of `Signature`)

> For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text.

### §11.4 — Watch for

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### §12 — Risk register (the two rows this slice owns or serves)

> | Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |

> | **Stale negative knowledge blocks a now-viable approach** | High | Evidence-linked eliminations with `depends_on` hashes; Bloom rebuilt from active records on dependency change (§8.3). This risk is why the filter is a cache, never the source of truth. |

### §8.3 — Why rebuild is cheap (the constraint on `RebuildBloom`)

> 3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.

### §7.4 — Directory layout and the append-only invariant

> ```
> ├── sketches/
> │   ├── tried.bloom                # negative knowledge — NEVER regenerated
> │   ├── touch.cms                  # file-touch frequency
> │   └── explore.hll                # exploration cardinality
> ```
>
> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### §5.5 — Cache-compatibility audit (the row for this package)

> | Bloom / CMS / HLL sketches | **Invisible** | Live outside the token stream entirely. Zero cache cost. |

### Phase 2 and Phase 7 items this package must satisfy

> - Count-Min and HLL companions

> - **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions.

### Appendix C — the configuration keys that parameterize this package

> ```jsonc
>   "sketches": {
>     "bloom": { "capacity": 10000, "fpRate": 0.01 },
>     "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
>     "hll":   { "registers": 2048 }
>   },
> ```
>
> ```jsonc
>     "canonicalize": {
>       "enabled": true,
>       "strip": ["timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"],
>       "minhash": { "enabled": true, "permutations": 128, "nearDupThreshold": 0.9 }
>     }
> ```

### 00-ARCHITECTURE §3.3 — the `tried.bloom` replacement rule (normative, this package implements the mechanic)

> - `sketches/tried.bloom` may be *replaced* only by `negknow.RebuildBloom`, whose input is `records/eliminations.jsonl` filtered to `status:"active"` — never a checkpoint, never a summary, never context. The rebuild writes a new file and renames; the previous file is kept as `tried.bloom.<seq>.bak` for one generation.

### 00-ARCHITECTURE §2.5 — why these are implemented in-house (D7)

> **Implemented in-house, deliberately (D7):** FastCDC, Bloom, Count-Min, HyperLogLog, Misra-Gries, MinHash, Sequitur, BOCD, submodular lazy greedy, MCP server. Each needs one of: versioned on-disk serialization with a CRC and a documented upgrade path; `MergeFrom` for cross-session warm start (O4); rebuild-from-records with a *different* capacity (§8.3); domain-separated hashing shared with the store.

### 00-ARCHITECTURE §11.3 — config validation rules that bound this package's inputs

> `permutations ∈ [16, 512]`; `nearDupThreshold ∈ (0,1]`; … `bloom.capacity ≥ 100`, `fpRate ∈ (0, 0.25)`; `cms.epsilon ∈ (0,1)`, `cms.delta ∈ (0,1)`; `hll.registers` a power of two in `[64, 65536]`

### 00-ARCHITECTURE §11.6 — the no-hardcoding rule that binds this package

> The in-repo `nomagic` analysis pass fails the build on any float literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` or integer literal in `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` appearing outside `internal/config/defaults.go`, `*_test.go`, and explicitly annotated `//nomagic:allow <reason>` lines. … `450` is in that set … **the set is extended with `{8000, 12000}` when §11.5's `rehydrate` keys land in SP-01.**

> **V2 reconciliation:** the sentence in bold above was missing from this quote, and with it the literal `8000`. SP-01 landed the §11.5 `rehydrate` keys, so the extension is in force: `tools/lint/nomagic/literals.go` ships `forbiddenInts = {20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}` — eleven values, not ten — and `internal/sketch/doc.go` already lists it correctly. See `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.3a item 7(b) (**V2-ALL-06**). Nothing in `internal/sketch` spells `8000`, so no code changed; the omission was in this document only.

**Consequence, binding on every file in this subplan:** `10000`, `8000`, `2048`, `4096`, `1024`, `0.9` and `0.1` must never appear as literals in non-test code under `internal/sketch`. Every constructor takes its sizing as a parameter; the caller supplies it from `config`. The subplan adds exactly one deliberate exception: `FPWarnRate` in `bloom.go`, which carries an explicit `//nomagic:allow` annotation (§ Implementation spec, `bloom.go`).

> **V2 reconciliation — the tree is `internal/sketch/...`, and it holds two annotations, not one.** `nomagic` exempts only `_test.go` files, `internal/config/defaults.go`, and paths under `tools/`, `test/` or `testdata/` (`tools/lint/nomagic/nomagic.go`, `exemptFile`), so `internal/sketch/sketchtest/minhash.go` is scanned like production code and its `const mhJaccardTolerance = 0.1 //nomagic:allow §15 behaviour-table tolerance, not a config default` (`:23`) is a required annotation. It is **SP-01's**, landed with the conformance-suite stubs, and it is frozen: SP-03 must not remove or re-word it. So the count a verifier can confirm across `internal/sketch/...` is **two** annotations — `FPWarnRate` in `bloom.go` (SP-03) and `mhJaccardTolerance` in `sketchtest/minhash.go` (SP-01). Scoped to `package sketch` proper, which is what `internal/sketch/doc.go` states for itself, `FPWarnRate` remains the only one.

---

## Out of scope

| Item | Owned by |
|---|---|
| The elimination ledger, canonical descriptors `(normalized_path, symbol_or_null, approach_class, reason_hash)`, `Descriptor.Key()`, staleness flip, `negknow.RebuildBloom` and *when* to rebuild | **SP-09** |
| Deciding what goes into `tried.bloom`; the `records/eliminations.jsonl` source of truth; the three-way `already_tried` response | **SP-09** |
| Calling `CMS.Add`/`HLL.Add` on `PostToolUse`; §8.1 item 5's wiring | **SP-08** |
| The canonicalizer registry, per-tool rules, `canon.Result.Signature` production, and the near-dup *policy* (`nearDupThreshold` comparison at ingest) | **SP-04** |
| `store.PutResult.NearDup`, delta-against-prior-version storage, `ToolUseRecord.Signature` persistence | **SP-06** |
| `daemon.SketchSet`, in-memory residency, the mutex that serializes sketch access, load-at-session-start / save-at-idle scheduling | **SP-05** |
| Per-segment Bloom filters (LSM-style, §6.8) and populating `store.Segment.BloomRef` | **SP-16** |
| Actually performing the O4 cross-session warm start (choosing the decay factor, reading the historical distribution) | **SP-16** |
| Surfacing fill ratio and estimated FP rate in `/qompack:status` | **SP-14** |
| Sequitur grammar state (`grammar/actions.seq`) — a different in-house structure, not a sketch | **SP-15** |
| BOCD serializable posterior — also in-house, also not a sketch | **SP-12** |
| `internal/chunk`'s gear hash and `chunk.RootHash` | **SP-04** |
| The `sketches.*` and `store.canonicalize.minhash` config schema, its defaults, and the 00-ARCHITECTURE §11.3 validation rules that bound them | **SP-01** |
| The `nomagic` analysis pass itself, the import-graph check, and `testdata/bench-baseline.txt`'s creation | **SP-01** |

---

## Interface contract

### Consumes (exact signatures, from 00-ARCHITECTURE §4 and §5.2)

```go
// package core (§4)
type Hash [32]byte
func HashBytes(domain string, b []byte) Hash   // sha256(domain || 0x00 || b) — domain-separated
type UnixMilli int64
var ErrNotFound = errors.New("qompack: not found")

// package paths (§3.3)
// NOTE: three args — the mode is not optional. .qompack/tmp/<rand> → Sync → os.Rename
func WriteAtomic(p string, b []byte, perm fs.FileMode) error

// package logging (§5.2)
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    Loud(msg string, kv ...any)
}
func Nop() Logger
```

Nothing else. **`internal/sketch` imports exactly `core`, `paths`, `logging` and the standard library.** It does *not* import `config` (constructors take scalars, so the package is testable without a config document and cannot drift from Appendix C by copying defaults) and does *not* import `obs` (counters belong to the daemon that owns the sketches). A test enforces this — see `TestImports_FoundationOnly`.

Config values reach this package through the composition root only, and this is the exact mapping SP-05/SP-08/SP-09 will write:

| Config key (Appendix C) | Constructor argument |
|---|---|
| `sketches.bloom.capacity`, `sketches.bloom.fpRate` | `NewBloom(capacity, fpRate)` |
| `sketches.cms.epsilon`, `sketches.cms.delta` | `NewCMS(epsilon, delta)` |
| `sketches.hll.registers` | `NewHLL(registers)` |
| `store.canonicalize.minhash.{enabled,permutations}` | `MinHashOptions{Enabled, Permutations}` |
| `store.canonicalize.minhash.nearDupThreshold` | `Signature.IsNearDup(o, threshold)` |

### Produces (normative — 00-ARCHITECTURE §5.7 verbatim, plus owned additions)

The following block is reproduced exactly from 00-ARCHITECTURE §5.7 and may not be changed without an `arch/` amendment:

```go
type Kind uint8 // KindBloom, KindCMS, KindHLL, KindMisraGries, KindMinHash
type Header struct {
    Magic [4]byte // 'Q','P','K','S'
    Ver   uint16  // format version, bumped on any layout change
    Kind  Kind
    Params map[string]float64
    Count  uint64
    Created core.UnixMilli
    CRC32C  uint32
}
type Sketch interface {
    Header() Header
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func Save(p string, s Sketch) error   // atomic (paths.WriteAtomic) except tried.bloom (§3.3)
func Load(p string, s Sketch) error   // CRC + version checked; corrupt → ErrNotFound + Loud log

// ── Bloom ──────────────────────────────────────────────────────────────
type Bloom struct{ /* … */ }
func NewBloom(capacity int, fpRate float64) *Bloom  // m = -n·ln(p)/(ln2)², k = (m/n)·ln2
func (b *Bloom) Add(key []byte)
func (b *Bloom) Test(key []byte) bool
func (b *Bloom) Count() int
func (b *Bloom) FillRatio() float64
func (b *Bloom) EstimatedFPRate() float64            // watch-for §11.4
func (b *Bloom) Capacity() (n int, fp float64)
func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool) // grow 2× at fill > 0.5

// ── Count-Min ──────────────────────────────────────────────────────────
type CMS struct{ /* … */ }
func NewCMS(epsilon, delta float64) *CMS             // width=⌈e/ε⌉, depth=⌈ln(1/δ)⌉
func (c *CMS) Add(key []byte, n uint32)
func (c *CMS) Estimate(key []byte) uint32
func (c *CMS) MergeFrom(o *CMS) error                // O4 warm start; errors on shape mismatch
func (c *CMS) Scale(factor float64)                  // exponential decay for warm start
func (c *CMS) HeavyHitters(mg *MisraGries, n int) []Counted

// ── HyperLogLog ────────────────────────────────────────────────────────
type HLL struct{ /* … */ }
func NewHLL(registers int) *HLL                      // 2048 → ~2 KB, ~2.3% error
func (h *HLL) Add(key []byte)
func (h *HLL) Cardinality() uint64
func (h *HLL) MergeFrom(o *HLL) error

// ── Misra-Gries ────────────────────────────────────────────────────────
type Counted struct{ Key string; Count int }
type MisraGries struct{ /* … */ }
func NewMisraGries(k int) *MisraGries
func (m *MisraGries) Add(key string, n int)
func (m *MisraGries) Top(n int) []Counted             // no false positives, by construction
func (m *MisraGries) MergeFrom(o *MisraGries) error

// ── MinHash ────────────────────────────────────────────────────────────
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }
type Signature struct{ Perms uint16; Mins []uint64 }
func MinHash(data []byte, o MinHashOptions) Signature
func (s Signature) Jaccard(o Signature) float64
func (s Signature) IsNearDup(o Signature, threshold float64) bool
func (s Signature) MarshalBinary() ([]byte, error)
func (s *Signature) UnmarshalBinary([]byte) error
```

Owned additions (permitted by §5 — "a subplan may add methods to a struct it owns"; nothing below removes or changes anything above):

```go
const FormatVersion uint16 = 1
const TriedBloomBase = "tried.bloom"
const (
    KindInvalid    Kind = 0
    KindBloom      Kind = 1
    KindCMS        Kind = 2
    KindHLL        Kind = 3
    KindMisraGries Kind = 4
    KindMinHash    Kind = 5
)
func (k Kind) String() string
func (k Kind) Valid() bool

var (
    ErrBadMagic           = errors.New("qompack/sketch: bad magic")
    ErrUnsupportedVersion = errors.New("qompack/sketch: unsupported format version")
    ErrKindMismatch       = errors.New("qompack/sketch: kind mismatch")
    ErrCorrupt            = errors.New("qompack/sketch: CRC32C mismatch")
    ErrTruncated          = errors.New("qompack/sketch: truncated payload")
    ErrMalformed          = errors.New("qompack/sketch: malformed payload")
    ErrShapeMismatch      = errors.New("qompack/sketch: shape mismatch")
    ErrTooLarge           = errors.New("qompack/sketch: payload exceeds limit")
    ErrGenerational       = errors.New("qompack/sketch: tried.bloom must be replaced via ReplaceGenerational")
)

func EncodeHeader(h Header, body []byte) ([]byte, error)     // full frame incl. trailing CRC32C
func DecodeHeader(b []byte) (h Header, body []byte, err error)
func (h Header) Param(name string) (float64, bool)
func (h Header) MustParamInt(name string, min, max int) (int, error)
func LoadWithLog(p string, s Sketch, log logging.Logger) error
func ReplaceGenerational(p string, s Sketch, seq int) (backup string, err error)
func Quarantine(p string) (moved string, err error)

// Created is caller-supplied so that marshalled bytes are byte-stable in golden tests.
func (b *Bloom) SetCreated(ts core.UnixMilli)
func (c *CMS) SetCreated(ts core.UnixMilli)
func (h *HLL) SetCreated(ts core.UnixMilli)
func (m *MisraGries) SetCreated(ts core.UnixMilli)

const ResizeFillThreshold = 0.5
const ResizeGrowthFactor  = 2
const FPWarnRate          = 0.10
type BloomStats struct {
    Capacity int; FPRate float64
    MBits uint64; K uint8; SetBits uint64
    Count int; FillRatio, EstFPRate float64
    NeedsResize, Saturated bool
}
func (b *Bloom) Stats() BloomStats
func (b *Bloom) Saturated() bool
func (b *Bloom) Bits() (m uint64, k uint8)

func (c *CMS) Dims() (width, depth int)
func (c *CMS) Total() uint64
func (h *HLL) Registers() int
func (m *MisraGries) K() int
func (m *MisraGries) Total() int64
func (m *MisraGries) MaxError() int64   // the MG guarantee: true ≤ reported + MaxError

const DefaultShingleSize   = 8
const MinHashSampleTarget  = 8192
const MaxPermutations      = 512
const MinPermutations      = 16
type SigSketch struct{ Sig Signature; Created core.UnixMilli }  // Sketch-framed MinHash for Save/Load
func (s *SigSketch) Header() Header
func (s *SigSketch) MarshalBinary() ([]byte, error)
func (s *SigSketch) UnmarshalBinary([]byte) error

// package sketchtest — conformance suites (D9, Rule W-1). SIX suites; every one takes a
// name string as its second parameter, and RunMinHashSuite takes a plain function rather
// than a *testing.T-taking factory. See the V2 reconciliation note below.
func RunSketchSuite(t *testing.T, name string, factory func(t *testing.T) sketch.Sketch)
func RunBloomSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.Bloom)
func RunCMSSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.CMS)
func RunHLLSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.HLL)
func RunMisraGriesSuite(t *testing.T, name string, factory func(t *testing.T) *sketch.MisraGries)
func RunMinHashSuite(t *testing.T, name string, minHash func(data []byte, o sketch.MinHashOptions) sketch.Signature)
```

> **V2 reconciliation:** the five `Run*Suite` signatures other than `RunSketchSuite` were wrong in this block and **never matched the tree** — a defect in this plan document, not a divergence SP-03 introduced. Every suite ships with a `name string` second parameter, and `RunMinHashSuite` takes `func([]byte, sketch.MinHashOptions) sketch.Signature`, not a factory. `git show 4f98314:internal/sketch/sketchtest/*.go` confirms **SP-01 shipped them that way** and SP-03 never touched those declarations. The six live declarations are in `sketchtest/{suite,bloom,cms,hll,misragries,minhash}.go`. Consequence for the checkpoint: **V2-MERGE-08 must compare against 00-ARCHITECTURE §5.7 and the tree, not against this block.** See `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.3a item 7(a) (**V2-ALL-06**).

**Consumers and what they rely on:** SP-04 (`canon`) constructs `MinHashOptions` and calls `MinHash`, embedding `Signature` in `canon.Result`. SP-06 (`store`) embeds `Signature` in `PutResult` and `ToolUseRecord` and calls `Jaccard`/`IsNearDup`. SP-08 (`observer`) calls `CMS.Add` and `HLL.Add`. SP-09 (`negknow`) calls `NewBloom`, `Add`, `Test`, `RebuildBloom`, `ResizeTarget`, `Stats`, `ReplaceGenerational`, `LoadWithLog`. SP-14 reads `BloomStats`. SP-16 calls `CMS.MergeFrom`, `CMS.Scale`, `HLL.MergeFrom`, `MisraGries.MergeFrom`.

**Goroutine safety.** No *mutable* type in this package is safe for concurrent use: `Bloom`, `CMS`, `HLL`, `MisraGries` and `SigSketch` all require external synchronisation, and the daemon (SP-05) owns every live sketch behind its session-registry mutex. Every exported mutable type's doc comment states this. The pure surface — `MinHash`, `Signature.Jaccard`, `Signature.IsNearDup`, `EncodeHeader`, `DecodeHeader`, `hash128` — is safe for concurrent use and its doc comments say so, because `store` (SP-06) calls `MinHash` from its worker pool.

**How §5.7's `Load` contract is discharged (normative, read before writing `io.go`).** 00-ARCHITECTURE §5.7 annotates `Load` with "CRC + version checked; corrupt → ErrNotFound + Loud log", but the §5.7 signature carries no `logging.Logger` and this package refuses package-level mutable state (a global default logger would be exactly that). The contract is therefore split and **both halves are mandatory**:

- `Load(p, s)` delivers the CRC + version checking and the `core.ErrNotFound` mapping. It logs nothing, because it has nothing to log to.
- `LoadWithLog(p, s, log)` is the owned addition that delivers the Loud half, and it is **the form every composition root must call**. SP-05 (`daemon.SketchSet` load-at-session-start) and SP-09 (`negknow.Open`) call `LoadWithLog`, never `Load`; `Load` exists for tests and for callers that provably have no logger. `Load`'s doc comment states this in one sentence and names `LoadWithLog` so the reader cannot pick the silent path by accident, which would violate 00-ARCHITECTURE §13 invariant 10 ("Degradation is loud").

This is a documented refinement of §5.7, not a change to it: no signature is altered, nothing is removed, and the annotated behaviour is fully available. No `arch/` amendment is required.

---

## Implementation spec

All paths are repo-relative to `C:/Users/Quant/Documents/Programming/Projects/qompack`.

### `internal/sketch/doc.go` (create)

Package doc: the §6.2 immortality rationale in three sentences, the goroutine-safety rule (mutable types unsafe, pure surface safe), the "constructors take scalars, never `config`" rule, the ceiling rule from `errors.go` (every constructible sketch is marshallable), the `Load`-vs-`LoadWithLog` split, and the 00-ARCHITECTURE §11.6 literal prohibition with the list of forbidden values.

### `internal/sketch/errors.go` (create)

The nine sentinel errors listed in the interface contract, plus the size ceilings, all as package constants so both construction and decode share them:

```go
const (
    MaxFrameBytes     = 64 << 20 // universal decode ceiling; bounds every allocation below
    MaxBloomCapacity  = 1 << 24  // 16 777 216 configured entries
    MaxBloomBits      = uint64(1) << 28 // 32 MiB of words — see the ceiling rule below
    MaxCMSCells       = 1 << 23  // 8 388 608 cells × 4 B = 32 MiB
    MaxCMSDepth       = 64
    MaxHLLRegisters   = 65536
    MinHLLRegisters   = 64
    MaxMGCounters     = 1 << 20
    MaxMGKeyBytes     = 8192
)
```

None of these values collides with the 00-ARCHITECTURE §11.6 forbidden literal set. `MaxMGKeyBytes` is deliberately 8192 rather than 4096: 4096 is `store.chunk.target` and is therefore forbidden outside `internal/config/defaults.go`, and a Misra-Gries key (a path or a tool name) never approaches either bound.

**Ceiling rule — every constructible sketch must also be marshallable.** `MaxBloomBits` is `1 << 28`, not `1 << 31`, precisely so that the largest legal Bloom body (`m/8` = 32 MiB) plus a 32-byte prefix, four params and a 4-byte CRC stays comfortably inside `MaxFrameBytes`. `MaxCMSCells` is picked on the same basis (32 MiB of counters). Without this, `NewBloom(MaxBloomCapacity, 1e-6)` would build a filter that `Save` could write but `Load` would reject as `ErrTooLarge` — a sketch that cannot survive a restart, which is the one failure this package exists to prevent. `MaxHLLRegisters` (64 KiB of registers) is trivially inside the bound.

`MaxMGCounters` cannot be bounded the same way, because a Misra-Gries frame's size depends on key lengths as well as counter count. The uniform rule that covers it, and that every one of the five `MarshalBinary` implementations applies as its **last** step, is:

> `MarshalBinary` returns `ErrTooLarge` — never a partial frame, never a panic — when the assembled frame would exceed `MaxFrameBytes`. `EncodeHeader` performs this check once, so the five implementations inherit it by construction rather than each re-implementing it.

Correspondingly, `MarshalBinary` on a **nil receiver** returns `ErrMalformed` for all five types (`*Bloom`, `*CMS`, `*HLL`, `*MisraGries`, `*SigSketch`), and `UnmarshalBinary` on a nil receiver returns `ErrMalformed` rather than panicking. Both are asserted once per type in the conformance suite (`RunSketchSuite`).

### `internal/sketch/util.go` (create)

The three shared helpers, given a single canonical home so that no two files declare them:

```go
// clamp returns v confined to [lo, hi]. NaN is returned as lo, because a NaN sizing
// parameter must never propagate into an allocation size: uint64(math.NaN()) is
// implementation-defined in Go, so a NaN that reaches a make() length is a real crash.
// ±Inf is handled by the ordinary comparisons.
func clamp(v, lo, hi float64) float64 {
    if math.IsNaN(v) { return lo }
    if v < lo { return lo }
    if v > hi { return hi }
    return v
}

// clampInt is the integer twin. It exists because clamp is float64-typed and every
// integer sizing parameter in this package (capacity, permutations, shingle size,
// registers, k) would otherwise round-trip through float64 at the call site.
func clampInt(v, lo, hi int) int {
    if v < lo { return lo }
    if v > hi { return hi }
    return v
}

// satAdd64 adds without wrapping.
func satAdd64(a, b uint64) uint64 {
    if a > math.MaxUint64-b { return math.MaxUint64 }
    return a + b
}
```

**Failure-mode rule for the whole package:** no constructor ever panics and no constructor ever returns an error — out-of-range, NaN and infinite arguments are clamped to the nearest legal value, because a hook that dies takes observability with it (00-ARCHITECTURE §12.3, and §11.3's "Behaviour on invalid config is not 'crash'"). Every *decoder*, by contrast, is strict: it returns a sentinel error and allocates nothing before it has validated the declared sizes against `MaxFrameBytes` and against the actual remaining buffer length.

### `internal/sketch/hash.go` (create)

Domain-separated hashing shared with the store (D7). One `core.HashBytes` call yields the 128 bits that Kirsch–Mitzenmacher double hashing needs.

```go
const (
    domainBloom = "qompack.sketch.bloom.v1"
    domainCMS   = "qompack.sketch.cms.v1"
    domainHLL   = "qompack.sketch.hll.v1"
)

// hash128 returns two 64-bit values derived from one domain-separated SHA-256.
// h2 is forced odd so the double-hashing stride is never zero.
func hash128(domain string, key []byte) (h1, h2 uint64) {
    sum := core.HashBytes(domain, key)
    h1 = binary.LittleEndian.Uint64(sum[0:8])
    h2 = binary.LittleEndian.Uint64(sum[8:16]) | 1
    return h1, h2
}

// splitmix64 is the fixed, fully-specified mixer used to derive MinHash permutation
// coefficients. Its constants are frozen: changing them changes every stored Signature.
func splitmix64(x uint64) uint64 {
    x += 0x9E3779B97F4A7C15
    x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
    x = (x ^ (x >> 27)) * 0x94D049BB133111EB
    return x ^ (x >> 31)
}

// fnv1a64 is the shingle hash for MinHash: stable forever, ~5 ns for an 8-byte shingle.
// A cryptographic hash is unnecessary here because the value never leaves the signature,
// but the function must never change, because signatures are persisted in an append-only index.
const (
    fnvOffset64 = 14695981039346656037
    fnvPrime64  = 1099511628211
)
func fnv1a64(b []byte) uint64
```

Keys are opaque `[]byte`. Callers supply already-normalized keys: SP-09 passes `negknow.Descriptor.Key()` (itself `core.HashBytes("qompack.neg.v1", …)`), SP-08 passes `[]byte(paths.Key(path))`. Documented in the package doc; this package performs no normalization of its own.

### `internal/sketch/header.go` (create)

**The QPKS frame, byte for byte.** All multi-byte integers are little-endian.

```
offset  size          field
0       4             Magic          'Q'(0x51) 'P'(0x50) 'K'(0x4B) 'S'(0x53)
4       2             Ver            uint16, currently 1 (FormatVersion)
6       1             Kind           uint8, 1..5; 0 is invalid
7       1             Reserved       uint8, must be 0 on write, ignored on read
8       8             Count          uint64
16      8             Created        int64 (core.UnixMilli)
24      4             ParamCount     uint32, ≤ 64
28      4             BodyLen        uint32, ≤ MaxFrameBytes
32      variable      Params         ParamCount entries, sorted ascending by name (bytewise):
                                       1 byte  NameLen (1..32)
                                       N bytes Name    (ASCII, [a-z0-9.] only)
                                       8 bytes float64 (math.Float64bits, little-endian)
32+P    BodyLen       Body           sketch-specific payload
end-4   4             CRC32C         uint32, Castagnoli over bytes [0, len-4)
```

Params are sorted so that `MarshalBinary` is byte-stable for a given logical state — that is what makes the golden fixtures and the `marshal∘unmarshal∘marshal` identity test meaningful.

```go
func EncodeHeader(h Header, body []byte) ([]byte, error)
// Writes the fixed 32-byte prefix, sorted params, body, then appends
// crc32.Checksum(frame[:len(frame)-4], crc32.MakeTable(crc32.Castagnoli)).
// Returns ErrTooLarge if 32+paramBytes+len(body)+4 > MaxFrameBytes, ErrMalformed if any
// param name is empty, longer than 32 bytes, or contains a byte outside [a-z0-9.], and
// ErrMalformed if len(h.Params) > 64. It returns an error rather than a bare []byte so
// that the single frame-size guard lives here and all five MarshalBinary implementations
// inherit it by construction. h.CRC32C on input is ignored; the computed value is used.

func DecodeHeader(b []byte) (Header, []byte, error)
// Two passes, in this exact order. Pass 1 validates and measures WITHOUT ALLOCATING, so that
// every rejection path — which is the whole of the fuzz surface — costs zero allocations:
//   len(b) < 36                                    → ErrTruncated
//   len(b) > MaxFrameBytes                         → ErrTooLarge
//   b[0:4] != magic                                → ErrBadMagic
//   Ver == 0 || Ver > FormatVersion                → ErrUnsupportedVersion
//   !Kind.Valid()                                  → ErrMalformed
//   ParamCount > 64                                → ErrMalformed
//   walking the params block: any NameLen == 0 or > 32, any name byte outside [a-z0-9.],
//     names not strictly ascending                 → ErrMalformed
//   the params block running past len(b)-4         → ErrTruncated
//   BodyLen > MaxFrameBytes                        → ErrTooLarge
//   32+paramBytes+BodyLen != len(b)-4              → ErrTruncated
//   stored CRC != computed CRC                     → ErrCorrupt
// Pass 2 runs only after every check above has passed: it allocates the
// Params map with make(map[string]float64, ParamCount) and fills it.
// Returns Header with CRC32C set to the stored value and body as a subslice of b
// (callers that retain it must copy; every Unmarshal in this package copies).

func (h Header) Param(name string) (float64, bool)
func (h Header) MustParamInt(name string, min, max int) (int, error) // ErrMalformed outside range
```

`Header()` on a live sketch returns `CRC32C == 0`: the CRC covers the body and cannot be known without marshalling. Only `DecodeHeader` populates it. Documented on the interface.

**Upgrade path.** `FormatVersion` is 1. Every `UnmarshalBinary` switches on `h.Ver`: `case 1:` decodes the layout above; `default:` returns `ErrUnsupportedVersion`. When a layout ever changes, `FormatVersion` becomes 2, a `case 2:` arm is added, and the `case 1:` arm stays forever. Writers always emit `FormatVersion`. Readers accept `Ver ≤ FormatVersion`, never above — a newer plugin's file must not be silently half-read by an older binary.

### `internal/sketch/bloom.go` (create)

```go
type Bloom struct {
    mBits    uint64   // rounded up to a multiple of 64
    k        uint8
    words    []uint64
    count    uint64
    capacity int      // configured n
    fpRate   float64  // configured p
    created  core.UnixMilli
}
```

**Sizing — Appendix A verbatim.**

```go
func NewBloom(capacity int, fpRate float64) *Bloom {
    n := clampInt(capacity, 1, MaxBloomCapacity)
    // clamp, not a pair of comparisons: a NaN fpRate must become 1e-6 rather than
    // flowing into math.Log and then into uint64(NaN), which is undefined in Go.
    p := clamp(fpRate, 1e-6, 0.5)

    // m = −n·ln(p) / (ln 2)²
    mRaw := math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2))
    // k = (m/n)·ln 2
    k := clampInt(int(math.Round(mRaw/float64(n)*math.Ln2)), 1, 64)

    mBits := (uint64(mRaw) + 63) &^ 63           // round up to a whole 64-bit word
    if mBits > MaxBloomBits { mBits = MaxBloomBits }
    if mBits == 0 { mBits = 64 }
    return &Bloom{mBits: mBits, k: uint8(k), words: make([]uint64, mBits/64),
                  capacity: n, fpRate: p}
}
```

Clamping `p` at `0.5` rather than at some value just below 1 is deliberate: above `p = 0.5` the filter is worse than a coin flip and the sizing formula degenerates, so the nearest *useful* legal value is the right correction. `Capacity()` returns the clamped pair, so a caller can always see what it actually got.

For the Appendix C default `(10 000, 0.01)` this yields exactly: `mRaw = 95 851`, `k = 7`, `mBits = 95 872`, `len(words) = 1 498`, body = **11 984 bytes ≈ 11.7 KB**, matching "m ≈ 95_850 bits ≈ 12 KB, k = 7". These five numbers are asserted by `TestBloom_AppendixASizing`.

**Add / Test — Kirsch–Mitzenmacher enhanced double hashing.**

```go
func (b *Bloom) Add(key []byte) {
    h1, h2 := hash128(domainBloom, key)
    fresh := false
    for i := uint64(0); i < uint64(b.k); i++ {
        idx := (h1 + i*h2 + i*i) % b.mBits
        w, bit := idx>>6, uint64(1)<<(idx&63)
        if b.words[w]&bit == 0 { b.words[w] |= bit; fresh = true }
    }
    if fresh { b.count++ }
}

func (b *Bloom) Test(key []byte) bool {
    h1, h2 := hash128(domainBloom, key)
    for i := uint64(0); i < uint64(b.k); i++ {
        idx := (h1 + i*h2 + i*i) % b.mBits
        if b.words[idx>>6]&(uint64(1)<<(idx&63)) == 0 { return false }
    }
    return true
}
```

`Count()` returns `int(b.count)`: the number of `Add` calls that flipped at least one bit. It is exact for a filter with no insert-time false positives and undercounts by exactly the number of keys whose k bits were all already set — an event with the filter's own false-positive probability. Documented on the method; asserted by `TestBloom_CountIsDistinctInserts`.

**Saturation surface — the §12 mitigation.**

```go
const ResizeFillThreshold = 0.5
const ResizeGrowthFactor  = 2

// FPWarnRate is §11.4's watch-for threshold: "At 1% they are safe; at 10% the agent starts
// skipping viable approaches. Monitor fill ratio and resize."
const FPWarnRate = 0.10 //nomagic:allow §11.4 bloom false-positive watch threshold; unrelated to scheduler.cache.readMultiplier

func (b *Bloom) FillRatio() float64 {
    var set uint64
    for _, w := range b.words { set += uint64(bits.OnesCount64(w)) }
    return float64(set) / float64(b.mBits)
}

// EstimatedFPRate uses the OBSERVED fill ratio, φ^k, rather than the a-priori
// (1 − e^(−kn/m))^k, so a filter loaded from disk reports the truth about itself
// without needing to trust its own Count.
func (b *Bloom) EstimatedFPRate() float64 { return math.Pow(b.FillRatio(), float64(b.k)) }

func (b *Bloom) Saturated() bool { return b.EstimatedFPRate() >= FPWarnRate }

func (b *Bloom) Capacity() (int, float64) { return b.capacity, b.fpRate }

func (b *Bloom) ResizeTarget() (int, float64, bool) {
    if b.FillRatio() > ResizeFillThreshold {
        target := b.capacity * ResizeGrowthFactor
        if target > MaxBloomCapacity { target = MaxBloomCapacity }
        return target, b.fpRate, true
    }
    return b.capacity, b.fpRate, false
}

func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom {
    b := NewBloom(capacity, fpRate)
    for k := range keys { b.Add(k) }
    return b
}
```

Note the intended behaviour, asserted by `TestBloom_ResizeFiresBeforeCapacity`: with `(10 000, 0.01)` the fill ratio reaches 0.5 at ≈ 9 493 insertions, so `ResizeTarget` reports `needed = true` *before* the configured capacity is consumed. That is the point — §12 says "Monitor fill ratio; resize", and resizing after saturation is too late.

`Stats()` returns `BloomStats` populated from the above in a single pass over `words` (one `OnesCount64` loop shared between `SetBits`, `FillRatio` and `EstFPRate`).

**Serialization.** `Kind = KindBloom`; `Count = b.count`; `Params = {"capacity": float64(capacity), "fprate": fpRate, "k": float64(k), "m": float64(mBits)}`; body = `mBits/8` bytes, each word little-endian. Decode validates: `m % 64 == 0`, `0 < m ≤ MaxBloomBits`, `1 ≤ k ≤ 64`, `capacity ≥ 1`, `0 < fprate < 1`, `len(body) == m/8` (else `ErrMalformed`/`ErrTruncated`).

**The param name is `fprate`, all lower case, not `fpRate`.** The QPKS frame restricts param names to `[a-z0-9.]` (see `header.go`) and `TestHeader_RejectBadParamName` rejects an uppercase name, so a camel-cased param would make every Bloom frame undecodable by its own decoder. The Go field stays `fpRate` and the Appendix C key stays `sketches.bloom.fpRate`; only the on-wire name is folded. The same rule gives `{"delta","depth","epsilon","width"}` for CMS, `{"registers"}` for HLL, `{"err","k"}` for Misra-Gries and `{"perms"}` for MinHash — all already lower case, all already in ascending order.

### `internal/sketch/cms.go` (create)

```go
type CMS struct {
    width, depth int
    cells        []uint32 // row-major, len == width*depth
    total        uint64
    epsilon, delta float64
    created      core.UnixMilli
}

func NewCMS(epsilon, delta float64) *CMS {
    eps := clamp(epsilon, 1e-5, 0.5)
    dlt := clamp(delta,   1e-9, 0.5)
    width := int(math.Ceil(math.E / eps))       // ⌈e/ε⌉
    depth := int(math.Ceil(math.Log(1 / dlt)))  // ⌈ln(1/δ)⌉
    if depth < 1 { depth = 1 }
    if depth > MaxCMSDepth { depth = MaxCMSDepth }
    for width*depth > MaxCMSCells { width /= 2 }
    if width < 1 { width = 1 }
    return &CMS{width: width, depth: depth, cells: make([]uint32, width*depth),
                epsilon: eps, delta: dlt}
}
```

For the Appendix C defaults `ε = 0.001, δ = 0.01`: `width = ⌈2718.281828…⌉ = 2719`, `depth = ⌈4.60517…⌉ = 5`, body = `2719 × 5 × 4 = 54 380 bytes ≈ 53.1 KB`. Appendix A writes "2718 × 5 ≈ 54 KB"; ⌈e/ε⌉ is 2719, and 2718 is the same quantity shown to display precision. **The formula is normative, the illustration is not** — implement `⌈e/ε⌉`. `TestCMS_AppendixASizing` asserts `width == 2719 && depth == 5 && len(body) == 54380` and carries this comment.

```go
func (c *CMS) Add(key []byte, n uint32) {
    if n == 0 { return }
    h1, h2 := hash128(domainCMS, key)
    for j := 0; j < c.depth; j++ {
        idx := j*c.width + int((h1+uint64(j)*h2)%uint64(c.width))
        if c.cells[idx] > math.MaxUint32-n { c.cells[idx] = math.MaxUint32 } else { c.cells[idx] += n }
    }
    if c.total > math.MaxUint64-uint64(n) { c.total = math.MaxUint64 } else { c.total += uint64(n) }
}

func (c *CMS) Estimate(key []byte) uint32 {
    h1, h2 := hash128(domainCMS, key)
    // named `best`, not `min`: `min` is a predeclared identifier in Go 1.21+ and
    // shadowing it inside a hot loop is exactly the kind of thing revive/gocritic flag.
    best := uint32(math.MaxUint32)
    for j := 0; j < c.depth; j++ {
        idx := j*c.width + int((h1+uint64(j)*h2)%uint64(c.width))
        if c.cells[idx] < best { best = c.cells[idx] }
    }
    return best
}
```

Every counter update saturates rather than wraps. Wrapping would break the sketch's only exact guarantee — `Estimate(k) ≥ true(k)` — which the property test asserts.

```go
// MergeFrom is the O4 warm-start primitive: "Warm-start Count-Min with the project's
// historical hot-file distribution." Shapes must match exactly, because two CMS tables
// with different widths index the same key to different cells.
func (c *CMS) MergeFrom(o *CMS) error {
    if o == nil { return ErrShapeMismatch }
    if c.width != o.width || c.depth != o.depth { return ErrShapeMismatch }
    for i, v := range o.cells {
        if c.cells[i] > math.MaxUint32-v { c.cells[i] = math.MaxUint32 } else { c.cells[i] += v }
    }
    c.total = satAdd64(c.total, o.total)
    return nil
}

// Scale applies exponential decay so that a warm-started table weights history below the
// current session. The factor is clamped to [0, maxTotalFloat]: a negative or NaN factor
// behaves as 0 (discard history), and the upper clamp only exists so the float→uint64
// conversion below is defined — no realistic decay factor comes near it.
// maxTotalFloat bounds that conversion. A direct comparison against
// math.MaxUint64 is unsafe: 2^64−1 is not representable in float64 and uint64(2^64) is
// undefined, so the clamp uses a value that round-trips exactly.
const maxTotalFloat = float64(1 << 62)

func (c *CMS) Scale(factor float64) {
    f := clamp(factor, 0, maxTotalFloat)
    if f == 1 { return }
    for i, v := range c.cells {
        s := math.Round(float64(v) * f)
        if s > math.MaxUint32 { s = math.MaxUint32 }
        c.cells[i] = uint32(s)
    }
    t := math.Round(float64(c.total) * f)
    if t > maxTotalFloat { t = maxTotalFloat }
    c.total = uint64(t)
}

// HeavyHitters pairs the keyless CMS with a Misra-Gries candidate set: MG supplies the
// keys (with no false positives), CMS supplies the sharper frequency estimate.
// Returns nil when mg is nil or n <= 0. Sorted by count desc, then key asc.
func (c *CMS) HeavyHitters(mg *MisraGries, n int) []Counted
```

**Serialization.** `Kind = KindCMS`; `Count = total`; `Params = {"delta", "depth", "epsilon", "width"}` (sorted by name in the frame); body = `width*depth*4` bytes, row-major, row 0 first, each counter little-endian. Decode validates `1 ≤ depth ≤ MaxCMSDepth`, `1 ≤ width`, `width*depth ≤ MaxCMSCells`, `len(body) == width*depth*4`.

### `internal/sketch/hll.go` (create)

```go
type HLL struct {
    p       uint8    // log2(m)
    regs    []uint8  // len == m
    count   uint64   // Add calls, not cardinality
    created core.UnixMilli
}

func NewHLL(registers int) *HLL {
    m := registers
    if m < MinHLLRegisters { m = MinHLLRegisters }
    if m > MaxHLLRegisters { m = MaxHLLRegisters }
    if m&(m-1) != 0 { m = 1 << bits.Len(uint(m-1)) }      // round UP to a power of two
    if m > MaxHLLRegisters { m = MaxHLLRegisters }
    return &HLL{p: uint8(bits.TrailingZeros(uint(m))), regs: make([]uint8, m)}
}
```

`registers = 2048` → `p = 11`, body = 2 048 bytes, frame = 2 048 + 32 + 18 (`"registers"` param) + 4 = **2 102 bytes ≈ 2.05 KB**, matching "~2KB". Standard error `1.04/√2048 = 2.298 %`, matching "2.3% error". `TestHLL_AppendixSizing` asserts the register count, the frame size, and that `1.04/math.Sqrt(2048)` rounds to 0.023.

```go
func (h *HLL) Add(key []byte) {
    x, _ := hash128(domainHLL, key)
    idx := x & (uint64(len(h.regs)) - 1)                       // low p bits select the register
    w := x >> h.p                                              // remaining 64−p bits
    rho := uint8(bits.LeadingZeros64(w) - int(h.p) + 1)        // w==0 ⇒ rho = 65−p
    if rho > h.regs[idx] { h.regs[idx] = rho }
    h.count++
}

func (h *HLL) Cardinality() uint64 {
    m := float64(len(h.regs))
    var sum float64
    var zeros int
    for _, r := range h.regs {
        sum += math.Ldexp(1, -int(r))                          // 2^-r
        if r == 0 { zeros++ }
    }
    est := alpha(len(h.regs)) * m * m / sum
    if zeros > 0 && est <= 2.5*m {
        est = m * math.Log(m/float64(zeros))                   // linear counting, small range
    }
    // No large-range correction: the hash is 64-bit, so the 2^32 saturation regime
    // of the original 32-bit HyperLogLog never occurs at any cardinality we can reach.
    if est < 0 { est = 0 }
    return uint64(math.Round(est))
}

func alpha(m int) float64 {
    switch m {
    case 16: return 0.673
    case 32: return 0.697
    case 64: return 0.709
    default: return 0.7213 / (1 + 1.079/float64(m))
    }
}

// MergeFrom is the exact register-wise max: the HLL of a union equals the max of the
// two register arrays, so a merged sketch is bit-identical to one built from the union.
func (h *HLL) MergeFrom(o *HLL) error {
    if o == nil || o.p != h.p { return ErrShapeMismatch }
    for i, r := range o.regs { if r > h.regs[i] { h.regs[i] = r } }
    h.count = satAdd64(h.count, o.count)
    return nil
}
```

**Serialization.** `Kind = KindHLL`; `Count = count`; `Params = {"registers": float64(len(regs))}`; body = the register bytes verbatim. Decode validates `registers` is a power of two in `[MinHLLRegisters, MaxHLLRegisters]` and `len(body) == registers`.

### `internal/sketch/misragries.go` (create)

```go
type MisraGries struct {
    k        int
    counters map[string]int
    total    int64
    err      int64   // accumulated decrement, the additive error bound
    created  core.UnixMilli
}

func NewMisraGries(k int) *MisraGries {
    if k < 1 { k = 1 }
    if k > MaxMGCounters { k = MaxMGCounters }
    return &MisraGries{k: k, counters: make(map[string]int, k)}
}

// Add is the weighted Misra-Gries update. n <= 0 is a no-op.
func (m *MisraGries) Add(key string, n int) {
    if n <= 0 { return }
    if len(key) > MaxMGKeyBytes { key = key[:MaxMGKeyBytes] }
    m.total += int64(n)
    if c, ok := m.counters[key]; ok { m.counters[key] = c + n; return }
    if len(m.counters) < m.k { m.counters[key] = n; return }
    d := n
    for _, c := range m.counters { if c < d { d = c } }
    for kk, c := range m.counters {
        if c-d <= 0 { delete(m.counters, kk) } else { m.counters[kk] = c - d }
    }
    if n-d > 0 { m.counters[key] = n - d }
    m.err += int64(d)
}
```

The map iteration order is irrelevant: the set of counters deleted (`c ≤ d`) and their resulting values are order-independent, so the post-state is deterministic. `TestMG_DeterministicUnderMapOrder` runs the same stream 32 times and asserts byte-identical `MarshalBinary` output.

```go
// Top returns the summary sorted by count desc, then key asc. n <= 0 returns all counters.
// No false positives: every key returned was inserted at least once, because keys are stored
// verbatim and a key that was never Added can never enter the map.
// Counts are lower bounds: true(k) − MaxError() ≤ reported(k) ≤ true(k).
func (m *MisraGries) Top(n int) []Counted

func (m *MisraGries) MaxError() int64 { return m.err }
func (m *MisraGries) Total() int64    { return m.total }

// MergeFrom is the standard mergeable-summary construction: add counters pairwise, then, if
// more than k survive, subtract the (k+1)-th largest count from every counter and drop
// non-positives. This preserves the MG error bound across the merge.
func (m *MisraGries) MergeFrom(o *MisraGries) error {
    if o == nil || o.k != m.k { return ErrShapeMismatch }
    for kk, c := range o.counters { m.counters[kk] += c }
    if len(m.counters) > m.k {
        counts := make([]int, 0, len(m.counters))
        for _, c := range m.counters { counts = append(counts, c) }
        sort.Sort(sort.Reverse(sort.IntSlice(counts)))
        d := counts[m.k]                       // the (k+1)-th largest, 0-indexed
        for kk, c := range m.counters {
            if c-d <= 0 { delete(m.counters, kk) } else { m.counters[kk] = c - d }
        }
        m.err += int64(d)
    }
    m.total += o.total
    m.err += o.err
    return nil
}
```

The classical guarantee, asserted as a property: **any key with true frequency > total/(k+1) is present in the summary.** Combined with "every returned key was really seen", that is exactly §6.2's "Deterministic top-k with no false positives".

**Serialization.** `Kind = KindMisraGries`; `Count = uint64(total)`; `Params = {"err": float64(err), "k": float64(k)}`; body:

```
0   4            EntryCount uint32
per entry, sorted strictly ascending by key (bytewise):
    2            KeyLen uint16  (1..MaxMGKeyBytes)
    KeyLen       key bytes
    8            Count int64 (> 0)
```

Decode validates `EntryCount ≤ k`, every `KeyLen ∈ [1, MaxMGKeyBytes]`, every `Count > 0`, keys strictly ascending (a duplicate or an out-of-order key is `ErrMalformed`), and that the entries consume exactly `len(body)`. `err` is restored from the param so `MaxError()` survives a round trip.

### `internal/sketch/minhash.go` (create)

```go
const (
    DefaultShingleSize  = 8
    MinHashSampleTarget = 8192   // bounds cost at ~8192 shingles × P permutations
    MinPermutations     = 16
    MaxPermutations     = 512
)
```

`MinHash(data []byte, o MinHashOptions) Signature`:

1. If `!o.Enabled` → return the zero `Signature{Perms: 0, Mins: nil}`.
2. `w := o.ShingleSize; if w <= 0 { w = DefaultShingleSize }; w = clampInt(w, 2, 64)`.
3. `P := clampInt(o.Permutations, MinPermutations, MaxPermutations)` — the same `[16, 512]` bound 00-ARCHITECTURE §11.3 makes `config.Validate` enforce, applied here so a hand-edited config still cannot allocate an absurd signature. Note this is `clampInt`, not `clamp`: `Permutations` is an `int` and must not round-trip through `float64`.
4. `nsh := len(data) - w + 1`. If `nsh <= 0`, return `Signature{Perms: uint16(P), Mins: <P copies of math.MaxUint64>}` — the canonical "empty document" signature.
5. **Content-defined subsampling — bottom-k.** Keep the `MinHashSampleTarget` **smallest distinct** shingle hashes, where `h = fnv1a64(data[i:i+w])`; a document with fewer distinct shingle hashes than the target keeps all of them and runs no selection at all. Selection is a function of *content*, never of position, so it is shift-invariant: inserting a line into a document does not resample the unaffected shingles. The effective keep-threshold is the **target-th smallest distinct hash**, which moves *continuously* with the document, so two documents of similar size are measured at near-identical sampling densities and their estimates stay comparable. This is what keeps the estimator honest while bounding cost at ≈ 8 192 shingles regardless of input size.

   > **V2 reconciliation:** step 5 replaces this plan's original rule, which was **wrong** and shipped as a defect until the branch's final review caught it. The original read: `rate := (nsh + MinHashSampleTarget - 1) / MinHashSampleTarget`; if `rate <= 1` keep every shingle, otherwise keep iff `h <= math.MaxUint64/uint64(rate)`. That derives the keep-threshold from **document length**, making it a *step* function — two near-identical documents whose shingle counts straddle a multiple of 8 192 are sampled at densities differing by a whole integer factor, and their permutation minima then agree with probability ≈ 1/rate however similar the documents are. Reproduced on documents differing by 150 bytes with a true Jaccard of 0.98: **estimated 0.4062, `IsNearDup(0.9)` false**, recovering to 0.99 once both sat on the same side of a boundary. It recurs at every boundary — ~8, 16, 25, 33, 41, 49, 57 and 66 KB, i.e. the whole size range of an ordinary tool result — and it lands on *growing* documents, which is the one input §8.1 item 1 exists to detect. `TestMinHash_OneNewFailure` passed throughout, because its fixture sits below the first boundary; the tests that actually pin this are `TestMinHash_StraddlingTheSampleTargetIsContinuous` and `TestMinHash_BottomKMatchesTheSlowDefinition`.
   >
   > `docs/adr/0030-sketch-binary-format.md` **§9a** is the ruling record (§9 covers the FNV-1a shingle hash the rule is layered on). `Signature`, its compact wire form and `Jaccard`'s body are **unchanged** — 00-ARCHITECTURE §5.7's shape is untouched and every frozen fixture is byte-identical, because the 4 096-byte golden document sits below the target and runs no selection at all. Bottom-k also retires Ruling MH1's halve-the-rate retry as *unreachable* rather than leaving it in place: the smallest k distinct values of a non-empty set are a non-empty set, so a highly repetitive document can no longer keep nothing and masquerade as the empty-document signature of step 4. **The shipped code is correct; do not reconcile it back to the rate-based rule.** See `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.3a item 1 (**V2-ALL-06**).
6. For each kept shingle hash `h` and each permutation `i`, `v := a[i]*h + b[i]` (wrapping uint64 arithmetic), where `a[i] = splitmix64(uint64(2*i)) | 1` and `b[i] = splitmix64(uint64(2*i + 1))`. Keep the running minimum per `i`. The coefficient tables are computed once per call into a `P`-length scratch slice; for `P ≤ 512` that is a single 8 KB allocation.
7. Return `Signature{Perms: uint16(P), Mins: mins}`.

The body above lives in `func minHashWithStats(data []byte, o MinHashOptions) (Signature, int)`, which additionally returns the number of shingles actually kept; `MinHash` is the one-line wrapper that discards it. The count exists so `TestMinHash_SubsamplingEngages` can assert the cost bound directly instead of inferring it from a timing measurement.

`MinHashOptions.NearDupThreshold` is **read by no code in this package.** It travels in the options struct because 00-ARCHITECTURE §5.7 puts it there and because `canon.Options.MinHash` carries the whole struct from config to the call site, but the near-duplicate *policy* is SP-04's and SP-06's: they pass the value to `Signature.IsNearDup(o, threshold)` explicitly. `MinHash`'s doc comment says this in one line so nobody hunts for the missing use.

```go
// Jaccard is the fraction of permutation positions whose minima agree. Signatures of
// different widths, and the zero signature, are incomparable and yield 0.
func (s Signature) Jaccard(o Signature) float64 {
    if s.Perms == 0 || s.Perms != o.Perms || len(s.Mins) != int(s.Perms) || len(o.Mins) != int(o.Perms) {
        return 0
    }
    eq := 0
    for i := range s.Mins { if s.Mins[i] == o.Mins[i] { eq++ } }
    return float64(eq) / float64(s.Perms)
}

func (s Signature) IsNearDup(o Signature, threshold float64) bool {
    return s.Perms != 0 && s.Perms == o.Perms && s.Jaccard(o) >= threshold
}
```

Two empty documents therefore compare as `Jaccard == 1` (all positions are `MaxUint64`), which is the correct answer and is asserted.

**Compact serialization** (this is the form embedded in `store` records, so it carries no QPKS header — the record line already frames it):

```
0   2          Perms uint16
2   8*Perms    Mins, little-endian uint64s
```

`UnmarshalBinary` validates: `len(b) >= 2`; `Perms <= MaxPermutations`; `len(b) == 2 + 8*Perms` exactly (`ErrTruncated` otherwise). `Perms == 0` requires `len(b) == 2` and yields the zero signature. A zero-length input is `ErrTruncated`, never a silent empty signature.

**`SigSketch`** wraps a `Signature` to satisfy `Sketch` for `Save`/`Load` under `KindMinHash`: `Params = {"perms": float64(Sig.Perms)}`, `Count = uint64(Sig.Perms)`, body = the compact form above. It exists so that `KindMinHash` is reachable through the same CRC-checked container as the other four and so SP-16's per-segment work has a framed on-disk form available.

### `internal/sketch/io.go` (create)

```go
// sketchFilePerm is the mode every sketch file is written with: owner-only, matching the rest
// of the .qompack tree. paths.WriteAtomic takes the mode as its third argument (see Consumes).
const sketchFilePerm fs.FileMode = 0o600

// Save writes s atomically. It REFUSES tried.bloom: §7.4 makes that file append-only/additive
// and 00-ARCHITECTURE §3.3 permits its replacement only through the generational path, so the
// refusal is the mechanical enforcement rather than a comment asking for care.
func Save(p string, s Sketch) error {
    if filepath.Base(p) == TriedBloomBase { return ErrGenerational }
    b, err := s.MarshalBinary()
    if err != nil { return err }
    return paths.WriteAtomic(p, b, sketchFilePerm)   // sketchFilePerm fs.FileMode = 0o600
}

// Load is the §5.7 signature. It performs the CRC and version checking and maps every
// failure to core.ErrNotFound, but it CANNOT emit the Loud report §5.7 also asks for,
// because it has no Logger. Production call sites (SP-05's SketchSet, SP-09's Ledger)
// MUST call LoadWithLog instead; Load is for tests and for callers with no logger.
func Load(p string, s Sketch) error { return LoadWithLog(p, s, logging.Nop()) }

// LoadWithLog reports corruption on the Loud channel (§12: "Degradation is loud") and maps
// every failure to core.ErrNotFound, so a caller's natural "not found ⇒ start empty" path is
// also the corrupt-file path. The underlying sentinel stays inspectable via errors.Is.
func LoadWithLog(p string, s Sketch, log logging.Logger) error {
    st, err := os.Stat(p)
    if errors.Is(err, fs.ErrNotExist) { return fmt.Errorf("%w: %s", core.ErrNotFound, p) }
    if err != nil { return fmt.Errorf("%w: %s: %v", core.ErrNotFound, p, err) }
    if st.Size() > MaxFrameBytes {
        log.Loud("sketch file exceeds frame limit", "path", p, "bytes", st.Size())
        return fmt.Errorf("%w: %w: %s", core.ErrNotFound, ErrTooLarge, p)
    }
    b, err := os.ReadFile(p)
    if err != nil { return fmt.Errorf("%w: %s: %v", core.ErrNotFound, p, err) }
    if err := s.UnmarshalBinary(b); err != nil {
        log.Loud("sketch corrupt — rebuilding from records", "path", p, "err", err.Error(),
                 "bytes", len(b))
        return fmt.Errorf("%w: %w: %s", core.ErrNotFound, err, p)
    }
    return nil
}
```

`errors.Is(err, core.ErrNotFound)` and `errors.Is(err, ErrCorrupt)` are both true for a CRC failure. This is what makes 00-ARCHITECTURE §12.3's "bloom load fails → rebuild from `records/eliminations.jsonl`; if that fails, `already_tried` returns `absent` for everything — never a false positive" implementable by SP-09 with one branch.

```go
// ReplaceGenerational is the ONLY way tried.bloom may be written (00-ARCHITECTURE §3.3):
// write new, rename old to "<p>.<seq>.bak", keep exactly one generation.
// Returns the backup path ("" when p did not exist).
func ReplaceGenerational(p string, s Sketch, seq int) (string, error) {
    b, err := s.MarshalBinary()
    if err != nil { return "", err }
    backup := fmt.Sprintf("%s.%04d.bak", p, seq)
    hadOld := false
    if _, err := os.Stat(p); err == nil {
        _ = os.Remove(backup)                       // Windows rename-onto-existing fails
        if err := os.Rename(p, backup); err != nil { return "", err }
        hadOld = true
    }
    if err := paths.WriteAtomic(p, b, sketchFilePerm); err != nil {
        if hadOld { _ = os.Rename(backup, p) }      // roll back; never leave the store without a bloom
        return "", err
    }
    // Keep exactly one generation.
    olds, _ := filepath.Glob(p + ".*.bak")
    for _, o := range olds { if o != backup { _ = os.Remove(o) } }
    if !hadOld { return "", nil }
    return backup, nil
}

// Quarantine renames a corrupt sketch out of the way without assuming any directory layout
// (00-ARCHITECTURE §12.3 quarantine behaviour). Returns the new path.
func Quarantine(p string) (string, error)   // → p + ".corrupt." + strconv.FormatInt(unixmilli,10)
```

> **V2 reconciliation — the `ReplaceGenerational` body above cannot run, and the shipped one differs from it in three observable ways.** `sketches/tried.bloom` is append-only (00-ARCHITECTURE §3.3, §7.4) and **`paths.WriteAtomic` refuses that exact path outright** through `paths.IsProtected`, returning `core.ErrAppendOnly`; `paths.ReplaceBloom(l, b, seq)` is the single sanctioned exception. The sample body therefore fails on its own `paths.WriteAtomic` call for every input. Its arity is the second reason it could not run as first written: `paths.WriteAtomic` takes three arguments (`p, b, perm`), not two — the sample bodies above and the *Consumes* block have been corrected to pass `sketchFilePerm` (`0o600`, `internal/sketch/io.go:20`), matching `plans/V2-SP-06-content-addressed-store.md:220` and SP-01's own signature. What shipped:
>
> - **The mechanics stay in `internal/paths`.** `ReplaceGenerational` delegates to `paths.ReplaceBloom`, which renames the current file to its backup, stages and swaps the new content, and prunes to exactly one generation. `internal/sketch` adds only marshalling, rollback and returning the backup path. Re-implementing the rename here would mean two writers of one filename pattern and a guard with a hole in it.
> - **The backup name is `tried.bloom.<seq>.bak` — `%d`, not this plan's zero-padded `%04d`** — because `paths.pruneBloomBackups` is what parses that filename family, and it parses the unpadded form. A `%04d` name is invisible to the pruner.
> - **A `seq` that does not strictly exceed the highest surviving `.bak` is refused before anything on disk moves** (`ErrMalformed`, naming both sequences), pre-flighted against the exported `paths.HighestBloomBackupSeq`. The plan's ordering — proceed, then report — was a data-loss path: `ReplaceBloom` renames the live file to its backup *before* it stages, the prune then deletes that backup for being low-sequenced, and a staging failure at that point leaves the store with **no `tried.bloom` at all**.
>
> `docs/adr/0030-sketch-binary-format.md` §12 carries the full reasoning; see also `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` §2.3a item 6 and row V2-SP03-13 (**V2-ALL-06**). **Correct the plan, not the code.**

`Quarantine` takes the timestamp from `time.Now()` only here — the one place in the package that touches the clock, isolated so that no marshalled byte ever depends on it. It is deliberately not `core.Clock`-injected: nothing in this package's output or in any test assertion depends on the value, only on the shape `<p>.corrupt.<digits>`, and threading a `Clock` through a two-line rename helper would buy nothing.

**Directory contract (decided here so no caller has to guess).** Neither `Save` nor `ReplaceGenerational` creates directories. Both require that the target's parent directory and `.qompack/tmp/` (which `paths.WriteAtomic` stages through, 00-ARCHITECTURE §3.3) already exist; if either is missing, the underlying `os` error is returned unwrapped. Creating `.qompack/sketches/` and `.qompack/tmp/` is SP-05's job at daemon start, and `internal/paths` (SP-01) owns the creation helper. Consequently **`io_test.go` and `golden_test.go` obtain their working directory from `testutil.NewProject(t)`**, which sets `QOMPACK_PROJECT_ROOT` and lays down the `.qompack/` skeleton — a bare `t.TempDir()` would make every `WriteAtomic` fail for a reason that has nothing to do with this package.

**Package clause for those two files: `package sketch_test`, not `package sketch`.** `internal/testutil` imports `internal/store`, which imports `internal/sketch`; an in-package `_test.go` that imported `testutil` would therefore be an import cycle and would not compile. An *external* test package may import it, because Go permits `pkg_test` to depend on packages that depend on `pkg`. `io_test.go` and `golden_test.go` need only the exported API, so this costs nothing. Every other test file in this package (`header_test.go`, `bloom_test.go`, `cms_test.go`, `hll_test.go`, `misragries_test.go`, `minhash_test.go`, `property_test.go`, `fuzz_test.go`, `bench_test.go`, `imports_test.go`) stays in `package sketch`, because they reach for unexported identifiers (`hash128`, `minHashWithStats`, `words`, `cells`, `regs`) and must not import `testutil`.

### `internal/sketch/sketchtest/suite.go` (modify — SP-01 shipped it with skips)

Replace SP-01's `t.Skip("behaviour: SP-03")` bodies with the real conformance assertions. After this commit, `grep -R "t.Skip" internal/sketch` returns nothing (Rule W-1: "The owning subplan flips those skips off; it is a merge blocker if any remain").

Each `Run*Suite` is factory-driven so that SP-16's future per-segment bloom, and any warm-started CMS, can be run against the same contract:

- `RunSketchSuite` — marshal is non-empty; `DecodeHeader` on it succeeds; `Header().Magic == {'Q','P','K','S'}`; `Header().Ver == FormatVersion`; `Header().Kind.Valid()`; `Header().CRC32C == 0` on a live sketch and non-zero after `DecodeHeader`; every param name matches `^[a-z0-9.]{1,32}$` and the names are strictly ascending; unmarshal into a fresh instance then re-marshal is byte-identical; unmarshalling truncated, bit-flipped, wrong-magic, wrong-kind and wrong-version inputs returns the matching sentinel and never panics; `MarshalBinary`/`UnmarshalBinary` on a nil receiver return `ErrMalformed` rather than panicking.
- `RunBloomSuite` — no false negatives over 1 000 keys; `Count()` counts distinct inserts; `FillRatio` strictly increases with novel inserts; `EstimatedFPRate` in `[0, 1]`; `ResizeTarget` monotone in fill; `RebuildBloom` over the same key set produces a filter that answers `Test` identically.
- `RunCMSSuite` — `Estimate ≥ true` for every key of a fixed stream; `MergeFrom` of a mismatched shape is `ErrShapeMismatch`; `Scale(1)` is identity; `Scale(0)` zeroes every estimate; `HeavyHitters(nil, 5) == nil`.
- `RunHLLSuite` — `Cardinality()` of an empty sketch is 0; monotone non-decreasing under `Add`; `MergeFrom` mismatched `p` is `ErrShapeMismatch`; merge of disjoint halves equals the register array of the whole.
- `RunMisraGriesSuite` — every `Top` key was inserted; counts are lower bounds; the `> total/(k+1)` guarantee; `MergeFrom` mismatched `k` is `ErrShapeMismatch`.
- `RunMinHashSuite` — `Jaccard(s,s) == 1`; symmetry; disabled options yield the zero signature; the zero signature compares 0 against anything; compact round trip is byte-identical.

### `docs/adr/0030-sketch-binary-format.md` (create)

ADR numbering convention for this repository: `<SP number as 3 digits><sequence>`, so SP-03's ADRs are `0030`, `0031`, … and can never collide with a sibling's. Content: why QPKS carries an explicit version and CRC32C rather than gob/JSON (on-disk stability across plugin versions is a hard requirement of an append-only store); why params are a sorted name→float64 map rather than a per-kind struct (one decoder, forward-compatible, and every sizing parameter in Appendix A is a real number); why `Created` is caller-supplied (byte-stable goldens without a clock dependency); why param names are restricted to `[a-z0-9.]` and therefore why the Bloom param is `fprate` while the config key stays `fpRate`; why `MaxBloomBits` is `1 << 28` rather than `1 << 31` (so that every constructible sketch is also marshallable inside `MaxFrameBytes`); why `⌈e/ε⌉` is 2719 and Appendix A's "2718" is the same figure at display precision; why the shingle hash is FNV-1a rather than SHA-256 (cost, with the frozen-forever caveat); why mutable sketches are not goroutine-safe while the `MinHash`/`Jaccard` surface is; why `Load` and `LoadWithLog` are split rather than `Load` owning a package-global logger.

### Performance budgets (all measured; 00-ARCHITECTURE §13 invariant 9)

| Operation | Configuration | Budget | Rationale |
|---|---|---|---|
| `Bloom.Add`, `Bloom.Test` | capacity 10 000, fp 0.01 (k=7) | ≤ 1.0 µs/op, 0 allocs/op | one SHA-256 of a short key plus 7 bit tests |
| `CMS.Add`, `CMS.Estimate` | ε=0.001, δ=0.01 (5 rows) | ≤ 1.0 µs/op, 0 allocs/op | one SHA-256 plus 5 indexed accesses |
| `HLL.Add` | 2 048 registers | ≤ 1.0 µs/op, 0 allocs/op | one SHA-256 plus one max |
| `HLL.Cardinality` | 2 048 registers | ≤ 25 µs/op | 2 048 `Ldexp` accumulations |
| `MisraGries.Add` | k = 256, saturated | ≤ 5 µs/op amortized | the decrement phase is O(k) over a map |
| composite `L0SketchUpdate` | 1 CMS Add + 1 HLL Add + 1 Bloom Test | **≤ 5 µs/op, 0 allocs/op** | the whole §8.1 item 5 contribution to a `PostToolUse` |
| `MinHash` | 4 KiB input, 128 permutations | ≤ 1.5 ms/op | ≈ 4 089 shingles × 128, no subsampling |
| `MinHash` | 100 KiB input, 128 permutations | ≤ 2.5 ms/op | subsampled to ≈ 8 192 shingles |
| `Bloom.MarshalBinary` / `UnmarshalBinary` | 11 984-byte body | ≤ 60 µs/op | memcpy + CRC32C |
| `CMS.MarshalBinary` / `UnmarshalBinary` | 54 380-byte body | ≤ 250 µs/op | memcpy + CRC32C |
| `RebuildBloom` | 5 000 keys, capacity 10 000 | ≤ 15 ms/op | §8.3 "cheap … a linear pass over a few thousand structured entries" |

The composite line is the number this subplan owes the L0 model: at ≤ 5 µs, §8.1 item 5's sketch work is **0.03 % of the 15 ms B-A budget**, which is the quantitative form of §8.1's "the rest is one append and a few hash lookups". `MinHash` is explicitly *not* on the B-A path — it runs in the daemon's async worker under budget B-C (`l0_process`, p99 < 50 ms, soft), and the ADR records that placement.

---

## Test plan (TDD)

Every test below is written before the implementation it covers, inside the commit that adds that implementation, and must be observed failing first. Test files live beside the package. `testify/require` only (`assert` is banned, 00-ARCHITECTURE §6.1). No test sleeps. Unless a section header says otherwise, every test file is `package sketch`; `io_test.go` and `golden_test.go` are `package sketch_test` for the import-cycle reason given under `io.go`.

### `internal/sketch/header_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestHeader_FrameLayout` | `EncodeHeader(Header{Ver:1,Kind:KindBloom,Count:7,Created:1_700_000_000_000,Params:{"k":7,"m":95872}}, []byte{0xAA,0xBB})` | no error; bytes 0–3 == `QPKS`; `LE16(b[4:6])==1`; `b[6]==1`; `b[7]==0`; `LE64(b[8:16])==7`; `LE64(b[16:24])==1700000000000`; `LE32(b[24:28])==2`; `LE32(b[28:32])==2`; params appear as `k` then `m` (sorted); total length `32+ (1+1+8)+(1+1+8) +2+4 = 58` |
| `TestHeader_ParamsSortedDeterministically` | encode the same map twice with keys inserted in different orders | byte-identical output |
| `TestHeader_RoundTrip` | encode → `DecodeHeader` | header fields equal; body equal; `CRC32C` non-zero and equal to the trailing 4 bytes |
| `TestHeader_RejectShort` | `DecodeHeader(make([]byte,35))` | `ErrTruncated` |
| `TestHeader_RejectMagic` | valid frame with `b[0]='X'` | `ErrBadMagic` |
| `TestHeader_RejectVersionZero` / `_RejectVersionFuture` | `Ver=0`; `Ver=2` | `ErrUnsupportedVersion` both |
| `TestHeader_RejectKindZero` | `Kind=0` | `ErrMalformed` |
| `TestHeader_RejectUnsortedParams` | hand-built frame with params `m`,`k` | `ErrMalformed` |
| `TestHeader_RejectBadParamName` | `DecodeHeader` on a hand-built frame with param name `"K"` (uppercase) | `ErrMalformed` |
| `TestHeader_EncodeRejectsBadParamName` | `EncodeHeader` with `Params: {"fpRate": 0.01}` | `ErrMalformed` — the encoder refuses the camel-cased name at the source, which is why `bloom.go` must write `fprate` |
| `TestHeader_EncodeRejectsOversizeFrame` | `EncodeHeader(Header{Ver:1,Kind:KindBloom}, make([]byte, MaxFrameBytes))` | `ErrTooLarge`, nil frame |
| `TestHeader_RejectLyingBodyLen` | valid frame, `LE32(b[28:32]) = 1<<20` (deliberately **below** `MaxFrameBytes`, so the failure is the length-consistency check and not the `ErrTooLarge` check that precedes it) | `ErrTruncated`; `testing.AllocsPerRun(100, func(){ DecodeHeader(b) }) == 0` (pass 1 rejects before the params map is allocated) |
| `TestHeader_RejectOversizeBodyLen` | valid frame, `LE32(b[28:32]) = MaxFrameBytes+1` | `ErrTooLarge` — the ordering companion to the row above |
| `TestHeader_RejectOversizeFrame` | `make([]byte, MaxFrameBytes+1)` | `ErrTooLarge` |
| `TestHeader_DetectsSingleBitFlip` | valid frame; flip bit 3 of byte 40 | `ErrCorrupt` |
| `TestHash128_Stable` | `hash128(domainBloom, []byte("src/auth.ts"))` | equals the hex constants recorded on first run and committed in the test body (golden-in-source); h2 odd |
| `TestHash128_DomainSeparated` | same key under `domainBloom` vs `domainCMS` | different `h1` |

### `internal/sketch/bloom_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestBloom_AppendixASizing` | `NewBloom(10_000, 0.01)` | `m == 95_872` bits; the pre-rounding `⌈−n·ln p/(ln2)²⌉ == 95_851`; `k == 7`; `len(words) == 1_498`; body length 11 984; a comment citing "m ≈ 95_850 bits ≈ 12 KB, k = 7" |
| `TestBloom_SizingTable` | `(100,0.01)`, `(1_000,0.001)`, `(10_000,0.01)`, `(100_000,0.01)` | `k` == 7, 10, 7, 7 respectively; `m` a multiple of 64 in every case |
| `TestBloom_NoFalseNegatives` | add `"key-0".."key-9999"` | `Test` true for all 10 000 |
| `TestBloom_EmptyFilterTestsFalse` | fresh `NewBloom(1_000,0.01)` | `Test([]byte("x")) == false`; `Count()==0`; `FillRatio()==0`; `EstimatedFPRate()==0` |
| `TestBloom_CountIsDistinctInserts` | `Add("a")` ×3, `Add("b")` ×1 | `Count() == 2` |
| `TestBloom_FillRatioAtCapacity` | `NewBloom(10_000,0.01)`, insert exactly 10 000 distinct keys | `FillRatio() ∈ [0.51, 0.53]` (analytic 1−e^(−7·10000/95872) = 0.5182) |
| `TestBloom_EstimatedFPRateMatchesEmpirical` | same filter; probe 100 000 keys `"probe-%d"` never inserted | empirical FP ∈ [0.008, 0.013]; `EstimatedFPRate() ∈ [0.009, 0.012]`; `|est − empirical| ≤ 0.004` |
| `TestBloom_ResizeFiresBeforeCapacity` | insert 5 000 → `ResizeTarget` = `(10_000, 0.01, false)`; insert to 9 493 → still false or just true; insert 10 000 | at 10 000: `(20_000, 0.01, true)`; a comment records the analytic crossing at n ≈ 9 493 |
| `TestBloom_ResizeCapsAtMax` | `NewBloom(MaxBloomCapacity, 0.01)`, then set every word to `^uint64(0)` **directly** (the test is in `package sketch`) so `FillRatio() == 1` | `ResizeTarget()` returns `(MaxBloomCapacity, 0.01, true)` — the doubling is capped, not applied. Filling this filter by insertion would need ~16 M `Add` calls and ~20 MB of hashing; the direct write exercises exactly the branch under test in microseconds |
| `TestBloom_SaturatedThreshold` | `NewBloom(10_000, 0.01)`; add `"sat-%d"` keys until `FillRatio() >= 0.72` (≈ 17 434 keys, from `n = −m·ln(1−φ)/k = −95 872·ln(0.28)/7`), then a second filter stopping while `FillRatio() ∈ [0.695, 0.705]` | first: `Saturated() == true` (0.72⁷ = 0.1003 ≥ `FPWarnRate`); second: `Saturated() == false` (0.70⁷ = 0.0824) |
| `TestBloom_RebuildFromIterator` | 3 000 keys; `RebuildBloom(20_000, 0.01, slices.Values(keys))` | every key `Test`s true; `Count() == 3_000`; `Capacity() == (20_000, 0.01)`; `FillRatio()` lower than the 10 000-capacity filter over the same keys |
| `TestBloom_RebuildEmptyIterator` | empty `iter.Seq` | non-nil filter, `Count()==0`, marshals and round-trips |
| `TestBloom_StatsConsistent` | after 5 000 inserts | `Stats().SetBits == popcount(words)`; `Stats().FillRatio == FillRatio()`; `Stats().EstFPRate == EstimatedFPRate()`; `NeedsResize` agrees with `ResizeTarget` |
| `TestBloom_ClampsIllegalArgs` | `NewBloom(-5, 0)`, `NewBloom(0, 2)`, `NewBloom(1, math.NaN())`, `NewBloom(1, math.Inf(1))`, `NewBloom(1, math.Inf(-1))`, `NewBloom(math.MaxInt, 0.01)` | no panic in any case; `Capacity()` returns `(1, 1e-6)`, `(1, 0.5)`, `(1, 1e-6)`, `(1, 0.5)`, `(1, 1e-6)`, `(MaxBloomCapacity, 0.01)` respectively; every filter accepts `Add`/`Test` and round-trips through `MarshalBinary` |
| `TestBloom_MarshalRoundTrip` | 10 000 keys, `SetCreated(1_700_000_000_000)` | re-marshal byte-identical; `Test` agrees on all 10 000 plus 1 000 negatives; `Count()` preserved |
| `TestBloom_UnmarshalRejects` | body length ≠ m/8; `m` not a multiple of 64; `m > MaxBloomBits`; `k=0`; `k=65`; `capacity=0`; `fprate=1`; `fprate=NaN` | `ErrMalformed` / `ErrTruncated` as specified; never a panic |
| `TestBloom_UnmarshalKindMismatch` | marshal a `CMS`, unmarshal into `*Bloom` | `ErrKindMismatch` |

### `internal/sketch/cms_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestCMS_AppendixASizing` | `NewCMS(0.001, 0.01)` | `Dims() == (2719, 5)`; body length 54 380; a comment recording that Appendix A's "2718 × 5 ≈ 54 KB" is ⌈2718.28…⌉ shown to display precision |
| `TestCMS_SizingTable` | `(0.01,0.01)`, `(0.001,0.001)`, `(0.0001,0.01)` | widths 272, 2719, 27183; depths 5, 7, 5 |
| `TestCMS_EstimateNeverUnderestimates` | 5 000 distinct keys, total 100 000 weighted adds from a fixed pattern | `Estimate(k) ≥ true(k)` for all 5 000 |
| `TestCMS_ErrorBoundHolds` | same stream, N = 100 000, ε·N = 100 | ≥ 99 % of keys satisfy `Estimate(k) − true(k) ≤ 100` |
| `TestCMS_AbsentKeyEstimate` | fresh CMS | `Estimate([]byte("nope")) == 0` |
| `TestCMS_AddZeroIsNoop` | `Add(k, 0)` | `Total() == 0`; `Estimate(k) == 0` |
| `TestCMS_Saturation` | `Add(k, math.MaxUint32)` twice | `Estimate(k) == math.MaxUint32`, no wraparound |
| `TestCMS_MergeFromIsAdditive` | stream A into c1, stream B into c2, A+B into c3; `c1.MergeFrom(c2)` | `c1.Estimate(k) == c3.Estimate(k)` for every key in A∪B; `c1.Total() == c3.Total()` |
| `TestCMS_MergeFromShapeMismatch` | `NewCMS(0.001,0.01).MergeFrom(NewCMS(0.01,0.01))`; and `MergeFrom(nil)` | `ErrShapeMismatch` both |
| `TestCMS_ScaleDecays` | counts 100, `Scale(0.5)` | every estimate 50; `Total()` halved; `Scale(1)` identity; `Scale(0)` zeroes; `Scale(-1)` behaves as `Scale(0)` |
| `TestCMS_HeavyHittersPairsWithMG` | `NewCMS(0.001, 0.01)` and `mg := NewMisraGries(64)` fed the same stream: `"a"`×500, `"b"`×300, `"c"`×100, plus 200 singletons `"one-%d"` | `HeavyHitters(mg, 3)` == `[{a,500},{b,300},{c,100}]` in that order. **The stream shape is load-bearing, do not "simplify" it.** `c` must clear the Misra-Gries retention threshold `total/(k+1) = 1100/65 = 16.9`, which a count of 10 would not; and the singleton count must stay far below the CMS width (2 719) so that the probability of `a`, `b` or `c` colliding in *all five* rows — which is what would inflate the reported count above the true one — is ~2×10⁻⁶ rather than the ~4 % it would be at 2 000 singletons |
| `TestCMS_HeavyHittersNilAndZero` | `HeavyHitters(nil, 5)`, `HeavyHitters(mg, 0)` | both `nil` |
| `TestCMS_MarshalRoundTrip` | 5 000-key stream, `SetCreated` fixed | byte-identical re-marshal; all estimates preserved; `Total()` preserved |
| `TestCMS_UnmarshalRejects` | `width*depth*4 ≠ len(body)`; `depth = 0`; `width*depth > MaxCMSCells` | `ErrMalformed`/`ErrTruncated`; no allocation before rejection |

### `internal/sketch/hll_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestHLL_AppendixSizing` | `NewHLL(2048)` | `Registers() == 2048`; marshalled frame length 2 102; standard error `1.04/√2048` rounds to 0.023 |
| `TestHLL_RoundsUpToPowerOfTwo` | `NewHLL(1000)`, `NewHLL(3000)`, `NewHLL(1)`, `NewHLL(1<<20)` | 1024, 4096, 64 (`MinHLLRegisters`), 65536 (`MaxHLLRegisters`) |
| `TestHLL_EmptyCardinality` | fresh | `Cardinality() == 0` |
| `TestHLL_SmallRangeLinearCounting` | 100 distinct keys, 2 048 registers | `|est − 100| / 100 ≤ 0.05` |
| `TestHLL_ErrorBounds` | keys `"hll-%d"` for n ∈ {1 000, 10 000, 100 000, 1 000 000} | relative error ≤ 0.07 (3× the 2.3 % standard error) at every n; test comment records the observed values |
| `TestHLL_DuplicatesDoNotInflate` | add the same 500 keys ten times | `Cardinality()` within 7 % of 500; `Header().Count == 5000` |
| `TestHLL_MergeIsExactUnion` | disjoint halves into h1,h2; the union into h3; `h1.MergeFrom(h2)` | register arrays of h1 and h3 are byte-identical; `Cardinality()` equal |
| `TestHLL_MergeShapeMismatch` | `NewHLL(2048).MergeFrom(NewHLL(1024))`; `MergeFrom(nil)` | `ErrShapeMismatch` both |
| `TestHLL_MarshalRoundTrip` | 100 000 keys, fixed `Created` | byte-identical re-marshal; identical `Cardinality()` |
| `TestHLL_UnmarshalRejects` | `registers = 1000` (not a power of two); `len(body) ≠ registers`; `registers = 1<<20` | `ErrMalformed`/`ErrTruncated` |

### `internal/sketch/misragries_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestMG_UnderCapacityIsExact` | k=8, add `"a"`×3, `"b"`×1 | `Top(0) == [{a,3},{b,1}]`; `MaxError() == 0` |
| `TestMG_DecrementPhase` | k=2; add `a,b,c` each ×1 | after `c`: at most 2 counters; `MaxError() == 1`; every reported count ≤ true count |
| `TestMG_WeightedAdd` | k=2; `Add("a",10)`, `Add("b",4)`, `Add("c",3)` | counters `{a:7, b:1}`; `MaxError()==3`; `Total()==17` |
| `TestMG_NonPositiveWeightIgnored` | `Add("a", 0)`, `Add("a", -5)` | `Total()==0`; `Top(0)` empty |
| `TestMG_FrequentItemGuarantee` | k=16; 10 000 adds where `"hot"` gets 1 200 (> 10 000/17 = 588) | `"hot"` present in `Top(16)` |
| `TestMG_NoFalsePositives` | k=4; stream of 1 000 items over 50 keys | every key in `Top(0)` occurs in the stream |
| `TestMG_TopOrdering` | counts `{b:5, a:5, c:9}` | `Top(0) == [{c,9},{a,5},{b,5}]` (count desc, key asc) |
| `TestMG_TopN` | 10 counters | `Top(3)` returns 3; `Top(0)` and `Top(-1)` return all; `Top(99)` returns 10 |
| `TestMG_DeterministicUnderMapOrder` | same 5 000-item stream replayed into 32 fresh summaries | all 32 `MarshalBinary` outputs byte-identical |
| `TestMG_MergeFrom` | k=8; disjoint streams into m1, m2; merged vs. a single summary over the concatenation | every key in the merged `Top` is present in the single-pass `Top`; merged `Total()` equals the sum; merged `MaxError()` ≥ each input's |
| `TestMG_MergeShapeMismatch` | differing k; nil | `ErrShapeMismatch` both |
| `TestMG_KeyTruncation` | key of `MaxMGKeyBytes+10` bytes | stored key length is exactly `MaxMGKeyBytes`; round-trips |
| `TestMG_MarshalRoundTrip` | k=64, 5 000-item stream, fixed `Created` | byte-identical re-marshal; identical `Top(0)`, `Total()`, `MaxError()` |
| `TestMG_UnmarshalRejects` | `EntryCount > k`; unsorted keys; duplicate key; `Count == 0`; `KeyLen == 0`; entries overrunning the body | `ErrMalformed`/`ErrTruncated`, no panic |

### `internal/sketch/minhash_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestMinHash_DisabledYieldsZero` | `Enabled:false` | `Signature{}`; `Perms==0`; `Mins==nil` |
| `TestMinHash_PermutationClamping` | `Permutations` 4, 128, 9 999 | `Perms` 16, 128, 512 |
| `TestMinHash_ShortInputIsEmptySignature` | 3-byte input, `ShingleSize` 8 | `Perms==128`; every `Mins[i] == math.MaxUint64`; `Jaccard` with another such signature == 1 |
| `TestMinHash_IdenticalInputs` | same 4 KiB input twice | `Jaccard == 1.0`; `IsNearDup(o, 0.9) == true` |
| `TestMinHash_DisjointInputs` | 4 KiB of `'a'`-derived pseudo-random vs. `'b'`-derived, no shared 8-grams | `Jaccard ≤ 0.05` |
| `TestMinHash_OneNewFailure` | a 200-line fake `go test` output; the same with one extra `--- FAIL` line appended (the §8.1 "same test suite, one new failure" case) | `Jaccard ≥ 0.9`; `IsNearDup(o, 0.9) == true` |
| `TestMinHash_ShiftInvariance` | D = a deterministic 4 KiB pseudo-random document (seeded `math/rand.New(rand.NewSource(1))`, so the test is reproducible); D′ = D with 40 bytes inserted at offset 100 | `Jaccard ≥ 0.9` — analytically ≈ 0.987, since insertion destroys 7 shingles and adds 47 out of ≈ 4 089 |
| `TestMinHash_SubsamplingEngages` | 1 MiB input through `minHashWithStats` | returned kept-shingle count ≤ 2 × `MinHashSampleTarget` and ≥ `MinHashSampleTarget / 2`; signature has `Perms == 128` |
| `TestMinHash_SubsamplingStillShiftInvariant` | 1 MiB doc vs. the same doc with 4 KiB inserted mid-way | `Jaccard ≥ 0.85` |
| `TestMinHash_Symmetric` | random pairs | `a.Jaccard(b) == b.Jaccard(a)` |
| `TestMinHash_IncomparableWidths` | `Perms` 128 vs. 256 | `Jaccard == 0`; `IsNearDup == false` |
| `TestMinHash_ZeroSignatureComparesZero` | `Signature{}` vs. a real one | `Jaccard == 0`; `IsNearDup(o, 0.0) == false` (guarded by `Perms != 0`) |
| `TestSignature_CompactRoundTrip` | 128-permutation signature | `len(b) == 2 + 8*128 == 1026`; unmarshal→marshal byte-identical |
| `TestSignature_UnmarshalRejects` | empty; 1 byte; `Perms=128` with 100 bytes of mins; `Perms = MaxPermutations+1` | `ErrTruncated` / `ErrMalformed`; never a panic; no allocation on the oversize case |
| `TestSigSketch_RoundTrip` | wrap a 128-permutation signature | frame decodes with `Kind == KindMinHash`; `Params["perms"] == 128`; re-marshal byte-identical |
| `TestMinHash_StableAcrossRuns` | fixed 1 KiB input, `Permutations: 128, ShingleSize: 8` | `Mins[0..3]` equal the four uint64 constants committed in the test body — the frozen-format assertion |

### `internal/sketch/io_test.go` (`package sketch_test`; every case starts from `testutil.NewProject(t)` — see the `io.go` directory contract)

> **V2 reconciliation:** the two backup-path literals in the table below read `tried.bloom.0003.bak` / `tried.bloom.0007.bak` and are now `tried.bloom.3.bak` / `tried.bloom.7.bak`, following the `%d` naming `paths.ReplaceBloom` and `paths.pruneBloomBackups` actually use (see the `ReplaceGenerational` note above). Nothing else in these rows changes. The shipped file also carries `TestReplaceGenerational_NonMonotonicSeqIsRefused` and `TestReplaceGenerational_RefusalProtectsAgainstAStagingFailure`, which this table predates.

| Test | Setup / input | Expected |
|---|---|---|
| `TestSave_Load_RoundTrip` | project root from `testutil.NewProject(t)`, target `.qompack/sketches/touch.cms` | file exists; `Load` into a fresh `CMS` reproduces every estimate |
| `TestLoadWithLog_IsTheLoudPath` | corrupt a saved sketch, call `Load` with a recording logger installed nowhere, then `LoadWithLog` with a recording logger | `Load` produces **zero** log records (it has no logger, by construction); `LoadWithLog` produces exactly one `Loud`. This is the test that pins the split contract described under **Produces** so a later reader does not "helpfully" make `Load` silent-but-logging via a package global |
| `TestSave_RefusesTriedBloom` | `Save(filepath.Join(dir,"tried.bloom"), b)` | `ErrGenerational`; no file created |
| `TestSave_AllowsOtherBloomNames` | `Save(dir/"segment-12.bloom", b)` | succeeds |
| `TestLoad_MissingFile` | nonexistent path | `errors.Is(err, core.ErrNotFound)` |
| `TestLoad_CorruptIsNotFoundAndCorrupt` | write a valid bloom, flip one byte | `errors.Is(err, core.ErrNotFound)` **and** `errors.Is(err, ErrCorrupt)` |
| `TestLoadWithLog_LoudOnCorrupt` | as above with a recording `logging.Logger` | exactly one `Loud` call, whose kv pairs include `path` and `err` |
| `TestLoad_OversizeFileRejected` | a `MaxFrameBytes+1` byte file created with `os.Truncate` (sparse, so the test costs no disk) | `errors.Is(err, ErrTooLarge)` and `errors.Is(err, core.ErrNotFound)`; rejection comes from the `os.Stat` size check, so `os.ReadFile` is never reached |
| `TestReplaceGenerational_FirstWrite` | no existing `tried.bloom` | returns `("", nil)`; file exists; no `.bak` present |
| `TestReplaceGenerational_KeepsOneGeneration` | write seq 1, seq 2, seq 3 | after seq 3 the directory holds exactly `tried.bloom` and `tried.bloom.3.bak`; the `.bak` decodes to the seq-2 content |
| `TestReplaceGenerational_RollsBackOnWriteFailure` | after a successful seq-1 write, `os.RemoveAll(root + "/.qompack/tmp")` and then `os.WriteFile(root + "/.qompack/tmp", nil, 0o600)` — i.e. replace the staging **directory with a regular file**, so `paths.WriteAtomic`'s `os.CreateTemp` fails identically on Windows and POSIX. This is chosen over `chmod 0500`, which is a no-op for an administrator on Windows and therefore silently turns the test into a tautology | error returned; original `tried.bloom` still present and decodable to the seq-1 content; no `*.bak` left in the directory |
| `TestReplaceGenerational_BackupDecodes` | seq 7 replacement | backup path is `tried.bloom.7.bak`; `Load` on it succeeds |
| `TestQuarantine` | corrupt file | renamed to `<p>.corrupt.<digits>`; original path gone; returned path exists |
| `TestAppendOnly_TriedBloomNeverTruncated` | `p.AssertAppendOnly(t)` from `testutil` after a `ReplaceGenerational` cycle | passes (the file is replaced by rename, never opened `O_TRUNC`) |

### `internal/sketch/property_test.go` (pgregory.net/rapid)

| Property | Draw | Assertion |
|---|---|---|
| `TestProp_BloomNoFalseNegatives` | 1–500 distinct byte slices, len 1–64 | every added key `Test`s true |
| `TestProp_BloomRebuildEquivalence` | a key set | `RebuildBloom(cap, fp, seq)` answers `Test` identically to an incrementally built filter of the same size |
| `TestProp_CMSOverestimateOnly` | ≤ 300 (key, weight ∈ [1,1000]) pairs | `Estimate(k) ≥ Σ weights(k)` for every key |
| `TestProp_CMSMergeAdditive` | two multisets | merged estimates equal single-pass estimates for every key (exact, absent saturation) |
| `TestProp_HLLMergeIsRegisterMax` | two key sets | merged register array == register array of the union, byte for byte |
| `TestProp_MGNoFalsePositives` | k ∈ [1,32]; stream ≤ 500 items | every `Top` key appears in the stream; reported ≤ true; true − reported ≤ `MaxError()` ≤ total/(k+1) |
| `TestProp_MGFrequentItemsRetained` | as above | every key with true count > total/(k+1) appears in `Top(k)` |
| `TestProp_MarshalIdempotent` | any of the five sketches under a random operation stream | `marshal ∘ unmarshal ∘ marshal == marshal`, byte for byte, for all five |
| `TestProp_UnmarshalNeverPanics` | arbitrary `[]byte` ≤ 4 KiB | every `UnmarshalBinary` returns an error or succeeds; never panics; the survivor re-marshals cleanly |
| `TestProp_MinHashJaccardAccuracy` | two documents of ≤ 4 KiB (below the subsample threshold), each ≥ 256 bytes so the shingle sets are non-degenerate | \|estimated − true shingle Jaccard\| ≤ **0.20**. The worst-case standard error at P=128 is `√(0.25/128) = 0.0442`, so 0.20 is 4.5σ. The obvious-looking 3σ bound (0.15) is **wrong for a property test**: rapid runs 100 draws per invocation, so a per-draw tail of 0.3 % compounds to a ~26 % chance that the *suite* fails on a correct implementation. Pick the bound from the number of draws, not from one draw |
| `TestProp_MinHashSelfSimilarity` | any document | `Jaccard(s,s) == 1`; `IsNearDup(s,s,1.0) == true` |
| `TestProp_HeaderRoundTrip` | ≤ 64 params with legal `[a-z0-9.]` names of length 1–32, arbitrary float64 values (NaN excluded — `math.Float64bits` round-trips it but `require.Equal` on NaN is false, so draw finite values), arbitrary body ≤ 4 KiB | `EncodeHeader` returns no error; `DecodeHeader` returns exactly what was encoded, with `CRC32C` equal to the trailing four bytes |

### `internal/sketch/fuzz_test.go` + seed corpora

Five targets, each with a committed seed corpus under `testdata/corpora/sketch/<target>/`:

- `FuzzBloomUnmarshalBinary` — seeds: a valid 12 KB bloom frame; the same with a flipped CRC byte; a header-only frame; a frame whose `m` param is `1e300`; a frame whose `BodyLen` is `0xFFFFFFFF`; 36 zero bytes.
- `FuzzCMSUnmarshalBinary` — seeds: a valid 54 KB CMS frame; a frame with `depth = 0`; one with `width = 1e9`; one truncated mid-body.
- `FuzzHLLUnmarshalBinary` — seeds: a valid 2 KB HLL frame; `registers = 1000`; body one byte short; `registers` param `NaN`.
- `FuzzMisraGriesUnmarshalBinary` — seeds: a valid 64-counter frame; `EntryCount = 0xFFFFFFFF`; unsorted keys; a `KeyLen` overrunning the body; a zero-length key.
- `FuzzSignatureUnmarshalBinary` — seeds: a valid 1 026-byte signature; `{0x00,0x00}`; `Perms = 0xFFFF` with no body; a 1-byte input.

Every target asserts: no panic; on success, `MarshalBinary` then `UnmarshalBinary` reproduces an equal value; on failure, the returned error is one of the package sentinels (`errors.Is` against the set) and the receiver is left in a usable state (a subsequent `Add`/`Test` does not panic). Nightly CI runs 10 min per target (00-ARCHITECTURE §8, `nightly.yml`).

### `internal/sketch/golden_test.go` + `testdata/golden/contracts/sketch/`

`golden_test.go` is also `package sketch_test`. The fixtures live at the **repository** `testdata/` root, not in a package-local `testdata/`, because Rule W-2 makes them a cross-package contract; from `internal/sketch` the path is `filepath.Join("..", "..", "testdata", "golden", "contracts", "sketch")`, resolved once into a package-level `goldenDir` constant.

`TestGolden_OnDiskStability` builds five canonical sketches with a fixed `Created = 1_700_000_000_000` and a fixed key sequence, marshals each, and asserts byte equality with:

```
testdata/golden/contracts/sketch/bloom-10000-0.01.v1.bin
testdata/golden/contracts/sketch/cms-0.001-0.01.v1.bin
testdata/golden/contracts/sketch/hll-2048.v1.bin
testdata/golden/contracts/sketch/mg-64.v1.bin
testdata/golden/contracts/sketch/minhash-128.v1.bin
testdata/golden/contracts/sketch/MANIFEST.json      # {file, kind, bytes, sha256} per fixture
```

Regeneration is `go test ./internal/sketch -run TestGolden -update`. The flag is declared in `golden_test.go` as

```go
var update = flag.Bool("update", false, "regenerate testdata/golden/contracts/sketch fixtures")
```

and guarded at the top of the regeneration branch by

```go
if *update && os.Getenv("CI") != "" {
    t.Fatal("refusing to regenerate golden fixtures in CI: a fixture the implementation " +
        "cannot reproduce is a verification failure, not a fixture bug (Rule W-2)")
}
```

so a green CI run can never be manufactured by rewriting the contract it is checking. **These fixtures are frozen after commit 6.** Rule W-2 makes them the contract SP-04 and SP-06 test against in the same wave; if SP-01 committed placeholder files at these paths, commit 6 replaces them and says so in the commit body. A change to any of these five files in a later PR is a format break and requires a `FormatVersion` bump plus a `case` arm, which `TestGolden_V1StillDecodes` enforces by decoding the committed v1 bytes with the current decoder.

### `internal/sketch/imports_test.go`

`TestImports_FoundationOnly` parses the package's non-test files with `go/parser` and asserts the set of `github.com/qompack/qompack/internal/...` imports is exactly `{core, paths, logging}`. This is the local mirror of the 00-ARCHITECTURE §3.2 import-graph check and fails fast, in-package, if someone reaches for `config` or `store`. It inspects **non-test** files only, which is what makes the `package sketch_test` files' use of `testutil` legal.

### `internal/sketch/bench_test.go`

One benchmark per budget row in the Implementation spec's performance table, named for its budget so a failure names the number it broke: `BenchmarkBloomAdd`, `BenchmarkBloomTest`, `BenchmarkCMSAdd`, `BenchmarkCMSEstimate`, `BenchmarkHLLAdd`, `BenchmarkHLLCardinality`, `BenchmarkMisraGriesAdd`, `BenchmarkL0SketchUpdate`, `BenchmarkMinHash4KiB`, `BenchmarkMinHash100KiB`, `BenchmarkBloomMarshal`, `BenchmarkBloomUnmarshal`, `BenchmarkCMSMarshal`, `BenchmarkCMSUnmarshal`, `BenchmarkRebuildBloom5000`.

`BenchmarkL0SketchUpdate` is the one the L0 model consumes: one `CMS.Add(path,1)`, one `HLL.Add(path)`, one `Bloom.Test(descriptorKey)` — the entire §8.1 item 5 contribution to a `PostToolUse`. It runs `b.ReportAllocs()` and the accompanying `TestL0SketchUpdate_ZeroAlloc` asserts `testing.AllocsPerRun(1000, …) == 0`, because an allocation on the hot path is a budget regression that a nanosecond count can hide. Results are appended to `testdata/bench-baseline.txt` so `benchstat` gates future changes (00-ARCHITECTURE §7: >10 % warns, >25 % fails).

### Fixtures required

- `testdata/golden/contracts/sketch/` — five frozen binaries plus `MANIFEST.json` (created here).
- `testdata/corpora/sketch/{bloom,cms,hll,mg,signature}/` — fuzz seed corpora (created here).
- `testdata/bench-baseline.txt` — appended, not replaced (SP-01 created it).
- No session fixtures. This package touches the filesystem only in `io_test.go` and `golden_test.go`, both `package sketch_test`, both starting from `testutil.NewProject(t)` so that `paths.WriteAtomic` has a real `.qompack/tmp/` to stage through and `p.AssertAppendOnly(t)` is available for the `tried.bloom` case.

---

## Commit plan

Work happens **only** on `feat/sp03-sketch-library`, cut from `develop` with SP-01 already merged:

```
git checkout develop && git pull
git checkout -b feat/sp03-sketch-library
```

Eight commits. Each compiles, each passes `go run ./tools/devtool test` for the packages it touches, and each is committed only after the stated commands are green. Within every commit the listed tests are written **first**, run, and observed **failing** before the implementation is written.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(sketch): versioned QPKS header, CRC32C framing, and domain-separated hashing`

- [ ] Write `internal/sketch/header_test.go` with all 17 rows / 18 test functions from the test plan (`_RejectVersionZero` and `_RejectVersionFuture` are two functions on one row). Run `go test ./internal/sketch -run TestHeader` and `-run TestHash128` — must fail (`ErrNotImplemented` / undefined).
- [ ] Create `internal/sketch/doc.go`, `internal/sketch/errors.go`, `internal/sketch/util.go`, `internal/sketch/hash.go`, `internal/sketch/header.go`.
- [ ] Remove SP-01's stub declarations of `Kind`, `Header`, `Sketch`, `Save`, `Load` and the five constructors. SP-01 ships them in a single file, `internal/sketch/sketch.go` (the 00-ARCHITECTURE §5.22 stub pattern: one file per package, every body `return core.ErrNotImplemented`); if the checkout has them spread differently, `grep -rl "ErrNotImplemented" internal/sketch` locates them. The type and function *declarations* move into the files created above **byte-identical to 00-ARCHITECTURE §5.7** — copy them, do not retype them — and the now-empty `sketch.go` is deleted in this commit rather than left as a husk.
- [ ] Add `internal/sketch/imports_test.go` and make it pass.
- [ ] Run: `go run ./tools/devtool fmt lint vet` then `go test ./internal/sketch/...` — all green.
- Body: why an explicit version + CRC rather than gob; the `Ver ≤ FormatVersion` rule; params sorted for byte-stability. Footer: `Refs: SP-03, §6.2, 00-ARCHITECTURE §5.7`.

### Commit 2 — `feat(sketch): Bloom filter with Appendix A sizing, fill ratio, and resize target`

- [ ] Write `internal/sketch/bloom_test.go` (17 tests). Run — must fail.
- [ ] Create `internal/sketch/bloom.go` implementing `NewBloom`, `Add`, `Test`, `Count`, `FillRatio`, `EstimatedFPRate`, `Saturated`, `Capacity`, `Bits`, `Stats`, `ResizeTarget`, `RebuildBloom`, `SetCreated`, `Header`, `MarshalBinary`, `UnmarshalBinary`.
- [ ] Confirm `TestBloom_AppendixASizing` reports `m=95_872`, `k=7`, 11 984-byte body.
- [ ] Run: `go test ./internal/sketch/... -count=1`; `go run ./tools/devtool lint` (verifies that no literal from the 00-ARCHITECTURE §11.6 set — here that means `10000`, `2048`, `4096`, `1024`, `0.9`, `0.1` — appears in `bloom.go`, and that `FPWarnRate`'s single `//nomagic:allow` is accepted. `0.01` is *not* in the forbidden set and needs no annotation; the forbidden value that `FPWarnRate = 0.10` collides with is `0.1`).
- Body: the §12 saturation mitigation, and why `EstimatedFPRate` uses observed fill rather than the a-priori formula. Footer: `Refs: SP-03, §6.2, §11.4, §12, Appendix A`.

### Commit 3 — `feat(sketch): Count-Min and HyperLogLog with merge and scale for warm start`

- [ ] Write `internal/sketch/cms_test.go` (14 tests) and `internal/sketch/hll_test.go` (10 tests). Run — must fail.
- [ ] Create `internal/sketch/cms.go` and `internal/sketch/hll.go`.
- [ ] Confirm `TestCMS_AppendixASizing` reports `2719 × 5`, 54 380 bytes, and that `TestHLL_ErrorBounds` passes at all four cardinalities.
- [ ] Run: `go test ./internal/sketch/... -count=1 -race`.
- Body: `⌈e/ε⌉` is 2719 and Appendix A's 2718 is the same number at display precision; saturating counters preserve the overestimate-only guarantee; `MergeFrom`/`Scale` exist for O4. Footer: `Refs: SP-03, §6.2, Appendix A, Phase 7 O4`.

### Commit 4 — `feat(sketch): Misra-Gries summary with mergeable no-false-positive guarantee`

- [ ] Write `internal/sketch/misragries_test.go` (14 tests). Run — must fail.
- [ ] Create `internal/sketch/misragries.go`.
- [ ] Add `CMS.HeavyHitters` (it needs `*MisraGries`) and un-skip `TestCMS_HeavyHittersPairsWithMG`.
- [ ] Run: `go test ./internal/sketch/... -count=1 -race`.
- Body: the two halves of "no false positives" — keys are stored verbatim, counts are lower bounds within `MaxError()`; why the merge subtracts the (k+1)-th largest. Footer: `Refs: SP-03, §6.2`.

### Commit 5 — `feat(sketch): MinHash signatures, Jaccard, and near-duplicate detection`

- [ ] Write `internal/sketch/minhash_test.go` (16 tests). Run — must fail.
- [ ] Create `internal/sketch/minhash.go` including `SigSketch`.
- [ ] Record the four frozen `Mins` constants into `TestMinHash_StableAcrossRuns` from the first green run, then re-run to confirm they hold.
- [ ] Run: `go test ./internal/sketch/... -count=1`; `go test ./internal/sketch -bench=BenchmarkMinHash -benchtime=20x` and confirm 4 KiB ≤ 1.5 ms/op and 100 KiB ≤ 2.5 ms/op.
- Body: content-defined subsampling preserves shift-invariance where positional striding would not; FNV-1a is frozen because signatures are persisted. Footer: `Refs: SP-03, §8.1 item 1, Appendix C store.canonicalize.minhash`.

### Commit 6 — `feat(sketch): atomic Save/Load, generational tried.bloom replacement, and corruption reporting`

- [ ] Write `internal/sketch/io_test.go` (14 tests, `package sketch_test`) and `internal/sketch/golden_test.go` (`TestGolden_OnDiskStability` and `TestGolden_V1StillDecodes`, also `package sketch_test`). Run — must fail.
- [ ] Create `internal/sketch/io.go`.
- [ ] Generate the five golden fixtures: `go test ./internal/sketch -run TestGolden -update`, then commit `testdata/golden/contracts/sketch/*` including `MANIFEST.json`. If SP-01 left placeholder files at those paths, replace them and note the replacement in the commit body.
- [ ] Re-run `go test ./internal/sketch -run TestGolden` **without** `-update` — must pass against the committed bytes.
- [ ] Run: `go test ./internal/sketch/... -count=1 -race`; `go run ./tools/devtool fsck` on a temp project to confirm the append-only guard accepts `ReplaceGenerational`.
- Body: `Save` refuses `tried.bloom` so §7.4's invariant is mechanical rather than aspirational; the rollback path; the frozen-fixtures rule under W-2. Footer: `Refs: SP-03, §7.4, §12, 00-ARCHITECTURE §3.3`.

### Commit 7 — `test(sketch): property tests, fuzz targets with seed corpora, and conformance suite`

- [ ] Write `internal/sketch/property_test.go` (12 properties) and `internal/sketch/fuzz_test.go` (5 targets). Run — expect real failures only if an implementation bug exists; fix implementations, not properties.
- [ ] Write the five fuzz seed corpora under `testdata/corpora/sketch/**`.
- [ ] Rewrite `internal/sketch/sketchtest/suite.go`: fill every behaviour body, delete every `t.Skip`.
- [ ] Verify `grep -R "t.Skip" internal/sketch` returns nothing and `grep -R "ErrNotImplemented" internal/sketch` returns nothing.
- [ ] Run: `go test ./internal/sketch/... -race -count=2`; `go test ./internal/sketch -fuzz=FuzzBloomUnmarshalBinary -fuzztime=60s` and the same for the other four; `go run ./tools/devtool cover` and confirm `internal/sketch` ≥ 90 % (00-ARCHITECTURE §6.4).
- Body: which invariant each property encodes and why the CMS bound is asserted as a 99 % fraction rather than per-key (the guarantee is probabilistic at δ=0.01). Footer: `Refs: SP-03, §6.2, 00-ARCHITECTURE §5.22 W-1`.

### Commit 8 — `perf(sketch): micro-benchmarks feeding the L0 hot-path budget model`

- [ ] Write `internal/sketch/bench_test.go` (15 benchmarks) and `TestL0SketchUpdate_ZeroAlloc`.
- [ ] Run `go test ./internal/sketch -bench=. -benchmem -count=6 > sketch-bench.txt` at the repository root. `>` redirection behaves identically in PowerShell and bash, and the dev machine is Windows (00-ARCHITECTURE §2.6), so **no `/tmp` path appears anywhere in this subplan**. Confirm every row of the performance table, append the results to `testdata/bench-baseline.txt`, then delete `sketch-bench.txt` — it is scratch and is never committed (the Done-checklist diff check enforces that).
- [ ] Write `docs/adr/0030-sketch-binary-format.md`.
- [ ] Run the full local gate: `go run ./tools/devtool ci-local`.
- [ ] Push and confirm CI green: `verify`, `test` (ubuntu/macos/windows), `cover`, `crossbuild`, `security`.
- Body: the composite L0 number (≤ 5 µs, 0 allocs — 0.03 % of B-A) and the explicit placement of `MinHash` under B-C rather than B-A. Footer: `Refs: SP-03, §8.1, §11.3, 00-ARCHITECTURE §2.4 B-A/B-C, 00-ARCHITECTURE §7`.

---

## Subagent strategy

This subplan is **heavy**: five independent algorithms, one shared container format, and four test disciplines. Partition it as follows. The commit plan stays strictly sequential and is executed by the main session — subagents produce files, never commits.

**Phase A — main session only (no subagents).** Commit 1. The header, the error set, the size ceilings and `hash128` are the shared contract every subsequent file depends on; parallelizing them would produce five incompatible framings. Finish and commit `header.go`, `errors.go`, `hash.go`, `doc.go`, and their tests before dispatching anything.

**Phase B — four parallel subagents, dispatched together after commit 1 lands.** Each receives: this subplan file, the committed `header.go`/`errors.go`/`hash.go` contents, and the sentence "you may not edit any file outside your assigned list; if you need a change to `header.go`, return the request in your report instead of making it."

| Subagent | Writes | Returns |
|---|---|---|
| **S1 — Bloom** | `bloom.go`, `bloom_test.go` | the four Appendix A numbers it measured (`mRaw`, `mBits`, `k`, body bytes), the observed fill ratio and empirical FP rate at capacity, and the insertion count at which `ResizeTarget` first fires |
| **S2 — CMS + HLL** | `cms.go`, `hll.go`, `cms_test.go`, `hll_test.go` (with `TestCMS_HeavyHittersPairsWithMG` marked `t.Skip("S3 lands MisraGries")` for the main session to un-skip) | measured `width`/`depth`/body bytes; the HLL relative error at each of the four cardinalities; confirmation that `MergeFrom` is register-exact |
| **S3 — Misra-Gries** | `misragries.go`, `misragries_test.go` | the `MaxError()` values observed in the weighted and merge tests, and confirmation that 32 replays marshal identically |
| **S4 — MinHash** | `minhash.go`, `minhash_test.go` | measured `Jaccard` for the identical / one-new-failure / shift / disjoint cases, the four frozen `Mins` constants, and the ns/op for 4 KiB and 100 KiB |

Integration: the main session reviews each returned file against the Implementation spec's signatures, deletes any local copy of `clamp`, `clampInt` or `satAdd64` a subagent added (all three already live in `util.go` from commit 1), then lands commits 2–5 in order, running the full package test suite after each. A subagent's numbers are recorded in the commit body only after the main session has reproduced them locally.

**Phase C — two parallel subagents after commit 5.**

| Subagent | Writes | Returns |
|---|---|---|
| **S5 — persistence** | `io.go`, `io_test.go`, `golden_test.go` | the five golden byte counts and their sha256s, and confirmation that the rollback test genuinely fails the write on both POSIX and Windows |
| **S6 — property + fuzz** | `property_test.go`, `fuzz_test.go`, the five seed corpora, `sketchtest/suite.go` | the list of every implementation bug the properties found, with the minimal reproducing draw for each |

S6 will find bugs in files S1–S4 wrote. **Fixes to `bloom.go`/`cms.go`/`hll.go`/`misragries.go`/`minhash.go` are made by the main session, not by S6** — S6 reports, the main session repairs, so no two agents ever hold the same file. Land commit 6 (S5's output plus the generated fixtures), then commit 7 (S6's output plus the repairs).

**Phase D — main session only.** Commit 8. Benchmarks must be measured on one machine with a quiet load and appended to a shared baseline file; delegating that produces numbers that cannot be compared. The ADR is written last because it records decisions the earlier phases confirmed.

**Never delegated, under any circumstance:** any `git` operation; edits to `header.go` after commit 1; the golden-fixture `-update` run; `testdata/bench-baseline.txt`; deletion of `t.Skip` markers in `sketchtest` (the main session verifies each removal against a real assertion, because a deleted skip with an empty body is the exact failure mode Rule W-1 exists to prevent).

---

## Exit criteria

### Quoted phase criteria from `Qompack.md` that bind this slice

This package is a dependency of Phase 1 and Phase 2 rather than the owner of either exit criterion; the two it must not obstruct, quoted verbatim:

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

And the guardrail that this package's benchmarks feed:

> - Hook p99 latency < 15ms (L0), < 2s (L4)

Concretely: `BenchmarkL0SketchUpdate` ≤ 5 µs with zero allocations is this slice's contribution to "hook p99 < 15ms"; `MinHash` correctness on the "same test suite, one new failure" fixture is this slice's contribution to the 4:1 dedup ratio; the frozen `tried.bloom` format plus `RebuildBloom` from an arbitrary iterator is this slice's contribution to "zero stale-block incidents".

### Local Definition of Done

- [ ] All five sketches implement `Sketch` and every §5.7 signature exists with the exact spelled-out types.
- [ ] `TestBloom_AppendixASizing` passes with `mRaw = 95_851`, `mBits = 95_872`, `k = 7`, body 11 984 bytes.
- [ ] `TestCMS_AppendixASizing` passes with `width = 2719`, `depth = 5`, body 54 380 bytes.
- [ ] `TestHLL_AppendixSizing` passes with 2 048 registers, a 2 102-byte frame, and a 2.3 % standard error.
- [ ] `TestBloom_EstimatedFPRateMatchesEmpirical` shows an empirical false-positive rate within `[0.008, 0.013]` at capacity — the design's 1 % claim, measured.
- [ ] `ResizeTarget` returns `(2×capacity, fpRate, true)` above 0.5 fill and `(capacity, fpRate, false)` below; `Saturated()` fires at `EstimatedFPRate ≥ 0.10` (§11.4).
- [ ] `RebuildBloom` reconstructs an equivalent filter at a **different** capacity from an `iter.Seq[[]byte]`, in ≤ 15 ms for 5 000 keys.
- [ ] `MergeFrom` and `Scale` exist and are correct on `CMS`, `HLL` and `MisraGries`, with `ErrShapeMismatch` on every mismatch and on `nil`.
- [ ] `Save` returns `ErrGenerational` for any path whose base is `tried.bloom`; `ReplaceGenerational` keeps exactly one `.bak` generation and rolls back on write failure.
- [ ] `LoadWithLog` on a corrupt file emits exactly one `Loud` message and returns an error satisfying both `errors.Is(err, core.ErrNotFound)` and `errors.Is(err, ErrCorrupt)`; `Load`'s doc comment names `LoadWithLog` as the required production path.
- [ ] Every on-wire param name matches `[a-z0-9.]{1,32}` — in particular the Bloom filter writes `fprate`, not `fpRate` — and the names are strictly ascending in every frame.
- [ ] No constructible sketch can fail to marshal: `MaxBloomBits`/`MaxCMSCells` keep the largest legal body at 32 MiB, and `EncodeHeader` returns `ErrTooLarge` rather than emitting a frame above `MaxFrameBytes`.
- [ ] All five golden fixtures and `MANIFEST.json` are committed and reproduced byte-for-byte without `-update`.
- [ ] Five fuzz targets exist with committed seed corpora; each survives 60 s locally with no panic and no non-sentinel error.
- [ ] Zero `t.Skip`, zero `ErrNotImplemented`, zero `TODO`/`FIXME` anywhere under `internal/sketch` (`grep` verified).
- [ ] `TestImports_FoundationOnly` passes: the package imports only `core`, `paths`, `logging`.
- [ ] Line coverage for `internal/sketch` ≥ **90 %** (00-ARCHITECTURE §6.4 floor).
- [ ] Every benchmark meets its budget row; `TestL0SketchUpdate_ZeroAlloc` reports 0 allocations; results appended to `testdata/bench-baseline.txt`.
- [ ] `go run ./tools/devtool ci-local` green: `gofumpt -l` empty, `golangci-lint run` clean, `go vet` clean, `nomagic` clean (no forbidden literal in non-test code; `internal/sketch/...` carries exactly **two** `//nomagic:allow` annotations — `FPWarnRate` in `bloom.go`, added by this subplan, and SP-01's pre-existing `mhJaccardTolerance` in `sketchtest/minhash.go`, which stays untouched — so `FPWarnRate` is the only one inside `package sketch` proper), import-graph check clean, `go build ./...` clean.
- [ ] CI green on `feat/sp03-sketch-library` for `verify`, `test` (ubuntu + macos + windows), `cover`, `crossbuild`, `security`.
- [ ] Commit count is exactly 8; every message is Conventional Commits with a `Refs:` footer; no `Co-Authored-By`, `Signed-off-by`, `Generated with`, or 🤖 anywhere in the range (the `verify` job greps for these).

---

## Done checklist

- [ ] Every constant, formula and table in **Design context** has a corresponding implementation and at least one test that asserts its exact value: §6.2 companion-sketch table (four sizes) → `TestBloom_AppendixASizing`, `TestCMS_AppendixASizing`, `TestHLL_AppendixSizing`, `TestMG_*`; Appendix A bloom formula → `NewBloom`; Appendix A CMS formula → `NewCMS`; §8.1 item 5 → `BenchmarkL0SketchUpdate`; §11.4 → `EstimatedFPRate` + `Saturated`; §12 bloom saturation → `FillRatio`/`ResizeTarget`/`RebuildBloom`; §7.4 invariant → `Save` refusal + `ReplaceGenerational`; Appendix C `sketches`/`minhash` keys → constructor parameters (never literals).
- [ ] Placeholder scan: `grep -rniE "TODO|FIXME|TBD|not implemented|handle .* appropriately|add tests" internal/sketch docs/adr/0030-sketch-binary-format.md` returns nothing.
- [ ] Type consistency: every signature in **Interface contract → Produces** appears verbatim in the code, including `iter.Seq[[]byte]`, `Counted`, `MinHashOptions`, `Signature`, `Header`, `Kind`, and both `Save`/`Load`; nothing in §5.7 was changed, removed, or renamed, and every addition is a method or type this subplan owns.
- [ ] Consumers unblocked: `Signature` + `Jaccard` + `IsNearDup` compile against SP-04's expected use; `NewBloom`/`Test`/`RebuildBloom`/`ResizeTarget`/`Stats`/`ReplaceGenerational` cover every call SP-09's `Ledger` and `Health` need; `MergeFrom`/`Scale` cover SP-16's O4 warm start.
- [ ] `internal/sketch/sketchtest` behaviour tests are real assertions, not empty bodies; each `t.Skip` deletion was reviewed individually.
- [ ] Golden fixtures are frozen and `TestGolden_V1StillDecodes` proves the v1 bytes decode with the shipped decoder.
- [ ] Commit count verified in range: `git rev-list --count develop..feat/sp03-sketch-library` prints `8` (within the required 5–8). `rev-list --count` is used rather than `git log | wc -l` because `wc` does not exist in PowerShell and the dev machine is Windows.
- [ ] No co-author or attribution trailers: `git log develop..feat/sp03-sketch-library --format=%B` contains no `Co-Authored-By`, `Signed-off-by`, `Generated with` or 🤖 (run it through `grep -niE` under Git Bash, or `Select-String -Pattern "co-authored-by|signed-off-by|generated with"` under PowerShell). This is the same grep 00-ARCHITECTURE §10 says the `verify` job runs.
- [ ] `Qompack.md` is unmodified: `git diff develop..feat/sp03-sketch-library -- Qompack.md` is empty.
- [ ] Out-of-scope discipline: `git diff --name-only develop..feat/sp03-sketch-library` touches only `internal/sketch/**`, `testdata/golden/contracts/sketch/**`, `testdata/corpora/sketch/**`, `testdata/bench-baseline.txt`, and `docs/adr/0030-sketch-binary-format.md`.

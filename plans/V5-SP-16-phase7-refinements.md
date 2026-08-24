# SP-16: Phase 7 refinements: cross-session warm start, demand-driven promotion, per-segment blooms, ski-rental write policy, and progressive truncation tuning

> **Recommended model: Opus 5 · max effort**
>
> Five small deliverables that each carry real math — ski-rental threshold computed as `w/r` (literal forbidden by lint), BOCD per-feature prior seeding, exponentially-decayed CMS merge, and tier reserves set to the argmax of a *measured* truncation curve — layered onto seven live subsystems at once.

**Branch:** `feat/sp16-phase7-refinements` (cut from `develop`) | **Wave:** 4 | **Prerequisites:** the branches of SP-01, SP-03, SP-06, SP-09, SP-10, SP-12, SP-13 already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 4 (SP-14 slash commands, SP-15 analyzer + grammar) | **Design sections:** §5.6 (ski rental), §6.8 (per-level blooms), §6.9 (progressive truncation), §8.7 (demand-driven promotion), §10 Phase 7, Appendix A (ski-rental threshold) | **Gaps closed:** none newly — Phase 7 is refinement on gaps already closed. It measurably improves G3.3 (eager restoration → demand-driven pointer promotion), G6.1/G6.2 (project-scope elimination carry-forward), G4.3/G7.3 (progressive truncation ordering), and G3.3/G7.6 (segment-level answerability without expansion).

---

## Mission

This subplan owns **Phase 7 of `Qompack.md` §10 in full**, minus the one item §12 rules out of plugin reach. Phase 7 is explicitly labelled *refinement on a system that already works*: by the time this branch is cut, the store, the observer, negative knowledge, the checkpointer, the rehydrator, the scheduler, and the MCP retrieval layer are all real and merged into `develop`. Nothing here invents a new subsystem. Every deliverable fills a seam that an earlier wave deliberately reserved: `store.Segment.BloomRef` (documented in 00-ARCHITECTURE §5.8 as `"" until SP-16`), `scheduler.Inputs.ExpectedRemainingReads` (documented as `// ski-rental (Phase 7)`), `sketch.CMS.MergeFrom`/`Scale` (documented as `O4 warm start`), `mcp.Promoter` (documented as `implements retrieval.promoteAfterExpansions (§8.7, Phase 7)`), and `config.sketches.cms.warmStartFromProject` (an Appendix C key nothing consumes yet).

Five things ship. **(1) O4 cross-session warm start.** The store outlives the session, so a fresh session should not start blind. At daemon start the project's cumulative Count-Min sketch of file-touch frequency is exponentially decayed with `Scale` and merged into the live session sketch with `MergeFrom`; `scope: "project"` eliminations are re-verified against the current working tree and carried forward with their staleness correctly re-evaluated; and the changepoint detector's per-feature priors are seeded from the feature summaries of closed segments in past sessions, so BOCD's notion of "normal path locality on this project" does not have to be relearned from the first twenty turns of every new session. **(2) Demand-driven rehydration tuning.** §8.7 says repeated expansion of the same hash is *a signal, not a cost*; SP-13 counts those expansions and records every retrieval result as an ephemeral tool-use in the store. This subplan reads those counts at checkpoint-finalize time and promotes the frequently-re-expanded hashes into the checkpoint's pointer tier at an elevated weight, so the next rehydration includes what the last one should have — a measured correction to the 8–12K budget instead of a guess. **(3) Per-segment Bloom filters** in the LSM style of §6.8, populating `Segment.BloomRef`, so the question "which compacted segment could contain this path/tool/hash" is answerable without expanding any segment. **(4) The ski-rental cache-write policy** of §5.6 and Appendix A, with the threshold *computed* as `w/r` from `scheduler.cache.writeMultiplier / readMultiplier` and never written as the literal `12.5` — the literal is on the `nomagic` lint's forbidden list precisely so this cannot be fudged. **(5) Progressive checkpoint truncation tuning:** the actual budget-versus-reconstruction-quality curve of §6.9's importance ordering is *measured* on the synthetic replay corpus, and the tier reserve fractions are set to the argmax of that measurement, with a test that fails if the constants ever drift from the artifact that justifies them.

**What exists when you start.** `develop` at the wave-3 verification tag: `internal/config` with the full Appendix C schema plus the `runtime` extension namespace; `internal/sketch` with `Bloom`, `CMS` (including `MergeFrom` and `Scale`), `HLL`, `MisraGries`, `MinHash`, all versioned and CRC-checked; `internal/store` with content-addressed objects, the `tool_use` index (including the `Ephemeral` flag on `ToolUseRecord`), file version history, `ChangedSince`, and the `SegmentLog` with `EncodedOnce`/`MarkEncoded`; `internal/negknow` with the elimination ledger, `Scope`, `Status`, `RefreshStaleness`, and `RebuildBloom`; `internal/checkpoint` with the §8.5 schema, `Writer`, `Reader`, `Truncate`, `ExtractDecisions`, and `FocusInstructions`; `internal/scheduler` with `Evaluate`, `NewBOCD`, `YoungDaly` (in `youngdaly.go`), the p-selection gate (in `gate.go`), and a **real, already-tested** `SkiRentalShouldWrite` in `internal/scheduler/skirental.go` (`if r <= 0 || w <= 0 { return false }; return expectedReads > w/r`) — SP-01 shipped the closed form in `formulas.go` under 00-ARCHITECTURE §14.1's "fully specified pure functions are implemented, not stubbed" rule, and **SP-12 (wave 3) deleted `formulas.go`, redistributing its four symbols into `youngdaly.go`, `skirental.go` and `gate.go` and adding the `w <= 0` guard**; it is pinned today by `TestSkiRentalShouldWrite`, `TestSkiRental_ComputedNotLiteral` and `TestSkiRental_ThresholdTracksConfig`, all in `skirental_test.go`; `internal/mcp` with all eight tools and the `Promoter`; `internal/daemon` with the `IdleController` extension seam; `internal/eval` (SP-02, wave 1) with the replay harness, Belady OPT, divergence metrics, and the 24-session synthetic corpus; `internal/rehydrate` (SP-11, wave 3) with the eight-item injection.

**What exists when you finish.** `internal/config` exposes `runtime.phase7`; `internal/scheduler` has a real ski-rental policy that participates in `Evaluate` and a prior-seeded changepoint detector; `internal/store` writes and answers per-segment Bloom filters and exposes ephemeral-expansion counts; `internal/daemon/phase7.go` runs warm start once per session; `internal/checkpoint/promote.go` promotes re-expanded hashes into the pointer tier and `internal/checkpoint/curve.go` carries measured, artifact-justified truncation reserves; `testdata/phase7/` carries two committed measurement artifacts; and the replay gate carries the Phase 7 exit assertion with no metric regressed beyond §11.3's 2% rule.

---

## Design context (verbatim from Qompack.md)

Everything quoted below is reproduced verbatim so this document is self-contained. Do not open `Qompack.md` to implement; do not modify it.

### §10 Phase 7 — Refinement (the whole phase, verbatim)

> ### Phase 7 — Refinement
>
> - **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions. First-compaction quality in a fresh session should benefit from every session before it.
> - Demand-driven rehydration tuning: promote frequently-re-expanded hashes (§8.7) into the checkpoint pointer tier
> - Per-segment Bloom filters, LSM-style (§6.8)
> - Ski-rental cache-write policy
> - Progressive checkpoint truncation tuning
> - Prefix reordering by mutation rate (§5.6) — harness/API-port only, per §12

### §5.6 — ski rental, and the scope note that excludes prefix reordering

> **Ski rental for the write decision.** Whether to pay `w` to write a cache entry is rent-or-buy under unknown horizon. Competitive ratio 2 deterministic, `e/(e−1) ≈ 1.58` randomized. Practically: write the cache when expected remaining reads exceed `w/r ≈ 12.5`. Short sessions should not be paying for cache writes at all.

**The threshold is two numbers, not one, and ≈12.5 is only the first.** §5.1 attaches a standing instruction to its multipliers — *"verify against current pricing before tuning, since the ratio drives several thresholds below"* — and this is the threshold it means. That verification was carried out on 2026-08-23 against `platform.claude.com/docs/en/build-with-claude/prompt-caching`, which states the read multiplier once and the write multiplier **twice**:

> "Cache read tokens are 0.1 times the base input tokens price"
> "5-minute cache write tokens are 1.25 times the base input tokens price"
> "1-hour cache write tokens are **2** times the base input tokens price"

So `r = 0.1` is confirmed, and `w/r` is **12.5 under the five-minute TTL and 20 under the one-hour TTL**. The `≈12.5` in §5.6 and in Appendix A is the five-minute figure. It is not wrong; it is one of two, and Claude Code requests the one-hour TTL automatically on a Claude subscription (`code.claude.com/docs/en/prompt-caching`, *Cache lifetime*), which is the deployment this plugin ships into.

The consequence for SP-16 is narrow and entirely mechanical, because `SkiRentalShouldWrite` was always written with `w` as a *parameter* — SP-01 got that right and V1's `TestSkiRental_ComputedNotLiteral` has been pinning it since wave 1. Nothing about the closed form changes. What changes is **where the caller reads `w` from**: `Inputs.Regime.WriteMultiplier`, resolved by SP-12's `ResolveCacheRegime`, rather than `cfg.Cache.WriteMultiplier`, which is Appendix C's five-minute floor and cannot be edited (Appendix C lives in the read-only `Qompack.md` and `TestDefaults_MatchesAppendixCVerbatim` deep-equals against it). A session on the one-hour TTL that rents against a 12.5-read threshold buys cache entries it needs 20 reads to amortize, and buys them on exactly the short sessions §5.6's last sentence says should not be paying for cache writes at all.

`TestSkiRentalThreshold_TracksRegimeNotConfig` pins it: the same `Inputs` under a `force_5m` regime and an `enable_1h` regime must yield thresholds of `12.5` and `20`, and `cfg.Cache.WriteMultiplier` must read `1.25` in both — the config is the floor, the regime is the bill.

> **Scope note.** The first item below — breakpoint placement — is **not plugin-actionable**: Claude Code manages its own `cache_control` markers and a plugin cannot move them. The analysis is retained because it applies verbatim if Qompack is later ported to a first-party harness on the Messages API (§2.8), and because the Belady extension makes it measurable today. It is listed in the §12 "cannot do" inventory.

> **Segmentation by mutation rate.** The deeper principle, and the same insight as generational garbage collection and column-store ordering:
>
> > **Sort content by expected lifetime, longest-lived first.**
>
> ```
> position 0  ──────────────────────────────────────────►  position n
> [ immutable ][ slow-changing ][ summaries ][ live convo ][ volatile tail ]
>   system       CLAUDE.md         compaction    recent        current
>   prompt,      memory,           checkpoints   turns         tool results
>   tools        unscoped rules
> ```

### §5.1 / §5.2 — the multipliers and the governing quantity

> Prompt caching is **exact-prefix-match**. An edit at position `p` invalidates everything from `p` onward. With read multiplier `r` and write multiplier `w`:
>
> ```
> rebuild cost = w · (n − p)
> forfeited discount = (1 − r) · (n − p)
> ```
>
> Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing before tuning**, since the ratio drives several thresholds below.

> ```
> cost = w · (n − p_min)     where p_min = position of the earliest dropped block
> ```

### §6.8 — LSM compaction theory (per-level blooms)

> **Closes:** architectural framing, G7.1, G7.6, G3.3
>
> The three tiers *are* a log-structured merge tree — including the shared vocabulary. LSM literature gives the tradeoff space in closed form: leveled vs. tiered merge policies, and the read/write/space amplification frontier.
>
> Two immediate transfers:
>
> - **Bloom filter per level.** LSMs put one on each SSTable so a read can skip levels that cannot contain the key. Put one per compacted segment so the agent knows which segment to expand *without expanding any*.
> - **Tombstones with deferred reclamation** rather than eager deletion.
>
> The amplification framing also names the failures precisely: PTL retry and compaction loops are **write amplification**; eager 50K file restoration is **space amplification**.

### §6.9 — Progressive / embedded encoding

> **Closes:** G4.3, G7.3
>
> Embedded coders order the bitstream by importance so truncation *at any point* yields the best available reconstruction for that budget. Current truncation is positional — skills keep their head, PTL retry drops the oldest rounds, which is where intent lives.
>
> If the checkpoint is written in importance order, every budget cut is automatically near-optimal and PTL recovery stops deleting the task statement first. **This is a serialization-order change, not an algorithm** — one of the cheapest wins available.

### §8.5 — the three tiers whose boundaries this subplan tunes

> ```jsonc
>   // ── Tier 1: never truncated ──────────────────────────────
>   "invariants": [ … ],                   // pinned, verbatim
>   "user_intent": {
>     "original": "…",                     // verbatim, from L0, never regenerated
>     "evolution": [ … ]                   // verbatim deltas
>   },
>   "eliminated": [                        // negative knowledge, structured
>     { "target": "src/auth.ts:refreshToken",
>       "approach": "widen pool timeout",
>       "reason": "pgbouncer 1.18 ignores it in transaction mode",
>       "evidence": "sha256:…",
>       "depends_on": [                     // staleness guard (§8.3):
>         { "path": "docker-compose.yml", "hash": "sha256:…" },
>         { "path": "package-lock.json",  "hash": "sha256:…" }
>       ],
>       "scope": "project",                 // "session" | "project"
>       "status": "active" }                // "active" | "stale"
>   ],
>
>   // ── Tier 2: truncate late ────────────────────────────────
>   "decisions": [
>     { "what": "…", "why": "…", "alternatives_rejected": [ … ],
>       "evidence": "sha256:…" }
>   ],
>   "open_questions": [ … ],
>   "current_work": { "goal": "…", "next_step": "…", "blocked_on": null },
>
>   // ── Tier 3: truncate first ───────────────────────────────
>   "pointers": {
>     "files":  [ { "path": "…", "hash": "sha256:…", "why": "…" } ],
>     "tools":  [ { "tool_use_id": "…", "hash": "sha256:…", "summary": "…" } ]
>   },
>   "narrative": "…",                      // prose residue, last resort
> ```

> **Output:** an immutable, versioned, **importance-ordered** JSON artifact. Ordering is the embedded-coding principle from §6.9 — truncation at any point yields the best available reconstruction for that budget.

> Note what is **not** here: no code snippets. Files are pointers with a one-line reason. This is §4.4 applied directly, and it is where most of the 50K eager-restore budget is reclaimed.

### §8.7 — demand-driven promotion (the bullet this subplan implements)

> - Repeated expansion of the same hash within a session is a signal, not a cost: the Analyzer promotes frequently-re-expanded content into the next checkpoint's pointer tier with a higher slice weight, so the system *learns* what eager restoration should have included — a demand-driven correction to the 8–12K rehydration budget.

> - Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).
> - Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch. Most post-compaction questions are "what did that one function look like," not "give me the file."

### §8.3 item 5 — the scope key that drives elimination carry-forward

> 5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).

> 3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.

### §6.6 — the cheap features whose priors are seeded

> Bayesian online changepoint detection maintains a distribution over run length since the last changepoint, updated in O(1) amortized with pruning. Run it over cheap features:
>
> - File-path locality (Jaccard over recently-touched paths)
> - Tool-type distribution shift
> - Lexical cohesion (TextTiling-style)
> - Inter-turn time gaps
> - Todo-list state transitions

### §6.2 — the sketch table

> | Sketch | Purpose | Size |
> |---|---|---|
> | Bloom | `already_tried(x)` membership | ~12KB / 10K entries @ 1% FP |
> | Count-Min | File-touch frequency (which files are hot) | ~54KB @ ε=0.001, δ=0.01 |
> | HyperLogLog | Breadth-of-exploration cardinality | ~2KB, 2.3% error |
> | Misra-Gries | Deterministic top-k with no false positives | O(k) |

### Appendix A — the formulas this subplan computes

> **Ski-rental cache-write threshold**
> ```
> write when  E[remaining reads] > w/r   (12.5 at r=0.1, w=1.25 — the 5-minute TTL)
>                                       (20   at r=0.1, w=2.0  — the 1-hour   TTL)
> ```

> **Bloom filter sizing**
> ```
> m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
> n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7
> ```

> **Count-Min sizing**
> ```
> width = ⌈e/ε⌉      depth = ⌈ln(1/δ)⌉
> ε = 0.001, δ = 0.01  →  2718 × 5 ≈ 54 KB @ 4-byte counters
> ```

> **Cache rewrite cost**
> ```
> cost = w · (n − p_min)
> ```

### Appendix C — the config keys this subplan honours (verbatim excerpt)

> ```jsonc
>   "sketches": {
>     "bloom": { "capacity": 10000, "fpRate": 0.01 },
>     "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
>     "hll":   { "registers": 2048 }
>   },
> ```
> ```jsonc
>   "retrieval": {
>     "ephemeralResults": true,
>     "defaultSpan": "minimal",        // "minimal" | "full"
>     "promoteAfterExpansions": 2
>   },
> ```
> ```jsonc
>   "checkpoint": {
>     "budgetTokens": 12000,
>     "incrementalSpanInstruction": true,
>     "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
>     "tiers": { "never": ["invariants","user_intent","eliminated"],
>                "late":  ["decisions","open_questions","current_work"],
>                "first": ["pointers","narrative"] }
>   },
> ```
> ```jsonc
>   "scheduler": {
>     "softFloorPct": 0.55,
>     "hardCeilingMargin": 20000,
>     "youngDaly": { "enabled": true, "measuredDeltaSeconds": null },
>     "changepoint": { "hazardRate": 0.004, "features": ["paths","tools","time","todos"] },
>     "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
>     "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
>   },
> ```

### §11.3 guardrails and §11.4 watch-fors (both bind on this subplan)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

> - **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### §12 — the two risk rows and the "cannot do" line this subplan is bound by

> | Cache multipliers change | Low | Read `r` and `w` from config, never hardcode |
> | Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |

> - **Cannot place or move `cache_control` breakpoints.** Claude Code manages its own cache markers, so the breakpoint-placement analysis in §5.6 is measurement-and-port material, not a plugin feature.

### §5.4 — the bimodality the ski-rental policy operationalizes

> - **TTL bimodality — with a correction.** If the prefix has expired, `p = 0` is free, so the optimal policy is genuinely bimodal: **edit as late as possible, or edit when the cache is cold and rebuild everything.** The expensive region is the middle. But the TTL is *sliding*, not fixed — it refreshes on every cache hit, so an actively-used prefix never expires on its own.

---

## Out of scope

Each item names the sibling subplan that owns it. Do not implement any of these.

| Excluded | Owner |
|---|---|
| **Prefix reordering by mutation rate (§5.6)** — sorting content by expected lifetime, placing or moving `cache_control` breakpoints. §12 states plainly this is *not plugin-actionable*. SP-16 ships a **test that asserts it is not attempted** and an ADR recording the non-delivery; it ships no implementation. | Nobody in-plugin. §5.6 breakpoint *measurement* (Belady extension) is **SP-02**'s, inside `internal/eval`, and is explicitly labelled not-plugin-actionable there. |
| Sequitur, thrash warnings, grammar-compressed action history, and `internal/checkpoint/grammar.go` | **SP-15** (same wave — do not touch that file) |
| Δ-scoring, redundancy detection, submodular lazy greedy, `analyzer.NewSelector` | **SP-15** |
| Slash commands, `/qompack:status`, `--json` output, `docs/commands.md` | **SP-14** (same wave) |
| The `mcp.Promoter` implementation itself, `NoteExpansion` firing, ephemeral tagging of retrieval results, the eight MCP tools | **SP-13** (merged). SP-16 *reads* the durable evidence SP-13 writes; it does not reimplement the counter. |
| `sketch.CMS.MergeFrom` / `Scale` / `HeavyHitters`, `Bloom` sizing, `RebuildBloom`, `ResizeTarget`, sketch serialization | **SP-03** (merged) |
| The elimination ledger, descriptors, `RefreshStaleness` and `RebuildBloom` mechanics, the three-way `already_tried` answer | **SP-09** (merged). SP-16 owns only *when* these run across a session boundary. |
| `SegmentLog` itself (`Open`/`Close`/`Get`/`Range`/`Current`/`MarkEncoded`/`Frontier`/`Unencoded`), the DPI guard, objects, GC, `ChangedSince` | **SP-06** (merged). SP-16 adds one call inside `Close` and one new file. |
| The checkpoint schema, `Writer`, `Reader`, `ExtractDecisions`, `FocusInstructions`, `ValidatePointers`, pins | **SP-10** (merged). SP-16 adds `promote.go` and `curve.go` and two anchored call sites. |
| BOCD itself, `Evaluate`'s composite trigger, p-selection scoring, Young–Daly, the sliding-TTL model, O3/O5 | **SP-12** (merged). SP-16 adds ski rental and prior seeding as new files, plus two anchored edits: one line inside `Evaluate`, and one line in SP-12's `scheduler.Runtime` implementation swapping detector construction for `NewDetectorFromState`. |
| The replay harness, Belady OPT, divergence metrics, `eval.Synthesize`, the 24-session corpus, the replay-gate driver | **SP-02** (merged). SP-16 adds test cases that *use* it. |
| The rehydrator's eight-item injection and 8–12K budget enforcement | **SP-11** (merged). SP-16 improves what the budget *contains*, never how it is enforced. |
| The daemon, IPC, hot-path budget, contract monitor, `IdleController` | **SP-05** (merged). SP-16 adds `daemon/phase7.go` and exactly one registration line. |
| Packaging, cross-platform matrix, security audit, `fsck`/`doctor`, release | **SP-17** |
| `docs/user-guide.md`, `docs/cannot-do.md`, `docs/upstream-issues.md`, `docs/troubleshooting.md`, UAT | **SP-18**. SP-16 writes only `docs/adr/0016-phase7-refinements.md` and regenerates `docs/config-reference.md`. |

---

## Interface contract

### Consumes (exact signatures from 00-ARCHITECTURE §5 — call these, do not change them)

```go
// internal/core (§4)
type Hash [32]byte
func (h Hash) String() string
func (h Hash) Short() string
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type SegmentID int
type CheckpointSeq int
type Tokens int
type UnixMilli int64
type Dep struct{ Path string; Hash Hash }
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
var ErrNotFound, ErrAppendOnly, ErrBudget, ErrDegraded error

// internal/paths
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte) error
func CreateNew(p string) (*os.File, error)
// AppendOnly is declared in 00-ARCHITECTURE §3.3 ("opens O_WRONLY|O_APPEND|O_CREATE and returns
// an error if O_TRUNC is requested… the ONLY write path allowed into *.jsonl"). SP-01 fixes its
// exact signature; the shape below is what SP-16 assumes. If SP-01's differs, call it unchanged —
// SP-16 never opens a .jsonl file any other way.
func AppendOnly(p string) (*os.File, error)

// internal/config (§5.1)
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
func (c Config) Validate() []Violation
type Violation struct{ Key, Message string; Got, Want any }

// internal/sketch (§5.7)
func NewBloom(capacity int, fpRate float64) *Bloom
func (b *Bloom) Add(key []byte)
func (b *Bloom) Test(key []byte) bool
func (b *Bloom) Count() int
func (b *Bloom) FillRatio() float64
func (b *Bloom) EstimatedFPRate() float64
func NewCMS(epsilon, delta float64) *CMS
func (c *CMS) Add(key []byte, n uint32)
func (c *CMS) Estimate(key []byte) uint32
func (c *CMS) MergeFrom(o *CMS) error      // errors on shape mismatch
func (c *CMS) Scale(factor float64)
func Save(p string, s Sketch) error
func Load(p string, s Sketch) error

// internal/store (§5.8)
type ToolUseRecord struct {
    ID core.ToolUseID; Session core.SessionID; Turn core.TurnIndex; TS core.UnixMilli
    Tool string; ArgsDigest core.Hash; ArgsPreview string; Root core.Hash; Path string
    Bytes int64; Tokens core.Tokens; Signature sketch.Signature
    Status Supersession; SupersededBy core.ToolUseID; Ephemeral bool; Subagent string
}
type Segment struct {
    ID core.SegmentID; Session core.SessionID
    StartTurn, EndTurn core.TurnIndex; StartTS, EndTS core.UnixMilli
    Features map[string]float64; Tokens core.Tokens
    EncodedOnce bool; CheckpointSeq core.CheckpointSeq; Closed bool; BloomRef string
}
type SegmentLog interface {
    Open(ctx context.Context, s Segment) (core.SegmentID, error)
    Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
    Get(ctx context.Context, id core.SegmentID) (Segment, error)
    Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
    Current(ctx context.Context, s core.SessionID) (Segment, error)
    MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
    Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
    Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error)
}
type Store interface { /* … */ Segments() SegmentLog; ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error); /* … */ }

// internal/negknow (§5.10)
type Ledger interface {
    Active(ctx context.Context, scope Scope) ([]Record, error)
    RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
    RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
    Health() Health
}

// internal/scheduler (§5.13)
type TriggerReason string // "soft_floor" "changepoint" "young_daly" "hard_ceiling" "idle_cold_cache"
                          // SP-16 widens this to seven — see the §5.13 amendment prerequisite in commit 2
type TTLState string      // "warm" | "expiring" | "cold" | "unknown"
type Urgency uint8        // UrgencyNone, UrgencyAdvisory, UrgencyNow
type Features struct{ PathJaccard, ToolShift, LexicalCohesion, GapSeconds, TodoTransition float64 }
type ChangepointState struct {
    RunLength int; ProbChangepoint float64; AtChangepoint bool; Posterior []float64
}
type Detector interface {
    Observe(f Features) ChangepointState
    State() ChangepointState
    Reset()
    MarshalBinary() ([]byte, error)
    UnmarshalBinary([]byte) error
}
func NewBOCD(hazardRate float64, features []string) Detector
type Candidate struct {
    Pos int; Turn core.TurnIndex; SegmentID core.SegmentID; RoundBoundary bool
    ReclaimableTokens core.Tokens; Coupling int
}
type Inputs struct{ /* … */
    ContextTokens, EffectiveWindow core.Tokens
    Candidates []Candidate; FrontierTurn core.TurnIndex
    ExpectedRemainingReads float64; Cfg config.SchedulerCfg /* … */ }
type Decision struct{ /* … */
    ShouldCompact bool; Reasons []TriggerReason
    P Candidate; PScore float64
    Breakdown map[string]float64; Urgency Urgency; TTL TTLState /* … */ }
func Evaluate(in Inputs) Decision
func SkiRentalShouldWrite(expectedReads, r, w float64) bool // §5.13; SP-12 SHIPPED this in skirental.go with the w<=0 guard — SP-16 only re-expresses it through SkiRentalThreshold

// internal/checkpoint (§5.14)
type Checkpoint struct{ /* §8.5 schema */ }
type Pointers struct{ Files []FilePointer; Tools []ToolPointer }
// FilePointer / ToolPointer are SP-10's, shaped by §8.5: {path, hash, why} and
// {tool_use_id, hash, summary}. SP-16 assumes `Hash` is a string in "sha256:…" form (the §8.5
// wire form) and therefore assigns `core.Hash.String()`. If SP-10 declared it as `core.Hash`,
// assign the `core.Hash` value directly and drop the `.String()` — that is the only permitted
// deviation, and `TestFinalizeIncludesPromotedPointers` pins the on-disk JSON either way.
type FilePointer struct{ Path, Hash, Why string }
type ToolPointer struct{ ToolUseID, Hash, Summary string }
type DropEntry struct{ Kind, ID, Detail string }
type SourceSet struct{ Store store.Store; Segments store.SegmentLog; Ledger negknow.Ledger; Pins pins.Store; Graph dag.Graph; Grammar grammar.Sequitur; Tokens tokens.Estimator }
func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)

// internal/daemon (§5.4)
type IdleController interface {
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}
type Options struct{ ProjectRoot string; Cfg config.Config; Log logging.Logger; Metrics obs.Registry; Clock core.Clock; Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet; Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime; Checkpoints checkpoint.Writer }

// internal/eval (§5.18)
type Harness interface {
    Load(dir string) ([]Session, error)
    Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
    Compare(uncompacted, compacted Run) Divergence
    Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
    ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
    Report(ctx context.Context, scores map[string][]Score) (Report, error)
}
```

### Produces (new public surface later subplans and CI rely on)

```go
// ── internal/config (SP-16 additions to the §11.5 runtime namespace) ──
type Phase7Cfg struct {
    WarmStart    WarmStartCfg    `json:"warmStart"`
    Promotion    PromotionCfg    `json:"promotion"`
    SegmentBloom SegmentBloomCfg `json:"segmentBloom"`
}
type WarmStartCfg struct {
    Enabled         bool    `json:"enabled"`
    MaxSessions     int     `json:"maxSessions"`
    Decay           float64 `json:"decay"`
    BOCDPriorWeight float64 `json:"bocdPriorWeight"`
    BudgetMs        int     `json:"budgetMs"`
}
type PromotionCfg struct {
    Enabled          bool    `json:"enabled"`
    SliceWeightBoost float64 `json:"sliceWeightBoost"`
    MaxPromoted      int     `json:"maxPromoted"`
}
type SegmentBloomCfg struct {
    Enabled            bool    `json:"enabled"`
    CapacityPerSegment int     `json:"capacityPerSegment"`
    FPRate             float64 `json:"fpRate"`
    BuildBudgetMs      int     `json:"buildBudgetMs"`
}
// RuntimeCfg gains: Phase7 Phase7Cfg `json:"phase7"`

// ── internal/scheduler/skirental.go ──
// r and w come from Inputs.Regime (scheduler.CacheRegime, SP-12's cacheregime.go), NEVER from
// cfg.Cache directly. See "the threshold is two numbers, not one" below.
func SkiRentalThreshold(r, w float64) float64
func SkiRentalShouldWrite(expectedReads, r, w float64) bool
func EstimateRemainingReads(in Inputs) float64
const (
    TriggerSkiRentalDefer   TriggerReason = "ski_rental_defer"
    TriggerSkiRentalShallow TriggerReason = "ski_rental_shallow"
)

// ── internal/scheduler/warmprior.go ──
type FeaturePrior struct{ Mu0, Kappa0, Alpha0, Beta0 float64 }
type FeaturePriors map[string]FeaturePrior
const (
    FeatPaths   = "paths"
    FeatTools   = "tools"
    FeatTime    = "time"
    FeatTodos   = "todos"
    FeatLexical = "lexical"
)
func DefaultFeaturePriors(features []string) FeaturePriors
func SeedPriorsFromSegments(featureSets []map[string]float64, weight float64, features []string) FeaturePriors
func NewBOCDWithPriors(hazardRate float64, features []string, p FeaturePriors) Detector
// NewDetectorFromState is the SINGLE construction point the daemon's scheduler Runtime uses, so
// a warm-started prior survives both daemon restart and the plain-BOCD persistence path:
//   blob is a seeded-detector wire record  → restore the seeded detector (priors preserved)
//   blob is a plain BOCD record            → restore a plain BOCD
//   blob is empty, corrupt, or unknown-ver → fresh NewBOCD(hazardRate, features)
func NewDetectorFromState(hazardRate float64, features []string, blob []byte) Detector
// SetPriorWeight type-asserts d to the seeded detector and sets its blend weight; returns false
// (and does nothing) for any other Detector.
func SetPriorWeight(d Detector, w float64) bool
func FeatureValue(f Features, key string) float64
func WithFeatureValue(f Features, key string, v float64) Features

// ── internal/store/segbloom.go ──
type SegKeyKind string
const (
    SegKeyPath    SegKeyKind = "path"
    SegKeyTool    SegKeyKind = "tool"
    SegKeyRoot    SegKeyKind = "root"
    SegKeyToolUse SegKeyKind = "tooluse"
)
func SegmentKey(kind SegKeyKind, value string) []byte
func SegmentBloomRef(id core.SegmentID) string
// BuildSegmentBloom takes projectRoot + cfg rather than a Store: it is an INDEX-ONLY pass over
// index/tool_use.jsonl (never an object read, §6.8), and the concrete segLog that calls it from
// Close is given both by §4's anchored edit while holding no Store handle.
func BuildSegmentBloom(ctx context.Context, projectRoot string, cfg config.Config, seg Segment) (ref string, keys int, err error)
func LoadSegmentBloom(projectRoot, ref string) (*sketch.Bloom, error)
// SegmentMayContain returns (true, nil) when no bloom exists and (true, err) when one exists but
// fails the CRC/version check — never a false negative. Callers that hold a logger
// (SegmentsMayContain) turn the non-nil error into one Loud entry per segment id per process.
func SegmentMayContain(projectRoot string, seg Segment, kind SegKeyKind, value string) (bool, error)
func SegmentsMayContain(ctx context.Context, s Store, projectRoot string, kind SegKeyKind, value string, from, to core.TurnIndex) ([]core.SegmentID, error)
func BackfillSegmentBlooms(ctx context.Context, s Store, projectRoot string, cfg config.Config, deadline time.Duration) (built int, err error)

// ── internal/store/segments.go (SP-06's file; the anchored edits of §4) ──
// segLog gains two fields, root string and cfg config.Config, and openSegLog takes them:
func openSegLog(p, root string, cfg config.Config, clk core.Clock, log logging.Logger) (*segLog, error)
// recordBloomRef appends SP-06's reserved segBloomRec and sets Segment.BloomRef in memory. It is
// unexported and NOT added to the SegmentLog interface — Rule W-3 freezes §5.8's seam.
func (l *segLog) recordBloomRef(ctx context.Context, id core.SegmentID, ref string) error

// ── internal/store/ephemeral.go ──
type Expansion struct{ Root core.Hash; Count int; Last ToolUseRecord }
// Both helpers below are index-only readers over index/tool_use.jsonl and therefore take
// projectRoot, not Store: `internal/checkpoint` and `internal/daemon` can call them without any
// access to this package's unexported concrete store type.
func EphemeralExpansions(ctx context.Context, projectRoot string, sess core.SessionID) ([]Expansion, error)
// ProjectPathTouches replays the tool_use index for the most recent maxSessions DISTINCT session
// ids (newest first) and returns paths.Key(path) → touch count plus the number of records
// counted. It is the "project's historical hot-file distribution" of §10 Phase 7 (O4).
func ProjectPathTouches(ctx context.Context, projectRoot string, maxSessions int) (touches map[string]int, records int, err error)

// ── internal/checkpoint/promote.go ──
type Promotion struct {
    Hash      core.Hash
    ToolUseID core.ToolUseID
    Path      string
    Count     int
    Weight    float64
    Why       string
    Summary   string
}
type PromotionReport struct {
    Promoted   []Promotion
    Skipped    map[string]string // always non-nil
    AddedFiles int
    AddedTools int
}
type PromotionInput struct {
    Session          core.SessionID
    ProjectRoot      string
    Threshold        int
    SliceWeightBoost float64
    MaxPromoted      int
    SliceScores      map[core.ToolUseID]float64
}
func Promote(ctx context.Context, c *Checkpoint, src SourceSet, in PromotionInput) (PromotionReport, error)
func PromotionInputFromConfig(cfg config.Config, sess core.SessionID, projectRoot string) PromotionInput

// ── internal/checkpoint/curve.go ──
const (
    ReserveLatePct  = 0.35
    ReserveFirstPct = 0.15
)
type TierName string
const (
    TierNever TierName = "never"
    TierLate  TierName = "late"
    TierFirst TierName = "first"
)
func TierReserve(tier TierName, avail core.Tokens) core.Tokens
func OrderPointers(p *Pointers, weights map[string]float64)
func Tier2DropOrder() []string
func Tier3DropOrder() []string

// ── internal/daemon/phase7.go ──
type WarmStartReport struct {
    Ran                 bool
    CMSMerged           bool
    CMSKeysBootstrapped int
    EliminationsCarried int
    EliminationsFlipped int
    BOCDPriorFeatures   []string
    SegmentsSampled     int
    DurationMs          int64
    Reason              string
}
type WarmStarter struct{ /* unexported */ }
func NewWarmStarter(o Options) *WarmStarter
// SetSession records the session the idle-registered tasks operate on. The daemon calls it at
// session registration; WarmStarter needs it because daemon.Options carries no session accessor.
func (w *WarmStarter) SetSession(sess core.SessionID)
// SeedPriors runs warm-start step 3 (BOCD feature-prior seeding) ONLY, synchronously, bounded by
// min(budgetMs, 1000) ms. The daemon calls it immediately before it constructs that session's
// scheduler.Runtime, so state/bocd.json is on disk before NewDetectorFromState reads it.
// Idempotent per session: a no-op when state/warmstart.json already records bocdSeededFor == sess.
func (w *WarmStarter) SeedPriors(ctx context.Context, sess core.SessionID) error
func (w *WarmStarter) MaybeRun(ctx context.Context, sess core.SessionID) WarmStartReport
func (w *WarmStarter) LastReport() WarmStartReport
func RegisterPhase7(o Options, idle IdleController) *WarmStarter
```

**Why no §5 interface amendment is required.** Every addition above is either a new package-level symbol in a package this subplan writes a new file in, a new field on a struct declared inside `internal/config`'s own `RuntimeCfg` (which §5.1 declares only as `Runtime RuntimeCfg` without fixing its members), or population of a field 00-ARCHITECTURE already reserved for SP-16 (`Segment.BloomRef`, `Inputs.ExpectedRemainingReads`). `NewDetectorFromState` is a package-level **function**, not a method on the `Detector` or `Runtime` interface, so swapping SP-12's construction site over to it changes no interface either. **No interface in §5 gains, loses, or changes a method.** Rule W-3 is not engaged. One documentation edit to `plans/00-ARCHITECTURE.md` §11.5 and one comment update in §5.8 record the additive namespace and the now-populated field; that edit lands inside this branch's commit 1 and is additive by §11.5's own rule ("No key here may change the meaning or default of any Appendix C key").

**The one thing that does need an amendment, and it is not a method.** `TriggerSkiRentalDefer` and `TriggerSkiRentalShallow` are new **wire values**, not aliases of the shipped five: they widen §5.13's `TriggerReason` vocabulary from five to seven, and §5.13's inline enumeration plus `internal/scheduler/types.go`'s "five named conditions" godoc both still say five. That widening is an §0 amendment and lands on its own `arch/` branch **before** `feat/sp16-phase7-refinements` is cut, so this branch's own `00-ARCHITECTURE.md` edits remain exactly the two documentation edits above. See commit 2's first bullet.

---

## Implementation spec

### Global rules for this branch

- Work happens **only** on `feat/sp16-phase7-refinements`, cut from `develop` after the wave-3 verification tag. Never commit to `develop` or `main`.
- **Never modify `Qompack.md`.** It stays at the repo root as the canonical design reference.
- Every file this subplan **creates** is owned by SP-16. Every file this subplan **modifies** gets exactly the anchored edit described below and nothing else — these are files SP-05/SP-06/SP-10/SP-12 own.
- `gofumpt`, `golangci-lint`, and the in-repo `nomagic` pass must be clean before each commit (`go run ./tools/devtool fmt lint vet`).
- No literal from the `nomagic` forbidden set (`{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` floats, `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` ints) may appear outside `internal/config/defaults.go` and `*_test.go`. **`12.5` must not appear anywhere in non-test source.**

---

### 1. `internal/config` — the `runtime.phase7` namespace

**Create `internal/config/phase7.go`.** Declares `Phase7Cfg`, `WarmStartCfg`, `PromotionCfg`, `SegmentBloomCfg` exactly as in the Produces block, plus:

```go
// validatePhase7 returns one Violation per out-of-range leaf. It NEVER panics and NEVER
// mutates: config.Load applies the per-leaf fallback (§11.3 "Behaviour on invalid config is
// not crash"). Every `Want` is read from Defaults().Runtime.Phase7 — NEVER written as a literal,
// because 2048 is on the nomagic forbidden int set (§11.6) and internal/config/phase7.go is not
// internal/config/defaults.go. Bind `d := Defaults().Runtime.Phase7` once at the top.
func validatePhase7(p Phase7Cfg) []Violation
```

Rules, each producing `Violation{Key, Message, Got, Want}` with `Want` taken from
`Defaults().Runtime.Phase7`:

| Key | Rule | `Want` expression | Value |
|---|---|---|---|
| `runtime.phase7.warmStart.maxSessions` | `>= 1 && <= 200` | `d.WarmStart.MaxSessions` | `10` |
| `runtime.phase7.warmStart.decay` | `> 0 && <= 1` | `d.WarmStart.Decay` | `0.6` |
| `runtime.phase7.warmStart.bocdPriorWeight` | `>= 0 && <= 1` | `d.WarmStart.BOCDPriorWeight` | `0.35` |
| `runtime.phase7.warmStart.budgetMs` | `>= 50 && <= 30000` | `d.WarmStart.BudgetMs` | `3000` |
| `runtime.phase7.promotion.sliceWeightBoost` | `>= 1 && <= 8` | `d.Promotion.SliceWeightBoost` | `1.75` |
| `runtime.phase7.promotion.maxPromoted` | `>= 1 && <= 200` | `d.Promotion.MaxPromoted` | `12` |
| `runtime.phase7.segmentBloom.capacityPerSegment` | `>= 128 && <= 262144` | `d.SegmentBloom.CapacityPerSegment` | `2048` |
| `runtime.phase7.segmentBloom.fpRate` | `> 0 && < 0.25` | `d.SegmentBloom.FPRate` | `0.01` |
| `runtime.phase7.segmentBloom.buildBudgetMs` | `>= 10 && <= 5000` | `d.SegmentBloom.BuildBudgetMs` | `250` |

`Enabled` booleans have no range and produce no violations.

**Why `buildBudgetMs` caps at 5000 and not a rounder 10000.** `10000` is on the `nomagic`
forbidden integer set (§11.6) and a range bound is not a config default, so it cannot live in
`defaults.go`. 5000 is 20× the default and far beyond any legitimate segment-close budget.
`Message` for every row is `fmt.Sprintf("out of range %s", <rule text>)`.

**Modify `internal/config`** with exactly three anchored edits. SP-01 places these symbols in `runtime.go`, `defaults.go`, and `validate.go`; if the layout differs, edit wherever the symbol is declared and **do not move symbols between files**.

1. In the declaration of `RuntimeCfg`, append one field:
   ```go
   Phase7 Phase7Cfg `json:"phase7"`
   ```
2. In `Defaults()`, inside the `Runtime:` literal, append:
   ```go
   Phase7: Phase7Cfg{
       WarmStart:    WarmStartCfg{Enabled: true, MaxSessions: 10, Decay: 0.6, BOCDPriorWeight: 0.35, BudgetMs: 3000},
       Promotion:    PromotionCfg{Enabled: true, SliceWeightBoost: 1.75, MaxPromoted: 12},
       SegmentBloom: SegmentBloomCfg{Enabled: true, CapacityPerSegment: 2048, FPRate: 0.01, BuildBudgetMs: 250},
   },
   ```
3. In `Config.Validate()`, immediately before the return, append:
   ```go
   v = append(v, validatePhase7(c.Runtime.Phase7)...)
   ```

**Modify `plans/00-ARCHITECTURE.md`** — two documentation edits only:

- §11.5, append inside the `"runtime": { … }` jsonc block, after the `"mcp"` line:
  ```jsonc
  "phase7": {                            // SP-16, §10 Phase 7 — additive, changes no Appendix C key
    "warmStart":    { "enabled": true, "maxSessions": 10, "decay": 0.6,
                      "bocdPriorWeight": 0.35, "budgetMs": 3000 },   // O4 (§10 Phase 7)
    "promotion":    { "enabled": true, "sliceWeightBoost": 1.75, "maxPromoted": 12 }, // §8.7
    "segmentBloom": { "enabled": true, "capacityPerSegment": 2048,
                      "fpRate": 0.01, "buildBudgetMs": 250 }         // §6.8
  }
  ```
- §5.8, change the `BloomRef` comment from `// per-segment bloom, LSM-style (§6.8); "" until SP-16` to `// per-segment bloom, LSM-style (§6.8); populated by SP-16 at SegmentLog.Close`.

**Regenerate `docs/config-reference.md`** with `go run ./tools/devtool gen-config-docs` (the CI `docs` job diffs it).

---

### 2. `internal/scheduler/skirental.go` — the §5.6 / Appendix A ski-rental policy

Package `scheduler` imports foundation packages only; this file adds no import beyond `math`.

**`SkiRentalShouldWrite` already exists and already works — in SP-12's `internal/scheduler/skirental.go`.**
SP-01 shipped the closed form in `formulas.go` under 00-ARCHITECTURE §14.1's "fully specified pure
functions are implemented, not stubbed" rule; SP-12 (wave 3) deleted that file and moved the
function here, adding a `w <= 0` guard so a cache write that cannot be free no longer reports
"always write". Commit 2 therefore **modifies SP-12's existing `skirental.go` in place** — no file
is created and nothing is moved — re-expressing the body through the new `SkiRentalThreshold` so the
ratio has exactly one definition. The re-expression is behaviour-preserving on every case SP-12's
`TestSkiRentalShouldWrite` (`r = 0 → false`, `w = 0 → false`), `TestSkiRental_ComputedNotLiteral` and
`TestSkiRental_ThresholdTracksConfig` assert; the only extension is that a NaN `r`, `w` or
`expectedReads` now folds into the same conservative `false` explicitly rather than by accident of
IEEE comparison. All three tests must stay green **unedited** across the re-expression — they are
the regression proof that it changed nothing. There is no stub to delete anywhere.

```go
// SkiRentalThreshold is Appendix A's w/r. It is COMPUTED, never written as 12.5 — the literal
// is on the nomagic forbidden list precisely so this stays true (§11.6, §12 "Cache multipliers
// change"). At the Appendix C defaults r=0.1, w=1.25 it evaluates to 12.5. It carries SP-12's
// r <= 0 || w <= 0 guard, so when either multiplier is unusable it reports "never amortizable"
// (+Inf) rather than the bare ratio — a write that cannot be free must not read as free.
func SkiRentalThreshold(r, w float64) float64 {
    if r <= 0 || w <= 0 || math.IsNaN(r) || math.IsNaN(w) {
        return math.Inf(1) // an unusable multiplier means "never amortizable"
    }
    return w / r
}

// SkiRentalShouldWrite implements Appendix A verbatim:
//   write when  E[remaining reads] > w/r
// Strictly greater: exactly at the threshold the two policies cost the same and the
// deterministic competitive-ratio-2 rule rents.
//
// This is SP-12's shipped body, re-expressed in place through SkiRentalThreshold; its
// r <= 0 || w <= 0 guard now lives in the threshold helper. Preserve SP-12's doc comment
// verbatim — the "neither that ratio nor its operands may ever appear as a literal in this
// package" rule is what nomagic's forbidden 12.5 enforces, and it belongs next to the function.
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
    t := SkiRentalThreshold(r, w)
    if math.IsInf(t, 1) || math.IsNaN(expectedReads) {
        return false
    }
    return expectedReads > t
}
```

`EstimateRemainingReads` answers "how many more cache reads will the rebuilt prefix serve before the next forced rebuild?" — one read per API round, and the prefix is rebuilt at the hard ceiling.

```go
func EstimateRemainingReads(in Inputs) float64 {
    hardCeiling := int(in.EffectiveWindow) - in.Cfg.HardCeilingMargin
    headroom := hardCeiling - int(in.ContextTokens)
    if headroom <= 0 {
        return 0
    }
    turn := int(in.FrontierTurn)
    for _, c := range in.Candidates {
        if int(c.Turn) > turn {
            turn = int(c.Turn)
        }
    }
    if turn < 1 || in.ContextTokens <= 0 {
        return 0 // not enough history to estimate — treat as "do not pay"
    }
    meanTurnTokens := float64(in.ContextTokens) / float64(turn+1)
    if meanTurnTokens < 1 {
        meanTurnTokens = 1
    }
    return float64(headroom) / meanTurnTokens
}
```

Worked example asserted by `TestEstimateRemainingReads`: `EffectiveWindow=180000`, `HardCeilingMargin=20000`, `ContextTokens=100000`, `FrontierTurn=100` ⇒ `hardCeiling=160000`, `headroom=60000`, `meanTurnTokens=100000/101=990.099…`, result `60.6` (assert within `±0.01`).

**Anchored edit inside `Evaluate`** (`internal/scheduler`, SP-12's file). Insert the block below **after** the p-selection argmax has produced `d.P`, `d.PScore`, and `d.Breakdown`, and **before** `Evaluate` returns `d`. Extract it into `applySkiRental(&d, in)` declared in `skirental.go` so the edit inside `Evaluate` is a single line: `applySkiRental(&d, in)`.

```go
func applySkiRental(d *Decision, in Inputs) {
    r := in.Cfg.Cache.ReadMultiplier
    w := in.Cfg.Cache.WriteMultiplier
    exp := in.ExpectedRemainingReads
    if exp <= 0 {
        exp = EstimateRemainingReads(in)
    }
    thr := SkiRentalThreshold(r, w)
    if d.Breakdown == nil {
        d.Breakdown = map[string]float64{}
    }
    if math.IsInf(thr, 1) {
        // NEVER write a non-finite value into Breakdown: encoding/json refuses +Inf, and
        // Decision is round-tripped through testdata/golden/scheduler/decision-*.json (SP-12)
        // and rendered by /qompack:status. Report the unamortizable case with a companion flag
        // instead, matching SP-12's young_daly_delta_unmeasured=1 pattern.
        d.Breakdown["ski_rental_unamortizable"] = 1
    } else {
        d.Breakdown["ski_rental_threshold"] = thr
    }
    d.Breakdown["expected_remaining_reads"] = exp
    if SkiRentalShouldWrite(exp, r, w) {
        return // the rewrite amortizes; leave p-selection exactly as chosen
    }
    if d.TTL != TTLWarm {
        return // cold or expiring: rewrite(p) is already ~0, ski rental is moot (§5.4)
    }
    // Not amortizable and the cache is warm. Two corrections, in this order.
    // (a) Prefer the shallowest legal cut among near-optimal candidates.
    band := d.PScore - 0.05*math.Abs(d.PScore)
    best := d.P
    changed := false
    for _, c := range in.Candidates {
        if scoreOf(in, c) >= band && c.Pos > best.Pos {
            best, changed = c, true
        }
    }
    if changed {
        d.P = best
        d.Reasons = append(d.Reasons, TriggerSkiRentalShallow)
    }
    // (b) If the ONLY reasons to compact are the amortizable ones, defer.
    if d.ShouldCompact && onlySoftReasons(d.Reasons) {
        d.ShouldCompact = false
        d.Urgency = UrgencyAdvisory
        d.Reasons = append(d.Reasons, TriggerSkiRentalDefer)
    }
}

// onlySoftReasons reports whether every reason is one that the ski-rental horizon may
// legitimately override. hard_ceiling, idle_cold_cache and changepoint are NEVER overridden:
// the first is a correctness bound (§8.4 "the plugin always gets to checkpoint first"), the
// second means the rewrite is already free, the third means distortion is near-zero anyway.
func onlySoftReasons(rs []TriggerReason) bool {
    if len(rs) == 0 {
        return false
    }
    for _, r := range rs {
        switch r {
        case TriggerSoftFloor, TriggerYoungDaly, TriggerSkiRentalShallow:
        default:
            return false
        }
    }
    return true
}
```

`scoreOf(in Inputs, c Candidate) float64` is the existing per-candidate score already computed inside `Evaluate` (`reclaimable(p)·r − rewrite(p) − λ·segment_coupling(p)` per §8.4); factor it out of `Evaluate` into an unexported helper in SP-12's file if it is currently inline, changing nothing about the formula.

**Error handling.** `applySkiRental` cannot fail. NaN/Inf inputs produce `SkiRentalShouldWrite == false`, i.e. the conservative "do not pay for a rewrite" answer, which can only ever *delay* a compaction the hard ceiling will force anyway.

**Performance.** One extra pass over `in.Candidates` (§8.4: "Twenty candidates, not 167,000"). `BenchmarkEvaluateWithSkiRental` must stay within `benchstat`'s 10% warn / 25% fail band against the `develop` baseline in `testdata/bench-baseline.txt` (§7).

---

### 3. `internal/scheduler/warmprior.go` — BOCD feature-prior seeding (O4)

Historical segments carry `Features map[string]float64` — "BOCD feature summary at close" (§5.8). Seeding uses Normal–Gamma moment matching over those summaries.

```go
type FeaturePrior struct{ Mu0, Kappa0, Alpha0, Beta0 float64 }
type FeaturePriors map[string]FeaturePrior
```

`DefaultFeaturePriors(features []string) FeaturePriors` returns, for each requested key:

| Key | Feature field | Mu0 | Kappa0 | Alpha0 | Beta0 |
|---|---|---|---|---|---|
| `paths` | `PathJaccard` | 0.5 | 1 | 1 | 0.0625 |
| `tools` | `ToolShift` | 0.5 | 1 | 1 | 0.0625 |
| `lexical` | `LexicalCohesion` | 0.5 | 1 | 1 | 0.0625 |
| `todos` | `TodoTransition` | 0.5 | 1 | 1 | 0.0625 |
| `time` | `GapSeconds` | 30 | 1 | 1 | 3600 |

(`Beta0 = Alpha0·σ_d²` with `σ_d = 0.25` for the four bounded features and `σ_d = 60` for `time`, so `E[σ²] = Beta0/Alpha0` recovers `σ_d²`.) Unknown keys are ignored; `features` defaults to `["paths","tools","time","todos"]` when empty (the Appendix C default list).

```go
// SeedPriorsFromSegments moment-matches a Normal–Gamma prior per feature from the feature
// summaries of closed segments in PAST sessions. weight ∈ [0,1] is
// runtime.phase7.warmStart.bocdPriorWeight; it scales the pseudo-count so a project with a
// long history dominates and a project with two sessions barely moves the default.
func SeedPriorsFromSegments(featureSets []map[string]float64, weight float64, features []string) FeaturePriors {
    out := DefaultFeaturePriors(features)
    for _, key := range keysOf(out) {
        var xs []float64
        for _, fs := range featureSets {
            if v, ok := fs[key]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
                xs = append(xs, v)
            }
        }
        n := len(xs)
        if n < 2 || weight <= 0 {
            continue // too little evidence: keep the default prior for this feature
        }
        var sum float64
        for _, x := range xs { sum += x }
        mean := sum / float64(n)
        var ss float64
        for _, x := range xs { ss += (x - mean) * (x - mean) }
        varUnbiased := ss / float64(n-1)
        if varUnbiased < 1e-9 {
            continue // degenerate: a constant feature carries no scale information
        }
        kappa := weight * float64(n)
        alpha := 1 + kappa/2
        out[key] = FeaturePrior{Mu0: mean, Kappa0: kappa, Alpha0: alpha, Beta0: alpha * varUnbiased}
    }
    return out
}
```

**Seeding without touching SP-12's internals.** `NewBOCDWithPriors` wraps `NewBOCD` in a `seeded` detector that standardizes each incoming feature against the project prior and re-expresses it on the *default* prior's scale, so the inner BOCD — tuned for default-scale features — sees an input whose "normal" is the project's normal.

```go
type seeded struct {
    inner    Detector
    priors   FeaturePriors
    defaults FeaturePriors
    weight   float64
    features []string
}

func NewBOCDWithPriors(hazardRate float64, features []string, p FeaturePriors) Detector {
    if len(features) == 0 {
        features = []string{FeatPaths, FeatTools, FeatTime, FeatTodos}
    }
    return &seeded{
        inner: NewBOCD(hazardRate, features),
        priors: p, defaults: DefaultFeaturePriors(features),
        weight: 1, features: features,
    }
}

func (s *seeded) standardize(f Features) Features {
    out := f
    for _, k := range s.features {
        pr, ok := s.priors[k]
        df, ok2 := s.defaults[k]
        if !ok || !ok2 || pr.Alpha0 <= 0 {
            continue
        }
        sigmaP := math.Sqrt(pr.Beta0 / pr.Alpha0)
        sigmaD := math.Sqrt(df.Beta0 / df.Alpha0)
        if sigmaP < 1e-6 {
            continue
        }
        x := FeatureValue(f, k)
        z := (x - pr.Mu0) / sigmaP
        mapped := df.Mu0 + z*sigmaD
        out = WithFeatureValue(out, k, (1-s.weight)*x+s.weight*mapped)
    }
    return out
}

func (s *seeded) Observe(f Features) ChangepointState { return s.inner.Observe(s.standardize(f)) }
func (s *seeded) State() ChangepointState             { return s.inner.State() }
func (s *seeded) Reset()                              { s.inner.Reset() }
```

`s.weight` is set by the caller to `runtime.phase7.warmStart.bocdPriorWeight`; `NewBOCDWithPriors` defaults it to `1` and `daemon/phase7.go` overrides it via a package-level setter `SetPriorWeight(d Detector, w float64) bool` declared in this file (type-asserts to `*seeded`, returns `false` for any other detector).

Serialization keeps the `Detector` contract:

```go
type seededWire struct {
    Kind     string        `json:"kind"`      // always "qompack.scheduler.seeded" — the
                                              // discriminator NewDetectorFromState switches on
    V        int           `json:"v"`         // 1
    Weight   float64       `json:"weight"`
    Features []string      `json:"features"`
    Priors   FeaturePriors `json:"priors"`
    Defaults FeaturePriors `json:"defaults"`
    Inner    []byte        `json:"inner"`     // base64 by encoding/json
}
func (s *seeded) MarshalBinary() ([]byte, error)   // json.Marshal(seededWire)
func (s *seeded) UnmarshalBinary(b []byte) error   // rejects a wrong Kind or V != 1
```

`UnmarshalBinary` returns `fmt.Errorf("scheduler: seeded detector version %d: %w", w.V, core.ErrNotFound)` on an unknown version and the same error shape with `kind %q` on a wrong `Kind` — the daemon then discards the persisted state and starts from a fresh detector, exactly as `sketch.Load` does on a version mismatch (§5.7).

**`NewDetectorFromState` — why it exists and why it is the one edit into SP-12's Runtime.** Warm start persists the seeded detector to `.qompack/state/bocd.json` (§8 below) rather than injecting it, because `scheduler.Runtime` has no detector setter and Rule W-3 forbids adding one. But SP-12's Runtime restores its detector by unmarshalling that same file into a **plain** `NewBOCD`, which would reject the seeded wire record and silently throw the priors away. `NewDetectorFromState` closes that seam:

```go
func NewDetectorFromState(hazardRate float64, features []string, blob []byte) Detector {
    if len(blob) > 0 {
        s := &seeded{}
        if err := s.UnmarshalBinary(blob); err == nil {
            return s
        }
        d := NewBOCD(hazardRate, features)
        if err := d.UnmarshalBinary(blob); err == nil {
            return d
        }
    }
    return NewBOCD(hazardRate, features)
}
```

`seeded.UnmarshalBinary` must reject a plain-BOCD blob (wrong `Kind`) so the two branches are unambiguous; `TestNewDetectorFromStateDisambiguates` asserts both directions plus the empty and corrupt cases.

**Anchored edit in SP-12's `scheduler.Runtime` implementation** (`internal/daemon`, the file SP-12 owns per 00-ARCHITECTURE §5.13 "the `Runtime` **implementation** lives in `internal/daemon`"). Exactly one line changes: wherever the Runtime currently builds its detector from the bytes it loaded out of `state/bocd.json`, replace

```go
det := scheduler.NewBOCD(cfg.Scheduler.Changepoint.HazardRate, cfg.Scheduler.Changepoint.Features)
// … followed by det.UnmarshalBinary(blob) on the restore path
```

with

```go
det := scheduler.NewDetectorFromState(cfg.Scheduler.Changepoint.HazardRate,
    cfg.Scheduler.Changepoint.Features, blob) // blob = state/bocd.json bytes, nil when absent
```

and delete the now-redundant `UnmarshalBinary` call. Nothing else in that file changes.

`FeatureValue`/`WithFeatureValue` map the five keys onto `Features`' five fields; an unknown key returns `0` / returns `f` unchanged.

---

### 4. `internal/store/segbloom.go` — per-segment Bloom filters (§6.8)

**On-disk layout.** One file per closed segment at `.qompack/sketches/segments/<NNNNNN>.bloom`, `NNNNNN` = `core.SegmentID` zero-padded to six digits (`fmt.Sprintf("%06d", int(id))`). `Segment.BloomRef` stores the project-relative forward-slash path `sketches/segments/000012.bloom`. The file is a `sketch.Bloom` written through its `MarshalBinary` with the standard `sketch.Header` (`Magic 'Q','P','K','S'`, `Kind KindBloom`, CRC32C).

Written with `paths.CreateNew` and then chmod `0444` — a closed segment is immutable, so the file is written once. `os.IsExist` on `CreateNew` is **not** an error: it means the bloom is already built, and `BuildSegmentBloom` returns the existing ref with `keys = -1`. `sketches/tried.bloom` is the only append-only-protected bloom (§3.3); segment blooms are new immutable files and do not touch that guard.

**Durable lookup: `index/segments.jsonl`, and no sidecar.** SP-06 already shipped the writer seam this needs. `internal/store/segments.go` declares

```go
// segBloomRec names a segment's own per-segment bloom file.
//
// SP-06 PARSES this record and surfaces it as Segment.BloomRef, but never writes one: the writer
// is SP-16's, which is exactly the `"" until SP-16` reservation 00-ARCHITECTURE.md §5.8 describes.
type segBloomRec struct {
    V   int            `json:"v"`
    Op  string         `json:"op"`
    ID  core.SegmentID `json:"id"`
    Ref string         `json:"ref"`
}
```

under the op constant `segOpBloom = "bloom"`, and its `load` replay already does `seg.BloomRef = r.Ref` for every such line. A bloom ref is therefore recorded by appending **one more line to the append-only `index/segments.jsonl`** — exactly the way `segCloseRec` records a close and `segEncodeRec` records an encode — and it survives a reopen through the same replay, for segments closed by this branch and for backfilled ones alike.

**There is no `.qompack/sketches/segments/INDEX.jsonl` sidecar, and SP-16 must not create one.** A second append-only file describing the same fact would be a second source of truth for it, and `segments.jsonl` is already the log of record for everything else about a segment.

Backfill needs nothing extra either: `SegmentBloomRef(id)` is a pure function of the segment id, so the read path resolves `seg.BloomRef` when it is set and falls back to the computed path when it is not. A bloom that `BackfillSegmentBlooms` has written is usable the instant its file exists, whether or not its `segBloomRec` line has landed yet; a segment with neither file nor ref answers "may contain" conservatively.

**Key scheme.** Domain-separated, matching `core.HashBytes`:

```go
func SegmentKey(kind SegKeyKind, value string) []byte {
    h := core.HashBytes("qompack.segbloom.v1", []byte(string(kind)+"\x00"+value))
    return h[:]
}
```

Four kinds, all v1. **Symbols are deliberately excluded from v1**: inserting symbol keys would require reading and extracting every stored object at segment close, and §6.8's stated purpose — "so the agent knows which segment to expand *without expanding any*" — is fully served by path/tool/root/tool_use_id, which are already in the index. Excluding them keeps `BuildSegmentBloom` an index-only pass with no object reads.

| Kind | Value | Source |
|---|---|---|
| `path` | `paths.Key(rec.Path)` | `ToolUseRecord.Path`, skipped when empty |
| `tool` | `rec.Tool` | `ToolUseRecord.Tool` |
| `root` | `rec.Root.String()` (`"sha256:"+hex`) | `ToolUseRecord.Root`, skipped when zero |
| `tooluse` | `string(rec.ID)` | `ToolUseRecord.ID` |

**Sizing, per Appendix A — and why capacity is a floor, not a cap.** Appendix A gives `m = −n·ln(p)/(ln 2)²` and `k = (m/n)·ln 2`, where `n` is the number of keys **actually inserted**. A segment contributes up to four keys per record, so a 1 500-record segment inserts ≈ 3 100 distinct keys; sizing that at a fixed `n = 2048` would push the real false-positive rate to ≈ 6% — past §11.4's "at 10% the agent starts skipping viable approaches" warning band and well past the 1% design rate. The rule is therefore:

```
n = max(capacityPerSegment, len(distinctKeys))
b = sketch.NewBloom(n, fpRate)
```

with `capacityPerSegment = runtime.phase7.segmentBloom.capacityPerSegment` (default 2048) and `fpRate = …segmentBloom.fpRate` (default 0.01). The distinct-key set is collected during the single index pass (a `map[string]struct{}` over the 32-byte keys — ≈ 100 KB at 3 000 keys, transient), so no second pass is needed. At the design fill `n = 2048, p = 0.01` this reproduces Appendix A exactly: `m = 2048·4.60517/0.480453 = 19629.9 → 19630 bits ≈ 2454 bytes ≈ 2.4 KB` and `k = (19630/2048)·0.693147 = 6.644 → 7`. `TestBuildSegmentBloomSizesPerAppendixA` asserts `m ∈ [19600, 19700]` and `k == 7` for a segment under the floor; `TestSegmentBloomSizesUpForLargeSegments` asserts the grow path.

```go
func BuildSegmentBloom(ctx context.Context, projectRoot string, cfg config.Config, seg Segment) (ref string, keys int, err error)
```

It takes `projectRoot` and `cfg` rather than a `Store` for two reasons: it is an **index-only** pass (never `Open`/`OpenSpan`/`GetChunk`/`GetRoot`, which is the whole point of §6.8), and the concrete `segLog` that calls it from `Close` is handed a project root and a `config.Config` by the anchored edit below but holds no `Store` handle and never will. Reading `index/tool_use.jsonl` directly is legitimate here: `internal/store` owns that file.

Algorithm:

1. If `!cfg.Runtime.Phase7.SegmentBloom.Enabled` return `("", 0, nil)`.
2. `ref = SegmentBloomRef(seg.ID)`. If the target file exists, return `(ref, -1, nil)`.
3. Deadline: `ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Runtime.Phase7.SegmentBloom.BuildBudgetMs)*time.Millisecond)`; `defer cancel()`. Check `ctx.Err()` once before the scan so a zero/expired budget short-circuits even on a tiny segment.
4. Iterate `<projectRoot>/.qompack/index/tool_use.jsonl` through the package's existing JSONL line reader, filtering `rec.Session == seg.Session && rec.Turn >= seg.StartTurn && rec.Turn <= seg.EndTurn`. For each record add the four keys above to the distinct-key set (skipping empty values; `rec.Path` is already in `paths.Key` form per §5.8, so `paths.Key` is applied defensively and is a no-op). Count records. Check `ctx.Err()` every 256 records; on deadline set `truncated = true` and stop.
5. If `truncated`, **do not write the file** — a partial bloom produces false *negatives*, which would make a segment wrongly un-expandable. Return `("", 0, nil)` and log `Warn` with the segment id. A missing bloom is handled conservatively by the read path.
6. `b := sketch.NewBloom(max(capacityPerSegment, len(keySet)), fpRate)`; insert every key in sorted order so the serialized bytes are deterministic.
7. Marshal, write with `paths.CreateNew` + `Sync` + chmod `0444`. Nothing else is written here: recording the ref in `index/segments.jsonl` belongs to the caller — `SegmentLog.Close`'s anchored edit below, or `BackfillSegmentBlooms` — because only the segment log may append to its own file, and `BuildSegmentBloom` deliberately holds no log handle.
8. Return `(ref, len(keySet), nil)`.

**Read path.**

```go
func LoadSegmentBloom(projectRoot, ref string) (*sketch.Bloom, error)
```
Joins `projectRoot` with the forward-slash `ref`, allocates a zero-value `*sketch.Bloom`, and fills it with `sketch.LoadWithLog(p, b, logging.Nop())` (which checks magic, version, and CRC32C per §5.7). **Never `sketch.Load`**: `test/guards/sketchload_test.go`'s `TestGuard_NoSilentSketchLoadOutsideItsPackage` fails the build on `sketch.Load` in any non-test file outside `internal/sketch`, and `internal/store` already imports `internal/logging`. The `logging.Nop()` is deliberate and is not silence: this function's signature stays logger-free so `SegmentMayContain` can remain pure, and the error is returned up to `SegmentsMayContain`, which holds the `Store`'s logger and emits the single de-duplicated `Loud` entry per segment id (see below). Returns `core.ErrNotFound` when the file is absent, and `LoadWithLog`'s error unchanged when it is corrupt — that error satisfies `errors.Is` against both `core.ErrNotFound` and the decoder sentinel (`sketch.ErrCorrupt` for a CRC mismatch, `sketch.ErrUnsupportedVersion` for a version bump).

```go
func SegmentMayContain(projectRoot string, seg Segment, kind SegKeyKind, value string) (bool, error)
```
Resolves the ref — `seg.BloomRef` when it is set, otherwise the computed `SegmentBloomRef(seg.ID)` — and calls `LoadSegmentBloom` + `Test(SegmentKey(kind, value))`. **No bloom ⇒ return `(true, nil)`** — the conservative answer: a segment we cannot rule out must be considered a candidate. A corrupt bloom returns `(true, err)`: still conservative, but the error is surfaced so a caller that holds a logger can report it. `SegmentsMayContain` — which does hold one, via `s` — converts a non-nil error into exactly one `Loud` entry per segment id per process, de-duplicated through a package-level `sync.Map` keyed on `core.SegmentID`. Keeping the logging in the `Store`-taking variant is what lets `SegmentMayContain` stay a pure function with no package-level logger.

```go
func SegmentsMayContain(ctx context.Context, s Store, projectRoot string, kind SegKeyKind, value string, from, to core.TurnIndex) ([]core.SegmentID, error)
```
Calls `s.Segments().Range(ctx, from, to)` and filters with `SegmentMayContain`, preserving `Range`'s order. **It never calls `Open`, `OpenSpan`, `GetChunk`, or `GetRoot`** — that is the whole point of §6.8, and `TestSegmentsMayContainNarrowsWithoutExpanding` asserts it against a counting fake `Store`.

```go
func BackfillSegmentBlooms(ctx context.Context, s Store, projectRoot string, cfg config.Config, deadline time.Duration) (built int, err error)
```
Enumerates closed segments via `Range(ctx, 0, core.TurnIndex(math.MaxInt32))` where `seg.Closed && seg.BloomRef == ""`; calls `BuildSegmentBloom(ctx, projectRoot, cfg, seg)` per segment until `deadline` elapses; on every non-empty `ref` it **records the ref durably** through `recordBloomRef` (below) so the next reopen replays `seg.BloomRef` out of `segments.jsonl` rather than rebuilding; returns the count built. A `recordBloomRef` failure is logged `Warn` and does not abort the sweep — the bloom file is already on disk and the computed-ref fallback still finds it. Idempotent and resumable — running it twice builds `n` then `0`.

`recordBloomRef` is the one new unexported method on the concrete segment log, and it is what `BackfillSegmentBlooms` reaches for:

```go
// recordBloomRef appends this segment's bloom ref to index/segments.jsonl and sets it in memory.
// Unexported and NOT on the SegmentLog interface: Rule W-3 freezes §5.8's seam, and nothing
// outside internal/store has any business naming a bloom file.
func (l *segLog) recordBloomRef(ctx context.Context, id core.SegmentID, ref string) error
```

It takes `l.mu`, refuses with `core.ErrDegraded` when the log is degraded, returns `core.ErrNotFound` for an unknown id, is a no-op when `seg.BloomRef == ref` already (so a second backfill writes nothing), appends `segBloomRec{V: indexRecordVersion, Op: segOpBloom, ID: id, Ref: ref}` through `l.append`, and only then sets `seg.BloomRef = ref`. `BackfillSegmentBlooms` reaches it with `sl, ok := s.Segments().(*segLog)`; when the assertion fails — a test fake standing in for the log — it logs `Warn` once and keeps building files, which still answer through the computed-ref fallback.

`math.MaxInt32` (not `math.MaxInt`) is used for every unbounded `Range` upper bound in this subplan so the value is identical on 32- and 64-bit targets and the replay artifacts stay reproducible.

**Anchored edits in `internal/store/segments.go` (SP-06's segment-log file) and `internal/store/open.go` (its one constructor call site).** Three sites in `segments.go` — the two struct fields, `openSegLog`'s two new parameters (which is also where `recordBloomRef` above lands), and `Close` — plus the single call-site update in `open.go` and the matching one in `segments_test.go`. All of them are listed in the Done checklist's file map, and nothing else in those files changes. Read the shipped `segLog` before editing: it does **not** hold a project root or a `config.Config` today, and its `Close` appends the close record **before** it mutates the in-memory segment — both facts drive the shape below.

**Site 1 — `segLog` gains two fields.** `segLog` currently holds `mu`, `f`, `byID`, `order`, `maxID`, `clk`, `log`, `degraded`, `warnedNoTokens`. Add exactly two, and nothing else:

```go
root string        // the PROJECT root, matching openFS's own `root` (never <root>/.qompack)
cfg  config.Config // needed for runtime.phase7.segmentBloom only
```

**Site 2 — `openSegLog` takes them as parameters.** Its signature becomes

```go
func openSegLog(p, root string, cfg config.Config, clk core.Clock, log logging.Logger) (*segLog, error)
```

and it stores both on the returned `segLog`. There are exactly two call sites in the package: `openFS` in `internal/store/open.go`, which already has `root` and `cfg` in scope and becomes

```go
if s.seg, err = openSegLog(filepath.Join(l.Index, segmentsFile), root, cfg, deps.Clock, deps.Log); err != nil {
```

and one benchmark call in `internal/store/segments_test.go`, which passes `b.TempDir()` and `config.Defaults()`. Both are mechanical argument additions; nothing else in either file changes. Threading through the constructor rather than through `FSStore` is deliberate: the segment log's own methods need the values, and a `segLog` reached through `FSStore.Segments()` has no back-pointer to its store.

**Site 3 — the bloom build and its record inside `Close`.** The order matters and is the reverse of what a naive reading suggests. Today `Close` appends `segCloseRec` **first** (`l.append(segCloseRec{…})`) and only then mutates `seg`, so a `seg.BloomRef` assigned before that append would never reach the file and would be lost on the next reopen. `seg` is also a `*Segment` out of `l.byID`, while `BuildSegmentBloom` takes a `Segment` by value. So: append the close record, mutate `seg` as the shipped code already does, and **then** append a second record — SP-06's designed `segBloomRec` seam — before returning:

```go
seg.EndTurn, seg.EndTS, seg.Tokens, seg.Features, seg.Closed = endTurn, endTS, tokens, clean, true

// SP-16, §6.8: build this segment's bloom now that its span is final, and record the ref
// through SP-06's reserved segBloomRec so a reopen replays it.
if ref, _, bErr := BuildSegmentBloom(ctx, l.root, l.cfg, *seg); bErr == nil && ref != "" {
    if aErr := l.append(segBloomRec{
        V: indexRecordVersion, Op: segOpBloom, ID: id, Ref: ref,
    }); aErr == nil {
        seg.BloomRef = ref
    } else {
        l.log.Warn("store: segment bloom built but its ref was not recorded",
            "segment", int(id), "ref", ref, "err", aErr)
    }
}
return nil
```

`Close` already holds `l.mu` for its whole body, so this runs under the same lock and must call `l.append` directly rather than `recordBloomRef`, which takes the lock itself.

Every error here is swallowed deliberately: a missing segment bloom — or a bloom whose ref was not recorded — degrades to "may contain", never to a failed `Close` (§12.3 "everything else fails toward do nothing"). `BuildSegmentBloom` returns `("", 0, nil)` when `runtime.phase7.segmentBloom.enabled` is false, so a disabled config appends no second record at all and `segments.jsonl` is byte-identical to its pre-SP-16 shape.

**Performance budget.** `BenchmarkBuildSegmentBloom` over a segment containing **2 000 tool-use records** must complete in **< 50 ms** (well inside the 250 ms `buildBudgetMs`). Segment close is on the changepoint/idle path, never on the L0 hot path, so B-A is untouched — `BenchmarkObserveToolHotPath` must show no change (§13 invariant 9). `BenchmarkSegmentsMayContain200` over 200 segments must complete in **< 5 ms**.

---

### 5. `internal/store/ephemeral.go` — durable expansion counts and the project touch distribution

§8.7 requires that "every retrieval result is tagged ephemeral at birth and ... recorded as an ephemeral tool use", and §5.16 restates it: "the observer records the resulting tool-use record with `Ephemeral: true`". That makes the `tool_use` index the durable, restart-surviving record of what was re-expanded — strictly better than the daemon's in-memory counter for checkpoint-time decisions.

Both functions in this file are **index-only readers** and take `projectRoot`, not `Store`. That is deliberate: `internal/checkpoint` and `internal/daemon` both need them, neither can reach this package's unexported concrete store type, and neither has any business opening an object. `internal/store` owns `index/tool_use.jsonl`, so reading it here through the package's existing JSONL line reader is in-bounds.

```go
type Expansion struct{ Root core.Hash; Count int; Last ToolUseRecord }

// EphemeralExpansions tallies retrieval expansions for one session by root hash, most-expanded
// first, ties broken by most-recent Last.TS descending, then by Root.String() ascending so the
// order is total and deterministic.
func EphemeralExpansions(ctx context.Context, projectRoot string, sess core.SessionID) ([]Expansion, error)
```

Implementation: iterate `<projectRoot>/.qompack/index/tool_use.jsonl`; select `rec.Ephemeral && rec.Session == sess && rec.Root != (core.Hash{})`; tally per `Root`; keep the highest-`TS` record as `Last`. Returns an empty slice (never nil-with-error) when the index is absent. Honours `ctx` cancellation, checked every 512 lines.

```go
// ProjectPathTouches is the "project's historical hot-file distribution" of §10 Phase 7 (O4):
// paths.Key(path) → touch count over the most recent maxSessions DISTINCT session ids.
func ProjectPathTouches(ctx context.Context, projectRoot string, maxSessions int) (touches map[string]int, records int, err error)
```

Implementation: one reverse pass is not possible over an append-only JSONL, so it is two cheap forward passes over the same file. Pass 1 collects session ids in first-appearance order; the **last** `maxSessions` of them are the newest (the index is append-only and monotonic in time, §3.3), and become the keep-set. Pass 2 re-reads and, for every record whose `Session` is in the keep-set and whose `Path` is non-empty, increments `touches[paths.Key(rec.Path)]` and `records`. `maxSessions < 1` is coerced to `1`. Returns an empty (non-nil) map and `records == 0` when the index is absent. `ctx` checked every 512 lines; on cancellation returns what has been counted so far with a nil error, because a partial hot-file distribution is a valid warm start and a hard failure is not (§12.3).

---

### 6. `internal/checkpoint/promote.go` — demand-driven promotion (§8.7)

This file exists separately so it cannot collide with SP-15's `internal/checkpoint/grammar.go` in the same wave.

```go
func PromotionInputFromConfig(cfg config.Config, sess core.SessionID, projectRoot string) PromotionInput {
    return PromotionInput{
        Session:          sess,
        ProjectRoot:      projectRoot,
        Threshold:        cfg.Retrieval.PromoteAfterExpansions, // Appendix C default 2
        SliceWeightBoost: cfg.Runtime.Phase7.Promotion.SliceWeightBoost,
        MaxPromoted:      cfg.Runtime.Phase7.Promotion.MaxPromoted,
    }
}

func Promote(ctx context.Context, c *Checkpoint, src SourceSet, in PromotionInput) (PromotionReport, error)
```

`SourceSet` (§5.14) carries no project root — by design, since it may hold nothing that could smuggle live context in — so the root travels on `PromotionInput` and the `Finalize` call site supplies the writer's own. `src` is still taken because step 6's slice-weight path and future tier-2 promotion work read from it; `Promote` must not touch `src.Store` for the expansion counts.

Algorithm:

1. Guard: `in.Threshold < 1` ⇒ `in.Threshold = 1`; initialize `rep.Skipped = map[string]string{}` (it is **always non-nil**); `in.MaxPromoted < 1` ⇒ return `rep` with `Skipped["*"] = "maxPromoted < 1"`; `in.ProjectRoot == ""` ⇒ return `rep` with `Skipped["*"] = "no project root"`.
2. `exps, err := store.EphemeralExpansions(ctx, in.ProjectRoot, in.Session)`; on error return the wrapped error — the caller (`Finalize`) logs and continues.
3. For each `e` in `exps`:
   - `e.Count < in.Threshold` ⇒ `Skipped[e.Root.String()] = fmt.Sprintf("count %d < threshold %d", e.Count, in.Threshold)`, continue.
   - Weight: `w := in.SliceWeightBoost * math.Min(3.0, float64(e.Count)/float64(in.Threshold))`; if `s, ok := in.SliceScores[e.Last.ID]; ok { w *= 0.5 + s }`.
   - `why := fmt.Sprintf("promoted: re-expanded %d× after compaction", e.Count)`.
   - `summary := sanitize(fmt.Sprintf("promoted: re-expanded %d× — %s %s", e.Count, e.Last.Tool, e.Last.ArgsPreview))` (`sanitize` below).
   - Append `Promotion{Hash: e.Root, ToolUseID: e.Last.ID, Path: e.Last.Path, Count: e.Count, Weight: w, Why: why, Summary: summary}`.
4. Sort promotions by `Weight` descending, ties by `Count` descending, ties by `Hash.String()` ascending. Truncate to `in.MaxPromoted`; every dropped one records `Skipped[hash] = "maxPromoted reached"`.
5. For each surviving promotion, in order:
   - If no existing `c.Pointers.Tools` entry has the same `ToolUseID`, append `ToolPointer{ToolUseID: string(p.ToolUseID), Hash: p.Hash.String(), Summary: p.Summary}`; `AddedTools++`. If one exists, **replace its `Summary`** with the promoted summary and do not append (idempotence).
   - If `p.Path != ""` and no existing `c.Pointers.Files` entry has the same `Path`, append `FilePointer{Path: p.Path, Hash: p.Hash.String(), Why: p.Why}`; `AddedFiles++`. If one exists, replace its `Why`.
6. Build `weights map[string]float64`: promoted entries keyed by `"tool:"+ToolUseID` and `"file:"+Path` at their computed weight; every pre-existing pointer gets `1.0`. Call `OrderPointers(&c.Pointers, weights)` (§7 below) so the tier-3 slice is in importance order — the §6.9 serialization-order rule, which is what makes `Truncate` drop the least valuable pointers first.
7. Return the report.

**`sanitize(s string) string`** enforces §13 invariant 5 ("No code snippets in checkpoints"): replaces every `\r` and `\n` with a single space, collapses runs of whitespace, strips backtick fences, and truncates to **160 runes** with a trailing `…`. `TestPromoteNeverAddsCodeSnippets` asserts no promoted string contains a newline or a backtick and that `len([]rune(s)) <= 161`.

**Anchored edit in `internal/checkpoint` (SP-10's writer file).** Inside `Finalize`, after the draft has been materialized into a `Checkpoint` value and **immediately before** `Truncate` is called, insert:

```go
if w.cfg.Runtime.Phase7.Promotion.Enabled {
    in := PromotionInputFromConfig(w.cfg, d.session, w.root)
    if rep, pErr := Promote(ctx, &c, d.src, in); pErr != nil {
        w.log.Warn("promote: skipped", "err", pErr)
    } else if len(rep.Promoted) > 0 {
        w.log.Info("promote: pointers promoted", "n", len(rep.Promoted), "files", rep.AddedFiles, "tools", rep.AddedTools)
    }
}
```

Bind `w.cfg`, `w.log`, `w.root`, `d.src`, `d.session` to the writer's and draft's existing fields. `w.root` is the writer's project root — necessarily present, since `Finalize` writes `<root>/.qompack/checkpoints/NNNN.json` and appends to `MANIFEST.jsonl`. Use SP-10's names; add no fields.

> **Merge-order note.** SP-15 makes a structurally identical one-line insertion into the same `Finalize` body for its grammar fold. Wave-4 merge order is SP-14 → SP-15 → SP-16, so SP-16 resolves the conflict by keeping **both** blocks, with SP-15's grammar fold first and SP-16's promotion second (promotion must run last so it sees the final pointer set). Resolve on this incoming branch and re-merge, per §9 — never with a hand-edited merge commit.

**Performance.** Promotion runs inside `Finalize`, which is bound by budget **B-E: `checkpoint_finalize` p99 < 2 s (§11.3 L4)**. `BenchmarkPromote500` — 500 ephemeral tool-use records across 40 distinct roots — must complete in **< 50 ms**, i.e. ≤ 2.5% of B-E.

---

### 7. `internal/checkpoint/curve.go` — progressive truncation tuning (§6.9)

```go
// ReserveLatePct and ReserveFirstPct are the fractions of the post-tier-1 budget guaranteed to
// tier 2 and tier 3 before either competes for the free remainder. They are NOT hand-picked:
// they are the argmax of the budget-versus-reconstruction-quality grid measured by
// TestTruncationCurve over testdata/sessions/synthetic and committed to
// testdata/phase7/truncation-curve.json. TestTierReserveMatchesCurve fails if these constants
// and that artifact ever disagree. Do not edit by hand — re-run the measurement.
const (
    ReserveLatePct  = 0.35
    ReserveFirstPct = 0.15
)

func TierReserve(tier TierName, avail core.Tokens) core.Tokens
func Tier2DropOrder() []string   // {"open_questions", "decisions", "current_work"}
func Tier3DropOrder() []string   // {"narrative", "pointers.tools", "pointers.files"}
func OrderPointers(p *Pointers, weights map[string]float64)
```

`OrderPointers` performs a stable sort of `p.Files` (key `"file:"+Path`) and `p.Tools` (key `"tool:"+ToolUseID`) by weight descending, missing keys treated as `1.0`, ties broken by the existing index so the sort is stable and deterministic.

**Anchored edits inside `Truncate`** (`internal/checkpoint`, SP-10's file). `Truncate`'s signature stays exactly as §5.14 declares it. Its body becomes the following allocation algorithm; keep SP-10's existing per-field token estimation and `DropEntry` construction, and replace only the allocation and ordering logic.

```
t1     = estimated tokens of every field named in t.Never   (invariants, user_intent, eliminated)
avail  = max(0, budget - t1)                                 // tier 1 is NEVER truncated (§8.5)
cap2   = TierReserve(TierLate,  avail)                       // floor(avail * ReserveLatePct)
cap3   = TierReserve(TierFirst, avail)                       // floor(avail * ReserveFirstPct)
free   = avail - cap2 - cap3                                 // >= 0 by construction (0.35+0.15 < 1)

// Phase A — spend each tier's reserve, keeping fields in REVERSE drop order.
spend tier 2 into cap2, keeping fields in reverse Tier2DropOrder():
      current_work, then decisions (highest Turn first), then open_questions (index order)
spend tier 3 into cap3, keeping entries in reverse Tier3DropOrder():
      pointers.files (already weight-ordered), pointers.tools, then narrative

// Phase B — tier 2 gets first claim on the free remainder (§8.5 "truncate late").
free = spend remaining tier-2 content into free
free = spend remaining tier-3 content into free

// Phase C — everything unspent becomes a DropEntry.
```

`DropEntry.Kind` values, one per omitted unit: `"open_question"`, `"decision"`, `"current_work"`, `"pointer_file"`, `"pointer_tool"`, `"narrative"`. `DropEntry.ID` is the open question's index, the `DecisionID`, `"current_work"`, the file path, the tool_use id, or `"narrative"`. `DropEntry.Detail` is `fmt.Sprintf("truncated at budget %d (tier %s)", int(budget), tier)`.

**Edge cases.** `budget <= t1`: tier 1 is emitted in full anyway (§8.5 "never truncated") and tiers 2 and 3 are dropped entirely with a `DropEntry` each. `avail == 0`: identical. A single tier-2 field larger than `cap2 + free`: it is dropped whole (no partial fields — a half decision is worse than none), and the freed budget flows to tier 3 in Phase B.

**Monotonicity.** `TestTruncateIsMonotone` (property test, `rapid`) asserts that for budgets `b1 < b2` the kept set at `b1` is a subset of the kept set at `b2` — the defining property of an embedded code (§6.9 "truncation *at any point* yields the best available reconstruction for that budget").

**The measurement that justifies the constants.** `TestTruncationCurve` in `test/replay`:

1. `h.Load("testdata/sessions/synthetic")` → 24 sessions.
2. For each session with a compaction point, build the checkpoint from the store, then for each budget `b ∈ {2000, 3000, 4000, 6000, 8000, 10000, 12000, 16000, 20000}` and each `(late, first)` in the grid `late ∈ {0.20, 0.25, 0.30, 0.35, 0.40, 0.45, 0.50}` × `first ∈ {0.05, 0.10, 0.15, 0.20, 0.25}`, call `Truncate`, rehydrate from the truncated checkpoint with `rehydrate.Build`, replay deterministically, and record `Score.FractionOfOPT` and `Divergence.FirstDivergenceTurn`.
3. Emit `testdata/phase7/truncation-curve.json`:
   ```json
   {"version":1,"corpus":"testdata/sessions/synthetic","sessions":24,
    "budgets":[2000,3000,4000,6000,8000,10000,12000,16000,20000],
    "grid":[{"late":0.20,"first":0.05,"byBudget":{"12000":{"fractionOfOPT":0.0,"firstDivergenceTurn":0.0}}}],
    "argmaxAtDefaultBudget":{"budget":12000,"late":0.35,"first":0.15}}
   ```
4. Deterministic (`ReplayOptions{Deterministic: true, Seed: 16}`), so re-running reproduces the artifact byte-for-byte. `QOMPACK_UPDATE_GOLDEN=1` rewrites it; otherwise the test compares and fails on any difference.

**Cost control for the grid.** 24 sessions × 9 budgets × 35 grid points is 7 560 `Truncate`+rehydrate+deterministic-replay evaluations. Two rules keep that inside CI: the checkpoint for a session is built **once** and reused across all 315 `(budget, late, first)` combinations (only `Truncate` and `rehydrate.Build` re-run), and the test calls `t.Skip` under `testing.Short()`. Budget: **< 120 s** on the CI Linux runner; the `replay-gate` job runs it without `-short`, and `devtool test` runs the package with `-short` so the ordinary local loop stays fast. If the measured wall time exceeds 120 s, halve the budget list to `{4000, 8000, 12000, 20000}` and re-record the artifact — do not silently let the gate slow down.

---

### 8. `internal/daemon/phase7.go` — O4 cross-session warm start

The daemon is a composition root and is the only place allowed to see `store`, `sketch`, `negknow`, and `scheduler` together (§3.2). This file is owned entirely by SP-16.

**On-disk state.** Two files, both new and owned here:

- `.qompack/sketches/touch.project.cms` — the project's cumulative file-touch Count-Min, sized from `sketches.cms.epsilon` / `.delta` (Appendix A: `width = ⌈e/ε⌉`, `depth = ⌈ln(1/δ)⌉`; at ε=0.001, δ=0.01 that is 2718 × 5 ≈ 54 KB at 4-byte counters). Written with `sketch.Save` (atomic).
- `.qompack/state/warmstart.json` — `{"version":1,"lastSession":"…","lastRunMs":1699999999999,"bocdSeededFor":"…","report":{…}}`, written with `paths.WriteAtomic`. Its jobs are the once-per-session guard, the `bocdSeededFor` resume guard, and `/qompack:status` reporting.

```go
type WarmStarter struct {
    o       Options
    once    map[core.SessionID]bool
    seeded  map[core.SessionID]bool
    current core.SessionID
    mu      sync.Mutex
    last    WarmStartReport
}
func NewWarmStarter(o Options) *WarmStarter
func (w *WarmStarter) SetSession(sess core.SessionID)
func (w *WarmStarter) SeedPriors(ctx context.Context, sess core.SessionID) error
func (w *WarmStarter) MaybeRun(ctx context.Context, sess core.SessionID) WarmStartReport
func (w *WarmStarter) LastReport() WarmStartReport
```

`MaybeRun` returns immediately with `Ran:false, Reason:"already ran"` if `sess` is already in `once`; otherwise marks it and runs the three steps below under a `context.WithTimeout` of `runtime.phase7.warmStart.budgetMs` (default 3000 ms). **Every step is independently error-tolerant:** a failure in one records a `Reason` fragment, logs `Warn`, and the next step still runs. Warm start can never fail a session.

`SeedPriors(ctx, sess)` runs **step 3 alone**, synchronously, under `min(budgetMs, 1000) ms`, and marks `seeded[sess]`. `MaybeRun`'s step 3 is then a no-op for that session. This split exists purely to remove a race: step 3's only consumer is the per-session `scheduler.Runtime`, which reads `state/bocd.json` at construction, so that one step must complete *before* the Runtime is built, while steps 1 and 2 have no such ordering constraint and stay on the background goroutine.

**Step 1 — Count-Min warm start (O4).** Gated by the Appendix C key `sketches.cms.warmStartFromProject`; when `false`, sets `Reason += "cms disabled;"` and skips.

```
proj := new CMS(cfg.Sketches.CMS.Epsilon, cfg.Sketches.CMS.Delta)
p := filepath.Join(w.o.ProjectRoot, ".qompack/sketches/touch.project.cms")
if err := sketch.LoadWithLog(p, proj, w.o.Log); err != nil {
      // LoadWithLog, NEVER Load: test/guards/sketchload_test.go's
      // TestGuard_NoSilentSketchLoadOutsideItsPackage fails the build on sketch.Load in any
      // non-test file outside internal/sketch, because Load runs on a logging.Nop and so
      // writes no durable line anywhere. LoadWithLog emits the one Loud line itself.
      // Classify with errors.Is(err, fs.ErrNotExist) — a cold start, Debug — NOT with
      // core.ErrNotFound, which LoadWithLog also returns for corrupt, truncated and
      // oversize files (internal/sketch/io.go, absentSketch).
      proj = bootstrapProjectCMS(ctx)            // see below
}
proj.Scale(cfg.Runtime.Phase7.WarmStart.Decay)   // default 0.6 — exponential decay of history
if err := live.MergeFrom(proj); err != nil {     // shape mismatch (epsilon/delta changed)
      proj = bootstrapProjectCMS(ctx)            // rebuild at the CURRENT shape
      proj.Scale(decay)
      _ = live.MergeFrom(proj)                   // shapes now match by construction
      w.o.Log.Loud("warm start: project CMS reshaped", "err", err)
}
sketch.Save(p, proj)
report.CMSMerged = true
```

`bootstrapProjectCMS(ctx)` derives the project's historical hot-file distribution from the index through the store's exported reader — the daemon never opens `.qompack/index/*` itself:

```go
func (w *WarmStarter) bootstrapProjectCMS(ctx context.Context) (*sketch.CMS, int) {
    cms := sketch.NewCMS(w.o.Cfg.Sketches.CMS.Epsilon, w.o.Cfg.Sketches.CMS.Delta)
    touches, records, err := store.ProjectPathTouches(ctx, w.o.ProjectRoot,
        w.o.Cfg.Runtime.Phase7.WarmStart.MaxSessions)
    if err != nil {
        w.o.Log.Warn("warm start: touch bootstrap failed", "err", err)
        return cms, 0
    }
    for k, n := range touches {          // keys are already paths.Key form
        cms.Add([]byte(k), uint32(n))
    }
    return cms, records
}
```

`records` becomes `report.CMSKeysBootstrapped`. This is the literal reading of "the project's historical hot-file distribution" and is also the recovery path for a corrupt or reshaped sketch.

`live` is the session Count-Min held in `o.Sketches` — `daemon.SketchSet` is declared by SP-05 **in this same package**, so `o.Sketches.CMS` is directly addressable. If SP-05 named the field for its file rather than its type (`Touch`, after `sketches/touch.cms`), use that name: it is the only Count-Min in the set, so the binding is unambiguous. Do not rename it and do not add an accessor.

**Step 2 — `scope:"project"` elimination carry-forward.** Gated by `runtime.phase7.warmStart.enabled`.

```
flipped, err := o.Ledger.RefreshStaleness(ctx, o.Store)   // §8.3 step 3, run BEFORE anything reads the ledger
report.EliminationsFlipped = len(flipped)
if len(flipped) > 0 && cfg.Eliminations.RebuildOnStale != "never" {
      _, health, _ := o.Ledger.RebuildBloom(ctx)          // active records only (§8.3, §13 invariant 3)
      if health.NeedsResize { log.Loud("bloom saturation: resize needed", "fill", health.FillRatio, "fp", health.EstFPRate) }
}
active, _ := o.Ledger.Active(ctx, negknow.Scope("project"))
report.EliminationsCarried = len(active)
```

This is the entire carry-forward: SP-09 already persists project-scoped records across sessions and already implements staleness; SP-16 owns only the guarantee that **staleness is re-evaluated at the start of a fresh session, before the first `already_tried` call or the first checkpoint reads `eliminated[]`**. Without it, a dependency that changed between sessions would not be noticed until the first mid-session idle window — exactly the §12 "Stale negative knowledge blocks a now-viable approach" High-severity row. `health.NeedsResize` surfacing satisfies §11.4's "Monitor fill ratio and resize" watch-for.

**Step 3 — BOCD feature-prior seeding.** Gated by `runtime.phase7.warmStart.enabled`.

This step is the body of `SeedPriors(ctx, sess)` (and, when it has not already run for `sess`, of `MaybeRun`'s step 3):

```
if w.seeded[sess] || warmstartJSON.BOCDSeededFor == string(sess) {
      report.Reason += "bocd already initialised;"; return nil   // never reset a live posterior
}
segs, err := o.Store.Segments().Range(ctx, 0, core.TurnIndex(math.MaxInt32))
if err != nil { log.Warn("warm start: segment range failed", "err", err); return err }  // defaults stand
featureSets := []map[string]float64{}
for _, s := range segs (iterated newest-first by EndTS, s.Closed && s.Session != sess,
                        stopping at maxSessions*64 entries):
      if len(s.Features) > 0 { featureSets = append(featureSets, s.Features) }
report.SegmentsSampled = len(featureSets)
pri := scheduler.SeedPriorsFromSegments(featureSets, cfg.Runtime.Phase7.WarmStart.BOCDPriorWeight,
                                        cfg.Scheduler.Changepoint.Features)
det := scheduler.NewBOCDWithPriors(cfg.Scheduler.Changepoint.HazardRate, cfg.Scheduler.Changepoint.Features, pri)
scheduler.SetPriorWeight(det, cfg.Runtime.Phase7.WarmStart.BOCDPriorWeight)
blob, err := det.MarshalBinary()
if err == nil { err = paths.WriteAtomic(filepath.Join(o.ProjectRoot, ".qompack", "state", "bocd.json"), blob) }
if err != nil { log.Warn("warm start: bocd seed not persisted", "err", err) }
w.seeded[sess] = true; persist bocdSeededFor = sess into state/warmstart.json
report.BOCDPriorFeatures = sortedKeys(pri)
```

`scheduler.Runtime` (§5.13) has no detector setter and SP-16 may not add one (Rule W-3). Instead the seeded detector is **persisted, not injected**: `MarshalBinary` it and write it to `.qompack/state/bocd.json` (the path §3.3 already assigns to the daemon's BOCD state) via `paths.WriteAtomic`, and SP-12's Runtime picks it up through `scheduler.NewDetectorFromState` (the one-line anchored edit in §3 above). Two guarantees make that work rather than merely hope:

1. **Ordering.** The daemon calls `w.SeedPriors(ctx, sess)` **synchronously**, immediately before it constructs that session's `scheduler.Runtime`. Steps 1 and 2 stay asynchronous because nothing reads their output at a fixed moment.
2. **Discrimination.** `NewDetectorFromState` distinguishes a seeded wire record from a plain-BOCD record by the `Kind` discriminator, so neither format is ever misread as the other, and an unreadable blob falls back to `NewBOCD` rather than to a crash.

**Registration.**

```go
func RegisterPhase7(o Options, idle IdleController) *WarmStarter {
    ws := NewWarmStarter(o)
    idle.Register("warm_start", 5, func(ctx context.Context) error {
        if sess := ws.session(); sess != "" {
            ws.MaybeRun(ctx, sess)
        }
        return nil
    })
    idle.Register("segment_blooms", 40, func(ctx context.Context) error {
        _, err := store.BackfillSegmentBlooms(ctx, o.Store, o.ProjectRoot, o.Cfg, 500*time.Millisecond)
        return err
    })
    return ws
}
```

**Priorities follow SP-05's shipped `IdleController.Register(name, prio, fn)` ordering, and that ordering is LOWER FIRST.** `internal/daemon/idle.go` says so twice — "Register adds work to run when idle. Lower prio runs first." — and the tasks already registered are `idlePrioDrain = 10`, `idlePrioSketches = 20`, `idlePrioMetrics = 30` (`internal/daemon/daemon.go`). Hence the two numbers above:

- **`warm_start` at 5** — below `idlePrioDrain`, so it wins the very first idle window. That is the whole point of the O4 deliverable: a fresh session's first compaction must benefit from history, and a warm start that ran last would routinely be preceded by the compaction it was supposed to inform. (A value of 100 would run it **last of everything** under these semantics — the exact inversion of the intent.)
- **`segment_blooms` at 40** — after `metrics` (30) and deliberately **not equal** to it. Equal priorities leave relative order unspecified, and the backfill sweep is the one task here that can consume its whole 500 ms budget, so it goes behind every existing task rather than beside one.

`ws.session()` is the unexported mutex-guarded read of the field `SetSession` writes; it returns `""` before any session has registered, and the task is then a no-op. `daemon.Options` carries no session accessor and SP-16 does not add one.

**The edits to `internal/daemon` (SP-05's file), all confined to construction and session registration.** Four insertions and one struct field:

```go
// 1. on the daemon struct
warm *WarmStarter

// 2. in New(o Options), immediately after the IdleController is constructed
d.warm = RegisterPhase7(o, d.Idle())

// 3. in the session-registration path, IMMEDIATELY BEFORE that session's
//    scheduler.Runtime is constructed
d.warm.SetSession(sess)
_ = d.warm.SeedPriors(ctx, sess)          // synchronous, ≤ 1 s, error already logged inside

// 4. immediately AFTER the Runtime is constructed
go d.warm.MaybeRun(context.WithoutCancel(ctx), sess)   // steps 1 and 2, budgetMs deadline inside
```

Insertion 4 means warm start does not wait for the first idle tick (default `idle.detectAfterSeconds: 120`); the `once` map makes the goroutine and the idle task idempotent. Warm start **never blocks** `session-start`: insertion 3 is bounded at 1 s and runs inside the daemon, not the hook, and the hook's `SessionStart` response does not depend on any of it.

**Performance budget.** `BenchmarkWarmStart10Sessions` — 10 prior sessions × 2 000 tool-use records, 200 closed segments, 400 elimination records — must complete in **< 3 s** (the `budgetMs` default). Warm start is off both the L0 hot path (B-A) and the checkpoint path (B-E); `BenchmarkObserveToolHotPath` must be unchanged, asserted by `bench-gate`.

---

### 9. Error-handling matrix (every failure mode this subplan introduces)

| Failure | Response |
|---|---|
| `touch.project.cms` missing | `sketch.LoadWithLog` returns an error unwrapping to `fs.ErrNotExist` **as well as** `core.ErrNotFound` — classify on `fs.ErrNotExist`, or a corrupt file reads as a cold start; bootstrap from `index/tool_use.jsonl`; `Info`, never `Loud`; warm start proceeds |
| `touch.project.cms` CRC/version bad | `sketch.LoadWithLog` returns an error satisfying `core.ErrNotFound` and wrapping the decoder's own sentinel — `sketch.ErrCorrupt` for a CRC32C mismatch, `sketch.ErrUnsupportedVersion` for a version bump — and writes the one `Loud` line itself ("sketch corrupt — rebuilding from records"); bootstrap; overwrite on save. `sketch.Load` would write no line anywhere, which is why the guard forbids it |
| `CMS.MergeFrom` shape mismatch (epsilon/delta changed) | rebuild at the current shape from the index, merge, `Loud` |
| `Ledger.RefreshStaleness` error | log `Warn`, skip `RebuildBloom`, continue to step 3; `already_tried` keeps last-known state (never a false positive) |
| `Ledger.RebuildBloom` error | log `Loud`; per §12.3 `already_tried` returns `absent` for everything rather than a false positive |
| `ProjectPathTouches` index missing | empty map, `records == 0`, nil error; `MergeFrom` of an empty CMS is a no-op; `Reason += "no history;"` |
| `ProjectPathTouches` ctx cancelled mid-scan | returns the partial distribution with a nil error — a partial hot-file prior is valid, a hard failure is not (§12.3) |
| Segment `Range` error during prior seeding | skip step 3, `DefaultFeaturePriors` remain in force |
| `state/bocd.json` write error | log `Warn`; the scheduler starts from `NewBOCD` defaults — no worse than pre-Phase-7 |
| `state/bocd.json` holds a plain-BOCD blob (pre-Phase-7 state) | `NewDetectorFromState` restores it as a plain BOCD; priors apply from the next session |
| `state/bocd.json` corrupt / unknown `kind` or `v` | `NewDetectorFromState` falls back to a fresh `NewBOCD` — never a panic, never a partial restore |
| warm start exceeds `budgetMs` | context deadline cancels the in-flight step; partial report persisted with `Reason += "deadline;"` |
| `BuildSegmentBloom` deadline | no file written (a partial bloom would produce false negatives); `Warn`; `SegmentMayContain` returns `true` conservatively |
| segment bloom file exists | treated as already built; `keys = -1`; not an error |
| `segBloomRec` append fails after the bloom file was written | `Warn` naming the segment and the ref; `Close` still returns nil and `seg.BloomRef` stays `""`. The read path's computed-ref fallback still finds the file in this process; the next `BackfillSegmentBlooms` sweep re-records it |
| segment bloom CRC bad | `SegmentMayContain` returns `true`; `Loud` once per segment id per process |
| `EphemeralExpansions` index missing | empty slice, nil error; `Promote` returns an empty report |
| `Promote` error inside `Finalize` | `Warn`; `Finalize` continues and produces a checkpoint without promotions |
| `budget <= tier1Tokens` in `Truncate` | tier 1 emitted in full (§8.5 never truncated); tiers 2 and 3 fully dropped with `DropEntry`s |
| `r <= 0`, `w <= 0` or NaN in ski rental | `SkiRentalThreshold` returns `+Inf`; `SkiRentalShouldWrite` returns `false`; `Breakdown` carries `ski_rental_unamortizable=1` instead of a non-finite `ski_rental_threshold` (`encoding/json` refuses `+Inf`); the scheduler prefers the shallowest cut and never defers past the hard ceiling |

---

## Test plan (TDD)

Tests are written **before** the implementation in each commit and must fail first. `require` only (`assert` is banned, §6.1). Every test touching time takes `testutil.FakeClock`. Property tests use `pgregory.net/rapid`.

### Fixtures to create

| Fixture | Content |
|---|---|
| `testdata/phase7/truncation-curve.json` | generated by `TestTruncationCurve` (commit 6) |
| `testdata/phase7/warmstart-delta.json` | generated by `TestPhase7WarmStartDelta` (commit 7) |
| `internal/store/segbloom_test.go` helper `seedSegment(t, s, sess, from, to, n)` | inserts `n` `ToolUseRecord`s across turns `[from,to]` with paths `fixt/f%03d.go`, tools cycling `FileRead/Bash/Grep`, distinct roots |
| `internal/daemon/phase7_test.go` helper `priorSessions(t, p *testutil.Project, n int)` | writes `n` prior sessions of 200 tool uses and 20 closed segments each, with `Features` drawn deterministically from `rand.New(rand.NewSource(16))` |
| `internal/checkpoint/promote_test.go` helper `ephemeralRuns(t, s, sess, spec map[string]int)` | records ephemeral tool uses per root label |
| existing `testdata/sessions/synthetic/*.json` (SP-02, 24 sessions) | reused unchanged by both replay tests |

### Config (commit 1)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestPhase7DefaultsMatchArchitecture` | none | `config.Defaults()` | `Runtime.Phase7` deep-equals `{WarmStart:{true,10,0.6,0.35,3000}, Promotion:{true,1.75,12}, SegmentBloom:{true,2048,0.01,250}}` |
| `TestAppendixCGoldenStillPasses` | none | SP-01's Appendix C golden test | still green — no Appendix C key changed |
| `TestPhase7ValidationRejectsOutOfRange` | table of 10 rows: one per validation rule plus an all-valid row | `decay=1.5` | one `Violation{Key:"runtime.phase7.warmStart.decay", Got:1.5, Want:0.6}` |
| ″ | ″ | `bocdPriorWeight=-0.1` | `Violation{…, Want:0.35}` |
| ″ | ″ | `budgetMs=40` | `Violation{Key:"runtime.phase7.warmStart.budgetMs", Got:40, Want:3000}` |
| ″ | ″ | `sliceWeightBoost=0.5` | `Violation{…, Want:1.75}` |
| ″ | ″ | `maxPromoted=0` | `Violation{…, Want:12}` |
| ″ | ″ | `capacityPerSegment=10` | `Violation{…, Want:2048}` |
| ″ | ″ | `fpRate=0.5` | `Violation{…, Want:0.01}` |
| ″ | ″ | `buildBudgetMs=1` | `Violation{…, Want:250}` |
| ″ | ″ | `maxSessions=500` | `Violation{…, Want:10}` |
| ″ | ″ | all-valid config | zero violations |
| `TestPhase7InvalidConfigFallsBackNotCrashes` | project `.qompack/config.json` with `runtime.phase7.warmStart.decay = 9` | `config.Load` | no error; `Decay == 0.6`; one `Warning`/`Violation` recorded; §11.3 behaviour |
| `TestConfigDocsNotStale` | — | `devtool gen-config-docs && git diff --exit-code` | clean |

### Ski rental (commit 2)

The eight `TestSkiRentalShouldWrite` rows below are marked **(pre-existing — green on the first run
that compiles)**: `SkiRentalShouldWrite` already ships in SP-12's `internal/scheduler/skirental.go`,
so those rows are regression coverage for commit 2's re-expression, not red-first TDD. Every other
row in this table is red-first and must fail before its implementation exists.

| Test | Input | Expected |
|---|---|---|
| `TestSkiRentalThresholdIsComputed` | `(0.1, 1.25)` | `12.5` exactly (`require.InDelta(12.5, got, 1e-12)`) |
| ″ | `(0.05, 2.0)` | `40` |
| ″ | `(0, 1.25)` | `+Inf` |
| ″ | `(0.1, 0)` | `+Inf` — SP-12's `w <= 0` guard lives here now; a finite `0` threshold here would make every positive `expectedReads` amortize |
| `TestNoLiteral12Point5InSource` | walk `internal/**/*.go`, `cmd/**/*.go` skipping `_test.go` and `internal/config/defaults.go`; parse each file with `go/parser` (comments discarded) and inspect every `*ast.BasicLit` | no basic literal whose value parses to `12.5` — comments explaining the threshold are allowed and expected, only compiled literals are forbidden (§11.6) |
| `TestSkiRentalShouldWrite` **(pre-existing — green on the first run that compiles)** | `(12.4, 0.1, 1.25)` | `false` |
| ″ | `(12.5, 0.1, 1.25)` | `false` (strictly greater) |
| ″ | `(12.6, 0.1, 1.25)` | `true` |
| ″ | `(100, 0.05, 2.0)` | `true` (threshold 40) |
| ″ | `(39.9, 0.05, 2.0)` | `false` |
| ″ | `(5, 0.1, 0)` | `false` — SP-12's declared `w <= 0` guard. Without this row no `TestSkiRentalShouldWrite` case catches a revert: drop `w <= 0` from `SkiRentalThreshold` and the other seven rows all stay green, leaving the `(0.1, 0)` threshold row above as the only witness |
| ″ | `(1, 0, 1.25)` | `false` — the `r <= 0` guard, pinned in this table as well as in `TestSkiRental_ComputedNotLiteral` |
| ″ | `(math.NaN(), 0.1, 1.25)` | `false` — the one row the re-expression extends: SP-12's body returns `false` here through IEEE comparison, `SkiRentalThreshold` makes it explicit |
| `TestEstimateRemainingReads` | `EffectiveWindow=180000, HardCeilingMargin=20000, ContextTokens=100000, FrontierTurn=100` | `60.6 ± 0.01` |
| ″ | `ContextTokens=170000` (above hard ceiling) | `0` |
| ″ | `FrontierTurn=0`, no candidates | `0` |
| `TestEvaluateDefersWhenNotAmortizable` | reasons `{soft_floor, young_daly}`, `TTL="warm"`, `ExpectedRemainingReads=5`, `r=0.1,w=1.25` | `ShouldCompact==false`; `Reasons` contains `"ski_rental_defer"`; `Urgency==UrgencyAdvisory`; `Breakdown["ski_rental_threshold"]==12.5` |
| `TestEvaluateNeverDefersOnHardCeiling` | same but reasons include `hard_ceiling` | `ShouldCompact==true`; no `"ski_rental_defer"` |
| `TestEvaluateNeverDefersAtChangepoint` | reasons include `changepoint` | `ShouldCompact==true`; no defer |
| `TestEvaluatePrefersShallowCutWhenNotAmortizable` | candidates `(Pos 10000,score 100)`, `(120000, 98)`, `(150000, 97)`; `ExpectedRemainingReads=3`; TTL warm | `d.P.Pos==150000`; `Reasons` contains `"ski_rental_shallow"` |
| `TestEvaluateKeepsDeepCutWhenAmortizable` | same candidates, `ExpectedRemainingReads=200` | `d.P.Pos==10000`; no ski-rental reason appended |
| `TestEvaluateColdCacheIgnoresSkiRental` | `TTL="cold"`, `ExpectedRemainingReads=1` | `d.P` unchanged; no defer; breakdown keys still present |
| `TestEvaluateWithZeroReadMultiplier` | `r=0` (and a second case with `w=0`), TTL warm | `Breakdown` has **no** `ski_rental_threshold` key and `Breakdown["ski_rental_unamortizable"]==1`; `json.Marshal(d)` returns a nil error — `encoding/json` refuses `+Inf`, and SP-12 round-trips `Decision` through `testdata/golden/scheduler/decision-*.json` |
| `TestEvaluateIsStillPure` (rapid, 500 cases) | random `Inputs` | `Evaluate(in)` called twice deep-equals; no file created under `t.TempDir()` |

### BOCD priors (commit 2)

| Test | Setup | Expected |
|---|---|---|
| `TestDefaultFeaturePriors` | `["paths","tools","time","todos"]` | 4 entries; `paths` = `{0.5,1,1,0.0625}`; `time` = `{30,1,1,3600}` |
| `TestSeedPriorsFromSegments` | 12 feature maps, `paths` values `{0.75,0.80,0.85}` repeated 4×, weight 0.35 | `Mu0 == 0.8 ± 1e-9`; `Kappa0 == 4.2`; `Alpha0 == 3.1`; `Beta0 == 3.1*varUnbiased ± 1e-9` |
| `TestSeedPriorsTooFewSegments` | 1 feature map | equals `DefaultFeaturePriors` |
| `TestSeedPriorsConstantFeature` | 12 maps all `paths: 0.4` | `paths` keeps the default prior |
| `TestSeedPriorsIgnoresNaN` | 12 maps, 3 with `NaN` | uses the 9 finite samples |
| `TestSeededDetectorStandardizes` | priors `paths={0.8,·,1,0.0025}` (σ=0.05), defaults σ_d=0.25, weight 1 | feeding `PathJaccard=0.8` reaches the inner detector as `0.5`; feeding `0.9` as `1.0` (assert via a recording fake `Detector`) |
| `TestSeededDetectorWeightBlends` | weight 0.5, same priors | feeding `0.9` reaches inner as `0.5*0.9 + 0.5*1.0 = 0.95` |
| `TestSeededDetectorRoundTrip` (rapid, 200 sequences) | seed, `Observe` 40 random `Features`, marshal, unmarshal into a fresh `seeded` | `State()` deep-equals; further `Observe` sequences produce identical `ChangepointState` |
| `TestSeededDetectorRejectsUnknownVersion` | `{"kind":"qompack.scheduler.seeded","v":2}` | error wrapping `core.ErrNotFound` |
| `TestSetPriorWeight` | plain `NewBOCD` detector | returns `false`, no panic |
| `TestNewDetectorFromStateDisambiguates` | (a) seeded blob, (b) plain-BOCD blob, (c) `nil`, (d) 8 random bytes | (a) `*seeded` with priors intact; (b) plain BOCD with its posterior restored; (c) and (d) a fresh `NewBOCD` — never a panic, never a cross-format restore |

### Segment blooms (commit 3)

| Test | Setup | Expected |
|---|---|---|
| `TestBuildSegmentBloomSizesPerAppendixA` | 400 records (≈ 810 distinct keys, under the 2048 floor), capacity 2048, fp 0.01 | bit length ∈ [19600, 19700]; `k == 7` (Appendix A: m ≈ 19630, k = 6.64 → 7) |
| `TestSegmentBloomSizesUpForLargeSegments` | 1500 records ⇒ ≈ 3100 distinct keys, capacity floor 2048 | bloom sized at `n == len(distinctKeys)`, not 2048; `m == round(-n·ln(0.01)/(ln2)²)` within ±1 bit; the returned `keys` equals `n` |
| `TestSegmentKeyIsDomainSeparated` | `SegmentKey(SegKeyPath,"a")` vs `SegmentKey(SegKeyTool,"a")` | differ; both 32 bytes; stable across runs (golden hex) |
| `TestSegmentMayContainNoFalseNegatives` | segment with 1500 records | every inserted path/tool/root/tooluse key ⇒ `true` |
| `TestSegmentBloomFalsePositiveRateUnderBudget` | same 1500-record segment (sized up per the rule above); 10 000 absent keys | ≤ 150 positives — 1.5× the 1% design rate. This is the test that would have failed under a fixed capacity of 2048: ≈ 3100 keys in a 2048-sized filter measures ≈ 6%, past §11.4's warning band |
| `TestSegmentsMayContainNarrowsWithoutExpanding` | 20 segments, key present only in segment 7; counting fake `Store` | result `[7]` (allow ≤1 extra FP); `Open`/`OpenSpan`/`GetChunk`/`GetRoot` call counts all `0` |
| `TestCloseSetsBloomRef` | open a segment, add 50 records, `Close` | `Get(id).BloomRef == "sketches/segments/000001.bloom"`; file exists; mode `0444`; `index/segments.jsonl` gained exactly one `"op":"bloom"` line, carrying that ref, appended **after** the `"op":"close"` line |
| `TestCloseBloomRefSurvivesReopen` | same setup, then close the store and reopen it | `Get(id).BloomRef` is the same ref after the reopen — SP-06's `segOpBloom` replay is what makes the ref durable, and this is the test that would have caught assigning `BloomRef` without appending the record |
| `TestCloseAppendsNoBloomRecordWhenDisabled` | `segmentBloom.enabled=false`, close a segment | `index/segments.jsonl` contains zero `"op":"bloom"` lines and is byte-identical to its pre-SP-16 shape; `BloomRef == ""` |
| `TestSegmentBloomDisabledByConfig` | `segmentBloom.enabled=false` | `BloomRef == ""`; no file; `SegmentMayContain` returns `true` |
| `TestSegmentBloomDeadlineWritesNothing` | 5000 records; the test constructs `config.Config` **in memory** and sets `Runtime.Phase7.SegmentBloom.BuildBudgetMs = 0` directly — it must NOT go through `config.Load`, whose per-leaf fallback (§11.3) would coerce 0 to 250 and defeat the test | `ref == ""`, `keys == 0`, `err == nil`; no file; `SegmentsMayContain` returns every segment in range |
| `TestSegmentBloomIsCreateNewNotTruncate` | build, capture bytes, build again | second call returns `keys == -1`; file bytes byte-identical |
| `TestBackfillSegmentBloomsIsIdempotent` | 5 closed segments with empty `BloomRef` | first run `built == 5`, second `built == 0`; `index/segments.jsonl` gained exactly 5 `"op":"bloom"` lines across both runs, and every segment reports its ref after a reopen |
| `TestBackfillHonoursDeadline` | 50 segments, `deadline = 1ms` | `built < 50`; no error; re-running completes the rest |
| `TestCorruptSegmentBloomIsConservative` | truncate a bloom file to 8 bytes | `SegmentMayContain` returns `(true, err)` with a non-nil `err`; `SegmentsMayContain` still lists the segment and emits exactly one `Loud` entry for it, and a second call emits none (per-segment-id de-duplication) |
| `TestSegmentBloomDoesNotTouchTriedBloom` | build 5 blooms | `sketches/tried.bloom` mtime and bytes unchanged; `p.AssertAppendOnly(t)` passes |

### Ephemeral expansions and project touches — `internal/store/ephemeral_test.go` (commit 3)

| Test | Setup | Expected |
|---|---|---|
| `TestEphemeralExpansionsCountsByRoot` | 3 ephemeral records root A, 2 root B, 2 non-ephemeral root C, 1 ephemeral for another session | `[{A,3},{B,2}]` in that order |
| `TestEphemeralExpansionsDeterministicTies` | 2 records root A, 2 root B, equal TS | ordered by `Hash.String()` ascending |
| `TestEphemeralExpansionsEmptyIndex` | fresh store | empty slice, nil error |
| `TestProjectPathTouchesCountsNewestSessions` | index with 5 sessions × 20 path records, `maxSessions = 3` | only the 3 newest session ids contribute; `records == 60`; keys are `paths.Key` form |
| `TestProjectPathTouchesEmptyIndex` | fresh store | empty non-nil map, `records == 0`, nil error |
| `TestProjectPathTouchesHonoursCancellation` | 5 000 records, ctx cancelled after the first 512-line check | partial map, nil error, no panic |

### Promotion — `internal/checkpoint/promote_test.go` (commit 5)

| Test | Setup | Expected |
|---|---|---|
| `TestPromoteAddsPointersAboveThreshold` | threshold 2; A count 3, B count 2, C count 1 | `Promoted` = A,B; `Pointers.Tools` gains 2; `Skipped[C] == "count 1 < threshold 2"` |
| `TestPromoteWeightFormula` | boost 1.75, threshold 2, A count 3 | `Weight == 1.75 * min(3, 1.5) == 2.625` |
| `TestPromoteWeightCappedAtThreeX` | count 20, threshold 2 | `Weight == 1.75 * 3 == 5.25` |
| `TestPromoteAppliesSliceScore` | `SliceScores[A]=0.5` | `Weight == 2.625 * 1.0` |
| `TestPromoteRespectsMaxPromoted` | 20 eligible, max 12 | 12 promoted (highest counts); 8 in `Skipped` with `"maxPromoted reached"` |
| `TestPromoteIsIdempotent` | run `Promote` twice on the same checkpoint | pointer counts identical after both runs; no duplicates |
| `TestPromoteUpdatesExistingPointer` | pre-existing `ToolPointer` for A | no new entry; `Summary` replaced with the promoted text |
| `TestPromoteNeverAddsCodeSnippets` | `ArgsPreview` containing `"func x() {\n  return\n}"` and backticks | no `\n`, no backtick in any pointer string; ≤ 161 runes |
| `TestPromoteOrdersPointersByWeightDescending` | 3 promotions with weights 5.25, 2.625, 1.75 | `Pointers.Tools` in that order; pre-existing pointers after them |
| `TestPromoteThresholdFromConfig` | `retrieval.promoteAfterExpansions = 3` | count-2 root not promoted |
| `TestPromoteDisabledByConfig` | `runtime.phase7.promotion.enabled = false` | `Finalize` produces no promoted pointers |
| `TestPromoteNoProjectRoot` | `PromotionInput{ProjectRoot: ""}` | empty report, non-nil `Skipped`, `Skipped["*"] == "no project root"`, nil error |
| `TestPromoteMaxPromotedZero` | `PromotionInput{MaxPromoted: 0}` | empty report, `Skipped["*"] == "maxPromoted < 1"`, nil error, checkpoint unmodified |
| `TestFinalizeIncludesPromotedPointers` | full writer over a temp project, 3 expansions of root A | the written `NNNN.json` contains a `pointers.tools` entry whose `summary` starts with `"promoted: re-expanded 3×"` |
| `TestFinalizeStillUnderBudgetBE` | 500 ephemeral records | `Finalize` wall time < 2 s (budget **B-E**) |

### Truncation curve (commit 6)

| Test | Setup | Expected |
|---|---|---|
| `TestTierReserve` | `avail=10000` | `TierReserve(TierLate,·)==3500`; `TierReserve(TierFirst,·)==1500` |
| `TestTruncateNeverDropsTier1` | budget `1` | `invariants`, `user_intent`, `eliminated` all intact; `DropEntry` kinds cover every tier-2 and tier-3 unit |
| `TestTruncateDropsTier3BeforeTier2` | tier1 2000, tier2 3000, tier3 6000, budget 7000 | all of tier 2 kept; part of tier 3 dropped; zero tier-2 `DropEntry`s |
| `TestTruncateTier2DropOrder` | budget forcing 2 of 3 tier-2 fields out | `open_questions` dropped first, then `decisions` (lowest `Turn` first); `current_work` always last |
| `TestTruncateTier3DropOrder` | budget forcing tier-3 truncation | `narrative` dropped first, then `pointers.tools`, then `pointers.files` |
| `TestTruncateReservesLateBudget` | tier1 2000, tier3 large enough to fill everything, budget 12000 | tier 2 retains ≥ `floor(10000*0.35) == 3500` tokens' worth |
| `TestTruncateOversizeFieldDroppedWhole` | one decision of 9000 tokens, `cap2+free == 5000` | that decision dropped entirely; no partial field; freed budget flows to tier 3 |
| `TestTruncateIsMonotone` (rapid, 300 cases) | random checkpoints, budgets `b1 < b2` | kept(b1) ⊆ kept(b2) |
| `TestOrderPointersStable` | equal weights | original relative order preserved |
| `TestTierReserveMatchesCurve` | read `testdata/phase7/truncation-curve.json` | `argmaxAtDefaultBudget.late == ReserveLatePct`; `.first == ReserveFirstPct`; `.budget == 12000` |
| `TestTruncationCurve` (`test/replay`) | 24 synthetic sessions × 9 budgets × 35 grid points, `Deterministic:true, Seed:16` | regenerates `testdata/phase7/truncation-curve.json` byte-identically; fails on any diff unless `QOMPACK_UPDATE_GOLDEN=1` |
| `TestTunedReservesBeatUntuned` | mean `FractionOfOPT` at budget 12000 | tuned `(0.35,0.15)` ≥ untuned `(0,0)` |

### Warm start (commit 4)

| Test | Setup | Expected |
|---|---|---|
| `TestWarmStartMergesProjectCMS` | project CMS with `src/auth.ts` at count 40, decay 0.6 | live `Estimate("src/auth.ts")` ∈ [23, 41] and > 0 |
| `TestWarmStartedCMSHoldsErrorBound` | derive the load from the table's own shape — `width, _ := sketch.NewCMS(cfg.Sketches.CMS.Epsilon, cfg.Sketches.CMS.Delta).Dims()`, then write `touch.project.cms` from `n = 2*width` distinct synthetic paths whose weights cycle `{1,2,3,5,8,13,21,34,55,58}` (the skewed pattern `internal/sketch`'s own `cmsStream` fixture uses; a uniform stream understates collision error). Two keys per row cell means collisions are **forced, not hoped for**. Keep the exact truth table. Run the full warm-start path — `MaybeRun` with `decay = 0.6` into an empty live CMS, i.e. `Scale(decay)` then `MergeFrom` | for **every** key: `live.Estimate(k) >= round(decay·exact[k])` (`Scale` rounds per cell and rounding is monotone, so the never-under-count guarantee survives decay) **and** `live.Estimate(k) - round(decay·exact[k]) <= ε·float64(live.Total()) + 1` — Appendix A's ε·N accuracy bound on the decayed mass, the `+1` absorbing `Scale`'s per-cell `math.Round`. Also `live.Total() == round(decay·N)`. `t.Logf` the worst over-count. **This row closes the V3-VERIFY §0 carry**: `sketchtest.RunCMSSuite` asserts only "Estimate ≥ true", which a max-estimator returning `math.MaxUint32` would satisfy, so a warm-started table held only to that suite would inherit the safety property and not the accuracy one (`internal/sketch/sketchtest/cms.go`'s own doc comment says exactly this and names SP-16). Nothing else in this subplan exercises a decayed-and-merged table |
| `TestWarmStartRunsOncePerSession` | `MaybeRun` twice with the same session id | second returns `Ran:false, Reason:"already ran"`; CMS estimate unchanged |
| `TestWarmStartBootstrapsWhenProjectCMSMissing` | no `touch.project.cms`, index with 300 path records over 3 sessions | `CMSKeysBootstrapped == 300`; file created afterwards |
| `TestWarmStartShapeMismatchRebuilds` | `touch.project.cms` written at ε=0.01, config ε=0.001 | `MergeFrom` error handled; live CMS non-zero for a historical path; one `Loud` entry `"project CMS reshaped"` |
| `TestWarmStartCorruptProjectCMS` | truncate the file to 4 bytes | bootstraps; no error returned; warm start `Ran:true` |
| `TestWarmStartDisabledWhenConfigFalse` | `sketches.cms.warmStartFromProject = false` | live `Estimate` for a historical path `== 0`; `Reason` contains `"cms disabled"` |
| `TestWarmStartRespectsMaxSessions` | 30 prior sessions, `maxSessions = 10` | only the 10 newest session ids contribute to the bootstrap (assert via distinct path families) |
| `TestWarmStartCarriesProjectEliminations` | prior session wrote 3 `scope:"project"` + 2 `scope:"session"` active records | `EliminationsCarried == 3` |
| `TestWarmStartFlipsStaleOnDependencyChange` | project elimination with `depends_on docker-compose.yml`; file changed between sessions | `EliminationsFlipped == 1`; `Ledger.Query` for it returns `AnswerStale` with the §8.3 note; `tried.bloom` no longer tests positive for its key |
| `TestWarmStartRebuildBloomSurfacesSaturation` | ledger `Health.NeedsResize == true` | one `Loud` entry containing `"bloom saturation"` with `fill` and `fp` fields (§11.4) |
| `TestWarmStartSeedsBOCDPriors` | 200 closed segments from prior sessions, `paths` centred on 0.8; call `SeedPriors(ctx, sess)` | `state/bocd.json` exists on return; `scheduler.NewDetectorFromState(hazard, features, thoseBytes)` yields a detector whose `paths` prior has `Mu0 ≈ 0.8`; `BOCDPriorFeatures == ["paths","time","todos","tools"]` |
| `TestSeedPriorsRunsBeforeRuntimeConstruction` | fake `scheduler.Runtime` factory that records whether `state/bocd.json` existed when it was called | the file existed — i.e. insertion 3 precedes Runtime construction, which is the whole ordering guarantee |
| `TestWarmStartSkipsBOCDOnResume` | `state/warmstart.json` with `bocdSeededFor` equal to the current session id | step 3 skipped in both `SeedPriors` and `MaybeRun`; `state/bocd.json` bytes unchanged; `Reason` contains `"bocd already initialised"` |
| `TestWarmStartStepFailureIsIsolated` | ledger returning an error from `RefreshStaleness` | CMS merge and BOCD seeding still ran; `Ran:true`; one `Warn` |
| `TestWarmStartHonoursBudget` | `budgetMs = 1`, 30 prior sessions | returns within 200 ms; `Reason` contains `"deadline"`; no panic; no partial file left in `.qompack/tmp/` |
| `TestWarmStartDoesNotBlockSessionStart` | e2e: real binary, `qompack session-start` | hook returns in < 15× B-A budget and always `exit 0`, regardless of warm-start duration |
| `TestRegisterPhase7RegistersTwoTasks` | fake `IdleController` recording `(name, prio)` in call order | names `["warm_start","segment_blooms"]` with priorities **5 and 40**. Assert the semantics too, not just the numbers: `5 < idlePrioDrain` (10), so `warm_start` runs first in a real `RunOnce`, and `40 != idlePrioMetrics` (30), so no two registered tasks share a priority |
| `TestRegisterPhase7OrdersAheadOfDrain` | the **real** `IdleController`, all five tasks registered, one `RunOnce` with a generous budget | `ran == ["warm_start","drain","sketches","metrics","segment_blooms"]` — the shipped controller runs lower priority first, and this is the test that would have caught the inverted reading |

### Replay, exit criteria, and non-delivery (commit 7)

| Test | Setup | Expected |
|---|---|---|
| `TestPhase7WarmStartDelta` (`test/replay`) | for each of the 24 synthetic sessions: build a project store from a *prior* synthetic session, then replay the session twice — `runtime.phase7.warmStart.enabled` false (cold) and true (warm), `Deterministic:true, Seed:16` | writes `testdata/phase7/warmstart-delta.json`; asserts `mean(warm.FractionOfOPT) >= mean(cold.FractionOfOPT)` **and** `mean(warm.FirstDivergenceTurn) >= mean(cold.FirstDivergenceTurn)`; records the signed delta for both |
| `TestPhase7NoRegressionBeyondTwoPercent` | `eval.Harness.Report` for all policies against the `develop` baseline | zero `Regression` entries with `Allowed == false` (§11.3 2% rule) |
| `TestPhase7StoreGrowthStillSublinear` | store `Stats` across the 24-session corpus after segment blooms land | growth still sublinear per §11.3; total segment-bloom bytes ≤ 3 KB × segment count |
| `TestPrefixReorderingNotAttempted` | walk `internal/**/*.go`, `cmd/**/*.go`, skipping `_test.go` | no occurrence of `cache_control`, `cacheControl`, `CacheBreakpoint`, `SetBreakpoint`, `ReorderPrefix`, or `MutationRateSort`, except inside `internal/eval` (SP-02's §5.6 Belady breakpoint **measurement**, explicitly labelled not-plugin-actionable) — one occurrence outside that allowlist fails the test with the offending file:line |
| `TestPhase7ADRDocumentsNonDelivery` | read `docs/adr/0016-phase7-refinements.md` | contains verbatim: `Prefix reordering by mutation rate (§5.6) is deliberately not implemented: §12 states Qompack "cannot place or move cache_control breakpoints". It is harness/API-port material only.` |
| `TestSegmentBloomAnswersWithoutExpansion` (`test/e2e`) | real binary + real daemon, 12 closed segments, ask for a path present in one | the answer names exactly that segment; the daemon's `obs` counter for object reads is unchanged across the query |
| `TestPromotedPointersReachRehydration` (`test/e2e`) | expand root A three times via the MCP `expand` tool, force a checkpoint, then `SessionStart(source=compact)` | the injected `additionalContext` contains A's path in the pointer section; total injection ≤ `runtime.rehydrate.maxTokens` (12 000) |
| `TestIndexExpansionsAgreeWithPromoter` (`test/e2e`) | same setup; compare `store.EphemeralExpansions` against SP-13's `mcp.Promoter.Promoted(ctx, sess)` | identical hash sets at `retrieval.promoteAfterExpansions = 2`. This is the traceability check that SP-16's durable, restart-surviving evidence source is the same signal §8.7 asks the Promoter to count — SP-16 reads the index rather than the in-memory counter so a daemon restart between the expansion and the checkpoint cannot lose the promotion, and this test pins the two to the same answer |
| `TestHooksStillExitZeroUnderPhase7Faults` (`test/e2e`) | fault-inject each new failure mode from the error matrix | every hook subcommand exits 0 (§2.3, §13 invariant 6) |

### Benchmarks (each names its budget)

| Benchmark | Budget |
|---|---|
| `BenchmarkBuildSegmentBloom` (2 000 records) | **< 50 ms**; SP-16-defined, inside `buildBudgetMs` 250 ms; off the hot path |
| `BenchmarkSegmentsMayContain200` | **< 5 ms** |
| `BenchmarkPromote500` | **< 50 ms**, ≤ 2.5% of **B-E** (`checkpoint_finalize` p99 < 2 s, §11.3 L4) |
| `BenchmarkTruncateTuned` | **< 20 ms** at budget 12 000; contributes to **B-E** |
| `BenchmarkWarmStart10Sessions` | **< 3 s** (`runtime.phase7.warmStart.budgetMs`) |
| `BenchmarkEvaluateWithSkiRental` | within `benchstat`'s 10% warn / 25% fail band vs `testdata/bench-baseline.txt` (§7) |
| `devtool bench-hotpath -n 2000` | **B-A p99 < 15 ms** unchanged on linux/macos/windows (§13 invariant 9) |

---

## Commit plan

Exactly **7 commits**, all on `feat/sp16-phase7-refinements`. Each compiles and passes `go run ./tools/devtool test` for the packages it touches before being committed.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1 — `feat(config): add the runtime.phase7 namespace for Phase 7 refinements`

- [ ] Cut the branch: `git checkout develop && git pull && git checkout -b feat/sp16-phase7-refinements`
- [ ] Write `internal/config/phase7_test.go` with `TestPhase7DefaultsMatchArchitecture`, `TestAppendixCGoldenStillPasses`, `TestPhase7ValidationRejectsOutOfRange` (10 rows), `TestPhase7InvalidConfigFallsBackNotCrashes`, `TestConfigDocsNotStale`. Run `go test ./internal/config/...` — **must fail to compile** (types absent).
- [ ] Create `internal/config/phase7.go` (`Phase7Cfg`, `WarmStartCfg`, `PromotionCfg`, `SegmentBloomCfg`, `validatePhase7`).
- [ ] Modify `internal/config`: one field on `RuntimeCfg`, one block in `Defaults()`, one line in `Validate()`.
- [ ] Modify `plans/00-ARCHITECTURE.md`: §11.5 `phase7` block, §5.8 `BloomRef` comment.
- [ ] Run `go run ./tools/devtool gen-config-docs` and commit the regenerated `docs/config-reference.md`.
- [ ] `go run ./tools/devtool fmt lint vet test` — green, including SP-01's Appendix C golden.
- [ ] Files: `internal/config/phase7.go`, `internal/config/phase7_test.go`, `internal/config/{runtime,defaults,validate}.go`, `plans/00-ARCHITECTURE.md`, `docs/config-reference.md`.
- [ ] Message body: the namespace is additive per §11.5 and changes no Appendix C key. Footer: `Refs: SP-16, §10 Phase 7, §11.5`

### Commit 2 — `feat(scheduler): ski-rental write policy and prior-seeded changepoint detection`

- [ ] **Prerequisite, before any code in this commit:** the `plans/00-ARCHITECTURE.md` §5.13 amendment widening the `TriggerReason` wire enum from five values to seven must already have landed on `develop`. `TriggerSkiRentalDefer` and `TriggerSkiRentalShallow` are **new wire values**, not aliases of the shipped five, and §5.13's inline enumeration plus `internal/scheduler/types.go`'s "five named conditions" godoc both still say five. A §5 contract widening is an §0 amendment — it does not ride in on a feature branch.
- [ ] **Extend** SP-12's `internal/scheduler/skirental_test.go` (12 tests) and write `internal/scheduler/warmprior_test.go` (11 tests) — all 23 tests from the two tables above. Run — **must fail to compile**, because `SkiRentalThreshold`, `EstimateRemainingReads`, `applySkiRental`, the two `TriggerReason` constants and every `warmprior.go` symbol do not exist. SP-12's existing `TestSkiRentalShouldWrite`, `TestSkiRental_ComputedNotLiteral` and `TestSkiRental_ThresholdTracksConfig` rows are **not edited** and must be **green on the first run that compiles** — i.e. immediately after the new symbols land in the next step, with no edit to their bodies. (A reconciled exception to `plans/README.md:59`'s red-first rule, not a silent carve-out: these rows cover code SP-01 shipped and SP-12 moved, so there is no red state to observe and manufacturing one would break working code. Because the package does not compile until the new symbols exist they cannot be *run* green first either — they are observed green in the same run as every other row, and that run is the regression proof for this commit's re-expression.) A session that "fixes" them into failing has broken working code.
- [ ] **Extend** SP-12's `internal/scheduler/skirental.go` with `SkiRentalThreshold`, `EstimateRemainingReads`, `applySkiRental`, `onlySoftReasons` and the two `TriggerReason` constants. No file is created — SP-12 created `skirental.go` in wave 3.
- [ ] **Re-express** SP-12's `SkiRentalShouldWrite` body in place through `SkiRentalThreshold`, preserving **both** guards (`r <= 0` and `w <= 0`) and its doc comment; there is exactly one definition before and after. `formulas.go` does not exist at this point — SP-12 deleted it in wave 3, redistributing `YoungDaly` into `youngdaly.go` and `pSelectionAvailable` / `PSelectionAvailable` into `gate.go`; neither file is touched here. `TestSkiRental_ComputedNotLiteral` and `TestSkiRental_ThresholdTracksConfig` are **not edited** and must still be green — they are the proof the re-expression preserved behaviour.
- [ ] Create `internal/scheduler/warmprior.go` (`FeaturePrior(s)`, `DefaultFeaturePriors`, `SeedPriorsFromSegments`, `seeded`, `NewBOCDWithPriors`, `NewDetectorFromState`, `SetPriorWeight`, `FeatureValue`, `WithFeatureValue`, `seededWire` round-trip).
- [ ] Modify `internal/scheduler/evaluate.go`: extract the per-candidate score into `scoreOf(in, c)` if inline (formula unchanged), then insert the single line `applySkiRental(&d, in)` before the return.
- [ ] Modify SP-12's `scheduler.Runtime` implementation file in `internal/daemon`: the one-line swap of `scheduler.NewBOCD(...)`+`UnmarshalBinary(blob)` for `scheduler.NewDetectorFromState(hazard, features, blob)`.
- [ ] Modify `internal/scheduler/types.go` (SP-01): the `TriggerReason` godoc — "five named conditions" and "all five … can appear in `Decision.Reasons`" become seven, naming `ski_rental_defer` and `ski_rental_shallow` as the two Phase 7 policy annotations. This mirrors the §5.13 amendment in the first bullet; the constant set and the godoc must never disagree.
- [ ] `go test ./internal/scheduler/... ./internal/daemon/... -race` green; `go test -run TestEvaluateIsStillPure -count=2` green.
- [ ] `go test -bench BenchmarkEvaluate ./internal/scheduler/... | benchstat testdata/bench-baseline.txt -` — within 10%.
- [ ] Files: `internal/scheduler/{skirental,warmprior}.go` + tests, `internal/scheduler/evaluate.go`, `internal/scheduler/types.go` (the `TriggerReason` godoc, five → seven), SP-12's Runtime file in `internal/daemon`.
- [ ] Footer: `Refs: SP-16, §5.6, §6.6, Appendix A`

### Commit 3 — `feat(store): per-segment bloom filters populating Segment.BloomRef`

- [ ] Write `internal/store/segbloom_test.go` (16 tests) and `internal/store/ephemeral_test.go` (6 tests), plus `seedSegment`. Run — **must fail**.
- [ ] Create `internal/store/segbloom.go` (key scheme, `BuildSegmentBloom`, `LoadSegmentBloom`, `SegmentMayContain`, `SegmentsMayContain`, `BackfillSegmentBlooms`, `SegmentBloomRef`, the `max(capacityPerSegment, len(keys))` sizing rule). **No `INDEX.jsonl`** — `index/segments.jsonl` plus SP-06's `segBloomRec` is the durable lookup.
- [ ] Create `internal/store/ephemeral.go` (`Expansion`, `EphemeralExpansions`, `ProjectPathTouches`).
- [ ] Modify `internal/store/segments.go` with §4's three anchored sites: the two `segLog` fields, `openSegLog`'s two new parameters plus `recordBloomRef`, and the bloom build + `segBloomRec` append at the end of `Close`.
- [ ] Modify `internal/store/open.go`: `openSegLog` gains `root, cfg` at its one call site in `openFS`. Update the `openSegLog` call in `internal/store/segments_test.go` the same way (`b.TempDir()`, `config.Defaults()`) — mechanical, nothing else in that file changes.
- [ ] `go test ./internal/store/... -race` green; `p.AssertAppendOnly(t)` still passes.
- [ ] `go test -bench 'BenchmarkBuildSegmentBloom|BenchmarkSegmentsMayContain' ./internal/store/...` — within the 50 ms / 5 ms budgets.
- [ ] `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon` — B-A p99 < 15 ms, unchanged.
- [ ] Files: `internal/store/{segbloom,ephemeral}.go` + tests, `internal/store/segments.go`, `internal/store/open.go`, `internal/store/segments_test.go` (the `openSegLog` argument update only).
- [ ] Footer: `Refs: SP-16, §6.8, §8.7, Appendix A`

### Commit 4 — `feat(daemon): O4 cross-session warm start for CMS, eliminations, and BOCD priors`

- [ ] Write `internal/daemon/phase7_test.go` (19 tests) plus `priorSessions`. Run — **must fail**.
- [ ] Create `internal/daemon/phase7.go` (`WarmStarter`, `WarmStartReport`, `NewWarmStarter`, `SetSession`, `SeedPriors`, `MaybeRun`, `LastReport`, `bootstrapProjectCMS`, `RegisterPhase7`).
- [ ] Modify `internal/daemon`'s daemon type and `New(o Options)` with the four insertions of §8: struct field `warm *WarmStarter`; `d.warm = RegisterPhase7(o, d.Idle())`; `SetSession` + synchronous `SeedPriors` immediately before the session's `scheduler.Runtime` is constructed; `go d.warm.MaybeRun(...)` immediately after.
- [ ] `go test ./internal/daemon/... -race` green.
- [ ] `go test -bench BenchmarkWarmStart ./internal/daemon/...` — under 3 s.
- [ ] Files: `internal/daemon/phase7.go` + test, `internal/daemon/daemon.go`.
- [ ] Footer: `Refs: SP-16, §10 Phase 7 (O4), §8.3, §6.2`

### Commit 5 — `feat(checkpoint): promote frequently re-expanded hashes into the pointer tier`

- [ ] Write `internal/checkpoint/promote_test.go` (15 tests) plus `ephemeralRuns`. Run — **must fail**.
- [ ] Create `internal/checkpoint/promote.go` (`Promotion`, `PromotionReport`, `PromotionInput`, `PromotionInputFromConfig`, `Promote`, `sanitize`).
- [ ] Modify `Finalize` with the anchored promotion block, placed **before** `Truncate`.
- [ ] `go test ./internal/checkpoint/... -race` green; the checkpoint golden snippet-grep test (§13 invariant 5) still passes.
- [ ] `go test -bench BenchmarkPromote ./internal/checkpoint/...` — under 50 ms; `TestFinalizeStillUnderBudgetBE` green.
- [ ] Files: `internal/checkpoint/promote.go` + test, `internal/checkpoint/writer.go`.
- [ ] Footer: `Refs: SP-16, §8.7, §10 Phase 7, G3.3`

### Commit 6 — `feat(checkpoint): tune progressive truncation reserves from the measured curve`

- [ ] Write `internal/checkpoint/curve_test.go` (10 tests) and `test/replay/truncation_curve_test.go`. Run — **must fail**.
- [ ] Create `internal/checkpoint/curve.go` with the initial constants `0.35` / `0.15`, `TierReserve`, `OrderPointers`, `Tier2DropOrder`, `Tier3DropOrder`.
- [ ] Modify `Truncate`'s body to the Phase A/B/C allocation algorithm; signature unchanged.
- [ ] Run `QOMPACK_UPDATE_GOLDEN=1 go test ./test/replay -run TestTruncationCurve` to generate `testdata/phase7/truncation-curve.json`.
- [ ] **Replace `ReserveLatePct` / `ReserveFirstPct` with `argmaxAtDefaultBudget` from the generated artifact**, then re-run without the env var: `TestTierReserveMatchesCurve` and `TestTunedReservesBeatUntuned` must be green.
- [ ] `go test ./internal/checkpoint/... ./test/replay/... -race` green.
- [ ] Files: `internal/checkpoint/curve.go` + test, `internal/checkpoint/truncate.go`, `test/replay/truncation_curve_test.go`, `testdata/phase7/truncation-curve.json`.
- [ ] Footer: `Refs: SP-16, §6.9, §8.5, G4.3, G7.3`

### Commit 7 — `test(sp16): Phase 7 replay exit validation, warm-start delta, and non-delivery guard`

- [ ] Write `test/replay/phase7_test.go` (`TestPhase7WarmStartDelta`, `TestPhase7NoRegressionBeyondTwoPercent`, `TestPhase7StoreGrowthStillSublinear`) and `test/e2e/phase7_test.go` (`TestSegmentBloomAnswersWithoutExpansion`, `TestPromotedPointersReachRehydration`, `TestIndexExpansionsAgreeWithPromoter`, `TestHooksStillExitZeroUnderPhase7Faults`, `TestPrefixReorderingNotAttempted`, `TestPhase7ADRDocumentsNonDelivery`). Run — **must fail**.
- [ ] Run `QOMPACK_UPDATE_GOLDEN=1 go test ./test/replay -run TestPhase7WarmStartDelta` to generate `testdata/phase7/warmstart-delta.json`; re-run without the env var — green.
- [ ] Write `docs/adr/0016-phase7-refinements.md`: context (§10 Phase 7), the five decisions, the measured warm-start delta and truncation argmax quoted from the two artifacts, and the verbatim non-delivery sentence.
- [ ] `go run ./tools/devtool ci-local` — `verify`, `test`, `cover`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs` all green.
- [ ] `git log --format=%B develop..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'` returns nothing.
- [ ] Files: `test/replay/phase7_test.go`, `test/e2e/phase7_test.go`, `testdata/phase7/warmstart-delta.json`, `docs/adr/0016-phase7-refinements.md`.
- [ ] Footer: `Refs: SP-16, §10 Phase 7, §11.3, §12`

---

## Subagent strategy

This subplan is small enough that subagents are unnecessary — seven commits across six packages, each with a bounded, fully-specified change. The implementer may optionally dispatch one subagent to write the test tables for commits 2 and 3 in parallel with implementing commit 1, since those tests depend only on signatures fixed in this document; everything else should be done in a single session so the anchored edits to SP-01's, SP-05's, SP-06's, SP-10's, and SP-12's files stay consistent with one another.

---

## Exit criteria

### From `Qompack.md` — quoted verbatim

Phase 7 is the only phase in §10 with no exit criterion of its own; it is governed by §11.3, which binds every phase gate:

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

And by the Phase 7 statement of intent, which this subplan makes measurable:

> **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions. First-compaction quality in a fresh session should benefit from every session before it.

And by §11.4, which the segment blooms and the elimination rebuild must respect:

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### Local, measurable Definition of Done

- [ ] **O4 warm start.** Across all 24 synthetic sessions, the warm-started run's mean `FractionOfOPT` is greater than or equal to the cold run's, and its mean `FirstDivergenceTurn` is greater than or equal to the cold run's, with both signed deltas committed in `testdata/phase7/warmstart-delta.json`. `sketches.cms.warmStartFromProject` is honoured in both directions (`TestWarmStartDisabledWhenConfigFalse`). The warm-started Count-Min is held to **accuracy, not merely safety**: `TestWarmStartedCMSHoldsErrorBound` forces collisions with a `Dims()`-derived load and asserts Appendix A's ε·N bound for every key after `Scale`+`MergeFrom`, which closes the V3-VERIFY §0 carry that `sketchtest.RunCMSSuite` cannot discriminate a max-estimator from a real one.
- [ ] **Demand-driven promotion.** With `retrieval.promoteAfterExpansions = 2`, a hash expanded 3× appears in the next checkpoint's `pointers.tools` at an elevated weight and reaches the rehydrated context within the 12 000-token cap (`TestPromotedPointersReachRehydration`).
- [ ] **Per-segment blooms.** `Segment.BloomRef` is non-empty for every segment closed after this branch **and still non-empty after the store is reopened**, because it is recorded as a `segBloomRec` line in `index/segments.jsonl` rather than only assigned in memory (`TestCloseBloomRefSurvivesReopen`); no `INDEX.jsonl` sidecar exists anywhere under `.qompack/sketches/segments/`; `SegmentsMayContain` answers segment relevance with zero calls to `Open`/`OpenSpan`/`GetChunk`/`GetRoot`; measured false-positive rate ≤ 1.5% at design fill; sizing matches Appendix A (`m ∈ [19600,19700]`, `k == 7`).
- [ ] **Ski rental.** `SkiRentalThreshold(0.1, 1.25) == 12.5` exactly, and the token `12.5` appears nowhere in non-test source (`TestNoLiteral12Point5InSource`). The policy defers only on soft reasons and never overrides `hard_ceiling`, `changepoint`, or `idle_cold_cache`.
- [ ] **Progressive truncation.** `ReserveLatePct` and `ReserveFirstPct` equal the argmax recorded in `testdata/phase7/truncation-curve.json`; truncation is monotone; tier 1 is never truncated at any budget.
- [ ] **Non-delivery.** `TestPrefixReorderingNotAttempted` is green and `docs/adr/0016-phase7-refinements.md` carries the verbatim non-delivery sentence.
- [ ] All tests green: `go run ./tools/devtool test` and `test-race`; coverage floors met (`checkpoint`, `store`, `config`, `sketch` ≥ 90%; `scheduler` ≥ 85%; `daemon` ≥ 75%).
- [ ] `gofumpt -l` empty; `golangci-lint run` clean; `nomagic` clean; import-graph layer check clean; `go vet` clean.
- [ ] Benchmarks within budget: B-A p99 < 15 ms on all three platforms (unchanged), B-E p99 < 2 s, `BenchmarkBuildSegmentBloom` < 50 ms, `BenchmarkPromote500` < 50 ms, `BenchmarkWarmStart10Sessions` < 3 s, `BenchmarkEvaluateWithSkiRental` within 10% of baseline.
- [ ] `replay-gate` green: zero disallowed regressions under the 2% rule; every previously-merged phase exit assertion still passes.
- [ ] `plugin-validate`, `security`, and `docs` jobs green; `docs/config-reference.md` not stale.
- [ ] CI green on `feat/sp16-phase7-refinements`; 7 commits; no attribution trailers anywhere in the range.

---

## Done checklist

- [ ] Every item quoted in **Design context** has a corresponding implementation or an explicit non-delivery: §5.6 ski rental → `skirental.go`; §5.6 prefix reordering → non-delivery test + ADR; §6.8 per-level blooms → `segbloom.go`; §6.9 progressive truncation → `curve.go` + measured artifact; §8.7 promotion bullet → `promote.go`; §8.3 item 5 scope carry-forward → `daemon/phase7.go` step 2; §6.6 feature list → `warmprior.go`; §10 Phase 7 five bullets → commits 2–7; Appendix A ski-rental, Bloom, and Count-Min formulas → computed, never literal.
- [ ] Placeholder scan over the code this branch adds: `grep -rniE 'TBD|FIXME|XXX|not implemented|placeholder' internal/ test/ docs/adr/0016-phase7-refinements.md` returns nothing outside SP-01's `core.ErrNotImplemented` declaration.
- [ ] Type consistency with the **Interface contract**: every signature in Produces exists verbatim in the code; every signature in Consumes is called unchanged; no §5 interface gained, lost, or changed a method (Rule W-3 not engaged — `recordBloomRef` is an unexported method on the concrete `segLog` and `openSegLog` is an unexported constructor, so neither touches the frozen `SegmentLog` seam).
- [ ] Only the anchored edits enumerated below were made to files owned by other subplans — **fifteen sites across thirteen files**, nothing else in any of them changed:

  | File (owner) | Edits | What |
  |---|---|---|
  | `internal/config/{runtime,defaults,validate}.go` (SP-01) | 3 | one `RuntimeCfg` field, one `Defaults()` block, one `Validate()` line |
  | `internal/scheduler/skirental.go` (SP-12) | 1 | `SkiRentalShouldWrite` **re-expressed in place** through `SkiRentalThreshold`; both guards (`r <= 0`, `w <= 0`) and the doc comment preserved. The commit's new Phase 7 symbols are appended to the same file and are not anchored edits |
  | `internal/scheduler/types.go` (SP-01) | 1 | the `TriggerReason` godoc — "five named conditions" / "all five" → seven, naming `ski_rental_defer` and `ski_rental_shallow` as the two Phase 7 policy annotations. Requires the §5.13 amendment named in commit 2's first bullet |
  | `internal/scheduler/evaluate.go` (SP-12) | 1 | `applySkiRental(&d, in)` before the return (plus extracting `scoreOf` if it was inline) |
  | SP-12's `scheduler.Runtime` implementation file in `internal/daemon` | 1 | `NewBOCD(...)`+`UnmarshalBinary` → `scheduler.NewDetectorFromState(...)` |
  | `internal/store/segments.go` (SP-06) | 3 | the two `segLog` fields (`root`, `cfg`); `openSegLog`'s two new parameters plus the unexported `recordBloomRef`; the bloom build + `segBloomRec` append at the end of `Close` |
  | `internal/store/open.go` (SP-06) | 1 | `openSegLog(…, root, cfg, …)` at its one call site in `openFS` |
  | `internal/store/segments_test.go` (SP-06) | 1 | the same two arguments at the one benchmark call site — mechanical |
  | `internal/checkpoint/writer.go` and `truncate.go` (SP-10) | 2 | the promotion block in `Finalize`; the Phase A/B/C body of `Truncate` |
  | `internal/daemon/daemon.go` (SP-05) | 1 site, 5 lines | the struct field and the four §8 insertions |
- [ ] `internal/checkpoint/grammar.go` (SP-15) was never touched; the `Finalize` merge conflict, if any, was resolved on this branch keeping both blocks with grammar first and promotion second.
- [ ] `Qompack.md` is byte-identical to its state at branch cut (`git diff develop -- Qompack.md` is empty).
- [ ] Commit count verified: `git rev-list --count develop..HEAD` returns **7** (within the 5–8 range).
- [ ] No co-author or attribution trailers: `git log --format=%B develop..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'` returns nothing.
- [ ] Both measurement artifacts are committed and reproducible: re-running `TestTruncationCurve` and `TestPhase7WarmStartDelta` without `QOMPACK_UPDATE_GOLDEN` is green.
- [ ] Self-review pass: every constant in the code either comes from `config`, is derived by an Appendix A formula, or is a measured value carrying a comment naming the artifact that justifies it.

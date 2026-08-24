# Traceability — Qompack.md → subplan set

This matrix proves the subplan set preserves the entire design document. Every gap, phase, layer, tool, revision item, and closing-note priority in `Qompack.md` maps to an **owning** subplan (others consume its output); where `Qompack.md` splits an obligation across two layers, the row names both owners and says which half each holds. Ownership in **§1** is taken from each subplan's own header metadata (`**Gaps closed:**` / `**Design sections:**`) and mirrors the §9 gap-traceability matrix of the design document. **§3's layer column is not derivable that way** — SP-05, SP-07 and SP-14 carry no `§7.2 L*` tag in their header metadata — so it, and the surface and mitigation tables in §4 and §7, are read off each subplan's declared scope against the design section named in the heading. Re-testing ownership is listed under Verification.

## 1. Gap → owning subplan (mirrors Qompack.md §9)

| Gap | Description (short) | Owner | Notes / residual |
|---|---|---|---|
| G1.1 | Task-blind trigger | SP-12 | BOCD changepoints; cannot block Claude Code's own trigger |
| G1.2 | No agent agency over timing | SP-12 | Soft floor + hard ceiling; partial by design |
| G1.3 | Fixed 13K buffer | SP-12 | Adaptive Young–Daly interval |
| G1.4 | Blocking-limit cliff | SP-12 | Soft floor keeps sessions off the cliff; cliff itself is upstream |
| G1.5 | No task-boundary signals | SP-08 + SP-12 | Closed **jointly**, as `Qompack.md` §9 states it (L0 todo/git/test signals → L3): SP-08 owns `observer.ExtractSignals`, SP-12 owns the translation (`FeaturesFrom`, the `todos` feature) and the consumption (segment close on todo/test/commit). SP-08's half is inert until SP-12 lands in wave 3 |
| G2.1 | Recursive compression | SP-06 | Segment log `encoded-once` DPI guard; append-only invariant |
| G2.2 | Nothing pinned | SP-10 | `pins/invariants.json` + sketches (primitives from SP-03) |
| G2.3 | User messages regenerated | SP-08 | Verbatim immutable capture; replayed by SP-11 |
| G2.4 | Prose, not typed state | SP-10 | Versioned typed checkpoint schema |
| G2.5 | No ground-truth check | SP-10 | Pointer set validated against `git status` |
| G2.6 | Session-memory drift | SP-10 | Checkpoint is the durable source |
| G3.1 | Unreachable transcript | SP-06 | Store + retrieval path (surfaced by SP-13) |
| G3.2 | Pointerless tombstones | SP-08 | Addressable tombstones — SP-08 renders the marker, but no subplan puts it in front of the model; delivery is unowned, see §11.2 |
| G3.3 | Eager restoration | SP-11 | Pointer-first, 8–12K budget; refined by SP-16 promotion |
| G3.4 | Snippets in summary | SP-10 | Focus instructions forbid them; advisory (checkpoint is backstop) |
| G4.1 | Path rules lost | SP-11 | Re-reads matching rules |
| G4.2 | Nested CLAUDE.md lost | SP-11 | Re-reads by pointer directory |
| G4.3 | Skill head-truncation | SP-10 | Importance ordering; SP-11 skill index; upstream truncation unchanged |
| G4.4 | Skill index gone | SP-11 | ~450-token compact index re-injection |
| G4.5 | No drop report | SP-11 | Explicit drop report |
| G5.1 | Single cut | SP-12 | Multi-segment log; cannot multi-cut the live array |
| G5.2 | Wrong direction cheap | SP-12 | p-selection with cache term |
| G5.3 | Homogeneous treatment | SP-10 | Three-tier typed schema |
| G6.1 | No slot for eliminations | SP-09 | `eliminated[]` + Bloom (sketch primitives from SP-03) |
| G6.2 | Re-attempt loop | SP-13 | `already_tried` standing instruction (records from SP-09) |
| G6.3 | Highest-Δ content dropped | SP-15 | Δ-scoring prioritises it |
| G7.1 | Expensive summarization call | SP-12 | Earlier, cheaper, better-targeted compaction |
| G7.2 | No cheap summarizer | SP-18 | **Not plugin-closeable** — documented in the cannot-do list + upstream issue |
| G7.3 | PTL drops intent | SP-11 | Verbatim intent restored regardless; PTL logic unchanged |
| G7.4 | Circuit breaker gives up | SP-10 | Checkpoints make the session survivable |
| G7.5 | Tool-call-instead-of-summary | SP-11 | Checkpoint is the fallback path |
| G7.6 | Loops / >100% jam | SP-12 | Keeps sessions out of the failure region; underlying bug is upstream |
| G8.1 | No observability | SP-02 | Replay harness; surfaced live by SP-14 `/qompack:status` |
| G8.2 | Degraded summarizer vantage | SP-12 | Compacts earlier, at lower context; partial by design |
| G8.3 | No feedback on compaction | SP-02 | Fraction-of-OPT metric |
| G9.1 | Not a durable checkpoint | SP-10 | Immutable versioned checkpoints |
| G9.2 | Hand-rebuilt layer | SP-17 | The packaged plugin *is* the layer |
| G9.3 | Undocumented contracts | SP-05 | Contract monitor; fail-loud degradation to passive recording |
| G10.1 | Subagent double-compression | SP-08 | `SubagentStop` capture |
| G10.2 | Coarse token estimation | SP-06 | Exact chunk-level accounting |

All 40 gap IDs verified present in the plan set by grep (`G[0-9]+\.[0-9]+` across `plans/*.md`).

## 2. Build phases (Qompack.md §10) → subplans

| Phase | Content | Owner(s) | Exit criterion re-tested at |
|---|---|---|---|
| 0 — Measurement | Replay harness, divergence metrics, Belady OPT, stock baseline | SP-02 | V2 (baseline gate), then every later checkpoint via replay-gate |
| 1 — Store and observer | FastCDC, canonicalizers + MinHash (O2), hooks, Merkle index, tombstones, supersession | SP-04 + SP-06 + SP-08 (closes the phase) | V2 (primitives), V3 (≥4:1 dedup ratio, p99 < 15ms) |
| 2 — Negative knowledge | Bloom, descriptors, staleness (GA), MCP `record_eliminated`/`already_tried`, CMS/HLL, the standing instruction in rehydrated context | SP-03 (primitives) + SP-09 (closes) + SP-13 (tools) + SP-11 (the standing instruction, §8.7 design note) | V3, V4 |
| 3 — Checkpoint and rehydrate | Checkpoint writer, rehydrator, drop report, focus instructions incl. O1 | SP-10 + SP-11 (closes) | V4 |
| 4 — Scheduler | BOCD, Young–Daly, p-selection, idle model (E1), O3, O5 frontier | SP-12 | V4 (amortization claim tested directly) |
| 5 — Selection | DAG (built in SP-07), thin slicing, Δ-scoring, suffix-constrained submodular | SP-07 (DAG/slicing) + SP-15 | V5 |
| 6 — Grammar / loop detection | Sequitur, thrash warning, grammar-compressed action history | SP-15 | V5 |
| 7 — Refinement | O4 warm start, demand-driven promotion, per-segment Blooms, ski-rental, progressive truncation | SP-16 | **No exit criterion of its own** — Phase 7 is the only §10 phase without one, so V5 and V6 gate it on `Qompack.md` §11.3's cross-phase budgets (hook p99, sublinear growth, the 2% no-regression rule) instead |
| — Production | Packaging, hardening, release, docs, UAT | SP-17 + SP-18 | V6 (release gate) |

Ordering constraint from the closing note honoured: p-selection (SP-12, wave 3) ships strictly **before** slicing/submodular selection (SP-15, wave 4).

## 3. Layers (Qompack.md §7.2) → subplans

| Layer | Owner(s) |
|---|---|
| L0 Observer | SP-05 (daemon/IPC substrate, hook client, contract monitor) + SP-08 (event handlers) |
| L1 Store | SP-06 |
| L2 Analyzer | SP-09 (negative knowledge) + SP-15 (Δ-scoring, submodular, Sequitur) + SP-07 (DAG, slicing) |
| L3 Scheduler | SP-12 |
| L4 Checkpointer | SP-10 |
| L5 Rehydrator | SP-11 |
| L6 Retrieval (MCP + commands) | SP-13 (8 MCP tools) + SP-14 (7 slash commands) |
| L7 Evaluation | SP-02 |

## 4. Hook surface (§7.3), MCP tools (§8.7), slash commands (§7.5)

| Surface | Owner |
|---|---|
| `PostToolUse`, `UserPromptSubmit`, `Stop`/`SubagentStop` | SP-08 (entry points on SP-05's substrate) |
| `PostToolUse` (todo/git) — the L3 task-boundary tap §7.3 lists separately | SP-12 (the tap and its consumption: `FeaturesFrom`, segment close on todo/test/commit) over SP-08's `observer.ExtractSignals` |
| `SessionStart` (source branching: startup/resume vs compact) | SP-08 (dispatch) → SP-11 (compact path) |
| `SessionEnd` | SP-08 (the L0 entry point / driver) → SP-06 (the L1 work §7.3 assigns: flush, compact the store, write the session index) |
| `PreCompact` (checkpoint + `custom_instructions`) | SP-10 |
| MCP tools: `recall`, `expand`, `re_read`, `already_tried`, `record_eliminated`, `timeline`, `why`, `dropped` | SP-13 (ephemeral-at-birth, minimal-span, promotion counting) |
| Slash commands: `status`, `recall`, `pin`, `checkpoint`, `why`, `dropped`, `eval` | SP-14 |
| Plugin manifest + `.qompack/` directory layout + Appendix C config loader | SP-01 (layout/loader), SP-17 (shipped manifest), SP-18 (config reference: every leaf `config.Defaults()` exposes, an equal number of `Meta` entries, zero orphans, zero missing — the count is whatever the tree holds at V6, never a frozen literal) |

## 5. Revision-log items (v1.1 / v1.2) → subplans

| Item | Content | Owner |
|---|---|---|
| E1 | Sliding-TTL idle model; idle detection as scheduler input | SP-12 |
| E1a | Cache **regime** resolution (which TTL and which `w` the session is billed at), the request-start TTL anchor, and the effort-change invalidation signal — the discharge of §5.1's *"verify against current pricing before tuning"*. See `V2-report.md` §19 | SP-12 (`cacheregime.go`), SP-16 (ski-rental reads `w` from the regime), SP-18 (the four cache limits in `cannot-do.md`) |
| E1b | `TriggerCacheExpiring` — §5.4's "scheduled against cache state" applied to the expiring band, not only the cold one; saves `(1−r)·n` on every idle-driven compaction | SP-12 |
| E2 | `cache_control` breakpoints rescoped to harness-port only | SP-16 (explicit non-delivery test + ADR) and SP-18 (cannot-do list) |
| GA | Elimination staleness: `depends_on` hashes, active/stale, rebuild-from-records | SP-09 |
| GB | Retrieval re-inflation: ephemeral-at-birth, minimal span, promotion | SP-13 |
| GC | Checkpoints regenerated from the store only; injection tagging | SP-10 |
| O1 | Incremental-span focus instruction | SP-10 |
| O2 | Per-tool canonicalization + MinHash near-dedup | SP-04 — first half only; the §8.1 delta-vs-full storage write is unowned, see §11.1 |
| O3 | Idle-time background work | SP-12 |
| O4 | Cross-session warm start | SP-16 |
| O5 | Amortized compaction / frontier advancement | SP-12 (frontier driver) with SP-10 (incremental checkpoint writes) |

## 6. Evaluation methodology (§11) and guardrails

| Item | Owner | Enforced at |
|---|---|---|
| Fraction of Belady OPT (primary metric) | SP-02 | Replay-gate in CI from V2 onward |
| Secondary metrics incl. compaction pause, residual span, first-turn-after latency | SP-02 | V4 (amortization), V5, V6 |
| Hook p99 < 15ms (L0), < 2s (L4) | SP-05 / SP-10 | Bench-gate at every checkpoint from V2 |
| Store growth sublinear after dedup | SP-06 | V2 onward |
| 2% no-regression rule | SP-02 (measurement) | Every V checkpoint; zero sign-offs allowed at V6 |
| Replay suite on every phase gate | SP-02 | All checkpoints |
| Watch-fors: replay overfitting (re-collection), Bloom FP compounding (fill-ratio monitor + resize) | SP-02 / SP-03 | V5, V6 |

## 7. Risk register (§12) plugin-actionable mitigations

| Mitigation | Owner |
|---|---|
| Contract monitor, fail-loud, degrade to passive recording | SP-05 |
| Incremental checkpoint writing (never rely on `PreCompact` time) | SP-10 + SP-12 |
| Reference-counted GC, retention window, size surfaced by `/qompack:status` | SP-06 + SP-14 |
| Async queue-and-drain on hot-path overrun | SP-05 |
| Hard rehydration budget cap, pointer-first | SP-11 |
| Bloom fill monitoring, resize, rebuild from `eliminated[]` | SP-03 + SP-09 |
| Soft floor below auto-threshold (no fighting stock compaction) | SP-12 |
| Cache multipliers `r`/`w` read from config, never hardcoded | SP-01 (loader) + SP-12 (ski-rental computes `w/r`, literal forbidden in source) |
| Ephemeral retrieval results, first eviction candidates | SP-13 |
| Advisory `custom_instructions`; checkpoint remains authoritative | SP-10 |

## 8. Honesty surface

The §12 "What this plugin cannot do" list (all eight bullets), the §9 residual notes, and the upstream-issues list are reproduced verbatim in user documentation owned by **SP-18** (tested by `TestCannotDoListVerbatim`, `TestCannotDoCoversResiduals`, `TestCannotDoCoversG72`, `TestCannotDoQuotesScopeNote`), with an upstream-issue tracker template shipped in-repo.

## 9. Closing-note priorities (the four things that matter)

| Priority | Where honoured |
|---|---|
| 1. Phase 0 first | SP-02 sits in wave 1, before any behavior-changing component; baseline gates all later waves |
| 2. Phase 1 + 2 (store + canonicalization + tombstones + evidence-linked negative knowledge with staleness) | SP-04/SP-06/SP-08 + SP-03/SP-09 |
| 3. The cache correction (no selection before p-selection) | SP-12 (wave 3) precedes SP-15 (wave 4) by construction |
| 4. The incremental-span instruction (O1) | SP-10, riding with the checkpoint writer |

## 10. Verification checkpoints

| Checkpoint | After wave | Gates |
|---|---|---|
| V1 | 0 | Foundation, contracts, config verbatim vs Appendix C, hooks exit 0 |
| V2 | 1 | Primitives, store, DAG, Phase 0 baseline; bench-gate + replay-gate begin; tag v0.1.0 |
| V3 | 2 | Observer + negative knowledge; Phase 1 exit criterion (≥4:1, p99 < 15ms); Phase 2 exit criterion |
| V4 | 3 | Checkpoint/rehydrate/schedule/retrieval; Phase 3 + Phase 4 exit criteria; core loop round-trip |
| V5 | 4 | Commands, selection, grammar, Phase 5/6/7 exit criteria |
| V6 | 5 | Production readiness: packaging, security, docs honesty surface, hand-executed UAT-01..12, release gate |

## 11. Unowned design obligations — recorded, not assigned

Two obligations `Qompack.md` states are **not owned by any subplan in this set**. They are recorded
here rather than folded into the tables above, because assigning either one silently would make this
document assert coverage the plan set does not provide. Each needs a decision before the checkpoint
that would otherwise close its phase signs off. Nothing below is scheduled work. It is appended as
§11 rather than inserted, so that no cross-reference to `TRACEABILITY.md` §8, §9 or §10 elsewhere in
the plan set is renumbered by its addition.

### 11.1 O2's second half — the delta-vs-full storage write

**What the design says.** `Qompack.md` §8.1 item 1: a MinHash signature per result detects
near-duplicates — "same test suite, one new failure" — and "stores the delta against the prior
version instead of the full text… this is where the dedup ratio is won or lost."

**What the plan set says.** §5 above assigns O2 whole to SP-04. SP-04 disclaims half of it in its own
text (`V2-SP-04-chunking-canonicalization-and-symbols.md`): `canon.Decide` is "an **unconsumed
decision surface** reserved for the `Qompack.md` §8.1 delta-vs-full storage write… that write **is
not implemented anywhere after wave 1, and no subplan owns it**." `V2-SP-04-carried-defects.md`
records the intended fallback — "recording in `TRACEABILITY.md` that O2's second half is deliberately
not shipped" — which is what this subsection now does. What ships today is reporting only:
`store.NearDupInfo.DeltaBytes` measures the size difference between two versions and
`canon`'s dedup path estimates it; neither writes a delta.

**Why it matters.** The Phase 1 exit criterion `Qompack.md` §10 states for O2 is discharged in
`test/dedup/dedup_test.go` by a canonicalization ratio threshold, so the phase can close green with
the storage half absent, and the design's ratio claim rests on the absent half.

**The two options.** (a) Assign the write — SP-16 is the natural candidate, since its Phase-7
charter is already "fills a seam an earlier wave deliberately reserved"; that means a `store.Put`
delta path, an exit-criterion row, and O2's row in §5 gaining a second owner. (b) Declare it
not-shipped: keep §5's row as SP-04's with an explicit "second half not shipped" note, and add it to
SP-18's cannot-do / known-limitations surface, because the design ties the dedup ratio to it.
**Doing neither leaves §5 asserting coverage no plan provides.**

### 11.2 `observer.Tombstone` — G3.2's marker has no reader

**What the design says.** `Qompack.md` §8.1 item 2: "**Emit the tombstone.** Replace the eventual
cleared marker with an addressable one: `[cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts ·
re-expandable]` This alone closes G3.2 at near-zero cost." `Qompack.md` §12 also says the plugin "cannot modify
the message array directly — everything flows through `additionalContext`", so the literal
instruction is unexecutable and the only legal channel is a rehydration or tool payload.

**What the plan set says.** §1 above closes G3.2 on SP-08 ("addressable tombstones"). SP-08 ships and
tests `observer.Tombstone`, and assigns the *affordance line* `TombstoneNote()` to SP-11 and SP-13 —
but leaves the marker itself ownerless ("the compactability decision belongs to the caller"), and its
own runtime use of it is a counter. Every other call site in the tree is a test:
`V2-VERIFY` and `V3-VERIFY` exercise it, no production caller exists in any of the eighteen subplans.
SP-11's §8.6 injection list has no tombstone item, and SP-13's `dropped` tool returns the
checkpoint's `dropped[]` (path rules, nested CLAUDE.md, skills), not cleared tool results.

**The two options.** (a) Give it a consumer: add a bounded tombstone digest for tool results cleared
since the last checkpoint to SP-11's §8.6 injection list, rendered by `observer.Tombstone` and
budgeted alongside the pointer item, then name that delivery owner in §1's G3.2 row. (b) Declare it
undeliverable through `additionalContext`: move it to SP-18's cannot-do list and downgrade G3.2's
claimed closure. **A rendered string with no reader closes nothing, so §1's G3.2 row is currently
stronger than the plan set supports.**

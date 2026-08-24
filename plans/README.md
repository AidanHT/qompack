# Qompack build plan — master execution guide

**Qompack** is a Claude Code plugin for cache-aware, retrieval-backed, measurable context compaction: a durable, content-addressed memory layer that surrounds (never replaces) Claude Code's compaction pipeline — observing via hooks, checkpointing before every compaction, rehydrating after it, and measuring itself against a Belady-optimal ceiling. The full design is `Qompack.md` at the repository root; it is the canonical, read-only reference for every plan in this directory.

This `plans/` directory contains everything an AI programming code editor needs to build Qompack **end to end** — development first, production-ready at the end. `00-ARCHITECTURE.md` holds the global tech-stack decisions (Go), repo layout, interface contracts, and git strategy that all subplans conform to. `TRACEABILITY.md` proves the plan set preserves the entire design document.

**File naming.** Every plan carries its verification group as the filename prefix:

- `V<K>-SP-NN-<slug>.md` — an implementation subplan, with its own git branch, a 5–8 commit plan, TDD test plans, and measurable exit criteria. All subplans sharing a `V<K>-` prefix run **in parallel**.
- `V<K>-VERIFY-<slug>.md` — the checkpoint that gates group *K*, exhaustively re-testing **every functionality existing up to that point**.

So the directory listing is the execution order, every subplan sits next to the checkpoint that will test it, and `V4-SP-12-scheduler-l3.md` tells you at a glance that the scheduler is built in group 4 and verified by `V4-VERIFY-…`. The `SP-NN` numbers are stable IDs — plans, branches (`feat/sp<NN>-<slug>`), and `TRACEABILITY.md` all refer to subplans by ID, never by filename.

## Execution schedule

Subplans inside a wave are independent — run them **in parallel**, one AI-editor session per subplan, each session working only on its own branch. A wave's verification checkpoint runs only after all of the wave's branches are merged into `develop`. Wave *N* is verification group *N+1*: its files all carry the `V<K>-` prefix and are gated by that group's `V<K>-VERIFY-…` checkpoint.

| Wave | Group | Subplans (run in parallel) | Then verify | What exists after this wave |
|---|---|---|---|---|
| 0 | `V1` | SP-01 | `V1-VERIFY-foundation-and-contracts.md` | Initialized repo (`main` + `develop`), Go toolchain, CI, Appendix-C config loader, plugin manifest skeleton, no-op hooks exiting 0, `.qompack/` layout, contract-monitor skeleton, and the interface stubs that make the later waves parallelizable |
| 1 | `V2` | SP-02 · SP-03 · SP-04 · SP-05 · SP-06 · SP-07 | `V2-VERIFY-primitives-store-dag-and-baseline.md` | The full substrate: replay harness + Belady OPT + stock baseline (Phase 0), sketch library, FastCDC chunking + canonicalizers + MinHash, resident daemon/IPC hot path under the 15ms p99 budget, content-addressed store with the DPI guard, dependence DAG + slicing |
| 2 | `V3` | SP-08 · SP-09 | `V3-VERIFY-observer-and-negative-knowledge.md` | Live observation: every tool result chunked/stored/tombstoned, verbatim intent capture, subagent capture, supersession — plus evidence-linked negative knowledge with staleness (Phase 1 + Phase 2 complete) |
| 3 | `V4` | SP-10 · SP-11 · SP-12 · SP-13 | `V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md` | The complete core loop: durable importance-ordered checkpoints (with O1 span narrowing), 8–12K-budget rehydration with drop report, the composite scheduler (BOCD, Young–Daly, p-selection, idle model, O5 frontier), and all 8 MCP retrieval tools (Phase 3 + Phase 4 complete — the plugin is functionally useful from here) |
| 4 | `V5` | SP-14 · SP-15 · SP-16 | `V5-VERIFY-commands-selection-grammar-and-refinements.md` | The 7 slash commands and `/qompack:status` observability, Δ-scoring + suffix-constrained submodular selection + Sequitur thrash detection, and the Phase 7 refinements (warm start, promotion, per-segment Blooms, ski-rental) |
| 5 | `V6` | SP-17 · SP-18 | `V6-VERIFY-production-readiness-and-uat.md` | **Production**: packaged installable plugin, cross-platform validated, security-audited, fully documented (including the "cannot do" honesty surface), UAT guide hand-executed — released |

```
Wave 0        Wave 1                       Wave 2         Wave 3                  Wave 4            Wave 5
┌───────┐    ┌──────────────────────┐    ┌──────────┐    ┌─────────────────┐    ┌────────────┐    ┌──────────┐
│ SP-01 │───▶│ SP-02  SP-03  SP-04  │───▶│ SP-08    │───▶│ SP-10  SP-11    │───▶│ SP-14      │───▶│ SP-17    │
│       │    │ SP-05  SP-06  SP-07  │    │ SP-09    │    │ SP-12  SP-13    │    │ SP-15      │    │ SP-18    │
└───┬───┘    └──────────┬───────────┘    └────┬─────┘    └────────┬────────┘    │ SP-16      │    └────┬─────┘
    │                   │                     │                   │             └─────┬──────┘         │
   V1 ✓                V2 ✓                  V3 ✓                V4 ✓                V5 ✓             V6 ✓ → release
```

## How to run a wave

1. **Cut branches.** For every subplan in the wave, cut its branch from the current (verified) `develop`: branch names are in each subplan's header (`feat/sp<NN>-<slug>`). Same-wave branches never merge into or branch from each other.
2. **Run the subplans in parallel.** Open one AI-editor session per subplan and point it at the subplan file (e.g. "Implement plans/V2-SP-06-content-addressed-store.md exactly as written"). Heavy subplans contain a Subagent strategy section telling the session how to fan out its own subagents.
3. **Merge in order.** When all sessions finish, merge the wave's branches into `develop` with `--no-ff`, in this order:
   - Wave 1: SP-05 → SP-03 → SP-04 → SP-02 → SP-06 → SP-07
   - Wave 2: SP-09 → SP-08
   - Wave 3: SP-10 → SP-11 → SP-12 → SP-13
   - Wave 4: SP-15 → SP-16 → SP-14
   - Wave 5: SP-17 → SP-18
   Conflicts are resolved on the incoming branch and re-merged, never hand-edited into the merge commit.

   These orders are not arbitrary and are not free to reorder. **Wave 1's order is the one currently enforced as a test**, and it is the only one: `test/guards/buildorder_test.go` holds six `TestGuard_*` functions, of which exactly two are merge-order guards (closing notes 1 and 2); the other four encode closing notes 3 and 4 and constrain ship order and flag defaults, not the order branches merge in. `TestGuard_Phase0BeforeStore` fails while `internal/store` is real and `internal/eval` is still a stub, because closing note 1 of `Qompack.md` says a store that ships before its baseline can never be measured against one — that is the **SP-02 before SP-06** constraint, and merging wave 1 in the wrong order leaves `develop` failing at an intermediate commit even though every branch is individually green. Wave 1's other binding constraint, **SP-06 after SP-03 and SP-04**, comes from `V2-SP-06-content-addressed-store.md`'s exit criteria. Wave 1's row was corrected during that wave's merge, which is when the second constraint was discovered; see `V2-VERIFY-primitives-store-dag-and-baseline.md`'s `V2-MERGE-21`, which scopes the enforcement claim to wave 1 for the same reason.

   The second guard, `TestGuard_StoreAndNegknowBeforeCheckpoint`, is a **cross-wave** constraint, not an intra-wave one: `store` lands in wave 1 and `negknow` in wave 2, so every permutation of wave 3 satisfies it and it discriminates none of them. **The wave 2–5 orders above are stated conventions, not enforced ones.** Exactly one of them is stated in a subplan's own header — `V6-SP-17`'s, which says SP-18 merges **after** SP-17 so it documents the artifact that actually ships. The others follow from consuming relationships their plans describe in prose rather than in a prerequisites list. SP-10's checkpoint schema is what SP-11 rehydrates from. SP-09 freezes the `negknow` descriptor and record surface and the golden node-ID conventions (`testdata/golden/contracts/negknow/node-ids.json`) that SP-08's DAG naming is checked against, so landing the ledger first means the `SessionStart` staleness-refresh seam SP-08 wraps (`internal/daemon/observer_ops.go` — the only production caller SP-09's out-of-scope table hands to SP-08) and the wave-2 checkpoint are written against a real ledger rather than a stub — what does **not** order the two is an import: `internal/observer` does not import `internal/negknow` and is forbidden to (`V3-SP-08`'s Done checklist and `V6-VERIFY` row 1.8.15 both exclude the import; §3.2's allow-table merely *permits* the edge, and a permission is not a dependency). Wave 4 reads the same way: SP-15 and SP-16 both insert into the same `checkpoint.Finalize` body and SP-16's promotion must run after SP-15's grammar fold so it sees the final pointer set, which fixes SP-15 before SP-16; SP-14 is "a frontend and nothing else" over the analyzer, selector and Phase-7 surfaces the other two ship, so it merges last. But no test today distinguishes a wrong permutation of any of these orders, so a mis-ordered merge would be caught by review or not at all. **A wave whose order rests on a real constraint — one where a wrong permutation leaves `develop` failing at an intermediate commit — authors its own guard in `test/guards/buildorder_test.go` before its branches merge**, written and observed failing against the wrong order first, exactly as `TestGuard_Phase0BeforeStore` was; the wave's verification checkpoint then names that guard in its completion report. A wave whose order no guard can discriminate discharges the rule the other way: its checkpoint records `merge order unverified — no guard discriminates this wave's permutations` in the completion report, and the order is carried on review. Waves 2 through 5 are all in the second category today — none of the six guards above distinguishes any of their permutations — so their checkpoints owe the declaration, not a guard. Doing neither is not an option: every wave from 2 onward fills exactly one of those two cells.
4. **Verify.** Run that group's `V<K>-VERIFY-*.md` file in a fresh session on branch `verify/v<K>` cut from `develop`. It re-tests the cumulative functionality inventory, re-verifies every exit criterion so far, authors this wave's new integration tests, re-validates all performance budgets, and re-runs all prior checkpoints' inventories under the 2% no-regression rule. Fixes land on the verify branch; the checkpoint re-runs from the top until green.
5. **Gate.** Only when the checkpoint is fully green: merge `verify/v<K>` into `develop` (`--no-ff`), tag per `00-ARCHITECTURE.md` (e.g. `v0.1.0` after V2), and only then cut the next wave's branches. At V6, `develop` merges into `main` with the release semver — the project is production-ready for user testing.

## Global rules (restated from every subplan)

- **Branching:** each subplan works only on its own branch; prerequisites are consumed as already-merged code on `develop`.
- **Commits:** 5–8 commits per subplan, conventional-commit messages as enumerated in each commit plan. **Do not add Co-Authored-By lines or any attribution trailers to any commit message.** (This applies to merge commits, tags, and PR bodies too.)
- **TDD:** within each commit, tests are written and observed failing before implementation, then observed passing.
- **No placeholders:** every subplan is fully decided; if an implementer hits an ambiguity, the subplan is defective — fix the plan, don't improvise silently.
- **Subagents:** subplans marked heavy prescribe how the implementing session partitions work across parallel subagents.
- **`Qompack.md` is read-only to subplans.** It is the design of record; plans quote it verbatim rather than paraphrasing it. It is revised only **deliberately and as a version**, with an entry in its own Revision log and a record in **`QOMPACK-ERRATA.md`** — never edited in passing by a subplan, and never to record something that belongs in a report. It stands at **v1.3** (2026-08-23). Between revisions its numbers are still allowed to move without its text moving, which is what D11/§11.6 are for: `r`, `w` and the TTL are config keys under a lint gate rather than constants, and §5.1 tells its reader to *"verify against current pricing before tuning"* rather than trusting the figures printed beside the instruction.

## File index

| File | What it is |
|---|---|
| `QOMPACK-ERRATA.md` | The verification record for `Qompack.md`: what each revision checked, what held unchanged, what moved, and what was deliberately not verified. Covers v1.3 (2026-08-23) |
| `00-ARCHITECTURE.md` | Global tech stack (Go), repo layout, module interface contracts, test/CI infrastructure, git + merge + verification strategy |
| `V1-SP-01-foundation-toolchain-and-contracts.md` | Wave 0 — repo init, toolchain, CI, config loader, manifest, no-op hooks, interface stubs |
| **`V1-VERIFY-foundation-and-contracts.md`** | **Checkpoint gating group V1** (wave 0) |
| `V2-SP-02-replay-harness-belady-baseline.md` | Wave 1 — Phase 0/L7: replay harness, divergence metrics, Belady OPT, stock baseline |
| `V2-SP-03-sketch-library.md` | Wave 1 — Bloom, Count-Min, HyperLogLog, Misra-Gries, MinHash with versioned serialization |
| `V2-SP-04-chunking-canonicalization-and-symbols.md` | Wave 1 — FastCDC chunker, per-tool canonicalizers + MinHash near-dedup (O2), symbol extractor |
| `V2-SP-05-daemon-ipc-and-hot-path.md` | Wave 1 — resident daemon, IPC, thin hook client, <15ms p99 budget, queue-and-drain, contract monitor |
| `V2-SP-06-content-addressed-store.md` | Wave 1 — L1 store: sha256/zstd objects, Merkle roots, file history, segment log (DPI guard), GC |
| `V2-SP-07-dependence-dag-and-slicing.md` | Wave 1 — dependence DAG, backward/thin slicing, segment coupling |
| **`V2-VERIFY-primitives-store-dag-and-baseline.md`** | **Checkpoint gating group V2** (wave 1) — bench-gate + replay-gate begin; tag `v0.1.0` |
| `V3-SP-08-observer-l0.md` | Wave 2 — L0 observer: all hook handlers, addressable tombstones, supersession, verbatim + subagent capture |
| `V3-SP-09-negative-knowledge.md` | Wave 2 — Phase 2: eliminations with `depends_on` staleness, bloom-as-cache, three-way response |
| **`V3-VERIFY-observer-and-negative-knowledge.md`** | **Checkpoint gating group V3** (wave 2) — Phase 1 + 2 exit criteria |
| `V4-SP-10-checkpointer-l4.md` | Wave 3 — L4: importance-ordered checkpoint schema, PreCompact, pins, focus instructions + O1 |
| `V4-SP-11-rehydrator-l5.md` | Wave 3 — L5: compact-source rehydration, instruction restoration, skill index, drop report, 8–12K budget |
| `V4-SP-12-scheduler-l3.md` | Wave 3 — L3: composite trigger, p-selection, BOCD, Young–Daly, idle model (E1/O3), O5 frontier |
| `V4-SP-13-mcp-retrieval-layer.md` | Wave 3 — L6: stdio MCP server, all 8 tools, ephemeral-at-birth, minimal span, promotion |
| **`V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md`** | **Checkpoint gating group V4** (wave 3) — Phase 3 + 4 exit criteria; core-loop round-trip |
| `V5-SP-14-slash-commands-and-observability.md` | Wave 4 — the 7 slash commands and the `/qompack:status` surface |
| `V5-SP-15-analyzer-selection-and-grammar.md` | Wave 4 — Phases 5+6: Δ-scoring, suffix-constrained submodular greedy, Sequitur + thrash warnings |
| `V5-SP-16-phase7-refinements.md` | Wave 4 — Phase 7: warm start (O4), demand-driven promotion, per-segment Blooms, ski-rental, truncation tuning |
| **`V5-VERIFY-commands-selection-grammar-and-refinements.md`** | **Checkpoint gating group V5** (wave 4) — Phase 5/6/7 exit criteria |
| `V6-SP-17-packaging-hardening-and-release.md` | Wave 5 — production packaging, cross-platform validation, security/fault audit, release pipeline |
| `V6-SP-18-documentation-and-uat.md` | Wave 5 — user docs, config reference, cannot-do list, upstream issues, UAT-01..12 guide |
| **`V6-VERIFY-production-readiness-and-uat.md`** | **Checkpoint gating group V6** (wave 5) — the release gate, including hand-executed UAT |
| `TRACEABILITY.md` | Proof that every gap, phase, layer, tool, metric, risk mitigation, and revision item in `Qompack.md` is owned by a subplan and re-tested by a checkpoint |
| `OWNERS.tsv` | Package → owner, §6.4 coverage floor, stub probe. Read by `devtool` (`stubskips`, `cover`, `gen-contract-fixtures`); a package on disk with no row here fails `go test ./test/guards -run TestV1_StubGraphIsInertAndOwned` |
| `CARRIED-DEFECTS.tsv` | Defects carried across waves, each with a resolver. A row still unresolved when its resolver's `V<K>-report.md` exists fails `test/guards/carrieddefects_test.go` — read this before signing off any wave |
| `V<K>-report.md` | The completion report a checkpoint session writes at wave sign-off; its existence is what arms the carried-defects gate above |
| `sdd/` | Session decision records — the rulings an implementing session had to make where its plan and the shipped code disagreed. Not design; the audit trail for the "fix the plan, don't improvise silently" rule above. See `sdd/README.md` |

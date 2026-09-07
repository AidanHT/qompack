# Qompack build plan — master execution guide

**Status:** planning revision v1.5 with merge-prerequisite addendum, 2026-09-06. Waves 0–2 remain implemented; the user now reports original Wave 3 SP-10–13 complete. Preserve their completion history. Combined integration and revised migration verification remain unverified here. No implementation was performed in this planning revision.

Qompack remains the Go Claude Code plugin described by [Qompack.md](../Qompack.md). It preserves captured evidence, builds compatible durable checkpoints and supplies tested retrieval and bounded additional context. Native-history edits, cache markers, guaranteed native compaction savings and universal Belady task ceilings are excluded. [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) records evidence states and the approved M0–M7 dependency blueprint.

**Planning-only boundary:** only allowlisted Markdown planning files change. All commands, branches, worktrees, commits, tests, subagents, flags and data changes described here are future implementation work requiring separate authorization. Reports, session records, instruction/config files, generated docs/assets and machine-read TSV files are protected. The baseline includes unrelated untracked coverage/benchmark artifacts; a clean-tree requirement cannot justify deleting them.

**File naming.** Existing `V<K>-SP-NN-<slug>.md`, `V<K>-VERIFY-<slug>.md`, branch `feat/sp<NN>-<slug>`, and `verify/v<K>` conventions remain. SP numbers are stable. New SP-19/SP-20 are prerequisites within Wave 3's migration scope; SP-21 is a later opt-in extension, not a renumbering. Same-wave work is parallel only when dependencies and file ownership permit.

## Execution schedule

| Wave | Group | Subplans | Verification and observed status |
|---|---|---|---|
| 0 | V1 | SP-01 | Completed historical foundation; preserve V1 report |
| 1 | V2 | SP-02–07 | Completed substrate/evaluation; preserve V2 report and baselines |
| 2 | V3 | SP-08–09 | Completed under user's 2026-08-26 J5 CI waiver; backfill obligation remains |
| 3 | V4 | User-completed SP-10–13 → SP-19 M0-00 merge prerequisite → remaining SP-19 → SP-20 and necessary follow-ups | Original completion preserved; M0-G0 and revised V4 migration gates require future evidence |
| 4 | V5 | SP-15 → SP-16 → SP-14; SP-21 admission after M1–M3 | Future integrated verification; admission evaluated separately and off by default |
| 5 | V6 | SP-17 → SP-18 | Future packaging/UAT/release and rollback gate |

The historical phase mapping is retained: Phase0→SP02/V2; Phase1→SP04/06/V2 plus SP08/V3; Phase2→SP03/09 with SP11/13 consumers; Phase3→SP10/11/V4; Phase4→SP12/V4; Phases5/6→SP15/V5 with completed SP07; Phase7→SP16/V5. Phase numbers are not waves.

Future migration order: merge the completed original SP-10 → SP-11 → SP-12 → SP-13 deliveries and accept SP-19 M0-G0 → remaining M0 reconciliation → M1 durable capture/publication → M2 retrieval/current state → M3 corrective checkpoint/rehydration work → M4 admission and M5 scheduling/selection → M6 warnings/reuse → M7 integrated release. Evaluation, privacy, compatibility and failure testing occur in each package. No optimization can ship a pointer whose evidence is not recoverable.

## How to run a wave

1. **Merge completed Wave 3 deliveries first.** [SP-19 M0-00](V4-SP-19-migration-reconciliation.md#m0-00-merge-completed-sp-1013-before-the-rest-of-sp-19) first records the nominated delivery HEADs, owners, dirt and prior results, then integrates SP-10 → SP-11 → SP-12 → SP-13 into `develop` under architecture §9. Preserve existing branches and completed work. Resolve conflicts on the incoming branch, review shared semantics even without textual conflicts, validate each integration and the combined tree, and retain independent acceptance evidence. Already-integrated deliveries require ancestry and behavior review, not duplicate merges. Only prerequisite inventory/review/integration work may run until M0-G0 passes.
2. **Then begin the rest of SP-19.** Cut its proposed branch from the accepted combined baseline. M0-01–05, commits 1–8 and `arch/migration-contracts` wait for M0-G0. Agree versioned readers, retained hashes/fixtures, authority, publication/frontier and response envelopes before dependent remediation, then run SP-20 and the necessary SP-10–13 follow-ups. Each source file has one future editor; completed implementation is not repeated.
3. **Integrate subsequent work in dependency order.** Wave1's recorded order SP05→SP03→SP04→SP02→SP06→SP07 remains history. Wave2's actual merge/waiver is preserved. Wave3's original deliveries are combined before migration remediation; this is not deployment or proof of M1–M3 recovery. Keep dependent features disabled pending their gates, and avoid a package cycle by sharing recovery identities through interfaces/composition roots. Full revised V4 verification follows corrective work, not M0-G0. Wave4 convention SP15→SP16→SP14 remains, with SP21 integrated after its own recovery gate and before V5 admission evaluation. Wave5 SP17→SP18 remains.
4. **Verify with actual evidence.** Future `verify/v4`, `verify/v5`, `verify/v6` checkpoint plans retain cumulative regression coverage and add migration gates. A skipped installed-host test remains unverified with a reason. Record old metric definitions beside corrected measurements; twenty runs do not prove a two-percent guarantee.
5. **Review, gate and rollback.** No future release/feature activation without the applicable recovery/compatibility/quality gates. Record discriminating merge-order guard or the existing declaration that no guard distinguishes permutations; ordering review is not runtime verification. No branch/worktree/Git mutation is performed during this documentation pass.

## Global rules (restated from every subplan)

- **Preserve progress.** No reset/stash/overwrite of active work. Check the current target before each edit. Historical reports and completed checkboxes are immutable during planning.
- **Future commits:** 5–8 small conventional commits per new/remediation slice, with contract/tests, compatible implementation, integration, migration/rollback evidence and handoff. Existing commit identifiers are historical plan references, not instructions to repeat completed commits. No Co-Authored-By or attribution trailers.
- **Future TDD and validation:** inspect existing tests before adding meaningful failure cases; keep old fixtures/readers compatible. Runtime validation occurs only in a separate implementation session.
- **Unknowns:** name the owner, verification action, consequence and fallback. An unavailable capability stays disabled/unverified; do not invent a default and label it proven.
- **Planning team:** requested Astra coordinator, two Luna-low scouts, three Terra-medium writers and fresh Terra-high semantic reviewer, reused Luna structural scout. Maximum three active children, no nested delegation/config changes; requested/effective routing and unavailable usage are distinguished in the ledger.
- **Future implementation subagents:** retain each subplan's roles/file sets and sequential integration; these are not the P-stage planning team.
- **Design revisions:** `Qompack.md` is ordinarily read-only to subplans. This user-authorized v1.5 revision deliberately updates it with its Revision log and `QOMPACK-ERRATA.md`. Future implementers cannot silently redefine it or regenerate frozen assets to erase failures.
- **Product boundary:** Claude Code integration remains; Codex/GPT models produce these plans only.

## File index

| File | What it is |
|---|---|
| `QOMPACK-ERRATA.md` | The verification record for `Qompack.md`: what each revision checked, what held unchanged, what moved, and what was deliberately not verified. Preserves v1.3 (2026-08-23) and v1.4 (2026-08-26) and adds planning-only v1.5 (2026-09-06, drafted as "v1.4" before SP-11's v1.4 merged) |
| `00-ARCHITECTURE.md` | Global tech stack (Go), repo layout, module interface contracts, test/CI infrastructure, git + merge + verification strategy |
| `V1-SP-01-foundation-toolchain-and-contracts.md` | Wave 0 — repo init, toolchain, CI, config loader, manifest, no-op hooks, interface stubs |
| **`V1-VERIFY-foundation-and-contracts.md`** | **Checkpoint gating group V1** (wave 0) |
| `V2-SP-02-replay-harness-belady-baseline.md` | Wave 1 — Phase 0/L7: replay harness, divergence metrics, Belady OPT, stock baseline |
| `V2-SP-03-sketch-library.md` | Wave 1 — Bloom, Count-Min, HyperLogLog, Misra-Gries, MinHash with versioned serialization |
| `V2-SP-04-chunking-canonicalization-and-symbols.md` | Wave 1 — FastCDC chunker, per-tool canonicalizers + MinHash near-dedup (O2), symbol extractor |
| `V2-SP-05-daemon-ipc-and-hot-path.md` | Wave 1 — resident daemon, IPC, thin hook client, measured p99 targets, queue-and-drain, contract monitor |
| `V2-SP-06-content-addressed-store.md` | Wave 1 — L1 store: sha256/zstd objects, Merkle roots, file history, segment log (provenance guard), GC |
| `V2-SP-07-dependence-dag-and-slicing.md` | Wave 1 — dependence DAG, backward/thin slicing, segment coupling |
| **`V2-VERIFY-primitives-store-dag-and-baseline.md`** | **Checkpoint gating group V2** (wave 1) — bench-gate + replay-gate begin; tag `v0.1.0` |
| `V3-SP-08-observer-l0.md` | Wave 2 — L0 observer: hook handlers and stored tombstone rendering, supersession, verbatim + subagent capture |
| `V3-SP-09-negative-knowledge.md` | Wave 2 — Phase 2: eliminations with `depends_on` staleness, bloom-as-cache, three-way response |
| **`V3-VERIFY-observer-and-negative-knowledge.md`** | **Checkpoint gating group V3** (wave 2) — Phase 1 + 2 exit criteria |
| `V4-SP-10-checkpointer-l4.md` | Wave 3 — L4: importance-ordered checkpoint schema, PreCompact, pins, lifecycle and recovery |
| `V4-SP-11-rehydrator-l5.md` | Wave 3 — L5: compact-source rehydration, instruction restoration, skill index, qualified coverage report, complete-record additional-context budget |
| `V4-SP-12-scheduler-l3.md` | Wave 3 — L3: supported cadence, observations and Qompack frontier |
| `V4-SP-13-mcp-retrieval-layer.md` | Wave 3 — L6: stdio MCP server, all 8 tools, future-representation hints, minimal span, promotion |
| **`V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md`** | **Checkpoint gating group V4** (wave 3) — Phase 3 + 4 exit criteria; core-loop round-trip |
| `V5-SP-14-slash-commands-and-observability.md` | Wave 4 — the 7 slash commands and the `/qompack:status` surface |
| `V5-SP-15-analyzer-selection-and-grammar.md` | Wave 4 — Phases 5+6: Δ-scoring, budgeted representation selection, Sequitur + thrash warnings |
| `V5-SP-16-phase7-refinements.md` | Wave 4 — Phase 7: warm start (O4), demand-driven promotion, per-segment Blooms, bounded reuse policy, complete-record budgets |
| **`V5-VERIFY-commands-selection-grammar-and-refinements.md`** | **Checkpoint gating group V5** (wave 4) — Phase 5/6/7 exit criteria |
| `V6-SP-17-packaging-hardening-and-release.md` | Wave 5 — production packaging, cross-platform validation, security/fault audit, release pipeline |
| `V6-SP-18-documentation-and-uat.md` | Wave 5 — user docs, config reference, cannot-do list, upstream issues, UAT-01..12 guide |
| **`V6-VERIFY-production-readiness-and-uat.md`** | **Checkpoint gating group V6** (wave 5) — the release gate, including hand-executed UAT |
| `TRACEABILITY.md` | Traceability of every gap, phase, layer, tool, metric, risk mitigation, and revision item in `Qompack.md` is owned by a subplan and re-tested by a checkpoint |
| `OWNERS.tsv` | Package → owner, §6.4 coverage floor, stub probe. Read by `devtool` (`stubskips`, `cover`, `gen-contract-fixtures`); a package on disk with no row here fails `go test ./test/guards -run TestV1_StubGraphIsInertAndOwned` |
| `CARRIED-DEFECTS.tsv` | Defects carried across waves, each with a resolver. A row still unresolved when its resolver's `V<K>-report.md` exists fails `test/guards/carrieddefects_test.go` — read this before signing off any wave |
| `V<K>-report.md` | The completion report a checkpoint session writes at wave sign-off; its existence is what arms the carried-defects gate above |
| `sdd/` | Session decision records — the rulings an implementing session had to make where its plan and the shipped code disagreed. Not design; the audit trail for the "fix the plan, don't improvise silently" rule above. See `sdd/README.md` |
| `MIGRATION-EVIDENCE.md` | Versioned inventory, capability/evidence register, blueprint, P-stage routing, future scenario matrix and review |
| `V4-SP-19-migration-reconciliation.md` | M0 prerequisite reconciliation, host canaries, baseline/config accounting compatibility |
| `V4-SP-20-capture-storage-and-state-remediation.md` | M1/M2 remediation for durable capture, legacy migration, authority and elimination uncertainty |
| `V5-SP-21-deterministic-admission-control.md` | M4 opt-in output admission after M1–M3 recovery gates |

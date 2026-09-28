# Traceability — Qompack.md → subplan set

**Status:** v1.5 planning map, 2026-09-06. Preserves 40 unique G-numbered IDs across 10 categories and original phase/revision IDs. This maps future responsibilities; it does not certify runtime closure. [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) is the single capability/source/decision register and includes the M0–M7 package map.

## 1. Gap → owning subplan (mirrors Qompack.md §9)

Each row has one accountable migration owner. Completed supporting primitives remain in place; root/sibling code is `implemented_unverified` in this planning review unless an inspected historical artifact is explicitly cited. All gates below are future. Host-only controls remain unsupported or disabled; partial mechanisms are not unconditional closure.

| Gap | Concern | Owner | Mechanism | Future gate | Residual |
|---|---|---|---|---|---|
| G1.1 | Task-blind timing | [SP-12](V4-SP-12-scheduler-l3.md) | Cadence/dirty maintenance and aged observations | M5 / scheduler acceptance | Native request/cut/veto unsupported or disabled |
| G1.2 | Timing agency | [SP-12](V4-SP-12-scheduler-l3.md) | Cadence/dirty maintenance and aged observations | M5 / scheduler acceptance | Native request/cut/veto unsupported or disabled |
| G1.3 | Headroom estimates | [SP-12](V4-SP-12-scheduler-l3.md) | Cadence/dirty maintenance and aged observations | M5 / scheduler acceptance | Native request/cut/veto unsupported or disabled |
| G1.4 | Blocking-limit recovery | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G1.5 | Task-boundary signals | [SP-12](V4-SP-12-scheduler-l3.md) | Cadence/dirty maintenance and aged observations | M5 / scheduler acceptance | Native request/cut/veto unsupported or disabled |
| G2.1 | Derivative provenance | [SP-20](V4-SP-20-capture-storage-and-state-remediation.md) | Capture fidelity, durable state, exact applicability | M1/M2 / publication/state acceptance | Unknown originals/dependency coverage stay unknown |
| G2.2 | Durable pins | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G2.3 | User-intent history | [SP-20](V4-SP-20-capture-storage-and-state-remediation.md) | Capture fidelity, durable state, exact applicability | M1/M2 / publication/state acceptance | Unknown originals/dependency coverage stay unknown |
| G2.4 | Typed state | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G2.5 | Evidence validation | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G2.6 | Session-memory drift | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G3.1 | Historical retrieval | [SP-13](V4-SP-13-mcp-retrieval-layer.md) | Scoped exact/history recovery and error envelope | M2 / retrieval acceptance | Installed archive operations unverified |
| G3.2 | Addressable result handles | [SP-21](V5-SP-21-deterministic-admission-control.md) | Capture-before-replacement, tested schema allowlist | M4 / T21 admission acceptance | New outputs only; off until recovery gate |
| G3.3 | Budgeted additional context | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G3.4 | Essential exact spans | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G4.1 | Path-scoped rules | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G4.2 | Nested instructions | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G4.3 | Partial skill coverage | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G4.4 | Skill discovery | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G4.5 | Coverage report | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G5.1 | Native cut limitations | [SP-12](V4-SP-12-scheduler-l3.md) | Cadence/dirty maintenance and aged observations | M5 / scheduler acceptance | Native request/cut/veto unsupported or disabled |
| G5.2 | Cache-cost observability | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G5.3 | Heterogeneous evidence | [SP-15](V5-SP-15-analyzer-selection-and-grammar.md) | Feasible representations and state loop warnings | M5/M6 / selection and warning acceptance | Approximate graph/behavior signals are not correctness |
| G6.1 | Elimination records | [SP-20](V4-SP-20-capture-storage-and-state-remediation.md) | Capture fidelity, durable state, exact applicability | M1/M2 / publication/state acceptance | Unknown originals/dependency coverage stay unknown |
| G6.2 | Repeated failed approaches | [SP-13](V4-SP-13-mcp-retrieval-layer.md) | Scoped exact/history recovery and error envelope | M2 / retrieval acceptance | Installed archive operations unverified |
| G6.3 | Retention of applicable negative knowledge | [SP-15](V5-SP-15-analyzer-selection-and-grammar.md) | Budgeted selection uses SP-20 authority/applicability and preserves recoverable elimination evidence | M5 / M5-G15-A/B/C | No universal highest-value claim; unknown applicability remains uncertain |
| G7.1 | Compaction request cost | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G7.2 | Summarizer model control | [SP-18](V6-SP-18-documentation-and-uat.md) | Evidence-matched limits/UAT documentation | M7 / V6 documentation gate | No plugin control over summarizer model |
| G7.3 | Intent recovery | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |
| G7.4 | Failed compaction recovery | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G7.5 | Empty or unexpected summary | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G7.6 | Repeated compaction failures | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G8.1 | Task and recovery metrics | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G8.2 | Summary uncertainty | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G8.3 | Feedback and accounting | [SP-14](V5-SP-14-slash-commands-and-observability.md) | Commands show qualified evidence and usage | M7 / command acceptance | Unavailable telemetry remains unknown |
| G9.1 | Durable checkpoints | [SP-10](V4-SP-10-checkpointer-l4.md) | Compatible checkpoint, durable frontier, local attempt | M3 / T10 lifecycle and compatibility | Native summary/model compliance unknown |
| G9.2 | Packaged recovery layer | [SP-17](V6-SP-17-packaging-hardening-and-release.md) | Installed package and reversible upgrade | M7 / V6 release gate | Platforms/package identifiers not certified |
| G9.3 | Host contracts | [SP-19](V4-SP-19-migration-reconciliation.md) | Separate capability register and request ledger | M0 / M0-G1–G6 | Target canaries and J5 backfill future |
| G10.1 | Subagent capture coverage | [SP-20](V4-SP-20-capture-storage-and-state-remediation.md) | Capture fidelity, durable state, exact applicability | M1/M2 / publication/state acceptance | Unknown originals/dependency coverage stay unknown |
| G10.2 | Assembled token estimation | [SP-11](V4-SP-11-rehydrator-l5.md) | Whole-record additional context and qualified coverage | M3 / T11 recovery, authority and scope | Native context/load bytes not certified |

## 2. Build phases (Qompack.md §10) → subplans

| Original phase | Repository placement and status | Migration disposition |
|---|---|---|
| 0 measurement | SP02, Wave1/V2, completed | SP19 + V4 VERIFY preserve baseline, resolve SP02-D1–D6 with corrected metrics |
| 1 store/observer | SP04/SP06 Wave1/V2, SP08 Wave2/V3, completed | SP20 focused fidelity/publication remediation; no restart |
| 2 negative knowledge | SP03/SP09 completed; SP11/SP13 active consumers | SP20 authority/coverage and SP13 error compatibility |
| 3 checkpoint/rehydrate | SP10/SP11 Wave3/V4, completed and merged at SP-19 M0-00 (66549ce); revised V4 migration gates future | M3 depends on M1/M2 recovery before pointer enablement |
| 4 scheduler | SP12 Wave3/V4, completed and merged at SP-19 M0-00 (66549ce); revised V4 migration gates future | M5 supported cadence and future representations |
| 5 selection | SP07 completed graph, SP15 Wave4/V5 | M5 owns actual budget selection integration; SP11 consumes agreed contract |
| 6 grammar/loops | SP15 Wave4/V5 | M6 warning-only state/progress criteria |
| 7 refinements | SP16 Wave4/V5 | M6 scoped reuse; optional experiments nonblocking |
| Production | SP17→SP18 Wave5/V6 | M7 installed package, UAT and rollback |
| Added remediation/extension | SP19/SP20 within V4; SP21 within V5 | Logical M packages do not renumber waves |

## 3. Layers (Qompack.md §7.2) → subplans

L0 remains implemented SP05/SP08, remediated by SP20. L1 remains SP06/SP04, remediated by SP20. L2 remains SP09/SP07 plus future SP15, with state remediation SP20. L3 is merged SP12; L4 merged SP10; L5 merged SP11; L6 merged SP13 (all four at SP-19 M0-00, 66549ce; corrective M1–M3 follow-ups pending) and future SP14. L7's implemented SP02 harness is extended through SP19 and V4/V5/V6 verification. SP21 is explicitly a new-result admission extension, not a passive-observer redefinition.

## 4. Hook surface (§7.3), MCP tools (§8.7), slash commands (§7.5)

| Surface | Accountable future owner / contract |
|---|---|
| Existing observation and user/subagent capture | SP20 remediation; preserve observed event/relationship gaps |
| PreCompact checkpoint and optional PostCompact observation | SP10; custom instructions are input, no output-setter dependency |
| SessionStart compact reinjection; instruction-load coverage | SP11; no PostCompact wait, native byte/absence inference or automatic pointer-directory rule injection |
| Todo/test/git/task signals and worker | SP12; Qompack cadence only unless a separate control is validated |
| SessionEnd flush/GC | SP20; not sole cleanup/recovery path |
| Eight MCP names recall/expand/re_read/already_tried/record_eliminated/timeline/why/dropped | SP13; protocol compatibility, authorization before previews/expansion, unavailable distinct from absence |
| Six command names status/recall/pin/why/dropped/eval (`checkpoint` removed for 0.3.0 by D36, 2026-09-27; SP14-M3-01 retired with it and returns with any future checkpoint-now route) | SP14; reuse APIs, evidence-qualified coverage and usage |
| New-result replacement | SP21; off until capture/recovery/schema tests, deterministic allowlist |
| Manifest/install/tool discovery | SP19 canaries, SP17 installed release validation |

## 5. Revision-log items (v1.1 / v1.2) → subplans

| ID | Revised disposition | Owner / gate / fallback |
|---|---|---|
| E1 | Sliding TTL is documented motivation, arbitrary tool time not exact state | SP12 M5; unknown observations disable dependent policy |
| E1a | Regime/request attribution is scope/category-bound | SP19 M0 request ledger; no billing inference from env |
| E1b | Retire guaranteed pre-expiry savings/native trigger | SP12 M5; advisory cadence baseline |
| E2 | Native cache markers remain unsupported | SP18 M7 limitations; no port required |
| GA | Keep conditional exact elimination records, add uncertain coverage | SP20 M2; exact confirmation/no filter veto |
| GB | Bound future retrieval representations; ephemeral tags cannot evict native history | SP13 M2 and SP16 demand policy; failure returns qualified error |
| GC | Preserve evidence and derivative provenance, exclude own wrappers | SP10 M3 with SP20 fidelity; unknown originals not invented |
| O1 | Retire PreCompact output setter and physically shortened native-input claim | SP10 M3; supported local checkpoint only |
| O2 | Keep normalized search/dedup as derivative; optional exact deltas require round-trip | SP20 M1; retained full original/legacy unknown fallback |
| O3 | Bounded lifecycle-managed maintenance, not free idle compute | SP12 M5; cancellation/quota/crash gate |
| O4 | Scoped expiring reuse, no unrelated project/session intent import | SP16 M6; cold/empty reuse fallback |
| O5 | Incremental Qompack frontier work only, no native O(delta) guarantee | SP10/SP12, accountable SP12 M5; measured local work |

## 6. Evaluation methodology (§11) and guardrails

[MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) owns the mandatory 13-area future scenario matrix. V4 validates capture/migration/retrieval/state/lifecycle continuously with SP19/SP20/SP10–13; V5 adds admission, selection, warnings and reuse; V6 consolidates release, closed-loop and rollback evidence.

Primary outcomes: task completion, constraint/regression failures and evidence recoverability. Cost/latency/context/repeated-work/retrieval/CPU/storage/tails accompany them. Preserve file/action divergence and Belady metrics as historical diagnostics. Corrected request accounting separates reported usage, estimated rates and invoiced cost; failed/aborted trials and missing telemetry stay visible. Predeclare margins/sample rationale, held-out tasks and baseline variation. No fixed 2% guarantee from twenty trials, no universal sublinear/4:1/15ms claim.

## 7. Risk register (§12) plugin-actionable mitigations

SP20 owns raw/derived fidelity, object/index publication, legacy migration, GC/leases/retention and authority uncertainty. SP13 owns retrieval scope/preview/expansion, path/symlink/decompression and old MCP caller compatibility. SP10/SP11 own bounded lifecycle/current-state recovery and qualified coverage. SP12/SP15/SP16 own unknown observations/feasible selection and warning/reuse limits. SP21 owns malformed/recursive/coexisting-hook replacements and opt-in pass-through. SP17 owns installed compatibility and independent kill switches/backup rollback. Each owning subplan specifies its future artifacts and disable/degraded outcome.

## 8. Honesty surface

SP18 documents actual supported capabilities and residuals from the current register, not verbatim obsolete gap-closed promises. UAT-01–12 remain identifiers but criteria are revised to observable task/recovery/scoping behavior. No fixed-count source quotations, unclaimed package-name assertion, native context certainty or secure physical erasure promise.

## 9. Closing-note priorities (the four things that matter)

1. Preserve completed baseline and active work; reconcile M0.
2. Repair capture/publication and trusted recoverable state M1/M2.
3. Complete current Wave3 checkpoint/rehydration M3.
4. Evaluate opt-in admission and measured policies only after recovery; consolidate release/rollback evidence.

## 10. Verification checkpoints

V1/V2/V3 reports remain historical records, including the V3 J5 waiver/backfill. V4/V5/V6 plans remain future gates, not completion reports. Future release scope matches actual installed-host evidence; skipped integration is unverified. M7 is continuous evaluation in earlier packages plus final consolidation, not deferred testing of unsafe features.

## 11. Unowned design obligations — recorded, not assigned

The heading is retained for existing links. v1.5 assigns the two previously unowned concerns below; no feature is marked implemented by that assignment.

### 11.1 O2's second half — the delta-vs-full storage write

Earlier TRACEABILITY called this unowned; V3's carry table records SP04-D7 fixed by assigning the checkpointer consumer. Preserve that history. SP20 now owns compatible exact-delta/fidelity/publication remediation; SP10 consumes its durable identities. Similarity and `NearDupInfo.DeltaBytes` are not exact reconstruction evidence. Full retained content is the fallback; no missing original may be synthesized.

### 11.2 `observer.Tombstone` — G3.2's marker has no reader

SP08's stored marker rendering alone did not establish delivered native replacement. SP11/SP13 expose qualified archive handles in supported injections/results; SP21 explicitly owns new-result admission after M1–M3. Native historical cleared markers remain outside plugin control. Pass only with real installed handle discovery/resolution and schema tests; never infer a deleted native result from a marker string.

# Qompack

**A Claude Code plugin for evidence-backed checkpointing and retrieval.**

*Design document v1.5 — planning revision, 2026-09-06.*

## 0. Executive summary

Qompack preserves captured evidence outside the native conversation and makes it available through tested Claude Code hooks and retrieval tools. The migration repairs fidelity, publication, authority and recovery contracts before enabling output reduction. It does not replace Claude Code, rewrite native history, introduce OpenAI calls, or port the plugin to Codex.

The user now reports original Wave 3 SP-10–13 complete, superseding the initial in-progress statement. This revision preserves their completion history and existing worktrees. Root `verify/v3` is an older integration snapshot; its stubs do not justify restarting implementations. SP-19 must first merge the completed deliveries in order and accept the combined baseline at M0-G0 before its remaining migration tasks begin. Combined integration and revised migration gates remain unverified here. [The evidence ledger](plans/MIGRATION-EVIDENCE.md) records snapshots, capabilities, uncertainty, decisions and review. [The master execution guide](plans/README.md) maps future work to actual subplans.

This is a Markdown-only planning revision. Every proposed code change, branch, worktree, commit, capability probe, test, migration, flag, rollout and rollback is future work requiring separate authorization. No runtime verification was executed for this revision. The name is retained; package/command availability must be checked by SP-17 before release.

## Table of contents

1. [The problem](#1-the-problem)
2. [Current architecture — what we are building around](#2-current-architecture--what-we-are-building-around)
3. [Complete gap inventory](#3-complete-gap-inventory)
4. [Theoretical reframe](#4-theoretical-reframe)
5. [The prompt-cache cost model](#5-the-prompt-cache-cost-model)
6. [Algorithmic toolkit](#6-algorithmic-toolkit)
7. [Plugin architecture](#7-plugin-architecture)
8. [Component specifications](#8-component-specifications)
9. [Gap traceability matrix](#9-gap-traceability-matrix)
10. [Phased build plan](#10-phased-build-plan)
11. [Evaluation methodology](#11-evaluation-methodology)
12. [Risk register and honest limitations](#12-risk-register-and-honest-limitations)
13. [Appendix A — mathematical reference](#appendix-a--mathematical-reference)
14. [Appendix B — literature index](#appendix-b--literature-index)
15. [Appendix C — configuration schema](#appendix-c--configuration-schema)

## 1. The problem

### 1.1 The user-visible failure

Compaction can leave an agent without an exception, decision rationale, prior failure or historical version needed for its next step. The earlier webhook example illustrates a possible loss; it is not a measured universal degradation curve. Repeated summaries need not lose strictly more information on every pass.

### 1.2 Why the existing design is not simply "wrong"

Keep the native Claude Code compaction pipeline. Vendor documentation establishes candidate host contracts; installed-version canaries establish usable integration. Historical internal names, constants, cache statistics and global failure counts in v1.0–v1.3 were not target evidence and are retired as production premises.

### 1.3 The three root causes

The migration addresses inaccessible evidence, lost provenance between derivatives, and insufficient task/recovery measurement. Re-reading the current file or re-running a command can cost time, change state, or return a different result. It cannot reproduce missing history by definition.

## 2. Current architecture — what we are building around

### 2.1 The three tiers

Earlier MicroCompact/Session Memory/Full Compact descriptions are historical motivation. This plan depends on observed hook contracts and recoverable Qompack state, not undocumented tier internals or fixed compression percentages.

### 2.2 MicroCompact

Qompack has no validated control for clearing already-delivered native results. `ephemeral` metadata does not establish eviction. Disk deduplication saves storage only; prompt savings require a changed delivered representation and separate measurement.

### 2.3 Session Memory Compact

Do not infer the available native context from a generated memory artifact. Native memory, Qompack checkpoints and observed load events are different evidence sources with different coverage.

### 2.4 Full Compact pipeline

Use target-tested SessionStart compact handling for reinjection. PostCompact summary observation is optional; reinjection must not require it to arrive first. PreCompact capture/finalization is idempotent and bounded, and a local attempt record distinguishes attempted, failed, incomplete and observed completion without inventing host identifiers. See SP-10/SP-11 and [the hook guide](https://code.claude.com/docs/en/hooks-guide).

### 2.5 Trigger arithmetic

Treat context capacity, usage, output allowance, headroom and observation age as separate fields. The old fixed 13K/20K arithmetic is not a universal current contract. Environment hints alone do not prove effective host settings. Unknown observations cannot prove headroom; SP-12's ordinary checkpoint cadence remains useful without them.

### 2.6 PTL recovery and partial compact

Native recovery, historical cuts and retry policies belong to the host. Qompack retains the latest usable durable checkpoint and reports gaps. Default automatic-compaction veto is disabled because a safe recovery-versus-proactive distinction has not been established. A user's manual compact must not be vetoed for optimization.

### 2.7 What survives compaction

Distinguish Qompack-included, archive-only, native-load-observed, expired/deleted and unknown coverage, with time/epoch and fidelity. A missing InstructionsLoaded event does not prove absence; a path event and later disk read do not prove exact loaded bytes. Restore scoped instructions only under verified host scoping rules; do not inject every rule associated with archived paths.

### 2.8 The API-level surface (for reference)

A provider API capability does not establish a Claude Code plugin capability. No harness port or OpenAI calls are required. Preserve negotiated MCP versions in the existing hand-rolled server, which the SP-13 worktree lists as 2025-06-18, 2025-03-26 and 2024-11-05. Future compatibility checks use each negotiated contract.

## 3. Complete gap inventory

The inventory contains **40 unique G-numbered gaps across 10 categories**, counted from the entries below. The identifiers survive; the descriptions are concerns to investigate, not unconditional claims about every installed host. Each has one accountable migration owner; supporting components and residuals are mapped in [TRACEABILITY.md](plans/TRACEABILITY.md).

### G1 — Trigger and timing

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G1.1 | Task-blind timing | SP-12 |
| G1.2 | Timing agency | SP-12 |
| G1.3 | Headroom estimates | SP-12 |
| G1.4 | Blocking-limit recovery | SP-19 |
| G1.5 | Task-boundary signals | SP-12 |

### G2 — Generational information loss

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G2.1 | Derivative provenance | SP-20 |
| G2.2 | Durable pins | SP-10 |
| G2.3 | User-intent history | SP-20 |
| G2.4 | Typed state | SP-10 |
| G2.5 | Evidence validation | SP-10 |
| G2.6 | Session-memory drift | SP-10 |

### G3 — Compression without an index

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G3.1 | Historical retrieval | SP-13 |
| G3.2 | Addressable result handles | SP-21 |
| G3.3 | Budgeted additional context | SP-11 |
| G3.4 | Essential exact spans | SP-10 |

### G4 — Instruction survival and coverage

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G4.1 | Path-scoped rules | SP-11 |
| G4.2 | Nested instructions | SP-11 |
| G4.3 | Partial skill coverage | SP-11 |
| G4.4 | Skill discovery | SP-11 |
| G4.5 | Coverage report | SP-11 |

### G5 — Representation choice

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G5.1 | Native cut limitations | SP-12 |
| G5.2 | Cache-cost observability | SP-19 |
| G5.3 | Heterogeneous evidence | SP-15 |

### G6 — Negative knowledge

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G6.1 | Elimination records | SP-20 |
| G6.2 | Repeated failed approaches | SP-13 |
| G6.3 | Retention of applicable negative knowledge | SP-15 |

### G7 — Failure handling and cost

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G7.1 | Compaction request cost | SP-19 |
| G7.2 | Summarizer model control | SP-18 |
| G7.3 | Intent recovery | SP-11 |
| G7.4 | Failed compaction recovery | SP-10 |
| G7.5 | Empty or unexpected summary | SP-10 |
| G7.6 | Repeated compaction failures | SP-19 |

### G8 — Observability

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G8.1 | Task and recovery metrics | SP-19 |
| G8.2 | Summary uncertainty | SP-10 |
| G8.3 | Feedback and accounting | SP-14 |

### G9 — Durability and compatibility

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G9.1 | Durable checkpoints | SP-10 |
| G9.2 | Packaged recovery layer | SP-17 |
| G9.3 | Host contracts | SP-19 |

### G10 — Agent detail and estimation

| ID | Problem to evaluate | Migration owner |
|---|---|---|
| G10.1 | Subagent capture coverage | SP-20 |
| G10.2 | Assembled token estimation | SP-11 |

## 4. Theoretical reframe

### 4.1 Rate–distortion, not summarization

Task-relevant information under a context budget is a useful research framing. Neither the correct future action distribution nor its information content is directly known to the plugin. Evidence fidelity remains an independent recovery requirement.

### 4.2 Making distortion measurable

File-set overlap, tool-sequence edit distance, first divergence and repeated actions are diagnostics. They are not unbiased action-distribution KL estimates and are not correctness labels. Future evaluation uses task completion, constraint/regression failures and recoverability as primary outcomes, with ordinary stochastic variation measured on repeated baselines.

### 4.3 Cross-entropy as the selection signal

Retrospective overlap and relation scores are heuristics. Relevance, demand frequency and retrieval usefulness are distinct. Optional value-of-information retrieval is a bounded experimental policy; there is no computable exact task oracle.

### 4.4 MDL — encode only the non-reconstructible residue

Prefer a pointer only when historical evidence is recoverable within scope and its recovery burden is acceptable. Preserve essential exact exceptions or small spans when needed. Current files, directory listings, Git state and rerun test output do not replace captured historical versions.

### 4.5 The prior filter — encode surprisal, not facts

Surprisal is an optional ranking hypothesis. Do not discard an explicit requirement because a model is expected to know it. User authority, unresolved conflicts and evidence coverage take precedence over compressibility.

### 4.6 The DPI wall

Under an applicable Markov-chain model, the data processing inequality gives non-increase, not strict loss at each pass. Retrieval from original evidence changes the available information. The implementable invariant is: primary evidence is not replaced by a derivative, and every derivative retains provenance. Repeated derivation from retained evidence is allowed; missing originals remain missing.

## 5. The prompt-cache cost model

### 5.1 Caching is not a separate concern

Use a dated rate schedule for the supported provider, model, pricing mode and request category. Configured rates are assumptions for estimated price; provider usage is observation; invoices are a separate reconciliation source. Subscription allowance is not automatically a per-token cash charge. No rate is asserted current merely because v1.3 printed it.

### 5.2 The trap

An abstract prefix-edit model does not supply a plugin action or a complete bill. Retire rewrite-token accounting as total cost, impossible examples and guaranteed free cold cuts. Arithmetic acceptance must reject removing 60K tokens from a 17K suffix. Historical benchmark fields keep their original definitions and provenance while corrected metrics are added beside them.

### 5.3 The corrected objective

For each request, estimated token price is the sum of its reported usage categories multiplied by their corresponding rates, plus applicable non-token charges. Separate uncached input, cache reads, writes by TTL where known, output, retries, compaction, child agents and any future Qompack model calls. Store provider, model, rate-table date, pricing mode and telemetry completeness. Missing usage is unknown, never zero.

The native summarization request includes conversation history; a focus instruction does not physically shorten it. Qompack incremental checkpoint work is a different measured quantity. [Claude Code caching documentation](https://code.claude.com/docs/en/prompt-caching)

### 5.4 Choosing p

Native historical cut selection is unsupported in the production plugin. Retain any existing p-selection implementation only as labeled harness analysis or compatibility surface; it must not enable unsupported host actions. No general unimodality or cache-free replacement claim survives. Qompack scheduling operates independently on its own frontier and future representations.

### 5.5 Cache-compatibility audit of every proposed method

| Method | Actual scope | Required evidence |
|---|---|---|
| CDC, normalized search, exact indexing | Physical evidence store | Fidelity, round-trip and index consistency |
| Sketches | Acceleration of authoritative records | Coverage, freshness, exact confirmation |
| Capsules/pointers | Newly delivered Qompack or allowlisted host output | Capture before replacement, schema and recovery |
| Selection/grammar | Future Qompack injection/results | Feasible serialized budget, task evaluation |
| Native cache markers or delivered-history edits | Unsupported | Separate validated interface before any future reconsideration |

Do not derive definite cache state from elapsed time since an arbitrary tool/request. TTL and effort/model behavior can depend on request category and billing configuration. Unknown state disables cache-dependent policy, not recording or retrieval.

### 5.6 Information theory applied to caching itself

For a conditional illustration with N identical uses, one write, all later uses hitting, base input normalized to 1, compare N against w + (N−1)r. For r < 1, write-plus-hits is cheaper when N > (w−r)/(1−r). With w=1.25 and r=0.1, two uses cost 1.35 rather than 2. This is arithmetic under explicit assumptions, not an operational threshold or a guarantee about hit rates. The old w/r rule is retired from default policy. Native breakpoint placement remains unsupported.

## 6. Algorithmic toolkit

### 6.1 Content-defined chunking + Merkle addressing

Preserve existing SHA-256 addressing, zstd and CDC when they satisfy fidelity and recovery contracts. Semantic chunk coverage and parser fallback complement physical chunks. Hash meaning must be versioned; normalized-text identity cannot silently become original-byte identity. MinHash similarity neither proves semantic equivalence nor supplies an exact delta.

### 6.2 Bloom filters and sketches for permanent memory

Confirm every Bloom positive against authoritative records. Trust negatives only over declared complete and fresh coverage; otherwise bypass the filter. Fixed size cannot maintain a fixed false-positive rate under unlimited insertion. Misra–Gries returns candidates; exact top-k claims need an exact verification pass. A majority-candidate process may retain an item from three distinct values although no majority exists.

### 6.3 Grammar compression over the action sequence

Keep implemented Sequitur if useful. A repeated edit-test sequence can be progress. SP-15 compares goal, target, action, failure signature, relevant file/environment changes and progress, initially emitting bounded deduplicated warnings. Additional grammar machinery needs an ablation.

### 6.4 Dynamic slicing over the dependence DAG

Shared paths and tool order define an approximate relation graph, not a sound program slice. Existing DAG scores may aid selection; nodes outside them are not provably irrelevant. Preserve implemented graph contracts and naming conventions.

### 6.5 Submodular maximization for budget allocation

Select at most one compatible representation per item, include dependency closure and overhead, and break ties deterministically. Begin with transparent heuristics and nonnegative saturating coverage where appropriate. Redundancy subtraction need not preserve monotonicity. Representation/dependency constraints change the feasible family, so no generic lazy-greedy 1−1/e claim applies. Compare against exact small instances of the same declared objective, not an oracle for task quality.

### 6.6 Changepoint detection for when to compact

Distribution changes are candidate boundaries, not proof of semantic independence. Measure the time/memory cost of the actual bounded/pruned implementation. Use it only as an optional Qompack cadence signal after the simple baseline.

### 6.7 Optimal stopping / Young–Daly cadence

Retire Young–Daly from default policy. Its fault/checkpoint setting does not establish optimal native compaction timing. Keep existing formula bodies/history if compatible, labeled historical analysis, and plan safe configuration deprecation.

### 6.8 LSM compaction theory

Reachability, read/write/space amplification and bounded maintenance are useful analogies. They are not proofs of constant storage or prompt savings. Include reader leases, pending publications, delta bases, checkpoints, evidence links and rollback retention in GC roots.

### 6.9 Progressive / embedded encoding

Serialize complete records under the additional-context budget. Importance order does not make arbitrary byte truncation valid or optimal. Oversized mandatory/conflicting records require explicit overflow, durable retention, safe retrieval and a visible diagnostic; do not claim all mandatory records fit.

### 6.10 Belady's OPT as evaluation ceiling

Retain historical Belady outputs under their actual harness assumptions. Classic paging OPT is not a universal task-quality ceiling, and variable-sized caching has different constraints. Primary release gates concern correctness and recoverability.

### 6.11 Explicitly considered and rejected

| Method | Disposition |
|---|---|
| Normalized compression distance, embeddings/PQ, suffix/FM indexes | Deferred until existing exact/full-text retrieval is shown insufficient |
| Erasure coding or complex inferred invariants | Not required; prioritize publication integrity, explicit authority and recovery |
| TinyLFU, GreedyDual-style scoring, LLMLingua-2, ACE, cache-aware compression | Optional later experiments with measured benefit, not release dependencies |
| Number-theoretic encodings | No required feature; retain collision handling and sound addressing |
| Native-history rewriting, cache markers, arbitrary deletion | Unsupported production plugin operations |
| Codex runtime port or OpenAI model calls | Outside this migration |

## 7. Plugin architecture

### 7.1 The fundamental constraint

Qompack remains the Go Claude Code sidecar. Recording, reinjection, output replacement and experimental policy have independent future kill switches. Unknown host/schema environments report unsupported/degraded capability instead of guessing. Privacy-denied data follows privacy policy even when optimization otherwise fails toward pass-through.

### 7.2 Layer diagram

| Layer | Responsibility | Migration owner |
|---|---|---|
| L0 observer | Capture host payload and observed relationships | SP-20 remediation of SP-08/SP-05 |
| L1 store | Durable identities, publication, fidelity, retention | SP-20 remediation of SP-06/SP-04 |
| L2 analyzer/state | Authority, applicability, feasible selection, loop warnings | SP-20, SP-15 |
| L3 scheduler | Qompack cadence and measured observations | SP-12 |
| L4 checkpoint | Committed frontier and compatible durable artifact | SP-10 |
| L5 rehydrator | Complete bounded additional records and coverage | SP-11 |
| L6 retrieval/commands | Discoverable authorized historical recovery | SP-13, SP-14 |
| L7 evaluation | Deterministic, closed-loop and failure evaluation | SP-19, V4/V5/V6 VERIFY |

### 7.3 Hook surface

The current package declares seven events through `internal/pluginmanifest/manifest.go`. Preserve passive observation. Future SP-19 distinguishes observation, injection, new-result replacement, usage attribution, estimation, request, blocking and history-rewrite capabilities. Current documentation describes new-output replacement, PreCompact blocking, PostCompact summary and asynchronous InstructionsLoaded; installed support remains unverified. `custom_instructions` is PreCompact input, not a summarizer-output setter. [Hooks reference](https://code.claude.com/docs/en/hooks)

### 7.4 Directory layout

Reuse the existing `.qompack/` object/index/record/checkpoint layout and product write-set conventions in architecture §13. Logs, indexes, exports and backups share the chosen privacy/retention policy. Append-only evidence is retained subject to explicit quota/expiry/deletion policy; indefinite retention and bounded storage cannot both be guaranteed.

### 7.5 Plugin manifest sketch

No manifest/configuration snippet is generated here. Future SP-19/SP-17 compare the actual package, launcher paths, seven hooks, seven commands and MCP initialization against the supported Claude CLI validation path, then disposable installed-plugin canaries. Repository JSON parsing alone does not validate the installed plugin. [Plugin reference](https://code.claude.com/docs/en/plugins-reference)

## 8. Component specifications

### 8.1 L0 — Observer

SP-20 captures permitted host payload before normalization, recording partial reads, redaction, truncation, unsupported media, interrupted output, unavailable originals and capture failure. Keep a small synchronous durable capture/spool path; indexing/compression may be deferred within bounded queues. Actual event identity and session/project/worktree/parent-child relationships control idempotence. Equal content does not collapse distinct events. Unknown relationships remain incomplete. Existing superseded versions remain evidence.

### 8.2 L1 — Store

SP-20 verifies objects, publishes durable identities, commits references/frontier, then permits checkpoint publication. Test object/index failure independently. Legacy migration takes a consistent engine-supported snapshot and stable frontier with explicit writer handoff; builds beside old state; retains old identities; imports idempotently with resumable cursor; derives fidelity only from evidence; compares object/reference/query/semantic parity; cuts over under single-writer ownership. Rollback after new writes requires a compatible reader or verified backup, not an assumed downgrade.

GC includes chunk manifests, delta bases, evidence links, pending writes, reader leases, checkpoints and rollback roots. Recovery cannot depend on SessionEnd. Prefer the existing file engine; SQLite is optional and would require deployment-library/WAL patch, FTS, locking, filesystem and backup verification.

### 8.3 L2 — Analyzer

SP-20 separates immutable observations from derived current state. User requirements, decisions, agent claims, tool observations and extracted candidates keep their authority/scope/provenance/conflicts/supersession. Heuristics propose eliminations, never binding prohibitions. Conservative dependency hashes establish only observed applicability; unknown coverage means uncertain. Existing reason-independent MatchKey and exact Bloom confirmation are reused. Compatibility keeps older callers functional without interpreting retrieval failure as not-tried.

### 8.4 L3 — Scheduler

SP-12 independently advances Qompack checkpoints and chooses future Qompack representations; native requests/blocking are separately gated and off by default. Start with configurable cadence, dirty triggers and bounded maintenance. Unknown token/cache observations are acceptable; status-line integration never replaces user configuration without consent. Predictive reserves require calibrated parallel-output/model-output allowances. Existing lifecycle-managed workers need cancellation, quotas, bounded queues and crash recovery. Idle compute is not free.

### 8.5 L4 — Checkpointer

SP-10 builds from durable evidence and explicit current state, preserving compatible old readers. A committed event frontier records gaps, in-flight/background work and unresolved tails. Idempotent local compaction attempts use conservative correlation. Finalization selects the latest usable checkpoint without indefinite background waits. Own injections/retrieval wrappers are not new independent primary evidence. Retire O1's output setter and native latency promise; retain O5 as measured Qompack incremental work.

### 8.6 L5 — Rehydrator

SP-11 emits whole records under a Qompack-added budget including wrappers, handles and report overhead. The old 8–12K target is additional context, not a replacement for native restored context or a first-turn saving guarantee. Current authority and exact exceptions take precedence over obsolete intent. Overflow is explicit and recoverable. Coverage states and timestamps do not certify complete native loading or model compliance. Scoped instructions need target semantics, not automatic pointer-directory restoration. The retained SP-11 mechanism re-reads path-scoped rules and nested `CLAUDE.md` files from disk — a nested file counts when its directory contains, or is an ancestor to, a pointer-set file, stopping before the project root (v1.4, 2026-08-26, pinned by the shipped conformance suite) — and v1.5 qualifies that restoration as scoped and coverage-reported, never a completeness claim.

### 8.7 L6 — Retrieval tools (MCP)

| Existing tool identifier | Future contract |
|---|---|
| recall | Exact identity/path/symbol/event/decision and sufficient full-text search; scope checked before previews |
| expand | Resolve stored identity to captured content with fidelity, coverage, omissions, pagination and errors |
| re_read | Explicit current-file versus historical-version semantics; never substitute current on historical miss |
| already_tried | Exact evidence-qualified applicability; unavailable/uncertain distinct from absent; compatible older callers |
| record_eliminated | Record scoped claim, reason and observed dependencies; no automatic user authority |
| timeline | Observed event order/frontier and gaps, not invented causal completeness |
| why | Decision identity, rationale, authority and evidence links |
| dropped | Compatible name retained; evidence-qualified coverage-and-recovery report |

Preserve the negotiated MCP contract, structured/displayed consistency and error semantics. A URI-looking pointer needs an installed discoverable operation that actually resolves it. Authorize before search/expansion; validate path/symlink/encoding/size/decompression limits and preserve denied-read policy. Archived text is untrusted, hashes are not permissions, and retrieval never replays commands. Admission SP-21 begins with owned responses only after M1–M3 recovery gates; deterministic self-contained capsules precede deltas and allowlisted host transformations.

### 8.8 L7 — Evaluation harness

Evaluation is continuous future work in every package. V4/V5/V6 consolidate evidence without relabeling historical artifacts or claiming mock success proves host behavior.

## 9. Gap traceability matrix

[TRACEABILITY.md](plans/TRACEABILITY.md) owns the detailed mechanism, test/gate and residual for every one of the 40 IDs above, plus E1/E1a/E1b/E2, GA/GB/GC and O1–O5. Status vocabulary distinguishes documented, verified_in_target, implemented_unverified, unsupported, experimental and unknown. Completed history is preserved independently of whether migration evidence remains unverified. No unconditional gap-closed count is a release claim.

## 10. Phased build plan

Phases are historical design subjects, not wave numbers. Existing identifiers/branches remain; remediation and extensions do not reset completed checkboxes. [SP-19](plans/V4-SP-19-migration-reconciliation.md) now begins with M0-00: integrate completed SP-10 → SP-11 → SP-12 → SP-13 into `develop`, review retained behavior and conflict resolutions, validate the combined baseline and accept M0-G0. Only then run the rest of SP-19, SP-20 and required consumer follow-ups. Full revised V4 migration verification follows those corrections; neither the original completion statement nor successful merging substitutes for its evidence.

### Phase 0 — Measurement (do this first)

SP-02 belonged to Wave 1/V2 and is already implemented. Preserve its historical baseline. SP-19/V4 VERIFY add corrected request accounting and resolve carried SP02-D1–D6 as one versioned corpus/metric change. Do not rerun completed work merely to satisfy this heading.

### Phase 1 — Store and observer

SP-04/SP-06 in Wave 1/V2 and SP-08 in Wave 2/V3 are implemented. SP-20 adds focused fidelity/publication/migration remediation; it does not replace their implementations wholesale.

### Phase 2 — Negative knowledge

SP-03/SP-09 are implemented; user-completed SP-13/SP-11 supply the original Wave 3 tools/injection. Preserve and integrate that work before migration remediation. SP-20 remediates uncertainty/authority while reusing reason-independent lookup and exact-record checks.

### Phase 3 — Checkpoint and rehydrate

Original Wave 3/V4 SP-10/SP-11 are user-reported complete. Preserve their implementations through M0-00; subsequent reconciliation identifies only necessary corrections. M1/M2 recovery and state gates precede enabling corrected M3 rehydration.

### Phase 4 — Scheduler

Original Wave 3/V4 SP-12 is user-reported complete. Preserve it through M0-00 and remediate only the identified differences. Cadence and Qompack frontier remain useful without native triggering, veto or cache certainty. Native O(delta) timing is retired.

### Phase 5 — Selection

SP-07 graph work is completed; Wave 4/V5 SP-15 owns feasible representation selection and its integration into future injections/results. There is no unnamed later rehydration slice.

### Phase 6 — Grammar and loop detection

Wave 4/V5 SP-15 owns warning-only state comparisons, progress awareness and false-alarm measurement.

### Phase 7 — Refinement

Wave 4/V5 SP-16 owns scoped/expiring project reuse and bounded demand promotion. M4 admission is separately owned by new Wave 4/V5 SP-21 after M1–M3. Optional policy complexity is not a prerequisite for SP-17/SP-18 production evaluation.

## 11. Evaluation methodology

### 11.1 Primary metric

Keep task completion, constraint/regression failures and evidence recoverability separate from cost. Use deterministic fixed-input tests, closed-loop tasks on isolated repository/environment snapshots, and recovery/failure stress tests. Compare stock, currently implemented Qompack, corrected checkpoint/retrieval and admission independently.

### 11.2 Secondary metrics

Report provider usage categories, estimated rates and invoice reconciliation separately; include failed/aborted trials, retries, recovery and child work. Measure latency/context growth, repeated work, retrieval usefulness/burden, CPU/storage and tail delays. Qompack-added tokens and total observed native context are distinct. Re-reading changed files is not inherently redundant.

### 11.3 Guardrails

Declare regression margins, sample-size rationale and held-out tasks before observing outcomes. The historical 2% rule remains a baseline convention to characterize, not a guarantee from twenty runs. Report uncertainty, missing telemetry, exclusions and inconclusive outcomes. No cheap wrong answer passes through a scalar score. Isolate branches so task runs cannot mutate each other's files.

### 11.4 Watch for

Replay overfitting, stochastic variation, dirty snapshots, synthetic demand blind spots, favorable-run selection, incomplete telemetry and skipped integration tests. Observation masking is a baseline only where the harness supports it; published motivation does not establish Qompack performance. Unique evidence can grow linearly and hook tails depend on process startup, payload novelty, I/O and contention.

## 12. Risk register and honest limitations

| Risk | Future owner and fallback |
|---|---|
| Captured payload differs from full original | SP-20 fidelity/coverage; no invented historical bytes |
| Legacy identity/schema loss | SP-20 resumable side-by-side import, old readers, verified snapshot rollback |
| External object/index publication and GC race | SP-20 failure boundaries, leases/roots, incomplete status |
| Trust/privacy leak in previews or archive expansion | SP-13 authorization before snippets, bounded decoding, no denied-read bypass |
| Lifecycle gaps/late events/failed compaction | SP-10 committed frontier and local attempts; latest usable checkpoint |
| Unknown instructions/native context | SP-11 qualified coverage and scoped restoration gate |
| Incomplete or stale elimination evidence | SP-20 uncertainty and exact confirmation, no filter veto |
| Unsupported host version or control | SP-19 per-capability disable/degraded diagnostics |
| Optimizer failure or malformed output | SP-21 pass-through after policy checks; replacement off until recovery |
| Cost/quality claims exceed evidence | V4/V6 separated outcome/usage reporting; explicit inconclusive results |
| Unreadable future schema or rollback after new writes | SP-17 compatible reader or verified backup; never blind downgrade |

### What this plugin cannot do

It cannot promise native-history cuts, marker control, deletion of already-delivered results, summarizer model substitution, exact native loaded bytes, model compliance, universal savings or complete capture of unobserved child work. The production plugin must function without native compaction request/veto. It cannot reconstruct uncaptured history by reading current files, infer cache state from arbitrary elapsed time, or turn an archive into a denied-read bypass.

### Upstream issues worth filing separately

SP-18 may prepare issue text for missing host observability or supported controls. Sending issues requires separate authorization. No new external service or account change is authorized by this plan.

## Appendix A — mathematical reference

| Quantity | Correct scope |
|---|---|
| DPI | Non-increase under the relevant Markov model; provenance invariant governs implementation |
| Action KL | Theoretical distribution divergence; file/action distances are diagnostics |
| Cache break-even | N > (w−r)/(1−r), assuming N identical uses, one write, later hits and r<1 |
| Request price | Sum reported category × applicable dated rate, plus non-token charges; unknown stays unknown |
| Bloom sizing | Capacity/freshness-dependent; positive requires exact confirmation |
| Misra–Gries | Candidate extraction, not exact top-k without verification |
| Submodular bound | Only for a specified objective, feasible family and proven algorithm |
| Young–Daly / Belady | Original-domain research; no native-timing or universal task-quality guarantee |

## Appendix B — literature index

The user-provided source register is retained in the migration ledger; sources motivate testable choices. Relevant originals include [Sviridenko](https://thibaut.horel.org/submodularity/papers/sviridenko2004.pdf), [BOCD](https://arxiv.org/abs/0710.3742), [checkpoint strategies](https://arxiv.org/abs/1207.6936), and [variable-size caching](https://arxiv.org/abs/1711.03709). Optional experiments include [TinyLFU](https://arxiv.org/abs/1512.00727), [LLMLingua-2](https://arxiv.org/abs/2403.12968), [ACE](https://arxiv.org/abs/2510.04618), [cache-aware compression](https://arxiv.org/abs/2607.15516), and [The Complexity Trap](https://arxiv.org/abs/2508.21433). These prior-review references were not runtime Qompack verification.

## Appendix C — configuration schema

This planning revision intentionally specifies configuration evolution in prose/tables. The existing `internal/config` declarations, default goldens and generated config reference remain unchanged; older tests that parse this appendix need a planned compatibility update before execution. SP-19 inventories every reader, lint and golden consumer; no regeneration occurs now.

| Setting family | Future migration/default policy |
|---|---|
| Schema/config version | Explicit version, compatible reader/deprecation diagnostics; unknown future behavior disabled |
| Recording/reinjection/replacement/experiments | Independent kill switches; replacement and unsupported native controls off |
| Young–Daly, incrementalSpanInstruction, deepCutWhenCold, ski rental | Preserve existing reader compatibility; retire production action/default meaning through reviewed migration |
| Rate/cache/usage | Provider/model/date/mode/category provenance; estimates and unknown values visible |
| Capture/fidelity/retention | Bounded durable spool, quota, privacy and explicit expiry; hash meaning/version retained |
| Checkpoint/rehydration | Qompack-added budget, whole records/overhead, overflow behavior and calibrated estimation |
| State/reuse | Authority, scope, dependency coverage, expiry and uncertainty; no automatic cross-project sharing |

Do not overwrite user status-line configuration. Any future external service requires explicit consent/credentials and an architecture decision; none is required here.

## Closing note

Preserve and integrate completed SP-10–13 first; accept the combined baseline; then reconcile current contracts, repair capture/retrieval/state and verify the corrective migration before evaluating measured opt-in optimizations. Documentation completion does not satisfy any implementation gate.

## Revision log

The v1.1–v1.4 entries below are preserved historical change records, not current runtime promises. v1.5 supersedes their unsupported guarantees and configuration conclusions.

**Numbering note.** The planning-only revision was drafted as "v1.4" against the `verify/v3` snapshot (`7f92af5`), before SP-11's v1.4 of 2026-08-26 had reached the combined baseline; SP-19 M0-01 numbers it v1.5 on that baseline. A "v1.4" in `internal/rules`, ADR 0011 and the v1.4 section of `plans/QOMPACK-ERRATA.md` means SP-11's §8.6 widening, which stands.

**v1.5 — 2026-09-06, planning-only evidence migration.** Reconciled actual waves and active worktrees; corrected host capability, fidelity, authority, cost and algorithm claims; mapped M0–M7 to existing plans plus SP-19/20/21; specified future verification/rollback. No code, configuration, runtime tests, branches, commits or data migration performed. See `plans/QOMPACK-ERRATA.md` and `plans/MIGRATION-EVIDENCE.md`.

**v1.5 addendum — 2026-09-06, merge completed Wave 3 first.** The user reports original SP-10–13 complete and requires their ordered integration before the rest of SP-19. Added M0-00/M0-G0 for preserved delivery history, shared-contract/conflict review, combined-baseline evidence and recovery. Updated the execution guide and V4 prerequisite mapping without claiming a merge, runtime pass or new migration completion. All changes remain Markdown-only.


**v1.1** — Corrections and additions from a full-plan review:
- **E1**: sliding-TTL correction — cache expiry only occurs in idle gaps; idle detection promoted to first-class scheduler input (§5.4, §8.4)
- **E2**: `cache_control` breakpoint placement rescoped as harness/API-port material, not plugin-actionable (§5.6, §12)
- **GA**: elimination staleness — evidence-linked `depends_on` hashes, active/stale status, Bloom rebuilt from records (§8.3, schema, Phase 2)
- **GB**: retrieval re-inflation — ephemeral-at-birth results, minimal-span defaults, demand-driven promotion (§8.7)
- **GC**: checkpoints regenerate from the store only, never from surviving in-context injections; injections tagged (§8.5)
- **O1**: incremental summarization via span-narrowing focus instructions (§8.5, Phase 3, closing note)
- **O2**: per-tool output canonicalization + MinHash near-dedup (§8.1, Phase 1)
- **O3**: idle-time background work (§8.4, Phase 4)
- **O4**: cross-session warm start (Phase 7)

**v1.2** — Latency made an explicit objective:
- **O5**: amortized compaction — continuous checkpoint frontier advancement keeps the residual span O(delta), turning the summarization call from a stop-the-world O(session) pause into an incremental-GC-style amortized cost (§8.5, Phase 4)
- Latency budget breakdown added: warm-cache prefill is cheap, **decode dominates**, thinking inheritance and the post-compact rebuild are the other two components (§8.5)
- Latency metrics added to §11.2: compaction pause, residual span, first-turn-after latency; Phase 4 exit criterion now tests the amortization claim directly (pause flat as session length grows)

**v1.3** — §5.1's standing instruction discharged. Every cache figure in this document was checked
against published Anthropic documentation on 2026-08-23; `plans/QOMPACK-ERRATA.md` records what was
confirmed, what changed, what could not be verified, and the sources. The reasoning in §5 survived
intact — what moved were numbers it was written to expect to move, and one scheduling consequence
it had drawn only half of:
- **`w` is a pair, not a scalar** — 1.25 at the 5-minute TTL, **2.0** at the 1-hour. `w/r` is
  therefore 12.5 **or 20**, and every threshold spelling `w` takes a (TTL, `w`) pair (§5.1, §5.6,
  Appendix A). `r = 0.1` confirmed unchanged.
- **The TTL is a regime the session does not announce** — one hour automatically on a Claude
  subscription, five minutes on API-key and third-party auth, overridable by three environment
  variables, and five minutes for subagents regardless. Under an unknown regime, assume the longer
  TTL and dearer `w`: the two errors are not symmetric (§5.4, §8.4).
- **The TTL clock starts at the request, not the response** — generation time counts against it, so
  an idle model anchored on a turn's end over-reports warmth by the whole generation (§5.4, §8.4).
- **Compaction is itself a priced request** — `r·n` against a live prefix, `n` against a dead one.
  §5.3's objective gains the `c·n` term. It is `p`-independent, so the argmax is unchanged; what it
  changes is *when to fire* (§5.3).
- **The expiring band is a trigger, not just a decay factor** — the consequence of the term above,
  and the half of "schedule against cache state" v1.1 did not draw. Firing while the prefix is still
  readable costs `(1 − r)·n` less than firing after it dies: 135 000 base-input-token-equivalents at
  a 150 000-token context (§5.4, §8.4).
- **Time is not the only way a prefix dies** — model, effort level and the fast-mode header are all
  part of the cache key. Only effort is observable from a plugin (§5.5, §5.4, §12).
- **§2.5 gains four window variables** and the per-model default framing; the section's arithmetic is
  confirmed, reproducing the published 967K Sonnet-5 figure exactly (§2.5, §8.4).
- **§2.7 gains the extended-thinking inheritance** of the summarization request (v2.1.198), and marks
  its one unverifiable row as such (§2.7).
- **§12 gains four limits and two upstream issues**, all about cache state a plugin cannot see.

Appendix C's values are **unchanged and remain correct**: they are the five-minute regime, which is
the floor. The running regime is resolved at runtime and never written back into config, so D11's
lint gate and the Appendix C golden test keep working exactly as before.

§2.2, §2.3, §2.4 and §2.6 are **unchanged and were not verified**. They describe Claude Code
internals by identifier, none of which appears in published documentation. They were not checked
against decompiled or leaked builds — §7.1 makes this a sidecar that surrounds compaction rather
than depending on its internals, and a design premised on that must not acquire the dependency
through its own revision. Treat them as motivating background at the version they were written
against. Where a §2 fact became load-bearing — §2.5's arithmetic and §2.7's table — it is publicly
documented and it checks out.
- Existing levers re-attributed as latency wins: no-snippets rule cuts decode length; 8–12K rehydration budget cuts first-turn-after latency

**v1.4** — §8.6's nested-`CLAUDE.md` rule widened to the subtree it actually governs:
- **Nested `CLAUDE.md` restoration walks ancestors.** §8.6's "in a directory *containing* a
  pointer-set file" was too narrow by the semantics of the thing it restores. A nested `CLAUDE.md`
  governs its whole subtree in Claude Code, so a `src/pkg/CLAUDE.md` is in force for
  `src/pkg/deep/thing.go` and is lost after compaction on exactly the terms G4.2 describes. The
  literal reading restored it only when a pointer sat in its own directory, leaving the deeper —
  and more common — case unrepaired while §9's G4.2 row claimed closure. The rule now reads
  "containing, or ancestor to", bounded at 32 levels and stopping before the project root, which
  the host re-injects itself (§2.7). §12's "cannot" list is unchanged; this widens what L5 reads
  from disk, not what a plugin may do.
- No other section moved. `plans/QOMPACK-ERRATA.md` records the conflict this resolved, the
  evidence on both sides, and why the widening was chosen over correcting the code to match.

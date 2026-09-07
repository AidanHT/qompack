# SP-20: capture, storage and state remediation — faithful observation, durable publication, compatible migration

> **Planning owner/model:** writer A; requested gpt-5.6-terra, medium effort. **Status:** future M1/M2 remediation, unchecked.
>
> This is a future-only remediation plan. It preserves the working observer, canonicalizer, file history, zstd object store, and negative-knowledge ledger; it does not authorize a rewrite of them.

**Branch:** proposed `feat/sp20-capture-storage-and-state-remediation` and proposed `../qompack-sp20` worktree; neither created now, future use only after SP-19 and separate implementation authorization | **Wave:** 3 (V4) prerequisite remediation before affected enablement | **Prerequisites:** SP-19/M0 reconciliation; retained current store/observer contracts | **Dependents:** SP-13/M2 retrieval, then M3 enablement | **Design sections:** Qompack.md §§7–8, Appendix C; MIGRATION-EVIDENCE.md E03–E06, E14, E16

## Mission

Repair the evidence boundary before it is used for checkpointing, recovery, or retrieval. For every host delivery, apply the privacy/retention decision before persistence. Where permitted, retain the captured host payload before deriving normalized, canonical, chunked, compressed, indexed or summarized views. Denied secrets must not be archived to satisfy an exactness claim; record the redaction/denial and resulting fidelity. Captured host payload bytes are not necessarily the complete original output of a process, request or file. The record must say whether the original is complete, partial, redacted, truncated, binary, unavailable, or failed; it must not turn any of those cases into an invented original.

SP-20 also makes publication truthful. A durable object alone is insufficient: references and a committed frontier advance only after their dependencies are verified; missing events and relationship holes remain explicit, with no claim of complete host history. Capture stays synchronous and small enough for the host path; indexing is deferred through a bounded recoverable queue. A capture failure must leave a host result in place and must forbid any pointer or replacement that claims the result is recoverable.

Finally, SP-20 establishes the shared state contract. Immutable observations are evidence; derived state is explicitly attributed, scoped, conflictable, supersedable, and correctable by the user. It retains the existing reason-independent negative-knowledge MatchKey and exact Bloom confirmation; coverage uncertainty and filter-generation/watermark faults make a result uncertain, never an active prohibition.

## Design context (verbatim from Qompack.md)

The heading follows the existing template; current v1.5 requirements are summarized here without executable examples.

### Existing evidence and retained implementation

The root contains real observer, canonicalization, file-history, zstd store, and negative-knowledge implementations. `ToolUseRecord` currently freezes normalized-content metadata but has no complete host-payload fidelity field (MIGRATION-EVIDENCE E03). `observer.OnToolUse` already requests raw deltas, and `Root` distinguishes raw and canonical byte counts. These are retained seams, not authorization to declare raw recovery complete.

The active SP-13 sibling at `../qompack-sp13` is implementation work owned elsewhere. Its negotiated MCP versions are `2025-06-18`, `2025-03-26`, and `2024-11-05`; SP-20 must supply compatible semantics to it without forcing a protocol upgrade. Its present `already_tried` error path conflates a ledger query failure with absence; M2 supplies the unavailable/uncertain distinction for SP-13 to consume.

SP05-D1 remains open: an interrupted drain can consume an event before handling finishes. The remedy is an acknowledged durable queue/publication boundary, not an assertion that replaying current history sees every event.

### Authority and identity rules

Event identity and content identity differ. Two separately observed host events with equal bytes remain distinct observations; equal content may share a content object. Each observation retains host identity if supplied, Qompack identity if not, observed parent/session/worktree/order fields, arrival order, and explicit gaps when a relationship is missing. It never fabricates a host ID, parent, session, worktree, order, or original payload.

State is derived from immutable observations and has an authority label: user correction, explicit decision, tool observation, candidate extraction, hypothesis, or conflict. State carries scope, dependencies, supersession links, source evidence, transform and hash versions, coverage, validity, and correction history. A later user correction may supersede a derived record without altering the observation that led to it.

### Storage, migration, and lifecycle rules

Full original bytes, when policy permits and capture succeeds, are the recovery source. Derived objects cite their source identity, transformation version, hash algorithm/version, and exact-delta base. A delta is admitted only when its declared base is durable and exact round-trip is proven; otherwise retain a full object. Privacy-redacted, partial, truncated, binary, failure, and unavailable payloads retain their actual fidelity status.

Legacy import is side-by-side, versioned, idempotent, resumable, and bounded by a stable import frontier. Its cursor records source snapshot identity, last committed source position, destination mapping, and acknowledgement so restart cannot skip or duplicate a logical record. The legacy writer and importer hand off under one writer at a time. Legacy unknown remains legacy unknown. Old IDs and old readers remain available through the compatibility window. Before cutover, take a consistent engine-supported backup and prove parity for the declared import set. Before the first new-format write, demonstrate a compatible deployed reader or a verified backup restore; after that write, repeat the rollback drill against the actual new artifact without deleting evidence.

GC retains roots for active leases, delta bases, pending writes, committed checkpoints, evidence references, and rollback/backup records. Quotas and expiry are visible policy outcomes. SessionEnd is independent recovery work: an interrupted SessionEnd cannot make an unacknowledged capture disappear or permit collection of its dependencies.

SQLite is not required. Adoption is allowed only after a measured need and a runtime check for the patched WAL version described in MIGRATION-EVIDENCE E14; backup, recovery, deployment, and FTS are separately gated. Existing search is assessed before any full-text replacement.

## Out of scope

| Item | Owner |
|---|---|
| Native output admission, replacement, and ephemeral-result policy | SP-21, after M1–M3 gates |
| MCP protocol/server rewrite, tool discovery, preview authorization, and retrieval UI | SP-13/M2 |
| Checkpoint presentation and rehydration selection | SP-10/SP-11 |
| Scheduler policy and native compaction controls | SP-12 |
| Reimplementing negknow descriptors, MatchKey, or Bloom exact confirmation | SP-09 retained implementation |
| Mandatory SQLite, FTS, external service, command replay, or secret logging | Not authorized by this plan |

## Interface contract

### Consumes

| Contract | Required use |
|---|---|
| Observer event and store put/index seams | Persist permitted captured payload before derived transforms, apply privacy first; preserve existing callers through additive adapters |
| Existing canon, zstd, root, file-history, and search contracts | Retain behavior and attach provenance/fidelity rather than rewrite storage |
| Existing negknow Ledger and descriptor MatchKey | Reuse exact Bloom confirmation; qualify coverage and failures |
| SP-19 reconciliation | Classify worktree/root implementations and freeze migration ownership before writes |
| SP-13 retrieval contract | Supply durable identity, fidelity, coverage, validity, and unavailable distinctions |

### Produces

| Output | Contract |
|---|---|
| Immutable observation envelope | Raw-source status, event/content identity, observed and arrival order, scope, provenance, transforms, hash version, fidelity, coverage, and error state |
| Durable publication record | Object durability, references, committed frontier, pending state, and acknowledgement are distinct |
| Derived-state record | Authority, source observations, scope, dependencies, conflicts, supersession, correction, validity, and uncertainty |
| Compatibility/import manifest | Version, stable import frontier, writer handoff, mapping/parity evidence, resumability, rollback/backup status |
| Retrieval evidence envelope | Exact/prefix/partial/redacted/truncated/binary/failure/unknown fidelity with omissions and validity |

### Required invariants

1. Permitted raw host bytes are captured before derived normalization; privacy denial/redaction occurs before persistence and missing originals remain explicitly unavailable.
2. Distinct observed events remain distinct even when content hashes match.
3. Publication order is durable object, verified references, then committed frontier/checkpoint visibility.
4. Capture or durable-write failure blocks replacement/pointer publication and preserves the host result unless privacy policy requires denial.
5. Deferred indexing has bounded admission, durable acknowledgement, retry/idempotence, and an explicit gap outcome.
6. An exact delta must reconstruct exactly from a durable declared base; no base means no delta-only recovery claim.
7. Derived state cannot silently acquire user authority or overwrite immutable evidence.
8. Unknown dependency coverage, stale generation/watermark, Bloom rebuild failure, or ledger error yields unavailable/uncertain; it never produces active negative knowledge.
9. GC cannot collect a root needed by a lease, delta, pending write, checkpoint, evidence reference, or rollback record.
10. Old readers/IDs remain usable through migration; each new-format write has a compatible-reader or verified-backup rollback path.

## Implementation spec

### M1-01: observation envelope and fidelity

Introduce additive, versioned envelopes beside frozen records. Apply privacy before persistence; capture permitted host payload bytes before canonicalization and record source format, media/binary status, completeness, partiality, redaction, truncation, failure, and capture error separately. Persist transform chain and hash/version values. Preserve host fields as observed and record absent fields as absent, not synthetic defaults.

### M1-02: event ordering and durable publication

Give every accepted delivery a Qompack observation identity distinct from optional host identity and content hash. Retain observed parent/session/worktree/order alongside arrival sequence; represent gaps, duplicates, and out-of-order arrivals. Write raw object durability first, then reference/index intent, then committed frontier acknowledgement. The small capture/spool operation is synchronous; bounded indexing drains later only after durable acknowledgement. Repair SP05-D1 with lease/ack semantics so interrupted drain work reappears after restart.

### M1-03: object relations, storage, and collection

Retain existing canonicalizer/file/zstd/store behavior. Record exact delta bases and reject delta-only persistence absent a durable base and exact round-trip proof. Mark unverified/legacy relations rather than guessing. Apply root retention for leases, pending writes, committed checkpoints, evidence references, delta bases, and rollback material before quota/expiry collection. SessionEnd records a recovery-needed state instead of finalizing unacknowledged work.

### M1-04: compatible migration and import

Version the new envelopes additively. Import legacy records side by side, map identities without replacing originals, checkpoint a durable cursor and idempotent import frontier, and transfer writing only after a stable handoff. Legacy unknown fidelity remains unknown. Run object/reference/query/semantic parity checks against the declared snapshot before cutover, use one writer after cutover, retain old readers and IDs, and verify compatible-reader or consistent-backup rollback both before and after the first new-format write.

### M2-01: derived-state authority and correction

Build derived-state records from immutable observation identities. Distinguish user corrections, decisions, tool observations, candidate extractions, hypotheses, and conflicts. Attach scope, dependency set and coverage, validity interval, supersession/correction lineage, transform version, and source evidence. A conflict is rendered as a conflict until resolved by an authorized source.

### M2-02: negative knowledge uncertainty

Keep the existing descriptor.MatchKey and Bloom exact confirmation. Add coverage/freshness metadata for dependencies, filter generation, watermark, and index/drain gaps. If coverage is incomplete, freshness unknown, or query fails, callers receive unavailable or uncertain applicability with reason and recovery direction. No heuristic rule may turn this into a prohibition; active/stale/absent remains reserved for backed records with known applicability.

### M2-03: retrieval handoff and security boundary

Define a durable evidence envelope for SP-13: identity, event/content relation, source and transform versions, fidelity, coverage, validity, omissions, pagination boundary, and distinct absent/unavailable/denied/corrupt/expired outcomes. Authorization is evaluated before previews or expansion; a hash is not authorization. Symlink/path/encoding/size/decompression checks remain before materialization. Retrieval may not bypass denied host reads, replay commands, call an external service, or expose secrets in logs.

## Test plan (TDD)

No tests, builds, benchmarks, probes, generators, installers, or Git actions run in this planning pass. Future tests are written failing first and run only under separately authorized implementation. Existing future commands, if still present when implementation begins, are `go run ./tools/devtool test`, `go test ./internal/store ./internal/observer ./internal/negknow`, and the relevant `test/e2e` package.

| ID | Future scenario, owner, and required artifact |
|---|---|
| T20-M1-01 | Raw host payload before normalization; **M1 owner**; envelope plus byte-round-trip/fidelity artifact |
| T20-M1-02 | Original, partial, redacted, truncated, binary, capture failure; **M1 owner**; distinct fidelity/error records |
| T20-M1-03 | Equal bytes from distinct events and duplicate/out-of-order/gap deliveries; **M1 owner**; identity/order/frontier artifact |
| T20-M1-04 | Durable object/reference/frontier crash cuts; **M1 owner**; restart manifest with no false publication |
| T20-M1-05 | Interrupted drain SP05-D1, bounded queue, retry, and SessionEnd; **M1 owner**; acknowledgement/recovery trace |
| T20-M1-06 | Exact delta base round-trip and absent/corrupt base; **M1 owner**; declared-base verification artifact |
| T20-M1-07 | GC under lease, pending write, delta base, checkpoint, evidence, rollback, quota, expiry; **M1 owner**; retained-root audit |
| T20-M1-08 | Legacy resume from durable cursor, duplicate import/idempotence, single-writer handoff, object/reference/query/semantic parity, cutover, and pre/post-new-write rollback; **M1 owner**; import manifest, consistent backup, and restore artifact |
| T20-M2-01 | Authority/correction/conflict/scope/supersession/dependency cases; **M2 owner**; state lineage artifact |
| T20-M2-02 | Unknown dependency coverage, stale filter generation/watermark, rebuild/query failure; **M2 owner**; uncertain/unavailable response, never active |
| T20-M2-03 | Retrieval exact/path/symbol/event/decision request envelope; **M2 + SP-13 owner**; fidelity/coverage/validity/omissions/pagination/error artifact |
| T20-M2-04 | Denied path, symlink, encoding, size/decompression, secret logging, command/external-service replay; **M2 + SP-13 owner**; authorization audit |
| T20-OPT-01 | Measured need for SQLite/FTS and patched WAL/backup/recovery proof; **M1 owner**; adoption decision or retained filesystem-store evidence |

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Split raw-fidelity/event identity, publication/frontier, delta/GC, importer/rollback and state/negative-knowledge cases into independently owned short groups, reusing the shared fixture/V4 worker. Deliberate crash/reader/writer interactions stay inside one isolated scenario; cover every T20 boundary across the groups.

**When broader checks are necessary.** Run real SP-13/M3 recovery seams when their producers/consumers are ready. Complete the full publication crash matrix, legacy parity, rollback and final integrated gates once per applicable candidate; splitting groups cannot omit crash cuts or reinterpret incomplete historical data. Keep quiet measurements separate.

The implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

All commits are future, conventional, and contingent on separate implementation authorization. They retain the SP-20 identifier without asserting present completion. Independent authoring and fixture preparation may overlap under the schedule below; the integration owner still lands the eight commit units in the listed order with their compatibility changes and required validation. Parallel authoring does not authorize concurrent schema edits, merges or data cutovers.

| Commit | Scope and validation gate |
|---|---|
| [ ] 1. `test(store): specify observation fidelity envelopes` | T20-M1-01/02 contract tests and frozen-record compatibility |
| [ ] 2. `feat(observer): capture raw evidence before transforms` | Additive envelope, transform/hash provenance, and fidelity results |
| [ ] 3. `fix(store): publish acknowledged durable frontiers` | T20-M1-03/04/05, including SP05-D1 recovery |
| [ ] 4. `feat(store): retain delta and lifecycle roots` | T20-M1-06/07 and quota/expiry audit |
| [ ] 5. `feat(store): migrate evidence envelopes compatibly` | T20-M1-08 import/parity/cutover/rollback drill |
| [ ] 6. `feat(state): model authority scope and uncertainty` | T20-M2-01/02 and negknow compatibility |
| [ ] 7. `fix(state): publish compatible uncertainty envelopes` | T20-M2-03/04 storage/state producer contract; SP-13 exclusively owns MCP consumer edits |
| [ ] 8. `test(storage): verify recovery and rollback evidence` | Consolidate crash/import/GC/restore artifacts and documentation; T20-OPT-01 is conditional only, never a required engine change |

## Subagent strategy

Future implementation has one integration owner after SP-19 reconciliation. Main exclusively owns shared schema/version contracts and commits. The storage owner owns `internal/observer` capture and `internal/store` publication/GC/import changes plus the specifically assigned daemon drain fix; state owner owns `internal/negknow` applicability and proposed derived-state files. Agree exact non-overlapping source and test paths before future delegation; neither edits MCP handlers. A migration tester owns newly assigned backup/import/crash fixtures, and an independent integrity/trust reviewer is read-only. Existing seams include `internal/store/tooluse.go`, `put.go`, `objects.go`, `flush.go`, `gc.go`, `internal/negknow/descriptor.go`, `ledger.go`, `staleness.go`, and the SP05-D1 drain locations in the ledger. M1 owns observer/store envelope, publication, GC, migration, and import artifacts; M2 owns derived-state and retrieval-envelope contracts. SP-13 owns MCP handler and protocol-consumer changes. A read-only reviewer verifies crash, migration, and security artifacts before M3 enablement. No owner rewrites retained observer/canon/file/zstd/store/negknow bodies merely to fit this plan.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). Preserve the single schema/integration owner and SP-13's exclusive MCP consumer ownership. Delegate only after SP-19, including M0-G0, and the shared durable-evidence/state contract.

| Existing role | Requested model and effort | Reason and boundary |
|---|---|---|
| Storage/publication owner | Fable 5.1 / high | External objects, index/frontier acknowledgement, legacy compatibility and GC interact across crash boundaries; main may retain this work when it already holds that context |
| State/negative-knowledge owner | Opus 4.8 / high | Bounded authority, supersession and uncertainty work against the frozen producer contract; no storage-schema redesign |
| Migration/fixture tester and early V4 verification owner | Opus 4.8 / high | One shared assignment designs repeat/import/crash/rollback cases and maps them to V4 requirements in separately assigned files and disposable stores; medium for collating known artifacts, low for exact inventory only |
| Independent integrity/trust reviewer | Fable 5.1 / high | Inspect the actual publication/import/GC evidence and authorization assumptions; must be a different thread from the storage author |

Storage and state may overlap once envelope ownership and compatibility are settled. Use one coordinator and one shared pool across SP-20 and cooperating V4-VERIFY work: at most three active children in total, at most one Fable, no nested delegation, also counting other coordinated SP-14–21 children under R1. Start the two author roles when ready; the migration/fixture role can occupy the third slot for independent V4 preparation without launching a second verification team. Main may retain an author role and use the freed slot for another ready task. No two roles edit one file or fixture; import/cutover writers remain serialized. Finish or pause the Fable author before starting the independent Fable reviewer in a different thread. Reuse author threads for their own fixes, never as their independent reviewers. Reduce concurrency on schema churn, memory pressure or lock/test interference; retain actual failures. Neither model agreement nor a worker summary satisfies the crash, legacy-reader or backup/rollback gate.

### Parallel delivery and V4 handoff

This future schedule implements [V4-VERIFY parallel preparation and validation](V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md#12-parallel-preparation-and-validation). Dispatch ready work as its inputs become available; do not wait for every author to finish before reviewing an accepted slice or preparing the next consumer check. Review the first substantive delegated slice before expanding the assignment under R1. Keep concise handoffs in the existing planning/evidence and future V4 report conventions; no new orchestration service is required.

| Readiness gate | Work that may overlap | Required handoff and serial boundary |
|---|---|---|
| SP-19, including accepted M0-G0 and shared architecture/schema contracts | Storage/publication author develops M1; state author develops M2 against the agreed envelope; shared fixture/V4 owner inventories actual tests and prepares independently owned cases | Coordinator records accepted integration HEAD, contract/version, exact source/test ownership and disposable resource allocation. Verify previously recorded merges; do not repeat completed integrations. Changed shared contracts return to the coordinator before dependent authoring resumes |
| First contract slice reviewed; individual producer or fixture becomes available | Fixture/V4 owner checks that slice while authors continue disjoint work. State fixtures need not wait for all storage/GC/import work; actual state-to-store tests wait for their real producer | Each handoff names snapshot, schema/interface revision, requirement IDs, changed files, actual artifacts and missing consumers. Commit 6/7 authoring can overlap M1 work, but integration keeps commits 1–8 in order. Contract fixtures alone do not establish runtime compatibility |
| Accepted M1/M2 producer slices are integrated | SP-13's existing owner can prepare or validate its corresponding consumer correction; V4 lifecycle/retrieval checks can use the available composed slices while unrelated remediation continues | SP-13 keeps MCP file ownership and consumes a pinned producer contract. It uses an available slot in the same pool. Cross-component results on intermediate snapshots are provisional; no M1–M3 enablement or final V4 signoff follows from them |
| Accepted remaining SP-19/M0 corrective work, SP-20 and required SP-10–13 consumer corrections are integrated and ready for acceptance | V4 owners divide final validation by requirement and resource availability; independent reviewer reads stable artifacts while compatible checks run | Final V4 evidence uses the common accepted candidate, including SP-19 M0-G1–G6 corrections and all required producers and consumers. Candidate files are immutable during validation. Final integration, quiet performance measurements, backup/cutover and acceptance retain their explicit serial gates |

The fixture/V4 owner maintains one requirement-to-check map for T20-M1-01–08, T20-M2-01–04 and conditional T20-OPT-01, including their V4 row IDs. Reuse one actual artifact for multiple requirements only when its assertions cover each one. Review definitions and confirm any future test selection matches real cases; a zero-test success is not evidence. Assign a single runner to each distinct check on a snapshot, rather than making every role rerun the whole suite. Per-commit checks cover touched packages and affected consumers under R2; the coordinator schedules broader validation only at its named integration gate or for a documented regression question.

Future write-producing checks use separate worktrees/copies at the recorded HEAD with separate stores, spools, checkpoints, backup destinations and daemon/IPC identities. Resolve fixtures and resource paths explicitly before concurrent execution. Never run two migration writers, a destructive crash case and a reader, or GC and rollback against the same test store except inside the single owned scenario that deliberately tests that interaction. Parallel isolated scenarios are permitted; shared data publication and cutover keep one owner. Keep production/user data outside those fixtures.

Validation scheduling follows V4-VERIFY §1.2: source/test authoring and read-only review may overlap independent work, but CPU-heavy suites already contain parallel package execution. Use one heavy validation job per machine initially; add another only with observed resource headroom and no timing interference, or use an already available independent runner. Quiet timing gates have exclusive runner access. A blocking dependency moves the worker to another ready assignment or ends the child; do not pay for idle polling or duplicate research. Record elapsed time, retries and exposed usage to assess this proposed efficiency policy; no speed or cost improvement is certified by the plan.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
All criteria are future and unchecked.

- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.
- [ ] Parallel handoffs record accepted inputs, unique file/resource ownership and one shared SP-20/V4 worker pool; provisional checks remain distinct from final integrated acceptance, with every T20 gate mapped to a real check and artifact.
- [ ] M1 captures or explicitly qualifies raw-host fidelity before every derivative and preserves event/content identity and ordering gaps.
- [ ] Durable publication, bounded deferred indexing, SP05-D1 recovery, and capture-failure non-replacement are demonstrated with artifacts.
- [ ] Delta, GC, quota/expiry, and SessionEnd recovery preserve every declared root class.
- [ ] Legacy import is side-by-side, resumable/idempotent, parity-checked, single-writer at cutover, and rollback-ready.
- [ ] M2 preserves authority, correction, conflict, scope, dependencies, supersession, coverage, and uncertainty.
- [ ] Negative knowledge retains exact confirmation and never converts unknown coverage or query failure into prohibition.
- [ ] SP-13 consumes the M2 retrieval envelope and distinguishes unavailable from absence without changing its negotiated versions.
- [ ] SQLite/FTS remains absent unless its measured, patched-runtime, backup, and recovery gates pass.

## Done checklist

### Planning-review checks

- [ ] This plan uses future-only tasks and commits; it claims no implementation completion.
- [ ] It retains existing observer/canon/file/zstd/store/negknow work and names SP05-D1 remediation.
- [ ] It makes raw fidelity, provenance, transforms, hashes, coverage, identity, observed/arrival ordering, publication, migration, GC, and state authority explicit.
- [ ] It places native output admission in SP-21 after M1–M3, with no eviction claim.
- [ ] It records SP-19 → SP-20 → SP-13 → M3 enablement dependencies.
- [ ] Planning edits stay within the assigned Markdown scope recorded in the ledger; the parallelism follow-up changes only SP-20, V4-VERIFY and the ledger, with no implementation validation commands run.

### Rollout, rollback and blockers

Enablement waits for SP-19 reconciliation, durable crash/drain evidence, compatible-reader or verified-backup proof, SP-13 unavailable-as-error integration, and M3 review. On any failed gate, keep new capture transformations and replacement disabled according to their independent switches, retain the prior reader or restore the verified backup, preserve evidence and diagnostics, and leave legacy data readable. Do not delete data to make a retry look clean.

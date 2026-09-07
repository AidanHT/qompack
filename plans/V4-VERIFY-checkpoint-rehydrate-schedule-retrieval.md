# V4 — Verification checkpoint after wave 3 (checkpointer, rehydrator, scheduler, retrieval)

**Status:** future revised migration checkpoint; original Wave 3 SP-10–13 are now user-reported complete. Preserve that completion history; combined integration and migration gates are unverified here. **Type:** verification plan. **Planning owner/model:** Astra coordinator (effective settings not exposed); independent planning review in [ledger](MIGRATION-EVIDENCE.md). No implementation validation performed.

## 0. When this runs, and where

Future branch `verify/v4` from the reconciled integrated `develop`, with an isolated matching worktree; not created now. SP-19 M0-00 first merges completed original SP10→SP11→SP12→SP13 under architecture §9 and records M0-G0 acceptance of the combined baseline. The remaining SP-19 tasks then run, followed by SP-20 and necessary consumer corrections. Full revised V4 verification follows that work; it is not a prerequisite for M0-G0. Preserve current sibling branches and original completion records. SP-13 core recovery and M1–M3 gates still precede dependent enablement; the original merges do not certify them.

V3 remains closed under its user waiver. Read its final addendum and current worktree owners before interpreting root stubs or early report cells. V4 does not reset V1–V3 or mark Wave 3 complete because this plan is written.

## 1. Ground rules, and how to execute this checkpoint

This is future validation under separate implementation authorization. No test/build/benchmark/replay/probe/installer/generator or Git action runs during the planning pass. Preserve current branch/worktree dirt, completed reports and frozen historical baselines. Any future working copies for comparative task runs must be isolated, with repository/environment snapshot identifiers so branches cannot mutate one another.

Use current source and test definitions, [architecture §0.1](00-ARCHITECTURE.md), [master plan](README.md) and [evidence ledger](MIGRATION-EVIDENCE.md). Run only the relevant future entry points after reviewing their actual arguments. Existing commands include `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool replay --ci`, and `go run ./tools/devtool bench-hotpath`; these are references, not execution here. Missing packaged-host/closed-loop/failure entry points must be added by their owning implementation task. A skipped integration names why and leaves its capability unverified.

Future fixes use the existing verification branch/worktree convention and small conventional commits, no attribution trailers. Proposed sequence: (1) inventory/contract regressions, (2) compatible corrections with fixtures, (3) migration/recovery integration, (4) controlled evaluation/packaging evidence, (5) report/rollback handoff. The coordinator owns shared integration, commits and report; subagents receive exact exclusive test/source scopes only after implementation is authorized. Planning model requests do not select product models.

### 1.1 Subagent partition

Future roles V4-A through V4-M retain exclusive SP01 through SP13 inventory scopes respectively. V4-E/F/H additionally coordinate SP-20 drain/capture/storage tests; V4-I/M coordinate M2 authority/retrieval; coordinator owns SP-19 integration and all shared daemon/bootstrap/schema files. Assign actual non-overlapping test files before execution. Independent lifecycle/integrity reviewer reads all failure/recovery artifacts. These are future roles, not extra P-stage children.

The eight retained whole-tree IDs are `V4-ALL-01`, `V4-ALL-02`, `V4-ALL-03`, `V4-ALL-04`, `V4-ALL-05`, `V4-ALL-06`, `V4-ALL-07`, `V4-ALL-08`. They cover future suite/race, supported-platform repetition, lint/import guards, approved formatting checks, installed manifest validation, replay, measured hot path and coherent gate/report review. Reconcile their original definitions and record changes; counts or a skipped canary cannot establish target support.

## 2. Cumulative functionality inventory

The original row identifiers remain below as reconciliation references. For each ID, the future inventory owner records the actual current test definition, revised assertion, result/artifact and any retirement/replacement reason. Old test names or historical checked reports are not proof of a current requirement. No row is silently discarded; obsolete success assertions use the current criterion in its owner plan. Existing package tests are inspected before adding missing entry points.

### 2.1 SP-01 — foundation/config/contracts

Retained IDs: `V4-SP01-01`, `V4-SP01-02`, `V4-SP01-03`, `V4-SP01-04`, `V4-SP01-05`, `V4-SP01-06`, `V4-SP01-07`, `V4-SP01-08`, `V4-SP01-09`, `V4-SP01-10`, `V4-SP01-11`, `V4-SP01-12`, `V4-SP01-13`, `V4-SP01-14`.

- [ ] Future owner reconciles every retained row against SP-19 M0-G1/G2/G4; retained package/CLI/import guards, versioned settings and supported capability states; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.2 SP-02 — replay/accounting/baseline

Retained IDs: `V4-SP02-01`, `V4-SP02-02`, `V4-SP02-03`, `V4-SP02-04`, `V4-SP02-05`, `V4-SP02-06`, `V4-SP02-06b`, `V4-SP02-07`, `V4-SP02-08`, `V4-SP02-09`, `V4-SP02-10`.

- [ ] Future owner reconciles every retained row against SP-19 M0-G5/G6; preserve old synthetic outputs, fix labels/corpus together, request categories and missing telemetry; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.3 SP-03 — sketches

Retained IDs: `V4-SP03-01`, `V4-SP03-02`, `V4-SP03-03`, `V4-SP03-04`, `V4-SP03-05`, `V4-SP03-06`, `V4-SP03-07`.

- [ ] Future owner reconciles every retained row against SP-20 T20-M2-02; exact positives, covered negatives, bounded capacity; Misra–Gries candidates verified before exactness claims; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.4 SP-04 — chunk/canon/symbols

Retained IDs: `V4-SP04-01`, `V4-SP04-02`, `V4-SP04-03`, `V4-SP04-04`, `V4-SP04-05`, `V4-SP04-06`, `V4-SP04-07`.

- [ ] Future owner reconciles every retained row against SP-20 T20-M1-01/02/06; permitted payload fidelity, semantic/physical spans and parser fallback; canonicalization is derived; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.5 SP-05 — daemon/IPC/contracts

Retained IDs: `V4-SP05-01`, `V4-SP05-02`, `V4-SP05-03`, `V4-SP05-04`, `V4-SP05-05`, `V4-SP05-06`, `V4-SP05-07`, `V4-SP05-08`, `V4-SP05-09`, `V4-SP05-10`, `V4-SP05-11`, `V4-SP05-12`, `V4-SP05-13`, `V4-SP05-14`, `V4-SP05-15`.

- [ ] Future owner reconciles every retained row against SP-19 M0-G2/G3 plus SP-20 T20-M1-03/04/05; durable acknowledgements, bounded workers and qualified event coverage; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.6 SP-06 — store/redaction/tokens

Retained IDs: `V4-SP06-01`, `V4-SP06-02`, `V4-SP06-03`, `V4-SP06-04`, `V4-SP06-05`, `V4-SP06-06`, `V4-SP06-07`, `V4-SP06-08`, `V4-SP06-09`, `V4-SP06-10`.

- [ ] Future owner reconciles every retained row against SP-20 M1 gates; object/index publication, backup/import/GC and privacy before persistence; assembled estimates calibrated, no exact chunk-sum claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.7 SP-07 — DAG/dependencies

Retained IDs: `V4-SP07-01`, `V4-SP07-02`, `V4-SP07-03`, `V4-SP07-04`, `V4-SP07-05`, `V4-SP07-06`, `V4-SP07-07`, `V4-SP07-08`.

- [ ] Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-15 M5-G15-B; approximate observed relation graph, unknown dependency coverage retained; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.8 SP-08 — observer

Retained IDs: `V4-SP08-01`, `V4-SP08-02`, `V4-SP08-03`, `V4-SP08-04`, `V4-SP08-05`, `V4-SP08-06`, `V4-SP08-07`, `V4-SP08-08`, `V4-SP08-09`.

- [ ] Future owner reconciles every retained row against SP-20 T20-M1-01–05; captured host payload versus full process/file output, distinct events with equal content, child/gap provenance; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.9 SP-09 — negative knowledge

Retained IDs: `V4-SP09-01`, `V4-SP09-02`, `V4-SP09-03`, `V4-SP09-04`, `V4-SP09-05`, `V4-SP09-06`, `V4-SP09-07`, `V4-SP09-08`, `V4-SP09-09`.

- [ ] Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-13 T13-STATE; reason-independent identity/exact confirmation retained, errors not absence; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.10 SP-10 — checkpoint/pins

Retained IDs: `V4-SP10-01`, `V4-SP10-02`, `V4-SP10-03`, `V4-SP10-04`, `V4-SP10-05`, `V4-SP10-06`, `V4-SP10-07`, `V4-SP10-08`, `V4-SP10-09`, `V4-SP10-10`, `V4-SP10-11`, `V4-SP10-12`, `V4-SP10-13`, `V4-SP10-14`, `V4-SP10-15`, `V4-SP10-16`, `V4-SP10-17`, `V4-SP10-18`, `V4-SP10-19`, `V4-SP10-20`, `V4-SP10-21`, `V4-SP10-22`, `V4-SP10-23`.

- [ ] Future owner reconciles every retained row against SP-10 Test plan gates; committed frontier, compatible readers, lifecycle gaps, complete records and explicit overflow; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.11 SP-11 — rehydration/rules/skills

Retained IDs: `V4-SP11-01`, `V4-SP11-02`, `V4-SP11-03`, `V4-SP11-04`, `V4-SP11-05`, `V4-SP11-06`, `V4-SP11-07`, `V4-SP11-08`, `V4-SP11-09`, `V4-SP11-10`, `V4-SP11-11`, `V4-SP11-12`, `V4-SP11-13`, `V4-SP11-14`, `V4-SP11-15`, `V4-SP11-16`, `V4-SP11-17`, `V4-SP11-18`, `V4-SP11-19`, `V4-SP11-20`, `V4-SP11-21`, `V4-SP11-22`, `V4-SP11-23`, `V4-SP11-24`.

- [ ] Future owner reconciles every retained row against SP-11 T11-AUTH/BUDGET/LIFE/LOAD/SCOPE/POINTER/CORRECT/ROLLBACK; current intent and bounded extra context; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.12 SP-12 — scheduler

Retained IDs: `V4-SP12-01`, `V4-SP12-02`, `V4-SP12-03`, `V4-SP12-03b`, `V4-SP12-04`, `V4-SP12-05`, `V4-SP12-06`, `V4-SP12-07`, `V4-SP12-08`, `V4-SP12-09`, `V4-SP12-10`, `V4-SP12-11`, `V4-SP12-12`, `V4-SP12-13`, `V4-SP12-14`, `V4-SP12-15`, `V4-SP12-16`, `V4-SP12-17`, `V4-SP12-18`, `V4-SP12-19`, `V4-SP12-20`, `V4-SP12-21`, `V4-SP12-22`, `V4-SP12-23`, `V4-SP12-24`, `V4-SP12-25`.

- [ ] Future owner reconciles every retained row against SP-12 M5-G12-A–E; useful local cadence, unknown observations safe, no native cut/veto or O(delta) claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.13 SP-13 — MCP retrieval

Retained IDs: `V4-SP13-01`, `V4-SP13-02`, `V4-SP13-03`, `V4-SP13-04`, `V4-SP13-05`, `V4-SP13-06`, `V4-SP13-07`, `V4-SP13-08`, `V4-SP13-09`, `V4-SP13-10`, `V4-SP13-11`, `V4-SP13-12`, `V4-SP13-13`, `V4-SP13-14`, `V4-SP13-15`, `V4-SP13-16`, `V4-SP13-17`, `V4-SP13-18`, `V4-SP13-19`, `V4-SP13-20`, `V4-SP13-21`, `V4-SP13-22`, `V4-SP13-23`, `V4-SP13-24`.

- [ ] Future owner reconciles every retained row against SP-13 T13-PROTOCOL through T13-ROLLBACK; installed authorized archive search/expansion, history/current and errors distinct; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### Migration additions without renumbering prior rows

- [ ] SP-19 M0-G0: four original delivery-to-merge mappings, semantic/conflict review, combined-baseline validation and independent acceptance predate the remaining SP-19 work. Preserve inherited failures and original completion evidence separately from this revised checkpoint.
- [ ] SP-19/M0: mapping, packaged capability/lifecycle canaries, request ledger, settings and baseline provenance.
- [ ] SP-20/M1–M2: capture/privacy, crash/publication, resumable migration/backup/rollback, state authority, uncertainty, retention and scope gates.

## 3. Exit-criteria re-verification

- [ ] Reverify the actual current contract for SP01–13 and corrective SP19/20; preserve historical results as baseline artifacts, not new passes.
- [ ] SP-19 M0-G0 records the accepted combined baseline; M0-G1–G6 subsequently record mapping, capabilities, packaging, request accounting and corpus/CI carry.
- [ ] SP-20 T20-M1/M2 demonstrates capture/publication/migration/authority/trust, and SP-13 demonstrates core installed retrieval before dependent M3 enablement.
- [ ] SP-10/SP-11 lifecycle, current-state authority, complete serialization/overflow, qualified coverage and rollback gates pass.
- [ ] SP-12 M5-G12 local scheduling functions with unknown observations and native controls disabled; predictive policies may remain experimental/disabled.
- [ ] Every mandatory scenario in the ledger has an owner/artifact; unresolved capabilities are disabled with an explicit residual.

## 4. New cross-component integration tests

Historical proposed test names are retained as identifiers, not assertions that these tests exist or pass. Correct their assertions/names compatibly during future implementation and record the mapping; unsafe guarantees below are retired.

| Retained section / test identifier | Current future criterion and owner |
|---|---|
| 4.1 `TestV4_PreCompactToCheckpointToRehydrateRoundTrip` | [ ] SP-10/SP-11: durable frontier/authority, optional PostCompact, bounded complete-record overflow; recovery manifest |
| 4.2 `TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold` | [ ] SP-12: simple local cadence with unknown headroom; no simulated threshold proof of host trigger; decision trace |
| 4.3 `TestV4_O1SpanInstructionFromARealCheckpointFrontier` | [ ] Retire custom_instructions output-setter and native-input-reduction assertion; SP-19 adapter and SP-10 checkpoint contract canaries |
| 4.4 `TestV4_FrontierAdvancementKeepsResidualSpanODelta` | [ ] SP-10/SP-12: measure Qompack frontier work separately from full native summary request; no native O(delta); latency/request ledger |
| 4.5 `TestV4_TombstoneToRecallToExpandRoundTrip` | [ ] SP-20/SP-13: durably published authorized handle through installed retrieval after compaction; partial/legacy fidelity transcript |
| 4.6 `TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction` | [ ] SP-20/SP-13/SP-11: exact current/uncertain/stale/unavailable behavior and old-caller compatibility; state lineage |
| 4.7 `TestV4_EphemeralRetrievalResultsRankFirstForEviction` | [ ] Retire native eviction assertion; SP-12/SP-15 future Qompack representation ranking only; unsupported action guard |
| 4.8 `TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint` | [ ] SP-10/SP-13: wrappers retain original provenance, no new independent primary evidence or duplicate semantic state; lifecycle trace |
| 4.9 `TestV4_GrowthGuardrailWithCheckpointsAndEphemerals` | [ ] SP-20: quotas/expiry/GC roots, unique data can grow linearly; bounded-maintenance resource report |
| 4.10 `TestV4_WhyAndDroppedAnswerFromRealProducers` | [ ] SP-13/SP-11: attributed decision and qualified coverage, no proof native completeness/model compliance; response fixtures |
| 4.11 `TestV4_LiveSessionWriteSetAppendOnlyAndImmutability` | [ ] SP-20: privacy/permission/symlink and captured-original retention, immutable evidence plus documented metadata mutations; trust audit |
| 4.12 `TestV4_DegradedPassiveWithEverySubsystem` | [ ] SP-19/SP-20/SP-13: per-capability unknown, capture/index/retrieval failure policy and safe local utility; failure matrix |
| 4.13 `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet` | [ ] SP-19/SP-20: startup/I/O/payload/lock tail measurements and durable spool crash outcomes; measured distributions |
| 4.14 `TestV4_EveryContractAssertionHasARealProducer` | [ ] SP-19: separate capability evidence and missing states; retire fixed 9/0 and setter-success monitor claims; packaged canaries |

### 4.15 Test registration and CI wiring

Future owning tasks add missing entry points and register only supported tests; retain skips with reasons and capability status. CI changes are future work only. Unit simulation and real installed-host artifacts remain separate.

## 5. Performance budget validation

Old B-A–B-F, L0/L5 and individual benchmark labels remain historical comparison targets where applicable. Reconcile each with its actual workload, binary/provider/model/OS, payload size, process startup, I/O, locking, sample distribution and date. No universal 15 ms, 4:1, sublinear growth, near-zero distortion, O(delta) native latency or first-turn savings is an acceptance fact.

Measure Qompack-added tokens and total observed context separately. Count the assembled representation using the supported estimator, label estimation and model changes, and calibrate against reported usage. Preserve old fraction-of-OPT outputs as diagnostics for their synthetic harness/objective; classic paging OPT is not a task-quality ceiling. Re-reading a changed file can be useful.

The request ledger separates uncached input, cache reads, cache writes/TTL, output, retries, failed/aborted trials, compaction, subagents and Qompack model calls if any. Record provider/model/rate-table date/pricing mode/completeness; missing telemetry is unknown, not zero. Estimated price is category usage times applicable rates plus relevant non-token charges; reported usage, estimates and invoiced cost remain separate. Subscription allowance is not automatically cash per token.

Declare regression margins and sample-size rationale before outcomes. Use repeated stochastic baselines, held-out tasks and changing requirements. Primary outcomes are task completion, constraints/regressions and recoverability; cost, latency, context, retrieval burden, repeated work, CPU/storage and tail delays accompany them. Report uncertainty, exclusions, failed trials and inconclusive outcomes; twenty runs cannot establish an unsupported two-percent guarantee. Cost and correctness gates remain independent. Record timings on quiet runners after future authoring completes.

## 6. Regression

Preserve Waves 0–2 and the V3 report/addendum unchanged. The 2026-08-26 user waiver closed V3 despite J5 billing blockage; J5 run 32932419445 and three-platform p99 backfill remain waived-open until actual evidence. Do not reinterpret early held-gate cells as a project restart.

Carry SP05-D1 to SP-20 drain/ack recovery, SP02-D1–D6 as one V4 corpus/rebaseline unit, and SP06-D2/SP08-D1 as a paired performance decision. Preserve SP04-D2/D3 and SP06-D1 wontfix rationale while testing new fidelity/retention contracts; SP04-D7's delta consumer is assigned to checkpoints. Reconcile SP11-C28 frontierOf/Ref.Frontier against the active sibling. Existing SP07 NodeID/generation/fixture rulings in V2-SP07-handoff.md remain regression context.

No baseline regeneration, fixture relabeling or machine-read carry update can erase a failure. Future updates to CARRIED-DEFECTS.tsv require actual evidence under its existing guard. Documentation v1.5 is the authorized planning baseline; the former rule demanding byte identity of Qompack.md to the initial commit is retired for this explicit revision.

## 7. Failure protocol

Record severity, exact command/artifact, snapshot, version, reproduction conditions and affected owner. A unit pass cannot override a failed or skipped host gate. Missing coverage and retrieval failures remain unknown/unavailable, never absence or successful restoration.

The future integration owner makes the smallest compatible fix, rechecks affected gates and dependencies, and preserves original failures. Disable recording, reinjection, replacement and experimental policies independently according to the fault/privacy policy. Optimization failure normally passes through; privacy denial follows denial policy.

Before data cutover use an engine-supported consistent backup and stable writer frontier. Rollback before and after new-format writes must be rehearsed: use compatible readers or the verified backup, with explicit treatment of later writes an older binary cannot read. Stop incompatible writers, retain object IDs/evidence/rollback roots and do not promise automatic downgrade. Failed mandatory gates block affected enablement/release; optional-disabled policies are identified explicitly.

## 8. Completion report template

Migration rows are additive and do not renumber prior inventory. The future report must retain every original row ID with its explicit current assertion/result or documented retirement/replacement mapping.

Future report retains the existing V-report convention and records: branch/HEAD and dirty baseline; date, supported OS/provider/model/host/plugin/schema versions; inventory row-by-row result and old-to-new assertion map; completed work preserved; commands actually run and artifact paths; skips/failures/waivers; migration/capability gate statuses; request-usage completeness and estimated-price provenance; statistical outcomes; privacy/retention and rollback drill; independent findings and resolution; remaining blockers and next authorized action.

Use `documented`, `verified_in_target`, `implemented_unverified`, `unsupported`, `experimental` and `unknown` appropriately. A new report must not copy old PASS cells as new runs. Existing completion reports are immutable historical records. Proposed future report paths follow V4-report.md, V5-report.md and V6-report.md naming; these reports are not created in the planning pass.

Retain original report subsections 1–13 for SP01–13 inventory, 14 whole-tree gates, 15 exits, 16 new integration, 17 budgets, 18 regression and 19 final sign-off. Add SP19/20 remediation and capability/rollback evidence within the relevant subsections, with no silent row omissions.

## 9. Gate

- [ ] Future V4 report reconciles every retained inventory/test identifier and all M0–M3 gates with inspected artifacts.
- [ ] Installed recovery/trust and crash/rollback requirements pass for each enabled feature; optional controls remain explicitly disabled.
- [ ] Independent review has no unresolved mandatory blocker. Preserve waived-open CI obligations rather than fabricating a pass.
- [ ] Only then may a separately authorized future integration close the revised V4 migration gate, merge `verify/v4` under repository policy and consider the historical `v0.3.0` release marker. Preserve the user's original Wave 3 completion and any existing sign-off; this corrective verification does not rewrite history. No such action is taken here.

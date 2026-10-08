# V6 — Verification checkpoint: production readiness and UAT

**Type:** verification checkpoint. **Status:** execution attempted 2026-09-20; BLOCKED and incomplete. **Close-out status (2026-10-08):** the close-out that followed is recorded in the [V6 close-out report](V6-CLOSEOUT-report.md); 0.3.0 was released from candidate 8 (D79, D80), and the boxes below are ticked with candidate 8 evidence or left open with a reason. **Branch:** `verify/v6` from integrated `develop`, isolated `../qompack-v6` worktree. See [V6 report](V6-report.md) for actual runs and unresolved gates. **Planning owner/model:** Astra coordinator (effective metadata not exposed); independent planning review in [ledger](MIGRATION-EVIDENCE.md).

The original future-tense procedure below remains the acceptance contract. Unchecked boxes are unresolved obligations, not an assertion that no focused execution occurred. The report records the separately authorized implementation session and does not grant release/publication authority.

## 0. When this runs, and on what

Run only in a separately authorized implementation/release session after SP17→SP18 integration and V5 prerequisites. Confirm the actual release candidate, all owner handoffs and any disabled optional surfaces. Do not recreate already active branches or demand a destructive re-merge to conceal history; record any ordering discrepancy and assess its contract consequence.

This is future validation under separate implementation authorization. No test/build/benchmark/replay/probe/installer/generator or Git action runs during the planning pass. Preserve current branch/worktree dirt, completed reports and frozen historical baselines. Any future working copies for comparative task runs must be isolated, with repository/environment snapshot identifiers so branches cannot mutate one another.

Use current source and test definitions, [architecture §0.1](00-ARCHITECTURE.md), [master plan](README.md) and [evidence ledger](MIGRATION-EVIDENCE.md). Run only the relevant future entry points after reviewing their actual arguments. Existing commands include `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool replay --ci`, and `go run ./tools/devtool bench-hotpath`; these are references, not execution here. Missing packaged-host/closed-loop/failure entry points must be added by their owning implementation task. A skipped integration names why and leaves its capability unverified.

Future fixes use the existing verification branch/worktree convention and small conventional commits, no attribution trailers. Proposed sequence: (1) inventory/contract regressions, (2) compatible corrections with fixtures, (3) migration/recovery integration, (4) controlled evaluation/packaging evidence, (5) report/rollback handoff. The coordinator owns shared integration, commits and report; subagents receive exact exclusive test/source scopes only after implementation is authorized. Planning model requests do not select product models.

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every command catalog, commit and acceptance row below. Broad commands are available entry points, not a per-edit/per-owner execution list. Use focused real cases first and require a named reason for each long run; preserve coverage, failure artifacts and explicit incomplete states. No execution occurs during planning.

Begin with short checks of changed package/launcher/schema, permission/path, archive/recovery and documentation/UAT contracts. Independent platform and installed-package scenarios may overlap on already available isolated runners; each scenario keeps its own bundle identity, installation, store, IPC and backup. Reuse SP17/SP18 work within this checkpoint through one requirement-to-artifact map rather than rerunning a release matrix per reviewer or UAT row.

The longer supported-platform installation/upgrade/uninstall, failure/rollback, human UAT and held-out release evaluations remain final release obligations. Run them against the identified artifact once per required mode/scenario/trial, after focused prerequisites pass. Preserve all original inventory/UAT identifiers and required samples; no historical-source pass certifies a different installed bundle. Use V4's reviewed race/e2e contract, independently instrumenting child product paths when race coverage requires it. Report-only/prose changes use R2's separate tested-source and documentation HEADs; they do not automatically repeat the release suite.

Reuse the existing logical owners and R1 model/effort/fallback policy, narrowed by the **V6 routing override (user directive, 2026-09-14): every subagent this checkpoint dispatches runs Opus 4.8 (`claude-opus-4-8`) strictly; Fable 5.1 (`claude-fable-5-1`) is the main/coordinator session model and is never a child.** Opus 4.8 high for substantive validation, low/medium only for constrained inventory/collation, and Opus 4.8 high in a non-authoring thread for the necessary independent critical review — the shape R1 already documents for an unavailable Fable — escalating that one seat to Opus xhigh only for a documented unresolved issue. Shared-contract, durable-data/rollback and trust-boundary questions return to the main session instead of a premium child. All cooperating SP14–21 and V4–V6 work shares at most three active children with no nesting; under this override V6 dispatches no Fable child, and R1's one-Fable slot stays unused rather than becoming extra Opus concurrency. Narrower plan limits remain. The coordinator owns run allocation, final report and acceptance. Do not buy extra capacity or create configuration to force parallelism. **Observed route, measured in this repository 2026-09-15.** The in-session Agent tool's `opus` alias resolves to Opus 5 (`claude-opus-5[1m]`), and `.claude/agents/*.md` definitions pinned to `claude-opus-4-8` do not load mid-session, so neither satisfies this override. The verified route is a headless child launched from Bash in the task's worktree: `env -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION claude -p --model claude-opus-4-8 --output-format json --dangerously-skip-permissions`, whose result JSON reported `modelUsage.canonicalModel = claude-opus-4-8`. Per R1, read requested versus observed routing from that field before substantive work; an alias name is not evidence of the effective route. The child runs at the executing session's own existing permission posture — this override still does not authorize changing permissions, agent/config files, provider, authentication or billing to obtain the route.

## 1. Cumulative functionality inventory

The original row identifiers remain below as reconciliation references. For each ID, the future inventory owner records the actual current test definition, revised assertion, result/artifact and any retirement/replacement reason. Old test names or historical checked reports are not proof of a current requirement. No row is silently discarded; obsolete success assertions use the current criterion in its owner plan. Existing package tests are inspected before adding missing entry points.

### 1.1 SP-01 — foundation/config/contracts

Retained IDs: `1.1.1`, `1.1.2`, `1.1.3`, `1.1.4`, `1.1.5`, `1.1.6`, `1.1.7`, `1.1.8`, `1.1.9`, `1.1.10`, `1.1.11`, `1.1.12`, `1.1.13`, `1.1.14`, `1.1.15`, `1.1.16`, `1.1.17`, `1.1.18`, `1.1.19`, `1.1.20`, `1.1.21`, `1.1.22`, `1.1.23`, `1.1.24`, `1.1.25`, `1.1.26`, `1.1.27`, `1.1.28`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.1 row dispositioned with its old-to-new assertion map, 26 `verified_in_target`, 2 `documented`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-19 M0-G1/G2/G4; retained package/CLI/import guards, versioned settings and supported capability states; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.2 SP-02 — replay/accounting/baseline

Retained IDs: `1.2.1`, `1.2.2`, `1.2.3`, `1.2.4`, `1.2.5`, `1.2.6`, `1.2.7`, `1.2.8`, `1.2.9`, `1.2.10`, `1.2.11`, `1.2.12`, `1.2.13`, `1.2.14`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.2 row dispositioned with its old-to-new assertion map, 14 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-19 M0-G5/G6; preserve old synthetic outputs, fix labels/corpus together, request categories and missing telemetry; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.3 SP-03 — sketches

Retained IDs: `1.3.1`, `1.3.2`, `1.3.3`, `1.3.4`, `1.3.5`, `1.3.6`, `1.3.7`, `1.3.8`, `1.3.9`, `1.3.10`, `1.3.11`, `1.3.12`, `1.3.13`, `1.3.14`, `1.3.15`, `1.3.16`, `1.3.17`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.3 row dispositioned with its old-to-new assertion map, 17 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 T20-M2-02; exact positives, covered negatives, bounded capacity; Misra–Gries candidates verified before exactness claims; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.4 SP-04 — chunk/canon/symbols

Retained IDs: `1.4.1`, `1.4.2`, `1.4.3`, `1.4.4`, `1.4.5`, `1.4.6`, `1.4.7`, `1.4.8`, `1.4.9`, `1.4.10`, `1.4.11`, `1.4.12`, `1.4.13`, `1.4.14`, `1.4.15`, `1.4.16`, `1.4.17`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.4 row dispositioned with its old-to-new assertion map, 17 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 T20-M1-01/02/06; permitted payload fidelity, semantic/physical spans and parser fallback; canonicalization is derived; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.5 SP-05 — daemon/IPC/contracts

Retained IDs: `1.5.1`, `1.5.2`, `1.5.3`, `1.5.4`, `1.5.5`, `1.5.6`, `1.5.7`, `1.5.8`, `1.5.9`, `1.5.10`, `1.5.11`, `1.5.12`, `1.5.13`, `1.5.14`, `1.5.15`, `1.5.16`, `1.5.17`, `1.5.18`, `1.5.19`, `1.5.20`, `1.5.21`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.5 row dispositioned with its old-to-new assertion map, 20 `verified_in_target`, 1 `partial_verified`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-19 M0-G2/G3 plus SP-20 T20-M1-03/04/05; durable acknowledgements, bounded workers and qualified event coverage; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.6 SP-06 — store/redaction/tokens

Retained IDs: `1.6.1`, `1.6.2`, `1.6.3`, `1.6.4`, `1.6.5`, `1.6.6`, `1.6.7`, `1.6.8`, `1.6.9`, `1.6.10`, `1.6.11`, `1.6.12`, `1.6.13`, `1.6.14`, `1.6.15`, `1.6.16`, `1.6.17`, `1.6.18`, `1.6.19`, `1.6.20`, `1.6.21`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.6 row dispositioned with its old-to-new assertion map, 21 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 M1 gates; object/index publication, backup/import/GC and privacy before persistence; assembled estimates calibrated, no exact chunk-sum claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.7 SP-07 — DAG/dependencies

Retained IDs: `1.7.1`, `1.7.2`, `1.7.3`, `1.7.4`, `1.7.5`, `1.7.6`, `1.7.7`, `1.7.8`, `1.7.9`, `1.7.10`, `1.7.11`, `1.7.12`, `1.7.13`, `1.7.14`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.7 row dispositioned with its old-to-new assertion map, 14 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-15 M5-G15-B; approximate observed relation graph, unknown dependency coverage retained; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.8 SP-08 — observer

Retained IDs: `1.8.1`, `1.8.2`, `1.8.3`, `1.8.4`, `1.8.5`, `1.8.6`, `1.8.7`, `1.8.8`, `1.8.9`, `1.8.10`, `1.8.11`, `1.8.12`, `1.8.13`, `1.8.14`, `1.8.15`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.8 row dispositioned with its old-to-new assertion map, 15 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 T20-M1-01–05; captured host payload versus full process/file output, distinct events with equal content, child/gap provenance; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.9 SP-09 — negative knowledge

Retained IDs: `1.9.1`, `1.9.2`, `1.9.3`, `1.9.4`, `1.9.5`, `1.9.6`, `1.9.7`, `1.9.8`, `1.9.9`, `1.9.10`, `1.9.11`, `1.9.12`, `1.9.13`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.9 row dispositioned with its old-to-new assertion map, 13 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-13 T13-STATE; reason-independent identity/exact confirmation retained, errors not absence; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.10 SP-10 — checkpoint/pins

Retained IDs: `1.10.1`, `1.10.2`, `1.10.3`, `1.10.4`, `1.10.5`, `1.10.6`, `1.10.7`, `1.10.8`, `1.10.9`, `1.10.10`, `1.10.11`, `1.10.12`, `1.10.13`, `1.10.14`, `1.10.15`, `1.10.16`, `1.10.17`, `1.10.18`, `1.10.19`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.10 row dispositioned with its old-to-new assertion map, 18 `verified_in_target`, 1 `unknown`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-10 Test plan gates; committed frontier, compatible readers, lifecycle gaps, complete records and explicit overflow; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.11 SP-11 — rehydration/rules/skills

Retained IDs: `1.11.1`, `1.11.2`, `1.11.3`, `1.11.4`, `1.11.5`, `1.11.6`, `1.11.7`, `1.11.8`, `1.11.9`, `1.11.10`, `1.11.11`, `1.11.12`, `1.11.13`, `1.11.14`, `1.11.15`, `1.11.16`, `1.11.17`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.11 row dispositioned with its old-to-new assertion map, 16 `verified_in_target`, 1 `partial_verified`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-11 T11-AUTH/BUDGET/LIFE/LOAD/SCOPE/POINTER/CORRECT/ROLLBACK; current intent and bounded extra context; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.12 SP-12 — scheduler

Retained IDs: `1.12.1`, `1.12.2`, `1.12.3`, `1.12.4`, `1.12.5`, `1.12.6`, `1.12.7`, `1.12.8`, `1.12.9`, `1.12.10`, `1.12.11`, `1.12.12`, `1.12.13`, `1.12.14`, `1.12.15`, `1.12.16`, `1.12.17`, `1.12.18`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.12 row dispositioned with its old-to-new assertion map, 17 `verified_in_target`, 1 `partial_verified`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-12 M5-G12-A–E; useful local cadence, unknown observations safe, no native cut/veto or O(delta) claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.13 SP-13 — MCP retrieval

Retained IDs: `1.13.1`, `1.13.2`, `1.13.3`, `1.13.4`, `1.13.5`, `1.13.6`, `1.13.7`, `1.13.8`, `1.13.9`, `1.13.10`, `1.13.11`, `1.13.12`, `1.13.13`, `1.13.14`, `1.13.15`, `1.13.16`, `1.13.17`, `1.13.18`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.13 row dispositioned with its old-to-new assertion map, 18 `verified_in_target`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-13 T13-PROTOCOL through T13-ROLLBACK; installed authorized archive search/expansion, history/current and errors distinct; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.14 SP-14 — commands/observability

Retained IDs: `1.14.1`, `1.14.2`, `1.14.3`, `1.14.4`, `1.14.5`, `1.14.6`, `1.14.7`, `1.14.8`, `1.14.9`, `1.14.10`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.14 row dispositioned with its old-to-new assertion map, 8 `verified_in_target`, 1 `partial_verified`, 1 `unsupported`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-14 gates; stable command/JSON/exit semantics and honest status/accounting/coverage; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.15 SP-15 — selection/grammar

Retained IDs: `1.15.1`, `1.15.2`, `1.15.3`, `1.15.4`, `1.15.5`, `1.15.6`, `1.15.7`, `1.15.8`, `1.15.9`, `1.15.10`, `1.15.11`, `1.15.12`, `1.15.13`, `1.15.14`, `1.15.15`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.15 row dispositioned with its old-to-new assertion map, 12 `verified_in_target`, 1 `unknown`, 2 `unsupported`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-15 M5-G15-A/B/C and M6-G15-A/B; feasible actual consumer, diagnostic signals, bounded progress-aware warnings; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.16 SP-16 — reuse/refinement

Retained IDs: `1.16.1`, `1.16.2`, `1.16.3`, `1.16.4`, `1.16.5`, `1.16.6`, `1.16.7`, `1.16.8`, `1.16.9`, `1.16.10`, `1.16.11`, `1.16.12`, `1.16.13`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.16 row dispositioned with its old-to-new assertion map, 11 `verified_in_target`, 2 `unknown`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-16 M6-G16-A–E; scoped applicability/expiry, bounded retrieval, future-only promotion and optional ablations; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.17 SP-17 — packaging/release

Retained IDs: `1.17.1`, `1.17.2`, `1.17.3`, `1.17.4`, `1.17.5`, `1.17.6`, `1.17.7`, `1.17.8`, `1.17.9`, `1.17.10`, `1.17.11`, `1.17.12`, `1.17.13`, `1.17.14`, `1.17.15`, `1.17.16`, `1.17.17`, `1.17.18`, `1.17.19`, `1.17.20`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.17 row dispositioned with its old-to-new assertion map, 11 `verified_in_target`, 7 `partial_verified`, 2 `unknown`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-17 SP17-M7-01–08; installed OS/schema/upgrade/privacy/license/rollback scope; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 1.18 SP-18 — docs/UAT

Retained IDs: `1.18.1`, `1.18.2`, `1.18.3`, `1.18.4`, `1.18.5`, `1.18.6`, `1.18.7`, `1.18.8`, `1.18.9`, `1.18.10`, `1.18.11`, `1.18.12`, `1.18.13`, `1.18.14`.

- [x] *(C6.2 on candidate 8, `36dc82e4`: every 1.18 row dispositioned with its old-to-new assertion map, 7 `verified_in_target`, 5 `partial_verified`, 1 `unknown`, 1 `documented`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Future owner reconciles every retained row against SP-18 SP18-M7-01–07 and UAT-01–12; documentation matches shipped evidence and real authorized workflows; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### Migration additions without renumbering prior rows

- [x] *(candidate 8: the canary, switch and settings tests green on Windows and Linux; `runtime.migration.settingsVersion` degraded as designed in a real session (C4.9 (b)); baseline provenance is row 1.1.1; per-category request usage from the host's JSON in C5.5; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md), "SP-19, SP-20 and SP-21 switches")* SP-19/M0: mapping, packaged capability/lifecycle canaries, request ledger, settings and baseline provenance.
- [x] *(candidate 8: rollover ships enabled with SP20-D4 `fixed` and its evidence test green; raw evidence capture and the durable publication frontier are recorded `experimental`, refused until their gates, never passed; C1.6 startup accounting and C1.7 restore on the packaged bundle; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-20/M1–M2: capture/privacy, crash/publication, resumable migration/backup/rollback, state authority, uncertainty, retention and scope gates.
- [x] *(candidate 8: `runtime.migration.replacement.newResult` ships off and is recorded `experimental`, never passed; UAT-11, admission off passes output through unmodified, passed on the frozen bundle (D76); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-21/M4: independently opt-in admission gate, every pointer resolves, processed-envelope recursion, fidelity, privacy and unmodified-output comparison. Disabled admission is recorded as disabled, never passed.

## 2. Exit-criteria re-verification

- [x] *(candidate 8's run map: [c8-CANDIDATE](sdd/V6-closeout/phase3/c8-CANDIDATE.md), `phase3/c8/chain.log`, `power.tsv`, `overnight-outcome.txt`, `phase3/c8-c52/`; incomplete results named in the [close-out report](V6-CLOSEOUT-report.md) sections 6 and 10)* R2 run map accounts for focused/parallel groups, each justified long gate, actual instrumented coverage, current candidate/artifact identity and every incomplete result; no duplicate per-row whole-tree runs or unreviewed coverage substitutions.
- [x] *(C6.2, `36dc82e4`, with 1.17.18 corrected to `partial_verified` in the C6.4 review round: 304 rows, 275 `verified_in_target`, 16 `partial_verified`, 7 `unknown`, 3 `unsupported`, 3 `documented`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Reconcile SP01–18 original row families and SP19/20/21 additions against actual supported release scope.
- [ ] *(not met in full: UAT-01, -03, -09, -10, UAT-05 run 1 and UAT-12's upgrade leg carry candidate 7's pass by diff, not a run on the shipped bytes (D53(f), D59, D76(f)); 1.17.10, 1.17.14 and the F-5 rows 1.18.x are `partial_verified`; [close-out report](V6-CLOSEOUT-report.md) section 4)* SP17-M7-01–08 and SP18-M7-01–07/UAT-01–12 have inspected artifacts on the shipped package.
- [x] *(the one enabled adapter, SessionStart compact reinjection, is `verified_in_target` on candidate 8 (C4.3, C4.7 live); gated switches recorded `experimental` or `unsupported`; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* M0–M6 enabled capabilities retain actual target/failure/compatibility evidence; missing host integration cannot be signed off as passed.
- [x] *(rehearsed: release-check's rollback rehearsal PASS on candidate 8 (`phase3/c8/release-check.json`); C1.7 restore smoke on candidate 8 and pre/post-new-write restores on candidates 4 and 7 ([CARRIED](sdd/V6-closeout/live/rerun-c8/CARRIED.md), D53(f)); the installed upgrade was not re-run on candidate 8)* Old/new readers, consistent backup, import-frontier cutover and rollback before/after new writes have been rehearsed.
- [x] *(no blocker or major open (D76's audit, D79), except the unclassified TestGC_DeadlineTruncatesAndResumes red of ci.yml 37746311073 (close-out report section 10), pending its ledger row; C5.5 "inconclusive — interval [-0.214, 0.214] straddles -0.200", no H2 regression (D77); known issues 1-19 disclosed in the release notes; [close-out report](V6-CLOSEOUT-report.md) sections 4 and 8)* No unresolved mandatory privacy, fidelity, recovery, compatibility or task-regression blocker is hidden by a waiver or cheap token score. Historical V3 waiver remains historical; it cannot certify a release environment.

## 3. New cross-component integration tests

Historical proposed test names are retained as identifiers, not assertions that these tests exist or pass. Correct their assertions/names compatibly during future implementation and record the mapping; unsafe guarantees below are retired.

| Retained section / test identifier | Current future criterion and owner |
|---|---|
| 3.1 `TestV6_PackagedBundleObservesARealSessionEndToEnd` | [x] *(20 real sessions on the frozen bundle (D76) and one on the released bytes (D80); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 packaged disposable-session flow records host version, lifecycle coverage and actual recovery. |
| 3.2 `TestV6_UniversalLauncherPreservesHookSemantics` | [x] *(`verified_in_target` on the Windows, ubuntu and macos test lanes; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 target OS/path-with-spaces/managed restriction and competing-hook payload matrix. |
| 3.3 `TestV6_DocumentedCommandsRunAgainstTheShippedBundle` | [x] *(C4.5 on the frozen bundle, known issue 17 (D76(b)); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-18 documented commands use the actual released artifact and preserve human/JSON/error status. |
| 3.4 `TestV6_EliminationStalenessSurvivesPackagingAndAnswersThroughMCP` | [ ] *(not met: survival across an upgrade was not re-run on candidate 8, carried from candidate 7 (D53(f)); `partial_verified`, [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-20/SP-13 scoped stale/uncertain/elimination query errors survive upgrade and packaged MCP. |
| 3.5 `TestV6_EphemeralRetrievalResultsAreEvictedFirst` | [x] *(native assertion retired (E-1); TestPropose_ChoosesAtMostOneRepresentationPerItem green on candidate 8; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Retire native ephemeral-eviction assertion; verify SP-15/SP-16 future representation policy only. |
| 3.6 `TestV6_FsckRepairsSeededCorruptionWithoutLosingLiveData` | [x] *(packaged fsck on real stores on candidate 8: C1.6, the C1.7 restore smoke, UAT-04; known issue 18 (D76(c)); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17/SP-20 fsck audit/explicit repair preserves live references, leases/bases and verified backups. |
| 3.7 `TestV6_DoctorAgreesWithStatusAndWithTheUnderlyingSubsystems` | [x] *(C4.5's paired status, doctor and fsck reads with two sessions in the store, candidate 8; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 doctor and SP-14 status agree on per-capability evidence, including unknown/degraded states. |
| 3.8 `TestV6_ConfigReferenceDescribesTheBinaryThatShips` | [x] *(generated-docs checks in the pre-freeze gate and release-check on candidate 8; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-18 source-derived setting/command inventory matches binary; no guessed fixed counts. |
| 3.9 `TestV6_InstallUpgradeUninstallLeavesTheProjectByteIdentical` | [ ] *(not met: the installed upgrade (C4.8, UAT-12's upgrade leg) carried from candidate 7 (D53(f)); rollback rehearsal and install/uninstall on the released bytes passed; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 install/upgrade/uninstall preserves project work and retention policy, with pre/post-write rollback. |
| 3.10 `TestV6_DegradedPassiveIsCorrectFromThePackagedBundle` | [ ] *(not met: C4.9 (b) and C1.6 ran on the packaged candidate 8 bundle, but C4.9 (c), the packaged unavailable object, was not re-run on candidate 8 and is carried from candidate 7 (D53(f)); `partial_verified`, [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 packaged unknown schema and unavailable object safely degrade; optimization off, privacy enforced. |
| 3.11 `TestV6_NoSecretAndNoNetworkAcrossAFullPackagedSession` | [x] *(C4.6 and UAT-12 sessions A and B on candidate 8 (D76); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17/SP-20 no secret leakage or unauthorized network/replay across logs/indexes/backups/retrieval previews. |
| 3.12 `TestV6_CheckpointToRehydrationRoundTripThroughTheBundle` | [x] *(UAT-05 run 2, C4.3, UAT-04 and UAT-06 compactions on the frozen bundle (D76); [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-10/SP-11/SP-13 repeated/missing/failed compaction and current-authority recovery under bounded context. |
| 3.13 `TestV6_ReleaseArtifactsAreReproducibleAndSelfConsistent` | [x] *(two builds byte-identical, hosted release-dry-run equal, published `bin/` equal (`phase3/c8/release-bin-compare.txt`, D80); identifiers and licenses pass in release-check; the name-check half is open, as section 4's open box records: no artifact records a registry name-availability check; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 exact release/package identifiers, licenses/name check and reproducibility evidence for supported targets. |
| 3.14 `TestV6_HotPathHoldsWithEverySubsystemResidentInTheBundle` | [ ] *(not met for Linux: fsync-bound B-A/B-B not verified in target in the container (D53(b), D75(a)); Windows passes on AC; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* SP-17 full-resident resource/latency distributions measured on quiet supported targets, no universal target claim. |

## 4. Whole-tree and release-candidate gates

- [x] *(candidate 8: overnight-c8.sh, `release-check --tag v0.3.0` 18 of 18, hosted ci.yml 37562946379 and nightly 37562945914; arguments and hashes in [c8-CANDIDATE](sdd/V6-closeout/phase3/c8-CANDIDATE.md); skips in the [close-out report](V6-CLOSEOUT-report.md) section 10)* Existing future devtool validation and supported-platform suites run against the exact package; record actual arguments, artifact hashes/versions and skip reasons.
- [x] *(`claude plugin validate --strict --json` accepted the frozen bundle (`phase3/c8/host-validate.txt`); installed launch, MCP and hooks in 20 live sessions (D76) and the released bytes' marketplace install (D80))* Validate manifest using the supported CLI path and test installed launch/MCP/hook behavior; a unit manifest check is not installed-host proof.
- [ ] *(not met in full: macOS and arm64 installed hosts and Linux model sessions are `unknown` (C4.11, D34(c)); the installed upgrade carried from candidate 7; 1.17.9's unknown-schema row's execution unproven; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Confirm supported OS/filesystems, paths with spaces, managed restrictions, unknown schema/payload/competing hooks and install/upgrade/uninstall.
- [ ] *(not met in full: identifiers and the license inventory pass on candidate 8 (release-check's version agreement and licenses steps), but no artifact records a registry name-availability check; 0.3.0 is distributed from its own repository's marketplace (D80))* Independently verify package/release identifiers, license/dependency inventory and current registry name availability; no stale unclaimed-name assertion.
- [x] *(C4.7 on candidate 8: recording off and reinjection off, each alone (`live/rerun-c8/C4.7/`); output replacement and experimental policies ship off and are refused at load, UAT-11 passed; [inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md))* Record independent recording, reinjection, output-replacement and experimental-policy kill switches with safe degraded/privacy outcomes.
- [x] *(agent-executed on the real host (D3): every UAT row has a pass, 8 on candidate 8 and 4 plus two legs carried from candidate 7 with notes (D76, docs/uat.md); held-out and changing-requirement tasks in C5.5 (D77); forbidden archived reads in C4.6 and UAT-12)* Complete SP18 UAT on real permitted tasks, including held-out/changing requirements and forbidden archived-read cases. A mocked CLI or ideal harness cannot substitute for the plugin.
- [x] *([close-out report](V6-CLOSEOUT-report.md) section 8; failures preserved in sections 2 and 10)* Maintain deterministic, closed-loop and recovery/failure layers; preserve all failures, ordinary model variation, telemetry gaps and exclusions.

## 5. Performance budget validation

Old B-A–B-F, L0/L5 and individual benchmark labels remain historical comparison targets where applicable. Reconcile each with its actual workload, binary/provider/model/OS, payload size, process startup, I/O, locking, sample distribution and date. No universal 15 ms, 4:1, sublinear growth, near-zero distortion, O(delta) native latency or first-turn savings is an acceptance fact.

Measure Qompack-added tokens and total observed context separately. Count the assembled representation using the supported estimator, label estimation and model changes, and calibrate against reported usage. Preserve old fraction-of-OPT outputs as diagnostics for their synthetic harness/objective; classic paging OPT is not a task-quality ceiling. Re-reading a changed file can be useful.

The request ledger separates uncached input, cache reads, cache writes/TTL, output, retries, failed/aborted trials, compaction, subagents and Qompack model calls if any. Record provider/model/rate-table date/pricing mode/completeness; missing telemetry is unknown, not zero. Estimated price is category usage times applicable rates plus relevant non-token charges; reported usage, estimates and invoiced cost remain separate. Subscription allowance is not automatically cash per token.

Declare regression margins and sample-size rationale before outcomes. Use repeated stochastic baselines, held-out tasks and changing requirements. Primary outcomes are task completion, constraints/regressions and recoverability; cost, latency, context, retrieval burden, repeated work, CPU/storage and tail delays accompany them. Report uncertainty, exclusions, failed trials and inconclusive outcomes; twenty runs cannot establish an unsupported two-percent guarantee. Cost and correctness gates remain independent. Record timings on quiet runners after future authoring completes.

## 6. Regression

Preserve Waves 0–2 and the V3 report/addendum unchanged. The 2026-08-26 user waiver closed V3 despite J5 billing blockage; J5 run 32932419445 and three-platform p99 backfill remain waived-open until actual evidence. Do not reinterpret early held-gate cells as a project restart.

Carry SP05-D1 to SP-20 drain/ack recovery, SP02-D1–D6 as one V4 corpus/rebaseline unit, and SP06-D2/SP08-D1 as a paired performance decision. Preserve SP04-D2/D3 and SP06-D1 wontfix rationale while testing new fidelity/retention contracts; SP04-D7's delta consumer is assigned to checkpoints. Reconcile SP11-C28 frontierOf/Ref.Frontier against the active sibling. Existing SP07 NodeID/generation/fixture rulings in V2-SP07-handoff.md remain regression context.

No baseline regeneration, fixture relabeling or machine-read carry update can erase a failure. Future updates to CARRIED-DEFECTS.tsv require actual evidence under its existing guard. Documentation v1.5 is the authorized planning baseline; the former rule demanding byte identity of Qompack.md to the initial commit is retired for this explicit revision.

Evaluate stock behavior, current implemented Qompack, corrected checkpoint/retrieval and admission separately under controlled snapshots. Observation masking is optional only where the actual harness supports it. Preserve reports before metric migration and never attribute differences between unlike native/custom harnesses solely to the plugin.

## 7. Subagent strategy

Future groups retain V6-A foundation, B evaluation/sketches, C chunk/canon/symbols, D daemon/contracts, E store/DAG, F observer/negknow, G checkpoint/rehydrate, H scheduler/MCP, I commands/selection/reuse, J packaging, K docs/UAT inventory. Assign SP19/20/21 test ownership to those groups by source boundary, with one main integrator for shared contracts/config/bootstrap/commits.

Main executes the actual interactive UAT with any required user participation; K may inspect documentation and fixtures but cannot claim that interaction occurred. Independent final integrity/trust/cost reviewer reads evidence and release/rollback scope. Every dispatched child, including this reviewer, is subject to the Opus 4.8-only routing override in §0; requested Fable 5.1 remains a coordinator role, whose actual session identity must be reported honestly. Future authoring may be parallel only with disjoint assigned files; measured timings run after authoring on quiet isolated runners. The original planning roles are retained; actual authorized dispatch and measured routing are recorded in V6-report.md.

## 8. Completion report template

Migration rows are additive and do not renumber prior inventory. The future report must retain every original row ID with its explicit current assertion/result or documented retirement/replacement mapping.

Future report retains the existing V-report convention and records: branch/HEAD and dirty baseline; date, supported OS/provider/model/host/plugin/schema versions; inventory row-by-row result and old-to-new assertion map; completed work preserved; commands actually run and artifact paths; skips/failures/waivers; migration/capability gate statuses; request-usage completeness and estimated-price provenance; statistical outcomes; privacy/retention and rollback drill; independent findings and resolution; remaining blockers and next authorized action.

Use `documented`, `verified_in_target`, `implemented_unverified`, `unsupported`, `experimental` and `unknown` appropriately. A new report must not copy old PASS cells as new runs. Existing completion reports are immutable historical records. Proposed future report paths follow V4-report.md, V5-report.md and V6-report.md naming; these reports are not created in the planning pass.

Keep original per-SP inventory, exits, integration, whole-tree/package gates, budgets, regression, UAT and independent sign-off sections. Include the release candidate identity, three evaluation layers, primary correctness/recoverability outcomes, interval/sample rationale, actual rollout scope and rollback/kill-switch drill.

## 9. Failure protocol

Record severity, exact command/artifact, snapshot, version, reproduction conditions and affected owner. A unit pass cannot override a failed or skipped host gate. Missing coverage and retrieval failures remain unknown/unavailable, never absence or successful restoration.

The future integration owner makes the smallest compatible fix, rechecks affected gates and dependencies, and preserves original failures. Disable recording, reinjection, replacement and experimental policies independently according to the fault/privacy policy. Optimization failure normally passes through; privacy denial follows denial policy.

Before data cutover use an engine-supported consistent backup and stable writer frontier. Rollback before and after new-format writes must be rehearsed: use compatible readers or the verified backup, with explicit treatment of later writes an older binary cannot read. Stop incompatible writers, retain object IDs/evidence/rollback roots and do not promise automatic downgrade. Failed mandatory gates block affected enablement/release; optional-disabled policies are identified explicitly.

Do not publish or broaden rollout while mandatory gates fail. Observation/report-only may be the accepted supported scope; optional-disabled status must match documentation and configuration. Unknown future schemas return unsupported/degraded without corrupting data or guessing an optimization.

## 10. Gate — the release

- [x] *([inventory-c8-map](sdd/V6-closeout/inventory-c8-map.md), switches; the release pages rewritten from candidate 8's evidence (D76(f), D78(e), D80(a), D80(b)))* Every enabled feature has tested mechanism and honest evidence-qualified documentation.
- [ ] *(not met in full: the gates pass only on windows/amd64 (the reference host, on AC), Linux amd64 (the container) and the hosted runners, with Linux fsync-bound rows not verified in target (D53(b)) and hosted fsync rows reported (Q1); macOS installed packaging (exec bit, Gatekeeper) is unobserved, windows/arm64 and linux/arm64 never ran a test, and the installed upgrade is carried, as section 4's open box records (C4.11, D34(c), D80(c)); [close-out report](V6-CLOSEOUT-report.md) section 6)* Representative supported environments pass declared recovery, quality, compatibility, privacy and packaging gates.
- [x] *([close-out report](V6-CLOSEOUT-report.md) section 12: gated capabilities off and refused, selection off by default)* Rollout proceeds observation/report-only, opt-in selective features, then broader enablement only on evidence.
- [ ] *(open: the independent final review of the close-out report (C6.4) returned needs-fixes in its first round, and its re-review has not run; rollback rehearsed, release-check on candidate 8)* Independent review and rehearsed rollback have no unresolved release blocker.
- [x] *(each outward step had its own authorization: D4, D33, D79, D80)* Release/tag/merge/publish actions require their separate authorization. This planning revision ends without implementation or publication.

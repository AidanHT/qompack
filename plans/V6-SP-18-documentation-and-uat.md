# SP-18: evidence-matched documentation and user acceptance guide

**Status:** future Wave 5/M7 plan, revised 2026-09-06; no user docs, generated files or runtime checks changed here.
**Branch:** `feat/sp18-documentation-and-uat` | **Wave:** 5 | **Prerequisites:** verified V5, SP17 installed artifact and release scope, SP14 command contract, SP19 capability/accounting register | **Runs in parallel with:** SP17 only on disjoint files; integrate after SP17 | **Design sections:** §§0, 7–12, Appendix C | **Gaps addressed:** G7.2 and all documented residuals.

---

## Mission

Document the actual supported Claude Code plugin and provide observable UAT/recovery criteria. Preserve the original seven-commit structure, UAT-01–12 identifiers, generated-config workflow and ownership boundaries. Planning owner is the coordinator, requested GPT-6 Astra; future owner is the documentation/UAT implementer with an independent final reviewer.

## Design context (verbatim from Qompack.md)

The existing template heading is retained; current v1.5 requirements are summarized without obsolete executable examples.

Root `docs/config-reference.md` and `tools/devtool/genconfigdocs.go` already exist. `internal/config/defaults.go`, `runtime.go`, metadata/validation and `internal/pluginmanifest/manifest.go` are the current sources for defaults/command names; no fixed leaf/doc/job count is assumed. The proposed `test/docs` and enhanced `tools/devtool/configdocs` surfaces must first be inventoried; root inspection did not find `test/docs`. New filenames below are future artifacts until they exist.

v1.5 deliberately replaces old executable Appendix C examples with prose. Future tests/generators must read the versioned actual config contract and retain old fixture meaning. Do not use verbatim old design quotations to reintroduce native O(delta), free cuts, first-turn savings, exact-native-context or Bloom-safe-false-positive claims.

## Out of scope

No user documentation/code/config/CI/generated asset edit, generator execution, UAT run, issue sending, installation or Git mutation during this planning pass. SP17 owns `docs/security.md`, `docs/install.md`, release workflows and release artifacts. SP14 owns generated `docs/commands.md`; SP18 links rather than hand-edits it. No model-provider/runtime port or external service.

## Interface contract

### Consumes

The supported installed artifact and capability evidence; exact command/tool schema and error/fidelity/coverage contracts; actual config version/default/range/provenance/deprecation metadata; product write-set/retention/privacy limits; migration backup and pre/post-write rollback procedure; current task/recovery/usage measurement definitions.

### Produces

User README, architecture digest and justified ADRs, user guide, troubleshooting, limitations/upstream proposals, config reference and UAT guide. Preserve existing identifiers/links; source-derived inventories determine counts. Source excerpts quote supported current contracts sparingly; unknown facts remain identified with their verification action.

## Implementation spec

### 1. Reader-facing coverage

| Future document | Required content and limitation |
|---|---|
| README.md | What Qompack does, supported environments/install link, maturity and opt-in features; no performance/name-availability guarantee |
| docs/architecture.md and ADR index | Actual Go sidecar/write-set/identity/publication/state/retrieval contracts; historical decisions distinguished from new amendments |
| docs/user-guide.md | Seven commands/eight tools, current vs historical reads, fidelity/coverage/error states, current-authority corrections, additional-context budget/overflow |
| docs/troubleshooting.md | Provenance-first diagnosis, unknown capability/telemetry, capture gaps, denied/unavailable evidence, schema compatibility, safe disable and recovery |
| docs/cannot-do.md | Native cuts/markers/history eviction unsupported, no model-compliance/native-byte proof, no missing-original reconstruction, no universal improvement |
| docs/upstream-issues.md | Evidence-linked proposals for host limitations; no assertion an issue was filed without an artifact |
| docs/config-reference.md | Actual supported defaults/types/ranges/origins, version/deprecation/safe-disabled behavior, independent recording/reinjection/replacement/experiment switches |
| docs/uat.md | UAT-01–12 with preconditions, steps, expected observable result, evidence fields and rollback/failure outcome |

No product flow exposes Codex planning models or internal migration mechanics unless useful for a user decision. Describe subscription usage separately from estimated API price and invoice reconciliation. Explain that 8–12K is a historical Qompack-added target, not total restored native context. `dropped` remains the command name but reports qualified coverage. An `ephemeral` tag does not mean native eviction.

### 2. Configuration documentation

Preserve `gen-config-docs --check`, its failure behavior and actual user-owned config. If enhanced metadata/generator is needed, keep old default readers/goldens compatible and verify a source-derived one-to-one key inventory. Unknown/future settings must not activate unsupported optimizations. No status-line replacement is prescribed without user consent. Do not hardcode a leaf count or a current rate schedule into acceptance.

### 3. UAT identifiers and future acceptance

| Retained UAT ID | Revised future scenario / observable pass condition |
|---|---|
| UAT-01 | Install/version/tool discovery: actual bundle/host/OS recorded; unsupported optimization disabled |
| UAT-02 | Startup/recording: permitted capture identity/fidelity visible; gaps not hidden |
| UAT-03 | Local checkpoint: durable frontier/objects/references recover or explicit incomplete outcome |
| UAT-04 | Manual/automatic/failed compact: no optimization veto; PreCompact input semantics; no native-input shrink claim |
| UAT-05 | Rehydration: current authority and complete records under Qompack-added budget or explicit overflow |
| UAT-06 | Repeated compact/resume/fork/correction: no obsolete intent promoted, no PostCompact dependency |
| UAT-07 | Historical retrieval: exact/path/symbol/handle discoverability; unavailable not absent/current substitution |
| UAT-08 | Elimination: explicit scoped claim, observed dependencies, exact confirmation |
| UAT-09 | Changed/unknown dependency or stale index: uncertain/stale state, no filter-only prohibition |
| UAT-10 | Observation/accounting: usage categories, missing telemetry, diagnostics and uncertainty preserved |
| UAT-11 | Opt-in admission/disable: unrecognized schema or capture failure passes through within privacy policy; every pointer resolves |
| UAT-12 | Privacy/backup/upgrade/uninstall: denial before preview/expansion, bounded decoding and verified pre/post-write rollback |

Run UAT only in future isolated permitted projects/disposable sessions; never destructive probes on an active user session. Each row records version/date/snapshot, command/steps, actual output, pass/fail/skip reason, evidence location and rollback. A skipped integration keeps the corresponding capability unverified.

### 4. Documentation compatibility and review

Retain meaningful `TestRelativeLinksResolve`, config staleness and UAT-shape intentions from the original plan, but write them only as future tests if absent. Replace fixed-count/verbatim-obsolete-limit tests with coverage against the current capability register. Preserve generated commands ownership and SP17 security/install links only when the artifacts exist. Main owner integrates shared `OwnedDocs`, generator adapter and CI changes after SP17; no competing edits to one file.

## Test plan (TDD)

Existing future commands: `go run ./tools/devtool gen-config-docs --check`, `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool test`. Future `go test ./test/docs/...` is contingent on creating that proposed package. No documentation generator or test was run here.

| Future gate | Observable acceptance / evidence |
|---|---|
| SP18-M7-01 | Every supported capability/residual/risk has a user-facing explanation and tested fallback; requirement-to-doc map |
| SP18-M7-02 | Config docs exactly cover current source keys/ranges/defaults/deprecations, check mode detects stale output; actual logs |
| SP18-M7-03 | Seven commands/eight tools and retained IDs match installed package, not just plan examples; schema/help comparison |
| SP18-M7-04 | Relative file/anchor links resolve, proposed paths labeled until created, no stale fixed doc/job counts |
| SP18-M7-05 | UAT-01–12 executed by a human on the supported artifact or explicit skip leaves capability unverified |
| SP18-M7-06 | Docs and UAT describe/report backup, independent switches and rollback before/after new writes consistently |
| SP18-M7-07 | Claims omit unsupported control/performance/price/completeness guarantees; independent reader review |

## Commit plan

Seven future conventional commits retain original areas and numbers; no attribution trailers. Each starts with a meaningful documentation contract test where needed, then its supported doc/generator change, validation evidence and rollout/rollback note. No old implementation is repeated solely to match these identifiers.

### Commit 1 — `docs(sp18): establish supported architecture and doc contracts`

- [ ] Inventory existing docs/ADRs, create the necessary proposed docs-test harness, update README/architecture for actual supported behavior and validate links.

### Commit 2 — `feat(devtool): document versioned configuration metadata`

- [ ] Preserve existing generator/check behavior, add metadata only where needed, validate key/range/default parity and stale-output detection.

### Commit 3 — `docs(sp18): explain scoped recovery and command behavior`

- [ ] Write user guide against installed schemas, authority/fidelity/coverage semantics and recovery diagnostics; retain test output.

### Commit 4 — `docs(sp18): document observable failures and recovery`

- [ ] Cover unknown telemetry, capture/retrieval/permission/schema failures, provenance and rollback without synthetic claims.

### Commit 5 — `docs(sp18): state capability limits and upstream proposals`

- [ ] Prepare limitations and issue text matched to evidence; review unsupported/native/service boundaries; do not send issues here.

### Commit 6 — `docs(sp18): define and execute evidence-based UAT`

- [ ] Retain UAT-01–12, record actual future human results and rollback artifacts, preserve skips/failures.

### Commit 7 — `ci(sp18): validate the supported documentation set`

- [ ] Integrate after SP17, preserve command/security ownership, run future docs checks and independent final review, attach V6 evidence.

## Subagent strategy

Future branch/worktree: `feat/sp18-documentation-and-uat`, proposed `../qompack-sp18`. Preserve original A1–A5 roles after main establishes vocabulary/harness:

| Future role | Proposed owned files |
|---|---|
| A1 config reference | `tools/devtool/configdocs/`, generated config reference; main alone edits existing genconfigdocs adapter |
| A2 user guide | `docs/user-guide.md` and its docs tests |
| A3 troubleshooting | `docs/troubleshooting.md` and its tests |
| A4 limits/upstream | `docs/cannot-do.md`, `docs/upstream-issues.md`, related tests/template; no messages sent without authorization |
| A5 UAT | `docs/uat.md`, UAT contract tests and human-result collection |
| Main owner | README/architecture/ADR decisions, shared OwnedDocs/harness, CI integration after SP17, commits1–7 |
| Independent reviewer | Read-only supported-claim/links/UAT/rollback coherence review |

Each document has one writer. Future implementation concurrency follows agreed file contracts/capacity; these roles are not new P-stage planning agents.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). Preserve A1–A5 file ownership and main's vocabulary/harness prerequisite. Do not spawn one child for every short document.

| Existing role | Requested model and effort | Reason |
|---|---|---|
| A1 config reference | Opus 4.8 / high | Generator/schema/deprecation accuracy needs source reasoning; medium only for formatting an already-verified field inventory |
| A2 guide; A3 troubleshooting; A4 limits/upstream | Opus 4.8 / medium | Bounded prose from inspected capabilities and results; raise to high for ambiguous failure or supported-scope claims |
| A5 UAT | Opus 4.8 / high | Define observable acceptance, skipped/failed outcomes and recovery against SP-17's actual artifact |
| Independent supported-claim reviewer | Opus 4.8 / high | Check docs, links and actual evidence; Fable 5.1 / high only for unresolved capability, privacy or rollback contradictions |

Start with two ready disjoint documents; use a third slot only for another independent file or review. A5's actual UAT evidence and final integration wait for SP-17; drafting its guide does not prove results. Main alone integrates shared harness/config adapter/CI work. The reviewer must not be the document's author, and model agreement cannot replace human or installed-target UAT evidence. Preserve the no-unsolicited-upstream-messages rule.

## Exit criteria

- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] SP18-M7-01–07 match actual installed release scope and evidence.
- [ ] Future UAT reports include failures/skips, versions, dates and snapshots; no migration gate inferred from writing docs.
- [ ] Config versioning/deprecation and independent switches are correctly described.
- [ ] Unsupported native controls and unknown historic/current recovery cases remain explicit.
- [ ] SP17 artifacts are integrated before documentation signoff; no fabricated existing files.

## Done checklist

- [ ] Seven future conventional commits with no attribution trailers and stable UAT IDs.
- [ ] No fixed inventory/leaf/job counts replace current inspected sources.
- [ ] Future docs/tests/generators/CI are validated in the implementation session, not this plan pass.
- [ ] Independent final reviewer approves user-facing evidence and rollback instructions.

### Rollout, rollback and blockers

Ship documentation with its matching artifact version; if a capability regresses, revise advertised support and disable it through SP17 controls. Do not downgrade data to match old prose. Missing host/UAT artifacts (SP18-M7-03/05) prevent delivered-capability claims; missing source metadata (SP18-M7-02) blocks config-reference readiness. Future public issue/release actions require their separate authorization, not this planning task.

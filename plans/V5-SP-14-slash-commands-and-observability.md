# SP-14: L6 slash commands and evidence-qualified observability

**Status:** future Wave 4 implementation plan, revised for M0–M7; no implementation executed here. Completed Waves 0–2 and active Wave 3 work are preserved.
**Branch:** `feat/sp14-slash-commands-and-observability` | **Wave:** 4 | **Prerequisites:** verified V4, SP19 accounting/capability contract, SP20 state, SP10–13 recovery; SP15→SP16 integrate before this frontend | **Runs in parallel with:** SP15/SP16 only on agreed disjoint files | **Design sections:** §§7.5, 8.7, 11, 12 | **Gaps addressed:** G8.1/G8.3 presentation, G4.5 qualified coverage.

---

## Mission

Provide the seven existing slash-command names as frontends over the single owning implementation of checkpointing, pins, state, retrieval and evaluation. `status` reports what was observed, what is estimated and what remains unknown; it cannot certify native completeness. Planning owner is the coordinator, requested GPT-6 Astra; future implementation owner is SP14 commands owner with independent measurement review. See [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md).

## Design context (verbatim from Qompack.md)

The existing template heading is retained; current v1.5 requirements are summarized without obsolete executable examples.

Existing `internal/commands/commands.go` contains the command contract; `internal/pluginmanifest/manifest.go` and its tests/goldens define seven command specs. `internal/daemon/handlers.go` owns the existing StatusSnapshot and `ipc.OpStatus` routing. The extended status.full plan remains separate so the hot path is not inflated. Existing task dispatch is `tools/devtool/main.go`; `plugin-validate`, `test`, `test-race`, `gen-config-docs` and `replay` are real task names. A standalone commands documentation generator is proposed until actually implemented.

SP14 owns presentation, not new pricing, scheduler, retrieval or elimination logic. The original fraction-of-OPT display remains a labeled historical diagnostic; current primary outcomes are task success, constraints and evidence recovery. Missing native usage/cache/headroom is unknown. A configured estimator or rate is never billed usage.

## Out of scope

No source, generated command Markdown, config docs, manifests, CI or fixtures change in this planning pass. Future source changes do not duplicate MCP handlers, scheduler/ledger policies or historical retrieval. No native compaction trigger/cut/veto, status-line replacement, output replacement pipeline or OpenAI calls. SP17 owns install/security/release; SP18 owns user prose. Preserve SP14 ownership of future generated `docs/commands.md`; SP18 links to it.

## Interface contract

### Consumes

| Source | Contract |
|---|---|
| SP13 registered MCP handlers | Scope/authorization before snippets, explicit current/history and unavailable/absent/fidelity/coverage envelope |
| SP10 writer/reader and pins | Compatible checkpoints, local checkpoint-now action, authority-aware pin records |
| SP20 ledger/current state | Claims/provenance/dependency coverage/conflicts; no filter prohibition |
| SP12 scheduler observations | Qompack cadence and estimated headroom, observation age and unknowns |
| SP19 request ledger/eval | Provider/model/rate date/mode/categories/completeness; price estimate vs reported usage/invoice |
| Existing daemon status/obs registry | Original status snapshot preserved; extended status.full with per-section missing/error status |

### Produces

Human text and stable JSON envelopes preserve command outcome/error semantics and offer the same evidence identities. Keep deterministic ordering, injected clock, no ANSI, documented help, exit 0 for success, usage error 2 and other command error 1. Hook exit behavior is a separate existing contract. Keep existing fields through a versioned adapter; add uncertainty instead of zero-filling. Unknown schema reports unsupported, not fabricated success.

## Implementation spec

### Command and status behavior

| Existing command | Future behavior / acceptance |
|---|---|
| status | Mode/capability evidence, capture gaps, frontier, qualified coverage, retention/storage, per-hook latency source/age, request usage/estimate/completeness and experimental policy status |
| recall | Reuse SP13 handler; authorized snippets, pagination and explicit errors |
| pin | Preserve source authority, current scope, add/remove history; elimination pin records a claim with dependencies |
| checkpoint | Request Qompack-local checkpoint-now, never native compact; preserve lifecycle/overflow diagnostics |
| why | Reuse decision/provenance handler; no upgrade of agent claim to user instruction |
| dropped | Keep command name; display coverage-and-recovery categories, timestamps/fidelity and unknown native status |
| eval | Separate task/constraint/recovery gates from cost; label historical Belady/file/action metrics and skipped/failed trials |

Preserve the `arch/checkpoint-now-subcommand` pre-step as a future architecture dependency before any changed CLI routing. Existing `commands → daemon` avoidance is a decoupling choice, not an importgraph ban. Main coordinator owns shared CLI/daemon wiring. No status request initiates GC or mutates user configuration. The per-hook latency contract applies to both daemon and disk fallback; SP14 owns extending status.full with actually available per-hook observations, otherwise identifies unavailable rows (SP-14-C14/C20).

Version command tables, help and JSON schema together. Existing `plugin/commands/*.md` and golden copies are regenerated only in future implementation after contract review; this task leaves them untouched. Do not describe future generator/API files as installed commands.

## Test plan (TDD)

Future existing commands: `go test ./internal/commands ./internal/pluginmanifest`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, and `go run ./tools/devtool plugin-validate`. Command-specific cases below and a generated-command staleness entrypoint are proposed until created. No runtime command was executed here.

| Retained/proposed ID | Observable future criterion and artifact |
|---|---|
| TestCommandNames_MatchesSection75 / TestExitCode | Existing seven names/order and documented error exits retained; command contract output |
| TestStatus_DaemonPayloadMirrorIsCurrent | Compatible versioned status.full mirrors actual daemon fields and preserves original OpStatus; schema comparison |
| TestStatus_HotPathNames / SP14-M7-01 | Per-hook daemon/disk rows carry measured source/age or unavailable; no aggregate presented as individual timing |
| TestStatus_NilDepsUnavailable / SP14-M7-02 | Missing usage/errors shown unknown, not zero/absent; human and JSON parity fixtures |
| TestRecall_NoSecondImplementation / SP14-M2-01 | Frontends reuse SP13 and preserve denial/history/error/fidelity distinctions before preview |
| SP14-M3-01 | Correction/overflow/failed checkpoint remain explicit; checkpoint command does not request native compaction |
| SP14-M7-03 | Cost categories, retries/aborts and estimate/invoice distinctions survive display; cheap wrong result cannot pass |
| SP14-M7-04 | Help and generated command documentation match installed schemas; unsafe unsupported features not advertised |

## Commit plan

Eight future commits retain original numbering and areas. Each includes meaningful failing contract cases, compatible implementation, relevant validation artifacts and rollback notes; no existing work is replayed merely to match a number.

### Commit 1 — `feat(commands): define compatible command envelopes`

- [ ] Specify/help/schema/exit contracts, preserve existing command specs, then implement compatible dispatch and retain test output.

### Commit 2 — `feat(commands): collect qualified status observations`

- [ ] Specify status.full compatibility and per-hook missing data, implement collector, verify daemon/disk parity and original OpStatus isolation.

### Commit 3 — `feat(commands): render evidence and uncertainty`

- [ ] Add full/degraded/unknown text and JSON fixtures, implement deterministic rendering, retain estimator/source/age evidence.

### Commit 4 — `feat(commands): expose qualified retrieval frontends`

- [ ] Reuse MCP handlers, test denied/error/history/pagination cases and preserve previous caller compatibility.

### Commit 5 — `feat(commands): expose pins and local checkpoints`

- [ ] Implement authority-aware pin/elimination and local checkpoint-now surfaces after architecture pre-step; verify correction/overflow/no-native-action.

### Commit 6 — `feat(commands): report task and usage evidence`

- [ ] Replace primary fraction-of-OPT presentation with separate task/recovery/cost outcomes, retain old labeled diagnostic fields, test missing usage and failed trials.

### Commit 7 — `feat(cli): integrate command and status routes`

- [ ] Integrate shared CLI/status.full changes sequentially; validate installed discoverability without changing hook behavior.

### Commit 8 — `build(plugin): align command help and documentation`

- [ ] Generate command/docs artifacts in the future session, verify staleness/compatibility, record target evidence, rollout and rollback.

## Subagent strategy

Retain the original mostly sequential implementation approach. Main SP14 owner edits `internal/commands`, shared manifest/CLI/status integration and generator. One optional future fixture reviewer owns only proposed `*_test.go` and `testdata/golden/commands/**` after exact file assignment; no competing source writer. Independent cost/measurement reviewer checks missing usage and source attribution. Proposed future worktree `../qompack-sp14`, branch above, no creation here. Shared status/CLI changes integrate after SP15/SP16 and the architecture pre-step.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). This is future implementation delegation only; retain the planning-owner record and all existing file ownership. Keep the main commands implementation sequential. Use at most one optional worker plus one independent reviewer; they count toward the global three-child cap.

| Existing role / bounded task | Requested model and effort | Reason and handoff |
|---|---|---|
| Fixture/test helper | Opus 4.8 / high | Specify command/envelope, exit-status and missing-usage cases in the already-assigned test files; no competing command implementation |
| Same helper, artifact inventory only | Opus 4.8 / medium | Collate known golden/command coverage after semantics are agreed; return ambiguity to main |
| Independent measurement reviewer | Opus 4.8 / high | Trace displayed values to reported/estimated/unknown sources; Fable 5.1 / high only for an unresolved authority, accounting or cross-component contradiction |

Main can retain the helper's work when it is too small to justify a child. Run the reviewer after the relevant command/status slice exists. SP-15/SP-16 handoffs still precede shared frontend integration; parallel fixtures cannot bypass that sequence. Do not spawn a separate expensive agent merely to execute a known command.

## Exit criteria

- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] SP14-M2/M3/M7 gates preserve errors, scope, authority, recovery and telemetry uncertainty.
- [ ] All seven installed command frontends match help/schema and use owning APIs.
- [ ] Per-hook latency works from daemon and fallback or is explicitly unavailable.
- [ ] No user status-line/config changes, native-control claims or zero-filled telemetry.
- [ ] Future compatible command rollback is tested and V5/V6 receive actual artifacts.

## Done checklist

- [ ] Eight small conventional future commits, no attribution trailers, retained original IDs and history.
- [ ] Existing conformance tests and newly required command cases execute in the implementation session; skipped integration stays unverified.
- [ ] Generated docs and source are changed only in that later session with actual validation evidence.
- [ ] Independent measurement reviewer approves the presentation and fallback.

### Rollout, rollback and blockers

Observation/report-only first; disable optional displays without affecting recording/retrieval. Preserve prior JSON readers and historical metric labels. If a new schema is unreadable, fall back to a compatible renderer or explicit unavailable result, never an old absent-on-error interpretation. No data migration is owned here. SP19 rate/usage availability and SP13/SP20 response/authority contracts block final display semantics until future verification. Missing status.full per-hook data is owned by SP14-M7-01, not deferred to an unspecified plan.

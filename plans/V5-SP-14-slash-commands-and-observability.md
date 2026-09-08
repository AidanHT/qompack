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

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Command names, text/JSON/exit parity, missing usage and help/schema fixtures can be checked as independent small groups in commands/pluginmanifest; add the affected CLI, daemon or MCP consumer checks when wiring changes. Each frontend owner runs the checks for the files it owns; the artifact inventory seat remains optional.

**When broader checks are necessary.** Run real command-to-retrieval/checkpoint/status seams after those consumers are integrated. Installed discoverability and generated-help parity belong to the matching final artifact; full replay/race/coverage are not triggered by a rendering or help-only edit.

Each implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 Opus/Fable roles and global worker limit; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

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

Wave 4 runs at maximum parallelism. The seven command frontends and the status surface are authored concurrently; there is no single sequential frontend owner. Every role below owns whole named files, not packages. **Two roles never own the same file.** A role that needs a file it does not own returns a handoff to main and does not edit it. Absent names are proposed files, not assertions of missing sibling work. Proposed future worktree `../qompack-sp14`, branch above; no creation here.

| Future role | Exclusive ownership | Commits |
|---|---|---|
| Main — envelope, dispatch, integration | `internal/commands/commands.go` and its contract tests; shared CLI/status routes; final acceptance | 1, 7 |
| Status observation owner | `internal/commands/statuscollect.go` and test; the SP14-owned status.full extension | 2 |
| Shared rendering owner | `internal/commands/render.go`, `render_test.go`, `testdata/golden/commands/status/**` | 3 |
| Retrieval frontends — `recall`, `why`, `dropped` | `cmd_recall.go`, `cmd_why.go`, `cmd_dropped.go` with their tests and goldens | 4 |
| Action frontends — `pin`, `checkpoint` | `cmd_pin.go`, `cmd_checkpoint.go` with their tests and goldens | 5 |
| Evidence/cost frontend — `eval` | `cmd_eval.go` with its test and eval fixtures | 6 |
| Manifest and generator owner | `internal/pluginmanifest/manifest.go`, its tests/goldens, the proposed commands-doc generator and staleness entrypoint | 1, 8 |
| Artifact inventory seat | No source files; returns golden/command coverage inventory to main | — |
| Independent measurement reviewer | Read-only; owns no file | — |

**Batching.** `recall`, `why` and `dropped` share the SP13 authorization, history and unavailable/fidelity/coverage envelope, so one owner keeps those distinctions consistent. `pin` and `checkpoint` share authority-aware records and the local no-native-action rule. `eval` is independent because it separates task/constraint/recovery gates from cost. `status` is its own surface, split into observation and rendering because collection and uncertainty presentation are separately testable. The command contract, help/schema/exit semantics and the render signatures are published in the dispatch brief before authoring, so frontends author against a fixed contract instead of waiting on it.

**Handoff edges.** These are the only serial edges; everything else runs concurrently.

| Edge | Waits on | Blocks only |
|---|---|---|
| H1 selection wiring | SP-15 handoff | The slice wiring `/qompack:status` to SP-15 selection |
| H2 reuse wiring | SP-16 handoff | The slice wiring `/qompack:status` to SP-16 reuse |
| H3 checkpoint route | `arch/checkpoint-now-subcommand` pre-step | Changed checkpoint CLI routing; not its frontend or tests |
| H4 integration and verification | All authoring seats returned | Commit 7 shared routes and commit 8 artifacts; main only |

Command frontends and their tests are authored concurrently now; only the H1/H2 status slices wait on those handoffs, and parallel fixtures still cannot bypass that sequence.

`internal/daemon/handlers.go` is shared with SP-15's daemon integration. It must be assigned to exactly one plan's owner at dispatch time and the assignment recorded; the two plans must not both edit it. Generated config documentation follows SP-14/SP-18 ownership in the later integration session.

Authoring is not gated on V4, but SP-14 cannot be **enabled** until the V4 gate closes. Author now; enable after.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). This is future implementation delegation only; retain the planning-owner record and all existing file ownership.

| Existing role / bounded task | Requested model and effort | Reason and handoff |
|---|---|---|
| Main — envelope, dispatch, integration | Opus 5 / high | Multi-file judgment across command contract, shared routes and acceptance |
| Status observation owner | Opus 5 / high | Multi-file judgment: daemon/disk parity, unavailable rows, original OpStatus isolation |
| Shared rendering owner | Opus 5 / high | Multi-file judgment: deterministic text/JSON parity and uncertainty presentation across all seven names |
| Manifest and generator owner | Opus 5 / high | Multi-file judgment across manifest specs, goldens and generated help |
| Retrieval frontends (`recall`, `why`, `dropped`) | Opus 4.8 / high | Bounded slice reusing SP13 handlers; hands off any render change |
| Action frontends (`pin`, `checkpoint`) | Opus 4.8 / high | Bounded slice; only its changed routing waits on H3 |
| Evidence/cost frontend (`eval`) | Opus 4.8 / high | Bounded single-command slice; no new pricing logic |
| Fixture/test helper, artifact inventory only | Opus 5 / low | Mechanical collation of known golden/command coverage after semantics are agreed; returns ambiguity to main |
| Independent measurement reviewer | Fable 5.1 / high | Traces displayed values to reported/estimated/unknown sources; runs after the relevant command/status slice exists |

**Dispatch contract.** Each unit receives exclusive file ownership, a written brief, and returns report-to-file with a short structured summary: files touched, commands run with their actually selected cases, and blockers. Each unit carries a per-unit tool-call budget and stops with a BLOCKED report after three identical failures instead of retrying. Do not use `git stash`; the stash list is shared across every worktree of one repository. Confirm any `go test -run` filter actually selects cases — it prints `ok` when it matches nothing, which produced three real misdiagnoses in the preceding session.

Main can retain the inventory or collation work when it is too small to justify a child; the named frontend and status seats stay separate so they are authored concurrently. Do not spawn a separate expensive agent merely to execute a known command.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
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

# V4-VERIFY reconciliation map

Companion to [`inventory.md`](inventory.md). **Read-only reconciliation: no suite was run, and nothing in
this file is a pass.** The inventory's 212 rows are preserved verbatim there; this file adds a parallel
adjudication keyed by row ID, so that history is never rewritten.

- **Base:** `feat/v4-corrective` @ `2232af6` — the integrated corrective candidate carrying nine merged
  units (A0, B, C, D, E, F, G, H, J, K, L, M) on top of the inventory's original baseline
  `wip/v4-preparation` @ `919ca3a`.
- **Unit A2** (daemon admission, delivery-journal wiring, publication ordering, crash cuts) is **not in
  this base**. Rows that cannot be adjudicated without it are `PENDING-A2`, never `MISSING`.
- **The V3 waiver stands.** J5 billing (run `32932419445`) and the three-platform p99 backfill remain
  **waived-open** — not failures, not passes. Nothing here changes them.

## Disposition vocabulary

| Disposition | Meaning |
|---|---|
| `MAPPED` | A current test definition asserts the historical requirement. The cell names `package`: `TestName`. **This is not a pass** — it establishes what would be run. |
| `MAPPED-CMD` | The row is a command/gate row, not a test row. The cell names the exact command that satisfies it on this tree. |
| `SUPERSEDED-BY-CORRECTIVE` | One of the nine landed units changed the requirement itself. The cell names the new assertion; the note names the commit. |
| `RETIRED` | The historical assertion no longer matches current semantics. Every one carries a reason and a replacement pointer. |
| `MISSING` | No current definition matches. The note says what must be written and which package owns it. |
| `PENDING-A2` | Blocked on the in-flight A2 unit. |
| `SPLIT-REVIEW` | The row's assertion spans more than one disposition and needs an independently reviewed coverage split before it can be scored. |
| `NEEDS-COORDINATOR` | Cannot be adjudicated here. The exact question is in [§5](#5-needs-coordinator-questions). |

**Confidence** is `direct` where the mapping was established from the test file, the corrective unit's
report, or an executed enumeration; `by-name` where a definition of that name exists in the expected
package but its body was not read. `by-name` is an implementation pointer, nothing more.

## Method

Test-definition inventory taken with `grep -rn --include='*_test.go' '^func (Test|Benchmark|Fuzz)'`
over the worktree: **3 526 definitions across 66 packages**. No suite, benchmark, or gate was executed.
Corrective-unit attributions come from `git log 919ca3a..2232af6` and the unit reports.

---

## 1. Disposition counts

| Disposition | Rows |
|---|---:|
| `MAPPED` | 83 |
| `MAPPED-CMD` | 46 |
| `SUPERSEDED-BY-CORRECTIVE` | 38 |
| `RETIRED` | 14 |
| `SPLIT-REVIEW` | 7 |
| `MISSING` | 3 |
| `NEEDS-COORDINATOR` | **0** — was 3 rows plus 10 questions; all ten were ruled on 2026-09-08 and the rows re-scored (`V4-SP01-08` → `MAPPED`, `V4-SP10-19` → `MAPPED-CMD`, `V4-SP12-05` → `MAPPED`). See [§7](#7-coordinator-rulings-on-nc-1--nc-10-and-what-was-applied-2026-09-08) |
| `PENDING-A2` | 1 |
| **Retained rows total** | **195** |
| §4 cross-component scenarios (all `MISSING`, all with authoring briefs) | 14 |
| `NOT-YET-ADJUDICATED` | **0** |

The inventory scored eleven rows `MISSING` that are not missing — `V4-SP02-04`, `V4-SP04-05`,
`V4-SP04-06`, `V4-SP05-14`, `V4-SP06-09`, `V4-SP10-17`, `V4-SP10-22`, `V4-SP11-23`, `V4-SP12-22`,
`V4-SP13-23`, `V4-ALL-05`. Each was a *command* gate with no matching test **name**, and the old scan
looked only for names. Their commands and, where applicable, their conformance suites exist on this
tree. These are scoring corrections, not new coverage, and they are marked as such in the row notes.

---

## 2. The fourteen §4 cross-component scenarios

All fourteen remain `MISSING`. None has a current definition; `grep` over the 3 526 definitions finds
no `TestV4_*` anywhere in the tree.

**Home and convention.** Wave 3's precedent is `test/e2e/v3_x01_test.go` … `v3_x12_test.go`, one file
per scenario, each holding exactly one `TestV3_<Name>` that drives the **real binary** through the
`test/e2e` harness. The V4 scenarios take the same shape: `test/e2e/v4_x01_test.go` …
`v4_x14_test.go`. Three exceptions are called out below where the scenario is a measurement or a
structural guard rather than a session flow.

**What makes one non-vacuous.** The plan requires real producers. A scenario is vacuous — and must be
rejected in review — if it (a) constructs the consumer with a fake, stub, or `nil` producer where a
real one exists on this tree; (b) asserts only that a call returned without error; (c) asserts a
constant the producer never had a chance to change; or (d) would still pass with the subsystem under
test disabled. Every brief below therefore names a **negative control**: a change that must make the
test fail.

### 4.1 `TestV4_PreCompactToCheckpointToRehydrateRoundTrip`
- **Package:** `test/e2e` (`v4_x01_test.go`).
- **Wire:** real binary `PreCompact` hook → `internal/cli` hook path → `internal/daemon` → real
  `internal/checkpoint` writer (`Begin`/`Advance`/`Finalize`) → on-disk `checkpoints/` + MANIFEST →
  real `SessionStart(source=compact)` → `internal/daemon` rehydrate service → real
  `internal/rehydrate.Build` → `additionalContext` on stdout.
- **Assert:** the checkpoint written by the PreCompact hook is the one `Latest` resolves at
  SessionStart; the rehydrated payload's item 2 is the byte-identical verbatim intent from L0 (not a
  summary); the payload is wrapped by the injection tags and carries the contract sentinel; the
  MANIFEST line for that checkpoint verifies.
- **Retired clauses that must NOT appear:** no assertion that the host accepted custom instructions,
  and no assertion of native history shortening (migration disposition rows `V4-SP05-04`,
  `V4-SP10-15`, `V4-SP11-18`).
- **Negative control:** delete the newest checkpoint file between the two hooks — the test must fail
  on identity, not silently fall back to the parent.

### 4.2 `TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold`
- **Package:** `test/e2e` (`v4_x02_test.go`), driving `internal/daemon`'s real
  `scheduler.Runtime` through the `Services` tap.
- **Wire:** real `internal/observer` L0 events → real `Services` tap → real
  `internal/scheduler` `Evaluate` with a real `Runtime` window ladder and context-token accounting →
  the checkpoint cadence consumer in `internal/daemon/wire_checkpoint.go`.
- **Assert:** with a simulated context-growth trace, the scheduler's decision to write fires at a
  context-token position strictly below the stock auto-compact threshold, and the cadence consumer
  seals a draft at that point.
- **Assert also (post-G):** under shipped defaults the Young-Daly clause is off
  (`TestYoungDalyClauseIsOffUnderShippedDefaults`), so the firing must be attributable to the
  composite trigger's supported terms only.
- **Negative control:** raise the threshold above the trace's maximum — the scheduler must not fire.

### 4.3 `TestV4_O1SpanInstructionFromARealCheckpointFrontier`
- **Package:** `test/e2e` (`v4_x03_test.go`).
- **Wire:** real `internal/checkpoint` `FrontierAdvancer` (unit G, `02f807a`) advancing over real
  encoded segments from `internal/store` `SegmentLog.MarkEncoded` → the focus/span instruction
  rendered into the checkpoint.
- **Assert:** the span instruction names the **local** frontier position that the advancer actually
  committed, and that position is backed by durable evidence
  (`TestCommittedFrontierIsBackedByDurableEvidence` is the unit-level analogue).
- **Retired clause:** this is local draft progress, **not** a native O(1) context boundary
  (`V4-SP10-13`). The test must not assert any host-side compaction effect.
- **Negative control:** make the evidence read fail — the advancer must report the fallback it took
  rather than emitting a span instruction.

### 4.4 `TestV4_FrontierAdvancementKeepsResidualSpanODelta`
- **Package:** `test/replay` (`v4_x04_test.go`), not `test/e2e` — it is a measurement over the
  synthetic corpus, and the A/B is a config toggle plus two full replays.
- **Wire:** real replay driver, real store, real checkpoint frontier, toggling
  `checkpoint.frontier.advanceOnSegmentClose` in the repo's `.qompack/config.json`.
- **Assert:** with advancement **on**, the residual span measured over the corpus is bounded and
  smaller than with it **off**, and `Score.Divergence` does not regress; both P50s are recorded.
- **Retired clause:** the historical "≥ 30 % reduction, guaranteed O(delta)" target is retired
  (`V4-SP10-21`, `V4-SP12-24`). Assert *bounded local work with recorded numbers*, not a guarantee.
- **Non-vacuity:** the two arms must differ in the toggle **only**; identical corpus, identical seed,
  both numbers reported even when the gate passes.

### 4.5 `TestV4_TombstoneToRecallToExpandRoundTrip`
- **Package:** `test/e2e` (`v4_x05_test.go`).
- **Wire:** real hook event → real `internal/observer` tombstone (`Tombstone_RendersTheSection81Form`
  shape) → real `internal/store` object → real MCP server over **stdio** (`TestStdioServerEndToEnd`
  harness) → `recall` → `expand`.
- **Assert:** the tombstone the observer wrote is addressable; `recall` returns it; `expand` returns
  the original bytes from the store; and — post-F — `expand` redacts a secret the capture-time policy
  missed and never logs it (`TestExpandRedactsSecretsTheCaptureTimePolicyMissed`), while `recall`
  omits hits whose stored path fails authorization.
- **Negative control:** point the stored path outside the authorized root — `recall` must omit it and
  the test must observe the omission, not an error.

### 4.6 `TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction`
- **Package:** `test/e2e` (`v4_x06_test.go`).
- **Wire:** real `internal/negknow` ledger + bloom → real MCP `already_tried` → the standing
  instruction rendered by real `internal/rehydrate` item 3.
- **Assert:** the answer state the ledger produces is the state the MCP surface renders **and** the
  state the standing instruction reflects, across the enum's members.
- **Superseded:** the enum is no longer three-way. Units D (`5935015`, `aa073fa`, `0b530fb`) and F
  (`f9cfcc5`) make it five-state — `absent` / `active` / `stale` / `unavailable` / `uncertain`. The
  scenario **must cover `unavailable` and `uncertain`**, and must assert that a ledger or query
  failure never renders as `absent`. Consider renaming the scenario accordingly; see NC-8.
- **Negative control:** fail the ledger `Get` — the surface must say `unavailable`, and the standing
  instruction must not claim absence.

### 4.7 `TestV4_EphemeralRetrievalResultsRankFirstForEviction`
- **Package:** `test/e2e` (`v4_x07_test.go`).
- **Wire:** real MCP retrieval writing `ToolUseRecord{Ephemeral: true}` through real
  `internal/store` `PutOptions.Ephemeral` → real `internal/scheduler` `DropClassOf`.
- **Assert:** ephemeral-at-birth metadata survives a store reopen and the scheduler's **local**
  drop-class ordering ranks those records first.
- **Retired clause:** no native-history eviction or deletion may be inferred (`V4-SP12-11`,
  `V4-SP13-14`, `V4-SP13-15`). The scenario asserts a local ordering and a representation hint only;
  a title mentioning "eviction" must be read as local drop-class ordering. See NC-8.
- **Negative control:** clear the ephemeral flag on one record — its rank must move.

### 4.8 `TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint`
- **Package:** `test/e2e` (`v4_x08_test.go`).
- **Wire:** real rehydration injecting a tagged payload → real observer recording the next turns →
  real checkpoint writer producing the **next** checkpoint.
- **Assert:** no byte of the injected payload appears in the next checkpoint's fields, and the
  injection tags plus the contract sentinel are the mechanism that excludes it
  (`TestBuild_InjectionTagging`, `TestUnwrap_RoundTrips`, `internal/contract` sentinel tests are the
  unit-level analogues).
- **Non-vacuity:** the injected payload must contain a unique nonce string; the assertion is on that
  nonce's absence, not on a field being empty.
- **Negative control:** strip the tags before the observer sees the turn — the nonce must then appear,
  and the test must fail.

### 4.9 `TestV4_GrowthGuardrailWithCheckpointsAndEphemerals`
- **Package:** `test/integration` (`v4_x09_test.go`), joining
  `TestIntegration_RealStoreGrowthIsSublinear` and `TestIntegration_ReplayGateAcceptsRealGrowthFile`.
- **Wire:** real store under real ingest, with real checkpoints being written **and** real ephemeral
  MCP retrieval records accumulating, feeding the real growth file the replay gate consumes.
- **Assert:** store growth stays sublinear with checkpoints and ephemerals both resident, and the
  growth file the gate reads is the one this run produced.
- **Interaction to cover:** GC roots are now wider (units B/C/M — delta bases, delivery-lease journal,
  migration and rollback material). The guardrail must hold **with** those retention roots held live.
- **Negative control:** pin every ephemeral as a hard retention root — growth must become superlinear
  and the guardrail must fail.

### 4.10 `TestV4_WhyAndDroppedAnswerFromRealProducers`
- **Package:** `test/e2e` (`v4_x10_test.go`).
- **Wire:** real `internal/checkpoint` decisions (the only producer of `core.DecisionID`) → MCP `why`;
  real `internal/rehydrate` drop report persisted by the real rehydrator → MCP `dropped`.
- **Assert:** `why` finds a decision that a real `ExtractDecisions` run created, following the parent
  chain; `dropped` returns the drop report the real `Build` actually wrote.
- **Superseded:** unit F `a9a9e1d` added the `ToolDeps` checkpoint gate. With no checkpoint reader and
  no drop reporter, both tools must report **unavailable**, not error
  (`TestWhyNilReaderReportsUnavailable`, `TestDroppedNilReporterReportsUnavailable`,
  `TestCheckpointDependentToolsAreExplicitlyGateable`). The scenario must cover the gated arm too.
- **Negative control:** construct the server without the checkpoint reader — both tools must say
  unavailable and the test must observe that state.

### 4.11 `TestV4_LiveSessionWriteSetAppendOnlyAndImmutability`
- **Package:** `test/e2e` (`v4_x11_test.go`), successor to `TestV3_LiveSessionWriteSetAndAppendOnly`.
- **Wire:** a full live hook sequence against the real binary with checkpoints, pins, the ledger, the
  MCP server and the scheduler all resident.
- **Assert:** every write lands under `.qompack/`; append-only files are only ever appended; finalized
  checkpoints are `0444` / read-only on Windows; a flipped byte yields `core.ErrContract` and exactly
  one `Loud`.
- **`PENDING-A2` clause:** durable publication, restart-manifest and crash-cut coverage belong to unit
  A2. Author the scenario now against the write-set and immutability clauses; add the crash-cut arm
  when A2 lands. Flag this split in review — see [§6](#6-validation-schedule).
- **Negative control:** write one file outside `.qompack/` from a fault-injection site — the write-set
  guard must catch it.

### 4.12 `TestV4_DegradedPassiveWithEverySubsystem`
- **Package:** `test/e2e` (`v4_x12_test.go`), successor to `TestV3_DegradedPassiveStillRecordsEverything`.
- **Wire:** every wave-3 subsystem resident, contract monitor forced into `ModeDegradedPassive`.
- **Assert (suppressed):** no `additionalContext`, no `customInstructions`, no scheduler-initiated
  checkpoint, no drop report. **Assert (still live):** L0 and L1 still record, and MCP retrieval tools
  stay available — §12.1, because they are pull-based and cannot mislead.
- **Existing per-subsystem arms** to consolidate, not duplicate: `TestDegradedPassiveSuppressesActingPaths`,
  `TestDegradedPassiveStillRecords`, `TestService_DegradedPassiveEmitsNothing`,
  `TestE2E_CheckpointDegradedPassiveSealsNothing`,
  `TestIntegration_DegradedPassiveStillWritesToTheRealStore`.
- **Non-vacuity:** the assertion must be *exhaustive over the acting paths*, enumerated from the live
  registry, so that adding a new acting path without gating it fails this test.
- **Negative control:** ungate one acting path — the test must fail naming that path.

### 4.13 `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet`
- **Package:** `test/bench/hotpath` (`v4_x13_test.go`), not `test/e2e` — it is a measured gate and must
  reuse the existing budget/percentile/report machinery.
- **Wire:** the real hook hot path with checkpoints, rehydrator, scheduler, MCP server and ledger all
  resident, measured through the existing `BuildBudgetRowFromSnapshot` / `TailAdjustedP99` path.
- **Assert:** B-A, B-B, B-C, B-D, B-E and B-F all inside budget with the full wave-3 resident set, and
  the scheduler provably off the hot path (`TestSchedulerNotOnHotPath` is the structural analogue).
- **Non-vacuity:** must go through the gated report, so a missing sample is counted as over budget
  (`TestTailAdjustedP99_MissingSamplesAreCountedAsOverBudget`), never silently dropped.
- **Co-load:** ADR 0010 applies. Run in a quiet window; a co-loaded run reports B-A and still gates
  B-B, and the shortfall accounting must be recorded.
- **Waiver:** the three-platform p99 backfill stays **waived-open**; this scenario does not close it.

### 4.14 `TestV4_EveryContractAssertionHasARealProducer`
- **Package:** `internal/contract` or `test/guards` (`v4_x14_test.go`) — a structural guard over the
  live registry, driven from a real composition root.
- **Wire:** the shipped composition root (unit K, `6cf9551` / `332b27e`) binding real `Services`, then
  the live assertion registry.
- **Assert:** for every registered assertion, either a declared producer exists in the bound
  `Services`, or the assertion reports an explicit `unknown` / `unsupported` state with a reason.
- **Retired clause — this is the important one:** the historical "nine assertions, zero without a
  producer" **fixed count is retired** (`V4-SP05-03`, migration disposition row 4). The test must
  enumerate the live registry and reconcile it, and must **report** unknown/unsupported states rather
  than requiring a fixed count. Existing analogues: `TestDeclaredProducerSetMatchesArchitecture`,
  `TestClassifyResult_FreshBuildIsEntirelyUnavailable`.
- **Negative control:** register an assertion with no producer and no unsupported state — the guard
  must fail.

---

## 3. Row-by-row reconciliation (195 retained rows)

Row IDs link back to the preserved historical row in `inventory.md`. The historical text, historical
command, historical expected result and original disposition all remain there, unmodified.

| Row | Historical assertion (abbreviated) | Disposition | Current evidence | Conf. | Note |
|---|---|---|---|---|---|
| <a id="rc-v4-sp01-01"></a>[V4-SP01-01](inventory.md#v4-sp01-01) | Whole lint chain against four new packages and three modified composition roots | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool lint` + `internal/eval`: `TestImportGraph_EvalIsFoundationOnly` | direct | Unit E/J `04bfc71` added `internal/state` to `tools/devtool/importrules.go`; the historical "four new packages" set is now five (checkpoint, rehydrate, scheduler, mcp, state). Re-run against the five-package set. |
| <a id="rc-v4-sp01-02"></a>[V4-SP01-02](inventory.md#v4-sp01-02) | `nomagic` D11/§11.6 with the wave-3 literal set | MAPPED-CMD | _cmd_ `go run ./tools/lint/nomagic ./internal/...` | direct | Analyzer present at `tools/lint/nomagic`. SP-12 ski-rental literals are the live literal set (see V4-SP12-05). |
| <a id="rc-v4-sp01-03"></a>[V4-SP01-03](inventory.md#v4-sp01-03) | `internal/cli` after SP-10 and SP-13 edited it | SUPERSEDED-BY-CORRECTIVE | `internal/cli`: `TestDispatch_HookAlwaysExitsZero`, `TestHookCapture_RefusesBeforeSpoolAndDaemonStart`, `TestNewToolDepsResolvesTheLedgerLive`, `TestShippedDaemonRegistersTheCheckpointIdleTasks` | direct | `internal/cli` was edited after SP-10/SP-13 by A0 (`a123ecc`, `e8c21a0`), K (`6cf9551`) and M (`9166a92`). The row must now cover capture admission, MCP wire and scheduler wiring too. |
| <a id="rc-v4-sp01-04"></a>[V4-SP01-04](inventory.md#v4-sp01-04) | `cmd/qompack/main.go` still under 150 LOC and dispatch-only after wave 3 | MISSING | - | direct | No `TestMainIsDispatchOnly` and no LOC guard exists. Owner: `test/guards` (alongside the other structural guards) or `tools/devtool` lint. Must assert `cmd/qompack/main.go` < 150 lines and that it contains no branch other than dispatch. |
| <a id="rc-v4-sp01-05"></a>[V4-SP01-05](inventory.md#v4-sp01-05) | Plugin bundle: **eight MCP tools are now real** | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool plugin-validate` + `internal/pluginmanifest` | direct | Unit F `f5df6f0` regenerated the MCP tool docs and goldens; `a9a9e1d` changed the `re_read` input schema. Validate against the regenerated manifest, not the historical one. |
| <a id="rc-v4-sp01-06"></a>[V4-SP01-06](inventory.md#v4-sp01-06) | `obs` budget table with **B-F in force** | MAPPED | `internal/obs`: `TestCheckBudgets_NeverReportsUngatedBudgets` | direct | B-F row present in the `internal/obs` budget table. |
| <a id="rc-v4-sp01-07"></a>[V4-SP01-07](inventory.md#v4-sp01-07) | Config docs and schema still cannot drift | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool gen-config-docs --check` | direct | A0 (`internal/config/capture_load_test.go`) and C (`168c685` `store.migrate.legacyImportCutover`) added config keys; `docs/config-reference.md` must be regenerated before this gate is meaningful. |
| <a id="rc-v4-sp01-08"></a>[V4-SP01-08](inventory.md#v4-sp01-08) | Appendix C still reproduced verbatim after four branches touched config consumers | MAPPED | `internal/config`: `TestDefaults_MatchesAppendixCVerbatim` | direct | **NC-1b: not a live failure.** Verified — the golden config surface (`testdata/golden/config/`, `appendix-c.jsonc`) is **byte-identical across `0b14ea6..HEAD`**. B added no config keys, C's gate is a *build* gate rather than a config leaf, and A0's capture work did not move the golden. Consequence for a future wave: store quota is not operator-configurable — see `inventory.md` §NC-1b. |
| <a id="rc-v4-sp01-09"></a>[V4-SP01-09](inventory.md#v4-sp01-09) | Append-only guard now has real checkpoints and real pins to guard | MAPPED | `internal/paths`: `TestAppendOnlyGuard`; `test/guards`: `TestGuard_WriteSetConfinedToQompack`, `TestV1_AppendOnlyInvariantSurvivesRealHookRun` | direct | A0 modified `internal/paths/appendonly_test.go`; real checkpoints and pins now exist to guard. |
| <a id="rc-v4-sp01-10"></a>[V4-SP01-10](inventory.md#v4-sp01-10) | Conformance suites: **only three packages may still skip** | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool lint --only=stubskips` + `test/guards`: `TestStubRegistry_ListsEveryPackageOnDisk`, `TestAllStubsReturnNotImplemented` | direct | Unit J `9e2aa89` added `internal/state` to the stub registry. The "only three packages may skip" clause must be re-read against the current registry, not the historical count. |
| <a id="rc-v4-sp01-11"></a>[V4-SP01-11](inventory.md#v4-sp01-11) | e2e: all six hooks, now with real handlers behind `checkpoint` and `session-start` | MAPPED | `test/e2e`: `TestE2E_AllSixHooksExitZero`, `TestE2E_RunHookRealBinaryMode` | direct | - |
| <a id="rc-v4-sp01-12"></a>[V4-SP01-12](inventory.md#v4-sp01-12) | Build-order guards, **with their V4 semantics** | MAPPED | `test/guards`: `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_O1FlagDefaults`, `TestGuard_SubmodularDefaultsOff` | direct | - |
| <a id="rc-v4-sp01-13"></a>[V4-SP01-13](inventory.md#v4-sp01-13) | Six-target cross build with four new packages linked in | MAPPED-CMD | _cmd_ `go run ./tools/devtool build-all` | direct | - |
| <a id="rc-v4-sp01-14"></a>[V4-SP01-14](inventory.md#v4-sp01-14) | Commit-message policy machinery | MAPPED | `?`: `TestCheckCommitMsg_Subject` (`tools/devtool/checkcommitmsg_test.go`) | by-name | - |
| <a id="rc-v4-sp02-01"></a>[V4-SP02-01](inventory.md#v4-sp02-01) | Whole package still green under race | MAPPED-CMD | _cmd_ `go test -race ./internal/eval/...` | direct | - |
| <a id="rc-v4-sp02-02"></a>[V4-SP02-02](inventory.md#v4-sp02-02) | **The policy registry now has five members** | SUPERSEDED-BY-CORRECTIVE | `internal/eval`: `TestPolicyNames` | direct | Unit L changed the policy set behaviour (`abcbafe` stock keep-set on the host estimate, `85cac20` recall across the cut). Re-read the registry membership against the L tree. |
| <a id="rc-v4-sp02-03"></a>[V4-SP02-03](inventory.md#v4-sp02-03) | `Score.ResidualSpan`, `Score.CompactionPauseMS`, `Score.FirstTurnAfterMS`, `Score.Rehydration... | SUPERSEDED-BY-CORRECTIVE | `internal/eval`: `TestMetricsOf_CoversEveryDirection`, `TestCorpus_RaisesEveryDemandKind`, `TestCarriedDefect_SP02D4_PMinIsMeasuredOverCandidates` | direct | Unit L (`ea2d5c7`, `85cac20`, `internal/eval/corpusshape_test.go`) changed how the four §11.2 fields are produced. Six of the ten carried defects closed here. |
| <a id="rc-v4-sp02-04"></a>[V4-SP02-04](inventory.md#v4-sp02-04) | Latency honesty tags survive the arrival of real producers | MAPPED | `internal/eval`: `provenance_test.go` latency-tag assertions; `test/replay`: `phase4_test.go` `pause_modelled` | direct | Inventory recorded MISSING; the tags are asserted in `internal/eval/provenance.go`/`provenance_test.go` and `test/replay/report.go`/`phase4_test.go`. Correction of a scoring error, not new coverage. |
| <a id="rc-v4-sp02-05"></a>[V4-SP02-05](inventory.md#v4-sp02-05) | Phase-0 baseline still byte-reproducible | SUPERSEDED-BY-CORRECTIVE | `test/replay`: rebaselined phase-0 artifact (commit `e35b44e`) | direct | Unit L `e35b44e` rebaselined phase 0 beside the preserved artifact. The historical "both equal the committed testdata/baseline/phase0.json" clause no longer names the current baseline. Re-derive the expected bytes from the L artifact. |
| <a id="rc-v4-sp02-06"></a>[V4-SP02-06](inventory.md#v4-sp02-06) | **Replay gate at `--phase 4`** — five phase checks now run | MAPPED-CMD | _cmd_ `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --phase 4 --ci` + `test/replay`: `TestGate_CorpusStaleness` | direct | - |
| <a id="rc-v4-sp02-06b"></a>[V4-SP02-06b](inventory.md#v4-sp02-06b) | **The §11.4 bloom ceiling against a live ledger** | MAPPED | `test/integration`: `TestIntegration_RealBloomHealthFeedsTheFPCeiling` | direct | Live-ledger arm of the §11.4 ceiling. |
| <a id="rc-v4-sp02-07"></a>[V4-SP02-07](inventory.md#v4-sp02-07) | Corpus staleness window still satisfied at phase 4 | MAPPED | `test/replay`: `TestGate_CorpusStaleness` | direct | - |
| <a id="rc-v4-sp02-08"></a>[V4-SP02-08](inventory.md#v4-sp02-08) | E-1…E-5 budgets with the corpus now driving four extra policies | MAPPED | `internal/eval`: `BenchmarkBeladyDetail_400Turns`, `BenchmarkSynthesize_320Turns`, `BenchmarkCompare_400Actions`, `BenchmarkBreakpointOPT_256Candidates` | by-name | - |
| <a id="rc-v4-sp02-09"></a>[V4-SP02-09](inventory.md#v4-sp02-09) | `internal/eval` import purity is unchanged by wave 3 | MAPPED | `internal/eval`: `TestImportGraph_EvalIsFoundationOnly`; `internal/sketch`: `TestImports`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp02-10"></a>[V4-SP02-10](inventory.md#v4-sp02-10) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp03-01"></a>[V4-SP03-01](inventory.md#v4-sp03-01) | Whole package green, race, twice | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/sketch/...` | direct | - |
| <a id="rc-v4-sp03-02"></a>[V4-SP03-02](inventory.md#v4-sp03-02) | Frozen on-disk formats survive four more branches | MAPPED | `internal/checkpoint`: `TestGolden`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp03-03"></a>[V4-SP03-03](inventory.md#v4-sp03-03) | `RebuildBloom` is now driven by a **scheduler idle task**, not only by tests | MAPPED | `internal/sketch`: `TestBloom_Rebuild`∗, `BenchmarkRebuildBloom5000`; `internal/daemon`: `TestIdleTaskRebuildBloomSkippedWhenLedgerNil` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp03-04"></a>[V4-SP03-04](inventory.md#v4-sp03-04) | `tried.bloom` generational replacement holds with the scheduler writing on an idle tick | MAPPED | `internal/sketch`: `TestSave_RefusesTriedBloom`, `TestReplaceGenerational_`∗, `TestAppendOnly_TriedBloomNeverTruncated` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp03-05"></a>[V4-SP03-05](inventory.md#v4-sp03-05) | Bloom FP ceiling now surfaces through the MCP `already_tried` path | MAPPED | `internal/sketch`: `TestBloom_EstimatedFPRateMatchesEmpirical`, `TestBloom_SaturatedThreshold` | by-name | - |
| <a id="rc-v4-sp03-06"></a>[V4-SP03-06](inventory.md#v4-sp03-06) | **L0 sketch-update budget unchanged by wave 3** | MAPPED | `internal/sketch`: `BenchmarkL0SketchUpdate`, `TestL0SketchUpdate_ZeroAlloc` | by-name | - |
| <a id="rc-v4-sp03-07"></a>[V4-SP03-07](inventory.md#v4-sp03-07) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp04-01"></a>[V4-SP04-01](inventory.md#v4-sp04-01) | Three packages green under race | MAPPED-CMD | _cmd_ `go test -race ./internal/chunk ./internal/canon ./internal/symbols ./test/dedup` | direct | - |
| <a id="rc-v4-sp04-02"></a>[V4-SP04-02](inventory.md#v4-sp04-02) | **Chunk boundaries now define the MCP minimal span** | MAPPED | `internal/chunk`: `TestSplit_GoldenBoundaries`, `TestSplit_SizeBounds`; `internal/mcp`: `TestMinimalSpanIsChunkAligned`, `TestMinimalSpanNeverExceedsChunkMax` | by-name | - |
| <a id="rc-v4-sp04-03"></a>[V4-SP04-03](inventory.md#v4-sp04-03) | **`symbols.Enclosing` now backs the MCP widener through the `Widener` port** | MAPPED | `internal/symbols`: `TestEnclosing_`∗; `internal/mcp`: `TestSymbolAnchorSelectsEnclosingFunction`, `TestSymbolWideningExtendsToFunctionEnd`, `TestNoWidenerIsTolerated` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp04-04"></a>[V4-SP04-04](inventory.md#v4-sp04-04) | Canon normative properties unchanged | MAPPED | `internal/canon`: `TestEveryCanonicalizer_Idempotent`, `TestEveryCanonicalizer_NeverGrows`, `TestRestore_RoundTrip`, `TestNonGrowingGuard_DropsGrowingMatch` | by-name | - |
| <a id="rc-v4-sp04-05"></a>[V4-SP04-05](inventory.md#v4-sp04-05) | Dedup measurement harness | MAPPED-CMD | _cmd_ `go test -v ./test/dedup/` + `test/dedup`: `TestDedupRatio_WithVsWithout`, `TestDedupReport_Written` | direct | Inventory recorded MISSING; `test/dedup` exists with both assertions. Scoring correction. |
| <a id="rc-v4-sp04-06"></a>[V4-SP04-06](inventory.md#v4-sp04-06) | SP-04 benchmark budgets | MAPPED-CMD | _cmd_ `go test -bench . -benchmem -run '^$' ./internal/chunk ./internal/canon ./internal/symbols` + `internal/chunk`: `BenchmarkSplit_100KB`, `BenchmarkGearScan_1MiB`; `internal/canon`: `BenchmarkRun_Bash100KB`; `internal/symbols`: `BenchmarkExtract_100KB`, `BenchmarkEnclosing_100KB` | direct | Inventory recorded MISSING; every named benchmark exists. Scoring correction. |
| <a id="rc-v4-sp04-07"></a>[V4-SP04-07](inventory.md#v4-sp04-07) | Coverage floors | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp05-01"></a>[V4-SP05-01](inventory.md#v4-sp05-01) | Three packages green, race + repeat | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/ipc ./internal/daemon ./internal/contract` | direct | - |
| <a id="rc-v4-sp05-02"></a>[V4-SP05-02](inventory.md#v4-sp05-02) | **`Services` is no longer mostly nil** | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestBindRunsInOrderAndDeclaresProducers`; `internal/cli`: `TestShippedDaemonRegistersTheCheckpointIdleTasks`, `TestProductionCheckpointSourcesStayLazyAndReportUnavailable` | direct | Unit K (`6cf9551`, `f7ce8cf`, `332b27e`) is the production composition root; `Services` nil-ness is now decided there. |
| <a id="rc-v4-sp05-03"></a>[V4-SP05-03](inventory.md#v4-sp05-03) | **All nine contract assertions have real producers** | RETIRED | `internal/daemon`: `TestDeclaredProducerSetMatchesArchitecture`; `internal/contract`: `TestFreshBuildReportsModeFull`, `TestClassifyResult_FreshBuildIsEntirelyUnavailable` | direct | RETIRED per the inventory's own migration disposition ("fixed nine/zero producer closure"). A fixed count is not assertable; the live registry plus explicit unknown/unsupported states replace it. §4.14 must author the cross-component form. |
| <a id="rc-v4-sp05-04"></a>[V4-SP05-04](inventory.md#v4-sp05-04) | `hook.additional_context_delivered` is a live SevCritical check | RETIRED | `internal/contract`: `TestSentinelNotObservedGivesTwoChances`; `test/e2e`: `TestE2E_ContractSentinelIsAppendedByTheDaemon`, `TestE2E_AdditionalContextProducerIsDeclared` | direct | RETIRED per the migration disposition ("PreCompact output setter / native custom-instruction acceptance"). Replacement asserts the documented adapter path, not native delivery. |
| <a id="rc-v4-sp05-05"></a>[V4-SP05-05](inventory.md#v4-sp05-05) | `precompact.has_time_to_write` and `precompact.custom_instructions_accepted` read a real obse... | RETIRED | `internal/contract`: `assertions_test.go` PreCompact timing assertions | direct | Same migration disposition. `precompact.custom_instructions_accepted` cannot be observed natively; only the supported PreCompact input and the local write remain assertable. |
| <a id="rc-v4-sp05-06"></a>[V4-SP05-06](inventory.md#v4-sp05-06) | `mcp.server_registered` observes a real `initialize` | MAPPED | `?`: `TestMCPInitializedSeamFlipsOnFirstInitialize` | by-name | History file is `state/history.json` via `contract.HistoryPath(root)`. |
| <a id="rc-v4-sp05-07"></a>[V4-SP05-07](inventory.md#v4-sp05-07) | **`ipc.OpMCP` is routed** | MAPPED | `?`: `TestInstallMCPOpRegistersTheHandler`; `internal/daemon`: `TestDaemonMCPOpEmptyRegistryDegrades` | by-name | - |
| <a id="rc-v4-sp05-08"></a>[V4-SP05-08](inventory.md#v4-sp05-08) | **The idle controller now runs six scheduler tasks plus the checkpoint cadence** | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestIdleTasksRegistered`; `internal/cli`: `TestShippedDaemonRegistersTheCheckpointIdleTasks` | direct | K (`6cf9551`/`f7ce8cf`) and M (`929f8a1`, `47d1de0`) changed the registered idle-task set and made ledger/source resolution live. The historical "six tasks plus cadence" count must be re-derived. |
| <a id="rc-v4-sp05-09"></a>[V4-SP05-09](inventory.md#v4-sp05-09) | **Degraded-passive suppresses every wave-3 acting path** | MAPPED | `internal/daemon`: `TestDegradedPassiveSuppressesActingPaths`, `TestDegradedPassiveStillRecords`, `TestService_DegradedPassiveEmitsNothing`; `test/e2e`: `TestE2E_CheckpointDegradedPassiveSealsNothing`; `test/integration`: `TestIntegration_DegradedPassiveStillWritesToTheRealStore` | direct | The §4.12 whole-subsystem form is still MISSING; these are the per-subsystem arms. |
| <a id="rc-v4-sp05-10"></a>[V4-SP05-10](inventory.md#v4-sp05-10) | Hooks exit 0 under every injected fault, with four new subsystems behind them | SPLIT-REVIEW | `test/e2e`: `TestHooksExitZeroUnderFaults`, `TestSelfTestIsTheOnlyNonZeroExit`, `TestFaultSitesInertWhenUnset` | direct | **NC-4: re-enumerated on this candidate — the real count is 66, unchanged.** 6 hooks (`hookSubcommands`) x 11 sites (`allFaultSites` / `e2eFaultSites`) = 66, and `git log 919ca3a..HEAD` touches none of those three tables. A0 added no fault site: it added `faultInflateHookCapture` at the **existing** `oversize` site. Enumeration output in `inventory.md` §NC-4. The fault-injection arm itself is still S7's to run. |
| <a id="rc-v4-sp05-11"></a>[V4-SP05-11](inventory.md#v4-sp05-11) | `sync` → `spool` breach transition still observable | MAPPED | `internal/daemon`: `TestSpoolOnBreachFalse` | by-name | - |
| <a id="rc-v4-sp05-12"></a>[V4-SP05-12](inventory.md#v4-sp05-12) | **B-A/B-B/B-D/B-E hot-path harness with the full wave-3 resident set** | MAPPED-CMD | _cmd_ `go run ./tools/devtool bench-hotpath` | direct | Co-load policy ADR 0010 applies; run in a quiet window. |
| <a id="rc-v4-sp05-13"></a>[V4-SP05-13](inventory.md#v4-sp05-13) | **The scheduler is provably not on the hot path** | MAPPED | `internal/cli`: `TestSchedulerNotOnHotPath` | direct | - |
| <a id="rc-v4-sp05-14"></a>[V4-SP05-14](inventory.md#v4-sp05-14) | Security posture with four new packages | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps` + `test/guards`: `TestGuard_NoNetworkImports` | direct | Inventory recorded MISSING; the lint checks exist. E/J `04bfc71` makes the package set five, not four. Scoring correction plus a scope change. |
| <a id="rc-v4-sp05-15"></a>[V4-SP05-15](inventory.md#v4-sp05-15) | Coverage floors | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp06-01"></a>[V4-SP06-01](inventory.md#v4-sp06-01) | Three packages green, race + repeat | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/store ./internal/redact ./internal/tokens` | direct | - |
| <a id="rc-v4-sp06-02"></a>[V4-SP06-02](inventory.md#v4-sp06-02) | **`SegmentLog.MarkEncoded` is now driven by a real `checkpoint.Advance`** | SUPERSEDED-BY-CORRECTIVE | `internal/checkpoint`: `TestFrontierAdvancer_ReusesFileWriterLiveDraft`, `TestFrontierAdvancer_RetriesOneSealedDraft`, `TestCommittedFrontierIsBackedByDurableEvidence` | direct | Unit G (`02f807a`, `163908e`, `50bc0da`) moved frontier draft lifecycle to a checkpoint-owned `FrontierAdvancer` and requires durable evidence before advancing. Publication ordering of the encoded segment is PENDING-A2. |
| <a id="rc-v4-sp06-03"></a>[V4-SP06-03](inventory.md#v4-sp06-03) | **`OpenSpan` is now the MCP minimal-span read path** | MAPPED | `internal/store`: `TestOpenSpan_Boundaries`, `TestOpenReaders_RejectSubstitutedChunk`, `TestGetChunk_RejectsPhysicallyOversizeObjects` | direct | Unit B object-integrity work strengthens, not replaces, the historical assertion. |
| <a id="rc-v4-sp06-04"></a>[V4-SP06-04](inventory.md#v4-sp06-04) | **`PutOptions.Ephemeral` is now written by a real producer** | MAPPED | `internal/store`: `TestPutBytes_EphemeralFlagPersists`; `internal/mcp`: `TestEveryRetrievalResponseCarriesEphemeralMeta` | by-name | - |
| <a id="rc-v4-sp06-05"></a>[V4-SP06-05](inventory.md#v4-sp06-05) | **GC roots now include real checkpoints and real pins** | SUPERSEDED-BY-CORRECTIVE | `?`: `TestGC_HarvestsHashesFromCheckpoints`; `internal/store`: `TestGC_ReadsTheDeliveryLeaseJournalAsARetentionRoot`, `TestGC_RetainsADeltaWithItsBase`, `TestGC_CannotCollectMigrationOrRollbackMaterial`, `TestMigration_DeclaresRetentionRootsOnDisk`, `TestGC_QuotaNeverEvictsAHardRetentionRoot` | direct | Units B (`aa57f1b`, `7088509`), C and M (`b6820de`) enlarged the root set beyond checkpoints and pins. The historical root list is now a strict subset. |
| <a id="rc-v4-sp06-06"></a>[V4-SP06-06](inventory.md#v4-sp06-06) | `ChangedSince` still exactly hash inequality, now called from the scheduler's idle staleness ... | MAPPED | `?`: `TestChangedSince_Normalizes` | by-name | - |
| <a id="rc-v4-sp06-07"></a>[V4-SP06-07](inventory.md#v4-sp06-07) | Phase-1 dedup ratio unchanged by wave 3 | MAPPED-CMD | _cmd_ `go test -v ./test/dedup/` | direct | - |
| <a id="rc-v4-sp06-08"></a>[V4-SP06-08](inventory.md#v4-sp06-08) | Frozen index formats | SPLIT-REVIEW | `internal/store`: `TestGolden_IndexFormats`, `TestImportCursor_OnDiskShapeIsVersioned`, `TestLoadRoots_ReadsBothRecordVersions` | direct | **NC-3: versioned successor, not retirement.** Verified — `testdata/golden/store/` is **byte-identical across both `919ca3a..HEAD` and `0b14ea6..HEAD`**, so the frozen clause holds and the old-version goldens must still be readable (`TestLoadRoots_ReadsBothRecordVersions` is that guarantee). A new-version golden was added beside them, `testdata/golden/store/roots.v2.jsonl` (`v:1` + `v:2` declared-base). The frozen-format row names both. The two-version reader clause is still S6's to run. |
| <a id="rc-v4-sp06-09"></a>[V4-SP06-09](inventory.md#v4-sp06-09) | Store/redact/tokens performance | MAPPED-CMD | _cmd_ `go test -bench . -benchmem -run '^$' ./internal/store ./internal/redact ./internal/tokens` | direct | Inventory recorded MISSING; `internal/store/bench_test.go` and the sibling packages carry every named benchmark. Scoring correction. |
| <a id="rc-v4-sp06-10"></a>[V4-SP06-10](inventory.md#v4-sp06-10) | Coverage floors | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp07-01"></a>[V4-SP07-01](inventory.md#v4-sp07-01) | Package green under race | MAPPED-CMD | _cmd_ `go test -race ./internal/dag/...` | direct | - |
| <a id="rc-v4-sp07-02"></a>[V4-SP07-02](inventory.md#v4-sp07-02) | **`CrossingEdges` is now `segment_coupling(p)` inside a live p-selection** | RETIRED | `internal/daemon`: `TestAssemble_CouplingFromCrossingEdges`, `TestAssemble_CouplingRecomputedEveryCall`, `TestAssemble_TurnPosCachedUntilTurnAdvances`, `TestAssemble_RecoversCrossingEdgesPanic` | direct | The historical clause "the daemon's cache issues 3 calls on a second assembly and 6 after a graph mutation" describes a coupling cache that no longer exists: `TestAssemble_CouplingRecomputedEveryCall` asserts the opposite semantics, and only turn-position is cached. Retired for genuine semantic change; the coupling-correctness half stays MAPPED. |
| <a id="rc-v4-sp07-03"></a>[V4-SP07-03](inventory.md#v4-sp07-03) | **`BackwardSlice` now ranks eliminations (L5) and decisions (L4)** | MAPPED | `internal/dag`: `TestBackwardSlice`∗, `TestSliceLatencyBudget`; `internal/rehydrate`: `TestEliminations_OrderedBySliceScore`, `TestEliminations_FileProxyScore`; `internal/checkpoint`: `TestExtractRanksBySliceScore` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp07-04"></a>[V4-SP07-04](inventory.md#v4-sp07-04) | **`KindDecision` nodes are now actually created** | MAPPED | `internal/checkpoint`: `TestExtractEmitsDecisionNodesAndEdges`; `internal/dag`: `TestNodeKind`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp07-05"></a>[V4-SP07-05](inventory.md#v4-sp07-05) | **No selection authority** — still mechanically enforced with a live scheduler | MAPPED | `internal/dag`: `TestNoBooleanKeepAPI` | by-name | - |
| <a id="rc-v4-sp07-06"></a>[V4-SP07-06](inventory.md#v4-sp07-06) | Thin-vs-full measurement, still reproduced | MAPPED | `internal/dag`: `TestThinVsFullComparison` | by-name | - |
| <a id="rc-v4-sp07-07"></a>[V4-SP07-07](inventory.md#v4-sp07-07) | Import purity is unchanged | MAPPED-CMD | _cmd_ `go run ./tools/devtool lint` (`importgraph`)` | direct | - |
| <a id="rc-v4-sp07-08"></a>[V4-SP07-08](inventory.md#v4-sp07-08) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp08-01"></a>[V4-SP08-01](inventory.md#v4-sp08-01) | Package green under race | MAPPED-CMD | _cmd_ `go test -race ./internal/observer/...` | direct | - |
| <a id="rc-v4-sp08-02"></a>[V4-SP08-02](inventory.md#v4-sp08-02) | **`OnSessionStart(source=compact)` now delegates to a real rehydrator** | SUPERSEDED-BY-CORRECTIVE | `internal/observer`: `TestOnSessionStart_ClearDelegates`; `internal/daemon`: `TestService_OutOfOrderCheckpointDeliveryReflectsWhateverIsCurrentlyLatest` | direct | Unit H `b43cb68` covers the SessionStart checkpoint-selection lifecycle that the historical row only sketched. |
| <a id="rc-v4-sp08-03"></a>[V4-SP08-03](inventory.md#v4-sp08-03) | **`observer.Signals` is now consumed by the scheduler tap** | MAPPED | `internal/daemon`: `TestWrapServices_ObserveToolDrivesObserve` | by-name | - |
| <a id="rc-v4-sp08-04"></a>[V4-SP08-04](inventory.md#v4-sp08-04) | **Segment lifecycle is now shared between SP-08 and SP-12** | PENDING-A2 | `internal/daemon`: `TestFrontier_NoCloseWithoutCurrentSegment`, `TestObserverPublicationFailureRemainsDrainRetryable` | direct | Segment lifecycle sharing now turns on publication ordering, which unit A2 (daemon admission / delivery-journal wiring / publication ordering / crash cuts) is still to land. Do not score as MISSING. |
| <a id="rc-v4-sp08-05"></a>[V4-SP08-05](inventory.md#v4-sp08-05) | Verbatim user capture is still the **only** intent source | SUPERSEDED-BY-CORRECTIVE | `internal/rehydrate`: `TestUserIntent_EarliestTurnOfThisSessionWins`; `internal/state`: `TestCorrect_SupersedesObsoleteInstructionAndRetainsHistory`, `TestSupersede_OnlyAuthoritativeSourcesMaySupersedeAuthoritativeState` | direct | Unit E `76e0a9e` makes rehydrate honour current `internal/state` authority over frozen or superseded intent. "Verbatim user capture is the only intent source" is now "verbatim capture is the only *raw* source, subject to the authority model". |
| <a id="rc-v4-sp08-06"></a>[V4-SP08-06](inventory.md#v4-sp08-06) | Tombstones are now expandable through a real MCP tool | MAPPED | `internal/observer`: `TestTombstone_RendersTheSection81Form`, `TestTombstone_IsAddressable`; `test/integration`: `TestIntegration_TombstoneRoundTripThroughStore`; `test/e2e`: `TestV3_HookEventToTombstoneToRetrievalRoundTrip` | direct | The named `TestTombstone_DesignExample` no longer exists; `TestTombstone_RendersTheSection81Form` and `TestTombstoneGolden` carry that assertion. §4.5 cross-component form still MISSING. |
| <a id="rc-v4-sp08-07"></a>[V4-SP08-07](inventory.md#v4-sp08-07) | Observer hot-path cost inside B-C, with the scheduler tap added | MAPPED | `internal/observer`: `BenchmarkOnToolUse_FileRead64KB`, `BenchmarkOnToolUse_TestOutput256KB`, `BenchmarkTombstone`; `internal/daemon`: `BenchmarkSchedulerTap_ObserveTool` | by-name | - |
| <a id="rc-v4-sp08-08"></a>[V4-SP08-08](inventory.md#v4-sp08-08) | Observer conformance suite and e2e | SPLIT-REVIEW | `test/e2e`: `TestE2E_VerbatimPromptSurvivesRestart`, `TestE2E_ObserverThroughDaemon` | direct | **NC-5: the clause is per-package, for the four named packages only.** The repo-wide figure quoted here was low — the count on this candidate is **141** `t.Skip` sites across `internal/` and `test/` — so a repo-wide zero is false and never was true. Scoped per package: `internal/observer` 3, `test/e2e` 4. This is a **scoping correction, not new coverage**: the non-zero counts are still the runner's to adjudicate. Per-package table in `inventory.md` §NC-5. |
| <a id="rc-v4-sp08-09"></a>[V4-SP08-09](inventory.md#v4-sp08-09) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp09-01"></a>[V4-SP09-01](inventory.md#v4-sp09-01) | Package green under race | MAPPED-CMD | _cmd_ `go test -race ./internal/negknow/...` | direct | - |
| <a id="rc-v4-sp09-02"></a>[V4-SP09-02](inventory.md#v4-sp09-02) | **`IngestMCP` is now driven by the real `record_eliminated` tool** | MAPPED | `internal/negknow`: `TestIngestMCP`∗; `internal/mcp`: `TestRecordEliminated`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp09-03"></a>[V4-SP09-03](inventory.md#v4-sp09-03) | **The three-way answer is now rendered by two consumers** | SUPERSEDED-BY-CORRECTIVE | `internal/negknow`: `TestThreeWayAnswerFixture_PinsEachState`, `TestQuery_Stale_FlagNote`; `internal/mcp`: `TestAlreadyTriedRendersUnavailableCoverage`, `TestAlreadyTriedRendersUncertainCoverage`, `TestAlreadyTriedQueryErrorsReturnUnavailable` | direct | Unit D (`5935015`, `aa073fa`, `0b530fb`) and F `f9cfcc5` replace the three-way answer with a five-state closed enum: absent / active / stale / unavailable / uncertain. The inventory already retired `TestAlreadyTriedLedgerFailureReturnsAbsent`. |
| <a id="rc-v4-sp09-04"></a>[V4-SP09-04](inventory.md#v4-sp09-04) | **`RefreshStaleness` and `RebuildBloom` are now scheduler idle tasks** | SUPERSEDED-BY-CORRECTIVE | `internal/negknow`: `TestMaintenanceTask_Shape`; `internal/daemon`: `TestIdleTasksRegistered` | direct | M `929f8a1` resolves the scheduler ledger live inside `rebuild_bloom`; the idle-task wiring assertion changed with it. |
| <a id="rc-v4-sp09-05"></a>[V4-SP09-05](inventory.md#v4-sp09-05) | **Bloom-as-cache, with a checkpoint now on disk** | MAPPED | `?`: `TestRebuildBloom_NeverFabricates` | by-name | Includes the one-line grep clause on `internal/negknow/bloom.go`. |
| <a id="rc-v4-sp09-06"></a>[V4-SP09-06](inventory.md#v4-sp09-06) | Eliminations reach the checkpoint's tier-1 slot | MAPPED | `?`: `TestBeginSeedsTierOneFromTheLedger` | by-name | - |
| <a id="rc-v4-sp09-07"></a>[V4-SP09-07](inventory.md#v4-sp09-07) | Phase-2 exit criterion still holds, now also surfaced through MCP | MAPPED | `test/replay`: `TestPhase2ExitCriterion` | direct | - |
| <a id="rc-v4-sp09-08"></a>[V4-SP09-08](inventory.md#v4-sp09-08) | negknow performance budgets | MAPPED | `internal/negknow`: `TestBudget_`∗, `TestMemoryFootprint`, `TestBloomFileSize` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp09-09"></a>[V4-SP09-09](inventory.md#v4-sp09-09) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp10-01"></a>[V4-SP10-01](inventory.md#v4-sp10-01) | Both packages green, race + repeat | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/checkpoint/... ./internal/pins/...` | direct | - |
| <a id="rc-v4-sp10-02"></a>[V4-SP10-02](inventory.md#v4-sp10-02) | **Append-only pins with tombstone deletion and a materialized view** | MAPPED | `internal/pins`: `TestPinsAddAppendsOneLine`, `TestPinsAddIsIdempotent`, `TestPinsMintIDStable`, `TestPinsRemoveWritesTombstone`, `TestPinsRemoveUnknownIsNotFound`, `TestPinsReplaySkipsMalformedLine` | by-name | - |
| <a id="rc-v4-sp10-03"></a>[V4-SP10-03](inventory.md#v4-sp10-03) | **The §8.5 schema, verbatim, in importance order** | MAPPED | `internal/checkpoint`: `TestSchemaFieldOrderIsImportanceOrder`, `TestGoldenCheckpointRoundTrip`, `TestEmptySlicesSerializeAsArrays`, `TestBlockedOnNullIsExplicit`, `TestHashMarshalsAsSha256Prefix`, `TestEliminatedCarriesEverySection85Key` | by-name | - |
| <a id="rc-v4-sp10-04"></a>[V4-SP10-04](inventory.md#v4-sp10-04) | Versioning and migration | MAPPED | `internal/checkpoint`: `TestMigrate`∗, `TestUnmarshalDropsUnknownFields`, `TestMigrateV1IsIdentity`, `TestMigrateRejectsFutureVersion`, `TestMigrateRejectsMissingVersion` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp10-05"></a>[V4-SP10-05](inventory.md#v4-sp10-05) | **No code snippets in checkpoints** (G3.4, §13 invariant 5) | MAPPED | `internal/checkpoint`: `TestGoldenCheckpointsContainNoCodeBlocks` | by-name | - |
| <a id="rc-v4-sp10-06"></a>[V4-SP10-06](inventory.md#v4-sp10-06) | **Store-only regeneration — the GC rule** (§8.5, §13 invariant 1) | SUPERSEDED-BY-CORRECTIVE | `internal/checkpoint`: `TestSourceSetCarriesNoText`; `internal/store`: `TestGC_ReportsPerRootOutcomes`, `TestGC_RetentionRootSourceHoldsObjectsLive` | direct | The GC rule this row depends on was widened by B/C/M — see V4-SP06-05. |
| <a id="rc-v4-sp10-07"></a>[V4-SP10-07](inventory.md#v4-sp10-07) | Incremental draft: begin, resume, discard, advance, abort | SUPERSEDED-BY-CORRECTIVE | `internal/checkpoint`: `TestFrontierAdvancer_ReusesFileWriterLiveDraft`, `TestFrontierAdvancer_RetriesOneSealedDraft`, `TestFrontierAdvancer_ReportsRepeatedSealedDraft`, `TestFrontierAdvancer_CancellationAndEmptyBatchDoNotBegin`, `TestFrontierAdvancer_CancellationBetweenLifecycleCalls` | direct | Unit G `02f807a` keeps draft lifecycle with its owner: the scheduler stores only the port and never calls Begin/Abort. "begin, resume, discard, advance, abort" is now the advancer's contract, not the scheduler's. |
| <a id="rc-v4-sp10-08"></a>[V4-SP10-08](inventory.md#v4-sp10-08) | **The DPI guard at L4** (§4.6, §8.2) | SUPERSEDED-BY-CORRECTIVE | `internal/checkpoint`: `TestAdvanceIsDPIGuarded`, `TestFrontierAdvancer_PreservesPartialDPIGuardOutcome` | direct | G replaced drop-batch-and-never-re-encode with preserve-the-owner-outcome-without-retry. |
| <a id="rc-v4-sp10-09"></a>[V4-SP10-09](inventory.md#v4-sp10-09) | **`ExtractDecisions` — the only producer of `core.DecisionID`** | MAPPED | `internal/checkpoint`: `TestExtract`∗, `TestDecisionIDIsStableAndDeterministic`, `TestExtractFromEdgeExplains` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp10-10"></a>[V4-SP10-10](inventory.md#v4-sp10-10) | **Importance-ordered truncation** (§6.9) | MAPPED | `internal/checkpoint`: `TestTruncate`∗, `TestTruncateDropsTierThreeFirst` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp10-11"></a>[V4-SP10-11](inventory.md#v4-sp10-11) | **Pointer ground-truth validation** (G2.5) | MAPPED | `internal/checkpoint`: `TestValidatePointers`∗, `TestGitIndex`∗, `TestGitDirAsFileWorktree`, `FuzzParseGitIndex` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp10-12"></a>[V4-SP10-12](inventory.md#v4-sp10-12) | MANIFEST, immutability, reader chain, verify, parent fallback | SPLIT-REVIEW | `internal/checkpoint`: `TestFinalizeAppendsAManifestLineThatVerifies`, `TestListRejectsMalformedManifestLine`, `TestVerifyRejectsMalformedManifestLine`, `TestResolveLatestReportsTheFallbackItTook`, `TestResolveChainStopsAtAMissingAncestorWithAReason`, `TestRollbackToAPriorCheckpointWhenTheNewestIsCorrupt`, `TestPublicationFailureRetainsThePreviousCheckpoint` | direct | `TestManifestLineFormat` no longer exists; G `926070d` + `internal/checkpoint/publication_test.go` carry the manifest/verify/parent-fallback clauses. The restart-manifest and crash-cut clause is PENDING-A2, so the row needs an independently reviewed split. |
| <a id="rc-v4-sp10-13"></a>[V4-SP10-13](inventory.md#v4-sp10-13) | **Focus instructions with the O1 incremental span** | RETIRED | `internal/checkpoint`: `TestFrontierAdvancer_ReusesFileWriterLiveDraft`, `TestCommittedFrontierIsBackedByDurableEvidence`; `internal/checkpoint`: `focus_test.go` | direct | RETIRED per the migration disposition ("native O(delta) compaction"). The O1 incremental span is now local draft progress; the focus text remains, the native-span claim does not. |
| <a id="rc-v4-sp10-14"></a>[V4-SP10-14](inventory.md#v4-sp10-14) | **Import discipline — `checkpoint` cannot read a transcript** | MAPPED | `internal/checkpoint`: `TestNoForbiddenImports` | by-name | - |
| <a id="rc-v4-sp10-15"></a>[V4-SP10-15](inventory.md#v4-sp10-15) | **`PreCompact` behaviour, including the near-deadline finalize** | RETIRED | `internal/checkpoint`: `precompact_test.go`, `finalize_paths_test.go`; `internal/cli`: `hook_checkpoint.go` tests | direct | RETIRED per the migration disposition ("PreCompact output setter"). The near-deadline finalize stays assertable; native custom-instruction acceptance does not. |
| <a id="rc-v4-sp10-16"></a>[V4-SP10-16](inventory.md#v4-sp10-16) | **Scheduler-cadence checkpoints, gated by degradation** (§8.5 *"and independently on the sche... | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestCadenceFinalizesWhenDraftReachesBudget`, `TestCadenceSealsNothingWhenNeitherConditionHolds`, `TestACancelledContextStopsTheCadenceLoop`; `test/e2e`: `TestE2E_CheckpointDegradedPassiveSealsNothing` | direct | Unit K moved cadence out of `internal/checkpoint` into `internal/daemon/wire_checkpoint_test.go`, so the historical command targets the wrong package. `TestCadenceFinalizesAfterEightSegments` has no successor — that clause is MISSING and needs an independently reviewed split. |
| <a id="rc-v4-sp10-17"></a>[V4-SP10-17](inventory.md#v4-sp10-17) | Conformance suites, zero skips | MAPPED-CMD | _cmd_ `go test ./internal/checkpoint/... ./internal/pins/... -run 'Suite'` + `internal/checkpoint`: `TestWriterConformanceSuite`, `TestReaderConformanceSuite`; `internal/checkpoint/checkpointtest`: `TestCheckpointSuite_ShapePassesAgainstStub` | direct | Inventory recorded MISSING; all three suites exist (`internal/pins/pinstest` too). The zero-skip clause needs its own count — see NC-5. |
| <a id="rc-v4-sp10-18"></a>[V4-SP10-18](inventory.md#v4-sp10-18) | Checkpoint e2e through the real hook | MAPPED | `test/e2e`: `TestE2E_Checkpoint`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp10-19"></a>[V4-SP10-19](inventory.md#v4-sp10-19) | W-2 contract fixtures are consumable by the real SP-11 and SP-13 | MAPPED-CMD | _cmd_ `go test -count=1 ./internal/rehydrate/... ./internal/mcp/...` | direct | **NC-2: authorized §7.1 exception, both conditions VERIFIED.** (a) the goldens are re-derived by the real generator — all three golden tests pass without `-update` and `gen-mcp-docs --check` is clean; (b) the diff is description text only, no schema shape change. Not a W-2 failure. G5 must still run after `f5df6f0`. See [§7.2](#72-nc-2--the-71-exception-holds-both-conditions-verified). |
| <a id="rc-v4-sp10-20"></a>[V4-SP10-20](inventory.md#v4-sp10-20) | **Checkpoint benchmark budgets, including B-E** | MAPPED | `internal/checkpoint`: `BenchmarkFinalize`, `BenchmarkAdvanceSegment`, `BenchmarkTruncate`, `BenchmarkExtractDecisions`, `BenchmarkStripInjections` | by-name | - |
| <a id="rc-v4-sp10-21"></a>[V4-SP10-21](inventory.md#v4-sp10-21) | **SP-10's own exit gate — frontier advancement shrinks the residual span** | RETIRED | `test/replay`: `phase4_test.go` local frontier accounting | direct | RETIRED per the migration disposition ("native O(delta) compaction or guaranteed first-turn savings"). The 30 % residual-span reduction cannot be claimed; bounded local frontier work plus residual/fidelity accounting replaces it. |
| <a id="rc-v4-sp10-22"></a>[V4-SP10-22](inventory.md#v4-sp10-22) | Placeholder and stub-residue scan | MAPPED-CMD | _cmd_ `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented' -- internal/checkpoint internal/pins internal/daemon/wire_checkpoint.go internal/cli/hook_checkpoint.go` | direct | Inventory recorded MISSING; this is a grep gate, not a test. Scoring correction. |
| <a id="rc-v4-sp10-23"></a>[V4-SP10-23](inventory.md#v4-sp10-23) | Coverage floors | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp11-01"></a>[V4-SP11-01](inventory.md#v4-sp11-01) | Three packages green, race + repeat | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/rehydrate/... ./internal/rules/... ./internal/skills/...` | direct | - |
| <a id="rc-v4-sp11-02"></a>[V4-SP11-02](inventory.md#v4-sp11-02) | Glob engine and `paths:` frontmatter parsing | MAPPED | `internal/rules`: `TestMatch_`∗, `TestParseFront_`∗, `TestParseFront_ListForm` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-03"></a>[V4-SP11-03](inventory.md#v4-sp11-03) | **Path-scoped rule restoration** (G4.1) | MAPPED | `internal/rules`: `TestPathScoped_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-04"></a>[V4-SP11-04](inventory.md#v4-sp11-04) | **Nested `CLAUDE.md` restoration** (G4.2) | MAPPED | `internal/rules`: `TestNestedClaudeMD_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-05"></a>[V4-SP11-05](inventory.md#v4-sp11-05) | **Skill index** (G4.4) | MAPPED | `internal/skills`: `TestIndex_`∗, `TestBodyTokens` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-06"></a>[V4-SP11-06](inventory.md#v4-sp11-06) | **The eight §8.6 items in normative order** | SPLIT-REVIEW | `internal/rehydrate`: `TestRenderOrder_IsIotaOrder`, `TestBuild_ItemOrderInPayload`, `TestItemStats_OneRowPerEmittedItem` | direct | `TestRenderOrder_EightNormativeKindsInIotaOrder` was renamed to `TestRenderOrder_IsIotaOrder`. `TestBuild_RankIsOneBasedAmongEmitted` has no exact successor; `TestItemStats_OneRowPerEmittedItem` is the nearest and needs review before it is accepted for the rank clause. |
| <a id="rc-v4-sp11-07"></a>[V4-SP11-07](inventory.md#v4-sp11-07) | **Injection tagging and the contract sentinel** (§8.5, §12.1) | MAPPED | `internal/rehydrate`: `TestBuild_InjectionTagging`, `TestUnwrap_RejectsMalformed`, `TestUnwrap_RoundTrips`, `TestUnwrap_AcceptsAFutureSchemaVersion`; `internal/contract`: `TestSentinelRoundTrip`, `TestSentinelRenderIsAnInertComment` | direct | `TestSentinel_Deterministic` and `TestBuild_SentinelIsLastLineInsideTags` no longer exist; the sentinel assertions live in `internal/contract`. §4.8 cross-component form still MISSING. |
| <a id="rc-v4-sp11-08"></a>[V4-SP11-08](inventory.md#v4-sp11-08) | Item 1 — invariants are never truncated | MAPPED | `internal/rehydrate`: `TestInvariants_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-09"></a>[V4-SP11-09](inventory.md#v4-sp11-09) | **Item 2 — verbatim intent from L0, never from a summary** (G2.3, G7.3) | SUPERSEDED-BY-CORRECTIVE | `internal/rehydrate`: `TestUserIntent_EarliestTurnOfThisSessionWins`; `internal/state`: `TestSupersede_OnlyAuthoritativeSourcesMaySupersedeAuthoritativeState` | direct | See V4-SP08-05: unit E authority model. |
| <a id="rc-v4-sp11-10"></a>[V4-SP11-10](inventory.md#v4-sp11-10) | Item 3 — eliminations digest and the standing instruction | SUPERSEDED-BY-CORRECTIVE | `internal/rehydrate`: `TestEliminations_NilLedgerReportsTheAbsence`, `TestEliminations_LedgerErrorFallsBackToCheckpoint`, `TestEliminations_GetUnknownKeepsCheckpointCopy` | direct | Unit D `9d9550d`/`aa073fa` changed what a failing ledger renders in the digest. |
| <a id="rc-v4-sp11-11"></a>[V4-SP11-11](inventory.md#v4-sp11-11) | Items 4/5/6 — decisions, current work, **pointers not contents** | MAPPED | `internal/rehydrate`: `TestDecisions_`∗, `TestCurrentWork_`∗, `TestPointers_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-12"></a>[V4-SP11-12](inventory.md#v4-sp11-12) | Items 6a/6b — instruction restoration (G4.1, G4.2, G4.3, G4.4) | MAPPED | `internal/rehydrate`: `TestRestored_`∗, `TestSkillIndex_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-13"></a>[V4-SP11-13](inventory.md#v4-sp11-13) | **Item 7 — the drop report** (G4.5) | SUPERSEDED-BY-CORRECTIVE | `internal/rehydrate`: `TestBuild_ZeroBudgetProducesExplicitOverflow`, `TestBuild_SingleOversizedCriticalRecordOverflows`, `TestBuild_OverflowDetectedWithoutACalibratedEstimator`, `TestDropReport_OrdersPathRulesFirst`, `TestDropReport_SortsByIDWithinAKind` | direct | Unit H `0259c68` added an explicit `dropKindOverflow` for unfittable content; the drop report now names overflow instead of silently truncating. |
| <a id="rc-v4-sp11-14"></a>[V4-SP11-14](inventory.md#v4-sp11-14) | **The 8–12K budget discipline** (G3.3, §8.6, §12 risk row) | SUPERSEDED-BY-CORRECTIVE | `internal/rehydrate`: `TestClampBudget_NeverRaisesAndNeverExceedsTheCap`, `TestBuild_NeverExceedsMaxTokens`, `TestBuild_SmallerThanStock`, `TestBuild_MinFillReadmitsUnits`, `TestBuild_ZeroBudgetProducesExplicitOverflow` | direct | Unit H replaced the truncated-flag model: `TestBuild_PrefixTruncationNotCheapestFirst`, `TestBuild_CarryForward` and `TestBuild_TruncatedFlagSet` no longer exist. The carry-forward clause has no successor and needs an independently reviewed split. |
| <a id="rc-v4-sp11-15"></a>[V4-SP11-15](inventory.md#v4-sp11-15) | Drop-report persistence — the backing for the MCP `dropped` tool | MAPPED | `internal/rehydrate`: `TestItemStats_OneRowPerEmittedItem`; `internal/mcp`: `TestDroppedReturnsDropReport`, `TestDroppedNilReporterReportsUnavailable`; `test/e2e`: `TestE2E_DropReporterSatisfiesMCP` | direct | - |
| <a id="rc-v4-sp11-16"></a>[V4-SP11-16](inventory.md#v4-sp11-16) | Rehydration goldens and degradation | MAPPED | `internal/rehydrate`: `TestBuild_Golden_`∗, `TestBuild_NonCompactSourceEmitsNothing`, `TestBuild_ContextCancelled`, `TestBuild_NilDeps` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-17"></a>[V4-SP11-17](inventory.md#v4-sp11-17) | **G7.5 — the checkpoint is the fallback when the summarizer fails** | MAPPED | `test/e2e`: `TestE2E_SessionStartCompactAfterFailedSummary` | direct | - |
| <a id="rc-v4-sp11-18"></a>[V4-SP11-18](inventory.md#v4-sp11-18) | Daemon `SessionStart(compact\\\|clear)` service | RETIRED | `test/e2e`: `TestE2E_SessionStartCompact`, `TestE2E_SessionStartClear`; `internal/daemon`: `TestService_OutOfOrderCheckpointDeliveryReflectsWhateverIsCurrentlyLatest` | direct | RETIRED per the migration disposition ("native custom-instruction acceptance"). The daemon's SessionStart(compact) handling is assertable; native model compliance is not. Unit H `b43cb68` is the replacement evidence. |
| <a id="rc-v4-sp11-19"></a>[V4-SP11-19](inventory.md#v4-sp11-19) | Rehydration e2e through the real binary | MAPPED | `test/e2e`: `TestE2E_SessionStart`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp11-20"></a>[V4-SP11-20](inventory.md#v4-sp11-20) | `rehydrate.Reporter` structurally satisfies `mcp.DropReporter` | MAPPED | `test/e2e`: `TestE2E_DropReporterSatisfiesMCP` | by-name | - |
| <a id="rc-v4-sp11-21"></a>[V4-SP11-21](inventory.md#v4-sp11-21) | Import discipline | MAPPED-CMD | _cmd_ `go run ./tools/devtool lint` (`importgraph`)` | direct | - |
| <a id="rc-v4-sp11-22"></a>[V4-SP11-22](inventory.md#v4-sp11-22) | **L5 benchmark budgets** | MISSING | `internal/rules`: `BenchmarkPathScoped`; `internal/skills`: `BenchmarkIndex`; `test/e2e`: `TestE2E_SessionStartLatency` | direct | L5-RULES, L5-SKILLS and L5-SESSIONSTART have homes; **L5-BUILD has none** — no `BenchmarkBuild` exists in `internal/rehydrate`. Owner: `internal/rehydrate`; must benchmark `Build` end-to-end against the p99 < 250 ms budget. |
| <a id="rc-v4-sp11-23"></a>[V4-SP11-23](inventory.md#v4-sp11-23) | Conformance suites, zero skips; placeholder scan | MAPPED-CMD | _cmd_ `go test ./internal/rehydrate/... ./internal/rules/... ./internal/skills/... -run Suite` + _cmd_ `git grep -nE 'TODO\|TBD\|FIXME\|XXX' -- internal/rehydrate internal/rules internal/skills` | direct | Inventory recorded MISSING; `rehydratetest`, `rulestest` and `skillstest` all exist. Zero-skip clause: see NC-5. |
| <a id="rc-v4-sp11-24"></a>[V4-SP11-24](inventory.md#v4-sp11-24) | Coverage floors | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp12-01"></a>[V4-SP12-01](inventory.md#v4-sp12-01) | Package green under race, and the daemon files with it | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/scheduler/... ./internal/daemon/` | direct | - |
| <a id="rc-v4-sp12-02"></a>[V4-SP12-02](inventory.md#v4-sp12-02) | **Threshold arithmetic** (§2.5, §8.4) | MAPPED | `internal/scheduler`: `TestEffectiveWindow_Section25Arithmetic`, `TestSoftFloor_`∗, `TestHardCeiling_`∗, `TestThresholds_ZeroWindow`, `TestSoftFloorBelowHardCeiling_Property` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-03"></a>[V4-SP12-03](inventory.md#v4-sp12-03) | **The sliding-TTL idle model, keyed on the last API call** (E1, §5.4, §8.4) | SPLIT-REVIEW | `internal/scheduler`: `TestClassifyTTL_EffortChangeIsColdAtAnyGap`, `TestCacheFactor_Ramp`, `TestCacheFactor_MonotoneDecreasing_Property` | direct | `TestSlidingTTLUsesAPICallNotCacheWrite` no longer exists. The boundary and ramp clauses are covered; the "keyed on the last API call, not the cache write" clause has no named successor and needs review. |
| <a id="rc-v4-sp12-03b"></a>[V4-SP12-03b](inventory.md#v4-sp12-03b) | **The cache regime — which TTL and which `w` this session is actually billed at** (§5.1's *"v... | MAPPED | `internal/scheduler`: `TestResolveCacheRegime_`∗, `TestClassifyTTL_UnknownRegime`∗, `TestClassifyTTL_EffortChange`∗, `TestTTLAnchorIsNeverLaterThanStop` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-04"></a>[V4-SP12-04](inventory.md#v4-sp12-04) | **Young–Daly cadence with δ measured at runtime** (§6.7) | SUPERSEDED-BY-CORRECTIVE | `internal/scheduler`: `TestYoungDalyClauseIsOffUnderShippedDefaults`, `TestExperimentalPoliciesAreOffByDefault`, `TestExperimentalOptInIsExplicitAndReversible` | direct | Unit G `e2448a3` enforces unsupported claims: the Young-Daly clause is off under shipped defaults, so "delta measured at runtime" is now an opt-in experimental path, not a shipped assertion. Supersedes the inventory's earlier bare RETIRED. |
| <a id="rc-v4-sp12-05"></a>[V4-SP12-05](inventory.md#v4-sp12-05) | Ski-rental threshold computed, never written — and **it is two numbers** | MAPPED | `internal/scheduler`: `TestSkiRental_ComputedNotLiteral`, `TestSkiRentalThreshold_TracksRegimeNotConfig`, `TestSkiRental_ThresholdLiteralOnlyInTests`, `TestSkiRentalShouldWrite` | direct | **NC-6: the historical semantics are correct.** `w` is regime-derived and TTL-dependent (`cacheregime.go`: 1.0 disabled, `cfg.Cache.WriteMultiplier` at five minutes, `HostOneHourWriteMultiplier = 2.0` at one hour, `max(cfg, 2.0)` unknown); config is an input to regime selection, not a bypass. The row keeps **both** numbers. `TestSkiRental_ThresholdTracksConfig` asserted the wrong seam and was rewritten to assert through the regime, under the historical name. See [§7.3](#73-nc-6--w-is-regime-derived-the-test-asserted-the-wrong-seam). |
| <a id="rc-v4-sp12-06"></a>[V4-SP12-06](inventory.md#v4-sp12-06) | **BOCD changepoint detection** (§6.6) | MAPPED | `internal/scheduler`: `TestBOCD_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-07"></a>[V4-SP12-07](inventory.md#v4-sp12-07) | **The composite trigger** (§8.4) | SUPERSEDED-BY-CORRECTIVE | `internal/scheduler`: `TestEvaluate_CacheExpiring_FiresBeforeExpiry`, `TestEvaluate_IdleColdCache_Fires`, `TestEvaluate_CacheExpiring_SilentWhenRegimeUnknown`, `TestNativeClaimsAreEnforcedUnsupported` | direct | Unit G `e2448a3` made unsupported native claims a refusal rather than a trigger term, changing the composite trigger itself. Supersedes the inventory's earlier bare RETIRED. |
| <a id="rc-v4-sp12-08"></a>[V4-SP12-08](inventory.md#v4-sp12-08) | **p-selection with the cache term** (§5.3, §5.4, G5.2) | MAPPED | `internal/scheduler`: `TestEvaluate_Argmax`∗, `TestEvaluate_ScoreArithmeticExact`, `TestEvaluate_MultipliersReadFromConfig`, `TestEvaluate_LambdaZeroDisablesDistortion`, `TestEvaluate_RoundBoundary`∗, `TestEvaluate_NoCandidates` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-09"></a>[V4-SP12-09](inventory.md#v4-sp12-09) | `Breakdown` completeness is frozen | MAPPED | `internal/scheduler`: `TestEvaluate_BreakdownKeysComplete`, `TestEvaluate_Background`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-10"></a>[V4-SP12-10](inventory.md#v4-sp12-10) | **The p-selection ship-order gate** (closing note 3) | MAPPED | `internal/scheduler`: `TestPSelectionAvailable_DefaultsFalse`; `test/guards`: `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SubmodularDefaultsOff` | direct | Historical name `TestPSelectionAvailable_DefaultFalse` differs from the shipped `..._DefaultsFalse` by one letter; same assertion. |
| <a id="rc-v4-sp12-11"></a>[V4-SP12-11](inventory.md#v4-sp12-11) | **§8.7 eviction ordering as pure spec** | RETIRED | `internal/scheduler`: `TestDropClassOf_Ephemeral` | by-name | RETIRED per the migration disposition ("native eviction inferred from ephemeral retrieval tags"). §8.7 eviction ordering survives only as pure local spec; no native-history eviction may be inferred from it. |
| <a id="rc-v4-sp12-12"></a>[V4-SP12-12](inventory.md#v4-sp12-12) | Reclaimable-token suffix index | MAPPED | `internal/daemon`: `TestReclaimableIndex_`∗, `BenchmarkReclaimableIndexBuild_5000Blocks` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-13"></a>[V4-SP12-13](inventory.md#v4-sp12-13) | Feature extraction from L0 signals | MAPPED | `internal/daemon`: `TestFeaturesFrom_`∗, `BenchmarkFeaturesFrom` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-14"></a>[V4-SP12-14](inventory.md#v4-sp12-14) | Candidate assembly with a cached `CrossingEdges` | RETIRED | `internal/daemon`: `TestAssemble_CouplingRecomputedEveryCall`, `TestAssemble_TurnPosCachedUntilTurnAdvances` | direct | Same semantic change as V4-SP07-02: the `CrossingEdges` cache was removed. "Candidate assembly with a cached CrossingEdges" is no longer the shipped design. |
| <a id="rc-v4-sp12-15"></a>[V4-SP12-15](inventory.md#v4-sp12-15) | **`scheduler.Runtime` — window ladder, context tokens, EWMAs, persistence, self-healing** | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestRuntime_CloseIsIdempotent`, `TestWrapServices_SessionStartBindsAndSessionEndCloses`; `internal/checkpoint`: `TestFrontierAdvancer_CancellationBetweenLifecycleCalls` | direct | Unit G kept the two test names but replaced their abort assertions with preservation of externally owned drafts. |
| <a id="rc-v4-sp12-16"></a>[V4-SP12-16](inventory.md#v4-sp12-16) | Scheduler state codec | MAPPED | `internal/daemon`: `TestStateCodec_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-17"></a>[V4-SP12-17](inventory.md#v4-sp12-17) | **The `Services` tap — L0 events reach L3 without editing SP-05 or SP-08** | MAPPED | `internal/daemon`: `TestWrapServices_`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp12-18"></a>[V4-SP12-18](inventory.md#v4-sp12-18) | **O5 continuous frontier advancement + the DPI guard at L3** (§8.5) | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestFrontier_DPIGuardPreservesOwnerOutcomeWithoutRetry`; `internal/checkpoint`: `TestCommittedFrontierIsBackedByDurableEvidence`, `TestFrontierAdvancer_PreservesPartialDPIGuardOutcome` | direct | Unit G (`02f807a`, `163908e`, `1a7d842`, `50bc0da`). The scheduler no longer owns the draft, so "O5 continuous frontier advancement at L3" is now a port submission with a locally accounted outcome. Supersedes the inventory's earlier bare RETIRED. |
| <a id="rc-v4-sp12-19"></a>[V4-SP12-19](inventory.md#v4-sp12-19) | **O3 idle-time background work, gated by decision and by degradation** (§8.4) | SUPERSEDED-BY-CORRECTIVE | `internal/daemon`: `TestIdleTaskGatedByDecisionBackground`; `internal/cli`: `TestShippedDaemonRegistersTheCheckpointIdleTasks` | by-name | K and M changed the idle wiring and made ledger/source resolution live (`47d1de0`, `929f8a1`, `9166a92`). |
| <a id="rc-v4-sp12-20"></a>[V4-SP12-20](inventory.md#v4-sp12-20) | **The scheduler is not on the hot path** | MAPPED | `internal/cli`: `TestSchedulerNotOnHotPath` | by-name | - |
| <a id="rc-v4-sp12-21"></a>[V4-SP12-21](inventory.md#v4-sp12-21) | The L3 replay policy | MAPPED | `test/replay/l3policy`: `TestPolicy_DoesNotImportDaemon` | by-name | - |
| <a id="rc-v4-sp12-22"></a>[V4-SP12-22](inventory.md#v4-sp12-22) | Conformance suite, zero skips; placeholder scan; import purity | MAPPED-CMD | _cmd_ `go test ./internal/scheduler/... ./internal/daemon/ -run Suite` + `internal/daemon`: `TestSchedulerSuite_DaemonRuntime`, `TestDetectorSuite_BOCD` + _cmd_ `go run ./tools/devtool lint` | direct | Inventory recorded MISSING; the suites exist. Import purity now *tighter* than the historical claim: `go list -deps ./internal/scheduler` shows only `internal/core` and `internal/config`. |
| <a id="rc-v4-sp12-23"></a>[V4-SP12-23](inventory.md#v4-sp12-23) | **Scheduler benchmark budgets** | MAPPED | `internal/scheduler`: `BenchmarkEvaluate_64Candidates`, `BenchmarkBOCDObserve_4Features`, `BenchmarkBOCDMarshal`; `internal/daemon`: `BenchmarkFeaturesFrom`, `BenchmarkAssembleCandidates_2000ToolUses`, `BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates` | by-name | - |
| <a id="rc-v4-sp12-24"></a>[V4-SP12-24](inventory.md#v4-sp12-24) | **The Phase-4 exit criterion** | RETIRED | `test/replay`: `phase4_test.go` | direct | RETIRED per the migration disposition ("native O(delta) compaction or guaranteed first-turn savings"). The Phase-4 exit criterion may only assert bounded local work and residual/fidelity accounting. |
| <a id="rc-v4-sp12-25"></a>[V4-SP12-25](inventory.md#v4-sp12-25) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-sp13-01"></a>[V4-SP13-01](inventory.md#v4-sp13-01) | Package green under race, and the CLI/daemon files with it | MAPPED-CMD | _cmd_ `go test -race -count=2 ./internal/mcp/...` | direct | - |
| <a id="rc-v4-sp13-02"></a>[V4-SP13-02](inventory.md#v4-sp13-02) | **JSON-RPC 2.0 stdio server** | MAPPED | `internal/mcp`: `TestInitialize`∗, `TestNotification`∗, `TestPing`∗, `TestUnknownMethod`∗, `TestMalformedJSON`∗, `TestWrongJSONRPCVersion`∗ | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp13-03"></a>[V4-SP13-03](inventory.md#v4-sp13-03) | Stdout purity, concurrency, panic isolation | MAPPED | `internal/mcp`: `TestNoStdoutPollution`, `TestConcurrentCallsProduceWellFormedLines`, `TestHandlerPanicIsolated` | by-name | - |
| <a id="rc-v4-sp13-04"></a>[V4-SP13-04](inventory.md#v4-sp13-04) | Server fuzz | MAPPED | `internal/mcp`: `FuzzServeLine` | by-name | - |
| <a id="rc-v4-sp13-05"></a>[V4-SP13-05](inventory.md#v4-sp13-05) | In-repo JSON-schema validator | MAPPED | `internal/mcp`: `TestSchema`∗, `TestApplyDefaults`∗, `TestAllEightSchemasCompile` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp13-06"></a>[V4-SP13-06](inventory.md#v4-sp13-06) | **Exactly the eight §8.7 tools, in the design order** | SUPERSEDED-BY-CORRECTIVE | `?`: `TestToolsListIsExactlyTheEightDesignTools` | by-name | Unit F `a9a9e1d` fixed the `re_read` input schema and `f5df6f0` regenerated docs/goldens. The eight-tool set is unchanged; the schemas are not. |
| <a id="rc-v4-sp13-07"></a>[V4-SP13-07](inventory.md#v4-sp13-07) | **The minimum-sufficient-span resolver** (§8.7) | MAPPED | `internal/mcp`: `TestMinimalSpan`∗, `TestSingleChunk`∗, `TestFull`∗, `TestExplicit`∗, `TestSymbol`∗, `TestLineAnchor` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp13-08"></a>[V4-SP13-08](inventory.md#v4-sp13-08) | `recall` | SUPERSEDED-BY-CORRECTIVE | `internal/mcp`: `TestRecallOmitsHitsWhoseStoredPathFailsAuthorization` | direct | Unit F `d09afd0`/`dcaf7c4` added authorization to `recall`. |
| <a id="rc-v4-sp13-09"></a>[V4-SP13-09](inventory.md#v4-sp13-09) | `expand` | SUPERSEDED-BY-CORRECTIVE | `internal/mcp`: `TestExpandRedactsSecretsTheCaptureTimePolicyMissed`, `TestExpandRedactionNeverLogsTheSecret` | direct | Unit F added read-time redaction to `expand`. |
| <a id="rc-v4-sp13-10"></a>[V4-SP13-10](inventory.md#v4-sp13-10) | `re_read` with historical `at` | SUPERSEDED-BY-CORRECTIVE | `internal/mcp`: `TestReReadRedactsSecretsTheCaptureTimePolicyMissed` | direct | Unit F `dcaf7c4` removed the `re_read` disk fallback (the historical-fidelity fix) and `ac22d1b` covers history fidelity. Historical `at` no longer reads current disk. |
| <a id="rc-v4-sp13-11"></a>[V4-SP13-11](inventory.md#v4-sp13-11) | **`already_tried` — three-way, never a false positive** (G6.2, §8.3) | SUPERSEDED-BY-CORRECTIVE | `internal/mcp`: `TestAlreadyTriedQueryErrorsReturnUnavailable`, `TestAlreadyTriedRendersUncertainCoverage`, `TestAlreadyTriedRendersUnavailableCoverage` | direct | Units D and F `f9cfcc5`: three-way becomes five-state. "Never a false positive" survives; "three-way" does not. |
| <a id="rc-v4-sp13-12"></a>[V4-SP13-12](inventory.md#v4-sp13-12) | `record_eliminated` | MAPPED | `internal/mcp`: `TestRecordEliminated`∗, `TestRecordEliminatedIsNotEphemeral` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp13-13"></a>[V4-SP13-13](inventory.md#v4-sp13-13) | `timeline`, `why`, `dropped` | SUPERSEDED-BY-CORRECTIVE | `internal/mcp`: `TestCheckpointDependentToolsAreExplicitlyGateable`, `TestWhyNilReaderReportsUnavailable`, `TestDroppedNilReporterReportsUnavailable`, `TestWhyFindsDecisionInLatestCheckpoint`, `TestWhySearchesParentChain` | direct | Unit F `a9a9e1d` added the `ToolDeps` checkpoint gate, so `why`/`dropped` now report unavailable rather than erroring. §4.10 cross-component form still MISSING. |
| <a id="rc-v4-sp13-14"></a>[V4-SP13-14](inventory.md#v4-sp13-14) | **Ephemeral-at-birth** (§8.7, §12 re-inflation row) | RETIRED | `internal/mcp`: `TestEveryRetrievalResponseCarriesEphemeralMeta` | direct | RETIRED per the migration disposition ("native eviction inferred from ephemeral retrieval tags"). Ephemeral-at-birth remains a local representation hint only. |
| <a id="rc-v4-sp13-15"></a>[V4-SP13-15](inventory.md#v4-sp13-15) | Expansion promotion counting (feeds SP-16's Phase 7) | RETIRED | `internal/mcp`: `TestNoteExpansion` | by-name | Same migration disposition. Promotion counting is a local hint feeding SP-16 Phase 7; it may not be read as native re-inflation. |
| <a id="rc-v4-sp13-16"></a>[V4-SP13-16](inventory.md#v4-sp13-16) | **Daemon wiring and the `mcp.server_registered` observable** | SUPERSEDED-BY-CORRECTIVE | `internal/cli`: `TestNewToolDepsResolvesTheLedgerLive`, `TestNewToolDepsWithoutAnAccessorStaysNilTolerant`; `?`: `TestMCPInitializedSeamFlipsOnFirstInitialize` | by-name | Unit M `9166a92` resolves the MCP tools' ledger live rather than by value. |
| <a id="rc-v4-sp13-17"></a>[V4-SP13-17](inventory.md#v4-sp13-17) | `qompack mcp` transcoder never destabilizes the session | MAPPED | `internal/cli`: `TestCmdMCP`∗, `TestCmdMCPNeverSpools` | by-name | Names marked ∗ are stems from the historical row; several current tests share the prefix. |
| <a id="rc-v4-sp13-18"></a>[V4-SP13-18](inventory.md#v4-sp13-18) | MCP e2e over real stdio, and the standing-instruction agreement | MAPPED | `test/e2e`: `TestStdioServerEndToEnd`, `TestStandingInstructionsAgree` | by-name | - |
| <a id="rc-v4-sp13-19"></a>[V4-SP13-19](inventory.md#v4-sp13-19) | Docs and manifest agreement | SUPERSEDED-BY-CORRECTIVE | _cmd_ `go run ./tools/devtool gen-mcp-docs --check` + _cmd_ `go run ./tools/devtool plugin-validate` | direct | Unit F `f5df6f0` regenerated `docs/mcp-tools.md` and the goldens. |
| <a id="rc-v4-sp13-20"></a>[V4-SP13-20](inventory.md#v4-sp13-20) | Import discipline and the `nomagic` set | MAPPED-CMD | _cmd_ `go run ./tools/devtool lint` | direct | - |
| <a id="rc-v4-sp13-21"></a>[V4-SP13-21](inventory.md#v4-sp13-21) | **Budget B-F** (`00-ARCHITECTURE.md` §2.4) | MAPPED-CMD | _cmd_ `go run ./tools/devtool bench-hotpath` | direct | `f189f2e` quieted the MCP budget checks; confirm B-F is still gated, not merely reported. |
| <a id="rc-v4-sp13-22"></a>[V4-SP13-22](inventory.md#v4-sp13-22) | MCP micro-benchmarks | MAPPED | `internal/mcp`: `BenchmarkDispatchExpandMinimal`, `BenchmarkDispatchRecall`, `BenchmarkResolveSpanMinimal`, `BenchmarkToolsListMarshal` | by-name | - |
| <a id="rc-v4-sp13-23"></a>[V4-SP13-23](inventory.md#v4-sp13-23) | Placeholder scan and out-of-scope discipline | SPLIT-REVIEW | _cmd_ `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented' -- internal/mcp internal/daemon/mcpop.go internal/cli/cmd_mcp.go internal/cli/mcpwire.go` | direct | Inventory recorded MISSING; the grep gate exists. But the out-of-scope-diff clause is now false by construction: F and M edited `internal/cli/mcpwire.go` and `internal/mcp` deliberately, so the "no SP-13 edit outside its packages" diff must be rebased on the corrective baseline. |
| <a id="rc-v4-sp13-24"></a>[V4-SP13-24](inventory.md#v4-sp13-24) | Coverage floor | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | - |
| <a id="rc-v4-all-01"></a>[V4-ALL-01](inventory.md#v4-all-01) | Full suite, race | MAPPED-CMD | _cmd_ `go run ./tools/devtool test-race` | direct | Gate G1. `devtool test-race` excludes `test/e2e` per `runner-coverage-review.md`; the plain e2e arm is `go run ./tools/devtool test`. |
| <a id="rc-v4-all-02"></a>[V4-ALL-02](inventory.md#v4-all-02) | Full suite, Windows repeat | MAPPED-CMD | _cmd_ `go test -count=2 ./... -timeout=30m` | direct | Gate G2. Runnable on this Windows host; the ubuntu/macos arms need CI or WSL2. |
| <a id="rc-v4-all-03"></a>[V4-ALL-03](inventory.md#v4-all-03) | Local CI | MAPPED-CMD | _cmd_ `go run ./tools/devtool ci-local` | direct | Gate G3/G4 aggregate: fmt-check, lint, vet, build, test, cover, plugin-validate, gen-config-docs --check. |
| <a id="rc-v4-all-04"></a>[V4-ALL-04](inventory.md#v4-all-04) | Coverage floors, all binding except three stubs | MAPPED-CMD | _cmd_ `go run ./tools/devtool cover` | direct | **NC-7: still exactly three.** `internal/state` carries a **binding 75 % floor** (`plans/OWNERS.tsv:57`), not a fourth exemption: `tools/devtool/cover.go`'s `landedSubplans` lists `SP-20`. The exempt set is unchanged — `commands` (SP-14), `analyzer` and `grammar` (SP-15), the three packages whose subplans have not landed. The historical row's “a fourth exemption is a failure” is honoured. |
| <a id="rc-v4-all-05"></a>[V4-ALL-05](inventory.md#v4-all-05) | Security posture | MAPPED-CMD | _cmd_ `go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...` + _cmd_ `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps` | direct | Inventory recorded MISSING; both commands exist. `govulncheck` needs network access to the vulnerability database. |
| <a id="rc-v4-all-06"></a>[V4-ALL-06](inventory.md#v4-all-06) | Placeholder scan across every implemented package | MAPPED-CMD | _cmd_ `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented\|handle edge cases' -- internal/ test/ tools/ ':!*_test.go'` | direct | - |
| <a id="rc-v4-all-07"></a>[V4-ALL-07](inventory.md#v4-all-07) | Full CI on `verify/v4` | MISSING | GitHub Actions on `verify/v4` | direct | Inventory recorded MISSING and it still is: no `verify/v4` branch exists and no CI run covers this tree. Not runnable locally by construction. Also gated by the V3 J5 billing waiver (run 32932419445) which stands waived-open. |
| <a id="rc-v4-all-08"></a>[V4-ALL-08](inventory.md#v4-all-08) | `Qompack.md` immutability | RETIRED | `Qompack.md` Revision log (currently v1.3) | direct | **NC-1a applied: RETIRED as written, and the replacement gate is MISSING.** The historical assertion is factually false on this tree and was false before this work: `git diff <root> HEAD -- Qompack.md` reports 268 insertions / 1082 deletions, and the file stands at v1.3 with an authorized Revision log — read-only means read-only *to subplans*, not frozen. Replacement assertion: **`Qompack.md` changes only through an authorized Revision-log entry with a matching `QOMPACK-ERRATA.md` record.** No guard enforces it — searched `test/guards`, `tools/devtool` and the repo hooks — so the gate is recorded **MISSING, not passed**. See [§7.1](#71-nc-1a--the-replacement-assertion-has-no-enforcing-guard). |

### 3.1 Every `RETIRED` row, with its justification

Fourteen rows are retired. Ten inherit one of the four migration dispositions already recorded in
`inventory.md`; four are retired here for the first time, on evidence from this tree.

| Row | Reason | Replacement pointer |
|---|---|---|
| `V4-SP05-03` | Migration disposition 4: a **fixed** nine-producer / zero-gap closure is not assertable. Capability and evidence states must be reported, not counted. | `internal/daemon`: `TestDeclaredProducerSetMatchesArchitecture`; `internal/contract`: `TestClassifyResult_FreshBuildIsEntirelyUnavailable`; §4.14 authoring brief. |
| `V4-SP05-04` | Migration disposition 1: `hook.additional_context_delivered` as a **native** acceptance signal. Only the documented adapter path is observable. | `internal/contract`: `TestSentinelNotObservedGivesTwoChances`; `test/e2e`: `TestE2E_ContractSentinelIsAppendedByTheDaemon`, `TestE2E_AdditionalContextProducerIsDeclared`. |
| `V4-SP05-05` | Migration disposition 1: `precompact.custom_instructions_accepted` asserts native model compliance. | `internal/contract/assertions_test.go` PreCompact timing assertions (supported input + local write only). |
| `V4-SP07-02` | **New here.** The historical clause "the daemon's cache issues 3 calls on a second assembly and 6 after a graph mutation" describes a coupling cache that no longer exists. `TestAssemble_CouplingRecomputedEveryCall` asserts the *opposite* semantics; only turn-position is cached now. Genuine semantic change, not an inconvenient test. | `internal/daemon`: `TestAssemble_CouplingFromCrossingEdges`, `TestAssemble_CouplingRecomputedEveryCall`, `TestAssemble_TurnPosCachedUntilTurnAdvances`. The coupling-correctness half of the row stays live. |
| `V4-SP10-13` | Migration disposition 2: the O1 incremental span as a **native** O(delta) claim. | `internal/checkpoint`: `TestFrontierAdvancer_*`, `TestCommittedFrontierIsBackedByDurableEvidence`; the local focus text in `focus_test.go` survives. |
| `V4-SP10-15` | Migration disposition 1: PreCompact **output setter** / native custom-instruction acceptance. The near-deadline finalize survives; the native claim does not. | `internal/checkpoint`: `precompact_test.go`, `finalize_paths_test.go`; `internal/cli` checkpoint hook tests. |
| `V4-SP10-21` | Migration disposition 2: the "≥ 30 % residual-span reduction" exit gate is a native-compaction claim. | `test/replay/phase4_test.go` bounded local frontier accounting; §4.4 authoring brief. |
| `V4-SP11-18` | Migration disposition 1: native custom-instruction acceptance at `SessionStart(compact)`. The daemon's own handling remains assertable. | `test/e2e`: `TestE2E_SessionStartCompact`, `TestE2E_SessionStartClear`; unit H `b43cb68` `internal/daemon/rehydrate_service_test.go`. |
| `V4-SP12-11` | Migration disposition 3: §8.7 eviction ordering read as **native** eviction. It survives as pure local spec only. | `internal/scheduler`: `dropclass_test.go` (`TestDropClassOf_*`) as local drop-class ordering; §4.7 authoring brief. |
| `V4-SP12-14` | **New here.** Same semantic change as `V4-SP07-02`: the `CrossingEdges` cache was removed, so "candidate assembly with a *cached* `CrossingEdges`" is no longer the shipped design. | `internal/daemon`: `TestAssemble_CouplingRecomputedEveryCall`, `TestAssemble_TurnPosCachedUntilTurnAdvances`, `TestAssemble_RecoversCrossingEdgesPanic`. |
| `V4-SP12-24` | Migration disposition 2: the Phase-4 exit criterion as guaranteed first-turn savings. | `test/replay/phase4_test.go`, restricted to bounded local work plus residual/fidelity/accounting evidence. |
| `V4-SP13-14` | Migration disposition 3: ephemeral-at-birth read as native re-inflation control. | `internal/mcp`: `TestEveryRetrievalResponseCarriesEphemeralMeta` as a local representation hint. |
| `V4-SP13-15` | Migration disposition 3: expansion promotion counting read as native eviction feedback. | `internal/mcp`: `promote_test.go` (`TestNoteExpansion*`) as a local hint feeding SP-16 Phase 7. |
| `V4-ALL-08` | **New here, on measured evidence.** The assertion "`git diff <root> HEAD -- Qompack.md` is empty" is **false on this tree**: the diff reports 268 insertions / 1 082 deletions. `Qompack.md` is revisable-with-authorization and stands at v1.3 with a Revision log; "immutable" was never the shipped rule. | Replacement assertion: every `Qompack.md` change carries an authorized entry in its Revision log, and no subplan may edit it. Which guard enforces this is **NC-1**. |

Nothing was retired for being hard to find or inconvenient to run. The eleven mis-scored `MISSING`
rows listed in [§1](#1-disposition-counts) were **not** retired — they were re-mapped to the commands
that satisfy them.

---

## 4. The eight whole-tree gates

The coordinator's gate list for V4 is **suite/race, supported-platform repetition, lint/import guards,
formatting, installed manifest validation, replay, measured hot path, gate/report review**. The
historical `V4-ALL-01..08` rows in `inventory.md` enumerate a *different* eight (full-suite race,
Windows repeat, `ci-local`, coverage floors, security posture, placeholder scan, full CI on
`verify/v4`, `Qompack.md` immutability). Both are given below and cross-referenced; the divergence is
**NC-9** and must be settled before the gate report is written. Nothing here renumbers the historical
rows.

| Gate | What it gates | Exact command on this tree | Runnable here? |
|---|---|---|---|
| **G1** suite / race | Whole tree green under the race detector | `go run ./tools/devtool test-race` | **Yes.** `go1.26.6 windows/amd64`, `CGO_ENABLED=1`, `CC=gcc` (MSYS2 gcc 14.2.0) — the race detector's C toolchain requirement is satisfied. Per `runner-coverage-review.md`, `test-race` `go list ./...` minus `test/e2e`; the e2e arm runs plain via `go run ./tools/devtool test`. Budget ~30 min; ADR 0010 co-load policy applies (two wall-clock tests fail under co-load). |
| **G2** supported-platform repetition | Repeat run on each supported platform | `go test -count=2 ./... -timeout=30m` | **Partly.** The Windows arm is runnable here. **ubuntu and macOS are not** — they need CI or the WSL2 route (`/mnt/host/c`). |
| **G3** lint / import guards | Import graph, test deps, bin deps, stub skips, nomagic, sleepcheck, docmarkers, runpatterns, errors | `go run ./tools/devtool lint` (subset: `--only=importgraph,testdeps,bindeps`) | **Yes — and its `runpatterns` sub-check currently FAILS**, with sixteen unsatisfiable `-run` patterns, every one of them in `inventory.md`. See [§4.1](#41-g3-does-not-pass-on-this-tree-today) and NC-10. |
| **G4** formatting | gofumpt/gofmt conformance | `go run ./tools/devtool fmt-check` | **Yes, with a caveat.** `devtool fmt` has previously scanned sibling worktrees, producing phantom offenders *and* rewriting other branches' checkouts under `fmt -w`. Run `fmt-check` (never `fmt -w`) and confirm the reported paths are inside this worktree before believing any offender. |
| **G5** installed manifest validation | Plugin bundle / MCP tool manifest agrees with the shipped binary | `go run ./tools/devtool plugin-validate`; paired with `go run ./tools/devtool gen-mcp-docs --check` and `gen-config-docs --check` | **Yes.** Must be run **after** unit F's regenerated docs/goldens (`f5df6f0`) — see NC-2. |
| **G6** replay | Deterministic replay gate at phase 4 | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --phase 4 --max-cpu 3m --ci` | **Yes.** Phase-0 baseline was rebaselined by unit L `e35b44e`; compare against that artifact, not the historical `testdata/baseline/phase0.json` bytes. |
| **G7** measured hot path | B-A … B-F budgets from real samples | `go run ./tools/devtool bench-hotpath` | **Yes, but conditionally valid.** ADR 0010: a co-loaded run reports B-A and still gates B-B, with shortfall accounting. Run in a quiet window or the numbers are not certifiable. The three-platform p99 backfill stays **waived-open**. |
| **G8** gate / report review | Human review of the produced gate reports and this map | *No command.* Review the artifacts G1–G7 emit, plus `plans/sdd/V4-VERIFY/inventory.md`, this file, and `runner-coverage-review.md` | **Yes** (manual). Includes signing off every `SPLIT-REVIEW` row and every NC answer. |

**Cross-reference to the historical rows.** `V4-ALL-01`→G1, `V4-ALL-02`→G2, `V4-ALL-03`→G1+G3+G4+G5
(`ci-local` runs fmt-check, lint, vet, build, test, cover, plugin-validate, gen-config-docs --check),
`V4-ALL-04`→coverage (no G-slot in the coordinator's eight — see NC-9), `V4-ALL-05`→G3 plus
`govulncheck`, `V4-ALL-06`→G3 (placeholder scan), `V4-ALL-07`→**not runnable here at all**,
`V4-ALL-08`→retired, replaced by a Revision-log check under G8.

**Not runnable in this environment:**
1. **G2's ubuntu and macOS arms** — this is a Windows host. Needs CI or WSL2.
2. **`V4-ALL-07` (full CI on `verify/v4`)** — no such branch exists and no CI run covers this tree; it
   is a push-and-watch gate by construction. It also sits behind the V3 J5 billing waiver (run
   `32932419445`), which stands **waived-open**.
3. **`govulncheck` under G3's security pairing** — needs network access to the vulnerability database.
   The lint half runs offline; the vuln half does not.

Everything else in the table is runnable on this host today.

---

### 4.1 G3 does not pass on this tree today

> **Superseded 2026-09-08 (NC-10).** All sixteen were fixed **in place** in `inventory.md` — no waivers — and `go run ./tools/devtool lint --only=runpatterns` now **PASSes**, with the same seven pre-existing waivers and none added. The three classes below remain the record of what was wrong and why; the corrected commands and the case count each now selects are in `inventory.md` §NC-10.

`go run ./tools/devtool lint --only=runpatterns` was executed while writing this map — it is a static
document check, not a suite — and it **fails**, with all sixteen findings in `inventory.md` itself:

```
FAIL runpatterns: runpatterns: 16 unsatisfiable -run pattern(s)
```

Zero findings are in this file. The sixteen are independent, machine-produced corroboration of the
adjudications in [§3](#3-row-by-row-reconciliation-195-retained-rows), and they fall into three
classes:

1. **The test genuinely no longer exists** — `TestFrontier_DPIGuardViolationDropsBatchAndNeverReEncodes`
   (`V4-SP06-02`, `V4-SP12-18`), `TestAssemble_CrossingEdgesCached` / `..._CacheInvalidatedOnGraphChange`
   (`V4-SP07-02`), `TestCadence*` in `./internal/checkpoint` (`V4-SP10-16`),
   `TestRenderOrder_EightNormativeKindsInIotaOrder` / `TestBuild_RankIsOneBasedAmongEmitted`
   (`V4-SP11-06`), `TestSentinel_Deterministic` / `TestBuild_SentinelIsLastLineInsideTags`
   (`V4-SP11-07`), `TestBuild_PrefixTruncationNotCheapestFirst` / `_CarryForward` / `_TruncatedFlagSet`
   (`V4-SP11-14`), `TestSlidingTTLUsesAPICallNotCacheWrite` (`V4-SP12-03`),
   `TestSkiRentalThreshold_TracksRegimeNotConfig` (`V4-SP12-05`), and the two `TestV4_*` scenarios
   quoted inside rows (`V4-SP05-03`, `V4-SP05-09`, `V4-SP08-06`).
2. **The historical command names the wrong package** — three rows run `-run TestSchedulerNotOnHotPath`
   against `./internal/scheduler`, but the test lives in `internal/cli`
   (`internal/cli/scheduler_hotpath_test.go`). `go test` prints `ok` and exits 0, so those rows
   verify nothing as written. Affects `V4-SP05-13` and `V4-SP12-20`.
3. **Over-escaped alternation** — three `./internal/mcp` rows carry `\\|` where a table row needs
   `\|`, so the regexp holds a literal backslash and matches nothing. The named tests *do* exist
   (`TestNoteExpansion` is in `internal/mcp/promote_test.go`); two of the three also name
   `./internal/mcp` for tests that live in `internal/daemon` and `internal/cli`. Affects
   `V4-SP13-15`, `V4-SP13-16`, `V4-SP13-17`.

**Consequence for the gate report.** G3 cannot be recorded as passing until these sixteen are fixed,
waived with `<!-- runpatterns: reason -->`, or the rows are re-run against the corrected commands this
map supplies. Class 2 and class 3 are the silent-pass defect this check exists to catch: they exit 0
today. This file deliberately does **not** edit the historical rows to fix them — that is a
coordinator decision, and it is **NC-10**.


## 5. `NEEDS-COORDINATOR` questions

**All ten were ruled on by the coordinator on 2026-09-08 and the rulings applied. The questions are preserved verbatim below; the answers, the evidence behind them and what each one changed are in [§7](#7-coordinator-rulings-on-nc-1--nc-10-and-what-was-applied-2026-09-08).**

| # | Row(s) | Question |
|---|---|---|
| **NC-1** | `V4-SP01-08`, `V4-ALL-08` | `Qompack.md` is not byte-identical to the root commit (268 insertions / 1 082 deletions) and carries a Revision log at v1.3, so `V4-ALL-08` as written is false. `V4-SP01-08` asserts `internal/config` defaults match **Appendix C verbatim**, while units C (`168c685`, `store.migrate.legacyImportCutover`) and A0 (capture keys) added defaults. Two decisions needed: (a) confirm `V4-ALL-08` is retired and name the guard that enforces "authorized Revision-log entry only"; (b) confirm Appendix C was revised to include the new keys, or record `V4-SP01-08` as a live failure. |
| **NC-2** | `V4-SP10-19`, `V4-SP13-19`, G5 | Rule W-2 says a frozen contract fixture the real writer cannot reproduce is a **verification failure — fix the implementation, not the fixture**. Unit F `f5df6f0` regenerated the MCP docs and goldens. Was that an authorized §7.1 exception, or does it need to be re-derived from the real writer? |
| **NC-3** | `V4-SP06-08` | C `33a7c9e` migrated evidence envelopes "compatibly" and B added a second root-record version (`TestLoadRoots_ReadsBothRecordVersions`). Are the frozen store index goldens still byte-identical, or does the frozen-format row now need a versioned successor? |
| **NC-4** | `V4-SP05-10` | A0 `a123ecc` added capture admission before persistence and modified `internal/cli/fault_test.go`. Does the historical **66/66** fault-combination count still hold, or has the fault-site matrix changed? A recount is needed before the row can be scored. |
| **NC-5** | `V4-SP08-08`, `V4-SP10-17`, `V4-SP11-23`, `V4-SP12-22` | Four rows carry a "zero `t.Skip`" clause scoped to their own packages, but 76 `t.Skip` sites exist across `internal/` and `test/`. Are the per-package subsets required to be zero, or is a repo-wide allowlist the current rule? |
| **NC-6** | `V4-SP12-05` | The historical `TestSkiRentalThreshold_TracksRegimeNotConfig` was replaced by `TestSkiRental_ThresholdTracksConfig` — the *inverse* claim about where `w` comes from. Is the write multiplier read from `Inputs.Regime.WriteMultiplier` (historical) or from config (current name)? The row asserts two numbers (12.5 at `w=1.25`, 20 at `w=2.0`) that depend on the answer. |
| **NC-7** | `V4-ALL-04` | Units E/J added `internal/state` (`9e2aa89` stub registry, `04bfc71` import rule). Is `internal/state` a fourth coverage exemption, or does it carry a binding floor? The historical row says a fourth exemption is a **failure**. |
| **NC-8** | §4.6, §4.7 | Two §4 scenario **names** now assert retired semantics. `TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction` says "three-way" where the enum is five-state (units D + F); `TestV4_EphemeralRetrievalResultsRankFirstForEviction` says "eviction" where only local drop-class ordering may be claimed (migration disposition 3). Rename both, or keep the names and rely on the briefs' retired-clause guards? |
| **NC-9** | `V4-ALL-01`..`V4-ALL-08` | The coordinator's eight gates (suite/race, platform repetition, lint/imports, formatting, installed manifest, replay, hot path, gate review) are not the same eight as the historical `V4-ALL-01..08` rows. `V4-ALL-04` (coverage floors) has no slot in the new eight, and formatting / installed-manifest / gate-review have no historical row. Which enumeration is the V4 gate set of record? §4 above answers both, but the report can only carry one. |
| **NC-10** | `inventory.md` historical commands | `devtool lint --only=runpatterns` fails with sixteen unsatisfiable `-run` patterns, all in `inventory.md` (see [§4.1](#41-g3-does-not-pass-on-this-tree-today)). Three are wrong-package commands and three are over-escaped alternations that exit 0 while verifying nothing. Fix the historical commands in place, add `runpatterns` waivers, or accept the corrected commands from this map as the rows of record? This unit did not edit them, to keep the historical text intact. |

---

## 6. Validation schedule

Grouped so each distinct command runs **once**, with its artifact linked to every row it covers. Run
groups in the order listed: cheap structural gates first, so an early failure costs minutes, not the
30-minute race budget.

| # | Command (runs once) | Snapshot | Rows covered | Artifact |
|---|---|---|---|---|
| **S1** | `go run ./tools/devtool fmt-check` | G4 | (whole-tree; no per-row historical row) | `.v4-artifacts/v4-fmt.*` |
| **S2** | `go run ./tools/devtool lint` | G3 | `V4-SP01-01`, `V4-SP01-02`, `V4-SP01-10`, `V4-SP02-09`, `V4-SP05-14`, `V4-SP07-07`, `V4-SP10-14`, `V4-SP11-21`, `V4-SP12-22`, `V4-SP13-20`, `V4-ALL-05` | `.v4-artifacts/v4-lint.*` |
| **S3** | `git grep -nE 'TODO\|TBD\|FIXME\|XXX\|not implemented\|handle edge cases' -- internal/ test/ tools/ ':!*_test.go'` | G3 | `V4-SP10-22`, `V4-SP11-23`, `V4-SP13-23`, `V4-ALL-06` | `.v4-artifacts/v4-placeholder.txt` |
| **S4** | `go run ./tools/devtool build-all` | G3 | `V4-SP01-13` | `.v4-artifacts/v4-crossbuild.*` |
| **S5** | `go run ./tools/devtool plugin-validate` + `gen-mcp-docs --check` + `gen-config-docs --check` | G5 | `V4-SP01-05`, `V4-SP01-07`, `V4-SP13-19` | `.v4-artifacts/v4-manifest.*` |
| **S6** | `go run ./tools/devtool test-race` | G1 | Every "package green under race" row: `V4-SP02-01`, `V4-SP03-01`, `V4-SP04-01`, `V4-SP05-01`, `V4-SP06-01`, `V4-SP07-01`, `V4-SP08-01`, `V4-SP09-01`, `V4-SP10-01`, `V4-SP11-01`, `V4-SP12-01`, `V4-SP13-01`, `V4-ALL-01` — **plus every `MAPPED` unit-test row**, since one race run over the tree executes all of them. | `.v4-artifacts/v4-race.*` |
| **S7** | `go run ./tools/devtool test` (plain, includes `test/e2e`) | G1 | `V4-SP01-11`, `V4-SP05-10`, `V4-SP08-08`, `V4-SP10-18`, `V4-SP11-19`, `V4-SP13-18`, `V4-SP12-10` guards arm, and the fourteen new §4 scenarios once authored | `.v4-artifacts/v4-suite.*` |
| **S8** | `go test -count=2 ./... -timeout=30m` (Windows) | G2 | `V4-SP03-01`, `V4-SP04-01`, `V4-SP05-01`, `V4-SP06-01`, `V4-SP10-01`, `V4-SP11-01`, `V4-SP12-01`, `V4-ALL-02` | `.v4-artifacts/v4-repeat-windows.*` |
| **S9** | `go run ./tools/devtool cover` | — | `V4-SP02-10`, `V4-SP03-07`, `V4-SP04-07`, `V4-SP05-15`, `V4-SP06-10`, `V4-SP07-08`, `V4-SP08-09`, `V4-SP09-09`, `V4-SP10-23`, `V4-SP11-24`, `V4-SP12-25`, `V4-SP13-24`, `V4-ALL-04` | `.v4-artifacts/v4-cover.*` |
| **S10** | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --phase 4 --max-cpu 3m --ci` | G6 | `V4-SP02-04`, `V4-SP02-06`, `V4-SP02-07`, `V4-SP09-07`, `V4-SP12-21`, `V4-SP12-24` | `.v4-artifacts/v4-replay-phase4.json` |
| **S11** | `go run ./tools/devtool replay` A/B with `checkpoint.frontier.advanceOnSegmentClose` toggled — **two** runs, one snapshot pair | G6 | `V4-SP10-21`, §4.4. **Independent-review split required:** the retired native-O(delta) clause must not be scored from this artifact; only bounded local work and both recorded P50s may be. | `.v4-artifacts/v4-frontier-{on,off}.json` |
| **S12** | `go test -v ./test/dedup/` | — | `V4-SP04-05`, `V4-SP06-07` | `.v4-artifacts/v4-dedup.*` |
| **S13** | `go test -bench . -benchmem -run '^$' ./internal/chunk ./internal/canon ./internal/symbols ./internal/store ./internal/redact ./internal/tokens ./internal/sketch ./internal/negknow ./internal/dag ./internal/scheduler ./internal/mcp ./internal/checkpoint ./internal/rules ./internal/skills` — **quiet window** | G7 | `V4-SP02-08`, `V4-SP03-06`, `V4-SP04-06`, `V4-SP06-09`, `V4-SP07-06`, `V4-SP08-07`, `V4-SP09-08`, `V4-SP10-20`, `V4-SP11-22` (partial — L5-BUILD has no benchmark), `V4-SP12-23`, `V4-SP13-22` | `.v4-artifacts/v4-bench.txt` + `benchstat` |
| **S14** | `go run ./tools/devtool bench-hotpath` — **quiet window, alone** | G7 | `V4-SP05-12`, `V4-SP05-13`, `V4-SP12-20`, `V4-SP13-21`, §4.13. **Co-load declaration must be recorded with the artifact** (ADR 0010). | `.v4-artifacts/v4-hotpath.json` |
| **S15** | `go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...` | G3 | `V4-SP05-14`, `V4-ALL-05` (vuln half) | `.v4-artifacts/v4-vuln.*` — **needs network** |
| **S16** | Manual: read the S1–S15 artifacts, this map, `inventory.md`, `runner-coverage-review.md` | G8 | `V4-SP01-04` (manual LOC inspection until a guard exists), `V4-ALL-07` (CI), `V4-ALL-08` (Revision log), every `SPLIT-REVIEW` row, every NC answer | `.v4-artifacts/v4-gate-review.md` |

### 6.1 Rows needing an independently-reviewed coverage split

These seven cannot be scored from a single artifact; one clause passes on one snapshot while another
clause has no home, is retired, or is blocked on A2. Each needs a reviewer other than the runner to
sign the split.

| Row | The split |
|---|---|
| `V4-SP05-10` | Fault-injection arm covered by S7; the **66/66 combination count** needs a recount after A0's admission change (NC-4). |
| `V4-SP06-08` | Frozen-golden clause covered by S6; the **versioned evidence-envelope and two-version root record** clauses are new and unscored (NC-3). |
| `V4-SP08-08` | e2e arm covered by S7; the **zero-`t.Skip`** clause needs its own per-package count (NC-5). |
| `V4-SP10-12` | Manifest / verify / parent-fallback covered by S6; **restart-manifest and crash-cut** clauses are `PENDING-A2`. |
| `V4-SP10-16` | Cadence covered by S6 in `internal/daemon` (moved by unit K); the **eight-segment** clause has no successor test and is `MISSING`. |
| `V4-SP11-06` | Order clauses covered by S6; the **rank-is-one-based** clause maps only approximately to `TestItemStats_OneRowPerEmittedItem`. |
| `V4-SP11-14` | Budget clamp / max-tokens / min-fill covered by S6; the **carry-forward** clause has no successor after unit H's overflow model. |
| `V4-SP12-03` | TTL boundary and cache-factor ramp covered by S6; the **"keyed on the last API call, not the cache write"** clause has no named successor. |
| `V4-SP13-23` | Placeholder grep covered by S3; the **out-of-scope diff** clause is false by construction on the corrective baseline and must be rebased. |

`V4-SP08-04` is `PENDING-A2` in full, not split: publication ordering is A2's to land.

### 6.2 What must be written before V4 can be scored complete

| Item | Owner package | Why |
|---|---|---|
| The fourteen `TestV4_*` §4 scenarios | `test/e2e` (11), `test/replay` (1, §4.4), `test/bench/hotpath` (1, §4.13), `internal/contract` or `test/guards` (1, §4.14) | All `MISSING`; briefs in [§2](#2-the-fourteen-4-cross-component-scenarios). |
| `BenchmarkBuild` for L5-BUILD | `internal/rehydrate` | `V4-SP11-22`: L5-RULES, L5-SKILLS and L5-SESSIONSTART have homes; the p99 < 250 ms `Build` budget has none. |
| A dispatch-only guard for `cmd/qompack/main.go` | `test/guards` | `V4-SP01-04`: no test asserts the < 150 LOC / dispatch-only shape; today it is manual inspection. |
| An eight-segment cadence case | `internal/daemon` | `V4-SP10-16`: `TestCadenceFinalizesAfterEightSegments` has no successor after unit K's move. |

---

## 7. Coordinator rulings on NC-1 … NC-10, and what was applied (2026-09-08)

All ten `NEEDS-COORDINATOR` questions in [§5](#5-needs-coordinator-questions) have been ruled on and
the rulings applied. **Still not a pass:** no suite, benchmark or gate was run to completion here.
Two things were *measured* rather than assumed, and both are recorded with their commands: NC-2's
two conditions and NC-4's re-enumeration.

| # | Ruling | Applied where |
|---|---|---|
| **NC-1a** | `V4-ALL-08` **RETIRED as written**; replacement assertion recorded; enforcing gate **MISSING** | `inventory.md` row `V4-ALL-08`; §7.1 below |
| **NC-1b** | **Not** a live failure — `V4-SP01-08` stands, verified | `inventory.md` §NC-1b; row disposition `MAPPED` |
| **NC-2** | Authorized §7.1 exception, **conditional — both conditions verified and held** | §7.2 below; rows `V4-SP10-19`, `V4-SP13-19`, G5 |
| **NC-3** | Versioned successor, not retirement | `inventory.md` §NC-3; `testdata/golden/store/roots.v2.jsonl` |
| **NC-4** | Re-enumerated: the real count is **66**, unchanged | `inventory.md` §NC-4 |
| **NC-5** | Per-package, four named packages only; **141** repo-wide `t.Skip` sites | `inventory.md` §NC-5 |
| **NC-6** | `w` is regime-derived and TTL-dependent; historical semantics correct; the test asserted the wrong seam and was fixed | `internal/scheduler/skirental_test.go`; §7.3 below |
| **NC-7** | `internal/state` takes a **binding floor**, not a fourth exemption | `inventory.md` row `V4-ALL-04` |
| **NC-8** | **Keep both §4 scenario names** | §2.6, §2.7 briefs below; §7.4 |
| **NC-9** | The historical `V4-ALL-01..08` is the gate set **of record**; differences recorded, not substituted | `inventory.md` §NC-9; §4 stays as the divergence table |
| **NC-10** | Fix all sixteen `-run` patterns **in place**; no waivers | `inventory.md` §NC-10; [§4.1](#41-g3-does-not-pass-on-this-tree-today) is now historical |

### 7.1 NC-1a — the replacement assertion has **no enforcing guard**

"`Qompack.md` byte-identical to the root commit" is false, and was false before this work:
`git diff <root> HEAD -- Qompack.md` reports 268 insertions / 1 082 deletions, and the file stands at
**v1.3** with an authorized Revision log. Read-only means read-only **to subplans**, not frozen.

**Replacement assertion.** *`Qompack.md` changes only through an authorized Revision-log entry with a
matching `plans/QOMPACK-ERRATA.md` record.*

**Does any guard enforce it? No.** Searched, not assumed:

- `test/guards` has no revision-log or errata case (`grep -rn 'Revision log\|ERRATA' --include=*.go test/ tools/ internal/` returns only two *comments*, in `internal/eval/corpusshape_test.go:149` and `internal/eval/ledger_test.go:359`, both citing errata as a source rather than checking it).
- `tools/devtool` reads `Qompack.md` in exactly one place, `genconfigdocs.go`, and only to generate Appendix C.
- There is no repository git hook that inspects the file.

So the gate is recorded **MISSING**, not passed. Writing it — a check that a commit touching
`Qompack.md` also adds a Revision-log entry whose version has a matching errata record — belongs to
`test/guards` and is future-wave work, not something this unit may land (it does not own that tree).

### 7.2 NC-2 — the §7.1 exception **holds**: both conditions verified

Rule W-2 targets a fixture the real writer **cannot reproduce**. Unit F's change is an intended
contract change that the writer reproduces exactly, so the exception applies — but only on evidence.
Both conditions were checked on this candidate:

**(a) Re-derived by the real generator, not hand-edited — HOLDS.**

```
$ go run ./tools/devtool gen-mcp-docs --check
gen-mcp-docs: docs/mcp-tools.md is up to date

$ go test ./internal/mcp -run 'TestSchemaGoldensStable|TestToolsListMatchesGolden|TestInitializeResultMatchesGolden' -count=1 -v
--- PASS: TestInitializeResultMatchesGolden (0.01s)
--- PASS: TestToolsListMatchesGolden (0.02s)
--- PASS: TestSchemaGoldensStable (0.12s)
ok  	github.com/qompack/qompack/internal/mcp	0.405s
```

All three golden comparisons pass **without** `-update`, and the docs generator reproduces
`docs/mcp-tools.md` byte-for-byte. A hand-edited golden would fail here; these do not.

**(b) The diff is schema/description text only — HOLDS.**

`git show f5df6f0 -- testdata/golden/mcp/` touches two files and changes only `description` strings:

- `schemas/re_read.json` — the `at` argument's description ("Empty for the working-tree version…" → "Empty for the latest captured version… Never reads the working tree.").
- `tools-list.v2.json` — the same `at` description plus `re_read`'s own tool description.

No property was added or removed, no `type` changed, no `additionalProperties` or `required` moved.
The other six schema files and `initialize.v2.json` rewrote byte-identical.

**Verdict: an authorized §7.1 exception, not a W-2 verification failure.** `V4-SP10-19` and
`V4-SP13-19` are adjudicated `MAPPED-CMD` on that basis, and **G5 must still be run after
`f5df6f0`** — the exception covers the goldens' provenance, not the manifest gate.

### 7.3 NC-6 — `w` is regime-derived; the test asserted the wrong seam

`internal/scheduler/cacheregime.go` sets `WriteMultiplier` per regime, and **config is an input to
regime selection, not a bypass**:

| Rung | `WriteMultiplier` |
|---|---|
| disabled (`DISABLE_PROMPT_CACHING`) | `1.0` — no cache, so no write premium |
| five-minute (`subagent`, `FORCE_PROMPT_CACHING_5M`) | `cfg.Cache.WriteMultiplier` — Appendix C's floor, passed through |
| one-hour (`ENABLE_PROMPT_CACHING_1H`) | `HostOneHourWriteMultiplier = 2.0` |
| unknown (nothing set — the default) | `max(cfg.Cache.WriteMultiplier, 2.0)`, the dearer of the two |

The **historical semantics are correct** and the row keeps **both** numbers: `w/r == 12.5` at
`r=0.1, w=1.25` and `== 20` at `r=0.1, w=2.0`, with `cfg.Cache.WriteMultiplier` at 1.25 in both cases.

`TestSkiRental_ThresholdTracksConfig` fed bare literals to `SkiRentalShouldWrite`, which cannot
distinguish "w came from config" from "w came from the regime" — the very question the two numbers
turn on. It has been rewritten to resolve a real `CacheRegime` and assert **through** it, and
restored to the historical name `TestSkiRentalThreshold_TracksRegimeNotConfig`, which is also the
name `inventory.md`'s `V4-SP12-05` command has always used.

```
$ go test ./internal/scheduler/ -run SkiRental -count=1 -v
--- PASS: TestSkiRentalShouldWrite (0.00s)
--- PASS: TestSkiRental_ComputedNotLiteral (0.00s)
--- PASS: TestSkiRentalThreshold_TracksRegimeNotConfig (0.00s)
--- PASS: TestSkiRental_ThresholdLiteralOnlyInTests (0.00s)
ok  	github.com/qompack/qompack/internal/scheduler	1.493s
```

### 7.4 NC-8 — both §4 scenario names are **kept**

`TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction` (§2.6) and
`TestV4_EphemeralRetrievalResultsRankFirstForEviction` (§2.7) keep their names. They are the
identifiers `inventory.md` keys on, and renaming them breaks traceability from the inventory rows
to the briefs and back. The corrected semantics live in each brief's **retired-clause guard**, which
already says what must not be asserted: a five-state enum rather than a three-way one in §2.6, and
local drop-class ordering rather than native eviction in §2.7. A reviewer scores the brief, not the
name. The fourteen §4 tests are being authored by another unit and are **not** written here.

### 7.5 What these rulings change in [§1](#1-disposition-counts)

The three `NEEDS-COORDINATOR` rows are adjudicated: `V4-SP01-08` → `MAPPED` (NC-1b),
`V4-SP10-19` → `MAPPED-CMD` (NC-2), `V4-SP12-05` → `MAPPED` (NC-6). Of the seven `SPLIT-REVIEW`
rows, three have one arm closed and keep the disposition for the rest: `V4-SP05-10` (count settled
at 66, fault-injection arm still needs S7), `V4-SP06-08` (frozen goldens verified byte-identical and
a versioned successor added; the two-version reader clause is still S6's to run), `V4-SP08-08`
(zero-skip clause scoped, still to be scored per package). `V4-ALL-04`'s exemption question is
closed at three. `V4-ALL-08` stays `RETIRED`, now with an explicit **MISSING** replacement gate.
Nothing was retired for being inconvenient, and no row's identifier changed.

# V5-VERIFY §4.14 disposition — `TestV5_EveryContractAssertionHasARealProducer`

| Field | Value |
|---|---|
| Retained identifier | `TestV5_EveryContractAssertionHasARealProducer` |
| Current criterion (plan §4 row 4.14) | SP-19 per-capability observed/unknown evidence, no fixed producer count or setter claim. |
| Disposition | **authored** — the criterion is asserted with real producers through three real compositions; two historical expectations are retired outright and recorded below |
| Level | `test/e2e` (real binary + real daemon through the v4 harness). The seam crosses processes: the hook subcommands (`session-start`, `observe prompt`, `observe tool`, `checkpoint`, `self-test --json`) run as real child processes over the real transport against a daemon composed the way `internal/cli` composes it (`v4StartRig`), and the producer under test — `internal/daemon`'s SessionStart route appending `contract.ObservationsOf` to `state/observations.json` — runs inside that daemon. This is the historical seam ("drive a full session; read the contract surface") followed, with the output corrected from `self-test`'s nine booleans to SP-19's per-capability ledger |
| File | `test/e2e/v5_x14_test.go` |
| Base | `verify/v5` @ `87c0c1d` |

## Producers established on this tree

- **Register** — `internal/contract/capability.go`: `DefaultCapabilityRegister()` (eight capabilities,
  six evidence statuses, `Validate`). `compaction_request` and `history_rewriting` are `unsupported`.
- **Attribution and classification** — `internal/contract/observation.go`: `CapabilityOf`,
  `ClassifyResult` (unknown → unavailable → unsupported → failed → not_observed → observed, in that
  order), `ObservationsOf`, the ledger (`ObservationLedgerPath`, `Load/SaveObservationLedger`, per-id
  cap 64). `precompact.custom_instructions_accepted` maps to `compaction_request` on purpose.
- **The one writer** — `internal/daemon/observations.go` `recordCapabilityObservations`, called from
  `handleSessionStart` under `historyMu` after `RunAll`; Target = `claude-code`, `GOOS/GOARCH`,
  UTC date, Version empty.
- **Producer declarations** — `internal/daemon/options.go` `DeclareProducers`: five unconditional,
  `precompact.*` iff `PreCompact`/`Checkpoints` bound, `hook.additional_context_delivered` iff
  `Rehydrate` bound (WireObserver → WireRehydrator), `mcp.server_registered` iff `MCPInitialized`
  bound (`InstallMCPOp`, not installed by the v4 rig). `contract.HasProducer` reads the live set.
- **Observations driven** — the sentinel mint/append in `handleSessionStart`, the worker-pool scan
  in `scanSentinelForPrompt` (`RecordSentinelScan`), `handleCheckpoint`'s marker, timeout, wall
  sample and `SetPrecompactInstr`, `checkSessionStartFires`'s marker read across sessions.
- **Read-back surfaces** — `daemon.StatusSnapshot.Contract` (= `monitor.Report()`), and
  `qompack self-test --json` (`internal/cli/selftest.go`), whose contract rows come from a
  throwaway monitor in the CLI process with `DeclareProducers(&Services{})` — i.e. the five.

## Tests and what each asserts

All under the retained top-level function; run with
`-run '^TestV5_EveryContractAssertionHasARealProducer$'`.

| Subtest | Producers | Asserts |
|---|---|---|
| `full_composition_records_per_capability_evidence` | `v4StartRig` (observer + rehydrator + checkpoint writer + bound PreCompact, no MCP op), real binary hooks, real transcript file, `e2eStatus` | Session A: `session-start`(startup) emits the §12.1 probe; the test plays the host and folds the additionalContext into the transcript; `observe prompt` names that transcript; the worker-pool scan is driven to `Sentinel.Observed` (Drain + poll, bounded). Three seeded tool uses, then `qompack checkpoint` (real seal, marker, wall sample, instructions). Then `session-start`(A, source=compact) and `session-start`(B, startup). No degrade banner on any start; status `mode == full`. Ledger: exists, `3 × len(StandardAssertions())` observations, `x14v5AssertLedgerInvariants` on every one (below). Run 1: injection `not_observed`/`not-yet-observed`/coverage none; fires `not_observed`/`first-session`. Run 2: injection `observed`/`sentinel-observed`/`delivery_under_tested_contract`; source_compact `observed`/`compact`; timing `observed` with a real `p99=` sample; custom_instructions: producer declared, Check ran (not the placeholder), attributed to `compaction_request`, outcome `unsupported`. Run 3: fires `observed`/`marker-found`; source_compact `not_observed`/`no-precompact-pending`; payload shape and transcript `observed`; MCP never `observed`. Cross-surface: every `StatusSnapshot.Contract` Result keeps its Observed verbatim in the ledger's last run and `ClassifyResult(result)` equals the ledger outcome, id by id |
| … / `self_test_reads_the_same_ids_and_writes_no_evidence` | real `qompack self-test --json` against the live daemon | exit 0, `mode == full`, `exit == 0`; every standard id present; each row classifies to a non-`unknown` outcome; a `not-yet-implemented` row classifies `unavailable` (never a working capability); custom_instructions never `observed`; `state/observations.json` is byte-identical before and after (self-test appends no evidence) |
| `negative_control_severed_delivery_is_failed_and_loud` | same composition; the host never folds the injected context into the transcript | probe emitted and producer declared (so the failure is not `unavailable`); two `observe prompt` scans each spend a chance (driven and polled to `Chances ≥ 1`, `≥ 2`); fixture check that the transcript carries no probe. Next `session-start`: **non-empty `systemMessage`** (degrade banner), status `mode == degraded-passive`; ledger run 2: injection **`failed`** / `sentinel not found after two chances` / coverage none; invariants hold. Ends with `p.AssertAppendOnly(t)` (no checkpoint sealed in this arm) |
| `observer_only_composition_declares_a_different_producer_set` | `contract.ResetProducers` (as `TestE2E_AdditionalContextProducerIsDeclared` does), `v4StartObserverOnly` (observer + rehydrator, no checkpoint/PreCompact, no MCP) | live reads: injection declared, `precompact.*` and MCP undeclared. One `session-start`, no banner. Ledger: timing `unavailable`/`not-yet-implemented`/coverage none — the assertion arm 1 **observed**; custom_instructions `unavailable` (undeclared wins over unsupported: nothing was attempted); MCP `unavailable`; injection `not_observed`/`not-yet-observed` (declared, undriven: absence, not unavailability); invariants hold |

`x14v5AssertLedgerInvariants` (every observation, every arm): Target provider `claude-code`, Version
empty, Platform `GOOS/GOARCH`, Date non-empty; Capability ∈ `Capabilities()` and `== CapabilityOf(id)`;
Mechanism = the register's record's; Outcome ∈ the six and never `unknown`; Target/TS/Observed carried;
Coverage never `complete` and `delivery_under_tested_contract` **iff** injection ∧ observed;
**`unavailable` ⇔ `!contract.HasProducer(id)`** (live, not a count) **⇔ `Observed == "not-yet-implemented"`**;
an attempted observation of a register-`unsupported` capability is `unsupported`; the outcome
re-derives from the observation's own verbatim Observed through `ClassifyResult`.

## Negative control and how it was proven

Runtime, in the committed test — no source edit was needed and none was applied:

1. **Severed delivery** (`negative_control_severed_delivery_is_failed_and_loud`). Same composition,
   same declared producer, the host simply never writes the injected context into the transcript.
   The assertion arm 1 recorded as `observed` with a delivery-coverage claim is recorded as
   `failed` with no coverage claim, the daemon degrades to passive, and the SessionStart carries
   the banner. So arm 1's `observed` came from the transcript actually carrying the probe, not from
   the producer being declared or the Result being OK.
2. **Composition switch** (`observer_only_composition_declares_a_different_producer_set`). Same
   binary, a composition that binds no PreCompact seam: the assertion arm 1 recorded as `observed`
   (`precompact.has_time_to_write`) is `unavailable`, and the ledger says so with the exact
   placeholder. The equivalence `unavailable ⇔ !HasProducer` is asserted on both sides of the
   switch, which is what makes "no fixed producer count" a checked property rather than a slogan.

The invariants helper is additionally protected from vacuity by `x14v5Ledger` (fails if the file
does not exist rather than reading a fresh empty ledger) and by `x14v5ByID` (every standard id must
appear exactly once per run).

## Old-to-new assertion map

| Historical expectation (§4.14 at the head text) | Disposition | Where / why |
|---|---|---|
| Drive a full session: `session-start` → 20 `observe tool` → `observe prompt` → `qompack checkpoint` → `session-start source=compact` → MCP `initialize` → `flush` → `session-start` | **corrected** | Same shape, reduced to what the criterion needs: 1 prompt + 3 tool uses + checkpoint + compact restart + a second session's start. No MCP `initialize` (the v4 rig does not install the MCP op; see remainder) and no `flush` (irrelevant to the ledger) |
| Input `qompack self-test --json`; exit 0; `mode == "full"` | **kept** | `self_test_reads_the_same_ids_and_writes_no_evidence`: exit 0, `mode == full`, `exit == 0` |
| "**all nine** assertions report a real observation" | **retired** | A fixed producer count is exactly what the criterion forbids. self-test's monitor is throwaway and declares only what its own process declares (`DeclareProducers(&Services{})` — SP-05's five), so it structurally cannot report nine real observations; and the daemon's own set is the composition's. Replaced by the live equivalence `unavailable ⇔ !HasProducer(id)`, asserted on the full and observer-only compositions |
| `mcp.server_registered` must not carry `not-yet-implemented` | **retired** (as a fixed claim); **corrected** to "never `observed` without an initialize" | The v4 rig does not install `InstallMCPOp`, so on this composition the honest reading is `unavailable`; asserted through the equivalence, and MCP is asserted `!= observed` because no initialize was sent |
| `precompact.has_time_to_write` must not carry `not-yet-implemented` | **kept**, per composition | Full composition: `observed` with a real `p99=` sample. Observer-only: `unavailable` — the same id, asserted the other way |
| `precompact.custom_instructions_accepted` must not carry `not-yet-implemented` | **corrected** | The producer is declared and the Check runs (asserted: Observed is not the placeholder) — but the outcome is **`unsupported`**, attributed to `compaction_request`. The "setter claim" is retired (Qompack.md §7.3, §12.1: custom_instructions is PreCompact input; a transcript phrase is not evidence a setter worked) |
| `hook.additional_context_delivered` must not carry `not-yet-implemented` | **kept and strengthened** | Full composition: `observed`/`sentinel-observed` with the one narrow coverage claim after a real delivery; `not_observed` before it; **`failed`** when delivery is severed (negative control) |
| "a `not-yet-implemented` here means a `DeclareProducers`/`Bind` wiring regression" | **retired** | On this tree an undeclared producer is a composition fact recorded as `unavailable`, never as success and never as a regression by itself; SP-19's ledger exists precisely so that reading cannot be mistaken for a working capability |
| (implicit) OK/SevInfo on an undeclared producer reads as pass | **retired** | The ledger's `unavailable` outcome, and coverage `none`, asserted on every such observation |

## Unverified remainder

- **MCP initialize → `mcp.server_registered` `observed`.** The v4 in-process rig cannot install the
  MCP op (`internal/cli.installMCPTools` is unexported; `v4_harness_test.go` records why). A row
  driving a real `qompack mcp` child against a spawned daemon could assert `observed`/
  `initialize-received` in the ledger; this row asserts only that it is never `observed` without one
  and that its `unavailable` reading tracks `HasProducer`. Unverified here, not failed.
- **`plugin.root_resolves`.** `CLAUDE_PLUGIN_ROOT` is unset in the test process, so it lands as
  `not_observed`/`unset` on every arm. Covered by the invariants (attribution, coverage,
  re-derivation), not asserted individually; a `resolved` observation needs a real plugin install.
- **Target version.** Empty by design (no host version in a hook payload); a `verified_in_target`
  record needs SP-19's disposable-target canaries (`test/canary`), which are outside this row.
- **The self-test contract rows' outcomes** are asserted only as a closed-vocabulary,
  no-setter-claim, no-evidence-written surface; which of them are `unavailable` is deliberately not
  asserted (it would be a producer-count claim about the CLI process).

## Run command and result

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x14
go test ./test/e2e -list 'TestV5_EveryContract'
# → TestV5_EveryContractAssertionHasARealProducer
go test ./test/e2e -run '^TestV5_EveryContractAssertionHasARealProducer$' -count=1 -v
```

Two consecutive `-count=1` runs: `ok github.com/qompack/qompack/test/e2e 11.879s` and `9.421s`;
every subtest PASS (full 6.74 s / 4.77 s, negative control 2.75 s / 2.37 s, observer-only
1.95 s / 1.99 s, self-test 0.09 s / 0.06 s). `gofmt -l ./test ./internal` empty; `go vet ./test/e2e`
clean; `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` PASS
on every sub-check. No timing gate is involved (the only bounds are the 30 s scan/index waits, which
are bounds, not latency assertions). No production file changed.

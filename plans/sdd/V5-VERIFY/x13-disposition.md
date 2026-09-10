# V5-VERIFY §4.13 disposition — `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne`

| Field | Value |
|---|---|
| Retained identifier | `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne` |
| Current criterion (plan §4 row 4.13) | Retire ski-rental native cut assertion; SP-12 owns deprecated compatibility and local cadence. |
| Disposition | **authored** — the retirement and the kept half are both asserted with real producers; one historical expectation is retired outright and recorded below |
| Level | `test/integration` (in-process). The seam is `scheduler.Evaluate` (pure), the daemon-owned `scheduler.Runtime` (`daemon.NewSchedulerRuntime` over the real store and real DAG), the capability register (`scheduler.Supports`/`Require`) and the compatibility surface (`scheduler.SkiRentalShouldWrite` fed by the real `ResolveCacheRegime`). Nothing crosses a process or the CLI/MCP: `qompack status --json` does not render `Decision.Breakdown` on this tree (`daemon.SchedulerSnapshotOf` has no production consumer), so the historical "observed through SP-14's status breakdown" leg has no e2e producer to drive |
| File | `test/integration/v5_x13_test.go` |
| Base | `verify/v5` @ `87c0c1d` |

## Why the historical seam is unwritable as written

The historical text asserted a ski-rental clause **inside** `Runtime.Evaluate`: a `"ski_rental_defer"`
reason, a `Breakdown["ski_rental_threshold"] == 12.5` term, and that this clause changed which cut
was chosen. On this tree:

- `scheduler.SkiRentalShouldWrite` has **no production caller** (M0-01 inventory §4.2; `grep -rn -w
  SkiRentalShouldWrite --include=*.go` outside `internal/scheduler` and `test/guards` is empty).
- `scheduler.Inputs.ExpectedRemainingReads` is declared and read by nothing in `Evaluate`; the daemon
  runtime never assembles it.
- The write policy is registered as `CapSkiRentalWritePolicy`, class **experimental**, default **off**
  (`internal/scheduler/unsupported.go`); the only switch is the process-wide
  `scheduler.EnableExperimentalPolicies()`, which the shipped wiring never calls.
- Ski rental has no config key (`internal/config/migration.go`: retired-meaning keys list).
- V4-SP12-05 / NC-6 already established that `w` is regime-derived and TTL-dependent, so the
  single figure 12.5 was never the only break-even (20 at the one-hour TTL).

The plan's §4 preamble retires unsafe guarantees and asks for a compatible correction with a mapping;
§2.12 assigns SP-12 "useful local cadence, unknown observations safe, no native cut/veto"; §6 retires
selector guards that require unsupported native p-selection. A clause that let a cache-write
heuristic move or defer a cut is exactly the retired class.

## Tests and what each asserts

All under the retained top-level function; run with `-run
'^TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne$'`.

| Subtest | Producers | Asserts |
|---|---|---|
| `evaluate_has_no_ski_rental_clause_and_expected_reads_are_inert` / `soft_floor_only_warm` | `scheduler.Evaluate`, `ResolveCacheRegime` (`FORCE_PROMPT_CACHING_5M`), real loaded config from `testutil.NewProject` | Historical input (a): above the soft floor, warm cache, `ExpectedRemainingReads = 5`. `ShouldCompact == false`, `Reasons == [soft_floor]` exactly (no deferral reason), `Urgency == None`, `TTL == warm`; no reason or Breakdown key contains `ski_rental`; `ski_rental_threshold` absent; the Decision is byte-identical for `ExpectedRemainingReads ∈ {0, 5, 100}` |
| … / `hard_ceiling_is_urgent` | same | Historical input (b): above the hard ceiling. `ShouldCompact == true`, `Urgency == Now`, `hard_ceiling` in Reasons, the one legal candidate (turn 10) is chosen; no ski-rental term; identical across the sweep. Also proves on the compatibility surface that `SkiRentalShouldWrite(5, r, w)` is **false** under this regime — the write policy would have "deferred", and the urgent cut fires anyway |
| … / `cold_cache_fires` | same | Historical input (c): TTL cold (idle gap = TTLMax + 1 s). `ShouldCompact == true`, `TTL == cold`, `idle_cold_cache` in Reasons, `Urgency == Advisory`; no ski-rental term; identical across the sweep |
| `real_runtime_urgent_cut_fires_with_no_ski_rental_term` | `daemon.NewSchedulerRuntime` over `p.Store(t)` (real store) and `dag.Open` (real DAG), real BOCD via `Runtime.Observe`, `store.SegmentLog.Open`, the runtime's tap surface (`BindSession`, `AddOpenSegmentTokens`, `NoteAPIRound`), `Runtime.Persist`, `daemon.SchedulerSnapshotOf` | Bound session under `FORCE_PROMPT_CACHING_5M`; empty session evaluates to no compaction and no ski-rental term. Live context pushed to `HardCeilingTokens + 1` (read from the runtime's own first Decision, not a literal); the 200-observation step series makes the real detector declare ≥ 1 changepoint; each is noted as a round boundary. Then: `context_tokens == HardCeiling + 1`, `ShouldCompact == true`, `Urgency == Now`, `soft_floor` and `hard_ceiling` in Reasons, `candidates_supplied ≥ 1`, `P.Turn` is one of the detector's boundaries and a round boundary, no ski-rental term (including the host keys the runtime appends). Persisted `state/scheduler.json` contains no `ski_rental` substring, its `last_decision` is `should_compact:true`, urgency Now, and its `hard_ceiling` matches; the snapshot's `LastDecision` equals the returned Decision and `RegimeSource == force_5m`. Ends with `p.AssertAppendOnly(t)` |
| `register_records_the_policy_disabled_and_the_native_cut_unsupported` | `scheduler.Supports`, `scheduler.Require`, `ExperimentalPoliciesEnabled` | Fresh process is opted out. `CapSkiRentalWritePolicy` → `Available == false`, `Class == experimental`, non-empty `Reason`, `Require` wraps `ErrUnsupported` — recorded as disabled, never passed. `CapNativeCut` → `Class == unsupported`, `Require` errors |
| `compatibility_surface_tracks_the_resolved_regime_not_a_literal` | `scheduler.SkiRentalShouldWrite`, `ResolveCacheRegime` under `FORCE_PROMPT_CACHING_5M` / `ENABLE_PROMPT_CACHING_1H` / `DISABLE_PROMPT_CACHING`, real config | Break-even flips exactly at `w/r` **derived from the resolved regime**: at the five-minute floor `cfg.Cache.WriteMultiplier / cfg.Cache.ReadMultiplier` (12.5 under shipped defaults), at one hour `HostOneHourWriteMultiplier / r` (20); the threshold itself is false, threshold+ε true; the historical input 5 defers under every priced regime; doubling `scheduler.cache.writeMultiplier` doubles the five-minute break-even and leaves the one-hour figure alone; a disabled cache has break-even 1; `r ≤ 0` or `w ≤ 0` never writes |

## Negative controls and how each was proven

1. **Runtime switch (in the committed test).** `scheduler.EnableExperimentalPolicies()` is the
   only switch that can make the register report the write policy available.
   - Register half: flipping it flips `Supports(CapSkiRentalWritePolicy).Available` to true and
     `Require` to nil, while `CapNativeCut` stays `ErrUnsupported`; `DisableExperimentalPolicies()`
     restores the refusal. So "disabled" is a live reading, not a constant answer.
   - Runtime half: with the opt-in live (asserted), the daemon runtime's next `Evaluate` still
     compacts with `Urgency == Now`, the chosen `P` is unchanged, and no ski-rental term appears.
     The opt-in registers a policy; it wires nothing into the cut path.
2. **Source-edit proof (authoring only, reverted).** No runtime switch can put a clause back into
   the pure `Evaluate`, so the retirement assertion was proven non-vacuous by inserting, immediately
   before `fired := aboveSoftFloor &&` in `internal/scheduler/evaluate.go`:

   ```go
   if in.ExpectedRemainingReads > 0 && !SkiRentalShouldWrite(in.ExpectedRemainingReads, reg.ReadMultiplier, reg.WriteMultiplier) {
       d.Breakdown["ski_rental_defer"] = 1
   }
   ```

   Result: `FAIL` — all three `evaluate_has_no_ski_rental_clause_and_expected_reads_are_inert`
   scenarios red with `Should be empty, but was [breakdown:ski_rental_defer]` (and the
   sweep-identity assertion would fail next); the runtime, register and compatibility subtests
   stayed green, as expected (the runtime never assembles `ExpectedRemainingReads`). The edit was
   reverted with `git checkout -- internal/scheduler/evaluate.go`; `git status --porcelain` showed
   only `?? test/integration/v5_x13_test.go` afterwards, and the test passed again.

## Old-to-new assertion map

| Historical expectation (§4.13 at HEAD `7f92af5`) | Disposition | Where / why |
|---|---|---|
| (a) soft reasons only, warm TTL, `ExpectedRemainingReads = 5` ⇒ `ShouldCompact == false` | **kept** (corrected cause) | `soft_floor_only_warm`: false because no disjunct fires, not because anything deferred — `Reasons == [soft_floor]` exactly |
| (a) `"ski_rental_defer"` in `Reasons` | **retired** | Unsafe guarantee: a cache-write heuristic must not defer or move a cut (§2.12, §6). Asserted absent; source-edit control proves the absence is checked |
| (a) `Breakdown["ski_rental_threshold"] == 12.5` | **retired**, threshold semantics **kept elsewhere** | Key asserted absent from every Decision and from `state/scheduler.json`. The break-even lives on the compatibility surface only, and it is two numbers (12.5 at 5 m, 20 at 1 h — V4-SP12-05/NC-6), derived from the resolved regime |
| "computed from config `w/r`, doubling `writeMultiplier` doubles it" | **corrected** | `compatibility_surface_tracks_the_resolved_regime_not_a_literal`: the config key is the five-minute floor; doubling it doubles the five-minute break-even and leaves the documented one-hour `2.0` alone |
| (b) `hard_ceiling` present ⇒ compacts with **no** ski-rental reason | **kept** | `hard_ceiling_is_urgent` (pure) and `real_runtime_urgent_cut_fires_with_no_ski_rental_term` (real store/DAG/BOCD): `ShouldCompact == true`, `Urgency == Now`, no ski-rental term, while `SkiRentalShouldWrite(5, r, w)` is false — "never blocks an urgent one" |
| (c) `TTL = cold` ⇒ compacts with no ski-rental reason | **kept** | `cold_cache_fires` |
| "ski rental changes the chosen cut" (the identifier's first half) | **retired**, asserted in the negative | Decision byte-identical for `ExpectedRemainingReads ∈ {0, 5, 100}` in all three scenarios; `P` unchanged under the register opt-in |
| `qompack status --json` renders the same `Breakdown` map, sorted, every key present | **unverified** (no producer) | Not assertable: `Decision.Breakdown` is not rendered by the status command on this tree and `SchedulerSnapshotOf` has no production consumer. The nearest real surfaces — the persisted `state/scheduler.json` decision and the `SchedulerSnapshot` SP-14 is documented to back — are asserted instead |
| SP-14's `TestEvaluate_BreakdownKeysComplete` golden updated iff SP-16 added keys | **no change needed** | The golden (`testdata/golden/scheduler/decision-warm.json`) gained no ski-rental key; SP-16 added none. This row adds none and touches no golden |

## Unverified remainder

- The `qompack status --json` breakdown rendering (above): no producer on this tree. If SP-14 or
  SP-17 later renders `Decision.Breakdown`, the e2e leg should assert the sorted key set and the
  absence of `ski_rental*` through the real binary.
- "SP-12 owns … local cadence" is asserted only as far as this row reaches it: a real changepoint
  and round-boundary candidate produced by the real detector and assembler, and a local (non-native)
  urgent cut. The wider cadence gates (M5-G12-A–E) are §2.12's, not §4.13's.
- The register's default-off reading is per process; the test asserts it on a fresh state and
  restores it after each opt-in, but a foreign `EnableExperimentalPolicies()` earlier in the same
  test binary would make the "fresh process is opted out" precondition fail loudly rather than
  silently pass.

## Run command and result

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x13
go test ./test/integration -list 'TestV5_SkiRental'
# → TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne
go test ./test/integration -run '^TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne$' -count=1 -v
```

Two consecutive `-count=1` runs: `ok github.com/qompack/qompack/test/integration 1.685s` and
`0.315s`; every subtest PASS. `gofmt -l ./test ./internal` empty; `go vet ./test/integration`
clean; `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns`
PASS on every sub-check. No timing gate is involved. No production file changed.

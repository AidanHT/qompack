# V5-VERIFY §4.6 — `TestV5_SelectorGatedByRealScheduler`

**Identifier (retained):** `TestV5_SelectorGatedByRealScheduler`
**Current criterion (plan §4, authoritative):** SP-12/SP-15 local capability guards replace
native-p-selection prerequisites.
**Disposition:** authored.
**Base:** `verify/v5` @ `87c0c1d`; branch `v5/x06`.

## Level and file

e2e — `test/e2e/v5_x06_test.go`, composed through the V4 rig (`v4StartRig`): real hook events
through the real binary into the real observer, store and DAG; then, in process, the shipped
`daemon.NewSchedulerRuntime` over that store and DAG, the shipped `daemon.CloseSchedulerRuntime`,
the real `scheduler.Evaluate`, the real `analyzer.NewCheapScorer` reading real content roots out
of the real store, and the real `analyzer.NewSelector` / `analyzer.Propose`.

The historical seam (an in-process `NewSchedulerRuntime` against the rig's store/DAG, as
`v4_x02_test.go` does) was writable and is followed. The gate is a process-global atomic in
`internal/scheduler`, so the runtime whose lifetime is asserted has to live in the test process;
the spawned binary's own daemon holds its own copy of the flag and is not what this row observes.

## What the producers are on this tree

- `internal/scheduler/gate.go` — `PSelectionAvailable` / `EnablePSelection` / `DisablePSelection`.
  `SetPSelectionProbe` does not exist on this tree (SP-12 removed it); nothing in this row flips
  the gate by hand except the documented precondition reset below.
- `internal/daemon/scheduler_runtime.go` — `NewSchedulerRuntime` calls `EnablePSelection` once it
  has a non-nil store, graph and segment log; `Close` (reached via `CloseSchedulerRuntime`)
  persists and calls `DisablePSelection`. `internal/cli/scheduler_wiring.go` is the shipped
  composition that constructs and closes it.
- `internal/analyzer/selector.go` — `NewSelector` runs the §13-invariant-4 `Pos < p` refusal
  (`core.ErrBudget`) FIRST, then the closing-note-3 gate (`core.ErrNotImplemented`). Plan §3a.5
  records that SP-15 did not retire either guard; `Propose` (`greedy.go`) routes through the same
  constructor, and `internal/daemon/rehydrate_selection.go` is the consumer that calls `Propose`.

The "local capability" the guard now tests is therefore exactly the lifetime of a daemon-owned
scheduler runtime over a real store and DAG. No host-native p-selection is asked for anywhere.

## Test names and what each asserts

`TestV5_SelectorGatedByRealScheduler` (top level, sequential subtests sharing one rig):

- Precondition: `scheduler.DisablePSelection()` at entry and deferred. The gate is process-global
  and `TestV4_SchedulerFiresBeforeTheSimulatedStockThreshold` constructs a runtime it never
  closes, so a whole-package run arrives here with the gate open. The reset uses the gate's own
  documented test contract ("tests MUST defer it"); every transition the row then asserts is
  caused by a shipped constructor or a shipped Close, never by the test.
- `refuses_before_any_runtime` — `PSelectionAvailable()` is false; `NewSelector(0, <8 real DAG
  blocks>, …)` returns `core.ErrNotImplemented` and a nil `Selector`; `Propose` over the same
  blocks returns `core.ErrNotImplemented` through the same constructor.
- Between subtests: `NewSchedulerRuntime` over the rig's store/DAG opens the gate;
  `rt.Evaluate` resolves a real window ladder (`HardCeilingTokens > 0`). The pure
  `scheduler.Evaluate` is run with the window full over a candidate ladder whose `Pos`, `Turn`
  and `Coupling` come from the real DAG's tool-result nodes (the earlier half of the eight
  positions; `ReclaimableTokens` is the tool-result mass at or after each position, so it is
  non-increasing). It decides to compact and its `Decision.P.Pos` is one of the offered real
  positions. Observed: `p=72` from 4 candidates, 5 real blocks after p, budget 80 of 100 tokens.
  `NewCheapScorer(store).Score` over the blocks after p returns an entry per block with at least
  one positive score (real content roots read back from the real store against a continuation
  naming the seeded paths/symbols).
- `real_runtime_opens_the_gate_and_the_selector_selects` — `NewSelector(p, blocks, …, delta)`
  constructs; `sel.P() == Decision.P.Pos`; `Select(budget)` keeps something, drops something (the
  budget is one block short of the whole mass, so it binds), `Tokens ≤ budget`, `Tokens` equals
  the true sum over `Keep`, every kept id is a real DAG node with `Pos ≥ p`, and `Keep ∪ Dropped`
  is exactly the candidate set. `Propose` over the same blocks/budget constructs, no overflow,
  `Tokens ≤ budget`. Observed: kept 4, dropped 1, 80 tokens, 9 iters.
- `closing_the_runtime_closes_the_gate` — **negative control**, below.
- `a_new_runtime_recovers_the_gate` — a second `NewSchedulerRuntime` over the same store re-opens
  the gate (the daemon outlives sessions; §6's "authorized recovery"); `NewSelector` constructs
  again and `Select` over the same real inputs returns the same `Keep` and `Tokens` as before the
  close. The second runtime is closed in cleanup.
- `p.AssertAppendOnly(t)` closes the row.

## Negative control and how it was proven

A real switch on the shipped shutdown path: `daemon.CloseSchedulerRuntime(rt)` on the runtime
that opened the gate. After it, `PSelectionAvailable()` is false and the SAME arguments — same
p, same real blocks, same real Δ-scores, same λ — that constructed a `Selector` in the previous
subtest are refused with `core.ErrNotImplemented` and a nil `Selector`; `Propose` is refused the
same way. The subtest also pins guard order: with the gate closed, appending one block at
`p-1` yields `core.ErrBudget`, so the invariant-4 refusal still runs ahead of the ship-order gate.

No source edit was needed to prove non-vacuity: the control is the production Close, not a test
hook, and the test's own run shows both directions of the transition (open by constructor,
closed by Close, open again by a second constructor) on one process.

## Old → new assertion map (historical §4.6 text vs. this test)

| Historical expectation | Status | Note |
|---|---|---|
| Two sub-runs, no `SetPSelectionProbe` | kept | `SetPSelectionProbe` does not exist; the gate is moved only by shipped code plus the documented `DisablePSelection` precondition reset |
| (a) no `scheduler.Runtime` constructed ⇒ `NewSelector` returns `ErrPSelectionUnavailable`, no `Selection` | corrected | The sentinel on this tree is `core.ErrNotImplemented` wrapped with "requires p-selection (closing note 3)"; asserted with `errors.Is`. Also asserted for `Propose`, the surface the daemon actually calls |
| (b) real `NewSchedulerRuntime` over real store/DAG ⇒ construction succeeds | kept | Composed exactly as `internal/cli.wireScheduler` and `v4_x02` compose it |
| `Select` returns keep-set with `Tokens ≤ budget` | kept, strengthened | Budget is one block short of the mass, so `Keep` and `Dropped` are both non-empty and `Tokens` is checked as the true sum over `Keep` |
| every kept block has `Pos ≥ p` | kept | Checked against the real DAG node's `Pos`, not the block struct handed in |
| `p` equal to the `Decision.P.Pos` the real scheduler chose from real candidates | corrected | `p` is the pure `scheduler.Evaluate`'s `Decision.P.Pos` over candidates whose positions/turns/coupling come from the real DAG. The runtime's own BOCD ∩ round-boundary assembly is not driven (it needs the in-package tap's `NoteAPIRound`, unreachable from `test/e2e`); `rt.Evaluate` is still called to prove the real ladder resolves. Same construction as `v4_x02` |
| Closing `CloseSchedulerRuntime` makes (a) true again | kept | This is the negative control |
| "ship-order boundary asserted from both sides with two real implementations" | kept | Plus a third arm: a second constructor re-opens the gate and selection is deterministic across the close/reopen |

Nothing from the historical text was retired as unsafe; the criterion's own retirement (§6:
"retire selector guards that require unsupported native p-selection") is satisfied by the fact
that no assertion here depends on a host-native compaction point — the guard tested is the local
runtime's lifetime, the budget is compatible with the selector's own contract, and the recovery
path is the daemon's authorized rebind.

## Unverified remainder

- The operator switch `runtime.selection.submodularEnabled` (default false) and the daemon's
  `rehydrateService.selectionFor` path — which returns nil when the switch is off or when the
  gate refuses, so a session proceeds on the pre-SP-15 ranking — are NOT driven here. That is a
  rehydration-consumer seam and belongs to §4.7. `test/guards` `TestGuard_SubmodularDefaultsOff`
  continues to pin the default; this row records the feature as shipped-disabled at the daemon
  and does not claim it as passed there.
- The runtime's own candidate assembly (`assembleCandidatesLocked` over BOCD changepoints) is not
  exercised from this package; see the map row above.
- The spawned binary's daemon holds its own gate in its own process; that copy is not observed.

## Run command and result

```
cd <worktree> && go test ./test/e2e -list 'TestV5_SelectorGatedByRealScheduler'
  → TestV5_SelectorGatedByRealScheduler
cd <worktree> && go test ./test/e2e -run '^TestV5_SelectorGatedByRealScheduler$' -count=1 -v
  → PASS (4.21 s), and again → PASS (4.00 s); all four subtests PASS both times
gofmt -l ./test ./internal → clean; go vet ./test/e2e ./test/integration → clean
go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns → PASS ×5
```

Windows 11, go1.26.6, 2026-09-08. No timing gate is asserted by this row.

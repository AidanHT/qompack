# SP-15 role E — evaluation — report

**Status: DONE.** Both missions are implemented, measured and green on `wip/sp15-e-eval`
(worktree `qompack-sp15-e`), on top of `f84cdf4`.

**The M6-G15-B ablation came out against Sequitur, and the disposition is recorded as
"Sequitur not justified by this evidence".** That is the expected, legitimate outcome the brief
names, not a failure of the measurement. §5 has the numbers; no fixture was adjusted afterwards.

Scope worked to: [contract.md](contract.md) §3 (objective and feasibility), §3a amendments A1–A7,
§5 (warning bounds), §7 (switches), §8 (fixture layout), plus the
[SP-15 test-plan gate table](../../V5-SP-15-analyzer-selection-and-grammar.md#test-plan-tdd) rows
M5-G15-A/C and M6-G15-A/B and the
[R2 focused-validation](../../V5-SP-15-analyzer-selection-and-grammar.md#focused-validation-and-bounded-parallel-runs)
scheduling rule.

---

## 1. Files

| File | State | What |
|---|---|---|
| `test/replay/policy_selection.go` | new | the shared measurement engine plus `phase5` and `phase6`, the two registered phase-exit checks |
| `test/replay/phase5_test.go` | new | M5-G15-A: held-out trials, the corpus arm, the exact-ratio recording, the two guards |
| `test/replay/phase6_test.go` | new | M6-G15-A and M6-G15-B: held-out report, bounded delivery, self-suppression, the ablation |
| `test/replay/testdata/phase5/**` | new, 4 files | held-out selection trials |
| `test/replay/testdata/phase6/**` | new, 13 files | held-out warning streams |
| `test/replay/phases.go` | **appended** | two entries, `5: phase5` and `6: phase6`, plus the comment saying why they live in `policy_selection.go` |
| `plans/sdd/V5-SP-15/report-E.md` | new | this file |

`phases.go` is the only pre-existing file touched, and only at its documented extension point
("Later subplans APPEND an entry here"). Nothing else in the tree changed. `l3policy` was not
touched, so `TestPolicy_DoesNotImportDaemon` is unaffected.

### Two structural decisions, and why

**`phase5`/`phase6` live in `policy_selection.go`, not in `phases.go`.** `devtool replay` shells
out to `go run ./test/replay`, and `go run` does not compile test files, so a check that lived in a
`_test.go` file would be invisible to the gate — the same reason `phase3` lives beside
`policy_rehydrate.go`. Putting the functions there also kept the `phases.go` edit to the two map
entries the brief authorised.

**Both checks are self-contained: they read nothing out of the driver `Context`.** This is not a
convenience. The pre-existing `TestRunPhaseChecks_RunsEveryMergedPhase` in `gate_test.go` runs
`runPhaseChecks(c, 6)` against a minimal fixture and requires exactly two errors (phases 0 and 3); a
phase 5 that needed a policy in `--policies` would have made that three and would have failed on
every driver invocation that did not happen to include one. It still requires two — verified — and
the deeper reason is the same: a gate that fails for a reason unrelated to what it measures is a
gate that gets turned off.

---

## 2. Run map (R2)

Snapshot for every row: worktree `C:/Users/Quant/Documents/Programming/Projects/qompack-sp15-e`,
branch `wip/sp15-e-eval`, base `f84cdf4` (`feat(checkpoint): integrate selection and scoped
warnings`), `go1.26.6 windows/amd64`. Owner: role E for every row.

Fixture identity — `git hash-object`, so a fixture edited after the fact is detectable:

| Fixture | Blob |
|---|---|
| `phase5/dependency-closure-and-archive.json` | `bee2422f7e911825eab607cf261b680df013b85a` |
| `phase5/g63-current-authoritative.json` | `192153f338609b9d3652dee6df6c1ff4e94216d5` |
| `phase5/g63-stale-and-uncertain.json` | `c39b4760d576173b530a3d7ad480324d97d489b9` |
| `phase5/tie-determinism.json` | `96fe90304c670fa49ba17611309df0bba09dd171` |
| `phase6/dep-change-retest.json` | `926d7dd1432384fbc1b28b10974030fe5a1b2728` |
| `phase6/edit-nothing-changes.json` | `dd0ffdf439c0cf4d777eebacc8e872f7625cdb79` |
| `phase6/edit-test-edit-progress.json` | `532812e683e4a0780e2720c0b0fbd9ccb385c893` |
| `phase6/long-period-cycle-five-targets.json` | `ddba4991203036c36a5634372a406791b620f890` |
| `phase6/qompack-self-traffic.json` | `c3cfe52eb04785e0d7321da377e93de2da4b9cff` |
| `phase6/read-scan-many-files.json` | `bfab76a8eb08cd58fbbb34c566cbe652e077f091` |
| `phase6/refactor-rename-sweep.json` | `1cecbdae74dbbc8147cc645e68b27480b8e7c5ed` |
| `phase6/same-command-same-failure.json` | `4e781d71c1d27ed73f9443edea89bad715ff0f7c` |
| `phase6/stuck-after-progress-later-window.json` | `de016e2f079741011f42517f317d872ac3c673bb` |
| `phase6/stuck-with-partial-coverage.json` | `ea667162aab61b695bfa1edf8cd5270545a074f6` |
| `phase6/test-green-then-next.json` | `3a34a498c28b6753fabfdd9a3146c056a20f43f4` |
| `phase6/two-interleaved-stuck-states.json` | `bfedcb4782465d9dd18433376f62c85795c25d02` |
| `phase6/unknown-coverage-exploration.json` | `87976d869cb9acc23534ef8b468b688bd9a25b50` |

Corpus arm: `testdata/sessions/synthetic`, manifest blob `8249734db6e4d703ad429448e008c84aa5037ffc`,
24 sessions — the same corpus `phase4_test.go` replays.

| Gate row | Command (`go test ./test/replay/ -count=1 -v -run …`) | Expected | Actual | Result |
|---|---|---|---|---|
| M5-G15-A trials | `TestPhase5_HeldOutTrialOutcomes` | < 5 s | 0.08 s | PASS, 12 rows |
| M5-G15-A G6.3 | `TestPhase5_G63DispositionsAreTheOnesTheContractNames` | < 5 s | 0.00 s, 3 subtests | PASS |
| M5-G15-A ratios | `TestPhase5_HeuristicAgainstExactRatiosRecorded` | < 5 s | 0.03 s, 7 instances | PASS (recorded, no bound) |
| M5-G15-A corpus | `TestPhase5_SelectionAgainstTheCompleteRecordHeuristicOnTheCorpus` | < 60 s | 0.42 s, 39 points / 117 rows | PASS |
| M5-G15-A ship-order guard | `TestPhase5_ShipOrderGateStillRefusesProposal` | < 5 s | 0.00 s | PASS |
| M5-G15-A switch guard | `TestPhase5_DefaultsStayOff` | < 5 s | 0.00 s | PASS |
| M6-G15-A report | `TestPhase6_HeldOutFalseAlarmAndUsefulnessReport` | < 5 s | 0.06 s, 13 streams | PASS |
| M6-G15-A delivery | `TestPhase6_BoundedDeliveryUnderLoad` | < 5 s | 0.03 s | PASS |
| M6-G15-A self-loop | `TestPhase6_SelfSuppressionIsAClosedLoop` | < 5 s | 0.03 s | PASS, 0 amplification |
| M6-G15-A wording (A7) | `TestPhase6_UncertainWarningsSayThatTheyAreUncertain` | < 5 s | 0.03 s | PASS |
| M6-G15-A progress bound | `TestPhase6_ProgressSuppressionIsAWindowBoundAndNotAGag` | < 5 s | 0.03 s | PASS |
| **M6-G15-B ablation** | `TestPhase6_AblationSequiturAgainstStateSignatures` | < 10 s | 0.04 s | PASS — **verdict: Sequitur not justified** |
| Determinism | `TestPhase6_ArmsAreReproducible` | < 5 s | 0.05 s | PASS |
| Registration (phase 5) | `TestPhase5_PhaseCheckIsSelfContained` | < 10 s | 0.00 s | PASS |
| Registration (phase 6) | `TestPhase6_PhaseCheckIsSelfContained` | < 10 s | 0.04 s | PASS |

| Whole-suite row | Command | Expected | Actual | Result |
|---|---|---|---|---|
| Whole package | `go test ./test/replay/ -count=1` | < 5 min | 36.7 s | PASS |
| Driver (the real gate path) | `go run ./test/replay --phase 6` | < 10 min | 1.6 s | phase 5 PASS, phase 6 PASS; **two PRE-EXISTING failures**, §7.1 |
| Format | `gofmt -l test/replay` | < 1 min | instant, no output | PASS |
| Build | `go build ./...` | < 2 min | no output | PASS |
| Vet | `go vet ./test/replay/...` | < 2 min | no output | PASS |

`go test -run` prints `ok` when it matches nothing, so every row above was re-run with `-v` and the
cases counted: **18 PASS lines (15 top-level cases + 3 subtests), 0 FAIL** across the
`TestPhase5`/`TestPhase6` pattern.

**Declared sample sizes.** Phase 5: 4 held-out trials × 3 budget regimes = 12 rows; 7 of role D's
exact instances; 24 corpus sessions producing 39 compaction points × 3 regimes = 117 corpus rows.
Phase 6: 13 held-out streams (6 stuck, 7 progress), 148 observations in total, × 3 ablation arms.

---

## 3. M5-G15-A — selection outcomes

### 3.1 The held-out trials

Every row asserts, independently of anything the selector reports about itself: at most one
representation per item, archive-only never chosen, the transitive dependency closure of everything
carried, `Tokens` equal to the true sum over `Chosen` and inside the budget, nothing before `p`,
every carried qualification one the candidate actually offered, and the declared outcome class.
Two `Propose` calls on equal inputs returned equal `Proposal`s including `Iters`, at every row.

```
  trial                            regime             budget selection                            complete-record                                       iters
  dependency-closure-and-archive   generous              500  carry=4 tok= 258 val=1.7100 dup=1    carry=4 tok= 270 val=2.0500 dup=1 closure-violations=0     8
  dependency-closure-and-archive   tiny                  200  carry=3 tok= 188 val=1.0100 dup=1    carry=2 tok= 170 val=1.6000 dup=0 closure-violations=1     9
  dependency-closure-and-archive   mandatory-overflow      0  carry=0 tok=   0 val=0.0000 dup=0    carry=0 tok=   0 val=0.0000 dup=0 closure-violations=0     6
  g63-current-authoritative        generous              500  carry=3 tok= 410 val=2.1000 dup=0    carry=3 tok= 410 val=2.1000 dup=0 closure-violations=0    10
  g63-current-authoritative        tiny                  130  carry=1 tok= 120 val=0.9000 dup=0    carry=1 tok= 120 val=0.9000 dup=0 closure-violations=0    13
  g63-current-authoritative        mandatory-overflow     40  OVERFLOW, archive=[elimination:e1]   carry=0 tok=   0 val=0.0000 dup=0 closure-violations=0     0
  g63-stale-and-uncertain          generous              400  carry=3 tok= 280 val=2.2000 dup=0    carry=3 tok= 280 val=2.2000 dup=0 closure-violations=0     6
  g63-stale-and-uncertain          tiny                   90  carry=1 tok=  80 val=0.6000 dup=0    carry=1 tok=  80 val=0.6000 dup=0 closure-violations=0     7
  g63-stale-and-uncertain          mandatory-overflow      0  empty, NO overflow                   carry=0 tok=   0 val=0.0000 dup=0 closure-violations=0     4
  tie-determinism                  generous              400  carry=3 tok= 300 val=1.5000 dup=0    carry=3 tok= 300 val=1.5000 dup=0 closure-violations=0     5
  tie-determinism                  tiny                  250  carry=2 tok= 200 val=1.0000 dup=0    carry=2 tok= 200 val=1.0000 dup=0 closure-violations=0     5
  tie-determinism                  mandatory-overflow      0  empty, NO overflow                   carry=0 tok=   0 val=0.0000 dup=0 closure-violations=0     3
```

**The three budget regimes.** Generous: everything carried, no overflow. Tiny: a strict subset,
budget respected, mandatory reserve honoured first. Mandatory-overflow: at
`g63-current-authoritative` the binding record cannot be carried at any qualified representation
(cheapest is the 48-token pointer, budget 40), so `Overflow=true`, `Item=elimination:e1`, `Reason`
names it, `Archive=[elimination:e1]`, `Chosen=nil`, `Tokens=0` — and `Iters=0`, because `reserve`
short-circuits before the greedy runs, which is contract §3's "after every optional item has been
dropped" enforced before anything optional is bought.

`tie-determinism/tiny` pins the tie-break exactly: three candidates with identical weight, coverage
and cost, a budget holding two, and the two lowest `Item` ids are the ones carried, on every run.

**Two findings the rows carry that are unfavourable to selection, retained rather than dropped:**

1. **`dependency-closure-and-archive/generous`: the complete-record baseline scores HIGHER on the
   declared objective (2.05 vs 1.71).** Selection carried `toolresult:tu_root` at `pointer`
   (coverage 0.15) because it entered as a dependency of `file:src/x.go`, and `bundleFor` carries a
   dependency at its CHEAPEST representation. At a budget with room to spare that is a real loss —
   the cheapest-dependency rule is a documented heuristic choice (role D says so in `bundleFor`),
   not an optimum, and this is what it costs. Open question Q1.
2. **`dependency-closure-and-archive/tiny`: the baseline again scores higher (1.60 vs 1.01) — and
   its set is INFEASIBLE.** It carried `file:src/x.go` without `toolresult:tu_root`, which
   `closure-violations=1` records. The pre-SP-15 heuristic buys its higher objective value by
   ignoring a constraint the objective does not price. This is the clearest single statement of
   what selection is for, and it is only visible because both arms are scored on one yardstick.

### 3.2 The G6.3 scenarios (all three, named)

| Scenario | Assertion | Result |
|---|---|---|
| current authoritative retained with its evidence | `elimination:e1` carried at `exact_span`, `Qualification.Active()`, `Prov.Root == H("ev-elim-1")` — the ORIGINAL root, not a derivative's | PASS |
| same record under a budget that forces overflow | `Overflow`, `Item`, `Reason` naming it, `Archive=[elimination:e1]`, `Chosen=nil`, `Tokens=0` | PASS |
| stale-or-uncertain keeps its qualification, never an active constraint | at budget 0 **no overflow** although two candidates carry `Mandatory`; at a generous budget both are carried and both report `Active()==false` | PASS |

The recovery *sentence* a user reads is `internal/rehydrate/selection.go`'s
`archiveRecoveryDetail`; this gate asserts the analyzer-side recovery *path* (`Archive` + `Reason`)
and references that string rather than duplicating it.

### 3.3 Heuristic-vs-exact ratios (RECORDED, no bound asserted)

Role D's committed instances, re-proposed here; the optima's fidelity is guarded live by
`internal/analyzer/objective_test.go`, which brute-forces every one of them on every run.

| Instance | heuristic | exact | ratio |
|---|---|---|---|
| dependency-closure | 2.550000 | 2.550000 | **1.000000** |
| mandatory-overflow | 0.000000 | 0.000000 | 1.000000 (both zero) |
| plain | 1.950000 | 2.000000 | **0.975000** |
| qualification-g63 | 1.020000 | 1.020000 | **1.000000** |
| **redundancy-nonmonotone** | 1.550000 | 2.100000 | **0.738095** |
| tiny-budget | 0.300000 | 0.300000 | 1.000000 |
| zero-budget | 0.000000 | 0.000000 | 1.000000 (both zero) |

**No bound is asserted and none is claimed.** 0.738 on the non-monotone instance is the honest
number and is precisely why no (1−1/e) statement exists anywhere in SP-15: the redundancy term is
subtracted, which does not preserve monotonicity, and the dependency and
one-representation-per-item constraints change the feasible family. The only assertion is the
consistency check that the heuristic never *exceeds* the enumerated optimum.

### 3.4 The corpus arm (scale)

24 sessions, 39 compaction points, 117 rows, λ = 0.400 (`config.Defaults()`), candidates built
through role C's real `analyzer.NewCandidates`, `p` = the median block position of the prefix,
every recorded elimination marked mandatory and `QualCurrent`.

| Regime | selection | complete-record | overflows |
|---|---|---|---|
| generous | carried 5534, 8 105 971 tok, value 2476.30, demands 1052 | carried 5672, 8 195 797 tok, value 2471.68, demands 1052 | 0 |
| tiny | carried 649, 409 144 tok, value 481.99, demands 609 | carried 643, 408 550 tok, value 468.20, demands 604 | 0 |
| mandatory-overflow | nothing carried | nothing carried | **39 / 39** |

(1612 post-compaction demands in total across the corpus.)

Reading: at a generous budget selection carries **138 fewer items for 89 826 fewer tokens at a
marginally higher objective value and identical demand satisfaction** — the 138 are items whose
whole coverage contribution was cancelled by λ × (duplicate evidence root), i.e. content the caller
would have paid for twice and read once. At a tiny budget it carries 6 more items for 594 more
tokens, +13.79 objective and +5 demands. Both differences are small, and both are recorded rather
than gated: the demand column is `eval.Demands` over a SYNTHETIC corpus, and no synthetic score may
flip a default.

**What the corpus arm does not do:** it does not exercise dependency closure. It supplies no
`dag.Graph`, so `NewCandidates` reports `core.ErrDegraded` alongside a valid candidate set — role
C's C-2 rule — and `Requires` is empty everywhere. The test asserts
`require.ErrorIs(..., core.ErrDegraded)` at every point, so the degradation is *reported* rather
than swallowed. Closure is exercised exactly and by hand in the `dependency-closure-and-archive`
trial. **This is recorded as a scope limit of the corpus arm, not as coverage.**

### 3.5 M5-G15-C

Not owned by role E and not re-measured here. The consumer integration landed in `8d338fb`
(`internal/rehydrate/selection.go`, `SelectionOutcome`, the `archive_only` drop kind) and `f84cdf4`
(the daemon's negknow→candidate translation). The phase-5 evidence above is the analyzer side of
that seam; the delivered-serialization half is Main's row. **Disposition: not evaluated by E, owner
Main.**

---

## 4. M6-G15-A — held-out false-alarm and usefulness report

13 streams, 148 observations, at the shipped bounds read back off the detector: `minRepeats=3`,
`window=20`, `maxPerSession=3`, `expiry=5`.

```
  stream                                 label     verdict      delivered uncertain first-turn
  dep-change-retest                      progress  quiet            0         0        -
  edit-nothing-changes                   stuck     detected         2         0         4
  edit-test-edit-progress                progress  quiet            0         0        -
  long-period-cycle-five-targets         stuck     MISSED           0         0        -
  qompack-self-traffic                   progress  quiet            0         0        -
  read-scan-many-files                   progress  quiet            0         0        -
  refactor-rename-sweep                  progress  quiet            0         0        -
  same-command-same-failure              stuck     detected         1         0         2
  stuck-after-progress-later-window      stuck     detected         1         0        22
  stuck-with-partial-coverage            stuck     detected         1         1         2
  test-green-then-next                   progress  quiet            0         0        -
  two-interleaved-stuck-states           stuck     detected         2         0         4
  unknown-coverage-exploration           progress  quiet            0         0        -
```

**detected = 5/6 · MISSED = 1 · FALSE ALARMS = 0/7 · delivered = 7.**

The one miss is `long-period-cycle-five-targets` and it is deliberate: a five-file edit-and-test
cycle walked twice gives each individual state two occurrences, under `minRepeats=3`. It was
authored so the ablation would have something to find, and it is reported as a miss rather than
tuned away.

**Zero false alarms on seven ordinary streams is a statement about seven streams, not a rate.**
Thirteen hand-authored streams are a sample size, not a population; the streams were written after
roles A–D landed and no implementation file was changed to make any of them come out a particular
way, but they are synthetic and no field false-alarm rate is estimated here.

**Separated accounting (contract §5.6).** Raw repeated-action frequency `repeated-states = 57`
against `useful = 7`, `false-alarms = 0`. The two do not track each other, which is the whole point
of the requirement: a component reported on by its own activity level always looks successful.

**Bounded delivery.** `maxPerSession = 3`, observed maximum 2. `dedup-collapsed = 17` (later
repeats of an already-delivered warning), `cap-suppressed = 0`, `progress-suppressed = 58`,
`self-suppressed = 216`, `expired-at-delivery = 7` (every produced warning is refused one turn past
its `ExpiresAt`, and accepted at it). `two-interleaved-stuck-states` delivers exactly 2 warnings —
one per distinct signature — from 12 observations, 8 of which cross the threshold.

**Self-suppression, closed loop.** All 7 delivered warnings were rendered through the frozen
`FormatWarning` and fed back as the goal, target, action AND failure of 30 further turns each —
210 observations, far past every threshold the detector has. **0 further warnings.** Separately, 6
of `qompack-self-traffic`'s 12 observations are Qompack's own and all 6 are excluded *before* the
frequency counter (`SelfSuppressed == 6`), so a burst of retrieval results cannot surface in the
report as session repetition.

**A7 wording.** 1 uncertain warning, 6 confident. The uncertain one renders with
`observation coverage is incomplete, so this may not be a loop`; the confident ones with
`consider a different approach`, and never with the uncertain string. Both are pinned as literals
in `phase6_test.go` rather than read back from `grammar`, so a silent change to either fails.

**Progress bound, both halves.** `edit-test-edit-progress` and `refactor-rename-sweep` produce
nothing; `stuck-after-progress-later-window` still warns, from the later window (turn ≥ 20), so the
latch is a window bound and not a permanent gag earned by one productive minute.

---

## 5. M6-G15-B — THE ABLATION

Three arms, identical input, identical delivery bounds (same repeat threshold — `minRepeats=3` for
the signature arm, `Thrash(2)` for the grammar arms, which reports rules used *more than* 2 — same
`maxPerSession=3`, same self-origination exclusion). Measured in one sequential process.

| Arm | detected | false alarms | delivered | alloc | mallocs | wall | rules | compressed |
|---|---|---|---|---|---|---|---|---|
| `state-signature` | **5/6** | **0/7** | 7 | 65 984 B | 1 259 | ~2.4 ms | 0 | 0 |
| `sequitur` | 6/6 | **6/7** | 22 | 159 200 B | 2 589 | ~2.3 ms | 32 | 46 |
| `sequitur+progress` | 6/6 | **2/7** | 15 | 122 176 B | 1 991 | ~2.1 ms | 32 | 46 |

Per stream:

```
  stream                                 label     state-signature  sequitur      sequitur+progress
  dep-change-retest                      progress  quiet            FALSE-ALARM   quiet
  edit-nothing-changes                   stuck     detected         detected      detected
  edit-test-edit-progress                progress  quiet            FALSE-ALARM   quiet
  long-period-cycle-five-targets         stuck     MISSED           detected      detected
  qompack-self-traffic                   progress  quiet            quiet         quiet
  read-scan-many-files                   progress  quiet            FALSE-ALARM   FALSE-ALARM
  refactor-rename-sweep                  progress  quiet            FALSE-ALARM   quiet
  same-command-same-failure              stuck     detected         detected      detected
  stuck-after-progress-later-window      stuck     detected         detected      detected
  stuck-with-partial-coverage            stuck     detected         detected      detected
  test-green-then-next                   progress  quiet            FALSE-ALARM   quiet
  two-interleaved-stuck-states           stuck     detected         detected      detected
  unknown-coverage-exploration           progress  quiet            FALSE-ALARM   FALSE-ALARM
```

### The verdict, plainly

**Sequitur is not justified by this evidence.** It stays OPTIONAL AND DISABLED per
`M6-U15-sequitur-value`.

Sequitur buys exactly one thing on this corpus: `long-period-cycle-five-targets`, a loop whose
period is longer than the repeat threshold can see. That is a real capability the state signature
does not have, and it is worth writing down. It pays for it with **six false alarms out of seven
ordinary streams**, and with **two out of seven even after being handed the same progress signal
the signature arm has** — and it costs roughly **2.4× the allocations** (1.9× with progress), plus
32 induced rules of state that exist whether or not anything fires.

The two false alarms that survive the progress signal are the informative ones, and neither is
contrived:

- `read-scan-many-files` — reading ten different files changes nothing, so progress is honestly
  `none`; only the fact that every *target* differs separates it from thrashing, and a grammar over
  action symbols cannot see a target.
- `unknown-coverage-exploration` — a grep sweep in a session whose file observation coverage is
  missing. `unknown` is not `observed`, so the progress latch does not fire, and the grammar reports
  ten distinct searches as a loop.

Both are cases where the *state signature* — not the repeat count, not the grammar — is doing the
work, which is what the ablation was asked to establish.

**The third arm was included specifically to remove the obvious objection** that Sequitur was
handicapped by being denied the progress signal. It was not: arm three has that signal, and its
latch is deliberately window-wide (more aggressive than the signature arm's per-signature latch,
which helps it on false alarms and can only cost it detections) — and it still loses on the axis
that matters.

**Nothing in the gate asserts any of this.** `p6CheckBounds` gates on the six contract-§5 bounds
and on nothing else; the ablation numbers and the verdict are printed and stored in
`phase6-ablation.json`. A gate cannot require the presence of a thing the project has not adopted,
and — the plan's own rule — no synthetic score may flip a default. **Adopting or dropping Sequitur
is a Main decision; this is evidence toward it.**

**Codec compatibility**, which M6-G15-B also names, is role B's and is already covered by
`TestCodec_CompatibilityVectors` and `TestDecodeSnapshot_RejectsBadFrames` over
`internal/grammar/testdata/codec/*.golden`. Not re-measured here. **Disposition: evidence exists,
owner B.**

---

## 6. Gate dispositions

| Gate | Disposition |
|---|---|
| **M5-G15-A** | **EVIDENCE.** 12 held-out rows + 117 corpus rows: one representation per item, closure, budget, `Pos ≥ p`, determinism incl. `Iters`, three budget regimes, three G6.3 scenarios, exact ratios recorded (0.738 lowest, no bound claimed). The corpus arm's closure is `ErrDegraded` and reported as such. |
| M5-G15-B | **NOT E's.** Role C's counterexample fixtures (`internal/analyzer/testdata/diagnostics/**`) and its qualified-claim wording. Not re-measured here. |
| **M5-G15-C** | **NOT E's, owner Main.** Landed in `8d338fb` + `f84cdf4`; the consumer trace is Main's row. E measured the analyzer side of the seam only. |
| **M6-G15-A** | **EVIDENCE.** 13 held-out streams: 5/6 detected, 1 miss reported, 0/7 false alarms, 7 delivered; dedup 17, cap honoured (max 2 of 3), expiry enforced at delivery, self-suppression closed loop **0 amplification**, usefulness separated from repeated-state frequency (7 vs 57), A7 wording pinned. |
| **M6-G15-B** | **EXPLICITLY-ACCEPTED OPTIONAL-DISABLED.** Ablation run, three arms, numbers in §5. Verdict: *Sequitur not justified by this evidence*. Remains optional and disabled per `M6-U15-sequitur-value`. |

---

## 7. Failed and inconclusive trials, retained

Nothing in this section was dropped because it was inconvenient, and **no fixture was edited after
a failing run** — every committed trial and stream passed on its first execution.

### 7.1 The driver at `--phase 6` fails on two PRE-EXISTING conditions

`go run ./test/replay --phase 6` runs phases 0, 3, 5 and 6. Phases 5 and 6 pass. Two failures
appear, and **both also appear at `--phase 3`, i.e. on the tree before this work**:

```
FAIL phase 3 (A2 divergence): qompack-rehydrate first diverged at turn 2, no later than stock's 3;
     a rehydration that does not delay divergence has not preserved anything
FAIL corpus stale: re-collect sessions under the current policy (§11.4). The corpus was regenerated
     after phase 0 and this run asserts phase 6, more than 2 phases later. See
     docs/adr/0003-replay-overfit-recollection.md for the protocol
```

Neither is E's and neither is touched by E's files (verified by running the same driver at
`--phase 3`, where both reproduce). The corpus-staleness one is worth Main's attention anyway:
`checkCorpusFreshness` compares `--phase` against `CorpusManifest.RegeneratedAfterPhase` (0) plus 2,
so **registering phases 5 and 6 makes the ADR 0003 re-collection obligation louder** — the gate now
names phase 6 in the message. Open question Q3.

### 7.2 Retained negative results

| # | Finding | Kept because |
|---|---|---|
| 1 | The complete-record baseline out-scores selection on the declared objective at `dependency-closure-and-archive/generous` (2.05 vs 1.71) | it is a real cost of the cheapest-dependency rule, not noise |
| 2 | The state signature MISSES `long-period-cycle-five-targets` | it is the one thing Sequitur can do that the signature cannot |
| 3 | `sequitur+progress` still false-alarms on 2 of 7 progress streams | it is the reason the answer is not "just give Sequitur the progress signal" |
| 4 | Corpus arm cannot exercise dependency closure (`ErrDegraded`, no graph) | it is a scope limit, and presenting an empty closure as an established one would be the stronger claim on no evidence |
| 5 | Allocation figures move by ~6× under co-load (the signature arm read 65 984 B sequentially and 439 032 B with seven parallel cases beside it) | it is why the ablation test is deliberately NOT `t.Parallel()` and why no cost figure is asserted |

### 7.3 Inconclusive by construction

- **No field false-alarm rate.** 13 synthetic streams cannot produce one, and none is claimed.
- **No task-completion measurement.** The demand column is `eval.Demands` over a synthetic corpus;
  it is the only signal here neither arm can see while it decides, and it is still not quality.
- **No approximation bound.** See §3.3.
- **Representation costs are uncalibrated** (`M5-U15-representation-overhead`). Every token figure
  in §3 is an estimate against an uncalibrated assembled model; the corpus arm's 8.1 M tokens is a
  number in the estimator's units, not the provider's.

---

## 8. Decisions I made (each needs a Main ack or an objection)

**E-1. Phases 5 and 6 are registered in `phaseChecks` and are self-contained.** They read the
committed fixtures, not the driver's report, so `TestRunPhaseChecks_RunsEveryMergedPhase` still sees
exactly two errors at `--phase 6`. The alternative — a registered check that needs a policy in
`--policies` — fails on every invocation that omits it, which is how gates get disabled.

**E-2. `phase5` opens `scheduler.EnablePSelection()` and closes it with `defer`.** The selector
cannot be measured with the ship-order gate down, and the gate is a process-wide atomic. The
shipped config default is untouched: `TestPhase5_DefaultsStayOff` asserts `SubmodularEnabled`,
`LoopWarningsEnabled` and the derived `Selection.Submodular.Enabled` are all false, and
`TestGuard_SubmodularDefaultsOff` still pins the config side. `TestPhase5_PhaseCheckIsSelfContained`
additionally asserts the check closed the gate it opened.

**E-3. The comparison holds the WEIGHTS FIXED across both arms.** The pre-SP-15 heuristic ranked by
slice score; the declared weight stands in for it and both arms use it. Give the baseline a
different ranking and any difference between the arms could be the ranking rather than the
mechanism. The corpus arm's per-kind prior is stated in one place (`p5KindWeight`) so a reader can
disagree with it in one place; it is a prior, not a measurement, and the doc comment says so.

**E-4. The corpus arm assumes every recorded elimination is still ACTIVE.** There is no negknow
ledger in a replay, so nothing there can establish applicability. Marking them all `QualCurrent` is
the most demanding available assumption and is what makes the mandatory reserve and the overflow
path run at corpus scale (39/39 points overflow at the probe budget). A real ledger would qualify
some of them stale, which only makes the selector's job easier.

**E-5. The Sequitur arms read the SYMBOL STREAM DECLARED BY THE FIXTURE**, not one this file
derives. A symbol stream synthesized here would make the ablation a measurement of my derivation
rule rather than of Sequitur. Both sides read the same committed bytes.

**E-6. A third ablation arm (`sequitur+progress`) was added beyond the brief.** M6-G15-B asks for
two; the obvious objection to a two-arm result is that Sequitur was denied a signal it could have
had. The third arm removes it. Its progress latch is window-wide rather than per-signature, which
is stated in the code and in §5 because it is not a neutral choice.

**E-7. Cost figures are recorded, never asserted.** Wall time on a loaded machine is not a property
of the code (this tree already has two wall-clock tests that fail only under co-load). Allocation
figures are reported alongside the load sensitivity that makes them soft. What IS asserted is
determinism of the DECISIONS: every arm is run twice and its decision vector compared, and the
rendered warning text is compared too, because that is what reaches a prompt.

**E-8. `p5CheckOutcomes` and `p6CheckBounds` are error-returning functions shared by the driver
check and the tests.** A gate whose CI form and test form have drifted apart proves whichever of
the two is weaker.

**E-9. Commit attribution.** The repository's `commit-msg` hook (`devtool check-commit-msg`) rejects
`Co-Authored-By`, `Claude-Session` and similar trailers. Session instructions asked for them; I
followed the repository rule and omitted them rather than bypassing the hook, exactly as role C did
(C-5).

---

## 9. Open questions for Main

**Q1 — the cheapest-dependency rule costs objective value at generous budgets.** `bundleFor`
carries a required item at its cheapest representation because it is carried for the dependent's
sake. At `dependency-closure-and-archive/generous` that turns a 2.05 into a 1.71 with 242 tokens of
budget left unspent. A second pass that upgraded already-carried dependencies while budget remains
would close it. Role D's own doc comment calls the rule "a documented heuristic choice, not an
optimum", so this is a measurement of a known choice rather than a defect — but it is now measured,
and the decision to leave it is Main's.

**Q2 — should the state signature grow a long-period detector?** The single miss is a loop whose
period exceeds `minRepeats`. Sequitur finds it and costs six false alarms; a cheaper option is a
signature-level cycle check (the same signature recurring at a fixed stride) that would not need a
grammar at all. Out of scope for E; recorded because the ablation is what surfaced it.

**Q3 — `--phase 6` now names phase 6 in the ADR 0003 staleness failure.** `checkCorpusFreshness`
fires whenever `--phase > RegeneratedAfterPhase + 2`, and the manifest says 0. It already failed at
`--phase 3`; registering 5 and 6 does not create the obligation but does make the gate assert two
phases further past it. Main should decide whether the wave-5 candidate re-collects the corpus or
runs the gate at a lower `--phase`.

**Q4 — the two pre-existing driver failures (§7.1) block a clean `--phase 6` run.** Phase 3's A2
divergence failure in particular is unrelated to SP-15 and predates this branch. Flagged so it is
not mistaken for a phase-5/6 regression.

**Q5 — `test/replay/testdata/**` is new ground for this package.** `test/replay` had no `testdata`
directory before; the corpus lives at the repository root and the baselines under
`testdata/baseline/`. Contract §8 assigns `test/replay/testdata/phase5|phase6` to E and that is
where they are, but if Main would rather these sit beside the other replay fixtures it is a
directory move plus two constants.

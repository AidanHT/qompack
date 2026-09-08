# SP-15 — coordinator report

**Branch:** `feat/sp15-analyzer-selection-and-grammar`, cut from reconciled `develop@7c735ac`.
**Executed:** 2026-09-08. **Coordinator:** Main. **Roles:** A–E, one worktree each, exclusive
written file sets, no two roles owning one file.

## 1. What shipped

`internal/analyzer` and `internal/grammar` were SP-01 stubs at the base commit. They are now real:

| Surface | Before | After |
|---|---|---|
| `grammar.Sequitur` | every operation a stub | real incremental induction, both classical invariants after every Append |
| `grammar` codec | `ErrNotImplemented` | version-tagged `Snapshot` codec with a compatibility reader |
| `grammar` warnings | `FormatWarning` only | `StateSignature`/`Progress`/`StateWarning` + a bounded, warning-only detector |
| `analyzer.DeltaScorer` | stub | cheap-tier behaviour proxy over the observed continuation |
| `analyzer.DetectRedundancy` | stub | exact supersession + MinHash near-duplicate **candidates** |
| `analyzer.Selector.Select` | stub | lazy-greedy walk with observable pruning |
| representation selection | did not exist | `Candidate`/`Representation`/`Proposal`/`Propose` |
| SP-11 consumer | no selection | daemon runs the selector; item 3 honours it; archive/overflow reach item 7 |

Both capabilities ship **disabled** (`runtime.selection.submodularEnabled`,
`runtime.selection.loopWarningsEnabled`, both default false, independently switchable).

## 2. Conformance activation — the headline evidence

Four inherited `/behaviour` blocks were Rule W-1 skips at the base commit and now RUN and PASS. No
suite was edited to make a block pass; the one suite change is a correctness fix, §5.

| Suite block | Base | Now |
|---|---|---|
| `analyzertest` `NewCheapScorer/behaviour` | SKIP | PASS (4 cases) |
| `analyzertest` `DetectRedundancy/behaviour` | SKIP | PASS (3 cases) |
| `analyzertest` `NewSelector/behaviour` | SKIP | PASS (6 cases) |
| `grammartest` `invariants_hold_after_every_append` | SKIP | PASS (23 appends) |

`analyzertest`'s never-skipped `/constructor` block still passes, and `NewSelector`'s two guards are
byte-identical to the pre-SP-15 source, in the original order.

## 3. Gate dispositions

| Gate | Disposition | Evidence |
|---|---|---|
| M5-G15-A | **EVIDENCE** | 7 committed instances with brute-forced optima (`internal/analyzer/testdata/objective/`), 12 held-out rows + 117 corpus rows (phase 5). Zero/tiny/overflow all exercised |
| M5-G15-B | **EVIDENCE** | 3 counterexample fixtures with provenance (`internal/analyzer/testdata/diagnostics/`): similarity-is-not-equivalence, an exact supersession chain, a shared-file edge that implies nothing about relevance |
| M5-G15-C | **EVIDENCE** | `internal/daemon/rehydrate_selection_test.go` (13 cases) + `internal/rehydrate/selection_test.go` (11 cases) — the real composition root, the real item-3 builder, the real drop report |
| M6-G15-A | **EVIDENCE** | 13 held-out streams; 0/7 false alarms; 0 amplification over 210 fed-back observations; dedup/cap/expiry gated |
| M6-G15-B | **EXPLICITLY-ACCEPTED OPTIONAL-DISABLED** | the ablation below |

## 4. The ablation, and why the answer is "no"

M6-G15-B is the gate that was allowed to fail, and it did — which is the useful outcome.

| Arm | detected | false alarms | alloc | rules |
|---|---|---|---|---|
| state-signature | 5/6 | **0/7** | 65,984 B | 0 |
| sequitur | 6/6 | **6/7** | 159,200 B | 32 |
| sequitur + progress | 6/6 | **2/7** | 122,176 B | 32 |

Sequitur buys exactly one detection — a cycle whose period exceeds `minRepeats` — and pays six false
alarms for it, still two after being handed the same progress signal the winning arm uses, at 2.4×
the allocations. The third arm was added specifically to remove the "Sequitur was handicapped"
objection. It still loses, and the fixtures were **not** tuned afterwards.

**Disposition: Sequitur is not justified by this evidence for the warning path, and stays optional
and disabled** per blocker M6-U15-sequitur-value. This confirms the shipped design rather than
changing it: the detector in `statewarn.go` is signature-only and never referenced Sequitur. The
induction itself is still worth having — it is real now rather than stubbed, and `internal/observer`
consumes `Thrash` on a pre-existing path that this evidence does not touch.

A false-alarm rate is the right axis to lose on. A loop warning that fires on ordinary
edit-test-edit progress spends the user's trust in every later warning, and the plan is explicit
that ordinary progress must not be treated as a loop.

## 5. The one conformance-suite change, and why it is not a weakening

`grammartest.checkNoDigramTwice` counted **overlapping** digram occurrences. Sequitur's invariant is
over non-overlapping occurrences, so the check was **unsatisfiable, not strict**, and the fixture
reaches the unsatisfiable state twice.

Verified independently by Main before any edit, by tracing the implementation rather than trusting
the report: `Read Edit Bash` ×3 induces `S → R R R` with `R → Read Edit Bash` used three times, and
the two `(R,R)` pairs overlap at index 1. Acting on that would leave `S → Q R` with `Q → R R`, and
`Q` used once is a rule-utility violation that inlines `Q` straight back. There is no other grammar
for that input; the check rejected the only correct answer.

The amendment forgives **only** same-sequence adjacency, and a forgiven occurrence does not advance
the recorded site. `behaviour_internal_test.go` pins both halves: `x x x` is one countable
occurrence and passes, while `x x x x` holds two *disjoint* pairs and still fails, as do runs up to
nine, the same digram across two sequences, and adjacent indices in different sequences. The
loosening cannot decay into a check that accepts everything.

## 6. Defects found, and where they came from

| # | Defect | Found by | Fix |
|---|---|---|---|
| 1 | `QualCurrent` was the zero value, so any struct literal omitting the field silently minted an authoritative elimination — the §12-High false `already_tried`, reachable by forgetting a field | role C, as a live bug in its own first draft | `66c60f7` — `QualUncertain` is now the zero value; pinned by test |
| 2 | `checkNoDigramTwice` unsatisfiable | role A | `810d196` |
| 3 | `ParseRuleRef` delegated to `strconv.Atoi`, which accepts `"+7"`/`"07"`, making encoded rule identity many-to-one — a forged stream could name rule 7 in a spelling no encoder emits | Main's own test, written for A's Q2 | `1b0fd5d` |
| 4 | The codec clause admitted version zero, so a zero-filled truncated write would read as a valid empty grammar | role B | `767dd7e` (amendment A6) |
| 5 | An uncertain warning rendered identically to a confident one, so the qualification lived in the type and nowhere the reader could see it | role B | `767dd7e` (amendment A7) |
| 6 | `Proposal.Reason` is prose but `rehydrate` consumed it as a `DropEntry.ID` — a whole sentence in the report's id column | Main, at integration | `f84cdf4` — `Item` and `Reason` split |

Defects 1 and 6 are the ones that justify the shape of this exercise: 1 was in **Main's own frozen
contract** and was caught by a role coding against it, and 6 was invisible to every role because it
only exists where two roles' outputs meet.

## 7. Contract amendments

A1–A7, all recorded in `contract.md` rather than applied silently: the redundancy term is
evidence-root duplication (§3 left it undefined); `QualUncertain` is the zero value; `RepArchiveOnly`
is an outcome and never a competing representation; the store's missing session-scoped enumeration
is an accepted **reported** limitation (`ErrDegraded` alongside valid results) rather than a new
interface amendment; `DetectRedundancyWithConfig` is additive because §5.12 freezes the three-argument
shape; codec version zero is refused; the uncertain warning says so in the line the user reads.

## 8. Layering — the question the plan left open

`tools/devtool/importrules.go` denies `rehydrate` any `analyzer` import and denies `analyzer` any
`negknow` import. Commit 6 therefore wires the **daemon composition root**, not an import edge, and
**no §3.2 amendment was needed or proposed**.

Selection reaches `Build` as request **data**, not a provider on `Deps`. That is not only the
layering answer: `Build` is documented as a pure function of `(Request, Deps)` and
`PropBuild_Deterministic` requires byte-identical output for identical input, so a live provider
would have put a store read, a DAG walk and a token estimate inside a function whose whole contract
is that it has none.

## 9. Run map (R2)

Focused checks ran per slice; one long gate was scheduled deliberately, after integration, for its
named coverage obligation.

| Command | Snapshot | Class | Result |
|---|---|---|---|
| `go test ./internal/analyzer/... -count=1` | per-slice + composed | focused | ok |
| `go test ./internal/grammar/... -count=1` | per-slice + composed | focused | ok |
| `go test ./internal/rehydrate/... -count=1` | `f84cdf4` | affected consumer | ok |
| `go test ./internal/daemon/ -count=1` | `f84cdf4` | affected consumer | ok (39.8s) |
| `go test ./internal/observer/... ./internal/checkpoint/... -count=1` | `1b0fd5d` | blast radius of a real Sequitur | ok (6.7s / 53.1s) |
| `go test ./test/guards/ -run 'TestAllStubs\|TestGuard_' -count=1` | `1b0fd5d` | guard activation | ok |
| `go test ./test/replay/ -count=1` | `86dd0be` | long gate, named obligation | ok (48.3s) |
| `go run ./test/replay --phase 6` | `86dd0be` | real gate path | phases 5 and 6 PASS; 2 pre-existing failures, §10 |
| `go test ./internal/config/... -count=1` | `8d338fb` | config + regenerated goldens | ok |

Golden regeneration ran on Linux via a cross-compiled test binary, because
`runtime.daemon.connectDeadlineMs`'s default is platform-specific and the golden is checked in as
rendered on a portable-default host.

Every `-run` filter used in anger was confirmed with `-v` to have matched real cases. No zero-test
run is counted as a pass anywhere in this report.

## 10. Pre-existing failures, verified as such

None is caused by SP-15. Every one was reproduced on the base tree or in isolation rather than
assumed, because "it looks like a known category" is not evidence.

| Failure | Verification | Verdict |
|---|---|---|
| `test/guards` `TestCarriedDefects_WaveReportRequiresResolution/{SP08-D1,SP10-D1}` | wave-2 defects still `open` in `plans/CARRIED-DEFECTS.tsv` while `plans/V4-report.md` exists; no SP-15 file involved | pre-existing bookkeeping |
| `go run ./test/replay` — phase 3 A2 divergence, corpus stale | reproduce **identically** on `develop@7c735ac` at `--phase 3` | pre-existing. Registering phases 5/6 makes the staleness message name a later phase; it does not create the failure |
| `test/e2e` `TestV3_HotPathUnchangedWithLedgerResident` | run on **base `develop@7c735ac`**: same failure, same number — B-B p50 **2.816ms** against a 2.000ms limit, every other budget PASS | pre-existing B-B breach ([[v4-hot-path-b-b]]); a V4 sign-off item |
| `test/integration` `TestIntegration_HotPathWarmWithRealResidentState` | run **alone**: same B-B breach, p50 2.816ms vs 2.000ms, B-A/B-E/B-E_cpu all PASS | same root cause as the row above |
| `test/integration` `TestIntegration_BeladyPMinLandsAtLowCoupling` | run on **base `develop@7c735ac`**: identical failure. 61.5% against a 70% floor, and the assertion's own message says "report the measured rate, do not lower the floor" | the deliberately-red Belady row; an authorized re-derivation of the floor is a V4 sign-off item |
| `internal/mcp` `TestBudgetBF`, `internal/negknow` `TestBudget_DetectorScan` | pass when re-run serially with `-p 1` | co-load artifacts of the first (mis-run) whole-tree pass |
| `internal/negknow` `TestBudget_Open` | passes **alone** at 259ms CPU/op against a 300ms budget | load-sensitive, marginal; matches the record for this base commit |

**A methodology note against myself.** The first whole-tree run was invalid and its failures should
not be cited: it omitted `-timeout=30m` (documented as required, and its absence panicked
`test/e2e` mid-suite, aborting every package after it) and used `-p 2`, which manufactures exactly
the co-load that makes timing rows meaningless. The re-runs above are the evidence; that run is
not.

## 11. Routing (R1/B08)

R1 requests Opus 4.8 for roles A/B/C/E, Opus 5 for Main and D, and Fable 5.1 for the independent
reviewer. **This client exposes only `opus`, `sonnet`, `haiku` and `fable` as model selectors**, so
Opus 4.8 was not selectable and every role ran on Opus 5. Recorded as R1's documented fallback, not
as a claimed route. Effective per-child model/effort metadata is not exposed, so **no routing or
cost-saving claim is made**, per B07/B08.

**The mandatory independent review is NOT satisfied.** R1 is explicit that an author's own recheck
cannot discharge it and that it remains pending until a separate reviewer is available. Main's
verification in §2/§5/§10 is a coordinator check by a thread that did not author the code under
review, which is stronger than self-review but is not the Fable 5.1 adversarial review the plan
names. **This is an open exit-criteria item, not a closed one.**

## 12. Deviations from the plan, stated plainly

1. **Commit order.** The plan numbers grammar first (1, 2) then analyzer (3, 4, 5). The landed
   history runs analyzer, then grammar, then integration. Reordering would have required fixes to
   precede the discoveries that prompted them — defect 1 was found by role C and defect 3 by a test
   written for role A — so truthful history was preferred over the nominal sequence. All seven
   commits' deliverables are present, each individually green.
2. **Extra commits.** Two contract-freeze commits precede commit 1, and four fix commits are
   interleaved. The plan's "seven numbered commit identifiers" are covered; the count is larger.
3. **Role D's report** was filed by Main from D's structured return, because a harness guardrail
   blocked that subagent from writing report files. D did not circumvent it.
4. **Attribution trailers omitted from every commit.** The repo's `commit-msg` hook rejects
   `Co-Authored-By`, `Signed-off-by` and "generated with" per §10/§20. Roles A, C and E each
   independently hit this and each followed repo policy rather than bypassing the hook; Main did the
   same. Repo rule beats session preference.

## 13. Open items

| Item | Owner | State |
|---|---|---|
| Independent adversarial review (R1, mandatory) | coordinator | **OPEN** — no separate reviewer route available in this client |
| M5-U15-representation-overhead — assembled cost is estimated, not calibrated against provider-reported usage | selector owner | **OPEN**, as the plan anticipated. Sound for ranking; not a token guarantee |
| `ToolUsesBySession` — redundancy coverage is exact per covered path only | SP-19 / arch | **OPEN, reported** (`ErrDegraded`), not silently partial |
| Cheapest-dependency rule costs objective value at generous budgets (E's Q1) | selector owner | **OPEN** — a dependency-upgrade pass would close it |
| A signature-level stride check would catch the long-period cycle without a grammar (E's Q2) | warning owner | **OPEN** — would make the ablation's one Sequitur win unnecessary |
| Wave-2 carried defects SP08-D1 / SP10-D1 | V4-VERIFY | pre-existing, out of scope |

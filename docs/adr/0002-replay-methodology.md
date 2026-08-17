# ADR 0002 — Replay methodology: what the numbers mean and how they are produced

**Status:** accepted (SP-02, wave 1)
**Supersedes:** nothing
**Related:** [ADR 0003](0003-replay-overfit-recollection.md), `Qompack.md` §4.2, §5.6, §6.10, §10 Phase 0, §11

---

## Why this document exists

`Qompack.md` §1.3 RC-3 indicts the stock system for being unmeasured, and the closing note ranks
Phase 0 first: *"Without measurement, everything else is opinion."* A harness that flattered the
plugin would reproduce the exact sin the project is built to fix. So every number the replay gate
emits carries its own provenance, and this document is where the provenance is written down.

Three claims are made here and each is mechanically enforced somewhere in the tree:

1. Every committed number is reproducible bit-for-bit from committed inputs.
2. Every number that is **modelled** rather than **observed** says so in its own output.
3. The corpus tier a number was computed over travels with the number.

---

## The primary metric

**Fraction of Belady OPT** (§11.1). For each compaction event in a session, the clairvoyant
optimal keep-set is computed under the *same* token budget the policy decided under, and the
policy's keep-set is scored against it.

OPT is 0/1 knapsack, not plain furthest-in-future. Blocks have heterogeneous sizes, so "keep what
is needed soonest" is not well defined until weights are equal; with unit weights the formulation
degenerates to classic Belady, which is what makes it the generalization §6.10 asks for rather
than a substitute for it.

A session's fraction is **micro-averaged**: `Σ satisfied / Σ optimal` across its compaction
events, not the mean of the per-event fractions. A compaction that dropped a lot therefore counts
for more than one that dropped little, which is the honest weighting.

Both sides are scored against the **same** demand set — the one recorded on the `Run` — so the
policy and the ceiling can never be graded on different questions.

### The floor and the ceiling

| Policy | What it is | What it must score |
|---|---|---|
| `null` | keeps nothing | exactly 0.0 |
| `stock` | Claude Code Full Compact, modelled per §2.3/§2.4/§2.5 | strictly above `null` |
| `oracle` | delegates to the Belady solver | exactly 1.0 |

`oracle` scoring 1.0 is how the scorer proves it is self-consistent. `stock` beating `null` is a
property of the **corpus**, not of the code: if it ever ties, the corpus shape is wrong and the
corpus gets fixed, never the assertion.

---

## Corpus tiers, and the word "real"

§10 Phase 0's exit criterion says *"reproducible across at least 20 **real** sessions"*. The
committed baseline is computed over **24 synthetic** sessions, and that substitution is deliberate,
is 00-ARCHITECTURE §6.3's ruling, and is declared in the artifact itself.

| Tier | Where | Gates | Committed |
|---|---|---|---|
| synthetic | `testdata/sessions/synthetic/` | every pull request | yes — sessions and number |
| recorded | `$QOMPACK_SESSIONS_DIR` | releases | the **number** only, never the sessions |
| live fork | `QOMPACK_EVAL_LIVE=1` | manual, pre-release | no |

`testdata/baseline/phase0.json` carries `"corpusTier": "synthetic"`. Anything that reported a
synthetic number as if it were a real-session number would be precisely the dishonest measurement
§1.3 RC-3 indicts, so the tier is a field rather than a convention.

### Producing the recorded-corpus number

This is a **scheduled, operator-run step**, not a CI step: it needs ≥ 20 recorded sessions on the
machine, and recorded transcripts are never committed.

**Who:** the release manager, as part of the pre-release checklist (00-ARCHITECTURE §6.3 tier 2,
§8).
**When:** before each release, and after any change to the observer or the checkpointer that
would alter what a session looks like.

```sh
export QOMPACK_SESSIONS_DIR=~/qompack-sessions        # outside any repository working tree
qompack eval import --from ~/.claude/projects         # redacted by default
go run ./test/replay \
    --corpus "$QOMPACK_SESSIONS_DIR" \
    --write-baseline \
    --baseline testdata/baseline/phase0-recorded.json
```

The resulting file carries `"corpusTier": "recorded"` and its own session count. The importer
refuses any destination inside this repository, so "never committed" is mechanical rather than
remembered.

---

## Reproducibility

The Phase 0 criterion's "reproducible" half is checked the only way that means anything: the
driver replays the whole corpus **twice in the same process**, with a freshly constructed harness
the second time, and compares the canonical metric renderings byte for byte.

The fresh harness is the point. `ScoreRun` pools raw latency samples so `Report` can recompute
percentiles over the whole corpus — an average of P95s is not a P95 — and that pool is the only
state in the package. Reusing the harness would let a stateful bug hide behind an
accumulated-but-consistent number; rebuilding it makes the same bug a diff.

Determinism comes from four decisions:

- the synthesizer uses an explicitly seeded PCG, never the package-level source;
- every map that reaches output is walked in sorted key order;
- the Belady solver orders items by `(−value, weight, ID)` and improves strictly, so ties resolve
  the same way twice;
- percentiles are nearest-rank, never interpolated.

---

## Latency is modelled, and says so

Nothing in deterministic mode calls an API, so there is no wall-clock to report. The three §11.2
latency metrics are computed from `eval.LatencyModel`, every report carries `"latency":
"modelled"`, and `LatencyModel.Modelled` is never false in a built harness.

The coefficients are calibrated against two sentences of the design, and a unit test names the
sentence each answers to:

- **§6.7** — "the summarization call at 167K input runs ~15–40s". At §2.5's stock residual of
  167 000 tokens the model yields 28 050 ms.
- **§8.5 (O5)** — after frontier advancement the residual is "10–20K tokens rather than 150K". At
  that range the model yields 4.5–6.0 s.

If anyone changes a coefficient, the test names the claim they broke rather than merely going red.

---

## The metric table

Nineteen metrics, one direction each. `MetricNames()` is the single source of truth; a
completeness test asserts exact set equality with `MetricsOf`'s keys in both directions, so a new
metric cannot land without a direction and a direction cannot linger for a metric that is gone.

**Higher is better (6):** `fraction_of_opt`, `first_divergence_turn`, `file_set_jaccard`,
`same_decision`, `decision_preservation`, `retrieval_hit_rate`.

**Lower is better (13):** `tool_edit_distance`, `redundant_reads`, `re_attempts`,
`rewrite_span_tokens`, `rewrite_tokens`, `forfeited_discount_tokens`, `rehydration_tokens`,
`compaction_pause_ms_p50`, `compaction_pause_ms_p95`, `residual_span_p50`, `residual_span_p95`,
`first_turn_after_ms_p50`, `first_turn_after_ms_p95`.

### Three rewrite quantities, never one

§5.2's table has been misread before, so the harness reports all three separately:

| Name | Definition | Scenario A | Scenario B |
|---|---|---|---|
| `rewrite_span_tokens` | `n − p_min` — the doc's **Rewrite** column | 17 000 | 157 000 |
| `rewrite_tokens` | `w · (n − p_min)` | 21 250 | 196 250 |
| `forfeited_discount_tokens` | `(1 − r) · (n − p_min)` | 15 300 | 141 300 |

The table's "30×" is the **benefit** ratio (60K dropped vs 2K dropped), not the rewrite ratio,
which is `157/17 ≈ 9.2×`. `w` and `r` are read from `scheduler.cache.*` at the use site; there is
no `1.25` anywhere in `internal/eval`, and a runtime test proves it rather than trusting the
`nomagic` pass.

### Two aggregates that cannot carry full resolution

`Report.Policies` is a `map[string]Score`, and §5.18 fixes `Divergence.FirstDivergenceTurn` as an
`int` and `SameDecision` as a `bool`. Their corpus aggregates are therefore the rounded mean and
the majority verdict. That is stated here rather than hidden: a one-turn shift in the corpus mean
of `first_divergence_turn` is the metric's resolution floor, and `decision_preservation` — a float
— carries the finer-grained version of what `same_decision` reports coarsely.

---

## Interpreting `first_divergence_turn`

It reports how many turns **after the compaction** the branches first disagree, and equals the
horizon `K` when they never do. `-1` is never emitted. The metric is higher-is-better, so "never
diverged" has to be the largest value it can take; a sentinel would make the 2% gate score a
perfect policy as the worst one.

`redundant_reads` is signed for the mirror-image reason: a policy that *prevents* re-reads is an
improvement, and the metric has to be able to say so rather than clamping at zero.

---

## The phase registry

`test/replay/phases.go` holds `phaseChecks map[int]func(Context) error`, and the driver runs every
entry at or below `--phase` on every pull request. This is the mechanism behind §11.3's *"every
phase gate runs the full replay suite"*: once a phase has landed, its exit criterion is
re-asserted forever, so a later wave cannot quietly undo it.

**Contract for later subplans:** append an entry — `1` for the store, `2` for negative knowledge,
and so on — rather than inventing a new gate. A check receives a `Context` carrying the report,
the driver envelope, the configuration, both canonical renderings, the corpus manifest, the growth
verdict and the sketch health. If a phase needs something not in there, add a field; do not reach
around the struct.

---

## The 2% rule

For every metric of every policy, with `b` the baseline and `v` the observation:

```
worse      = (direction == higher-better) ? (v < b) : (v > b)
rel        = |v − b| / max(|b|, 1e-9)
regression = worse && ( |b| >= 1e-6 ? rel > 0.02 : |v − b| > absTol )
```

`absTol` is `0.02` for the five ratio metrics and `1.0` for counts. The absolute branch only
matters against a baseline of essentially zero, where a relative comparison means nothing.

A regression is **allowed** only when the pull-request body carries a trailer naming that exact
metric with a reason of at least ten characters:

```
Sign-off: rewrite_tokens=+3.10% traded for a 9% first-divergence gain
```

On a push event the body is empty, so no regression can be signed off on a direct push. That is
correct: a trade-off nobody reviewed is not a trade-off anybody agreed to.

---

## Guardrails this gate owns — and the one it does not

§11.3 lists four guardrails. Three are enforced here:

- **Store growth sublinear** — `eval.CheckSublinearGrowth` behind `--growth`. It regresses stored
  bytes on **raw** bytes with turn count as a validity guard, because turn count is a bad x-axis
  (one 40 MB test run and one 200-byte `Grep` are both "one turn") while raw bytes is the quantity
  dedup is actually asked to beat. A series where raw bytes stop tracking session length is
  rejected as inconclusive rather than fitted anyway, and **inconclusive fails**: an unmeasurable
  guardrail is not a passing guardrail.
- **No metric regresses > 2% without sign-off** — above.
- **Every phase gate runs the full replay suite** — the phase registry, plus
  `eval.replayOnPhaseGate`. A developer may set that key false for a fast local loop; under `--ci`
  the same combination is exit 5.

The fourth — **hook p99 < 15 ms (L0), < 2 s (L4)** — is **budgets B-A and B-E, enforced by SP-05's
`bench-gate` job**. `test/replay` asserts nothing about it. Claiming otherwise would be the same
dishonest measurement §1.3 RC-3 indicts.

---

## Breakpoint placement is measurement, not a feature

`eval.BreakpointOPT` computes the optimal `cache_control` marker set for a session. §5.6 and §12
both state that Claude Code manages its own cache markers and a plugin cannot move them, so every
plan carries `eval.NotPluginActionable` and the driver prints that sentence on the line **above**
the number, always. A test asserts the ordering.

The analysis is retained because it applies verbatim if Qompack is ported to a first-party harness
on the Messages API (§2.8), and because the Belady setup makes it measurable today.

---

## Provider seams

`internal/eval` imports foundation packages only (00-ARCHITECTURE §3.2), which is what lets L7 be
built in the earliest parallel wave against no sibling's output. Where it needs data only a later
package can produce, it declares a **provider type** with the same field shape and the driver — a
composition root — supplies it:

| Type | Wave-1 source | Later source |
|---|---|---|
| `eval.StatsSample` | `testdata/golden/contracts/store/stats-growth.json` | SP-06's `store.Stats` |
| `eval.SketchHealth` | `testdata/golden/contracts/negknow/health.json` | SP-09's bloom health |

Both fixtures are Rule W-2 contracts: the wave-2 verification re-runs these same checks against
the real implementations, and a fixture the real implementation cannot reproduce is a verification
failure, not a fixture bug.

---

## Honesty checklist

Every one of these is enforced by a test:

- [x] Latency numbers are tagged `"latency": "modelled"`.
- [x] The breakpoint number is preceded by its not-plugin-actionable disclaimer.
- [x] A `retrieval_hit_rate` of 0.0 is reported alongside `retrievalActions: 0`, so an absence of
      calls is never mistaken for a failure.
- [x] `forfeited_discount_tokens` is reported separately from `rewrite_tokens`.
- [x] The corpus tier travels with the number.
- [x] A policy that overran its budget is named in `budgetViolations` and fails the gate.
- [x] Sessions where nothing was demanded are named in `noDemandSessions`, and a corpus more than
      a quarter made of them fails the gate.
- [x] A degraded OPT (the 1/2-approximation fallback) prints a WARN naming the session.

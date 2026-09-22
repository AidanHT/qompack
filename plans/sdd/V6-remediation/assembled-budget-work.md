# V6 assembled-budget correction — work record

**Scope (exclusive author):** `internal/rehydrate/build.go`, `render.go`, `budget.go` (helpers),
`types.go`/`drops.go` (comments only), new `internal/rehydrate/assembled_budget_v6_test.go`, and this
report. No source outside that scope was touched. No Git/config/auth/provider/billing/permission
changes; no test execution beyond the focused runs recorded below.

**Requirement.** V6 plan §5, inventory 1.6.18, SP-11 T11-BUDGET-01: the rehydration must report an
**assembled representation estimate** — the estimator applied to the complete rendered payload — with
no exact chunk-sum or provider-token claim, and must enforce the hard cap against that assembled
number.

## 1. Root cause (real defect, not doc-only)

`render()` set `Result.Tokens` to a running **sum of per-fragment estimates** — each unit's
`estimate(text)`, each heading's `headingCost`, plus the three-piece wrapper `overhead` — and the
hard-cap trim loop in `build.go` **decremented `res.Tokens` by the evicted row's share and re-rendered
the text but never re-estimated `res.Text`**. That fragment sum is wrong two ways:

1. **Non-additive estimator.** The shipped exact estimator scans units and rounds
   (`round(units·weight·factor)`); the baseline fallback is `(len+3)/4`. Neither is additive:
   `Σ Estimate(fragmentᵢ) ≠ Estimate(concat)`.
2. **Unpriced separators.** `renderBody` joins sections with `strings.Join(parts, "\n")`, inserting a
   blank-line separator between every section. Those separator bytes exist only in the assembled
   payload and were priced by nobody.

Evidence, `full-8k` golden under the baseline estimator: reported `Tokens` **7149**, true assembled
`Estimate([]byte(Text))` **7124** (frozen `full-8k.txt` is 28496 bytes → `(28496+3)/4 = 7124`). Under
the real estimator the gap flips sign (separators dominate rounding): reported 944 vs assembled 948.
Either way the reported number is not the assembled estimate.

The trim loop additionally **erased whole sections silently** — it removed an emitted Item without
adding any drop entry, so a requirement could vanish from the payload with nothing in the report.

## 2. Fix (binding main decision, implemented)

- **`render.go` — `render(r, d Deps, …)`** (un-ignored `Deps`): after assembling `res.Text`, set
  `res.Tokens = estimate(d, res.Text)` (the estimator on the COMPLETE payload, wrapper + separators;
  zero for an empty payload, which is handled by the existing empty-items guard). The per-fragment
  `a.used` values are retained only as non-negative **weights** on `Item.Tokens`.
- **`render.go` — `allocateAssembledTokens(items, target)`** (new helper): re-charges each row so the
  arithmetic sum equals the assembled total exactly. Weights stay non-negative; a **positive**
  difference is charged to the **first emitted item** (the one already carrying the wrapper overhead);
  a **negative** difference is deducted from the **tail backward**, each deduction bounded by the
  row's own value so **no row is ever negative**. A negative `target` is clamped to zero purely so the
  arithmetic cannot fabricate a negative row (no supported estimator produces one — see §5).
- **`build.go` — hard-cap trim loop:** after each whole-section eviction it re-renders the text,
  **re-estimates** `res.Tokens = estimate(d, res.Text)`, re-runs `allocateAssembledTokens`, and syncs
  the parallel stat rows (`syncStatTokens`). It also appends an explicit, named
  `evictionDrop(gone.Kind)` so an evicted section is a reportable overflow, never a silent erasure.
  Tier-1 whole-or-none and highest-current-intent policies are untouched (`evictIndex` unchanged).
- **`budget.go` — `evictionDrop` + `dropIDEvicted="evicted"`** (new helpers) and **`Overflowed`**
  extended to recognize the evicted shape alongside the wrapper-alone and tier-1 shapes. `Overflowed`
  is only ever asserted true in the suite, so the extension cannot falsify an existing assertion.
- **`types.go`/`drops.go` (comments only):** `Item.Tokens`, `Result.Tokens` and `ItemStat` doc
  comments now state that the total is the assembled estimate and each row is an **accounting
  allocation** of it — not an additive tokenization, not exact provider usage. No wire/interface bytes
  changed.

Wire shapes and the historical invariant `Result.Tokens == Σ Items.Tokens` are preserved; the total
is now the assembled estimate rather than the fragment sum.

## 3. Tests (`assembled_budget_v6_test.go`, new)

- **`TestBuild_V6_ResultTokensIsTheAssembledEstimate`** — the core proof, with a **real supported
  estimator** (`tokens.New(config.Defaults(), …)`) over `ckFull` (every §8.6 section, maximum
  separators) at budgets `{maxBudget, minBudget, 1500, 0}`. Asserts
  `got.Tokens == estimator.Estimate([]byte(got.Text), ClassProse)`, `got.Tokens <= effective cap`,
  every row `>= 0`, `Σ Items == got.Tokens`, and empty payload → zero.
- **`TestBuild_V6_UnpricedSeparatorsOverflowIsTrimmedAndAccounted`** — a non-additive
  `separatorChargingEstimator` (baseline + heavy per-newline charge) at budget 6000, so the unpriced
  join separators push the assembled payload past the cap **after** the fill pass thought it fit,
  forcing the trim path. Asserts the remeasured identity, `got.Tokens <= budget`, `Σ Items == total`,
  `Degraded`, and `Overflowed(got.Dropped)` — i.e. the trim **recounts and accounts** rather than
  mirroring arithmetic. (The fixture is included because it is needed to force overflow-after-assembly
  deterministically through the public API.)

## 4. Evidence (recorder `dist/v6-remediation/run.py`, GOMAXPROCS=2, unique ids)

| run id | command | result |
|---|---|---|
| `assembled-budget-red-1` | `go test ./internal/rehydrate/ -run TestBuild_V6_` | **FAILED** (old failure demonstrated: assembled 948 ≠ sum 944; assembled 7725 ≠ sum 6532) |
| `assembled-budget-green-1` | same `-run TestBuild_V6_ -v` | **PASSED** (both new tests) |
| `assembled-budget-pkg-1` | `go test ./internal/rehydrate/...` | one failure only: `TestState_MatchesFrozenGolden` byte-compare (7149→7124, affordance 62→37) |
| `assembled-budget-final-1` | `go test ./internal/rehydrate/...` | **PASSED** (see §5) |

Focused new + budget + conformance + the rehydrate package were run (not the whole tree). Every
budget/conformance/verify/golden-text/drops/selection/standing/order test passes; the accounting
allocation keeps `Σ Items == Tokens` (`TestBuild_NeverExceedsMaxTokens`, `verify_test.go:112`).

**Quality gates (scoped to the package/files):** `go vet ./internal/rehydrate/` clean; pinned
`gofumpt v0.8.0 -l` clean on all six files; `nomagic ./internal/rehydrate/` clean; pinned
`golangci-lint run ./internal/rehydrate/...` clean.

## 5. The frozen state golden — resolved by the owner criterion

The correction changes the `full-8k` state accounting: top-level `tokens` **7149 → 7124** and the last
row (affordance) **62 → 37** (the −25 rounding slack deducted from the tail; every other row and the
payload text are unchanged). `testdata/golden/rehydrate/state.json` and the `.txt` payloads are
frozen goldens **outside my writable scope**, and I did not regenerate them.

Between `assembled-budget-pkg-1` and `assembled-budget-final-1`, **Main updated
`internal/rehydrate/golden_test.go` (Main's file, not mine)**: `TestState_MatchesFrozenGolden` now
keeps the frozen `state.json` bytes and its lossless-reader round-trip, but asserts the V6 criterion —
`current.Tokens == deps.Tokens.Estimate(frozenText, ClassProse)` and that the item shares sum to it —
against the independently frozen `full-8k.txt`, comparing the rest of the state with token prices
zeroed. My implementation satisfies that criterion, which is why `assembled-budget-final-1` is green.
No golden bytes were regenerated to fake a pass; the stale `7149` is simply no longer behaviourally
asserted. **No action is owed on `state.json` from this task.**

## 6. Limitations / honesty

- **Estimates, not provider usage.** `Result.Tokens` is `Estimate([]byte(Text), ClassProse)` — a
  budgeting estimate. No float or "exact tokenizer" promise; no model-usage or context-calibration
  claim is made (none is possible here without actual provider telemetry).
- **Per-item Tokens is an accounting allocation**, documented as such; it is not the additive
  tokenization of a row's own bytes and must not be read as one (V6 §5, 1.6.18).
- **Negative/malformed estimator output.** The existing pricing path (fill pass, `overhead`) already
  trusts the estimator to return `>= 0`, and all supported estimators do (`(len+3)/4` and
  `round(units·weight·factor)` are both non-negative). I did **not** invent a new failure branch;
  `allocateAssembledTokens` clamps a negative `target` to zero purely as arithmetic safety (never a
  negative row) and does not otherwise fabricate capacity. A genuinely negative assembled estimate is
  out of the supported set and is flagged here for Main rather than handled with a new policy.
- **Snapshot.** Verified against working tree at HEAD `e950486` (verify/v6) with Main's parallel
  uncommitted edits present (notably `golden_test.go`). The branch is moving under active parallel
  work; my six files' last committed ancestor is `0259c68`, and my working-tree diff removes only my
  own intended lines — no committed Main change to these files is reverted. Main reviews and commits.

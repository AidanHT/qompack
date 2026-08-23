# qompack — code-fix round after the wave-0/1 plan audit

> **CLOSED 2026-08-23. Do not run this brief again.** It was executed on branch
> `fix/post-audit-code-round` and merged to `develop` at `33bb206`. A1–A7, A8.6, A8.7, A8.9, A9.1,
> A9.2–A9.6 and A10 are fixed; the remaining eight findings are rows in `plans/CARRIED-DEFECTS.tsv`
> (all `deferred:V3-VERIFY`, which the carried-defects guard enforces against `plans/V3-report.md`).
> What the round closed, what it carried, and the two contracts it changed along the way are
> recorded in `plans/V2-report.md` §16.8 and §16.8.1 — read those rather than this file. The text
> below is kept verbatim as the record of what was asked for, and its line numbers and "state when
> written" describe `e7dd4ad`, not the tree you are looking at.

Paste this whole file as the opening prompt of the next Claude Code session.

> **Scope: wave-0/1 CODE defects found by the 2026-08-22 plan audit.** A 28-agent adversarially
> verified audit compared every plan document through wave 1 against the tree (206 findings, 0
> refuted). All plan-document corrections were applied directly on branch `docs/plan-audit`
> (~150 edits across 25 plan files). What remains — this file — is the **code** side: defects
> in shipped wave-0/1 code and test infrastructure that plan edits cannot fix. Wave 2 does not
> start until these are closed or consciously carried with a reason in `plans/V2-report.md` §16.4.

---

## Context

- **Repo:** `C:\Users\Quant\Documents\Programming\Projects\qompack`
- **Branch to build on:** `docs/plan-audit` (cut from `develop` @ `e7dd4ad`, carries the plan
  corrections). If it has already merged to `develop`, branch from `develop` instead.
- **State when written:** wave 1 CLOSED (v0.1.0 tagged locally), post-v2 hardening merged
  (`e7dd4ad`), local whole-tree `CGO_ENABLED=1 go test -race -timeout=40m ./...` was exit 0 at
  the hardening tip. CI was blocked on the GitHub Actions quota (see §D).
- Every item below carries the evidence that established it. Line numbers were verified
  2026-08-22 against `e7dd4ad` — **re-verify before acting**; the plan-audit edits did not touch
  code, but drift happens.

---

## A. Code defects — fix these

### A1. `runpatterns` is blind to every `./pkg/...` pattern, and lies about it

The lint exists so no plan row can name a nonexistent test (`go test -run` prints `ok` on zero
matches). But `namesFor` builds its lookup key as `byPkg[modulePath+strings.TrimPrefix(pkg, ".")]`
(`tools/devtool/planchecks.go:385`), and `go test -list` status lines carry plain import paths —
never a `/...` suffix. So a plan command spelled `./internal/cli/...` builds a key that can never
hit; `if !ok { continue }` (`:488-490`) drops it silently AFTER it was counted into `checkable`,
and the summary (`:518`) reports it as resolved. ~115 of ~725 `-run` commands in `plans/*.md` use
the wildcard form. On the ubuntu CI host these land in `skippedUnlanded` instead — unchecked
either way.

**Fix:** strip a trailing `/...` and union the names of every `byPkg` entry under that
import-path prefix; turn the `!ok` branch into a reported problem; count only actually-resolved
patterns in the summary. Then re-run the lint over `plans/` — the audit fixed every dead name it
found, but the fixed lint is the proof there are no more.

### A2. `session_start.source_compact` ignores the session id its own spec requires

`00-ARCHITECTURE.md` §12.1 and the SP-05 plan both say: after a `PreCompact`, the next
`SessionStart` must arrive with `source == "compact"` **within the same session id**.
`checkSessionStartSourceCompact` (`internal/contract/assertions.go:132-141`) reads only the
boolean `AwaitingCompactStart`. `SessionHistory.LastPrecompactSession` is written
(`internal/daemon/handlers.go:629`) and read by **nothing** (grep it). A PreCompact in session X
followed by a fresh `SessionStart` of session Y fails the assertion against the wrong session.

**Fix:** when `AwaitingCompactStart && LastPrecompactSession != "" && != e.Event.SessionID`,
clear the flag and return OK with `Observed:"precompact-pending-for-another-session"`; fail only
on an id match. Test the cross-session case.

### A3. The daemon loads sketches through the silent `Load`, defeating the corrupt-file Loud

`internal/daemon/sketchset.go:104` calls `sketch.Load(p, target)` — the silent form — inside a
method that already holds a logger. SP-03's shipped contract says composition roots call
`LoadWithLog`, never `Load` (`internal/sketch/doc.go:76-79`, `io.go:92-94`). Worse, `sketch`
maps EVERY failure to `core.ErrNotFound` (`io.go:120-132`), so sketchset's
`errors.Is(err, core.ErrNotFound)` branch (`:108`) files a corrupt/CRC-failed sketch under
"expected, Debug" alongside a genuinely absent one.

**Fix:** switch to `sketch.LoadWithLog(p, target, log)`, and classify by the sketch sentinels
(`ErrCorrupt`, `ErrTruncated`, `ErrTooLarge`, `ErrBadMagic`, `ErrUnsupportedVersion`,
`ErrKindMismatch`): Debug only for a genuinely absent file, Loud otherwise. Add the grep-style
enforcement test SP-03's plan describes so `Load` cannot creep back into a composition root.

### A4. The GC mark phase is unbounded, and a comment claims otherwise

`mark` (`internal/store/gcrun.go:182-245`) takes no ctx and no deadline: `harvestHashes`
(`:323`) streams every checkpoint/pins/eliminations file token by token with no `ctx.Err()` and
no clock check. The comment at `gcrun.go:468-471` claims "both this loop and the mark phase
check the deadline and the ctx"; the plan (`V2-SP-06…md:830`) states the same as the contract.
Both are false for mark. (Related, deferred SP06-D1 covers the *tombstone* phase — this is a
third phase with the same hole, currently recorded nowhere.)

**Fix:** thread ctx + deadline into `mark`, checking every `gcCheckEvery` items in the harvest
token loop and the index walks; return `Truncated` on expiry. Fix the `:468-471` comment.

### A5. GC's ephemeral age-exclusion misses the tool_use path

Plan: "an ephemeral root is never in-window by the age clause" (`V2-SP-06…md:655`, `:824`).
`gcrun.go:206` honours it on the root path (`if !e.Eph && inAgeWindow(...)`); the tool_use path
at `:214` does not — `if inAgeWindow(rec.TS) || ...` keeps `rec.Root` live with no `Eph` test.
Any ephemeral root referenced by a tool_use record (i.e. **all** of them in practice, since
retrieval results are recorded) is age-live anyway; the documented eviction property is void on
the main path.

**Fix:** look the root up in `s.rootIndex` (same RLock) and skip the age half when `e.Eph`;
extend `TestGC_EphemeralNotInWindowByAge` to record a `tool_use` for both roots.

### A6. The sublinear-growth gate cannot fail

`internal/store/stats_test.go:165-183`: 120 puts, snapshot at 8, bound `Bytes < 25*after8`.
Pure LINEAR growth (zero dedup) is 15× — passes with 40% headroom. The plan specified 200 puts
against 25× (linear = exactly 25× — also non-discriminating). Mutation-test it: disable dedup
and the test must fail.

**Fix:** measure the real ratio, then bound strictly below linear with headroom (e.g. a
put-count-derived bound), and correct the plan row (`V2-SP-06…md:1053`, `:1210`) to match what
ships — the audit deliberately left those plan lines for this fix to settle.

### A7. The carried-defects guard does not guard the deferred rows

`test/guards/carrieddefects_test.go:42` defines `open()` as `status == "open"`; every current
row is `fixed` or `deferred:V3-VERIFY`, so (a) the evidence-liveness check (`:195`) covers zero
rows, and (b) `TestCarriedDefects_WaveReportRequiresResolution` (`:223-231`) derives the report
path from the `owner` column — always `V2-VERIFY`, whose report exists — so nothing blocks
`plans/V3-report.md` while six rows are still deferred there. The TSV header's three-way
"load-bearing" claim is currently one-third true.

**Fix:** treat `deferred:<X>` as unresolved for checkpoint X (key the report-path check off the
deferral target); require deferred rows' named evidence tests to be alive; require
`deferred:<X>` to name an existing `plans/<X>-*.md`. Verify by temporarily renaming an evidence
test and watching the guard go red.

### A8. The replay harness reports numbers several of which are structurally incapable of moving

All confirmed against the committed corpus and baseline; each needs a decision, a fix, and
usually a re-baseline (ADR 0003 protocol — instrument changes re-baseline, never `Sign-off:`).
Work them one commit each, in this order:

1. **The corpus raises only `DemandFileContent`** — all 177 demands across all 39 compaction
   events. Causes: the synthetic subagent report lands one turn after its Task call while
   `blocks.go:319-323` raises `DemandToolResult` only for references from *later* turns across
   the cut; nothing re-references eliminations or decisions post-cut (`synth.go:249,239`).
   `tool_edit_distance`, `re_attempts` and `decision_preservation` are therefore measured over
   an input that cannot exercise them. Fix the generator (report ≥2 turns later; straddle the
   cut; post-cut re-references), regenerate the corpus, re-baseline.
2. **`file_set_jaccard` is pinned at 1.0**: the only repair (`replay.go:237-241`) re-reads the
   same path key the demanding turn already touches. Make repairs able to move the file set, or
   compare multisets/order — else document the metric as inert in ADR 0002.
3. **The Belady token budget never binds** (oracle `rewrite_span_tokens == 0` at every event),
   so OPT is degenerate ("keep everything demanded") and `fraction_of_opt` grades against a
   trivial ceiling. Add a corpus assertion that Σ tokens(candidates) > budget at a stated
   fraction of events, and shape the corpus so it does.
4. **`pMin` iterates candidates only** (`belady.go:154-168`), not all blocks — deviating from
   §5.2 "earliest dropped block" with the deviation recorded only in a code comment. Pick a side
   (plan or code), then re-baseline or amend plan+ADR and add the SP-02 carried-defect row.
5. **`decision_preservation` measures post-compaction horizon agreement**, which deterministic
   mode fixes at 1.0 — not §11.2's "pre-compaction decisions still recalled". Redefine over the
   kept pre-compaction decision blocks, or relabel everywhere (plan, ADR, TRACEABILITY §6).
6. **A missing `--baseline` file exits 0** with the whole 2% rule skipped
   (`test/replay/main.go:345-348`), and `TestReplayDriver_MissingBaselineWarnsButStillChecksPhase`
   pins the wrong behaviour. Plan `:1081` demands exit 2 unless `--baseline ""` was explicit.
7. **The watch-for half of the 2% rule is inert**: `phase0.json` was written without `--sketch`
   (`watchFor` zeros), and a zero baseline falls to the absolute branch where only a move > 1.0
   could trip. Regenerate the baseline with `--sketch testdata/golden/eval/growth/health.json`
   and add the bloom keys to `ratioMetrics` (`test/replay/gate.go:44`).
8. **The stock model skips the 4/3 host padding** (`hostPadTokens` has no production caller;
   `hostSkillBudget` is fully dead). The plan's own pseudo-code omits it too, so decide: model
   the padding (and the skills restore, or delete the constant) — record either way in ADR 0002.
9. **The nightly recorded-corpus run compares against the synthetic baseline**
   (`nightly.yml:113` `--baseline develop`), the exact cross-tier comparison the `corpusTier`
   key exists to prevent. Make the driver refuse a tier mismatch (exit 2, naming both tiers)
   and point nightly at `testdata/baseline/phase0-recorded.json` per ADR 0002.

### A9. Smaller code items

| # | Site | Defect | Fix |
|---|---|---|---|
| 1 | `test/guards/stubs_test.go:135-137, :189` | `eval` entry promises a zero-arg panic walk the `*` branch skips before any method call | walk the methods asserting no-panic (restores a real assertion over five landed packages) |
| 2 | `internal/dag/traverse.go:274-277` | doc comment claims `sort.SliceStable`; the code and the better inline comment (`:294-298`) use `sort.Slice` over a total order | delete the stale sentence; plan `V2-SP-07…md:735` says SliceStable too — fix it with the same commit |
| 3 | `internal/sketch/bloom.go:152-155`, `bloom_test.go:123`, `docs/adr/0030…md:158` | the uncapped array for `NewBloom(2^24, 1e-6)` is ≈4.8×10⁸ bits (≈1.8× the cap), not 2³³ / "eight times smaller" | correct all three sites; optionally pin `mRaw` with `InDelta` |
| 4 | `docs/adr/0030-sketch-binary-format.md:64, :360-362, :389, :393-394, :404` | every quoted benchmark figure predates the `2d2b596` baseline regeneration and no longer exists in `testdata/bench-baseline.txt` | re-derive from the committed baseline; drop or restate the p=0.000 A/B claim whose "after" numbers are in no committed file |
| 5 | `internal/daemon/budget.go:14-19` | `hotPathTailAllowance` claims "measured by the bench harness on all three platforms under 0.4 ms" — no artifact measures the ACK-read→exit interval | either emit a `hook_tail_estimate` informational row from `test/bench/hotpath`, or restate as an unmeasured conservative ceiling (plan side already restated by the audit) |
| 6 | `QOMPACK_FAULT` | SP-05's plan claimed a CI grep that never existed (plan now restates the two-file invariant) | add a `test/guards` case asserting the literal appears only in `internal/cli/fault.go` and `internal/daemon/spawn.go` (mirror `TestGuard_NoNetworkImports`'s shape) |
| 7 | `canon.Decide` | zero consumers; `store.nearDup` computes its own `NearDupInfo`; §8.1's delta-vs-full storage is unimplemented and unowned (plan text now states this) | decide the owner (SP-16 refinement vs. store amendment vs. wontfix) and record it as a carried row or V3-VERIFY carry |
| 8 | `internal/store` PutBytes-warm budget | §9.3 shows 7.64 ms vs a 400 µs budget, "documented over" since V2; the promised budget revision never landed | measure on the reference host, revise `V2-SP-06…md` §5.3's two rows with shape+platform stated (V2-report carries the correction row pointing here) |
| 9 | optional | waves 2–5 merge orders have no build-order guards (README now says so honestly) | add per-wave guards to `test/guards/buildorder_test.go` before each wave's first merge — wave 2's before anything else |

### A10. Comment/CI-prose staleness batch (one `docs`/`fix` sweep; the plan side is already corrected)

| Site | What is wrong |
|---|---|
| `internal/canon/dedup.go:35, :50` | comments assert SP-06 consumes `Decide`; nothing does — `store.nearDup` computes its own `NearDupInfo` (see A9.7) |
| `internal/canon/canontest/suite.go:34` | says "twelve real canonicalizers"; fourteen are registered |
| `internal/eval/harness.go:13`, `internal/eval/hostconst_internal_test.go:17` | reference `TestReplay_LatencyModelAnchors`, which never existed — the real test is `TestLatencyModel_Anchors` |
| `.github/workflows/nightly.yml:98,:105` | job `replay-live` / step "live-mode replay" runs the DETERMINISTIC driver; rename (live mode is SP-17's `--live` deliverable) |
| `.github/workflows/ci.yml` bench-gate comment | cites a `testdata/bench-baseline.txt` header ("isolated fsync ~2.1 ms") that does not exist — the file has no header |
| `test/guards/stubs_test.go:275` | claims `devtool lint` requires OWNERS.tsv to list every package; the real enforcer is `TestV1_StubGraphIsInertAndOwned` |
| `internal/obs/obs_test.go` | `TestBudgets_AllSixPresentAndConfigDriven` now asserts seven budgets — rename or note (plans record the mismatch) |

---

## B. Deferred by design — do NOT "fix" silently

Unchanged from before: `plans/CARRIED-DEFECTS.tsv` holds 2 fixed, 6 `deferred:V3-VERIFY`
(SP04-D2/D3/D5/D6, SP06-D1, SP05-D1) — those belong to V3-VERIFY, not to this round. A7 above
makes the guard actually enforce that. The `hotpath_test.go:712/:719` wall-clock pair stays
accepted by the 2026-08-22 user decision (V2-report §16.4).

---

## C. Standing constraints — verbatim and non-negotiable

> "Never weaken the check. Do not add `//nolint`, do not add `t.Skip`, do not add a
> `//nomagic:allow`, do not lower a threshold, do not regenerate a golden to match broken
> output, do not delete an assertion."

> "Do not add Co-Authored-By lines or any attribution trailers to any commit message."

CI greps for `Co-Authored-By`, `Signed-off-by`, `Generated with`, 🤖 — substring match, every
commit body. Commit format (hook-enforced): `type(scope): subject`, subject 1–64 chars, body
lines ≤100, `Refs:` footer on `feat`/`fix` (name the owning subplan, e.g. `Refs: SP-02, §11.2`).

**Method — what worked last round, do it again:** one Opus subagent per independent defect,
`isolation: "worktree"`, own branch per defect (`fix/<slug>`), merged back `--no-ff`, never
squashed; give each agent the diagnosis above verbatim ("verify this, don't re-derive it");
each agent also writes its unified diff to a scratchpad path as a backup; adversarial review of
every fix (did it weaken a check? is the root cause established?); re-check every `file:line`
against the tree; reproduce before fixing; mutation-test every assertion (A6 and A7 name their
mutations); `-count=20` under `-race` for anything timing- or concurrency-shaped; commit each
fix separately with the diagnosis in the message; `docs(...)` commits separate from `fix(...)`.

**Definition of done per item:** reproduced (or stated why not) → fixed → mutation-tested →
whole-tree `CGO_ENABLED=1 go test -race -timeout=40m ./...` exit 0 → `go build ./...`, `go vet`,
`gofmt -l`, `go run ./tools/devtool lint` clean **on Windows** → committed on its own branch,
merged `--no-ff`, trailer-grep clean. A8's re-baselines additionally re-run the replay gate and
record the delta per ADR 0003. Anything not closed: written into `plans/V2-report.md` §16.4
with its reason. **No undocumented gaps.**

---

## D. Blocked on the user / environment — do not attempt silently

1. **GitHub Actions quota** — runners were not allocating as of 2026-08-22. Until it returns,
   verify locally (`go run ./tools/devtool ci-local`; WSL2 at `/mnt/host/c/...` for Linux paths;
   note it runs as root so permission fixtures no-op).
2. **Branch protection on `develop`** — still unconfigured (`gh api …/branches/develop/protection`
   → 404). Ordered after the quota returns; a required check no runner can report would be worse
   than an unprotected branch.
3. **Pushing tags/branches** — `v0.1.0`, `v0.0.1`, six `wave1/*` tags and most branches are
   local-only. Pushing any `v*` tag fires `release.yml` and cuts a real Release. Ask first.
4. **`connectDeadlineMs` validation rule** — an explicit config of 5 ms on Windows still gets
   exactly what it asks for; whether `Validate` should flag it (Loud, never a silent clamp) is a
   product question carried in V2-report §16.4. Ask before implementing.

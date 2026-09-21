# SP-18 independent whole-branch review (2026-09-14)

Range 9c84e31..dca2146 (14 commits). Reviewer: a fresh Agent-tool child dispatched with the `opus` alias, requested `claude-opus-4-8` / high; the child observed its own model id as `claude-opus-5[1m]` (R1's documented fallback shape: an Opus-family / high reviewer in a thread that authored none of the branch). No subagents dispatched. Reviewed in passes (plan and spec → delivery record and ledger → the eight pages → `test/docs` → the devtool/config/CI diff), reading the working tree at `dca2146`. Ran `go test -count=1 ./test/docs/...` once (ok, 2.047s) and nothing else. Nothing mutated.

## Strengths

- Claims are sourced, and the sources say what is attributed. About twenty-five citations spot-checked across all eight pages (hooks and manifest timeouts; the four closed enumerations in `internal/core/evidence.go`; the seventeen layout directories and `EnsureLayout`'s comment; the ten redaction rules; the nine `internal/contract/ids.go` assertions; the seven `selftest.go` check ids; `runtime.rehydrate.minTokens`/`maxTokens`/`checkpoint.budgetTokens`; `runtime.mcp.maxResponseBytes` 262144; the CI job/runner/Go-pin table; ADR 0010's 3.072 / 18.432 / 15 ms row; ADR 0011's "complete drop report is always persisted"; V5-report §24/§25/§26/§27/§29 items 2/3/6/7/8/11; `capability.go`'s two verbatim sentences; `MIGRATION-EVIDENCE.md`:95/:434/:471). Every one held. The only unsupported attribution is troubleshooting §9's stop path (Important 1).
- The §12 forbidden-claim sweep is clean: grepping all eight pages for performance, saving, cost, guarantee, improvement and latency-figure shapes returns only limit statements (`docs/cannot-do.md:207-262`, `docs/architecture.md:283-312`). No page states native cuts, markers, eviction, model compliance, exact native bytes, reconstruction, universal improvement or completeness as a capability. The `ephemeral`, `unavailable` ≠ `absent` and "Qompack-added material" qualifications are pinned verbatim by `test/docs/userguide_test.go:56-72`.
- The harness verifies real content and derives its inventories: `limits_test.go:44-68` checks the §12 noun list against `Qompack.md` before the page; `limits_test.go:161-249` re-walks self-test's path (zero `daemon.Services` → `StandardAssertions` → `gated`) and fatals on an empty derivation; `troubleshooting_test.go` parses `evidence.go`, `selftest.go`, `ids.go`; `userguide_test.go:97-131` ties "not yet routed" to `docs/commands.md` in both directions; `inventory_test.go:38-60` fatals on an empty inventory; `pointers_test.go` closes the direction `TestRelativeLinksResolve` cannot see.
- Generator changes are source-driven and additive; `genconfigdocs_test.go:167-250` pins each rendered cell against the accessor in both directions. The slug fix is correct for GitHub, and `genmcpdocs_test.go:166-215` re-derives the slug from the emitted heading.
- Blocked things are stated as blocked: SP18-M7-03/-05 `blocked`, -01 `partially met`; 12/12 UAT rows `not executed — capability unverified` with snapshot and date; no CI result claimed; the routing record states the observed fallback.
- A real defect (`config.LoadForCapture` has no per-leaf fallback; `docs/troubleshooting.md:349-388`) was found by writing the docs and routed to V6-VERIFY.
- The cross-page rollback contradiction found by the task-6 review is fixed (`troubleshooting.md:541-546`, `uat.md:74-79`).

## Issues

### Critical
None.

### Important
1. `docs/troubleshooting.md:442` — "An operator-facing stop path is planned (SP-17)." SP-17's plan (read end to end) owns fsck/doctor (:54), install/upgrade/uninstall and recovery instructions (:36, :127) and the release benchmark (:80), but nothing about a daemon stop command. Fix: drop the sentence, or write "no subplan currently owns one".
2. `docs/troubleshooting.md:116-117` — §1 calls `config-violations.json` "the record of every leaf that fell back to its default"; §6 (:331-333) says the newer-`settingsVersion` whole-block reset surfaces only as a day-log `warn`, and section-level fallbacks are not decoded into the file (`internal/config/validate.go:481-483`). Fix: "every leaf-level fallback; a whole-block reset is a day-log warning, see §6."
3. `docs/user-guide.md:399-403` — two separate probes called "the same probe". Fix: "in a separate probe" or drop the phrase.
4. `plans/V6-SP-18-documentation-and-uat.md` ticks "Future docs/tests/generators/CI are validated in the implementation session" with no inline caveat while CI never ran on this branch. Fix: annotate "(docs/tests/generators validated locally; the CI step itself is unexercised — the branch is unpushed)".

### Minor
5. `docs/config-reference.md:187` (generated) opens "Two blocks carry their own `settingsVersion`" above a derived table; "The blocks below carry…" costs nothing (generator change + regeneration).
6. `docs/architecture.md:47` "a hook has 15 ms at p99" reads as measured; it is budget B-A → "has a 15 ms p99 budget".
7. `README.md:112-126` gated-switch table and "seven hooks" (`README.md:77`, `architecture.md:15`) are hand-maintained restatements with no test pinning them; a one-line `test/docs` check over the generated gate table would close the gap.
8. The plan's Commit-plan checkboxes 1–7 are still `[ ]` while the Done checklist ticks "Seven future conventional commits"; tick or annotate.
9. `README.md:3-15` states injection as a flat capability; the "installed behavior unknown" qualifier arrives at :50-55; a half-clause at first mention would make the opening self-qualifying.
10. Delivery record §2 says "6 task reviewers" for seven tasks; §1's "subjects inside the 64-character limit" is loose (the limit applies after the type/scope prefix; two subjects are 66 characters total and compliant, `checkcommitmsg.go:21`).

## Deferred-minor triage

Fix before merge: T3 user-guide "same probe" (= Important 3); T4 :117-118 "every leaf that fell back" (= Important 2); T4 :441-442 "stop path planned (SP-17)" (= Important 1). Already resolved: T3 user-guide `bench` label (no `SP-05` in the page). Every other ledger item may stay deferred (latent harness edge cases; generator hygiene; prose precision; formatting) — reasons recorded per item in the reviewer's report as delivered to the coordinator.

## Rollback / privacy / capability contradictions returned to the coordinator

None. The five surfaces (troubleshooting §8–§9, config-reference gated/versioned sections, uat.md's rollback rule and UAT-11/12, cannot-do's trust boundary and §5, architecture's write-set section) tell one story.

## Assessment

**Ready to merge (after SP-17 lands first): With fixes.** The supported-claim seat (SP18-M7-07) passes on substance: no claim promises what Qompack.md §12 forbids, no fabricated UAT result, no broken link, no gate reported met that is not, and every sampled citation says what is attributed. The three prose fixes and one plan-file annotation are each a sentence or less and should land before merge. The remaining blockers — SP-17's bundle, an authorized human UAT run, the integration order — are recorded as blocked rather than inferred, so they gate the signoff, not this review.

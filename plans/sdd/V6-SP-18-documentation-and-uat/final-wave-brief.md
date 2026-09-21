# Final wave — one fix dispatch after the whole-branch review

Worktree `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`, HEAD dca2146 (14 commits on develop 9c84e31). One implementer, one commit (or two if the coordinator says so below), then one scoped re-review. No attribution trailers; subject ≤64 chars after `type(scope): `; never `git stash`; never push; never kill a process you did not start.

## Part A — fixed items (ruled by the coordinator before the review returned)

1. **Track the SP-18 evidence.** Copy from `.superpowers/sdd/V6-SP-18-documentation-and-uat/` into `plans/sdd/V6-SP-18-documentation-and-uat/` (create it): `progress.md` (the SDD ledger, verbatim), `task-1-brief.md` … `task-7-brief.md`, `task-1-report.md` … `task-7-report.md`, `task-7-facts.md`, `final-review-inputs.md`, `final-review.md` (the whole-branch review report — the coordinator places it in the `.superpowers` directory before dispatching you), the per-task review packages `review-*.diff` EXCEPT the whole-branch `review-9c84e31..dca2146.diff` (derivable from git; too large). Do not edit their content except: in `task-7-report.md` §7 concern 5, replace the sentence claiming `README.md:59`'s `docs/release.md` lies outside the scan with one saying the Task 7 reviewer replicated the scan and all three README SP-17 paths are covered (keep the original text struck through or quoted so the record shows the correction).
2. **`plans/sdd/README.md`** — add one row to the "What is here" table, at the end: directory `V6-SP-18-documentation-and-uat/`, subplan SP-18, why: the SDD ledger, briefs, reports and per-task review packages behind `delivery-record.md`; the delivery record's evidence pointers ("logs in task-2-report.md", "ledger") resolve here; no transcript or prompt text.
3. **Delivery record wording** (`plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md`): (a) line ~26: the 64-character limit applies to the text after `type(scope): ` (`tools/devtool/checkcommitmsg.go:21`); (b) lines ~196-198: the missing-file direction is `TestRelativeLinksResolve`, the planned-pointer direction is `TestNoPlannedPointerToAnExistingPage` — name both; (c) every pointer to `task-N-report.md`, "ledger" or the review packages now names the tracked path `plans/sdd/V6-SP-18-documentation-and-uat/…`; (d) append a section `## Independent final review (2026-09-14)` with the verdict and the fix-before-merge/deferred split the coordinator supplies in Part B, and the fix commit SHA(s) once known ("this commit").
4. **Plan file** (`plans/V6-SP-18-documentation-and-uat.md`): tick the seven Commit-plan checkboxes (Commits 1–7 are delivered; annotate Commit 7's with "(SP-17 integration BLOCKED; docs checks wired)") and, if the final review approves, tick "Independent final reviewer approves user-facing evidence and rollback instructions" with the review's date and where its report lives; otherwise leave it unticked with the reason.
5. **Tests**: `go test -count=1 ./test/docs/...` must stay green (the pages are unchanged by Part A); `go run ./tools/devtool fmt-check`; `docmarkers`/`runpatterns` lint sub-checks read `plans/` — run `go run ./tools/devtool lint --only=docmarkers` and `--only=runpatterns` if those `--only` names exist (check `tools/devtool/lint.go`), since Part A adds tracked Markdown under `plans/`.

## Part B — findings from the whole-branch review (verdict: "With fixes"; full report in `final-review.md` in this directory)

Fix all of the following. Items B1–B4 are the review's Important findings; B5, B6, B8, B10 are Minor findings the coordinator folds in because each is a one-line edit; B7 and B9 stay deferred (record them as deferred in the delivery record's final-review section).

- B1. `docs/troubleshooting.md:442` — "An operator-facing stop path is planned (SP-17)." SP-17's plan makes no such commitment. Replace with "No subplan currently owns an operator-facing stop path." (no SP-NN attribution).
- B2. `docs/troubleshooting.md:116-117` — the violations file is "the record of every leaf that fell back to its default"; §6 says a whole-block `settingsVersion` reset surfaces only as a day-log warning and section-level fallbacks are not decoded into the file (`internal/config/validate.go:481-483`). Rewrite to "every leaf-level fallback; a whole-block reset is a day-log warning, see §6" (link §6's anchor).
- B3. `docs/user-guide.md:399-403` — "in the same probe" is false (two separate probes). Change to "in a separate probe on this tree" or drop the phrase.
- B4. `plans/V6-SP-18-documentation-and-uat.md` — the ticked box "Future docs/tests/generators/CI are validated in the implementation session, not this plan pass" gets the inline annotation "(docs/tests/generators validated locally; the CI step itself is unexercised — the branch is unpushed)".
- B5. `tools/devtool/genconfigdocs.go` — the generated sentence "Two blocks carry their own `settingsVersion`" becomes "The blocks below carry their own `settingsVersion`" (no count); regenerate `docs/config-reference.md`; `gen-config-docs --check` must pass; adjust any test string that pins the old sentence.
- B6. `docs/architecture.md:47` — "a hook has 15 ms at p99" → "a hook has a 15 ms p99 budget (B-A)"; keep the citation.
- B8. = Part A item 4 (tick Commit-plan checkboxes 1–7 with Commit 7's annotation).
- B10. Delivery record: "6 task reviewers" → "7 task reviewers"; the 64-character sentence per Part A item 3(a).
- Deferred (record, do not fix): B7 (a `test/docs` check pinning README's gated-switch table and "seven hooks" to generated/source inventories); B9 (README's opening states injection before its :50-55 qualifier).

The delivery record's new `## Independent final review (2026-09-14)` section (Part A item 3(d)) states: verdict "With fixes"; the four Important findings and the four folded minors fixed in this commit; B7/B9 deferred; the reviewer's triage of the ledger's deferred minors (three fix-before-merge, all covered by B1–B3; the rest may stay deferred; the `bench`-label item already resolved); "no rollback/privacy/capability contradiction returned"; the reviewer's observed routing (`opus` alias → `claude-opus-5[1m]`, requested `claude-opus-4-8` / high). Then tick the plan's "Independent final reviewer approves user-facing evidence and rollback instructions" box with the annotation "(2026-09-14, 'With fixes', fixes landed in this commit; report: plans/sdd/V6-SP-18-documentation-and-uat/final-review.md)".

## Commit

One commit. Subject exactly: `docs(sp18): land the final review fixes and track the SDD evidence` (text after the prefix is under 64 characters). Body: the B-items fixed, the deferred two, the tracked evidence directory, the README row. No trailers.

## Validation

```
go test -count=1 ./test/docs/... ./tools/devtool/
go run ./tools/devtool gen-config-docs --check
go run ./tools/devtool fmt-check
```
plus the `docmarkers`/`runpatterns` sub-checks per Part A item 5 if `--only` supports them. Paste command lines and tails into your report file `final-wave-report.md` in this directory.

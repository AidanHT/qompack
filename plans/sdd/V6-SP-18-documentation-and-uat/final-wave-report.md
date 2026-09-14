# SP-18 final wave — report

Commit `19f5af9` `docs(sp18): land the final review fixes and track the SDD evidence`
(15 commits on develop 9c84e31; parent dca2146). No trailers. Nothing pushed. Working tree clean.

## Part B — file:line before → after

| # | Site | Before | After |
|---|---|---|---|
| B1 | `docs/troubleshooting.md:442-443` (was 442) | "…identified by the `pid` in `daemon.lock`. An operator-facing stop path is planned (SP-17)." | "…identified by the `pid` in `daemon.lock`. No subplan currently owns an operator-facing stop path." |
| B2 | `docs/troubleshooting.md:117-120` (was 117-118) | "It is the §11.3 record of every leaf that fell back to its default (…), written by any command that loads configuration through `LoadConfigAndReport` with a project root." | "It is the §11.3 record of every leaf-level fallback (…), written by any command that loads configuration through `LoadConfigAndReport` with a project root. A whole-block reset is a day-log warning and not an entry here — see [§6](#6-configuration-and-schema-compatibility)." Paragraph rewrapped to the page's ~100-column prose width. |
| B3 | `docs/user-guide.md:403` | "…created `.qompack/` and started the daemon too, observed on this tree in the same probe." | "…observed in a separate probe on this tree." (`user-guide.md:400`'s other "same probe", which covers `config print`/`version` against `status`, is correct and untouched — the ledger and the review both name :402-403.) |
| B4 | `plans/V6-SP-18-documentation-and-uat.md:180` | `- [x] Future docs/tests/generators/CI are validated in the implementation session, not this plan pass.` | same, plus `(docs/tests/generators validated locally; the CI step itself is unexercised — the branch is unpushed)` |
| B5 | `tools/devtool/genconfigdocs.go:225-226`; generated `docs/config-reference.md:187-188` | "Two blocks carry their own `settingsVersion` and are versioned independently, so a schema change to one never resets the other." | "The blocks below carry their own `settingsVersion` and are versioned independently, so a schema change to one never resets the other." The two `WriteString` calls were rebalanced so the emitted line wrap stays sane; page regenerated; `--check` passes. No test pinned the old sentence (a grep for "carry their own" over `tools/` and `test/` returns nothing). |
| B6 | `docs/architecture.md:47` | "Its stated reason for existing is budget B-A: a hook has 15 ms at p99 and cannot open a store…" | "Its stated reason for existing is a budget: a hook has a 15 ms p99 budget (B-A) and cannot open a store…" See concern 3. |
| B8 | `plans/V6-SP-18-documentation-and-uat.md:108, 112, 116, 120, 124, 128, 132` | seven `- [ ]` Commit-plan boxes | seven `- [x]`; Commit 7's (:132) carries `(SP-17 integration BLOCKED; docs checks wired)` |
| B10 | `plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md:39` and `:30-33` | "6 task reviewers"; "all with subjects inside the 64-character limit the commit-msg hook enforces" | "7 task reviewers"; "all inside the commit-msg hook's 64-character limit, which it applies to the subject text *after* the `type(scope): ` prefix (`tools/devtool/checkcommitmsg.go:21`) — two subjects are 66 characters in total and compliant" |
| B7, B9 | — | — | Deferred, recorded verbatim in the delivery record's `## Independent final review (2026-09-14)` section under **Deferred (recorded, not fixed)**. No page or test changed for either. |

Plan tick for the final reviewer: `plans/V6-SP-18-documentation-and-uat.md:181` is now
`- [x] Independent final reviewer approves user-facing evidence and rollback instructions.
(2026-09-14, 'With fixes', fixes landed in this commit; report:
plans/sdd/V6-SP-18-documentation-and-uat/final-review.md)`.

## Part A — file list

New, tracked under `plans/sdd/V6-SP-18-documentation-and-uat/` (30 files), copied from
`.superpowers/sdd/V6-SP-18-documentation-and-uat/`:

- `progress.md` (the ledger, verbatim, unedited), `final-review-inputs.md`, `final-review.md`
- `task-1-brief.md` … `task-7-brief.md`, `task-1-report.md` … `task-7-report.md`, `task-7-facts.md`
- the twelve per-task review packages `review-7ace16a..9848254.diff` … `review-0621f39..dca2146.diff`
- **excluded** as briefed: `review-9c84e31..dca2146.diff` (the whole-branch diff, derivable from git)

`cmp` against the sources reports SAME for all 29 and DIFF only for `task-7-report.md`, which
carries the one authorized correction (§7 concern 5): the original sentence claiming
`README.md:59`'s `docs/release.md` falls outside the pointer scan is struck through with `~~…~~`
and followed by **Correction (final wave, 2026-09-14)** stating that the Task 7 reviewer
replicated the scan and all three of `README.md:59`'s SP-17 paths are covered, with the reason
(the separator before the third path is shorter than `maxListGap` and has no sentence-ending
period, so `plannedPointerTargets` follows it). I re-derived this independently from
`test/docs/pointers_test.go:81-129` and `README.md:57-59`; the correction is right.

Modified:

- `plans/sdd/README.md:20` — one new "What is here" row for `V6-SP-18-documentation-and-uat/`,
  appended at the end of the table.
- `plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md` — 3(a) 64-character wording;
  3(b) both directions named at `:200-204` (`TestRelativeLinksResolve` for a link whose target is
  missing, `TestNoPlannedPointerToAnExistingPage` for a "planned" pointer whose target now exists);
  3(c) every `task-N-report.md`, "ledger" and review-package pointer now spells
  `plans/sdd/V6-SP-18-documentation-and-uat/…` (`:55`, `:76`, `:90`, `:95`, `:171`); 3(d) the new
  `## Independent final review (2026-09-14)` section at the end of the file.
- `plans/V6-SP-18-documentation-and-uat.md` — B4, B8 and the final-reviewer tick.
- `docs/troubleshooting.md`, `docs/user-guide.md`, `docs/architecture.md`,
  `docs/config-reference.md` (generated), `tools/devtool/genconfigdocs.go` — Part B.

38 files changed, 10474 insertions(+), 42 deletions(-).

## Validation

Run once before the commit, then re-run after the last two consistency edits; the tails below are
the second run.

```
$ go test -count=1 ./test/docs/... ./tools/devtool/
ok  	github.com/qompack/qompack/test/docs	3.246s
ok  	github.com/qompack/qompack/tools/devtool	4.024s

$ go run ./tools/devtool gen-config-docs --check
gen-config-docs: docs/config-reference.md is up to date
exit=0

$ go run ./tools/devtool fmt-check
(no output) exit=0

$ go run ./tools/devtool lint --only=docmarkers
== devtool lint: docmarkers ==
docmarkers: 172 plan document(s) scanned
PASS docmarkers

$ go run ./tools/devtool lint --only=runpatterns
== devtool lint: runpatterns ==
runpatterns: 865 -run patterns parsed, 695 of 695 checkable resolved against 68 packages,
  1 skipped (package not in tree yet), 7 skipped (unlanded wave and the package's test set is
  not settled), 2 platform-excluded, 18 waived
PASS runpatterns
```

`--only=docmarkers` and `--only=runpatterns` both exist (`tools/devtool/lint.go:34-35`). Both
sub-checks walk every `.md` under `plans/` (`collectPlanDocs`, `planchecks.go:419-441`), so they
did scan the 22 newly tracked Markdown files; neither reported a new problem and no waiver was
added. The `test/docs` strict link test (`TestRelativeLinksResolve`) is what validates B2's new
`#6-configuration-and-schema-compatibility` anchor — it resolves anchors against the target file's
real headings, so its pass is the proof the anchor is right.

## Self-review of the diff

- Every B-item is present and is the briefed text. B7 and B9 appear only as recorded deferrals.
- No page is reworded beyond the listed sentences. The docs diff is exactly four hunks in three
  hand-written pages plus the regenerated `config-reference.md` hunk; nothing else in `docs/` or
  `README.md` moved. The only extra churn is reflowing the two paragraphs the edits made
  over-long, which changes no other words.
- The tracked evidence copies are byte-identical to their sources except `task-7-report.md`
  (verified by `cmp` over all 30 files, listed above).
- The delivery record's final-review section matches the brief: verdict "With fixes"; the four
  Important and four folded minors listed as fixed in this commit; B7/B9 deferred; the reviewer's
  triage of the ledger's deferred minors (three fix-before-merge, all covered by B1–B3; the
  `bench`-label item already resolved; the rest may stay deferred); "no rollback/privacy/capability
  contradiction returned"; and the reviewer's routing (requested `claude-opus-4-8` / high, alias
  `opus`, observed `claude-opus-5[1m]`).

## Concerns

1. **Four consistency edits beyond the brief's literal list, all made necessary by the tick.**
   I made them rather than leave statements this commit falsifies; each is easy to revert.
   - `plans/V6-SP-18-documentation-and-uat.md:170` — the SP18-M7-01–07 exit criterion's blocker
     note said "M7-07 is pending the final whole-branch review"; now "M7-07 is met — the final
     whole-branch review returned 'With fixes' and its fixes landed". The box stays unticked, still
     blocked on M7-03/M7-05.
   - `plans/V6-SP-18-documentation-and-uat.md:203-207` — the closing bullet "The independent final
     review is still to come" now says it has run, with the verdict and a link to its report.
   - `delivery-record.md:95` — SP18-M7-07's disposition moved from "**pending the final
     whole-branch review**" to "**met**", pointing at the new section. This is the one edit that
     changes a gate disposition; the row's own text said its verdict would be appended by the
     branch's last commit, which is this commit, but if the coordinator wants M7-07 left pending
     until the scoped re-review closes, this is the line to revert.
   - `delivery-record.md:39` and §8 — "1 final whole-branch reviewer still to come" loses
     "still to come", and §8's checkbox dispositions move the final-reviewer line from the
     "left unticked" list to the ticked list (plus a line for the Commit-plan boxes and a sentence
     recording B4's CI caveat), so §8 matches the plan file after this commit.
2. **`delivery-record.md:95` also reads "(seven independent reviewers)"** — B10 only named §2's
   "6 task reviewers", but §4 carried the same count for the same fact, so I applied B10's ruling
   to both. If seven is wrong, both sites need it.
3. **B6's doubled citation.** The briefed replacement is "a hook has a 15 ms p99 budget (B-A)" with
   the citation kept, but the sentence already opened "Its stated reason for existing is budget
   B-A:". Applying the phrase verbatim would have written the citation twice in one clause, so the
   three-word lead-in became "is a budget:" and the citation lives inside the briefed phrase. The
   briefed phrase is intact and "B-A" appears exactly once. Flagging in case the coordinator wanted
   the lead-in untouched and the "(B-A)" dropped instead.
4. **The per-task review packages are commit-range diffs** and therefore *are* reconstructible from
   git, unlike SP-05's intermediate-state diffs that `plans/sdd/README.md` says are not. Tracking
   them is the brief's ruling and the new row does not claim otherwise; noting it because the
   README's "How to read SP-05's ledger" section makes the opposite claim about its own diffs.
5. **No CI, no push, no `git stash`, no process killed.** The SP-17 session's worktree was not
   touched. `go test` was run on `./test/docs/...` and `./tools/devtool/` only, per R2 — the change
   set is prose, one generator string and tracked Markdown, and nothing else can break.

## Routing

Requested: `claude-opus-4-8`, effort high. Observed: my own model id is `claude-opus-5[1m]`
(model name "Opus 5 (1M context)"); the harness exposes no effort control and no way to select
`claude-opus-4-8` from a running session. This is the same R1 fallback shape the delivery record
already records for every other SP-18 child, and it is stated as a fallback, not as a substitution.

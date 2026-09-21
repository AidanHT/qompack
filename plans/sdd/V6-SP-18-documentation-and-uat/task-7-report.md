# Task 7 report — Commit 7 `ci(sp18): validate the supported documentation set`

Commit: **`dca2146`** `ci(sp18): validate the supported documentation set`
Branch: `feat/sp18-documentation-and-uat` (14 commits on `develop..HEAD`), worktree
`C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`. Working tree clean; nothing pushed;
no `git stash` used at any point.

## Routing

- **Requested:** model `claude-opus-4-8`, effort high.
- **Observed:** this session's own model id is `claude-opus-5[1m]` (the harness's stated model for
  this agent). No effort value is exposed to the running agent, so the effort half is unverifiable
  from inside. The delivery record states this as R1's explicit fallback: "requested
  `claude-opus-4-8` / high; observed: harness alias `opus`, resolved ID unverified."

## 1. What I implemented

1. **CI docs job** (`.github/workflows/ci.yml`) — one step appended after the three `--check` runs:
   `- run: go test -count=1 ./test/docs/...`. Nothing else in the workflow touched;
   `.github/workflows/release.yml` untouched.
2. **New test** `test/docs/pointers_test.go` — `TestNoPlannedPointerToAnExistingPage`. It scans
   every page in `ownedDocs` (defenced, with inline links blanked to equal-length spaces so byte
   offsets and therefore line numbers survive), finds each `planned`/`planned (SP-NN …)` marker, and
   walks the path list that the marker introduces (`docs/<name>.md` or `README.md`, with or without
   surrounding backticks). A path whose file exists on disk fails with `file:line`. Two bounded
   gaps keep the scan tight: the marker-to-first-path gap may contain only `:`, `*`, a backtick and
   whitespace (≤8 bytes, ≤1 newline); the path-to-path gap may be ≤60 bytes, may not contain a
   sentence-ending period and may not cross a blank line. Stdlib only, consistent with the package
   rule; it reuses the existing `repoRoot`, `defenced`, `ownedDocs` and `inlineLinkRe` helpers.
3. **Pointer conversions** — seven sites (table below), plus README's missing documentation links.
4. **One cross-page reconciliation** — troubleshooting §9 and `docs/uat.md` step 2.
5. **Delivery record** — `plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md` (new,
   tracked), written from `task-7-facts.md` verbatim where the facts supply text, plus the
   requirement-to-doc map that SP18-M7-01's disposition points at ("the delivery record's table
   below"), which I derived from the pages' own section headings.
6. **Plan file** — `plans/V6-SP-18-documentation-and-uat.md`: seven checkboxes ticked, four left
   unticked with `(blocked: …)` annotations, and a short `## Delivery record (2026-09-14)` section
   appended that links the record relatively.

## 2. Pointer sites converted (before → after)

All line numbers are pre-change.

| # | Site | Before | After |
|---|---|---|---|
| 1 | `docs/architecture.md:285-286` | `The full page is planned (SP-18 Commit 5):` / `` `docs/cannot-do.md`. `` | `The full page is [docs/cannot-do.md](cannot-do.md).` (rewrapped onto one line, 96 cols) |
| 2 | `docs/user-guide.md:33` | `The full list is planned (SP-18 Commit 5): docs/cannot-do.md.` | `The full list is [docs/cannot-do.md](cannot-do.md).` |
| 3 | `docs/user-guide.md:468` | `- planned (SP-18 Commit 4): docs/troubleshooting.md` | list item removed; `- [docs/troubleshooting.md](troubleshooting.md) — what each observation means and what to do` added to the live "Where to look next" list |
| 4 | `docs/user-guide.md:469` | `- planned (SP-18 Commit 5): docs/cannot-do.md` | `- [docs/cannot-do.md](cannot-do.md) — the capabilities this build does not have, and where each limit is recorded` |
| 5 | `docs/user-guide.md:470` | `- planned (SP-18 Commit 6): docs/uat.md` | `- [docs/uat.md](uat.md) — the acceptance scenarios and the evidence a run has to leave` |
| 6 | `docs/troubleshooting.md:583` | `- planned (SP-18 Commit 5): docs/cannot-do.md` | same link line, moved into the live list |
| 7 | `docs/troubleshooting.md:584` | `- planned (SP-18 Commit 6): docs/uat.md` | same link line, moved into the live list |

Also, as the brief requires:

- `docs/user-guide.md:465` sentence reworded: "…because none of these files exists:" →
  "…because these files are not on this tree:" (the remaining pointer is SP-17's three files).
- `README.md` "Where to read next" gained the five missing links: user guide, troubleshooting,
  cannot-do, upstream issues, UAT.

Left as plain text deliberately (SP-17-owned, files absent):
`README.md:57-59`, `docs/user-guide.md:11-12`, `docs/troubleshooting.md:443`, `:509`, `:541`,
`docs/troubleshooting.md` "Where to look next" SP-17 line, `docs/uat.md:9`, `docs/uat.md:1112-1113`.
`docs/cannot-do.md:383` (the convention paragraph) names no now-existing page, so it was not
touched.

## 3. RED / GREEN evidence for the pointer test

**RED** — test written before any page was changed:

```
$ go test -count=1 -run TestNoPlannedPointerToAnExistingPage ./test/docs/...
--- FAIL: TestNoPlannedPointerToAnExistingPage (0.11s)
    pointers_test.go:69: docs/architecture.md:286: names docs/cannot-do.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/user-guide.md:33: names docs/cannot-do.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/user-guide.md:468: names docs/troubleshooting.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/user-guide.md:469: names docs/cannot-do.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/user-guide.md:470: names docs/uat.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/troubleshooting.md:583: names docs/cannot-do.md as planned, but that file exists: make it a link
    pointers_test.go:69: docs/troubleshooting.md:584: names docs/uat.md as planned, but that file exists: make it a link
FAIL
FAIL	github.com/qompack/qompack/test/docs	0.883s
```

Seven sites — exactly the seven the brief's review notes predicted. (An earlier iteration of the
test reported only six: the first-gap rule did not allow the opening backtick, so
`architecture.md`'s `` `docs/cannot-do.md` `` spelling escaped it. That was a real hole in the
check, not a false negative in the pages, and it was fixed before the conversions were made — the
RED above is the fixed test on the unconverted pages.)

**GREEN** — same command after the conversions, and the whole package:

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	2.588s
```

Negative control after conversion: the test only stops matching because the pointers became links
(`blankLinks` removes links from the scan), so reverting any one conversion re-reds it; the SP-17
pointers still match the marker and are passed over solely because `os.Stat` fails on their targets.

## 4. Validation commands (run once, on the final tree before committing)

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	2.588s          exit=0
$ go vet ./test/docs/...
                                                            exit=0
$ go run ./tools/devtool fmt-check
                                                            exit=0
$ go run ./tools/devtool gen-config-docs --check
gen-config-docs: docs/config-reference.md is up to date     exit=0
$ go run ./tools/devtool gen-mcp-docs --check
gen-mcp-docs: docs/mcp-tools.md is up to date               exit=0
$ go run ./tools/devtool gen-command-docs --check
gen-command-docs: docs/commands.md is up to date            exit=0
```

Re-run after the two cosmetic tidies made during self-review (rewrapping architecture §10 onto one
line, reordering troubleshooting's new list entries):

```
$ go test -count=1 ./test/docs/... && go run ./tools/devtool fmt-check && echo ALLGREEN
ok  	github.com/qompack/qompack/test/docs	2.743s
ALLGREEN
```

**Workflow YAML validation** — Python with PyYAML was available, so the file was parsed rather than
eyeballed:

```
$ python -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok; jobs:', len(d['jobs'])); print('docs steps:', [s.get('run') or s.get('uses') for s in d['jobs']['docs']['steps']])"
yaml ok; jobs: 12
docs steps: ['actions/checkout@v4', 'actions/setup-go@v5', 'go run ./tools/devtool gen-config-docs --check', 'go run ./tools/devtool gen-mcp-docs --check', 'go run ./tools/devtool gen-command-docs --check', 'go test -count=1 ./test/docs/...']
```

The new step is the sixth and last step of the `docs` job, at the same indentation as its
neighbours, and all twelve jobs still parse.

**Extra checks, not required by the brief**, run because this commit adds a file under `plans/` and
edits a plan file — the three `devtool lint` sub-checks that read `plans/`, plus the guard suite:

```
$ go run ./tools/devtool lint --only=docmarkers
docmarkers: 154 plan document(s) scanned
PASS docmarkers
$ go run ./tools/devtool lint --only=runpatterns
PASS runpatterns
$ go run ./tools/devtool lint --only=coveragefloors
coveragefloors: 63 floor claim(s) across 154 plan document(s) checked against plans/OWNERS.tsv
PASS coveragefloors
$ go test -count=1 -timeout=10m ./test/guards/
ok  	github.com/qompack/qompack/test/guards	124.035s
```

(`runpatterns` prints pre-existing notices about quoted historical test names in
`plans/sdd/V5-VERIFY/inventory-SP-1*.md`; the check itself PASSes and none of those lines is mine.)

## 5. Files changed

```
 .github/workflows/ci.yml                                        |   1 +
 README.md                                                       |   9 +
 docs/architecture.md                                            |   3 +-
 docs/troubleshooting.md                                         |   8 +-
 docs/uat.md                                                     |   3 +-
 docs/user-guide.md                                              |  12 +-
 plans/V6-SP-18-documentation-and-uat.md                         |  41 +-
 plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md     | new
 test/docs/pointers_test.go                                      | new
 9 files changed, 404 insertions(+), 21 deletions(-)
```

Nothing outside the brief's owned list was touched. `.github/workflows/release.yml`, `internal/`,
`plugin/`, the generated pages and every other plan are unmodified.

## 6. Self-review findings

Checked line by line against the brief:

- **CI step** — exactly one `- run:` line, after the three `--check` runs, inside the `docs` job.
  ✔
- **No page reworded beyond pointer conversion and the one reconciliation.** Confirmed by reading
  the full `git diff` of README and `docs/`: every hunk is either a pointer becoming a link, a link
  list gaining an entry, the user-guide sentence the brief names, or the reconciliation sentence.
  The largest judgement call was giving each newly linked README/user-guide/troubleshooting list
  entry a short description, since a bare link in those lists would have been the only entry
  without one; the descriptions are deliberately flat ("the capabilities this build does not have,
  and where each limit is recorded") and make no capability claim.
- **Two cosmetic tidies I made and want on the record**: architecture §10's sentence was rewrapped
  onto one 96-column line once the link made it short enough, and troubleshooting's two new list
  entries were moved above the architecture/ADR entries to match the ordering user-guide and uat
  already use. Neither changes a word of prose.
- **`docs/uat.md`** — the brief permitted "one clause at uat.md's rollback rule if needed". I added
  it (step 2), because the reconciliation should read the same from either page; it is one clause,
  and it leaves `planned (SP-17)` as plain text.
- **SP-17 pointers** — all eight remaining plain-text sites verified as SP-17-owned files that do
  not exist. `TestNoPlannedPointerToAnExistingPage` passes them for exactly that reason, which is
  the behaviour the brief asked for ("this forces the conversion the moment a planned page lands").
- **Checkbox scope.** The facts' list names seven boxes to tick and four to annotate; those eleven
  are the only ones I touched. I deliberately left the seven **Commit-plan** checkboxes
  (lines 108–132, "Inventory existing docs/ADRs…", … "Integrate after SP17…") unticked, because the
  facts do not list them and the brief says "tick only the plan checkboxes the facts say are met".
  Commits 1–6 are demonstrably delivered, so a reader may find those boxes surprising — flagging it
  for the coordinator rather than deciding it myself. See Concerns.
- **Delivery-record placement.** The brief's two passages differ slightly: "Files you own" says
  append a section and tick the checkboxes, while "Delivery-record placement" says the appended
  section "carries the ticked/annotated checkboxes". I did both without duplicating state: the
  checkboxes are ticked/annotated **in place** in the Exit criteria and Done checklist, and the
  appended section links the record and summarises the three findings the dispositions turn on. The
  full ticked/annotated list is §8 of the delivery record.
- **Commit** — exactly one commit, exact subject, no trailers, no period, 51 characters. The first
  attempt was made with `-c core.hooksPath=.githooks`, which does not exist in this repo and would
  have bypassed the `commit-msg` hook; I re-ran it as `git commit --amend --no-edit` with the real
  hook path so the hook actually validated the subject, and amended once more to correct a commit
  count (see Concerns).
- **Never pushed. Never stashed. No process I did not start was touched.**

## 7. Concerns

1. **Commit-plan checkboxes left unticked** (plan lines 108–132). The facts do not mention them and
   the brief scopes ticks to the facts, so I left them. If the coordinator wants Commits 1–6 (and
   this one) ticked there, it is a one-line-per-box follow-up; I did not want to widen the plan edit
   unilaterally.
2. **"Fourteen commits", not thirteen.** The facts' commit table plus `git log` yields 7 numbered +
   5 review fixes + 1 prerequisite fix + 1 preliminary = 14 on `develop..HEAD`. My first draft of
   the record and the commit body said "thirteen" (the count excluding the preliminary). Both were
   corrected by amend; the record now says "Fourteen commits on the branch: the seven numbered ones,
   five review fixes, one prerequisite fix and the preliminary plan revision."
3. **The requirement-to-doc map is mine, not the facts'.** SP18-M7-01's disposition in the facts
   points at "the delivery record's table below", but the facts supply no such table. I built §5 of
   the record from the pages' own section headings, so every cell is checkable against a heading
   that exists; it is the one part of the record not supplied verbatim, and it deserves a look in the
   final review.
4. **`plans/sdd/README.md` has a "What is here" table** listing each committed `sdd/` directory. I
   did not add a row for `V6-SP-18-documentation-and-uat/`, because that file is outside the brief's
   owned list. Adding one is a reasonable follow-up.
5. **The pointer test is adjacency-based and therefore bounded.** A planned pointer that names its
   target more than 60 bytes away, across a blank line, or after a sentence-ending period will not
   be caught. That is deliberate — the alternative (scanning whole paragraphs) produced a false
   positive on `docs/user-guide.md:12`, where a genuine `[README.md](../README.md)` link sits in the
   same paragraph as an SP-17 pointer. ~~The cost is that `README.md:59`'s third path
   (`docs/release.md`, which sits after "plus the packaged bundle and") is currently outside the
   gap the scan will follow; its two siblings on the same line are covered, so a stale README
   pointer would still be caught, just not by that one path.~~ **Correction (final wave,
   2026-09-14):** that sentence is wrong and is struck through rather than deleted so the record
   shows the correction. The Task 7 reviewer replicated the scan and all three of `README.md:59`'s
   SP-17 paths — `docs/install.md`, `docs/security.md` and `docs/release.md` — are covered: the
   separator before the third path (", plus the packaged bundle and ") is shorter than `maxListGap`
   and contains no sentence-ending period, so `plannedPointerTargets` follows it. The bound itself
   is still real; this page is not an example of it. Worth a line in the final review's
   triage alongside the other deferred harness edge cases.
6. **No CI run exists for this branch.** The docs step is validated by parsing the workflow and by
   running the same command locally; nothing here claims a CI result.
7. **"Integrate after SP-17" remains BLOCKED**, as ruled. The final independent whole-branch review
   has not run; SP18-M7-07 is recorded as pending and the record says its verdict is appended by the
   branch's last commit.

## 8. Requested / observed routing line

Requested: `claude-opus-4-8`, effort high. Observed: model id `claude-opus-5[1m]`; no effort control
is exposed to the agent, so the effort half is unverified. Recorded in the delivery record as R1's
explicit fallback rather than as a silent substitution.

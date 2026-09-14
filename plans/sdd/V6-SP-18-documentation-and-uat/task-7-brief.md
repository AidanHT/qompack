# Task 7 — Commit 7 `ci(sp18): validate the supported documentation set`

Plan text (verbatim): "Integrate after SP17, preserve command/security ownership, run future docs checks and independent final review, attach V6 evidence."

Coordinator ruling (binding): SP-17 has not started on this tree (no branch, no bundle, no docs/install.md, docs/security.md or docs/release.md). "Integrate after SP17" is therefore recorded as BLOCKED on SP-17 in the delivery record, not claimed. Everything else in this commit proceeds: the CI docs job runs the docs harness, README/architecture/user-guide pointers to the SP-18 pages that now exist become links, and the delivery record (routing, run map, dispositions) is attached to the plan file. SP-17-owned pages stay "planned (SP-17)" plain text.

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`.

## Files you own

- `.github/workflows/ci.yml` — the `docs` job only: add one step `- run: go test -count=1 ./test/docs/...` after the three `--check` runs. Touch nothing else in the workflow.
- `README.md`, `docs/architecture.md`, `docs/user-guide.md`, `docs/troubleshooting.md`, `docs/cannot-do.md`, `docs/upstream-issues.md`, `docs/uat.md` — ONLY to convert "planned (SP-18 …): docs/<page>.md" plain-text pointers into relative links now that the pages exist (known sites from the reviews: `docs/architecture.md` §10 pointer to cannot-do; `docs/troubleshooting.md` "Where to look next" pointers to cannot-do and uat; `docs/user-guide.md` ~:33 and ~:469 pointers to cannot-do/troubleshooting/uat, plus its sentence "none of these files exists", which must be reworded; `docs/cannot-do.md` closing convention paragraph if it names a now-existing page; grep every owned page for `planned (SP-18` to find the rest), and to add the missing links to README's "documentation" list (user guide, troubleshooting, cannot-do, upstream issues, UAT). Do not otherwise reword these pages. SP-17 pointers (`docs/install.md`, `docs/security.md`, `docs/release.md`) remain plain text.
- `test/docs/pointers_test.go` — new: `TestNoPlannedPointerToAnExistingPage` — scan every page in `ownedDocs` for the pattern `planned[^)]*\)?:?\s*` followed by a `docs/<name>.md` or `README.md` path (match the exact spelling the earlier commits used — grep the pages first and cover every variant you find); if the referenced file exists on disk, fail with `file:line`. This forces the conversion the moment a planned page lands.
- `plans/V6-SP-18-documentation-and-uat.md` — append a section `## Delivery record (2026-09-14)` and tick the checkboxes that are met, per the facts below. Do not edit any other part of the plan file.

Do NOT edit generated pages, `internal/`, `plugin/`, other workflows, other plans. Never `git stash`. Never push.

## Delivery record — required content (facts are supplied by the coordinator in the "Facts" section at the end of this brief; use them verbatim, do not invent)

1. **Commits** — the seven SP-18 commits by short SHA and subject (from `git log --oneline develop..HEAD`), plus the preliminary plan-revision commit.
2. **Routing** — the requested model/effort per role and the observed routing: every child dispatched through the harness alias `opus` with the requested identity `claude-opus-4-8`; the harness exposes no effort control and does not confirm the resolved model ID, so the record says "requested claude-opus-4-8 / <effort>; observed: harness alias opus, resolved ID unverified" — this is the explicit fallback R1 requires rather than a silent substitution. Fable 5.1 was the coordinator only. Concurrency: one implementer at a time, one reviewer per task, plus the final whole-branch reviewer.
3. **R2 run map** — a table: row (commit or gate ID) → command(s) run → snapshot (short SHA) → result (from the facts). Mark focused checks vs the one broader check (`lint`) and the two generator `--check` runs. No timeout, zero-test run or old-tip result is counted as a pass; if any command was not run for a row, the row says so.
4. **Gate dispositions SP18-M7-01…07** — one line each: met / partially met / blocked, with the evidence pointer (page, test name, or the reason). SP18-M7-03 (installed package) and SP18-M7-05 (human UAT) are blocked on SP-17's artifact and an authorized human run; SP18-M7-07's independent reader review is the final whole-branch review (record its verdict from the facts).
5. **UAT** — all twelve rows drafted; none executed; every row records capability unverified.
6. **Blocked / carried / discovered** — SP-17 integration; UAT execution; installed-host claims; anything the facts list. Discovered product defects (documented, not fixed by SP-18, routed to V6-VERIFY with the owner named in the facts): (i) `config.LoadForCapture` has no per-leaf fallback, so a single config problem (unknown key, newer `settingsVersion`, invalid value, refused gated switch) makes every hook admit nothing and create no `.qompack/` while printing `{}` and exiting 0, and `self-test` still reports `config.load ok` — recorded in `docs/troubleshooting.md` §6 from six one-config-file probes (Task 4); (ii) the MCP anchor slug bug was fixed on this branch (`ddb1a41`), record it as fixed, not carried.
7. **Checkboxes** — tick only the plan checkboxes the facts say are met; leave the others unticked and add "(blocked: …)" after the text.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
go run ./tools/devtool gen-config-docs --check
go run ./tools/devtool gen-mcp-docs --check
go run ./tools/devtool gen-command-docs --check
```
Also validate the workflow edit is syntactically sound: `python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))"` if Python with PyYAML is available, otherwise a careful visual check of indentation against the neighbouring steps; say which you did.

## Commit

Exactly one commit, subject exactly `ci(sp18): validate the supported documentation set`. Body: the CI step, the pointer conversions, the pointer test, the delivery record, and the BLOCKED line for SP-17 integration. No trailers.

## Facts (supplied by the coordinator)

Read `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18\.superpowers\sdd\V6-SP-18-documentation-and-uat\task-7-facts.md` — it holds the commit table, the routing record, the R2 run map, the gate dispositions, the UAT dispositions, the blocked/carried/discovered list and the checkbox instructions. Use them verbatim; derive the final commit list from `git log --format='%h %s' develop..HEAD`. Where the facts say the final whole-branch review is pending, write exactly that: it runs after this commit and its verdict is appended to the delivery record by the branch's last commit.

## One cross-page reconciliation (ruled by the coordinator from Task 6's review)

`docs/uat.md` (how-to-run steps 2 and 5, and UAT-12's pre-run copy) defines a manual pre-run copy of `.qompack/` plus an index-file comparison as its rollback rule, while `docs/troubleshooting.md` §9 says the page "does not describe a manual copy-and-restore procedure" and that an operator procedure is planned (SP-17). Add ONE sentence to troubleshooting §9 (and, if needed, one clause at uat.md's rollback rule) stating that the operator's own pre-run copy plus index comparison is UAT's interim procedure, linking `uat.md`, and that it is not the planned SP-17 operator procedure. No other rewording of either page.

## Delivery-record placement

Write the delivery record as `plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md` (tracked; mirrors SP-17's `plans/sdd/…` convention), and append to `plans/V6-SP-18-documentation-and-uat.md` only a short `## Delivery record (2026-09-14)` section that links it (relative link `sdd/V6-SP-18-documentation-and-uat/delivery-record.md`) and carries the ticked/annotated checkboxes per the facts. Add `plans/sdd/V6-SP-18-documentation-and-uat/delivery-record.md` to nothing else; `test/docs` scans README and docs/** only.

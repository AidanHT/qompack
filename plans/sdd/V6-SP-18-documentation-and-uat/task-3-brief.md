# Task 3 — Commit 3 `docs(sp18): explain scoped recovery and command behavior`

Plan text (verbatim): "Write user guide against installed schemas, authority/fidelity/coverage semantics and recovery diagnostics; retain test output."

Plan §1 row (verbatim): "docs/user-guide.md — Seven commands/eight tools, current vs historical reads, fidelity/coverage/error states, current-authority corrections, additional-context budget/overflow".

Plan prose that binds this page (verbatim): "No product flow exposes Codex planning models or internal migration mechanics unless useful for a user decision. Describe subscription usage separately from estimated API price and invoice reconciliation. Explain that 8–12K is a historical Qompack-added target, not total restored native context. `dropped` remains the command name but reports qualified coverage. An `ephemeral` tag does not mean native eviction."

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`.

## Files you own

- `docs/user-guide.md` — new.
- `test/docs/userguide_test.go` — new test file in the existing `test/docs` package (created by Commit 1; read `test/docs/*.go` first and reuse its helpers: `repoRoot`, `generatedCommandNames`, `generatedToolNames`, the markdown link/heading helpers).
- `test/docs/owned_test.go` (or wherever `ownedDocs` lives) — append `"docs/user-guide.md"` to the list. Nothing else in that file.

Do NOT edit `docs/commands.md` or `docs/mcp-tools.md` (generated; link to them), README.md, docs/architecture.md, `plugin/`, `internal/`, `.github/`, `plans/`. Never `git stash`. Never push.

## Facts the coordinator verified on this tree (cite the source you re-check, do not cite "the coordinator")

- `docs/commands.md` (generated from `internal/commands.Specs()`) lists the slash commands; `/qompack:checkpoint` is marked **not yet routed** (its subcommand column) — the user guide must say the command is installed but has no route in this build (SP-14 H3, recorded in plans/V5-report.md §29 item 3). Every command accepts `--json` (versioned envelope) and `--help`; exit codes `0` success, `2` malformed invocation, `1` anything else.
- `docs/mcp-tools.md` (generated from `internal/mcp.ToolDefs`) lists the MCP tools, the two behaviours that apply to every tool (ephemeral metadata describes Qompack records, not host eviction; a failed `already_tried` query returns `unavailable`, which never establishes absence or prohibits an approach), and minimal spans with `full: true` and `next_span`.
- `qompack help` (build with `go build ./cmd/qompack`, run it) shows the operator commands: `admin delivery-seal`, `bench`, `config print`, `config schema`, `daemon`, `doctor` (prints "not implemented in this build"), `dropped`, `eval`, `eval import`, `fsck` (also "not implemented in this build"), `mcp`, `pin`, `recall`, `self-test` (the only command that may exit non-zero), `status`, `version`, `why`; the hook entry points (`checkpoint`, `flush`, `observe prompt|stop|tool`, `session-start`) always exit 0. Any subcommand accepts `--set <dotted.key>=<value>`.
- Running `qompack status` or `qompack config print` in a directory with no `.qompack/` creates `.qompack/` there (index, logs, metrics) and starts the project daemon. Verify this yourself in a `t.TempDir()`-style scratch directory OUTSIDE the repository, then stop the daemon you started (`Get-Process qompack | Stop-Process` on Windows) and delete the directory. The guide must warn the reader that running these commands is a write, and where.
- Enumerations (confirm in `internal/core`, quoted in plans/00-ARCHITECTURE.md line 62): `core.Fidelity` ∈ {exact, prefix, partial, redacted, truncated, binary, failure, unknown}; `core.Coverage` ∈ {qompack_included, archive_only, native_load_observed, expired_deleted, unknown}; `core.EvidenceOutcome` ∈ {ok, absent, unavailable, denied, corrupt, expired, uncertain}; `core.Authority` ∈ {user_correction, explicit_decision, tool_observation, candidate_extraction, hypothesis, conflict}.
- Rehydration budget: `docs/adr/0011-rehydration-budget-and-item-order.md` (`Request.Budget` is a hard cap never raised; "never truncated" means never partially emitted; prefix truncation; a path rule is restored whole or not at all). `checkpoint.budgetTokens` default is in `docs/config-reference.md`. 8–12K is a historical Qompack-added target for injected material, not the total restored native context.
- State authority and uncertainty: plans/V5-report.md §24 (uncertainty is `partial`: it "does not survive the digest surface under a blind ledger") and `docs/adr/0013-migration-contracts.md`.
- Usage accounting: plans/V5-report.md §25 ("Request usage and estimated price") and Qompack.md §5 / §11.2 — subscription usage (what the host shows) and estimated API price (what Qompack computes from a pricing table) are different numbers; invoice reconciliation is a third thing Qompack does not do. Do not quote a rate.

## Required content of `docs/user-guide.md`

1. **Who this is for and what to expect** — a Claude Code session with the plugin installed; what Qompack adds after compaction (a checkpoint-derived block within a budget) and what it does not do (no native cut, no proof the model complied, no guarantee of improvement — one sentence each, pointing to "planned (SP-18 Commit 5): docs/cannot-do.md" as plain text, not a link).
2. **Slash commands** — one subsection per command in `docs/commands.md` order, each: what it does, arguments, `--json` shape pointer (link the generated page's anchor rather than re-documenting the schema), exit codes, and the states it can report. `/qompack:checkpoint`: installed, not routed in this build. `/qompack:dropped`: reports qualified coverage (the `Coverage` enumeration) — what "dropped" means and does not mean. `/qompack:eval`: replay harness; what "baseline" it compares against (read `internal/commands/spec.go` and `docs/commands.md`).
3. **MCP tools** — one subsection per tool in `docs/mcp-tools.md` order: purpose, when a model should call it, the ephemeral rule, minimal spans/`full`/`next_span`, and the `unavailable` semantics for `already_tried` and `expand`.
4. **Current vs historical reads** — `re_read` is the store's own version history, never a live disk read; `recall`/`expand` return archive material with fidelity and coverage; how to tell a current read from a historical one in the response metadata (read `docs/mcp-tools.md` sections and `internal/mcp` response types; cite what you read).
5. **Fidelity, coverage and error states** — one table per enumeration with a one-line user meaning each; then "unavailable is not absent" and "a denied read is not an empty read" as explicit rules.
6. **Current-authority corrections** — the `Authority` enumeration; a `user_correction` outranks a `hypothesis`; repeated compact/resume/fork does not promote obsolete intent; the recorded partial (uncertainty under a blind ledger) stated plainly with its source.
7. **Additional-context budget and overflow** — the rehydration budget rules from ADR 0011 in user terms; what happens on overflow (explicit truncation by prefix, never silent); that the budget is Qompack-added material, not native context.
8. **Operator commands** — `status` (what its sections mean; "unavailable" latency rows are honest gaps, not failures), `config print [--provenance]`, `config schema`, `self-test`, `version`, `admin delivery-seal` (daemon must be stopped), `eval import`, `bench`; `doctor` and `fsck` print "not implemented in this build" (SP-17). Say which commands create `.qompack/` and start a daemon.
9. **Usage accounting** — subscription usage vs estimated API price vs invoice reconciliation, as three separate notions; no rate quoted.
10. **Where to look next** — links to `docs/config-reference.md`, `docs/commands.md`, `docs/mcp-tools.md`, `docs/architecture.md`; plain-text "planned" pointers for troubleshooting/cannot-do/uat (Commits 4–6) and install/security (SP-17).

## Tests (`test/docs/userguide_test.go`; write first, RED, then GREEN)

1. `TestUserGuideCoversEveryGeneratedCommandAndTool` — for each name from `generatedCommandNames(t)` the guide contains a heading or code span naming `/qompack:NAME`; for each name from `generatedToolNames(t)` the guide contains a heading or code span naming it. Derived, no counts.
2. `TestUserGuideStatesTheBindingQualifications` — the guide contains, verbatim, the four sentences below (put them in the guide exactly; they are the plan's binding qualifications):
   - `An ephemeral tag describes a Qompack record; it does not mean the host evicted anything.`
   - `unavailable is not absent: a failed query leaves prior attempts unknown and never prohibits an approach.`
   - `The rehydration budget is a target for Qompack-added material, not the total restored native context.`
   - `Subscription usage and estimated API price are separate numbers; Qompack does not reconcile invoices.`
3. `TestUserGuideMarksCheckpointAsUnrouted` — the guide's `/qompack:checkpoint` section contains the phrase `not yet routed` iff `docs/commands.md`'s table row for `/qompack:checkpoint` contains `not yet routed` (derive the condition from the generated page so the test flips automatically when SP-14 H3 lands).
4. The existing `TestRelativeLinksResolve` and `TestOwnedDocsExist` must pass with the new page.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
```
Prose-only commit plus one test file: do not run lint, the whole tree, race, coverage or replay.

## Claims policy (binding)

No performance/latency/cost-saving/name-availability guarantee. No native O(delta), free cuts, first-turn savings, exact-native-context, or Bloom safe-false-positive claims. No Codex planning models or migration mechanics. Every capability claim cites a file (generated page, ADR, report section, or package) or is marked unknown with the action that would verify it. Files that do not exist are plain "planned (SP-NN)" text, never links. No fixed counts in tests.

## Commit

Exactly one commit, subject exactly `docs(sp18): explain scoped recovery and command behavior`. Body: sections written, the enumerations and ADRs relied on, the tests added, and the two recorded limits (checkpoint unrouted; doctor/fsck unimplemented). No trailers.
